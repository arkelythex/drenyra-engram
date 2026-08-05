// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. The memory model carries an optional
// Materiality threshold as int64 cents; Confidence is a float64 probability
// (0..1), never a monetary value.
//
// Core memory model v2 — the unit of institutional accounting memory.
// Implements contracts/memory.md, contracts/scope.md, contracts/lifecycle.md and
// contracts/provenance.md (frozen-for-0.1 semantics carried into the standalone
// Go engine per ADR-001, extended by the v2 AccountingMemory model).
//
// v2 model (approved design — do not redesign):
//   - MemoryKind replaces the generic v1 `type` (8 accounting kinds).
//   - MemoryStatus replaces v1 AuthorityStatus (6 states, approval-gated).
//   - FiscalEffect classifies fiscal impact and drives the human-approval gate:
//     a memory with fiscalEffect != none is saved as pending_review and can only
//     reach approved via a HUMAN actor (actorKind == human).
//   - Triple timestamps: EffectiveAt (when it happened accounting-wise),
//     RecordedAt (when it entered the system — automatic, immutable),
//     ObservedAt (when it was detected — optional).
//   - Source replaces v1 Provenance (structured provenance with actorKind).
//   - ContentHash is the canonical SHA-256 of the immutable content; id, status,
//     recordedAt and revision never participate.
//   - Relations vocabulary extended from 6 to 17 (accounting evidence graph).
//
// Contract-to-code mapping:
//   - MemoryStatus is the contract's lifecycle state (v2).
//   - Validity is the contract's vigencia (effective/expiry window).
//   - Identity.TopicKey is the contract's topic_key (the upsert target).
//   - Scope equality is exact: two memories differing only in scope are
//     different memories (scope.md rule 5), and period participates in that
//     equality. Single YYYYMM periods only in this slice; ranges are future.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ──────────────────────────────────────────────
// Scope — structural tenant isolation
// ──────────────────────────────────────────────

// ScopeKind discriminates a company-scoped memory from an institutional one.
type ScopeKind string

const (
	// ScopeKindCompany scopes a memory to an organization/company/RUC/period;
	// invisible to queries from any other company (structural isolation, not a
	// post-filter).
	ScopeKindCompany ScopeKind = "company"
	// ScopeKindInstitutional declares explicitly cross-company knowledge; it is
	// only surfaced when the query scope is institutional or the caller
	// explicitly asks for it (scope.md rule 3).
	ScopeKindInstitutional ScopeKind = "institutional"
)

// Scope is the fiscal scope of a memory (contracts/scope.md).
//
// For kind=company: Period is the single fiscal period `YYYYMM` when present
// (empty when absent); ranges are future. For kind=institutional all the
// company fields are empty.
type Scope struct {
	Kind           ScopeKind `json:"kind"`
	OrganizationID string    `json:"organizationId,omitempty"`
	CompanyID      string    `json:"companyId,omitempty"`
	// RUC is the Peruvian RUC — exactly 11 digits in this slice (checksum:
	// future).
	RUC    string `json:"ruc,omitempty"`
	Period string `json:"period,omitempty"`
}

// ──────────────────────────────────────────────
// Identity
// ──────────────────────────────────────────────

// Identity is the canonical identity: a stable memory id plus the upsert
// topic key.
type Identity struct {
	ID       string `json:"id"`
	TopicKey string `json:"topicKey"`
}

// ──────────────────────────────────────────────
// v2 vocabulary
// ──────────────────────────────────────────────

// MemoryKind classifies the accounting nature of a memory (v2). It replaces the
// generic v1 `type`.
type MemoryKind string

const (
	// KindFact is a directly observed accounting fact (a comprobante was issued,
	// a balance, a supplier status at query time).
	KindFact MemoryKind = "fact"
	// KindEvidence is a document or result backing a fact (XML, PDF, CDR, bank
	// statement, SUNAT response, RUC query capture, SIRE file, contract, email).
	KindEvidence MemoryKind = "evidence"
	// KindDecision is a professional judgment made by a person or agent
	// (classified as expense, credit deferred, policy applied).
	KindDecision MemoryKind = "decision"
	// KindRule is the policy or disposition applied (SUNAT rule, internal policy,
	// IFRS, materiality threshold, client configuration).
	KindRule MemoryKind = "rule"
	// KindException is something that does not fit or needs intervention (XML vs
	// PDF amounts differ, invoice missing from SIRE, bank balance mismatch).
	KindException MemoryKind = "exception"
	// KindControl is a validation executed and its result (duplicity PASS, closed
	// period BLOCKED, tenant PASS, minimum evidence FAIL).
	KindControl MemoryKind = "control"
	// KindObligation is a future event derived from what is known (file PDT 621,
	// request vendor substantiation, review an estimate, renew a certificate,
	// reconcile an account).
	KindObligation MemoryKind = "obligation"
	// KindSummary is a closing or executed-work summary (monthly close memory,
	// mission summary).
	KindSummary MemoryKind = "summary"
)

// IsValidMemoryKind reports whether kind is a known v2 memory kind. An empty
// kind is INVALID for v2 writes — the caller must classify; only the v1→v2
// migration derives a kind automatically.
func IsValidMemoryKind(kind MemoryKind) bool {
	switch kind {
	case KindFact, KindEvidence, KindDecision, KindRule,
		KindException, KindControl, KindObligation, KindSummary:
		return true
	}
	return false
}

// MemoryStatus is the lifecycle state of a memory (v2). It replaces v1
// AuthorityStatus.
type MemoryStatus string

const (
	// StatusActive is an informative memory, current and effective (fiscalEffect
	// == none). No approval gate.
	StatusActive MemoryStatus = "active"
	// StatusPendingReview is a memory with fiscal effect (fiscalEffect != none)
	// waiting for explicit human approval.
	StatusPendingReview MemoryStatus = "pending_review"
	// StatusApproved is a memory approved by a human actor.
	StatusApproved MemoryStatus = "approved"
	// StatusRejected is a memory rejected by a human actor. Terminal.
	StatusRejected MemoryStatus = "rejected"
	// StatusSuperseded is a memory replaced by a newer revision of the same
	// (topicKey, scope). Terminal; readers route to the successor.
	StatusSuperseded MemoryStatus = "superseded"
	// StatusVoided is a memory rendered inoperative without a successor
	// (correction/annulment). Terminal.
	StatusVoided MemoryStatus = "voided"
)

// IsValidMemoryStatus reports whether status is a known v2 memory status.
func IsValidMemoryStatus(status MemoryStatus) bool {
	switch status {
	case StatusActive, StatusPendingReview, StatusApproved,
		StatusRejected, StatusSuperseded, StatusVoided:
		return true
	}
	return false
}

// FiscalEffect classifies the fiscal impact of a memory (v2). A non-none effect
// drives the mandatory human-approval gate: the memory is saved as
// pending_review and can only reach approved via a human actor. It is a
// classifier only — monetary amounts live in the structured content (int64
// cents), never in this field.
type FiscalEffect string

const (
	// FiscalEffectNone marks an informative memory with no direct accounting
	// effect; saved directly as active.
	FiscalEffectNone FiscalEffect = "none"
	// FiscalEffectJournalEntry marks a memory that posts an accounting entry
	// (asiento).
	FiscalEffectJournalEntry FiscalEffect = "journal_entry"
	// FiscalEffectDeclaration marks a declared filing (declaración).
	FiscalEffectDeclaration FiscalEffect = "declaration"
	// FiscalEffectClosing marks a monthly close (cierre).
	FiscalEffectClosing FiscalEffect = "closing"
	// FiscalEffectAdjustment marks an adjustment (ajuste).
	FiscalEffectAdjustment FiscalEffect = "adjustment"
	// FiscalEffectReclassification marks a reclassification (reclasificación).
	FiscalEffectReclassification FiscalEffect = "reclassification"
	// FiscalEffectApproval marks an approval event (aprobación).
	FiscalEffectApproval FiscalEffect = "approval"
	// FiscalEffectSunatFiling marks a SUNAT submission (presentación SUNAT).
	FiscalEffectSunatFiling FiscalEffect = "sunat_filing"
)

// MaterialityLevel is the DECLARED materiality classification of a memory
// (v0.4.0 Step 1). It is set by the writing agent; NULL (unset) is treated as
// normal by the approval policy. The policy classifies by this level and NEVER
// reinterprets the Materiality *int64 threshold field (frozen decision,
// 2026-08-05).
type MaterialityLevel string

const (
	// MaterialityNormal is the default classification (NULL = normal).
	MaterialityNormal MaterialityLevel = "normal"
	// MaterialityMaterial requires a senior accountant under the v0.4.0 policy.
	MaterialityMaterial MaterialityLevel = "material"
	// MaterialityCritical requires a controller under the v0.4.0 policy.
	MaterialityCritical MaterialityLevel = "critical"
)

// IsValidMaterialityLevel reports whether level is a known materiality
// classification. An empty level is INVALID for v2 writes; NULL is represented
// by a nil pointer, never by an empty string.
func IsValidMaterialityLevel(level MaterialityLevel) bool {
	switch level {
	case MaterialityNormal, MaterialityMaterial, MaterialityCritical:
		return true
	}
	return false
}

// IsValidFiscalEffect reports whether effect is a known v2 fiscal effect.
// An empty effect is INVALID for v2 writes; callers must classify (none is
// explicit).
func IsValidFiscalEffect(effect FiscalEffect) bool {
	switch effect {
	case FiscalEffectNone, FiscalEffectJournalEntry, FiscalEffectDeclaration,
		FiscalEffectClosing, FiscalEffectAdjustment, FiscalEffectReclassification,
		FiscalEffectApproval, FiscalEffectSunatFiling:
		return true
	}
	return false
}

// ActorKind discriminates who originated or decided a memory.
type ActorKind string

const (
	// ActorKindHuman is a professional (contador). Only humans can approve or
	// reject gated memories.
	ActorKindHuman ActorKind = "human"
	// ActorKindAgent is an autonomous software agent.
	ActorKindAgent ActorKind = "agent"
	// ActorKindSystem is a deterministic system event.
	ActorKindSystem ActorKind = "system"
)

// IsValidActorKind reports whether kind is a known actor kind.
func IsValidActorKind(kind ActorKind) bool {
	switch kind {
	case ActorKindHuman, ActorKindAgent, ActorKindSystem:
		return true
	}
	return false
}

// ──────────────────────────────────────────────
// Content / source / validity
// ──────────────────────────────────────────────

// Content is the structured content — the canonical What/Why/Where/Learned
// shape (contracts/memory.md rule 1).
type Content struct {
	What    string `json:"what"`
	Why     string `json:"why"`
	Where   string `json:"where"`
	Learned string `json:"learned"`
}

// Source is the structured provenance of a memory (v2). It replaces the flat v1
// Provenance: system (which system produced the event), an external reference,
// the actor identity and kind, the model for agent actors, and the session.
type Source struct {
	// System is REQUIRED: the system that produced the event (e.g. "drenyra-core",
	// "sire", "manual").
	System string `json:"system"`
	// Reference is an optional external reference (e.g. "F001-948",
	// "AJ-2026-07-019").
	Reference string `json:"reference,omitempty"`
	// ActorID identifies who (user/agent/system id). REQUIRED for human actors.
	ActorID string `json:"actorId,omitempty"`
	// ActorKind is REQUIRED: human | agent | system.
	ActorKind ActorKind `json:"actorKind"`
	// Model is the agent model when the actor is an agent.
	Model string `json:"model,omitempty"`
	// Session identifies the agent/session continuity context.
	Session string `json:"session,omitempty"`
}

// Validity is the vigencia window: effective/expiry. Expired memories surface
// as stale at read time, never as current fact.
//
// Source records the PROVENANCE of the vigencia (frozen decision, v0.3.0):
//   - "declared"                    — written explicitly by a v2 caller
//   - "migrated_from_effective_at_v1" — inferred during the v1→v2 migration
//     (the v1 effective_at doubled as the vigencia start)
//
// An audit can therefore distinguish a vigencia originally confirmed from one
// inferred historically — the inference never masquerades as declared data.
type Validity struct {
	EffectiveAt string `json:"effectiveAt,omitempty"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
	Source      string `json:"source,omitempty"`
}

// ──────────────────────────────────────────────
// Relations
// ──────────────────────────────────────────────

// Relation is the relation vocabulary between memories: the six legacy generic
// relations plus eleven accounting-evidence relations (v2) — 17 total.
type Relation string

const (
	// ── legacy (v1, unchanged) ──
	RelationRelated       Relation = "related"
	RelationCompatible    Relation = "compatible"
	RelationScoped        Relation = "scoped"
	RelationConflictsWith Relation = "conflicts_with"
	RelationSupersedes    Relation = "supersedes"
	RelationNotConflict   Relation = "not_conflict"
	// ── v2 accounting-evidence vocabulary ──
	// RelationSupports: A supports B — argument/evidence backs a memory.
	RelationSupports Relation = "supports"
	// RelationContradicts: A contradicts B — explicit contradiction.
	RelationContradicts Relation = "contradicts"
	// RelationExplains: A explains B — A provides the rationale for B.
	RelationExplains Relation = "explains"
	// RelationDerivedFrom: A derives from B — a computed memory from a base.
	RelationDerivedFrom Relation = "derived_from"
	// RelationPostedAs: A posted as B — an entry posted as a journal entry.
	RelationPostedAs Relation = "posted_as"
	// RelationReconciles: A reconciles B — a reconciliation matches a balance.
	RelationReconciles Relation = "reconciles"
	// RelationReverses: A reverses B — a reversal entry of B.
	RelationReverses Relation = "reverses"
	// RelationRequires: A requires B — a rule/obligation requires an action.
	RelationRequires Relation = "requires"
	// RelationViolates: A violates B — a memory violates a rule.
	RelationViolates Relation = "violates"
	// RelationApprovedBy: A approved by B — approval provenance (human actor).
	RelationApprovedBy Relation = "approved_by"
	// RelationRejectedBy: A rejected by B — rejection provenance (human actor).
	RelationRejectedBy Relation = "rejected_by"
)

// IsValidRelation reports whether relation is a known relation vocabulary
// member (17 total).
func IsValidRelation(relation Relation) bool {
	switch relation {
	case RelationRelated, RelationCompatible, RelationScoped,
		RelationConflictsWith, RelationSupersedes, RelationNotConflict,
		RelationSupports, RelationContradicts, RelationExplains,
		RelationDerivedFrom, RelationPostedAs, RelationReconciles,
		RelationReverses, RelationRequires, RelationViolates,
		RelationApprovedBy, RelationRejectedBy:
		return true
	}
	return false
}

// ──────────────────────────────────────────────
// AccountingMemory — the v2 model
// ──────────────────────────────────────────────

// AccountingMemory is a stored institutional accounting memory. Kind, Scope,
// Content, timestamps, Source and ContentHash are immutable once written;
// Status is the single field the lifecycle machine may transition, and
// EvidenceRefs/RuleRefs grow only through dedicated link records (never by
// mutating a stored memory).
type AccountingMemory struct {
	Identity Identity     `json:"identity"`
	Title    string       `json:"title"`
	Kind     MemoryKind   `json:"kind"`
	Scope    Scope        `json:"scope"`
	Content  Content      `json:"content"`
	Status   MemoryStatus `json:"status"`
	// FiscalEffect classifies the fiscal impact; non-none triggers the
	// human-approval gate (pending_review until a human approves).
	FiscalEffect FiscalEffect `json:"fiscalEffect"`
	// EffectiveAt is when the event happened ACCOUNTING-WISE (contablemente).
	// Critical for late events that affect a previous closed period.
	EffectiveAt string `json:"effectiveAt"`
	// RecordedAt is when the memory entered the system — automatic and immutable.
	RecordedAt string `json:"recordedAt"`
	// ObservedAt is when the event was detected (optional).
	ObservedAt string `json:"observedAt,omitempty"`
	// Source is the structured provenance (v2).
	Source Source `json:"source"`
	// Validity is the vigencia window (optional).
	Validity *Validity `json:"validity,omitempty"`
	// EvidenceRefs names evidence objects (XML/PDF/CDR/extracto) backing this
	// memory. Immutable at write; grows via link records.
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
	// RuleRefs names the policy/rule paths applied (e.g.
	// "policy/igv/late-document-v3"). Immutable at write; grows via link records.
	RuleRefs []string `json:"ruleRefs,omitempty"`
	// Confidence is an optional 0..1 probability (never money).
	Confidence *float64 `json:"confidence,omitempty"`
	// Materiality is an optional monetary threshold in int64 cents (never float).
	Materiality *int64 `json:"materiality,omitempty"`
	// MaterialityLevel is the DECLARED classification (normal | material |
	// critical), set by the writing agent; NULL = normal. v0.4.0 approval policy
	// classifies by this level; the Materiality threshold is never reinterpreted
	// by policy. NOT persisted yet (v3 schema batch); it does NOT participate in
	// the envelope hash (frozen decision).
	MaterialityLevel *MaterialityLevel `json:"materialityLevel,omitempty"`
	// CloseSnapshot is the OPTIONAL structured payload of a monthly close memory
	// (kind=summary, fiscalEffect=closing — v0.5.0, design §2.1). Canonical bytes
	// are persisted verbatim in observations.close_snapshot_json (schema v6) and
	// PARTICIPATE in the content and envelope hashes; nil on every non-close
	// memory (contributes the empty string, so pre-v6 envelopes are unchanged).
	CloseSnapshot *CloseSnapshot `json:"closeSnapshot,omitempty"`
	// ContentHash is the canonical SHA-256 of the semantic content (see
	// ComputeContentHash). Computed at write, never editable.
	ContentHash string `json:"contentHash"`
	// IdentityHash is the SHA-256 of the DOMAIN identity: tenant/company/period
	// scope + topicKey + effectiveAt + source reference. Two memories with the
	// same IdentityHash represent the same domain thing in the same scope (see
	// ComputeIdentityHash).
	IdentityHash string `json:"identityHash,omitempty"`
	// EnvelopeHash is the SHA-256 of everything signable/verifiable: identity +
	// content + fiscal effect + source + evidence/rule refs + timestamps +
	// supersession (see ComputeEnvelopeHash). The import decides duplicate vs
	// conflict on it.
	EnvelopeHash string `json:"envelopeHash,omitempty"`
	// ReceiptID references the Ed25519 receipt issued by the Drenyra ecosystem.
	// Only a reference — this engine never signs (non-authorization boundary).
	ReceiptID string `json:"receiptId,omitempty"`
	// SupersedesID is the id of the memory this one replaces (set on the
	// successor at supersession time).
	SupersedesID string `json:"supersedesId,omitempty"`
	// Revision is the 1-based revision within the (topicKey, scope) chain; a
	// JSON integer, never a float.
	Revision int `json:"revision"`
}

// SaveInput is the input for saving/upserting under a topic key + exact scope.
// Status and RecordedAt are derived by the engine (approval gate + clock); they
// are never caller-supplied.
type SaveInput struct {
	TopicKey string     `json:"topicKey"`
	Title    string     `json:"title"`
	Kind     MemoryKind `json:"kind"`
	Scope    Scope      `json:"scope"`
	Content  Content    `json:"content"`
	// FiscalEffect drives the approval gate: != none → pending_review.
	FiscalEffect FiscalEffect `json:"fiscalEffect"`
	// EffectiveAt is when the event happened accounting-wise. REQUIRED when
	// FiscalEffect != none (defaults to the record time otherwise).
	EffectiveAt string    `json:"effectiveAt"`
	ObservedAt  string    `json:"observedAt,omitempty"`
	Source      Source    `json:"source"`
	Validity    *Validity `json:"validity,omitempty"`
	// RuleRefs names the policy/rule paths applied at write time (e.g.
	// "policy/igv/late-document-v3"). Written once with the memory; grows via
	// link records (immutability).
	RuleRefs    []string `json:"ruleRefs,omitempty"`
	Confidence  *float64 `json:"confidence,omitempty"`
	Materiality *int64   `json:"materiality,omitempty"`
	// MaterialityLevel is the DECLARED materiality classification
	// (normal | material | critical), set by the writing agent; NULL is treated
	// as normal by the approval policy. Persisted in the v3
	// observations.materiality_level column; it does NOT participate in the
	// envelope hash (frozen decision).
	MaterialityLevel *MaterialityLevel `json:"materialityLevel,omitempty"`
	// CloseSnapshot is the OPTIONAL structured payload of a monthly close memory
	// (kind=summary, fiscalEffect=closing — v0.5.0, design §2.1). Canonical bytes
	// are persisted verbatim in observations.close_snapshot_json (schema v6) and
	// PARTICIPATE in the content and envelope hashes; nil on every non-close
	// memory (contributes the empty string, so pre-v6 envelopes are unchanged).
	// HTTP/MCP/CLI never construct closing memories through generic save: the
	// CreateClose service is the canonical path (design §2.1).
	CloseSnapshot *CloseSnapshot `json:"closeSnapshot,omitempty"`
	ReceiptID     string         `json:"receiptId,omitempty"`
}

// WriteOutcome is the save (upsert) outcome. Conflict and Unknown are the
// documented fallback outcomes (contracts/memory.md frozen semantics).
type WriteOutcome string

const (
	WriteCreated  WriteOutcome = "created"
	WriteUpdated  WriteOutcome = "updated"
	WriteConflict WriteOutcome = "conflict" // reserved for a future OCC slice
	WriteUnknown  WriteOutcome = "unknown"
)

// WriteResult is the result of a save (upsert) operation.
type WriteResult struct {
	Memory  AccountingMemory `json:"memory"`
	Outcome WriteOutcome     `json:"outcome"`
}

// RelationMeta carries the optional actor/timestamp of a relation record.
type RelationMeta struct {
	Actor     string
	Timestamp string
}

// RelationRecord is a recorded relation between two memories.
type RelationRecord struct {
	FromID    string   `json:"fromId"`
	ToID      string   `json:"toId"`
	Relation  Relation `json:"relation"`
	Actor     string   `json:"actor,omitempty"`
	Timestamp string   `json:"timestamp,omitempty"`
}

// EvidenceLink is one evidence attachment (v2): an evidence reference linked to
// a memory AFTER creation, without mutating the immutable memory. Stored in the
// dedicated evidence_links table.
type EvidenceLink struct {
	MemoryID  string `json:"memoryId"`
	Ref       string `json:"ref"`
	Actor     string `json:"actor,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// StatusTransitionRecord is one entry of the lifecycle audit trail
// (contracts/provenance.md rule 3: every state traces to actor+time).
type StatusTransitionRecord struct {
	MemoryID  string       `json:"memoryId"`
	From      MemoryStatus `json:"from"`
	To        MemoryStatus `json:"to"`
	Actor     string       `json:"actor"`
	ActorKind ActorKind    `json:"actorKind"`
	Timestamp string       `json:"timestamp"`
}

// ──────────────────────────────────────────────
// Legacy types (v1) — migration/read-compat only, deprecated
// ──────────────────────────────────────────────

// Provenance is the flat v1 provenance shape, retained ONLY for the v1→v2
// migration and legacy JSON reads. v2 uses Source.
type Provenance struct {
	Actor     string `json:"actor"`
	Timestamp string `json:"timestamp"` // UTC ISO-8601 creation time
	Source    string `json:"source"`
	Session   string `json:"session,omitempty"`
}

// AuthorityStatus is the v1 lifecycle state, retained ONLY for migration and
// legacy JSON reads. v2 uses MemoryStatus.
type AuthorityStatus string

const (
	StatusDraft    AuthorityStatus = "draft"
	StatusReviewed AuthorityStatus = "reviewed"
	StatusPromoted AuthorityStatus = "promoted"
	// LegacySuperseded is the v1 superseded state (renamed to avoid colliding
	// with the v2 MemoryStatus constant of the same value).
	LegacySuperseded AuthorityStatus = "superseded"
)

// ──────────────────────────────────────────────
// Scope helpers
// ──────────────────────────────────────────────

// ScopeEquals is exact scope equality. Period participates: "" === "" but a
// perioded scope never equals an unperioded one (scope is part of identity).
func ScopeEquals(a, b Scope) bool {
	if a.Kind == ScopeKindInstitutional && b.Kind == ScopeKindInstitutional {
		return true
	}
	if a.Kind != ScopeKindCompany || b.Kind != ScopeKindCompany {
		return false
	}
	return a.OrganizationID == b.OrganizationID &&
		a.CompanyID == b.CompanyID &&
		a.RUC == b.RUC &&
		a.Period == b.Period
}

// ScopeKey is the canonical serialization of a scope, used to key upsert
// chains.
func ScopeKey(s Scope) string {
	if s.Kind == ScopeKindInstitutional {
		return "institutional"
	}
	return "company\x00" + s.OrganizationID + "\x00" + s.CompanyID + "\x00" + s.RUC + "\x00" + s.Period
}

// copyCountMap returns a defensive copy of a string→int count map (nil input
// stays nil).
func copyCountMap(in map[string]int) map[string]int {
	if in == nil {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// CloneMemory is a defensive copy — stored memories are never handed out by
// reference.
func CloneMemory(m AccountingMemory) AccountingMemory {
	cloned := m
	if m.Validity != nil {
		v := *m.Validity
		cloned.Validity = &v
	}
	if m.Confidence != nil {
		c := *m.Confidence
		cloned.Confidence = &c
	}
	if m.Materiality != nil {
		mat := *m.Materiality
		cloned.Materiality = &mat
	}
	if m.MaterialityLevel != nil {
		ml := *m.MaterialityLevel
		cloned.MaterialityLevel = &ml
	}
	if m.CloseSnapshot != nil {
		cloned.CloseSnapshot = CloneCloseSnapshot(m.CloseSnapshot)
	}
	cloned.EvidenceRefs = append([]string(nil), m.EvidenceRefs...)
	cloned.RuleRefs = append([]string(nil), m.RuleRefs...)
	return cloned
}

// CloneCloseSnapshot returns a defensive deep copy of a CloseSnapshot: the maps
// and slices are copied so callers can never mutate a stored snapshot through a
// clone (shared by CloneMemory, the store's save path and any adapter). A nil
// snapshot stays nil (the empty contribution keeps pre-v6 envelopes unchanged).
func CloneCloseSnapshot(s *CloseSnapshot) *CloseSnapshot {
	if s == nil {
		return nil
	}
	snap := *s
	snap.Counts.ByKind = copyCountMap(s.Counts.ByKind)
	snap.Counts.ByStatus = copyCountMap(s.Counts.ByStatus)
	snap.Totals = append([]CloseTotal(nil), s.Totals...)
	for i := range snap.Totals {
		snap.Totals[i].SourceMemoryIDs = append([]string(nil), s.Totals[i].SourceMemoryIDs...)
	}
	snap.PendingItems = append([]ClosePendingItem(nil), s.PendingItems...)
	snap.NarrativeMemoryIDs = append([]string(nil), s.NarrativeMemoryIDs...)
	return &snap
}

// ──────────────────────────────────────────────
// Content hash — canonical and immutable
// ──────────────────────────────────────────────

// ComputeContentHash is the canonical SHA-256 (hex) of the IMMUTABLE content of
// a memory: scope, kind, title, fiscal effect, effective date, the four content
// fields, and the source system + actor kind. Identity, status, recordedAt and
// revision deliberately do NOT participate — the hash identifies the content,
// not the envelope. Same input → same hash; any immutable-field change → a
// different hash (idempotency-safe for exact duplicates).
func ComputeContentHash(m AccountingMemory) string {
	parts := []string{
		ScopeKey(m.Scope),
		string(m.Kind),
		m.Title,
		string(m.FiscalEffect),
		m.EffectiveAt,
		m.Content.What,
		m.Content.Why,
		m.Content.Where,
		m.Content.Learned,
		m.Source.System,
		string(m.Source.ActorKind),
	}
	if contribution := closeSnapshotCanonicalContribution(m.CloseSnapshot); contribution != "" {
		// A snapshot contributes a self-describing element; a memory WITHOUT one
		// contributes NOTHING, so every pre-v6 canonical string is byte-identical
		// (frozen v0.3 hash contract — legacy envelopes never re-hash).
		parts = append(parts, contribution)
	}
	canonical := strings.Join(parts, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// ComputeIdentityHash hashes the DOMAIN identity of a memory — the tuple that
// answers "is this the same domain thing in the same scope": tenant
// (organization/company/RUC), period, topicKey, effectiveAt and the source
// reference. Two memories with the same identity hash are the same domain
// entity; a different content hash on the same identity is a conflict or a new
// revision (the import decides).
func ComputeIdentityHash(m AccountingMemory) string {
	canonical := strings.Join([]string{
		ScopeKey(m.Scope),
		m.Identity.TopicKey,
		m.EffectiveAt,
		m.Source.Reference,
	}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// ComputeEnvelopeHash hashes EVERYTHING signable/verifiable about a memory:
// the identity, the canonical content hash, the fiscal effect, the status, the
// complete source, the evidence and rule references, the timestamps and the
// supersession link. The import treats a matching envelope hash as an exact
// duplicate (idempotent no-op) and a differing envelope hash on the same
// identity as an immutable conflict.
func ComputeEnvelopeHash(m AccountingMemory) string {
	parts := []string{
		ComputeIdentityHash(m),
		m.ContentHash,
		string(m.FiscalEffect),
		string(m.Status),
		m.Source.System,
		m.Source.ActorID,
		string(m.Source.ActorKind),
		m.Source.Model,
		m.Source.Session,
		m.RecordedAt,
		m.ObservedAt,
		m.SupersedesID,
		m.ReceiptID,
		canonicalRefs(m.EvidenceRefs),
		canonicalRefs(m.RuleRefs),
	}
	if contribution := closeSnapshotCanonicalContribution(m.CloseSnapshot); contribution != "" {
		// Same contract as the content hash: only memories WITH a snapshot
		// contribute; pre-v6 envelopes stay byte-identical.
		parts = append(parts, contribution)
	}
	canonical := strings.Join(parts, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// canonicalRefs orders and deduplicates a reference list canonically.
// evidenceRefs / ruleRefs are SETS: their order is not semantically
// meaningful, so two memories with the same links in different orders MUST
// produce the same envelope hash. When ordinal or role matters, the caller
// should use an explicit EvidenceLink entity instead of a bare ref list
// (frozen decision, v0.3.0).
func canonicalRefs(refs []string) string {
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	sort.Strings(out)
	return strings.Join(out, "\x00")
}

// ──────────────────────────────────────────────
// Validation (scope.md: RUC 11 digits, period YYYYMM; v2 model rules)
// ──────────────────────────────────────────────

const rucDigits = 11

// IsValidRUC reports whether ruc is exactly 11 digits (checksum validation:
// future slice).
func IsValidRUC(ruc string) bool {
	if len(ruc) != rucDigits {
		return false
	}
	for i := 0; i < len(ruc); i++ {
		if ruc[i] < '0' || ruc[i] > '9' {
			return false
		}
	}
	return true
}

// IsValidPeriod reports whether period is `YYYYMM` (six digits, month 01-12)
// in this slice.
func IsValidPeriod(period string) bool {
	if len(period) != 6 {
		return false
	}
	for i := 0; i < len(period); i++ {
		if period[i] < '0' || period[i] > '9' {
			return false
		}
	}
	month := (period[4]-'0')*10 + (period[5] - '0')
	return month >= 1 && month <= 12
}

// AssertValidScope fails closed on malformed company scopes. An institutional
// scope is always valid.
func AssertValidScope(s Scope) error {
	if s.Kind == ScopeKindInstitutional {
		return nil
	}
	if s.Kind != ScopeKindCompany {
		return fmt.Errorf("INVALID_SCOPE: unknown scope kind %q", s.Kind)
	}
	if s.OrganizationID == "" {
		return fmt.Errorf("INVALID_SCOPE: organizationId must be a non-empty string")
	}
	if s.CompanyID == "" {
		return fmt.Errorf("INVALID_SCOPE: companyId must be a non-empty string")
	}
	if !IsValidRUC(s.RUC) {
		return fmt.Errorf("INVALID_RUC: expected exactly 11 digits, got %q", s.RUC)
	}
	if s.Period != "" && !IsValidPeriod(s.Period) {
		return fmt.Errorf("INVALID_PERIOD: expected YYYYMM (six digits, month 01-12), got %q", s.Period)
	}
	return nil
}

// AssertValidContent validates the structured content — all four fields must be
// non-empty strings.
func AssertValidContent(c Content) error {
	for field, value := range map[string]string{
		"what": c.What, "why": c.Why, "where": c.Where, "learned": c.Learned,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("INVALID_CONTENT: field %q must be a non-empty string", field)
		}
	}
	return nil
}

// AssertValidSource validates the v2 source: system non-empty, actorKind known,
// and actorId REQUIRED for human actors (provenance must trace to a person).
func AssertValidSource(s Source) error {
	if strings.TrimSpace(s.System) == "" {
		return fmt.Errorf("INVALID_SOURCE: system must be a non-empty string")
	}
	if !IsValidActorKind(s.ActorKind) {
		return fmt.Errorf("INVALID_SOURCE: unknown actorKind %q — expected human|agent|system", s.ActorKind)
	}
	if s.ActorKind == ActorKindHuman && strings.TrimSpace(s.ActorID) == "" {
		return fmt.Errorf("INVALID_SOURCE: actorId is required for human actors")
	}
	return nil
}

// AssertValidValidity fails closed on malformed vigencia windows at write time;
// a nil window is valid.
func AssertValidValidity(v *Validity) error {
	if v == nil {
		return nil
	}
	if v.EffectiveAt != "" {
		if _, ok := ParseDateTime(v.EffectiveAt); !ok {
			return fmt.Errorf("INVALID_VALIDITY: effectiveAt must be a parseable date string")
		}
	}
	if v.ExpiresAt != "" {
		if _, ok := ParseDateTime(v.ExpiresAt); !ok {
			return fmt.Errorf("INVALID_VALIDITY: expiresAt must be a parseable date string")
		}
	}
	return nil
}

// AssertValidMemory validates a full v2 memory before write: scope, content,
// source, kind, status, fiscal effect, timestamps, confidence and materiality.
// EffectiveAt is REQUIRED when the fiscal effect is non-none (a fiscal action
// must know its accounting period). Status must be a known v2 status (derived
// by the engine for saves; validated on reads and transitions).
func AssertValidMemory(m AccountingMemory) error {
	if err := AssertValidScope(m.Scope); err != nil {
		return err
	}
	if err := AssertValidContent(m.Content); err != nil {
		return err
	}
	if err := AssertValidSource(m.Source); err != nil {
		return err
	}
	if !IsValidMemoryKind(m.Kind) {
		return fmt.Errorf("INVALID_KIND: unknown memory kind %q — expected fact|evidence|decision|rule|exception|control|obligation|summary", m.Kind)
	}
	if !IsValidMemoryStatus(m.Status) {
		return fmt.Errorf("INVALID_STATUS: unknown memory status %q — expected active|pending_review|approved|rejected|superseded|voided", m.Status)
	}
	if !IsValidFiscalEffect(m.FiscalEffect) {
		return fmt.Errorf("INVALID_FISCAL_EFFECT: unknown fiscal effect %q — expected none|journal_entry|declaration|closing|adjustment|reclassification|approval|sunat_filing", m.FiscalEffect)
	}
	if m.EffectiveAt == "" {
		return fmt.Errorf("INVALID_EFFECTIVE_AT: effectiveAt must be a parseable date string")
	}
	if _, ok := ParseDateTime(m.EffectiveAt); !ok {
		return fmt.Errorf("INVALID_EFFECTIVE_AT: effectiveAt must be a parseable date string, got %q", m.EffectiveAt)
	}
	if m.ObservedAt != "" {
		if _, ok := ParseDateTime(m.ObservedAt); !ok {
			return fmt.Errorf("INVALID_OBSERVED_AT: observedAt must be a parseable date string, got %q", m.ObservedAt)
		}
	}
	if m.Confidence != nil && (*m.Confidence < 0 || *m.Confidence > 1) {
		return fmt.Errorf("INVALID_CONFIDENCE: confidence must be in [0,1], got %v", *m.Confidence)
	}
	if m.Materiality != nil && *m.Materiality < 0 {
		return fmt.Errorf("INVALID_MATERIALITY: materiality must be >= 0 (int64 cents), got %d", *m.Materiality)
	}
	if m.MaterialityLevel != nil && !IsValidMaterialityLevel(*m.MaterialityLevel) {
		return fmt.Errorf("INVALID_MATERIALITY_LEVEL: unknown materiality level %q — expected normal|material|critical", *m.MaterialityLevel)
	}
	return nil
}

// ──────────────────────────────────────────────
// v1→v2 migration mapping (type→kind, status→status)
// ──────────────────────────────────────────────

// LegacyTypeToKind maps the v1 generic `type` to the v2 accounting kind.
// Unknown types degrade to KindFact (a recorded fact) so historical content is
// never blocked by classification.
func LegacyTypeToKind(t string) MemoryKind {
	switch t {
	case "decision", "judgment":
		return KindDecision
	case "policy", "pattern", "config", "preference":
		return KindRule
	case "discovery", "bugfix":
		return KindFact
	case "architecture":
		return KindSummary
	default:
		return KindFact
	}
}

// LegacyStatusToStatus maps the v1 authority_status to the v2 memory status.
// Promoted → approved; superseded → superseded; everything else (draft,
// reviewed) → active: migrated v1 content is informative (fiscalEffect none)
// and must never be blocked by the approval gate.
func LegacyStatusToStatus(s string) MemoryStatus {
	switch s {
	case "promoted":
		return StatusApproved
	case "superseded":
		return StatusSuperseded
	default:
		return StatusActive
	}
}

// timeLayouts are the accepted timestamp shapes. The reference (TS Date.parse)
// accepts ISO-8601; RFC3339 (with optional fractional seconds) covers the
// canonical form, with two lenient fallbacks.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// ParseDateTime parses a date string the way the reference Date.parse does; ok
// is false for unparseable input (fail closed, never fabricate a time).
func ParseDateTime(s string) (time.Time, bool) {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
