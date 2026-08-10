// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; the reconstructibility percentage is integer
// math, never floating point. This module is the G-10 reconstructibility PURE
// contract (spec FZ-1, FZ-2, FR-9; design D-2): the material-decision eligibility
// predicate (the metric denominator), the first-failure classifier with the
// closed six-reason vocabulary (the metric numerator), the integer ratio and
// percentage, and the pure aggregation of the frozen metric.
//
// PURITY BOUNDARY: every function here is a pure function of its inputs — no
// store access, no transactions, no I/O, no clocks. The server aggregator
// (internal/server/reconstructibility_service.go) composes these functions with
// the narrow store reads; nothing in this file can mutate or observe state.
//
// The classifier is deliberately SEPARATE from the aggregation so `not_approved`
// stays a real, unit-testable closed-vocabulary outcome rather than dead code
// hidden behind the denominator prefilter (design D-2).
package core

import (
	"math"
	"sort"
)

// ──────────────────────────────────────────────
// FZ-2 closed reason vocabulary
// ──────────────────────────────────────────────

// ReconstructibilityReason is the closed vocabulary of non-reconstructibility
// reasons (FZ-2). A decision is NEVER classified with more than one reason, and
// no other reason string may be emitted.
type ReconstructibilityReason string

const (
	// ReasonNotApproved — FZ-2 step (a): the decision's current status is not
	// StatusApproved. Structurally unreachable for denominator members in v1
	// (FZ-1 already requires approved); a frozen member of the vocabulary that
	// protects the metric contract if eligibility ever evolves.
	ReasonNotApproved ReconstructibilityReason = "not_approved"
	// ReasonReceiptFailed — FZ-2 step (d): the full signed receipt chain or the
	// approval provenance layer failed.
	ReasonReceiptFailed ReconstructibilityReason = "receipt_failed"
	// ReasonMissingEvidence — FZ-2 step (b), first half: the evidence-availability
	// layer failed.
	ReasonMissingEvidence ReconstructibilityReason = "missing_evidence"
	// ReasonEvidenceMissingObject — FZ-2 step (b), second half: every resolved
	// evidence object's WORM bytes failed to re-hash to its content address.
	ReasonEvidenceMissingObject ReconstructibilityReason = "evidence_missing_object"
	// ReasonRuleUnresolved — FZ-2 step (c), first half: the rule-availability
	// layer failed.
	ReasonRuleUnresolved ReconstructibilityReason = "rule_unresolved"
	// ReasonRuleVersionFailed — FZ-2 step (c), second half: the rule-version/
	// vigencia layer failed (a structured trace outcome other than resolved or
	// legacy-skipped).
	ReasonRuleVersionFailed ReconstructibilityReason = "rule_version_failed"
)

// AllReconstructibilityReasons returns the complete, closed reason vocabulary in
// the frozen FZ-2 order — exactly six members, no more.
func AllReconstructibilityReasons() []ReconstructibilityReason {
	return []ReconstructibilityReason{
		ReasonNotApproved,
		ReasonReceiptFailed,
		ReasonMissingEvidence,
		ReasonEvidenceMissingObject,
		ReasonRuleUnresolved,
		ReasonRuleVersionFailed,
	}
}

// IsValidReconstructibilityReason reports whether reason is a member of the
// closed vocabulary. Arbitrary reason strings never classify a decision.
func IsValidReconstructibilityReason(reason ReconstructibilityReason) bool {
	switch reason {
	case ReasonNotApproved, ReasonReceiptFailed, ReasonMissingEvidence,
		ReasonEvidenceMissingObject, ReasonRuleUnresolved, ReasonRuleVersionFailed:
		return true
	}
	return false
}

// ──────────────────────────────────────────────
// FZ-1 material-decision eligibility (denominator)
// ──────────────────────────────────────────────

// IsMaterialDecision applies the frozen FZ-1 eligibility predicate: a memory is
// a material decision in the queried scope/period if and only if ALL of:
//
//  1. it is the LATEST revision of its topicKey chain (isLatest);
//  2. its Scope is an exact company scope whose CompanyID/RUC equal the queried
//     scope AND whose Period equals the queried period byte-for-byte;
//  3. its Status is exactly StatusApproved;
//  4. its FiscalEffect is one of exactly journal_entry, adjustment,
//     reclassification, declaration, closing, sunat_filing;
//  5. its declared MaterialityLevel is material or critical (nil — which means
//     normal — is NOT eligible).
//
// The numeric Materiality cents threshold field MUST NOT participate in
// eligibility (frozen decision: the write-time-validated level enum is the only
// eligibility signal). Pure — no store access.
func IsMaterialDecision(memory AccountingMemory, requestedScope Scope, isLatest bool) bool {
	// FZ-1.1 — latest chain revision only.
	if !isLatest {
		return false
	}
	// FZ-1.2 — exact company scope and byte-equal period (scope.md rule 5).
	if !ScopeEquals(memory.Scope, requestedScope) {
		return false
	}
	// FZ-1.3 — the human-approval gate (baseline stability).
	if memory.Status != StatusApproved {
		return false
	}
	// FZ-1.4 — exactly the six frozen fiscal effects.
	switch memory.FiscalEffect {
	case FiscalEffectJournalEntry, FiscalEffectAdjustment, FiscalEffectReclassification,
		FiscalEffectDeclaration, FiscalEffectClosing, FiscalEffectSunatFiling:
	default:
		return false
	}
	// FZ-1.5 — declared material/critical level; nil (== normal) excluded. The
	// numeric Materiality threshold never participates.
	if memory.MaterialityLevel == nil {
		return false
	}
	switch *memory.MaterialityLevel {
	case MaterialityMaterial, MaterialityCritical:
		return true
	}
	return false
}

// ──────────────────────────────────────────────
// FZ-2 reconstructibility classifier
// ──────────────────────────────────────────────

// receiptChainFailed reports whether the decision's full signed receipt chain
// passes every receipt layer AND the principal-provenance layer (FZ-2 step d).
// A layer in the skipped state is not a failure (inapplicable); only a failed
// layer is. The classifier inspects the top-level aggregated report layers by
// their stable names — the shape VerifyMemory and its TS mirror produce.
func receiptChainFailed(report VerificationReport) bool {
	for _, l := range report.Layers {
		switch l.Name {
		case LayerPayloadCanonicalization, LayerEnvelopeIntegrity, LayerSignature,
			LayerSigningKeyValidity, LayerTenantCompanyScope, LayerChainLink,
			LayerPrincipalProvenance:
			if l.Status == VerificationFailed {
				return true
			}
		}
	}
	return false
}

// reportLayerFailed reports whether the named top-level layer is present AND
// failed. An absent layer (e.g. rule version/vigencia on a memory with no
// structured rule links) or a skipped layer is not a failure.
func reportLayerFailed(report VerificationReport, name string) bool {
	for _, l := range report.Layers {
		if l.Name == name {
			return l.Status == VerificationFailed
		}
	}
	return false
}

// ClassifyReconstructibility applies FZ-2 in the frozen first-failure order:
//
//	(a) approval        → not_approved
//	(d) receipt         → receipt_failed
//	(b) evidence        → missing_evidence, then object → evidence_missing_object
//	(c) rule            → rule_unresolved, then version/vigencia → rule_version_failed
//
// ok=true means the decision is reconstructible (a numerator member) and reason
// is the empty string; ok=false means the decision is NOT reconstructible and
// reason is exactly ONE member of the closed six-reason vocabulary. A decision
// is never classified with more than one reason. Pure — no store access.
func ClassifyReconstructibility(memory AccountingMemory, report VerificationReport) (reason ReconstructibilityReason, ok bool) {
	// (a) approval — the first failure.
	if memory.Status != StatusApproved {
		return ReasonNotApproved, false
	}
	// (d) receipt — the full signed receipt chain plus approval provenance.
	if receiptChainFailed(report) {
		return ReasonReceiptFailed, false
	}
	// (b) evidence availability, then object availability.
	if reportLayerFailed(report, LayerEvidenceAvailability) {
		return ReasonMissingEvidence, false
	}
	if reportLayerFailed(report, LayerObjectAvailability) {
		return ReasonEvidenceMissingObject, false
	}
	// (c) rule availability, then rule version/vigencia.
	if reportLayerFailed(report, LayerRuleAvailability) {
		return ReasonRuleUnresolved, false
	}
	if reportLayerFailed(report, LayerRuleVersionVigencia) {
		return ReasonRuleVersionFailed, false
	}
	return "", true
}

// ──────────────────────────────────────────────
// Integer ratio and percentage (FR-9 iii) — never floating point
// ──────────────────────────────────────────────

// ReconstructibilityRatio is the frozen ratio shape {numerator, denominator}.
type ReconstructibilityRatio struct {
	Numerator   int `json:"numerator"`
	Denominator int `json:"denominator"`
}

// ReconstructibilityPercentage computes the metric percentage as integer
// division (numerator*100)/denominator in integer math — never floating point —
// with an explicit multiplication-overflow guard (D-4). It returns nil (null)
// when the denominator is zero — and, defensively, for a denominator or
// numerator whose true percentage is not representable in int (unreachable for
// bounded slice counts, but the computation must never wrap).
func ReconstructibilityPercentage(numerator, denominator int) *int {
	// Zero (or negative) denominator is null — checked BEFORE the numerator
	// branch so (0,0) never misreads as a 0%.
	if denominator <= 0 {
		return nil
	}
	if numerator <= 0 {
		zero := 0
		return &zero
	}
	// Decompose (numerator*100)/denominator = q*100 + (r*100)/denominator so the
	// multiplication never overflows while the true percentage fits int.
	q := numerator / denominator
	r := numerator % denominator
	if q > math.MaxInt/100 {
		return nil // true percentage not representable — fail closed, never wrap
	}
	pct := q*100 + (r*100)/denominator
	return &pct
}

// ReconstructibilityCounts is the frozen count shape of the metric: the
// denominator, the numerator, the ratio, the integer percentage (null when the
// denominator is zero) and the zero-denominator flag.
type ReconstructibilityCounts struct {
	Denominator     int                     `json:"denominator"`
	Numerator       int                     `json:"numerator"`
	Ratio           ReconstructibilityRatio `json:"ratio"`
	Percentage      *int                    `json:"percentage"`
	ZeroDenominator bool                    `json:"zeroDenominator"`
}

// BuildReconstructibilityCounts assembles the frozen count shape (FR-9 iii/iv).
// A zero denominator emits EXACTLY {denominator:0, numerator:0, ratio:{0,0},
// percentage:null, zeroDenominator:true} — NEVER a misleading 0% or 100%.
func BuildReconstructibilityCounts(denominator, numerator int) ReconstructibilityCounts {
	if denominator <= 0 {
		return ReconstructibilityCounts{
			Denominator:     0,
			Numerator:       0,
			Ratio:           ReconstructibilityRatio{Numerator: 0, Denominator: 0},
			Percentage:      nil,
			ZeroDenominator: true,
		}
	}
	return ReconstructibilityCounts{
		Denominator:     denominator,
		Numerator:       numerator,
		Ratio:           ReconstructibilityRatio{Numerator: numerator, Denominator: denominator},
		Percentage:      ReconstructibilityPercentage(numerator, denominator),
		ZeroDenominator: false,
	}
}

// ──────────────────────────────────────────────
// Pure aggregation
// ──────────────────────────────────────────────

// ReconstructibilityAggregate is the pure outcome of aggregating the metric
// over a set of chain heads: the denominator (FZ-1 eligible heads), the
// numerator (reconstructible heads) and the per-reason decision-ID lists of the
// non-reconstructible members.
type ReconstructibilityAggregate struct {
	Denominator int
	Numerator   int
	Reasons     map[ReconstructibilityReason][]string
}

// AggregateReconstructibility applies FZ-1 (latest chain revision, exact company
// scope + byte-equal period, approved, six frozen fiscal effects, material/
// critical level) to every chain head, classifies each eligible head with FZ-2
// and counts the frozen metric. Pure: no store access. Deterministic (FR-9 vi):
// heads are processed in bytewise decision-ID order and every reason list is
// sorted bytewise, so the aggregate is independent of reader ordering.
//
// reports must carry one entry per eligible head. An eligible head MISSING from
// reports fails closed as receipt_failed — the pure counterpart of the service
// layer's ErrNoReceipts mapping (design D-3: a subject without an established
// signed receipt chain is never a numerator member).
func AggregateReconstructibility(heads []AccountingMemory, reports map[string]VerificationReport, scope Scope) ReconstructibilityAggregate {
	ordered := make([]AccountingMemory, len(heads))
	copy(ordered, heads)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Identity.ID < ordered[j].Identity.ID
	})

	agg := ReconstructibilityAggregate{
		Denominator: 0,
		Numerator:   0,
		Reasons:     map[ReconstructibilityReason][]string{},
	}
	for _, head := range ordered {
		if !IsMaterialDecision(head, scope, true) {
			continue
		}
		agg.Denominator++
		report, hasReport := reports[head.Identity.ID]
		if !hasReport {
			agg.Reasons[ReasonReceiptFailed] = append(agg.Reasons[ReasonReceiptFailed], head.Identity.ID)
			continue
		}
		reason, ok := ClassifyReconstructibility(head, report)
		if ok {
			agg.Numerator++
			continue
		}
		agg.Reasons[reason] = append(agg.Reasons[reason], head.Identity.ID)
	}
	for reason := range agg.Reasons {
		sort.Strings(agg.Reasons[reason])
	}
	return agg
}
