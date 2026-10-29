package server

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestMCPInputSchemaMatchesDecoder pins the tools/list contract: every
// property a tool advertises in its inputSchema must be accepted by that
// tool's argument decoder under the advertised name and JSON type. A client
// that follows the published schema must never get a -32602 shape error:
//
//   - strict decoders reject a misnamed or mistyped property with "unknown
//     field" or "cannot unmarshal";
//   - lenient decoders silently drop a misnamed property, which surfaces as
//     "<field> is required" even though every advertised property was sent.
//
// Domain failures (missing records, absent session, invalid values) are
// expected and ignored here; only a schema and decoder that disagree fail.
func TestMCPInputSchemaMatchesDecoder(t *testing.T) {
	m, _ := newTestMCP(t)

	list := call(t, m, 1, "tools/list", map[string]any{})
	if list.Error != nil {
		t.Fatalf("tools/list: %+v", list.Error)
	}
	var catalog struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Properties map[string]map[string]any `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(list.Result, &catalog); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(catalog.Tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}

	var mismatches []string
	for i, tool := range catalog.Tools {
		names := make([]string, 0, len(tool.InputSchema.Properties))
		for name := range tool.InputSchema.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for j, name := range names {
			args := map[string]any{name: sampleForSchema(tool.InputSchema.Properties[name])}
			response := call(t, m, 1000+i*100+j, "tools/call", map[string]any{
				"name":      tool.Name,
				"arguments": args,
			})
			if response.Error == nil {
				continue
			}
			msg := response.Error.Message
			if strings.Contains(msg, "unknown field") || strings.Contains(msg, "cannot unmarshal") {
				mismatches = append(mismatches, tool.Name+"."+name+": "+msg)
			}
		}

		all := make(map[string]any, len(names))
		for _, name := range names {
			all[name] = sampleForSchema(tool.InputSchema.Properties[name])
		}
		response := call(t, m, 900000+i, "tools/call", map[string]any{
			"name":      tool.Name,
			"arguments": all,
		})
		if response.Error != nil && strings.HasSuffix(response.Error.Message, " is required") {
			mismatches = append(mismatches, tool.Name+" (all advertised properties sent): "+response.Error.Message)
		}
	}
	if len(mismatches) > 0 {
		t.Fatalf("inputSchema disagrees with the argument decoder for %d properties:\n%s",
			len(mismatches), strings.Join(mismatches, "\n"))
	}
}

// sampleForSchema returns a minimal value of the JSON type a property schema
// declares, so the decoder sees the advertised shape.
func sampleForSchema(schema map[string]any) any {
	switch schema["type"] {
	case "string":
		return "x"
	case "integer":
		return 1
	case "number":
		return 0.5
	case "boolean":
		return false
	case "array":
		return []any{}
	default:
		return map[string]any{}
	}
}
