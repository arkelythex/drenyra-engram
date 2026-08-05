// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the v0.4.0 Step 2 judgment
// model and pure lifecycle: the entity round-trip, the six proposable relations,
// the legal/illegal transition matrix with typed error codes, and the canonical
// judgment hash (deterministic, status-shaped, and pinned to the exact bytes the
// TypeScript mirror produces).
package core_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// baseJudgment is the canonical proposed judgment of the hash vectors: the same
// fixture in Go and TypeScript must produce the SAME pinned hex below.
func baseJudgment() core.AccountingJudgment {
	return core.AccountingJudgment{
		ID:             "judgment-1",
		TenantID:       "tenant-1",
		CompanyID:      "acme",
		FiscalPeriodID: "202601",
		FromID:         "obs-1",
		ToID:           "obs-2",
		Relation:       core.RelationContradicts,
		Status:         core.JudgmentProposed,
		Proposer: core.Source{
			System:    "sire",
			ActorID:   "agent-7",
			ActorKind: core.ActorKindAgent,
		},
		ProposalReason: "igv rate mismatch",
		ProposedAt:     "2026-08-05T12:00:00Z",
		UpdatedAt:      "2026-08-05T12:00:00Z",
	}
}

// snapshot is the canonical adjudicator snapshot of the confirmed vector.
func snapshot() *auth.PrincipalSnapshot {
	return &auth.PrincipalSnapshot{
		SubjectID:            "subject-1",
		MembershipID:         "membership-1",
		Roles:                []auth.AccountingRole{auth.RoleController},
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      "2026-08-05T13:00:00Z",
	}
}

func confirmedJudgment(t *testing.T) core.AccountingJudgment {
	t.Helper()
	j := baseJudgment()
	if err := core.ConfirmJudgment(&j, "igv rate confirmed at 18 percent", snapshot(), "judgment-policy/v0.4.0", "2026-08-05T13:00:00Z"); err != nil {
		t.Fatalf("confirmed fixture: %v", err)
	}
	return j
}

// TestJudgmentModelRoundTrip marshals and unmarshals a proposed AND a confirmed
// judgment (covering the Adjudicator pointer) and requires exact equality.
func TestJudgmentModelRoundTrip(t *testing.T) {
	for _, j := range []core.AccountingJudgment{baseJudgment(), confirmedJudgment(t)} {
		data, err := json.Marshal(j)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got core.AccountingJudgment
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !reflect.DeepEqual(got, j) {
			t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, j)
		}
	}
}

// TestJudgmentStatusesAreValid pins the closed status set.
func TestJudgmentStatusesAreValid(t *testing.T) {
	want := []core.JudgmentStatus{
		core.JudgmentProposed, core.JudgmentConfirmed, core.JudgmentRejected,
		core.JudgmentWithdrawn, core.JudgmentSuperseded,
	}
	for _, s := range want {
		if !core.IsValidJudgmentStatus(s) {
			t.Errorf("IsValidJudgmentStatus(%q) = false, want true", s)
		}
	}
	if core.IsValidJudgmentStatus(core.JudgmentStatus("draft")) {
		t.Error("IsValidJudgmentStatus(draft) = true, want false")
	}
}

// TestProposableRelations freezes the six proposable relations and asserts that
// conflicts_with, related and every other vocabulary member are NOT proposable.
func TestProposableRelations(t *testing.T) {
	got := core.ProposableRelations()
	want := []core.Relation{
		core.RelationSupports, core.RelationContradicts, core.RelationExplains,
		core.RelationReconciles, core.RelationReverses, core.RelationSupersedes,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProposableRelations() = %v, want %v (fixed order)", got, want)
	}

	yes := map[core.Relation]bool{}
	for _, r := range want {
		yes[r] = true
	}
	for _, r := range []core.Relation{
		core.RelationRelated, core.RelationCompatible, core.RelationScoped,
		core.RelationConflictsWith, core.RelationNotConflict,
		core.RelationDerivedFrom, core.RelationPostedAs, core.RelationRequires,
		core.RelationViolates, core.RelationApprovedBy, core.RelationRejectedBy,
	} {
		if core.IsProposableRelation(r) {
			t.Errorf("IsProposableRelation(%q) = true, want false (legacy/other)", r)
		}
	}
	for r := range yes {
		if !core.IsProposableRelation(r) {
			t.Errorf("IsProposableRelation(%q) = false, want true", r)
		}
	}
}

// TestCanProposeRequiresAgentOrSystem: a human Source can never propose; a
// human's authority arrives as a verified principal at confirm/reject time.
func TestCanProposeRequiresAgentOrSystem(t *testing.T) {
	if core.CanPropose(core.Source{System: "sire", ActorKind: core.ActorKindAgent}) != true {
		t.Error("agent proposer must be allowed")
	}
	if core.CanPropose(core.Source{System: "drenyra-core", ActorKind: core.ActorKindSystem}) != true {
		t.Error("system proposer must be allowed")
	}
	if core.CanPropose(core.Source{System: "manual", ActorID: "maria", ActorKind: core.ActorKindHuman}) {
		t.Error("human proposer must NOT be allowed (source is provenance, never authority)")
	}
}

// TestJudgmentTransitions drives every legal transition and every illegal one
// through the pure mutators, asserting the typed error code on failure and the
// exact resulting status on success.
func TestJudgmentTransitions(t *testing.T) {
	confirm := func(j *core.AccountingJudgment) error {
		return core.ConfirmJudgment(j, "resolution", snapshot(), "judgment-policy/v0.4.0", "2026-08-05T13:00:00Z")
	}
	reject := func(j *core.AccountingJudgment) error {
		return core.RejectJudgment(j, "rejection reason", snapshot(), "judgment-policy/v0.4.0", "2026-08-05T13:00:00Z")
	}
	withdraw := func(j *core.AccountingJudgment) error {
		return core.WithdrawJudgment(j, "2026-08-05T13:00:00Z")
	}
	supersede := func(j *core.AccountingJudgment) error {
		return core.SupersedeJudgment(j, "judgment-2", "2026-08-05T13:00:00Z")
	}

	all := []core.JudgmentStatus{
		core.JudgmentProposed, core.JudgmentConfirmed, core.JudgmentRejected,
		core.JudgmentWithdrawn, core.JudgmentSuperseded,
	}
	tests := []struct {
		name       string
		from       core.JudgmentStatus
		apply      func(*core.AccountingJudgment) error
		wantCode   string
		wantStatus core.JudgmentStatus
	}{
		// proposed → confirmed
		{"confirm from proposed", core.JudgmentProposed, confirm, "", core.JudgmentConfirmed},
		{"confirm from confirmed", core.JudgmentConfirmed, confirm, auth.CodeInvalidJudgmentTransition, core.JudgmentConfirmed},
		{"confirm from rejected", core.JudgmentRejected, confirm, auth.CodeInvalidJudgmentTransition, core.JudgmentRejected},
		{"confirm from withdrawn", core.JudgmentWithdrawn, confirm, auth.CodeInvalidJudgmentTransition, core.JudgmentWithdrawn},
		{"confirm from superseded", core.JudgmentSuperseded, confirm, auth.CodeInvalidJudgmentTransition, core.JudgmentSuperseded},
		// proposed → rejected
		{"reject from proposed", core.JudgmentProposed, reject, "", core.JudgmentRejected},
		{"reject from confirmed", core.JudgmentConfirmed, reject, auth.CodeInvalidJudgmentTransition, core.JudgmentConfirmed},
		{"reject from rejected", core.JudgmentRejected, reject, auth.CodeInvalidJudgmentTransition, core.JudgmentRejected},
		{"reject from withdrawn", core.JudgmentWithdrawn, reject, auth.CodeInvalidJudgmentTransition, core.JudgmentWithdrawn},
		{"reject from superseded", core.JudgmentSuperseded, reject, auth.CodeInvalidJudgmentTransition, core.JudgmentSuperseded},
		// proposed → withdrawn
		{"withdraw from proposed", core.JudgmentProposed, withdraw, "", core.JudgmentWithdrawn},
		{"withdraw from confirmed", core.JudgmentConfirmed, withdraw, auth.CodeInvalidJudgmentTransition, core.JudgmentConfirmed},
		{"withdraw from rejected", core.JudgmentRejected, withdraw, auth.CodeInvalidJudgmentTransition, core.JudgmentRejected},
		{"withdraw from withdrawn", core.JudgmentWithdrawn, withdraw, auth.CodeInvalidJudgmentTransition, core.JudgmentWithdrawn},
		{"withdraw from superseded", core.JudgmentSuperseded, withdraw, auth.CodeInvalidJudgmentTransition, core.JudgmentSuperseded},
		// confirmed → superseded (ONLY)
		{"supersede from proposed", core.JudgmentProposed, supersede, auth.CodeInvalidJudgmentTransition, core.JudgmentProposed},
		{"supersede from confirmed", core.JudgmentConfirmed, supersede, "", core.JudgmentSuperseded},
		{"supersede from rejected", core.JudgmentRejected, supersede, auth.CodeInvalidJudgmentTransition, core.JudgmentRejected},
		{"supersede from withdrawn", core.JudgmentWithdrawn, supersede, auth.CodeInvalidJudgmentTransition, core.JudgmentWithdrawn},
		{"supersede from superseded", core.JudgmentSuperseded, supersede, auth.CodeInvalidJudgmentTransition, core.JudgmentSuperseded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := baseJudgment()
			j.Status = tt.from
			err := tt.apply(&j)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("transition must succeed, got %v", err)
				}
				if j.Status != tt.wantStatus {
					t.Errorf("status = %q, want %q", j.Status, tt.wantStatus)
				}
				return
			}
			if err == nil {
				t.Fatalf("transition must fail with %s, got nil", tt.wantCode)
			}
			if got := auth.Code(err); got != tt.wantCode {
				t.Errorf("code = %q, want %q (err: %v)", got, tt.wantCode, err)
			}
			if j.Status != tt.wantStatus {
				t.Errorf("status mutated after illegal transition = %q, want %q", j.Status, tt.wantStatus)
			}
		})
	}

	// The adjacency table must agree with the mutators for every pair.
	for _, from := range all {
		for _, to := range all {
			legal := core.IsLegalJudgmentTransition(from, to)
			switch from {
			case core.JudgmentProposed:
				want := to == core.JudgmentConfirmed || to == core.JudgmentRejected ||
					to == core.JudgmentWithdrawn || to == core.JudgmentSuperseded
				if legal != want {
					t.Errorf("IsLegalJudgmentTransition(%s→%s) = %v, want %v", from, to, legal, want)
				}
			case core.JudgmentConfirmed:
				if legal != (to == core.JudgmentSuperseded) {
					t.Errorf("IsLegalJudgmentTransition(%s→%s) = %v, want confirmed→superseded only", from, to, legal)
				}
			default:
				if legal {
					t.Errorf("IsLegalJudgmentTransition(%s→%s) = true, want false (terminal)", from, to)
				}
			}
		}
	}
}

// TestConfirmRequiresResolutionAndPrincipal: an empty resolution fails with
// RESOLUTION_REQUIRED and a nil adjudicator fails with AUTHENTICATION_REQUIRED;
// the judgment stays proposed.
func TestConfirmRequiresResolutionAndPrincipal(t *testing.T) {
	j := baseJudgment()
	err := core.ConfirmJudgment(&j, "   ", snapshot(), "judgment-policy/v0.4.0", "2026-08-05T13:00:00Z")
	if auth.Code(err) != auth.CodeResolutionRequired {
		t.Fatalf("blank resolution: code = %q, want RESOLUTION_REQUIRED", auth.Code(err))
	}
	if j.Status != core.JudgmentProposed {
		t.Errorf("status mutated by failed confirm = %q", j.Status)
	}

	err = core.ConfirmJudgment(&j, "resolution", nil, "judgment-policy/v0.4.0", "2026-08-05T13:00:00Z")
	if auth.Code(err) != auth.CodeAuthenticationRequired {
		t.Fatalf("nil adjudicator: code = %q, want AUTHENTICATION_REQUIRED", auth.Code(err))
	}

	if err := core.RejectJudgment(&j, "", snapshot(), "judgment-policy/v0.4.0", "2026-08-05T13:00:00Z"); auth.Code(err) != auth.CodeResolutionRequired {
		t.Fatalf("blank rejection reason: code = %q, want RESOLUTION_REQUIRED", auth.Code(err))
	}
}

// TestRejectStoresHumanReasonAsResolution: the proposal reason is never silently
// promoted into the professional resolution.
func TestRejectStoresHumanReasonAsResolution(t *testing.T) {
	j := baseJudgment()
	if err := core.RejectJudgment(&j, "evidence does not match the comprobante", snapshot(), "judgment-policy/v0.4.0", "2026-08-05T13:00:00Z"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if j.Resolution != "evidence does not match the comprobante" {
		t.Errorf("Resolution = %q, want the human reason", j.Resolution)
	}
	if j.Status != core.JudgmentRejected || j.DecidedAt != "2026-08-05T13:00:00Z" {
		t.Errorf("rejected judgment = %+v", j)
	}
}

// TestSupersedeKeepsAdjudicationFieldsByteEqual: supersession changes ONLY
// status, SupersedesID and UpdatedAt; resolution, adjudicator and policy stay.
func TestSupersedeKeepsAdjudicationFieldsByteEqual(t *testing.T) {
	j := confirmedJudgment(t)
	if err := core.SupersedeJudgment(&j, "judgment-2", "2026-08-05T14:00:00Z"); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if j.Status != core.JudgmentSuperseded || j.SupersedesID != "judgment-2" {
		t.Errorf("superseded routing = %+v", j)
	}
	if j.Resolution != "igv rate confirmed at 18 percent" || j.PolicyVersion != "judgment-policy/v0.4.0" || j.Adjudicator == nil {
		t.Errorf("adjudication fields must stay byte-equal after supersession: %+v", j)
	}
	if j.DecidedAt != "2026-08-05T13:00:00Z" {
		t.Errorf("DecidedAt must keep the original confirmation time, got %q", j.DecidedAt)
	}
}

// TestComputeJudgmentHash pins the canonical contract: the reviewed shape and
// the confirmed shape are byte-exact (the same fixture in TypeScript must
// produce these identical hex strings), the hash is deterministic, status
// changes it, and routing/mutable fields never participate.
func TestComputeJudgmentHash(t *testing.T) {
	const wantProposedHex = "62cf0ff6d9d5531d771008ce0cb6dbe6d474adbda11d51e8bdff7a0d93409ad8"
	const wantConfirmedHex = "9a43192f84d067fd3dafde45a1e64f80f3ae5e24a9cb274ccf44bd335891aaad"

	t.Run("pinned canonical vectors", func(t *testing.T) {
		if got := core.ComputeJudgmentHash(baseJudgment()); got != wantProposedHex {
			t.Errorf("proposed hash = %s, want %s", got, wantProposedHex)
		}
		if got := core.ComputeJudgmentHash(confirmedJudgment(t)); got != wantConfirmedHex {
			t.Errorf("confirmed hash = %s, want %s", got, wantConfirmedHex)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		if a, b := core.ComputeJudgmentHash(baseJudgment()), core.ComputeJudgmentHash(baseJudgment()); a != b {
			t.Errorf("same input produced different hashes: %s vs %s", a, b)
		}
	})

	t.Run("status change proposed to confirmed changes the hash", func(t *testing.T) {
		if core.ComputeJudgmentHash(baseJudgment()) == core.ComputeJudgmentHash(confirmedJudgment(t)) {
			t.Error("proposed and confirmed hashes must differ")
		}
	})

	t.Run("confirmed hash covers resolution", func(t *testing.T) {
		a := confirmedJudgment(t)
		b := confirmedJudgment(t)
		b.Resolution = "different professional resolution"
		if core.ComputeJudgmentHash(a) == core.ComputeJudgmentHash(b) {
			t.Error("different resolution must change the confirmed hash")
		}
	})

	t.Run("confirmed hash covers decidedAt", func(t *testing.T) {
		a := confirmedJudgment(t)
		b := confirmedJudgment(t)
		b.DecidedAt = "2026-08-05T15:00:00Z"
		if core.ComputeJudgmentHash(a) == core.ComputeJudgmentHash(b) {
			t.Error("different decidedAt must change the confirmed hash")
		}
	})

	t.Run("proposed hash covers proposalReason", func(t *testing.T) {
		a := baseJudgment()
		b := baseJudgment()
		b.ProposalReason = "a different mismatch"
		if core.ComputeJudgmentHash(a) == core.ComputeJudgmentHash(b) {
			t.Error("different proposalReason must change the proposed hash")
		}
	})

	t.Run("proposed hash ignores resolution updatedAt and supersedesId", func(t *testing.T) {
		a := baseJudgment()
		b := baseJudgment()
		b.Resolution = "must not participate in the reviewed hash"
		b.UpdatedAt = "2026-08-05T23:59:59Z"
		b.SupersedesID = "judgment-9"
		if core.ComputeJudgmentHash(a) != core.ComputeJudgmentHash(b) {
			t.Error("reviewed shape must exclude resolution, updatedAt and supersedesId")
		}
	})

	t.Run("canonical proposer omits empty optional fields", func(t *testing.T) {
		a := baseJudgment()
		b := baseJudgment()
		b.Proposer.ActorID = ""
		b.Proposer.Model = ""
		b.Proposer.Reference = ""
		b.Proposer.Session = ""
		a.Proposer = core.Source{System: "sire", ActorKind: core.ActorKindAgent}
		if core.ComputeJudgmentHash(a) != core.ComputeJudgmentHash(b) {
			t.Error("empty optional proposer fields must be omitted from the canonical JSON")
		}
	})
}
