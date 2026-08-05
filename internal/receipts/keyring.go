// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This package owns the SIGNING side of the
// v0.4.0 Step 3 action-receipt protocol (docs/architecture/ed25519-receipts-
// step3.md): the user-owned keyring file holding the private Ed25519 SEEDS (the
// SQLite store keeps public keys only) and the Signer that turns a covered act
// into an immutable signed receipt inside the caller's transaction. The private
// half of the protocol NEVER touches the store: seeds live only in the 0600
// keyring file; public keys, revocation and receipts live in the store.
package receipts

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// keyringEnvVar overrides the default keyring location (tests/CI), mirroring
// the session-file env override.
const keyringEnvVar = "DRENYRA_ENGRAM_SIGNING_KEY"

// DefaultKeyringPath is the keyring location: $DRENYRA_ENGRAM_SIGNING_KEY when
// set, otherwise the platform config dir (os.UserConfigDir → ~/.config on
// Linux) under drenyra-engram/signing-keys.json — the SAME user-only directory
// the CLI session file uses.
func DefaultKeyringPath() string {
	if path := os.Getenv(keyringEnvVar); path != "" {
		return path
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "drenyra-engram", "signing-keys.json")
	}
	return filepath.Join(".", ".drenyra-engram", "signing-keys.json")
}

// KeyEntry is one signing key inside the keyring: the private Ed25519 SEED
// (padded base64 on disk — 32 bytes decoded) plus the lifecycle timestamps the
// CLI shows. Seeds are NEVER written anywhere else.
type KeyEntry struct {
	Seed      string `json:"seed"`
	CreatedAt string `json:"createdAt"`
	RevokedAt string `json:"revokedAt"`
}

// keyringFile is the on-disk shape: the ACTIVE key id plus every seed ever
// generated for this keyring (old seeds may be kept for recovery but are never
// selected after rotation).
type keyringFile struct {
	ActiveKeyID string              `json:"activeKeyId"`
	Keys        map[string]KeyEntry `json:"keys"`
}

// Keyring is a loaded, validated keyring. An active key whose id does not
// derive from its own seed, or a seed that is not exactly 32 bytes, is
// corruption and fails closed at load time.
type Keyring struct {
	ActiveKeyID string
	keys        map[string]KeyEntry
	order       []string // deterministic key ids (sorted)
}

// nowISO is the keyring timestamp format: RFC3339 UTC (the store's own
// timestamp grammar, contracts/provenance.md rule 3).
func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// newSeed returns a fresh 32-byte Ed25519 seed from crypto/rand.
func newSeed() ([]byte, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("generate signing seed: %w", err)
	}
	return seed, nil
}

// seedBytes decodes a padded-base64 seed and fails closed unless it is exactly
// 32 bytes.
func seedBytes(seedB64 string) ([]byte, error) {
	seed, err := base64.StdEncoding.DecodeString(seedB64)
	if err != nil {
		return nil, fmt.Errorf("seed is not valid padded base64: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("seed length %d — expected %d bytes", len(seed), ed25519.SeedSize)
	}
	return seed, nil
}

// validateKeyring performs the fail-closed checks on a parsed keyring: the
// active key must exist, and EVERY key id must be the canonical id of its own
// seed (a mismatched seed or key id is corruption).
func validateKeyring(kf keyringFile) (*Keyring, error) {
	if kf.ActiveKeyID == "" {
		return nil, errors.New("keyring carries no activeKeyId — corruption, fail closed")
	}
	if len(kf.Keys) == 0 {
		return nil, errors.New("keyring carries no seeds — corruption, fail closed")
	}
	if _, ok := kf.Keys[kf.ActiveKeyID]; !ok {
		return nil, fmt.Errorf("keyring activeKeyId %q has no seed — corruption, fail closed", kf.ActiveKeyID)
	}
	order := make([]string, 0, len(kf.Keys))
	for keyID, entry := range kf.Keys {
		seed, err := seedBytes(entry.Seed)
		if err != nil {
			return nil, fmt.Errorf("keyring key %s: %w", keyID, err)
		}
		derived := core.ReceiptKeyID(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
		if derived != keyID {
			return nil, fmt.Errorf("keyring key %s: seed derives key id %s — corruption, fail closed", keyID, derived)
		}
		order = append(order, keyID)
	}
	sort.Strings(order)
	return &Keyring{ActiveKeyID: kf.ActiveKeyID, keys: kf.Keys, order: order}, nil
}

// LoadKeyring loads and validates the keyring at path. It FAILS CLOSED when
// the file is missing, has group/other permissions (a private-key file must be
// user-only 0600), is not valid JSON, or any seed/key-id check fails. It never
// creates or repairs the file.
func LoadKeyring(path string) (*Keyring, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("keyring %s: permissions %04o are too open — expected user-only 0600", path, fi.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read keyring %s: %w", path, err)
	}
	var kf keyringFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return nil, fmt.Errorf("parse keyring %s: %w", path, err)
	}
	kr, err := validateKeyring(kf)
	if err != nil {
		return nil, fmt.Errorf("keyring %s: %w", path, err)
	}
	return kr, nil
}

// writeKeyring durably persists the keyring with the session-file controls:
// user-only directory (0700), SAME-directory exclusive temporary creation
// (0600), fsync, atomic rename, and the private-key file never lands with
// group/other permissions. On any failure the temporary file is removed.
func writeKeyring(path string, kr *Keyring) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create keyring dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".signing-keys-*.tmp")
	if err != nil {
		return fmt.Errorf("create keyring temp: %w", err)
	}
	tmpName := tmp.Name()
	remove := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	data, err := json.Marshal(keyringFile{
		ActiveKeyID: kr.ActiveKeyID,
		Keys:        kr.keys,
	})
	if err != nil {
		remove()
		return fmt.Errorf("encode keyring: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		remove()
		return fmt.Errorf("keyring temp permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		remove()
		return fmt.Errorf("write keyring temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		remove()
		return fmt.Errorf("sync keyring temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close keyring temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename keyring into place: %w", err)
	}
	return nil
}

// EnsureActiveKey returns the keyring, generating and durably activating a new
// active key on FIRST use (crypto/rand seed, 0600 file). Concurrent first use
// converges on ONE active key: the atomic rename means the final file always
// holds exactly one active key, and the caller re-reads the file after writing,
// so the winner of the race (whoever renamed last) is what every caller sees.
// A corrupt or unreadable keyring NEVER gets clobbered — it fails closed.
func EnsureActiveKey(path string) (*Keyring, error) {
	kr, err := LoadKeyring(path)
	if err == nil {
		return kr, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	seed, err := newSeed()
	if err != nil {
		return nil, err
	}
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	keyID := core.ReceiptKeyID(pub)
	createdAt := nowISO()
	kr = &Keyring{
		ActiveKeyID: keyID,
		keys: map[string]KeyEntry{
			keyID: {Seed: base64.StdEncoding.EncodeToString(seed), CreatedAt: createdAt},
		},
		order: []string{keyID},
	}
	if err := writeKeyring(path, kr); err != nil {
		return nil, err
	}
	// Converge on the durable winner: re-read and return the file's active key.
	reloaded, err := LoadKeyring(path)
	if err != nil {
		return nil, fmt.Errorf("keyring written but unreadable: %w", err)
	}
	return reloaded, nil
}

// KeyIDs returns every key id in the keyring in deterministic (sorted) order —
// active first, then the retained recovery seeds.
func (k *Keyring) KeyIDs() []string {
	out := make([]string, 0, len(k.order))
	for _, id := range k.order {
		if id == k.ActiveKeyID {
			out = append(out, id)
		}
	}
	for _, id := range k.order {
		if id != k.ActiveKeyID {
			out = append(out, id)
		}
	}
	return out
}

// SeedFor returns the raw 32-byte seed of keyID (never serialized out of this
// package beyond the keyring file).
func (k *Keyring) SeedFor(keyID string) ([]byte, error) {
	entry, ok := k.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("keyring has no seed for %s", keyID)
	}
	return seedBytes(entry.Seed)
}

// PublicKeyFor derives the raw 32-byte Ed25519 public key of keyID from its
// seed (the key id is the SHA-256 hex of exactly these bytes).
func (k *Keyring) PublicKeyFor(keyID string) ([]byte, error) {
	seed, err := k.SeedFor(keyID)
	if err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey), nil
}

// CreatedAt returns the lifecycle timestamp of keyID ("" for an unknown key).
func (k *Keyring) CreatedAt(keyID string) string {
	return k.keys[keyID].CreatedAt
}

// RevokedAt returns the revocation timestamp of keyID ("" when unrevoked).
func (k *Keyring) RevokedAt(keyID string) string {
	return k.keys[keyID].RevokedAt
}
