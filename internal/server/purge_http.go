// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the narrow HTTP surface of the v0.8 batch 4
// evidence purge pipeline (docs/architecture/evidence-lifecycle-v0.8.md
// §2/§3/§9/§10/§11/§12 — WU-4): purge REQUEST / APPROVE (order 1 and the dual
// second approval are the SAME operation — the store derives the order from the
// decision ledger) / REJECT / CANCEL / WITHDRAW / EXECUTE plus the
// READ-ONLY lifecycle EXPORT read.
//
//   - every mutation is an AUTHENTICATED principal mutation (the authenticate
//     middleware derives the pre-verified principal; the strict body can NEVER
//     declare identity — ADR-003) and rides the (tenant, requestId) idempotency
//     key in the Idempotency-Key header like the close/reopen/hold/retention
//     surfaces;
//   - the objectId/requestId identity comes from the URI; the strict body
//     carries ONLY the command evidence (jurisdiction/legislation/category/
//     expectedLifecycleHash/reason) — never identity, never scope;
//   - EXECUTE rides the (tenant, executionId) idempotency key in the
//     Idempotency-Key header (a retry after an interrupted attempt uses a FRESH
//     execution id; replaying the same id returns the stored outcome);
//   - the EXPORT read is a SCOPE-FIRST READ (requireToken only, like
//     retention-policy resolve / hold list): the caller's exact scope tuple
//     comes from the query parameters and the store enforces the
//     tenant/company/RUC/period boundary structurally; the export is a
//     READ-ONLY query that emits NO receipt and NEVER reads object bytes.
//
// Errors map through writePurgeError to the frozen status/code table (design
// §8.3): authz failures → 403, missing targets → 404, validation/evidence
// failures → 400, state blockers → 409. Every error carries ONLY the frozen
// code + message — never object bytes or private content.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// purgeRequestCommand is the strict wire shape of a purge request: the command
// minus objectId (the URI owns the object identity), minus requestId (the
// Idempotency-Key header) and minus ANY identity or scope field (the principal
// comes from the middleware; the object's exact scope comes from the stored
// evidence_objects row — never the caller). DisallowUnknownFields rejects
// caller-supplied authority and caller-supplied scope.
type purgeRequestCommand struct {
	Jurisdiction          string `json:"jurisdiction"`
	Legislation           string `json:"legislation"`
	Category              string `json:"category"`
	ExpectedLifecycleHash string `json:"expectedLifecycleHash"`
	Reason                string `json:"reason"`
}

// purgeApproveCommand is the strict wire shape of an approval: the command
// minus requestId (the URI), minus requestIdKey (the Idempotency-Key header)
// and minus ANY identity field. The same operation serves order 1 (default
// approver) and order 2 (dual second approver) — the store derives the order
// from the stored decision ledger.
type purgeApproveCommand struct {
	ExpectedLifecycleHash string `json:"expectedLifecycleHash"`
	Reason                string `json:"reason"`
}

// purgeReasonCommand is the strict wire shape of the reason-bearing closing
// commands (reject / withdraw): the reason only (requestId from the URI, the
// idempotency key from the header, the principal from the middleware).
type purgeReasonCommand struct {
	Reason string `json:"reason"`
}

// purgeExecuteCommand is the strict wire shape of an execution: the command
// minus requestId (the URI) and minus executionId (the Idempotency-Key header
// — the (tenant, executionId) idempotency key of THIS execution attempt). The
// reason is OPTIONAL for execution (the design's execute transition records the
// execution, not a judgment).
type purgeExecuteCommand struct {
	ExpectedLifecycleHash string `json:"expectedLifecycleHash"`
	Reason                string `json:"reason,omitempty"`
}

// handlePurgeRequest opens ONE purge pipeline per object (v0.8 batch 4, design
// §2/§3.3/§9/§10): the FULL blocker set (closed-period gate → exact active
// retention resolution → eligibility → active blocking hold scan → expected
// lifecycle hash) BEFORE the authenticated request gate (accounting ladder),
// (tenant, requestId) idempotency, the immutable request row (one per object),
// the retention binding and the purge_requested event + receipt all live in the
// store. The objectId comes from the URI; the strict body carries ONLY the
// resolution evidence and the reason.
func (h *HTTPServer) handlePurgeRequest(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireAuthPrincipal(w, r)
	if !ok {
		return
	}
	objectID := strings.TrimSpace(r.PathValue("objectId"))
	if objectID == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "objectId path parameter is required")
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
	var req purgeRequestCommand
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	result, err := h.api.RequestPurge(r.Context(), core.RequestPurgeCommand{
		ObjectID:              objectID,
		Jurisdiction:          req.Jurisdiction,
		Legislation:           req.Legislation,
		Category:              req.Category,
		ExpectedLifecycleHash: req.ExpectedLifecycleHash,
		Reason:                req.Reason,
		RequestID:             requestID,
	}, principal)
	if err != nil {
		writePurgeError(w, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, result)
}

// handlePurgeApprove records ONE human approval (v0.8 batch 4, design §2/§3.4/
// §8/§9): the SAME operation serves the default approver (order 1) and the
// DISTINCT dual second approver (order 2) — the store derives the order from
// the stored decision ledger, re-checks the FULL blocker set BEFORE authz
// (approval can never override a blocker) and enforces SoD (approver ≠
// requester) plus the distinct-principal rule store-side. The requestId comes
// from the URI; the Idempotency-Key header is THIS approval act's (tenant,
// requestIdKey) key.
func (h *HTTPServer) handlePurgeApprove(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireAuthPrincipal(w, r)
	if !ok {
		return
	}
	requestID := strings.TrimSpace(r.PathValue("requestId"))
	if requestID == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "requestId path parameter is required")
		return
	}
	requestIDKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if requestIDKey == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "Idempotency-Key header is required")
		return
	}
	body, err := readBounded(r)
	if err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "request body exceeds the limit")
		return
	}
	var req purgeApproveCommand
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	result, err := h.api.ApprovePurge(r.Context(), core.ApprovePurgeCommand{
		RequestID:             requestID,
		ExpectedLifecycleHash: req.ExpectedLifecycleHash,
		Reason:                req.Reason,
		RequestIDKey:          requestIDKey,
	}, principal)
	if err != nil {
		writePurgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handlePurgeReject records the TERMINAL rejection (design §2): an
// authenticated default approver closes the request with a reason; the
// projection moves to purge_rejected and never re-opens. The requestId comes
// from the URI; the strict body carries ONLY the reason.
func (h *HTTPServer) handlePurgeReject(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireAuthPrincipal(w, r)
	if !ok {
		return
	}
	requestID := strings.TrimSpace(r.PathValue("requestId"))
	if requestID == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "requestId path parameter is required")
		return
	}
	requestIDKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if requestIDKey == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "Idempotency-Key header is required")
		return
	}
	body, err := readBounded(r)
	if err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "request body exceeds the limit")
		return
	}
	var req purgeReasonCommand
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	result, err := h.api.RejectPurge(r.Context(), core.RejectPurgeCommand{
		RequestID:    requestID,
		Reason:       req.Reason,
		RequestIDKey: requestIDKey,
	}, principal)
	if err != nil {
		writePurgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handlePurgeCancel is the ORIGINAL requester's idempotent retraction (design
// §2): the pipeline returns to stored and a fresh request is a fresh act on
// the same one-per-object row. The requestId comes from the URI; there is NO
// body (the strict body of a cancellation is empty).
func (h *HTTPServer) handlePurgeCancel(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireAuthPrincipal(w, r)
	if !ok {
		return
	}
	requestID := strings.TrimSpace(r.PathValue("requestId"))
	if requestID == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "requestId path parameter is required")
		return
	}
	requestIDKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if requestIDKey == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "Idempotency-Key header is required")
		return
	}
	if body, err := readBounded(r); err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "request body exceeds the limit")
		return
	} else if len(bytes.TrimSpace(body)) != 0 {
		// A cancellation carries NO command evidence: a non-empty body is a
		// malformed request (DisallowUnknownFields-equivalent fail closed).
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "a purge cancellation carries no body")
		return
	}
	result, err := h.api.CancelPurge(r.Context(), core.CancelPurgeCommand{
		RequestID:    requestID,
		RequestIDKey: requestIDKey,
	}, principal)
	if err != nil {
		writePurgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handlePurgeWithdraw is the approval retraction (design §2/§7): a default
// approver or dual second approver withdraws an approved pipeline with a
// reason — the documented cleanup; the pipeline returns to stored. The
// requestId comes from the URI; the strict body carries ONLY the reason.
func (h *HTTPServer) handlePurgeWithdraw(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireAuthPrincipal(w, r)
	if !ok {
		return
	}
	requestID := strings.TrimSpace(r.PathValue("requestId"))
	if requestID == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "requestId path parameter is required")
		return
	}
	requestIDKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if requestIDKey == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "Idempotency-Key header is required")
		return
	}
	body, err := readBounded(r)
	if err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "request body exceeds the limit")
		return
	}
	var req purgeReasonCommand
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	result, err := h.api.WithdrawPurge(r.Context(), core.WithdrawPurgeCommand{
		RequestID:    requestID,
		Reason:       req.Reason,
		RequestIDKey: requestIDKey,
	}, principal)
	if err != nil {
		writePurgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handlePurgeExecute physically executes an APPROVED purge pipeline (design
// §2/§3.7/§9/§11): the TWO-PHASE, RECEIPT-COVERED protocol (durable intent →
// byte removal outside SQL with the pre-removal hash check → durable
// completion) all lives in the store. The requestId comes from the URI; the
// Idempotency-Key header is the (tenant, executionId) idempotency key of THIS
// execution attempt — a retry after an interrupted attempt uses a FRESH
// execution id, replaying the same id returns the stored outcome. Only object
// bytes are removed; the immutable audit rows never change.
func (h *HTTPServer) handlePurgeExecute(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireAuthPrincipal(w, r)
	if !ok {
		return
	}
	requestID := strings.TrimSpace(r.PathValue("requestId"))
	if requestID == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "requestId path parameter is required")
		return
	}
	executionID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if executionID == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "Idempotency-Key header (the execution id) is required")
		return
	}
	body, err := readBounded(r)
	if err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "request body exceeds the limit")
		return
	}
	var req purgeExecuteCommand
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	result, err := h.api.FinalizePurge(r.Context(), core.ExecutePurgeCommand{
		RequestID:             requestID,
		ExpectedLifecycleHash: req.ExpectedLifecycleHash,
		Reason:                req.Reason,
		ExecutionID:           executionID,
	}, principal)
	if err != nil {
		writePurgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleLifecycleExport serves the READ-ONLY deterministic evidence-lifecycle
// export (v0.8 batch 4, design §12 — WU-3/WU-4): a tenant/RUC-scoped audit
// bundle for the EXACT company scope of the stored evidence
// (?companyId= + ?ruc= + ?organizationId= + optional ?period= — an empty period
// selects ALL periods of the RUC). The companyId is REQUIRED: the export
// resolves the same company scope the stored evidence rows carry (company and
// RUC are distinct scope dimensions — the export NEVER derives the company id
// from the RUC, unlike the generic reads; the caller supplies the exact tuple,
// mirroring the MCP export's scope argument). Scope is enforced structurally by
// every store query and double-checked by the fail-closed core validators; the
// export is a QUERY that emits NO receipt and NEVER reads object bytes.
// Read-only, requireToken only — no authenticated principal, no mutation
// semantics.
func (h *HTTPServer) handleLifecycleExport(w http.ResponseWriter, r *http.Request) {
	// The companyId requirement is enforced AFTER the ruc/period fail-closed
	// validation in httpQueryScope (INVALID_RUC/INVALID_PERIOD still win first),
	// and BEFORE the scope is used: the export never falls back to the ruc
	// derivation for the company id.
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("companyId")) == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID",
			"companyId query parameter is required — the export resolves the exact company scope of the stored evidence, never the RUC")
		return
	}
	bundle, err := h.api.ExportEvidenceLifecycle(r.Context(), core.EvidenceExportCriteria{Scope: scope})
	if err != nil {
		writePurgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bundle)
}

// requireAuthPrincipal is the shared authenticated-handler preamble: it returns
// the verified middleware principal, writing the frozen 401 response when the
// request carries no principal (or a rejected credential).
func requireAuthPrincipal(w http.ResponseWriter, r *http.Request) (auth.VerifiedApprovalPrincipal, bool) {
	principal, err := RequirePrincipal(r.Context())
	if err != nil {
		if rejected := AuthErrorFromContext(r.Context()); rejected != nil {
			writePurgeError(w, rejected)
			return auth.VerifiedApprovalPrincipal{}, false
		}
		writeHTTPError(w, http.StatusUnauthorized, auth.CodeAuthenticationRequired,
			"authentication required")
		return auth.VerifiedApprovalPrincipal{}, false
	}
	return principal, true
}

// writePurgeError maps the frozen v0.8 batch 4 codes to HTTP statuses (design
// §8.3): authz failures → 403, missing targets → 404, validation/evidence
// failures → 400, state blockers (UNKNOWN_RETENTION_STATE,
// RETENTION_POLICY_AMBIGUOUS, RETENTION_NOT_DUE, HOLD_ACTIVE, PERIOD_CLOSED,
// LIFECYCLE_VERSION_MISMATCH, IDEMPOTENCY_CONFLICT, ALREADY_DECIDED,
// INVALID_TRANSITION, NOT_PURGEABLE, PURGE_EXECUTION_INTERRUPTED) → 409.
// Every error carries ONLY the frozen code + message — never object bytes.
func writePurgeError(w http.ResponseWriter, err error) {
	code := auth.Code(err)
	switch code {
	case auth.CodeAuthenticationRequired, auth.CodePrincipalInvalid:
		writeHTTPError(w, http.StatusUnauthorized, code, err.Error())
		return
	case auth.CodeMembershipInactive, auth.CodeTenantScopeMismatch, auth.CodeCompanyScopeDenied,
		auth.CodeRoleNotAuthorized, auth.CodeRoleDenied, auth.CodeAssuranceTooLow,
		auth.CodeApproverIsRequester, auth.CodeDualApprovalRequired, auth.CodeSamePrincipalSecondApproval:
		writeHTTPError(w, http.StatusForbidden, code, err.Error())
		return
	case auth.CodeReasonRequired, auth.CodePolicyEvidenceRequired:
		writeHTTPError(w, http.StatusBadRequest, code, err.Error())
		return
	case auth.CodeUnknownRetentionState, auth.CodeRetentionPolicyAmbiguous,
		auth.CodeRetentionNotDue, auth.CodeHoldActive, auth.CodePeriodClosed,
		auth.CodeLifecycleVersionMismatch, auth.CodeIdempotencyConflict,
		auth.CodeAlreadyDecided, auth.CodeInvalidTransition, auth.CodeNotPurgeable:
		writeHTTPError(w, http.StatusConflict, code, err.Error())
		return
	}
	// Missing targets (OBJECT_NOT_FOUND, PURGE_REQUEST_NOT_FOUND) are 404.
	if strings.Contains(err.Error(), "OBJECT_NOT_FOUND") || strings.Contains(err.Error(), "PURGE_REQUEST_NOT_FOUND") {
		writeHTTPError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	// A REPORTED interrupted execution attempt is a conflict (the engine never
	// pretends completion; a retry runs a FRESH execution id).
	if strings.Contains(err.Error(), "PURGE_EXECUTION_INTERRUPTED") {
		writeHTTPError(w, http.StatusConflict, "PURGE_EXECUTION_INTERRUPTED", err.Error())
		return
	}
	// Validation errors are 400: the core validators return the frozen code as a
	// literal prefix on a plain error (REASON_REQUIRED, INVALID_PURGE_*), while
	// the store wraps some gates in auth.New — both shapes are mapped here.
	if strings.Contains(err.Error(), "REASON_REQUIRED") {
		writeHTTPError(w, http.StatusBadRequest, auth.CodeReasonRequired, err.Error())
		return
	}
	if strings.Contains(err.Error(), "INVALID_") {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", err.Error())
		return
	}
	if errors.Is(err, context.Canceled) {
		writeHTTPError(w, 499, "CANCELED", "request canceled")
		return
	}
	writeHTTPError(w, http.StatusInternalServerError, "INTERNAL", "purge operation failed")
}
