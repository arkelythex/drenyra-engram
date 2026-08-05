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
 * HTTP/MCP mapping. Mirrors the codes in auth/errors.go. Extended in
 * v0.4.0 Step 2 with the frozen judgment lifecycle codes; the existing
 * codes are unchanged.
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
	| "IDEMPOTENCY_CONFLICT"
	| "JUDGMENT_NOT_FOUND"
	| "RELATION_NOT_PROPOSABLE"
	| "RESOLUTION_REQUIRED"
	| "PROPOSAL_UNAUTHORIZED"
	| "INVALID_JUDGMENT_TRANSITION"
	| "JUDGMENT_CONFLICT"
	| "JUDGMENT_HASH_MISMATCH"
	| "PERIOD_CLOSED"
	| "PERIOD_ALREADY_CLOSED";

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
	"JUDGMENT_NOT_FOUND",
	"RELATION_NOT_PROPOSABLE",
	"RESOLUTION_REQUIRED",
	"PROPOSAL_UNAUTHORIZED",
	"INVALID_JUDGMENT_TRANSITION",
	"JUDGMENT_CONFLICT",
	"JUDGMENT_HASH_MISMATCH",
	"PERIOD_CLOSED",
	"PERIOD_ALREADY_CLOSED",
];

/**
 * Approval command (v0.4.0 Step 1). Deliberately carries NO principal
 * fields (ADR-003): authority arrives as a separate verified principal,
 * never inside the transport payload. Mirrors core.ApproveMemoryCommand.
 */
export interface ApproveMemoryCommand {
	memoryId: string;
	/** The envelope hash the reviewer actually saw; a mismatch fails with
	 * ENVELOPE_MISMATCH. */
	expectedEnvelopeHash: string;
	/** Human-readable justification (REQUIRED, non-whitespace). */
	reason: string;
	/** Idempotency key scoped to (tenant, requestId). */
	requestId: string;
}

/**
 * Outcome of an atomic approval. previousStatus is always "pending_review"
 * and currentStatus always "approved" for a fresh approval; a replay
 * returns the stored result with idempotentReplay=true. Mirrors
 * core.ApprovalResult.
 */
export interface ApprovalResult {
	memoryId: string;
	approvalEventId: string;
	previousStatus: "pending_review";
	currentStatus: "approved";
	reviewedEnvelopeHash: string;
	/** H2 — the envelope of the approved memory; always differs from the
	 * reviewed envelope (status participates in the hash). */
	resultingEnvelopeHash: string;
	principalSubjectId: string;
	membershipId: string;
	policyVersion: string;
	approvedAt: string;
	/** True when replayed from the completed idempotency reservation. */
	idempotentReplay: boolean;
}

/**
 * Immutable audit record of an authenticated approval — mirrors
 * core.ApprovalEvent and the v3 approval_events table: action always
 * "approved", fromStatus "pending_review", toStatus "approved",
 * authorizationReasonCode always "AUTHORIZED". The snapshot carries
 * canonical (sorted, deduplicated) roles.
 */
export interface ApprovalEvent {
	id: string;
	requestId: string;
	memoryId: string;
	tenantId: string;
	companyId: string;
	fiscalPeriodId?: string;
	action: "approved";
	fromStatus: "pending_review";
	toStatus: "approved";
	reviewedEnvelopeHash: string;
	resultingEnvelopeHash: string;
	reason: string;
	principalSnapshot: PrincipalSnapshot;
	policyVersion: string;
	authorizationReasonCode: "AUTHORIZED";
	createdAt: string;
}

/**
 * Transport-independent approval error carrying the frozen code (mirror of
 * auth.Error in internal/auth/errors.go). ONLY an ENVELOPE_MISMATCH error
 * carries the two envelope hashes and only a JUDGMENT_HASH_MISMATCH error
 * carries the two judgment hashes; memory/judgment content is never
 * included, especially on cross-tenant surfaces. Judgment hashes are a
 * separately versioned contract and are NEVER compared against envelope
 * hashes (design §6).
 */
export class ApprovalError extends Error {
	readonly code: ApprovalErrorCode;
	/** Present ONLY on ENVELOPE_MISMATCH — the hash the reviewer submitted. */
	readonly expectedEnvelopeHash?: string;
	/** Present ONLY on ENVELOPE_MISMATCH — the current envelope hash. */
	readonly actualEnvelopeHash?: string;
	/** Present ONLY on JUDGMENT_HASH_MISMATCH — the reviewed judgment hash. */
	readonly expectedJudgmentHash?: string;
	/** Present ONLY on JUDGMENT_HASH_MISMATCH — the current judgment hash. */
	readonly actualJudgmentHash?: string;

	constructor(
		code: ApprovalErrorCode,
		message: string,
		hashes?: {
			expectedEnvelopeHash?: string;
			actualEnvelopeHash?: string;
			expectedJudgmentHash?: string;
			actualJudgmentHash?: string;
		},
	) {
		super(message);
		this.name = "ApprovalError";
		this.code = code;
		this.expectedEnvelopeHash = hashes?.expectedEnvelopeHash;
		this.actualEnvelopeHash = hashes?.actualEnvelopeHash;
		this.expectedJudgmentHash = hashes?.expectedJudgmentHash;
		this.actualJudgmentHash = hashes?.actualJudgmentHash;
	}
}

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
		...(memory.identityHash === undefined
			? {}
			: { identityHash: memory.identityHash }),
		...(memory.envelopeHash === undefined
			? {}
			: { envelopeHash: memory.envelopeHash }),
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
	const digest = await crypto.subtle.digest(
		"SHA-256",
		new TextEncoder().encode(canonical),
	);
	return Array.from(new Uint8Array(digest))
		.map((byte) => byte.toString(16).padStart(2, "0"))
		.join("");
}

/**
 * Identity hash (v2) — SHA-256 of the DOMAIN identity: scope (tenant/company/
 * period) + topicKey + effectiveAt + source reference. Mirrors
 * core.ComputeIdentityHash.
 */
export async function computeIdentityHash(
	memory: AccountingMemory,
): Promise<string> {
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

export async function computeEnvelopeHash(
	memory: AccountingMemory,
): Promise<string> {
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

// ──────────────────────────────────────────────
// v0.4.0 Step 2 — accounting judgment vocabulary
// ──────────────────────────────────────────────

/**
 * Lifecycle state of an accounting judgment (v0.4.0 Step 2). Mirrors
 * core.JudgmentStatus. Legal machine: proposed → confirmed | rejected |
 * withdrawn | superseded; confirmed → superseded ONLY; rejected/withdrawn/
 * superseded are terminal.
 */
export const JUDGMENT_STATUSES = [
	"proposed",
	"confirmed",
	"rejected",
	"withdrawn",
	"superseded",
] as const;

export type JudgmentStatus = (typeof JUDGMENT_STATUSES)[number];

/**
 * An adjudication act over two immutable observations (v0.4.0 Step 2) — NOT
 * a KindDecision memory. Agents/systems may propose and withdraw their own
 * proposals (proposer is provenance ONLY, never authority); only a
 * VerifiedApprovalPrincipal may confirm or reject. Mirrors
 * core.AccountingJudgment.
 */
export interface AccountingJudgment {
	id: string;
	tenantId: string;
	companyId: string;
	/** Set only when both observations share a fiscal period. */
	fiscalPeriodId?: string;
	fromId: string;
	toId: string;
	relation: MemoryRelation;
	status: JudgmentStatus;
	/** Provenance only — agent|system; never authority. */
	proposer: MemorySource;
	proposalReason: string;
	/** Empty until confirmed/rejected. */
	resolution?: string;
	/** Absent until an authenticated decision. */
	adjudicator?: PrincipalSnapshot;
	/** Empty until an authenticated decision. */
	policyVersion?: string;
	/** Correction target declared by the successor. */
	predecessorId?: string;
	/** Successor routing stored on the old row. */
	supersedesId?: string;
	proposedAt: string;
	updatedAt: string;
	/** Set when decided (confirmed/rejected/superseded). */
	decidedAt?: string;
}

/**
 * Proposal command (v0.4.0 Step 2). Deliberately carries NO subject,
 * membership, role, actor-kind or assurance fields (compile-level contract):
 * the proposer Source arrives separately as provenance-only caller context,
 * and authority never travels in the transport payload. Mirrors
 * core.ProposeJudgmentCommand.
 */
export interface ProposeJudgmentCommand {
	fromId: string;
	toId: string;
	relation: MemoryRelation;
	/** Proposer's justification (REQUIRED, non-whitespace). */
	reason: string;
	/** Idempotency key scoped to (tenant, requestId). */
	requestId: string;
	/** Names an existing judgment this proposal corrects. */
	predecessorId?: string;
}

/**
 * Confirmation command (v0.4.0 Step 2). Resolution is the professional
 * human resolution (REQUIRED); expectedJudgmentHash is the reviewed
 * proposed hash the adjudicator actually saw. Mirrors
 * core.ConfirmJudgmentCommand.
 */
export interface ConfirmJudgmentCommand {
	judgmentId: string;
	resolution: string;
	expectedJudgmentHash: string;
	requestId: string;
}

/**
 * Rejection command (v0.4.0 Step 2). Reason is the human reason, stored as
 * the resolution (REQUIRED); expectedJudgmentHash is the reviewed proposed
 * hash the adjudicator actually saw. Mirrors core.RejectJudgmentCommand.
 */
export interface RejectJudgmentCommand {
	judgmentId: string;
	reason: string;
	expectedJudgmentHash: string;
	requestId: string;
}

/**
 * Withdrawal command (v0.4.0 Step 2): withdraws the caller's OWN proposed
 * judgment. Mirrors core.WithdrawJudgmentCommand.
 */
export interface WithdrawJudgmentCommand {
	judgmentId: string;
	requestId: string;
}

/**
 * Proposal result (v0.4.0 Step 2). A proposal writes NO judgment event
 * (the events CHECK admits only confirm|reject|withdraw|supersede), so the
 * result carries the entity alone; a same-request retry replays the same
 * judgment with idempotentReplay=true. Mirrors core.ProposeJudgmentResult.
 */
export interface ProposeJudgmentResult {
	judgmentId: string;
	judgment: AccountingJudgment;
	/** True when re-derived from the completed idempotency reservation. */
	idempotentReplay: boolean;
}

/** Confirmation result (v0.4.0 Step 2). Mirrors core.ConfirmJudgmentResult. */
export interface ConfirmJudgmentResult {
	judgmentId: string;
	judgment: AccountingJudgment;
	/** The immutable 'confirm' event written for this decision. */
	judgmentEventId: string;
	idempotentReplay: boolean;
}

/** Rejection result (v0.4.0 Step 2). Mirrors core.RejectJudgmentResult. */
export interface RejectJudgmentResult {
	judgmentId: string;
	judgment: AccountingJudgment;
	/** The immutable 'reject' event written for this decision. */
	judgmentEventId: string;
	idempotentReplay: boolean;
}

/** Withdrawal result (v0.4.0 Step 2). Mirrors core.WithdrawJudgmentResult. */
export interface WithdrawJudgmentResult {
	judgmentId: string;
	judgment: AccountingJudgment;
	/** The immutable 'withdraw' event written for this withdrawal. */
	judgmentEventId: string;
	idempotentReplay: boolean;
}

/**
 * Immutable judgment transition event (v0.4.0 Step 2). Every decision
 * (confirm/reject/withdraw/supersede) writes exactly one event; proposals
 * write none. Confirm/reject events carry the adjudicator snapshot and the
 * frozen policy version. Mirrors the judgment_events table (design §4).
 */
export interface JudgmentEvent {
	id: string;
	requestId: string;
	judgmentId: string;
	tenantId: string;
	/** Frozen to confirm | reject | withdraw | supersede. */
	action: "confirm" | "reject" | "withdraw" | "supersede";
	fromStatus: JudgmentStatus;
	toStatus: JudgmentStatus;
	/** Hash of the resulting state (reviewed shape for supersede). */
	judgmentHash: string;
	/** Present ONLY on confirm/reject — the adjudicator snapshot. */
	principalSnapshot?: PrincipalSnapshot;
	/** Present ONLY on confirm/reject — the frozen policy version. */
	policyVersion?: string;
	/** The human resolution (confirm) / reason (reject) / empty otherwise. */
	reason?: string;
	createdAt: string;
}

const PROPOSABLE_RELATIONS: readonly MemoryRelation[] = [
	"supports",
	"contradicts",
	"explains",
	"reconciles",
	"reverses",
	"supersedes",
];

/**
 * The six proposable judgment relations in fixed order. `conflicts_with` is
 * a legacy sync/discovery marker: it can motivate a proposal but is neither
 * accepted as a proposal relation nor removed automatically (design §3).
 * Mirrors core.ProposableRelations.
 */
export function proposableRelations(): MemoryRelation[] {
	return [...PROPOSABLE_RELATIONS];
}

/**
 * Reports whether a relation is one of the six proposable relations.
 * related/conflicts_with/derived_from/... are never proposable. Mirrors
 * core.IsProposableRelation.
 */
export function isProposableRelation(relation: MemoryRelation): boolean {
	return PROPOSABLE_RELATIONS.includes(relation);
}

/**
 * Canonical proposer JSON shape: keys sorted alphabetically (actorId,
 * actorKind, model, reference, session, system), empty optional fields
 * omitted — byte-identical with core's canonicalSource (Go).
 */
function canonicalJudgmentProposer(
	source: MemorySource,
): Record<string, string> {
	const out: Record<string, string> = {};
	if (source.actorId !== undefined && source.actorId !== "") {
		out.actorId = source.actorId;
	}
	out.actorKind = source.actorKind;
	if (source.model !== undefined && source.model !== "") {
		out.model = source.model;
	}
	if (source.reference !== undefined && source.reference !== "") {
		out.reference = source.reference;
	}
	if (source.session !== undefined && source.session !== "") {
		out.session = source.session;
	}
	out.system = source.system;
	return out;
}

/**
 * Canonical adjudicator snapshot: roles sorted and deduplicated; field order
 * matches auth.PrincipalSnapshot (subjectId, membershipId, roles,
 * authenticationMethod, assuranceLevel, authenticatedAt) so Go and TS
 * produce identical JSON bytes. Mirrors core.canonicalSnapshot (Go).
 */
function canonicalJudgmentSnapshot(
	snapshot: PrincipalSnapshot,
): PrincipalSnapshot {
	return {
		subjectId: snapshot.subjectId,
		membershipId: snapshot.membershipId,
		roles: [...new Set(snapshot.roles)].sort(),
		authenticationMethod: snapshot.authenticationMethod,
		assuranceLevel: snapshot.assuranceLevel,
		authenticatedAt: snapshot.authenticatedAt,
	};
}

/**
 * Canonical SHA-256 (hex) of a judgment's REVIEWED or CONFIRMED state over
 * canonical JSON — byte-identical with core.ComputeJudgmentHash (Go). The
 * payload key order below is the byte contract (Go marshals its payload
 * struct in this exact order).
 *
 * Documented field coverage per status:
 * - proposed (and every non-confirmed status): id, tenantId, companyId,
 *   fiscalPeriodId ("" when absent), fromId, toId, relation, status,
 *   canonical proposer (sorted keys, empties omitted), proposalReason,
 *   predecessorId ("" when absent), proposedAt. Routing fields
 *   (supersedesId) and updatedAt NEVER participate.
 * - confirmed: the base fields PLUS resolution, the canonical adjudicator
 *   snapshot (sorted roles), policyVersion, status and decidedAt.
 *
 * Rejected/withdrawn/superseded judgments hash with the reviewed shape
 * (decided fields never participate).
 */
export async function computeJudgmentHash(
	judgment: AccountingJudgment,
): Promise<string> {
	const payload: Record<string, unknown> = {
		id: judgment.id,
		tenantId: judgment.tenantId,
		companyId: judgment.companyId,
		fiscalPeriodId: judgment.fiscalPeriodId ?? "",
		fromId: judgment.fromId,
		toId: judgment.toId,
		relation: judgment.relation,
		status: judgment.status,
		proposer: canonicalJudgmentProposer(judgment.proposer),
		proposalReason: judgment.proposalReason,
		predecessorId: judgment.predecessorId ?? "",
		proposedAt: judgment.proposedAt,
	};
	if (judgment.status === "confirmed") {
		if (judgment.resolution !== undefined && judgment.resolution !== "") {
			payload.resolution = judgment.resolution;
		}
		if (judgment.adjudicator !== undefined) {
			payload.adjudicator = canonicalJudgmentSnapshot(judgment.adjudicator);
		}
		if (judgment.policyVersion !== undefined && judgment.policyVersion !== "") {
			payload.policyVersion = judgment.policyVersion;
		}
		if (judgment.decidedAt !== undefined && judgment.decidedAt !== "") {
			payload.decidedAt = judgment.decidedAt;
		}
	}
	return sha256Hex(JSON.stringify(payload));
}

// ──────────────────────────────────────────────
// v0.4.0 Step 3 — Ed25519 action receipts
// ──────────────────────────────────────────────

/**
 * Subject kind of an Ed25519 action receipt (v0.4.0 Step 3): an immutable
 * memory observation or an accounting judgment. Mirrors core.SubjectType.
 */
export const RECEIPT_SUBJECT_TYPES = ["memory", "judgment"] as const;

export type ReceiptSubjectType = (typeof RECEIPT_SUBJECT_TYPES)[number];

/**
 * The CLOSED set of covered acts (v0.4.0 Step 3; extended with the two
 * v0.5.0 close actions memory_closed / memory_reopened) — an unknown
 * action fails closed. Mirrors core.ReceiptAction.
 */
export const RECEIPT_ACTIONS = [
	"memory_recorded",
	"memory_approved",
	"memory_rejected",
	"memory_voided",
	"relation_confirmed",
	"relation_rejected",
	"evidence_linked",
	"memory_superseded",
	"memory_closed",
	"memory_reopened",
] as const;

export type ReceiptAction = (typeof RECEIPT_ACTIONS)[number];

/** Frozen receipt payload version (v0.4.0 Step 3). */
export const RECEIPT_PAYLOAD_VERSION = "receipt-payload/v0.4.0";

/** Frozen receipt signing algorithm (v0.4.0 Step 3). */
export const RECEIPT_ALGORITHM = "Ed25519";

/**
 * The frozen signed envelope of an Ed25519 action receipt (v0.4.0 Step 3).
 * Field ORDER is part of the byte contract (see canonicalUnsignedEnvelope /
 * completeReceiptBytes in receipt.ts). signature is PADDED base64 in the
 * model (raw bytes in SQLite); previousReceiptHash is the digest of the
 * prior complete canonical signed receipt for the same subject (genesis is
 * empty). algorithm is exactly "Ed25519". Mirrors core.SignedReceipt.
 */
export interface SignedReceipt {
	subjectType: ReceiptSubjectType;
	subjectId: string;
	action: ReceiptAction;
	tenantId: string;
	companyId: string;
	fiscalPeriodId: string;
	payloadHash: string;
	previousReceiptHash: string;
	principalId: string;
	membershipId: string;
	policyVersion: string;
	algorithm: string;
	keyId: string;
	signature: string;
	issuedAt: string;
}

/**
 * The canonical payload of an Ed25519 action receipt (v0.4.0 Step 3). EVERY
 * key is present in this exact order — inapplicable fields are empty, never
 * omitted; there are no optional fields, maps or nulls. Payload scope,
 * principal, policy, timestamp, subject and action equal the envelope
 * (verifyReceipt enforces it). Roles are canonicalized (sorted +
 * deduplicated). Mirrors core.ReceiptPayload.
 */
export interface ReceiptPayload {
	version: string;
	subjectType: ReceiptSubjectType;
	subjectId: string;
	action: ReceiptAction;
	tenantId: string;
	companyId: string;
	fiscalPeriodId: string;
	reviewedEnvelopeHash: string;
	resultingEnvelopeHash: string;
	reviewedJudgmentHash: string;
	resultingJudgmentHash: string;
	fromMemoryId: string;
	fromEnvelopeHash: string;
	toMemoryId: string;
	toEnvelopeHash: string;
	successorId: string;
	evidenceRef: string;
	reason: string;
	principalId: string;
	membershipId: string;
	principalRoles: string[];
	authenticationMethod: string;
	assuranceLevel: string;
	principalAuthenticatedAt: string;
	policyVersion: string;
	issuedAt: string;
}

// ──────────────────────────────────────────────
// v0.5.0 — Close Intelligence (monthly close)
// ──────────────────────────────────────────────

/** Canonical topic-key prefix of a monthly close: closing/CIERRE-<YYYYMM>. */
export const CLOSE_TOPIC_PREFIX = "closing/CIERRE-";

/** Closure states of the period_closures projection. */
export type ClosureState = "open" | "closed" | "reopened";

export const CLOSURE_STATES: readonly ClosureState[] = [
	"open",
	"closed",
	"reopened",
];

/** Frozen close-time counts of a CloseSnapshot (mirror of core.CloseCounts). */
export interface CloseCounts {
	total: number;
	byKind: Record<string, number>;
	byStatus: Record<string, number>;
}

/** One explicit monetary total (signed cents — never a float). */
export interface CloseTotal {
	code: string;
	currency: string;
	/** Signed total in cents (BigInt transport; negative is legal). */
	amountCents: bigint;
	sourceMemoryIds: string[];
}

/** One frozen pending item of a CloseSnapshot. */
export interface ClosePendingItem {
	memoryId: string;
	topicKey: string;
	kind: string;
	status: string;
	title: string;
	effectiveAt: string;
}

/** Period reconciliation lifecycle counts. */
export interface CloseReconciliation {
	proposed: number;
	confirmed: number;
	rejected: number;
}

/** The structured truth of a monthly close (mirror of core.CloseSnapshot). */
export interface CloseSnapshot {
	period: string;
	generatedAt: string;
	/** SHA-256 hex of the canonical snapshot JSON (self-hash). */
	summaryHash: string;
	counts: CloseCounts;
	totals: CloseTotal[];
	pendingItems: ClosePendingItem[];
	reconciliation: CloseReconciliation;
	narrativeMemoryIds: string[];
}

/** CreateClose input (mirror of core.CreateCloseInput). */
export interface CreateCloseInput {
	period: string;
	totals: CloseTotal[];
	reason?: string;
	source: MemorySource;
}

/** ReopenPeriod command (mirror of core.ReopenPeriodCommand). */
export interface ReopenPeriodCommand {
	scope: MemoryScope;
	expectedCloseMemoryId: string;
	reason: string;
	requestId: string;
}

/** ReopenPeriod outcome (mirror of core.ReopenPeriodResult). */
export interface ReopenPeriodResult {
	tenantId: string;
	companyId: string;
	fiscalPeriodId: string;
	closeMemoryId: string;
	eventId: string;
	status: string;
	reopenedAt: string;
	principalSubjectId: string;
	policyVersion: string;
	idempotentReplay: boolean;
}

/**
 * Month-end UTC stamp of a YYYYMM period (last day 23:59:59Z) — the canonical
 * effectiveAt of a monthly close. A malformed period fails with INVALID_PERIOD.
 */
export function monthEndUTC(period: string): string {
	if (!isValidPeriod(period)) {
		throw new Error(`INVALID_PERIOD: expected YYYYMM (six digits, month 01-12), got "${period}"`);
	}
	const year = Number(period.slice(0, 4));
	const month = Number(period.slice(4, 6));
	const end = new Date(Date.UTC(year, month, 0, 23, 59, 59)); // month is 1-based in Date.UTC
	return end.toISOString();
}
