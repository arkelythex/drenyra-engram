// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module syncs observations between
// stores; the model has no monetary fields (content is structured text), so no
// money value is merged or computed here.
//
// Sync — additive, provenance-preserving, conflict-visible reconciliation.
// docs/architecture.md: "Local and cloud stores sync with explicit semantics:
// tombstone-aware, provenance-preserving, and conflict-visible (conflicts are
// surfaced for human/relation review — never silently resolved)."
//
// The store forbids DELETE and rewriting (immutability triggers), so sync is
// ADDITIVE by construction: it imports the source's full revision history and
// never removes or edits anything in the sink. Divergence is SURFACED, never
// auto-resolved:
//
//   - Immutable conflict: an id exists in the sink with different immutable
//     bytes — the source's row is skipped and reported (never overwritten).
//   - Divergent chain: source-head and sink-head of the same (topicKey, exact
//     scope) chain differ — both histories are preserved (nothing is lost) and
//     the two heads are linked with a `conflicts_with` relation plus a report
//     entry, for human review.
//   - Illegal transition replay: a lifecycle record cannot be replayed on the
//     sink (its state diverged) — reported, never forced.
//
// Lifecycle converges via TRANSITION REPLAY, not by editing observations:
// imported audit-trail records are replayed in order through the lifecycle
// machine, so promotion state propagates with provenance intact and illegal
// moves fail closed.
//
// Cloud is explicitly out of scope for this slice (ROADMAP non-goals: cloud
// offering deferred to arkelythex/drenyra-cloud). Sync operates on any pair of
// stores (file paths today; an HTTP transport can reuse the same engine).

package sync

import (
	"strings"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// Source is the read surface of the synced-away store; the SQLite store
// satisfies it structurally (consumer-side interface, repo pattern).
type Source interface {
	List() ([]core.AccountingMemory, error)
	Relations() ([]core.RelationRecord, error)
	TransitionLog() ([]core.StatusTransitionRecord, error)
}

// Sink is the read+write surface of the synced-into store. ImportObservation
// and ImportTransition are the store's verbatim, validated, idempotent imports
// (internal/store); the lifecycle machine (core.ApplyTransition) is the single
// status mutation path, so replay legality fails closed.
type Sink interface {
	Source
	FindByID(id string) (core.AccountingMemory, bool)
	FindByTopicKey(topicKey string, scope core.Scope) (core.AccountingMemory, bool)
	ImportObservation(observation core.AccountingMemory) (bool, error)
	ImportTransition(record core.StatusTransitionRecord) (bool, error)
	// ApplyImportedStatus advances status WITHOUT logging (the audit row is
	// imported separately) — sync-only, forward-only by contract.
	ApplyImportedStatus(memoryID string, to core.MemoryStatus, meta core.TransitionMeta) (core.AccountingMemory, error)
	Relate(fromID, toID string, relation core.Relation, meta *core.RelationMeta) error
	RelationBetween(fromID, toID string) (string, bool)
}

// Options configures a sync run.
type Options struct {
	// Actor is recorded on the conflict relations the sync records.
	Actor string
	// Timestamp is recorded on the conflict relations (UTC ISO-8601).
	Timestamp string
}

// ConflictKind discriminates the surfaced divergences.
type ConflictKind string

const (
	// ConflictDivergentChain: source-head and sink-head of the same
	// (topicKey, exact scope) chain differ — both histories are preserved and
	// the heads are linked conflicts_with for human review.
	ConflictDivergentChain ConflictKind = "divergent_chain"
	// ConflictImmutable: an id exists in the sink with different immutable
	// bytes; the source row was skipped, never overwritten.
	ConflictImmutable ConflictKind = "immutable_conflict"
)

// Conflict is one surfaced divergence (never silently resolved).
type Conflict struct {
	Kind      ConflictKind `json:"kind"`
	TopicKey  string       `json:"topicKey,omitempty"`
	Scope     core.Scope   `json:"scope,omitempty"`
	LocalID   string       `json:"localId,omitempty"`  // sink-side observation
	RemoteID  string       `json:"remoteId,omitempty"` // source-side observation
	LocalRev  int          `json:"localRevision,omitempty"`
	RemoteRev int          `json:"remoteRevision,omitempty"`
	Reason    string       `json:"reason,omitempty"`
}

// Report is the JSON shape of a sync run.
type Report struct {
	ObservationsImported      int        `json:"observationsImported"`
	ObservationsSkipped       int        `json:"observationsSkipped"`
	RelationsImported         int        `json:"relationsImported"`
	TransitionsImported       int        `json:"transitionsImported"`
	TransitionsReplayed       int        `json:"transitionsReplayed"`
	ConflictRelationsRecorded int        `json:"conflictRelationsRecorded"`
	Conflicts                 []Conflict `json:"conflicts"`
}

// Sync reconciles from into to: additive, idempotent, provenance-preserving
// and conflict-visible. Re-running with the same pair is a no-op. It never
// deletes, edits, or silently resolves anything.
func Sync(from Source, to Sink, opts Options) (Report, error) {
	if opts.Actor == "" {
		opts.Actor = "sync"
	}
	if opts.Timestamp == "" {
		opts.Timestamp = nowISO()
	}

	report := Report{Conflicts: []Conflict{}}

	sourceObservations, err := from.List()
	if err != nil {
		return report, err
	}

	// ── 1. Divergent-chain detection runs FIRST (before any import) so the
	// divergence is captured even though both histories are preserved below.
	divergent := detectDivergentChains(sourceObservations, to)

	// ── 2. Observations: verbatim, validated, idempotent import.
	for _, observation := range sourceObservations {
		imported, err := to.ImportObservation(observation)
		if err != nil {
			if isImmutableConflict(err) {
				report.Conflicts = append(report.Conflicts, Conflict{
					Kind:     ConflictImmutable,
					TopicKey: observation.Identity.TopicKey,
					Scope:    observation.Scope,
					LocalID:  observation.Identity.ID,
					Reason:   err.Error(),
				})
				continue // surfaced, never fatal, never overwritten
			}
			return report, err // any other failure fails closed
		}
		if imported {
			report.ObservationsImported++
		} else {
			report.ObservationsSkipped++
		}
	}

	// ── 3. Relations: Relate is idempotent; count only new edges.
	sourceRelations, err := from.Relations()
	if err != nil {
		return report, err
	}
	for _, relation := range sourceRelations {
		if _, ok := to.RelationBetween(relation.FromID, relation.ToID); ok {
			continue
		}
		if err := to.Relate(relation.FromID, relation.ToID, relation.Relation, &core.RelationMeta{
			Actor:     relation.Actor,
			Timestamp: relation.Timestamp,
		}); err != nil {
			// A relation referencing an observation the sink does not have is a
			// source-consistency issue — fail closed (never half-apply).
			return report, err
		}
		report.RelationsImported++
	}

	// ── 4. Audit trail: VERBATIM record import + forward-only status
	// convergence. The records are history — they cross stores as-is (the sink
	// observation was imported at its final status, so nothing is replayed
	// backward and no phantom conflict arises). Status convergence is a
	// FORWARD-ONLY advance when the sink is BEHIND the record's target (a
	// lagging sink, e.g. synced while draft and the source advanced later);
	// the advance is log-less because the imported record IS the audit row.
	// TransitionLog returns insertion order, so advances follow the source's
	// own adjacent sequence.
	sourceTransitions, err := from.TransitionLog()
	if err != nil {
		return report, err
	}
	for _, record := range sourceTransitions {
		imported, err := to.ImportTransition(record)
		if err != nil {
			return report, err
		}
		if !imported {
			continue // identical record already present — no-op
		}
		report.TransitionsImported++
		// Replay only when the record's target is a LEGAL v2 transition from the
		// sink's CURRENT state (core.IsLegalV2Transition): terminal states never
		// reopen, and a human-approved memory is never silently demoted to
		// rejected by a stale or crafted source record.
		if observation, ok := to.FindByID(record.MemoryID); ok &&
			core.IsLegalV2Transition(observation.Status, record.To) {
			if _, err := to.ApplyImportedStatus(record.MemoryID, record.To, core.TransitionMeta{
				Actor:     record.Actor,
				Timestamp: record.Timestamp,
			}); err != nil {
				return report, err // persistence failure — fail closed
			}
			report.TransitionsReplayed++
		}
	}

	// ── 5. Record the divergent-chain heads as a visible conflicts_with
	// relation (idempotent; the report entry already surfaced it).
	for _, d := range divergent {
		report.Conflicts = append(report.Conflicts, Conflict{
			Kind:      ConflictDivergentChain,
			TopicKey:  d.topicKey,
			Scope:     d.scope,
			LocalID:   d.localHead.Identity.ID,
			RemoteID:  d.remoteHead.Identity.ID,
			LocalRev:  d.localHead.Revision,
			RemoteRev: d.remoteHead.Revision,
		})
		if relation, ok := to.RelationBetween(d.localHead.Identity.ID, d.remoteHead.Identity.ID); ok && relation == string(core.RelationConflictsWith) {
			continue // already linked as a conflict
		}
		if err := to.Relate(d.localHead.Identity.ID, d.remoteHead.Identity.ID, core.RelationConflictsWith, &core.RelationMeta{
			Actor:     opts.Actor,
			Timestamp: opts.Timestamp,
		}); err == nil {
			report.ConflictRelationsRecorded++
		}
		// A failure to record the relation is not fatal: the report entry is
		// the durable surface for human review.
	}

	return report, nil
}

// divergentChain is one source-head / sink-head divergence detected pre-import.
type divergentChain struct {
	topicKey   string
	scope      core.Scope
	localHead  core.AccountingMemory // sink head (current, pre-import)
	remoteHead core.AccountingMemory // source head
}

// detectDivergentChains compares, per (topicKey, exact scope) chain, the
// source's head with the sink's CURRENT head. A divergence exists ONLY when
// the sink's head is NOT part of the source's chain history — the two stores
// evolved the same chain independently. A sink whose head IS an ancestor
// within the source's chain is merely LAGGING (fast-forward): the import
// converges it, and reporting a conflict there would be a false positive
// (with a permanent, undeletable conflicts_with relation to show for it).
func detectDivergentChains(source []core.AccountingMemory, to Sink) []divergentChain {
	chainIDs := make(map[string]map[string]bool)
	heads := make(map[string]core.AccountingMemory)
	for _, observation := range source {
		key := chainKey(observation)
		if chainIDs[key] == nil {
			chainIDs[key] = make(map[string]bool)
		}
		chainIDs[key][observation.Identity.ID] = true
		if current, ok := heads[key]; !ok || observation.Revision > current.Revision {
			heads[key] = observation
		}
	}
	divergent := make([]divergentChain, 0)
	for _, remoteHead := range heads {
		localHead, ok := to.FindByTopicKey(remoteHead.Identity.TopicKey, remoteHead.Scope)
		if !ok {
			continue // sink has nothing on this chain — fast-forward, not divergence
		}
		if chainIDs[chainKey(remoteHead)][localHead.Identity.ID] {
			continue // sink head is an ancestor in the source's history — lagging, converges
		}
		divergent = append(divergent, divergentChain{
			topicKey:   remoteHead.Identity.TopicKey,
			scope:      remoteHead.Scope,
			localHead:  localHead,
			remoteHead: remoteHead,
		})
	}
	return divergent
}

func isImmutableConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "IMPORT_CONFLICT")
}

// nowISO is the sync engine's event timestamp: current UTC time in RFC3339
// (contracts/provenance.md rule 3 — every state traces to actor+time).
func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func chainKey(observation core.AccountingMemory) string {
	return observation.Identity.TopicKey + "\x00" + core.ScopeKey(observation.Scope)
}
