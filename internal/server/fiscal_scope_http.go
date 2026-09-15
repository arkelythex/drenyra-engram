// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the Delivery Slice 6 HTTP
// fiscal-scope adapter (openspec/changes/fiscal-runtime-foundations,
// design.md "Public contracts > HTTP" and the boundary matrix row "HTTP
// approval | Raw fiscal scope + checks | Bounded pre-auth binding middleware |
// Locked approval transaction"):
//
//   - decodeReviewChecksV1Strict is the presence-aware, strict decoder for the
//     authenticated approval body's `reviewChecks` object: an absent object or
//     member maps to Present:false; an explicit boolean maps to Present:true
//     with that value; duplicate keys, unknown fields, non-boolean values and
//     trailing data all fail closed. It never defaults an omitted value into a
//     successful declaration (proposal.md).
//   - writeFiscalScopeHTTPError maps a *core.FiscalScopeError to its OWN
//     stable code (SCOPE_BINDING_REQUIRED/SCOPE_BINDING_INVALID/
//     SCOPE_MISMATCH/UNSUPPORTED_SCOPE_VERSION) in the existing httpErrorBody
//     envelope, so the HTTP boundary exposes the SAME typed vocabulary the
//     core/store/service layers use.
//   - fiscalApprovalPreAuth + validateFiscalApprovalPreAuth is the bounded
//     pre-auth middleware: it reads and strictly decodes the approval body
//     ONCE, validates any supplied fiscalScope binding and reviewChecks
//     BEFORE the authenticate() middleware runs (which performs the
//     authentication membership lookup — spec.md "RUC validation precedes
//     protected work": "...before scoped reads...authentication membership
//     lookup..."), and caches the validated value in the request context so
//     handleApprovalApprove never re-reads the body.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// decodeReviewChecksV1Strict decodes the approval body's optional
// `reviewChecks` raw JSON member into a presence-aware core.ReviewChecksV1.
// An empty/absent raw value returns the zero value (both checks omitted) —
// the same state a legacy caller that never supplies the object produces.
func decodeReviewChecksV1Strict(raw json.RawMessage) (core.ReviewChecksV1, error) {
	if len(raw) == 0 {
		return core.ReviewChecksV1{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return core.ReviewChecksV1{}, errors.New("reviewChecks must be a JSON object")
	}
	var checks core.ReviewChecksV1
	seen := make(map[string]bool, 2)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return core.ReviewChecksV1{}, errors.New("reviewChecks: malformed key")
		}
		key, ok := keyToken.(string)
		if !ok {
			return core.ReviewChecksV1{}, errors.New("reviewChecks: keys must be strings")
		}
		if seen[key] {
			return core.ReviewChecksV1{}, errors.New("reviewChecks: duplicate key " + key)
		}
		seen[key] = true
		valueToken, err := decoder.Token()
		if err != nil {
			return core.ReviewChecksV1{}, errors.New("reviewChecks: malformed value for " + key)
		}
		value, ok := valueToken.(bool)
		if !ok {
			return core.ReviewChecksV1{}, errors.New("reviewChecks: " + key + " must be a boolean")
		}
		ack := core.ReviewAcknowledgement{Present: true, Value: value}
		switch key {
		case "evidenceInspected":
			checks.EvidenceInspected = ack
		case "applicableRulesInspected":
			checks.RuleInspected = ack
		default:
			return core.ReviewChecksV1{}, errors.New("reviewChecks: unknown field " + key)
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return core.ReviewChecksV1{}, errors.New("reviewChecks: invalid object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return core.ReviewChecksV1{}, errors.New("reviewChecks: trailing JSON value")
	}
	return checks, nil
}

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

// fiscalApprovalPreAuthKey is the unexported context key for the pre-auth
// validated approval body (never a plain string — avoids collision).
type fiscalApprovalPreAuthKey struct{}

// fiscalApprovalPreAuth is the validated, cached result of the pre-auth
// approval body decode: the authenticated handler reads its fields instead of
// re-decoding the (already consumed) request body.
type fiscalApprovalPreAuth struct {
	expectedEnvelopeHash string
	reason               string
	fiscalIntent         *core.FiscalWriteIntent
	reviewChecks         core.ReviewChecksV1
}

// validateFiscalApprovalPreAuth is the bounded pre-auth middleware (design.md
// boundary matrix "HTTP approval"): it reads the body ONCE, strictly decodes
// the approvalApproveInput shape (rejecting unknown fields — including any
// caller-declared authority — exactly like the existing strict decode this
// replaces), validates an optional fiscalScope binding and the optional
// reviewChecks object, and places the result in the request context BEFORE
// calling next. It runs OUTSIDE h.authenticate, so a malformed/invalid
// binding or reviewChecks shape never reaches the authentication membership
// lookup — RUC validation precedes protected work, authentication included.
func (h *HTTPServer) validateFiscalApprovalPreAuth(next http.HandlerFunc) http.HandlerFunc {
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
		var intent *core.FiscalWriteIntent
		if len(input.FiscalScope) > 0 {
			binding, err := core.DecodeFiscalScopeV1JSON(input.FiscalScope)
			if err != nil {
				writeFiscalScopeHTTPError(w, err)
				return
			}
			intent = &core.FiscalWriteIntent{Binding: binding}
		}
		checks, err := decodeReviewChecksV1Strict(input.ReviewChecks)
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, "INVALID", err.Error())
			return
		}
		pre := &fiscalApprovalPreAuth{
			expectedEnvelopeHash: input.ExpectedEnvelopeHash,
			reason:               input.Reason,
			fiscalIntent:         intent,
			reviewChecks:         checks,
		}
		next(w, r.WithContext(context.WithValue(r.Context(), fiscalApprovalPreAuthKey{}, pre)))
	}
}
