// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the atomic judgment
// adjudication path (v0.4.0 Step 2 — adjudicable conflicts; batch B commit 2)
// at the store boundary: propose → confirm/reject/withdraw as ONE BEGIN
// IMMEDIATE transaction per operation, the frozen error codes, the
// JUDGMENT_HASH_MISMATCH details carrier, idempotency replay/conflict, and the
// correction supersession routing (JudgmentSuccessorOf).
//
// Principals are minted through auth.Resolver.Authenticate with a fake session
// store — the SAME path production middleware uses; there is no public
// arbitrary-input principal constructor. Agents can never confirm/reject: the
// signatures REQUIRE a verified principal (compile-level contract — an agent
// Source is provenance only). The exhaustive immutability + two-store
// concurrency enforcement (test(store): enforce immutable and concurrent
// judgments) lives in judgment_immutability_test.go and
// judgment_concurrency_test.go.
package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// testProposer is the canonical provenance Source of the proposal fixtures:
// agent|system ONLY — provenance continuity, never authority.
var testProposer = core.Source{System: "go-test", ActorID: "agent-1", ActorKind: core.ActorKindAgent, Session: "sess-1"}

var otherProposer = core.Source{System: "go-test", ActorID: "agent-2", ActorKind: core.ActorKindAgent, Session: "sess-2"}

// proposeContext saves two observations plus the acme identity chain the
// authenticated decisions need (the confirm event's adjudicator_membership_id
// FK references memberships). seedJudgmentContext is NOT reused here: it seeds
// company co-1 with the same RUC as acme, which collides with the identity
// chain's UNIQUE(tenant_id, ruc).
func proposeContext(t *testing.T, s *SQLiteStore) (fromID, toID string) {
	t.Helper()
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	a, err := s.Save(validInput("tax.judgment.from", "from observation"))
	if err != nil {
		t.Fatalf("save from observation: %v", err)
	}
	b, err := s.Save(validInput("tax.judgment.to", "to observation"))
	if err != nil {
		t.Fatalf("save to observation: %v", err)
	}
	return a.Memory.Identity.ID, b.Memory.Identity.ID
}

func proposeJudgment(t *testing.T, s *SQLiteStore, cmd core.ProposeJudgmentCommand, caller core.Source) core.ProposeJudgmentResult {
	t.Helper()
	res, err := s.ProposeJudgment(context.Background(), cmd, caller)
	if err != nil {
		t.Fatalf("propose judgment: %v", err)
	}
	return res
}

func confirmJudgment(t *testing.T, s *SQLiteStore, judgmentID, expectedHash, requestID, resolution string, principal auth.VerifiedApprovalPrincipal) core.ConfirmJudgmentResult {
	t.Helper()
	res, err := s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
		JudgmentID:           judgmentID,
		Resolution:           resolution,
		ExpectedJudgmentHash: expectedHash,
		RequestID:            requestID,
	}, principal, authz.NewJudgmentPolicy())
	if err != nil {
		t.Fatalf("confirm judgment: %v", err)
	}
	return res
}

func rejectJudgment(t *testing.T, s *SQLiteStore, judgmentID, expectedHash, requestID, reason string, principal auth.VerifiedApprovalPrincipal) core.RejectJudgmentResult {
	t.Helper()
	res, err := s.RejectJudgment(context.Background(), core.RejectJudgmentCommand{
		JudgmentID:           judgmentID,
		Reason:               reason,
		ExpectedJudgmentHash: expectedHash,
		RequestID:            requestID,
	}, principal, authz.NewJudgmentPolicy())
	if err != nil {
		t.Fatalf("reject judgment: %v", err)
	}
	return res
}

func withdrawJudgment(t *testing.T, s *SQLiteStore, judgmentID, requestID string, caller core.Source) core.WithdrawJudgmentResult {
	t.Helper()
	res, err := s.WithdrawJudgment(context.Background(), core.WithdrawJudgmentCommand{
		JudgmentID: judgmentID,
		RequestID:  requestID,
	}, caller)
	if err != nil {
		t.Fatalf("withdraw judgment: %v", err)
	}
	return res
}

// readStoredJudgment reads a judgment row directly through the store's own
// scanner — tests may inspect the persisted state without a public read API.
func readStoredJudgment(t *testing.T, s *SQLiteStore, id string) core.AccountingJudgment {
	t.Helper()
	j, err := scanJudgment(s.db.QueryRow(`SELECT `+judgmentColumns+` FROM judgments WHERE id = ?`, id))
	if err != nil {
		t.Fatalf("read stored judgment %s: %v", id, err)
	}
	return j
}

// ──────────────────────────────────────────────
// Proposal
// ──────────────────────────────────────────────

func TestProposeJudgmentHappyPath(t *testing.T) {
	s := newTestStore(t)
	from, to := proposeContext(t, s)

	res := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal reason", RequestID: "req-propose",
	}, testProposer)

	if res.IdempotentReplay {
		t.Error("fresh proposal must not be a replay")
	}
	if res.JudgmentID == "" || res.JudgmentID != res.Judgment.ID {
		t.Errorf("JudgmentID = %q, want the judgment's id %q", res.JudgmentID, res.Judgment.ID)
	}
	j := res.Judgment
	if j.Status != core.JudgmentProposed {
		t.Errorf("status = %s, want proposed", j.Status)
	}
	if j.TenantID != testOrgID || j.CompanyID != "acme" {
		t.Errorf("scope = %s/%s, want %s/acme (derived from the observations)", j.TenantID, j.CompanyID, testOrgID)
	}
	if j.FiscalPeriodID != testPeriod {
		t.Errorf("FiscalPeriodID = %q, want %q (matching observation periods)", j.FiscalPeriodID, testPeriod)
	}
	if j.DecidedAt != "" {
		t.Errorf("DecidedAt = %q, want empty for an open proposal", j.DecidedAt)
	}
	wantProposer := core.Source{System: "go-test", ActorID: "agent-1", ActorKind: core.ActorKindAgent, Session: "sess-1"}
	if j.Proposer != wantProposer {
		t.Errorf("proposer = %+v, want %+v", j.Proposer, wantProposer)
	}
	if j.ProposalReason != "proposal reason" || j.Resolution != "" {
		t.Errorf("reason/resolution = %q/%q, want proposal reason/empty", j.ProposalReason, j.Resolution)
	}
	if j.ProposedAt == "" || j.UpdatedAt != j.ProposedAt {
		t.Errorf("timestamps = %q/%q, want equal non-empty", j.ProposedAt, j.UpdatedAt)
	}

	// The returned entity is byte-identical to the persisted row.
	stored := readStoredJudgment(t, s, res.JudgmentID)
	if !reflect.DeepEqual(stored, j) {
		t.Errorf("stored judgment differs from the returned entity:\nstored: %+v\nreturned: %+v", stored, j)
	}

	// A proposal writes NO judgment event: the frozen events CHECK admits only
	// confirm|reject|withdraw|supersede (design §4).
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ?`, res.JudgmentID); n != 0 {
		t.Errorf("judgment_events rows = %d, want 0 (a proposal is not a transition)", n)
	}

	// The reservation completed with BOTH result_json and judgment_event_id
	// NULL (the table CHECK requires them together; the proposal replay
	// re-derives the judgment from the tuple).
	var completedAt sql.NullString
	var resultJSON, eventID sql.NullString
	if err := s.db.QueryRow(`
		SELECT completed_at, result_json, judgment_event_id
		FROM judgment_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		testOrgID, "req-propose").Scan(&completedAt, &resultJSON, &eventID); err != nil {
		t.Fatalf("read proposal reservation: %v", err)
	}
	if !completedAt.Valid || resultJSON.Valid || eventID.Valid {
		t.Errorf("reservation = completed:%v result:%v event:%v, want completed with NULL result/event",
			completedAt.Valid, resultJSON.Valid, eventID.Valid)
	}
}

func TestProposeJudgmentSyntaxGuards(t *testing.T) {
	s := newTestStore(t)

	t.Run("human proposer", func(t *testing.T) {
		_, err := s.ProposeJudgment(context.Background(), core.ProposeJudgmentCommand{
			FromID: "from", ToID: "to", Relation: core.RelationSupports,
			Reason: "reason", RequestID: "req-human",
		}, core.Source{System: "go-test", ActorID: "human-1", ActorKind: core.ActorKindHuman})
		if auth.Code(err) != auth.CodeProposalUnauthorized {
			t.Errorf("code = %q, want PROPOSAL_UNAUTHORIZED", auth.Code(err))
		}
	})

	t.Run("non-proposable relation", func(t *testing.T) {
		_, err := s.ProposeJudgment(context.Background(), core.ProposeJudgmentCommand{
			FromID: "from", ToID: "to", Relation: core.RelationConflictsWith,
			Reason: "reason", RequestID: "req-rel",
		}, testProposer)
		if auth.Code(err) != auth.CodeRelationNotProposable {
			t.Errorf("code = %q, want RELATION_NOT_PROPOSABLE", auth.Code(err))
		}
	})

	t.Run("missing observation", func(t *testing.T) {
		_, err := s.ProposeJudgment(context.Background(), core.ProposeJudgmentCommand{
			FromID: "missing-from", ToID: "to", Relation: core.RelationSupports,
			Reason: "reason", RequestID: "req-missing",
		}, testProposer)
		if auth.Code(err) != auth.CodeMemoryNotFound {
			t.Errorf("code = %q, want MEMORY_NOT_FOUND", auth.Code(err))
		}
	})

	t.Run("empty reason", func(t *testing.T) {
		_, err := s.ProposeJudgment(context.Background(), core.ProposeJudgmentCommand{
			FromID: "from", ToID: "to", Relation: core.RelationSupports,
			Reason: "   ", RequestID: "req-reason",
		}, testProposer)
		if auth.Code(err) != auth.CodeMemoryNotFound {
			t.Errorf("code = %q, want MEMORY_NOT_FOUND (incomplete command)", auth.Code(err))
		}
	})

	if n := countRows(t, s, `SELECT COUNT(*) FROM judgments`); n != 0 {
		t.Errorf("judgments rows = %d, want 0 (all guarded proposals rolled back)", n)
	}
}

// ──────────────────────────────────────────────
// Confirmation
// ──────────────────────────────────────────────

func TestConfirmJudgmentHappyPath(t *testing.T) {
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal reason", RequestID: "req-propose",
	}, testProposer)
	expectedHash := core.ComputeJudgmentHash(proposed.Judgment)
	principal := controllerPrincipal(t)

	res := confirmJudgment(t, s, proposed.JudgmentID, expectedHash, "req-confirm", "professional resolution", principal)

	if res.IdempotentReplay {
		t.Error("fresh confirmation must not be a replay")
	}
	if res.JudgmentEventID == "" {
		t.Error("JudgmentEventID must not be empty")
	}
	j := res.Judgment
	if j.Status != core.JudgmentConfirmed {
		t.Errorf("status = %s, want confirmed", j.Status)
	}
	if j.Resolution != "professional resolution" {
		t.Errorf("Resolution = %q, want the professional resolution", j.Resolution)
	}
	if j.PolicyVersion != authz.JudgmentPolicyVersion {
		t.Errorf("PolicyVersion = %q, want %q", j.PolicyVersion, authz.JudgmentPolicyVersion)
	}
	if j.Adjudicator == nil || j.Adjudicator.SubjectID != principal.SubjectID() {
		t.Fatalf("Adjudicator = %+v, want subject %s", j.Adjudicator, principal.SubjectID())
	}
	snap := principal.PrincipalSnapshot()
	if !reflect.DeepEqual(j.Adjudicator.Roles, snap.Roles) {
		t.Errorf("adjudicator roles = %v, want canonical %v", j.Adjudicator.Roles, snap.Roles)
	}
	if j.DecidedAt == "" || j.UpdatedAt != j.DecidedAt {
		t.Errorf("timestamps = %q/%q, want decidedAt non-empty and updatedAt == decidedAt", j.UpdatedAt, j.DecidedAt)
	}

	// The persisted row is byte-identical to the returned entity.
	stored := readStoredJudgment(t, s, proposed.JudgmentID)
	if !reflect.DeepEqual(stored, j) {
		t.Errorf("stored judgment differs from the returned entity:\nstored: %+v\nreturned: %+v", stored, j)
	}

	// Exactly one immutable confirm event with the canonical fields.
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID); n != 1 {
		t.Fatalf("judgment_events rows = %d, want 1", n)
	}
	var action, fromStatus, toStatus, policyVersion, reason, snapshotJSON, eventHash string
	if err := s.db.QueryRow(`
		SELECT action, from_status, to_status, policy_version, reason, principal_snapshot_json, judgment_hash
		FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID).Scan(
		&action, &fromStatus, &toStatus, &policyVersion, &reason, &snapshotJSON, &eventHash,
	); err != nil {
		t.Fatalf("read confirm event: %v", err)
	}
	if action != "confirm" || fromStatus != "proposed" || toStatus != "confirmed" {
		t.Errorf("event = %s %s→%s, want confirm proposed→confirmed", action, fromStatus, toStatus)
	}
	if policyVersion != authz.JudgmentPolicyVersion || reason != "professional resolution" {
		t.Errorf("event policy/reason = %q/%q, want %q/professional resolution", policyVersion, reason, authz.JudgmentPolicyVersion)
	}
	if snapshotJSON == "" {
		t.Error("confirm event must carry the principal snapshot")
	}
	if eventHash != core.ComputeJudgmentHash(j) {
		t.Errorf("event judgment_hash does not equal the resulting confirmed hash")
	}

	// The compatibility observation relation projection: one row, actor is the
	// verified subject (design §1/§5 step 8).
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ?`, from, to); n != 1 {
		t.Fatalf("relations rows = %d, want 1", n)
	}
	var relRelation, relActor string
	if err := s.db.QueryRow(`SELECT relation, actor FROM relations WHERE from_id = ? AND to_id = ?`, from, to).Scan(&relRelation, &relActor); err != nil {
		t.Fatalf("read relation projection: %v", err)
	}
	if relRelation != string(core.RelationSupports) || relActor != principal.SubjectID() {
		t.Errorf("relation projection = %s by %s, want supports by %s", relRelation, relActor, principal.SubjectID())
	}

	// Judgment confirmation does NOT touch either observation's lifecycle.
	memFrom, okFrom := s.FindByID(from)
	memTo, okTo := s.FindByID(to)
	if !okFrom || !okTo {
		t.Fatal("observations vanished after confirmation")
	}
	if memFrom.Status != core.StatusActive || memTo.Status != core.StatusActive {
		t.Errorf("observation statuses = %s/%s, want active/active (confirmation never changes observations)", memFrom.Status, memTo.Status)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM transition_log`); n != 0 {
		t.Errorf("transition_log rows = %d, want 0 (judgment acts never write observation transitions)", n)
	}
}

func TestConfirmJudgmentHashMismatch(t *testing.T) {
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal reason", RequestID: "req-propose",
	}, testProposer)
	principal := controllerPrincipal(t)

	wrong := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	_, err := s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
		JudgmentID: proposed.JudgmentID, Resolution: "resolution",
		ExpectedJudgmentHash: wrong, RequestID: "req-wrong-hash",
	}, principal, authz.NewJudgmentPolicy())
	if auth.Code(err) != auth.CodeJudgmentHashMismatch {
		t.Fatalf("code = %q, want JUDGMENT_HASH_MISMATCH", auth.Code(err))
	}
	var ae *auth.Error
	if !errors.As(err, &ae) {
		t.Fatalf("error %v is not an auth.Error", err)
	}
	if ae.ExpectedJudgmentHash != wrong {
		t.Errorf("ExpectedJudgmentHash = %s, want the reviewed (wrong) hash", ae.ExpectedJudgmentHash)
	}
	if ae.ActualJudgmentHash != core.ComputeJudgmentHash(proposed.Judgment) {
		t.Errorf("ActualJudgmentHash = %s, want the fresh proposed hash", ae.ActualJudgmentHash)
	}

	// The failed confirmation left NO event, NO relation and NO reservation
	// behind (whole transaction rolled back); the proposal stays open. The
	// proposal's OWN completed reservation legitimately remains, so the count
	// is scoped to the confirm request id.
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID); n != 0 {
		t.Errorf("judgment_events rows = %d, want 0 (rolled back)", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ?`, from, to); n != 0 {
		t.Errorf("relations rows = %d, want 0 (rolled back)", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_idempotency_keys WHERE request_id = 'req-wrong-hash'`); n != 0 {
		t.Errorf("judgment_idempotency_keys rows = %d, want 0 (confirm reservation rolled back)", n)
	}
	if got := readStoredJudgment(t, s, proposed.JudgmentID); got.Status != core.JudgmentProposed {
		t.Errorf("status = %s, want proposed (no partial confirmation)", got.Status)
	}
}

func TestConfirmJudgmentInvalidTransitionAfterDecided(t *testing.T) {
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal reason", RequestID: "req-propose",
	}, testProposer)
	expectedHash := core.ComputeJudgmentHash(proposed.Judgment)
	principal := controllerPrincipal(t)
	confirmJudgment(t, s, proposed.JudgmentID, expectedHash, "req-confirm", "professional resolution", principal)

	// A stale confirm (new request id) after the proposal was decided is an
	// invalid transition — the status gate decides, not the caller.
	_, err := s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
		JudgmentID: proposed.JudgmentID, Resolution: "second resolution",
		ExpectedJudgmentHash: expectedHash, RequestID: "req-confirm-2",
	}, principal, authz.NewJudgmentPolicy())
	if auth.Code(err) != auth.CodeInvalidJudgmentTransition {
		t.Fatalf("code = %q, want INVALID_JUDGMENT_TRANSITION", auth.Code(err))
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID); n != 1 {
		t.Errorf("judgment_events rows = %d, want exactly 1 (stale confirm wrote nothing)", n)
	}
}

// ──────────────────────────────────────────────
// Rejection (terminal, no relation projection)
// ──────────────────────────────────────────────

func TestRejectJudgmentTerminalNoRelation(t *testing.T) {
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal reason", RequestID: "req-propose",
	}, testProposer)
	expectedHash := core.ComputeJudgmentHash(proposed.Judgment)
	principal := controllerPrincipal(t)

	res := rejectJudgment(t, s, proposed.JudgmentID, expectedHash, "req-reject", "human rejection reason", principal)

	if res.IdempotentReplay {
		t.Error("fresh rejection must not be a replay")
	}
	if res.JudgmentEventID == "" {
		t.Error("JudgmentEventID must not be empty")
	}
	j := res.Judgment
	if j.Status != core.JudgmentRejected {
		t.Errorf("status = %s, want rejected", j.Status)
	}
	if j.Resolution != "human rejection reason" {
		t.Errorf("Resolution = %q, want the human reason (never the proposal reason)", j.Resolution)
	}
	if j.DecidedAt == "" {
		t.Error("DecidedAt must be set (the schema CHECK requires it on every non-proposed row)")
	}

	// Exactly one immutable reject event carrying the snapshot.
	var action, fromStatus, toStatus string
	var snapshotJSON, policyVersion sql.NullString
	if err := s.db.QueryRow(`
		SELECT action, from_status, to_status, principal_snapshot_json, policy_version
		FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID).Scan(
		&action, &fromStatus, &toStatus, &snapshotJSON, &policyVersion,
	); err != nil {
		t.Fatalf("read reject event: %v", err)
	}
	if action != "reject" || fromStatus != "proposed" || toStatus != "rejected" {
		t.Errorf("event = %s %s→%s, want reject proposed→rejected", action, fromStatus, toStatus)
	}
	if !snapshotJSON.Valid || !policyVersion.Valid || policyVersion.String != authz.JudgmentPolicyVersion {
		t.Errorf("event snapshot/policy = %v/%v, want set/%s", snapshotJSON.Valid, policyVersion.Valid, authz.JudgmentPolicyVersion)
	}

	// Rejection writes NO observation relation projection (design §5).
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ?`, from, to); n != 0 {
		t.Errorf("relations rows = %d, want 0 (rejection is terminal and writes no projection)", n)
	}

	// Terminal: a rejected judgment never re-opens.
	_, err := s.RejectJudgment(context.Background(), core.RejectJudgmentCommand{
		JudgmentID: proposed.JudgmentID, Reason: "again",
		ExpectedJudgmentHash: expectedHash, RequestID: "req-reject-2",
	}, principal, authz.NewJudgmentPolicy())
	if auth.Code(err) != auth.CodeInvalidJudgmentTransition {
		t.Errorf("second reject code = %q, want INVALID_JUDGMENT_TRANSITION", auth.Code(err))
	}
}

// ──────────────────────────────────────────────
// Withdrawal (same proposer identity only)
// ──────────────────────────────────────────────

func TestWithdrawJudgmentSameProposer(t *testing.T) {
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal reason", RequestID: "req-propose",
	}, testProposer)

	res := withdrawJudgment(t, s, proposed.JudgmentID, "req-withdraw", testProposer)

	if res.IdempotentReplay {
		t.Error("fresh withdrawal must not be a replay")
	}
	if res.JudgmentEventID == "" {
		t.Error("JudgmentEventID must not be empty")
	}
	j := res.Judgment
	if j.Status != core.JudgmentWithdrawn {
		t.Errorf("status = %s, want withdrawn", j.Status)
	}
	if j.DecidedAt == "" {
		t.Error("DecidedAt must be set (the schema CHECK requires it on every non-proposed row)")
	}

	// One immutable withdraw event WITHOUT a snapshot or policy version.
	var action, fromStatus, toStatus string
	var snapshotJSON, policyVersion sql.NullString
	if err := s.db.QueryRow(`
		SELECT action, from_status, to_status, principal_snapshot_json, policy_version
		FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID).Scan(
		&action, &fromStatus, &toStatus, &snapshotJSON, &policyVersion,
	); err != nil {
		t.Fatalf("read withdraw event: %v", err)
	}
	if action != "withdraw" || fromStatus != "proposed" || toStatus != "withdrawn" {
		t.Errorf("event = %s %s→%s, want withdraw proposed→withdrawn", action, fromStatus, toStatus)
	}
	if snapshotJSON.Valid || policyVersion.Valid {
		t.Errorf("withdraw event snapshot/policy = %v/%v, want both NULL", snapshotJSON.Valid, policyVersion.Valid)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ?`, from, to); n != 0 {
		t.Errorf("relations rows = %d, want 0 (withdrawal writes no projection)", n)
	}

	// A DIFFERENT agent (same system/kind/session, different actorId) cannot
	// withdraw the proposal: PROPOSAL_UNAUTHORIZED (provenance continuity).
	proposed2 := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationContradicts,
		Reason: "second proposal", RequestID: "req-propose-2",
	}, testProposer)
	_, err := s.WithdrawJudgment(context.Background(), core.WithdrawJudgmentCommand{
		JudgmentID: proposed2.JudgmentID, RequestID: "req-foreign-withdraw",
	}, otherProposer)
	if auth.Code(err) != auth.CodeProposalUnauthorized {
		t.Errorf("foreign withdraw code = %q, want PROPOSAL_UNAUTHORIZED", auth.Code(err))
	}

	// A confirmed judgment can never be withdrawn.
	expectedHash := core.ComputeJudgmentHash(proposed2.Judgment)
	principal := controllerPrincipal(t)
	confirmJudgment(t, s, proposed2.JudgmentID, expectedHash, "req-confirm", "professional resolution", principal)
	_, err = s.WithdrawJudgment(context.Background(), core.WithdrawJudgmentCommand{
		JudgmentID: proposed2.JudgmentID, RequestID: "req-withdraw-confirmed",
	}, testProposer)
	if auth.Code(err) != auth.CodeInvalidJudgmentTransition {
		t.Errorf("withdraw confirmed code = %q, want INVALID_JUDGMENT_TRANSITION", auth.Code(err))
	}
}

// ──────────────────────────────────────────────
// Correction: supersession routing
// ──────────────────────────────────────────────

func TestJudgmentCorrectionSupersedesPredecessor(t *testing.T) {
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	principal := controllerPrincipal(t)

	// A confirmed judgment is corrected by a NEW proposed judgment naming it as
	// predecessor (same pair and relation).
	first := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "original", RequestID: "req-first",
	}, testProposer)
	firstHash := core.ComputeJudgmentHash(first.Judgment)
	confirmed := confirmJudgment(t, s, first.JudgmentID, firstHash, "req-confirm-first", "first resolution", principal)

	second := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "correction", RequestID: "req-correction", PredecessorID: first.JudgmentID,
	}, testProposer)
	if second.Judgment.PredecessorID != first.JudgmentID {
		t.Fatalf("PredecessorID = %q, want %q", second.Judgment.PredecessorID, first.JudgmentID)
	}
	secondHash := core.ComputeJudgmentHash(second.Judgment)
	confirmJudgment(t, s, second.JudgmentID, secondHash, "req-confirm-correction", "corrected resolution", principal)

	// The predecessor is superseded ATOMICALLY with the correction's
	// confirmation: status flips, supersedes_id routes to the successor, and
	// decided_at KEEPS the original confirmation time (routing-only change).
	predRow := readStoredJudgment(t, s, first.JudgmentID)
	if predRow.Status != core.JudgmentSuperseded {
		t.Errorf("predecessor status = %s, want superseded", predRow.Status)
	}
	if predRow.SupersedesID != second.JudgmentID {
		t.Errorf("predecessor SupersedesID = %q, want %q", predRow.SupersedesID, second.JudgmentID)
	}
	if predRow.DecidedAt != confirmed.Judgment.DecidedAt {
		t.Errorf("predecessor DecidedAt = %q, want original confirmation time %q (never re-stamped)", predRow.DecidedAt, confirmed.Judgment.DecidedAt)
	}

	// The predecessor's immutable supersede event (its own confirm event also
	// exists — filter by action).
	var action, fromStatus, toStatus string
	if err := s.db.QueryRow(`
		SELECT action, from_status, to_status FROM judgment_events
		WHERE judgment_id = ? AND action = 'supersede'`, first.JudgmentID).Scan(
		&action, &fromStatus, &toStatus,
	); err != nil {
		t.Fatalf("read predecessor event: %v", err)
	}
	if action != "supersede" || fromStatus != "confirmed" || toStatus != "superseded" {
		t.Errorf("predecessor event = %s %s→%s, want supersede confirmed→superseded", action, fromStatus, toStatus)
	}

	// The judgment supersedes relation routes readers onward.
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_relations WHERE from_judgment_id = ? AND to_judgment_id = ?`, first.JudgmentID, second.JudgmentID); n != 1 {
		t.Fatalf("judgment_relations rows = %d, want 1", n)
	}
	succ, ok := s.JudgmentSuccessorOf(context.Background(), first.JudgmentID)
	if !ok {
		t.Fatal("JudgmentSuccessorOf must route a superseded judgment")
	}
	if succ.ID != second.JudgmentID {
		t.Errorf("successor = %q, want %q (the correction)", succ.ID, second.JudgmentID)
	}
}

func TestJudgmentProposedPredecessorSameProposerSupersedes(t *testing.T) {
	// Design §3.7: a PROPOSED predecessor may be superseded immediately, but
	// only by the SAME proposer identity — the open-tuple index otherwise
	// rejects a second open proposal for the pair+relation.
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	principal := controllerPrincipal(t)

	first := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "original", RequestID: "req-first",
	}, testProposer)

	// A different agent cannot correct the still-open proposal.
	_, err := s.ProposeJudgment(context.Background(), core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "foreign correction", RequestID: "req-foreign", PredecessorID: first.JudgmentID,
	}, otherProposer)
	if auth.Code(err) != auth.CodeProposalUnauthorized {
		t.Fatalf("foreign correction code = %q, want PROPOSAL_UNAUTHORIZED", auth.Code(err))
	}

	// The SAME proposer's correction supersedes the open proposal immediately.
	second := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "correction", RequestID: "req-correction", PredecessorID: first.JudgmentID,
	}, testProposer)

	predRow := readStoredJudgment(t, s, first.JudgmentID)
	if predRow.Status != core.JudgmentSuperseded || predRow.SupersedesID != second.JudgmentID {
		t.Fatalf("predecessor = %s routing to %q, want superseded routing to %q", predRow.Status, predRow.SupersedesID, second.JudgmentID)
	}
	if predRow.DecidedAt == "" {
		t.Error("immediately superseded predecessor must carry decided_at (schema CHECK)")
	}
	var action, fromStatus string
	if err := s.db.QueryRow(`SELECT action, from_status FROM judgment_events WHERE judgment_id = ?`, first.JudgmentID).Scan(&action, &fromStatus); err != nil {
		t.Fatalf("read predecessor event: %v", err)
	}
	if action != "supersede" || fromStatus != "proposed" {
		t.Errorf("predecessor event = %s from %s, want supersede from proposed", action, fromStatus)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_relations WHERE from_judgment_id = ? AND to_judgment_id = ?`, first.JudgmentID, second.JudgmentID); n != 1 {
		t.Errorf("judgment_relations rows = %d, want 1", n)
	}

	// The correction still confirms; the already-superseded predecessor is not
	// re-superseded (exactly one routing row survives).
	expectedHash := core.ComputeJudgmentHash(second.Judgment)
	confirmJudgment(t, s, second.JudgmentID, expectedHash, "req-confirm", "professional resolution", principal)
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_relations`); n != 1 {
		t.Errorf("judgment_relations rows = %d, want exactly 1 (no duplicate supersession)", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ?`, first.JudgmentID); n != 1 {
		t.Errorf("predecessor events = %d, want exactly 1", n)
	}
}

// ──────────────────────────────────────────────
// Idempotency: replay and conflict
// ──────────────────────────────────────────────

func TestProposeJudgmentIdempotentReplayAndConflict(t *testing.T) {
	s := newTestStore(t)
	from, to := proposeContext(t, s)

	cmd := core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal reason", RequestID: "req-replay",
	}
	first := proposeJudgment(t, s, cmd, testProposer)
	if first.IdempotentReplay {
		t.Fatal("first proposal must not be a replay")
	}

	// Same request + same payload → replay of the ORIGINAL proposal.
	second, err := s.ProposeJudgment(context.Background(), cmd, testProposer)
	if err != nil {
		t.Fatalf("replay propose: %v", err)
	}
	if !second.IdempotentReplay {
		t.Fatal("replay must set IdempotentReplay=true")
	}
	if second.JudgmentID != first.JudgmentID || second.Judgment.ID != first.JudgmentID {
		t.Errorf("replay judgment id = %q, want %q (same committed proposal)", second.JudgmentID, first.JudgmentID)
	}

	// Same request id, DIFFERENT payload → IDEMPOTENCY_CONFLICT, never a second
	// proposal and never a silent dedup (design §3 rule 6).
	conflict := cmd
	conflict.Reason = "different reason"
	_, err = s.ProposeJudgment(context.Background(), conflict, testProposer)
	if auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Fatalf("code = %q, want IDEMPOTENCY_CONFLICT", auth.Code(err))
	}

	if n := countRows(t, s, `SELECT COUNT(*) FROM judgments WHERE status = 'proposed'`); n != 1 {
		t.Errorf("open proposals = %d, want exactly 1", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_idempotency_keys`); n != 1 {
		t.Errorf("judgment_idempotency_keys rows = %d, want exactly 1", n)
	}
}

func TestConfirmJudgmentIdempotentReplayAndConflict(t *testing.T) {
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal reason", RequestID: "req-propose",
	}, testProposer)
	expectedHash := core.ComputeJudgmentHash(proposed.Judgment)
	principal := controllerPrincipal(t)

	first := confirmJudgment(t, s, proposed.JudgmentID, expectedHash, "req-confirm", "professional resolution", principal)
	if first.IdempotentReplay {
		t.Fatal("first confirmation must not be a replay")
	}

	// Same request + same payload → the stored result replays (same event id).
	second, err := s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
		JudgmentID: proposed.JudgmentID, Resolution: "professional resolution",
		ExpectedJudgmentHash: expectedHash, RequestID: "req-confirm",
	}, principal, authz.NewJudgmentPolicy())
	if err != nil {
		t.Fatalf("replay confirm: %v", err)
	}
	if !second.IdempotentReplay {
		t.Fatal("replay must set IdempotentReplay=true")
	}
	if second.JudgmentEventID != first.JudgmentEventID {
		t.Errorf("replay event id = %s, want %s (same committed result)", second.JudgmentEventID, first.JudgmentEventID)
	}

	// Same request id, DIFFERENT payload (a different resolution) →
	// IDEMPOTENCY_CONFLICT.
	_, err = s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
		JudgmentID: proposed.JudgmentID, Resolution: "different resolution",
		ExpectedJudgmentHash: expectedHash, RequestID: "req-confirm",
	}, principal, authz.NewJudgmentPolicy())
	if auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Fatalf("code = %q, want IDEMPOTENCY_CONFLICT", auth.Code(err))
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID); n != 1 {
		t.Errorf("judgment_events rows = %d, want exactly 1 (no duplicate event on replay/conflict)", n)
	}
}

func TestWithdrawJudgmentIdempotentReplay(t *testing.T) {
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal reason", RequestID: "req-propose",
	}, testProposer)

	first := withdrawJudgment(t, s, proposed.JudgmentID, "req-withdraw", testProposer)
	if first.IdempotentReplay {
		t.Fatal("first withdrawal must not be a replay")
	}

	second, err := s.WithdrawJudgment(context.Background(), core.WithdrawJudgmentCommand{
		JudgmentID: proposed.JudgmentID, RequestID: "req-withdraw",
	}, testProposer)
	if err != nil {
		t.Fatalf("replay withdraw: %v", err)
	}
	if !second.IdempotentReplay {
		t.Fatal("replay must set IdempotentReplay=true")
	}
	if second.JudgmentEventID != first.JudgmentEventID {
		t.Errorf("replay event id = %s, want %s", second.JudgmentEventID, first.JudgmentEventID)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID); n != 1 {
		t.Errorf("judgment_events rows = %d, want exactly 1", n)
	}
}
