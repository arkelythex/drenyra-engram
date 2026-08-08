/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Versioned evidence-lifecycle authorization policy (v0.8.0) — the exact
 * TypeScript mirror of `internal/authz/evidence_lifecycle_policy.go` (approved
 * design §8, docs/architecture/evidence-lifecycle-v0.8.md). PURE: no database,
 * clock, token, or identity-provider access; it authorizes from the
 * pre-verified principal only. The scope tuple, category and dual-approval
 * configuration arrive pre-read from the immutable retention policy and the
 * request aggregate (ADR-003: the transport payload can never declare roles).
 *
 * Frozen v0.8.0 matrix (design §8.1):
 *   - request: accounting ladder (accountant | senior_accountant | controller).
 *   - approve/reject (default approver): records_compliance_officer OR
 *     tenant_records_owner — explicit match, never implied, never a second
 *     approver.
 *   - second_approve (dual-approval categories): controller OR tax_responsible —
 *     explicit match (ladder position never implies tax roles and vice versa),
 *     on a DISTINCT principal from the first approver.
 *   - withdraw/execute: a default approver or a dual second approver.
 *   - Deny-list precedes EVERY allow: operational_accountant, any role token
 *     containing "admin", agent and system actor kinds are NEVER authorized for
 *     any lifecycle act.
 *
 * Frozen check order (design §8.2; first reason code wins): tenant → company
 * scope → membership active → actor-kind deny (agent/system) → role deny-list
 * (operational_accountant, *admin) → role allow → assurance ≥ standard →
 * requester ≠ approver (SoD) → dual-approval config (category) → second
 * approver distinct principal.
 */

import type {
	ActorKind,
	AccountingRole,
	ApprovalErrorCode,
	AssuranceLevel,
	VerifiedApprovalPrincipal,
} from "../core/types.js";
import { REASON_AUTHORIZED } from "./approval-policy.js";

/** The frozen policy version, stamped on every decision. */
export const EVIDENCE_LIFECYCLE_POLICY_VERSION =
	"evidence-lifecycle-policy/v0.8.0";

/**
 * The lifecycle act being authorized. Tokens mirror the design's transition
 * names (§2) and are frozen for reproducibility.
 */
export type LifecycleAction =
	| "request"
	| "approve"
	| "second_approve"
	| "reject"
	| "withdraw"
	| "execute"
	// v0.8 object-level legal holds (batch 3): the preservation acts,
	// authorized to the default approver only (records_compliance_officer |
	// tenant_records_owner), never the accounting ladder, never a dual second
	// approver; emergency place/lift bypasses the closed-period gate at the
	// store layer because holds only preserve evidence (design §7).
	| "place_hold"
	| "lift_hold";

/** Pure authorization outcome: allowed, the exact policy version, reason code. */
export interface LifecycleAuthorizationDecision {
	allowed: boolean;
	/** Exactly `evidence-lifecycle-policy/v0.8.0`. */
	policyVersion: string;
	/** Frozen reason code (or `AUTHORIZED` when allowed). */
	reasonCode: string;
}

/**
 * The frozen v0.8.0 authorization context. Every field is pre-verified
 * (ADR-003): `principal` comes from `createVerifiedApprovalPrincipal`,
 * actorKind/scope/category/dual-approval configuration come from the store
 * layer reading the immutable retention policy and the request aggregate. This
 * policy only decides; it never reads any of those sources.
 */
export interface LifecycleAuthorizationRequest {
	/** The lifecycle act being authorized. */
	action: LifecycleAction;
	/** The authenticated human (or agent/system claim) acting. */
	principal: VerifiedApprovalPrincipal;
	/** Who the act claims to come from: human | agent | system. */
	actorKind: ActorKind;
	/** The object's scope tenant (exact-match scope check). */
	tenantId: string;
	/** The object's scope company (inside the membership's companyScopes). */
	companyId: string;
	/** Object/policy category (e.g. 'invoice', 'fiscal', 'material'). */
	category: string;
	/** Deployment config: dual approval required for this category (§8.1). */
	dualApprovalRequired: boolean;
	/** Principal of the purge request (SoD: requester ≠ approver). */
	requester?: VerifiedApprovalPrincipal;
	/** Principal of the first approval (distinct second approver). */
	firstApprover?: VerifiedApprovalPrincipal;
}

const ASSURANCE_RANK: Record<AssuranceLevel, number> = {
	low: 0,
	standard: 1,
	strong: 2,
};

function lifecycleDenied(
	reasonCode: ApprovalErrorCode,
): LifecycleAuthorizationDecision {
	return {
		allowed: false,
		policyVersion: EVIDENCE_LIFECYCLE_POLICY_VERSION,
		reasonCode,
	};
}

/**
 * Role dominance exists ONLY within the accounting ladder (used for the
 * request act); lifecycle and tax roles sit outside at rank 0 and can never
 * dominate an approval role.
 */
function satisfies(
	roles: readonly AccountingRole[],
	required: AccountingRole,
): boolean {
	if (
		required === "tax_reviewer" ||
		required === "authorized_tax_professional"
	) {
		return roles.includes(required);
	}
	const rr = LADDER_RANK[required];
	return roles.some((role) => LADDER_RANK[role] >= rr);
}

/** Accounting ladder rank: accountant < senior_accountant < controller. */
const LADDER_RANK: Record<AccountingRole, number> = {
	accountant: 1,
	senior_accountant: 2,
	controller: 3,
	// Tax roles sit OUTSIDE the ladder at rank 0 and can never dominate it.
	tax_reviewer: 0,
	authorized_tax_professional: 0,
	// v0.8.0 evidence-lifecycle roles also sit OUTSIDE the ladder at rank 0
	// (explicit-match only, exactly like tax roles; design §8.1).
	records_compliance_officer: 0,
	tenant_records_owner: 0,
	tax_responsible: 0,
	operational_accountant: 0,
};

/**
 * Maps an action to its role matrix (design §8.1). All matches are EXPLICIT
 * except request, which uses the accounting ladder — lifecycle and tax roles
 * can never request or dominate an approval role.
 */
function roleAllowed(
	action: LifecycleAction,
	roles: readonly AccountingRole[],
): boolean {
	switch (action) {
		case "request":
			return satisfies(roles, "accountant");
		case "approve":
		case "reject":
			return hasAnyRole(roles, "records_compliance_officer", "tenant_records_owner");
		case "second_approve":
			return hasAnyRole(roles, "controller", "tax_responsible");
		case "withdraw":
		case "execute":
			return hasAnyRole(
				roles,
				"records_compliance_officer",
				"tenant_records_owner",
				"controller",
				"tax_responsible",
			);
		// v0.8 object-level hold acts (batch 3): a default approver ONLY —
		// explicit match, never the accounting ladder, never a dual second
		// approver (hold acts have no dual-approval configuration).
		case "place_hold":
		case "lift_hold":
			return hasAnyRole(roles, "records_compliance_officer", "tenant_records_owner");
		default:
			return false;
	}
}

/**
 * Deny-list match: the operational_accountant role or ANY role token containing
 * "admin" (generic admin, deployment_admin, ...). Evaluated before every allow
 * and wins over it.
 */
function roleDenied(roles: readonly AccountingRole[]): boolean {
	return roles.some(
		(role) =>
			role === "operational_accountant" || role.includes("admin"),
	);
}

/** Explicit role match — no dominance. */
function hasAnyRole(
	roles: readonly AccountingRole[],
	...required: AccountingRole[]
): boolean {
	return roles.some((role) => required.includes(role));
}

/** Same human? Empty subject ids never prove sameness (fail closed upstream). */
function samePrincipal(
	a: VerifiedApprovalPrincipal,
	b: VerifiedApprovalPrincipal,
): boolean {
	return a.subjectId !== "" && a.subjectId === b.subjectId;
}

/**
 * The pure v0.8.0 evidence-lifecycle authorization decision. Returns the FIRST
 * frozen reason code in the fixed check order so denials are reproducible.
 * Denial precedes allow: the actor-kind and role deny-lists are evaluated
 * before any role grant, assurance floor or SoD rule.
 */
export function authorizeLifecycleAction(
	req: LifecycleAuthorizationRequest,
): LifecycleAuthorizationDecision {
	const { principal, actorKind, tenantId, companyId } = req;

	// 1. Tenant scope: the principal's tenant must equal the object's tenant.
	if (principal.tenantId !== tenantId) {
		return lifecycleDenied("TENANT_SCOPE_MISMATCH");
	}
	// 2. Company scope: the object's company must be inside the membership's
	//    company scopes.
	if (!principal.companyScopes.includes(companyId)) {
		return lifecycleDenied("COMPANY_SCOPE_DENIED");
	}
	// 3. Membership active: a pre-verified principal always carries the
	//    membership id of the ACTIVE membership it was derived from; empty
	//    means no active membership.
	if (principal.membershipId === "") {
		return lifecycleDenied("MEMBERSHIP_INACTIVE");
	}
	// 4. Actor-kind deny: agents and systems never act under this policy.
	if (actorKind !== "human") {
		return lifecycleDenied("ROLE_DENIED");
	}
	// 5. Role deny-list: operational_accountant and any *admin role are NEVER
	//    authorized — the deny-list wins over every other check.
	if (roleDenied(principal.roles)) {
		return lifecycleDenied("ROLE_DENIED");
	}
	// 6. Role allow: the action's role matrix (design §8.1).
	if (!roleAllowed(req.action, principal.roles)) {
		return lifecycleDenied("ROLE_NOT_AUTHORIZED");
	}
	// 7. Assurance: minimum standard for every lifecycle act.
	if (ASSURANCE_RANK[principal.assuranceLevel] < ASSURANCE_RANK["standard"]) {
		return lifecycleDenied("ASSURANCE_TOO_LOW");
	}
	// 8. SoD: the approver can never be the requester of the purge request.
	if (req.requester !== undefined && samePrincipal(principal, req.requester)) {
		return lifecycleDenied("APPROVER_IS_REQUESTER");
	}
	// 9. Dual-approval configuration: a second approval is only meaningful for
	//    a category whose retention policy requires it.
	if (req.action === "second_approve" && !req.dualApprovalRequired) {
		return lifecycleDenied("DUAL_APPROVAL_REQUIRED");
	}
	// 10. Second approver must be a DISTINCT principal from the first approver.
	if (
		req.action === "second_approve" &&
		req.firstApprover !== undefined &&
		samePrincipal(principal, req.firstApprover)
	) {
		return lifecycleDenied("SAME_PRINCIPAL_SECOND_APPROVAL");
	}
	return {
		allowed: true,
		policyVersion: EVIDENCE_LIFECYCLE_POLICY_VERSION,
		reasonCode: REASON_AUTHORIZED,
	};
}
