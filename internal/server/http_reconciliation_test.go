// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the AUTHENTICATED
// first-class reconciliation HTTP surface (v0.5.0 — adjudicated
// reconciliations, design §3.2/§6): proposals and withdrawals carry a
// provenance-only source (agent|system — never authority); confirm/reject derive
// the principal ONLY from the Authorization credential and their STRICT bodies
// reject any authority field (the ADR-003 gap closure for adjudication); the
// domain amounts travel as JSON integers (int64 cents, never floats); errors map
// to the frozen status/code table and only RECONCILIATION_HASH_MISMATCH carries
// details (the two hashes, never content); a proposal touching a CLOSED period
// fails PERIOD_CLOSED → 409.
//
// Fixtures are end-to-end: identities and sessions are seeded directly on the
// test SQLite store and principals are minted through the REAL resolver — the
// same path production middleware uses.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// seedReconciliationObservations saves two company-scoped facts of the demo
// scope (cmp_org / cmp_01 / 202607) — the pair a proposal reconciles.
func seedReconciliationObservations(t *testing.T, api *API) (leftID, rightID string) {
	t.Helper()
	left := saveDemoFact(t, api, "reconciliation/left-001", "Saldo de mayor 4011",
		"Saldo de mayor S/ 10,000.00", "2026-07-10T00:00:00Z")
	right := saveDemoFact(t, api, "reconciliation/right-001", "Saldo de SIRE 4011",
		"Saldo de SIRE S/ 10,000.00", "2026-07-10T00:00:00Z")
	return left.Identity.ID, right.Identity.ID
}

// reconciliationServer builds a test server plus a demo-tenant controller
// identity (token returned) and the two observations a proposal needs.
func reconciliationServer(t *testing.T) (*httptest.Server, *API, string, string, string) {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	token := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleController})
	leftID, rightID := seedReconciliationObservations(t, api)
	return ts, api, token, leftID, rightID
}

// proposeReconciliationHTTP proposes over the fixtures with the given
// provenance source and an explicit idempotency key. The domain amounts are
// int64 cents (the JSON wire contract; never floats).
func proposeReconciliationHTTP(t *testing.T, ts *httptest.Server, leftID, rightID string, amounts map[string]any, source map[string]any, key string) (int, string) {
	t.Helper()
	body := map[string]any{
		"leftMemoryId": leftID, "rightMemoryId": rightID,
		"method": "extracto_contable", "currency": "PEN",
		"leftAmountCents": amounts["left"], "rightAmountCents": amounts["right"],
		"toleranceCents": amounts["tolerance"],
		"reason":         "diferencia de saldo entre mayor y SIRE",
		"source":         source,
	}
	return approvalHTTP(t, http.MethodPost, ts.URL+"/accounting/reconciliations", "", key, body)
}

// proposeReconciliationFixture proposes with the canonical agent source and
// returns the decoded result (fails the test on any non-201 response).
func proposeReconciliationFixture(t *testing.T, ts *httptest.Server, leftID, rightID, key string) core.ProposeReconciliationResult {
	t.Helper()
	status, raw := proposeReconciliationHTTP(t, ts, leftID, rightID,
		map[string]any{"left": 1000000, "right": 1000000, "tolerance": 5000},
		agentProposalSource(), key)
	if status != http.StatusCreated {
		t.Fatalf("propose status = %d, want 201; body %s", status, raw)
	}
	var result core.ProposeReconciliationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode propose result: %v", err)
	}
	return result
}

// TestHTTPReconciliationProposeHappyPath: an agent source proposes over two
// existing observations → 201 with the proposed reconciliation; the provenance
// is preserved, the tenant/company are derived from the observations and the
// variance is engine-derived (left − right), never caller-supplied.
func TestHTTPReconciliationProposeHappyPath(t *testing.T) {
	ts, _, _, leftID, rightID := reconciliationServer(t)

	status, raw := proposeReconciliationHTTP(t, ts, leftID, rightID,
		map[string]any{"left": 1000000, "right": 984000, "tolerance": 16000},
		agentProposalSource(), "reconciliation-propose-1")
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", status, raw)
	}
	var result core.ProposeReconciliationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.ReconciliationID == "" || result.ReconciliationID != result.Reconciliation.ID {
		t.Errorf("reconciliationId = %q, want the reconciliation's id %q", result.ReconciliationID, result.Reconciliation.ID)
	}
	if result.IdempotentReplay {
		t.Error("fresh proposal must not be a replay")
	}
	r := result.Reconciliation
	if r.Status != core.ReconciliationProposed {
		t.Errorf("status = %q, want proposed", r.Status)
	}
	if r.LeftMemoryID != leftID || r.RightMemoryID != rightID {
		t.Errorf("pair = %s/%s, want %s/%s", r.LeftMemoryID, r.RightMemoryID, leftID, rightID)
	}
	if r.Method != "extracto_contable" || r.Currency != "PEN" {
		t.Errorf("method/currency = %q/%q, want extracto_contable/PEN", r.Method, r.Currency)
	}
	if r.LeftAmountCents != 1000000 || r.RightAmountCents != 984000 {
		t.Errorf("amounts = %d/%d, want 1000000/984000 (int64 cents)", r.LeftAmountCents, r.RightAmountCents)
	}
	if r.VarianceCents != 16000 {
		t.Errorf("varianceCents = %d, want 16000 (engine-derived left − right)", r.VarianceCents)
	}
	if r.ToleranceCents != 16000 {
		t.Errorf("toleranceCents = %d, want 16000", r.ToleranceCents)
	}
	if r.TenantID != "cmp_org" || r.CompanyID != "cmp_01" {
		t.Errorf("scope = %s/%s, want cmp_org/cmp_01 (derived from the observations)", r.TenantID, r.CompanyID)
	}
	if r.FiscalPeriodID != "202607" {
		t.Errorf("fiscalPeriodId = %q, want 202607 (matching observation periods)", r.FiscalPeriodID)
	}
	wantProposer := core.Source{System: "http", ActorID: "agent-1", ActorKind: core.ActorKindAgent, Session: "sess-1"}
	if r.Proposer != wantProposer {
		t.Errorf("proposer = %+v, want %+v (provenance preserved, never authority)", r.Proposer, wantProposer)
	}
	if r.ProposalReason != "diferencia de saldo entre mayor y SIRE" || r.Resolution != "" {
		t.Errorf("reason/resolution = %q/%q, want reason/empty", r.ProposalReason, r.Resolution)
	}
	if r.DecidedAt != "" {
		t.Errorf("decidedAt = %q, want empty for an open proposal", r.DecidedAt)
	}
}

// TestHTTPReconciliationProposeHumanSourceDenied: a human source is
// provenance-only and cannot propose — 403 PROPOSAL_UNAUTHORIZED.
func TestHTTPReconciliationProposeHumanSourceDenied(t *testing.T) {
	ts, _, _, leftID, rightID := reconciliationServer(t)
	human := map[string]any{"system": "http", "actorId": "maria.torres", "actorKind": "human"}

	status, raw := proposeReconciliationHTTP(t, ts, leftID, rightID,
		map[string]any{"left": 100, "right": 100, "tolerance": 0}, human, "reconciliation-propose-human-1")
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", status, raw)
	}
	if !strings.Contains(raw, "PROPOSAL_UNAUTHORIZED") {
		t.Fatalf("body %q must carry PROPOSAL_UNAUTHORIZED", raw)
	}
}

// TestHTTPReconciliationConfirmRequiresAuthentication: confirm without
// Authorization gets AUTHENTICATION_REQUIRED (401) — no silent fallback to the
// shared token guard.
func TestHTTPReconciliationConfirmRequiresAuthentication(t *testing.T) {
	ts, _, _, leftID, rightID := reconciliationServer(t)
	proposed := proposeReconciliationFixture(t, ts, leftID, rightID, "reconciliation-confirm-noauth-propose")

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/reconciliations/"+proposed.ReconciliationID+"/confirm",
		"", "reconciliation-confirm-noauth-1",
		map[string]string{"resolution": "x", "expectedReconciliationHash": "x"})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", status, raw)
	}
	if !strings.Contains(raw, "AUTHENTICATION_REQUIRED") {
		t.Fatalf("body %q must carry AUTHENTICATION_REQUIRED", raw)
	}
}

// TestHTTPReconciliationConfirmRejectsCallerSuppliedAuthority is the regression
// test of the ADR-003 gap closure for adjudication: any authority field in the
// STRICT confirm body is REJECTED with 400, never ignored.
func TestHTTPReconciliationConfirmRejectsCallerSuppliedAuthority(t *testing.T) {
	ts, _, token, leftID, rightID := reconciliationServer(t)
	proposed := proposeReconciliationFixture(t, ts, leftID, rightID, "reconciliation-confirm-strict-propose")
	hash := core.ComputeReconciliationHash(proposed.Reconciliation)

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
			body := map[string]any{"resolution": "x", "expectedReconciliationHash": hash}
			for k, v := range c.field {
				body[k] = v
			}
			status, raw := approvalHTTP(t, http.MethodPost,
				ts.URL+"/accounting/reconciliations/"+proposed.ReconciliationID+"/confirm",
				token, "reconciliation-confirm-strict-1", body)
			if status != http.StatusBadRequest {
				t.Fatalf("%s: status = %d, want 400; body %s", c.name, status, raw)
			}
			if !strings.Contains(raw, "INVALID") {
				t.Fatalf("%s: body %q must carry the INVALID code", c.name, raw)
			}
		})
	}
}

// TestHTTPReconciliationConfirmHappyPath: the seeded controller confirms the
// proposed reconciliation against the CURRENT reviewed hash → 200 with the
// confirmed reconciliation, the resolution and the verified adjudicator
// recorded, and the observation relation leftMemoryId --reconciles-->
// rightMemoryId projected atomically.
func TestHTTPReconciliationConfirmHappyPath(t *testing.T) {
	ts, api, token, leftID, rightID := reconciliationServer(t)
	proposed := proposeReconciliationFixture(t, ts, leftID, rightID, "reconciliation-confirm-happy-propose")
	hash := core.ComputeReconciliationHash(proposed.Reconciliation)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/reconciliations/"+proposed.ReconciliationID+"/confirm",
		token, "reconciliation-confirm-happy-1",
		map[string]string{"resolution": "El mayor y el SIRE concilian tras el ajuste.", "expectedReconciliationHash": hash})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", status, raw)
	}
	var result core.ConfirmReconciliationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.ReconciliationID != proposed.ReconciliationID {
		t.Errorf("reconciliationId = %q, want %q", result.ReconciliationID, proposed.ReconciliationID)
	}
	if result.ReconciliationEventID == "" {
		t.Error("reconciliationEventId must be set on confirmation")
	}
	if result.IdempotentReplay {
		t.Error("fresh confirmation must not be a replay")
	}
	r := result.Reconciliation
	if r.Status != core.ReconciliationConfirmed {
		t.Errorf("status = %q, want confirmed", r.Status)
	}
	if r.Resolution != "El mayor y el SIRE concilian tras el ajuste." {
		t.Errorf("resolution = %q, want the professional resolution", r.Resolution)
	}
	if r.Adjudicator == nil || r.Adjudicator.SubjectID != "maria.torres" {
		t.Errorf("adjudicator = %+v, want the verified subject maria.torres", r.Adjudicator)
	}
	if r.PolicyVersion == "" {
		t.Error("policyVersion must be stamped on confirmation")
	}
	if r.DecidedAt == "" {
		t.Error("decidedAt must be set on confirmation")
	}
	// Confirmation projects exactly one observation relation: left --reconciles-->
	// right (rejected/withdrawn proposals project none).
	relations, err := api.Relations()
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	found := false
	for _, rel := range relations {
		if rel.FromID == leftID && rel.ToID == rightID && rel.Relation == core.RelationReconciles {
			found = true
		}
	}
	if !found {
		t.Fatalf("confirmation must project the reconciles relation %s -> %s; got %+v", leftID, rightID, relations)
	}
}

// TestHTTPReconciliationConfirmHashMismatch: a reviewer who saw different bytes
// gets 409 RECONCILIATION_HASH_MISMATCH whose body carries ONLY the error
// envelope plus the two reconciliation hashes — never reconciliation content.
func TestHTTPReconciliationConfirmHashMismatch(t *testing.T) {
	ts, _, token, leftID, rightID := reconciliationServer(t)
	proposed := proposeReconciliationFixture(t, ts, leftID, rightID, "reconciliation-confirm-stale-propose")
	currentHash := core.ComputeReconciliationHash(proposed.Reconciliation)
	staleHash := strings.Repeat("0", len(currentHash))
	if staleHash == currentHash {
		t.Fatal("fixture: stale hash must differ from the current hash")
	}

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/reconciliations/"+proposed.ReconciliationID+"/confirm",
		token, "reconciliation-confirm-stale-1",
		map[string]string{"resolution": "x", "expectedReconciliationHash": staleHash})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", status, raw)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	// ONLY error + the two hashes — the "ONLY the two hashes" contract.
	if len(body) != 3 {
		t.Fatalf("error body carries %d top-level keys, want exactly 3 (error, expectedReconciliationHash, actualReconciliationHash): %s", len(body), raw)
	}
	if _, ok := body["expectedReconciliationHash"]; !ok {
		t.Error("error body missing expectedReconciliationHash")
	}
	if _, ok := body["actualReconciliationHash"]; !ok {
		t.Error("error body missing actualReconciliationHash")
	}
	var detail struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body["error"], &detail); err != nil {
		t.Fatalf("decode error detail: %v", err)
	}
	if detail.Code != "RECONCILIATION_HASH_MISMATCH" {
		t.Errorf("error code = %q, want RECONCILIATION_HASH_MISMATCH", detail.Code)
	}
	var got struct {
		Expected string `json:"expectedReconciliationHash"`
		Actual   string `json:"actualReconciliationHash"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode hashes: %v", err)
	}
	if got.Expected != staleHash || got.Actual != currentHash {
		t.Errorf("hashes = expected %s actual %s, want expected %s actual %s",
			got.Expected, got.Actual, staleHash, currentHash)
	}
}

// TestHTTPReconciliationRejectTerminal: the authenticated rejection becomes
// terminal (200, rejected, human reason stored as the resolution); a later
// confirm of the same reconciliation fails with 409
// INVALID_RECONCILIATION_TRANSITION.
func TestHTTPReconciliationRejectTerminal(t *testing.T) {
	ts, _, token, leftID, rightID := reconciliationServer(t)
	proposed := proposeReconciliationFixture(t, ts, leftID, rightID, "reconciliation-reject-propose")
	hash := core.ComputeReconciliationHash(proposed.Reconciliation)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/reconciliations/"+proposed.ReconciliationID+"/reject",
		token, "reconciliation-reject-1",
		map[string]string{"reason": "El extracto no respalda el saldo.", "expectedReconciliationHash": hash})
	if status != http.StatusOK {
		t.Fatalf("reject status = %d, want 200; body %s", status, raw)
	}
	var result core.RejectReconciliationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode reject result: %v", err)
	}
	if result.Reconciliation.Status != core.ReconciliationRejected {
		t.Errorf("status = %q, want rejected (terminal)", result.Reconciliation.Status)
	}
	if result.Reconciliation.Resolution != "El extracto no respalda el saldo." {
		t.Errorf("resolution = %q, want the human reason", result.Reconciliation.Resolution)
	}
	if result.Reconciliation.Adjudicator == nil || result.Reconciliation.Adjudicator.SubjectID != "maria.torres" {
		t.Errorf("adjudicator = %+v, want the verified subject", result.Reconciliation.Adjudicator)
	}
	if result.ReconciliationEventID == "" {
		t.Error("reconciliationEventId must be set on rejection")
	}

	// A rejected reconciliation is terminal: confirming it now is an invalid
	// transition (409), never a silent re-open.
	status, raw = approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/reconciliations/"+proposed.ReconciliationID+"/confirm",
		token, "reconciliation-confirm-after-reject-1",
		map[string]string{"resolution": "x", "expectedReconciliationHash": hash})
	if status != http.StatusConflict {
		t.Fatalf("confirm-after-reject status = %d, want 409; body %s", status, raw)
	}
	if !strings.Contains(raw, "INVALID_RECONCILIATION_TRANSITION") {
		t.Fatalf("body %q must carry INVALID_RECONCILIATION_TRANSITION", raw)
	}
}

// TestHTTPReconciliationWithdrawDifferentProposerDenied: withdrawal requires the
// SAME exact proposer identity (provenance continuity). A different agent source
// → 403 PROPOSAL_UNAUTHORIZED; the SAME source → 200 withdrawn.
func TestHTTPReconciliationWithdrawDifferentProposerDenied(t *testing.T) {
	ts, _, _, leftID, rightID := reconciliationServer(t)
	proposed := proposeReconciliationFixture(t, ts, leftID, rightID, "reconciliation-withdraw-propose")

	other := map[string]any{"system": "http", "actorId": "agent-2", "actorKind": "agent", "session": "sess-2"}
	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/reconciliations/"+proposed.ReconciliationID+"/withdraw",
		"", "reconciliation-withdraw-other-1", map[string]any{"source": other})
	if status != http.StatusForbidden {
		t.Fatalf("different proposer: status = %d, want 403; body %s", status, raw)
	}
	if !strings.Contains(raw, "PROPOSAL_UNAUTHORIZED") {
		t.Fatalf("body %q must carry PROPOSAL_UNAUTHORIZED", raw)
	}

	// The SAME proposer identity withdraws its own proposal.
	status, raw = approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/reconciliations/"+proposed.ReconciliationID+"/withdraw",
		"", "reconciliation-withdraw-same-1", map[string]any{"source": agentProposalSource()})
	if status != http.StatusOK {
		t.Fatalf("same proposer: status = %d, want 200; body %s", status, raw)
	}
	var result core.WithdrawReconciliationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode withdraw result: %v", err)
	}
	if result.Reconciliation.Status != core.ReconciliationWithdrawn {
		t.Errorf("status = %q, want withdrawn (terminal)", result.Reconciliation.Status)
	}
	if result.ReconciliationEventID == "" {
		t.Error("reconciliationEventId must be set on withdrawal")
	}
}

// TestHTTPReconciliationConfirmCrossTenantDenied: the controller policy is
// tenant-scoped — a controller of ANOTHER tenant can never confirm the
// reconciliation (403 TENANT_SCOPE_MISMATCH), even with a valid hash.
func TestHTTPReconciliationConfirmCrossTenantDenied(t *testing.T) {
	ts, api, _, leftID, rightID := reconciliationServer(t)
	proposed := proposeReconciliationFixture(t, ts, leftID, rightID, "reconciliation-cross-tenant-propose")
	hash := core.ComputeReconciliationHash(proposed.Reconciliation)

	otherToken := seedApprovalIdentity(t, api, "other_org", "other_01", "20600995804",
		[]auth.AccountingRole{auth.RoleController})
	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/reconciliations/"+proposed.ReconciliationID+"/confirm",
		otherToken, "reconciliation-cross-tenant-1",
		map[string]string{"resolution": "x", "expectedReconciliationHash": hash})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", status, raw)
	}
	if !strings.Contains(raw, "TENANT_SCOPE_MISMATCH") {
		t.Fatalf("body %q must carry TENANT_SCOPE_MISMATCH", raw)
	}
}

// TestHTTPReconciliationProposePeriodClosed: when an endpoint observation sits in
// a CLOSED exact company period, the proposal fails 409 PERIOD_CLOSED (design
// §2.3 — the write gate applies to reconciliation proposal paths).
func TestHTTPReconciliationProposePeriodClosed(t *testing.T) {
	api := closeAcceptanceStore(t)
	token := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleController})
	// The endpoint observations are seeded BEFORE the close (a closed period
	// blocks every later write, so the pair must pre-date the closure).
	leftID, rightID := seedReconciliationObservations(t, api)
	sourceFact := seedJulyPeriod(t, api)
	closeMemory := createJulyClose(t, api, sourceFact, "cierre de julio")
	principal := resolvePrincipal(t, api, token)
	st := api.Store.(*store.SQLiteStore)
	// Approve the close through the real authenticated service (the projection
	// flips to closed atomically with the approval).
	h1 := core.ComputeEnvelopeHash(closeMemory)
	if _, err := ApproveMemory(context.Background(), st, authz.NewApprovalPolicy(), core.ApproveMemoryCommand{
		MemoryID:             closeMemory.Identity.ID,
		ExpectedEnvelopeHash: h1,
		Reason:               "cierre revisado y conforme",
		RequestID:            "req-close-reconciliation-test",
	}, principal); err != nil {
		t.Fatalf("controller approval of the close: %v", err)
	}

	ts := httptest.NewServer(NewHTTPServer(api, "").Handler())
	t.Cleanup(ts.Close)

	status, raw := proposeReconciliationHTTP(t, ts, leftID, rightID,
		map[string]any{"left": 100, "right": 100, "tolerance": 0},
		agentProposalSource(), "reconciliation-period-closed-1")
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", status, raw)
	}
	if !strings.Contains(raw, "PERIOD_CLOSED") {
		t.Fatalf("body %q must carry PERIOD_CLOSED", raw)
	}
}
