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
	path := filepath.Join(t.TempDir(), "engram.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, "test")
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
