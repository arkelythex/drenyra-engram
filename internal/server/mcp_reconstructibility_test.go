// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; the reconstructibility percentage is integer
// math. This module tests the G-10 MCP adapter surface (design D-1/D-4): the
// accounting_reconstructibility tool takes ONE required strict-decoded exact
// company scope object, unknown arguments fail closed (-32602), the scope object
// itself rejects unknown fields, domain failures open with the stable code, and
// a valid call returns the frozen ReconstructibilityResult JSON — the same
// read-only observation every transport shares (the catalog wording never
// authorizes or approves anything).
package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

func TestMCPReconstructibilityValidCall(t *testing.T) {
	m, api := newTestMCP(t)
	scope := reconstructCompanyA()
	ids := []string{
		seedReconstructibilityDecision(t, api, "decision/alpha", scope),
		seedReconstructibilityDecision(t, api, "decision/beta", scope),
	}

	response := call(t, m, 1, "tools/call", map[string]any{
		"name":      "accounting_reconstructibility",
		"arguments": map[string]any{"scope": reconstructibilityScopeJSON(scope)},
	})
	if response.Error != nil {
		t.Fatalf("tools/call error: %+v", response.Error)
	}
	raw := toolResultText(t, response)

	var got ReconstructibilityResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode tool result: %v\n%s", err, raw)
	}
	want := expectedReconstructibilityResult(scope, ids)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MCP result =\n%+v\nwant\n%+v", got, want)
	}
	// The tool text payload is the frozen canonical JSON bytes (D-4) — exactly
	// json.Marshal of the engine result, never a re-marshal or re-shape.
	if raw != string(mustJSON(want)) {
		t.Fatalf("MCP bytes differ:\n got %s\nwant %s", raw, string(mustJSON(want)))
	}
}

func TestMCPReconstructibilityUnknownArgumentsFailClosed(t *testing.T) {
	m, api := newTestMCP(t)
	scope := reconstructCompanyA()
	seedReconstructibilityDecision(t, api, "decision/alpha", scope)

	// An extra top-level argument (e.g. a caller-declared principal) must be
	// REJECTED, never silently ignored (strict decode, design D-1).
	response := call(t, m, 2, "tools/call", map[string]any{
		"name": "accounting_reconstructibility",
		"arguments": map[string]any{
			"scope":     reconstructibilityScopeJSON(scope),
			"principal": "declared-by-caller",
		},
	})
	if response.Error == nil || response.Error.Code != codeInvalidParams {
		t.Fatalf("want -32602 for an unknown argument, got %+v", response.Error)
	}
}

func TestMCPReconstructibilityScopeObjectFailsClosed(t *testing.T) {
	m, _ := newTestMCP(t)

	// Unknown field INSIDE the scope object is ambiguous — fail closed with the
	// stable code in the domain text (the JSON-RPC transport stays successful,
	// the same domain-failure convention as every other scope decode error).
	withExtra := reconstructibilityScopeJSON(reconstructCompanyA())
	withExtra = strings.TrimSuffix(withExtra, "}")
	withExtra += `,"extra":"ignored-me"}`
	response := call(t, m, 3, "tools/call", map[string]any{
		"name":      "accounting_reconstructibility",
		"arguments": map[string]any{"scope": withExtra},
	})
	if response.Error != nil {
		t.Fatalf("a scope-decode failure is domain text, not a JSON-RPC error: %+v", response.Error)
	}
	if raw := toolResultText(t, response); !strings.HasPrefix(raw, "INVALID_RECONSTRUCTIBILITY_SCOPE") {
		t.Fatalf("error text %q must begin with the stable code", raw)
	}

	// Missing required fields fail closed with the stable code in the text.
	tests := []struct {
		name  string
		scope core.Scope
		code  string
	}{
		{"institutional scope", core.Scope{Kind: core.ScopeKindInstitutional}, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"missing companyId", core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-acme", RUC: "20100039201", Period: "202401"}, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"missing period", core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-acme", CompanyID: "co_a", RUC: "20100039201"}, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"malformed ruc", core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-acme", CompanyID: "co_a", RUC: "123", Period: "202401"}, "INVALID_RECONSTRUCTIBILITY_SCOPE"},
		{"malformed period", core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-acme", CompanyID: "co_a", RUC: "20100039201", Period: "202413"}, "INVALID_PERIOD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := call(t, m, 4, "tools/call", map[string]any{
				"name":      "accounting_reconstructibility",
				"arguments": map[string]any{"scope": reconstructibilityScopeJSON(tt.scope)},
			})
			if response.Error != nil {
				t.Fatalf("a domain failure must keep the JSON-RPC transport successful, got %+v", response.Error)
			}
			raw := toolResultText(t, response)
			if !strings.HasPrefix(raw, tt.code) {
				t.Fatalf("error text %q must begin with the stable code %q", raw, tt.code)
			}
		})
	}
}

func TestMCPReconstructibilityScopeIsolation(t *testing.T) {
	m, api := newTestMCP(t)
	scopeA := reconstructCompanyA()
	idsA := []string{
		seedReconstructibilityDecision(t, api, "decision/alpha", scopeA),
		seedReconstructibilityDecision(t, api, "decision/beta", scopeA),
	}
	scopeB := reconstructCompanyB()

	// Company B's call sees ONLY its own zero — A's heads are structurally
	// invisible (the exact scope is part of the tool arguments; cross-tenant
	// aggregation is forbidden, IR-2).
	responseB := call(t, m, 5, "tools/call", map[string]any{
		"name":      "accounting_reconstructibility",
		"arguments": map[string]any{"scope": reconstructibilityScopeJSON(scopeB)},
	})
	if responseB.Error != nil {
		t.Fatalf("B call error: %+v", responseB.Error)
	}
	rawB := toolResultText(t, responseB)
	var gotB ReconstructibilityResult
	if err := json.Unmarshal([]byte(rawB), &gotB); err != nil {
		t.Fatalf("decode B result: %v", err)
	}
	if !gotB.ZeroDenominator || gotB.Denominator != 0 {
		t.Fatalf("B result = %+v — must be a zero denominator, never A's aggregate", gotB)
	}
	if strings.Contains(rawB, idsA[0]) || strings.Contains(rawB, idsA[1]) {
		t.Fatalf("B response leaks A's decision ids: %s", rawB)
	}

	// A's own call still sees its aggregate.
	responseA := call(t, m, 6, "tools/call", map[string]any{
		"name":      "accounting_reconstructibility",
		"arguments": map[string]any{"scope": reconstructibilityScopeJSON(scopeA)},
	})
	var gotA ReconstructibilityResult
	if err := json.Unmarshal([]byte(toolResultText(t, responseA)), &gotA); err != nil {
		t.Fatalf("decode A result: %v", err)
	}
	if gotA.Denominator != 2 || gotA.Numerator != 0 {
		t.Fatalf("A result = %d/%d, want 0/2", gotA.Numerator, gotA.Denominator)
	}
}

func TestMCPReconstructibilityCatalogReadOnlyWording(t *testing.T) {
	// The catalog description must declare the surface a read-only observation
	// that never authorizes or approves (task 4.9 wording).
	var found *map[string]any
	for _, tool := range ToolCatalog() {
		name, _ := tool["name"].(string)
		if name == "accounting_reconstructibility" {
			found = &tool
			break
		}
	}
	if found == nil {
		t.Fatal("accounting_reconstructibility missing from the tool catalog")
	}
	description, _ := (*found)["description"].(string)
	lower := strings.ToLower(description)
	for _, phrase := range []string{"read-only", "observation", "never authorizes", "only reports"} {
		if !strings.Contains(lower, phrase) {
			t.Fatalf("catalog description must say %q — got %q", phrase, description)
		}
	}
	// The description must carry the negation, never a positive authority claim.
	for _, positive := range []string{"authorizes the", "approves the", "posts", "files", "reopens"} {
		if strings.Contains(lower, positive) {
			t.Fatalf("catalog description must not claim authority (%q): %q", positive, description)
		}
	}
}
