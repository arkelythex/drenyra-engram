// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. These tests drive the automatic MCP session
// context (v0.5.0, design §5): DRENYRA_DEFAULT_SCOPE parsing (absent → null
// initialize context with instructions pointing at the tool; valid → the
// CurrentContext carried in _meta.drenyra/currentContext; invalid or
// inaccessible → construction fails closed), the accounting_current_context
// tool (exact scope, strict decode, cross-scope denial) and the replaced stale
// lifecycle instructions. No monetary fields cross the protocol.

package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// mcpDefaultScopeJSON is the demoScope company/period as a raw
// DRENYRA_DEFAULT_SCOPE value.
const mcpDefaultScopeJSON = `{"kind":"company","organizationId":"cmp_org","companyId":"cmp_01","ruc":"20601234567","period":"202607"}`

// seedContextScenario seeds two demo-scope chains: a fact and a pending
// decision (a fiscal-effect write behind the human gate).
func seedContextScenario(t *testing.T, api *API) {
	t.Helper()
	saveInScope(t, api, demoScope(), "fact/igv-tasa", "fact", "Tasa IGV", "tasa vigente", "2026-07-05T00:00:00Z")
	saveInScope(t, api, demoScope(), "decision/pendiente", "decision", "Ajuste pendiente", "ajuste", "2026-07-15T00:00:00Z")
}

func TestMCPInitializeNoDefaultScope(t *testing.T) {
	m, _ := newTestMCP(t) // NewMCPServer — DRENYRA_DEFAULT_SCOPE unset
	response := call(t, m, 1, "initialize", map[string]any{"protocolVersion": "2025-03-26"})
	if response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	var result struct {
		Instructions string                     `json:"instructions"`
		Meta         map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	raw, ok := result.Meta["drenyra/currentContext"]
	if !ok {
		t.Fatal("initialize must carry _meta.drenyra/currentContext")
	}
	if string(raw) != "null" {
		t.Fatalf("_meta.drenyra/currentContext = %s, want null when DRENYRA_DEFAULT_SCOPE is unset", raw)
	}
	if !strings.Contains(result.Instructions, "accounting_current_context") {
		t.Fatalf("instructions must point at accounting_current_context when no default scope is configured: %q", result.Instructions)
	}
}

func TestMCPInitializeWithDefaultScope(t *testing.T) {
	api := newTestAPI(t)
	seedContextScenario(t, api)

	m, err := NewMCPServerWithDefaultScope(api, mcpDefaultScopeJSON)
	if err != nil {
		t.Fatalf("NewMCPServerWithDefaultScope: %v", err)
	}
	response := call(t, m, 1, "initialize", map[string]any{"protocolVersion": "2025-03-26"})
	if response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	var result struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	var ctx core.CurrentContext
	raw := result.Meta["drenyra/currentContext"]
	if len(raw) == 0 {
		t.Fatal("initialize must carry the default scope context")
	}
	if err := json.Unmarshal(raw, &ctx); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	if !core.ScopeEquals(ctx.Scope, demoScope()) {
		t.Fatalf("context scope = %+v, want demoScope", ctx.Scope)
	}
	if ctx.PeriodSummary.Total != 2 || len(ctx.RecentChains) != 2 {
		t.Fatalf("context = %+v / %d chains, want total 2 and 2 recent chains", ctx.PeriodSummary, len(ctx.RecentChains))
	}
	if ctx.GeneratedAt == "" {
		t.Fatal("generatedAt must be stamped")
	}
}

// TestMCPServerDefaultScopeFailClosed proves the construction contract: a
// present but malformed, non-company or unperioded DRENYRA_DEFAULT_SCOPE fails
// construction — the server never starts with partial cross-scope data.
func TestMCPServerDefaultScopeFailClosed(t *testing.T) {
	api := newTestAPI(t)

	t.Run("invalid JSON", func(t *testing.T) {
		if _, err := NewMCPServerWithDefaultScope(api, `{"kind": "company"`); err == nil {
			t.Fatal("malformed DRENYRA_DEFAULT_SCOPE must fail construction")
		}
	})

	t.Run("institutional scope", func(t *testing.T) {
		raw := `{"kind":"institutional","organizationId":"cmp_org"}`
		_, err := NewMCPServerWithDefaultScope(api, raw)
		if err == nil || !strings.Contains(err.Error(), "DRENYRA_DEFAULT_SCOPE") {
			t.Fatalf("institutional default scope = %v, want a DRENYRA_DEFAULT_SCOPE construction failure", err)
		}
	})

	t.Run("unperioded company scope", func(t *testing.T) {
		raw := `{"kind":"company","organizationId":"cmp_org","companyId":"cmp_01","ruc":"20601234567"}`
		if _, err := NewMCPServerWithDefaultScope(api, raw); err == nil {
			t.Fatal("a default scope without a period must fail construction")
		}
	})
}

func TestMCPCurrentContextTool(t *testing.T) {
	api := newTestAPI(t)
	seedContextScenario(t, api)
	m := NewMCPServer(api)

	response := call(t, m, 1, "tools/call", map[string]any{
		"name":      "accounting_current_context",
		"arguments": map[string]any{"scope": mcpDefaultScopeJSON},
	})
	if response.Error != nil {
		t.Fatalf("tool error: %+v", response.Error)
	}
	var ctx core.CurrentContext
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &ctx); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	if !core.ScopeEquals(ctx.Scope, demoScope()) {
		t.Fatalf("scope = %+v, want demoScope", ctx.Scope)
	}
	if ctx.PeriodSummary.Total != 2 || ctx.PeriodSummary.ClosureState != string(core.ClosureStateOpen) {
		t.Fatalf("summary = %+v, want total 2 open", ctx.PeriodSummary)
	}
	// The pending decision appears both in the pending-item digest and as the
	// newest recent chain.
	if len(ctx.PendingItems) != 1 || ctx.PendingItems[0].TopicKey != "decision/pendiente" {
		t.Fatalf("pendingItems = %+v, want the pending decision", ctx.PendingItems)
	}
	if len(ctx.RecentChains) != 2 {
		t.Fatalf("recentChains = %d, want 2", len(ctx.RecentChains))
	}
	if ctx.RecentChains[0].EffectiveAt != "2026-07-15T00:00:00Z" {
		t.Fatalf("newest chain = %q, want the decision (2026-07-15)", ctx.RecentChains[0].EffectiveAt)
	}
}

func TestMCPCurrentContextToolErrors(t *testing.T) {
	m, _ := newTestMCP(t)

	t.Run("missing scope", func(t *testing.T) {
		response := call(t, m, 1, "tools/call", map[string]any{
			"name":      "accounting_current_context",
			"arguments": map[string]any{},
		})
		if response.Error == nil || response.Error.Code != codeInvalidParams {
			t.Fatalf("missing scope: want -32602, got %+v", response.Error)
		}
	})

	t.Run("unknown argument strict decode", func(t *testing.T) {
		response := call(t, m, 2, "tools/call", map[string]any{
			"name":      "accounting_current_context",
			"arguments": map[string]any{"scope": mcpDefaultScopeJSON, "tenantId": "x"},
		})
		if response.Error == nil || !strings.Contains(response.Error.Message, "unknown field") {
			t.Fatalf("strict decode: want unknown-field rejection, got %+v", response.Error)
		}
	})

	t.Run("institutional scope denied", func(t *testing.T) {
		response := call(t, m, 3, "tools/call", map[string]any{
			"name":      "accounting_current_context",
			"arguments": map[string]any{"scope": `{"kind":"institutional","organizationId":"cmp_org"}`},
		})
		text := toolResultText(t, response)
		if !strings.HasPrefix(text, "INVALID_PERIOD") {
			t.Fatalf("institutional scope result = %q, want INVALID_PERIOD (never inferred)", text)
		}
	})
}

// TestMCPInitializeLifecycleInstructions proves the stale v0.3 lifecycle line
// ("Lifecycle: draft → reviewed → promoted → superseded, adjacent-forward
// only") is gone and the current per-memory lifecycle + human approval gate +
// period closure semantics are in place.
func TestMCPInitializeLifecycleInstructions(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "initialize", map[string]any{})
	if response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	var result struct {
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	if strings.Contains(result.Instructions, "adjacent-forward only") {
		t.Fatalf("stale lifecycle text still present: %q", result.Instructions)
	}
	for _, want := range []string{"pending_review", "human approval gate", "closureState", "reopen"} {
		if !strings.Contains(result.Instructions, want) {
			t.Fatalf("instructions must mention %q: %q", want, result.Instructions)
		}
	}
}
