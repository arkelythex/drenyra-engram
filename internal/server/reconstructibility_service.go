// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; the reconstructibility percentage is integer
// math, never floating point. This module is the G-10 reconstructibility server
// aggregator (spec FR-9/FR-10, design D-3/D-4): a read-only service that
// composes the narrow store reads — MaterialDecisionReader + MemoryVerifier —
// with the PURE core classifier and aggregator (internal/core/
// reconstructibility.go) and assembles the frozen ReconstructibilityResult.
//
// READ-ONLY AND NON-AUTHORIZING (IR-3): no transaction is ever started, no row
// is ever written, and the result is an observation — it never authorizes,
// posts, files, approves or reopens anything. Persistence/decode/corruption
// errors abort the WHOLE report as RECONSTRUCTIBILITY_UNAVAILABLE and are never
// mislabeled as a business reason; a reader returning an out-of-scope head
// aborts with RECONSTRUCTIBILITY_SCOPE_MISMATCH (defensive — the SQL is already
// scope-scoped).
package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// MaterialDecisionReader is the narrow read surface the reconstructibility
// aggregator delegates to (mirrors the RuleImpactStore/VerificationStore
// narrow-interface patterns): ONE exact-company-scope read of the FZ-1
// material-decision heads, scoped in SQL. *store.SQLiteStore satisfies it.
type MaterialDecisionReader interface {
	LatestMaterialDecisionHeads(ctx context.Context, scope core.Scope) ([]core.AccountingMemory, error)
}

// MemoryVerifier is the narrow read surface for full signed-chain verification
// of ONE decision memory (the existing offline verification engine, read-only
// and transaction-free). memoryVerifierAdapter binds it to server.VerifyMemory;
// *store.SQLiteStore satisfies the underlying VerificationStore seam.
type MemoryVerifier interface {
	VerifyMemory(ctx context.Context, memoryID string) (core.VerificationReport, error)
}

// memoryVerifierAdapter adapts the existing verification service to the narrow
// MemoryVerifier seam — the SAME read-only machinery the CLI/MCP/HTTP verify
// surfaces use; the metric never builds a second reconstruction truth model
// (FR-9 vi, design D-3).
type memoryVerifierAdapter struct {
	store VerificationStore
}

// VerifyMemory delegates to the established read-only verification service.
func (a memoryVerifierAdapter) VerifyMemory(ctx context.Context, memoryID string) (core.VerificationReport, error) {
	return VerifyMemory(ctx, a.store, memoryID)
}

// Sentinels distinguish report-building failures (the whole report is aborted)
// from metric data. Non-reconstructible decisions are DATA, never these errors.
var (
	// ErrReconstructibilityScopeMismatch means a reader returned a head outside
	// the requested exact scope — a reader-contract violation (defensive; the
	// SQL is scope-scoped). The report is aborted, never post-filtered (IR-2).
	ErrReconstructibilityScopeMismatch = errors.New("RECONSTRUCTIBILITY_SCOPE_MISMATCH")
	// ErrReconstructibilityUnavailable means the report could not be built
	// (persistence/decode/corruption) — a corrupted read is never mislabeled as
	// a business reason (design D-3).
	ErrReconstructibilityUnavailable = errors.New("RECONSTRUCTIBILITY_UNAVAILABLE")
)

// ReconstructibilityReasons is the frozen per-category non-reconstructible
// decision-ID container (D-4): a concrete struct — NOT a map — with fields in
// the frozen FZ-2 vocabulary order, so the JSON field order is stable and empty
// lists serialize as [] (never null).
type ReconstructibilityReasons struct {
	NotApproved           []string `json:"not_approved"`
	ReceiptFailed         []string `json:"receipt_failed"`
	MissingEvidence       []string `json:"missing_evidence"`
	EvidenceMissingObject []string `json:"evidence_missing_object"`
	RuleUnresolved        []string `json:"rule_unresolved"`
	RuleVersionFailed     []string `json:"rule_version_failed"`
}

// ReconstructibilityResult is the frozen G-10 result shape (FR-9 iii, D-4):
// declaration/JSON order is scope, period, denominator, numerator, ratio,
// percentage, reasons, zeroDenominator. percentage is null when the denominator
// is zero; a zero denominator emits exactly {0,0} + null percentage +
// zeroDenominator:true — NEVER a misleading 0% or 100%.
type ReconstructibilityResult struct {
	Scope           core.Scope                   `json:"scope"`
	Period          string                       `json:"period"`
	Denominator     int                          `json:"denominator"`
	Numerator       int                          `json:"numerator"`
	Ratio           core.ReconstructibilityRatio `json:"ratio"`
	Percentage      *int                         `json:"percentage"`
	Reasons         ReconstructibilityReasons    `json:"reasons"`
	ZeroDenominator bool                         `json:"zeroDenominator"`
}

// validateReconstructibilityScope fails closed on a missing, ambiguous or
// malformed scope/period (FR-9 i): the metric takes exactly one company scope
// and one period; cross-tenant aggregation is forbidden.
func validateReconstructibilityScope(scope core.Scope) error {
	if scope.Kind != core.ScopeKindCompany {
		return fmt.Errorf("INVALID_RECONSTRUCTIBILITY_SCOPE: the metric requires an exact company scope, got kind %q", scope.Kind)
	}
	if scope.Period == "" {
		return fmt.Errorf("INVALID_RECONSTRUCTIBILITY_SCOPE: period is required (YYYYMM) for an exact company scope")
	}
	return core.AssertValidScope(scope)
}

// Reconstructibility computes the deterministic, read-only reconstructibility
// baseline for ONE exact company scope + period (FR-9): the denominator is the
// FZ-1 material-decision count, the numerator the FZ-2 reconstructible count,
// and the per-reason non-reconstructible decision IDs are grouped in the frozen
// vocabulary order. Two runs against the same store snapshot return
// byte-identical results (FR-9 vi).
func Reconstructibility(ctx context.Context, reader MaterialDecisionReader, verifier MemoryVerifier, scope core.Scope) (ReconstructibilityResult, error) {
	empty := ReconstructibilityResult{}
	if err := validateReconstructibilityScope(scope); err != nil {
		return empty, err
	}

	heads, err := reader.LatestMaterialDecisionHeads(ctx, scope)
	if err != nil {
		return empty, fmt.Errorf("%w: read material decision heads: %v", ErrReconstructibilityUnavailable, err)
	}

	// Defensive per-row invariant (D-3): the SQL is exact-scope scoped, but a
	// reader returning ANY out-of-scope row is a contract violation — abort the
	// report, never silently post-filter (IR-2).
	for _, head := range heads {
		if !core.ScopeEquals(head.Scope, scope) {
			return empty, fmt.Errorf("%w: reader returned head %s with scope %v outside the requested exact scope %v",
				ErrReconstructibilityScopeMismatch, head.Identity.ID, head.Scope, scope)
		}
	}

	// Verify every FZ-1-eligible head through the existing read-only verification
	// machinery. ErrNoReceipts is EVIDENCE (the decision has no signed chain →
	// receipt_failed via the pure aggregator's missing-report rule); every other
	// error aborts the whole report as unavailable.
	reports := map[string]core.VerificationReport{}
	for _, head := range heads {
		if !core.IsMaterialDecision(head, scope, true) {
			continue // defensive FZ-1 re-check (the SQL already filtered)
		}
		report, err := verifier.VerifyMemory(ctx, head.Identity.ID)
		if err != nil {
			if errors.Is(err, ErrNoReceipts) {
				continue // → receipt_failed (missing report entry), never an error
			}
			return empty, fmt.Errorf("%w: verify %s: %v", ErrReconstructibilityUnavailable, head.Identity.ID, err)
		}
		reports[head.Identity.ID] = report
	}

	// Pure aggregation + frozen count shape; the reason lists are already sorted
	// bytewise by the aggregator (FR-9 vi).
	agg := core.AggregateReconstructibility(heads, reports, scope)
	counts := core.BuildReconstructibilityCounts(agg.Denominator, agg.Numerator)

	return ReconstructibilityResult{
		Scope:           scope,
		Period:          scope.Period,
		Denominator:     counts.Denominator,
		Numerator:       counts.Numerator,
		Ratio:           counts.Ratio,
		Percentage:      counts.Percentage,
		Reasons:         buildReconstructibilityReasons(agg.Reasons),
		ZeroDenominator: counts.ZeroDenominator,
	}, nil
}

// buildReconstructibilityReasons maps the pure reason groups into the frozen
// concrete struct. Empty lists are [] — never null (D-4).
func buildReconstructibilityReasons(reasons map[core.ReconstructibilityReason][]string) ReconstructibilityReasons {
	return ReconstructibilityReasons{
		NotApproved:           idsOrEmpty(reasons[core.ReasonNotApproved]),
		ReceiptFailed:         idsOrEmpty(reasons[core.ReasonReceiptFailed]),
		MissingEvidence:       idsOrEmpty(reasons[core.ReasonMissingEvidence]),
		EvidenceMissingObject: idsOrEmpty(reasons[core.ReasonEvidenceMissingObject]),
		RuleUnresolved:        idsOrEmpty(reasons[core.ReasonRuleUnresolved]),
		RuleVersionFailed:     idsOrEmpty(reasons[core.ReasonRuleVersionFailed]),
	}
}

func idsOrEmpty(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
