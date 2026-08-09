// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the stateless OIDC
// access-token validator (oidc.go) of the first Production Identity slice: a
// REAL RSA-signed JWT fixture served over a TLS httptest JWKS endpoint, the
// exact issuer/audience rules, the strict bounded time claims, the RS256-only
// algorithm gate, the one-refresh key rotation path through the FULL validation
// chain, the configurable custom claim names and the no-raw-token persistence
// contract. Every failure case fails closed. The config/claim primitive matrix
// lives in oidc_config_test.go and the isolated JWKS machinery tests in
// oidc_jwks_test.go.
package auth

import (
	"context"
	"crypto/rsa"
	"fmt"
	"strings"
	"testing"
	"time"
)

// validOIDCClaims returns a claim set that is valid at `now` for the fixture
// issuer/audience: exp +1h, nbf/iat −1m, and the default tenant/company custom
// claims.
func validOIDCClaims(issuer, subject, tenant, company string, now time.Time) map[string]any {
	return map[string]any{
		"iss":        issuer,
		"aud":        testOIDCAudience,
		"sub":        subject,
		"exp":        now.Add(time.Hour).Unix(),
		"nbf":        now.Add(-time.Minute).Unix(),
		"iat":        now.Add(-time.Minute).Unix(),
		"tenant_id":  tenant,
		"company_id": company,
	}
}

// newTestOIDCValidator builds a validator against the fixture JWKS server with
// a FIXED clock; the JWKS URL and claim names exercise the production defaults
// (issuer-derived JWKS URL, tenant_id/company_id claims, 30s skew).
func newTestOIDCValidator(t *testing.T, srv *testJWKSServer, now time.Time, mutate func(*OIDCConfig)) *OIDCValidator {
	t.Helper()
	cfg := OIDCConfig{
		Issuer:     srv.URL(),
		Audience:   testOIDCAudience,
		HTTPClient: srv.Client(),
		Now:        func() time.Time { return now },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	v, err := NewOIDCValidator(cfg)
	if err != nil {
		t.Fatalf("NewOIDCValidator: %v", err)
	}
	return v
}

// TestOIDCValidatorValidAccessToken: a correctly signed RS256 token with the
// exact issuer/audience and the custom tenant/company claims resolves.
func TestOIDCValidatorValidAccessToken(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	key := testRSAKey(t)
	srv := newTestJWKSServer(t, key)
	v := newTestOIDCValidator(t, srv, now, nil)

	token := signTestJWT(t, key, "test-key-1",
		validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now), "")
	claims, err := v.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims.Subject != "subject-1" || claims.TenantID != "tenant-1" || claims.CompanyID != "acme" {
		t.Errorf("claims = %+v, want subject-1/tenant-1/acme", claims)
	}
	// iat is the authentication time, normalized to UTC.
	wantIat := now.Add(-time.Minute).UTC()
	if !claims.IssuedAt.Equal(wantIat) {
		t.Errorf("IssuedAt = %v, want %v (iat)", claims.IssuedAt, wantIat)
	}
}

// TestOIDCValidatorRejectsInvalidTokens is the fail-closed matrix: signature,
// algorithm, issuer, audience, time and required-claim violations.
func TestOIDCValidatorRejectsInvalidTokens(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	key := testRSAKey(t)
	otherKey := testRSAKey(t)
	srv := newTestJWKSServer(t, key)
	v := newTestOIDCValidator(t, srv, now, nil)

	base := validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now)
	tests := []struct {
		name      string
		mutate    func(map[string]any)
		key       *rsa.PrivateKey
		kid       string
		headerAlg string
	}{
		{"wrong signature", nil, otherKey, "test-key-1", ""},
		{"unknown key id", nil, key, "no-such-kid", ""},
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://evil.example/" }, key, "test-key-1", ""},
		{"wrong audience", func(c map[string]any) { c["aud"] = "https://other.example/api" }, key, "test-key-1", ""},
		{"multi audience", func(c map[string]any) { c["aud"] = []string{testOIDCAudience, "https://other.example/api"} }, key, "test-key-1", ""},
		{"missing aud", func(c map[string]any) { delete(c, "aud") }, key, "test-key-1", ""},
		{"missing exp", func(c map[string]any) { delete(c, "exp") }, key, "test-key-1", ""},
		{"expired", func(c map[string]any) { c["exp"] = now.Add(-time.Hour).Unix() }, key, "test-key-1", ""},
		{"not yet valid", func(c map[string]any) { c["nbf"] = now.Add(time.Hour).Unix() }, key, "test-key-1", ""},
		{"iat in the future", func(c map[string]any) { c["iat"] = now.Add(time.Hour).Unix() }, key, "test-key-1", ""},
		{"missing tenant claim", func(c map[string]any) { delete(c, "tenant_id") }, key, "test-key-1", ""},
		{"missing company claim", func(c map[string]any) { delete(c, "company_id") }, key, "test-key-1", ""},
		{"empty subject", func(c map[string]any) { c["sub"] = "" }, key, "test-key-1", ""},
		{"alg none", nil, key, "test-key-1", "none"},
		{"alg hs256", nil, key, "test-key-1", "HS256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := make(map[string]any, len(base))
			for k, val := range base {
				claims[k] = val
			}
			if tt.mutate != nil {
				tt.mutate(claims)
			}
			token := signTestJWT(t, tt.key, tt.kid, claims, tt.headerAlg)
			if _, err := v.Validate(context.Background(), token); err == nil {
				t.Fatalf("Validate must reject %s", tt.name)
			}
		})
	}
}

// TestOIDCValidatorRefreshOnUnknownKid: the key-rotation path through the FULL
// validation chain — a token signed with a freshly rotated key triggers exactly
// ONE JWKS refresh, and a token signed with the rotated-out key fails closed
// after the cache is replaced. (The isolated lookupKey fetch-count proof lives
// in oidc_jwks_test.go.)
func TestOIDCValidatorRefreshOnUnknownKid(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	key1 := testRSAKey(t)
	key2 := testRSAKey(t)
	srv := newTestJWKSServer(t, key1)
	v := newTestOIDCValidator(t, srv, now, nil)

	// Prime the cache with key1.
	tok1 := signTestJWT(t, key1, "test-key-1", validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now), "")
	if _, err := v.Validate(context.Background(), tok1); err != nil {
		t.Fatalf("validate with key1: %v", err)
	}

	// Rotate: the provider now serves key2 only, under its NEW kid — the
	// unknown-kid → one-refresh rotation path.
	srv.rotate(map[string]*rsa.PrivateKey{"test-key-2": key2})
	tok2 := signTestJWT(t, key2, "test-key-2", validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now), "")
	if _, err := v.Validate(context.Background(), tok2); err != nil {
		t.Fatalf("validate after rotation (unknown kid → one refresh): %v", err)
	}

	// The rotated-out key1 must now fail closed (cache was replaced; one more
	// refresh cannot resurrect it).
	if _, err := v.Validate(context.Background(), tok1); err == nil {
		t.Fatal("token signed with the rotated-out key must be rejected")
	}
}

// TestOIDCValidatorConfigurableClaims: custom claim names are honored (the
// Auth0 namespaced-claim convention).
func TestOIDCValidatorConfigurableClaims(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	key := testRSAKey(t)
	srv := newTestJWKSServer(t, key)
	v := newTestOIDCValidator(t, srv, now, func(cfg *OIDCConfig) {
		cfg.TenantClaim = "https://drenyra.test/tenant"
		cfg.CompanyClaim = "https://drenyra.test/company"
	})
	claims := validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now)
	delete(claims, "tenant_id")
	delete(claims, "company_id")
	claims["https://drenyra.test/tenant"] = "tenant-1"
	claims["https://drenyra.test/company"] = "acme"

	token := signTestJWT(t, key, "test-key-1", claims, "")
	got, err := v.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate with custom claims: %v", err)
	}
	if got.TenantID != "tenant-1" || got.CompanyID != "acme" {
		t.Errorf("claims = %+v, want tenant-1/acme", got)
	}
}

// TestOIDCValidatorNeverRetainsRawToken: after validation the validator's
// observable state (config, JWKS cache) and the returned claims hold NO token
// material — the raw access token exists only as a local variable.
func TestOIDCValidatorNeverRetainsRawToken(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	key := testRSAKey(t)
	srv := newTestJWKSServer(t, key)
	v := newTestOIDCValidator(t, srv, now, nil)

	token := signTestJWT(t, key, "test-key-1", validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now), "")
	claims, err := v.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	dump := fmt.Sprintf("%+v", v.config)
	if strings.Contains(dump, token) || strings.Contains(dump, "subject-1") {
		t.Error("raw token or its claims leaked into the validator configuration")
	}
	// The JWKS cache is keyed by kid only.
	v.mu.Lock()
	for kid := range v.keys {
		if strings.Contains(kid, token) || strings.Contains(kid, ".") {
			t.Errorf("jwks cache key %q carries token material", kid)
		}
	}
	v.mu.Unlock()
	// The returned claims carry only the verified identity.
	claimsDump := fmt.Sprintf("%+v", claims)
	if strings.Contains(claimsDump, token) {
		t.Error("raw token leaked into the returned claims")
	}
}
