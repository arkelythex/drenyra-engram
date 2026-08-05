// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the v2 lifecycle machine
// (approval gate, void, supersession) against the SQLite store, the same way the
// shared API composes it: core decides, the store persists.

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

var testSource = core.Source{
	System:    "go-test",
	ActorID:   "test-agent",
	ActorKind: core.ActorKindAgent,
}

var humanSource = core.Source{
	System:    "go-test",
	ActorID:   "maria.torres",
	ActorKind: core.ActorKindHuman,
}

var systemSource = core.Source{
	System:    "drenyra-core",
	ActorID:   "scheduler",
	ActorKind: core.ActorKindSystem,
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
		TopicKey:     "tax.igv.rate",
		Title:        "IGV base rate",
		Kind:         core.KindRule,
		Scope:        testScope,
		Content:      core.Content{What: what, Why: "standard rate for goods", Where: "Peru", Learned: "applies to all invoices"},
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  testTimestamp,
		Source:       testSource,
	}
}

// TestSaveGateStatus is the v2 save gate: fiscalEffect none → active (no gate),
// any non-none effect → pending_review (mandatory human approval).
func TestSaveGateStatus(t *testing.T) {
	s := newTestStore(t)

	informative, err := s.Save(saveInput("informative version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if informative.Memory.Status != core.StatusActive {
		t.Fatalf("status = %s, want active for fiscalEffect none", informative.Memory.Status)
	}

	gated := saveInput("gated version")
	gated.TopicKey = "tax.igv.adjustment"
	gated.Kind = core.KindDecision
	gated.FiscalEffect = core.FiscalEffectAdjustment
	gated.EffectiveAt = "2026-01-31"
	review, err := s.Save(gated)
	if err != nil {
		t.Fatalf("save gated: %v", err)
	}
	if review.Memory.Status != core.StatusPendingReview {
		t.Fatalf("status = %s, want pending_review for fiscalEffect adjustment", review.Memory.Status)
	}
	if !core.IsGated(gated.FiscalEffect) {
		t.Fatal("IsGated(adjustment) = false, want true")
	}
}

// TestApproveHumanFlow: a pending_review memory reaches approved through a HUMAN
// actor, and the audit trail records actor + actorKind.
func TestApproveHumanFlow(t *testing.T) {
	s := newTestStore(t)
	saved, err := s.Save(gatedInput())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID

	fromStore, ok := s.FindByID(id)
	if !ok {
		t.Fatal("memory not found")
	}
	if err := core.Approve(&fromStore, humanSource); err != nil {
		t.Fatalf("approve: %v", err)
	}
	approved, err := s.ApplyStatusTransition(id, core.StatusApproved, core.TransitionMeta{
		Actor:     humanSource.ActorID,
		ActorKind: humanSource.ActorKind,
		Timestamp: testTimestamp,
	})
	if err != nil {
		t.Fatalf("applyStatusTransition: %v", err)
	}
	if approved.Status != core.StatusApproved {
		t.Fatalf("status = %s, want approved", approved.Status)
	}
	fromStore, ok = s.FindByID(id)
	if !ok || fromStore.Status != core.StatusApproved {
		t.Fatalf("stored status = %v, want approved", fromStore.Status)
	}

	log, err := s.TransitionLog()
	if err != nil {
		t.Fatalf("transition log: %v", err)
	}
	found := false
	for _, entry := range log {
		if entry.MemoryID == id &&
			entry.From == core.StatusPendingReview &&
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

// TestApproveByAgentFailsClosed: the gate is non-negotiable — an agent CANNOT
// approve, and the stored memory stays pending_review.
func TestApproveByAgentFailsClosed(t *testing.T) {
	s := newTestStore(t)
	saved, err := s.Save(gatedInput())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID

	fromStore, _ := s.FindByID(id)
	approveErr := core.Approve(&fromStore, testSource)
	if approveErr == nil {
		t.Fatal("agent approval must fail closed")
	}
	if !strings.Contains(approveErr.Error(), core.ErrGateRequiresHuman.Error()) {
		t.Fatalf("expected GATE_REQUIRES_HUMAN, got %v", approveErr)
	}
	fromStore, _ = s.FindByID(id)
	if fromStore.Status != core.StatusPendingReview {
		t.Fatalf("memory changed after failed gate: %s", fromStore.Status)
	}
}

// TestRejectHumanFlow: a human can reject a pending_review memory (terminal).
func TestRejectHumanFlow(t *testing.T) {
	s := newTestStore(t)
	saved, err := s.Save(gatedInput())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID

	fromStore, _ := s.FindByID(id)
	if err := core.Reject(&fromStore, humanSource); err != nil {
		t.Fatalf("reject: %v", err)
	}
	rejected, err := s.ApplyStatusTransition(id, core.StatusRejected, core.TransitionMeta{
		Actor:     humanSource.ActorID,
		ActorKind: humanSource.ActorKind,
		Timestamp: testTimestamp,
	})
	if err != nil {
		t.Fatalf("applyStatusTransition: %v", err)
	}
	if rejected.Status != core.StatusRejected {
		t.Fatalf("status = %s, want rejected", rejected.Status)
	}

	// Terminal: rejecting again is an illegal transition and leaves the store
	// unchanged.
	fromStore, _ = s.FindByID(id)
	if err := core.Reject(&fromStore, humanSource); err == nil || !strings.Contains(err.Error(), core.ErrInvalidTransition.Error()) {
		t.Fatalf("double reject: expected INVALID_TRANSITION, got %v", err)
	}
	if _, err := s.ApplyStatusTransition(id, core.StatusRejected, core.TransitionMeta{Actor: "x", Timestamp: testTimestamp}); err != nil {
		t.Fatalf("persisting the illegal transition must still be a store-level error-free op (legality is the machine's job), got: %v", err)
	}
}

// TestVoidFlow: void admits human/system actors, never agents; result is
// terminal.
func TestVoidFlow(t *testing.T) {
	s := newTestStore(t)

	t.Run("agent cannot void", func(t *testing.T) {
		saved, err := s.Save(saveInput("to-void"))
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		id := saved.Memory.Identity.ID
		m, _ := s.FindByID(id)
		if err := core.Void(&m, testSource); err == nil || !strings.Contains(err.Error(), "GATE_AGENT_CANNOT_VOID") {
			t.Fatalf("expected GATE_AGENT_CANNOT_VOID, got %v", err)
		}
		if stored, _ := s.FindByID(id); stored.Status != core.StatusActive {
			t.Fatalf("memory mutated by failed void: %s", stored.Status)
		}
	})

	t.Run("system void succeeds", func(t *testing.T) {
		saved, err := s.Save(saveInput("to-void"))
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		id := saved.Memory.Identity.ID
		m, _ := s.FindByID(id)
		if err := core.Void(&m, systemSource); err != nil {
			t.Fatalf("system void: %v", err)
		}
		updated, err := s.ApplyStatusTransition(id, core.StatusVoided, core.TransitionMeta{
			Actor:     systemSource.ActorID,
			ActorKind: systemSource.ActorKind,
			Timestamp: testTimestamp,
		})
		if err != nil {
			t.Fatalf("applyStatusTransition: %v", err)
		}
		if updated.Status != core.StatusVoided {
			t.Fatalf("status = %s, want voided", updated.Status)
		}
	})
}

// TestSupersedePrevViaSave: a new save of the same (topicKey, scope) chain marks
// the previous current revision superseded with the successor id — history is
// immutable, readers route onward.
func TestSupersedePrevViaSave(t *testing.T) {
	s := newTestStore(t)
	first, err := s.Save(saveInput("first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	second, err := s.Save(saveInput("second version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	old, ok := s.FindByID(first.Memory.Identity.ID)
	if !ok {
		t.Fatal("revision 1 lost")
	}
	if old.Status != core.StatusSuperseded {
		t.Fatalf("status = %s, want superseded", old.Status)
	}
	if old.SupersedesID != second.Memory.Identity.ID {
		t.Fatalf("SupersedesID = %q, want %q", old.SupersedesID, second.Memory.Identity.ID)
	}
	if old.Content.What != "first version" || old.Revision != 1 {
		t.Fatalf("superseded revision mutated: %+v", old)
	}

	latest, ok := s.FindByTopicKey("tax.igv.rate", testScope)
	if !ok || latest.Revision != 2 {
		t.Fatalf("latest = %+v, want revision 2", latest)
	}
}

// TestIllegalTransitionLeavesStoredMemoryUnchanged: a transition the machine
// rejects never mutates the stored row.
func TestIllegalTransitionLeavesStoredMemoryUnchanged(t *testing.T) {
	s := newTestStore(t)
	saved, err := s.Save(saveInput("first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID

	m, _ := s.FindByID(id)
	if err := core.Approve(&m, humanSource); err == nil || !strings.Contains(err.Error(), core.ErrInvalidTransition.Error()) {
		t.Fatalf("approve from active: expected INVALID_TRANSITION, got %v", err)
	}
	stored, _ := s.FindByID(id)
	if stored.Status != core.StatusActive {
		t.Fatalf("stored status = %s, want unchanged active", stored.Status)
	}
}

func gatedInput() core.SaveInput {
	return core.SaveInput{
		TopicKey:     "tax.igv.adjustment",
		Title:        "Ajuste de IGV",
		Kind:         core.KindDecision,
		Scope:        testScope,
		Content:      testContent,
		FiscalEffect: core.FiscalEffectJournalEntry,
		EffectiveAt:  "2026-01-31",
		Source:       testSource,
	}
}
