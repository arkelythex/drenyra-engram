// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the MCP adjudication
// surface (v0.4.0 Step 2 — design §7): accounting_judgment_propose and
// accounting_judgment_withdraw carry a provenance-only source (agent|system,
// NEVER authority); accounting_judgment_confirm and accounting_judgment_reject
// accept NO identity arguments and fail closed with AUTHENTICATION_REQUIRED on
// this session-less stdio server; ALL four decode strictly (any unknown field —
// including any caller-supplied authority — is -32602, never ignored).
package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// mcpJudgmentPair seeds the two same-company observations a proposal needs and
// returns their ids (the MCP test scope: org-acme / acme / testRucA / 202401).
func mcpJudgmentPair(t *testing.T, api *API) (fromID, toID string) {
	t.Helper()
	scope := testScope(testRucA)
	from := saveOne(t, api, validInput("judgment/mcp/from", "Saldo de mayor 4011",
		"Saldo de mayor S/ 10,000.00", scope))
	to := saveOne(t, api, validInput("judgment/mcp/to", "Saldo de SIRE 4011",
		"Saldo de SIRE S/ 11,284.30", scope))
	return from.Identity.ID, to.Identity.ID
}

// agentMCPSource is the canonical provenance-only source of the MCP judgment
// tests (snake_case wire shape of the source object).
func agentMCPSource(actor string) map[string]any {
	return map[string]any{"system": "mcp", "actor_id": actor, "actor_kind": "agent", "session": "sess-1"}
}

// proposeJudgmentMCP proposes over the pair with the given provenance source
// and idempotency key.
func proposeJudgmentMCP(t *testing.T, m *MCPServer, fromID, toID, key string, source map[string]any) testResponse {
	t.Helper()
	return call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_judgment_propose",
		"arguments": map[string]any{
			"from_id":    fromID,
			"to_id":      toID,
			"relation":   "contradicts",
			"reason":     "diferencia de saldo entre mayor y SIRE",
			"request_id": key,
			"source":     source,
		},
	})
}

// toolCallErrorText asserts the response is an in-band FAILED tool result
// (isError=true) and returns its text.
func toolCallErrorText(t *testing.T, response testResponse) string {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("domain failure must be a tool result, not a JSON-RPC error: %+v", response.Error)
	}
	var output toolCallOutput
	if err := json.Unmarshal(response.Result, &output); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if !output.IsError {
		t.Fatal("isError = false, want true (fail closed)")
	}
	if len(output.Content) == 0 {
		t.Fatal("failed tool result has no content")
	}
	return output.Content[0]["text"]
}

// TestMCPJudgmentProposeHappyPath: an agent source proposes over two existing
// observations → the proposed judgment arrives as the in-band tool result; the
// provenance is preserved and the tenant/company are derived from the
// observations, never from caller claims.
func TestMCPJudgmentProposeHappyPath(t *testing.T) {
	m, api := newTestMCP(t)
	fromID, toID := mcpJudgmentPair(t, api)

	response := proposeJudgmentMCP(t, m, fromID, toID, "mcp-judgment-propose-1", agentMCPSource("agent-1"))
	if response.Error != nil {
		t.Fatalf("propose error: %+v", response.Error)
	}
	var result core.ProposeJudgmentResult
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &result); err != nil {
		t.Fatalf("decode propose result: %v", err)
	}
	if result.JudgmentID == "" || result.JudgmentID != result.Judgment.ID {
		t.Errorf("judgmentId = %q, want the judgment id %q", result.JudgmentID, result.Judgment.ID)
	}
	if result.IdempotentReplay {
		t.Error("fresh proposal must not be a replay")
	}
	j := result.Judgment
	if j.Status != core.JudgmentProposed {
		t.Errorf("status = %q, want proposed", j.Status)
	}
	if j.FromID != fromID || j.ToID != toID || j.Relation != core.RelationContradicts {
		t.Errorf("pair = %s/%s %s, want %s/%s contradicts", j.FromID, j.ToID, j.Relation, fromID, toID)
	}
	if j.TenantID != testOrgID || j.CompanyID != "acme" {
		t.Errorf("scope = %s/%s, want %s/acme (derived from the observations)", j.TenantID, j.CompanyID, testOrgID)
	}
	wantProposer := core.Source{System: "mcp", ActorID: "agent-1", ActorKind: core.ActorKindAgent, Session: "sess-1"}
	if j.Proposer != wantProposer {
		t.Errorf("proposer = %+v, want %+v (provenance preserved, never authority)", j.Proposer, wantProposer)
	}
	if j.ProposalReason != "diferencia de saldo entre mayor y SIRE" || j.Resolution != "" {
		t.Errorf("reason/resolution = %q/%q, want reason/empty", j.ProposalReason, j.Resolution)
	}
	if j.DecidedAt != "" {
		t.Errorf("decidedAt = %q, want empty for an open proposal", j.DecidedAt)
	}
}

// TestMCPJudgmentProposeHumanSourceDenied: a human source is provenance-only
// and cannot propose — PROPOSAL_UNAUTHORIZED as an in-band tool result.
func TestMCPJudgmentProposeHumanSourceDenied(t *testing.T) {
	m, api := newTestMCP(t)
	fromID, toID := mcpJudgmentPair(t, api)
	human := map[string]any{"system": "mcp", "actor_id": "maria.torres", "actor_kind": "human"}

	response := proposeJudgmentMCP(t, m, fromID, toID, "mcp-judgment-human-1", human)
	text := toolCallErrorText(t, response)
	if !strings.Contains(text, "PROPOSAL_UNAUTHORIZED") {
		t.Fatalf("error text must carry PROPOSAL_UNAUTHORIZED: %q", text)
	}
}

// TestMCPJudgmentProposeRejectsUnknownFields: accounting_judgment_propose
// parses its args STRICTLY (design §6): ANY unknown field — including any
// caller-supplied authority (actorId/subjectId/roles) at top level or inside
// the source object — is a malformed argument shape (JSON-RPC -32602), never
// silently ignored.
func TestMCPJudgmentProposeRejectsUnknownFields(t *testing.T) {
	m, api := newTestMCP(t)
	fromID, toID := mcpJudgmentPair(t, api)
	base := map[string]any{
		"from_id": fromID, "to_id": toID, "relation": "supports",
		"reason": "r", "request_id": "q-1", "source": agentMCPSource("agent-1"),
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
				"name": "accounting_judgment_propose", "arguments": args,
			})
			if response.Error == nil || response.Error.Code != codeInvalidParams {
				t.Fatalf("extra args %v: want JSON-RPC -32602, got %+v", tc.extras, response.Error)
			}
		})
	}
}

// TestMCPJudgmentConfirmFailsClosedWithoutSession: accounting_judgment_confirm
// accepts exactly its four command arguments but the stdio MCP server has NO
// authenticated session binding (design §3), so the tool FAILS CLOSED with the
// frozen AUTHENTICATION_REQUIRED as an in-band tool result — never a JSON-RPC
// error, never a silent identity from the arguments.
func TestMCPJudgmentConfirmFailsClosedWithoutSession(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_judgment_confirm",
		"arguments": map[string]any{
			"judgment_id":            "j-1",
			"resolution":             "resolución profesional",
			"expected_judgment_hash": "abc",
			"request_id":             "req-1",
		},
	})
	text := toolCallErrorText(t, response)
	if !strings.Contains(text, "AUTHENTICATION_REQUIRED") {
		t.Fatalf("error text must carry AUTHENTICATION_REQUIRED: %q", text)
	}
}

// TestMCPJudgmentConfirmRejectsUnknownFields: authority fields are rejected as
// a malformed argument shape (-32602) even on the fail-closed confirm tool.
func TestMCPJudgmentConfirmRejectsUnknownFields(t *testing.T) {
	m, _ := newTestMCP(t)
	base := map[string]any{
		"judgment_id": "j-1", "resolution": "r", "expected_judgment_hash": "abc", "request_id": "q-1",
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
				"name": "accounting_judgment_confirm", "arguments": args,
			})
			if response.Error == nil || response.Error.Code != codeInvalidParams {
				t.Fatalf("extra args %v: want JSON-RPC -32602, got %+v", tc.extras, response.Error)
			}
		})
	}
}

// TestMCPJudgmentRejectFailsClosedWithoutSession: same fail-closed contract for
// accounting_judgment_reject.
func TestMCPJudgmentRejectFailsClosedWithoutSession(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_judgment_reject",
		"arguments": map[string]any{
			"judgment_id":            "j-1",
			"reason":                 "evidencia insuficiente",
			"expected_judgment_hash": "abc",
			"request_id":             "req-1",
		},
	})
	text := toolCallErrorText(t, response)
	if !strings.Contains(text, "AUTHENTICATION_REQUIRED") {
		t.Fatalf("error text must carry AUTHENTICATION_REQUIRED: %q", text)
	}
}

// TestMCPJudgmentProposeIdempotentReplay (AC-L-2, FR-L.3): the SAME tool call
// issued twice with the same (request_id, payload, provenance source) — the
// second result carries the FIRST stored judgment id and the replay marker
// (idempotentReplay=true), and the store keeps exactly ONE proposal. The
// fresh-only proposal tests above remain but no longer stand alone.
func TestMCPJudgmentProposeIdempotentReplay(t *testing.T) {
	m, api := newTestMCP(t)
	fromID, toID := mcpJudgmentPair(t, api)

	firstResp := proposeJudgmentMCP(t, m, fromID, toID, "mcp-judgment-replay-1", agentMCPSource("agent-1"))
	if firstResp.Error != nil {
		t.Fatalf("first propose error: %+v", firstResp.Error)
	}
	var first core.ProposeJudgmentResult
	if err := json.Unmarshal([]byte(toolResultText(t, firstResp)), &first); err != nil {
		t.Fatalf("decode first propose result: %v", err)
	}
	if first.IdempotentReplay || first.JudgmentID == "" {
		t.Fatalf("first propose = %+v, want a fresh stored judgment id", first)
	}

	// The exact same tool call again: the second result must carry the FIRST
	// stored judgment id and the replay marker — never a second proposal.
	secondResp := proposeJudgmentMCP(t, m, fromID, toID, "mcp-judgment-replay-1", agentMCPSource("agent-1"))
	if secondResp.Error != nil {
		t.Fatalf("replay propose error: %+v", secondResp.Error)
	}
	var second core.ProposeJudgmentResult
	if err := json.Unmarshal([]byte(toolResultText(t, secondResp)), &second); err != nil {
		t.Fatalf("decode replay propose result: %v", err)
	}
	if !second.IdempotentReplay {
		t.Fatalf("second propose must be an idempotent replay, got %+v", second)
	}
	if second.JudgmentID != first.JudgmentID || second.Judgment.ID != first.JudgmentID {
		t.Fatalf("replay judgment id = %q, want the first stored id %q", second.JudgmentID, first.JudgmentID)
	}
	if second.Judgment.Status != core.JudgmentProposed {
		t.Fatalf("replay status = %q, want proposed (the stored outcome)", second.Judgment.Status)
	}
}

// TestMCPJudgmentProposeConflictOnChangedPayload (FR-L.2, AC-L-6): reusing the
// same MCP request id with a DIFFERENT bound payload is the frozen typed
// conflict (IDEMPOTENCY_CONFLICT as an in-band tool result), never a silent
// success and never a second proposal.
func TestMCPJudgmentProposeConflictOnChangedPayload(t *testing.T) {
	m, api := newTestMCP(t)
	fromID, toID := mcpJudgmentPair(t, api)

	resp := proposeJudgmentMCP(t, m, fromID, toID, "mcp-judgment-conflict-1", agentMCPSource("agent-1"))
	if resp.Error != nil {
		t.Fatalf("first propose error: %+v", resp.Error)
	}
	var first core.ProposeJudgmentResult
	if err := json.Unmarshal([]byte(toolResultText(t, resp)), &first); err != nil {
		t.Fatalf("decode first propose result: %v", err)
	}

	conflict := call(t, m, 3, "tools/call", map[string]any{
		"name": "accounting_judgment_propose",
		"arguments": map[string]any{
			"from_id":    fromID,
			"to_id":      toID,
			"relation":   "contradicts",
			"reason":     "CHANGED reason under the same request id",
			"request_id": "mcp-judgment-conflict-1",
			"source":     agentMCPSource("agent-1"),
		},
	})
	text := toolCallErrorText(t, conflict)
	if !strings.Contains(text, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("changed payload under the same request id: error text must carry IDEMPOTENCY_CONFLICT, got %q", text)
	}
}

// TestMCPJudgmentWithdrawIdempotentReplay: the same withdraw tool call twice
// with the same request id — the second result replays the stored withdrawn
// outcome (same event id) with no second event.
func TestMCPJudgmentWithdrawIdempotentReplay(t *testing.T) {
	m, api := newTestMCP(t)
	fromID, toID := mcpJudgmentPair(t, api)

	resp := proposeJudgmentMCP(t, m, fromID, toID, "mcp-judgment-w-replay-1", agentMCPSource("agent-1"))
	var proposed core.ProposeJudgmentResult
	if err := json.Unmarshal([]byte(toolResultText(t, resp)), &proposed); err != nil {
		t.Fatalf("decode propose result: %v", err)
	}

	withdrawArgs := map[string]any{
		"judgment_id": proposed.JudgmentID,
		"request_id":  "mcp-judgment-w-replay-1w",
		"source":      agentMCPSource("agent-1"),
	}
	first := call(t, m, 1, "tools/call", map[string]any{"name": "accounting_judgment_withdraw", "arguments": withdrawArgs})
	var firstResult core.WithdrawJudgmentResult
	if err := json.Unmarshal([]byte(toolResultText(t, first)), &firstResult); err != nil {
		t.Fatalf("decode first withdraw result: %v", err)
	}
	if firstResult.IdempotentReplay || firstResult.JudgmentEventID == "" {
		t.Fatalf("first withdraw = %+v, want a fresh stored event", firstResult)
	}

	second := call(t, m, 2, "tools/call", map[string]any{"name": "accounting_judgment_withdraw", "arguments": withdrawArgs})
	var secondResult core.WithdrawJudgmentResult
	if err := json.Unmarshal([]byte(toolResultText(t, second)), &secondResult); err != nil {
		t.Fatalf("decode replay withdraw result: %v", err)
	}
	if !secondResult.IdempotentReplay || secondResult.JudgmentEventID != firstResult.JudgmentEventID {
		t.Fatalf("replay withdraw = %+v, want the stored event %s with idempotentReplay", secondResult, firstResult.JudgmentEventID)
	}
	if secondResult.Judgment.Status != core.JudgmentWithdrawn {
		t.Fatalf("replay status = %q, want withdrawn (stored outcome)", secondResult.Judgment.Status)
	}
}

func TestMCPJudgmentWithdrawSameProposer(t *testing.T) {
	m, api := newTestMCP(t)
	fromID, toID := mcpJudgmentPair(t, api)

	response := proposeJudgmentMCP(t, m, fromID, toID, "mcp-judgment-withdraw-1", agentMCPSource("agent-1"))
	var proposed core.ProposeJudgmentResult
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &proposed); err != nil {
		t.Fatalf("decode propose result: %v", err)
	}

	withdraw := call(t, m, 2, "tools/call", map[string]any{
		"name": "accounting_judgment_withdraw",
		"arguments": map[string]any{
			"judgment_id": proposed.JudgmentID,
			"request_id":  "mcp-judgment-withdraw-1w",
			"source":      agentMCPSource("agent-1"),
		},
	})
	if withdraw.Error != nil {
		t.Fatalf("withdraw error: %+v", withdraw.Error)
	}
	var result core.WithdrawJudgmentResult
	if err := json.Unmarshal([]byte(toolResultText(t, withdraw)), &result); err != nil {
		t.Fatalf("decode withdraw result: %v", err)
	}
	if result.Judgment.Status != core.JudgmentWithdrawn {
		t.Errorf("status = %q, want withdrawn (terminal)", result.Judgment.Status)
	}
	if result.JudgmentEventID == "" {
		t.Error("judgmentEventId must be set on withdrawal")
	}
}

// TestMCPJudgmentWithdrawDifferentProposer: a DIFFERENT proposer identity
// cannot withdraw someone else's proposal — PROPOSAL_UNAUTHORIZED (provenance
// continuity, design §3.7).
func TestMCPJudgmentWithdrawDifferentProposer(t *testing.T) {
	m, api := newTestMCP(t)
	fromID, toID := mcpJudgmentPair(t, api)

	response := proposeJudgmentMCP(t, m, fromID, toID, "mcp-judgment-withdraw-2", agentMCPSource("agent-1"))
	var proposed core.ProposeJudgmentResult
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &proposed); err != nil {
		t.Fatalf("decode propose result: %v", err)
	}

	withdraw := call(t, m, 2, "tools/call", map[string]any{
		"name": "accounting_judgment_withdraw",
		"arguments": map[string]any{
			"judgment_id": proposed.JudgmentID,
			"request_id":  "mcp-judgment-withdraw-2w",
			"source":      agentMCPSource("agent-2"),
		},
	})
	text := toolCallErrorText(t, withdraw)
	if !strings.Contains(text, "PROPOSAL_UNAUTHORIZED") {
		t.Fatalf("error text must carry PROPOSAL_UNAUTHORIZED: %q", text)
	}
}
