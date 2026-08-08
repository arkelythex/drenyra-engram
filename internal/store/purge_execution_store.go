// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the PHYSICAL PURGE EXECUTION layer of the v0.8
// evidence lifecycle (batch 4 — docs/architecture/evidence-lifecycle-v0.8.md
// §2/§3.7/§11; schema v12):
//
//   - the v11→v12 migration is ONE fail-closed transaction that (a) aborts when
//     evidence_purge_executions already exists (corruption signal), (b) creates
//     the immutable evidence_purge_executions attempt ledger (§3.7: the exact
//     rel_path, the recorded size, the pre-removal hash the bytes MUST re-hash
//     to, and the guarded state machine intent → completed | interrupted — a
//     terminal attempt never re-opens), (c) REBUILDS the receipts table with the
//     action CHECK extended by the v0.8 execution-intent act (purge_intent —
//     SQLite cannot alter a CHECK, so the table is copied and swapped inside the
//     transaction, byte-preserving every v11 row) and (d) REBUILDS the receipts
//     singleton index uq_receipts_singleton with purge_intent excluded (retries
//     legitimately emit ONE purge_intent receipt per execution attempt, while
//     the exact-duplicate backstop stays the UNIQUE(subject_type, subject_id,
//     action, payload_hash) table constraint). schema_version flips to 12 only
//     after every step succeeded;
//
//   - ExecutePurge runs the design's TWO-PHASE, RECEIPT-COVERED execution
//     protocol (§11) for an APPROVED pipeline. Phase 1 (durable intent, ONE
//     BEGIN IMMEDIATE transaction): the request is re-read UNDER SCOPE, the
//     (tenant, executionId) idempotency key is reserved/replayed, the state gate
//     (only 'approved' executes) and the FULL non-overridable blocker set are
//     re-run BEFORE authz — closed-period gate, retention re-resolution against
//     the bound policy, eligibility (unknown/not_due fail closed), the active
//     blocking hold scan INCLUDING holds placed after approval (HOLD_ACTIVE) and
//     the lifecycle snapshot/version check (LIFECYCLE_VERSION_MISMATCH) — then
//     the authenticated execute gate (default approver | controller |
//     tax_responsible, deny-list first); a stale 'intent' attempt of the same
//     request is marked 'interrupted' (terminal, guarded) and a FRESH execution
//     row is inserted with the purge_intent event + receipt. COMMIT — at this
//     point NOTHING has been deleted and the object is still verifiable.
//     Phase 2 (byte removal, OUTSIDE SQL): the object layer's
//     removeObjectBytes reads the exact content-addressed path, re-hashes the
//     bytes and requires the digest to equal the immutable object id and the
//     recorded size; a mismatch ABORTS — bytes are never unlinked, the attempt
//     stays 'intent' and is surfaced as interrupted. Phase 3 (durable
//     completion, ONE BEGIN IMMEDIATE transaction): the request is RE-READ under
//     scope (it must STILL be 'approved' — a withdraw racing the
//     intent→completion window aborts the completion and the interrupted intent
//     stays surfaced), the executions row flips to 'completed' with the
//     completion receipt id, the purge_executed event + receipt commit, the
//     projection flips to 'purged' (terminal) and the request to 'executed'.
//
//   - retry/idempotency is safe by EXECUTION ID: replaying the SAME completed
//     (tenant, executionId) returns the stored outcome with NO new intent/
//     removal/completion; a reservation that never completed (an interrupted
//     attempt) is REPORTED with the frozen PURGE_EXECUTION_INTERRUPTED code —
//     the engine never pretends completion; a retry runs a FRESH execution row
//     under the same request (design §3.7: "interrupted is terminal for that
//     attempt; a retry creates a NEW execution row under the same request").
//
//   - only object BYTES are removed at successful execution; the immutable
//     evidence metadata, hash, link, event, approval and receipt rows never
//     change. The doctor surface REPORTS 'intent'/'interrupted' execution
//     rows and the documented purge absences as auditable findings (design
//     §13.3, WU-1 — see doctorPurgeExecutionsScan); DEFERRED (documented,
//     never implemented here): deterministic export (§12), the remaining
//     verification layers (§13.1) and the HTTP/MCP/CLI surfaces (§14).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// purgeExecutionErrInterrupted is the frozen interrupted-execution code: an
// execution attempt whose intent committed but whose completion never did. It
// is REPORTED, never pretended completed (design §11 failure semantics).
const purgeExecutionErrInterrupted = "PURGE_EXECUTION_INTERRUPTED"

// ──────────────────────────────────────────────
// v12 schema — the executions attempt ledger + receipts (design §3.7/§4/§11)
// ──────────────────────────────────────────────

// evidencePurgeExecutionsDDL is the IMMUTABLE execution-attempt ledger (§3.7):
// the exact rel_path, the recorded size, the pre-removal hash (the content
// address the bytes MUST re-hash to immediately before the unlink) and the
// BOUND intent_reviewed_hash (the non-empty canonical lifecycle snapshot hash
// H1 the executor examined under the full blocker set — the frozen
// authorization the crash recovery validator re-validates the intent
// against), plus the guarded attempt state machine (intent → completed |
// interrupted; a terminal attempt never re-opens — a retry runs a FRESH
// execution row under the same request).
const evidencePurgeExecutionsDDL = `
                    CREATE TABLE evidence_purge_executions (
                      execution_id TEXT PRIMARY KEY,
                      request_id TEXT NOT NULL REFERENCES evidence_purge_requests(id),
                      object_id TEXT NOT NULL REFERENCES evidence_objects(id),
                      rel_path TEXT NOT NULL,
                      size INTEGER NOT NULL CHECK(size >= 0),
                      pre_removal_hash TEXT NOT NULL,
                      intent_reviewed_hash TEXT NOT NULL CHECK(intent_reviewed_hash <> ''),
                      state TEXT NOT NULL CHECK(state IN ('intent','completed','interrupted')),
                      intent_at TEXT NOT NULL,
                      intent_by TEXT NOT NULL,
                      completed_at TEXT,
                      completed_by TEXT,
                      completion_receipt_id TEXT
                    );
                    `

const evidencePurgeExecutionsRequestIndexDDL = `CREATE INDEX idx_evidence_purge_executions_request
                    ON evidence_purge_executions(request_id, state);`

// evidencePurgeExecutionsGuardedDDL freezes the execution attempt machine at the
// schema level (defense in depth — the store API is the only writer): the ONLY
// legal UPDATE is the guarded intent → completed/interrupted transition that
// leaves every recorded evidence column byte-identical; interrupted carries NO
// completion columns and completed REQUIRES all three completion columns (the
// completion is receipt-covered — completion_receipt_id is part of the
// transition). The NULL-equality conjuncts are deliberate: SQL NULL never
// defeats the guard because a FALSE conjunct always aborts, and the NULL case
// arises only when a NULL column is unchanged (the legal transition shape).
const evidencePurgeExecutionsGuardedDDL = `
                    CREATE TRIGGER evidence_purge_executions_guarded BEFORE UPDATE ON evidence_purge_executions
                    BEGIN
                      SELECT RAISE(ABORT,'IMMUTABLE_PURGE_EXECUTION: an execution record only advances intent → completed/interrupted through the guarded store API')
                      WHERE NOT (
                        OLD.state = 'intent'
                        AND NEW.state IN ('completed','interrupted')
                        AND NEW.execution_id = OLD.execution_id
                        AND NEW.request_id = OLD.request_id
                        AND NEW.object_id = OLD.object_id
                        AND NEW.rel_path = OLD.rel_path
                        AND NEW.size = OLD.size
                        AND NEW.pre_removal_hash = OLD.pre_removal_hash
                        AND NEW.intent_reviewed_hash = OLD.intent_reviewed_hash
                        AND NEW.intent_at = OLD.intent_at
                        AND NEW.intent_by = OLD.intent_by
                        AND (
                          (NEW.state = 'interrupted' AND NEW.completed_at IS NULL AND NEW.completed_by IS NULL AND NEW.completion_receipt_id IS NULL)
                          OR
                          (NEW.state = 'completed' AND NEW.completed_at IS NOT NULL AND NEW.completed_by IS NOT NULL AND NEW.completion_receipt_id IS NOT NULL)
                        )
                      );
                    END;
                    `

const evidencePurgeExecutionsNoDeleteDDL = `
                    CREATE TRIGGER evidence_purge_executions_no_delete BEFORE DELETE ON evidence_purge_executions
                    BEGIN
                      SELECT RAISE(ABORT,'IMMUTABLE_PURGE_EXECUTION: deletion is forbidden; execution records are permanent'); END;
                    `

// receiptsV12DDL is the v12 receipts table: the v11 layout verbatim (every CHECK,
// FK, exactly-one-typed-FK constraint and the unique
// (subject_type, subject_id, action, payload_hash)) with ONLY the action CHECK
// extended by the v0.8 execution-intent act (purge_intent — design §11 step 1:
// every transition event, INCLUDING the intent, is receipt-covered so an
// interrupted execution is auditable).
const receiptsV12DDL = `
                    CREATE TABLE receipts_v12 (
                      id INTEGER PRIMARY KEY AUTOINCREMENT,
                      subject_type TEXT NOT NULL CHECK(subject_type IN ('memory','judgment','reconciliation','evidence_object')),
                      subject_id TEXT NOT NULL,
                      action TEXT NOT NULL CHECK(action IN
                        ('memory_recorded','memory_approved','memory_rejected','memory_voided',
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

// receiptsV12CopyDDL copies every v11 receipt row byte-preserved into the staging
// table (explicit column order — the typed FK columns copy verbatim).
const receiptsV12CopyDDL = `
                    INSERT INTO receipts_v12 (id, subject_type, subject_id, action, tenant_id, company_id,
                      fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
                      policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
                      memory_id, judgment_id, reconciliation_id, evidence_object_id)
                    SELECT id, subject_type, subject_id, action, tenant_id, company_id,
                      fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
                      policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
                      memory_id, judgment_id, reconciliation_id, evidence_object_id FROM receipts;
                    `

// receiptsSingletonIndexV12DDL is the v12 rebuild of uq_receipts_singleton: the
// v11 semantics PLUS the purge_intent exclusion — execution retries legitimately
// emit ONE purge_intent receipt per execution attempt, so the append-only-growth
// exemption covers it; the exact-duplicate backstop stays the
// UNIQUE(subject_type, subject_id, action, payload_hash) table constraint.
const receiptsSingletonIndexV12DDL = `CREATE UNIQUE INDEX uq_receipts_singleton
                    ON receipts(subject_type, subject_id, action)
                    WHERE action NOT IN ('evidence_linked','hold_placed','hold_lifted',
                      'retention_bound','purge_requested','purge_approved','purge_rejected',
                      'purge_cancelled','purge_withdrawn','purge_intent','purge_executed');`

// migrateV11ToV12 upgrades a schema_version=11 store to v12 IN ONE fail-closed
// transaction (design §3.7/§4/§11, execution batch):
//
//	(a) validate that evidence_purge_executions does not exist (a pre-existing
//	    executions table is a corruption signal; abort);
//	(b) create evidence_purge_executions with its request index and its two
//	    triggers (the guarded intent → completed/interrupted machine and the
//	    no-delete guard);
//	(c) rebuild receipts under the staging name receipts_v12: the v11 layout
//	    verbatim with ONLY the action CHECK extended by purge_intent; copy every
//	    row byte-preserved, swap the table into place, recreate the v12 singleton
//	    index (purge_intent excluded — retries grow per attempt), the subject/key
//	    indexes and the no-update/no-delete triggers;
//	(d) UPDATE schema_meta SET value = '12' ONLY after every step above
//	    succeeded; any failure rolls the whole migration back and leaves
//	    schema_version=11.
//
// No existing row is backfilled or re-hashed. Fresh schema DDL (applySchema +
// the migration chain in Open) produces the same tables/triggers/indexes.
func migrateV11ToV12(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate v11→v12: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (a) Fail closed on a pre-existing executions table: the chain is additive
	// and never replays — a pre-existing table is a corruption signal.
	var existing string
	err = tx.QueryRowContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name = 'evidence_purge_executions'
		LIMIT 1`).Scan(&existing)
	switch {
	case err == nil:
		return fmt.Errorf("migrate v11→v12: pre-existing table %q — corruption signal, abort (additive migrations never replay)", existing)
	case errors.Is(err, sql.ErrNoRows):
		// clean — proceed
	default:
		return fmt.Errorf("migrate v11→v12: inspect existing tables: %w", err)
	}

	// (b) The executions ledger + its guards.
	for _, ddl := range []string{
		evidencePurgeExecutionsDDL, evidencePurgeExecutionsRequestIndexDDL,
		evidencePurgeExecutionsGuardedDDL, evidencePurgeExecutionsNoDeleteDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v11→v12: create executions table: %w", err)
		}
	}

	// (c) The receipts table rebuild (extended action CHECK, layout verbatim).
	if _, err := tx.ExecContext(ctx, receiptsV12DDL); err != nil {
		return fmt.Errorf("migrate v11→v12: create receipts_v12: %w", err)
	}
	if _, err := tx.ExecContext(ctx, receiptsV12CopyDDL); err != nil {
		return fmt.Errorf("migrate v11→v12: copy receipts rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dropReceiptsDDL); err != nil {
		return fmt.Errorf("migrate v11→v12: swap out v11 receipts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE receipts_v12 RENAME TO receipts`); err != nil {
		return fmt.Errorf("migrate v11→v12: rename receipts_v12: %w", err)
	}
	for _, ddl := range []string{
		receiptsSingletonIndexV12DDL, receiptsSubjectTimeIndexDDL, receiptsKeyTimeIndexDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v11→v12: create receipts index: %w", err)
		}
	}
	for _, ddl := range []string{receiptsNoUpdateDDL, receiptsNoDeleteDDL} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v11→v12: create receipts trigger: %w", err)
		}
	}

	// (d) schema_version = 12 ONLY after the whole migration succeeded — same
	// transaction, so a failure above rolls everything back.
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = '12' WHERE key = 'schema_version'`); err != nil {
		return fmt.Errorf("migrate v11→v12: set schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate v11→v12: commit: %w", err)
	}
	committed = true
	return nil
}

// ──────────────────────────────────────────────
// Execution store operations (batch 4)
// ──────────────────────────────────────────────

// purgeExecutionColumns is the fixed column projection of one executions row.
const purgeExecutionColumns = `execution_id, request_id, object_id, rel_path, size,
	pre_removal_hash, intent_reviewed_hash, state, intent_at, intent_by,
	COALESCE(completed_at, ''), COALESCE(completed_by, ''), COALESCE(completion_receipt_id, '')`

// scanPurgeExecution scans one evidence_purge_executions row into the core model.
func scanPurgeExecution(sc interface{ Scan(dest ...any) error }) (core.EvidencePurgeExecution, error) {
	var (
		e    core.EvidencePurgeExecution
		size int64
	)
	if err := sc.Scan(
		&e.ExecutionID, &e.RequestID, &e.ObjectID, &e.RelPath, &size,
		&e.PreRemovalHash, &e.IntentReviewedHash, &e.State, &e.IntentAt, &e.IntentBy,
		&e.CompletedAt, &e.CompletedBy, &e.CompletionReceiptID,
	); err != nil {
		return core.EvidencePurgeExecution{}, err
	}
	e.Size = size
	return e, nil
}

// insertPurgeExecutionOn appends ONE immutable execution-attempt row in state
// 'intent' (the completion columns stay NULL — the guarded trigger permits only
// the single intent → completed/interrupted transition).
func insertPurgeExecutionOn(ctx context.Context, q Queryer, e core.EvidencePurgeExecution) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO evidence_purge_executions (execution_id, request_id, object_id, rel_path, size,
			pre_removal_hash, intent_reviewed_hash, state, intent_at, intent_by, completed_at, completed_by, completion_receipt_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL)`,
		e.ExecutionID, e.RequestID, e.ObjectID, e.RelPath, e.Size, e.PreRemovalHash, e.IntentReviewedHash,
		string(e.State), e.IntentAt, e.IntentBy,
	); err != nil {
		return fmt.Errorf("persistence error: insert purge execution: %w", err)
	}
	return nil
}

// markInterruptedPurgeExecutionsOn is the retry path of design §3.7: a stale
// 'intent' attempt of the same request is TERMINAL for that attempt — it is
// marked 'interrupted' (guarded transition) and the retry runs a FRESH execution
// row under the new execution id.
func markInterruptedPurgeExecutionsOn(ctx context.Context, q Queryer, requestID, excludeExecutionID string) error {
	if _, err := q.ExecContext(ctx, `
		UPDATE evidence_purge_executions SET state = 'interrupted'
		WHERE request_id = ? AND state = 'intent' AND execution_id != ?`,
		requestID, excludeExecutionID,
	); err != nil {
		return fmt.Errorf("persistence error: mark interrupted purge execution: %w", err)
	}
	return nil
}

// executePurgeCommandShape is the canonical idempotency fingerprint of the
// execute command (the executionId/key is the key itself, never hashed).
type executePurgeCommandShape struct {
	RequestID             string `json:"requestId"`
	ExpectedLifecycleHash string `json:"expectedLifecycleHash"`
	Reason                string `json:"reason"`
}

// ──────────────────────────────────────────────
// ExecutePurge
// ──────────────────────────────────────────────

// ExecutePurge runs the RECEIPT-COVERED execution protocol of an APPROVED
// purge pipeline (batch 4, design §2/§3.7/§9/§11 — audit-hardened):
//
//   - PHASE 1 (durable intent, ONE BEGIN IMMEDIATE transaction): the request
//     is re-read UNDER SCOPE, the (tenant, executionId) idempotency key is
//     reserved/replayed, the state gate (only 'approved' — prior human
//     approval present and not withdrawn) and the FULL non-overridable
//     blocker set are re-run BEFORE authz — closed-period gate, retention
//     re-resolution against the bound policy, eligibility (unknown/not_due
//     fail closed), the active blocking hold scan INCLUDING holds placed
//     after approval (HOLD_ACTIVE) and the lifecycle snapshot/version check
//     (LIFECYCLE_VERSION_MISMATCH) — then the authenticated execute gate
//     (default approver | controller | tax_responsible, deny-list first);
//     the executions row in state 'intent' (with its BOUND immutable
//     authorization: the pre-removal hash, size and the reviewed lifecycle
//     snapshot hash H1) + the purge_intent event + receipt commit — at this
//     point NOTHING has been deleted and the object is still verifiable;
//
//   - PHASE 2 (the EXECUTION transaction, ONE BEGIN IMMEDIATE transaction):
//     the request/object are RE-READ under scope and the FULL blocker set is
//     RE-RUN (closed-period gate → retention re-resolution → eligibility →
//     active blocking hold scan INCLUDING holds committed after the intent →
//     lifecycle snapshot/version) while the exclusion lock is held; the
//     guarded byte unlink runs UNDER THAT SAME LOCK and the completion
//     (purge_executed event + receipt, executions → 'completed', projection
//     → 'purged' terminal, request → 'executed') persists BEFORE the commit.
//     A hold can NEVER commit between the final revalidation and the unlink
//     (the BEGIN IMMEDIATE lock excludes every other writer); a hold placed
//     after the intent blocks the unlink — the attempt stays 'intent' and is
//     surfaced interrupted, never hidden;
//
//   - CRASH CONVERGENCE: a crash after the durable intent (with or without
//     the unlink) leaves the execution row 'intent' and the idempotency key
//     incomplete. A retry of the SAME execution id converges idempotently:
//     bytes present → the execution transaction re-runs (revalidation +
//     unlink + completion); bytes missing → the durable intent plus its bound
//     immutable authorization (pre-removal hash/size/reviewed snapshot) prove
//     the EXACT authorized-absence case and converge ONCE to 'purged' with
//     exactly ONE purge_executed receipt — no second unlink, no generic
//     corruption classification. A FRESH recovery id NEVER creates a
//     duplicate completion/event: it converges the PRIOR intent row (the
//     fresh id becomes the replay key of that convergence). If the prior
//     intent/authorization is absent or invalid, missing bytes remains the
//     OBJECT_BYTES_MISSING integrity incident.
//
//   - retry/idempotency is safe by EXECUTION ID: replaying the SAME completed
//     (tenant, executionId) returns the stored outcome with NO new
//     intent/removal/completion; a same-id retry of an interrupted attempt
//     converges (above); a stale 'intent' attempt of the same request with
//     its bytes PRESENT is marked 'interrupted' (terminal, guarded) and a
//     retry runs a FRESH execution row under the same request (design §3.7).
//
// Only object BYTES are removed at successful execution; the immutable
// evidence metadata, hash, link, event, approval and receipt rows never
// change. The object layer re-hashes the bytes and requires
// pre_removal_hash == objectId BEFORE the unlink — a mismatch ABORTS and
// nothing is unlinked.
func (s *SQLiteStore) ExecutePurge(ctx context.Context, cmd core.ExecutePurgeCommand, principal auth.VerifiedApprovalPrincipal) (core.ExecutePurgeResult, error) {
	if err := core.AssertValidExecutePurgeCommand(cmd); err != nil {
		return core.ExecutePurgeResult{}, err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	now := nowISO()
	commandHash := purgeCommandHash(executePurgeCommandShape{
		RequestID: cmd.RequestID, ExpectedLifecycleHash: cmd.ExpectedLifecycleHash, Reason: cmd.Reason,
	})
	actorBinding := principal.SubjectID()

	// ── TRANSACTION 1: durable intent (design §11 step 1) ──
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	// 1. Locked request re-read UNDER SCOPE: the request row is the scope
	// authority (the exact tenant/company/RUC/period tuple the pipeline was
	// opened under — never a caller-declared scope).
	request, err := purgeRequestByIDOn(ctx, conn, cmd.RequestID)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	scope := purgeRequestScope(request)

	// 2. Tenant-scoped idempotency for THIS execution attempt (the (tenant,
	// executionId) key). A COMPLETED attempt replays its stored outcome with NO
	// new intent/removal/completion; an INCOMPLETE reservation is an INTERRUPTED
	// attempt and is REPORTED (the engine never pretends completion); a fresh key
	// proceeds. Replaying a completed id with a different command or principal is
	// IDEMPOTENCY_CONFLICT.
	var (
		// entity_id and result_json are NULL for a reserved-but-never-completed
		// attempt (a crash between intent and completion), so they are scanned
		// nullable: a NULL scan must surface PURGE_EXECUTION_INTERRUPTED, never a
		// persistence error.
		storedEntityID, storedResult, storedHash, storedActor sql.NullString
	)
	err = conn.QueryRowContext(ctx, `
		SELECT entity_id, result_json, command_hash, actor_binding
		FROM evidence_purge_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		scope.OrganizationID, cmd.ExecutionID,
	).Scan(&storedEntityID, &storedResult, &storedHash, &storedActor)
	switch {
	case err == nil:
		if storedHash.String != commandHash || storedActor.String != actorBinding {
			return core.ExecutePurgeResult{}, auth.New(auth.CodeIdempotencyConflict, "execution id already used with a different command or principal")
		}
		if storedEntityID.String == "" || storedResult.String == "" {
			// The reservation never completed: the prior intent committed (the
			// executions row exists in state 'intent') but the completion never did
			// (a crash between intent and completion, or a failed byte removal). A
			// SAME-ID retry CONVERGES under the durable intent's bound immutable
			// authorization: bytes present → the execution transaction re-runs
			// (fresh revalidation + unlink + completion); bytes missing → the exact
			// authorized-absence case converges ONCE to 'purged' with exactly ONE
			// purge_executed receipt — no second unlink, never pretended as generic
			// corruption. The recovery completes the SAME idempotency key and runs
			// INSIDE this transaction; the caller commits it below.
			recovered, rerr := s.recoverSameExecutionOn(ctx, conn, cmd, request, scope, principal, now, actorBinding)
			if rerr != nil {
				return core.ExecutePurgeResult{}, rerr
			}
			if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
				return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: commit purge execution recovery: %w", err)
			}
			committed = true
			return recovered, nil
		}
		var replayed core.ExecutePurgeResult
		if err := json.Unmarshal([]byte(storedResult.String), &replayed); err != nil {
			return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: decode replayed execution: %w", err)
		}
		// The stored outcome was written by the ORIGINAL attempt (IdempotentReplay
		// false); THIS call is the replay, so the returned result must carry the
		// idempotent-replay flag — the caller can never mistake a replayed stored
		// outcome for a fresh execution. Nothing is re-persisted: the stored bytes
		// stay the immutable original outcome.
		replayed.IdempotentReplay = true
		return replayed, nil
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx, `
	INSERT INTO evidence_purge_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, entity_id, result_json, created_at, completed_at)
	VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			scope.OrganizationID, cmd.ExecutionID, commandHash, actorBinding, now,
		); err != nil {
			return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: reserve execution idempotency key: %w", err)
		}
		// proceed — no prior execution attempt for this execution id
	default:
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: read execution idempotency key: %w", err)
	}

	// 3. State gate: ONLY an 'approved' pipeline executes (prior human approval
	// present and not withdrawn — design §2 execute row). An executed request is
	// ALREADY_DECIDED; every other status is an INVALID_TRANSITION.
	switch request.Status {
	case core.PurgeRequestStatusApproved:
		// proceed
	case core.PurgeRequestStatusExecuted:
		return core.ExecutePurgeResult{}, auth.New(auth.CodeAlreadyDecided, "this purge request has already been executed")
	default:
		return core.ExecutePurgeResult{}, auth.New(auth.CodeInvalidTransition,
			fmt.Sprintf("cannot execute a purge request in status %s", request.Status))
	}

	// 4. Locked object re-read: the byte facts (the immutable content address,
	// the exact rel_path and the recorded size) come from the evidence_objects
	// row, never from the caller.
	object, ok := s.evidenceObjectByIDOn(ctx, conn, request.ObjectID)
	if !ok {
		return core.ExecutePurgeResult{}, fmt.Errorf("%s: %s", objectErrNotFound, request.ObjectID)
	}

	// 5. BLOCKERS, before authz (design §2 execute row — the FULL non-overridable
	// set re-run immediately before deletion; no override field exists): closed-
	// period gate → retention re-resolution against the bound policy's resolution
	// evidence → eligibility → active blocking hold scan (INCLUDING holds placed
	// after approval) → lifecycle snapshot/version.
	if err := s.assertPeriodWritable(ctx, conn, scope, "execute purge"); err != nil {
		return core.ExecutePurgeResult{}, err
	}
	bound, ok, err := retentionPolicyByIDOn(ctx, conn, request.PolicyID)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	if !ok {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "the bound retention policy is no longer readable")
	}
	resolved, matched, err := resolveRetentionPolicyOn(ctx, conn, scope, bound.Jurisdiction, bound.Legislation, request.Category)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	if !matched {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "no exact active retention policy resolves at execution time (the engine never guesses)")
	}
	eligibility := core.EvaluateRetentionEligibility(resolved, scope.Period)
	switch eligibility {
	case core.RetentionEligibilityEligible:
		// proceed
	case core.RetentionEligibilityNotDue:
		return core.ExecutePurgeResult{}, auth.New(auth.CodeRetentionNotDue, "the object's period no longer reaches the resolved policy's min_period floor")
	default:
		return core.ExecutePurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "retention state could not be evaluated at execution time (never guessed)")
	}

	current, currentHash, err := currentPurgeSnapshotOn(ctx, conn, request.ObjectID, scope, resolved.BlockingHoldKinds)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	// The active blocking holds are INSIDE the snapshot; the explicit blocker
	// check still runs first so the frozen HOLD_ACTIVE code wins (a hold placed
	// AFTER the approval ALWAYS re-blocks execution — design §2/§7/§13.1).
	if len(current.BlockingHolds) > 0 {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeHoldActive, "an active blocking hold protects this object — the execution is blocked (holds placed after approval always re-block)")
	}
	if currentHash != cmd.ExpectedLifecycleHash {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeLifecycleVersionMismatch,
			fmt.Sprintf("expectedLifecycleHash %s does not match the current lifecycle snapshot hash %s — re-read and retry", cmd.ExpectedLifecycleHash, currentHash))
	}

	// 6. AUTHZ (after the blockers — a blocked execution never reaches the
	// policy): the execute gate is the design's executor matrix (deny-list first,
	// then records_compliance_officer | tenant_records_owner | controller |
	// tax_responsible, assurance ≥ standard — §11 executor). A deployment-
	// configured scheduler invokes the SAME guarded operation with its own
	// verified principal; it is never an approver.
	if err := authorizePurgeAct(principal, scope, authz.LifecycleActionExecutePurge, resolved.Category, false, "", ""); err != nil {
		return core.ExecutePurgeResult{}, err
	}

	// 7. Recovery check for a FRESH id: a stale 'intent' attempt of the SAME
	// request whose bytes are ALREADY missing (a crash after the authorized
	// unlink) converges the PRIOR intent under its bound immutable
	// authorization — the fresh id NEVER creates a duplicate completion/event:
	// the prior executions row completes with exactly ONE purge_executed
	// event/receipt and the fresh id becomes the replay key of that convergence
	// (its stored outcome carries the PRIOR execution row). An invalid prior
	// intent leaves the missing bytes as the OBJECT_BYTES_MISSING integrity
	// incident. When the stale attempt's bytes are PRESENT it is TERMINAL for
	// that attempt (design §3.7): marked 'interrupted' (guarded transition) and
	// a FRESH execution row runs under this new execution id.
	staleExec, staleExists, err := purgeIntentExecutionForRequestOn(ctx, conn, request.RequestID, cmd.ExecutionID)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	if staleExists {
		present, perr := s.objectBytesExist(object.RelPath)
		if perr != nil {
			return core.ExecutePurgeResult{}, perr
		}
		if !present {
			if verr := validatePurgeIntentOn(ctx, conn, staleExec, request, object); verr != nil {
				return core.ExecutePurgeResult{}, fmt.Errorf("%s: %s (the prior purge intent %s is invalid — the missing bytes remain an integrity incident)", objectErrBytesMissing, request.ObjectID, staleExec.ExecutionID)
			}
			converged, cverr := s.convergePurgeOn(ctx, conn, cmd, request, scope, staleExec, principal, now, actorBinding)
			if cverr != nil {
				return core.ExecutePurgeResult{}, cverr
			}
			if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
				return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: commit purge execution convergence: %w", err)
			}
			committed = true
			return converged, nil
		}
		if err := markInterruptedPurgeExecutionsOn(ctx, conn, request.RequestID, cmd.ExecutionID); err != nil {
			return core.ExecutePurgeResult{}, err
		}
	}

	// 8. The durable intent: the executions row (state 'intent' — the exact
	// rel_path, the recorded size, the pre-removal hash the bytes MUST
	// re-hash to immediately before the unlink AND the bound immutable
	// authorization: the reviewed lifecycle snapshot hash H1 the executor
	// examined under the full blocker set), the purge_intent event and the
	// purge_intent receipt. H1/H2 are BOTH the reviewed approval hash — the
	// intent changes NO canonical snapshot field (the projection flips only in
	// completion); the event and the receipt record the EXACT state the executor
	// examined. At this point NOTHING has been deleted and the object is still
	// verifiable.
	preRemovalHash := object.ObjectID
	if err := insertPurgeExecutionOn(ctx, conn, core.EvidencePurgeExecution{
		ExecutionID:        cmd.ExecutionID,
		RequestID:          request.RequestID,
		ObjectID:           request.ObjectID,
		RelPath:            object.RelPath,
		Size:               object.Size,
		PreRemovalHash:     preRemovalHash,
		IntentReviewedHash: currentHash,
		State:              core.PurgeExecutionIntent,
		IntentAt:           now,
		IntentBy:           actorBinding,
	}); err != nil {
		return core.ExecutePurgeResult{}, err
	}
	eventID, err := newUUID()
	if err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: generate intent event id: %w", err)
	}
	if err := insertLifecycleEventOn(ctx, conn, core.EvidenceLifecycleEvent{
		EventID:           eventID,
		ObjectID:          request.ObjectID,
		RequestID:         request.RequestID,
		Action:            core.PurgeEventIntent,
		FromState:         string(core.PurgeLifecycleApproved),
		ToState:           string(core.PurgeLifecycleApproved),
		ReviewedHash:      currentHash,
		ResultingHash:     currentHash,
		PrincipalSnapshot: principal.PrincipalSnapshot(),
		Reason:            cmd.Reason,
		PolicyVersion:     authz.LifecyclePolicyVersion,
		CreatedAt:         now,
	}); err != nil {
		return core.ExecutePurgeResult{}, err
	}
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeEvidenceObject, request.ObjectID, core.ReceiptActionPurgeIntent,
		purgeReceiptPayload(scope, request.ObjectID, currentHash, currentHash, cmd.Reason, cmd.ExecutionID, principal), now); err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: emit purge_intent receipt: %w", err)
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: commit purge intent: %w", err)
	}
	committed = true

	// ── TRANSACTION 2: the EXECUTION transaction (design §11 — audit-hardened) ──
	// ONE BEGIN IMMEDIATE transaction: the request/object are RE-READ under scope
	// and the FULL non-overridable blocker set is RE-RUN (closed-period gate →
	// retention re-resolution → eligibility → active blocking hold scan INCLUDING
	// holds committed after the durable intent → lifecycle snapshot/version)
	// while the exclusion lock is held; the guarded byte unlink runs UNDER THAT
	// SAME LOCK and the completion (purge_executed event + receipt, executions →
	// 'completed', projection → 'purged', request → 'executed') persists BEFORE
	// the commit. A hold can NEVER commit between the final revalidation and the
	// unlink; a hold placed after the durable intent BLOCKS the unlink and the
	// attempt stays 'intent' (surfaced interrupted).
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed = false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	result, err := s.executeAndCompleteOn(ctx, conn, cmd, core.EvidencePurgeExecution{
		ExecutionID:        cmd.ExecutionID,
		RequestID:          request.RequestID,
		ObjectID:           request.ObjectID,
		RelPath:            object.RelPath,
		Size:               object.Size,
		PreRemovalHash:     preRemovalHash,
		IntentReviewedHash: currentHash,
		State:              core.PurgeExecutionIntent,
		IntentAt:           now,
		IntentBy:           actorBinding,
	}, principal, now, actorBinding)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	// The idempotency ledger's entity_id is the FK target: the PURGE REQUEST id
	// (evidence_purge_requests.id), never the execution id — the execution id
	// stays in the execution-result/audit fields only.
	if err := completePurgeIdempotencyOn(ctx, conn, scope.OrganizationID, cmd.ExecutionID, request.RequestID, result, now); err != nil {
		return core.ExecutePurgeResult{}, err
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: commit purge execution: %w", err)
	}
	committed = true
	return result, nil
}

// purgeExecutionOn reads ONE executions row by execution id (sql.ErrNoRows
// when the attempt never left a row).
func purgeExecutionOn(ctx context.Context, q Queryer, executionID string) (core.EvidencePurgeExecution, error) {
	e, err := scanPurgeExecution(q.QueryRowContext(ctx,
		`SELECT `+purgeExecutionColumns+` FROM evidence_purge_executions WHERE execution_id = ?`, executionID))
	if err != nil {
		return core.EvidencePurgeExecution{}, err
	}
	return e, nil
}

// purgeIntentExecutionForRequestOn reads the LIVE 'intent' attempt of a request
// EXCLUDING executionID (the stale attempt a fresh id may supersede or
// converge). At most one live intent exists per request (every fresh attempt
// marks its predecessors 'interrupted' under the same lock); the earliest one
// wins defensively.
func purgeIntentExecutionForRequestOn(ctx context.Context, q Queryer, requestID, excludeExecutionID string) (core.EvidencePurgeExecution, bool, error) {
	e, err := scanPurgeExecution(q.QueryRowContext(ctx, `
		SELECT `+purgeExecutionColumns+` FROM evidence_purge_executions
		WHERE request_id = ? AND state = 'intent' AND execution_id != ?
		ORDER BY intent_at LIMIT 1`, requestID, excludeExecutionID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.EvidencePurgeExecution{}, false, nil
		}
		return core.EvidencePurgeExecution{}, false, err
	}
	return e, true, nil
}

// validatePurgeIntentOn verifies the BOUND immutable authorization of an intent:
// the executions row's request/object/rel_path/size/pre_removal_hash must equal
// the CURRENT immutable request/object rows (the intent authorizes the removal
// of EXACTLY the recorded bytes at the recorded content address) and a
// receipt-covered purge_intent event must carry the SAME reviewed snapshot hash
// (the bound authorization was receipt-covered). An invalid intent NEVER
// authorizes anything: missing bytes stay the integrity incident, present bytes
// stay unremoved.
func validatePurgeIntentOn(ctx context.Context, q Queryer, exec core.EvidencePurgeExecution, request core.EvidencePurgeRequest, object core.EvidenceObject) error {
	switch {
	case exec.RequestID != request.RequestID:
		return fmt.Errorf("purge intent %s request id %s differs from the request row %s", exec.ExecutionID, exec.RequestID, request.RequestID)
	case exec.ObjectID != request.ObjectID || exec.ObjectID != object.ObjectID:
		return fmt.Errorf("purge intent %s object id %s differs from the request/object rows %s", exec.ExecutionID, exec.ObjectID, request.ObjectID)
	case exec.RelPath != object.RelPath:
		return fmt.Errorf("purge intent %s rel path %s differs from the object row %s", exec.ExecutionID, exec.RelPath, object.RelPath)
	case exec.Size != object.Size:
		return fmt.Errorf("purge intent %s size %d differs from the object row %d", exec.ExecutionID, exec.Size, object.Size)
	case exec.PreRemovalHash != object.ObjectID:
		return fmt.Errorf("purge intent %s pre-removal hash %s differs from the object id %s", exec.ExecutionID, exec.PreRemovalHash, object.ObjectID)
	case exec.IntentReviewedHash == "":
		return fmt.Errorf("purge intent %s carries no bound reviewed snapshot hash", exec.ExecutionID)
	}
	var covered int
	if err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM evidence_lifecycle_events
		WHERE object_id = ? AND action = 'purge_intent' AND reviewed_hash = ?`,
		exec.ObjectID, exec.IntentReviewedHash,
	).Scan(&covered); err != nil {
		return fmt.Errorf("persistence error: read purge intent coverage: %w", err)
	}
	if covered == 0 {
		return fmt.Errorf("purge intent %s has no receipt-covered reviewed snapshot %s", exec.ExecutionID, exec.IntentReviewedHash)
	}
	return nil
}

// recoverSameExecutionOn is the SAME-execution-id retry of an interrupted
// attempt (runs INSIDE the caller's open BEGIN IMMEDIATE transaction): it loads
// the attempt's executions row and
//
//   - 'completed' → a recovery already completed THIS row (e.g. a fresh-id
//     convergence) but this idempotency key never closed: close the key now and
//     return the stored outcome — no duplicate completion, no second unlink;
//   - 'intent' + bytes missing → the EXACT authorized-absence case: the durable
//     intent plus its bound immutable authorization (validatePurgeIntentOn)
//     converge ONCE to 'purged' with exactly ONE purge_executed receipt (no
//     second unlink, never generic corruption); an invalid intent keeps the
//     missing bytes the integrity incident;
//   - 'intent' + bytes present → the execution transaction re-runs under the
//     SAME lock (fresh revalidation INCLUDING holds committed after the intent,
//     then the guarded unlink and the completion); the completion CONVERGES the
//     interrupted attempt, so the returned outcome reports Recovered=true;
//   - 'interrupted' → TERMINAL for this attempt (design §3.7): surfaced, retry
//     with a FRESH execution id.
func (s *SQLiteStore) recoverSameExecutionOn(ctx context.Context, conn *sql.Conn, cmd core.ExecutePurgeCommand, request core.EvidencePurgeRequest, scope core.Scope, principal auth.VerifiedApprovalPrincipal, now, actorBinding string) (core.ExecutePurgeResult, error) {
	exec, err := purgeExecutionOn(ctx, conn, cmd.ExecutionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The reservation committed but the intent transaction never did (a
			// crash between reservation and intent): nothing is bound to this id —
			// REPORT the interrupted reservation, retry with a FRESH execution id.
			return core.ExecutePurgeResult{}, fmt.Errorf("%s: execution attempt %s for purge request %s never completed (interrupted) — retry with a FRESH execution id", purgeExecutionErrInterrupted, cmd.ExecutionID, cmd.RequestID)
		}
		return core.ExecutePurgeResult{}, err
	}
	switch exec.State {
	case core.PurgeExecutionCompleted:
		result := core.ExecutePurgeResult{Request: request, Execution: exec, IdempotentReplay: true}
		// entity_id is the FK to evidence_purge_requests(id) — the purge request
		// id, never the execution id (the execution id stays in the result/audit).
		if err := completePurgeIdempotencyOn(ctx, conn, scope.OrganizationID, cmd.ExecutionID, request.RequestID, result, now); err != nil {
			return core.ExecutePurgeResult{}, err
		}
		return result, nil
	case core.PurgeExecutionIntent:
		object, ok := s.evidenceObjectByIDOn(ctx, conn, exec.ObjectID)
		if !ok {
			return core.ExecutePurgeResult{}, fmt.Errorf("%s: %s", objectErrNotFound, exec.ObjectID)
		}
		if verr := validatePurgeIntentOn(ctx, conn, exec, request, object); verr != nil {
			// An invalid frozen intent never authorizes anything: the attempt cannot
			// converge and is surfaced interrupted (a fresh id re-runs the intent).
			return core.ExecutePurgeResult{}, fmt.Errorf("%s: execution attempt %s has an invalid bound intent (%v) — retry with a FRESH execution id", purgeExecutionErrInterrupted, cmd.ExecutionID, verr)
		}
		present, perr := s.objectBytesExist(object.RelPath)
		if perr != nil {
			return core.ExecutePurgeResult{}, perr
		}
		if !present {
			return s.convergePurgeOn(ctx, conn, cmd, request, scope, exec, principal, now, actorBinding)
		}
		result, rerr := s.executeAndCompleteOn(ctx, conn, cmd, exec, principal, now, actorBinding)
		if rerr != nil {
			return core.ExecutePurgeResult{}, rerr
		}
		// The re-execution of an interrupted attempt IS the convergence: the durable
		// intent bound the frozen authorization and THIS retry completes it under
		// the exclusion lock — the outcome reports Recovered=true (a fresh
		// attempt's own completion keeps Recovered=false).
		result.Recovered = true
		return result, nil
	case core.PurgeExecutionInterrupted:
		return core.ExecutePurgeResult{}, fmt.Errorf("%s: execution attempt %s is terminal interrupted — retry with a FRESH execution id", purgeExecutionErrInterrupted, cmd.ExecutionID)
	default:
		return core.ExecutePurgeResult{}, fmt.Errorf("%s: execution attempt %s is in an unknown state %q", purgeExecutionErrInterrupted, cmd.ExecutionID, exec.State)
	}
}

// convergePurgeOn converges a VALIDATED interrupted intent whose bytes are
// ALREADY MISSING (a crash after the authorized unlink) to the terminal purged
// state — runs INSIDE the caller's open BEGIN IMMEDIATE transaction. The removal
// is NOT repeated (the bytes are gone) and the completion relies on the intent's
// bound immutable authorization, never on a fresh re-derivation: exactly ONE
// purge_executed event + receipt is emitted (reviewed hash = the FROZEN
// intent_reviewed_hash), the PRIOR executions row flips to 'completed' and the
// request/projection flip to executed/purged. The caller's idempotency key (the
// SAME id on a same-id retry, the FRESH recovery id on a fresh-id convergence)
// is completed with the converged outcome, so a replay returns it without
// duplicating anything. Recovered=true marks the convergence.
func (s *SQLiteStore) convergePurgeOn(ctx context.Context, conn *sql.Conn, cmd core.ExecutePurgeCommand, request core.EvidencePurgeRequest, scope core.Scope, exec core.EvidencePurgeExecution, principal auth.VerifiedApprovalPrincipal, now, actorBinding string) (core.ExecutePurgeResult, error) {
	if request.Status == core.PurgeRequestStatusExecuted {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeAlreadyDecided, "this purge request has already been executed")
	}
	bound, ok, err := retentionPolicyByIDOn(ctx, conn, request.PolicyID)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	if !ok {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "the bound retention policy is no longer readable")
	}
	current, _, err := currentPurgeSnapshotOn(ctx, conn, request.ObjectID, scope, bound.BlockingHoldKinds)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	result, cerr := s.completeExecutionOn(ctx, conn, request, scope, exec, current, principal, cmd.Reason, now, actorBinding, true)
	if cerr != nil {
		return core.ExecutePurgeResult{}, cerr
	}
	// The caller's idempotency key (the SAME id on a same-id retry, the FRESH
	// recovery id on a fresh-id convergence) is completed with the converged
	// outcome carrying the PRIOR execution row, so a replay of that id returns the
	// convergence idempotently — the fresh recovery id aliases the original
	// converged attempt and NEVER leaves an incomplete reservation that would be
	// surfaced PURGE_EXECUTION_INTERRUPTED.
	if err := completePurgeIdempotencyOn(ctx, conn, scope.OrganizationID, cmd.ExecutionID, request.RequestID, result, now); err != nil {
		return core.ExecutePurgeResult{}, err
	}
	return result, nil
}

// executeAndCompleteOn is the EXECUTION transaction of the design
// (audit-hardened): it runs on the caller's OPEN BEGIN IMMEDIATE transaction,
// RE-READS the request/object under scope, RE-RUNS the FULL non-overridable
// blocker set (closed-period gate → retention re-resolution against the bound
// policy → eligibility → active blocking hold scan INCLUDING holds committed
// after the durable intent → lifecycle snapshot/version against the BOUND
// intent_reviewed_hash), invokes the guarded byte unlink WHILE the exclusion
// lock is held, and persists the completion (purge_executed event + receipt,
// executions → 'completed', projection → 'purged', request → 'executed'). A
// blocker or a failed unlink ABORTS (the caller rolls back; the attempt stays
// 'intent' and is surfaced interrupted — nothing is ever unlinked under a stale
// authorization, and a hold can never commit between the final revalidation and
// the unlink).
func (s *SQLiteStore) executeAndCompleteOn(ctx context.Context, conn *sql.Conn, cmd core.ExecutePurgeCommand, exec core.EvidencePurgeExecution, principal auth.VerifiedApprovalPrincipal, now, actorBinding string) (core.ExecutePurgeResult, error) {
	// 1. The requested state is RE-READ under scope at EVERY transition: the
	// request must STILL be 'approved'. A withdraw (or any other transition)
	// racing the intent→execution window aborts — the execution row stays
	// 'intent' and the interrupted intent is surfaced, never hidden.
	request, err := purgeRequestByIDOn(ctx, conn, exec.RequestID)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	if request.Status != core.PurgeRequestStatusApproved {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeInvalidTransition,
			fmt.Sprintf("the purge request is no longer approved (status %s) during execution — the attempt was interrupted after intent and is surfaced as such; retry with a fresh execution id once the pipeline is approved again", request.Status))
	}
	executionScope := purgeRequestScope(request)
	object, ok := s.evidenceObjectByIDOn(ctx, conn, exec.ObjectID)
	if !ok {
		return core.ExecutePurgeResult{}, fmt.Errorf("%s: %s", objectErrNotFound, exec.ObjectID)
	}

	// 2. The FULL blocker set is RE-RUN under the exclusion lock (the audit's
	// TOCTOU closure): a hold placed AFTER the durable intent — including one
	// that committed in the intent→execution window — re-blocks the unlink
	// (HOLD_ACTIVE wins over the now-stale hash, frozen precedence).
	if err := s.assertPeriodWritable(ctx, conn, executionScope, "execute purge"); err != nil {
		return core.ExecutePurgeResult{}, err
	}
	bound, ok, err := retentionPolicyByIDOn(ctx, conn, request.PolicyID)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	if !ok {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "the bound retention policy is no longer readable")
	}
	resolved, matched, err := resolveRetentionPolicyOn(ctx, conn, executionScope, bound.Jurisdiction, bound.Legislation, request.Category)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	if !matched {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "no exact active retention policy resolves at execution time (the engine never guesses)")
	}
	switch eligibility := core.EvaluateRetentionEligibility(resolved, executionScope.Period); eligibility {
	case core.RetentionEligibilityEligible:
		// proceed
	case core.RetentionEligibilityNotDue:
		return core.ExecutePurgeResult{}, auth.New(auth.CodeRetentionNotDue, "the object's period no longer reaches the resolved policy's min_period floor")
	default:
		return core.ExecutePurgeResult{}, auth.New(auth.CodeUnknownRetentionState, "retention state could not be evaluated at execution time (never guessed)")
	}
	current, currentHash, err := currentPurgeSnapshotOn(ctx, conn, request.ObjectID, executionScope, resolved.BlockingHoldKinds)
	if err != nil {
		return core.ExecutePurgeResult{}, err
	}
	if len(current.BlockingHolds) > 0 {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeHoldActive, "an active blocking hold protects this object — the execution is blocked (holds placed after the intent always re-block under the execution lock)")
	}
	if currentHash != exec.IntentReviewedHash {
		return core.ExecutePurgeResult{}, auth.New(auth.CodeLifecycleVersionMismatch,
			fmt.Sprintf("the lifecycle snapshot hash %s no longer matches the bound intent authorization %s — the execution aborts and the attempt stays intent", currentHash, exec.IntentReviewedHash))
	}

	// 3. The guarded byte unlink runs UNDER the exclusion lock (the object layer
	// re-hashes and requires pre_removal_hash == objectId; a mismatch ABORTS and
	// nothing is unlinked).
	if err := s.removeObjectBytes(object.RelPath, request.ObjectID, object.Size); err != nil {
		return core.ExecutePurgeResult{}, err
	}

	// 4. The completion persists BEFORE the commit (receipt-covered: the engine
	// never claims 'purged' unless this transaction committed).
	return s.completeExecutionOn(ctx, conn, request, executionScope, exec, current, principal, cmd.Reason, now, actorBinding, false)
}

// completeExecutionOn persists the receipt-covered completion of ONE execution
// attempt on the caller's OPEN transaction: the purge_executed event + receipt
// (ReviewedHash = the BOUND intent_reviewed_hash — the frozen authorization the
// removal was examined under; ResultingHash = H2, the current snapshot flipped
// to the terminal purged state), the executions row → 'completed' (guarded)
// with the completion receipt id, the request → 'executed' and the projection →
// 'purged'. recovered=true marks a crash convergence (Recovered on the result).
// The caller completes the idempotency key and commits.
func (s *SQLiteStore) completeExecutionOn(ctx context.Context, conn *sql.Conn, request core.EvidencePurgeRequest, scope core.Scope, exec core.EvidencePurgeExecution, current core.LifecycleSnapshot, principal auth.VerifiedApprovalPrincipal, reason, now, actorBinding string, recovered bool) (core.ExecutePurgeResult, error) {
	h2 := assembleSnapshot(request.ObjectID, scope, core.PurgeLifecyclePurged, current.RetentionState,
		current.PolicyID, current.Category, current.PolicyVersion, current.BlockingHolds, request.RequestID, current.ApprovalIDs)
	resultingHash := core.ComputeLifecycleSnapshotHash(h2)

	eventID, err := newUUID()
	if err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: generate completion event id: %w", err)
	}
	if err := insertLifecycleEventOn(ctx, conn, core.EvidenceLifecycleEvent{
		EventID:           eventID,
		ObjectID:          request.ObjectID,
		RequestID:         request.RequestID,
		Action:            core.PurgeEventExecuted,
		FromState:         string(core.PurgeLifecycleApproved),
		ToState:           string(core.PurgeLifecyclePurged),
		ReviewedHash:      exec.IntentReviewedHash,
		ResultingHash:     resultingHash,
		PrincipalSnapshot: principal.PrincipalSnapshot(),
		Reason:            reason,
		PolicyVersion:     authz.LifecyclePolicyVersion,
		CreatedAt:         now,
	}); err != nil {
		return core.ExecutePurgeResult{}, err
	}
	receipt, err := s.emitReceipt(ctx, conn, core.SubjectTypeEvidenceObject, request.ObjectID, core.ReceiptActionPurgeExecuted,
		purgeReceiptPayload(scope, request.ObjectID, exec.IntentReviewedHash, resultingHash, reason, exec.ExecutionID, principal), now)
	if err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: emit purge_executed receipt: %w", err)
	}
	completionReceiptID := ""
	if s.signer != nil {
		completionReceiptID = core.ReceiptHash(receipt)
	}

	// The executions row flips to 'completed' (guarded trigger); the request flips
	// to 'executed' (the guarded status machine) with ITS completion column; the
	// projection flips to 'purged' (terminal).
	res, err := conn.ExecContext(ctx, `
		UPDATE evidence_purge_executions
		SET state = 'completed', completed_at = ?, completed_by = ?, completion_receipt_id = ?
		WHERE execution_id = ? AND state = 'intent'`,
		now, actorBinding, completionReceiptID, exec.ExecutionID,
	)
	if err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: complete purge execution: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: read completed execution rows: %w", err)
	}
	if affected != 1 {
		return core.ExecutePurgeResult{}, fmt.Errorf("%s: execution attempt %s is no longer in state 'intent' — the completion cannot be claimed", purgeExecutionErrInterrupted, exec.ExecutionID)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE evidence_purge_requests SET status = 'executed', execution_id = ? WHERE id = ?`,
		exec.ExecutionID, request.RequestID,
	); err != nil {
		return core.ExecutePurgeResult{}, fmt.Errorf("persistence error: complete purge request: %w", err)
	}
	if err := upsertRetentionStateOn(ctx, conn, core.EvidenceRetentionState{
		ObjectID:              request.ObjectID,
		LifecycleState:        core.PurgeLifecyclePurged,
		RetentionState:        current.RetentionState,
		PolicyID:              current.PolicyID,
		Category:              current.Category,
		HasActiveBlockingHold: len(current.BlockingHolds) > 0,
		CurrentHash:           resultingHash,
		UpdatedAt:             now,
	}); err != nil {
		return core.ExecutePurgeResult{}, err
	}

	// The completed execution row closes the attempt (every evidence column is the
	// intent's immutable bytes; only the guarded state + completion columns moved).
	request.Status = core.PurgeRequestStatusExecuted
	request.ExecutionID = exec.ExecutionID
	completed := exec
	completed.State = core.PurgeExecutionCompleted
	completed.CompletedAt = now
	completed.CompletedBy = actorBinding
	completed.CompletionReceiptID = completionReceiptID
	return core.ExecutePurgeResult{Request: request, Execution: completed, IdempotentReplay: false, Recovered: recovered}, nil
}

// ──────────────────────────────────────────────
// Doctor — purge execution findings (design §13.3, WU-1)
// ──────────────────────────────────────────────

// PurgeDoctorFinding classifies ONE execution-ledger anomaly the doctor surface
// reports (design §13.3): an 'intent' execution row (the durable intent
// committed but the completion did not — a crash between intent and completion
// is the RECOVERY WINDOW surfaced for operator visibility) or an 'interrupted'
// execution row (a TERMINAL attempt whose intent never completed — the frozen
// PURGE_EXECUTION_INTERRUPTED code; design §3.7). Both are REPORTED findings,
// never failed, never repaired: the doctor is read-only evidence. The finding
// carries the exact execution/request/object identity, the guarded state, the
// intent metadata, the completion-receipt presence and a SAFE read-only
// bytes-state probe (present | absent | unreadable) so an operator can tell a
// present-bytes intent (the execution transaction can re-run) from an
// absent-bytes intent (the exact authorized-absence convergence case). The
// tenant/company/RUC/period tuple comes from the STORED request row (the scope
// authority — never a caller-declared or derived scope).
type PurgeDoctorFinding struct {
	Kind                string `json:"kind"` // PURGE_EXECUTION_INTENT | PURGE_EXECUTION_INTERRUPTED
	ExecutionID         string `json:"executionId"`
	RequestID           string `json:"requestId"`
	ObjectID            string `json:"objectId"`
	State               string `json:"state"`
	RelPath             string `json:"relPath,omitempty"`
	Size                int64  `json:"size"`
	PreRemovalHash      string `json:"preRemovalHash,omitempty"`
	IntentReviewedHash  string `json:"intentReviewedHash,omitempty"`
	IntentAt            string `json:"intentAt"`
	IntentBy            string `json:"intentBy"`
	CompletionReceiptID string `json:"completionReceiptId,omitempty"`
	BytesState          string `json:"bytesState"` // present | absent | unreadable
	TenantID            string `json:"tenantId,omitempty"`
	CompanyID           string `json:"companyId,omitempty"`
	RUC                 string `json:"ruc,omitempty"`
	Period              string `json:"period,omitempty"`
	Detail              string `json:"detail,omitempty"`
}

// Purge execution finding kinds reported by the doctor (§13.3).
const (
	// purgeFindingIntent is the §13.3 finding token of a LIVE 'intent'
	// execution row: the durable intent committed but the completion did not —
	// the crash recovery window.
	purgeFindingIntent = "PURGE_EXECUTION_INTENT"
	// purgeFindingInterrupted is the §13.3 finding token of a TERMINAL
	// 'interrupted' execution row — the frozen PURGE_EXECUTION_INTERRUPTED code.
	purgeFindingInterrupted = purgeExecutionErrInterrupted
)

// Purge execution bytes-state probe outcomes (read-only, never a write).
const (
	purgeBytesStatePresent    = "present"
	purgeBytesStateAbsent     = "absent"
	purgeBytesStateUnreadable = "unreadable"
)

// doctorPurgeExecutionsScan audits the immutable execution-attempt ledger for
// the doctor surface (design §13.3): every 'intent' / 'interrupted' row is
// REPORTED as an auditable finding with its exact identity, the guarded state,
// the intent metadata, the completion-receipt presence (always empty for
// intent/interrupted — the guarded machine permits the receipt only on the
// intent → completed transition) and a SAFE read-only bytes-state probe through
// the SAME path-containment defenses as reads/writes (objectBytesExist — an
// invalid/symlinked path is a typed probe failure, never a followed path). The
// exact stored scope tuple comes from the request row the execution is bound
// to. The scan is strictly READ-ONLY: nothing is deleted, moved or repaired,
// and a probe failure is REPORTED in the finding detail — the fail-closed
// corruption gate stays with the object-layer scan (doctorObjectScan), which
// already aborts the report on undocumented missing bytes or invalid row paths.
func (s *SQLiteStore) doctorPurgeExecutionsScan() ([]PurgeDoctorFinding, error) {
	rows, err := s.db.Query(`
		SELECT e.execution_id, e.request_id, e.object_id, e.rel_path, e.size,
		       e.pre_removal_hash, e.intent_reviewed_hash, e.state, e.intent_at, e.intent_by,
		       COALESCE(e.completion_receipt_id, ''),
		       r.tenant_id, r.company_id, r.ruc, r.period
		FROM evidence_purge_executions e
		JOIN evidence_purge_requests r ON r.id = e.request_id
		WHERE e.state IN ('intent','interrupted')
		ORDER BY e.intent_at, e.execution_id`)
	if err != nil {
		return nil, fmt.Errorf("corrupt store: read purge executions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var findings []PurgeDoctorFinding
	for rows.Next() {
		var (
			f     PurgeDoctorFinding
			size  int64
			state string
		)
		if err := rows.Scan(&f.ExecutionID, &f.RequestID, &f.ObjectID, &f.RelPath, &size,
			&f.PreRemovalHash, &f.IntentReviewedHash, &state, &f.IntentAt, &f.IntentBy,
			&f.CompletionReceiptID, &f.TenantID, &f.CompanyID, &f.RUC, &f.Period); err != nil {
			return nil, fmt.Errorf("corrupt store: scan purge execution: %w", err)
		}
		f.Size = size
		f.State = state
		if state == string(core.PurgeExecutionIntent) {
			f.Kind = purgeFindingIntent
		} else {
			f.Kind = purgeFindingInterrupted
		}
		switch present, perr := s.objectBytesExist(f.RelPath); {
		case perr != nil:
			f.BytesState = purgeBytesStateUnreadable
			f.Detail = fmt.Sprintf("bytes-state probe failed: %v", perr)
		case present:
			f.BytesState = purgeBytesStatePresent
		default:
			f.BytesState = purgeBytesStateAbsent
		}
		findings = append(findings, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("corrupt store: read purge executions: %w", err)
	}
	return findings, nil
}
