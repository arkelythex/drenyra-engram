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

// ReviewAcknowledgement preserves whether a reviewer declaration was omitted,
// explicitly false, or explicitly true. It records no authorization.
type ReviewAcknowledgement struct {
	Present bool `json:"present"`
	Value   bool `json:"value"`
}

type ReviewAcknowledgementState string

const (
	ReviewAcknowledgementOmitted ReviewAcknowledgementState = "omitted"
	ReviewAcknowledgementFalse   ReviewAcknowledgementState = "false"
	ReviewAcknowledgementTrue    ReviewAcknowledgementState = "true"
)

func (a ReviewAcknowledgement) State() ReviewAcknowledgementState {
	if !a.Present {
		return ReviewAcknowledgementOmitted
	}
	if a.Value {
		return ReviewAcknowledgementTrue
	}
	return ReviewAcknowledgementFalse
}

// ReviewChecksV1 is the Slice 5 presence-aware anti-rubber-stamp contract
// (design.md "Immutable act evidence and envelope linkage"): each check is
// independently omitted, provided false, or provided true. It is the type of
// ApproveMemoryCommand.ReviewChecks — the exported FIELD name stays exactly
// "ReviewChecks" (ADR-003's frozen field-name contract,
// TestApproveMemoryCommandCarriesNoPrincipalFields), only its TYPE moved from
// the legacy boolean-only ReviewChecks (internal/core/review.go, still used
// unchanged by the frozen v0.9.0 Go/TypeScript parity golden vectors via
// authz.ValidateReviewChecks) to this tri-state. This value itself grants
// nothing.
type ReviewChecksV1 struct {
	EvidenceInspected ReviewAcknowledgement `json:"evidenceInspected"`
	RuleInspected     ReviewAcknowledgement `json:"applicableRulesInspected"`
}

// ApproveMemoryCommand is the approval command. It carries the memory to
// approve, the envelope hash the caller reviewed, the reason, the idempotency
// request id, the presence-aware review checks and the OPTIONAL Slice 5 v1
// fiscal scope binding intent. No principal fields (compile-level contract —
// internal/server verifies the field set stays exactly this;
// TestApproveMemoryCommandCarriesNoPrincipalFields is the frozen list —
// FiscalIntent is scope METADATA, never authority, exactly like
// SaveInput.FiscalIntent and TransitionMeta.FiscalIntent added in Slice 3).
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
	// ReviewChecks (v0.9.0/Slice 5): presence-aware anti-rubber-stamp checks
	// for material/critical approvals. Adapters MUST NOT default an omitted
	// value into a successful declaration (proposal.md).
	ReviewChecks ReviewChecksV1 `json:"reviewChecks,omitempty"`
	// FiscalIntent is the OPTIONAL Slice 5 v1 fiscal scope binding a caller
	// wants bound to THIS approval act (operationType "memory.approve" or
	// "close.approve", authorityLevel EXECUTE per design.md's operation map —
	// legitimately DIFFERENT from an earlier memory.save/close.create
	// PREPARE binding on the same subject). It participates in command
	// INTENT only: authentication, membership, role, assurance, separation
	// of duties and the closed-period gate remain independently and
	// authoritatively enforced by the store. A nil intent is the legacy path
	// (byte-identical to pre-Slice-5 behavior) UNLESS the memory already
	// carries a v1 fiscal binding link, in which case a nil intent is a
	// direct-store-bypass attempt and fails closed
	// (spec.md "Direct store bypass is denied").
	FiscalIntent *FiscalWriteIntent `json:"-"`
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
