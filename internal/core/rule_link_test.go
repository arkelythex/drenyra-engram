// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.6.0 RuleLink DTO
// (docs/architecture/fiscal-policy-memory-v0.6.md §2.2): the validation matrix
// (non-empty ref/version, RFC3339 effectiveAt), the dedupe/conflict discipline
// (identical links collapse, different links for the same ref fail
// RULE_LINK_VERSION_CONFLICT) and the bare-ref derivation used by the store
// before hashing.
package core_test

import (
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// sampleRuleLink returns a deterministic well-formed structured link.
func sampleRuleLink() core.RuleLink {
	return core.RuleLink{
		Ref:         "policy/indirect-tax/late-document",
		Version:     "rule-v1-id",
		EffectiveAt: "2026-07-31T12:00:00Z",
	}
}

// TestAssertValidRuleLinkMatrix covers the malformed-link matrix: every
// well-formed link passes, every malformed shape fails closed with
// INVALID_RULE_LINK.
func TestAssertValidRuleLinkMatrix(t *testing.T) {
	good := sampleRuleLink()
	if err := core.AssertValidRuleLink(good); err != nil {
		t.Fatalf("well-formed link must pass: %v", err)
	}
	// RFC3339 with fractional seconds is accepted.
	if err := core.AssertValidRuleLink(core.RuleLink{
		Ref: "policy/ref", Version: "rule-v2-id", EffectiveAt: "2026-07-31T12:00:00.500Z",
	}); err != nil {
		t.Fatalf("RFC3339Nano effectiveAt must pass: %v", err)
	}
	for name, link := range map[string]core.RuleLink{
		"empty ref":           {Ref: "", Version: "rule-v1-id", EffectiveAt: "2026-07-31T12:00:00Z"},
		"blank ref":           {Ref: "   ", Version: "rule-v1-id", EffectiveAt: "2026-07-31T12:00:00Z"},
		"empty version":       {Ref: "policy/ref", Version: "", EffectiveAt: "2026-07-31T12:00:00Z"},
		"blank version":       {Ref: "policy/ref", Version: " \t", EffectiveAt: "2026-07-31T12:00:00Z"},
		"date-only effective": {Ref: "policy/ref", Version: "rule-v1-id", EffectiveAt: "2026-07-31"},
		"lenient layout":      {Ref: "policy/ref", Version: "rule-v1-id", EffectiveAt: "2026-07-31 12:00:00"},
		"garbage effective":   {Ref: "policy/ref", Version: "rule-v1-id", EffectiveAt: "not-a-time"},
		"empty effective":     {Ref: "policy/ref", Version: "rule-v1-id", EffectiveAt: ""},
	} {
		if err := core.AssertValidRuleLink(link); err == nil || !strings.Contains(err.Error(), "INVALID_RULE_LINK") {
			t.Fatalf("%s: err = %v, want INVALID_RULE_LINK", name, err)
		}
	}
}

// TestAssertValidRuleLinksDedupeAndConflict freezes the list discipline:
// identical links dedupe to one entry; two different links for the same ref
// fail RULE_LINK_VERSION_CONFLICT (metadata is never updated in place).
func TestAssertValidRuleLinksDedupeAndConflict(t *testing.T) {
	link := sampleRuleLink()
	deduped, err := core.AssertValidRuleLinks([]core.RuleLink{link, link})
	if err != nil {
		t.Fatalf("identical links must dedupe, got %v", err)
	}
	if len(deduped) != 1 {
		t.Fatalf("deduped = %v, want exactly one entry", deduped)
	}
	// A different version for the same ref is a conflict.
	if _, err := core.AssertValidRuleLinks([]core.RuleLink{
		link, {Ref: link.Ref, Version: "rule-v2-id", EffectiveAt: link.EffectiveAt},
	}); err == nil || !strings.Contains(err.Error(), "RULE_LINK_VERSION_CONFLICT") {
		t.Fatalf("different version = %v, want RULE_LINK_VERSION_CONFLICT", err)
	}
	// A different date for the same ref is a conflict too.
	if _, err := core.AssertValidRuleLinks([]core.RuleLink{
		link, {Ref: link.Ref, Version: link.Version, EffectiveAt: "2026-08-15T00:00:00Z"},
	}); err == nil || !strings.Contains(err.Error(), "RULE_LINK_VERSION_CONFLICT") {
		t.Fatalf("different date = %v, want RULE_LINK_VERSION_CONFLICT", err)
	}
	// A nil/empty list is valid and canonicalizes to an empty non-nil slice.
	empty, err := core.AssertValidRuleLinks(nil)
	if err != nil {
		t.Fatalf("nil list must pass: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("nil list must canonicalize to an empty slice, got %#v", empty)
	}
	// A malformed member fails the whole list.
	if _, err := core.AssertValidRuleLinks([]core.RuleLink{link, {Ref: "", Version: "x", EffectiveAt: "2026-07-31T12:00:00Z"}}); err == nil {
		t.Fatal("a malformed member must fail the whole list")
	}
}

// TestDeriveRuleRefs freezes the bare-ref derivation: the store merges
// ruleLinks[].ref into the existing refs (deduped, stable order) BEFORE
// hashing, so the canonical envelope carries every structured ref.
func TestDeriveRuleRefs(t *testing.T) {
	derived := core.DeriveRuleRefs(
		[]string{"policy/existing", "policy/existing"},
		[]core.RuleLink{
			{Ref: "policy/pinned-a", Version: "rule-v1", EffectiveAt: "2026-07-31T12:00:00Z"},
			{Ref: "policy/existing", Version: "rule-v2", EffectiveAt: "2026-07-31T12:00:00Z"},
			{Ref: "policy/pinned-b", Version: "rule-v3", EffectiveAt: "2026-07-31T12:00:00Z"},
		},
	)
	want := []string{"policy/existing", "policy/pinned-a", "policy/pinned-b"}
	if len(derived) != len(want) {
		t.Fatalf("derived = %v, want %v", derived, want)
	}
	for i := range want {
		if derived[i] != want[i] {
			t.Fatalf("derived = %v, want %v", derived, want)
		}
	}
	// No links → the existing refs pass through untouched.
	if got := core.DeriveRuleRefs([]string{"policy/existing"}, nil); len(got) != 1 || got[0] != "policy/existing" {
		t.Fatalf("no links must pass through the existing refs, got %v", got)
	}
	// Clone is defensive: mutating the copy never touches the source.
	clone := core.CloneRuleLinks([]core.RuleLink{sampleRuleLink()})
	clone[0].Version = "mutated"
	if sampleRuleLink().Version != "rule-v1-id" {
		t.Fatal("cloning must be defensive")
	}
}
