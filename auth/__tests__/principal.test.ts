/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Verified approval principal factory tests (v0.4.0 Step 1). The factory is the
 * ONLY principal construction path: it throws on invalid input and the snapshot
 * deliberately omits session/token material while canonicalizing roles.
 */

import { describe, expect, it } from "vitest";

import type {
	AccountingRole,
	PrincipalSnapshot,
	VerifiedApprovalPrincipal,
} from "../../core/types.js";
import {
	createVerifiedApprovalPrincipal,
	isVerifiedApprovalPrincipal,
	principalSnapshot,
} from "../principal.js";

function validPrincipal(): VerifiedApprovalPrincipal {
	return createVerifiedApprovalPrincipal({
		subjectId: "subject-1",
		tenantId: "tenant-1",
		membershipId: "membership-1",
		companyScopes: ["acme", "sire-sa"],
		roles: ["controller", "accountant", "accountant", "senior_accountant"],
		authenticationMethod: "session",
		assuranceLevel: "standard",
		authenticatedAt: "2026-08-05T12:00:00Z",
		sessionId: "session-secret-id",
	});
}

describe("createVerifiedApprovalPrincipal", () => {
	it("mints a branded, read-only principal from valid input", () => {
		const principal = validPrincipal();
		expect(isVerifiedApprovalPrincipal(principal)).toBe(true);
		expect(principal.subjectId).toBe("subject-1");
		expect(principal.tenantId).toBe("tenant-1");
		expect(principal.membershipId).toBe("membership-1");
		expect(principal.companyScopes).toEqual(["acme", "sire-sa"]);
		expect(principal.authenticationMethod).toBe("session");
		expect(principal.assuranceLevel).toBe("standard");
		expect(principal.sessionId).toBe("session-secret-id");
		expect(Object.isFrozen(principal)).toBe(true);
	});

	it("rejects a plain object literal as a principal", () => {
		expect(
			isVerifiedApprovalPrincipal({
				subjectId: "subject-1",
				tenantId: "tenant-1",
				membershipId: "membership-1",
				companyScopes: ["acme"],
				roles: ["accountant"],
				authenticationMethod: "session",
				assuranceLevel: "standard",
				authenticatedAt: "2026-08-05T12:00:00Z",
			} as unknown),
		).toBe(false);
	});

	it.each([
		["empty subjectId", { ...validPrincipal(), subjectId: "" }],
		["empty tenantId", { ...validPrincipal(), tenantId: "  " }],
		["empty membershipId", { ...validPrincipal(), membershipId: "" }],
		["empty companyScopes", { ...validPrincipal(), companyScopes: [] }],
		["unknown role", { ...validPrincipal(), roles: ["senior_manager"] }],
		["empty roles", { ...validPrincipal(), roles: [] }],
		["unknown authenticationMethod", { ...validPrincipal(), authenticationMethod: "magic" }],
		["unknown assuranceLevel", { ...validPrincipal(), assuranceLevel: "mega" }],
		["unparseable authenticatedAt", { ...validPrincipal(), authenticatedAt: "yesterday-ish" }],
		["empty authenticatedAt", { ...validPrincipal(), authenticatedAt: "" }],
		["empty sessionId", { ...validPrincipal(), sessionId: "" }],
	])("throws PRINCIPAL_INVALID for %s", (_name, input) => {
		expect(() =>
			createVerifiedApprovalPrincipal(input as Parameters<typeof createVerifiedApprovalPrincipal>[0]),
		).toThrow(/^PRINCIPAL_INVALID:/);
	});
});

describe("principalSnapshot", () => {
	it("omits sessionId and token material while exposing the identity view", () => {
		const snapshot: PrincipalSnapshot = principalSnapshot(validPrincipal());
		expect(snapshot).toEqual({
			subjectId: "subject-1",
			membershipId: "membership-1",
			// Canonical order is lexicographic (Go sort.Strings == TS default
			// sort), NOT ladder order — parity is what matters.
			roles: ["accountant", "controller", "senior_accountant"],
			authenticationMethod: "session",
			assuranceLevel: "standard",
			authenticatedAt: "2026-08-05T12:00:00Z",
		});
		// The snapshot shape has no session/token key at all.
		expect(Object.keys(snapshot)).not.toContain("sessionId");
		expect(Object.keys(snapshot)).not.toContain("token");
	});

	it("canonicalizes roles sorted and deduplicated (Go parity)", () => {
		const roles: AccountingRole[] = principalSnapshot(validPrincipal()).roles;
		expect(roles).toEqual(["accountant", "controller", "senior_accountant"]);
	});
});
