// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module defines the first-class
// reconciliation model and its PURE lifecycle (v0.5.0 — adjudicated
// reconciliations; see docs/architecture/close-intelligence-v0.5.md §3).
//
// A Reconciliation is an adjudicated relationship between two observations:
// the left/right endpoints, the domain amounts (left/right/variance/tolerance),
// the method and currency, the provenance-only proposer, and the authenticated
// decision (resolution, adjudicator snapshot, policy version). It is NOT a
// memory kind — confirmation atomically projects one observation relation
// leftMemoryId --reconciles--> rightMemoryId. Agents/systems may propose and
// withdraw their OWN proposals (their Source is provenance only, never
// authority); only an auth.VerifiedApprovalPrincipal may confirm or reject.
//
// Legal states: proposed -> confirmed | rejected | withdrawn | superseded;
// confirmed -> superseded ONLY (atomically with the confirming correction);
// rejected/withdrawn/superseded are terminal.
//
// This module holds values, the pure transition machine and the canonical
// reconciliation hash. The atomic authenticated act (policy + hash comparison +
// persistence) lives in internal/store.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
)

// ──────────────────────────────────────────────
// ReconciliationStatus — lifecycle state
// ──────────────────────────────────────────────

// ReconciliationStatus is the lifecycle state of a reconciliation
// (proposed | confirmed | rejected | withdrawn | superseded).
type ReconciliationStatus string

const (
	// ReconciliationProposed is a proposal awaiting an authenticated decision.
	ReconciliationProposed ReconciliationStatus = "proposed"
	// ReconciliationConfirmed is a confirmed reconciliation (resolution +
	// adjudicator; projects the reconciles observation relation).
	ReconciliationConfirmed ReconciliationStatus = "confirmed"
	// ReconciliationRejected is a rejected proposal (terminal; human reason
	// recorded, no relation projection).
	ReconciliationRejected ReconciliationStatus = "rejected"
	// ReconciliationWithdrawn is a proposal withdrawn by its own proposer
	// (terminal).
	ReconciliationWithdrawn ReconciliationStatus = "withdrawn"
	// ReconciliationSuperseded is a confirmed reconciliation corrected by a
	// successor (terminal; readers route to the successor).
	ReconciliationSuperseded ReconciliationStatus = "superseded"
)

// IsValidReconciliationStatus reports whether status is a known reconciliation
// status.
func IsValidReconciliationStatus(status ReconciliationStatus) bool {
	switch status {
	case ReconciliationProposed, ReconciliationConfirmed, ReconciliationRejected,
		ReconciliationWithdrawn, ReconciliationSuperseded:
		return true
	}
	return false
}

// ──────────────────────────────────────────────
// Reconciliation — the entity
// ──────────────────────────────────────────────

// Reconciliation is an adjudication act over two observations: the endpoint
// pair, the domain amounts (varianceCents is engine-derived =
// leftAmountCents - rightAmountCents), the method/currency, the
// provenance-only proposer, and the authenticated decision (resolution,
// adjudicator snapshot, policy version). Routing (SupersedesID) is excluded
// from the reconciliation hash; every proposal/adjudication field is immutable
// once decided.
type Reconciliation struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenantId"`
	CompanyID        string `json:"companyId"`
	FiscalPeriodID   string `json:"fiscalPeriodId,omitempty"`
	LeftMemoryID     string `json:"leftMemoryId"`
	RightMemoryID    string `json:"rightMemoryId"`
	Method           string `json:"method"`
	Currency         string `json:"currency"`
	LeftAmountCents  int64  `json:"leftAmountCents"`
	RightAmountCents int64  `json:"rightAmountCents"`
	// VarianceCents is engine-derived = left - right (schema-enforced).
	VarianceCents int64 `json:"varianceCents"`
	// ToleranceCents is the accepted variance band (non-negative).
	ToleranceCents int64                `json:"toleranceCents"`
	Status         ReconciliationStatus `json:"status"`
	// Proposer is provenance ONLY (agent|system) — it never authorizes.
	Proposer Source `json:"proposer"`
	// ProposalReason is the proposer's justification (never silently promoted
	// into a professional resolution).
	ProposalReason string `json:"proposalReason"`
	// Resolution is empty until confirmed/rejected.
	Resolution string `json:"resolution,omitempty"`
	// Adjudicator is nil until an authenticated decision.
	Adjudicator *auth.PrincipalSnapshot `json:"adjudicator,omitempty"`
	// PolicyVersion is empty until an authenticated decision.
	PolicyVersion string `json:"policyVersion,omitempty"`
	// PredecessorID is the correction target declared by the successor.
	PredecessorID string `json:"predecessorId,omitempty"`
	// SupersedesID is the successor routing stored on the old row.
	SupersedesID string `json:"supersedesId,omitempty"`
	// ProposedAt/DecidedAt are RFC3339; DecidedAt is set only when decided.
	ProposedAt string `json:"proposedAt"`
	DecidedAt  string `json:"decidedAt,omitempty"`
}

// ──────────────────────────────────────────────
// Commands — VALUES ONLY, no principal fields
// ──────────────────────────────────────────────

// ProposeReconciliationCommand proposes a new adjudication over two
// observations. It deliberately carries NO subject/membership/role/actor-kind/
// assurance fields (compile-level contract): the proposer Source arrives
// separately as provenance-only caller context, and authority never travels in
// the payload. VarianceCents is engine-derived (never caller-supplied);
// ToleranceCents is the proposer's accepted variance band.
type ProposeReconciliationCommand struct {
	LeftMemoryID     string `json:"leftMemoryId"`
	RightMemoryID    string `json:"rightMemoryId"`
	Method           string `json:"method"`
	Currency         string `json:"currency"`
	LeftAmountCents  int64  `json:"leftAmountCents"`
	RightAmountCents int64  `json:"rightAmountCents"`
	// ToleranceCents is the accepted variance band (REQUIRED, non-negative).
	ToleranceCents int64 `json:"toleranceCents"`
	// Reason is the proposer's justification (REQUIRED, non-whitespace).
	Reason string `json:"reason"`
	// RequestID is the idempotency key scoped to (tenant, requestId).
	RequestID string `json:"requestId"`
	// PredecessorID names an existing reconciliation this proposal corrects.
	PredecessorID string `json:"predecessorId,omitempty"`
}

// ConfirmReconciliationCommand confirms a proposed reconciliation. Resolution
// is the professional human resolution (REQUIRED);
// ExpectedReconciliationHash is the reviewed proposed hash the adjudicator
// actually saw.
type ConfirmReconciliationCommand struct {
	ReconciliationID           string `json:"reconciliationId"`
	Resolution                 string `json:"resolution"`
	ExpectedReconciliationHash string `json:"expectedReconciliationHash"`
	RequestID                  string `json:"requestId"`
}

// RejectReconciliationCommand rejects a proposed reconciliation. Reason is the
// human reason, stored as the resolution (REQUIRED);
// ExpectedReconciliationHash is the reviewed proposed hash the adjudicator
// actually saw.
type RejectReconciliationCommand struct {
	ReconciliationID           string `json:"reconciliationId"`
	Reason                     string `json:"reason"`
	ExpectedReconciliationHash string `json:"expectedReconciliationHash"`
	RequestID                  string `json:"requestId"`
}

// WithdrawReconciliationCommand withdraws the caller's OWN proposed
// reconciliation (proposer-identity verification is the caller's provenance
// check — the store).
type WithdrawReconciliationCommand struct {
	ReconciliationID string `json:"reconciliationId"`
	RequestID        string `json:"requestId"`
}

// ──────────────────────────────────────────────
// Results — store-boundary outcomes
// ──────────────────────────────────────────────

// ProposeReconciliationResult is the outcome of an atomic proposal: the created
// reconciliation (proposed, decided_at empty) and the idempotency marker. The
// proposal writes NO reconciliation event (the frozen events CHECK allows only
// confirm|reject|withdraw|supersede), so the result carries the entity alone; a
// same-request retry replays the same reconciliation with
// IdempotentReplay=true.
type ProposeReconciliationResult struct {
	ReconciliationID string         `json:"reconciliationId"`
	Reconciliation   Reconciliation `json:"reconciliation"`
	// IdempotentReplay is true when the result was re-derived from the
	// completed idempotency reservation instead of a fresh proposal.
	IdempotentReplay bool `json:"idempotentReplay"`
}

// ConfirmReconciliationResult is the outcome of an atomic confirmation: the
// confirmed reconciliation, the immutable confirm event id and the idempotency
// marker.
type ConfirmReconciliationResult struct {
	ReconciliationID string         `json:"reconciliationId"`
	Reconciliation   Reconciliation `json:"reconciliation"`
	// ReconciliationEventID is the immutable 'confirm' event written for this
	// decision.
	ReconciliationEventID string `json:"reconciliationEventId"`
	// IdempotentReplay is true when the result was replayed from the completed
	// idempotency reservation instead of a fresh confirmation.
	IdempotentReplay bool `json:"idempotentReplay"`
}

// RejectReconciliationResult is the outcome of an atomic rejection: the
// rejected reconciliation (terminal), the immutable 'reject' event id and the
// idempotency marker. Rejection stores the human reason as the resolution and
// writes NO observation relation projection.
type RejectReconciliationResult struct {
	ReconciliationID string         `json:"reconciliationId"`
	Reconciliation   Reconciliation `json:"reconciliation"`
	// ReconciliationEventID is the immutable 'reject' event written for this
	// decision.
	ReconciliationEventID string `json:"reconciliationEventId"`
	// IdempotentReplay is true when the result was replayed from the completed
	// idempotency reservation instead of a fresh rejection.
	IdempotentReplay bool `json:"idempotentReplay"`
}

// WithdrawReconciliationResult is the outcome of an atomic withdrawal: the
// withdrawn reconciliation (terminal), the immutable 'withdraw' event id and
// the idempotency marker. Withdrawal is the proposer's provenance continuity
// act — it never carries an adjudicator.
type WithdrawReconciliationResult struct {
	ReconciliationID string         `json:"reconciliationId"`
	Reconciliation   Reconciliation `json:"reconciliation"`
	// ReconciliationEventID is the immutable 'withdraw' event written for this
	// withdrawal.
	ReconciliationEventID string `json:"reconciliationEventId"`
	// IdempotentReplay is true when the result was replayed from the completed
	// idempotency reservation instead of a fresh withdrawal.
	IdempotentReplay bool `json:"idempotentReplay"`
}

// ──────────────────────────────────────────────
// Pure lifecycle predicates
// ──────────────────────────────────────────────

// CanConfirmReconciliation reports whether a reconciliation in this status can
// be confirmed (proposed only).
func CanConfirmReconciliation(status ReconciliationStatus) bool {
	return status == ReconciliationProposed
}

// CanRejectReconciliation reports whether a reconciliation in this status can
// be rejected (proposed only).
func CanRejectReconciliation(status ReconciliationStatus) bool {
	return status == ReconciliationProposed
}

// CanWithdrawReconciliation reports whether a reconciliation in this status
// can be withdrawn by its proposer (proposed only).
func CanWithdrawReconciliation(status ReconciliationStatus) bool {
	return status == ReconciliationProposed
}

// CanSupersedeReconciliation reports whether a reconciliation in this status
// can be superseded (confirmed only — a correction supersedes its predecessor
// while atomically confirming).
func CanSupersedeReconciliation(status ReconciliationStatus) bool {
	return status == ReconciliationConfirmed
}

// IsLegalReconciliationTransition is the adjacency table of the reconciliation
// machine, independent of any stored reconciliation: proposed → confirmed|
// rejected|withdrawn|superseded; confirmed → superseded ONLY; terminal states
// never re-open.
func IsLegalReconciliationTransition(from, to ReconciliationStatus) bool {
	switch from {
	case ReconciliationProposed:
		return to == ReconciliationConfirmed || to == ReconciliationRejected ||
			to == ReconciliationWithdrawn || to == ReconciliationSuperseded
	case ReconciliationConfirmed:
		return to == ReconciliationSuperseded
	}
	return false
}

// ──────────────────────────────────────────────
// Pure lifecycle transitions (typed errors)
// ──────────────────────────────────────────────

// ConfirmReconciliation applies the pure lifecycle transition proposed →
// confirmed: it records the professional resolution, the canonical adjudicator
// snapshot, the policy version and the decision timestamp. This is the STATE
// MACHINE's pure transition — the authenticated act (hash comparison +
// authorization policy + persistence) is the store's job. Typed errors:
// INVALID_RECONCILIATION_TRANSITION (status), RESOLUTION_REQUIRED (empty
// resolution), AUTHENTICATION_REQUIRED (nil adjudicator).
func ConfirmReconciliation(r *Reconciliation, resolution string, adjudicator *auth.PrincipalSnapshot, policyVersion, decidedAt string) error {
	if !CanConfirmReconciliation(r.Status) {
		return fmt.Errorf("%w: %s → confirmed is not legal from status %q", auth.ErrInvalidReconciliationTransition, r.ID, r.Status)
	}
	if strings.TrimSpace(resolution) == "" {
		return fmt.Errorf("%w: confirmation requires a non-empty human resolution", auth.ErrResolutionRequired)
	}
	if adjudicator == nil {
		return fmt.Errorf("%w: confirmation requires an authenticated principal", auth.ErrAuthenticationRequired)
	}
	r.Status = ReconciliationConfirmed
	r.Resolution = resolution
	r.Adjudicator = adjudicator
	r.PolicyVersion = policyVersion
	r.DecidedAt = decidedAt
	return nil
}

// RejectReconciliation applies the pure lifecycle transition proposed →
// rejected (terminal). The human reason is stored as the resolution — the
// proposal reason is never silently promoted into a professional resolution.
// Typed errors: INVALID_RECONCILIATION_TRANSITION, RESOLUTION_REQUIRED,
// AUTHENTICATION_REQUIRED.
func RejectReconciliation(r *Reconciliation, reason string, adjudicator *auth.PrincipalSnapshot, policyVersion, decidedAt string) error {
	if !CanRejectReconciliation(r.Status) {
		return fmt.Errorf("%w: %s → rejected is not legal from status %q", auth.ErrInvalidReconciliationTransition, r.ID, r.Status)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: rejection requires a non-empty human reason", auth.ErrResolutionRequired)
	}
	if adjudicator == nil {
		return fmt.Errorf("%w: rejection requires an authenticated principal", auth.ErrAuthenticationRequired)
	}
	r.Status = ReconciliationRejected
	r.Resolution = reason
	r.Adjudicator = adjudicator
	r.PolicyVersion = policyVersion
	r.DecidedAt = decidedAt
	return nil
}

// WithdrawReconciliation applies the pure lifecycle transition proposed →
// withdrawn (terminal). Proposer-identity verification (PROPOSAL_UNAUTHORIZED
// on mismatch) is the caller's provenance check; this function only guards the
// state machine.
func WithdrawReconciliation(r *Reconciliation, at string) error {
	if !CanWithdrawReconciliation(r.Status) {
		return fmt.Errorf("%w: %s → withdrawn is not legal from status %q", auth.ErrInvalidReconciliationTransition, r.ID, r.Status)
	}
	r.Status = ReconciliationWithdrawn
	r.DecidedAt = at
	return nil
}

// SupersedeReconciliation applies the pure lifecycle transition confirmed →
// superseded (terminal), routing readers from the predecessor to successorID.
// Only routing fields change: status and SupersedesID; every proposal/
// adjudication field stays byte-equal. DecidedAt keeps the original
// confirmation time.
func SupersedeReconciliation(r *Reconciliation, successorID, at string) error {
	if !CanSupersedeReconciliation(r.Status) {
		return fmt.Errorf("%w: %s → superseded is not legal from status %q", auth.ErrInvalidReconciliationTransition, r.ID, r.Status)
	}
	r.Status = ReconciliationSuperseded
	r.SupersedesID = successorID
	return nil
}

// ──────────────────────────────────────────────
// Canonical reconciliation hash
// ──────────────────────────────────────────────

// reconciliationHashPayload is the canonical JSON payload of
// ComputeReconciliationHash. The base fields are ALWAYS present (empty string
// when absent); the confirmed-only fields (resolution, adjudicator,
// policyVersion, decidedAt) appear ONLY for a confirmed reconciliation. The
// field ORDER is part of the byte contract — the TypeScript mirror
// (core/types.ts computeReconciliationHash, protocol-freeze batch) builds the
// object in this exact order so Go and TS hash identical bytes.
type reconciliationHashPayload struct {
	ID               string                  `json:"id"`
	TenantID         string                  `json:"tenantId"`
	CompanyID        string                  `json:"companyId"`
	FiscalPeriodID   string                  `json:"fiscalPeriodId"`
	LeftMemoryID     string                  `json:"leftMemoryId"`
	RightMemoryID    string                  `json:"rightMemoryId"`
	Method           string                  `json:"method"`
	Currency         string                  `json:"currency"`
	LeftAmountCents  int64                   `json:"leftAmountCents"`
	RightAmountCents int64                   `json:"rightAmountCents"`
	VarianceCents    int64                   `json:"varianceCents"`
	ToleranceCents   int64                   `json:"toleranceCents"`
	Status           ReconciliationStatus    `json:"status"`
	Proposer         canonicalSource         `json:"proposer"`
	ProposalReason   string                  `json:"proposalReason"`
	PredecessorID    string                  `json:"predecessorId"`
	ProposedAt       string                  `json:"proposedAt"`
	Resolution       string                  `json:"resolution,omitempty"`
	Adjudicator      *auth.PrincipalSnapshot `json:"adjudicator,omitempty"`
	PolicyVersion    string                  `json:"policyVersion,omitempty"`
	DecidedAt        string                  `json:"decidedAt,omitempty"`
}

// ComputeReconciliationHash is the canonical SHA-256 (hex) of a
// reconciliation's REVIEWED or CONFIRMED state, over canonical JSON
// (deterministic marshal; byte-identical in Go and the TypeScript mirror).
//
// Documented field coverage per status:
//   - PROPOSED (and every non-confirmed status): id, tenantId, companyId,
//     fiscalPeriodId ("" when absent), leftMemoryId, rightMemoryId, method,
//     currency, leftAmountCents, rightAmountCents, varianceCents,
//     toleranceCents, status, canonical proposer (sorted keys, empties
//     omitted), proposalReason, predecessorId ("" when absent), proposedAt.
//     Routing fields (supersedesId) NEVER participate.
//   - CONFIRMED: the base fields PLUS resolution, the canonical adjudicator
//     snapshot (sorted roles), policyVersion, status and decidedAt.
//
// Rejected/withdrawn/superseded reconciliations are hashed with the reviewed
// shape (decided fields never participate): confirm/reject compare the
// reviewed hash BEFORE deciding, and the confirmed hash is recorded once at
// confirmation time.
func ComputeReconciliationHash(r Reconciliation) string {
	payload := reconciliationHashPayload{
		ID:               r.ID,
		TenantID:         r.TenantID,
		CompanyID:        r.CompanyID,
		FiscalPeriodID:   r.FiscalPeriodID,
		LeftMemoryID:     r.LeftMemoryID,
		RightMemoryID:    r.RightMemoryID,
		Method:           r.Method,
		Currency:         r.Currency,
		LeftAmountCents:  r.LeftAmountCents,
		RightAmountCents: r.RightAmountCents,
		VarianceCents:    r.VarianceCents,
		ToleranceCents:   r.ToleranceCents,
		Status:           r.Status,
		Proposer:         canonicalProposer(r.Proposer),
		ProposalReason:   r.ProposalReason,
		PredecessorID:    r.PredecessorID,
		ProposedAt:       r.ProposedAt,
	}
	if r.Status == ReconciliationConfirmed {
		payload.Resolution = r.Resolution
		payload.Adjudicator = canonicalSnapshot(r.Adjudicator)
		payload.PolicyVersion = r.PolicyVersion
		payload.DecidedAt = r.DecidedAt
	}
	sum := sha256.Sum256([]byte(canonicalJSON(payload)))
	return hex.EncodeToString(sum[:])
}

// canonicalSnapshot returns a defensive copy of the adjudicator snapshot with
// sorted, deduplicated roles — the canonical form the hash covers (Go and TS
// bytes match; the snapshot factory already canonicalizes, but the hash
// contract must not depend on the caller). Defined in judgment.go; reused here
// so both hashes canonicalize the principal identically.
