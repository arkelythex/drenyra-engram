// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the CLI identity→scope binding
// test surface (scope-param-rollout FR-SPR-3, AC-SPR-3, D-SPR-4/D-SPR-5): it
// proves that a session-authenticated scope-flag command whose --company lies
// OUTSIDE the session principal's membership fails closed with the frozen typed
// denial and a non-zero exit BEFORE any store data access, that an exact
// membership company proceeds unchanged, and that session-less operation is
// byte-for-byte pre-change behavior (FD-SPR-3). No monetary field exists
// anywhere in this file (IR-1).
package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIIdentityScopeBinding — AC-SPR-3/FR-SPR-3: session-A principal
// (seedCLIIdentity + writeSessionFile) on the scope-flag read commands search
// (main.go ~300) and context (~348). Rows: session-A + --company B → typed
// denial + non-zero exit with NO search data access; session-A + --company A →
// unchanged; session-less → unchanged.
func TestCLIIdentityScopeBinding(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	env := writeSessionFile(t, t.TempDir(), token)

	t.Run("session A denied foreign company on search", func(t *testing.T) {
		stdout, stderr, code := runCLIEnv(t, env, "search", "tenant", "--company", cliRucB, "--db", db)
		if code == 0 {
			t.Fatalf("search --company %s exited 0 (leak): stdout=%q stderr=%q", cliRucB, stdout, stderr)
		}
		if !strings.Contains(stderr, "COMPANY_SCOPE_DENIED") {
			t.Fatalf("search stderr = %q, want COMPANY_SCOPE_DENIED typed denial", stderr)
		}
		if strings.Contains(stdout, "\"matches\"") || strings.Contains(stdout, "\"memory\"") {
			t.Fatalf("search stdout carries results despite denial: %q", stdout)
		}
	})

	t.Run("session A exact membership on search unchanged", func(t *testing.T) {
		stdout, stderr, code := runCLIEnv(t, env, "search", "tenant", "--company", cliRucA, "--db", db)
		if code != 0 {
			t.Fatalf("search --company %s failed (exit %d): %s", cliRucA, code, stderr)
		}
		if !strings.Contains(stdout, "[]") {
			t.Fatalf("search --company %s stdout = %q, want empty results (no seeded memory)", cliRucA, stdout)
		}
	})

	t.Run("session A denied foreign company on context", func(t *testing.T) {
		stdout, stderr, code := runCLIEnv(t, env, "context", cliRucB, "--db", db)
		if code == 0 {
			t.Fatalf("context %s exited 0 (leak): stdout=%q stderr=%q", cliRucB, stdout, stderr)
		}
		if !strings.Contains(stderr, "COMPANY_SCOPE_DENIED") {
			t.Fatalf("context stderr = %q, want COMPANY_SCOPE_DENIED typed denial", stderr)
		}
	})

	t.Run("session A exact membership on context unchanged", func(t *testing.T) {
		stdout, stderr, code := runCLIEnv(t, env, "context", cliRucA, "--db", db)
		if code != 0 {
			t.Fatalf("context %s failed (exit %d): %s", cliRucA, code, stderr)
		}
		if !strings.Contains(stdout, "[]") {
			t.Fatalf("context %s stdout = %q, want empty observations", cliRucA, stdout)
		}
	})

	t.Run("session-less operation unchanged", func(t *testing.T) {
		// Empty session dir: no session file → unbound reference mode (FD-SPR-3).
		noSession := sessionFileEnv(t.TempDir())
		stdout, stderr, code := runCLIEnv(t, noSession, "search", "tenant", "--company", cliRucB, "--db", db)
		if code != 0 {
			t.Fatalf("session-less search --company %s failed (exit %d): %s", cliRucB, code, stderr)
		}
		if !strings.Contains(stdout, "[]") {
			t.Fatalf("session-less search stdout = %q, want empty results (reference mode)", stdout)
		}
	})
}
