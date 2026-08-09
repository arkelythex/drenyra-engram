// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the HTTP OIDC integration
// of the first Production Identity slice: when the server has OIDC enabled,
// JWT-shaped bearer credentials are validated as stateless RS256 access tokens
// (exact issuer/audience, tenant/company claims cross-checked against the DB
// membership) while session credentials keep resolving through the session
// store. Invalid or mismatched tokens fail closed with PRINCIPAL_INVALID; a
// rejected token never authorizes.
package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// httpOIDCAudience is the fixed resource-server audience of the HTTP OIDC
// fixtures.
const httpOIDCAudience = "https://engram.drenyra.test/api"

// httpTestRSAKey generates a fresh RSA signing key for the HTTP OIDC fixtures.
func httpTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return key
}

// httpJWKSServer serves one RSA JWK (kid http-test-key) over TLS.
func httpJWKSServer(t *testing.T, key *rsa.PrivateKey) *httptest.Server {
	t.Helper()
	handler := func(w http.ResponseWriter, _ *http.Request) {
		jwk := map[string]string{
			"kty": "RSA",
			"kid": "http-test-key",
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01}),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{jwk}})
	}
	ts := httptest.NewTLSServer(http.HandlerFunc(handler))
	t.Cleanup(ts.Close)
	return ts
}

// httpSignJWT builds a compact RS256 JWT for the given claims.
func httpSignJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "http-test-key"})
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

// httpOIDCToken builds the access-token claim set of the seeded demo identity
// (maria.torres @ cmp_org/cmp_01) valid at `now`.
func httpOIDCToken(t *testing.T, key *rsa.PrivateKey, issuer string, now time.Time, mutate func(map[string]any)) string {
	t.Helper()
	claims := map[string]any{
		"iss":        issuer,
		"aud":        httpOIDCAudience,
		"sub":        "maria.torres",
		"exp":        now.Add(time.Hour).Unix(),
		"nbf":        now.Add(-time.Minute).Unix(),
		"iat":        now.Add(-time.Minute).Unix(),
		"tenant_id":  "cmp_org",
		"company_id": "cmp_01",
	}
	if mutate != nil {
		mutate(claims)
	}
	return httpSignJWT(t, key, claims)
}

// newHTTPOIDCServer builds a test API (with the demo identity + one pending
// approval) and an HTTP server with OIDC enabled against the fixture JWKS. It
// returns the server, the API, the signing key, the seeded session token, the
// pending memory, the OIDC ISSUER (the fixture JWKS URL — the exact issuer
// every fixture token must carry) and the fixed validation clock.
func newHTTPOIDCServer(t *testing.T) (*httptest.Server, *API, *rsa.PrivateKey, string, core.AccountingMemory, string, time.Time) {
	t.Helper()
	api := newTestAPI(t)
	token := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleController})
	mem := savePendingApproval(t, api, "oidc/fixture")

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	key := httpTestRSAKey(t)
	jwks := httpJWKSServer(t, key)

	h := NewHTTPServer(api, "")
	if _, err := h.EnableOIDC(auth.OIDCConfig{
		Issuer:     jwks.URL,
		Audience:   httpOIDCAudience,
		HTTPClient: jwks.Client(),
		Now:        func() time.Time { return now },
	}); err != nil {
		t.Fatalf("EnableOIDC: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	return ts, api, key, token, mem, jwks.URL, now
}

// TestHTTPOIDCAccessTokenAuthenticatesApproval: a valid Auth0-style access
// token whose tenant/company claims match the DB membership authorizes the
// approval route against the current envelope.
func TestHTTPOIDCAccessTokenAuthenticatesApproval(t *testing.T) {
	ts, _, key, _, mem, issuer, now := newHTTPOIDCServer(t)
	token := httpOIDCToken(t, key, issuer, now, nil)
	h1 := core.ComputeEnvelopeHash(mem)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		token, "oidc-approve-1", map[string]string{"expectedEnvelopeHash": h1, "reason": "oidc fixture"})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", status, raw)
	}
	if containsCode(raw, "AUTHENTICATION_REQUIRED") || containsCode(raw, "PRINCIPAL_INVALID") {
		t.Fatalf("authenticated approval must not fail authentication; body %s", raw)
	}
}

// TestHTTPOIDCSessionCredentialStillWorksWhenOIDCEnabled: enabling OIDC never
// breaks the session path — a non-JWT credential resolves through the session
// store exactly as before.
func TestHTTPOIDCSessionCredentialStillWorksWhenOIDCEnabled(t *testing.T) {
	ts, _, _, sessionToken, mem, _, _ := newHTTPOIDCServer(t)
	h1 := core.ComputeEnvelopeHash(mem)
	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		sessionToken, "oidc-session-1", map[string]string{"expectedEnvelopeHash": h1, "reason": "session fixture"})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (session path must keep working); body %s", status, raw)
	}
}

// TestHTTPOIDCRejectsInvalidToken: a JWT-shaped credential with a bad
// signature fails closed as PRINCIPAL_INVALID and never reaches the approval
// policy.
func TestHTTPOIDCRejectsInvalidToken(t *testing.T) {
	ts, _, _, _, mem, issuer, now := newHTTPOIDCServer(t)
	wrongKey := httpTestRSAKey(t)
	token := httpOIDCToken(t, wrongKey, issuer, now, nil)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		token, "oidc-bad-sig", map[string]string{"expectedEnvelopeHash": "x", "reason": "x"})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", status, raw)
	}
	if !containsCode(raw, "PRINCIPAL_INVALID") {
		t.Fatalf("body %q must carry PRINCIPAL_INVALID", raw)
	}
}

// TestHTTPOIDCRejectsClaimMembershipMismatch: a VALID token whose company
// claim does not match any DB membership of the subject fails closed — claims
// never mint membership.
func TestHTTPOIDCRejectsClaimMembershipMismatch(t *testing.T) {
	ts, _, key, _, mem, issuer, now := newHTTPOIDCServer(t)
	token := httpOIDCToken(t, key, issuer, now, func(c map[string]any) {
		c["company_id"] = "cmp_02" // the subject has no membership there
	})

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		token, "oidc-mismatch", map[string]string{"expectedEnvelopeHash": "x", "reason": "x"})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", status, raw)
	}
	if !containsCode(raw, "PRINCIPAL_INVALID") {
		t.Fatalf("body %q must carry PRINCIPAL_INVALID", raw)
	}
}

// containsCode is a minimal body-code matcher for the JSON error envelope
// {"error":{"code","message"}}.
func containsCode(raw, code string) bool {
	return strings.Contains(raw, `"code":"`+code+`"`)
}
