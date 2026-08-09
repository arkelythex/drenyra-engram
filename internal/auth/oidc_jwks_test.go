// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the RS256 key machinery of
// the stateless OIDC access-token slice (oidc_jwks.go) IN ISOLATION: the JWKS
// cache with its exactly-one-refresh rotation path, the bounded fetch
// fail-closed behavior, the RS256 signature verifier and the JWK wire
// decoding. Validators here are built DIRECTLY (no NewOIDCValidator/Validate —
// those live in module 3, oidc.go/oidc_test.go) so the cache machinery is
// proven before the constructor exists. The shared RSA/JWT fixture helpers
// (testRSAKey, testJWKSServer, signTestJWT, jwkFromKey) live here and are also
// used by the end-to-end validator matrix and the resolver OIDC tests.
package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// testRSAKey generates a fresh RSA signing key (RS256).
func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return key
}

// testJWKSServer is an httptest TLS server that serves a JWKS document with one
// RSA key per private key (kids test-key-1, test-key-2, ...). rotate replaces
// the served key set, mimicking provider key rotation. The client returned by
// Client() trusts the test certificate.
//
// The served kid is derived per-key from the fixture's entry list, so a
// rotation can serve a NEW key under a NEW kid (the unknown-kid → one-refresh
// rotation path) without index collision. fetchCount reports how many JWKS
// fetches the validator triggered — the exactly-one-refresh proof.
type testJWKSServer struct {
	t    *testing.T
	ts   *httptest.Server
	mu   sync.Mutex
	kids []string
	keys []*rsa.PrivateKey
	// fetches counts served JWKS requests.
	fetches int
}

func newTestJWKSServer(t *testing.T, keys ...*rsa.PrivateKey) *testJWKSServer {
	t.Helper()
	srv := &testJWKSServer{t: t, keys: append([]*rsa.PrivateKey(nil), keys...)}
	for i := range srv.keys {
		srv.kids = append(srv.kids, fmt.Sprintf("test-key-%d", i+1))
	}
	srv.ts = httptest.NewTLSServer(http.HandlerFunc(srv.serve))
	t.Cleanup(srv.ts.Close)
	return srv
}

func (s *testJWKSServer) serve(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetches++
	keys := make([]jwkKey, 0, len(s.keys))
	for i, key := range s.keys {
		kid := "test-key-1"
		if i < len(s.kids) {
			kid = s.kids[i]
		}
		keys = append(keys, jwkFromKey(kid, key))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jwksDocument{Keys: keys})
}

// rotate replaces the served key set with the given kid → key pairs. It is the
// fixture for the key-rotation path: a rotated provider serves a NEW key under
// a NEW kid, and the validator's unknown-kid refresh must pick it up.
func (s *testJWKSServer) rotate(pairs map[string]*rsa.PrivateKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kids = nil
	s.keys = nil
	for kid, key := range pairs {
		s.kids = append(s.kids, kid)
		s.keys = append(s.keys, key)
	}
}

// URL is the https issuer served by the test server.
func (s *testJWKSServer) URL() string { return s.ts.URL }

// Client returns an http.Client trusting the test TLS certificate.
func (s *testJWKSServer) Client() *http.Client { return s.ts.Client() }

// fetchCount returns how many JWKS documents the server has served so far.
func (s *testJWKSServer) fetchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetches
}

// jwkFromKey renders one RSA JWK (kid, sig use, RS256) from a private key.
func jwkFromKey(kid string, key *rsa.PrivateKey) jwkKey {
	return jwkKey{
		Kty: "RSA",
		Kid: kid,
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
	}
}

// signTestJWT builds a compact RS256 JWT carrying the given claims. A non-empty
// headerAlg overrides the header alg (for the algorithm-confusion cases).
func signTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any, headerAlg string) string {
	t.Helper()
	if headerAlg == "" {
		headerAlg = "RS256"
	}
	header, err := json.Marshal(map[string]string{"alg": headerAlg, "typ": "JWT", "kid": kid})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	headerB64 := base64.RawURLEncoding.EncodeToString(header)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := headerB64 + "." + payloadB64
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// newJWKSValidator builds a validator DIRECTLY (no constructor — the cache
// machinery is tested in isolation, before the module-3 constructor/Validate
// exist) against the fixture JWKS server with a fixed clock.
func newJWKSValidator(t *testing.T, srv *testJWKSServer, now time.Time) *OIDCValidator {
	t.Helper()
	cfg, err := NormalizeOIDCConfig(OIDCConfig{
		Issuer:     srv.URL(),
		Audience:   testOIDCAudience,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NormalizeOIDCConfig: %v", err)
	}
	return &OIDCValidator{
		config: cfg,
		client: cfg.HTTPClient,
		now:    func() time.Time { return now },
		keys:   make(map[string]*rsa.PublicKey),
	}
}

// TestLookupKeyCachesAfterFirstFetch: the first lookup fetches the JWKS and
// caches the key; a second lookup of the SAME kid is a cache hit (no extra
// fetch).
func TestLookupKeyCachesAfterFirstFetch(t *testing.T) {
	key := testRSAKey(t)
	srv := newTestJWKSServer(t, key)
	v := newJWKSValidator(t, srv, time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC))

	got, err := v.lookupKey(context.Background(), "test-key-1")
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if !got.Equal(&key.PublicKey) {
		t.Error("first lookup returned the wrong public key")
	}
	if _, err := v.lookupKey(context.Background(), "test-key-1"); err != nil {
		t.Fatalf("cached lookup: %v", err)
	}
	if srv.fetchCount() != 1 {
		t.Errorf("fetches = %d, want exactly 1 (second lookup must be a cache hit)", srv.fetchCount())
	}
}

// TestLookupKeyOneRefreshRotation: the isolated rotation contract — an unknown
// kid triggers EXACTLY ONE JWKS refresh, the freshly rotated key set replaces
// the cache, and a still-unknown kid (the rotated-out key) fails closed.
func TestLookupKeyOneRefreshRotation(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	key1 := testRSAKey(t)
	key2 := testRSAKey(t)
	srv := newTestJWKSServer(t, key1)
	v := newJWKSValidator(t, srv, now)

	// Prime the cache with key1 (one fetch).
	if _, err := v.lookupKey(context.Background(), "test-key-1"); err != nil {
		t.Fatalf("prime cache with key1: %v", err)
	}
	// Rotate: the provider now serves key2 only, under its NEW kid.
	srv.rotate(map[string]*rsa.PrivateKey{"test-key-2": key2})

	// Unknown kid → exactly one refresh → the rotated key resolves.
	got, err := v.lookupKey(context.Background(), "test-key-2")
	if err != nil {
		t.Fatalf("lookup rotated kid: %v", err)
	}
	if !got.Equal(&key2.PublicKey) {
		t.Error("rotation lookup returned the wrong public key")
	}
	if srv.fetchCount() != 2 {
		t.Errorf("fetches = %d, want exactly 2 (prime + one rotation refresh)", srv.fetchCount())
	}
	// The rotated-out key1: the cache was replaced and one more refresh cannot
	// resurrect it — fail closed.
	if _, err := v.lookupKey(context.Background(), "test-key-1"); err == nil {
		t.Fatal("lookup of the rotated-out kid must fail closed after one refresh")
	}
}

// TestLookupKeyUnknownKidFailsClosed: a kid that is not in the cached or
// freshly refreshed JWKS fails closed after exactly one refresh.
func TestLookupKeyUnknownKidFailsClosed(t *testing.T) {
	key := testRSAKey(t)
	srv := newTestJWKSServer(t, key)
	v := newJWKSValidator(t, srv, time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC))

	if _, err := v.lookupKey(context.Background(), "no-such-kid"); err == nil {
		t.Fatal("lookup of an unknown kid must fail closed after one refresh")
	}
}

// TestRefreshFailsClosedOnFetchErrors: a non-200 JWKS response, an unparseable
// document or a network failure all fail the refresh — the cache is never
// replaced by a partial or garbage set.
func TestRefreshFailsClosedOnFetchErrors(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		serve func(w http.ResponseWriter)
	}{
		{"status 500", func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }},
		{"garbage body", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("this is not a jwks document"))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				tt.serve(w)
			}))
			t.Cleanup(srv.Close)
			cfg, err := NormalizeOIDCConfig(OIDCConfig{
				Issuer:     srv.URL,
				Audience:   testOIDCAudience,
				HTTPClient: srv.Client(),
			})
			if err != nil {
				t.Fatalf("NormalizeOIDCConfig: %v", err)
			}
			v := &OIDCValidator{config: cfg, client: cfg.HTTPClient, now: func() time.Time { return now }, keys: make(map[string]*rsa.PublicKey)}
			if err := v.refresh(context.Background()); err == nil {
				t.Fatal("refresh must fail closed")
			}
		})
	}
	// Network failure: the JWKS endpoint refuses connections.
	refused := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	refusedURL := refused.URL
	refused.Close()
	cfg, err := NormalizeOIDCConfig(OIDCConfig{
		Issuer:     refusedURL,
		Audience:   testOIDCAudience,
		HTTPClient: refused.Client(),
	})
	if err != nil {
		t.Fatalf("NormalizeOIDCConfig: %v", err)
	}
	v := &OIDCValidator{config: cfg, client: cfg.HTTPClient, now: func() time.Time { return now }, keys: make(map[string]*rsa.PublicKey)}
	if err := v.refresh(context.Background()); err == nil {
		t.Fatal("refresh must fail closed on a network error")
	}
}

// TestRefreshFiltersIrrelevantKeys: non-RSA keys, `enc`-use keys and keys
// claiming a non-RS256 alg are irrelevant to RS256 verification and are
// filtered out; the valid RS256 signing key still resolves.
func TestRefreshFiltersIrrelevantKeys(t *testing.T) {
	key := testRSAKey(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwksDocument{Keys: []jwkKey{
			jwkFromKey("sig-1", key),
			{Kty: "EC", Kid: "ec-1", Use: "sig", Alg: "ES256"},
			{Kty: "RSA", Kid: "enc-1", Use: "enc", Alg: "RS256", N: "garbage", E: "garbage"},
			{Kty: "RSA", Kid: "hs-1", Use: "sig", Alg: "HS256", N: "garbage", E: "garbage"},
		}})
	}))
	t.Cleanup(srv.Close)
	cfg, err := NormalizeOIDCConfig(OIDCConfig{
		Issuer:     srv.URL,
		Audience:   testOIDCAudience,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NormalizeOIDCConfig: %v", err)
	}
	v := &OIDCValidator{config: cfg, client: cfg.HTTPClient, now: time.Now, keys: make(map[string]*rsa.PublicKey)}

	got, err := v.lookupKey(context.Background(), "sig-1")
	if err != nil {
		t.Fatalf("lookup of the valid RS256 key: %v", err)
	}
	if !got.Equal(&key.PublicKey) {
		t.Error("lookup returned the wrong public key")
	}
	// Filtered-out kids must NOT resolve (they are not in the cache).
	for _, kid := range []string{"ec-1", "enc-1", "hs-1"} {
		if _, err := v.lookupKey(context.Background(), kid); err == nil {
			t.Errorf("kid %q must be filtered out of the RS256 cache", kid)
		}
	}
}

// TestRefreshFailsClosedOnMalformedSigningKey: a key that CLAIMS to be an RS256
// signing key but is malformed fails the WHOLE refresh (never trust a partial
// set) — even when a valid key is also served.
func TestRefreshFailsClosedOnMalformedSigningKey(t *testing.T) {
	key := testRSAKey(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwksDocument{Keys: []jwkKey{
			jwkFromKey("good-1", key),
			{Kty: "RSA", Kid: "broken-1", Use: "sig", Alg: "RS256", N: "!!!not-base64url!!!", E: "AQAB"},
		}})
	}))
	t.Cleanup(srv.Close)
	cfg, err := NormalizeOIDCConfig(OIDCConfig{
		Issuer:     srv.URL,
		Audience:   testOIDCAudience,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NormalizeOIDCConfig: %v", err)
	}
	v := &OIDCValidator{config: cfg, client: cfg.HTTPClient, now: time.Now, keys: make(map[string]*rsa.PublicKey)}

	_, err = v.lookupKey(context.Background(), "good-1")
	if err == nil {
		t.Fatal("lookup must fail closed: the malformed RS256-signing key fails the whole refresh")
	}
	if !strings.Contains(err.Error(), "jwks key") {
		t.Errorf("error = %v, want a refresh failure naming the malformed key", err)
	}
}

// TestVerifyRS256: the verifier accepts the exact signing input of a valid
// RS256 token and rejects a tampered input, a garbage signature, a wrong key
// and a malformed signature encoding.
func TestVerifyRS256(t *testing.T) {
	key := testRSAKey(t)
	parts := strings.Split(signTestJWT(t, key, "kid-1", map[string]any{"sub": "s"}, ""), ".")
	signingInput := parts[0] + "." + parts[1]
	if err := verifyRS256(&key.PublicKey, signingInput, parts[2]); err != nil {
		t.Fatalf("verifyRS256(valid): %v", err)
	}
	// Tampered signing input: a forged payload re-signed over the ORIGINAL
	// header+sig is still caught because the input differs.
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"evil"}`))
	if err := verifyRS256(&key.PublicKey, tampered, parts[2]); err == nil {
		t.Fatal("verifyRS256 must reject a tampered signing input")
	}
	// Wrong key.
	other := testRSAKey(t)
	if err := verifyRS256(&other.PublicKey, signingInput, parts[2]); err == nil {
		t.Fatal("verifyRS256 must reject a signature made with a different key")
	}
	// Malformed signature encoding.
	if err := verifyRS256(&key.PublicKey, signingInput, "!!!not-base64url!!!"); err == nil {
		t.Fatal("verifyRS256 must reject a non-base64url signature")
	}
}

// TestJWKKeyPublicKey: a well-formed JWK decodes to the matching RSA public
// key; missing, malformed or degenerate modulus/exponent pairs fail closed.
func TestJWKKeyPublicKey(t *testing.T) {
	key := testRSAKey(t)
	valid := jwkFromKey("kid-1", key)
	pub, err := valid.publicKey()
	if err != nil {
		t.Fatalf("publicKey(valid): %v", err)
	}
	if !pub.Equal(&key.PublicKey) {
		t.Error("publicKey returned the wrong key")
	}

	badN := "!!not-base64url!!"
	zeroN := base64.RawURLEncoding.EncodeToString([]byte{0x00})
	tinyE := base64.RawURLEncoding.EncodeToString([]byte{0x02})
	tests := []struct {
		name string
		n    string
		e    string
	}{
		{"missing modulus", "", "AQAB"},
		{"missing exponent", "AQAB", ""},
		{"invalid base64url modulus", badN, "AQAB"},
		{"invalid base64url exponent", "AQAB", badN},
		{"zero modulus", zeroN, "AQAB"},
		{"tiny exponent", "AQAB", tinyE},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := jwkKey{Kty: "RSA", Kid: "kid-1", Use: "sig", Alg: "RS256", N: tt.n, E: tt.e}
			if _, err := k.publicKey(); err == nil {
				t.Fatalf("publicKey(%s) must fail closed", tt.name)
			}
		})
	}
}
