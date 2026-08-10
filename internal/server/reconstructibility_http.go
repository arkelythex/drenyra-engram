// Reconstructibility HTTP surface — v1-readiness G-10 (design D-1/D-4). A
// deterministic READ-ONLY metric for ONE exact company scope + period: the
// route requires ALL FOUR exact-scope query fields (?organizationId=&companyId=
// &ruc=&period=) — it NEVER applies the generic HTTP companyId := ruc fallback,
// so an apparently precise baseline can never query an inferred company identity
// (cross-tenant aggregation is forbidden, IR-2). The shared token guard only
// (requireToken): no principal, no body, no approval semantics — the result is
// an observation (IR-3). Mounted before the generic /v1/* routes in http.go.
package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// handleReconstructibility serves GET /accounting/reconstructibility: it parses
// the exact scope from the query with a DEDICATED parser (all four fields
// required + valid), then delegates to the canonical API method. Invalid scopes
// fail closed with the frozen G-10 codes (400); a reader-contract violation or
// an unavailable/corrupt read is a report-building failure (500) — never a
// business reason.
func (h *HTTPServer) handleReconstructibility(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	scope := core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: query.Get("organizationId"),
		CompanyID:      query.Get("companyId"),
		RUC:            query.Get("ruc"),
		Period:         query.Get("period"),
	}
	if err := validateReconstructibilityAdapterScope(scope); err != nil {
		writeHTTPError(w, http.StatusBadRequest, reconstructibilityCode(err), err.Error())
		return
	}
	result, err := h.api.Reconstructibility(r.Context(), scope)
	if err != nil {
		writeReconstructibilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// validateReconstructibilityAdapterScope is the adapter-layer fail-closed scope
// check (design D-1): the metric takes EXACTLY one company scope with all four
// exact-scope fields present and well-formed. It emits the frozen G-10 codes so
// the transports never leak a generic INVALID code. The service re-validates
// the same scope defensively (second line, unchanged semantics).
func validateReconstructibilityAdapterScope(scope core.Scope) error {
	if scope.Kind != core.ScopeKindCompany || scope.OrganizationID == "" || scope.CompanyID == "" || scope.Period == "" {
		return errors.New("INVALID_RECONSTRUCTIBILITY_SCOPE: the metric requires an exact company scope with organizationId, companyId, an 11-digit ruc and a YYYYMM period")
	}
	if !core.IsValidRUC(scope.RUC) {
		return errors.New("INVALID_RECONSTRUCTIBILITY_SCOPE: ruc must be exactly 11 digits")
	}
	if !core.IsValidPeriod(scope.Period) {
		return errors.New("INVALID_PERIOD: period must be YYYYMM with month 01-12")
	}
	return nil
}

// reconstructibilityCode returns the frozen reconstructibility-surface error
// code carried by err, or "". It is consulted BEFORE the generic classifier so
// the G-10 codes keep their frozen identity on the HTTP route: INVALID_* → 400,
// RECONSTRUCTIBILITY_* → 500.
func reconstructibilityCode(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, code := range []string{
		"INVALID_RECONSTRUCTIBILITY_SCOPE", "INVALID_PERIOD",
		"RECONSTRUCTIBILITY_SCOPE_MISMATCH", "RECONSTRUCTIBILITY_UNAVAILABLE",
	} {
		if strings.HasPrefix(msg, code) {
			return code
		}
	}
	return ""
}

// writeReconstructibilityError maps a G-10 domain error to its frozen HTTP
// contract (design D-1): INVALID_RECONSTRUCTIBILITY_SCOPE / INVALID_PERIOD →
// 400; RECONSTRUCTIBILITY_SCOPE_MISMATCH and RECONSTRUCTIBILITY_UNAVAILABLE →
// 500. Anything else falls through the generic classifier.
func writeReconstructibilityError(w http.ResponseWriter, err error) {
	if code := reconstructibilityCode(err); code != "" {
		status := http.StatusInternalServerError
		if strings.HasPrefix(code, "INVALID_") {
			status = http.StatusBadRequest
		}
		writeHTTPError(w, status, code, err.Error())
		return
	}
	classified := classify(err)
	writeHTTPError(w, classified.status, classified.code, err.Error())
}
