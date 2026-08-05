// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the ONLY principal factory
// (Resolver.Authenticate) against a hash-keyed fake SessionStore: token
// resolution, wrong/expired/revoked credentials, inactive memberships,
// local_dev gating and the oidc Step 1 rejection. Raw credentials are never
// stored — only SHA-256 hashes cross into the store.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeSessionStore is a hash-keyed in-memory SessionStore. It records every
// token hash it receives so tests can prove raw credentials never cross the
// boundary.
type fakeSessionStore struct {
	sessions      map[string]StoredSession
	memberships   map[string]MembershipRecord
	lookupErr     error
	membershipErr error
	lookedUpHashes []string
}

func (f *fakeSessionStore) LookupByTokenHash(_ context.Context, tokenHash string) (StoredSession, error) {
	f.lookedUpHashes = append(f.lookedUpHashes, tokenHash)
	if f.lookupErr != nil {
		return StoredSession{}, f.lookupErr
	}
	s, ok := f.sessions[tokenHash]
	if !ok {
		return StoredSession{}, errors.New("session not found")
	}
	return s, nil
}

func (f *fakeSessionStore) LoadMembership(_ context.Context, membershipID string) (MembershipRecord, error) {
	if f.membershipErr != nil {
		return MembershipRecord{}, f.membershipErr
	}
	m, ok := f.memberships[membershipID]
	if !ok {
		return MembershipRecord{}, errors.New("membership not found")
	}
	return m, nil
}

func hashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func futureExpiry() string {
	return time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
}

func activeMembership(id string) MembershipRecord {
	return MembershipRecord{
		ID:            id,
		SubjectID:     "subject-1",
		TenantID:      "tenant-1",
		CompanyID:     "acme",
		Status:        "active",
		Roles:         []AccountingRole{RoleSeniorAccountant},
		CompanyActive: true,
	}
}

func activeSession(id string) StoredSession {
	return StoredSession{
		ID:                   id,
		MembershipID:         "membership-1",
		AuthenticationMethod: AuthMethodSession,
		AssuranceLevel:       AssuranceStandard,
		AuthenticatedAt:      "2026-08-05T12:00:00Z",
		ExpiresAt:            futureExpiry(),
	}
}

func newTestResolver(store SessionStore, mode RuntimeMode) *Resolver {
	return &Resolver{Sessions: store, Mode: mode}
}

func TestAuthenticateSessionTokenResolvesToPrincipal(t *testing.T) {
	store := &fakeSessionStore{
		sessions:    map[string]StoredSession{hashOf("secret-token"): activeSession("session-1")},
		memberships: map[string]MembershipRecord{"membership-1": activeMembership("membership-1")},
	}
	p, err := newTestResolver(store, RuntimeProduction).Authenticate(context.Background(), AuthenticationAssertion{
		Method:     AuthMethodSession,
		Credential: "secret-token",
	})
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.SubjectID() != "subject-1" || p.TenantID() != "tenant-1" {
		t.Errorf("principal identity = %s/%s, want subject-1/tenant-1", p.SubjectID(), p.TenantID())
	}
	if p.MembershipID() != "membership-1" || p.SessionID() != "session-1" {
		t.Errorf("principal membership/session = %s/%s, want membership-1/session-1", p.MembershipID(), p.SessionID())
	}
	if p.AuthenticationMethod() != AuthMethodSession || p.AssuranceLevel() != AssuranceStandard {
		t.Errorf("method/assurance = %s/%s, want session/standard", p.AuthenticationMethod(), p.AssuranceLevel())
	}
	scopes := p.CompanyScopes()
	if len(scopes) != 1 || scopes[0] != "acme" {
		t.Errorf("companyScopes = %v, want [acme]", scopes)
	}
}

func TestAuthenticateServiceAssertionResolvesByTokenHash(t *testing.T) {
	store := &fakeSessionStore{
		sessions: map[string]StoredSession{hashOf("service-credential"): {
			ID:                   "sess-sa",
			MembershipID:         "membership-1",
			AuthenticationMethod: AuthMethodServiceAssertion,
			AssuranceLevel:       AssuranceStrong,
			AuthenticatedAt:      "2026-08-05T12:00:00Z",
			ExpiresAt:            futureExpiry(),
		}},
		memberships: map[string]MembershipRecord{"membership-1": activeMembership("membership-1")},
	}
	p, err := newTestResolver(store, RuntimeProduction).Authenticate(context.Background(), AuthenticationAssertion{
		Method:     AuthMethodServiceAssertion,
		Credential: "service-credential",
	})
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.AuthenticationMethod() != AuthMethodServiceAssertion || p.AssuranceLevel() != AssuranceStrong {
		t.Errorf("method/assurance = %s/%s, want service_assertion/strong", p.AuthenticationMethod(), p.AssuranceLevel())
	}
}

func TestAuthenticateNeverStoresOrReturnsRawCredential(t *testing.T) {
	store := &fakeSessionStore{
		sessions:    map[string]StoredSession{hashOf("secret-token"): activeSession("session-1")},
		memberships: map[string]MembershipRecord{"membership-1": activeMembership("membership-1")},
	}
	p, err := newTestResolver(store, RuntimeProduction).Authenticate(context.Background(), AuthenticationAssertion{
		Method:     AuthMethodSession,
		Credential: "secret-token",
	})
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	// The store only ever saw the SHA-256 hash.
	if len(store.lookedUpHashes) != 1 {
		t.Fatalf("store saw %d lookups, want 1", len(store.lookedUpHashes))
	}
	if store.lookedUpHashes[0] == "secret-token" {
		t.Fatal("raw credential leaked into the store")
	}
	if store.lookedUpHashes[0] != hashOf("secret-token") {
		t.Errorf("store received %q, want SHA-256 of the credential", store.lookedUpHashes[0])
	}
	// The principal and its snapshot carry no credential material.
	snap := p.PrincipalSnapshot()
	all := strings.Join([]string{
		p.SubjectID(), p.TenantID(), p.MembershipID(), p.SessionID(), snap.SubjectID, snap.MembershipID,
	}, "|")
	if strings.Contains(all, "secret-token") || strings.Contains(all, "secret") {
		t.Error("credential material leaked into the principal or its snapshot")
	}
}

func TestAuthenticateWrongTokenReturnsPrincipalInvalid(t *testing.T) {
	store := &fakeSessionStore{
		sessions:    map[string]StoredSession{hashOf("right-token"): activeSession("session-1")},
		memberships: map[string]MembershipRecord{"membership-1": activeMembership("membership-1")},
	}
	_, err := newTestResolver(store, RuntimeProduction).Authenticate(context.Background(), AuthenticationAssertion{
		Method:     AuthMethodSession,
		Credential: "wrong-token",
	})
	if Code(err) != CodePrincipalInvalid {
		t.Errorf("Code(err) = %q, want PRINCIPAL_INVALID", Code(err))
	}
}

func TestAuthenticateExpiredSessionReturnsPrincipalInvalid(t *testing.T) {
	expired := activeSession("session-1")
	expired.ExpiresAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	store := &fakeSessionStore{
		sessions:    map[string]StoredSession{hashOf("token"): expired},
		memberships: map[string]MembershipRecord{"membership-1": activeMembership("membership-1")},
	}
	_, err := newTestResolver(store, RuntimeProduction).Authenticate(context.Background(), AuthenticationAssertion{
		Method: AuthMethodSession, Credential: "token",
	})
	if Code(err) != CodePrincipalInvalid {
		t.Errorf("Code(err) = %q, want PRINCIPAL_INVALID (expired)", Code(err))
	}
}

func TestAuthenticateRevokedSessionReturnsPrincipalInvalid(t *testing.T) {
	revoked := activeSession("session-1")
	when := time.Now().UTC().Format(time.RFC3339)
	revoked.RevokedAt = &when
	store := &fakeSessionStore{
		sessions:    map[string]StoredSession{hashOf("token"): revoked},
		memberships: map[string]MembershipRecord{"membership-1": activeMembership("membership-1")},
	}
	_, err := newTestResolver(store, RuntimeProduction).Authenticate(context.Background(), AuthenticationAssertion{
		Method: AuthMethodSession, Credential: "token",
	})
	if Code(err) != CodePrincipalInvalid {
		t.Errorf("Code(err) = %q, want PRINCIPAL_INVALID (revoked)", Code(err))
	}
}

func TestAuthenticateInactiveMembershipReturnsMembershipInactive(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*MembershipRecord)
	}{
		{"membership status inactive", func(m *MembershipRecord) { m.Status = "inactive" }},
		{"company inactive", func(m *MembershipRecord) { m.CompanyActive = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := activeMembership("membership-1")
			tt.mutate(&m)
			store := &fakeSessionStore{
				sessions:    map[string]StoredSession{hashOf("token"): activeSession("session-1")},
				memberships: map[string]MembershipRecord{"membership-1": m},
			}
			_, err := newTestResolver(store, RuntimeProduction).Authenticate(context.Background(), AuthenticationAssertion{
				Method: AuthMethodSession, Credential: "token",
			})
			if Code(err) != CodeMembershipInactive {
				t.Errorf("Code(err) = %q, want MEMBERSHIP_INACTIVE", Code(err))
			}
		})
	}
}

func TestAuthenticateOIDCReturnsAuthenticationRequired(t *testing.T) {
	_, err := newTestResolver(&fakeSessionStore{}, RuntimeProduction).Authenticate(context.Background(), AuthenticationAssertion{
		Method:     AuthMethodOIDC,
		Credential: "id-token",
	})
	if Code(err) != CodeAuthenticationRequired {
		t.Errorf("Code(err) = %q, want AUTHENTICATION_REQUIRED (oidc not resolvable in Step 1)", Code(err))
	}
}

func TestAuthenticateNoSilentFallback(t *testing.T) {
	store := &fakeSessionStore{
		sessions:    map[string]StoredSession{hashOf("token"): activeSession("session-1")},
		memberships: map[string]MembershipRecord{"membership-1": activeMembership("membership-1")},
	}
	resolver := newTestResolver(store, RuntimeProduction)

	// An absent method must NOT fall back to the session store.
	_, err := resolver.Authenticate(context.Background(), AuthenticationAssertion{})
	if Code(err) != CodeAuthenticationRequired {
		t.Errorf("empty assertion: Code(err) = %q, want AUTHENTICATION_REQUIRED", Code(err))
	}
	if len(store.lookedUpHashes) != 0 {
		t.Error("empty assertion must not touch the session store")
	}

	// A session method without a credential must NOT fall back either.
	_, err = resolver.Authenticate(context.Background(), AuthenticationAssertion{Method: AuthMethodSession})
	if Code(err) != CodeAuthenticationRequired {
		t.Errorf("credential-less session: Code(err) = %q, want AUTHENTICATION_REQUIRED", Code(err))
	}
	if len(store.lookedUpHashes) != 0 {
		t.Error("credential-less assertion must not touch the session store")
	}
}

func TestAuthenticateLocalDevDeniedInProduction(t *testing.T) {
	_, err := newTestResolver(&fakeSessionStore{}, RuntimeProduction).Authenticate(context.Background(), AuthenticationAssertion{
		Method:          AuthMethodLocalDev,
		LocalDevSubjectID: "local-dev-1",
	})
	if Code(err) != CodeAuthenticationRequired {
		t.Errorf("Code(err) = %q, want AUTHENTICATION_REQUIRED (local_dev in production)", Code(err))
	}
}

func TestAuthenticateLocalDevAllowedInLocalDevMode(t *testing.T) {
	p, err := newTestResolver(&fakeSessionStore{}, RuntimeLocalDev).Authenticate(context.Background(), AuthenticationAssertion{
		Method:            AuthMethodLocalDev,
		LocalDevSubjectID: "local-dev-1",
	})
	if err != nil {
		t.Fatalf("Authenticate(local_dev): %v", err)
	}
	if p.SubjectID() != "local-dev-1" {
		t.Errorf("SubjectID = %q, want local-dev-1", p.SubjectID())
	}
	// Explicit local_dev marker and standard assurance.
	if p.AuthenticationMethod() != AuthMethodLocalDev {
		t.Errorf("method = %q, want local_dev marker", p.AuthenticationMethod())
	}
	if p.AssuranceLevel() != AssuranceStandard {
		t.Errorf("assurance = %q, want standard", p.AssuranceLevel())
	}
}

func TestAuthenticateLocalDevRequiresSubjectInLocalDevMode(t *testing.T) {
	_, err := newTestResolver(&fakeSessionStore{}, RuntimeLocalDev).Authenticate(context.Background(), AuthenticationAssertion{
		Method: AuthMethodLocalDev,
	})
	if Code(err) != CodeAuthenticationRequired {
		t.Errorf("Code(err) = %q, want AUTHENTICATION_REQUIRED (missing local_dev subject)", Code(err))
	}
}
