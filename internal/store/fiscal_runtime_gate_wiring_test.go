// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test proves the DRENYRA_FISCAL_RUNTIME_MODE
// gate (design.md "Runtime and downgrade modes") is REALLY wired into the
// production write paths through a PUBLIC entry point (Save), not just
// exercised in isolation against FiscalRuntimeMode.CheckProtectedWrite
// directly (see TestFiscalRuntimeModesAndDowngradeRefusal in
// migration_v18_test.go for that isolated unit proof). A prior audit found
// FiscalRuntimeMode/ParseFiscalRuntimeMode/CheckProtectedWrite fully defined
// but never called from any production write path — checkFiscalRuntimeGate
// and its five call sites (Save, ApproveMemory, SupersedeExplicit,
// addLinksBound, storeObject) close that gap.
package store

import (
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// TestSaveDeniedByDefaultRuntimeMode proves the production DEFAULT (shadow —
// DRENYRA_FISCAL_RUNTIME_MODE unset) fails closed on a plain company-scope
// Save, through the real Save entry point rather than a direct
// CheckProtectedWrite call: an unconfigured production deployment never
// silently permits a protected write.
func TestSaveDeniedByDefaultRuntimeMode(t *testing.T) {
	// Deliberately do NOT set DRENYRA_FISCAL_RUNTIME_MODE — t.Setenv with an
	// empty string proves the SAME thing an entirely absent env var would
	// (ParseFiscalRuntimeMode treats "" as shadow), while still isolating
	// this test from whatever the process environment happens to carry.
	t.Setenv("DRENYRA_FISCAL_RUNTIME_MODE", "")
	path := t.TempDir() + "/engram.db"
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	_, err = s.Save(validInput("tax.igv.rate", "shadow-mode default"))
	if err == nil {
		t.Fatal("a company-scope Save under the unconfigured (shadow) default must fail closed")
	}
	if auth.Code(err) != auth.CodeFiscalWriteGateClosed {
		t.Fatalf("code = %q, want %q (err %v)", auth.Code(err), auth.CodeFiscalWriteGateClosed, err)
	}
	if _, ok := s.FindByTopicKey("tax.igv.rate", testScope(testRucA)); ok {
		t.Fatal("a gate-denied save must not create an observation row")
	}
}

// TestEnforceModeAllowsV1DeniesLegacyThroughSave proves enforce mode's
// documented asymmetry (design.md: "First-slice protected mutations require
// a complete binding. ... Legacy protected mutation fails
// SCOPE_BINDING_REQUIRED.") through the real Save entry point: a v1
// (FiscalIntent-carrying) Save succeeds, and a legacy (nil-intent) Save on
// the SAME store — same process, same resolved mode — is denied.
func TestEnforceModeAllowsV1DeniesLegacyThroughSave(t *testing.T) {
	s := newTestStoreMode(t, "enforce")
	scope := testScope(fiscalRucA)

	bound := validInput("topic/fiscal/gate-wiring-v1", "v1 write under enforce")
	bound.Scope = scope
	bound.FiscalIntent = fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	if _, err := s.Save(bound); err != nil {
		t.Fatalf("v1 save under enforce must succeed: %v", err)
	}

	legacy := validInput("topic/fiscal/gate-wiring-legacy", "legacy write under enforce")
	legacy.Scope = scope
	_, err := s.Save(legacy)
	if err == nil {
		t.Fatal("a legacy (nil-intent) save under enforce must fail closed")
	}
	if !strings.Contains(err.Error(), core.ScopeBindingRequired) {
		t.Fatalf("error = %v, want %s", err, core.ScopeBindingRequired)
	}
	if _, ok := s.FindByTopicKey("topic/fiscal/gate-wiring-legacy", scope); ok {
		t.Fatal("a gate-denied legacy save must not create an observation row")
	}
}
