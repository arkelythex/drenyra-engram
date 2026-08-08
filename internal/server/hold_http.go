// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the narrow HTTP surface of the v0.8 batch 3
// object-level legal-hold layer (docs/architecture/evidence-lifecycle-v0.8.md
// §3.2/§7/§9): hold PLACE / LIFT / LIST ONLY — no purge, no export, no deletion.
//
//   - PLACE and LIFT are AUTHENTICATED principal mutations (the authenticate
//     middleware derives the pre-verified principal; the strict body can NEVER
//     declare identity — ADR-003); the (tenant, requestId) idempotency key rides
//     the Idempotency-Key header like the close/reopen/retention surfaces;
//   - PLACE/LIFT deliberately BYPASS the closed-period gate (holds only PRESERVE
//     evidence — emergency placement/lift works inside a closed period);
//   - LIST is a SCOPE-FIRST READ (requireToken only): the caller's exact scope
//     tuple comes from the query parameters and must equal the object's stored
//     scope (OBJECT_NOT_FOUND otherwise — cross-tenant invisibility); the
//     response carries every hold record plus the active blocking subset.
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

// holdPlaceRequest is the strict wire shape of a hold placement: the command
// minus objectId (the URI owns the object identity), minus requestId (the
// Idempotency-Key header) and minus ANY identity field (the principal comes from
// the middleware). DisallowUnknownFields rejects caller-supplied authority and
// caller-supplied object scope.
type holdPlaceRequest struct {
	Kind           core.HoldKind `json:"kind"`
	Reason         string        `json:"reason"`
	OwnerSubjectID string        `json:"ownerSubjectId"`
}

// holdLiftRequest is the strict wire shape of a hold lift: the reason only
// (holdId comes from the URI, requestId from the header, the principal from the
// middleware).
type holdLiftRequest struct {
	Reason string `json:"reason"`
}

// holdListResponse is the wire shape of the scope-first hold list: every hold
// record of the object (placed and lifted, placement order) plus the ACTIVE
// blocking subset (the deployment's blocking kinds via ?blockingKinds=).
type holdListResponse struct {
	Holds               []core.EvidenceHold `json:"holds"`
	ActiveBlockingHolds []core.EvidenceHold `json:"activeBlockingHolds"`
}

// handleHoldPlace places ONE object-level legal hold (v0.8 batch 3): the
// authenticated preservation gate (extended evidence-lifecycle policy,
// place_hold action — deny-list first, then records_compliance_officer |
// tenant_records_owner, assurance ≥ standard, tenant/company match), (tenant,
// requestId) idempotency, the immutable evidence_holds row and the hold_placed
// receipt on the evidence_object chain all live in the store. The objectId comes
// from the URI; the strict body carries ONLY kind/reason/ownerSubjectId.
// EMERGENCY BYPASS: no closed-period gate (holds only preserve evidence).
func (h *HTTPServer) handleHoldPlace(w http.ResponseWriter, r *http.Request) {
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
	var req holdPlaceRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	result, err := h.api.PlaceHold(r.Context(), core.PlaceHoldCommand{
		ObjectID:       objectID,
		Kind:           req.Kind,
		Reason:         req.Reason,
		OwnerSubjectID: req.OwnerSubjectID,
		RequestID:      requestID,
	}, principal)
	if err != nil {
		writeHoldError(w, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, result)
}

// handleHoldLift closes ONE placed hold one-way (v0.8 batch 3): the
// authenticated lift act (lift_hold — same role matrix), (tenant, requestId)
// idempotency, the guarded one-way closure (lifted_at/lifted_by/lift_reason set
// together; ALREADY_DECIDED on a fresh lift of an already-lifted hold) and the
// hold_lifted receipt on the evidence_object chain all live in the store. The
// holdId comes from the URI; the strict body carries ONLY the lift reason.
// EMERGENCY BYPASS: no closed-period gate (holds only preserve evidence).
func (h *HTTPServer) handleHoldLift(w http.ResponseWriter, r *http.Request) {
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
	holdID := strings.TrimSpace(r.PathValue("holdId"))
	if holdID == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "holdId path parameter is required")
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
	var req holdLiftRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "parse body: "+err.Error())
		return
	}
	result, err := h.api.LiftHold(r.Context(), core.LiftHoldCommand{
		HoldID:    holdID,
		Reason:    req.Reason,
		RequestID: requestID,
	}, principal)
	if err != nil {
		writeHoldError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleHoldList serves the SCOPE-FIRST hold list (v0.8 batch 3, design §7): the
// caller's exact scope tuple comes from the query parameters
// (?ruc= + ?organizationId= + ?period=) and must equal the object's stored scope
// (OBJECT_NOT_FOUND otherwise — cross-tenant invisibility). ?blockingKinds=
// (comma-separated subset of legal,audit,dispute,fiscalization,other) selects
// the deployment's blocking set; when absent NOTHING is treated as blocking (the
// response carries an empty activeBlockingHolds). Read-only.
func (h *HTTPServer) handleHoldList(w http.ResponseWriter, r *http.Request) {
	objectID := strings.TrimSpace(r.PathValue("objectId"))
	if objectID == "" {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", "objectId path parameter is required")
		return
	}
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	blockingKinds := splitCommaList(r.URL.Query().Get("blockingKinds"))

	holds, err := h.api.HoldsForObject(r.Context(), objectID, scope)
	if err != nil {
		writeHoldError(w, err)
		return
	}
	active, err := h.api.ActiveBlockingHolds(r.Context(), objectID, scope, blockingKinds)
	if err != nil {
		writeHoldError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, holdListResponse{Holds: holds, ActiveBlockingHolds: active})
}

// splitCommaList splits a comma-separated token list (trimmed, empty tokens
// dropped) — the ?blockingKinds= wire form.
func splitCommaList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// writeHoldError maps the frozen hold/object codes to HTTP statuses (design
// §8.3): authz failures → 403, missing targets → 404, validation/evidence
// failures → 400, state blockers (ALREADY_DECIDED, IDEMPOTENCY_CONFLICT,
// HOLD_ACTIVE) → 409. Every error carries ONLY the frozen code + message — never
// hold content beyond the identifiers and never object bytes.
func writeHoldError(w http.ResponseWriter, err error) {
	code := auth.Code(err)
	switch code {
	case auth.CodeAuthenticationRequired:
		writeHTTPError(w, http.StatusUnauthorized, code, err.Error())
		return
	case auth.CodeMembershipInactive, auth.CodeTenantScopeMismatch, auth.CodeCompanyScopeDenied,
		auth.CodeRoleNotAuthorized, auth.CodeRoleDenied, auth.CodeAssuranceTooLow:
		writeHTTPError(w, http.StatusForbidden, code, err.Error())
		return
	case auth.CodeReasonRequired:
		writeHTTPError(w, http.StatusBadRequest, code, err.Error())
		return
	case auth.CodeAlreadyDecided, auth.CodeIdempotencyConflict, auth.CodeInvalidTransition:
		writeHTTPError(w, http.StatusConflict, code, err.Error())
		return
	}
	// Missing targets (OBJECT_NOT_FOUND, HOLD_NOT_FOUND) are 404.
	if strings.Contains(err.Error(), "OBJECT_NOT_FOUND") || strings.Contains(err.Error(), "HOLD_NOT_FOUND") {
		writeHTTPError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	// Validation errors (INVALID_*, REASON_REQUIRED, IDEMPOTENCY_CONFLICT as a
	// syntax guard) are 400.
	if strings.Contains(err.Error(), "INVALID_") {
		writeHTTPError(w, http.StatusBadRequest, "INVALID", err.Error())
		return
	}
	if errors.Is(err, context.Canceled) {
		writeHTTPError(w, 499, "CANCELED", "request canceled")
		return
	}
	writeHTTPError(w, http.StatusInternalServerError, "INTERNAL", "hold operation failed")
}
