// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the signer orchestration
// (v0.4.0 Step 3 keys commit): the ACTIVE keyring key signs, the receipt is
// persisted with the derived chain head and raw signature, a revoked key never
// signs, a stored public key that differs from the derived bytes is corruption
// and fails closed, and rotation durably activates a new key while revoking the
// old IN ONE DB transaction (a failed transaction restores the keyring).
package receipts_test

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/receipts"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// ──────────────────────────────────────────────
// Fake store (the go-testing skill's boundary mock)
// ──────────────────────────────────────────────

// fakeStore implements receipts.ReceiptStore in memory: signer tests control
// the stored public-key rows, the chain head and the failure points.
type fakeStore struct {
	records      map[string]store.SigningKeyRecord
	chainHead    map[string]string
	rows         []store.ReceiptRow
	registered   []string
	revoked      []string
	failRegister bool
	failRevoke   bool
	failBegin    bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		records:   map[string]store.SigningKeyRecord{},
		chainHead: map[string]string{},
	}
}

func (f *fakeStore) BeginReceiptTx(ctx context.Context) (*sql.Tx, error) {
	if f.failBegin {
		return nil, errors.New("fake: begin failed")
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	_ = db.Close()
	return tx, nil
}

func (f *fakeStore) RegisterPublicKey(ctx context.Context, q receipts.Queryer, keyID, algorithm, publicKey, createdAt string) error {
	if f.failRegister {
		return errors.New("fake: register failed")
	}
	f.registered = append(f.registered, keyID)
	f.records[keyID] = store.SigningKeyRecord{Algorithm: algorithm, PublicKey: publicKey, CreatedAt: createdAt, Found: true}
	return nil
}

func (f *fakeStore) RevokePublicKey(ctx context.Context, q receipts.Queryer, keyID, revokedAt string) error {
	if f.failRevoke {
		return errors.New("fake: revoke failed")
	}
	f.revoked = append(f.revoked, keyID)
	return nil
}

func (f *fakeStore) LookupSigningKey(ctx context.Context, q receipts.Queryer, keyID string) (store.SigningKeyRecord, error) {
	rec, ok := f.records[keyID]
	if !ok {
		return store.SigningKeyRecord{}, nil
	}
	rec.Found = true
	return rec, nil
}

func (f *fakeStore) LatestReceiptChainHead(ctx context.Context, q receipts.Queryer, subjectType core.SubjectType, subjectID string) (string, error) {
	return f.chainHead[subjectID], nil
}

func (f *fakeStore) InsertReceipt(ctx context.Context, q receipts.Queryer, row store.ReceiptRow) error {
	f.rows = append(f.rows, row)
	return nil
}

// ──────────────────────────────────────────────
// Fixtures
// ──────────────────────────────────────────────

// signPayload is a minimal legal memory_recorded payload (a covered act with a
// memory subject, kernel policy, empty inapplicable fields).
func signPayload(issuedAt string) core.ReceiptPayload {
	return core.ReceiptPayload{
		Version:        core.ReceiptPayloadVersion,
		SubjectType:    core.SubjectTypeMemory,
		SubjectID:      "memory-1",
		Action:         core.ReceiptActionMemoryRecorded,
		TenantID:       "tenant-1",
		CompanyID:      "acme",
		FiscalPeriodID: "202601",
		PrincipalID:    "cli",
		PolicyVersion:  "kernel/v0.4.0",
		IssuedAt:       issuedAt,
	}
}

func signerPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "drenyra-engram", "signing-keys.json")
}

// TestSignMintsAndPersistsReceipt is the happy path: the ACTIVE key signs, the
// receipt verifies with core.VerifyReceipt, the store registers the public key
// and receives the receipt row with the derived chain head (genesis), the RAW
// signature bytes, the canonical payload JSON and the exactly-one-typed-FK.
func TestSignMintsAndPersistsReceipt(t *testing.T) {
	f := newFakeStore()
	path := signerPath(t)
	s := receipts.NewSigner(f, path)
	payload := signPayload("2026-08-05T13:00:00Z")

	r, err := s.Sign(context.Background(), nil, payload, payload.IssuedAt)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// The receipt is the canonical signed artifact of the payload.
	kr, err := receipts.LoadKeyring(path)
	if err != nil {
		t.Fatalf("load keyring: %v", err)
	}
	pub, err := kr.PublicKeyFor(r.KeyID)
	if err != nil {
		t.Fatalf("public key for %s: %v", r.KeyID, err)
	}
	if err := core.VerifyReceipt(r, payload, pub); err != nil {
		t.Fatalf("signed receipt must verify: %v", err)
	}
	if r.PreviousReceiptHash != "" {
		t.Fatalf("genesis receipt must chain on empty, got %q", r.PreviousReceiptHash)
	}
	if r.KeyID != kr.ActiveKeyID {
		t.Fatalf("receipt key %s, want active key %s", r.KeyID, kr.ActiveKeyID)
	}
	if r.IssuedAt != payload.IssuedAt || r.PayloadHash != core.ReceiptPayloadHash(payload) {
		t.Fatalf("envelope fields drift from the payload: issuedAt=%s payloadHash=%s", r.IssuedAt, r.PayloadHash)
	}

	// The public key was registered and the receipt row persisted with the
	// exact derived bytes.
	if len(f.registered) != 1 || f.registered[0] != r.KeyID {
		t.Fatalf("registered = %v, want [%s]", f.registered, r.KeyID)
	}
	rec := f.records[r.KeyID]
	if rec.PublicKey != base64.StdEncoding.EncodeToString(pub) {
		t.Fatalf("stored public key %q, want %q", rec.PublicKey, base64.StdEncoding.EncodeToString(pub))
	}
	if len(f.rows) != 1 {
		t.Fatalf("inserted %d rows, want 1", len(f.rows))
	}
	row := f.rows[0]
	if row.ReceiptHash != core.ReceiptHash(r) {
		t.Fatalf("row receipt_hash %q, want derived %q", row.ReceiptHash, core.ReceiptHash(r))
	}
	if row.PayloadJSON != string(core.CanonicalReceiptPayload(payload)) {
		t.Fatalf("row payload_json differs from canonical payload bytes")
	}
	rawSig, err := base64.StdEncoding.DecodeString(r.Signature)
	if err != nil {
		t.Fatalf("receipt signature is not padded base64: %v", err)
	}
	if string(row.Signature) != string(rawSig) {
		t.Fatal("row signature is not the RAW signature bytes")
	}
	if row.MemoryID != payload.SubjectID || row.JudgmentID != "" {
		t.Fatalf("typed FK = (memory %q, judgment %q), want exactly memory %q", row.MemoryID, row.JudgmentID, payload.SubjectID)
	}
	if row.KeyID != r.KeyID || row.Algorithm != core.ReceiptAlgorithm {
		t.Fatalf("row key/algorithm = (%s, %s)", row.KeyID, row.Algorithm)
	}
}

// TestSignChainsOnTheLatestReceipt verifies the chain-head contract: the store
// returns the prior subject receipt and Sign copies it into
// previousReceiptHash before signing.
func TestSignChainsOnTheLatestReceipt(t *testing.T) {
	f := newFakeStore()
	f.chainHead["memory-1"] = "prior-receipt-hash"
	s := receipts.NewSigner(f, signerPath(t))
	payload := signPayload("2026-08-05T13:00:00Z")

	r, err := s.Sign(context.Background(), nil, payload, payload.IssuedAt)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if r.PreviousReceiptHash != "prior-receipt-hash" {
		t.Fatalf("previousReceiptHash = %q, want the store's chain head", r.PreviousReceiptHash)
	}
}

// TestSignRefusesRevokedKey verifies the fail-closed revocation gate: a key the
// store marks revoked is NEVER selected, and NOTHING is registered or inserted.
func TestSignRefusesRevokedKey(t *testing.T) {
	f := newFakeStore()
	path := signerPath(t)
	kr, err := receipts.EnsureActiveKey(path)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	pub, _ := kr.PublicKeyFor(kr.ActiveKeyID)
	f.records[kr.ActiveKeyID] = store.SigningKeyRecord{
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		RevokedAt: "2026-08-06T09:00:00Z",
		Found:     true,
	}
	s := receipts.NewSigner(f, path)
	payload := signPayload("2026-08-05T13:00:00Z")

	if _, err := s.Sign(context.Background(), nil, payload, payload.IssuedAt); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("signing with a revoked key must fail closed, got %v", err)
	}
	if len(f.rows) != 0 || len(f.registered) != 0 {
		t.Fatalf("revoked sign must not register or insert: rows=%d registered=%d", len(f.rows), len(f.registered))
	}
}

// TestSignFailsClosedOnStoredPublicKeyMismatch verifies the corruption gate: a
// stored public key whose bytes differ from the seed's derived key aborts the
// signature (fail closed).
func TestSignFailsClosedOnStoredPublicKeyMismatch(t *testing.T) {
	f := newFakeStore()
	path := signerPath(t)
	kr, err := receipts.EnsureActiveKey(path)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	otherSeed := make([]byte, 32)
	otherPub := ed25519.NewKeyFromSeed(otherSeed).Public().(ed25519.PublicKey)
	f.records[kr.ActiveKeyID] = store.SigningKeyRecord{
		PublicKey: base64.StdEncoding.EncodeToString(otherPub),
		Found:     true,
	}
	s := receipts.NewSigner(f, path)
	payload := signPayload("2026-08-05T13:00:00Z")

	if _, err := s.Sign(context.Background(), nil, payload, payload.IssuedAt); err == nil || !strings.Contains(err.Error(), "corruption") {
		t.Fatalf("public-bytes mismatch must fail closed as corruption, got %v", err)
	}
	if len(f.rows) != 0 {
		t.Fatalf("corrupt sign must not insert: %d rows", len(f.rows))
	}
}

// TestSignRefusesKeyringRevokedActiveKey verifies defense in depth: an active
// key the KEYRING itself marks revoked is never selected.
func TestSignRefusesKeyringRevokedActiveKey(t *testing.T) {
	f := newFakeStore()
	path := signerPath(t)
	if _, err := receipts.EnsureActiveKey(path); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Corrupt the keyring file by hand: mark the ACTIVE key revoked.
	patched := strings.Replace(mustRead(t, path), `"revokedAt":""`, `"revokedAt":"2026-08-06T09:00:00Z"`, 1)
	if err := osWriteFile(path, []byte(patched)); err != nil {
		t.Fatalf("patch keyring: %v", err)
	}
	s := receipts.NewSigner(f, path)
	payload := signPayload("2026-08-05T13:00:00Z")

	if _, err := s.Sign(context.Background(), nil, payload, payload.IssuedAt); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("keyring-revoked active key must fail closed, got %v", err)
	}
	if len(f.rows) != 0 {
		t.Fatalf("revoked sign must not insert: %d rows", len(f.rows))
	}
}

// TestSignRejectsEmissionTimestampMismatch verifies the design invariant that
// payload issuedAt equals the emission timestamp.
func TestSignRejectsEmissionTimestampMismatch(t *testing.T) {
	f := newFakeStore()
	s := receipts.NewSigner(f, signerPath(t))
	payload := signPayload("2026-08-05T13:00:00Z")

	if _, err := s.Sign(context.Background(), nil, payload, "2026-08-06T00:00:00Z"); err == nil || !strings.Contains(err.Error(), "issuedAt") {
		t.Fatalf("issuedAt mismatch must fail, got %v", err)
	}
}

// TestSignUsesSameActiveKeyAcrossCalls verifies keyring idempotency through the
// signer: two signatures use the SAME active key and chain on each other.
func TestSignUsesSameActiveKeyAcrossCalls(t *testing.T) {
	f := newFakeStore()
	path := signerPath(t)
	s := receipts.NewSigner(f, path)
	payload := signPayload("2026-08-05T13:00:00Z")

	first, err := s.Sign(context.Background(), nil, payload, payload.IssuedAt)
	if err != nil {
		t.Fatalf("first sign: %v", err)
	}
	f.chainHead[payload.SubjectID] = core.ReceiptHash(first)
	second, err := s.Sign(context.Background(), nil, payload, payload.IssuedAt)
	if err != nil {
		t.Fatalf("second sign: %v", err)
	}
	if first.KeyID != second.KeyID {
		t.Fatalf("active key changed between signs: %s → %s", first.KeyID, second.KeyID)
	}
	if second.PreviousReceiptHash != core.ReceiptHash(first) {
		t.Fatalf("second receipt chains on %q, want %q", second.PreviousReceiptHash, core.ReceiptHash(first))
	}
	// The signer re-registers on every sign (the STORE dedupes via INSERT OR
	// IGNORE); the ACTIVE key must never change between calls.
	for _, id := range f.registered {
		if id != first.KeyID {
			t.Fatalf("registered %s, want the stable active key %s", id, first.KeyID)
		}
	}
}

// ──────────────────────────────────────────────
// Rotation
// ──────────────────────────────────────────────

// TestRotateActivatesNewKeyAndRevokesOld verifies the full rotation contract:
// the keyring durably activates the new key, the DB transaction registers it
// and revokes the old key, and subsequent signatures use the NEW key while the
// old key is never selected again.
func TestRotateActivatesNewKeyAndRevokesOld(t *testing.T) {
	f := newFakeStore()
	path := signerPath(t)
	s := receipts.NewSigner(f, path)
	payload := signPayload("2026-08-05T13:00:00Z")

	before, err := s.Sign(context.Background(), nil, payload, payload.IssuedAt)
	if err != nil {
		t.Fatalf("sign before rotation: %v", err)
	}

	res, err := receipts.Rotate(context.Background(), f, path)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if res.OldKeyID != before.KeyID {
		t.Fatalf("rotation old key %s, want %s", res.OldKeyID, before.KeyID)
	}
	if res.NewKeyID == res.OldKeyID {
		t.Fatal("rotation must activate a NEW key")
	}

	// The keyring file: new active key, old key revoked, old seed retained.
	kr, err := receipts.LoadKeyring(path)
	if err != nil {
		t.Fatalf("reload keyring: %v", err)
	}
	if kr.ActiveKeyID != res.NewKeyID {
		t.Fatalf("active key after rotation = %s, want %s", kr.ActiveKeyID, res.NewKeyID)
	}
	if kr.RevokedAt(res.OldKeyID) != res.RevokedAt {
		t.Fatalf("old key revokedAt = %q, want %q", kr.RevokedAt(res.OldKeyID), res.RevokedAt)
	}
	if _, err := kr.SeedFor(res.OldKeyID); err != nil {
		t.Fatalf("old seed must be retained for recovery: %v", err)
	}

	// The DB transaction: new key registered, old key revoked.
	if len(f.registered) == 0 || f.registered[len(f.registered)-1] != res.NewKeyID {
		t.Fatalf("rotation did not register the new key: %v", f.registered)
	}
	if len(f.revoked) != 1 || f.revoked[0] != res.OldKeyID {
		t.Fatalf("rotation revoked %v, want [%s]", f.revoked, res.OldKeyID)
	}

	// New signatures use the NEW key; the old key is never selected.
	after, err := s.Sign(context.Background(), nil, payload, payload.IssuedAt)
	if err != nil {
		t.Fatalf("sign after rotation: %v", err)
	}
	if after.KeyID != res.NewKeyID {
		t.Fatalf("post-rotation signature uses %s, want %s", after.KeyID, res.NewKeyID)
	}
	// The old receipt still verifies with the old key (revocation never
	// invalidates receipts issued before it).
	oldPub, _ := kr.PublicKeyFor(res.OldKeyID)
	if err := core.VerifyReceipt(before, payload, oldPub); err != nil {
		t.Fatalf("pre-rotation receipt must still verify: %v", err)
	}
}

// TestRotateRestoresKeyringOnTransactionFailure verifies the fail-closed
// restore: when the DB transaction cannot revoke the old key, the keyring file
// is restored to the pre-rotation state (old key still active).
func TestRotateRestoresKeyringOnTransactionFailure(t *testing.T) {
	f := newFakeStore()
	path := signerPath(t)
	kr, err := receipts.EnsureActiveKey(path)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	oldKey := kr.ActiveKeyID

	f.failRevoke = true
	if _, err := receipts.Rotate(context.Background(), f, path); err == nil || !strings.Contains(err.Error(), "revoke") {
		t.Fatalf("rotation must fail when revoke fails, got %v", err)
	}

	restored, err := receipts.LoadKeyring(path)
	if err != nil {
		t.Fatalf("reload restored keyring: %v", err)
	}
	if restored.ActiveKeyID != oldKey {
		t.Fatalf("failed rotation must restore the old active key, got %s want %s", restored.ActiveKeyID, oldKey)
	}
	if restored.RevokedAt(oldKey) != "" {
		t.Fatal("failed rotation must leave the old key unrevoked")
	}
}

// TestRotateRequiresAnActiveKey verifies that rotation without an initialized
// keyring fails closed with a clear message.
func TestRotateRequiresAnActiveKey(t *testing.T) {
	f := newFakeStore()
	path := filepath.Join(t.TempDir(), "drenyra-engram", "signing-keys.json")
	if _, err := receipts.Rotate(context.Background(), f, path); err == nil || !strings.Contains(err.Error(), "keys init") {
		t.Fatalf("rotation without an active key must fail with a keys-init hint, got %v", err)
	}
}

// mustRead reads a small file, failing the test on error.
func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
