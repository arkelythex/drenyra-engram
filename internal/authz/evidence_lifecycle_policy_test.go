// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the v0.8.0 evidence
// lifecycle policy (evidence-lifecycle-policy/v0.8.0): role matrices per act,
// the deny-list precedence (operational_accountant, *admin, agent, system),
// the SoD rules (approver ≠ requester, second approver ≠ first approver), the
// dual-approval configuration gate, cross-tenant/company and principal
// authentication failures (inactive membership, low assurance), and the frozen
// check order — denial precedes allow. Principals are minted through
// auth.Resolver.Authenticate with a minimal fake session store, the same path
// production middleware uses. Every helper is lifecycle-prefixed to stay
// private to this file (sibling authz tests define their own fixtures).
package authz_test

import (
	"context"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// lifecycleFixedSessionStore ignores the token hash and returns the configured
// session/membership (or the configured error) — enough to mint fixtures.
type lifecycleFixedSessionStore struct {
	session    auth.StoredSession
	membership auth.MembershipRecord
	lookupErr  error
}

func (f *lifecycleFixedSessionStore) LookupByTokenHash(context.Context, string) (auth.StoredSession, error) {
	if f.lookupErr != nil {
		return auth.StoredSession{}, f.lookupErr
	}
	return f.session, nil
}

func (f *lifecycleFixedSessionStore) LoadMembership(context.Context, string) (auth.MembershipRecord, error) {
	return f.membership, nil
}

// lifecycleFixtureStore builds a session store that mints a principal with the
// given membership attributes (subject-1 / tenant-1 / acme by default).
func lifecycleFixtureStore(tenantID, companyID string, roles []auth.AccountingRole, assurance auth.AssuranceLevel) auth.SessionStore {
	return &lifecycleFixedSessionStore{
		session: auth.StoredSession{
			ID:                   "session-1",
			MembershipID:         "membership-1",
			AuthenticationMethod: auth.AuthMethodSession,
			AssuranceLevel:       assurance,
			AuthenticatedAt:      "2026-08-05T12:00:00Z",
			ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
		membership: auth.MembershipRecord{
			ID:            "membership-1",
			SubjectID:     "subject-1",
			TenantID:      tenantID,
			CompanyID:     companyID,
			Status:        "active",
			Roles:         roles,
			CompanyActive: true,
		},
	}
}

func lifecycleMustPrincipal(t *testing.T, store auth.SessionStore) auth.VerifiedApprovalPrincipal {
	t.Helper()
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

func lifecyclePrincipalWithRoles(t *testing.T, roles ...auth.AccountingRole) auth.VerifiedApprovalPrincipal {
	t.Helper()
	return lifecycleMustPrincipal(t, lifecycleFixtureStore("tenant-1", "acme", roles, auth.AssuranceStandard))
}

func lifecyclePrincipalWithAssurance(t *testing.T, assurance auth.AssuranceLevel, roles ...auth.AccountingRole) auth.VerifiedApprovalPrincipal {
	t.Helper()
	return lifecycleMustPrincipal(t, lifecycleFixtureStore("tenant-1", "acme", roles, assurance))
}

// lifecyclePrincipalWithSubject mints a principal with an explicit subject id,
// so SoD fixtures can prove DISTINCT principals (the default fixture is
// always subject-1).
func lifecyclePrincipalWithSubject(t *testing.T, subjectID string, roles ...auth.AccountingRole) auth.VerifiedApprovalPrincipal {
	t.Helper()
	store := lifecycleFixtureStore("tenant-1", "acme", roles, auth.AssuranceStandard)
	store.(*lifecycleFixedSessionStore).membership.SubjectID = subjectID
	return lifecycleMustPrincipal(t, store)
}

func lifecycleCrossTenantPrincipal(t *testing.T, roles ...auth.AccountingRole) auth.VerifiedApprovalPrincipal {
	t.Helper()
	return lifecycleMustPrincipal(t, lifecycleFixtureStore("tenant-2", "acme", roles, auth.AssuranceStandard))
}

func lifecycleOutOfScopePrincipal(t *testing.T, roles ...auth.AccountingRole) auth.VerifiedApprovalPrincipal {
	t.Helper()
	return lifecycleMustPrincipal(t, lifecycleFixtureStore("tenant-1", "other-company", roles, auth.AssuranceStandard))
}

// lifecycleReq builds a base request for the given action and principal
// against the default object scope (tenant-1/acme, category 'invoice', dual
// approval off).
func lifecycleReq(action authz.LifecycleAction, p auth.VerifiedApprovalPrincipal) authz.LifecycleAuthorizationRequest {
	return authz.LifecycleAuthorizationRequest{
		Action:               action,
		Principal:            p,
		ActorKind:            core.ActorKindHuman,
		TenantID:             "tenant-1",
		CompanyID:            "acme",
		Category:             "invoice",
		DualApprovalRequired: false,
	}
}

func lifecycleMustAllowed(t *testing.T, d authz.LifecycleDecision) {
	t.Helper()
	if !d.Allowed {
		t.Fatalf("decision must be allowed, got %+v", d)
	}
	if d.ReasonCode != authz.ReasonAuthorized {
		t.Errorf("allowed ReasonCode = %q, want AUTHORIZED", d.ReasonCode)
	}
	if d.PolicyVersion != authz.LifecyclePolicyVersion {
		t.Errorf("PolicyVersion = %q, want %q", d.PolicyVersion, authz.LifecyclePolicyVersion)
	}
}

func lifecycleMustDenied(t *testing.T, d authz.LifecycleDecision, wantCode string) {
	t.Helper()
	if d.Allowed {
		t.Fatalf("decision must be denied, got %+v", d)
	}
	if d.ReasonCode != wantCode {
		t.Errorf("ReasonCode = %q, want %q", d.ReasonCode, wantCode)
	}
	if d.PolicyVersion != authz.LifecyclePolicyVersion {
		t.Errorf("PolicyVersion = %q, want %q", d.PolicyVersion, authz.LifecyclePolicyVersion)
	}
}

func TestLifecyclePolicyVersionConstantIsExact(t *testing.T) {
	if authz.LifecyclePolicyVersion != "evidence-lifecycle-policy/v0.8.0" {
		t.Fatalf("LifecyclePolicyVersion = %q, want evidence-lifecycle-policy/v0.8.0", authz.LifecyclePolicyVersion)
	}
	// Allowed and denied decisions both stamp the exact version.
	p := lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer)
	allowed := authz.NewEvidenceLifecyclePolicy().Authorize(lifecycleReq(authz.LifecycleActionApprovePurge, p))
	if !allowed.Allowed || allowed.PolicyVersion != "evidence-lifecycle-policy/v0.8.0" {
		t.Fatalf("allowed decision must stamp the exact policy version, got %+v", allowed)
	}
	denied := authz.NewEvidenceLifecyclePolicy().Authorize(lifecycleReq(authz.LifecycleActionRequestPurge, p))
	if denied.Allowed || denied.PolicyVersion != "evidence-lifecycle-policy/v0.8.0" {
		t.Fatalf("denied decision must stamp the exact policy version, got %+v", denied)
	}
}

func TestRequestPurgeRoleMatrix(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	tests := []struct {
		name  string
		roles []auth.AccountingRole
		want  string // wantCode; empty means allowed
	}{
		{name: "accountant may request", roles: []auth.AccountingRole{auth.RoleAccountant}},
		{name: "senior accountant may request", roles: []auth.AccountingRole{auth.RoleSeniorAccountant}},
		{name: "controller may request", roles: []auth.AccountingRole{auth.RoleController}},
		{name: "records compliance officer may NOT request", roles: []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, want: auth.CodeRoleNotAuthorized},
		{name: "tenant records owner may NOT request", roles: []auth.AccountingRole{auth.RoleTenantRecordsOwner}, want: auth.CodeRoleNotAuthorized},
		{name: "tax responsible may NOT request", roles: []auth.AccountingRole{auth.RoleTaxResponsible}, want: auth.CodeRoleNotAuthorized},
		{name: "tax reviewer may NOT request", roles: []auth.AccountingRole{auth.RoleTaxReviewer}, want: auth.CodeRoleNotAuthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := policy.Authorize(lifecycleReq(authz.LifecycleActionRequestPurge, lifecyclePrincipalWithRoles(t, tt.roles...)))
			if tt.want == "" {
				lifecycleMustAllowed(t, d)
			} else {
				lifecycleMustDenied(t, d, tt.want)
			}
		})
	}
}

func TestRequestPurgeDenyList(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	tests := []struct {
		name  string
		roles []auth.AccountingRole
	}{
		{name: "operational_accountant is deny-listed", roles: []auth.AccountingRole{auth.RoleOperationalAccountant}},
		{name: "generic admin token is deny-listed", roles: []auth.AccountingRole{auth.AccountingRole("admin")}},
		{name: "admin-containing token is deny-listed", roles: []auth.AccountingRole{auth.AccountingRole("deployment_admin")}},
		{name: "deny-listed role never wins over ladder roles", roles: []auth.AccountingRole{auth.RoleController, auth.RoleOperationalAccountant}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The deny-list precedes every allow: even a role that could
			// request (controller) is denied when a deny-listed role rides
			// along, and the reason is ROLE_DENIED.
			d := policy.Authorize(lifecycleReq(authz.LifecycleActionRequestPurge, lifecyclePrincipalWithRoles(t, tt.roles...)))
			lifecycleMustDenied(t, d, auth.CodeRoleDenied)
		})
	}
}

func TestActorKindDenyAtEveryGate(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	for _, kind := range []core.ActorKind{core.ActorKindAgent, core.ActorKindSystem} {
		for _, action := range []authz.LifecycleAction{
			authz.LifecycleActionRequestPurge,
			authz.LifecycleActionApprovePurge,
			authz.LifecycleActionSecondApprove,
			authz.LifecycleActionRejectPurge,
			authz.LifecycleActionWithdrawApproval,
			authz.LifecycleActionExecutePurge,
		} {
			name := string(kind) + "-" + string(action)
			t.Run(name, func(t *testing.T) {
				r := lifecycleReq(action, lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer))
				r.ActorKind = kind
				lifecycleMustDenied(t, policy.Authorize(r), auth.CodeRoleDenied)
			})
		}
	}
}

func TestApprovePurgeRoleMatrix(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	tests := []struct {
		name  string
		roles []auth.AccountingRole
		want  string // wantCode; empty means allowed
	}{
		{name: "records compliance officer is the default approver", roles: []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}},
		{name: "tenant records owner is the default approver", roles: []auth.AccountingRole{auth.RoleTenantRecordsOwner}},
		{name: "controller is second-approver only", roles: []auth.AccountingRole{auth.RoleController}, want: auth.CodeRoleNotAuthorized},
		{name: "tax responsible is second-approver only", roles: []auth.AccountingRole{auth.RoleTaxResponsible}, want: auth.CodeRoleNotAuthorized},
		{name: "senior accountant may NOT be default approver", roles: []auth.AccountingRole{auth.RoleSeniorAccountant}, want: auth.CodeRoleNotAuthorized},
		{name: "accountant may NOT be default approver", roles: []auth.AccountingRole{auth.RoleAccountant}, want: auth.CodeRoleNotAuthorized},
		{name: "operational_accountant is deny-listed", roles: []auth.AccountingRole{auth.RoleOperationalAccountant}, want: auth.CodeRoleDenied},
		{name: "admin token is deny-listed before role allow", roles: []auth.AccountingRole{auth.AccountingRole("generic_admin")}, want: auth.CodeRoleDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := policy.Authorize(lifecycleReq(authz.LifecycleActionApprovePurge, lifecyclePrincipalWithRoles(t, tt.roles...)))
			if tt.want == "" {
				lifecycleMustAllowed(t, d)
			} else {
				lifecycleMustDenied(t, d, tt.want)
			}
		})
	}
}

func TestApproveRequesterCannotBeApprover(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	approver := lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer)
	r := lifecycleReq(authz.LifecycleActionApprovePurge, approver)
	r.Requester = &approver // the approver IS the requester
	lifecycleMustDenied(t, policy.Authorize(r), auth.CodeApproverIsRequester)
}

func TestApproveDistinctRequesterIsAllowed(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	approver := lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer)
	requester := lifecyclePrincipalWithSubject(t, "subject-2", auth.RoleAccountant)
	r := lifecycleReq(authz.LifecycleActionApprovePurge, approver)
	r.Requester = &requester
	lifecycleMustAllowed(t, policy.Authorize(r))
}

func TestSecondApproveRoleMatrix(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	tests := []struct {
		name  string
		roles []auth.AccountingRole
		want  string // wantCode; empty means allowed
	}{
		{name: "controller is a second approver", roles: []auth.AccountingRole{auth.RoleController}},
		{name: "tax responsible is a second approver", roles: []auth.AccountingRole{auth.RoleTaxResponsible}},
		{name: "default approver may NEVER be second approver", roles: []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, want: auth.CodeRoleNotAuthorized},
		{name: "tenant records owner may NEVER be second approver", roles: []auth.AccountingRole{auth.RoleTenantRecordsOwner}, want: auth.CodeRoleNotAuthorized},
		{name: "senior accountant does NOT ladder into second approval", roles: []auth.AccountingRole{auth.RoleSeniorAccountant}, want: auth.CodeRoleNotAuthorized},
		{name: "accountant may NOT second approve", roles: []auth.AccountingRole{auth.RoleAccountant}, want: auth.CodeRoleNotAuthorized},
		{name: "operational_accountant is deny-listed", roles: []auth.AccountingRole{auth.RoleOperationalAccountant}, want: auth.CodeRoleDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := lifecycleReq(authz.LifecycleActionSecondApprove, lifecyclePrincipalWithRoles(t, tt.roles...))
			r.DualApprovalRequired = true
			first := lifecyclePrincipalWithSubject(t, "subject-2", auth.RoleRecordsComplianceOfficer)
			r.FirstApprover = &first
			d := policy.Authorize(r)
			if tt.want == "" {
				lifecycleMustAllowed(t, d)
			} else {
				lifecycleMustDenied(t, d, tt.want)
			}
		})
	}
}

func TestSecondApproveRequiresDualCategoryConfiguration(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	second := lifecyclePrincipalWithRoles(t, auth.RoleController)
	r := lifecycleReq(authz.LifecycleActionSecondApprove, second)
	// Category is NOT configured for dual approval → DUAL_APPROVAL_REQUIRED.
	lifecycleMustDenied(t, policy.Authorize(r), auth.CodeDualApprovalRequired)

	// A configured category authorizes the same principal.
	r.DualApprovalRequired = true
	lifecycleMustAllowed(t, policy.Authorize(r))
}

func TestSecondApproveRequiresDistinctPrincipal(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	// The same principal holding BOTH approval roles cannot be the second
	// approver of its own first approval.
	same := lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer, auth.RoleController)
	r := lifecycleReq(authz.LifecycleActionSecondApprove, same)
	r.DualApprovalRequired = true
	r.FirstApprover = &same
	lifecycleMustDenied(t, policy.Authorize(r), auth.CodeSamePrincipalSecondApproval)

	// A DISTINCT controller second-approving a records officer is allowed.
	second := lifecyclePrincipalWithSubject(t, "subject-2", auth.RoleController)
	first := lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer)
	ok := lifecycleReq(authz.LifecycleActionSecondApprove, second)
	ok.DualApprovalRequired = true
	ok.FirstApprover = &first
	lifecycleMustAllowed(t, policy.Authorize(ok))
}

func TestRejectWithdrawExecuteRoles(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	tests := []struct {
		name   string
		action authz.LifecycleAction
		roles  []auth.AccountingRole
		want   string // wantCode; empty means allowed
	}{
		{name: "reject by default approver", action: authz.LifecycleActionRejectPurge, roles: []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}},
		{name: "reject by tenant records owner", action: authz.LifecycleActionRejectPurge, roles: []auth.AccountingRole{auth.RoleTenantRecordsOwner}},
		{name: "reject by controller denied", action: authz.LifecycleActionRejectPurge, roles: []auth.AccountingRole{auth.RoleController}, want: auth.CodeRoleNotAuthorized},
		{name: "withdraw by default approver", action: authz.LifecycleActionWithdrawApproval, roles: []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}},
		{name: "withdraw by dual second approver", action: authz.LifecycleActionWithdrawApproval, roles: []auth.AccountingRole{auth.RoleTaxResponsible}},
		{name: "withdraw by accountant denied", action: authz.LifecycleActionWithdrawApproval, roles: []auth.AccountingRole{auth.RoleAccountant}, want: auth.CodeRoleNotAuthorized},
		{name: "execute by default approver", action: authz.LifecycleActionExecutePurge, roles: []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}},
		{name: "execute by controller second approver", action: authz.LifecycleActionExecutePurge, roles: []auth.AccountingRole{auth.RoleController}},
		{name: "execute by accountant denied", action: authz.LifecycleActionExecutePurge, roles: []auth.AccountingRole{auth.RoleAccountant}, want: auth.CodeRoleNotAuthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := policy.Authorize(lifecycleReq(tt.action, lifecyclePrincipalWithRoles(t, tt.roles...)))
			if tt.want == "" {
				lifecycleMustAllowed(t, d)
			} else {
				lifecycleMustDenied(t, d, tt.want)
			}
		})
	}
}

func TestLifecycleCrossTenantDenied(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	for _, action := range []authz.LifecycleAction{
		authz.LifecycleActionRequestPurge, authz.LifecycleActionApprovePurge, authz.LifecycleActionExecutePurge,
	} {
		t.Run(string(action), func(t *testing.T) {
			r := lifecycleReq(action, lifecycleCrossTenantPrincipal(t, auth.RoleRecordsComplianceOfficer))
			lifecycleMustDenied(t, policy.Authorize(r), auth.CodeTenantScopeMismatch)
		})
	}
}

func TestLifecycleCompanyOutOfScopeDenied(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	p := lifecycleOutOfScopePrincipal(t, auth.RoleRecordsComplianceOfficer)
	lifecycleMustDenied(t, policy.Authorize(lifecycleReq(authz.LifecycleActionApprovePurge, p)), auth.CodeCompanyScopeDenied)
}

func TestLifecycleInactiveMembershipDenied(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	store := lifecycleFixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard)
	store.(*lifecycleFixedSessionStore).membership.ID = ""

	p := lifecycleMustPrincipal(t, store)
	if p.MembershipID() != "" {
		t.Fatalf("fixture must carry an empty membership id, got %q", p.MembershipID())
	}
	lifecycleMustDenied(t, policy.Authorize(lifecycleReq(authz.LifecycleActionApprovePurge, p)), auth.CodeMembershipInactive)
}

func TestLifecycleAssuranceTooLowDenied(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	tests := []struct {
		name   string
		action authz.LifecycleAction
		roles  []auth.AccountingRole
	}{
		// Each principal must pass the role gate first so the assurance floor
		// (position 7) is what fires.
		{name: "request", action: authz.LifecycleActionRequestPurge, roles: []auth.AccountingRole{auth.RoleAccountant}},
		{name: "approve", action: authz.LifecycleActionApprovePurge, roles: []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}},
		{name: "execute", action: authz.LifecycleActionExecutePurge, roles: []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := lifecyclePrincipalWithAssurance(t, auth.AssuranceLow, tt.roles...)
			lifecycleMustDenied(t, policy.Authorize(lifecycleReq(tt.action, p)), auth.CodeAssuranceTooLow)
		})
	}
}

func TestLifecycleCheckOrderIsFrozen(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()

	// Frozen order (design §8.2): tenant → company scope → membership active →
	// actor-kind deny → role deny-list → role allow → assurance → requester ≠
	// approver (SoD) → dual-approval config → second approver distinct
	// principal. First reason code wins; denial precedes allow.

	// 1 over 5: cross-tenant + operational_accountant → TENANT_SCOPE_MISMATCH.
	cross := lifecycleReq(authz.LifecycleActionRequestPurge,
		lifecycleCrossTenantPrincipal(t, auth.RoleOperationalAccountant))
	lifecycleMustDenied(t, policy.Authorize(cross), auth.CodeTenantScopeMismatch)

	// 5 over 6/7: deny-list beats role allow and assurance.
	denied := lifecycleReq(authz.LifecycleActionRequestPurge,
		lifecyclePrincipalWithAssurance(t, auth.AssuranceLow, auth.RoleOperationalAccountant))
	lifecycleMustDenied(t, policy.Authorize(denied), auth.CodeRoleDenied)

	// 4 over 5: an agent carrying a deny-listed role is denied by actor-kind
	// (position 4 fires before the role deny-list at position 5).
	agent := lifecycleReq(authz.LifecycleActionApprovePurge,
		lifecyclePrincipalWithRoles(t, auth.RoleOperationalAccountant))
	agent.ActorKind = core.ActorKindAgent
	lifecycleMustDenied(t, policy.Authorize(agent), auth.CodeRoleDenied)

	// 6 over 7: wrong role + low assurance → ROLE_NOT_AUTHORIZED (role allow
	// fires before the assurance floor).
	wrongRole := lifecycleReq(authz.LifecycleActionApprovePurge,
		lifecyclePrincipalWithAssurance(t, auth.AssuranceLow, auth.RoleAccountant))
	lifecycleMustDenied(t, policy.Authorize(wrongRole), auth.CodeRoleNotAuthorized)

	// 7 over 8: low assurance + requester==approver → ASSURANCE_TOO_LOW.
	soD := lifecycleReq(authz.LifecycleActionApprovePurge,
		lifecyclePrincipalWithAssurance(t, auth.AssuranceLow, auth.RoleRecordsComplianceOfficer))
	low := soD.Principal
	soD.Requester = &low
	lifecycleMustDenied(t, policy.Authorize(soD), auth.CodeAssuranceTooLow)

	// 8 over 9/10: approver==requester beats dual config and distinctness.
	// requester and second are both subject-1 (same human).
	second := lifecyclePrincipalWithRoles(t, auth.RoleController)
	dual := lifecycleReq(authz.LifecycleActionSecondApprove, second)
	dual.DualApprovalRequired = true
	requester := lifecyclePrincipalWithRoles(t, auth.RoleAccountant)
	dual.Requester = &requester
	first := lifecyclePrincipalWithSubject(t, "subject-2", auth.RoleRecordsComplianceOfficer)
	dual.FirstApprover = &first
	lifecycleMustDenied(t, policy.Authorize(dual), auth.CodeApproverIsRequester)

	// 9 over 10: non-dual category + first approver == second approver →
	// DUAL_APPROVAL_REQUIRED fires before the distinct-principal check.
	// requester is distinct (subject-2) so position 8 passes.
	r2 := requester2(t)
	same := lifecycleReq(authz.LifecycleActionSecondApprove, second)
	same.Requester = &r2
	same.FirstApprover = &second
	lifecycleMustDenied(t, policy.Authorize(same), auth.CodeDualApprovalRequired)

	// 10: configured dual category + first approver == second approver →
	// SAME_PRINCIPAL_SECOND_APPROVAL.
	r3 := requester2(t)
	distinct := lifecycleReq(authz.LifecycleActionSecondApprove, second)
	distinct.Requester = &r3
	distinct.DualApprovalRequired = true
	distinct.FirstApprover = &second
	lifecycleMustDenied(t, policy.Authorize(distinct), auth.CodeSamePrincipalSecondApproval)
}

// requester2 mints a distinct-subject requester (subject-2) for check-order
// fixtures where position 8 must PASS so a later position can fire.
func requester2(t *testing.T) auth.VerifiedApprovalPrincipal {
	t.Helper()
	return lifecyclePrincipalWithSubject(t, "subject-2", auth.RoleAccountant)
}

func TestLifecycleUnknownActionIsDenied(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	p := lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer)
	r := lifecycleReq(authz.LifecycleAction("cancel"), p)
	lifecycleMustDenied(t, policy.Authorize(r), auth.CodeRoleNotAuthorized)
}

func TestLifecycleZeroPrincipalFailsClosed(t *testing.T) {
	policy := authz.NewEvidenceLifecyclePolicy()
	var zero auth.VerifiedApprovalPrincipal
	r := authz.LifecycleAuthorizationRequest{
		Action:    authz.LifecycleActionApprovePurge,
		Principal: zero,
		ActorKind: core.ActorKindHuman,
		TenantID:  "tenant-1",
		CompanyID: "acme",
	}
	d := policy.Authorize(r)
	if d.Allowed {
		t.Fatal("zero principal must fail closed")
	}
	if d.ReasonCode != auth.CodeTenantScopeMismatch {
		t.Errorf("ReasonCode = %q, want TENANT_SCOPE_MISMATCH (first check)", d.ReasonCode)
	}
}
