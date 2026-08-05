// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module defines the accounting judgment
// model and its PURE lifecycle (v0.4.0 Step 2 — adjudicable conflicts; see
// docs/architecture/conflict-judgment-step2.md).
//
// An AccountingJudgment is a first-class adjudication act over two immutable
// observations — it is NOT a KindDecision memory. Agents/systems may propose and
// withdraw their OWN proposals (their Source is provenance only, never
// authority); only an auth.VerifiedApprovalPrincipal may confirm or reject.
//
// Legal states: proposed -> confirmed | rejected | withdrawn | superseded;
// confirmed -> superseded ONLY (atomically with the confirming correction);
// rejected/withdrawn/superseded are terminal.
//
// This module holds values, the pure transition machine and the canonical
// judgment hash. The atomic authenticated act (policy + hash comparison +
// persistence) lives in internal/store (batch B).
package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
)

// ──────────────────────────────────────────────
// JudgmentStatus — lifecycle state
// ──────────────────────────────────────────────

// JudgmentStatus is the lifecycle state of an accounting judgment
// (proposed | confirmed | rejected | withdrawn | superseded).
type JudgmentStatus string

const (
	// JudgmentProposed is a proposal awaiting an authenticated decision.
	JudgmentProposed JudgmentStatus = "proposed"
	// JudgmentConfirmed is a confirmed adjudication (resolution + adjudicator).
	JudgmentConfirmed JudgmentStatus = "confirmed"
	// JudgmentRejected is a rejected proposal (terminal; human reason recorded).
	JudgmentRejected JudgmentStatus = "rejected"
	// JudgmentWithdrawn is a proposal withdrawn by its own proposer (terminal).
	JudgmentWithdrawn JudgmentStatus = "withdrawn"
	// JudgmentSuperseded is a confirmed judgment corrected by a successor
	// (terminal; readers route to the successor).
	JudgmentSuperseded JudgmentStatus = "superseded"
)

// IsValidJudgmentStatus reports whether status is a known judgment status.
func IsValidJudgmentStatus(status JudgmentStatus) bool {
	switch status {
	case JudgmentProposed, JudgmentConfirmed, JudgmentRejected,
		JudgmentWithdrawn, JudgmentSuperseded:
		return true
	}
	return false
}

// ──────────────────────────────────────────────
// AccountingJudgment — the entity
// ──────────────────────────────────────────────

// AccountingJudgment is an adjudication act over two immutable observations:
// the pair, the proposable relation, the provenance-only proposer, and the
// authenticated decision (resolution, adjudicator snapshot, policy version).
// Routing (SupersedesID) and UpdatedAt are excluded from the judgment hash;
// every proposal/adjudication field is immutable once decided.
type AccountingJudgment struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenantId"`
	CompanyID      string `json:"companyId"`
	FiscalPeriodID string `json:"fiscalPeriodId,omitempty"`
	FromID         string `json:"fromId"`
	ToID           string `json:"toId"`
	Relation       Relation     `json:"relation"`
	Status         JudgmentStatus `json:"status"`
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
	// ProposedAt/UpdatedAt are RFC3339; DecidedAt is set only when decided.
	ProposedAt string `json:"proposedAt"`
	UpdatedAt  string `json:"updatedAt"`
	DecidedAt  string `json:"decidedAt,omitempty"`
}

// ──────────────────────────────────────────────
// Commands — VALUES ONLY, no principal fields
// ──────────────────────────────────────────────

// ProposeJudgmentCommand proposes a new adjudication over two observations.
// It deliberately carries NO subject/membership/role/actor-kind/assurance
// fields (compile-level contract): the proposer Source arrives separately as
// provenance-only caller context, and authority never travels in the payload.
type ProposeJudgmentCommand struct {
	FromID   string   `json:"fromId"`
	ToID     string   `json:"toId"`
	Relation Relation `json:"relation"`
	// Reason is the proposer's justification (REQUIRED, non-whitespace).
	Reason string `json:"reason"`
	// RequestID is the idempotency key scoped to (tenant, requestId).
	RequestID string `json:"requestId"`
	// PredecessorID names an existing judgment this proposal corrects.
	PredecessorID string `json:"predecessorId,omitempty"`
}

// ConfirmJudgmentCommand confirms a proposed judgment. Resolution is the
// professional human resolution (REQUIRED); ExpectedJudgmentHash is the
// reviewed proposed hash the adjudicator actually saw.
type ConfirmJudgmentCommand struct {
	JudgmentID           string `json:"judgmentId"`
	Resolution           string `json:"resolution"`
	ExpectedJudgmentHash string `json:"expectedJudgmentHash"`
	RequestID            string `json:"requestId"`
}

// RejectJudgmentCommand rejects a proposed judgment. Reason is the human
// reason, stored as the resolution (REQUIRED); ExpectedJudgmentHash is the
// reviewed proposed hash the adjudicator actually saw.
type RejectJudgmentCommand struct {
	JudgmentID           string `json:"judgmentId"`
	Reason               string `json:"reason"`
	ExpectedJudgmentHash string `json:"expectedJudgmentHash"`
	RequestID            string `json:"requestId"`
}

// WithdrawJudgmentCommand withdraws the caller's OWN proposed judgment
// (proposer-identity verification is the caller's provenance check — batch B).
type WithdrawJudgmentCommand struct {
	JudgmentID string `json:"judgmentId"`
	RequestID  string `json:"requestId"`
}

// ──────────────────────────────────────────────
// Proposable relations
// ──────────────────────────────────────────────

// ProposableRelations returns the six proposable judgment relations in a fixed
// order. conflicts_with remains a legacy sync/discovery marker: it can motivate
// a proposal but is neither accepted as a proposal relation nor removed
// automatically (design §3).
func ProposableRelations() []Relation {
	return []Relation{
		RelationSupports,
		RelationContradicts,
		RelationExplains,
		RelationReconciles,
		RelationReverses,
		RelationSupersedes,
	}
}

// IsProposableRelation reports whether relation is one of the six proposable
// relations (supports | contradicts | explains | reconciles | reverses |
// supersedes). related/conflicts_with/derived_from/... are never proposable.
func IsProposableRelation(r Relation) bool {
	switch r {
	case RelationSupports, RelationContradicts, RelationExplains,
		RelationReconciles, RelationReverses, RelationSupersedes:
		return true
	}
	return false
}

// ──────────────────────────────────────────────
// Pure lifecycle predicates
// ──────────────────────────────────────────────

// CanPropose reports whether a Source may propose a judgment: agent|system
// only. Humans propose nothing — their authority arrives as a verified
// principal at confirm/reject time, never as caller-declared source.
func CanPropose(source Source) bool {
	return source.ActorKind == ActorKindAgent || source.ActorKind == ActorKindSystem
}

// CanConfirm reports whether a judgment in this status can be confirmed
// (proposed only).
func CanConfirm(status JudgmentStatus) bool { return status == JudgmentProposed }

// CanRejectJudgment reports whether a judgment in this status can be rejected
// (proposed only). Named CanRejectJudgment because the v2 memory lifecycle
// already owns CanReject(MemoryStatus) and Go has no overloading — the
// judgment machine's predicate is this one.
func CanRejectJudgment(status JudgmentStatus) bool { return status == JudgmentProposed }

// CanWithdraw reports whether a judgment in this status can be withdrawn by its
// proposer (proposed only).
func CanWithdraw(status JudgmentStatus) bool { return status == JudgmentProposed }

// CanSupersedeConfirmed reports whether a judgment in this status can be
// superseded (confirmed only — a correction supersedes its predecessor while
// atomically confirming).
func CanSupersedeConfirmed(status JudgmentStatus) bool { return status == JudgmentConfirmed }

// IsLegalJudgmentTransition is the adjacency table of the judgment machine,
// independent of any stored judgment: proposed → confirmed|rejected|withdrawn|
// superseded; confirmed → superseded ONLY; terminal states never re-open.
func IsLegalJudgmentTransition(from, to JudgmentStatus) bool {
	switch from {
	case JudgmentProposed:
		return to == JudgmentConfirmed || to == JudgmentRejected ||
			to == JudgmentWithdrawn || to == JudgmentSuperseded
	case JudgmentConfirmed:
		return to == JudgmentSuperseded
	}
	return false
}

// ──────────────────────────────────────────────
// Pure lifecycle transitions (typed errors)
// ──────────────────────────────────────────────

// ConfirmJudgment applies the pure lifecycle transition proposed → confirmed:
// it records the professional resolution, the canonical adjudicator snapshot,
// the policy version and the decision timestamp. This is the STATE MACHINE's
// pure transition — the authenticated act (hash comparison + authorization
// policy + persistence) is the store's job (batch B). Typed errors:
// INVALID_JUDGMENT_TRANSITION (status), RESOLUTION_REQUIRED (empty resolution),
// AUTHENTICATION_REQUIRED (nil adjudicator — an anonymous act never confirms).
func ConfirmJudgment(j *AccountingJudgment, resolution string, adjudicator *auth.PrincipalSnapshot, policyVersion, decidedAt string) error {
	if !CanConfirm(j.Status) {
		return fmt.Errorf("%w: %s → confirmed is not legal from status %q", auth.ErrInvalidJudgmentTransition, j.ID, j.Status)
	}
	if strings.TrimSpace(resolution) == "" {
		return fmt.Errorf("%w: confirmation requires a non-empty human resolution", auth.ErrResolutionRequired)
	}
	if adjudicator == nil {
		return fmt.Errorf("%w: confirmation requires an authenticated principal", auth.ErrAuthenticationRequired)
	}
	j.Status = JudgmentConfirmed
	j.Resolution = resolution
	j.Adjudicator = adjudicator
	j.PolicyVersion = policyVersion
	j.DecidedAt = decidedAt
	j.UpdatedAt = decidedAt
	return nil
}

// RejectJudgment applies the pure lifecycle transition proposed → rejected
// (terminal). The human reason is stored as the resolution — the proposal
// reason is never silently promoted into a professional resolution. Typed
// errors: INVALID_JUDGMENT_TRANSITION, RESOLUTION_REQUIRED,
// AUTHENTICATION_REQUIRED.
func RejectJudgment(j *AccountingJudgment, reason string, adjudicator *auth.PrincipalSnapshot, policyVersion, decidedAt string) error {
	if !CanRejectJudgment(j.Status) {
		return fmt.Errorf("%w: %s → rejected is not legal from status %q", auth.ErrInvalidJudgmentTransition, j.ID, j.Status)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: rejection requires a non-empty human reason", auth.ErrResolutionRequired)
	}
	if adjudicator == nil {
		return fmt.Errorf("%w: rejection requires an authenticated principal", auth.ErrAuthenticationRequired)
	}
	j.Status = JudgmentRejected
	j.Resolution = reason
	j.Adjudicator = adjudicator
	j.PolicyVersion = policyVersion
	j.DecidedAt = decidedAt
	j.UpdatedAt = decidedAt
	return nil
}

// WithdrawJudgment applies the pure lifecycle transition proposed → withdrawn
// (terminal). Proposer-identity verification (PROPOSAL_UNAUTHORIZED on
// mismatch) is the caller's provenance check; this function only guards the
// state machine.
func WithdrawJudgment(j *AccountingJudgment, at string) error {
	if !CanWithdraw(j.Status) {
		return fmt.Errorf("%w: %s → withdrawn is not legal from status %q", auth.ErrInvalidJudgmentTransition, j.ID, j.Status)
	}
	j.Status = JudgmentWithdrawn
	j.UpdatedAt = at
	return nil
}

// SupersedeJudgment applies the pure lifecycle transition confirmed → superseded
// (terminal), routing readers from the predecessor to successorID. Only routing
// fields change: status, SupersedesID and UpdatedAt; every proposal/adjudication
// field stays byte-equal. DecidedAt keeps the original confirmation time.
func SupersedeJudgment(j *AccountingJudgment, successorID, at string) error {
	if !CanSupersedeConfirmed(j.Status) {
		return fmt.Errorf("%w: %s → superseded is not legal from status %q", auth.ErrInvalidJudgmentTransition, j.ID, j.Status)
	}
	j.Status = JudgmentSuperseded
	j.SupersedesID = successorID
	j.UpdatedAt = at
	return nil
}

// ──────────────────────────────────────────────
// Canonical judgment hash
// ──────────────────────────────────────────────

// judgmentHashPayload is the canonical JSON payload of ComputeJudgmentHash. The
// base fields are ALWAYS present (empty string when absent); the confirmed-only
// fields (resolution, adjudicator, policyVersion, decidedAt) appear ONLY for a
// confirmed judgment. The field ORDER is part of the byte contract — the
// TypeScript mirror (core/types.ts computeJudgmentHash) builds the object in
// this exact order so Go and TS hash identical bytes.
type judgmentHashPayload struct {
	ID             string         `json:"id"`
	TenantID       string         `json:"tenantId"`
	CompanyID      string         `json:"companyId"`
	FiscalPeriodID string         `json:"fiscalPeriodId"`
	FromID         string         `json:"fromId"`
	ToID           string         `json:"toId"`
	Relation       Relation       `json:"relation"`
	Status         JudgmentStatus `json:"status"`
	Proposer       canonicalSource `json:"proposer"`
	ProposalReason string         `json:"proposalReason"`
	PredecessorID  string         `json:"predecessorId"`
	ProposedAt     string         `json:"proposedAt"`
	Resolution     string         `json:"resolution,omitempty"`
	Adjudicator    *auth.PrincipalSnapshot `json:"adjudicator,omitempty"`
	PolicyVersion  string         `json:"policyVersion,omitempty"`
	DecidedAt      string         `json:"decidedAt,omitempty"`
}

// canonicalSource is the canonical JSON shape of a judgment proposer: keys
// sorted alphabetically (actorId, actorKind, model, reference, session,
// system), empty optional fields omitted — byte-identical with the TypeScript
// mirror.
type canonicalSource struct {
	ActorID   string    `json:"actorId,omitempty"`
	ActorKind ActorKind `json:"actorKind"`
	Model     string    `json:"model,omitempty"`
	Reference string    `json:"reference,omitempty"`
	Session   string    `json:"session,omitempty"`
	System    string    `json:"system"`
}

func canonicalProposer(s Source) canonicalSource {
	return canonicalSource{
		ActorID:   s.ActorID,
		ActorKind: s.ActorKind,
		Model:     s.Model,
		Reference: s.Reference,
		Session:   s.Session,
		System:    s.System,
	}
}

// canonicalSnapshot returns a defensive copy of the adjudicator snapshot with
// sorted, deduplicated roles — the canonical form the hash covers (Go and TS
// bytes match; the snapshot factory already canonicalizes, but the hash
// contract must not depend on the caller).
func canonicalSnapshot(s *auth.PrincipalSnapshot) *auth.PrincipalSnapshot {
	if s == nil {
		return nil
	}
	seen := make(map[auth.AccountingRole]struct{}, len(s.Roles))
	roles := make([]auth.AccountingRole, 0, len(s.Roles))
	for _, r := range s.Roles {
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		roles = append(roles, r)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	cp := *s
	cp.Roles = roles
	return &cp
}

// ComputeJudgmentHash is the canonical SHA-256 (hex) of a judgment's REVIEWED
// or CONFIRMED state, over canonical JSON (deterministic marshal; byte-identical
// in Go and the TypeScript mirror).
//
// Documented field coverage per status:
//   - PROPOSED (and every non-confirmed status): id, tenantId, companyId,
//     fiscalPeriodId ("" when absent), fromId, toId, relation, status, canonical
//     proposer (sorted keys, empties omitted), proposalReason, predecessorId
//     ("" when absent), proposedAt. Routing fields (supersedesId) and updatedAt
//     NEVER participate.
//   - CONFIRMED: the base fields PLUS resolution, the canonical adjudicator
//     snapshot (sorted roles), policyVersion, status and decidedAt.
//
// Rejected/withdrawn/superseded judgments are hashed with the reviewed shape
// (decided fields never participate): confirm/reject compare the reviewed hash
// BEFORE deciding, and the confirmed hash is recorded once at confirmation time.
func ComputeJudgmentHash(j AccountingJudgment) string {
	payload := judgmentHashPayload{
		ID:             j.ID,
		TenantID:       j.TenantID,
		CompanyID:      j.CompanyID,
		FiscalPeriodID: j.FiscalPeriodID,
		FromID:         j.FromID,
		ToID:           j.ToID,
		Relation:       j.Relation,
		Status:         j.Status,
		Proposer:       canonicalProposer(j.Proposer),
		ProposalReason: j.ProposalReason,
		PredecessorID:  j.PredecessorID,
		ProposedAt:     j.ProposedAt,
	}
	if j.Status == JudgmentConfirmed {
		payload.Resolution = j.Resolution
		payload.Adjudicator = canonicalSnapshot(j.Adjudicator)
		payload.PolicyVersion = j.PolicyVersion
		payload.DecidedAt = j.DecidedAt
	}
	sum := sha256.Sum256([]byte(canonicalJSON(payload)))
	return hex.EncodeToString(sum[:])
}

// canonicalJSON marshals v to compact JSON WITHOUT HTML escaping (Go escapes
// <,>,& by default, JSON.stringify in the mirror does not — disabling it keeps
// the bytes identical for the same data). The payload is fixed to strings and
// structs, so Encode cannot fail; a failure would be an internal invariant
// violation and fails closed via panic.
func canonicalJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(fmt.Sprintf("judgment hash: canonical marshal failed: %v", err))
	}
	return strings.TrimRight(buf.String(), "\n")
}
