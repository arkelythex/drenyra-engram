// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the versioned, PURE approval
// authorization policy (v0.4.0 Step 1, ADR-003). It works from the
// PRE-VERIFIED principal only — no database, clock, token, or identity-provider
// access. The engine records the claim for provenance; this policy authorizes.
//
// Frozen v0.4.0 matrix:
//   - Base role by fiscal effect: journal_entry/adjustment/reclassification →
//     accountant; approval → senior_accountant; closing → controller;
//     declaration → tax_reviewer; sunat_filing → authorized_tax_professional.
//     Any other (or missing) effect cannot be approved under policy.
//   - Materiality raises the accounting ladder: material → senior_accountant,
//     critical → controller (only for effects whose base role is accountant or
//     senior_accountant; normal/NULL stays at base).
//   - Role dominance applies ONLY within the accounting ladder
//     accountant < senior_accountant < controller; tax roles are explicit and
//     never implied by a controller (and vice versa).
//   - Minimum assurance is standard; sunat_filing additionally requires strong.
//   - Check order (first frozen code wins, for reproducibility): tenant →
//     company scope → membership active → role → assurance → materiality.
package authz

import (
	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// PolicyVersion is the frozen version of the policy. It is stamped on every
// decision and persisted with approval events.
const PolicyVersion = "approval-policy/v0.4.0"

// ReasonAuthorized is the reason code of an allowed decision (the approval
// event schema records authorization_reason_code='AUTHORIZED').
const ReasonAuthorized = "AUTHORIZED"

// Decision is the pure authorization outcome: allowed, the exact policy
// version, and the frozen reason code.
type Decision struct {
	Allowed      bool
	PolicyVersion string
	ReasonCode   string
}

// ApprovalAuthorizationPolicy is the pure authorization contract. Implementors
// must not touch the database, the clock, tokens, or identity providers.
type ApprovalAuthorizationPolicy interface {
	Authorize(principal auth.VerifiedApprovalPrincipal, memory core.AccountingMemory) Decision
}

// frozenPolicy is the immutable v0.4.0 policy.
type frozenPolicy struct{}

// NewApprovalPolicy returns the frozen v0.4.0 policy.
func NewApprovalPolicy() ApprovalAuthorizationPolicy { return frozenPolicy{} }

// Authorize evaluates the pre-verified principal against the memory's fiscal
// effect, declared materiality level and scope. It returns the FIRST frozen
// reason code in the fixed check order so denials are reproducible.
func (frozenPolicy) Authorize(p auth.VerifiedApprovalPrincipal, m core.AccountingMemory) Decision {
	// 1. Tenant scope: the principal's tenant must equal the memory's tenant.
	if p.TenantID() != m.Scope.OrganizationID {
		return denied(auth.CodeTenantScopeMismatch)
	}
	// 2. Company scope: the memory's company must be inside the membership's
	//    company scopes. Institutional memories have no company to authorize.
	if m.Scope.Kind != core.ScopeKindCompany || !contains(p.CompanyScopes(), m.Scope.CompanyID) {
		return denied(auth.CodeCompanyScopeDenied)
	}
	// 3. Membership active: a pre-verified principal always carries the
	//    membership id of the ACTIVE membership it was derived from; empty
	//    means no active membership (the domain does not query the identity
	//    provider — this policy works from the pre-verified principal).
	if p.MembershipID() == "" {
		return denied(auth.CodeMembershipInactive)
	}
	// 4. Role: the principal must satisfy the BASE role for the effect.
	base, ok := baseRole(m.FiscalEffect)
	if !ok || !satisfies(p.Roles(), base) {
		return denied(auth.CodeRoleNotAuthorized)
	}
	// 5. Assurance: minimum standard; sunat_filing additionally requires strong.
	minAssurance := auth.AssuranceStandard
	if m.FiscalEffect == core.FiscalEffectSunatFiling {
		minAssurance = auth.AssuranceStrong
	}
	if assuranceRank(p.AssuranceLevel()) < assuranceRank(minAssurance) {
		return denied(auth.CodeAssuranceTooLow)
	}
	// 6. Materiality: the declared level may raise the requirement above the
	//    principal's reach → MATERIALITY_LIMIT_EXCEEDED.
	raised := requiredRole(base, m.MaterialityLevel)
	if raised != base && !satisfies(p.Roles(), raised) {
		return denied(auth.CodeMaterialityLimitExceeded)
	}
	return Decision{Allowed: true, PolicyVersion: PolicyVersion, ReasonCode: ReasonAuthorized}
}

func denied(code string) Decision {
	return Decision{Allowed: false, PolicyVersion: PolicyVersion, ReasonCode: code}
}

// baseRole maps a fiscal effect to its base accounting role. Effects without a
// base role (none, or unknown) return ok=false — a memory without a fiscal
// effect cannot be approved under policy.
func baseRole(e core.FiscalEffect) (auth.AccountingRole, bool) {
	switch e {
	case core.FiscalEffectJournalEntry, core.FiscalEffectAdjustment, core.FiscalEffectReclassification:
		return auth.RoleAccountant, true
	case core.FiscalEffectApproval:
		return auth.RoleSeniorAccountant, true
	case core.FiscalEffectClosing:
		return auth.RoleController, true
	case core.FiscalEffectDeclaration:
		return auth.RoleTaxReviewer, true
	case core.FiscalEffectSunatFiling:
		return auth.RoleAuthorizedTaxProfessional, true
	default:
		return "", false
	}
}

// requiredRole raises the base role for the declared materiality level. The
// raise applies ONLY to effects whose base role is accountant or
// senior_accountant; normal/NULL stays at base; tax bases never raise.
func requiredRole(base auth.AccountingRole, level *core.MaterialityLevel) auth.AccountingRole {
	if level != nil {
		switch *level {
		case core.MaterialityCritical:
			if base == auth.RoleAccountant || base == auth.RoleSeniorAccountant {
				return auth.RoleController
			}
		case core.MaterialityMaterial:
			if base == auth.RoleAccountant {
				return auth.RoleSeniorAccountant
			}
		}
	}
	return base
}

// satisfies reports whether any principal role dominates the required role.
// Dominance exists ONLY within the accounting ladder
// (accountant < senior_accountant < controller); tax roles are matched
// explicitly — a controller NEVER implies tax_reviewer or
// authorized_tax_professional, and vice versa.
func satisfies(roles []auth.AccountingRole, required auth.AccountingRole) bool {
	if required == auth.RoleTaxReviewer || required == auth.RoleAuthorizedTaxProfessional {
		for _, r := range roles {
			if r == required {
				return true
			}
		}
		return false
	}
	rr := ladderRank(required)
	for _, r := range roles {
		if ladderRank(r) >= rr {
			return true
		}
	}
	return false
}

// ladderRank maps a role to its accounting-ladder rank. Tax roles (and any
// unknown role) sit OUTSIDE the ladder at rank 0 and can never dominate.
func ladderRank(r auth.AccountingRole) int {
	switch r {
	case auth.RoleAccountant:
		return 1
	case auth.RoleSeniorAccountant:
		return 2
	case auth.RoleController:
		return 3
	default:
		return 0
	}
}

// assuranceRank orders assurance levels: low < standard < strong. Unknown
// levels rank below everything and fail the minimum-assurance check.
func assuranceRank(l auth.AssuranceLevel) int {
	switch l {
	case auth.AssuranceLow:
		return 0
	case auth.AssuranceStandard:
		return 1
	case auth.AssuranceStrong:
		return 2
	default:
		return -1
	}
}

func contains(scopes []string, target string) bool {
	for _, s := range scopes {
		if s == target {
			return true
		}
	}
	return false
}
