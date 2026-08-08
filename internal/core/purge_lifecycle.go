// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the PURE purge-lifecycle model of the v0.8
// evidence lifecycle (batch 4 — docs/architecture/evidence-lifecycle-v0.8.md
// §2/§3/§9; schema v11):
//
//   - the state machine is the frozen per-object purge pipeline
//     (stored → purge_requested → purge_approved → purged, terminal
//     purge_rejected, reversible cancel/withdraw — §2). Retention eligibility is
//     a separate dimension (retention_policy.go §6) and holds are separate
//     records (hold.go §7) that gate transitions; neither adds machine states;
//   - the request aggregate (evidence_purge_requests) is ONE open pipeline per
//     object (UNIQUE object_id — §3.3): only the guarded status advances through
//     recorded transitions; a cancelled/withdrawn pipeline returns the object to
//     `stored` and a fresh request is a fresh act on the SAME row (the retraction
//     path is the design's documented cleanup — §7);
//   - approvals are IMMUTABLE decision rows (evidence_purge_approvals, §3.4)
//     with a decision token (approved | rejected | withdrawn), a 1-based
//     approval_order (1 = default approver, 2 = the dual-approval second
//     approver for a policy-designated fiscal/material category), the reviewed
//     lifecycle hash H1 the human examined and the resulting hash H2 after the
//     transition (H2 always != H1 — the approval id and/or the state change is
//     part of the canonical snapshot);
//   - the lifecycle event log (evidence_lifecycle_events, §3.5) is the immutable
//     queryable source of truth; the guarded projection
//     (evidence_retention_state, §3.6) is derived queryable state, never a
//     separate authority;
//   - the canonical lifecycle snapshot hash (§3.8) covers, in FIXED canonical
//     JSON order: object id, exact scope tuple, lifecycle state, retention
//     state, policy id + version, category, the active blocking holds (id +
//     kind + placed_at, sorted by id) and the request/approval ids when present.
//     This hash is H1/H2: what the requester/approver examined and what each
//     transition produced; the store fails closed on any drift
//     (LIFECYCLE_VERSION_MISMATCH). The bytes are deterministic — compact UTF-8
//     JSON, fixed property order, NO HTML escaping.
//
// This module is PURE: no I/O, no store, no clock. It owns the closed model,
// the fail-closed validators and the canonical snapshot byte contract.
// Persistence, the authenticated gates (the extended evidence-lifecycle policy
// + store-side SoD against the stored requester), (tenant, requestId)
// idempotency, the retention binding + retention_bound receipt and the
// atomic event/projection/receipt transitions live in internal/store
// (purge_store.go). Physical execution (purge_executed, byte removal) and the
// executions protocol (§11) live in internal/store (purge_execution_store.go,
// schema v12): the execution model — the evidence_purge_executions attempt
// state machine (intent → completed | interrupted), the execute command and
// the execute result — is FROZEN here; this module itself stays pure.
package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
)

// PurgeLifecycleModelVersion is the version-prefix of the purge-lifecycle model
// (schema v11). The design freezes the authorization policy version
// evidence-lifecycle-policy/v0.8.0 (authz); this model version identifies the
// lifecycle snapshot/state contract stamped on stored results.
const PurgeLifecycleModelVersion = "purge-lifecycle/v0.8.0"

// PurgeLifecycleState is the per-object lifecycle state of the evidence
// retention projection (design §2/§3.6). `stored` is the initial state of a
// bound object; `purge_approved` means the pipeline is APPROVED FOR EXECUTION
// (all required human approvals present) — approval alone never removes bytes;
// `purged` is terminal and only reachable through the execution work unit.
type PurgeLifecycleState string

const (
	// PurgeLifecycleStored is the initial state: bytes present, no outstanding
	// purge request (objects without a lifecycle row are `unmanaged`, never
	// `stored` — §14).
	PurgeLifecycleStored PurgeLifecycleState = "stored"
	// PurgeLifecycleRequested is the request state: a policy-eligible request is
	// recorded with full provenance, awaiting human authorization.
	PurgeLifecycleRequested PurgeLifecycleState = "purge_requested"
	// PurgeLifecycleApproved is the approval state: human authorization is
	// complete (single approval, or both approvals for a dual-approval
	// category). Execution pending — never removes bytes by itself.
	PurgeLifecycleApproved PurgeLifecycleState = "purge_approved"
	// PurgeLifecycleRejected is a TERMINAL state: the request is closed; history
	// stays visible and the object never re-enters the pipeline.
	PurgeLifecycleRejected PurgeLifecycleState = "purge_rejected"
	// PurgeLifecyclePurged is the TERMINAL post-execution state (execution work
	// unit): bytes physically removed, metadata/hash/links/events/receipts/
	// approvals immutable.
	PurgeLifecyclePurged PurgeLifecycleState = "purged"
)

// IsValidPurgeLifecycleState reports whether s is a closed lifecycle-state token.
func IsValidPurgeLifecycleState(s PurgeLifecycleState) bool {
	switch s {
	case PurgeLifecycleStored, PurgeLifecycleRequested, PurgeLifecycleApproved,
		PurgeLifecycleRejected, PurgeLifecyclePurged:
		return true
	}
	return false
}

// PurgeRequestStatus is the guarded status of the request aggregate (design
// §3.3). Only the store API advances it, always through a recorded event and an
// atomic receipt.
type PurgeRequestStatus string

const (
	// PurgeRequestStatusRequested is an open pipeline awaiting approval.
	PurgeRequestStatusRequested PurgeRequestStatus = "requested"
	// PurgeRequestStatusApproved means the approval requirements are COMPLETE
	// (single approval, or both approvals for a dual-approval category).
	PurgeRequestStatusApproved PurgeRequestStatus = "approved"
	// PurgeRequestStatusRejected is terminal (the object stays purge_rejected).
	PurgeRequestStatusRejected PurgeRequestStatus = "rejected"
	// PurgeRequestStatusWithdrawn means a default/dual second approver retracted
	// the approval (the object returns to stored).
	PurgeRequestStatusWithdrawn PurgeRequestStatus = "withdrawn"
	// PurgeRequestStatusCancelled means the original requester retracted the
	// request (the object returns to stored; a fresh request is a fresh act).
	PurgeRequestStatusCancelled PurgeRequestStatus = "cancelled"
	// PurgeRequestStatusExecuted is terminal (execution work unit).
	PurgeRequestStatusExecuted PurgeRequestStatus = "executed"
)

// IsValidPurgeRequestStatus reports whether s is a closed request-status token.
func IsValidPurgeRequestStatus(s PurgeRequestStatus) bool {
	switch s {
	case PurgeRequestStatusRequested, PurgeRequestStatusApproved, PurgeRequestStatusRejected,
		PurgeRequestStatusWithdrawn, PurgeRequestStatusCancelled, PurgeRequestStatusExecuted:
		return true
	}
	return false
}

// PurgeApprovalDecision is the frozen decision token of one evidence_purge_approvals
// row (design §3.4): an approver's decision, a rejection decision or a
// withdrawal decision are all immutable decision records.
type PurgeApprovalDecision string

const (
	// PurgeApprovalDecisionApproved is the decision token of an approval act.
	PurgeApprovalDecisionApproved PurgeApprovalDecision = "approved"
	// PurgeApprovalDecisionRejected is the decision token of a rejection.
	PurgeApprovalDecisionRejected PurgeApprovalDecision = "rejected"
	// PurgeApprovalDecisionWithdrawn is the decision token of a withdrawal.
	PurgeApprovalDecisionWithdrawn PurgeApprovalDecision = "withdrawn"
)

// IsValidPurgeApprovalDecision reports whether d is a closed decision token.
func IsValidPurgeApprovalDecision(d PurgeApprovalDecision) bool {
	switch d {
	case PurgeApprovalDecisionApproved, PurgeApprovalDecisionRejected, PurgeApprovalDecisionWithdrawn:
		return true
	}
	return false
}

// PurgeLifecycleEventAction is the closed action set of evidence_lifecycle_events
// (design §3.5). The actions mirror the receipt action tokens so an audit can
// correlate an event with its receipt on the object chain one-to-one. The
// execution-phase actions (purge_intent, purge_executed) are frozen in the
// closed set and in the schema CHECK; WU-1 never emits them.
type PurgeLifecycleEventAction string

const (
	// PurgeEventRetentionBound records the binding of the resolved retention
	// snapshot to the object at request time (design §5/§6).
	PurgeEventRetentionBound PurgeLifecycleEventAction = "retention_bound"
	// PurgeEventRequested records the request transition.
	PurgeEventRequested PurgeLifecycleEventAction = "purge_requested"
	// PurgeEventApproved records an approval transition (order 1 or 2).
	PurgeEventApproved PurgeLifecycleEventAction = "purge_approved"
	// PurgeEventRejected records the terminal rejection.
	PurgeEventRejected PurgeLifecycleEventAction = "purge_rejected"
	// PurgeEventCancelled records the requester retraction.
	PurgeEventCancelled PurgeLifecycleEventAction = "purge_cancelled"
	// PurgeEventWithdrawn records the approval retraction.
	PurgeEventWithdrawn PurgeLifecycleEventAction = "purge_withdrawn"
	// PurgeEventIntent is the execution-intent event (WU-2, frozen).
	PurgeEventIntent PurgeLifecycleEventAction = "purge_intent"
	// PurgeEventExecuted is the execution-completion event (WU-2, frozen).
	PurgeEventExecuted PurgeLifecycleEventAction = "purge_executed"
)

// IsValidPurgeLifecycleEventAction reports whether a is a closed event-action token.
func IsValidPurgeLifecycleEventAction(a PurgeLifecycleEventAction) bool {
	switch a {
	case PurgeEventRetentionBound, PurgeEventRequested, PurgeEventApproved, PurgeEventRejected,
		PurgeEventCancelled, PurgeEventWithdrawn, PurgeEventIntent, PurgeEventExecuted:
		return true
	}
	return false
}

// EvidencePurgeRequest is the wire shape of ONE evidence_purge_requests row
// (design §3.3): the object + requested purge intent. The scope tuple is
// flattened exactly like EvidenceObject/EvidenceHold. RetentionStateSnapshot is
// the eligibility dimension bound at request time; ReviewedLifecycleHash is the
// canonical lifecycle snapshot hash (H1) the requester asserted — the store
// fails closed on any drift. Status is the guarded transition column;
// ApprovedAt/ExecutionID are the guarded completion columns.
type EvidencePurgeRequest struct {
	RequestID string `json:"requestId"`
	ObjectID  string `json:"objectId"`
	TenantID  string `json:"tenantId"`
	CompanyID string `json:"companyId"`
	RUC       string `json:"ruc"`
	Period    string `json:"period,omitempty"`
	Category  string `json:"category"`
	PolicyID  string `json:"policyId"`
	// RetentionStateSnapshot is the bound eligibility dimension ('eligible' |
	// 'not_due' | 'unknown') — never a statutory duration claim.
	RetentionStateSnapshot RetentionEligibility `json:"retentionStateSnapshot"`
	// ReviewedLifecycleHash is H_request: the canonical snapshot hash the
	// requester examined.
	ReviewedLifecycleHash string             `json:"reviewedLifecycleHash"`
	Status                PurgeRequestStatus `json:"status"`
	RequestedAt           string             `json:"requestedAt"`
	RequestedBy           string             `json:"requestedBy"`
	ApprovedAt            string             `json:"approvedAt,omitempty"`
	ExecutionID           string             `json:"executionId,omitempty"`
}

// EvidencePurgeApproval is the wire shape of ONE immutable
// evidence_purge_approvals row (design §3.4): the full principal snapshot, the
// reviewed (H1) and resulting (H2) lifecycle snapshot hashes, the decision
// token, the 1-based approval order (1 = default approver; 2 = the dual
// controller/tax_responsible second approver) and the frozen policy version.
type EvidencePurgeApproval struct {
	ApprovalID string `json:"approvalId"`
	RequestID  string `json:"requestId"`
	// ApprovalOrder is a JSON integer (never a float): 1 | 2.
	ApprovalOrder int64                 `json:"approvalOrder"`
	Decision      PurgeApprovalDecision `json:"decision"`
	// ReviewedHash is H1: the canonical lifecycle snapshot hash the human
	// examined; ResultingHash is H2 after the transition (always != H1).
	ReviewedHash      string                 `json:"reviewedHash"`
	ResultingHash     string                 `json:"resultingHash"`
	PrincipalSnapshot auth.PrincipalSnapshot `json:"principalSnapshot"`
	Reason            string                 `json:"reason"`
	PolicyVersion     string                 `json:"policyVersion"`
	CreatedAt         string                 `json:"createdAt"`
}

// EvidenceLifecycleEvent is the wire shape of ONE immutable
// evidence_lifecycle_events row (design §3.5): the transition delta with its
// reviewed/resulting lifecycle hashes, the acting principal snapshot and the
// frozen policy version. Events are the source of truth; the projection is
// derived.
type EvidenceLifecycleEvent struct {
	EventID           string                    `json:"eventId"`
	ObjectID          string                    `json:"objectId"`
	RequestID         string                    `json:"requestId,omitempty"`
	Action            PurgeLifecycleEventAction `json:"action"`
	FromState         string                    `json:"fromState"`
	ToState           string                    `json:"toState"`
	ReviewedHash      string                    `json:"reviewedHash"`
	ResultingHash     string                    `json:"resultingHash"`
	PrincipalSnapshot auth.PrincipalSnapshot    `json:"principalSnapshot"`
	Reason            string                    `json:"reason"`
	PolicyVersion     string                    `json:"policyVersion"`
	CreatedAt         string                    `json:"createdAt"`
}

// EvidenceRetentionState is the read model of ONE evidence_retention_state
// projection row (design §3.6): the derived, queryable lifecycle state.
type EvidenceRetentionState struct {
	ObjectID              string               `json:"objectId"`
	LifecycleState        PurgeLifecycleState  `json:"lifecycleState"`
	RetentionState        RetentionEligibility `json:"retentionState"`
	PolicyID              string               `json:"policyId,omitempty"`
	Category              string               `json:"category"`
	HasActiveBlockingHold bool                 `json:"hasActiveBlockingHold"`
	CurrentHash           string               `json:"currentHash"`
	UpdatedAt             string               `json:"updatedAt"`
}

// LifecycleHoldRef is the canonical hold contribution to the lifecycle snapshot
// (§3.8): id, kind and placed_at of an ACTIVE blocking hold, sorted by id.
type LifecycleHoldRef struct {
	HoldID   string `json:"id"`
	Kind     string `json:"kind"`
	PlacedAt string `json:"placedAt"`
}

// LifecycleSnapshot is the canonical per-object lifecycle snapshot (§3.8): the
// fixed-shape input of the reviewed/resulting lifecycle hashes H1/H2. Field
// order IS the canonical JSON property order. PolicyVersion is a JSON integer
// (never a float). BlockingHolds is the sorted list of ACTIVE holds whose kind
// is in the deployment's blocking set (empty when none — never null);
// RequestID/ApprovalIDs appear only when present.
type LifecycleSnapshot struct {
	ObjectID       string               `json:"objectId"`
	TenantID       string               `json:"tenantId"`
	CompanyID      string               `json:"companyId"`
	RUC            string               `json:"ruc"`
	Period         string               `json:"period,omitempty"`
	LifecycleState PurgeLifecycleState  `json:"lifecycleState"`
	RetentionState RetentionEligibility `json:"retentionState"`
	PolicyID       string               `json:"policyId,omitempty"`
	// PolicyVersion is the resolved policy version (JSON integer).
	PolicyVersion int64  `json:"policyVersion,omitempty"`
	Category      string `json:"category,omitempty"`
	// BlockingHolds is canonical: sorted by hold id, empty array when none.
	BlockingHolds []LifecycleHoldRef `json:"blockingHolds"`
	RequestID     string             `json:"requestId,omitempty"`
	// ApprovalIDs is canonical: sorted ascending, omitted when none.
	ApprovalIDs []string `json:"approvalIds,omitempty"`
}

// RequestPurgeCommand is the store command for the request_purge transition
// (design §9): an authenticated human accountant/controller requests a purge
// pipeline for the object. ObjectID is the content-addressed object id;
// Jurisdiction/Legislation/Category are the resolution evidence the store
// resolves against the exact active retention policy; ExpectedLifecycleHash is
// the canonical lifecycle snapshot hash (H1) the requester reviewed (the store
// fails closed on drift); RequestID is the (tenant, requestId) idempotency key.
// The acting principal is a separate pre-verified argument (ADR-003 — the
// command can never declare identity).
type RequestPurgeCommand struct {
	ObjectID              string `json:"objectId"`
	Jurisdiction          string `json:"jurisdiction"`
	Legislation           string `json:"legislation"`
	Category              string `json:"category"`
	ExpectedLifecycleHash string `json:"expectedLifecycleHash"`
	Reason                string `json:"reason"`
	RequestID             string `json:"requestId"`
}

// ApprovePurgeCommand is the store command for the approve transition (design
// §9): an authenticated default approver (order 1) or, for a
// policy-designated fiscal/material category, a distinct controller or
// tax_responsible (order 2) records an approval. RequestID is the purge
// request being approved; ExpectedLifecycleHash is the reviewed hash H1 (for
// the second approval, the first approval's resulting hash); RequestIDKey is
// the (tenant, requestId) idempotency key of THIS approval act.
type ApprovePurgeCommand struct {
	RequestID             string `json:"requestId"`
	ExpectedLifecycleHash string `json:"expectedLifecycleHash"`
	Reason                string `json:"reason"`
	RequestIDKey          string `json:"requestIdKey"`
}

// RejectPurgeCommand is the store command for the terminal reject transition:
// an authenticated default approver closes the request with a reason.
type RejectPurgeCommand struct {
	RequestID    string `json:"requestId"`
	Reason       string `json:"reason"`
	RequestIDKey string `json:"requestIdKey"`
}

// CancelPurgeCommand is the store command for the requester retraction: the
// ORIGINAL requester (idempotent retraction) returns the object to stored.
type CancelPurgeCommand struct {
	RequestID    string `json:"requestId"`
	RequestIDKey string `json:"requestIdKey"`
}

// WithdrawPurgeCommand is the store command for the approval retraction: a
// default approver or dual second approver withdraws an approved pipeline with
// a reason (the design's documented cleanup — §7).
type WithdrawPurgeCommand struct {
	RequestID    string `json:"requestId"`
	Reason       string `json:"reason"`
	RequestIDKey string `json:"requestIdKey"`
}

// RequestPurgeResult is the outcome of a request. A successful request ALWAYS
// created/refreshed the request row and the open pipeline (Created=true);
// IdempotentReplay=true means the (tenant, requestId) was already completed
// with the SAME command and principal, so the stored outcome is returned with
// NO new row/event/receipt.
type RequestPurgeResult struct {
	Request          EvidencePurgeRequest `json:"request"`
	Created          bool                 `json:"created"`
	IdempotentReplay bool                 `json:"idempotentReplay"`
}

// ApprovePurgeResult is the outcome of an approval. ApprovalOrder is 1 for the
// default-approver approval, 2 for the dual second approval; the request status
// flips to 'approved' only when the category's approval requirements are
// complete.
type ApprovePurgeResult struct {
	Request          EvidencePurgeRequest  `json:"request"`
	Approval         EvidencePurgeApproval `json:"approval"`
	ApprovalOrder    int64                 `json:"approvalOrder"`
	IdempotentReplay bool                  `json:"idempotentReplay"`
}

// RejectPurgeResult is the outcome of a rejection (terminal).
type RejectPurgeResult struct {
	Request          EvidencePurgeRequest  `json:"request"`
	Approval         EvidencePurgeApproval `json:"approval"`
	IdempotentReplay bool                  `json:"idempotentReplay"`
}

// CancelPurgeResult is the outcome of a requester retraction.
type CancelPurgeResult struct {
	Request          EvidencePurgeRequest `json:"request"`
	IdempotentReplay bool                 `json:"idempotentReplay"`
}

// WithdrawPurgeResult is the outcome of an approval retraction.
type WithdrawPurgeResult struct {
	Request          EvidencePurgeRequest  `json:"request"`
	Approval         EvidencePurgeApproval `json:"approval"`
	IdempotentReplay bool                  `json:"idempotentReplay"`
}

// PurgeExecutionState is the guarded attempt state of ONE
// evidence_purge_executions row (design §3.7): `intent` is the durably
// receipt-covered intent (committed BEFORE any byte is removed); `completed`
// is the terminal success of that attempt; `interrupted` is TERMINAL for that
// attempt — a retry runs a FRESH execution row under the same request
// (idempotent by execution_id).
type PurgeExecutionState string

const (
	// PurgeExecutionIntent is the committed intent: the executions row + the
	// purge_intent event/receipt committed, NOTHING deleted yet.
	PurgeExecutionIntent PurgeExecutionState = "intent"
	// PurgeExecutionCompleted is the terminal success of ONE attempt: the
	// bytes were removed and the purge_executed completion committed.
	PurgeExecutionCompleted PurgeExecutionState = "completed"
	// PurgeExecutionInterrupted is the TERMINAL state of an attempt whose
	// intent never completed (a process crash or a failed byte removal): it is
	// surfaced, never pretended completed; a retry runs a fresh execution row.
	PurgeExecutionInterrupted PurgeExecutionState = "interrupted"
)

// IsValidPurgeExecutionState reports whether s is a closed execution-state token.
func IsValidPurgeExecutionState(s PurgeExecutionState) bool {
	switch s {
	case PurgeExecutionIntent, PurgeExecutionCompleted, PurgeExecutionInterrupted:
		return true
	}
	return false
}

// EvidencePurgeExecution is the wire shape of ONE evidence_purge_executions row
// (design §3.7): the execution-attempt record. RelPath is the exact
// content-addressed byte path, Size the recorded byte size and
// PreRemovalHash the content address the bytes MUST re-hash to immediately
// before the unlink (a mismatch aborts — bytes are never deleted on a hash
// mismatch). IntentReviewedHash is the BOUND immutable authorization of the
// attempt: the canonical lifecycle snapshot hash H1 the executor examined and
// the full non-overridable blocker set (closed-period gate, retention
// eligibility, active blocking holds, lifecycle version, approvals) passed
// under, recorded durably BEFORE any byte is removed — the recovery path
// re-validates the intent against it (a crash after the authorized unlink
// converges on this frozen snapshot, never on a fresh re-derivation). State is
// the guarded attempt state; CompletionReceiptID is the receipt hash of the
// purge_executed receipt (the completion is receipt-covered).
type EvidencePurgeExecution struct {
	ExecutionID         string              `json:"executionId"`
	RequestID           string              `json:"requestId"`
	ObjectID            string              `json:"objectId"`
	RelPath             string              `json:"relPath"`
	Size                int64               `json:"size"`
	PreRemovalHash      string              `json:"preRemovalHash"`
	IntentReviewedHash  string              `json:"intentReviewedHash"`
	State               PurgeExecutionState `json:"state"`
	IntentAt            string              `json:"intentAt"`
	IntentBy            string              `json:"intentBy"`
	CompletedAt         string              `json:"completedAt,omitempty"`
	CompletedBy         string              `json:"completedBy,omitempty"`
	CompletionReceiptID string              `json:"completionReceiptId,omitempty"`
}

// ExecutePurgeCommand is the store command for the execute transition (design
// §2/§9/§11): an authenticated executor (human default approver or dual second
// approver — or a deployment-configured scheduler invoking the SAME guarded
// store operation; the scheduler is never an approver) executes an APPROVED
// pipeline. RequestID identifies the approved purge request;
// ExpectedLifecycleHash is the canonical lifecycle snapshot hash (H1) the
// executor examined — the store fails closed on any drift; ExecutionID is the
// (tenant, executionId) idempotency key of THIS execution attempt: a retry
// after an interrupted attempt uses a FRESH execution id (the interrupted
// attempt stays terminal), while replaying the same id returns the stored
// outcome.
type ExecutePurgeCommand struct {
	RequestID             string `json:"requestId"`
	ExpectedLifecycleHash string `json:"expectedLifecycleHash"`
	Reason                string `json:"reason,omitempty"`
	ExecutionID           string `json:"executionId"`
}

// ExecutePurgeResult is the outcome of ONE execution attempt: the final
// request row (status executed) and the execution row (state completed).
// IdempotentReplay=true means the (tenant, executionId) attempt was already
// completed with the SAME command and principal, so the stored outcome is
// returned with NO new intent/removal/completion. Recovered=true means the
// call CONVERGED a previously interrupted attempt (a crash between the durable
// intent and the completion) to the terminal purged state under its bound
// immutable authorization — exactly one purge_executed event/receipt is
// emitted and no duplicate execution row is created; a recovery is never an
// idempotent replay of a stored outcome (the completion is first persisted by
// THIS call).
type ExecutePurgeResult struct {
	Request          EvidencePurgeRequest   `json:"request"`
	Execution        EvidencePurgeExecution `json:"execution"`
	IdempotentReplay bool                   `json:"idempotentReplay"`
	Recovered        bool                   `json:"recovered,omitempty"`
}

// AssertValidRequestPurgeCommand fails closed on malformed request input: the
// object id must be the 64-lowercase-hex content address, the resolution
// evidence must be complete (jurisdiction syntax + non-empty legislation and
// category), the expected lifecycle hash must be the 64-lowercase-hex SHA-256
// shape, the reason is REQUIRED and the (tenant, requestId) idempotency key is
// REQUIRED.
func AssertValidRequestPurgeCommand(cmd RequestPurgeCommand) error {
	if !objectIDPattern.MatchString(cmd.ObjectID) {
		return fmt.Errorf("INVALID_PURGE_OBJECT_ID: objectId must be 64 lowercase hex digits (the content-addressed object id), got %q", cmd.ObjectID)
	}
	if !JurisdictionOK(cmd.Jurisdiction) {
		return fmt.Errorf("INVALID_PURGE_JURISDICTION: jurisdiction must match ^[A-Z][A-Z0-9-]{1,15}$, got %q", cmd.Jurisdiction)
	}
	if strings.TrimSpace(cmd.Legislation) == "" {
		return fmt.Errorf("INVALID_PURGE_LEGISLATION: legislation must be non-empty (resolution evidence)")
	}
	if strings.TrimSpace(cmd.Category) == "" {
		return fmt.Errorf("INVALID_PURGE_CATEGORY: category must be non-empty (resolution evidence)")
	}
	if !hash64Pattern.MatchString(cmd.ExpectedLifecycleHash) {
		return fmt.Errorf("INVALID_PURGE_EXPECTED_HASH: expectedLifecycleHash must be the 64-lowercase-hex canonical lifecycle snapshot hash, got %q", cmd.ExpectedLifecycleHash)
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return fmt.Errorf("REASON_REQUIRED: a purge request requires a non-empty reason")
	}
	if strings.TrimSpace(cmd.RequestID) == "" {
		return auth.New(auth.CodeIdempotencyConflict, "requestId (tenant-scoped idempotency key) is required")
	}
	return nil
}

// AssertValidApprovePurgeCommand fails closed on malformed approval input: the
// request id must be a UUID, the expected lifecycle hash the 64-hex SHA-256
// shape, the reason is REQUIRED and the idempotency key REQUIRED.
func AssertValidApprovePurgeCommand(cmd ApprovePurgeCommand) error {
	if !policyIDPattern(cmd.RequestID) {
		return fmt.Errorf("INVALID_PURGE_REQUEST_ID: requestId must be a UUID (the purge request id), got %q", cmd.RequestID)
	}
	if !hash64Pattern.MatchString(cmd.ExpectedLifecycleHash) {
		return fmt.Errorf("INVALID_PURGE_EXPECTED_HASH: expectedLifecycleHash must be the 64-lowercase-hex canonical lifecycle snapshot hash, got %q", cmd.ExpectedLifecycleHash)
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return fmt.Errorf("REASON_REQUIRED: an approval requires a non-empty reason")
	}
	if strings.TrimSpace(cmd.RequestIDKey) == "" {
		return auth.New(auth.CodeIdempotencyConflict, "requestIdKey (tenant-scoped idempotency key) is required")
	}
	return nil
}

// AssertValidRejectPurgeCommand fails closed on malformed rejection input.
func AssertValidRejectPurgeCommand(cmd RejectPurgeCommand) error {
	if !policyIDPattern(cmd.RequestID) {
		return fmt.Errorf("INVALID_PURGE_REQUEST_ID: requestId must be a UUID (the purge request id), got %q", cmd.RequestID)
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return fmt.Errorf("REASON_REQUIRED: a rejection requires a non-empty reason")
	}
	if strings.TrimSpace(cmd.RequestIDKey) == "" {
		return auth.New(auth.CodeIdempotencyConflict, "requestIdKey (tenant-scoped idempotency key) is required")
	}
	return nil
}

// AssertValidCancelPurgeCommand fails closed on malformed cancellation input.
func AssertValidCancelPurgeCommand(cmd CancelPurgeCommand) error {
	if !policyIDPattern(cmd.RequestID) {
		return fmt.Errorf("INVALID_PURGE_REQUEST_ID: requestId must be a UUID (the purge request id), got %q", cmd.RequestID)
	}
	if strings.TrimSpace(cmd.RequestIDKey) == "" {
		return auth.New(auth.CodeIdempotencyConflict, "requestIdKey (tenant-scoped idempotency key) is required")
	}
	return nil
}

// AssertValidWithdrawPurgeCommand fails closed on malformed withdrawal input.
func AssertValidWithdrawPurgeCommand(cmd WithdrawPurgeCommand) error {
	if !policyIDPattern(cmd.RequestID) {
		return fmt.Errorf("INVALID_PURGE_REQUEST_ID: requestId must be a UUID (the purge request id), got %q", cmd.RequestID)
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return fmt.Errorf("REASON_REQUIRED: a withdrawal requires a non-empty reason")
	}
	if strings.TrimSpace(cmd.RequestIDKey) == "" {
		return auth.New(auth.CodeIdempotencyConflict, "requestIdKey (tenant-scoped idempotency key) is required")
	}
	return nil
}

// AssertValidExecutePurgeCommand fails closed on malformed execution input: the
// request id must be a UUID, the expected lifecycle hash the 64-hex SHA-256
// shape (the reviewed approval hash H1 the executor examined) and the
// (tenant, executionId) idempotency key a REQUIRED UUID. The reason is
// OPTIONAL for execution (the design's execute transition records the
// execution, not a judgment).
func AssertValidExecutePurgeCommand(cmd ExecutePurgeCommand) error {
	if !policyIDPattern(cmd.RequestID) {
		return fmt.Errorf("INVALID_PURGE_REQUEST_ID: requestId must be a UUID (the purge request id), got %q", cmd.RequestID)
	}
	if !hash64Pattern.MatchString(cmd.ExpectedLifecycleHash) {
		return fmt.Errorf("INVALID_PURGE_EXPECTED_HASH: expectedLifecycleHash must be the 64-lowercase-hex canonical lifecycle snapshot hash, got %q", cmd.ExpectedLifecycleHash)
	}
	if !policyIDPattern(cmd.ExecutionID) {
		return fmt.Errorf("INVALID_PURGE_EXECUTION_ID: executionId must be a UUID (the tenant-scoped idempotency key of this execution attempt), got %q", cmd.ExecutionID)
	}
	return nil
}

// hash64Pattern freezes the canonical hash syntax: 64 lowercase hex digits (the
// SHA-256 digest shape) — the shape of every lifecycle snapshot hash and every
// receipt payload hash.
var hash64Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// AssertValidPurgeRequest fails closed on malformed stored request metadata:
// the request id must be a UUID, the object id the 64-hex content address, the
// scope an EXACT company scope, category/policy evidence non-empty, the bound
// eligibility a closed token, the reviewed hash the 64-hex shape, the status a
// closed token, the requester non-empty, and the timestamps parseable when
// present (a partially completed row is invalid metadata).
func AssertValidPurgeRequest(r EvidencePurgeRequest) error {
	if r.RequestID != "" && !policyIDPattern(r.RequestID) {
		return fmt.Errorf("INVALID_PURGE_REQUEST_ID: requestId must be a UUID, got %q", r.RequestID)
	}
	if !objectIDPattern.MatchString(r.ObjectID) {
		return fmt.Errorf("INVALID_PURGE_OBJECT_ID: objectId must be 64 lowercase hex digits, got %q", r.ObjectID)
	}
	if err := AssertValidObjectScope(Scope{
		Kind:           ScopeKindCompany,
		OrganizationID: r.TenantID,
		CompanyID:      r.CompanyID,
		RUC:            r.RUC,
		Period:         r.Period,
	}); err != nil {
		return err
	}
	if strings.TrimSpace(r.Category) == "" {
		return fmt.Errorf("INVALID_PURGE_CATEGORY: category must be non-empty")
	}
	if !policyIDPattern(r.PolicyID) {
		return fmt.Errorf("INVALID_PURGE_POLICY_ID: policyId must be a UUID, got %q", r.PolicyID)
	}
	if !IsValidRetentionEligibility(r.RetentionStateSnapshot) {
		return fmt.Errorf("INVALID_PURGE_RETENTION_SNAPSHOT: retentionStateSnapshot must be one of eligible|not_due|unknown, got %q", r.RetentionStateSnapshot)
	}
	if !hash64Pattern.MatchString(r.ReviewedLifecycleHash) {
		return fmt.Errorf("INVALID_PURGE_REVIEWED_HASH: reviewedLifecycleHash must be the 64-lowercase-hex canonical lifecycle snapshot hash, got %q", r.ReviewedLifecycleHash)
	}
	if !IsValidPurgeRequestStatus(r.Status) {
		return fmt.Errorf("INVALID_PURGE_STATUS: status must be one of requested|approved|rejected|withdrawn|cancelled|executed, got %q", r.Status)
	}
	if strings.TrimSpace(r.RequestedBy) == "" {
		return fmt.Errorf("INVALID_PURGE_REQUESTED_BY: requestedBy must be a non-empty string")
	}
	if r.RequestedAt != "" {
		if _, ok := ParseDateTime(r.RequestedAt); !ok {
			return fmt.Errorf("INVALID_PURGE_REQUESTED_AT: requestedAt must be a parseable date string, got %q", r.RequestedAt)
		}
	}
	if r.ApprovedAt != "" {
		if _, ok := ParseDateTime(r.ApprovedAt); !ok {
			return fmt.Errorf("INVALID_PURGE_APPROVED_AT: approvedAt must be a parseable date string, got %q", r.ApprovedAt)
		}
	}
	// A completed request (approved/rejected/withdrawn/cancelled/executed) must
	// not carry a missing requested_at; an approved request must carry
	// approved_at. The store sets these together; a partially completed row is
	// invalid metadata.
	if r.Status != PurgeRequestStatusRequested && r.ApprovedAt == "" && r.Status != PurgeRequestStatusCancelled {
		return fmt.Errorf("INVALID_PURGE_COMPLETION: a completed request requires approvedAt (cancellation is the requester retraction and carries none)")
	}
	return nil
}

// canonicalLifecycleSnapshot is the canonical JSON shape of a LifecycleSnapshot:
// the struct field order IS the property order and property names use the wire
// names, so the canonical bytes are the wire representation.
type canonicalLifecycleSnapshot LifecycleSnapshot

// CanonicalLifecycleSnapshotJSON returns the canonical compact UTF-8 JSON bytes
// of a LifecycleSnapshot (design §3.8): FIXED property order (exactly the
// struct order above), JSON string escaping, NO HTML escaping (matching the
// receipt canonicalizers — Go escapes <,>,& by default, disabling it keeps the
// bytes deterministic across runtimes). BlockingHolds and ApprovalIDs are
// normalized to canonical sorted form (defensive — the contract never depends
// on the caller's ordering). Marshaling cannot fail (fixed value shapes) — a
// failure is an internal invariant violation and fails closed via panic.
func CanonicalLifecycleSnapshotJSON(s LifecycleSnapshot) []byte {
	canonical := canonicalLifecycleSnapshot(s)
	canonical.BlockingHolds = sortedLifecycleHolds(s.BlockingHolds)
	canonical.ApprovalIDs = sortedApprovalIDs(s.ApprovalIDs)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(canonical); err != nil {
		panic(fmt.Sprintf("canonicalize lifecycle snapshot: %v", err))
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// ComputeLifecycleSnapshotHash returns the lowercase SHA-256 hex of the
// canonical lifecycle snapshot bytes — the deterministic H1/H2 an approver
// examines and every transition produces (design §3.8/§9). The store fails
// closed on any drift (LIFECYCLE_VERSION_MISMATCH). sha256HexBytes is the
// receipt module's canonical lowercase SHA-256 hex helper (same digest
// contract — the lifecycle hashes share the receipt hash syntax).
func ComputeLifecycleSnapshotHash(s LifecycleSnapshot) string {
	return sha256HexBytes(CanonicalLifecycleSnapshotJSON(s))
}

// sortedLifecycleHolds returns a defensive copy of holds sorted by hold id
// (canonical contribution order — §3.8); nil becomes an empty (never null)
// array.
func sortedLifecycleHolds(holds []LifecycleHoldRef) []LifecycleHoldRef {
	out := make([]LifecycleHoldRef, len(holds))
	copy(out, holds)
	sort.Slice(out, func(i, j int) bool { return out[i].HoldID < out[j].HoldID })
	return out
}

// sortedApprovalIDs returns a defensive copy of the approval ids sorted
// ascending; nil becomes an empty (omitted by omitempty) list.
func sortedApprovalIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}
