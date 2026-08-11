// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module freezes the v0.8 PHYSICAL PURGE EXECUTION store
// surface (docs/architecture/evidence-lifecycle-v0.8.md §2/§3.7/§11; schema
// v12):
//
//   - ExecutePurge runs the TWO-PHASE, RECEIPT-COVERED protocol for an APPROVED
//     pipeline: a durable intent transaction (request re-read under scope,
//     (tenant, executionId) idempotency, state gate, the FULL blocker re-check
//     INCLUDING holds placed after approval, authz, the executions row in state
//     'intent' + purge_intent event + receipt, COMMIT without touching bytes),
//     the byte removal OUTSIDE SQL (re-hash before unlink — a mismatch ABORTS
//     and nothing is deleted), and a durable completion transaction (request
//     re-read under scope again, executions → 'completed' with the completion
//     receipt id, purge_executed event + receipt, projection → 'purged',
//     request → 'executed');
//   - an active post-approval hold BLOCKS execution with HOLD_ACTIVE WITHOUT
//     destroying the approval (lift the hold and the same pipeline executes);
//   - corrupt bytes abort the execution with OBJECT_HASH_MISMATCH and are NEVER
//     unlinked; the interrupted intent stays 'intent' (surfaced, never
//     pretended completed);
//   - retry/idempotency is safe by EXECUTION ID: replaying a completed
//     execution id returns the stored outcome; a SAME-id retry of an interrupted
//     attempt CONVERGES under the durable intent's bound immutable authorization
//     (bytes present -> the execution transaction re-runs; bytes missing -> the
//     exact authorized-absence case converges ONCE to 'purged' with exactly ONE
//     purge_executed receipt — no second unlink, never generic corruption); a
//     retry with a FRESH id marks a stale attempt with PRESENT bytes
//     'interrupted' (terminal) and runs a new execution row, while a fresh
//     recovery id of a missing-bytes attempt converges the PRIOR intent with NO
//     duplicate completion/event;
//   - only object BYTES are removed at successful execution; the immutable
//     evidence metadata/hash/event/approval/receipt rows never change.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// purgeExecutionFixture is a fully APPROVED purge pipeline: the stored object,
// the exact-company retention policy, the approved request and the canonical
// approval-resulting snapshot hash H2 the executor examines.
type purgeExecutionFixture struct {
	objectID     string
	object       core.EvidenceObject
	policy       core.RetentionPolicy
	request      core.EvidencePurgeRequest
	approval     core.EvidencePurgeApproval
	approvedHash string
	scope        core.Scope
}

// approvedPurgePipeline drives object store → policy put (exact company scope,
// single approval) → request (accountant subject-2) → approval (records
// compliance officer subject-1) and returns the approved fixture with the
// canonical H2 the executor must examine.
func approvedPurgePipeline(t *testing.T, s *SQLiteStore) purgeExecutionFixture {
	t.Helper()
	ctx := context.Background()
	scope := testScope(testRucA)

	objResult, err := s.StoreObject(ctx, objectInputForTest(t, []byte("purge-target-bytes-0123456789")))
	if err != nil {
		t.Fatalf("store purge target object: %v", err)
	}
	objectID := objResult.Object.ObjectID

	polResult, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.Scope = companyPolicyScope() // the EXACT company scope of the object (tenant-level policies never resolve for a company tuple)
		cmd.MinPeriod = "202401"
		cmd.RequestID = "req-policy-exec-fixture"
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("put retention policy: %v", err)
	}
	policy := polResult.Policy

	current, currentHash, err := currentPurgeSnapshotOn(ctx, s.db, objectID, scope, policy.BlockingHoldKinds)
	if err != nil {
		t.Fatalf("read pre-request lifecycle snapshot: %v", err)
	}
	if current.LifecycleState != core.PurgeLifecycleStored || current.RetentionState != core.RetentionEligibility("unmanaged") {
		t.Fatalf("pre-request snapshot = %+v, want stored/unmanaged", current)
	}

	reqResult, err := s.RequestPurge(ctx, core.RequestPurgeCommand{
		ObjectID:              objectID,
		Jurisdiction:          "PE",
		Legislation:           "NATIONAL-TAX",
		Category:              "invoice",
		ExpectedLifecycleHash: currentHash,
		Reason:                "retention period elapsed",
		RequestID:             "req-purge-exec-fixture",
	}, subjectPrincipal(t, "subject-2", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
	if err != nil {
		t.Fatalf("request purge: %v", err)
	}
	request := reqResult.Request
	h2Request := core.ComputeLifecycleSnapshotHash(assembleSnapshot(objectID, scope, core.PurgeLifecycleRequested,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		[]core.LifecycleHoldRef{}, request.RequestID, []string{}))

	appResult, err := s.ApprovePurge(ctx, core.ApprovePurgeCommand{
		RequestID:             request.RequestID,
		ExpectedLifecycleHash: h2Request,
		Reason:                "verified against the reviewed snapshot",
		RequestIDKey:          "req-approve-exec-fixture",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("approve purge: %v", err)
	}
	if appResult.Request.Status != core.PurgeRequestStatusApproved {
		t.Fatalf("fixture request status = %s, want approved (single-approval category)", appResult.Request.Status)
	}
	h2Approved := core.ComputeLifecycleSnapshotHash(assembleSnapshot(objectID, scope, core.PurgeLifecycleApproved,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		[]core.LifecycleHoldRef{}, request.RequestID, []string{appResult.Approval.ApprovalID}))

	return purgeExecutionFixture{
		objectID:     objectID,
		object:       objResult.Object,
		policy:       policy,
		request:      appResult.Request,
		approval:     appResult.Approval,
		approvedHash: h2Approved,
		scope:        scope,
	}
}

// executionState reads the guarded state of one executions row.
func executionState(t *testing.T, s *SQLiteStore, executionID string) string {
	t.Helper()
	var state string
	if err := s.db.QueryRow(`SELECT state FROM evidence_purge_executions WHERE execution_id = ?`, executionID).Scan(&state); err != nil {
		t.Fatalf("read execution %s state: %v", executionID, err)
	}
	return state
}

// objectBytesPath resolves the absolute WORM byte path of an object (tests may
// corrupt/restore the bytes to exercise the hash-before-unlink contract).
func objectBytesPath(t *testing.T, s *SQLiteStore, object core.EvidenceObject) string {
	t.Helper()
	return filepath.Join(s.objectsRoot, filepath.FromSlash(object.RelPath))
}

// TestExecutePurgeHappyPath is the frozen happy-path table: every executor role
// of the design's execute matrix (default approver + dual second approver set)
// physically executes the approved pipeline through the two-phase protocol.
// The object bytes are gone, the immutable metadata/events/receipts stay, the
// projection is purged (terminal), the request is executed, a replay returns
// the stored outcome with NO new rows, and a fresh execution id is
// ALREADY_DECIDED.
func TestExecutePurgeHappyPath(t *testing.T) {
	executors := []struct {
		name      string
		role      auth.AccountingRole
		subjectID string
	}{
		{"default approver", auth.RoleRecordsComplianceOfficer, "exec-1"},
		{"tenant records owner", auth.RoleTenantRecordsOwner, "exec-2"},
		{"controller second approver", auth.RoleController, "exec-3"},
		{"tax responsible second approver", auth.RoleTaxResponsible, "exec-4"},
	}
	for _, tt := range executors {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			s.SetReceiptSigner(newParitySigner(s))
			fx := approvedPurgePipeline(t, s)

			result, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
				RequestID:             fx.request.RequestID,
				ExpectedLifecycleHash: fx.approvedHash,
				Reason:                "execution batch approved",
				ExecutionID:           "00000000-0000-4000-8000-000000000501",
			}, subjectPrincipal(t, tt.subjectID, []auth.AccountingRole{tt.role}, auth.AssuranceStandard))
			if err != nil {
				t.Fatalf("execute purge: %v", err)
			}
			if result.IdempotentReplay {
				t.Fatal("a fresh execution id must not replay")
			}

			// The execution attempt completed with full provenance.
			e := result.Execution
			if e.State != core.PurgeExecutionCompleted {
				t.Fatalf("execution state = %q, want completed", e.State)
			}
			if e.ExecutionID != "00000000-0000-4000-8000-000000000501" || e.RequestID != fx.request.RequestID || e.ObjectID != fx.objectID {
				t.Fatalf("execution identity = %+v", e)
			}
			if e.RelPath != fx.object.RelPath || e.Size != fx.object.Size || e.PreRemovalHash != fx.objectID {
				t.Fatalf("execution byte facts = %+v", e)
			}
			if e.IntentAt == "" || e.IntentBy != tt.subjectID || e.CompletedAt == "" || e.CompletedBy != tt.subjectID {
				t.Fatalf("execution provenance = %+v", e)
			}
			if e.CompletionReceiptID == "" {
				t.Fatal("a completed execution must carry the completion receipt id")
			}

			// The request is executed with its guarded completion column; the
			// projection is purged (terminal).
			if result.Request.Status != core.PurgeRequestStatusExecuted || result.Request.ExecutionID != e.ExecutionID {
				t.Fatalf("request after execution = %+v", result.Request)
			}
			projection, ok, err := retentionStateOn(context.Background(), s.db, fx.objectID)
			if err != nil || !ok {
				t.Fatalf("read projection: ok=%v err=%v", ok, err)
			}
			if projection.LifecycleState != core.PurgeLifecyclePurged || projection.CurrentHash == "" {
				t.Fatalf("projection after execution = %+v, want purged", projection)
			}

			// The BYTES are gone (documented receipt-covered absence), the metadata
			// row is IMMUTABLE (never deleted).
			if _, err := s.readObjectBytes(fx.object.RelPath); err == nil {
				t.Fatal("object bytes must be removed at successful execution")
			} else if !strings.Contains(err.Error(), objectErrBytesMissing) {
				t.Fatalf("missing bytes must fail closed with OBJECT_BYTES_MISSING, got %v", err)
			}
			if _, ok := s.EvidenceObjectByID(context.Background(), fx.objectID); !ok {
				t.Fatal("the evidence_objects metadata row must survive the purge (immutable)")
			}

			// The intent and completion are RECEIPT-COVERED on the object chain
			// (v0.9.0 payload, the frozen lifecycle policy version).
			for _, action := range []string{"purge_intent", "purge_executed"} {
				var payloadJSON, policyVersion, prev string
				if err := s.db.QueryRow(`SELECT payload_json, policy_version, previous_receipt_hash FROM receipts WHERE subject_type = 'evidence_object' AND subject_id = ? AND action = ?`, fx.objectID, action).Scan(&payloadJSON, &policyVersion, &prev); err != nil {
					t.Fatalf("read %s receipt: %v", action, err)
				}
				if policyVersion != "evidence-lifecycle-policy/v0.8.0" {
					t.Fatalf("%s policy version = %q", action, policyVersion)
				}
				if !strings.Contains(payloadJSON, `"version":"receipt-payload/v0.9.0"`) {
					t.Fatalf("%s payload must be v0.9.0: %s", action, payloadJSON)
				}
			}
			// The completion receipt is the executions row's completion_receipt_id
			// and the purge_executed receipt CHAINS on the purge_intent receipt.
			var completionHash, intentHash string
			if err := s.db.QueryRow(`SELECT receipt_hash FROM receipts WHERE action = 'purge_intent' AND subject_id = ?`, fx.objectID).Scan(&intentHash); err != nil {
				t.Fatalf("read purge_intent hash: %v", err)
			}
			if err := s.db.QueryRow(`SELECT previous_receipt_hash FROM receipts WHERE action = 'purge_executed' AND subject_id = ?`, fx.objectID).Scan(&completionHash); err != nil {
				t.Fatalf("read purge_executed predecessor: %v", err)
			}
			if completionHash != intentHash {
				t.Fatalf("purge_executed must chain on the purge_intent receipt: prev=%s want=%s", completionHash, intentHash)
			}

			// The lifecycle events log holds both transitions (immutable source of
			// truth); the intent event carries the SAME reviewed/resulting hash
			// (the intent changes no canonical snapshot field).
			var intentEvents, executedEvents int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_intent'`, fx.objectID).Scan(&intentEvents); err != nil {
				t.Fatalf("count intent events: %v", err)
			}
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedEvents); err != nil {
				t.Fatalf("count executed events: %v", err)
			}
			if intentEvents != 1 || executedEvents != 1 {
				t.Fatalf("events = (%d intent, %d executed), want exactly one each", intentEvents, executedEvents)
			}
			var reviewed, resulting, fromState, toState string
			if err := s.db.QueryRow(`SELECT reviewed_hash, resulting_hash, from_state, to_state FROM evidence_lifecycle_events WHERE action = 'purge_intent' AND object_id = ?`, fx.objectID).Scan(&reviewed, &resulting, &fromState, &toState); err != nil {
				t.Fatalf("read intent event: %v", err)
			}
			if reviewed != fx.approvedHash || resulting != fx.approvedHash || fromState != "purge_approved" || toState != "purge_approved" {
				t.Fatalf("intent event = (%s, %s, %s, %s), want reviewed==resulting==approved hash at purge_approved", reviewed, resulting, fromState, toState)
			}
			var executedReviewed, executedResulting string
			if err := s.db.QueryRow(`SELECT reviewed_hash, resulting_hash FROM evidence_lifecycle_events WHERE action = 'purge_executed' AND object_id = ?`, fx.objectID).Scan(&executedReviewed, &executedResulting); err != nil {
				t.Fatalf("read executed event: %v", err)
			}
			if executedReviewed != fx.approvedHash {
				t.Fatalf("executed event reviewed hash = %s, want the examined approval hash", executedReviewed)
			}
			if executedResulting == "" || executedResulting == executedReviewed {
				t.Fatalf("executed event resulting hash must differ from the reviewed hash (purged snapshot): %s", executedResulting)
			}

			// Replay with the SAME execution id returns the stored outcome with NO
			// new rows/events/receipts.
			replayed, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
				RequestID:             fx.request.RequestID,
				ExpectedLifecycleHash: fx.approvedHash,
				Reason:                "execution batch approved",
				ExecutionID:           "00000000-0000-4000-8000-000000000501",
			}, subjectPrincipal(t, tt.subjectID, []auth.AccountingRole{tt.role}, auth.AssuranceStandard))
			if err != nil {
				t.Fatalf("replay execution: %v", err)
			}
			if !replayed.IdempotentReplay || replayed.Execution.State != core.PurgeExecutionCompleted {
				t.Fatalf("replay = %+v, want an idempotent replay of the completed attempt", replayed)
			}
			var executionRows int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_purge_executions WHERE request_id = ?`, fx.request.RequestID).Scan(&executionRows); err != nil {
				t.Fatalf("count executions: %v", err)
			}
			if executionRows != 1 {
				t.Fatalf("executions rows = %d, want exactly 1 (replay must not create a new attempt)", executionRows)
			}
			// The replay is a decoded copy of the stored outcome.
			// The stored result_json stays the immutable ORIGINAL; nothing is re-persisted.
			// Only the returned outcome carries the replay flag.
			var storedResult string
			if err := s.db.QueryRow(`SELECT result_json FROM evidence_purge_idempotency_keys WHERE tenant_id = ? AND request_id = ?`, fx.scope.OrganizationID, "00000000-0000-4000-8000-000000000501").Scan(&storedResult); err != nil {
				t.Fatalf("read stored replay result: %v", err)
			}
			if strings.Contains(storedResult, `"idempotentReplay":true`) {
				t.Fatalf("stored outcome must stay the immutable original, got: %s", storedResult)
			}

			// A FRESH execution id on an already-executed request is
			// ALREADY_DECIDED.
			_, err = s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
				RequestID:             fx.request.RequestID,
				ExpectedLifecycleHash: fx.approvedHash,
				ExecutionID:           "00000000-0000-4000-8000-000000000502",
			}, subjectPrincipal(t, tt.subjectID, []auth.AccountingRole{tt.role}, auth.AssuranceStandard))
			if err == nil || !strings.Contains(err.Error(), auth.CodeAlreadyDecided) {
				t.Fatalf("fresh execution of an executed request = %v, want ALREADY_DECIDED", err)
			}
		})
	}
}

// TestExecutePurgeStateAndGate freezes the execution state gate and the
// authenticated execute gate: only an APPROVED pipeline executes
// (INVALID_TRANSITION otherwise, ALREADY_DECIDED after execution), and an
// executor outside the execute matrix is denied with the frozen role code
// before any intent is written.
func TestExecutePurgeStateAndGate(t *testing.T) {
	t.Run("unapproved request", func(t *testing.T) {
		s := newTestStore(t)
		s.SetReceiptSigner(newParitySigner(s))
		fx := approvedPurgePipeline(t, s)
		// Withdraw the approval — the pipeline returns to stored and the request
		// is withdrawn; execution must fail closed.
		withdrawn, err := s.WithdrawPurge(context.Background(), core.WithdrawPurgeCommand{
			RequestID:    fx.request.RequestID,
			Reason:       "cleanup before execution",
			RequestIDKey: "req-withdraw-exec-fixture",
		}, recordsPrincipal(t))
		if err != nil {
			t.Fatalf("withdraw: %v", err)
		}
		_ = withdrawn
		_, err = s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
			RequestID:             fx.request.RequestID,
			ExpectedLifecycleHash: fx.approvedHash,
			ExecutionID:           "00000000-0000-4000-8000-000000000601",
		}, recordsPrincipal(t))
		if err == nil || !strings.Contains(err.Error(), auth.CodeInvalidTransition) {
			t.Fatalf("executing a withdrawn request = %v, want INVALID_TRANSITION", err)
		}
		var executionRows int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_purge_executions`).Scan(&executionRows); err != nil {
			t.Fatalf("count executions: %v", err)
		}
		if executionRows != 0 {
			t.Fatalf("a blocked execution must write NO execution row, got %d", executionRows)
		}
	})

	t.Run("non-executor denied", func(t *testing.T) {
		s := newTestStore(t)
		s.SetReceiptSigner(newParitySigner(s))
		fx := approvedPurgePipeline(t, s)
		_, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
			RequestID:             fx.request.RequestID,
			ExpectedLifecycleHash: fx.approvedHash,
			ExecutionID:           "00000000-0000-4000-8000-000000000602",
		}, subjectPrincipal(t, "acct-9", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
		if err == nil || !strings.Contains(err.Error(), auth.CodeRoleNotAuthorized) {
			t.Fatalf("an accountant executor = %v, want ROLE_NOT_AUTHORIZED", err)
		}
		var executionRows int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_purge_executions`).Scan(&executionRows); err != nil {
			t.Fatalf("count executions: %v", err)
		}
		if executionRows != 0 {
			t.Fatalf("a denied execution must write NO execution row, got %d", executionRows)
		}
		// The approval survives intact: the pipeline is still executable.
		if _, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
			RequestID:             fx.request.RequestID,
			ExpectedLifecycleHash: fx.approvedHash,
			ExecutionID:           "00000000-0000-4000-8000-000000000603",
		}, recordsPrincipal(t)); err != nil {
			t.Fatalf("the denied attempt must not destroy the approval: %v", err)
		}
	})

	t.Run("malformed execution id", func(t *testing.T) {
		s := newTestStore(t)
		fx := approvedPurgePipeline(t, s)
		_, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
			RequestID:             fx.request.RequestID,
			ExpectedLifecycleHash: fx.approvedHash,
			ExecutionID:           "not-a-uuid",
		}, recordsPrincipal(t))
		if err == nil || !strings.Contains(err.Error(), "INVALID_PURGE_EXECUTION_ID") {
			t.Fatalf("a malformed execution id = %v, want INVALID_PURGE_EXECUTION_ID", err)
		}
	})
}

// TestExecutePurgePostApprovalHoldBlocks freezes the post-approval hold
// revalidation: a blocking hold placed AFTER the approval blocks execution with
// HOLD_ACTIVE (the frozen blocker code wins over the now-stale expected hash),
// NO intent is written, the bytes stay, and the APPROVAL is NOT destroyed —
// lifting the hold lets the SAME pipeline execute to completion.
func TestExecutePurgePostApprovalHoldBlocks(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := approvedPurgePipeline(t, s)

	// A legal hold placed AFTER the approval (kind in the policy's default
	// blocking set).
	hold, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       fx.objectID,
		Kind:           core.HoldKindLegal,
		Reason:         "dispute filed after approval",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-hold-after-approval",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("place post-approval hold: %v", err)
	}

	_, err = s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-000000000701",
	}, recordsPrincipal(t))
	if err == nil || !strings.Contains(err.Error(), auth.CodeHoldActive) {
		t.Fatalf("execution under a post-approval hold = %v, want HOLD_ACTIVE", err)
	}

	// No intent, no removal, no projection flip: the executions ledger and the
	// event log are empty and the bytes are still verifiable.
	var executionRows, intentEvents int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_purge_executions`).Scan(&executionRows); err != nil {
		t.Fatalf("count executions: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE action = 'purge_intent' AND object_id = ?`, fx.objectID).Scan(&intentEvents); err != nil {
		t.Fatalf("count intent events: %v", err)
	}
	if executionRows != 0 || intentEvents != 0 {
		t.Fatalf("a held execution must write no intent, got (%d executions, %d intent events)", executionRows, intentEvents)
	}
	if err := s.VerifyObjectBytes(context.Background(), fx.objectID); err != nil {
		t.Fatalf("the object bytes must be untouched under the hold: %v", err)
	}
	request, err := purgeRequestByIDOn(context.Background(), s.db, fx.request.RequestID)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	if request.Status != core.PurgeRequestStatusApproved {
		t.Fatalf("the post-approval hold must not destroy the approval: status = %s", request.Status)
	}

	// Lifting the hold restores executability: the SAME approval executes.
	if _, err := s.LiftHold(context.Background(), core.LiftHoldCommand{
		HoldID:    hold.Hold.HoldID,
		Reason:    "dispute resolved",
		RequestID: "req-lift-after-approval",
	}, recordsPrincipal(t)); err != nil {
		t.Fatalf("lift hold: %v", err)
	}
	result, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-000000000702",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("execute after lifting the hold: %v", err)
	}
	if result.Execution.State != core.PurgeExecutionCompleted || result.Request.Status != core.PurgeRequestStatusExecuted {
		t.Fatalf("post-lift execution = %+v, want completed/executed", result)
	}
}

// TestExecutePurgeCorruptBytesAbortNoUnlink freezes the hash-before-unlink
// contract: corrupt bytes re-hash to a different content address, the execution
// ABORTS with OBJECT_HASH_MISMATCH and the corrupt file is NEVER unlinked. The
// intent committed durably (receipt-covered, design §11) and stays 'intent' —
// the attempt is interrupted, never pretended completed; the request stays
// approved.
func TestExecutePurgeCorruptBytesAbortNoUnlink(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := approvedPurgePipeline(t, s)

	path := objectBytesPath(t, s, fx.object)
	if err := os.WriteFile(path, []byte("CORRUPTED-BYTES-NOT-THE-ORIGINAL"), 0o600); err != nil {
		t.Fatalf("corrupt object bytes: %v", err)
	}

	_, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-000000000801",
	}, recordsPrincipal(t))
	if err == nil || !strings.Contains(err.Error(), objectErrHashMismatch) {
		t.Fatalf("execution on corrupt bytes = %v, want OBJECT_HASH_MISMATCH", err)
	}

	// The corrupt file is STILL on disk (abort — never unlinked), the attempt is
	// 'intent' (interrupted, surfaced), the request is still approved and no
	// completion was ever claimed.
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("the corrupt bytes must NOT be unlinked: %v", statErr)
	}
	if got := executionState(t, s, "00000000-0000-4000-8000-000000000801"); got != string(core.PurgeExecutionIntent) {
		t.Fatalf("interrupted attempt state = %q, want intent", got)
	}
	var executedEvents, executedReceipts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedEvents); err != nil {
		t.Fatalf("count executed events: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM receipts WHERE subject_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedReceipts); err != nil {
		t.Fatalf("count executed receipts: %v", err)
	}
	if executedEvents != 0 || executedReceipts != 0 {
		t.Fatalf("a failed removal must never claim completion: (%d events, %d receipts)", executedEvents, executedReceipts)
	}
	request, err := purgeRequestByIDOn(context.Background(), s.db, fx.request.RequestID)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	if request.Status != core.PurgeRequestStatusApproved {
		t.Fatalf("an aborted removal must not flip the request: status = %s", request.Status)
	}
}

// TestExecutePurgeInterruptedIntentAndRetry freezes the interrupted/retry
// protocol (design §3.7/§11 — audit-hardened): a corrupt-byte abort leaves the
// attempt in 'intent'; retrying the SAME execution id re-runs the execution
// transaction under the bound intent and ABORTS AGAIN on the still-corrupt bytes
// (OBJECT_HASH_MISMATCH — a byte-integrity failure is never converged as an
// authorized absence, the corrupt file is never unlinked); after restoring the
// bytes, a retry with a FRESH execution id marks the stale attempt 'interrupted'
// (terminal) and runs a new execution row to completion — exactly one
// purge_executed event/receipt, two receipt-covered intents, one per attempt.
func TestExecutePurgeInterruptedIntentAndRetry(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := approvedPurgePipeline(t, s)

	original := []byte("purge-target-bytes-0123456789")
	path := objectBytesPath(t, s, fx.object)
	if err := os.WriteFile(path, []byte("CORRUPTED-BYTES-NOT-THE-ORIGINAL"), 0o600); err != nil {
		t.Fatalf("corrupt object bytes: %v", err)
	}

	// Attempt 1: the intent commits, the byte removal aborts (hash mismatch).
	if _, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-000000000901",
	}, recordsPrincipal(t)); err == nil || !strings.Contains(err.Error(), objectErrHashMismatch) {
		t.Fatalf("attempt 1 = %v, want OBJECT_HASH_MISMATCH", err)
	}

	// Retrying the SAME execution id re-runs the execution transaction under the
	// bound intent: the blockers still pass (nothing changed), so the guarded
	// unlink re-hashes the STILL-CORRUPT bytes and ABORTS again with
	// OBJECT_HASH_MISMATCH — the corrupt file is never unlinked and the attempt
	// stays 'intent' (a byte-integrity failure is never converged as an
	// authorized absence).
	_, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-000000000901",
	}, recordsPrincipal(t))
	if err == nil || !strings.Contains(err.Error(), objectErrHashMismatch) {
		t.Fatalf("same-id retry on still-corrupt bytes = %v, want OBJECT_HASH_MISMATCH", err)
	}
	if got := executionState(t, s, "00000000-0000-4000-8000-000000000901"); got != string(core.PurgeExecutionIntent) {
		t.Fatalf("the interrupted attempt must stay intent: %q", got)
	}

	// Restore the exact WORM bytes (the corruption is repaired by the operator —
	// the store itself never repairs).
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("restore object bytes: %v", err)
	}

	// Retry with a FRESH execution id: the stale attempt becomes 'interrupted'
	// (terminal for that attempt) and the fresh attempt executes to completion.
	result, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-000000000902",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("retry execution: %v", err)
	}
	if result.Execution.State != core.PurgeExecutionCompleted || result.IdempotentReplay {
		t.Fatalf("fresh retry = %+v, want a completed non-replay attempt", result)
	}
	if got := executionState(t, s, "00000000-0000-4000-8000-000000000901"); got != string(core.PurgeExecutionInterrupted) {
		t.Fatalf("the stale attempt must be terminal interrupted: %q", got)
	}
	if got := executionState(t, s, "00000000-0000-4000-8000-000000000902"); got != string(core.PurgeExecutionCompleted) {
		t.Fatalf("the fresh attempt must be completed: %q", got)
	}
	// The interrupted attempt carries NO completion columns (guarded transition).
	var completedAt any
	if err := s.db.QueryRow(`SELECT completed_at FROM evidence_purge_executions WHERE execution_id = '00000000-0000-4000-8000-000000000901'`).Scan(&completedAt); err != nil {
		t.Fatalf("read interrupted completion column: %v", err)
	}
	if completedAt != nil {
		t.Fatalf("an interrupted attempt must carry no completion timestamp, got %v", completedAt)
	}

	// Exactly one completion was ever claimed (one purge_executed event +
	// receipt); each attempt emitted its own receipt-covered intent (two
	// purge_intent events + receipts — the singleton index exemption).
	var executedEvents, intentEvents int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedEvents); err != nil {
		t.Fatalf("count executed events: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_intent'`, fx.objectID).Scan(&intentEvents); err != nil {
		t.Fatalf("count intent events: %v", err)
	}
	if executedEvents != 1 || intentEvents != 2 {
		t.Fatalf("events = (%d executed, %d intent), want exactly one completion and one intent per attempt", executedEvents, intentEvents)
	}

	// The two attempts' intent receipts are DISTINCT: each payload carries its
	// own execution-attempt id (the additive v0.9.0 executionAttemptId
	// discriminator, populated from the execution id), so the payload hashes
	// differ and the fresh-ID retry NEVER collides on the
	// UNIQUE(subject_type, subject_id, action, payload_hash) backstop — every
	// intent receipt stays uniquely auditable.
	intentHashes := make(map[string]string)
	for _, execID := range []string{"00000000-0000-4000-8000-000000000901", "00000000-0000-4000-8000-000000000902"} {
		var payloadJSON, payloadHash string
		if err := s.db.QueryRow(`SELECT payload_json, payload_hash FROM receipts WHERE subject_id = ? AND action = 'purge_intent' AND instr(payload_json, ?) > 0`, fx.objectID, execID).Scan(&payloadJSON, &payloadHash); err != nil {
			t.Fatalf("read intent receipt for attempt %s: %v", execID, err)
		}
		if !strings.Contains(payloadJSON, `"executionAttemptId":"`+execID+`"`) {
			t.Fatalf("intent receipt payload for attempt %s must carry its execution-attempt id: %s", execID, payloadJSON)
		}
		intentHashes[execID] = payloadHash
	}
	if intentHashes["00000000-0000-4000-8000-000000000901"] == intentHashes["00000000-0000-4000-8000-000000000902"] {
		t.Fatal("the two attempts' intent receipts must carry DISTINCT payload hashes (the execution-attempt discriminator) — a fresh-ID retry must never collide on the payload-hash UNIQUE backstop")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the retried execution must have removed the bytes: %v", err)
	}

	// Replay of the completed FRESH id is idempotent.
	replayed, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-000000000902",
	}, recordsPrincipal(t))
	if err != nil || !replayed.IdempotentReplay {
		t.Fatalf("replay of the completed retry = %+v err=%v, want an idempotent replay", replayed, err)
	}
}

// crashWindowFixture builds an approved pipeline whose bytes are ALREADY gone
// before any execution, then drives ONE ExecutePurge so the durable intent
// commits and the execution transaction fails closed on the missing bytes —
// the EXACT database state a crash between the authorized unlink and the
// completion commit leaves behind (executions row 'intent', idempotency key
// incomplete, no purge_executed, bytes gone). Returns the store, the fixture
// and the execution id.
func crashWindowFixture(t *testing.T, s *SQLiteStore, executionID string) purgeExecutionFixture {
	t.Helper()
	fx := approvedPurgePipeline(t, s)
	if err := os.Remove(objectBytesPath(t, s, fx.object)); err != nil {
		t.Fatalf("remove object bytes (simulated crash window): %v", err)
	}
	if _, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           executionID,
	}, recordsPrincipal(t)); err == nil || !strings.Contains(err.Error(), objectErrBytesMissing) {
		t.Fatalf("execute purge on pre-missing bytes = %v, want OBJECT_BYTES_MISSING (the interrupted window)", err)
	}
	if got := executionState(t, s, executionID); got != string(core.PurgeExecutionIntent) {
		t.Fatalf("execution state = %q, want intent (the interrupted window)", got)
	}
	return fx
}

// TestExecutePurgeCrashAfterUnlinkSameIDConverges freezes the audit's Fix 1
// crash convergence: after a crash between the authorized unlink and the
// completion, retrying the SAME execution id detects the EXACT
// authorized-missing-bytes case through the durable intent plus its bound
// immutable authorization (never generic corruption) and converges ONCE to
// PURGED with exactly ONE purge_executed event + receipt — no second unlink, no
// duplicate execution row — and a replay of the completed id is idempotent.
func TestExecutePurgeCrashAfterUnlinkSameIDConverges(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	execID := "00000000-0000-4000-8000-000000001001"
	fx := crashWindowFixture(t, s, execID)

	converged, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           execID,
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("same-id recovery must converge, got: %v", err)
	}
	if !converged.Recovered || converged.IdempotentReplay {
		t.Fatalf("recovery result = %+v, want Recovered=true and NOT an idempotent replay", converged)
	}
	if converged.Execution.State != core.PurgeExecutionCompleted || converged.Execution.ExecutionID != execID {
		t.Fatalf("converged execution = %+v, want the completed original attempt", converged.Execution)
	}
	if converged.Request.Status != core.PurgeRequestStatusExecuted {
		t.Fatalf("converged request status = %s, want executed", converged.Request.Status)
	}
	if got := executionState(t, s, execID); got != string(core.PurgeExecutionCompleted) {
		t.Fatalf("execution state after convergence = %q, want completed", got)
	}
	var executedEvents, executedReceipts, executionRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedEvents); err != nil {
		t.Fatalf("count executed events: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM receipts WHERE subject_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedReceipts); err != nil {
		t.Fatalf("count executed receipts: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_purge_executions WHERE request_id = ?`, fx.request.RequestID).Scan(&executionRows); err != nil {
		t.Fatalf("count executions: %v", err)
	}
	if executedEvents != 1 || executedReceipts != 1 {
		t.Fatalf("convergence completion = (%d events, %d receipts), want exactly ONE of each", executedEvents, executedReceipts)
	}
	if executionRows != 1 {
		t.Fatalf("executions rows = %d, want exactly 1 (a recovery never creates a duplicate attempt)", executionRows)
	}
	// The completion chains on the ORIGINAL purge_intent receipt (the frozen
	// authorization's receipt-covered history).
	var intentHash, executedPrev string
	if err := s.db.QueryRow(`SELECT receipt_hash FROM receipts WHERE action = 'purge_intent' AND subject_id = ?`, fx.objectID).Scan(&intentHash); err != nil {
		t.Fatalf("read purge_intent hash: %v", err)
	}
	if err := s.db.QueryRow(`SELECT previous_receipt_hash FROM receipts WHERE action = 'purge_executed' AND subject_id = ?`, fx.objectID).Scan(&executedPrev); err != nil {
		t.Fatalf("read purge_executed predecessor: %v", err)
	}
	if executedPrev != intentHash {
		t.Fatalf("the converged completion must chain on the ORIGINAL intent receipt: prev=%s want=%s", executedPrev, intentHash)
	}

	// Replay of the completed id is idempotent with NO new rows.
	replayed, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           execID,
	}, recordsPrincipal(t))
	if err != nil || !replayed.IdempotentReplay {
		t.Fatalf("replay of the converged id = %+v err=%v, want an idempotent replay", replayed, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedEvents); err != nil {
		t.Fatalf("count executed events after replay: %v", err)
	}
	if executedEvents != 1 {
		t.Fatalf("a replay must not duplicate the completion: events = %d", executedEvents)
	}
	// The projection is terminal purged and the metadata row is immutable.
	projection, ok, err := retentionStateOn(context.Background(), s.db, fx.objectID)
	if err != nil || !ok {
		t.Fatalf("read projection: ok=%v err=%v", ok, err)
	}
	if projection.LifecycleState != core.PurgeLifecyclePurged {
		t.Fatalf("projection = %s, want purged (terminal)", projection.LifecycleState)
	}
	if _, ok := s.EvidenceObjectByID(context.Background(), fx.objectID); !ok {
		t.Fatal("the immutable metadata row must survive the convergence")
	}
}

// TestExecutePurgeCrashAfterUnlinkFreshIDConvergesNoDuplicate freezes the audit
// requirement that a FRESH recovery id NEVER creates a duplicate
// completion/event: it converges the PRIOR intent row (the fresh id becomes the
// replay key of that convergence) with exactly ONE purge_executed event/receipt
// and exactly ONE executions row, and a replay of the fresh recovery id returns
// the converged outcome idempotently.
func TestExecutePurgeCrashAfterUnlinkFreshIDConvergesNoDuplicate(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	priorExecID := "00000000-0000-4000-8000-000000001101"
	freshExecID := "00000000-0000-4000-8000-000000001102"
	fx := crashWindowFixture(t, s, priorExecID)

	// A FRESH recovery id: the callers may re-derive the current hash — the
	// convergence is bound to the PRIOR intent's frozen authorization.
	current, currentHash, err := currentPurgeSnapshotOn(context.Background(), s.db, fx.objectID, fx.scope, fx.policy.BlockingHoldKinds)
	if err != nil {
		t.Fatalf("read current snapshot: %v", err)
	}
	_ = current
	recovered, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: currentHash,
		ExecutionID:           freshExecID,
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("fresh recovery id must converge the prior intent: %v", err)
	}
	if !recovered.Recovered || recovered.IdempotentReplay {
		t.Fatalf("fresh recovery result = %+v, want Recovered=true and NOT an idempotent replay", recovered)
	}
	// The converged outcome IS the PRIOR intent's execution row — the fresh id
	// never becomes a second attempt.
	if recovered.Execution.ExecutionID != priorExecID {
		t.Fatalf("converged execution id = %q, want the PRIOR intent %q (no duplicate attempt)", recovered.Execution.ExecutionID, priorExecID)
	}
	if got := executionState(t, s, priorExecID); got != string(core.PurgeExecutionCompleted) {
		t.Fatalf("prior execution state = %q, want completed (converged)", got)
	}
	var executionRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_purge_executions WHERE request_id = ?`, fx.request.RequestID).Scan(&executionRows); err != nil {
		t.Fatalf("count executions: %v", err)
	}
	if executionRows != 1 {
		t.Fatalf("executions rows = %d, want exactly 1 (a fresh recovery id must not create a duplicate)", executionRows)
	}
	var executedEvents, intentEvents int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedEvents); err != nil {
		t.Fatalf("count executed events: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_intent'`, fx.objectID).Scan(&intentEvents); err != nil {
		t.Fatalf("count intent events: %v", err)
	}
	if executedEvents != 1 || intentEvents != 1 {
		t.Fatalf("events = (%d executed, %d intent), want exactly ONE of each (the fresh id emits NO new intent and NO duplicate completion)", executedEvents, intentEvents)
	}

	// A replay of the FRESH recovery id returns the converged outcome idempotently.
	replayed, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: currentHash,
		ExecutionID:           freshExecID,
	}, recordsPrincipal(t))
	if err != nil || !replayed.IdempotentReplay {
		t.Fatalf("replay of the fresh recovery id = %+v err=%v, want an idempotent replay of the convergence", replayed, err)
	}
	if replayed.Execution.ExecutionID != priorExecID {
		t.Fatalf("replayed outcome execution id = %q, want the converged prior intent", replayed.Execution.ExecutionID)
	}
	// A FRESH id on the now-executed request is ALREADY_DECIDED.
	_, err = s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: currentHash,
		ExecutionID:           "00000000-0000-4000-8000-000000001103",
	}, recordsPrincipal(t))
	if err == nil || !strings.Contains(err.Error(), auth.CodeAlreadyDecided) {
		t.Fatalf("fresh execution after convergence = %v, want ALREADY_DECIDED", err)
	}
}

// TestExecutePurgeHoldAfterIntentBlocksReExecution freezes the TOCTOU closure
// on the same-id recovery path: an interrupted attempt whose bytes are STILL
// PRESENT re-runs the execution transaction under the exclusion lock, and a
// blocking hold placed after the durable intent RE-BLOCKS the unlink with
// HOLD_ACTIVE (the bytes stay, the attempt stays 'intent', the approval is not
// destroyed); lifting the hold lets the SAME execution id converge.
func TestExecutePurgeHoldAfterIntentBlocksReExecution(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := approvedPurgePipeline(t, s)
	execID := "00000000-0000-4000-8000-000000001201"

	// Corrupt the bytes so the first attempt commits its intent and aborts the
	// unlink (bytes PRESENT, attempt 'intent' — the crash-BEFORE-unlink window).
	path := objectBytesPath(t, s, fx.object)
	if err := os.WriteFile(path, []byte("CORRUPTED-BYTES-NOT-THE-ORIGINAL"), 0o600); err != nil {
		t.Fatalf("corrupt object bytes: %v", err)
	}
	if _, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           execID,
	}, recordsPrincipal(t)); err == nil || !strings.Contains(err.Error(), objectErrHashMismatch) {
		t.Fatalf("attempt 1 = %v, want OBJECT_HASH_MISMATCH", err)
	}

	// Restore the exact bytes, then place a blocking hold AFTER the durable
	// intent: the same-id retry must RE-BLOCK under the execution lock.
	if err := os.WriteFile(path, []byte("purge-target-bytes-0123456789"), 0o600); err != nil {
		t.Fatalf("restore object bytes: %v", err)
	}
	if _, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       fx.objectID,
		Kind:           core.HoldKindLegal,
		Reason:         "hold committed in the intent window",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-hold-intent-window",
	}, recordsPrincipal(t)); err != nil {
		t.Fatalf("place post-intent hold: %v", err)
	}
	_, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           execID,
	}, recordsPrincipal(t))
	if err == nil || !strings.Contains(err.Error(), auth.CodeHoldActive) {
		t.Fatalf("same-id retry under a post-intent hold = %v, want HOLD_ACTIVE (the unlink is re-blocked under the lock)", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("the bytes must NOT be unlinked under the hold: %v", statErr)
	}
	if got := executionState(t, s, execID); got != string(core.PurgeExecutionIntent) {
		t.Fatalf("the attempt must stay intent under the hold: %q", got)
	}

	// Lifting the hold lets the SAME execution id converge to completion.
	if _, err := s.LiftHold(context.Background(), core.LiftHoldCommand{
		HoldID:    "",
		Reason:    "dispute resolved",
		RequestID: "req-lift-intent-window",
	}, recordsPrincipal(t)); err == nil {
		t.Fatalf("lifting with an empty hold id must fail (need the real id)")
	}
	// Read the hold id and lift for real.
	var holdID string
	if err := s.db.QueryRow(`SELECT id FROM evidence_holds WHERE object_id = ? AND kind = 'legal' AND lifted_at IS NULL`, fx.objectID).Scan(&holdID); err != nil {
		t.Fatalf("read hold id: %v", err)
	}
	if _, err := s.LiftHold(context.Background(), core.LiftHoldCommand{
		HoldID:    holdID,
		Reason:    "dispute resolved",
		RequestID: "req-lift-intent-window",
	}, recordsPrincipal(t)); err != nil {
		t.Fatalf("lift hold: %v", err)
	}
	converged, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           execID,
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("same-id retry after the hold lifts must converge: %v", err)
	}
	if !converged.Recovered || converged.Execution.State != core.PurgeExecutionCompleted {
		t.Fatalf("post-lift convergence = %+v, want a Recovered completed attempt", converged)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the bytes must be gone after the converged execution: %v", statErr)
	}
}

// TestVerifyObjectBytesPurgedExpectedAbsence freezes the verifier/doctor
// reconciliation: a purged object's missing bytes are the DOCUMENTED EXPECTED
// ABSENCE (core.ErrObjectBytesPurgedExpected — the WORM layer PASSES with the
// purged message, never a corruption finding), while missing bytes WITHOUT any
// purge authorization stay OBJECT_BYTES_MISSING corruption.
func TestVerifyObjectBytesPurgedExpectedAbsence(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	ctx := context.Background()

	// A plain stored object whose bytes vanish is STILL corruption (no purge
	// authorization).
	plain, err := s.StoreObject(ctx, objectInputForTest(t, []byte("verify-purged-absence-plain-0001")))
	if err != nil {
		t.Fatalf("store plain object: %v", err)
	}
	if err := os.Remove(objectBytesPath(t, s, plain.Object)); err != nil {
		t.Fatalf("remove plain object bytes: %v", err)
	}
	if err := s.VerifyObjectBytes(ctx, plain.Object.ObjectID); err == nil || !strings.Contains(err.Error(), objectErrBytesMissing) {
		t.Fatalf("missing bytes WITHOUT a purge authorization = %v, want OBJECT_BYTES_MISSING (integrity incident)", err)
	}

	// A purged object's missing bytes are the documented expected absence.
	fx := approvedPurgePipeline(t, s)
	if _, err := s.ExecutePurge(ctx, core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-000000001301",
	}, recordsPrincipal(t)); err != nil {
		t.Fatalf("execute purge: %v", err)
	}
	if _, err := os.Stat(objectBytesPath(t, s, fx.object)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("purged bytes must be gone: %v", err)
	}
	verifyErr := s.VerifyObjectBytes(ctx, fx.objectID)
	if verifyErr == nil || !errors.Is(verifyErr, core.ErrObjectBytesPurgedExpected) {
		t.Fatalf("purged missing bytes = %v, want the wrapped %q sentinel (expected absence, NOT corruption)", verifyErr, core.ErrObjectBytesPurgedExpected)
	}
	// The pure WORM layer maps the sentinel to a PASSED layer.
	if layer := core.VerifyObjectBytesIntegrity(verifyErr); layer.Status != core.VerificationPassed {
		t.Fatalf("WORM layer for a purged object = %s, want passed (expected absence)", layer.Status)
	}
	if layer := core.VerifyObjectBytesIntegrity(fmt.Errorf("%s: %w", fx.objectID, core.ErrObjectBytesPurgedExpected)); layer.Status != core.VerificationPassed {
		t.Fatalf("WORM layer with a wrapped sentinel = %s, want passed", layer.Status)
	}

	// The doctor does NOT fail closed on the purged object's missing bytes (the
	// report builds), while the plain object's missing bytes keep failing closed.
	if _, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine}); err == nil || !strings.Contains(err.Error(), "OBJECT_BYTES_MISSING") {
		t.Fatalf("doctor with an unauthorized missing byte file = %v, want OBJECT_BYTES_MISSING (fail closed)", err)
	}

	// A store with ONLY a purged object reports cleanly (expected absence is not
	// corruption). Isolation is WORM-safe: the plain object's immutable
	// evidence_objects metadata is NEVER deleted or mutated — the purged-only
	// state is proven in a FRESH store whose object bytes are removed only
	// through the approved execution path (the physical byte file, never the
	// metadata).
	if _, ok := s.EvidenceObjectByID(ctx, plain.Object.ObjectID); !ok {
		t.Fatal("the plain object's immutable metadata row must survive the isolation (never deleted)")
	}
	iso := newTestStore(t)
	iso.SetReceiptSigner(newParitySigner(iso))
	isoFx := approvedPurgePipeline(t, iso)
	if _, err := iso.ExecutePurge(ctx, core.ExecutePurgeCommand{
		RequestID:             isoFx.request.RequestID,
		ExpectedLifecycleHash: isoFx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-000000001302",
	}, recordsPrincipal(t)); err != nil {
		t.Fatalf("execute purge on the isolated store: %v", err)
	}
	if _, err := iso.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine}); err != nil {
		t.Fatalf("doctor with only a purged object must succeed (documented expected absence): %v", err)
	}
}

// TestExecutePurgeWithdrawalInIntentWindowAbortsNoUnlink freezes the phase-2
// revalidation contract against a withdrawal racing the intent→completion window
// (design §3.7/§11 — the executeAndCompleteOn step-1 re-read: the request must
// STILL be 'approved'; a withdraw committed in the window aborts the completion,
// the interrupted intent is surfaced, and NOTHING is unlinked).
//
// The window is reproduced DETERMINISTICALLY through the existing controlled
// seam, never with sleeps or races:
//
//  1. corrupt the bytes → ExecutePurge commits the durable intent (executions row
//     'intent', purge_intent event + receipt, idempotency key reserved-incomplete)
//     and aborts the unlink (OBJECT_HASH_MISMATCH) — the crash-BEFORE-unlink
//     persisted shape with the bytes still present;
//  2. restore the EXACT original bytes (the window state: bytes intact and
//     WORM-verifiable) and WITHDRAW the approval — the withdrawal lands in the
//     intent→completion window (request 'approved' → 'withdrawn', pipeline back
//     to stored);
//  3. the same-id retry re-runs the phase-2 revalidation under the bound intent:
//     the request re-read is no longer 'approved' → INVALID_TRANSITION abort.
//
// The test proves the abort leaves the bytes INTACT (the guarded unlink never
// runs), records NO bytes-purged completion receipt (no purge_executed event /
// receipt, no completion columns on the executions row, the idempotency key stays
// incomplete) and SURFACES the interrupted intent instead of hiding it.
func TestExecutePurgeWithdrawalInIntentWindowAbortsNoUnlink(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := approvedPurgePipeline(t, s)
	execID := "00000000-0000-4000-8000-000000001401"
	ctx := context.Background()
	original := []byte("purge-target-bytes-0123456789")
	path := objectBytesPath(t, s, fx.object)

	// Step 1: the durable intent commits, the unlink aborts (corrupt bytes) — the
	// attempt stays 'intent' with the bytes PRESENT (the crash-before-unlink
	// window).
	if err := os.WriteFile(path, []byte("CORRUPTED-BYTES-NOT-THE-ORIGINAL"), 0o600); err != nil {
		t.Fatalf("corrupt object bytes: %v", err)
	}
	if _, err := s.ExecutePurge(ctx, core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           execID,
	}, recordsPrincipal(t)); err == nil || !strings.Contains(err.Error(), objectErrHashMismatch) {
		t.Fatalf("intent attempt = %v, want OBJECT_HASH_MISMATCH (durable intent committed, unlink aborted)", err)
	}
	if got := executionState(t, s, execID); got != string(core.PurgeExecutionIntent) {
		t.Fatalf("attempt state after the aborted unlink = %q, want intent", got)
	}

	// Step 2: restore the EXACT original bytes (intact and verifiable again) and
	// WITHDRAW the approval — the withdrawal commits inside the intent→completion
	// window.
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("restore object bytes: %v", err)
	}
	if err := s.VerifyObjectBytes(ctx, fx.objectID); err != nil {
		t.Fatalf("restored bytes must re-hash to the immutable object id: %v", err)
	}
	withdrawn, err := s.WithdrawPurge(ctx, core.WithdrawPurgeCommand{
		RequestID:    fx.request.RequestID,
		Reason:       "human cleanup racing the execution window",
		RequestIDKey: "req-withdraw-intent-window",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("withdraw in the intent window: %v", err)
	}
	if withdrawn.Request.Status != core.PurgeRequestStatusWithdrawn {
		t.Fatalf("withdrawn request status = %s, want withdrawn", withdrawn.Request.Status)
	}

	// Step 3: the same-id retry converges the interrupted intent under its bound
	// authorization — the phase-2 revalidation RE-READS the request and aborts:
	// it is no longer 'approved'. The unlink NEVER runs.
	_, err = s.ExecutePurge(ctx, core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           execID,
	}, recordsPrincipal(t))
	if err == nil || !strings.Contains(err.Error(), auth.CodeInvalidTransition) {
		t.Fatalf("same-id retry after a window withdrawal = %v, want INVALID_TRANSITION (the phase-2 revalidation aborts)", err)
	}

	// The interrupted intent is SURFACED, never hidden: the attempt stays 'intent'
	// and carries NO completion columns.
	if got := executionState(t, s, execID); got != string(core.PurgeExecutionIntent) {
		t.Fatalf("attempt state after the aborted revalidation = %q, want intent (surfaced interrupted)", got)
	}
	var completedAt, completedBy, completionReceiptID any
	if err := s.db.QueryRow(`SELECT completed_at, completed_by, completion_receipt_id FROM evidence_purge_executions WHERE execution_id = ?`, execID).Scan(&completedAt, &completedBy, &completionReceiptID); err != nil {
		t.Fatalf("read execution completion columns: %v", err)
	}
	if completedAt != nil || completedBy != nil || completionReceiptID != nil {
		t.Fatalf("an aborted completion must record NO completion columns, got (%v, %v, %v)", completedAt, completedBy, completionReceiptID)
	}

	// No bytes-purged completion receipt, event or request flip was ever written:
	// the idempotency key stays reserved-incomplete (the completion never claimed)
	// and the object chain has NO purge_executed artifact.
	var storedEntityID sql.NullString
	if err := s.db.QueryRow(`SELECT entity_id FROM evidence_purge_idempotency_keys WHERE tenant_id = ? AND request_id = ?`, fx.scope.OrganizationID, execID).Scan(&storedEntityID); err != nil {
		t.Fatalf("read execution idempotency key: %v", err)
	}
	if storedEntityID.Valid {
		t.Fatal("an aborted completion must leave the execution idempotency key incomplete (entity_id NULL)")
	}
	var executedEvents, executedReceipts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedEvents); err != nil {
		t.Fatalf("count executed events: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM receipts WHERE subject_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedReceipts); err != nil {
		t.Fatalf("count executed receipts: %v", err)
	}
	if executedEvents != 0 || executedReceipts != 0 {
		t.Fatalf("an aborted completion must record NO bytes-purged receipt: (%d events, %d receipts)", executedEvents, executedReceipts)
	}

	// The bytes are INTACT: the physical unlink never ran, the bytes re-hash to
	// the immutable content address and the request stays withdrawn (a fresh
	// execution id on the withdrawn pipeline is INVALID_TRANSITION too).
	if err := s.VerifyObjectBytes(ctx, fx.objectID); err != nil {
		t.Fatalf("the bytes must be intact after the aborted revalidation: %v", err)
	}
	if _, err := s.ExecutePurge(ctx, core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-000000001402",
	}, recordsPrincipal(t)); err == nil || !strings.Contains(err.Error(), auth.CodeInvalidTransition) {
		t.Fatalf("a fresh execution id on the withdrawn pipeline = %v, want INVALID_TRANSITION", err)
	}
}
