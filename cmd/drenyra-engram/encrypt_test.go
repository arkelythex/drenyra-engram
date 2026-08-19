// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the legacy re-encryption
// operator command (sdd-060-legacy-reencrypt, FR-RE-1/3/5, AC-RE-5/6/7):
// dry-run report, --apply re-encryption, fail-closed without the key, usage
// errors. No monetary field exists anywhere in this file.
package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// seedLegacyCLIStore writes company-scope memories through a NO-KEY store (real
// legacy plaintext rows) and returns the db path.
func seedLegacyCLIStore(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "engram.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open plain store: %v", err)
	}
	defer func() { _ = st.Close() }()
	scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "cli", CompanyID: cliRucA, RUC: cliRucA, Period: "202401"}
	for i, topic := range []string{"legacy/cli/one", "legacy/cli/two"} {
		if _, err := st.Save(core.SaveInput{
			TopicKey: topic, Title: "legacy cli", Kind: core.KindFact, Scope: scope,
			Content:      core.Content{What: "legacy cli content " + string(rune('a'+i)), Why: "why", Where: "where", Learned: "learned"},
			FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2024-01-15T00:00:00Z",
			Source:     core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
			Confidence: 0.8,
		}); err != nil {
			t.Fatalf("save %s: %v", topic, err)
		}
	}
	return db
}

func TestCLIEncrypt(t *testing.T) {
	db := seedLegacyCLIStore(t)
	keyEnv := []string{"DRENYRA_ENCRYPTION_MASTER_KEY=" + strings.Repeat("42", 32)}

	t.Run("default dry-run reports and writes nothing", func(t *testing.T) {
		stdout, stderr, code := runCLIEnv(t, keyEnv, "encrypt", "--db", db)
		if code != 0 {
			t.Fatalf("encrypt dry-run failed (exit %d): %s", code, stderr)
		}
		var report struct {
			DryRun      bool `json:"dryRun"`
			TotalLegacy int  `json:"totalLegacy"`
			PerTenant   []struct {
				OrganizationID string `json:"organizationId"`
				LegacyRows     int    `json:"legacyRows"`
			} `json:"perTenant"`
		}
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatalf("dry-run output not JSON: %v\n%s", err, stdout)
		}
		if !report.DryRun || report.TotalLegacy != 2 || len(report.PerTenant) != 1 {
			t.Fatalf("dry-run report = %+v, want dryRun 2 legacy rows 1 tenant", report)
		}
		// Zero writes: rows still plaintext — a key-less read succeeds (an
		// encrypted row would fail ENCRYPTION_REQUIRED).
		_, stderr, code = runCLI(t, "context", cliRucA, "--period", "202401", "--db", db)
		if code != 0 {
			t.Fatalf("key-less context after dry-run failed (exit %d): %s — dry-run mutated rows", code, stderr)
		}
	})

	t.Run("apply re-encrypts; without the key reads fail closed", func(t *testing.T) {
		stdout, stderr, code := runCLIEnv(t, keyEnv, "encrypt", "--apply", "--db", db)
		if code != 0 {
			t.Fatalf("encrypt --apply failed (exit %d): %s\n%s", code, stderr, stdout)
		}
		var report struct {
			DryRun      bool `json:"dryRun"`
			TotalLegacy int  `json:"totalLegacy"`
		}
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatalf("apply output not JSON: %v\n%s", err, stdout)
		}
		if report.DryRun || report.TotalLegacy != 2 {
			t.Fatalf("apply report = %+v, want 2 re-encrypted", report)
		}
		// The legacy plaintext gap is closed: a key-less context read fails.
		_, stderr, code = runCLI(t, "context", cliRucA, "--period", "202401", "--db", db)
		if code == 0 || !strings.Contains(stderr, "ENCRYPTION_REQUIRED") {
			t.Fatalf("context without key after apply: code=%d stderr=%q, want ENCRYPTION_REQUIRED", code, stderr)
		}
		// Idempotent: a second apply re-encrypts nothing.
		stdout, _, code = runCLIEnv(t, keyEnv, "encrypt", "--apply", "--db", db)
		if code != 0 || !strings.Contains(stdout, `"totalLegacy": 0`) {
			t.Fatalf("second apply not idempotent: code=%d stdout=%s", code, stdout)
		}
	})

	t.Run("apply fails closed without the key", func(t *testing.T) {
		_, stderr, code := runCLI(t, "encrypt", "--apply", "--db", db)
		if code == 0 || !strings.Contains(stderr, "ENCRYPTION_REQUIRED") {
			t.Fatalf("no-key --apply: code=%d stderr=%q, want ENCRYPTION_REQUIRED", code, stderr)
		}
	})

	t.Run("usage errors", func(t *testing.T) {
		_, _, code := runCLIEnv(t, keyEnv, "encrypt", "--dry-run", "--apply", "--db", db)
		if code != 2 {
			t.Fatalf("--dry-run --apply exit = %d, want usage error 2", code)
		}
		_, _, code = runCLIEnv(t, keyEnv, "encrypt", "extra", "--db", db)
		if code != 2 {
			t.Fatalf("extra positional exit = %d, want usage error 2", code)
		}
	})
}
