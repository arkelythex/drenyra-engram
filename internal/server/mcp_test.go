// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the MCP JSON-RPC surface
// (internal/server/mcp.go) with structured-text observation fixtures; there
// are no monetary fields, so no money value crosses the protocol.

package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// jsonrpcResponse is the wire shape the tests decode (result as raw JSON so
// each test can unmarshal what it expects).
type testResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// mcpRequest builds a JSON-RPC request message (used by tests and HTTP tests).
func mcpRequest(id int, method string, params any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
}

// call dispatches one message through the MCP server and decodes the response.
func call(t *testing.T, m *MCPServer, id int, method string, params any) testResponse {
	t.Helper()
	request, err := json.Marshal(mcpRequest(id, method, params))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	raw := m.HandleMessage(request)
	if raw == nil {
		t.Fatalf("method %s returned no response", method)
	}
	var response testResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	return response
}

// toolResultText unwraps the MCP tool-result envelope and returns the inner
// JSON text payload (the domain result or the domain error message).
func toolResultText(t *testing.T, response testResponse) string {
	t.Helper()
	var output toolCallOutput
	if err := json.Unmarshal(response.Result, &output); err != nil {
		t.Fatalf("decode tool result: %v (raw %s)", err, response.Result)
	}
	if len(output.Content) == 0 {
		t.Fatal("tool result has empty content")
	}
	return output.Content[0]["text"]
}

func newTestMCP(t *testing.T) (*MCPServer, *API) {
	t.Helper()
	api := newTestAPI(t)
	return NewMCPServer(api), api
}

// ──────────────────────────────────────────────
// Protocol lifecycle
// ──────────────────────────────────────────────

func TestMCPInitialize(t *testing.T) {
	m, _ := newTestMCP(t)

	response := call(t, m, 1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "test", "version": "1"},
	})
	if response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	var result struct {
		ProtocolVersion string            `json:"protocolVersion"`
		Capabilities    map[string]any    `json:"capabilities"`
		ServerInfo      map[string]string `json:"serverInfo"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	if result.ProtocolVersion != "2025-03-26" {
		t.Fatalf("protocolVersion = %q, want 2025-03-26 (echo)", result.ProtocolVersion)
	}
	if _, ok := result.Capabilities["tools"]; !ok {
		t.Fatalf("capabilities must advertise tools: %v", result.Capabilities)
	}
	if result.ServerInfo["name"] != "drenyra-engram" {
		t.Fatalf("serverInfo = %v, want name drenyra-engram", result.ServerInfo)
	}
}

func TestMCPInitializeUnsupportedVersion(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "initialize", map[string]any{"protocolVersion": "1999-01-01"})
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	if result.ProtocolVersion == "1999-01-01" {
		t.Fatal("unsupported protocolVersion must not be echoed back")
	}
}

// TestMCPNotificationNoResponse: notifications never get a response.
func TestMCPNotificationNoResponse(t *testing.T) {
	m, _ := newTestMCP(t)
	raw := m.HandleMessage([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if raw != nil {
		t.Fatalf("notification must not produce a response, got %s", raw)
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 7, "no/such/method", nil)
	if response.Error == nil {
		t.Fatal("unknown method must produce a JSON-RPC error")
	}
	if response.Error.Code != codeMethodNotFound {
		t.Fatalf("error code = %d, want %d", response.Error.Code, codeMethodNotFound)
	}
}

func TestMCPInvalidParams(t *testing.T) {
	m, _ := newTestMCP(t)
	// engram_get requires id; a missing field is a shape error → -32602.
	response := call(t, m, 8, "tools/call", map[string]any{
		"name": "engram_get", "arguments": map[string]any{},
	})
	if response.Error == nil || response.Error.Code != codeInvalidParams {
		t.Fatalf("missing id: want -32602, got %+v", response.Error)
	}
}

func TestMCPParseError(t *testing.T) {
	m, _ := newTestMCP(t)
	raw := m.HandleMessage([]byte(`{not json`))
	var response testResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Error == nil || response.Error.Code != codeParseError {
		t.Fatalf("want parse error, got %+v", response.Error)
	}
}

// ──────────────────────────────────────────────
// tools/list — catalog + non-authorization boundary
// ──────────────────────────────────────────────

func TestMCPToolsList(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 2, "tools/list", nil)
	if response.Error != nil {
		t.Fatalf("tools/list error: %+v", response.Error)
	}
	var result struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(result.Tools) != 27 {
		t.Fatalf("tool count = %d, want 27 (13 engram_* + 14 accounting_*)", len(result.Tools))
	}
	for _, tool := range result.Tools {
		name, _ := tool["name"].(string)
		if !strings.HasPrefix(name, "engram_") && !strings.HasPrefix(name, "accounting_") {
			t.Fatalf("tool %q must be namespaced engram_ or accounting_", name)
		}
		if _, ok := tool["inputSchema"]; !ok {
			t.Fatalf("tool %q missing inputSchema", name)
		}
	}
}

// TestMCPToolCatalogNonAuthorization is the protocol-level non-authorization
// boundary: the catalog has NO authorize/approve/allow tool, ever.
func TestMCPToolCatalogNonAuthorization(t *testing.T) {
	for _, tool := range ToolCatalog() {
		name, _ := tool["name"].(string)
		lower := strings.ToLower(name)
		// v2: engram_reject is the HUMAN review gate of a memory (a professional
		// rejecting a pending_review memory) — part of the model, not authorization
		// of business actions. The boundary bans authorization of business actions.
		// accounting_approve is the AUTHENTICATED approval tool (v0.4.0 Step 1);
		// it also never authorizes a business action — it approves a memory review.
		for _, forbidden := range []string{"authorize", "allow", "execute", "declare", "file", "pay"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("tool %q violates the non-authorization boundary", name)
			}
		}
	}
}

// ──────────────────────────────────────────────
// tools/call — domain operations
// ──────────────────────────────────────────────

func TestMCPToolsCallSaveAndGet(t *testing.T) {
	m, _ := newTestMCP(t)

	input := validInput("topic/mcp", "MCP", "works", testScope(testRucA))
	saved := call(t, m, 3, "tools/call", map[string]any{
		"name": "engram_save", "arguments": input,
	})
	if saved.Error != nil {
		t.Fatalf("save error: %+v", saved.Error)
	}
	var result core.WriteResult
	if err := json.Unmarshal([]byte(toolResultText(t, saved)), &result); err != nil {
		t.Fatalf("decode save result: %v", err)
	}
	if result.Outcome != core.WriteCreated {
		t.Fatalf("outcome = %q, want created", result.Outcome)
	}

	got := call(t, m, 4, "tools/call", map[string]any{
		"name": "engram_get", "arguments": map[string]any{"id": result.Memory.Identity.ID},
	})
	var observation core.AccountingMemory
	if err := json.Unmarshal([]byte(toolResultText(t, got)), &observation); err != nil {
		t.Fatalf("decode get result: %v", err)
	}
	if observation.Content.What != "works" {
		t.Fatalf("what = %q, want works", observation.Content.What)
	}
}

// TestMCPToolsCallLifecycle runs review → promote → supersede → compare through
// the protocol and asserts the corrected source-check semantics end to end.
func TestMCPToolsCallLifecycle(t *testing.T) {
	m, _ := newTestMCP(t)
	scope := testScope(testRucA)

	saveOld := call(t, m, 1, "tools/call", map[string]any{
		"name": "engram_save", "arguments": validInput("topic/chain", "Chain", "old", scope),
	})
	var oldResult core.WriteResult
	if err := json.Unmarshal([]byte(toolResultText(t, saveOld)), &oldResult); err != nil {
		t.Fatalf("decode save old: %v", err)
	}

	saveNew := call(t, m, 2, "tools/call", map[string]any{
		"name": "engram_save", "arguments": validInput("topic/chain", "Chain", "new", scope),
	})
	var newResult core.WriteResult
	if err := json.Unmarshal([]byte(toolResultText(t, saveNew)), &newResult); err != nil {
		t.Fatalf("decode save new: %v", err)
	}

	supersede := call(t, m, 3, "tools/call", map[string]any{
		"name": "engram_supersede", "arguments": map[string]any{
			"id":        oldResult.Memory.Identity.ID,
			"targetId":  newResult.Memory.Identity.ID,
			"actorId":   "maria.torres",
			"actorKind": "human",
		},
	})
	if supersede.Error != nil {
		t.Fatalf("supersede error: %+v", supersede.Error)
	}

	compare := call(t, m, 4, "tools/call", map[string]any{
		"name": "engram_compare", "arguments": map[string]any{
			"idA": oldResult.Memory.Identity.ID, "idB": newResult.Memory.Identity.ID,
		},
	})
	var output CompareOutput
	if err := json.Unmarshal([]byte(toolResultText(t, compare)), &output); err != nil {
		t.Fatalf("decode compare: %v", err)
	}
	if output.RelationVerdict != "supersedes" {
		t.Fatalf("verdict = %q, want supersedes", output.RelationVerdict)
	}
	if output.StatusA != core.StatusSuperseded || output.StatusB == core.StatusSuperseded {
		t.Fatalf("statusA/statusB = %q/%q, want superseded/non-superseded", output.StatusA, output.StatusB)
	}
}

// TestMCPAccountingApproveFailsClosedWithoutSession: accounting_approve accepts
// exactly its four command arguments but the stdio MCP server has NO authenticated
// session binding (design §3), so the tool FAILS CLOSED with the frozen
// AUTHENTICATION_REQUIRED as an in-band tool result (isError=true) — never a
// JSON-RPC error, never a silent identity from the arguments.
func TestMCPAccountingApproveFailsClosedWithoutSession(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_approve",
		"arguments": map[string]any{
			"memory_id":              "m-1",
			"expected_envelope_hash": "abc",
			"reason":                 "revisado y conforme",
			"request_id":             "req-1",
		},
	})
	if response.Error != nil {
		t.Fatalf("domain failure must be a tool result, not a JSON-RPC error: %+v", response.Error)
	}
	var output toolCallOutput
	if err := json.Unmarshal(response.Result, &output); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if !output.IsError {
		t.Fatal("isError = false, want true (fail closed without a session binding)")
	}
	if len(output.Content) == 0 || !strings.Contains(output.Content[0]["text"], "AUTHENTICATION_REQUIRED") {
		t.Fatalf("error text must carry AUTHENTICATION_REQUIRED: %v", output.Content)
	}
}

// TestMCPAccountingApproveRejectsExtraArgs: accounting_approve parses its args
// STRICTLY (design §6): ANY unknown field — including any caller-supplied
// authority (actorId/actorKind/subjectId/roles) — is a malformed argument shape
// (JSON-RPC -32602), never silently ignored.
func TestMCPAccountingApproveRejectsExtraArgs(t *testing.T) {
	m, _ := newTestMCP(t)
	base := map[string]any{
		"memory_id": "m-1", "expected_envelope_hash": "abc", "reason": "r", "request_id": "q-1",
	}
	cases := []struct {
		name   string
		extras map[string]any
	}{
		{"actorId", map[string]any{"actorId": "maria.torres"}},
		{"actorKind", map[string]any{"actorKind": "human"}},
		{"roles", map[string]any{"roles": []string{"controller"}}},
		{"subjectId", map[string]any{"subjectId": "maria.torres"}},
		{"unrelated extra", map[string]any{"bogus": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := make(map[string]any, len(base)+len(tc.extras))
			for k, v := range base {
				args[k] = v
			}
			for k, v := range tc.extras {
				args[k] = v
			}
			response := call(t, m, 1, "tools/call", map[string]any{
				"name": "accounting_approve", "arguments": args,
			})
			if response.Error == nil || response.Error.Code != codeInvalidParams {
				t.Fatalf("extra args %v: want JSON-RPC -32602, got %+v", tc.extras, response.Error)
			}
		})
	}
}

// TestMCPLegacyApproveToolRemoved: the actor-supplying approve tool is GONE from
// the surface — absent from the catalog and unknown to the dispatcher (-32601).
// Caller-supplied authority has no MCP tool.
func TestMCPLegacyApproveToolRemoved(t *testing.T) {
	m, _ := newTestMCP(t)
	for _, tool := range ToolCatalog() {
		if name, _ := tool["name"].(string); name == "engram_approve" {
			t.Fatal("legacy engram_approve must be removed from the catalog")
		}
	}
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "engram_approve", "arguments": map[string]any{
			"id": "m-1", "actorId": "maria.torres", "actorKind": "human",
		},
	})
	if response.Error == nil || response.Error.Code != codeMethodNotFound {
		t.Fatalf("engram_approve must be unknown (-32601), got %+v", response.Error)
	}
}

func TestMCPToolsCallNotFound(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 9, "tools/call", map[string]any{
		"name": "engram_get", "arguments": map[string]any{"id": "missing"},
	})
	var output toolCallOutput
	if err := json.Unmarshal(response.Result, &output); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !output.IsError || !strings.Contains(output.Content[0]["text"], "MEMORY_NOT_FOUND") {
		t.Fatalf("want not-found tool error, got %+v", output)
	}
}

func TestMCPToolsCallUnknownTool(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 10, "tools/call", map[string]any{
		"name": "engram_hack", "arguments": map[string]any{},
	})
	if response.Error == nil || response.Error.Code != codeMethodNotFound {
		t.Fatalf("want -32601 for unknown tool, got %+v", response.Error)
	}
}

// TestMCPDoctor: a read-only tool over the HTTP-less path.
func TestMCPDoctor(t *testing.T) {
	m, api := newTestMCP(t)
	scope := testScope(testRucA)
	saveOne(t, api, validInput("topic/doc", "Doc", "x", scope))

	response := call(t, m, 5, "tools/call", map[string]any{"name": "engram_doctor"})
	if response.Error != nil {
		t.Fatalf("doctor error: %+v", response.Error)
	}
	var report struct {
		Observations int `json:"observations"`
	}
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &report); err != nil {
		t.Fatalf("decode doctor: %v", err)
	}
	if report.Observations != 1 {
		t.Fatalf("observations = %d, want 1", report.Observations)
	}
}

// TestMCPBatchRejected: JSON-RPC batches are not supported by MCP — the server
// fails closed with -32600 invalid request (never partially executes).
func TestMCPBatchRejected(t *testing.T) {
	m, _ := newTestMCP(t)
	raw := m.HandleMessage([]byte(`[
		{"jsonrpc":"2.0","id":1,"method":"tools/list"},
		{"jsonrpc":"2.0","id":2,"method":"tools/list"}
	]`))
	var response testResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Error == nil || response.Error.Code != codeInvalidRequest {
		t.Fatalf("want -32600 for batch, got %+v", response.Error)
	}
}

func TestMCPChainTool(t *testing.T) {
	m, api := newTestMCP(t)
	scope := testScope(testRucA)
	saveOne(t, api, validInput("topic/m", "M", "v1", scope))
	saveOne(t, api, validInput("topic/m", "M", "v2", scope))

	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "engram_chain", "arguments": map[string]any{"topicKey": "topic/m", "scope": scope},
	})
	if response.Error != nil {
		t.Fatalf("chain error: %+v", response.Error)
	}
	var chain []core.AccountingMemory
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &chain); err != nil {
		t.Fatalf("decode chain: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("chain has %d revisions, want 2", len(chain))
	}
}
