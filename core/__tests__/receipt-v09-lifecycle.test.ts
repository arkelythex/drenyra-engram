/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Receipt-payload/v0.9.0 lifecycle-hash canonicalization tests (v0.8 batch 4,
 * schema v11 — docs/architecture/evidence-lifecycle-v0.8.md §3.8/§5). The exact
 * TS counterpart of the v0.9.0 branch of `core.CanonicalReceiptPayload` (Go):
 *
 * - a v0.9.0 payload appends reviewedLifecycleHash / resultingLifecycleHash /
 *   executionAttemptId AFTER issuedAt, in FIXED order, on top of the frozen
 *   legacy key list — the two canonical lifecycle snapshot hashes H1/H2 (design
 *   §3.8) plus the additive execution-attempt id of a purge_intent receipt
 *   (populated ONLY for the intent act, so every intent receipt is uniquely
 *   auditable and a fresh-ID retry after an interrupted intent never collides
 *   on the payload-hash UNIQUE backstop);
 * - the extension is VERSION-conditional: a pre-v0.9 payload canonicalizes to
 *   the frozen legacy bytes even when the optional lifecycle fields carry
 *   values (legacy receipts never re-version, so pre-v0.9 payload bytes stay
 *   byte-identical — verifiers keep accepting v0.4.0–v0.8.0 payloads);
 * - the v0.9.0 evidence-lifecycle family (retention_bound and the six purge
 *   transitions plus purge_intent) JOINS the closed TS action set with full
 *   parity against Go's IsValidReceiptAction — the strict verifier accepts
 *   every act, and the execution-intent receipt (Go's purgeReceiptPayload:
 *   H1 == H2, the intent changes NO canonical snapshot field) canonicalizes
 *   to pinned bytes shared with Go;
 * - the pinned literals below are the Go↔TS byte contract: Go's
 *   `CanonicalReceiptPayload` produces exactly these bytes, so the pinned
 *   SHA-256 digests are shared across runtimes.
 */

import { describe, expect, it } from "vitest";

import { canonicalReceiptPayload, receiptPayloadHash } from "../receipt.js";
import {
	RECEIPT_ACTIONS,
	RECEIPT_PAYLOAD_VERSION_V08,
	RECEIPT_PAYLOAD_VERSION_V09,
	type ReceiptPayload,
} from "../types.js";

/** Content-addressed evidence-object id of the fixtures (64 lowercase hex). */
const OBJECT_ID = "a".repeat(64);

/** The two canonical lifecycle snapshot hashes (H1/H2, design §3.8). */
const H1 = "h1-reviewed-lifecycle-hash";
const H2 = "h2-resulting-lifecycle-hash";

/** The (tenant, executionId) of the pinned purge_intent attempt (WU-2). */
const EXECUTION_ID = "00000000-0000-4000-8000-000000000901";

/**
 * The canonical v0.9.0 purge_requested fixture: an evidence_object subject, the
 * object identity in evidenceRef, UNSORTED + DUPLICATED roles (canonicalized by
 * the payload contract) and the two lifecycle hashes. The action token is a
 * CLOSED TS action (no cast — full Go IsValidReceiptAction parity);
 * canonicalization stays VERSION-driven, never action-driven — exactly what
 * these tests pin.
 */
function v09LifecyclePayload(
	overrides?: Partial<ReceiptPayload>,
): ReceiptPayload {
	return {
		version: RECEIPT_PAYLOAD_VERSION_V09,
		subjectType: "evidence_object",
		subjectId: OBJECT_ID,
		action: "purge_requested",
		tenantId: "tenant-1",
		companyId: "acme",
		fiscalPeriodId: "202601",
		reviewedEnvelopeHash: "",
		resultingEnvelopeHash: "",
		reviewedJudgmentHash: "",
		resultingJudgmentHash: "",
		fromMemoryId: "",
		fromEnvelopeHash: "",
		toMemoryId: "",
		toEnvelopeHash: "",
		successorId: "",
		evidenceRef: OBJECT_ID,
		reason: "retention period elapsed",
		principalId: "subject-1",
		membershipId: "membership-1",
		principalRoles: ["controller", "accountant", "controller"],
		authenticationMethod: "session",
		assuranceLevel: "standard",
		principalAuthenticatedAt: "2026-08-05T13:00:00Z",
		policyVersion: "evidence-lifecycle-policy/v0.8.0",
		issuedAt: "2026-08-05T13:00:00Z",
		reviewedLifecycleHash: H1,
		resultingLifecycleHash: H2,
		executionAttemptId: "",
		...overrides,
	};
}

/**
 * The frozen legacy v0.8.0 hold_placed payload carrying the SAME optional
 * lifecycle fields with values — canonicalization must DROP them (the
 * extension is version-conditional, so pre-v0.9 bytes stay frozen).
 */
function v08LegacyPayload(): ReceiptPayload {
	return {
		...v09LifecyclePayload({
			version: RECEIPT_PAYLOAD_VERSION_V08,
			action: "hold_placed",
			reason: "hold placed",
			executionAttemptId: EXECUTION_ID,
		}),
	};
}

/**
 * The canonical v0.9.0 purge_intent execution fixture — the payload Go's
 * execution store emits (purgeReceiptPayload, design §11 step 1): H1 AND H2
 * are BOTH the reviewed approval hash (the intent changes NO canonical
 * snapshot field — the projection flips only in completion), the object
 * identity rides evidenceRef, the frozen evidence-lifecycle policy version is
 * stamped and the execution-attempt id (the (tenant, executionId) of the
 * attempt) rides executionAttemptId — the additive per-attempt discriminator.
 * purge_intent is a CLOSED TS action (no cast) — one act of the full v0.9
 * evidence-lifecycle family in Go↔TS closed-set parity.
 */
function purgeIntentPayload(
	overrides?: Partial<ReceiptPayload>,
): ReceiptPayload {
	return v09LifecyclePayload({
		action: "purge_intent",
		reason: "execute approved purge",
		reviewedLifecycleHash: H1,
		resultingLifecycleHash: H1,
		executionAttemptId: EXECUTION_ID,
		...overrides,
	});
}

/**
 * Pinned canonical v0.9.0 bytes — the EXACT bytes Go's
 * `core.CanonicalReceiptPayload` produces for the same payload (fixed field
 * order: the frozen legacy key list, then reviewedLifecycleHash then
 * resultingLifecycleHash then executionAttemptId AFTER issuedAt — empty for
 * non-intent acts). Builds the literal from OBJECT_ID / H1 / H2 so the fixture
 * and the pin cannot drift.
 */
const PINNED_V09_CANONICAL = `{"version":"receipt-payload/v0.9.0","subjectType":"evidence_object","subjectId":"${OBJECT_ID}","action":"purge_requested","tenantId":"tenant-1","companyId":"acme","fiscalPeriodId":"202601","reviewedEnvelopeHash":"","resultingEnvelopeHash":"","reviewedJudgmentHash":"","resultingJudgmentHash":"","fromMemoryId":"","fromEnvelopeHash":"","toMemoryId":"","toEnvelopeHash":"","successorId":"","evidenceRef":"${OBJECT_ID}","reason":"retention period elapsed","principalId":"subject-1","membershipId":"membership-1","principalRoles":["accountant","controller"],"authenticationMethod":"session","assuranceLevel":"standard","principalAuthenticatedAt":"2026-08-05T13:00:00Z","policyVersion":"evidence-lifecycle-policy/v0.8.0","issuedAt":"2026-08-05T13:00:00Z","reviewedLifecycleHash":"${H1}","resultingLifecycleHash":"${H2}","executionAttemptId":""}`;

/** SHA-256 hex of PINNED_V09_CANONICAL — the Go↔TS shared digest contract. */
const PINNED_V09_PAYLOAD_HASH =
	"bdf6ad3e57791ea7ac5524523fa4504dd059446b01780bfac6afbeefc9f0e3bd";

/** Pinned frozen legacy v0.8.0 bytes (the lifecycle fields are DROPPED). */
const PINNED_V08_CANONICAL = `{"version":"receipt-payload/v0.8.0","subjectType":"evidence_object","subjectId":"${OBJECT_ID}","action":"hold_placed","tenantId":"tenant-1","companyId":"acme","fiscalPeriodId":"202601","reviewedEnvelopeHash":"","resultingEnvelopeHash":"","reviewedJudgmentHash":"","resultingJudgmentHash":"","fromMemoryId":"","fromEnvelopeHash":"","toMemoryId":"","toEnvelopeHash":"","successorId":"","evidenceRef":"${OBJECT_ID}","reason":"hold placed","principalId":"subject-1","membershipId":"membership-1","principalRoles":["accountant","controller"],"authenticationMethod":"session","assuranceLevel":"standard","principalAuthenticatedAt":"2026-08-05T13:00:00Z","policyVersion":"evidence-lifecycle-policy/v0.8.0","issuedAt":"2026-08-05T13:00:00Z"}`;

/** SHA-256 hex of PINNED_V08_CANONICAL — the Go↔TS shared digest contract. */
const PINNED_V08_PAYLOAD_HASH =
	"5268b4daae84a51f06d1c08b8f07c971021a77b3801d4df41f52ce830dd3d435";

/**
 * Pinned canonical v0.9.0 purge_intent bytes — the EXACT bytes Go's
 * `core.CanonicalReceiptPayload` produces for the execution-intent payload
 * (H1 == H2: the intent changes no canonical snapshot field; the
 * execution-attempt id is the per-attempt discriminator). Builds the literal
 * from OBJECT_ID / H1 / EXECUTION_ID so the fixture and the pin cannot drift.
 */
const PINNED_PURGE_INTENT_CANONICAL = `{"version":"receipt-payload/v0.9.0","subjectType":"evidence_object","subjectId":"${OBJECT_ID}","action":"purge_intent","tenantId":"tenant-1","companyId":"acme","fiscalPeriodId":"202601","reviewedEnvelopeHash":"","resultingEnvelopeHash":"","reviewedJudgmentHash":"","resultingJudgmentHash":"","fromMemoryId":"","fromEnvelopeHash":"","toMemoryId":"","toEnvelopeHash":"","successorId":"","evidenceRef":"${OBJECT_ID}","reason":"execute approved purge","principalId":"subject-1","membershipId":"membership-1","principalRoles":["accountant","controller"],"authenticationMethod":"session","assuranceLevel":"standard","principalAuthenticatedAt":"2026-08-05T13:00:00Z","policyVersion":"evidence-lifecycle-policy/v0.8.0","issuedAt":"2026-08-05T13:00:00Z","reviewedLifecycleHash":"${H1}","resultingLifecycleHash":"${H1}","executionAttemptId":"${EXECUTION_ID}"}`;

/** SHA-256 hex of PINNED_PURGE_INTENT_CANONICAL — the Go↔TS shared digest. */
const PINNED_PURGE_INTENT_PAYLOAD_HASH =
	"238684abdc19291cd22b22b34604ca7a345582e56c2b6a706dd7f6e00d7363fe";

describe("receipt-payload/v0.9.0 lifecycle-hash canonicalization", () => {
	it("pins the exact Go-compatible canonical field order for a v0.9 payload", () => {
		expect(canonicalReceiptPayload(v09LifecyclePayload())).toBe(
			PINNED_V09_CANONICAL,
		);
	});

	it("appends the three v0.9 fields AFTER issuedAt in fixed order", () => {
		const canonical = canonicalReceiptPayload(v09LifecyclePayload());
		const issuedAt = canonical.indexOf('"issuedAt"');
		const reviewed = canonical.indexOf('"reviewedLifecycleHash"');
		const resulting = canonical.indexOf('"resultingLifecycleHash"');
		const attempt = canonical.indexOf('"executionAttemptId"');
		expect(issuedAt).toBeGreaterThan(0);
		expect(reviewed).toBeGreaterThan(issuedAt);
		expect(resulting).toBeGreaterThan(reviewed);
		expect(attempt).toBeGreaterThan(resulting);
		// The frozen legacy key list stays intact on top of the extension.
		expect(canonical).toContain('"version":"receipt-payload/v0.9.0"');
		expect(canonical).toContain('"action":"purge_requested"');
		expect(canonical).toContain('"principalRoles":["accountant","controller"]');
		expect(canonical).toContain(`"reviewedLifecycleHash":"${H1}"`);
		expect(canonical).toContain(`"resultingLifecycleHash":"${H2}"`);
		expect(canonical).toContain('"executionAttemptId":""');
	});

	it("keeps pre-v0.9 bytes frozen: a v0.8 payload drops the lifecycle fields even when set", () => {
		// The SAME fixture with lifecycle fields populated canonicalizes WITHOUT
		// the v0.9 keys (including a populated execution-attempt id) —
		// byte-identical to the frozen legacy literal (legacy receipts never
		// re-version).
		expect(canonicalReceiptPayload(v08LegacyPayload())).toBe(
			PINNED_V08_CANONICAL,
		);
		expect(PINNED_V08_CANONICAL).not.toContain("reviewedLifecycleHash");
		expect(PINNED_V08_CANONICAL).not.toContain("resultingLifecycleHash");
		expect(PINNED_V08_CANONICAL).not.toContain("executionAttemptId");
	});

	it("canonicalizes absent lifecycle fields as empty strings on a v0.9 payload", () => {
		const canonical = canonicalReceiptPayload(
			v09LifecyclePayload({
				reviewedLifecycleHash: undefined,
				resultingLifecycleHash: undefined,
				executionAttemptId: undefined,
			}),
		);
		expect(
			canonical.endsWith(
				'"issuedAt":"2026-08-05T13:00:00Z","reviewedLifecycleHash":"","resultingLifecycleHash":"","executionAttemptId":""}',
			),
		).toBe(true);
	});

	it("pins the Go↔TS shared payload digests for v0.9 and legacy bytes", () => {
		expect(receiptPayloadHash(v09LifecyclePayload())).toBe(
			PINNED_V09_PAYLOAD_HASH,
		);
		expect(receiptPayloadHash(v08LegacyPayload())).toBe(
			PINNED_V08_PAYLOAD_HASH,
		);
	});

	it("accepts purge_intent as a CLOSED action (no cast) with Go-compatible canonical bytes", () => {
		// Go↔TS parity: every v0.9 evidence-lifecycle act joins the closed TS
		// action set, so the fixture needs NO cast (compile-time closed-set
		// proof) and the strict verifier accepts it (receipt.test.ts round trip).
		expect(RECEIPT_ACTIONS).toContain("purge_intent");
		expect(canonicalReceiptPayload(purgeIntentPayload())).toBe(
			PINNED_PURGE_INTENT_CANONICAL,
		);
		expect(canonicalReceiptPayload(purgeIntentPayload())).toContain(
			'"action":"purge_intent"',
		);
		expect(canonicalReceiptPayload(purgeIntentPayload())).toContain(
			`"executionAttemptId":"${EXECUTION_ID}"`,
		);
	});

	it("pins the Go↔TS shared payload digest for the purge_intent execution intent", () => {
		// The intent receipt covers H1 == H2 (the reviewed approval hash): the
		// intent changes NO canonical snapshot field (Go's purgeReceiptPayload),
		// and the execution-attempt id discriminates every attempt.
		const intent = purgeIntentPayload();
		expect(intent.resultingLifecycleHash).toBe(intent.reviewedLifecycleHash);
		expect(intent.executionAttemptId).toBe(EXECUTION_ID);
		expect(receiptPayloadHash(intent)).toBe(PINNED_PURGE_INTENT_PAYLOAD_HASH);
	});

	it("makes every intent receipt attempt uniquely auditable: a fresh execution id changes the digest", () => {
		// WU-2 regression: a fresh-ID retry after an interrupted intent must emit
		// a DISTINCT purge_intent payload (the additive execution-attempt
		// discriminator), so the payload hashes never collide on the
		// UNIQUE(subject_type, subject_id, action, payload_hash) backstop.
		const attempt1 = purgeIntentPayload();
		const attempt2 = purgeIntentPayload({
			executionAttemptId: "00000000-0000-4000-8000-000000000902",
		});
		expect(receiptPayloadHash(attempt1)).not.toBe(receiptPayloadHash(attempt2));
		expect(canonicalReceiptPayload(attempt1)).not.toBe(
			canonicalReceiptPayload(attempt2),
		);
	});

	it("is deterministic and version-driven, not action-driven", () => {
		const a = canonicalReceiptPayload(v09LifecyclePayload());
		const b = canonicalReceiptPayload(v09LifecyclePayload());
		expect(a).toBe(b);
		// The same v0.9 version with a different closed act (the v0.8 hold action
		// hold_lifted, a closed TS action without a cast) canonicalizes with the
		// SAME three-field tail — only the version selects the shape (the
		// execution-attempt id stays empty for non-intent acts).
		const otherAct = canonicalReceiptPayload(
			v09LifecyclePayload({ action: "hold_lifted" }),
		);
		expect(otherAct).toContain('"reviewedLifecycleHash":"');
		expect(otherAct).toContain('"resultingLifecycleHash":"');
		expect(otherAct).toContain('"executionAttemptId":""');
	});
});
