// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the cross-process race proof
// of the atomic first-class reconciliation path (v0.5.0 — adjudicated
// reconciliations; design §3.2): two INDEPENDENTLY opened stores (separate
// *sql.DB handles against ONE WAL database file — MaxOpenConns(1) cannot
// serialize them) race the SAME confirmation and the SAME tuple proposal.
//
// BEGIN IMMEDIATE on a dedicated connection closes both races: exactly one
// confirm flips the row and the loser reads the committed status and returns
// INVALID_RECONCILIATION_TRANSITION; exactly one open proposal survives the
// partial unique index and the loser returns RECONCILIATION_CONFLICT. Run with
// -race.
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

// TestConfirmReconciliationConcurrentSingleTransition is the reconciliation
// race proof: two stores confirm the SAME reconciliation concurrently with
// DIFFERENT request ids — exactly ONE confirmed transition
// (idempotentReplay=false), the loser returns INVALID_RECONCILIATION_TRANSITION,
// and exactly ONE 'confirm' event + ONE confirmed row + ONE projected reconciles
// relation survive.
func TestConfirmReconciliationConcurrentSingleTransition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-reconciliation-concurrent.db")
	s1 := openTestStorePath(t, path)
	s2 := openTestStorePath(t, path)

	left, right := reconcileContext(t, s1)
	proposed := proposeReconciliation(t, s1, baseReconcileCmd(left, right), reconciliationProposer)
	expectedHash := core.ComputeReconciliationHash(proposed.Reconciliation)
	principal := controllerPrincipal(t)
	policy := authz.NewReconciliationPolicy()

	start := make(chan struct{})
	results := make([]core.ConfirmReconciliationResult, 2)
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
			results[i], errs[i] = st.ConfirmReconciliation(context.Background(), core.ConfirmReconciliationCommand{
				ReconciliationID:           proposed.ReconciliationID,
				Resolution:                 "concurrent resolution",
				ExpectedReconciliationHash: expectedHash,
				RequestID:                  fmt.Sprintf("req-concurrent-confirm-%d", i),
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
			if results[i].ReconciliationEventID == "" {
				t.Errorf("goroutine %d: ReconciliationEventID must not be empty", i)
			}
			if results[i].Reconciliation.Status != core.ReconciliationConfirmed {
				t.Errorf("goroutine %d: status = %s, want confirmed", i, results[i].Reconciliation.Status)
			}
		case auth.Code(errs[i]) == auth.CodeInvalidReconciliationTransition:
			losers++
		default:
			t.Fatalf("goroutine %d: unexpected error %v (code %q)", i, errs[i], auth.Code(errs[i]))
		}
	}
	if confirmed != 1 || losers != 1 {
		t.Fatalf("confirmed=%d losers=%d, want exactly 1 confirmed and 1 INVALID_RECONCILIATION_TRANSITION", confirmed, losers)
	}

	// Exactly one confirmed row, one 'confirm' event, one reconciles projection.
	if n := countRows(t, s1, `SELECT COUNT(*) FROM reconciliations WHERE id = ? AND status = 'confirmed'`, proposed.ReconciliationID); n != 1 {
		t.Errorf("confirmed rows = %d, want exactly 1", n)
	}
	if n := countRows(t, s1, `SELECT COUNT(*) FROM reconciliation_events WHERE reconciliation_id = ? AND action = 'confirm'`, proposed.ReconciliationID); n != 1 {
		t.Errorf("confirm events = %d, want exactly 1", n)
	}
	if n := countRows(t, s1, `SELECT COUNT(*) FROM relations WHERE from_id = ? AND to_id = ? AND relation = 'reconciles'`, left, right); n != 1 {
		t.Errorf("reconciles projection rows = %d, want exactly 1", n)
	}
	// The winning confirm reservation persists; the loser's rolled back (the
	// proposal reservation is the third completed row).
	if n := countRows(t, s1, `SELECT COUNT(*) FROM reconciliation_idempotency_keys WHERE request_id LIKE 'req-concurrent-confirm-%'`); n != 1 {
		t.Errorf("confirm reservations = %d, want exactly 1 (loser rolled back)", n)
	}
}

// TestProposeReconciliationConcurrentSingleProposal races TWO proposals for the
// SAME (tenant, company, left, right, method) tuple across two stores: the
// partial unique index admits exactly ONE proposed row and the loser returns
// RECONCILIATION_CONFLICT — never a silent dedup and never a second open
// proposal.
func TestProposeReconciliationConcurrentSingleProposal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-reconciliation-proposal-concurrent.db")
	s1 := openTestStorePath(t, path)
	s2 := openTestStorePath(t, path)

	left, right := reconcileContext(t, s1)

	start := make(chan struct{})
	results := make([]core.ProposeReconciliationResult, 2)
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
			cmd := baseReconcileCmd(left, right)
			cmd.RequestID = fmt.Sprintf("req-concurrent-propose-%d", i)
			results[i], errs[i] = st.ProposeReconciliation(context.Background(), cmd, reconciliationProposer)
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
		case auth.Code(errs[i]) == auth.CodeReconciliationConflict:
			conflicts++
		default:
			t.Fatalf("goroutine %d: unexpected error %v (code %q)", i, errs[i], auth.Code(errs[i]))
		}
	}
	if proposed != 1 || conflicts != 1 {
		t.Fatalf("proposed=%d conflicts=%d, want exactly 1 proposal and 1 RECONCILIATION_CONFLICT", proposed, conflicts)
	}

	// Exactly one open proposal for the tuple; proposals write NO events; the
	// loser's reservation rolled back with its transaction.
	if n := countRows(t, s1, `SELECT COUNT(*) FROM reconciliations WHERE left_memory_id = ? AND right_memory_id = ? AND status = 'proposed'`, left, right); n != 1 {
		t.Errorf("open proposals = %d, want exactly 1", n)
	}
	if n := countRows(t, s1, `SELECT COUNT(*) FROM reconciliation_events`); n != 0 {
		t.Errorf("reconciliation_events rows = %d, want 0 (a proposal is not a transition)", n)
	}
	if n := countRows(t, s1, `SELECT COUNT(*) FROM reconciliation_idempotency_keys WHERE request_id LIKE 'req-concurrent-propose-%'`); n != 1 {
		t.Errorf("proposal reservations = %d, want exactly 1 (loser rolled back)", n)
	}
}
