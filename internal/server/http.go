// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module exposes the shared API as an
// HTTP REST surface; observation content is structured text with no monetary
// fields, so no money value crosses the wire.
//
// HTTP API — local REST surface over the shared domain services
// (internal/server/api.go). Same semantics as the CLI and MCP: scope is
// structural, compare verdicts and lifecycle transitions are byte-identical.
//
// Security posture (fail closed):
//   - Binds 127.0.0.1 by default — a memory engine for local agents, not a
//     public service.
//   - Optional bearer token (DRENYRA_ENGRAM_TOKEN / --token): when set, every
//     request must present Authorization: Bearer <token>. No token is
//     generated; the operator owns provisioning.
//   - Non-authorization boundary (contracts/provenance.md): there are NO
//     authorize/approve/allow endpoints. Memory guides; it never authorizes.
//
// Error model: the engine's stable codes map to HTTP statuses
// (400 validation / 404 not found / 409 state conflict / 500 internal);
// the body always carries {"error": {"code", "message"}} so clients can
// classify without scraping text.

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/search"
)

// maxBodyBytes bounds request bodies (an observation payload is small; the
// limit is a DoS guard, not a contract).
const maxBodyBytes = 1 << 20 // 1 MiB

// httpErrorBody is the JSON error envelope of every failed request.
type httpErrorBody struct {
	Error httpErrorDetail `json:"error"`
}

type httpErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// apiError is the internal error shape handlers produce; classify maps it to a
// status code.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.message }

// classify maps a domain error to its HTTP status, preserving the engine's
// stable error code as the machine-readable `code`.
func classify(err error) *apiError {
	if err == nil {
		return nil
	}
	var mapped *apiError
	if errors.As(err, &mapped) {
		return mapped
	}
	switch {
	case IsNotFound(err):
		return &apiError{status: http.StatusNotFound, code: "NOT_FOUND", message: err.Error()}
	case IsConflict(err):
		return &apiError{status: http.StatusConflict, code: "CONFLICT", message: err.Error()}
	case IsInvalid(err):
		return &apiError{status: http.StatusBadRequest, code: "INVALID", message: err.Error()}
	default:
		return &apiError{status: http.StatusInternalServerError, code: "INTERNAL", message: err.Error()}
	}
}

// HTTPServer is the HTTP REST surface over the shared API.
type HTTPServer struct {
	api   *API
	token string // optional shared bearer token; empty means no token guard
	// resolver derives the verified approval principal from Authorization
	// credentials (ADR-003). It is non-nil when api.Store implements
	// auth.SessionStore; a nil resolver fails authentication closed.
	resolver *auth.Resolver
	// approvalStore is the atomic approval surface the authenticated route
	// delegates to (one BEGIN IMMEDIATE store operation).
	approvalStore ApprovalStore
	// legacyApprove mounts the deprecated v0.3 POST /v1/observations/{id}/approve
	// route. Disabled by default (v0.5.0 removes it); daemons opt in explicitly
	// for the migration window (design section 6, resolved decision 2).
	legacyApprove bool
}

// NewHTTPServer returns an HTTPServer over api. When token is non-empty it is
// required on every request (Authorization: Bearer <token>). The authenticated
// approval route derives its principal ONLY from the resolved session credential
// — the shared token guard is NOT identity and never authorizes approval.
func NewHTTPServer(api *API, token string) *HTTPServer {
	h := &HTTPServer{api: api, token: token}
	if sessions, ok := api.Store.(auth.SessionStore); ok {
		h.resolver = &auth.Resolver{Sessions: sessions, Mode: auth.RuntimeProduction}
	}
	if approval, ok := api.Store.(ApprovalStore); ok {
		h.approvalStore = approval
	}
	return h
}

// EnableLegacyApprove mounts the deprecated POST /v1/observations/{id}/approve
// route (v0.3 adapter, disabled by default in the daemon; removed in v0.5.0).
func (h *HTTPServer) EnableLegacyApprove() *HTTPServer {
	h.legacyApprove = true
	return h
}

// SetAuthMode overrides the runtime mode used to resolve authentication
// assertions (default production). Daemons in an explicit local development
// mode pass auth.RuntimeLocalDev so local_dev sessions resolve; session and
// service_assertion credentials resolve in both modes.
func (h *HTTPServer) SetAuthMode(mode auth.RuntimeMode) *HTTPServer {
	if h.resolver != nil {
		h.resolver.Mode = mode
	}
	return h
}

// Handler returns the full route table (REST + MCP /mcp). Go 1.22+ ServeMux
// patterns select on method and path values.
func (h *HTTPServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/observations", h.requireToken(h.handleSave))
	mux.HandleFunc("GET /v1/observations/{id}", h.requireToken(h.handleGet))
	mux.HandleFunc("GET /v1/topic/{topicKey}", h.requireToken(h.handleGetByTopic))
	mux.HandleFunc("GET /v1/chain", h.requireToken(h.handleChain))
	mux.HandleFunc("GET /v1/search", h.requireToken(h.handleSearch))
	mux.HandleFunc("GET /v1/context", h.requireToken(h.handleContext))
	mux.HandleFunc("POST /v1/compare", h.requireToken(h.handleCompare))
	// Authenticated approval (v0.4.0 Step 1, ADR-003): the principal is derived
	// ONLY from the Authorization credential; the strict body can never supply
	// authority (actor/actorKind/subjectId/roles are REJECTED, never ignored).
	mux.HandleFunc("POST /accounting/memories/{memoryId}/approve", h.authenticate(h.handleApprovalApprove))
	// Deprecated v0.3 approval surface: stays compiled but is DISABLED by default
	// in the daemon (v0.5.0 removes it). Mounted only behind an explicit opt-in
	// (EnableLegacyApprove) for the migration window.
	if h.legacyApprove {
		mux.HandleFunc("POST /v1/observations/{id}/approve", h.requireToken(h.handleApprove))
	}
	mux.HandleFunc("POST /v1/observations/{id}/reject", h.requireToken(h.handleReject))
	mux.HandleFunc("POST /v1/observations/{id}/void", h.requireToken(h.handleVoid))
	mux.HandleFunc("POST /v1/observations/{id}/supersede", h.requireToken(h.handleSupersede))
	mux.HandleFunc("GET /v1/relations", h.requireToken(h.handleRelations))
	mux.HandleFunc("GET /v1/transitions", h.requireToken(h.handleTransitions))
	mux.HandleFunc("GET /v1/doctor", h.requireToken(h.handleDoctor))
	// MCP streamable-HTTP JSON mode (tools over HTTP; stdio stays the primary
	// agent transport via `drenyra-engram mcp`).
	mux.HandleFunc("POST /mcp", h.requireToken(h.handleMCP))
	return mux
}

// authenticate resolves the Authorization bearer credential ONCE per request
// (design section 3). A missing/malformed header leaves the context empty;
// a rejected credential stores the typed auth error. No silent fallback.
func (h *HTTPServer) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.resolver != nil {
			if token, ok := parseBearer(r.Header.Get("Authorization")); ok {
				principal, err := h.resolver.Authenticate(r.Context(), auth.AuthenticationAssertion{
					Method:     auth.AuthMethodSession,
					Credential: token,
				})
				if err != nil {
					r = r.WithContext(WithAuthError(r.Context(), err))
				} else {
					r = r.WithContext(WithPrincipal(r.Context(), principal))
				}
			}
		}
		next(w, r)
	}
}

// parseBearer extracts the token from an Authorization: Bearer <token> header.
func parseBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// requireToken wraps a handler with the optional bearer-token guard. When no
// token is configured the guard is a pass-through (fail closed means: when a
// token IS configured, requests without one are rejected).
func (h *HTTPServer) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.token != "" {
			header := r.Header.Get("Authorization")
			if !strings.EqualFold(header, "Bearer "+h.token) {
				writeHTTPError(w, http.StatusUnauthorized, "UNAUTHORIZED",
					"missing or invalid bearer token")
				return
			}
		}
		next(w, r)
	}
}

func (h *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	body, err := readBounded(r)
	if err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "request body exceeds 1 MiB")
		return
	}
	mcp := NewMCPServer(h.api)
	response := mcp.HandleMessage(body)
	if response == nil {
		// Notification over HTTP: MCP returns an empty 202 Accepted.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(response)
}

// ──────────────────────────────────────────────
// Handlers
// ──────────────────────────────────────────────

func (h *HTTPServer) handleSave(w http.ResponseWriter, r *http.Request) {
	var input core.SaveInput
	if !h.decodeBody(w, r, &input) {
		return
	}
	result, err := h.api.Save(input)
	if err != nil {
		h.writeError(w, err)
		return
	}
	status := http.StatusOK
	if result.Outcome == core.WriteCreated {
		status = http.StatusCreated
	}
	writeJSON(w, status, result)
}

func (h *HTTPServer) handleGet(w http.ResponseWriter, r *http.Request) {
	observation, err := h.api.Get(r.PathValue("id"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, observation)
}

func (h *HTTPServer) handleGetByTopic(w http.ResponseWriter, r *http.Request) {
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	observation, err := h.api.GetByTopic(r.PathValue("topicKey"), scope)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, observation)
}

// handleChain serves the full revision history of a (topicKey, exact scope)
// chain — every revision, ordered ascending (the counterpart of handleGetByTopic).
func (h *HTTPServer) handleChain(w http.ResponseWriter, r *http.Request) {
	topicKey := r.URL.Query().Get("topicKey")
	if strings.TrimSpace(topicKey) == "" {
		h.writeError(w, errors.New("INVALID_TOPIC_KEY: query parameter topicKey is required"))
		return
	}
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	chain, err := h.api.Chain(topicKey, scope)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, chain)
}

func (h *HTTPServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if strings.TrimSpace(query) == "" {
		h.writeError(w, errors.New("INVALID_SEARCH: query parameter q is required"))
		return
	}
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	input := search.Input{
		Query: query,
		Scope: scope,
	}
	if mode := r.URL.Query().Get("matchMode"); mode != "" {
		input.MatchMode = search.MatchMode(mode)
	}
	if raw := r.URL.Query().Get("includeInstitutional"); raw != "" {
		input.IncludeInstitutional = raw == "true" || raw == "1"
	}
	results, err := h.api.Search(input)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (h *HTTPServer) handleContext(w http.ResponseWriter, r *http.Request) {
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	observations, err := h.api.Context(scope)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, observations)
}

func (h *HTTPServer) handleCompare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDA string `json:"idA"`
		IDB string `json:"idB"`
	}
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.IDA == "" || body.IDB == "" {
		h.writeError(w, errors.New("INVALID_COMPARE: idA and idB are required"))
		return
	}
	output, err := h.api.Compare(body.IDA, body.IDB)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, output)
}

// httpSource builds the Source for an HTTP lifecycle call: the caller names an
// actor id (a professional) or the server default applies. HTTP callers are
// treated as human actors — the approval gate lives here, on the transport.
func (h *HTTPServer) httpSource(actor string) core.Source {
	if actor == "" {
		actor = h.api.DefaultActor
	}
	return core.Source{System: "http", ActorID: actor, ActorKind: core.ActorKindHuman}
}

func (h *HTTPServer) handleApprove(w http.ResponseWriter, r *http.Request) {
	h.writeGateTransition(w, r, func(id string, src core.Source) (any, error) {
		return h.api.Approve(id, src)
	})
}

func (h *HTTPServer) handleReject(w http.ResponseWriter, r *http.Request) {
	h.writeGateTransition(w, r, func(id string, src core.Source) (any, error) {
		return h.api.Reject(id, src)
	})
}

func (h *HTTPServer) handleVoid(w http.ResponseWriter, r *http.Request) {
	h.writeGateTransition(w, r, func(id string, src core.Source) (any, error) {
		return h.api.Void(id, src)
	})
}

func (h *HTTPServer) handleSupersede(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetID string `json:"targetId"`
		Actor    string `json:"actor"`
	}
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.TargetID == "" {
		h.writeError(w, errors.New("INVALID_SUPERSEDE_TARGET: targetId is required"))
		return
	}
	output, err := h.api.Supersede(r.PathValue("id"), body.TargetID, h.httpSource(body.Actor))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, output)
}

// approvalApproveInput is the STRICT approval body.
type approvalApproveInput struct {
	ExpectedEnvelopeHash string `json:"expectedEnvelopeHash"`
	Reason               string `json:"reason"`
}

// handleApprovalApprove is the authenticated approval route.
func (h *HTTPServer) handleApprovalApprove(w http.ResponseWriter, r *http.Request) {
	principal, err := RequirePrincipal(r.Context())
	if err != nil {
		if rejected := AuthErrorFromContext(r.Context()); rejected != nil {
			writeApprovalError(w, rejected)
			return
		}
		writeHTTPError(w, http.StatusUnauthorized, auth.CodeAuthenticationRequired,
			"authentication required")
		return
	}
	requestID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if requestID == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "Idempotency-Key header is required")
		return
	}
	body, err := readBounded(r)
	if err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "request body exceeds the limit")
		return
	}
	var input approvalApproveInput
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	// The mux route is POST /accounting/memories/{memoryId}/approve; the memory
	// id is the single path segment between the fixed prefixes/suffixes.
	memoryID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/accounting/memories/"), "/approve")
	if h.approvalStore == nil {
		writeHTTPError(w, http.StatusInternalServerError, "INTERNAL", "approval store is not available")
		return
	}
	cmd := core.ApproveMemoryCommand{
		MemoryID:             memoryID,
		ExpectedEnvelopeHash: input.ExpectedEnvelopeHash,
		Reason:               input.Reason,
		RequestID:            requestID,
	}
	result, err := ApproveMemory(r.Context(), h.approvalStore, authz.NewApprovalPolicy(), cmd, principal)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// envelopeMismatchBody is the ONLY approval error shape with extra fields: the
// expected and actual envelope hashes. Memory content never appears in approval
// errors (design section 6).
type envelopeMismatchBody struct {
	Error                httpErrorDetail `json:"error"`
	ExpectedEnvelopeHash string          `json:"expectedEnvelopeHash"`
	ActualEnvelopeHash   string          `json:"actualEnvelopeHash"`
}

// approvalErrorStatus maps a frozen approval code to its HTTP status (design
// section 6). An unmapped code fails closed (not ok).
func approvalErrorStatus(code string) (int, bool) {
	switch code {
	case auth.CodeAuthenticationRequired, auth.CodePrincipalInvalid:
		return http.StatusUnauthorized, true
	case auth.CodeMembershipInactive, auth.CodeTenantScopeMismatch, auth.CodeCompanyScopeDenied,
		auth.CodeRoleNotAuthorized, auth.CodeAssuranceTooLow, auth.CodeMaterialityLimitExceeded:
		return http.StatusForbidden, true
	case auth.CodeReasonRequired:
		return http.StatusBadRequest, true
	case auth.CodeMemoryNotFound:
		return http.StatusNotFound, true
	case auth.CodeInvalidTransition, auth.CodeEnvelopeMismatch, auth.CodeAlreadyDecided, auth.CodeIdempotencyConflict:
		return http.StatusConflict, true
	}
	return 0, false
}

// writeApprovalError writes the approval error envelope. Only ENVELOPE_MISMATCH
// adds expectedEnvelopeHash/actualEnvelopeHash; every other code carries just
// the frozen code and message. Never include memory content.
func writeApprovalError(w http.ResponseWriter, err error) {
	var e *auth.Error
	if !errors.As(err, &e) {
		writeHTTPError(w, http.StatusInternalServerError, "INTERNAL", "approval failed")
		return
	}
	status, ok := approvalErrorStatus(e.Code)
	if !ok {
		// Fail closed: an unmapped frozen code is a server defect, never a guess.
		writeHTTPError(w, http.StatusInternalServerError, "INTERNAL",
			"approval error code "+e.Code+" is not mapped to a transport status")
		return
	}
	if e.Code == auth.CodeEnvelopeMismatch {
		writeJSON(w, status, envelopeMismatchBody{
			Error:                httpErrorDetail{Code: e.Code, Message: e.Message},
			ExpectedEnvelopeHash: e.ExpectedEnvelopeHash,
			ActualEnvelopeHash:   e.ActualEnvelopeHash,
		})
		return
	}
	writeHTTPError(w, status, e.Code, e.Message)
}

// writeGateTransition shares the approve/reject/void handler shape: a JSON body
// with an optional actor, gated transition by path id.
func (h *HTTPServer) writeGateTransition(w http.ResponseWriter, r *http.Request, run func(id string, src core.Source) (any, error)) {
	var body struct {
		Actor string `json:"actor"`
	}
	// The body is optional (actor only); tolerate an empty body.
	if r.ContentLength != 0 {
		if !h.decodeBody(w, r, &body) {
			return
		}
	}
	output, err := run(r.PathValue("id"), h.httpSource(body.Actor))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, output)
}

func (h *HTTPServer) handleRelations(w http.ResponseWriter, r *http.Request) {
	relations, err := h.api.Relations()
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, relations)
}

func (h *HTTPServer) handleTransitions(w http.ResponseWriter, r *http.Request) {
	transitions, err := h.api.Transitions()
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, transitions)
}

func (h *HTTPServer) handleDoctor(w http.ResponseWriter, r *http.Request) {
	report, err := h.api.Doctor()
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

// httpQueryScope builds a company scope from query parameters. For kind=company
// (the default) ruc is required; kind=institutional produces an institutional
// scope. period is validated when present.
func httpQueryScope(r *http.Request, institutional bool) (core.Scope, error) {
	query := r.URL.Query()
	if institutional || query.Get("kind") == "institutional" {
		return core.Scope{Kind: core.ScopeKindInstitutional}, nil
	}
	ruc := query.Get("ruc")
	if !core.IsValidRUC(ruc) {
		return core.Scope{}, errors.New("INVALID_RUC: query parameter ruc must be exactly 11 digits")
	}
	period := query.Get("period")
	if period != "" && !core.IsValidPeriod(period) {
		return core.Scope{}, errors.New("INVALID_PERIOD: query parameter period must be YYYYMM with month 01-12")
	}
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: query.Get("organizationId"),
		CompanyID:      ruc,
		RUC:            ruc,
		Period:         period,
	}, nil
}

// decodeBody reads and unmarshals a JSON request body (bounded, fail closed on
// malformed JSON). It writes the error response itself and reports false.
func (h *HTTPServer) decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := readBounded(r)
	if err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "request body exceeds 1 MiB")
		return false
	}
	if len(body) == 0 {
		// Empty body is valid for optional-payload endpoints (e.g. a transition
		// with no actor); leave dst at its zero value.
		return true
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return false
	}
	return true
}

// readBounded reads a request body with a hard 1 MiB cap; an over-cap body is
// an error (413), never silently truncated into a partial parse.
func readBounded(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, errors.New("body too large")
	}
	return body, nil
}

func (h *HTTPServer) writeError(w http.ResponseWriter, err error) {
	mapped := classify(err)
	if mapped == nil {
		writeHTTPError(w, http.StatusInternalServerError, "INTERNAL", "unknown error")
		return
	}
	writeHTTPError(w, mapped.status, mapped.code, mapped.message)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	_ = encoder.Encode(value)
}

func writeHTTPError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, httpErrorBody{Error: httpErrorDetail{Code: code, Message: message}})
}
