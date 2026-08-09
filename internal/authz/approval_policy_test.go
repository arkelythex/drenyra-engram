// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the v0.4.0 approval
// policy: base role matrix, materiality ladder raises, assurance floors, tax
// role explicitness, check order and the exact policy version. Because there is
// NO public principal constructor, every principal fixture is minted through
// auth.Resolver.Authenticate with a minimal fake session store — the same path
// production middleware uses.
package authz_test

import (
	"context"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// fixedSessionStore ignores the token hash and returns the configured
// session/membership (or the configured error) — enough to mint fixtures.
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

// LookupMembershipByScope satisfies the auth.SessionStore contract. These
// approval-policy tests never exercise the OIDC path, so the stub returns the
// configured membership for any (subject, tenant, company) tuple.
func (f *fixedSessionStore) LookupMembershipByScope(context.Context, string, string, string) (auth.MembershipRecord, error) {
	if f.lookupErr != nil {
		return auth.MembershipRecord{}, f.lookupErr
	}
	return f.membership, nil
}

// fixtureStore builds a session store that mints a principal with the given
// membership attributes.
func fixtureStore(tenantID, companyID string, roles []auth.AccountingRole, assurance auth.AssuranceLevel) auth.SessionStore {
	return &fixedSessionStore{
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

func mustPrincipal(t *testing.T, store auth.SessionStore) auth.VerifiedApprovalPrincipal {
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

// memory builds a company-scoped memory with the given fiscal effect and
// materiality level.
func memory(effect core.FiscalEffect, level *core.MaterialityLevel) core.AccountingMemory {
	return core.AccountingMemory{
		Identity: core.Identity{ID: "mem-1", TopicKey: "dk/acme/202601/adj-1"},
		Scope: core.Scope{
			Kind:           core.ScopeKindCompany,
			OrganizationID: "tenant-1",
			CompanyID:      "acme",
			RUC:            "20100039201",
			Period:         "202601",
		},
		Status:           core.StatusPendingReview,
		FiscalEffect:     effect,
		MaterialityLevel: level,
	}
}

func level(l core.MaterialityLevel) *core.MaterialityLevel { return &l }

func controllerPrincipal(t *testing.T) auth.VerifiedApprovalPrincipal {
	return mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
}

func TestControllerApprovesClosing(t *testing.T) {
	decision := authz.NewApprovalPolicy().Authorize(controllerPrincipal(t), memory(core.FiscalEffectClosing, nil))
	if !decision.Allowed {
		t.Fatalf("controller must approve closing, got %+v", decision)
	}
	if decision.ReasonCode != authz.ReasonAuthorized {
		t.Errorf("allowed ReasonCode = %q, want AUTHORIZED", decision.ReasonCode)
	}
	if decision.PolicyVersion != authz.PolicyVersion {
		t.Errorf("PolicyVersion = %q, want %q", decision.PolicyVersion, authz.PolicyVersion)
	}
}

func TestPolicyVersionConstantIsExact(t *testing.T) {
	if authz.PolicyVersion != "approval-policy/v0.4.0" {
		t.Errorf("PolicyVersion = %q, want approval-policy/v0.4.0", authz.PolicyVersion)
	}
	// Denied decisions must also carry the exact policy version.
	principal := mustPrincipal(t, fixtureStore("tenant-2", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := authz.NewApprovalPolicy().Authorize(principal, memory(core.FiscalEffectClosing, nil))
	if decision.PolicyVersion != "approval-policy/v0.4.0" {
		t.Errorf("denied PolicyVersion = %q, want approval-policy/v0.4.0", decision.PolicyVersion)
	}
}

func TestAccountantDeniedClosing(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
	decision := authz.NewApprovalPolicy().Authorize(principal, memory(core.FiscalEffectClosing, nil))
	if decision.Allowed {
		t.Fatal("accountant must NOT approve closing")
	}
	if decision.ReasonCode != auth.CodeRoleNotAuthorized {
		t.Errorf("ReasonCode = %q, want ROLE_NOT_AUTHORIZED", decision.ReasonCode)
	}
}

func TestAccountantApprovesJournalEntry(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
	decision := authz.NewApprovalPolicy().Authorize(principal, memory(core.FiscalEffectJournalEntry, nil))
	if !decision.Allowed {
		t.Fatalf("accountant must approve journal_entry, got %+v", decision)
	}
}

func TestSeniorAccountantApprovesApprovalEffect(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleSeniorAccountant}, auth.AssuranceStandard))
	decision := authz.NewApprovalPolicy().Authorize(principal, memory(core.FiscalEffectApproval, nil))
	if !decision.Allowed {
		t.Fatalf("senior accountant must approve approval effect, got %+v", decision)
	}
	// An accountant does not satisfy the senior requirement.
	accountant := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
	if decision := authz.NewApprovalPolicy().Authorize(accountant, memory(core.FiscalEffectApproval, nil)); decision.Allowed {
		t.Fatal("accountant must NOT approve the approval effect")
	}
}

func TestMaterialityRaisesTheAccountingLadder(t *testing.T) {
	policy := authz.NewApprovalPolicy()
	accountant := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
	senior := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleSeniorAccountant}, auth.AssuranceStandard))

	// accountant + material adjustment → MATERIALITY_LIMIT_EXCEEDED.
	decision := policy.Authorize(accountant, memory(core.FiscalEffectAdjustment, level(core.MaterialityMaterial)))
	if decision.Allowed {
		t.Fatal("accountant must NOT approve a material adjustment")
	}
	if decision.ReasonCode != auth.CodeMaterialityLimitExceeded {
		t.Errorf("ReasonCode = %q, want MATERIALITY_LIMIT_EXCEEDED", decision.ReasonCode)
	}

	// senior accountant + material adjustment → allowed.
	if decision := policy.Authorize(senior, memory(core.FiscalEffectAdjustment, level(core.MaterialityMaterial))); !decision.Allowed {
		t.Fatalf("senior accountant must approve a material adjustment, got %+v", decision)
	}

	// Normal/NULL stays at base: accountant still approves a normal adjustment.
	if decision := policy.Authorize(accountant, memory(core.FiscalEffectAdjustment, level(core.MaterialityNormal))); !decision.Allowed {
		t.Fatalf("accountant must approve a normal adjustment, got %+v", decision)
	}
}

func TestCriticalAdjustmentRequiresController(t *testing.T) {
	policy := authz.NewApprovalPolicy()
	senior := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleSeniorAccountant}, auth.AssuranceStandard))
	controller := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))

	decision := policy.Authorize(senior, memory(core.FiscalEffectAdjustment, level(core.MaterialityCritical)))
	if decision.Allowed {
		t.Fatal("senior accountant must NOT approve a critical adjustment")
	}
	if decision.ReasonCode != auth.CodeMaterialityLimitExceeded {
		t.Errorf("ReasonCode = %q, want MATERIALITY_LIMIT_EXCEEDED", decision.ReasonCode)
	}

	if decision := policy.Authorize(controller, memory(core.FiscalEffectAdjustment, level(core.MaterialityCritical))); !decision.Allowed {
		t.Fatalf("controller must approve a critical adjustment, got %+v", decision)
	}
}

func TestCrossTenantPrincipalDenied(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-2", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := authz.NewApprovalPolicy().Authorize(principal, memory(core.FiscalEffectClosing, nil))
	if decision.Allowed {
		t.Fatal("cross-tenant principal must be denied")
	}
	if decision.ReasonCode != auth.CodeTenantScopeMismatch {
		t.Errorf("ReasonCode = %q, want TENANT_SCOPE_MISMATCH", decision.ReasonCode)
	}
}

func TestCompanyOutOfScopeDenied(t *testing.T) {
	principal := mustPrincipal(t, fixtureStore("tenant-1", "other-company", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	decision := authz.NewApprovalPolicy().Authorize(principal, memory(core.FiscalEffectClosing, nil))
	if decision.Allowed {
		t.Fatal("company out of scope must be denied")
	}
	if decision.ReasonCode != auth.CodeCompanyScopeDenied {
		t.Errorf("ReasonCode = %q, want COMPANY_SCOPE_DENIED", decision.ReasonCode)
	}
}

func TestInactiveMembershipDenied(t *testing.T) {
	// A membership record without an id is not a real active membership: the
	// resolver derives a principal with an EMPTY membership id that passes the
	// tenant/company checks and fails the membership check (defense in depth).
	store := fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard)
	fixed := store.(*fixedSessionStore)
	fixed.membership.ID = ""

	principal := mustPrincipal(t, store)
	if principal.MembershipID() != "" {
		t.Fatalf("fixture must carry an empty membership id, got %q", principal.MembershipID())
	}
	decision := authz.NewApprovalPolicy().Authorize(principal, memory(core.FiscalEffectClosing, nil))
	if decision.Allowed {
		t.Fatal("principal without active membership must be denied")
	}
	if decision.ReasonCode != auth.CodeMembershipInactive {
		t.Errorf("ReasonCode = %q, want MEMBERSHIP_INACTIVE", decision.ReasonCode)
	}
}

func TestInstitutionalMemoryFailsCompanyScope(t *testing.T) {
	principal := controllerPrincipal(t)
	m := memory(core.FiscalEffectClosing, nil)
	m.Scope = core.Scope{Kind: core.ScopeKindInstitutional}
	m.Scope.OrganizationID = "tenant-1"
	decision := authz.NewApprovalPolicy().Authorize(principal, m)
	if decision.Allowed {
		t.Fatal("institutional memory must not be approvable by a company principal")
	}
	if decision.ReasonCode != auth.CodeCompanyScopeDenied {
		t.Errorf("ReasonCode = %q, want COMPANY_SCOPE_DENIED", decision.ReasonCode)
	}
}

func TestSunatFilingRequiresStrongAssurance(t *testing.T) {
	policy := authz.NewApprovalPolicy()
	base := memory(core.FiscalEffectSunatFiling, nil)

	standard := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAuthorizedTaxProfessional}, auth.AssuranceStandard))
	decision := policy.Authorize(standard, base)
	if decision.Allowed {
		t.Fatal("sunat_filing with standard assurance must be denied")
	}
	if decision.ReasonCode != auth.CodeAssuranceTooLow {
		t.Errorf("ReasonCode = %q, want ASSURANCE_TOO_LOW", decision.ReasonCode)
	}

	strong := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAuthorizedTaxProfessional}, auth.AssuranceStrong))
	if decision := policy.Authorize(strong, base); !decision.Allowed {
		t.Fatalf("sunat_filing with strong assurance must be allowed, got %+v", decision)
	}
}

func TestTaxRolesAreExplicitAndNotImpliedByController(t *testing.T) {
	policy := authz.NewApprovalPolicy()
	controller := controllerPrincipal(t)

	// A controller does NOT imply tax_reviewer for declarations.
	decision := policy.Authorize(controller, memory(core.FiscalEffectDeclaration, nil))
	if decision.Allowed {
		t.Fatal("controller must NOT approve a declaration (tax_reviewer is explicit)")
	}
	if decision.ReasonCode != auth.CodeRoleNotAuthorized {
		t.Errorf("ReasonCode = %q, want ROLE_NOT_AUTHORIZED", decision.ReasonCode)
	}

	// A controller does NOT imply authorized_tax_professional for filings.
	if decision := policy.Authorize(controller, memory(core.FiscalEffectSunatFiling, nil)); decision.Allowed {
		t.Fatal("controller must NOT approve a sunat_filing")
	}

	// A tax_reviewer does NOT imply any accounting ladder role.
	taxReviewer := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleTaxReviewer}, auth.AssuranceStandard))
	if decision := policy.Authorize(taxReviewer, memory(core.FiscalEffectClosing, nil)); decision.Allowed {
		t.Fatal("tax_reviewer must NOT approve a closing")
	}

	// The exact tax role does authorize its own effect.
	if decision := policy.Authorize(taxReviewer, memory(core.FiscalEffectDeclaration, nil)); !decision.Allowed {
		t.Fatalf("tax_reviewer must approve a declaration, got %+v", decision)
	}
}

func TestEffectWithoutFiscalEffectCannotBeApproved(t *testing.T) {
	principal := controllerPrincipal(t)
	decision := authz.NewApprovalPolicy().Authorize(principal, memory(core.FiscalEffectNone, nil))
	if decision.Allowed {
		t.Fatal("a memory without a fiscal effect cannot be approved under policy")
	}
	if decision.ReasonCode != auth.CodeRoleNotAuthorized {
		t.Errorf("ReasonCode = %q, want ROLE_NOT_AUTHORIZED", decision.ReasonCode)
	}
}

func TestCheckOrderReturnsFirstFrozenCode(t *testing.T) {
	policy := authz.NewApprovalPolicy()
	// Cross-tenant + wrong role + low assurance: tenant wins (first check).
	foreign := mustPrincipal(t, fixtureStore("tenant-9", "other", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceLow))
	decision := policy.Authorize(foreign, memory(core.FiscalEffectClosing, nil))
	if decision.ReasonCode != auth.CodeTenantScopeMismatch {
		t.Errorf("ReasonCode = %q, want TENANT_SCOPE_MISMATCH (check order)", decision.ReasonCode)
	}
	// Same tenant, wrong company + wrong role: company wins.
	wrongCompany := mustPrincipal(t, fixtureStore("tenant-1", "other", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceLow))
	if decision := policy.Authorize(wrongCompany, memory(core.FiscalEffectClosing, nil)); decision.ReasonCode != auth.CodeCompanyScopeDenied {
		t.Errorf("ReasonCode = %q, want COMPANY_SCOPE_DENIED (check order)", decision.ReasonCode)
	}
}
