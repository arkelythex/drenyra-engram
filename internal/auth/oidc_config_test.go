// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the configuration and
// claim layer of the stateless OIDC access-token slice (oidc_config.go): the
// fail-closed NormalizeOIDCConfig gate with its defaults and bounds, the
// mandatory HTTPS URL gate, and the pure JWT-claim parsing primitives
// (splitJWT, checkAudience, jsonStringClaim, jsonTimeClaim) with focused table
// tests. No network or crypto state is exercised here — the fixture JWKS
// helpers live in oidc_jwks_test.go and the end-to-end validator matrix in
// oidc_test.go.
package auth

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testOIDCAudience is the fixed resource-server audience of the OIDC fixtures.
const testOIDCAudience = "https://engram.drenyra.test/api"

// TestNormalizeOIDCConfigFailsClosed: incomplete or invalid OIDC configuration
// is an error — the server must never start with a partial trust contract.
func TestNormalizeOIDCConfigFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		cfg  OIDCConfig
	}{
		{"empty config", OIDCConfig{}},
		{"missing audience", OIDCConfig{Issuer: "https://issuer.test/"}},
		{"http issuer", OIDCConfig{Issuer: "http://issuer.test/", Audience: testOIDCAudience}},
		{"http jwks url", OIDCConfig{Issuer: "https://issuer.test/", Audience: testOIDCAudience, JWKSURL: "http://issuer.test/keys"}},
		{"negative skew", OIDCConfig{Issuer: "https://issuer.test/", Audience: testOIDCAudience, ClockSkew: -time.Second}},
		{"oversized skew", OIDCConfig{Issuer: "https://issuer.test/", Audience: testOIDCAudience, ClockSkew: MaxOIDCClockSkew + time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NormalizeOIDCConfig(tt.cfg); err == nil {
				t.Fatalf("NormalizeOIDCConfig(%+v) must fail closed", tt.cfg)
			}
		})
	}
}

// TestNormalizeOIDCConfigAppliesDefaults: a minimal valid config gets the
// issuer-derived JWKS URL, the default claim names and the bounded default
// skew.
func TestNormalizeOIDCConfigAppliesDefaults(t *testing.T) {
	cfg, err := NormalizeOIDCConfig(OIDCConfig{
		Issuer:   "https://issuer.test/",
		Audience: testOIDCAudience,
	})
	if err != nil {
		t.Fatalf("NormalizeOIDCConfig: %v", err)
	}
	if cfg.JWKSURL != "https://issuer.test/.well-known/jwks.json" {
		t.Errorf("JWKSURL = %q, want issuer-derived default", cfg.JWKSURL)
	}
	if cfg.TenantClaim != DefaultOIDCTenantClaim || cfg.CompanyClaim != DefaultOIDCCompanyClaim {
		t.Errorf("claim names = %q/%q, want %q/%q", cfg.TenantClaim, cfg.CompanyClaim, DefaultOIDCTenantClaim, DefaultOIDCCompanyClaim)
	}
	if cfg.ClockSkew != DefaultOIDCClockSkew {
		t.Errorf("ClockSkew = %v, want %v", cfg.ClockSkew, DefaultOIDCClockSkew)
	}
}

// TestNormalizeOIDCConfigTrimsWhitespace: normalization trims surrounding
// whitespace from every user-supplied field, so a stray space never becomes a
// trust mismatch.
func TestNormalizeOIDCConfigTrimsWhitespace(t *testing.T) {
	cfg, err := NormalizeOIDCConfig(OIDCConfig{
		Issuer:       "  https://issuer.test/  ",
		Audience:     "  " + testOIDCAudience + "  ",
		JWKSURL:      "  https://issuer.test/keys  ",
		TenantClaim:  "  ns/tenant  ",
		CompanyClaim: "  ns/company  ",
	})
	if err != nil {
		t.Fatalf("NormalizeOIDCConfig: %v", err)
	}
	if cfg.Issuer != "https://issuer.test/" || cfg.Audience != testOIDCAudience {
		t.Errorf("issuer/audience = %q/%q, want trimmed values", cfg.Issuer, cfg.Audience)
	}
	if cfg.JWKSURL != "https://issuer.test/keys" {
		t.Errorf("JWKSURL = %q, want trimmed value", cfg.JWKSURL)
	}
	if cfg.TenantClaim != "ns/tenant" || cfg.CompanyClaim != "ns/company" {
		t.Errorf("claim names = %q/%q, want trimmed values", cfg.TenantClaim, cfg.CompanyClaim)
	}
}

// TestRequireHTTPSURL: the issuer and the JWKS endpoint MUST be absolute https
// URLs; anything else (http, scheme-less, host-less, unparseable) is rejected.
func TestRequireHTTPSURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"http", "http://issuer.test/"},
		{"no scheme", "issuer.test/keys"},
		{"no host", "https:///keys"},
		{"unparseable", "://::not a url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := requireHTTPSURL("oidc test url", tt.raw); err == nil {
				t.Errorf("requireHTTPSURL(%q) must reject a non-https URL", tt.raw)
			}
		})
	}
	// The one accepted shape: absolute https with a host.
	if err := requireHTTPSURL("oidc test url", "https://issuer.test/.well-known/jwks.json"); err != nil {
		t.Errorf("requireHTTPSURL(valid https) = %v, want nil", err)
	}
}

// rawClaims marshals Go values into a claim map for the pure parsing
// primitives.
func rawClaims(t *testing.T, vals map[string]any) map[string]json.RawMessage {
	t.Helper()
	out := make(map[string]json.RawMessage, len(vals))
	for k, val := range vals {
		b, err := json.Marshal(val)
		if err != nil {
			t.Fatalf("marshal claim %q: %v", k, err)
		}
		out[k] = b
	}
	return out
}

// TestSplitJWTParsesCompactToken: only a three-part compact JWT with non-empty
// segments is accepted, and the segments are returned verbatim (they form the
// signing input — never re-encoded).
func TestSplitJWTParsesCompactToken(t *testing.T) {
	header, payload, sig, err := splitJWT("aaa.bbb.ccc")
	if err != nil {
		t.Fatalf("splitJWT(valid): %v", err)
	}
	if header != "aaa" || payload != "bbb" || sig != "ccc" {
		t.Errorf("segments = %q/%q/%q, want aaa/bbb/ccc verbatim", header, payload, sig)
	}
	tests := []struct {
		name  string
		token string
	}{
		{"no dots", "not-a-jwt"},
		{"two parts", "aa.bb"},
		{"four parts", "aa.bb.cc.dd"},
		{"empty header", ".bb.cc"},
		{"empty payload", "aa..cc"},
		{"empty signature", "aa.bb."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, err := splitJWT(tt.token); err == nil {
				t.Errorf("splitJWT(%q) must reject a non-three-part token", tt.token)
			}
		})
	}
}

// TestCheckAudience: `aud` must be EXACTLY the configured audience — a string
// must match; an array must contain exactly that one audience. Missing,
// mismatched, multi-audience and non-string/array claims all fail closed.
func TestCheckAudience(t *testing.T) {
	tests := []struct {
		name     string
		aud      any
		expected string
		wantErr  bool
	}{
		{"exact string", testOIDCAudience, testOIDCAudience, false},
		{"mismatched string", "https://other.example/api", testOIDCAudience, true},
		{"single-element array", []string{testOIDCAudience}, testOIDCAudience, false},
		{"multi-audience array", []string{testOIDCAudience, "https://other.example/api"}, testOIDCAudience, true},
		{"wrong single-element array", []string{"https://other.example/api"}, testOIDCAudience, true},
		{"number claim", 42, testOIDCAudience, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := rawClaims(t, map[string]any{"aud": tt.aud})
			err := checkAudience(claims, tt.expected)
			if tt.wantErr && err == nil {
				t.Fatalf("checkAudience(%v) must fail closed", tt.aud)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("checkAudience(%v) = %v, want nil", tt.aud, err)
			}
		})
	}
	// Missing aud claim always fails closed.
	if err := checkAudience(map[string]json.RawMessage{}, testOIDCAudience); err == nil {
		t.Error("checkAudience without an aud claim must fail closed")
	}
}

// TestJSONStringClaim: an absent claim is "" (callers decide requiredness); a
// present non-string claim is an error.
func TestJSONStringClaim(t *testing.T) {
	claims := rawClaims(t, map[string]any{
		"str":    "value",
		"number": 7,
		"object": map[string]any{"a": 1},
	})
	if got, err := jsonStringClaim(claims, "str"); err != nil || got != "value" {
		t.Errorf("jsonStringClaim(str) = %q, %v; want value, nil", got, err)
	}
	if got, err := jsonStringClaim(claims, "absent"); err != nil || got != "" {
		t.Errorf("jsonStringClaim(absent) = %q, %v; want \"\", nil", got, err)
	}
	for _, key := range []string{"number", "object"} {
		if _, err := jsonStringClaim(claims, key); err == nil {
			t.Errorf("jsonStringClaim(%q) must reject a non-string claim", key)
		}
	}
}

// TestJSONTimeClaim: NumericDate claims (integer or fractional seconds) parse
// to UTC; an absent claim is nil; a present non-number claim is an error.
func TestJSONTimeClaim(t *testing.T) {
	claims := rawClaims(t, map[string]any{
		"int":   1700000000,
		"frac":  1700000000.5,
		"str":   "2026-08-08",
		"array": []int{1, 2},
	})
	for key, want := range map[string]time.Time{
		"int":  time.Unix(1700000000, 0).UTC(),
		"frac": time.Unix(1700000000, 0).UTC(),
	} {
		got, err := jsonTimeClaim(claims, key)
		if err != nil {
			t.Errorf("jsonTimeClaim(%s): %v", key, err)
			continue
		}
		if got == nil || !got.Equal(want) {
			t.Errorf("jsonTimeClaim(%s) = %v, want %v", key, got, want)
		}
	}
	if got, err := jsonTimeClaim(claims, "absent"); err != nil || got != nil {
		t.Errorf("jsonTimeClaim(absent) = %v, %v; want nil, nil", got, err)
	}
	for _, key := range []string{"str", "array"} {
		if _, err := jsonTimeClaim(claims, key); err == nil {
			t.Errorf("jsonTimeClaim(%q) must reject a non-number claim", key)
		}
	}
}

// TestRequireHTTPSURLParseError: an unparseable URL surfaces a parse error
// mentioning the offending value (fail closed with a useful message).
func TestRequireHTTPSURLParseError(t *testing.T) {
	err := requireHTTPSURL("oidc test url", "http://[::1")
	if err == nil {
		t.Fatal("requireHTTPSURL must reject an unparseable URL")
	}
	if !strings.Contains(err.Error(), "not a valid URL") {
		t.Errorf("error = %q, want a parse-error message", err)
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Errorf("error = %v, want an underlying url.Error", err)
	}
}
