// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the sync engine with
// structured-text observation fixtures; the model has no monetary fields, so
// no money value is merged or computed here.

package sync

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

const testOrgID = "org-acme"

func newTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engram.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func testScope(ruc string) core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: testOrgID,
		CompanyID:      ruc,
		RUC:            ruc,
		Period:         "202401",
	}
}

func validInput(topicKey, title, what string, scope core.Scope) core.SaveInput {
	return core.SaveInput{
		TopicKey: topicKey,
		Title:    title,
		Type:     "decision",
		Scope:    scope,
		Content: core.Content{
			What:    what,
			Why:     "sync test fixture",
			Where:   "internal/sync",
			Learned: "n/a",
		},
		Provenance: core.Provenance{
			Actor:     "test",
			Timestamp: "2026-01-15T12:00:00Z",
			Source:    "test",
		},
	}
}

// TestSyncAdditivePreservesHistory: a full revision history crosses stores
// with ids, revisions and provenance intact — sync is additive, never a
// re-save (which would fabricate new ids).
func TestSyncAdditivePreservesHistory(t *testing.T) {
	from := newTestStore(t)
	to := newTestStore(t)
	scope := testScope("20100039201")

	first, err := from.Save(validInput("topic/history", "History", "v1", scope))
	if err != nil {
		t.Fatalf("save v1: %v", err)
	}
	second, err := from.Save(validInput("topic/history", "History", "v2", scope))
	if err != nil {
		t.Fatalf("save v2: %v", err)
	}

	report, err := Sync(from, to, Options{Actor: "test", Timestamp: "2026-01-15T12:00:00Z"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if report.ObservationsImported != 2 {
		t.Fatalf("observationsImported = %d, want 2", report.ObservationsImported)
	}
	if len(report.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", report.Conflicts)
	}

	// Both revisions exist in the sink with ORIGINAL ids and revision numbers.
	got, ok := to.FindByID(first.Observation.Identity.ID)
	if !ok || got.Content.What != "v1" || got.Revision != 1 {
		t.Fatalf("sink lost revision 1: %+v ok=%v", got, ok)
	}
	got, ok = to.FindByID(second.Observation.Identity.ID)
	if !ok || got.Content.What != "v2" || got.Revision != 2 {
		t.Fatalf("sink lost revision 2: %+v ok=%v", got, ok)
	}
	if got.Provenance.Actor != "test" || got.Provenance.Source != "test" {
		t.Fatalf("provenance not preserved: %+v", got.Provenance)
	}
}

// TestSyncIdempotent: re-running the same pair is a no-op — nothing re-imported,
// nothing new conflicted.
func TestSyncIdempotent(t *testing.T) {
	from := newTestStore(t)
	to := newTestStore(t)
	scope := testScope("20100039201")

	if _, err := from.Save(validInput("topic/idem", "Idem", "x", scope)); err != nil {
		t.Fatalf("save: %v", err)
	}
	first, err := Sync(from, to, Options{Actor: "test"})
	if err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	second, err := Sync(from, to, Options{Actor: "test"})
	if err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if first.ObservationsImported != 1 {
		t.Fatalf("first import = %d, want 1", first.ObservationsImported)
	}
	if second.ObservationsImported != 0 {
		t.Fatalf("second import = %d, want 0 (idempotent)", second.ObservationsImported)
	}
	if second.ObservationsSkipped != 1 {
		t.Fatalf("second skipped = %d, want 1", second.ObservationsSkipped)
	}
	if len(second.Conflicts) != 0 {
		t.Fatalf("second run surfaced conflicts: %+v", second.Conflicts)
	}
}

// TestSyncPropagatesLifecycle: review/promote transitions cross the sync via
// AUDIT-TRAIL REPLAY — the sink's observation reaches the same status with the
// original actor/timestamp, never by editing the observation directly.
func TestSyncPropagatesLifecycle(t *testing.T) {
	from := newTestStore(t)
	to := newTestStore(t)
	scope := testScope("20100039201")

	saved, err := from.Save(validInput("topic/life", "Life", "x", scope))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Observation.Identity.ID

	// Sync while draft.
	if _, err := Sync(from, to, Options{Actor: "test"}); err != nil {
		t.Fatalf("sync draft: %v", err)
	}

	// Advance the source lifecycle.
	if _, err := core.ApplyTransition(from, id, core.StatusReviewed, core.TransitionMeta{Actor: "alice", Timestamp: "2026-01-15T12:00:00Z"}); err != nil {
		t.Fatalf("review: %v", err)
	}
	if _, err := core.ApplyTransition(from, id, core.StatusPromoted, core.TransitionMeta{Actor: "alice", Timestamp: "2026-01-15T13:00:00Z"}); err != nil {
		t.Fatalf("promote: %v", err)
	}

	report, err := Sync(from, to, Options{Actor: "test"})
	if err != nil {
		t.Fatalf("sync lifecycle: %v", err)
	}
	if report.TransitionsImported != 2 || report.TransitionsReplayed != 2 {
		t.Fatalf("transitions imported/replayed = %d/%d, want 2/2", report.TransitionsImported, report.TransitionsReplayed)
	}

	got, ok := to.FindByID(id)
	if !ok || got.AuthorityStatus != core.StatusPromoted {
		t.Fatalf("sink status = %q (ok=%v), want promoted", got.AuthorityStatus, ok)
	}
	log, err := to.TransitionLog()
	if err != nil {
		t.Fatalf("transition log: %v", err)
	}
	if len(log) != 2 {
		t.Fatalf("sink transition log has %d entries, want 2", len(log))
	}
	if log[0].Actor != "alice" || log[0].Timestamp != "2026-01-15T12:00:00Z" {
		t.Fatalf("replay must preserve provenance: %+v", log[0])
	}
}

// TestSyncDivergentChainConflict: the same (topicKey, scope) chain evolved
// independently in both stores — sync preserves BOTH histories and surfaces the
// divergence with a conflicts_with relation plus a report entry. Never silently
// resolved.
func TestSyncDivergentChainConflict(t *testing.T) {
	from := newTestStore(t)
	to := newTestStore(t)
	scope := testScope("20100039201")

	fromSaved, err := from.Save(validInput("topic/chain", "Chain", "from-side", scope))
	if err != nil {
		t.Fatalf("save from: %v", err)
	}
	toSaved, err := to.Save(validInput("topic/chain", "Chain", "to-side", scope))
	if err != nil {
		t.Fatalf("save to: %v", err)
	}

	report, err := Sync(from, to, Options{Actor: "sync-actor", Timestamp: "2026-01-15T12:00:00Z"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(report.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1: %+v", len(report.Conflicts), report.Conflicts)
	}
	conflict := report.Conflicts[0]
	if conflict.Kind != ConflictDivergentChain {
		t.Fatalf("conflict kind = %q, want divergent_chain", conflict.Kind)
	}
	if conflict.LocalID != toSaved.Observation.Identity.ID || conflict.RemoteID != fromSaved.Observation.Identity.ID {
		t.Fatalf("conflict heads wrong: local=%s remote=%s", conflict.LocalID, conflict.RemoteID)
	}

	// Both histories preserved: the sink has BOTH heads.
	if _, ok := to.FindByID(toSaved.Observation.Identity.ID); !ok {
		t.Fatal("sink lost its own head")
	}
	if _, ok := to.FindByID(fromSaved.Observation.Identity.ID); !ok {
		t.Fatal("sink lost the source head")
	}

	// The divergence is visible as a conflicts_with relation.
	relation, ok := to.RelationBetween(toSaved.Observation.Identity.ID, fromSaved.Observation.Identity.ID)
	if !ok || relation != string(core.RelationConflictsWith) {
		t.Fatalf("conflicts_with relation = %q ok=%v, want recorded", relation, ok)
	}
	if report.ConflictRelationsRecorded != 1 {
		t.Fatalf("conflictRelationsRecorded = %d, want 1", report.ConflictRelationsRecorded)
	}
}

// TestSyncImmutableConflictSurfaced: an id existing in the sink with different
// immutable bytes is NEVER overwritten — the source row is skipped and the
// divergence is surfaced.
func TestSyncImmutableConflictSurfaced(t *testing.T) {
	from := newTestStore(t)
	to := newTestStore(t)
	scope := testScope("20100039201")

	fromSaved, err := from.Save(validInput("topic/tamper", "Tamper", "source content", scope))
	if err != nil {
		t.Fatalf("save from: %v", err)
	}

	// Fabricate a conflicting row in the sink with the SAME id but different
	// immutable content (as if the sink diverged or was tampered).
	divergent := fromSaved.Observation
	divergent.Content.What = "different content"
	divergent.Content.Why = "different why"
	if _, err := to.ImportObservation(divergent); err != nil {
		t.Fatalf("seed sink: %v", err)
	}

	report, err := Sync(from, to, Options{Actor: "test"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if report.ObservationsImported != 0 || report.ObservationsSkipped != 0 {
		t.Fatalf("imported/skipped = %d/%d, want 0/0 (source row rejected)", report.ObservationsImported, report.ObservationsSkipped)
	}
	if len(report.Conflicts) != 1 || report.Conflicts[0].Kind != ConflictImmutable {
		t.Fatalf("want one immutable_conflict, got %+v", report.Conflicts)
	}

	// The sink's version is untouched.
	got, _ := to.FindByID(fromSaved.Observation.Identity.ID)
	if got.Content.What != "different content" {
		t.Fatalf("sink content overwritten: %q", got.Content.What)
	}
}

// TestSyncOneShotFullLifecycleNoPhantomConflicts (regression, verify finding A):
// a FIRST full sync into an EMPTY sink — the observation imports at its final
// status and the audit records cross verbatim. No phantom transition conflicts,
// no duplicated audit rows, the sink's log is complete.
func TestSyncOneShotFullLifecycleNoPhantomConflicts(t *testing.T) {
	from := newTestStore(t)
	to := newTestStore(t)
	scope := testScope("20100039201")

	saved, err := from.Save(validInput("topic/full", "Full", "x", scope))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Observation.Identity.ID
	// draft → reviewed → promoted (adjacent-forward).
	for _, toStatus := range []core.AuthorityStatus{core.StatusReviewed, core.StatusPromoted} {
		if _, err := core.ApplyTransition(from, id, toStatus, core.TransitionMeta{Actor: "alice", Timestamp: "2026-01-15T12:00:00Z"}); err != nil {
			t.Fatalf("transition to %s: %v", toStatus, err)
		}
	}
	// Supersede is the FINAL step (it applies promoted → superseded itself).
	target, err := from.Save(validInput("topic/full", "Full", "replacement", scope))
	if err != nil {
		t.Fatalf("save target: %v", err)
	}
	if _, err := core.Supersede(core.SupersedeInput{Store: from, ObservationID: id, TargetID: target.Observation.Identity.ID, Actor: "alice", Timestamp: "2026-01-15T12:00:01Z"}); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	report, err := Sync(from, to, Options{Actor: "test"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(report.Conflicts) != 0 {
		t.Fatalf("phantom conflicts: %+v", report.Conflicts)
	}
	got, ok := to.FindByID(id)
	if !ok || got.AuthorityStatus != core.StatusSuperseded {
		t.Fatalf("sink status = %q (ok=%v), want superseded", got.AuthorityStatus, ok)
	}
	log, err := to.TransitionLog()
	if err != nil {
		t.Fatalf("transition log: %v", err)
	}
	if len(log) != 3 {
		t.Fatalf("sink log has %d records, want 3 (verbatim, no duplication)", len(log))
	}
	// Records arrived with the source's provenance and order.
	if log[0].From != core.StatusDraft || log[0].To != core.StatusReviewed || log[0].Actor != "alice" {
		t.Fatalf("first record wrong: %+v", log[0])
	}
	if log[2].To != core.StatusSuperseded {
		t.Fatalf("last record wrong: %+v", log[2])
	}
}

// TestSyncFastForwardNoConflict (regression, verify finding B): the source
// advances a chain AFTER a prior sync — the sink is merely LAGGING (its head
// is an ancestor in the source's history), so this is NOT a divergence and no
// conflicts_with relation is recorded.
func TestSyncFastForwardNoConflict(t *testing.T) {
	from := newTestStore(t)
	to := newTestStore(t)
	scope := testScope("20100039201")

	first, err := from.Save(validInput("topic/ff", "FF", "v1", scope))
	if err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if _, err := Sync(from, to, Options{Actor: "test"}); err != nil {
		t.Fatalf("sync 1: %v", err)
	}

	// Source advances the chain (new revision, new id).
	second, err := from.Save(validInput("topic/ff", "FF", "v2", scope))
	if err != nil {
		t.Fatalf("save v2: %v", err)
	}

	report, err := Sync(from, to, Options{Actor: "test"})
	if err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if len(report.Conflicts) != 0 {
		t.Fatalf("fast-forward must not conflict: %+v", report.Conflicts)
	}
	// The sink converged to the new head.
	got, ok := to.FindByTopicKey("topic/ff", scope)
	if !ok || got.Identity.ID != second.Observation.Identity.ID {
		t.Fatalf("sink head = %+v ok=%v, want the source's new revision", got, ok)
	}
	// And no conflicts_with relation was recorded.
	if relation, ok := to.RelationBetween(first.Observation.Identity.ID, second.Observation.Identity.ID); ok {
		t.Fatalf("unexpected relation %q on a fast-forward", relation)
	}
}

// TestSyncDivergentLifecycleBothPreserved: the sink advanced an observation to
// promoted while the source only reviewed it — the audit histories cross
// verbatim (both preserved), the sink's status is never forced backward, and
// no phantom conflict is produced.
func TestSyncDivergentLifecycleBothPreserved(t *testing.T) {
	from := newTestStore(t)
	to := newTestStore(t)
	scope := testScope("20100039201")

	saved, err := from.Save(validInput("topic/div", "Div", "x", scope))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Observation.Identity.ID

	// Sink gets the draft and then advances itself to promoted.
	if _, err := Sync(from, to, Options{Actor: "test"}); err != nil {
		t.Fatalf("sync draft: %v", err)
	}
	if _, err := core.ApplyTransition(to, id, core.StatusReviewed, core.TransitionMeta{Actor: "bob", Timestamp: "2026-01-15T10:00:00Z"}); err != nil {
		t.Fatalf("sink review: %v", err)
	}
	if _, err := core.ApplyTransition(to, id, core.StatusPromoted, core.TransitionMeta{Actor: "bob", Timestamp: "2026-01-15T11:00:00Z"}); err != nil {
		t.Fatalf("sink promote: %v", err)
	}

	// Source only reviews (never promotes).
	if _, err := core.ApplyTransition(from, id, core.StatusReviewed, core.TransitionMeta{Actor: "alice", Timestamp: "2026-01-15T12:00:00Z"}); err != nil {
		t.Fatalf("source review: %v", err)
	}

	report, err := Sync(from, to, Options{Actor: "test"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(report.Conflicts) != 0 {
		t.Fatalf("divergent lifecycle must not phantom-conflict: %+v", report.Conflicts)
	}
	// The sink's status is never forced backward.
	got, _ := to.FindByID(id)
	if got.AuthorityStatus != core.StatusPromoted {
		t.Fatalf("sink status = %q, want unchanged promoted", got.AuthorityStatus)
	}
	// Both audit histories are preserved (sink's own + the imported source one).
	log, err := to.TransitionLog()
	if err != nil {
		t.Fatalf("transition log: %v", err)
	}
	if len(log) != 3 {
		t.Fatalf("sink log has %d records, want 3 (bob's two + alice's imported one)", len(log))
	}
}

// TestSyncRelationsPreserved: recorded relations cross the sync (idempotent).
func TestSyncRelationsPreserved(t *testing.T) {
	from := newTestStore(t)
	to := newTestStore(t)
	scope := testScope("20100039201")

	old, err := from.Save(validInput("topic/rel", "Rel", "old", scope))
	if err != nil {
		t.Fatalf("save old: %v", err)
	}
	target, err := from.Save(validInput("topic/rel", "Rel", "new", scope))
	if err != nil {
		t.Fatalf("save target: %v", err)
	}
	// draft → reviewed → promoted → supersede to record a supersedes relation.
	reviewed, err := core.ApplyTransition(from, old.Observation.Identity.ID, core.StatusReviewed, core.TransitionMeta{Actor: "test", Timestamp: "2026-01-15T12:00:00Z"})
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	_ = reviewed
	if _, err := core.ApplyTransition(from, old.Observation.Identity.ID, core.StatusPromoted, core.TransitionMeta{Actor: "test", Timestamp: "2026-01-15T12:00:01Z"}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if _, err := core.Supersede(core.SupersedeInput{
		Store:         from,
		ObservationID: old.Observation.Identity.ID,
		TargetID:      target.Observation.Identity.ID,
		Actor:         "test",
		Timestamp:     "2026-01-15T12:00:02Z",
	}); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	report, err := Sync(from, to, Options{Actor: "test"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if report.RelationsImported != 1 {
		t.Fatalf("relationsImported = %d, want 1", report.RelationsImported)
	}
	relation, ok := to.RelationBetween(old.Observation.Identity.ID, target.Observation.Identity.ID)
	if !ok || relation != string(core.RelationSupersedes) {
		t.Fatalf("supersedes relation = %q ok=%v, want synced", relation, ok)
	}
	if report.ObservationsImported != 2 {
		t.Fatalf("observationsImported = %d, want 2", report.ObservationsImported)
	}
}

// TestImportObservationRejectsMalformedRows: the import path re-validates with
// the same fail-closed validators as Save — a malformed row can never enter a
// store through a public API (this is why the sync engine can trust sources).
func TestImportObservationRejectsMalformedRows(t *testing.T) {
	st := newTestStore(t)
	bad := core.Observation{
		Identity: core.Identity{ID: "bad-id", TopicKey: "topic/bad"},
		Title:    "Bad",
		Scope:    testScope("20100039201"),
		Content:  core.Content{What: "x", Why: "y", Where: "z", Learned: "w"},
		Provenance: core.Provenance{
			Actor:     "test",
			Timestamp: "2026-01-15T12:00:00Z",
			Source:    "test",
		},
		Revision: 1,
	}
	if _, err := st.ImportObservation(bad); err != nil {
		t.Fatalf("baseline import: %v", err)
	}
	for name, mutate := range map[string]func(*core.Observation){
		"empty id":       func(o *core.Observation) { o.Identity.ID = "" },
		"revision zero":  func(o *core.Observation) { o.Revision = 0 },
		"bad scope kind": func(o *core.Observation) { o.Scope.Kind = "unknown" },
		"bad ruc":        func(o *core.Observation) { o.Scope.RUC = "123" },
		"empty content":  func(o *core.Observation) { o.Content.What = "" },
	} {
		row := bad
		mutate(&row)
		if _, err := st.ImportObservation(row); err == nil {
			t.Fatalf("import with %s must fail closed", name)
		}
	}
}

// failingSink embeds a real store and overrides ImportObservation to force a
// failure — the sync engine's error path.
type failingSink struct {
	*store.SQLiteStore
	err error
}

func (f *failingSink) ImportObservation(core.Observation) (bool, error) {
	return false, f.err
}

// TestSyncFailsClosedOnImportError: a generic import failure aborts the sync
// (fail closed) — a partial sync is never half-applied.
func TestSyncFailsClosedOnImportError(t *testing.T) {
	from := newTestStore(t)
	scope := testScope("20100039201")
	if _, err := from.Save(validInput("topic/fail", "Fail", "x", scope)); err != nil {
		t.Fatalf("save: %v", err)
	}
	sink := &failingSink{SQLiteStore: newTestStore(t), err: errors.New("persistence error: forced")}
	if _, err := Sync(from, sink, Options{Actor: "test"}); err == nil {
		t.Fatal("sync must fail closed on a generic import error")
	}
}

// TestSyncSurfacesImmutableConflict: an IMPORT_CONFLICT is surfaced as a
// conflict entry, never fatal — sync keeps going and reports the divergence.
func TestSyncSurfacesImmutableConflict(t *testing.T) {
	from := newTestStore(t)
	scope := testScope("20100039201")
	if _, err := from.Save(validInput("topic/c", "C", "x", scope)); err != nil {
		t.Fatalf("save: %v", err)
	}
	sink := &failingSink{SQLiteStore: newTestStore(t), err: errors.New("IMPORT_CONFLICT: forced")}
	report, err := Sync(from, sink, Options{Actor: "test"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(report.Conflicts) != 1 || report.Conflicts[0].Kind != ConflictImmutable {
		t.Fatalf("want one immutable_conflict, got %+v", report.Conflicts)
	}
}
