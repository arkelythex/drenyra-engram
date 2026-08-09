// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the configuration and claim
// layer of the stateless OIDC access-token slice: the OIDCConfig trust
// contract, NormalizeOIDCConfig — the single fail-closed startup gate — the
// mandatory HTTPS URL gate, and the pure JWT-claim parsing/validation
// primitives shared by the validator (splitJWT, checkAudience, jsonStringClaim,
// jsonTimeClaim). No network or crypto state lives here; the RS256/JWKS
// machinery is in oidc_jwks.go and the validator entry (constructor, claims,
// Validate) is in oidc.go.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OIDC configuration constants (fail-closed defaults and bounds).
const (
	// DefaultOIDCTenantClaim is the custom claim name carrying the tenant id
	// when DRENYRA_OIDC_CLAIM_TENANT is unset.
	DefaultOIDCTenantClaim = "tenant_id"
	// DefaultOIDCCompanyClaim is the custom claim name carrying the company id
	// when DRENYRA_OIDC_CLAIM_COMPANY is unset.
	DefaultOIDCCompanyClaim = "company_id"
	// DefaultOIDCClockSkew is the time-claim tolerance when
	// DRENYRA_OIDC_CLOCK_SKEW is unset.
	DefaultOIDCClockSkew = 30 * time.Second
	// MaxOIDCClockSkew bounds the configurable skew; a larger configured value
	// fails startup (an unbounded skew would weaken exp/nbf/iat enforcement).
	MaxOIDCClockSkew = 5 * time.Minute

	// jwtAlgRS256 is the ONLY accepted signing algorithm. It is shared by the
	// JWKS key filter (oidc_jwks.go) and the Validate algorithm gate (oidc.go).
	jwtAlgRS256 = "RS256"
)

// OIDCConfig is the complete, validated OIDC trust configuration. The zero
// value is INVALID: at minimum Issuer and Audience are required (see
// NormalizeOIDCConfig).
type OIDCConfig struct {
	// Issuer is the exact `iss` value every access token must carry (e.g.
	// https://drenyra.eu.auth0.com/). Required; must be an https URL.
	Issuer string
	// Audience is the exact `aud` value every access token must carry (the
	// resource-server identifier, e.g. https://engram.drenyra.local/api).
	// Required.
	Audience string
	// JWKSURL is the JSON Web Key Set location. Optional; defaults to
	// <issuer>/.well-known/jwks.json (the Auth0/OIDC discovery convention).
	// When set it must be an https URL.
	JWKSURL string
	// TenantClaim is the custom claim name carrying the tenant id. Optional;
	// defaults to DefaultOIDCTenantClaim.
	TenantClaim string
	// CompanyClaim is the custom claim name carrying the company id. Optional;
	// defaults to DefaultOIDCCompanyClaim.
	CompanyClaim string
	// ClockSkew is the bounded tolerance for exp/nbf/iat. Optional; defaults to
	// DefaultOIDCClockSkew and must not exceed MaxOIDCClockSkew.
	ClockSkew time.Duration
	// HTTPClient is used for JWKS fetches. Optional; defaults to a client with
	// jwksClientTimeout. Tests inject the httptest TLS server's client so the
	// self-signed test certificate is trusted.
	HTTPClient *http.Client
	// Now supplies the validation clock. Optional; defaults to time.Now.
	Now func() time.Time
}

// NormalizeOIDCConfig validates cfg and returns the effective configuration
// with defaults applied. It is the single fail-closed gate: an incomplete or
// invalid configuration is an error — the server must never start with a
// partial trust contract. Any caller that receives a normalized config may
// hand it to NewOIDCValidator without re-validation.
func NormalizeOIDCConfig(cfg OIDCConfig) (OIDCConfig, error) {
	issuer := strings.TrimSpace(cfg.Issuer)
	if issuer == "" {
		return OIDCConfig{}, errors.New("oidc issuer is required")
	}
	if err := requireHTTPSURL("oidc issuer", issuer); err != nil {
		return OIDCConfig{}, err
	}
	audience := strings.TrimSpace(cfg.Audience)
	if audience == "" {
		return OIDCConfig{}, errors.New("oidc audience is required")
	}
	jwksURL := strings.TrimSpace(cfg.JWKSURL)
	if jwksURL == "" {
		jwksURL = strings.TrimSuffix(issuer, "/") + "/.well-known/jwks.json"
	}
	if err := requireHTTPSURL("oidc jwks url", jwksURL); err != nil {
		return OIDCConfig{}, err
	}
	tenantClaim := strings.TrimSpace(cfg.TenantClaim)
	if tenantClaim == "" {
		tenantClaim = DefaultOIDCTenantClaim
	}
	companyClaim := strings.TrimSpace(cfg.CompanyClaim)
	if companyClaim == "" {
		companyClaim = DefaultOIDCCompanyClaim
	}
	skew := cfg.ClockSkew
	if skew == 0 {
		skew = DefaultOIDCClockSkew
	}
	if skew < 0 || skew > MaxOIDCClockSkew {
		return OIDCConfig{}, fmt.Errorf(
			"oidc clock skew %s is out of bounds: must be between 0 and %s",
			skew, MaxOIDCClockSkew)
	}
	return OIDCConfig{
		Issuer:       issuer,
		Audience:     audience,
		JWKSURL:      jwksURL,
		TenantClaim:  tenantClaim,
		CompanyClaim: companyClaim,
		ClockSkew:    skew,
		HTTPClient:   cfg.HTTPClient,
		Now:          cfg.Now,
	}, nil
}

// requireHTTPSURL reports an error when raw is not an absolute https URL.
// HTTPS is mandatory for the issuer and the JWKS endpoint; a loopback-http
// configuration is never accepted in production configuration parsing.
func requireHTTPSURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s %q is not a valid URL: %w", name, raw, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%s %q must be an absolute https URL", name, raw)
	}
	return nil
}

// splitJWT splits the compact JWT into its three raw base64url segments. The
// returned segments are the EXACT original strings — they form the signing
// input and are never re-encoded.
func splitJWT(token string) (header, payload, sig string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", errors.New("token is not a three-part compact JWT")
	}
	return parts[0], parts[1], parts[2], nil
}

// checkAudience enforces the EXACT configured audience: a string claim must
// equal it; an array claim must be exactly the single configured audience. A
// multi-audience token fails closed (the token is ambiguous about its intended
// resource server).
func checkAudience(claims map[string]json.RawMessage, expected string) error {
	raw, ok := claims["aud"]
	if !ok {
		return errors.New("token carries no aud claim")
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single != expected {
			return fmt.Errorf("token audience %q does not match the configured audience", single)
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return errors.New("token aud claim is neither a string nor an array of strings")
	}
	if len(many) != 1 || many[0] != expected {
		return fmt.Errorf("token audience must be exactly the configured audience; got %v", many)
	}
	return nil
}

// jsonStringClaim returns the string value of a claim. An absent claim returns
// "" (callers decide requiredness); a present non-string claim is an error.
func jsonStringClaim(claims map[string]json.RawMessage, key string) (string, error) {
	raw, ok := claims[key]
	if !ok {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("token claim %q is not a string", key)
	}
	return s, nil
}

// jsonTimeClaim returns the NumericDate value of a claim (seconds since Unix
// epoch, integer or fractional — the JWT spec allows both), normalized to UTC.
// An absent claim returns nil; a present non-number claim is an error.
func jsonTimeClaim(claims map[string]json.RawMessage, key string) (*time.Time, error) {
	raw, ok := claims[key]
	if !ok {
		return nil, nil
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err != nil {
		return nil, fmt.Errorf("token claim %q is not a number", key)
	}
	f, err := num.Float64()
	if err != nil {
		return nil, fmt.Errorf("token claim %q is not a valid number: %w", key, err)
	}
	t := time.Unix(int64(f), 0).UTC()
	return &t, nil
}
