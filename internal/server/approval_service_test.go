// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the authenticated
// approval service contract (v0.4.0 Step 1, ADR-003): command syntax
// validation, the principal → provenance mapping (Source records the verified
// claim; it does NOT authorize), the compile-level absence of principal fields
// on the command, and the delegation of the WHOLE state change to one atomic
// store operation.

package server

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// approvalFixedSessionStore ignores the token hash and returns the configured
// session/membership — enough to mint principals through the real resolver.
type approvalFixedSessionStore struct {
	session    auth.StoredSession
	membership auth.MembershipRecord
}

func (f *approvalFixedSessionStore) LookupByTokenHash(context.Context, string) (auth.StoredSession, error) {
	return f.session, nil
}

func (f *approvalFixedSessionStore) LoadMembership(context.Context, string) (auth.MembershipRecord, error) {
	return f.membership, nil
}

// LookupMembershipByScope satisfies the auth.SessionStore contract with the
// SAME fail-closed tuple semantics as the real store
// (internal/store/session_store.go): the configured membership is returned ONLY
// when the exact (subject, tenant, company) tuple matches — any other tuple is a
// plain not-found error the resolver maps to PRINCIPAL_INVALID. The fake must
// not mint memberships for arbitrary claimed tuples.
func (f *approvalFixedSessionStore) LookupMembershipByScope(_ context.Context, subjectID, tenantID, companyID string) (auth.MembershipRecord, error) {
	if subjectID != f.membership.SubjectID || tenantID != f.membership.TenantID || companyID != f.membership.CompanyID {
		return auth.MembershipRecord{}, errors.New("membership not found")
	}
	return f.membership, nil
}

func approvalFixtureStore() auth.SessionStore {
	return &approvalFixedSessionStore{
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
}

func approvalMustPrincipal(t *testing.T) auth.VerifiedApprovalPrincipal {
	t.Helper()
	resolver := &auth.Resolver{Sessions: approvalFixtureStore(), Mode: auth.RuntimeProduction}
	p, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: "fixture-token",
	})
	if err != nil {
		t.Fatalf("fixture principal: %v", err)
	}
	return p
}

// recordingApprovalStore records the delegated call and returns the configured
// result — the service must hand over the complete state change.
type recordingApprovalStore struct {
	called    bool
	gotCmd    core.ApproveMemoryCommand
	gotP      auth.VerifiedApprovalPrincipal
	gotPolicy authz.ApprovalAuthorizationPolicy
	result    core.ApprovalResult
	err       error
}

func (r *recordingApprovalStore) ApproveMemory(ctx context.Context, cmd core.ApproveMemoryCommand, principal auth.VerifiedApprovalPrincipal, policy authz.ApprovalAuthorizationPolicy) (core.ApprovalResult, error) {
	r.called = true
	r.gotCmd = cmd
	r.gotP = principal
	r.gotPolicy = policy
	return r.result, r.err
}

func TestApproveMemoryServiceValidation(t *testing.T) {
	principal := approvalMustPrincipal(t)
	policy := authz.NewApprovalPolicy()

	cases := []struct {
		name                       string
		memoryID, expected, reason string
		requestID                  string
		principal                  auth.VerifiedApprovalPrincipal
		wantCode                   string
	}{
		{"empty reason", "mem-1", "abc", "", "req-1", principal, auth.CodeReasonRequired},
		{"whitespace reason", "mem-1", "abc", "   ", "req-1", principal, auth.CodeReasonRequired},
		{"empty memoryId", "", "abc", "ok", "req-1", principal, auth.CodeMemoryNotFound},
		{"empty expectedEnvelopeHash", "mem-1", "", "ok", "req-1", principal, auth.CodeMemoryNotFound},
		{"empty requestId", "mem-1", "abc", "ok", "", principal, auth.CodeMemoryNotFound},
		{"zero principal", "mem-1", "abc", "ok", "req-1", auth.VerifiedApprovalPrincipal{}, auth.CodePrincipalInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &recordingApprovalStore{}
			_, err := ApproveMemory(context.Background(), store, policy, core.ApproveMemoryCommand{
				MemoryID:             c.memoryID,
				ExpectedEnvelopeHash: c.expected,
				Reason:               c.reason,
				RequestID:            c.requestID,
			}, c.principal)
			if auth.Code(err) != c.wantCode {
				t.Fatalf("code = %q, want %q (err %v)", auth.Code(err), c.wantCode, err)
			}
			if store.called {
				t.Fatal("store must not be reached for a rejected command")
			}
		})
	}
}

func TestApproveMemoryServiceDelegatesTheWholeStateChange(t *testing.T) {
	principal := approvalMustPrincipal(t)
	policy := authz.NewApprovalPolicy()
	want := core.ApprovalResult{
		MemoryID:              "mem-1",
		ApprovalEventID:       "evt-1",
		PreviousStatus:        "pending_review",
		CurrentStatus:         "approved",
		ReviewedEnvelopeHash:  "abc",
		ResultingEnvelopeHash: "def",
		PrincipalSubjectID:    principal.SubjectID(),
		MembershipID:          principal.MembershipID(),
		PolicyVersion:         authz.PolicyVersion,
		ApprovedAt:            "2026-08-05T12:00:00Z",
	}
	store := &recordingApprovalStore{result: want}
	cmd := core.ApproveMemoryCommand{
		MemoryID:             "mem-1",
		ExpectedEnvelopeHash: "abc",
		Reason:               "approved",
		RequestID:            "req-1",
	}

	got, err := ApproveMemory(context.Background(), store, policy, cmd, principal)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !store.called {
		t.Fatal("the store operation must be the single delegated authority")
	}
	if got != want {
		t.Fatalf("service must return the store result unchanged, got %+v want %+v", got, want)
	}
	if store.gotCmd != cmd {
		t.Fatalf("delegated command = %+v, want %+v", store.gotCmd, cmd)
	}
	if store.gotP.SubjectID() != principal.SubjectID() || store.gotP.MembershipID() != principal.MembershipID() {
		t.Fatalf("delegated principal = %+v, want %+v", store.gotP, principal)
	}
	if store.gotPolicy == nil {
		t.Fatal("delegated policy must not be nil")
	}
}

func TestPrincipalProvenanceMapsVerifiedClaim(t *testing.T) {
	principal := approvalMustPrincipal(t)
	src := principalProvenance(principal)

	if src.System != string(auth.AuthMethodSession) {
		t.Errorf("System = %q, want %q (the verified authentication method)", src.System, auth.AuthMethodSession)
	}
	if src.ActorID != principal.SubjectID() {
		t.Errorf("ActorID = %q, want %q", src.ActorID, principal.SubjectID())
	}
	if src.ActorKind != core.ActorKindHuman {
		t.Errorf("ActorKind = %q, want human (a verified principal IS a professional)", src.ActorKind)
	}
	if src.Session != principal.SessionID() {
		t.Errorf("Session = %q, want %q", src.Session, principal.SessionID())
	}
	// The Source is provenance only: it never carries roles, membership or
	// assurance — those authorize, and authorization belongs to the policy.
	if src.Reference != "" || src.Model != "" {
		t.Errorf("provenance carries unrelated fields: %+v", src)
	}
}

// TestApproveMemoryCommandCarriesNoPrincipalFields is the compile-level
// contract (ADR-003): the command shape is EXACTLY the four syntax fields. If a
// principal field ever sneaks in, this reflection check fails — transport
// payloads can never carry authority.
func TestApproveMemoryCommandCarriesNoPrincipalFields(t *testing.T) {
	typ := reflect.TypeOf(core.ApproveMemoryCommand{})
	got := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		got = append(got, typ.Field(i).Name)
	}
	sort.Strings(got)
	want := []string{"ExpectedEnvelopeHash", "MemoryID", "Reason", "RequestID"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command fields = %v, want exactly %v — principal fields are forbidden in the payload (ADR-003)", got, want)
	}
}
