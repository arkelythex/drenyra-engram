/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * EvidenceHold pure mirror tests (v0.8.0 object-level legal holds, batch 3) —
 * the exact TS counterpart of `internal/core/hold_test.go`
 * (docs/architecture/evidence-lifecycle-v0.8.md §3.2/§7): the closed hold-kind
 * set, the ONE-WAY lift consistency validator, the PURE active-blocking-hold
 * helper (empty blocking set blocks NOTHING; lifted holds never block), the
 * clone-isolation contract and the FROZEN canonical metadata bytes pinned
 * byte-identically with Go (fixed property order, compact UTF-8, NO HTML
 * escaping, omitempty optional fields).
 *
 * CROSS-RUNTIME PARITY: the sample fixture and the pinned canonical literal
 * below are SHARED with the Go test — the same canonical bytes must match
 * byte-identically in both runtimes.
 */

import { describe, expect, it } from "vitest";

import {
	canonicalEvidenceHoldJSON,
	cloneEvidenceHold,
	hasActiveBlockingHold,
	isValidHoldKind,
	validateEvidenceHold,
} from "../evidence-hold.js";
import type { EvidenceHold } from "../types.js";

/** The deterministic object id of the shared fixture (64 hex digits). */
const SAMPLE_OBJECT_ID = "a".repeat(64);

/** The FROZEN canonical bytes of sampleHold — the exact same literal pinned in
 * internal/core/hold_test.go (Go↔TS canonical bytes must match byte-identically:
 * fixed property order, compact UTF-8, no HTML escaping, omitempty optional
 * fields). The placed state carries NO lift keys. */
const PINNED_PLACED_CANONICAL_JSON =
	'{"holdId":"00000000-0000-4000-8000-000000000001","objectId":"' +
	"a".repeat(64) +
	'","tenantId":"org-001","companyId":"acme","ruc":"20100039201","period":"202401","kind":"legal","reason":"dispute F001-948 under review","ownerSubjectId":"subject-1","placedAt":"2026-08-07T12:00:00.000Z","placedBy":"lucia.ramirez"}';

const sampleHold: EvidenceHold = {
	holdId: "00000000-0000-4000-8000-000000000001",
	objectId: SAMPLE_OBJECT_ID,
	tenantId: "org-001",
	companyId: "acme",
	ruc: "20100039201",
	period: "202401",
	kind: "legal",
	reason: "dispute F001-948 under review",
	ownerSubjectId: "subject-1",
	placedAt: "2026-08-07T12:00:00.000Z",
	placedBy: "lucia.ramirez",
};

/** The lifted variant — the lift fields append AFTER placedBy in canonical
 * order. */
const liftedHold: EvidenceHold = {
	...sampleHold,
	liftedAt: "2026-08-08T09:00:00.000Z",
	liftedBy: "maria.torres",
	liftReason: "dispute resolved",
};

describe("evidence hold mirror (v0.8.0 batch 3)", () => {
	it("pins the closed hold-kind set shared with the retention policy", () => {
		expect(isValidHoldKind("legal")).toBe(true);
		expect(isValidHoldKind("audit")).toBe(true);
		expect(isValidHoldKind("dispute")).toBe(true);
		expect(isValidHoldKind("fiscalization")).toBe(true);
		expect(isValidHoldKind("other")).toBe(true);
		expect(isValidHoldKind("rogue")).toBe(false);
		expect(isValidHoldKind(42)).toBe(false);
	});

	it("pins the canonical placed bytes shared with Go", () => {
		expect(canonicalEvidenceHoldJSON(sampleHold)).toBe(
			PINNED_PLACED_CANONICAL_JSON,
		);
	});

	it("appends the one-way lift fields in canonical order after placedBy", () => {
		const canonical = canonicalEvidenceHoldJSON(liftedHold);
		expect(canonical).toContain('"placedBy":"lucia.ramirez"');
		expect(canonical).toContain(
			'"liftedAt":"2026-08-08T09:00:00.000Z","liftedBy":"maria.torres","liftReason":"dispute resolved"',
		);
		// The lift keys come strictly after placedBy (the fixed field order).
		expect(canonical.indexOf("placedBy")).toBeLessThan(canonical.indexOf("liftedAt"));
	});

	it("omits empty optional fields (omitempty parity with Go)", () => {
		const noPeriod = { ...sampleHold, period: "" };
		const canonical = canonicalEvidenceHoldJSON(noPeriod);
		expect(canonical).not.toContain('"period"');
		expect(canonical).not.toContain("liftedAt");
	});

	it("is deterministic for the same input and stable under clone", () => {
		expect(canonicalEvidenceHoldJSON(sampleHold)).toBe(
			canonicalEvidenceHoldJSON({ ...sampleHold }),
		);
		const cloned = cloneEvidenceHold(sampleHold);
		expect(cloned).toEqual(sampleHold);
		expect(cloned).not.toBe(sampleHold);
		cloned.reason = "mutated";
		expect(sampleHold.reason).toBe("dispute F001-948 under review");
	});

	it("validates the structural contract fail-closed", () => {
		expect(validateEvidenceHold(sampleHold)).toBeNull();
		expect(validateEvidenceHold(liftedHold)).toBeNull();

		expect(validateEvidenceHold(null)).toContain("INVALID_HOLD");
		expect(validateEvidenceHold({ ...sampleHold, objectId: "not-hex" })).toContain(
			"INVALID_HOLD_OBJECT_ID",
		);
		expect(
			validateEvidenceHold({ ...sampleHold, holdId: "not-a-uuid" }),
		).toContain("INVALID_HOLD_ID");
		expect(
			validateEvidenceHold({ ...sampleHold, tenantId: "" }),
		).toContain("INVALID_HOLD_SCOPE");
		expect(
			validateEvidenceHold({ ...sampleHold, ruc: "123" }),
		).toContain("INVALID_HOLD_SCOPE");
		expect(
			validateEvidenceHold({ ...sampleHold, kind: "rogue" }),
		).toContain("INVALID_HOLD_KIND");
		expect(validateEvidenceHold({ ...sampleHold, reason: "  " })).toContain(
			"REASON_REQUIRED",
		);
		expect(
			validateEvidenceHold({ ...sampleHold, ownerSubjectId: "" }),
		).toContain("INVALID_HOLD_OWNER");
		expect(
			validateEvidenceHold({ ...sampleHold, placedBy: "" }),
		).toContain("INVALID_HOLD_PLACED_BY");
		expect(
			validateEvidenceHold({ ...sampleHold, placedAt: "not-a-date" }),
		).toContain("INVALID_HOLD_PLACED_AT");

		// The lift fields are CONSISTENT: a partially lifted row is invalid.
		expect(
			validateEvidenceHold({ ...sampleHold, liftedAt: "2026-08-08T09:00:00.000Z" }),
		).toContain("INVALID_HOLD_LIFT");
		expect(
			validateEvidenceHold({
				...sampleHold,
				liftedAt: "not-a-date",
				liftedBy: "maria.torres",
				liftReason: "resolved",
			}),
		).toContain("INVALID_HOLD_LIFTED_AT");
		expect(
			validateEvidenceHold({
				...sampleHold,
				liftedAt: "2026-08-08T09:00:00.000Z",
				liftedBy: "maria.torres",
				liftReason: " ",
			}),
		).toContain("REASON_REQUIRED");
	});

	it("blocks only ACTIVE holds whose kind is in the blocking set", () => {
		const defaultBlocking = ["legal", "audit", "dispute", "fiscalization"];
		expect(hasActiveBlockingHold(sampleHold, defaultBlocking)).toBe(true);
		expect(hasActiveBlockingHold(liftedHold, defaultBlocking)).toBe(false);

		const other: EvidenceHold = { ...sampleHold, kind: "other" };
		expect(hasActiveBlockingHold(other, defaultBlocking)).toBe(false);
		// A deployment may extend the blocking set per policy.
		expect(hasActiveBlockingHold(other, [...defaultBlocking, "other"])).toBe(
			true,
		);
		// An EMPTY blocking set blocks NOTHING (fail closed).
		expect(hasActiveBlockingHold(sampleHold, [])).toBe(false);
		// A non-blocking kind on an active hold never blocks.
		const audit: EvidenceHold = { ...sampleHold, kind: "audit" };
		expect(hasActiveBlockingHold(audit, ["legal"])).toBe(false);
	});
});
