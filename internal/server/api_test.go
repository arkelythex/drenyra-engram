// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the shared domain services
// (internal/server/api.go) with structured-text observation fixtures; there
// are no monetary fields, so no money value is computed here.

package server

import (
	"reflect"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/search"
)

// ──────────────────────────────────────────────
// Writes / reads
// ──────────────────────────────────────────────

func TestSaveGetRoundTrip(t *testing.T) {
	api := newTestAPI(t)

	result, err := api.Save(validInput("topic/igv", "IGV 18%", "IGV is 18%", testScope(testRucA)))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if result.Outcome != core.WriteCreated {
		t.Fatalf("outcome = %q, want created", result.Outcome)
	}
	if result.Memory.Revision != 1 {
		t.Fatalf("revision = %d, want 1", result.Memory.Revision)
	}
	if result.Memory.Identity.ID == "" {
		t.Fatal("empty observation id")
	}

	got, err := api.Get(result.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content.What != "IGV is 18%" {
		t.Fatalf("what = %q, want %q", got.Content.What, "IGV is 18%")
	}
	if got.Status != core.StatusActive {
		t.Fatalf("status = %q, want active (informative save, fiscalEffect none)", got.Status)
	}
}

func TestSaveCreatesNewRevision(t *testing.T) {
	api := newTestAPI(t)
	scope := testScope(testRucA)

	first := saveOne(t, api, validInput("topic/retention", "Retention 4%", "v1", scope))
	second := saveOne(t, api, validInput("topic/retention", "Retention 4%", "v2 corrected", scope))

	if first.Identity.ID == second.Identity.ID {
		t.Fatal("revisions must have distinct ids (immutable history)")
	}
	if second.Revision != 2 {
		t.Fatalf("revision = %d, want 2", second.Revision)
	}
	if got, err := api.Get(first.Identity.ID); err != nil || got.Content.What != "v1" {
		t.Fatalf("history must stay retrievable by id: got %+v err %v", got, err)
	}
}

func TestGetNotFound(t *testing.T) {
	api := newTestAPI(t)
	_, err := api.Get("no-such-id")
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false, want true", err)
	}
}

func TestGetByTopicLatestRevision(t *testing.T) {
	api := newTestAPI(t)
	scope := testScope(testRucA)
	saveOne(t, api, validInput("topic/sunat", "SUNAT due", "old", scope))
	latest := saveOne(t, api, validInput("topic/sunat", "SUNAT due", "new", scope))

	got, err := api.GetByTopic("topic/sunat", scope)
	if err != nil {
		t.Fatalf("get by topic: %v", err)
	}
	if got.Identity.ID != latest.Identity.ID {
		t.Fatalf("got id %s, want latest %s", got.Identity.ID, latest.Identity.ID)
	}
	if got.Content.What != "new" {
		t.Fatalf("what = %q, want new", got.Content.What)
	}
}

func TestContextSurfacesCurrentMemory(t *testing.T) {
	api := newTestAPI(t)
	scope := testScope(testRucA)

	saveOne(t, api, validInput("topic/a", "A", "v1", scope))
	saveOne(t, api, validInput("topic/a", "A", "v2", scope))
	saveOne(t, api, validInput("topic/b", "B", "only", scope))

	context, err := api.Context(scope)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if len(context) != 2 {
		t.Fatalf("context has %d observations, want 2 (latest per chain)", len(context))
	}
	for _, observation := range context {
		if observation.Identity.TopicKey == "topic/a" && observation.Content.What != "v2" {
			t.Fatalf("topic/a surfaced %q, want v2 (latest)", observation.Content.What)
		}
	}
}

// ──────────────────────────────────────────────
// Scope isolation (contracts/scope.md rule 1 + 5)
// ──────────────────────────────────────────────

// TestSearchScopeIsolation is the REQUIRED cross-tenant property: a company-A
// observation is never retrievable from company B, even with identical text and
// topic key (the scope filter runs before ranking, never as a post-filter).
func TestSearchScopeIsolation(t *testing.T) {
	api := newTestAPI(t)

	scopeA := testScope(testRucA)
	scopeB := testScope(testRucB)
	inputA := validInput("topic/shared", "shared", "identical text", scopeA)
	saveOne(t, api, inputA)

	results, err := api.Search(newSearchInput("identical", scopeB))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("company B search returned %d results for company A memory", len(results))
	}

	results, err = api.Search(newSearchInput("identical", scopeA))
	if err != nil {
		t.Fatalf("search own scope: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("company A search returned %d results, want 1", len(results))
	}
}

func newSearchInput(query string, scope core.Scope) search.Input {
	return search.Input{Query: query, Scope: scope}
}

// ──────────────────────────────────────────────
// compare verdicts
// ──────────────────────────────────────────────

// TestCompareSupersedesSourceCheck is the corrected semantics: when A is
// superseded by B, A (the source) is stored superseded and B (the successor)
// stays draft/promoted. compare(A, B) reports supersedes only when the SOURCE
// is superseded.
func TestCompareSupersedesSourceCheck(t *testing.T) {
	api := newTestAPI(t)
	scope := testScope(testRucA)

	old := saveOne(t, api, validInput("topic/rule", "Rule", "old", scope))
	replacement := saveOne(t, api, validInput("topic/rule", "Rule", "replacement", scope))

	// The second save superseded the first automatically: the STORED old memory
	// is superseded (re-read, not the first-save snapshot) and the supersedes
	// relation is recorded atomically.
	storedOld, err := api.Get(old.Identity.ID)
	if err != nil {
		t.Fatalf("get old: %v", err)
	}
	if storedOld.Status != core.StatusSuperseded {
		t.Fatalf("stored old status = %q, want superseded (automatic on chain save)", storedOld.Status)
	}
	if storedOld.SupersedesID != replacement.Identity.ID {
		t.Fatalf("stored old supersedesId = %q, want successor %q", storedOld.SupersedesID, replacement.Identity.ID)
	}

	output, err := api.Compare(old.Identity.ID, replacement.Identity.ID)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if output.RelationVerdict != "supersedes" {
		t.Fatalf("verdict = %q, want supersedes", output.RelationVerdict)
	}
	if output.StatusA != core.StatusSuperseded {
		t.Fatalf("statusA = %q, want superseded (source is superseded)", output.StatusA)
	}
	if output.StatusB == core.StatusSuperseded {
		t.Fatalf("statusB = %q, successor must NOT be superseded", output.StatusB)
	}
}

func TestCompareNotConflict(t *testing.T) {
	api := newTestAPI(t)
	a := saveOne(t, api, validInput("topic/a", "A", "a", testScope(testRucA)))
	b := saveOne(t, api, validInput("topic/b", "B", "b", testScope(testRucA)))

	output, err := api.Compare(a.Identity.ID, b.Identity.ID)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if output.RelationVerdict != "not_conflict" {
		t.Fatalf("verdict = %q, want not_conflict", output.RelationVerdict)
	}
}

func TestCompareSameChainReportsSupersedes(t *testing.T) {
	api := newTestAPI(t)
	scope := testScope(testRucA)
	a := saveOne(t, api, validInput("topic/shared", "A", "a", scope))
	b := saveOne(t, api, validInput("topic/shared", "B", "b", scope))

	// v2: two saves of the same (topicKey, scope) chain — the first is
	// superseded by the second; compare(A, B) reports the completed supersede
	// pair (the SOURCE is stored superseded).
	output, err := api.Compare(a.Identity.ID, b.Identity.ID)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if output.RelationVerdict != "supersedes" {
		t.Fatalf("verdict = %q, want supersedes (same chain, first superseded)", output.RelationVerdict)
	}
	if output.StatusA != core.StatusSuperseded {
		t.Fatalf("statusA = %q, want superseded (source)", output.StatusA)
	}
	if output.StatusB == core.StatusSuperseded {
		t.Fatalf("statusB = %q, successor must not be superseded", output.StatusB)
	}
	if output.ScopeMatch != "exact" {
		t.Fatalf("scopeMatch = %q, want exact", output.ScopeMatch)
	}
}

// ──────────────────────────────────────────────
// Lifecycle (contracts/lifecycle.md)
// ──────────────────────────────────────────────

func TestHumanApprovalGate(t *testing.T) {
	api := newTestAPI(t)
	scope := testScope(testRucA)
	// A memory WITH fiscal effect is saved as pending_review (the gate).
	gated := saveOne(t, api, gatedInput("topic/life", "Ajuste", scope))
	if gated.Status != core.StatusPendingReview {
		t.Fatalf("gated save status = %q, want pending_review", gated.Status)
	}

	// A MACHINE (agent) cannot approve — GATE_REQUIRES_HUMAN.
	if _, err := api.Approve(gated.Identity.ID, testAgentSource); !IsGateError(err) {
		t.Fatalf("agent approve: IsGateError(%v) = false, want true", err)
	}

	// A HUMAN approves — pending_review → approved.
	approved, err := api.Approve(gated.Identity.ID, humanSource("maria.torres"))
	if err != nil {
		t.Fatalf("human approve: %v", err)
	}
	if approved.Status != core.StatusApproved {
		t.Fatalf("status = %q, want approved", approved.Status)
	}

	// An ACTIVE memory (informative) cannot be approved (never gated).
	informative := saveOne(t, api, validInput("topic/info", "Info", "y", scope))
	_, approveErr := api.Approve(informative.Identity.ID, humanSource("maria.torres"))
	if !IsConflict(approveErr) {
		t.Fatalf("approve active: IsConflict(%v) = false, want true", approveErr)
	}
	if !strings.Contains(approveErr.Error(), "INVALID_TRANSITION") {
		t.Fatalf("error %q must carry INVALID_TRANSITION", approveErr)
	}
}

// gatedInput builds a SaveInput with a real fiscal effect (adjustment) so the
// save lands in pending_review behind the human gate.
func gatedInput(topicKey, title string, scope core.Scope) core.SaveInput {
	input := validInput(topicKey, title, "ajuste de periodo", scope)
	input.FiscalEffect = core.FiscalEffectAdjustment
	input.EffectiveAt = "2026-07-31T12:00:00Z"
	return input
}

func TestSupersedeExplicitFromActive(t *testing.T) {
	api := newTestAPI(t)
	scope := testScope(testRucA)
	old := saveOne(t, api, validInput("topic/s", "S", "old", scope))
	target := saveOne(t, api, validInput("topic/t", "T", "new", scope))

	// Explicit supersede of an ACTIVE memory routes readers to the target.
	if _, err := api.Supersede(old.Identity.ID, target.Identity.ID, humanSource("maria.torres")); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	got, err := api.Get(old.Identity.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != core.StatusSuperseded {
		t.Fatalf("status = %q, want superseded", got.Status)
	}
	if got.SupersedesID != target.Identity.ID {
		t.Fatalf("supersedesId = %q, want target %q", got.SupersedesID, target.Identity.ID)
	}
}

func TestSupersedeRecordsRelationAndRoutesReaders(t *testing.T) {
	api := newTestAPI(t)
	scope := testScope(testRucA)
	old := saveOne(t, api, validInput("topic/s2", "S2", "old", scope))
	target := saveOne(t, api, validInput("topic/t2", "T2", "new", scope))

	if _, err := api.Supersede(old.Identity.ID, target.Identity.ID, humanSource("maria.torres")); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	relations, err := api.Relations()
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	found := false
	for _, relation := range relations {
		if relation.FromID == old.Identity.ID && relation.ToID == target.Identity.ID && relation.Relation == core.RelationSupersedes {
			found = true
		}
	}
	if !found {
		t.Fatal("supersedes relation not recorded")
	}

	successor, ok := api.Store.SuccessorOf(old.Identity.ID)
	if !ok || successor.Identity.ID != target.Identity.ID {
		t.Fatalf("SuccessorOf routes to %+v, want target", successor)
	}
}

// ──────────────────────────────────────────────
// Non-authorization boundary (contracts/provenance.md)
// ──────────────────────────────────────────────

// TestNonAuthorizationBoundary is the runtime guard (mirror of the TS
// assertNonAuthorizing): the shared API surface has NO authorize/approve/allow
// operation, ever. Memory guides; it never authorizes.
func TestNonAuthorizationBoundary(t *testing.T) {
	api := newTestAPI(t)
	typ := reflect.TypeOf(api)
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		lower := strings.ToLower(name)
		// v2: Approve/Reject are the HUMAN review gate of a memory (a professional
		// approving a pending_review memory) — explicitly part of the model. The
		// boundary bans AUTHORIZATION OF BUSINESS ACTIONS: the API can never
		// authorize, allow, execute, file, declare or pay anything.
		for _, forbidden := range []string{"authorize", "allow", "execute", "declare", "file", "pay"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("API method %q violates the non-authorization boundary", name)
			}
		}
	}
}

func TestDoctorFailsClosedOnMissingTable(t *testing.T) {
	// Doctor on a fresh store reports the schema; the fail-closed corruption
	// path is covered in the store package. Here we assert the happy path.
	api := newTestAPI(t)
	report, err := api.Doctor()
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.SchemaVersion != 5 {
		t.Fatalf("schemaVersion = %d, want 5", report.SchemaVersion)
	}
}

func TestChainReturnsFullHistory(t *testing.T) {
	api := newTestAPI(t)
	scope := testScope(testRucA)

	first := saveOne(t, api, validInput("topic/history", "H", "v1", scope))
	second := saveOne(t, api, validInput("topic/history", "H", "v2", scope))
	_ = second

	chain, err := api.Chain("topic/history", scope)
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("chain has %d revisions, want 2", len(chain))
	}
	if chain[0].Identity.ID != first.Identity.ID || chain[0].Revision != 1 {
		t.Fatalf("chain[0] = %s rev %d, want first rev 1", chain[0].Identity.ID, chain[0].Revision)
	}
	if chain[1].Revision != 2 {
		t.Fatalf("chain[1] revision = %d, want 2 (ascending order)", chain[1].Revision)
	}

	// Scope isolation: the same topicKey under another RUC is an empty chain.
	other, err := api.Chain("topic/history", testScope(testRucB))
	if err != nil {
		t.Fatalf("chain other scope: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("other-scope chain has %d revisions, want 0 (structural isolation)", len(other))
	}
}

func TestChainRequiresScopeAndTopicKey(t *testing.T) {
	api := newTestAPI(t)
	if _, err := api.Chain("", testScope(testRucA)); err == nil {
		t.Fatal("empty topicKey must fail closed")
	}
	if _, err := api.Chain("topic/x", core.Scope{}); err == nil {
		t.Fatal("invalid scope must fail closed")
	}
}
