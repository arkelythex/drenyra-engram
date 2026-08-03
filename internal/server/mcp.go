// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module exposes the shared API as a
// Model Context Protocol (MCP) server; observation content is structured text
// with no monetary fields, so no money value crosses this surface.
//
// MCP server — JSON-RPC 2.0 over stdio (agents) and POST /mcp (streamable-HTTP
// JSON mode). Implements the tool surface of contracts/memory.md, scope.md,
// lifecycle.md and provenance.md through the shared domain services
// (internal/server/api.go); the CLI and the HTTP API exercise the same code
// path, so semantics are byte-identical everywhere.
//
// Protocol: initialize → notifications/initialized → tools/list → tools/call.
// Only the `tools` capability is advertised (no resources/prompts in this
// slice). Domain failures (validation, not-found, illegal transition) return a
// tool result with isError=true and the engine's stable error code as text;
// malformed tool names or argument shapes are JSON-RPC errors (-32601/-32602).
//
// Non-authorization boundary (contracts/provenance.md): the tool catalog has
// NO authorize/approve/allow tool, ever. Memory guides; it never authorizes.

package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/search"
)

// Protocol versions this server speaks. The client's requested version is
// echoed when supported; otherwise the server advertises its latest.
var supportedProtocolVersions = []string{
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

const latestProtocolVersion = "2025-06-18"

// engineVersion is the semantic version of the Go engine reported in the MCP
// serverInfo and the HTTP surface.
const engineVersion = "0.2.0"

// ──────────────────────────────────────────────
// JSON-RPC 2.0 wire shapes
// ──────────────────────────────────────────────

// jsonrpcError codes (JSON-RPC 2.0 + MCP).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface so handlers can return JSON-RPC errors
// through the normal error path and classify them with errors.As.
func (e *jsonrpcError) Error() string {
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
}

// MCPServer exposes the shared API through the Model Context Protocol.
type MCPServer struct {
	api *API
}

// NewMCPServer returns an MCP server over the shared API.
func NewMCPServer(api *API) *MCPServer {
	return &MCPServer{api: api}
}

// ──────────────────────────────────────────────
// stdio transport (agents: Claude Desktop, pi, etc.)
// ──────────────────────────────────────────────

// ServeStdio serves newline-delimited JSON-RPC over the given reader/writer
// (typically os.Stdin / os.Stdout). It returns on EOF or a read error.
func (m *MCPServer) ServeStdio(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	// MCP arguments can carry full observation payloads — size the buffer
	// generously.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		response := m.HandleMessage(line)
		if response == nil {
			continue // notification — no response
		}
		if _, err := w.Write(response); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// ──────────────────────────────────────────────
// Message dispatch
// ──────────────────────────────────────────────

// HandleMessage processes one JSON-RPC message and returns the response bytes,
// or nil when the message is a notification (no response). It never panics on
// malformed input — parse failures produce a JSON-RPC error response.
func (m *MCPServer) HandleMessage(raw []byte) []byte {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '[' {
		// JSON-RPC batches are not supported by MCP; respond invalid request.
		return m.errorResponse(nil, codeInvalidRequest, "invalid request: JSON-RPC batches are not supported")
	}
	var request jsonrpcRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return m.errorResponse(nil, codeParseError, "parse error: "+err.Error())
	}
	if request.JSONRPC != "2.0" {
		return m.errorResponse(request.ID, codeInvalidRequest, "invalid request: jsonrpc must be \"2.0\"")
	}

	// A request without an id is a notification: process, never respond.
	if len(request.ID) == 0 {
		m.handleNotification(request)
		return nil
	}

	result, err := m.dispatch(request.Method, request.Params)
	if err != nil {
		if isJSONRPCError(err) {
			var jerr *jsonrpcError
			_ = errors.As(err, &jerr)
			return m.errorResponse(request.ID, jerr.Code, jerr.Message)
		}
		// Unknown methods and shape errors get their JSON-RPC codes.
		switch {
		case strings.HasPrefix(err.Error(), "method not found"):
			return m.errorResponse(request.ID, codeMethodNotFound, err.Error())
		case strings.HasPrefix(err.Error(), "invalid params"):
			return m.errorResponse(request.ID, codeInvalidParams, err.Error())
		default:
			return m.errorResponse(request.ID, codeInternalError, err.Error())
		}
	}

	response := jsonrpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result}
	encoded, err := json.Marshal(response)
	if err != nil {
		return m.errorResponse(request.ID, codeInternalError, "encode response: "+err.Error())
	}
	return encoded
}

func (m *MCPServer) errorResponse(id json.RawMessage, code int, message string) []byte {
	response := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: message},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return []byte(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"encode error response"}}`)
	}
	return encoded
}

// isJSONRPCError reports whether err carries a JSON-RPC code.
func isJSONRPCError(err error) bool {
	var jerr *jsonrpcError
	return errors.As(err, &jerr)
}

// handleNotification processes methods that expect no response. Unknown
// notifications are ignored (spec behavior — notifications have no reply).
func (m *MCPServer) handleNotification(request jsonrpcRequest) {
	switch request.Method {
	case "notifications/initialized":
		// No-op: the client signals it is ready after initialize.
	case "notifications/cancelled":
		// No-op: cancellation is best-effort.
	case "notifications/roots/list_changed":
		// No-op: this server does not read roots.
	default:
		// Unknown notifications are dropped silently.
	}
}

// dispatch routes a method to its handler.
func (m *MCPServer) dispatch(method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		return m.handleInitialize(params)
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return m.handleToolsList()
	case "tools/call":
		return m.handleToolsCall(params)
	case "resources/list":
		return map[string]any{"resources": []any{}}, nil
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	default:
		return nil, errors.New("method not found: " + method)
	}
}

// ──────────────────────────────────────────────
// initialize
// ──────────────────────────────────────────────

func (m *MCPServer) handleInitialize(params json.RawMessage) (any, error) {
	var request struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &jsonrpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
		}
	}
	version := request.ProtocolVersion
	if !supportedVersion(version) {
		version = latestProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]string{
			"name":    "drenyra-engram",
			"version": engineVersion,
		},
		"instructions": "Institutional accounting memory (scope-first). Memory guides decisions; it never authorizes them. " +
			"Every company-scoped read requires an exact scope (kind=company, organizationId, companyId, ruc, optional period). " +
			"Lifecycle: draft → reviewed → promoted → superseded, adjacent-forward only.",
	}, nil
}

func supportedVersion(version string) bool {
	for _, supported := range supportedProtocolVersions {
		if version == supported {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────
// tools/list
// ──────────────────────────────────────────────

// ToolCatalog returns the MCP tool definitions (exported for the conformance
// test that asserts the non-authorization boundary: no authorize/approve/allow).
func ToolCatalog() []map[string]any {
	return []map[string]any{
		{
			"name":        "engram_save",
			"description": "Upsert an observation under a topic key + exact scope. Each save creates a NEW immutable revision (created/updated); content, scope and provenance never change after write.",
			"inputSchema": objectSchema(map[string]any{
				"topicKey": stringSchema("stable upsert handle for evolving knowledge (required)"),
				"title":    stringSchema("short searchable title (required)"),
				"type":     stringSchema("free-form observation category, e.g. decision, discovery, pattern (optional)"),
				"scope":    scopeSchema(),
				"content":  contentSchema(),
				"authorityStatus": enumSchema("optional initial lifecycle state (default draft)",
					"draft", "reviewed", "promoted", "superseded"),
				"validity": objectSchema(map[string]any{
					"effectiveAt": stringSchema("UTC ISO-8601 effective time (optional)"),
					"expiresAt":   stringSchema("UTC ISO-8601 expiry time (optional); expired observations surface as stale"),
				}),
				"provenance": objectSchema(map[string]any{
					"actor":     stringSchema("who created it (required)"),
					"timestamp": stringSchema("UTC ISO-8601 creation time (required)"),
					"source":    stringSchema("where it came from, e.g. cli, http, mcp (required)"),
					"session":   stringSchema("optional session id"),
				}, "actor", "timestamp", "source"),
			}, "topicKey", "title", "type", "scope", "content", "provenance"),
		},
		{
			"name":        "engram_get",
			"description": "Get one observation by its immutable id (any revision, any status).",
			"inputSchema": objectSchema(map[string]any{
				"id": stringSchema("observation id"),
			}, "id"),
		},
		{
			"name":        "engram_get_by_topic",
			"description": "Get the LATEST revision of a (topicKey, exact scope) chain, if any.",
			"inputSchema": objectSchema(map[string]any{
				"topicKey": stringSchema("topic key"),
				"scope":    scopeSchema(),
			}, "topicKey", "scope"),
		},
		{
			"name":        "engram_chain",
			"description": "Get the FULL revision history of a (topicKey, exact scope) chain, ordered by revision ascending — every revision, not just the current one.",
			"inputSchema": objectSchema(map[string]any{
				"topicKey": stringSchema("topic key"),
				"scope":    scopeSchema(),
			}, "topicKey", "scope"),
		},
		{
			"name":        "engram_search",
			"description": "Scope-first search: the scope filter runs BEFORE ranking, so out-of-scope knowledge is never scored. Returns latest revision per chain, ranked by token overlap, with a stale flag for expired observations.",
			"inputSchema": objectSchema(map[string]any{
				"query": stringSchema("search text; tokens are matched across title + What/Why/Where/Learned"),
				"scope": scopeSchema(),
				"matchMode": enumSchema("all requires every query token, any requires at least one (default any)",
					"all", "any"),
				"includeInstitutional": boolSchema("include institutional observations for a company query scope (default false)"),
				"limit":                intSchema("max results (default 20)"),
			}, "query", "scope"),
		},
		{
			"name":        "engram_context",
			"description": "CURRENT memory for a scope: latest revision per (topicKey, exact scope) chain — never the full revision history.",
			"inputSchema": objectSchema(map[string]any{
				"scope": scopeSchema(),
			}, "scope"),
		},
		{
			"name":        "engram_compare",
			"description": "Report identity/scope/content deltas and the relation verdict between two stored observations (supersedes/related/not_conflict). A completed supersede pair reports supersedes only when the SOURCE is stored superseded.",
			"inputSchema": objectSchema(map[string]any{
				"idA": stringSchema("first observation id"),
				"idB": stringSchema("second observation id"),
			}, "idA", "idB"),
		},
		{
			"name":        "engram_doctor",
			"description": "Store health snapshot: schema version, storage, and counts (observations, revision chains, transitions, relations). Fails closed on corruption.",
			"inputSchema": objectSchema(nil),
		},
		{
			"name":        "engram_review",
			"description": "Lifecycle transition draft → reviewed. Adjacent-forward only; illegal moves fail with INVALID_TRANSITION and leave the observation unchanged.",
			"inputSchema": objectSchema(map[string]any{
				"id":    stringSchema("observation id"),
				"actor": stringSchema("actor recorded in the audit trail (optional)"),
			}, "id"),
		},
		{
			"name":        "engram_promote",
			"description": "Lifecycle transition reviewed → promoted. Adjacent-forward only; illegal moves fail with INVALID_TRANSITION.",
			"inputSchema": objectSchema(map[string]any{
				"id":    stringSchema("observation id"),
				"actor": stringSchema("actor recorded in the audit trail (optional)"),
			}, "id"),
		},
		{
			"name":        "engram_supersede",
			"description": "Lifecycle transition promoted → superseded with a REQUIRED replacement target; records a supersedes relation routing readers from this observation to the target. Never auto-promotes the replacement.",
			"inputSchema": objectSchema(map[string]any{
				"id":       stringSchema("observation id being superseded"),
				"targetId": stringSchema("REQUIRED replacing observation id"),
				"actor":    stringSchema("actor recorded in the audit trail (optional)"),
			}, "id", "targetId"),
		},
		{
			"name":        "engram_relations",
			"description": "Every recorded relation, insertion order (supersedes pairs included).",
			"inputSchema": objectSchema(nil),
		},
		{
			"name":        "engram_transitions",
			"description": "The full lifecycle audit trail: every status transition with actor and timestamp.",
			"inputSchema": objectSchema(nil),
		},
	}
}

func (m *MCPServer) handleToolsList() (any, error) {
	return map[string]any{"tools": ToolCatalog()}, nil
}

// ──────────────────────────────────────────────
// tools/call
// ──────────────────────────────────────────────

// toolCallOutput is the MCP result shape for tools/call.
type toolCallOutput struct {
	Content []map[string]string `json:"content"`
	IsError bool                `json:"isError"`
}

// textContent builds the tool result payload (agents receive the JSON text).
func textContent(text string) toolCallOutput {
	return toolCallOutput{
		Content: []map[string]string{{"type": "text", "text": text}},
	}
}

// errTextContent builds a FAILED tool result: isError=true with the engine's
// stable error code as the message text (agents see why the call failed).
func errTextContent(err error) toolCallOutput {
	return toolCallOutput{
		Content: []map[string]string{{"type": "text", "text": err.Error()}},
		IsError: true,
	}
}

func (m *MCPServer) handleToolsCall(params json.RawMessage) (any, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, &jsonrpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	if call.Name == "" {
		return nil, &jsonrpcError{Code: codeInvalidParams, Message: "invalid params: tool name is required"}
	}

	switch call.Name {
	case "engram_save":
		var input core.SaveInput
		if err := decodeArguments(call.Arguments, &input); err != nil {
			return nil, err
		}
		result, err := m.api.Save(input)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(result)), nil

	case "engram_get":
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("id", args.ID); err != nil {
			return nil, err
		}
		observation, err := m.api.Get(args.ID)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(observation)), nil

	case "engram_get_by_topic":
		var args struct {
			TopicKey string     `json:"topicKey"`
			Scope    core.Scope `json:"scope"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("topicKey", args.TopicKey); err != nil {
			return nil, err
		}
		observation, err := m.api.GetByTopic(args.TopicKey, args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(observation)), nil

	case "engram_chain":
		var args struct {
			TopicKey string     `json:"topicKey"`
			Scope    core.Scope `json:"scope"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("topicKey", args.TopicKey); err != nil {
			return nil, err
		}
		chain, err := m.api.Chain(args.TopicKey, args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(chain)), nil

	case "engram_search":
		var args struct {
			Query                string     `json:"query"`
			Scope                core.Scope `json:"scope"`
			MatchMode            string     `json:"matchMode"`
			IncludeInstitutional bool       `json:"includeInstitutional"`
			Limit                int        `json:"limit"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("query", args.Query); err != nil {
			return nil, err
		}
		results, err := m.api.Search(search.Input{
			Query:                args.Query,
			Scope:                args.Scope,
			MatchMode:            search.MatchMode(args.MatchMode),
			IncludeInstitutional: args.IncludeInstitutional,
			Limit:                args.Limit,
		})
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(results)), nil

	case "engram_context":
		var args struct {
			Scope core.Scope `json:"scope"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		observations, err := m.api.Context(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(observations)), nil

	case "engram_compare":
		var args struct {
			IDA string `json:"idA"`
			IDB string `json:"idB"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("idA", args.IDA); err != nil {
			return nil, err
		}
		if err := requireParams("idB", args.IDB); err != nil {
			return nil, err
		}
		output, err := m.api.Compare(args.IDA, args.IDB)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(output)), nil

	case "engram_doctor":
		report, err := m.api.Doctor()
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(report)), nil

	case "engram_review", "engram_promote":
		var args struct {
			ID    string `json:"id"`
			Actor string `json:"actor"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("id", args.ID); err != nil {
			return nil, err
		}
		var (
			output TransitionOutput
			err    error
		)
		if call.Name == "engram_review" {
			output, err = m.api.Review(args.ID, args.Actor)
		} else {
			output, err = m.api.Promote(args.ID, args.Actor)
		}
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(output)), nil

	case "engram_supersede":
		var args struct {
			ID       string `json:"id"`
			TargetID string `json:"targetId"`
			Actor    string `json:"actor"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("id", args.ID); err != nil {
			return nil, err
		}
		if err := requireParams("targetId", args.TargetID); err != nil {
			return nil, err
		}
		output, err := m.api.Supersede(args.ID, args.TargetID, args.Actor)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(output)), nil

	case "engram_relations":
		relations, err := m.api.Relations()
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(relations)), nil

	case "engram_transitions":
		transitions, err := m.api.Transitions()
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(transitions)), nil

	default:
		return nil, &jsonrpcError{Code: codeMethodNotFound, Message: "method not found: tool " + call.Name}
	}
}

// decodeArguments unmarshals tool arguments; a shape failure is an invalid-params
// JSON-RPC error (the caller sent a malformed schema, not a domain failure).
func decodeArguments(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return &jsonrpcError{Code: codeInvalidParams, Message: "invalid params: missing arguments"}
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return &jsonrpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	return nil
}

// requireParams fails with -32602 when any named required field is empty — the
// server enforces the declared inputSchema `required` array (advisory for
// clients, enforced here, fail closed).
func requireParams(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return &jsonrpcError{Code: codeInvalidParams, Message: "invalid params: " + field + " is required"}
	}
	return nil
}

// mustJSON serializes a result into the tool text payload; a serialization
// failure here is an internal error (results are engine-owned types).
func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf(`{"error":"encode result: %v"}`, err)
	}
	return string(encoded)
}

// ──────────────────────────────────────────────
// JSON schema helpers (MCP inputSchema)
// ──────────────────────────────────────────────

func objectSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func boolSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func intSchema(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func enumSchema(description string, values ...string) map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        values,
		"description": description,
	}
}

func scopeSchema() map[string]any {
	return objectSchema(map[string]any{
		"kind": map[string]any{
			"type":        "string",
			"enum":        []string{"company", "institutional"},
			"description": "company scopes to an organization/company/RUC/period; institutional is explicit cross-company knowledge",
		},
		"organizationId": stringSchema("organization id (required for kind=company)"),
		"companyId":      stringSchema("company id (required for kind=company)"),
		"ruc":            stringSchema("Peruvian RUC — exactly 11 digits (required for kind=company)"),
		"period":         stringSchema("fiscal period YYYYMM, optional — a perioded scope never matches an unperioded one"),
	}, "kind")
}

func contentSchema() map[string]any {
	return objectSchema(map[string]any{
		"what":    stringSchema("what was done (required)"),
		"why":     stringSchema("why it was done (required)"),
		"where":   stringSchema("files/paths affected (required)"),
		"learned": stringSchema("gotchas and surprises (required)"),
	}, "what", "why", "where", "learned")
}
