// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the at-rest content
// encryption of the SQLite store (sdd-060-at-rest-encryption, FR-ENC-1/2/3):
// per-tenant derived keys (HKDF-SHA256 over the operator master key) and an
// AES-256-GCM envelope over the observation CONTENT narrative. It is a
// STORAGE-layer concern: the memory model and every frozen contract are
// unchanged, encrypted rows decrypt to byte-identical AccountingMemory, and
// encryption never authorizes anything. No monetary field exists anywhere in
// this file.
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// contentEnvelope is the canonical JSON shape of the encrypted content
// narrative — exactly {what, why, where, learned} in the same field order as
// the plaintext columns, so decryption reconstructs the byte-identical memory.
type contentEnvelope struct {
	What    string `json:"what"`
	Why     string `json:"why"`
	Where   string `json:"where"`
	Learned string `json:"learned"`
}

// contentEncAlgoV1 is the frozen algorithm marker of the at-rest envelope.
const contentEncAlgoV1 = "aes-256-gcm-v1"

// errEncryptionRequired fails closed when an encrypted row is read without a
// configured master key — never redacted, never partial.
var errEncryptionRequired = errors.New("ENCRYPTION_REQUIRED: observation content is encrypted at rest and no DRENYRA_ENCRYPTION_MASTER_KEY is configured")

// deriveTenantKey derives the per-tenant AES-256 key from the operator master
// key (D-ENC-2): HKDF-SHA256(master, salt=organization_id,
// info="drenyra-engram/at-rest/v1"). Keys are SEPARABLE — each tenant's key
// material is independent (beyond the master); discarding a tenant's salt makes
// that tenant's encrypted content unrecoverable (right-to-delete posture).
func deriveTenantKey(master []byte, organizationID string) ([]byte, error) {
	return hkdf.Key(sha256.New, master, []byte(organizationID), "drenyra-engram/at-rest/v1", 32)
}

// encryptContent encrypts the canonical content narrative of one observation
// under the tenant's derived key (D-ENC-3): AES-256-GCM, one fresh random
// 12-byte nonce per write. Returns the nonce + ciphertext + the frozen algo
// marker. The canonical content shape is exactly {what, why, where, learned}
// (same field order as the plaintext columns), so decryption reconstructs the
// byte-identical memory.
func encryptContent(master []byte, organizationID string, content contentEnvelope) (nonce, ciphertext []byte, algo string, err error) {
	key, err := deriveTenantKey(master, organizationID)
	if err != nil {
		return nil, nil, "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, "", fmt.Errorf("encrypt: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, "", fmt.Errorf("encrypt: new gcm: %w", err)
	}
	plaintext, err := json.Marshal(content)
	if err != nil {
		return nil, nil, "", fmt.Errorf("encrypt: marshal content: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, "", fmt.Errorf("encrypt: nonce: %w", err)
	}
	return nonce, gcm.Seal(nil, nonce, plaintext, nil), contentEncAlgoV1, nil
}

// decryptContent decrypts one observation's envelope under the tenant's derived
// key. GCM authentication failure (wrong key, tampered ciphertext, or a
// DIFFERENT tenant's row under this tenant's key) fails closed with
// DECRYPTION_FAILED — a partial decrypt is impossible.
func decryptContent(master []byte, organizationID string, nonce, ciphertext []byte) (contentEnvelope, error) {
	key, err := deriveTenantKey(master, organizationID)
	if err != nil {
		return contentEnvelope{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return contentEnvelope{}, fmt.Errorf("decrypt: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return contentEnvelope{}, fmt.Errorf("decrypt: new gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return contentEnvelope{}, errors.New("DECRYPTION_FAILED: observation content could not be authenticated with the configured master key")
	}
	var content contentEnvelope
	if err := json.Unmarshal(plaintext, &content); err != nil {
		return contentEnvelope{}, fmt.Errorf("DECRYPTION_FAILED: stored content envelope is corrupt: %w", err)
	}
	return content, nil
}

// encryptContentForWrite is the shared write-side seam of at-rest encryption
// (sdd-060-at-rest-encryption, FR-ENC-1): when the store is encryption-enabled
// and the memory is company-scope, it encrypts the CONTENT narrative under the
// tenant's derived key and returns the redacted plaintext columns plus the
// envelope (nonce, ciphertext, algo). Non-company scopes stay plaintext
// (structural). Both Save and ImportObservation route through this seam so a
// synced encrypted source re-encrypts on the target (never lands plaintext).
func (s *SQLiteStore) encryptContentForWrite(memory core.AccountingMemory) (what, why, where, learned string, cipher, nonce []byte, algo string, err error) {
	what, why, where, learned = memory.Content.What, memory.Content.Why, memory.Content.Where, memory.Content.Learned
	if len(s.encMaster) == 0 || memory.Scope.Kind != core.ScopeKindCompany {
		return what, why, where, learned, nil, nil, "", nil
	}
	nonce, ciphertext, a, encErr := encryptContent(s.encMaster, memory.Scope.OrganizationID, contentEnvelope{
		What: memory.Content.What, Why: memory.Content.Why, Where: memory.Content.Where, Learned: memory.Content.Learned,
	})
	if encErr != nil {
		return "", "", "", "", nil, nil, "", encErr
	}
	return "", "", "", "", ciphertext, nonce, a, nil
}

// EncryptionEnabled reports whether
// EncryptionEnabled reports whether the store was opened with an at-rest
// encryption master key (sdd-060-at-rest-encryption, FR-ENC-1). The sync
// adapter uses it for the fail-closed encryption-mismatch guard (FR-ENC-4).
func (s *SQLiteStore) EncryptionEnabled() bool {
	return len(s.encMaster) != 0
}
