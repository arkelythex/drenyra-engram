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
// v0.4.0 approval vocabulary (ADR-003 mirror)
// ──────────────────────────────────────────────

/**
 * Declared materiality classification of a memory (v0.4.0 Step 1). Set by the
 * writing agent; NULL (unset) is treated as `normal` by the approval policy.
 * The policy classifies by this level and NEVER reinterprets the `materiality`
 * threshold (frozen decision, 2026-08-05). Mirrors core.MaterialityLevel.
 */
export type MaterialityLevel = "normal" | "material" | "critical";

export const MATERIALITY_LEVELS: readonly MaterialityLevel[] = [
	"normal",
	"material",
	"critical",
];

/**
 * How a principal proved its identity (v0.4.0). `oidc` is recognized but not
 * resolvable in Step 1; `session`/`service_assertion` are opaque bearer
 * credentials resolved by SHA-256 token hash; `local_dev` is loopback-only.
 * Mirrors auth.AuthenticationMethod.
 */
export type AuthenticationMethod =
	| "oidc"
	| "session"
	| "service_assertion"
	| "local_dev";

export const AUTHENTICATION_METHODS: readonly AuthenticationMethod[] = [
	"oidc",
	"session",
	"service_assertion",
	"local_dev",
];

/** Strength of the authentication evidence (v0.4.0). Mirrors auth.AssuranceLevel. */
export type AssuranceLevel = "low" | "standard" | "strong";

export const ASSURANCE_LEVELS: readonly AssuranceLevel[] = [
	"low",
	"standard",
	"strong",
];

/**
 * Professional accounting role (v0.4.0). The ladder
 * `accountant < senior_accountant < controller` is the ONLY dominance relation;
 * tax roles are explicit and never implied. Mirrors auth.AccountingRole.
 */
export type AccountingRole =
	| "accountant"
	| "senior_accountant"
	| "controller"
	| "tax_reviewer"
	| "authorized_tax_professional";

export const ACCOUNTING_ROLES: readonly AccountingRole[] = [
	"accountant",
	"senior_accountant",
	"controller",
	"tax_reviewer",
	"authorized_tax_professional",
];

/**
 * The deliberately narrow, serializable view of a verified principal: subject,
 * membership, canonical roles, method, assurance and time. OMITS sessionId,
 * token material, cookies and unrelated claims — the only fields approval
 * events may record (ADR-003). Mirrors auth.PrincipalSnapshot.
 */
export interface PrincipalSnapshot {
	subjectId: string;
	membershipId: string;
	/** Canonicalized: sorted and deduplicated. */
	roles: AccountingRole[];
	authenticationMethod: AuthenticationMethod;
	assuranceLevel: AssuranceLevel;
	authenticatedAt: string;
}

/**
 * The authenticated, pre-verified principal used to authorize approvals
 * (ADR-003). Read-only shape; NEVER assembled from caller-declared claims. The
 * only construction path is the validating factory in `auth/principal.ts`
 * (mirror of auth.Resolver.Authenticate).
 */
export interface VerifiedApprovalPrincipal {
	readonly subjectId: string;
	readonly tenantId: string;
	readonly membershipId: string;
	readonly companyScopes: readonly string[];
	readonly roles: readonly AccountingRole[];
	readonly authenticationMethod: AuthenticationMethod;
	readonly assuranceLevel: AssuranceLevel;
	/** RFC3339 authentication timestamp. */
	readonly authenticatedAt: string;
	/** Optional session continuity id; never a token or cookie. */
	readonly sessionId?: string;
}

/**
 * Pure authorization decision of the versioned approval policy (v0.4.0).
 * Mirrors authz.Decision.
 */
export interface ApprovalAuthorizationDecision {
	allowed: boolean;
	/** Exactly `approval-policy/v0.4.0`. */
	policyVersion: string;
	/** Frozen reason code (or `AUTHORIZED` when allowed). */
	reasonCode: string;
}

/**
 * Frozen approval error codes (v0.4.0 Step 1) — the single source for the
 * HTTP/MCP mapping. Mirrors the codes in auth/errors.go.
 */
export type ApprovalErrorCode =
	| "AUTHENTICATION_REQUIRED"
	| "PRINCIPAL_INVALID"
	| "MEMBERSHIP_INACTIVE"
	| "TENANT_SCOPE_MISMATCH"
	| "COMPANY_SCOPE_DENIED"
	| "ROLE_NOT_AUTHORIZED"
	| "ASSURANCE_TOO_LOW"
	| "MATERIALITY_LIMIT_EXCEEDED"
	| "REASON_REQUIRED"
	| "MEMORY_NOT_FOUND"
	| "INVALID_TRANSITION"
	| "ENVELOPE_MISMATCH"
	| "ALREADY_DECIDED"
	| "IDEMPOTENCY_CONFLICT";

export const APPROVAL_ERROR_CODES: readonly ApprovalErrorCode[] = [
	"AUTHENTICATION_REQUIRED",
	"PRINCIPAL_INVALID",
	"MEMBERSHIP_INACTIVE",
	"TENANT_SCOPE_MISMATCH",
	"COMPANY_SCOPE_DENIED",
	"ROLE_NOT_AUTHORIZED",
	"ASSURANCE_TOO_LOW",
	"MATERIALITY_LIMIT_EXCEEDED",
	"REASON_REQUIRED",
	"MEMORY_NOT_FOUND",
	"INVALID_TRANSITION",
	"ENVELOPE_MISMATCH",
	"ALREADY_DECIDED",
	"IDEMPOTENCY_CONFLICT",
];


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
/** Vigencia: effective/expiry window. Expired memories surface as stale.
 * `source` records the vigencia provenance (frozen decision, v0.3.0):
 * "declared" (written explicitly by a v2 caller) or
 * "migrated_from_effective_at_v1" (inferred during the v1→v2 migration) — an
 * audit can distinguish a vigencia originally confirmed from one inferred. */
export interface MemoryValidity {
	effectiveAt?: string;
	expiresAt?: string;
	source?: string;
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
	/**
	 * Declared materiality classification (v0.4.0): `normal|material|critical`.
	 * NULL (unset) is treated as `normal` by the approval policy; the
	 * `materiality` threshold is never reinterpreted by policy. Not persisted
	 * until the v3 schema batch; does NOT participate in the envelope hash
	 * (frozen decision). Mirrors core.MaterialityLevel.
	 */
	materialityLevel?: MaterialityLevel;
	/** Canonical SHA-256 (hex) of the immutable content. */
	contentHash: string;
	/** SHA-256 (hex) of the DOMAIN identity (scope + topicKey + effectiveAt + source reference). */
	identityHash?: string;
	/** SHA-256 (hex) of everything signable (identity + content + effect + source + refs + timestamps + supersession). */
	envelopeHash?: string;
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
	/** Declared materiality classification (normal | material | critical); NULL is
	 * treated as normal by the approval policy. Mirrors core.SaveInput. */
	materialityLevel?: MaterialityLevel;
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
		...(memory.materialityLevel === undefined
			? {}
			: { materialityLevel: memory.materialityLevel }),
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
 * Internal SHA-256 hex helper (WebCrypto).
 */
async function sha256Hex(canonical: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(canonical));
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");
}

/**
 * Identity hash (v2) — SHA-256 of the DOMAIN identity: scope (tenant/company/
 * period) + topicKey + effectiveAt + source reference. Mirrors
 * core.ComputeIdentityHash.
 */
export async function computeIdentityHash(memory: AccountingMemory): Promise<string> {
  const canonical = [
    scopeKey(memory.scope),
    memory.identity.topicKey,
    memory.effectiveAt,
    memory.source.reference ?? "",
  ].join("\u0000");
  return sha256Hex(canonical);
}

/**
 * Envelope hash (v2) — SHA-256 of EVERYTHING signable/verifiable: identity +
 * content hash + fiscal effect + status + source + evidence/rule refs +
 * timestamps + supersession + receipt. Mirrors core.ComputeEnvelopeHash.
 */
/** Canonical ref ordering: evidenceRefs/ruleRefs are SETS — order is not
 * semantically meaningful, so the envelope hash must not depend on it
 * (frozen decision, v0.3.0). Same algorithm as core.canonicalRefs (Go). */
function canonicalRefs(refs: string[]): string {
  const unique = [...new Set(refs.filter((ref) => ref !== ""))].sort();
  return unique.join("\u0000");
}

export async function computeEnvelopeHash(memory: AccountingMemory): Promise<string> {
  const canonical = [
    await computeIdentityHash(memory),
    memory.contentHash,
    memory.fiscalEffect,
    memory.status,
    memory.source.system,
    memory.source.actorId ?? "",
    memory.source.actorKind,
    memory.source.model ?? "",
    memory.source.session ?? "",
    memory.recordedAt,
    memory.observedAt ?? "",
    memory.supersedesId ?? "",
    memory.receiptId ?? "",
    canonicalRefs(memory.evidenceRefs ?? []),
    canonicalRefs(memory.ruleRefs ?? []),
  ].join("\u0000");
  return sha256Hex(canonical);
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
	if (
		memory.materialityLevel !== undefined &&
		!MATERIALITY_LEVELS.includes(memory.materialityLevel)
	) {
		throw new Error(
			`INVALID_MATERIALITY_LEVEL: unknown materiality level "${memory.materialityLevel}" — expected normal|material|critical`,
		);
	}
}
