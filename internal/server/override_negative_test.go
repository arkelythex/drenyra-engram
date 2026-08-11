// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test is the BEHAVIORAL half of negative
// override conformance (FR-J.3 / AC-J-3, design §J): it sends JSON and MCP
// arguments containing every forbidden override spelling through the REAL strict
// decoders of representative approval, judgment, reconciliation, review and
// evidence-lifecycle mutations and expects the existing unknown-field /
// invalid-input typed denial — a JSON-RPC -32602 on the MCP surface and a 400
// INVALID on the HTTP surface — with no state, event, receipt or idempotency
// change (asserted via a logical doctor-count digest before/after).
//
// One administrative/privileged role input (a seeded records-compliance-officer
// session — NFR-J.1) is exercised on the HTTP row to prove no implicit bypass
// exists even for an otherwise privileged caller. The structural absence is
// proven separately by TestNoOverrideFieldOnRequestSurfaces
// (no_override_surface_test.go, FR-J.2); a surface that silently accepts an
// override spelling or mutates state here is a REAL defect that stops the slice
// (never a weakened assertion).
package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// overrideSpellingKeys are the forbidden input spellings, sent as JSON/MCP
// argument keys through the strict decoders.
var overrideSpellingKeys = []string{
	"override", "break_glass", "breakglass", "force", "bypass",
}

// storeDigest is a deterministic logical digest over the store's doctor counts
// (state, events, purge lifecycle, holds, evidence). It is a logical snapshot,
// never raw SQLite bytes.
func storeDigest(t *testing.T, api *API) string {
	t.Helper()
	report, err := api.Store.Doctor(context.Background(), store.DoctorOptions{Mode: store.ModeRoutine})
	if err != nil {
		t.Fatalf("doctor digest: %v", err)
	}
	return fmt.Sprintf("obs=%d transitions=%d pending=%d events=%d objects=%d relations=%d "+
		"links=%d purgereq=%d purgeapprovals=%d holds=%d purgeidem=%d holdidem=%d retention=%d executions=%d",
		report.Observations, report.Transitions, report.PendingApprovals, report.LifecycleEvents,
		report.EvidenceObjects, report.Relations, report.EvidenceLinks, report.PurgeRequests,
		report.PurgeApprovals, report.Holds, report.PurgeIdempotencyKeys, report.HoldIdempotencyKeys,
		report.RetentionState, report.PurgeExecutions)
}

// TestOverrideInputDeniedFailClosed (AC-J-3 / FR-J.3): forbidden spellings sent
// through the real strict decoders of representative mutations fail with the
// frozen typed denial and leave the store untouched.
func TestOverrideInputDeniedFailClosed(t *testing.T) {
	t.Run("mcp-strict-decoders", func(t *testing.T) {
		m, api := newTestMCP(t)
		hash := strings.Repeat("a", 64)
		uuid := "00000000-0000-4000-8000-000000000401"
		objectID := strings.Repeat("b", 64)
		scope := `{"kind":"company","organizationId":"` + testOrgID + `","companyId":"acme","ruc":"` + testRucA + `","period":"202401"}`

		base := map[string]map[string]any{
			"accounting_approve": {
				"memory_id": "mem-1", "expected_envelope_hash": hash,
				"reason": "verify", "request_id": uuid,
			},
			"accounting_judgment_propose": {
				"from_id": "obs-1", "to_id": "obs-2", "relation": "contradicts",
				"reason": "conflict", "request_id": uuid, "predecessor_id": "",
				"source": map[string]any{"system": "mcp", "actor_id": "agent-a", "actor_kind": "agent", "session": ""},
			},
			"accounting_reconciliation_propose": {
				"left_memory_id": "obs-1", "right_memory_id": "obs-2", "method": "trial-balance",
				"currency": "PEN", "left_amount_cents": int64(100), "right_amount_cents": int64(100),
				"tolerance_cents": int64(10), "reason": "variance", "request_id": uuid, "predecessor_id": "",
				"source": map[string]any{"system": "mcp", "actor_id": "agent-a", "actor_kind": "agent", "session": ""},
			},
			"accounting_review_reject": {
				"memory_id": "mem-1", "expected_envelope_hash": hash,
				"reason": "insufficient", "request_id": uuid,
			},
			"accounting_purge_request": {
				"object_id": objectID, "jurisdiction": "PE", "legislation": "NATIONAL-TAX",
				"category": "invoice", "expected_lifecycle_hash": hash,
				"reason": "retention elapsed", "request_id": uuid,
			},
			"accounting_hold_place": {
				"object_id": objectID, "kind": "legal", "reason": "preserve",
				"owner_subject_id": "subject-1", "request_id": uuid,
			},
			"accounting_period_reopen": {
				"period": "202401", "scope": scope, "expected_close_memory_id": "close-1",
				"reason": "reopen", "request_id": uuid,
			},
			"accounting_retention_policy_put": {
				"scope": scope, "jurisdiction": "PE", "legislation": "NATIONAL-TAX",
				"authority": "tenant-records", "source": "deployment decision", "category": "invoice",
				"min_period": "202401", "expected_version": int64(0), "dual_approval_required": false,
				"dual_approver_roles": []string{}, "blocking_hold_kinds": []string{},
				"enabled": true, "request_id": uuid,
			},
			"accounting_close_create": {
				"period": "202401", "scope": scope, "totals": []any{}, "reason": "close",
			},
		}

		before := storeDigest(t, api)
		for tool, args := range base {
			for _, spelling := range overrideSpellingKeys {
				t.Run(tool+"/"+spelling, func(t *testing.T) {
					withExtra := make(map[string]any, len(args)+1)
					for k, v := range args {
						withExtra[k] = v
					}
					withExtra[spelling] = true
					response := call(t, m, 1, "tools/call", map[string]any{
						"name": tool, "arguments": withExtra,
					})
					if response.Error == nil || response.Error.Code != codeInvalidParams {
						t.Fatalf("%s with %s: want JSON-RPC -32602, got %+v", tool, spelling, response.Error)
					}
				})
			}
		}
		after := storeDigest(t, api)
		if before != after {
			t.Fatalf("MCP denied inputs mutated the store: before %s, after %s", before, after)
		}
	})

	t.Run("http-strict-decoder-with-privileged-session", func(t *testing.T) {
		// NFR-J.1: an administrative/privileged role input proves no implicit
		// bypass — a records-compliance-officer session with a valid token still
		// gets the frozen 400 INVALID for a forbidden spelling.
		ts, api := newTestHTTPServer(t, "")
		token := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
			[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})

		before := storeDigest(t, api)
		for _, spelling := range overrideSpellingKeys {
			t.Run("approve/"+spelling, func(t *testing.T) {
				body := map[string]any{
					"expectedEnvelopeHash": strings.Repeat("a", 64),
					"reason":               "verify",
					spelling:               true,
				}
				encoded := mustJSON(body)
				request, err := http.NewRequest(http.MethodPost,
					ts.URL+"/accounting/memories/mem-1/approve", bytes.NewReader([]byte(encoded)))
				if err != nil {
					t.Fatalf("new request: %v", err)
				}
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer "+token)
				request.Header.Set("Idempotency-Key", "00000000-0000-4000-8000-000000000411")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatalf("do request: %v", err)
				}
				defer func() { _ = response.Body.Close() }()
				var buffer bytes.Buffer
				if _, err := buffer.ReadFrom(response.Body); err != nil {
					t.Fatalf("read body: %v", err)
				}
				if response.StatusCode != http.StatusBadRequest {
					t.Fatalf("approve with %s: status = %d, want 400; body %s", spelling, response.StatusCode, buffer.String())
				}
				if !strings.Contains(buffer.String(), `"code":"INVALID"`) {
					t.Fatalf("approve with %s: body %s, want the frozen INVALID typed denial", spelling, buffer.String())
				}
			})
		}
		after := storeDigest(t, api)
		if before != after {
			t.Fatalf("HTTP denied inputs mutated the store: before %s, after %s", before, after)
		}
	})
}
