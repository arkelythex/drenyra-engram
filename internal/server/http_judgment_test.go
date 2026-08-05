// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the AUTHENTICATED judgment
// HTTP surface (v0.4.0 Step 2 — adjudicable conflicts, design §6/§7): proposals
// and withdrawals carry a provenance-only source (agent|system — never
// authority); confirm/reject derive the principal ONLY from the Authorization
// credential and their STRICT bodies reject any authority field (the ADR-003
// gap closure for adjudication); errors map to the frozen status/code table and
// only JUDGMENT_HASH_MISMATCH carries details (the two hashes, never content);
// the legacy caller-declared Judge is fail-closed.
//
// Fixtures are end-to-end: identities and sessions are seeded directly on the
// test SQLite store and principals are minted through the REAL resolver — the
// same path production middleware uses.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// seedJudgmentObservations saves two company-scoped facts of the demo scope
// (cmp_org / cmp_01 / 202607) — the pair a proposal adjudicates.
func seedJudgmentObservations(t *testing.T, api *API) (fromID, toID string) {
	t.Helper()
	from := saveDemoFact(t, api, "judgment/from-001", "Saldo de mayor 4011",
		"Saldo de mayor S/ 10,000.00", "2026-07-10T00:00:00Z")
	to := saveDemoFact(t, api, "judgment/to-001", "Saldo de SIRE 4011",
		"Saldo de SIRE S/ 11,284.30", "2026-07-10T00:00:00Z")
	return from.Identity.ID, to.Identity.ID
}

// judgmentServer builds a test server plus a demo-tenant controller identity
// (token returned) and the two observations a proposal needs.
func judgmentServer(t *testing.T) (*httptest.Server, *API, string, string, string) {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	token := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleController})
	fromID, toID := seedJudgmentObservations(t, api)
	return ts, api, token, fromID, toID
}

// agentProposalSource is the canonical provenance-only source of the fixtures.
func agentProposalSource() map[string]any {
	return map[string]any{"system": "http", "actorId": "agent-1", "actorKind": "agent", "session": "sess-1"}
}

// proposeJudgmentHTTP proposes a judgment over the fixtures with the given
// provenance source and an explicit idempotency key.
func proposeJudgmentHTTP(t *testing.T, ts *httptest.Server, fromID, toID, relation, reason, key string, source map[string]any) (int, string) {
	t.Helper()
	body := map[string]any{
		"fromId": fromID, "toId": toID, "relation": relation, "reason": reason,
		"source": source,
	}
	return approvalHTTP(t, http.MethodPost, ts.URL+"/accounting/judgments", "", key, body)
}

// proposeJudgmentFixture proposes with the canonical agent source and returns
// the decoded result (fails the test on any non-201 response).
func proposeJudgmentFixture(t *testing.T, ts *httptest.Server, fromID, toID, key string) core.ProposeJudgmentResult {
	t.Helper()
	status, raw := proposeJudgmentHTTP(t, ts, fromID, toID, "contradicts", "diferencia de saldo entre mayor y SIRE", key, agentProposalSource())
	if status != http.StatusCreated {
		t.Fatalf("propose status = %d, want 201; body %s", status, raw)
	}
	var result core.ProposeJudgmentResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode propose result: %v", err)
	}
	return result
}

// TestHTTPJudgmentProposeHappyPath: an agent source proposes over two existing
// observations → 201 with the proposed judgment; the provenance is preserved
// and the tenant/company are derived from the observations.
func TestHTTPJudgmentProposeHappyPath(t *testing.T) {
	ts, _, _, fromID, toID := judgmentServer(t)

	status, raw := proposeJudgmentHTTP(t, ts, fromID, toID, "supports", "el mayor confirma el saldo", "judgment-propose-1", agentProposalSource())
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", status, raw)
	}
	var result core.ProposeJudgmentResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.JudgmentID == "" || result.JudgmentID != result.Judgment.ID {
		t.Errorf("judgmentId = %q, want the judgment's id %q", result.JudgmentID, result.Judgment.ID)
	}
	if result.IdempotentReplay {
		t.Error("fresh proposal must not be a replay")
	}
	j := result.Judgment
	if j.Status != core.JudgmentProposed {
		t.Errorf("status = %q, want proposed", j.Status)
	}
	if j.FromID != fromID || j.ToID != toID || j.Relation != core.RelationSupports {
		t.Errorf("pair = %s/%s %s, want %s/%s supports", j.FromID, j.ToID, j.Relation, fromID, toID)
	}
	if j.TenantID != "cmp_org" || j.CompanyID != "cmp_01" {
		t.Errorf("scope = %s/%s, want cmp_org/cmp_01 (derived from the observations)", j.TenantID, j.CompanyID)
	}
	if j.FiscalPeriodID != "202607" {
		t.Errorf("fiscalPeriodId = %q, want 202607 (matching observation periods)", j.FiscalPeriodID)
	}
	wantProposer := core.Source{System: "http", ActorID: "agent-1", ActorKind: core.ActorKindAgent, Session: "sess-1"}
	if j.Proposer != wantProposer {
		t.Errorf("proposer = %+v, want %+v (provenance preserved, never authority)", j.Proposer, wantProposer)
	}
	if j.ProposalReason != "el mayor confirma el saldo" || j.Resolution != "" {
		t.Errorf("reason/resolution = %q/%q, want reason/empty", j.ProposalReason, j.Resolution)
	}
	if j.DecidedAt != "" {
		t.Errorf("decidedAt = %q, want empty for an open proposal", j.DecidedAt)
	}
}

// TestHTTPJudgmentProposeHumanSourceDenied: a human source is provenance-only
// and cannot propose — 403 PROPOSAL_UNAUTHORIZED.
func TestHTTPJudgmentProposeHumanSourceDenied(t *testing.T) {
	ts, _, _, fromID, toID := judgmentServer(t)
	human := map[string]any{"system": "http", "actorId": "maria.torres", "actorKind": "human"}

	status, raw := proposeJudgmentHTTP(t, ts, fromID, toID, "supports", "razón", "judgment-propose-human-1", human)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", status, raw)
	}
	if !strings.Contains(raw, "PROPOSAL_UNAUTHORIZED") {
		t.Fatalf("body %q must carry PROPOSAL_UNAUTHORIZED", raw)
	}
}

// TestHTTPJudgmentProposeUnknownRelation: conflicts_with remains a legacy
// sync/discovery marker and is never proposable — 400 RELATION_NOT_PROPOSABLE.
func TestHTTPJudgmentProposeUnknownRelation(t *testing.T) {
	ts, _, _, fromID, toID := judgmentServer(t)

	status, raw := proposeJudgmentHTTP(t, ts, fromID, toID, "conflicts_with", "razón", "judgment-propose-rel-1", agentProposalSource())
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", status, raw)
	}
	if !strings.Contains(raw, "RELATION_NOT_PROPOSABLE") {
		t.Fatalf("body %q must carry RELATION_NOT_PROPOSABLE", raw)
	}
}

// TestHTTPJudgmentConfirmRequiresAuthentication: confirm without Authorization
// gets AUTHENTICATION_REQUIRED (401) — no silent fallback to the shared token
// guard.
func TestHTTPJudgmentConfirmRequiresAuthentication(t *testing.T) {
	ts, _, _, fromID, toID := judgmentServer(t)
	proposed := proposeJudgmentFixture(t, ts, fromID, toID, "judgment-confirm-noauth-propose")

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/judgments/"+proposed.JudgmentID+"/confirm",
		"", "judgment-confirm-noauth-1",
		map[string]string{"resolution": "x", "expectedJudgmentHash": "x"})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", status, raw)
	}
	if !strings.Contains(raw, "AUTHENTICATION_REQUIRED") {
		t.Fatalf("body %q must carry AUTHENTICATION_REQUIRED", raw)
	}
}

// TestHTTPJudgmentConfirmRejectsCallerSuppliedAuthority is the regression test
// of the ADR-003 gap closure for adjudication: any authority field in the
// STRICT confirm body is REJECTED with 400, never ignored.
func TestHTTPJudgmentConfirmRejectsCallerSuppliedAuthority(t *testing.T) {
	ts, _, token, fromID, toID := judgmentServer(t)
	proposed := proposeJudgmentFixture(t, ts, fromID, toID, "judgment-confirm-strict-propose")
	hash := core.ComputeJudgmentHash(proposed.Judgment)

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
			body := map[string]any{"resolution": "x", "expectedJudgmentHash": hash}
			for k, v := range c.field {
				body[k] = v
			}
			status, raw := approvalHTTP(t, http.MethodPost,
				ts.URL+"/accounting/judgments/"+proposed.JudgmentID+"/confirm",
				token, "judgment-confirm-strict-1", body)
			if status != http.StatusBadRequest {
				t.Fatalf("%s: status = %d, want 400; body %s", c.name, status, raw)
			}
			if !strings.Contains(raw, "INVALID") {
				t.Fatalf("%s: body %q must carry the INVALID code", c.name, raw)
			}
		})
	}
}

// TestHTTPJudgmentConfirmHappyPath: the seeded controller confirms the proposed
// judgment against the CURRENT reviewed hash → 200 with the confirmed judgment,
// the resolution and the verified adjudicator recorded.
func TestHTTPJudgmentConfirmHappyPath(t *testing.T) {
	ts, _, token, fromID, toID := judgmentServer(t)
	proposed := proposeJudgmentFixture(t, ts, fromID, toID, "judgment-confirm-happy-propose")
	hash := core.ComputeJudgmentHash(proposed.Judgment)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/judgments/"+proposed.JudgmentID+"/confirm",
		token, "judgment-confirm-happy-1",
		map[string]string{"resolution": "El crédito se difiere; el mayor prevalece.", "expectedJudgmentHash": hash})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", status, raw)
	}
	var result core.ConfirmJudgmentResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.JudgmentID != proposed.JudgmentID {
		t.Errorf("judgmentId = %q, want %q", result.JudgmentID, proposed.JudgmentID)
	}
	if result.JudgmentEventID == "" {
		t.Error("judgmentEventId must be set on confirmation")
	}
	if result.IdempotentReplay {
		t.Error("fresh confirmation must not be a replay")
	}
	j := result.Judgment
	if j.Status != core.JudgmentConfirmed {
		t.Errorf("status = %q, want confirmed", j.Status)
	}
	if j.Resolution != "El crédito se difiere; el mayor prevalece." {
		t.Errorf("resolution = %q, want the professional resolution", j.Resolution)
	}
	if j.Adjudicator == nil || j.Adjudicator.SubjectID != "maria.torres" {
		t.Errorf("adjudicator = %+v, want the verified subject maria.torres", j.Adjudicator)
	}
	if j.PolicyVersion == "" {
		t.Error("policyVersion must be stamped on confirmation")
	}
	if j.DecidedAt == "" {
		t.Error("decidedAt must be set on confirmation")
	}
}

// TestHTTPJudgmentConfirmHashMismatch: a reviewer who saw different bytes gets
// 409 JUDGMENT_HASH_MISMATCH whose body carries ONLY the error envelope plus
// the two judgment hashes — never judgment content.
func TestHTTPJudgmentConfirmHashMismatch(t *testing.T) {
	ts, _, token, fromID, toID := judgmentServer(t)
	proposed := proposeJudgmentFixture(t, ts, fromID, toID, "judgment-confirm-stale-propose")
	currentHash := core.ComputeJudgmentHash(proposed.Judgment)
	staleHash := strings.Repeat("0", len(currentHash))
	if staleHash == currentHash {
		t.Fatal("fixture: stale hash must differ from the current hash")
	}

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/judgments/"+proposed.JudgmentID+"/confirm",
		token, "judgment-confirm-stale-1",
		map[string]string{"resolution": "x", "expectedJudgmentHash": staleHash})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", status, raw)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	// ONLY error + the two hashes — the "ONLY the two hashes" contract.
	if len(body) != 3 {
		t.Fatalf("error body carries %d top-level keys, want exactly 3 (error, expectedJudgmentHash, actualJudgmentHash): %s", len(body), raw)
	}
	if _, ok := body["expectedJudgmentHash"]; !ok {
		t.Error("error body missing expectedJudgmentHash")
	}
	if _, ok := body["actualJudgmentHash"]; !ok {
		t.Error("error body missing actualJudgmentHash")
	}
	var detail struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body["error"], &detail); err != nil {
		t.Fatalf("decode error detail: %v", err)
	}
	if detail.Code != "JUDGMENT_HASH_MISMATCH" {
		t.Errorf("error code = %q, want JUDGMENT_HASH_MISMATCH", detail.Code)
	}
	var got struct {
		Expected string `json:"expectedJudgmentHash"`
		Actual   string `json:"actualJudgmentHash"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode hashes: %v", err)
	}
	if got.Expected != staleHash || got.Actual != currentHash {
		t.Errorf("hashes = expected %s actual %s, want expected %s actual %s",
			got.Expected, got.Actual, staleHash, currentHash)
	}
}

// TestHTTPJudgmentRejectTerminal: the authenticated rejection becomes terminal
// (200, rejected, human reason stored as the resolution); a later confirm of
// the same judgment fails with 409 INVALID_JUDGMENT_TRANSITION.
func TestHTTPJudgmentRejectTerminal(t *testing.T) {
	ts, _, token, fromID, toID := judgmentServer(t)
	proposed := proposeJudgmentFixture(t, ts, fromID, toID, "judgment-reject-propose")
	hash := core.ComputeJudgmentHash(proposed.Judgment)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/judgments/"+proposed.JudgmentID+"/reject",
		token, "judgment-reject-1",
		map[string]string{"reason": "El XML no corresponde al CDR.", "expectedJudgmentHash": hash})
	if status != http.StatusOK {
		t.Fatalf("reject status = %d, want 200; body %s", status, raw)
	}
	var result core.RejectJudgmentResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode reject result: %v", err)
	}
	if result.Judgment.Status != core.JudgmentRejected {
		t.Errorf("status = %q, want rejected (terminal)", result.Judgment.Status)
	}
	if result.Judgment.Resolution != "El XML no corresponde al CDR." {
		t.Errorf("resolution = %q, want the human reason", result.Judgment.Resolution)
	}
	if result.Judgment.Adjudicator == nil || result.Judgment.Adjudicator.SubjectID != "maria.torres" {
		t.Errorf("adjudicator = %+v, want the verified subject", result.Judgment.Adjudicator)
	}
	if result.JudgmentEventID == "" {
		t.Error("judgmentEventId must be set on rejection")
	}

	// A rejected judgment is terminal: confirming it now is an invalid
	// transition (409), never a silent re-open.
	status, raw = approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/judgments/"+proposed.JudgmentID+"/confirm",
		token, "judgment-confirm-after-reject-1",
		map[string]string{"resolution": "x", "expectedJudgmentHash": hash})
	if status != http.StatusConflict {
		t.Fatalf("confirm-after-reject status = %d, want 409; body %s", status, raw)
	}
	if !strings.Contains(raw, "INVALID_JUDGMENT_TRANSITION") {
		t.Fatalf("body %q must carry INVALID_JUDGMENT_TRANSITION", raw)
	}
}

// TestHTTPJudgmentWithdrawDifferentProposerDenied: withdrawal requires the SAME
// exact proposer identity (provenance continuity). A different agent source →
// 403 PROPOSAL_UNAUTHORIZED; the SAME source → 200 withdrawn.
func TestHTTPJudgmentWithdrawDifferentProposerDenied(t *testing.T) {
	ts, _, _, fromID, toID := judgmentServer(t)
	proposed := proposeJudgmentFixture(t, ts, fromID, toID, "judgment-withdraw-propose")

	other := map[string]any{"system": "http", "actorId": "agent-2", "actorKind": "agent", "session": "sess-2"}
	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/judgments/"+proposed.JudgmentID+"/withdraw",
		"", "judgment-withdraw-other-1", map[string]any{"source": other})
	if status != http.StatusForbidden {
		t.Fatalf("different proposer: status = %d, want 403; body %s", status, raw)
	}
	if !strings.Contains(raw, "PROPOSAL_UNAUTHORIZED") {
		t.Fatalf("body %q must carry PROPOSAL_UNAUTHORIZED", raw)
	}

	// The SAME proposer identity withdraws its own proposal.
	status, raw = approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/judgments/"+proposed.JudgmentID+"/withdraw",
		"", "judgment-withdraw-same-1", map[string]any{"source": agentProposalSource()})
	if status != http.StatusOK {
		t.Fatalf("same proposer: status = %d, want 200; body %s", status, raw)
	}
	var result core.WithdrawJudgmentResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode withdraw result: %v", err)
	}
	if result.Judgment.Status != core.JudgmentWithdrawn {
		t.Errorf("status = %q, want withdrawn (terminal)", result.Judgment.Status)
	}
	if result.JudgmentEventID == "" {
		t.Error("judgmentEventId must be set on withdrawal")
	}
}

// TestHTTPJudgmentLegacyJudgeFailClosed: the deprecated caller-declared
// accounting_judge tool (design §4) fails closed with AUTHENTICATION_REQUIRED
// as an in-band tool result — the legacy path no longer writes.
func TestHTTPJudgmentLegacyJudgeFailClosed(t *testing.T) {
	m, api := newTestMCP(t)
	conflict := saveDemoFact(t, api, "exception/mismatch-002", "Diferencia de saldo",
		"Diferencia S/ 1,284.30 entre mayor y SIRE", "2026-07-28T00:00:00Z")

	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_judge",
		"arguments": map[string]any{
			"conflictId": conflict.Identity.ID,
			"resolution": "resolución profesional",
			"actorId":    "maria.torres",
		},
	})
	if response.Error != nil {
		t.Fatalf("fail-closed legacy judge must be a tool result, not a JSON-RPC error: %+v", response.Error)
	}
	var output toolCallOutput
	if err := json.Unmarshal(response.Result, &output); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if !output.IsError {
		t.Fatal("isError = false, want true (legacy path fails closed)")
	}
	if len(output.Content) == 0 || !strings.Contains(output.Content[0]["text"], "AUTHENTICATION_REQUIRED") {
		t.Fatalf("error text must carry AUTHENTICATION_REQUIRED: %v", output.Content)
	}

	// Fail-closed means NO write: no decision memory was created.
	if _, err := api.GetByTopic("judgment/"+conflict.Identity.ID, conflict.Scope); err == nil {
		t.Fatal("legacy Judge must not write a decision memory")
	}
}
