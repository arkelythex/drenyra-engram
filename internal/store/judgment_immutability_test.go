// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module enforces the judgment
// immutability contract (v0.4.0 Step 2 — adjudicable conflicts; batch B commit
// 3) at the STORE level on REAL rows produced by the atomic adjudication API:
// the v4 triggers freeze confirmed/terminal rows and judgment_events, the only
// legal confirmed-row update is confirmed→superseded with routing-only changes,
// proposed rows stay the machine's work area, idempotency is bound to the exact
// adjudicator principal, and judging pending_review observations never touches
// the observations. The raw-SQL/synthetic-row trigger proofs live in
// migration_v4_test.go; this file re-proves them through the real propose →
// confirm/reject/withdraw flows and adds the store-API terminality assertions.
//
// v3→v4 chain integrity (design §9: rollback, trigger, partial-index and
// old-memory readability) is already covered by migration_v4_test.go
// (TestV3StoreMigratesToV4AdditivelyPreservingRows and the trigger/index
// suites) and is not duplicated here. The two-store concurrency proofs live in
// judgment_concurrency_test.go.
package store

import (
	"context"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ──────────────────────────────────────────────
// Confirmed rows: trigger-enforced immutability
// ──────────────────────────────────────────────

func TestJudgmentConfirmedRowImmutable(t *testing.T) {
	// Design §9: "confirmed resolution/principal/proposal fields cannot update
	// or delete (IMMUTABLE_JUDGMENT)". Proved on a REAL confirmed row (the
	// synthetic-row variant lives in migration_v4_test.go).
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	principal := controllerPrincipal(t)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "immutable confirmed", RequestID: "req-propose",
	}, testProposer)
	expectedHash := core.ComputeJudgmentHash(proposed.Judgment)
	confirmJudgment(t, s, proposed.JudgmentID, expectedHash, "req-confirm", "professional resolution", principal)

	// A second same-tuple proposal provides the FK-valid successor id for the
	// trigger's routing branch (supersedes_id REFERENCES judgments(id)).
	successor := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "successor row", RequestID: "req-successor",
	}, testProposer)

	cases := []struct {
		name string
		stmt string
		args []any
	}{
		{"change resolution", `UPDATE judgments SET resolution = 'mutated' WHERE id = ?`, []any{proposed.JudgmentID}},
		{"change relation", `UPDATE judgments SET relation = 'contradicts' WHERE id = ?`, []any{proposed.JudgmentID}},
		{"change policy_version", `UPDATE judgments SET policy_version = 'judgment-policy/mutated' WHERE id = ?`, []any{proposed.JudgmentID}},
		{"change adjudicator", `UPDATE judgments SET adjudicator_subject_id = 'other-subject' WHERE id = ?`, []any{proposed.JudgmentID}},
		{"status to rejected", `UPDATE judgments SET status = 'rejected' WHERE id = ?`, []any{proposed.JudgmentID}},
		{"status to withdrawn", `UPDATE judgments SET status = 'withdrawn' WHERE id = ?`, []any{proposed.JudgmentID}},
		{"status to proposed", `UPDATE judgments SET status = 'proposed', decided_at = NULL WHERE id = ?`, []any{proposed.JudgmentID}},
		{"set supersedes_id while confirmed", `UPDATE judgments SET supersedes_id = ? WHERE id = ?`, []any{successor.JudgmentID, proposed.JudgmentID}},
		{"supersede with mutated resolution", `UPDATE judgments SET status = 'superseded', supersedes_id = ?, resolution = 'mutated' WHERE id = ?`, []any{successor.JudgmentID, proposed.JudgmentID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.db.Exec(tc.stmt, tc.args...); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT") {
				t.Fatalf("UPDATE %q on a confirmed row must abort with IMMUTABLE_JUDGMENT, got %v", tc.name, err)
			}
		})
	}

	// DELETE aborts (IMMUTABLE_JUDGMENT) and every aborted UPDATE left the row
	// byte-untouched.
	if _, err := s.db.Exec(`DELETE FROM judgments WHERE id = ?`, proposed.JudgmentID); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT") {
		t.Fatalf("DELETE on a confirmed row must abort with IMMUTABLE_JUDGMENT, got %v", err)
	}
	row := readStoredJudgment(t, s, proposed.JudgmentID)
	if row.Status != core.JudgmentConfirmed || row.Resolution != "professional resolution" || row.SupersedesID != "" {
		t.Errorf("confirmed row was mutated by aborted statements: status=%s resolution=%q supersedes_id=%q",
			row.Status, row.Resolution, row.SupersedesID)
	}
}

func TestJudgmentConfirmedSupersedeIsTheOnlyLegalUpdate(t *testing.T) {
	// Design §4: the ONLY legal update of a confirmed row is
	// status confirmed→superseded while setting a previously-empty
	// supersedes_id, with every proposal/adjudication field byte-equal. Proved
	// with direct SQL on a real row (the store API exercises the same branch
	// through the correction flow in judgment_test.go).
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	principal := controllerPrincipal(t)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "only legal update", RequestID: "req-propose",
	}, testProposer)
	expectedHash := core.ComputeJudgmentHash(proposed.Judgment)
	confirmJudgment(t, s, proposed.JudgmentID, expectedHash, "req-confirm", "professional resolution", principal)
	successor := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "successor row", RequestID: "req-successor",
	}, testProposer)

	if _, err := s.db.Exec(
		`UPDATE judgments SET status = 'superseded', supersedes_id = ?, updated_at = ? WHERE id = ?`,
		successor.JudgmentID, nowISO(), proposed.JudgmentID,
	); err != nil {
		t.Fatalf("confirmed→superseded routing update must be allowed, got %v", err)
	}
	row := readStoredJudgment(t, s, proposed.JudgmentID)
	if row.Status != core.JudgmentSuperseded || row.SupersedesID != successor.JudgmentID {
		t.Fatalf("row = (%s, %q), want (superseded, %q)", row.Status, row.SupersedesID, successor.JudgmentID)
	}
	if row.DecidedAt == "" {
		t.Error("superseded row must keep decided_at (schema CHECK: every non-proposed row carries it)")
	}

	// The superseded row is terminal: even routing fields may no longer change.
	if _, err := s.db.Exec(`UPDATE judgments SET resolution = 'mutated' WHERE id = ?`, proposed.JudgmentID); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT") {
		t.Fatalf("UPDATE on the now-superseded row must abort with IMMUTABLE_JUDGMENT, got %v", err)
	}
}

// ──────────────────────────────────────────────
// Terminal rows: store API and trigger both refuse
// ──────────────────────────────────────────────

func TestJudgmentTerminalRowsRejectStoreAPIAndSQL(t *testing.T) {
	// Design §4: updates of rejected|withdrawn|superseded rows abort
	// IMMUTABLE_JUDGMENT, and the pure predicates (§6) keep every terminal state
	// closed. Proved on REAL rows through the store API.
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	principal := controllerPrincipal(t)

	rejected := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "will be rejected", RequestID: "req-rej-prop",
	}, testProposer)
	rejectJudgment(t, s, rejected.JudgmentID, core.ComputeJudgmentHash(rejected.Judgment), "req-reject", "human rejection reason", principal)

	withdrawn := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationContradicts,
		Reason: "will be withdrawn", RequestID: "req-wd-prop",
	}, testProposer)
	withdrawJudgment(t, s, withdrawn.JudgmentID, "req-withdraw", testProposer)

	first := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationExplains,
		Reason: "will be superseded", RequestID: "req-ss-prop",
	}, testProposer)
	confirmJudgment(t, s, first.JudgmentID, core.ComputeJudgmentHash(first.Judgment), "req-ss-confirm", "first resolution", principal)
	correction := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationExplains,
		Reason: "correction", RequestID: "req-ss-correction", PredecessorID: first.JudgmentID,
	}, testProposer)
	confirmJudgment(t, s, correction.JudgmentID, core.ComputeJudgmentHash(correction.Judgment), "req-ss-confirm-correction", "corrected resolution", principal)

	for _, tc := range []struct {
		name string
		id   string
	}{
		{"rejected", rejected.JudgmentID},
		{"withdrawn", withdrawn.JudgmentID},
		{"superseded", first.JudgmentID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			currentHash := core.ComputeJudgmentHash(readStoredJudgment(t, s, tc.id))

			// The trigger aborts ANY update, including a "reopen".
			if _, err := s.db.Exec(`UPDATE judgments SET resolution = 'mutated' WHERE id = ?`, tc.id); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT") {
				t.Fatalf("UPDATE on a %s row must abort with IMMUTABLE_JUDGMENT, got %v", tc.name, err)
			}
			if _, err := s.db.Exec(`UPDATE judgments SET status = 'proposed', decided_at = NULL WHERE id = ?`, tc.id); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT") {
				t.Fatalf("reopen UPDATE on a %s row must abort with IMMUTABLE_JUDGMENT, got %v", tc.name, err)
			}

			// The store API refuses every transition (the status gate fires
			// before the hash gate, so the fresh hash is still provided).
			if _, err := s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
				JudgmentID: tc.id, Resolution: "cannot confirm", ExpectedJudgmentHash: currentHash, RequestID: "req-api-confirm-" + tc.name,
			}, principal, authz.NewJudgmentPolicy()); auth.Code(err) != auth.CodeInvalidJudgmentTransition {
				t.Errorf("confirm on a %s row: code = %q, want INVALID_JUDGMENT_TRANSITION", tc.name, auth.Code(err))
			}
			if _, err := s.RejectJudgment(context.Background(), core.RejectJudgmentCommand{
				JudgmentID: tc.id, Reason: "cannot reject", ExpectedJudgmentHash: currentHash, RequestID: "req-api-reject-" + tc.name,
			}, principal, authz.NewJudgmentPolicy()); auth.Code(err) != auth.CodeInvalidJudgmentTransition {
				t.Errorf("reject on a %s row: code = %q, want INVALID_JUDGMENT_TRANSITION", tc.name, auth.Code(err))
			}
			if _, err := s.WithdrawJudgment(context.Background(), core.WithdrawJudgmentCommand{
				JudgmentID: tc.id, RequestID: "req-api-withdraw-" + tc.name,
			}, testProposer); auth.Code(err) != auth.CodeInvalidJudgmentTransition {
				t.Errorf("withdraw on a %s row: code = %q, want INVALID_JUDGMENT_TRANSITION", tc.name, auth.Code(err))
			}
		})
	}

	// Every refused transition wrote NOTHING: each terminal row keeps exactly
	// its one immutable event (reject/withdraw/supersede).
	for _, tc := range []struct {
		id    string
		event string
	}{
		{rejected.JudgmentID, "reject"},
		{withdrawn.JudgmentID, "withdraw"},
		{first.JudgmentID, "supersede"},
	} {
		if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ? AND action = ?`, tc.id, tc.event); n != 1 {
			t.Errorf("events(%s) = %d, want exactly 1", tc.event, n)
		}
	}
}

// ──────────────────────────────────────────────
// Proposed rows: the machine's work area
// ──────────────────────────────────────────────

func TestJudgmentProposedRowIsTheMachinesWorkArea(t *testing.T) {
	// Design §4: proposed rows are the machine's work area — the state machine
	// may transition them (withdraw sets status + decided_at) and the trigger
	// must not lock those legitimate writes.
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "work area", RequestID: "req-propose",
	}, testProposer)

	if row := readStoredJudgment(t, s, proposed.JudgmentID); row.DecidedAt != "" {
		t.Errorf("open proposal decided_at = %q, want empty (schema CHECK)", row.DecidedAt)
	}

	withdrawJudgment(t, s, proposed.JudgmentID, "req-withdraw", testProposer)
	row := readStoredJudgment(t, s, proposed.JudgmentID)
	if row.Status != core.JudgmentWithdrawn || row.DecidedAt == "" {
		t.Errorf("machine transition row = (%s, decided_at %q), want (withdrawn, set)", row.Status, row.DecidedAt)
	}
}

// ──────────────────────────────────────────────
// Events: immutable on the real flow
// ──────────────────────────────────────────────

func TestJudgmentEventsImmutableOnRealFlow(t *testing.T) {
	// Design §4: all judgment_events updates/deletes abort IMMUTABLE_JUDGMENT_EVENT.
	// Proved on the real confirm event (the synthetic variant lives in
	// migration_v4_test.go).
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	principal := controllerPrincipal(t)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "event immutability", RequestID: "req-propose",
	}, testProposer)
	confirmJudgment(t, s, proposed.JudgmentID, core.ComputeJudgmentHash(proposed.Judgment), "req-confirm", "professional resolution", principal)

	if _, err := s.db.Exec(`UPDATE judgment_events SET reason = 'mutated' WHERE judgment_id = ?`, proposed.JudgmentID); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT_EVENT") {
		t.Fatalf("UPDATE on judgment_events must abort with IMMUTABLE_JUDGMENT_EVENT, got %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT_EVENT") {
		t.Fatalf("DELETE on judgment_events must abort with IMMUTABLE_JUDGMENT_EVENT, got %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID); n != 1 {
		t.Errorf("judgment_events rows = %d, want exactly 1 (aborted mutations wrote nothing)", n)
	}
}

// ──────────────────────────────────────────────
// Idempotency edge cases
// ──────────────────────────────────────────────

func TestJudgmentConfirmIdempotencyDifferentPrincipalConflict(t *testing.T) {
	// Design §5 step 1: a completed (tenant, requestId) reservation is bound to
	// the EXACT adjudicator subject — a different principal on the same request
	// id returns IDEMPOTENCY_CONFLICT, never a replay and never a second event.
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "principal binding", RequestID: "req-propose",
	}, testProposer)
	expectedHash := core.ComputeJudgmentHash(proposed.Judgment)
	principal := controllerPrincipal(t)
	confirmJudgment(t, s, proposed.JudgmentID, expectedHash, "req-confirm", "professional resolution", principal)

	// subject-2 in the SAME tenant/company/roles — only the binding differs.
	store := fixtureSessionStore(testOrgID, "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard)
	store.(*fixedSessionStore).membership.SubjectID = "subject-2"
	other := mustPrincipal(t, store)
	if other.SubjectID() == principal.SubjectID() {
		t.Fatal("fixture must produce a DIFFERENT principal subject")
	}

	_, err := s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
		JudgmentID: proposed.JudgmentID, Resolution: "professional resolution",
		ExpectedJudgmentHash: expectedHash, RequestID: "req-confirm",
	}, other, authz.NewJudgmentPolicy())
	if auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Fatalf("code = %q, want IDEMPOTENCY_CONFLICT (same request id, different principal)", auth.Code(err))
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ?`, proposed.JudgmentID); n != 1 {
		t.Errorf("judgment_events rows = %d, want exactly 1", n)
	}
}

func TestProposeReplayReturnsOriginalProposal(t *testing.T) {
	// The propose-replay contract (design §3 rule 6 / §5): a same-request retry
	// must replay the ORIGINAL proposal the reservation created — never a newer
	// proposal of the same tuple. The reservation stores judgment_id (the exact
	// judgment it produced), so the replay returns that row even after the
	// original was decided and a newer proposal was opened for the tuple.
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	principal := controllerPrincipal(t)

	original := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "original proposal", RequestID: "req-original",
	}, testProposer)
	confirmJudgment(t, s, original.JudgmentID, core.ComputeJudgmentHash(original.Judgment), "req-confirm", "professional resolution", principal)

	// A second proposal for the SAME tuple is legal once the original is
	// decided — the partial unique index only constrains OPEN proposals.
	newer := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "newer proposal", RequestID: "req-newer",
	}, testProposer)
	if newer.JudgmentID == original.JudgmentID {
		t.Fatal("the newer proposal must be a distinct judgment row")
	}

	replay, err := s.ProposeJudgment(context.Background(), core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "original proposal", RequestID: "req-original",
	}, testProposer)
	if err != nil {
		t.Fatalf("replay propose: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("a completed proposal reservation must replay")
	}
	// The replay returns the ORIGINAL judgment (id + confirmed status), never
	// the newer open proposal of the same tuple.
	if replay.JudgmentID != original.JudgmentID {
		t.Fatalf("replayed judgment id = %q, want the original %q", replay.JudgmentID, original.JudgmentID)
	}
	if replay.Judgment.Status != core.JudgmentConfirmed {
		t.Errorf("replayed status = %s, want confirmed (the original decided row)", replay.Judgment.Status)
	}
	// The newer proposal is untouched by the replay (read-only).
	if row := readStoredJudgment(t, s, newer.JudgmentID); row.Status != core.JudgmentProposed {
		t.Errorf("newer proposal status = %s, want proposed (replay is read-only)", row.Status)
	}
}

// ──────────────────────────────────────────────
// Observation status untouched
// ──────────────────────────────────────────────

func TestJudgmentDecisionLeavesPendingReviewObservationsUntouched(t *testing.T) {
	// Design §9: "judgment confirmation does not change either observation
	// status". Proved on pending_review observations — the case where an
	// approval gate exists — for BOTH confirm and reject.
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	fromSave, err := s.Save(gatedInput("tax.judgment.pr.from", "from observation"))
	if err != nil {
		t.Fatalf("save from observation: %v", err)
	}
	toSave, err := s.Save(gatedInput("tax.judgment.pr.to", "to observation"))
	if err != nil {
		t.Fatalf("save to observation: %v", err)
	}
	from, to := fromSave.Memory.Identity.ID, toSave.Memory.Identity.ID
	statusOf := func(id string) core.MemoryStatus {
		t.Helper()
		m, ok := s.FindByID(id)
		if !ok {
			t.Fatalf("observation %s not found", id)
		}
		return m.Status
	}
	if statusOf(from) != core.StatusPendingReview || statusOf(to) != core.StatusPendingReview {
		t.Fatalf("fixtures must be pending_review, got %s/%s", statusOf(from), statusOf(to))
	}
	principal := controllerPrincipal(t)

	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "confirm over pending review", RequestID: "req-confirm-prop",
	}, testProposer)
	confirmJudgment(t, s, proposed.JudgmentID, core.ComputeJudgmentHash(proposed.Judgment), "req-confirm", "professional resolution", principal)

	rejected := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationContradicts,
		Reason: "reject over pending review", RequestID: "req-reject-prop",
	}, testProposer)
	rejectJudgment(t, s, rejected.JudgmentID, core.ComputeJudgmentHash(rejected.Judgment), "req-reject", "human rejection reason", principal)

	for _, id := range []string{from, to} {
		if got := statusOf(id); got != core.StatusPendingReview {
			t.Errorf("observation %s status = %s, want pending_review (judgment acts never touch observations)", id, got)
		}
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM transition_log`); n != 0 {
		t.Errorf("transition_log rows = %d, want 0 (no observation transitions written)", n)
	}
}

// ──────────────────────────────────────────────
// Relation projection
// ──────────────────────────────────────────────

func TestJudgmentRelationProjectionExactlyOnce(t *testing.T) {
	// Design §9: confirmation inserts ONE compatibility observation relation
	// (the judgment's relation, from=from_id, to=to_id, actor=subject);
	// rejection inserts NONE; a second confirmation (replay OR a new request)
	// never duplicates the projection.
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	principal := controllerPrincipal(t)

	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "projection", RequestID: "req-propose",
	}, testProposer)
	expectedHash := core.ComputeJudgmentHash(proposed.Judgment)
	confirmJudgment(t, s, proposed.JudgmentID, expectedHash, "req-confirm", "professional resolution", principal)

	assertProjection := func(want int) {
		t.Helper()
		if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ? AND relation = 'supports'`, from, to); n != want {
			t.Errorf("relation projection rows = %d, want %d", n, want)
		}
	}
	assertProjection(1)
	var relRelation, relActor string
	if err := s.db.QueryRow(`SELECT relation, actor FROM relations WHERE from_id = ? AND to_id = ?`, from, to).Scan(&relRelation, &relActor); err != nil {
		t.Fatalf("read relation projection: %v", err)
	}
	if relRelation != string(core.RelationSupports) || relActor != principal.SubjectID() {
		t.Errorf("relation projection = %s by %s, want supports by %s", relRelation, relActor, principal.SubjectID())
	}

	// Replay of the SAME confirm request returns the stored result and does NOT
	// duplicate the projection.
	replay, err := s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
		JudgmentID: proposed.JudgmentID, Resolution: "professional resolution",
		ExpectedJudgmentHash: expectedHash, RequestID: "req-confirm",
	}, principal, authz.NewJudgmentPolicy())
	if err != nil || !replay.IdempotentReplay {
		t.Fatalf("replay confirm: err=%v replay=%v, want a replay", err, replay.IdempotentReplay)
	}
	assertProjection(1)

	// A NEW confirm request is an invalid transition and also writes no second
	// projection.
	if _, err := s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
		JudgmentID: proposed.JudgmentID, Resolution: "again",
		ExpectedJudgmentHash: expectedHash, RequestID: "req-confirm-2",
	}, principal, authz.NewJudgmentPolicy()); auth.Code(err) != auth.CodeInvalidJudgmentTransition {
		t.Fatalf("stale confirm code = %q, want INVALID_JUDGMENT_TRANSITION", auth.Code(err))
	}
	assertProjection(1)

	// Rejection inserts NONE (a different relation keeps the open tuple free).
	rejected := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationContradicts,
		Reason: "reject projection", RequestID: "req-propose-2",
	}, testProposer)
	rejectJudgment(t, s, rejected.JudgmentID, core.ComputeJudgmentHash(rejected.Judgment), "req-reject", "human rejection reason", principal)
	if n := countRows(t, s, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ?`, from, to); n != 1 {
		t.Errorf("relations rows = %d, want exactly 1 (only the confirmed supports projection)", n)
	}
}

// ──────────────────────────────────────────────
// Supersession routing and terminality
// ──────────────────────────────────────────────

func TestJudgmentSupersededPredecessorIsTerminal(t *testing.T) {
	// Design §9: a confirmed correction supersedes its predecessor and
	// JudgmentSuccessorOf(old) returns the correction; the predecessor row is
	// superseded + terminal (pure predicates all false, the store rejects any
	// transition, and the trigger freezes it).
	s := newTestStore(t)
	from, to := proposeContext(t, s)
	principal := controllerPrincipal(t)

	first := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "original", RequestID: "req-first",
	}, testProposer)
	firstHash := core.ComputeJudgmentHash(first.Judgment)
	confirmJudgment(t, s, first.JudgmentID, firstHash, "req-confirm-first", "first resolution", principal)
	correction := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "correction", RequestID: "req-correction", PredecessorID: first.JudgmentID,
	}, testProposer)
	confirmJudgment(t, s, correction.JudgmentID, core.ComputeJudgmentHash(correction.Judgment), "req-confirm-correction", "corrected resolution", principal)

	pred := readStoredJudgment(t, s, first.JudgmentID)
	if pred.Status != core.JudgmentSuperseded || pred.SupersedesID != correction.JudgmentID {
		t.Fatalf("predecessor = (%s, %q), want (superseded, %q)", pred.Status, pred.SupersedesID, correction.JudgmentID)
	}

	// Pure predicates: the superseded predecessor opens nothing.
	if core.CanConfirm(pred.Status) || core.CanRejectJudgment(pred.Status) || core.CanWithdraw(pred.Status) || core.CanSupersedeConfirmed(pred.Status) {
		t.Errorf("pure predicates must all be false for a superseded row (CanConfirm=%v CanReject=%v CanWithdraw=%v CanSupersede=%v)",
			core.CanConfirm(pred.Status), core.CanRejectJudgment(pred.Status), core.CanWithdraw(pred.Status), core.CanSupersedeConfirmed(pred.Status))
	}
	for _, to := range []core.JudgmentStatus{core.JudgmentConfirmed, core.JudgmentRejected, core.JudgmentWithdrawn, core.JudgmentSuperseded, core.JudgmentProposed} {
		if core.IsLegalJudgmentTransition(pred.Status, to) {
			t.Errorf("IsLegalJudgmentTransition(%s, %s) = true, want false", pred.Status, to)
		}
	}

	// Routing: JudgmentSuccessorOf routes readers to the correction.
	succ, ok := s.JudgmentSuccessorOf(context.Background(), first.JudgmentID)
	if !ok || succ.ID != correction.JudgmentID {
		t.Fatalf("JudgmentSuccessorOf = (%s, %v), want correction %s", succ.ID, ok, correction.JudgmentID)
	}

	// The store rejects every transition on the superseded predecessor.
	predHash := core.ComputeJudgmentHash(pred)
	if _, err := s.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
		JudgmentID: first.JudgmentID, Resolution: "cannot confirm", ExpectedJudgmentHash: predHash, RequestID: "req-pred-confirm",
	}, principal, authz.NewJudgmentPolicy()); auth.Code(err) != auth.CodeInvalidJudgmentTransition {
		t.Errorf("confirm predecessor code = %q, want INVALID_JUDGMENT_TRANSITION", auth.Code(err))
	}
	if _, err := s.RejectJudgment(context.Background(), core.RejectJudgmentCommand{
		JudgmentID: first.JudgmentID, Reason: "cannot reject", ExpectedJudgmentHash: predHash, RequestID: "req-pred-reject",
	}, principal, authz.NewJudgmentPolicy()); auth.Code(err) != auth.CodeInvalidJudgmentTransition {
		t.Errorf("reject predecessor code = %q, want INVALID_JUDGMENT_TRANSITION", auth.Code(err))
	}
	if _, err := s.WithdrawJudgment(context.Background(), core.WithdrawJudgmentCommand{
		JudgmentID: first.JudgmentID, RequestID: "req-pred-withdraw",
	}, testProposer); auth.Code(err) != auth.CodeInvalidJudgmentTransition {
		t.Errorf("withdraw predecessor code = %q, want INVALID_JUDGMENT_TRANSITION", auth.Code(err))
	}

	// The trigger freezes the row.
	if _, err := s.db.Exec(`UPDATE judgments SET resolution = 'mutated' WHERE id = ?`, first.JudgmentID); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT") {
		t.Fatalf("UPDATE on the superseded predecessor must abort with IMMUTABLE_JUDGMENT, got %v", err)
	}
}
