// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the RS256 key machinery of
// the stateless OIDC access-token slice: the OIDCValidator type with its cached
// kid → RSA-public-key store, the one-refresh key-rotation path, the bounded
// JWKS fetch, signature verification and the JWKS wire types. Config and the
// pure claim primitives live in oidc_config.go; the validator constructor, the
// OIDCClaims result type and Validate live in oidc.go.
package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// JWKS fetch bounds (fail-closed DoS guards).
const (
	// maxJWKSBytes bounds the JWKS document read (a DoS guard).
	maxJWKSBytes = 1 << 20 // 1 MiB

	// jwksClientTimeout bounds a single JWKS fetch.
	jwksClientTimeout = 10 * time.Second
)

// OIDCValidator validates RS256 access tokens against the configured issuer,
// audience and JWKS. It is safe for concurrent use: the JWKS cache is guarded
// by a mutex. It holds NO token material — raw tokens exist only as local
// variables during Validate and are never stored, logged or returned. The
// type is defined here with the cache machinery it owns; the constructor
// (NewOIDCValidator) and the Validate method live in oidc.go.
type OIDCValidator struct {
	config OIDCConfig
	client *http.Client
	now    func() time.Time

	mu   sync.Mutex
	keys map[string]*rsa.PublicKey // kid → public key (JWKS cache)
}

// lookupKey resolves the kid from the JWKS cache. An unknown kid triggers ONE
// cache refresh (the rotation path: a freshly rotated key set replaces the
// cached set), then a final lookup. A still-unknown kid fails closed.
func (v *OIDCValidator) lookupKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	key := v.keys[kid]
	v.mu.Unlock()
	if key != nil {
		return key, nil
	}
	if err := v.refresh(ctx); err != nil {
		return nil, err
	}
	v.mu.Lock()
	key = v.keys[kid]
	v.mu.Unlock()
	if key != nil {
		return key, nil
	}
	return nil, fmt.Errorf("unknown key id %q after one jwks refresh", kid)
}

// refresh fetches the JWKS document and replaces the cached key set. The fetch
// is bounded (maxJWKSBytes, jwksClientTimeout, caller context). Keys that
// cannot be used for RS256 verification (non-RSA kty, `enc` use, other alg)
// are filtered out; a key that CLAIMS to be an RS256 signing key but is
// malformed fails the whole refresh (fail closed — never trust a partial set).
func (v *OIDCValidator) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.config.JWKSURL, nil)
	if err != nil {
		return fmt.Errorf("build jwks request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes))
	if err != nil {
		return fmt.Errorf("read jwks: %w", err)
	}
	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("parse jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Use == "enc" {
			// Not an RSA signing key — irrelevant to RS256 verification.
			continue
		}
		if k.Alg != "" && k.Alg != jwtAlgRS256 {
			continue
		}
		pub, err := k.publicKey()
		if err != nil {
			return fmt.Errorf("jwks key %q: %w", k.Kid, err)
		}
		if k.Kid != "" {
			keys[k.Kid] = pub
		}
	}
	v.mu.Lock()
	v.keys = keys
	v.mu.Unlock()
	return nil
}

// verifyRS256 verifies the token signature with RSA PKCS#1 v1.5 over SHA-256 —
// the ONLY accepted combination (the header already guaranteed alg=RS256).
func verifyRS256(pub *rsa.PublicKey, signingInput, sigB64 string) error {
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return errors.New("token signature is not valid base64url")
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return errors.New("rsa signature verification failed")
	}
	return nil
}

// jwksDocument is the wire shape of a JSON Web Key Set document.
type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

// jwkKey is one RSA JSON Web Key as served by the JWKS endpoint. Only the
// fields relevant to RS256 verification are modeled; unknown fields are
// ignored by the decoder.
type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// publicKey decodes the base64url modulus/exponent into an *rsa.PublicKey. A
// malformed or degenerate key is an error (fail closed).
func (k jwkKey) publicKey() (*rsa.PublicKey, error) {
	if strings.TrimSpace(k.N) == "" || strings.TrimSpace(k.E) == "" {
		return nil, errors.New("missing rsa modulus or exponent")
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("invalid rsa modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("invalid rsa exponent: %w", err)
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	n := new(big.Int).SetBytes(nBytes)
	if n.Sign() <= 0 || e < 3 {
		return nil, errors.New("degenerate rsa modulus or exponent")
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}
