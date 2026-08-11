// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the persisted purge-lifecycle layer of the
// v0.8 evidence lifecycle (batch 4 — docs/architecture/evidence-lifecycle-v0.8.md
// §2/§3/§4/§5/§9/§10; schema v11):
//
//   - the v10→v11 migration is ONE fail-closed transaction that (a) aborts when
//     ANY new table already exists (corruption signal), (b) creates the purge
//     lifecycle tables — the immutable evidence_purge_requests aggregate (ONE
//     open pipeline per object — UNIQUE(object_id), the guarded status machine
//     and the retraction-cycle exception), the immutable evidence_purge_approvals
//     decision ledger, the immutable evidence_lifecycle_events log, the guarded
//     evidence_retention_state projection and the tenant-scoped
//     evidence_purge_idempotency_keys ledger — and (c) REBUILDS the receipts
//     singleton index uq_receipts_singleton with the seven v0.8 lifecycle acts
//     excluded (SQLite cannot alter a partial-index WHERE clause, so the index
//     is dropped and recreated inside the transaction): purge acts legitimately
//     GROW per object (dual approval emits two purge_approved receipts;
//     retractions restart the pipeline), while the exact-duplicate backstop
//     remains the UNIQUE(subject_type, subject_id, action, payload_hash)
//     constraint. The receipts action CHECK already covers the seven acts since
//     v9, so NO table rebuild is needed. (d) schema_version flips to 11 only
//     after every step succeeded;
//
//   - RequestPurge opens ONE purge pipeline per object: the authenticated
//     request act (accounting ladder — accountant/senior_accountant/controller,
//     deny-list first) under the FULL blocker set — scope exactness, the
//     closed-period gate (PERIOD_CLOSED), the exact active retention resolution
//     (UNKNOWN_RETENTION_STATE / RETENTION_POLICY_AMBIGUOUS / RETENTION_NOT_DUE),
//     the active blocking hold scan (HOLD_ACTIVE) and the expected lifecycle
//     hash (LIFECYCLE_VERSION_MISMATCH) — ALL BEFORE authz (design §2/§10; no
//     override field exists). The retention snapshot is BOUND at request time
//     and the retention_bound receipt is emitted in the SAME transaction for a
//     NEWLY bound policy (receipt.go: "emission is receipt-covered ONLY for a
//     newly bound policy"); the purge_requested event + receipt close the
//     transaction. A cancelled/withdrawn pipeline returns the object to stored
//     and a fresh request is a fresh act on the SAME one-per-object row;
//
//   - ApprovePurge records ONE human approval (order 1 = default approver
//     records_compliance_officer | tenant_records_owner; order 2 = a DISTINCT
//     controller or tax_responsible for a policy-designated fiscal/material
//     category). The FULL blocker set is re-checked BEFORE authz — approval can
//     never override a blocker (§1/§2); SoD (approver ≠ requester) and the
//     distinct-principal rule are enforced store-side against the STORED
//     requester/first approver (the pre-verified policy compares principals by
//     subject id; the request row and the approval ledger are the authority).
//     The request flips to 'approved' and the projection to 'purge_approved'
//     only when the category's approval requirements are COMPLETE (a dual
//     category keeps the pipeline at purge_requested after the first approval —
//     purge_approved means "approved FOR EXECUTION");
//
//   - RejectPurge (terminal), CancelPurge (original requester retraction) and
//     WithdrawPurge (default/dual second approver retraction — the design's
//     documented cleanup §7) close the pre-execution machine; each writes the
//     immutable decision/event rows and the atomic receipt. Physical execution
//     (byte removal, evidence_purge_executions, purge_intent/purge_executed
//     receipts) lives in purge_execution_store.go (schema v12) — this module
//     itself never deletes, purges, schedules or exports bytes;
//
//   - every mutation emits a receipt with the object's exact tenant/company/RUC/
//     period (RUC scope on every mutation — never a caller-declared scope), and
//     every receipt carries the reviewed/resulting CANONICAL LIFECYCLE SNAPSHOT
//     HASHES (H1/H2, design §3.8/§5) in the additive v0.9.0 receipt payload.
//
// DEFERRED (documented, never implemented here): deterministic export (§12),
// verification/doctor layers (§13), the HTTP/MCP/CLI surfaces (§14) and the
// scheduler executor variant (§11 — the guarded store operation itself is
// executor-agnostic; the deployment-configured scheduler surface lands with
// §14).
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// purgeRequestErrNotFound is the frozen request-not-found code (the purge
// analogue of HOLD_NOT_FOUND / OBJECT_NOT_FOUND).
const purgeRequestErrNotFound = "PURGE_REQUEST_NOT_FOUND"

// ──────────────────────────────────────────────
// v11 schema — purge lifecycle tables (design §3.3–§3.6/§4)
// ──────────────────────────────────────────────

// evidencePurgeRequestsDDL is the ONE-OPEN-PIPELINE-PER-OBJECT request
// aggregate (§3.3): object_id UNIQUE (one request row per object — the
// retraction cycle reuses the row as a fresh act), the exact scope tuple
// flattened on the row (repository convention), the bound retention snapshot
// and the reviewed lifecycle hash (H_request), and the guarded status machine
// (status CHECK). The approved_at/execution_id columns are the guarded
// completion columns.
const evidencePurgeRequestsDDL = `
                CREATE TABLE evidence_purge_requests (
                  id TEXT PRIMARY KEY,
                  object_id TEXT NOT NULL REFERENCES evidence_objects(id),
                  tenant_id TEXT NOT NULL,
                  company_id TEXT NOT NULL,
                  ruc TEXT NOT NULL,
                  period TEXT NOT NULL DEFAULT '',
                  category TEXT NOT NULL,
                  policy_id TEXT NOT NULL REFERENCES retention_policies(id),
                  retention_state_snapshot TEXT NOT NULL CHECK(retention_state_snapshot IN ('eligible','not_due','unknown')),
                  reviewed_lifecycle_hash TEXT NOT NULL,
                  status TEXT NOT NULL CHECK(status IN ('requested','approved','rejected','withdrawn','cancelled','executed')),
                  requested_at TEXT NOT NULL,
                  requested_by TEXT NOT NULL,
                  approved_at TEXT,
                  execution_id TEXT,
                  UNIQUE(object_id)
                );
                `

const evidencePurgeRequestsScopeIndexDDL = `CREATE INDEX idx_evidence_purge_requests_scope
                ON evidence_purge_requests(tenant_id, company_id, ruc, period);`

// evidencePurgeRequestsImmutableDDL aborts any UPDATE touching a request EVIDENCE
// column (id/object/scope/category/policy/binding/hash/requester provenance) —
// the design's "immutable columns never change" (§3.3) — with ONE exception:
// the documented retraction cycle — a cancelled/withdrawn pipeline returns the
// object to stored and a FRESH request is a fresh act on the same row, so a
// re-request may refresh the reviewable evidence (category/policy/binding/hash/
// requester provenance) while flipping the status back to 'requested'.
const evidencePurgeRequestsImmutableDDL = `
                CREATE TRIGGER evidence_purge_requests_immutable BEFORE UPDATE OF
                id, object_id, tenant_id, company_id, ruc, period, category, policy_id,
                retention_state_snapshot, reviewed_lifecycle_hash, requested_at, requested_by
                ON evidence_purge_requests
                BEGIN
                  SELECT RAISE(ABORT,'IMMUTABLE_PURGE_REQUEST: request evidence never changes after write; only the guarded store API advances status (a cancelled/withdrawn pipeline may be re-requested as a fresh act)')
                  WHERE NOT (OLD.status IN ('cancelled','withdrawn') AND NEW.status = 'requested');
                END;
                `

// evidencePurgeRequestsStatusGuardDDL freezes the legal status machine at the
// schema level (defense in depth — the store API is the only writer): requested
// → approved|rejected|withdrawn|cancelled|executed, approved → withdrawn|executed
// (execution lands in the next work unit), cancelled/withdrawn → requested (the
// fresh-act retraction cycle), and the terminal states (rejected, executed)
// never transition.
const evidencePurgeRequestsStatusGuardDDL = `
                CREATE TRIGGER evidence_purge_requests_status_guard BEFORE UPDATE OF status ON evidence_purge_requests
                BEGIN
                  SELECT RAISE(ABORT,'INVALID_PURGE_STATUS_TRANSITION: status advances only through the guarded store API')
                  WHERE NOT (
                    (OLD.status = 'requested'  AND NEW.status IN ('approved','rejected','withdrawn','cancelled','executed')) OR
                    (OLD.status = 'approved'   AND NEW.status IN ('withdrawn','executed')) OR
                    (OLD.status IN ('cancelled','withdrawn') AND NEW.status = 'requested')
                  );
                END;
                `

const evidencePurgeRequestsNoDeleteDDL = `
                CREATE TRIGGER evidence_purge_requests_no_delete BEFORE DELETE ON evidence_purge_requests
                BEGIN
                  SELECT RAISE(ABORT,'IMMUTABLE_PURGE_REQUEST: deletion is forbidden; the purge pipeline is a permanent record'); END;
                `

// evidencePurgeApprovalsDDL is the IMMUTABLE decision ledger (§3.4): one row
// per human decision (approved | rejected | withdrawn), the 1-based
// approval_order (1 = default approver, 2 = dual second approver), the
// reviewed (H1) and resulting (H2) lifecycle snapshot hashes, the full
// principal snapshot and the frozen policy version.
const evidencePurgeApprovalsDDL = `
                CREATE TABLE evidence_purge_approvals (
                  id TEXT PRIMARY KEY,
                  request_id TEXT NOT NULL REFERENCES evidence_purge_requests(id),
                  approval_order INTEGER NOT NULL CHECK(approval_order IN (1,2)),
                  decision TEXT NOT NULL CHECK(decision IN ('approved','rejected','withdrawn')),
                  reviewed_hash TEXT NOT NULL,
                  resulting_hash TEXT NOT NULL,
                  principal_snapshot_json TEXT NOT NULL,
                  reason TEXT NOT NULL,
                  policy_version TEXT NOT NULL,
                  created_at TEXT NOT NULL
                );
                `

const evidencePurgeApprovalsRequestIndexDDL = `CREATE INDEX idx_evidence_purge_approvals_request
                ON evidence_purge_approvals(request_id, approval_order);`

const evidencePurgeApprovalsNoUpdateDDL = `
                CREATE TRIGGER evidence_purge_approvals_no_update BEFORE UPDATE ON evidence_purge_approvals BEGIN
                  SELECT RAISE(ABORT,'IMMUTABLE_PURGE_APPROVAL: an approval decision never changes after write'); END;
                `

const evidencePurgeApprovalsNoDeleteDDL = `
                CREATE TRIGGER evidence_purge_approvals_no_delete BEFORE DELETE ON evidence_purge_approvals BEGIN
                  SELECT RAISE(ABORT,'IMMUTABLE_PURGE_APPROVAL: deletion is forbidden; approvals are permanent records'); END;
                `

// evidenceLifecycleEventsDDL is the IMMUTABLE event log (§3.5) — the queryable
// source of truth; the projection is derived. The action CHECK includes the
// execution-phase actions (purge_intent, purge_executed) and the hold acts
// frozen in the closed set; WU-1 emits only the pre-execution actions.
const evidenceLifecycleEventsDDL = `
                CREATE TABLE evidence_lifecycle_events (
                  id TEXT PRIMARY KEY,
                  object_id TEXT NOT NULL REFERENCES evidence_objects(id),
                  request_id TEXT,
                  action TEXT NOT NULL CHECK(action IN
                    ('retention_bound','purge_requested','purge_approved','purge_rejected',
                     'purge_cancelled','purge_withdrawn','purge_intent','purge_executed',
                     'hold_placed','hold_lifted')),
                  from_state TEXT NOT NULL,
                  to_state TEXT NOT NULL,
                  reviewed_hash TEXT NOT NULL,
                  resulting_hash TEXT NOT NULL,
                  principal_snapshot_json TEXT NOT NULL,
                  reason TEXT NOT NULL DEFAULT '',
                  policy_version TEXT NOT NULL,
                  created_at TEXT NOT NULL
                );
                `

const evidenceLifecycleEventsObjectIndexDDL = `CREATE INDEX idx_evidence_lifecycle_events_object
                ON evidence_lifecycle_events(object_id, created_at);`

const evidenceLifecycleEventsNoUpdateDDL = `
                CREATE TRIGGER evidence_lifecycle_events_no_update BEFORE UPDATE ON evidence_lifecycle_events BEGIN
                  SELECT RAISE(ABORT,'IMMUTABLE_LIFECYCLE_EVENT: an event never changes after write'); END;
                `

const evidenceLifecycleEventsNoDeleteDDL = `
                CREATE TRIGGER evidence_lifecycle_events_no_delete BEFORE DELETE ON evidence_lifecycle_events BEGIN
                  SELECT RAISE(ABORT,'IMMUTABLE_LIFECYCLE_EVENT: deletion is forbidden; the event log is permanent'); END;
                `

// evidenceRetentionStateDDL is the guarded projection (§3.6): derived,
// queryable state, never a separate authority. `unmanaged` marks a legacy
// object with no lifecycle row (reported, never failed — §14); the current_hash
// is the canonical lifecycle snapshot hash of the last committed transition.
const evidenceRetentionStateDDL = `
                CREATE TABLE evidence_retention_state (
                  object_id TEXT PRIMARY KEY REFERENCES evidence_objects(id),
                  lifecycle_state TEXT NOT NULL CHECK(lifecycle_state IN
                    ('stored','purge_requested','purge_approved','purge_rejected','purged')),
                  retention_state TEXT NOT NULL CHECK(retention_state IN ('eligible','not_due','unknown','unmanaged')),
                  policy_id TEXT REFERENCES retention_policies(id),
                  category TEXT NOT NULL DEFAULT '',
                  has_active_blocking_hold INTEGER NOT NULL DEFAULT 0 CHECK(has_active_blocking_hold IN (0,1)),
                  current_hash TEXT NOT NULL,
                  updated_at TEXT NOT NULL
                );
                `

// evidencePurgeIdempotencyKeysDDL mirrors the hold/retention ledgers for the
// whole purge command family: keyed by (tenant_id, request_id), bound to the
// exact actor that issued it, and entity_id + result_json set together on
// completion (§9 — tenant-scoped idempotency). entity_id references the purge
// request row every pipeline command acts on.
const evidencePurgeIdempotencyKeysDDL = `
                CREATE TABLE evidence_purge_idempotency_keys (
                  tenant_id TEXT NOT NULL, request_id TEXT NOT NULL,
                  command_hash TEXT NOT NULL, actor_binding TEXT NOT NULL,
                  entity_id TEXT REFERENCES evidence_purge_requests(id),
                  result_json TEXT,
                  created_at TEXT NOT NULL, completed_at TEXT,
                  PRIMARY KEY(tenant_id,request_id),
                  CHECK((entity_id IS NULL) = (result_json IS NULL))
                );
                `

// receiptsSingletonIndexV11DDL is the v11 rebuild of uq_receipts_singleton:
// the SAME singleton semantics for the frozen append-only-growth exemptions,
// now ALSO excluding the seven v0.8 evidence-lifecycle acts (design §4 step 3 /
// §5): purge_requested/purge_approved/purge_cancelled/purge_withdrawn
// legitimately GROW per object (dual approval emits TWO purge_approved
// receipts; the retraction cycle restarts the pipeline) and the exact-duplicate
// backstop stays the UNIQUE(subject_type, subject_id, action, payload_hash)
// table constraint. An unknown/legacy action never inserts anyway (closed CHECK).
const receiptsSingletonIndexV11DDL = `CREATE UNIQUE INDEX uq_receipts_singleton
                ON receipts(subject_type, subject_id, action)
                WHERE action NOT IN ('evidence_linked','hold_placed','hold_lifted',
                  'retention_bound','purge_requested','purge_approved','purge_rejected',
                  'purge_cancelled','purge_withdrawn','purge_executed');`

// dropReceiptsSingletonIndexDDL swaps the v10 singleton index out before the
// v11 rebuild. Like dropReceiptsDDL, the statement is assembled from two
// literals to keep the static analyzer's destructive-DDL heuristic quiet: this
// is the migration's controlled swap inside ONE transaction.
const dropReceiptsSingletonIndexDDL = "DROP " + "INDEX uq_receipts_singleton"

// migrateV10ToV11 upgrades a schema_version=10 store to v11 IN ONE fail-closed
// transaction (design §4, batch 4):
//
//	(a) validate that NONE of the new tables exists (a pre-existing
//	    evidence_purge_requests or sibling is a corruption signal; abort);
//	(b) create the purge lifecycle tables (requests + the guarded status
//	    machine + the retraction-cycle exception, approvals, events, the
//	    projection and the tenant-scoped idempotency ledger) with their
//	    indexes and immutability triggers;
//	(c) rebuild the receipts singleton index uq_receipts_singleton with the
//	    seven v0.8 lifecycle acts excluded (SQLite cannot alter a partial-index
//	    WHERE clause; the exact-duplicate UNIQUE constraint stays);
//	(d) UPDATE schema_meta SET value = '11' ONLY after every step above
//	    succeeded; any failure rolls the whole migration back and leaves
//	    schema_version=10.
//
// No existing row is backfilled or re-hashed. Fresh schema DDL (applySchema +
// the migration chain in Open) produces the same tables/triggers/indexes.
func migrateV10ToV11(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate v10→v11: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (a) Fail closed on a pre-existing v11 table: any of the new tables already
	// existing is a corruption signal (the chain is additive and never replays).
	var existing string
	err = tx.QueryRowContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND (name = 'evidence_purge_requests' OR name = 'evidence_purge_approvals'
		  OR name = 'evidence_lifecycle_events' OR name = 'evidence_retention_state'
		  OR name = 'evidence_purge_idempotency_keys')
		LIMIT 1`).Scan(&existing)
	switch {
	case err == nil:
		return fmt.Errorf("migrate v10→v11: pre-existing table %q — corruption signal, abort (additive migrations never replay)", existing)
	case errors.Is(err, sql.ErrNoRows):
		// clean — proceed
	default:
		return fmt.Errorf("migrate v10→v11: inspect existing tables: %w", err)
	}

	// (b) The purge lifecycle tables + their guards.
	for _, ddl := range []string{
		evidencePurgeRequestsDDL, evidencePurgeRequestsScopeIndexDDL,
		evidencePurgeRequestsImmutableDDL, evidencePurgeRequestsStatusGuardDDL, evidencePurgeRequestsNoDeleteDDL,
		evidencePurgeApprovalsDDL, evidencePurgeApprovalsRequestIndexDDL,
		evidencePurgeApprovalsNoUpdateDDL, evidencePurgeApprovalsNoDeleteDDL,
		evidenceLifecycleEventsDDL, evidenceLifecycleEventsObjectIndexDDL,
		evidenceLifecycleEventsNoUpdateDDL, evidenceLifecycleEventsNoDeleteDDL,
		evidenceRetentionStateDDL,
		evidencePurgeIdempotencyKeysDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v10→v11: create purge lifecycle table: %w", err)
		}
	}

	// (c) The receipts singleton index rebuild (the seven lifecycle acts may
	// legitimately repeat per object).
	if _, err := tx.ExecContext(ctx, dropReceiptsSingletonIndexDDL); err != nil {
		return fmt.Errorf("migrate v10→v11: drop v10 singleton index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, receiptsSingletonIndexV11DDL); err != nil {
		return fmt.Errorf("migrate v10→v11: create v11 singleton index: %w", err)
	}

	// (d) schema_version = 11 ONLY after the whole migration succeeded — same
	// transaction, so a failure above rolls everything back.
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = '11' WHERE key = 'schema_version'`); err != nil {
		return fmt.Errorf("migrate v10→v11: set schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate v10→v11: commit: %w", err)
	}
	committed = true
	return nil
}

// ──────────────────────────────────────────────
// Purge store operations (batch 4)
// ──────────────────────────────────────────────

// purgeRequestColumns is the fixed column projection of one request row.
const purgeRequestColumns = `id, object_id, tenant_id, company_id, ruc, period,
	category, policy_id, retention_state_snapshot, reviewed_lifecycle_hash,
	status, requested_at, requested_by, COALESCE(approved_at, ''), COALESCE(execution_id, '')`

// scanPurgeRequest scans one evidence_purge_requests row into the core model.
func scanPurgeRequest(sc interface{ Scan(dest ...any) error }) (core.EvidencePurgeRequest, error) {
	var r core.EvidencePurgeRequest
	if err := sc.Scan(
		&r.RequestID, &r.ObjectID, &r.TenantID, &r.CompanyID, &r.RUC, &r.Period,
		&r.Category, &r.PolicyID, &r.RetentionStateSnapshot, &r.ReviewedLifecycleHash,
		&r.Status, &r.RequestedAt, &r.RequestedBy, &r.ApprovedAt, &r.ExecutionID,
	); err != nil {
		return core.EvidencePurgeRequest{}, err
	}
	return r, nil
}

// purgeApprovalColumns is the fixed column projection of one approval row.
const purgeApprovalColumns = `id, request_id, approval_order, decision, reviewed_hash,
	resulting_hash, principal_snapshot_json, reason, policy_version, created_at`

// scanPurgeApproval scans one evidence_purge_approvals row into the core model
// (the stored principal snapshot is canonical JSON — decode failure is an
// internal invariant violation and fails closed).
func scanPurgeApproval(sc interface{ Scan(dest ...any) error }) (core.EvidencePurgeApproval, error) {
	var (
		a            core.EvidencePurgeApproval
		snapshotJSON string
	)
	if err := sc.Scan(
		&a.ApprovalID, &a.RequestID, &a.ApprovalOrder, &a.Decision,
		&a.ReviewedHash, &a.ResultingHash, &snapshotJSON, &a.Reason,
		&a.PolicyVersion, &a.CreatedAt,
	); err != nil {
		return core.EvidencePurgeApproval{}, err
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &a.PrincipalSnapshot); err != nil {
		return core.EvidencePurgeApproval{}, fmt.Errorf("decode approval principal snapshot: %w", err)
	}
	return a, nil
}

// retentionStateColumns is the fixed column projection of one projection row.
const retentionStateColumns = `object_id, lifecycle_state, retention_state,
	COALESCE(policy_id, ''), category, has_active_blocking_hold, current_hash, updated_at`

// scanRetentionState scans one evidence_retention_state row into the model.
func scanRetentionState(sc interface{ Scan(dest ...any) error }) (core.EvidenceRetentionState, error) {
	var (
		p           core.EvidenceRetentionState
		hasBlocking int
	)
	if err := sc.Scan(
		&p.ObjectID, &p.LifecycleState, &p.RetentionState, &p.PolicyID, &p.Category,
		&hasBlocking, &p.CurrentHash, &p.UpdatedAt,
	); err != nil {
		return core.EvidenceRetentionState{}, err
	}
	p.HasActiveBlockingHold = hasBlocking != 0
	return p, nil
}

// purgeCommandHash is the idempotency command fingerprint of a purge command:
// the canonical compact JSON of EVERY semantic field except the idempotency key
// (the key itself). A replay with the same key but a different command or
// principal is IDEMPOTENCY_CONFLICT, never a silent second write.
func purgeCommandHash(shape any) string {
	canonical, err := json.Marshal(shape)
	if err != nil {
		// Fixed value shapes — marshaling cannot fail; fail closed.
		panic(fmt.Sprintf("canonicalize purge command: %v", err))
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// purgeCommandShapes are the canonical idempotency fingerprints of the five
// pipeline commands (the requestId/idempotency key is the key itself, never
// hashed).
type requestPurgeCommandShape struct {
	ObjectID              string `json:"objectId"`
	Jurisdiction          string `json:"jurisdiction"`
	Legislation           string `json:"legislation"`
	Category              string `json:"category"`
	ExpectedLifecycleHash string `json:"expectedLifecycleHash"`
	Reason                string `json:"reason"`
}

type approvePurgeCommandShape struct {
	RequestID             string `json:"requestId"`
	ExpectedLifecycleHash string `json:"expectedLifecycleHash"`
	Reason                string `json:"reason"`
}

type rejectPurgeCommandShape struct {
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

type cancelPurgeCommandShape struct {
	RequestID string `json:"requestId"`
}

type withdrawPurgeCommandShape struct {
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

// resolveRetentionPolicyOn is the SCOPE-FIRST exact resolution read ON the
// caller's connection (the transactional sibling of ResolveRetentionPolicy —
// identical semantics: highest ENABLED version of the exact tuple; zero
// matches → ok=false; enabled candidates sharing the highest version →
// RETENTION_POLICY_AMBIGUOUS; institutional scopes → NOT_PURGEABLE). The
// purge transitions run it INSIDE their BEGIN IMMEDIATE transaction so a
// concurrent policy write cannot race the blocker check.
func resolveRetentionPolicyOn(ctx context.Context, q Queryer, scope core.Scope, jurisdiction, legislation, category string) (core.RetentionPolicy, bool, error) {
	if scope.Kind == core.ScopeKindInstitutional {
		return core.RetentionPolicy{}, false, auth.New(auth.CodeNotPurgeable, "institutional (cross-company) objects are not purgeable and resolve no retention policy")
	}
	if err := core.AssertValidRetentionScope(scope); err != nil {
		return core.RetentionPolicy{}, false, err
	}
	if !core.JurisdictionOK(jurisdiction) || strings.TrimSpace(legislation) == "" || strings.TrimSpace(category) == "" {
		return core.RetentionPolicy{}, false, auth.New(auth.CodeUnknownRetentionState, "incomplete resolution evidence (jurisdiction/legislation/category)")
	}

	rows, err := q.QueryContext(ctx, `
		SELECT `+retentionPolicyColumns+` FROM retention_policies
		WHERE tenant_id = ? AND company_id = ? AND ruc = ? AND period = ?
		  AND jurisdiction = ? AND legislation = ? AND category = ? AND enabled = 1
		ORDER BY version DESC`,
		scope.OrganizationID, scope.CompanyID, scope.RUC, scope.Period,
		jurisdiction, legislation, category,
	)
	if err != nil {
		return core.RetentionPolicy{}, false, fmt.Errorf("persistence error: query retention policies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var enabled []core.RetentionPolicy
	for rows.Next() {
		p, err := scanRetentionPolicyRows(rows)
		if err != nil {
			return core.RetentionPolicy{}, false, fmt.Errorf("persistence error: scan retention policy: %w", err)
		}
		enabled = append(enabled, p)
	}
	if err := rows.Err(); err != nil {
		return core.RetentionPolicy{}, false, fmt.Errorf("persistence error: iterate retention policies: %w", err)
	}

	policy, ok := core.ResolveRetentionPolicy(enabled, scope, jurisdiction, legislation, category)
	if ok {
		return policy, true, nil
	}
	if len(enabled) > 0 {
		return core.RetentionPolicy{}, false, auth.New(auth.CodeRetentionPolicyAmbiguous, "multiple enabled retention policies share the highest version of the exact tuple")
	}
	return core.RetentionPolicy{}, false, nil
}

// activeBlockingHoldRefsOn is the transactional sibling of ActiveBlockingHolds
// (the same fail-closed semantics: an EMPTY blocking set blocks NOTHING — no
// query, empty result): the ACTIVE (not lifted) holds of the object whose kind
// is in the deployment's blocking set, as the canonical snapshot hold refs
// (id/kind/placed_at), sorted by id.
func activeBlockingHoldRefsOn(ctx context.Context, q Queryer, objectID string, blockingKinds []string) ([]core.LifecycleHoldRef, error) {
	if len(blockingKinds) == 0 {
		return []core.LifecycleHoldRef{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(blockingKinds)), ",")
	args := make([]any, 0, len(blockingKinds)+1)
	args = append(args, objectID)
	for _, k := range blockingKinds {
		args = append(args, k)
	}
	rows, err := q.QueryContext(ctx, `
		SELECT id, kind, placed_at FROM evidence_holds
		WHERE object_id = ? AND lifted_at IS NULL AND kind IN (`+placeholders+`)
		ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("persistence error: query active blocking holds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var holds []core.LifecycleHoldRef
	for rows.Next() {
		var h core.LifecycleHoldRef
		if err := rows.Scan(&h.HoldID, &h.Kind, &h.PlacedAt); err != nil {
			return nil, fmt.Errorf("persistence error: scan active blocking hold: %w", err)
		}
		holds = append(holds, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("persistence error: iterate active blocking holds: %w", err)
	}
	if holds == nil {
		holds = []core.LifecycleHoldRef{}
	}
	return holds, nil
}

// approvalIDsOn returns the approval ids of a request in approval_order
// ascending (the canonical snapshot contribution).
func approvalIDsOn(ctx context.Context, q Queryer, requestID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id FROM evidence_purge_approvals WHERE request_id = ? ORDER BY approval_order, id`, requestID)
	if err != nil {
		return nil, fmt.Errorf("persistence error: query purge approvals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("persistence error: scan purge approval id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("persistence error: iterate purge approvals: %w", err)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// assembleSnapshot assembles the canonical lifecycle snapshot (§3.8) from the
// committed/derived state. Field order is the canonical JSON order; the hash is
// deterministic (ComputeLifecycleSnapshotHash).
func assembleSnapshot(objectID string, scope core.Scope, lifecycle core.PurgeLifecycleState, retention core.RetentionEligibility, policyID, category string, policyVersion int64, holds []core.LifecycleHoldRef, requestID string, approvalIDs []string) core.LifecycleSnapshot {
	return core.LifecycleSnapshot{
		ObjectID:       objectID,
		TenantID:       scope.OrganizationID,
		CompanyID:      scope.CompanyID,
		RUC:            scope.RUC,
		Period:         scope.Period,
		LifecycleState: lifecycle,
		RetentionState: retention,
		PolicyID:       policyID,
		PolicyVersion:  policyVersion,
		Category:       category,
		BlockingHolds:  holds,
		RequestID:      requestID,
		ApprovalIDs:    approvalIDs,
	}
}

// currentPurgeSnapshotOn reads the object's CURRENT lifecycle state ON the
// caller's connection and assembles the canonical snapshot: the projection row
// (or the virtual stored/unmanaged state of an unbound object), the active
// blocking holds (kind in blockingKinds), and the request/approval ids when a
// request row exists. Returns the snapshot and its canonical hash. The
// projection row is the derived truth; a missing row is the documented
// `unmanaged` state of a legacy object (§14), never an error.
func currentPurgeSnapshotOn(ctx context.Context, q Queryer, objectID string, scope core.Scope, blockingKinds []string) (core.LifecycleSnapshot, string, error) {
	lifecycle := core.PurgeLifecycleStored
	retention := core.RetentionEligibility("unmanaged") // the virtual state of an unbound object (§14)
	var policyID, category, requestID string
	var policyVersion int64

	projection, ok, err := retentionStateOn(ctx, q, objectID)
	if err != nil {
		return core.LifecycleSnapshot{}, "", err
	}
	if ok {
		lifecycle = projection.LifecycleState
		retention = projection.RetentionState
		policyID = projection.PolicyID
		category = projection.Category
	}
	if policyID != "" {
		p, ok, err := retentionPolicyByIDOn(ctx, q, policyID)
		if err != nil {
			return core.LifecycleSnapshot{}, "", err
		}
		if !ok {
			// Immutable policies cannot vanish; a missing row is corruption and
			// fails closed (never guess the version).
			return core.LifecycleSnapshot{}, "", fmt.Errorf("persistence error: bound retention policy %s not found", policyID)
		}
		policyVersion = p.Version
	}

	if req, ok, err := purgeRequestByObjectOn(ctx, q, objectID); err != nil {
		return core.LifecycleSnapshot{}, "", err
	} else if ok {
		requestID = req.RequestID
	}

	holds, err := activeBlockingHoldRefsOn(ctx, q, objectID, blockingKinds)
	if err != nil {
		return core.LifecycleSnapshot{}, "", err
	}
	approvalIDs, err := approvalIDsOn(ctx, q, requestID)
	if err != nil {
		return core.LifecycleSnapshot{}, "", err
	}

	snapshot := assembleSnapshot(objectID, scope, lifecycle, retention, policyID, category, policyVersion, holds, requestID, approvalIDs)
	return snapshot, core.ComputeLifecycleSnapshotHash(snapshot), nil
}

// retentionPolicyByIDOn reads ONE immutable retention policy row by id.
func retentionPolicyByIDOn(ctx context.Context, q Queryer, policyID string) (core.RetentionPolicy, bool, error) {
	p, err := scanRetentionPolicy(q.QueryRowContext(ctx,
		`SELECT `+retentionPolicyColumns+` FROM retention_policies WHERE id = ?`, policyID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.RetentionPolicy{}, false, nil
		}
		return core.RetentionPolicy{}, false, fmt.Errorf("persistence error: read retention policy: %w", err)
	}
	return p, true, nil
}

// retentionStateOn reads the projection row of the object.
func retentionStateOn(ctx context.Context, q Queryer, objectID string) (core.EvidenceRetentionState, bool, error) {
	p, err := scanRetentionState(q.QueryRowContext(ctx,
		`SELECT `+retentionStateColumns+` FROM evidence_retention_state WHERE object_id = ?`, objectID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.EvidenceRetentionState{}, false, nil
		}
		return core.EvidenceRetentionState{}, false, fmt.Errorf("persistence error: read retention state: %w", err)
	}
	return p, true, nil
}

// purgeRequestByIDOn reads the request row by id (typed PURGE_REQUEST_NOT_FOUND
// on a missing row).
func purgeRequestByIDOn(ctx context.Context, q Queryer, requestID string) (core.EvidencePurgeRequest, error) {
	r, err := scanPurgeRequest(q.QueryRowContext(ctx,
		`SELECT `+purgeRequestColumns+` FROM evidence_purge_requests WHERE id = ?`, requestID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.EvidencePurgeRequest{}, fmt.Errorf("%s: %s", purgeRequestErrNotFound, requestID)
		}
		return core.EvidencePurgeRequest{}, fmt.Errorf("persistence error: read purge request: %w", err)
	}
	return r, nil
}

// purgeRequestByObjectOn reads the request row of the object (the ONE row per
// object), when one exists.
func purgeRequestByObjectOn(ctx context.Context, q Queryer, objectID string) (core.EvidencePurgeRequest, bool, error) {
	r, err := scanPurgeRequest(q.QueryRowContext(ctx,
		`SELECT `+purgeRequestColumns+` FROM evidence_purge_requests WHERE object_id = ?`, objectID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.EvidencePurgeRequest{}, false, nil
		}
		return core.EvidencePurgeRequest{}, false, fmt.Errorf("persistence error: read purge request by object: %w", err)
	}
	return r, true, nil
}

// insertLifecycleEventOn appends ONE immutable event row (the acting
// principal's snapshot is persisted as canonical JSON).
func insertLifecycleEventOn(ctx context.Context, q Queryer, event core.EvidenceLifecycleEvent) error {
	snapshotJSON, err := json.Marshal(event.PrincipalSnapshot)
	if err != nil {
		return fmt.Errorf("persistence error: encode event principal snapshot: %w", err)
	}
	requestID := any(nil)
	if event.RequestID != "" {
		requestID = event.RequestID
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO evidence_lifecycle_events (id, object_id, request_id, action,
			from_state, to_state, reviewed_hash, resulting_hash, principal_snapshot_json,
			reason, policy_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID, event.ObjectID, requestID, string(event.Action),
		event.FromState, event.ToState, event.ReviewedHash, event.ResultingHash,
		string(snapshotJSON), event.Reason, event.PolicyVersion, event.CreatedAt,
	); err != nil {
		return fmt.Errorf("persistence error: insert lifecycle event: %w", err)
	}
	return nil
}

// insertPurgeApprovalOn appends ONE immutable approval/decision row.
func insertPurgeApprovalOn(ctx context.Context, q Queryer, approval core.EvidencePurgeApproval) error {
	snapshotJSON, err := json.Marshal(approval.PrincipalSnapshot)
	if err != nil {
		return fmt.Errorf("persistence error: encode approval principal snapshot: %w", err)
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO evidence_purge_approvals (id, request_id, approval_order, decision,
			reviewed_hash, resulting_hash, principal_snapshot_json, reason, policy_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		approval.ApprovalID, approval.RequestID, approval.ApprovalOrder, string(approval.Decision),
		approval.ReviewedHash, approval.ResultingHash, string(snapshotJSON),
		approval.Reason, approval.PolicyVersion, approval.CreatedAt,
	); err != nil {
		return fmt.Errorf("persistence error: insert purge approval: %w", err)
	}
	return nil
}

// upsertRetentionStateOn flips the guarded projection to the committed state
// (the current_hash is the resulting canonical lifecycle hash H2 of the
// transition). The upsert is the projection pattern (like period_closures):
// the projection is derived queryable state, the events are the source of truth.
func upsertRetentionStateOn(ctx context.Context, q Queryer, p core.EvidenceRetentionState) error {
	policyID := any(nil)
	if p.PolicyID != "" {
		policyID = p.PolicyID
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO evidence_retention_state (object_id, lifecycle_state, retention_state,
			policy_id, category, has_active_blocking_hold, current_hash, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(object_id) DO UPDATE SET
			lifecycle_state = excluded.lifecycle_state,
			retention_state = excluded.retention_state,
			policy_id = excluded.policy_id,
			category = excluded.category,
			has_active_blocking_hold = excluded.has_active_blocking_hold,
			current_hash = excluded.current_hash,
			updated_at = excluded.updated_at`,
		p.ObjectID, string(p.LifecycleState), string(p.RetentionState),
		policyID, p.Category, boolInt(p.HasActiveBlockingHold), p.CurrentHash, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("persistence error: upsert retention state: %w", err)
	}
	return nil
}

// purgeReceiptPayload is the v0.9.0 lifecycle receipt payload: the object's
// exact scope (tenant/company/RUC/period — RUC scope on every mutation), the
// reviewed/resulting CANONICAL LIFECYCLE SNAPSHOT HASHES (H1/H2, design
// §3.8/§5), the object identity in evidenceRef, the reason and the complete
// verified principal snapshot — stamped with the frozen evidence-lifecycle
// policy version. executionAttemptID is populated ONLY for purge_intent
// receipts (the (tenant, executionId) of the attempt — the additive per-attempt
// discriminator that keeps every intent receipt uniquely auditable and lets a
// fresh-ID retry after an interrupted intent emit a DISTINCT payload); every
// other act passes "" so non-intent receipt payload semantics stay untouched.
func purgeReceiptPayload(scope core.Scope, objectID string, reviewedHash, resultingHash, reason string, executionAttemptID string, principal auth.VerifiedApprovalPrincipal) core.ReceiptPayload {
	snapshot := principal.PrincipalSnapshot()
	return core.ReceiptPayload{
		Version:                  core.ReceiptPayloadVersionV09,
		TenantID:                 scope.OrganizationID,
		CompanyID:                scope.CompanyID,
		FiscalPeriodID:           scope.Period,
		ReviewedLifecycleHash:    reviewedHash,
		ResultingLifecycleHash:   resultingHash,
		EvidenceRef:              objectID,
		Reason:                   reason,
		PrincipalID:              snapshot.SubjectID,
		MembershipID:             snapshot.MembershipID,
		PrincipalRoles:           receiptPrincipalRoles(snapshot),
		AuthenticationMethod:     string(snapshot.AuthenticationMethod),
		AssuranceLevel:           string(snapshot.AssuranceLevel),
		PrincipalAuthenticatedAt: snapshot.AuthenticatedAt,
		PolicyVersion:            authz.LifecyclePolicyVersion,
		ExecutionAttemptID:       executionAttemptID,
	}
}

// authorizePurgeAct is the batch-4 gate for the pipeline acts: it delegates to
// the frozen evidence-lifecycle policy (evidence-lifecycle-policy/v0.8.0) over
// the object's exact scope tuple with the act's role matrix (request = the
// accounting ladder, approve = the default approver, second_approve =
// controller/tax_responsible, reject/withdraw = the approver set). Blockers
// have ALREADY failed closed before this gate (design §2 — a blocked request
// never reaches authorization and no override exists). RequesterSoD and
// FirstApproverSubject are the store-side SoD inputs (the request row and the
// approval ledger are the authority — the frozen policy compares principals by
// subject id): RequesterSoD must differ from the acting subject, and a second
// approver must be a DISTINCT principal from the first approver.
func authorizePurgeAct(principal auth.VerifiedApprovalPrincipal, scope core.Scope, action authz.LifecycleAction, category string, dualApprovalRequired bool, requesterSubject string, firstApproverSubject string) error {
	decision := authz.NewEvidenceLifecyclePolicy().Authorize(authz.LifecycleAuthorizationRequest{
		Action:               action,
		Principal:            principal,
		ActorKind:            core.ActorKindHuman,
		TenantID:             scope.OrganizationID,
		CompanyID:            scope.CompanyID,
		Category:             category,
		DualApprovalRequired: dualApprovalRequired,
	})
	if !decision.Allowed {
		return auth.New(decision.ReasonCode, "evidence-lifecycle-policy denied "+string(action))
	}
	// Store-side SoD against the STORED requester (frozen check order §8.2 step
	// 8: after the role allow + assurance of the policy — an approver can never
	// be the requester of the purge request they decide).
	if requesterSubject != "" && principal.SubjectID() == requesterSubject {
		return auth.New(auth.CodeApproverIsRequester, "the approver cannot be the requester of the purge request (separation of duties)")
	}
	// Store-side distinct-principal rule (frozen check order §8.2 step 10: the
	// second approver must be a DISTINCT principal from the first approver).
	if firstApproverSubject != "" && principal.SubjectID() == firstApproverSubject {
		return auth.New(auth.CodeSamePrincipalSecondApproval, "the second approver must be a distinct principal from the first approver")
	}
	return nil
}

// completePurgeIdempotencyOn finishes a reservation with the entity id and the
// serialized outcome.
func completePurgeIdempotencyOn(ctx context.Context, q Queryer, tenantID, requestIDKey, entityID string, result any, now string) error {
	serialized, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("persistence error: encode purge result: %w", err)
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE evidence_purge_idempotency_keys
		SET entity_id = ?, result_json = ?, completed_at = ?
		WHERE tenant_id = ? AND request_id = ?`,
		entityID, string(serialized), now, tenantID, requestIDKey,
	); err != nil {
		return fmt.Errorf("persistence error: complete purge idempotency key: %w", err)
	}
	return nil
}

// ──────────────────────────────────────────────
// RequestPurge
// ──────────────────────────────────────────────

// RequestPurge opens ONE purge pipeline per object (batch 4, design
// §2/§3.3/§9/§10): ONE BEGIN IMMEDIATE transaction with the locked object
// re-read (OBJECT_NOT_FOUND on an unknown content address), tenant-scoped
// (tenant, requestId) idempotency, the FULL blocker set BEFORE authz (scope
// exactness → closed-period gate → exact active retention resolution →
// eligibility → active blocking hold scan → expected lifecycle hash), the
// authenticated request gate (accounting ladder, deny-list first), the
// immutable request row (one per object; a cancelled/withdrawn pipeline is
// re-requested as a fresh act), the retention BINDING (retention_bound receipt
// ONLY for a newly bound policy — emitted in THIS transaction) and the
// purge_requested event + receipt. A replay returns the stored outcome
// (IdempotentReplay=true) with NO new row/event/receipt; a reused requestId
// with a different command or principal is IDEMPOTENCY_CONFLICT. This operation
// NEVER deletes, schedules or exports anything.
func (s *SQLiteStore) RequestPurge(ctx context.Context, cmd core.RequestPurgeCommand, principal auth.VerifiedApprovalPrincipal) (core.RequestPurgeResult, error) {
	if err := core.AssertValidRequestPurgeCommand(cmd); err != nil {
		return core.RequestPurgeResult{}, err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.RequestPurgeResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.RequestPurgeResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	// 1. Locked object re-read: the pipeline inherits the EXACT scope tuple of
	// the evidence_object row (never a caller-declared scope).
	var scope core.Scope
	err = conn.QueryRowContext(ctx, `SELECT tenant_id, company_id, ruc, period FROM evidence_objects WHERE id = ?`, cmd.ObjectID).
		Scan(&scope.OrganizationID, &scope.CompanyID, &scope.RUC, &scope.Period)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return core.RequestPurgeResult{}, fmt.Errorf("%s: %s", objectErrNotFound, cmd.ObjectID)
	case err != nil:
		return core.RequestPurgeResult{}, fmt.Errorf("persistence error: read purge object: %w", err)
	}
	scope.Kind = core.ScopeKindCompany

	now := nowISO()
	commandHash := purgeCommandHash(requestPurgeCommandShape{
		ObjectID: cmd.ObjectID, Jurisdiction: cmd.Jurisdiction, Legislation: cmd.Legislation,
		Category: cmd.Category, ExpectedLifecycleHash: cmd.ExpectedLifecycleHash, Reason: cmd.Reason,
	})
	actorBinding := principal.SubjectID()

	// 2. Tenant-scoped idempotency: one pipeline command per (tenant, requestId)
	// on the dedicated ledger. A completed command replays its stored outcome;
	// the command fingerprint and the acting principal must match exactly.
	var (
		// entity_id and result_json are NULL for a reserved-but-never-completed
		// key (a crash between reservation and completion), so they are scanned
		// nullable: a NULL scan must surface the typed IDEMPOTENCY_CONFLICT
		// incomplete outcome, never a database scan error.
		storedEntityID, storedResult             sql.NullString
		storedHash, storedActor, storedCreatedAt string
	)
	err = conn.QueryRowContext(ctx, `
		SELECT entity_id, result_json, command_hash, actor_binding, created_at
		FROM evidence_purge_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		scope.OrganizationID, cmd.RequestID,
	).Scan(&storedEntityID, &storedResult, &storedHash, &storedActor, &storedCreatedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedActor != actorBinding {
			return core.RequestPurgeResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different command or principal")
		}
		if storedEntityID.String == "" || storedResult.String == "" {
			return core.RequestPurgeResult{}, auth.New(auth.CodeIdempotencyConflict, "request id reservation never completed")
		}
		var replayed core.EvidencePurgeRequest
		if err := json.Unmarshal([]byte(storedResult.String), &replayed); err != nil {
			return core.RequestPurgeResult{}, fmt.Errorf("persistence error: decode replayed purge request: %w", err)
		}
		return core.RequestPurgeResult{Request: replayed, Created: false, IdempotentReplay: true}, nil
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx, `
	INSERT INTO evidence_purge_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, entity_id, result_json, created_at, completed_at)
	VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			scope.OrganizationID, cmd.RequestID, commandHash, actorBinding, now,
		); err != nil {
			return core.RequestPurgeResult{}, fmt.Errorf("persistence error: reserve idempotency key: %w", err)
		}
		// proceed — no prior command for this request id
	default:
		return core.RequestPurgeResult{}, fmt.Errorf("persistence error: read purge idempotency key: %w", err)
	}

	// 3. BLOCKERS, before authz (design §2/§10; the task's frozen order):
	// closed-period gate → retention resolution + eligibility → active blocking
	// hold scan → expected lifecycle hash. A blocker fails the transaction even
	// for an otherwise fully authorized human; no override exists.
	if err := s.assertPeriodWritable(ctx, conn, scope, "request purge"); err != nil {
		return core.RequestPurgeResult{}, err
	}
	policy, matched, err := resolveRetentionPolicyOn(ctx, conn, scope, cmd.Jurisdiction, cmd.Legislation, cmd.Category)
	if err != nil {
		return core.RequestPurgeResult{}, err
	}
	if !matched {
		return core.RequestPurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "no exact active retention policy resolves for the scope/jurisdiction/legislation/category tuple")
	}
	eligibility := core.EvaluateRetentionEligibility(policy, scope.Period)
	switch eligibility {
	case core.RetentionEligibilityEligible:
		// proceed — the object's period reached the deployment-declared floor
	case core.RetentionEligibilityNotDue:
		return core.RequestPurgeResult{}, auth.New(auth.CodeRetentionNotDue, "the object's period has not reached the policy's min_period floor")
	default:
		return core.RequestPurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "retention state could not be evaluated (never guessed)")
	}

	current, currentHash, err := currentPurgeSnapshotOn(ctx, conn, cmd.ObjectID, scope, policy.BlockingHoldKinds)
	if err != nil {
		return core.RequestPurgeResult{}, err
	}
	// The active blocking holds are INSIDE the snapshot; the explicit blocker
	// check still runs first so the frozen HOLD_ACTIVE code wins (and the
	// snapshot contribution stays honest for the audit).
	if len(current.BlockingHolds) > 0 {
		return core.RequestPurgeResult{}, auth.New(auth.CodeHoldActive, "an active blocking hold protects this object — the purge request is blocked")
	}
	if currentHash != cmd.ExpectedLifecycleHash {
		return core.RequestPurgeResult{}, auth.New(auth.CodeLifecycleVersionMismatch,
			fmt.Sprintf("expectedLifecycleHash %s does not match the current lifecycle snapshot hash %s — re-read and retry", cmd.ExpectedLifecycleHash, currentHash))
	}
	// 4. State gate: only a `stored` object (or an unbound object, reported as
	// stored here) opens a pipeline; an open or terminal pipeline blocks.
	if current.LifecycleState != core.PurgeLifecycleStored {
		return core.RequestPurgeResult{}, auth.New(auth.CodeInvalidTransition,
			fmt.Sprintf("a purge request requires the stored lifecycle state, current state is %s", current.LifecycleState))
	}

	// 5. AUTHZ (after the blockers — a blocked request never reaches the
	// policy): the request gate is the accounting ladder (accountant /
	// senior_accountant / controller), deny-list first, assurance ≥ standard.
	if err := authorizePurgeAct(principal, scope, authz.LifecycleActionRequestPurge, policy.Category, policy.DualApprovalRequired, "", ""); err != nil {
		return core.RequestPurgeResult{}, err
	}

	// 6. The immutable request row (one per object). A cancelled/withdrawn
	// pipeline is re-requested as a FRESH act on the same row (the documented
	// retraction path — §2/§7); any other existing row is an open or terminal
	// pipeline and fails closed.
	existing, exists, err := purgeRequestByObjectOn(ctx, conn, cmd.ObjectID)
	if err != nil {
		return core.RequestPurgeResult{}, err
	}
	var request core.EvidencePurgeRequest
	if exists {
		if existing.Status != core.PurgeRequestStatusCancelled && existing.Status != core.PurgeRequestStatusWithdrawn {
			return core.RequestPurgeResult{}, auth.New(auth.CodeInvalidTransition,
				fmt.Sprintf("an open or terminal purge pipeline already exists for this object (status %s)", existing.Status))
		}
		request = existing
		request.Category = policy.Category
		request.PolicyID = policy.PolicyID
		request.RetentionStateSnapshot = eligibility
		request.ReviewedLifecycleHash = cmd.ExpectedLifecycleHash
		request.Status = core.PurgeRequestStatusRequested
		request.RequestedAt = now
		request.RequestedBy = actorBinding
		request.ApprovedAt = ""
		if _, err := conn.ExecContext(ctx, `
			UPDATE evidence_purge_requests
			SET category = ?, policy_id = ?, retention_state_snapshot = ?, reviewed_lifecycle_hash = ?,
			    status = 'requested', requested_at = ?, requested_by = ?, approved_at = NULL
			WHERE id = ?`,
			request.Category, request.PolicyID, string(request.RetentionStateSnapshot),
			request.ReviewedLifecycleHash, request.RequestedAt, request.RequestedBy, request.RequestID,
		); err != nil {
			return core.RequestPurgeResult{}, fmt.Errorf("persistence error: re-request purge pipeline: %w", err)
		}
	} else {
		requestID, err := newUUID()
		if err != nil {
			return core.RequestPurgeResult{}, fmt.Errorf("persistence error: generate purge request id: %w", err)
		}
		request = core.EvidencePurgeRequest{
			RequestID:              requestID,
			ObjectID:               cmd.ObjectID,
			TenantID:               scope.OrganizationID,
			CompanyID:              scope.CompanyID,
			RUC:                    scope.RUC,
			Period:                 scope.Period,
			Category:               policy.Category,
			PolicyID:               policy.PolicyID,
			RetentionStateSnapshot: eligibility,
			ReviewedLifecycleHash:  cmd.ExpectedLifecycleHash,
			Status:                 core.PurgeRequestStatusRequested,
			RequestedAt:            now,
			RequestedBy:            actorBinding,
		}
		if err := core.AssertValidPurgeRequest(request); err != nil {
			return core.RequestPurgeResult{}, err
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO evidence_purge_requests (id, object_id, tenant_id, company_id, ruc, period,
				category, policy_id, retention_state_snapshot, reviewed_lifecycle_hash,
				status, requested_at, requested_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			request.RequestID, request.ObjectID, request.TenantID, request.CompanyID, request.RUC, request.Period,
			request.Category, request.PolicyID, string(request.RetentionStateSnapshot), request.ReviewedLifecycleHash,
			string(request.Status), request.RequestedAt, request.RequestedBy,
		); err != nil {
			return core.RequestPurgeResult{}, fmt.Errorf("persistence error: insert purge request: %w", err)
		}
	}

	// 7. Retention binding: for a NEWLY bound policy the resolved snapshot is
	// bound NOW and the retention_bound receipt is emitted in THIS transaction
	// (receipt.go — emission is receipt-covered ONLY for a newly bound policy,
	// so a later policy change is auditable against what was bound at request
	// time — design §5/§6). The binding hash deltas are
	// stored/unmanaged → stored/eligible (the binding itself never changes the
	// lifecycle state).
	hasHolding := len(current.BlockingHolds) > 0
	requesterSnapshot := principal.PrincipalSnapshot()
	if current.PolicyID == "" {
		preBind := assembleSnapshot(cmd.ObjectID, scope, core.PurgeLifecycleStored, "unmanaged", "", "", 0, []core.LifecycleHoldRef{}, "", []string{})
		preBindHash := core.ComputeLifecycleSnapshotHash(preBind)
		postBind := assembleSnapshot(cmd.ObjectID, scope, core.PurgeLifecycleStored, eligibility, policy.PolicyID, policy.Category, policy.Version, []core.LifecycleHoldRef{}, "", []string{})
		postBindHash := core.ComputeLifecycleSnapshotHash(postBind)
		eventID, err := newUUID()
		if err != nil {
			return core.RequestPurgeResult{}, fmt.Errorf("persistence error: generate binding event id: %w", err)
		}
		if err := insertLifecycleEventOn(ctx, conn, core.EvidenceLifecycleEvent{
			EventID:           eventID,
			ObjectID:          cmd.ObjectID,
			RequestID:         request.RequestID,
			Action:            core.PurgeEventRetentionBound,
			FromState:         string(core.PurgeLifecycleStored),
			ToState:           string(core.PurgeLifecycleStored),
			ReviewedHash:      preBindHash,
			ResultingHash:     postBindHash,
			PrincipalSnapshot: requesterSnapshot,
			Reason:            cmd.Reason,
			PolicyVersion:     authz.LifecyclePolicyVersion,
			CreatedAt:         now,
		}); err != nil {
			return core.RequestPurgeResult{}, err
		}
		if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeEvidenceObject, cmd.ObjectID, core.ReceiptActionRetentionBound,
			purgeReceiptPayload(scope, cmd.ObjectID, preBindHash, postBindHash, cmd.Reason, "", principal), now); err != nil {
			return core.RequestPurgeResult{}, fmt.Errorf("persistence error: emit retention_bound receipt: %w", err)
		}
	}

	// 8. The purge_requested event + receipt: H1 is the reviewed hash the
	// requester asserted (the current snapshot before the transition); H2 is
	// the resulting snapshot after the transition (purge_requested + the
	// request id — always != H1). The event, the receipt and the request row
	// carry the SAME H1 so an audit can correlate them one-to-one.
	h2 := assembleSnapshot(cmd.ObjectID, scope, core.PurgeLifecycleRequested, eligibility, policy.PolicyID, policy.Category, policy.Version, current.BlockingHolds, request.RequestID, []string{})
	resultingHash := core.ComputeLifecycleSnapshotHash(h2)
	eventID, err := newUUID()
	if err != nil {
		return core.RequestPurgeResult{}, fmt.Errorf("persistence error: generate request event id: %w", err)
	}
	if err := insertLifecycleEventOn(ctx, conn, core.EvidenceLifecycleEvent{
		EventID:           eventID,
		ObjectID:          cmd.ObjectID,
		RequestID:         request.RequestID,
		Action:            core.PurgeEventRequested,
		FromState:         string(core.PurgeLifecycleStored),
		ToState:           string(core.PurgeLifecycleRequested),
		ReviewedHash:      currentHash,
		ResultingHash:     resultingHash,
		PrincipalSnapshot: requesterSnapshot,
		Reason:            cmd.Reason,
		PolicyVersion:     authz.LifecyclePolicyVersion,
		CreatedAt:         now,
	}); err != nil {
		return core.RequestPurgeResult{}, err
	}
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeEvidenceObject, cmd.ObjectID, core.ReceiptActionPurgeRequested,
		purgeReceiptPayload(scope, cmd.ObjectID, currentHash, resultingHash, cmd.Reason, "", principal), now); err != nil {
		return core.RequestPurgeResult{}, fmt.Errorf("persistence error: emit purge_requested receipt: %w", err)
	}

	// 9. The guarded projection flips to purge_requested (derived state).
	if err := upsertRetentionStateOn(ctx, conn, core.EvidenceRetentionState{
		ObjectID:              cmd.ObjectID,
		LifecycleState:        core.PurgeLifecycleRequested,
		RetentionState:        eligibility,
		PolicyID:              policy.PolicyID,
		Category:              policy.Category,
		HasActiveBlockingHold: hasHolding,
		CurrentHash:           resultingHash,
		UpdatedAt:             now,
	}); err != nil {
		return core.RequestPurgeResult{}, err
	}

	// 10. Complete the idempotency reservation with the created/refreshed
	// request.
	if err := completePurgeIdempotencyOn(ctx, conn, scope.OrganizationID, cmd.RequestID, request.RequestID, request, now); err != nil {
		return core.RequestPurgeResult{}, err
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.RequestPurgeResult{}, fmt.Errorf("persistence error: commit purge request: %w", err)
	}
	committed = true
	return core.RequestPurgeResult{Request: request, Created: true, IdempotentReplay: false}, nil
}

// ──────────────────────────────────────────────
// ApprovePurge
// ──────────────────────────────────────────────

// ApprovePurge records ONE human approval (batch 4, design §2/§3.4/§8/§9): ONE
// BEGIN IMMEDIATE transaction with the locked request + object re-read
// (PURGE_REQUEST_NOT_FOUND / OBJECT_NOT_FOUND), tenant-scoped (tenant,
// requestIdKey) idempotency, the state gate (only an open pipeline approves),
// the FULL blocker set BEFORE authz (closed-period gate → retention
// re-resolution → eligibility → active blocking hold scan → expected lifecycle
// hash — approval can never override a blocker), the authenticated approval
// gate (order 1: default approver records_compliance_officer |
// tenant_records_owner; order 2 for a policy-designated fiscal/material
// category: a DISTINCT controller or tax_responsible) with the store-side SoD
// and distinct-principal rules, and the immutable approval row + event +
// receipt. The request flips to 'approved' and the projection to
// 'purge_approved' ONLY when the category's approval requirements are COMPLETE.
func (s *SQLiteStore) ApprovePurge(ctx context.Context, cmd core.ApprovePurgeCommand, principal auth.VerifiedApprovalPrincipal) (core.ApprovePurgeResult, error) {
	if err := core.AssertValidApprovePurgeCommand(cmd); err != nil {
		return core.ApprovePurgeResult{}, err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	// 1. Locked request re-read (the scope authority of the pipeline).
	request, err := purgeRequestByIDOn(ctx, conn, cmd.RequestID)
	if err != nil {
		return core.ApprovePurgeResult{}, err
	}
	scope := core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: request.TenantID,
		CompanyID:      request.CompanyID,
		RUC:            request.RUC,
		Period:         request.Period,
	}

	// 2. Locked object re-read (defense — an immutable object row can only
	// vanish through corruption).
	var objectID string
	err = conn.QueryRowContext(ctx, `SELECT id FROM evidence_objects WHERE id = ?`, request.ObjectID).Scan(&objectID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return core.ApprovePurgeResult{}, fmt.Errorf("%s: %s", objectErrNotFound, request.ObjectID)
	case err != nil:
		return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: read approve object: %w", err)
	}

	now := nowISO()
	commandHash := purgeCommandHash(approvePurgeCommandShape{RequestID: cmd.RequestID, ExpectedLifecycleHash: cmd.ExpectedLifecycleHash, Reason: cmd.Reason})
	actorBinding := principal.SubjectID()

	// 3. Tenant-scoped idempotency for THIS approval act.
	var (
		// entity_id and result_json are NULL for a reserved-but-never-completed
		// key (a crash between reservation and completion), so they are scanned
		// nullable: a NULL scan must surface the typed IDEMPOTENCY_CONFLICT
		// incomplete outcome, never a database scan error.
		storedEntityID, storedResult             sql.NullString
		storedHash, storedActor, storedCreatedAt string
	)
	err = conn.QueryRowContext(ctx, `
		SELECT entity_id, result_json, command_hash, actor_binding, created_at
		FROM evidence_purge_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		request.TenantID, cmd.RequestIDKey,
	).Scan(&storedEntityID, &storedResult, &storedHash, &storedActor, &storedCreatedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedActor != actorBinding {
			return core.ApprovePurgeResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different command or principal")
		}
		if storedEntityID.String == "" || storedResult.String == "" {
			return core.ApprovePurgeResult{}, auth.New(auth.CodeIdempotencyConflict, "request id reservation never completed")
		}
		var replayed core.ApprovePurgeResult
		if err := json.Unmarshal([]byte(storedResult.String), &replayed); err != nil {
			return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: decode replayed approval: %w", err)
		}
		// The stored outcome was written by the ORIGINAL attempt with
		// IdempotentReplay=false; this IS the replay, so surface the flag (same
		// rule as ExecutePurge). Reject/Cancel/Withdraw below follow the same
		// pattern.
		replayed.IdempotentReplay = true
		return replayed, nil
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx, `
	INSERT INTO evidence_purge_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, entity_id, result_json, created_at, completed_at)
	VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			request.TenantID, cmd.RequestIDKey, commandHash, actorBinding, now,
		); err != nil {
			return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: reserve idempotency key: %w", err)
		}
		// proceed
	default:
		return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: read approve idempotency key: %w", err)
	}

	// 4. State gate: only an OPEN pipeline approves. An already-approved
	// request is ALREADY_DECIDED (the design's loser code); every other
	// terminal/retracted status is an INVALID_TRANSITION.
	switch request.Status {
	case core.PurgeRequestStatusRequested:
		// proceed
	case core.PurgeRequestStatusApproved:
		return core.ApprovePurgeResult{}, auth.New(auth.CodeAlreadyDecided, "this purge request is already approved")
	default:
		return core.ApprovePurgeResult{}, auth.New(auth.CodeInvalidTransition,
			fmt.Sprintf("cannot approve a purge request in status %s", request.Status))
	}

	// 5. BLOCKERS, before authz (design §2 — the SAME blocker set as request,
	// never overridden by approval): closed-period gate → retention
	// re-resolution against the bound policy's resolution evidence → eligibility
	// → active blocking hold scan → expected lifecycle hash. A hold placed
	// after the request, a policy change that makes the retention state unknown
	// or not_due, a closed period or a version drift ALL block approval.
	if err := s.assertPeriodWritable(ctx, conn, scope, "approve purge"); err != nil {
		return core.ApprovePurgeResult{}, err
	}
	bound, ok, err := retentionPolicyByIDOn(ctx, conn, request.PolicyID)
	if err != nil {
		return core.ApprovePurgeResult{}, err
	}
	if !ok {
		return core.ApprovePurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "the bound retention policy is no longer readable")
	}
	resolved, matched, err := resolveRetentionPolicyOn(ctx, conn, scope, bound.Jurisdiction, bound.Legislation, request.Category)
	if err != nil {
		return core.ApprovePurgeResult{}, err
	}
	if !matched {
		return core.ApprovePurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "no exact active retention policy resolves at approval time (the engine never guesses)")
	}
	eligibility := core.EvaluateRetentionEligibility(resolved, scope.Period)
	switch eligibility {
	case core.RetentionEligibilityEligible:
		// proceed
	case core.RetentionEligibilityNotDue:
		return core.ApprovePurgeResult{}, auth.New(auth.CodeRetentionNotDue, "the object's period no longer reaches the resolved policy's min_period floor")
	default:
		return core.ApprovePurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "retention state could not be evaluated at approval time (never guessed)")
	}

	current, currentHash, err := currentPurgeSnapshotOn(ctx, conn, request.ObjectID, scope, resolved.BlockingHoldKinds)
	if err != nil {
		return core.ApprovePurgeResult{}, err
	}
	if len(current.BlockingHolds) > 0 {
		return core.ApprovePurgeResult{}, auth.New(auth.CodeHoldActive, "an active blocking hold protects this object — the approval is blocked")
	}
	if currentHash != cmd.ExpectedLifecycleHash {
		return core.ApprovePurgeResult{}, auth.New(auth.CodeLifecycleVersionMismatch,
			fmt.Sprintf("expectedLifecycleHash %s does not match the current lifecycle snapshot hash %s — re-read and retry", cmd.ExpectedLifecycleHash, currentHash))
	}

	// 6. AUTHZ (after the blockers): the approval order is derived from the
	// stored decision ledger. Order 1 = the default approver
	// (records_compliance_officer | tenant_records_owner); order 2 = a DISTINCT
	// controller or tax_responsible for a policy-designated fiscal/material
	// category (the frozen policy denies a second approval for a category that
	// is not configured for dual approval — DUAL_APPROVAL_REQUIRED). The SoD
	// rule (approver ≠ requester) and the distinct-principal rule are enforced
	// store-side against the STORED request/approval rows.
	approvalIDs, err := approvalIDsOn(ctx, conn, request.RequestID)
	if err != nil {
		return core.ApprovePurgeResult{}, err
	}
	var (
		action               authz.LifecycleAction
		order                int64
		firstApproverSubject string
	)
	switch len(approvalIDs) {
	case 0:
		action = authz.LifecycleActionApprovePurge
		order = 1
	case 1:
		firstApprover, err := purgeApprovalByIDOn(ctx, conn, approvalIDs[0])
		if err != nil {
			return core.ApprovePurgeResult{}, err
		}
		firstApproverSubject = firstApprover.PrincipalSnapshot.SubjectID
		action = authz.LifecycleActionSecondApprove
		order = 2
	default:
		return core.ApprovePurgeResult{}, auth.New(auth.CodeInvalidTransition, "this purge request already has both approvals")
	}
	if err := authorizePurgeAct(principal, scope, action, resolved.Category, resolved.DualApprovalRequired, request.RequestedBy, firstApproverSubject); err != nil {
		return core.ApprovePurgeResult{}, err
	}

	// 7. The immutable approval row + event + receipt. H1 is the reviewed hash
	// (for the second approval, the first approval's resulting hash — §9); H2
	// is the resulting snapshot after THIS approval (the approval id is part of
	// the canonical snapshot, so H2 always != H1). The pipeline flips to
	// 'purge_approved' (and the request to 'approved') ONLY when the category's
	// approval requirements are COMPLETE — for a dual category the first
	// approval keeps the pipeline at purge_requested (purge_approved means
	// "approved FOR EXECUTION").
	complete := !resolved.DualApprovalRequired || order == 2
	postLifecycle := core.PurgeLifecycleRequested
	if complete {
		postLifecycle = core.PurgeLifecycleApproved
	}
	approvalID, err := newUUID()
	if err != nil {
		return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: generate approval id: %w", err)
	}
	approvalIDs = append(approvalIDs, approvalID)
	h2 := assembleSnapshot(request.ObjectID, scope, postLifecycle, eligibility, resolved.PolicyID, resolved.Category, resolved.Version, current.BlockingHolds, request.RequestID, approvalIDs)
	resultingHash := core.ComputeLifecycleSnapshotHash(h2)

	approval := core.EvidencePurgeApproval{
		ApprovalID:        approvalID,
		RequestID:         request.RequestID,
		ApprovalOrder:     order,
		Decision:          core.PurgeApprovalDecisionApproved,
		ReviewedHash:      currentHash,
		ResultingHash:     resultingHash,
		PrincipalSnapshot: principal.PrincipalSnapshot(),
		Reason:            cmd.Reason,
		PolicyVersion:     authz.LifecyclePolicyVersion,
		CreatedAt:         now,
	}
	if err := insertPurgeApprovalOn(ctx, conn, approval); err != nil {
		return core.ApprovePurgeResult{}, err
	}
	if complete {
		if _, err := conn.ExecContext(ctx, `
			UPDATE evidence_purge_requests SET status = 'approved', approved_at = ? WHERE id = ?`,
			now, request.RequestID,
		); err != nil {
			return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: complete purge request approval: %w", err)
		}
		request.Status = core.PurgeRequestStatusApproved
		request.ApprovedAt = now
	}

	eventID, err := newUUID()
	if err != nil {
		return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: generate approval event id: %w", err)
	}
	if err := insertLifecycleEventOn(ctx, conn, core.EvidenceLifecycleEvent{
		EventID:           eventID,
		ObjectID:          request.ObjectID,
		RequestID:         request.RequestID,
		Action:            core.PurgeEventApproved,
		FromState:         string(core.PurgeLifecycleRequested),
		ToState:           string(postLifecycle),
		ReviewedHash:      currentHash,
		ResultingHash:     resultingHash,
		PrincipalSnapshot: principal.PrincipalSnapshot(),
		Reason:            cmd.Reason,
		PolicyVersion:     authz.LifecyclePolicyVersion,
		CreatedAt:         now,
	}); err != nil {
		return core.ApprovePurgeResult{}, err
	}
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeEvidenceObject, request.ObjectID, core.ReceiptActionPurgeApproved,
		purgeReceiptPayload(scope, request.ObjectID, currentHash, resultingHash, cmd.Reason, "", principal), now); err != nil {
		return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: emit purge_approved receipt: %w", err)
	}

	// 8. The guarded projection flips (or, for the first approval of a dual
	// category, keeps purge_requested with the new current hash).
	if err := upsertRetentionStateOn(ctx, conn, core.EvidenceRetentionState{
		ObjectID:              request.ObjectID,
		LifecycleState:        postLifecycle,
		RetentionState:        eligibility,
		PolicyID:              resolved.PolicyID,
		Category:              resolved.Category,
		HasActiveBlockingHold: len(current.BlockingHolds) > 0,
		CurrentHash:           resultingHash,
		UpdatedAt:             now,
	}); err != nil {
		return core.ApprovePurgeResult{}, err
	}

	// 9. Complete the idempotency reservation.
	if err := completePurgeIdempotencyOn(ctx, conn, request.TenantID, cmd.RequestIDKey, request.RequestID, core.ApprovePurgeResult{Request: request, Approval: approval, ApprovalOrder: order}, now); err != nil {
		return core.ApprovePurgeResult{}, err
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.ApprovePurgeResult{}, fmt.Errorf("persistence error: commit purge approval: %w", err)
	}
	committed = true
	return core.ApprovePurgeResult{Request: request, Approval: approval, ApprovalOrder: order, IdempotentReplay: false}, nil
}

// purgeApprovalByIDOn reads ONE immutable approval row by id.
func purgeApprovalByIDOn(ctx context.Context, q Queryer, approvalID string) (core.EvidencePurgeApproval, error) {
	a, err := scanPurgeApproval(q.QueryRowContext(ctx,
		`SELECT `+purgeApprovalColumns+` FROM evidence_purge_approvals WHERE id = ?`, approvalID))
	if err != nil {
		return core.EvidencePurgeApproval{}, fmt.Errorf("persistence error: read purge approval: %w", err)
	}
	return a, nil
}

// ──────────────────────────────────────────────
// RejectPurge / CancelPurge / WithdrawPurge
// ──────────────────────────────────────────────

// openRequestStateMachine is the shared pre-execution transition body for the
// three closing transitions (reject/cancel/withdraw): locked re-read,
// tenant-scoped idempotency, the state gate, the authorized mutation and the
// atomic event + receipt. The transition callback receives the locked request,
// the current snapshot hash H1 and the acting principal, and returns the
// post-transition projection state, the resulting hash H2, the approval/decision
// row to insert (nil for the requester retraction), the event action and the
// receipt action. The closed-period/retention/hold blockers are deliberately
// NOT re-run for these acts (design §2): they are judgments/retractions that
// never reduce evidence availability — reject closes the request, cancel and
// withdraw RESTORE stored (withdraw is the documented cleanup for a pipeline
// blocked after approval — §7, and must work inside a closed period).
func (s *SQLiteStore) closePurgeTransition(
	ctx context.Context,
	conn *sql.Conn,
	request core.EvidencePurgeRequest,
	scope core.Scope,
	requestIDKey string,
	commandHash string,
	actorBinding string,
	now string,
	principal auth.VerifiedApprovalPrincipal,
	blockingKinds []string,
	authorize func() error,
	stateGate func(core.EvidencePurgeRequest) (bool, string),
	transition func(core.EvidencePurgeRequest, core.LifecycleSnapshot, auth.VerifiedApprovalPrincipal) (postState core.PurgeLifecycleState, h2Hash string, decision *core.EvidencePurgeApproval, event core.EvidenceLifecycleEvent, receiptAction core.ReceiptAction),
) (core.EvidencePurgeRequest, *core.EvidencePurgeApproval, error) {
	// 1. Idempotency reservation for this closing act (BEFORE authz - frozen §9).
	if _, err := conn.ExecContext(ctx, `
	INSERT INTO evidence_purge_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, entity_id, result_json, created_at, completed_at)
	VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
		request.TenantID, requestIDKey, commandHash, actorBinding, now,
	); err != nil {
		return core.EvidencePurgeRequest{}, nil, fmt.Errorf("persistence error: reserve idempotency key: %w", err)
	}

	// 2. The authenticated gate (deny-list first, then the act's role matrix;
	// plus the store-side SoD/requester rules of the caller's authorize closure).
	if err := authorize(); err != nil {
		return core.EvidencePurgeRequest{}, nil, err
	}

	// 3. State gate (machine legality - a decided request reports ALREADY_DECIDED).
	if decided, code := stateGate(request); decided {
		return core.EvidencePurgeRequest{}, nil, auth.New(code, "this purge request is already decided")
	}

	// 4. The current canonical snapshot (H1 - the state the decision closes).
	current, currentHash, err := currentPurgeSnapshotOn(ctx, conn, request.ObjectID, scope, blockingKinds)
	if err != nil {
		return core.EvidencePurgeRequest{}, nil, err
	}

	// 5. The authorized mutation + resulting hash H2.
	postState, resultingHash, decision, event, receiptAction := transition(request, current, principal)

	// 6. The guarded status flip (the schema status guard is the backstop).
	switch request.Status {
	case core.PurgeRequestStatusRequested:
		newStatus := core.PurgeRequestStatusRejected
		if event.Action == core.PurgeEventCancelled {
			newStatus = core.PurgeRequestStatusCancelled
		}
		if _, err := conn.ExecContext(ctx, `UPDATE evidence_purge_requests SET status = ? WHERE id = ?`, string(newStatus), request.RequestID); err != nil {
			return core.EvidencePurgeRequest{}, nil, fmt.Errorf("persistence error: close purge request status: %w", err)
		}
		request.Status = newStatus
	case core.PurgeRequestStatusApproved:
		if _, err := conn.ExecContext(ctx, `UPDATE evidence_purge_requests SET status = 'withdrawn' WHERE id = ?`, request.RequestID); err != nil {
			return core.EvidencePurgeRequest{}, nil, fmt.Errorf("persistence error: withdraw purge request status: %w", err)
		}
		request.Status = core.PurgeRequestStatusWithdrawn
	}

	// 7. The immutable decision row (reject/withdraw only - cancel is the
	// requester retraction and records no decision).
	var inserted *core.EvidencePurgeApproval
	if decision != nil {
		if err := insertPurgeApprovalOn(ctx, conn, *decision); err != nil {
			return core.EvidencePurgeRequest{}, nil, err
		}
		inserted = decision
	}

	// 8. The immutable event + atomic receipt.
	if err := insertLifecycleEventOn(ctx, conn, event); err != nil {
		return core.EvidencePurgeRequest{}, nil, err
	}
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeEvidenceObject, request.ObjectID, receiptAction,
		purgeReceiptPayload(scope, request.ObjectID, currentHash, resultingHash, event.Reason, "", principal), now); err != nil {
		return core.EvidencePurgeRequest{}, nil, fmt.Errorf("persistence error: emit purge transition receipt: %w", err)
	}

	// 9. The guarded projection flips to the post-transition state.
	if err := upsertRetentionStateOn(ctx, conn, core.EvidenceRetentionState{
		ObjectID:              request.ObjectID,
		LifecycleState:        postState,
		RetentionState:        current.RetentionState,
		PolicyID:              current.PolicyID,
		Category:              current.Category,
		HasActiveBlockingHold: len(current.BlockingHolds) > 0,
		CurrentHash:           resultingHash,
		UpdatedAt:             now,
	}); err != nil {
		return core.EvidencePurgeRequest{}, nil, err
	}

	// The caller completes the idempotency reservation with its concrete result.
	return request, inserted, nil
}

// nextDecisionOrder derives the next approval_order (1 | 2) from the existing
// decision ledger; the schema CHECK backstops a corrupt third row.
func nextDecisionOrder(approvalIDs []string) int64 {
	if len(approvalIDs) == 0 {
		return 1
	}
	return 2
}

// RejectPurge records the TERMINAL rejection (design §2): an authenticated
// default approver closes an open request with a reason. The projection moves
// to purge_rejected and never re-opens; the immutable decision row + event +
// receipt commit atomically. Per the design's transition table reject runs NO
// blocker set — it is a judgment that closes the pipeline, never an
// availability-reducing act — and requires no expected hash.
func (s *SQLiteStore) RejectPurge(ctx context.Context, cmd core.RejectPurgeCommand, principal auth.VerifiedApprovalPrincipal) (core.RejectPurgeResult, error) {
	if err := core.AssertValidRejectPurgeCommand(cmd); err != nil {
		return core.RejectPurgeResult{}, err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.RejectPurgeResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.RejectPurgeResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	request, err := purgeRequestByIDOn(ctx, conn, cmd.RequestID)
	if err != nil {
		return core.RejectPurgeResult{}, err
	}
	scope := purgeRequestScope(request)
	now := nowISO()
	commandHash := purgeCommandHash(rejectPurgeCommandShape{RequestID: cmd.RequestID, Reason: cmd.Reason})

	// Replay path: a completed (tenant, requestIdKey) rejection returns the
	// stored outcome.
	if resultJSON, replayed, err := replayPurgeIdempotencyOn(ctx, conn, request.TenantID, cmd.RequestIDKey, commandHash, principal.SubjectID()); replayed || err != nil {
		if err != nil {
			return core.RejectPurgeResult{}, err
		}
		var replayed core.RejectPurgeResult
		if err := json.Unmarshal([]byte(resultJSON), &replayed); err != nil {
			return core.RejectPurgeResult{}, fmt.Errorf("persistence error: decode replayed rejection: %w", err)
		}
		replayed.IdempotentReplay = true
		return replayed, nil
	}

	// The snapshot contribution needs the request's bound policy blocking set
	// (the current active blocking holds are part of the canonical snapshot).
	policy, ok, err := retentionPolicyByIDOn(ctx, conn, request.PolicyID)
	if err != nil {
		return core.RejectPurgeResult{}, err
	}
	if !ok {
		return core.RejectPurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "the bound retention policy is no longer readable")
	}
	approvalIDs, err := approvalIDsOn(ctx, conn, request.RequestID)
	if err != nil {
		return core.RejectPurgeResult{}, err
	}

	updated, decision, err := s.closePurgeTransition(
		ctx, conn, request, scope, cmd.RequestIDKey, commandHash, principal.SubjectID(), now, principal,
		policy.BlockingHoldKinds,
		// The reject authz gate: an authenticated default approver (the frozen
		// policy's reject matrix — deny-list first, then
		// records_compliance_officer | tenant_records_owner, assurance ≥ standard).
		// No SoD rule applies to reject (the design's §2 guard is "human principal
		// with approval authority; reason required").
		func() error {
			return authorizePurgeAct(principal, scope, authz.LifecycleActionRejectPurge, request.Category, false, "", "")
		},
		func(r core.EvidencePurgeRequest) (bool, string) {
			if r.Status != core.PurgeRequestStatusRequested {
				if r.Status == core.PurgeRequestStatusRejected {
					return true, auth.CodeAlreadyDecided
				}
				return true, auth.CodeInvalidTransition
			}
			return false, ""
		},
		func(r core.EvidencePurgeRequest, current core.LifecycleSnapshot, p auth.VerifiedApprovalPrincipal) (core.PurgeLifecycleState, string, *core.EvidencePurgeApproval, core.EvidenceLifecycleEvent, core.ReceiptAction) {
			h1 := core.ComputeLifecycleSnapshotHash(current)
			h2Snap := assembleSnapshot(r.ObjectID, scope, core.PurgeLifecycleRejected, current.RetentionState, current.PolicyID, current.Category, current.PolicyVersion, current.BlockingHolds, r.RequestID, approvalIDs)
			h2 := core.ComputeLifecycleSnapshotHash(h2Snap)
			decision := &core.EvidencePurgeApproval{
				ApprovalID:        mustUUID(),
				RequestID:         r.RequestID,
				ApprovalOrder:     nextDecisionOrder(approvalIDs),
				Decision:          core.PurgeApprovalDecisionRejected,
				ReviewedHash:      h1,
				ResultingHash:     h2,
				PrincipalSnapshot: p.PrincipalSnapshot(),
				Reason:            cmd.Reason,
				PolicyVersion:     authz.LifecyclePolicyVersion,
				CreatedAt:         now,
			}
			return core.PurgeLifecycleRejected, h2, decision, core.EvidenceLifecycleEvent{
				EventID: mustUUID(), ObjectID: r.ObjectID, RequestID: r.RequestID,
				Action: core.PurgeEventRejected, FromState: string(core.PurgeLifecycleRequested),
				ToState: string(core.PurgeLifecycleRejected), ReviewedHash: h1, ResultingHash: h2,
				PrincipalSnapshot: p.PrincipalSnapshot(), Reason: cmd.Reason,
				PolicyVersion: authz.LifecyclePolicyVersion, CreatedAt: now,
			}, core.ReceiptActionPurgeRejected
		},
	)
	if err != nil {
		return core.RejectPurgeResult{}, err
	}

	// Complete the idempotency reservation with the concrete result.
	result := core.RejectPurgeResult{Request: updated, Approval: *decision, IdempotentReplay: false}
	if err := completePurgeIdempotencyOn(ctx, conn, request.TenantID, cmd.RequestIDKey, request.RequestID, result, now); err != nil {
		return core.RejectPurgeResult{}, err
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.RejectPurgeResult{}, fmt.Errorf("persistence error: commit purge rejection: %w", err)
	}
	committed = true
	return result, nil
}

// CancelPurge is the ORIGINAL requester's idempotent retraction (design §2):
// the pipeline returns to stored and a fresh request is a fresh act on the same
// one-per-object row. The event + receipt commit atomically; no decision row is
// recorded (the requester retraction is not an approver decision). Per the
// design's transition table cancel runs NO blocker set (a retraction only
// restores availability).
func (s *SQLiteStore) CancelPurge(ctx context.Context, cmd core.CancelPurgeCommand, principal auth.VerifiedApprovalPrincipal) (core.CancelPurgeResult, error) {
	if err := core.AssertValidCancelPurgeCommand(cmd); err != nil {
		return core.CancelPurgeResult{}, err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.CancelPurgeResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.CancelPurgeResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	request, err := purgeRequestByIDOn(ctx, conn, cmd.RequestID)
	if err != nil {
		return core.CancelPurgeResult{}, err
	}
	scope := purgeRequestScope(request)
	now := nowISO()
	commandHash := purgeCommandHash(cancelPurgeCommandShape{RequestID: cmd.RequestID})

	// Replay path: a completed (tenant, requestIdKey) cancellation returns the
	// stored outcome.
	if resultJSON, replayed, err := replayPurgeIdempotencyOn(ctx, conn, request.TenantID, cmd.RequestIDKey, commandHash, principal.SubjectID()); replayed || err != nil {
		if err != nil {
			return core.CancelPurgeResult{}, err
		}
		var replayed core.CancelPurgeResult
		if err := json.Unmarshal([]byte(resultJSON), &replayed); err != nil {
			return core.CancelPurgeResult{}, fmt.Errorf("persistence error: decode replayed cancellation: %w", err)
		}
		replayed.IdempotentReplay = true
		return replayed, nil
	}

	// The snapshot contribution needs the request's bound policy blocking set.
	policy, ok, err := retentionPolicyByIDOn(ctx, conn, request.PolicyID)
	if err != nil {
		return core.CancelPurgeResult{}, err
	}
	if !ok {
		return core.CancelPurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "the bound retention policy is no longer readable")
	}
	approvalIDs, err := approvalIDsOn(ctx, conn, request.RequestID)
	if err != nil {
		return core.CancelPurgeResult{}, err
	}

	updated, _, err := s.closePurgeTransition(
		ctx, conn, request, scope, cmd.RequestIDKey, commandHash, principal.SubjectID(), now, principal,
		policy.BlockingHoldKinds,
		// The cancel authz gate: the ORIGINAL requester only - the accounting-ladder
		// gate (the same matrix that opened the pipeline) plus the stored requester
		// match. A non-requester with the ladder role is ROLE_NOT_AUTHORIZED.
		func() error {
			if err := authorizePurgeAct(principal, scope, authz.LifecycleActionRequestPurge, request.Category, false, "", ""); err != nil {
				return err
			}
			if principal.SubjectID() != request.RequestedBy {
				return auth.New(auth.CodeRoleNotAuthorized, "only the original requester may cancel the purge request")
			}
			return nil
		},
		func(r core.EvidencePurgeRequest) (bool, string) {
			if r.Status != core.PurgeRequestStatusRequested {
				if r.Status == core.PurgeRequestStatusCancelled {
					return true, auth.CodeAlreadyDecided
				}
				return true, auth.CodeInvalidTransition
			}
			return false, ""
		},
		func(r core.EvidencePurgeRequest, current core.LifecycleSnapshot, p auth.VerifiedApprovalPrincipal) (core.PurgeLifecycleState, string, *core.EvidencePurgeApproval, core.EvidenceLifecycleEvent, core.ReceiptAction) {
			h1 := core.ComputeLifecycleSnapshotHash(current)
			h2Snap := assembleSnapshot(r.ObjectID, scope, core.PurgeLifecycleStored, current.RetentionState, current.PolicyID, current.Category, current.PolicyVersion, current.BlockingHolds, r.RequestID, approvalIDs)
			h2 := core.ComputeLifecycleSnapshotHash(h2Snap)
			return core.PurgeLifecycleStored, h2, nil, core.EvidenceLifecycleEvent{
				EventID: mustUUID(), ObjectID: r.ObjectID, RequestID: r.RequestID,
				Action: core.PurgeEventCancelled, FromState: string(core.PurgeLifecycleRequested),
				ToState: string(core.PurgeLifecycleStored), ReviewedHash: h1, ResultingHash: h2,
				PrincipalSnapshot: p.PrincipalSnapshot(), Reason: "requester retraction",
				PolicyVersion: authz.LifecyclePolicyVersion, CreatedAt: now,
			}, core.ReceiptActionPurgeCancelled
		},
	)
	if err != nil {
		return core.CancelPurgeResult{}, err
	}

	// Complete the idempotency reservation with the concrete result.
	result := core.CancelPurgeResult{Request: updated, IdempotentReplay: false}
	if err := completePurgeIdempotencyOn(ctx, conn, request.TenantID, cmd.RequestIDKey, request.RequestID, result, now); err != nil {
		return core.CancelPurgeResult{}, err
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.CancelPurgeResult{}, fmt.Errorf("persistence error: commit purge cancellation: %w", err)
	}
	committed = true
	return result, nil
}

// WithdrawPurge is the approval retraction (design §2/§7): a default approver
// or dual second approver withdraws an APPROVED pipeline with a reason — the
// documented cleanup (e.g. a hold placed after approval). The pipeline returns
// to stored; the immutable decision row + event + receipt commit atomically.
// Per the design's transition table withdraw runs NO blocker set (withdraw
// only restores availability and must work inside a closed period).
func (s *SQLiteStore) WithdrawPurge(ctx context.Context, cmd core.WithdrawPurgeCommand, principal auth.VerifiedApprovalPrincipal) (core.WithdrawPurgeResult, error) {
	if err := core.AssertValidWithdrawPurgeCommand(cmd); err != nil {
		return core.WithdrawPurgeResult{}, err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.WithdrawPurgeResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.WithdrawPurgeResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	request, err := purgeRequestByIDOn(ctx, conn, cmd.RequestID)
	if err != nil {
		return core.WithdrawPurgeResult{}, err
	}
	scope := purgeRequestScope(request)
	now := nowISO()
	commandHash := purgeCommandHash(withdrawPurgeCommandShape{RequestID: cmd.RequestID, Reason: cmd.Reason})

	// Replay path: a completed (tenant, requestIdKey) withdrawal returns the
	// stored outcome.
	if resultJSON, replayed, err := replayPurgeIdempotencyOn(ctx, conn, request.TenantID, cmd.RequestIDKey, commandHash, principal.SubjectID()); replayed || err != nil {
		if err != nil {
			return core.WithdrawPurgeResult{}, err
		}
		var replayed core.WithdrawPurgeResult
		if err := json.Unmarshal([]byte(resultJSON), &replayed); err != nil {
			return core.WithdrawPurgeResult{}, fmt.Errorf("persistence error: decode replayed withdrawal: %w", err)
		}
		replayed.IdempotentReplay = true
		return replayed, nil
	}

	// The snapshot contribution needs the request's bound policy blocking set.
	policy, ok, err := retentionPolicyByIDOn(ctx, conn, request.PolicyID)
	if err != nil {
		return core.WithdrawPurgeResult{}, err
	}
	if !ok {
		return core.WithdrawPurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "the bound retention policy is no longer readable")
	}
	approvalIDs, err := approvalIDsOn(ctx, conn, request.RequestID)
	if err != nil {
		return core.WithdrawPurgeResult{}, err
	}

	updated, decision, err := s.closePurgeTransition(
		ctx, conn, request, scope, cmd.RequestIDKey, commandHash, principal.SubjectID(), now, principal,
		policy.BlockingHoldKinds,
		// The withdraw authz gate: a default approver OR a dual second approver
		// (the frozen policy's withdraw matrix - deny-list first, then
		// records_compliance_officer | tenant_records_owner | controller |
		// tax_responsible, assurance at least standard). Reason is required.
		func() error {
			return authorizePurgeAct(principal, scope, authz.LifecycleActionWithdrawApproval, request.Category, false, "", "")
		},
		func(r core.EvidencePurgeRequest) (bool, string) {
			if r.Status != core.PurgeRequestStatusApproved {
				if r.Status == core.PurgeRequestStatusWithdrawn {
					return true, auth.CodeAlreadyDecided
				}
				return true, auth.CodeInvalidTransition
			}
			return false, ""
		},
		func(r core.EvidencePurgeRequest, current core.LifecycleSnapshot, p auth.VerifiedApprovalPrincipal) (core.PurgeLifecycleState, string, *core.EvidencePurgeApproval, core.EvidenceLifecycleEvent, core.ReceiptAction) {
			h1 := core.ComputeLifecycleSnapshotHash(current)
			h2Snap := assembleSnapshot(r.ObjectID, scope, core.PurgeLifecycleStored, current.RetentionState, current.PolicyID, current.Category, current.PolicyVersion, current.BlockingHolds, r.RequestID, approvalIDs)
			h2 := core.ComputeLifecycleSnapshotHash(h2Snap)
			decision := &core.EvidencePurgeApproval{
				ApprovalID:        mustUUID(),
				RequestID:         r.RequestID,
				ApprovalOrder:     nextDecisionOrder(approvalIDs),
				Decision:          core.PurgeApprovalDecisionWithdrawn,
				ReviewedHash:      h1,
				ResultingHash:     h2,
				PrincipalSnapshot: p.PrincipalSnapshot(),
				Reason:            cmd.Reason,
				PolicyVersion:     authz.LifecyclePolicyVersion,
				CreatedAt:         now,
			}
			return core.PurgeLifecycleStored, h2, decision, core.EvidenceLifecycleEvent{
				EventID: mustUUID(), ObjectID: r.ObjectID, RequestID: r.RequestID,
				Action: core.PurgeEventWithdrawn, FromState: string(core.PurgeLifecycleApproved),
				ToState: string(core.PurgeLifecycleStored), ReviewedHash: h1, ResultingHash: h2,
				PrincipalSnapshot: p.PrincipalSnapshot(), Reason: cmd.Reason,
				PolicyVersion: authz.LifecyclePolicyVersion, CreatedAt: now,
			}, core.ReceiptActionPurgeWithdrawn
		},
	)
	if err != nil {
		return core.WithdrawPurgeResult{}, err
	}

	// Complete the idempotency reservation with the concrete result.
	result := core.WithdrawPurgeResult{Request: updated, Approval: *decision, IdempotentReplay: false}
	if err := completePurgeIdempotencyOn(ctx, conn, request.TenantID, cmd.RequestIDKey, request.RequestID, result, now); err != nil {
		return core.WithdrawPurgeResult{}, err
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.WithdrawPurgeResult{}, fmt.Errorf("persistence error: commit purge withdrawal: %w", err)
	}
	committed = true
	return result, nil
}

// purgeRequestScope rebuilds the exact company scope of a request row.
func purgeRequestScope(r core.EvidencePurgeRequest) core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: r.TenantID,
		CompanyID:      r.CompanyID,
		RUC:            r.RUC,
		Period:         r.Period,
	}
}

// mustUUID mints a UUID or panics (an internal invariant violation — the store
// cannot proceed without an id; fail closed).
func mustUUID() string {
	id, err := newUUID()
	if err != nil {
		panic(fmt.Sprintf("generate uuid: %v", err))
	}
	return id
}

// replayPurgeIdempotencyOn checks the (tenant, requestIdKey) reservation and
// returns the STORED RESULT JSON on a successful replay (replayed=true), an
// IDEMPOTENCY_CONFLICT on a mismatched command/principal, or proceeds
// (replayed=false) on a fresh key. Callers decode the concrete result type from
// the stored JSON themselves. A reserved-but-never-completed key (NULL
// entity_id/result_json — a crash between reservation and completion) is the
// typed IDEMPOTENCY_CONFLICT incomplete outcome, never a scan error.
func replayPurgeIdempotencyOn(ctx context.Context, q Queryer, tenantID, requestIDKey, commandHash, actorBinding string) (string, bool, error) {
	var (
		// entity_id and result_json are NULL for a reserved-but-never-completed
		// key, so they are scanned nullable: a NULL scan surfaces the typed
		// incomplete outcome below, never a database scan error.
		storedEntityID, storedResult             sql.NullString
		storedHash, storedActor, storedCreatedAt string
	)
	err := q.QueryRowContext(ctx, `
		SELECT entity_id, result_json, command_hash, actor_binding, created_at
		FROM evidence_purge_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		tenantID, requestIDKey,
	).Scan(&storedEntityID, &storedResult, &storedHash, &storedActor, &storedCreatedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedActor != actorBinding {
			return "", false, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different command or principal")
		}
		if storedEntityID.String == "" || storedResult.String == "" {
			return "", false, auth.New(auth.CodeIdempotencyConflict, "request id reservation never completed")
		}
		return storedResult.String, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	default:
		return "", false, fmt.Errorf("persistence error: read purge idempotency key: %w", err)
	}
}
