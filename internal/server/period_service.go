// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the period-over-period
// comparison application service (v0.5.0 — docs/architecture/close-intelligence-v0.5.md
// §4): a PURE scope-first read. ComparePeriods validates both exact company
// scopes (same tenant/company/RUC, distinct valid YYYYMM periods), loads each
// period's current memories (FindByScope + latest-per-chain) and the shared
// pending-item digest, reads the period_closures projection and delegates the
// deterministic delta computation to core.ComputePeriodComparison. It writes
// NOTHING and adds no schema — the comparison is a view, never a mutation.
package server

import (
	"context"
	"errors"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// PeriodCompareStore is the narrow store surface ComparePeriods needs: the
// current memories of a scope (API.Context = FindByScope + latest-per-chain),
// the explainable summary whose PendingItems digest feeds the pending delta, and
// the period_closures projection for the close state. *API satisfies it — the
// transports always call the shared domain service, never the store directly.
type PeriodCompareStore interface {
	Context(scope core.Scope) ([]core.AccountingMemory, error)
	PeriodSummary(scope core.Scope) (PeriodSummaryOutput, error)
	FindPeriodClosure(scope core.Scope) (store.PeriodClosureRecord, bool)
}

// ComparePeriods is the period-over-period comparison service (design §4).
//
// Validation contract:
//   - both scopes must be EXACT company scopes with valid YYYYMM periods
//     (INVALID_PERIOD / INVALID_RUC / INVALID_SCOPE — 400 on the transports);
//   - the two scopes must share tenant, company and RUC (COMPANY_SCOPE_DENIED —
//     403 on the transports; a comparison across companies would mix two
//     institutions' chains under the same topic keys);
//   - the periods must be DISTINCT (INVALID_PERIOD — 400).
//
// Chain matching is by topic key after the exact scope is stripped (the scopes
// differ only by period, so one topic key identifies one chain per period).
// `changed` chains and `statusChanges` are computed by the pure core delta; the
// pending-item digest reuses the close foundation's shared derivation
// (deriveClosePendingItems via PeriodSummary.PendingItems), so the comparison
// and the close snapshot can never diverge on what "pending" means.
func ComparePeriods(ctx context.Context, store PeriodCompareStore, fromScope, toScope core.Scope) (core.PeriodComparison, error) {
	// 1. Both scopes must be exact company scopes with valid YYYYMM periods.
	if err := core.AssertValidScope(fromScope); err != nil {
		return core.PeriodComparison{}, err
	}
	if err := core.AssertValidScope(toScope); err != nil {
		return core.PeriodComparison{}, err
	}
	if fromScope.Kind != core.ScopeKindCompany || fromScope.Period == "" {
		return core.PeriodComparison{}, errors.New("INVALID_PERIOD: the from scope must be an exact company scope with a YYYYMM period")
	}
	if toScope.Kind != core.ScopeKindCompany || toScope.Period == "" {
		return core.PeriodComparison{}, errors.New("INVALID_PERIOD: the to scope must be an exact company scope with a YYYYMM period")
	}
	if fromScope.Period == toScope.Period {
		return core.PeriodComparison{}, errors.New("INVALID_PERIOD: the from and to periods must be distinct")
	}

	// 2. Same tenant, company and RUC (the comparison never crosses companies).
	if fromScope.OrganizationID != toScope.OrganizationID ||
		fromScope.CompanyID != toScope.CompanyID ||
		fromScope.RUC != toScope.RUC {
		return core.PeriodComparison{}, auth.New(auth.CodeCompanyScopeDenied,
			"COMPANY_SCOPE_DENIED: comparison requires two exact scopes of the same tenant, company and RUC")
	}

	// 3. Current memories per period (FindByScope + latest-per-chain — the exact
	// chains a period summary narrates).
	fromCurrent, err := store.Context(fromScope)
	if err != nil {
		return core.PeriodComparison{}, err
	}
	toCurrent, err := store.Context(toScope)
	if err != nil {
		return core.PeriodComparison{}, err
	}

	// 4. Pending-item digests through the shared close derivation
	// (PeriodSummary.PendingItems is deriveClosePendingItems output — the same
	// frozen list CreateClose embeds in the CloseSnapshot).
	fromSummary, err := store.PeriodSummary(fromScope)
	if err != nil {
		return core.PeriodComparison{}, err
	}
	toSummary, err := store.PeriodSummary(toScope)
	if err != nil {
		return core.PeriodComparison{}, err
	}

	// 5. Close state per period (period_closures projection; "open" when the
	// period was never closed).
	fromClose := string(core.ClosureStateOpen)
	if closure, ok := store.FindPeriodClosure(fromScope); ok {
		fromClose = closure.Status
	}
	toClose := string(core.ClosureStateOpen)
	if closure, ok := store.FindPeriodClosure(toScope); ok {
		toClose = closure.Status
	}

	// 6. The pure deterministic delta (no store, no clock).
	return core.ComputePeriodComparison(fromScope.Period, toScope.Period,
		fromCurrent, toCurrent, fromSummary.PendingItems, toSummary.PendingItems,
		fromClose, toClose), nil
}
