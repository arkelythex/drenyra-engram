/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Evidence-lifecycle policy mirror tests (v0.8.0) — the exact TS mirror of the
 * Go matrix in `internal/authz/evidence_lifecycle_policy_test.go`: role
 * matrices per act, the deny-list precedence (operational_accountant, *admin,
 * agent, system), the SoD rules (approver ≠ requester, second approver ≠ first
 * approver), the dual-approval configuration gate, cross-tenant/company and
 * principal authentication failures (inactive membership, low assurance), and
 * the frozen check order — denial precedes allow.
 */

import { describe, expect, it } from "vitest";

import type {
	ActorKind,
	AccountingRole,
	AssuranceLevel,
	VerifiedApprovalPrincipal,
} from "../../core/types.js";
import { createVerifiedApprovalPrincipal } from "../../auth/principal.js";
import {
	authorizeLifecycleAction,
	EVIDENCE_LIFECYCLE_POLICY_VERSION,
	type LifecycleAuthorizationRequest,
} from "../evidence-lifecycle-policy.js";
import { REASON_AUTHORIZED } from "../approval-policy.js";

function principal(opts: {
	roles: AccountingRole[];
	assurance?: AssuranceLevel;
	tenantId?: string;
	companyId?: string;
	membershipId?: string;
	subjectId?: string;
}): VerifiedApprovalPrincipal {
	return createVerifiedApprovalPrincipal({
		subjectId: opts.subjectId ?? "subject-1",
		tenantId: opts.tenantId ?? "tenant-1",
		membershipId: opts.membershipId ?? "membership-1",
		companyScopes: [opts.companyId ?? "acme"],
		roles: opts.roles,
		authenticationMethod: "session",
		assuranceLevel: opts.assurance ?? "standard",
		authenticatedAt: "2026-08-05T12:00:00Z",
	});
}

/** A principal that could only exist through a transport bug (see tests). */
function malformedPrincipal(
	overrides: Partial<VerifiedApprovalPrincipal>,
): VerifiedApprovalPrincipal {
	return {
		subjectId: "subject-1",
		tenantId: "tenant-1",
		membershipId: "membership-1",
		companyScopes: ["acme"],
		roles: ["controller"],
		authenticationMethod: "session",
		assuranceLevel: "standard",
		authenticatedAt: "2026-08-05T12:00:00Z",
		...overrides,
	} as VerifiedApprovalPrincipal;
}

const REQUEST: Pick<
	LifecycleAuthorizationRequest,
	"tenantId" | "companyId" | "category"
> = { tenantId: "tenant-1", companyId: "acme", category: "invoice" };

function req(
	action: LifecycleAuthorizationRequest["action"],
	p: VerifiedApprovalPrincipal,
	opts?: Partial<LifecycleAuthorizationRequest>,
): LifecycleAuthorizationRequest {
	return {
		action,
		principal: p,
		actorKind: "human",
		...REQUEST,
		dualApprovalRequired: false,
		...opts,
	};
}

function expectAllowed(decision: {
	allowed: boolean;
	policyVersion: string;
	reasonCode: string;
}): void {
	expect(decision.allowed).toBe(true);
	expect(decision.reasonCode).toBe(REASON_AUTHORIZED);
	expect(decision.policyVersion).toBe(EVIDENCE_LIFECYCLE_POLICY_VERSION);
}

function expectDenied(
	decision: { allowed: boolean; policyVersion: string; reasonCode: string },
	reasonCode: string,
): void {
	expect(decision.allowed).toBe(false);
	expect(decision.reasonCode).toBe(reasonCode);
	expect(decision.policyVersion).toBe(EVIDENCE_LIFECYCLE_POLICY_VERSION);
}

describe("evidence-lifecycle-policy v0.8.0 mirror", () => {
	it("stamps the exact frozen policy version", () => {
		expect(EVIDENCE_LIFECYCLE_POLICY_VERSION).toBe(
			"evidence-lifecycle-policy/v0.8.0",
		);
		const allowed = authorizeLifecycleAction(
			req("approve", principal({ roles: ["records_compliance_officer"] })),
		);
		const denied = authorizeLifecycleAction(
			req("request", principal({ roles: ["records_compliance_officer"] })),
		);
		expect(allowed.policyVersion).toBe("evidence-lifecycle-policy/v0.8.0");
		expect(denied.policyVersion).toBe("evidence-lifecycle-policy/v0.8.0");
	});

	it("authorizes the accounting ladder to request purge", () => {
		for (const roles of [
			["accountant"],
			["senior_accountant"],
			["controller"],
		] as const) {
			expectAllowed(
				authorizeLifecycleAction(
					req("request", principal({ roles: [...roles] })),
				),
			);
		}
	});

	it("denies non-ladder roles the request act (ROLE_NOT_AUTHORIZED)", () => {
		for (const roles of [
			["records_compliance_officer"],
			["tenant_records_owner"],
			["tax_responsible"],
			["tax_reviewer"],
		] as const) {
			expectDenied(
				authorizeLifecycleAction(
					req("request", principal({ roles: [...roles] })),
				),
				"ROLE_NOT_AUTHORIZED",
			);
		}
	});

	it("deny-list: operational_accountant and admin tokens never request", () => {
		expectDenied(
			authorizeLifecycleAction(
				req("request", principal({ roles: ["operational_accountant"] })),
			),
			"ROLE_DENIED",
		);
		// An admin role token could only arrive through a transport or
		// provisioning bug; the deny-list pattern still catches it.
		expectDenied(
			authorizeLifecycleAction(
				req(
					"request",
					malformedPrincipal({ roles: ["generic_admin" as AccountingRole] }),
				),
			),
			"ROLE_DENIED",
		);
		// Deny-list wins even when a request-capable role rides along.
		expectDenied(
			authorizeLifecycleAction(
				req(
					"request",
					principal({ roles: ["controller", "operational_accountant"] }),
				),
			),
			"ROLE_DENIED",
		);
	});

	it("denies agent and system actor kinds at every gate (ROLE_DENIED)", () => {
		const actions = [
			"request",
			"approve",
			"second_approve",
			"reject",
			"withdraw",
			"execute",
		] as const;
		for (const actorKind of ["agent", "system"] as const) {
			for (const action of actions) {
				const decision = authorizeLifecycleAction(
					req(action, principal({ roles: ["records_compliance_officer"] }), {
						actorKind,
					}),
				);
				expectDenied(decision, "ROLE_DENIED");
			}
		}
	});

	it("authorizes only the default approvers for approve", () => {
		expectAllowed(
			authorizeLifecycleAction(
				req("approve", principal({ roles: ["records_compliance_officer"] })),
			),
		);
		expectAllowed(
			authorizeLifecycleAction(
				req("approve", principal({ roles: ["tenant_records_owner"] })),
			),
		);
	});

	it("denies non-default approvers the approve act", () => {
		for (const roles of [
			["controller"],
			["tax_responsible"],
			["senior_accountant"],
			["accountant"],
		] as const) {
			expectDenied(
				authorizeLifecycleAction(
					req("approve", principal({ roles: [...roles] })),
				),
				"ROLE_NOT_AUTHORIZED",
			);
		}
		expectDenied(
			authorizeLifecycleAction(
				req("approve", principal({ roles: ["operational_accountant"] })),
			),
			"ROLE_DENIED",
		);
	});

	it("enforces SoD: the approver cannot be the requester", () => {
		const approver = principal({ roles: ["records_compliance_officer"] });
		expectDenied(
			authorizeLifecycleAction(
				req("approve", approver, { requester: approver }),
			),
			"APPROVER_IS_REQUESTER",
		);
	});

	it("allows a distinct requester", () => {
		const approver = principal({ roles: ["records_compliance_officer"] });
		const requester = principal({
			roles: ["accountant"],
			subjectId: "subject-2",
		});
		expectAllowed(
			authorizeLifecycleAction(req("approve", approver, { requester })),
		);
	});

	it("authorizes controller and tax_responsible as second approvers on dual categories", () => {
		const first = principal({
			roles: ["records_compliance_officer"],
			subjectId: "subject-2",
		});
		for (const roles of [["controller"], ["tax_responsible"]] as const) {
			expectAllowed(
				authorizeLifecycleAction(
					req("second_approve", principal({ roles: [...roles] }), {
						dualApprovalRequired: true,
						firstApprover: first,
					}),
				),
			);
		}
	});

	it("denies non-second roles the second approval (no ladder dominance)", () => {
		const first = principal({
			roles: ["records_compliance_officer"],
			subjectId: "subject-2",
		});
		for (const roles of [
			["records_compliance_officer"],
			["tenant_records_owner"],
			["senior_accountant"],
			["accountant"],
		] as const) {
			expectDenied(
				authorizeLifecycleAction(
					req("second_approve", principal({ roles: [...roles] }), {
						dualApprovalRequired: true,
						firstApprover: first,
					}),
				),
				"ROLE_NOT_AUTHORIZED",
			);
		}
		expectDenied(
			authorizeLifecycleAction(
				req("second_approve", principal({ roles: ["operational_accountant"] }), {
					dualApprovalRequired: true,
					firstApprover: first,
				}),
			),
			"ROLE_DENIED",
		);
	});

	it("requires a dual-approval-configured category for second approval", () => {
		const second = principal({ roles: ["controller"] });
		expectDenied(
			authorizeLifecycleAction(req("second_approve", second)),
			"DUAL_APPROVAL_REQUIRED",
		);
		expectAllowed(
			authorizeLifecycleAction(
				req("second_approve", second, { dualApprovalRequired: true }),
			),
		);
	});

	it("requires a distinct principal for the second approval", () => {
		const same = principal({
			roles: ["records_compliance_officer", "controller"],
		});
		expectDenied(
			authorizeLifecycleAction(
				req("second_approve", same, {
					dualApprovalRequired: true,
					firstApprover: same,
				}),
			),
			"SAME_PRINCIPAL_SECOND_APPROVAL",
		);
		// Distinct controller second-approving a records officer passes.
		const second = principal({ roles: ["controller"], subjectId: "subject-2" });
		const first = principal({ roles: ["records_compliance_officer"] });
		expectAllowed(
			authorizeLifecycleAction(
				req("second_approve", second, {
					dualApprovalRequired: true,
					firstApprover: first,
				}),
			),
		);
	});

	it("authorizes reject/withdraw/execute for approval-authority roles only", () => {
		expectAllowed(
			authorizeLifecycleAction(
				req("reject", principal({ roles: ["records_compliance_officer"] })),
			),
		);
		expectDenied(
			authorizeLifecycleAction(
				req("reject", principal({ roles: ["controller"] })),
			),
			"ROLE_NOT_AUTHORIZED",
		);
		expectAllowed(
			authorizeLifecycleAction(
				req("withdraw", principal({ roles: ["tenant_records_owner"] })),
			),
		);
		expectAllowed(
			authorizeLifecycleAction(
				req("withdraw", principal({ roles: ["tax_responsible"] })),
			),
		);
		expectAllowed(
			authorizeLifecycleAction(
				req("execute", principal({ roles: ["records_compliance_officer"] })),
			),
		);
		expectAllowed(
			authorizeLifecycleAction(
				req("execute", principal({ roles: ["controller"] })),
			),
		);
		expectDenied(
			authorizeLifecycleAction(
				req("execute", principal({ roles: ["accountant"] })),
			),
			"ROLE_NOT_AUTHORIZED",
		);
	});

	it("denies a cross-tenant principal (TENANT_SCOPE_MISMATCH)", () => {
		expectDenied(
			authorizeLifecycleAction(
				req(
					"approve",
					principal({ roles: ["records_compliance_officer"], tenantId: "tenant-2" }),
				),
			),
			"TENANT_SCOPE_MISMATCH",
		);
	});

	it("denies a company out of scope (COMPANY_SCOPE_DENIED)", () => {
		expectDenied(
			authorizeLifecycleAction(
				req(
					"approve",
					principal({ roles: ["records_compliance_officer"], companyId: "other-company" }),
				),
			),
			"COMPANY_SCOPE_DENIED",
		);
	});

	it("denies an inactive membership (MEMBERSHIP_INACTIVE)", () => {
		// The factory never mints a principal without a membership id, so this
		// exercises the policy's defensive branch directly: a malformed
		// principal that passed tenant/company but carries no active
		// membership must fail closed.
		const noMembership = malformedPrincipal({ membershipId: "" });
		expectDenied(
			authorizeLifecycleAction(req("approve", noMembership)),
			"MEMBERSHIP_INACTIVE",
		);
	});

	it("denies low assurance (ASSURANCE_TOO_LOW)", () => {
		expectDenied(
			authorizeLifecycleAction(
				req(
					"approve",
					principal({ roles: ["records_compliance_officer"], assurance: "low" }),
				),
			),
			"ASSURANCE_TOO_LOW",
		);
		expectDenied(
			authorizeLifecycleAction(
				req("request", principal({ roles: ["accountant"], assurance: "low" })),
			),
			"ASSURANCE_TOO_LOW",
		);
	});

	it("returns the first frozen code in check order (denial precedes allow)", () => {
		// 1 over 5: cross-tenant + operational_accountant → TENANT_SCOPE_MISMATCH.
		expectDenied(
			authorizeLifecycleAction(
				req(
					"request",
					principal({
						roles: ["operational_accountant"],
						tenantId: "tenant-2",
					}),
				),
			),
			"TENANT_SCOPE_MISMATCH",
		);
		// 5 over 6/7: deny-list beats role allow and assurance.
		expectDenied(
			authorizeLifecycleAction(
				req(
					"request",
					principal({ roles: ["operational_accountant"], assurance: "low" }),
				),
			),
			"ROLE_DENIED",
		);
		// 4 over 5: an agent carrying a deny-listed role is denied by actor-kind.
		expectDenied(
			authorizeLifecycleAction(
				req("approve", principal({ roles: ["operational_accountant"] }), {
					actorKind: "agent" as ActorKind,
				}),
			),
			"ROLE_DENIED",
		);
		// 6 over 7: wrong role + low assurance → ROLE_NOT_AUTHORIZED.
		expectDenied(
			authorizeLifecycleAction(
				req(
					"approve",
					principal({ roles: ["accountant"], assurance: "low" }),
				),
			),
			"ROLE_NOT_AUTHORIZED",
		);
		// 7 over 8: low assurance + requester==approver → ASSURANCE_TOO_LOW.
		const low = principal({
			roles: ["records_compliance_officer"],
			assurance: "low",
		});
		expectDenied(
			authorizeLifecycleAction(req("approve", low, { requester: low })),
			"ASSURANCE_TOO_LOW",
		);
		// 8 over 9/10: approver==requester beats dual config and distinctness.
		const second = principal({ roles: ["controller"] });
		const requester = principal({ roles: ["accountant"] }); // subject-1
		const first = principal({
			roles: ["records_compliance_officer"],
			subjectId: "subject-2",
		});
		expectDenied(
			authorizeLifecycleAction(
				req("second_approve", second, {
					dualApprovalRequired: true,
					requester,
					firstApprover: first,
				}),
			),
			"APPROVER_IS_REQUESTER",
		);
		// 9 over 10: non-dual category + first==second → DUAL_APPROVAL_REQUIRED.
		expectDenied(
			authorizeLifecycleAction(
				req("second_approve", second, {
					requester: principal({
						roles: ["accountant"],
						subjectId: "subject-2",
					}),
					firstApprover: second,
				}),
			),
			"DUAL_APPROVAL_REQUIRED",
		);
		// 10: configured dual category + first==second → SAME_PRINCIPAL_SECOND_APPROVAL.
		expectDenied(
			authorizeLifecycleAction(
				req("second_approve", second, {
					dualApprovalRequired: true,
					requester: principal({
						roles: ["accountant"],
						subjectId: "subject-2",
					}),
					firstApprover: second,
				}),
			),
			"SAME_PRINCIPAL_SECOND_APPROVAL",
		);
	});

	it("denies an unknown action", () => {
		expectDenied(
			authorizeLifecycleAction(
				req(
					"cancel" as LifecycleAuthorizationRequest["action"],
					principal({ roles: ["records_compliance_officer"] }),
				),
			),
			"ROLE_NOT_AUTHORIZED",
		);
	});
});
