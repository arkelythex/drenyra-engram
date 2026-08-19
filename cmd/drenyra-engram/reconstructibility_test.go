// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; the reconstructibility percentage is integer
// math. This module tests the G-10 CLI adapter (design D-1): the
// `drenyra-engram reconstructibility <ruc> --period <YYYYMM>` command is a
// read-only observation — JSON to stdout (pretty-printed per the repo's CLI
// `emit` convention), exit 0 even when the denominator is zero, exit 2 with a
// stable code on stderr for invalid scope/period or an unavailable/corrupt read
// (the metric has NO failed-metric exit 1). The stdout JSON document equals the
// canonical frozen bytes (json.Marshal of the engine result).
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/server"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// seedCLIReconstructibilityFixture seeds a CLI-shaped store: the exact scope the
// session-less CLI derives (organizationId "cli", companyId := ruc), two
// approved material decisions WITHOUT receipt chains (→ receipt_failed), and
// returns their ids.
func seedCLIReconstructibilityFixture(t *testing.T, dbPath, ruc, period string) []string {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: cliOrganizationID, CompanyID: ruc, RUC: ruc, Period: period}
	lvl := core.MaterialityMaterial
	ids := []string{}
	for _, topic := range []string{"decision/alpha", "decision/beta"} {
		result, err := st.Save(core.SaveInput{
			TopicKey:         topic,
			Title:            "material decision fixture",
			Kind:             core.KindDecision,
			Scope:            scope,
			Content:          core.Content{What: "w", Why: "y", Where: "f", Learned: "x"},
			FiscalEffect:     core.FiscalEffectJournalEntry,
			EffectiveAt:      "2026-01-15T12:00:00.000Z",
			Source:           core.Source{System: "go-test", ActorID: "agent", ActorKind: core.ActorKindAgent},
			MaterialityLevel: &lvl,
			Confidence:       0.8,
		})
		if err != nil {
			t.Fatalf("save %s: %v", topic, err)
		}
		if _, err := st.ApplyStatusTransition(result.Memory.Identity.ID, core.StatusApproved, core.TransitionMeta{
			Actor: "maria.torres", ActorKind: core.ActorKindHuman, Timestamp: "2026-01-16T12:00:00.000Z",
		}); err != nil {
			t.Fatalf("approve %s: %v", topic, err)
		}
		ids = append(ids, result.Memory.Identity.ID)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return ids
}

// cliReconstructibilityScope is the exact scope the CLI derives for a RUC.
func cliReconstructibilityScope(ruc, period string) core.Scope {
	return core.Scope{Kind: core.ScopeKindCompany, OrganizationID: cliOrganizationID, CompanyID: ruc, RUC: ruc, Period: period}
}

// expectedCLIReconstructibilityResult is the frozen result the seeded fixture
// must produce (denominator = len(ids), numerator 0, receipt_failed both heads,
// ids bytewise sorted, integer percentage 0).
func expectedCLIReconstructibilityResult(ruc, period string, ids []string) server.ReconstructibilityResult {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	return server.ReconstructibilityResult{
		Scope:       cliReconstructibilityScope(ruc, period),
		Period:      period,
		Denominator: len(sorted),
		Numerator:   0,
		Ratio:       core.ReconstructibilityRatio{Numerator: 0, Denominator: len(sorted)},
		Percentage:  intPtrCLI(0),
		Reasons: server.ReconstructibilityReasons{
			NotApproved:           []string{},
			ReceiptFailed:         sorted,
			MissingEvidence:       []string{},
			EvidenceMissingObject: []string{},
			RuleUnresolved:        []string{},
			RuleVersionFailed:     []string{},
		},
		ZeroDenominator: false,
	}
}

func intPtrCLI(v int) *int { return &v }

// TestCLIReconstructibilitySmoke is the CLI smoke: run the real binary, parse
// the stdout JSON, assert the frozen result shape (D-4) and the FZ-1/FZ-2
// aggregates over the seeded fixture (denominator 2, numerator 0,
// receipt_failed = both heads).
func TestCLIReconstructibilitySmoke(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	ids := seedCLIReconstructibilityFixture(t, db, cliRucA, "202401")

	stdout, stderr, code := runCLI(t, "reconstructibility", cliRucA, "--period", "202401", "--db", db)
	if code != 0 {
		t.Fatalf("reconstructibility exited %d; stderr: %s", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr on success: %q", stderr)
	}
	var got server.ReconstructibilityResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not the result JSON: %v\n%s", err, stdout)
	}
	want := expectedCLIReconstructibilityResult(cliRucA, "202401", ids)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CLI result =\n%+v\nwant\n%+v", got, want)
	}
	// The stdout JSON DOCUMENT is the canonical frozen bytes: re-marshalling the
	// parsed result yields exactly json.Marshal of the engine result (field
	// order frozen by the struct tags — D-4). The CLI pretty-prints per the repo
	// convention, so the comparison is on the document, not whitespace.
	compact, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if string(compact) != string(mustMarshal(t, want)) {
		t.Fatalf("CLI JSON document differs from the canonical bytes:\n got %s\nwant %s", compact, mustMarshal(t, want))
	}
	// Pretty output still carries the frozen skeleton (scope first, then period,
	// denominator, numerator, ratio, percentage, reasons, zeroDenominator): the
	// reasons object opens with the pretty receipt_failed array.
	if !strings.Contains(stdout, `"receipt_failed": [`+"\n") {
		t.Fatalf("pretty output must carry the pretty receipt_failed array:\n%s", stdout)
	}
}

// TestCLIReconstructibilityZeroDenominatorExitZero: a valid scope with no
// material decisions is DATA (exit 0), never a command failure.
func TestCLIReconstructibilityZeroDenominatorExitZero(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	stdout, stderr, code := runCLI(t, "reconstructibility", cliRucA, "--period", "202401", "--db", db)
	if code != 0 {
		t.Fatalf("zero-denominator exited %d; stderr: %s", code, stderr)
	}
	var got server.ReconstructibilityResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if !got.ZeroDenominator || got.Denominator != 0 || got.Numerator != 0 {
		t.Fatalf("result = %+v — want exactly {denominator:0,numerator:0,zeroDenominator:true}", got)
	}
	if got.Percentage != nil {
		t.Fatalf("percentage = %d — must be null for a zero denominator", *got.Percentage)
	}
	want := server.ReconstructibilityResult{
		Scope:           cliReconstructibilityScope(cliRucA, "202401"),
		Period:          "202401",
		Denominator:     0,
		Numerator:       0,
		Ratio:           core.ReconstructibilityRatio{Numerator: 0, Denominator: 0},
		Percentage:      nil,
		Reasons:         server.ReconstructibilityReasons{NotApproved: []string{}, ReceiptFailed: []string{}, MissingEvidence: []string{}, EvidenceMissingObject: []string{}, RuleUnresolved: []string{}, RuleVersionFailed: []string{}},
		ZeroDenominator: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zero-denominator result =\n%+v\nwant\n%+v", got, want)
	}
	compact, _ := json.Marshal(got)
	if string(compact) != string(mustMarshal(t, want)) {
		t.Fatalf("zero-denominator document differs:\n got %s\nwant %s", compact, mustMarshal(t, want))
	}
}

// TestCLIReconstructibilityUsageErrorsExitTwo: invalid/ambiguous scope or period
// and a corrupt read all exit 2 with the stable code on stderr — the metric has
// no failed-metric exit 1 (design D-1).
func TestCLIReconstructibilityUsageErrorsExitTwo(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	seedCLIReconstructibilityFixture(t, db, cliRucA, "202401")

	corruptDB := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(corruptDB, []byte("this is not a sqlite database"), 0o600); err != nil {
		t.Fatalf("write corrupt db: %v", err)
	}

	tests := []struct {
		name     string
		args     []string
		wantCode string
	}{
		{"missing period", []string{"reconstructibility", cliRucA, "--db", db}, "INVALID_PERIOD"},
		{"malformed period", []string{"reconstructibility", cliRucA, "--period", "202413", "--db", db}, "INVALID_PERIOD"},
		{"invalid ruc", []string{"reconstructibility", "123", "--period", "202401", "--db", db}, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"missing ruc argument", []string{"reconstructibility", "--period", "202401", "--db", db}, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"corrupt database", []string{"reconstructibility", cliRucA, "--period", "202401", "--db", corruptDB}, "RECONSTRUCTIBILITY_UNAVAILABLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, code := runCLI(t, tt.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2; stderr: %s", code, stderr)
			}
			if stdout != "" {
				t.Fatalf("an error must not emit a result on stdout: %q", stdout)
			}
			if !strings.Contains(stderr, tt.wantCode) {
				t.Fatalf("stderr %q must carry the stable code %q", stderr, tt.wantCode)
			}
		})
	}
}

// TestCLIReconstructibilityExplicitCompanyID triangulates the --company-id /
// --organization overrides (design D-1): the session-less CLI derives companyId
// := ruc under the fixed CLI organization ONLY when the flags are omitted; an
// explicit company identity queries the exact scope it names.
func TestCLIReconstructibilityExplicitCompanyID(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	customOrg := "cli-custom-org"
	customCompany := "co_x"
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: customOrg, CompanyID: customCompany, RUC: cliRucA, Period: "202401"}
	lvl := core.MaterialityMaterial
	result, err := st.Save(core.SaveInput{
		TopicKey: "decision/custom", Title: "material decision fixture", Kind: core.KindDecision,
		Scope: scope, Content: core.Content{What: "w", Why: "y", Where: "f", Learned: "x"},
		FiscalEffect: core.FiscalEffectJournalEntry, EffectiveAt: "2026-01-15T12:00:00.000Z",
		Source: core.Source{System: "go-test", ActorID: "agent", ActorKind: core.ActorKindAgent}, MaterialityLevel: &lvl,
		Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := st.ApplyStatusTransition(result.Memory.Identity.ID, core.StatusApproved, core.TransitionMeta{
		Actor: "maria.torres", ActorKind: core.ActorKindHuman, Timestamp: "2026-01-16T12:00:00.000Z",
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	stdout, stderr, code := runCLI(t, "reconstructibility", cliRucA, "--period", "202401",
		"--company-id", customCompany, "--organization", customOrg, "--db", db)
	if code != 0 {
		t.Fatalf("exit = %d; stderr: %s", code, stderr)
	}
	var got server.ReconstructibilityResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if got.Scope.OrganizationID != customOrg || got.Scope.CompanyID != customCompany {
		t.Fatalf("scope = %+v, want organization %q companyId %q (the explicit overrides)", got.Scope, customOrg, customCompany)
	}
	if got.Denominator != 1 || !reflect.DeepEqual(got.Reasons.ReceiptFailed, []string{result.Memory.Identity.ID}) {
		t.Fatalf("metric = %d/%d receipt_failed %v, want the custom-scope head", got.Numerator, got.Denominator, got.Reasons.ReceiptFailed)
	}

	// Without the overrides the CLI derives companyId := ruc under the fixed CLI
	// organization — the custom head is structurally invisible → zero denominator.
	stdout2, stderr2, code2 := runCLI(t, "reconstructibility", cliRucA, "--period", "202401", "--db", db)
	if code2 != 0 {
		t.Fatalf("derived-scope exit = %d; stderr: %s", code2, stderr2)
	}
	var got2 server.ReconstructibilityResult
	if err := json.Unmarshal([]byte(stdout2), &got2); err != nil {
		t.Fatalf("derived stdout is not JSON: %v", err)
	}
	if !got2.ZeroDenominator || got2.Denominator != 0 {
		t.Fatalf("derived scope must not see the custom-scope head: %+v", got2)
	}
	if strings.Contains(stdout2, result.Memory.Identity.ID) {
		t.Fatal("derived scope leaks the custom-scope head id")
	}
}

// TestCLIReconstructibilityUsageText: the top-level usage lists the command.
func TestCLIReconstructibilityUsageText(t *testing.T) {
	stdout, _, code := runCLI(t, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d", code)
	}
	if !strings.Contains(stdout, "reconstructibility <ruc> --period <YYYYMM>") {
		t.Fatalf("usage must list the reconstructibility command:\n%s", stdout)
	}
}

// ──────────────────────────────────────────────
// small helpers
// ──────────────────────────────────────────────

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
