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
	"context"
	"encoding/base64"
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
	// judgmentStore is the atomic judgment surface the adjudication tools
	// delegate to (one BEGIN IMMEDIATE store operation per transition, v0.4.0
	// Step 2 design §2). The SQLiteStore satisfies it; a store without the
	// judgment surface makes the adjudication tools fail closed.
	judgmentStore JudgmentStore
	// reconciliationStore is the atomic first-class reconciliation surface the
	// accounting_reconciliation_* tools delegate to (one BEGIN IMMEDIATE store
	// operation per transition, v0.5.0 design §3.2). The SQLiteStore satisfies
	// it; a store without the reconciliation surface makes the tools fail
	// closed.
	reconciliationStore ReconciliationStore
	// defaultContext is the automatic session context (v0.5.0 design §5): the
	// CurrentContext loaded at construction for the configured
	// DRENYRA_DEFAULT_SCOPE exact scope. nil when no default scope is
	// configured — initialize then carries a null context and its
	// instructions point at the accounting_current_context tool. The context
	// is never inferred and never partial (construction fails closed instead).
	defaultContext *core.CurrentContext
}

// NewMCPServer returns an MCP server over the shared API with NO default
// session context (DRENYRA_DEFAULT_SCOPE unset): initialize carries a null
// context and the instructions point at accounting_current_context.
func NewMCPServer(api *API) *MCPServer {
	m, _ := NewMCPServerWithDefaultScope(api, "")
	return m
}

// NewMCPServerWithDefaultScope returns an MCP server whose initialize carries
// the automatic session context (design §5) for the configured exact company
// scope. defaultScopeJSON is the raw DRENYRA_DEFAULT_SCOPE value
// (JSON-encoded company scope). It FAILS CLOSED: an absent value yields a
// server with a null default context; a present but malformed JSON scope,
// non-company scope, invalid period or inaccessible scope is a construction
// error — the server never starts with partial cross-scope data. The context
// is loaded eagerly so an inaccessible scope fails at construction, exactly
// where the operator can see it.
func NewMCPServerWithDefaultScope(api *API, defaultScopeJSON string) (*MCPServer, error) {
	m := &MCPServer{api: api}
	if judgments, ok := api.Store.(JudgmentStore); ok {
		m.judgmentStore = judgments
	}
	if reconciliations, ok := api.Store.(ReconciliationStore); ok {
		m.reconciliationStore = reconciliations
	}
	raw := strings.TrimSpace(defaultScopeJSON)
	if raw == "" {
		return m, nil // unset → null context, instructions point at the tool
	}
	var scope core.Scope
	if err := json.Unmarshal([]byte(raw), &scope); err != nil {
		return nil, fmt.Errorf("DRENYRA_DEFAULT_SCOPE: invalid JSON scope: %v", err)
	}
	if err := core.AssertValidScope(scope); err != nil {
		return nil, fmt.Errorf("DRENYRA_DEFAULT_SCOPE: %v", err)
	}
	if scope.Kind != core.ScopeKindCompany || scope.Period == "" {
		return nil, errors.New("DRENYRA_DEFAULT_SCOPE: the default scope must be an exact company scope with a YYYYMM period")
	}
	ctx, err := CurrentContextFor(context.Background(), api, scope)
	if err != nil {
		return nil, fmt.Errorf("DRENYRA_DEFAULT_SCOPE: the configured scope is not accessible: %v", err)
	}
	m.defaultContext = &ctx
	return m, nil
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
	// The automatic session context (v0.5.0 design §5): the CurrentContext of
	// the configured DRENYRA_DEFAULT_SCOPE exact scope, or null when no default
	// scope is configured — the instructions then point at the
	// accounting_current_context tool. The context is never inferred and never
	// partial (a present-but-invalid configuration fails at construction).
	var currentContext any
	instructions := baseInstructions
	if m.defaultContext != nil {
		currentContext = *m.defaultContext
		instructions += " A default session context for the configured scope is provided in _meta.drenyra/currentContext; call accounting_current_context to refresh it."
	} else {
		instructions += " No default session scope is configured; call accounting_current_context with an exact company scope to load the session context."
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
		"instructions": instructions,
		"_meta": map[string]any{
			"drenyra/currentContext": currentContext,
		},
	}, nil
}

// baseInstructions is the fixed initialize instruction text (v0.5.0 design §5):
// scope-first reads, the per-memory lifecycle with the human approval gate, and
// the period closure states. The stale v0.3 line ("Lifecycle: draft → reviewed
// → promoted → superseded, adjacent-forward only") is gone: a memory's status
// chain and the period's closure state are separate semantics, and any fiscal-
// effect write lands pending_review behind the human approval gate.
const baseInstructions = "Institutional accounting memory (scope-first). Memory guides decisions; it never authorizes them. " +
	"Every company-scoped read requires an exact scope (kind=company, organizationId, companyId, ruc, optional period). " +
	"Lifecycle is per memory: draft → reviewed → promoted → superseded (adjacent-forward), and any write with a fiscal effect " +
	"lands pending_review behind the human approval gate (approve/reject/void/supersede; agents never approve). " +
	"Approving a monthly close (kind=summary, fiscalEffect=closing) closes its period (closureState=closed): later period-scoped " +
	"mutations are BLOCKED until an explicit controller reopen (closureState=reopened)."

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
		// ── accounting_close_* (v0.5.0 close foundation, design §6): creation is
		// a NORMAL SAVE by an agent with a provenance-only source claim (the tool
		// stamps the agent source — tool arguments never carry identity); the
		// APPROVAL is the authenticated controller act through accounting_approve.
		// Reopening is the explicit authenticated controller act and fails closed
		// with AUTHENTICATION_REQUIRED on this session-less stdio server.
		{
			"name":        "accounting_close_create",
			"description": "Create a monthly close memory (kind=summary, fiscalEffect=closing, topic closing/CIERRE-<period>) in pending_review (v0.5.0 close foundation, design §2.1). The agent source is stamped by the server (provenance, never authority); each total requires code, currency, signed int64 cents and at least one same-scope source memory id. Approval happens through the authenticated accounting_approve.",
			"inputSchema": objectSchema(map[string]any{
				"period": stringSchema("fiscal period YYYYMM the close covers (required; must equal the scope period)"),
				"scope":  stringSchema(`JSON scope with period (required)`),
				"totals": map[string]any{
					"type":        "array",
					"description": "explicit monetary totals (required): each {code, currency, amount_cents (signed int64), source_memory_ids[]}",
					"items": objectSchema(map[string]any{
						"code":              stringSchema("total code (e.g. igv, ventas)"),
						"currency":          stringSchema("ISO 4217 currency code (e.g. PEN, USD)"),
						"amount_cents":      intSchema("signed total in cents (int64; never float)"),
						"source_memory_ids": map[string]any{"type": "array", "items": stringSchema("same-scope source memory id"), "description": "at least one same-scope source memory (required)"},
					}, "code", "currency", "amount_cents", "source_memory_ids"),
				},
				"reason": stringSchema("close rationale (optional)"),
			}, "period", "scope", "totals"),
		},
		{
			"name":        "accounting_period_reopen",
			"description": "Explicitly reopen a closed period (v0.5.0 close foundation, design §2.3) — the authenticated controller act that admits corrections. Requires an authenticated session binding; tool arguments NEVER supply identity. The current stdio MCP server has no session binding, so the tool fails closed with AUTHENTICATION_REQUIRED.",
			"inputSchema": objectSchema(map[string]any{
				"period":                   stringSchema("fiscal period YYYYMM being reopened"),
				"scope":                    stringSchema(`JSON scope with period`),
				"expected_close_memory_id": stringSchema("close memory id that closed the period (the current closure row)"),
				"reason":                   stringSchema("human-readable reopen justification (required)"),
				"request_id":               stringSchema("idempotency key scoped to (tenant, requestId) (required)"),
			}, "period", "scope", "expected_close_memory_id", "reason", "request_id"),
		},
		// ── accounting_judgment_* (v0.4.0 Step 2): the adjudication surface. The
		// caller-declared accounting_judge tool is GONE (design §4): proposals and
		// withdrawals carry a provenance-only source (agent|system — NEVER
		// authority); confirm/reject require an authenticated session binding and
		// tool arguments NEVER supply identity (design §7).
		{
			"name":        "accounting_judgment_propose",
			"description": "Propose an adjudicable judgment over two observations (v0.4.0 Step 2). The proposer source is provenance ONLY (agent|system; a human source is rejected PROPOSAL_UNAUTHORIZED) — it never authorizes. Confirmation/rejection happen through the authenticated confirm/reject tools.",
			"inputSchema": objectSchema(map[string]any{
				"from_id":        stringSchema("first observation id (required)"),
				"to_id":          stringSchema("second observation id (required)"),
				"relation":       enumSchema("the proposable adjudication relation (required)", "supports", "contradicts", "explains", "reconciles", "reverses", "supersedes"),
				"reason":         stringSchema("the proposer's justification (required)"),
				"request_id":     stringSchema("idempotency key scoped to (tenant, requestId) (required)"),
				"predecessor_id": stringSchema("id of an existing judgment this proposal corrects (optional)"),
				"source": objectSchema(map[string]any{
					"system":     stringSchema("which system produced the proposal (required)"),
					"actor_id":   stringSchema("actor id (provenance, never authority)"),
					"actor_kind": enumSchema("agent|system only — a human source is rejected PROPOSAL_UNAUTHORIZED", "agent", "system"),
					"session":    stringSchema("optional session id"),
				}, "system", "actor_kind"),
			}, "from_id", "to_id", "relation", "reason", "request_id", "source"),
		},
		{
			"name":        "accounting_judgment_confirm",
			"description": "Confirm a proposed judgment with the professional human resolution (v0.4.0 Step 2). Requires an authenticated session binding; tool arguments NEVER supply identity. The current stdio MCP server has no session binding, so the tool fails closed with AUTHENTICATION_REQUIRED.",
			"inputSchema": objectSchema(map[string]any{
				"judgment_id":            stringSchema("judgment id"),
				"resolution":             stringSchema("the professional human resolution (required)"),
				"expected_judgment_hash": stringSchema("the proposed judgment hash the adjudicator actually saw (required)"),
				"request_id":             stringSchema("idempotency key scoped to (tenant, requestId) (required)"),
			}, "judgment_id", "resolution", "expected_judgment_hash", "request_id"),
		},
		{
			"name":        "accounting_judgment_reject",
			"description": "Reject a proposed judgment with a human reason (v0.4.0 Step 2). Requires an authenticated session binding; tool arguments NEVER supply identity. The current stdio MCP server has no session binding, so the tool fails closed with AUTHENTICATION_REQUIRED.",
			"inputSchema": objectSchema(map[string]any{
				"judgment_id":            stringSchema("judgment id"),
				"reason":                 stringSchema("the human rejection reason (required)"),
				"expected_judgment_hash": stringSchema("the proposed judgment hash the adjudicator actually saw (required)"),
				"request_id":             stringSchema("idempotency key scoped to (tenant, requestId) (required)"),
			}, "judgment_id", "reason", "expected_judgment_hash", "request_id"),
		},
		{
			"name":        "accounting_judgment_withdraw",
			"description": "Withdraw the caller's OWN proposed judgment (v0.4.0 Step 2). The provenance source must match the original proposer exactly (PROPOSAL_UNAUTHORIZED otherwise) — provenance continuity, never professional authorization.",
			"inputSchema": objectSchema(map[string]any{
				"judgment_id": stringSchema("judgment id"),
				"request_id":  stringSchema("idempotency key scoped to (tenant, requestId) (required)"),
				"source": objectSchema(map[string]any{
					"system":     stringSchema("which system produced the withdrawal (required)"),
					"actor_id":   stringSchema("actor id (provenance, never authority)"),
					"actor_kind": enumSchema("agent|system only", "agent", "system"),
					"session":    stringSchema("optional session id"),
				}, "system", "actor_kind"),
			}, "judgment_id", "request_id", "source"),
		},
		// ── accounting_reconciliation_* (v0.5.0, design §3.2/§6): the
		// first-class reconciliation surface mirrors judgments 1:1 — proposals and
		// withdrawals carry a provenance-only source (agent|system — NEVER
		// authority); confirm/reject require an authenticated session binding and
		// tool arguments NEVER supply identity. Domain amounts are integer cents
		// (int64; never floats — the fiscal transport contract).
		{
			"name":        "accounting_reconciliation_propose",
			"description": "Propose a first-class reconciliation over two observations (v0.5.0, design §3.2): the endpoint pair, method/currency, the domain amounts and tolerance as INTEGER cents (int64; never float), the proposer's reason, and a provenance-only source. The proposer source is provenance ONLY (agent|system; a human source is rejected PROPOSAL_UNAUTHORIZED) — it never authorizes. Confirmation/rejection happen through the authenticated confirm/reject tools.",
			"inputSchema": objectSchema(map[string]any{
				"left_memory_id":     stringSchema("left observation id (required)"),
				"right_memory_id":    stringSchema("right observation id (required)"),
				"method":             stringSchema("reconciliation method (e.g. extracto_contable; required)"),
				"currency":           stringSchema("ISO 4217 currency code (e.g. PEN; required)"),
				"left_amount_cents":  intSchema("left endpoint amount in int64 cents (required; never float)"),
				"right_amount_cents": intSchema("right endpoint amount in int64 cents (required; never float)"),
				"tolerance_cents":    intSchema("accepted variance band in int64 cents (required, non-negative)"),
				"reason":             stringSchema("the proposer's justification (required)"),
				"request_id":         stringSchema("idempotency key scoped to (tenant, requestId) (required)"),
				"predecessor_id":     stringSchema("id of an existing reconciliation this proposal corrects (optional)"),
				"source": objectSchema(map[string]any{
					"system":     stringSchema("which system produced the proposal (required)"),
					"actor_id":   stringSchema("actor id (provenance, never authority)"),
					"actor_kind": enumSchema("agent|system only — a human source is rejected PROPOSAL_UNAUTHORIZED", "agent", "system"),
					"session":    stringSchema("optional session id"),
				}, "system", "actor_kind"),
			}, "left_memory_id", "right_memory_id", "method", "currency", "left_amount_cents", "right_amount_cents", "tolerance_cents", "reason", "request_id", "source"),
		},
		{
			"name":        "accounting_reconciliation_confirm",
			"description": "Confirm a proposed reconciliation with the professional human resolution (v0.5.0, design §3.2). Requires an authenticated session binding; tool arguments NEVER supply identity. The current stdio MCP server has no session binding, so the tool fails closed with AUTHENTICATION_REQUIRED.",
			"inputSchema": objectSchema(map[string]any{
				"reconciliation_id":            stringSchema("reconciliation id"),
				"resolution":                   stringSchema("the professional human resolution (required)"),
				"expected_reconciliation_hash": stringSchema("the proposed reconciliation hash the adjudicator actually saw (required)"),
				"request_id":                   stringSchema("idempotency key scoped to (tenant, requestId) (required)"),
			}, "reconciliation_id", "resolution", "expected_reconciliation_hash", "request_id"),
		},
		{
			"name":        "accounting_reconciliation_reject",
			"description": "Reject a proposed reconciliation with a human reason (v0.5.0, design §3.2). Requires an authenticated session binding; tool arguments NEVER supply identity. The current stdio MCP server has no session binding, so the tool fails closed with AUTHENTICATION_REQUIRED.",
			"inputSchema": objectSchema(map[string]any{
				"reconciliation_id":            stringSchema("reconciliation id"),
				"reason":                       stringSchema("the human rejection reason (required)"),
				"expected_reconciliation_hash": stringSchema("the proposed reconciliation hash the adjudicator actually saw (required)"),
				"request_id":                   stringSchema("idempotency key scoped to (tenant, requestId) (required)"),
			}, "reconciliation_id", "reason", "expected_reconciliation_hash", "request_id"),
		},
		{
			"name":        "accounting_reconciliation_withdraw",
			"description": "Withdraw the caller's OWN proposed reconciliation (v0.5.0, design §3.2). The provenance source must match the original proposer exactly (PROPOSAL_UNAUTHORIZED otherwise) — provenance continuity, never professional authorization.",
			"inputSchema": objectSchema(map[string]any{
				"reconciliation_id": stringSchema("reconciliation id"),
				"request_id":        stringSchema("idempotency key scoped to (tenant, requestId) (required)"),
				"source": objectSchema(map[string]any{
					"system":     stringSchema("which system produced the withdrawal (required)"),
					"actor_id":   stringSchema("actor id (provenance, never authority)"),
					"actor_kind": enumSchema("agent|system only", "agent", "system"),
					"session":    stringSchema("optional session id"),
				}, "system", "actor_kind"),
			}, "reconciliation_id", "request_id", "source"),
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
			"name":        "accounting_current_context",
			"description": "Session context for an exact company scope (v0.5.0, design §5): the explainable period summary with closure state and latest close, the shared pending-item digest, and the at most 20 most recent chains (latest revision per chain, effectiveAt desc). Pure read — no writes. Strict decode: the only accepted argument is scope.",
			"inputSchema": objectSchema(map[string]any{
				"scope": stringSchema(`JSON company scope with period (required)`),
			}, "scope"),
		},
		{
			"name":        "accounting_compare_periods",
			"description": "Period-over-period comparison (v0.5.0, design §4): deterministic chain/status/pending/close deltas between two exact company scopes of the same tenant, company and RUC with distinct YYYYMM periods. Pure read — no schema, no writes. from_scope and to_scope are JSON company scopes (the same wire shape as accounting_period_summary's scope).",
			"inputSchema": objectSchema(map[string]any{
				"from_scope": stringSchema(`JSON company scope with the from period (required)`),
				"to_scope":   stringSchema(`JSON company scope with the to period (required)`),
			}, "from_scope", "to_scope"),
		},
		{
			"name":        "accounting_context",
			"description": "Current accounting memory for a scope: latest revision per (topicKey, scope) chain.",
			"inputSchema": objectSchema(map[string]any{
				"scope": stringSchema(`JSON scope`),
			}, "scope"),
		},
		{
			"name":        "accounting_object_store",
			"description": "Store ONE evidence object WORM-style (v0.7.0): artifact bytes as standard padded base64, exact company scope, capture source. Identity is the SHA-256 of the bytes; identical bytes already stored is a NO-OP (created=false). Writes to a CLOSED exact company period fail with PERIOD_CLOSED. This surface can NEVER approve anything — storing is a provenance-recorded capture, not an authorization.",
			"inputSchema": objectSchema(map[string]any{
				"bytesB64":    stringSchema("artifact bytes as standard padded base64"),
				"contentType": stringSchema("optional MIME hint, stored verbatim"),
				"scope":       stringSchema(`JSON exact company scope: {"kind":"company","organizationId":"...","companyId":"...","ruc":"11 digits","period":"YYYYMM"}`),
				"source":      stringSchema(`JSON capture provenance: {"system":"...","actorId":"...","actorKind":"agent|system","reference":"..."}`),
			}, "bytesB64", "scope", "source"),
		},
		{
			"name":        "accounting_object_get",
			"description": "Get one evidence object SCOPE-FIRST (v0.7.0): the caller's exact scope must equal the stored scope (cross-tenant invisibility — OBJECT_NOT_FOUND otherwise). The stored bytes are re-hashed on every read; corruption fails closed. Returns the metadata plus the artifact bytes as base64.",
			"inputSchema": objectSchema(map[string]any{
				"objectId": stringSchema("content address (64 lowercase hex SHA-256 digits)"),
				"scope":    stringSchema(`JSON exact company scope of the object`),
			}, "objectId", "scope"),
		},
		// ── accounting_retention_policy_* (v0.8 batch 2, design §3.1/§6/§9): the
		// narrow retention-policy surface — put is the AUTHENTICATED administration
		// mutation (fails closed with AUTHENTICATION_REQUIRED on this session-less
		// stdio server, exactly like accounting_approve — tool arguments NEVER supply
		// identity); resolve/evaluate are SCOPE-FIRST READS whose exact scope tuple is
		// part of the arguments, so a caller whose scope differs sees matched=false /
		// UNKNOWN_RETENTION_STATE — never the policy. NO holds, NO purge, NO export,
		// NO deletion, NO scheduling.
		{
			"name":        "accounting_retention_policy_put",
			"description": "Put ONE immutable retention-policy version (v0.8 batch 2): the authenticated administration gate (deny-list first, then records_compliance_officer | tenant_records_owner, assurance ≥ standard, tenant match), (tenant, requestId) idempotency and the expected-version supersession guard. Requires an authenticated session binding; tool arguments NEVER supply identity. The current stdio MCP server has no session binding, so the tool fails closed with AUTHENTICATION_REQUIRED. NO receipt is emitted (a policy put is not an object-chain act).",
			"inputSchema": objectSchema(map[string]any{
				"scope":                  stringSchema(`JSON exact company scope: {"kind":"company","organizationId":"...","companyId":"...","ruc":"...","period":"YYYYMM"} (companyId/ruc/period empty = tenant-level policy)`),
				"jurisdiction":           stringSchema("uppercase jurisdiction token ^[A-Z][A-Z0-9-]{1,15}$ (required)"),
				"legislation":            stringSchema("regime/family identifier (required)"),
				"authority":              stringSchema("policy owner/issuer (required)"),
				"source":                 stringSchema("who decided, when, on what basis (required)"),
				"category":               stringSchema("retention category (required)"),
				"min_period":             stringSchema("deployment-declared YYYYMM retention floor (required; NO statutory duration claim)"),
				"expected_version":       intSchema("version of the current chain head the caller reviewed; 0 = none (default 0)"),
				"dual_approval_required": boolSchema("require a second dual-approval role (default false)"),
				"dual_approver_roles":    map[string]any{"type": "array", "items": stringSchema("controller|tax_responsible"), "description": "canonical sorted subset of the closed enum (default controller,tax_responsible)"},
				"blocking_hold_kinds":    map[string]any{"type": "array", "items": stringSchema("legal|audit|dispute|fiscalization|other"), "description": "canonical sorted subset of the closed enum (default legal,audit,dispute,fiscalization)"},
				"enabled":                boolSchema("enable the policy for resolution (default false)"),
				"request_id":             stringSchema("tenant-scoped idempotency key (required)"),
			}, "scope", "jurisdiction", "legislation", "authority", "source", "category", "min_period", "request_id"),
		},
		{
			"name":        "accounting_retention_policy_resolve",
			"description": "SCOPE-FIRST exact retention resolution (v0.8 batch 2): the exact scope tuple + (jurisdiction, legislation, category) against the HIGHEST version of an ENABLED policy. matched=false when no exact active policy resolves (a caller whose exact scope differs sees matched=false — cross-tenant invisibility); ambiguity fails closed with RETENTION_POLICY_AMBIGUOUS. Pure read — never deletes, no statutory duration claim.",
			"inputSchema": objectSchema(map[string]any{
				"scope":        stringSchema(`JSON exact company scope (required)`),
				"jurisdiction": stringSchema("uppercase jurisdiction token (required)"),
				"legislation":  stringSchema("regime/family identifier (required)"),
				"category":     stringSchema("retention category (required)"),
			}, "scope", "jurisdiction", "legislation", "category"),
		},
		{
			"name":        "accounting_retention_policy_evaluate",
			"description": "Fail-closed purge-eligibility read (v0.8 batch 2): UNKNOWN_RETENTION_STATE unless an exact active policy resolves; otherwise the pure eligible|not_due dimension of the object's YYYYMM period vs the deployment-declared min_period floor. Read-only — never deletes, never schedules, no statutory duration claim.",
			"inputSchema": objectSchema(map[string]any{
				"scope":         stringSchema(`JSON exact company scope (required)`),
				"jurisdiction":  stringSchema("uppercase jurisdiction token (required)"),
				"legislation":   stringSchema("regime/family identifier (required)"),
				"category":      stringSchema("retention category (required)"),
				"object_period": stringSchema("the object's fiscal period YYYYMM (required)"),
			}, "scope", "jurisdiction", "legislation", "category", "object_period"),
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

	case "accounting_object_store":
		// v0.7.0 EvidenceObject capture — STORE ONLY, never approval. The
		// artifact bytes arrive as standard padded base64; the exact company
		// scope and the capture provenance are explicit arguments.
		var args struct {
			BytesB64    string `json:"bytesB64"`
			ContentType string `json:"contentType"`
			Scope       string `json:"scope"`
			Source      string `json:"source"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("bytesB64", args.BytesB64); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		if err := requireParams("source", args.Source); err != nil {
			return nil, err
		}
		bytes, err := base64.StdEncoding.DecodeString(args.BytesB64)
		if err != nil {
			return errTextContent(errors.New("INVALID_OBJECT: bytesB64 must be standard padded base64")), nil
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		var source core.Source
		if err := json.Unmarshal([]byte(args.Source), &source); err != nil {
			return errTextContent(fmt.Errorf("INVALID_OBJECT: source must be JSON: %w", err)), nil
		}
		result, err := m.api.StoreObject(context.Background(), core.ObjectStoreInput{
			Bytes:       bytes,
			ContentType: args.ContentType,
			Scope:       scope,
			Source:      source,
		})
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(result)), nil

	case "accounting_object_get":
		// v0.7.0 EvidenceObject read — SCOPE-FIRST: the caller's exact scope
		// must equal the stored scope (OBJECT_NOT_FOUND otherwise); the stored
		// bytes are re-hashed on every read.
		var args struct {
			ObjectID string `json:"objectId"`
			Scope    string `json:"scope"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("objectId", args.ObjectID); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		obj, bytes, err := m.api.GetObject(context.Background(), args.ObjectID, scope)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(map[string]any{
			"object":   obj,
			"bytesB64": base64.StdEncoding.EncodeToString(bytes),
		})), nil

	// ── retention-policy tools (v0.8 batch 2, design §3.1/§6/§9): put is the
	// AUTHENTICATED administration mutation and fails closed with
	// AUTHENTICATION_REQUIRED on this session-less stdio server (exactly like
	// accounting_approve — tool arguments NEVER supply identity); resolve and
	// evaluate are SCOPE-FIRST READS whose exact scope is part of the arguments,
	// so a caller whose scope differs sees matched=false /
	// UNKNOWN_RETENTION_STATE — never the policy.
	case "accounting_retention_policy_put":
		var args struct {
			Scope                string   `json:"scope"`
			Jurisdiction         string   `json:"jurisdiction"`
			Legislation          string   `json:"legislation"`
			Authority            string   `json:"authority"`
			Source               string   `json:"source"`
			Category             string   `json:"category"`
			MinPeriod            string   `json:"min_period"`
			ExpectedVersion      int64    `json:"expected_version"`
			DualApprovalRequired bool     `json:"dual_approval_required"`
			DualApproverRoles    []string `json:"dual_approver_roles"`
			BlockingHoldKinds    []string `json:"blocking_hold_kinds"`
			Enabled              bool     `json:"enabled"`
			RequestID            string   `json:"request_id"`
		}
		// Strict shape (design §6): ANY unknown field — including any caller-
		// supplied authority (actorId/actorKind/subjectId/roles) — is a malformed
		// argument shape (JSON-RPC -32602), never silently ignored.
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		if err := requireParams("jurisdiction", args.Jurisdiction); err != nil {
			return nil, err
		}
		if err := requireParams("legislation", args.Legislation); err != nil {
			return nil, err
		}
		if err := requireParams("authority", args.Authority); err != nil {
			return nil, err
		}
		if err := requireParams("source", args.Source); err != nil {
			return nil, err
		}
		if err := requireParams("category", args.Category); err != nil {
			return nil, err
		}
		if err := requireParams("min_period", args.MinPeriod); err != nil {
			return nil, err
		}
		if err := requireParams("request_id", args.RequestID); err != nil {
			return nil, err
		}
		// The stdio MCP server has NO authenticated session binding (design §3):
		// tool arguments NEVER supply identity, so the tool fails closed. HTTP
		// MCP may put only when the HTTP middleware supplies a bound principal to
		// the server.
		return errTextContent(auth.New(auth.CodeAuthenticationRequired,
			"accounting_retention_policy_put requires an authenticated session binding; the stdio MCP server has none — tool arguments never supply identity")), nil

	case "accounting_retention_policy_resolve":
		var args struct {
			Scope        string `json:"scope"`
			Jurisdiction string `json:"jurisdiction"`
			Legislation  string `json:"legislation"`
			Category     string `json:"category"`
		}
		// Strict shape (design §6): the tool accepts EXACTLY its four declared
		// arguments — unknown fields (including any caller-supplied identity) are
		// rejected, never silently ignored.
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		if err := requireParams("jurisdiction", args.Jurisdiction); err != nil {
			return nil, err
		}
		if err := requireParams("legislation", args.Legislation); err != nil {
			return nil, err
		}
		if err := requireParams("category", args.Category); err != nil {
			return nil, err
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		policy, matched, err := m.api.ResolveRetentionPolicy(context.Background(), scope, args.Jurisdiction, args.Legislation, args.Category)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(retentionPolicyResolveResponse{Policy: policy, Matched: matched})), nil

	case "accounting_retention_policy_evaluate":
		var args struct {
			Scope        string `json:"scope"`
			Jurisdiction string `json:"jurisdiction"`
			Legislation  string `json:"legislation"`
			Category     string `json:"category"`
			ObjectPeriod string `json:"object_period"`
		}
		// Strict shape (design §6): the tool accepts EXACTLY its five declared
		// arguments — unknown fields (including any caller-supplied identity) are
		// rejected, never silently ignored.
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		if err := requireParams("jurisdiction", args.Jurisdiction); err != nil {
			return nil, err
		}
		if err := requireParams("legislation", args.Legislation); err != nil {
			return nil, err
		}
		if err := requireParams("category", args.Category); err != nil {
			return nil, err
		}
		if err := requireParams("object_period", args.ObjectPeriod); err != nil {
			return nil, err
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		result, err := m.api.EvaluatePurgeEligibility(context.Background(), core.EvaluatePurgeEligibilityInput{
			Scope:        scope,
			Jurisdiction: args.Jurisdiction,
			Legislation:  args.Legislation,
			Category:     args.Category,
			ObjectPeriod: args.ObjectPeriod,
		})
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(result)), nil

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

	// ── close tools (v0.5.0 close foundation, design §6): creation is a NORMAL
	// SAVE by an agent (the server stamps the agent source — provenance, never
	// authority); reopening is the authenticated controller act and fails closed
	// with AUTHENTICATION_REQUIRED on this session-less stdio server (exactly
	// like accounting_approve).
	case "accounting_close_create":
		var args struct {
			Period string          `json:"period"`
			Scope  string          `json:"scope"`
			Totals []mcpCloseTotal `json:"totals"`
			Reason string          `json:"reason"`
		}
		// Strict shape: ANY unknown field is a malformed argument shape (JSON-RPC
		// -32602), never silently ignored — a body field can never supply
		// authority or scope.
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("period", args.Period); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		if scope.Period != "" && scope.Period != args.Period {
			return errTextContent(auth.New(auth.CodeInvalidTransition,
				"period argument does not match the scope period")), nil
		}
		totals := make([]core.CloseTotal, 0, len(args.Totals))
		for _, t := range args.Totals {
			totals = append(totals, core.CloseTotal{
				Code:            t.Code,
				Currency:        t.Currency,
				AmountCents:     t.AmountCents,
				SourceMemoryIDs: append([]string(nil), t.SourceMemoryIDs...),
			})
		}
		// The agent source claim is stamped here (provenance ONLY — the close
		// creation is a normal agent save; the APPROVAL is the authenticated
		// controller act through accounting_approve).
		memory, err := CreateClose(context.Background(), m.api, scope, core.CreateCloseInput{
			Period: args.Period,
			Totals: totals,
			Reason: args.Reason,
			Source: core.Source{System: "mcp", ActorID: "drenyra-agent", ActorKind: core.ActorKindAgent},
		})
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(memory)), nil

	case "accounting_period_reopen":
		var args struct {
			Period                string `json:"period"`
			Scope                 string `json:"scope"`
			ExpectedCloseMemoryID string `json:"expected_close_memory_id"`
			Reason                string `json:"reason"`
			RequestID             string `json:"request_id"`
		}
		// Strict shape (design §6): ANY unknown field — including any caller-
		// supplied authority — is a malformed argument shape (JSON-RPC -32602),
		// never silently ignored.
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("period", args.Period); err != nil {
			return nil, err
		}
		if err := requireParams("expected_close_memory_id", args.ExpectedCloseMemoryID); err != nil {
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
		// MCP may reopen only when the HTTP middleware supplies a bound principal
		// to the server.
		return errTextContent(auth.New(auth.CodeAuthenticationRequired,
			"accounting_period_reopen requires an authenticated session binding; the stdio MCP server has none — tool arguments never supply identity")), nil

	// ── adjudication tools (v0.4.0 Step 2, design §7): proposals and
	// withdrawals carry a provenance-only source; confirm/reject have NO
	// identity arguments at all and fail closed with AUTHENTICATION_REQUIRED on
	// this session-less stdio server (exactly like accounting_approve).
	case "accounting_judgment_propose":
		var args struct {
			FromID        string `json:"from_id"`
			ToID          string `json:"to_id"`
			Relation      string `json:"relation"`
			Reason        string `json:"reason"`
			RequestID     string `json:"request_id"`
			PredecessorID string `json:"predecessor_id"`
			Source        struct {
				System    string `json:"system"`
				ActorID   string `json:"actor_id"`
				ActorKind string `json:"actor_kind"`
				Session   string `json:"session"`
			} `json:"source"`
		}
		// Strict shape: ANY unknown field — including any caller-supplied
		// authority (subjectId/roles/assurance) at top level or inside source —
		// is a malformed argument shape (JSON-RPC -32602), never ignored.
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("from_id", args.FromID); err != nil {
			return nil, err
		}
		if err := requireParams("to_id", args.ToID); err != nil {
			return nil, err
		}
		if err := requireParams("relation", args.Relation); err != nil {
			return nil, err
		}
		if err := requireParams("reason", args.Reason); err != nil {
			return nil, err
		}
		if err := requireParams("request_id", args.RequestID); err != nil {
			return nil, err
		}
		caller, err := judgmentMCPSource(args.Source.System, args.Source.ActorID, args.Source.ActorKind, args.Source.Session)
		if err != nil {
			return errTextContent(err), nil
		}
		if m.judgmentStore == nil {
			return errTextContent(auth.New(auth.CodeJudgmentNotFound, "judgment store is not available")), nil
		}
		result, err := ProposeJudgment(context.Background(), m.judgmentStore, core.ProposeJudgmentCommand{
			FromID:        args.FromID,
			ToID:          args.ToID,
			Relation:      core.Relation(args.Relation),
			Reason:        args.Reason,
			RequestID:     args.RequestID,
			PredecessorID: args.PredecessorID,
		}, caller)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(result)), nil

	case "accounting_judgment_confirm":
		var args struct {
			JudgmentID           string `json:"judgment_id"`
			Resolution           string `json:"resolution"`
			ExpectedJudgmentHash string `json:"expected_judgment_hash"`
			RequestID            string `json:"request_id"`
		}
		// Strict shape (design §6): ANY unknown field — including any caller-
		// supplied authority (actorId/actorKind/subjectId/roles) — is a malformed
		// argument shape (JSON-RPC -32602), never silently ignored.
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("judgment_id", args.JudgmentID); err != nil {
			return nil, err
		}
		if err := requireParams("resolution", args.Resolution); err != nil {
			return nil, err
		}
		if err := requireParams("expected_judgment_hash", args.ExpectedJudgmentHash); err != nil {
			return nil, err
		}
		if err := requireParams("request_id", args.RequestID); err != nil {
			return nil, err
		}
		// The stdio MCP server has NO authenticated session binding (design §3):
		// tool arguments NEVER supply identity, so the tool fails closed. HTTP
		// MCP may confirm only when the HTTP middleware supplies a bound
		// principal to the server.
		return errTextContent(auth.New(auth.CodeAuthenticationRequired,
			"accounting_judgment_confirm requires an authenticated session binding; the stdio MCP server has none — tool arguments never supply identity")), nil

	case "accounting_judgment_reject":
		var args struct {
			JudgmentID           string `json:"judgment_id"`
			Reason               string `json:"reason"`
			ExpectedJudgmentHash string `json:"expected_judgment_hash"`
			RequestID            string `json:"request_id"`
		}
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("judgment_id", args.JudgmentID); err != nil {
			return nil, err
		}
		if err := requireParams("reason", args.Reason); err != nil {
			return nil, err
		}
		if err := requireParams("expected_judgment_hash", args.ExpectedJudgmentHash); err != nil {
			return nil, err
		}
		if err := requireParams("request_id", args.RequestID); err != nil {
			return nil, err
		}
		return errTextContent(auth.New(auth.CodeAuthenticationRequired,
			"accounting_judgment_reject requires an authenticated session binding; the stdio MCP server has none — tool arguments never supply identity")), nil

	case "accounting_judgment_withdraw":
		var args struct {
			JudgmentID string `json:"judgment_id"`
			RequestID  string `json:"request_id"`
			Source     struct {
				System    string `json:"system"`
				ActorID   string `json:"actor_id"`
				ActorKind string `json:"actor_kind"`
				Session   string `json:"session"`
			} `json:"source"`
		}
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("judgment_id", args.JudgmentID); err != nil {
			return nil, err
		}
		if err := requireParams("request_id", args.RequestID); err != nil {
			return nil, err
		}
		caller, err := judgmentMCPSource(args.Source.System, args.Source.ActorID, args.Source.ActorKind, args.Source.Session)
		if err != nil {
			return errTextContent(err), nil
		}
		if m.judgmentStore == nil {
			return errTextContent(auth.New(auth.CodeJudgmentNotFound, "judgment store is not available")), nil
		}
		result, err := WithdrawJudgment(context.Background(), m.judgmentStore, core.WithdrawJudgmentCommand{
			JudgmentID: args.JudgmentID,
			RequestID:  args.RequestID,
		}, caller)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(result)), nil

	// ── first-class reconciliation tools (v0.5.0, design §3.2/§6): mirror the
	// judgment surface 1:1 — proposals and withdrawals carry a provenance-only
	// source; confirm/reject accept NO identity arguments and fail closed with
	// AUTHENTICATION_REQUIRED on this session-less stdio server. Domain amounts
	// are integer cents (int64; never floats).
	case "accounting_reconciliation_propose":
		var args struct {
			LeftMemoryID     string `json:"left_memory_id"`
			RightMemoryID    string `json:"right_memory_id"`
			Method           string `json:"method"`
			Currency         string `json:"currency"`
			LeftAmountCents  int64  `json:"left_amount_cents"`
			RightAmountCents int64  `json:"right_amount_cents"`
			ToleranceCents   int64  `json:"tolerance_cents"`
			Reason           string `json:"reason"`
			RequestID        string `json:"request_id"`
			PredecessorID    string `json:"predecessor_id"`
			Source           struct {
				System    string `json:"system"`
				ActorID   string `json:"actor_id"`
				ActorKind string `json:"actor_kind"`
				Session   string `json:"session"`
			} `json:"source"`
		}
		// Strict shape: ANY unknown field — including any caller-supplied
		// authority (subjectId/roles/assurance) at top level or inside source —
		// is a malformed argument shape (JSON-RPC -32602), never ignored.
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("left_memory_id", args.LeftMemoryID); err != nil {
			return nil, err
		}
		if err := requireParams("right_memory_id", args.RightMemoryID); err != nil {
			return nil, err
		}
		if err := requireParams("method", args.Method); err != nil {
			return nil, err
		}
		if err := requireParams("currency", args.Currency); err != nil {
			return nil, err
		}
		if err := requireParams("reason", args.Reason); err != nil {
			return nil, err
		}
		if err := requireParams("request_id", args.RequestID); err != nil {
			return nil, err
		}
		caller, err := reconciliationMCPSource(args.Source.System, args.Source.ActorID, args.Source.ActorKind, args.Source.Session)
		if err != nil {
			return errTextContent(err), nil
		}
		if m.reconciliationStore == nil {
			return errTextContent(auth.New(auth.CodeReconciliationNotFound, "reconciliation store is not available")), nil
		}
		result, err := ProposeReconciliation(context.Background(), m.reconciliationStore, core.ProposeReconciliationCommand{
			LeftMemoryID:     args.LeftMemoryID,
			RightMemoryID:    args.RightMemoryID,
			Method:           args.Method,
			Currency:         args.Currency,
			LeftAmountCents:  args.LeftAmountCents,
			RightAmountCents: args.RightAmountCents,
			ToleranceCents:   args.ToleranceCents,
			Reason:           args.Reason,
			RequestID:        args.RequestID,
			PredecessorID:    args.PredecessorID,
		}, caller)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(result)), nil

	case "accounting_reconciliation_confirm":
		var args struct {
			ReconciliationID           string `json:"reconciliation_id"`
			Resolution                 string `json:"resolution"`
			ExpectedReconciliationHash string `json:"expected_reconciliation_hash"`
			RequestID                  string `json:"request_id"`
		}
		// Strict shape (design §6): ANY unknown field — including any caller-
		// supplied authority (actorId/actorKind/subjectId/roles) — is a malformed
		// argument shape (JSON-RPC -32602), never silently ignored.
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("reconciliation_id", args.ReconciliationID); err != nil {
			return nil, err
		}
		if err := requireParams("resolution", args.Resolution); err != nil {
			return nil, err
		}
		if err := requireParams("expected_reconciliation_hash", args.ExpectedReconciliationHash); err != nil {
			return nil, err
		}
		if err := requireParams("request_id", args.RequestID); err != nil {
			return nil, err
		}
		// The stdio MCP server has NO authenticated session binding (design §3):
		// tool arguments NEVER supply identity, so the tool fails closed. HTTP
		// MCP may confirm only when the HTTP middleware supplies a bound
		// principal to the server.
		return errTextContent(auth.New(auth.CodeAuthenticationRequired,
			"accounting_reconciliation_confirm requires an authenticated session binding; the stdio MCP server has none — tool arguments never supply identity")), nil

	case "accounting_reconciliation_reject":
		var args struct {
			ReconciliationID           string `json:"reconciliation_id"`
			Reason                     string `json:"reason"`
			ExpectedReconciliationHash string `json:"expected_reconciliation_hash"`
			RequestID                  string `json:"request_id"`
		}
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("reconciliation_id", args.ReconciliationID); err != nil {
			return nil, err
		}
		if err := requireParams("reason", args.Reason); err != nil {
			return nil, err
		}
		if err := requireParams("expected_reconciliation_hash", args.ExpectedReconciliationHash); err != nil {
			return nil, err
		}
		if err := requireParams("request_id", args.RequestID); err != nil {
			return nil, err
		}
		return errTextContent(auth.New(auth.CodeAuthenticationRequired,
			"accounting_reconciliation_reject requires an authenticated session binding; the stdio MCP server has none — tool arguments never supply identity")), nil

	case "accounting_reconciliation_withdraw":
		var args struct {
			ReconciliationID string `json:"reconciliation_id"`
			RequestID        string `json:"request_id"`
			Source           struct {
				System    string `json:"system"`
				ActorID   string `json:"actor_id"`
				ActorKind string `json:"actor_kind"`
				Session   string `json:"session"`
			} `json:"source"`
		}
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("reconciliation_id", args.ReconciliationID); err != nil {
			return nil, err
		}
		if err := requireParams("request_id", args.RequestID); err != nil {
			return nil, err
		}
		caller, err := reconciliationMCPSource(args.Source.System, args.Source.ActorID, args.Source.ActorKind, args.Source.Session)
		if err != nil {
			return errTextContent(err), nil
		}
		if m.reconciliationStore == nil {
			return errTextContent(auth.New(auth.CodeReconciliationNotFound, "reconciliation store is not available")), nil
		}
		result, err := WithdrawReconciliation(context.Background(), m.reconciliationStore, core.WithdrawReconciliationCommand{
			ReconciliationID: args.ReconciliationID,
			RequestID:        args.RequestID,
		}, caller)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(result)), nil

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

	case "accounting_current_context":
		// Strict decode (design §5/§6): the tool accepts EXACTLY its one
		// declared argument — scope (a JSON company scope). Unknown fields are
		// rejected, never silently ignored.
		var args struct {
			Scope string `json:"scope"`
		}
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("scope", args.Scope); err != nil {
			return nil, err
		}
		scope, err := decodeScope(args.Scope)
		if err != nil {
			return errTextContent(err), nil
		}
		current, err := CurrentContextFor(context.Background(), m.api, scope)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(current)), nil

	case "accounting_compare_periods":
		// Strict snake_case decode (design §6): the tool accepts EXACTLY its two
		// declared arguments — from_scope and to_scope are JSON company scopes
		// (the same wire shape every scope-taking tool uses). Unknown fields are
		// rejected, never silently ignored.
		var args struct {
			FromScope string `json:"from_scope"`
			ToScope   string `json:"to_scope"`
		}
		if err := decodeArgumentsStrict(call.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireParams("from_scope", args.FromScope); err != nil {
			return nil, err
		}
		if err := requireParams("to_scope", args.ToScope); err != nil {
			return nil, err
		}
		fromScope, err := decodeScope(args.FromScope)
		if err != nil {
			return errTextContent(err), nil
		}
		toScope, err := decodeScope(args.ToScope)
		if err != nil {
			return errTextContent(err), nil
		}
		comparison, err := ComparePeriods(context.Background(), m.api, fromScope, toScope)
		if err != nil {
			return errTextContent(err), nil
		}
		return textContent(mustJSON(comparison)), nil

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

// judgmentMCPSource validates the provenance-only source object of a proposal or
// withdrawal tool call: a system is required and, when present, the actor kind
// must be a KNOWN kind. The agent|system-only gate is deliberately NOT enforced
// here: the service rejects a human (or unknown) proposer with the frozen
// PROPOSAL_UNAUTHORIZED — the same fail-closed domain decision the HTTP surface
// produces. The source is provenance continuity, NEVER professional authority
// (design §3/§7).
func judgmentMCPSource(system, actorID, actorKind, session string) (core.Source, error) {
	if strings.TrimSpace(system) == "" {
		return core.Source{}, errors.New("INVALID_SOURCE: judgment source requires a system")
	}
	kind := core.ActorKind(actorKind)
	if kind != "" && !core.IsValidActorKind(kind) {
		return core.Source{}, errors.New("INVALID_SOURCE: judgment source actor_kind must be human|agent|system")
	}
	return core.Source{System: system, ActorID: actorID, ActorKind: kind, Session: session}, nil
}

// reconciliationMCPSource validates the provenance-only source object of a
// reconciliation proposal or withdrawal tool call — the same fail-closed shape
// as judgmentMCPSource: a system is required and, when present, the actor kind
// must be a KNOWN kind. The agent|system-only gate is deliberately NOT enforced
// here: the service rejects a human (or unknown) proposer with the frozen
// PROPOSAL_UNAUTHORIZED, the same domain decision the HTTP surface produces
// (design §3.2/§7).
func reconciliationMCPSource(system, actorID, actorKind, session string) (core.Source, error) {
	if strings.TrimSpace(system) == "" {
		return core.Source{}, errors.New("INVALID_SOURCE: reconciliation source requires a system")
	}
	kind := core.ActorKind(actorKind)
	if kind != "" && !core.IsValidActorKind(kind) {
		return core.Source{}, errors.New("INVALID_SOURCE: reconciliation source actor_kind must be human|agent|system")
	}
	return core.Source{System: system, ActorID: actorID, ActorKind: kind, Session: session}, nil
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

// mcpCloseTotal is the snake_case MCP wire shape of one caller-supplied close
// total (the MCP argument vocabulary is snake_case; the engine model is
// camelCase core.CloseTotal). amount_cents is a signed int64 JSON integer —
// never float.
type mcpCloseTotal struct {
	Code            string   `json:"code"`
	Currency        string   `json:"currency"`
	AmountCents     int64    `json:"amount_cents"`
	SourceMemoryIDs []string `json:"source_memory_ids"`
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
