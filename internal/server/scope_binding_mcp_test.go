// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the MCP identity→scope binding
// test surface (scope-param-rollout FR-SPR-2, AC-SPR-2, D-SPR-3/D-SPR-5): it
// proves that a company-kind tool call whose effective scope lies OUTSIDE the
// bound principal's membership fails closed with the frozen typed denial BEFORE
// dispatch, that an exact-membership scope proceeds unchanged, and that the
// unbound (shared-token / stdio reference) path is byte-for-byte pre-change
// behavior (FD-SPR-3). The decodeScope path (accounting_* string scopes) and the
// core.Scope-object path (engram_* tools) are both covered. No monetary field
// exists anywhere in this file (IR-1).
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// callAs dispatches one message through the MCP server WITH a request context —
// the HTTP /mcp identity context (scope-param-rollout FR-SPR-2). A context
// without a principal exercises the unbound reference mode (FD-SPR-3).
func callAs(t *testing.T, m *MCPServer, ctx context.Context, id int, method string, params any) testResponse {
	t.Helper()
	request, err := json.Marshal(mcpRequest(id, method, params))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	raw := m.HandleMessageContext(ctx, request)
	if raw == nil {
		t.Fatalf("method %s returned no response", method)
	}
	var response testResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	return response
}

// scopeArg renders a company scope as the tool-argument JSON object (the shape
// the engram_* core.Scope tools decode).
func scopeArg(s core.Scope) map[string]any {
	return map[string]any{
		"kind":           string(s.Kind),
		"organizationId": s.OrganizationID,
		"companyId":      s.CompanyID,
		"ruc":            s.RUC,
		"period":         s.Period,
	}
}

// TestMCPIdentityScopeBinding — AC-SPR-2/FR-SPR-2: table-driven over the MCP
// server with a bound tenant-B principal in the request context. Denial rows
// assert the frozen typed codes (TENANT_SCOPE_MISMATCH / COMPANY_SCOPE_DENIED)
// and that no API effect happens; match rows assert unchanged results; the
// unbound row asserts pre-change reference behavior (FD-SPR-3).
func TestMCPIdentityScopeBinding(t *testing.T) {
	f := newCrossTenantFixture(t)
	seedTenantAMemory(t, f, "tenant/a")
	saveOne(t, f.api, core.SaveInput{
		TopicKey:     "tenant/b",
		Title:        "tenant B memory",
		Kind:         core.KindFact,
		Scope:        f.scopeB,
		Content:      core.Content{What: "tenant-b-only content", Why: "fixture", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  "2024-02-15T00:00:00Z",
		Source:       testAgentSource,
	})
	m := NewMCPServer(f.api)
	boundCtx := WithPrincipal(context.Background(), f.principalB)

	t.Run("bound principal denied foreign tenant", func(t *testing.T) {
		response := callAs(t, m, boundCtx, 1, "tools/call", map[string]any{
			"name":      "engram_get_by_topic",
			"arguments": map[string]any{"topicKey": "tenant/a", "scope": scopeArg(f.scopeA)},
		})
		text := toolResultText(t, response)
		if !strings.Contains(text, "TENANT_SCOPE_MISMATCH") {
			t.Fatalf("bound B + scope A = %q, want TENANT_SCOPE_MISMATCH denial", text)
		}
	})

	t.Run("bound principal denied foreign company", func(t *testing.T) {
		foreign := f.scopeB
		foreign.CompanyID = "co_foreign"
		response := callAs(t, m, boundCtx, 2, "tools/call", map[string]any{
			"name":      "engram_get_by_topic",
			"arguments": map[string]any{"topicKey": "tenant/b", "scope": scopeArg(foreign)},
		})
		text := toolResultText(t, response)
		if !strings.Contains(text, "COMPANY_SCOPE_DENIED") {
			t.Fatalf("bound B + foreign company = %q, want COMPANY_SCOPE_DENIED denial", text)
		}
	})

	t.Run("bound principal exact membership unchanged", func(t *testing.T) {
		response := callAs(t, m, boundCtx, 3, "tools/call", map[string]any{
			"name":      "engram_get_by_topic",
			"arguments": map[string]any{"topicKey": "tenant/b", "scope": scopeArg(f.scopeB)},
		})
		text := toolResultText(t, response)
		if strings.Contains(text, "SCOPE_DENIED") || strings.Contains(text, "SCOPE_MISMATCH") {
			t.Fatalf("bound B + scope B denied: %q", text)
		}
		if !strings.Contains(text, "tenant/b") {
			t.Fatalf("bound B + scope B = %q, want the tenant-B memory", text)
		}
	})

	t.Run("unbound reference mode unchanged", func(t *testing.T) {
		response := call(t, m, 4, "tools/call", map[string]any{
			"name":      "engram_get_by_topic",
			"arguments": map[string]any{"topicKey": "tenant/a", "scope": scopeArg(f.scopeA)},
		})
		text := toolResultText(t, response)
		if strings.Contains(text, "SCOPE_DENIED") || strings.Contains(text, "SCOPE_MISMATCH") {
			t.Fatalf("unbound + scope A denied: %q", text)
		}
		if !strings.Contains(text, "tenant/a") {
			t.Fatalf("unbound + scope A = %q, want the tenant-A memory", text)
		}
	})

	t.Run("object-scope tool denied", func(t *testing.T) {
		response := callAs(t, m, boundCtx, 5, "tools/call", map[string]any{
			"name":      "engram_search",
			"arguments": map[string]any{"query": "tenant", "scope": scopeArg(f.scopeA)},
		})
		text := toolResultText(t, response)
		if !strings.Contains(text, "TENANT_SCOPE_MISMATCH") {
			t.Fatalf("bound B + search scope A = %q, want TENANT_SCOPE_MISMATCH denial", text)
		}
	})

	t.Run("string-scope tool denied via decodeScope", func(t *testing.T) {
		rawScopeA, err := json.Marshal(f.scopeA)
		if err != nil {
			t.Fatalf("marshal scope A: %v", err)
		}
		response := callAs(t, m, boundCtx, 6, "tools/call", map[string]any{
			"name":      "accounting_search",
			"arguments": map[string]any{"query": "tenant", "scope": string(rawScopeA)},
		})
		text := toolResultText(t, response)
		if !strings.Contains(text, "TENANT_SCOPE_MISMATCH") {
			t.Fatalf("bound B + search scope A = %q, want TENANT_SCOPE_MISMATCH denial", text)
		}
	})

	t.Run("string-scope tool exact membership unchanged", func(t *testing.T) {
		rawScopeB, err := json.Marshal(f.scopeB)
		if err != nil {
			t.Fatalf("marshal scope B: %v", err)
		}
		response := callAs(t, m, boundCtx, 7, "tools/call", map[string]any{
			"name":      "accounting_search",
			"arguments": map[string]any{"query": "tenant", "scope": string(rawScopeB)},
		})
		text := toolResultText(t, response)
		if strings.Contains(text, "SCOPE_DENIED") || strings.Contains(text, "SCOPE_MISMATCH") {
			t.Fatalf("bound B + search scope B denied: %q", text)
		}
		if !strings.Contains(text, "tenant/b") {
			t.Fatalf("bound B + search scope B = %q, want the tenant-B memory", text)
		}
	})
}

// TestScopeDeniedCrossSurfaceConsistency — AC-SPR-6/NFR-SPR-3: the SAME frozen
// typed code (TENANT_SCOPE_MISMATCH) is emitted by the HTTP surface (403 error
// envelope over the authenticate-mounted rule-impact route) and the MCP surface
// (tool error text over the /mcp identity context) for the same bound principal
// B + foreign scope A pair. The CLI surface asserts the same frozen constants
// (COMPANY_SCOPE_DENIED — its organization id is fixed, so only the company
// leg can fire) in scope_binding_cli_test.go.
func TestScopeDeniedCrossSurfaceConsistency(t *testing.T) {
	f := newCrossTenantFixture(t)
	_ = seedRuleImpact(t, f) // tenant-A rule under scopeA (topic ctTopicKey)

	// HTTP: rule-impact with principal-B session + scope-A query → 403 typed.
	status, raw := approvalHTTP(t, http.MethodGet,
		f.ts.URL+"/accounting/rules/"+url.PathEscape(ctTopicKey)+"/impact?revision=1&"+scopeQuery(f.scopeA),
		f.tokenB, "", nil)
	if status != http.StatusForbidden {
		t.Fatalf("HTTP denial status = %d, want 403; raw=%s", status, raw)
	}
	if !strings.Contains(raw, "TENANT_SCOPE_MISMATCH") {
		t.Fatalf("HTTP denial body = %q, want TENANT_SCOPE_MISMATCH", raw)
	}

	// MCP: bound B + scope A tool call → typed denial text (same frozen code).
	m := NewMCPServer(f.api)
	boundCtx := WithPrincipal(context.Background(), f.principalB)
	response := callAs(t, m, boundCtx, 1, "tools/call", map[string]any{
		"name":      "engram_get_by_topic",
		"arguments": map[string]any{"topicKey": "tenant/a", "scope": scopeArg(f.scopeA)},
	})
	text := toolResultText(t, response)
	if !strings.Contains(text, "TENANT_SCOPE_MISMATCH") {
		t.Fatalf("MCP denial = %q, want TENANT_SCOPE_MISMATCH", text)
	}
}

// TestMCPHTTPIdentityScopeBinding proves the /mcp middleware chain end-to-end:
// a session bearer token resolves a verified principal (authenticate middleware),
// the request context flows into the MCP server (handleMCP), and a company-kind
// tool call outside the membership fails closed with the typed denial. The
// shared-token-only call (no Authorization header) passes through unchanged
// (FD-SPR-3) — the reference-mode read returns the tenant-A memory.
func TestMCPHTTPIdentityScopeBinding(t *testing.T) {
	f := newCrossTenantFixture(t)
	seedTenantAMemory(t, f, "tenant/a")

	mcpBody := func(id int, topicKey string, scope core.Scope) []byte {
		request, err := json.Marshal(mcpRequest(id, "tools/call", map[string]any{
			"name":      "engram_get_by_topic",
			"arguments": map[string]any{"topicKey": topicKey, "scope": scopeArg(scope)},
		}))
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		return request
	}

	t.Run("session principal denied foreign scope over /mcp", func(t *testing.T) {
		status, raw := approvalHTTP(t, http.MethodPost, f.ts.URL+"/mcp", f.tokenB, "",
			json.RawMessage(mcpBody(1, "tenant/a", f.scopeA)))
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (JSON-RPC response over HTTP)", status)
		}
		var response testResponse
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			t.Fatalf("decode /mcp response: %v (%s)", err, raw)
		}
		text := toolResultText(t, response)
		if !strings.Contains(text, "TENANT_SCOPE_MISMATCH") {
			t.Fatalf("HTTP /mcp bound B + scope A = %q, want TENANT_SCOPE_MISMATCH denial", text)
		}
	})

	t.Run("shared-token-only /mcp call unchanged", func(t *testing.T) {
		status, raw := approvalHTTP(t, http.MethodPost, f.ts.URL+"/mcp", "", "",
			json.RawMessage(mcpBody(2, "tenant/a", f.scopeA)))
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		var response testResponse
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			t.Fatalf("decode /mcp response: %v (%s)", err, raw)
		}
		text := toolResultText(t, response)
		if strings.Contains(text, "SCOPE_DENIED") || strings.Contains(text, "SCOPE_MISMATCH") {
			t.Fatalf("shared-token-only /mcp call denied: %q", text)
		}
		if !strings.Contains(text, "tenant/a") {
			t.Fatalf("shared-token-only /mcp call = %q, want the tenant-A memory", text)
		}
	})
}
