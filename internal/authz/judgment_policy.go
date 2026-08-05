// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the versioned, PURE judgment
// authorization policy (v0.4.0 Step 2 — adjudicable conflicts). Like the
// approval policy, it works from the PRE-VERIFIED principal only — no database,
// clock, token, or identity-provider access. The judgment row records the
// provenance; this policy authorizes confirmation/rejection.
//
// Frozen judgment-policy/v0.4.0 matrix (design §6):
//   - Minimum role is senior_accountant; controller dominates it through the
//     accounting ladder; tax roles do NOT (a controller never implies
//     tax_reviewer/authorized_tax_professional, and vice versa).
//   - Minimum assurance is standard.
//   - The judgment must be tenant-scoped to the principal's tenant and its
//     company must be inside the membership's companyScopes (a non-empty
//     company is required).
//   - Check order (first frozen code wins, for reproducibility): tenant →
//     company scope → membership active → role → assurance.
package authz

import (
	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// JudgmentPolicyVersion is the frozen version of the judgment policy. It is
// stamped on every decision and persisted with confirm/reject events.
const JudgmentPolicyVersion = "judgment-policy/v0.4.0"

// JudgmentAuthorizationDecision is the pure authorization outcome: allowed, the
// exact policy version, and the frozen reason code (AUTHORIZED when allowed).
type JudgmentAuthorizationDecision struct {
	Allowed       bool
	PolicyVersion string
	ReasonCode    string
}

// JudgmentAuthorizationPolicy is the pure authorization contract for
// confirming/rejecting a judgment. Implementors must not touch the database,
// the clock, tokens, or identity providers.
type JudgmentAuthorizationPolicy interface {
	Authorize(principal auth.VerifiedApprovalPrincipal, judgment core.AccountingJudgment) JudgmentAuthorizationDecision
}

// frozenJudgmentPolicy is the immutable judgment-policy/v0.4.0 policy.
type frozenJudgmentPolicy struct{}

// NewJudgmentPolicy returns the frozen judgment-policy/v0.4.0 policy.
func NewJudgmentPolicy() JudgmentAuthorizationPolicy { return frozenJudgmentPolicy{} }

// Authorize evaluates the pre-verified principal against the judgment's tenant,
// company, membership, role and assurance — in that exact order; the first
// frozen reason code wins so denials are reproducible.
func (frozenJudgmentPolicy) Authorize(p auth.VerifiedApprovalPrincipal, j core.AccountingJudgment) JudgmentAuthorizationDecision {
	// 1. Tenant scope: the principal's tenant must equal the judgment's tenant.
	if p.TenantID() != j.TenantID {
		return judgmentDenied(auth.CodeTenantScopeMismatch)
	}
	// 2. Company scope: the judgment's company must be inside the membership's
	//    company scopes. A judgment without a company ("" ) fails closed.
	if j.CompanyID == "" || !contains(p.CompanyScopes(), j.CompanyID) {
		return judgmentDenied(auth.CodeCompanyScopeDenied)
	}
	// 3. Membership active: a pre-verified principal always carries the
	//    membership id of the ACTIVE membership it was derived from; empty
	//    means no active membership.
	if p.MembershipID() == "" {
		return judgmentDenied(auth.CodeMembershipInactive)
	}
	// 4. Role: the principal must satisfy senior_accountant (minimum). Only the
	//    accounting ladder dominates (controller ≥ senior_accountant); tax
	//    roles sit outside the ladder and never authorize adjudication.
	if !satisfies(p.Roles(), auth.RoleSeniorAccountant) {
		return judgmentDenied(auth.CodeRoleNotAuthorized)
	}
	// 5. Assurance: minimum standard.
	if assuranceRank(p.AssuranceLevel()) < assuranceRank(auth.AssuranceStandard) {
		return judgmentDenied(auth.CodeAssuranceTooLow)
	}
	return JudgmentAuthorizationDecision{Allowed: true, PolicyVersion: JudgmentPolicyVersion, ReasonCode: ReasonAuthorized}
}

// judgmentDenied returns a denied decision carrying the frozen policy version.
func judgmentDenied(code string) JudgmentAuthorizationDecision {
	return JudgmentAuthorizationDecision{Allowed: false, PolicyVersion: JudgmentPolicyVersion, ReasonCode: code}
}
