// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the SQLite SessionStore
// implementation (internal/store/session_store.go) over the v3 identity tables:
// token-hash → session (joined with membership + company), membership → roles +
// company active flag, and the explicit session seed fixture (design section 3,
// section 8). Raw bearer credentials never reach the store — only SHA-256 hashes.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
)

// sessionFixtureTokenHash is the SHA-256 of the fixture token "fixture-token";
// the raw token is never stored, only this hash.
const sessionFixtureTokenHash = "2f5ecbdd1701d69f3a44a2db6c1e5e2a9f78e56c9a87dab1f1e0d4b2c3a4f5a6"

// seedSessionFixture seeds the acme identity (testOrgID/acme) plus one expiring
// session row for membership-1.
func seedSessionFixture(t *testing.T, s *SQLiteStore) {
	t.Helper()
	if err := s.SeedIdentity(IdentitySeed{
		TenantID:     testOrgID,
		CompanyID:    "acme",
		CompanyRUC:   testRucA,
		CompanyName:  "ACME SAC",
		MembershipID: "membership-1",
		SubjectID:    "subject-1",
		Roles:        []auth.AccountingRole{auth.RoleController},
	}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	if err := s.SeedSession(SessionSeed{
		ID:                   "session-1",
		TokenHash:            sessionFixtureTokenHash,
		MembershipID:         "membership-1",
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      "2026-08-05T12:00:00Z",
		ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func TestLookupByTokenHashValid(t *testing.T) {
	s := newTestStore(t)
	seedSessionFixture(t, s)

	session, err := s.LookupByTokenHash(context.Background(), sessionFixtureTokenHash)
	if err != nil {
		t.Fatalf("lookup by token hash: %v", err)
	}
	if session.ID != "session-1" || session.MembershipID != "membership-1" {
		t.Errorf("session = %s/%s, want session-1/membership-1", session.ID, session.MembershipID)
	}
	if session.AuthenticationMethod != auth.AuthMethodSession || session.AssuranceLevel != auth.AssuranceStandard {
		t.Errorf("method/assurance = %s/%s, want session/standard",
			session.AuthenticationMethod, session.AssuranceLevel)
	}
	if session.AuthenticatedAt != "2026-08-05T12:00:00Z" {
		t.Errorf("authenticatedAt = %q", session.AuthenticatedAt)
	}
	if session.ExpiresAt == "" {
		t.Error("expiresAt must be set")
	}
	if session.RevokedAt != nil {
		t.Errorf("revokedAt = %v, want nil for an active session", *session.RevokedAt)
	}
}

func TestLookupByTokenHashUnknown(t *testing.T) {
	s := newTestStore(t)
	seedSessionFixture(t, s)

	if _, err := s.LookupByTokenHash(context.Background(), "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("unknown token hash must return an error (resolver maps it to PRINCIPAL_INVALID)")
	}
}

func TestLookupByTokenHashInactiveMembership(t *testing.T) {
	s := newTestStore(t)
	seedSessionFixture(t, s)
	if _, err := s.db.Exec(`UPDATE memberships SET status = 'inactive' WHERE id = 'membership-1'`); err != nil {
		t.Fatalf("deactivate membership: %v", err)
	}

	_, err := s.LookupByTokenHash(context.Background(), sessionFixtureTokenHash)
	if auth.Code(err) != auth.CodeMembershipInactive {
		t.Fatalf("code = %q, want MEMBERSHIP_INACTIVE", auth.Code(err))
	}
}

func TestLookupByTokenHashInactiveCompany(t *testing.T) {
	s := newTestStore(t)
	seedSessionFixture(t, s)
	if _, err := s.db.Exec(`UPDATE companies SET active = 0 WHERE id = 'acme'`); err != nil {
		t.Fatalf("deactivate company: %v", err)
	}

	_, err := s.LookupByTokenHash(context.Background(), sessionFixtureTokenHash)
	if auth.Code(err) != auth.CodeMembershipInactive {
		t.Fatalf("code = %q, want MEMBERSHIP_INACTIVE", auth.Code(err))
	}
}

func TestLookupByTokenHashRevoked(t *testing.T) {
	s := newTestStore(t)
	seedSessionFixture(t, s)
	revoked := "2026-08-06T00:00:00Z"
	if err := s.SeedSession(SessionSeed{
		ID:                   "session-revoked",
		TokenHash:            "aaaa000000000000000000000000000000000000000000000000000000000000",
		MembershipID:         "membership-1",
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceLow,
		AuthenticatedAt:      "2026-08-05T12:00:00Z",
		ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		RevokedAt:            &revoked,
	}); err != nil {
		t.Fatalf("seed revoked session: %v", err)
	}

	session, err := s.LookupByTokenHash(context.Background(), "aaaa000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("lookup revoked session: %v", err)
	}
	if session.RevokedAt == nil || *session.RevokedAt != revoked {
		t.Fatalf("revokedAt = %v, want %q (the resolver rejects revoked sessions)", session.RevokedAt, revoked)
	}
}

func TestLoadMembershipWithRolesAndCompanyActive(t *testing.T) {
	s := newTestStore(t)
	seedSessionFixture(t, s)

	m, err := s.LoadMembership(context.Background(), "membership-1")
	if err != nil {
		t.Fatalf("load membership: %v", err)
	}
	if m.ID != "membership-1" || m.SubjectID != "subject-1" || m.TenantID != testOrgID || m.CompanyID != "acme" {
		t.Errorf("membership = %+v, want membership-1/subject-1/%s/acme", m, testOrgID)
	}
	if m.Status != "active" || !m.CompanyActive {
		t.Errorf("status/companyActive = %q/%v, want active/true", m.Status, m.CompanyActive)
	}
	if len(m.Roles) != 1 || m.Roles[0] != auth.RoleController {
		t.Errorf("roles = %v, want [controller]", m.Roles)
	}
}

func TestLoadMembershipMissing(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.LoadMembership(context.Background(), "no-such-membership"); err == nil {
		t.Fatal("missing membership must return an error")
	}
}
