// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; the reconstructibility percentage is integer
// math. This module tests the G-10 server aggregator (design D-3/D-4): the
// service composes the narrow store reads (MaterialDecisionReader +
// MemoryVerifier) with the PURE core classifier/aggregator, assembles the frozen
// ReconstructibilityResult and never mutates anything (read-only, no
// transaction). ErrNoReceipts maps to receipt_failed; persistence/decode/
// corruption errors abort the report as RECONSTRUCTIBILITY_UNAVAILABLE; a
// reader returning an out-of-scope head aborts with
// RECONSTRUCTIBILITY_SCOPE_MISMATCH.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// ──────────────────────────────────────────────
// Fakes over the narrow seams
// ──────────────────────────────────────────────

// fakeMaterialReader records the requested scope (proving the service passes the
// exact scope through) and returns the canned heads or error.
type fakeMaterialReader struct {
	heads    []core.AccountingMemory
	err      error
	gotScope core.Scope
	calls    int
}

func (f *fakeMaterialReader) LatestMaterialDecisionHeads(ctx context.Context, scope core.Scope) ([]core.AccountingMemory, error) {
	f.calls++
	f.gotScope = scope
	if f.err != nil {
		return nil, f.err
	}
	out := make([]core.AccountingMemory, len(f.heads))
	copy(out, f.heads)
	return out, nil
}

// fakeVerifier returns the canned report or error per memory ID.
type fakeVerifier struct {
	reports map[string]core.VerificationReport
	errs    map[string]error
	calls   int
}

func (f *fakeVerifier) VerifyMemory(ctx context.Context, memoryID string) (core.VerificationReport, error) {
	f.calls++
	if err, ok := f.errs[memoryID]; ok {
		return core.VerificationReport{}, err
	}
	if r, ok := f.reports[memoryID]; ok {
		return r, nil
	}
	return core.VerificationReport{}, fmt.Errorf("unexpected memory %s in fake verifier", memoryID)
}

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

func reconstructScope() core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: "org-001",
		CompanyID:      "acme",
		RUC:            "20100039201",
		Period:         "202401",
	}
}

func decisionMemory(id, topicKey string) core.AccountingMemory {
	lvl := core.MaterialityMaterial
	return core.AccountingMemory{
		Identity:         core.Identity{ID: id, TopicKey: topicKey},
		Title:            "material decision",
		Kind:             core.KindDecision,
		Scope:            reconstructScope(),
		Content:          core.Content{What: "w", Why: "y", Where: "f", Learned: "x"},
		Status:           core.StatusApproved,
		FiscalEffect:     core.FiscalEffectJournalEntry,
		EffectiveAt:      "2026-01-15T12:00:00.000Z",
		RecordedAt:       "2026-01-15T12:00:00.000Z",
		Source:           core.Source{System: "go-test", ActorKind: core.ActorKindAgent},
		MaterialityLevel: &lvl,
		Revision:         1,
	}
}

func allPassReport(subjectID string) core.VerificationReport {
	layers := []core.VerificationLayer{}
	for _, name := range core.ReceiptLayerNames() {
		layers = append(layers, core.VerificationLayer{Name: name, Status: core.VerificationPassed, Detail: "ok"})
	}
	layers = append(layers,
		core.VerificationLayer{Name: core.LayerPrincipalProvenance, Status: core.VerificationPassed, Detail: "ok"},
		core.VerificationLayer{Name: core.LayerSupersessionChain, Status: core.VerificationPassed, Detail: "ok"},
		core.VerificationLayer{Name: core.LayerEvidenceAvailability, Status: core.VerificationPassed, Detail: "ok"},
		core.VerificationLayer{Name: core.LayerObjectAvailability, Status: core.VerificationPassed, Detail: "ok"},
		core.VerificationLayer{Name: core.LayerRuleAvailability, Status: core.VerificationPassed, Detail: "ok"},
		core.VerificationLayer{Name: core.LayerRuleVersionVigencia, Status: core.VerificationPassed, Detail: "ok"},
	)
	return core.VerificationReport{
		SubjectType: "memory",
		SubjectID:   subjectID,
		Outcome:     core.VerificationOutcomePassed,
		Receipts:    []core.ReceiptVerification{},
		Layers:      layers,
	}
}

func failedLayerReport(subjectID, failedLayer string) core.VerificationReport {
	r := allPassReport(subjectID)
	layers := make([]core.VerificationLayer, 0, len(r.Layers))
	for _, l := range r.Layers {
		if l.Name == failedLayer {
			l.Status = core.VerificationFailed
			l.Detail = "test failure"
		}
		layers = append(layers, l)
	}
	r.Layers = layers
	r.Outcome = core.VerificationOutcomeFailed
	return r
}

// ──────────────────────────────────────────────
// Service tests
// ──────────────────────────────────────────────

func TestReconstructibilityAssemblesFrozenResult(t *testing.T) {
	scope := reconstructScope()
	reader := &fakeMaterialReader{
		heads: []core.AccountingMemory{
			decisionMemory("mem-a", "decision/a"), // reconstructible
			decisionMemory("mem-b", "decision/b"), // missing evidence
			decisionMemory("mem-c", "decision/c"), // no report → receipt_failed
		},
	}
	verifier := &fakeVerifier{
		reports: map[string]core.VerificationReport{
			"mem-a": allPassReport("mem-a"),
			"mem-b": failedLayerReport("mem-b", core.LayerEvidenceAvailability),
		},
		errs: map[string]error{"mem-c": ErrNoReceipts},
	}

	got, err := Reconstructibility(context.Background(), reader, verifier, scope)
	if err != nil {
		t.Fatalf("Reconstructibility: %v", err)
	}
	want := ReconstructibilityResult{
		Scope:       scope,
		Period:      "202401",
		Denominator: 3,
		Numerator:   1,
		Ratio:       core.ReconstructibilityRatio{Numerator: 1, Denominator: 3},
		Percentage:  intPtr(33), // integer truncation: (1*100)/3 = 33
		Reasons: ReconstructibilityReasons{
			NotApproved:           []string{},
			ReceiptFailed:         []string{"mem-c"},
			MissingEvidence:       []string{"mem-b"},
			EvidenceMissingObject: []string{},
			RuleUnresolved:        []string{},
			RuleVersionFailed:     []string{},
		},
		ZeroDenominator: false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconstructibility() =\n%+v\nwant\n%+v", got, want)
	}
	if reader.calls != 1 || verifier.calls != 3 {
		t.Fatalf("reader calls = %d (want 1), verifier calls = %d (want 3)", reader.calls, verifier.calls)
	}
	if !core.ScopeEquals(reader.gotScope, scope) {
		t.Fatalf("reader got scope %+v, want the exact requested scope %+v", reader.gotScope, scope)
	}

	// Frozen JSON field order (D-4): scope, period, denominator, numerator,
	// ratio, percentage, reasons, zeroDenominator; reasons in vocabulary order.
	pinned := `{"scope":{"kind":"company","organizationId":"org-001","companyId":"acme","ruc":"20100039201","period":"202401"},"period":"202401","denominator":3,"numerator":1,"ratio":{"numerator":1,"denominator":3},"percentage":33,"reasons":{"not_approved":[],"receipt_failed":["mem-c"],"missing_evidence":["mem-b"],"evidence_missing_object":[],"rule_unresolved":[],"rule_version_failed":[]},"zeroDenominator":false}`
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if string(raw) != pinned {
		t.Fatalf("frozen JSON bytes differ:\n got %s\nwant %s", raw, pinned)
	}
}

func TestReconstructibilityZeroDenominatorFrozenShape(t *testing.T) {
	scope := reconstructScope()
	reader := &fakeMaterialReader{heads: nil}
	verifier := &fakeVerifier{reports: map[string]core.VerificationReport{}}

	got, err := Reconstructibility(context.Background(), reader, verifier, scope)
	if err != nil {
		t.Fatalf("Reconstructibility: %v", err)
	}
	want := ReconstructibilityResult{
		Scope:           scope,
		Period:          "202401",
		Denominator:     0,
		Numerator:       0,
		Ratio:           core.ReconstructibilityRatio{Numerator: 0, Denominator: 0},
		Percentage:      nil,
		Reasons:         newEmptyReasons(),
		ZeroDenominator: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zero-denominator result =\n%+v\nwant\n%+v", got, want)
	}
	if got.Percentage != nil {
		t.Fatalf("zero denominator percentage = %d — must be null, NEVER a misleading 0%% or 100%%", *got.Percentage)
	}
	// Empty lists serialize as [] — never null — and the shape is frozen.
	pinned := `{"scope":{"kind":"company","organizationId":"org-001","companyId":"acme","ruc":"20100039201","period":"202401"},"period":"202401","denominator":0,"numerator":0,"ratio":{"numerator":0,"denominator":0},"percentage":null,"reasons":{"not_approved":[],"receipt_failed":[],"missing_evidence":[],"evidence_missing_object":[],"rule_unresolved":[],"rule_version_failed":[]},"zeroDenominator":true}`
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if string(raw) != pinned {
		t.Fatalf("frozen zero-denominator JSON differs:\n got %s\nwant %s", raw, pinned)
	}
}

func TestReconstructibilityErrNoReceiptsMapsToReceiptFailed(t *testing.T) {
	scope := reconstructScope()
	reader := &fakeMaterialReader{heads: []core.AccountingMemory{decisionMemory("mem-nr", "decision/nr")}}
	verifier := &fakeVerifier{errs: map[string]error{"mem-nr": ErrNoReceipts}}

	got, err := Reconstructibility(context.Background(), reader, verifier, scope)
	if err != nil {
		t.Fatalf("Reconstructibility: %v", err)
	}
	if got.Denominator != 1 || got.Numerator != 0 {
		t.Fatalf("metric = %d/%d, want 0/1", got.Numerator, got.Denominator)
	}
	if !reflect.DeepEqual(got.Reasons.ReceiptFailed, []string{"mem-nr"}) {
		t.Fatalf("receipt_failed = %v, want [mem-nr] (ErrNoReceipts is evidence, never a report-building error)", got.Reasons.ReceiptFailed)
	}
	if got.Percentage == nil || *got.Percentage != 0 {
		t.Fatalf("percentage = %v, want 0", got.Percentage)
	}
}

func TestReconstructibilityPersistenceErrorAbortsUnavailable(t *testing.T) {
	scope := reconstructScope()
	reader := &fakeMaterialReader{heads: []core.AccountingMemory{decisionMemory("mem-c", "decision/c")}}
	verifier := &fakeVerifier{errs: map[string]error{"mem-c": fmt.Errorf("database is corrupt: %w", errors.New("disk I/O error"))}}

	_, err := Reconstructibility(context.Background(), reader, verifier, scope)
	if err == nil {
		t.Fatal("expected an error for a persistence/decode failure")
	}
	if !errors.Is(err, ErrReconstructibilityUnavailable) {
		t.Fatalf("error = %v, want RECONSTRUCTIBILITY_UNAVAILABLE (a corrupted read is never mislabeled as a business reason)", err)
	}
	if strings.Contains(err.Error(), "receipt_failed") {
		t.Fatalf("unavailable error must not carry a business reason: %v", err)
	}
}

func TestReconstructibilityScopeMismatchAborts(t *testing.T) {
	scope := reconstructScope()
	outOfScope := decisionMemory("mem-x", "decision/x")
	outOfScope.Scope.CompanyID = "other-co"
	reader := &fakeMaterialReader{heads: []core.AccountingMemory{outOfScope}}
	verifier := &fakeVerifier{reports: map[string]core.VerificationReport{}}

	_, err := Reconstructibility(context.Background(), reader, verifier, scope)
	if err == nil {
		t.Fatal("expected RECONSTRUCTIBILITY_SCOPE_MISMATCH for an out-of-scope reader head")
	}
	if !errors.Is(err, ErrReconstructibilityScopeMismatch) {
		t.Fatalf("error = %v, want RECONSTRUCTIBILITY_SCOPE_MISMATCH", err)
	}
	if verifier.calls != 0 {
		t.Fatalf("verifier called %d times — out-of-scope rows must abort BEFORE any verification", verifier.calls)
	}
}

func TestReconstructibilityFailsClosedOnInvalidScope(t *testing.T) {
	reader := &fakeMaterialReader{}
	verifier := &fakeVerifier{reports: map[string]core.VerificationReport{}}

	tests := []struct {
		name    string
		scope   core.Scope
		wantErr string
	}{
		{
			name:    "institutional scope",
			scope:   core.Scope{Kind: core.ScopeKindInstitutional},
			wantErr: "INVALID_RECONSTRUCTIBILITY_SCOPE",
		},
		{
			name:    "missing period",
			scope:   core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-001", CompanyID: "acme", RUC: "20100039201"},
			wantErr: "INVALID_RECONSTRUCTIBILITY_SCOPE",
		},
		{
			name:    "malformed period",
			scope:   core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-001", CompanyID: "acme", RUC: "20100039201", Period: "2024x1"},
			wantErr: "INVALID_PERIOD",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Reconstructibility(context.Background(), reader, verifier, tt.scope)
			if err == nil {
				t.Fatalf("expected a typed failure for scope %+v", tt.scope)
			}
			if !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want prefix %q", err.Error(), tt.wantErr)
			}
			if reader.calls != 0 {
				t.Fatalf("reader called %d times — an invalid scope must fail closed BEFORE any read", reader.calls)
			}
		})
	}
}

func TestReconstructibilityDeterminism(t *testing.T) {
	scope := reconstructScope()
	// Deliberately reversed insertion order.
	reader := &fakeMaterialReader{
		heads: []core.AccountingMemory{
			decisionMemory("mem-z", "decision/z"),
			decisionMemory("mem-a", "decision/a"),
			decisionMemory("mem-m", "decision/m"),
		},
	}
	verifier := &fakeVerifier{
		reports: map[string]core.VerificationReport{
			"mem-a": allPassReport("mem-a"),
			"mem-z": failedLayerReport("mem-z", core.LayerRuleAvailability),
			"mem-m": failedLayerReport("mem-m", core.LayerRuleAvailability),
		},
	}

	first, err := Reconstructibility(context.Background(), reader, verifier, scope)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := Reconstructibility(context.Background(), reader, verifier, scope)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two runs against the same snapshot differ:\nfirst %+v\nsecond %+v", first, second)
	}
	// Reason IDs are sorted bytewise ("mem-m" < "mem-z"), never insertion order.
	wantRuleFailed := []string{"mem-m", "mem-z"}
	if !reflect.DeepEqual(first.Reasons.RuleUnresolved, wantRuleFailed) {
		t.Fatalf("rule_unresolved IDs = %v, want bytewise-sorted %v", first.Reasons.RuleUnresolved, wantRuleFailed)
	}
	// Byte-identical JSON (FR-9 vi).
	rawA, _ := json.Marshal(first)
	rawB, _ := json.Marshal(second)
	if string(rawA) != string(rawB) {
		t.Fatal("JSON bytes differ across two runs of the same snapshot")
	}
}

// ──────────────────────────────────────────────
// Real-store integration (read-only proof)
// ──────────────────────────────────────────────

func TestReconstructibilityRealStoreIntegrationReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metric.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	scope := reconstructScope()
	seeded := []string{}
	for _, topic := range []string{"decision/alpha", "decision/beta"} {
		result, err := s.Save(core.SaveInput{
			TopicKey:         topic,
			Title:            "material decision",
			Kind:             core.KindDecision,
			Scope:            scope,
			Content:          core.Content{What: "w", Why: "y", Where: "f", Learned: "x"},
			FiscalEffect:     core.FiscalEffectJournalEntry,
			EffectiveAt:      "2026-01-15T12:00:00.000Z",
			Source:           core.Source{System: "go-test", ActorID: "agent", ActorKind: core.ActorKindAgent},
			MaterialityLevel: ptr(core.MaterialityMaterial),
		})
		if err != nil {
			t.Fatalf("save %s: %v", topic, err)
		}
		if _, err := s.ApplyStatusTransition(result.Memory.Identity.ID, core.StatusApproved, core.TransitionMeta{
			Actor: "maria.torres", ActorKind: core.ActorKindHuman, Timestamp: "2026-01-16T12:00:00.000Z",
		}); err != nil {
			t.Fatalf("approve %s: %v", topic, err)
		}
		seeded = append(seeded, result.Memory.Identity.ID)
	}

	before := storeFileHash(t, path)
	beforeList, err := s.List()
	if err != nil {
		t.Fatalf("list before: %v", err)
	}

	// Real wiring: SQLiteStore as the reader, the memoryVerifierAdapter over the
	// real verification service as the verifier. No receipt chains were minted,
	// so every eligible head maps through ErrNoReceipts → receipt_failed — proof
	// that the service+store+verifier compose without mutating anything.
	reader := materialReaderAdapter{s}
	verifier := memoryVerifierAdapter{store: s}
	first, err := Reconstructibility(context.Background(), reader, verifier, scope)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := Reconstructibility(context.Background(), reader, verifier, scope)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if first.Denominator != 2 || first.Numerator != 0 {
		t.Fatalf("metric = %d/%d, want 0/2 (approved material heads without receipt chains)", first.Numerator, first.Denominator)
	}
	if !reflect.DeepEqual(first.Reasons.ReceiptFailed, []string{seeded[0], seeded[1]}) && !reflect.DeepEqual(first.Reasons.ReceiptFailed, []string{seeded[1], seeded[0]}) {
		t.Fatalf("receipt_failed = %v, want the two seeded heads", first.Reasons.ReceiptFailed)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("double-run determinism failed against the real store")
	}

	// Read-only proof (AC-8): the database file bytes and the logical observation
	// state are unchanged after two full metric runs.
	after := storeFileHash(t, path)
	if after != before {
		t.Fatalf("store file hash changed across read-only metric runs:\nbefore %s\nafter  %s", before, after)
	}
	afterList, err := s.List()
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if !reflect.DeepEqual(afterList, beforeList) {
		t.Fatal("observation rows changed across read-only metric runs")
	}
}

// materialReaderAdapter adapts *store.SQLiteStore to the narrow MaterialDecisionReader seam.
type materialReaderAdapter struct {
	s *store.SQLiteStore
}

func (a materialReaderAdapter) LatestMaterialDecisionHeads(ctx context.Context, scope core.Scope) ([]core.AccountingMemory, error) {
	return a.s.LatestMaterialDecisionHeads(ctx, scope)
}

func storeFileHash(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store file: %v", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ptr(v core.MaterialityLevel) *core.MaterialityLevel { return &v }

func intPtr(v int) *int { return &v }

// newEmptyReasons returns a reasons struct with non-nil empty lists — the exact
// shape idsOrEmpty produces ([] never null).
func newEmptyReasons() ReconstructibilityReasons {
	return ReconstructibilityReasons{
		NotApproved:           []string{},
		ReceiptFailed:         []string{},
		MissingEvidence:       []string{},
		EvidenceMissingObject: []string{},
		RuleUnresolved:        []string{},
		RuleVersionFailed:     []string{},
	}
}
