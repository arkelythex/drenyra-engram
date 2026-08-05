// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. These tests drive the period-over-period
// comparison SERVICE (v0.5.0, design §4): validation of the two exact company
// scopes, loading both periods' current memories + pending-item digests, close
// state and the deterministic delta. The fixtures reuse the close acceptance
// seeds (seedJulyPeriod) plus an August period, so the from/to scenario covers
// added/removed/changed chains, an approved-between-periods status change, the
// pending delta and a closed-vs-open close state.

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// saveInScope saves one memory in an arbitrary exact scope (the fixture period
// helpers save into the fixed July demoScope; comparison needs two periods).
func saveInScope(t *testing.T, api *API, scope core.Scope, topicKey, kind, title, what, effectiveAt string) core.AccountingMemory {
	t.Helper()
	effect := core.FiscalEffectNone
	if kind == string(core.KindDecision) {
		effect = core.FiscalEffectAdjustment
	}
	result, err := api.Save(core.SaveInput{
		TopicKey:     topicKey,
		Title:        title,
		Kind:         core.MemoryKind(kind),
		Scope:        scope,
		Content:      core.Content{What: what, Why: "fixture", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: effect,
		EffectiveAt:  effectiveAt,
		Source:       testAgentSource,
	})
	if err != nil {
		t.Fatalf("save fixture %q in period %s: %v", topicKey, scope.Period, err)
	}
	return result.Memory
}

// augustScope is the August 2026 twin of demoScope (same tenant/company/RUC).
func augustScope() core.Scope {
	scope := demoScope()
	scope.Period = "202608"
	return scope
}

// seedCompareScenario seeds the July acceptance fixtures and an August twin with:
//   - a NEW chain (account/4011/ventas-agosto);
//   - a CHANGED chain (fact/igv-tasa — new content);
//   - an UNCHANGED chain (obligation/igv-621 — same topic and content);
//   - a STATUS-CHANGED chain (adjust/aj-001 — approved by a human between
//     periods, same canonical content);
//   - REMOVED chains (account/4011/ventas-julio and exception/banco-001 —
//     July only).
func seedCompareScenario(t *testing.T, api *API) {
	t.Helper()
	seedJulyPeriod(t, api)
	august := augustScope()
	saveInScope(t, api, august, "account/4011/ventas-agosto", "fact", "Ventas agosto", "ventas de agosto", "2026-08-10T00:00:00Z")
	// fact/igv-tasa CHANGES content between periods (18% → 18.5%).
	saveInScope(t, api, august, "fact/igv-tasa", "fact", "Tasa IGV", "tasa vigente 18.5%", "2026-08-05T00:00:00Z")
	// The obligation CARRIES OVER unchanged: same canonical content, same
	// effectiveAt (the July 20 due date) — only the scope period differs.
	saveInScope(t, api, august, "obligation/igv-621", "obligation", "Obligacion PDT 621", "declarar IGV julio", "2026-07-20T00:00:00Z")
	adjustment := saveInScope(t, api, august, "adjust/aj-001", "decision", "Ajuste AJ-001", "ajuste por comprobante tardio", "2026-08-15T00:00:00Z")
	// The human approves the adjustment between periods: pending_review → approved.
	if _, err := api.Approve(adjustment.Identity.ID, core.Source{
		System: "test", ActorID: "jefe.contabilidad", ActorKind: core.ActorKindHuman,
	}); err != nil {
		t.Fatalf("approve august adjustment: %v", err)
	}
}

// TestComparePeriodsDeterministicDeltas covers the design §9.5 acceptance delta:
// added/removed/changed chains, the status change reported separately, the
// per-kind/per-status count deltas and the pending-item delta.
func TestComparePeriodsDeterministicDeltas(t *testing.T) {
	api := closeAcceptanceStore(t)
	seedCompareScenario(t, api)

	got, err := ComparePeriods(context.Background(), api, demoScope(), augustScope())
	if err != nil {
		t.Fatalf("compare: %v", err)
	}

	if got.From != "202607" || got.To != "202608" {
		t.Fatalf("periods = %q/%q, want 202607/202608", got.From, got.To)
	}
	// July 5 chains, August 4 (1 removed, 1 added).
	if got.Counts.FromTotal != 5 || got.Counts.ToTotal != 4 || got.Counts.Delta != -1 {
		t.Fatalf("counts = %+v, want fromTotal 5, toTotal 4, delta -1", got.Counts)
	}
	if got.Counts.ByKindDelta["fact"] != 0 || got.Counts.ByKindDelta["exception"] != -1 ||
		got.Counts.ByKindDelta["decision"] != 0 || got.Counts.ByKindDelta["obligation"] != 0 {
		t.Fatalf("byKindDelta = %v, want fact 0, decision 0, obligation 0, exception -1", got.Counts.ByKindDelta)
	}
	if got.Counts.ByStatusDelta["active"] != -1 || got.Counts.ByStatusDelta["pending_review"] != -1 ||
		got.Counts.ByStatusDelta["approved"] != 1 {
		t.Fatalf("byStatusDelta = %v, want active -1, pending_review -1, approved +1", got.Counts.ByStatusDelta)
	}

	if len(got.Chains.New) != 1 || got.Chains.New[0].TopicKey != "account/4011/ventas-agosto" {
		t.Fatalf("new = %+v, want exactly account/4011/ventas-agosto", got.Chains.New)
	}
	// Removed chains: the July sales fact chain and the exception (both exist only
	// in July), stable-sorted by topic key.
	if len(got.Chains.Removed) != 2 ||
		got.Chains.Removed[0].TopicKey != "account/4011/ventas-julio" ||
		got.Chains.Removed[1].TopicKey != "exception/banco-001" {
		t.Fatalf("removed = %+v, want account/4011/ventas-julio then exception/banco-001", got.Chains.Removed)
	}
	// Changed, stable-sorted by topic key: adjust/aj-001 (status) then
	// fact/igv-tasa (content).
	if len(got.Chains.Changed) != 2 ||
		got.Chains.Changed[0].TopicKey != "adjust/aj-001" ||
		got.Chains.Changed[1].TopicKey != "fact/igv-tasa" {
		t.Fatalf("changed = %+v, want adjust/aj-001 then fact/igv-tasa", got.Chains.Changed)
	}
	if got.Chains.UnchangedCount != 1 {
		t.Fatalf("unchangedCount = %d, want 1 (obligation/igv-621)", got.Chains.UnchangedCount)
	}

	// Status changes are reported SEPARATELY (design §4): the approved-between-
	// periods adjustment.
	if len(got.StatusChanges) != 1 {
		t.Fatalf("statusChanges = %+v, want exactly one", got.StatusChanges)
	}
	sc := got.StatusChanges[0]
	if sc.TopicKey != "adjust/aj-001" || sc.FromStatus != "pending_review" || sc.ToStatus != "approved" {
		t.Fatalf("statusChange = %+v, want adjust/aj-001 pending_review → approved", sc)
	}

	// Pending delta: July digest = adjustment (pending_review) + obligation +
	// exception = 3; August digest = obligation only (adjustment approved,
	// exception removed) = 1.
	if got.PendingItems.From != 3 || got.PendingItems.To != 1 || got.PendingItems.Delta != -2 {
		t.Fatalf("pendingItems counts = %+v, want from 3, to 1, delta -2", got.PendingItems)
	}
	if len(got.PendingItems.AddedIDs) != 0 {
		t.Fatalf("addedIds = %v, want none (the august pending item was also pending in july)", got.PendingItems.AddedIDs)
	}
	if len(got.PendingItems.ResolvedIDs) != 2 {
		t.Fatalf("resolvedIds = %v, want the adjustment and the exception", got.PendingItems.ResolvedIDs)
	}

	if got.CloseState.From != "open" || got.CloseState.To != "open" {
		t.Fatalf("closeState = %+v, want open/open", got.CloseState)
	}
}

// TestComparePeriodsClosedVsOpen proves the close state surface: a CLOSED July
// (approved close projects 'closed') vs an OPEN August.
func TestComparePeriodsClosedVsOpen(t *testing.T) {
	api := closeAcceptanceStore(t)
	controllerToken := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleController})
	sourceFact := seedJulyPeriod(t, api)
	closeMemory := createJulyClose(t, api, sourceFact, "cierre de julio")
	principal := resolvePrincipal(t, api, controllerToken)
	if _, err := ApproveMemory(context.Background(), api.Store.(ApprovalStore), authz.NewApprovalPolicy(), core.ApproveMemoryCommand{
		MemoryID:             closeMemory.Identity.ID,
		ExpectedEnvelopeHash: core.ComputeEnvelopeHash(closeMemory),
		Reason:               "cierre revisado y conforme",
		RequestID:            "req-compare-close",
	}, principal); err != nil {
		t.Fatalf("approve july close: %v", err)
	}
	if closure, ok := api.FindPeriodClosure(demoScope()); !ok || closure.Status != "closed" {
		t.Fatalf("july closure = (%v, %v), want closed", closure, ok)
	}

	got, err := ComparePeriods(context.Background(), api, demoScope(), augustScope())
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if got.CloseState.From != "closed" || got.CloseState.To != "open" {
		t.Fatalf("closeState = %+v, want closed/open", got.CloseState)
	}
}

// TestComparePeriodsInvalidScopes covers the typed validation contract: bad
// periods, equal periods, non-company scopes and cross-company/tenant pairs.
func TestComparePeriodsInvalidScopes(t *testing.T) {
	api := closeAcceptanceStore(t)
	seedJulyPeriod(t, api)
	august := augustScope()

	otherCompany := august
	otherCompany.CompanyID = "cmp_02"
	otherCompany.RUC = "20100039201"

	otherTenant := august
	otherTenant.OrganizationID = "other_org"

	cases := []struct {
		name       string
		from, to   core.Scope
		wantCode   string
		wantPrefix string
	}{
		{name: "bad from period", from: func() core.Scope { s := demoScope(); s.Period = "20261"; return s }(), to: august, wantPrefix: "INVALID_PERIOD"},
		{name: "bad to period month", from: demoScope(), to: func() core.Scope { s := augustScope(); s.Period = "202613"; return s }(), wantPrefix: "INVALID_PERIOD"},
		{name: "same period", from: demoScope(), to: demoScope(), wantPrefix: "INVALID_PERIOD"},
		{name: "unperioded from scope", from: core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "cmp_org", CompanyID: "cmp_01", RUC: "20601234567"}, to: august, wantPrefix: "INVALID_PERIOD"},
		{name: "institutional scope", from: core.Scope{Kind: core.ScopeKindInstitutional}, to: august, wantPrefix: "INVALID_PERIOD"},
		{name: "cross company", from: demoScope(), to: otherCompany, wantCode: auth.CodeCompanyScopeDenied},
		{name: "cross tenant", from: demoScope(), to: otherTenant, wantCode: auth.CodeCompanyScopeDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ComparePeriods(context.Background(), api, tc.from, tc.to)
			if err == nil {
				t.Fatal("compare must fail for an invalid scope pair")
			}
			if tc.wantCode != "" {
				if auth.Code(err) != tc.wantCode {
					t.Fatalf("error code = %q, want %q (err: %v)", auth.Code(err), tc.wantCode, err)
				}
				return
			}
			if !strings.HasPrefix(err.Error(), tc.wantPrefix) {
				t.Fatalf("error = %q, want prefix %q", err.Error(), tc.wantPrefix)
			}
		})
	}
}

// TestComparePeriodsDeterminism runs the service twice over the same data and
// asserts byte-identical JSON (deterministic deltas, design §9.5).
func TestComparePeriodsDeterminism(t *testing.T) {
	api := closeAcceptanceStore(t)
	seedCompareScenario(t, api)

	first, err := ComparePeriods(context.Background(), api, demoScope(), augustScope())
	if err != nil {
		t.Fatalf("compare (first): %v", err)
	}
	second, err := ComparePeriods(context.Background(), api, demoScope(), augustScope())
	if err != nil {
		t.Fatalf("compare (second): %v", err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("determinism violated:\nfirst  %s\nsecond %s", firstJSON, secondJSON)
	}
}

// TestComparePeriodsEmptyPeriods proves the service handles two empty periods
// (valid scopes, no memories) as a deterministic zero delta — a comparison never
// fails on absence of data.
func TestComparePeriodsEmptyPeriods(t *testing.T) {
	api := closeAcceptanceStore(t)
	got, err := ComparePeriods(context.Background(), api, demoScope(), augustScope())
	if err != nil {
		t.Fatalf("compare empty periods: %v", err)
	}
	if got.Counts.FromTotal != 0 || got.Counts.ToTotal != 0 || got.Chains.UnchangedCount != 0 {
		t.Fatalf("empty comparison = %+v, want zero counts", got.Counts)
	}
	if got.CloseState.From != "open" || got.CloseState.To != "open" {
		t.Fatalf("closeState = %+v, want open/open", got.CloseState)
	}
}
