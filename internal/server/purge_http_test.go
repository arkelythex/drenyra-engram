// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This test freezes the v0.8 batch 4 purge HTTP surface (WU-4 —
// docs/architecture/evidence-lifecycle-v0.8.md §2/§3/§9/§10/§11/§12):
//
//   - every mutation (request/approve/reject/cancel/withdraw/execute) requires
//     an AUTHENTICATED principal (401 AUTHENTICATION_REQUIRED without a
//     credential — no silent fallback), rides the (tenant, requestId)
//     Idempotency-Key header and parses its strict body with
//     DisallowUnknownFields (caller-supplied authority/scope → 400, never
//     ignored);
//   - approve serves BOTH the default approver (order 1) and the DISTINCT dual
//     second approver (order 2) — the store derives the order from the decision
//     ledger;
//   - the lifecycle export is a READ-ONLY SCOPE-FIRST read that emits NO
//     receipt and never reads object bytes.
//
// Fixtures are end-to-end: identities and sessions are seeded directly on the
// test SQLite store with DISTINCT subject ids (the SoD fixtures need separate
// principals in the same tenant/company) and principals are minted through the
// REAL resolver — the same path production middleware uses.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// seedPurgeIdentity seeds ONE identity + expiring session for an EXPLICIT
// subject id (the SoD/distinct-principal fixtures need separate principals in
// the SAME tenant/company) and returns the raw bearer token (only its SHA-256
// hash is stored — design §3).
func seedPurgeIdentity(t *testing.T, api *API, tenantID, companyID, ruc, subjectID string, roles []auth.AccountingRole) string {
	t.Helper()
	st, ok := api.Store.(*store.SQLiteStore)
	if !ok {
		t.Fatalf("test API store is %T, want *store.SQLiteStore", api.Store)
	}
	membershipID := "membership-" + subjectID
	if err := st.SeedIdentity(store.IdentitySeed{
		TenantID:     tenantID,
		CompanyID:    companyID,
		CompanyRUC:   ruc,
		CompanyName:  "Demo SAC",
		MembershipID: membershipID,
		SubjectID:    subjectID,
		Roles:        roles,
	}); err != nil {
		t.Fatalf("seed identity %s: %v", subjectID, err)
	}
	token := "fixture-token-" + subjectID
	if err := st.SeedSession(store.SessionSeed{
		ID:                   "session-" + subjectID,
		TokenHash:            sha256HexString(token),
		MembershipID:         membershipID,
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      "2026-08-05T12:00:00Z",
		ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed session %s: %v", subjectID, err)
	}
	return token
}

// purgePrincipal mints the pre-verified principal of a seeded session through
// the REAL resolver — the same factory the HTTP middleware uses.
func purgePrincipal(t *testing.T, api *API, token string) auth.VerifiedApprovalPrincipal {
	t.Helper()
	st, ok := api.Store.(*store.SQLiteStore)
	if !ok {
		t.Fatalf("test API store is %T, want *store.SQLiteStore", api.Store)
	}
	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		t.Fatalf("mint fixture principal: %v", err)
	}
	return principal
}

// purgeSnapshotHash reproduces the store's canonical lifecycle snapshot hash
// (§3.8 — the same assembleSnapshot contract the store tests freeze) for the
// transport fixtures: the object id, the exact scope tuple, the given
// lifecycle/retention state, the bound policy (empty before a request binds
// one), the request id and approval ids when present.
func purgeSnapshotHash(t *testing.T, scope core.Scope, objectID string, lifecycle core.PurgeLifecycleState, retention core.RetentionEligibility, policyID, category string, policyVersion int64, requestID string, approvalIDs []string) string {
	t.Helper()
	return core.ComputeLifecycleSnapshotHash(core.LifecycleSnapshot{
		ObjectID:       objectID,
		TenantID:       scope.OrganizationID,
		CompanyID:      scope.CompanyID,
		RUC:            scope.RUC,
		Period:         scope.Period,
		LifecycleState: lifecycle,
		RetentionState: retention,
		PolicyID:       policyID,
		PolicyVersion:  policyVersion,
		Category:       category,
		BlockingHolds:  []core.LifecycleHoldRef{},
		RequestID:      requestID,
		ApprovalIDs:    approvalIDs,
	})
}

// purgeHTTP performs an HTTP request against the purge surface with an optional
// bearer token and Idempotency-Key (the same wire helper as approvalHTTP).
func purgeHTTP(t *testing.T, method, url, token, idempotencyKey string, body any) (int, string) {
	t.Helper()
	return approvalHTTP(t, method, url, token, idempotencyKey, body)
}

// purgeFixture seeds ONE evidence object + ONE enabled retention policy at the
// EXACT company test scope and returns the object id, the policy, the scope and
// the PRE-REQUEST lifecycle snapshot hash (H_request — the virtual
// stored/unmanaged snapshot of an unbound object, §14).
func purgeFixture(t *testing.T, api *API, recordsToken string) (string, core.RetentionPolicy, core.Scope, string) {
	t.Helper()
	ctx := context.Background()
	scope := testScope(testRucA)
	objResult, err := api.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       []byte("purge-http-target-bytes-0123456789"),
		ContentType: "application/xml",
		Scope:       scope,
		Source:      core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		t.Fatalf("store purge target object: %v", err)
	}
	polResult, err := api.PutRetentionPolicy(ctx, core.PutRetentionPolicyCommand{
		Scope:           scope,
		Jurisdiction:    "PE",
		Legislation:     "NATIONAL-TAX",
		Authority:       "tenant-records",
		Source:          "deployment decision 2026-08-07",
		Category:        "invoice",
		MinPeriod:       "202401",
		ExpectedVersion: 0,
		Enabled:         true,
		RequestID:       "req-policy-http-fixture",
	}, purgePrincipal(t, api, recordsToken))
	if err != nil {
		t.Fatalf("seed retention policy: %v", err)
	}
	policy := polResult.Policy
	h := purgeSnapshotHash(t, scope, objResult.Object.ObjectID, core.PurgeLifecycleStored,
		core.RetentionEligibility("unmanaged"), "", "", 0, "", nil)
	return objResult.Object.ObjectID, policy, scope, h
}

// requestPurgeHTTP opens a purge pipeline for the object with the accountant
// credential and returns the decoded result (fails the test on any non-201).
func requestPurgeHTTP(t *testing.T, ts *httptest.Server, objectID, expectedHash, key, accountantToken string) core.RequestPurgeResult {
	t.Helper()
	status, raw := purgeHTTP(t, http.MethodPost, ts.URL+"/accounting/objects/"+objectID+"/purge",
		accountantToken, key, map[string]any{
			"jurisdiction":          "PE",
			"legislation":           "NATIONAL-TAX",
			"category":              "invoice",
			"expectedLifecycleHash": expectedHash,
			"reason":                "retention period elapsed",
		})
	if status != http.StatusCreated {
		t.Fatalf("purge request status = %d, want 201; body %s", status, raw)
	}
	var result core.RequestPurgeResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode purge request result: %v", err)
	}
	if result.IdempotentReplay {
		t.Fatal("a fresh purge request must not replay")
	}
	if result.Request.Status != core.PurgeRequestStatusRequested {
		t.Fatalf("request status = %q, want requested", result.Request.Status)
	}
	return result
}

// TestHTTPPurgeRequiresAuthentication: every purge mutation without an
// Authorization credential gets 401 AUTHENTICATION_REQUIRED — there is no
// silent fallback to the shared token guard and no caller-declared identity.
func TestHTTPPurgeRequiresAuthentication(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	// A records officer credential is seeded so the fixture policy can be put;
	// the missing Authorization on the mutations is the only defect under test.
	seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	objectID, _, _, h := purgeFixture(t, api, "fixture-token-lucia.ramirez")

	cases := []struct {
		name string
		url  string
		body any
	}{
		{"request", "/accounting/objects/" + objectID + "/purge", map[string]any{
			"jurisdiction": "PE", "legislation": "NATIONAL-TAX", "category": "invoice",
			"expectedLifecycleHash": h, "reason": "retention period elapsed",
		}},
		{"approve", "/accounting/purge-requests/00000000-0000-4000-8000-000000000001/approve", map[string]any{
			"expectedLifecycleHash": h, "reason": "verified",
		}},
		{"reject", "/accounting/purge-requests/00000000-0000-4000-8000-000000000001/reject", map[string]any{
			"reason": "not eligible",
		}},
		{"cancel", "/accounting/purge-requests/00000000-0000-4000-8000-000000000001/cancel", nil},
		{"withdraw", "/accounting/purge-requests/00000000-0000-4000-8000-000000000001/withdraw", map[string]any{
			"reason": "cleanup",
		}},
		{"execute", "/accounting/purge-requests/00000000-0000-4000-8000-000000000001/execute", map[string]any{
			"expectedLifecycleHash": h,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := purgeHTTP(t, http.MethodPost, ts.URL+tc.url, "", "noauth-key-1", tc.body)
			if status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body %s", status, raw)
			}
			if !strings.Contains(raw, "AUTHENTICATION_REQUIRED") {
				t.Fatalf("body %q must carry AUTHENTICATION_REQUIRED", raw)
			}
		})
	}
}

// TestHTTPPurgeRejectsUnknownToken: a presented but unknown credential is a
// REJECTED principal (PRINCIPAL_INVALID, 401) — not a generic auth-required and
// never a silent fallback.
func TestHTTPPurgeRejectsUnknownToken(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	objectID, _, _, h := purgeFixture(t, api, recordsToken)

	status, raw := purgeHTTP(t, http.MethodPost, ts.URL+"/accounting/objects/"+objectID+"/purge",
		"definitely-not-a-real-token", "req-purge-badtoken", map[string]any{
			"jurisdiction": "PE", "legislation": "NATIONAL-TAX", "category": "invoice",
			"expectedLifecycleHash": h, "reason": "retention period elapsed",
		})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", status, raw)
	}
	if !strings.Contains(raw, "PRINCIPAL_INVALID") {
		t.Fatalf("body %q must carry PRINCIPAL_INVALID", raw)
	}
}

// TestHTTPPurgeRequestHappyPath: an authenticated accountant opens the pipeline
// → 201 with the requested status; replaying the same (tenant, requestId) key
// returns the stored outcome with idempotentReplay=true and NO new row.
func TestHTTPPurgeRequestHappyPath(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	objectID, _, _, h := purgeFixture(t, api, recordsToken)

	result := requestPurgeHTTP(t, ts, objectID, h, "req-purge-http-001", accountantToken)
	if !result.Created {
		t.Fatal("a fresh request must report created=true")
	}
	if result.Request.ObjectID != objectID || result.Request.RequestedBy != "ana.garcia" {
		t.Fatalf("request row = %+v, want object %s requested by ana.garcia", result.Request, objectID)
	}

	// Replay with the SAME (tenant, requestId) key + same command + same
	// principal → the stored outcome, no new write.
	status, raw := purgeHTTP(t, http.MethodPost, ts.URL+"/accounting/objects/"+objectID+"/purge",
		accountantToken, "req-purge-http-001", map[string]any{
			"jurisdiction":          "PE",
			"legislation":           "NATIONAL-TAX",
			"category":              "invoice",
			"expectedLifecycleHash": h,
			"reason":                "retention period elapsed",
		})
	if status != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body %s", status, raw)
	}
	var replayed core.RequestPurgeResult
	if err := json.Unmarshal([]byte(raw), &replayed); err != nil {
		t.Fatalf("decode replayed result: %v", err)
	}
	if !replayed.IdempotentReplay || replayed.Request.RequestID != result.Request.RequestID {
		t.Fatalf("replay = %+v, want idempotentReplay=true with the stored request", replayed)
	}
}

// TestHTTPPurgeApproveWithdrawFlow: the SAME approve endpoint serves the default
// approver (order 1 → approved); the approval retraction (withdraw) returns the
// pipeline to stored (the documented cleanup §7).
func TestHTTPPurgeApproveWithdrawFlow(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	objectID, policy, scope, h := purgeFixture(t, api, recordsToken)

	request := requestPurgeHTTP(t, ts, objectID, h, "req-purge-http-002", accountantToken)
	h2Request := purgeSnapshotHash(t, scope, objectID, core.PurgeLifecycleRequested,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		request.Request.RequestID, nil)

	status, raw := purgeHTTP(t, http.MethodPost,
		ts.URL+"/accounting/purge-requests/"+request.Request.RequestID+"/approve",
		recordsToken, "req-approve-http-002", map[string]any{
			"expectedLifecycleHash": h2Request,
			"reason":                "verified against the reviewed snapshot",
		})
	if status != http.StatusOK {
		t.Fatalf("approve status = %d, want 200; body %s", status, raw)
	}
	var approved core.ApprovePurgeResult
	if err := json.Unmarshal([]byte(raw), &approved); err != nil {
		t.Fatalf("decode approve result: %v", err)
	}
	if approved.ApprovalOrder != 1 || approved.Approval.Decision != core.PurgeApprovalDecisionApproved {
		t.Fatalf("approval = %+v, want order 1 approved", approved)
	}
	if approved.Request.Status != core.PurgeRequestStatusApproved {
		t.Fatalf("request status after first approval = %q, want approved (single-approval category)", approved.Request.Status)
	}

	// Withdraw the approval (a default approver; the Idempotency-Key is THIS
	// withdrawal act's key).
	status, raw = purgeHTTP(t, http.MethodPost,
		ts.URL+"/accounting/purge-requests/"+request.Request.RequestID+"/withdraw",
		recordsToken, "req-withdraw-http-002", map[string]any{"reason": "cleanup before execution"})
	if status != http.StatusOK {
		t.Fatalf("withdraw status = %d, want 200; body %s", status, raw)
	}
	var withdrawn core.WithdrawPurgeResult
	if err := json.Unmarshal([]byte(raw), &withdrawn); err != nil {
		t.Fatalf("decode withdraw result: %v", err)
	}
	if withdrawn.Request.Status != core.PurgeRequestStatusWithdrawn {
		t.Fatalf("request status after withdrawal = %q, want withdrawn", withdrawn.Request.Status)
	}
	if withdrawn.Approval.Decision != core.PurgeApprovalDecisionWithdrawn {
		t.Fatalf("withdrawal decision = %q, want withdrawn", withdrawn.Approval.Decision)
	}
}

// TestHTTPPurgeRejectTerminal: an authenticated default approver rejects the
// open pipeline → the request is TERMINAL rejected and never re-opens.
func TestHTTPPurgeRejectTerminal(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	objectID, _, _, h := purgeFixture(t, api, recordsToken)

	request := requestPurgeHTTP(t, ts, objectID, h, "req-purge-http-003", accountantToken)

	status, raw := purgeHTTP(t, http.MethodPost,
		ts.URL+"/accounting/purge-requests/"+request.Request.RequestID+"/reject",
		recordsToken, "req-reject-http-003", map[string]any{"reason": "evidence still required"})
	if status != http.StatusOK {
		t.Fatalf("reject status = %d, want 200; body %s", status, raw)
	}
	var rejected core.RejectPurgeResult
	if err := json.Unmarshal([]byte(raw), &rejected); err != nil {
		t.Fatalf("decode reject result: %v", err)
	}
	if rejected.Request.Status != core.PurgeRequestStatusRejected {
		t.Fatalf("request status = %q, want rejected (terminal)", rejected.Request.Status)
	}
	if rejected.Approval.Decision != core.PurgeApprovalDecisionRejected {
		t.Fatalf("decision = %q, want rejected", rejected.Approval.Decision)
	}
}

// TestHTTPPurgeDualSecondApproval: a policy-designated fiscal/material category
// needs TWO DISTINCT human approvals. The SAME approve endpoint serves order 2:
// the first approval (records_compliance_officer) keeps the pipeline at
// requested; the second approval (a DISTINCT controller) flips it to approved.
func TestHTTPPurgeDualSecondApproval(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	controllerToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "carlos.ruiz",
		[]auth.AccountingRole{auth.RoleController})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})

	ctx := context.Background()
	scope := testScope(testRucA)
	objResult, err := api.StoreObject(ctx, core.ObjectStoreInput{
		Bytes: []byte("purge-http-dual-target-bytes"), ContentType: "application/xml",
		Scope: scope, Source: core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		t.Fatalf("store dual target object: %v", err)
	}
	polResult, err := api.PutRetentionPolicy(ctx, core.PutRetentionPolicyCommand{
		Scope: scope, Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
		Authority: "tenant-records", Source: "deployment decision 2026-08-07",
		Category: "invoice", MinPeriod: "202401", ExpectedVersion: 0,
		DualApprovalRequired: true,
		Enabled:              true,
		RequestID:            "req-policy-http-dual",
	}, purgePrincipal(t, api, recordsToken))
	if err != nil {
		t.Fatalf("seed dual retention policy: %v", err)
	}
	policy := polResult.Policy
	objectID := objResult.Object.ObjectID
	h := purgeSnapshotHash(t, scope, objectID, core.PurgeLifecycleStored,
		core.RetentionEligibility("unmanaged"), "", "", 0, "", nil)

	request := requestPurgeHTTP(t, ts, objectID, h, "req-purge-http-dual", accountantToken)
	h2Request := purgeSnapshotHash(t, scope, objectID, core.PurgeLifecycleRequested,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		request.Request.RequestID, nil)

	// First approval (order 1): the pipeline STAYS at requested for a dual
	// category — approved means "approved FOR EXECUTION".
	status, raw := purgeHTTP(t, http.MethodPost,
		ts.URL+"/accounting/purge-requests/"+request.Request.RequestID+"/approve",
		recordsToken, "req-approve-http-dual-1", map[string]any{
			"expectedLifecycleHash": h2Request, "reason": "first approval",
		})
	if status != http.StatusOK {
		t.Fatalf("first approval status = %d, want 200; body %s", status, raw)
	}
	var first core.ApprovePurgeResult
	if err := json.Unmarshal([]byte(raw), &first); err != nil {
		t.Fatalf("decode first approval: %v", err)
	}
	if first.ApprovalOrder != 1 {
		t.Fatalf("first approval order = %d, want 1", first.ApprovalOrder)
	}
	if first.Request.Status != core.PurgeRequestStatusRequested {
		t.Fatalf("request status after first approval = %q, want requested (dual category)", first.Request.Status)
	}

	// Second approval (order 2, DISTINCT controller): the reviewed hash is the
	// first approval's resulting snapshot (lifecycle requested + approval id).
	h2AfterFirst := purgeSnapshotHash(t, scope, objectID, core.PurgeLifecycleRequested,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		request.Request.RequestID, []string{first.Approval.ApprovalID})
	status, raw = purgeHTTP(t, http.MethodPost,
		ts.URL+"/accounting/purge-requests/"+request.Request.RequestID+"/approve",
		controllerToken, "req-approve-http-dual-2", map[string]any{
			"expectedLifecycleHash": h2AfterFirst, "reason": "dual second approval",
		})
	if status != http.StatusOK {
		t.Fatalf("second approval status = %d, want 200; body %s", status, raw)
	}
	var second core.ApprovePurgeResult
	if err := json.Unmarshal([]byte(raw), &second); err != nil {
		t.Fatalf("decode second approval: %v", err)
	}
	if second.ApprovalOrder != 2 {
		t.Fatalf("second approval order = %d, want 2", second.ApprovalOrder)
	}
	if second.Request.Status != core.PurgeRequestStatusApproved {
		t.Fatalf("request status after second approval = %q, want approved", second.Request.Status)
	}
}

// TestHTTPPurgeExecuteHappyPath: request → approve → execute through the HTTP
// surface: the two-phase protocol completes (execution state completed, request
// executed) and replaying the SAME execution id returns the stored outcome with
// idempotentReplay=true and NO new write.
func TestHTTPPurgeExecuteHappyPath(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	objectID, policy, scope, h := purgeFixture(t, api, recordsToken)

	request := requestPurgeHTTP(t, ts, objectID, h, "req-purge-http-004", accountantToken)
	h2Request := purgeSnapshotHash(t, scope, objectID, core.PurgeLifecycleRequested,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		request.Request.RequestID, nil)

	status, raw := purgeHTTP(t, http.MethodPost,
		ts.URL+"/accounting/purge-requests/"+request.Request.RequestID+"/approve",
		recordsToken, "req-approve-http-004", map[string]any{
			"expectedLifecycleHash": h2Request, "reason": "verified",
		})
	if status != http.StatusOK {
		t.Fatalf("approve status = %d, want 200; body %s", status, raw)
	}
	var approved core.ApprovePurgeResult
	if err := json.Unmarshal([]byte(raw), &approved); err != nil {
		t.Fatalf("decode approve result: %v", err)
	}
	h2Approved := purgeSnapshotHash(t, scope, objectID, core.PurgeLifecycleApproved,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		request.Request.RequestID, []string{approved.Approval.ApprovalID})

	// Execute — the Idempotency-Key header is the (tenant, executionId) key.
	executionKey := "00000000-0000-4000-8000-000000000701"
	status, raw = purgeHTTP(t, http.MethodPost,
		ts.URL+"/accounting/purge-requests/"+request.Request.RequestID+"/execute",
		recordsToken, executionKey, map[string]any{
			"expectedLifecycleHash": h2Approved, "reason": "execution batch approved",
		})
	if status != http.StatusOK {
		t.Fatalf("execute status = %d, want 200; body %s", status, raw)
	}
	var executed core.ExecutePurgeResult
	if err := json.Unmarshal([]byte(raw), &executed); err != nil {
		t.Fatalf("decode execute result: %v", err)
	}
	if executed.IdempotentReplay {
		t.Fatal("a fresh execution id must not replay")
	}
	if executed.Execution.State != core.PurgeExecutionCompleted {
		t.Fatalf("execution state = %q, want completed", executed.Execution.State)
	}
	if executed.Request.Status != core.PurgeRequestStatusExecuted || executed.Request.ExecutionID != executionKey {
		t.Fatalf("request after execution = %+v, want executed with the execution id", executed.Request)
	}

	// Replay the SAME execution id → the stored outcome, no new write.
	status, raw = purgeHTTP(t, http.MethodPost,
		ts.URL+"/accounting/purge-requests/"+request.Request.RequestID+"/execute",
		recordsToken, executionKey, map[string]any{
			"expectedLifecycleHash": h2Approved, "reason": "execution batch approved",
		})
	if status != http.StatusOK {
		t.Fatalf("execute replay status = %d, want 200; body %s", status, raw)
	}
	var replayed core.ExecutePurgeResult
	if err := json.Unmarshal([]byte(raw), &replayed); err != nil {
		t.Fatalf("decode execute replay: %v", err)
	}
	if !replayed.IdempotentReplay || replayed.Execution.ExecutionID != executionKey {
		t.Fatalf("execute replay = %+v, want idempotentReplay=true with the stored outcome", replayed)
	}
}

// TestHTTPPurgeApproveStrictBodyRejectsAuthority: the strict approve body
// REJECTS any caller-supplied authority field (actorId/roles/subjectId) with
// 400 — the ADR-003 contract (identity comes ONLY from the credential).
func TestHTTPPurgeApproveStrictBodyRejectsAuthority(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	_, _, _, h := purgeFixture(t, api, recordsToken)

	requestID := "00000000-0000-4000-8000-000000000002"
	for name, extra := range map[string]map[string]any{
		"actorId":   {"actorId": "lucia.ramirez"},
		"roles":     {"roles": []string{"records_compliance_officer"}},
		"subjectId": {"subjectId": "lucia.ramirez"},
		"tenantId":  {"tenantId": testOrgID},
	} {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{"expectedLifecycleHash": h, "reason": "verified"}
			for k, v := range extra {
				body[k] = v
			}
			status, raw := purgeHTTP(t, http.MethodPost,
				ts.URL+"/accounting/purge-requests/"+requestID+"/approve",
				recordsToken, "req-approve-strict-1", body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", status, raw)
			}
			if !strings.Contains(raw, "INVALID") {
				t.Fatalf("body %q must carry INVALID", raw)
			}
		})
	}
	// The unknown request id above never reaches the store: the strict-body
	// rejection happens BEFORE any lookup, so the 400 proves the authority
	// field was rejected without a domain attempt.
}

// TestHTTPPurgeRequestRequiresIdempotencyKey: a purge mutation without the
// Idempotency-Key header is a 400, never a silent non-idempotent write.
func TestHTTPPurgeRequestRequiresIdempotencyKey(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	objectID, _, _, h := purgeFixture(t, api, recordsToken)

	status, raw := purgeHTTP(t, http.MethodPost, ts.URL+"/accounting/objects/"+objectID+"/purge",
		accountantToken, "", map[string]any{
			"jurisdiction": "PE", "legislation": "NATIONAL-TAX", "category": "invoice",
			"expectedLifecycleHash": h, "reason": "retention period elapsed",
		})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", status, raw)
	}
	if !strings.Contains(raw, "Idempotency-Key") {
		t.Fatalf("body %q must name the missing Idempotency-Key header", raw)
	}
}

// TestHTTPPurgeApproveUnknownRequestIs404: an unknown purge request id maps to
// 404 PURGE_REQUEST_NOT_FOUND — never a silent success.
func TestHTTPPurgeApproveUnknownRequestIs404(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	_, _, _, h := purgeFixture(t, api, recordsToken)

	status, raw := purgeHTTP(t, http.MethodPost,
		ts.URL+"/accounting/purge-requests/00000000-0000-4000-8000-000000000003/approve",
		recordsToken, "req-approve-404", map[string]any{
			"expectedLifecycleHash": h, "reason": "verified",
		})
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", status, raw)
	}
	if !strings.Contains(raw, "PURGE_REQUEST_NOT_FOUND") {
		t.Fatalf("body %q must carry PURGE_REQUEST_NOT_FOUND", raw)
	}
}

// TestHTTPLifecycleExportScopeFirst: the read-only export returns the
// deterministic bundle for the exact RUC-scoped request — the self-hashing
// manifest carries the frozen version, the applied scope, the counts and the
// content-addressed exportId. The export is a query: it emits NO receipt and
// never reads object bytes.
func TestHTTPLifecycleExportScopeFirst(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	objectID, _, scope, _ := purgeFixture(t, api, recordsToken)

	// The export is a SCOPE-FIRST read over the EXACT company scope of the
	// stored evidence: the URL supplies the company id explicitly (?companyId=)
	// alongside ruc/organizationId/period — the export never derives the company
	// id from the RUC (the fixture evidence lives under companyId=acme).
	url := ts.URL + "/accounting/lifecycle/export?ruc=" + testRucA + "&organizationId=" + testOrgID + "&companyId=acme&period=" + testPeriod
	status, raw := httpJSON(t, http.MethodGet, url, "", nil)
	if status != http.StatusOK {
		t.Fatalf("export status = %d, want 200; body %s", status, raw)
	}
	var bundle core.EvidenceExportBundle
	if err := json.Unmarshal([]byte(raw), &bundle); err != nil {
		t.Fatalf("decode export bundle: %v", err)
	}
	if bundle.Manifest.Version != core.EvidenceExportModelVersion {
		t.Fatalf("manifest version = %q, want %q", bundle.Manifest.Version, core.EvidenceExportModelVersion)
	}
	if !strings.HasPrefix(bundle.Manifest.ExportID, core.EvidenceExportModelVersion+":") {
		t.Fatalf("exportId = %q, want the content-addressed %q: prefix", bundle.Manifest.ExportID, core.EvidenceExportModelVersion)
	}
	if bundle.Manifest.Scope.OrganizationID != scope.OrganizationID || bundle.Manifest.Scope.RUC != scope.RUC {
		t.Fatalf("manifest scope = %+v, want the requested RUC scope", bundle.Manifest.Scope)
	}
	if bundle.Manifest.Counts.Objects < 1 {
		t.Fatalf("objects count = %d, want >= 1 (the fixture object)", bundle.Manifest.Counts.Objects)
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
	// The deterministic contract: two identical exports are byte-identical.
	status, raw2 := httpJSON(t, http.MethodGet, url, "", nil)
	if status != http.StatusOK {
		t.Fatalf("second export status = %d, want 200", status)
	}
	if raw != raw2 {
		t.Fatal("identical data must produce the identical export bundle (determinism contract)")
	}
}

// TestHTTPLifecycleExportInvalidScope: a malformed export scope fails closed
// with 400 (INVALID_RUC) — the export never guesses a scope.
func TestHTTPLifecycleExportInvalidScope(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	status, raw := httpJSON(t, http.MethodGet,
		ts.URL+"/accounting/lifecycle/export?ruc=123&organizationId="+testOrgID, "", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", status, raw)
	}
	if !strings.Contains(raw, "INVALID_RUC") {
		t.Fatalf("body %q must carry INVALID_RUC", raw)
	}
}
