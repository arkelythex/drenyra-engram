// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module drives the v0.7.0 EvidenceObject
// MCP tools (accounting_object_store / accounting_object_get, §7 of
// docs/architecture/evidence-object-v0.7.md): the store tool captures ONE
// artifact WORM-style from base64 bytes (duplicate → created=false NO-OP,
// PERIOD_CLOSED inside a closed exact company period, INVALID_OBJECT on
// malformed base64) and the get tool reads SCOPE-FIRST (OBJECT_NOT_FOUND when
// the caller's exact scope differs). Neither tool can approve anything
// (non-authorization boundary).
package server

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// mcpObjectStoreCall invokes accounting_object_store with the given wire args.
func mcpObjectStoreCall(t *testing.T, m *MCPServer, id int, scopeJSON, sourceJSON string, bytes []byte, contentType string) testResponse {
	t.Helper()
	return call(t, m, id, "tools/call", map[string]any{
		"name": "accounting_object_store",
		"arguments": map[string]any{
			"bytesB64":    base64.StdEncoding.EncodeToString(bytes),
			"contentType": contentType,
			"scope":       scopeJSON,
			"source":      sourceJSON,
		},
	})
}

func mcpObjectScopeJSON(scope core.Scope) string {
	encoded, _ := json.Marshal(scope)
	return string(encoded)
}

// TestMCPObjectStoreAndGetRoundTrip verifies the MCP store/get round trip: a
// genuinely new object is created (created=true), the duplicate is a NO-OP and
// the scope-first get returns the metadata plus the artifact bytes.
func TestMCPObjectStoreAndGetRoundTrip(t *testing.T) {
	m, _ := newTestMCP(t)
	scopeJSON := mcpObjectScopeJSON(testScope(testRucA))
	sourceJSON := `{"system":"mcp-test","actorId":"agent-1","actorKind":"agent"}`

	first := mcpObjectStoreCall(t, m, 1, scopeJSON, sourceJSON, []byte("test"), "application/xml")
	if first.Error != nil {
		t.Fatalf("store error: %+v", first.Error)
	}
	var stored core.ObjectStoreResult
	if err := json.Unmarshal([]byte(toolResultText(t, first)), &stored); err != nil {
		t.Fatalf("decode store result: %v", err)
	}
	if !stored.Created {
		t.Fatal("first store must report created=true")
	}

	second := mcpObjectStoreCall(t, m, 2, scopeJSON, sourceJSON, []byte("test"), "application/xml")
	var dup core.ObjectStoreResult
	if err := json.Unmarshal([]byte(toolResultText(t, second)), &dup); err != nil {
		t.Fatalf("decode duplicate result: %v", err)
	}
	if dup.Created || dup.Object.ObjectID != stored.Object.ObjectID {
		t.Fatalf("duplicate = %+v, want created=false with the same content address", dup)
	}

	get := call(t, m, 3, "tools/call", map[string]any{
		"name": "accounting_object_get",
		"arguments": map[string]any{
			"objectId": stored.Object.ObjectID,
			"scope":    scopeJSON,
		},
	})
	if get.Error != nil {
		t.Fatalf("get error: %+v", get.Error)
	}
	var got struct {
		Object   core.EvidenceObject `json:"object"`
		BytesB64 string              `json:"bytesB64"`
	}
	if err := json.Unmarshal([]byte(toolResultText(t, get)), &got); err != nil {
		t.Fatalf("decode get result: %v", err)
	}
	if got.BytesB64 != base64.StdEncoding.EncodeToString([]byte("test")) {
		t.Fatalf("get bytes = %q, want the artifact bytes", got.BytesB64)
	}
}

// TestMCPObjectGetScopeFirst verifies cross-tenant invisibility at the MCP
// surface: the object exists but a different exact scope reads OBJECT_NOT_FOUND.
func TestMCPObjectGetScopeFirst(t *testing.T) {
	m, api := newTestMCP(t)
	scope := testScope(testRucA)
	_, err := api.StoreObject(t.Context(), core.ObjectStoreInput{
		Bytes:       []byte("test"),
		ContentType: "application/xml",
		Scope:       scope,
		Source:      core.Source{System: "mcp-test", ActorID: "agent-1", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		t.Fatalf("seed object: %v", err)
	}
	objectID := core.ComputeObjectID([]byte("test"))

	otherRuc := testScope(testRucB)
	otherRucJSON := mcpObjectScopeJSON(otherRuc)
	get := call(t, m, 4, "tools/call", map[string]any{
		"name": "accounting_object_get",
		"arguments": map[string]any{
			"objectId": objectID,
			"scope":    otherRucJSON,
		},
	})
	if text := toolResultText(t, get); !strings.Contains(text, "OBJECT_NOT_FOUND") {
		t.Fatalf("cross-RUC get = %s, want OBJECT_NOT_FOUND", text)
	}
}

// TestMCPObjectStoreRejectsMalformedBase64 pins the fail-closed wire
// validation: corrupt base64 never reaches the store.
func TestMCPObjectStoreRejectsMalformedBase64(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 5, "tools/call", map[string]any{
		"name": "accounting_object_store",
		"arguments": map[string]any{
			"bytesB64": "%%%not-base64%%%",
			"scope":    mcpObjectScopeJSON(testScope(testRucA)),
			"source":   `{"system":"mcp-test","actorId":"agent-1","actorKind":"agent"}`,
		},
	})
	if text := toolResultText(t, response); !strings.Contains(text, "INVALID_OBJECT") {
		t.Fatalf("malformed base64 = %s, want INVALID_OBJECT", text)
	}
}
