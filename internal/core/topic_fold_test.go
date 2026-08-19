// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the canonical topic-key
// fold (sdd-060-tenant-cli, FR-TEN-3/AC-TEN-2): pure, deterministic, formatting
// drift collides while word boundaries survive. No monetary field exists
// anywhere in this file.
package core

import "testing"

func TestFoldTopicKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already folded", "rule igv credit", "rule igv credit"},
		{"case folding", "rule/IGV credit", "rule igv credit"},
		{"hyphen drift", "rule/igv-credit", "rule igv credit"},
		{"dot drift", "rule.igv.credit", "rule igv credit"},
		{"double slash", "rule//igv//credit", "rule igv credit"},
		{"underscore drift", "rule_igv_credit", "rule igv credit"},
		{"mixed whitespace", "rule\t igv\ncredit", "rule igv credit"},
		{"leading trailing", "  rule/igv credit  ", "rule igv credit"},
		{"digits kept", "policy/v1.2", "policy v1 2"},
		{"accent kept (no accent fold)", "sede principal", "sede principal"},
		{"empty", "", ""},
		{"only punctuation", "///---", ""},
		{"unicode lower", "RULE/IGV/CRÉDITO", "rule igv crédito"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FoldTopicKey(tc.in); got != tc.want {
				t.Fatalf("FoldTopicKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFoldTopicKeyDriftCollision pins the drift contract: the two canonical
// examples from the spec collide under the fold.
func TestFoldTopicKeyDriftCollision(t *testing.T) {
	if FoldTopicKey("rule/IGV credit") != FoldTopicKey("rule/igv-credit") {
		t.Fatal("drift pair must collide under the fold")
	}
	if FoldTopicKey("rule/IGV credit") != "rule igv credit" {
		t.Fatalf("fold = %q, want %q", FoldTopicKey("rule/IGV credit"), "rule igv credit")
	}
}

// TestFoldTopicKeyDistinctNoCollision guards against over-folding: genuinely
// distinct words never collide.
func TestFoldTopicKeyDistinctNoCollision(t *testing.T) {
	if FoldTopicKey("rule igv credit") == FoldTopicKey("rule iqu reten") {
		t.Fatal("distinct keys must not collide")
	}
}
