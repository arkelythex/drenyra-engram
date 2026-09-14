/**
 * Delivery Slice 4 (openspec/changes/fiscal-runtime-foundations) — the pure
 * "fiscal scope binding" verification layer (design.md "Verification and
 * audit"). Mirrors internal/core/verify_test.go's
 * TestVerifyFiscalScopeBinding* suite case-for-case for Go↔TS semantic
 * parity.
 *
 * Deliberately isolated from verify.test.ts: that file's top-level fixtures
 * call signReceipt/NodeSeedSigner, which crash with "Invalid JWK OKP key"
 * under this environment's Node version (a confirmed pre-existing,
 * unrelated failure — see apply-progress.md). These tests exercise only the
 * PURE fiscal classifier, which needs no signing key material at all.
 */
import { describe, expect, it } from "vitest";

import {
	AUTHORITY_LEVEL,
	canonicalFiscalScopeBytes,
	fiscalScopeHash,
	type FiscalScopeBinding,
} from "../fiscal-scope.js";
import type { ReviewAcknowledgement } from "../types.js";
import {
	LAYER_FISCAL_SCOPE_BINDING,
	type FiscalBindingLinkEvidence,
	computeActEvidenceHash,
	verifyFiscalScopeBinding,
} from "../verify.js";

const OMITTED: ReviewAcknowledgement = { present: false, value: false };

function baseBinding(): FiscalScopeBinding {
	return {
		version: "v1",
		tenant: "tenant-1",
		organization: "acme",
		company: "20100070970", // checksum-valid SUNAT RUC
		fiscalPeriod: "202401",
		ledgerBook: "purchases",
		operationType: "memory.save",
		sourceSnapshot: "a".repeat(64),
		policyVersion: "fiscal-v1",
		actor: "agent-1",
		authorityLevel: AUTHORITY_LEVEL.PREPARE,
	};
}

function link(
	sequence: number,
	binding: FiscalScopeBinding,
	resultingEnvelopeHash: string,
): FiscalBindingLinkEvidence {
	const bindingHash = fiscalScopeHash(binding);
	return {
		sequence,
		binding,
		bindingHash,
		canonicalBytes: canonicalFiscalScopeBytes(binding),
		actEvidenceHash: computeActEvidenceHash(bindingHash, "", OMITTED, OMITTED),
		reviewedEnvelopeHash: "",
		resultingEnvelopeHash,
		evidenceInspected: OMITTED,
		ruleInspected: OMITTED,
		auditAnchorResolved: true,
	};
}

describe("verifyFiscalScopeBinding", () => {
	it("no links: SKIPPED legacy/unbound", () => {
		const layer = verifyFiscalScopeBinding([], "envelope-hash");
		expect(layer.status).toBe("skipped");
		expect(layer.name).toBe(LAYER_FISCAL_SCOPE_BINDING);
		expect(layer.detail).toBe("legacy/unbound: no v1 fiscal binding evidence");
	});

	it("one complete, hash-consistent link with a matching envelope PASSES with an act count", () => {
		const l = link(1, baseBinding(), "envelope-hash-2");
		const layer = verifyFiscalScopeBinding([l], "envelope-hash-2");
		expect(layer.status).toBe("passed");
		expect(layer.detail).toContain("1 act(s)");
	});

	it("two sequential acts PASS with a deterministic act count of 2 (triangulation)", () => {
		const first = baseBinding();
		const second = { ...baseBinding(), actor: "agent-2" };
		const links = [
			link(1, first, "envelope-hash-2"),
			link(2, second, "envelope-hash-3"),
		];
		const layer = verifyFiscalScopeBinding(links, "envelope-hash-3");
		expect(layer.status).toBe("passed");
		expect(layer.detail).toContain("2 act(s)");
	});

	it("a sequence gap FAILS closed", () => {
		const l = { ...link(1, baseBinding(), "envelope-hash-2"), sequence: 2 };
		const layer = verifyFiscalScopeBinding([l], "envelope-hash-2");
		expect(layer.status).toBe("failed");
	});

	it("an invalid binding (checksum-invalid RUC) FAILS closed", () => {
		// bindingHash/canonicalBytes are pinned to a VALID base binding (fiscal-
		// scope.ts's fiscalScopeHash validates and would throw on construction
		// otherwise); only the evidence's declared `binding` field carries the
		// checksum-invalid RUC under test — exactly what a store-reloaded
		// mismatched row looks like to this pure layer.
		const l = {
			...link(1, baseBinding(), "envelope-hash-2"),
			binding: { ...baseBinding(), company: "11111111111" },
		};
		const layer = verifyFiscalScopeBinding([l], "envelope-hash-2");
		expect(layer.status).toBe("failed");
	});

	it("a binding-hash mismatch (tampering) FAILS closed", () => {
		const l = { ...link(1, baseBinding(), "envelope-hash-2"), bindingHash: "f".repeat(64) };
		const layer = verifyFiscalScopeBinding([l], "envelope-hash-2");
		expect(layer.status).toBe("failed");
	});

	it("an act-evidence-hash mismatch FAILS closed", () => {
		const l = { ...link(1, baseBinding(), "envelope-hash-2"), actEvidenceHash: "e".repeat(64) };
		const layer = verifyFiscalScopeBinding([l], "envelope-hash-2");
		expect(layer.status).toBe("failed");
	});

	it("an unresolved audit anchor FAILS closed", () => {
		const l = { ...link(1, baseBinding(), "envelope-hash-2"), auditAnchorResolved: false };
		const layer = verifyFiscalScopeBinding([l], "envelope-hash-2");
		expect(layer.status).toBe("failed");
	});

	it("an envelope mismatch FAILS closed", () => {
		const l = link(1, baseBinding(), "envelope-hash-2");
		const layer = verifyFiscalScopeBinding([l], "a-different-envelope-hash");
		expect(layer.status).toBe("failed");
	});

	it("never discloses a foreign tenant value on failure", () => {
		const l = {
			...link(1, baseBinding(), "envelope-hash-2"),
			binding: {
				...baseBinding(),
				tenant: "foreign-tenant-should-not-leak",
				company: "11111111111",
			},
		};
		const layer = verifyFiscalScopeBinding([l], "envelope-hash-2");
		expect(layer.status).toBe("failed");
		expect(layer.detail).not.toContain("foreign-tenant-should-not-leak");
	});
});
