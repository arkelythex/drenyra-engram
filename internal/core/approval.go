// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module defines the approval contracts
// of the authenticated approval path (v0.4.0 Step 1, ADR-003): the command, the
// result and the immutable approval event. These are VALUES only — no logic
// lives here. The atomic state change lives in internal/store
// (SQLiteStore.ApproveMemory) and the service orchestration in
// internal/server.
//
// The command deliberately carries NO principal fields (ADR-003): authority
// arrives as a separate verified principal (auth.VerifiedApprovalPrincipal),
// never inside the transport payload. The event records the deliberately
// narrow principal snapshot (auth.PrincipalSnapshot) — subject, membership,
// canonical roles, method, assurance and time; session ids and token material
// never appear.
package core

import "github.com/arkelythex/drenyra-engram/internal/auth"

// ApproveMemoryCommand is the approval command. It carries exactly four fields:
// the memory to approve, the envelope hash the caller reviewed, the reason, and
// the idempotency request id. No principal fields (compile-level contract —
// internal/server verifies the field set stays exactly this).
type ApproveMemoryCommand struct {
	// MemoryID is the pending_review memory to approve.
	MemoryID string `json:"memoryId"`
	// ExpectedEnvelopeHash is the envelope hash the reviewer actually saw; the
	// store recomputes the CURRENT envelope and fails with ENVELOPE_MISMATCH
	// when it differs (a post-review link or status change invalidates it).
	ExpectedEnvelopeHash string `json:"expectedEnvelopeHash"`
	// Reason is the human-readable justification (REQUIRED, non-whitespace).
	Reason string `json:"reason"`
	// RequestID is the idempotency key scoped to (tenant, requestId); a replay
	// with the same id and payload returns the stored result.
	RequestID string `json:"requestId"`
}

// ApprovalResult is the outcome of an atomic approval. PreviousStatus is always
// "pending_review" and CurrentStatus always "approved" for a fresh approval; a
// replay returns the stored result with IdempotentReplay=true.
type ApprovalResult struct {
	MemoryID             string `json:"memoryId"`
	ApprovalEventID      string `json:"approvalEventId"`
	PreviousStatus       string `json:"previousStatus"`
	CurrentStatus        string `json:"currentStatus"`
	ReviewedEnvelopeHash string `json:"reviewedEnvelopeHash"`
	// ResultingEnvelopeHash is H2 — the envelope of the approved memory; it
	// always differs from ReviewedEnvelopeHash (status participates in the
	// envelope hash).
	ResultingEnvelopeHash string `json:"resultingEnvelopeHash"`
	PrincipalSubjectID    string `json:"principalSubjectId"`
	MembershipID          string `json:"membershipId"`
	PolicyVersion         string `json:"policyVersion"`
	ApprovedAt            string `json:"approvedAt"`
	// IdempotentReplay is true when this result was replayed from the completed
	// idempotency reservation instead of a fresh approval.
	IdempotentReplay bool `json:"idempotentReplay"`
}

// ApprovalEvent is the immutable audit record of an authenticated approval,
// mirroring the v3 approval_events table and the binding spec: action is always
// "approved", fromStatus "pending_review", toStatus "approved" and
// authorizationReasonCode always "AUTHORIZED". PrincipalSnapshot is the
// canonical snapshot with sorted, deduplicated roles.
type ApprovalEvent struct {
	ID             string `json:"id"`
	RequestID      string `json:"requestId"`
	MemoryID       string `json:"memoryId"`
	TenantID       string `json:"tenantId"`
	CompanyID      string `json:"companyId"`
	FiscalPeriodID string `json:"fiscalPeriodId,omitempty"`
	Action         string `json:"action"`
	FromStatus     string `json:"fromStatus"`
	ToStatus       string `json:"toStatus"`

	ReviewedEnvelopeHash  string `json:"reviewedEnvelopeHash"`
	ResultingEnvelopeHash string `json:"resultingEnvelopeHash"`
	Reason                string `json:"reason"`

	PrincipalSnapshot       auth.PrincipalSnapshot `json:"principalSnapshot"`
	PolicyVersion           string                 `json:"policyVersion"`
	AuthorizationReasonCode string                 `json:"authorizationReasonCode"`
	CreatedAt               string                 `json:"createdAt"`
}
