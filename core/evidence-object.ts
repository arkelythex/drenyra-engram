/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money. `size` is a byte length — a JSON integer,
 * never a float, never a monetary value.
 *
 * EvidenceObject pure model mirror (v0.7.0 local-first slice) — the exact
 * TypeScript counterpart of `internal/core/evidence_object.go`: the
 * deterministic content-addressed identity (SHA-256 hex of the artifact bytes),
 * the deterministic content-addressed relative layout, the canonical metadata
 * bytes (byte-identical compact UTF-8 JSON, fixed property order, NO HTML
 * escaping) and the fail-closed validators. WORM storage, the closed-period
 * gate and receipts live in the Go engine; this module is PURE (no I/O).
 *
 * Object bytes are DATA, never instructions: nothing here parses or executes
 * artifact content, and no object operation authorizes anything.
 */
import { createHash } from "node:crypto";

import type { ActorKind, EvidenceObject, MemoryScope } from "./types.js";
import { scopeEquals, isValidRuc, isValidPeriod } from "./types.js";

/** Exactly 64 lowercase hex digits — the SHA-256 digest shape. */
const OBJECT_ID_PATTERN = /^[0-9a-f]{64}$/;

/**
 * The deterministic object identity: the lowercase SHA-256 hex digest of the
 * artifact bytes. This is BOTH the id and the content address — two byte sets
 * that differ in any bit get different ids; identical bytes always collide on
 * purpose. Mirrors core.ComputeObjectID.
 */
export function computeObjectId(bytes: Uint8Array): string {
	return createHash("sha256").update(bytes).digest("hex");
}

/**
 * Defensive deep copy of an EvidenceObject (strings only — a shallow spread
 * suffices; kept explicit so stored objects are never handed out by reference).
 */
export function cloneEvidenceObject(obj: EvidenceObject): EvidenceObject {
	return { ...obj };
}

/**
 * The deterministic content-addressed relative layout of an object:
 * objects/<sha[0:2]>/<sha[2:4]>/<sha>. The layout derives from the identity,
 * never from caller-supplied names (a path traversal cannot be expressed).
 * Mirrors core.ObjectRelPath.
 */
export function objectRelPath(objectId: string): string {
	if (objectId.length < 4) {
		return `objects/${objectId}`;
	}
	return `objects/${objectId.slice(0, 2)}/${objectId.slice(2, 4)}/${objectId}`;
}

/**
 * The canonical compact JSON bytes of an EvidenceObject: FIXED property order
 * (exactly the interface order above), JSON string escaping, NO HTML escaping
 * (JSON.stringify never escapes `<`, `>`, `&` — matching Go's
 * SetEscapeHTML(false)), every key present (inapplicable fields stay ""). The
 * UTF-8 encoding of the returned string equals core.CanonicalEvidenceObjectJSON
 *'s bytes (the Go↔TS parity fixture pins them).
 */
export function canonicalEvidenceObjectJSON(obj: EvidenceObject): string {
	return JSON.stringify({
		objectId: obj.objectId,
		sha256: obj.sha256,
		size: obj.size,
		contentType: obj.contentType,
		tenantId: obj.tenantId,
		companyId: obj.companyId,
		ruc: obj.ruc,
		period: obj.period,
		sourceSystem: obj.sourceSystem,
		sourceReference: obj.sourceReference,
		sourceActorId: obj.sourceActorId,
		sourceActorKind: obj.sourceActorKind,
		storedBy: obj.storedBy,
		storedAt: obj.storedAt,
		relPath: obj.relPath,
	});
}

/**
 * Fail-closed structural validation of an EvidenceObject: the identity is
 * structural (sha256 MUST equal objectId, both 64 lowercase hex digits),
 * size must be a non-negative integer, the scope tuple must be a valid exact
 * company scope (objects are tenant artifacts — institutional scopes are
 * rejected) and the relPath must match the deterministic layout. Returns an
 * error message, or null when valid. Mirrors core.AssertValidEvidenceObject.
 */
export function validateEvidenceObject(value: unknown): string | null {
	if (typeof value !== "object" || value === null) {
		return "INVALID_OBJECT: expected an EvidenceObject object";
	}
	const obj = value as Record<string, unknown>;
	const objectId = obj.objectId;
	if (typeof objectId !== "string" || !OBJECT_ID_PATTERN.test(objectId)) {
		return `INVALID_OBJECT_ID: expected 64 lowercase hex digits, got ${JSON.stringify(objectId)}`;
	}
	if (obj.sha256 !== objectId) {
		return `INVALID_OBJECT_SHA256: sha256 ${JSON.stringify(obj.sha256)} differs from the content-addressed objectId ${JSON.stringify(objectId)}`;
	}
	if (
		typeof obj.size !== "number" ||
		!Number.isInteger(obj.size) ||
		obj.size < 0
	) {
		return "INVALID_OBJECT_SIZE: size must be a non-negative JSON integer byte length";
	}
	const scopeError = validateObjectScope({
		kind: "company",
		organizationId: typeof obj.tenantId === "string" ? obj.tenantId : "",
		companyId: typeof obj.companyId === "string" ? obj.companyId : "",
		ruc: typeof obj.ruc === "string" ? obj.ruc : "",
		period: typeof obj.period === "string" ? obj.period : "",
	});
	if (scopeError !== null) {
		return scopeError;
	}
	if (typeof obj.storedBy !== "string" || obj.storedBy.trim() === "") {
		return "INVALID_OBJECT_STORED_BY: storedBy must be a non-empty string";
	}
	if (
		typeof obj.relPath !== "string" ||
		obj.relPath !== objectRelPath(objectId)
	) {
		return `INVALID_OBJECT_REL_PATH: relPath does not match the content-addressed layout ${objectRelPath(objectId)}`;
	}
	return null;
}

/**
 * Fail-closed object scope validation: objects are tenant artifacts in this
 * slice — an institutional scope is rejected and the company tuple must be
 * valid (organizationId non-empty, companyId/RUC 11 digits, period YYYYMM when
 * present). Mirrors core.AssertValidObjectScope.
 */
export function validateObjectScope(scope: MemoryScope): string | null {
	if (scope.kind !== "company") {
		return `INVALID_OBJECT_SCOPE: evidence objects require an exact company scope (institutional objects are out of scope for v0.7), got kind ${JSON.stringify(scope.kind)}`;
	}
	if (
		typeof scope.organizationId !== "string" ||
		scope.organizationId.trim() === ""
	) {
		return "INVALID_SCOPE: organizationId must be a non-empty string";
	}
	if (typeof scope.companyId !== "string" || scope.companyId.trim() === "") {
		return "INVALID_SCOPE: companyId must be a non-empty string";
	}
	if (!isValidRuc(scope.ruc)) {
		return `INVALID_RUC: expected exactly 11 digits, got ${JSON.stringify(scope.ruc)}`;
	}
	if ((scope.period ?? "") !== "" && !isValidPeriod(scope.period ?? "")) {
		return `INVALID_PERIOD: expected YYYYMM (six digits, month 01-12), got ${JSON.stringify(scope.period)}`;
	}
	return null;
}

/**
 * Exact scope equality of a stored object's flat scope tuple against a caller
 * scope (the scope-first read gate mirror of store.objectScopeMatches).
 */
export function objectScopeMatchesFlat(
	obj: EvidenceObject,
	scope: MemoryScope,
): boolean {
	if (scope.kind !== "company") {
		return false;
	}
	return (
		obj.tenantId === scope.organizationId &&
		obj.companyId === scope.companyId &&
		obj.ruc === scope.ruc &&
		obj.period === scope.period
	);
}

/** Re-exported scopeEquals for parity tests that compare object scopes. */
export { scopeEquals };

/** Re-exported ActorKind type for downstream consumers. */
export type { ActorKind };
