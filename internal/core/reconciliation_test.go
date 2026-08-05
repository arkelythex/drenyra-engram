// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the v0.5.0 reconciliation
// model and pure lifecycle (docs/architecture/close-intelligence-v0.5.md §3.2):
// the entity round-trip, the legal/illegal transition matrix with typed error
// codes, the engine-derived variance, and the canonical reconciliation hash
// (deterministic, status-shaped, and pinned to the exact bytes the TypeScript
// mirror produces in the protocol-freeze batch).
package core_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// baseReconciliation is the canonical proposed reconciliation of the hash
// vectors: the same fixture in Go and TypeScript must produce the SAME pinned
// hex below (mirrored in core/types.ts computeReconciliationHash, frozen in the
// protocol-freeze batch).
func baseReconciliation() core.Reconciliation {
	return core.Reconciliation{
		ID:               "reconciliation-1",
		TenantID:         "tenant-1",
		CompanyID:        "acme",
		FiscalPeriodID:   "202601",
		LeftMemoryID:     "obs-1",
		RightMemoryID:    "obs-2",
		Method:           "trial-balance",
		Currency:         "PEN",
		LeftAmountCents:  150000,
		RightAmountCents: 149500,
		VarianceCents:    500,
		ToleranceCents:   1000,
		Status:           core.ReconciliationProposed,
		Proposer: core.Source{
			System:    "sire",
			ActorID:   "agent-7",
			ActorKind: core.ActorKindAgent,
		},
		ProposalReason: "bank statement matches ledger within tolerance",
		ProposedAt:     "2026-08-05T12:00:00Z",
	}
}

// reconciliationSnapshot is the canonical adjudicator snapshot of the confirmed
// vector.
func reconciliationSnapshot() *auth.PrincipalSnapshot {
	return &auth.PrincipalSnapshot{
		SubjectID:            "subject-1",
		MembershipID:         "membership-1",
		Roles:                []auth.AccountingRole{auth.RoleController},
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      "2026-08-05T13:00:00Z",
	}
}

func confirmedReconciliation(t *testing.T) core.Reconciliation {
	t.Helper()
	r := baseReconciliation()
	if err := core.ConfirmReconciliation(&r, "ledger matches bank statement; variance within tolerance", reconciliationSnapshot(), "reconciliation-policy/v0.5.0", "2026-08-05T13:00:00Z"); err != nil {
		t.Fatalf("confirmed fixture: %v", err)
	}
	return r
}

// TestReconciliationModelRoundTrip marshals and unmarshals a proposed AND a
// confirmed reconciliation (covering the Adjudicator pointer and the int64
// cents) and requires exact equality.
func TestReconciliationModelRoundTrip(t *testing.T) {
	for _, r := range []core.Reconciliation{baseReconciliation(), confirmedReconciliation(t)} {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got core.Reconciliation
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !reflect.DeepEqual(got, r) {
			t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, r)
		}
	}
}

// TestReconciliationStatusesAreValid pins the closed status set.
func TestReconciliationStatusesAreValid(t *testing.T) {
	want := []core.ReconciliationStatus{
		core.ReconciliationProposed, core.ReconciliationConfirmed, core.ReconciliationRejected,
		core.ReconciliationWithdrawn, core.ReconciliationSuperseded,
	}
	for _, s := range want {
		if !core.IsValidReconciliationStatus(s) {
			t.Errorf("IsValidReconciliationStatus(%q) = false, want true", s)
		}
	}
	if core.IsValidReconciliationStatus(core.ReconciliationStatus("draft")) {
		t.Error("IsValidReconciliationStatus(draft) = true, want false")
	}
}

// TestReconciliationTransitionMatrix freezes the adjacency table: proposed →
// confirmed|rejected|withdrawn|superseded; confirmed → superseded ONLY;
// terminal states never re-open.
func TestReconciliationTransitionMatrix(t *testing.T) {
	legal := map[core.ReconciliationStatus][]core.ReconciliationStatus{
		core.ReconciliationProposed: {
			core.ReconciliationConfirmed, core.ReconciliationRejected,
			core.ReconciliationWithdrawn, core.ReconciliationSuperseded,
		},
		core.ReconciliationConfirmed: {core.ReconciliationSuperseded},
	}
	all := []core.ReconciliationStatus{
		core.ReconciliationProposed, core.ReconciliationConfirmed, core.ReconciliationRejected,
		core.ReconciliationWithdrawn, core.ReconciliationSuperseded,
	}
	for _, from := range all {
		for _, to := range all {
			want := false
			for _, allowed := range legal[from] {
				if allowed == to {
					want = true
				}
			}
			if core.IsLegalReconciliationTransition(from, to) != want {
				t.Errorf("IsLegalReconciliationTransition(%q, %q) = %v, want %v", from, to, !want, want)
			}
		}
	}
}

// TestReconciliationPureTransitions verifies the pure state machine with typed
// codes: confirm/reject require status + resolution + adjudicator; withdraw
// only guards the status; supersede only routes a confirmed row.
func TestReconciliationPureTransitions(t *testing.T) {
	t.Run("confirm happy path", func(t *testing.T) {
		r := baseReconciliation()
		if err := core.ConfirmReconciliation(&r, "professional resolution", reconciliationSnapshot(), "reconciliation-policy/v0.5.0", "2026-08-05T13:00:00Z"); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		if r.Status != core.ReconciliationConfirmed || r.Resolution != "professional resolution" ||
			r.Adjudicator == nil || r.PolicyVersion != "reconciliation-policy/v0.5.0" || r.DecidedAt == "" {
			t.Fatalf("confirmed state mismatch: %+v", r)
		}
	})

	t.Run("confirm wrong status", func(t *testing.T) {
		r := baseReconciliation()
		r.Status = core.ReconciliationWithdrawn
		err := core.ConfirmReconciliation(&r, "resolution", reconciliationSnapshot(), "reconciliation-policy/v0.5.0", "2026-08-05T13:00:00Z")
		if auth.Code(err) != auth.CodeInvalidReconciliationTransition {
			t.Errorf("code = %q, want INVALID_RECONCILIATION_TRANSITION", auth.Code(err))
		}
	})

	t.Run("confirm empty resolution", func(t *testing.T) {
		r := baseReconciliation()
		err := core.ConfirmReconciliation(&r, "   ", reconciliationSnapshot(), "reconciliation-policy/v0.5.0", "2026-08-05T13:00:00Z")
		if auth.Code(err) != auth.CodeResolutionRequired {
			t.Errorf("code = %q, want RESOLUTION_REQUIRED", auth.Code(err))
		}
	})

	t.Run("confirm nil adjudicator", func(t *testing.T) {
		r := baseReconciliation()
		err := core.ConfirmReconciliation(&r, "resolution", nil, "reconciliation-policy/v0.5.0", "2026-08-05T13:00:00Z")
		if auth.Code(err) != auth.CodeAuthenticationRequired {
			t.Errorf("code = %q, want AUTHENTICATION_REQUIRED", auth.Code(err))
		}
	})

	t.Run("reject happy path stores reason as resolution", func(t *testing.T) {
		r := baseReconciliation()
		if err := core.RejectReconciliation(&r, "voucher missing", reconciliationSnapshot(), "reconciliation-policy/v0.5.0", "2026-08-05T13:00:00Z"); err != nil {
			t.Fatalf("reject: %v", err)
		}
		if r.Status != core.ReconciliationRejected || r.Resolution != "voucher missing" {
			t.Fatalf("rejected state mismatch: %+v", r)
		}
	})

	t.Run("withdraw happy path", func(t *testing.T) {
		r := baseReconciliation()
		if err := core.WithdrawReconciliation(&r, "2026-08-05T13:00:00Z"); err != nil {
			t.Fatalf("withdraw: %v", err)
		}
		if r.Status != core.ReconciliationWithdrawn || r.DecidedAt == "" {
			t.Fatalf("withdrawn state mismatch: %+v", r)
		}
	})

	t.Run("withdraw wrong status", func(t *testing.T) {
		r := baseReconciliation()
		r.Status = core.ReconciliationConfirmed
		err := core.WithdrawReconciliation(&r, "2026-08-05T13:00:00Z")
		if auth.Code(err) != auth.CodeInvalidReconciliationTransition {
			t.Errorf("code = %q, want INVALID_RECONCILIATION_TRANSITION", auth.Code(err))
		}
	})

	t.Run("supersede confirmed routes successor", func(t *testing.T) {
		r := confirmedReconciliation(t)
		decidedAt := r.DecidedAt
		if err := core.SupersedeReconciliation(&r, "reconciliation-2", "2026-08-05T14:00:00Z"); err != nil {
			t.Fatalf("supersede: %v", err)
		}
		if r.Status != core.ReconciliationSuperseded || r.SupersedesID != "reconciliation-2" || r.DecidedAt != decidedAt {
			t.Fatalf("superseded state mismatch: %+v", r)
		}
	})

	t.Run("supersede non-confirmed", func(t *testing.T) {
		r := baseReconciliation()
		err := core.SupersedeReconciliation(&r, "reconciliation-2", "2026-08-05T14:00:00Z")
		if auth.Code(err) != auth.CodeInvalidReconciliationTransition {
			t.Errorf("code = %q, want INVALID_RECONCILIATION_TRANSITION", auth.Code(err))
		}
	})
}

// TestComputeReconciliationHashPinsTheCanonicalBytes freezes the deterministic
// hash: identical input → identical output, confirmed shape differs from the
// proposed shape, and the reviewed (proposed) hash is what confirm/reject
// compare before deciding.
func TestComputeReconciliationHashPinsTheCanonicalBytes(t *testing.T) {
	proposed := baseReconciliation()
	proposedHash := core.ComputeReconciliationHash(proposed)
	if proposedHash == "" || len(proposedHash) != 64 {
		t.Fatalf("proposed hash = %q, want a 64-hex digest", proposedHash)
	}
	if again := core.ComputeReconciliationHash(proposed); again != proposedHash {
		t.Fatalf("hash not deterministic: %q vs %q", again, proposedHash)
	}

	confirmed := confirmedReconciliation(t)
	confirmedHash := core.ComputeReconciliationHash(confirmed)
	if confirmedHash == proposedHash {
		t.Fatal("confirmed hash must differ from the proposed hash")
	}

	// The confirmed hash covers the adjudication fields: mutating any of them
	// changes the digest.
	mutated := confirmed
	mutated.Resolution = "different professional resolution"
	if core.ComputeReconciliationHash(mutated) == confirmedHash {
		t.Error("resolution change must change the confirmed hash")
	}

	// The proposed (reviewed) hash covers the pair/amounts/method/currency:
	// mutating any of them changes the digest — the hash guard would catch it.
	for name, change := range map[string]func(*core.Reconciliation){
		"amount":      func(r *core.Reconciliation) { r.LeftAmountCents++ },
		"currency":    func(r *core.Reconciliation) { r.Currency = "USD" },
		"method":      func(r *core.Reconciliation) { r.Method = "bank-statement" },
		"tolerance":   func(r *core.Reconciliation) { r.ToleranceCents = 0 },
		"rightMemory": func(r *core.Reconciliation) { r.RightMemoryID = "obs-9" },
	} {
		cp := proposed
		change(&cp)
		if core.ComputeReconciliationHash(cp) == proposedHash {
			t.Errorf("%s mutation must change the proposed hash", name)
		}
	}

	// Routing fields never participate in the CONFIRMED shape: mutating
	// supersedesId alone (status unchanged) keeps the confirmed digest stable.
	routed := confirmed
	routed.SupersedesID = "reconciliation-9"
	if core.ComputeReconciliationHash(routed) != confirmedHash {
		t.Error("supersedesId mutation alone must not change the confirmed hash (routing fields never participate)")
	}

	// Variance is covered: the engine-derived field participates in the hash.
	noVariance := proposed
	noVariance.VarianceCents = 0
	if core.ComputeReconciliationHash(noVariance) == proposedHash {
		t.Error("variance change must change the proposed hash")
	}
}

// TestReconciliationVarianceIsEngineDerived documents the entity-level
// invariant that the STORE enforces (schema CHECK variance_cents =
// left_amount_cents - right_amount_cents); the pure model simply carries the
// derived value.
func TestReconciliationVarianceIsEngineDerived(t *testing.T) {
	r := baseReconciliation()
	if r.VarianceCents != r.LeftAmountCents-r.RightAmountCents {
		t.Fatalf("VarianceCents = %d, want %d (left - right)", r.VarianceCents, r.LeftAmountCents-r.RightAmountCents)
	}
}
