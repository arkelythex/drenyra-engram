// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the authenticated
// first-class reconciliation CLI surface (v0.5.0 — design §3.2/§6) through the
// built binary: propose and withdraw carry the agent provenance source {cli,
// cli, agent} (never authority); confirm/reject derive the principal ONLY from
// the stored 0600 CLI session and reject caller-supplied authority flags; show
// is read-only. The amounts are int64 cents parsed from the CLI flags; a
// non-integer amount is a usage error, never a silent float conversion.
package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// seedReconciliationObservationsCLI saves two same-company observations through
// the built CLI (the CLI's fixed company scope: cli / cliRucA / 202401) and
// returns their ids — the pair a proposal reconciles.
func seedReconciliationObservationsCLI(t *testing.T, db string) (leftID, rightID string) {
	t.Helper()
	dir := t.TempDir()
	leftID = saveViaCLI(t, db, writeSaveInput(t, dir, "left.json",
		cliSaveInput("reconcile/cli/left", cliRucA, "202401", "Saldo de mayor 4011", "smoke-r-1")))
	rightID = saveViaCLI(t, db, writeSaveInput(t, dir, "right.json",
		cliSaveInput("reconcile/cli/right", cliRucA, "202401", "Saldo de SIRE 4011", "smoke-r-2")))
	return leftID, rightID
}

// proposeReconciliationCLI proposes over the pair through the built CLI and
// returns the parsed ProposeReconciliationResult.
func proposeReconciliationCLI(t *testing.T, db, leftID, rightID string) core.ProposeReconciliationResult {
	t.Helper()
	stdout, stderr, code := runCLI(t, "reconcile", "propose", leftID, rightID,
		"--method", "extracto_contable", "--currency", "PEN",
		"--left-amount-cents", "1000000", "--right-amount-cents", "984000",
		"--tolerance-cents", "16000", "--reason", "diferencia de saldo entre mayor y SIRE", "--db", db)
	if code != 0 {
		t.Fatalf("reconcile propose failed (exit %d): %s", code, stderr)
	}
	var result core.ProposeReconciliationResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("reconcile propose output not JSON: %v\n%s", err, stdout)
	}
	return result
}

// reconciliationHashCLI computes the CURRENT hash of a stored reconciliation
// (the hash an adjudicator would have seen) through the real store.
func reconciliationHashCLI(t *testing.T, db, id string) string {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	r, ok := st.GetReconciliation(context.Background(), id)
	if !ok {
		t.Fatalf("reconciliation %s not found", id)
	}
	return core.ComputeReconciliationHash(r)
}

// seedForeignReconciliationCLI proposes a reconciliation with a DIFFERENT agent
// provenance identity through the store directly, so the CLI caller
// (cli/cli/agent) can never withdraw it (provenance continuity).
func seedForeignReconciliationCLI(t *testing.T, db, leftID, rightID string) string {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	result, err := st.ProposeReconciliation(context.Background(), core.ProposeReconciliationCommand{
		LeftMemoryID:     leftID,
		RightMemoryID:    rightID,
		Method:           "extracto_contable",
		Currency:         "PEN",
		LeftAmountCents:  1000000,
		RightAmountCents: 1000000,
		ToleranceCents:   0,
		Reason:           "propuesta de otro agente",
		RequestID:        "foreign-reconciliation-1",
	}, core.Source{System: "other-cli", ActorID: "other-agent", ActorKind: core.ActorKindAgent})
	if err != nil {
		t.Fatalf("seed foreign proposal: %v", err)
	}
	return result.ReconciliationID
}

// TestCLIReconcileProposeHappyPath: the agent CLI caller proposes over two
// existing observations → the ProposeReconciliationResult JSON with the
// proposed reconciliation; the provenance source is exactly {system: cli,
// actorId: cli, actorKind: agent}, the amounts are int64 cents and the variance
// is engine-derived.
func TestCLIReconcileProposeHappyPath(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	leftID, rightID := seedReconciliationObservationsCLI(t, db)

	result := proposeReconciliationCLI(t, db, leftID, rightID)
	if result.ReconciliationID == "" || result.ReconciliationID != result.Reconciliation.ID {
		t.Fatalf("reconciliationId = %q, want the reconciliation id %q", result.ReconciliationID, result.Reconciliation.ID)
	}
	if result.IdempotentReplay {
		t.Fatal("fresh proposal must not be a replay")
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
	if r.LeftAmountCents != 1000000 || r.RightAmountCents != 984000 || r.VarianceCents != 16000 || r.ToleranceCents != 16000 {
		t.Errorf("amounts = %d/%d variance %d tolerance %d, want 1000000/984000/16000/16000 (int64 cents)", r.LeftAmountCents, r.RightAmountCents, r.VarianceCents, r.ToleranceCents)
	}
	wantProposer := core.Source{System: "cli", ActorID: "cli", ActorKind: core.ActorKindAgent}
	if r.Proposer != wantProposer {
		t.Errorf("proposer = %+v, want %+v (agent provenance, never authority)", r.Proposer, wantProposer)
	}
	if r.TenantID != cliOrganizationID || r.CompanyID != cliRucA {
		t.Errorf("scope = %s/%s, want %s/%s (derived from the observations)", r.TenantID, r.CompanyID, cliOrganizationID, cliRucA)
	}
	if r.ProposalReason != "diferencia de saldo entre mayor y SIRE" || r.Resolution != "" {
		t.Errorf("reason/resolution = %q/%q, want reason/empty", r.ProposalReason, r.Resolution)
	}
	if r.DecidedAt != "" {
		t.Errorf("decidedAt = %q, want empty for an open proposal", r.DecidedAt)
	}
}

// TestCLIReconcileProposeRejectsFloatAmount: money is int64 cents only — a float
// amount is a usage error (exit 2), never a silent float conversion.
func TestCLIReconcileProposeRejectsFloatAmount(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	leftID, rightID := seedReconciliationObservationsCLI(t, db)

	stdout, stderr, code := runCLI(t, "reconcile", "propose", leftID, rightID,
		"--method", "extracto_contable", "--currency", "PEN",
		"--left-amount-cents", "10000.50", "--right-amount-cents", "10000",
		"--reason", "r", "--db", db)
	if code != 1 {
		t.Fatalf("reconcile propose float amount exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("amount failure must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "integer amount in cents") {
		t.Fatalf("stderr must explain the integer-cents contract: %q", stderr)
	}
}

// TestCLIReconcileConfirmWithoutSession: confirm with no authenticated CLI
// session fails closed with AUTHENTICATION_REQUIRED and points at auth login —
// the exact judgment approval pattern.
func TestCLIReconcileConfirmWithoutSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	leftID, rightID := seedReconciliationObservationsCLI(t, db)
	proposed := proposeReconciliationCLI(t, db, leftID, rightID)
	hash := reconciliationHashCLI(t, db, proposed.ReconciliationID)

	stdout, stderr, code := runCLIEnv(t, sessionFileEnv(t.TempDir()),
		"reconcile", "confirm", proposed.ReconciliationID,
		"--resolution", "el mayor y el SIRE concilian tras el ajuste",
		"--expected-hash", hash, "--db", db)
	if code != 1 {
		t.Fatalf("confirm without session exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("confirm without session must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "AUTHENTICATION_REQUIRED") || !strings.Contains(stderr, "auth login") {
		t.Fatalf("stderr must carry AUTHENTICATION_REQUIRED and point at auth login: %q", stderr)
	}
}

// TestCLIReconcileConfirmRejectsActorFlag: caller-supplied authority is gone
// from the authenticated reconciliation commands — --actor (and any
// role/subject flag) is an unknown flag (usage error 2).
func TestCLIReconcileConfirmRejectsActorFlag(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	stdout, stderr, code := runCLI(t, "reconcile", "confirm", "some-reconciliation",
		"--resolution", "x", "--expected-hash", "x", "--actor", "maria.torres", "--db", db)
	if code != 2 {
		t.Fatalf("reconcile confirm --actor exit = %d, want 2 (flag rejected); stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("flag rejection must not write stdout: %q", stdout)
	}
}

// TestCLIReconcileConfirmWithSeededSession is the full authenticated round trip:
// the seeded controller session resolves the principal from the 0600 session
// file, and confirm prints the confirmed reconciliation JSON (resolution +
// adjudicator).
func TestCLIReconcileConfirmWithSeededSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	env := writeSessionFile(t, t.TempDir(), token)
	leftID, rightID := seedReconciliationObservationsCLI(t, db)
	proposed := proposeReconciliationCLI(t, db, leftID, rightID)
	hash := reconciliationHashCLI(t, db, proposed.ReconciliationID)

	stdout, stderr, code := runCLIEnv(t, env,
		"reconcile", "confirm", proposed.ReconciliationID,
		"--resolution", "el mayor y el SIRE concilian tras el ajuste por comprobante tardio",
		"--expected-hash", hash, "--db", db)
	if code != 0 {
		t.Fatalf("reconcile confirm failed (exit %d): %s", code, stderr)
	}
	var result core.ConfirmReconciliationResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("confirm output not JSON: %v\n%s", err, stdout)
	}
	if result.ReconciliationID != proposed.ReconciliationID {
		t.Errorf("reconciliationId = %q, want %q", result.ReconciliationID, proposed.ReconciliationID)
	}
	if result.IdempotentReplay {
		t.Error("fresh confirmation must not be a replay")
	}
	if result.ReconciliationEventID == "" {
		t.Error("reconciliationEventId must be set on confirmation")
	}
	r := result.Reconciliation
	if r.Status != core.ReconciliationConfirmed {
		t.Errorf("status = %q, want confirmed", r.Status)
	}
	if r.Resolution != "el mayor y el SIRE concilian tras el ajuste por comprobante tardio" {
		t.Errorf("resolution = %q, want the professional resolution", r.Resolution)
	}
	if r.Adjudicator == nil || r.Adjudicator.SubjectID != "maria.torres" {
		t.Errorf("adjudicator = %+v, want maria.torres (from the session, never caller-declared)", r.Adjudicator)
	}
	if r.DecidedAt == "" {
		t.Error("decidedAt must be set on confirmation")
	}
}

// TestCLIReconcileRejectWithSeededSession: the same authenticated pattern
// rejects a proposal → the human reason is stored as the resolution (terminal).
func TestCLIReconcileRejectWithSeededSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	env := writeSessionFile(t, t.TempDir(), token)
	leftID, rightID := seedReconciliationObservationsCLI(t, db)
	proposed := proposeReconciliationCLI(t, db, leftID, rightID)
	hash := reconciliationHashCLI(t, db, proposed.ReconciliationID)

	stdout, stderr, code := runCLIEnv(t, env,
		"reconcile", "reject", proposed.ReconciliationID,
		"--reason", "la evidencia no respalda el saldo",
		"--expected-hash", hash, "--db", db)
	if code != 0 {
		t.Fatalf("reconcile reject failed (exit %d): %s", code, stderr)
	}
	var result core.RejectReconciliationResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("reject output not JSON: %v\n%s", err, stdout)
	}
	if result.Reconciliation.Status != core.ReconciliationRejected {
		t.Errorf("status = %q, want rejected (terminal)", result.Reconciliation.Status)
	}
	if result.Reconciliation.Resolution != "la evidencia no respalda el saldo" {
		t.Errorf("resolution = %q, want the human rejection reason", result.Reconciliation.Resolution)
	}
	if result.ReconciliationEventID == "" {
		t.Error("reconciliationEventId must be set on rejection")
	}
}

// TestCLIReconcileWithdrawHappyPath: the SAME agent proposer identity
// (cli/cli/agent) withdraws its own proposal → the withdrawn reconciliation
// JSON.
func TestCLIReconcileWithdrawHappyPath(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	leftID, rightID := seedReconciliationObservationsCLI(t, db)
	proposed := proposeReconciliationCLI(t, db, leftID, rightID)

	stdout, stderr, code := runCLI(t, "reconcile", "withdraw", proposed.ReconciliationID, "--db", db)
	if code != 0 {
		t.Fatalf("reconcile withdraw failed (exit %d): %s", code, stderr)
	}
	var result core.WithdrawReconciliationResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("withdraw output not JSON: %v\n%s", err, stdout)
	}
	if result.Reconciliation.Status != core.ReconciliationWithdrawn {
		t.Errorf("status = %q, want withdrawn (terminal)", result.Reconciliation.Status)
	}
	if result.ReconciliationEventID == "" {
		t.Error("reconciliationEventId must be set on withdrawal")
	}
}

// TestCLIReconcileWithdrawDifferentProposer: a proposal made by a DIFFERENT
// agent identity cannot be withdrawn from the CLI — PROPOSAL_UNAUTHORIZED
// (exit 1).
func TestCLIReconcileWithdrawDifferentProposer(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	leftID, rightID := seedReconciliationObservationsCLI(t, db)
	foreignID := seedForeignReconciliationCLI(t, db, leftID, rightID)

	stdout, stderr, code := runCLI(t, "reconcile", "withdraw", foreignID, "--db", db)
	if code != 1 {
		t.Fatalf("reconcile withdraw foreign exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("failed withdrawal must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "PROPOSAL_UNAUTHORIZED") {
		t.Fatalf("stderr must carry PROPOSAL_UNAUTHORIZED: %q", stderr)
	}
}

// TestCLIReconcileShow: reconcile show prints the reconciliation JSON
// (read-only); a missing reconciliation fails with RECONCILIATION_NOT_FOUND.
func TestCLIReconcileShow(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	leftID, rightID := seedReconciliationObservationsCLI(t, db)
	proposed := proposeReconciliationCLI(t, db, leftID, rightID)

	stdout, stderr, code := runCLI(t, "reconcile", "show", proposed.ReconciliationID, "--db", db)
	if code != 0 {
		t.Fatalf("reconcile show failed (exit %d): %s", code, stderr)
	}
	var reconciliation core.Reconciliation
	if err := json.Unmarshal([]byte(stdout), &reconciliation); err != nil {
		t.Fatalf("show output not JSON: %v\n%s", err, stdout)
	}
	if reconciliation.ID != proposed.ReconciliationID || reconciliation.Status != core.ReconciliationProposed {
		t.Errorf("shown reconciliation = %s/%s, want %s/proposed", reconciliation.ID, reconciliation.Status, proposed.ReconciliationID)
	}

	stdout, stderr, code = runCLI(t, "reconcile", "show", "missing-reconciliation", "--db", db)
	if code != 1 {
		t.Fatalf("reconcile show missing exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("missing reconciliation must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "RECONCILIATION_NOT_FOUND") {
		t.Fatalf("stderr must carry RECONCILIATION_NOT_FOUND: %q", stderr)
	}
}

// TestCLIHelpListsReconcileCommands: help documents the reconciliation surface.
func TestCLIHelpListsReconcileCommands(t *testing.T) {
	stdout, _, code := runCLI(t, "help")
	if code != 0 {
		t.Fatalf("help exit = %d, want 0", code)
	}
	for _, line := range []string{
		"reconcile propose", "reconcile confirm", "reconcile reject", "reconcile withdraw", "reconcile show",
	} {
		if !strings.Contains(stdout, line) {
			t.Fatalf("help output missing %q", line)
		}
	}
}
