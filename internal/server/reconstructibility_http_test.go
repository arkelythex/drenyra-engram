// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; the reconstructibility percentage is integer
// math, never floating point. This module tests the G-10 HTTP + API adapter
// surfaces (design D-1/D-4): GET /accounting/reconstructibility requires ALL
// FOUR exact-scope query fields (it NEVER applies the generic companyId := ruc
// fallback), is structurally isolated across tenants, carries the shared token
// guard, and serializes the frozen ReconstructibilityResult bytes — the same
// read-only, non-authorizing observation every transport shares (IR-3).
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// ──────────────────────────────────────────────
// Shared seeded fixture (reused by the MCP and parity tests)
// ──────────────────────────────────────────────

// reconstructCompanyA/B are the two exact company scopes of the adapter fixture:
// DIFFERENT companyId AND ruc so cross-tenant isolation is structural (IR-2),
// never a post-filter.
func reconstructCompanyA() core.Scope {
	return core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-acme", CompanyID: "co_a", RUC: "20100039201", Period: "202401"}
}

func reconstructCompanyB() core.Scope {
	return core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-acme", CompanyID: "co_b", RUC: "20600995804", Period: "202401"}
}

// saveMaterialDecision saves ONE FZ-1-shaped material decision (journal_entry,
// declared materiality level) under the exact scope WITHOUT approving it — the
// raw seeding primitive the approval wrapper and the eligibility-negative
// fixtures reuse.
func saveMaterialDecision(t *testing.T, api *API, topicKey string, scope core.Scope, lvl *core.MaterialityLevel) core.AccountingMemory {
	t.Helper()
	input := core.SaveInput{
		TopicKey:         topicKey,
		Title:            "material decision fixture",
		Kind:             core.KindDecision,
		Scope:            scope,
		Content:          core.Content{What: "w", Why: "y", Where: "f", Learned: "x"},
		FiscalEffect:     core.FiscalEffectJournalEntry,
		EffectiveAt:      "2026-01-15T12:00:00.000Z",
		Source:           core.Source{System: "go-test", ActorID: "agent", ActorKind: core.ActorKindAgent},
		MaterialityLevel: lvl,
	}
	return saveOne(t, api, input)
}

// seedReconstructibilityDecision saves + approves ONE FZ-1-eligible material
// decision (journal_entry, declared material, approved) under the exact scope
// and returns its id. No receipt chain is minted, so the metric maps the head
// through ErrNoReceipts → receipt_failed — the FZ-2 classification the adapters
// must surface. The approval uses the store's status transition with a human
// actor (the same deterministic pattern as the PR-1 service integration test).
func seedReconstructibilityDecision(t *testing.T, api *API, topicKey string, scope core.Scope) string {
	t.Helper()
	lvl := core.MaterialityMaterial
	mem := saveMaterialDecision(t, api, topicKey, scope, &lvl)
	if _, err := api.Store.ApplyStatusTransition(mem.Identity.ID, core.StatusApproved, core.TransitionMeta{
		Actor: "maria.torres", ActorKind: core.ActorKindHuman, Timestamp: "2026-01-16T12:00:00.000Z",
	}); err != nil {
		t.Fatalf("approve %s: %v", mem.Identity.ID, err)
	}
	return mem.Identity.ID
}

// expectedReconstructibilityResult builds the frozen result the seeded
// two-head fixture must produce: denominator = len(ids), numerator 0, every
// head receipt_failed (no signed chains), percentage 0 (integer math), reason
// ids sorted bytewise (FR-9 vi).
func expectedReconstructibilityResult(scope core.Scope, ids []string) ReconstructibilityResult {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	return ReconstructibilityResult{
		Scope:       scope,
		Period:      scope.Period,
		Denominator: len(sorted),
		Numerator:   0,
		Ratio:       core.ReconstructibilityRatio{Numerator: 0, Denominator: len(sorted)},
		Percentage:  intPtr(0),
		Reasons: ReconstructibilityReasons{
			NotApproved:           []string{},
			ReceiptFailed:         sorted,
			MissingEvidence:       []string{},
			EvidenceMissingObject: []string{},
			RuleUnresolved:        []string{},
			RuleVersionFailed:     []string{},
		},
		ZeroDenominator: false,
	}
}

// reconstructibilityQuery builds the exact-scope query string of the HTTP
// surface (all four fields).
func reconstructibilityQuery(scope core.Scope) string {
	return "organizationId=" + scope.OrganizationID +
		"&companyId=" + scope.CompanyID +
		"&ruc=" + scope.RUC +
		"&period=" + scope.Period
}

// ──────────────────────────────────────────────
// API method (the canonical server surface, design D-1)
// ──────────────────────────────────────────────

func TestAPIReconstructibilityFrozenResult(t *testing.T) {
	api := newTestAPI(t)
	scope := reconstructCompanyA()
	ids := []string{
		seedReconstructibilityDecision(t, api, "decision/alpha", scope),
		seedReconstructibilityDecision(t, api, "decision/beta", scope),
	}

	first, err := api.Reconstructibility(context.Background(), scope)
	if err != nil {
		t.Fatalf("api.Reconstructibility: %v", err)
	}
	want := expectedReconstructibilityResult(scope, ids)
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("api.Reconstructibility() =\n%+v\nwant\n%+v", first, want)
	}
	if first.Percentage == nil || *first.Percentage != 0 {
		t.Fatalf("percentage = %v, want integer 0 (denominator > 0)", first.Percentage)
	}

	// Double-run determinism + read-only (AC-8): the second run is byte-identical.
	second, err := api.Reconstructibility(context.Background(), scope)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	rawA, _ := json.Marshal(first)
	rawB, _ := json.Marshal(second)
	if string(rawA) != string(rawB) {
		t.Fatal("two API runs against the same store differ")
	}
}

// TestAPIReconstructibilityFZ1EligibilityThroughAdapter triangulates the
// eligibility predicate THROUGH the adapter surface: only the FZ-1 conjunction
// (latest + approved + six fiscal effects + material/critical level) reaches the
// denominator — an approved-but-normal-level head and a pending_review material
// head are excluded by the SQL, and their ids never appear in any reason list.
func TestAPIReconstructibilityFZ1EligibilityThroughAdapter(t *testing.T) {
	api := newTestAPI(t)
	scope := reconstructCompanyA()

	eligible := seedReconstructibilityDecision(t, api, "decision/eligible", scope) // approved + material
	normal := saveMaterialDecision(t, api, "decision/normal", scope, nil)          // approved + nil (== normal) → excluded
	if _, err := api.Store.ApplyStatusTransition(normal.Identity.ID, core.StatusApproved, core.TransitionMeta{
		Actor: "maria.torres", ActorKind: core.ActorKindHuman, Timestamp: "2026-01-16T12:00:00.000Z",
	}); err != nil {
		t.Fatalf("approve normal: %v", err)
	}
	lvl := core.MaterialityMaterial
	pending := saveMaterialDecision(t, api, "decision/pending", scope, &lvl) // material but NOT approved → excluded
	_ = pending

	got, err := api.Reconstructibility(context.Background(), scope)
	if err != nil {
		t.Fatalf("api.Reconstructibility: %v", err)
	}
	if got.Denominator != 1 || got.Numerator != 0 {
		t.Fatalf("metric = %d/%d, want 0/1 (only the approved material head is FZ-1-eligible)", got.Numerator, got.Denominator)
	}
	if !reflect.DeepEqual(got.Reasons.ReceiptFailed, []string{eligible}) {
		t.Fatalf("receipt_failed = %v, want [%s] — normal/pending heads must never reach any reason list", got.Reasons.ReceiptFailed, eligible)
	}
	for name, list := range map[string][]string{
		"not_approved": got.Reasons.NotApproved, "missing_evidence": got.Reasons.MissingEvidence,
		"evidence_missing_object": got.Reasons.EvidenceMissingObject, "rule_unresolved": got.Reasons.RuleUnresolved,
		"rule_version_failed": got.Reasons.RuleVersionFailed,
	} {
		if strings.Contains(strings.Join(list, ","), normal.Identity.ID) || strings.Contains(strings.Join(list, ","), pending.Identity.ID) {
			t.Fatalf("%s leaked an excluded head: %v", name, list)
		}
	}
}

func TestAPIReconstructibilityZeroDenominator(t *testing.T) {
	api := newTestAPI(t)
	scope := reconstructCompanyB() // valid scope, nothing seeded
	got, err := api.Reconstructibility(context.Background(), scope)
	if err != nil {
		t.Fatalf("api.Reconstructibility: %v", err)
	}
	if !got.ZeroDenominator || got.Denominator != 0 || got.Numerator != 0 {
		t.Fatalf("zero-denominator result = %+v", got)
	}
	if got.Percentage != nil {
		t.Fatalf("zero-denominator percentage = %d — must be null", *got.Percentage)
	}
	for name, list := range map[string][]string{
		"not_approved": got.Reasons.NotApproved, "receipt_failed": got.Reasons.ReceiptFailed,
		"missing_evidence": got.Reasons.MissingEvidence, "evidence_missing_object": got.Reasons.EvidenceMissingObject,
		"rule_unresolved": got.Reasons.RuleUnresolved, "rule_version_failed": got.Reasons.RuleVersionFailed,
	} {
		if len(list) != 0 {
			t.Fatalf("zero-denominator %s = %v — must be empty []", name, list)
		}
	}
}

// ──────────────────────────────────────────────
// HTTP route — GET /accounting/reconstructibility
// ──────────────────────────────────────────────

func TestHTTPReconstructibilityReturnsFrozenResult(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	scope := reconstructCompanyA()
	ids := []string{
		seedReconstructibilityDecision(t, api, "decision/alpha", scope),
		seedReconstructibilityDecision(t, api, "decision/beta", scope),
	}

	status, raw := httpJSON(t, http.MethodGet, ts.URL+"/accounting/reconstructibility?"+reconstructibilityQuery(scope), "", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", status, raw)
	}
	var got ReconstructibilityResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode body: %v\n%s", err, raw)
	}
	want := expectedReconstructibilityResult(scope, ids)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTP result =\n%+v\nwant\n%+v", got, want)
	}

	// Frozen JSON field order (D-4): scope, period, denominator, numerator,
	// ratio, percentage, reasons, zeroDenominator — the raw body bytes carry the
	// seeded ids in bytewise order inside receipt_failed and [] (never null)
	// for the empty reason lists.
	wantSorted := append([]string(nil), ids...)
	sort.Strings(wantSorted)
	pinned := `{"scope":{"kind":"company","organizationId":"org-acme","companyId":"co_a","ruc":"20100039201","period":"202401"},"period":"202401","denominator":2,"numerator":0,"ratio":{"numerator":0,"denominator":2},"percentage":0,"reasons":{"not_approved":[],"receipt_failed":["` +
		wantSorted[0] + `","` + wantSorted[1] + `"],"missing_evidence":[],"evidence_missing_object":[],"rule_unresolved":[],"rule_version_failed":[]},"zeroDenominator":false}`
	if strings.TrimSpace(raw) != pinned {
		t.Fatalf("frozen HTTP JSON bytes differ:\n got %s\nwant %s", strings.TrimSpace(raw), pinned)
	}
}

func TestHTTPReconstructibilityScopeIsolation(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	scopeA := reconstructCompanyA()
	idsA := []string{
		seedReconstructibilityDecision(t, api, "decision/alpha", scopeA),
		seedReconstructibilityDecision(t, api, "decision/beta", scopeA),
	}
	scopeB := reconstructCompanyB()

	// Company B sees ONLY its own zero — A's heads are structurally invisible.
	status, rawB := httpJSON(t, http.MethodGet, ts.URL+"/accounting/reconstructibility?"+reconstructibilityQuery(scopeB), "", nil)
	if status != http.StatusOK {
		t.Fatalf("B status = %d, want 200; body %s", status, rawB)
	}
	var gotB ReconstructibilityResult
	if err := json.Unmarshal([]byte(rawB), &gotB); err != nil {
		t.Fatalf("decode B body: %v", err)
	}
	if !gotB.ZeroDenominator || gotB.Denominator != 0 {
		t.Fatalf("B result = %+v — must be a zero denominator, never A's aggregate", gotB)
	}
	if strings.Contains(rawB, idsA[0]) || strings.Contains(rawB, idsA[1]) {
		t.Fatalf("B response leaks A's decision ids: %s", rawB)
	}

	// A still sees its own aggregate.
	status, rawA := httpJSON(t, http.MethodGet, ts.URL+"/accounting/reconstructibility?"+reconstructibilityQuery(scopeA), "", nil)
	if status != http.StatusOK {
		t.Fatalf("A status = %d, want 200; body %s", status, rawA)
	}
	var gotA ReconstructibilityResult
	if err := json.Unmarshal([]byte(rawA), &gotA); err != nil {
		t.Fatalf("decode A body: %v", err)
	}
	if gotA.Denominator != 2 || gotA.Numerator != 0 {
		t.Fatalf("A result = %d/%d, want 0/2", gotA.Numerator, gotA.Denominator)
	}
}

func TestHTTPReconstructibilityMissingFieldsFailClosed(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	scope := reconstructCompanyA()

	tests := []struct {
		name     string
		query    string
		wantCode string
	}{
		{"missing organizationId", "companyId=" + scope.CompanyID + "&ruc=" + scope.RUC + "&period=" + scope.Period, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"missing companyId", "organizationId=" + scope.OrganizationID + "&ruc=" + scope.RUC + "&period=" + scope.Period, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"missing ruc", "organizationId=" + scope.OrganizationID + "&companyId=" + scope.CompanyID + "&period=" + scope.Period, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"missing period", "organizationId=" + scope.OrganizationID + "&companyId=" + scope.CompanyID + "&ruc=" + scope.RUC, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"malformed ruc", "organizationId=" + scope.OrganizationID + "&companyId=" + scope.CompanyID + "&ruc=123&period=" + scope.Period, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"malformed period", "organizationId=" + scope.OrganizationID + "&companyId=" + scope.CompanyID + "&ruc=" + scope.RUC + "&period=202413", "INVALID_PERIOD"},
		{"institutional kind", "kind=institutional&organizationId=" + scope.OrganizationID + "&period=" + scope.Period, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, raw := httpJSON(t, http.MethodGet, ts.URL+"/accounting/reconstructibility?"+tt.query, "", nil)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", status, raw)
			}
			if !strings.Contains(raw, tt.wantCode) {
				t.Fatalf("body %q must carry stable code %q", raw, tt.wantCode)
			}
		})
	}
}

func TestHTTPReconstructibilityTokenGuard(t *testing.T) {
	ts, api := newTestHTTPServer(t, "sekret-token")
	scope := reconstructCompanyA()
	seedReconstructibilityDecision(t, api, "decision/alpha", scope)

	url := ts.URL + "/accounting/reconstructibility?" + reconstructibilityQuery(scope)

	// Without the shared token the read fails closed (401), never partially.
	status, raw := httpJSON(t, http.MethodGet, url, "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401; body %s", status, raw)
	}
	if !strings.Contains(raw, "UNAUTHORIZED") {
		t.Fatalf("no-token body %q must carry UNAUTHORIZED", raw)
	}

	// With the token the observation is served.
	status, raw = httpJSON(t, http.MethodGet, url, "sekret-token", nil)
	if status != http.StatusOK {
		t.Fatalf("token status = %d, want 200; body %s", status, raw)
	}
}

// ──────────────────────────────────────────────
// Adapter parity: MCP and HTTP share ONE store and emit the SAME bytes
// ──────────────────────────────────────────────

func TestReconstructibilityAdapterParityMCPHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	api := New(s, "test")
	m := NewMCPServer(api)
	httpServer := NewHTTPServer(api, "")
	ts := httptest.NewServer(httpServer.Handler())
	t.Cleanup(ts.Close)

	scope := reconstructCompanyA()
	ids := []string{
		seedReconstructibilityDecision(t, api, "decision/alpha", scope),
		seedReconstructibilityDecision(t, api, "decision/beta", scope),
	}

	// MCP text payload (mustJSON — no trailing newline).
	response := call(t, m, 1, "tools/call", map[string]any{
		"name":      "accounting_reconstructibility",
		"arguments": map[string]any{"scope": reconstructibilityScopeJSON(scope)},
	})
	if response.Error != nil {
		t.Fatalf("MCP call error: %+v", response.Error)
	}
	mcpText := toolResultText(t, response)

	// HTTP body (json.Encoder — the same JSON plus one trailing newline).
	status, httpBody := httpJSON(t, http.MethodGet, ts.URL+"/accounting/reconstructibility?"+reconstructibilityQuery(scope), "", nil)
	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d; body %s", status, httpBody)
	}

	want := expectedReconstructibilityResult(scope, ids)
	if mcpText != string(mustJSON(want)) {
		t.Fatalf("MCP bytes differ:\n got %s\nwant %s", mcpText, string(mustJSON(want)))
	}
	if httpBody != mcpText+"\n" {
		t.Fatalf("HTTP/MCP parity broken:\nHTTP %q\nMCP+NL %q", httpBody, mcpText+"\n")
	}
}

// reconstructibilityScopeJSON serializes the MCP scope argument (a JSON string).
func reconstructibilityScopeJSON(scope core.Scope) string {
	raw, err := json.Marshal(scope)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
