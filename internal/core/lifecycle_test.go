// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module contains no monetary fields and
// tests the observation lifecycle machine against the SQLite store.

package core_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

const testTimestamp = "2026-01-15T12:00:00.000Z"

var testScope = core.Scope{
	Kind:           core.ScopeKindCompany,
	OrganizationID: "org-001",
	CompanyID:      "acme",
	RUC:            "20100039201",
	Period:         "202401",
}

var testContent = core.Content{
	What:    "IGV base rate is 18 percent",
	Why:     "standard rate for goods",
	Where:   "Peru",
	Learned: "applies to all invoices",
}

var testProvenance = core.Provenance{
	Actor:     "test-agent",
	Timestamp: testTimestamp,
	Source:    "go-test",
}

func newTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engram.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func saveInput(what string) core.SaveInput {
	return core.SaveInput{
		TopicKey:   "tax.igv.rate",
		Title:      "IGV base rate",
		Type:       "policy",
		Scope:      testScope,
		Content:    core.Content{What: what, Why: "standard rate for goods", Where: "Peru", Learned: "applies to all invoices"},
		Provenance: testProvenance,
	}
}

func TestTransitionAuthorityLegalChain(t *testing.T) {
	legal := []struct{ from, to core.AuthorityStatus }{
		{core.StatusDraft, core.StatusReviewed},
		{core.StatusReviewed, core.StatusPromoted},
		{core.StatusPromoted, core.StatusSuperseded},
	}
	for _, tt := range legal {
		if err := core.TransitionAuthority(tt.from, tt.to); err != nil {
			t.Errorf("TransitionAuthority(%s → %s) should be legal: %v", tt.from, tt.to, err)
		}
		if !core.IsLegalTransition(tt.from, tt.to) {
			t.Errorf("IsLegalTransition(%s → %s) = false, want true", tt.from, tt.to)
		}
	}
}

func TestTransitionAuthorityIllegalTransitions(t *testing.T) {
	illegal := []struct {
		from, to core.AuthorityStatus
		name     string
	}{
		{core.StatusPromoted, core.StatusDraft, "promoted → draft"},
		{core.StatusDraft, core.StatusPromoted, "draft → promoted (non-adjacent)"},
		{core.StatusReviewed, core.StatusReviewed, "self-transition"},
		{core.StatusDraft, core.StatusSuperseded, "draft → superseded (jump)"},
		{core.StatusSuperseded, core.StatusDraft, "terminal to draft"},
	}
	for _, tt := range illegal {
		t.Run(tt.name, func(t *testing.T) {
			err := core.TransitionAuthority(tt.from, tt.to)
			if err == nil {
				t.Fatalf("TransitionAuthority(%s → %s) must fail", tt.from, tt.to)
			}
			if !strings.Contains(err.Error(), core.ErrInvalidTransition) {
				t.Fatalf("expected %s, got %v", core.ErrInvalidTransition, err)
			}
		})
	}
}

func TestApplyTransitionRecordsAuditTrail(t *testing.T) {
	s := newTestStore(t)
	saved, err := s.Save(saveInput("first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	reviewed, err := core.ApplyTransition(s, saved.Observation.Identity.ID, core.StatusReviewed, core.TransitionMeta{
		Actor:     "reviewer",
		Timestamp: testTimestamp,
	})
	if err != nil {
		t.Fatalf("applyTransition: %v", err)
	}
	if reviewed.AuthorityStatus != core.StatusReviewed {
		t.Fatalf("status = %s, want reviewed", reviewed.AuthorityStatus)
	}
	fromStore, ok := s.FindByID(saved.Observation.Identity.ID)
	if !ok || fromStore.AuthorityStatus != core.StatusReviewed {
		t.Fatalf("stored status = %v, want reviewed", fromStore.AuthorityStatus)
	}

	log, err := s.TransitionLog()
	if err != nil {
		t.Fatalf("transition log: %v", err)
	}
	found := false
	for _, entry := range log {
		if entry.ObservationID == saved.Observation.Identity.ID &&
			entry.From == core.StatusDraft &&
			entry.To == core.StatusReviewed &&
			entry.Actor == "reviewer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit-trail entry missing: %+v", log)
	}
}

func TestApplyTransitionIllegalLeavesObservationUnchanged(t *testing.T) {
	s := newTestStore(t)
	saved, err := s.Save(saveInput("first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := core.ApplyTransition(s, saved.Observation.Identity.ID, core.StatusPromoted, core.TransitionMeta{
		Actor:     "owner",
		Timestamp: testTimestamp,
	}); err == nil {
		t.Fatal("draft → promoted must be rejected")
	}
	fromStore, ok := s.FindByID(saved.Observation.Identity.ID)
	if !ok || fromStore.AuthorityStatus != core.StatusDraft {
		t.Fatalf("observation changed after illegal transition: %v", fromStore.AuthorityStatus)
	}
}

func promote(t *testing.T, s *store.SQLiteStore, id string) {
	t.Helper()
	for _, to := range []core.AuthorityStatus{core.StatusReviewed, core.StatusPromoted} {
		if _, err := core.ApplyTransition(s, id, to, core.TransitionMeta{Actor: "owner", Timestamp: testTimestamp}); err != nil {
			t.Fatalf("promote %s → %s: %v", id, to, err)
		}
	}
}

func TestSupersedeMarksAndRelates(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(saveInput("first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	second, err := s.Save(saveInput("second version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	promote(t, s, first.Observation.Identity.ID)
	promote(t, s, second.Observation.Identity.ID)

	updated, err := core.Supersede(core.SupersedeInput{
		Store:         s,
		ObservationID: first.Observation.Identity.ID,
		TargetID:      second.Observation.Identity.ID,
		Actor:         "owner",
		Timestamp:     testTimestamp,
	})
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if updated.AuthorityStatus != core.StatusSuperseded {
		t.Fatalf("status = %s, want superseded", updated.AuthorityStatus)
	}

	fromStore, ok := s.FindByID(first.Observation.Identity.ID)
	if !ok || fromStore.AuthorityStatus != core.StatusSuperseded {
		t.Fatalf("old observation not superseded: %v", fromStore.AuthorityStatus)
	}
	if successor, ok := s.SuccessorOf(first.Observation.Identity.ID); !ok || successor.Identity.ID != second.Observation.Identity.ID {
		t.Fatalf("successor routing broken: %+v", successor)
	}
	relations, err := s.Relations()
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	found := false
	for _, r := range relations {
		if r.FromID == first.Observation.Identity.ID && r.ToID == second.Observation.Identity.ID && r.Relation == core.RelationSupersedes {
			found = true
		}
	}
	if !found {
		t.Fatalf("supersedes relation missing: %+v", relations)
	}
}

func TestSupersedeRequiresPromotedSource(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(saveInput("first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	second, err := s.Save(saveInput("second version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err = core.Supersede(core.SupersedeInput{
		Store:         s,
		ObservationID: first.Observation.Identity.ID,
		TargetID:      second.Observation.Identity.ID,
		Actor:         "owner",
		Timestamp:     testTimestamp,
	})
	if err == nil || !strings.Contains(err.Error(), core.ErrInvalidTransition) {
		t.Fatalf("expected INVALID_TRANSITION for draft source, got %v", err)
	}
}

func TestSupersedeRequiresExistingTarget(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(saveInput("first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	promote(t, s, first.Observation.Identity.ID)

	_, err = core.Supersede(core.SupersedeInput{
		Store:         s,
		ObservationID: first.Observation.Identity.ID,
		TargetID:      "missing-target",
		Actor:         "owner",
		Timestamp:     testTimestamp,
	})
	if err == nil || !strings.Contains(err.Error(), "OBSERVATION_NOT_FOUND") {
		t.Fatalf("expected OBSERVATION_NOT_FOUND for missing target, got %v", err)
	}
}

func TestSupersedeRejectsSelf(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(saveInput("first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	promote(t, s, first.Observation.Identity.ID)

	_, err = core.Supersede(core.SupersedeInput{
		Store:         s,
		ObservationID: first.Observation.Identity.ID,
		TargetID:      first.Observation.Identity.ID,
		Actor:         "owner",
		Timestamp:     testTimestamp,
	})
	if err == nil || !strings.Contains(err.Error(), core.ErrInvalidSupersedeTarget) {
		t.Fatalf("expected INVALID_SUPERSEDE_TARGET for self-supersede, got %v", err)
	}
}
