// Rule version resolution + verification layer tests — Phase 6 v0.6.0,
// design §4 (Batch 4).
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// TestResolveRuleVersionFromChainPure covers design §4.1 steps 4-6: sole
// revision in force, RULE_NOT_IN_FORCE, RULE_VIGENCIA_OVERLAP, and
// RULE_VERSION_MISMATCH — all PURE, no store access.
func TestResolveRuleVersionFromChainPure(t *testing.T) {
	scope := ruleFixtureScope()
	mkRule := func(id, topic, eff, exp string) core.AccountingMemory {
		m := core.AccountingMemory{Identity: core.Identity{ID: id, TopicKey: topic}, Scope: scope, Kind: core.KindRule, EffectiveAt: eff}
		if exp != "" {
			m.Validity = &core.Validity{EffectiveAt: eff, ExpiresAt: exp, Source: "declared"}
		}
		return m
	}
	v1 := mkRule("rule-v1", "policy/t", "2026-01-01T00:00:00Z", "2026-03-31T23:59:59Z")
	v2 := mkRule("rule-v2", "policy/t", "2026-04-01T00:00:00Z", "")

	cases := []struct {
		name    string
		chain   []core.AccountingMemory
		pinned  string
		instant string
		wantID  string
		wantErr string
	}{
		{"resolved to v1", []core.AccountingMemory{v1, v2}, "rule-v1", "2026-02-15T00:00:00Z", "rule-v1", ""},
		{"resolved to v2 (open-ended)", []core.AccountingMemory{v1, v2}, "rule-v2", "2026-06-01T00:00:00Z", "rule-v2", ""},
		{"not in force (before any window)", []core.AccountingMemory{v1, v2}, "rule-v1", "2020-01-01T00:00:00Z", "", core.RuleVersionNotInForce},
		{"adjacent windows: v2 in force at gap (pinned v1 → mismatch)", []core.AccountingMemory{v1, v2}, "rule-v1", "2026-04-15T00:00:00Z", "", core.RuleVersionMismatch},
		{"overlap (two windows)", []core.AccountingMemory{
			mkRule("a", "policy/t", "2026-01-01T00:00:00Z", "2026-12-31T00:00:00Z"),
			mkRule("b", "policy/t", "2026-06-01T00:00:00Z", "2027-01-01T00:00:00Z"),
		}, "a", "2026-07-01T00:00:00Z", "", core.RuleVersionOverlap},
		{"mismatch (v1 in force but pinned v2)", []core.AccountingMemory{v1, v2}, "rule-v2", "2026-02-15T00:00:00Z", "", core.RuleVersionMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := core.ResolveRuleVersionFromChain(c.chain, c.pinned, c.instant)
			if c.wantErr != "" {
				if core.RuleVersionCode(err) != c.wantErr {
					t.Fatalf("err = %v, want code %s", err, c.wantErr)
				}
				return
			}
			if err != nil || got.Identity.ID != c.wantID {
				t.Fatalf("resolved = %+v (%v), want %s", got.Identity.ID, err, c.wantID)
			}
		})
	}
}

// TestVerifyMemoryRuleVersionTrace — design §8 acceptance item 6: verify memory
// resolves the decision to v1, matches the pin, and emits a PASSING trace with
// the "rule version/vigencia" layer.
func TestVerifyMemoryRuleVersionTrace(t *testing.T) {
	ctx := context.Background()
	api := closeAcceptanceStore(t)
	scope := ruleFixtureScope()

	v1, err := api.Save(core.SaveInput{
		TopicKey: ruleRetentionV2, Title: "Retention rate v1", Kind: core.KindRule,
		Scope: scope, Content: core.Content{What: "r1", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-01T00:00:00Z",
		Validity:   &core.Validity{EffectiveAt: "2026-01-01T00:00:00Z", ExpiresAt: "2026-03-31T23:59:59Z", Source: "declared"},
		PolicyRule: &core.PolicyRule{Jurisdiction: "PE", Legislation: "NATIONAL-TAX", Authority: "tax", Tags: []string{"r"}},
		Source:     testAgentSource,
		Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("save rule v1: %v", err)
	}
	if _, err := api.Save(core.SaveInput{
		TopicKey: ruleRetentionV2, Title: "Retention rate v2", Kind: core.KindRule,
		Scope: scope, Content: core.Content{What: "r2", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-04-01T00:00:00Z",
		Validity:   &core.Validity{EffectiveAt: "2026-04-01T00:00:00Z", Source: "declared"},
		Source:     testAgentSource,
		Confidence: 0.8,
	}); err != nil {
		t.Fatalf("save rule v2: %v", err)
	}
	decision, err := api.Save(core.SaveInput{
		TopicKey: "entry/4011/ret", Title: "dec", Kind: core.KindDecision,
		Scope: scope, Content: core.Content{What: "d", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectJournalEntry, EffectiveAt: "2026-02-15T00:00:00Z",
		RuleLinks:  []core.RuleLink{{Ref: ruleRetentionV2, Version: v1.Memory.Identity.ID, EffectiveAt: "2026-02-15T00:00:00Z"}},
		Source:     testAgentSource,
		Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("save decision: %v", err)
	}

	report, err := VerifyMemory(ctx, api.Store.(*store.SQLiteStore), decision.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("verify memory: %v", err)
	}
	if report.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("outcome = %q, want passed: %+v", report.Outcome, report.Layers)
	}
	if len(report.RuleVersions) != 1 {
		t.Fatalf("ruleVersions = %+v, want 1 trace", report.RuleVersions)
	}
	tr := report.RuleVersions[0]
	if tr.Outcome != core.RuleVersionResolved || tr.ResolvedVersion != v1.Memory.Identity.ID || tr.Revision != 1 {
		t.Fatalf("trace = %+v, want resolved to v1 revision 1", tr)
	}
	if tr.Jurisdiction != "PE" || tr.StatusAsOf != string(core.StatusActive) {
		t.Fatalf("trace jurisdiction/status = %q/%q, want PE/active", tr.Jurisdiction, tr.StatusAsOf)
	}
	if report.AccountingCorrectness != core.AccountingCorrectnessNotAsserted {
		t.Fatalf("accountingCorrectness = %q, want NOT ASSERTED", report.AccountingCorrectness)
	}
	// The layer is present and passed.
	found := false
	for _, l := range report.Layers {
		if l.Name == core.LayerRuleVersionVigencia {
			found = true
			if l.Status != core.VerificationPassed {
				t.Fatalf("rule version layer status = %q, want passed", l.Status)
			}
		}
	}
	if !found {
		t.Fatalf("missing rule version/vigencia layer: %+v", report.Layers)
	}
}

// TestVerifyMemoryRuleVersionLegacySkipped — a decision with a BARE rule ref
// (no structured link) yields a SKIPPED trace, and the layer still passes.
func TestVerifyMemoryRuleVersionLegacySkipped(t *testing.T) {
	ctx := context.Background()
	api := closeAcceptanceStore(t)
	scope := ruleFixtureScope()

	if _, err := api.Save(core.SaveInput{
		TopicKey: ruleRetentionV2, Title: "rule", Kind: core.KindRule,
		Scope: scope, Content: core.Content{What: "r", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-01T00:00:00Z",
		Source:     testAgentSource,
		Confidence: 0.8,
	}); err != nil {
		t.Fatalf("save rule: %v", err)
	}
	decision, err := api.Save(core.SaveInput{
		TopicKey: "entry/legacy", Title: "legacy", Kind: core.KindDecision,
		Scope: scope, Content: core.Content{What: "d", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectJournalEntry, EffectiveAt: "2026-02-01T00:00:00Z",
		RuleRefs:   []string{ruleRetentionV2},
		Source:     testAgentSource,
		Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("save legacy decision: %v", err)
	}
	report, err := VerifyMemory(ctx, api.Store.(*store.SQLiteStore), decision.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("verify memory: %v", err)
	}
	if len(report.RuleVersions) != 1 || report.RuleVersions[0].Outcome != core.RuleVersionLegacySkipped {
		t.Fatalf("ruleVersions = %+v, want one SKIPPED trace", report.RuleVersions)
	}
	if report.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("outcome = %q, want passed (skipped is not a failure)", report.Outcome)
	}
}

// TestVerifyMemoryRuleVersionInvalidPin — a structured pin to a revision NOT in
// force at the decision instant fails the layer (RULE_NOT_IN_FORCE trace).
func TestVerifyMemoryRuleVersionInvalidPin(t *testing.T) {
	ctx := context.Background()
	api := closeAcceptanceStore(t)
	scope := ruleFixtureScope()

	v1, err := api.Save(core.SaveInput{
		TopicKey: ruleRetentionV2, Title: "rule v1", Kind: core.KindRule,
		Scope: scope, Content: core.Content{What: "r1", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-01T00:00:00Z",
		Validity:   &core.Validity{EffectiveAt: "2026-01-01T00:00:00Z", ExpiresAt: "2026-03-31T23:59:59Z", Source: "declared"},
		Source:     testAgentSource,
		Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("save rule v1: %v", err)
	}
	// Decision on 2026-05-15 pins v1 whose window expired 2026-03-31.
	decision, err := api.Save(core.SaveInput{
		TopicKey: "entry/late-pin", Title: "late pin", Kind: core.KindDecision,
		Scope: scope, Content: core.Content{What: "d", Why: "x", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectJournalEntry, EffectiveAt: "2026-05-15T00:00:00Z",
		RuleLinks:  []core.RuleLink{{Ref: ruleRetentionV2, Version: v1.Memory.Identity.ID, EffectiveAt: "2026-05-15T00:00:00Z"}},
		Source:     testAgentSource,
		Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("save decision: %v", err)
	}
	report, err := VerifyMemory(ctx, api.Store.(*store.SQLiteStore), decision.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("verify memory: %v", err)
	}
	if report.Outcome != core.VerificationOutcomeFailed {
		t.Fatalf("outcome = %q, want failed (pinned revision not in force)", report.Outcome)
	}
	if len(report.RuleVersions) != 1 || !strings.HasPrefix(report.RuleVersions[0].Outcome, "RULE_") {
		t.Fatalf("ruleVersions = %+v, want a failing trace", report.RuleVersions)
	}
}
