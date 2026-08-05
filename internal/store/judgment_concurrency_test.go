// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the cross-process race proof
// of the atomic judgment adjudication path (v0.4.0 Step 2 — adjudicable
// conflicts; batch B commit 3): two INDEPENDENTLY opened stores (separate
// *sql.DB handles against ONE WAL database file — MaxOpenConns(1) cannot
// serialize them) race the SAME confirmation and the SAME tuple proposal.
//
// BEGIN IMMEDIATE on a dedicated connection closes both races: exactly one
// confirm flips the row and the loser reads the committed status and returns
// INVALID_JUDGMENT_TRANSITION; exactly one open proposal survives the partial
// unique index and the loser returns JUDGMENT_CONFLICT. Run with -race. The
// single-process immutability/idempotency enforcement lives in
// judgment_immutability_test.go.
package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// TestConfirmJudgmentConcurrentSingleTransition is the judgment race proof: two
// stores confirm the SAME judgment concurrently with DIFFERENT request ids —
// exactly ONE confirmed transition (idempotentReplay=false), the loser returns
// INVALID_JUDGMENT_TRANSITION, and exactly ONE 'confirmed' event + ONE
// confirmed judgment row + ONE projected relation survive (design §5/§9).
func TestConfirmJudgmentConcurrentSingleTransition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-judgment-concurrent.db")
	s1 := openTestStorePath(t, path)
	s2 := openTestStorePath(t, path)

	from, to := proposeContext(t, s1)
	proposed := proposeJudgment(t, s1, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "concurrent proposal", RequestID: "req-concurrent-propose",
	}, testProposer)
	expectedHash := core.ComputeJudgmentHash(proposed.Judgment)
	principal := controllerPrincipal(t)
	policy := authz.NewJudgmentPolicy()

	start := make(chan struct{})
	results := make([]core.ConfirmJudgmentResult, 2)
	errs := make([]error, 2)
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
			results[i], errs[i] = st.ConfirmJudgment(context.Background(), core.ConfirmJudgmentCommand{
				JudgmentID:           proposed.JudgmentID,
				Resolution:           "concurrent resolution",
				ExpectedJudgmentHash: expectedHash,
				RequestID:            fmt.Sprintf("req-concurrent-confirm-%d", i),
			}, principal, policy)
		}(i)
	}
	close(start)
	wg.Wait()

	confirmed, losers := 0, 0
	for i := 0; i < 2; i++ {
		switch {
		case errs[i] == nil:
			confirmed++
			if results[i].IdempotentReplay {
				t.Fatalf("goroutine %d must be a fresh confirmation, not a replay", i)
			}
			if results[i].JudgmentEventID == "" {
				t.Errorf("goroutine %d: JudgmentEventID must not be empty", i)
			}
			if results[i].Judgment.Status != core.JudgmentConfirmed {
				t.Errorf("goroutine %d: status = %s, want confirmed", i, results[i].Judgment.Status)
			}
		case auth.Code(errs[i]) == auth.CodeInvalidJudgmentTransition:
			losers++
		default:
			t.Fatalf("goroutine %d: unexpected error %v (code %q)", i, errs[i], auth.Code(errs[i]))
		}
	}
	if confirmed != 1 || losers != 1 {
		t.Fatalf("confirmed=%d losers=%d, want exactly 1 confirmed and 1 INVALID_JUDGMENT_TRANSITION", confirmed, losers)
	}

	// Exactly one confirmed judgment row, one 'confirm' event, one projection.
	if n := countRows(t, s1, `SELECT COUNT(*) FROM judgments WHERE id = ? AND status = 'confirmed'`, proposed.JudgmentID); n != 1 {
		t.Errorf("confirmed rows = %d, want exactly 1", n)
	}
	if n := countRows(t, s1, `SELECT COUNT(*) FROM judgment_events WHERE judgment_id = ? AND action = 'confirm'`, proposed.JudgmentID); n != 1 {
		t.Errorf("confirm events = %d, want exactly 1", n)
	}
	if n := countRows(t, s1, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ?`, from, to); n != 1 {
		t.Errorf("relation projection rows = %d, want exactly 1", n)
	}
	// The winning confirm reservation persists; the loser's rolled back (the
	// proposal reservation is the third completed row).
	if n := countRows(t, s1, `SELECT COUNT(*) FROM judgment_idempotency_keys WHERE request_id LIKE 'req-concurrent-confirm-%'`); n != 1 {
		t.Errorf("confirm reservations = %d, want exactly 1 (loser rolled back)", n)
	}
}

// TestProposeJudgmentConcurrentSingleProposal races TWO proposals for the SAME
// (tenant, company, from, to, relation) tuple across two stores: the partial
// unique index admits exactly ONE proposed row and the loser returns
// JUDGMENT_CONFLICT (design §3 rule 6) — never a silent dedup and never a
// second open proposal.
func TestProposeJudgmentConcurrentSingleProposal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-proposal-concurrent.db")
	s1 := openTestStorePath(t, path)
	s2 := openTestStorePath(t, path)

	from, to := proposeContext(t, s1)

	start := make(chan struct{})
	results := make([]core.ProposeJudgmentResult, 2)
	errs := make([]error, 2)
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
			results[i], errs[i] = st.ProposeJudgment(context.Background(), core.ProposeJudgmentCommand{
				FromID: from, ToID: to, Relation: core.RelationSupports,
				Reason: "concurrent proposal", RequestID: fmt.Sprintf("req-concurrent-propose-%d", i),
			}, testProposer)
		}(i)
	}
	close(start)
	wg.Wait()

	proposed, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		switch {
		case errs[i] == nil:
			proposed++
			if results[i].IdempotentReplay {
				t.Fatalf("goroutine %d must be a fresh proposal, not a replay", i)
			}
		case auth.Code(errs[i]) == auth.CodeJudgmentConflict:
			conflicts++
		default:
			t.Fatalf("goroutine %d: unexpected error %v (code %q)", i, errs[i], auth.Code(errs[i]))
		}
	}
	if proposed != 1 || conflicts != 1 {
		t.Fatalf("proposed=%d conflicts=%d, want exactly 1 proposal and 1 JUDGMENT_CONFLICT", proposed, conflicts)
	}

	// Exactly one open proposal for the tuple; proposals write NO events; the
	// loser's reservation rolled back with its transaction.
	if n := countRows(t, s1, `SELECT COUNT(*) FROM judgments WHERE from_id = ? AND to_id = ? AND status = 'proposed'`, from, to); n != 1 {
		t.Errorf("open proposals = %d, want exactly 1", n)
	}
	if n := countRows(t, s1, `SELECT COUNT(*) FROM judgment_events`); n != 0 {
		t.Errorf("judgment_events rows = %d, want 0 (a proposal is not a transition)", n)
	}
	if n := countRows(t, s1, `SELECT COUNT(*) FROM judgment_idempotency_keys WHERE request_id LIKE 'req-concurrent-propose-%'`); n != 1 {
		t.Errorf("proposal reservations = %d, want exactly 1 (loser rolled back)", n)
	}
}
