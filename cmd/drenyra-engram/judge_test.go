// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the authenticated judgment
// CLI surface (v0.4.0 Step 2 — design §7) through the built binary: propose and
// withdraw carry the agent provenance source {cli, cli, agent} (never
// authority); confirm/reject derive the principal ONLY from the stored 0600 CLI
// session and reject caller-supplied authority flags; show is read-only.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// seedJudgeObservationsCLI saves two same-company observations through the built
// CLI (the CLI's fixed company scope: cli / cliRucA / 202401) and returns their
// ids — the pair a proposal adjudicates.
func seedJudgeObservationsCLI(t *testing.T, db string) (fromID, toID string) {
	t.Helper()
	dir := t.TempDir()
	fromID = saveViaCLI(t, db, writeSaveInput(t, dir, "from.json",
		cliSaveInput("judge/cli/from", cliRucA, "202401", "Saldo de mayor 4011", "smoke-j-1")))
	toID = saveViaCLI(t, db, writeSaveInput(t, dir, "to.json",
		cliSaveInput("judge/cli/to", cliRucA, "202401", "Saldo de SIRE 4011", "smoke-j-2")))
	return fromID, toID
}

// proposeJudgmentCLI proposes over the pair through the built CLI and returns
// the parsed ProposeJudgmentResult.
func proposeJudgmentCLI(t *testing.T, db, fromID, toID string) core.ProposeJudgmentResult {
	t.Helper()
	stdout, stderr, code := runCLI(t, "judge", "propose", fromID, toID,
		"--relation", "contradicts", "--reason", "diferencia de saldo entre mayor y SIRE", "--db", db)
	if code != 0 {
		t.Fatalf("judge propose failed (exit %d): %s", code, stderr)
	}
	var result core.ProposeJudgmentResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("judge propose output not JSON: %v\n%s", err, stdout)
	}
	return result
}

// judgmentHashCLI computes the CURRENT hash of a stored judgment (the hash an
// adjudicator would have seen) through the real store.
func judgmentHashCLI(t *testing.T, db, id string) string {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	j, ok := st.GetJudgment(context.Background(), id)
	if !ok {
		t.Fatalf("judgment %s not found", id)
	}
	return core.ComputeJudgmentHash(j)
}

// seedForeignJudgmentCLI proposes a judgment with a DIFFERENT agent provenance
// identity through the store directly, so the CLI caller (cli/cli/agent) can
// never withdraw it (provenance continuity).
func seedForeignJudgmentCLI(t *testing.T, db, fromID, toID string) string {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	result, err := st.ProposeJudgment(context.Background(), core.ProposeJudgmentCommand{
		FromID:    fromID,
		ToID:      toID,
		Relation:  core.RelationSupports,
		Reason:    "propuesta de otro agente",
		RequestID: "foreign-proposal-1",
	}, core.Source{System: "other-cli", ActorID: "other-agent", ActorKind: core.ActorKindAgent})
	if err != nil {
		t.Fatalf("seed foreign proposal: %v", err)
	}
	return result.JudgmentID
}

// TestCLIJudgeProposeHappyPath: the agent CLI caller proposes over two existing
// observations → the ProposeJudgmentResult JSON with the proposed judgment; the
// provenance source is exactly {system: cli, actorId: cli, actorKind: agent}.
func TestCLIJudgeProposeHappyPath(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	fromID, toID := seedJudgeObservationsCLI(t, db)

	result := proposeJudgmentCLI(t, db, fromID, toID)
	if result.JudgmentID == "" || result.JudgmentID != result.Judgment.ID {
		t.Fatalf("judgmentId = %q, want the judgment id %q", result.JudgmentID, result.Judgment.ID)
	}
	if result.IdempotentReplay {
		t.Fatal("fresh proposal must not be a replay")
	}
	j := result.Judgment
	if j.Status != core.JudgmentProposed {
		t.Errorf("status = %q, want proposed", j.Status)
	}
	if j.FromID != fromID || j.ToID != toID || j.Relation != core.RelationContradicts {
		t.Errorf("pair = %s/%s %s, want %s/%s contradicts", j.FromID, j.ToID, j.Relation, fromID, toID)
	}
	wantProposer := core.Source{System: "cli", ActorID: "cli", ActorKind: core.ActorKindAgent}
	if j.Proposer != wantProposer {
		t.Errorf("proposer = %+v, want %+v (agent provenance, never authority)", j.Proposer, wantProposer)
	}
	if j.TenantID != cliOrganizationID || j.CompanyID != cliRucA {
		t.Errorf("scope = %s/%s, want %s/%s (derived from the observations)", j.TenantID, j.CompanyID, cliOrganizationID, cliRucA)
	}
	if j.ProposalReason != "diferencia de saldo entre mayor y SIRE" || j.Resolution != "" {
		t.Errorf("reason/resolution = %q/%q, want reason/empty", j.ProposalReason, j.Resolution)
	}
	if j.DecidedAt != "" {
		t.Errorf("decidedAt = %q, want empty for an open proposal", j.DecidedAt)
	}
}

// TestCLIJudgeProposeRejectsBadRelation: conflicts_with stays a legacy marker
// and is never proposable; the CLI validates the six relations as a usage error.
func TestCLIJudgeProposeRejectsBadRelation(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	fromID, toID := seedJudgeObservationsCLI(t, db)

	stdout, stderr, code := runCLI(t, "judge", "propose", fromID, toID,
		"--relation", "conflicts_with", "--reason", "r", "--db", db)
	if code != 2 {
		t.Fatalf("judge propose bad relation exit = %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("usage error must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "invalid relation") {
		t.Fatalf("stderr must explain the invalid relation: %q", stderr)
	}
}

// TestCLIJudgeConfirmWithoutSession: confirm with no authenticated CLI session
// fails closed with AUTHENTICATION_REQUIRED and points at auth login — the
// exact Step 1 approval pattern.
func TestCLIJudgeConfirmWithoutSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	fromID, toID := seedJudgeObservationsCLI(t, db)
	proposed := proposeJudgmentCLI(t, db, fromID, toID)
	hash := judgmentHashCLI(t, db, proposed.JudgmentID)

	stdout, stderr, code := runCLIEnv(t, sessionFileEnv(t.TempDir()),
		"judge", "confirm", proposed.JudgmentID,
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

// TestCLIJudgeConfirmRejectsActorFlag: caller-supplied authority is gone from
// the authenticated judgment commands — --actor (and any role/subject flag) is
// an unknown flag (usage error 2).
func TestCLIJudgeConfirmRejectsActorFlag(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	stdout, stderr, code := runCLI(t, "judge", "confirm", "some-judgment",
		"--resolution", "x", "--expected-hash", "x", "--actor", "maria.torres", "--db", db)
	if code != 2 {
		t.Fatalf("judge confirm --actor exit = %d, want 2 (flag rejected); stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("flag rejection must not write stdout: %q", stdout)
	}
}

// TestCLIJudgeConfirmWithSeededSession is the full authenticated round trip: the
// seeded controller session resolves the principal from the 0600 session file,
// and confirm prints the confirmed judgment JSON (resolution + adjudicator).
func TestCLIJudgeConfirmWithSeededSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	env := writeSessionFile(t, t.TempDir(), token)
	fromID, toID := seedJudgeObservationsCLI(t, db)
	proposed := proposeJudgmentCLI(t, db, fromID, toID)
	hash := judgmentHashCLI(t, db, proposed.JudgmentID)

	stdout, stderr, code := runCLIEnv(t, env,
		"judge", "confirm", proposed.JudgmentID,
		"--resolution", "el mayor y el SIRE concilian tras el ajuste por comprobante tardio",
		"--expected-hash", hash, "--db", db)
	if code != 0 {
		t.Fatalf("judge confirm failed (exit %d): %s", code, stderr)
	}
	var result core.ConfirmJudgmentResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("confirm output not JSON: %v\n%s", err, stdout)
	}
	if result.JudgmentID != proposed.JudgmentID {
		t.Errorf("judgmentId = %q, want %q", result.JudgmentID, proposed.JudgmentID)
	}
	if result.IdempotentReplay {
		t.Error("fresh confirmation must not be a replay")
	}
	if result.JudgmentEventID == "" {
		t.Error("judgmentEventId must be set on confirmation")
	}
	j := result.Judgment
	if j.Status != core.JudgmentConfirmed {
		t.Errorf("status = %q, want confirmed", j.Status)
	}
	if j.Resolution != "el mayor y el SIRE concilian tras el ajuste por comprobante tardio" {
		t.Errorf("resolution = %q, want the professional resolution", j.Resolution)
	}
	if j.Adjudicator == nil || j.Adjudicator.SubjectID != "maria.torres" {
		t.Errorf("adjudicator = %+v, want maria.torres (from the session, never caller-declared)", j.Adjudicator)
	}
	if j.DecidedAt == "" {
		t.Error("decidedAt must be set on confirmation")
	}
}

// TestCLIJudgeRejectWithSeededSession: the same authenticated pattern rejects a
// proposal → the human reason is stored as the resolution (terminal).
func TestCLIJudgeRejectWithSeededSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	env := writeSessionFile(t, t.TempDir(), token)
	fromID, toID := seedJudgeObservationsCLI(t, db)
	proposed := proposeJudgmentCLI(t, db, fromID, toID)
	hash := judgmentHashCLI(t, db, proposed.JudgmentID)

	stdout, stderr, code := runCLIEnv(t, env,
		"judge", "reject", proposed.JudgmentID,
		"--reason", "la evidencia no respalda la relacion",
		"--expected-hash", hash, "--db", db)
	if code != 0 {
		t.Fatalf("judge reject failed (exit %d): %s", code, stderr)
	}
	var result core.RejectJudgmentResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("reject output not JSON: %v\n%s", err, stdout)
	}
	if result.Judgment.Status != core.JudgmentRejected {
		t.Errorf("status = %q, want rejected (terminal)", result.Judgment.Status)
	}
	if result.Judgment.Resolution != "la evidencia no respalda la relacion" {
		t.Errorf("resolution = %q, want the human rejection reason", result.Judgment.Resolution)
	}
	if result.JudgmentEventID == "" {
		t.Error("judgmentEventId must be set on rejection")
	}
}

// TestCLIJudgeWithdrawHappyPath: the SAME agent proposer identity (cli/cli/
// agent) withdraws its own proposal → the withdrawn judgment JSON.
func TestCLIJudgeWithdrawHappyPath(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	fromID, toID := seedJudgeObservationsCLI(t, db)
	proposed := proposeJudgmentCLI(t, db, fromID, toID)

	stdout, stderr, code := runCLI(t, "judge", "withdraw", proposed.JudgmentID, "--db", db)
	if code != 0 {
		t.Fatalf("judge withdraw failed (exit %d): %s", code, stderr)
	}
	var result core.WithdrawJudgmentResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("withdraw output not JSON: %v\n%s", err, stdout)
	}
	if result.Judgment.Status != core.JudgmentWithdrawn {
		t.Errorf("status = %q, want withdrawn (terminal)", result.Judgment.Status)
	}
	if result.JudgmentEventID == "" {
		t.Error("judgmentEventId must be set on withdrawal")
	}
}

// TestCLIJudgeWithdrawDifferentProposer: a proposal made by a DIFFERENT agent
// identity cannot be withdrawn from the CLI — PROPOSAL_UNAUTHORIZED (exit 1).
func TestCLIJudgeWithdrawDifferentProposer(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	fromID, toID := seedJudgeObservationsCLI(t, db)
	foreignID := seedForeignJudgmentCLI(t, db, fromID, toID)

	stdout, stderr, code := runCLI(t, "judge", "withdraw", foreignID, "--db", db)
	if code != 1 {
		t.Fatalf("judge withdraw foreign exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("failed withdrawal must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "PROPOSAL_UNAUTHORIZED") {
		t.Fatalf("stderr must carry PROPOSAL_UNAUTHORIZED: %q", stderr)
	}
}

// TestCLIJudgeShow: judge show prints the judgment JSON (read-only); a missing
// judgment fails with JUDGMENT_NOT_FOUND.
func TestCLIJudgeShow(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	fromID, toID := seedJudgeObservationsCLI(t, db)
	proposed := proposeJudgmentCLI(t, db, fromID, toID)

	stdout, stderr, code := runCLI(t, "judge", "show", proposed.JudgmentID, "--db", db)
	if code != 0 {
		t.Fatalf("judge show failed (exit %d): %s", code, stderr)
	}
	var judgment core.AccountingJudgment
	if err := json.Unmarshal([]byte(stdout), &judgment); err != nil {
		t.Fatalf("show output not JSON: %v\n%s", err, stdout)
	}
	if judgment.ID != proposed.JudgmentID || judgment.Status != core.JudgmentProposed {
		t.Errorf("shown judgment = %s/%s, want %s/proposed", judgment.ID, judgment.Status, proposed.JudgmentID)
	}

	stdout, stderr, code = runCLI(t, "judge", "show", "missing-judgment", "--db", db)
	if code != 1 {
		t.Fatalf("judge show missing exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("missing judgment must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "JUDGMENT_NOT_FOUND") {
		t.Fatalf("stderr must carry JUDGMENT_NOT_FOUND: %q", stderr)
	}
}

// cliDoctorDigest is the deterministic logical zero-effect digest of the CLI
// replay tests (doctor counts — never raw SQLite bytes).
func cliDoctorDigest(t *testing.T, db string) string {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store for digest: %v", err)
	}
	defer func() { _ = st.Close() }()
	report, err := st.Doctor(context.Background(), store.DoctorOptions{})
	if err != nil {
		t.Fatalf("doctor digest: %v", err)
	}
	return fmt.Sprintf("%d/%d/%d/%d/%d/%d/%d", report.Observations, report.Transitions, report.PendingApprovals,
		report.PurgeRequests, report.LifecycleEvents, report.PurgeIdempotencyKeys, report.Holds)
}

// TestCLIJudgeReplay (AC-L-3, FR-L.4): judge propose with the SAME --request-id
// submitted twice — the second run prints the FIRST stored judgment id with
// idempotentReplay=true, and the store serves exactly the same single proposal
// (the open-tuple contract makes a duplicate proposal of the same pair
// impossible; the stored id identity proves no second row). The fresh-only
// assertion above is NOT sufficient alone.
func TestCLIJudgeReplay(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	fromID, toID := seedJudgeObservationsCLI(t, db)
	const key = "req-cli-judge-replay-1"

	runPropose := func() core.ProposeJudgmentResult {
		t.Helper()
		stdout, stderr, code := runCLI(t, "judge", "propose", fromID, toID,
			"--relation", "contradicts", "--reason", "diferencia de saldo entre mayor y SIRE",
			"--request-id", key, "--db", db)
		if code != 0 {
			t.Fatalf("judge propose failed (exit %d): %s", code, stderr)
		}
		var result core.ProposeJudgmentResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("judge propose output not JSON: %v\n%s", err, stdout)
		}
		return result
	}

	first := runPropose()
	if first.IdempotentReplay || first.JudgmentID == "" {
		t.Fatalf("first propose = %+v, want a fresh stored judgment id", first)
	}
	afterFirst := cliDoctorDigest(t, db)

	second := runPropose()
	if !second.IdempotentReplay || second.JudgmentID != first.JudgmentID || second.Judgment.ID != first.JudgmentID {
		t.Fatalf("replay propose = %+v, want the stored judgment %s with idempotentReplay", second, first.JudgmentID)
	}
	if second.Judgment.Status != core.JudgmentProposed {
		t.Fatalf("replay status = %q, want proposed (the stored outcome)", second.Judgment.Status)
	}
	afterSecond := cliDoctorDigest(t, db)
	if afterFirst != afterSecond {
		t.Fatalf("judge replay duplicated effects: before %s after %s", afterFirst, afterSecond)
	}

	// The store serves exactly the same single proposal row.
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	j, ok := st.GetJudgment(context.Background(), first.JudgmentID)
	if !ok || j.ID != first.JudgmentID || j.Status != core.JudgmentProposed {
		t.Fatalf("stored judgment = %+v (ok=%v), want the single stored proposal", j, ok)
	}
}

// TestCLIHelpListsJudgeCommands: help documents the adjudication surface.
func TestCLIHelpListsJudgeCommands(t *testing.T) {
	stdout, _, code := runCLI(t, "help")
	if code != 0 {
		t.Fatalf("help exit = %d, want 0", code)
	}
	for _, line := range []string{
		"judge propose", "judge confirm", "judge reject", "judge withdraw", "judge show",
	} {
		if !strings.Contains(stdout, line) {
			t.Fatalf("help output missing %q", line)
		}
	}
}
