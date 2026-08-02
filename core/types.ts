/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Core memory model — the observation unit of institutional accounting memory.
 * Implements contracts/memory.md, contracts/scope.md, contracts/lifecycle.md and
 * contracts/provenance.md for the first vertical slice.
 *
 * Contract-to-code mapping:
 * - `authorityStatus` is the contract's lifecycle state
 *   (`draft → reviewed → promoted → superseded`).
 * - `validity` is the contract's vigencia (effective/expiry window).
 * - `identity.topicKey` is the contract's topic_key (the upsert target).
 * - Scope equality is exact: two observations differing only in scope are
 *   different observations (scope.md rule 5), and `period` participates in that
 *   equality. Single `YYYYMM` periods only in this slice; ranges are future.
 */

// ──────────────────────────────────────────────
// Scope — structural tenant isolation
// ──────────────────────────────────────────────

/**
 * Fiscal scope of an observation (contracts/scope.md).
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

/** Canonical identity: a stable observation id plus the upsert topic key. */
export interface MemoryIdentity {
  id: string;
  topicKey: string;
}

// ──────────────────────────────────────────────
// Content / provenance / validity
// ──────────────────────────────────────────────

/** Structured content — the canonical `What / Why / Where / Learned` shape. */
export interface MemoryContent {
  what: string;
  why: string;
  where: string;
  learned: string;
}

/** Audit metadata captured at creation; not editable afterward. */
export interface MemoryProvenance {
  actor: string;
  /** UTC ISO-8601 creation time. */
  timestamp: string;
  source: string;
  session?: string;
}

/** Vigencia: effective/expiry window. Expired observations surface as stale. */
export interface MemoryValidity {
  effectiveAt?: string;
  expiresAt?: string;
}

// ──────────────────────────────────────────────
// Lifecycle / relations / writes
// ──────────────────────────────────────────────

/** Observation lifecycle state (contracts/lifecycle.md). */
export type MemoryAuthorityStatus =
  | "draft"
  | "reviewed"
  | "promoted"
  | "superseded";

/** Relation vocabulary between observations (contracts/memory.md, lifecycle.md). */
export type MemoryRelation =
  | "related"
  | "compatible"
  | "scoped"
  | "conflicts_with"
  | "supersedes"
  | "not_conflict";

/** Write outcome. `conflict` and `unknown` are documented fallback outcomes. */
export type MemoryWriteOutcome = "created" | "updated" | "conflict" | "unknown";

// ──────────────────────────────────────────────
// Observation / results
// ──────────────────────────────────────────────

/** A stored observation. Content/scope/provenance are immutable once created. */
export interface MemoryObservation {
  identity: MemoryIdentity;
  title: string;
  type: string;
  scope: MemoryScope;
  content: MemoryContent;
  authorityStatus: MemoryAuthorityStatus;
  validity?: MemoryValidity;
  provenance: MemoryProvenance;
  /** 1-based revision within the (topicKey, scope) chain; JSON integer. */
  revision: number;
}

/** Result of a save (upsert) operation. */
export interface MemoryWriteResult {
  observation: MemoryObservation;
  outcome: MemoryWriteOutcome;
}

/** Input for saving/upserting under a topic key + exact scope. */
export interface SaveMemoryInput {
  topicKey: string;
  title: string;
  type: string;
  scope: MemoryScope;
  content: MemoryContent;
  authorityStatus?: MemoryAuthorityStatus;
  validity?: MemoryValidity;
  provenance: MemoryProvenance;
}

/** A recorded relation between two observations. */
export interface MemoryRelationRecord {
  fromId: string;
  toId: string;
  relation: MemoryRelation;
  actor?: string;
  timestamp?: string;
}

/** One entry of the lifecycle audit trail. */
export interface StatusTransitionRecord {
  observationId: string;
  from: MemoryAuthorityStatus;
  to: MemoryAuthorityStatus;
  actor: string;
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

/** Defensive copy — stored observations are never handed out by reference. */
export function cloneObservation(observation: MemoryObservation): MemoryObservation {
  return {
    identity: { ...observation.identity },
    title: observation.title,
    type: observation.type,
    scope: cloneScope(observation.scope),
    content: { ...observation.content },
    authorityStatus: observation.authorityStatus,
    ...(observation.validity === undefined
      ? {}
      : { validity: { ...observation.validity } }),
    provenance: { ...observation.provenance },
    revision: observation.revision,
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
// Validation (scope.md: RUC 11 digits, period YYYYMM)
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
    throw new Error(`INVALID_RUC: expected exactly 11 digits, got "${scope.ruc}"`);
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
    if (typeof content[field] !== "string" || content[field].trim().length === 0) {
      throw new Error(`INVALID_CONTENT: field "${field}" must be a non-empty string`);
    }
  }
}

/** Provenance is captured at creation and must be traceable. */
export function assertValidProvenance(provenance: MemoryProvenance): void {
  if (typeof provenance.actor !== "string" || provenance.actor.length === 0) {
    throw new Error("INVALID_PROVENANCE: actor must be a non-empty string");
  }
  if (
    typeof provenance.timestamp !== "string" ||
    Number.isNaN(Date.parse(provenance.timestamp))
  ) {
    throw new Error("INVALID_PROVENANCE: timestamp must be a parseable date string");
  }
  if (typeof provenance.source !== "string" || provenance.source.length === 0) {
    throw new Error("INVALID_PROVENANCE: source must be a non-empty string");
  }
}

/** Vigencia dates must parse; malformed windows fail closed at write time. */
export function assertValidValidity(validity: MemoryValidity | undefined): void {
  if (validity === undefined) return;
  if (
    validity.effectiveAt !== undefined &&
    Number.isNaN(Date.parse(validity.effectiveAt))
  ) {
    throw new Error("INVALID_VALIDITY: effectiveAt must be a parseable date string");
  }
  if (
    validity.expiresAt !== undefined &&
    Number.isNaN(Date.parse(validity.expiresAt))
  ) {
    throw new Error("INVALID_VALIDITY: expiresAt must be a parseable date string");
  }
}
