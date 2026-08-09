// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the F1 audit fix: on the
// AUTHENTICATED evidence-lifecycle routes (hold PLACE, hold LIFT, retention
// policy PUT) a REJECTED bearer credential must be reported as its frozen HTTP
// auth status + reason code (401 PRINCIPAL_INVALID / 401 AUTHENTICATION_REQUIRED)
// through the SHARED error writer — never a 500 INTERNAL — while a VALID
// credential that fails authorization keeps its 403 denial (the route-local
// writers stay intact). All fixtures run with OIDC enabled (the first
// Production Identity slice).
package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// TestHTTPOIDCRejectedTokenOnHoldAndRetentionRoutes: an invalid OIDC access
// token (bad signature) presented on hold PLACE, hold LIFT or the retention
// policy PUT fails closed as 401 PRINCIPAL_INVALID through the shared
// writeError/classify — the frozen auth status + reason code, never 500
// INTERNAL. A request WITHOUT a credential keeps AUTHENTICATION_REQUIRED (the
// missing-header path never fabricates a rejection).
func TestHTTPOIDCRejectedTokenOnHoldAndRetentionRoutes(t *testing.T) {
	ts, _, _, _, _, issuer, now := newHTTPOIDCServer(t)
	wrongKey := httpTestRSAKey(t)
	rejectedToken := httpOIDCToken(t, wrongKey, issuer, now, nil)

	cases := []struct {
		name   string
		method string
		url    string
		token  string
		body   any
		want   string // frozen reason code
	}{
		{
			name:   "hold place with rejected credential",
			method: http.MethodPost,
			url:    ts.URL + "/accounting/objects/" + strings.Repeat("a", 64) + "/holds",
			token:  rejectedToken,
			body:   map[string]string{"kind": "legal", "reason": "rejected fixture", "ownerSubjectId": "maria.torres"},
			want:   "PRINCIPAL_INVALID",
		},
		{
			name:   "hold lift with rejected credential",
			method: http.MethodPost,
			url:    ts.URL + "/accounting/holds/00000000-0000-4000-8000-000000000001/lift",
			token:  rejectedToken,
			body:   map[string]string{"reason": "rejected fixture"},
			want:   "PRINCIPAL_INVALID",
		},
		{
			name:   "retention policy put with rejected credential",
			method: http.MethodPost,
			url:    ts.URL + "/accounting/retention-policies",
			token:  rejectedToken,
			body: map[string]any{
				"scope":        demoScope(),
				"jurisdiction": "PE",
				"legislation":  "NATIONAL-TAX",
				"authority":    "tenant-records",
				"source":       "deployment decision 2026-08-07",
				"category":     "invoice",
				"minPeriod":    "202401",
				"enabled":      true,
			},
			want: "PRINCIPAL_INVALID",
		},
		{
			name:   "hold place without credential stays AUTHENTICATION_REQUIRED",
			method: http.MethodPost,
			url:    ts.URL + "/accounting/objects/" + strings.Repeat("b", 64) + "/holds",
			token:  "",
			body:   map[string]string{"kind": "legal", "reason": "no credential", "ownerSubjectId": "x"},
			want:   "AUTHENTICATION_REQUIRED",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			status, raw := approvalHTTP(t, tt.method, tt.url, tt.token, "oidc-rejected", tt.body)
			if status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body %s", status, raw)
			}
			if !containsCode(raw, tt.want) {
				t.Fatalf("body %q must carry the frozen code %s", raw, tt.want)
			}
			if containsCode(raw, "INTERNAL") {
				t.Fatalf("body %q must never fall through to INTERNAL", raw)
			}
		})
	}
}

// TestHTTPOIDCAuthenticatedDenialStaysForbiddenOnHoldAndRetention: a VALID
// credential (the seeded controller session) that is not authorized for the
// hold acts or retention administration keeps the frozen 403 denial with
// ROLE_NOT_AUTHORIZED — the shared classify fix must not turn real
// authorization denials into 401s or 500s on the affected routes.
func TestHTTPOIDCAuthenticatedDenialStaysForbiddenOnHoldAndRetention(t *testing.T) {
	ts, api, _, sessionToken, _, _, _ := newHTTPOIDCServer(t)
	ctx := context.Background()

	// Seed ONE evidence object in the demo scope — the hold target.
	objResult, err := api.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       []byte("hold-denial-target"),
		ContentType: "application/octet-stream",
		Scope:       demoScope(),
		Source:      testAgentSource,
	})
	if err != nil {
		t.Fatalf("store hold target object: %v", err)
	}

	// Place ONE hold through the REAL store with a records_compliance_officer
	// principal (the fixture fake of the MCP retention suite) so the controller
	// lift attempt below reaches the authorization gate.
	placed, err := api.PlaceHold(ctx, core.PlaceHoldCommand{
		ObjectID:       objResult.Object.ObjectID,
		Kind:           core.HoldKindLegal,
		Reason:         "audit in progress",
		OwnerSubjectID: "maria.torres",
		RequestID:      "req-hold-denial-fixture",
	}, retentionFixturePrincipal(t, "cmp_org", "cmp_01"))
	if err != nil {
		t.Fatalf("place hold fixture: %v", err)
	}

	cases := []struct {
		name   string
		method string
		url    string
		body   any
	}{
		{
			name:   "hold place denied for controller",
			method: http.MethodPost,
			url:    ts.URL + "/accounting/objects/" + objResult.Object.ObjectID + "/holds",
			body:   map[string]string{"kind": "legal", "reason": "controller attempt", "ownerSubjectId": "maria.torres"},
		},
		{
			name:   "hold lift denied for controller",
			method: http.MethodPost,
			url:    ts.URL + "/accounting/holds/" + placed.Hold.HoldID + "/lift",
			body:   map[string]string{"reason": "controller attempt"},
		},
		{
			name:   "retention policy put denied for controller",
			method: http.MethodPost,
			url:    ts.URL + "/accounting/retention-policies",
			body: map[string]any{
				"scope":        demoScope(),
				"jurisdiction": "PE",
				"legislation":  "NATIONAL-TAX",
				"authority":    "tenant-records",
				"source":       "deployment decision 2026-08-07",
				"category":     "invoice",
				"minPeriod":    "202401",
				"enabled":      true,
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			status, raw := approvalHTTP(t, tt.method, tt.url, sessionToken, "oidc-denied-"+tt.name, tt.body)
			if status != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (authorization denial must stay forbidden); body %s", status, raw)
			}
			if !containsCode(raw, "ROLE_NOT_AUTHORIZED") {
				t.Fatalf("body %q must carry ROLE_NOT_AUTHORIZED", raw)
			}
			if containsCode(raw, "INTERNAL") || containsCode(raw, "PRINCIPAL_INVALID") {
				t.Fatalf("body %q must not misreport the denial as INTERNAL or a credential rejection", raw)
			}
		})
	}
}
