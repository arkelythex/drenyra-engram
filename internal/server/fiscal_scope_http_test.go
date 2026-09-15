// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the Delivery Slice 6 HTTP
// strict fiscal-scope/reviewChecks decoders (openspec/changes/
// fiscal-runtime-foundations, design.md "Public contracts > HTTP"): pure,
// no-I/O decode functions consumed by the pre-auth approval middleware.
package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// TestDecodeReviewChecksV1StrictPresence (RED->GREEN, design.md "The raw DTO
// uses presence-aware nullable booleans"): an absent object maps to both
// checks omitted; explicit true/false map to present with that value; a
// present object with only one member leaves the other omitted.
func TestDecodeReviewChecksV1StrictPresence(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want core.ReviewChecksV1
	}{
		{"absent", "", core.ReviewChecksV1{}},
		{"empty object", `{}`, core.ReviewChecksV1{}},
		{"both true", `{"evidenceInspected":true,"applicableRulesInspected":true}`,
			core.ReviewChecksV1{
				EvidenceInspected: core.ReviewAcknowledgement{Present: true, Value: true},
				RuleInspected:     core.ReviewAcknowledgement{Present: true, Value: true},
			}},
		{"both false", `{"evidenceInspected":false,"applicableRulesInspected":false}`,
			core.ReviewChecksV1{
				EvidenceInspected: core.ReviewAcknowledgement{Present: true, Value: false},
				RuleInspected:     core.ReviewAcknowledgement{Present: true, Value: false},
			}},
		{"only evidence present", `{"evidenceInspected":true}`,
			core.ReviewChecksV1{
				EvidenceInspected: core.ReviewAcknowledgement{Present: true, Value: true},
				RuleInspected:     core.ReviewAcknowledgement{Present: false, Value: false},
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var raw json.RawMessage
			if c.raw != "" {
				raw = json.RawMessage(c.raw)
			}
			got, err := decodeReviewChecksV1Strict(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got != c.want {
				t.Fatalf("decodeReviewChecksV1Strict(%q) = %+v, want %+v", c.raw, got, c.want)
			}
		})
	}
}

// TestDecodeReviewChecksV1StrictRejectsMalformed (TRIANGULATE): duplicate
// keys, unknown fields and non-boolean values all fail closed.
func TestDecodeReviewChecksV1StrictRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"duplicate key", `{"evidenceInspected":true,"evidenceInspected":false,"applicableRulesInspected":true}`},
		{"unknown field", `{"evidenceInspected":true,"applicableRulesInspected":true,"actorId":"x"}`},
		{"non-boolean value", `{"evidenceInspected":"yes","applicableRulesInspected":true}`},
		{"trailing data", `{"evidenceInspected":true}{}`},
		{"not an object", `true`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeReviewChecksV1Strict(json.RawMessage(c.raw))
			if err == nil {
				t.Fatalf("decodeReviewChecksV1Strict(%q) must fail closed", c.raw)
			}
		})
	}
}

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
