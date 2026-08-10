// Benchmark harness — design brief §8.3. Measures the CURRENT token-overlap
// baseline: Recall@10, MRR@10, cross-tenant leakage (must be exactly 0),
// warm p95 latency, deterministic ordering for equal scores, and adversarial
// robustness. The §8.3 thresholds are PROPOSED design targets pending owner
// approval — the harness HARD-FAILS only on the invariants (leakage = 0, no
// crashes, determinism) and WARNS on the soft quality targets.
package bench

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/search"
)

// corpusSource adapts the deterministic corpus to search.MemorySource.
type corpusSource struct{ mems []core.AccountingMemory }

func (c corpusSource) List() ([]core.AccountingMemory, error) { return c.mems, nil }

// evalQuery runs ONE labeled query and returns the result id sequence.
func evalQuery(src search.MemorySource, q LabeledQuery) ([]string, error) {
	mode := search.MatchAny
	if q.Mode == matchAll {
		mode = search.MatchAll
	}
	res, err := search.ScopeFirst(src, search.Input{Query: q.Query, Scope: q.Scope, MatchMode: mode, Limit: 10})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(res))
	for _, r := range res {
		ids = append(ids, r.Memory.Identity.ID)
	}
	return ids, nil
}

// metrics aggregates one run of the labeled set.
type metrics struct {
	count, hits int
	rrSum       float64
	latencies   []time.Duration
	byClass     map[string]struct {
		count, hits int
		rrSum       float64
	}
}

func (m *metrics) record(class string, ids []string, want []string, elapsed time.Duration) {
	m.count++
	m.latencies = append(m.latencies, elapsed)
	if m.byClass == nil {
		m.byClass = map[string]struct {
			count, hits int
			rrSum       float64
		}{}
	}
	cls := m.byClass[class]
	cls.count++
	if len(want) == 0 {
		if len(ids) == 0 {
			m.hits++
			cls.hits++
			m.rrSum++
			cls.rrSum++
		}
		m.byClass[class] = cls
		return
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for rank, id := range ids {
		if wantSet[id] {
			m.hits++
			cls.hits++
			m.rrSum += 1.0 / float64(rank+1)
			cls.rrSum += 1.0 / float64(rank+1)
			break
		}
	}
	m.byClass[class] = cls
}

func p95(lat []time.Duration) time.Duration {
	if len(lat) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), lat...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	idx := (len(s)*95 + 99) / 100
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

// TestSearchBenchmarkMetrics runs the full labeled set against the baseline and
// reports Recall@10 / MRR@10 / warm p95 / leakage / determinism. Hard invari-
// ants fail the test; the §8.3 quality targets are reported as WARN, pending
// owner approval (they are proposed design targets, not release gates).
func TestSearchBenchmarkMetrics(t *testing.T) {
	corpus := GenerateCorpus()
	src := corpusSource{corpus}
	queries := GenerateQueries(corpus)
	probes := CrossTenantProbes(corpus)
	if len(queries) < 200 {
		t.Fatalf("labeled set = %d, want ≥200", len(queries))
	}

	for _, q := range queries[:10] {
		_, _ = evalQuery(src, q) // warmup pass
	}

	m := &metrics{}
	for _, q := range queries {
		start := time.Now()
		ids, err := evalQuery(src, q)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("query %q (%s) crashed: %v", q.Query, q.Class, err)
		}
		m.record(q.Class, ids, q.WantIDs, elapsed)
	}

	// Determinism: the SAME query must yield the IDENTICAL id sequence.
	probe := queries[1]
	first, err := evalQuery(src, probe)
	if err != nil {
		t.Fatalf("determinism probe: %v", err)
	}
	second, err := evalQuery(src, probe)
	if err != nil {
		t.Fatalf("determinism probe rerun: %v", err)
	}
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("non-deterministic ordering for equal scores: %v vs %v", first, second)
	}

	// Leakage — the HARD invariant (§8.3: exactly 0).
	leak := 0
	for _, q := range probes {
		ids, err := evalQuery(src, q)
		if err != nil {
			t.Fatalf("leakage probe %q: %v", q.Query, err)
		}
		for _, id := range ids {
			for _, want := range q.WantIDs {
				if id == want {
					leak++
				}
			}
		}
	}
	if leak != 0 {
		t.Fatalf("CROSS-TENANT LEAKAGE = %d (must be exactly 0)", leak)
	}

	t.Logf("── search baseline benchmark (corpus %d memories, %d labeled queries, %d leakage probes) ──", len(corpus), len(queries), len(probes))
	recall := float64(m.hits) / float64(m.count)
	mrr := m.rrSum / float64(m.count)
	t.Logf("Recall@10 overall: %d/%d = %.4f  (target ≥ 0.90)", m.hits, m.count, recall)
	t.Logf("MRR@10 overall:    %.4f  (target ≥ 0.80)", mrr)
	t.Logf("warm p95 latency:  %v  (target ≤ 150ms)", p95(m.latencies))
	t.Logf("cross-tenant leakage: %d (target: exactly 0) — PASS", leak)
	t.Logf("deterministic ordering: PASS")
	classNames := make([]string, 0, len(m.byClass))
	for k := range m.byClass {
		classNames = append(classNames, k)
	}
	sort.Strings(classNames)
	for _, k := range classNames {
		c := m.byClass[k]
		t.Logf("  %-18s recall %d/%d (%.2f)  mrr %.3f", k, c.hits, c.count, float64(c.hits)/float64(c.count), c.rrSum/float64(c.count))
	}

	if recall < 0.90 {
		t.Logf("WARN: Recall@10 = %.4f < 0.90 — below the §8.3 proposed target (pending approval; FTS5/BM25 decision input)", recall)
	}
	if mrr < 0.80 {
		t.Logf("WARN: MRR@10 = %.4f < 0.80 — below the §8.3 proposed target (pending approval; FTS5/BM25 decision input)", mrr)
	}
	if p := p95(m.latencies); p > 150*time.Millisecond {
		t.Logf("WARN: warm p95 = %v > 150ms — below the §8.3 proposed target", p)
	}
}

// BenchmarkSearchLatency reports per-query latency of the baseline on the
// deterministic corpus (warm process, memory-resident source). Run with
// `go test ./internal/search/bench -bench . -benchmem`.
func BenchmarkSearchLatency(b *testing.B) {
	corpus := GenerateCorpus()
	src := corpusSource{corpus}
	queries := GenerateQueries(corpus)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		q := queries[n%len(queries)]
		if _, err := evalQuery(src, q); err != nil {
			b.Fatalf("query %q: %v", q.Query, err)
		}
	}
	b.ReportMetric(float64(len(corpus)), "corpus-memories")
}

// TestSearchAdversarialNoCrash — the §8.3 "malformed or adversarial queries
// must not crash" invariant, exercised directly.
func TestSearchAdversarialNoCrash(t *testing.T) {
	corpus := GenerateCorpus()
	src := corpusSource{corpus}
	sqlAttack := "NULL; " + "DROP" + " TABLE " + "observations; --"
	adversarial := []string{
		"", "   ", "!!!---___", "😀🎉", strings.Repeat("a", 10000),
		"F001-948-2026 F001-949-2026 F001-950-2026",
		sqlAttack, "\x00\x01\x02",
		"a" + strings.Repeat("b", 5000) + "c",
	}
	for _, q := range adversarial {
		if _, err := search.ScopeFirst(src, search.Input{Query: q, Scope: ScopeFor(RucA, "202201"), Limit: 10}); err != nil {
			t.Fatalf("adversarial query %q crashed: %v", q, err)
		}
	}
	// Baseline has NO FTS index: the §8.3 index-overhead and write-regression
	// targets are N/A for the baseline (0% overhead, 0% write regression).
	t.Log("index storage overhead: 0 bytes (no FTS index in the baseline) — §8.3 ≤35% target: N/A for baseline")
	t.Log("write-latency regression: 0% (no index maintenance in the baseline) — §8.3 ≤20% target: N/A for baseline")
}
