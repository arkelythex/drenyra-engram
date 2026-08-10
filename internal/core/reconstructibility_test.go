// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the G-10 reconstructibility
// pure contract (spec FZ-1/FZ-2/FR-9, design D-2): the material-decision
// eligibility predicate, the first-failure classifier with the closed six-reason
// vocabulary, the integer ratio/percentage (never floating point) and the pure
// aggregation of the frozen metric. All functions are PURE — no store access.
package core

import (
	"math"
	"reflect"
	"sort"
	"testing"
)

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

func materialityLevel(l MaterialityLevel) *MaterialityLevel {
	v := l
	return &v
}

func eligibleScope() Scope {
	return Scope{
		Kind:           ScopeKindCompany,
		OrganizationID: "org-001",
		CompanyID:      "acme",
		RUC:            "20100039201",
		Period:         "202401",
	}
}

// eligibleMemory returns a decision memory satisfying every FZ-1 axis: latest
// chain revision, exact company scope + period, approved, a frozen-eligible
// fiscal effect and a declared material/critical level. Tests vary one axis at a
// time from this base.
func eligibleMemory() AccountingMemory {
	return AccountingMemory{
		Identity:         Identity{ID: "mem-1", TopicKey: "decision/expense/001"},
		Title:            "expense classification",
		Kind:             KindDecision,
		Scope:            eligibleScope(),
		Content:          Content{What: "w", Why: "y", Where: "f", Learned: "x"},
		Status:           StatusApproved,
		FiscalEffect:     FiscalEffectJournalEntry,
		EffectiveAt:      "2026-01-15T12:00:00.000Z",
		RecordedAt:       "2026-01-15T12:00:00.000Z",
		Source:           Source{System: "go-test", ActorKind: ActorKindAgent},
		MaterialityLevel: materialityLevel(MaterialityMaterial),
		Revision:         1,
	}
}

func layer(name string, status VerificationStatus) VerificationLayer {
	return VerificationLayer{Name: name, Status: status, Detail: "test"}
}

// passingReport returns a report whose top-level layers all pass: the six
// receipt layers, principal provenance, supersession chain, evidence
// availability, object availability, rule availability and rule version/vigencia.
func passingReport() VerificationReport {
	layers := []VerificationLayer{}
	for _, name := range ReceiptLayerNames() {
		layers = append(layers, layer(name, VerificationPassed))
	}
	layers = append(layers,
		layer(LayerPrincipalProvenance, VerificationPassed),
		layer(LayerSupersessionChain, VerificationPassed),
		layer(LayerEvidenceAvailability, VerificationPassed),
		layer(LayerObjectAvailability, VerificationPassed),
		layer(LayerRuleAvailability, VerificationPassed),
		layer(LayerRuleVersionVigencia, VerificationPassed),
	)
	return VerificationReport{
		SubjectType: "memory",
		SubjectID:   "mem-1",
		Outcome:     VerificationOutcomePassed,
		Receipts:    []ReceiptVerification{},
		Layers:      layers,
	}
}

// reportWith returns a report built from the given layers (append a failing
// layer over a fully-passing base by convention: the FIRST failing layer in FZ-2
// order decides the classification).
func reportWith(layers ...VerificationLayer) VerificationReport {
	r := passingReport()
	r.Layers = layers
	return r
}

// ──────────────────────────────────────────────
// FZ-1 eligibility matrix (IsMaterialDecision)
// ──────────────────────────────────────────────

func TestIsMaterialDecisionEligibilityMatrix(t *testing.T) {
	scope := eligibleScope()
	tests := []struct {
		name     string
		mutate   func(*AccountingMemory)
		isLatest bool
		want     bool
	}{
		{
			name:     "eligible: latest approved material journal_entry",
			mutate:   func(m *AccountingMemory) {},
			isLatest: true,
			want:     true,
		},
		{
			name:     "not latest revision excluded",
			mutate:   func(m *AccountingMemory) {},
			isLatest: false,
			want:     false,
		},
		{
			name: "status active excluded",
			mutate: func(m *AccountingMemory) {
				m.Status = StatusActive
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "status pending_review excluded",
			mutate: func(m *AccountingMemory) {
				m.Status = StatusPendingReview
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "status rejected excluded",
			mutate: func(m *AccountingMemory) {
				m.Status = StatusRejected
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "status returned excluded",
			mutate: func(m *AccountingMemory) {
				m.Status = StatusReturned
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "status superseded excluded",
			mutate: func(m *AccountingMemory) {
				m.Status = StatusSuperseded
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "status voided excluded",
			mutate: func(m *AccountingMemory) {
				m.Status = StatusVoided
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "fiscal effect journal_entry eligible",
			mutate: func(m *AccountingMemory) {
				m.FiscalEffect = FiscalEffectJournalEntry
			},
			isLatest: true,
			want:     true,
		},
		{
			name: "fiscal effect adjustment eligible",
			mutate: func(m *AccountingMemory) {
				m.FiscalEffect = FiscalEffectAdjustment
			},
			isLatest: true,
			want:     true,
		},
		{
			name: "fiscal effect reclassification eligible",
			mutate: func(m *AccountingMemory) {
				m.FiscalEffect = FiscalEffectReclassification
			},
			isLatest: true,
			want:     true,
		},
		{
			name: "fiscal effect declaration eligible",
			mutate: func(m *AccountingMemory) {
				m.FiscalEffect = FiscalEffectDeclaration
			},
			isLatest: true,
			want:     true,
		},
		{
			name: "fiscal effect closing eligible",
			mutate: func(m *AccountingMemory) {
				m.FiscalEffect = FiscalEffectClosing
			},
			isLatest: true,
			want:     true,
		},
		{
			name: "fiscal effect sunat_filing eligible",
			mutate: func(m *AccountingMemory) {
				m.FiscalEffect = FiscalEffectSunatFiling
			},
			isLatest: true,
			want:     true,
		},
		{
			name: "fiscal effect none excluded",
			mutate: func(m *AccountingMemory) {
				m.FiscalEffect = FiscalEffectNone
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "fiscal effect approval excluded",
			mutate: func(m *AccountingMemory) {
				m.FiscalEffect = FiscalEffectApproval
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "materiality level material eligible",
			mutate: func(m *AccountingMemory) {
				m.MaterialityLevel = materialityLevel(MaterialityMaterial)
			},
			isLatest: true,
			want:     true,
		},
		{
			name: "materiality level critical eligible",
			mutate: func(m *AccountingMemory) {
				m.MaterialityLevel = materialityLevel(MaterialityCritical)
			},
			isLatest: true,
			want:     true,
		},
		{
			name: "materiality level normal excluded",
			mutate: func(m *AccountingMemory) {
				m.MaterialityLevel = materialityLevel(MaterialityNormal)
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "nil materiality level excluded (nil means normal)",
			mutate: func(m *AccountingMemory) {
				m.MaterialityLevel = nil
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "numeric materiality cents never participate: huge threshold + normal level excluded",
			mutate: func(m *AccountingMemory) {
				m.MaterialityLevel = materialityLevel(MaterialityNormal)
				cents := int64(9_999_999_999)
				m.Materiality = &cents
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "numeric materiality cents never participate: nil threshold + material level eligible",
			mutate: func(m *AccountingMemory) {
				m.MaterialityLevel = materialityLevel(MaterialityMaterial)
				m.Materiality = nil
			},
			isLatest: true,
			want:     true,
		},
		{
			name: "organization mismatch excluded",
			mutate: func(m *AccountingMemory) {
				m.Scope.OrganizationID = "org-999"
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "company mismatch excluded",
			mutate: func(m *AccountingMemory) {
				m.Scope.CompanyID = "other-co"
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "ruc mismatch excluded",
			mutate: func(m *AccountingMemory) {
				m.Scope.RUC = "20600995804"
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "period mismatch excluded",
			mutate: func(m *AccountingMemory) {
				m.Scope.Period = "202402"
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "institutional memory excluded from company query",
			mutate: func(m *AccountingMemory) {
				m.Scope = Scope{Kind: ScopeKindInstitutional}
			},
			isLatest: true,
			want:     false,
		},
		{
			name: "kind is NOT an eligibility axis (frozen): a fact with the same axes is eligible",
			mutate: func(m *AccountingMemory) {
				m.Kind = KindFact
			},
			isLatest: true,
			want:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			memory := eligibleMemory()
			tt.mutate(&memory)
			if got := IsMaterialDecision(memory, scope, tt.isLatest); got != tt.want {
				t.Errorf("IsMaterialDecision() = %v, want %v (status=%s effect=%s level=%v latest=%v)",
					got, tt.want, memory.Status, memory.FiscalEffect, levelOf(memory), tt.isLatest)
			}
		})
	}
}

// TestIsMaterialDecisionInstitutionalRequestExcluded pins the company-only
// contract: the metric takes exactly one company scope; an institutional query
// scope is never a valid requested scope (the eligibility predicate fails
// closed instead of aggregating cross-company).
func TestIsMaterialDecisionInstitutionalRequestExcluded(t *testing.T) {
	memory := eligibleMemory()
	inst := Scope{Kind: ScopeKindInstitutional}
	if IsMaterialDecision(memory, inst, true) {
		t.Fatal("IsMaterialDecision() = true for an institutional requested scope — the metric must stay company-scoped")
	}
}

func levelOf(m AccountingMemory) string {
	if m.MaterialityLevel == nil {
		return "<nil>"
	}
	return string(*m.MaterialityLevel)
}

// ──────────────────────────────────────────────
// FZ-2 classifier precedence (ClassifyReconstructibility)
// ──────────────────────────────────────────────

func TestClassifyReconstructibilityFirstFailurePrecedence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AccountingMemory)
		report VerificationReport
		want   ReconstructibilityReason
		wantOK bool
	}{
		{
			name:   "reconstructible: every layer passes",
			mutate: func(m *AccountingMemory) {},
			report: passingReport(),
			want:   "",
			wantOK: true,
		},
		{
			name: "not_approved is the first failure even when receipts fail",
			mutate: func(m *AccountingMemory) {
				m.Status = StatusRejected
			},
			report: reportWith(layer(LayerSignature, VerificationFailed)),
			want:   ReasonNotApproved,
			wantOK: false,
		},
		{
			name: "not_approved direct classifier input (approved-unreachable in the production denominator)",
			mutate: func(m *AccountingMemory) {
				m.Status = StatusPendingReview
			},
			report: passingReport(),
			want:   ReasonNotApproved,
			wantOK: false,
		},
		{
			name:   "receipt_failed: payload canonicalization",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerPayloadCanonicalization, VerificationFailed)),
			want:   ReasonReceiptFailed,
			wantOK: false,
		},
		{
			name:   "receipt_failed: envelope integrity",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerEnvelopeIntegrity, VerificationFailed)),
			want:   ReasonReceiptFailed,
			wantOK: false,
		},
		{
			name:   "receipt_failed: signature",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerSignature, VerificationFailed)),
			want:   ReasonReceiptFailed,
			wantOK: false,
		},
		{
			name:   "receipt_failed: signing-key validity",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerSigningKeyValidity, VerificationFailed)),
			want:   ReasonReceiptFailed,
			wantOK: false,
		},
		{
			name:   "receipt_failed: tenant/company scope",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerTenantCompanyScope, VerificationFailed)),
			want:   ReasonReceiptFailed,
			wantOK: false,
		},
		{
			name:   "receipt_failed: chain link",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerChainLink, VerificationFailed)),
			want:   ReasonReceiptFailed,
			wantOK: false,
		},
		{
			name:   "receipt_failed: principal provenance (memory_approved)",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerPrincipalProvenance, VerificationFailed)),
			want:   ReasonReceiptFailed,
			wantOK: false,
		},
		{
			name:   "missing_evidence: evidence availability failed",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerEvidenceAvailability, VerificationFailed)),
			want:   ReasonMissingEvidence,
			wantOK: false,
		},
		{
			name:   "evidence_missing_object: object availability failed after evidence passed",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerObjectAvailability, VerificationFailed)),
			want:   ReasonEvidenceMissingObject,
			wantOK: false,
		},
		{
			name:   "rule_unresolved: rule availability failed",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerRuleAvailability, VerificationFailed)),
			want:   ReasonRuleUnresolved,
			wantOK: false,
		},
		{
			name:   "rule_version_failed: rule version/vigencia failed after rule availability passed",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerRuleVersionVigencia, VerificationFailed)),
			want:   ReasonRuleVersionFailed,
			wantOK: false,
		},
		{
			name:   "precedence: receipt_failed wins over evidence and rule failures",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(
				layer(LayerSignature, VerificationFailed),
				layer(LayerEvidenceAvailability, VerificationFailed),
				layer(LayerRuleAvailability, VerificationFailed),
			),
			want:   ReasonReceiptFailed,
			wantOK: false,
		},
		{
			name:   "precedence: missing_evidence wins over object and rule failures",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(
				layer(LayerEvidenceAvailability, VerificationFailed),
				layer(LayerObjectAvailability, VerificationFailed),
				layer(LayerRuleVersionVigencia, VerificationFailed),
			),
			want:   ReasonMissingEvidence,
			wantOK: false,
		},
		{
			name:   "precedence: evidence_missing_object wins over rule failures",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(
				layer(LayerObjectAvailability, VerificationFailed),
				layer(LayerRuleAvailability, VerificationFailed),
			),
			want:   ReasonEvidenceMissingObject,
			wantOK: false,
		},
		{
			name:   "precedence: rule_unresolved wins over rule-version failure",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(
				layer(LayerRuleAvailability, VerificationFailed),
				layer(LayerRuleVersionVigencia, VerificationFailed),
			),
			want:   ReasonRuleUnresolved,
			wantOK: false,
		},
		{
			name:   "skipped object layer (no evidence refs) is NOT a failure",
			mutate: func(m *AccountingMemory) {},
			report: reportWith(layer(LayerObjectAvailability, VerificationSkipped)),
			want:   "",
			wantOK: true,
		},
		{
			name:   "absent rule-version layer (no structured rule links) is NOT a failure",
			mutate: func(m *AccountingMemory) {},
			report: reportWithoutRuleVersionLayer(),
			want:   "",
			wantOK: true,
		},
		{
			name:   "empty report with approved memory passes (no failing layer)",
			mutate: func(m *AccountingMemory) {},
			report: VerificationReport{SubjectType: "memory", SubjectID: "mem-1"},
			want:   "",
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			memory := eligibleMemory()
			tt.mutate(&memory)
			gotReason, gotOK := ClassifyReconstructibility(memory, tt.report)
			if gotOK != tt.wantOK {
				t.Fatalf("ClassifyReconstructibility() ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotReason != tt.want {
				t.Errorf("ClassifyReconstructibility() reason = %q, want %q", gotReason, tt.want)
			}
		})
	}
}

// reportWithoutRuleVersionLayer is a fully-passing report minus the rule
// version/vigencia layer — the shape a memory with NO structured rule links
// produces (the layer is appended only when traces exist).
func reportWithoutRuleVersionLayer() VerificationReport {
	layers := []VerificationLayer{}
	for _, name := range ReceiptLayerNames() {
		layers = append(layers, layer(name, VerificationPassed))
	}
	layers = append(layers,
		layer(LayerPrincipalProvenance, VerificationPassed),
		layer(LayerSupersessionChain, VerificationPassed),
		layer(LayerEvidenceAvailability, VerificationPassed),
		layer(LayerObjectAvailability, VerificationPassed),
		layer(LayerRuleAvailability, VerificationPassed),
	)
	return VerificationReport{
		SubjectType: "memory",
		SubjectID:   "mem-1",
		Outcome:     VerificationOutcomePassed,
		Receipts:    []ReceiptVerification{},
		Layers:      layers,
	}
}

// ──────────────────────────────────────────────
// Closed reason vocabulary (FZ-2)
// ──────────────────────────────────────────────

func TestReconstructibilityReasonVocabularyIsClosed(t *testing.T) {
	want := []ReconstructibilityReason{
		ReasonNotApproved,
		ReasonReceiptFailed,
		ReasonMissingEvidence,
		ReasonEvidenceMissingObject,
		ReasonRuleUnresolved,
		ReasonRuleVersionFailed,
	}
	got := AllReconstructibilityReasons()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AllReconstructibilityReasons() = %v, want the frozen six %v", got, want)
	}
	if len(got) != 6 {
		t.Fatalf("reason vocabulary must have exactly 6 members, got %d", len(got))
	}
	for _, r := range got {
		if !IsValidReconstructibilityReason(r) {
			t.Errorf("IsValidReconstructibilityReason(%q) = false for a vocabulary member", r)
		}
	}
	for _, bogus := range []ReconstructibilityReason{"", "made_up", "RECEIPT_FAILED", "not_approved "} {
		if IsValidReconstructibilityReason(bogus) {
			t.Errorf("IsValidReconstructibilityReason(%q) = true — arbitrary strings must not compile into the closed vocabulary", bogus)
		}
	}
}

// ──────────────────────────────────────────────
// Integer ratio/percentage (FR-9 iii) — never floating point
// ──────────────────────────────────────────────

func TestReconstructibilityPercentageIntegerMath(t *testing.T) {
	tests := []struct {
		name        string
		numerator   int
		denominator int
		want        *int
	}{
		{name: "2/3 truncates to 66", numerator: 2, denominator: 3, want: intPtr(66)},
		{name: "0/5 is 0", numerator: 0, denominator: 5, want: intPtr(0)},
		{name: "5/5 is 100", numerator: 5, denominator: 5, want: intPtr(100)},
		{name: "1/2 is 50", numerator: 1, denominator: 2, want: intPtr(50)},
		{name: "7/2 is 350", numerator: 7, denominator: 2, want: intPtr(350)},
		{name: "1/3 is 33 (truncation, never rounding)", numerator: 1, denominator: 3, want: intPtr(33)},
		{name: "zero denominator is null", numerator: 0, denominator: 0, want: nil},
		{name: "negative denominator fails closed to null", numerator: 3, denominator: -1, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReconstructibilityPercentage(tt.numerator, tt.denominator)
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("ReconstructibilityPercentage(%d,%d) = %d, want null", tt.numerator, tt.denominator, *got)
			case tt.want != nil && got == nil:
				t.Fatalf("ReconstructibilityPercentage(%d,%d) = null, want %d", tt.numerator, tt.denominator, *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Errorf("ReconstructibilityPercentage(%d,%d) = %d, want %d", tt.numerator, tt.denominator, *got, *tt.want)
			}
		})
	}
}

// TestReconstructibilityPercentageOverflowGuard pins the multiplication guard
// (D-4): counts originate from bounded slice lengths, but the implementation
// must never wrap. A percentage not representable in int fails closed to null.
func TestReconstructibilityPercentageOverflowGuard(t *testing.T) {
	if got := ReconstructibilityPercentage(math.MaxInt, 1); got != nil {
		t.Fatalf("ReconstructibilityPercentage(MaxInt,1) = %d, want null (true percentage overflows int)", *got)
	}
	// The largest representable percentage still computes exactly: numerator =
	// (MaxInt/100)*2 over denominator 2 → true percentage (MaxInt/100)*100, the
	// biggest value that fits int without wrapping.
	maxSafe := math.MaxInt / 100
	if got := ReconstructibilityPercentage(maxSafe*2, 2); got == nil || *got != maxSafe*100 {
		t.Fatalf("ReconstructibilityPercentage(%d,2) = %v, want %d", maxSafe*2, got, maxSafe*100)
	}
}

func intPtr(v int) *int { return &v }

// ──────────────────────────────────────────────
// Frozen zero-denominator representation (FR-9 iv)
// ──────────────────────────────────────────────

func TestBuildReconstructibilityCountsZeroDenominator(t *testing.T) {
	got := BuildReconstructibilityCounts(0, 0)
	want := ReconstructibilityCounts{
		Denominator:     0,
		Numerator:       0,
		Ratio:           ReconstructibilityRatio{Numerator: 0, Denominator: 0},
		Percentage:      nil,
		ZeroDenominator: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildReconstructibilityCounts(0,0) = %+v, want the frozen zero shape %+v", got, want)
	}
	if got.Percentage != nil {
		t.Fatalf("zero denominator percentage = %d — must be null, NEVER a misleading 0%% or 100%%", *got.Percentage)
	}
}

func TestBuildReconstructibilityCountsNonZero(t *testing.T) {
	got := BuildReconstructibilityCounts(3, 2)
	if got.Denominator != 3 || got.Numerator != 2 {
		t.Fatalf("counts = %d/%d, want 2/3", got.Numerator, got.Denominator)
	}
	if got.ZeroDenominator {
		t.Fatal("zeroDenominator = true for a non-zero denominator")
	}
	if got.Ratio != (ReconstructibilityRatio{Numerator: 2, Denominator: 3}) {
		t.Fatalf("ratio = %+v, want {2,3}", got.Ratio)
	}
	if got.Percentage == nil || *got.Percentage != 66 {
		t.Fatalf("percentage = %v, want 66 (integer truncation)", got.Percentage)
	}
}

// ──────────────────────────────────────────────
// Pure aggregation (denominator/numerator/reason groups)
// ──────────────────────────────────────────────

func TestAggregateReconstructibilityCountsAndGroups(t *testing.T) {
	scope := eligibleScope()

	reconstructible := eligibleMemory()
	reconstructible.Identity.ID = "mem-a"
	reconstructible.Identity.TopicKey = "decision/a"

	missingEvidence := eligibleMemory()
	missingEvidence.Identity.ID = "mem-b"
	missingEvidence.Identity.TopicKey = "decision/b"

	receiptFailed := eligibleMemory()
	receiptFailed.Identity.ID = "mem-c"
	receiptFailed.Identity.TopicKey = "decision/c"

	notEligibleStatus := eligibleMemory()
	notEligibleStatus.Identity.ID = "mem-d"
	notEligibleStatus.Identity.TopicKey = "decision/d"
	notEligibleStatus.Status = StatusRejected

	notEligibleEffect := eligibleMemory()
	notEligibleEffect.Identity.ID = "mem-e"
	notEligibleEffect.Identity.TopicKey = "decision/e"
	notEligibleEffect.FiscalEffect = FiscalEffectNone

	outOfScope := eligibleMemory()
	outOfScope.Identity.ID = "mem-f"
	outOfScope.Identity.TopicKey = "decision/f"
	outOfScope.Scope.CompanyID = "other-co"

	// Deliberately reversed insertion order — aggregation must be deterministic
	// (heads are processed in bytewise decision-ID order).
	heads := []AccountingMemory{outOfScope, notEligibleEffect, receiptFailed, notEligibleStatus, missingEvidence, reconstructible}

	reports := map[string]VerificationReport{
		"mem-a": passingReport(),
		"mem-b": reportWith(layer(LayerEvidenceAvailability, VerificationFailed)),
		// mem-c has NO report entry → fails closed as receipt_failed (the
		// ErrNoReceipts mapping of the service layer).
	}

	got := AggregateReconstructibility(heads, reports, scope)

	want := ReconstructibilityAggregate{
		Denominator: 3, // mem-a, mem-b, mem-c — the FZ-1 eligible heads only
		Numerator:   1, // mem-a
		Reasons: map[ReconstructibilityReason][]string{
			ReasonMissingEvidence: {"mem-b"},
			ReasonReceiptFailed:   {"mem-c"},
		},
	}
	if got.Denominator != want.Denominator || got.Numerator != want.Numerator {
		t.Fatalf("aggregate = %d/%d, want %d/%d", got.Numerator, got.Denominator, want.Numerator, want.Denominator)
	}
	for reason, wantIDs := range want.Reasons {
		if !reflect.DeepEqual(got.Reasons[reason], wantIDs) {
			t.Errorf("reasons[%q] = %v, want %v", reason, got.Reasons[reason], wantIDs)
		}
	}
	for reason, ids := range got.Reasons {
		if len(ids) == 0 {
			t.Errorf("reasons[%q] has no members but appears in the map", reason)
		}
		if _, ok := want.Reasons[reason]; !ok {
			t.Errorf("unexpected reason group %q with %v", reason, ids)
		}
	}
	// Non-eligible heads never leak into any group.
	for _, id := range []string{"mem-d", "mem-e", "mem-f"} {
		for reason, ids := range got.Reasons {
			for _, gotID := range ids {
				if gotID == id {
					t.Errorf("non-eligible head %s leaked into reason group %q", id, reason)
				}
			}
		}
	}
}

func TestAggregateReconstructibilitySortsReasonIDs(t *testing.T) {
	scope := eligibleScope()
	makeHead := func(id, topic string) AccountingMemory {
		m := eligibleMemory()
		m.Identity.ID = id
		m.Identity.TopicKey = topic
		return m
	}
	// "mem-z" sorts before "mem-10" bytewise ("1" < "z") — the frozen order is
	// bytewise ascending decision ID, never insertion order.
	heads := []AccountingMemory{
		makeHead("mem-z", "decision/z"),
		makeHead("mem-10", "decision/10"),
		makeHead("mem-a", "decision/a"),
	}
	reports := map[string]VerificationReport{
		"mem-z":  reportWith(layer(LayerRuleAvailability, VerificationFailed)),
		"mem-10": reportWith(layer(LayerRuleAvailability, VerificationFailed)),
		"mem-a":  reportWith(layer(LayerRuleAvailability, VerificationFailed)),
	}
	got := AggregateReconstructibility(heads, reports, scope)
	ids := got.Reasons[ReasonRuleUnresolved]
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("reason IDs not sorted bytewise: %v", ids)
	}
	want := []string{"mem-10", "mem-a", "mem-z"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("reason IDs = %v, want bytewise-sorted %v", ids, want)
	}
	if got.Denominator != 3 || got.Numerator != 0 {
		t.Fatalf("aggregate = %d/%d, want 0/3", got.Numerator, got.Denominator)
	}
}
