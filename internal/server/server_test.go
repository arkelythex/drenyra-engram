// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test suite drives the shared API, HTTP
// and MCP surfaces with structured-text observation fixtures; there are no
// monetary fields, so no money value is asserted here.

package server

import (
	"path/filepath"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

const (
	testOrgID  = "org-acme"
	testPeriod = "202401"
)

var (
	testRucA = "20100039201"
	testRucB = "20600995804"
)

// newTestAPI opens a temp SQLite store and wraps it in the shared API.
func newTestAPI(t *testing.T) *API {
	t.Helper()
	// The overwhelming majority of newTestAPI callers are plain legacy (no
	// FiscalIntent/fiscalScope) requests exercising unrelated surfaces
	// (review, purge, reconciliation, judgment, OIDC, etc.); default this
	// shared helper to legacy_compat so DRENYRA_FISCAL_RUNTIME_MODE (design.md
	// "Runtime and downgrade modes") doesn't fail-close them. A test whose
	// calls are homogeneously v1 opens its own store via
	// newTestAPIMode(t, "enforce") instead.
	t.Setenv("DRENYRA_FISCAL_RUNTIME_MODE", "legacy_compat")
	path := filepath.Join(t.TempDir(), "engram.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, "test")
}

// newTestAPIMode is newTestAPI with an explicit DRENYRA_FISCAL_RUNTIME_MODE,
// for a test whose requests are homogeneously legacy-class or v1-class
// (unlike newTestAPI's blanket legacy_compat default) — most commonly
// "enforce" for a test that only ever supplies a fiscalScope/FiscalIntent.
func newTestAPIMode(t *testing.T, mode string) *API {
	t.Helper()
	t.Setenv("DRENYRA_FISCAL_RUNTIME_MODE", mode)
	path := filepath.Join(t.TempDir(), "engram.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open test store (mode=%s): %v", mode, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, "test")
}

// newTestAPIPathMode is newTestAPIMode but also returns the store's path and
// the raw *store.SQLiteStore, for a test that will later
// reopenTestAPIMode it under a different mode.
func newTestAPIPathMode(t *testing.T, mode string) (*API, string, *store.SQLiteStore) {
	t.Helper()
	t.Setenv("DRENYRA_FISCAL_RUNTIME_MODE", mode)
	path := filepath.Join(t.TempDir(), "engram.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open test store (mode=%s): %v", mode, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, "test"), path, st
}

// reopenTestAPIMode closes st and reopens the SAME underlying SQLite file
// under a DIFFERENT DRENYRA_FISCAL_RUNTIME_MODE, returning a fresh API
// wrapping the reopened store. See internal/store's reopenTestStoreMode doc
// comment: FiscalRuntimeMode is resolved once per Open and frozen for that
// handle's lifetime, and no single mode permits both a legacy-class and a
// v1-class protected write, so a test whose single logical dataset needs
// both closes and reopens the identical file under the mode each phase
// needs.
func reopenTestAPIMode(t *testing.T, st *store.SQLiteStore, path, mode string) (*API, *store.SQLiteStore) {
	t.Helper()
	if err := st.Close(); err != nil {
		t.Fatalf("close test store before reopen: %v", err)
	}
	t.Setenv("DRENYRA_FISCAL_RUNTIME_MODE", mode)
	ns, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen test store (mode=%s): %v", mode, err)
	}
	t.Cleanup(func() { _ = ns.Close() })
	return New(ns, "test"), ns
}

// testScope builds a company scope for a RUC within the test organization.
func testScope(ruc string) core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: testOrgID,
		CompanyID:      "acme",
		RUC:            ruc,
		Period:         testPeriod,
	}
}

// testAgentSource is the fixture actor for agent-originated saves.
var testAgentSource = core.Source{
	System:    "go-test",
	ActorID:   "test-agent",
	ActorKind: core.ActorKindAgent,
}

// humanSource builds a human actor source for approval-gate tests.
func humanSource(actor string) core.Source {
	return core.Source{System: "go-test", ActorID: actor, ActorKind: core.ActorKindHuman}
}

// validInput builds a valid v2 SaveInput under a topic key + scope. The default
// is an INFORMATIVE memory (fiscalEffect none → active) so most fixtures do not
// trip the human-approval gate; gate tests set FiscalEffect explicitly.
func validInput(topicKey, title, what string, scope core.Scope) core.SaveInput {
	return core.SaveInput{
		TopicKey:     topicKey,
		Title:        title,
		Kind:         core.KindDecision,
		Scope:        scope,
		Content:      core.Content{What: what, Why: "test fixture", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: core.FiscalEffectNone,
		Source:       testAgentSource,
		Confidence:   0.8,
	}
}

// saveOne saves a fixture and returns the stored memory.
func saveOne(t *testing.T, api *API, input core.SaveInput) core.AccountingMemory {
	t.Helper()
	result, err := api.Save(input)
	if err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	return result.Memory
}
