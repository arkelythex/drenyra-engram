// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module searches observations whose
// content is structured text; there are no monetary fields, so no money value
// is scored or asserted here.

package search

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

const (
	testOrg     = "org-001"
	testRucA    = "20100039201"
	testRucB    = "20600995804"
	testPeriod  = "202401"
	testTimeStr = "2026-01-15T12:00:00.000Z"
)

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

func companyScope(ruc string) core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: testOrg,
		CompanyID:      "acme-" + ruc,
		RUC:            ruc,
		Period:         testPeriod,
	}
}

var testAgentSource = core.Source{
	System:    "go-test",
	ActorID:   "test-agent",
	ActorKind: core.ActorKindAgent,
}

var igvContent = core.Content{
	What:    "IGV base rate is 18 percent",
	Why:     "standard rate for goods",
	Where:   "Peru",
	Learned: "applies to all invoices",
}

var institutionalContent = core.Content{
	What:    "Banking rules apply to all clients",
	Why:     "cross-company convention",
	Where:   "Peru",
	Learned: "institutional knowledge",
}

func save(t *testing.T, s *store.SQLiteStore, input core.SaveInput) {
	t.Helper()
	if _, err := s.Save(input); err != nil {
		t.Fatalf("save: %v", err)
	}
}

// TestCompanyAObservationNeverVisibleFromCompanyB is THE MANDATORY cross-tenant
// isolation conformance test (contracts/scope.md frozen semantics #3): a
// company-A observation — identical text, identical topicKey, differing only
// by scope — is NEVER retrievable from a company-B query, under any match mode.
func TestCompanyAObservationNeverVisibleFromCompanyB(t *testing.T) {
	s := newTestStore(t)

	// Identical text, identical topicKey — differing only by scope (ruc).
	save(t, s, core.SaveInput{
		TopicKey: "tax.igv.rate", Title: "IGV base rate", Kind: core.KindRule,
		Scope: companyScope(testRucA), Content: igvContent, Source: testAgentSource,
		FiscalEffect: core.FiscalEffectNone,
		Confidence:   0.8,
	})
	save(t, s, core.SaveInput{
		TopicKey: "tax.igv.rate", Title: "IGV base rate", Kind: core.KindRule,
		Scope: companyScope(testRucB), Content: igvContent, Source: testAgentSource,
		FiscalEffect: core.FiscalEffectNone,
		Confidence:   0.8,
	})

	// From company B: exactly one result, and it is B's own observation.
	fromB, err := ScopeFirst(s, Input{Query: "IGV base rate", Scope: companyScope(testRucB), MatchMode: MatchAny})
	if err != nil {
		t.Fatalf("search from B: %v", err)
	}
	if len(fromB) != 1 {
		t.Fatalf("search from B returned %d results, want exactly 1 (its own)", len(fromB))
	}
	if fromB[0].Memory.Scope.RUC != testRucB {
		t.Fatalf("result from B scoped to %s, want %s", fromB[0].Memory.Scope.RUC, testRucB)
	}
	for _, result := range fromB {
		if result.Memory.Scope.RUC == testRucA {
			t.Fatal("LEAK: company-A observation surfaced in a company-B query")
		}
	}

	// From company A: exactly one result, and it is A's own observation.
	fromA, err := ScopeFirst(s, Input{Query: "IGV base rate", Scope: companyScope(testRucA), MatchMode: MatchAny})
	if err != nil {
		t.Fatalf("search from A: %v", err)
	}
	if len(fromA) != 1 {
		t.Fatalf("search from A returned %d results, want exactly 1 (its own)", len(fromA))
	}
	if fromA[0].Memory.Scope.RUC != testRucA {
		t.Fatalf("result from A scoped to %s, want %s", fromA[0].Memory.Scope.RUC, testRucA)
	}
	for _, result := range fromA {
		if result.Memory.Scope.RUC == testRucB {
			t.Fatal("LEAK: company-B observation surfaced in a company-A query")
		}
	}
}

func TestInstitutionalOnlyOnExplicitIntent(t *testing.T) {
	s := newTestStore(t)

	save(t, s, core.SaveInput{
		TopicKey: "tax.igv.rate", Title: "IGV base rate", Kind: core.KindRule,
		Scope: companyScope(testRucA), Content: igvContent, Source: testAgentSource,
		FiscalEffect: core.FiscalEffectNone,
		Confidence:   0.8,
	})
	save(t, s, core.SaveInput{
		TopicKey: "policy.banking-rules", Title: "Banking rules", Kind: core.KindRule,
		Scope: core.Scope{Kind: core.ScopeKindInstitutional}, Content: institutionalContent, Source: testAgentSource,
		FiscalEffect: core.FiscalEffectNone,
		Confidence:   0.8,
	})

	// Plain company-A query: institutional observation must NOT appear.
	plainA, err := ScopeFirst(s, Input{Query: "banking rules", Scope: companyScope(testRucA), MatchMode: MatchAny})
	if err != nil {
		t.Fatalf("plain company search: %v", err)
	}
	if len(plainA) != 0 {
		t.Fatalf("plain company query surfaced %d results, want 0 (no institutional leak)", len(plainA))
	}

	// Explicit opt-in: institutional observation appears alongside A's own.
	explicitA, err := ScopeFirst(s, Input{Query: "banking rules", Scope: companyScope(testRucA), MatchMode: MatchAny, IncludeInstitutional: true})
	if err != nil {
		t.Fatalf("opt-in company search: %v", err)
	}
	if !containsInstitutional(explicitA) {
		t.Fatalf("opt-in query must surface the institutional observation: %+v", explicitA)
	}

	// Institutional query scope: institutional observation returned, and A's
	// scoped observation never leaks into it.
	institutionalQuery, err := ScopeFirst(s, Input{Query: "banking rules", Scope: core.Scope{Kind: core.ScopeKindInstitutional}, MatchMode: MatchAny})
	if err != nil {
		t.Fatalf("institutional search: %v", err)
	}
	if len(institutionalQuery) != 1 {
		t.Fatalf("institutional query returned %d results, want exactly 1", len(institutionalQuery))
	}
	if institutionalQuery[0].Memory.Scope.Kind != core.ScopeKindInstitutional {
		t.Fatalf("institutional query surfaced a company-scoped observation: %+v", institutionalQuery[0].Memory.Scope)
	}
	for _, result := range institutionalQuery {
		if result.Memory.Scope.Kind == core.ScopeKindCompany {
			t.Fatal("LEAK: company observation surfaced in an institutional query")
		}
	}
}

func containsInstitutional(results []Result) bool {
	for _, result := range results {
		if result.Memory.Scope.Kind == core.ScopeKindInstitutional {
			return true
		}
	}
	return false
}

func TestMatchModeAllRequiresEveryToken(t *testing.T) {
	s := newTestStore(t)
	save(t, s, core.SaveInput{
		TopicKey: "tax.igv.rate", Title: "IGV base rate", Kind: core.KindRule,
		Scope: companyScope(testRucB), Content: igvContent, Source: testAgentSource,
		FiscalEffect: core.FiscalEffectNone,
		Confidence:   0.8,
	})

	// "payroll" is not in B's observation: all rejects, any accepts.
	allMode, err := ScopeFirst(s, Input{Query: "payroll igv rate", Scope: companyScope(testRucB), MatchMode: MatchAll})
	if err != nil {
		t.Fatalf("all-mode search: %v", err)
	}
	if len(allMode) != 0 {
		t.Fatalf("all-mode returned %d results, want 0", len(allMode))
	}

	anyMode, err := ScopeFirst(s, Input{Query: "payroll igv rate", Scope: companyScope(testRucB), MatchMode: MatchAny})
	if err != nil {
		t.Fatalf("any-mode search: %v", err)
	}
	if len(anyMode) != 1 || anyMode[0].Score <= 0 {
		t.Fatalf("any-mode results = %+v, want 1 hit with score > 0", anyMode)
	}
}

func TestSearchUsesOnlyLatestRevisionPerChain(t *testing.T) {
	s := newTestStore(t)
	save(t, s, core.SaveInput{
		TopicKey: "tax.igv.rate", Title: "IGV base rate", Kind: core.KindRule,
		Scope: companyScope(testRucA), Content: igvContent, Source: testAgentSource,
		FiscalEffect: core.FiscalEffectNone,
		Confidence:   0.8,
	})
	save(t, s, core.SaveInput{
		TopicKey: "tax.igv.rate", Title: "IGV base rate (updated)", Kind: core.KindRule,
		Scope:        companyScope(testRucA),
		Content:      core.Content{What: "IGV base rate is 18 percent since 2011", Why: "standard rate for goods", Where: "Peru", Learned: "applies to all invoices"},
		FiscalEffect: core.FiscalEffectNone,
		Source:       testAgentSource,
		Confidence:   0.8,
	})

	results, err := ScopeFirst(s, Input{Query: "igv base rate", Scope: companyScope(testRucA), MatchMode: MatchAny})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("search returned %d results, want exactly 1 (latest per chain)", len(results))
	}
	if results[0].Memory.Revision != 2 {
		t.Fatalf("result revision = %d, want 2 (latest)", results[0].Memory.Revision)
	}
}

func TestStaleFlagOnExpiredObservation(t *testing.T) {
	s := newTestStore(t)
	save(t, s, core.SaveInput{
		TopicKey: "tax.igv.expired-rule", Title: "Old IGV rule", Kind: core.KindRule,
		Scope: companyScope(testRucA), Content: igvContent,
		FiscalEffect: core.FiscalEffectNone,
		Validity:     &core.Validity{ExpiresAt: "2000-01-01T00:00:00.000Z"},
		Source:       testAgentSource,
		Confidence:   0.8,
	})

	results, err := ScopeFirst(s, Input{Query: "old igv", Scope: companyScope(testRucA), MatchMode: MatchAny})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("search returned %d results, want 1", len(results))
	}
	if !results[0].Stale {
		t.Fatal("expired observation must surface as stale")
	}
}

func TestNoStaleFlagForValidOrWindowlessObservations(t *testing.T) {
	s := newTestStore(t)
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	save(t, s, core.SaveInput{
		TopicKey: "tax.igv.valid-rule", Title: "IGV current rule", Kind: core.KindRule,
		Scope: companyScope(testRucA), Content: igvContent,
		FiscalEffect: core.FiscalEffectNone,
		Validity:     &core.Validity{ExpiresAt: future},
		Source:       testAgentSource,
		Confidence:   0.8,
	})
	save(t, s, core.SaveInput{
		TopicKey: "tax.igv.no-window", Title: "IGV rule without window", Kind: core.KindRule,
		Scope: companyScope(testRucA), Content: igvContent, Source: testAgentSource,
		FiscalEffect: core.FiscalEffectNone,
		Confidence:   0.8,
	})

	results, err := ScopeFirst(s, Input{Query: "igv rule", Scope: companyScope(testRucA), MatchMode: MatchAny})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("search returned %d results, want 2", len(results))
	}
	for _, result := range results {
		if result.Stale {
			t.Fatalf("observation %s wrongly flagged stale", result.Memory.Identity.ID)
		}
	}
}
