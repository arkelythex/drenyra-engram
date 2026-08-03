// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the CLI against fixtures
// whose content is structured text; there are no monetary fields, so no money
// value is asserted here.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	cmd := exec.Command(binPath, args...)
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
		Observation struct {
			Scope struct {
				RUC string `json:"ruc"`
			} `json:"scope"`
		} `json:"observation"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(stdout), &saveResult); err != nil {
		t.Fatalf("save output not JSON: %v\n%s", err, stdout)
	}
	if saveResult.Outcome != "created" {
		t.Fatalf("outcome = %s, want created", saveResult.Outcome)
	}
	if saveResult.Observation.Scope.RUC != cliRucA {
		t.Fatalf("saved scope ruc = %s, want %s", saveResult.Observation.Scope.RUC, cliRucA)
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
	if fromA[0].Observation.Scope.RUC != cliRucA {
		t.Fatalf("search from A returned ruc %s, want %s", fromA[0].Observation.Scope.RUC, cliRucA)
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
		if len(fromA) != 1 || fromA[0].Observation.Scope.RUC != cliRucA {
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
		if len(fromB) != 1 || fromB[0].Observation.Scope.RUC != cliRucB {
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
	if report.SchemaVersion != 1 || report.Observations != 2 || report.RevisionChains != 2 {
		t.Fatalf("doctor report = %+v, want schemaVersion 1, 2 observations, 2 chains", report)
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
		Observation struct {
			Identity struct {
				ID string `json:"id"`
			} `json:"identity"`
		} `json:"observation"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("save output not JSON: %v\n%s", err, stdout)
	}
	if result.Outcome != "created" && result.Outcome != "updated" {
		t.Fatalf("save outcome = %s, want created/updated", result.Outcome)
	}
	if result.Observation.Identity.ID == "" {
		t.Fatal("save returned an empty id")
	}
	return result.Observation.Identity.ID
}

// cliSaveInput builds a save payload for the CLI's fixed company scope shape.
func cliSaveInput(topicKey, ruc, period, what, session string) core.SaveInput {
	return core.SaveInput{
		TopicKey: topicKey,
		Title:    "base rate",
		Type:     "policy",
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
		AuthorityStatus: core.StatusDraft,
		Provenance: core.Provenance{
			Actor:     "cli-user",
			Timestamp: "2026-01-15T12:00:00.000Z",
			Source:    "cli",
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

// TestCLILifecycleRoundTrip drives the full lifecycle through the built binary:
// save → review → promote → supersede (with target), asserting each command's
// JSON shape and exit code against a fresh temp database.
func TestCLILifecycleRoundTrip(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")

	idA := saveViaCLI(t, db, fixturePath(t, "a.json"))

	stdout, _, code := runTransition(t, db, "review", idA, "--actor", "test-actor")
	var reviewed transitionOutput
	if err := json.Unmarshal([]byte(stdout), &reviewed); err != nil {
		t.Fatalf("review output not JSON: %v\n%s", err, stdout)
	}
	if reviewed.ID != idA || reviewed.From != core.StatusDraft || reviewed.To != core.StatusReviewed || reviewed.Revision != 1 {
		t.Fatalf("review output = %+v, want id %s, draft → reviewed, revision 1", reviewed, idA)
	}

	stdout, _, code = runTransition(t, db, "promote", idA, "--actor", "test-actor")
	var promoted transitionOutput
	if err := json.Unmarshal([]byte(stdout), &promoted); err != nil {
		t.Fatalf("promote output not JSON: %v\n%s", err, stdout)
	}
	if promoted.ID != idA || promoted.From != core.StatusReviewed || promoted.To != core.StatusPromoted {
		t.Fatalf("promote output = %+v, want id %s, reviewed → promoted", promoted, idA)
	}

	idB := saveViaCLI(t, db, fixturePath(t, "b.json"))
	stdout, _, code = runTransition(t, db, "supersede", idA, "--target", idB, "--actor", "test-actor")
	var superseded supersedeOutput
	if err := json.Unmarshal([]byte(stdout), &superseded); err != nil {
		t.Fatalf("supersede output not JSON: %v\n%s", err, stdout)
	}
	if superseded.ID != idA || superseded.From != core.StatusPromoted || superseded.To != core.StatusSuperseded || superseded.TargetID != idB {
		t.Fatalf("supersede output = %+v, want id %s, promoted → superseded, target %s", superseded, idA, idB)
	}

	// The supersedes relation is recorded; with idB still draft the verdict
	// falls back to the shared topicKey → related, not supersedes.
	stdout, stderr, code := runCLI(t, "compare", idA, idB, "--db", db)
	if code != 0 {
		t.Fatalf("compare failed (exit %d): %s", code, stderr)
	}
	var cmp compareOutput
	if err := json.Unmarshal([]byte(stdout), &cmp); err != nil {
		t.Fatalf("compare output not JSON: %v\n%s", err, stdout)
	}
	// idA was superseded by idB in this round trip → the verdict is
	// "supersedes" (source check: A is superseded, B is the successor).
	if cmp.RelationVerdict != "supersedes" || !cmp.IdentityMatch || cmp.ScopeMatch != "none" {
		t.Fatalf("compare(idA, idB) = %+v, want supersedes + identityMatch + scopeMatch none", cmp)
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
	if !cmp.IdentityMatch || cmp.RelationVerdict != "related" || cmp.ScopeMatch != "exact" {
		t.Fatalf("compare(A, A2) = %+v, want related + identityMatch + scopeMatch exact", cmp)
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

	// Promote all three, then supersede C → A2 and A → C: the relations table
	// records A→C as supersedes with C superseded → verdict supersedes.
	for _, id := range []string{idA, idC, idA2} {
		if _, _, code := runCLI(t, "review", id, "--actor", "test-actor", "--db", db); code != 0 {
			t.Fatalf("review %s failed (exit %d)", id, code)
		}
		if _, _, code := runCLI(t, "promote", id, "--actor", "test-actor", "--db", db); code != 0 {
			t.Fatalf("promote %s failed (exit %d)", id, code)
		}
	}
	if _, _, code := runCLI(t, "supersede", idC, "--target", idA2, "--db", db); code != 0 {
		t.Fatalf("supersede C→A2 failed (exit %d)", code)
	}
	if _, _, code := runCLI(t, "supersede", idA, "--target", idC, "--db", db); code != 0 {
		t.Fatalf("supersede A→C failed (exit %d)", code)
	}

	stdout, _, code = runCLI(t, "compare", idA, idC, "--db", db)
	if code != 0 {
		t.Fatalf("compare superseded pair failed (exit %d)", code)
	}
	if err := json.Unmarshal([]byte(stdout), &cmp); err != nil {
		t.Fatalf("compare superseded output not JSON: %v\n%s", err, stdout)
	}
	if cmp.RelationVerdict != "supersedes" {
		t.Fatalf("compare(A, C) after supersede = %+v, want verdict supersedes", cmp)
	}
	if cmp.StatusA != core.StatusSuperseded || cmp.StatusB != core.StatusSuperseded {
		t.Fatalf("compare(A, C) statuses = %s/%s, want superseded/superseded", cmp.StatusA, cmp.StatusB)
	}
}

// TestCLIIllegalPromoteFromDraftFailsClosed asserts the fail-closed lifecycle
// boundary: a non-adjacent transition exits 1, writes nothing to stdout and
// leaves the observation unchanged.
func TestCLIIllegalPromoteFromDraftFailsClosed(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	id := saveViaCLI(t, db, fixturePath(t, "a.json"))

	stdout, stderr, code := runCLI(t, "promote", id, "--db", db)
	if code != 1 {
		t.Fatalf("promote draft exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("illegal promote must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, core.ErrInvalidTransition) {
		t.Fatalf("stderr must name INVALID_TRANSITION: %q", stderr)
	}

	// The observation is still draft.
	stdout, _, code = runCLI(t, "compare", id, id, "--db", db)
	if code != 0 {
		t.Fatalf("compare after illegal promote failed (exit %d): %s", code, stderr)
	}
	var cmp compareOutput
	if err := json.Unmarshal([]byte(stdout), &cmp); err != nil {
		t.Fatalf("compare output not JSON: %v\n%s", err, stdout)
	}
	if cmp.StatusA != core.StatusDraft {
		t.Fatalf("observation changed after illegal promote: status = %s, want draft", cmp.StatusA)
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
		if !strings.Contains(stderr, "OBSERVATION_NOT_FOUND") {
			t.Fatalf("stderr must name OBSERVATION_NOT_FOUND: %q", stderr)
		}
	})

	t.Run("review with missing observation", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "engram.db")
		_, stderr, code := runCLI(t, "review", "missing-id", "--db", db)
		if code != 1 {
			t.Fatalf("review missing id exit = %d, want 1; stderr=%q", code, stderr)
		}
		if !strings.Contains(stderr, "OBSERVATION_NOT_FOUND") {
			t.Fatalf("stderr must name OBSERVATION_NOT_FOUND: %q", stderr)
		}
	})

	t.Run("help lists lifecycle commands", func(t *testing.T) {
		stdout, _, code := runCLI(t, "help")
		if code != 0 {
			t.Fatalf("help exit = %d, want 0", code)
		}
		for _, cmd := range []string{"compare", "review", "promote", "supersede"} {
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
			for _, forbidden := range []string{"authorize", "approve", "allow"} {
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
