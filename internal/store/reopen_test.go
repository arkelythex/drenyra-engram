// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the explicit controller
// reopen (v0.5.0 close foundation, docs/architecture/close-intelligence-v0.5.md
// §2.3/§2.5) at the store boundary:
//
//   - ReopenPeriod flips the period_closures projection to 'reopened' inside ONE
//     BEGIN IMMEDIATE transaction, appends the immutable period_closure_events
//     row (action 'reopened') and emits the memory_reopened receipt on the close
//     memory's chain — and NEVER edits the approved close memory;
//   - after the reopen the period is writable again (a save succeeds) and a NEW
//     close revision re-closes it on approval;
//   - the guards (never-closed period, wrong expected close, already-reopened
//     period, non-controller role, cross-tenant principal) fail with the frozen
//     codes and no partial mutation;
//   - (tenant, requestId) idempotency replays the stored outcome and rejects a
//     request-id reuse with a different command/principal;
//   - a signing failure rolls the whole reopen back.
package store

import (
	"context"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// reopen runs one ReopenPeriod with the given request id and the controller
// fixture principal.
func reopen(s *SQLiteStore, scope core.Scope, expectedCloseID, reason, requestID string, principal auth.VerifiedApprovalPrincipal) (core.ReopenPeriodResult, error) {
	return s.ReopenPeriod(context.Background(), core.ReopenPeriodCommand{
		Scope:                 scope,
		ExpectedCloseMemoryID: expectedCloseID,
		Reason:                reason,
		RequestID:             requestID,
	}, principal, authz.NewApprovalPolicy())
}

// readReopenEvent returns the period_closure_events row for the exact scope and
// request id, or ok=false.
func readReopenEvent(t *testing.T, s *SQLiteStore, scope core.Scope, requestID string) (eventID, action, closeID, subject, reason string, ok bool) {
	t.Helper()
	err := s.db.QueryRow(`
		SELECT id, action, close_memory_id, subject_id, reason
		FROM period_closure_events
		WHERE tenant_id = ? AND company_id = ? AND fiscal_period_id = ? AND request_id = ?`,
		scope.OrganizationID, scope.CompanyID, scope.Period, requestID,
	).Scan(&eventID, &action, &closeID, &subject, &reason)
	if err != nil {
		return "", "", "", "", "", false
	}
	return eventID, action, closeID, subject, reason, true
}

// TestReopenPeriodHappyPath is the core reopen proof: the controller reopen
// flips the projection to 'reopened' with the reopen provenance, appends the
// immutable closure event, emits the memory_reopened receipt on the close
// memory's chain, restores period writability and NEVER edits the approved close
// memory (status + envelope stay exactly as the approval left them).
func TestReopenPeriodHappyPath(t *testing.T) {
	s, signer := receiptStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	closeID, approval := saveAndApproveClose(t, s, scope, "reopen happy", "req-reopen-close")

	principal := controllerPrincipal(t)
	result, err := reopen(s, scope, closeID, "correccion de julio", "req-reopen-1", principal)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if result.EventID == "" || result.Status != "reopened" {
		t.Fatalf("result = %+v, want a reopened result with an event id", result)
	}
	if result.CloseMemoryID != closeID {
		t.Fatalf("closeMemoryId = %q, want %q", result.CloseMemoryID, closeID)
	}
	if result.PrincipalSubjectID != principal.SubjectID() || result.PolicyVersion != authz.PolicyVersion {
		t.Fatalf("result principal/policy = (%q, %q), want (%q, %q)",
			result.PrincipalSubjectID, result.PolicyVersion, principal.SubjectID(), authz.PolicyVersion)
	}
	if result.IdempotentReplay {
		t.Fatal("a fresh reopen must not report idempotentReplay")
	}

	// The projection flipped to 'reopened' with the reopen provenance.
	status, storedCloseID, _, _, ok := readClosure(t, s, scope)
	if !ok || status != "reopened" || storedCloseID != closeID {
		t.Fatalf("projection = (%s, %s), want (reopened, %s)", status, storedCloseID, closeID)
	}
	var reopenedAt, reopenedBy, reopenReason string
	if err := s.db.QueryRow(`SELECT COALESCE(reopened_at,''), COALESCE(reopened_by_subject_id,''), COALESCE(reopen_reason,'') FROM period_closures WHERE tenant_id = ? AND company_id = ? AND fiscal_period_id = ?`,
		scope.OrganizationID, scope.CompanyID, scope.Period).Scan(&reopenedAt, &reopenedBy, &reopenReason); err != nil {
		t.Fatalf("read reopen fields: %v", err)
	}
	if reopenedAt == "" || reopenedBy != principal.SubjectID() || reopenReason != "correccion de julio" {
		t.Fatalf("reopen fields = (%q, %q, %q), want (timestamp, subject, reason)", reopenedAt, reopenedBy, reopenReason)
	}

	// The immutable closure-event ledger row (action 'reopened').
	eventID, action, eventCloseID, eventSubject, eventReason, ok := readReopenEvent(t, s, scope, "req-reopen-1")
	if !ok {
		t.Fatal("no period_closure_events row for the reopen")
	}
	if eventID != result.EventID || action != "reopened" || eventCloseID != closeID ||
		eventSubject != principal.SubjectID() || eventReason != "correccion de julio" {
		t.Fatalf("event = (%s, %s, %s, %s, %s), want (id, reopened, closeID, subject, reason)",
			eventID, action, eventCloseID, eventSubject, eventReason)
	}

	// The memory_reopened receipt on the close memory's chain: 4 receipts now
	// (recorded, approved, closed, reopened); the reopened one chains on the
	// memory_closed receipt, carries the v0.5.0 payload and verifies offline.
	receipts := readReceipts(t, s)
	if len(receipts) != 4 {
		t.Fatalf("receipts = %d, want 4 (recorded, approved, closed, reopened)", len(receipts))
	}
	closed := byAction(t, receipts, string(core.ReceiptActionMemoryClosed))
	reopened := byAction(t, receipts, string(core.ReceiptActionMemoryReopened))
	if reopened.SubjectID != closeID {
		t.Fatalf("memory_reopened subject = %q, want the close memory %q", reopened.SubjectID, closeID)
	}
	if reopened.PreviousReceiptHash != closed.ReceiptHash {
		t.Fatalf("memory_reopened previousReceiptHash = %q, want the memory_closed receipt hash %q (atomic chain)", reopened.PreviousReceiptHash, closed.ReceiptHash)
	}
	payload := reopened.payload(t)
	if payload.Version != core.ReceiptPayloadVersionV05 {
		t.Fatalf("memory_reopened payload version = %q, want %q", payload.Version, core.ReceiptPayloadVersionV05)
	}
	if payload.Action != core.ReceiptActionMemoryReopened || payload.Reason != "correccion de julio" ||
		payload.PrincipalID != principal.SubjectID() || payload.FiscalPeriodID != scope.Period {
		t.Fatalf("memory_reopened payload = %+v, want reopened/scope/reason/principal coverage", payload)
	}
	verifyStored(t, signer, reopened)

	// The period is writable again: an ordinary in-period save succeeds.
	if _, err := s.Save(validInput("tax.after.reopen", "post-reopen save")); err != nil {
		t.Fatalf("save after reopen must succeed: %v", err)
	}

	// The approved close memory was NEVER edited: status stays approved and the
	// stored envelope stays exactly H2 of the approval.
	mem, ok := s.FindByID(closeID)
	if !ok || mem.Status != core.StatusApproved {
		t.Fatalf("close status = %v, want approved (the reopen never edits the memory)", mem.Status)
	}
	if mem.EnvelopeHash != approval.ResultingEnvelopeHash {
		t.Fatalf("close envelope = %q, want the approval's H2 %q (unchanged)", mem.EnvelopeHash, approval.ResultingEnvelopeHash)
	}
}

// TestReopenPeriodGuards freezes the guard matrix: a never-closed period, a
// stale expected close, an already-reopened period, a non-controller role and a
// cross-tenant principal all fail with the frozen codes and leave the projection
// untouched.
func TestReopenPeriodGuards(t *testing.T) {
	t.Run("never closed", func(t *testing.T) {
		s := newTestStore(t)
		seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
		_, err := reopen(s, testScope(testRucA), "some-close-id", "why", "req-reopen-never", controllerPrincipal(t))
		if auth.Code(err) != auth.CodeInvalidTransition {
			t.Fatalf("code = %q, want INVALID_TRANSITION (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("wrong expected close", func(t *testing.T) {
		s := newTestStore(t)
		seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
		scope := testScope(testRucA)
		closeID, _ := saveAndApproveClose(t, s, scope, "guard wrong close", "req-reopen-wrong-close")
		_, err := reopen(s, scope, closeID+"-stale", "why", "req-reopen-wrong", controllerPrincipal(t))
		if auth.Code(err) != auth.CodeInvalidTransition {
			t.Fatalf("code = %q, want INVALID_TRANSITION (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("already reopened", func(t *testing.T) {
		s := newTestStore(t)
		seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
		scope := testScope(testRucA)
		closeID, _ := saveAndApproveClose(t, s, scope, "guard double reopen", "req-reopen-double-close")
		if _, err := reopen(s, scope, closeID, "first reopen", "req-reopen-double-1", controllerPrincipal(t)); err != nil {
			t.Fatalf("first reopen: %v", err)
		}
		_, err := reopen(s, scope, closeID, "second reopen", "req-reopen-double-2", controllerPrincipal(t))
		if auth.Code(err) != auth.CodeInvalidTransition {
			t.Fatalf("code = %q, want INVALID_TRANSITION (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("accountant denied", func(t *testing.T) {
		s := newTestStore(t)
		seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
		scope := testScope(testRucA)
		closeID, _ := saveAndApproveClose(t, s, scope, "guard role", "req-reopen-role-close")
		accountant := mustPrincipal(t, fixtureSessionStore(testOrgID, "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
		_, err := reopen(s, scope, closeID, "why", "req-reopen-role", accountant)
		if auth.Code(err) != auth.CodeRoleNotAuthorized {
			t.Fatalf("code = %q, want ROLE_NOT_AUTHORIZED (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("cross tenant denied", func(t *testing.T) {
		s := newTestStore(t)
		seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
		scope := testScope(testRucA)
		closeID, _ := saveAndApproveClose(t, s, scope, "guard tenant", "req-reopen-tenant-close")
		otherTenant := mustPrincipal(t, fixtureSessionStore("other-org", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
		_, err := reopen(s, scope, closeID, "why", "req-reopen-tenant", otherTenant)
		if auth.Code(err) != auth.CodeTenantScopeMismatch {
			t.Fatalf("code = %q, want TENANT_SCOPE_MISMATCH (err: %v)", auth.Code(err), err)
		}
	})

	// After every guard failure the projection is untouched: the last sub-store
	// assertion is repeated here for the role case (a denied policy leaves the
	// row closed).
	t.Run("no partial mutation on denial", func(t *testing.T) {
		s := newTestStore(t)
		seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
		scope := testScope(testRucA)
		closeID, _ := saveAndApproveClose(t, s, scope, "guard rollback", "req-reopen-rollback-close")
		accountant := mustPrincipal(t, fixtureSessionStore(testOrgID, "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
		if _, err := reopen(s, scope, closeID, "why", "req-reopen-rollback", accountant); auth.Code(err) != auth.CodeRoleNotAuthorized {
			t.Fatalf("code = %q, want ROLE_NOT_AUTHORIZED", auth.Code(err))
		}
		status, storedCloseID, _, _, ok := readClosure(t, s, scope)
		if !ok || status != "closed" || storedCloseID != closeID {
			t.Fatalf("projection after denial = (%s, %s), want (closed, %s)", status, storedCloseID, closeID)
		}
		if _, _, _, _, _, ok := readReopenEvent(t, s, scope, "req-reopen-rollback"); ok {
			t.Fatal("a denied reopen must not append a closure event")
		}
	})
}

// TestReopenPeriodIdempotency freezes the (tenant, requestId) replay contract: a
// repeated reopen with the SAME intent returns the stored outcome with
// IdempotentReplay=true; a request-id reuse with a different reason or a
// different expected close is IDEMPOTENCY_CONFLICT.
func TestReopenPeriodIdempotency(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	closeID, _ := saveAndApproveClose(t, s, scope, "idempotent close", "req-reopen-idem-close")
	principal := controllerPrincipal(t)

	first, err := reopen(s, scope, closeID, "reason one", "req-reopen-idem", principal)
	if err != nil {
		t.Fatalf("first reopen: %v", err)
	}
	replay, err := reopen(s, scope, closeID, "reason one", "req-reopen-idem", principal)
	if err != nil {
		t.Fatalf("replay reopen: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("a repeated reopen must report idempotentReplay")
	}
	if replay.EventID != first.EventID || replay.ReopenedAt != first.ReopenedAt || replay.Status != "reopened" {
		t.Fatalf("replay = %+v, want the stored outcome %+v", replay, first)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM period_closure_events WHERE tenant_id = ? AND request_id = ?`, scope.OrganizationID, "req-reopen-idem"); n != 1 {
		t.Fatalf("closure events = %d, want exactly 1 (replay appends nothing)", n)
	}

	// A reuse with a DIFFERENT reason is a conflict.
	if _, err := reopen(s, scope, closeID, "different reason", "req-reopen-idem", principal); auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Fatalf("code = %q, want IDEMPOTENCY_CONFLICT (err: %v)", auth.Code(err), err)
	}
	// A reuse with a DIFFERENT expected close is a conflict.
	if _, err := reopen(s, scope, closeID+"-stale", "reason one", "req-reopen-idem", principal); auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Fatalf("code = %q, want IDEMPOTENCY_CONFLICT (err: %v)", auth.Code(err), err)
	}
}

// TestReopenPeriodSignerFailureRollsBack proves the atomicity contract: when the
// receipt signer fails, the whole reopen rolls back — the projection stays
// 'closed', no closure event survives and no receipt is minted.
func TestReopenPeriodSignerFailureRollsBack(t *testing.T) {
	s, signer := receiptStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	closeID, _ := saveAndApproveClose(t, s, scope, "signer close", "req-reopen-signer-close")

	before := countRows(t, s, `SELECT COUNT(*) FROM receipts`)
	signer.fail = true
	_, err := reopen(s, scope, closeID, "why", "req-reopen-signer", controllerPrincipal(t))
	if err == nil {
		t.Fatal("a signer failure must fail the reopen")
	}
	signer.fail = false

	status, storedCloseID, _, _, ok := readClosure(t, s, scope)
	if !ok || status != "closed" || storedCloseID != closeID {
		t.Fatalf("projection after signer failure = (%s, %s), want (closed, %s)", status, storedCloseID, closeID)
	}
	if _, _, _, _, _, ok := readReopenEvent(t, s, scope, "req-reopen-signer"); ok {
		t.Fatal("a failed reopen must not append a closure event")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts`); n != before {
		t.Fatalf("receipts = %d, want %d — no receipt may survive a failed reopen", n, before)
	}
}

// TestReopenThenReClose is the §2.3 correction cycle at the store boundary: close
// → reopen → NEW close revision of the SAME close topic → approve → the period is
// closed again with the new close memory and the reopen fields reset.
func TestReopenThenReClose(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)
	firstID, _ := saveAndApproveClose(t, s, scope, "first close", "req-reclose-close-1")
	principal := controllerPrincipal(t)

	if _, err := reopen(s, scope, firstID, "correction window", "req-reclose-reopen", principal); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	// The new close is a NEW REVISION of the SAME close chain (the topic is
	// canonical); the Save auto-supersedes the first approved revision.
	secondSaved, err := s.Save(closeInputForTest(t, scope, "re-close revision"))
	if err != nil {
		t.Fatalf("save re-close revision: %v", err)
	}
	secondID := secondSaved.Memory.Identity.ID
	if secondSaved.Memory.Revision != 2 {
		t.Fatalf("re-close revision = %d, want 2 (same close topic chain)", secondSaved.Memory.Revision)
	}
	if secondSaved.Memory.Identity.TopicKey != (core.CloseTopicPrefix + scope.Period) {
		t.Fatalf("re-close topic = %q, want %q", secondSaved.Memory.Identity.TopicKey, core.CloseTopicPrefix+scope.Period)
	}

	result, err := approve(s, secondID, currentEnvelope(secondSaved), "req-reclose-approve", principal)
	if err != nil {
		t.Fatalf("approve re-close: %v", err)
	}
	status, storedCloseID, eventID, _, ok := readClosure(t, s, scope)
	if !ok || status != "closed" || storedCloseID != secondID || eventID != result.ApprovalEventID {
		t.Fatalf("re-close projection = (%s, %s, %s), want (closed, %s, %s)",
			status, storedCloseID, eventID, secondID, result.ApprovalEventID)
	}
	var reopenAt, reopenBy, reopenReason string
	if err := s.db.QueryRow(`SELECT COALESCE(reopened_at,''), COALESCE(reopened_by_subject_id,''), COALESCE(reopen_reason,'') FROM period_closures WHERE tenant_id = ? AND company_id = ? AND fiscal_period_id = ?`,
		scope.OrganizationID, scope.CompanyID, scope.Period).Scan(&reopenAt, &reopenBy, &reopenReason); err != nil {
		t.Fatalf("read reopen fields: %v", err)
	}
	if reopenAt != "" || reopenBy != "" || reopenReason != "" {
		t.Fatalf("re-close must reset the reopen fields, got %q/%q/%q", reopenAt, reopenBy, reopenReason)
	}
	// The first close revision is superseded by the re-close revision (normal
	// immutable-chain semantics) and the period is gated again.
	first, _ := s.FindByID(firstID)
	if first.Status != core.StatusSuperseded {
		t.Fatalf("first close status = %v, want superseded (new revision of the same chain)", first.Status)
	}
	if _, err := s.Save(validInput("tax.after.reclose", "must be blocked again")); auth.Code(err) != auth.CodePeriodClosed {
		t.Fatalf("code = %q, want PERIOD_CLOSED after the re-close (err: %v)", auth.Code(err), err)
	}
}
