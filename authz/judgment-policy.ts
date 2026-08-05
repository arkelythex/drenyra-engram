/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Versioned judgment authorization policy (v0.4.0 Step 2) — the exact
 * TypeScript mirror of `internal/authz/judgment_policy.go`. PURE: no database,
 * clock, token, or identity-provider access; it authorizes from the
 * pre-verified principal only.
 *
 * Frozen judgment-policy/v0.4.0 matrix and check order (first reason code wins,
 * for reproducibility): tenant → company scope → membership active → role →
 * assurance. Minimum role senior_accountant (controller dominates it through
 * the accounting ladder; tax roles never do); minimum assurance standard; the
 * judgment's company must be inside the membership's companyScopes.
 */

import type {
	AccountingJudgment,
	AccountingRole,
	ApprovalErrorCode,
	AssuranceLevel,
	VerifiedApprovalPrincipal,
} from "../core/types.js";
import { REASON_AUTHORIZED } from "./approval-policy.js";

/** The frozen policy version, stamped on every decision. */
export const JUDGMENT_POLICY_VERSION = "judgment-policy/v0.4.0";

/** Pure authorization outcome: allowed, the exact policy version, reason code. */
export interface JudgmentAuthorizationDecision {
	allowed: boolean;
	/** Exactly `judgment-policy/v0.4.0`. */
	policyVersion: string;
	/** Frozen reason code (or `AUTHORIZED` when allowed). */
	reasonCode: string;
}

/**
 * Accounting ladder rank: accountant < senior_accountant < controller. Tax roles
 * sit OUTSIDE the ladder at rank 0 and can never dominate it. (Mirror of the
 * ladder in approval-policy.ts; duplicated here so this module is
 * self-contained and the Step 1 file stays untouched.)
 */
const LADDER_RANK: Record<AccountingRole, number> = {
	accountant: 1,
	senior_accountant: 2,
	controller: 3,
	tax_reviewer: 0,
	authorized_tax_professional: 0,
};

const ASSURANCE_RANK: Record<AssuranceLevel, number> = {
	low: 0,
	standard: 1,
	strong: 2,
};

function judgmentDenied(
	reasonCode: ApprovalErrorCode,
): JudgmentAuthorizationDecision {
	return {
		allowed: false,
		policyVersion: JUDGMENT_POLICY_VERSION,
		reasonCode,
	};
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
	if (required === "tax_reviewer" || required === "authorized_tax_professional") {
		return roles.includes(required);
	}
	const rr = LADDER_RANK[required];
	return roles.some((role) => LADDER_RANK[role] >= rr);
}

/**
 * The pure judgment-policy/v0.4.0 decision for a pre-verified principal and a
 * judgment. Returns the FIRST frozen reason code in the fixed check order.
 */
export function authorizeJudgment(
	principal: VerifiedApprovalPrincipal,
	judgment: AccountingJudgment,
): JudgmentAuthorizationDecision {
	// 1. Tenant scope: the principal's tenant must equal the judgment's tenant.
	if (principal.tenantId !== judgment.tenantId) {
		return judgmentDenied("TENANT_SCOPE_MISMATCH");
	}
	// 2. Company scope: the judgment's company must be inside the membership's
	//    company scopes; a judgment without a company fails closed.
	if (
		judgment.companyId.length === 0 ||
		!principal.companyScopes.includes(judgment.companyId)
	) {
		return judgmentDenied("COMPANY_SCOPE_DENIED");
	}
	// 3. Membership active: a pre-verified principal always carries the
	//    membership id of the ACTIVE membership it was derived from; empty
	//    means no active membership.
	if (principal.membershipId === "") {
		return judgmentDenied("MEMBERSHIP_INACTIVE");
	}
	// 4. Role: the principal must satisfy senior_accountant (minimum). Only the
	//    accounting ladder dominates; tax roles never authorize adjudication.
	if (!satisfies(principal.roles, "senior_accountant")) {
		return judgmentDenied("ROLE_NOT_AUTHORIZED");
	}
	// 5. Assurance: minimum standard.
	if (ASSURANCE_RANK[principal.assuranceLevel] < ASSURANCE_RANK["standard"]) {
		return judgmentDenied("ASSURANCE_TOO_LOW");
	}
	return {
		allowed: true,
		policyVersion: JUDGMENT_POLICY_VERSION,
		reasonCode: REASON_AUTHORIZED,
	};
}
