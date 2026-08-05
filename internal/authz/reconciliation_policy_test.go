// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the
// reconciliation-policy/v0.5.0 authorization matrix: minimum CONTROLLER
// (stronger than the judgment policy's senior_accountant), minimum standard
// assurance, exact tenant/company scope, active membership, the check order and
// the exact policy version. Principals are minted through
// auth.Resolver.Authenticate exactly like production middleware (see
// approval_policy_test.go for the shared fixtures).
package authz_test

import (
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// reconciliationFixture is a company-scoped proposed reconciliation the policy
// evaluates.
func reconciliationFixture() core.Reconciliation {
	return core.Reconciliation{
		ID:               "reconciliation-1",
		TenantID:         "tenant-1",
		CompanyID:        "acme",
		FiscalPeriodID:   "202601",
		LeftMemoryID:     "obs-1",
		RightMemoryID:    "obs-2",
		Method:           "trial-balance",
		Currency:         "PEN",
		LeftAmountCents:  150000,
		RightAmountCents: 149500,
		VarianceCents:    500,
		ToleranceCents:   1000,
		Status:           core.ReconciliationProposed,
	}
}

func TestControllerConfirmsReconciliation(t *testing.T) {
	policy := authz.NewReconciliationPolicy()
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := policy.Authorize(principal, reconciliationFixture())
	if !decision.Allowed {
		t.Fatalf("controller must confirm a reconciliation, got %+v", decision)
	}
	if decision.ReasonCode != authz.ReasonAuthorized {
		t.Errorf("ReasonCode = %q, want AUTHORIZED", decision.ReasonCode)
	}
	if decision.PolicyVersion != authz.ReconciliationPolicyVersion {
		t.Errorf("PolicyVersion = %q, want %q", decision.PolicyVersion, authz.ReconciliationPolicyVersion)
	}
}

func TestSeniorAccountantDeniedReconciliationConfirmation(t *testing.T) {
	// senior_accountant authorizes judgments but NOT reconciliations: the
	// reconciliation floor is controller (a reconciliation decides a domain
	// balance pair).
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleSeniorAccountant}, auth.AssuranceStandard))
	decision := authz.NewReconciliationPolicy().Authorize(principal, reconciliationFixture())
	if decision.Allowed {
		t.Fatal("senior accountant must NOT confirm a reconciliation")
	}
	if decision.ReasonCode != auth.CodeRoleNotAuthorized {
		t.Errorf("ReasonCode = %q, want ROLE_NOT_AUTHORIZED", decision.ReasonCode)
	}
}

func TestReconciliationPolicyVersionConstantIsExact(t *testing.T) {
	if authz.ReconciliationPolicyVersion != "reconciliation-policy/v0.5.0" {
		t.Errorf("ReconciliationPolicyVersion = %q, want reconciliation-policy/v0.5.0", authz.ReconciliationPolicyVersion)
	}
	// Denied decisions must also carry the exact policy version.
	foreign := mustPrincipal(t, fixtureStore("tenant-2", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := authz.NewReconciliationPolicy().Authorize(foreign, reconciliationFixture())
	if decision.Allowed {
		t.Fatal("cross-tenant principal must be denied")
	}
	if decision.PolicyVersion != "reconciliation-policy/v0.5.0" {
		t.Errorf("denied PolicyVersion = %q, want reconciliation-policy/v0.5.0", decision.PolicyVersion)
	}
}

func TestAccountantDeniedReconciliationConfirmation(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
	decision := authz.NewReconciliationPolicy().Authorize(principal, reconciliationFixture())
	if decision.Allowed {
		t.Fatal("accountant must NOT confirm a reconciliation")
	}
	if decision.ReasonCode != auth.CodeRoleNotAuthorized {
		t.Errorf("ReasonCode = %q, want ROLE_NOT_AUTHORIZED", decision.ReasonCode)
	}
}

func TestTaxReviewerDeniedReconciliationConfirmation(t *testing.T) {
	// Tax roles are explicit and NEVER imply the accounting ladder: a
	// tax_reviewer cannot adjudicate a reconciliation, even with full assurance.
	for _, role := range []auth.AccountingRole{auth.RoleTaxReviewer, auth.RoleAuthorizedTaxProfessional} {
		principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{role}, auth.AssuranceStrong))
		decision := authz.NewReconciliationPolicy().Authorize(principal, reconciliationFixture())
		if decision.Allowed {
			t.Fatalf("tax role %s must NOT confirm a reconciliation", role)
		}
		if decision.ReasonCode != auth.CodeRoleNotAuthorized {
			t.Errorf("tax role %s: ReasonCode = %q, want ROLE_NOT_AUTHORIZED", role, decision.ReasonCode)
		}
	}
}

func TestCrossTenantReconciliationDenied(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-2", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := authz.NewReconciliationPolicy().Authorize(principal, reconciliationFixture())
	if decision.Allowed {
		t.Fatal("cross-tenant principal must be denied")
	}
	if decision.ReasonCode != auth.CodeTenantScopeMismatch {
		t.Errorf("ReasonCode = %q, want TENANT_SCOPE_MISMATCH", decision.ReasonCode)
	}
}

func TestReconciliationCompanyOutOfScopeDenied(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "other-company", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := authz.NewReconciliationPolicy().Authorize(principal, reconciliationFixture())
	if decision.Allowed {
		t.Fatal("company out of scope must be denied")
	}
	if decision.ReasonCode != auth.CodeCompanyScopeDenied {
		t.Errorf("ReasonCode = %q, want COMPANY_SCOPE_DENIED", decision.ReasonCode)
	}
}

func TestReconciliationWithoutCompanyFailsClosed(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	r := reconciliationFixture()
	r.CompanyID = ""
	decision := authz.NewReconciliationPolicy().Authorize(principal, r)
	if decision.Allowed {
		t.Fatal("a reconciliation without a company must be denied")
	}
	if decision.ReasonCode != auth.CodeCompanyScopeDenied {
		t.Errorf("ReasonCode = %q, want COMPANY_SCOPE_DENIED", decision.ReasonCode)
	}
}

func TestInactiveMembershipReconciliationDenied(t *testing.T) {
	store := fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard)
	fixed := store.(*fixedSessionStore)
	fixed.membership.ID = ""

	principal := mustPrincipal(t, store)
	if principal.MembershipID() != "" {
		t.Fatalf("fixture must carry an empty membership id, got %q", principal.MembershipID())
	}
	decision := authz.NewReconciliationPolicy().Authorize(principal, reconciliationFixture())
	if decision.Allowed {
		t.Fatal("principal without active membership must be denied")
	}
	if decision.ReasonCode != auth.CodeMembershipInactive {
		t.Errorf("ReasonCode = %q, want MEMBERSHIP_INACTIVE", decision.ReasonCode)
	}
}

func TestReconciliationAssuranceTooLowDenied(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceLow))
	decision := authz.NewReconciliationPolicy().Authorize(principal, reconciliationFixture())
	if decision.Allowed {
		t.Fatal("low assurance must be denied")
	}
	if decision.ReasonCode != auth.CodeAssuranceTooLow {
		t.Errorf("ReasonCode = %q, want ASSURANCE_TOO_LOW", decision.ReasonCode)
	}
}

func TestReconciliationCheckOrderReturnsFirstFrozenCode(t *testing.T) {
	policy := authz.NewReconciliationPolicy()
	// Cross-tenant + wrong role + low assurance: tenant wins (first check).
	foreign := mustPrincipal(t, fixtureStore("tenant-9", "other", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceLow))
	if decision := policy.Authorize(foreign, reconciliationFixture()); decision.ReasonCode != auth.CodeTenantScopeMismatch {
		t.Errorf("ReasonCode = %q, want TENANT_SCOPE_MISMATCH (check order)", decision.ReasonCode)
	}
	// Same tenant, wrong company + wrong role: company wins.
	wrongCompany := mustPrincipal(t, fixtureStore("tenant-1", "other", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceLow))
	if decision := policy.Authorize(wrongCompany, reconciliationFixture()); decision.ReasonCode != auth.CodeCompanyScopeDenied {
		t.Errorf("ReasonCode = %q, want COMPANY_SCOPE_DENIED (check order)", decision.ReasonCode)
	}
	// Same tenant/company, weak role + low assurance: role wins over assurance.
	senior := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleSeniorAccountant}, auth.AssuranceLow))
	if decision := policy.Authorize(senior, reconciliationFixture()); decision.ReasonCode != auth.CodeRoleNotAuthorized {
		t.Errorf("ReasonCode = %q, want ROLE_NOT_AUTHORIZED (check order)", decision.ReasonCode)
	}
}
