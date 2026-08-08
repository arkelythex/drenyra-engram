// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This test freezes the v0.8 batch 2 retention-policy MCP surface
// (docs/architecture/evidence-lifecycle-v0.8.md §3.1/§6/§9):
//
//   - accounting_retention_policy_put is the AUTHENTICATED administration
//     mutation: its strict argument shape accepts NO identity field and, on this
//     session-less stdio server, it FAILS CLOSED with AUTHENTICATION_REQUIRED —
//     the same ADR-003 contract as accounting_approve (design §3/§6);
//   - accounting_retention_policy_resolve / _evaluate are SCOPE-FIRST READS: the
//     exact scope tuple is part of the arguments, so a caller whose scope differs
//     sees matched=false / UNKNOWN_RETENTION_STATE — never the policy.
//
// Fixtures mint the records_compliance_officer principal through the REAL
// resolver over a minimal session-store fake (the membership_roles role CHECK
// stays ladder-only in this slice, so the v0.8 roles are never seeded through
// the DB — the same fixture pattern the store suite uses) and put policies
// through the REAL store path.
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// retentionSessionStore is a minimal auth.SessionStore fixture that bypasses the
// membership_roles CHECK constraint (the v0.8 roles are deliberately NOT seeded
// through the DB — the store suite uses the same fake pattern, and the role CHECK
// stays ladder-only in this slice) and mints a records_compliance_officer
// principal through the REAL resolver, the same factory production middleware
// uses.
type retentionSessionStore struct {
	session    auth.StoredSession
	membership auth.MembershipRecord
}

func (f *retentionSessionStore) LookupByTokenHash(context.Context, string) (auth.StoredSession, error) {
	return f.session, nil
}

func (f *retentionSessionStore) LoadMembership(context.Context, string) (auth.MembershipRecord, error) {
	return f.membership, nil
}

// retentionFixturePrincipal mints the §8.1 records_compliance_officer principal
// for tenantID/companyID via the REAL resolver over the fake session store.
func retentionFixturePrincipal(t *testing.T, tenantID, companyID string) auth.VerifiedApprovalPrincipal {
	t.Helper()
	resolver := &auth.Resolver{Sessions: &retentionSessionStore{
		session: auth.StoredSession{
			ID:                   "session-retention-1",
			MembershipID:         "membership-retention-1",
			AuthenticationMethod: auth.AuthMethodSession,
			AssuranceLevel:       auth.AssuranceStandard,
			AuthenticatedAt:      "2026-08-07T12:00:00Z",
			ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
		membership: auth.MembershipRecord{
			ID:            "membership-retention-1",
			SubjectID:     "lucia.ramirez",
			TenantID:      tenantID,
			CompanyID:     companyID,
			Status:        "active",
			Roles:         []auth.AccountingRole{auth.RoleRecordsComplianceOfficer},
			CompanyActive: true,
		},
	}, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: "fixture-token",
	})
	if err != nil {
		t.Fatalf("fixture principal: %v", err)
	}
	return principal
}

// putRetentionFixture writes ONE enabled company-scope policy through the REAL
// store path with a records_compliance_officer principal and returns the created
// policy.
func putRetentionFixture(t *testing.T, api *API, scope core.Scope) core.RetentionPolicy {
	t.Helper()
	st, ok := api.Store.(*store.SQLiteStore)
	if !ok {
		t.Fatalf("test API store is %T, want *store.SQLiteStore", api.Store)
	}
	result, err := st.PutRetentionPolicy(context.Background(), core.PutRetentionPolicyCommand{
		Scope:           scope,
		Jurisdiction:    "PE",
		Legislation:     "NATIONAL-TAX",
		Authority:       "tenant-records",
		Source:          "deployment decision 2026-08-07",
		Category:        "invoice",
		MinPeriod:       "202401",
		ExpectedVersion: 0,
		Enabled:         true,
		RequestID:       "req-fixture-" + scope.OrganizationID,
	}, retentionFixturePrincipal(t, scope.OrganizationID, scope.CompanyID))
	if err != nil {
		t.Fatalf("seed retention policy: %v", err)
	}
	return result.Policy
}

// retentionScopeJSON is the MCP wire scope of testScope(testRucA).
const retentionScopeJSON = `{"kind":"company","organizationId":"org-acme","companyId":"acme","ruc":"20100039201","period":"202401"}`

// TestMCPRetentionPolicyToolsListed: the three v0.8 tools are advertised in the
// catalog under the accounting_ namespace.
func TestMCPRetentionPolicyToolsListed(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range ToolCatalog() {
		name, _ := tool["name"].(string)
		names[name] = true
	}
	for _, want := range []string{
		"accounting_retention_policy_put",
		"accounting_retention_policy_resolve",
		"accounting_retention_policy_evaluate",
	} {
		if !names[want] {
			t.Fatalf("catalog missing tool %q", want)
		}
	}
}

// TestMCPRetentionPolicyPutFailsClosedWithoutSession: accounting_retention_policy_put
// accepts exactly its declared command arguments (including the scope JSON and
// the tenant-scoped idempotency key) but the stdio MCP server has NO
// authenticated session binding (design §3), so the tool FAILS CLOSED with the
// frozen AUTHENTICATION_REQUIRED as an in-band tool result (isError=true) —
// never a JSON-RPC error, never a silent identity from the arguments.
func TestMCPRetentionPolicyPutFailsClosedWithoutSession(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_retention_policy_put",
		"arguments": map[string]any{
			"scope":            retentionScopeJSON,
			"jurisdiction":     "PE",
			"legislation":      "NATIONAL-TAX",
			"authority":        "tenant-records",
			"source":           "deployment decision 2026-08-07",
			"category":         "invoice",
			"min_period":       "202401",
			"expected_version": int64(0),
			"enabled":          true,
			"request_id":       "req-put-1",
		},
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
}

// TestMCPRetentionPolicyPutRejectsExtraArgs: accounting_retention_policy_put
// parses its args STRICTLY (design §6): ANY unknown field — including any
// caller-supplied authority (actorId/actorKind/subjectId/roles) — is a
// malformed argument shape (JSON-RPC -32602), never silently ignored.
func TestMCPRetentionPolicyPutRejectsExtraArgs(t *testing.T) {
	m, _ := newTestMCP(t)
	base := map[string]any{
		"scope":        retentionScopeJSON,
		"jurisdiction": "PE",
		"legislation":  "NATIONAL-TAX",
		"authority":    "tenant-records",
		"source":       "deployment decision 2026-08-07",
		"category":     "invoice",
		"min_period":   "202401",
		"request_id":   "req-1",
	}
	cases := []struct {
		name   string
		extras map[string]any
	}{
		{"actorId", map[string]any{"actorId": "lucia.ramirez"}},
		{"actorKind", map[string]any{"actorKind": "human"}},
		{"roles", map[string]any{"roles": []string{"records_compliance_officer"}}},
		{"subjectId", map[string]any{"subjectId": "lucia.ramirez"}},
		{"unrelated extra", map[string]any{"bogus": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := make(map[string]any, len(base)+len(tc.extras))
			for k, v := range base {
				args[k] = v
			}
			for k, v := range tc.extras {
				args[k] = v
			}
			response := call(t, m, 1, "tools/call", map[string]any{
				"name": "accounting_retention_policy_put", "arguments": args,
			})
			if response.Error == nil || response.Error.Code != codeInvalidParams {
				t.Fatalf("extra args %v: want JSON-RPC -32602, got %+v", tc.extras, response.Error)
			}
		})
	}
}

// TestMCPRetentionPolicyPutRequiresAllArgs: a missing declared argument is a
// shape error (-32602), never a silent default or a domain attempt.
func TestMCPRetentionPolicyPutRequiresAllArgs(t *testing.T) {
	m, _ := newTestMCP(t)
	base := map[string]any{
		"scope":        retentionScopeJSON,
		"jurisdiction": "PE",
		"legislation":  "NATIONAL-TAX",
		"authority":    "tenant-records",
		"source":       "deployment decision 2026-08-07",
		"category":     "invoice",
		"min_period":   "202401",
		"request_id":   "req-1",
	}
	for _, missing := range []string{"scope", "jurisdiction", "legislation", "authority", "source", "category", "min_period", "request_id"} {
		t.Run(missing, func(t *testing.T) {
			args := make(map[string]any, len(base))
			for k, v := range base {
				args[k] = v
			}
			delete(args, missing)
			response := call(t, m, 1, "tools/call", map[string]any{
				"name": "accounting_retention_policy_put", "arguments": args,
			})
			if response.Error == nil || response.Error.Code != codeInvalidParams {
				t.Fatalf("missing %s: want JSON-RPC -32602, got %+v", missing, response.Error)
			}
		})
	}
}

// TestMCPRetentionPolicyResolveScopeFirst: the scope-first read returns the
// exact active policy for the exact scope + evidence tuple.
func TestMCPRetentionPolicyResolveScopeFirst(t *testing.T) {
	m, api := newTestMCP(t)
	scope := testScope(testRucA)
	putRetentionFixture(t, api, scope)

	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_retention_policy_resolve",
		"arguments": map[string]any{
			"scope":        retentionScopeJSON,
			"jurisdiction": "PE",
			"legislation":  "NATIONAL-TAX",
			"category":     "invoice",
		},
	})
	if response.Error != nil {
		t.Fatalf("resolve error: %+v", response.Error)
	}
	var resolved struct {
		Policy  core.RetentionPolicy `json:"policy"`
		Matched bool                 `json:"matched"`
	}
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &resolved); err != nil {
		t.Fatalf("decode resolve: %v", err)
	}
	if !resolved.Matched {
		t.Fatal("matched = false, want true for the exact scope + tuple")
	}
	if resolved.Policy.Jurisdiction != "PE" || resolved.Policy.Version != 1 || resolved.Policy.TenantID != testOrgID {
		t.Fatalf("resolved policy mismatch: %+v", resolved.Policy)
	}
}

// TestMCPRetentionPolicyResolveCrossTenantInvisible: a caller whose exact scope
// differs sees matched=false with a ZERO policy — never another tenant's policy.
func TestMCPRetentionPolicyResolveCrossTenantInvisible(t *testing.T) {
	m, api := newTestMCP(t)
	scope := testScope(testRucA)
	putRetentionFixture(t, api, scope)

	// Different company RUC in the same organization.
	otherScope := `{"kind":"company","organizationId":"org-acme","companyId":"acme","ruc":"20600995804","period":"202401"}`
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_retention_policy_resolve",
		"arguments": map[string]any{
			"scope":        otherScope,
			"jurisdiction": "PE",
			"legislation":  "NATIONAL-TAX",
			"category":     "invoice",
		},
	})
	var resolved struct {
		Policy  core.RetentionPolicy `json:"policy"`
		Matched bool                 `json:"matched"`
	}
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &resolved); err != nil {
		t.Fatalf("decode resolve: %v", err)
	}
	if resolved.Matched {
		t.Fatal("LEAK via MCP: a different exact scope must never resolve another tenant's policy")
	}
	if resolved.Policy.PolicyID != "" {
		t.Fatalf("cross-tenant resolve must return a ZERO policy, got %+v", resolved.Policy)
	}

	// Same scope but a different evidence tuple also never resolves.
	response = call(t, m, 2, "tools/call", map[string]any{
		"name": "accounting_retention_policy_resolve",
		"arguments": map[string]any{
			"scope":        retentionScopeJSON,
			"jurisdiction": "BR",
			"legislation":  "NATIONAL-TAX",
			"category":     "invoice",
		},
	})
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &resolved); err != nil {
		t.Fatalf("decode resolve: %v", err)
	}
	if resolved.Matched {
		t.Fatal("a different jurisdiction tuple must never resolve")
	}
}

// TestMCPRetentionPolicyEvaluateDimension: the fail-closed eligibility read
// reports eligible once the object's period reached the min_period floor and
// not_due before it.
func TestMCPRetentionPolicyEvaluateDimension(t *testing.T) {
	m, api := newTestMCP(t)
	scope := testScope(testRucA)
	putRetentionFixture(t, api, scope)

	evaluate := func(objectPeriod string) string {
		t.Helper()
		response := call(t, m, 1, "tools/call", map[string]any{
			"name": "accounting_retention_policy_evaluate",
			"arguments": map[string]any{
				"scope":         retentionScopeJSON,
				"jurisdiction":  "PE",
				"legislation":   "NATIONAL-TAX",
				"category":      "invoice",
				"object_period": objectPeriod,
			},
		})
		if response.Error != nil {
			t.Fatalf("evaluate error: %+v", response.Error)
		}
		return toolResultText(t, response)
	}

	eligible := evaluate("202401")
	var result core.RetentionEligibilityResult
	if err := json.Unmarshal([]byte(eligible), &result); err != nil {
		t.Fatalf("decode evaluate: %v", err)
	}
	if result.Eligibility != core.RetentionEligibilityEligible {
		t.Fatalf("eligibility = %q, want eligible (period reached the floor)", result.Eligibility)
	}
	if result.PolicyVersion != 1 || result.MinPeriod != "202401" || result.ModelVersion != core.RetentionPolicyModelVersion {
		t.Fatalf("eligibility evidence mismatch: %+v", result)
	}

	notDue := evaluate("202312")
	if err := json.Unmarshal([]byte(notDue), &result); err != nil {
		t.Fatalf("decode evaluate: %v", err)
	}
	if result.Eligibility != core.RetentionEligibilityNotDue {
		t.Fatalf("eligibility = %q, want not_due (period below the floor)", result.Eligibility)
	}
}

// TestMCPRetentionPolicyEvaluateFailsClosed: without an exact active policy the
// read fails closed with UNKNOWN_RETENTION_STATE as an in-band tool result —
// the engine never guesses a retention outcome.
func TestMCPRetentionPolicyEvaluateFailsClosed(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_retention_policy_evaluate",
		"arguments": map[string]any{
			"scope":         retentionScopeJSON,
			"jurisdiction":  "PE",
			"legislation":   "NATIONAL-TAX",
			"category":      "invoice",
			"object_period": "202401",
		},
	})
	if response.Error != nil {
		t.Fatalf("domain failure must be a tool result, not a JSON-RPC error: %+v", response.Error)
	}
	var output toolCallOutput
	if err := json.Unmarshal(response.Result, &output); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if !output.IsError {
		t.Fatal("isError = false, want true (unknown retention state fails closed)")
	}
	if len(output.Content) == 0 || !strings.Contains(output.Content[0]["text"], "UNKNOWN_RETENTION_STATE") {
		t.Fatalf("error text must carry UNKNOWN_RETENTION_STATE: %v", output.Content)
	}
}

// TestMCPRetentionPolicyReadsRejectExtraArgs: the read tools also parse STRICTLY
// — an unknown field (including caller-supplied identity) is -32602, never
// silently ignored.
func TestMCPRetentionPolicyReadsRejectExtraArgs(t *testing.T) {
	m, _ := newTestMCP(t)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"resolve actorId", map[string]any{
			"scope": retentionScopeJSON, "jurisdiction": "PE", "legislation": "L",
			"category": "invoice", "actorId": "lucia.ramirez",
		}},
		{"evaluate subjectId", map[string]any{
			"scope": retentionScopeJSON, "jurisdiction": "PE", "legislation": "L",
			"category": "invoice", "object_period": "202401", "subjectId": "lucia.ramirez",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := "accounting_retention_policy_resolve"
			if strings.Contains(tc.name, "evaluate") {
				tool = "accounting_retention_policy_evaluate"
			}
			response := call(t, m, 1, "tools/call", map[string]any{
				"name": tool, "arguments": tc.args,
			})
			if response.Error == nil || response.Error.Code != codeInvalidParams {
				t.Fatalf("extra args: want JSON-RPC -32602, got %+v", response.Error)
			}
		})
	}
}
