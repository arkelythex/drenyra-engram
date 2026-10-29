// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the Delivery Slice 6 HTTP
// fiscal adapter (openspec/changes/fiscal-runtime-foundations, design.md
// "Public contracts > HTTP").
//
// It covers ONLY what is genuinely transport: the typed-error-to-HTTP-code
// mapping. The presence-aware decoding rules themselves are contract
// properties and are proven once in internal/core
// (review_checks_v1_json_test.go); the end-to-end effect of those rules over
// a real request is proven in http_approval_test.go.
package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// TestWriteFiscalScopeHTTPErrorMapsTypedCode (RED->GREEN): a *core.
// FiscalScopeError maps to its own stable code (SCOPE_BINDING_INVALID etc.)
// in the JSON error body — not a generic INVALID — so the HTTP boundary
// exposes the SAME typed vocabulary the store/service layers use.
func TestWriteFiscalScopeHTTPErrorMapsTypedCode(t *testing.T) {
	_, err := core.DecodeFiscalScopeV1JSON([]byte(`{"version":"v2"}`))
	if err == nil {
		t.Fatal("fixture: an unsupported version must fail to decode")
	}
	recorder := httptest.NewRecorder()
	writeFiscalScopeHTTPError(recorder, err)
	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	var body httpErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != core.UnsupportedScopeVersion {
		t.Fatalf("error code = %q, want %q", body.Error.Code, core.UnsupportedScopeVersion)
	}
}

// TestWriteFiscalScopeHTTPErrorFallsBackToInvalid: the defensive branch for a
// non-fiscal error still fails closed as a 400 INVALID — never a 500 — since
// every caller of this helper is decoding caller-supplied input.
func TestWriteFiscalScopeHTTPErrorFallsBackToInvalid(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeFiscalScopeHTTPError(recorder, errPlain("reviewChecks: unknown member actorId"))
	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	var body httpErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != "INVALID" {
		t.Fatalf("error code = %q, want INVALID", body.Error.Code)
	}
}

type errPlain string

func (e errPlain) Error() string { return string(e) }
