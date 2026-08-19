// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the HTTP identity→scope
// binding test surface (scope-param-rollout, FR-SPR-1/FR-SPR-4, AC-SPR-1/AC-SPR-4/
// AC-SPR-6, FD-SPR-1/FD-SPR-3/FD-SPR-4/FD-SPR-5, D-SPR-5): table-driven over
// EVERY generic read handler that derives its effective scope via
// httpQueryScope(r, false), proving that a verified principal's query-derived
// scope is bound to its membership scope (typed 403 on mismatch, unchanged on
// exact match, shared-token-only and institutional untouched). The canonical
// crossTenantCatalog() method set is NOT touched, so
// TestCrossTenantMatrixExhaustiveness stays green (NFR-SPR-3). No monetary
// field exists anywhere in this file (IR-1).
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// httpBindingCase describes one httpQueryScope-deriving generic read handler:
// its tenant-A seed (state the handler reads), its raw request builder for a
// given effective scope (no principal in the context), and its dispatcher.
// authOnly marks handlers mounted behind h.authenticate, whose no-principal leg
// is the frozen 401 AUTHENTICATION_REQUIRED (pre-change behavior, FD-SPR-3).
// mutates marks the state-mutating lifecycle handlers: their match leg compares
// like-for-like statuses on FRESH fixtures (per-fixture ids make byte-compare
// meaningless), never mutated state.
type httpBindingCase struct {
	name     string
	seed     func(t *testing.T, f *crossTenantFixture) operationState
	raw      func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request
	invoke   func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request)
	authOnly bool
	mutates  bool
}

// scopeQuery renders the effective scope as query parameters (no leading "?"):
// the exact ruc=&organizationId=&companyId=&period= tuple httpQueryScope parses
// for company scopes, or kind=institutional for institutional scopes.
func scopeQuery(scope core.Scope) string {
	if scope.Kind != core.ScopeKindCompany {
		return "kind=institutional"
	}
	return "ruc=" + url.QueryEscape(scope.RUC) +
		"&organizationId=" + url.QueryEscape(scope.OrganizationID) +
		"&companyId=" + url.QueryEscape(scope.CompanyID) +
		"&period=" + url.QueryEscape(scope.Period)
}

// httpBindingRequest builds a handler request (no principal in the context).
func httpBindingRequest(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, target, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

// httpPrincipalA mints the verified tenant-A principal through the REAL
// seed + resolver path the authenticate middleware uses (FD-SPR-1 membership
// tuple: tenant org-tenant-a, company co_a).
func httpPrincipalA(t *testing.T, f *crossTenantFixture) auth.VerifiedApprovalPrincipal {
	t.Helper()
	token := seedPurgeIdentity(t, f.api, f.scopeA.OrganizationID, f.scopeA.CompanyID, f.scopeA.RUC, "alice.a",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer, auth.RoleAccountant})
	return purgePrincipal(t, f.api, token)
}

// otherCompanyScope is a company scope inside tenant-A's organization for a
// company OUTSIDE the A principal's membership (the COMPANY_SCOPE_DENIED row:
// organizationId matches, companyId does not — FD-SPR-1).
func otherCompanyScope(f *crossTenantFixture) core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: f.scopeA.OrganizationID,
		CompanyID:      "co_other",
		RUC:            "11111111111",
		Period:         f.scopeA.Period,
	}
}

// seedTwoMemories seeds two tenant-A memories under scopeA (the Compare row).
func seedTwoMemories(t *testing.T, f *crossTenantFixture) operationState {
	t.Helper()
	s := seedMemory(t, f)
	second := saveOne(t, f.api, core.SaveInput{
		TopicKey:     ctTopicKey + "-compare",
		Title:        "tenant A compare target",
		Kind:         core.KindFact,
		Scope:        f.scopeA,
		Content:      core.Content{What: "tenant-a compare target content", Why: "fixture", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  "2024-01-16T00:00:00Z",
		Source:       testAgentSource,
		Confidence:   0.8,
	})
	s.toID = second.Identity.ID
	return s
}

// httpBindingCases is the complete FR-SPR-1 handler table: every generic read
// handler that derives its effective scope via httpQueryScope(r, false)
// (http.go:1805), in http.go declaration order, plus the writeGateTransition
// lifecycle routes (approve/reject/void) and the scope-first reads in
// review_http.go / rules_http.go / object_http.go / hold_http.go /
// retention_policy_http.go / purge_http.go. Save, reconstructibility and close
// create derive their scope from body/dedicated parsers and are intentionally
// absent (they never pass through httpQueryScope).
func httpBindingCases() []httpBindingCase {
	return []httpBindingCase{
		{
			name: "Get", seed: seedMemory,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodGet, "/v1/observations/"+s.memoryID+"?"+scopeQuery(scope), nil)
				r.SetPathValue("id", s.memoryID)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) { h.handleGet(w, r) },
		},
		{
			name: "GetByTopic", seed: seedMemory,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodGet, "/v1/topic/"+url.PathEscape(s.topicKey)+"?"+scopeQuery(scope), nil)
				r.SetPathValue("topicKey", s.topicKey)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleGetByTopic(w, r)
			},
		},
		{
			name: "Chain", seed: seedMemory,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				return httpBindingRequest(t, http.MethodGet, "/v1/chain?topicKey="+url.QueryEscape(s.topicKey)+"&"+scopeQuery(scope), nil)
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) { h.handleChain(w, r) },
		},
		{
			name: "Search", seed: seedMemory,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				return httpBindingRequest(t, http.MethodGet, "/v1/search?q=tenant-a-only&"+scopeQuery(scope), nil)
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) { h.handleSearch(w, r) },
		},
		{
			name: "Context", seed: seedMemory,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				return httpBindingRequest(t, http.MethodGet, "/v1/context?"+scopeQuery(scope), nil)
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleContext(w, r)
			},
		},
		{
			name: "Compare", seed: seedTwoMemories,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				return httpBindingRequest(t, http.MethodPost, "/v1/compare?"+scopeQuery(scope), map[string]any{"idA": s.memoryID, "idB": s.toID})
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleCompare(w, r)
			},
		},
		{
			name: "Supersede", seed: seedMemory, mutates: true,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodPost, "/v1/observations/"+s.memoryID+"/supersede?"+scopeQuery(scope), map[string]any{"targetId": s.memoryID, "actor": "fixture-actor"})
				r.SetPathValue("id", s.memoryID)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleSupersede(w, r)
			},
		},
		{
			name: "Approve", seed: seedMemory, mutates: true,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodPost, "/v1/observations/"+s.memoryID+"/approve?"+scopeQuery(scope), map[string]any{"actor": "fixture-actor"})
				r.SetPathValue("id", s.memoryID)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.writeGateTransition(w, r, func(id string, src core.Source) (any, error) { return h.api.Approve(id, src) })
			},
		},
		{
			name: "Reject", seed: seedMemory, mutates: true,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodPost, "/v1/observations/"+s.memoryID+"/reject?"+scopeQuery(scope), map[string]any{"actor": "fixture-actor"})
				r.SetPathValue("id", s.memoryID)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.writeGateTransition(w, r, func(id string, src core.Source) (any, error) { return h.api.Reject(id, src) })
			},
		},
		{
			name: "Void", seed: seedMemory, mutates: true,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodPost, "/v1/observations/"+s.memoryID+"/void?"+scopeQuery(scope), map[string]any{"actor": "fixture-actor"})
				r.SetPathValue("id", s.memoryID)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.writeGateTransition(w, r, func(id string, src core.Source) (any, error) { return h.api.Void(id, src) })
			},
		},
		{
			name: "Relations", seed: seedRelation,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				return httpBindingRequest(t, http.MethodGet, "/v1/relations?"+scopeQuery(scope), nil)
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleRelations(w, r)
			},
		},
		{
			name: "Transitions", seed: seedMemory,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				return httpBindingRequest(t, http.MethodGet, "/v1/transitions?"+scopeQuery(scope), nil)
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleTransitions(w, r)
			},
		},
		{
			name: "ReviewQueue", seed: seedReviewMemory,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				return httpBindingRequest(t, http.MethodGet, "/accounting/review/queue?"+scopeQuery(scope), nil)
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleReviewQueue(w, r)
			},
		},
		{
			name: "ReviewDetail", seed: seedReviewMemory,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodGet, "/accounting/review/"+s.memoryID+"?"+scopeQuery(scope), nil)
				r.SetPathValue("memoryId", s.memoryID)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleReviewDetail(w, r)
			},
		},
		{
			name: "RuleShow", seed: seedRule,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodGet, "/accounting/rules/"+url.PathEscape(s.topicKey)+"?"+scopeQuery(scope), nil)
				r.SetPathValue("topic", s.topicKey)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleRuleShow(w, r)
			},
		},
		{
			name: "RuleHistory", seed: seedRule,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodGet, "/accounting/rules/"+url.PathEscape(s.topicKey)+"/history?"+scopeQuery(scope), nil)
				r.SetPathValue("topic", s.topicKey)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleRuleHistory(w, r)
			},
		},
		{
			name: "RuleImpact", seed: seedRuleImpact, authOnly: true,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodGet, "/accounting/rules/"+url.PathEscape(s.topicKey)+"/impact?revision=1&"+scopeQuery(scope), nil)
				r.SetPathValue("topic", s.topicKey)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleRuleImpact(w, r)
			},
		},
		{
			name: "HoldList", seed: seedHold,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodGet, "/accounting/objects/"+s.objectID+"/holds?"+scopeQuery(scope), nil)
				r.SetPathValue("objectId", s.objectID)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleHoldList(w, r)
			},
		},
		{
			name: "RetentionResolve", seed: seedRetentionPolicy,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				return httpBindingRequest(t, http.MethodGet, "/accounting/retention-policies/resolve?"+scopeQuery(scope)+"&jurisdiction=PE&legislation=NATIONAL-TAX&category=invoice", nil)
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleRetentionPolicyResolve(w, r)
			},
		},
		{
			name: "LifecycleExport", seed: seedObject,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				return httpBindingRequest(t, http.MethodGet, "/accounting/lifecycle/export?"+scopeQuery(scope), nil)
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleLifecycleExport(w, r)
			},
		},
		{
			name: "ObjectGet", seed: seedObject,
			raw: func(t *testing.T, f *crossTenantFixture, s operationState, scope core.Scope) *http.Request {
				r := httpBindingRequest(t, http.MethodGet, "/accounting/objects/"+s.objectID+"?"+scopeQuery(scope), nil)
				r.SetPathValue("objectId", s.objectID)
				return r
			},
			invoke: func(t *testing.T, h *HTTPServer, w *httptest.ResponseRecorder, r *http.Request) {
				h.handleObjectGet(w, r)
			},
		},
	}
}

// bindingRow is one test row of TestHTTPIdentityScopeBinding.
type bindingRow string

const (
	rowMismatchTenant  bindingRow = "mismatch-tenant"   // principal-A + scope-B → 403 TENANT_SCOPE_MISMATCH, no API call
	rowMismatchCompany bindingRow = "mismatch-company"  // principal-A + same-tenant other-company → 403 COMPANY_SCOPE_DENIED, no API call
	rowMatch           bindingRow = "match"             // principal-A + scope-A → proceeds unchanged (FD-SPR-1)
	rowSharedToken     bindingRow = "shared-token-only" // no principal → caller-asserted, unchanged (FD-SPR-3)
	rowInstitutional   bindingRow = "institutional"     // kind=institutional → byte-identical with/without principal (FD-SPR-4)
)

// bindingSetup builds one fresh fixture for a binding row: the cross-tenant
// fixture, the HTTP server over it, the verified tenant-A principal and the
// tenant-A seed state.
func bindingSetup(t *testing.T, tc httpBindingCase) (*crossTenantFixture, *HTTPServer, auth.VerifiedApprovalPrincipal, operationState) {
	t.Helper()
	f := newCrossTenantFixture(t)
	h := NewHTTPServer(f.api, "")
	pA := httpPrincipalA(t, f)
	var s operationState
	if tc.seed != nil {
		s = tc.seed(t, f)
	}
	return f, h, pA, s
}

// runBindingRow executes one row on a FRESH fixture (no row depends on another;
// state-mutating handlers get a fresh fixture PER LEG so the with/without
// comparisons compare equal outcomes).
func runBindingRow(t *testing.T, tc httpBindingCase, row bindingRow) {
	t.Helper()
	f, h, pA, s := bindingSetup(t, tc)
	inject := func(scope core.Scope) *http.Request {
		r := tc.raw(t, f, s, scope)
		return r.WithContext(WithPrincipal(r.Context(), pA))
	}
	invoke := func(hh *HTTPServer, r *http.Request) (int, string) {
		w := httptest.NewRecorder()
		tc.invoke(t, hh, w, r)
		return w.Code, w.Body.String()
	}
	denied := func(code int, body string) bool {
		return code == http.StatusForbidden &&
			(strings.Contains(body, auth.CodeTenantScopeMismatch) || strings.Contains(body, auth.CodeCompanyScopeDenied))
	}

	switch row {
	case rowMismatchTenant, rowMismatchCompany:
		scope := f.scopeB
		want := auth.CodeTenantScopeMismatch
		if row == rowMismatchCompany {
			scope = otherCompanyScope(f)
			want = auth.CodeCompanyScopeDenied
		}
		before := storeDigest(t, f.api)
		code, body := invoke(h, inject(scope))
		if code != http.StatusForbidden || !strings.Contains(body, want) {
			t.Fatalf("principal-A + foreign scope = %d (%s), want 403 %s (FR-SPR-1/FD-SPR-5)", code, body, want)
		}
		if after := storeDigest(t, f.api); before != after {
			t.Fatalf("binding denial must precede ANY API call (FD-SPR-6): store digest %s -> %s", before, after)
		}
	case rowMatch:
		if tc.authOnly {
			// Authenticated-only handler: the no-principal leg is the frozen 401,
			// so "unchanged" is proven against the pre-change API outcome instead.
			code, body := invoke(h, inject(f.scopeA))
			if denied(code, body) {
				t.Fatalf("principal-A + scope-A must be allowed (FD-SPR-1), got a binding denial: %s", body)
			}
			if code != http.StatusOK {
				t.Fatalf("principal-A + scope-A = %d (%s), want the unchanged 200 API result", code, body)
			}
			return
		}
		withPCode, withPBody := invoke(h, inject(f.scopeA))
		if tc.mutates {
			// State-mutating handler: compare like-for-like statuses on a FRESH
			// fixture (per-fixture ids make byte-compare meaningless) — the binding
			// must let the exact-scope call through to the same API outcome.
			f2, h2, _, s2 := bindingSetup(t, tc)
			noPCode, noPBody := invoke(h2, tc.raw(t, f2, s2, f2.scopeA))
			if withPCode != noPCode || denied(withPCode, withPBody) || withPCode >= http.StatusInternalServerError {
				t.Fatalf("principal-A + scope-A must proceed unchanged (FD-SPR-1): with-principal (%d %s) != without-principal (%d %s)",
					withPCode, withPBody, noPCode, noPBody)
			}
			return
		}
		noPCode, noPBody := invoke(h, tc.raw(t, f, s, f.scopeA))
		if withPCode != noPCode || withPBody != noPBody {
			t.Fatalf("principal-A + scope-A must proceed unchanged (FD-SPR-1): with-principal (%d %s) != without-principal (%d %s)",
				withPCode, withPBody, noPCode, noPBody)
		}
	case rowSharedToken:
		code, body := invoke(h, tc.raw(t, f, s, f.scopeA))
		if denied(code, body) {
			t.Fatalf("shared-token-only must stay caller-asserted (FD-SPR-3), got a binding denial: %s", body)
		}
		if tc.authOnly && (code != http.StatusUnauthorized || !strings.Contains(body, auth.CodeAuthenticationRequired)) {
			t.Fatalf("authenticated-only handler without a principal = %d (%s), want the frozen 401 AUTHENTICATION_REQUIRED (unchanged)", code, body)
		}
	case rowInstitutional:
		inst := core.Scope{Kind: core.ScopeKindInstitutional}
		code, body := invoke(h, inject(inst))
		if denied(code, body) {
			t.Fatalf("institutional must never be binding-denied (FD-SPR-4), got %s", body)
		}
		if !tc.authOnly {
			noPCode, noPBody := invoke(h, tc.raw(t, f, s, inst))
			if code != noPCode || body != noPBody {
				t.Fatalf("institutional must be byte-identical with/without a principal (FD-SPR-4): with (%d %s) != without (%d %s)",
					code, body, noPCode, noPBody)
			}
		}
	}
}

// TestHTTPIdentityScopeBinding is the adapter-boundary binding test (AC-SPR-1,
// AC-SPR-4, FR-SPR-1/FR-SPR-4, D-SPR-5): every httpQueryScope-deriving generic
// read handler × the five rows above. RED before the binding exists (mismatch
// rows reach the API instead of the typed 403); GREEN only after every handler
// binds (S1.1 RED → S1.2 GREEN).
func TestHTTPIdentityScopeBinding(t *testing.T) {
	for _, tc := range httpBindingCases() {
		tc := tc
		for _, row := range []bindingRow{rowMismatchTenant, rowMismatchCompany, rowMatch, rowSharedToken, rowInstitutional} {
			row := row
			t.Run(tc.name+"/"+string(row), func(t *testing.T) { runBindingRow(t, tc, row) })
		}
	}
}

// TestHTTPIdentityBoundMatrix is the identity-bound sibling matrix (AC-SPR-6,
// NFR-SPR-3, D-SPR-5): principal-A + scope-B → typed SCOPE_DENIED and
// principal-A + scope-A → allowed, on HTTP, across the SAME handler table. The
// canonical crossTenantCatalog() method set is untouched, so
// TestCrossTenantMatrixExhaustiveness stays green (S1.3 RED → GREEN).
func TestHTTPIdentityBoundMatrix(t *testing.T) {
	for _, tc := range httpBindingCases() {
		tc := tc
		t.Run(tc.name+"/principal-A-scope-B/scope_denied", func(t *testing.T) { runBindingRow(t, tc, rowMismatchTenant) })
		t.Run(tc.name+"/principal-A-scope-A/allowed", func(t *testing.T) { runBindingRow(t, tc, rowMatch) })
	}
}
