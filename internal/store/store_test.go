// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module stores v2 AccountingMemory
// objects whose content is structured text; there are no monetary fields, so no
// money value is persisted or asserted here.

package store

import (
	"context"
	"database/sql"
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

var testAgentSource = core.Source{
	System:    "go-test",
	ActorID:   "test-agent",
	ActorKind: core.ActorKindAgent,
}

func validInput(topicKey, what string) core.SaveInput {
	return core.SaveInput{
		TopicKey: topicKey,
		Title:    "IGV base rate",
		Kind:     core.KindRule,
		Scope:    testScope(testRucA),
		Content: core.Content{
			What:    what,
			Why:     "standard rate for goods",
			Where:   "Peru",
			Learned: "applies to all invoices",
		},
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  testT,
		Source:       testAgentSource,
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
	if result.Memory.Revision != 1 {
		t.Fatalf("revision = %d, want 1", result.Memory.Revision)
	}
	if result.Memory.Status != core.StatusActive {
		t.Fatalf("status = %s, want active (fiscalEffect none)", result.Memory.Status)
	}
	if result.Memory.Identity.TopicKey != "tax.igv.rate" {
		t.Fatalf("topicKey = %s, want tax.igv.rate", result.Memory.Identity.TopicKey)
	}
	if result.Memory.Identity.ID == "" {
		t.Fatal("id must not be empty")
	}
	if result.Memory.ContentHash == "" {
		t.Fatal("contentHash must be computed at save")
	}
}

func TestSaveSecondSaveSameChainRevision2SupersedesPrev(t *testing.T) {
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
	if second.Memory.Revision != 2 {
		t.Fatalf("revision = %d, want 2", second.Memory.Revision)
	}
	if second.Memory.Identity.ID == first.Memory.Identity.ID {
		t.Fatal("a new revision must have a new id")
	}

	// History preserved: revision 1 is superseded, never mutated.
	rev1, ok := s.FindByID(first.Memory.Identity.ID)
	if !ok {
		t.Fatal("revision 1 no longer retrievable by id")
	}
	if rev1.Status != core.StatusSuperseded {
		t.Fatalf("revision 1 status = %s, want superseded", rev1.Status)
	}
	if rev1.SupersedesID != second.Memory.Identity.ID {
		t.Fatalf("revision 1 SupersedesID = %q, want %q", rev1.SupersedesID, second.Memory.Identity.ID)
	}
	if rev1.Revision != 1 || rev1.Content.What != "first version" {
		t.Fatalf("revision 1 mutated: %+v", rev1)
	}
	rev2, ok := s.FindByID(second.Memory.Identity.ID)
	if !ok || rev2.Revision != 2 {
		t.Fatalf("revision 2 not retrievable: %+v", rev2)
	}
}

// TestSaveTerminalPrevStaysTerminal: when the previous current revision is in a
// terminal state (rejected/superseded/voided), a new save creates the new
// revision WITHOUT re-opening the terminal memory — history never re-opens.
func TestSaveTerminalPrevStaysTerminal(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := first.Memory.Identity.ID

	// Void the previous current revision (human/system void).
	m, _ := s.FindByID(id)
	if err := core.Void(&m, core.Source{System: "go-test", ActorID: "scheduler", ActorKind: core.ActorKindSystem}); err != nil {
		t.Fatalf("void: %v", err)
	}
	if _, err := s.ApplyStatusTransition(id, core.StatusVoided, core.TransitionMeta{Actor: "scheduler", Timestamp: testT}); err != nil {
		t.Fatalf("applyStatusTransition: %v", err)
	}

	second, err := s.Save(validInput("tax.igv.rate", "second version"))
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if second.Memory.Revision != 2 {
		t.Fatalf("revision = %d, want 2", second.Memory.Revision)
	}
	prev, _ := s.FindByID(id)
	if prev.Status != core.StatusVoided {
		t.Fatalf("terminal prev reopened: status = %s, want voided", prev.Status)
	}
	if prev.SupersedesID != "" {
		t.Fatalf("terminal prev must not carry a successor: %q", prev.SupersedesID)
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
	a, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	bInput := validInput("tax.igv.rate", "first version")
	bInput.Scope = testScope(testRucB)
	b, err := s.Save(bInput)
	if err != nil {
		t.Fatalf("save B: %v", err)
	}

	// Same topicKey under another scope is a NEW chain: a fresh revision 1.
	if a.Outcome != core.WriteCreated || b.Outcome != core.WriteCreated {
		t.Fatalf("outcomes = %s/%s, want created/created", a.Outcome, b.Outcome)
	}
	if b.Memory.Revision != 1 {
		t.Fatalf("B revision = %d, want 1 (scope is part of identity)", b.Memory.Revision)
	}
	storedB, ok := s.FindByID(b.Memory.Identity.ID)
	if !ok || storedB.Scope.RUC != testRucB {
		t.Fatalf("B memory scoped wrong: %+v", storedB.Scope)
	}
}

func TestFindByScopeExactFilter(t *testing.T) {
	s := newTestStore(t)
	aInput := validInput("topic.a", "version a")
	aInput.Scope = testScope(testRucA)
	if _, err := s.Save(aInput); err != nil {
		t.Fatalf("save A: %v", err)
	}
	bInput := validInput("topic.b", "version b")
	bInput.Scope = testScope(testRucB)
	if _, err := s.Save(bInput); err != nil {
		t.Fatalf("save B: %v", err)
	}

	inA, err := s.FindByScope(testScope(testRucA))
	if err != nil {
		t.Fatalf("findByScope: %v", err)
	}
	if len(inA) != 1 || inA[0].Scope.RUC != testRucA {
		t.Fatalf("scope A = %+v, want exactly A's memory", inA)
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

func TestListByStatusAndApplyStatusTransition(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.Save(validInput("tax.igv.rate", "second version")); err != nil {
		t.Fatalf("save: %v", err)
	}

	// v2 supersession: the second save superseded the first — only the LATEST
	// revision of the chain is active; the previous one is superseded (terminal).
	active, err := s.ListByStatus(core.StatusActive)
	if err != nil {
		t.Fatalf("listByStatus: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active count = %d, want 1 (only the latest revision is active)", len(active))
	}
	if active[0].Identity.ID == first.Memory.Identity.ID {
		t.Fatalf("active memory must be the SECOND revision (the first was superseded): got the first revision %s", active[0].Identity.ID)
	}
	if active[0].Revision != 2 {
		t.Fatalf("active revision = %d, want 2", active[0].Revision)
	}

	prev, ok := s.FindByID(first.Memory.Identity.ID)
	if !ok {
		t.Fatalf("first revision no longer findable (immutable history): %v", err)
	}
	if prev.Status != core.StatusSuperseded {
		t.Fatalf("first revision status = %s, want superseded", prev.Status)
	}

	// The store-level transition is the low-level status-only mutation: the API
	// applies the human gate and the lifecycle machine (core.Approve) BEFORE
	// calling it, and the audit trail records the actor. Here we exercise the
	// store surface directly: approve the current revision.
	current, err := s.ApplyStatusTransition(active[0].Identity.ID, core.StatusApproved, core.TransitionMeta{Actor: "maria.torres", ActorKind: core.ActorKindHuman, Timestamp: testT})
	if err != nil {
		t.Fatalf("applyStatusTransition: %v", err)
	}
	if current.Status != core.StatusApproved {
		t.Fatalf("status = %s, want approved", current.Status)
	}

	approved, err := s.ListByStatus(core.StatusApproved)
	if err != nil {
		t.Fatalf("listByStatus: %v", err)
	}
	if len(approved) != 1 || approved[0].Identity.ID != current.Identity.ID {
		t.Fatalf("approved = %+v, want exactly the transitioned memory", approved)
	}

	log, err := s.TransitionLog()
	if err != nil {
		t.Fatalf("transition log: %v", err)
	}
	found := false
	for _, entry := range log {
		if entry.MemoryID == current.Identity.ID &&
			entry.From == core.StatusActive &&
			entry.To == core.StatusApproved &&
			entry.Actor == "maria.torres" &&
			entry.ActorKind == core.ActorKindHuman {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit-trail entry missing: %+v", log)
	}
}

func TestSaveNeverEditsPromotedObservationInPlace(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// A new save of the chain is a new revision, never an in-place edit.
	second, err := s.Save(validInput("tax.igv.rate", "second version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if second.Memory.Revision != 2 {
		t.Fatalf("revision = %d, want 2", second.Memory.Revision)
	}

	firstStored, ok := s.FindByID(first.Memory.Identity.ID)
	if !ok {
		t.Fatal("first memory lost")
	}
	if firstStored.Content.What != "first version" || firstStored.Revision != 1 {
		t.Fatalf("first memory mutated in place: %+v", firstStored)
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

	t.Run("source without system", func(t *testing.T) {
		in := validInput("tax.igv.rate", "v")
		in.Source.System = ""
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "INVALID_SOURCE") {
			t.Fatalf("expected INVALID_SOURCE, got %v", err)
		}
	})

	t.Run("unknown kind", func(t *testing.T) {
		in := validInput("tax.igv.rate", "v")
		in.Kind = "policy"
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "INVALID_KIND") {
			t.Fatalf("expected INVALID_KIND, got %v", err)
		}
	})

	t.Run("unknown fiscal effect", func(t *testing.T) {
		in := validInput("tax.igv.rate", "v")
		in.FiscalEffect = "void"
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "INVALID_FISCAL_EFFECT") {
			t.Fatalf("expected INVALID_FISCAL_EFFECT, got %v", err)
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
}

func TestSchemaImmutabilityGuards(t *testing.T) {
	s := newTestStore(t)
	result, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := result.Memory.Identity.ID

	// Direct SQL-level attacks must fail closed (the store API never exposes
	// these paths; this proves the schema guard is real).
	t.Run("content update aborts", func(t *testing.T) {
		_, err := s.db.Exec(`UPDATE observations SET title = 'hacked' WHERE id = ?`, id)
		if err == nil || !strings.Contains(err.Error(), "IMMUTABLE_OBSERVATION") {
			t.Fatalf("expected IMMUTABLE_OBSERVATION on content update, got %v", err)
		}
	})

	t.Run("kind update aborts", func(t *testing.T) {
		_, err := s.db.Exec(`UPDATE observations SET kind = 'decision' WHERE id = ?`, id)
		if err == nil || !strings.Contains(err.Error(), "IMMUTABLE_OBSERVATION") {
			t.Fatalf("expected IMMUTABLE_OBSERVATION on kind update, got %v", err)
		}
	})

	t.Run("delete aborts", func(t *testing.T) {
		_, err := s.db.Exec(`DELETE FROM observations WHERE id = ?`, id)
		if err == nil || !strings.Contains(err.Error(), "IMMUTABLE_OBSERVATION") {
			t.Fatalf("expected IMMUTABLE_OBSERVATION on delete, got %v", err)
		}
	})

	t.Run("status-only update is allowed", func(t *testing.T) {
		if _, err := s.db.Exec(`UPDATE observations SET status = 'approved', authority_status = 'promoted' WHERE id = ?`, id); err != nil {
			t.Fatalf("status-only update blocked: %v", err)
		}
	})

	t.Run("supersedes_id update is allowed", func(t *testing.T) {
		if _, err := s.db.Exec(`UPDATE observations SET supersedes_id = 'successor' WHERE id = ?`, id); err != nil {
			t.Fatalf("supersedes_id update blocked: %v", err)
		}
	})

	t.Run("memory still intact", func(t *testing.T) {
		memory, ok := s.FindByID(id)
		if !ok || memory.Title != "IGV base rate" || memory.Content.What != "first version" {
			t.Fatalf("memory corrupted: %+v", memory)
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

	report, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
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

// Additional relation and link tests live below.

func TestRelationBetweenReadsDirectionalRelation(t *testing.T) {
	s := newTestStore(t)
	a, err := s.Save(validInput("memory.relations.probe.a", "first version"))
	if err != nil {
		t.Fatalf("save a: %v", err)
	}
	b, err := s.Save(validInput("memory.relations.probe.b", "second version"))
	if err != nil {
		t.Fatalf("save b: %v", err)
	}

	if err := s.Relate(a.Memory.Identity.ID, b.Memory.Identity.ID, core.RelationSupersedes, nil); err != nil {
		t.Fatalf("relate: %v", err)
	}

	// The recorded edge reads back in the recorded direction only.
	relation, ok := s.RelationBetween(a.Memory.Identity.ID, b.Memory.Identity.ID)
	if !ok || relation != string(core.RelationSupersedes) {
		t.Fatalf("RelationBetween(a, b) = %q, %v; want %q, true", relation, ok, core.RelationSupersedes)
	}
	if _, ok := s.RelationBetween(b.Memory.Identity.ID, a.Memory.Identity.ID); ok {
		t.Fatal("RelationBetween(b, a) must not find the reverse edge")
	}
	if _, ok := s.RelationBetween(a.Memory.Identity.ID, "missing"); ok {
		t.Fatal("RelationBetween with a missing pair must report not found")
	}

	t.Run("self relation rejected", func(t *testing.T) {
		if err := s.Relate(a.Memory.Identity.ID, a.Memory.Identity.ID, core.RelationRelated, nil); err == nil {
			t.Fatal("self-relation must fail closed")
		}
	})

	t.Run("unknown relation rejected", func(t *testing.T) {
		if err := s.Relate(a.Memory.Identity.ID, b.Memory.Identity.ID, "fuzzy", nil); err == nil {
			t.Fatal("unknown relation must fail closed")
		}
	})
}

// TestRelationsForScopeToIDScopeAssertion pins the to_id scope assertion
// (contracts/scope.md rules 3/4): RelationsForScope must return only edges
// whose FROM AND TO endpoints both belong to the exact query scope. A relation
// edge never discloses a foreign-scope endpoint id, so cross-scope edges are
// excluded in both directions even though the write path (Relate) permits them.
func TestRelationsForScopeToIDScopeAssertion(t *testing.T) {
	s := newTestStore(t)
	scopeA := testScope(testRucA)
	scopeB := testScope(testRucB)

	saveScoped := func(topicKey, what string, scope core.Scope) string {
		t.Helper()
		input := validInput(topicKey, what)
		input.Scope = scope
		res, err := s.Save(input)
		if err != nil {
			t.Fatalf("save %s: %v", topicKey, err)
		}
		return res.Memory.Identity.ID
	}

	a1 := saveScoped("relations.scope.a1", "tenant A memory 1", scopeA)
	a2 := saveScoped("relations.scope.a2", "tenant A memory 2", scopeA)
	b1 := saveScoped("relations.scope.b1", "tenant B memory", scopeB)

	// Same-scope edge (visible to A) + cross-scope edges in both directions
	// (must be invisible to both tenants).
	if err := s.Relate(a1, a2, core.RelationRelated, nil); err != nil {
		t.Fatalf("relate a1->a2: %v", err)
	}
	if err := s.Relate(a1, b1, core.RelationRelated, nil); err != nil {
		t.Fatalf("relate a1->b1: %v", err)
	}
	if err := s.Relate(b1, a1, core.RelationRelated, nil); err != nil {
		t.Fatalf("relate b1->a1: %v", err)
	}

	gotA, err := s.RelationsForScope(scopeA)
	if err != nil {
		t.Fatalf("RelationsForScope(A): %v", err)
	}
	if len(gotA) != 1 || gotA[0].FromID != a1 || gotA[0].ToID != a2 {
		t.Fatalf("RelationsForScope(A) = %+v; want exactly one edge a1->a2", gotA)
	}

	gotB, err := s.RelationsForScope(scopeB)
	if err != nil {
		t.Fatalf("RelationsForScope(B): %v", err)
	}
	if len(gotB) != 0 {
		t.Fatalf("RelationsForScope(B) = %+v; want no edges (b1->a1 to_id escapes scope B)", gotB)
	}
}

// ──────────────────────────────────────────────
// Evidence / rule links
// ──────────────────────────────────────────────

func TestEvidenceLinks(t *testing.T) {
	s := newTestStore(t)
	result, err := s.Save(validInput("tax.igv.documents", "documented"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := result.Memory.Identity.ID

	t.Run("add and list with dedup", func(t *testing.T) {
		if err := s.AddEvidenceLink(id, "xml:F001-948", "maria.torres"); err != nil {
			t.Fatalf("add link: %v", err)
		}
		if err := s.AddEvidenceLink(id, "xml:F001-948", "maria.torres"); err != nil {
			t.Fatalf("duplicate add must be a no-op, got: %v", err)
		}
		if err := s.AddEvidenceLink(id, "cdr:F001-948", "maria.torres"); err != nil {
			t.Fatalf("add link: %v", err)
		}
		refs, err := s.EvidenceRefs(id)
		if err != nil {
			t.Fatalf("evidenceRefs: %v", err)
		}
		if len(refs) != 2 {
			t.Fatalf("refs = %v, want exactly 2 (deduped)", refs)
		}
		if refs[0] != "xml:F001-948" || refs[1] != "cdr:F001-948" {
			t.Fatalf("refs order = %v, want [xml:F001-948 cdr:F001-948]", refs)
		}
	})

	t.Run("links do not mutate the stored memory", func(t *testing.T) {
		var storedRefs string
		if err := s.db.QueryRow(`SELECT evidence_refs_json FROM observations WHERE id = ?`, id).Scan(&storedRefs); err != nil {
			t.Fatalf("read stored refs: %v", err)
		}
		if storedRefs != "[]" {
			t.Fatalf("stored evidence_refs_json mutated by linking: %q", storedRefs)
		}
		// The read view merges the link rows.
		memory, ok := s.FindByID(id)
		if !ok {
			t.Fatal("memory not found")
		}
		if len(memory.EvidenceRefs) != 2 {
			t.Fatalf("read view missing links: %v", memory.EvidenceRefs)
		}
	})

	t.Run("missing memory fails closed", func(t *testing.T) {
		if err := s.AddEvidenceLink("missing", "ref", "x"); err == nil || !strings.Contains(err.Error(), "OBSERVATION_NOT_FOUND") {
			t.Fatalf("expected OBSERVATION_NOT_FOUND, got %v", err)
		}
		if _, err := s.EvidenceRefs("missing"); err == nil || !strings.Contains(err.Error(), "OBSERVATION_NOT_FOUND") {
			t.Fatalf("expected OBSERVATION_NOT_FOUND, got %v", err)
		}
	})

	t.Run("empty ref fails closed", func(t *testing.T) {
		if err := s.AddEvidenceLink(id, "  ", "x"); err == nil || !strings.Contains(err.Error(), "INVALID_REF") {
			t.Fatalf("expected INVALID_REF, got %v", err)
		}
	})

	t.Run("rule links mirror evidence links", func(t *testing.T) {
		if err := s.AddRuleLink(id, "policy/igv/late-document-v3", "maria.torres"); err != nil {
			t.Fatalf("add rule link: %v", err)
		}
		if err := s.AddRuleLink(id, "policy/igv/late-document-v3", "maria.torres"); err != nil {
			t.Fatalf("duplicate rule add must be a no-op, got: %v", err)
		}
		refs, err := s.RuleRefs(id)
		if err != nil {
			t.Fatalf("ruleRefs: %v", err)
		}
		if len(refs) != 1 || refs[0] != "policy/igv/late-document-v3" {
			t.Fatalf("ruleRefs = %v, want [policy/igv/late-document-v3]", refs)
		}
	})
}

// ──────────────────────────────────────────────
// v1 → v2 additive migration
// ──────────────────────────────────────────────

// v1SchemaDDL is the EXACT schema_version=1 layout (as shipped before the v2
// propagation) used to build a legacy store the migration must upgrade.
const v1SchemaDDL = `
CREATE TABLE IF NOT EXISTS schema_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT OR IGNORE INTO schema_meta (key, value) VALUES ('schema_version', '1');

CREATE TABLE IF NOT EXISTS observations (
    id               TEXT PRIMARY KEY,
    topic_key        TEXT NOT NULL,
    title            TEXT NOT NULL,
    type             TEXT NOT NULL,
    scope_kind       TEXT NOT NULL,
    organization_id  TEXT NOT NULL DEFAULT '',
    company_id       TEXT NOT NULL DEFAULT '',
    ruc              TEXT NOT NULL DEFAULT '',
    period           TEXT NOT NULL DEFAULT '',
    what             TEXT NOT NULL,
    why              TEXT NOT NULL,
    where_text       TEXT NOT NULL,
    learned          TEXT NOT NULL,
    authority_status TEXT NOT NULL,
    effective_at     TEXT NOT NULL DEFAULT '',
    expires_at       TEXT NOT NULL DEFAULT '',
    actor            TEXT NOT NULL,
    timestamp        TEXT NOT NULL,
    source           TEXT NOT NULL,
    session          TEXT NOT NULL DEFAULT '',
    revision         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_observations_chain
    ON observations (topic_key, scope_kind, organization_id, company_id, ruc, period, revision DESC);
CREATE INDEX IF NOT EXISTS idx_observations_scope
    ON observations (scope_kind, organization_id, company_id, ruc, period);

CREATE TABLE IF NOT EXISTS relations (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id   TEXT NOT NULL REFERENCES observations(id),
    to_id     TEXT NOT NULL REFERENCES observations(id),
    relation  TEXT NOT NULL,
    actor     TEXT NOT NULL DEFAULT '',
    timestamp TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_relations_from ON relations (from_id);

CREATE TABLE IF NOT EXISTS transition_log (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    observation_id TEXT NOT NULL REFERENCES observations(id),
    from_status    TEXT NOT NULL,
    to_status      TEXT NOT NULL,
    actor          TEXT NOT NULL,
    timestamp      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_transition_log_obs ON transition_log (observation_id);

CREATE TRIGGER IF NOT EXISTS observations_immutable_content
BEFORE UPDATE OF id, topic_key, title, type, scope_kind, organization_id, company_id, ruc, period,
                 what, why, where_text, learned, effective_at, expires_at, actor, timestamp, source,
                 session, revision ON observations
BEGIN
    SELECT RAISE(ABORT, 'IMMUTABLE_OBSERVATION: content, scope and provenance never change after write');
END;

CREATE TRIGGER IF NOT EXISTS observations_no_delete
BEFORE DELETE ON observations
BEGIN
    SELECT RAISE(ABORT, 'IMMUTABLE_OBSERVATION: history is never deleted');
END;
`

// TestV1ToV2MigrationIsAdditive builds a genuine schema_version=1 store with
// example rows, opens it with the v2 store, and verifies the additive
// migration: schema_version=2, kind/status/fiscal_effect/recorded_at/
// effective_at/source_json/content_hash backfilled per the core mappings, and
// the v1 columns (type, authority_status) preserved untouched.
func TestV1ToV2MigrationIsAdditive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacy.Exec(v1SchemaDDL); err != nil {
		t.Fatalf("apply v1 schema: %v", err)
	}
	insert := `
		INSERT INTO observations (
			id, topic_key, title, type, scope_kind, organization_id, company_id, ruc, period,
			what, why, where_text, learned, authority_status, effective_at, expires_at,
			actor, timestamp, source, session, revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	rows := []struct {
		id, topicKey, title, typ, status, effAt, actor, timestamp, source, session string
		revision                                                                   int
	}{
		{
			id: "mig-1", topicKey: "tax.igv.adjustment", title: "Ajuste IGV", typ: "decision",
			status: "promoted", effAt: "2026-01-01T00:00:00Z", actor: "maria.torres",
			timestamp: "2026-01-02T10:00:00Z", source: "cli", session: "s-1", revision: 1,
		},
		{
			id: "mig-2", topicKey: "tax.igv.policy", title: "Política IGV", typ: "policy",
			status: "draft", effAt: "", actor: "agent-1",
			timestamp: "2026-02-01T08:00:00Z", source: "mcp", session: "", revision: 1,
		},
		{
			id: "mig-3", topicKey: "arch.design", title: "Diseño motor", typ: "architecture",
			status: "superseded", effAt: "2026-03-01T00:00:00Z", actor: "owner",
			timestamp: "2026-03-02T09:30:00Z", source: "http", session: "s-3", revision: 1,
		},
	}
	scope := testScope(testRucA)
	for _, r := range rows {
		if _, err := legacy.Exec(insert,
			r.id, r.topicKey, r.title, r.typ, string(scope.Kind), scope.OrganizationID, scope.CompanyID, scope.RUC, scope.Period,
			"what "+r.id, "why", "where", "learned", r.status, r.effAt, "",
			r.actor, r.timestamp, r.source, r.session, r.revision,
		); err != nil {
			_ = legacy.Close()
			t.Fatalf("insert legacy row %s: %v", r.id, err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// (c) schema_version must be the current layout only after the whole
	// migration chain (v1→v2→v3) completed.
	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("schema_version = %d, want %d", version, schemaVersion)
	}

	// Backfill per the core mappings.
	t.Run("backfilled v2 fields", func(t *testing.T) {
		checks := []struct {
			id              string
			wantKind        core.MemoryKind
			wantStatus      core.MemoryStatus
			wantEffectiveAt string
		}{
			{"mig-1", core.KindDecision, core.StatusApproved, "2026-01-01T00:00:00Z"},  // promoted → approved, validity.effectiveAt kept
			{"mig-2", core.KindRule, core.StatusActive, "2026-02-01T08:00:00Z"},        // policy → rule, draft → active, effectiveAt ← timestamp
			{"mig-3", core.KindSummary, core.StatusSuperseded, "2026-03-01T00:00:00Z"}, // architecture → summary
		}
		for _, c := range checks {
			memory, ok := s.FindByID(c.id)
			if !ok {
				t.Fatalf("migrated memory %s not found", c.id)
			}
			if memory.Kind != c.wantKind {
				t.Fatalf("%s kind = %s, want %s", c.id, memory.Kind, c.wantKind)
			}
			if memory.Status != c.wantStatus {
				t.Fatalf("%s status = %s, want %s", c.id, memory.Status, c.wantStatus)
			}
			if memory.FiscalEffect != core.FiscalEffectNone {
				t.Fatalf("%s fiscal_effect = %s, want none", c.id, memory.FiscalEffect)
			}
			if memory.EffectiveAt != c.wantEffectiveAt {
				t.Fatalf("%s effectiveAt = %q, want %q", c.id, memory.EffectiveAt, c.wantEffectiveAt)
			}
			if memory.RecordedAt == "" {
				t.Fatalf("%s recordedAt must be backfilled from provenance.timestamp", c.id)
			}
			if memory.ContentHash == "" {
				t.Fatalf("%s content_hash must be computed", c.id)
			}
			if memory.Source.System == "" || memory.Source.ActorID == "" {
				t.Fatalf("%s source_json not backfilled: %+v", c.id, memory.Source)
			}
			if memory.Source.ActorKind != core.ActorKindHuman {
				t.Fatalf("%s actorKind = %s, want human (v1 provenance never carried one)", c.id, memory.Source.ActorKind)
			}
		}
	})

	t.Run("content hash matches the canonical reconstruction", func(t *testing.T) {
		// Recompute the canonical hash for mig-1 and compare with the stored one.
		var rawHash string
		if err := s.db.QueryRow(`SELECT content_hash FROM observations WHERE id = 'mig-1'`).Scan(&rawHash); err != nil {
			t.Fatalf("read content_hash: %v", err)
		}
		canonical := core.ComputeContentHash(core.AccountingMemory{
			Scope: scope,
			Kind:  core.KindDecision,
			Title: "Ajuste IGV",
			Content: core.Content{
				What: "what mig-1", Why: "why", Where: "where", Learned: "learned",
			},
			FiscalEffect: core.FiscalEffectNone,
			EffectiveAt:  "2026-01-01T00:00:00Z",
			Source:       core.Source{System: "cli", ActorID: "maria.torres", ActorKind: core.ActorKindHuman, Session: "s-1"},
		})
		if rawHash != canonical {
			t.Fatalf("content_hash = %q, want canonical %q", rawHash, canonical)
		}
	})

	t.Run("v1 columns preserved", func(t *testing.T) {
		var typ, authorityStatus string
		if err := s.db.QueryRow(`SELECT type, authority_status FROM observations WHERE id = 'mig-2'`).Scan(&typ, &authorityStatus); err != nil {
			t.Fatalf("read v1 columns: %v", err)
		}
		if typ != "policy" || authorityStatus != "draft" {
			t.Fatalf("v1 columns mutated by migration: type=%q authority_status=%q", typ, authorityStatus)
		}
	})

	t.Run("migrated store accepts v2 writes and v2 guard is active", func(t *testing.T) {
		result, err := s.Save(validInput("tax.igv.new", "post-migration"))
		if err != nil {
			t.Fatalf("v2 save on migrated store: %v", err)
		}
		if result.Memory.Revision != 1 {
			t.Fatalf("revision = %d, want 1", result.Memory.Revision)
		}
		// The v2 immutability guard protects the migrated v2 columns.
		_, err = s.db.Exec(`UPDATE observations SET kind = 'fact' WHERE id = ?`, result.Memory.Identity.ID)
		if err == nil || !strings.Contains(err.Error(), "IMMUTABLE_OBSERVATION") {
			t.Fatalf("v2 guard must protect kind after migration, got %v", err)
		}
	})
}

// ──────────────────────────────────────────────
// Immutable history
// ──────────────────────────────────────────────

// TestImmutableHistory: re-saving the same (topicKey, scope) creates a new
// revision and never mutates the previous one — the stored row bytes of the
// previous revision stay identical (except the lifecycle-owned status +
// supersedes_id).
func TestImmutableHistory(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	firstID := first.Memory.Identity.ID

	// Snapshot the row before the second save.
	var (
		titleBefore, whatBefore, kindBefore, hashBefore string
		revisionBefore                                  int
	)
	err = s.db.QueryRow(`SELECT title, what, kind, content_hash, revision FROM observations WHERE id = ?`, firstID).
		Scan(&titleBefore, &whatBefore, &kindBefore, &hashBefore, &revisionBefore)
	if err != nil {
		t.Fatalf("snapshot row: %v", err)
	}

	second, err := s.Save(validInput("tax.igv.rate", "second version"))
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if second.Memory.Revision != 2 {
		t.Fatalf("revision = %d, want 2", second.Memory.Revision)
	}

	var (
		titleAfter, whatAfter, kindAfter, hashAfter string
		revisionAfter                               int
		statusAfter                                 string
	)
	// supersedes_id and status changed (lifecycle-owned); everything else must
	// be byte-identical.
	err = s.db.QueryRow(`SELECT title, what, kind, content_hash, revision, status FROM observations WHERE id = ?`, firstID).
		Scan(&titleAfter, &whatAfter, &kindAfter, &hashAfter, &revisionAfter, &statusAfter)
	if err != nil {
		t.Fatalf("re-read row: %v", err)
	}
	if titleAfter != titleBefore || whatAfter != whatBefore || kindAfter != kindBefore || hashAfter != hashBefore {
		t.Fatalf("immutable columns mutated: title %q→%q what %q→%q kind %q→%q hash %q→%q",
			titleBefore, titleAfter, whatBefore, whatAfter, kindBefore, kindAfter, hashBefore, hashAfter)
	}
	if revisionAfter != revisionBefore {
		t.Fatalf("revision mutated: %d → %d", revisionBefore, revisionAfter)
	}
	if statusAfter != string(core.StatusSuperseded) {
		t.Fatalf("status = %q, want superseded (the only allowed change)", statusAfter)
	}
	if second.Memory.Identity.ID == firstID {
		t.Fatal("new revision must have a new id")
	}
}
