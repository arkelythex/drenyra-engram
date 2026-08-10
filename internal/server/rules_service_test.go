// Rule impact tests — Phase 6 v0.6.0, design §5/§6/§8 (acceptance items 4-5).
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// TestRuleImpactPureClassifier covers the half-open interval logic (design §5):
// a consuming POINT overlaps [b1, b2) iff b1 <= t < b2; a consuming WINDOW
// [a1, a2) overlaps iff a2 > b1 && a1 < b2; empty bounds are unbounded.
func TestRuleImpactPureClassifier(t *testing.T) {
	cases := []struct {
		name                       string
		dStart, dEnd, rStart, rEnd string
		want                       bool
	}{
		{"point inside window", "2026-02-15T00:00:00Z", "", "2026-01-01T00:00:00Z", "2026-03-31T23:59:59Z", true},
		{"point before window", "2026-01-01T00:00:00Z", "", "2026-04-01T00:00:00Z", "2026-12-31T00:00:00Z", false},
		{"point exactly at window start (half-open)", "2026-04-01T00:00:00Z", "", "2026-04-01T00:00:00Z", "2026-12-31T00:00:00Z", true},
		{"point exactly at window end (half-open)", "2026-12-31T00:00:00Z", "", "2026-04-01T00:00:00Z", "2026-12-31T00:00:00Z", false},
		{"window overlaps", "2026-02-01T00:00:00Z", "2026-05-01T00:00:00Z", "2026-04-01T00:00:00Z", "2026-12-31T00:00:00Z", true},
		{"window before", "2026-01-01T00:00:00Z", "2026-03-31T23:59:59Z", "2026-04-01T00:00:00Z", "2026-12-31T00:00:00Z", false},
		{"window ends exactly at start (half-open)", "2026-01-01T00:00:00Z", "2026-04-01T00:00:00Z", "2026-04-01T00:00:00Z", "2026-12-31T00:00:00Z", false},
		{"open-ended changed window (no expiry)", "2026-02-15T00:00:00Z", "", "2026-04-01T00:00:00Z", "", false},
		{"open-ended changed window inside", "2026-05-01T00:00:00Z", "", "2026-04-01T00:00:00Z", "", true},
		{"empty consuming start fails closed", "", "", "2026-01-01T00:00:00Z", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			item := core.ClassifyRuleImpactItem(core.RuleImpactItem{}, c.dStart, c.dEnd, c.rStart, c.rEnd)
			if item.OverlapsChangedWindow != c.want {
				t.Fatalf("overlap = %v, want %v", item.OverlapsChangedWindow, c.want)
			}
		})
	}
}

// TestRuleImpactEndToEnd is design §8 acceptance items 1-2, 4-6: rule v1 with a
// declared vigencia window, a decision pinning v1 at its EffectiveAt, rule v2
// superseding v1 on the same chain — then history shows both, show returns v2,
// impact against v1 overlaps the decision, impact against v2 (chain head) does
// not, and the resolved pin matches.
func TestRuleImpactEndToEnd(t *testing.T) {
	ctx := context.Background()
	api := closeAcceptanceStore(t)
	scope := ruleFixtureScope()

	// Rule v1: vigencia 2026-01-01 → 2026-03-31, jurisdiction PE.
	v1, err := api.Save(core.SaveInput{
		TopicKey: ruleRetentionV2, Title: "Retention rate v1", Kind: core.KindRule,
		Scope:        scope,
		Content:      core.Content{What: "Retention rate 3 percent from 2026-01", Why: "rule v1", Where: "fixture", Learned: "superseded by v2"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-01T00:00:00Z",
		Validity:   &core.Validity{EffectiveAt: "2026-01-01T00:00:00Z", ExpiresAt: "2026-03-31T23:59:59Z", Source: "declared"},
		PolicyRule: &core.PolicyRule{Jurisdiction: "PE", Legislation: "NATIONAL-TAX", Authority: "tax authority", Tags: []string{"retention"}},
		Source:     testAgentSource,
	})
	if err != nil {
		t.Fatalf("save rule v1: %v", err)
	}
	// Rule v2: same chain, later window — supersedes v1.
	v2, err := api.Save(core.SaveInput{
		TopicKey: ruleRetentionV2, Title: "Retention rate v2", Kind: core.KindRule,
		Scope:        scope,
		Content:      core.Content{What: "Retention rate 4 percent from 2026-04", Why: "rule v2 supersedes v1", Where: "fixture", Learned: "current"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-04-01T00:00:00Z",
		Validity:   &core.Validity{EffectiveAt: "2026-04-01T00:00:00Z", Source: "declared"},
		PolicyRule: &core.PolicyRule{Jurisdiction: "PE", Legislation: "NATIONAL-TAX", Authority: "tax authority", Tags: []string{"retention"}},
		Source:     testAgentSource,
	})
	if err != nil {
		t.Fatalf("save rule v2: %v", err)
	}

	// Decision pinning v1 at EffectiveAt 2026-02-15 (a journal entry, gated).
	decision, err := api.Save(core.SaveInput{
		TopicKey: "entry/4011/retention", Title: "Retencion febrero", Kind: core.KindDecision,
		Scope:        scope,
		Content:      core.Content{What: "Retencion febrero 2026", Why: "decision", Where: "fixture", Learned: "pins rule v1"},
		FiscalEffect: core.FiscalEffectJournalEntry, EffectiveAt: "2026-02-15T00:00:00Z",
		RuleLinks: []core.RuleLink{{Ref: ruleRetentionV2, Version: v1.Memory.Identity.ID, EffectiveAt: "2026-02-15T00:00:00Z"}},
		Source:    testAgentSource,
	})
	if err != nil {
		t.Fatalf("save decision with pinned rule: %v", err)
	}

	// history: v1 then v2; v1 superseded, v2 head.
	chain, err := api.RuleHistory(ruleRetentionV2, scope)
	if err != nil || len(chain) != 2 {
		t.Fatalf("rule history = %d revisions (%v), want 2", len(chain), err)
	}
	if chain[0].Status != core.StatusSuperseded || chain[1].Identity.ID != v2.Memory.Identity.ID {
		t.Fatalf("history order/status wrong: %+v", chain)
	}
	// show: chain head = v2.
	head, err := api.RuleShow(ruleRetentionV2, scope)
	if err != nil || head.Identity.ID != v2.Memory.Identity.ID {
		t.Fatalf("rule show = %s (%v), want v2", head.Identity.ID, err)
	}

	// impact against revision 1 (v1): the decision (2026-02-15) OVERLAPS v1's
	// window [2026-01-01, 2026-03-31).
	resV1, err := api.RuleImpact(ctx, fixtureOrg, ruleRetentionV2, &scope, 1)
	if err != nil {
		t.Fatalf("impact v1: %v", err)
	}
	if resV1.Revision != 1 || resV1.SelectedRevision != v1.Memory.Identity.ID {
		t.Fatalf("impact v1 selection = rev %d id %s, want 1 / %s", resV1.Revision, resV1.SelectedRevision, v1.Memory.Identity.ID)
	}
	if len(resV1.Items) != 1 {
		t.Fatalf("impact v1 items = %d, want 1", len(resV1.Items))
	}
	it := resV1.Items[0]
	if it.MemoryID != decision.Memory.Identity.ID || !it.OverlapsChangedWindow {
		t.Fatalf("impact v1 item = %+v, want the decision overlapping", it)
	}
	if it.ResolvedVersion != v1.Memory.Identity.ID || it.Outcome != core.RuleImpactResolved {
		t.Fatalf("impact v1 pin = %+v, want resolved to v1", it)
	}

	// impact against the chain head (v2): the decision at 2026-02-15 does NOT
	// overlap v2's window [2026-04-01, ∞).
	resV2, err := api.RuleImpact(ctx, fixtureOrg, ruleRetentionV2, &scope, 0)
	if err != nil {
		t.Fatalf("impact head: %v", err)
	}
	if resV2.SelectedRevision != v2.Memory.Identity.ID {
		t.Fatalf("impact head selection = %s, want v2", resV2.SelectedRevision)
	}
	if resV2.Items[0].OverlapsChangedWindow {
		t.Fatalf("impact head must NOT overlap: %+v", resV2.Items[0])
	}
}

// TestRuleImpactChainAmbiguous — the design's RULE_CHAIN_AMBIGUOUS: the same
// topic key pinned in TWO different exact scopes fails without a scope selector
// instead of merging unrelated chains.
func TestRuleImpactChainAmbiguous(t *testing.T) {
	ctx := context.Background()
	api := closeAcceptanceStore(t)
	scopeA := ruleFixtureScope()

	otherRUC := "20600995804"
	scopeB := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: fixtureOrg, CompanyID: "cmp_02", RUC: otherRUC, Period: fixturePeriod}

	// Same topic, two scopes, each with a rule + a pinned decision.
	ruleA, err := api.Save(core.SaveInput{
		TopicKey: "policy/ambiguous/shared", Title: "rule A", Kind: core.KindRule,
		Scope: scopeA, Content: core.Content{What: "A", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-01T00:00:00Z",
		Source: testAgentSource,
	})
	if err != nil {
		t.Fatalf("save rule A: %v", err)
	}
	ruleB, err := api.Save(core.SaveInput{
		TopicKey: "policy/ambiguous/shared", Title: "rule B", Kind: core.KindRule,
		Scope: scopeB, Content: core.Content{What: "B", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-01T00:00:00Z",
		Source: testAgentSource,
	})
	if err != nil {
		t.Fatalf("save rule B: %v", err)
	}
	decA, err := api.Save(core.SaveInput{
		TopicKey: "entry/A", Title: "dec A", Kind: core.KindDecision,
		Scope: scopeA, Content: core.Content{What: "da", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectJournalEntry, EffectiveAt: "2026-01-10T00:00:00Z",
		RuleLinks: []core.RuleLink{{Ref: "policy/ambiguous/shared", Version: ruleA.Memory.Identity.ID, EffectiveAt: "2026-01-10T00:00:00Z"}},
		Source:    testAgentSource,
	})
	if err != nil {
		t.Fatalf("save dec A: %v", err)
	}
	if _, err := api.Save(core.SaveInput{
		TopicKey: "entry/B", Title: "dec B", Kind: core.KindDecision,
		Scope: scopeB, Content: core.Content{What: "db", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectJournalEntry, EffectiveAt: "2026-01-10T00:00:00Z",
		RuleLinks: []core.RuleLink{{Ref: "policy/ambiguous/shared", Version: ruleB.Memory.Identity.ID, EffectiveAt: "2026-01-10T00:00:00Z"}},
		Source:    testAgentSource,
	}); err != nil {
		t.Fatalf("save dec B: %v", err)
	}
	// Without a scope selector → RULE_CHAIN_AMBIGUOUS.
	_, err = api.RuleImpact(ctx, fixtureOrg, "policy/ambiguous/shared", nil, 0)
	if err == nil || !strings.Contains(err.Error(), "RULE_CHAIN_AMBIGUOUS") {
		t.Fatalf("impact without scope = %v, want RULE_CHAIN_AMBIGUOUS", err)
	}
	// With a scope selector the same call works.
	res, err := api.RuleImpact(ctx, fixtureOrg, "policy/ambiguous/shared", &scopeA, 0)
	if err != nil || len(res.Items) != 1 || res.Items[0].MemoryID != decA.Memory.Identity.ID {
		t.Fatalf("impact scoped = %+v (%v), want only A's decision", res, err)
	}
}

// TestRuleImpactLegacyUnpinned — a decision with a BARE ruleRef (no structured
// link) is included as legacy-unpinned, never silently dropped.
func TestRuleImpactLegacyUnpinned(t *testing.T) {
	ctx := context.Background()
	api := closeAcceptanceStore(t)
	scope := ruleFixtureScope()

	if _, err := api.Save(core.SaveInput{
		TopicKey: ruleRetentionV2, Title: "rule", Kind: core.KindRule,
		Scope: scope, Content: core.Content{What: "r", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-01T00:00:00Z",
		Source: testAgentSource,
	}); err != nil {
		t.Fatalf("save rule: %v", err)
	}
	if _, err := api.Save(core.SaveInput{
		TopicKey: "entry/legacy", Title: "legacy", Kind: core.KindDecision,
		Scope: scope, Content: core.Content{What: "d", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectJournalEntry, EffectiveAt: "2026-02-01T00:00:00Z",
		RuleRefs: []string{ruleRetentionV2},
		Source:   testAgentSource,
	}); err != nil {
		t.Fatalf("save legacy decision: %v", err)
	}
	res, err := api.RuleImpact(ctx, fixtureOrg, ruleRetentionV2, &scope, 0)
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Outcome != core.RuleImpactLegacyUnpinned || res.UnresolvedLegacy != 1 {
		t.Fatalf("legacy impact = %+v (unresolved=%d), want one legacy-unpinned item", res.Items, res.UnresolvedLegacy)
	}
}
