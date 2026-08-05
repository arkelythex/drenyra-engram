// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the versioned, PURE
// reconciliation authorization policy (v0.5.0 — adjudicated reconciliations;
// docs/architecture/close-intelligence-v0.5.md §3.2). Like the judgment
// policy, it works from the PRE-VERIFIED principal only — no database, clock,
// token, or identity-provider access. The reconciliation row records the
// provenance; this policy authorizes confirmation/rejection.
//
// Frozen reconciliation-policy/v0.5.0 matrix (design §3.2):
//   - Minimum role is controller (STRONGER than the judgment policy's
//     senior_accountant: a reconciliation decides a domain balance pair, not
//     just a relation); controller dominates through the accounting ladder; tax
//     roles do NOT (a controller never implies tax_reviewer/
//     authorized_tax_professional, and vice versa).
//   - Minimum assurance is standard.
//   - The reconciliation must be tenant-scoped to the principal's tenant and
//     its company must be inside the membership's companyScopes (a non-empty
//     company is required).
//   - Check order (first frozen code wins, for reproducibility): tenant →
//     company scope → membership active → role → assurance.
package authz

import (
	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ReconciliationPolicyVersion is the frozen version of the reconciliation
// policy. It is stamped on every decision and persisted with confirm/reject
// events and receipts.
const ReconciliationPolicyVersion = "reconciliation-policy/v0.5.0"

// ReconciliationAuthorizationDecision is the pure authorization outcome:
// allowed, the exact policy version, and the frozen reason code (AUTHORIZED
// when allowed).
type ReconciliationAuthorizationDecision struct {
	Allowed       bool
	PolicyVersion string
	ReasonCode    string
}

// ReconciliationAuthorizationPolicy is the pure authorization contract for
// confirming/rejecting a reconciliation. Implementors must not touch the
// database, the clock, tokens, or identity providers.
type ReconciliationAuthorizationPolicy interface {
	Authorize(principal auth.VerifiedApprovalPrincipal, reconciliation core.Reconciliation) ReconciliationAuthorizationDecision
}

// frozenReconciliationPolicy is the immutable
// reconciliation-policy/v0.5.0 policy.
type frozenReconciliationPolicy struct{}

// NewReconciliationPolicy returns the frozen reconciliation-policy/v0.5.0
// policy.
func NewReconciliationPolicy() ReconciliationAuthorizationPolicy { return frozenReconciliationPolicy{} }

// Authorize evaluates the pre-verified principal against the reconciliation's
// tenant, company, membership, role and assurance — in that exact order; the
// first frozen reason code wins so denials are reproducible.
func (frozenReconciliationPolicy) Authorize(p auth.VerifiedApprovalPrincipal, r core.Reconciliation) ReconciliationAuthorizationDecision {
	// 1. Tenant scope: the principal's tenant must equal the reconciliation's
	//    tenant.
	if p.TenantID() != r.TenantID {
		return reconciliationDenied(auth.CodeTenantScopeMismatch)
	}
	// 2. Company scope: the reconciliation's company must be inside the
	//    membership's company scopes. A reconciliation without a company ("" )
	//    fails closed.
	if r.CompanyID == "" || !contains(p.CompanyScopes(), r.CompanyID) {
		return reconciliationDenied(auth.CodeCompanyScopeDenied)
	}
	// 3. Membership active: a pre-verified principal always carries the
	//    membership id of the ACTIVE membership it was derived from; empty
	//    means no active membership.
	if p.MembershipID() == "" {
		return reconciliationDenied(auth.CodeMembershipInactive)
	}
	// 4. Role: the principal must satisfy controller (minimum). Only the
	//    accounting ladder dominates (controller ≥ senior_accountant ≥
	//    accountant); tax roles sit outside the ladder and never authorize
	//    adjudication.
	if !satisfies(p.Roles(), auth.RoleController) {
		return reconciliationDenied(auth.CodeRoleNotAuthorized)
	}
	// 5. Assurance: minimum standard.
	if assuranceRank(p.AssuranceLevel()) < assuranceRank(auth.AssuranceStandard) {
		return reconciliationDenied(auth.CodeAssuranceTooLow)
	}
	return ReconciliationAuthorizationDecision{Allowed: true, PolicyVersion: ReconciliationPolicyVersion, ReasonCode: ReasonAuthorized}
}

// reconciliationDenied returns a denied decision carrying the frozen policy
// version.
func reconciliationDenied(code string) ReconciliationAuthorizationDecision {
	return ReconciliationAuthorizationDecision{Allowed: false, PolicyVersion: ReconciliationPolicyVersion, ReasonCode: code}
}
