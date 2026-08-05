// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the atomic first-class
// reconciliation path (v0.5.0 — docs/architecture/close-intelligence-v0.5.md
// §3) at the store boundary: propose → confirm/reject/withdraw as ONE BEGIN
// IMMEDIATE transaction per operation, the frozen error codes, the
// RECONCILIATION_HASH_MISMATCH details carrier, the confirm-time reconciles
// relation projection, idempotency replay/conflict, the correction supersession
// routing (ReconciliationSuccessorOf), the closed-period write gate on both
// endpoints, and the atomic reconciliation receipts.
//
// Principals are minted through auth.Resolver.Authenticate with a fake session
// store — the SAME path production middleware uses. Agents can never
// confirm/reject: the signatures REQUIRE a verified principal (compile-level
// contract — an agent Source is provenance only). The cross-process concurrency
// proof lives in reconciliation_concurrency_test.go.
package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// reconciliationProposer is the canonical provenance Source of the
// reconciliation proposal fixtures: agent|system ONLY — provenance continuity,
// never authority.
var reconciliationProposer = core.Source{System: "go-test", ActorID: "recon-agent", ActorKind: core.ActorKindAgent, Session: "sess-recon"}

var otherReconciliationProposer = core.Source{System: "go-test", ActorID: "recon-agent-2", ActorKind: core.ActorKindAgent, Session: "sess-recon-2"}

// reconcileContext saves two observations plus the acme identity chain the
// authenticated decisions need (the confirm event's adjudicator_membership_id
// FK references memberships).
func reconcileContext(t *testing.T, s *SQLiteStore) (leftID, rightID string) {
	t.Helper()
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	a, err := s.Save(validInput("tax.reconcile.left", "ledger observation"))
	if err != nil {
		t.Fatalf("save left observation: %v", err)
	}
	b, err := s.Save(validInput("tax.reconcile.right", "bank observation"))
	if err != nil {
		t.Fatalf("save right observation: %v", err)
	}
	return a.Memory.Identity.ID, b.Memory.Identity.ID
}

// baseReconcileCmd is the canonical propose command of the fixtures (signed
// int64 cents; variance is engine-derived).
func baseReconcileCmd(leftID, rightID string) core.ProposeReconciliationCommand {
	return core.ProposeReconciliationCommand{
		LeftMemoryID:     leftID,
		RightMemoryID:    rightID,
		Method:           "trial-balance",
		Currency:         "PEN",
		LeftAmountCents:  150000,
		RightAmountCents: 149500,
		ToleranceCents:   1000,
		Reason:           "bank statement matches ledger within tolerance",
		RequestID:        "req-reconcile",
	}
}

func proposeReconciliation(t *testing.T, s *SQLiteStore, cmd core.ProposeReconciliationCommand, caller core.Source) core.ProposeReconciliationResult {
	t.Helper()
	res, err := s.ProposeReconciliation(context.Background(), cmd, caller)
	if err != nil {
		t.Fatalf("propose reconciliation: %v", err)
	}
	return res
}

func confirmReconciliation(t *testing.T, s *SQLiteStore, reconciliationID, expectedHash, requestID, resolution string, principal auth.VerifiedApprovalPrincipal) core.ConfirmReconciliationResult {
	t.Helper()
	res, err := s.ConfirmReconciliation(context.Background(), core.ConfirmReconciliationCommand{
		ReconciliationID:           reconciliationID,
		Resolution:                 resolution,
		ExpectedReconciliationHash: expectedHash,
		RequestID:                  requestID,
	}, principal, authz.NewReconciliationPolicy())
	if err != nil {
		t.Fatalf("confirm reconciliation: %v", err)
	}
	return res
}

func rejectReconciliation(t *testing.T, s *SQLiteStore, reconciliationID, expectedHash, requestID, reason string, principal auth.VerifiedApprovalPrincipal) core.RejectReconciliationResult {
	t.Helper()
	res, err := s.RejectReconciliation(context.Background(), core.RejectReconciliationCommand{
		ReconciliationID:           reconciliationID,
		Reason:                     reason,
		ExpectedReconciliationHash: expectedHash,
		RequestID:                  requestID,
	}, principal, authz.NewReconciliationPolicy())
	if err != nil {
		t.Fatalf("reject reconciliation: %v", err)
	}
	return res
}

func withdrawReconciliation(t *testing.T, s *SQLiteStore, reconciliationID, requestID string, caller core.Source) core.WithdrawReconciliationResult {
	t.Helper()
	res, err := s.WithdrawReconciliation(context.Background(), core.WithdrawReconciliationCommand{
		ReconciliationID: reconciliationID,
		RequestID:        requestID,
	}, caller)
	if err != nil {
		t.Fatalf("withdraw reconciliation: %v", err)
	}
	return res
}

// readStoredReconciliation reads a reconciliation row directly through the
// store's own scanner — tests may inspect the persisted state without a public
// read API.
func readStoredReconciliation(t *testing.T, s *SQLiteStore, id string) core.Reconciliation {
	t.Helper()
	r, err := scanReconciliation(s.db.QueryRow(`SELECT `+reconciliationColumns+` FROM reconciliations WHERE id = ?`, id))
	if err != nil {
		t.Fatalf("read stored reconciliation %s: %v", id, err)
	}
	return r
}

// ──────────────────────────────────────────────
// Proposal
// ──────────────────────────────────────────────

func TestProposeReconciliationHappyPath(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)

	res := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)

	if res.IdempotentReplay {
		t.Error("fresh proposal must not be a replay")
	}
	if res.ReconciliationID == "" || res.ReconciliationID != res.Reconciliation.ID {
		t.Errorf("ReconciliationID = %q, want the reconciliation's id %q", res.ReconciliationID, res.Reconciliation.ID)
	}
	r := res.Reconciliation
	if r.Status != core.ReconciliationProposed {
		t.Errorf("status = %s, want proposed", r.Status)
	}
	if r.TenantID != testOrgID || r.CompanyID != "acme" {
		t.Errorf("scope = %s/%s, want %s/acme (derived from the observations)", r.TenantID, r.CompanyID, testOrgID)
	}
	if r.FiscalPeriodID != testPeriod {
		t.Errorf("FiscalPeriodID = %q, want %q (matching observation periods)", r.FiscalPeriodID, testPeriod)
	}
	if r.LeftMemoryID != left || r.RightMemoryID != right {
		t.Errorf("endpoints = %q/%q, want %q/%q", r.LeftMemoryID, r.RightMemoryID, left, right)
	}
	if r.Method != "trial-balance" || r.Currency != "PEN" {
		t.Errorf("method/currency = %q/%q, want trial-balance/PEN", r.Method, r.Currency)
	}
	if r.LeftAmountCents != 150000 || r.RightAmountCents != 149500 || r.ToleranceCents != 1000 {
		t.Errorf("amounts = %d/%d tol %d, want the command's signed int64 cents", r.LeftAmountCents, r.RightAmountCents, r.ToleranceCents)
	}
	if r.VarianceCents != 500 {
		t.Errorf("VarianceCents = %d, want 500 (engine-derived left - right)", r.VarianceCents)
	}
	if r.DecidedAt != "" {
		t.Errorf("DecidedAt = %q, want empty for an open proposal", r.DecidedAt)
	}
	wantProposer := core.Source{System: "go-test", ActorID: "recon-agent", ActorKind: core.ActorKindAgent, Session: "sess-recon"}
	if r.Proposer != wantProposer {
		t.Errorf("proposer = %+v, want %+v", r.Proposer, wantProposer)
	}
	if r.ProposalReason != "bank statement matches ledger within tolerance" || r.Resolution != "" {
		t.Errorf("reason/resolution = %q/%q, want proposal reason/empty", r.ProposalReason, r.Resolution)
	}
	if r.ProposedAt == "" {
		t.Errorf("ProposedAt = %q, want non-empty", r.ProposedAt)
	}

	// The returned entity is byte-identical to the persisted row.
	stored := readStoredReconciliation(t, s, res.ReconciliationID)
	if !reflect.DeepEqual(stored, r) {
		t.Errorf("stored reconciliation differs from the returned entity:\nstored: %+v\nreturned: %+v", stored, r)
	}

	// A proposal writes NO reconciliation event (frozen events CHECK).
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_events WHERE reconciliation_id = ?`, res.ReconciliationID); n != 0 {
		t.Errorf("reconciliation_events rows = %d, want 0 (a proposal is not a transition)", n)
	}

	// The reservation completed with BOTH result_json and reconciliation_event_id
	// NULL (the table CHECK requires them together).
	var completedAt sql.NullString
	var resultJSON, eventID sql.NullString
	if err := s.db.QueryRow(`
		SELECT completed_at, result_json, reconciliation_event_id
		FROM reconciliation_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		testOrgID, "req-reconcile").Scan(&completedAt, &resultJSON, &eventID); err != nil {
		t.Fatalf("read proposal reservation: %v", err)
	}
	if !completedAt.Valid || resultJSON.Valid || eventID.Valid {
		t.Errorf("reservation = completed:%v result:%v event:%v, want completed with NULL result/event",
			completedAt.Valid, resultJSON.Valid, eventID.Valid)
	}
}

// TestProposeReconciliationCrossPeriodKeepsFiscalPeriodNull verifies the
// explicit cross-period convention: endpoints with DIFFERENT periods produce
// fiscalPeriodId NULL (matching judgment convention).
func TestProposeReconciliationCrossPeriodKeepsFiscalPeriodNull(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	leftScope := testScope(testRucA)
	rightScope := testScope(testRucA)
	rightScope.Period = "202402" // different period, same tenant/company

	a, err := s.Save(validInput("tax.reconcile.left-x", "period one observation"))
	if err != nil {
		t.Fatalf("save left: %v", err)
	}
	rightInput := validInput("tax.reconcile.right-x", "period two observation")
	rightInput.Scope = rightScope
	b, err := s.Save(rightInput)
	if err != nil {
		t.Fatalf("save right: %v", err)
	}
	_ = leftScope

	res := proposeReconciliation(t, s, baseReconcileCmd(a.Memory.Identity.ID, b.Memory.Identity.ID), reconciliationProposer)
	if res.Reconciliation.FiscalPeriodID != "" {
		t.Errorf("FiscalPeriodID = %q, want empty (explicit cross-period reconciliation)", res.Reconciliation.FiscalPeriodID)
	}
	stored := readStoredReconciliation(t, s, res.ReconciliationID)
	if stored.FiscalPeriodID != "" {
		t.Errorf("stored fiscal_period_id = %q, want NULL (cross-period)", stored.FiscalPeriodID)
	}
}

func TestProposeReconciliationSyntaxGuards(t *testing.T) {
	s := newTestStore(t)

	t.Run("human proposer", func(t *testing.T) {
		_, err := s.ProposeReconciliation(context.Background(), baseReconcileCmd("left", "right"),
			core.Source{System: "go-test", ActorID: "human-1", ActorKind: core.ActorKindHuman})
		if auth.Code(err) != auth.CodeProposalUnauthorized {
			t.Errorf("code = %q, want PROPOSAL_UNAUTHORIZED", auth.Code(err))
		}
	})

	t.Run("missing observation", func(t *testing.T) {
		_, err := s.ProposeReconciliation(context.Background(), baseReconcileCmd("missing-left", "right"), reconciliationProposer)
		if auth.Code(err) != auth.CodeMemoryNotFound {
			t.Errorf("code = %q, want MEMORY_NOT_FOUND", auth.Code(err))
		}
	})

	t.Run("same endpoint", func(t *testing.T) {
		_, err := s.ProposeReconciliation(context.Background(), baseReconcileCmd("left", "left"), reconciliationProposer)
		if auth.Code(err) != auth.CodeReconciliationNotFound {
			t.Errorf("code = %q, want RECONCILIATION_NOT_FOUND (incomplete command)", auth.Code(err))
		}
	})

	t.Run("empty method", func(t *testing.T) {
		cmd := baseReconcileCmd("left", "right")
		cmd.Method = "  "
		_, err := s.ProposeReconciliation(context.Background(), cmd, reconciliationProposer)
		if auth.Code(err) != auth.CodeReconciliationNotFound {
			t.Errorf("code = %q, want RECONCILIATION_NOT_FOUND (incomplete command)", auth.Code(err))
		}
	})

	t.Run("negative tolerance", func(t *testing.T) {
		cmd := baseReconcileCmd("left", "right")
		cmd.ToleranceCents = -1
		_, err := s.ProposeReconciliation(context.Background(), cmd, reconciliationProposer)
		if auth.Code(err) != auth.CodeReconciliationNotFound {
			t.Errorf("code = %q, want RECONCILIATION_NOT_FOUND (invalid command)", auth.Code(err))
		}
	})

	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliations`); n != 0 {
		t.Errorf("reconciliations rows = %d, want 0 (all guarded proposals rolled back)", n)
	}
}

func TestProposeReconciliationCrossTenantDenied(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	a, err := s.Save(validInput("tax.reconcile.tenant-a", "tenant a"))
	if err != nil {
		t.Fatalf("save left: %v", err)
	}
	foreign := validInput("tax.reconcile.tenant-b", "tenant b")
	foreign.Scope.OrganizationID = "org-002"
	foreign.Scope.CompanyID = "other"
	b, err := s.Save(foreign)
	if err != nil {
		t.Fatalf("save right: %v", err)
	}

	_, err = s.ProposeReconciliation(context.Background(), baseReconcileCmd(a.Memory.Identity.ID, b.Memory.Identity.ID), reconciliationProposer)
	if auth.Code(err) != auth.CodeTenantScopeMismatch {
		t.Errorf("code = %q, want TENANT_SCOPE_MISMATCH", auth.Code(err))
	}
}

// ──────────────────────────────────────────────
// Confirmation
// ──────────────────────────────────────────────

func TestConfirmReconciliationHappyPath(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)
	expectedHash := core.ComputeReconciliationHash(proposed.Reconciliation)
	principal := controllerPrincipal(t)

	res := confirmReconciliation(t, s, proposed.ReconciliationID, expectedHash, "req-confirm", "ledger matches bank statement; variance within tolerance", principal)

	if res.IdempotentReplay {
		t.Error("fresh confirmation must not be a replay")
	}
	if res.ReconciliationEventID == "" {
		t.Error("ReconciliationEventID must not be empty")
	}
	r := res.Reconciliation
	if r.Status != core.ReconciliationConfirmed {
		t.Errorf("status = %s, want confirmed", r.Status)
	}
	if r.Resolution != "ledger matches bank statement; variance within tolerance" {
		t.Errorf("Resolution = %q, want the professional resolution", r.Resolution)
	}
	if r.PolicyVersion != authz.ReconciliationPolicyVersion {
		t.Errorf("PolicyVersion = %q, want %q", r.PolicyVersion, authz.ReconciliationPolicyVersion)
	}
	if r.Adjudicator == nil || r.Adjudicator.SubjectID != principal.SubjectID() {
		t.Fatalf("Adjudicator = %+v, want subject %s", r.Adjudicator, principal.SubjectID())
	}
	snap := principal.PrincipalSnapshot()
	if !reflect.DeepEqual(r.Adjudicator.Roles, snap.Roles) {
		t.Errorf("adjudicator roles = %v, want canonical %v", r.Adjudicator.Roles, snap.Roles)
	}
	if r.DecidedAt == "" {
		t.Errorf("DecidedAt = %q, want non-empty", r.DecidedAt)
	}

	// The persisted row is byte-identical to the returned entity.
	stored := readStoredReconciliation(t, s, proposed.ReconciliationID)
	if !reflect.DeepEqual(stored, r) {
		t.Fatalf("stored reconciliation differs from the returned entity:\nstored: %+v\nreturned: %+v", stored, r)
	}

	// Exactly one immutable confirm event carrying the snapshot and policy.
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_events WHERE reconciliation_id = ? AND action = 'confirm'`, proposed.ReconciliationID); n != 1 {
		t.Errorf("confirm events = %d, want exactly 1", n)
	}
	var principalSnapshotJSON, policyVersion string
	if err := s.db.QueryRow(`SELECT principal_snapshot_json, policy_version FROM reconciliation_events WHERE reconciliation_id = ? AND action = 'confirm'`, proposed.ReconciliationID).
		Scan(&principalSnapshotJSON, &policyVersion); err != nil {
		t.Fatalf("read confirm event: %v", err)
	}
	if policyVersion != authz.ReconciliationPolicyVersion {
		t.Errorf("event policy_version = %q, want %q", policyVersion, authz.ReconciliationPolicyVersion)
	}
	if principalSnapshotJSON == "" {
		t.Error("event principal_snapshot_json must be set on confirm")
	}

	// The confirm projects EXACTLY ONE reconciles observation relation.
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ? AND relation = 'reconciles'`, left, right); n != 1 {
		t.Errorf("reconciles projection rows = %d, want exactly 1", n)
	}
}

// TestConfirmReconciliationProjectsReconcilesRelation verifies the projection
// shape: from_id = left, to_id = right, relation frozen to 'reconciles', actor =
// the verified subject.
func TestConfirmReconciliationProjectsReconcilesRelation(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)
	principal := controllerPrincipal(t)
	confirmReconciliation(t, s, proposed.ReconciliationID, core.ComputeReconciliationHash(proposed.Reconciliation), "req-confirm-proj", "confirmed", principal)

	var fromID, toID, relation, actor string
	if err := s.db.QueryRow(`SELECT from_id, to_id, relation, actor FROM relations WHERE relation = 'reconciles'`).
		Scan(&fromID, &toID, &relation, &actor); err != nil {
		t.Fatalf("read reconciles projection: %v", err)
	}
	if fromID != left || toID != right {
		t.Errorf("projection endpoints = %q/%q, want %q/%q (left → right)", fromID, toID, left, right)
	}
	if actor != principal.SubjectID() {
		t.Errorf("projection actor = %q, want the verified subject %q", actor, principal.SubjectID())
	}
}

// TestRejectReconciliationTerminalNoProjection verifies rejection: the human
// reason becomes the resolution, the row is terminal and NO reconciles relation
// is projected.
func TestRejectReconciliationTerminalNoProjection(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)
	principal := controllerPrincipal(t)

	res := rejectReconciliation(t, s, proposed.ReconciliationID, core.ComputeReconciliationHash(proposed.Reconciliation), "req-reject", "supporting voucher missing", principal)

	if res.IdempotentReplay {
		t.Error("fresh rejection must not be a replay")
	}
	r := res.Reconciliation
	if r.Status != core.ReconciliationRejected {
		t.Errorf("status = %s, want rejected", r.Status)
	}
	if r.Resolution != "supporting voucher missing" {
		t.Errorf("Resolution = %q, want the human reason", r.Resolution)
	}
	if r.PolicyVersion != authz.ReconciliationPolicyVersion || r.Adjudicator == nil {
		t.Errorf("rejected decision must carry policy/adjudicator: %+v", r)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ?`, left, right); n != 0 {
		t.Errorf("relations rows = %d, want 0 (a rejected proposal projects none)", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_events WHERE reconciliation_id = ? AND action = 'reject'`, proposed.ReconciliationID); n != 1 {
		t.Errorf("reject events = %d, want exactly 1", n)
	}

	// A rejected row is terminal: confirm/reject/withdraw all fail closed.
	if _, err := s.ConfirmReconciliation(context.Background(), core.ConfirmReconciliationCommand{
		ReconciliationID: proposed.ReconciliationID, Resolution: "late", ExpectedReconciliationHash: core.ComputeReconciliationHash(r), RequestID: "req-after-reject",
	}, principal, authz.NewReconciliationPolicy()); auth.Code(err) != auth.CodeInvalidReconciliationTransition {
		t.Errorf("confirm after reject: code = %q, want INVALID_RECONCILIATION_TRANSITION", auth.Code(err))
	}
}

// TestWithdrawReconciliationSameProposer verifies the provenance continuity
// rule: only the EXACT same proposer identity may withdraw, only while the
// proposal is open, and the row becomes terminal.
func TestWithdrawReconciliationSameProposer(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)

	t.Run("happy path", func(t *testing.T) {
		cmd := baseReconcileCmd(left, right)
		cmd.RequestID = "req-withdraw-happy-propose"
		proposed := proposeReconciliation(t, s, cmd, reconciliationProposer)
		res := withdrawReconciliation(t, s, proposed.ReconciliationID, "req-withdraw-happy", reconciliationProposer)
		if res.IdempotentReplay {
			t.Error("fresh withdrawal must not be a replay")
		}
		if res.Reconciliation.Status != core.ReconciliationWithdrawn {
			t.Errorf("status = %s, want withdrawn", res.Reconciliation.Status)
		}
		if res.Reconciliation.Adjudicator != nil {
			t.Errorf("withdrawal must never carry an adjudicator: %+v", res.Reconciliation.Adjudicator)
		}
		if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_events WHERE reconciliation_id = ? AND action = 'withdraw'`, proposed.ReconciliationID); n != 1 {
			t.Errorf("withdraw events = %d, want exactly 1", n)
		}
	})

	t.Run("other proposer denied", func(t *testing.T) {
		cmd := baseReconcileCmd(left, right)
		cmd.RequestID = "req-withdraw-other-propose"
		proposed := proposeReconciliation(t, s, cmd, reconciliationProposer)
		_, err := s.WithdrawReconciliation(context.Background(), core.WithdrawReconciliationCommand{
			ReconciliationID: proposed.ReconciliationID, RequestID: "req-withdraw-other",
		}, otherReconciliationProposer)
		if auth.Code(err) != auth.CodeProposalUnauthorized {
			t.Errorf("code = %q, want PROPOSAL_UNAUTHORIZED", auth.Code(err))
		}
		// Free the open tuple for the next subtest (the denied attempt left the
		// proposal open).
		withdrawReconciliation(t, s, proposed.ReconciliationID, "req-withdraw-other-fix", reconciliationProposer)
	})

	t.Run("decided proposal cannot be withdrawn", func(t *testing.T) {
		cmd := baseReconcileCmd(left, right)
		cmd.RequestID = "req-withdraw-confirmed-propose"
		proposed := proposeReconciliation(t, s, cmd, reconciliationProposer)
		principal := controllerPrincipal(t)
		confirmReconciliation(t, s, proposed.ReconciliationID, core.ComputeReconciliationHash(proposed.Reconciliation), "req-confirm-then-withdraw", "confirmed", principal)
		_, err := s.WithdrawReconciliation(context.Background(), core.WithdrawReconciliationCommand{
			ReconciliationID: proposed.ReconciliationID, RequestID: "req-withdraw-confirmed",
		}, reconciliationProposer)
		if auth.Code(err) != auth.CodeInvalidReconciliationTransition {
			t.Errorf("code = %q, want INVALID_RECONCILIATION_TRANSITION", auth.Code(err))
		}
	})
}

// TestReconciliationHashMismatchDetailsOnly verifies the hash guard: a stale
// expected hash fails with RECONCILIATION_HASH_MISMATCH carrying ONLY the two
// hashes (never content), and NO partial mutation survives (no event, no
// receipt, no relation projection, reservation rolled back).
func TestReconciliationHashMismatchDetailsOnly(t *testing.T) {
	s, signer := receiptStore(t)
	left, right := reconcileContext(t, s)
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)
	principal := controllerPrincipal(t)

	_, err := s.ConfirmReconciliation(context.Background(), core.ConfirmReconciliationCommand{
		ReconciliationID:           proposed.ReconciliationID,
		Resolution:                 "confirmed",
		ExpectedReconciliationHash: "stale-reviewed-hash",
		RequestID:                  "req-stale-hash",
	}, principal, authz.NewReconciliationPolicy())

	var e *auth.Error
	if !errors.As(err, &e) {
		t.Fatalf("error = %v, want a typed *auth.Error", err)
	}
	if e.Code != auth.CodeReconciliationHashMismatch {
		t.Fatalf("code = %q, want RECONCILIATION_HASH_MISMATCH", e.Code)
	}
	if e.ExpectedJudgmentHash != "stale-reviewed-hash" || e.ActualJudgmentHash != core.ComputeReconciliationHash(proposed.Reconciliation) {
		t.Errorf("details = %q/%q, want expected stale hash and the actual proposed hash", e.ExpectedJudgmentHash, e.ActualJudgmentHash)
	}
	if !strings.Contains(e.Error(), "reconciliation hash") {
		t.Errorf("Error() must name the reconciliation hash contract: %q", e.Error())
	}

	// No partial mutation: row still proposed, no events, no projection, no
	// receipt, reservation rolled back.
	if got := readStoredReconciliation(t, s, proposed.ReconciliationID); got.Status != core.ReconciliationProposed {
		t.Errorf("status = %s, want still proposed", got.Status)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_events WHERE reconciliation_id = ?`, proposed.ReconciliationID); n != 0 {
		t.Errorf("events = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ?`, left, right); n != 0 {
		t.Errorf("relations = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts WHERE subject_id = ?`, proposed.ReconciliationID); n != 0 {
		t.Errorf("receipts = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_idempotency_keys WHERE request_id = 'req-stale-hash'`); n != 0 {
		t.Errorf("reservation rows = %d, want 0 (rolled back)", n)
	}
	_ = signer
}

// TestConfirmReconciliationEmitsReconciliationReceipt verifies the atomic
// receipt: reconciliation_confirmed on the reconciliation subject chain with
// the reviewed/resulting reconciliation hashes, both endpoint ids/envelope
// hashes, the principal snapshot and the frozen policy version.
func TestConfirmReconciliationEmitsReconciliationReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	left, right := reconcileContext(t, s)
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)
	principal := controllerPrincipal(t)

	res := confirmReconciliation(t, s, proposed.ReconciliationID, core.ComputeReconciliationHash(proposed.Reconciliation), "req-confirm-receipt", "confirmed within tolerance", principal)

	// The two context saves emitted memory_recorded receipts; the confirmation
	// adds exactly ONE reconciliation_confirmed receipt.
	receipts := readReceipts(t, s)
	rcpt := byAction(t, receipts, string(core.ReceiptActionReconciliationConfirmed))
	if len(receipts) != 3 {
		t.Fatalf("receipts = %d, want 2 memory_recorded + 1 reconciliation_confirmed", len(receipts))
	}
	if rcpt.SubjectType != string(core.SubjectTypeReconciliation) || rcpt.SubjectID != res.ReconciliationID {
		t.Errorf("subject = %s/%s, want reconciliation/%s", rcpt.SubjectType, rcpt.SubjectID, res.ReconciliationID)
	}
	if !rcpt.ReconciliationID.Valid || rcpt.ReconciliationID.String != res.ReconciliationID {
		t.Errorf("reconciliation_id FK = %v, want the reconciliation id %s", rcpt.ReconciliationID, res.ReconciliationID)
	}
	if rcpt.MemoryID.Valid || rcpt.JudgmentID.Valid {
		t.Errorf("typed FKs must be exactly one (reconciliation only): memory=%v judgment=%v", rcpt.MemoryID.Valid, rcpt.JudgmentID.Valid)
	}
	if rcpt.PolicyVersion != authz.ReconciliationPolicyVersion {
		t.Errorf("policy_version = %q, want %q", rcpt.PolicyVersion, authz.ReconciliationPolicyVersion)
	}

	payload := rcpt.payload(t)
	if payload.Version != core.ReceiptPayloadVersionV05 {
		t.Errorf("payload version = %q, want %q (new action)", payload.Version, core.ReceiptPayloadVersionV05)
	}
	if payload.ReviewedJudgmentHash != core.ComputeReconciliationHash(proposed.Reconciliation) {
		t.Errorf("reviewed hash = %q, want the proposed reconciliation hash", payload.ReviewedJudgmentHash)
	}
	if payload.ResultingJudgmentHash != core.ComputeReconciliationHash(res.Reconciliation) {
		t.Errorf("resulting hash = %q, want the confirmed reconciliation hash", payload.ResultingJudgmentHash)
	}
	if payload.FromMemoryID != left || payload.ToMemoryID != right {
		t.Errorf("endpoint ids = %q/%q, want %q/%q", payload.FromMemoryID, payload.ToMemoryID, left, right)
	}
	if payload.FromEnvelopeHash == "" || payload.ToEnvelopeHash == "" {
		t.Errorf("endpoint envelope hashes = %q/%q, want fresh envelope hashes", payload.FromEnvelopeHash, payload.ToEnvelopeHash)
	}
	if payload.Reason != "confirmed within tolerance" {
		t.Errorf("reason = %q, want the resolution", payload.Reason)
	}
	if payload.PrincipalID != principal.SubjectID() || payload.MembershipID != principal.MembershipID() {
		t.Errorf("principal = %s/%s, want %s/%s", payload.PrincipalID, payload.MembershipID, principal.SubjectID(), principal.MembershipID())
	}
	if payload.PolicyVersion != authz.ReconciliationPolicyVersion {
		t.Errorf("payload policy = %q, want %q", payload.PolicyVersion, authz.ReconciliationPolicyVersion)
	}

	// The stored receipt verifies offline with the parity public key.
	verifyStored(t, signer, rcpt)
}

// TestRejectReconciliationEmitsReconciliationRejectedReceipt verifies the
// rejected action: same coverage, no relation projection.
func TestRejectReconciliationEmitsReconciliationRejectedReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	left, right := reconcileContext(t, s)
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)
	principal := controllerPrincipal(t)

	res := rejectReconciliation(t, s, proposed.ReconciliationID, core.ComputeReconciliationHash(proposed.Reconciliation), "req-reject-receipt", "voucher missing", principal)

	receipts := readReceipts(t, s)
	rcpt := byAction(t, receipts, string(core.ReceiptActionReconciliationRejected))
	if len(receipts) != 3 {
		t.Fatalf("receipts = %d, want 2 memory_recorded + 1 reconciliation_rejected", len(receipts))
	}
	if rcpt.SubjectID != res.ReconciliationID {
		t.Errorf("subject = %s, want %s", rcpt.SubjectID, res.ReconciliationID)
	}
	payload := rcpt.payload(t)
	if payload.ResultingJudgmentHash != core.ComputeReconciliationHash(res.Reconciliation) {
		t.Errorf("resulting hash = %q, want the rejected reconciliation hash", payload.ResultingJudgmentHash)
	}
	if payload.Reason != "voucher missing" {
		t.Errorf("reason = %q, want the human reason", payload.Reason)
	}
	verifyStored(t, signer, rcpt)
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ?`, left, right); n != 0 {
		t.Errorf("relations = %d, want 0 (rejected projects none)", n)
	}
}

// TestSigningFailureRollsBackReconciliationDecision verifies that a receipt
// signing failure rolls the WHOLE decision back: no status flip, no event, no
// projection, no reservation.
func TestSigningFailureRollsBackReconciliationDecision(t *testing.T) {
	s, signer := receiptStore(t)
	left, right := reconcileContext(t, s)
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)
	signer.fail = true
	principal := controllerPrincipal(t)

	_, err := s.ConfirmReconciliation(context.Background(), core.ConfirmReconciliationCommand{
		ReconciliationID:           proposed.ReconciliationID,
		Resolution:                 "confirmed",
		ExpectedReconciliationHash: core.ComputeReconciliationHash(proposed.Reconciliation),
		RequestID:                  "req-signer-fail",
	}, principal, authz.NewReconciliationPolicy())
	if err == nil {
		t.Fatal("signing failure must fail the confirmation")
	}
	if got := readStoredReconciliation(t, s, proposed.ReconciliationID); got.Status != core.ReconciliationProposed {
		t.Errorf("status = %s, want still proposed (whole decision rolled back)", got.Status)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_events`); n != 0 {
		t.Errorf("events = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ?`, left, right); n != 0 {
		t.Errorf("relations = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts WHERE subject_type = 'reconciliation'`); n != 0 {
		t.Errorf("reconciliation receipts = %d, want 0", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_idempotency_keys WHERE request_id = 'req-signer-fail'`); n != 0 {
		t.Errorf("reservation rows = %d, want 0 (rolled back)", n)
	}
}

// ──────────────────────────────────────────────
// Idempotency and conflict
// ──────────────────────────────────────────────

// TestReconciliationConfirmReplayAndConflict covers the (tenant, requestId)
// contract: a same-request retry replays the ORIGINAL outcome while a reused
// request id with a different decision or adjudicator is IDEMPOTENCY_CONFLICT.
func TestReconciliationConfirmReplayAndConflict(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)
	expectedHash := core.ComputeReconciliationHash(proposed.Reconciliation)
	principal := controllerPrincipal(t)

	first := confirmReconciliation(t, s, proposed.ReconciliationID, expectedHash, "req-confirm-replay", "confirmed", principal)
	if first.IdempotentReplay {
		t.Fatal("first confirmation must not be a replay")
	}

	// Same request id + same command + same adjudicator → replay of the ORIGINAL
	// outcome (same event id, same entity bytes).
	replay, err := s.ConfirmReconciliation(context.Background(), core.ConfirmReconciliationCommand{
		ReconciliationID:           proposed.ReconciliationID,
		Resolution:                 "confirmed",
		ExpectedReconciliationHash: expectedHash,
		RequestID:                  "req-confirm-replay",
	}, principal, authz.NewReconciliationPolicy())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Error("same-request retry must be an idempotent replay")
	}
	if replay.ReconciliationEventID != first.ReconciliationEventID {
		t.Errorf("replay event = %q, want the original %q", replay.ReconciliationEventID, first.ReconciliationEventID)
	}
	if !reflect.DeepEqual(replay.Reconciliation, first.Reconciliation) {
		t.Errorf("replay entity differs from the original outcome")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_events WHERE action = 'confirm'`); n != 1 {
		t.Errorf("confirm events = %d, want exactly 1 (replay mints nothing)", n)
	}

	// Same request id, different resolution → IDEMPOTENCY_CONFLICT.
	_, err = s.ConfirmReconciliation(context.Background(), core.ConfirmReconciliationCommand{
		ReconciliationID:           proposed.ReconciliationID,
		Resolution:                 "a different resolution",
		ExpectedReconciliationHash: expectedHash,
		RequestID:                  "req-confirm-replay",
	}, principal, authz.NewReconciliationPolicy())
	if auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Errorf("code = %q, want IDEMPOTENCY_CONFLICT", auth.Code(err))
	}
}

// TestProposeReconciliationConflict verifies the open-tuple partial unique
// index: a second open proposal for the same (tenant, company, left, right,
// method) tuple is RECONCILIATION_CONFLICT.
func TestProposeReconciliationConflict(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)
	proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)

	second := baseReconcileCmd(left, right)
	second.RequestID = "req-reconcile-2"
	second.Reason = "a second attempt"
	_, err := s.ProposeReconciliation(context.Background(), second, reconciliationProposer)
	if auth.Code(err) != auth.CodeReconciliationConflict {
		t.Errorf("code = %q, want RECONCILIATION_CONFLICT", auth.Code(err))
	}

	// A different method is a different tuple: allowed.
	third := baseReconcileCmd(left, right)
	third.Method = "bank-statement"
	third.RequestID = "req-reconcile-3"
	res := proposeReconciliation(t, s, third, reconciliationProposer)
	if res.Reconciliation.Method != "bank-statement" {
		t.Errorf("method = %q, want bank-statement", res.Reconciliation.Method)
	}
}

// TestProposeReconciliationReplayReturnsOriginalProposal verifies that a
// same-request retry replays the ORIGINAL proposal even when a newer proposal
// of the same tuple exists, and a reused request id with a different payload is
// IDEMPOTENCY_CONFLICT.
func TestProposeReconciliationReplayReturnsOriginalProposal(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)
	first := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)

	// Withdraw the first proposal so a second open proposal of the tuple is
	// legal, then propose again with a DIFFERENT request id.
	withdrawReconciliation(t, s, first.ReconciliationID, "req-withdraw-first", reconciliationProposer)
	second := baseReconcileCmd(left, right)
	second.RequestID = "req-reconcile-second"
	second.Reason = "corrected reason"
	proposeReconciliation(t, s, second, reconciliationProposer)

	// Replay the FIRST request id: it must return the ORIGINAL proposal row.
	replay, err := s.ProposeReconciliation(context.Background(), baseReconcileCmd(left, right), reconciliationProposer)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Error("same-request retry must be an idempotent replay")
	}
	if replay.ReconciliationID != first.ReconciliationID {
		t.Errorf("replay id = %q, want the original proposal %q", replay.ReconciliationID, first.ReconciliationID)
	}

	// Same request id, different amounts → IDEMPOTENCY_CONFLICT.
	conflicting := baseReconcileCmd(left, right)
	conflicting.LeftAmountCents = 999999
	_, err = s.ProposeReconciliation(context.Background(), conflicting, reconciliationProposer)
	if auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Errorf("code = %q, want IDEMPOTENCY_CONFLICT", auth.Code(err))
	}
}

// ──────────────────────────────────────────────
// Correction supersession
// ──────────────────────────────────────────────

// TestReconciliationCorrectionSupersedesPredecessor verifies the correction
// flow: a correction against a CONFIRMED predecessor confirms atomically with
// the predecessor's supersession; ReconciliationSuccessorOf routes readers to
// the successor; the confirmed historical edge is NOT deleted.
func TestReconciliationCorrectionSupersedesPredecessor(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)
	principal := controllerPrincipal(t)

	original := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)
	confirmed := confirmReconciliation(t, s, original.ReconciliationID, core.ComputeReconciliationHash(original.Reconciliation), "req-confirm-original", "confirmed original", principal)

	correction := baseReconcileCmd(left, right)
	correction.RequestID = "req-correction"
	correction.Reason = "correcting the amounts"
	correction.LeftAmountCents = 150050
	correction.PredecessorID = original.ReconciliationID
	correctionProposal := proposeReconciliation(t, s, correction, reconciliationProposer)

	correctionResult := confirmReconciliation(t, s, correctionProposal.ReconciliationID, core.ComputeReconciliationHash(correctionProposal.Reconciliation), "req-confirm-correction", "confirmed corrected amounts", principal)
	if correctionResult.Reconciliation.Status != core.ReconciliationConfirmed {
		t.Fatalf("correction status = %s, want confirmed", correctionResult.Reconciliation.Status)
	}

	// The predecessor is superseded and routed to the correction.
	pred := readStoredReconciliation(t, s, original.ReconciliationID)
	if pred.Status != core.ReconciliationSuperseded {
		t.Errorf("predecessor status = %s, want superseded", pred.Status)
	}
	if pred.SupersedesID != correctionProposal.ReconciliationID {
		t.Errorf("predecessor supersedes_id = %q, want %q", pred.SupersedesID, correctionProposal.ReconciliationID)
	}
	if pred.DecidedAt != confirmed.Reconciliation.DecidedAt {
		t.Errorf("predecessor decided_at = %q, must keep the original confirmation time %q", pred.DecidedAt, confirmed.Reconciliation.DecidedAt)
	}

	// ReconciliationSuccessorOf routes to the correction.
	successor, ok := s.ReconciliationSuccessorOf(context.Background(), original.ReconciliationID)
	if !ok {
		t.Fatal("ReconciliationSuccessorOf must route from the superseded predecessor")
	}
	if successor.ID != correctionProposal.ReconciliationID {
		t.Errorf("successor = %q, want %q", successor.ID, correctionProposal.ReconciliationID)
	}
	if _, ok := s.ReconciliationSuccessorOf(context.Background(), "missing-id"); ok {
		t.Error("ReconciliationSuccessorOf(missing) = ok, want false")
	}

	// Both confirm events + one supersede event exist; the reconciles edge stays
	// (a confirmed historical edge is never deleted).
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_events WHERE action = 'confirm'`); n != 2 {
		t.Errorf("confirm events = %d, want 2", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_events WHERE action = 'supersede'`); n != 1 {
		t.Errorf("supersede events = %d, want 1", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ? AND relation = 'reconciles'`, left, right); n != 1 {
		t.Errorf("reconciles edges = %d, want exactly 1 (never deleted)", n)
	}
}

// TestReconciliationProposedPredecessorSupersededAtProposeTime verifies the
// immediate same-proposer supersession of an OPEN predecessor: correcting a
// proposed reconciliation frees the open tuple for the correction.
func TestReconciliationProposedPredecessorSupersededAtProposeTime(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)

	original := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)

	correction := baseReconcileCmd(left, right)
	correction.RequestID = "req-correction-proposed"
	correction.Reason = "correcting while still proposed"
	correction.LeftAmountCents = 150050
	correction.PredecessorID = original.ReconciliationID
	res := proposeReconciliation(t, s, correction, reconciliationProposer)

	pred := readStoredReconciliation(t, s, original.ReconciliationID)
	if pred.Status != core.ReconciliationSuperseded {
		t.Errorf("predecessor status = %s, want superseded (immediate same-proposer supersession)", pred.Status)
	}
	if pred.SupersedesID != res.ReconciliationID {
		t.Errorf("predecessor supersedes_id = %q, want %q", pred.SupersedesID, res.ReconciliationID)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM reconciliation_events WHERE action = 'supersede' AND from_status = 'proposed'`); n != 1 {
		t.Errorf("proposed supersede events = %d, want 1", n)
	}

	// A different proposer cannot correct a superseded predecessor; a superseded
	// predecessor never re-opens (INVALID_RECONCILIATION_TRANSITION — the same
	// frozen code the judgment machine returns for a non-correctable
	// predecessor).
	other := baseReconcileCmd(left, right)
	other.RequestID = "req-correction-other"
	other.PredecessorID = original.ReconciliationID
	_, err := s.ProposeReconciliation(context.Background(), other, otherReconciliationProposer)
	if auth.Code(err) != auth.CodeInvalidReconciliationTransition {
		t.Errorf("code = %q, want INVALID_RECONCILIATION_TRANSITION (superseded predecessor never re-opens)", auth.Code(err))
	}

	// The predecessor must concern the same pair and method.
	wrongTuple := baseReconcileCmd(left, right)
	wrongTuple.Method = "bank-statement"
	wrongTuple.RequestID = "req-correction-wrong-tuple"
	wrongTuple.PredecessorID = original.ReconciliationID
	_, err = s.ProposeReconciliation(context.Background(), wrongTuple, reconciliationProposer)
	if auth.Code(err) != auth.CodeReconciliationConflict {
		t.Errorf("code = %q, want RECONCILIATION_CONFLICT (predecessor tuple mismatch)", auth.Code(err))
	}
}

// ──────────────────────────────────────────────
// Closed-period write gate
// ──────────────────────────────────────────────

// TestReconciliationClosedPeriodEndpointBlocksProposalAndDecision verifies the
// period write gate wiring: proposing a reconciliation whose EITHER endpoint
// lives in a CLOSED exact company period fails PERIOD_CLOSED, and deciding an
// already-proposed reconciliation whose endpoint period was closed afterwards
// also fails PERIOD_CLOSED — with NO partial mutation.
func TestReconciliationClosedPeriodEndpointBlocksProposalAndDecision(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(testRucA)

	left, err := s.Save(validInput("tax.reconcile.closed-left", "ledger in the closeable period"))
	if err != nil {
		t.Fatalf("save left: %v", err)
	}
	right, err := s.Save(validInput("tax.reconcile.closed-right", "bank in the closeable period"))
	if err != nil {
		t.Fatalf("save right: %v", err)
	}
	// The second proposal endpoint is saved BEFORE the close (the gate blocks
	// the save itself), then the proposal is attempted after the close.
	newLeft, err := s.Save(validInput("tax.reconcile.after-close", "observation saved before the close"))
	if err != nil {
		t.Fatalf("save new left: %v", err)
	}

	// Propose BEFORE the period closes (the gate is open), then close it.
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left.Memory.Identity.ID, right.Memory.Identity.ID), reconciliationProposer)
	_, _ = saveAndApproveClose(t, s, scope, "close before the reconciliation decision", "req-close-recon")

	// The decision path now fails PERIOD_CLOSED (either endpoint is in the
	// closed period).
	principal := controllerPrincipal(t)
	_, err = s.ConfirmReconciliation(context.Background(), core.ConfirmReconciliationCommand{
		ReconciliationID:           proposed.ReconciliationID,
		Resolution:                 "confirmed",
		ExpectedReconciliationHash: core.ComputeReconciliationHash(proposed.Reconciliation),
		RequestID:                  "req-confirm-closed-period",
	}, principal, authz.NewReconciliationPolicy())
	if auth.Code(err) != auth.CodePeriodClosed {
		t.Fatalf("confirm in closed period: code = %q, want PERIOD_CLOSED", auth.Code(err))
	}
	if got := readStoredReconciliation(t, s, proposed.ReconciliationID); got.Status != core.ReconciliationProposed {
		t.Errorf("status = %s, want still proposed (no partial mutation)", got.Status)
	}

	// A NEW proposal touching a closed-period endpoint also fails PERIOD_CLOSED.
	cmd := baseReconcileCmd(newLeft.Memory.Identity.ID, right.Memory.Identity.ID)
	cmd.RequestID = "req-propose-closed-period"
	_, err = s.ProposeReconciliation(context.Background(), cmd, reconciliationProposer)
	if auth.Code(err) != auth.CodePeriodClosed {
		t.Errorf("propose in closed period: code = %q, want PERIOD_CLOSED", auth.Code(err))
	}
}

// TestGetReconciliationReadSurface verifies the public read-only surface.
func TestGetReconciliationReadSurface(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)

	got, ok := s.GetReconciliation(context.Background(), proposed.ReconciliationID)
	if !ok {
		t.Fatal("GetReconciliation must find the proposed row")
	}
	if !reflect.DeepEqual(got, proposed.Reconciliation) {
		t.Fatalf("GetReconciliation mismatch:\n got %+v\nwant %+v", got, proposed.Reconciliation)
	}
	if _, ok := s.GetReconciliation(context.Background(), "missing"); ok {
		t.Error("GetReconciliation(missing) = ok, want false")
	}
}
