/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Judgment policy mirror tests (v0.4.0 Step 2) — the exact TS mirror of the Go
 * matrix in `internal/authz/judgment_policy_test.go`: minimum senior_accountant
 * (controller dominates, tax roles do not), minimum standard assurance, exact
 * tenant/company scope, active membership, check order and the frozen policy
 * version.
 */

import { describe, expect, it } from "vitest";

import type {
	AccountingJudgment,
	AccountingRole,
	AssuranceLevel,
	VerifiedApprovalPrincipal,
} from "../../core/types.js";
import { createVerifiedApprovalPrincipal } from "../../auth/principal.js";
import {
	authorizeJudgment,
	JUDGMENT_POLICY_VERSION,
	type JudgmentAuthorizationDecision,
} from "../judgment-policy.js";

function principal(opts: {
	roles: AccountingRole[];
	assurance?: AssuranceLevel;
	tenantId?: string;
	companyId?: string;
	membershipId?: string;
}): VerifiedApprovalPrincipal {
	return createVerifiedApprovalPrincipal({
		subjectId: "subject-1",
		tenantId: opts.tenantId ?? "tenant-1",
		membershipId: opts.membershipId ?? "membership-1",
		companyScopes: [opts.companyId ?? "acme"],
		roles: opts.roles,
		authenticationMethod: "session",
		assuranceLevel: opts.assurance ?? "standard",
		authenticatedAt: "2026-08-05T12:00:00Z",
	});
}

function judgment(overrides?: Partial<AccountingJudgment>): AccountingJudgment {
	return {
		id: "judgment-1",
		tenantId: "tenant-1",
		companyId: "acme",
		fromId: "obs-1",
		toId: "obs-2",
		relation: "contradicts",
		status: "proposed",
		proposer: { system: "sire", actorId: "agent-7", actorKind: "agent" },
		proposalReason: "igv rate mismatch",
		proposedAt: "2026-08-05T12:00:00Z",
		updatedAt: "2026-08-05T12:00:00Z",
		...overrides,
	};
}

const controller = () => principal({ roles: ["controller"] });

function expectDenied(
	decision: JudgmentAuthorizationDecision,
	reasonCode: string,
): void {
	expect(decision.allowed).toBe(false);
	expect(decision.reasonCode).toBe(reasonCode);
	expect(decision.policyVersion).toBe(JUDGMENT_POLICY_VERSION);
}

describe("judgment-policy v0.4.0 mirror", () => {
	it("stamps the exact frozen policy version", () => {
		expect(JUDGMENT_POLICY_VERSION).toBe("judgment-policy/v0.4.0");
		const allowed = authorizeJudgment(controller(), judgment());
		const denied = authorizeJudgment(
			principal({ roles: ["accountant"] }),
			judgment(),
		);
		expect(allowed.policyVersion).toBe("judgment-policy/v0.4.0");
		expect(denied.policyVersion).toBe("judgment-policy/v0.4.0");
	});

	it("authorizes a controller confirming a judgment", () => {
		const decision = authorizeJudgment(controller(), judgment());
		expect(decision.allowed).toBe(true);
		expect(decision.reasonCode).toBe("AUTHORIZED");
		expect(decision.policyVersion).toBe(JUDGMENT_POLICY_VERSION);
	});

	it("authorizes a senior accountant (the minimum role)", () => {
		const decision = authorizeJudgment(
			principal({ roles: ["senior_accountant"] }),
			judgment(),
		);
		expect(decision.allowed).toBe(true);
	});

	it("denies an accountant (ROLE_NOT_AUTHORIZED)", () => {
		expectDenied(
			authorizeJudgment(principal({ roles: ["accountant"] }), judgment()),
			"ROLE_NOT_AUTHORIZED",
		);
	});

	it("denies tax roles even with strong assurance (ROLE_NOT_AUTHORIZED)", () => {
		expectDenied(
			authorizeJudgment(
				principal({ roles: ["tax_reviewer"], assurance: "strong" }),
				judgment(),
			),
			"ROLE_NOT_AUTHORIZED",
		);
		expectDenied(
			authorizeJudgment(
				principal({
					roles: ["authorized_tax_professional"],
					assurance: "strong",
				}),
				judgment(),
			),
			"ROLE_NOT_AUTHORIZED",
		);
	});

	it("denies a cross-tenant principal (TENANT_SCOPE_MISMATCH)", () => {
		expectDenied(
			authorizeJudgment(
				principal({ roles: ["controller"], tenantId: "tenant-2" }),
				judgment(),
			),
			"TENANT_SCOPE_MISMATCH",
		);
	});

	it("denies a company out of scope (COMPANY_SCOPE_DENIED)", () => {
		expectDenied(
			authorizeJudgment(
				principal({ roles: ["controller"], companyId: "other-company" }),
				judgment(),
			),
			"COMPANY_SCOPE_DENIED",
		);
	});

	it("denies a judgment without a company (COMPANY_SCOPE_DENIED)", () => {
		expectDenied(
			authorizeJudgment(controller(), judgment({ companyId: "" })),
			"COMPANY_SCOPE_DENIED",
		);
	});

	it("denies an inactive membership (MEMBERSHIP_INACTIVE)", () => {
		// The factory never mints a principal without a membership id, so this
		// exercises the policy's defensive branch directly.
		const noMembership = {
			subjectId: "subject-1",
			tenantId: "tenant-1",
			membershipId: "",
			companyScopes: ["acme"],
			roles: ["controller"],
			authenticationMethod: "session",
			assuranceLevel: "standard",
			authenticatedAt: "2026-08-05T12:00:00Z",
		} as VerifiedApprovalPrincipal;
		expectDenied(
			authorizeJudgment(noMembership, judgment()),
			"MEMBERSHIP_INACTIVE",
		);
	});

	it("denies low assurance (ASSURANCE_TOO_LOW)", () => {
		expectDenied(
			authorizeJudgment(
				principal({ roles: ["controller"], assurance: "low" }),
				judgment(),
			),
			"ASSURANCE_TOO_LOW",
		);
	});

	it("returns the first frozen code in check order", () => {
		// Cross-tenant + wrong company + weak role + low assurance: tenant wins.
		expectDenied(
			authorizeJudgment(
				principal({
					roles: ["accountant"],
					tenantId: "tenant-9",
					companyId: "other",
					assurance: "low",
				}),
				judgment(),
			),
			"TENANT_SCOPE_MISMATCH",
		);
		// Same tenant, wrong company: company wins over role/assurance.
		expectDenied(
			authorizeJudgment(
				principal({
					roles: ["accountant"],
					companyId: "other",
					assurance: "low",
				}),
				judgment(),
			),
			"COMPANY_SCOPE_DENIED",
		);
		// Same tenant/company, weak role + low assurance: role wins over assurance.
		expectDenied(
			authorizeJudgment(
				principal({ roles: ["accountant"], assurance: "low" }),
				judgment(),
			),
			"ROLE_NOT_AUTHORIZED",
		);
	});
});
