// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the MCP first-class
// reconciliation surface (v0.5.0 — design §3.2/§6): accounting_reconciliation_
// propose and _withdraw carry a provenance-only source (agent|system, NEVER
// authority); _confirm and _reject accept NO identity arguments and fail closed
// with AUTHENTICATION_REQUIRED on this session-less stdio server; ALL four
// decode strictly (any unknown field — including any caller-supplied authority —
// is -32602, never ignored). The domain amounts travel as integer cents (int64;
// never floats) and the variance is engine-derived.
package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// mcpReconciliationPair seeds the two same-company observations a proposal needs
// and returns their ids (the MCP test scope: org-acme / acme / testRucA /
// 202401).
func mcpReconciliationPair(t *testing.T, api *API) (leftID, rightID string) {
	t.Helper()
	scope := testScope(testRucA)
	left := saveOne(t, api, validInput("reconciliation/mcp/left", "Saldo de mayor 4011",
		"Saldo de mayor S/ 10,000.00", scope))
	right := saveOne(t, api, validInput("reconciliation/mcp/right", "Saldo de SIRE 4011",
		"Saldo de SIRE S/ 9,840.00", scope))
	return left.Identity.ID, right.Identity.ID
}

// proposeReconciliationMCP proposes over the pair with the given provenance
// source, idempotency key and domain amounts (int64 cents; never floats).
func proposeReconciliationMCP(t *testing.T, m *MCPServer, leftID, rightID, key string, source map[string]any) testResponse {
	t.Helper()
	return call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_reconciliation_propose",
		"arguments": map[string]any{
			"left_memory_id":     leftID,
			"right_memory_id":    rightID,
			"method":             "extracto_contable",
			"currency":           "PEN",
			"left_amount_cents":  1000000,
			"right_amount_cents": 984000,
			"tolerance_cents":    16000,
			"reason":             "diferencia de saldo entre mayor y SIRE",
			"request_id":         key,
			"source":             source,
		},
	})
}

// TestMCPReconciliationProposeHappyPath: an agent source proposes over two
// existing observations → the proposed reconciliation arrives as the in-band
// tool result; the provenance is preserved, the tenant/company are derived from
// the observations, the amounts are int64 cents and the variance is
// engine-derived (left − right).
func TestMCPReconciliationProposeHappyPath(t *testing.T) {
	m, api := newTestMCP(t)
	leftID, rightID := mcpReconciliationPair(t, api)

	response := proposeReconciliationMCP(t, m, leftID, rightID, "mcp-reconciliation-propose-1", agentMCPSource("agent-1"))
	if response.Error != nil {
		t.Fatalf("propose error: %+v", response.Error)
	}
	var result core.ProposeReconciliationResult
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &result); err != nil {
		t.Fatalf("decode propose result: %v", err)
	}
	if result.ReconciliationID == "" || result.ReconciliationID != result.Reconciliation.ID {
		t.Errorf("reconciliationId = %q, want the reconciliation id %q", result.ReconciliationID, result.Reconciliation.ID)
	}
	if result.IdempotentReplay {
		t.Error("fresh proposal must not be a replay")
	}
	r := result.Reconciliation
	if r.Status != core.ReconciliationProposed {
		t.Errorf("status = %q, want proposed", r.Status)
	}
	if r.LeftMemoryID != leftID || r.RightMemoryID != rightID {
		t.Errorf("pair = %s/%s, want %s/%s", r.LeftMemoryID, r.RightMemoryID, leftID, rightID)
	}
	if r.Method != "extracto_contable" || r.Currency != "PEN" {
		t.Errorf("method/currency = %q/%q, want extracto_contable/PEN", r.Method, r.Currency)
	}
	if r.LeftAmountCents != 1000000 || r.RightAmountCents != 984000 {
		t.Errorf("amounts = %d/%d, want 1000000/984000 (int64 cents)", r.LeftAmountCents, r.RightAmountCents)
	}
	if r.VarianceCents != 16000 {
		t.Errorf("varianceCents = %d, want 16000 (engine-derived left − right)", r.VarianceCents)
	}
	if r.ToleranceCents != 16000 {
		t.Errorf("toleranceCents = %d, want 16000", r.ToleranceCents)
	}
	if r.TenantID != testOrgID || r.CompanyID != "acme" {
		t.Errorf("scope = %s/%s, want %s/acme (derived from the observations)", r.TenantID, r.CompanyID, testOrgID)
	}
	wantProposer := core.Source{System: "mcp", ActorID: "agent-1", ActorKind: core.ActorKindAgent, Session: "sess-1"}
	if r.Proposer != wantProposer {
		t.Errorf("proposer = %+v, want %+v (provenance preserved, never authority)", r.Proposer, wantProposer)
	}
	if r.ProposalReason != "diferencia de saldo entre mayor y SIRE" || r.Resolution != "" {
		t.Errorf("reason/resolution = %q/%q, want reason/empty", r.ProposalReason, r.Resolution)
	}
	if r.DecidedAt != "" {
		t.Errorf("decidedAt = %q, want empty for an open proposal", r.DecidedAt)
	}
}

// TestMCPReconciliationProposeHumanSourceDenied: a human source is
// provenance-only and cannot propose — PROPOSAL_UNAUTHORIZED as an in-band tool
// result.
func TestMCPReconciliationProposeHumanSourceDenied(t *testing.T) {
	m, api := newTestMCP(t)
	leftID, rightID := mcpReconciliationPair(t, api)
	human := map[string]any{"system": "mcp", "actor_id": "maria.torres", "actor_kind": "human"}

	response := proposeReconciliationMCP(t, m, leftID, rightID, "mcp-reconciliation-human-1", human)
	text := toolCallErrorText(t, response)
	if !strings.Contains(text, "PROPOSAL_UNAUTHORIZED") {
		t.Fatalf("error text must carry PROPOSAL_UNAUTHORIZED: %q", text)
	}
}

// TestMCPReconciliationProposeRejectsUnknownFields:
// accounting_reconciliation_propose parses its args STRICTLY (design §6): ANY
// unknown field — including any caller-supplied authority (actorId/subjectId/
// roles) at top level or inside the source object — is a malformed argument
// shape (JSON-RPC -32602), never silently ignored.
func TestMCPReconciliationProposeRejectsUnknownFields(t *testing.T) {
	m, api := newTestMCP(t)
	leftID, rightID := mcpReconciliationPair(t, api)
	base := map[string]any{
		"left_memory_id":     leftID,
		"right_memory_id":    rightID,
		"method":             "extracto_contable",
		"currency":           "PEN",
		"left_amount_cents":  100,
		"right_amount_cents": 100,
		"tolerance_cents":    0,
		"reason":             "r",
		"request_id":         "q-1",
		"source":             agentMCPSource("agent-1"),
	}
	cases := []struct {
		name   string
		extras map[string]any
	}{
		{"top-level actorId", map[string]any{"actorId": "maria.torres"}},
		{"top-level actorKind", map[string]any{"actorKind": "human"}},
		{"top-level subjectId", map[string]any{"subjectId": "subject-1"}},
		{"top-level roles", map[string]any{"roles": []string{"controller"}}},
		{"unrelated extra", map[string]any{"bogus": 1}},
		{"inside source", map[string]any{"source": map[string]any{
			"system": "mcp", "actor_id": "a", "actor_kind": "agent", "roles": []string{"controller"},
		}}},
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
				"name": "accounting_reconciliation_propose", "arguments": args,
			})
			if response.Error == nil || response.Error.Code != codeInvalidParams {
				t.Fatalf("extra args %v: want JSON-RPC -32602, got %+v", tc.extras, response.Error)
			}
		})
	}
}

// TestMCPReconciliationConfirmFailsClosedWithoutSession:
// accounting_reconciliation_confirm accepts exactly its four command arguments
// but the stdio MCP server has NO authenticated session binding (design §3), so
// the tool FAILS CLOSED with the frozen AUTHENTICATION_REQUIRED as an in-band
// tool result — never a JSON-RPC error, never a silent identity from the
// arguments.
func TestMCPReconciliationConfirmFailsClosedWithoutSession(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_reconciliation_confirm",
		"arguments": map[string]any{
			"reconciliation_id":            "r-1",
			"resolution":                   "resolución profesional",
			"expected_reconciliation_hash": "abc",
			"request_id":                   "req-1",
		},
	})
	text := toolCallErrorText(t, response)
	if !strings.Contains(text, "AUTHENTICATION_REQUIRED") {
		t.Fatalf("error text must carry AUTHENTICATION_REQUIRED: %q", text)
	}
}

// TestMCPReconciliationConfirmRejectsUnknownFields: authority fields are
// rejected as a malformed argument shape (-32602) even on the fail-closed
// confirm tool.
func TestMCPReconciliationConfirmRejectsUnknownFields(t *testing.T) {
	m, _ := newTestMCP(t)
	base := map[string]any{
		"reconciliation_id": "r-1", "resolution": "r", "expected_reconciliation_hash": "abc", "request_id": "q-1",
	}
	for _, tc := range []struct {
		name   string
		extras map[string]any
	}{
		{"actorId", map[string]any{"actorId": "maria.torres"}},
		{"roles", map[string]any{"roles": []string{"controller"}}},
		{"unrelated extra", map[string]any{"bogus": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := make(map[string]any, len(base)+len(tc.extras))
			for k, v := range base {
				args[k] = v
			}
			for k, v := range tc.extras {
				args[k] = v
			}
			response := call(t, m, 1, "tools/call", map[string]any{
				"name": "accounting_reconciliation_confirm", "arguments": args,
			})
			if response.Error == nil || response.Error.Code != codeInvalidParams {
				t.Fatalf("extra args %v: want JSON-RPC -32602, got %+v", tc.extras, response.Error)
			}
		})
	}
}

// TestMCPReconciliationRejectFailsClosedWithoutSession: same fail-closed
// contract for accounting_reconciliation_reject.
func TestMCPReconciliationRejectFailsClosedWithoutSession(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_reconciliation_reject",
		"arguments": map[string]any{
			"reconciliation_id":            "r-1",
			"reason":                       "evidencia insuficiente",
			"expected_reconciliation_hash": "abc",
			"request_id":                   "req-1",
		},
	})
	text := toolCallErrorText(t, response)
	if !strings.Contains(text, "AUTHENTICATION_REQUIRED") {
		t.Fatalf("error text must carry AUTHENTICATION_REQUIRED: %q", text)
	}
}

// TestMCPReconciliationWithdrawSameProposer: the SAME proposer identity
// withdraws its own proposal → the withdrawn reconciliation is the in-band tool
// result.
func TestMCPReconciliationWithdrawSameProposer(t *testing.T) {
	m, api := newTestMCP(t)
	leftID, rightID := mcpReconciliationPair(t, api)

	response := proposeReconciliationMCP(t, m, leftID, rightID, "mcp-reconciliation-withdraw-1", agentMCPSource("agent-1"))
	var proposed core.ProposeReconciliationResult
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &proposed); err != nil {
		t.Fatalf("decode propose result: %v", err)
	}

	withdraw := call(t, m, 2, "tools/call", map[string]any{
		"name": "accounting_reconciliation_withdraw",
		"arguments": map[string]any{
			"reconciliation_id": proposed.ReconciliationID,
			"request_id":        "mcp-reconciliation-withdraw-1w",
			"source":            agentMCPSource("agent-1"),
		},
	})
	if withdraw.Error != nil {
		t.Fatalf("withdraw error: %+v", withdraw.Error)
	}
	var result core.WithdrawReconciliationResult
	if err := json.Unmarshal([]byte(toolResultText(t, withdraw)), &result); err != nil {
		t.Fatalf("decode withdraw result: %v", err)
	}
	if result.Reconciliation.Status != core.ReconciliationWithdrawn {
		t.Errorf("status = %q, want withdrawn (terminal)", result.Reconciliation.Status)
	}
	if result.ReconciliationEventID == "" {
		t.Error("reconciliationEventId must be set on withdrawal")
	}
}

// TestMCPReconciliationWithdrawDifferentProposer: a DIFFERENT proposer identity
// cannot withdraw someone else's proposal — PROPOSAL_UNAUTHORIZED (provenance
// continuity, design §3.7).
func TestMCPReconciliationWithdrawDifferentProposer(t *testing.T) {
	m, api := newTestMCP(t)
	leftID, rightID := mcpReconciliationPair(t, api)

	response := proposeReconciliationMCP(t, m, leftID, rightID, "mcp-reconciliation-withdraw-2", agentMCPSource("agent-1"))
	var proposed core.ProposeReconciliationResult
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &proposed); err != nil {
		t.Fatalf("decode propose result: %v", err)
	}

	withdraw := call(t, m, 2, "tools/call", map[string]any{
		"name": "accounting_reconciliation_withdraw",
		"arguments": map[string]any{
			"reconciliation_id": proposed.ReconciliationID,
			"request_id":        "mcp-reconciliation-withdraw-2w",
			"source":            agentMCPSource("agent-2"),
		},
	})
	text := toolCallErrorText(t, withdraw)
	if !strings.Contains(text, "PROPOSAL_UNAUTHORIZED") {
		t.Fatalf("error text must carry PROPOSAL_UNAUTHORIZED: %q", text)
	}
}

// TestMCPReconciliationNoLegacyTool: caller-supplied authority has no MCP tool —
// the surface is exactly the four accounting_reconciliation_* tools and there is
// no caller-declared accounting_reconcile legacy tool (absent from the catalog
// and unknown to the dispatcher, -32601).
func TestMCPReconciliationNoLegacyTool(t *testing.T) {
	m, _ := newTestMCP(t)
	for _, tool := range ToolCatalog() {
		if name, _ := tool["name"].(string); name == "accounting_reconcile" {
			t.Fatal("legacy accounting_reconcile must not exist in the catalog")
		}
	}
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_reconcile",
		"arguments": map[string]any{
			"leftId": "a", "rightId": "b", "resolution": "x", "actorId": "maria.torres",
		},
	})
	if response.Error == nil || response.Error.Code != codeMethodNotFound {
		t.Fatalf("accounting_reconcile must be unknown to the dispatcher (-32601), got %+v", response.Error)
	}
}
