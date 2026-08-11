// Makefile / CI contract test for the bounded fuzz gate (spec FR-3, AC-2;
// design D-10 WU-4): `make fuzz-ci` must run exactly the three frozen fuzz
// targets with -fuzztime=30s each, propagate non-zero exit, and no unbounded
// fuzz command may exist anywhere in the Makefile.
package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMakefileFuzzCIContract pins the `make fuzz-ci` contract (task 1.6,
// FR-3): exactly three -fuzztime=30s invocations, one per frozen target, with
// no -fuzz invocation missing its 30s budget (unbounded fuzzing must never be
// a CI gate).
func TestMakefileFuzzCIContract(t *testing.T) {
	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("root Makefile unreadable from internal/core: %v", err)
	}
	content := string(makefile)

	want := []string{
		"go test ./internal/core -run '^$$' -fuzz='^FuzzParseComprobanteXML$$' -fuzztime=30s",
		"go test ./internal/core -run '^$$' -fuzz='^FuzzCanonicalReceiptPayload$$' -fuzztime=30s",
		"go test ./internal/search -run '^$$' -fuzz='^FuzzSearchTokenize$$' -fuzztime=30s",
	}
	for _, w := range want {
		if !strings.Contains(content, w) {
			t.Fatalf("Makefile fuzz-ci is missing the frozen invocation:\n  %s", w)
		}
	}

	// Exactly three -fuzz and three -fuzztime=30s occurrences, and every fuzz
	// invocation is budgeted (never unbounded).
	if n := strings.Count(content, "-fuzz="); n != 3 {
		t.Fatalf("Makefile has %d -fuzz invocations, want exactly 3", n)
	}
	if n := strings.Count(content, "-fuzztime=30s"); n != 3 {
		t.Fatalf("Makefile has %d -fuzztime=30s budgets, want exactly 3", n)
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "-fuzz=") && !strings.Contains(trimmed, "-fuzztime=30s") {
			t.Fatalf("unbounded fuzz invocation in Makefile: %q", trimmed)
		}
	}

	// The three frozen target names each anchor exactly one -fuzz invocation.
	for _, target := range []string{"FuzzParseComprobanteXML", "FuzzCanonicalReceiptPayload", "FuzzSearchTokenize"} {
		anchor := "-fuzz='^" + target + "$$'"
		if strings.Count(content, anchor) != 1 {
			t.Fatalf("target %s must anchor exactly one -fuzz invocation, got %d", target, strings.Count(content, anchor))
		}
	}

	// CI wiring must exist: a workflow invoking `make fuzz-ci`.
	workflow := filepath.Join("..", "..", ".github", "workflows", "fuzz.yml")
	ci, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatalf("fuzz CI workflow unreadable: %v", err)
	}
	if !strings.Contains(string(ci), "make fuzz-ci") {
		t.Fatalf("fuzz CI workflow does not invoke `make fuzz-ci`")
	}
}
