/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats — and `size` is a byte length, a JSON integer, never a float.
 *
 * EvidenceObject pure mirror tests (v0.7.0 local-first slice) — the exact TS
 * counterpart of `internal/core/evidence_object_test.go`: the deterministic
 * content address (computeObjectId), the deterministic content-addressed
 * relative layout (objectRelPath), the FROZEN canonical metadata bytes pinned
 * byte-identically with Go (fixed property order, compact UTF-8, NO HTML
 * escaping), the clone-isolation contract and the fail-closed validator matrix.
 *
 * CROSS-RUNTIME PARITY: the sample fixture and the pinned canonical literal
 * below are SHARED with the Go test — the same SHA-256 hex (of the bytes
 * "test"), the same layout and the same canonical bytes must match
 * byte-identically in both runtimes. The v0.7.0 EvidenceObject metadata carries
 * NO tag/array fields (tags are a v0.6.0 PolicyRule concept with its own Go
 * model), so there is nothing to sort or deduplicate in this module — the
 * deterministic contract is the fixed canonical property order itself.
 *
 * Object bytes are DATA, never instructions: these tests never parse or execute
 * artifact content, and no object operation authorizes anything.
 */

import { describe, expect, it } from "vitest";

import {
	canonicalEvidenceObjectJSON,
	cloneEvidenceObject,
	computeObjectId,
	objectRelPath,
	objectScopeMatchesFlat,
	validateEvidenceObject,
	validateObjectScope,
} from "../evidence-object.js";
import type { EvidenceObject, MemoryScope } from "../types.js";

/** The deterministic object id — the SHA-256 hex of the sample artifact bytes
 * ("test"), shared with the Go test. */
const SAMPLE_OBJECT_ID =
	"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08";

/** The FROZEN canonical bytes of sampleEvidenceObject — the exact same literal
 * pinned in internal/core/evidence_object_test.go (Go↔TS canonical bytes must
 * match byte-identically: fixed property order, compact UTF-8, no HTML
 * escaping). */
const PINNED_CANONICAL_JSON =
	'{"objectId":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","size":4,"contentType":"application/xml","tenantId":"org-001","companyId":"acme","ruc":"20100039201","period":"202401","sourceSystem":"go-test","sourceReference":"F001-1","sourceActorId":"test-agent","sourceActorKind":"agent","storedBy":"test-agent","storedAt":"2026-01-15T12:00:00.000Z","relPath":"objects/9f/86/9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"}';

/** The fixed canonical property order — exactly the interface/struct order that
 * both runtimes serialize. */
const CANONICAL_PROPERTY_ORDER = [
	"objectId",
	"sha256",
	"size",
	"contentType",
	"tenantId",
	"companyId",
	"ruc",
	"period",
	"sourceSystem",
	"sourceReference",
	"sourceActorId",
	"sourceActorKind",
	"storedBy",
	"storedAt",
	"relPath",
] as const;

/** sampleEvidenceObject returns a deterministic EvidenceObject exercising every
 * field — the same fixture the Go test builds. */
function sampleEvidenceObject(): EvidenceObject {
	return {
		objectId: SAMPLE_OBJECT_ID,
		sha256: SAMPLE_OBJECT_ID,
		size: 4,
		contentType: "application/xml",
		tenantId: "org-001",
		companyId: "acme",
		ruc: "20100039201",
		period: "202401",
		sourceSystem: "go-test",
		sourceReference: "F001-1",
		sourceActorId: "test-agent",
		sourceActorKind: "agent",
		storedBy: "test-agent",
		storedAt: "2026-01-15T12:00:00.000Z",
		relPath: objectRelPath(SAMPLE_OBJECT_ID),
	};
}

describe("computeObjectId — deterministic content address", () => {
	it("pins the SHA-256 hex of the sample bytes (shared with Go)", () => {
		expect(computeObjectId(Buffer.from("test"))).toBe(SAMPLE_OBJECT_ID);
	});

	it("collides identical bytes and separates any bit difference", () => {
		const a = computeObjectId(Buffer.from("test"));
		const b = computeObjectId(Buffer.from("test"));
		const c = computeObjectId(Buffer.from("Test"));
		expect(a).toBe(b);
		expect(c).not.toBe(a);
	});

	it("is always exactly 64 lowercase hex digits", () => {
		for (const char of computeObjectId(Buffer.from("test"))) {
			expect(
				(char >= "0" && char <= "9") ||
					(char >= "a" && char <= "f"),
			).toBe(true);
		}
		expect(computeObjectId(Buffer.from("test"))).toMatch(/^[0-9a-f]{64}$/);
	});
});

describe("objectRelPath — deterministic content-addressed layout", () => {
	it("derives objects/<sha[0:2]>/<sha[2:4]>/<sha> from the identity", () => {
		expect(objectRelPath(SAMPLE_OBJECT_ID)).toBe(
			`objects/9f/86/${SAMPLE_OBJECT_ID}`,
		);
	});

	it("keeps ids too short to shard flat", () => {
		expect(objectRelPath("ab")).toBe("objects/ab");
	});
});

describe("canonicalEvidenceObjectJSON — pinned canonical bytes", () => {
	it("serializes the sample byte-identically to the frozen Go↔TS literal", () => {
		expect(canonicalEvidenceObjectJSON(sampleEvidenceObject())).toBe(
			PINNED_CANONICAL_JSON,
		);
	});

	it("is deterministic across calls (same object, same bytes)", () => {
		const obj = sampleEvidenceObject();
		expect(canonicalEvidenceObjectJSON(obj)).toBe(
			canonicalEvidenceObjectJSON(obj),
		);
	});

	it("uses the FIXED property order — the interface order, every key present", () => {
		const parsed = JSON.parse(
			canonicalEvidenceObjectJSON(sampleEvidenceObject()),
		) as Record<string, unknown>;
		expect(Object.keys(parsed)).toEqual([...CANONICAL_PROPERTY_ORDER]);
	});

	it("never HTML-escapes <, > or & (parity with Go)", () => {
		const obj: EvidenceObject = {
			...sampleEvidenceObject(),
			contentType: "application/xml; q=<&>",
		};
		expect(canonicalEvidenceObjectJSON(obj)).toContain(
			'"contentType":"application/xml; q=<&>"',
		);
	});
});

describe("cloneEvidenceObject — clone isolation", () => {
	it("returns an equal but distinct object", () => {
		const original = sampleEvidenceObject();
		const clone = cloneEvidenceObject(original);
		expect(clone).toEqual(original);
		expect(clone).not.toBe(original);
	});

	it("mutating the clone never touches the stored original", () => {
		const original = sampleEvidenceObject();
		const clone = cloneEvidenceObject(original);
		clone.objectId = "not-hex";
		clone.sourceReference = "F001-EDITED";
		expect(original.objectId).toBe(SAMPLE_OBJECT_ID);
		expect(original.sourceReference).toBe("F001-1");
		// The clone is fully independent: its corruption fails validation while
		// the original stays valid.
		expect(validateEvidenceObject(clone)).toMatch(/^INVALID_OBJECT_ID/);
		expect(validateEvidenceObject(original)).toBeNull();
	});
});

describe("validateEvidenceObject — valid metadata and fail-closed matrix", () => {
	const sample = sampleEvidenceObject();

	it("accepts the valid sample", () => {
		expect(validateEvidenceObject(sample)).toBeNull();
	});

	it.each([
		{
			name: "non-object input (null)",
			value: null,
			code: "INVALID_OBJECT",
		},
		{
			name: "non-object input (string)",
			value: "not-an-object",
			code: "INVALID_OBJECT",
		},
		{
			name: "bad object id",
			value: { ...sample, objectId: "not-hex" },
			code: "INVALID_OBJECT_ID",
		},
		{
			name: "sha256 differs",
			value: { ...sample, sha256: "0".repeat(64) },
			code: "INVALID_OBJECT_SHA256",
		},
		{
			name: "negative size",
			value: { ...sample, size: -1 },
			code: "INVALID_OBJECT_SIZE",
		},
		{
			name: "non-number size",
			value: { ...sample, size: "4" } as unknown as EvidenceObject,
			code: "INVALID_OBJECT_SIZE",
		},
		{
			name: "invalid ruc",
			value: { ...sample, ruc: "123" },
			code: "INVALID_RUC",
		},
		{
			name: "bad period",
			value: { ...sample, period: "2024" },
			code: "INVALID_PERIOD",
		},
		{
			name: "missing storedBy",
			value: { ...sample, storedBy: "  " },
			code: "INVALID_OBJECT_STORED_BY",
		},
		{
			name: "relPath mismatch",
			value: { ...sample, relPath: "objects/evil" },
			code: "INVALID_OBJECT_REL_PATH",
		},
	])("fails closed on $name", ({ value, code }) => {
		const error = validateEvidenceObject(value);
		expect(error?.startsWith(code)).toBe(true);
	});
});

describe("validateObjectScope — tenant artifact discipline", () => {
	const companyScope: MemoryScope = {
		kind: "company",
		organizationId: "org-001",
		companyId: "acme",
		ruc: "20100039201",
		period: "202401",
	};

	it("accepts an exact company scope", () => {
		expect(validateObjectScope(companyScope)).toBeNull();
	});

	it("rejects an institutional scope (documented deferral, never a fallback)", () => {
		expect(validateObjectScope({ kind: "institutional" })).toMatch(
			/^INVALID_OBJECT_SCOPE/,
		);
	});

	it.each([
		{
			name: "empty organizationId",
			scope: { ...companyScope, organizationId: " " },
			code: "INVALID_SCOPE",
		},
		{
			name: "empty companyId",
			scope: { ...companyScope, companyId: " " },
			code: "INVALID_SCOPE",
		},
		{
			name: "malformed ruc",
			scope: { ...companyScope, ruc: "123" },
			code: "INVALID_RUC",
		},
		{
			name: "malformed period",
			scope: { ...companyScope, period: "2024" },
			code: "INVALID_PERIOD",
		},
	])("fails closed on $name", ({ scope, code }) => {
		const error = validateObjectScope(scope);
		expect(error?.startsWith(code)).toBe(true);
	});
});

describe("objectScopeMatchesFlat — exact scope tuple equality", () => {
	const sample = sampleEvidenceObject();
	const exactScope: MemoryScope = {
		kind: "company",
		organizationId: "org-001",
		companyId: "acme",
		ruc: "20100039201",
		period: "202401",
	};

	it("matches only the exact tenant/company/RUC/period tuple", () => {
		expect(objectScopeMatchesFlat(sample, exactScope)).toBe(true);
		expect(
			objectScopeMatchesFlat(sample, { ...exactScope, ruc: "20100039202" }),
		).toBe(false);
		expect(
			objectScopeMatchesFlat(sample, { ...exactScope, period: "202402" }),
		).toBe(false);
		expect(
			objectScopeMatchesFlat(sample, { ...exactScope, companyId: "other" }),
		).toBe(false);
	});

	it("never matches an institutional scope", () => {
		expect(objectScopeMatchesFlat(sample, { kind: "institutional" })).toBe(
			false,
		);
	});
});
