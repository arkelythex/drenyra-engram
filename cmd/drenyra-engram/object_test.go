// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the v0.7.x WORM evidence
// object CLI surfaces through the built binary and proves the advertised
// --objects root flag is REAL (docs/architecture/evidence-object-v0.7.md
// §Hardening): object store/get, doctor and verify object all honor an explicit
// custom objects root, and the parity negative holds — the SAME commands
// without --objects resolve the default root and fail closed (the stored bytes
// are not under it), never silently reading a different root.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliStoreObjectAtRoot stores one artifact through the built binary with an
// explicit --objects root and returns the content address (objectId).
func cliStoreObjectAtRoot(t *testing.T, env []string, db, root string, ruc string, artifactPath string) string {
	t.Helper()
	stdout, stderr, code := runCLIEnv(t, env,
		"object", "store", artifactPath,
		"--ruc", ruc, "--period", "202401",
		"--objects", root, "--db", db)
	if code != 0 {
		t.Fatalf("object store failed (exit %d): %s", code, stderr)
	}
	var result struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
		Created bool `json:"created"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("object store output not JSON: %v\n%s", err, stdout)
	}
	if result.Object.ObjectID == "" || !result.Created {
		t.Fatalf("object store result = %+v, want a created object with an objectId", result)
	}
	return result.Object.ObjectID
}

// TestCLIObjectCustomRootParity proves --objects is honored end-to-end: a store
// with the custom root round-trips through object get, doctor reports the
// custom root, verify object passes WITH the flag and fails closed WITHOUT it
// (the default root does not hold the bytes — the parity negative). The signer
// keyring is pinned to a temp path and ambient DRENYRA_ENGRAM_OBJECTS is
// stripped, so the default root is deterministic (<dir-of-db>/objects).
func TestCLIObjectCustomRootParity(t *testing.T) {
	env := verifyCLIEnv(t)
	db := filepath.Join(t.TempDir(), "engram.db")
	root := filepath.Join(t.TempDir(), "custom-objects")
	artifact := filepath.Join(t.TempDir(), "factura-2026-01.xml")
	if err := os.WriteFile(artifact, []byte("factura-bytes-2026-01"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	objectID := cliStoreObjectAtRoot(t, env, db, root, cliRucA, artifact)

	// object get with the custom root: metadata on stderr, exact bytes on stdout.
	stdout, stderr, code := runCLIEnv(t, env,
		"object", "get", objectID,
		"--ruc", cliRucA, "--period", "202401",
		"--objects", root, "--db", db)
	if code != 0 {
		t.Fatalf("object get failed (exit %d): %s", code, stderr)
	}
	if !strings.Contains(stderr, objectID) {
		t.Fatalf("object get metadata on stderr must carry the object id: %q", stderr)
	}
	if stdout != "factura-bytes-2026-01" {
		t.Fatalf("object get bytes = %q, want the exact artifact bytes", stdout)
	}

	// doctor with the custom root: reports the root and one object, no findings.
	stdout, stderr, code = runCLIEnv(t, env, "doctor", "--objects", root, "--db", db)
	if code != 0 {
		t.Fatalf("doctor with custom root failed (exit %d): %s", code, stderr)
	}
	if !strings.Contains(stdout, `"objectsRoot": "`+root+`"`) {
		t.Fatalf("doctor must report the custom objectsRoot: %s", stdout)
	}
	if !strings.Contains(stdout, `"evidenceObjects": 1`) {
		t.Fatalf("doctor must count the stored object: %s", stdout)
	}

	// verify object with the custom root: passed (exit 0).
	stdout, stderr, code = runCLIEnv(t, env, "verify", "object", objectID, "--objects", root, "--db", db)
	if code != 0 {
		t.Fatalf("verify object with custom root exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"outcome": "passed"`) {
		t.Fatalf("verify object with custom root must pass: %s", stdout)
	}

	// Parity negative: the same commands WITHOUT --objects resolve the default
	// root (<dir-of-db>/objects), which does NOT hold the bytes — verify object
	// reports a failed WORM byte-integrity layer (exit 1, evidence not error)
	// and doctor FAILS CLOSED (row bytes missing under the default root).
	_, _, code = runCLIEnv(t, env, "verify", "object", objectID, "--db", db)
	if code != 1 {
		t.Fatalf("verify object without --objects exit = %d, want 1 (WORM layer failure, evidence)", code)
	}
	_, stderr, code = runCLIEnv(t, env, "doctor", "--db", db)
	if code != 1 {
		t.Fatalf("doctor without --objects exit = %d, want 1 (fail closed)", code)
	}
	if !strings.Contains(stderr, "OBJECT_BYTES_MISSING") {
		t.Fatalf("doctor without --objects must fail closed naming the missing bytes: %q", stderr)
	}
}
