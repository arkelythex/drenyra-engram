// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module implements the v2 memory
// lifecycle (contracts/lifecycle.md) with the mandatory human-approval gate for
// memories with fiscal effect; no money value is computed here.
//
// v2 lifecycle (approved design):
//
//	save (fiscalEffect == none)  ─────────────► active            (informative)
//	save (fiscalEffect != none)  ─────────────► pending_review    (GATE)
//	pending_review ──approve(human)──► approved
//	pending_review ──reject(human)───► rejected
//	pending_review ──return(human)──► returned      (NON-terminal, v0.9.0)
//	returned ──agent Save (new revision)──► pending_review   (new revision)
//	active | pending_review | approved ──void(human|system)──► voided
//	active | pending_review | approved | returned ──supersede(save)──► superseded
//
// GATE semantics: a memory with fiscal effect can only reach `approved` through
// a HUMAN actor (source.ActorKind == human). Agents and systems may CREATE a
// pending_review memory (they record; they never approve). Voiding admits human
// or system actors (systemic correction), never agents. rejected, superseded and
// voided are terminal. returned is NON-terminal: the reviewer sends the
// proposal back for correction and an agent Save on the returned memory creates
// a NEW revision that re-enters pending_review — the returned revision itself
// never reopens.
//
// The AI assists; the system validates; the professional reviews; the evidence
// remains. Memory informs decisions — it never authorizes them
// (contracts/provenance.md non-authorization boundary).
package core

import (
	"errors"
	"fmt"
)

// Gate error — the mandatory human-approval gate.
var (
	// ErrGateRequiresHuman is returned when a gated lifecycle operation
	// (approve/reject) is attempted by a non-human actor.
	ErrGateRequiresHuman = errors.New("GATE_REQUIRES_HUMAN: approve/reject requires a human actor (source.actorKind == human)")
	// ErrInvalidTransition is returned for any transition outside the legal
	// v2 lifecycle.
	ErrInvalidTransition = errors.New("INVALID_TRANSITION: not a legal v2 lifecycle transition for this status")
)

// InitialStatus derives the save-time status from the fiscal effect: none →
// active (informative, no gate); any non-none effect → pending_review
// (mandatory human approval gate).
func InitialStatus(fe FiscalEffect) MemoryStatus {
	if fe == FiscalEffectNone {
		return StatusActive
	}
	return StatusPendingReview
}

// IsGated reports whether the fiscal effect triggers the human-approval gate.
func IsGated(fe FiscalEffect) bool {
	return fe != FiscalEffectNone
}

// CanApprove reports whether a memory in this status can be approved
// (pending_review only — active memories are already effective informatives,
// and terminal states never reopen).
func CanApprove(status MemoryStatus) bool {
	return status == StatusPendingReview
}

// CanReject reports whether a memory in this status can be rejected
// (pending_review only).
func CanReject(status MemoryStatus) bool {
	return status == StatusPendingReview
}

// CanReturn reports whether a memory in this status can be RETURNED to its
// proposer for correction (v0.9.0 review workspace — pending_review only).
func CanReturn(status MemoryStatus) bool {
	return status == StatusPendingReview
}

// IsLegalV2Transition reports whether (from → to) is a legal v2 lifecycle
// transition pair, independent of any stored memory. It is the adjacency table
// the audit-trail import and the sync replay validate against: the v2 machine
// has NO linear chain (approve/reject/return only from pending_review;
// void/supersede from active|pending_review|approved; a returned memory only
// leaves via a NEW revision's supersede; terminal states never reopen), so a
// crafted record such as active→approved or approved→rejected is rejected here
// (never jumps states or fabricates provenance).
func IsLegalV2Transition(from, to MemoryStatus) bool {
	switch from {
	case StatusPendingReview:
		return to == StatusApproved || to == StatusRejected || to == StatusReturned || to == StatusVoided || to == StatusSuperseded
	case StatusReturned:
		// A returned revision is superseded by the agent's correction save (a NEW
		// revision that re-enters pending_review); it never reopens by itself.
		return to == StatusSuperseded
	case StatusActive:
		return to == StatusVoided || to == StatusSuperseded
	case StatusApproved:
		return to == StatusVoided || to == StatusSuperseded
	}
	return false
}

// CanVoid reports whether a memory in this status can be voided
// (active | pending_review | approved).
func CanVoid(status MemoryStatus) bool {
	switch status {
	case StatusActive, StatusPendingReview, StatusApproved:
		return true
	}
	return false
}

// AssertHumanApproval fails closed with ErrGateRequiresHuman when the actor is
// not a human — the non-negotiable approval boundary.
func AssertHumanApproval(actor Source) error {
	if actor.ActorKind != ActorKindHuman {
		return ErrGateRequiresHuman
	}
	return nil
}

// Approve transitions a pending_review memory to approved. REQUIRES a human
// actor; fails closed otherwise. Mutates the memory in place (the caller owns
// persistence of the transition).
//
// Deprecated: v0.4.0 Step 1 replaced this low-level guard with the
// authenticated approval path (ADR-003). New code MUST approve through
// internal/server.ApproveMemory → store.SQLiteStore.ApproveMemory, which
// verifies the principal, recomputes the envelope hash, runs the versioned
// policy and persists the immutable approval event atomically. This function
// remains ONLY for v0.3 consumers; it is not reachable from the new approval
// transports.
func Approve(m *AccountingMemory, actor Source) error {
	if !CanApprove(m.Status) {
		return fmt.Errorf("%w: %s → approved is not legal from status %q", ErrInvalidTransition, m.Identity.ID, m.Status)
	}
	if err := AssertHumanApproval(actor); err != nil {
		return err
	}
	m.Status = StatusApproved
	return nil
}

// Reject transitions a pending_review memory to rejected (terminal). REQUIRES a
// human actor.
func Reject(m *AccountingMemory, actor Source) error {
	if !CanReject(m.Status) {
		return fmt.Errorf("%w: %s → rejected is not legal from status %q", ErrInvalidTransition, m.Identity.ID, m.Status)
	}
	if err := AssertHumanApproval(actor); err != nil {
		return err
	}
	m.Status = StatusRejected
	return nil
}

// Return transitions a pending_review memory to returned (NON-terminal — the
// proposer/agent corrects via a NEW revision that re-enters pending_review).
// REQUIRES a human actor.
//
// Deprecated-style low-level guard: the v0.9.0 review workspace decides
// through the AUTHENTICATED path (server.ReturnMemory →
// store.SQLiteStore.ReturnMemory), which verifies the principal, recomputes the
// envelope hash, enforces SoD and persists the immutable return event + receipt
// atomically. This function remains ONLY for the shared lifecycle machine and
// legacy consumers; the new review transports never call it.
func Return(m *AccountingMemory, actor Source) error {
	if !CanReturn(m.Status) {
		return fmt.Errorf("%w: %s → returned is not legal from status %q", ErrInvalidTransition, m.Identity.ID, m.Status)
	}
	if err := AssertHumanApproval(actor); err != nil {
		return err
	}
	m.Status = StatusReturned
	return nil
}

// Void transitions an active | pending_review | approved memory to voided
// (terminal, no successor). Admits human or system actors (systemic
// correction); NEVER an agent — an autonomous agent cannot annul professional
// or approved content.
func Void(m *AccountingMemory, actor Source) error {
	if !CanVoid(m.Status) {
		return fmt.Errorf("%w: %s → voided is not legal from status %q", ErrInvalidTransition, m.Identity.ID, m.Status)
	}
	if actor.ActorKind == ActorKindAgent {
		return errors.New("GATE_AGENT_CANNOT_VOID: voiding requires a human or system actor, never an agent")
	}
	m.Status = StatusVoided
	return nil
}

// SupersedePrev marks a previously current memory superseded and routes readers
// to successorID. Only active | pending_review | approved | returned memories
// can be superseded (a RETURNED revision is superseded by the agent's
// correction save — v0.9.0 review workspace); terminal states never re-open.
func SupersedePrev(prev *AccountingMemory, successorID string) error {
	switch prev.Status {
	case StatusActive, StatusPendingReview, StatusApproved, StatusReturned:
		// legal — proceed
	default:
		return fmt.Errorf("%w: %s → superseded is not legal from status %q", ErrInvalidTransition, prev.Identity.ID, prev.Status)
	}
	prev.Status = StatusSuperseded
	prev.SupersedesID = successorID
	return nil
}

// TransitionMeta carries the actor+timestamp of a lifecycle event
// (contracts/provenance.md rule 3: every state traces to actor+time).
type TransitionMeta struct {
	Actor     string
	ActorKind ActorKind
	Timestamp string
}

// LifecycleStore is the narrow, consumer-side view of the store the lifecycle
// machine needs. internal/store.Store (and its SQLiteStore) satisfies it
// structurally; it is declared here so package core stays free of an import
// cycle (store imports core for types).
type LifecycleStore interface {
	FindByID(id string) (AccountingMemory, bool)
	ApplyStatusTransition(memoryID string, to MemoryStatus, meta TransitionMeta) (AccountingMemory, error)
	Relate(fromID, toID string, relation Relation, meta *RelationMeta) error
	SuccessorOf(memoryID string) (AccountingMemory, bool)
}
