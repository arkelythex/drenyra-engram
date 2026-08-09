// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the OIDC resolution path
// of the ONLY principal factory: a signature-verified access token is
// cross-checked against the ACTIVE DB membership for the claimed
// (subject, tenant, company) tuple. It proves the stateless contract — no
// session lookup, no raw-token hash, no credential material retained — and the
// fail-closed cases: missing validator, claim/membership mismatch, no
// membership and inactive membership.
package auth

import (
	"context"
	"crypto/rsa"
	"strings"
	"testing"
	"time"
)

// oidcFixture builds a JWKS server, a resolver with a configured OIDC
// validator and a fake store whose memberships are keyed by
// subject|tenant|company.
func oidcFixture(t *testing.T) (*testJWKSServer, *Resolver, *fakeSessionStore, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	key := testRSAKey(t)
	srv := newTestJWKSServer(t, key)
	store := &fakeSessionStore{}
	resolver := newTestResolver(store, RuntimeProduction)
	resolver.OIDC = newTestOIDCValidator(t, srv, now, nil)
	return srv, resolver, store, now
}

// TestAuthenticateOIDCResolvesToPrincipal: a valid access token whose
// tenant/company claims cross-check to an active DB membership resolves to an
// oidc principal with standard assurance and NO session id.
func TestAuthenticateOIDCResolvesToPrincipal(t *testing.T) {
	srv, resolver, store, now := oidcFixture(t)
	store.membershipByScope = map[string]MembershipRecord{
		"subject-1|tenant-1|acme": activeMembership("membership-1"),
	}
	token := signTestJWT(t, mustFixtureKey(t, srv), "test-key-1",
		validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now), "")

	p, err := resolver.Authenticate(context.Background(), AuthenticationAssertion{
		Method:     AuthMethodOIDC,
		Credential: token,
	})
	if err != nil {
		t.Fatalf("Authenticate(oidc): %v", err)
	}
	if p.SubjectID() != "subject-1" || p.TenantID() != "tenant-1" || p.MembershipID() != "membership-1" {
		t.Errorf("identity = %s/%s/%s, want subject-1/tenant-1/membership-1", p.SubjectID(), p.TenantID(), p.MembershipID())
	}
	scopes := p.CompanyScopes()
	if len(scopes) != 1 || scopes[0] != "acme" {
		t.Errorf("companyScopes = %v, want [acme]", scopes)
	}
	if p.AuthenticationMethod() != AuthMethodOIDC || p.AssuranceLevel() != AssuranceStandard {
		t.Errorf("method/assurance = %s/%s, want oidc/standard", p.AuthenticationMethod(), p.AssuranceLevel())
	}
	if p.SessionID() != "" {
		t.Errorf("SessionID = %q, want empty (oidc is stateless)", p.SessionID())
	}
	// The DB-backed cross-check ran against the exact claimed scope.
	if len(store.scopeLookups) != 1 || store.scopeLookups[0] != "subject-1|tenant-1|acme" {
		t.Errorf("scope lookups = %v, want [subject-1|tenant-1|acme]", store.scopeLookups)
	}
}

// mustFixtureKey returns the FIRST private key the fixture JWKS server serves —
// test helpers sign with it; production never re-derives private keys.
func mustFixtureKey(t *testing.T, srv *testJWKSServer) *rsa.PrivateKey {
	t.Helper()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.keys) == 0 {
		t.Fatal("fixture jwks server serves no keys")
	}
	return srv.keys[0]
}

// TestAuthenticateOIDCClaimMembershipMismatchFailsClosed: a token whose
// claimed tenant/company tuple does not resolve to a DB membership for `sub`
// is PRINCIPAL_INVALID — ambiguity or drift never becomes a principal.
func TestAuthenticateOIDCClaimMembershipMismatchFailsClosed(t *testing.T) {
	srv, resolver, store, now := oidcFixture(t)
	// The subject exists, but under a DIFFERENT tenant/company scope.
	store.membershipByScope = map[string]MembershipRecord{
		"subject-1|tenant-other|other-co": activeMembership("membership-other"),
	}
	token := signTestJWT(t, mustFixtureKey(t, srv), "test-key-1",
		validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now), "")

	_, err := resolver.Authenticate(context.Background(), AuthenticationAssertion{
		Method: AuthMethodOIDC, Credential: token,
	})
	if Code(err) != CodePrincipalInvalid {
		t.Errorf("Code(err) = %q, want PRINCIPAL_INVALID (claim/membership mismatch)", Code(err))
	}
}

// TestAuthenticateOIDCNoMembershipFailsClosed: an unknown subject fails closed
// as PRINCIPAL_INVALID.
func TestAuthenticateOIDCNoMembershipFailsClosed(t *testing.T) {
	srv, resolver, _, now := oidcFixture(t)
	token := signTestJWT(t, mustFixtureKey(t, srv), "test-key-1",
		validOIDCClaims(srv.URL(), "unknown-subject", "tenant-1", "acme", now), "")

	_, err := resolver.Authenticate(context.Background(), AuthenticationAssertion{
		Method: AuthMethodOIDC, Credential: token,
	})
	if Code(err) != CodePrincipalInvalid {
		t.Errorf("Code(err) = %q, want PRINCIPAL_INVALID (no membership)", Code(err))
	}
}

// TestAuthenticateOIDCInactiveMembership: an existing but inactive membership
// (status or company) maps to MEMBERSHIP_INACTIVE.
func TestAuthenticateOIDCInactiveMembership(t *testing.T) {
	srv, resolver, store, now := oidcFixture(t)
	tests := []struct {
		name   string
		mutate func(*MembershipRecord)
	}{
		{"membership status inactive", func(m *MembershipRecord) { m.Status = "inactive" }},
		{"company inactive", func(m *MembershipRecord) { m.CompanyActive = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := activeMembership("membership-1")
			tt.mutate(&m)
			store.membershipByScope = map[string]MembershipRecord{"subject-1|tenant-1|acme": m}
			token := signTestJWT(t, mustFixtureKey(t, srv), "test-key-1",
				validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now), "")
			_, err := resolver.Authenticate(context.Background(), AuthenticationAssertion{
				Method: AuthMethodOIDC, Credential: token,
			})
			if Code(err) != CodeMembershipInactive {
				t.Errorf("Code(err) = %q, want MEMBERSHIP_INACTIVE", Code(err))
			}
		})
	}
}

// TestAuthenticateOIDCRejectedTokenFailsClosed: a signature/claim-rejected
// access token maps to PRINCIPAL_INVALID and never touches the membership
// store.
func TestAuthenticateOIDCRejectedTokenFailsClosed(t *testing.T) {
	srv, resolver, store, now := oidcFixture(t)
	store.membershipByScope = map[string]MembershipRecord{
		"subject-1|tenant-1|acme": activeMembership("membership-1"),
	}
	wrongKey := testRSAKey(t)
	token := signTestJWT(t, wrongKey, "test-key-1",
		validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now), "")

	_, err := resolver.Authenticate(context.Background(), AuthenticationAssertion{
		Method: AuthMethodOIDC, Credential: token,
	})
	if Code(err) != CodePrincipalInvalid {
		t.Errorf("Code(err) = %q, want PRINCIPAL_INVALID (rejected token)", Code(err))
	}
	if len(store.scopeLookups) != 0 {
		t.Error("a rejected token must never reach the membership store")
	}
}

// TestAuthenticateOIDCSnapshotRecordsValidationTime: the principal snapshot's
// authenticatedAt is the SERVER-OBSERVED validation time (the validator's fixed
// clock — when the resource server authenticated the caller), never the token
// `iat`. Audit provenance must record when the server verified the credential,
// not when the token was issued; token-expiry validation itself is unchanged.
func TestAuthenticateOIDCSnapshotRecordsValidationTime(t *testing.T) {
	srv, resolver, store, now := oidcFixture(t)
	store.membershipByScope = map[string]MembershipRecord{
		"subject-1|tenant-1|acme": activeMembership("membership-1"),
	}
	// validOIDCClaims sets iat = now-1m while the validator's clock is fixed at
	// `now`: the snapshot must carry `now`, not the older iat.
	token := signTestJWT(t, mustFixtureKey(t, srv), "test-key-1",
		validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now), "")

	p, err := resolver.Authenticate(context.Background(), AuthenticationAssertion{
		Method: AuthMethodOIDC, Credential: token,
	})
	if err != nil {
		t.Fatalf("Authenticate(oidc): %v", err)
	}
	want := now.UTC().Format(time.RFC3339)
	if got := p.AuthenticatedAt(); got != want {
		t.Errorf("AuthenticatedAt = %q, want %q (server-observed validation time)", got, want)
	}
	if got := p.AuthenticatedAt(); got == now.Add(-time.Minute).UTC().Format(time.RFC3339) {
		t.Errorf("AuthenticatedAt = %q, must NOT be the token iat", got)
	}
	if snap := p.PrincipalSnapshot(); snap.AuthenticatedAt != want {
		t.Errorf("snapshot.AuthenticatedAt = %q, want %q", snap.AuthenticatedAt, want)
	}
}

// TestOIDCValidatorValidatedAtIsServerClock: the validator reports BOTH the
// token's own issue time (IssuedAt) and the server-observed validation clock
// (ValidatedAt) — the resolver records the latter.
func TestOIDCValidatorValidatedAtIsServerClock(t *testing.T) {
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
	if !claims.ValidatedAt.Equal(now.UTC()) {
		t.Errorf("ValidatedAt = %v, want the server clock %v", claims.ValidatedAt, now.UTC())
	}
	if !claims.IssuedAt.Equal(now.Add(-time.Minute).UTC()) {
		t.Errorf("IssuedAt = %v, want the token iat %v", claims.IssuedAt, now.Add(-time.Minute).UTC())
	}
}

// TestAuthenticateOIDCIsStateless: the OIDC path never performs a session
// lookup, never hashes the raw credential and never stores anything — there is
// no session row and no token hash anywhere in the flow.
func TestAuthenticateOIDCIsStateless(t *testing.T) {
	srv, resolver, store, now := oidcFixture(t)
	store.membershipByScope = map[string]MembershipRecord{
		"subject-1|tenant-1|acme": activeMembership("membership-1"),
	}
	token := signTestJWT(t, mustFixtureKey(t, srv), "test-key-1",
		validOIDCClaims(srv.URL(), "subject-1", "tenant-1", "acme", now), "")

	p, err := resolver.Authenticate(context.Background(), AuthenticationAssertion{
		Method: AuthMethodOIDC, Credential: token,
	})
	if err != nil {
		t.Fatalf("Authenticate(oidc): %v", err)
	}
	if len(store.lookedUpHashes) != 0 {
		t.Errorf("oidc must not hash the credential into a session lookup; saw %d hash lookups", len(store.lookedUpHashes))
	}
	snap := p.PrincipalSnapshot()
	all := strings.Join([]string{p.SubjectID(), p.TenantID(), p.MembershipID(), snap.SubjectID, snap.MembershipID}, "|")
	if strings.Contains(all, token) || strings.Contains(all, "secret") {
		t.Error("credential material leaked into the principal or its snapshot")
	}
}
