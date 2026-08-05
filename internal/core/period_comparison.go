// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the PURE period-over-period
// comparison read model (v0.5.0 — docs/architecture/close-intelligence-v0.5.md
// §4). It adds NO schema: the comparison service (internal/server) loads the two
// periods' current memories (FindByScope + latest-per-chain), pending-item
// digests and closure states, then delegates the deterministic delta computation
// to ComputePeriodComparison. Same input memories → byte-identical output
// (arrays are stable-sorted, maps serialize with Go's deterministic key order).
package core

import (
	"sort"
	"strings"
)

// PeriodComparison is the pure scope-first read model of a period-over-period
// comparison (design §4):
//
//	from, to, counts{fromTotal, toTotal, delta, byKindDelta, byStatusDelta},
//	chains{new[], removed[], changed[], unchangedCount},
//	statusChanges[]{topicKey, fromId, toId, fromStatus, toStatus},
//	pendingItems{from, to, delta, addedIds[], resolvedIds[]},
//	closeState{from, to}, narrative
//
// Chains are matched by topic key after the exact scope is stripped (the two
// scopes differ ONLY by period, so a topic key identifies one chain per period).
// `changed` means canonical content / envelope-relevant fields differ (see
// chainsDiffer); status differences are reported SEPARATELY in statusChanges —
// a chain may legitimately appear in both lists.
type PeriodComparison struct {
	// From and To are the compared YYYYMM periods.
	From string `json:"from"`
	To   string `json:"to"`
	// Counts aggregates the current-memory totals and per-kind/per-status deltas.
	Counts PeriodCounts `json:"counts"`
	// Chains lists the new/removed/changed chains and the unchanged count.
	Chains PeriodChains `json:"chains"`
	// StatusChanges reports every matched chain whose lifecycle status differs
	// between the two periods (e.g. pending_review → approved).
	StatusChanges []StatusChange `json:"statusChanges"`
	// PendingItems is the pending-item digest delta (pending_review memories +
	// active/pending/approved obligations and exceptions, design §2.2).
	PendingItems PendingItemsDelta `json:"pendingItems"`
	// CloseState carries the period_closures projection state per period
	// ("open" when the period was never closed, else "closed"|"reopened").
	CloseState CloseStatePair `json:"closeState"`
	// Narrative is the deterministic human-readable delta summary.
	Narrative string `json:"narrative"`
}

// PeriodCounts is the count digest of the comparison (design §4 counts).
type PeriodCounts struct {
	FromTotal int `json:"fromTotal"`
	ToTotal   int `json:"toTotal"`
	// Delta is toTotal − fromTotal.
	Delta int `json:"delta"`
	// ByKindDelta maps each kind present in either period to toCount − fromCount.
	ByKindDelta map[string]int `json:"byKindDelta"`
	// ByStatusDelta maps each status present in either period to
	// toCount − fromCount.
	ByStatusDelta map[string]int `json:"byStatusDelta"`
}

// PeriodChains is the chain-membership digest of the comparison (design §4
// chains). Every array is stable-sorted by topic key then memory ID.
type PeriodChains struct {
	// New are chains present only in the `to` period.
	New []ChainRef `json:"new"`
	// Removed are chains present only in the `from` period.
	Removed []ChainRef `json:"removed"`
	// Changed are matched chains whose canonical content / envelope-relevant
	// fields differ (chainsDiffer); status-only changes also land here AND in
	// StatusChanges.
	Changed []ChainChange `json:"changed"`
	// UnchangedCount is the number of matched chains with identical canonical
	// content and envelope-relevant state.
	UnchangedCount int `json:"unchangedCount"`
}

// ChainRef is one chain identified by topic key in a new/removed list.
type ChainRef struct {
	TopicKey string `json:"topicKey"`
	MemoryID string `json:"memoryId"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Title    string `json:"title"`
}

// ChainChange is one matched chain whose canonical content / envelope-relevant
// state differs between the periods.
type ChainChange struct {
	TopicKey string `json:"topicKey"`
	FromID   string `json:"fromId"`
	ToID     string `json:"toId"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
}

// StatusChange reports one matched chain whose lifecycle status differs between
// the periods (design §4 statusChanges).
type StatusChange struct {
	TopicKey   string `json:"topicKey"`
	FromID     string `json:"fromId"`
	ToID       string `json:"toId"`
	FromStatus string `json:"fromStatus"`
	ToStatus   string `json:"toStatus"`
}

// PendingItemsDelta is the pending-item digest delta between the periods (design
// §4 pendingItems), keyed by CHAIN (topic key) like every other comparison
// array: a chain pending in both periods carries over and is neither added nor
// resolved even when its current revision (memory ID) changed. AddedIDs are the
// `to` period's current memory IDs of chains pending only in `to`; ResolvedIDs
// are the `from` period's current memory IDs of chains pending only in `from`
// (approved, rejected, voided, superseded or otherwise left the digest). Both
// lists are sorted ascending.
type PendingItemsDelta struct {
	From        int      `json:"from"`
	To          int      `json:"to"`
	Delta       int      `json:"delta"`
	AddedIDs    []string `json:"addedIds"`
	ResolvedIDs []string `json:"resolvedIds"`
}

// CloseStatePair carries the period_closures projection state of each period
// ("open" | "closed" | "reopened").
type CloseStatePair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ComputePeriodComparison derives the deterministic period-over-period delta
// from the two periods' CURRENT memories (latest revision per chain), pending
// item digests and closure states. It is a pure function: no store, no clock, no
// I/O — same inputs always produce byte-identical output.
func ComputePeriodComparison(fromPeriod, toPeriod string, from, to []AccountingMemory, fromPending, toPending []ClosePendingItem, fromCloseState, toCloseState string) PeriodComparison {
	fromByTopic := indexByTopic(from)
	toByTopic := indexByTopic(to)

	comparison := PeriodComparison{
		From: fromPeriod,
		To:   toPeriod,
		Counts: PeriodCounts{
			FromTotal:     len(from),
			ToTotal:       len(to),
			Delta:         len(to) - len(from),
			ByKindDelta:   kindDeltas(from, to),
			ByStatusDelta: statusDeltas(from, to),
		},
		StatusChanges: []StatusChange{},
		PendingItems:  pendingDelta(fromPending, toPending),
		CloseState:    CloseStatePair{From: fromCloseState, To: toCloseState},
	}

	// Match chains by topic key (the exact scope is stripped: the two scopes
	// differ only by period). Deterministic per-chain classification.
	for topicKey, toMem := range toByTopic {
		fromMem, matched := fromByTopic[topicKey]
		if !matched {
			comparison.Chains.New = append(comparison.Chains.New, chainRef(toMem))
			continue
		}
		if chainsDiffer(fromMem, toMem) {
			comparison.Chains.Changed = append(comparison.Chains.Changed, ChainChange{
				TopicKey: topicKey,
				FromID:   fromMem.Identity.ID,
				ToID:     toMem.Identity.ID,
				Kind:     string(toMem.Kind),
				Title:    toMem.Title,
			})
		} else {
			comparison.Chains.UnchangedCount++
		}
		if fromMem.Status != toMem.Status {
			comparison.StatusChanges = append(comparison.StatusChanges, StatusChange{
				TopicKey:   topicKey,
				FromID:     fromMem.Identity.ID,
				ToID:       toMem.Identity.ID,
				FromStatus: string(fromMem.Status),
				ToStatus:   string(toMem.Status),
			})
		}
	}
	for topicKey, fromMem := range fromByTopic {
		if _, matched := toByTopic[topicKey]; !matched {
			comparison.Chains.Removed = append(comparison.Chains.Removed, chainRef(fromMem))
		}
	}

	sort.SliceStable(comparison.Chains.New, func(i, j int) bool {
		return chainRefLess(comparison.Chains.New[i], comparison.Chains.New[j])
	})
	sort.SliceStable(comparison.Chains.Removed, func(i, j int) bool {
		return chainRefLess(comparison.Chains.Removed[i], comparison.Chains.Removed[j])
	})
	sort.SliceStable(comparison.Chains.Changed, func(i, j int) bool {
		a, b := comparison.Chains.Changed[i], comparison.Chains.Changed[j]
		if a.TopicKey != b.TopicKey {
			return a.TopicKey < b.TopicKey
		}
		if a.FromID != b.FromID {
			return a.FromID < b.FromID
		}
		return a.ToID < b.ToID
	})
	sort.SliceStable(comparison.StatusChanges, func(i, j int) bool {
		a, b := comparison.StatusChanges[i], comparison.StatusChanges[j]
		if a.TopicKey != b.TopicKey {
			return a.TopicKey < b.TopicKey
		}
		if a.FromID != b.FromID {
			return a.FromID < b.FromID
		}
		return a.ToID < b.ToID
	})

	comparison.Narrative = comparisonNarrative(comparison)
	return comparison
}

// indexByTopic maps each current memory by its topic key. The exact scope is
// implicitly stripped: within one FindByScope result every (topicKey, scope)
// chain is unique, and the two compared scopes differ only by period.
func indexByTopic(memories []AccountingMemory) map[string]AccountingMemory {
	out := make(map[string]AccountingMemory, len(memories))
	for _, memory := range memories {
		out[memory.Identity.TopicKey] = memory
	}
	return out
}

// kindDeltas maps every kind present in either period to toCount − fromCount.
func kindDeltas(from, to []AccountingMemory) map[string]int {
	fromCounts := make(map[string]int)
	for _, memory := range from {
		fromCounts[string(memory.Kind)]++
	}
	toCounts := make(map[string]int)
	for _, memory := range to {
		toCounts[string(memory.Kind)]++
	}
	out := make(map[string]int)
	for kind := range fromCounts {
		out[kind] = -fromCounts[kind]
	}
	for kind, count := range toCounts {
		out[kind] += count
	}
	return out
}

// statusDeltas maps every status present in either period to toCount − fromCount.
func statusDeltas(from, to []AccountingMemory) map[string]int {
	fromCounts := make(map[string]int)
	for _, memory := range from {
		fromCounts[string(memory.Status)]++
	}
	toCounts := make(map[string]int)
	for _, memory := range to {
		toCounts[string(memory.Status)]++
	}
	out := make(map[string]int)
	for status := range fromCounts {
		out[status] = -fromCounts[status]
	}
	for status, count := range toCounts {
		out[status] += count
	}
	return out
}

// chainRef builds the deterministic new/removed reference of one current memory.
func chainRef(memory AccountingMemory) ChainRef {
	return ChainRef{
		TopicKey: memory.Identity.TopicKey,
		MemoryID: memory.Identity.ID,
		Kind:     string(memory.Kind),
		Status:   string(memory.Status),
		Title:    memory.Title,
	}
}

// chainRefLess orders new/removed references by topic key then memory ID
// (stable — design §4 "Stable-sort arrays by topic key then memory ID").
func chainRefLess(a, b ChainRef) bool {
	if a.TopicKey != b.TopicKey {
		return a.TopicKey < b.TopicKey
	}
	return a.MemoryID < b.MemoryID
}

// chainsDiffer reports whether the two current revisions of the same chain carry
// different canonical content or envelope-relevant state between periods:
//   - the canonical content hash computed with the SCOPE STRIPPED (see
//     strippedContentHash — the compared scopes differ only by period, and the
//     raw content hash embeds the scope, so a raw comparison would mark EVERY
//     matched chain as changed);
//   - the lifecycle Status;
//   - the evidence and rule link SETS (order-insensitive — links grow AFTER
//     write through the dedicated link records);
//   - the SupersedesID supersession link.
//
// Pure write-time metadata (RecordedAt, revision) NEVER marks a chain changed on
// its own: the comparison reports WHAT each period says about the chain, not
// WHEN it was written. Status differences are additionally reported separately
// in StatusChanges (design §4) — a chain may appear in both lists.
func chainsDiffer(a, b AccountingMemory) bool {
	if strippedContentHash(a) != strippedContentHash(b) {
		return true
	}
	if a.Status != b.Status {
		return true
	}
	if a.SupersedesID != b.SupersedesID {
		return true
	}
	return !refSetsEqual(a.EvidenceRefs, b.EvidenceRefs) || !refSetsEqual(a.RuleRefs, b.RuleRefs)
}

// strippedContentHash is the canonical content hash of a memory with the scope
// PERIOD stripped. The canonical hash participates in the scope (ScopeKey embeds
// the period), but the two compared scopes differ ONLY by period by construction
// — the period must never mark a chain changed on its own. The function reuses
// the canonical ComputeContentHash (same engine, frozen v0.3/v0.5 contract) over
// a period-stripped clone; no hash logic is duplicated. org/company/RUC stay
// intact: the comparison service guarantees they are equal across the pair.
func strippedContentHash(m AccountingMemory) string {
	clone := m
	clone.Scope.Period = ""
	return ComputeContentHash(clone)
}

// refSetsEqual compares two reference slices as SETS (order and duplicates are
// not semantically meaningful for evidence/rule refs).
func refSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, ref := range a {
		counts[ref]++
	}
	for _, ref := range b {
		counts[ref]--
		if counts[ref] < 0 {
			return false
		}
	}
	return true
}

// pendingDelta derives the pending-item digest delta keyed by CHAIN (topic key),
// matching the design's chain-matching rule: a pending item that carries over
// across periods is the same chain even though its current revision (memory ID)
// differs — it is neither added nor resolved. AddedIDs are the `to` period's
// current memory IDs of chains pending only in `to`; ResolvedIDs are the `from`
// period's current memory IDs of chains pending only in `from` (approved,
// rejected, voided, superseded or otherwise left the digest). Both lists are
// sorted ascending (deterministic). One chain contributes one current memory per
// period, so topic-key keys never collide within a digest.
func pendingDelta(fromPending, toPending []ClosePendingItem) PendingItemsDelta {
	fromByTopic := make(map[string]string, len(fromPending))
	for _, item := range fromPending {
		fromByTopic[item.TopicKey] = item.MemoryID
	}
	toByTopic := make(map[string]string, len(toPending))
	for _, item := range toPending {
		toByTopic[item.TopicKey] = item.MemoryID
	}
	added := make([]string, 0)
	resolved := make([]string, 0)
	for topic, id := range toByTopic {
		if _, ok := fromByTopic[topic]; !ok {
			added = append(added, id)
		}
	}
	for topic, id := range fromByTopic {
		if _, ok := toByTopic[topic]; !ok {
			resolved = append(resolved, id)
		}
	}
	sort.Strings(added)
	sort.Strings(resolved)
	return PendingItemsDelta{
		From:        len(fromPending),
		To:          len(toPending),
		Delta:       len(toPending) - len(fromPending),
		AddedIDs:    added,
		ResolvedIDs: resolved,
	}
}

// comparisonNarrative is the deterministic human-readable delta summary (design
// §4 narrative — a self-contained delta shape chosen over the periods'
// per-item narratives: it never depends on memory content or ordering, so the
// same data always produces the same text).
func comparisonNarrative(c PeriodComparison) string {
	var sb strings.Builder
	sb.WriteString("Comparacion ")
	sb.WriteString(c.From)
	sb.WriteString(" → ")
	sb.WriteString(c.To)
	sb.WriteString(": ")
	sb.WriteString(itoa(c.Counts.FromTotal))
	sb.WriteString(" memorias en el periodo de origen, ")
	sb.WriteString(itoa(c.Counts.ToTotal))
	sb.WriteString(" en el de destino (delta ")
	sb.WriteString(signed(c.Counts.Delta))
	sb.WriteString("); cadenas nuevas: ")
	sb.WriteString(itoa(len(c.Chains.New)))
	sb.WriteString(", removidas: ")
	sb.WriteString(itoa(len(c.Chains.Removed)))
	sb.WriteString(", cambiadas: ")
	sb.WriteString(itoa(len(c.Chains.Changed)))
	sb.WriteString(", sin cambios: ")
	sb.WriteString(itoa(c.Chains.UnchangedCount))
	sb.WriteString("; cambios de estado: ")
	sb.WriteString(itoa(len(c.StatusChanges)))
	sb.WriteString("; items pendientes: ")
	sb.WriteString(itoa(c.PendingItems.From))
	sb.WriteString(" → ")
	sb.WriteString(itoa(c.PendingItems.To))
	sb.WriteString(" (delta ")
	sb.WriteString(signed(c.PendingItems.Delta))
	sb.WriteString("); estado de cierre: ")
	sb.WriteString(c.CloseState.From)
	sb.WriteString(" → ")
	sb.WriteString(c.CloseState.To)
	sb.WriteString(".")
	return sb.String()
}

// signed formats n with an explicit + for positive values (the narrative delta
// convention: "+2", "0", "-1").
func signed(n int) string {
	if n > 0 {
		return "+" + itoa(n)
	}
	return itoa(n)
}

// itoa is a small allocation-free int formatter for the narrative (mirrors the
// server package's helper; core keeps its own to stay a self-contained pure
// module).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	negative := n < 0
	if negative {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
