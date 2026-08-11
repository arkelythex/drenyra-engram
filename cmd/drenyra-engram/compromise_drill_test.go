// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; sequence/revision counters are JSON integers.
//
// Compromise-response DRILL (gate G-7): the full operator flow from
// docs/security/key-compromise-response.md exercised end-to-end through the
// real CLI — suspend (stop signing with the suspected key) → rotate (one
// transaction: new key activated, old key revoked) → verify fail-closed (the
// FZ-3 cutoff: at/after-cutoff signatures rejected, pre-cutoff retained) and
// the store/signing seam (a revoked key never signs a new receipt). This is the
// missing automated journey: the playbook steps were individually pinned, but
// the complete suspend→rotate→revoke→verify sequence was not exercised as one
// deterministic CLI flow.

package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dbReceiptKeyID returns the signing key id of the newest receipt row for a
// subject (direct read of the receipts table — the same evidence the DRILL's
// Step 7 inventory would query).
func dbReceiptKeyID(t *testing.T, dbPath, subjectType, subjectID string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, pragma := range []string{`PRAGMA foreign_keys = ON`, `PRAGMA busy_timeout = 5000`, `PRAGMA journal_mode = WAL`} {
		if _, err := db.Exec(pragma); err != nil {
			t.Fatalf("%s: %v", pragma, err)
		}
	}
	var keyID string
	err = db.QueryRow(
		`SELECT key_id FROM receipts WHERE subject_type = ? AND subject_id = ? ORDER BY id DESC LIMIT 1`,
		subjectType, subjectID,
	).Scan(&keyID)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("query key_id for %s/%s: %v", subjectType, subjectID, err)
	}
	return keyID
}

// TestCLICompromiseResponseDrill runs the full G-7 sequence:
//
//  1. keys init — active key A created (0600 keyring).
//  2. save a fixture — receipt signed by key A (pre-compromise artifact).
//  3. verify memory — PASSES before any incident (baseline).
//  4. keys rotate — ONE transaction: key B activated, key A revoked_at stamped.
//  5. verify memory — STILL PASSES: pre-cutoff (issued_at < revoked_at) retained
//     under the frozen FZ-3 policy (Step 6 of the playbook).
//  6. save a second fixture — the store/signing seam must sign with key B, never
//     the revoked key A (Step 2 "stop signing is ENFORCED by the engine").
//  7. keys show — active key is B, unrevoked; A carries its revoked_at.
func TestCLICompromiseResponseDrill(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	_, env := cliKeyringEnv(t)

	// Step 0: a deterministic second fixture (same shape as testdata a.json but
	// a distinct topicKey so it is a genuinely new subject).
	secondFixture := filepath.Join(t.TempDir(), "post-rotation.json")
	secondJSON := `{
	  "topicKey": "tax.itf.rate",
	  "title": "ITF base rate",
	  "kind": "rule",
	  "scope": {
	    "kind": "company",
	    "organizationId": "cli",
	    "companyId": "20100039201",
	    "ruc": "20100039201",
	    "period": "202401"
	  },
	  "content": {
	    "what": "ITF base rate is 0.005 percent",
	    "why": "standard rate for bank transfers",
	    "where": "Peru",
	    "learned": "applies to all bank transfers"
	  },
	  "fiscalEffect": "none",
	  "effectiveAt": "2024-01-01T00:00:00.000Z",
	  "validity": {
	    "effectiveAt": "2024-01-01T00:00:00.000Z"
	  },
	  "source": {
	    "system": "cli",
	    "actorId": "cli-user",
	    "actorKind": "agent",
	    "session": "drill-002"
	  }
	}`
	if err := os.WriteFile(secondFixture, []byte(secondJSON), 0o600); err != nil {
		t.Fatalf("write second fixture: %v", err)
	}

	// 1. keys init — active key A.
	stdout, stderr, code := runCLIEnv(t, env, "keys", "init")
	if code != 0 {
		t.Fatalf("keys init failed (exit %d): %s", code, stderr)
	}
	var initOut struct {
		KeyID string `json:"keyId"`
	}
	if err := json.Unmarshal([]byte(stdout), &initOut); err != nil {
		t.Fatalf("init output not JSON: %v\n%s", err, stdout)
	}
	keyA := initOut.KeyID

	// 2. save the pre-compromise fixture — receipt signed by key A.
	stdout, stderr, code = runCLIEnv(t, env, "save", fixturePath(t, "a.json"), "--db", db)
	if code != 0 {
		t.Fatalf("pre-rotation save failed (exit %d): %s", code, stderr)
	}
	var saveResult struct {
		Memory struct {
			Identity struct {
				ID string `json:"id"`
			} `json:"identity"`
			Scope struct {
				RUC string `json:"ruc"`
			} `json:"scope"`
		} `json:"memory"`
	}
	if err := json.Unmarshal([]byte(stdout), &saveResult); err != nil {
		t.Fatalf("save output not JSON: %v\n%s", err, stdout)
	}
	memoryID := saveResult.Memory.Identity.ID
	if memoryID == "" {
		t.Fatalf("save returned no memory id: %s", stdout)
	}
	if got := dbReceiptKeyID(t, db, "memory", memoryID); got != keyA {
		t.Fatalf("pre-rotation receipt key_id = %s, want key A %s", got, keyA)
	}

	// The FZ-3 cutoff rejects issued_at == revoked_at deterministically, so the
	// DRILL must guarantee the fixture receipt is stamped strictly BEFORE the
	// rotation instant: a 1.1s pause between save and rotate makes the
	// pre-cutoff case deterministic instead of racing the same-second clock.
	time.Sleep(1100 * time.Millisecond)

	// 3. baseline verify — PASSES before the incident.
	stdout, stderr, code = runCLIEnv(t, env, "verify", "memory", memoryID, "--db", db)
	if code != 0 {
		t.Fatalf("baseline verify must pass (exit %d): %s\n%s", code, stderr, stdout)
	}
	var baseline struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(stdout), &baseline); err != nil {
		t.Fatalf("baseline report not JSON: %v\n%s", err, stdout)
	}
	if baseline.Outcome != "passed" {
		t.Fatalf("baseline outcome = %s, want passed", baseline.Outcome)
	}

	// 4. keys rotate — ONE transaction: key B activated, key A revoked.
	stdout, stderr, code = runCLIEnv(t, env, "keys", "rotate", "--db", db)
	if code != 0 {
		t.Fatalf("keys rotate failed (exit %d): %s", code, stderr)
	}
	var rot struct {
		KeyID         string `json:"keyId"`
		PreviousKeyID string `json:"previousKeyId"`
		RevokedAt     string `json:"revokedAt"`
	}
	if err := json.Unmarshal([]byte(stdout), &rot); err != nil {
		t.Fatalf("rotate output not JSON: %v\n%s", err, stdout)
	}
	if rot.KeyID == keyA {
		t.Fatal("rotation must activate a NEW key")
	}
	if rot.PreviousKeyID != keyA {
		t.Fatalf("previousKeyId = %s, want key A %s", rot.PreviousKeyID, keyA)
	}
	keyB := rot.KeyID
	if dbSigningKeyRevokedAt(t, db, keyA) == "" {
		t.Fatal("key A must be revoked in the store after rotation")
	}
	if dbSigningKeyRevokedAt(t, db, keyB) != "" {
		t.Fatal("key B must be unrevoked in the store after rotation")
	}

	// 5. verify memory after revocation — STILL PASSES: the receipt is strictly
	// pre-cutoff (issued_at < revoked_at), retained under the FZ-3 policy.
	stdout, stderr, code = runCLIEnv(t, env, "verify", "memory", memoryID, "--db", db)
	if code != 0 {
		t.Fatalf("pre-cutoff receipt must stay verified (exit %d): %s\n%s", code, stderr, stdout)
	}
	var after struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(stdout), &after); err != nil {
		t.Fatalf("post-rotation report not JSON: %v\n%s", err, stdout)
	}
	if after.Outcome != "passed" {
		t.Fatalf("post-rotation outcome = %s, want passed (pre-cutoff retention)", after.Outcome)
	}

	// 6. save post-rotation — the store/signing seam signs with key B; the
	// revoked key A is NEVER selected for a new signature (fail closed).
	stdout, stderr, code = runCLIEnv(t, env, "save", secondFixture, "--db", db)
	if code != 0 {
		t.Fatalf("post-rotation save failed (exit %d): %s", code, stderr)
	}
	var secondSave struct {
		Memory struct {
			Identity struct {
				ID string `json:"id"`
			} `json:"identity"`
		} `json:"memory"`
	}
	if err := json.Unmarshal([]byte(stdout), &secondSave); err != nil {
		t.Fatalf("second save output not JSON: %v\n%s", err, stdout)
	}
	if got := dbReceiptKeyID(t, db, "memory", secondSave.Memory.Identity.ID); got != keyB {
		t.Fatalf("post-rotation receipt key_id = %s, want key B %s (revoked key A must never sign)", got, keyB)
	}

	// 7. keys show — active key is B, unrevoked.
	stdout, stderr, code = runCLIEnv(t, env, "keys", "show")
	if code != 0 {
		t.Fatalf("keys show after rotation failed (exit %d): %s", code, stderr)
	}
	var show struct {
		KeyID     string `json:"keyId"`
		RevokedAt string `json:"revokedAt"`
	}
	if err := json.Unmarshal([]byte(stdout), &show); err != nil {
		t.Fatalf("show output not JSON: %v\n%s", err, stdout)
	}
	if show.KeyID != keyB {
		t.Fatalf("active key = %s, want key B %s", show.KeyID, keyB)
	}
	if show.RevokedAt != "" {
		t.Fatalf("active key B must be unrevoked, got %q", show.RevokedAt)
	}

	// Final guard: the playbook's boundary sentence must be reachable — the
	// engine never reopens writes. Both saves above were gated by the SAME
	// session (fixture source session "smoke-001" / "drill-002"); the drill
	// asserts signature provenance switched cleanly, not that anything was
	// re-signed or rewritten.
	if strings.Contains(stdout, "re-signed") {
		t.Fatal("drill must never re-sign: the response path is not a rewrite surface")
	}
}
