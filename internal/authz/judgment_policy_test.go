// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the judgment-policy/v0.4.0
// authorization matrix: minimum senior_accountant (controller dominates, tax
// roles do not), minimum standard assurance, exact tenant/company scope, active
// membership, the check order and the exact policy version. Principals are
// minted through auth.Resolver.Authenticate exactly like production middleware
// (see approval_policy_test.go for the shared fixtures).
package authz_test

import (
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// judgmentFixture is a company-scoped proposed judgment the policy evaluates.
func judgmentFixture() core.AccountingJudgment {
	return core.AccountingJudgment{
		ID:        "judgment-1",
		TenantID:  "tenant-1",
		CompanyID: "acme",
		FromID:    "obs-1",
		ToID:      "obs-2",
		Relation:  core.RelationContradicts,
		Status:    core.JudgmentProposed,
	}
}

func TestControllerConfirmsJudgment(t *testing.T) {
	policy := authz.NewJudgmentPolicy()
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := policy.Authorize(principal, judgmentFixture())
	if !decision.Allowed {
		t.Fatalf("controller must confirm a judgment, got %+v", decision)
	}
	if decision.ReasonCode != authz.ReasonAuthorized {
		t.Errorf("ReasonCode = %q, want AUTHORIZED", decision.ReasonCode)
	}
	if decision.PolicyVersion != authz.JudgmentPolicyVersion {
		t.Errorf("PolicyVersion = %q, want %q", decision.PolicyVersion, authz.JudgmentPolicyVersion)
	}
}

func TestSeniorAccountantConfirmsJudgment(t *testing.T) {
	// senior_accountant is the MINIMUM adjudicator role.
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleSeniorAccountant}, auth.AssuranceStandard))
	if decision := authz.NewJudgmentPolicy().Authorize(principal, judgmentFixture()); !decision.Allowed {
		t.Fatalf("senior accountant must confirm a judgment, got %+v", decision)
	}
}

func TestJudgmentPolicyVersionConstantIsExact(t *testing.T) {
	if authz.JudgmentPolicyVersion != "judgment-policy/v0.4.0" {
		t.Errorf("JudgmentPolicyVersion = %q, want judgment-policy/v0.4.0", authz.JudgmentPolicyVersion)
	}
	// Denied decisions must also carry the exact policy version.
	foreign := mustPrincipal(t, fixtureStore("tenant-2", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := authz.NewJudgmentPolicy().Authorize(foreign, judgmentFixture())
	if decision.Allowed {
		t.Fatal("cross-tenant principal must be denied")
	}
	if decision.PolicyVersion != "judgment-policy/v0.4.0" {
		t.Errorf("denied PolicyVersion = %q, want judgment-policy/v0.4.0", decision.PolicyVersion)
	}
}

func TestAccountantDeniedJudgmentConfirmation(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
	decision := authz.NewJudgmentPolicy().Authorize(principal, judgmentFixture())
	if decision.Allowed {
		t.Fatal("accountant must NOT confirm a judgment")
	}
	if decision.ReasonCode != auth.CodeRoleNotAuthorized {
		t.Errorf("ReasonCode = %q, want ROLE_NOT_AUTHORIZED", decision.ReasonCode)
	}
}

func TestTaxReviewerDeniedJudgmentConfirmation(t *testing.T) {
	// Tax roles are explicit and NEVER imply the accounting ladder: a
	// tax_reviewer cannot adjudicate, even with full assurance.
	for _, role := range []auth.AccountingRole{auth.RoleTaxReviewer, auth.RoleAuthorizedTaxProfessional} {
		principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{role}, auth.AssuranceStrong))
		decision := authz.NewJudgmentPolicy().Authorize(principal, judgmentFixture())
		if decision.Allowed {
			t.Fatalf("tax role %s must NOT confirm a judgment", role)
		}
		if decision.ReasonCode != auth.CodeRoleNotAuthorized {
			t.Errorf("tax role %s: ReasonCode = %q, want ROLE_NOT_AUTHORIZED", role, decision.ReasonCode)
		}
	}
}

func TestCrossTenantJudgmentDenied(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-2", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := authz.NewJudgmentPolicy().Authorize(principal, judgmentFixture())
	if decision.Allowed {
		t.Fatal("cross-tenant principal must be denied")
	}
	if decision.ReasonCode != auth.CodeTenantScopeMismatch {
		t.Errorf("ReasonCode = %q, want TENANT_SCOPE_MISMATCH", decision.ReasonCode)
	}
}

func TestJudgmentCompanyOutOfScopeDenied(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "other-company", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := authz.NewJudgmentPolicy().Authorize(principal, judgmentFixture())
	if decision.Allowed {
		t.Fatal("company out of scope must be denied")
	}
	if decision.ReasonCode != auth.CodeCompanyScopeDenied {
		t.Errorf("ReasonCode = %q, want COMPANY_SCOPE_DENIED", decision.ReasonCode)
	}
}

func TestJudgmentWithoutCompanyFailsClosed(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	j := judgmentFixture()
	j.CompanyID = ""
	decision := authz.NewJudgmentPolicy().Authorize(principal, j)
	if decision.Allowed {
		t.Fatal("a judgment without a company must be denied")
	}
	if decision.ReasonCode != auth.CodeCompanyScopeDenied {
		t.Errorf("ReasonCode = %q, want COMPANY_SCOPE_DENIED", decision.ReasonCode)
	}
}

func TestInactiveMembershipJudgmentDenied(t *testing.T) {
	store := fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard)
	fixed := store.(*fixedSessionStore)
	fixed.membership.ID = ""

	principal := mustPrincipal(t, store)
	if principal.MembershipID() != "" {
		t.Fatalf("fixture must carry an empty membership id, got %q", principal.MembershipID())
	}
	decision := authz.NewJudgmentPolicy().Authorize(principal, judgmentFixture())
	if decision.Allowed {
		t.Fatal("principal without active membership must be denied")
	}
	if decision.ReasonCode != auth.CodeMembershipInactive {
		t.Errorf("ReasonCode = %q, want MEMBERSHIP_INACTIVE", decision.ReasonCode)
	}
}

func TestJudgmentAssuranceTooLowDenied(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceLow))
	decision := authz.NewJudgmentPolicy().Authorize(principal, judgmentFixture())
	if decision.Allowed {
		t.Fatal("low assurance must be denied")
	}
	if decision.ReasonCode != auth.CodeAssuranceTooLow {
		t.Errorf("ReasonCode = %q, want ASSURANCE_TOO_LOW", decision.ReasonCode)
	}
}

func TestJudgmentCheckOrderReturnsFirstFrozenCode(t *testing.T) {
	policy := authz.NewJudgmentPolicy()
	// Cross-tenant + wrong role + low assurance: tenant wins (first check).
	foreign := mustPrincipal(t, fixtureStore("tenant-9", "other", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceLow))
	if decision := policy.Authorize(foreign, judgmentFixture()); decision.ReasonCode != auth.CodeTenantScopeMismatch {
		t.Errorf("ReasonCode = %q, want TENANT_SCOPE_MISMATCH (check order)", decision.ReasonCode)
	}
	// Same tenant, wrong company + wrong role: company wins.
	wrongCompany := mustPrincipal(t, fixtureStore("tenant-1", "other", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceLow))
	if decision := policy.Authorize(wrongCompany, judgmentFixture()); decision.ReasonCode != auth.CodeCompanyScopeDenied {
		t.Errorf("ReasonCode = %q, want COMPANY_SCOPE_DENIED (check order)", decision.ReasonCode)
	}
	// Same tenant/company, weak role + low assurance: role wins over assurance.
	accountant := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceLow))
	if decision := policy.Authorize(accountant, judgmentFixture()); decision.ReasonCode != auth.CodeRoleNotAuthorized {
		t.Errorf("ReasonCode = %q, want ROLE_NOT_AUTHORIZED (check order)", decision.ReasonCode)
	}
}
