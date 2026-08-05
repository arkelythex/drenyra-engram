// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. These tests drive the MCP accounting_
// compare_periods tool (v0.5.0, design §4/§6): two snake_case exact-scope
// arguments, strict decode (unknown fields rejected), typed domain failures
// (INVALID_PERIOD / COMPANY_SCOPE_DENIED) and the deterministic PeriodComparison
// result. No monetary fields cross the protocol.

package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

const (
	mcpFromScope = `{"kind":"company","organizationId":"cmp_org","companyId":"cmp_01","ruc":"20601234567","period":"202607"}`
	mcpToScope   = `{"kind":"company","organizationId":"cmp_org","companyId":"cmp_01","ruc":"20601234567","period":"202608"}`
)

// seedMCPCompareScenario seeds July (two chains) and August (one changed, one
// new) through the shared API.
func seedMCPCompareScenario(t *testing.T, api *API) {
	t.Helper()
	saveInScope(t, api, demoScope(), "fact/igv-tasa", "fact", "Tasa IGV", "tasa vigente 18%", "2026-07-05T00:00:00Z")
	saveInScope(t, api, demoScope(), "obligation/igv-621", "obligation", "Obligacion PDT 621", "declarar IGV julio", "2026-07-20T00:00:00Z")
	saveInScope(t, api, augustScope(), "fact/igv-tasa", "fact", "Tasa IGV", "tasa vigente 18.5%", "2026-08-05T00:00:00Z")
	saveInScope(t, api, augustScope(), "account/4011/ventas-agosto", "fact", "Ventas agosto", "ventas de agosto", "2026-08-10T00:00:00Z")
}

func TestMCPComparePeriods(t *testing.T) {
	m, api := newTestMCP(t)
	seedMCPCompareScenario(t, api)

	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_compare_periods",
		"arguments": map[string]any{
			"from_scope": mcpFromScope,
			"to_scope":   mcpToScope,
		},
	})
	if response.Error != nil {
		t.Fatalf("compare error: %+v", response.Error)
	}
	var got core.PeriodComparison
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &got); err != nil {
		t.Fatalf("decode comparison: %v", err)
	}
	if got.From != "202607" || got.To != "202608" {
		t.Fatalf("periods = %q/%q, want 202607/202608", got.From, got.To)
	}
	if got.Counts.FromTotal != 2 || got.Counts.ToTotal != 2 {
		t.Fatalf("counts = %+v, want fromTotal 2, toTotal 2", got.Counts)
	}
	if len(got.Chains.New) != 1 || got.Chains.New[0].TopicKey != "account/4011/ventas-agosto" {
		t.Fatalf("new = %+v, want exactly account/4011/ventas-agosto", got.Chains.New)
	}
	if len(got.Chains.Changed) != 1 || got.Chains.Changed[0].TopicKey != "fact/igv-tasa" {
		t.Fatalf("changed = %+v, want exactly fact/igv-tasa", got.Chains.Changed)
	}
}

// TestMCPComparePeriodsStrictDecode proves the strict snake_case decode: the tool
// accepts EXACTLY from_scope and to_scope; any unknown argument is a JSON-RPC
// invalid-params error, never silently ignored.
func TestMCPComparePeriodsStrictDecode(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_compare_periods",
		"arguments": map[string]any{
			"from_scope": mcpFromScope,
			"to_scope":   mcpToScope,
			"scope":      mcpFromScope, // not a declared argument
		},
	})
	if response.Error == nil {
		t.Fatal("an undeclared argument must be rejected by the strict decode")
	}
	if !strings.Contains(response.Error.Message, "unknown field") {
		t.Fatalf("error = %+v, want an unknown-field rejection", response.Error)
	}
}

func TestMCPComparePeriodsTypedFailures(t *testing.T) {
	m, _ := newTestMCP(t)

	t.Run("missing argument", func(t *testing.T) {
		response := call(t, m, 1, "tools/call", map[string]any{
			"name": "accounting_compare_periods",
			"arguments": map[string]any{
				"from_scope": mcpFromScope,
			},
		})
		if response.Error == nil {
			t.Fatal("missing to_scope must be an invalid-params error")
		}
	})

	t.Run("same period", func(t *testing.T) {
		response := call(t, m, 2, "tools/call", map[string]any{
			"name": "accounting_compare_periods",
			"arguments": map[string]any{
				"from_scope": mcpFromScope,
				"to_scope":   mcpFromScope,
			},
		})
		text := toolResultText(t, response)
		if !strings.HasPrefix(text, "INVALID_PERIOD") {
			t.Fatalf("same-period result = %q, want INVALID_PERIOD", text)
		}
	})

	t.Run("cross company", func(t *testing.T) {
		other := `{"kind":"company","organizationId":"cmp_org","companyId":"cmp_02","ruc":"20100039201","period":"202608"}`
		response := call(t, m, 3, "tools/call", map[string]any{
			"name": "accounting_compare_periods",
			"arguments": map[string]any{
				"from_scope": mcpFromScope,
				"to_scope":   other,
			},
		})
		text := toolResultText(t, response)
		if !strings.Contains(text, "COMPANY_SCOPE_DENIED") {
			t.Fatalf("cross-company result = %q, want COMPANY_SCOPE_DENIED", text)
		}
	})
}
