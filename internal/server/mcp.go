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

	"github.com/arkelythex/drenyra-engram/internal/auth"
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
const engineVersion = "0.3.0"

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
			"description": "Upsert an accounting memory (v2) under a topic key + exact scope. Each save creates a NEW immutable revision (created/updated); content, scope, source and contentHash never change after write. fiscalEffect != none lands pending_review behind the human gate.",
			"inputSchema": objectSchema(map[string]any{
				"topicKey":     stringSchema("stable upsert handle for evolving knowledge (required)"),
				"title":        stringSchema("short searchable title (required)"),
				"kind":         stringSchema("accounting kind: fact|evidence|decision|rule|exception|control|obligation|summary (required)"),
				"fiscalEffect": stringSchema("none|journal_entry|declaration|closing|adjustment|reclassification|approval|sunat_filing (required)"),
				"effectiveAt":  stringSchema("when it happened ACCOUNTING-wise, ISO-8601 (required; default record time)"),
				"scope":        scopeSchema(),
				"content":      contentSchema(),
				"source": objectSchema(map[string]any{
					"system":    stringSchema("which system produced the event (required)"),
					"actorId":   stringSchema("who (required for human actors)"),
					"actorKind": stringSchema("human|agent|system (required)"),
					"session":   stringSchema("optional session id"),
				}, "system", "actorKind"),
			}, "topicKey", "title", "kind", "fiscalEffect", "scope", "content", "source"),
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
			"name":        "engram_reject",
			"description": "Reject a pending_review memory (terminal). REQUIRES a human actor.",
			"inputSchema": objectSchema(map[string]any{
				"id":        stringSchema("memory id"),
				"actorId":   stringSchema("human professional id (required for rejection)"),
				"actorKind": stringSchema("actor kind: human|agent|system (rejection requires human)"),
			}, "id", "actorId"),
		},
		{
			"name":        "engram_void",
			"description": "Void an active|pending_review|approved memory (terminal, no successor). Admits human or system actors, NEVER an agent.",
			"inputSchema": objectSchema(map[string]any{
				"id":        stringSchema("memory id"),
				"actorId":   stringSchema("actor id"),
				"actorKind": stringSchema("actor kind: human|agent|system (void requires human or system)"),
			}, "id", "actorId"),
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
		// ── accounting_* (v2): the accounting-native surface ──
		{
			"name":        "accounting_record",
			"description": "Record an accounting memory (fact/evidence/decision/rule/exception/control/obligation/summary) with fiscal effect, effective/observed dates and structured source. fiscalEffect != none lands pending_review behind the human gate.",
			"inputSchema": objectSchema(map[string]any{
				"topicKey":     stringSchema("stable accounting-fiscal topic key (e.g. account/4011/igv-payable)"),
				"title":        stringSchema("short verb + what title"),
				"kind":         stringSchema("fact|evidence|decision|rule|exception|control|obligation|summary"),
				"fiscalEffect": stringSchema("none|journal_entry|declaration|closing|adjustment|reclassification|approval|sunat_filing"),
				"effectiveAt":  stringSchema("when it happened ACCOUNTING-wise (ISO-8601)"),
				"observedAt":   stringSchema("when it was detected (ISO-8601, optional)"),
				"source":       stringSchema(`JSON: {"system": "...", "actorId": "...", "actorKind": "human|agent|system", "session": "..."}`),
			}, "topicKey", "title", "kind", "fiscalEffect", "effectiveAt"),
		},
		{
			"name":        "accounting_get",
			"description": "Get one accounting memory by immutable id (any revision, any status).",
			"inputSchema": objectSchema(map[string]any{"id": stringSchema("memory id")}, "id"),
		},
		{
			"name":        "accounting_search",
			"description": "Scope-first accounting search with kind/status/fiscal-effect filters. The scope filter runs BEFORE ranking — out-of-scope knowledge is never scored.",
			"inputSchema": objectSchema(map[string]any{
				"query":        stringSchema("search text"),
				"scope":        stringSchema(`JSON scope: {"kind":"company","organizationId":"...","companyId":"...","ruc":"11 digits","period":"YYYYMM"}`),
				"kinds":        stringSchema("comma-separated kinds to include (optional)"),
				"status":       stringSchema("comma-separated statuses to include (optional)"),
				"fiscalEffect": stringSchema("exact fiscal effect filter (optional)"),
			}, "query", "scope"),
		},
		{
			"name":        "accounting_timeline",
			"description": "Full revision timeline of a (topicKey, exact scope) chain, ordered by revision ascending — the provenance line of a memory.",
			"inputSchema": objectSchema(map[string]any{
				"topicKey": stringSchema("stable topic key"),
				"scope":    stringSchema(`JSON scope`),
			}, "topicKey", "scope"),
		},
		{
			"name":        "accounting_compare",
			"description": "Compare two accounting memories: identity/scope/kind/status deltas and the relation verdict (supersedes/related/not_conflict).",
			"inputSchema": objectSchema(map[string]any{"idA": stringSchema("memory id"), "idB": stringSchema("memory id")}, "idA", "idB"),
		},
		{
			"name":        "accounting_approve",
			"description": "Approve a pending_review memory against the envelope hash the reviewer actually saw (v0.4.0 Step 1, ADR-003). Requires an authenticated session binding; tool arguments NEVER supply identity. The current stdio MCP server has no session binding, so the tool fails closed with AUTHENTICATION_REQUIRED.",
			"inputSchema": objectSchema(map[string]any{
				"memoryId":             stringSchema("memory id"),
				"expectedEnvelopeHash": stringSchema("envelope hash the reviewer actually saw (required)"),
				"reason":               stringSchema("human-readable justification (required)"),
				"requestId":            stringSchema("idempotency key scoped to (tenant, requestId) (required)"),
			}, "memoryId", "expectedEnvelopeHash", "reason", "requestId"),
		},
		{
			"name":        "accounting_judge",
			"description": "Record a PROFESSIONAL adjudication of a conflict (REQUIRES a human actorId): creates an approved decision memory linked to the conflict with an explains relation.",
			"inputSchema": objectSchema(map[string]any{
				"conflictId": stringSchema("id of the conflicting memory"),
				"resolution": stringSchema("the documented professional resolution"),
				"actorId":    stringSchema("human professional id (REQUIRED)"),
			}, "conflictId", "resolution", "actorId"),
		},
		{
			"name":        "accounting_link_evidence",
			"description": "Attach evidence references (XML/PDF/CDR/extracto) to a memory AFTER write — the memory stays immutable, the links grow.",
			"inputSchema": objectSchema(map[string]any{
				"id":    stringSchema("memory id"),
				"refs":  stringSchema("comma-separated evidence refs"),
				"actor": stringSchema("actor id recorded on the links (optional)"),
			}, "id", "refs"),
		},
		{
			"name":        "accounting_period_summary",
			"description": "Explainable period summary: counts by kind/status, pending human approvals, active obligations/exceptions and the effectiveAt-ordered narrative (the killer demo — why did account 4011 end with this balance).",
			"inputSchema": objectSchema(map[string]any{
				"scope": stringSchema(`JSON scope with period`),
			}, "scope"),
		},
		{
			"name":        "accounting_context",
			"description": "Current accounting memory for a scope: latest revision per (topicKey, scope) chain.",
			"inputSchema": objectSchema(map[string]any{
				"scope": stringSchema(`JSON scope`),
			}, "scope"),
		},
		{
			"name":        "accounting_doctor",
			"description": "Store health snapshot: schema version, storage, counts. Fails closed on corruption.",
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

	case "engram_reject", "engram_void":
		var args struct {
			ID        string `json:"id"`
			ActorID   string `json:"actorId"`
			ActorKind string `json:"actorKind"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("id", args.ID); err != nil {
			return nil, err
		}
		source := core.Source{
			System:    "mcp",
			ActorID:   args.ActorID,
			ActorKind: core.ActorKind(args.ActorKind),
		}
		if err := requireExplicitActor(source, call.Name == "engram_void"); err != nil {
			return errTextContent(err), nil
		}
		var (
			output core.AccountingMemory
			err    error
		)
		switch call.Name {
		case "engram_reject":
			output, err = m.api.Reject(args.ID, source)
		case "engram_void":
			output, err = m.api.Void(args.ID, source)
		}
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(output)), nil

	case "engram_supersede":
		var args struct {
			ID        string `json:"id"`
			TargetID  string `json:"targetId"`
			ActorID   string `json:"actorId"`
			ActorKind string `json:"actorKind"`
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
		source := core.Source{
			System:    "mcp",
			ActorID:   args.ActorID,
			ActorKind: core.ActorKind(args.ActorKind),
		}
		if err := requireExplicitActor(source, false); err != nil {
			return errTextContent(err), nil
		}
		output, err := m.api.Supersede(args.ID, args.TargetID, source)
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

	case "accounting_record":
		var args struct {
			TopicKey     string `json:"topicKey"`
			Title        string `json:"title"`
			Kind         string `json:"kind"`
			FiscalEffect string `json:"fiscalEffect"`
			EffectiveAt  string `json:"effectiveAt"`
			ObservedAt   string `json:"observedAt"`
			Scope        string `json:"scope"`
			Source       string `json:"source"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("topicKey", args.TopicKey); err != nil {
			return nil, err
		}
		if err := requireParams("title", args.Title); err != nil {
			return nil, err
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		source, err := decodeSource(args.Source)
		if err != nil {
			return errTextContent(err), nil
		}
		result, err := m.api.Save(core.SaveInput{
			TopicKey:     args.TopicKey,
			Title:        args.Title,
			Kind:         core.MemoryKind(args.Kind),
			Scope:        scope,
			FiscalEffect: core.FiscalEffect(args.FiscalEffect),
			EffectiveAt:  args.EffectiveAt,
			ObservedAt:   args.ObservedAt,
			Source:       source,
		})
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(result)), nil

	case "accounting_get":
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("id", args.ID); err != nil {
			return nil, err
		}
		memory, err := m.api.Get(args.ID)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(memory)), nil

	case "accounting_search":
		var args struct {
			Query        string `json:"query"`
			Scope        string `json:"scope"`
			Kinds        string `json:"kinds"`
			Status       string `json:"status"`
			FiscalEffect string `json:"fiscalEffect"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("query", args.Query); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		input := search.Input{Query: args.Query, Scope: scope}
		for _, kind := range splitCSV(args.Kinds) {
			input.Kinds = append(input.Kinds, core.MemoryKind(kind))
		}
		for _, status := range splitCSV(args.Status) {
			input.Status = append(input.Status, core.MemoryStatus(status))
		}
		if args.FiscalEffect != "" {
			effect := core.FiscalEffect(args.FiscalEffect)
			input.FiscalEffect = &effect
		}
		results, err := m.api.Search(input)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(results)), nil

	case "accounting_timeline":
		var args struct {
			TopicKey string `json:"topicKey"`
			Scope    string `json:"scope"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("topicKey", args.TopicKey); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		chain, err := m.api.Chain(args.TopicKey, scope)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(chain)), nil

	case "accounting_compare":
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

	case "accounting_approve":
		var args struct {
			MemoryID             string `json:"memory_id"`
			ExpectedEnvelopeHash string `json:"expected_envelope_hash"`
			Reason               string `json:"reason"`
			RequestID            string `json:"request_id"`
		}
		// Strict shape (design §6): ANY unknown field — including any caller-
		// supplied authority (actorId/actorKind/subjectId/roles) — is a malformed
		// argument shape (JSON-RPC -32602), never silently ignored.
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("memory_id", args.MemoryID); err != nil {
			return nil, err
		}
		if err := requireParams("expected_envelope_hash", args.ExpectedEnvelopeHash); err != nil {
			return nil, err
		}
		if err := requireParams("reason", args.Reason); err != nil {
			return nil, err
		}
		if err := requireParams("request_id", args.RequestID); err != nil {
			return nil, err
		}
		// The stdio MCP server has NO authenticated session binding (design §3):
		// tool arguments NEVER supply identity, so the tool fails closed. HTTP
		// MCP may approve only when the HTTP middleware supplies a bound
		// principal to the server.
		return errTextContent(auth.New(auth.CodeAuthenticationRequired,
			"accounting_approve requires an authenticated session binding; the stdio MCP server has none — tool arguments never supply identity")), nil

	case "accounting_judge":
		var args struct {
			ConflictID string `json:"conflictId"`
			Resolution string `json:"resolution"`
			ActorID    string `json:"actorId"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("conflictId", args.ConflictID); err != nil {
			return nil, err
		}
		if err := requireParams("resolution", args.Resolution); err != nil {
			return nil, err
		}
		memory, err := m.api.Judge(args.ConflictID, args.Resolution, core.Source{System: "mcp", ActorID: args.ActorID, ActorKind: core.ActorKindHuman})
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(memory)), nil

	case "accounting_link_evidence":
		var args struct {
			ID    string `json:"id"`
			Refs  string `json:"refs"`
			Actor string `json:"actor"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("id", args.ID); err != nil {
			return nil, err
		}
		if err := requireParams("refs", args.Refs); err != nil {
			return nil, err
		}
		refs, err := m.api.LinkEvidence(args.ID, splitCSV(args.Refs), args.Actor)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(refs)), nil

	case "accounting_period_summary":
		var args struct {
			Scope string `json:"scope"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		summary, err := m.api.PeriodSummary(scope)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(summary)), nil

	case "accounting_context":
		var args struct {
			Scope string `json:"scope"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		memories, err := m.api.Context(scope)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(memories)), nil

	case "accounting_doctor":
		report, err := m.api.Doctor()
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(report)), nil

	default:
		return nil, &jsonrpcError{Code: codeMethodNotFound, Message: "method not found: tool " + call.Name}
	}
}

// requireExplicitActor enforces the fail-closed actor contract on the gate
// surfaces: approve/reject REQUIRE an explicit human actorId+actorKind
// (GATE_REQUIRES_HUMAN otherwise — agents never approve); void admits explicit
// human or system (never agent, never an implicit default); supersede requires
// an explicit known actorKind. No surface may default an omitted actorKind to
// the highest-trust class.
func requireExplicitActor(source core.Source, allowSystem bool) error {
	if source.ActorKind == "" {
		return core.ErrGateRequiresHuman
	}
	if !core.IsValidActorKind(source.ActorKind) {
		return fmt.Errorf("INVALID_SOURCE: unknown actorKind %q — expected human|agent|system", source.ActorKind)
	}
	if source.ActorKind == core.ActorKindHuman && strings.TrimSpace(source.ActorID) == "" {
		return fmt.Errorf("INVALID_SOURCE: actorId is required for human actors")
	}
	if !allowSystem && source.ActorKind == core.ActorKindSystem {
		return core.ErrGateRequiresHuman
	}
	return nil
}

// splitCSV splits a comma-separated list, trimming spaces and dropping empties.
func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// decodeScope parses a JSON scope string.
func decodeScope(raw string) (core.Scope, error) {
	if strings.TrimSpace(raw) == "" {
		return core.Scope{}, errors.New("INVALID_SCOPE: scope is required")
	}
	var scope core.Scope
	if err := json.Unmarshal([]byte(raw), &scope); err != nil {
		return core.Scope{}, fmt.Errorf("INVALID_SCOPE: %v", err)
	}
	return scope, nil
}

// decodeSource parses a JSON source string; empty falls back to a system actor.
func decodeSource(raw string) (core.Source, error) {
	if strings.TrimSpace(raw) == "" {
		return core.Source{System: "mcp", ActorKind: core.ActorKindSystem}, nil
	}
	var source core.Source
	if err := json.Unmarshal([]byte(raw), &source); err != nil {
		return core.Source{}, fmt.Errorf("INVALID_SOURCE: %v", err)
	}
	if source.System == "" {
		source.System = "mcp"
	}
	if !core.IsValidActorKind(source.ActorKind) {
		return core.Source{}, errors.New("INVALID_SOURCE: actorKind must be human|agent|system")
	}
	return source, nil
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

// decodeArgumentsStrict unmarshals tool arguments REJECTING any unknown field —
// the strict shape for surfaces where caller-declared authority (or any extra
// field) must never be silently ignored (design §6: accounting_approve accepts
// exactly its four command arguments).
func decodeArgumentsStrict(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return &jsonrpcError{Code: codeInvalidParams, Message: "invalid params: missing arguments"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
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
