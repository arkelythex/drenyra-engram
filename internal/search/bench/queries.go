// Labeled query set — design brief §8.2: ≥200 labeled accounting queries with
// a computable relevance ground truth. Every query is generated FROM a target
// memory's distinctive tokens, so the relevant set is deterministic (the target
// id is derived, never hand-labeled). Classes: exact identifiers, account +
// period, rule/vigencia, evidence-to-decision reconstruction, typo cases,
// punctuation cases, cross-tenant probes, no-result and adversarial inputs.
//
// The corpus monetary fields (Materiality) are whole int64 cents — queries in
// this set never use monetary tokens, so the score space is term overlap only.
package bench

import (
	"fmt"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// LabeledQuery is one evaluation input with its deterministic relevance set.
type LabeledQuery struct {
	Class   string
	Query   string
	Scope   core.Scope
	Mode    searchMatchMode
	WantIDs []string // relevant set (exactly one for distinctive queries)
}

// searchMatchMode mirrors search.MatchMode without importing search (avoids an
// import cycle — bench is a sibling package).
type searchMatchMode string

const (
	matchAny searchMatchMode = "any"
	matchAll searchMatchMode = "all"
)

// GenerateQueries builds the labeled set (≥200) from the corpus, determinis-
// tically, at the CI density (PerYear). Class distribution: ~120 distinctive/
// exact, ~30 typo, ~20 account+period, ~15 rule/vigencia, ~10 punctuation,
// ~10 no-result/adversarial, and the cross-tenant probes appended separately.
func GenerateQueries(corpus []core.AccountingMemory) []LabeledQuery {
	return GenerateQueriesAt(corpus, PerYear)
}

// GenerateQueriesAt is GenerateQueries at an explicit per-year density (see
// GenerateCorpusAt). The stride constants (197, 823, 1151, 1597) are prime and
// co-prime with any per-year density, so the walk covers every class.
func GenerateQueriesAt(corpus []core.AccountingMemory, perYear int) []LabeledQuery {
	q := make([]LabeledQuery, 0, 220)
	nA := Years * perYear // RucA memories occupy indices [0, nA)

	// 1. Exact-identifier + distinctive queries (~120) — walk RucA with a
	// deterministic stride so every class of memory is covered.
	stride := 197 // prime, co-prime with any perYear
	count := 0
	for i := 0; i < nA && count < 130; i = (i + stride) % nA {
		mem := corpus[i]
		gen := i % perYear // generator index (the corpus packs perYear/year)
		doc := DocNumber(RucA, memYear(mem), gen)
		class := "exact-identifier"
		query := doc
		if count%3 == 1 {
			// Distinctive token query: doc (UNIQUE token) + noun — the target
			// scores strictly above every tied memory.
			class = "distinctive"
			query = fmt.Sprintf("%s %s", doc, contentNoun(mem))
		} else if count%3 == 2 {
			// Evidence-to-decision reconstruction: doc + verb.
			class = "reconstruction"
			query = fmt.Sprintf("%s %s", doc, contentVerb(mem))
		}
		q = append(q, LabeledQuery{Class: class, Query: query, Scope: ScopeFor(RucA, memPeriod(mem)), Mode: matchAny, WantIDs: []string{mem.Identity.ID}})
		count++
	}

	// 2. Typo cases (~30) — mutate the doc number (a transposition and a drop).
	typoCount := 0
	for i := 0; i < nA && typoCount < 36; i += 823 {
		mem := corpus[i]
		gen := i % perYear
		doc := DocNumber(RucA, memYear(mem), gen)
		var typo string
		if typoCount%2 == 0 && len(doc) > 2 {
			// Transpose two middle characters.
			b := []byte(doc)
			mid := len(b) / 2
			b[mid], b[mid+1] = b[mid+1], b[mid]
			typo = string(b)
		} else if len(doc) > 1 {
			typo = doc[:len(doc)-1] // drop the final digit
		} else {
			typo = doc
		}
		q = append(q, LabeledQuery{Class: "typo", Query: typo, Scope: ScopeFor(RucA, memPeriod(mem)), Mode: matchAny, WantIDs: []string{mem.Identity.ID}})
		typoCount++
	}

	// 3. Account + period (~20) — broad set: the target is one of the relevant
	// memories; recall@10 measures whether SOME relevant hit surfaces.
	apCount := 0
	for i := 0; i < nA && apCount < 26; i += 1151 {
		mem := corpus[i]
		acct := contentAccount(mem)
		per := memPeriod(mem)
		// Relevant = every memory with the same account code and period.
		want := []string{}
		for j, other := range corpus {
			if j >= nA {
				break
			}
			if contentAccount(other) == acct && memPeriod(other) == per {
				want = append(want, other.Identity.ID)
			}
		}
		if len(want) == 0 {
			continue
		}
		q = append(q, LabeledQuery{Class: "account-period", Query: fmt.Sprintf("%s %s", acct, per), Scope: ScopeFor(RucA, per), Mode: matchAll, WantIDs: want})
		apCount++
	}

	// 4. Rule/vigencia (~15) — a rule memory's noun + year.
	ruleCount := 0
	for i := 0; i < nA && ruleCount < 22; i += 1597 {
		mem := corpus[i]
		if mem.Kind != core.KindRule {
			continue
		}
		gen := i % perYear
		q = append(q, LabeledQuery{Class: "rule-vigencia", Query: fmt.Sprintf("%s %s", DocNumber(RucA, memYear(mem), gen), contentNoun(mem)), Scope: ScopeFor(RucA, memPeriod(mem)), Mode: matchAny, WantIDs: []string{mem.Identity.ID}})
		ruleCount++
	}

	// 5. Punctuation (~10) — the UNIQUE doc number with adversarial separators:
	// the tokenizer must split ":", "." and "_" exactly like "-", so the
	// target stays uniquely rank-1 (a separator regression is a real miss).
	for i := 0; i < 10; i++ {
		mem := corpus[(i*997)%nA]
		gen := ((i * 997) % nA) % perYear
		doc := DocNumber(RucA, memYear(mem), gen)
		sep := []string{":", ".", "_", ":"}[i%4]
		q = append(q, LabeledQuery{Class: "punctuation", Query: strings.ReplaceAll(doc, "-", sep), Scope: ScopeFor(RucA, memPeriod(mem)), Mode: matchAny, WantIDs: []string{mem.Identity.ID}})
	}

	// 6. No-result + adversarial (~10).
	q = append(q,
		LabeledQuery{Class: "no-result", Query: "zzznoexiste", Scope: ScopeFor(RucA, "202201"), Mode: matchAny, WantIDs: []string{}},
		LabeledQuery{Class: "no-result", Query: "qwertyuiopasdfghjkl", Scope: ScopeFor(RucA, "202201"), Mode: matchAny, WantIDs: []string{}},
		LabeledQuery{Class: "adversarial", Query: "!!!---___", Scope: ScopeFor(RucA, "202201"), Mode: matchAny, WantIDs: []string{}},
		LabeledQuery{Class: "adversarial", Query: "", Scope: ScopeFor(RucA, "202201"), Mode: matchAny, WantIDs: []string{}},
		LabeledQuery{Class: "adversarial", Query: "😀🎉", Scope: ScopeFor(RucA, "202201"), Mode: matchAny, WantIDs: []string{}},
		LabeledQuery{Class: "adversarial", Query: strings.Repeat("a", 10000), Scope: ScopeFor(RucA, "202201"), Mode: matchAny, WantIDs: []string{}},
		LabeledQuery{Class: "adversarial", Query: "F001-948-2026 F001-949-2026 F001-950-2026", Scope: ScopeFor(RucA, "202201"), Mode: matchAny, WantIDs: []string{}},
		LabeledQuery{Class: "no-result", Query: "  ", Scope: ScopeFor(RucA, "202201"), Mode: matchAny, WantIDs: []string{}},
	)

	return q
}

// CrossTenantProbes returns the leakage checks: queries whose RELEVANT memory
// lives in RucB, executed under a RucA scope — the relevant hit must NEVER
// surface (leakage exactly 0, §8.3).
func CrossTenantProbes(corpus []core.AccountingMemory) []LabeledQuery {
	return CrossTenantProbesAt(corpus, PerYear)
}

// CrossTenantProbesAt is CrossTenantProbes at an explicit per-year density.
func CrossTenantProbesAt(corpus []core.AccountingMemory, perYear int) []LabeledQuery {
	probes := []LabeledQuery{}
	nA := Years * perYear
	for i := nA; i < len(corpus); i += 37 {
		mem := corpus[i]
		gen := (i - nA) % 100 // RucB packs 100/year
		probes = append(probes, LabeledQuery{
			Class:   "cross-tenant",
			Query:   DocNumber(RucB, memYear(mem), gen),
			Scope:   ScopeFor(RucA, memPeriod(mem)), // RucA scope
			Mode:    matchAny,
			WantIDs: []string{mem.Identity.ID}, // relevant ONLY in RucB
		})
	}
	return probes
}

// ── corpus introspection helpers (deterministic token extraction) ──

func memYear(mem core.AccountingMemory) int {
	if len(mem.Scope.Period) >= 4 {
		var y int
		fmt.Sscanf(mem.Scope.Period[:4], "%d", &y)
		return y
	}
	return 0
}

func memPeriod(mem core.AccountingMemory) string { return mem.Scope.Period }

// contentNoun extracts the vocabulary noun embedded in the memory's content
// (the generator places it as the first content token).
func contentNoun(mem core.AccountingMemory) string {
	parts := strings.Fields(mem.Content.What)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// contentAccount extracts the account code (a bare 4-digit token in content).
func contentAccount(mem core.AccountingMemory) string {
	for _, t := range strings.Fields(mem.Content.What) {
		if len(t) == 4 && t[0] >= '0' && t[0] <= '9' {
			return t
		}
	}
	return ""
}

// contentVerb extracts the verb (the second content token).
func contentVerb(mem core.AccountingMemory) string {
	parts := strings.Fields(mem.Content.What)
	if len(parts) > 1 {
		return parts[1]
	}
	return ""
}
