// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.5.0 close
// foundation at the store boundary
// (docs/architecture/close-intelligence-v0.5.md §2.3 and §2.5):
//
//   - approving a VALID monthly close projects the period_closures row to
//     'closed' inside the approval's BEGIN IMMEDIATE transaction, and emits
//     memory_approved THEN memory_closed atomically on the close memory's
//     receipt chain;
//   - a non-close approval projects nothing;
//   - a second close for an already-closed period is PERIOD_ALREADY_CLOSED;
//   - the write gate (PERIOD_CLOSED) blocks every period-scoped mutation
//     (Save, status/supersession transitions, evidence/rule links, judgment
//     proposal and decisions touching either endpoint) with NO partial
//     mutation, while reads and the approval itself stay exempt;
//   - two concurrent approvals of closes for the same period serialize on
//     BEGIN IMMEDIATE into exactly ONE closure projection.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// closeInputForTest builds a VALID monthly close save: kind=summary,
// fiscalEffect=closing, topic closing/CIERRE-<period> in the given scope.
func closeInputForTest(t *testing.T, scope core.Scope, what string) core.SaveInput {
	t.Helper()
	if scope.Period == "" {
		t.Fatal("close fixtures require a perioded company scope")
	}
	return core.SaveInput{
		TopicKey: core.CloseTopicPrefix + scope.Period,
		Title:    "Monthly close " + scope.Period,
		Kind:     core.KindSummary,
		Scope:    scope,
		Content: core.Content{
			What:    what,
			Why:     "month-end close",
			Where:   "Peru",
			Learned: "close snapshot frozen",
		},
		FiscalEffect: core.FiscalEffectClosing,
		EffectiveAt:  testT,
		Source:       testAgentSource,
		Confidence:   0.8,
	}
}

// saveAndApproveClose saves a valid close and approves it with the fixture
// controller, returning the close memory id, the approval result and the event.
func saveAndApproveClose(t *testing.T, s *SQLiteStore, scope core.Scope, what, requestID string) (string, core.ApprovalResult) {
	t.Helper()
	// The caller seeds the identity chain ONCE (companies.UNIQUE(tenant_id, ruc)
	// makes re-seeding fail); this helper only saves + approves.
	saved, err := s.Save(closeInputForTest(t, scope, what))
	if err != nil {
		t.Fatalf("save close: %v", err)
	}
	id := saved.Memory.Identity.ID
	principal := controllerPrincipal(t)
	result, err := approve(s, id, currentEnvelope(saved), requestID, principal)
	if err != nil {
		t.Fatalf("approve close: %v", err)
	}
	return id, result
}

// readClosure returns the period_closures row for the exact scope tuple, or ok=false.
func readClosure(t *testing.T, s *SQLiteStore, scope core.Scope) (status, closeMemoryID, eventID, closedAt string, ok bool) {
	t.Helper()
	err := s.db.QueryRow(`
		SELECT status, close_memory_id, COALESCE(close_approval_event_id, ''), closed_at
		FROM period_closures
		WHERE tenant_id = ? AND company_id = ? AND fiscal_period_id = ?`,
		scope.OrganizationID, scope.CompanyID, scope.Period,
	).Scan(&status, &closeMemoryID, &eventID, &closedAt)
	if err != nil {
		return "", "", "", "", false
	}
	return status, closeMemoryID, eventID, closedAt, true
}

// TestApproveCloseProjectsClosure verifies the projection: approving a valid
// monthly close upserts period_closures to 'closed' with the close memory, the
// approval event and the captured timestamp.
func TestApproveCloseProjectsClosure(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	closeID, result := saveAndApproveClose(t, s, scope, "approval projects closure", "req-close-proj")

	status, storedCloseID, eventID, closedAt, ok := readClosure(t, s, scope)
	if !ok {
		t.Fatal("approving a valid close must project a period_closures row")
	}
	if status != "closed" {
		t.Fatalf("status = %q, want closed", status)
	}
	if storedCloseID != closeID {
		t.Fatalf("close_memory_id = %q, want %q", storedCloseID, closeID)
	}
	if eventID != result.ApprovalEventID {
		t.Fatalf("close_approval_event_id = %q, want the approval event %q", eventID, result.ApprovalEventID)
	}
	if closedAt != result.ApprovedAt {
		t.Fatalf("closed_at = %q, want the approval timestamp %q", closedAt, result.ApprovedAt)
	}
}

// TestNonCloseApprovalDoesNotProject verifies that approving a gated memory that
// is NOT a valid close (kind=rule with fiscalEffect=closing) projects nothing.
func TestNonCloseApprovalDoesNotProject(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, err := s.Save(gatedInput("tax.igv.not-close", "rule with closing effect is not a close"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	principal := controllerPrincipal(t)
	if _, err := approve(s, id, currentEnvelope(saved), "req-not-close", principal); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, _, _, _, ok := readClosure(t, s, testScope(testRucA)); ok {
		t.Fatal("a non-close approval must NOT project a period closure")
	}
}

// TestDuplicateCloseApprovalRejected verifies the approval-time guard: a second
// pending close for the same (tenant, company, period) — in a DIFFERENT chain
// (different RUC, saved BEFORE the period closed) — is PERIOD_ALREADY_CLOSED at
// approval, the projection keeps the FIRST close, and the losing approval rolls
// back completely (its memory stays pending_review, no event).
func TestDuplicateCloseApprovalRejected(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)

	// Both closes exist BEFORE the first approval: the write gate only engages
	// once the period is closed, so the second chain (different RUC → different
	// chain key) is saved as pending_review first.
	firstSaved, err := s.Save(closeInputForTest(t, scope, "first close"))
	if err != nil {
		t.Fatalf("save first close: %v", err)
	}
	firstClose := firstSaved.Memory.Identity.ID
	secondScope := testScope(testRucB)
	secondSaved, err := s.Save(closeInputForTest(t, secondScope, "duplicate close"))
	if err != nil {
		t.Fatalf("save second close: %v", err)
	}
	secondID := secondSaved.Memory.Identity.ID
	principal := controllerPrincipal(t)

	// Approve the first close → the period closes.
	if _, err := approve(s, firstClose, currentEnvelope(firstSaved), "req-close-first", principal); err != nil {
		t.Fatalf("approve first close: %v", err)
	}

	// Approving the SECOND close for the same closed period is rejected.
	_, err = approve(s, secondID, currentEnvelope(secondSaved), "req-close-second", principal)
	if auth.Code(err) != auth.CodePeriodAlreadyClosed {
		t.Fatalf("code = %q, want PERIOD_ALREADY_CLOSED (err: %v)", auth.Code(err), err)
	}

	status, storedCloseID, _, _, ok := readClosure(t, s, scope)
	if !ok || status != "closed" || storedCloseID != firstClose {
		t.Fatalf("projection must keep the first close (status=%q id=%q)", status, storedCloseID)
	}
	// The losing approval rolled back: no event, memory still pending_review.
	if n := countRows(t, s, `SELECT COUNT(*) FROM approval_events WHERE memory_id = ?`, secondID); n != 0 {
		t.Fatalf("approval_events rows = %d for the rejected close, want 0", n)
	}
	mem, ok := s.FindByID(secondID)
	if !ok || mem.Status != core.StatusPendingReview {
		t.Fatalf("rejected close status = %v, want pending_review (approval rolled back)", mem.Status)
	}
}

// TestSaveBlockedInClosedPeriodNoPartialMutation is the write-gate core proof:
// a save into a closed exact company period fails with typed PERIOD_CLOSED
// carrying the scope tuple + close memory ID, and NO row, transition or receipt
// is left behind.
func TestSaveBlockedInClosedPeriodNoPartialMutation(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	closeID, _ := saveAndApproveClose(t, s, scope, "block saves", "req-close-block")

	beforeObs := countRows(t, s, `SELECT COUNT(*) FROM observations`)
	beforeTrans := countRows(t, s, `SELECT COUNT(*) FROM transition_log`)
	beforeRec := countRows(t, s, `SELECT COUNT(*) FROM receipts`)
	beforeIdem := countRows(t, s, `SELECT COUNT(*) FROM idempotency_keys`)

	_, err := s.Save(validInput("tax.after.close", "must be blocked"))
	if auth.Code(err) != auth.CodePeriodClosed {
		t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
	}
	var periodErr *auth.Error
	if !errors.As(err, &periodErr) {
		t.Fatalf("error must be typed *auth.Error, got %T", err)
	}
	if periodErr.TenantID != scope.OrganizationID || periodErr.CompanyID != scope.CompanyID ||
		periodErr.FiscalPeriodID != scope.Period || periodErr.CloseMemoryID != closeID {
		t.Fatalf("PERIOD_CLOSED must carry the scope tuple + close memory id, got %+v", periodErr)
	}

	if n := countRows(t, s, `SELECT COUNT(*) FROM observations`); n != beforeObs {
		t.Fatalf("observations = %d, want %d — the blocked save must leave NO row", n, beforeObs)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM transition_log`); n != beforeTrans {
		t.Fatalf("transition_log = %d, want %d — no transition may survive", n, beforeTrans)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts`); n != beforeRec {
		t.Fatalf("receipts = %d, want %d — no receipt may survive", n, beforeRec)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM idempotency_keys`); n != beforeIdem {
		t.Fatalf("idempotency_keys = %d, want %d — no reservation may survive", n, beforeIdem)
	}
}

// TestGateBlocksPeriodMutations covers the gate at every enumerated mutation:
// status transitions (reject/void/supersede), evidence/rule links, explicit
// supersession, and judgment proposal/decisions when EITHER endpoint is in the
// closed period — all fail with PERIOD_CLOSED and no partial state.
func TestGateBlocksPeriodMutations(t *testing.T) {
	s := newTestStore(t)
	scope := testScope(testRucA)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})

	// Pre-close fixtures: two in-period memories, a successor for the
	// supersession probe, and a proposed judgment whose endpoints are in-period.
	a, err := s.Save(validInput("tax.gate.a", "in-period memory a"))
	if err != nil {
		t.Fatalf("save a: %v", err)
	}
	b, err := s.Save(validInput("tax.gate.b", "in-period memory b"))
	if err != nil {
		t.Fatalf("save b: %v", err)
	}
	succ, err := s.Save(validInput("tax.gate.succ", "successor"))
	if err != nil {
		t.Fatalf("save successor: %v", err)
	}
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: a.Memory.Identity.ID, ToID: b.Memory.Identity.ID, Relation: core.RelationSupports,
		Reason: "proposal before close", RequestID: "req-gate-propose",
	}, testProposer)
	proposedHash := core.ComputeJudgmentHash(proposed.Judgment)

	// Close the period AFTER the fixtures exist.
	_, _ = saveAndApproveClose(t, s, scope, "gate blocks mutations", "req-close-gate")

	aID, succID := a.Memory.Identity.ID, succ.Memory.Identity.ID
	meta := core.TransitionMeta{Actor: "reviewer-1", ActorKind: core.ActorKindHuman, Timestamp: testT}
	principal := controllerPrincipal(t)

	t.Run("reject transition", func(t *testing.T) {
		if _, err := s.ApplyStatusTransition(aID, core.StatusRejected, meta); auth.Code(err) != auth.CodePeriodClosed {
			t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("void transition", func(t *testing.T) {
		if _, err := s.ApplyStatusTransition(aID, core.StatusVoided, meta); auth.Code(err) != auth.CodePeriodClosed {
			t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("supersede transition", func(t *testing.T) {
		if _, err := s.ApplyStatusTransition(aID, core.StatusSuperseded, meta); auth.Code(err) != auth.CodePeriodClosed {
			t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("explicit supersession", func(t *testing.T) {
		if _, err := s.SupersedeExplicit(aID, succID, meta); auth.Code(err) != auth.CodePeriodClosed {
			t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("evidence link", func(t *testing.T) {
		if err := s.AddEvidenceLink(aID, "evidence://blocked.pdf", "cli"); auth.Code(err) != auth.CodePeriodClosed {
			t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("rule link", func(t *testing.T) {
		if err := s.AddRuleLink(aID, "policy/blocked/v1", "cli"); auth.Code(err) != auth.CodePeriodClosed {
			t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("judgment proposal", func(t *testing.T) {
		// A new tuple over the PRE-CLOSE in-period memories with a DIFFERENT
		// relation: the proposal must be gated (both endpoints in the closed
		// period).
		_, err := s.ProposeJudgment(context.Background(), core.ProposeJudgmentCommand{
			FromID: aID, ToID: b.Memory.Identity.ID, Relation: core.RelationContradicts,
			Reason: "proposal in closed period", RequestID: "req-gate-propose-2",
		}, testProposer)
		if auth.Code(err) != auth.CodePeriodClosed {
			t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("judgment confirm", func(t *testing.T) {
		if _, err := s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
			JudgmentID: proposed.JudgmentID, Resolution: "confirmed", ExpectedJudgmentHash: proposedHash, RequestID: "req-gate-confirm",
		}, principal, authz.NewJudgmentPolicy()); auth.Code(err) != auth.CodePeriodClosed {
			t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("judgment reject", func(t *testing.T) {
		if _, err := s.RejectJudgment(context.Background(), core.RejectJudgmentCommand{
			JudgmentID: proposed.JudgmentID, Reason: "rejected", ExpectedJudgmentHash: proposedHash, RequestID: "req-gate-reject",
		}, principal, authz.NewJudgmentPolicy()); auth.Code(err) != auth.CodePeriodClosed {
			t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
		}
	})

	// No partial mutation anywhere: the in-period memories are untouched and the
	// pre-close proposal is still proposed.
	memA, _ := s.FindByID(aID)
	if memA.Status != core.StatusActive {
		t.Fatalf("memory a status = %v, want active (untouched)", memA.Status)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM evidence_links`); n != 0 {
		t.Fatalf("evidence_links = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM rule_links`); n != 0 {
		t.Fatalf("rule_links = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgments WHERE status = 'proposed' AND id = ?`, proposed.JudgmentID); n != 1 {
		t.Fatalf("proposed judgment must survive the blocked decisions untouched, rows = %d", n)
	}
}

// TestGateExemptions verifies the gate applies ONLY to exact company scopes WITH
// a period: reads, institutional saves, unperioded company saves, other-period
// saves and approvals all keep working after the close.
func TestGateExemptions(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	closeID, _ := saveAndApproveClose(t, s, scope, "exemptions", "req-close-exempt")

	// Reads are exempt.
	if _, ok := s.FindByID(closeID); !ok {
		t.Fatal("reads must keep working in a closed period")
	}

	// Institutional save is exempt (no company/period to gate).
	inst := validInput("tax.institutional", "institutional note")
	inst.Scope = core.Scope{Kind: core.ScopeKindInstitutional}
	if _, err := s.Save(inst); err != nil {
		t.Fatalf("institutional save must be exempt: %v", err)
	}

	// A company scope WITHOUT a period is exempt.
	unperioded := validInput("tax.unperioded", "no period")
	unperioded.Scope = core.Scope{
		Kind: core.ScopeKindCompany, OrganizationID: testOrgID, CompanyID: "acme", RUC: testRucA, Period: "",
	}
	if _, err := s.Save(unperioded); err != nil {
		t.Fatalf("unperioded company save must be exempt: %v", err)
	}

	// A DIFFERENT period of the same company stays writable.
	other := validInput("tax.other.period", "other period")
	other.Scope = testScope(testRucA)
	other.Scope.Period = "202402"
	if _, err := s.Save(other); err != nil {
		t.Fatalf("other-period save must be exempt: %v", err)
	}

	// Approving gated memories in OTHER periods still works (the identity
	// chain was seeded once at the top).
	gated := gatedInput("tax.gate.other", "gated in another period")
	gated.Scope = testScope(testRucA)
	gated.Scope.Period = "202403"
	saved, err := s.Save(gated)
	if err != nil {
		t.Fatalf("save gated in other period: %v", err)
	}
	if _, err := approve(s, saved.Memory.Identity.ID, currentEnvelope(saved), "req-exempt-approve", controllerPrincipal(t)); err != nil {
		t.Fatalf("approval in another period must be exempt: %v", err)
	}
}

// TestReopenedProjectionAcceptsReClose verifies the upsert semantics for the
// reopened path (ReopenPeriod ships next batch; here the projection is flipped
// to 'reopened' directly): a NEW close approval for the reopened period
// re-closes the projection with the new close memory and resets reopen fields.
func TestReopenedProjectionAcceptsReClose(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	_, _ = saveAndApproveClose(t, s, scope, "first close", "req-close-reopen-first")

	// Explicit-reopen stand-in: flip the projection (ReopenPeriod owns this
	// transition next batch).
	if _, err := s.db.Exec(`UPDATE period_closures SET status = 'reopened', reopened_at = ?, reopened_by_subject_id = 'subject-1', reopen_reason = 'correction' WHERE tenant_id = ? AND company_id = ? AND fiscal_period_id = ?`,
		testT, scope.OrganizationID, scope.CompanyID, scope.Period); err != nil {
		t.Fatalf("flip projection to reopened: %v", err)
	}

	// A new close (different chain) approves and re-closes the period.
	secondScope := testScope(testRucB)
	saved, err := s.Save(closeInputForTest(t, secondScope, "re-close"))
	if err != nil {
		t.Fatalf("save re-close: %v", err)
	}
	principal := controllerPrincipal(t)
	result, err := approve(s, saved.Memory.Identity.ID, currentEnvelope(saved), "req-close-reclose", principal)
	if err != nil {
		t.Fatalf("approve re-close: %v", err)
	}

	status, storedCloseID, eventID, _, ok := readClosure(t, s, scope)
	if !ok || status != "closed" || storedCloseID != saved.Memory.Identity.ID || eventID != result.ApprovalEventID {
		t.Fatalf("re-close projection = (%s, %s, %s), want (closed, %s, %s)",
			status, storedCloseID, eventID, saved.Memory.Identity.ID, result.ApprovalEventID)
	}
	var reopenAt, reopenBy, reopenReason sql.NullString
	if err := s.db.QueryRow(`SELECT reopened_at, reopened_by_subject_id, reopen_reason FROM period_closures WHERE tenant_id = ? AND company_id = ? AND fiscal_period_id = ?`,
		scope.OrganizationID, scope.CompanyID, scope.Period).Scan(&reopenAt, &reopenBy, &reopenReason); err != nil {
		t.Fatalf("read reopen fields: %v", err)
	}
	if reopenAt.Valid || reopenBy.Valid || reopenReason.Valid {
		t.Fatalf("re-close must reset the reopen fields, got %v/%v/%v", reopenAt, reopenBy, reopenReason)
	}
}

// TestApproveCloseEmitsMemoryClosedReceipt verifies the atomic receipt pair: a
// close approval emits memory_approved THEN memory_closed on the same subject
// chain, the close payload is v0.5.0 and verifies offline with the parity key.
func TestApproveCloseEmitsMemoryClosedReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	saved, err := s.Save(closeInputForTest(t, scope, "receipt close"))
	if err != nil {
		t.Fatalf("save close: %v", err)
	}
	id := saved.Memory.Identity.ID
	principal := controllerPrincipal(t)
	result, err := approve(s, id, currentEnvelope(saved), "req-close-receipt", principal)
	if err != nil {
		t.Fatalf("approve close: %v", err)
	}

	receipts := readReceipts(t, s)
	if len(receipts) != 3 {
		t.Fatalf("receipts = %d, want 3 (recorded, approved, closed)", len(receipts))
	}
	approved := byAction(t, receipts, string(core.ReceiptActionMemoryApproved))
	closed := byAction(t, receipts, string(core.ReceiptActionMemoryClosed))

	if closed.SubjectID != id {
		t.Fatalf("memory_closed subject = %q, want the close memory %q", closed.SubjectID, id)
	}
	if closed.PreviousReceiptHash != approved.ReceiptHash {
		t.Fatalf("memory_closed previousReceiptHash = %q, want the memory_approved receipt hash %q (atomic chain)", closed.PreviousReceiptHash, approved.ReceiptHash)
	}
	if closed.PayloadJSON == "" {
		t.Fatal("memory_closed must carry a payload")
	}
	payload := closed.payload(t)
	if payload.Version != core.ReceiptPayloadVersionV05 {
		t.Fatalf("memory_closed payload version = %q, want %q", payload.Version, core.ReceiptPayloadVersionV05)
	}
	if payload.Action != core.ReceiptActionMemoryClosed || payload.SubjectID != id {
		t.Fatalf("memory_closed payload = %s/%s, want memory_closed/%s", payload.Action, payload.SubjectID, id)
	}
	if payload.ReviewedEnvelopeHash != result.ReviewedEnvelopeHash || payload.ResultingEnvelopeHash != result.ResultingEnvelopeHash {
		t.Fatalf("memory_closed must cover H1/H2: (%s, %s), want (%s, %s)",
			payload.ReviewedEnvelopeHash, payload.ResultingEnvelopeHash, result.ReviewedEnvelopeHash, result.ResultingEnvelopeHash)
	}
	if payload.Reason != "approved by fixture reviewer" || payload.PrincipalID != principal.SubjectID() {
		t.Fatalf("memory_closed must cover reason + principal: (%q, %q)", payload.Reason, payload.PrincipalID)
	}
	verifyStored(t, signer, closed)

	// The memory_approved receipt stayed v0.4.0 (existing actions keep their bytes).
	approvedPayload := approved.payload(t)
	if approvedPayload.Version != core.ReceiptPayloadVersion {
		t.Fatalf("memory_approved version = %q, want %q (existing actions unchanged)", approvedPayload.Version, core.ReceiptPayloadVersion)
	}
	verifyStored(t, signer, approved)
}

// TestConcurrentCloseApprovalsSingleProjection is the cross-process race proof:
// two independently opened stores approve the SAME close memory concurrently.
// BEGIN IMMEDIATE serializes them: exactly ONE approval wins (the loser reads
// the committed approved status → ALREADY_DECIDED) and exactly ONE closure
// projection survives. Run with -race.
func TestConcurrentCloseApprovalsSingleProjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-close-concurrent.db")
	s1 := openTestStorePath(t, path)
	s2 := openTestStorePath(t, path)

	seedAcmeIdentity(t, s1, []auth.AccountingRole{auth.RoleController})
	saved, err := s1.Save(closeInputForTest(t, testScope(testRucA), "concurrent close"))
	if err != nil {
		t.Fatalf("save close: %v", err)
	}
	id := saved.Memory.Identity.ID
	expected := currentEnvelope(saved)
	principal := controllerPrincipal(t)
	policy := authz.NewApprovalPolicy()

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			st := s1
			if i == 1 {
				st = s2
			}
			_, err := st.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
				MemoryID:             id,
				ExpectedEnvelopeHash: expected,
				Reason:               "concurrent close approval",
				RequestID:            fmt.Sprintf("req-close-concurrent-%d", i),
			}, principal, policy)
			errCh <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errCh)

	approved, decided := 0, 0
	for err := range errCh {
		switch {
		case err == nil:
			approved++
		case auth.Code(err) == auth.CodeAlreadyDecided:
			decided++
		default:
			t.Fatalf("unexpected error %v (code %q)", err, auth.Code(err))
		}
	}
	if approved != 1 || decided != 1 {
		t.Fatalf("approved=%d decided=%d, want exactly 1 APPROVED and 1 ALREADY_DECIDED", approved, decided)
	}

	if n := countRows(t, s1, `SELECT COUNT(*) FROM period_closures`); n != 1 {
		t.Fatalf("period_closures rows = %d, want exactly 1 (BEGIN IMMEDIATE serializes the projection)", n)
	}
	if n := countRows(t, s1, `SELECT COUNT(*) FROM approval_events`); n != 1 {
		t.Fatalf("approval_events rows = %d, want exactly 1", n)
	}
	mem, ok := s1.FindByID(id)
	if !ok || mem.Status != core.StatusApproved {
		t.Fatalf("close status = %v, want approved", mem.Status)
	}
}
