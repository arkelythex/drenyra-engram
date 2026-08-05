// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. These tests drive the PURE
// period-over-period comparison read model (v0.5.0, design §4): deterministic
// chain/status/pending/close deltas with stable ordering. There are no monetary
// fields in the comparison model — no money value is asserted.

package core

import (
	"encoding/json"
	"testing"
)

// comparisonMemory builds one current-revision memory with the given identity,
// kind, status and content. The canonical ContentHash is computed the same way
// the store computes it at write time.
func comparisonMemory(id, topicKey, kind, status, title, what string) AccountingMemory {
	memory := AccountingMemory{
		Identity: Identity{ID: id, TopicKey: topicKey},
		Title:    title,
		Kind:     MemoryKind(kind),
		Scope: Scope{
			Kind:           ScopeKindCompany,
			OrganizationID: "cmp_org",
			CompanyID:      "cmp_01",
			RUC:            "20601234567",
			Period:         "202607",
		},
		Content: Content{What: what, Why: "fixture", Where: "core", Learned: "n/a"},
		Status:  MemoryStatus(status),
	}
	memory.ContentHash = ComputeContentHash(memory)
	return memory
}

// reperiod returns a clone of memory with the scope period replaced (the pure
// comparison matches chains by topic key across two scopes that differ only by
// period; the fixtures model exactly that).
func reperiod(memory AccountingMemory, period string) AccountingMemory {
	cloned := memory
	cloned.Scope.Period = period
	return cloned
}

func TestComputePeriodComparisonDeterministicDeltas(t *testing.T) {
	// July: three chains — account/4011 (content A), fact/igv-tasa (content 18%),
	// obligation/igv-621. August: account/4011 with CHANGED content, fact/igv-tasa
	// unchanged, and a NEW chain account/4011/ventas-agosto; obligation/igv-621 is
	// REMOVED. Expect: new=1, removed=1, changed=1, unchanged=1, no status changes.
	from := []AccountingMemory{
		comparisonMemory("j1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo"),
		comparisonMemory("j2", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%"),
		comparisonMemory("j3", "obligation/igv-621", "obligation", "active", "Obligacion PDT 621", "declarar IGV julio"),
	}
	to := []AccountingMemory{
		reperiod(comparisonMemory("a1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo CORREGIDAS"), "202608"),
		reperiod(comparisonMemory("a2", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%"), "202608"),
		reperiod(comparisonMemory("a3", "account/4011/ventas-agosto", "fact", "active", "Ventas agosto", "ventas de agosto"), "202608"),
	}

	got := ComputePeriodComparison("202607", "202608", from, to, nil, nil, "open", "open")

	if got.From != "202607" || got.To != "202608" {
		t.Fatalf("periods = %q/%q, want 202607/202608", got.From, got.To)
	}
	if got.Counts.FromTotal != 3 || got.Counts.ToTotal != 3 || got.Counts.Delta != 0 {
		t.Fatalf("counts = %+v, want fromTotal 3, toTotal 3, delta 0", got.Counts)
	}
	// July: fact x2, obligation x1. August: fact x3, obligation x0.
	if got.Counts.ByKindDelta["fact"] != 1 || got.Counts.ByKindDelta["obligation"] != -1 {
		t.Fatalf("byKindDelta = %v, want fact +1, obligation -1", got.Counts.ByKindDelta)
	}
	if got.Counts.ByStatusDelta["active"] != 0 {
		t.Fatalf("byStatusDelta = %v, want active 0", got.Counts.ByStatusDelta)
	}
	if len(got.Chains.New) != 1 || got.Chains.New[0].TopicKey != "account/4011/ventas-agosto" {
		t.Fatalf("new = %+v, want exactly account/4011/ventas-agosto", got.Chains.New)
	}
	if len(got.Chains.Removed) != 1 || got.Chains.Removed[0].TopicKey != "obligation/igv-621" {
		t.Fatalf("removed = %+v, want exactly obligation/igv-621", got.Chains.Removed)
	}
	if len(got.Chains.Changed) != 1 || got.Chains.Changed[0].TopicKey != "account/4011" {
		t.Fatalf("changed = %+v, want exactly account/4011", got.Chains.Changed)
	}
	if got.Chains.UnchangedCount != 1 {
		t.Fatalf("unchangedCount = %d, want 1 (fact/igv-tasa)", got.Chains.UnchangedCount)
	}
	if len(got.StatusChanges) != 0 {
		t.Fatalf("statusChanges = %+v, want none", got.StatusChanges)
	}
	if got.CloseState.From != "open" || got.CloseState.To != "open" {
		t.Fatalf("closeState = %+v, want open/open", got.CloseState)
	}
}

func TestComputePeriodComparisonStatusChange(t *testing.T) {
	// The same chain (same canonical content) moves pending_review → approved
	// between the periods: it is BOTH a changed chain (envelope state differs)
	// and a status change (design §4 reports the transition separately).
	from := []AccountingMemory{
		comparisonMemory("j1", "adjust/aj-001", "decision", "pending_review", "Ajuste AJ-001", "ajuste por comprobante tardio"),
	}
	to := []AccountingMemory{
		reperiod(comparisonMemory("a1", "adjust/aj-001", "decision", "approved", "Ajuste AJ-001", "ajuste por comprobante tardio"), "202608"),
	}

	got := ComputePeriodComparison("202607", "202608", from, to, nil, nil, "open", "open")

	if len(got.Chains.Changed) != 1 {
		t.Fatalf("changed = %+v, want the status-changed chain listed", got.Chains.Changed)
	}
	if got.Chains.Changed[0].FromID != "j1" || got.Chains.Changed[0].ToID != "a1" {
		t.Fatalf("changed ids = %q/%q, want j1/a1", got.Chains.Changed[0].FromID, got.Chains.Changed[0].ToID)
	}
	if got.Chains.UnchangedCount != 0 {
		t.Fatalf("unchangedCount = %d, want 0", got.Chains.UnchangedCount)
	}
	if len(got.StatusChanges) != 1 {
		t.Fatalf("statusChanges = %+v, want exactly one", got.StatusChanges)
	}
	sc := got.StatusChanges[0]
	if sc.TopicKey != "adjust/aj-001" || sc.FromStatus != "pending_review" || sc.ToStatus != "approved" {
		t.Fatalf("statusChange = %+v, want adjust/aj-001 pending_review → approved", sc)
	}
}

func TestComputePeriodComparisonContentHashInsensitiveToWriteTime(t *testing.T) {
	// The changed predicate compares canonical content + envelope state, NOT the
	// write-time envelope: two revisions with identical content but different
	// recordedAt/revision are UNCHANGED (deterministic period deltas never trip
	// on when a memory was written).
	fromMem := comparisonMemory("j1", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%")
	toMem := comparisonMemory("a1", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%")
	fromMem.RecordedAt = "2026-07-01T00:00:00Z"
	toMem.RecordedAt = "2026-08-01T00:00:00Z"
	fromMem.Revision = 1
	toMem.Revision = 2

	got := ComputePeriodComparison("202607", "202608", []AccountingMemory{fromMem}, []AccountingMemory{toMem}, nil, nil, "open", "open")
	if len(got.Chains.Changed) != 0 {
		t.Fatalf("changed = %+v, want none (only write-time metadata differs)", got.Chains.Changed)
	}
	if got.Chains.UnchangedCount != 1 {
		t.Fatalf("unchangedCount = %d, want 1", got.Chains.UnchangedCount)
	}
}

func TestComputePeriodComparisonLinkSetsDiffer(t *testing.T) {
	// Evidence/rule links grow AFTER write through the dedicated link records: a
	// chain whose link SET differs between periods is CHANGED, while a chain
	// whose links appear in a different ORDER is UNCHANGED (links are sets —
	// order is not semantically meaningful).
	base := comparisonMemory("j1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo")

	reordered := reperiod(comparisonMemory("a1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo"), "202608")
	reordered.EvidenceRefs = []string{"xml/ventas.xml", "cdr/ventas.cdr"}
	base.EvidenceRefs = []string{"cdr/ventas.cdr", "xml/ventas.xml"}
	got := ComputePeriodComparison("202607", "202608", []AccountingMemory{base}, []AccountingMemory{reordered}, nil, nil, "open", "open")
	if len(got.Chains.Changed) != 0 || got.Chains.UnchangedCount != 1 {
		t.Fatalf("same link set in different order must be unchanged: changed=%+v unchanged=%d", got.Chains.Changed, got.Chains.UnchangedCount)
	}

	grown := reperiod(comparisonMemory("a2", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo"), "202608")
	grown.EvidenceRefs = []string{"xml/ventas.xml", "cdr/ventas.cdr", "extracto/ventas.pdf"}
	base2 := comparisonMemory("j2", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo")
	base2.EvidenceRefs = []string{"xml/ventas.xml", "cdr/ventas.cdr"}
	got2 := ComputePeriodComparison("202607", "202608", []AccountingMemory{base2}, []AccountingMemory{grown}, nil, nil, "open", "open")
	if len(got2.Chains.Changed) != 1 {
		t.Fatalf("a grown link set must be changed, got %+v", got2.Chains.Changed)
	}
}

func TestComputePeriodComparisonPendingItemsDelta(t *testing.T) {
	// The obligation carries over ACROSS periods as the same chain under a NEW
	// current revision (memory ID) — keyed by topic key, it is neither added nor
	// resolved. The adjustment left the digest (approved): resolved. The August
	// exception is a brand-new pending chain: added.
	fromPending := []ClosePendingItem{
		{MemoryID: "mem-pend-a", TopicKey: "adjust/aj-001"},
		{MemoryID: "mem-pend-b", TopicKey: "obligation/igv-621"},
	}
	toPending := []ClosePendingItem{
		{MemoryID: "mem-pend-b2", TopicKey: "obligation/igv-621"},
		{MemoryID: "mem-pend-c", TopicKey: "exception/banco-002"},
	}

	got := ComputePeriodComparison("202607", "202608", nil, nil, fromPending, toPending, "open", "open")

	if got.PendingItems.From != 2 || got.PendingItems.To != 2 || got.PendingItems.Delta != 0 {
		t.Fatalf("pendingItems counts = %+v, want from 2, to 2, delta 0", got.PendingItems)
	}
	if len(got.PendingItems.AddedIDs) != 1 || got.PendingItems.AddedIDs[0] != "mem-pend-c" {
		t.Fatalf("addedIds = %v, want [mem-pend-c]", got.PendingItems.AddedIDs)
	}
	if len(got.PendingItems.ResolvedIDs) != 1 || got.PendingItems.ResolvedIDs[0] != "mem-pend-a" {
		t.Fatalf("resolvedIds = %v, want [mem-pend-a]", got.PendingItems.ResolvedIDs)
	}
}

func TestComputePeriodComparisonCloseState(t *testing.T) {
	got := ComputePeriodComparison("202607", "202608", nil, nil, nil, nil, "closed", "open")
	if got.CloseState.From != "closed" || got.CloseState.To != "open" {
		t.Fatalf("closeState = %+v, want closed/open (a closed period vs an open one)", got.CloseState)
	}
}

func TestComputePeriodComparisonStableSorting(t *testing.T) {
	// Arrays are stable-sorted by topic key then memory ID regardless of input
	// order (design §4) — the delta is byte-deterministic.
	from := []AccountingMemory{
		comparisonMemory("jz", "topic/zeta", "fact", "active", "Z", "contenido z"),
		comparisonMemory("ja", "topic/alfa", "fact", "active", "A", "contenido a"),
	}
	to := []AccountingMemory{
		comparisonMemory("az", "topic/zeta", "fact", "active", "Z", "contenido z CAMBIADO"),
		comparisonMemory("ab", "topic/bravo", "fact", "active", "B", "contenido b"),
		comparisonMemory("aa", "topic/alfa", "fact", "active", "A", "contenido a"),
	}

	got := ComputePeriodComparison("202607", "202608", from, to, nil, nil, "open", "open")

	if len(got.Chains.New) != 1 || got.Chains.New[0].TopicKey != "topic/bravo" {
		t.Fatalf("new = %+v, want exactly topic/bravo", got.Chains.New)
	}
	if len(got.Chains.Removed) != 0 {
		t.Fatalf("removed = %+v, want none", got.Chains.Removed)
	}
	if len(got.Chains.Changed) != 1 || got.Chains.Changed[0].TopicKey != "topic/zeta" {
		t.Fatalf("changed = %+v, want exactly topic/zeta", got.Chains.Changed)
	}
	if got.Chains.UnchangedCount != 1 {
		t.Fatalf("unchangedCount = %d, want 1 (topic/alfa)", got.Chains.UnchangedCount)
	}
}

func TestComputePeriodComparisonDeterminism(t *testing.T) {
	// Same data → byte-identical output (deterministic deltas, design §9.5).
	from := []AccountingMemory{
		comparisonMemory("j1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo"),
		comparisonMemory("j2", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%"),
		comparisonMemory("j3", "obligation/igv-621", "obligation", "active", "Obligacion PDT 621", "declarar IGV julio"),
		comparisonMemory("j4", "exception/banco-001", "exception", "active", "Diferencia banco", "extracto vs libro"),
		comparisonMemory("j5", "adjust/aj-001", "decision", "pending_review", "Ajuste AJ-001", "ajuste por comprobante tardio"),
	}
	to := []AccountingMemory{
		reperiod(comparisonMemory("a1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo CORREGIDAS"), "202608"),
		reperiod(comparisonMemory("a2", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%"), "202608"),
		reperiod(comparisonMemory("a3", "account/4011/ventas-agosto", "fact", "active", "Ventas agosto", "ventas de agosto"), "202608"),
		reperiod(comparisonMemory("a4", "adjust/aj-001", "decision", "approved", "Ajuste AJ-001", "ajuste por comprobante tardio"), "202608"),
	}
	fromPending := []ClosePendingItem{
		{MemoryID: "j5", TopicKey: "adjust/aj-001"},
		{MemoryID: "j3", TopicKey: "obligation/igv-621"},
		{MemoryID: "j4", TopicKey: "exception/banco-001"},
	}
	toPending := []ClosePendingItem{
		{MemoryID: "j3", TopicKey: "obligation/igv-621"},
	}

	first := ComputePeriodComparison("202607", "202608", from, to, fromPending, toPending, "closed", "open")
	second := ComputePeriodComparison("202607", "202608", from, to, fromPending, toPending, "closed", "open")

	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("determinism violated:\nfirst  %s\nsecond %s", firstJSON, secondJSON)
	}
	// Spot-check the narrative delta summary is present and deterministic.
	if first.Narrative == "" {
		t.Fatal("narrative must not be empty")
	}
	if first.Narrative != second.Narrative {
		t.Fatalf("narrative not deterministic: %q vs %q", first.Narrative, second.Narrative)
	}
}

func TestComputePeriodComparisonEmptyPeriods(t *testing.T) {
	got := ComputePeriodComparison("202607", "202608", nil, nil, nil, nil, "open", "open")
	if got.Counts.FromTotal != 0 || got.Counts.ToTotal != 0 || got.Counts.Delta != 0 {
		t.Fatalf("counts = %+v, want all zeros", got.Counts)
	}
	if got.Chains.UnchangedCount != 0 || len(got.Chains.New)+len(got.Chains.Removed)+len(got.Chains.Changed) != 0 {
		t.Fatalf("chains = %+v, want all empty", got.Chains)
	}
	if len(got.StatusChanges) != 0 {
		t.Fatalf("statusChanges = %+v, want none", got.StatusChanges)
	}
}
