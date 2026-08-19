// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the AUTHENTICATED approval
// HTTP surface (v0.4.0 Step 1, ADR-003): the principal is derived ONLY from the
// Authorization credential; a strict body can never supply authority; the legacy
// v0.3 approve route is disabled by default; failures map to the frozen
// status/code table of design section 6 and never include memory content.
//
// Fixtures are end-to-end: identities and sessions are seeded directly on the
// test SQLite store (design section 8 — no environment state), and principals
// are minted through the REAL resolver + REAL SessionStore, the same path
// production middleware uses.
package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// sha256HexString is the token-hash helper of the fixtures (the middleware and
// resolver hash the raw token the same way before any lookup).
func sha256HexString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// seedApprovalIdentity seeds the FK chain (companies → memberships →
// membership_roles) plus one expiring session for tenantID/companyID and returns
// the raw token (ONLY its SHA-256 hash is stored). Roles are the membership's
// accounting roles.
func seedApprovalIdentity(t *testing.T, api *API, tenantID, companyID, ruc string, roles []auth.AccountingRole) string {
	t.Helper()
	st, ok := api.Store.(*store.SQLiteStore)
	if !ok {
		t.Fatalf("test API store is %T, want *store.SQLiteStore", api.Store)
	}
	membershipID := "membership-" + tenantID
	if err := st.SeedIdentity(store.IdentitySeed{
		TenantID:     tenantID,
		CompanyID:    companyID,
		CompanyRUC:   ruc,
		CompanyName:  "Demo SAC",
		MembershipID: membershipID,
		SubjectID:    "maria.torres",
		Roles:        roles,
	}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	token := "fixture-token-" + tenantID + "-" + companyID
	if err := st.SeedSession(store.SessionSeed{
		ID:                   "session-" + tenantID,
		TokenHash:            sha256HexString(token),
		MembershipID:         membershipID,
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      "2026-08-05T12:00:00Z",
		ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return token
}

// savePendingApproval saves a fiscalEffect=closing memory in the demo scope: it
// lands pending_review behind the v2 human gate and can only reach approved via
// the authenticated approval route.
func savePendingApproval(t *testing.T, api *API, topicKey string) core.AccountingMemory {
	t.Helper()
	result, err := api.Save(core.SaveInput{
		TopicKey:     topicKey,
		Title:        "Ajuste pendiente de aprobacion",
		Kind:         core.KindDecision,
		Scope:        demoScope(),
		Content:      core.Content{What: "ajuste por comprobante tardio", Why: "fixture", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: core.FiscalEffectClosing,
		EffectiveAt:  "2026-07-31T12:00:00Z",
		Source:       testAgentSource,
		Confidence:   0.8,
	})
	if err != nil {
		t.Fatalf("save pending approval fixture: %v", err)
	}
	if result.Memory.Status != core.StatusPendingReview {
		t.Fatalf("fixture status = %q, want pending_review (fiscal effect gate)", result.Memory.Status)
	}
	return result.Memory
}

// approvalHTTP performs an HTTP request against the approval route with an
// optional bearer token and Idempotency-Key.
func approvalHTTP(t *testing.T, method, url, token, idempotencyKey string, body any) (int, string) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer func() { _ = response.Body.Close() }()
	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(response.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response.StatusCode, buffer.String()
}

// approvalServer builds a test server plus a demo-tenant controller identity
// (token returned) and one pending_review memory.
func approvalServer(t *testing.T) (*httptest.Server, *API, string, core.AccountingMemory) {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	token := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleController})
	mem := savePendingApproval(t, api, "approval/fixture")
	return ts, api, token, mem
}

// TestHTTPApprovalRequiresAuthentication: a request without Authorization gets
// AUTHENTICATION_REQUIRED — there is no silent fallback to the legacy shared
// token guard.
func TestHTTPApprovalRequiresAuthentication(t *testing.T) {
	ts, _, _, mem := approvalServer(t)
	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		"", "approval-noauth-1", map[string]string{"expectedEnvelopeHash": "x", "reason": "x"})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", status, raw)
	}
	if !strings.Contains(raw, "AUTHENTICATION_REQUIRED") {
		t.Fatalf("body %q must carry AUTHENTICATION_REQUIRED", raw)
	}
}

// TestHTTPApprovalRejectsUnknownToken: a presented but unknown credential is a
// rejected principal (PRINCIPAL_INVALID), not a generic auth-required.
func TestHTTPApprovalRejectsUnknownToken(t *testing.T) {
	ts, _, _, mem := approvalServer(t)
	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		"definitely-not-a-real-token", "approval-unknown-1",
		map[string]string{"expectedEnvelopeHash": "x", "reason": "x"})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", status, raw)
	}
	if !strings.Contains(raw, "PRINCIPAL_INVALID") {
		t.Fatalf("body %q must carry PRINCIPAL_INVALID", raw)
	}
}

// TestHTTPApprovalRejectsCallerSuppliedAuthority is THE regression test for the
// ADR-003 gap: actor/actorKind/subjectId/roles (or any other extra body field)
// must be REJECTED with 400, never ignored — a body field can never supply
// authority.
func TestHTTPApprovalRejectsCallerSuppliedAuthority(t *testing.T) {
	ts, _, token, mem := approvalServer(t)
	h1 := core.ComputeEnvelopeHash(mem)
	cases := []struct {
		name  string
		field map[string]any
	}{
		{"actor", map[string]any{"actor": "maria.torres"}},
		{"actorKind", map[string]any{"actorKind": "human"}},
		{"subjectId", map[string]any{"subjectId": "subject-1"}},
		{"roles", map[string]any{"roles": []string{"controller"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := map[string]any{"expectedEnvelopeHash": h1, "reason": "x"}
			for k, v := range c.field {
				body[k] = v
			}
			status, raw := approvalHTTP(t, http.MethodPost,
				ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
				token, "approval-strict-1", body)
			if status != http.StatusBadRequest {
				t.Fatalf("%s: status = %d, want 400; body %s", c.name, status, raw)
			}
			if !strings.Contains(raw, "INVALID") {
				t.Fatalf("%s: body %q must carry the INVALID code", c.name, raw)
			}
		})
	}
}

// TestHTTPApprovalRequiresIdempotencyKey: the Idempotency-Key header is
// REQUIRED (it becomes the request id of the idempotency reservation); a
// missing key is a 400, never an authorization decision.
func TestHTTPApprovalRequiresIdempotencyKey(t *testing.T) {
	ts, _, token, mem := approvalServer(t)
	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		token, "", map[string]string{"expectedEnvelopeHash": "x", "reason": "x"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", status, raw)
	}
	if !strings.Contains(raw, "INVALID") {
		t.Fatalf("body %q must carry the INVALID code", raw)
	}
}

// TestHTTPApprovalHappyPath: a seeded identity with an expiring session approves
// a pending_review memory against the CURRENT envelope → 200 with the
// core.ApprovalResult JSON, envelope H1 → H2, persisted as approved.
func TestHTTPApprovalHappyPath(t *testing.T) {
	ts, api, token, mem := approvalServer(t)
	h1 := core.ComputeEnvelopeHash(mem)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		token, "approval-happy-1", map[string]string{
			"expectedEnvelopeHash": h1,
			"reason":               "revisado y conforme",
		})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", status, raw)
	}
	var result core.ApprovalResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.MemoryID != mem.Identity.ID {
		t.Errorf("memoryId = %q, want %q", result.MemoryID, mem.Identity.ID)
	}
	if result.PreviousStatus != string(core.StatusPendingReview) || result.CurrentStatus != string(core.StatusApproved) {
		t.Errorf("statuses = %s -> %s, want pending_review -> approved", result.PreviousStatus, result.CurrentStatus)
	}
	if result.ReviewedEnvelopeHash != h1 {
		t.Errorf("reviewedEnvelopeHash = %s, want %s (H1)", result.ReviewedEnvelopeHash, h1)
	}
	if result.ResultingEnvelopeHash == "" || result.ResultingEnvelopeHash == h1 {
		t.Errorf("resultingEnvelopeHash must be a non-empty H2 different from H1; got %q", result.ResultingEnvelopeHash)
	}
	if result.PrincipalSubjectID != "maria.torres" {
		t.Errorf("principalSubjectId = %q, want maria.torres (the verified subject)", result.PrincipalSubjectID)
	}
	if result.IdempotentReplay {
		t.Error("fresh approval must not report idempotentReplay")
	}

	// The state change is persisted: approved with the resulting envelope.
	got, err := api.Get(mem.Identity.ID)
	if err != nil {
		t.Fatalf("get approved memory: %v", err)
	}
	if got.Status != core.StatusApproved {
		t.Errorf("stored status = %q, want approved", got.Status)
	}
	if got.EnvelopeHash != result.ResultingEnvelopeHash {
		t.Errorf("stored envelope_hash = %q, want %q (H2)", got.EnvelopeHash, result.ResultingEnvelopeHash)
	}
}

// TestHTTPApprovalCrossTenantDenied: a valid session of ANOTHER tenant cannot
// approve the memory (frozen TENANT_SCOPE_MISMATCH, 403).
func TestHTTPApprovalCrossTenantDenied(t *testing.T) {
	ts, api, _, mem := approvalServer(t)
	otherToken := seedApprovalIdentity(t, api, "other_org", "other_01", "20100039201",
		[]auth.AccountingRole{auth.RoleController})
	h1 := core.ComputeEnvelopeHash(mem)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		otherToken, "approval-cross-1", map[string]string{"expectedEnvelopeHash": h1, "reason": "x"})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", status, raw)
	}
	if !strings.Contains(raw, "TENANT_SCOPE_MISMATCH") {
		t.Fatalf("body %q must carry TENANT_SCOPE_MISMATCH", raw)
	}
}

// TestHTTPApprovalRoleNotAuthorized triangulates the policy through HTTP: an
// accountant cannot approve a closing (controller) memory → 403
// ROLE_NOT_AUTHORIZED.
func TestHTTPApprovalRoleNotAuthorized(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	accountantToken := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleAccountant})
	mem := savePendingApproval(t, api, "approval/role")
	h1 := core.ComputeEnvelopeHash(mem)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		accountantToken, "approval-role-1", map[string]string{"expectedEnvelopeHash": h1, "reason": "x"})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", status, raw)
	}
	if !strings.Contains(raw, "ROLE_NOT_AUTHORIZED") {
		t.Fatalf("body %q must carry ROLE_NOT_AUTHORIZED", raw)
	}
}

// TestHTTPApprovalEnvelopeMismatch: an expected hash that differs from the
// CURRENT envelope → 409 ENVELOPE_MISMATCH whose body carries ONLY the error
// envelope plus the two hashes — never memory content.
func TestHTTPApprovalEnvelopeMismatch(t *testing.T) {
	ts, _, token, mem := approvalServer(t)
	// A reviewer who saw different bytes: clone the memory, change BOTH the
	// content and its canonical hash (the envelope hashes ContentHash, not the
	// raw text), and compute a DIFFERENT expected hash (post-review drift).
	changed := mem
	changed.Content.What = "contenido que el revisor jamas vio"
	changed.ContentHash = core.ComputeContentHash(changed)
	staleHash := core.ComputeEnvelopeHash(changed)
	currentHash := core.ComputeEnvelopeHash(mem)
	if staleHash == currentHash {
		t.Fatal("fixture: changed content must change the envelope hash")
	}

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/approve",
		token, "approval-stale-1", map[string]string{"expectedEnvelopeHash": staleHash, "reason": "x"})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", status, raw)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	// ONLY error + the two hashes — the "ONLY the two hashes" contract.
	if len(body) != 3 {
		t.Fatalf("error body carries %d top-level keys, want exactly 3 (error, expectedEnvelopeHash, actualEnvelopeHash): %s", len(body), raw)
	}
	if _, ok := body["expectedEnvelopeHash"]; !ok {
		t.Error("error body missing expectedEnvelopeHash")
	}
	if _, ok := body["actualEnvelopeHash"]; !ok {
		t.Error("error body missing actualEnvelopeHash")
	}
	var detail struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body["error"], &detail); err != nil {
		t.Fatalf("decode error detail: %v", err)
	}
	if detail.Code != "ENVELOPE_MISMATCH" {
		t.Errorf("error code = %q, want ENVELOPE_MISMATCH", detail.Code)
	}
	var got struct {
		Expected string `json:"expectedEnvelopeHash"`
		Actual   string `json:"actualEnvelopeHash"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode hashes: %v", err)
	}
	if got.Expected != staleHash || got.Actual != currentHash {
		t.Errorf("hashes = expected %s actual %s, want expected %s actual %s",
			got.Expected, got.Actual, staleHash, currentHash)
	}
}

// TestHTTPLegacyApproveDisabledByDefault: the deprecated v0.3 approve route
// stays compiled but returns 404 when the opt-in flag is off (the daemon
// default; removed in v0.5.0).
func TestHTTPLegacyApproveDisabledByDefault(t *testing.T) {
	ts, _, token, mem := approvalServer(t)
	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/v1/observations/"+mem.Identity.ID+"/approve",
		token, "", map[string]any{"actorId": "maria.torres", "actorKind": "human"})
	if status != http.StatusNotFound {
		t.Fatalf("legacy approve status = %d, want 404 (disabled by default); body %s", status, raw)
	}
}
