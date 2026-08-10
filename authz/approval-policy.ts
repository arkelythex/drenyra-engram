/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Versioned approval authorization policy (v0.4.0 Step 1, ADR-003) — the exact
 * TypeScript mirror of `internal/authz/approval_policy.go`. PURE: no database,
 * clock, token, or identity-provider access; it authorizes from the
 * pre-verified principal only.
 *
 * Frozen v0.4.0 matrix and check order (first reason code wins, for
 * reproducibility): tenant → company scope → membership active → role →
 * assurance → materiality.
 */

import type {
	AccountingMemory,
	AccountingRole,
	ApprovalAuthorizationDecision,
	ApprovalErrorCode,
	AssuranceLevel,
	FiscalEffect,
	MaterialityLevel,
	VerifiedApprovalPrincipal,
} from "../core/types.js";
import { ApprovalError } from "../core/types.js";

/** The frozen policy version, stamped on every decision. */
export const APPROVAL_POLICY_VERSION = "approval-policy/v0.4.0";

/** Reason code of an allowed decision (schema: authorization_reason_code='AUTHORIZED'). */
export const REASON_AUTHORIZED = "AUTHORIZED";

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

const ASSURANCE_RANK: Record<AssuranceLevel, number> = {
	low: 0,
	standard: 1,
	strong: 2,
};

function denied(reasonCode: ApprovalErrorCode): ApprovalAuthorizationDecision {
	return {
		allowed: false,
		policyVersion: APPROVAL_POLICY_VERSION,
		reasonCode,
	};
}

/**
 * Base role by fiscal effect. Effects without a base role (none, or unknown)
 * return undefined — a memory without a fiscal effect cannot be approved.
 */
export function baseRoleForEffect(
	effect: FiscalEffect,
): AccountingRole | undefined {
	switch (effect) {
		case "journal_entry":
		case "adjustment":
		case "reclassification":
			return "accountant";
		case "approval":
			return "senior_accountant";
		case "closing":
			return "controller";
		case "declaration":
			return "tax_reviewer";
		case "sunat_filing":
			return "authorized_tax_professional";
		default:
			return undefined;
	}
}

/**
 * Raises the base role for the declared materiality level. Applies ONLY to
 * effects whose base role is accountant or senior_accountant; normal/NULL
 * stays at base; tax bases never raise.
 */
function requiredRole(
	base: AccountingRole,
	level: MaterialityLevel | undefined,
): AccountingRole {
	if (
		level === "critical" &&
		(base === "accountant" || base === "senior_accountant")
	) {
		return "controller";
	}
	if (level === "material" && base === "accountant") {
		return "senior_accountant";
	}
	return base;
}

/**
 * Role dominance exists ONLY within the accounting ladder; tax roles are
 * matched explicitly — a controller NEVER implies tax_reviewer or
 * authorized_tax_professional, and vice versa.
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

/**
 * The pure v0.4.0 authorization decision for a pre-verified principal and a
 * memory. Returns the FIRST frozen reason code in the fixed check order.
 */
export function authorizeApproval(
	principal: VerifiedApprovalPrincipal,
	memory: AccountingMemory,
): ApprovalAuthorizationDecision {
	// 1. Tenant scope: the principal's tenant must equal the memory's tenant.
	if (memory.scope.kind === "institutional") {
		// An institutional scope carries no organizationId (Go: "").
		if (principal.tenantId !== "") {
			return denied("TENANT_SCOPE_MISMATCH");
		}
		return denied("COMPANY_SCOPE_DENIED");
	}
	if (principal.tenantId !== memory.scope.organizationId) {
		return denied("TENANT_SCOPE_MISMATCH");
	}
	// 2. Company scope: the memory's company must be inside the membership's
	//    company scopes.
	if (!principal.companyScopes.includes(memory.scope.companyId)) {
		return denied("COMPANY_SCOPE_DENIED");
	}
	// 3. Membership active: a pre-verified principal always carries the
	//    membership id of the ACTIVE membership it was derived from; empty
	//    means no active membership.
	if (principal.membershipId === "") {
		return denied("MEMBERSHIP_INACTIVE");
	}
	// 4. Role: the principal must satisfy the BASE role for the effect.
	const base = baseRoleForEffect(memory.fiscalEffect);
	if (base === undefined || !satisfies(principal.roles, base)) {
		return denied("ROLE_NOT_AUTHORIZED");
	}
	// 5. Assurance: minimum standard; sunat_filing additionally requires strong.
	const minAssurance: AssuranceLevel =
		memory.fiscalEffect === "sunat_filing" ? "strong" : "standard";
	if (ASSURANCE_RANK[principal.assuranceLevel] < ASSURANCE_RANK[minAssurance]) {
		return denied("ASSURANCE_TOO_LOW");
	}
	// 6. Materiality: the declared level may raise the requirement above the
	//    principal's reach → MATERIALITY_LIMIT_EXCEEDED.
	const raised = requiredRole(base, memory.materialityLevel);
	if (raised !== base && !satisfies(principal.roles, raised)) {
		return denied("MATERIALITY_LIMIT_EXCEEDED");
	}
	return {
		allowed: true,
		policyVersion: APPROVAL_POLICY_VERSION,
		reasonCode: REASON_AUTHORIZED,
	};
}

// ──────────────────────────────────────────────
// v0.9.0 review-workspace clauses (design §5/§6) — pure, fail-closed
// ──────────────────────────────────────────────

/**
 * Reports whether the authenticated reviewer IS the pending revision's
 * proposer — the separation-of-duties clause (design §5/§6.5.5): the decision
 * is only legal when the principal's subject id DIFFERS from the pending
 * revision's recordedBy. A non-empty proposer that equals the reviewer is a
 * violation (fail-closed inside the transaction); an empty proposer (a source
 * without an actor id) never equals an authenticated reviewer. Mirrors
 * authz.SODViolation.
 */
export function sodViolation(
	proposerRecordedBy: string,
	reviewerSubjectId: string,
): boolean {
	return proposerRecordedBy !== "" && proposerRecordedBy === reviewerSubjectId;
}

/**
 * Reports whether the declared materiality level demands the two review
 * checks (design §5/§6): material and critical require evidenceInspected &&
 * ruleInspected; normal/NULL never do. Mirrors authz.ReviewChecksRequired.
 */
export function reviewChecksRequired(
	level: MaterialityLevel | undefined,
): boolean {
	return level === "material" || level === "critical";
}

/**
 * Fails closed on the anti-rubber-stamp clause: when the declared materiality
 * level demands the two review checks, BOTH must be true
 * (REVIEW_CHECKS_REQUIRED otherwise). When no check is demanded, the checks
 * are ignored (a normal approval never trips this clause). Mirrors
 * authz.ValidateReviewChecks.
 */
export function validateReviewChecks(
	level: MaterialityLevel | undefined,
	checks: { evidenceInspected: boolean; ruleInspected: boolean },
): void {
	if (
		reviewChecksRequired(level) &&
		!(checks.evidenceInspected && checks.ruleInspected)
	) {
		throw new ApprovalError(
			"REVIEW_CHECKS_REQUIRED",
			"material/critical approvals require both review checks (evidenceInspected and ruleInspected)",
		);
	}
}
