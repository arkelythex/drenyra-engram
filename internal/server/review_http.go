// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the REVIEW WORKSPACE HTTP
// surface (v0.9.0, docs/architecture/review-workspace-v0.9.md §7): the
// scope-first queue/detail reads and the AUTHENTICATED reject/return decisions.
//
// Reads are SCOPE-FIRST (the exact scope tuple comes from ?ruc= +
// ?organizationId= + ?period= — the same derivation as the other accounting GET
// routes; reads never authorize and never require a principal). Reject/return
// are the AUTHENTICATED controller acts (ADR-003): the principal is derived ONLY
// from the Authorization credential; the strict body {expectedEnvelopeHash,
// reason} can never supply authority; the Idempotency-Key header is the
// (tenant, requestId) reservation key. Failures share the frozen approval error
// envelope (writeApprovalError/approvalErrorStatus) so SOD_VIOLATION → 403,
// REASON_REQUIRED/REVIEW_CHECKS_REQUIRED → 400 and ENVELOPE_MISMATCH → 409 carry
// the same status/code mapping as the approval surface.
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// parseQueryInt parses a strict non-negative integer query parameter (the
// pagination counters are JSON integers by contract — never a float).
func parseQueryInt(raw string) (int, error) {
	n := 0
	for _, c := range strings.TrimSpace(raw) {
		if c < '0' || c > '9' {
			return 0, errors.New("not an integer")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// handleReviewQueue serves GET /accounting/review/queue: the pending_review queue
// of the EXACT company scope derived from the query parameters (design §3 —
// scope-first, never a post-filter), deterministically ordered with bounded
// pagination (?limit= default 50 max 200, ?offset= default 0).
func (h *HTTPServer) handleReviewQueue(w http.ResponseWriter, r *http.Request) {
	if h.reviewStore == nil {
		writeHTTPError(w, http.StatusInternalServerError, "INTERNAL", "review store is not available")
		return
	}
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	limit := 0
	offset := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = parseQueryInt(raw)
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, "INVALID", "limit must be an integer")
			return
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = parseQueryInt(raw)
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, "INVALID", "offset must be an integer")
			return
		}
	}
	page, err := ReviewQueue(r.Context(), h.reviewStore, core.ReviewQueueQuery{
		Scope:  scope,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		writeReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleReviewDetail serves GET /accounting/review/{memoryId}: the composed
// review of ONE pending revision, scope-guarded (?ruc= + ?organizationId= +
// ?period=) — the full pending revision, the structured content diff, the
// evidence state with WORM availability, the best-effort rule state, the open
// proposed judgments, the review metadata and the boundary notice (design §4).
func (h *HTTPServer) handleReviewDetail(w http.ResponseWriter, r *http.Request) {
	if h.reviewStore == nil {
		writeHTTPError(w, http.StatusInternalServerError, "INTERNAL", "review store is not available")
		return
	}
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	detail, err := ReviewDetail(r.Context(), h.reviewStore, r.PathValue("memoryId"), scope)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleReviewReject serves POST /accounting/memories/{memoryId}/reject — the
// AUTHENTICATED rejection (design §5): the principal comes ONLY from the
// resolved session credential; the strict body can never supply authority.
func (h *HTTPServer) handleReviewReject(w http.ResponseWriter, r *http.Request) {
	h.handleReviewDecision(w, r, "reject")
}

// handleReviewReturn serves POST /accounting/memories/{memoryId}/return — the
// AUTHENTICATED return (design §5): same authority shape as reject; the return
// is NON-terminal (pending_review → returned) and the reason is REQUIRED (a
// correction request — the reason tells the agent what to fix).
func (h *HTTPServer) handleReviewReturn(w http.ResponseWriter, r *http.Request) {
	h.handleReviewDecision(w, r, "return")
}

// handleReviewDecision is the shared authenticated reject/return handler. The
// memory id is the single path segment between the fixed prefixes and the
// /reject|/return suffix (the mux route owns the literal suffix, so the id is
// everything between them).
func (h *HTTPServer) handleReviewDecision(w http.ResponseWriter, r *http.Request, kind string) {
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
	var input struct {
		ExpectedEnvelopeHash string `json:"expectedEnvelopeHash"`
		Reason               string `json:"reason"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	if h.reviewStore == nil {
		writeHTTPError(w, http.StatusInternalServerError, "INTERNAL", "review store is not available")
		return
	}
	memoryID := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"+kind), "/accounting/memories/")
	if kind == "return" {
		result, err := ReturnMemory(r.Context(), h.reviewStore, core.ReturnMemoryCommand{
			MemoryID:             memoryID,
			ExpectedEnvelopeHash: input.ExpectedEnvelopeHash,
			Reason:               input.Reason,
			RequestID:            requestID,
		}, principal)
		if err != nil {
			writeApprovalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	result, err := RejectMemory(r.Context(), h.reviewStore, core.RejectMemoryCommand{
		MemoryID:             memoryID,
		ExpectedEnvelopeHash: input.ExpectedEnvelopeHash,
		Reason:               input.Reason,
		RequestID:            requestID,
	}, principal)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// writeReviewError writes a review-surface error. Typed auth errors (scope
// validation, pagination bounds, memory-not-found, invalid-transition) share the
// approval error envelope; a raw domain error maps through the generic classifier.
func writeReviewError(w http.ResponseWriter, err error) {
	var e *auth.Error
	if errors.As(err, &e) {
		writeApprovalError(w, err)
		return
	}
	classified := classify(err)
	writeHTTPError(w, classified.status, classified.code, err.Error())
}
