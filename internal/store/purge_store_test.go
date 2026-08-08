// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the interrupted-
// reservation contract of the request-purge idempotency family (design §9,
// schema v11): evidence_purge_idempotency_keys reserves a (tenant, requestId)
// key with NULL entity_id/result_json, so a crash between reservation and
// completion leaves NULLs behind. Every replay/read path must scan those
// columns NULLABLE and surface the TYPED IDEMPOTENCY_CONFLICT incomplete
// outcome (or PURGE_EXECUTION_INTERRUPTED in the executor) — never a database
// scan error — and write nothing (fail closed).
package store

import (
	"context"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// assertPurgeRequestSideEffectFree asserts that no request row, lifecycle event,
// projection row or PURGE MUTATION RECEIPT exists for the object (the interrupted
// replay wrote NOTHING — fail closed). The setup's content-addressed object_stored
// receipt is a legitimate fixture artifact on the SAME subject chain, so the
// receipt check filters to the eight v0.9 evidence-lifecycle acts a purge mutation
// could emit — proving NO purge mutation receipt was written without tripping on
// the setup receipt.
func assertPurgeRequestSideEffectFree(t *testing.T, s *SQLiteStore, objectID string) {
	t.Helper()
	type sideEffectCheck struct {
		table string
		where string
	}
	for _, check := range []sideEffectCheck{
		{"evidence_purge_requests", "object_id = ?"},
		{"evidence_lifecycle_events", "object_id = ?"},
		{"evidence_retention_state", "object_id = ?"},
		// The setup emits exactly ONE object_stored receipt on the object chain;
		// it is excluded so the assertion proves no purge mutation receipt exists
		// (retention_bound plus the seven purge-transition acts).
		{"receipts", "subject_id = ? AND action IN ('retention_bound','purge_requested','purge_approved','purge_rejected','purge_cancelled','purge_withdrawn','purge_intent','purge_executed')"},
	} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+check.table+` WHERE `+check.where, objectID).Scan(&count); err != nil {
			t.Fatalf("count %s rows: %v", check.table, err)
		}
		if count != 0 {
			t.Fatalf("interrupted request reservation wrote %d %s row(s), want 0", count, check.table)
		}
	}
}

// TestRequestPurgeInterruptedReservationIsConflictNotScanError freezes the
// interrupted-reservation contract of RequestPurge: a (tenant, requestId) key
// reserved with NULL entity_id/result_json (a crash between reservation and
// completion) replays as the TYPED IDEMPOTENCY_CONFLICT "reservation never
// completed" outcome — never a database scan error — and writes nothing.
func TestRequestPurgeInterruptedReservationIsConflictNotScanError(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	ctx := context.Background()
	scope := testScope(testRucA)

	objResult, err := s.StoreObject(ctx, objectInputForTest(t, []byte("purge-request-interrupted-bytes")))
	if err != nil {
		t.Fatalf("store purge request target object: %v", err)
	}
	objectID := objResult.Object.ObjectID

	polResult, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.Scope = companyPolicyScope()
		cmd.MinPeriod = "202401"
		cmd.RequestID = "req-policy-request-interrupted"
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("put retention policy: %v", err)
	}
	policy := polResult.Policy

	_, currentHash, err := currentPurgeSnapshotOn(ctx, s.db, objectID, scope, policy.BlockingHoldKinds)
	if err != nil {
		t.Fatalf("read pre-request lifecycle snapshot: %v", err)
	}

	principal := subjectPrincipal(t, "subject-2", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard)
	cmd := core.RequestPurgeCommand{
		ObjectID:              objectID,
		Jurisdiction:          "PE",
		Legislation:           "NATIONAL-TAX",
		Category:              "invoice",
		ExpectedLifecycleHash: currentHash,
		Reason:                "retention period elapsed",
		RequestID:             "req-purge-interrupted-001",
	}
	commandHash := purgeCommandHash(requestPurgeCommandShape{
		ObjectID: cmd.ObjectID, Jurisdiction: cmd.Jurisdiction, Legislation: cmd.Legislation,
		Category: cmd.Category, ExpectedLifecycleHash: cmd.ExpectedLifecycleHash, Reason: cmd.Reason,
	})
	// Simulate the crash state: the key was reserved (entity_id/result_json
	// NULL) but the request never completed.
	if _, err := s.db.Exec(`
		INSERT INTO evidence_purge_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, entity_id, result_json, created_at, completed_at)
		VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
		scope.OrganizationID, cmd.RequestID, commandHash, principal.SubjectID(), testT,
	); err != nil {
		t.Fatalf("insert interrupted request reservation: %v", err)
	}

	_, err = s.RequestPurge(ctx, cmd, principal)
	if err == nil || !strings.Contains(err.Error(), auth.CodeIdempotencyConflict) || strings.Contains(err.Error(), "persistence error") {
		t.Fatalf("replaying an interrupted request reservation = %v, want the typed IDEMPOTENCY_CONFLICT (never a scan error)", err)
	}
	assertPurgeRequestSideEffectFree(t, s, objectID)
}

// TestRejectPurgeInterruptedReservationReplayIsConflictNotScanError freezes the
// interrupted-reservation REPLAY contract of the closing transitions (the shared
// replayPurgeIdempotencyOn read — reject/cancel/withdraw): a reserved-but-never-
// completed key replays as the typed IDEMPOTENCY_CONFLICT incomplete outcome —
// never a database scan error — and the open pipeline stays untouched.
func TestRejectPurgeInterruptedReservationReplayIsConflictNotScanError(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	ctx := context.Background()
	scope := testScope(testRucA)

	objResult, err := s.StoreObject(ctx, objectInputForTest(t, []byte("purge-reject-interrupted-bytes")))
	if err != nil {
		t.Fatalf("store purge reject target object: %v", err)
	}
	objectID := objResult.Object.ObjectID

	polResult, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.Scope = companyPolicyScope()
		cmd.MinPeriod = "202401"
		cmd.RequestID = "req-policy-reject-interrupted"
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("put retention policy: %v", err)
	}
	policy := polResult.Policy

	_, currentHash, err := currentPurgeSnapshotOn(ctx, s.db, objectID, scope, policy.BlockingHoldKinds)
	if err != nil {
		t.Fatalf("read pre-request lifecycle snapshot: %v", err)
	}

	reqResult, err := s.RequestPurge(ctx, core.RequestPurgeCommand{
		ObjectID:              objectID,
		Jurisdiction:          "PE",
		Legislation:           "NATIONAL-TAX",
		Category:              "invoice",
		ExpectedLifecycleHash: currentHash,
		Reason:                "retention period elapsed",
		RequestID:             "req-purge-reject-fixture",
	}, subjectPrincipal(t, "subject-2", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
	if err != nil {
		t.Fatalf("request purge: %v", err)
	}
	request := reqResult.Request
	approver := recordsPrincipal(t)

	rejectCmd := core.RejectPurgeCommand{
		RequestID:    request.RequestID,
		Reason:       "reject after interrupted reservation",
		RequestIDKey: "req-reject-interrupted-001",
	}
	commandHash := purgeCommandHash(rejectPurgeCommandShape{RequestID: rejectCmd.RequestID, Reason: rejectCmd.Reason})
	// Simulate the crash state: the closing transition's key was reserved
	// (entity_id/result_json NULL) but the decision never completed.
	if _, err := s.db.Exec(`
		INSERT INTO evidence_purge_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, entity_id, result_json, created_at, completed_at)
		VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
		request.TenantID, rejectCmd.RequestIDKey, commandHash, approver.SubjectID(), testT,
	); err != nil {
		t.Fatalf("insert interrupted reject reservation: %v", err)
	}

	if _, err := s.RejectPurge(ctx, rejectCmd, approver); err == nil || !strings.Contains(err.Error(), auth.CodeIdempotencyConflict) || strings.Contains(err.Error(), "persistence error") {
		t.Fatalf("replaying an interrupted reject reservation = %v, want the typed IDEMPOTENCY_CONFLICT (never a scan error)", err)
	}

	// Fail closed: no decision row, event or receipt was ever written, and the
	// open pipeline stays 'requested'.
	for _, check := range []struct {
		table string
		where string
		arg   string
	}{
		{"evidence_purge_approvals", "request_id = ?", request.RequestID},
		{"evidence_lifecycle_events", "object_id = ? AND action = 'purge_rejected'", objectID},
		{"receipts", "subject_id = ? AND action = 'purge_rejected'", objectID},
	} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+check.table+` WHERE `+check.where, check.arg).Scan(&count); err != nil {
			t.Fatalf("count %s rows: %v", check.table, err)
		}
		if count != 0 {
			t.Fatalf("interrupted reject replay wrote %d %s row(s), want 0", count, check.table)
		}
	}
	after, err := purgeRequestByIDOn(ctx, s.db, request.RequestID)
	if err != nil {
		t.Fatalf("re-read purge request: %v", err)
	}
	if after.Status != core.PurgeRequestStatusRequested {
		t.Fatalf("request status after interrupted reject replay = %s, want requested", after.Status)
	}
}
