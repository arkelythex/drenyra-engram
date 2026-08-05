// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the HTTP REST surface with
// structured-text observation fixtures; there are no monetary fields, so no
// money value crosses the wire.

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/search"
)

// newTestHTTPServer returns an httptest server over the shared API with the
// given optional token.
func newTestHTTPServer(t *testing.T, token string) (*httptest.Server, *API) {
	t.Helper()
	api := newTestAPI(t)
	httpServer := NewHTTPServer(api, token)
	ts := httptest.NewServer(httpServer.Handler())
	t.Cleanup(ts.Close)
	return ts, api
}

// httpScope builds a scope matching what the HTTP surface derives from query
// parameters (companyId := ruc), so HTTP saves and HTTP reads round-trip.
func httpScope(ruc string) core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: testOrgID,
		CompanyID:      ruc,
		RUC:            ruc,
		Period:         testPeriod,
	}
}

func httpJSON(t *testing.T, method, url, token string, body any) (int, string) {
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
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer func() { _ = response.Body.Close() }()
	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(response.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response.StatusCode, buffer.String()
}

func TestHTTPSaveAndGet(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")

	status, raw := httpJSON(t, http.MethodPost, ts.URL+"/v1/observations", "",
		validInput("topic/http", "HTTP", "round trip", testScope(testRucA)))
	if status != http.StatusCreated {
		t.Fatalf("save status = %d, want 201; body %s", status, raw)
	}
	var result core.WriteResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode save: %v", err)
	}

	status, raw = httpJSON(t, http.MethodGet, ts.URL+"/v1/observations/"+result.Memory.Identity.ID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body %s", status, raw)
	}
	var observation core.AccountingMemory
	if err := json.Unmarshal([]byte(raw), &observation); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if observation.Content.What != "round trip" {
		t.Fatalf("what = %q, want round trip", observation.Content.What)
	}
}

func TestHTTPGetNotFound(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	status, raw := httpJSON(t, http.MethodGet, ts.URL+"/v1/observations/no-such", "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", status, raw)
	}
	if !strings.Contains(raw, "NOT_FOUND") {
		t.Fatalf("body %q must carry the NOT_FOUND code", raw)
	}
}

func TestHTTPSearchInvalidScope(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	// ruc is missing — malformed scope must fail closed with 400.
	status, raw := httpJSON(t, http.MethodGet, ts.URL+"/v1/search?q=igv", "", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", status, raw)
	}
	if !strings.Contains(raw, "INVALID_RUC") {
		t.Fatalf("body %q must carry INVALID_RUC", raw)
	}
}

func TestHTTPSearchScopeIsolation(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	// The HTTP surface derives companyId from the RUC (CLI convention), so the
	// saved observation must use the same derived scope to round-trip.
	scopeA := httpScope(testRucA)

	status, raw := httpJSON(t, http.MethodPost, ts.URL+"/v1/observations", "",
		validInput("topic/x", "X", "confidential", scopeA))
	if status != http.StatusCreated {
		t.Fatalf("save status = %d, want 201; body %s", status, raw)
	}

	// Company B (different RUC) must not see company A's memory.
	status, raw = httpJSON(t, http.MethodGet,
		ts.URL+"/v1/search?q=confidential&ruc="+testRucB+"&organizationId="+testOrgID+"&period="+testPeriod, "", nil)
	if status != http.StatusOK {
		t.Fatalf("search status = %d, want 200; body %s", status, raw)
	}
	var results []search.Result
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("company B search returned %d results for company A memory", len(results))
	}

	// Company A sees it.
	status, raw = httpJSON(t, http.MethodGet,
		ts.URL+"/v1/search?q=confidential&ruc="+testRucA+"&organizationId="+testOrgID+"&period="+testPeriod, "", nil)
	if status != http.StatusOK {
		t.Fatalf("search status = %d, want 200; body %s", status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("company A search returned %d results, want 1", len(results))
	}
}

func TestHTTPLifecycleConflict(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	scope := testScope(testRucA)

	_, raw := httpJSON(t, http.MethodPost, ts.URL+"/v1/observations", "",
		validInput("topic/lc", "LC", "x", scope))
	var result core.WriteResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode save: %v", err)
	}
	id := result.Memory.Identity.ID

	// Approving an ACTIVE (informative, never-gated) memory is an illegal
	// transition → 409.
	status, raw := httpJSON(t, http.MethodPost, ts.URL+"/v1/observations/"+id+"/approve", "",
		map[string]string{"actorId": "maria.torres", "actorKind": "human"})
	if status != http.StatusConflict {
		t.Fatalf("approve status = %d, want 409; body %s", status, raw)
	}
	if !strings.Contains(raw, "INVALID_TRANSITION") {
		t.Fatalf("body %q must carry INVALID_TRANSITION", raw)
	}
}

func TestHTTPCompareSupersedes(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	scope := testScope(testRucA)
	old := saveOne(t, api, validInput("topic/cmp", "CMP", "old", scope))
	target := saveOne(t, api, validInput("topic/cmp2", "CMP2", "new", scope))

	if _, err := api.Supersede(old.Identity.ID, target.Identity.ID, humanSource("maria.torres")); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	status, raw := httpJSON(t, http.MethodPost, ts.URL+"/v1/compare", "",
		map[string]string{"idA": old.Identity.ID, "idB": target.Identity.ID})
	if status != http.StatusOK {
		t.Fatalf("compare status = %d, want 200; body %s", status, raw)
	}
	var output CompareOutput
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		t.Fatalf("decode compare: %v", err)
	}
	if output.RelationVerdict != "supersedes" || output.StatusA != core.StatusSuperseded {
		t.Fatalf("verdict/status = %q/%q, want supersedes/superseded", output.RelationVerdict, output.StatusA)
	}
}

func TestHTTPTokenAuth(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "secret-token")

	// No token → 401.
	status, _ := httpJSON(t, http.MethodGet, ts.URL+"/v1/doctor", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", status)
	}
	// Wrong token → 401.
	status, _ = httpJSON(t, http.MethodGet, ts.URL+"/v1/doctor", "wrong", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", status)
	}
	// Right token → 200.
	status, raw := httpJSON(t, http.MethodGet, ts.URL+"/v1/doctor", "secret-token", nil)
	if status != http.StatusOK {
		t.Fatalf("right token status = %d, want 200; body %s", status, raw)
	}
}

// TestHTTPNoAuthorizationEndpoints is the transport-level non-authorization
// boundary: no authorize/approve/allow route exists on the HTTP surface.
func TestHTTPNoAuthorizationEndpoints(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	for _, path := range []string{
		"/v1/authorize", "/v1/approve", "/v1/allow",
		"/v1/observations/x/authorize", "/v1/observations/x/approve",
	} {
		status, _ := httpJSON(t, http.MethodPost, ts.URL+path, "", nil)
		if status != http.StatusNotFound {
			t.Fatalf("POST %s status = %d, want 404 (authorization route must not exist)", path, status)
		}
	}
}

// TestHTTPMCPEndpoint runs the MCP JSON-RPC surface over POST /mcp — the same
// server binary serves agents over stdio and HTTP.
func TestHTTPMCPEndpoint(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")

	message := mcpRequest(1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"clientInfo":      map[string]string{"name": "test", "version": "1"},
	})
	status, raw := httpJSON(t, http.MethodPost, ts.URL+"/mcp", "", message)
	if status != http.StatusOK {
		t.Fatalf("mcp status = %d, want 200; body %s", status, raw)
	}
	var response jsonrpcResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("decode mcp response: %v", err)
	}
	if response.Error != nil {
		t.Fatalf("mcp error: %+v", response.Error)
	}
	if response.ID == nil {
		t.Fatal("mcp response missing id")
	}
}

// TestHTTPBodyTooLarge: an over-cap request body is rejected with 413, never
// silently truncated into a partial parse.
func TestHTTPBodyTooLarge(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	huge := strings.Repeat("x", maxBodyBytes+10)
	status, raw := httpJSON(t, http.MethodPost, ts.URL+"/v1/observations", "", map[string]string{"blob": huge})
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body %s", status, raw)
	}
	if !strings.Contains(raw, "TOO_LARGE") {
		t.Fatalf("body %q must carry TOO_LARGE", raw)
	}
}

// TestHTTPChainFullHistory: GET /v1/chain returns every revision of a
// (topicKey, exact scope) chain, ascending — the surface the fiscal-memory
// adapter uses for findById/findRevisions.
func TestHTTPChainFullHistory(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	scope := httpScope(testRucA)

	saveOne(t, api, validInput("topic/chain", "C", "v1", scope))
	saveOne(t, api, validInput("topic/chain", "C", "v2", scope))

	status, raw := httpJSON(t, http.MethodGet,
		ts.URL+"/v1/chain?topicKey=topic/chain&ruc="+testRucA+"&organizationId="+testOrgID+"&period="+testPeriod, "", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", status, raw)
	}
	var chain []core.AccountingMemory
	if err := json.Unmarshal([]byte(raw), &chain); err != nil {
		t.Fatalf("decode chain: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("chain has %d revisions, want 2", len(chain))
	}
	if chain[0].Content.What != "v1" || chain[1].Content.What != "v2" {
		t.Fatalf("chain order wrong: %q then %q", chain[0].Content.What, chain[1].Content.What)
	}

	// Missing topicKey fails closed.
	status, _ = httpJSON(t, http.MethodGet, ts.URL+"/v1/chain?ruc="+testRucA, "", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("missing topicKey status = %d, want 400", status)
	}
}
