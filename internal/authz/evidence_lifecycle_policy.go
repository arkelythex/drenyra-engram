// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the versioned, PURE evidence
// lifecycle authorization policy (v0.8.0, evidence-lifecycle-policy/v0.8.0,
// ADR-003) — the exact Go side of the approved design (§8,
// docs/architecture/evidence-lifecycle-v0.8.md). It is deterministic: no
// database, clock, token, or identity-provider access. The principal is a
// PRE-VERIFIED argument (the transport payload can never declare roles); the
// scope tuple, category and dual-approval configuration arrive pre-read from
// the immutable retention policy and the request aggregate.
//
// Frozen v0.8.0 matrix (design §8.1):
//   - request_purge: accounting ladder (accountant | senior_accountant |
//     controller) — the operational accountant roles may request.
//   - approve/reject (default approver): records_compliance_officer OR
//     tenant_records_owner — explicit match, never implied, never a second
//     approver.
//   - second approval (dual-approval categories): controller OR
//     tax_responsible — explicit match (ladder position never implies tax
//     roles and vice versa), on a DISTINCT principal from the first approver.
//   - withdraw/execute: a default approver or a dual second approver.
//   - place_hold/lift_hold (batch 3 object-level legal holds): a default
//     approver ONLY (records_compliance_officer | tenant_records_owner — the
//     hold acts are preservation acts, never the accounting ladder, never a
//     dual second approver). Emergency place/lift bypasses the closed-period
//     gate at the store layer because holds only preserve evidence (design §7).
//   - Deny-list precedes EVERY allow: operational_accountant, any role token
//     containing "admin", agent and system actor kinds are NEVER authorized
//     for any lifecycle act.
//
// Frozen check order (design §8.2; first reason code wins, for
// reproducibility): tenant → company scope → membership active → actor-kind
// deny (agent/system) → role deny-list (operational_accountant, *admin) →
// role allow → assurance ≥ standard → requester ≠ approver (SoD) →
// dual-approval config (category) → second approver distinct principal.
//
// Blocker checks (UNKNOWN_RETENTION_STATE, HOLD_ACTIVE, PERIOD_CLOSED, version
// drift) run BEFORE authorization at the store layer — a blocked request never
// reaches this policy, and no override field exists. The purge-transition
// storage, receipts and surfaces remain deferred (design §4–§7, §9–§13); the
// batch 3 object-level hold acts (place_hold/lift_hold) are consumed by the
// store layer (internal/store/hold_store.go) with NO blocker (holds only
// preserve evidence — no retention state, no closed-period gate).
package authz

import (
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// LifecyclePolicyVersion is the frozen version of the policy. It is stamped on
// every decision and will be persisted with lifecycle events/approvals.
const LifecyclePolicyVersion = "evidence-lifecycle-policy/v0.8.0"

// LifecycleAction is the lifecycle act being authorized. Tokens mirror the
// design's transition names (§2) and are frozen for reproducibility.
type LifecycleAction string

const (
	// LifecycleActionRequestPurge is the request_purge transition.
	LifecycleActionRequestPurge LifecycleAction = "request"
	// LifecycleActionApprovePurge is the first (default approver) approval.
	LifecycleActionApprovePurge LifecycleAction = "approve"
	// LifecycleActionSecondApprove is the dual-approval second approval.
	LifecycleActionSecondApprove LifecycleAction = "second_approve"
	// LifecycleActionRejectPurge is the reject transition.
	LifecycleActionRejectPurge LifecycleAction = "reject"
	// LifecycleActionWithdrawApproval is the withdraw transition.
	LifecycleActionWithdrawApproval LifecycleAction = "withdraw"
	// LifecycleActionExecutePurge is the execute transition (human executor).
	LifecycleActionExecutePurge LifecycleAction = "execute"
	// LifecycleActionPlaceHold is the object-level hold placement act (batch 3):
	// a preservation act authorized to the default approver only. It bypasses the
	// closed-period gate at the store layer (holds never reduce evidence
	// availability).
	LifecycleActionPlaceHold LifecycleAction = "place_hold"
	// LifecycleActionLiftHold is the object-level hold lift act (batch 3): the
	// one-way closure of a placed hold, same role matrix as place_hold.
	LifecycleActionLiftHold LifecycleAction = "lift_hold"
)

// LifecycleDecision is the pure authorization outcome: allowed, the exact
// policy version, and the frozen reason code.
type LifecycleDecision struct {
	Allowed       bool
	PolicyVersion string
	ReasonCode    string
}

// LifecycleAuthorizationRequest is the frozen v0.8.0 authorization context.
// Every field is pre-verified (ADR-003): Principal comes from
// auth.Resolver.Authenticate, ActorKind/scope/category/dual-approval
// configuration come from the store layer reading the immutable retention
// policy and the request aggregate. This policy has no access to any of those
// sources — it only decides.
type LifecycleAuthorizationRequest struct {
	// Action is the lifecycle act being authorized.
	Action LifecycleAction
	// Principal is the authenticated human (or agent/system claim) acting.
	Principal auth.VerifiedApprovalPrincipal
	// ActorKind is who the act claims to come from: human | agent | system.
	// Agents and systems are deny-listed before every other role check.
	ActorKind core.ActorKind
	// TenantID is the object's scope tenant (exact-match scope check).
	TenantID string
	// CompanyID is the object's scope company (must be inside the
	// membership's company scopes).
	CompanyID string
	// Category is the object/policy category (e.g. 'invoice', 'fiscal',
	// 'material'); the dual-approval rule is configured per category.
	Category string
	// DualApprovalRequired is the deployment configuration for the category:
	// when true, a second approval by controller/tax_responsible is required
	// before execution (design §8.1).
	DualApprovalRequired bool
	// Requester is the principal of the purge request. Approve/second_approve
	// enforce requester ≠ approver (SoD). Nil for request and other acts.
	Requester *auth.VerifiedApprovalPrincipal
	// FirstApprover is the principal of the first approval. The second
	// approval must be a DISTINCT principal. Only meaningful for
	// second_approve.
	FirstApprover *auth.VerifiedApprovalPrincipal
}

// EvidenceLifecyclePolicy is the pure authorization contract. Implementors
// must not touch the database, the clock, tokens, or identity providers.
type EvidenceLifecyclePolicy interface {
	Authorize(req LifecycleAuthorizationRequest) LifecycleDecision
}

// frozenLifecyclePolicy is the immutable v0.8.0 policy.
type frozenLifecyclePolicy struct{}

// NewEvidenceLifecyclePolicy returns the frozen v0.8.0 policy.
func NewEvidenceLifecyclePolicy() EvidenceLifecyclePolicy { return frozenLifecyclePolicy{} }

// Authorize evaluates the pre-verified request and returns the FIRST frozen
// reason code in the fixed check order so denials are reproducible. Denial
// precedes allow: the actor-kind and role deny-lists are evaluated before any
// role grant, assurance floor or SoD rule.
func (frozenLifecyclePolicy) Authorize(req LifecycleAuthorizationRequest) LifecycleDecision {
	p := req.Principal

	// 1. Tenant scope: the principal's tenant must equal the object's tenant.
	if p.TenantID() != req.TenantID {
		return lifecycleDenied(auth.CodeTenantScopeMismatch)
	}
	// 2. Company scope: the object's company must be inside the membership's
	//    company scopes. Institutional objects are not purgeable (the store
	//    layer fails them with NOT_PURGEABLE); a company principal acting on
	//    an out-of-scope company fails here.
	if !contains(p.CompanyScopes(), req.CompanyID) {
		return lifecycleDenied(auth.CodeCompanyScopeDenied)
	}
	// 3. Membership active: a pre-verified principal always carries the
	//    membership id of the ACTIVE membership it was derived from; empty
	//    means no active membership (the domain does not query the identity
	//    provider — this policy works from the pre-verified principal).
	if p.MembershipID() == "" {
		return lifecycleDenied(auth.CodeMembershipInactive)
	}
	// 4. Actor-kind deny: agents and systems never act under this policy —
	//    request, approve, reject, withdraw and execute are professional
	//    human acts (the deployment-configured scheduler path is a store-layer
	//    concern that never re-authorizes a human role).
	if req.ActorKind != core.ActorKindHuman {
		return lifecycleDenied(auth.CodeRoleDenied)
	}
	// 5. Role deny-list: operational_accountant and any role token containing
	//    "admin" are NEVER authorized — deny-list wins over every other check.
	if roleDenied(p.Roles()) {
		return lifecycleDenied(auth.CodeRoleDenied)
	}
	// 6. Role allow: the action's role matrix (design §8.1).
	if !lifecycleRoleAllowed(req.Action, p.Roles()) {
		return lifecycleDenied(auth.CodeRoleNotAuthorized)
	}
	// 7. Assurance: minimum standard for every lifecycle act.
	if assuranceRank(p.AssuranceLevel()) < assuranceRank(auth.AssuranceStandard) {
		return lifecycleDenied(auth.CodeAssuranceTooLow)
	}
	// 8. SoD: the approver can never be the requester of the purge request
	//    (independent professional acts).
	if req.Requester != nil && samePrincipal(p, *req.Requester) {
		return lifecycleDenied(auth.CodeApproverIsRequester)
	}
	// 9. Dual-approval configuration: a second approval is only meaningful for
	//    a category whose retention policy requires it.
	if req.Action == LifecycleActionSecondApprove && !req.DualApprovalRequired {
		return lifecycleDenied(auth.CodeDualApprovalRequired)
	}
	// 10. Second approver must be a DISTINCT principal from the first approver.
	if req.Action == LifecycleActionSecondApprove && req.FirstApprover != nil && samePrincipal(p, *req.FirstApprover) {
		return lifecycleDenied(auth.CodeSamePrincipalSecondApproval)
	}
	return LifecycleDecision{Allowed: true, PolicyVersion: LifecyclePolicyVersion, ReasonCode: ReasonAuthorized}
}

func lifecycleDenied(code string) LifecycleDecision {
	return LifecycleDecision{Allowed: false, PolicyVersion: LifecyclePolicyVersion, ReasonCode: code}
}

// lifecycleRoleAllowed maps an action to its role matrix (design §8.1). All
// matches are EXPLICIT except request, which uses the accounting ladder
// (accountant < senior_accountant < controller) — lifecycle and tax roles sit
// outside the ladder and can never request or dominate an approval role.
func lifecycleRoleAllowed(action LifecycleAction, roles []auth.AccountingRole) bool {
	switch action {
	case LifecycleActionRequestPurge:
		return satisfies(roles, auth.RoleAccountant)
	case LifecycleActionApprovePurge, LifecycleActionRejectPurge:
		return hasAnyRole(roles, auth.RoleRecordsComplianceOfficer, auth.RoleTenantRecordsOwner)
	case LifecycleActionSecondApprove:
		return hasAnyRole(roles, auth.RoleController, auth.RoleTaxResponsible)
	case LifecycleActionWithdrawApproval, LifecycleActionExecutePurge:
		return hasAnyRole(roles,
			auth.RoleRecordsComplianceOfficer, auth.RoleTenantRecordsOwner,
			auth.RoleController, auth.RoleTaxResponsible)
	case LifecycleActionPlaceHold, LifecycleActionLiftHold:
		// The v0.8 object-level hold acts (batch 3): a default approver ONLY —
		// records_compliance_officer | tenant_records_owner, explicit match. The
		// accounting ladder never places or lifts holds (holds are preservation
		// acts, not accounting operations) and a dual second approver never does
		// either (hold acts have no dual-approval configuration).
		return hasAnyRole(roles, auth.RoleRecordsComplianceOfficer, auth.RoleTenantRecordsOwner)
	default:
		return false
	}
}

// roleDenied reports whether the deny-list matches: the operational_accountant
// role or ANY role token containing "admin" (generic admin, deployment_admin,
// ...). The deny-list is evaluated before every allow and wins over it.
func roleDenied(roles []auth.AccountingRole) bool {
	for _, r := range roles {
		if r == auth.RoleOperationalAccountant || strings.Contains(string(r), "admin") {
			return true
		}
	}
	return false
}

// hasAnyRole reports whether the principal carries any of the required roles —
// explicit match, no dominance.
func hasAnyRole(roles []auth.AccountingRole, required ...auth.AccountingRole) bool {
	for _, r := range roles {
		for _, want := range required {
			if r == want {
				return true
			}
		}
	}
	return false
}

// samePrincipal reports whether two principals are the SAME human. Empty
// subject ids (defensive zero values) never prove sameness — the membership
// check already failed closed for such principals.
func samePrincipal(a, b auth.VerifiedApprovalPrincipal) bool {
	return a.SubjectID() != "" && a.SubjectID() == b.SubjectID()
}
