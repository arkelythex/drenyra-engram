// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the monthly-close application
// service (v0.5.0 — docs/architecture/close-intelligence-v0.5.md §2.1/§2.3).
//
// CreateClose is the CANONICAL close-creation path: HTTP/MCP/CLI must never
// construct arbitrary closing memories through generic save. It validates the
// exact company scope and YYYYMM period, fixes the topic closing/CIERRE-<period>,
// rejects another current close for that scope, derives + freezes the pending
// items from the existing PeriodSummary, validates the caller-supplied monetary
// totals and their same-scope source memories, builds the CloseSnapshot with the
// canonical self-hash and saves through normal immutable Store.Save (the memory
// lands pending_review behind the human gate — the APPROVAL is the authenticated
// controller act, never this path).
//
// ReopenPeriod is the explicit authenticated controller reopen: the service
// validates command syntax and the verified principal, then delegates the WHOLE
// state change (BEGIN IMMEDIATE, (tenant, requestId) idempotency, exact-scope and
// expected-close guards, controller policy, projection flip, immutable closure
// event, memory_reopened receipt) to ONE atomic store operation. There is NO
// FindPeriodClosure + mutate composition anywhere — that split is a TOCTOU hole.
package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// CloseServiceStore is the narrow store surface CreateClose needs. The *API
// satisfies it (PeriodSummary + the store's Save/GetByTopic/Get wrapped behind
// the shared domain service).
type CloseServiceStore interface {
	PeriodSummary(scope core.Scope) (PeriodSummaryOutput, error)
	Save(input core.SaveInput) (core.WriteResult, error)
	GetByTopic(topicKey string, scope core.Scope) (core.AccountingMemory, error)
	Get(id string) (core.AccountingMemory, error)
	FindPeriodClosure(scope core.Scope) (store.PeriodClosureRecord, bool)
}

// ReopenStore is the narrow store surface the reopen service delegates to. The
// SQLiteStore satisfies it; the whole reopen is ONE store operation.
type ReopenStore interface {
	ReopenPeriod(ctx context.Context, cmd core.ReopenPeriodCommand, principal auth.VerifiedApprovalPrincipal, policy authz.ApprovalAuthorizationPolicy) (core.ReopenPeriodResult, error)
}

// CreateClose is the canonical monthly-close creation service (design §2.1). It
// returns the created pending_review close memory. The money is never derived
// from prose: every caller-supplied total must carry at least one SAME-SCOPE
// source memory, verified here before the snapshot is frozen.
func CreateClose(ctx context.Context, store CloseServiceStore, scope core.Scope, input core.CreateCloseInput) (core.AccountingMemory, error) {
	// 1. Exact company scope + valid YYYYMM period. The scope is the single
	// source of the company tuple; the input period must be the same tuple
	// element (they are one scope).
	if err := core.AssertValidScope(scope); err != nil {
		return core.AccountingMemory{}, err
	}
	if scope.Kind != core.ScopeKindCompany || scope.Period == "" {
		return core.AccountingMemory{}, errors.New("INVALID_PERIOD: a monthly close requires an exact company scope with a YYYYMM period")
	}
	if !core.IsValidPeriod(scope.Period) {
		return core.AccountingMemory{}, fmt.Errorf("INVALID_PERIOD: expected YYYYMM (six digits, month 01-12), got %q", scope.Period)
	}
	if strings.TrimSpace(input.Period) == "" {
		return core.AccountingMemory{}, errors.New("INVALID_PERIOD: the input period is required")
	}
	if input.Period != scope.Period {
		return core.AccountingMemory{}, auth.New(auth.CodeInvalidTransition, "input period does not match the scope period")
	}

	// 2. The topic key is CANONICAL — closing/CIERRE-<period> (design §2.1,
	// never caller-chosen). Reject another CURRENT close for the scope: a
	// pending_review close is always the canonical one; an approved close is
	// rejected UNLESS the period was explicitly reopened (a reopened period
	// admits a NEW close revision that re-closes it — the §2.3 correction path).
	topicKey := core.CloseTopicPrefix + scope.Period
	if existing, err := store.GetByTopic(topicKey, scope); err == nil {
		switch existing.Status {
		case core.StatusPendingReview:
			return core.AccountingMemory{}, auth.New(auth.CodePeriodAlreadyClosed,
				fmt.Sprintf("period %s already has a close under review (memory %s); approve or reject it before creating another", scope.Period, existing.Identity.ID))
		case core.StatusApproved:
			closure, ok := store.FindPeriodClosure(scope)
			if !ok || closure.Status != string(core.ClosureStateReopened) {
				return core.AccountingMemory{}, auth.New(auth.CodePeriodAlreadyClosed,
					fmt.Sprintf("period %s is closed by close memory %s; reopen the period explicitly before creating a new close", scope.Period, existing.Identity.ID))
			}
			// reopened: the new revision re-closes the period on approval.
		}
	} else if !IsNotFound(err) {
		return core.AccountingMemory{}, err
	}

	// 3. The period summary is the single read source of the close envelope
	// (counts, pending items, narrative). A summary failure aborts the close.
	summary, err := store.PeriodSummary(scope)
	if err != nil {
		return core.AccountingMemory{}, err
	}

	// 4. Derive + freeze the pending items (design §2.2): every pending_review
	// memory plus active/pending/approved obligations and exceptions, deduped by
	// memory ID, excluding the close being created, sorted by kind/effectiveAt/
	// memoryId. Pending items do not prevent close approval — they disclose close
	// state.
	pending := deriveClosePendingItems(summary, scope.Period)

	// 5. Validate the caller-supplied monetary totals (design §2.1): each total
	// requires a code, a currency and at least one SAME-SCOPE source memory. The
	// engine never derives money from prose — a total without a verifiable
	// same-scope source is rejected, not guessed.
	for i, total := range input.Totals {
		if strings.TrimSpace(total.Code) == "" {
			return core.AccountingMemory{}, fmt.Errorf("INVALID_TOTAL: total %d has an empty code", i)
		}
		if !isCurrencyCode(total.Currency) {
			return core.AccountingMemory{}, fmt.Errorf("INVALID_TOTAL: total %q currency %q is not a 3-letter ISO 4217 code", total.Code, total.Currency)
		}
		if len(total.SourceMemoryIDs) == 0 {
			return core.AccountingMemory{}, fmt.Errorf("INVALID_TOTAL: total %q requires at least one same-scope source memory id", total.Code)
		}
		for _, sourceID := range total.SourceMemoryIDs {
			if strings.TrimSpace(sourceID) == "" {
				return core.AccountingMemory{}, fmt.Errorf("INVALID_TOTAL: total %q carries an empty source memory id", total.Code)
			}
			source, err := store.Get(sourceID)
			if err != nil {
				return core.AccountingMemory{}, fmt.Errorf("INVALID_TOTAL: total %q source memory %s is not found: %v", total.Code, sourceID, err)
			}
			if !core.ScopeEquals(source.Scope, scope) {
				return core.AccountingMemory{}, fmt.Errorf("INVALID_TOTAL: total %q source memory %s is outside the close scope (same-scope sources only)", total.Code, sourceID)
			}
		}
	}

	// 6. Build the frozen CloseSnapshot. The SummaryHash is the self-hash of the
	// canonical bytes: it is computed over the snapshot with summaryHash="" (the
	// field participates in the bytes as empty at hash time), then stamped.
	now := nowISO()
	snapshot := core.CloseSnapshot{
		Period:      scope.Period,
		GeneratedAt: now,
		Counts: core.CloseCounts{
			Total:    summary.Total,
			ByKind:   kindCounts(summary.ByKind),
			ByStatus: statusCounts(summary.ByStatus),
		},
		Totals:             cloneCloseTotals(input.Totals),
		PendingItems:       pending,
		Reconciliation:     core.CloseReconciliation{Proposed: 0, Confirmed: 0, Rejected: 0},
		NarrativeMemoryIDs: narrativeMemoryIDs(summary),
	}
	// The snapshot reflects the PERIOD STATE at close-creation time (the
	// summary exactly — the close being created is NOT counted; it documents
	// what is being closed, pending items excluded).
	snapshot.SummaryHash = core.CloseSnapshotSummaryHash(&snapshot)

	// 7. The close memory content (design §2.1): What carries the period, counts,
	// totals and pending-item digest; Why the rationale and policy basis; Where
	// the company/RUC, period, source systems and source memory ids; Learned the
	// unresolved risks and follow-up. The CloseSnapshot is the structured truth;
	// this text is the explainable mirror.
	effectiveAt, err := core.MonthEndUTC(scope.Period)
	if err != nil {
		return core.AccountingMemory{}, err
	}
	content := closeContent(scope, summary, input, pending)

	result, err := store.Save(core.SaveInput{
		TopicKey:      topicKey,
		Title:         fmt.Sprintf("Cierre mensual %s", scope.Period),
		Kind:          core.KindSummary,
		Scope:         scope,
		Content:       content,
		FiscalEffect:  core.FiscalEffectClosing,
		EffectiveAt:   effectiveAt,
		Source:        input.Source,
		CloseSnapshot: &snapshot,
		// Confidence is a REQUIRED field (sdd-060-confidence-required,
		// FR-CN-3): a close summary is an automated computation with
		// deterministic inputs, scored high.
		Confidence: 0.9,
	})
	if err != nil {
		return core.AccountingMemory{}, err
	}
	return result.Memory, nil
}

// ReopenPeriod validates the reopen command and the verified principal, then
// delegates the whole authenticated reopen (idempotency, guards, controller
// policy, projection flip, immutable event, receipt) to ONE atomic store
// operation. The service never touches the approved close memory.
func ReopenPeriod(ctx context.Context, store ReopenStore, policy authz.ApprovalAuthorizationPolicy, scope core.Scope, expectedCloseMemoryID, reason, requestID string, principal auth.VerifiedApprovalPrincipal) (core.ReopenPeriodResult, error) {
	// A zero principal cannot reopen anything. There is no struct literal or
	// caller-declared-claims constructor for a verified principal; the zero value
	// fails closed here (the policy would otherwise misreport it as a scope error).
	if strings.TrimSpace(principal.SubjectID()) == "" && principal.AuthenticationMethod() == "" {
		return core.ReopenPeriodResult{}, auth.New(auth.CodePrincipalInvalid, "no verified approval principal present")
	}
	// Command syntax — malformed commands are identity/validation failures, never
	// authorization decisions.
	if err := core.AssertValidScope(scope); err != nil {
		return core.ReopenPeriodResult{}, err
	}
	if scope.Kind != core.ScopeKindCompany || scope.Period == "" {
		return core.ReopenPeriodResult{}, errors.New("INVALID_PERIOD: reopening requires an exact company scope with a YYYYMM period")
	}
	if strings.TrimSpace(expectedCloseMemoryID) == "" {
		return core.ReopenPeriodResult{}, auth.New(auth.CodeMemoryNotFound, "expectedCloseMemoryId is required")
	}
	if strings.TrimSpace(requestID) == "" {
		return core.ReopenPeriodResult{}, auth.New(auth.CodeMemoryNotFound, "requestId (idempotency key) is required")
	}
	if strings.TrimSpace(reason) == "" {
		return core.ReopenPeriodResult{}, auth.New(auth.CodeReasonRequired, "a reason is required for reopening a period")
	}
	return store.ReopenPeriod(ctx, core.ReopenPeriodCommand{
		Scope:                 scope,
		ExpectedCloseMemoryID: expectedCloseMemoryID,
		Reason:                reason,
		RequestID:             requestID,
	}, principal, policy)
}

// deriveClosePendingItems is the shared pending-item derivation (design §2.2):
// the union of the summary's pending_review memories, active/pending/approved
// obligations and active/pending/approved exceptions, deduplicated by memory ID,
// excluding the close being created (its topic is closing/CIERRE-<period>), and
// stable-sorted by kind, effectiveAt, memoryId. Both CreateClose's frozen list
// and the extended PeriodSummaryOutput use it, so the two views never diverge.
func deriveClosePendingItems(summary PeriodSummaryOutput, period string) []core.ClosePendingItem {
	seen := make(map[string]struct{})
	items := make([]core.ClosePendingItem, 0,
		len(summary.PendingApprovals)+len(summary.ActiveObligations)+len(summary.ActiveExceptions))
	add := func(m core.AccountingMemory) {
		if strings.HasPrefix(m.Identity.TopicKey, core.CloseTopicPrefix) {
			return // the close being created (or any close revision) is never a pending item
		}
		if _, ok := seen[m.Identity.ID]; ok {
			return
		}
		seen[m.Identity.ID] = struct{}{}
		items = append(items, core.ClosePendingItem{
			MemoryID:    m.Identity.ID,
			TopicKey:    m.Identity.TopicKey,
			Kind:        string(m.Kind),
			Status:      string(m.Status),
			Title:       m.Title,
			EffectiveAt: m.EffectiveAt,
		})
	}
	for _, m := range summary.PendingApprovals {
		add(m)
	}
	for _, m := range summary.ActiveObligations {
		add(m)
	}
	for _, m := range summary.ActiveExceptions {
		add(m)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.EffectiveAt != b.EffectiveAt {
			return a.EffectiveAt < b.EffectiveAt
		}
		return a.MemoryID < b.MemoryID
	})
	return items
}

// kindCounts converts the summary's per-kind counts to the snapshot's string-keyed
// map (the canonical snapshot model uses string keys).
func kindCounts(in map[core.MemoryKind]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[string(k)] = v
	}
	return out
}

// statusCounts converts the summary's per-status counts to the snapshot's
// string-keyed map.
func statusCounts(in map[core.MemoryStatus]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[string(k)] = v
	}
	return out
}

// narrativeMemoryIDs extracts the memory ids of the period's explainable
// narrative in effectiveAt order (the summary already orders them).
func narrativeMemoryIDs(summary PeriodSummaryOutput) []string {
	ids := make([]string, 0, len(summary.Narrative))
	for _, item := range summary.Narrative {
		ids = append(ids, item.MemoryID)
	}
	return ids
}

// cloneCloseTotals returns a defensive copy of the validated totals (source ids
// are copied so the frozen snapshot never aliases caller memory).
func cloneCloseTotals(in []core.CloseTotal) []core.CloseTotal {
	out := make([]core.CloseTotal, len(in))
	for i, t := range in {
		out[i] = t
		out[i].SourceMemoryIDs = append([]string(nil), t.SourceMemoryIDs...)
	}
	return out
}

// isCurrencyCode reports whether currency is a 3-letter ISO 4217-style code
// (uppercase letters only — the design's examples PEN/USD; the fixed list is a
// future slice).
func isCurrencyCode(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for i := 0; i < len(currency); i++ {
		c := currency[i]
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// closeContent composes the explainable What/Why/Where/Learned of a close memory
// (design §2.1). The CloseSnapshot is the structured truth; this text mirrors it
// for human readers and satisfies the four-field content contract.
func closeContent(scope core.Scope, summary PeriodSummaryOutput, input core.CreateCloseInput, pending []core.ClosePendingItem) core.Content {
	totalLines := make([]string, 0, len(input.Totals))
	sourceLines := make([]string, 0, len(input.Totals))
	for _, t := range input.Totals {
		totalLines = append(totalLines, fmt.Sprintf("%s %s %d", t.Code, t.Currency, t.AmountCents))
		sourceLines = append(sourceLines, t.SourceMemoryIDs...)
	}
	sourceSystems := "drenyra-core"
	if input.Source.System != "" {
		sourceSystems = input.Source.System
	}
	what := fmt.Sprintf("Cierre mensual %s: %d memorias en el periodo, %d items pendientes, %d totales monetarios explicitos (%s).",
		scope.Period, summary.Total, len(pending), len(input.Totals), strings.Join(totalLines, ", "))
	why := "Cierre de periodo contable: los totales son entradas explicitas con memorias fuente del mismo alcance; el cierre bloquea mutaciones del periodo hasta una reapertura explicita del controlador."
	if strings.TrimSpace(input.Reason) != "" {
		why += " Motivo: " + strings.TrimSpace(input.Reason)
	}
	where := fmt.Sprintf("Compania %s (RUC %s), periodo %s; fuente: %s; memorias fuente: %s.",
		scope.CompanyID, scope.RUC, scope.Period, sourceSystems, strings.Join(sourceLines, ", "))
	learned := fmt.Sprintf("Riesgos pendientes: %d items sin resolver; conciliaciones: 0 propuestas en este corte; seguimiento en la reapertura o el proximo periodo.",
		len(pending))
	return core.Content{What: what, Why: why, Where: where, Learned: learned}
}
