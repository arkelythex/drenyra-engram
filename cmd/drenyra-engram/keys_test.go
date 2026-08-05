// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module drives the CLI signing-key
// lifecycle (v0.4.0 Step 3 keys commit): keys init creates the user-owned 0600
// keyring and prints the key id, keys show prints the active key id / public
// key / lifecycle timestamps and NEVER the seed, and keys rotate activates a
// new key while revoking the old one in one DB transaction. Permission checks
// on the private-key file fail closed. The tests point $DRENYRA_ENGRAM_SIGNING_KEY
// at a temp file so they never touch the real user config dir.
package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliKeyringEnv runs the CLI with $DRENYRA_ENGRAM_SIGNING_KEY set to a temp
// keyring path.
func cliKeyringEnv(t *testing.T) (string, []string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "drenyra-engram")
	path := filepath.Join(dir, "signing-keys.json")
	return path, []string{"DRENYRA_ENGRAM_SIGNING_KEY=" + path}
}

// TestCLIKeysInitCreatesDurableKeyring verifies first-use generation through
// the CLI: exit 0, a canonical key id in the JSON output, and the 0600 keyring
// file on disk (dir 0700).
func TestCLIKeysInitCreatesDurableKeyring(t *testing.T) {
	path, env := cliKeyringEnv(t)
	stdout, stderr, code := runCLIEnv(t, env, "keys", "init")
	if code != 0 {
		t.Fatalf("keys init failed (exit %d): %s", code, stderr)
	}
	var out struct {
		KeyID     string `json:"keyId"`
		CreatedAt string `json:"createdAt"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("keys init output not JSON: %v\n%s", err, stdout)
	}
	if !strings.HasPrefix(out.KeyID, "ed25519:") || len(out.KeyID) != len("ed25519:")+64 {
		t.Fatalf("key id %q is not ed25519:<sha256 hex>", out.KeyID)
	}
	if out.CreatedAt == "" {
		t.Fatal("keys init must print the key createdAt lifecycle timestamp")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("keyring file not created: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("keyring permissions %04o, want 0600", perm)
	}

	// Idempotent: a second init prints the SAME key id.
	stdout2, _, code2 := runCLIEnv(t, env, "keys", "init")
	if code2 != 0 {
		t.Fatalf("second keys init failed (exit %d)", code2)
	}
	var out2 struct {
		KeyID string `json:"keyId"`
	}
	if err := json.Unmarshal([]byte(stdout2), &out2); err != nil {
		t.Fatalf("second init output not JSON: %v", err)
	}
	if out2.KeyID != out.KeyID {
		t.Fatalf("second init printed a different key %s, want %s", out2.KeyID, out.KeyID)
	}
}

// TestCLIKeysShowPrintsPublicKeyNeverSeed verifies keys show: active key id,
// hex public key and lifecycle timestamps — and that the private seed NEVER
// appears anywhere in the output.
func TestCLIKeysShowPrintsPublicKeyNeverSeed(t *testing.T) {
	_, env := cliKeyringEnv(t)
	if _, stderr, code := runCLIEnv(t, env, "keys", "init"); code != 0 {
		t.Fatalf("keys init failed: %s", stderr)
	}
	stdout, stderr, code := runCLIEnv(t, env, "keys", "show")
	if code != 0 {
		t.Fatalf("keys show failed (exit %d): %s", code, stderr)
	}
	var out struct {
		KeyID     string `json:"keyId"`
		PublicKey string `json:"publicKey"`
		CreatedAt string `json:"createdAt"`
		RevokedAt string `json:"revokedAt"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("keys show output not JSON: %v\n%s", err, stdout)
	}
	if out.KeyID == "" || len(out.PublicKey) != 64 || out.CreatedAt == "" {
		t.Fatalf("keys show incomplete: %+v", out)
	}
	if out.RevokedAt != "" {
		t.Fatalf("fresh active key must be unrevoked, got %q", out.RevokedAt)
	}
	if strings.Contains(strings.ToLower(stdout), "seed") {
		t.Fatalf("keys show must NEVER print the seed, output contains it: %s", stdout)
	}
}

// TestCLIKeysShowFailsClosedOnOpenPermissions verifies the private-key
// permission gate through the CLI: a 0644 keyring is refused (show AND init —
// a corrupt/too-open keyring is never clobbered).
func TestCLIKeysShowFailsClosedOnOpenPermissions(t *testing.T) {
	path, env := cliKeyringEnv(t)
	if _, stderr, code := runCLIEnv(t, env, "keys", "init"); code != 0 {
		t.Fatalf("keys init failed: %s", stderr)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod keyring: %v", err)
	}
	_, stderr, code := runCLIEnv(t, env, "keys", "show")
	if code != 1 || !strings.Contains(stderr, "permissions") {
		t.Fatalf("keys show must fail closed on 0644 (exit 1, permissions), got exit %d: %s", code, stderr)
	}
	_, stderr, code = runCLIEnv(t, env, "keys", "init")
	if code != 1 || !strings.Contains(stderr, "permissions") {
		t.Fatalf("keys init must not clobber a too-open keyring, got exit %d: %s", code, stderr)
	}
}

// TestCLIKeysRotateActivatesNewKeyAndRevokesOld verifies the full rotation
// flow end-to-end: the keyring activates the new key, the DB registers it and
// revokes the old key, and keys show reflects the new active key.
func TestCLIKeysRotateActivatesNewKeyAndRevokesOld(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	_, env := cliKeyringEnv(t)

	stdout, stderr, code := runCLIEnv(t, env, "keys", "init")
	if code != 0 {
		t.Fatalf("keys init failed: %s", stderr)
	}
	var initOut struct {
		KeyID string `json:"keyId"`
	}
	if err := json.Unmarshal([]byte(stdout), &initOut); err != nil {
		t.Fatalf("init output: %v", err)
	}

	stdout, stderr, code = runCLIEnv(t, env, "keys", "rotate", "--db", db)
	if code != 0 {
		t.Fatalf("keys rotate failed (exit %d): %s", code, stderr)
	}
	var rot struct {
		KeyID         string `json:"keyId"`
		PreviousKeyID string `json:"previousKeyId"`
		CreatedAt     string `json:"createdAt"`
		RevokedAt     string `json:"revokedAt"`
	}
	if err := json.Unmarshal([]byte(stdout), &rot); err != nil {
		t.Fatalf("rotate output not JSON: %v\n%s", err, stdout)
	}
	if rot.KeyID == initOut.KeyID {
		t.Fatal("rotation must activate a NEW key")
	}
	if rot.PreviousKeyID != initOut.KeyID {
		t.Fatalf("previousKeyId %s, want %s", rot.PreviousKeyID, initOut.KeyID)
	}
	if rot.RevokedAt == "" {
		t.Fatal("rotation must carry a revocation timestamp")
	}

	// The DB ledger: the new key is registered and unrevoked, the old key is
	// revoked (the one-tx contract).
	oldRevokedAt := dbSigningKeyRevokedAt(t, db, initOut.KeyID)
	if oldRevokedAt == "" {
		t.Fatal("old key must be revoked in the DB after rotation")
	}
	newRevokedAt := dbSigningKeyRevokedAt(t, db, rot.KeyID)
	if newRevokedAt != "" {
		t.Fatalf("new key must be unrevoked in the DB, got %q", newRevokedAt)
	}

	// keys show now reflects the new active key.
	showOut, _, showCode := runCLIEnv(t, env, "keys", "show")
	if showCode != 0 {
		t.Fatalf("keys show after rotation failed (exit %d)", showCode)
	}
	var show struct {
		KeyID     string `json:"keyId"`
		RevokedAt string `json:"revokedAt"`
	}
	if err := json.Unmarshal([]byte(showOut), &show); err != nil {
		t.Fatalf("show output: %v", err)
	}
	if show.KeyID != rot.KeyID {
		t.Fatalf("active key after rotation = %s, want %s", show.KeyID, rot.KeyID)
	}
	if show.RevokedAt != "" {
		t.Fatalf("new active key must be unrevoked, got %q", show.RevokedAt)
	}
}

// TestCLIKeysRotateFailsWithoutActiveKey verifies the fail-closed hint: rotate
// before any init exits 1 and points at keys init.
func TestCLIKeysRotateFailsWithoutActiveKey(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	_, env := cliKeyringEnv(t)
	_, stderr, code := runCLIEnv(t, env, "keys", "rotate", "--db", db)
	if code != 1 || !strings.Contains(stderr, "keys init") {
		t.Fatalf("rotate without an active key must exit 1 with a keys-init hint, got exit %d: %s", code, stderr)
	}
}

// TestCLIKeysUsage verifies the subcommand usage gate: bare `keys` and unknown
// subcommands are usage errors (exit 2).
func TestCLIKeysUsage(t *testing.T) {
	_, env := cliKeyringEnv(t)
	if _, _, code := runCLIEnv(t, env, "keys"); code != 2 {
		t.Fatalf("bare keys must be a usage error (exit 2), got %d", code)
	}
	if _, stderr, code := runCLIEnv(t, env, "keys", "frobnicate"); code != 2 || !strings.Contains(stderr, "unknown keys subcommand") {
		t.Fatalf("unknown keys subcommand must be a usage error, got exit %d: %s", code, stderr)
	}
}

// dbSigningKeyRevokedAt opens the rotated store DB directly and reads the
// revoked_at ledger for a key id ("" when the row is missing or unrevoked).
func dbSigningKeyRevokedAt(t *testing.T, dbPath, keyID string) string {
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
	var revokedAt sql.NullString
	if err := db.QueryRow(`SELECT revoked_at FROM signing_keys WHERE key_id = ?`, keyID).Scan(&revokedAt); err != nil {
		if err == sql.ErrNoRows {
			return ""
		}
		t.Fatalf("query revoked_at for %s: %v", keyID, err)
	}
	return revokedAt.String
}
