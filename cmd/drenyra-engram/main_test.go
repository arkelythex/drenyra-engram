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
