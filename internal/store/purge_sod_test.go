// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the PERSISTED
// separation-of-duties boundary of ApprovePurge (design §8.2 check-order steps 8
// and 10; store-side enforcement in authorizePurgeAct against the STORED request
// and approval rows — the pure policy alone is already proven in
// internal/authz). Two end-to-end proofs the authz suite cannot give:
//
//   - the principal recorded on the evidence_purge_requests row (requested_by)
//     can never approve their OWN purge request — the denial is APPROVER_IS_REQUESTER
//     and it fires against the STORED requester binding, even when the same human
//     carries the approver role through the role matrix;
//   - for a policy-designated dual-approval category, the SAME principal who
//     supplied the FIRST stored approval can never supply the required SECOND
//     approval — the denial is SAME_PRINCIPAL_SECOND_APPROVAL and it fires
//     against the STORED first-approval ledger row (subject id), not a pure
//     policy claim.
//
// Both denials fail closed: no approval row, no purge_approved event, no approval
// receipt and no status flip are ever written; the open pipeline stays untouched.
package store

import (
	"context"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// assertApprovalSoDDenied asserts the fail-closed shape of a denied approval act:
// exactly wantApprovalRows approval decision rows, wantApprovedEvents
// purge_approved events and wantApprovedReceipts purge_approved receipts exist for
// the pipeline, and the request status is still wantStatus (the denial wrote
// NOTHING that could advance or close the pipeline).
func assertApprovalSoDDenied(t *testing.T, s *SQLiteStore, objectID, requestID string, wantApprovalRows, wantApprovedEvents, wantApprovedReceipts int, wantStatus core.PurgeRequestStatus) {
	t.Helper()
	ctx := context.Background()

	var approvalRows, approvedEvents, approvedReceipts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_purge_approvals WHERE request_id = ?`, requestID).Scan(&approvalRows); err != nil {
		t.Fatalf("count approval rows: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_approved'`, objectID).Scan(&approvedEvents); err != nil {
		t.Fatalf("count purge_approved events: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM receipts WHERE subject_id = ? AND action = 'purge_approved'`, objectID).Scan(&approvedReceipts); err != nil {
		t.Fatalf("count purge_approved receipts: %v", err)
	}
	if approvalRows != wantApprovalRows || approvedEvents != wantApprovedEvents || approvedReceipts != wantApprovedReceipts {
		t.Fatalf("denied approval side effects = (%d approvals, %d events, %d receipts), want (%d, %d, %d)",
			approvalRows, approvedEvents, approvedReceipts, wantApprovalRows, wantApprovedEvents, wantApprovedReceipts)
	}
	request, err := purgeRequestByIDOn(ctx, s.db, requestID)
	if err != nil {
		t.Fatalf("re-read purge request: %v", err)
	}
	if request.Status != wantStatus {
		t.Fatalf("request status after the denied approval = %s, want %s (the denial must not advance or close the pipeline)", request.Status, wantStatus)
	}
}

// TestPurgeApproveStoreSideSeparationOfDuties freezes the two persisted SoD
// denials of ApprovePurge end-to-end through the store (sqlite, request row +
// approval ledger + receipts):
//
//   - requester-cannot-approve: the human recorded as requested_by on the stored
//     request row attempts to approve it with an approver role — the denial is
//     APPROVER_IS_REQUESTER, read from the PERSISTED requester binding;
//   - same-principal-cannot-supply-the-second-approval: after a stored first
//     approval on a dual-approval category, the same subject id (with the dual
//     controller role) attempts the second approval — the denial is
//     SAME_PRINCIPAL_SECOND_APPROVAL, read from the PERSISTED first-approval row.
func TestPurgeApproveStoreSideSeparationOfDuties(t *testing.T) {
	ctx := context.Background()

	t.Run("requester cannot approve own request", func(t *testing.T) {
		s := newTestStore(t)
		s.SetReceiptSigner(newParitySigner(s))
		scope := testScope(testRucA)

		objResult, err := s.StoreObject(ctx, objectInputForTest(t, []byte("purge-sod-requester-bytes")))
		if err != nil {
			t.Fatalf("store purge target object: %v", err)
		}
		objectID := objResult.Object.ObjectID

		polResult, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
			cmd.Scope = companyPolicyScope() // the EXACT company scope of the object
			cmd.MinPeriod = "202401"
			cmd.RequestID = "req-policy-sod-requester"
		}, recordsPrincipal(t))
		if err != nil {
			t.Fatalf("put retention policy: %v", err)
		}
		policy := polResult.Policy

		_, currentHash, err := currentPurgeSnapshotOn(ctx, s.db, objectID, scope, policy.BlockingHoldKinds)
		if err != nil {
			t.Fatalf("read pre-request lifecycle snapshot: %v", err)
		}

		// The requester carries BOTH the accountant ladder role (to open the
		// pipeline) AND the records_compliance_officer approver role (to pass the
		// approve role matrix) — so the denial MUST come from the STORED requester
		// binding (approver == requester), never from a role denial.
		requester := subjectPrincipal(t, "subject-sod-requester",
			[]auth.AccountingRole{auth.RoleAccountant, auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard)
		reqResult, err := s.RequestPurge(ctx, core.RequestPurgeCommand{
			ObjectID:              objectID,
			Jurisdiction:          "PE",
			Legislation:           "NATIONAL-TAX",
			Category:              "invoice",
			ExpectedLifecycleHash: currentHash,
			Reason:                "retention period elapsed",
			RequestID:             "req-purge-sod-requester",
		}, requester)
		if err != nil {
			t.Fatalf("request purge: %v", err)
		}
		request := reqResult.Request
		h2Request := core.ComputeLifecycleSnapshotHash(assembleSnapshot(objectID, scope, core.PurgeLifecycleRequested,
			core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
			[]core.LifecycleHoldRef{}, request.RequestID, []string{}))

		// The approver IS the stored requester (identical subject id): the approval
		// must fail with APPROVER_IS_REQUESTER — the persisted request row is the
		// authority, never a caller-declared claim.
		_, err = s.ApprovePurge(ctx, core.ApprovePurgeCommand{
			RequestID:             request.RequestID,
			ExpectedLifecycleHash: h2Request,
			Reason:                "must be denied: the requester approves its own purge",
			RequestIDKey:          "req-approve-sod-requester",
		}, requester)
		if err == nil || !strings.Contains(err.Error(), auth.CodeApproverIsRequester) {
			t.Fatalf("requester approving its own request = %v, want APPROVER_IS_REQUESTER (stored requester binding)", err)
		}
		assertApprovalSoDDenied(t, s, objectID, request.RequestID, 0, 0, 0, core.PurgeRequestStatusRequested)
	})

	t.Run("same principal cannot supply the dual second approval", func(t *testing.T) {
		s := newTestStore(t)
		s.SetReceiptSigner(newParitySigner(s))
		scope := testScope(testRucA)

		objResult, err := s.StoreObject(ctx, objectInputForTest(t, []byte("purge-sod-dual-bytes")))
		if err != nil {
			t.Fatalf("store purge target object: %v", err)
		}
		objectID := objResult.Object.ObjectID

		polResult, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
			cmd.Scope = companyPolicyScope()
			cmd.MinPeriod = "202401"
			cmd.DualApprovalRequired = true // policy-designated dual-approval category
			cmd.RequestID = "req-policy-sod-dual"
		}, recordsPrincipal(t))
		if err != nil {
			t.Fatalf("put dual-approval retention policy: %v", err)
		}
		policy := polResult.Policy
		if !policy.DualApprovalRequired {
			t.Fatal("fixture policy must be dual-approval configured")
		}

		_, currentHash, err := currentPurgeSnapshotOn(ctx, s.db, objectID, scope, policy.BlockingHoldKinds)
		if err != nil {
			t.Fatalf("read pre-request lifecycle snapshot: %v", err)
		}

		requester := subjectPrincipal(t, "subject-sod-dual-requester",
			[]auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard)
		reqResult, err := s.RequestPurge(ctx, core.RequestPurgeCommand{
			ObjectID:              objectID,
			Jurisdiction:          "PE",
			Legislation:           "NATIONAL-TAX",
			Category:              "invoice",
			ExpectedLifecycleHash: currentHash,
			Reason:                "retention period elapsed",
			RequestID:             "req-purge-sod-dual",
		}, requester)
		if err != nil {
			t.Fatalf("request purge: %v", err)
		}
		request := reqResult.Request
		h2Request := core.ComputeLifecycleSnapshotHash(assembleSnapshot(objectID, scope, core.PurgeLifecycleRequested,
			core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
			[]core.LifecycleHoldRef{}, request.RequestID, []string{}))

		// First approval by a distinct default approver — persisted in the ledger.
		first := subjectPrincipal(t, "subject-sod-first-approver",
			[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard)
		firstResult, err := s.ApprovePurge(ctx, core.ApprovePurgeCommand{
			RequestID:             request.RequestID,
			ExpectedLifecycleHash: h2Request,
			Reason:                "first approval of the dual pipeline",
			RequestIDKey:          "req-approve-sod-first",
		}, first)
		if err != nil {
			t.Fatalf("first approval: %v", err)
		}
		if firstResult.Request.Status != core.PurgeRequestStatusRequested {
			t.Fatalf("dual pipeline after the first approval = %s, want requested (not yet executable)", firstResult.Request.Status)
		}

		// The SAME subject id (now carrying the dual controller role) attempts the
		// required SECOND approval: the denial must be SAME_PRINCIPAL_SECOND_APPROVAL
		// against the STORED first-approval row — never a pure-policy claim.
		same := subjectPrincipal(t, "subject-sod-first-approver",
			[]auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard)
		_, err = s.ApprovePurge(ctx, core.ApprovePurgeCommand{
			RequestID:             request.RequestID,
			ExpectedLifecycleHash: firstResult.Approval.ResultingHash,
			Reason:                "must be denied: the first approver supplies the second approval",
			RequestIDKey:          "req-approve-sod-second",
		}, same)
		if err == nil || !strings.Contains(err.Error(), auth.CodeSamePrincipalSecondApproval) {
			t.Fatalf("same principal supplying the second approval = %v, want SAME_PRINCIPAL_SECOND_APPROVAL (stored first-approval ledger)", err)
		}
		// Fail closed: exactly ONE stored approval (the first), one purge_approved
		// event and receipt, and the pipeline stays OPEN at 'requested' — the
		// denied second act wrote nothing.
		assertApprovalSoDDenied(t, s, objectID, request.RequestID, 1, 1, 1, core.PurgeRequestStatusRequested)
	})
}
