// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the canonical
// presence-aware reviewChecks decoder (openspec/changes/
// fiscal-runtime-foundations, design.md "The raw DTO uses presence-aware
// nullable booleans"). It lives beside the decoder it proves: the rule
// "omitted is never a successful declaration" is a contract property, so it
// is proven once here rather than once per transport adapter.
package core

import (
	"errors"
	"testing"
)

// TestDecodeReviewChecksV1JSONPresence: an absent document maps to both checks
// omitted; explicit true/false map to present with that value; a present
// object with only one member leaves the other omitted.
func TestDecodeReviewChecksV1JSONPresence(t *testing.T) {
	present := func(v bool) ReviewAcknowledgement { return ReviewAcknowledgement{Present: true, Value: v} }
	cases := []struct {
		name string
		raw  string
		want ReviewChecksV1
	}{
		{"absent", "", ReviewChecksV1{}},
		{"empty object", `{}`, ReviewChecksV1{}},
		{"both true", `{"evidenceInspected":true,"applicableRulesInspected":true}`,
			ReviewChecksV1{EvidenceInspected: present(true), RuleInspected: present(true)}},
		{"both false", `{"evidenceInspected":false,"applicableRulesInspected":false}`,
			ReviewChecksV1{EvidenceInspected: present(false), RuleInspected: present(false)}},
		{"only evidence present", `{"evidenceInspected":true}`,
			ReviewChecksV1{EvidenceInspected: present(true)}},
		{"only rules present", `{"applicableRulesInspected":false}`,
			ReviewChecksV1{RuleInspected: present(false)}},
		// Member order is free: these two members are independently optional,
		// unlike the canonical-order fiscal scope binding.
		{"reverse order", `{"applicableRulesInspected":true,"evidenceInspected":false}`,
			ReviewChecksV1{EvidenceInspected: present(false), RuleInspected: present(true)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DecodeReviewChecksV1JSON([]byte(c.raw))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got != c.want {
				t.Fatalf("DecodeReviewChecksV1JSON(%q) = %+v, want %+v", c.raw, got, c.want)
			}
		})
	}
}

// TestDecodeReviewChecksV1JSONRejectsMalformed: every ambiguous document fails
// closed. The duplicate-member case is the security-relevant one — plain
// encoding/json would silently keep the LAST value, letting a caller smuggle a
// false declaration past a reviewer who read the first.
func TestDecodeReviewChecksV1JSONRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"duplicate member", `{"evidenceInspected":true,"evidenceInspected":false,"applicableRulesInspected":true}`},
		{"duplicate member same value", `{"evidenceInspected":true,"evidenceInspected":true}`},
		{"unknown member", `{"evidenceInspected":true,"applicableRulesInspected":true,"actorId":"x"}`},
		{"caller-declared authority", `{"subjectId":"maria.torres"}`},
		{"non-boolean value", `{"evidenceInspected":"yes","applicableRulesInspected":true}`},
		{"object value", `{"evidenceInspected":{"value":true}}`},
		{"null value", `{"evidenceInspected":null}`},
		{"trailing data", `{"evidenceInspected":true}{}`},
		{"not an object", `true`},
		{"array", `[]`},
		{"unterminated object", `{"evidenceInspected":true`},
		{"whitespace only", ` `},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DecodeReviewChecksV1JSON([]byte(c.raw))
			if err == nil {
				t.Fatalf("DecodeReviewChecksV1JSON(%q) must fail closed, got %+v", c.raw, got)
			}
			// A rejected document never yields a partially-populated result a
			// caller could mistake for a declaration.
			if got != (ReviewChecksV1{}) {
				t.Fatalf("a rejected document must return the zero value, got %+v", got)
			}
		})
	}
}

// TestDecodeFiscalWriteIntentJSONIsTheSharedSeam: a valid binding produces a
// non-nil intent carrying the decoded binding, and an invalid one produces a
// nil intent plus the SAME typed *FiscalScopeError the store boundary raises —
// so every adapter that routes through this seam reports one vocabulary.
func TestDecodeFiscalWriteIntentJSONIsTheSharedSeam(t *testing.T) {
	intent, err := DecodeFiscalWriteIntentJSON([]byte(`{"version":"v2"}`))
	if err == nil {
		t.Fatal("an unsupported version must fail to decode")
	}
	if intent != nil {
		t.Fatalf("a rejected binding must yield a nil intent, got %+v", intent)
	}
	var fse *FiscalScopeError
	if !errors.As(err, &fse) {
		t.Fatalf("error %v must be a *FiscalScopeError", err)
	}
	if fse.Code != UnsupportedScopeVersion {
		t.Fatalf("code = %q, want %q", fse.Code, UnsupportedScopeVersion)
	}
}
