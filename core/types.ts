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
	| "voided"
	| "returned"; // v0.9.0 review workspace — NON-terminal

export const MEMORY_STATUSES: readonly MemoryStatus[] = [
	"active",
	"pending_review",
	"approved",
	"rejected",
	"superseded",
	"voided",
	"returned",
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
 *
 * v0.8.0 evidence-lifecycle roles extend the same union (design §8.1):
 * `records_compliance_officer` / `tenant_records_owner` are default approvers,
 * `tax_responsible` is a dual-approval second approver ONLY, and
 * `operational_accountant` is DENY-LISTED (never requests, approves, rejects,
 * withdraws or executes). Like tax roles, lifecycle roles sit OUTSIDE the
 * accounting ladder at rank 0 — explicit-match only, never implied.
 */
export type AccountingRole =
	| "accountant"
	| "senior_accountant"
	| "controller"
	| "tax_reviewer"
	| "authorized_tax_professional"
	| "records_compliance_officer"
	| "tenant_records_owner"
	| "tax_responsible"
	| "operational_accountant";
export const ACCOUNTING_ROLES: readonly AccountingRole[] = [
	"accountant",
	"senior_accountant",
	"controller",
	"tax_reviewer",
	"authorized_tax_professional",
	"records_compliance_officer",
	"tenant_records_owner",
	"tax_responsible",
	"operational_accountant",
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
 * v0.4.0 Step 2 with the frozen judgment lifecycle codes, in v0.5.0
 * with the close (PERIOD_CLOSED, PERIOD_ALREADY_CLOSED) and reconciliation
 * (RECONCILIATION_*) codes, and in v0.8.0 with the evidence-lifecycle
 * policy codes (ROLE_DENIED, APPROVER_IS_REQUESTER, DUAL_APPROVAL_REQUIRED,
 * SAME_PRINCIPAL_SECOND_APPROVAL); the existing codes are unchanged.
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
	| "PERIOD_ALREADY_CLOSED"
	| "RECONCILIATION_NOT_FOUND"
	| "INVALID_RECONCILIATION_TRANSITION"
	| "RECONCILIATION_CONFLICT"
	| "RECONCILIATION_HASH_MISMATCH"
	| "ROLE_DENIED"
	| "APPROVER_IS_REQUESTER"
	| "DUAL_APPROVAL_REQUIRED"
	| "SAME_PRINCIPAL_SECOND_APPROVAL"
	// v0.9.0 review-workspace clauses (design §5/§6): SOD_VIOLATION fails the
	// decision closed when the authenticated reviewer IS the proposer;
	// REVIEW_CHECKS_REQUIRED fails a material/critical approval closed when the
	// two review checks were not both declared. Mirrors auth.CodeSODViolation /
	// auth.CodeReviewChecksRequired.
	| "SOD_VIOLATION"
	| "REVIEW_CHECKS_REQUIRED";

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
	"RECONCILIATION_NOT_FOUND",
	"INVALID_RECONCILIATION_TRANSITION",
	"RECONCILIATION_CONFLICT",
	"RECONCILIATION_HASH_MISMATCH",
	"ROLE_DENIED",
	"APPROVER_IS_REQUESTER",
	"DUAL_APPROVAL_REQUIRED",
	"SAME_PRINCIPAL_SECOND_APPROVAL",
	"SOD_VIOLATION",
	"REVIEW_CHECKS_REQUIRED",
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
// v0.9.0 review workspace (docs/architecture/review-workspace-v0.9.md)
// ──────────────────────────────────────────────

/**
 * The two anti-rubber-stamp checks a human reviewer declares for a
 * HIGH-RISK approval (design §5/§6): the evidence was inspected and the
 * rules were inspected. A material/critical approval requires BOTH true →
 * REVIEW_CHECKS_REQUIRED otherwise (fail-closed inside the transaction).
 * Mirrors core.ReviewChecks.
 */
export interface ReviewChecks {
	evidenceInspected: boolean;
	ruleInspected: boolean;
}

/**
 * The authenticated reject command (v0.9.0, design §5). Deliberately carries
 * NO principal fields (ADR-003). Mirrors core.RejectMemoryCommand.
 */
export interface RejectMemoryCommand {
	memoryId: string;
	/** The envelope hash the reviewer actually saw; a mismatch fails with
	 * ENVELOPE_MISMATCH. */
	expectedEnvelopeHash: string;
	/** Human rejection reason (REQUIRED for material/critical or
	 * closing/declaration/sunat_filing; optional otherwise; always persisted). */
	reason: string;
	/** Idempotency key scoped to (tenant, requestId). */
	requestId: string;
}

/** Outcome of an atomic rejection (pending_review → rejected, terminal). */
export interface RejectMemoryResult {
	memoryId: string;
	decisionEventId: string;
	previousStatus: "pending_review";
	currentStatus: "rejected";
	reviewedEnvelopeHash: string;
	resultingEnvelopeHash: string;
	reason: string;
	principalSubjectId: string;
	membershipId: string;
	policyVersion: string;
	decidedAt: string;
	idempotentReplay: boolean;
}

/**
 * The authenticated RETURN command (v0.9.0, design §5): the reason is
 * REQUIRED (a return is a correction request — the reason tells the agent
 * what to fix). Mirrors core.ReturnMemoryCommand.
 */
export interface ReturnMemoryCommand {
	memoryId: string;
	expectedEnvelopeHash: string;
	reason: string;
	requestId: string;
}

/**
 * Outcome of an atomic return: pending_review → returned (NON-terminal — an
 * agent Save on the returned memory creates a NEW revision that re-enters
 * pending_review; the returned revision itself never reopens). Mirrors
 * core.ReturnMemoryResult.
 */
export interface ReturnMemoryResult {
	memoryId: string;
	decisionEventId: string;
	previousStatus: "pending_review";
	currentStatus: "returned";
	reviewedEnvelopeHash: string;
	resultingEnvelopeHash: string;
	reason: string;
	principalSubjectId: string;
	membershipId: string;
	policyVersion: string;
	decidedAt: string;
	idempotentReplay: boolean;
}

/**
 * Immutable audit record of an authenticated reject/return (v0.9.0) — the
 * memory_decision_events ledger mirror: action "rejected"|"returned" always
 * leaves pending_review and lands in the matching status;
 * authorizationReasonCode "REJECTED"|"RETURNED" matches the action. The
 * snapshot carries canonical (sorted, deduplicated) roles. Mirrors
 * core.MemoryDecisionEvent.
 */
export interface MemoryDecisionEvent {
	id: string;
	requestId: string;
	memoryId: string;
	tenantId: string;
	companyId: string;
	fiscalPeriodId?: string;
	action: "rejected" | "returned";
	fromStatus: "pending_review";
	toStatus: "rejected" | "returned";
	reviewedEnvelopeHash: string;
	resultingEnvelopeHash: string;
	reason: string;
	principalSnapshot: PrincipalSnapshot;
	policyVersion: string;
	authorizationReasonCode: "REJECTED" | "RETURNED";
	createdAt: string;
}

/** Pagination of the pending_review queue (design §3): limit bounded
 * (default 50, max 200), offset defaults to 0. Mirrors core.ReviewQueueQuery. */
export interface ReviewQueueQuery {
	scope: MemoryScope;
	limit?: number;
	offset?: number;
}

/** ONE pending_review queue item (design §3). materialityCents is the
 * declared monetary threshold in WHOLE BigInt cents (0 when unset) — never a
 * float. envelopeHash is the CURRENT envelope the reviewer must sign against;
 * recordedBy is the proposer (SoD requires the reviewer to differ from it). */
export interface ReviewQueueItem {
	memoryId: string;
	kind: MemoryKind;
	fiscalEffect: FiscalEffect;
	materialityLevel?: MaterialityLevel;
	materialityCents: bigint;
	status: MemoryStatus;
	envelopeHash: string;
	recordedBy: string;
	recordedAt: string;
	evidenceRefCount: number;
	ruleRefCount: number;
	openJudgmentCount: number;
}

/** The paginated queue result (items + echoed limit/offset). */
export interface ReviewQueuePage {
	items: ReviewQueueItem[];
	limit: number;
	offset: number;
}

/**
 * The composed review of ONE pending revision (design §4): the full pending
 * revision, the structured content diff vs its chain predecessor, the
 * evidence state with WORM availability, the best-effort rule state, the
 * open proposed judgments and the decision-relevant review metadata with the
 * boundary notice. Mirrors core.ReviewDetail.
 */
export interface ReviewDetail {
	memory: AccountingMemory;
	diff: { changes: Array<{ field: string; before: string; after: string }> };
	evidence: Array<{
		ref: string;
		availability: "present" | "absent" | "not-a-ref";
		objectId?: string;
		sizeBytes?: bigint;
		contentType?: string;
	}>;
	rules: Array<{
		ref: string;
		resolved: boolean;
		memoryId?: string;
		status?: MemoryStatus;
		effectiveAt?: string;
		expiresAt?: string;
	}>;
	openJudgments: Array<{
		judgmentId: string;
		relation: MemoryRelation;
		fromId: string;
		toId: string;
		proposerId: string;
		proposedAt: string;
	}>;
	reviewMetadata: {
		envelopeHashToSign: string;
		recordedBy: string;
		recordedAt: string;
		observedAt?: string;
		fiscalEffect: FiscalEffect;
		materialityLevel?: MaterialityLevel;
		materialityCents: bigint;
		priorApprovedRevision?: string;
	};
	boundaryNotice: string;
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
	/**
	 * Structured rule links (v0.6.0, design §2.2) — the READ SURFACE: one
	 * entry per structured rule_links row (version metadata present). Legacy
	 * unversioned refs stay bare in ruleRefs and never appear here. Structured
	 * metadata does NOT participate in the hashes — the bare refs do.
	 */
	ruleLinks?: RuleLink[];
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
	/**
	 * Structured rule links (v0.6.0, design §2.2) — TRANSPORT-ONLY on save:
	 * the store derives/deduplicates the bare ruleRefs from ruleLinks[].ref
	 * before hashing and inserts the memory AND the structured rows atomically.
	 * Structured metadata never contributes to the envelope/hashes.
	 */
	ruleLinks?: RuleLink[];
	confidence?: number;
	materiality?: bigint;
	/** Declared materiality classification (normal | material | critical); NULL is
	 * treated as normal by the approval policy. Mirrors core.SaveInput. */
	materialityLevel?: MaterialityLevel;
	receiptId?: string;
}

/**
 * ONE structured rule link (v0.6.0, design §2.2): a bare rule ref pinned to
 * exactly ONE immutable rule-memory revision. `ref` is the rule chain's
 * stable topicKey; `version` is the immutable rule-memory ID of one chain
 * revision (NOT the mutable latest revision); `effectiveAt` is the consuming
 * decision's accounting time and MUST equal the consuming memory's
 * effectiveAt. Mirrors core.RuleLink.
 */
export interface RuleLink {
	ref: string;
	version: string;
	effectiveAt: string;
}

/** Defensive copy of a structured-link list. */
export function cloneRuleLinks(
	links: RuleLink[] | undefined,
): RuleLink[] | undefined {
	if (links === undefined) return undefined;
	return links.map((link) => ({ ...link }));
}

/**
 * Fail-closed structured-link validation (design §2.2): non-empty ref and
 * version plus an RFC3339 effectiveAt. Throws INVALID_RULE_LINK otherwise.
 */
export function assertValidRuleLink(link: RuleLink): void {
	if (typeof link.ref !== "string" || link.ref.trim().length === 0) {
		throw new Error("INVALID_RULE_LINK: ref must be a non-empty string");
	}
	if (typeof link.version !== "string" || link.version.trim().length === 0) {
		throw new Error(
			`INVALID_RULE_LINK: version must be a non-empty string (the immutable rule-memory id) for ref "${link.ref}"`,
		);
	}
	if (
		typeof link.effectiveAt !== "string" ||
		Number.isNaN(Date.parse(link.effectiveAt))
	) {
		throw new Error(
			`INVALID_RULE_LINK: effectiveAt must be RFC3339, got "${link.effectiveAt}" for ref "${link.ref}"`,
		);
	}
}

/**
 * Validates a structured-link list and returns the deduplicated canonical
 * set: identical (ref, version, effectiveAt) triples collapse (no-op); two
 * DIFFERENT links for the same ref fail RULE_LINK_VERSION_CONFLICT
 * (metadata is never updated in place). Mirrors core.AssertValidRuleLinks.
 */
export function assertValidRuleLinks(
	links: RuleLink[] | undefined,
): RuleLink[] {
	if (links === undefined) return [];
	const seen = new Map<string, RuleLink>();
	const out: RuleLink[] = [];
	for (const link of links) {
		assertValidRuleLink(link);
		const prev = seen.get(link.ref);
		if (prev !== undefined) {
			if (
				prev.version !== link.version ||
				prev.effectiveAt !== link.effectiveAt
			) {
				throw new Error(
					`RULE_LINK_VERSION_CONFLICT: ref "${link.ref}" is pinned to ${prev.version} at ${prev.effectiveAt} and cannot be re-pinned to ${link.version} at ${link.effectiveAt} (metadata is never updated in place)`,
				);
			}
			// Identical link for the same ref: no-op, keep the first occurrence.
			continue;
		}
		seen.set(link.ref, link);
		out.push(link);
	}
	return out;
}

/**
 * Derives the deduplicated bare ruleRefs from a structured-link list (design
 * §2.2): the store merges ruleLinks[].ref into the existing refs BEFORE
 * hashing, because the canonical envelope hashes ONLY the bare refs.
 */
export function deriveRuleRefs(
	existing: string[] | undefined,
	links: RuleLink[] | undefined,
): string[] | undefined {
	if (links === undefined || links.length === 0) return existing;
	const seen = new Set<string>();
	const out: string[] = [];
	for (const ref of existing ?? []) {
		if (ref.length === 0) continue;
		if (seen.has(ref)) continue;
		seen.add(ref);
		out.push(ref);
	}
	for (const link of links) {
		if (seen.has(link.ref)) continue;
		seen.add(link.ref);
		out.push(link.ref);
	}
	return out;
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
		...(memory.ruleLinks === undefined
			? {}
			: { ruleLinks: cloneRuleLinks(memory.ruleLinks) }),
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
// v0.5.0 — First-class reconciliation (design §3)
// ──────────────────────────────────────────────

/**
 * Lifecycle state of a first-class reconciliation (v0.5.0). Mirrors
 * core.ReconciliationStatus. Legal machine: proposed → confirmed | rejected |
 * withdrawn | superseded; confirmed → superseded ONLY; rejected/withdrawn/
 * superseded are terminal.
 */
export const RECONCILIATION_STATUSES = [
	"proposed",
	"confirmed",
	"rejected",
	"withdrawn",
	"superseded",
] as const;

export type ReconciliationStatus = (typeof RECONCILIATION_STATUSES)[number];

/**
 * An adjudication act over two observations (v0.5.0, design §3.2) — NOT a
 * memory kind. Confirmation atomically projects one observation relation
 * leftMemoryId --reconciles--> rightMemoryId. Agents/systems may propose and
 * withdraw their own proposals (proposer is provenance ONLY, never
 * authority); only a VerifiedApprovalPrincipal may confirm or reject.
 * Mirrors core.Reconciliation.
 */
export interface Reconciliation {
	id: string;
	tenantId: string;
	companyId: string;
	/** Set only when both endpoints share a fiscal period. */
	fiscalPeriodId?: string;
	leftMemoryId: string;
	rightMemoryId: string;
	method: string;
	currency: string;
	leftAmountCents: bigint;
	rightAmountCents: bigint;
	/** Engine-derived = leftAmountCents − rightAmountCents (schema-enforced). */
	varianceCents: bigint;
	/** Accepted variance band (REQUIRED, non-negative). */
	toleranceCents: bigint;
	status: ReconciliationStatus;
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
	/** Set when decided (confirmed/rejected/withdrawn/superseded). */
	decidedAt?: string;
}

/**
 * Proposal command (v0.5.0). Deliberately carries NO subject, membership,
 * role, actor-kind or assurance fields (compile-level contract): the proposer
 * Source arrives separately as provenance-only caller context, and authority
 * never travels in the transport payload. VarianceCents is engine-derived
 * (never caller-supplied). Amounts are signed BigInt cents (never float).
 * Mirrors core.ProposeReconciliationCommand.
 */
export interface ProposeReconciliationCommand {
	leftMemoryId: string;
	rightMemoryId: string;
	method: string;
	currency: string;
	leftAmountCents: bigint;
	rightAmountCents: bigint;
	/** Accepted variance band (REQUIRED, non-negative). */
	toleranceCents: bigint;
	/** Proposer's justification (REQUIRED, non-whitespace). */
	reason: string;
	/** Idempotency key scoped to (tenant, requestId). */
	requestId: string;
	/** Names an existing reconciliation this proposal corrects. */
	predecessorId?: string;
}

/**
 * Confirmation command (v0.5.0). Resolution is the professional human
 * resolution (REQUIRED); expectedReconciliationHash is the reviewed proposed
 * hash the adjudicator actually saw. Mirrors
 * core.ConfirmReconciliationCommand.
 */
export interface ConfirmReconciliationCommand {
	reconciliationId: string;
	resolution: string;
	expectedReconciliationHash: string;
	requestId: string;
}

/**
 * Rejection command (v0.5.0). Reason is the human reason, stored as the
 * resolution (REQUIRED); expectedReconciliationHash is the reviewed proposed
 * hash the adjudicator actually saw. Mirrors core.RejectReconciliationCommand.
 */
export interface RejectReconciliationCommand {
	reconciliationId: string;
	reason: string;
	expectedReconciliationHash: string;
	requestId: string;
}

/**
 * Withdrawal command (v0.5.0): withdraws the caller's OWN proposed
 * reconciliation. Mirrors core.WithdrawReconciliationCommand.
 */
export interface WithdrawReconciliationCommand {
	reconciliationId: string;
	requestId: string;
}

/** Proposal result (v0.5.0). A proposal writes NO reconciliation event, so the
 * result carries the entity alone; a same-request retry replays the same
 * reconciliation with idempotentReplay=true. Mirrors
 * core.ProposeReconciliationResult. */
export interface ProposeReconciliationResult {
	reconciliationId: string;
	reconciliation: Reconciliation;
	idempotentReplay: boolean;
}

/** Confirmation result (v0.5.0). Mirrors core.ConfirmReconciliationResult. */
export interface ConfirmReconciliationResult {
	reconciliationId: string;
	reconciliation: Reconciliation;
	/** The immutable 'confirm' event written for this decision. */
	reconciliationEventId: string;
	idempotentReplay: boolean;
}

/** Rejection result (v0.5.0). Mirrors core.RejectReconciliationResult. */
export interface RejectReconciliationResult {
	reconciliationId: string;
	reconciliation: Reconciliation;
	/** The immutable 'reject' event written for this decision. */
	reconciliationEventId: string;
	idempotentReplay: boolean;
}

/** Withdrawal result (v0.5.0). Mirrors core.WithdrawReconciliationResult. */
export interface WithdrawReconciliationResult {
	reconciliationId: string;
	reconciliation: Reconciliation;
	/** The immutable 'withdraw' event written for this withdrawal. */
	reconciliationEventId: string;
	idempotentReplay: boolean;
}

/** Reports whether status is a known reconciliation status. Mirrors
 * core.IsValidReconciliationStatus. */
export function isValidReconciliationStatus(
	status: string,
): status is ReconciliationStatus {
	return (RECONCILIATION_STATUSES as readonly string[]).includes(status);
}

/** Can be confirmed (proposed only). Mirrors core.CanConfirmReconciliation. */
export function canConfirmReconciliation(
	status: ReconciliationStatus,
): boolean {
	return status === "proposed";
}

/** Can be rejected (proposed only). Mirrors core.CanRejectReconciliation. */
export function canRejectReconciliation(status: ReconciliationStatus): boolean {
	return status === "proposed";
}

/** Can be withdrawn by its proposer (proposed only). Mirrors
 * core.CanWithdrawReconciliation. */
export function canWithdrawReconciliation(
	status: ReconciliationStatus,
): boolean {
	return status === "proposed";
}

/** Can be superseded (confirmed only — a correction supersedes its
 * predecessor while atomically confirming). Mirrors
 * core.CanSupersedeReconciliation. */
export function canSupersedeReconciliation(
	status: ReconciliationStatus,
): boolean {
	return status === "confirmed";
}

/**
 * The adjacency table of the reconciliation machine: proposed → confirmed|
 * rejected|withdrawn|superseded; confirmed → superseded ONLY; terminal states
 * never re-open. Mirrors core.IsLegalReconciliationTransition.
 */
export function isLegalReconciliationTransition(
	from: ReconciliationStatus,
	to: ReconciliationStatus,
): boolean {
	switch (from) {
		case "proposed":
			return (
				to === "confirmed" ||
				to === "rejected" ||
				to === "withdrawn" ||
				to === "superseded"
			);
		case "confirmed":
			return to === "superseded";
		default:
			return false;
	}
}

/**
 * Compact canonical JSON with NO HTML escaping and exact int64 support:
 * strings delegate to JSON.stringify (identical escaping to Go's
 * encoding/json without SetEscapeHTML), bigint values marshal as bare integer
 * digits (never quoted, never coerced through a lossy JS Number), object keys
 * follow insertion order (the caller builds payloads in the Go struct field
 * order), undefined fails closed. This is the byte contract of
 * computeReconciliationHash — Go marshals int64 cents as JSON numbers, and
 * JSON.stringify alone cannot represent BigInt.
 */
function canonicalJSONStringify(value: unknown): string {
	if (value === null) return "null";
	if (typeof value === "bigint") return value.toString();
	if (typeof value === "number" || typeof value === "boolean") {
		return JSON.stringify(value);
	}
	if (typeof value === "string") return JSON.stringify(value);
	if (Array.isArray(value)) {
		return `[${value.map(canonicalJSONStringify).join(",")}]`;
	}
	if (typeof value === "object") {
		const entries = Object.entries(value as Record<string, unknown>);
		return `{${entries
			.map(([key, v]) => `${JSON.stringify(key)}:${canonicalJSONStringify(v)}`)
			.join(",")}}`;
	}
	throw new Error(
		"canonical JSON: unsupported value in reconciliation hash payload",
	);
}

/**
 * Canonical SHA-256 (hex) of a reconciliation's REVIEWED or CONFIRMED state
 * over canonical JSON — byte-identical with core.ComputeReconciliationHash
 * (Go). The payload key order below is the byte contract (Go marshals its
 * payload struct in this exact order).
 *
 * Documented field coverage per status:
 * - proposed (and every non-confirmed status): id, tenantId, companyId,
 *   fiscalPeriodId ("" when absent), leftMemoryId, rightMemoryId, method,
 *   currency, leftAmountCents, rightAmountCents, varianceCents,
 *   toleranceCents, status, canonical proposer (sorted keys, empties
 *   omitted), proposalReason, predecessorId ("" when absent), proposedAt.
 *   Routing fields (supersedesId) NEVER participate.
 * - confirmed: the base fields PLUS resolution, the canonical adjudicator
 *   snapshot (sorted roles), policyVersion, status and decidedAt.
 *
 * Rejected/withdrawn/superseded reconciliations hash with the reviewed shape
 * (decided fields never participate).
 */
export async function computeReconciliationHash(
	r: Reconciliation,
): Promise<string> {
	const payload: Record<string, unknown> = {
		id: r.id,
		tenantId: r.tenantId,
		companyId: r.companyId,
		fiscalPeriodId: r.fiscalPeriodId ?? "",
		leftMemoryId: r.leftMemoryId,
		rightMemoryId: r.rightMemoryId,
		method: r.method,
		currency: r.currency,
		leftAmountCents: r.leftAmountCents,
		rightAmountCents: r.rightAmountCents,
		varianceCents: r.varianceCents,
		toleranceCents: r.toleranceCents,
		status: r.status,
		proposer: canonicalJudgmentProposer(r.proposer),
		proposalReason: r.proposalReason,
		predecessorId: r.predecessorId ?? "",
		proposedAt: r.proposedAt,
	};
	if (r.status === "confirmed") {
		if (r.resolution !== undefined && r.resolution !== "") {
			payload.resolution = r.resolution;
		}
		if (r.adjudicator !== undefined) {
			payload.adjudicator = canonicalJudgmentSnapshot(r.adjudicator);
		}
		if (r.policyVersion !== undefined && r.policyVersion !== "") {
			payload.policyVersion = r.policyVersion;
		}
		if (r.decidedAt !== undefined && r.decidedAt !== "") {
			payload.decidedAt = r.decidedAt;
		}
	}
	return sha256Hex(canonicalJSONStringify(payload));
}

// ──────────────────────────────────────────────
// v0.4.0 Step 3 — Ed25519 action receipts
// ──────────────────────────────────────────────

/**
 * Subject kind of an Ed25519 action receipt (v0.4.0 Step 3): an immutable
 * memory observation, an accounting judgment, a first-class reconciliation
 * (v0.5.0), or an EvidenceObject (v0.7.0 — the subject id is the
 * content-addressed SHA-256 hex of the artifact bytes). Mirrors core.SubjectType.
 */
export const RECEIPT_SUBJECT_TYPES = [
	"memory",
	"judgment",
	"reconciliation",
	"evidence_object",
] as const;

export type ReceiptSubjectType = (typeof RECEIPT_SUBJECT_TYPES)[number];

/**
 * The CLOSED set of covered acts (v0.4.0 Step 3): the thirteen v0.4–v0.7
 * acts (the eight original acts, memory_closed / memory_reopened,
 * reconciliation_confirmed / reconciliation_rejected and object_stored),
 * the eight v0.9 evidence-lifecycle acts — retention binding plus the six
 * purge-transition acts (request / approval / rejection / cancellation /
 * withdrawal / execution) and the durable execution-intent act
 * purge_intent — and the two v0.8 object-level hold acts. Exact parity with
 * Go's core.IsValidReceiptAction (twenty-three closed actions); an unknown
 * action fails closed. Mirrors core.ReceiptAction.
 */
export const RECEIPT_ACTIONS = [
	"memory_recorded",
	"memory_approved",
	"memory_rejected",
	"memory_returned", // v0.9.0 review workspace (design §2/§5)
	"memory_voided",
	"relation_confirmed",
	"relation_rejected",
	"evidence_linked",
	"memory_superseded",
	"memory_closed",
	"memory_reopened",
	"reconciliation_confirmed",
	"reconciliation_rejected",
	"object_stored",
	// v0.9.0 evidence-lifecycle acts (design §4 step 3 / §5): retention
	// binding (retention_bound) plus the six purge-transition acts
	// (purge_requested, purge_approved, purge_rejected, purge_cancelled,
	// purge_withdrawn, purge_executed) and the durable execution-intent act
	// purge_intent (design §11 step 1 — the intent transaction is
	// receipt-covered on the evidence_object chain BEFORE any byte is
	// removed). Full parity with Go's IsValidReceiptAction.
	"retention_bound",
	"purge_requested",
	"purge_approved",
	"purge_rejected",
	"purge_cancelled",
	"purge_withdrawn",
	"purge_intent",
	"purge_executed",
	// v0.8.0 object-level legal holds (batch 3): the two hold acts are
	// emitted atomically on the evidence_object subject chain.
	"hold_placed",
	"hold_lifted",
] as const;

export type ReceiptAction = (typeof RECEIPT_ACTIONS)[number];

/** Frozen receipt payload version (v0.4.0 Step 3). */
export const RECEIPT_PAYLOAD_VERSION = "receipt-payload/v0.4.0";

/**
 * Payload version stamped on the v0.5.0 actions (memory_closed,
 * memory_reopened, reconciliation_confirmed, reconciliation_rejected).
 * Canonicalization is version-agnostic (the payload shape is unchanged), so
 * verifiers accept both v0.4.0 and v0.5.0 payloads. Mirrors
 * core.ReceiptPayloadVersionV05.
 */
export const RECEIPT_PAYLOAD_VERSION_V05 = "receipt-payload/v0.5.0";

/**
 * Payload version stamped on the v0.7.0 action (object_stored).
 * Canonicalization is version-agnostic (the payload SHAPE is unchanged — the
 * object identity rides the existing evidenceRef field, the scope rides the
 * existing tenant/company/fiscalPeriod fields and the claimed actor rides
 * principalId), so verifiers keep accepting v0.4.0/v0.5.0 payloads unchanged
 * AND accept v0.7.0 payloads without a protocol break (the versioned
 * protocol decision). Mirrors core.ReceiptPayloadVersionV07.
 */
export const RECEIPT_PAYLOAD_VERSION_V07 = "receipt-payload/v0.7.0";

/**
 * Payload version stamped on the v0.8.0 object-level hold acts
 * (hold_placed, hold_lifted — batch 3). Canonicalization is version-agnostic
 * (the payload SHAPE is unchanged), so verifiers keep accepting v0.4.0–v0.7.0
 * payloads unchanged AND accept v0.8.0 payloads without a protocol break.
 * Mirrors core.ReceiptPayloadVersionV08.
 */
export const RECEIPT_PAYLOAD_VERSION_V08 = "receipt-payload/v0.8.0";

/**
 * Payload version stamped on the v0.9.0 evidence-lifecycle purge acts
 * (retention_bound, purge_requested, purge_approved, purge_rejected,
 * purge_cancelled, purge_withdrawn — batch 4, schema v11). Unlike v0.5–v0.8
 * (which reused the existing envelope-hash fields), the v0.9.0 payload ADDS
 * the two lifecycle-hash fields reviewedLifecycleHash / resultingLifecycleHash
 * (the canonical lifecycle snapshot hashes H1/H2) and the additive
 * execution-attempt field executionAttemptId (the per-attempt discriminator of
 * a purge_intent receipt). Canonicalization is version-conditional for these
 * three fields ONLY: pre-v0.9 payloads canonicalize byte-identically to today
 * (verifiers keep accepting v0.4.0–v0.8.0 payloads unchanged), while v0.9.0
 * payloads append the fields in fixed order after issuedAt. Mirrors
 * core.ReceiptPayloadVersionV09.
 */
export const RECEIPT_PAYLOAD_VERSION_V09 = "receipt-payload/v0.9.0";

/**
 * Payload version stamped on the v0.9.0 REVIEW WORKSPACE decision receipts
 * (docs/architecture/review-workspace-v0.9.md §2/§5): the authenticated
 * memory_rejected payload (extended with the human reason and the reviewed
 * envelope hash H1 — mirrors approve signing H1+H2) and the new
 * memory_returned act (same coverage: reviewed H1, resulting H2, reason and
 * the complete verified principal snapshot). Canonicalization is
 * version-agnostic (the reason/reviewedEnvelopeHash fields already exist in
 * the frozen payload SHAPE), so verifiers keep accepting v0.4.0–v0.9.0
 * payloads unchanged AND accept v0.10.0 payloads without a protocol break.
 * Mirrors core.ReceiptPayloadVersionV10.
 */
export const RECEIPT_PAYLOAD_VERSION_V10 = "receipt-payload/v0.10.0";

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
 *
 * v0.9.0 additive extension: the two lifecycle-hash fields below are OPTIONAL
 * and empty for every pre-v0.9 action/version, so every existing payload
 * constructor stays valid. Canonicalization appends them ONLY for the v0.9.0
 * payload version (in fixed order after issuedAt), keeping pre-v0.9 payload
 * bytes frozen.
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
	// v0.9.0 additive fields (evidence-lifecycle purge acts, batch 4): the
	// reviewed/resulting CANONICAL LIFECYCLE SNAPSHOT HASHES (H1/H2) and the
	// execution-attempt id of a purge_intent receipt (the (tenant, executionId)
	// of the attempt — populated ONLY for purge_intent, so a fresh-ID retry
	// after an interrupted intent emits a DISTINCT payload and never collides on
	// the UNIQUE(subject_type, subject_id, action, payload_hash) backstop).
	// Optional and empty for every pre-v0.9 action/version; canonicalization
	// appends them ONLY for the v0.9.0 payload version so pre-v0.9 payload bytes
	// stay frozen.
	reviewedLifecycleHash?: string;
	resultingLifecycleHash?: string;
	executionAttemptId?: string;
}

export interface EvidenceObject {
	objectId: string;
	sha256: string;
	// byte length of the artifact — a JSON integer, never a float, never money
	size: number;
	contentType: string;
	tenantId: string;
	companyId: string;
	ruc: string;
	period: string;
	sourceSystem: string;
	sourceReference: string;
	sourceActorId: string;
	sourceActorKind: ActorKind;
	storedBy: string;
	storedAt: string;
	relPath: string;
}

/**
 * Input for storing ONE evidence object (v0.7.0): the artifact bytes, an
 * optional MIME hint, the exact company scope and the capture provenance.
 * Mirrors core.ObjectStoreInput.
 */
export interface ObjectStoreInput {
	bytes: Uint8Array;
	contentType?: string;
	scope: MemoryScope;
	source: MemorySource;
}

/** Outcome of a store attempt: the stored object plus whether THIS call
 * created it (false = content-addressed duplicate no-op). */
export interface ObjectStoreResult {
	object: EvidenceObject;
	created: boolean;
}

// ──────────────────────────────────────────────
// v0.8.0 — object-level legal holds (batch 3, design §3.2/§7)
// ──────────────────────────────────────────────

/**
 * The closed hold-kind set (§3.2). The TOKENS are the frozen
 * retention-policy hold-kind tokens (one closed set across the lifecycle,
 * never duplicated). Mirrors core.HoldKind.
 */
export const HOLD_KINDS = [
	"legal",
	"audit",
	"dispute",
	"fiscalization",
	"other",
] as const;

export type HoldKind = (typeof HOLD_KINDS)[number];

/**
 * ONE immutable object-level hold record (design §3.2). The scope tuple is
 * flattened (tenantId/companyId/ruc/period) exactly like EvidenceObject so
 * the wire shape mirrors the DB row byte-for-byte. The lift fields are the
 * ONE-WAY closure: all empty while placed, all set together on lift, never
 * cleared or rewritten (lifted holds remain visible forever). Mirrors
 * core.EvidenceHold — field ORDER is part of the canonical byte contract.
 */
export interface EvidenceHold {
	holdId: string;
	objectId: string;
	tenantId: string;
	companyId: string;
	ruc: string;
	period: string;
	kind: HoldKind;
	reason: string;
	ownerSubjectId: string;
	placedAt: string;
	placedBy: string;
	liftedAt?: string;
	liftedBy?: string;
	liftReason?: string;
}

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

/** One recent-chain entry of a CurrentContext (mirror of core.RecentChain). */
export interface RecentChain {
	topicKey: string;
	memoryId: string;
	kind: string;
	status: string;
	effectiveAt: string;
	title: string;
}

/**
 * The compact explainable period summary inside a CurrentContext (design §5):
 * the period's memory counts, the period_closures projection state and the
 * latest close memory id (empty when the period has no close).
 */
export interface CurrentContextPeriodSummary {
	total: number;
	byKind: Record<string, number>;
	byStatus: Record<string, number>;
	closureState: ClosureState;
	latestClose: string;
}

/**
 * Automatic MCP session context (design §5, mirror of core.CurrentContext):
 * the exact configured scope, the compact period summary with closure state,
 * the shared pending-item digest, the at most 20 most recent chains (latest
 * revision per chain, effectiveAt desc) and the generation timestamp.
 */
export interface CurrentContext {
	scope: MemoryScope;
	periodSummary: CurrentContextPeriodSummary;
	pendingItems: ClosePendingItem[];
	recentChains: RecentChain[];
	generatedAt: string;
}

/**
 * Month-end UTC stamp of a YYYYMM period (last day 23:59:59Z) — the canonical
 * effectiveAt of a monthly close. A malformed period fails with INVALID_PERIOD.
 */
export function monthEndUTC(period: string): string {
	if (!isValidPeriod(period)) {
		throw new Error(
			`INVALID_PERIOD: expected YYYYMM (six digits, month 01-12), got "${period}"`,
		);
	}
	const year = Number(period.slice(0, 4));
	const month = Number(period.slice(4, 6));
	const end = new Date(Date.UTC(year, month, 0, 23, 59, 59)); // month is 1-based in Date.UTC
	return end.toISOString();
}

// ──────────────────────────────────────────────
// v0.5.0 — Period-over-period comparison (design §4)
// ──────────────────────────────────────────────

/**
 * Count digest of the comparison (design §4 counts). byKindDelta/byStatusDelta
 * map each kind/status present in either period to toCount − fromCount.
 * Mirrors core.PeriodCounts.
 */
export interface PeriodCounts {
	fromTotal: number;
	toTotal: number;
	/** Delta is toTotal − fromTotal. */
	delta: number;
	byKindDelta: Record<string, number>;
	byStatusDelta: Record<string, number>;
}

/** One chain identified by topic key in a new/removed list. Mirrors
 * core.ChainRef. */
export interface ChainRef {
	topicKey: string;
	memoryId: string;
	kind: string;
	status: string;
	title: string;
}

/** One matched chain whose canonical content / envelope-relevant state differs
 * between the periods (design §4 chains.changed). Mirrors core.ChainChange. */
export interface ChainChange {
	topicKey: string;
	fromId: string;
	toId: string;
	kind: string;
	title: string;
}

/** One matched chain whose lifecycle status differs between the periods
 * (design §4 statusChanges). Mirrors core.StatusChange. */
export interface StatusChange {
	topicKey: string;
	fromId: string;
	toId: string;
	fromStatus: string;
	toStatus: string;
}

/** Chain-membership digest of the comparison (design §4 chains). Every array is
 * stable-sorted by topic key then memory ID. Mirrors core.PeriodChains. */
export interface PeriodChains {
	/** Chains present only in the `to` period. */
	new: ChainRef[];
	/** Chains present only in the `from` period. */
	removed: ChainRef[];
	/** Matched chains whose canonical content / envelope-relevant fields differ. */
	changed: ChainChange[];
	/** Matched chains with identical canonical content and envelope-relevant state. */
	unchangedCount: number;
}

/**
 * Pending-item digest delta keyed by CHAIN (topic key): a chain pending in both
 * periods carries over and is neither added nor resolved. addedIds are the `to`
 * period's current memory IDs of chains pending only in `to`; resolvedIds are
 * the `from` period's current memory IDs of chains pending only in `from`;
 * both lists are sorted ascending. Mirrors core.PendingItemsDelta.
 */
export interface PendingItemsDelta {
	from: number;
	to: number;
	delta: number;
	addedIds: string[];
	resolvedIds: string[];
}

/** period_closures projection state per period ("open" | "closed" | "reopened").
 * Mirrors core.CloseStatePair. */
export interface CloseStatePair {
	from: string;
	to: string;
}

/**
 * The pure scope-first read model of a period-over-period comparison (design
 * §4): from/to, counts, chains (new/removed/changed/unchangedCount),
 * statusChanges, pendingItems, closeState and the deterministic narrative.
 * Chains are matched by topic key after the exact scope is stripped. Mirrors
 * core.PeriodComparison.
 */
export interface PeriodComparison {
	from: string;
	to: string;
	counts: PeriodCounts;
	chains: PeriodChains;
	statusChanges: StatusChange[];
	pendingItems: PendingItemsDelta;
	closeState: CloseStatePair;
	/** Deterministic human-readable delta summary (Spanish, fixed shape). */
	narrative: string;
}

/** Maps each current memory by topic key (the exact scope is implicitly
 * stripped: within one FindByScope result every (topicKey, scope) chain is
 * unique, and the two compared scopes differ only by period). */
function indexByTopic(
	memories: AccountingMemory[],
): Map<string, AccountingMemory> {
	const out = new Map<string, AccountingMemory>();
	for (const memory of memories) {
		out.set(memory.identity.topicKey, memory);
	}
	return out;
}

/** Maps every kind present in either period to toCount − fromCount. */
function kindDeltas(
	from: AccountingMemory[],
	to: AccountingMemory[],
): Record<string, number> {
	const out: Record<string, number> = {};
	for (const memory of from) {
		out[memory.kind] = (out[memory.kind] ?? 0) - 1;
	}
	for (const memory of to) {
		out[memory.kind] = (out[memory.kind] ?? 0) + 1;
	}
	return out;
}

/** Maps every status present in either period to toCount − fromCount. */
function statusDeltas(
	from: AccountingMemory[],
	to: AccountingMemory[],
): Record<string, number> {
	const out: Record<string, number> = {};
	for (const memory of from) {
		out[memory.status] = (out[memory.status] ?? 0) - 1;
	}
	for (const memory of to) {
		out[memory.status] = (out[memory.status] ?? 0) + 1;
	}
	return out;
}

/** Builds the deterministic new/removed reference of one current memory. */
function chainRef(memory: AccountingMemory): ChainRef {
	return {
		topicKey: memory.identity.topicKey,
		memoryId: memory.identity.id,
		kind: memory.kind,
		status: memory.status,
		title: memory.title,
	};
}

/** Orders new/removed references by topic key then memory ID (stable). */
function chainRefLess(a: ChainRef, b: ChainRef): number {
	if (a.topicKey !== b.topicKey) return a.topicKey < b.topicKey ? -1 : 1;
	if (a.memoryId !== b.memoryId) return a.memoryId < b.memoryId ? -1 : 1;
	return 0;
}

function chainChangeLess(a: ChainChange, b: ChainChange): number {
	if (a.topicKey !== b.topicKey) return a.topicKey < b.topicKey ? -1 : 1;
	if (a.fromId !== b.fromId) return a.fromId < b.fromId ? -1 : 1;
	if (a.toId !== b.toId) return a.toId < b.toId ? -1 : 1;
	return 0;
}

function statusChangeLess(a: StatusChange, b: StatusChange): number {
	if (a.topicKey !== b.topicKey) return a.topicKey < b.topicKey ? -1 : 1;
	if (a.fromId !== b.fromId) return a.fromId < b.fromId ? -1 : 1;
	if (a.toId !== b.toId) return a.toId < b.toId ? -1 : 1;
	return 0;
}

/** Clone of a memory with the scope PERIOD stripped (the compared scopes differ
 * only by period — the period must never mark a chain changed on its own). */
function stripPeriod(memory: AccountingMemory): AccountingMemory {
	if (memory.scope.kind !== "company") return memory;
	return { ...memory, scope: { ...memory.scope, period: undefined } };
}

/** Canonical content hash with the scope period stripped — mirrors
 * core.strippedContentHash (reuses the same canonical computeContentHash). */
async function strippedContentHash(memory: AccountingMemory): Promise<string> {
	return computeContentHash(stripPeriod(memory));
}

/**
 * Reports whether the two current revisions of the same chain carry different
 * canonical content or envelope-relevant state between periods: the stripped
 * canonical content hash, the lifecycle status, the evidence and rule link
 * SETS (order-insensitive) and the supersedesId link. Pure write-time metadata
 * (recordedAt, revision) never marks a chain changed on its own. Mirrors
 * core.chainsDiffer.
 */
async function chainsDiffer(
	a: AccountingMemory,
	b: AccountingMemory,
): Promise<boolean> {
	if ((await strippedContentHash(a)) !== (await strippedContentHash(b))) {
		return true;
	}
	if (a.status !== b.status) return true;
	if ((a.supersedesId ?? "") !== (b.supersedesId ?? "")) return true;
	return (
		!refSetsEqual(a.evidenceRefs ?? [], b.evidenceRefs ?? []) ||
		!refSetsEqual(a.ruleRefs ?? [], b.ruleRefs ?? [])
	);
}

/** Compares two reference slices as SETS (order and duplicates are not
 * semantically meaningful for evidence/rule refs). Mirrors core.refSetsEqual. */
function refSetsEqual(a: string[], b: string[]): boolean {
	if (a.length !== b.length) return false;
	const counts = new Map<string, number>();
	for (const ref of a) {
		counts.set(ref, (counts.get(ref) ?? 0) + 1);
	}
	for (const ref of b) {
		const next = (counts.get(ref) ?? 0) - 1;
		if (next < 0) return false;
		counts.set(ref, next);
	}
	return true;
}

/** Derives the pending-item digest delta keyed by CHAIN (topic key), matching
 * the design's chain-matching rule: a pending item that carries over across
 * periods is the same chain even though its current revision (memory ID)
 * differs — neither added nor resolved. Both lists are sorted ascending.
 * Mirrors core.pendingDelta. */
function pendingDelta(
	fromPending: ClosePendingItem[],
	toPending: ClosePendingItem[],
): PendingItemsDelta {
	const fromByTopic = new Map<string, string>();
	for (const item of fromPending) {
		fromByTopic.set(item.topicKey, item.memoryId);
	}
	const toByTopic = new Map<string, string>();
	for (const item of toPending) {
		toByTopic.set(item.topicKey, item.memoryId);
	}
	const added: string[] = [];
	const resolved: string[] = [];
	for (const [topic, id] of toByTopic.entries()) {
		if (!fromByTopic.has(topic)) added.push(id);
	}
	for (const [topic, id] of fromByTopic.entries()) {
		if (!toByTopic.has(topic)) resolved.push(id);
	}
	added.sort();
	resolved.sort();
	return {
		from: fromPending.length,
		to: toPending.length,
		delta: toPending.length - fromPending.length,
		addedIds: added,
		resolvedIds: resolved,
	};
}

/** Formats n with an explicit + for positive values (the narrative delta
 * convention: "+2", "0", "-1"). Mirrors core.signed. */
function signed(n: number): string {
	return n > 0 ? `+${itoa(n)}` : itoa(n);
}

/** Small int formatter for the narrative. Mirrors core.itoa. */
function itoa(n: number): string {
	if (n === 0) return "0";
	const negative = n < 0;
	let abs = Math.abs(n);
	const digits: string[] = [];
	while (abs > 0) {
		digits.unshift(String(abs % 10));
		abs = Math.floor(abs / 10);
	}
	return `${negative ? "-" : ""}${digits.join("")}`;
}

/** The deterministic human-readable delta summary (design §4 narrative — a
 * self-contained delta shape that never depends on memory content or ordering).
 * Mirrors core.comparisonNarrative. */
function comparisonNarrative(c: PeriodComparison): string {
	let sb = "";
	sb += "Comparacion ";
	sb += c.from;
	sb += " → ";
	sb += c.to;
	sb += ": ";
	sb += itoa(c.counts.fromTotal);
	sb += " memorias en el periodo de origen, ";
	sb += itoa(c.counts.toTotal);
	sb += " en el de destino (delta ";
	sb += signed(c.counts.delta);
	sb += "); cadenas nuevas: ";
	sb += itoa(c.chains.new.length);
	sb += ", removidas: ";
	sb += itoa(c.chains.removed.length);
	sb += ", cambiadas: ";
	sb += itoa(c.chains.changed.length);
	sb += ", sin cambios: ";
	sb += itoa(c.chains.unchangedCount);
	sb += "; cambios de estado: ";
	sb += itoa(c.statusChanges.length);
	sb += "; items pendientes: ";
	sb += itoa(c.pendingItems.from);
	sb += " → ";
	sb += itoa(c.pendingItems.to);
	sb += " (delta ";
	sb += signed(c.pendingItems.delta);
	sb += "); estado de cierre: ";
	sb += c.closeState.from;
	sb += " → ";
	sb += c.closeState.to;
	sb += ".";
	return sb;
}

/**
 * Derives the deterministic period-over-period delta from the two periods'
 * CURRENT memories (latest revision per chain), pending item digests and
 * closure states. Pure function: no store, no clock, no I/O — same inputs
 * always produce byte-identical output (arrays stable-sorted by topic key then
 * memory ID). Mirrors core.ComputePeriodComparison.
 */
export async function computePeriodComparison(
	fromPeriod: string,
	toPeriod: string,
	from: AccountingMemory[],
	to: AccountingMemory[],
	fromPending: ClosePendingItem[],
	toPending: ClosePendingItem[],
	fromCloseState: string,
	toCloseState: string,
): Promise<PeriodComparison> {
	const fromByTopic = indexByTopic(from);
	const toByTopic = indexByTopic(to);

	const comparison: PeriodComparison = {
		from: fromPeriod,
		to: toPeriod,
		counts: {
			fromTotal: from.length,
			toTotal: to.length,
			delta: to.length - from.length,
			byKindDelta: kindDeltas(from, to),
			byStatusDelta: statusDeltas(from, to),
		},
		chains: { new: [], removed: [], changed: [], unchangedCount: 0 },
		statusChanges: [],
		pendingItems: pendingDelta(fromPending, toPending),
		closeState: { from: fromCloseState, to: toCloseState },
		narrative: "",
	};

	for (const [topicKey, toMem] of toByTopic.entries()) {
		const fromMem = fromByTopic.get(topicKey);
		if (fromMem === undefined) {
			comparison.chains.new.push(chainRef(toMem));
			continue;
		}
		if (await chainsDiffer(fromMem, toMem)) {
			comparison.chains.changed.push({
				topicKey,
				fromId: fromMem.identity.id,
				toId: toMem.identity.id,
				kind: toMem.kind,
				title: toMem.title,
			});
		} else {
			comparison.chains.unchangedCount++;
		}
		if (fromMem.status !== toMem.status) {
			comparison.statusChanges.push({
				topicKey,
				fromId: fromMem.identity.id,
				toId: toMem.identity.id,
				fromStatus: fromMem.status,
				toStatus: toMem.status,
			});
		}
	}
	for (const [topicKey, fromMem] of fromByTopic.entries()) {
		if (!toByTopic.has(topicKey)) {
			comparison.chains.removed.push(chainRef(fromMem));
		}
	}

	comparison.chains.new.sort(chainRefLess);
	comparison.chains.removed.sort(chainRefLess);
	comparison.chains.changed.sort(chainChangeLess);
	comparison.statusChanges.sort(statusChangeLess);

	comparison.narrative = comparisonNarrative(comparison);
	return comparison;
}
