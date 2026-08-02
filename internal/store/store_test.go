// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module stores observations whose
// content is structured text; there are no monetary fields, so no money value
// is persisted or asserted here.

package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

const (
	testOrgID  = "org-001"
	testRucA   = "20100039201"
	testRucB   = "20600995804"
	testPeriod = "202401"
	testT      = "2026-01-15T12:00:00.000Z"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engram.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testScope(ruc string) core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: testOrgID,
		CompanyID:      "acme",
		RUC:            ruc,
		Period:         testPeriod,
	}
}

func validInput(topicKey, what string) core.SaveInput {
	return core.SaveInput{
		TopicKey: topicKey,
		Title:    "IGV base rate",
		Type:     "policy",
		Scope:    testScope(testRucA),
		Content: core.Content{
			What:    what,
			Why:     "standard rate for goods",
			Where:   "Peru",
			Learned: "applies to all invoices",
		},
		Provenance: core.Provenance{Actor: "test-agent", Timestamp: testT, Source: "go-test"},
	}
}

func TestSaveCreatesRevision1WithOutcomeCreated(t *testing.T) {
	s := newTestStore(t)
	result, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if result.Outcome != core.WriteCreated {
		t.Fatalf("outcome = %s, want created", result.Outcome)
	}
	if result.Observation.Revision != 1 {
		t.Fatalf("revision = %d, want 1", result.Observation.Revision)
	}
	if result.Observation.AuthorityStatus != core.StatusDraft {
		t.Fatalf("authorityStatus = %s, want draft", result.Observation.AuthorityStatus)
	}
	if result.Observation.Identity.TopicKey != "tax.igv.rate" {
		t.Fatalf("topicKey = %s, want tax.igv.rate", result.Observation.Identity.TopicKey)
	}
	if result.Observation.Identity.ID == "" {
		t.Fatal("id must not be empty")
	}
}

func TestSaveSecondSaveSameChainRevision2PreservesHistory(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := s.Save(validInput("tax.igv.rate", "second version"))
	if err != nil {
		t.Fatalf("second save: %v", err)
	}

	if second.Outcome != core.WriteUpdated {
		t.Fatalf("outcome = %s, want updated", second.Outcome)
	}
	if second.Observation.Revision != 2 {
		t.Fatalf("revision = %d, want 2", second.Observation.Revision)
	}
	if second.Observation.Identity.ID == first.Observation.Identity.ID {
		t.Fatal("a new revision must have a new id")
	}

	// History preserved: both revisions remain retrievable by id.
	rev1, ok := s.FindByID(first.Observation.Identity.ID)
	if !ok {
		t.Fatal("revision 1 no longer retrievable by id")
	}
	if rev1.Revision != 1 || rev1.Content.What != "first version" {
		t.Fatalf("revision 1 mutated: %+v", rev1)
	}
	rev2, ok := s.FindByID(second.Observation.Identity.ID)
	if !ok || rev2.Revision != 2 {
		t.Fatalf("revision 2 not retrievable: %+v", rev2)
	}
}

func TestFindByIDUnknownReturnsFalse(t *testing.T) {
	s := newTestStore(t)
	if _, ok := s.FindByID("does-not-exist"); ok {
		t.Fatal("unknown id must not resolve")
	}
}

func TestFindByTopicKeyReturnsLatestForExactScope(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(validInput("tax.igv.rate", "first version")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.Save(validInput("tax.igv.rate", "second version")); err != nil {
		t.Fatalf("save: %v", err)
	}

	latest, ok := s.FindByTopicKey("tax.igv.rate", testScope(testRucA))
	if !ok {
		t.Fatal("chain not found")
	}
	if latest.Revision != 2 || latest.Content.What != "second version" {
		t.Fatalf("latest = %+v, want revision 2", latest)
	}
}

func TestSaveScopeIsPartOfIdentity(t *testing.T) {
	s := newTestStore(t)
	a, err := s.Save(core.SaveInput{
		TopicKey: "tax.igv.rate", Title: "IGV base rate", Type: "policy",
		Scope: testScope(testRucA),
		Content: core.Content{What: "first version", Why: "standard rate for goods", Where: "Peru", Learned: "applies to all invoices"},
		Provenance: core.Provenance{Actor: "test-agent", Timestamp: testT, Source: "go-test"},
	})
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	b, err := s.Save(core.SaveInput{
		TopicKey: "tax.igv.rate", Title: "IGV base rate", Type: "policy",
		Scope: testScope(testRucB),
		Content: core.Content{What: "first version", Why: "standard rate for goods", Where: "Peru", Learned: "applies to all invoices"},
		Provenance: core.Provenance{Actor: "test-agent", Timestamp: testT, Source: "go-test"},
	})
	if err != nil {
		t.Fatalf("save B: %v", err)
	}

	// Same topicKey under another scope is a NEW chain: a fresh revision 1.
	if a.Outcome != core.WriteCreated || b.Outcome != core.WriteCreated {
		t.Fatalf("outcomes = %s/%s, want created/created", a.Outcome, b.Outcome)
	}
	if b.Observation.Revision != 1 {
		t.Fatalf("B revision = %d, want 1 (scope is part of identity)", b.Observation.Revision)
	}
	storedB, ok := s.FindByID(b.Observation.Identity.ID)
	if !ok || storedB.Scope.RUC != testRucB {
		t.Fatalf("B observation scoped wrong: %+v", storedB.Scope)
	}
}

func TestFindByScopeExactFilter(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(core.SaveInput{
		TopicKey: "topic.a", Title: "A", Type: "policy", Scope: testScope(testRucA),
		Content: core.Content{What: "version a", Why: "w", Where: "r", Learned: "l"},
		Provenance: core.Provenance{Actor: "test-agent", Timestamp: testT, Source: "go-test"},
	}); err != nil {
		t.Fatalf("save A: %v", err)
	}
	if _, err := s.Save(core.SaveInput{
		TopicKey: "topic.b", Title: "B", Type: "policy", Scope: testScope(testRucB),
		Content: core.Content{What: "version b", Why: "w", Where: "r", Learned: "l"},
		Provenance: core.Provenance{Actor: "test-agent", Timestamp: testT, Source: "go-test"},
	}); err != nil {
		t.Fatalf("save B: %v", err)
	}

	inA, err := s.FindByScope(testScope(testRucA))
	if err != nil {
		t.Fatalf("findByScope: %v", err)
	}
	if len(inA) != 1 || inA[0].Scope.RUC != testRucA {
		t.Fatalf("scope A = %+v, want exactly A's observation", inA)
	}
}

func TestListReturnsEveryRevision(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(validInput("tax.igv.rate", "first version")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.Save(validInput("tax.igv.rate", "second version")); err != nil {
		t.Fatalf("save: %v", err)
	}
	all, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("list length = %d, want 2 (full revision history)", len(all))
	}
}

func TestListByAuthorityAndApplyStatusTransition(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.Save(validInput("tax.igv.rate", "second version")); err != nil {
		t.Fatalf("save: %v", err)
	}

	drafts, err := s.ListByAuthority(core.StatusDraft)
	if err != nil {
		t.Fatalf("listByAuthority: %v", err)
	}
	if len(drafts) != 2 {
		t.Fatalf("draft count = %d, want 2", len(drafts))
	}

	if _, err := s.ApplyStatusTransition(first.Observation.Identity.ID, core.StatusReviewed, core.TransitionMeta{Actor: "reviewer", Timestamp: testT}); err != nil {
		t.Fatalf("applyStatusTransition: %v", err)
	}

	reviewed, err := s.ListByAuthority(core.StatusReviewed)
	if err != nil {
		t.Fatalf("listByAuthority: %v", err)
	}
	if len(reviewed) != 1 || reviewed[0].Identity.ID != first.Observation.Identity.ID {
		t.Fatalf("reviewed = %+v, want exactly the transitioned observation", reviewed)
	}
}

func TestSaveNeverEditsPromotedObservationInPlace(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.ApplyStatusTransition(first.Observation.Identity.ID, core.StatusReviewed, core.TransitionMeta{Actor: "reviewer", Timestamp: testT}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if _, err := s.ApplyStatusTransition(first.Observation.Identity.ID, core.StatusPromoted, core.TransitionMeta{Actor: "owner", Timestamp: testT}); err != nil {
		t.Fatalf("transition: %v", err)
	}

	// A new save of the chain is a new revision, never an in-place edit.
	second, err := s.Save(validInput("tax.igv.rate", "second version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if second.Observation.Revision != 2 {
		t.Fatalf("revision = %d, want 2", second.Observation.Revision)
	}

	promoted, ok := s.FindByID(first.Observation.Identity.ID)
	if !ok {
		t.Fatal("promoted observation lost")
	}
	if promoted.AuthorityStatus != core.StatusPromoted || promoted.Content.What != "first version" {
		t.Fatalf("promoted observation mutated in place: %+v", promoted)
	}
}

func TestSaveInvalidInputFailsFast(t *testing.T) {
	s := newTestStore(t)

	t.Run("empty topicKey", func(t *testing.T) {
		in := validInput("tax.igv.rate", "v")
		in.TopicKey = "  "
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "INVALID_TOPIC_KEY") {
			t.Fatalf("expected INVALID_TOPIC_KEY, got %v", err)
		}
	})

	t.Run("empty title", func(t *testing.T) {
		in := validInput("tax.igv.rate", "v")
		in.Title = ""
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "INVALID_TITLE") {
			t.Fatalf("expected INVALID_TITLE, got %v", err)
		}
	})

	t.Run("malformed ruc", func(t *testing.T) {
		in := validInput("tax.igv.rate", "v")
		in.Scope.RUC = "123"
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "INVALID_RUC") {
			t.Fatalf("expected INVALID_RUC, got %v", err)
		}
	})

	t.Run("empty content field", func(t *testing.T) {
		in := validInput("tax.igv.rate", "v")
		in.Content.What = ""
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "INVALID_CONTENT") {
			t.Fatalf("expected INVALID_CONTENT, got %v", err)
		}
	})

	t.Run("unparseable provenance timestamp", func(t *testing.T) {
		in := validInput("tax.igv.rate", "v")
		in.Provenance.Timestamp = "nope"
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "INVALID_PROVENANCE") {
			t.Fatalf("expected INVALID_PROVENANCE, got %v", err)
		}
	})
}

func TestSavePersistenceFailureNeverFabricatesSuccess(t *testing.T) {
	s := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	result, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err == nil && (result.Outcome == core.WriteCreated || result.Outcome == core.WriteUpdated) {
		t.Fatalf("fabricated success: outcome = %s on a closed store", result.Outcome)
	}
	if err == nil && result.Outcome != core.WriteUnknown {
		t.Fatalf("expected outcome unknown on persistence failure, got %s", result.Outcome)
	}
	if err == nil {
		// The unknown outcome reports the would-be observation; it must NOT be
		// stored — a re-read must not resolve it.
		if _, ok := s.FindByID(result.Observation.Identity.ID); ok {
			t.Fatal("observation was stored despite unknown outcome")
		}
	}
}

func TestSchemaImmutabilityGuards(t *testing.T) {
	s := newTestStore(t)
	result, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := result.Observation.Identity.ID

	// Direct SQL-level attacks must fail closed (the store API never exposes
	// these paths; this proves the schema guard is real).
	t.Run("content update aborts", func(t *testing.T) {
		_, err := s.db.Exec(`UPDATE observations SET title = 'hacked' WHERE id = ?`, id)
		if err == nil || !strings.Contains(err.Error(), "IMMUTABLE_OBSERVATION") {
			t.Fatalf("expected IMMUTABLE_OBSERVATION on content update, got %v", err)
		}
	})

	t.Run("delete aborts", func(t *testing.T) {
		_, err := s.db.Exec(`DELETE FROM observations WHERE id = ?`, id)
		if err == nil || !strings.Contains(err.Error(), "IMMUTABLE_OBSERVATION") {
			t.Fatalf("expected IMMUTABLE_OBSERVATION on delete, got %v", err)
		}
	})

	t.Run("status-only update is allowed", func(t *testing.T) {
		if _, err := s.db.Exec(`UPDATE observations SET authority_status = 'reviewed' WHERE id = ?`, id); err != nil {
			t.Fatalf("status-only update blocked: %v", err)
		}
	})

	t.Run("observation still intact", func(t *testing.T) {
		obs, ok := s.FindByID(id)
		if !ok || obs.Title != "IGV base rate" || obs.Content.What != "first version" {
			t.Fatalf("observation corrupted: %+v", obs)
		}
	})
}

func TestDoctorReportsHealth(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(validInput("tax.igv.rate", "first version")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.Save(validInput("tax.igv.rate", "second version")); err != nil {
		t.Fatalf("save: %v", err)
	}

	report, err := s.Doctor()
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.SchemaVersion != schemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", report.SchemaVersion, schemaVersion)
	}
	if report.Observations != 2 {
		t.Fatalf("observations = %d, want 2", report.Observations)
	}
	if report.RevisionChains != 1 {
		t.Fatalf("revisionChains = %d, want 1", report.RevisionChains)
	}
	if report.DBPath == "" {
		t.Fatal("dbPath must be reported")
	}
}

// Additional lifecycle-relation tests live below.

// makeTestInput builds a save input for a fresh chain on the test scope.
func makeTestInput(topicKey, what string) core.SaveInput {
	return core.SaveInput{
		TopicKey: topicKey,
		Title:    "base rate",
		Type:     "policy",
		Scope:    testScope(testRucA),
		Content: core.Content{
			What:    what,
			Why:     "standard for goods",
			Where:   "Peru",
			Learned: "applies to all",
		},
		Provenance: core.Provenance{Actor: "test-agent", Timestamp: testT, Source: "go-test"},
	}
}

func TestRelationBetweenReadsDirectionalRelation(t *testing.T) {
	s := newTestStore(t)
	a, err := s.Save(makeTestInput("memory.relations.probe.a", "first version"))
	if err != nil {
		t.Fatalf("save a: %v", err)
	}
	b, err := s.Save(makeTestInput("memory.relations.probe.b", "second version"))
	if err != nil {
		t.Fatalf("save b: %v", err)
	}

	if err := s.Relate(a.Observation.Identity.ID, b.Observation.Identity.ID, core.RelationSupersedes, nil); err != nil {
		t.Fatalf("relate: %v", err)
	}

	// The recorded edge reads back in the recorded direction only.
	relation, ok := s.RelationBetween(a.Observation.Identity.ID, b.Observation.Identity.ID)
	if !ok || relation != string(core.RelationSupersedes) {
		t.Fatalf("RelationBetween(a, b) = %q, %v; want %q, true", relation, ok, core.RelationSupersedes)
	}
	if _, ok := s.RelationBetween(b.Observation.Identity.ID, a.Observation.Identity.ID); ok {
		t.Fatal("RelationBetween(b, a) must not find the reverse edge")
	}
	if _, ok := s.RelationBetween(a.Observation.Identity.ID, "missing"); ok {
		t.Fatal("RelationBetween with a missing pair must report not found")
	}
}
