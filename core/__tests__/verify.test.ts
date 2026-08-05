/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * OFFLINE verification mirror tests (v0.4.0 Step 4) — the exact TS counterpart
 * of `internal/core/verify_test.go`: the pure layer functions (payload
 * canonicalization, envelope integrity, signature and key timing, tenant/company
 * scope, chain links, principal provenance, supersession chains, evidence/rule
 * availability, judgment hashes), the aggregator, the stable layer names and the
 * mandatory conclusion.
 *
 * The fixtures reuse the receipt protocol's pinned parity seed (32×0x01), so
 * every asserted layer input is a REAL signed receipt. Every detail string
 * asserted here is part of the Go↔TS fixture contract (design §2 — names,
 * statuses and details must be byte-identical). The shared
 * testdata/verify-parity.json fixture additionally runs the FULL report builder
 * (per-receipt blocks + aggregated top-level layers + object layers + Finalize)
 * and pins the expected ordered results in both runtimes.
 */
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import {
	canonicalReceiptPayload,
	receiptHash,
	signReceipt,
} from "../receipt.js";
import {
	RECEIPT_ALGORITHM,
	type ReceiptPayload,
	type SignedReceipt,
} from "../types.js";
import {
	ACCOUNTING_CORRECTNESS_NOT_ASSERTED,
	LAYER_PRINCIPAL_PROVENANCE,
	aggregateLayers,
	decodeSigningPublicKey,
	decodeStoredPayload,
	finalize,
	newReport,
	receiptLayerNames,
	verifyChainLink,
	verifyEnvelopeIntegrity,
	verifyEvidenceAvailability,
	verifyJudgmentHash,
	verifyPayloadCanonicalization,
	verifyPrincipalProvenance,
	verifyRuleAvailability,
	verifySignature,
	verifySigningKeyValidity,
	verifySupersessionChain,
	verifyTenantCompanyScope,
	type ActProvenance,
	type SigningKey,
	type SubjectScope,
	type SupersessionLink,
	type VerificationLayer,
	type VerificationOutcome,
	type VerificationReport,
	type VerificationStatus,
} from "../verify.js";
import type { ReceiptVerification } from "../verify.js";

/** Fixed parity seed (AC9/AC10): 32 bytes of 0x01 (documented in the Go test). */
const PARITY_SEED_HEX =
	"0101010101010101010101010101010101010101010101010101010101010101";
const PARITY_SEED = Buffer.from(PARITY_SEED_HEX, "hex");

/**
 * The canonical fixture of the parity constants: a memory_approved payload
 * exercising the complete principal snapshot, empty inapplicable fields, a `&`
 * (no HTML escaping) and a `"` (JSON escaping) in the reason, and UNSORTED +
 * DUPLICATED roles (canonicalized by the payload hash contract). The identical
 * fixture lives in the Go test (baseReceiptPayload).
 */
function basePayload(overrides: Partial<ReceiptPayload> = {}): ReceiptPayload {
	return {
		version: "receipt-payload/v0.4.0",
		subjectType: "memory",
		subjectId: "memory-1",
		action: "memory_approved",
		tenantId: "tenant-1",
		companyId: "acme",
		fiscalPeriodId: "202601",
		reviewedEnvelopeHash: "h1-reviewed-envelope",
		resultingEnvelopeHash: "h2-resulting-envelope",
		reviewedJudgmentHash: "",
		resultingJudgmentHash: "",
		fromMemoryId: "",
		fromEnvelopeHash: "",
		toMemoryId: "",
		toEnvelopeHash: "",
		successorId: "",
		evidenceRef: "",
		reason: 'approved & verified "controller" review',
		principalId: "subject-1",
		membershipId: "membership-1",
		principalRoles: ["controller", "accountant", "controller"],
		authenticationMethod: "session",
		assuranceLevel: "standard",
		principalAuthenticatedAt: "2026-08-05T13:00:00Z",
		policyVersion: "approval-policy/v0.4.0",
		issuedAt: "2026-08-05T13:00:00Z",
		...overrides,
	};
}

/** Signs the base fixture with the parity seed (Go's signFixture). */
function signedFixture(overrides: Partial<ReceiptPayload> = {}): {
	receipt: SignedReceipt;
	payload: ReceiptPayload;
	publicKey: Uint8Array;
} {
	const payload = basePayload(overrides);
	const { receipt, publicKey } = signReceipt(payload, PARITY_SEED);
	return { receipt, payload, publicKey };
}

/** A valid signing-key record over the fixture's public key (Go's good()). */
function goodKey(
	publicKey: Uint8Array,
	overrides: Partial<SigningKey> = {},
): SigningKey {
	return {
		found: true,
		algorithm: RECEIPT_ALGORITHM,
		publicKey: Buffer.from(publicKey).toString("base64"),
		createdAt: "2026-08-05T10:00:00Z",
		revokedAt: "",
		...overrides,
	};
}

// ──────────────────────────────────────────────
// Payload canonicalization
// ──────────────────────────────────────────────

describe("decodeStoredPayload", () => {
	const { payload } = signedFixture();

	it("canonical payload decodes (roles in canonical order)", () => {
		const canonical = canonicalReceiptPayload(payload);
		const want = { ...payload, principalRoles: ["accountant", "controller"] };
		expect(decodeStoredPayload(canonical)).toEqual(want);
	});

	it("unknown field is rejected (strict decode)", () => {
		const canonical = canonicalReceiptPayload(payload);
		const unknownKey = canonical.replace(
			'"version"',
			'"unknownField":"x","version"',
		);
		expect(() => decodeStoredPayload(unknownKey)).toThrow(/unknown field/);
	});

	it("trailing data is rejected", () => {
		const canonical = canonicalReceiptPayload(payload);
		expect(() => decodeStoredPayload(canonical + ' {"extra":1}')).toThrow(
			/stored payload_json is not a valid canonical receipt payload/,
		);
	});
});

describe("verifyPayloadCanonicalization", () => {
	const { receipt, payload } = signedFixture();
	const canonical = canonicalReceiptPayload(payload);

	it("canonical passes", () => {
		const layer = verifyPayloadCanonicalization(canonical, payload, receipt);
		expect(layer.status).toBe("passed");
	});

	it("non-canonical byte order fails", () => {
		const reordered = canonical.replace(
			'"tenantId":"tenant-1","companyId":"acme"',
			'"companyId":"acme","tenantId":"tenant-1"',
		);
		expect(reordered).not.toBe(canonical);
		expect(
			verifyPayloadCanonicalization(reordered, payload, receipt).status,
		).toBe("failed");
	});

	it("whitespace fails", () => {
		const spaced = canonical.replace('"tenantId":', '"tenantId" : ');
		expect(verifyPayloadCanonicalization(spaced, payload, receipt).status).toBe(
			"failed",
		);
	});

	it("payloadHash mismatch fails", () => {
		const mutated = { ...receipt, payloadHash: "0".repeat(64) };
		expect(
			verifyPayloadCanonicalization(canonical, payload, mutated).status,
		).toBe("failed");
	});
});

// ──────────────────────────────────────────────
// Envelope integrity
// ──────────────────────────────────────────────

describe("verifyEnvelopeIntegrity", () => {
	const { receipt, payload } = signedFixture();
	const stored = receiptHash(receipt);

	it("integral passes", () => {
		expect(verifyEnvelopeIntegrity(receipt, payload, stored).status).toBe(
			"passed",
		);
	});

	it("unknown action fails closed", () => {
		const mutated = {
			...receipt,
			action: "memory_deleted",
		} as unknown as SignedReceipt;
		expect(verifyEnvelopeIntegrity(mutated, payload, stored).status).toBe(
			"failed",
		);
	});

	it("envelope/payload drift fails", () => {
		const mutated = { ...receipt, issuedAt: "2026-08-05T14:00:00Z" };
		expect(verifyEnvelopeIntegrity(mutated, payload, stored).status).toBe(
			"failed",
		);
	});

	it("stored hash mismatch fails", () => {
		const layer = verifyEnvelopeIntegrity(receipt, payload, "0".repeat(64));
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("recomputed receipt hash");
	});
});

// ──────────────────────────────────────────────
// Signature and signing-key validity
// ──────────────────────────────────────────────

describe("verifySignature", () => {
	const { receipt, publicKey } = signedFixture();

	it("valid signature passes", () => {
		expect(verifySignature(receipt, publicKey).status).toBe("passed");
	});

	it("altered signature fails", () => {
		const mutated = { ...receipt };
		const sig = Buffer.from(mutated.signature, "base64");
		sig[0] ^= 0x01;
		mutated.signature = sig.toString("base64");
		expect(verifySignature(mutated, publicKey).status).toBe("failed");
	});

	it("malformed base64 fails", () => {
		const mutated = { ...receipt, signature: "!!!not-base64!!!" };
		expect(verifySignature(mutated, publicKey).status).toBe("failed");
	});

	it("missing key material skips with the failed prerequisite", () => {
		const layer = verifySignature(receipt, null);
		expect(layer.status).toBe("skipped");
		expect(layer.detail).toContain("signing-key validity");
	});
});

describe("verifySigningKeyValidity", () => {
	const { receipt, publicKey } = signedFixture(); // issuedAt 2026-08-05T13:00:00Z

	it("valid key passes", () => {
		expect(verifySigningKeyValidity(goodKey(publicKey), receipt).status).toBe(
			"passed",
		);
	});

	it("before revocation passes", () => {
		const key = goodKey(publicKey, { revokedAt: "2026-08-05T14:00:00Z" });
		expect(verifySigningKeyValidity(key, receipt).status).toBe("passed");
	});

	it("at revocation fails", () => {
		const key = goodKey(publicKey, { revokedAt: "2026-08-05T13:00:00Z" });
		expect(verifySigningKeyValidity(key, receipt).status).toBe("failed");
	});

	it("after revocation fails", () => {
		const key = goodKey(publicKey, { revokedAt: "2026-08-05T12:00:00Z" });
		expect(verifySigningKeyValidity(key, receipt).status).toBe("failed");
	});

	it("unregistered key fails", () => {
		const key = goodKey(publicKey, { found: false });
		expect(verifySigningKeyValidity(key, receipt).status).toBe("failed");
	});

	it("wrong algorithm fails", () => {
		const key = goodKey(publicKey, { algorithm: "RSA" });
		expect(verifySigningKeyValidity(key, receipt).status).toBe("failed");
	});

	it("malformed public key fails", () => {
		const key = goodKey(publicKey, { publicKey: "!!!not-base64!!!" });
		expect(verifySigningKeyValidity(key, receipt).status).toBe("failed");
	});

	it("key id mismatch fails", () => {
		const wrongSeed = new Uint8Array(32);
		wrongSeed[0] = 0x02;
		const { publicKey: wrongPub } = signReceipt(basePayload(), wrongSeed);
		const key = goodKey(publicKey, {
			publicKey: Buffer.from(wrongPub).toString("base64"),
		});
		expect(verifySigningKeyValidity(key, receipt).status).toBe("failed");
	});

	it("created after issued fails", () => {
		const key = goodKey(publicKey, { createdAt: "2026-08-05T14:00:00Z" });
		expect(verifySigningKeyValidity(key, receipt).status).toBe("failed");
	});

	it("malformed created_at fails", () => {
		const key = goodKey(publicKey, { createdAt: "not-a-timestamp" });
		expect(verifySigningKeyValidity(key, receipt).status).toBe("failed");
	});

	it("malformed revoked_at fails", () => {
		const key = goodKey(publicKey, { revokedAt: "not-a-timestamp" });
		expect(verifySigningKeyValidity(key, receipt).status).toBe("failed");
	});

	it("malformed issued_at fails", () => {
		const mutated = { ...receipt, issuedAt: "not-a-timestamp" };
		expect(verifySigningKeyValidity(goodKey(publicKey), mutated).status).toBe(
			"failed",
		);
	});
});

// ──────────────────────────────────────────────
// Tenant/company scope and chain link
// ──────────────────────────────────────────────

describe("verifyTenantCompanyScope", () => {
	const { receipt, payload } = signedFixture();
	const subject: SubjectScope = {
		tenantId: "tenant-1",
		companyId: "acme",
		fiscalPeriodId: "202601",
	};

	it("matching scope passes", () => {
		const layer = verifyTenantCompanyScope(receipt, payload, subject);
		expect(layer.status).toBe("passed");
	});

	it("tenant drift fails", () => {
		const other: SubjectScope = {
			tenantId: "tenant-2",
			companyId: "acme",
			fiscalPeriodId: "202601",
		};
		expect(verifyTenantCompanyScope(receipt, payload, other).status).toBe(
			"failed",
		);
	});
});

describe("verifyChainLink", () => {
	const { receipt: r1Raw } = signedFixture();
	const r1: SignedReceipt = { ...r1Raw, previousReceiptHash: "" };
	const r1hash = receiptHash(r1);
	const r2: SignedReceipt = { ...r1, previousReceiptHash: r1hash };
	const r2hash = receiptHash(r2);

	it("genesis passes", () => {
		const layer = verifyChainLink(r1, "", true);
		expect(layer.status).toBe("passed");
	});

	it("chained receipt passes", () => {
		const layer = verifyChainLink(r2, r1hash, true);
		expect(layer.status).toBe("passed");
	});

	it("broken genesis fails", () => {
		const layer = verifyChainLink(r2, "", true);
		expect(layer.status).toBe("failed");
	});

	it("chain gap fails", () => {
		const gap: SignedReceipt = { ...r2, previousReceiptHash: "" };
		const layer = verifyChainLink(gap, r1hash, true);
		expect(layer.status).toBe("failed");
	});

	it("missing predecessor fails", () => {
		const layer = verifyChainLink(r2, "", false);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("does not resolve");
	});

	it("hash mismatch fails", () => {
		const layer = verifyChainLink(r2, r2hash, true);
		expect(layer.status).toBe("failed");
	});
});

// ──────────────────────────────────────────────
// Principal provenance
// ──────────────────────────────────────────────

describe("verifyPrincipalProvenance", () => {
	const approvedPayload = basePayload(); // memory_approved with the full snapshot

	const approvedAct = (
		overrides: Partial<ActProvenance> = {},
	): ActProvenance => ({
		action: "approved",
		timestamp: "2026-08-05T13:00:00Z",
		principalId: "subject-1",
		membershipId: "membership-1",
		roles: ["controller", "accountant"],
		authenticationMethod: "session",
		assuranceLevel: "standard",
		authenticatedAt: "2026-08-05T13:00:00Z",
		policy: "approval-policy/v0.4.0",
		reason: 'approved & verified "controller" review',
		reviewedEnvelopeHash: "h1-reviewed-envelope",
		resultingEnvelopeHash: "h2-resulting-envelope",
		recordedJudgmentHash: "",
		...overrides,
	});

	it("approved snapshot matches", () => {
		const layer = verifyPrincipalProvenance(approvedPayload, approvedAct());
		expect(layer.status).toBe("passed");
	});

	it("approved reason mismatch fails", () => {
		const act = approvedAct({ reason: "a DIFFERENT reason" });
		const layer = verifyPrincipalProvenance(approvedPayload, act);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("reason");
	});

	it("approved roles mismatch fails", () => {
		const act = approvedAct({ roles: ["controller"] });
		expect(verifyPrincipalProvenance(approvedPayload, act).status).toBe(
			"failed",
		);
	});

	it("claimed act attribution matches", () => {
		const claimed: ReceiptPayload = {
			...basePayload({
				action: "memory_recorded",
				principalId: "test-agent",
				issuedAt: "2026-08-05T12:00:00Z",
			}),
			reason: "",
			resultingEnvelopeHash: "h1-recorded-envelope",
		};
		const act: ActProvenance = {
			action: "recorded",
			timestamp: "2026-08-05T12:00:00Z",
			principalId: "test-agent",
			membershipId: "",
			roles: [],
			authenticationMethod: "",
			assuranceLevel: "",
			authenticatedAt: "",
			policy: "",
			reason: "",
			reviewedEnvelopeHash: "",
			resultingEnvelopeHash: "",
			recordedJudgmentHash: "",
		};
		const layer = verifyPrincipalProvenance(claimed, act);
		expect(layer.status).toBe("passed");
	});

	it("claimed act attribution mismatch fails", () => {
		const claimed: ReceiptPayload = {
			...basePayload({ action: "evidence_linked", principalId: "cli" }),
			reason: "",
			resultingEnvelopeHash: "h3",
		};
		const act: ActProvenance = {
			action: "linked",
			timestamp: "2026-08-05T13:00:00Z",
			principalId: "someone-else",
			membershipId: "",
			roles: [],
			authenticationMethod: "",
			assuranceLevel: "",
			authenticatedAt: "",
			policy: "",
			reason: "",
			reviewedEnvelopeHash: "",
			resultingEnvelopeHash: "",
			recordedJudgmentHash: "",
		};
		const layer = verifyPrincipalProvenance(claimed, act);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("attribution mismatch");
	});

	it("relation confirmed snapshot matches", () => {
		const decided: ReceiptPayload = {
			...basePayload({
				action: "relation_confirmed",
				principalRoles: ["controller", "controller"],
				principalAuthenticatedAt: "2026-08-05T12:00:00Z",
				policyVersion: "judgment-policy/v0.4.0",
				reason: "resolution text",
				resultingJudgmentHash: "a".repeat(64),
				issuedAt: "2026-08-05T14:00:00Z",
			}),
			resultingEnvelopeHash: "",
		};
		const act: ActProvenance = {
			action: "confirm",
			timestamp: "2026-08-05T14:00:00Z",
			principalId: "subject-1",
			membershipId: "membership-1",
			roles: ["controller"],
			authenticationMethod: "session",
			assuranceLevel: "standard",
			authenticatedAt: "2026-08-05T12:00:00Z",
			policy: "judgment-policy/v0.4.0",
			reason: "resolution text",
			reviewedEnvelopeHash: "",
			resultingEnvelopeHash: "",
			recordedJudgmentHash: "a".repeat(64),
		};
		const layer = verifyPrincipalProvenance(decided, act);
		expect(layer.status).toBe("passed");
	});

	it("relation rejected action mapping", () => {
		const rejected: ReceiptPayload = {
			...basePayload({
				action: "relation_rejected",
				principalRoles: ["controller"],
				principalAuthenticatedAt: "2026-08-05T12:00:00Z",
				policyVersion: "judgment-policy/v0.4.0",
				reason: "no",
				resultingJudgmentHash: "b".repeat(64),
				issuedAt: "2026-08-05T14:00:00Z",
			}),
			resultingEnvelopeHash: "",
		};
		const act: ActProvenance = {
			action: "reject",
			timestamp: "2026-08-05T14:00:00Z",
			principalId: "subject-1",
			membershipId: "membership-1",
			roles: ["controller"],
			authenticationMethod: "session",
			assuranceLevel: "standard",
			authenticatedAt: "2026-08-05T12:00:00Z",
			policy: "judgment-policy/v0.4.0",
			reason: "no",
			reviewedEnvelopeHash: "",
			resultingEnvelopeHash: "",
			recordedJudgmentHash: "b".repeat(64),
		};
		const layer = verifyPrincipalProvenance(rejected, act);
		expect(layer.status).toBe("passed");
	});

	it("event action mismatch fails", () => {
		const act = approvedAct({ action: "reject" });
		expect(verifyPrincipalProvenance(approvedPayload, act).status).toBe(
			"failed",
		);
	});
});

// ──────────────────────────────────────────────
// Supersession chain
// ──────────────────────────────────────────────

describe("verifySupersessionChain", () => {
	const scopeA: SubjectScope = {
		tenantId: "tenant-1",
		companyId: "acme",
		fiscalPeriodId: "202601",
	};
	const scopeB: SubjectScope = {
		tenantId: "tenant-2",
		companyId: "other",
		fiscalPeriodId: "202601",
	};

	it("current terminal passes and reports the current id", () => {
		const links: SupersessionLink[] = [
			{
				subjectId: "mem-a",
				successorId: "mem-b",
				superseded: true,
				scope: scopeA,
			},
			{ subjectId: "mem-b", successorId: "", superseded: false, scope: scopeA },
		];
		const layer = verifySupersessionChain(links, scopeA);
		expect(layer.status).toBe("passed");
		expect(layer.detail).toContain("mem-b");
	});

	it("missing successor fails", () => {
		const links: SupersessionLink[] = [
			{ subjectId: "mem-a", successorId: "", superseded: true, scope: scopeA },
		];
		const layer = verifySupersessionChain(links, scopeA);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("missing successor");
	});

	it("cycle fails", () => {
		const links: SupersessionLink[] = [
			{
				subjectId: "mem-a",
				successorId: "mem-b",
				superseded: true,
				scope: scopeA,
			},
			{
				subjectId: "mem-b",
				successorId: "mem-a",
				superseded: true,
				scope: scopeA,
			},
			{
				subjectId: "mem-a",
				successorId: "mem-b",
				superseded: true,
				scope: scopeA,
			},
		];
		const layer = verifySupersessionChain(links, scopeA);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("cycle");
	});

	it("cross-scope link fails", () => {
		const links: SupersessionLink[] = [
			{
				subjectId: "mem-a",
				successorId: "mem-b",
				superseded: true,
				scope: scopeA,
			},
			{ subjectId: "mem-b", successorId: "", superseded: false, scope: scopeB },
		];
		const layer = verifySupersessionChain(links, scopeA);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("crosses scope");
	});

	it("status/relation disagreement fails", () => {
		const links: SupersessionLink[] = [
			{
				subjectId: "mem-a",
				successorId: "mem-b",
				superseded: false,
				scope: scopeA,
			},
		];
		const layer = verifySupersessionChain(links, scopeA);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("disagrees");
	});
});

// ──────────────────────────────────────────────
// Evidence / rule availability
// ──────────────────────────────────────────────

describe("verifyEvidenceAvailability", () => {
	const env = "e".repeat(64);

	it("declared refs linked and envelope matches", () => {
		expect(
			verifyEvidenceAvailability(["ref-1"], ["ref-1"], env, env).status,
		).toBe("passed");
	});

	it("removed link row fails", () => {
		const layer = verifyEvidenceAvailability(["ref-1"], [], env, env);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("ref-1");
	});

	it("envelope mismatch fails", () => {
		const layer = verifyEvidenceAvailability(
			["ref-1"],
			["ref-1"],
			"f".repeat(64),
			env,
		);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("head result");
	});
});

describe("verifyRuleAvailability", () => {
	const env = "e".repeat(64);

	it("dynamic refs row-backed and envelope matches", () => {
		expect(
			verifyRuleAvailability(
				["stored-1"],
				["linked-1"],
				["stored-1", "linked-1"],
				env,
				env,
			).status,
		).toBe("passed");
	});

	it("unbacked dynamic ref fails", () => {
		const layer = verifyRuleAvailability(
			["stored-1"],
			[],
			["stored-1", "linked-1"],
			env,
			env,
		);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toContain("linked-1");
	});

	it("envelope mismatch fails", () => {
		expect(
			verifyRuleAvailability(
				["stored-1"],
				["linked-1"],
				["stored-1", "linked-1"],
				"f".repeat(64),
				env,
			).status,
		).toBe("failed");
	});
});

// ──────────────────────────────────────────────
// Judgment hash
// ──────────────────────────────────────────────

describe("verifyJudgmentHash", () => {
	const hash = "a".repeat(64);

	it("matching hashes pass", () => {
		expect(verifyJudgmentHash(hash, hash, hash).status).toBe("passed");
	});

	it("current-row mismatch fails", () => {
		expect(verifyJudgmentHash("b".repeat(64), hash, hash).status).toBe(
			"failed",
		);
	});

	it("event mismatch fails", () => {
		expect(verifyJudgmentHash(hash, "c".repeat(64), hash).status).toBe(
			"failed",
		);
	});
});

// ──────────────────────────────────────────────
// Aggregation and finalization
// ──────────────────────────────────────────────

describe("aggregateLayers", () => {
	const pass: VerificationLayer = { name: "x", status: "passed", detail: "ok" };
	const fail: VerificationLayer = {
		name: "x",
		status: "failed",
		detail: "boom",
	};
	const skip: VerificationLayer = {
		name: "x",
		status: "skipped",
		detail: "skipped: prerequisite",
	};

	it("any failure fails", () => {
		const layer = aggregateLayers("x", [pass, fail, pass]);
		expect(layer.status).toBe("failed");
		expect(layer.detail).toBe("boom");
	});

	it("all skipped stays skipped", () => {
		const layer = aggregateLayers("x", [skip, skip]);
		expect(layer.status).toBe("skipped");
	});

	it("mixed skip and pass passes", () => {
		const layer = aggregateLayers("x", [skip, pass]);
		expect(layer.status).toBe("passed");
		expect(layer.detail).toContain("2 receipts");
	});

	it("single instance is identity", () => {
		const layer = aggregateLayers("supersession chain", [
			{
				name: "supersession chain",
				status: "passed",
				detail: "chain current at mem-b",
			},
		]);
		expect(layer.status).toBe("passed");
		expect(layer.detail).toBe("chain current at mem-b");
	});
});

describe("finalize mandatory conclusion", () => {
	const suffix = `"accountingCorrectness":"Accounting correctness: NOT ASSERTED"}`;

	const build = (status: VerificationStatus): VerificationReport => {
		const report = newReport("memory", "mem-1");
		report.layers.push({ name: "x", status, detail: "d" });
		finalize(report);
		return report;
	};

	it("all-pass report ends with the exact conclusion", () => {
		const passed = build("passed");
		expect(passed.outcome).toBe("passed");
		expect(JSON.stringify(passed).endsWith(suffix)).toBe(true);
	});

	it("failed report ends with the exact conclusion", () => {
		const failed = build("failed");
		expect(failed.outcome).toBe("failed");
		expect(JSON.stringify(failed).endsWith(suffix)).toBe(true);
		expect(failed.accountingCorrectness).toBe(
			ACCOUNTING_CORRECTNESS_NOT_ASSERTED,
		);
	});
});

describe("receiptLayerNames stable order", () => {
	it("freezes the six receipt-layer names shared with Go (design §2)", () => {
		expect(receiptLayerNames()).toEqual([
			"payload canonicalization",
			"envelope integrity",
			"signature",
			"signing-key validity",
			"tenant/company scope",
			"chain link",
		]);
	});
});

// ──────────────────────────────────────────────
// Shared Go↔TS parity fixture (design §7)
// ──────────────────────────────────────────────

/** The shared verify fixture shape (testdata/verify-parity.json). Field names
 * mirror the Go fixture-local structs (design §7). */
interface VerifyParityAvailability {
	declaredRefs: string[];
	currentLinks: string[];
	currentEnvelope: string;
	committedEnvelope: string;
}

interface VerifyParityRuleAvailability {
	storedRefs: string[];
	currentLinks: string[];
	mergedRefs: string[];
	currentEnvelope: string;
	committedEnvelope: string;
}

interface VerifyParityScenario {
	key: string;
	expected: {
		outcome: VerificationOutcome;
		accountingCorrectness: string;
		receipts: ReceiptVerification[];
		layers: VerificationLayer[];
	};
}

interface VerifyParityFixture {
	name: string;
	subjectType: string;
	subjectId: string;
	seed: string;
	subjectScope: SubjectScope;
	keys: Record<string, SigningKey>;
	receipts: SignedReceipt[];
	payloads: ReceiptPayload[];
	provenance: ActProvenance[];
	supersessionLinks: SupersessionLink[];
	evidence: VerifyParityAvailability;
	rules: VerifyParityRuleAvailability;
	scenarios: Record<string, VerifyParityScenario>;
}

const VERIFY_PARITY_PATH = resolve(
	process.cwd(),
	"testdata",
	"verify-parity.json",
);

function loadVerifyParityFixture(): VerifyParityFixture {
	return JSON.parse(
		readFileSync(VERIFY_PARITY_PATH, "utf-8"),
	) as VerifyParityFixture;
}

/**
 * Builds the full verification report exactly as the Go parity test does (and as
 * internal/server/verify_service.go assembles memory reports): per-receipt six
 * layers in the stable order, the six aggregated top-level layers, the per-
 * receipt diagnostic blocks, then the memory object layers (principal
 * provenance aggregate, supersession chain, evidence availability, rule
 * availability) and Finalize. The fixture pins the expected ordered results in
 * both runtimes.
 */
function buildParityReport(
	fixture: VerifyParityFixture,
	key: SigningKey,
): VerificationReport {
	const report = newReport(fixture.subjectType, fixture.subjectId);
	const rawKey = decodeSigningPublicKey(key);
	const perReceipt: VerificationLayer[][] = [];
	let prevComputed = "";
	for (let i = 0; i < fixture.receipts.length; i++) {
		const receipt = fixture.receipts[i]!;
		const payload = fixture.payloads[i]!;
		const payloadJson = canonicalReceiptPayload(payload);
		const storedHash = receiptHash(receipt);
		const layers: VerificationLayer[] = [
			verifyPayloadCanonicalization(payloadJson, payload, receipt),
			verifyEnvelopeIntegrity(receipt, payload, storedHash),
			verifySignature(receipt, rawKey),
			verifySigningKeyValidity(key, receipt),
			verifyTenantCompanyScope(receipt, payload, fixture.subjectScope),
		];
		layers.push(verifyChainLink(receipt, prevComputed, true));
		prevComputed = storedHash;
		perReceipt.push(layers);
	}
	for (let col = 0; col < receiptLayerNames().length; col++) {
		const name = receiptLayerNames()[col]!;
		const instances = perReceipt.map((layers) => layers[col]!);
		report.layers.push(aggregateLayers(name, instances));
	}
	for (let i = 0; i < fixture.receipts.length; i++) {
		report.receipts.push({
			receiptHash: receiptHash(fixture.receipts[i]!),
			action: fixture.receipts[i]!.action,
			layers: perReceipt[i]!,
		});
	}
	const provInstances = fixture.payloads.map((p, i) =>
		verifyPrincipalProvenance(p, fixture.provenance[i]!),
	);
	report.layers.push(
		aggregateLayers(LAYER_PRINCIPAL_PROVENANCE, provInstances),
	);
	report.layers.push(
		verifySupersessionChain(fixture.supersessionLinks, fixture.subjectScope),
	);
	report.layers.push(
		verifyEvidenceAvailability(
			fixture.evidence.declaredRefs,
			fixture.evidence.currentLinks,
			fixture.evidence.currentEnvelope,
			fixture.evidence.committedEnvelope,
		),
	);
	report.layers.push(
		verifyRuleAvailability(
			fixture.rules.storedRefs,
			fixture.rules.currentLinks,
			fixture.rules.mergedRefs,
			fixture.rules.currentEnvelope,
			fixture.rules.committedEnvelope,
		),
	);
	finalize(report);
	return report;
}

describe("shared verify parity fixture (Go ↔ TS)", () => {
	const fixture = loadVerifyParityFixture();

	it("fixture sanity: fixed parity seed", () => {
		expect(fixture.seed).toBe(PARITY_SEED_HEX);
	});

	for (const [scenarioName, scenario] of Object.entries(fixture.scenarios)) {
		it(`scenario ${scenarioName}: identical ordered layers, outcome and conclusion`, () => {
			const key = fixture.keys[scenario.key]!;
			expect(key).toBeDefined();
			const report = buildParityReport(fixture, key);

			expect(report.subjectType).toBe(fixture.subjectType);
			expect(report.subjectId).toBe(fixture.subjectId);
			expect(report.outcome).toBe(scenario.expected.outcome);
			expect(report.accountingCorrectness).toBe(
				ACCOUNTING_CORRECTNESS_NOT_ASSERTED,
			);
			expect(report.accountingCorrectness).toBe(
				scenario.expected.accountingCorrectness,
			);
			expect(report.receipts).toEqual(scenario.expected.receipts);
			expect(report.layers).toEqual(scenario.expected.layers);

			// AC12: the serialized report ends with the exact conclusion in both
			// the all-pass and the failed report.
			expect(JSON.stringify(report).endsWith(suffixFor(scenario))).toBe(true);
		});
	}
});

/** The serialized closing bytes every finalized report must end with. */
function suffixFor(scenario: VerifyParityScenario): string {
	return `"accountingCorrectness":${JSON.stringify(scenario.expected.accountingCorrectness)}}`;
}
