// Rule HTTP surfaces — Phase 6 v0.6.0, design §6. Read-only: show (current
// revision), history (full chain), impact (regulatory-change reconstruction).
// The {topic} path segment is a topic key — HTTP clients percent-encode its
// slashes (design §5: "Topic keys may contain /; HTTP clients percent-encode
// {topic}").
package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// handleRuleShow serves GET /accounting/rules/{topic} — the CURRENT rule
// revision (chain head) of the exact scope derived from ?ruc=&period=.
func (h *HTTPServer) handleRuleShow(w http.ResponseWriter, r *http.Request) {
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	mem, err := h.api.RuleShow(r.PathValue("topic"), scope)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mem)
}

// handleRuleHistory serves GET /accounting/rules/{topic}/history — the FULL
// rule chain, ordered by revision ascending.
func (h *HTTPServer) handleRuleHistory(w http.ResponseWriter, r *http.Request) {
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	chain, err := h.api.RuleHistory(r.PathValue("topic"), scope)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, chain)
}

// handleRuleImpact serves GET /accounting/rules/{topic}/impact?revision=N —
// the regulatory-change impact read. The exact scope is OPTIONAL (?ruc=&period=
// present = pinned chain; absent = chain derived from the pinned versions,
// RULE_CHAIN_AMBIGUOUS on multiple distinct chains). The tenant comes from the
// AUTHENTICATED principal (h.authenticate on the route).
func (h *HTTPServer) handleRuleImpact(w http.ResponseWriter, r *http.Request) {
	principal, err := RequirePrincipal(r.Context())
	if err != nil {
		if rejected := AuthErrorFromContext(r.Context()); rejected != nil {
			writeApprovalError(w, rejected)
			return
		}
		writeHTTPError(w, http.StatusUnauthorized, auth.CodeAuthenticationRequired, "authentication required")
		return
	}
	topic := r.PathValue("topic")
	revision := 0
	if raw := r.URL.Query().Get("revision"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeHTTPError(w, http.StatusBadRequest, "INVALID", "revision must be a positive integer")
			return
		}
		revision = n
	}
	var scope *core.Scope
	if hasScopeQuery(r) {
		s, err := httpQueryScope(r, false)
		if err != nil {
			h.writeError(w, err)
			return
		}
		scope = &s
	}
	result, err := h.api.RuleImpact(r.Context(), principal.TenantID(), topic, scope, revision)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// hasScopeQuery reports whether the request carries the exact-scope query
// parameters (ruc with period) needed to pin a rule chain.
func hasScopeQuery(r *http.Request) bool {
	return strings.TrimSpace(r.URL.Query().Get("ruc")) != "" || strings.TrimSpace(r.URL.Query().Get("period")) != ""
}
