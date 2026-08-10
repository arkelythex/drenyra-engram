// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the G-10 store read
// (design D-3): LatestMaterialDecisionHeads returns ONLY the latest revision per
// (topic_key, exact scope) chain inside the queried company scope + byte-equal
// period, applying the FZ-1 status/fiscal-effect/materiality predicates in SQL —
// structural isolation, never a post-filter. Scope/period are validated and fail
// closed on malformed or partial input.
package store

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// saveDecision seeds one decision memory in the given exact scope with the given
// declared materiality level and target status (Save derives pending_review for
// non-none fiscal effects; the explicit status transition mirrors the human
// approval gate). Saving the same (topicKey, scope) again supersedes the prior
// revision, exactly like production upserts.
func saveDecision(t *testing.T, s *SQLiteStore, topicKey string, scope core.Scope, level core.MaterialityLevel, status core.MemoryStatus) core.AccountingMemory {
	t.Helper()
	result, err := s.Save(core.SaveInput{
		TopicKey:         topicKey,
		Title:            "material decision",
		Kind:             core.KindDecision,
		Scope:            scope,
		Content:          core.Content{What: "w", Why: "y", Where: "f", Learned: "x"},
		FiscalEffect:     core.FiscalEffectJournalEntry,
		EffectiveAt:      testT,
		Source:           testAgentSource,
		MaterialityLevel: &level,
	})
	if err != nil {
		t.Fatalf("save %s: %v", topicKey, err)
	}
	memory, ok := s.FindByID(result.Memory.Identity.ID)
	if !ok {
		t.Fatalf("find saved %s", topicKey)
	}
	if status != "" && memory.Status != status {
		memory, err = s.ApplyStatusTransition(memory.Identity.ID, status, core.TransitionMeta{
			Actor:     "maria.torres",
			ActorKind: core.ActorKindHuman,
			Timestamp: testT,
		})
		if err != nil {
			t.Fatalf("transition %s → %s: %v", topicKey, status, err)
		}
	}
	return memory
}

// saveDecisionRaw seeds a decision with full caller control of the fiscal effect
// and materiality level (nil = normal), for the FZ-1 predicate-in-SQL cases.
func saveDecisionRaw(t *testing.T, s *SQLiteStore, topicKey string, scope core.Scope, effect core.FiscalEffect, level *core.MaterialityLevel, status core.MemoryStatus) core.AccountingMemory {
	t.Helper()
	result, err := s.Save(core.SaveInput{
		TopicKey:         topicKey,
		Title:            "material decision",
		Kind:             core.KindDecision,
		Scope:            scope,
		Content:          core.Content{What: "w", Why: "y", Where: "f", Learned: "x"},
		FiscalEffect:     effect,
		EffectiveAt:      testT,
		Source:           testAgentSource,
		MaterialityLevel: level,
	})
	if err != nil {
		t.Fatalf("save %s: %v", topicKey, err)
	}
	memory, ok := s.FindByID(result.Memory.Identity.ID)
	if !ok {
		t.Fatalf("find saved %s", topicKey)
	}
	if status != "" && memory.Status != status {
		memory, err = s.ApplyStatusTransition(memory.Identity.ID, status, core.TransitionMeta{
			Actor:     "maria.torres",
			ActorKind: core.ActorKindHuman,
			Timestamp: testT,
		})
		if err != nil {
			t.Fatalf("transition %s → %s: %v", topicKey, status, err)
		}
	}
	return memory
}

// TestLatestMaterialDecisionHeadsExactScopeIsolation seeds two companies sharing
// topic keys plus an adjacent period and asserts only the exact queried
// scope+period heads are returned — company B rows are never loaded by an A
// query (structural isolation, IR-2).
func TestLatestMaterialDecisionHeadsExactScopeIsolation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	scopeA := testScope(testRucA)  // org-001 / acme / 20100039201 / 202401
	scopeB := testScope(testRucB)  // org-001 / acme / 20600995804 / 202401 — same topic keys
	scopeA2 := testScope(testRucA) // org-001 / acme / 20100039201 / 202401
	scopeA2.Period = "202402"      // adjacent period

	saveDecision(t, s, "decision/x", scopeA, core.MaterialityMaterial, core.StatusApproved)
	saveDecision(t, s, "decision/y", scopeA, core.MaterialityCritical, core.StatusApproved)
	// Same topic keys in the OTHER company and the ADJACENT period — must never
	// surface from an A/202401 query.
	saveDecision(t, s, "decision/x", scopeB, core.MaterialityMaterial, core.StatusApproved)
	saveDecision(t, s, "decision/y", scopeB, core.MaterialityMaterial, core.StatusApproved)
	saveDecision(t, s, "decision/x", scopeA2, core.MaterialityMaterial, core.StatusApproved)
	saveDecision(t, s, "decision/y", scopeA2, core.MaterialityMaterial, core.StatusApproved)

	heads, err := s.LatestMaterialDecisionHeads(ctx, scopeA)
	if err != nil {
		t.Fatalf("LatestMaterialDecisionHeads(A/202401): %v", err)
	}
	if len(heads) != 2 {
		t.Fatalf("A/202401 heads = %d, want 2 (cross-tenant/period rows leaked)", len(heads))
	}
	gotIDs := make([]string, len(heads))
	for i, h := range heads {
		gotIDs[i] = h.Identity.ID
		if !core.ScopeEquals(h.Scope, scopeA) {
			t.Errorf("head %s has scope %+v outside the queried exact scope", h.Identity.ID, h.Scope)
		}
	}
	sort.Strings(gotIDs)

	// The store returns heads deterministically ordered by decision ID (bytewise
	// ascending), independent of insertion order.
	if !sort.StringsAreSorted(gotIDs) {
		t.Fatalf("heads not ordered by decision ID: %v", gotIDs)
	}

	// Company B query returns exactly its own head for decision/x — never A's.
	bHeads, err := s.LatestMaterialDecisionHeads(ctx, scopeB)
	if err != nil {
		t.Fatalf("LatestMaterialDecisionHeads(B/202401): %v", err)
	}
	if len(bHeads) != 2 {
		t.Fatalf("B/202401 heads = %d, want 2", len(bHeads))
	}
	for _, h := range bHeads {
		if h.Scope.RUC != testRucB {
			t.Errorf("B query returned a %s-scoped head: %+v", h.Scope.RUC, h)
		}
	}

	// Adjacent period query returns exactly its own heads.
	a2Heads, err := s.LatestMaterialDecisionHeads(ctx, scopeA2)
	if err != nil {
		t.Fatalf("LatestMaterialDecisionHeads(A/202402): %v", err)
	}
	if len(a2Heads) != 2 {
		t.Fatalf("A/202402 heads = %d, want 2", len(a2Heads))
	}
	for _, h := range a2Heads {
		if h.Scope.Period != "202402" {
			t.Errorf("A/202402 query returned a %s-period head", h.Scope.Period)
		}
	}
}

// TestLatestMaterialDecisionHeadsLatestRevisionOnly pins FZ-1.1 in SQL: only the
// MAXIMUM revision per (topic_key, exact scope) chain can ever be a head, and
// that latest revision must itself satisfy status/fiscal-effect/materiality —
// an eligible OLD revision never substitutes for an ineligible latest one.
func TestLatestMaterialDecisionHeadsLatestRevisionOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scope := testScope(testRucA)

	// Chain "decision/latest-ok": rev1 eligible, rev2 eligible → rev2 is the head.
	saveDecision(t, s, "decision/latest-ok", scope, core.MaterialityMaterial, core.StatusApproved)
	saveDecision(t, s, "decision/latest-ok", scope, core.MaterialityMaterial, core.StatusApproved)

	// Chain "decision/latest-rejected": rev1 eligible, rev2 REJECTED → excluded
	// (the latest revision is not approved; rev1 is not latest).
	saveDecision(t, s, "decision/latest-rejected", scope, core.MaterialityMaterial, core.StatusApproved)
	saveDecision(t, s, "decision/latest-rejected", scope, core.MaterialityMaterial, core.StatusRejected)

	// Chain "decision/latest-none-effect": rev1 eligible, rev2 approved but
	// fiscalEffect none → excluded (FZ-1.4 fails on the latest revision).
	normal := core.MaterialityNormal
	saveDecisionRaw(t, s, "decision/latest-none-effect", scope, core.FiscalEffectNone, &normal, core.StatusApproved)

	// Chain "decision/latest-normal-level": rev1 eligible, rev2 approved but
	// level normal → excluded (FZ-1.5 fails on the latest revision).
	saveDecision(t, s, "decision/latest-normal-level", scope, core.MaterialityMaterial, core.StatusApproved)
	saveDecisionRaw(t, s, "decision/latest-normal-level", scope, core.FiscalEffectJournalEntry, &normal, core.StatusApproved)

	// Chain "decision/superseded-only": rev1 eligible, rev2 VOIDED → excluded.
	saveDecision(t, s, "decision/superseded-only", scope, core.MaterialityMaterial, core.StatusApproved)
	saveDecision(t, s, "decision/superseded-only", scope, core.MaterialityMaterial, core.StatusVoided)

	// Chain "decision/approval-effect": approved but fiscalEffect approval →
	// excluded (not one of the six frozen effects).
	saveDecisionRaw(t, s, "decision/approval-effect", scope, core.FiscalEffectApproval, &normal, core.StatusApproved)

	// Chain "decision/nil-level": approved journal_entry with a NIL declared
	// level (nil means normal) → excluded.
	saveDecisionRaw(t, s, "decision/nil-level", scope, core.FiscalEffectJournalEntry, nil, core.StatusApproved)

	heads, err := s.LatestMaterialDecisionHeads(ctx, scope)
	if err != nil {
		t.Fatalf("LatestMaterialDecisionHeads: %v", err)
	}
	if len(heads) != 1 {
		t.Fatalf("heads = %d, want exactly 1 (only decision/latest-ok rev2); got %+v", len(heads), headIDs(heads))
	}
	if heads[0].Identity.TopicKey != "decision/latest-ok" {
		t.Fatalf("head = %s, want decision/latest-ok", heads[0].Identity.TopicKey)
	}
	if heads[0].Revision != 2 {
		t.Fatalf("head revision = %d, want 2 (the maximum revision of the chain)", heads[0].Revision)
	}
}

func headIDs(heads []core.AccountingMemory) []string {
	ids := make([]string, len(heads))
	for i, h := range heads {
		ids[i] = h.Identity.TopicKey + "@" + h.Scope.Period + ":" + h.Identity.ID
	}
	return ids
}

// TestLatestMaterialDecisionHeadsValidEmptyScopeReturnsZero pins the empty
// period representation: a valid exact scope with no material decisions is
// ZERO heads and NO error — the metric reports zeroDenominator, never a failure.
func TestLatestMaterialDecisionHeadsValidEmptyScopeReturnsZero(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scope := testScope(testRucA)
	scope.Period = "202412" // no rows

	heads, err := s.LatestMaterialDecisionHeads(ctx, scope)
	if err != nil {
		t.Fatalf("empty scope returned error: %v", err)
	}
	if len(heads) != 0 {
		t.Fatalf("heads = %d, want 0", len(heads))
	}
}

// TestLatestMaterialDecisionHeadsFailsClosedOnPartialScope pins FR-9 (i): a
// missing, ambiguous or malformed scope/period fails closed with a typed error —
// cross-tenant or partial aggregation is forbidden.
func TestLatestMaterialDecisionHeadsFailsClosedOnPartialScope(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		scope   core.Scope
		wantErr string
	}{
		{
			name:    "institutional scope is not an exact company scope",
			scope:   core.Scope{Kind: core.ScopeKindInstitutional},
			wantErr: "INVALID_RECONSTRUCTIBILITY_SCOPE",
		},
		{
			name:    "missing period",
			scope:   core.Scope{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, CompanyID: "acme", RUC: testRucA},
			wantErr: "INVALID_RECONSTRUCTIBILITY_SCOPE",
		},
		{
			name:    "malformed period",
			scope:   core.Scope{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, CompanyID: "acme", RUC: testRucA, Period: "2024x1"},
			wantErr: "INVALID_PERIOD",
		},
		{
			name:    "missing company id",
			scope:   core.Scope{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, RUC: testRucA, Period: "202401"},
			wantErr: "INVALID_SCOPE",
		},
		{
			name:    "missing ruc",
			scope:   core.Scope{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, CompanyID: "acme", Period: "202401"},
			wantErr: "INVALID_RUC",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			heads, err := s.LatestMaterialDecisionHeads(ctx, tt.scope)
			if err == nil {
				t.Fatalf("expected a typed failure for scope %+v, got %d heads", tt.scope, len(heads))
			}
			if !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want prefix %q", err.Error(), tt.wantErr)
			}
		})
	}
}
