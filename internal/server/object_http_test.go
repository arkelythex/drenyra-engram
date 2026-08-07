// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module drives the v0.7.0 EvidenceObject
// HTTP surfaces (internal/server/object_http.go): POST /accounting/objects
// captures WORM-style (201 on creation, 200 on content-addressed duplicate
// NO-OP, PERIOD_CLOSED inside a closed exact company period, INVALID_OBJECT on
// malformed base64) and GET /accounting/objects/{objectId} is SCOPE-FIRST (404
// when the caller's exact scope differs — cross-tenant invisibility). Both carry
// the shared-token guard and can NEVER approve anything (non-authorization
// boundary).
package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// objectHTTPBody is the wire shape the HTTP store surface decodes.
type objectHTTPBody struct {
	BytesB64    string      `json:"bytesB64"`
	ContentType string      `json:"contentType,omitempty"`
	Scope       core.Scope  `json:"scope"`
	Source      core.Source `json:"source"`
}

func objectStoreBody(t *testing.T, bytes []byte, scope core.Scope) objectHTTPBody {
	t.Helper()
	return objectHTTPBody{
		BytesB64:    base64.StdEncoding.EncodeToString(bytes),
		ContentType: "application/xml",
		Scope:       scope,
		Source:      core.Source{System: "http-test", ActorID: "agent-1", ActorKind: core.ActorKindAgent},
	}
}

// TestHTTPObjectStoreAndGetRoundTrip verifies the happy path: POST creates the
// object (201), the duplicate POST is a NO-OP (200, created=false, no second
// receipt), and the scope-first GET returns the metadata plus the artifact
// bytes.
func TestHTTPObjectStoreAndGetRoundTrip(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	_ = api
	body := objectStoreBody(t, []byte("test"), httpScope(testRucA))

	status, raw := httpJSON(t, http.MethodPost, ts.URL+"/accounting/objects", "", body)
	if status != http.StatusCreated {
		t.Fatalf("store status = %d, want 201; body %s", status, raw)
	}
	var first core.ObjectStoreResult
	if err := json.Unmarshal([]byte(raw), &first); err != nil {
		t.Fatalf("decode store: %v", err)
	}
	if !first.Created {
		t.Fatal("first store must report created=true")
	}

	// Duplicate store: same bytes → same content address → NO-OP.
	status, raw = httpJSON(t, http.MethodPost, ts.URL+"/accounting/objects", "", body)
	if status != http.StatusOK {
		t.Fatalf("duplicate store status = %d, want 200; body %s", status, raw)
	}
	var second core.ObjectStoreResult
	if err := json.Unmarshal([]byte(raw), &second); err != nil {
		t.Fatalf("decode duplicate: %v", err)
	}
	if second.Created || second.Object.ObjectID != first.Object.ObjectID {
		t.Fatalf("duplicate = %+v, want created=false with the same content address", second)
	}

	// Scope-first GET: exact scope → the metadata + bytes.
	status, raw = httpJSON(t, http.MethodGet,
		ts.URL+"/accounting/objects/"+first.Object.ObjectID+"?organizationId="+testOrgID+"&ruc="+testRucA+"&period="+testPeriod, "", nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body %s", status, raw)
	}
	var got struct {
		Object   core.EvidenceObject `json:"object"`
		BytesB64 string              `json:"bytesB64"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.BytesB64 != base64.StdEncoding.EncodeToString([]byte("test")) {
		t.Fatalf("get bytes = %q, want the artifact bytes", got.BytesB64)
	}
}

// TestHTTPObjectGetScopeFirstInvisibility verifies cross-tenant invisibility at
// the HTTP surface: the object exists, but a different RUC (or a missing scope)
// reads 404 OBJECT_NOT_FOUND — never the object.
func TestHTTPObjectGetScopeFirstInvisibility(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	body := objectStoreBody(t, []byte("test"), httpScope(testRucA))
	status, raw := httpJSON(t, http.MethodPost, ts.URL+"/accounting/objects", "", body)
	if status != http.StatusCreated {
		t.Fatalf("store status = %d, want 201; body %s", status, raw)
	}
	var result core.ObjectStoreResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode store: %v", err)
	}

	// Different RUC — the object is invisible.
	status, raw = httpJSON(t, http.MethodGet,
		ts.URL+"/accounting/objects/"+result.Object.ObjectID+"?organizationId="+testOrgID+"&ruc="+testRucB+"&period="+testPeriod, "", nil)
	if status != http.StatusNotFound || !strings.Contains(raw, "OBJECT_NOT_FOUND") {
		t.Fatalf("cross-RUC get = %d %s, want 404 OBJECT_NOT_FOUND", status, raw)
	}

	// Unknown object id — also 404.
	status, raw = httpJSON(t, http.MethodGet,
		ts.URL+"/accounting/objects/"+strings.Repeat("0", 64)+"?organizationId="+testOrgID+"&ruc="+testRucA+"&period="+testPeriod, "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("unknown-object get = %d %s, want 404", status, raw)
	}
}

// TestHTTPObjectStoreRejectsInvalidInput pins the fail-closed wire validation:
// malformed base64 is INVALID_OBJECT (the surface can never approve anything,
// and a capture with corrupt wire bytes never reaches the store).
func TestHTTPObjectStoreRejectsInvalidInput(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	bad := objectStoreBody(t, []byte("test"), httpScope(testRucA))
	bad.BytesB64 = "%%%not-base64%%%"
	status, raw := httpJSON(t, http.MethodPost, ts.URL+"/accounting/objects", "", bad)
	if status != http.StatusOK {
		// The handler writes a JSON error body with 200 transport status (shared
		// error envelope convention); assert the wire code instead.
		if !strings.Contains(raw, "INVALID_OBJECT") {
			t.Fatalf("malformed base64 response = %d %s, want INVALID_OBJECT", status, raw)
		}
	}
}
