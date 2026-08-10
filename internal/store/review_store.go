// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the v0.9.0 REVIEW WORKSPACE
// store layer (docs/architecture/review-workspace-v0.9.md): the pending_review
// queue, the detail assembly and the AUTHENTICATED reject/return decisions,
// plus the schema v13 migration that adds the decision ledger, the review
// idempotency ledger, the immutable velocity-alert table and the receipts
// action CHECK extension (memory_returned).
//
// Reject/return follow the ApproveMemory discipline EXACTLY (one BEGIN IMMEDIATE
// transaction on a dedicated connection — no FindByID + ApplyStatusTransition
// composition, which is a TOCTOU hole): idempotency reservation → locked re-read
// of row + links → scope checks → status gate → fresh H1 recompute vs expected →
// SoD (reviewer ≠ proposer, fail-closed) → reason policy → guarded status flip +
// envelope cache update → immutable decision event + legacy transition row →
// atomic signed receipt → velocity alert check → completed reservation → commit.
//
// The decision events live in the NEW immutable memory_decision_events ledger
// (additive — approval_events keeps its frozen v0.4.0 layout, action='approved'
// CHECK untouched; idempotency_keys stays approval-only). Reject/return reserve
// (tenant, requestId) on the new review_idempotency_keys ledger (the same
// actor-bound pattern as the purge pipeline).
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ──────────────────────────────────────────────
// v13 schema — review workspace tables (design §5/§6)
// ──────────────────────────────────────────────

// memoryDecisionEventsDDL is the IMMUTABLE decision ledger of the v0.9.0 review
// workspace (design §5): one row per authenticated reject/return with the
// reviewed/resulting envelope hashes, the human reason, the complete verified
// principal snapshot and the frozen policy version. The CHECKs mirror the
// decision transition table: action 'rejected'|'returned' always leaves
// pending_review and lands in the matching status; authorization_reason_code
// matches the action. UNIQUE(memory_id) — ONE decision per observation row (a
// returned revision never reopens; the agent's correction is a NEW revision).
const memoryDecisionEventsDDL = `
CREATE TABLE memory_decision_events (
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL,
  memory_id TEXT NOT NULL REFERENCES observations(id),
  tenant_id TEXT NOT NULL, company_id TEXT NOT NULL, fiscal_period_id TEXT,
  action TEXT NOT NULL CHECK(action IN ('rejected','returned')),
  from_status TEXT NOT NULL CHECK(from_status='pending_review'),
  to_status TEXT NOT NULL CHECK(to_status IN ('rejected','returned')),
  reviewed_envelope_hash TEXT NOT NULL, resulting_envelope_hash TEXT NOT NULL,
  reason TEXT NOT NULL, principal_subject_id TEXT NOT NULL,
  membership_id TEXT NOT NULL REFERENCES memberships(id), principal_roles_json TEXT NOT NULL,
  authentication_method TEXT NOT NULL, assurance_level TEXT NOT NULL,
  principal_authenticated_at TEXT NOT NULL, policy_version TEXT NOT NULL,
  authorization_reason_code TEXT NOT NULL CHECK(authorization_reason_code IN ('REJECTED','RETURNED')),
  created_at TEXT NOT NULL,
  UNIQUE(tenant_id,request_id), UNIQUE(memory_id),
  CHECK((action='rejected') = (to_status='rejected')),
  CHECK((action='returned') = (to_status='returned')),
  CHECK((authorization_reason_code='REJECTED') = (action='rejected'))
);
`

const memoryDecisionEventsMemoryIndexDDL = `CREATE INDEX idx_memory_decision_events_memory ON memory_decision_events(memory_id,created_at);`

const memoryDecisionEventsNoUpdateDDL = `
CREATE TRIGGER memory_decision_events_no_update BEFORE UPDATE ON memory_decision_events BEGIN
  SELECT RAISE(ABORT,'IMMUTABLE_DECISION_EVENT'); END;
`

const memoryDecisionEventsNoDeleteDDL = `
CREATE TRIGGER memory_decision_events_no_delete BEFORE DELETE ON memory_decision_events BEGIN
  SELECT RAISE(ABORT,'IMMUTABLE_DECISION_EVENT'); END;
`

// reviewIdempotencyKeysDDL mirrors the purge pipeline's tenant-scoped
// idempotency ledger for the review decisions: keyed by (tenant_id, request_id),
// bound to the exact actor that issued it, and decision_event_id + result_json
// set together on completion (design §5 — idempotency by (tenant, requestId)
// for all three decisions; approvals keep the frozen idempotency_keys ledger).
const reviewIdempotencyKeysDDL = `
CREATE TABLE review_idempotency_keys (
  tenant_id TEXT NOT NULL, request_id TEXT NOT NULL,
  command_hash TEXT NOT NULL, actor_binding TEXT NOT NULL,
  decision_event_id TEXT REFERENCES memory_decision_events(id),
  result_json TEXT, created_at TEXT NOT NULL, completed_at TEXT,
  PRIMARY KEY(tenant_id,request_id),
  CHECK((decision_event_id IS NULL) = (result_json IS NULL))
);
`

// reviewVelocityEventsDDL is the IMMUTABLE audit-visible monitoring ledger of
// the anti-rubber-stamp controls (design §6): one row per threshold crossing of
// the per-principal rolling counters (approval_velocity — >30 approvals per
// 15-minute window; consecutive_decisions — ≥3 consecutive rejections/returns
// without an intervening approval). NOT a receipt and NOT a blocking control —
// a velocity signal only; no decision is ever blocked by it in this slice.
const reviewVelocityEventsDDL = `
CREATE TABLE review_velocity_events (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL, principal_subject_id TEXT NOT NULL,
  alert_type TEXT NOT NULL CHECK(alert_type IN ('approval_velocity','consecutive_decisions')),
  window_started_at TEXT NOT NULL, window_ended_at TEXT NOT NULL,
  observed_count INTEGER NOT NULL, consecutive_count INTEGER NOT NULL,
  recorded_at TEXT NOT NULL
);
`

const reviewVelocityEventsPrincipalIndexDDL = `CREATE INDEX idx_review_velocity_events_principal ON review_velocity_events(tenant_id, principal_subject_id, recorded_at);`

const reviewVelocityEventsNoUpdateDDL = `
CREATE TRIGGER review_velocity_events_no_update BEFORE UPDATE ON review_velocity_events BEGIN
  SELECT RAISE(ABORT,'IMMUTABLE_REVIEW_VELOCITY_EVENT'); END;
`

const reviewVelocityEventsNoDeleteDDL = `
CREATE TRIGGER review_velocity_events_no_delete BEFORE DELETE ON review_velocity_events BEGIN
  SELECT RAISE(ABORT,'IMMUTABLE_REVIEW_VELOCITY_EVENT'); END;
`

// receiptsV13DDL is the v13 receipts table: the v12 layout verbatim with ONLY
// the action CHECK extended by the v0.9.0 review-workspace return act
// (memory_returned — design §2/§5: every decision, INCLUDING the return, is
// receipt-covered). SQLite cannot alter a CHECK, so the table is copied and
// swapped inside the migration transaction, byte-preserving every v12 row.
const receiptsV13DDL = `
CREATE TABLE receipts_v13 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  subject_type TEXT NOT NULL CHECK(subject_type IN ('memory','judgment','reconciliation','evidence_object')),
  subject_id TEXT NOT NULL,
  action TEXT NOT NULL CHECK(action IN
    ('memory_recorded','memory_approved','memory_rejected','memory_returned','memory_voided',
     'relation_confirmed','relation_rejected','evidence_linked','memory_superseded',
     'memory_closed','memory_reopened','reconciliation_confirmed','reconciliation_rejected',
     'object_stored',
     'retention_bound','purge_requested','purge_approved','purge_rejected',
     'purge_cancelled','purge_withdrawn','purge_intent','purge_executed',
     'hold_placed','hold_lifted')),
  tenant_id TEXT NOT NULL,
  company_id TEXT NOT NULL,
  fiscal_period_id TEXT NOT NULL,
  payload_hash TEXT NOT NULL,
  previous_receipt_hash TEXT NOT NULL,
  principal_id TEXT NOT NULL,
  membership_id TEXT NOT NULL,
  policy_version TEXT NOT NULL,
  algorithm TEXT NOT NULL CHECK(algorithm='Ed25519'),
  key_id TEXT NOT NULL REFERENCES signing_keys(key_id),
  signature BLOB NOT NULL,
  issued_at TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  receipt_hash TEXT NOT NULL UNIQUE,
  memory_id TEXT REFERENCES observations(id),
  judgment_id TEXT REFERENCES judgments(id),
  reconciliation_id TEXT REFERENCES reconciliations(id),
  evidence_object_id TEXT REFERENCES evidence_objects(id),
  UNIQUE(subject_type, subject_id, action, payload_hash),
  CHECK(((memory_id IS NULL) + (judgment_id IS NULL) + (reconciliation_id IS NULL) + (evidence_object_id IS NULL)) = 3),
  CHECK(COALESCE(memory_id, judgment_id, reconciliation_id, evidence_object_id) = subject_id)
);
`

// receiptsV13CopyDDL copies every v12 receipt row byte-preserved into the staging
// table (explicit column order — the typed FK columns copy verbatim).
const receiptsV13CopyDDL = `
INSERT INTO receipts_v13 (id, subject_type, subject_id, action, tenant_id, company_id,
  fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
  policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
  memory_id, judgment_id, reconciliation_id, evidence_object_id)
SELECT id, subject_type, subject_id, action, tenant_id, company_id,
  fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
  policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
  memory_id, judgment_id, reconciliation_id, evidence_object_id FROM receipts;
`

// dropApprovalEventsDDL is unused — approval_events keeps its frozen layout.
// dropReceiptsDDL (store.go) swaps the receipts table out during the migration.

// migrateV12ToV13 upgrades a schema_version=12 store to v13 IN ONE fail-closed
// transaction (design §5/§6 — review workspace):
//
//	(a) validate that none of the new tables exist (a pre-existing table is a
//	    corruption signal; abort);
//	(b) create the immutable memory_decision_events decision ledger, the
//	    tenant-scoped review_idempotency_keys ledger and the immutable
//	    review_velocity_events monitoring ledger, with their indexes and guards;
//	(c) rebuild receipts under the staging name receipts_v13: the v12 layout
//	    verbatim with ONLY the action CHECK extended by memory_returned; copy
//	    every row byte-preserved, swap the table into place, recreate the
//	    singleton index (memory_returned stays IN the append-only singleton —
//	    ONE return per subject — so the index is unchanged), the subject/key
//	    indexes and the no-update/no-delete triggers;
//	(d) UPDATE schema_meta SET value = '13' ONLY after every step above
//	    succeeded; any failure rolls the whole migration back and leaves
//	    schema_version=12.
//
// No existing row is backfilled or re-hashed. Fresh schema DDL (applySchema +
// the migration chain in Open) produces the same tables/triggers/indexes.
func migrateV12ToV13(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate v12→v13: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (a) Fail closed on any pre-existing new table: the chain is additive and
	// never replays — a pre-existing table is a corruption signal.
	var existing string
	err = tx.QueryRowContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND (name = 'memory_decision_events'
		  OR name = 'review_idempotency_keys' OR name = 'review_velocity_events'
		  OR name = 'receipts_v13')
		LIMIT 1`).Scan(&existing)
	switch {
	case err == nil:
		return fmt.Errorf("migrate v12→v13: pre-existing table %q — corruption signal, abort (additive migrations never replay)", existing)
	case errors.Is(err, sql.ErrNoRows):
		// clean — proceed
	default:
		return fmt.Errorf("migrate v12→v13: inspect existing tables: %w", err)
	}

	// (b) The review-workspace tables + their guards.
	for _, ddl := range []string{
		memoryDecisionEventsDDL, memoryDecisionEventsMemoryIndexDDL,
		memoryDecisionEventsNoUpdateDDL, memoryDecisionEventsNoDeleteDDL,
		reviewIdempotencyKeysDDL,
		reviewVelocityEventsDDL, reviewVelocityEventsPrincipalIndexDDL,
		reviewVelocityEventsNoUpdateDDL, reviewVelocityEventsNoDeleteDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v12→v13: create review workspace table: %w", err)
		}
	}

	// (c) The receipts table rebuild (extended action CHECK, layout verbatim).
	if _, err := tx.ExecContext(ctx, receiptsV13DDL); err != nil {
		return fmt.Errorf("migrate v12→v13: create receipts_v13: %w", err)
	}
	if _, err := tx.ExecContext(ctx, receiptsV13CopyDDL); err != nil {
		return fmt.Errorf("migrate v12→v13: copy receipts rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dropReceiptsDDL); err != nil {
		return fmt.Errorf("migrate v12→v13: swap out v12 receipts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE receipts_v13 RENAME TO receipts`); err != nil {
		return fmt.Errorf("migrate v12→v13: rename receipts_v13: %w", err)
	}
	for _, ddl := range []string{
		receiptsSingletonIndexV12DDL, receiptsSubjectTimeIndexDDL, receiptsKeyTimeIndexDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v12→v13: create receipts index: %w", err)
		}
	}
	for _, ddl := range []string{receiptsNoUpdateDDL, receiptsNoDeleteDDL} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v12→v13: create receipts trigger: %w", err)
		}
	}

	// (d) schema_version = 13 ONLY after the whole migration succeeded — same
	// transaction, so a failure above rolls everything back.
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = '13' WHERE key = 'schema_version'`); err != nil {
		return fmt.Errorf("migrate v12→v13: set schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate v12→v13: commit: %w", err)
	}
	committed = true
	return nil
}

// ──────────────────────────────────────────────
// Queue (design §3)
// ──────────────────────────────────────────────

// ListReviewQueue returns the pending_review queue of an EXACT company scope,
// deterministically ordered (materialityLevel rank DESC → recordedAt ASC →
// rowid ASC) with bounded pagination (default 50, max 200). Scope is a
// structural filter (never a post-filter); an institutional scope fails closed —
// institutional memories have no company to review. Status is closed to
// pending_review (no status injection).
func (s *SQLiteStore) ListReviewQueue(ctx context.Context, query core.ReviewQueueQuery) (core.ReviewQueuePage, error) {
	if err := core.AssertValidScope(query.Scope); err != nil {
		return core.ReviewQueuePage{}, err
	}
	if query.Scope.Kind != core.ScopeKindCompany {
		return core.ReviewQueuePage{}, auth.New(auth.CodeInvalidTransition, "the review queue requires an exact company scope (institutional memories have no company to review)")
	}
	limit := query.Limit
	if limit == 0 {
		limit = core.DefaultReviewQueueLimit
	}
	if limit < 1 || limit > core.MaxReviewQueueLimit {
		return core.ReviewQueuePage{}, fmt.Errorf("INVALID_PAGINATION: limit must be within [1,%d], got %d", core.MaxReviewQueueLimit, limit)
	}
	if query.Offset < 0 {
		return core.ReviewQueuePage{}, fmt.Errorf("INVALID_PAGINATION: offset must be >= 0, got %d", query.Offset)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id, o.kind, o.fiscal_effect, o.materiality_level, COALESCE(o.materiality, 0),
		       o.status, o.envelope_hash, o.actor, o.recorded_at,
		       o.evidence_refs_json, o.rule_refs_json,
		       (SELECT COUNT(*) FROM evidence_links el WHERE el.memory_id = o.id),
		       (SELECT COUNT(*) FROM rule_links rl WHERE rl.memory_id = o.id),
		       (SELECT COUNT(*) FROM judgments j WHERE j.status = 'proposed' AND (j.from_id = o.id OR j.to_id = o.id))
		FROM observations o
		WHERE o.status = 'pending_review'
		  AND o.scope_kind = ? AND o.organization_id = ? AND o.company_id = ? AND o.ruc = ? AND o.period = ?
		ORDER BY CASE o.materiality_level WHEN 'critical' THEN 3 WHEN 'material' THEN 2 ELSE 1 END DESC,
		         o.recorded_at ASC, o.rowid ASC
		LIMIT ? OFFSET ?`,
		string(query.Scope.Kind), query.Scope.OrganizationID, query.Scope.CompanyID, query.Scope.RUC, query.Scope.Period,
		limit, query.Offset,
	)
	if err != nil {
		return core.ReviewQueuePage{}, fmt.Errorf("persistence error: list review queue: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]core.ReviewQueueItem, 0, limit)
	for rows.Next() {
		var (
			item            core.ReviewQueueItem
			level           sql.NullString
			storedEvidence  sql.NullString
			storedRules     sql.NullString
			evidenceLinkCnt int
			ruleLinkCnt     int
			openJudgmentCnt int
		)
		if err := rows.Scan(
			&item.MemoryID, &item.Kind, &item.FiscalEffect, &level, &item.MaterialityCents,
			&item.Status, &item.EnvelopeHash, &item.RecordedBy, &item.RecordedAt,
			&storedEvidence, &storedRules, &evidenceLinkCnt, &ruleLinkCnt, &openJudgmentCnt,
		); err != nil {
			return core.ReviewQueuePage{}, fmt.Errorf("persistence error: scan review queue item: %w", err)
		}
		if level.Valid && level.String != "" {
			l := core.MaterialityLevel(level.String)
			item.MaterialityLevel = &l
		}
		// Ref counts = stored refs (JSON arrays) + link rows (deduped sets).
		var storedEv, storedRu []string
		_ = json.Unmarshal([]byte(storedEvidence.String), &storedEv)
		_ = json.Unmarshal([]byte(storedRules.String), &storedRu)
		item.EvidenceRefCount = len(dedupe(storedEv)) + evidenceLinkCnt
		item.RuleRefCount = len(dedupe(storedRu)) + ruleLinkCnt
		item.OpenJudgmentCount = openJudgmentCnt
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return core.ReviewQueuePage{}, fmt.Errorf("persistence error: iterate review queue: %w", err)
	}
	return core.ReviewQueuePage{Items: items, Limit: limit, Offset: query.Offset}, nil
}

// dedupe returns the unique, order-preserving members of refs (empty strings
// dropped) — the stored JSON arrays are engine-canonical (already deduped), so
// this is a defensive normalization, never a semantic reorder.
func dedupe(refs []string) []string {
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if r == "" {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}

// ──────────────────────────────────────────────
// Detail (design §4)
// ──────────────────────────────────────────────

// ReviewDetail composes the review of ONE pending revision, scope-guarded: the
// full pending revision (latest chain head), the structured content diff vs the
// immediate chain predecessor, the evidence refs with WORM availability, the
// best-effort rule refs with vigencia, the open proposed judgments touching the
// memory and the decision-relevant review metadata with the boundary notice.
func (s *SQLiteStore) ReviewDetail(ctx context.Context, memoryID string, scope core.Scope) (core.ReviewDetail, error) {
	if strings.TrimSpace(memoryID) == "" {
		return core.ReviewDetail{}, auth.New(auth.CodeMemoryNotFound, "memoryId is required")
	}
	if err := core.AssertValidScope(scope); err != nil {
		return core.ReviewDetail{}, err
	}
	memory, ok := s.readMemoryWithLinks(ctx, s.db, memoryID)
	if !ok {
		return core.ReviewDetail{}, auth.New(auth.CodeMemoryNotFound, "memory not found: "+memoryID)
	}
	// Scope guard: the caller's exact scope must equal the stored scope
	// (cross-tenant invisibility — never a post-filter).
	if !core.ScopeEquals(memory.Scope, scope) {
		return core.ReviewDetail{}, auth.New(auth.CodeMemoryNotFound, "memory not found: "+memoryID)
	}
	// The review workspace reviews PENDING revisions only; a decided memory is
	// read through the plain memory surfaces.
	if memory.Status != core.StatusPendingReview {
		return core.ReviewDetail{}, auth.New(auth.CodeInvalidTransition, fmt.Sprintf("review detail requires a pending_review memory, got status %q", memory.Status))
	}

	detail := core.ReviewDetail{
		Memory:         core.CloneMemory(memory),
		Evidence:       []core.EvidenceRefState{},
		Rules:          []core.RuleRefState{},
		OpenJudgments:  []core.OpenJudgmentRef{},
		BoundaryNotice: core.ReviewBoundaryNotice,
	}

	// 1. Structured content diff vs the immediate chain predecessor (identity/
	// content fields only — status, timestamps and recorded-by are provenance).
	prev, err := s.previousChainRevision(ctx, memory)
	if err != nil {
		return core.ReviewDetail{}, err
	}
	if prev != nil {
		detail.Diff = contentDiff(*prev, memory)
	}

	// 2. Evidence state — WORM availability via the existing object store
	// (present / absent / not-a-ref; corruption fails closed as evidence).
	evObjects, err := s.ObjectAvailability(ctx, memory.EvidenceRefs)
	if err != nil {
		return core.ReviewDetail{}, fmt.Errorf("persistence error: review evidence availability: %w", err)
	}
	for _, ref := range memory.EvidenceRefs {
		state := core.EvidenceRefState{Ref: ref}
		if obj, ok := evObjects[ref]; ok {
			state.Availability = core.EvidencePresent
			state.ObjectID = obj.ObjectID
			state.SizeBytes = obj.Size
			state.ContentType = obj.ContentType
		} else if core.IsObjectID(ref) {
			state.Availability = core.EvidenceAbsent
		} else {
			state.Availability = core.EvidenceNotARef
		}
		detail.Evidence = append(detail.Evidence, state)
	}

	// 3. Rule state — best-effort vigencia (Phase 6 is NOT required): a rule ref
	// that resolves to a rule memory in the SAME exact scope surfaces its vigencia
	// window and status; anything else stays unresolved.
	for _, ref := range memory.RuleRefs {
		state := core.RuleRefState{Ref: ref}
		if rule, ok := s.FindByTopicKey(ref, memory.Scope); ok && rule.Kind == core.KindRule {
			state.Resolved = true
			state.MemoryID = rule.Identity.ID
			state.Status = rule.Status
			if rule.Validity != nil {
				state.EffectiveAt = rule.Validity.EffectiveAt
				state.ExpiresAt = rule.Validity.ExpiresAt
			}
		}
		detail.Rules = append(detail.Rules, state)
	}

	// 4. Open judgments — proposed judgments with fromId or toId = this memory
	// (design §4.5: WHERE status='proposed' AND (from_id=? OR to_id=?)),
	// structurally scoped to the memory's own tenant/company.
	detail.OpenJudgments, err = s.openJudgmentsTouching(ctx, memory)
	if err != nil {
		return core.ReviewDetail{}, err
	}

	// 5. Review metadata — H1 (fresh recompute of the CURRENT pending revision),
	// proposer, timestamps, risk class and the prior approved revision (when the
	// chain has one) for before/after context.
	detail.ReviewMetadata = core.ReviewMetadata{
		EnvelopeHashToSign: core.ComputeEnvelopeHash(memory),
		RecordedBy:         memory.Source.ActorID,
		RecordedAt:         memory.RecordedAt,
		ObservedAt:         memory.ObservedAt,
		FiscalEffect:       memory.FiscalEffect,
		MaterialityLevel:   memory.MaterialityLevel,
	}
	if memory.Materiality != nil {
		detail.ReviewMetadata.MaterialityCents = *memory.Materiality
	}
	if prior, err := s.priorApprovedChainRevision(ctx, memory); err != nil {
		return core.ReviewDetail{}, err
	} else if prior != nil {
		detail.ReviewMetadata.PriorApprovedRevision = prior.Identity.ID
	}
	return detail, nil
}

// previousChainRevision returns the immediate predecessor of memory in its
// (topicKey, exact scope) chain (any status), or nil for the chain's first
// revision.
func (s *SQLiteStore) previousChainRevision(ctx context.Context, memory core.AccountingMemory) (*core.AccountingMemory, error) {
	return s.chainRevisionWhere(ctx, memory, "", "")
}

// priorApprovedChainRevision returns the newest APPROVED revision older than
// memory in its chain, or nil when the chain has none (before/after context).
func (s *SQLiteStore) priorApprovedChainRevision(ctx context.Context, memory core.AccountingMemory) (*core.AccountingMemory, error) {
	return s.chainRevisionWhere(ctx, memory, "AND status = 'approved'", "")
}

func (s *SQLiteStore) chainRevisionWhere(ctx context.Context, memory core.AccountingMemory, extraWhere, order string) (*core.AccountingMemory, error) {
	if order == "" {
		order = "revision DESC"
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+memoryColumns+` FROM observations
		WHERE topic_key = ? AND scope_kind = ? AND organization_id = ? AND company_id = ? AND ruc = ? AND period = ?
		  AND revision < ? `+extraWhere+`
		ORDER BY `+order+` LIMIT 1`,
		memory.Identity.TopicKey, string(memory.Scope.Kind), memory.Scope.OrganizationID, memory.Scope.CompanyID,
		memory.Scope.RUC, memory.Scope.Period, memory.Revision,
	)
	m, err := scanMemory(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("persistence error: read chain revision: %w", err)
	}
	return &m, nil
}

// contentDiff computes the structured diff between the previous chain revision
// and the pending head over IDENTITY/CONTENT fields only — status, timestamps
// and recorded-by are provenance, never content (design §4.2). Structured
// values render canonically (JSON with sorted keys for maps/slices).
func contentDiff(prev, head core.AccountingMemory) core.ContentDiff {
	diff := core.ContentDiff{}
	add := func(field, before, after string) {
		if before != after {
			diff.Changes = append(diff.Changes, core.FieldChange{Field: field, Before: before, After: after})
		}
	}
	add("title", prev.Title, head.Title)
	add("kind", string(prev.Kind), string(head.Kind))
	add("scope", core.ScopeKey(prev.Scope), core.ScopeKey(head.Scope))
	add("fiscalEffect", string(prev.FiscalEffect), string(head.FiscalEffect))
	add("effectiveAt", prev.EffectiveAt, head.EffectiveAt)
	add("content.what", prev.Content.What, head.Content.What)
	add("content.why", prev.Content.Why, head.Content.Why)
	add("content.where", prev.Content.Where, head.Content.Where)
	add("content.learned", prev.Content.Learned, head.Content.Learned)
	add("evidenceRefs", canonicalRefsString(prev.EvidenceRefs), canonicalRefsString(head.EvidenceRefs))
	add("ruleRefs", canonicalRefsString(prev.RuleRefs), canonicalRefsString(head.RuleRefs))
	if prev.MaterialityLevel != nil || head.MaterialityLevel != nil {
		add("materialityLevel", levelString(prev.MaterialityLevel), levelString(head.MaterialityLevel))
	}
	if prev.Materiality != nil || head.Materiality != nil {
		add("materialityCents", centsString(prev.Materiality), centsString(head.Materiality))
	}
	return diff
}

// canonicalRefsString renders a ref SET canonically: sorted, deduplicated,
// empty-string-dropped, comma-joined — the same set semantics the envelope hash
// uses (order is never semantically meaningful).
func canonicalRefsString(refs []string) string {
	out := dedupe(refs)
	sort.Strings(out)
	return strings.Join(out, ",")
}

func levelString(l *core.MaterialityLevel) string {
	if l == nil {
		return ""
	}
	return string(*l)
}

func centsString(c *int64) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("%d", *c)
}

// openJudgmentsTouching returns the PROPOSED judgments with fromId or toId =
// this memory (design §4.5), structurally scoped to the memory's tenant/company.
func (s *SQLiteStore) openJudgmentsTouching(ctx context.Context, memory core.AccountingMemory) ([]core.OpenJudgmentRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, relation, from_id, to_id, proposer_actor_id, proposed_at
		FROM judgments
		WHERE status = 'proposed' AND tenant_id = ? AND company_id = ?
		  AND (from_id = ? OR to_id = ?)
		ORDER BY proposed_at ASC, rowid ASC`,
		memory.Scope.OrganizationID, memory.Scope.CompanyID, memory.Identity.ID, memory.Identity.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("persistence error: read open judgments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []core.OpenJudgmentRef{}
	for rows.Next() {
		var j core.OpenJudgmentRef
		if err := rows.Scan(&j.JudgmentID, &j.Relation, &j.FromID, &j.ToID, &j.ProposerID, &j.ProposedAt); err != nil {
			return nil, fmt.Errorf("persistence error: scan open judgment: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ──────────────────────────────────────────────
// Reject / return — authenticated decisions (design §5)
// ──────────────────────────────────────────────

// decisionKind is the closed description of one authenticated review decision:
// the target status, the event action/authorization code and the receipt action.
type decisionKind struct {
	action        string // "rejected" | "returned"
	toStatus      core.MemoryStatus
	reasonCode    string // "REJECTED" | "RETURNED"
	receiptAction core.ReceiptAction
}

var (
	rejectDecision = decisionKind{action: "rejected", toStatus: core.StatusRejected, reasonCode: "REJECTED", receiptAction: core.ReceiptActionMemoryRejected}
	returnDecision = decisionKind{action: "returned", toStatus: core.StatusReturned, reasonCode: "RETURNED", receiptAction: core.ReceiptActionMemoryReturned}
)

// RejectMemory atomically rejects a pending_review memory against the caller's
// expected envelope hash — the AUTHENTICATED reject (v0.9.0 review workspace,
// design §5), replacing the legacy actor-only path for review purposes. The
// reason is REQUIRED when the memory's risk class demands it (materiality ≥
// material OR fiscalEffect ∈ {closing, declaration, sunat_filing}) and is ALWAYS
// persisted in the decision event and the memory_rejected receipt (new payload
// version receipt-payload/v0.10.0 with reason + reviewed H1). Terminal.
func (s *SQLiteStore) RejectMemory(ctx context.Context, cmd core.RejectMemoryCommand, principal auth.VerifiedApprovalPrincipal) (core.RejectMemoryResult, error) {
	return s.decidePendingMemory(ctx, cmd.MemoryID, cmd.ExpectedEnvelopeHash, cmd.Reason, cmd.RequestID, principal, rejectDecision)
}

// ReturnMemory atomically RETURNS a pending_review memory to its proposer for
// correction — the AUTHENTICATED return (v0.9.0 review workspace, design §5):
// pending_review → returned (NON-terminal). The reason is REQUIRED (a return is
// a correction request — the reason tells the agent what to fix) and is
// persisted in the decision event and the memory_returned receipt. An agent
// Save on the returned memory creates a NEW revision that re-enters
// pending_review; the returned revision itself never reopens.
func (s *SQLiteStore) ReturnMemory(ctx context.Context, cmd core.ReturnMemoryCommand, principal auth.VerifiedApprovalPrincipal) (core.ReturnMemoryResult, error) {
	res, err := s.decidePendingMemory(ctx, cmd.MemoryID, cmd.ExpectedEnvelopeHash, cmd.Reason, cmd.RequestID, principal, returnDecision)
	if err != nil {
		return core.ReturnMemoryResult{}, err
	}
	// The two result shapes are identical by contract; the shared decision core
	// returns the reject shape and the return surface maps it field-by-field.
	return core.ReturnMemoryResult{
		MemoryID:              res.MemoryID,
		DecisionEventID:       res.DecisionEventID,
		PreviousStatus:        res.PreviousStatus,
		CurrentStatus:         res.CurrentStatus,
		ReviewedEnvelopeHash:  res.ReviewedEnvelopeHash,
		ResultingEnvelopeHash: res.ResultingEnvelopeHash,
		Reason:                res.Reason,
		PrincipalSubjectID:    res.PrincipalSubjectID,
		MembershipID:          res.MembershipID,
		PolicyVersion:         res.PolicyVersion,
		DecidedAt:             res.DecidedAt,
		IdempotentReplay:      res.IdempotentReplay,
	}, nil
}

// decidePendingMemory is the shared authenticated reject/return core — the
// ApproveMemory discipline, ONE BEGIN IMMEDIATE transaction on a dedicated
// connection:
//
//	idempotency reservation (review_idempotency_keys) → locked re-read of row +
//	links → scope checks → status gate → fresh H1 recompute vs expected → SoD
//	(reviewer ≠ proposer, fail-closed) → reason policy → guarded status flip +
//	envelope cache update → immutable memory_decision_events row + legacy
//	transition row → atomic signed receipt (payload v0.10.0) → velocity alert
//	check → completed reservation → commit.
func (s *SQLiteStore) decidePendingMemory(ctx context.Context, memoryID, expectedEnvelopeHash, reason, requestID string, principal auth.VerifiedApprovalPrincipal, kind decisionKind) (core.RejectMemoryResult, error) {
	// Syntax guards (defense in depth — the service validates first): an
	// incomplete command fails closed before any lock.
	if strings.TrimSpace(memoryID) == "" || strings.TrimSpace(expectedEnvelopeHash) == "" || strings.TrimSpace(requestID) == "" {
		return core.RejectMemoryResult{}, auth.New(auth.CodeMemoryNotFound, "decision command is incomplete (memoryId, expectedEnvelopeHash and requestId are required)")
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// BEGIN IMMEDIATE is the write intent (design §5): SQLite's reserved writer
	// lock is taken here, before any race-sensitive read.
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	now := nowISO()
	commandHash := decisionCommandHash(memoryID, expectedEnvelopeHash, reason)
	actorBinding := principal.SubjectID() + "\x00" + principal.MembershipID()

	// 1. Idempotency: one reservation per (tenant, requestId) on the review
	// ledger. A replayed command AND principal binding returns the stored result.
	var (
		storedHash, storedActor     string
		storedEventID, storedResult sql.NullString
	)
	err = conn.QueryRowContext(ctx, `
		SELECT command_hash, actor_binding, decision_event_id, result_json
		FROM review_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		principal.TenantID(), requestID,
	).Scan(&storedHash, &storedActor, &storedEventID, &storedResult)
	switch {
	case err == nil:
		if storedHash != commandHash || storedActor != actorBinding {
			return core.RejectMemoryResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different command or principal")
		}
		if storedEventID.String == "" || storedResult.String == "" {
			// Incomplete reservation (an interrupted attempt): surface the typed
			// conflict — a crash left the intent uncommitted; retry with a FRESH
			// request id (never pretend an interrupted attempt decided).
			return core.RejectMemoryResult{}, auth.New(auth.CodeIdempotencyConflict, "request id is reserved by an incomplete decision attempt; use a fresh request id")
		}
		var replay core.RejectMemoryResult
		if err := json.Unmarshal([]byte(storedResult.String), &replay); err != nil {
			return core.RejectMemoryResult{}, fmt.Errorf("persistence error: decode replayed decision result: %w", err)
		}
		replay.IdempotentReplay = true
		return replay, nil
	case errors.Is(err, sql.ErrNoRows):
		// 2. Reserve: command hash plus the compared principal binding; the
		// event/result stay NULL until the decision commits.
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO review_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, decision_event_id, result_json, created_at, completed_at)
			VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			principal.TenantID(), requestID, commandHash, actorBinding, now,
		); err != nil {
			return core.RejectMemoryResult{}, fmt.Errorf("persistence error: reserve review idempotency key: %w", err)
		}
	default:
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: read review idempotency key: %w", err)
	}

	// 3. Read the observation row + all evidence/rule refs THROUGH the same
	// connection (the withLinks merge is scoped to this transaction).
	memory, ok := s.readMemoryWithLinks(ctx, conn, memoryID)
	if !ok {
		return core.RejectMemoryResult{}, auth.New(auth.CodeMemoryNotFound, "memory not found: "+memoryID)
	}

	// 4. Structural scope checks (derived from the row, never caller claims).
	if memory.Scope.Kind != core.ScopeKindCompany {
		return core.RejectMemoryResult{}, auth.New(auth.CodeCompanyScopeDenied, "institutional memories cannot be decided by a company-scoped principal")
	}
	if principal.TenantID() != memory.Scope.OrganizationID {
		return core.RejectMemoryResult{}, auth.New(auth.CodeTenantScopeMismatch, "principal tenant does not match the memory tenant")
	}
	if !principalHasCompanyScope(principal, memory.Scope.CompanyID) {
		return core.RejectMemoryResult{}, auth.New(auth.CodeCompanyScopeDenied, "company is outside the principal's scope")
	}

	// 5. Status gate: only pending_review can be decided; a decided memory is
	// ALREADY_DECIDED; anything else is an invalid transition.
	switch memory.Status {
	case core.StatusPendingReview:
		// proceed
	case core.StatusApproved, core.StatusRejected, core.StatusReturned:
		return core.RejectMemoryResult{}, auth.New(auth.CodeAlreadyDecided, "memory is already decided")
	default:
		return core.RejectMemoryResult{}, auth.New(auth.CodeInvalidTransition, fmt.Sprintf("%s is not legal from status %q", kind.action, memory.Status))
	}

	// 6. H1 recomputed FRESH from the locked row + current canonical refs — a
	// mismatch returns ENVELOPE_MISMATCH carrying ONLY the two hashes.
	h1 := core.ComputeEnvelopeHash(memory)
	if !strings.EqualFold(strings.TrimSpace(expectedEnvelopeHash), h1) {
		return core.RejectMemoryResult{}, auth.NewEnvelopeMismatch(expectedEnvelopeHash, h1, "memory envelope changed after review; expected hash does not match the current envelope")
	}

	// 7. SoD fail-closed (design §5/§6.5.5): the reviewer cannot decide their own
	// proposal — the pending revision's recordedBy must differ from the
	// authenticated principal's subjectId.
	if authz.SODViolation(memory.Source.ActorID, principal.SubjectID()) {
		return core.RejectMemoryResult{}, auth.New(auth.CodeSODViolation, "the reviewer cannot decide their own proposal (separation of duties)")
	}

	// 8. Reason policy (design §5): REASON_REQUIRED for the memory's risk class
	// (material/critical or closing/declaration/sunat_filing for reject; ALWAYS
	// for return — a return is a correction request). The reason is persisted in
	// the event and the receipt either way.
	requireReason := kind.action == "returned" || core.RejectReasonRequired(memory)
	if requireReason && strings.TrimSpace(reason) == "" {
		return core.RejectMemoryResult{}, auth.New(auth.CodeReasonRequired, "a reason is required for this decision")
	}

	// 9. H2 from the same snapshot with the target status; status participates in
	// the envelope hash, so H2 must differ from H1.
	decidedSnapshot := memory
	decidedSnapshot.Status = kind.toStatus
	h2 := core.ComputeEnvelopeHash(decidedSnapshot)
	if h2 == h1 {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: resulting envelope equals reviewed envelope — status change did not affect the hash")
	}
	res, err := conn.ExecContext(ctx,
		`UPDATE observations SET status = ?, authority_status = ?, envelope_hash = ? WHERE id = ? AND status = ?`,
		string(kind.toStatus), legacyStatusFor(kind.toStatus), h2, memoryID, string(core.StatusPendingReview),
	)
	if err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: decision update: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: decision rows affected: %w", err)
	}
	if affected != 1 {
		return core.RejectMemoryResult{}, auth.New(auth.CodeInvalidTransition, "guarded status update did not match exactly one pending_review row")
	}

	// 10. The immutable decision event + the legacy transition mirror, sharing
	// ONE captured UTC timestamp.
	eventID, err := newUUID()
	if err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: generate decision event id: %w", err)
	}
	snapshot := principal.PrincipalSnapshot()
	rolesJSON, err := json.Marshal(snapshot.Roles)
	if err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: encode principal roles: %w", err)
	}
	var fiscalPeriodID any
	if memory.Scope.Period != "" {
		fiscalPeriodID = memory.Scope.Period
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO memory_decision_events (
			id, request_id, memory_id, tenant_id, company_id, fiscal_period_id,
			action, from_status, to_status, reviewed_envelope_hash, resulting_envelope_hash,
			reason, principal_subject_id, membership_id, principal_roles_json,
			authentication_method, assurance_level, principal_authenticated_at,
			policy_version, authorization_reason_code, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID, requestID, memoryID, memory.Scope.OrganizationID, memory.Scope.CompanyID, fiscalPeriodID,
		kind.action, string(core.StatusPendingReview), string(kind.toStatus), h1, h2,
		reason, snapshot.SubjectID, snapshot.MembershipID, string(rolesJSON),
		string(snapshot.AuthenticationMethod), string(snapshot.AssuranceLevel), snapshot.AuthenticatedAt,
		kernelPolicyVersion, kind.reasonCode, now,
	); err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: insert decision event: %w", err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO transition_log (observation_id, from_status, to_status, actor, actor_kind, timestamp) VALUES (?, ?, ?, ?, ?, ?)`,
		memoryID, string(core.StatusPendingReview), string(kind.toStatus), principal.SubjectID(), string(core.ActorKindHuman), now,
	); err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: record decision transition: %w", err)
	}

	// 11. Atomic receipt emission (design §5): inside the SAME transaction with
	// the captured now, on the memory's receipt chain. The authenticated reject
	// carries the EXTENDED payload (v0.10.0: reason + reviewed H1 + resulting H2
	// + the complete verified principal snapshot) and the return is the new
	// memory_returned act with the same coverage. A signing failure rolls the
	// whole decision back (no change, no receipt).
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeMemory, memoryID, kind.receiptAction, core.ReceiptPayload{
		Version:                  core.ReceiptPayloadVersionV10,
		TenantID:                 memory.Scope.OrganizationID,
		CompanyID:                memory.Scope.CompanyID,
		FiscalPeriodID:           memory.Scope.Period,
		ReviewedEnvelopeHash:     h1,
		ResultingEnvelopeHash:    h2,
		Reason:                   reason,
		PrincipalID:              snapshot.SubjectID,
		MembershipID:             snapshot.MembershipID,
		PrincipalRoles:           receiptPrincipalRoles(snapshot),
		AuthenticationMethod:     string(snapshot.AuthenticationMethod),
		AssuranceLevel:           string(snapshot.AssuranceLevel),
		PrincipalAuthenticatedAt: snapshot.AuthenticatedAt,
		PolicyVersion:            kernelPolicyVersion,
	}, now); err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: emit decision receipt: %w", err)
	}

	// 12. Anti-rubber-stamp observable events (design §6): derive the
	// per-principal rolling counters from the immutable ledgers and insert an
	// immutable review_velocity_alert row when a threshold trips. NOT a receipt,
	// NOT a blocking control — a velocity signal only; a failure here rolls the
	// whole decision back (the decision never commits without its alert rows).
	if err := s.maybeRecordReviewVelocityAlerts(ctx, conn, memory.Scope.OrganizationID, snapshot.SubjectID, kind.action, now); err != nil {
		return core.RejectMemoryResult{}, err
	}

	result := core.RejectMemoryResult{
		MemoryID:              memoryID,
		DecisionEventID:       eventID,
		PreviousStatus:        string(core.StatusPendingReview),
		CurrentStatus:         string(kind.toStatus),
		ReviewedEnvelopeHash:  h1,
		ResultingEnvelopeHash: h2,
		Reason:                reason,
		PrincipalSubjectID:    snapshot.SubjectID,
		MembershipID:          snapshot.MembershipID,
		PolicyVersion:         kernelPolicyVersion,
		DecidedAt:             now,
		IdempotentReplay:      false,
	}

	// 13. Complete the reservation and commit — the whole decision is one atomic
	// unit. The CHECK on the table requires decision_event_id and result_json to
	// be set together.
	serializedResult, err := json.Marshal(result)
	if err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: encode decision result: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE review_idempotency_keys SET result_json = ?, completed_at = ?, decision_event_id = ? WHERE tenant_id = ? AND request_id = ?`,
		string(serializedResult), now, eventID, principal.TenantID(), requestID,
	); err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: complete review idempotency key: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.RejectMemoryResult{}, fmt.Errorf("persistence error: commit decision: %w", err)
	}
	committed = true
	return result, nil
}

// decisionCommandHash is the canonical idempotency command hash of a review
// decision: SHA-256 hex of memoryId NUL lowercase(expectedEnvelopeHash) NUL
// exact reason (same canonicalization as approveCommandHash — decisions share
// the frozen command-hash contract).
func decisionCommandHash(memoryID, expectedEnvelopeHash, reason string) string {
	canonical := memoryID + "\x00" + strings.ToLower(expectedEnvelopeHash) + "\x00" + reason
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// ──────────────────────────────────────────────
// Anti-rubber-stamp observable events (design §6)
// ──────────────────────────────────────────────

// maybeRecordReviewVelocityAlerts derives the per-principal rolling counters
// from the IMMUTABLE decision ledgers (approval_events for approvals,
// memory_decision_events for reject/return — no counter table, the ledgers ARE
// the source) and inserts an immutable review_velocity_alert row when a
// threshold is freshly CROSSED (design §6 defaults: >30 approvals per 15-minute
// window; ≥3 consecutive rejections/returns without an intervening approval).
// Each alert is emitted exactly once per crossing — a sustained burst does not
// flood the ledger. NOT a receipt and NOT a blocking control.
func (s *SQLiteStore) maybeRecordReviewVelocityAlerts(ctx context.Context, q Queryer, tenantID, subjectID, action, now string) error {
	windowStart := time.Now().UTC().Add(-time.Duration(core.ApprovalVelocityWindowMinutes) * time.Minute).Format(time.RFC3339)

	// 1. Approval velocity: count approvals in the window BEFORE this decision
	// (the new event is either an approval already inserted — count it — or a
	// reject/return which does not move the approval count).
	var approvals int
	if err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM approval_events
		WHERE principal_subject_id = ? AND action = 'approved' AND created_at >= ?`,
		subjectID, windowStart,
	).Scan(&approvals); err != nil {
		return fmt.Errorf("persistence error: count approval velocity: %w", err)
	}
	approvalsBefore := approvals
	if action == "approved" {
		approvalsBefore = approvals - 1
	}
	if approvalsBefore < 0 {
		approvalsBefore = 0
	}
	if approvals > core.ApprovalVelocityThreshold && approvalsBefore <= core.ApprovalVelocityThreshold {
		alertID, err := newUUID()
		if err != nil {
			return fmt.Errorf("persistence error: generate velocity alert id: %w", err)
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO review_velocity_events (id, tenant_id, principal_subject_id, alert_type,
				window_started_at, window_ended_at, observed_count, consecutive_count, recorded_at)
			VALUES (?, ?, ?, 'approval_velocity', ?, ?, ?, 0, ?)`,
			alertID, tenantID, subjectID, windowStart, now, approvals, now,
		); err != nil {
			return fmt.Errorf("persistence error: insert approval velocity alert: %w", err)
		}
	}

	// 2. Consecutive rejections/returns: the trailing streak BEFORE this decision.
	streak, err := s.trailingRejectReturnStreak(ctx, q, subjectID)
	if err != nil {
		return err
	}
	if action == "approved" {
		return nil // an intervening approval resets the streak — no alert
	}
	streak++
	if streak == core.ConsecutiveDecisionThreshold {
		alertID, err := newUUID()
		if err != nil {
			return fmt.Errorf("persistence error: generate velocity alert id: %w", err)
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO review_velocity_events (id, tenant_id, principal_subject_id, alert_type,
				window_started_at, window_ended_at, observed_count, consecutive_count, recorded_at)
			VALUES (?, ?, ?, 'consecutive_decisions', ?, ?, 0, ?, ?)`,
			alertID, tenantID, subjectID, windowStart, now, streak, now,
		); err != nil {
			return fmt.Errorf("persistence error: insert consecutive decisions alert: %w", err)
		}
	}
	return nil
}

// trailingRejectReturnStreak returns the count of the newest consecutive
// reject/return decisions of the principal (0 when the newest decision is an
// approval or there are none) — merged chronologically across the two immutable
// decision ledgers.
func (s *SQLiteStore) trailingRejectReturnStreak(ctx context.Context, q Queryer, subjectID string) (int, error) {
	type decision struct {
		action    string
		createdAt string
		rowid     int64
	}
	collect := func(query string, args ...any) ([]decision, error) {
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("persistence error: read decision ledger: %w", err)
		}
		defer func() { _ = rows.Close() }()
		var out []decision
		for rows.Next() {
			var d decision
			if err := rows.Scan(&d.action, &d.createdAt, &d.rowid); err != nil {
				return nil, fmt.Errorf("persistence error: scan decision ledger: %w", err)
			}
			out = append(out, d)
		}
		return out, rows.Err()
	}
	approvals, err := collect(`
		SELECT 'approved', created_at, rowid FROM approval_events
		WHERE principal_subject_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?`,
		subjectID, core.ConsecutiveDecisionThreshold)
	if err != nil {
		return 0, err
	}
	decisions, err := collect(`
		SELECT action, created_at, rowid FROM memory_decision_events
		WHERE principal_subject_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?`,
		subjectID, core.ConsecutiveDecisionThreshold)
	if err != nil {
		return 0, err
	}
	// Merge newest-first and take the leading run of reject/return.
	merged := append(approvals, decisions...)
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].createdAt != merged[j].createdAt {
			return merged[i].createdAt > merged[j].createdAt
		}
		return merged[i].rowid > merged[j].rowid
	})
	streak := 0
	for _, d := range merged {
		if d.action == "approved" {
			break
		}
		streak++
		if streak >= core.ConsecutiveDecisionThreshold {
			break
		}
	}
	return streak, nil
}
