// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the LOST-RESPONSE /
// INTERRUPTED-RESERVATION contract of the idempotency families beyond purge
// (FR-L.5 / AC-L-4): a reservation row that was created but never completed
// (entity/event/result/completed columns NULL) MUST yield a deterministic,
// TYPED outcome — never a scan, JSON-decode or persistence error — and MUST NOT
// produce a duplicate mutation, event or receipt.
//
// The frozen per-family semantics (mirroring the purge precedent at
// purge_store_test.go:58/:121) are NOT uniform:
//
//   - conflict families — review (review_idempotency_keys) and hold
//     (evidence_hold_idempotency_keys) — surface the TYPED IDEMPOTENCY_CONFLICT
//     incomplete-reservation outcome and write NOTHING (fail closed), exactly
//     like purge;
//   - reuse (deterministic re-derivation) families — approval (idempotency_keys),
//     judgment propose (judgment_idempotency_keys) and reconciliation propose
//     (reconciliation_idempotency_keys) — REUSE the reservation and re-derive the
//     operation deterministically (the code comments say so explicitly:
//     store.go:3516 "Incomplete reservation ... reuse it", store.go:4269
//     "an incomplete one (an interrupted attempt) is reused and the open-tuple
//     index decides below"); the operation completes EXACTLY ONCE and the next
//     identical invocation replays the stored outcome with IdempotentReplay=true
//     and no second event/receipt.
//
// Reopen has NO nullable reservation ledger (period_closure_events is the
// completed immutable outcome and the projection flip + event insert are ONE
// BEGIN IMMEDIATE transaction), so its lost-response proof is two-part: a
// temporary test trigger aborts the event insert AFTER the projection update was
// attempted and the whole transaction rolls back; then the post-commit replay
// (discarding the first result and invoking the same command again) replays the
// stored event with IdempotentReplay=true and no duplicate event/receipt.
// Existing purge-execution recovery anchors (purge_execution_test.go:559/:713/
// :802) and the fresh purge interrupted tests stay green.
package store

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// interruptedInvocation carries the exact command (and its tenant/request-id/
// principal/source binding) that a lost-response retry would re-invoke.
type interruptedInvocation struct {
	tenantID  string
	requestID string
	cmd       any
	principal auth.VerifiedApprovalPrincipal
	caller    core.Source
}

// effectSnapshot is a deterministic LOGICAL digest (operation-specific entity/
// projection counts + event/receipt/idempotency-ledger counts) — never raw
// SQLite bytes.
type effectSnapshot map[string]int64

// interruptedReservationCase is the consolidated test-only descriptor
// (design §L — the same shape the purge precedent uses, extended per family).
type interruptedReservationCase struct {
	operation string
	seed      func(t *testing.T, s *SQLiteStore) interruptedInvocation
	insert    func(t *testing.T, s *SQLiteStore, in interruptedInvocation)
	invoke    func(context.Context, *SQLiteStore, interruptedInvocation) (any, error)
	// wantCode is the frozen typed code the interrupted replay MUST surface
	// (conflict families). Empty selects the frozen deterministic
	// re-derivation families (approval/judgment/reconciliation), which complete
	// exactly once and then replay the stored outcome.
	wantCode     string
	snapshot     func(t *testing.T, s *SQLiteStore, in interruptedInvocation) effectSnapshot
	assertReplay func(t *testing.T, in interruptedInvocation, first, second any)
}

func TestInterruptedReservationDeterministicRecovery(t *testing.T) {
	cases := []interruptedReservationCase{
		interruptedApprovalReservationCase(),
		interruptedJudgmentProposeReservationCase(),
		interruptedReconciliationProposeReservationCase(),
		interruptedReviewRejectReservationCase(),
		interruptedHoldPlaceReservationCase(),
	}
	for _, tc := range cases {
		t.Run(tc.operation+"/interrupted-reservation", func(t *testing.T) {
			s := newTestStore(t)
			s.SetReceiptSigner(newParitySigner(s))
			in := tc.seed(t, s)
			tc.insert(t, s, in)

			if tc.wantCode != "" {
				// Conflict family: the interrupted replay is the TYPED conflict —
				// never a scan/JSON/persistence error — and writes NOTHING.
				before := tc.snapshot(t, s, in)
				_, err := tc.invoke(context.Background(), s, in)
				if err == nil || !strings.Contains(err.Error(), tc.wantCode) || strings.Contains(err.Error(), "persistence error") {
					t.Fatalf("interrupted reservation replay = %v, want the typed %s (never a scan/persistence error)", err, tc.wantCode)
				}
				after := tc.snapshot(t, s, in)
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("interrupted replay mutated state: before %v after %v (fail closed, zero effect)", before, after)
				}
				return
			}

			// Reuse family: the frozen reservation semantics re-derive the
			// operation deterministically — it completes EXACTLY ONCE, and a
			// repeated invocation replays the stored outcome with no second
			// event/receipt/ledger mutation.
			first, err := tc.invoke(context.Background(), s, in)
			if err != nil {
				t.Fatalf("re-derivation of an interrupted reservation must succeed: %v", err)
			}
			afterFirst := tc.snapshot(t, s, in)
			second, err := tc.invoke(context.Background(), s, in)
			if err != nil {
				t.Fatalf("post-completion replay must succeed: %v", err)
			}
			tc.assertReplay(t, in, first, second)
			afterSecond := tc.snapshot(t, s, in)
			if !reflect.DeepEqual(afterFirst, afterSecond) {
				t.Fatalf("replay duplicated effects: after-first %v after-second %v", afterFirst, afterSecond)
			}
		})
	}
}

// ──────────────────────────────────────────────
// Approval (idempotency_keys) — reuse family
// ──────────────────────────────────────────────

func interruptedApprovalReservationCase() interruptedReservationCase {
	const requestID = "req-approval-interrupted-001"
	return interruptedReservationCase{
		operation: "approve-memory",
		seed: func(t *testing.T, s *SQLiteStore) interruptedInvocation {
			seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
			saved, err := s.Save(gatedInput("tax.igv.interrupted", "needs approval"))
			if err != nil {
				t.Fatalf("save approval fixture: %v", err)
			}
			return interruptedInvocation{
				tenantID:  testOrgID,
				requestID: requestID,
				cmd: core.ApproveMemoryCommand{
					MemoryID:             saved.Memory.Identity.ID,
					ExpectedEnvelopeHash: currentEnvelope(saved),
					Reason:               "approved by fixture reviewer",
					RequestID:            requestID,
				},
				principal: controllerPrincipal(t),
			}
		},
		insert: func(t *testing.T, s *SQLiteStore, in interruptedInvocation) {
			cmd := in.cmd.(core.ApproveMemoryCommand)
			commandHash := approveCommandHash(cmd.MemoryID, cmd.ExpectedEnvelopeHash, cmd.Reason)
			if _, err := s.db.Exec(`
				INSERT INTO idempotency_keys (tenant_id, request_id, command_hash, principal_subject_id, membership_id, approval_event_id, result_json, created_at, completed_at)
				VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, NULL)`,
				in.tenantID, cmd.RequestID, commandHash, in.principal.SubjectID(), in.principal.MembershipID(), testT,
			); err != nil {
				t.Fatalf("insert interrupted approval reservation: %v", err)
			}
		},
		invoke: func(ctx context.Context, s *SQLiteStore, in interruptedInvocation) (any, error) {
			cmd := in.cmd.(core.ApproveMemoryCommand)
			return s.ApproveMemory(ctx, cmd, in.principal, authz.NewApprovalPolicy())
		},
		wantCode: "", // reuse family
		snapshot: func(t *testing.T, s *SQLiteStore, in interruptedInvocation) effectSnapshot {
			cmd := in.cmd.(core.ApproveMemoryCommand)
			return effectSnapshot{
				"approval_events":   int64(countRows(t, s, `SELECT COUNT(*) FROM approval_events WHERE memory_id = ?`, cmd.MemoryID)),
				"approved_receipts": int64(countRows(t, s, `SELECT COUNT(*) FROM receipts WHERE subject_type = 'memory' AND subject_id = ? AND action = 'memory_approved'`, cmd.MemoryID)),
				"idempotency_keys":  int64(countRows(t, s, `SELECT COUNT(*) FROM idempotency_keys WHERE tenant_id = ? AND request_id = ?`, in.tenantID, cmd.RequestID)),
			}
		},
		assertReplay: func(t *testing.T, in interruptedInvocation, first, second any) {
			f, s := first.(core.ApprovalResult), second.(core.ApprovalResult)
			if !s.IdempotentReplay || s.ApprovalEventID != f.ApprovalEventID || s.MemoryID != f.MemoryID {
				t.Fatalf("replay = %+v, want the stored outcome (event %s) with idempotentReplay", s, f.ApprovalEventID)
			}
		},
	}
}

// ──────────────────────────────────────────────
// Judgment propose (judgment_idempotency_keys) — reuse family
// ──────────────────────────────────────────────

func interruptedJudgmentProposeReservationCase() interruptedReservationCase {
	const requestID = "req-judgment-propose-interrupted-001"
	return interruptedReservationCase{
		operation: "judgment-propose",
		seed: func(t *testing.T, s *SQLiteStore) interruptedInvocation {
			from, to := proposeContext(t, s)
			return interruptedInvocation{
				tenantID:  testOrgID,
				requestID: requestID,
				cmd: core.ProposeJudgmentCommand{
					FromID: from, ToID: to, Relation: core.RelationSupports,
					Reason: "proposal reason", RequestID: requestID,
				},
				caller: testProposer,
			}
		},
		insert: func(t *testing.T, s *SQLiteStore, in interruptedInvocation) {
			cmd := in.cmd.(core.ProposeJudgmentCommand)
			commandHash := proposeJudgmentCommandHash(cmd)
			binding := proposerBinding(in.caller)
			if _, err := s.db.Exec(`
				INSERT INTO judgment_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, judgment_id, result_json, judgment_event_id, created_at, completed_at)
				VALUES (?, ?, ?, ?, NULL, NULL, NULL, ?, NULL)`,
				in.tenantID, cmd.RequestID, commandHash, binding, testT,
			); err != nil {
				t.Fatalf("insert interrupted judgment reservation: %v", err)
			}
		},
		invoke: func(ctx context.Context, s *SQLiteStore, in interruptedInvocation) (any, error) {
			cmd := in.cmd.(core.ProposeJudgmentCommand)
			return s.ProposeJudgment(ctx, cmd, in.caller)
		},
		wantCode: "", // reuse family
		snapshot: func(t *testing.T, s *SQLiteStore, in interruptedInvocation) effectSnapshot {
			return effectSnapshot{
				"judgments":          int64(countRows(t, s, `SELECT COUNT(*) FROM judgments WHERE status = 'proposed'`)),
				"judgment_idem_keys": int64(countRows(t, s, `SELECT COUNT(*) FROM judgment_idempotency_keys WHERE tenant_id = ? AND request_id = ?`, in.tenantID, in.requestID)),
			}
		},
		assertReplay: func(t *testing.T, in interruptedInvocation, first, second any) {
			f, s := first.(core.ProposeJudgmentResult), second.(core.ProposeJudgmentResult)
			if !s.IdempotentReplay || s.JudgmentID != f.JudgmentID || s.Judgment.ID != f.JudgmentID {
				t.Fatalf("replay = %+v, want the stored proposal %s with idempotentReplay", s, f.JudgmentID)
			}
		},
	}
}

// ──────────────────────────────────────────────
// Reconciliation propose (reconciliation_idempotency_keys) — reuse family
// ──────────────────────────────────────────────

func interruptedReconciliationProposeReservationCase() interruptedReservationCase {
	const requestID = "req-reconciliation-propose-interrupted-001"
	return interruptedReservationCase{
		operation: "reconciliation-propose",
		seed: func(t *testing.T, s *SQLiteStore) interruptedInvocation {
			left, right := reconcileContext(t, s)
			cmd := baseReconcileCmd(left, right)
			cmd.RequestID = requestID
			return interruptedInvocation{
				tenantID:  testOrgID,
				requestID: requestID,
				cmd:       cmd,
				caller:    reconciliationProposer,
			}
		},
		insert: func(t *testing.T, s *SQLiteStore, in interruptedInvocation) {
			cmd := in.cmd.(core.ProposeReconciliationCommand)
			commandHash := proposeReconciliationCommandHash(cmd)
			binding := proposerBinding(in.caller)
			if _, err := s.db.Exec(`
				INSERT INTO reconciliation_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, reconciliation_id, result_json, reconciliation_event_id, created_at, completed_at)
				VALUES (?, ?, ?, ?, NULL, NULL, NULL, ?, NULL)`,
				in.tenantID, cmd.RequestID, commandHash, binding, testT,
			); err != nil {
				t.Fatalf("insert interrupted reconciliation reservation: %v", err)
			}
		},
		invoke: func(ctx context.Context, s *SQLiteStore, in interruptedInvocation) (any, error) {
			cmd := in.cmd.(core.ProposeReconciliationCommand)
			return s.ProposeReconciliation(ctx, cmd, in.caller)
		},
		wantCode: "", // reuse family
		snapshot: func(t *testing.T, s *SQLiteStore, in interruptedInvocation) effectSnapshot {
			return effectSnapshot{
				"reconciliations":          int64(countRows(t, s, `SELECT COUNT(*) FROM reconciliations WHERE status = 'proposed'`)),
				"reconciliation_idem_keys": int64(countRows(t, s, `SELECT COUNT(*) FROM reconciliation_idempotency_keys WHERE tenant_id = ? AND request_id = ?`, in.tenantID, in.requestID)),
			}
		},
		assertReplay: func(t *testing.T, in interruptedInvocation, first, second any) {
			f, s := first.(core.ProposeReconciliationResult), second.(core.ProposeReconciliationResult)
			if !s.IdempotentReplay || s.ReconciliationID != f.ReconciliationID || s.Reconciliation.ID != f.ReconciliationID {
				t.Fatalf("replay = %+v, want the stored proposal %s with idempotentReplay", s, f.ReconciliationID)
			}
		},
	}
}

// ──────────────────────────────────────────────
// Review reject (review_idempotency_keys) — conflict family
// ──────────────────────────────────────────────

func interruptedReviewRejectReservationCase() interruptedReservationCase {
	const requestID = "req-review-reject-interrupted-001"
	return interruptedReservationCase{
		operation: "review-reject",
		seed: func(t *testing.T, s *SQLiteStore) interruptedInvocation {
			seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
			res := mustSave(t, s, reviewSaveInput("review.interrupted.reject", "pending", "agent-a"))
			return interruptedInvocation{
				tenantID:  testOrgID,
				requestID: requestID,
				cmd: core.RejectMemoryCommand{
					MemoryID:             res.Memory.Identity.ID,
					ExpectedEnvelopeHash: currentEnvelope(res),
					Reason:               "rejected in review",
					RequestID:            requestID,
				},
				principal: controllerPrincipal(t),
			}
		},
		insert: func(t *testing.T, s *SQLiteStore, in interruptedInvocation) {
			cmd := in.cmd.(core.RejectMemoryCommand)
			commandHash := decisionCommandHash(cmd.MemoryID, cmd.ExpectedEnvelopeHash, cmd.Reason)
			actorBinding := in.principal.SubjectID() + "\x00" + in.principal.MembershipID()
			if _, err := s.db.Exec(`
				INSERT INTO review_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, decision_event_id, result_json, created_at, completed_at)
				VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
				in.tenantID, cmd.RequestID, commandHash, actorBinding, testT,
			); err != nil {
				t.Fatalf("insert interrupted review reservation: %v", err)
			}
		},
		invoke: func(ctx context.Context, s *SQLiteStore, in interruptedInvocation) (any, error) {
			cmd := in.cmd.(core.RejectMemoryCommand)
			return s.RejectMemory(ctx, cmd, in.principal)
		},
		wantCode: auth.CodeIdempotencyConflict,
		snapshot: func(t *testing.T, s *SQLiteStore, in interruptedInvocation) effectSnapshot {
			cmd := in.cmd.(core.RejectMemoryCommand)
			return effectSnapshot{
				"decision_events":   int64(countRows(t, s, `SELECT COUNT(*) FROM memory_decision_events WHERE memory_id = ?`, cmd.MemoryID)),
				"rejected_receipts": int64(countRows(t, s, `SELECT COUNT(*) FROM receipts WHERE subject_type = 'memory' AND subject_id = ? AND action = 'memory_rejected'`, cmd.MemoryID)),
				"review_idem_keys":  int64(countRows(t, s, `SELECT COUNT(*) FROM review_idempotency_keys WHERE tenant_id = ? AND request_id = ?`, in.tenantID, cmd.RequestID)),
			}
		},
	}
}

// ──────────────────────────────────────────────
// Hold place (evidence_hold_idempotency_keys) — conflict family
// ──────────────────────────────────────────────

func interruptedHoldPlaceReservationCase() interruptedReservationCase {
	const requestID = "req-hold-place-interrupted-001"
	return interruptedReservationCase{
		operation: "hold-place",
		seed: func(t *testing.T, s *SQLiteStore) interruptedInvocation {
			objectID := holdFixtureObject(t, s)
			return interruptedInvocation{
				tenantID:  testOrgID,
				requestID: requestID,
				cmd: core.PlaceHoldCommand{
					ObjectID:       objectID,
					Kind:           core.HoldKindAudit,
					Reason:         "audit in progress",
					OwnerSubjectID: "subject-9",
					RequestID:      requestID,
				},
				principal: recordsPrincipal(t),
			}
		},
		insert: func(t *testing.T, s *SQLiteStore, in interruptedInvocation) {
			cmd := in.cmd.(core.PlaceHoldCommand)
			commandHash := holdCommandHash(placeHoldCommandShape{
				ObjectID: cmd.ObjectID, Kind: string(cmd.Kind),
				Reason: cmd.Reason, OwnerSubjectID: cmd.OwnerSubjectID,
			})
			if _, err := s.db.Exec(`
				INSERT INTO evidence_hold_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, hold_id, result_json, created_at, completed_at)
				VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
				in.tenantID, cmd.RequestID, commandHash, in.principal.SubjectID(), testT,
			); err != nil {
				t.Fatalf("insert interrupted hold reservation: %v", err)
			}
		},
		invoke: func(ctx context.Context, s *SQLiteStore, in interruptedInvocation) (any, error) {
			cmd := in.cmd.(core.PlaceHoldCommand)
			return s.PlaceHold(ctx, cmd, in.principal)
		},
		wantCode: auth.CodeIdempotencyConflict,
		snapshot: func(t *testing.T, s *SQLiteStore, in interruptedInvocation) effectSnapshot {
			cmd := in.cmd.(core.PlaceHoldCommand)
			return effectSnapshot{
				"holds":          int64(countRows(t, s, `SELECT COUNT(*) FROM evidence_holds WHERE object_id = ?`, cmd.ObjectID)),
				"hold_receipts":  int64(countRows(t, s, `SELECT COUNT(*) FROM receipts WHERE subject_type = 'evidence_object' AND subject_id = ? AND action = 'hold_placed'`, cmd.ObjectID)),
				"hold_idem_keys": int64(countRows(t, s, `SELECT COUNT(*) FROM evidence_hold_idempotency_keys WHERE tenant_id = ? AND request_id = ?`, in.tenantID, cmd.RequestID)),
			}
		},
	}
}

// ──────────────────────────────────────────────
// Reopen — two-part lost-response proof (no nullable reservation ledger)
// ──────────────────────────────────────────────

// TestReopenPeriodInterruptedCommitRollsBackAtomic is part (a) of the reopen
// lost-response proof (AC-L-4, FR-L.5): reopen has NO nullable reservation row —
// period_closure_events is the completed immutable outcome and the projection
// flip + event insert are ONE BEGIN IMMEDIATE transaction. A temporary test
// trigger aborts the 'reopened' event INSERT after the projection UPDATE was
// attempted; the whole transaction MUST roll back (period stays closed, no
// event/receipt/request row), and a clean retry succeeds exactly once.
func TestReopenPeriodInterruptedCommitRollsBackAtomic(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	closeID, _ := saveAndApproveClose(t, s, scope, "interrupted close", "req-reopen-interrupted-close")

	if _, err := s.db.Exec(`
		CREATE TRIGGER test_abort_reopened_event
		BEFORE INSERT ON period_closure_events
		WHEN NEW.action = 'reopened'
		BEGIN
			SELECT RAISE(ABORT, 'TEST_ABORT_REOPEN_EVENT');
		END`); err != nil {
		t.Fatalf("install abort trigger: %v", err)
	}
	defer func() {
		_, _ = s.db.Exec(`DROP TRIGGER IF EXISTS test_abort_reopened_event`)
	}()

	// The reopen flips the projection (step 6), then the event INSERT (step 7)
	// is aborted by the trigger — the whole transaction must roll back.
	_, err := reopen(s, scope, closeID, "interrupted reopen", "req-reopen-interrupted", controllerPrincipal(t))
	if err == nil || !strings.Contains(err.Error(), "TEST_ABORT_REOPEN_EVENT") {
		t.Fatalf("triggered reopen = %v, want the injected TEST_ABORT_REOPEN_EVENT failure", err)
	}

	// Rollback coherence: projection still closed, no event, no receipt, no
	// idempotency row (period_closure_events carries the request id).
	status, storedCloseID, _, _, ok := readClosure(t, s, scope)
	if !ok || status != "closed" || storedCloseID != closeID {
		t.Fatalf("projection after aborted reopen = (%s, %s), want (closed, %s)", status, storedCloseID, closeID)
	}
	if _, _, _, _, _, ok := readReopenEvent(t, s, scope, "req-reopen-interrupted"); ok {
		t.Fatal("an aborted reopen must not append a closure event")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts WHERE action = 'memory_reopened' AND subject_id = ?`, closeID); n != 0 {
		t.Fatalf("memory_reopened receipts = %d, want 0 (rolled back)", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM period_closure_events WHERE tenant_id = ? AND request_id = ?`, scope.OrganizationID, "req-reopen-interrupted"); n != 0 {
		t.Fatalf("closure events for the interrupted request = %d, want 0", n)
	}

	// Remove the failure injection: the clean retry runs against the REAL schema.
	if _, err := s.db.Exec(`DROP TRIGGER test_abort_reopened_event`); err != nil {
		t.Fatalf("drop abort trigger: %v", err)
	}

	// Clean retry (fresh request id) succeeds EXACTLY ONCE.
	first, err := reopen(s, scope, closeID, "clean retry", "req-reopen-clean", controllerPrincipal(t))
	if err != nil {
		t.Fatalf("clean retry reopen: %v", err)
	}
	if first.IdempotentReplay {
		t.Fatal("clean retry must be a fresh reopen")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM period_closure_events WHERE tenant_id = ? AND action = 'reopened'`, scope.OrganizationID); n != 1 {
		t.Fatalf("reopened events = %d, want exactly 1 (retry succeeded exactly once)", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts WHERE action = 'memory_reopened' AND subject_id = ?`, closeID); n != 1 {
		t.Fatalf("memory_reopened receipts = %d, want exactly 1", n)
	}
}

// TestReopenPeriodPostCommitResponseLossReplays is part (b) of the reopen
// lost-response proof: after a SUCCESSFUL commit, a lost response (the caller
// discards the first result and invokes the same command again) replays the
// existing event with IdempotentReplay=true and no duplicate event/receipt.
func TestReopenPeriodPostCommitResponseLossReplays(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	closeID, _ := saveAndApproveClose(t, s, scope, "response-loss close", "req-reopen-response-loss-close")
	principal := controllerPrincipal(t)

	// Discard the first successful result — the caller never saw the response.
	if _, err := reopen(s, scope, closeID, "reopen after response loss", "req-reopen-response-loss", principal); err != nil {
		t.Fatalf("first reopen: %v", err)
	}
	replay, err := reopen(s, scope, closeID, "reopen after response loss", "req-reopen-response-loss", principal)
	if err != nil {
		t.Fatalf("re-invoke after response loss: %v", err)
	}
	if !replay.IdempotentReplay || replay.Status != "reopened" {
		t.Fatalf("re-invoke = %+v, want the stored reopened outcome with idempotentReplay", replay)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM period_closure_events WHERE tenant_id = ? AND request_id = ?`, scope.OrganizationID, "req-reopen-response-loss"); n != 1 {
		t.Fatalf("closure events = %d, want exactly 1 (no duplicate event)", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts WHERE action = 'memory_reopened' AND subject_id = ?`, closeID); n != 1 {
		t.Fatalf("memory_reopened receipts = %d, want exactly 1 (no duplicate receipt)", n)
	}
}
