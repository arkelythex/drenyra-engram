// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the Delivery Slice 6 HTTP
// fiscal-scope adapter (openspec/changes/fiscal-runtime-foundations,
// design.md "Public contracts > HTTP" and the boundary matrix row "HTTP
// approval | Raw fiscal scope + checks | Bounded pre-auth binding middleware |
// Locked approval transaction").
//
// It holds ONLY transport concerns: reading the bounded body, mapping typed
// fiscal errors onto HTTP codes, and composing the approval pipeline. The
// decoding RULES themselves belong to the contract and live in core
// (core.DecodeFiscalWriteIntentJSON, core.DecodeReviewChecksV1JSON) so this
// adapter cannot drift into a private, weaker parser.
//
// The pipeline is ordered validate -> authenticate -> handle, because spec.md
// requires "RUC validation precedes protected work" — the authentication
// membership lookup included. The validated body is handed to the handler as
// a TYPED ARGUMENT rather than through the request context: the composition
// is then checked by the compiler, and a handler reached without validated
// input is not an error to report at runtime, it is a program that does not
// build.
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// writeFiscalScopeHTTPError maps a fiscal-scope decode/validation error to a
// 400 response carrying its OWN typed code — SCOPE_BINDING_REQUIRED,
// SCOPE_BINDING_INVALID, SCOPE_MISMATCH or UNSUPPORTED_SCOPE_VERSION — never
// a generic INVALID. A non-fiscal error (defensive fallback; every caller of
// this helper only ever passes a *core.FiscalScopeError) still fails closed
// with a 400 INVALID rather than a 500, since the caller is always decoding
// caller-supplied input.
func writeFiscalScopeHTTPError(w http.ResponseWriter, err error) {
	var fse *core.FiscalScopeError
	if errors.As(err, &fse) {
		writeHTTPError(w, http.StatusBadRequest, fse.Code, fse.Message)
		return
	}
	writeHTTPError(w, http.StatusBadRequest, "INVALID", err.Error())
}

// approvalRequest is the VALIDATED approval body: every value here has already
// passed the strict shape decode, the binding decode and the presence-aware
// acknowledgement decode. Holding a value of this type is the proof that
// validation ran — which is why it is built in exactly one place
// (validateFiscalApprovalPreAuth) and passed by value from there.
type approvalRequest struct {
	expectedEnvelopeHash string
	reason               string
	fiscalIntent         *core.FiscalWriteIntent
	reviewChecks         core.ReviewChecksV1
}

// approvalHandler is a handler that can only run on a validated body.
type approvalHandler func(http.ResponseWriter, *http.Request, approvalRequest)

// approvalRoute composes the whole authenticated-approval pipeline in the one
// order the contract allows — bounded body read and fiscal validation FIRST,
// then authentication, then the handler — and is what the route table mounts.
// Naming the composition keeps that ordering a single reviewable statement
// instead of an invariant spread across the mux registration.
func (h *HTTPServer) approvalRoute() http.HandlerFunc {
	return h.validateFiscalApprovalPreAuth(
		func(w http.ResponseWriter, r *http.Request, approval approvalRequest) {
			h.authenticate(func(w http.ResponseWriter, r *http.Request) {
				h.handleApprovalApprove(w, r, approval)
			})(w, r)
		})
}

// validateFiscalApprovalPreAuth is the bounded pre-auth middleware (design.md
// boundary matrix "HTTP approval"): it reads the body ONCE, strictly decodes
// the approvalApproveInput shape (rejecting unknown fields — including any
// caller-declared authority — exactly like the strict decode the original
// handler performed), validates an optional fiscalScope binding and the
// optional reviewChecks object, and calls next with the validated result.
//
// It runs OUTSIDE h.authenticate, so a malformed/invalid binding or
// reviewChecks shape never reaches the authentication membership lookup. The
// work it performs before authentication is deliberately bounded and pure:
// a size-capped read plus in-memory decoding of the caller's OWN input, with
// no store, session or credential access.
func (h *HTTPServer) validateFiscalApprovalPreAuth(next approvalHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		approval := approvalRequest{
			expectedEnvelopeHash: input.ExpectedEnvelopeHash,
			reason:               input.Reason,
		}
		if len(input.FiscalScope) > 0 {
			approval.fiscalIntent, err = core.DecodeFiscalWriteIntentJSON(input.FiscalScope)
			if err != nil {
				writeFiscalScopeHTTPError(w, err)
				return
			}
		}
		if approval.reviewChecks, err = core.DecodeReviewChecksV1JSON(input.ReviewChecks); err != nil {
			writeHTTPError(w, http.StatusBadRequest, "INVALID", err.Error())
			return
		}
		next(w, r, approval)
	}
}
