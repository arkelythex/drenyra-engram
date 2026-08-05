// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the automatic MCP session
// context application service (v0.5.0 — docs/architecture/close-intelligence-v0.5.md
// §5): a PURE scope-first read. CurrentContextFor validates the EXACT company
// scope (fail closed — the service never infers a scope from database recency
// or any other signal), loads the period summary (whose closureState,
// latestClose and PendingItems come from the extended PeriodSummaryOutput —
// design §2.2), selects at most 20 recent chains (latest revision per chain,
// effectiveAt desc) and stamps the generation timestamp. It writes NOTHING.
package server

import (
	"context"
	"errors"
	"sort"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// maxRecentChains is the bounded size of the session context's recent-chains
// digest (design §5: "at most 20 recent chains").
const maxRecentChains = 20

// CurrentContextStore is the narrow store surface CurrentContextFor needs: the
// current memories of a scope (API.Context = FindByScope + latest-per-chain)
// and the explainable summary whose closureState/latestClose/PendingItems feed
// the context. *API satisfies it — the transports always call the shared
// domain service, never the store directly.
type CurrentContextStore interface {
	Context(scope core.Scope) ([]core.AccountingMemory, error)
	PeriodSummary(scope core.Scope) (PeriodSummaryOutput, error)
}

// CurrentContextFor builds the bounded session context for one EXACT company
// scope (design §5).
//
// Validation contract:
//   - the scope must be valid and an EXACT company scope with a YYYYMM period
//     (INVALID_SCOPE / INVALID_PERIOD — the session context never accepts an
//     institutional, unperioded or inferred scope; that is the fail-closed
//     line: no partial cross-scope data is ever assembled);
//   - the period summary is the single read source (its closure state comes
//     from the period_closures projection, its latest close from the close
//     chain and its pending items from the shared derivation);
//   - the recent chains are the latest revision per (topicKey, exact scope)
//     chain, ordered by effectiveAt DESC (most recent first; memory id breaks
//     ties deterministically) and bounded at 20;
//   - GeneratedAt is stamped from the UTC clock at read time.
func CurrentContextFor(ctx context.Context, store CurrentContextStore, scope core.Scope) (core.CurrentContext, error) {
	// 1. Exact scope — fail closed, never infer. An institutional scope is
	// valid shape but carries no company period; only an exact company scope
	// with a period can project a session context.
	if err := core.AssertValidScope(scope); err != nil {
		return core.CurrentContext{}, err
	}
	if scope.Kind != core.ScopeKindCompany || scope.Period == "" {
		return core.CurrentContext{}, errors.New("INVALID_PERIOD: the session context requires an exact company scope with a YYYYMM period")
	}

	// 2. The period summary is the single read source of the context's
	// aggregate (counts, closure state, latest close, pending-item digest). A
	// summary failure aborts the context.
	summary, err := store.PeriodSummary(scope)
	if err != nil {
		return core.CurrentContext{}, err
	}

	// 3. Recent chains: the latest revision per chain, ordered by effectiveAt
	// DESC and bounded at 20. The digest is the current-memory view of the
	// scope — exactly the chains a period summary narrates.
	current, err := store.Context(scope)
	if err != nil {
		return core.CurrentContext{}, err
	}
	recent := make([]core.RecentChain, 0, len(current))
	for _, memory := range current {
		recent = append(recent, core.RecentChain{
			TopicKey:    memory.Identity.TopicKey,
			MemoryID:    memory.Identity.ID,
			Kind:        string(memory.Kind),
			Status:      string(memory.Status),
			EffectiveAt: memory.EffectiveAt,
			Title:       memory.Title,
		})
	}
	sort.SliceStable(recent, func(i, j int) bool {
		if recent[i].EffectiveAt != recent[j].EffectiveAt {
			return recent[i].EffectiveAt > recent[j].EffectiveAt
		}
		return recent[i].MemoryID < recent[j].MemoryID
	})
	if len(recent) > maxRecentChains {
		recent = recent[:maxRecentChains]
	}

	// 4. The compact period summary (design §5) is the wire subset of the full
	// PeriodSummaryOutput: totals, the projection state and the latest close
	// memory id (empty when the period has no close memory).
	latestClose := ""
	if summary.LatestClose != nil {
		latestClose = summary.LatestClose.Identity.ID
	}
	return core.CurrentContext{
		Scope: scope,
		PeriodSummary: core.CurrentContextPeriodSummary{
			Total:        summary.Total,
			ByKind:       kindCounts(summary.ByKind),
			ByStatus:     statusCounts(summary.ByStatus),
			ClosureState: summary.ClosureState,
			LatestClose:  latestClose,
		},
		PendingItems: summary.PendingItems,
		RecentChains: recent,
		GeneratedAt:  nowISO(),
	}, nil
}
