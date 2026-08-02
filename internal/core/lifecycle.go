// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module implements the observation
// lifecycle (contracts/lifecycle.md); the memory model has no monetary fields,
// so no money value is computed here.
//
// Observation lifecycle and vigencia enforcement — mirrors
// lifecycle/transitions.ts semantically.
//
// Legal state machine for this slice — ONLY adjacent forward transitions:
//
//	draft ──► reviewed ──► promoted ──► superseded
//
// Anything else fails with INVALID_TRANSITION. This is deliberately STRICTER
// than the 0.1-draft contract table (which also lists promote-from-draft):
// unknown or non-adjacent transitions fail closed, and re-submission is always
// a new revision, never an in-place edit.
//
// Supersede requires a target observation id and records a `supersedes`
// relation on the store (from the superseded observation to its replacement),
// marking the superseded observation's status. It never auto-promotes the
// replacement — promotion is explicit (contracts/lifecycle.md rule 1).

package core

import "fmt"

const (
	// ErrInvalidTransition is returned for any transition outside the legal
	// chain draft → reviewed → promoted → superseded.
	ErrInvalidTransition = "INVALID_TRANSITION"
	// ErrInvalidSupersedeTarget is returned when supersede has no valid target.
	ErrInvalidSupersedeTarget = "INVALID_SUPERSEDE_TARGET"
)

// legalTransitions maps each state to its single legal adjacent-forward
// successor.
var legalTransitions = map[AuthorityStatus]AuthorityStatus{
	StatusDraft:    StatusReviewed,
	StatusReviewed: StatusPromoted,
	StatusPromoted: StatusSuperseded,
}

// IsLegalTransition reports whether (from → to) is an adjacent forward
// transition in the legal chain.
func IsLegalTransition(from, to AuthorityStatus) bool {
	return legalTransitions[from] == to
}

// TransitionAuthority guards a transition: it fails closed with
// INVALID_TRANSITION for anything outside the legal chain.
func TransitionAuthority(from, to AuthorityStatus) error {
	if !IsLegalTransition(from, to) {
		return fmt.Errorf("%s: %s → %s is not legal — the only legal chain is draft → reviewed → promoted → superseded", ErrInvalidTransition, from, to)
	}
	return nil
}

// TransitionMeta carries the actor+timestamp of a lifecycle event
// (contracts/provenance.md rule 3: every state traces to actor+time).
type TransitionMeta struct {
	Actor     string
	Timestamp string
}

// LifecycleStore is the narrow, consumer-side view of the store the lifecycle
// machine needs. internal/store.Store (and its SQLiteStore) satisfies it
// structurally; it is declared here so package core stays free of an import
// cycle (store imports core for types).
type LifecycleStore interface {
	FindByID(id string) (Observation, bool)
	ApplyStatusTransition(observationID string, to AuthorityStatus, meta TransitionMeta) (Observation, error)
	Relate(fromID, toID string, relation Relation, meta *RelationMeta) error
	SuccessorOf(observationID string) (Observation, bool)
}

// ApplyTransition applies a legal status transition to a stored observation,
// recording an audit-trail entry. The observation is found first so that an
// illegal transition leaves the stored observation unchanged.
func ApplyTransition(s LifecycleStore, observationID string, to AuthorityStatus, meta TransitionMeta) (Observation, error) {
	observation, ok := s.FindByID(observationID)
	if !ok {
		return Observation{}, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", observationID)
	}
	if err := TransitionAuthority(observation.AuthorityStatus, to); err != nil {
		return Observation{}, err
	}
	return s.ApplyStatusTransition(observationID, to, meta)
}

// SupersedeInput names the superseded observation and its REQUIRED replacement
// target.
type SupersedeInput struct {
	Store         LifecycleStore
	ObservationID string
	// TargetID is the REQUIRED replacing observation this one routes readers to.
	TargetID string
	Actor    string
	// Timestamp of the supersede event.
	Timestamp string
}

// Supersede marks a promoted observation `superseded` and records a
// `supersedes` relation to the target. It never edits content in place and
// never auto-promotes the replacement (contracts/lifecycle.md rule 1).
func Supersede(input SupersedeInput) (Observation, error) {
	if input.ObservationID == input.TargetID {
		return Observation{}, fmt.Errorf("%s: observation %s cannot supersede itself", ErrInvalidSupersedeTarget, input.ObservationID)
	}
	target, ok := input.Store.FindByID(input.ObservationID)
	if !ok {
		return Observation{}, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", input.ObservationID)
	}
	if _, ok := input.Store.FindByID(input.TargetID); !ok {
		return Observation{}, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", input.TargetID)
	}
	if err := TransitionAuthority(target.AuthorityStatus, StatusSuperseded); err != nil {
		return Observation{}, err
	}
	updated, err := input.Store.ApplyStatusTransition(input.ObservationID, StatusSuperseded, TransitionMeta{
		Actor:     input.Actor,
		Timestamp: input.Timestamp,
	})
	if err != nil {
		return Observation{}, err
	}
	if err := input.Store.Relate(input.ObservationID, input.TargetID, RelationSupersedes, &RelationMeta{
		Actor:     input.Actor,
		Timestamp: input.Timestamp,
	}); err != nil {
		return Observation{}, err
	}
	return updated, nil
}
