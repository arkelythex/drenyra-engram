// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.5.0 CloseSnapshot
// model (docs/architecture/close-intelligence-v0.5.md §2.1): the canonical JSON
// bytes, the self-hash, the optional hash contribution (nil snapshot → legacy
// envelopes stay byte-identical) and the IsCloseMemory predicate that drives the
// approval-time closure projection.
package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// sampleSnapshot returns a deterministic CloseSnapshot exercising every field.
func sampleSnapshot(t *testing.T) *CloseSnapshot {
	t.Helper()
	cents := int64(1234567890) // signed cents — a negative total is legal too
	snap := CloseSnapshot{
		Period:      "202407",
		GeneratedAt: "2026-08-05T23:00:00Z",
		SummaryHash: "",
		Counts: CloseCounts{
			Total:    3,
			ByKind:   map[string]int{"fact": 2, "exception": 1},
			ByStatus: map[string]int{"approved": 2, "pending_review": 1},
		},
		Totals: []CloseTotal{
			{Code: "igv", Currency: "PEN", AmountCents: cents, SourceMemoryIDs: []string{"m-1", "m-2"}},
			{Code: "ventas", Currency: "PEN", AmountCents: -cents, SourceMemoryIDs: []string{"m-1"}},
		},
		PendingItems: []ClosePendingItem{
			{MemoryID: "m-3", TopicKey: "tax.igv.rate", Kind: "rule", Status: "pending_review", Title: "IGV rate", EffectiveAt: "2026-07-15T00:00:00Z"},
		},
		Reconciliation:     CloseReconciliation{Proposed: 1, Confirmed: 1, Rejected: 0},
		NarrativeMemoryIDs: []string{"m-1", "m-2"},
	}
	return &snap
}

func TestCloseSnapshotCanonicalJSONDeterministic(t *testing.T) {
	a := CanonicalCloseSnapshotJSON(sampleSnapshot(t))
	b := CanonicalCloseSnapshotJSON(sampleSnapshot(t))
	if string(a) != string(b) {
		t.Fatal("canonical snapshot JSON must be deterministic")
	}
	// The canonical bytes decode back to an identical snapshot (round-trip).
	var decoded CloseSnapshot
	if err := json.Unmarshal(a, &decoded); err != nil {
		t.Fatalf("canonical bytes must decode: %v", err)
	}
	if decoded.Period != "202407" || decoded.Counts.Total != 3 {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
	if decoded.Totals[0].AmountCents != 1234567890 {
		t.Fatalf("amountCents round-trip = %d, want 1234567890 (signed int64 cents)", decoded.Totals[0].AmountCents)
	}
	if decoded.Totals[1].AmountCents != -1234567890 {
		t.Fatalf("negative amountCents round-trip = %d, want -1234567890 (signed)", decoded.Totals[1].AmountCents)
	}
}

func TestCloseSnapshotSummaryHashDigestsCanonicalBytes(t *testing.T) {
	snap := sampleSnapshot(t)
	digest := CloseSnapshotSummaryHash(snap)
	if digest == "" {
		t.Fatal("summary hash must be non-empty")
	}
	snap.Counts.Total++ // mutate AFTER hashing
	if again := CloseSnapshotSummaryHash(snap); again == digest {
		t.Fatal("summary hash must change when the snapshot changes")
	}
}

// TestCloseSnapshotHashContributionPreservesLegacyHashes is the frozen v0.3 hash
// contract decision: the NEW optional field contributes the empty string when
// absent, so a memory WITHOUT a snapshot hashes byte-identically to its pre-v6
// value; a memory WITH a snapshot hashes differently and deterministically.
func TestCloseSnapshotHashContributionPreservesLegacyHashes(t *testing.T) {
	base := AccountingMemory{
		Identity:     Identity{ID: "m-1", TopicKey: "tax.igv.rate"},
		Title:        "IGV base rate",
		Kind:         KindRule,
		Scope:        Scope{Kind: ScopeKindCompany, OrganizationID: "org-1", CompanyID: "acme", RUC: "20100039201", Period: "202407"},
		Content:      Content{What: "rate", Why: "law", Where: "Peru", Learned: "apply"},
		Status:       StatusActive,
		FiscalEffect: FiscalEffectNone,
		EffectiveAt:  "2026-07-01T00:00:00Z",
		RecordedAt:   "2026-08-01T00:00:00Z",
		Source:       Source{System: "go-test", ActorKind: ActorKindAgent},
		Revision:     1,
	}
	base.ContentHash = ComputeContentHash(base)
	base.IdentityHash = ComputeIdentityHash(base)
	base.EnvelopeHash = ComputeEnvelopeHash(base)

	// A nil snapshot contributes NOTHING: legacy hashes stay byte-identical.
	clone := CloneMemory(base)
	clone.CloseSnapshot = nil
	if ComputeContentHash(clone) != base.ContentHash {
		t.Fatal("nil snapshot must preserve the content hash")
	}
	if ComputeEnvelopeHash(clone) != base.EnvelopeHash {
		t.Fatal("nil snapshot must preserve the envelope hash")
	}

	// A present snapshot changes both hashes deterministically.
	withSnap := CloneMemory(base)
	withSnap.CloseSnapshot = sampleSnapshot(t)
	if ComputeContentHash(withSnap) == base.ContentHash {
		t.Fatal("a snapshot must change the content hash")
	}
	if ComputeEnvelopeHash(withSnap) == base.EnvelopeHash {
		t.Fatal("a snapshot must change the envelope hash")
	}
	again := CloneMemory(withSnap)
	if ComputeEnvelopeHash(again) != ComputeEnvelopeHash(withSnap) {
		t.Fatal("equal snapshots must produce equal envelope hashes")
	}
}

// TestIsCloseMemory freezes the valid-close predicate: kind=summary,
// fiscalEffect=closing, exact company scope with a period, and the canonical
// closing/CIERRE-<period> topic. Everything else is NOT a close — in particular
// a rule with fiscalEffect=closing (the generic gated path) never projects a
// period closure.
func TestIsCloseMemory(t *testing.T) {
	valid := AccountingMemory{
		Identity:     Identity{ID: "m-close", TopicKey: CloseTopicPrefix + "202407"},
		Kind:         KindSummary,
		FiscalEffect: FiscalEffectClosing,
		Scope:        Scope{Kind: ScopeKindCompany, OrganizationID: "org-1", CompanyID: "acme", RUC: "20100039201", Period: "202407"},
	}
	if !IsCloseMemory(valid) {
		t.Fatal("a summary+closing memory with the canonical topic must be a valid close")
	}

	bad := []struct {
		name string
		mut  func(m *AccountingMemory)
	}{
		{"wrong kind", func(m *AccountingMemory) { m.Kind = KindRule }},
		{"wrong fiscal effect", func(m *AccountingMemory) { m.FiscalEffect = FiscalEffectNone }},
		{"institutional scope", func(m *AccountingMemory) { m.Scope.Kind = ScopeKindInstitutional }},
		{"no period", func(m *AccountingMemory) { m.Scope.Period = "" }},
		{"wrong topic", func(m *AccountingMemory) { m.Identity.TopicKey = "closing/CIERRE-202406" }},
		{"free-form topic", func(m *AccountingMemory) { m.Identity.TopicKey = "closing/julio-2024" }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			m := valid
			tc.mut(&m)
			if IsCloseMemory(m) {
				t.Fatalf("%s must not be a valid close", tc.name)
			}
		})
	}
}

func TestCloseSnapshotCanonicalJSONRejectsHTMLEscaping(t *testing.T) {
	snap := sampleSnapshot(t)
	snap.PendingItems[0].Title = "PDT <621> & SUNAT"
	bytes := CanonicalCloseSnapshotJSON(snap)
	if strings.Contains(string(bytes), "\\u003c") || strings.Contains(string(bytes), "\\u0026") {
		t.Fatalf("canonical JSON must not HTML-escape: %s", bytes)
	}
}
