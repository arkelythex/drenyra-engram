// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. These tests drive the automatic MCP session
// context service (v0.5.0, design §5): exact-scope validation (fail closed —
// the service never infers a scope), the period summary with closure state and
// latest close, the shared pending-item digest and the bounded at-most-20
// recent chains ordered by effectiveAt desc. No monetary fields cross the
// surface.

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// TestCurrentContextForValidScope seeds 25 fact chains plus a pending decision,
// an active obligation and an active exception in the demo scope and asserts
// the full context envelope: exact scope, summary totals/closure state, the
// shared pending-item digest and the at-most-20 recent chains ordered by
// effectiveAt desc.
func TestCurrentContextForValidScope(t *testing.T) {
	api := newTestAPI(t)
	for i := 0; i < 25; i++ {
		saveInScope(t, api, demoScope(), fmt.Sprintf("fact/t-%02d", i), "fact",
			fmt.Sprintf("Hecho %d", i), "contenido", fmt.Sprintf("2026-07-%02dT00:00:00Z", 1+i))
	}
	// A decision with a fiscal effect lands pending_review behind the gate.
	saveInScope(t, api, demoScope(), "decision/pendiente", "decision", "Decision pendiente", "ajuste", "2026-07-28T00:00:00Z")
	saveInScope(t, api, demoScope(), "obligation/igv-621", "obligation", "Obligacion", "declarar", "2026-07-20T00:00:00Z")
	saveInScope(t, api, demoScope(), "exception/banco", "exception", "Diferencia", "extracto", "2026-07-25T00:00:00Z")

	ctx, err := CurrentContextFor(context.Background(), api, demoScope())
	if err != nil {
		t.Fatalf("CurrentContextFor: %v", err)
	}
	if !core.ScopeEquals(ctx.Scope, demoScope()) {
		t.Fatalf("scope = %+v, want demoScope", ctx.Scope)
	}
	if ctx.PeriodSummary.Total != 28 {
		t.Fatalf("total = %d, want 28", ctx.PeriodSummary.Total)
	}
	if ctx.PeriodSummary.ByKind["fact"] != 25 || ctx.PeriodSummary.ByKind["decision"] != 1 {
		t.Fatalf("byKind = %v, want 25 facts and 1 decision", ctx.PeriodSummary.ByKind)
	}
	if ctx.PeriodSummary.ClosureState != string(core.ClosureStateOpen) {
		t.Fatalf("closureState = %q, want open", ctx.PeriodSummary.ClosureState)
	}
	if ctx.PeriodSummary.LatestClose != "" {
		t.Fatalf("latestClose = %q, want empty for an open period", ctx.PeriodSummary.LatestClose)
	}
	// The shared derivation: the pending decision + active obligation + active
	// exception, never the facts.
	if len(ctx.PendingItems) != 3 {
		t.Fatalf("pendingItems = %d, want 3 (decision + obligation + exception)", len(ctx.PendingItems))
	}
	if ctx.GeneratedAt == "" {
		t.Fatal("generatedAt must be stamped")
	}

	// The recent-chains digest is bounded at 20 and ordered effectiveAt desc.
	// EffectiveAt values: decision 07-28, exception + fact-24 07-25, obligation
	// + fact-19 07-20, facts 07-01..07-25. The 20 newest end at day 9.
	if len(ctx.RecentChains) != 20 {
		t.Fatalf("recentChains = %d, want the at-most-20 bound", len(ctx.RecentChains))
	}
	if ctx.RecentChains[0].EffectiveAt != "2026-07-28T00:00:00Z" {
		t.Fatalf("newest chain = %q, want 2026-07-28 (the decision)", ctx.RecentChains[0].EffectiveAt)
	}
	if ctx.RecentChains[19].EffectiveAt != "2026-07-09T00:00:00Z" {
		t.Fatalf("oldest kept chain = %q, want 2026-07-09 (the 20th newest)", ctx.RecentChains[19].EffectiveAt)
	}
	for i := 1; i < len(ctx.RecentChains); i++ {
		if ctx.RecentChains[i-1].EffectiveAt < ctx.RecentChains[i].EffectiveAt {
			t.Fatalf("recentChains not ordered effectiveAt desc at %d", i)
		}
	}
	for _, chain := range ctx.RecentChains {
		if chain.MemoryID == "" || chain.TopicKey == "" || chain.Kind == "" || chain.Status == "" || chain.Title == "" {
			t.Fatalf("recent chain missing fields: %+v", chain)
		}
	}
}

// TestCurrentContextForFailClosed proves the exact-scope contract: institutional
// scopes, unperioded company scopes and malformed scopes are all denied — the
// service never infers a scope and never assembles partial cross-scope data.
func TestCurrentContextForFailClosed(t *testing.T) {
	api := newTestAPI(t)

	t.Run("institutional scope denied", func(t *testing.T) {
		scope := core.Scope{Kind: core.ScopeKindInstitutional, OrganizationID: "org"}
		_, err := CurrentContextFor(context.Background(), api, scope)
		if err == nil || !strings.HasPrefix(err.Error(), "INVALID_PERIOD") {
			t.Fatalf("institutional scope = %v, want INVALID_PERIOD (never inferred)", err)
		}
	})

	t.Run("unperioded company scope denied", func(t *testing.T) {
		scope := demoScope()
		scope.Period = ""
		_, err := CurrentContextFor(context.Background(), api, scope)
		if err == nil || !strings.HasPrefix(err.Error(), "INVALID_PERIOD") {
			t.Fatalf("unperioded scope = %v, want INVALID_PERIOD", err)
		}
	})

	t.Run("malformed scope denied", func(t *testing.T) {
		scope := core.Scope{Kind: "bogus"}
		_, err := CurrentContextFor(context.Background(), api, scope)
		if err == nil || !strings.HasPrefix(err.Error(), "INVALID_SCOPE") {
			t.Fatalf("malformed scope = %v, want INVALID_SCOPE", err)
		}
	})
}

// TestCurrentContextForClosedPeriod seeds a controller-approved close and
// asserts the context reflects the period_closures projection: closureState
// closed, latestClose the close memory id and the pending-item digest never
// containing the close itself.
func TestCurrentContextForClosedPeriod(t *testing.T) {
	api := closeAcceptanceStore(t)
	token := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleController})
	sourceFact := seedJulyPeriod(t, api)
	closeMemory := createJulyClose(t, api, sourceFact, "cierre julio")
	principal := resolvePrincipal(t, api, token)
	if _, err := ApproveMemory(context.Background(), api.Store.(ApprovalStore), authz.NewApprovalPolicy(), core.ApproveMemoryCommand{
		MemoryID:             closeMemory.Identity.ID,
		ExpectedEnvelopeHash: core.ComputeEnvelopeHash(closeMemory),
		Reason:               "cierre revisado",
		RequestID:            "req-ctx-close",
	}, principal); err != nil {
		t.Fatalf("controller approval: %v", err)
	}

	ctx, err := CurrentContextFor(context.Background(), api, demoScope())
	if err != nil {
		t.Fatalf("CurrentContextFor: %v", err)
	}
	if ctx.PeriodSummary.ClosureState != string(core.ClosureStateClosed) {
		t.Fatalf("closureState = %q, want closed", ctx.PeriodSummary.ClosureState)
	}
	if ctx.PeriodSummary.LatestClose != closeMemory.Identity.ID {
		t.Fatalf("latestClose = %q, want %s", ctx.PeriodSummary.LatestClose, closeMemory.Identity.ID)
	}
	// The seeded period carries the pending decision, obligation and exception —
	// the close is disclosed in latestClose, never listed as a pending item.
	if len(ctx.PendingItems) != 3 {
		t.Fatalf("pendingItems = %d, want 3 for the seeded period", len(ctx.PendingItems))
	}
	for _, item := range ctx.PendingItems {
		if item.MemoryID == closeMemory.Identity.ID {
			t.Fatal("the close memory must never be a pending item")
		}
	}
}
