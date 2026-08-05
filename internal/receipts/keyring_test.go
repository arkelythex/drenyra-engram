// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the user-owned signing-key
// keyring (v0.4.0 Step 3 keys commit): durable 0600/0700 creation, fail-closed
// permission and corruption checks, deterministic key ids, stable ordering, and
// convergence of concurrent first use on ONE active key.
package receipts_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/receipts"
)

func keyringPath(t *testing.T) string {
	t.Helper()
	// The keyring lives in its OWN user-only subdirectory (created 0700 by
	// writeKeyring) — the outer temp dir is the test scratch space.
	return filepath.Join(t.TempDir(), "drenyra-engram", "signing-keys.json")
}

// TestEnsureActiveKeyCreatesDurableKeyring verifies first-use generation: the
// file lands with user-only permissions (dir 0700, file 0600), the active key
// id is the canonical "ed25519:" + SHA-256 hex of the raw public key, and a
// reload returns the SAME durable key.
func TestEnsureActiveKeyCreatesDurableKeyring(t *testing.T) {
	path := keyringPath(t)
	kr, err := receipts.EnsureActiveKey(path)
	if err != nil {
		t.Fatalf("ensure active key: %v", err)
	}
	if !strings.HasPrefix(kr.ActiveKeyID, "ed25519:") || len(kr.ActiveKeyID) != len("ed25519:")+64 {
		t.Fatalf("active key id %q is not ed25519:<sha256 hex>", kr.ActiveKeyID)
	}
	seed, err := kr.SeedFor(kr.ActiveKeyID)
	if err != nil {
		t.Fatalf("seed for active key: %v", err)
	}
	if len(seed) != 32 {
		t.Fatalf("seed length %d, want 32", len(seed))
	}
	pub, err := kr.PublicKeyFor(kr.ActiveKeyID)
	if err != nil {
		t.Fatalf("public key for active key: %v", err)
	}
	if core.ReceiptKeyID(pub) != kr.ActiveKeyID {
		t.Fatalf("active key id does not derive from its seed: got %s want %s", kr.ActiveKeyID, core.ReceiptKeyID(pub))
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat keyring: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("keyring permissions %04o, want 0600", perm)
	}
	dirFi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat keyring dir: %v", err)
	}
	if perm := dirFi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("keyring dir permissions %04o, want 0700", perm)
	}

	reloaded, err := receipts.LoadKeyring(path)
	if err != nil {
		t.Fatalf("reload keyring: %v", err)
	}
	if reloaded.ActiveKeyID != kr.ActiveKeyID {
		t.Fatalf("reload active key %s, want %s", reloaded.ActiveKeyID, kr.ActiveKeyID)
	}
}

// TestEnsureActiveKeyIsIdempotent verifies that a second call reuses the
// durable active key instead of generating another one.
func TestEnsureActiveKeyIsIdempotent(t *testing.T) {
	path := keyringPath(t)
	first, err := receipts.EnsureActiveKey(path)
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	second, err := receipts.EnsureActiveKey(path)
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if second.ActiveKeyID != first.ActiveKeyID {
		t.Fatalf("active key changed between calls: %s → %s", first.ActiveKeyID, second.ActiveKeyID)
	}
	seed1, _ := first.SeedFor(first.ActiveKeyID)
	seed2, _ := second.SeedFor(second.ActiveKeyID)
	if string(seed1) != string(seed2) {
		t.Fatal("seed changed between calls")
	}
}

// TestConcurrentFirstUseConvergesOnOneActiveKey verifies the race: N callers
// generate simultaneously, and the atomic-rename protocol converges the FILE on
// exactly ONE active key (never two, never corrupt).
func TestConcurrentFirstUseConvergesOnOneActiveKey(t *testing.T) {
	path := keyringPath(t)
	const callers = 8
	type outcome struct {
		i   int
		kr  *receipts.Keyring
		err error
	}
	ch := make(chan outcome, callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			kr, err := receipts.EnsureActiveKey(path)
			ch <- outcome{i: i, kr: kr, err: err}
		}(i)
	}
	for range callers {
		o := <-ch
		if o.err != nil {
			t.Fatalf("caller %d failed: %v", o.i, o.err)
		}
	}

	// The durable file holds exactly ONE active key and one seed, and it loads
	// cleanly — the convergence contract.
	final, err := receipts.LoadKeyring(path)
	if err != nil {
		t.Fatalf("load converged keyring: %v", err)
	}
	if len(final.KeyIDs()) != 1 {
		t.Fatalf("converged keyring has %d keys, want exactly 1: %v", len(final.KeyIDs()), final.KeyIDs())
	}
	if final.ActiveKeyID != final.KeyIDs()[0] {
		t.Fatalf("active key %s not first in %v", final.ActiveKeyID, final.KeyIDs())
	}
}

// TestLoadKeyringFailsClosedOnOpenPermissions verifies the fail-closed
// permission gate: a private-key file with group/other bits is NEVER loaded.
func TestLoadKeyringFailsClosedOnOpenPermissions(t *testing.T) {
	path := keyringPath(t)
	if _, err := receipts.EnsureActiveKey(path); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod keyring: %v", err)
	}
	if _, err := receipts.LoadKeyring(path); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("LoadKeyring must fail closed on 0644, got %v", err)
	}
	// EnsureActiveKey must NOT clobber a keyring it cannot validate.
	if _, err := receipts.EnsureActiveKey(path); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("EnsureActiveKey must fail closed on an invalid existing keyring, got %v", err)
	}
}

// TestLoadKeyringFailsClosedOnCorruption verifies the seed/key-id integrity
// gate: a tampered seed or a tampered key id is corruption and fails closed.
func TestLoadKeyringFailsClosedOnCorruption(t *testing.T) {
	t.Run("tampered seed", func(t *testing.T) {
		path := keyringPath(t)
		if _, err := receipts.EnsureActiveKey(path); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		var file struct {
			ActiveKeyID string                       `json:"activeKeyId"`
			Keys        map[string]receipts.KeyEntry `json:"keys"`
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read keyring: %v", err)
		}
		if err := json.Unmarshal(data, &file); err != nil {
			t.Fatalf("parse keyring: %v", err)
		}
		// Flip the first seed byte: the derived key id no longer matches.
		entry := file.Keys[file.ActiveKeyID]
		seed, _ := base64.StdEncoding.DecodeString(entry.Seed)
		seed[0] ^= 0x01
		entry.Seed = base64.StdEncoding.EncodeToString(seed)
		file.Keys[file.ActiveKeyID] = entry
		patched, _ := json.Marshal(file)
		if err := os.WriteFile(path, patched, 0o600); err != nil {
			t.Fatalf("patch keyring: %v", err)
		}
		if _, err := receipts.LoadKeyring(path); err == nil || !strings.Contains(err.Error(), "corruption") {
			t.Fatalf("LoadKeyring must fail closed on a tampered seed, got %v", err)
		}
	})
	t.Run("tampered key id", func(t *testing.T) {
		path := keyringPath(t)
		if _, err := receipts.EnsureActiveKey(path); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		var file map[string]any
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read keyring: %v", err)
		}
		if err := json.Unmarshal(data, &file); err != nil {
			t.Fatalf("parse keyring: %v", err)
		}
		file["activeKeyId"] = "ed25519:0000000000000000000000000000000000000000000000000000000000000000"
		patched, _ := json.Marshal(file)
		if err := os.WriteFile(path, patched, 0o600); err != nil {
			t.Fatalf("patch keyring: %v", err)
		}
		if _, err := receipts.LoadKeyring(path); err == nil || !strings.Contains(err.Error(), "corruption") {
			t.Fatalf("LoadKeyring must fail closed on a tampered key id, got %v", err)
		}
	})
	t.Run("short seed", func(t *testing.T) {
		path := keyringPath(t)
		if _, err := receipts.EnsureActiveKey(path); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		var file struct {
			ActiveKeyID string                       `json:"activeKeyId"`
			Keys        map[string]receipts.KeyEntry `json:"keys"`
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read keyring: %v", err)
		}
		if err := json.Unmarshal(data, &file); err != nil {
			t.Fatalf("parse keyring: %v", err)
		}
		entry := file.Keys[file.ActiveKeyID]
		entry.Seed = base64.StdEncoding.EncodeToString([]byte("short"))
		file.Keys[file.ActiveKeyID] = entry
		patched, _ := json.Marshal(file)
		if err := os.WriteFile(path, patched, 0o600); err != nil {
			t.Fatalf("patch keyring: %v", err)
		}
		if _, err := receipts.LoadKeyring(path); err == nil || !strings.Contains(err.Error(), "seed length") {
			t.Fatalf("LoadKeyring must fail closed on a short seed, got %v", err)
		}
	})
}

// TestKeyringKeyIDsAndSeedFor verifies the deterministic key ordering and the
// seed lookup contract (active first, retained seeds after, unknown fails).
func TestKeyringKeyIDsAndSeedFor(t *testing.T) {
	path := keyringPath(t)
	kr, err := receipts.EnsureActiveKey(path)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	ids := kr.KeyIDs()
	if len(ids) != 1 || ids[0] != kr.ActiveKeyID {
		t.Fatalf("KeyIDs = %v, want [%s]", ids, kr.ActiveKeyID)
	}
	seed, err := kr.SeedFor(kr.ActiveKeyID)
	if err != nil || len(seed) != 32 {
		t.Fatalf("SeedFor(active) = (%d bytes, %v)", len(seed), err)
	}
	if _, err := kr.SeedFor("ed25519:nope"); err == nil {
		t.Fatal("SeedFor(unknown) must fail")
	}
	if kr.CreatedAt(kr.ActiveKeyID) == "" {
		t.Fatal("active key must carry a createdAt lifecycle timestamp")
	}
	if kr.RevokedAt(kr.ActiveKeyID) != "" {
		t.Fatal("fresh active key must be unrevoked")
	}
}
