// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test proves the openStoreWithRoot
// stderr diagnostic added in response to a review CRITICAL finding
// (review-70931ebc5d9a7d85, R4-1/R4-2): the unconfigured shadow default fails
// every company-scoped protected write closed by design (design.md "Runtime
// and downgrade modes") — that behavior is INTENTIONAL and unchanged by this
// diagnostic. What was missing was OBSERVABILITY: nothing told an operator,
// before a write failed, that this was about to happen. The fix is a stderr
// line at store-open time, never stdout, so it can never corrupt a command's
// machine-readable JSON output.
package main

import (
	"strings"
	"testing"
)

// TestOpenStoreWarnsOnUnconfiguredShadowMode: with DRENYRA_FISCAL_RUNTIME_MODE
// explicitly unset (overriding TestMain's process-wide legacy_compat), a
// store-opening command emits the shadow-mode diagnostic on stderr — and only
// stderr, never mixed into stdout's JSON.
func TestOpenStoreWarnsOnUnconfiguredShadowMode(t *testing.T) {
	db := repoTempDBPath(t)
	stdout, stderr, code := runCLIEnv(t, []string{"DRENYRA_FISCAL_RUNTIME_MODE="}, "doctor", "--db", db)
	if code != 0 {
		t.Fatalf("doctor failed (exit %d): %s", code, stderr)
	}
	if !strings.Contains(stderr, "DRENYRA_FISCAL_RUNTIME_MODE is unset") {
		t.Fatalf("stderr must carry the shadow-mode diagnostic, got: %q", stderr)
	}
	if !strings.Contains(stderr, "FISCAL_WRITE_GATE_CLOSED") {
		t.Fatalf("stderr diagnostic must name the code a blocked write will see, got: %q", stderr)
	}
	if strings.Contains(stdout, "DRENYRA_FISCAL_RUNTIME_MODE") {
		t.Fatalf("the diagnostic must never leak into stdout (machine-readable output), got: %q", stdout)
	}
}

// TestOpenStoreSilentWhenModeExplicitlyConfigured: an operator who HAS made an
// explicit choice (legacy_compat, enforce, or read_only) sees no diagnostic —
// it exists only to surface the SURPRISING unconfigured default, not to nag
// on every invocation.
func TestOpenStoreSilentWhenModeExplicitlyConfigured(t *testing.T) {
	db := repoTempDBPath(t)
	for _, mode := range []string{"legacy_compat", "enforce", "read_only"} {
		t.Run(mode, func(t *testing.T) {
			_, stderr, code := runCLIEnv(t, []string{"DRENYRA_FISCAL_RUNTIME_MODE=" + mode}, "doctor", "--db", db)
			if code != 0 {
				t.Fatalf("doctor failed (exit %d): %s", code, stderr)
			}
			if strings.Contains(stderr, "DRENYRA_FISCAL_RUNTIME_MODE is unset") {
				t.Fatalf("an explicitly configured mode (%s) must not print the unconfigured-default diagnostic, got: %q", mode, stderr)
			}
		})
	}
}

// repoTempDBPath returns a fresh per-test SQLite path under t.TempDir().
func repoTempDBPath(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/engram.db"
}
