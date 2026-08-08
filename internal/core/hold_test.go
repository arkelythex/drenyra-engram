// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module freezes the PURE object-level legal-hold model
// (v0.8 batch 3 — docs/architecture/evidence-lifecycle-v0.8.md §3.2/§7):
//
//   - the closed hold-kind set (legal/audit/dispute/fiscalization/other — one
//     shared enum with the retention policy, never duplicated);
//   - the fail-closed validators (object id, exact company scope, kind, reason,
//     owner, placed provenance, parseable timestamps, CONSISTENT lift fields);
//   - the PURE active-blocking-hold helper: an ACTIVE hold of a blocking kind
//     blocks; an EMPTY blocking set blocks NOTHING (fail closed); a lifted hold
//     never blocks; 'other' never blocks by default;
//   - the FROZEN canonical bytes pinned byte-identically with the TypeScript
//     mirror (core/__tests__/evidence-hold.test.ts).
package core

import (
	"strings"
	"testing"
)

// sampleHold is the Go↔TS parity fixture: the SAME sample the TypeScript mirror
// test pins (same holdId, object id, scope, kind, reason, owner, timestamps).
func sampleHold() EvidenceHold {
	return EvidenceHold{
		HoldID:         "00000000-0000-4000-8000-000000000001",
		ObjectID:       strings.Repeat("a", 64),
		TenantID:       "org-001",
		CompanyID:      "acme",
		RUC:            "20100039201",
		Period:         "202401",
		Kind:           HoldKindLegal,
		Reason:         "dispute F001-948 under review",
		OwnerSubjectID: "subject-1",
		PlacedAt:       "2026-08-07T12:00:00.000Z",
		PlacedBy:       "lucia.ramirez",
	}
}

// pinnedPlacedCanonicalJSON is the FROZEN canonical bytes of sampleHold — the
// EXACT same literal pinned in core/__tests__/evidence-hold.test.ts (Go↔TS
// canonical bytes must match byte-identically: fixed property order, compact
// UTF-8, no HTML escaping, omitempty optional fields). The placed state carries
// NO lift keys.
const pinnedPlacedCanonicalJSON = `{"holdId":"00000000-0000-4000-8000-000000000001","objectId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tenantId":"org-001","companyId":"acme","ruc":"20100039201","period":"202401","kind":"legal","reason":"dispute F001-948 under review","ownerSubjectId":"subject-1","placedAt":"2026-08-07T12:00:00.000Z","placedBy":"lucia.ramirez"}`

func TestHoldClosedKindSet(t *testing.T) {
	for _, kind := range []HoldKind{
		HoldKindLegal, HoldKindAudit, HoldKindDispute, HoldKindFiscalization, HoldKindOther,
	} {
		if !IsValidHoldKind(kind) {
			t.Errorf("IsValidHoldKind(%q) = false, want true", kind)
		}
	}
	if IsValidHoldKind("rogue") || IsValidHoldKind("") {
		t.Error("IsValidHoldKind accepted an unknown token — the hold-kind set is closed")
	}
	// ONE shared enum with the retention policy (never duplicated).
	if HoldKindLegal != HoldKind(RetentionHoldKindLegal) || string(HoldKindOther) != RetentionHoldKindOther {
		t.Error("the hold-kind tokens must be the frozen retention-policy tokens (one closed set)")
	}
}

func TestCanonicalEvidenceHoldJSONParity(t *testing.T) {
	h := sampleHold()
	got := string(CanonicalEvidenceHoldJSON(h))
	if got != pinnedPlacedCanonicalJSON {
		t.Fatalf("canonical bytes differ from the pinned Go↔TS literal:\n got %s\nwant %s", got, pinnedPlacedCanonicalJSON)
	}

	// The lifted variant appends the lift fields AFTER placedBy in fixed order.
	lifted := h
	lifted.LiftedAt = "2026-08-08T09:00:00.000Z"
	lifted.LiftedBy = "maria.torres"
	lifted.LiftReason = "dispute resolved"
	liftedJSON := string(CanonicalEvidenceHoldJSON(lifted))
	if !strings.Contains(liftedJSON, `"placedBy":"lucia.ramirez"`) ||
		!strings.Contains(liftedJSON, `"liftedAt":"2026-08-08T09:00:00.000Z","liftedBy":"maria.torres","liftReason":"dispute resolved"`) {
		t.Fatalf("lifted canonical bytes misplaced: %s", liftedJSON)
	}
	if strings.Index(liftedJSON, "placedBy") > strings.Index(liftedJSON, "liftedAt") {
		t.Fatalf("liftedAt must come strictly after placedBy: %s", liftedJSON)
	}

	// omitempty: an empty period contributes NO key (TS parity contract).
	noPeriod := h
	noPeriod.Period = ""
	noPeriodJSON := string(CanonicalEvidenceHoldJSON(noPeriod))
	if strings.Contains(noPeriodJSON, `"period"`) {
		t.Fatalf("empty period must be omitted (omitempty): %s", noPeriodJSON)
	}
	if strings.Contains(noPeriodJSON, "liftedAt") {
		t.Fatalf("placed hold must carry no lift keys: %s", noPeriodJSON)
	}
}

func TestAssertValidPlaceHoldCommand(t *testing.T) {
	valid := PlaceHoldCommand{
		ObjectID:       strings.Repeat("b", 64),
		Kind:           HoldKindAudit,
		Reason:         "audit in progress",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-1",
	}
	if err := AssertValidPlaceHoldCommand(valid); err != nil {
		t.Fatalf("valid place command rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*PlaceHoldCommand)
	}{
		{"object id", func(c *PlaceHoldCommand) { c.ObjectID = "not-hex" }},
		{"kind", func(c *PlaceHoldCommand) { c.Kind = "rogue" }},
		{"reason", func(c *PlaceHoldCommand) { c.Reason = "  " }},
		{"owner", func(c *PlaceHoldCommand) { c.OwnerSubjectID = "" }},
		{"requestId", func(c *PlaceHoldCommand) { c.RequestID = "" }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cmd := valid
			tt.mutate(&cmd)
			if err := AssertValidPlaceHoldCommand(cmd); err == nil {
				t.Fatalf("place command with invalid %s accepted", tt.name)
			}
		})
	}
}

func TestAssertValidLiftHoldCommand(t *testing.T) {
	valid := LiftHoldCommand{HoldID: "00000000-0000-4000-8000-000000000002", Reason: "resolved", RequestID: "req-2"}
	if err := AssertValidLiftHoldCommand(valid); err != nil {
		t.Fatalf("valid lift command rejected: %v", err)
	}
	if err := AssertValidLiftHoldCommand(LiftHoldCommand{HoldID: "", Reason: "x", RequestID: "r"}); err == nil {
		t.Fatal("lift command without holdId accepted")
	}
	if err := AssertValidLiftHoldCommand(LiftHoldCommand{HoldID: "h", Reason: " ", RequestID: "r"}); err == nil {
		t.Fatal("lift command without reason accepted")
	}
	if err := AssertValidLiftHoldCommand(LiftHoldCommand{HoldID: "h", Reason: "x", RequestID: ""}); err == nil {
		t.Fatal("lift command without requestId accepted")
	}
}

func TestAssertValidEvidenceHold(t *testing.T) {
	if err := AssertValidEvidenceHold(sampleHold()); err != nil {
		t.Fatalf("valid hold rejected: %v", err)
	}
	lifted := sampleHold()
	lifted.LiftedAt = "2026-08-08T09:00:00.000Z"
	lifted.LiftedBy = "maria.torres"
	lifted.LiftReason = "resolved"
	if err := AssertValidEvidenceHold(lifted); err != nil {
		t.Fatalf("valid lifted hold rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*EvidenceHold)
	}{
		{"hold id", func(h *EvidenceHold) { h.HoldID = "not-a-uuid" }},
		{"object id", func(h *EvidenceHold) { h.ObjectID = "nope" }},
		{"tenant", func(h *EvidenceHold) { h.TenantID = "" }},
		{"company", func(h *EvidenceHold) { h.CompanyID = "" }},
		{"ruc", func(h *EvidenceHold) { h.RUC = "123" }},
		{"period", func(h *EvidenceHold) { h.Period = "202413" }},
		{"kind", func(h *EvidenceHold) { h.Kind = "rogue" }},
		{"reason", func(h *EvidenceHold) { h.Reason = "" }},
		{"owner", func(h *EvidenceHold) { h.OwnerSubjectID = "" }},
		{"placedBy", func(h *EvidenceHold) { h.PlacedBy = "" }},
		{"placedAt", func(h *EvidenceHold) { h.PlacedAt = "not-a-date" }},
		{"partial lift", func(h *EvidenceHold) { h.LiftedAt = "2026-08-08T09:00:00.000Z" }},
		{"liftedAt unparseable", func(h *EvidenceHold) {
			h.LiftedAt, h.LiftedBy, h.LiftReason = "nope", "maria.torres", "resolved"
		}},
		{"lift reason blank", func(h *EvidenceHold) {
			h.LiftedAt, h.LiftedBy, h.LiftReason = "2026-08-08T09:00:00.000Z", "maria.torres", "  "
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			h := sampleHold()
			tt.mutate(&h)
			if err := AssertValidEvidenceHold(h); err == nil {
				t.Fatalf("hold with invalid %s accepted", tt.name)
			}
		})
	}
}

func TestHasActiveBlockingHold(t *testing.T) {
	defaultBlocking := []string{RetentionHoldKindLegal, RetentionHoldKindAudit, RetentionHoldKindDispute, RetentionHoldKindFiscalization}

	legal := sampleHold()
	if !HasActiveBlockingHold(legal, defaultBlocking) {
		t.Fatal("an ACTIVE legal hold with the default blocking set must block")
	}

	// A lifted hold never blocks.
	lifted := legal
	lifted.LiftedAt = "2026-08-08T09:00:00.000Z"
	lifted.LiftedBy = "maria.torres"
	lifted.LiftReason = "resolved"
	if HasActiveBlockingHold(lifted, defaultBlocking) {
		t.Fatal("a lifted hold must never block")
	}

	// 'other' never blocks by default, but a deployment may extend the set.
	other := legal
	other.Kind = HoldKindOther
	if HasActiveBlockingHold(other, defaultBlocking) {
		t.Fatal("an 'other' hold must not block under the default blocking set")
	}
	if !HasActiveBlockingHold(other, append(append([]string(nil), defaultBlocking...), RetentionHoldKindOther)) {
		t.Fatal("an 'other' hold must block when the deployment extended the blocking set")
	}

	// An EMPTY blocking set blocks NOTHING (fail closed).
	if HasActiveBlockingHold(legal, nil) {
		t.Fatal("an empty blocking set must block nothing")
	}

	// A non-blocking kind on an active hold never blocks.
	audit := legal
	audit.Kind = HoldKindAudit
	if HasActiveBlockingHold(audit, []string{RetentionHoldKindLegal}) {
		t.Fatal("an audit hold must not block when only legal is blocking")
	}
}
