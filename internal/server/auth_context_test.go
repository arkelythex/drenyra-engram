// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test verifies the request-context
// principal helpers. Because there is NO public principal constructor, the
// fixtures are minted through the ONLY factory (auth.Resolver.Authenticate)
// with a minimal fake session store — the same path real middleware uses.
package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
)

// fixedSessionStore is a minimal SessionStore fake: it ignores the token hash
// and returns the configured session/membership (or the configured error).
type fixedSessionStore struct {
	session    auth.StoredSession
	membership auth.MembershipRecord
	lookupErr  error
}

func (f *fixedSessionStore) LookupByTokenHash(context.Context, string) (auth.StoredSession, error) {
	if f.lookupErr != nil {
		return auth.StoredSession{}, f.lookupErr
	}
	return f.session, nil
}

func (f *fixedSessionStore) LoadMembership(context.Context, string) (auth.MembershipRecord, error) {
	return f.membership, nil
}

// fixturePrincipal mints a verified principal through the resolver.
func fixturePrincipal(t *testing.T) auth.VerifiedApprovalPrincipal {
	t.Helper()
	store := &fixedSessionStore{
		session: auth.StoredSession{
			ID:                   "session-1",
			MembershipID:         "membership-1",
			AuthenticationMethod: auth.AuthMethodSession,
			AssuranceLevel:       auth.AssuranceStandard,
			AuthenticatedAt:      "2026-08-05T12:00:00Z",
			ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
		membership: auth.MembershipRecord{
			ID:            "membership-1",
			SubjectID:     "subject-1",
			TenantID:      "tenant-1",
			CompanyID:     "acme",
			Status:        "active",
			Roles:         []auth.AccountingRole{auth.RoleController},
			CompanyActive: true,
		},
	}
	resolver := &auth.Resolver{Sessions: store, Mode: auth.RuntimeProduction}
	p, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: "fixture-token",
	})
	if err != nil {
		t.Fatalf("fixture principal: %v", err)
	}
	return p
}

func TestPrincipalContextRoundTrip(t *testing.T) {
	p := fixturePrincipal(t)
	ctx := WithPrincipal(context.Background(), p)

	got, ok := PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("PrincipalFromContext did not find the principal")
	}
	if got.SubjectID() != p.SubjectID() || got.MembershipID() != p.MembershipID() {
		t.Errorf("round trip principal = %s/%s, want %s/%s",
			got.SubjectID(), got.MembershipID(), p.SubjectID(), p.MembershipID())
	}

	required, err := RequirePrincipal(ctx)
	if err != nil {
		t.Fatalf("RequirePrincipal: %v", err)
	}
	if required.TenantID() != "tenant-1" {
		t.Errorf("RequirePrincipal tenant = %q, want tenant-1", required.TenantID())
	}
}

func TestPrincipalContextEmptyWhenAbsent(t *testing.T) {
	_, ok := PrincipalFromContext(context.Background())
	if ok {
		t.Error("PrincipalFromContext on a bare context must report absent")
	}
}

func TestRequirePrincipalWithoutPrincipalReturnsAuthenticationRequired(t *testing.T) {
	_, err := RequirePrincipal(context.Background())
	if auth.Code(err) != auth.CodeAuthenticationRequired {
		t.Errorf("Code(err) = %q, want AUTHENTICATION_REQUIRED", auth.Code(err))
	}
	if errors.Is(err, auth.ErrPrincipalInvalid) {
		t.Error("missing principal must not be reported as PRINCIPAL_INVALID")
	}
}

func TestPrincipalContextDoesNotCollideWithOtherValues(t *testing.T) {
	ctx := context.WithValue(context.Background(), someOtherKey{}, "unrelated")
	p := fixturePrincipal(t)
	ctx = WithPrincipal(ctx, p)

	// The unrelated value survives; the principal resolves independently.
	if got := ctx.Value(someOtherKey{}); got != "unrelated" {
		t.Errorf("unrelated context value = %v, want unrelated", got)
	}
	got, ok := PrincipalFromContext(ctx)
	if !ok || got.SubjectID() != p.SubjectID() {
		t.Error("principal lost when other context values are present")
	}
}

type someOtherKey struct{}
