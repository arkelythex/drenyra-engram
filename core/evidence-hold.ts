/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * EvidenceHold pure model mirror (v0.8.0 object-level legal holds, batch 3) —
 * the exact TypeScript counterpart of `internal/core/hold.go`
 * (docs/architecture/evidence-lifecycle-v0.8.md §3.2/§7): the ONE-WAY
 * placed→lifted record, the fail-closed structural validator, the PURE
 * active-blocking-hold helper and the canonical metadata bytes (byte-identical
 * compact UTF-8 JSON, fixed property order, NO HTML escaping). Persistence, the
 * authenticated place/lift gate (the extended evidence-lifecycle policy with the
 * place_hold/lift_hold actions), (tenant, requestId) idempotency, the
 * hold_placed/hold_lifted receipts on the evidence_object chain and the
 * scope-first blocking query live in the Go engine; this module is PURE (no
 * I/O).
 *
 * A hold BLOCKS purge only while ACTIVE (not lifted) AND its kind is in the
 * deployment's blocking set (the frozen default is {legal, audit, dispute,
 * fiscalization}; an EMPTY blocking set blocks NOTHING — fail closed).
 */
import type { EvidenceHold, HoldKind } from "./types.js";
import { HOLD_KINDS } from "./types.js";

/**
 * Defensive deep copy of an EvidenceHold (strings only — a shallow spread
 * suffices; kept explicit so stored holds are never handed out by reference).
 */
export function cloneEvidenceHold(hold: EvidenceHold): EvidenceHold {
	return { ...hold };
}

/**
 * The closed hold-kind set as an array (mirrors core hold kinds): a helper for
 * callers that need the canonical token list at runtime.
 */
export const HOLD_KIND_TOKENS: readonly string[] = HOLD_KINDS;

/** Reports whether k is a closed hold-kind token. Mirrors core.IsValidHoldKind. */
export function isValidHoldKind(k: unknown): k is HoldKind {
	return typeof k === "string" && (HOLD_KINDS as readonly string[]).includes(k);
}

/**
 * The canonical compact JSON bytes of an EvidenceHold: FIXED property order
 * (exactly the interface order above), JSON string escaping, NO HTML escaping
 * (JSON.stringify never escapes `<`, `>`, `&` — matching Go's
 * SetEscapeHTML(false)), inapplicable optional fields stay "" (the Go struct
 * omits empty omitempty fields, so the wire form is the canonical form). The
 * UTF-8 encoding of the returned string equals core.CanonicalEvidenceHoldJSON's
 * bytes (the Go↔TS parity fixture pins them).
 */
export function canonicalEvidenceHoldJSON(hold: EvidenceHold): string {
	const out: Record<string, string> = {
		holdId: hold.holdId,
		objectId: hold.objectId,
		tenantId: hold.tenantId,
		companyId: hold.companyId,
		ruc: hold.ruc,
	};
	// The Go mirror carries omitempty on the optional fields — the canonical
	// bytes OMIT them when empty (never a null, never a ""). The insertion
	// order below is the FIXED struct order (period after ruc, the lift fields
	// after placedBy).
	if (hold.period !== "") {
		out.period = hold.period;
	}
	out.kind = hold.kind;
	out.reason = hold.reason;
	out.ownerSubjectId = hold.ownerSubjectId;
	out.placedAt = hold.placedAt;
	out.placedBy = hold.placedBy;
	if (hold.liftedAt !== undefined && hold.liftedAt !== "") {
		out.liftedAt = hold.liftedAt;
	}
	if (hold.liftedBy !== undefined && hold.liftedBy !== "") {
		out.liftedBy = hold.liftedBy;
	}
	if (hold.liftReason !== undefined && hold.liftReason !== "") {
		out.liftReason = hold.liftReason;
	}
	return JSON.stringify(out);
}

const OBJECT_ID_PATTERN = /^[0-9a-f]{64}$/;
const HOLD_ID_PATTERN =
	/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/** Exactly 11 digits — the Peruvian RUC shape this slice validates. */
function isRuc(ruc: string): boolean {
	return /^[0-9]{11}$/.test(ruc);
}

/** YYYYMM with month 01-12. */
function isPeriod(period: string): boolean {
	if (!/^[0-9]{6}$/.test(period)) {
		return false;
	}
	const month = Number(period.slice(4, 6));
	return month >= 1 && month <= 12;
}

function isParseableDate(value: string): boolean {
	return value === "" || !Number.isNaN(Date.parse(value));
}

/**
 * Fail-closed structural validation of an EvidenceHold: the hold id is a UUID
 * (empty allowed on wire input), the object id is the 64-lowercase-hex content
 * address, the scope is an EXACT company scope (objects are tenant artifacts —
 * institutional scopes are rejected), the kind is a closed hold-kind token,
 * reason/owner/placedBy are non-empty, placedAt/liftedAt are parseable when
 * present, and the lift fields are CONSISTENT (all empty while placed, all set
 * together on lift — a partially lifted row is invalid metadata). Returns an
 * error message, or null when valid. Mirrors core.AssertValidEvidenceHold.
 */
export function validateEvidenceHold(value: unknown): string | null {
	if (typeof value !== "object" || value === null) {
		return "INVALID_HOLD: expected an EvidenceHold object";
	}
	const hold = value as Record<string, unknown>;
	const holdId = typeof hold.holdId === "string" ? hold.holdId : "";
	const objectId = typeof hold.objectId === "string" ? hold.objectId : "";
	const tenantId = typeof hold.tenantId === "string" ? hold.tenantId : "";
	const companyId = typeof hold.companyId === "string" ? hold.companyId : "";
	const ruc = typeof hold.ruc === "string" ? hold.ruc : "";
	const period = typeof hold.period === "string" ? hold.period : "";
	const kind = typeof hold.kind === "string" ? hold.kind : "";
	const reason = typeof hold.reason === "string" ? hold.reason : "";
	const ownerSubjectId =
		typeof hold.ownerSubjectId === "string" ? hold.ownerSubjectId : "";
	const placedAt = typeof hold.placedAt === "string" ? hold.placedAt : "";
	const placedBy = typeof hold.placedBy === "string" ? hold.placedBy : "";
	const liftedAt = typeof hold.liftedAt === "string" ? hold.liftedAt : "";
	const liftedBy = typeof hold.liftedBy === "string" ? hold.liftedBy : "";
	const liftReason =
		typeof hold.liftReason === "string" ? hold.liftReason : "";

	if (holdId !== "" && !HOLD_ID_PATTERN.test(holdId)) {
		return `INVALID_HOLD_ID: holdId must be a UUID, got ${JSON.stringify(holdId)}`;
	}
	if (!OBJECT_ID_PATTERN.test(objectId)) {
		return `INVALID_HOLD_OBJECT_ID: objectId must be 64 lowercase hex digits, got ${JSON.stringify(objectId)}`;
	}
	if (tenantId === "" || companyId === "" || !isRuc(ruc)) {
		return "INVALID_HOLD_SCOPE: an object-level hold requires the exact company scope (tenantId, companyId, 11-digit ruc)";
	}
	if (period !== "" && !isPeriod(period)) {
		return `INVALID_HOLD_SCOPE: period must be YYYYMM with month 01-12, got ${JSON.stringify(period)}`;
	}
	if (!isValidHoldKind(kind)) {
		return `INVALID_HOLD_KIND: kind must be one of legal|audit|dispute|fiscalization|other, got ${JSON.stringify(kind)}`;
	}
	if (reason.trim() === "") {
		return "REASON_REQUIRED: a hold record requires a non-empty reason";
	}
	if (ownerSubjectId.trim() === "") {
		return "INVALID_HOLD_OWNER: ownerSubjectId must be a non-empty string";
	}
	if (placedBy.trim() === "") {
		return "INVALID_HOLD_PLACED_BY: placedBy must be a non-empty string";
	}
	if (!isParseableDate(placedAt)) {
		return `INVALID_HOLD_PLACED_AT: placedAt must be a parseable date string, got ${JSON.stringify(placedAt)}`;
	}
	const hasLiftedAt = liftedAt !== "";
	const hasLiftedBy = liftedBy !== "";
	const hasLiftReason = liftReason !== "";
	if (hasLiftedAt !== hasLiftedBy || hasLiftedAt !== hasLiftReason) {
		return "INVALID_HOLD_LIFT: liftedAt/liftedBy/liftReason must be all empty (placed) or all set (lifted)";
	}
	if (hasLiftedAt) {
		if (!isParseableDate(liftedAt)) {
			return `INVALID_HOLD_LIFTED_AT: liftedAt must be a parseable date string, got ${JSON.stringify(liftedAt)}`;
		}
		if (liftReason.trim() === "") {
			return "REASON_REQUIRED: a lifted hold requires a non-empty lift reason";
		}
	}
	return null;
}

/**
 * The PURE active-blocking-hold helper (design §7): a hold BLOCKS purge while it
 * is ACTIVE (not lifted) AND its kind is in the deployment's blocking set. An
 * EMPTY blocking set blocks NOTHING (fail closed — a caller that does not know
 * its blocking policy cannot claim a block); the frozen policy default blocking
 * set is {legal, audit, dispute, fiscalization}, so an 'other' hold never blocks
 * unless a deployment explicitly added 'other' to blocking_hold_kinds. A lifted
 * hold never blocks. Mirrors core.HasActiveBlockingHold.
 */
export function hasActiveBlockingHold(
	hold: EvidenceHold,
	blockingKinds: readonly string[],
): boolean {
	if (hold.liftedAt !== "" && hold.liftedAt !== undefined) {
		return false;
	}
	return blockingKinds.includes(hold.kind);
}
