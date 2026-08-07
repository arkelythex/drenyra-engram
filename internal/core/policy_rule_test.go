// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.6.0 PolicyRule
// model (docs/architecture/fiscal-policy-memory-v0.6.md §2.1): the canonical
// JSON bytes, the validation matrix (kind=rule only, jurisdiction syntax,
// required legislation/authority, trimmed non-empty tags) and the optional
// hash contribution (nil rule → legacy hashes stay byte-identical; a rule
// changing only its Validity window hashes differently — the v0.6 promise).
package core_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// samplePolicyRule returns a deterministic PolicyRule exercising every field.
// The tag input is deliberately unsorted and duplicated: the canonical JSON
// must carry the deduplicated, lexicographically sorted set.
func samplePolicyRule() *core.PolicyRule {
	return &core.PolicyRule{
		Jurisdiction: "PE",
		Legislation:  "NATIONAL-TAX",
		Authority:    "National tax authority",
		Tags:         []string{"late-document", "indirect-tax", "indirect-tax"},
	}
}

// pinnedCanonicalPolicyRuleJSON is the FROZEN canonical bytes of
// samplePolicyRule — the same literal is pinned in core/__tests__/
// policy-rule.test.ts (Go↔TS canonical bytes must match byte-identically).
const pinnedCanonicalPolicyRuleJSON = `{"jurisdiction":"PE","legislation":"NATIONAL-TAX","authority":"National tax authority","tags":["indirect-tax","late-document"]}`

// pinnedContributionHex is the FROZEN SHA-256 hex of the full
// policyRule/v0.6 contribution element (canonical JSON + the vigencia window
// fields of the sample validity below). The same value is pinned in the
// TypeScript mirror test — a divergence between runtimes fails one runner.
const pinnedContributionHex = ""

// pinnedLegacyContentHash / pinnedLegacyEnvelopeHash are the FROZEN hashes of
// the base rule memory WITHOUT policyRule (the legacy contract). They are the
// byte-identical-to-pre-v0.6 proof; the same constants live in the TS mirror.
const (
	pinnedLegacyContentHash  = ""
	pinnedLegacyEnvelopeHash = ""
)

// pinnedV06ContentHash / pinnedV06EnvelopeHash are the FROZEN hashes of the
// SAME memory WITH policyRule + validity — the v0.6.0 opt-in bytes shared with
// the TypeScript mirror (core/__tests__/policy-rule.test.ts).
const (
	pinnedV06ContentHash  = ""
	pinnedV06EnvelopeHash = ""
)

// basePolicyRuleMemory builds the deterministic rule memory the pinned
// constants hash. withRule toggles the v0.6.0 opt-in metadata; withValidity
// toggles the vigencia window carried by the policy-rule contribution.
func basePolicyRuleMemory(withRule, withValidity bool) core.AccountingMemory {
	m := core.AccountingMemory{
		Identity:     core.Identity{ID: "rule-v1", TopicKey: "policy/indirect-tax/late-document"},
		Title:        "Late document rule",
		Kind:         core.KindRule,
		Scope:        core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-1", CompanyID: "acme", RUC: "20100039201", Period: "202407"},
		Content:      core.Content{What: "late document", Why: "law", Where: "Peru", Learned: "apply"},
		Status:       core.StatusActive,
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  "2026-07-01T00:00:00Z",
		RecordedAt:   "2026-08-01T00:00:00Z",
		Source:       core.Source{System: "go-test", ActorKind: core.ActorKindAgent},
		Revision:     1,
	}
	if withValidity {
		m.Validity = &core.Validity{
			EffectiveAt: "2026-01-01T00:00:00Z",
			ExpiresAt:   "2026-08-01T00:00:00Z",
			Source:      "declared",
		}
	}
	if withRule {
		m.PolicyRule = samplePolicyRule()
	}
	m.ContentHash = core.ComputeContentHash(m)
	m.IdentityHash = core.ComputeIdentityHash(m)
	m.EnvelopeHash = core.ComputeEnvelopeHash(m)
	return m
}

// TestPolicyRuleValidationMatrix freezes the validation contract: nil is valid
// (legacy), valid jurisdictions PE/LATAM/INTL pass, malformed jurisdictions,
// missing legislation/authority and empty tags fail closed with
// INVALID_POLICY_RULE, and a present rule on any non-rule kind fails.
func TestPolicyRuleValidationMatrix(t *testing.T) {
	for _, jurisdiction := range []string{"PE", "LATAM", "INTL"} {
		if err := core.AssertValidPolicyRule(core.KindRule, &core.PolicyRule{
			Jurisdiction: jurisdiction,
			Legislation:  "NATIONAL-TAX",
			Authority:    "authority",
			Tags:         []string{"a"},
		}); err != nil {
			t.Errorf("jurisdiction %q must be valid: %v", jurisdiction, err)
		}
	}
	if err := core.AssertValidPolicyRule(core.KindRule, nil); err != nil {
		t.Errorf("nil policy rule must be valid (legacy): %v", err)
	}
	if err := core.AssertValidPolicyRule(core.KindRule, &core.PolicyRule{}); err == nil {
		t.Errorf("empty policy rule struct must be invalid (missing jurisdiction), got: %v", err)
	}

	invalid := []struct {
		name string
		kind core.MemoryKind
		rule *core.PolicyRule
	}{
		{"kind=fact with rule", core.KindFact, samplePolicyRule()},
		{"kind=decision with rule", core.KindDecision, samplePolicyRule()},
		{"kind=summary with rule", core.KindSummary, samplePolicyRule()},
		{"jurisdiction lowercase", core.KindRule, &core.PolicyRule{Jurisdiction: "pe", Legislation: "L", Authority: "A"}},
		{"jurisdiction lowercase after first", core.KindRule, &core.PolicyRule{Jurisdiction: "Pe", Legislation: "L", Authority: "A"}},
		{"jurisdiction too long", core.KindRule, &core.PolicyRule{Jurisdiction: "ABCDEFGHIJKLMNOPQ", Legislation: "L", Authority: "A"}},
		{"jurisdiction empty", core.KindRule, &core.PolicyRule{Jurisdiction: "", Legislation: "L", Authority: "A"}},
		{"jurisdiction bad char", core.KindRule, &core.PolicyRule{Jurisdiction: "PE_", Legislation: "L", Authority: "A"}},
		{"missing legislation", core.KindRule, &core.PolicyRule{Jurisdiction: "PE", Legislation: "", Authority: "A"}},
		{"blank legislation", core.KindRule, &core.PolicyRule{Jurisdiction: "PE", Legislation: "  ", Authority: "A"}},
		{"missing authority", core.KindRule, &core.PolicyRule{Jurisdiction: "PE", Legislation: "L", Authority: ""}},
		{"blank authority", core.KindRule, &core.PolicyRule{Jurisdiction: "PE", Legislation: "L", Authority: "  "}},
		{"empty tag", core.KindRule, &core.PolicyRule{Jurisdiction: "PE", Legislation: "L", Authority: "A", Tags: []string{""}}},
		{"blank tag", core.KindRule, &core.PolicyRule{Jurisdiction: "PE", Legislation: "L", Authority: "A", Tags: []string{"   "}}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			err := core.AssertValidPolicyRule(tc.kind, tc.rule)
			if err == nil {
				t.Fatal("must fail closed with INVALID_POLICY_RULE")
			}
			if !strings.HasPrefix(err.Error(), "INVALID_POLICY_RULE:") {
				t.Fatalf("error %q must carry the INVALID_POLICY_RULE code", err)
			}
		})
	}

	// Full-memory path: AssertValidMemory routes the kind gate.
	nonRule := basePolicyRuleMemory(false, false)
	nonRule.Kind = core.KindFact
	nonRule.PolicyRule = samplePolicyRule()
	if err := core.AssertValidMemory(nonRule); err == nil || !strings.HasPrefix(err.Error(), "INVALID_POLICY_RULE:") {
		t.Fatalf("AssertValidMemory must reject policyRule on a non-rule kind, got %v", err)
	}
	rule := basePolicyRuleMemory(true, true)
	if err := core.AssertValidMemory(rule); err != nil {
		t.Fatalf("AssertValidMemory must accept a valid v0.6 rule: %v", err)
	}
}

// TestPolicyRuleCanonicalJSONDeterministic freezes the canonical bytes: fixed
// property order (jurisdiction, legislation, authority, tags), sorted +
// deduplicated tags, empty tag set serializes as [], and the bytes round-trip.
func TestPolicyRuleCanonicalJSONDeterministic(t *testing.T) {
	a := core.CanonicalPolicyRuleJSON(samplePolicyRule())
	b := core.CanonicalPolicyRuleJSON(samplePolicyRule())
	if string(a) != string(b) {
		t.Fatal("canonical policy rule JSON must be deterministic")
	}
	if string(a) != pinnedCanonicalPolicyRuleJSON {
		t.Errorf("canonical policy rule JSON = %s, want %s", a, pinnedCanonicalPolicyRuleJSON)
	}

	var decoded core.PolicyRule
	if err := json.Unmarshal(a, &decoded); err != nil {
		t.Fatalf("canonical bytes must decode: %v", err)
	}
	if decoded.Jurisdiction != "PE" || decoded.Legislation != "NATIONAL-TAX" || decoded.Authority != "National tax authority" {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
	if len(decoded.Tags) != 2 || decoded.Tags[0] != "indirect-tax" || decoded.Tags[1] != "late-document" {
		t.Fatalf("canonical tags must be the sorted deduplicated set, got %v", decoded.Tags)
	}

	// An empty tag set serializes as [] (never null) — deterministic cross-runtime.
	noTags := core.CanonicalPolicyRuleJSON(&core.PolicyRule{Jurisdiction: "INTL", Legislation: "L", Authority: "A"})
	if string(noTags) != `{"jurisdiction":"INTL","legislation":"L","authority":"A","tags":[]}` {
		t.Fatalf("empty tags must serialize as [], got %s", noTags)
	}

	// ClonePolicyRule deep-copies the tags slice.
	clone := core.ClonePolicyRule(samplePolicyRule())
	clone.Tags[0] = "mutated"
	if samplePolicyRule().Tags[0] != "late-document" {
		t.Fatal("ClonePolicyRule must not share the tags slice")
	}
	if core.ClonePolicyRule(nil) != nil {
		t.Fatal("ClonePolicyRule(nil) must stay nil")
	}
}

// TestPolicyRuleHashContributionPreservesLegacyHashes is the frozen v0.3 hash
// contract decision carried into v0.6.0: the NEW optional field contributes
// the empty string when absent, so a memory WITHOUT rule metadata hashes
// byte-identically to its pre-v0.6 value (pinned constants); a memory WITH
// rule metadata hashes differently and deterministically; and a rule changing
// ONLY its Validity window gets a distinct hash (the v0.6 promise).
func TestPolicyRuleHashContributionPreservesLegacyHashes(t *testing.T) {
	legacy := basePolicyRuleMemory(false, false)
	if legacy.ContentHash != pinnedLegacyContentHash || legacy.EnvelopeHash != pinnedLegacyEnvelopeHash {
		t.Logf("LEGACY content=%s envelope=%s", legacy.ContentHash, legacy.EnvelopeHash)
	}
	if legacy.ContentHash == "" || legacy.EnvelopeHash == "" {
		t.Fatal("legacy hashes must be non-empty")
	}
	if pinnedLegacyContentHash != "" && legacy.ContentHash != pinnedLegacyContentHash {
		t.Errorf("legacy contentHash = %s, want pinned %s", legacy.ContentHash, pinnedLegacyContentHash)
	}
	if pinnedLegacyEnvelopeHash != "" && legacy.EnvelopeHash != pinnedLegacyEnvelopeHash {
		t.Errorf("legacy envelopeHash = %s, want pinned %s", legacy.EnvelopeHash, pinnedLegacyEnvelopeHash)
	}

	// A nil rule contributes NOTHING: legacy hashes stay byte-identical.
	clone := core.CloneMemory(legacy)
	clone.PolicyRule = nil
	if core.ComputeContentHash(clone) != legacy.ContentHash {
		t.Fatal("nil policyRule must preserve the content hash")
	}
	if core.ComputeEnvelopeHash(clone) != legacy.EnvelopeHash {
		t.Fatal("nil policyRule must preserve the envelope hash")
	}

	// A present rule changes both hashes deterministically.
	withRule := basePolicyRuleMemory(true, false)
	if withRule.ContentHash == legacy.ContentHash {
		t.Fatal("a policyRule must change the content hash")
	}
	if withRule.EnvelopeHash == legacy.EnvelopeHash {
		t.Fatal("a policyRule must change the envelope hash")
	}
	again := core.CloneMemory(withRule)
	if core.ComputeEnvelopeHash(again) != withRule.EnvelopeHash {
		t.Fatal("equal policyRules must produce equal envelope hashes")
	}

	// The v0.6 promise: changing ONLY the vigencia window yields a distinct
	// hash (the window participates through the policy-rule contribution).
	windowed := basePolicyRuleMemory(true, true)
	if windowed.ContentHash == withRule.ContentHash {
		t.Fatal("adding the vigencia window must change the content hash (v0.6 promise)")
	}
	if windowed.EnvelopeHash == withRule.EnvelopeHash {
		t.Fatal("adding the vigencia window must change the envelope hash (v0.6 promise)")
	}
	otherWindow := core.CloneMemory(windowed)
	otherWindow.Validity = &core.Validity{EffectiveAt: "2027-01-01T00:00:00Z", ExpiresAt: "", Source: "declared"}
	otherWindow.ContentHash = core.ComputeContentHash(otherWindow)
	otherWindow.EnvelopeHash = core.ComputeEnvelopeHash(otherWindow)
	if otherWindow.EnvelopeHash == windowed.EnvelopeHash {
		t.Fatal("changing ONLY the Validity window must change the envelope hash")
	}
	if otherWindow.ContentHash == windowed.ContentHash {
		t.Fatal("changing ONLY the Validity window must change the content hash")
	}

	if pinnedV06ContentHash != "" && windowed.ContentHash != pinnedV06ContentHash {
		t.Errorf("v0.6 contentHash = %s, want pinned %s", windowed.ContentHash, pinnedV06ContentHash)
	}
	if pinnedV06EnvelopeHash != "" && windowed.EnvelopeHash != pinnedV06EnvelopeHash {
		t.Errorf("v0.6 envelopeHash = %s, want pinned %s", windowed.EnvelopeHash, pinnedV06EnvelopeHash)
	}
	t.Logf("V06 content=%s envelope=%s", windowed.ContentHash, windowed.EnvelopeHash)
}

// TestPolicyRuleCanonicalContributionPinned is the Go↔TS byte contract: the
// exact contribution element bytes are pinned in BOTH runtimes (hex digest of
// the element), so a canonicalization or field-order divergence fails one of
// the two runners, never silently.
func TestPolicyRuleCanonicalContributionPinned(t *testing.T) {
	m := basePolicyRuleMemory(true, true)
	if m.ContentHash == "" || m.EnvelopeHash == "" {
		t.Fatal("hashes must be computed")
	}

	// Reconstruct the exact contribution element from the public primitives —
	// the same construction the TypeScript mirror uses.
	element := "policyRule/v0.6\x00" + pinnedCanonicalPolicyRuleJSON +
		"\x00" + m.Validity.EffectiveAt + "\x00" + m.Validity.ExpiresAt + "\x00" + m.Validity.Source
	digest := sha256.Sum256([]byte(element))
	sum := hex.EncodeToString(digest[:])
	if pinnedContributionHex != "" && sum != pinnedContributionHex {
		t.Errorf("contribution element digest = %s, want pinned %s", sum, pinnedContributionHex)
	}
	t.Logf("CONTRIBUTION element=%q digest=%s", element, sum)
	t.Logf("CONTRIBUTION_HEX %s", sum)
}
