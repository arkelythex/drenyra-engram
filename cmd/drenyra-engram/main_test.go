// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the CLI against fixtures
// whose content is structured text; there are no monetary fields, so no money
// value is asserted here.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/search"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

const (
	cliRucA = "20100039201"
	cliRucB = "20600995804"
)

var (
	repoRoot string
	binPath  string
)

// TestMain builds the CLI binary once and runs the suite against it — the CLI
// smoke (save → search → context round trip) runs through the real binary.
func TestMain(m *testing.M) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve repo root: %v\n", err)
		os.Exit(1)
	}
	repoRoot = root

	binDir, err := os.MkdirTemp("", "drenyra-engram-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "temp dir: %v\n", err)
		os.Exit(1)
	}
	binPath = filepath.Join(binDir, "drenyra-engram")

	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/drenyra-engram")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build drenyra-engram: %v\n%s", err, out)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(binDir)
	os.Exit(code)
}

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return runCLIEnv(t, nil, args...)
}

// runCLIEnv runs the built CLI with extra environment variables (the tests use
// $DRENYRA_ENGRAM_SESSION and $DRENYRA_ENV overrides so they never touch the real
// user config dir or environment state).
func runCLIEnv(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return stdout.String(), stderr.String(), code
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(repoRoot, "testdata", "observations", name)
}

// TestCLISaveSearchContextRoundTrip is the CLI smoke: save a fixture JSON →
// search from the same company finds it → search from another company returns
// NOTHING. It then saves the other tenant's identical-text/identical-topicKey
// fixture and asserts the stronger property: each company's search returns
// exactly its OWN observation, never the other tenant's.
func TestCLISaveSearchContextRoundTrip(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")

	// save company A's fixture.
	stdout, stderr, code := runCLI(t, "save", fixturePath(t, "a.json"), "--db", db)
	if code != 0 {
		t.Fatalf("save A failed (exit %d): %s", code, stderr)
	}
	var saveResult struct {
		Memory struct {
			Scope struct {
				RUC string `json:"ruc"`
			} `json:"scope"`
		} `json:"memory"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(stdout), &saveResult); err != nil {
		t.Fatalf("save output not JSON: %v\n%s", err, stdout)
	}
	if saveResult.Outcome != "created" {
		t.Fatalf("outcome = %s, want created", saveResult.Outcome)
	}
	if saveResult.Memory.Scope.RUC != cliRucA {
		t.Fatalf("saved scope ruc = %s, want %s", saveResult.Memory.Scope.RUC, cliRucA)
	}

	// search from the same company finds the observation.
	stdout, stderr, code = runCLI(t, "search", "IGV base rate", "--company", cliRucA, "--period", "202401", "--db", db)
	if code != 0 {
		t.Fatalf("search A failed (exit %d): %s", code, stderr)
	}
	var fromA []search.Result
	if err := json.Unmarshal([]byte(stdout), &fromA); err != nil {
		t.Fatalf("search A output not JSON: %v\n%s", err, stdout)
	}
	if len(fromA) != 1 {
		t.Fatalf("search from A returned %d results, want 1", len(fromA))
	}
	if fromA[0].Memory.Scope.RUC != cliRucA {
		t.Fatalf("search from A returned ruc %s, want %s", fromA[0].Memory.Scope.RUC, cliRucA)
	}

	// search from company B returns NOTHING before B has any memory
	// (mandatory cross-tenant isolation: A's observation is never retrievable
	// from a B query).
	for _, mode := range []string{"", "--any"} {
		args := []string{"search", "IGV base rate", "--company", cliRucB, "--period", "202401", "--db", db}
		if mode != "" {
			args = append(args, mode)
		}
		stdout, stderr, code := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("search B (%s) failed (exit %d): %s", mode, code, stderr)
		}
		var fromB []search.Result
		if err := json.Unmarshal([]byte(stdout), &fromB); err != nil {
			t.Fatalf("search B (%s) output not JSON: %v\n%s", mode, err, stdout)
		}
		if len(fromB) != 0 {
			t.Fatalf("LEAK via CLI: search from company B (%s) returned %d results, want 0 (A's memory is invisible)", mode, len(fromB))
		}
	}

	// Now save B's identical-text/identical-topicKey fixture and re-check the
	// stronger property: each company's search returns exactly its OWN
	// observation, never the other tenant's.
	_, stderr, code = runCLI(t, "save", fixturePath(t, "b.json"), "--db", db)
	if code != 0 {
		t.Fatalf("save B failed (exit %d): %s", code, stderr)
	}
	for _, mode := range []string{"", "--any"} {
		args := []string{"search", "IGV base rate", "--company", cliRucA, "--period", "202401", "--db", db}
		if mode != "" {
			args = append(args, mode)
		}
		stdout, stderr, code := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("search A (%s) failed (exit %d): %s", mode, code, stderr)
		}
		var fromA []search.Result
		if err := json.Unmarshal([]byte(stdout), &fromA); err != nil {
			t.Fatalf("search A (%s) output not JSON: %v\n%s", mode, err, stdout)
		}
		if len(fromA) != 1 || fromA[0].Memory.Scope.RUC != cliRucA {
			t.Fatalf("search from A (%s) = %+v, want exactly A's own observation", mode, fromA)
		}

		args = []string{"search", "IGV base rate", "--company", cliRucB, "--period", "202401", "--db", db}
		if mode != "" {
			args = append(args, mode)
		}
		stdout, stderr, code = runCLI(t, args...)
		if code != 0 {
			t.Fatalf("search B (%s) failed (exit %d): %s", mode, code, stderr)
		}
		var fromB []search.Result
		if err := json.Unmarshal([]byte(stdout), &fromB); err != nil {
			t.Fatalf("search B (%s) output not JSON: %v\n%s", mode, err, stdout)
		}
		if len(fromB) != 1 || fromB[0].Memory.Scope.RUC != cliRucB {
			t.Fatalf("search from B (%s) = %+v, want exactly B's own observation, never A's", mode, fromB)
		}
	}

	// context from company A surfaces A's current memory.
	stdout, stderr, code = runCLI(t, "context", cliRucA, "--period", "202401", "--db", db)
	if code != 0 {
		t.Fatalf("context A failed (exit %d): %s", code, stderr)
	}
	var ctxA []struct {
		Identity struct {
			TopicKey string `json:"topicKey"`
		} `json:"identity"`
		Scope struct {
			RUC string `json:"ruc"`
		} `json:"scope"`
	}
	if err := json.Unmarshal([]byte(stdout), &ctxA); err != nil {
		t.Fatalf("context A output not JSON: %v\n%s", err, stdout)
	}
	if len(ctxA) != 1 || ctxA[0].Scope.RUC != cliRucA || ctxA[0].Identity.TopicKey != "tax.igv.rate" {
		t.Fatalf("context A = %+v, want exactly A's observation", ctxA)
	}

	// doctor reports a healthy store.
	stdout, stderr, code = runCLI(t, "doctor", "--db", db)
	if code != 0 {
		t.Fatalf("doctor failed (exit %d): %s", code, stderr)
	}
	var report store.DoctorReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("doctor output not JSON: %v\n%s", err, stdout)
	}
	if report.SchemaVersion != 4 || report.Observations != 2 || report.RevisionChains != 2 {
		t.Fatalf("doctor report = %+v, want schemaVersion 4, 2 observations, 2 chains", report)
	}
}

func TestCLIHasNoAuthorizationCommands(t *testing.T) {
	// The public surface is non-authorizing (contracts/provenance.md): any
	// authorization-sounding command must be rejected as a usage error, never
	// succeed, never mutate.
	for _, forbidden := range []string{"authorize", "approve", "allow"} {
		t.Run(forbidden, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "engram.db")
			stdout, stderr, code := runCLI(t, forbidden, "--db", db)
			if code != 2 {
				t.Fatalf("%s exit = %d, want 2 (usage); stdout=%q stderr=%q", forbidden, code, stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("%s must not write to stdout: %q", forbidden, stdout)
			}
		})
	}
}

func TestCLIUsageErrors(t *testing.T) {
	t.Run("no arguments", func(t *testing.T) {
		_, _, code := runCLI(t)
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
	})

	t.Run("unknown command", func(t *testing.T) {
		_, _, code := runCLI(t, "frobnicate")
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
	})

	t.Run("search without company", func(t *testing.T) {
		_, _, code := runCLI(t, "search", "igv")
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
	})

	t.Run("search with malformed ruc", func(t *testing.T) {
		_, _, code := runCLI(t, "search", "igv", "--company", "123")
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
	})

	t.Run("help exits zero", func(t *testing.T) {
		stdout, _, code := runCLI(t, "help")
		if code != 0 {
			t.Fatalf("help exit = %d, want 0", code)
		}
		if !strings.Contains(stdout, "drenyra-engram") {
			t.Fatalf("help output missing binary name: %q", stdout)
		}
	})
}

// ──────────────────────────────────────────────
// Lifecycle CLI tests (review / promote / supersede / compare)
// ──────────────────────────────────────────────

// saveViaCLI saves the JSON file at path through the built CLI and returns the
// stored observation id.
func saveViaCLI(t *testing.T, db, path string) string {
	t.Helper()
	stdout, stderr, code := runCLI(t, "save", path, "--db", db)
	if code != 0 {
		t.Fatalf("save %s failed (exit %d): %s", path, code, stderr)
	}
	var result struct {
		Memory struct {
			Identity struct {
				ID string `json:"id"`
			} `json:"identity"`
		} `json:"memory"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("save output not JSON: %v\n%s", err, stdout)
	}
	if result.Outcome != "created" && result.Outcome != "updated" {
		t.Fatalf("save outcome = %s, want created/updated", result.Outcome)
	}
	if result.Memory.Identity.ID == "" {
		t.Fatal("save returned an empty id")
	}
	return result.Memory.Identity.ID
}

// cliSaveInput builds a save payload for the CLI's fixed company scope shape.
func cliSaveInput(topicKey, ruc, period, what, session string) core.SaveInput {
	return core.SaveInput{
		TopicKey: topicKey,
		Title:    "base rate",
		Kind:     core.KindRule,
		Scope: core.Scope{
			Kind:           core.ScopeKindCompany,
			OrganizationID: cliOrganizationID,
			CompanyID:      ruc,
			RUC:            ruc,
			Period:         period,
		},
		Content: core.Content{
			What:    what,
			Why:     "standard for goods",
			Where:   "Peru",
			Learned: "applies to all",
		},
		FiscalEffect: core.FiscalEffectNone,
		Source: core.Source{
			System:    "cli",
			ActorID:   "cli-user",
			ActorKind: core.ActorKindAgent,
			Session:   session,
		},
	}
}

// writeSaveInput marshals a save payload to a JSON file in dir and returns its
// path, so the built CLI can ingest it.
func writeSaveInput(t *testing.T, dir, name string, input core.SaveInput) string {
	t.Helper()
	data, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		t.Fatalf("marshal save input: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// runTransition runs a review/promote/supersede CLI command against id and
// returns the parsed JSON output.
func runTransition(t *testing.T, db string, args ...string) (string, string, int) {
	t.Helper()
	stdout, stderr, code := runCLI(t, append(args, "--db", db)...)
	if code != 0 {
		t.Fatalf("%v failed (exit %d): %s", args, code, stderr)
	}
	return stdout, stderr, code
}

// TestCLILifecycleRoundTrip drives the v2 lifecycle through the built binary:
// save (gated → pending_review) → approve (human gate) → supersede (with
// target), asserting each command's JSON shape and exit code against a fresh
// temp database.
func TestCLILifecycleRoundTrip(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")

	// A gated fixture (fiscalEffect adjustment) lands pending_review.
	idA := saveViaCLI(t, db, writeGatedFixture(t))

	// Approve through the AUTHENTICATED path: the store is seeded with an
	// identity + session, the session file is written (what auth login does), and
	// the command approves against the CURRENT envelope hash — caller-supplied
	// authority (--actor) is gone from the approve command (ADR-003).
	token := seedCLIIdentity(t, db)
	env := writeSessionFile(t, t.TempDir(), token)
	h1 := memoryEnvelope(t, db, idA)
	stdout, stderr, code := runCLIEnv(t, env, "approve", idA, "--expected-envelope", h1, "--reason", "revisado y conforme", "--db", db)
	if code != 0 {
		t.Fatalf("approve failed (exit %d): %s", code, stderr)
	}
	var approved struct {
		MemoryID      string `json:"memoryId"`
		CurrentStatus string `json:"currentStatus"`
	}
	if err := json.Unmarshal([]byte(stdout), &approved); err != nil {
		t.Fatalf("approve output not JSON: %v\n%s", err, stdout)
	}
	if approved.MemoryID != idA || approved.CurrentStatus != "approved" {
		t.Fatalf("approve output = %+v, want id %s approved", approved, idA)
	}

	idB := saveViaCLI(t, db, fixturePath(t, "b.json"))
	stdout, _, code = runTransition(t, db, "supersede", idA, "--target", idB, "--actor", "maria.torres")
	var superseded supersedeOutput
	if err := json.Unmarshal([]byte(stdout), &superseded); err != nil {
		t.Fatalf("supersede output not JSON: %v\n%s", err, stdout)
	}
	if superseded.ID != idA || superseded.From != core.StatusApproved || superseded.To != core.StatusSuperseded || superseded.TargetID != idB {
		t.Fatalf("supersede output = %+v, want id %s, approved → superseded, target %s", superseded, idA, idB)
	}

	// The supersedes relation is recorded; with idB still draft the verdict
	// falls back to the shared topicKey → related, not supersedes.
	stdout, stderr, code = runCLI(t, "compare", idA, idB, "--db", db)
	if code != 0 {
		t.Fatalf("compare failed (exit %d): %s", code, stderr)
	}
	var cmp compareOutput
	if err := json.Unmarshal([]byte(stdout), &cmp); err != nil {
		t.Fatalf("compare output not JSON: %v\n%s", err, stdout)
	}
	// idA was superseded by idB in this round trip → the verdict is
	// "supersedes" (source check: A is superseded, B is the successor). The two
	// fixtures have DIFFERENT topic keys, so identityMatch is false.
	if cmp.RelationVerdict != "supersedes" || cmp.IdentityMatch || cmp.ScopeMatch != "none" {
		t.Fatalf("compare(idA, idB) = %+v, want supersedes + identityMatch false + scopeMatch none", cmp)
	}
}

// TestCLICompareScenarios covers the compare verdict matrix: not_conflict,
// related (shared topicKey), partial/exact scope matching and the supersedes
// verdict for a completed supersede pair.
func TestCLICompareScenarios(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	dir := t.TempDir()

	idA := saveViaCLI(t, db, fixturePath(t, "a.json"))
	idC := saveViaCLI(t, db, writeSaveInput(t, dir, "c.json", cliSaveInput("memory.relations.probe.c", cliRucA, "202401", "a different finding", "smoke-003")))
	idA2 := saveViaCLI(t, db, fixturePath(t, "a.json")) // revision 2 of A's chain
	idB := saveViaCLI(t, db, fixturePath(t, "b.json"))

	// Different topicKey, no relation, identical scope → not_conflict + exact.
	stdout, stderr, code := runCLI(t, "compare", idA, idC, "--db", db)
	if code != 0 {
		t.Fatalf("compare A/C failed (exit %d): %s", code, stderr)
	}
	var cmp compareOutput
	if err := json.Unmarshal([]byte(stdout), &cmp); err != nil {
		t.Fatalf("compare A/C output not JSON: %v\n%s", err, stdout)
	}
	if cmp.IdentityMatch || cmp.RelationVerdict != "not_conflict" || cmp.ScopeMatch != "exact" {
		t.Fatalf("compare(A, C) = %+v, want identityMatch=false, not_conflict, scopeMatch=exact", cmp)
	}
	if !cmp.ContentDeltas.What {
		t.Fatalf("compare(A, C) contentDeltas = %+v, want differing content flagged", cmp.ContentDeltas)
	}

	// Same chain re-save → related, identical content → no deltas.
	stdout, _, code = runCLI(t, "compare", idA, idA2, "--db", db)
	if code != 0 {
		t.Fatalf("compare A/A2 failed (exit %d): %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &cmp); err != nil {
		t.Fatalf("compare A/A2 output not JSON: %v\n%s", err, stdout)
	}
	// v2: two saves of the same (topicKey, scope) chain — the first was
	// superseded automatically by the second; the verdict is supersedes with the
	// identical content unchanged.
	if !cmp.IdentityMatch || cmp.RelationVerdict != "supersedes" || cmp.ScopeMatch != "exact" {
		t.Fatalf("compare(A, A2) = %+v, want supersedes + identityMatch + scopeMatch exact", cmp)
	}
	if cmp.ContentDeltas.What || cmp.ContentDeltas.Why || cmp.ContentDeltas.Where || cmp.ContentDeltas.Learned {
		t.Fatalf("compare(A, A2) contentDeltas = %+v, want all false for identical content", cmp.ContentDeltas)
	}

	// Same topicKey across different companies → related, scopeMatch none.
	stdout, _, code = runCLI(t, "compare", idA, idB, "--db", db)
	if code != 0 {
		t.Fatalf("compare A/B failed (exit %d): %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &cmp); err != nil {
		t.Fatalf("compare A/B output not JSON: %v\n%s", err, stdout)
	}
	if !cmp.IdentityMatch || cmp.RelationVerdict != "related" || cmp.ScopeMatch != "none" {
		t.Fatalf("compare(A, B) = %+v, want related + identityMatch + scopeMatch none", cmp)
	}

	// Same company/RUC, different period → partial scope.
	idP := saveViaCLI(t, db, writeSaveInput(t, dir, "p.json", cliSaveInput("memory.relations.probe.p", cliRucA, "202402", "a different finding", "smoke-004")))
	stdout, _, code = runCLI(t, "compare", idA, idP, "--db", db)
	if code != 0 {
		t.Fatalf("compare A/P failed (exit %d): %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &cmp); err != nil {
		t.Fatalf("compare A/P output not JSON: %v\n%s", err, stdout)
	}
	if cmp.ScopeMatch != "partial" {
		t.Fatalf("compare(A, P) scopeMatch = %q, want partial", cmp.ScopeMatch)
	}

	// v2: active (informative) memories are directly supersedeable — no review/
	// promote step exists. Supersede C → B (distinct chains) records the pair.
	if _, _, code := runCLI(t, "supersede", idC, "--target", idB, "--db", db); code != 0 {
		t.Fatalf("supersede C→B failed (exit %d)", code)
	}

	stdout, _, code = runCLI(t, "compare", idC, idB, "--db", db)
	if code != 0 {
		t.Fatalf("compare superseded pair failed (exit %d)", code)
	}
	if err := json.Unmarshal([]byte(stdout), &cmp); err != nil {
		t.Fatalf("compare superseded output not JSON: %v\n%s", err, stdout)
	}
	if cmp.RelationVerdict != "supersedes" {
		t.Fatalf("compare(C, B) after supersede = %+v, want verdict supersedes", cmp)
	}
	if cmp.StatusA != core.StatusSuperseded || cmp.StatusB == core.StatusSuperseded {
		t.Fatalf("compare(C, B) statuses = %s/%s, want C superseded, B current", cmp.StatusA, cmp.StatusB)
	}
}

// TestCLIIllegalApproveOfActiveFailsClosed asserts the fail-closed gate
// boundary: approving an ACTIVE (informative, never-gated) memory exits 1,
// writes nothing to stdout and leaves the memory unchanged.
func TestCLIIllegalApproveOfActiveFailsClosed(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	// A valid authenticated session is required to reach the transition check; an
	// ACTIVE (informative, never-gated) memory is still INVALID_TRANSITION.
	token := seedCLIIdentity(t, db)
	env := writeSessionFile(t, t.TempDir(), token)
	id := saveViaCLI(t, db, fixturePath(t, "a.json"))
	h1 := memoryEnvelope(t, db, id)

	stdout, stderr, code := runCLIEnv(t, env, "approve", id, "--expected-envelope", h1, "--reason", "x", "--db", db)
	if code != 1 {
		t.Fatalf("approve active exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("illegal approve must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "INVALID_TRANSITION") {
		t.Fatalf("stderr must name INVALID_TRANSITION: %q", stderr)
	}

	// The memory is still active.
	stdout, _, code = runCLI(t, "compare", id, id, "--db", db)
	if code != 0 {
		t.Fatalf("compare after illegal approve failed (exit %d): %s", code, stderr)
	}
	var cmp compareOutput
	if err := json.Unmarshal([]byte(stdout), &cmp); err != nil {
		t.Fatalf("compare output not JSON: %v\n%s", err, stdout)
	}
	if cmp.StatusA != core.StatusActive {
		t.Fatalf("memory changed after illegal approve: status = %s, want active", cmp.StatusA)
	}
}

// TestCLIUsageErrorsForNewCommands pins exit codes for the lifecycle surface:
// 2 for malformed usage, 1 for runtime errors (missing observations).
func TestCLIUsageErrorsForNewCommands(t *testing.T) {
	t.Run("supersede without target", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "engram.db")
		id := saveViaCLI(t, db, fixturePath(t, "a.json"))
		stdout, _, code := runCLI(t, "supersede", id, "--db", db)
		if code != 2 {
			t.Fatalf("supersede without --target exit = %d, want 2; stdout=%q", code, stdout)
		}
	})

	t.Run("supersede without id", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "engram.db")
		_, _, code := runCLI(t, "supersede", "--target", "x", "--db", db)
		if code != 2 {
			t.Fatalf("supersede without id exit = %d, want 2", code)
		}
	})

	t.Run("compare with one id", func(t *testing.T) {
		_, _, code := runCLI(t, "compare", "some-id", "--db", filepath.Join(t.TempDir(), "engram.db"))
		if code != 2 {
			t.Fatalf("compare with one id exit = %d, want 2", code)
		}
	})

	t.Run("review without id", func(t *testing.T) {
		_, _, code := runCLI(t, "review", "--db", filepath.Join(t.TempDir(), "engram.db"))
		if code != 2 {
			t.Fatalf("review without id exit = %d, want 2", code)
		}
	})

	t.Run("compare with missing observations", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "engram.db")
		_, stderr, code := runCLI(t, "compare", "missing-a", "missing-b", "--db", db)
		if code != 1 {
			t.Fatalf("compare missing ids exit = %d, want 1; stderr=%q", code, stderr)
		}
		if !strings.Contains(stderr, "MEMORY_NOT_FOUND") {
			t.Fatalf("stderr must name OBSERVATION_NOT_FOUND: %q", stderr)
		}
	})

	t.Run("approve with missing observation", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "engram.db")
		// The authenticated session must resolve first; only then does the
		// runtime path reach the store and report MEMORY_NOT_FOUND.
		token := seedCLIIdentity(t, db)
		env := writeSessionFile(t, t.TempDir(), token)
		_, stderr, code := runCLIEnv(t, env, "approve", "missing-id", "--expected-envelope", "x", "--reason", "x", "--db", db)
		if code != 1 {
			t.Fatalf("approve missing id exit = %d, want 1; stderr=%q", code, stderr)
		}
		if !strings.Contains(stderr, "MEMORY_NOT_FOUND") {
			t.Fatalf("stderr must name MEMORY_NOT_FOUND: %q", stderr)
		}
	})

	t.Run("help lists lifecycle commands", func(t *testing.T) {
		stdout, _, code := runCLI(t, "help")
		if code != 0 {
			t.Fatalf("help exit = %d, want 0", code)
		}
		for _, cmd := range []string{"compare", "approve", "reject", "void", "supersede", "period-summary"} {
			if !strings.Contains(stdout, cmd) {
				t.Fatalf("help output missing %q: %s", cmd, stdout)
			}
		}
	})
}

// ──────────────────────────────────────────────
// mcp / serve surfaces
// ──────────────────────────────────────────────

// TestCLIMCPStdioRoundTrip is the agent-transport smoke: the real binary serves
// newline-delimited JSON-RPC over stdin/stdout (initialize → tools/call) and
// answers with protocol responses.
func TestCLIMCPStdioRoundTrip(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	// Seed one observation through the CLI so the MCP doctor call sees it.
	_, stderr, code := runCLI(t, "save", fixturePath(t, "a.json"), "--db", db)
	if code != 0 {
		t.Fatalf("seed save failed (exit %d): %s", code, stderr)
	}

	cmd := exec.Command(binPath, "mcp", "--db", db)
	cmd.Stdin = strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"engram_doctor","arguments":{}}}`,
	}, "\n") + "\n")
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("mcp run: %v; stderr=%s", err, errOut.String())
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("mcp produced %d response lines, want 2: %s", len(lines), out.String())
	}

	var initResponse struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &initResponse); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if initResponse.Error != nil || initResponse.Result.ServerInfo.Name != "drenyra-engram" {
		t.Fatalf("initialize response wrong: %+v", initResponse)
	}

	var doctorResponse struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &doctorResponse); err != nil {
		t.Fatalf("decode doctor response: %v", err)
	}
	if doctorResponse.Error != nil {
		t.Fatalf("doctor error: %+v", doctorResponse.Error)
	}
	if len(doctorResponse.Result.Content) == 0 {
		t.Fatal("doctor tool result has no content")
	}
	var report struct {
		Observations int `json:"observations"`
	}
	if err := json.Unmarshal([]byte(doctorResponse.Result.Content[0].Text), &report); err != nil {
		t.Fatalf("decode doctor report: %v", err)
	}
	if report.Observations != 1 {
		t.Fatalf("doctor observations = %d, want 1 (seeded via CLI)", report.Observations)
	}
}

// TestCLIMCPSurfacesHelp asserts the new commands appear in help and their usage
// guards reject stray arguments (exit 2).
func TestCLIMCPSurfacesHelp(t *testing.T) {
	t.Run("help lists mcp and serve", func(t *testing.T) {
		stdout, _, code := runCLI(t, "help")
		if code != 0 {
			t.Fatalf("help exit = %d, want 0", code)
		}
		for _, cmd := range []string{"mcp", "serve"} {
			if !strings.Contains(stdout, cmd) {
				t.Fatalf("help output missing %q", cmd)
			}
		}
	})

	t.Run("serve --help exits 0", func(t *testing.T) {
		_, _, code := runCLI(t, "serve", "--help")
		if code != 0 {
			t.Fatalf("serve --help exit = %d, want 0", code)
		}
	})

	t.Run("mcp rejects extra arguments", func(t *testing.T) {
		_, _, code := runCLI(t, "mcp", "stray")
		if code != 2 {
			t.Fatalf("mcp stray exit = %d, want 2 (usage error)", code)
		}
	})

	t.Run("non-authorization: no authorize/approve/allow command line", func(t *testing.T) {
		stdout, _, code := runCLI(t, "help")
		if code != 0 {
			t.Fatalf("help exit = %d, want 0", code)
		}
		// The help MAY document the boundary ("there is no authorize command");
		// it must never list an authorization command as invocable.
		for _, line := range strings.Split(stdout, "\n") {
			trimmed := strings.TrimSpace(line)
			for _, forbidden := range []string{"authorize", "allow", "execute", "declare", "file", "pay"} {
				if strings.HasPrefix(trimmed, "drenyra-engram "+forbidden) {
					t.Fatalf("help lists forbidden command line %q", trimmed)
				}
			}
		}
	})
}

// ──────────────────────────────────────────────
// sync surface
// ──────────────────────────────────────────────

// TestCLISyncRoundTrip: save into dbA, sync A→B, and the target store serves
// the observation (context via the CLI) with its original identity. A second
// sync is a no-op.
func TestCLISyncRoundTrip(t *testing.T) {
	dbA := filepath.Join(t.TempDir(), "a.db")
	dbB := filepath.Join(t.TempDir(), "b.db")

	// Seed A through the CLI.
	stdout, stderr, code := runCLI(t, "save", fixturePath(t, "a.json"), "--db", dbA)
	if code != 0 {
		t.Fatalf("save A failed (exit %d): %s", code, stderr)
	}
	var saved struct {
		Observation struct {
			Identity struct {
				ID string `json:"id"`
			} `json:"identity"`
		} `json:"observation"`
	}
	if err := json.Unmarshal([]byte(stdout), &saved); err != nil {
		t.Fatalf("save output not JSON: %v", err)
	}

	// Sync A → B.
	stdout, stderr, code = runCLI(t, "sync", "--from", dbA, "--to", dbB)
	if code != 0 {
		t.Fatalf("sync failed (exit %d): %s", code, stderr)
	}
	var report struct {
		ObservationsImported int                     `json:"observationsImported"`
		ObservationsSkipped  int                     `json:"observationsSkipped"`
		Conflicts            []struct{ Kind string } `json:"conflicts"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("sync output not JSON: %v\n%s", err, stdout)
	}
	if report.ObservationsImported != 1 || len(report.Conflicts) != 0 {
		t.Fatalf("report = %+v, want 1 imported, 0 conflicts", report)
	}

	// B serves the synced observation with its ORIGINAL id.
	stdout, stderr, code = runCLI(t, "context", cliRucA, "--period", "202401", "--db", dbB)
	if code != 0 {
		t.Fatalf("context B failed (exit %d): %s", code, stderr)
	}
	if !strings.Contains(stdout, saved.Observation.Identity.ID) {
		t.Fatalf("context B must serve the original id %s: %s", saved.Observation.Identity.ID, stdout)
	}

	// Second sync: idempotent no-op.
	stdout, stderr, code = runCLI(t, "sync", "--from", dbA, "--to", dbB)
	if code != 0 {
		t.Fatalf("second sync failed (exit %d): %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("second sync output not JSON: %v", err)
	}
	if report.ObservationsImported != 0 || report.ObservationsSkipped != 1 {
		t.Fatalf("second report = %+v, want 0 imported, 1 skipped (idempotent)", report)
	}

	// Missing required flags → usage error.
	_, _, code = runCLI(t, "sync", "--from", dbA)
	if code != 2 {
		t.Fatalf("sync missing --to exit = %d, want 2", code)
	}
}

// ──────────────────────────────────────────────
// Authenticated approval CLI tests (v0.4.0 Step 1, ADR-003)
// ──────────────────────────────────────────────

// sha256HexCLI is the token-hash fixture helper (the CLI hashes the raw token
// the same way before any lookup).
func sha256HexCLI(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// writeGatedFixture writes a fiscalEffect=adjustment fixture (lands
// pending_review behind the v2 human gate) and returns its path.
func writeGatedFixture(t *testing.T) string {
	t.Helper()
	gated := `{"topicKey":"adjust/aj-001","title":"Ajuste AJ-001","kind":"decision","scope":{"kind":"company","organizationId":"cli","companyId":"20100039201","ruc":"20100039201","period":"202401"},"content":{"what":"ajuste de periodo","why":"comprobante tardio","where":"cli","learned":"n/a"},"fiscalEffect":"adjustment","effectiveAt":"2024-01-31T00:00:00.000Z","source":{"system":"cli","actorId":"cli-user","actorKind":"agent"}}`
	path := filepath.Join(t.TempDir(), "gated.json")
	if err := os.WriteFile(path, []byte(gated), 0o600); err != nil {
		t.Fatalf("write gated fixture: %v", err)
	}
	return path
}

// savePendingCLIMemory saves the gated fixture through the built CLI and returns
// the pending_review memory id.
func savePendingCLIMemory(t *testing.T, db string) string {
	t.Helper()
	return saveViaCLI(t, db, writeGatedFixture(t))
}

// seedCLIIdentity seeds one identity + expiring session directly on db (design
// section 8: test helpers call store.SeedIdentity directly; they never depend on
// environment state) and returns the raw token (ONLY its SHA-256 hash is stored).
// The tenant is the CLI's fixed organization id so the principal can approve
// memories saved through the CLI surface.
func seedCLIIdentity(t *testing.T, db string) string {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	membershipID := "membership-cli"
	if err := st.SeedIdentity(store.IdentitySeed{
		TenantID:     cliOrganizationID,
		CompanyID:    cliRucA,
		CompanyRUC:   cliRucA,
		CompanyName:  "CLI Demo SAC",
		MembershipID: membershipID,
		SubjectID:    "maria.torres",
		Roles:        []auth.AccountingRole{auth.RoleController},
	}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	token := "cli-fixture-token"
	if err := st.SeedSession(store.SessionSeed{
		ID:                   "session-cli",
		TokenHash:            sha256HexCLI(token),
		MembershipID:         membershipID,
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return token
}

// sessionFileEnv returns the CLI env that points the session file at
// dir/session.json (the $DRENYRA_ENGRAM_SESSION test override — the tests never
// touch the real user config dir).
func sessionFileEnv(dir string) []string {
	return []string{"DRENYRA_ENGRAM_SESSION=" + filepath.Join(dir, "session.json")}
}

// writeSessionFile writes the raw token to dir/session.json (the CLI test
// override path) and returns the env to pass to the built CLI.
func writeSessionFile(t *testing.T, dir, token string) []string {
	t.Helper()
	path := filepath.Join(dir, "session.json")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"token":%q}`, token)), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	return sessionFileEnv(dir)
}

// memoryEnvelope computes the CURRENT envelope hash of a stored memory — the
// hash a reviewer would have seen at approve time.
func memoryEnvelope(t *testing.T, db, id string) string {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	mem, ok := st.FindByID(id)
	if !ok {
		t.Fatalf("memory %s not found", id)
	}
	return core.ComputeEnvelopeHash(mem)
}

// TestCLIAuthLoginWithoutToken: auth login requires --token (usage error 2).
func TestCLIAuthLoginWithoutToken(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	stdout, stderr, code := runCLIEnv(t, sessionFileEnv(t.TempDir()), "auth", "login", "--db", db)
	if code != 2 {
		t.Fatalf("auth login without --token exit = %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("usage error must not write stdout: %q", stdout)
	}
}

// TestCLIAuthLoginInvalidToken: an unknown token maps to PRINCIPAL_INVALID and
// never creates a session file.
func TestCLIAuthLoginInvalidToken(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	dir := t.TempDir()
	stdout, stderr, code := runCLIEnv(t, sessionFileEnv(dir), "auth", "login", "--token", "not-a-real-token", "--db", db)
	if code != 1 {
		t.Fatalf("auth login invalid token exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "PRINCIPAL_INVALID") {
		t.Fatalf("stderr must carry PRINCIPAL_INVALID: %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("failed login must not write stdout: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "session.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid login must not create a session file (stat err=%v)", err)
	}
}

// TestCLIApproveWithoutSession: approve with no authenticated CLI session fails
// closed with AUTHENTICATION_REQUIRED and points at auth login.
func TestCLIApproveWithoutSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	id := savePendingCLIMemory(t, db)
	h1 := memoryEnvelope(t, db, id)

	stdout, stderr, code := runCLIEnv(t, sessionFileEnv(t.TempDir()),
		"approve", id, "--expected-envelope", h1, "--reason", "reviewed", "--db", db)
	if code != 1 {
		t.Fatalf("approve without session exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("approve without session must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "AUTHENTICATION_REQUIRED") || !strings.Contains(stderr, "auth login") {
		t.Fatalf("stderr must carry AUTHENTICATION_REQUIRED and point at auth login: %q", stderr)
	}
}

// TestCLIApproveWithSeededSession is the full authenticated round trip through
// the real binary: auth login validates the token and writes the user-only (0600)
// session file, then approve resolves the principal from it and prints the
// core.ApprovalResult JSON.
func TestCLIApproveWithSeededSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	dir := t.TempDir()
	env := sessionFileEnv(dir)

	stdout, stderr, code := runCLIEnv(t, env, "auth", "login", "--token", token, "--db", db)
	if code != 0 {
		t.Fatalf("auth login failed (exit %d): %s", code, stderr)
	}
	var login struct {
		Authenticated bool   `json:"authenticated"`
		SubjectID     string `json:"subjectId"`
		SessionFile   string `json:"sessionFile"`
	}
	if err := json.Unmarshal([]byte(stdout), &login); err != nil {
		t.Fatalf("login output not JSON: %v\n%s", err, stdout)
	}
	if !login.Authenticated || login.SubjectID != "maria.torres" {
		t.Fatalf("login output = %+v, want authenticated maria.torres", login)
	}
	sessionPath := filepath.Join(dir, "session.json")
	info, err := os.Stat(sessionPath)
	if err != nil {
		t.Fatalf("session file missing after login: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session file mode = %o, want 0600", info.Mode().Perm())
	}
	sessionData, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	if !strings.Contains(string(sessionData), token) {
		t.Fatalf("session file must hold the RAW token: %s", sessionData)
	}

	id := savePendingCLIMemory(t, db)
	h1 := memoryEnvelope(t, db, id)
	stdout, stderr, code = runCLIEnv(t, env,
		"approve", id, "--expected-envelope", h1, "--reason", "revisado y conforme", "--db", db)
	if code != 0 {
		t.Fatalf("approve failed (exit %d): %s", code, stderr)
	}
	var result core.ApprovalResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("approve output not JSON: %v\n%s", err, stdout)
	}
	if result.MemoryID != id || result.CurrentStatus != "approved" || result.PreviousStatus != "pending_review" {
		t.Fatalf("approve result = %+v, want %s pending_review → approved", result, id)
	}
	if result.ReviewedEnvelopeHash != h1 {
		t.Fatalf("reviewedEnvelopeHash = %q, want %q", result.ReviewedEnvelopeHash, h1)
	}
	if result.IdempotentReplay {
		t.Fatalf("fresh approval must not be an idempotent replay: %+v", result)
	}
}

// TestCLIApproveRejectsActorFlag: caller-supplied authority is gone from the
// approval command — --actor is an unknown flag (usage error 2).
func TestCLIApproveRejectsActorFlag(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	stdout, stderr, code := runCLI(t, "approve", "some-id", "--actor", "maria.torres", "--db", db)
	if code != 2 {
		t.Fatalf("approve --actor exit = %d, want 2 (flag rejected); stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("flag rejection must not write stdout: %q", stdout)
	}
}

// TestCLISeedLocalDevRejectedInProduction: the seed command is explicit,
// isolated and rejected outside DRENYRA_ENV=local_dev — nothing is seeded.
func TestCLISeedLocalDevRejectedInProduction(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	stdout, stderr, code := runCLIEnv(t, []string{"DRENYRA_ENV=production"},
		"auth", "seed-local-dev", "--db", db, "--tenant", cliOrganizationID, "--company", cliRucA,
		"--ruc", cliRucA, "--subject", "maria.torres", "--roles", "controller")
	if code != 1 {
		t.Fatalf("seed-local-dev in production exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("rejected seed must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "DRENYRA_ENV") || !strings.Contains(stderr, "local_dev") {
		t.Fatalf("stderr must explain the local_dev requirement: %q", stderr)
	}
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store after rejected seed: %v", err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.LookupByTokenHash(context.Background(), sha256HexCLI("whatever")); err == nil {
		t.Fatal("production rejection must not seed any session")
	}
}

// TestCLISeedLocalDevPrintsTokenOnce: in DRENYRA_ENV=local_dev the seed prints
// the raw token exactly ONCE on stdout and the store keeps only its hash.
func TestCLISeedLocalDevPrintsTokenOnce(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	dir := t.TempDir()
	env := append([]string{"DRENYRA_ENV=local_dev"}, sessionFileEnv(dir)...)
	stdout, stderr, code := runCLIEnv(t, env,
		"auth", "seed-local-dev", "--db", db, "--tenant", cliOrganizationID, "--company", cliRucA,
		"--ruc", cliRucA, "--subject", "maria.torres", "--roles", "controller,senior_accountant")
	if code != 0 {
		t.Fatalf("seed-local-dev failed (exit %d): %s", code, stderr)
	}
	var seeded struct {
		Token     string `json:"token"`
		SessionID string `json:"sessionId"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal([]byte(stdout), &seeded); err != nil {
		t.Fatalf("seed output not JSON: %v\n%s", err, stdout)
	}
	if len(seeded.Token) < 32 {
		t.Fatalf("seeded token looks weak: %q", seeded.Token)
	}
	if strings.Count(stdout, seeded.Token) != 1 {
		t.Fatalf("raw token must be printed exactly once, got %d occurrences in %s", strings.Count(stdout, seeded.Token), stdout)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	// The store holds ONLY the SHA-256 hash of the token.
	session, err := st.LookupByTokenHash(context.Background(), sha256HexCLI(seeded.Token))
	if err != nil {
		t.Fatalf("seeded session must resolve by its hash: %v", err)
	}
	membership, err := st.LoadMembership(context.Background(), session.MembershipID)
	if err != nil {
		t.Fatalf("load membership: %v", err)
	}
	if membership.SubjectID != "maria.torres" || len(membership.Roles) != 2 {
		t.Fatalf("seeded membership = %+v, want maria.torres with 2 roles", membership)
	}
	if session.AuthenticationMethod != auth.AuthMethodLocalDev || session.AssuranceLevel != auth.AssuranceStandard {
		t.Fatalf("seeded session = %+v, want local_dev + standard assurance", session)
	}
}
