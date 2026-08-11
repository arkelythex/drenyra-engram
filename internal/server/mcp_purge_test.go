// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This test freezes the v0.8 batch 4 purge MCP surface (WU-4 —
// docs/architecture/evidence-lifecycle-v0.8.md §2/§3/§9/§10/§11/§12):
//
//   - accounting_purge_request|approve|reject|cancel|withdraw|execute are the
//     AUTHENTICATED principal mutations: their strict argument shapes accept NO
//     identity field and, on this session-less stdio server, they FAIL CLOSED
//     with AUTHENTICATION_REQUIRED — the same ADR-003 contract as
//     accounting_approve (design §3/§6). approve serves BOTH the default
//     approver (order 1) and the dual second approver (order 2) — the store
//     derives the order from the decision ledger;
//   - accounting_lifecycle_export is a READ-ONLY SCOPE-FIRST read whose exact
//     scope tuple is part of the arguments (the store enforces the
//     tenant/company/RUC/period boundary structurally; the export emits NO
//     receipt and never reads object bytes).
package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// requestPurgeDirect opens ONE purge pipeline through the shared API (the MCP
// fixture path — the same domain service the MCP server delegates to) and
// returns the created request.
func requestPurgeDirect(t *testing.T, api *API, objectID, expectedHash, key string, accountantToken string) core.RequestPurgeResult {
	t.Helper()
	result, err := api.RequestPurge(context.Background(), core.RequestPurgeCommand{
		ObjectID:              objectID,
		Jurisdiction:          "PE",
		Legislation:           "NATIONAL-TAX",
		Category:              "invoice",
		ExpectedLifecycleHash: expectedHash,
		Reason:                "retention period elapsed",
		RequestID:             key,
	}, purgePrincipal(t, api, accountantToken))
	if err != nil {
		t.Fatalf("request purge fixture: %v", err)
	}
	if result.IdempotentReplay || result.Request.Status != core.PurgeRequestStatusRequested {
		t.Fatalf("fixture request = %+v, want a fresh requested pipeline", result)
	}
	return result
}

// TestMCPPurgeToolsListed: the six purge mutations plus the lifecycle export
// read are advertised in the catalog under the accounting_ namespace.
func TestMCPPurgeToolsListed(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range ToolCatalog() {
		name, _ := tool["name"].(string)
		names[name] = true
	}
	for _, want := range []string{
		"accounting_purge_request",
		"accounting_purge_approve",
		"accounting_purge_reject",
		"accounting_purge_cancel",
		"accounting_purge_withdraw",
		"accounting_purge_finalize",
		"accounting_lifecycle_export",
	} {
		if !names[want] {
			t.Fatalf("catalog missing tool %q", want)
		}
	}
}

// TestMCPPurgeMutationsFailClosedWithoutSession: every purge mutation accepts
// exactly its declared command arguments but the stdio MCP server has NO
// authenticated session binding (design §3), so each tool FAILS CLOSED with the
// frozen AUTHENTICATION_REQUIRED as an in-band tool result (isError=true) —
// never a JSON-RPC error, never a silent identity from the arguments.
func TestMCPPurgeMutationsFailClosedWithoutSession(t *testing.T) {
	m, _ := newTestMCP(t)
	hash := strings.Repeat("a", 64)
	uuid := "00000000-0000-4000-8000-000000000101"
	objectID := strings.Repeat("b", 64)

	cases := []struct {
		name string
		args map[string]any
	}{
		{"accounting_purge_request", map[string]any{
			"object_id": objectID, "jurisdiction": "PE", "legislation": "NATIONAL-TAX",
			"category": "invoice", "expected_lifecycle_hash": hash,
			"reason": "retention period elapsed", "request_id": uuid,
		}},
		{"accounting_purge_approve", map[string]any{
			"request_id": uuid, "expected_lifecycle_hash": hash,
			"reason": "verified", "request_id_key": "00000000-0000-4000-8000-000000000102",
		}},
		{"accounting_purge_reject", map[string]any{
			"request_id": uuid, "reason": "not eligible",
			"request_id_key": "00000000-0000-4000-8000-000000000103",
		}},
		{"accounting_purge_cancel", map[string]any{
			"request_id":     uuid,
			"request_id_key": "00000000-0000-4000-8000-000000000104",
		}},
		{"accounting_purge_withdraw", map[string]any{
			"request_id": uuid, "reason": "cleanup",
			"request_id_key": "00000000-0000-4000-8000-000000000105",
		}},
		{"accounting_purge_finalize", map[string]any{
			"request_id": uuid, "expected_lifecycle_hash": hash,
			"reason": "execution batch approved", "execution_id": "00000000-0000-4000-8000-000000000106",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := call(t, m, 1, "tools/call", map[string]any{
				"name": tc.name, "arguments": tc.args,
			})
			if response.Error != nil {
				t.Fatalf("domain failure must be a tool result, not a JSON-RPC error: %+v", response.Error)
			}
			var output toolCallOutput
			if err := json.Unmarshal(response.Result, &output); err != nil {
				t.Fatalf("decode tool result: %v", err)
			}
			if !output.IsError {
				t.Fatal("isError = false, want true (fail closed without a session binding)")
			}
			if len(output.Content) == 0 || !strings.Contains(output.Content[0]["text"], "AUTHENTICATION_REQUIRED") {
				t.Fatalf("error text must carry AUTHENTICATION_REQUIRED: %v", output.Content)
			}
		})
	}
}

// TestMCPPurgeMutationsRejectExtraArgs: every purge mutation parses its args
// STRICTLY (design §6): ANY unknown field — including any caller-supplied
// authority (actorId/actorKind/subjectId/roles) — is a malformed argument shape
// (JSON-RPC -32602), never silently ignored.
func TestMCPPurgeMutationsRejectExtraArgs(t *testing.T) {
	m, _ := newTestMCP(t)
	hash := strings.Repeat("a", 64)
	uuid := "00000000-0000-4000-8000-000000000201"
	objectID := strings.Repeat("b", 64)

	base := map[string]map[string]any{
		"accounting_purge_request": {
			"object_id": objectID, "jurisdiction": "PE", "legislation": "NATIONAL-TAX",
			"category": "invoice", "expected_lifecycle_hash": hash,
			"reason": "retention period elapsed", "request_id": uuid,
		},
		"accounting_purge_approve": {
			"request_id": uuid, "expected_lifecycle_hash": hash,
			"reason": "verified", "request_id_key": uuid,
		},
		"accounting_purge_reject": {
			"request_id": uuid, "reason": "not eligible", "request_id_key": uuid,
		},
		"accounting_purge_cancel": {
			"request_id": uuid, "request_id_key": uuid,
		},
		"accounting_purge_withdraw": {
			"request_id": uuid, "reason": "cleanup", "request_id_key": uuid,
		},
		"accounting_purge_finalize": {
			"request_id": uuid, "expected_lifecycle_hash": hash, "execution_id": uuid,
		},
	}
	for tool, args := range base {
		for _, extra := range []string{"actorId", "actorKind", "subjectId", "roles", "bogus"} {
			t.Run(tool+"+"+extra, func(t *testing.T) {
				withExtra := make(map[string]any, len(args)+1)
				for k, v := range args {
					withExtra[k] = v
				}
				withExtra[extra] = "lucia.ramirez"
				response := call(t, m, 1, "tools/call", map[string]any{
					"name": tool, "arguments": withExtra,
				})
				if response.Error == nil || response.Error.Code != codeInvalidParams {
					t.Fatalf("extra %s on %s: want JSON-RPC -32602, got %+v", extra, tool, response.Error)
				}
			})
		}
	}
}

// TestMCPPurgeMutationsRequireAllArgs: a missing declared argument is a shape
// error (-32602), never a silent default or a domain attempt.
func TestMCPPurgeMutationsRequireAllArgs(t *testing.T) {
	m, _ := newTestMCP(t)
	hash := strings.Repeat("a", 64)
	uuid := "00000000-0000-4000-8000-000000000301"

	cases := []struct {
		tool    string
		args    map[string]any
		missing []string
	}{
		{"accounting_purge_request", map[string]any{
			"object_id": strings.Repeat("b", 64), "jurisdiction": "PE",
			"legislation": "NATIONAL-TAX", "category": "invoice",
			"expected_lifecycle_hash": hash, "reason": "retention period elapsed",
			"request_id": uuid,
		}, []string{"object_id", "jurisdiction", "legislation", "category", "expected_lifecycle_hash", "reason", "request_id"}},
		{"accounting_purge_approve", map[string]any{
			"request_id": uuid, "expected_lifecycle_hash": hash,
			"reason": "verified", "request_id_key": uuid,
		}, []string{"request_id", "expected_lifecycle_hash", "reason", "request_id_key"}},
		{"accounting_purge_finalize", map[string]any{
			"request_id": uuid, "expected_lifecycle_hash": hash, "execution_id": uuid,
		}, []string{"request_id", "expected_lifecycle_hash", "execution_id"}},
	}
	for _, tc := range cases {
		for _, missing := range tc.missing {
			t.Run(tc.tool+"+"+missing, func(t *testing.T) {
				args := make(map[string]any, len(tc.args))
				for k, v := range tc.args {
					args[k] = v
				}
				delete(args, missing)
				response := call(t, m, 1, "tools/call", map[string]any{
					"name": tc.tool, "arguments": args,
				})
				if response.Error == nil || response.Error.Code != codeInvalidParams {
					t.Fatalf("missing %s on %s: want JSON-RPC -32602, got %+v", missing, tc.tool, response.Error)
				}
			})
		}
	}
}

// TestMCPPurgeRequestIdempotentReplay (AC-L-2, FR-L.3) freezes the replay
// contract of the MCP purge-request surface. The stdio MCP server has NO
// authenticated session binding (design §3 — tool arguments never supply
// identity), so accounting_purge_request FAILS CLOSED with
// AUTHENTICATION_REQUIRED on EVERY call: issuing the exact same tool call
// twice with the same request_id yields the IDENTICAL deterministic fail-closed
// result and ZERO state (no request row, event, receipt or idempotency key) —
// the surface can never silently create a partial reservation or a duplicate.
// The stored-outcome replay semantics live in the exact domain service the
// tool delegates to with a bound principal (m.api.RequestPurge — the same
// service requestPurgeDirect exercises): replaying the same (tenant, requestId,
// payload, principal) returns the ORIGINAL stored request with
// idempotentReplay=true and no second event/receipt. The fresh-only request
// fixture above remains but no longer stands alone.
func TestMCPPurgeRequestIdempotentReplay(t *testing.T) {
	m, api := newTestMCP(t)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	objectID, _, _, h := purgeFixture(t, api, recordsToken)
	const replayKey = "req-purge-mcp-replay-001"

	toolArgs := map[string]any{
		"object_id": objectID, "jurisdiction": "PE", "legislation": "NATIONAL-TAX",
		"category": "invoice", "expected_lifecycle_hash": h,
		"reason": "retention period elapsed", "request_id": replayKey,
	}
	before := mcpPurgeDoctorDigest(t, api)

	// The exact same tool call twice: BOTH fail closed with the frozen
	// AUTHENTICATION_REQUIRED (the stdio server has no session binding) —
	// deterministic, identical, zero state.
	var firstText, secondText string
	for i := 0; i < 2; i++ {
		response := call(t, m, 1, "tools/call", map[string]any{
			"name": "accounting_purge_request", "arguments": toolArgs,
		})
		if response.Error != nil {
			t.Fatalf("domain failure must be a tool result, not a JSON-RPC error: %+v", response.Error)
		}
		var output toolCallOutput
		if err := json.Unmarshal(response.Result, &output); err != nil {
			t.Fatalf("decode tool result: %v", err)
		}
		if !output.IsError || len(output.Content) == 0 || !strings.Contains(output.Content[0]["text"], "AUTHENTICATION_REQUIRED") {
			t.Fatalf("MCP purge request must fail closed with AUTHENTICATION_REQUIRED: %v", output.Content)
		}
		text := output.Content[0]["text"]
		if i == 0 {
			firstText = text
		} else {
			secondText = text
		}
	}
	if firstText != secondText {
		t.Fatalf("replayed fail-closed result differs: %q vs %q (must be deterministic)", firstText, secondText)
	}
	after := mcpPurgeDoctorDigest(t, api)
	if before != after {
		t.Fatalf("MCP fail-closed purge request mutated state: before %v after %v", before, after)
	}

	// The stored-outcome replay semantics live in the domain service the tool
	// delegates to with a bound principal: first call fresh, same-request replay
	// returns the ORIGINAL stored request with idempotentReplay=true and NO
	// second event/receipt.
	first := requestPurgeDirect(t, api, objectID, h, replayKey, accountantToken)
	afterFirst := mcpPurgeDoctorDigest(t, api)
	replay, err := api.RequestPurge(context.Background(), core.RequestPurgeCommand{
		ObjectID:              objectID,
		Jurisdiction:          "PE",
		Legislation:           "NATIONAL-TAX",
		Category:              "invoice",
		ExpectedLifecycleHash: h,
		Reason:                "retention period elapsed",
		RequestID:             replayKey,
	}, purgePrincipal(t, api, accountantToken))
	if err != nil {
		t.Fatalf("delegated-service replay: %v", err)
	}
	if !replay.IdempotentReplay || replay.Request.RequestID != first.Request.RequestID {
		t.Fatalf("delegated-service replay = %+v, want the stored request %s with idempotentReplay", replay, first.Request.RequestID)
	}
	afterReplay := mcpPurgeDoctorDigest(t, api)
	if afterFirst != afterReplay {
		t.Fatalf("delegated-service replay duplicated effects: before %v after %v", afterFirst, afterReplay)
	}
}

// mcpPurgeDoctorDigest is the deterministic logical zero-effect digest of the
// purge surface (doctor counts — never raw SQLite bytes).
func mcpPurgeDoctorDigest(t *testing.T, api *API) string {
	t.Helper()
	report, err := api.Doctor()
	if err != nil {
		t.Fatalf("doctor digest: %v", err)
	}
	return strings.Join([]string{
		strconv.Itoa(report.PurgeRequests), strconv.Itoa(report.PurgeApprovals), strconv.Itoa(report.LifecycleEvents),
		strconv.Itoa(report.PurgeIdempotencyKeys), strconv.Itoa(report.Holds), strconv.Itoa(report.HoldIdempotencyKeys),
		strconv.Itoa(report.Observations),
	}, "/")
}

// TestMCPLifecycleExportScopeFirst: the read-only export returns the
// deterministic bundle for the exact RUC-scoped request — the self-hashing
// manifest carries the frozen version, the applied scope, the counts and the
// content-addressed exportId, and the fixture object's metadata is present.
func TestMCPLifecycleExportScopeFirst(t *testing.T) {
	m, api := newTestMCP(t)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	objectID, _, scope, h := purgeFixture(t, api, recordsToken)
	requestPurgeDirect(t, api, objectID, h, "req-purge-mcp-export", accountantToken)

	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_lifecycle_export",
		"arguments": map[string]any{
			"scope": `{"kind":"company","organizationId":"org-acme","companyId":"acme","ruc":"20100039201","period":"202401"}`,
		},
	})
	if response.Error != nil {
		t.Fatalf("export must not be a JSON-RPC error: %+v", response.Error)
	}
	var output toolCallOutput
	if err := json.Unmarshal(response.Result, &output); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if output.IsError {
		t.Fatalf("export failed in-band: %v", output.Content)
	}
	var bundle core.EvidenceExportBundle
	if err := json.Unmarshal([]byte(output.Content[0]["text"]), &bundle); err != nil {
		t.Fatalf("decode export bundle: %v", err)
	}
	if bundle.Manifest.Version != core.EvidenceExportModelVersion {
		t.Fatalf("manifest version = %q, want %q", bundle.Manifest.Version, core.EvidenceExportModelVersion)
	}
	if !strings.HasPrefix(bundle.Manifest.ExportID, core.EvidenceExportModelVersion+":") {
		t.Fatalf("exportId = %q, want the content-addressed prefix", bundle.Manifest.ExportID)
	}
	if bundle.Manifest.Scope.OrganizationID != scope.OrganizationID || bundle.Manifest.Scope.RUC != scope.RUC {
		t.Fatalf("manifest scope = %+v, want the requested RUC scope", bundle.Manifest.Scope)
	}
	if bundle.Manifest.Counts.PurgeRequests < 1 {
		t.Fatalf("purgeRequests count = %d, want >= 1 (the fixture request)", bundle.Manifest.Counts.PurgeRequests)
	}
	found := false
	for _, o := range bundle.Objects {
		if o.Object.ObjectID == objectID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("bundle must carry the fixture object %s", objectID)
	}
}

// TestMCPLifecycleExportRequiresScope: the read accepts EXACTLY its declared
// scope argument — a missing scope is a shape error (-32602).
func TestMCPLifecycleExportRequiresScope(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_lifecycle_export", "arguments": map[string]any{},
	})
	if response.Error == nil || response.Error.Code != codeInvalidParams {
		t.Fatalf("missing scope: want JSON-RPC -32602, got %+v", response.Error)
	}
}

// TestMCPLifecycleExportRejectsExtraArgs: an extra identity/scope field is a
// malformed argument shape (-32602), never silently ignored.
func TestMCPLifecycleExportRejectsExtraArgs(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_lifecycle_export",
		"arguments": map[string]any{
			"scope":   `{"kind":"company","organizationId":"org-acme","companyId":"acme","ruc":"20100039201","period":"202401"}`,
			"actorId": "lucia.ramirez",
		},
	})
	if response.Error == nil || response.Error.Code != codeInvalidParams {
		t.Fatalf("extra actorId: want JSON-RPC -32602, got %+v", response.Error)
	}
}

// TestMCPLifecycleExportInvalidScope: a malformed scope is an in-band domain
// failure (INVALID_SCOPE / INVALID_EXPORT_SCOPE), never a JSON-RPC error and
// never a guessed scope.
func TestMCPLifecycleExportInvalidScope(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_lifecycle_export",
		"arguments": map[string]any{
			"scope": `{"kind":"company","organizationId":"org-acme","companyId":"acme","ruc":"123","period":"202401"}`,
		},
	})
	if response.Error != nil {
		t.Fatalf("malformed scope must be an in-band domain failure: %+v", response.Error)
	}
	var output toolCallOutput
	if err := json.Unmarshal(response.Result, &output); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if !output.IsError {
		t.Fatal("isError = false, want true (fail closed on a malformed scope)")
	}
	if len(output.Content) == 0 || !strings.Contains(output.Content[0]["text"], "INVALID") {
		t.Fatalf("error text must carry an INVALID code: %v", output.Content)
	}
}
