// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test is the Delivery Slice 5 RED/GREEN
// suite for the presence-aware anti-rubber-stamp clause
// (openspec/changes/fiscal-runtime-foundations, design.md "Immutable act
// evidence and envelope linkage"): authz.ValidateReviewChecksV1 must fail
// closed for a material/critical approval whenever EITHER acknowledgement is
// omitted OR explicitly false, and succeed only when BOTH are explicitly
// present and true. It must reach the SAME policy outcome as the frozen
// boolean ValidateReviewChecks for every case, distinguishing omitted from
// false only in the TRI-STATE it carries, never in the policy result.
package authz_test

import (
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

func ack(present, value bool) core.ReviewAcknowledgement {
	return core.ReviewAcknowledgement{Present: present, Value: value}
}

// TestValidateReviewChecksV1MaterialPolicy (RED->GREEN): table-driven over
// every materiality level and every combination of the two acknowledgements'
// tri-state (omitted/false/true) — nine combinations for a material/critical
// level, one representative combination for normal/nil (the checks are
// ignored entirely when not required).
func TestValidateReviewChecksV1MaterialPolicy(t *testing.T) {
	material := core.MaterialityMaterial
	critical := core.MaterialityCritical
	normal := core.MaterialityNormal

	cases := []struct {
		name           string
		level          *core.MaterialityLevel
		evidence, rule core.ReviewAcknowledgement
		wantErr        bool
	}{
		{"material: both omitted", &material, ack(false, false), ack(false, false), true},
		{"material: both explicit false", &material, ack(true, false), ack(true, false), true},
		{"material: evidence true, rule omitted", &material, ack(true, true), ack(false, false), true},
		{"material: evidence omitted, rule true", &material, ack(false, false), ack(true, true), true},
		{"material: evidence true, rule explicit false", &material, ack(true, true), ack(true, false), true},
		{"material: evidence explicit false, rule true", &material, ack(true, false), ack(true, true), true},
		{"material: both explicit true", &material, ack(true, true), ack(true, true), false},
		{"critical: both omitted", &critical, ack(false, false), ack(false, false), true},
		{"critical: both explicit true", &critical, ack(true, true), ack(true, true), false},
		{"normal: both omitted never trips the clause", &normal, ack(false, false), ack(false, false), false},
		{"nil level never trips the clause", nil, ack(false, false), ack(false, false), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checks := core.ReviewChecksV1{EvidenceInspected: c.evidence, RuleInspected: c.rule}
			err := authz.ValidateReviewChecksV1(c.level, checks)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ValidateReviewChecksV1(%+v, %+v) = nil, want REVIEW_CHECKS_REQUIRED", c.level, checks)
				}
				if auth.Code(err) != auth.CodeReviewChecksRequired {
					t.Fatalf("code = %q, want %q", auth.Code(err), auth.CodeReviewChecksRequired)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateReviewChecksV1(%+v, %+v) = %v, want nil", c.level, checks, err)
			}
		})
	}
}

// TestValidateReviewChecksV1MatchesBooleanOutcome (TRIANGULATE): for every
// case above, the tri-state policy OUTCOME (pass/fail) must be identical to
// the frozen boolean ValidateReviewChecks fed the equivalent (Present&&Value)
// projection — the tri-state adds evidence, it never changes the decision.
func TestValidateReviewChecksV1MatchesBooleanOutcome(t *testing.T) {
	material := core.MaterialityMaterial
	cases := []struct {
		evidence, rule core.ReviewAcknowledgement
	}{
		{ack(false, false), ack(false, false)},
		{ack(true, false), ack(true, false)},
		{ack(true, true), ack(false, false)},
		{ack(true, true), ack(true, true)},
	}
	for _, c := range cases {
		v1 := authz.ValidateReviewChecksV1(&material, core.ReviewChecksV1{EvidenceInspected: c.evidence, RuleInspected: c.rule})
		legacy := authz.ValidateReviewChecks(&material, core.ReviewChecks{
			EvidenceInspected: c.evidence.Present && c.evidence.Value,
			RuleInspected:     c.rule.Present && c.rule.Value,
		})
		if (v1 == nil) != (legacy == nil) {
			t.Fatalf("evidence=%+v rule=%+v: v1 err=%v, legacy err=%v — outcomes must match", c.evidence, c.rule, v1, legacy)
		}
	}
}
