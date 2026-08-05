/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Core memory model v2 — the unit of institutional accounting memory.
 * Implements contracts/memory.md, contracts/scope.md, contracts/lifecycle.md and
 * contracts/provenance.md (frozen-for-0.2 semantics). Mirrors
 * internal/core/types.go semantically.
 *
 * Contract-to-code mapping:
 * - `status` is the contract's lifecycle state (v2 approval-gated machine).
 * - `kind` is the accounting nature (`fact` … `summary`).
 * - `fiscalEffect` drives the human-approval gate: != `none` → `pending_review`.
 * - `validity` is the contract's vigencia (effective/expiry window).
 * - `identity.topicKey` is the contract's topic_key (the upsert target).
 * - Scope equality is exact: two memories differing only in scope are
 *   different memories (scope.md rule 5), and `period` participates in that
 *   equality. Single `YYYYMM` periods only in this slice; ranges are future.
 */

// ──────────────────────────────────────────────
// Scope — structural tenant isolation
// ──────────────────────────────────────────────

/**
 * Fiscal scope of a memory (contracts/scope.md).
 *
 * `company` — scoped to an organization/company/RUC/period; invisible to
 * queries from any other company (structural isolation, not a post-filter).
 *
 * `institutional` — explicitly declared cross-company knowledge; only surfaced
 * when the query scope is institutional or the caller explicitly asks for it.
 */
export type MemoryScope =
	| {
			kind: "company";
			organizationId: string;
			companyId: string;
			/** Peruvian RUC — exactly 11 digits in this slice (checksum: future). */
			ruc: string;
			/** Fiscal period `YYYYMM` when present (single period; ranges: future). */
			period?: string;
	  }
	| { kind: "institutional" };

// ──────────────────────────────────────────────
// Identity
// ──────────────────────────────────────────────

/** Canonical identity: a stable memory id plus the upsert topic key. */
export interface MemoryIdentity {
	id: string;
	topicKey: string;
}

// ──────────────────────────────────────────────
// v2 vocabulary
// ──────────────────────────────────────────────

/** Accounting nature of a memory (v2). Replaces the generic v1 `type`. */
export type MemoryKind =
	| "fact"
	| "evidence"
	| "decision"
	| "rule"
	| "exception"
	| "control"
	| "obligation"
	| "summary";

export const MEMORY_KINDS: readonly MemoryKind[] = [
	"fact",
	"evidence",
	"decision",
	"rule",
	"exception",
	"control",
	"obligation",
	"summary",
];

/** Lifecycle state (v2). Replaces v1 AuthorityStatus. */
export type MemoryStatus =
	| "active"
	| "pending_review"
	| "approved"
	| "rejected"
	| "superseded"
	| "voided";

export const MEMORY_STATUSES: readonly MemoryStatus[] = [
	"active",
	"pending_review",
	"approved",
	"rejected",
	"superseded",
	"voided",
];

/** Fiscal impact classifier; non-none triggers the human-approval gate. */
export type FiscalEffect =
	| "none"
	| "journal_entry"
	| "declaration"
	| "closing"
	| "adjustment"
	| "reclassification"
	| "approval"
	| "sunat_filing";

export const FISCAL_EFFECTS: readonly FiscalEffect[] = [
	"none",
	"journal_entry",
	"declaration",
	"closing",
	"adjustment",
	"reclassification",
	"approval",
	"sunat_filing",
];

/** Actor kind: who originated or decided a memory. */
export type ActorKind = "human" | "agent" | "system";

// ──────────────────────────────────────────────
// Content / source / validity
// ──────────────────────────────────────────────

/** Structured content — the canonical `What / Why / Where / Learned` shape. */
export interface MemoryContent {
	what: string;
	why: string;
	where: string;
	learned: string;
}

/** Structured provenance (v2). Replaces the flat v1 provenance. */
export interface MemorySource {
	/** REQUIRED — which system produced the event (e.g. "drenyra-core", "sire"). */
	system: string;
	/** Optional external reference (e.g. "F001-948", "AJ-2026-07-019"). */
	reference?: string;
	/** Who (user/agent/system id); REQUIRED for human actors. */
	actorId?: string;
	/** REQUIRED — human | agent | system. */
	actorKind: ActorKind;
	/** Agent model, when the actor is an agent. */
	model?: string;
	/** Session identifier (agent continuity). */
	session?: string;
}

/** Vigencia: effective/expiry window. Expired memories surface as stale. */
export interface MemoryValidity {
	effectiveAt?: string;
	expiresAt?: string;
}

// ──────────────────────────────────────────────
// Relations (17) / writes
// ──────────────────────────────────────────────

/** Relation vocabulary (v2): 6 legacy + 11 accounting-evidence relations. */
export type MemoryRelation =
	| "related"
	| "compatible"
	| "scoped"
	| "conflicts_with"
	| "supersedes"
	| "not_conflict"
	| "supports"
	| "contradicts"
	| "explains"
	| "derived_from"
	| "posted_as"
	| "reconciles"
	| "reverses"
	| "requires"
	| "violates"
	| "approved_by"
	| "rejected_by";

/** Write outcome. `conflict` and `unknown` are documented fallback outcomes. */
export type MemoryWriteOutcome = "created" | "updated" | "conflict" | "unknown";

// ──────────────────────────────────────────────
// AccountingMemory / results
// ──────────────────────────────────────────────

/**
 * A stored institutional accounting memory (v2). Kind, scope, content,
 * timestamps, source and contentHash are immutable once created; status is the
 * single field the lifecycle machine may transition.
 */
export interface AccountingMemory {
	identity: MemoryIdentity;
	title: string;
	kind: MemoryKind;
	scope: MemoryScope;
	content: MemoryContent;
	status: MemoryStatus;
	/** Non-none triggers the mandatory human-approval gate. */
	fiscalEffect: FiscalEffect;
	/** When the event happened ACCOUNTING-wise (the period it belongs to). */
	effectiveAt: string;
	/** When it entered the system — automatic, immutable. */
	recordedAt: string;
	/** When it was detected (optional). */
	observedAt?: string;
	source: MemorySource;
	validity?: MemoryValidity;
	/** Evidence objects backing this memory (XML/PDF/CDR); grows via links. */
	evidenceRefs?: string[];
	/** Policy/rule paths applied; grows via links. */
	ruleRefs?: string[];
	/** Optional 0..1 probability (never money). */
	confidence?: number;
	/** Optional monetary threshold in int64 cents (never float). */
	materiality?: bigint;
	/** Canonical SHA-256 (hex) of the immutable content. */
	contentHash: string;
	/** Reference to the Ed25519 receipt issued by the Drenyra ecosystem. */
	receiptId?: string;
	/** Id of the memory this one replaces (set on the successor). */
	supersedesId?: string;
	/** 1-based revision within the (topicKey, scope) chain; JSON integer. */
	revision: number;
}

/** Result of a save (upsert) operation. */
export interface MemoryWriteResult {
	memory: AccountingMemory;
	outcome: MemoryWriteOutcome;
}

/** Input for saving/upserting under a topic key + exact scope. */
export interface SaveMemoryInput {
	topicKey: string;
	title: string;
	kind: MemoryKind;
	scope: MemoryScope;
	content: MemoryContent;
	/** Drives the approval gate: != none → pending_review. */
	fiscalEffect: FiscalEffect;
	/** When it happened accounting-wise; required when fiscalEffect != none. */
	effectiveAt?: string;
	observedAt?: string;
	source: MemorySource;
	validity?: MemoryValidity;
	ruleRefs?: string[];
	confidence?: number;
	materiality?: bigint;
	receiptId?: string;
}

/** A recorded relation between two memories. */
export interface MemoryRelationRecord {
	fromId: string;
	toId: string;
	relation: MemoryRelation;
	actor?: string;
	timestamp?: string;
}

/** One entry of the lifecycle audit trail (v2: actor kind included). */
export interface StatusTransitionRecord {
	memoryId: string;
	from: MemoryStatus;
	to: MemoryStatus;
	actor: string;
	actorKind: ActorKind;
	timestamp: string;
}

// ──────────────────────────────────────────────
// Scope helpers
// ──────────────────────────────────────────────

/**
 * Exact scope equality. Period participates: `undefined === undefined` but a
 * perioded scope never equals an unperioded one (scope is part of identity).
 */
export function scopeEquals(a: MemoryScope, b: MemoryScope): boolean {
	if (a.kind === "institutional" && b.kind === "institutional") return true;
	if (a.kind !== "company" || b.kind !== "company") return false;
	return (
		a.organizationId === b.organizationId &&
		a.companyId === b.companyId &&
		a.ruc === b.ruc &&
		a.period === b.period
	);
}

/** Canonical serialization of a scope, used to key upsert chains. */
export function scopeKey(scope: MemoryScope): string {
	if (scope.kind === "institutional") return "institutional";
	return `company\u0000${scope.organizationId}\u0000${scope.companyId}\u0000${scope.ruc}\u0000${scope.period ?? ""}`;
}

/** Defensive copy — stored memories are never handed out by reference. */
export function cloneMemory(memory: AccountingMemory): AccountingMemory {
	return {
		identity: { ...memory.identity },
		title: memory.title,
		kind: memory.kind,
		scope: cloneScope(memory.scope),
		content: { ...memory.content },
		status: memory.status,
		fiscalEffect: memory.fiscalEffect,
		effectiveAt: memory.effectiveAt,
		recordedAt: memory.recordedAt,
		...(memory.observedAt === undefined
			? {}
			: { observedAt: memory.observedAt }),
		source: { ...memory.source },
		...(memory.validity === undefined
			? {}
			: { validity: { ...memory.validity } }),
		...(memory.evidenceRefs === undefined
			? {}
			: { evidenceRefs: [...memory.evidenceRefs] }),
		...(memory.ruleRefs === undefined
			? {}
			: { ruleRefs: [...memory.ruleRefs] }),
		...(memory.confidence === undefined
			? {}
			: { confidence: memory.confidence }),
		...(memory.materiality === undefined
			? {}
			: { materiality: memory.materiality }),
		contentHash: memory.contentHash,
		...(memory.receiptId === undefined ? {} : { receiptId: memory.receiptId }),
		...(memory.supersedesId === undefined
			? {}
			: { supersedesId: memory.supersedesId }),
		revision: memory.revision,
	};
}

function cloneScope(scope: MemoryScope): MemoryScope {
	if (scope.kind === "institutional") return { kind: "institutional" };
	const { kind, organizationId, companyId, ruc, period } = scope;
	return period === undefined
		? { kind, organizationId, companyId, ruc }
		: { kind, organizationId, companyId, ruc, period };
}

// ──────────────────────────────────────────────
// Validation (scope.md: RUC 11 digits, period YYYYMM; v2 model rules)
// ──────────────────────────────────────────────

const RUC_PATTERN = /^\d{11}$/;
const PERIOD_PATTERN = /^\d{6}$/;

/** A Peruvian RUC is exactly 11 digits (checksum validation: future slice). */
export function isValidRuc(ruc: string): boolean {
	return RUC_PATTERN.test(ruc);
}

/** A fiscal period is `YYYYMM` (six digits, month 01-12) in this slice. */
export function isValidPeriod(period: string): boolean {
	if (!PERIOD_PATTERN.test(period)) return false;
	const month = Number(period.slice(4, 6));
	return month >= 1 && month <= 12;
}

/** Fail-closed scope validation; throws on malformed company scopes. */
export function assertValidScope(scope: MemoryScope): void {
	if (scope.kind === "institutional") return;
	if (scope.organizationId.length === 0) {
		throw new Error("INVALID_SCOPE: organizationId must be a non-empty string");
	}
	if (scope.companyId.length === 0) {
		throw new Error("INVALID_SCOPE: companyId must be a non-empty string");
	}
	if (!isValidRuc(scope.ruc)) {
		throw new Error(
			`INVALID_RUC: expected exactly 11 digits, got "${scope.ruc}"`,
		);
	}
	if (scope.period !== undefined && !isValidPeriod(scope.period)) {
		throw new Error(
			`INVALID_PERIOD: expected YYYYMM (six digits, month 01-12), got "${scope.period}"`,
		);
	}
}

/** Structured-content validation — all four fields must be non-empty strings. */
export function assertValidContent(content: MemoryContent): void {
	for (const field of ["what", "why", "where", "learned"] as const) {
		if (
			typeof content[field] !== "string" ||
			content[field].trim().length === 0
		) {
			throw new Error(
				`INVALID_CONTENT: field "${field}" must be a non-empty string`,
			);
		}
	}
}

/** Source validation (v2): system non-empty, actorKind known, human → actorId. */
export function assertValidSource(source: MemorySource): void {
	if (typeof source.system !== "string" || source.system.trim().length === 0) {
		throw new Error("INVALID_SOURCE: system must be a non-empty string");
	}
	if (
		source.actorKind !== "human" &&
		source.actorKind !== "agent" &&
		source.actorKind !== "system"
	) {
		throw new Error(
			`INVALID_SOURCE: unknown actorKind — expected human|agent|system, got "${String(source.actorKind)}"`,
		);
	}
	if (
		source.actorKind === "human" &&
		(typeof source.actorId !== "string" || source.actorId.length === 0)
	) {
		throw new Error("INVALID_SOURCE: actorId is required for human actors");
	}
}

/** Vigencia dates must parse; malformed windows fail closed at write time. */
export function assertValidValidity(
	validity: MemoryValidity | undefined,
): void {
	if (validity === undefined) return;
	if (
		validity.effectiveAt !== undefined &&
		Number.isNaN(Date.parse(validity.effectiveAt))
	) {
		throw new Error(
			"INVALID_VALIDITY: effectiveAt must be a parseable date string",
		);
	}
	if (
		validity.expiresAt !== undefined &&
		Number.isNaN(Date.parse(validity.expiresAt))
	) {
		throw new Error(
			"INVALID_VALIDITY: expiresAt must be a parseable date string",
		);
	}
}

/**
 * Canonical content hash (v2) — SHA-256 (hex) of the immutable content: scope,
 * kind, title, fiscal effect, effective date, the four content fields, and the
 * source system + actor kind. Identity, status, recordedAt and revision do NOT
 * participate. Mirrors core.ComputeContentHash.
 */
export async function computeContentHash(
	memory: AccountingMemory,
): Promise<string> {
	const canonical = [
		scopeKey(memory.scope),
		memory.kind,
		memory.title,
		memory.fiscalEffect,
		memory.effectiveAt,
		memory.content.what,
		memory.content.why,
		memory.content.where,
		memory.content.learned,
		memory.source.system,
		memory.source.actorKind,
	].join("\u0000");
	const digest = await crypto.subtle.digest(
		"SHA-256",
		new TextEncoder().encode(canonical),
	);
	return Array.from(new Uint8Array(digest))
		.map((byte) => byte.toString(16).padStart(2, "0"))
		.join("");
}

/**
 * Fail-closed full-memory validation (v2). Mirrors core.AssertValidMemory:
 * scope, content, source, kind, status, fiscal effect, timestamps, confidence
 * and materiality.
 */
export function assertValidMemory(memory: AccountingMemory): void {
	assertValidScope(memory.scope);
	assertValidContent(memory.content);
	assertValidSource(memory.source);
	if (!MEMORY_KINDS.includes(memory.kind)) {
		throw new Error(`INVALID_KIND: unknown memory kind "${memory.kind}"`);
	}
	if (!MEMORY_STATUSES.includes(memory.status)) {
		throw new Error(`INVALID_STATUS: unknown memory status "${memory.status}"`);
	}
	if (!FISCAL_EFFECTS.includes(memory.fiscalEffect)) {
		throw new Error(
			`INVALID_FISCAL_EFFECT: unknown fiscal effect "${memory.fiscalEffect}"`,
		);
	}
	if (
		typeof memory.effectiveAt !== "string" ||
		Number.isNaN(Date.parse(memory.effectiveAt))
	) {
		throw new Error(
			"INVALID_EFFECTIVE_AT: effectiveAt must be a parseable date string",
		);
	}
	if (
		memory.observedAt !== undefined &&
		Number.isNaN(Date.parse(memory.observedAt))
	) {
		throw new Error(
			"INVALID_OBSERVED_AT: observedAt must be a parseable date string",
		);
	}
	if (
		memory.confidence !== undefined &&
		(memory.confidence < 0 || memory.confidence > 1)
	) {
		throw new Error(
			`INVALID_CONFIDENCE: confidence must be in [0,1], got ${memory.confidence}`,
		);
	}
	if (memory.materiality !== undefined && memory.materiality < 0n) {
		throw new Error(
			"INVALID_MATERIALITY: materiality must be >= 0 (int64 cents)",
		);
	}
}
