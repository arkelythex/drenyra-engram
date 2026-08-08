// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the narrow HTTP surface of the v0.8 batch 2
// retention-policy layer (docs/architecture/evidence-lifecycle-v0.8.md §6/§9):
// policy PUT / RESOLVE / EVALUATE ONLY — no holds, no purge, no export, no
// deletion, no scheduling.
//
//   - PUT is an AUTHENTICATED principal mutation (the authenticate middleware
//     derives the pre-verified principal; the payload can NEVER declare
//     identity — ADR-003); the (tenant, requestId) idempotency key rides the
//     Idempotency-Key header like the close/reopen surface;
//   - RESOLVE and EVALUATE are SCOPE-FIRST READS (requireToken only): the
//     caller's exact scope tuple is part of the query; a caller whose scope
//     differs sees no policy (cross-tenant invisibility);
//   - EVALUATE fails closed with UNKNOWN_RETENTION_STATE (409) unless an exact
//     active policy resolves — the engine never guesses a retention outcome,
//     makes NO statutory duration claim and NEVER auto-deletes.
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

// retentionPolicyPutRequest is the strict wire shape of a policy put: the
// command minus requestId (the Idempotency-Key header) and minus ANY identity
// field (the principal comes from the middleware). DisallowUnknownFields
// rejects caller-supplied authority.
type retentionPolicyPutRequest struct {
	Scope                core.Scope `json:"scope"`
	Jurisdiction         string     `json:"jurisdiction"`
	Legislation          string     `json:"legislation"`
	Authority            string     `json:"authority"`
	Source               string     `json:"source"`
	Category             string     `json:"category"`
	MinPeriod            string     `json:"minPeriod"`
	ExpectedVersion      int64      `json:"expectedVersion"`
	DualApprovalRequired bool       `json:"dualApprovalRequired"`
	DualApproverRoles    []string   `json:"dualApproverRoles,omitempty"`
	BlockingHoldKinds    []string   `json:"blockingHoldKinds,omitempty"`
	Enabled              bool       `json:"enabled"`
}

// retentionPolicyResolveResponse is the wire shape of a scope-first resolve:
// the exact active policy and whether it matched.
type retentionPolicyResolveResponse struct {
	Policy  core.RetentionPolicy `json:"policy"`
	Matched bool                 `json:"matched"`
}

// retentionPolicyEvaluateRequest is the strict wire shape of an eligibility
// evaluation: the exact scope, the resolution evidence and the object's
// YYYYMM period.
type retentionPolicyEvaluateRequest struct {
	Scope        core.Scope `json:"scope"`
	Jurisdiction string     `json:"jurisdiction"`
	Legislation  string     `json:"legislation"`
	Category     string     `json:"category"`
	ObjectPeriod string     `json:"objectPeriod"`
}

// handleRetentionPolicyPut writes ONE immutable retention-policy version
// (v0.8 batch 2): the authenticated administration gate, (tenant, requestId)
// idempotency and the expected-version supersession guard all live in the
// store. NO receipt is emitted (a policy put is not an object-chain act; the
// retention_bound receipt for a newly bound policy lands with object binding).
func (h *HTTPServer) handleRetentionPolicyPut(w http.ResponseWriter, r *http.Request) {
	principal, err := RequirePrincipal(r.Context())
	if err != nil {
		if rejected := AuthErrorFromContext(r.Context()); rejected != nil {
			h.writeError(w, rejected)
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
	var req retentionPolicyPutRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	result, err := h.api.PutRetentionPolicy(r.Context(), core.PutRetentionPolicyCommand{
		Scope:                req.Scope,
		Jurisdiction:         req.Jurisdiction,
		Legislation:          req.Legislation,
		Authority:            req.Authority,
		Source:               req.Source,
		Category:             req.Category,
		MinPeriod:            req.MinPeriod,
		ExpectedVersion:      req.ExpectedVersion,
		DualApprovalRequired: req.DualApprovalRequired,
		DualApproverRoles:    req.DualApproverRoles,
		BlockingHoldKinds:    req.BlockingHoldKinds,
		Enabled:              req.Enabled,
		RequestID:            requestID,
	}, principal)
	if err != nil {
		writeRetentionPolicyError(w, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, result)
}

// handleRetentionPolicyResolve serves the SCOPE-FIRST exact resolution read
// (design §6): ?jurisdiction= + ?legislation= + ?category= + the exact scope
// query parameters. A caller whose exact scope differs from the policy's scope
// sees matched=false (cross-tenant invisibility), never the policy.
func (h *HTTPServer) handleRetentionPolicyResolve(w http.ResponseWriter, r *http.Request) {
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	jurisdiction := r.URL.Query().Get("jurisdiction")
	legislation := r.URL.Query().Get("legislation")
	category := r.URL.Query().Get("category")
	if jurisdiction == "" || legislation == "" || category == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID",
			"jurisdiction, legislation and category query parameters are required")
		return
	}
	policy, matched, err := h.api.ResolveRetentionPolicy(r.Context(), scope, jurisdiction, legislation, category)
	if err != nil {
		writeRetentionPolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, retentionPolicyResolveResponse{Policy: policy, Matched: matched})
}

// handleRetentionPolicyEvaluate serves the fail-closed eligibility read:
// UNKNOWN_RETENTION_STATE (409) unless an exact active policy resolves;
// otherwise the pure eligible/not_due dimension against the
// deployment-declared min_period floor. Read-only: never deletes, never
// schedules, no statutory duration claim.
func (h *HTTPServer) handleRetentionPolicyEvaluate(w http.ResponseWriter, r *http.Request) {
	body, err := readBounded(r)
	if err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "request body exceeds the limit")
		return
	}
	var req retentionPolicyEvaluateRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	result, err := h.api.EvaluatePurgeEligibility(r.Context(), core.EvaluatePurgeEligibilityInput{
		Scope:        req.Scope,
		Jurisdiction: req.Jurisdiction,
		Legislation:  req.Legislation,
		Category:     req.Category,
		ObjectPeriod: req.ObjectPeriod,
	})
	if err != nil {
		writeRetentionPolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// writeRetentionPolicyError maps the frozen v0.8 retention codes to HTTP
// statuses (design §8.3): authz failures → 403, validation/evidence failures →
// 400, state blockers (UNKNOWN_RETENTION_STATE, RETENTION_POLICY_AMBIGUOUS,
// LIFECYCLE_VERSION_MISMATCH, IDEMPOTENCY_CONFLICT, NOT_PURGEABLE) → 409.
// Every error carries ONLY the frozen code + message — never policy content
// beyond the identifiers and never object bytes.
func writeRetentionPolicyError(w http.ResponseWriter, err error) {
	code := auth.Code(err)
	switch code {
	case auth.CodeAuthenticationRequired:
		writeHTTPError(w, http.StatusUnauthorized, code, err.Error())
		return
	case auth.CodeMembershipInactive, auth.CodeTenantScopeMismatch, auth.CodeCompanyScopeDenied,
		auth.CodeRoleNotAuthorized, auth.CodeRoleDenied, auth.CodeAssuranceTooLow:
		writeHTTPError(w, http.StatusForbidden, code, err.Error())
		return
	case auth.CodePolicyEvidenceRequired, auth.CodeReasonRequired:
		writeHTTPError(w, http.StatusBadRequest, code, err.Error())
		return
	case auth.CodeUnknownRetentionState, auth.CodeRetentionPolicyAmbiguous,
		auth.CodeRetentionNotDue, auth.CodeLifecycleVersionMismatch,
		auth.CodeIdempotencyConflict, auth.CodeNotPurgeable, auth.CodeInvalidTransition:
		writeHTTPError(w, http.StatusConflict, code, err.Error())
		return
	}
	// Validation errors (INVALID_*, INVALID_SCOPE, INVALID_RUC, ...) are 400;
	// everything else is an internal failure — never a silent success.
	if strings.Contains(err.Error(), "INVALID_") {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", err.Error())
		return
	}
	if errors.Is(err, context.Canceled) {
		writeHTTPError(w, 499, "CANCELED", "request canceled")
		return
	}
	writeHTTPError(w, http.StatusInternalServerError, "INTERNAL", "retention policy operation failed")
}
