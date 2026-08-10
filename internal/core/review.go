// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module defines the v0.9.0 REVIEW WORKSPACE
// contracts (docs/architecture/review-workspace-v0.9.md): the queue, the detail
// assembly and the authenticated decision operations (reject/return). These are
// VALUES and PURE decisions only — the atomic state changes live in
// internal/store (SQLiteStore) and the orchestration in internal/server.
//
// The commands deliberately carry NO principal fields (ADR-003, same as
// ApproveMemoryCommand): authority arrives as a separate verified principal
// (auth.VerifiedApprovalPrincipal), never inside the transport payload.
//
// Decisions are: authenticated (ApprovalPrincipal), idempotent by
// (tenant, requestId), hash-guarded (fresh H1 recompute vs expectedEnvelopeHash),
// atomic in one transaction, and emit an immutable event + signed receipt. The
// SoD clause (approver subjectId ≠ proposer recordedBy → SOD_VIOLATION) and the
// reason policy are enforced inside the store transaction (design §5/§6.5.5).
package core

import "github.com/arkelythex/drenyra-engram/internal/auth"

// ──────────────────────────────────────────────
// Queue (design §3)
// ──────────────────────────────────────────────

// ReviewQueueQuery paginates the pending_review queue of an EXACT company scope.
// Scope is the structural filter (server-side, never a post-filter); limit is
// bounded (default 50, max 200, min 1) and offset defaults to 0. The queue
// returns pending_review items only (closed set, no status injection).
type ReviewQueueQuery struct {
	Scope  Scope
	Limit  int
	Offset int
}

// DefaultReviewQueueLimit is the default page size (design §3).
const DefaultReviewQueueLimit = 50

// MaxReviewQueueLimit is the hard page-size bound (design §3).
const MaxReviewQueueLimit = 200

// ReviewQueueItem is one pending_review row of the queue. MaterialityCents is the
// declared monetary threshold in WHOLE CENTS (int64; 0 when unset) — never a
// float. EnvelopeHash is the CURRENT envelope the reviewer must sign against.
// RecordedBy is the proposer (the pending revision's source actor id); the
// decision SoD clause requires the reviewer to differ from it.
type ReviewQueueItem struct {
	MemoryID          string            `json:"memoryId"`
	Kind              MemoryKind        `json:"kind"`
	FiscalEffect      FiscalEffect      `json:"fiscalEffect"`
	MaterialityLevel  *MaterialityLevel `json:"materialityLevel,omitempty"`
	MaterialityCents  int64             `json:"materialityCents"`
	Status            MemoryStatus      `json:"status"`
	EnvelopeHash      string            `json:"envelopeHash"`
	RecordedBy        string            `json:"recordedBy"`
	RecordedAt        string            `json:"recordedAt"`
	EvidenceRefCount  int               `json:"evidenceRefCount"`
	RuleRefCount      int               `json:"ruleRefCount"`
	OpenJudgmentCount int               `json:"openJudgmentCount"`
}

// ReviewQueuePage is the paginated queue result: the items plus the applied
// limit/offset (echoed for deterministic client-side cursor math).
type ReviewQueuePage struct {
	Items  []ReviewQueueItem `json:"items"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

// ──────────────────────────────────────────────
// Detail (design §4)
// ──────────────────────────────────────────────

// FieldChange is ONE changed identity/content field of the structured diff
// (previous chain revision → pending head). Before/After are canonical string
// renderings of the field (JSON for structured values, the raw string otherwise);
// status, timestamps and recorded-by are provenance and never appear.
type FieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// ContentDiff is the structured content diff of the pending revision vs its
// immediate chain predecessor (design §4.2). An empty Changes slice means the
// revision only changed provenance/status (or is the chain's first revision).
type ContentDiff struct {
	Changes []FieldChange `json:"changes"`
}

// EvidenceAvailability classifies the WORM availability of an evidence ref
// (design §4.3): object-backed and re-verified → present; object-shaped (a
// 64-hex SHA-256 content address) with no row → absent; anything else (a legacy
// ref, a path, an external id) → not-a-ref.
type EvidenceAvailability string

const (
	EvidencePresent EvidenceAvailability = "present"
	EvidenceAbsent  EvidenceAvailability = "absent"
	EvidenceNotARef EvidenceAvailability = "not-a-ref"
)

// EvidenceRefState is one evidence ref with its WORM availability (design §4.3).
// ObjectID/SizeBytes/ContentType are filled only when the ref is object-backed.
type EvidenceRefState struct {
	Ref          string               `json:"ref"`
	Availability EvidenceAvailability `json:"availability"`
	ObjectID     string               `json:"objectId,omitempty"`
	SizeBytes    int64                `json:"sizeBytes,omitempty"`
	ContentType  string               `json:"contentType,omitempty"`
}

// RuleRefState is one rule ref with best-effort vigencia (design §4.4 — Phase 6
// fiscal-policy-memory is NOT required; unresolved refs stay resolved=false).
// When the ref resolves to a rule memory in the SAME scope, the vigencia window
// and status surface as recorded.
type RuleRefState struct {
	Ref         string       `json:"ref"`
	Resolved    bool         `json:"resolved"`
	MemoryID    string       `json:"memoryId,omitempty"`
	Status      MemoryStatus `json:"status,omitempty"`
	EffectiveAt string       `json:"effectiveAt,omitempty"`
	ExpiresAt   string       `json:"expiresAt,omitempty"`
}

// OpenJudgmentRef is one PROPOSED judgment touching the memory under review
// (design §4.5: status='proposed' AND (from_id=? OR to_id=?)). Proposed
// judgments are open questions the reviewer should consider before deciding.
type OpenJudgmentRef struct {
	JudgmentID string   `json:"judgmentId"`
	Relation   Relation `json:"relation"`
	FromID     string   `json:"fromId"`
	ToID       string   `json:"toId"`
	ProposerID string   `json:"proposerId"`
	ProposedAt string   `json:"proposedAt"`
}

// ReviewMetadata is the decision-relevant envelope of the pending revision
// (design §4.6): the envelope hash to sign (H1 — fresh recompute of the current
// pending revision), the proposer, the timestamps, the risk class and the prior
// APPROVED revision of the chain when one exists (before/after context).
type ReviewMetadata struct {
	EnvelopeHashToSign    string            `json:"envelopeHashToSign"`
	RecordedBy            string            `json:"recordedBy"`
	RecordedAt            string            `json:"recordedAt"`
	ObservedAt            string            `json:"observedAt,omitempty"`
	FiscalEffect          FiscalEffect      `json:"fiscalEffect"`
	MaterialityLevel      *MaterialityLevel `json:"materialityLevel,omitempty"`
	MaterialityCents      int64             `json:"materialityCents"`
	PriorApprovedRevision string            `json:"priorApprovedRevision,omitempty"`
}

// ReviewBoundaryNotice is the gate G-9 wording echoed in EVERY review detail
// payload (design §4.7): a signature proves integrity, never accounting
// correctness. Never claim approval = correctness.
const ReviewBoundaryNotice = "signature integrity is not accounting correctness"

// ReviewDetail is the composed review of ONE pending revision (design §4):
// the full pending revision, the structured content diff vs its chain
// predecessor, the evidence state with WORM availability, the best-effort rule
// state, the open proposed judgments touching the memory, the decision-relevant
// review metadata and the boundary notice.
type ReviewDetail struct {
	Memory         AccountingMemory   `json:"memory"`
	Diff           ContentDiff        `json:"diff"`
	Evidence       []EvidenceRefState `json:"evidence"`
	Rules          []RuleRefState     `json:"rules"`
	OpenJudgments  []OpenJudgmentRef  `json:"openJudgments"`
	ReviewMetadata ReviewMetadata     `json:"reviewMetadata"`
	BoundaryNotice string             `json:"boundaryNotice"`
}

// ──────────────────────────────────────────────
// Decision operations (design §5)
// ──────────────────────────────────────────────

// ReviewChecks are the two anti-rubber-stamp checks a human reviewer declares for
// a HIGH-RISK approval (design §5/§6): the evidence was inspected and the rules
// were inspected. A material/critical approval requires BOTH true →
// REVIEW_CHECKS_REQUIRED otherwise (fail-closed inside the transaction).
type ReviewChecks struct {
	EvidenceInspected bool `json:"evidenceInspected"`
	RuleInspected     bool `json:"ruleInspected"`
}

// RejectReasonRequired reports whether the memory's risk class DEMANDS a
// rejection reason (design §5): materiality ≥ material OR fiscalEffect ∈
// {closing, declaration, sunat_filing}. An empty reason for a required class
// fails closed with REASON_REQUIRED; otherwise the reason is optional.
func RejectReasonRequired(memory AccountingMemory) bool {
	if memory.MaterialityLevel != nil {
		switch *memory.MaterialityLevel {
		case MaterialityMaterial, MaterialityCritical:
			return true
		}
	}
	switch memory.FiscalEffect {
	case FiscalEffectClosing, FiscalEffectDeclaration, FiscalEffectSunatFiling:
		return true
	}
	return false
}

// RejectMemoryCommand is the authenticated reject command: the memory to reject,
// the envelope hash the reviewer actually saw (fresh recompute vs
// expectedEnvelopeHash → ENVELOPE_MISMATCH on drift), the reason (REQUIRED for
// the risk classes of RejectReasonRequired, optional otherwise; always persisted
// in the event and receipt) and the (tenant, requestId) idempotency key. No
// principal fields (ADR-003).
type RejectMemoryCommand struct {
	MemoryID             string `json:"memoryId"`
	ExpectedEnvelopeHash string `json:"expectedEnvelopeHash"`
	Reason               string `json:"reason"`
	RequestID            string `json:"requestId"`
}

// RejectMemoryResult is the outcome of an atomic authenticated rejection.
// PreviousStatus is always "pending_review", CurrentStatus always "rejected";
// a replay returns the stored result with IdempotentReplay=true.
type RejectMemoryResult struct {
	MemoryID              string `json:"memoryId"`
	DecisionEventID       string `json:"decisionEventId"`
	PreviousStatus        string `json:"previousStatus"`
	CurrentStatus         string `json:"currentStatus"`
	ReviewedEnvelopeHash  string `json:"reviewedEnvelopeHash"`
	ResultingEnvelopeHash string `json:"resultingEnvelopeHash"`
	Reason                string `json:"reason"`
	PrincipalSubjectID    string `json:"principalSubjectId"`
	MembershipID          string `json:"membershipId"`
	PolicyVersion         string `json:"policyVersion"`
	DecidedAt             string `json:"decidedAt"`
	IdempotentReplay      bool   `json:"idempotentReplay"`
}

// ReturnMemoryCommand is the authenticated RETURN command: the memory to return,
// the reviewed envelope hash (H1 guard, same semantics as reject/approve), the
// reason (REQUIRED — a return is a correction request; the reason tells the
// agent what to fix) and the (tenant, requestId) idempotency key.
type ReturnMemoryCommand struct {
	MemoryID             string `json:"memoryId"`
	ExpectedEnvelopeHash string `json:"expectedEnvelopeHash"`
	Reason               string `json:"reason"`
	RequestID            string `json:"requestId"`
}

// ReturnMemoryResult is the outcome of an atomic authenticated return.
// PreviousStatus is always "pending_review", CurrentStatus always "returned"
// (NON-terminal: an agent Save on the returned memory creates a NEW revision
// that re-enters pending_review).
type ReturnMemoryResult struct {
	MemoryID              string `json:"memoryId"`
	DecisionEventID       string `json:"decisionEventId"`
	PreviousStatus        string `json:"previousStatus"`
	CurrentStatus         string `json:"currentStatus"`
	ReviewedEnvelopeHash  string `json:"reviewedEnvelopeHash"`
	ResultingEnvelopeHash string `json:"resultingEnvelopeHash"`
	Reason                string `json:"reason"`
	PrincipalSubjectID    string `json:"principalSubjectId"`
	MembershipID          string `json:"membershipId"`
	PolicyVersion         string `json:"policyVersion"`
	DecidedAt             string `json:"decidedAt"`
	IdempotentReplay      bool   `json:"idempotentReplay"`
}

// MemoryDecisionEvent is the immutable audit record of an authenticated reject or
// return (v0.9.0) — the SAME approval_events ledger (extended v13 action CHECKs:
// action IN ('approved','rejected','returned'), from_status 'pending_review',
// to_status IN ('approved','rejected','returned'),
// authorization_reason_code IN ('AUTHORIZED','REJECTED','RETURNED')). For a
// reject: action "rejected", toStatus "rejected", authorizationReasonCode
// "REJECTED"; for a return: action "returned", toStatus "returned",
// authorizationReasonCode "RETURNED". PrincipalSnapshot is the canonical
// snapshot with sorted, deduplicated roles.
type MemoryDecisionEvent struct {
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

// ──────────────────────────────────────────────
// Anti-rubber-stamp observable events (design §6, engine-side minimal)
// ──────────────────────────────────────────────

// Velocity thresholds (design §6 — engine-side DEFAULTS; a deployment may tune
// them before the alert check runs; they are documented defaults, never claims).
const (
	// ApprovalVelocityWindowMinutes is the rolling window for approval velocity.
	ApprovalVelocityWindowMinutes = 15
	// ApprovalVelocityThreshold is the approval-count threshold per window (>30).
	ApprovalVelocityThreshold = 30
	// ConsecutiveDecisionThreshold is the consecutive reject/return streak that
	// triggers the unusual-pattern alert (≥3 without an intervening approval).
	ConsecutiveDecisionThreshold = 3
)

// ReviewVelocityAlertType discriminates the observable velocity events.
type ReviewVelocityAlertType string

const (
	// ReviewVelocityAlertApproval fires when a principal exceeded the per-window
	// approval count (>30 approvals per 15 minutes).
	ReviewVelocityAlertApproval ReviewVelocityAlertType = "approval_velocity"
	// ReviewVelocityAlertConsecutive fires when a principal decided ≥3
	// consecutive rejections/returns without an intervening approval (the
	// minimal unusual-pattern interpretation: same principal, any item set).
	ReviewVelocityAlertConsecutive ReviewVelocityAlertType = "consecutive_decisions"
)

// ReviewVelocityAlert is ONE immutable, audit-visible monitoring event (design
// §6): NOT a receipt and NOT a blocking control — a velocity signal only. The
// row is listed via the audit trail; no decision is ever blocked by it in this
// slice.
type ReviewVelocityAlert struct {
	ID                 string                  `json:"id"`
	TenantID           string                  `json:"tenantId"`
	PrincipalSubjectID string                  `json:"principalSubjectId"`
	AlertType          ReviewVelocityAlertType `json:"alertType"`
	WindowStartedAt    string                  `json:"windowStartedAt"`
	WindowEndedAt      string                  `json:"windowEndedAt"`
	ObservedCount      int                     `json:"observedCount"`
	ConsecutiveCount   int                     `json:"consecutiveCount"`
	RecordedAt         string                  `json:"recordedAt"`
}
