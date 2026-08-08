// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module freezes the PURE purge-lifecycle model (v0.8 batch 4
// — docs/architecture/evidence-lifecycle-v0.8.md §2/§3/§9; schema v11):
//
//   - the CLOSED token sets: the per-object lifecycle state machine
//     (stored → purge_requested → purge_approved → purged, terminal
//     purge_rejected, reversible cancel/withdraw), the guarded request status,
//     the immutable approval decision tokens and the closed event-action set
//     (the execution actions purge_intent/purge_executed are frozen but WU-1
//     never emits them);
//   - the fail-closed command validators (request/approve/reject/cancel/
//     withdraw) and the stored-request validator, each rejecting malformed
//     input (invalid ids/hashes/scopes/tokens/completion);
//   - the canonical lifecycle snapshot byte contract (§3.8): FIXED property
//     order, compact UTF-8 JSON, NO HTML escaping, holds sorted by id (empty
//     array never null), approval ids sorted ascending and omitted when none,
//     and the deterministic lowercase SHA-256 hex H1/H2 over those bytes.
package core

import (
	"strings"
	"testing"
)

// sampleLifecycleSnapshot is the canonicalization fixture: a fully-populated
// stored snapshot with an active-blocking-hold list passed UNSORTED (the
// canonicalizer sorts it) and approval ids passed UNSORTED (canonical ascending).
func sampleLifecycleSnapshot() LifecycleSnapshot {
	return LifecycleSnapshot{
		ObjectID:       strings.Repeat("a", 64),
		TenantID:       "org-001",
		CompanyID:      "acme",
		RUC:            "20100039201",
		Period:         "202401",
		LifecycleState: PurgeLifecycleStored,
		RetentionState: RetentionEligibilityEligible,
		PolicyID:       "00000000-0000-4000-8000-000000000001",
		PolicyVersion:  3,
		Category:       "invoice",
		BlockingHolds: []LifecycleHoldRef{
			{HoldID: "00000000-0000-4000-8000-000000000020", Kind: "audit", PlacedAt: "2026-08-07T12:00:00.000Z"},
			{HoldID: "00000000-0000-4000-8000-000000000010", Kind: "legal", PlacedAt: "2026-08-01T09:00:00.000Z"},
		},
		RequestID:   "00000000-0000-4000-8000-000000000030",
		ApprovalIDs: []string{"00000000-0000-4000-8000-000000000041", "00000000-0000-4000-8000-000000000040"},
	}
}

// pinnedCanonicalLifecycleSnapshotJSON is the FROZEN canonical bytes of
// sampleLifecycleSnapshot (§3.8): FIXED property order, holds sorted by id,
// approval ids sorted ascending, policyVersion as a JSON integer. The pinned
// SHA-256 hex of these bytes (ComputeLifecycleSnapshotHash) is the deterministic
// H1/H2 contract the store fails closed on (LIFECYCLE_VERSION_MISMATCH).
const pinnedCanonicalLifecycleSnapshotJSON = `{"objectId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tenantId":"org-001","companyId":"acme","ruc":"20100039201","period":"202401","lifecycleState":"stored","retentionState":"eligible","policyId":"00000000-0000-4000-8000-000000000001","policyVersion":3,"category":"invoice","blockingHolds":[{"id":"00000000-0000-4000-8000-000000000010","kind":"legal","placedAt":"2026-08-01T09:00:00.000Z"},{"id":"00000000-0000-4000-8000-000000000020","kind":"audit","placedAt":"2026-08-07T12:00:00.000Z"}],"requestId":"00000000-0000-4000-8000-000000000030","approvalIds":["00000000-0000-4000-8000-000000000040","00000000-0000-4000-8000-000000000041"]}`

// pinnedLifecycleSnapshotHashHex is the lowercase SHA-256 hex of the pinned
// canonical bytes above — the frozen H1/H2 digest of the fixture snapshot.
const pinnedLifecycleSnapshotHashHex = "2be1d394a5aedf0498867edf514f07349cd176e79f0696fb66bbf71b286415ec"

func TestPurgeLifecycleClosedVocabularies(t *testing.T) {
	// The model version is the frozen snapshot/state contract version.
	if PurgeLifecycleModelVersion != "purge-lifecycle/v0.8.0" {
		t.Errorf("PurgeLifecycleModelVersion = %q, want purge-lifecycle/v0.8.0", PurgeLifecycleModelVersion)
	}

	// Lifecycle states: every closed token accepted, anything else rejected.
	for _, state := range []PurgeLifecycleState{
		PurgeLifecycleStored, PurgeLifecycleRequested, PurgeLifecycleApproved,
		PurgeLifecycleRejected, PurgeLifecyclePurged,
	} {
		if !IsValidPurgeLifecycleState(state) {
			t.Errorf("IsValidPurgeLifecycleState(%q) = false, want true", state)
		}
	}
	for _, rogue := range []PurgeLifecycleState{"", "purge_pending", "deleted", "PURGED"} {
		if IsValidPurgeLifecycleState(rogue) {
			t.Errorf("IsValidPurgeLifecycleState(%q) accepted an unknown token — the state set is closed", rogue)
		}
	}

	// Request statuses.
	for _, status := range []PurgeRequestStatus{
		PurgeRequestStatusRequested, PurgeRequestStatusApproved, PurgeRequestStatusRejected,
		PurgeRequestStatusWithdrawn, PurgeRequestStatusCancelled, PurgeRequestStatusExecuted,
	} {
		if !IsValidPurgeRequestStatus(status) {
			t.Errorf("IsValidPurgeRequestStatus(%q) = false, want true", status)
		}
	}
	for _, rogue := range []PurgeRequestStatus{"", "queued", "in_review"} {
		if IsValidPurgeRequestStatus(rogue) {
			t.Errorf("IsValidPurgeRequestStatus(%q) accepted an unknown token — the status set is closed", rogue)
		}
	}

	// Approval decision tokens.
	for _, decision := range []PurgeApprovalDecision{
		PurgeApprovalDecisionApproved, PurgeApprovalDecisionRejected, PurgeApprovalDecisionWithdrawn,
	} {
		if !IsValidPurgeApprovalDecision(decision) {
			t.Errorf("IsValidPurgeApprovalDecision(%q) = false, want true", decision)
		}
	}
	for _, rogue := range []PurgeApprovalDecision{"", "maybe", "APPROVED"} {
		if IsValidPurgeApprovalDecision(rogue) {
			t.Errorf("IsValidPurgeApprovalDecision(%q) accepted an unknown token — the decision set is closed", rogue)
		}
	}

	// Lifecycle event actions: the frozen closed set INCLUDES the execution
	// actions (WU-1 never emits them — they are schema-frozen for WU-2).
	for _, action := range []PurgeLifecycleEventAction{
		PurgeEventRetentionBound, PurgeEventRequested, PurgeEventApproved, PurgeEventRejected,
		PurgeEventCancelled, PurgeEventWithdrawn, PurgeEventIntent, PurgeEventExecuted,
	} {
		if !IsValidPurgeLifecycleEventAction(action) {
			t.Errorf("IsValidPurgeLifecycleEventAction(%q) = false, want true", action)
		}
	}
	for _, rogue := range []PurgeLifecycleEventAction{"", "purge_paused", "hold_placed"} {
		if IsValidPurgeLifecycleEventAction(rogue) {
			t.Errorf("IsValidPurgeLifecycleEventAction(%q) accepted an unknown token — the action set is closed", rogue)
		}
	}
}

func TestAssertValidRequestPurgeCommand(t *testing.T) {
	valid := RequestPurgeCommand{
		ObjectID:              strings.Repeat("c", 64),
		Jurisdiction:          "PE",
		Legislation:           "NATIONAL-TAX",
		Category:              "invoice",
		ExpectedLifecycleHash: strings.Repeat("d", 64),
		Reason:                "retention period elapsed per the exact active policy",
		RequestID:             "req-1",
	}
	if err := AssertValidRequestPurgeCommand(valid); err != nil {
		t.Fatalf("valid request command rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*RequestPurgeCommand)
	}{
		{"object id", func(c *RequestPurgeCommand) { c.ObjectID = "not-hex" }},
		{"jurisdiction", func(c *RequestPurgeCommand) { c.Jurisdiction = "pe" }},
		{"legislation", func(c *RequestPurgeCommand) { c.Legislation = "   " }},
		{"category", func(c *RequestPurgeCommand) { c.Category = "" }},
		{"expected lifecycle hash", func(c *RequestPurgeCommand) { c.ExpectedLifecycleHash = "DEADBEEF" }},
		{"reason", func(c *RequestPurgeCommand) { c.Reason = " " }},
		{"requestId idempotency key", func(c *RequestPurgeCommand) { c.RequestID = "" }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cmd := valid
			tt.mutate(&cmd)
			if err := AssertValidRequestPurgeCommand(cmd); err == nil {
				t.Fatalf("request command with invalid %s accepted", tt.name)
			}
		})
	}
}

func TestAssertValidApprovePurgeCommand(t *testing.T) {
	valid := ApprovePurgeCommand{
		RequestID:             "00000000-0000-4000-8000-000000000005",
		ExpectedLifecycleHash: strings.Repeat("e", 64),
		Reason:                "verified against the reviewed lifecycle snapshot",
		RequestIDKey:          "req-key-1",
	}
	if err := AssertValidApprovePurgeCommand(valid); err != nil {
		t.Fatalf("valid approve command rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*ApprovePurgeCommand)
	}{
		{"request id", func(c *ApprovePurgeCommand) { c.RequestID = "not-a-uuid" }},
		{"expected lifecycle hash", func(c *ApprovePurgeCommand) { c.ExpectedLifecycleHash = "short" }},
		{"reason", func(c *ApprovePurgeCommand) { c.Reason = "" }},
		{"requestIdKey idempotency key", func(c *ApprovePurgeCommand) { c.RequestIDKey = "  " }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cmd := valid
			tt.mutate(&cmd)
			if err := AssertValidApprovePurgeCommand(cmd); err == nil {
				t.Fatalf("approve command with invalid %s accepted", tt.name)
			}
		})
	}
}

func TestAssertValidRejectCancelWithdrawPurgeCommands(t *testing.T) {
	// Reject: a UUID request id + a REQUIRED reason + the idempotency key.
	reject := RejectPurgeCommand{
		RequestID:    "00000000-0000-4000-8000-000000000006",
		Reason:       "documentation incomplete",
		RequestIDKey: "req-key-2",
	}
	if err := AssertValidRejectPurgeCommand(reject); err != nil {
		t.Fatalf("valid reject command rejected: %v", err)
	}
	if err := AssertValidRejectPurgeCommand(RejectPurgeCommand{RequestID: "x", Reason: "ok", RequestIDKey: "k"}); err == nil {
		t.Fatal("reject command without a UUID request id accepted")
	}
	if err := AssertValidRejectPurgeCommand(RejectPurgeCommand{RequestID: "00000000-0000-4000-8000-000000000006", Reason: " ", RequestIDKey: "k"}); err == nil {
		t.Fatal("reject command without a reason accepted")
	}
	if err := AssertValidRejectPurgeCommand(RejectPurgeCommand{RequestID: "00000000-0000-4000-8000-000000000006", Reason: "ok", RequestIDKey: ""}); err == nil {
		t.Fatal("reject command without the idempotency key accepted")
	}

	// Cancel: a UUID request id + the idempotency key (no reason field).
	cancel := CancelPurgeCommand{
		RequestID:    "00000000-0000-4000-8000-000000000007",
		RequestIDKey: "req-key-3",
	}
	if err := AssertValidCancelPurgeCommand(cancel); err != nil {
		t.Fatalf("valid cancel command rejected: %v", err)
	}
	if err := AssertValidCancelPurgeCommand(CancelPurgeCommand{RequestID: "not-a-uuid", RequestIDKey: "k"}); err == nil {
		t.Fatal("cancel command without a UUID request id accepted")
	}
	if err := AssertValidCancelPurgeCommand(CancelPurgeCommand{RequestID: "00000000-0000-4000-8000-000000000007", RequestIDKey: ""}); err == nil {
		t.Fatal("cancel command without the idempotency key accepted")
	}

	// Withdraw: a UUID request id + a REQUIRED reason + the idempotency key.
	withdraw := WithdrawPurgeCommand{
		RequestID:    "00000000-0000-4000-8000-000000000008",
		Reason:       "approval retracted by the approver",
		RequestIDKey: "req-key-4",
	}
	if err := AssertValidWithdrawPurgeCommand(withdraw); err != nil {
		t.Fatalf("valid withdraw command rejected: %v", err)
	}
	if err := AssertValidWithdrawPurgeCommand(WithdrawPurgeCommand{RequestID: "x", Reason: "ok", RequestIDKey: "k"}); err == nil {
		t.Fatal("withdraw command without a UUID request id accepted")
	}
	if err := AssertValidWithdrawPurgeCommand(WithdrawPurgeCommand{RequestID: "00000000-0000-4000-8000-000000000008", Reason: "", RequestIDKey: "k"}); err == nil {
		t.Fatal("withdraw command without a reason accepted")
	}
	if err := AssertValidWithdrawPurgeCommand(WithdrawPurgeCommand{RequestID: "00000000-0000-4000-8000-000000000008", Reason: "ok", RequestIDKey: " "}); err == nil {
		t.Fatal("withdraw command without the idempotency key accepted")
	}
}

// samplePurgeRequest is a valid stored request row: exact company scope, a
// closed eligibility token, the 64-hex reviewed lifecycle hash and an open
// (requested) status with requester provenance.
func samplePurgeRequest() EvidencePurgeRequest {
	return EvidencePurgeRequest{
		RequestID:              "00000000-0000-4000-8000-000000000001",
		ObjectID:               strings.Repeat("a", 64),
		TenantID:               "org-001",
		CompanyID:              "acme",
		RUC:                    "20100039201",
		Period:                 "202401",
		Category:               "invoice",
		PolicyID:               "00000000-0000-4000-8000-000000000002",
		RetentionStateSnapshot: RetentionEligibilityEligible,
		ReviewedLifecycleHash:  strings.Repeat("b", 64),
		Status:                 PurgeRequestStatusRequested,
		RequestedAt:            "2026-08-07T12:00:00.000Z",
		RequestedBy:            "subject-1",
	}
}

func TestAssertValidPurgeRequest(t *testing.T) {
	if err := AssertValidPurgeRequest(samplePurgeRequest()); err != nil {
		t.Fatalf("valid stored request rejected: %v", err)
	}

	// A completed (non-cancelled) request must carry approved_at — the store
	// sets the completion columns together; a partially completed row is invalid
	// metadata.
	completed := samplePurgeRequest()
	completed.Status = PurgeRequestStatusApproved
	completed.ApprovedAt = "2026-08-08T09:00:00.000Z"
	if err := AssertValidPurgeRequest(completed); err != nil {
		t.Fatalf("valid completed request rejected: %v", err)
	}
	// Cancellation is the requester retraction and carries NO approved_at.
	cancelled := samplePurgeRequest()
	cancelled.Status = PurgeRequestStatusCancelled
	if err := AssertValidPurgeRequest(cancelled); err != nil {
		t.Fatalf("valid cancelled request rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*EvidencePurgeRequest)
	}{
		{"request id", func(r *EvidencePurgeRequest) { r.RequestID = "not-a-uuid" }},
		{"object id", func(r *EvidencePurgeRequest) { r.ObjectID = "not-hex" }},
		{"tenant id", func(r *EvidencePurgeRequest) { r.TenantID = "" }},
		{"company id", func(r *EvidencePurgeRequest) { r.CompanyID = "" }},
		{"ruc", func(r *EvidencePurgeRequest) { r.RUC = "123" }},
		{"period", func(r *EvidencePurgeRequest) { r.Period = "2024" }},
		{"category", func(r *EvidencePurgeRequest) { r.Category = " " }},
		{"policy id", func(r *EvidencePurgeRequest) { r.PolicyID = "not-a-uuid" }},
		{"retention snapshot", func(r *EvidencePurgeRequest) { r.RetentionStateSnapshot = "maybe" }},
		{"reviewed lifecycle hash", func(r *EvidencePurgeRequest) { r.ReviewedLifecycleHash = "ABCDEF" }},
		{"status", func(r *EvidencePurgeRequest) { r.Status = "queued" }},
		{"requestedBy", func(r *EvidencePurgeRequest) { r.RequestedBy = "" }},
		{"requestedAt", func(r *EvidencePurgeRequest) { r.RequestedAt = "not-a-date" }},
		{"approvedAt", func(r *EvidencePurgeRequest) {
			r.Status = PurgeRequestStatusApproved
			r.ApprovedAt = "not-a-date"
		}},
		{"completion metadata", func(r *EvidencePurgeRequest) {
			r.Status = PurgeRequestStatusRejected
			r.ApprovedAt = ""
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			req := samplePurgeRequest()
			tt.mutate(&req)
			if err := AssertValidPurgeRequest(req); err == nil {
				t.Fatalf("stored request with invalid %s accepted", tt.name)
			}
		})
	}
}

// TestCanonicalLifecycleSnapshotJSONPinned freezes the canonical lifecycle
// snapshot bytes (§3.8): FIXED property order (the struct order), JSON string
// escaping, NO HTML escaping, holds sorted by id (empty array, never null),
// approval ids sorted ascending and omitted when none, and omitempty optional
// fields (period/policyId/policyVersion/category/requestId) dropped when empty.
func TestCanonicalLifecycleSnapshotJSONPinned(t *testing.T) {
	got := string(CanonicalLifecycleSnapshotJSON(sampleLifecycleSnapshot()))
	if got != pinnedCanonicalLifecycleSnapshotJSON {
		t.Fatalf("canonical lifecycle snapshot bytes differ from the pinned literal:\n got %s\nwant %s", got, pinnedCanonicalLifecycleSnapshotJSON)
	}

	// Deterministic: the same snapshot canonicalizes byte-identically every time
	// and regardless of the caller's hold/approval ordering (the fixture passes
	// unsorted holds and approval ids).
	sorted := sampleLifecycleSnapshot()
	sorted.BlockingHolds = []LifecycleHoldRef{
		{HoldID: "00000000-0000-4000-8000-000000000010", Kind: "legal", PlacedAt: "2026-08-01T09:00:00.000Z"},
		{HoldID: "00000000-0000-4000-8000-000000000020", Kind: "audit", PlacedAt: "2026-08-07T12:00:00.000Z"},
	}
	sorted.ApprovalIDs = []string{"00000000-0000-4000-8000-000000000040", "00000000-0000-4000-8000-000000000041"}
	if again := string(CanonicalLifecycleSnapshotJSON(sorted)); again != pinnedCanonicalLifecycleSnapshotJSON {
		t.Fatalf("canonicalization depends on caller ordering — not canonical:\n got %s\nwant %s", again, pinnedCanonicalLifecycleSnapshotJSON)
	}

	// The minimal snapshot: NO holds, NO request, NO approvals, no optional
	// fields. blockingHolds must stay a present empty array (never null) while
	// requestId/approvalIds/period/policyId/policyVersion/category are omitted.
	minimal := LifecycleSnapshot{
		ObjectID:       strings.Repeat("a", 64),
		TenantID:       "org-001",
		CompanyID:      "acme",
		RUC:            "20100039201",
		LifecycleState: PurgeLifecycleStored,
		RetentionState: RetentionEligibilityUnknown,
	}
	minimalJSON := string(CanonicalLifecycleSnapshotJSON(minimal))
	if !strings.Contains(minimalJSON, `"blockingHolds":[]`) {
		t.Fatalf("empty holds must marshal as [] (never null): %s", minimalJSON)
	}
	for _, omitted := range []string{`"requestId"`, `"approvalIds"`, `"period"`, `"policyId"`, `"policyVersion"`, `"category"`} {
		if strings.Contains(minimalJSON, omitted) {
			t.Fatalf("empty optional field must be omitted (omitempty), found %q: %s", omitted, minimalJSON)
		}
	}

	// NO HTML escaping (Go escapes <,>,& by default — disabled to match the
	// receipt canonicalizers and the TypeScript mirror) and JSON string escaping
	// for quotes/backslashes.
	escaped := sampleLifecycleSnapshot()
	escaped.Category = `invoices & "specials"`
	escapedJSON := string(CanonicalLifecycleSnapshotJSON(escaped))
	if !strings.Contains(escapedJSON, `"category":"invoices & \"specials\""`) {
		t.Fatalf("canonical bytes must keep & raw and escape quotes: %s", escapedJSON)
	}
}

// TestComputeLifecycleSnapshotHashDeterministic pins the deterministic
// lowercase SHA-256 hex digest (H1/H2) of the canonical snapshot bytes and
// proves the hash fails closed on ANY drift: every snapshot field change
// produces a DIFFERENT digest, and the digest has the frozen 64-hex shape the
// store compares against (LIFECYCLE_VERSION_MISMATCH on drift).
func TestComputeLifecycleSnapshotHashDeterministic(t *testing.T) {
	base := ComputeLifecycleSnapshotHash(sampleLifecycleSnapshot())
	if base != pinnedLifecycleSnapshotHashHex {
		t.Fatalf("ComputeLifecycleSnapshotHash = %s, want the pinned digest %s", base, pinnedLifecycleSnapshotHashHex)
	}
	if base != ComputeLifecycleSnapshotHash(sampleLifecycleSnapshot()) {
		t.Fatal("ComputeLifecycleSnapshotHash is not deterministic for the same snapshot")
	}
	if len(base) != 64 {
		t.Fatalf("lifecycle snapshot hash length = %d, want 64 (the frozen SHA-256 shape)", len(base))
	}
	for i, r := range base {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("lifecycle snapshot hash contains non-lowercase-hex rune %q at %d", r, i)
		}
	}

	sensitive := []struct {
		name   string
		mutate func(*LifecycleSnapshot)
	}{
		{"object id", func(s *LifecycleSnapshot) { s.ObjectID = strings.Repeat("f", 64) }},
		{"tenant id", func(s *LifecycleSnapshot) { s.TenantID = "org-002" }},
		{"company id", func(s *LifecycleSnapshot) { s.CompanyID = "acme-2" }},
		{"ruc", func(s *LifecycleSnapshot) { s.RUC = "20600995804" }},
		{"period", func(s *LifecycleSnapshot) { s.Period = "202402" }},
		{"lifecycle state", func(s *LifecycleSnapshot) { s.LifecycleState = PurgeLifecycleRequested }},
		{"retention state", func(s *LifecycleSnapshot) { s.RetentionState = RetentionEligibilityNotDue }},
		{"policy id", func(s *LifecycleSnapshot) { s.PolicyID = "00000000-0000-4000-8000-000000000099" }},
		{"policy version", func(s *LifecycleSnapshot) { s.PolicyVersion = 4 }},
		{"category", func(s *LifecycleSnapshot) { s.Category = "receipt" }},
		{"blocking hold", func(s *LifecycleSnapshot) {
			s.BlockingHolds = append(s.BlockingHolds, LifecycleHoldRef{HoldID: "00000000-0000-4000-8000-000000000050", Kind: "dispute", PlacedAt: "2026-08-02T10:00:00.000Z"})
		}},
		{"request id", func(s *LifecycleSnapshot) { s.RequestID = "00000000-0000-4000-8000-000000000031" }},
		{"approval ids", func(s *LifecycleSnapshot) {
			s.ApprovalIDs = append(s.ApprovalIDs, "00000000-0000-4000-8000-000000000042")
		}},
	}
	for _, tt := range sensitive {
		t.Run(tt.name, func(t *testing.T) {
			s := sampleLifecycleSnapshot()
			tt.mutate(&s)
			if got := ComputeLifecycleSnapshotHash(s); got == base {
				t.Fatalf("changing %s must change the lifecycle snapshot hash", tt.name)
			}
		})
	}
}
