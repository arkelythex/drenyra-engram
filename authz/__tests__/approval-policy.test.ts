/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Approval policy mirror tests (v0.4.0 Step 1) — the exact TS mirror of the Go
 * matrix: base roles, materiality raises, assurance floors, tax role
 * explicitness, check order and the frozen policy version.
 */

import { describe, expect, it } from "vitest";

import type {
	AccountingMemory,
	AccountingRole,
	ApprovalAuthorizationDecision,
	AssuranceLevel,
	FiscalEffect,
	MaterialityLevel,
	VerifiedApprovalPrincipal,
} from "../../core/types.js";
import { createVerifiedApprovalPrincipal } from "../../auth/principal.js";
import {
	APPROVAL_POLICY_VERSION,
	authorizeApproval,
	REASON_AUTHORIZED,
} from "../approval-policy.js";

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

function memory(
	fiscalEffect: FiscalEffect,
	materialityLevel?: MaterialityLevel,
	scope?: Partial<AccountingMemory["scope"] & { kind: "company" }>,
): AccountingMemory {
	return {
		identity: { id: "mem-1", topicKey: "dk/acme/202601/adj-1" },
		title: "fixture",
		kind: "decision",
		scope: {
			kind: "company",
			organizationId: "tenant-1",
			companyId: "acme",
			ruc: "20100039201",
			period: "202601",
			...scope,
		},
		content: { what: "x", why: "y", where: "z", learned: "w" },
		status: "pending_review",
		fiscalEffect,
		effectiveAt: "2026-01-15T00:00:00Z",
		recordedAt: "2026-01-16T00:00:00Z",
		source: { system: "drenyra-core", actorKind: "agent" },
		contentHash: "hash",
		...(materialityLevel === undefined
			? {}
			: { materialityLevel }),
		revision: 1,
	};
}

const controller = () => principal({ roles: ["controller"] });
const accountant = () => principal({ roles: ["accountant"] });
const senior = () => principal({ roles: ["senior_accountant"] });
const taxPro = (assurance: AssuranceLevel = "strong") =>
	principal({ roles: ["authorized_tax_professional"], assurance });
const taxReviewer = () => principal({ roles: ["tax_reviewer"] });

function expectDenied(
	decision: ApprovalAuthorizationDecision,
	reasonCode: string,
): void {
	expect(decision.allowed).toBe(false);
	expect(decision.reasonCode).toBe(reasonCode);
	expect(decision.policyVersion).toBe(APPROVAL_POLICY_VERSION);
}

describe("approval-policy v0.4.0 mirror", () => {
	it("stamps the exact frozen policy version", () => {
		expect(APPROVAL_POLICY_VERSION).toBe("approval-policy/v0.4.0");
		const allowed = authorizeApproval(controller(), memory("closing"));
		const denied = authorizeApproval(accountant(), memory("closing"));
		expect(allowed.policyVersion).toBe("approval-policy/v0.4.0");
		expect(denied.policyVersion).toBe("approval-policy/v0.4.0");
	});

	it("authorizes a controller approving a closing", () => {
		const decision = authorizeApproval(controller(), memory("closing"));
		expect(decision.allowed).toBe(true);
		expect(decision.reasonCode).toBe(REASON_AUTHORIZED);
	});

	it("denies an accountant approving a closing (ROLE_NOT_AUTHORIZED)", () => {
		expectDenied(authorizeApproval(accountant(), memory("closing")), "ROLE_NOT_AUTHORIZED");
	});

	it("authorizes an accountant approving a journal_entry", () => {
		expect(authorizeApproval(accountant(), memory("journal_entry")).allowed).toBe(true);
	});

	it("authorizes a senior accountant approving the approval effect", () => {
		expect(authorizeApproval(senior(), memory("approval")).allowed).toBe(true);
		expectDenied(authorizeApproval(accountant(), memory("approval")), "ROLE_NOT_AUTHORIZED");
	});

	it("denies an accountant on a material adjustment but allows a senior accountant", () => {
		expectDenied(
			authorizeApproval(accountant(), memory("adjustment", "material")),
			"MATERIALITY_LIMIT_EXCEEDED",
		);
		expect(
			authorizeApproval(senior(), memory("adjustment", "material")).allowed,
		).toBe(true);
		// Normal stays at base: accountant still approves.
		expect(authorizeApproval(accountant(), memory("adjustment", "normal")).allowed).toBe(true);
	});

	it("requires a controller for a critical adjustment", () => {
		expectDenied(
			authorizeApproval(senior(), memory("adjustment", "critical")),
			"MATERIALITY_LIMIT_EXCEEDED",
		);
		expect(authorizeApproval(controller(), memory("adjustment", "critical")).allowed).toBe(
			true,
		);
	});

	it("denies a cross-tenant principal (TENANT_SCOPE_MISMATCH)", () => {
		expectDenied(
			authorizeApproval(
				principal({ roles: ["controller"], tenantId: "tenant-2" }),
				memory("closing"),
			),
			"TENANT_SCOPE_MISMATCH",
		);
	});

	it("denies a company out of scope (COMPANY_SCOPE_DENIED)", () => {
		expectDenied(
			authorizeApproval(
				principal({ roles: ["controller"], companyId: "other-company" }),
				memory("closing"),
			),
			"COMPANY_SCOPE_DENIED",
		);
	});

	it("denies an inactive membership (MEMBERSHIP_INACTIVE)", () => {
		// The factory never mints a principal without a membership id, so this
		// exercises the policy's defensive branch directly: a malformed
		// principal that passed tenant/company but carries no active
		// membership must fail closed.
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
			authorizeApproval(noMembership, memory("closing")),
			"MEMBERSHIP_INACTIVE",
		);
	});

	it("denies sunat_filing with standard assurance (ASSURANCE_TOO_LOW) and allows strong", () => {
		expectDenied(
			authorizeApproval(taxPro("standard"), memory("sunat_filing")),
			"ASSURANCE_TOO_LOW",
		);
		expect(authorizeApproval(taxPro("strong"), memory("sunat_filing")).allowed).toBe(true);
	});

	it("keeps tax roles explicit: a controller does not imply them", () => {
		expectDenied(
			authorizeApproval(controller(), memory("declaration")),
			"ROLE_NOT_AUTHORIZED",
		);
		expectDenied(
			authorizeApproval(controller(), memory("sunat_filing")),
			"ROLE_NOT_AUTHORIZED",
		);
		expectDenied(authorizeApproval(taxReviewer(), memory("closing")), "ROLE_NOT_AUTHORIZED");
		expect(authorizeApproval(taxReviewer(), memory("declaration")).allowed).toBe(true);
	});

	it("rejects a memory without a fiscal effect (ROLE_NOT_AUTHORIZED)", () => {
		expectDenied(authorizeApproval(controller(), memory("none")), "ROLE_NOT_AUTHORIZED");
	});

	it("returns the first frozen code in check order", () => {
		// Cross-tenant + wrong company + wrong role + low assurance: tenant wins.
		expectDenied(
			authorizeApproval(
				principal({
					roles: ["accountant"],
					tenantId: "tenant-9",
					companyId: "other",
				}),
				memory("closing"),
			),
			"TENANT_SCOPE_MISMATCH",
		);
		// Same tenant, wrong company: company wins over role.
		expectDenied(
			authorizeApproval(
				principal({ roles: ["accountant"], companyId: "other" }),
				memory("closing"),
			),
			"COMPANY_SCOPE_DENIED",
		);
	});
});
