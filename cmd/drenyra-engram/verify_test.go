// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the OFFLINE verification
// CLI surface (v0.4.0 Step 4 — docs/architecture/offline-verification-step4.md
// §6) through the built binary: the three command forms, hash/numeric receipt
// selectors, indent-2 JSON for BOTH outcomes, and the exit contract 0=passed /
// 1=layer failure / 2=usage-or-incomplete. AC7 (a direct-SQL-removed evidence
// link fails the evidence-availability layer) and AC12 (every serialized report
// ends with `Accounting correctness: NOT ASSERTED`) hold on the CLI surface.
//
// Every CLI invocation that can mint a receipt pins the signer keyring to a
// fresh temp path via $DRENYRA_ENGRAM_SIGNING_KEY — the tests never touch the
// real user config dir.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// verifyCLIEnv returns an env override that pins the built binary's keyring to
// a fresh temp path (receipt emission) and STRIPS any ambient
// DRENYRA_ENGRAM_SIGNING_KEY / DRENYRA_ENGRAM_SESSION overrides — os.Getenv
// returns the FIRST duplicate, so an ambient value would silently win over an
// appended override. Extra env pairs (e.g. the session-file env for
// authenticated commands) are appended last.
func verifyCLIEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	env := []string{"DRENYRA_ENGRAM_SIGNING_KEY=" + filepath.Join(t.TempDir(), "signing-keys.json")}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "DRENYRA_ENGRAM_SIGNING_KEY=") || strings.HasPrefix(kv, "DRENYRA_ENGRAM_SESSION=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// verifySaveCLI saves one informative memory through the built binary WITH the
// test keyring env (so the covered save mints a memory_recorded receipt) and
// returns its id.
func verifySaveCLI(t *testing.T, env []string, db string) string {
	t.Helper()
	dir := t.TempDir()
	path := writeSaveInput(t, dir, "verify-memory.json",
		cliSaveInput("verify/cli/memory", cliRucA, "202401", "saldo de mayor 4011", "smoke-verify-1"))
	stdout, stderr, code := runCLIEnv(t, env, "save", path, "--db", db)
	if code != 0 {
		t.Fatalf("verify save failed (exit %d): %s", code, stderr)
	}
	var result struct {
		Memory struct {
			Identity struct {
				ID string `json:"id"`
			} `json:"identity"`
		} `json:"memory"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("verify save output not JSON: %v\n%s", err, stdout)
	}
	if result.Memory.Identity.ID == "" {
		t.Fatal("verify save returned an empty id")
	}
	return result.Memory.Identity.ID
}

// verifyReceiptLocator returns the portable hash AND the local SQLite row id of
// the subject's first receipt (ordered issued_at, rowid) — the two selectors
// the `verify receipt` command accepts.
func verifyReceiptLocator(t *testing.T, db string, subjectType core.SubjectType, subjectID string) (hash string, id int64) {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	chain, err := st.ReceiptsForSubject(context.Background(), subjectType, subjectID)
	if err != nil {
		t.Fatalf("receipts for subject: %v", err)
	}
	if len(chain) == 0 {
		t.Fatalf("subject %s %s has no receipts", subjectType, subjectID)
	}
	hash = core.ReceiptHash(chain[0])
	_, _, id, err = st.ReceiptPayloadByHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("receipt payload by hash: %v", err)
	}
	return hash, id
}

// parseVerifyReport decodes the CLI's indent-2 JSON verification report.
func parseVerifyReport(t *testing.T, stdout string) core.VerificationReport {
	t.Helper()
	var report core.VerificationReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("verify output not JSON: %v\n%s", err, stdout)
	}
	return report
}

// assertNotAsserted checks AC12 on the CLI surface: the decoded report carries
// the exact NOT ASSERTED conclusion and the serialized JSON contains it as the
// final value.
func assertNotAsserted(t *testing.T, stdout string, report core.VerificationReport) {
	t.Helper()
	if report.AccountingCorrectness != core.AccountingCorrectnessNotAsserted {
		t.Fatalf("accountingCorrectness = %q, want %q", report.AccountingCorrectness, core.AccountingCorrectnessNotAsserted)
	}
	if !strings.Contains(stdout, `"accountingCorrectness": "Accounting correctness: NOT ASSERTED"`) {
		t.Fatalf("serialized report must carry the exact conclusion: %s", stdout)
	}
}

// TestCLIVerifyMemoryValid: a freshly saved memory (one memory_recorded receipt)
// verifies clean — exit 0 and an indent-2 JSON report with the NOT ASSERTED
// conclusion (AC12 on the CLI surface).
func TestCLIVerifyMemoryValid(t *testing.T) {
	env := verifyCLIEnv(t)
	db := filepath.Join(t.TempDir(), "engram.db")
	id := verifySaveCLI(t, env, db)

	stdout, stderr, code := runCLIEnv(t, env, "verify", "memory", id, "--db", db)
	if code != 0 {
		t.Fatalf("verify memory exit = %d, want 0; stderr=%q", code, stderr)
	}
	report := parseVerifyReport(t, stdout)
	if report.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("outcome = %s, want passed (%+v)", report.Outcome, report)
	}
	if report.SubjectType != "memory" || report.SubjectID != id {
		t.Fatalf("subject = %s/%s, want memory/%s", report.SubjectType, report.SubjectID, id)
	}
	if len(report.Receipts) != 1 {
		t.Fatalf("receipts = %d, want 1 (memory_recorded)", len(report.Receipts))
	}
	assertNotAsserted(t, stdout, report)
}

// TestCLIVerifyReceiptByHashAndID: the standalone receipt selector accepts both
// the portable 64-hex hash and the local SQLite row id; both verify the same
// receipt with exit 0.
func TestCLIVerifyReceiptByHashAndID(t *testing.T) {
	env := verifyCLIEnv(t)
	db := filepath.Join(t.TempDir(), "engram.db")
	id := verifySaveCLI(t, env, db)
	hash, rowID := verifyReceiptLocator(t, db, core.SubjectTypeMemory, id)

	for _, tc := range []struct {
		name string
		arg  string
	}{
		{"hash", hash},
		{"id", strconv.FormatInt(rowID, 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCLIEnv(t, env, "verify", "receipt", tc.arg, "--db", db)
			if code != 0 {
				t.Fatalf("verify receipt %s exit = %d, want 0; stderr=%q", tc.arg, code, stderr)
			}
			report := parseVerifyReport(t, stdout)
			if report.Outcome != core.VerificationOutcomePassed {
				t.Fatalf("outcome = %s, want passed", report.Outcome)
			}
			if report.SubjectID != id {
				t.Fatalf("subjectId = %s, want %s", report.SubjectID, id)
			}
			assertNotAsserted(t, stdout, report)
		})
	}
}

// TestCLIVerifyReceiptTargetUsage: a receipt target that is neither 64 lowercase
// hex digits nor a decimal int64 is a usage error (exit 2) BEFORE the store is
// touched; a well-formed hash that resolves to NO stored receipt is a not-found
// target (exit 2) with no stdout report.
func TestCLIVerifyReceiptTargetUsage(t *testing.T) {
	env := verifyCLIEnv(t)
	db := filepath.Join(t.TempDir(), "engram.db")

	t.Run("malformed target", func(t *testing.T) {
		stdout, stderr, code := runCLIEnv(t, env, "verify", "receipt", "not-a-target", "--db", db)
		if code != 2 {
			t.Fatalf("verify receipt malformed exit = %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if stdout != "" {
			t.Fatalf("usage error must not write stdout: %q", stdout)
		}
	})

	t.Run("uppercase hex is not a hash target", func(t *testing.T) {
		// Exactly 64 UPPERCASE hex digits: not the lowercase-hash selector, not a
		// decimal int64 → usage error (the CLI hash selector is lowercase-only).
		upper := strings.ToUpper(strings.Repeat("ab", 32))
		stdout, _, code := runCLIEnv(t, env, "verify", "receipt", upper, "--db", db)
		if code != 2 {
			t.Fatalf("verify receipt uppercase exit = %d, want 2; stdout=%q", code, stdout)
		}
		if stdout != "" {
			t.Fatalf("usage error must not write stdout: %q", stdout)
		}
	})

	t.Run("unknown hash is not-found", func(t *testing.T) {
		missing := strings.Repeat("0", 64)
		stdout, stderr, code := runCLIEnv(t, env, "verify", "receipt", missing, "--db", db)
		if code != 2 {
			t.Fatalf("verify receipt unknown hash exit = %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if stdout != "" {
			t.Fatalf("not-found target must not write stdout: %q", stdout)
		}
	})
}

// TestCLIVerifyUnknownTarget: an unknown memory or judgment id is a not-found
// target → exit 2 with no stdout report; unknown/missing verify subcommands are
// usage errors (exit 2).
func TestCLIVerifyUnknownTarget(t *testing.T) {
	env := verifyCLIEnv(t)
	db := filepath.Join(t.TempDir(), "engram.db")

	t.Run("unknown memory", func(t *testing.T) {
		stdout, stderr, code := runCLIEnv(t, env, "verify", "memory", "missing-memory", "--db", db)
		if code != 2 {
			t.Fatalf("verify memory unknown exit = %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if stdout != "" {
			t.Fatalf("not-found target must not write stdout: %q", stdout)
		}
	})

	t.Run("unknown judgment", func(t *testing.T) {
		stdout, stderr, code := runCLIEnv(t, env, "verify", "judgment", "missing-judgment", "--db", db)
		if code != 2 {
			t.Fatalf("verify judgment unknown exit = %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if stdout != "" {
			t.Fatalf("not-found target must not write stdout: %q", stdout)
		}
	})

	t.Run("unknown subcommand", func(t *testing.T) {
		_, _, code := runCLIEnv(t, env, "verify", "bogus")
		if code != 2 {
			t.Fatalf("verify bogus exit = %d, want 2", code)
		}
	})

	t.Run("verify without subcommand", func(t *testing.T) {
		_, _, code := runCLIEnv(t, env, "verify")
		if code != 2 {
			t.Fatalf("verify alone exit = %d, want 2", code)
		}
	})

	t.Run("memory without id", func(t *testing.T) {
		_, _, code := runCLIEnv(t, env, "verify", "memory", "--db", db)
		if code != 2 {
			t.Fatalf("verify memory without id exit = %d, want 2", code)
		}
	})

	t.Run("receipt without target", func(t *testing.T) {
		_, _, code := runCLIEnv(t, env, "verify", "receipt", "--db", db)
		if code != 2 {
			t.Fatalf("verify receipt without target exit = %d, want 2", code)
		}
	})
}

// TestCLIVerifyMemoryRemovedEvidence (AC7 on the CLI surface): a memory whose
// envelope commits an evidence ref whose link row was removed through direct
// SQL (the API has no delete path) exits 1 and emits a failed
// evidence-availability layer — still ending with the NOT ASSERTED conclusion
// (AC12).
func TestCLIVerifyMemoryRemovedEvidence(t *testing.T) {
	env := verifyCLIEnv(t)
	db := filepath.Join(t.TempDir(), "engram.db")
	id := verifySaveCLI(t, env, db)

	// Baseline: the fresh memory verifies clean.
	stdout, stderr, code := runCLIEnv(t, env, "verify", "memory", id, "--db", db)
	if code != 0 {
		t.Fatalf("baseline verify exit = %d, want 0; stderr=%q", code, stderr)
	}

	// Link evidence through the CLI (mints an evidence_linked receipt); the
	// memory still verifies clean.
	stdout, stderr, code = runCLIEnv(t, env, "link-evidence", id, "--ref", "xml:FA01-0001", "--db", db)
	if code != 0 {
		t.Fatalf("link-evidence failed (exit %d): %s", code, stderr)
	}
	stdout, stderr, code = runCLIEnv(t, env, "verify", "memory", id, "--db", db)
	if code != 0 {
		t.Fatalf("verify after link exit = %d, want 0; stderr=%q", code, stderr)
	}

	// Bypass the append-only API: remove the link row through a direct SQLite
	// connection to the same database file.
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.ExecContext(context.Background(), `DELETE FROM evidence_links WHERE memory_id = ?`, id); err != nil {
		t.Fatalf("remove link row: %v", err)
	}

	// The verification now FAILS the evidence-availability layer: exit 1, but
	// the report is still emitted as JSON and ends with NOT ASSERTED (a layer
	// failure is evidence, never a store error — design §6).
	stdout, stderr, code = runCLIEnv(t, env, "verify", "memory", id, "--db", db)
	if code != 1 {
		t.Fatalf("verify after removal exit = %d, want 1; stderr=%q", code, stderr)
	}
	report := parseVerifyReport(t, stdout)
	if report.Outcome != core.VerificationOutcomeFailed {
		t.Fatalf("outcome = %s, want failed", report.Outcome)
	}
	var evidenceFailed bool
	for _, layer := range report.Layers {
		if layer.Name == "evidence availability" {
			evidenceFailed = layer.Status == core.VerificationFailed
		}
	}
	if !evidenceFailed {
		t.Fatalf("evidence availability layer must fail after link removal: %+v", report.Layers)
	}
	assertNotAsserted(t, stdout, report)
}

// TestCLIVerifyJudgment: the authenticated judgment round trip (propose +
// confirm through the CLI, so the confirm mints a relation_confirmed receipt)
// verifies clean — exit 0 with the NOT ASSERTED conclusion.
func TestCLIVerifyJudgment(t *testing.T) {
	env := verifyCLIEnv(t)
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	sessionEnv := verifyCLIEnv(t, writeSessionFile(t, t.TempDir(), token)...)

	fromID := verifySaveCLI(t, env, db)
	toID := verifySaveCLI(t, env, db)
	proposed := proposeJudgmentCLI(t, db, fromID, toID)
	hash := judgmentHashCLI(t, db, proposed.JudgmentID)

	stdout, stderr, code := runCLIEnv(t, sessionEnv,
		"judge", "confirm", proposed.JudgmentID,
		"--resolution", "el mayor y el SIRE concilian tras el ajuste por comprobante tardio",
		"--expected-hash", hash, "--db", db)
	if code != 0 {
		t.Fatalf("judge confirm failed (exit %d): %s", code, stderr)
	}

	stdout, stderr, code = runCLIEnv(t, env, "verify", "judgment", proposed.JudgmentID, "--db", db)
	if code != 0 {
		t.Fatalf("verify judgment exit = %d, want 0; stderr=%q", code, stderr)
	}
	report := parseVerifyReport(t, stdout)
	if report.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("outcome = %s, want passed (%+v)", report.Outcome, report)
	}
	if report.SubjectType != "judgment" || report.SubjectID != proposed.JudgmentID {
		t.Fatalf("subject = %s/%s, want judgment/%s", report.SubjectType, report.SubjectID, proposed.JudgmentID)
	}
	assertNotAsserted(t, stdout, report)
}

// TestCLIHelpListsVerifyCommands: help documents the verify surface.
func TestCLIHelpListsVerifyCommands(t *testing.T) {
	stdout, _, code := runCLI(t, "help")
	if code != 0 {
		t.Fatalf("help exit = %d, want 0", code)
	}
	for _, line := range []string{
		"verify memory <id>", "verify judgment <id>", "verify receipt <hash|id>",
	} {
		if !strings.Contains(stdout, line) {
			t.Fatalf("help output missing %q", line)
		}
	}
}
