// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the stateless OIDC
// access-token validation surface of the resource-server identity slice:
// RS256-only JWT verification against a cached JWKS, exact issuer/audience
// validation, strict time claims with bounded skew, and configurable
// tenant/company custom claims. It deliberately depends ONLY on the standard
// library — no JWT/JWKS dependency is introduced.
//
// Layout of the OIDC slice in package auth:
//   - oidc_config.go — OIDCConfig, NormalizeOIDCConfig (the fail-closed startup
//     gate), the HTTPS URL gate and the pure JWT-claim parsing primitives.
//   - oidc_jwks.go — the OIDCValidator type with its RS256 JWKS cache, the
//     one-refresh key-rotation path, signature verification and the JWKS wire
//     types.
//   - oidc.go — the validator constructor (NewOIDCValidator), the verified
//     OIDCClaims result type and Validate: the full fail-closed validation
//     order.
//
// Contract (the first Production Identity slice):
//   - Resource-server access tokens only. The validator never stores, logs or
//     returns the raw token; it only verifies it in memory (no session is ever
//     created on the OIDC path — the CLI remains session-based).
//   - RS256 ONLY. Any other `alg` (including `none`, HS*/PS*/ES*) is rejected
//     before any signature or key work — there is no algorithm-confusion path.
//   - `iss` must match the configured issuer exactly; `aud` must be exactly the
//     configured audience (a multi-audience token fails closed).
//   - `exp` is required and enforced with bounded skew; `nbf`/`iat` are
//     enforced when present with the same bounded skew.
//   - The tenant and company custom claims are REQUIRED and must be non-empty
//     strings; the resolver cross-checks both against the DB membership for the
//     token `sub` (ambiguity/mismatch fails closed).
//   - JWKS keys are cached; an unknown `kid` triggers exactly ONE refresh (key
//     rotation), then fails closed if the key is still unknown.
//   - Assurance defaults to `standard`; no ACR/MFA elevation is configured.
//
// Startup fails closed: an incomplete or invalid OIDC configuration is an
// error (see NormalizeOIDCConfig), never a partial trust configuration.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NewOIDCValidator builds a validator from a normalized configuration. An
// invalid configuration is an error (fail closed at construction); callers
// that did not normalize first get the same validation via this function.
func NewOIDCValidator(cfg OIDCConfig) (*OIDCValidator, error) {
	normalized, err := NormalizeOIDCConfig(cfg)
	if err != nil {
		return nil, err
	}
	client := normalized.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: jwksClientTimeout}
	}
	now := normalized.Now
	if now == nil {
		now = time.Now
	}
	return &OIDCValidator{
		config: normalized,
		client: client,
		now:    now,
		keys:   make(map[string]*rsa.PublicKey),
	}, nil
}

// OIDCClaims is the verified identity material the resolver may consume. It is
// assembled ONLY from signature-verified token claims plus the configured
// custom claims; the raw token never leaves Validate.
type OIDCClaims struct {
	// Subject is the verified `sub` claim.
	Subject string
	// TenantID is the verified custom tenant claim value.
	TenantID string
	// CompanyID is the verified custom company claim value.
	CompanyID string
	// IssuedAt is the token `iat` claim when present, else the validation clock,
	// normalized to UTC. It is the token's OWN issue time — never a substitute for
	// the server-observed authentication time (see ValidatedAt).
	IssuedAt time.Time
	// ValidatedAt is the server-observed validation clock (the configured Now),
	// normalized to UTC: the instant the resource server verified this token. The
	// resolver records ValidatedAt as the principal snapshot's authenticatedAt —
	// audit provenance reflects when the server authenticated the caller, not when
	// the token was issued.
	ValidatedAt time.Time
}

// Validate verifies the compact JWT access token and returns the verified
// claims. Every failure is a typed *Error-free descriptive error that the
// resolver maps to PRINCIPAL_INVALID (fail closed). Validate never persists
// the raw token and never performs network I/O other than the at-most-one
// JWKS refresh for an unknown key id.
//
// Ordering is fail-closed: the header pins alg/kid, the SIGNATURE is verified
// before any claim value is parsed or trusted (a forged token only ever sees
// the generic signature verdict — no error oracle about issuer/audience/time/
// custom-claim rules), and only then are the claims validated.
func (v *OIDCValidator) Validate(ctx context.Context, token string) (OIDCClaims, error) {
	headerPart, payloadPart, sigPart, err := splitJWT(token)
	if err != nil {
		return OIDCClaims{}, err
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(headerPart)
	if err != nil {
		return OIDCClaims{}, errors.New("token header is not valid base64url")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return OIDCClaims{}, errors.New("token header is not valid JSON")
	}
	// No algorithm confusion: RS256 ONLY. `none`, HS*/PS*/ES* and unknown
	// algorithms are rejected before any signature or key work.
	if header.Alg != jwtAlgRS256 {
		return OIDCClaims{}, fmt.Errorf("token algorithm %q is not allowed (only %s is accepted)", header.Alg, jwtAlgRS256)
	}
	kid := strings.TrimSpace(header.Kid)
	if kid == "" {
		return OIDCClaims{}, errors.New("token carries no key id")
	}

	// Signature FIRST: cached JWKS key by kid; an unknown kid triggers exactly
	// one refresh (rotation), then fails closed. The signing input is the EXACT
	// original header.payload strings — never a re-encoding.
	key, err := v.lookupKey(ctx, kid)
	if err != nil {
		return OIDCClaims{}, err
	}
	signingInput := headerPart + "." + payloadPart
	if err := verifyRS256(key, signingInput, sigPart); err != nil {
		return OIDCClaims{}, errors.New("token signature verification failed")
	}

	// Claims are parsed ONLY after the signature verified.
	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return OIDCClaims{}, errors.New("token payload is not valid base64url")
	}
	var rawClaims map[string]json.RawMessage
	if err := json.Unmarshal(payloadJSON, &rawClaims); err != nil {
		return OIDCClaims{}, errors.New("token payload is not valid JSON")
	}

	// Exact issuer / audience / subject extraction (fail closed on mismatch).
	issuer, err := jsonStringClaim(rawClaims, "iss")
	if err != nil {
		return OIDCClaims{}, err
	}
	if issuer != v.config.Issuer {
		return OIDCClaims{}, fmt.Errorf("token issuer %q does not match the configured issuer", issuer)
	}
	if err := checkAudience(rawClaims, v.config.Audience); err != nil {
		return OIDCClaims{}, err
	}
	subject, err := jsonStringClaim(rawClaims, "sub")
	if err != nil {
		return OIDCClaims{}, err
	}
	if strings.TrimSpace(subject) == "" {
		return OIDCClaims{}, errors.New("token subject is empty")
	}

	// Strict time claims with bounded skew: exp REQUIRED; nbf/iat enforced when
	// present. iat is additionally bounded against the future so a wildly
	// mis-set clock cannot mint a valid-looking token.
	now := v.now().UTC()
	exp, err := jsonTimeClaim(rawClaims, "exp")
	if err != nil {
		return OIDCClaims{}, err
	}
	if exp == nil {
		return OIDCClaims{}, errors.New("token carries no exp claim")
	}
	if now.After(exp.Add(v.config.ClockSkew)) {
		return OIDCClaims{}, errors.New("token is expired")
	}
	nbf, err := jsonTimeClaim(rawClaims, "nbf")
	if err != nil {
		return OIDCClaims{}, err
	}
	if nbf != nil && now.Before(nbf.Add(-v.config.ClockSkew)) {
		return OIDCClaims{}, errors.New("token is not valid yet (nbf)")
	}
	iat, err := jsonTimeClaim(rawClaims, "iat")
	if err != nil {
		return OIDCClaims{}, err
	}
	if iat != nil && iat.After(now.Add(v.config.ClockSkew)) {
		return OIDCClaims{}, errors.New("token iat is in the future")
	}

	// Custom tenant/company claims: REQUIRED non-empty strings. The resolver
	// cross-checks both against the DB membership for `sub`.
	tenantID, err := jsonStringClaim(rawClaims, v.config.TenantClaim)
	if err != nil {
		return OIDCClaims{}, err
	}
	if strings.TrimSpace(tenantID) == "" {
		return OIDCClaims{}, fmt.Errorf("token lacks the configured tenant claim %q", v.config.TenantClaim)
	}
	companyID, err := jsonStringClaim(rawClaims, v.config.CompanyClaim)
	if err != nil {
		return OIDCClaims{}, err
	}
	if strings.TrimSpace(companyID) == "" {
		return OIDCClaims{}, fmt.Errorf("token lacks the configured company claim %q", v.config.CompanyClaim)
	}

	issuedAt := now
	if iat != nil {
		issuedAt = iat.UTC()
	}
	return OIDCClaims{
		Subject:     subject,
		TenantID:    tenantID,
		CompanyID:   companyID,
		IssuedAt:    issuedAt,
		ValidatedAt: now,
	}, nil
}
