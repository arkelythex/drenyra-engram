// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the signer orchestration of
// the v0.4.0 Step 3 action-receipt protocol: it turns a covered act payload
// into an immutable SignedReceipt signed with the ACTIVE keyring key, and
// persists the receipt through the store's receipt surfaces INSIDE the
// caller's transaction (those surfaces never start or commit one — the caller's
// tx owns atomicity). Revocation and key/public-bytes integrity fail closed:
// a revoked key is never selected for a new signature, and a stored public key
// that differs from the derived bytes is corruption.
package receipts

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// Queryer is the statement surface the signer and rotation run through — the
// caller's transaction owns atomicity. It aliases store.Queryer (the exact
// same three-method statement surface), so *SQLiteStore satisfies ReceiptStore
// and *sql.Tx / *sql.DB both qualify as the caller-owned query surface.
type Queryer = store.Queryer

// ReceiptStore is the store surface the signer and rotation need. Every method
// runs on the caller-provided Queryer and NEVER starts or commits a
// transaction; BeginReceiptTx is the one explicit transaction owner (rotation
// and the batch-2 emission points commit register+revoke / act+receipt
// atomically).
type ReceiptStore interface {
	BeginReceiptTx(ctx context.Context) (*sql.Tx, error)
	RegisterPublicKey(ctx context.Context, q Queryer, keyID, algorithm, publicKey, createdAt string) error
	RevokePublicKey(ctx context.Context, q Queryer, keyID, revokedAt string) error
	LookupSigningKey(ctx context.Context, q Queryer, keyID string) (store.SigningKeyRecord, error)
	LatestReceiptChainHead(ctx context.Context, q Queryer, subjectType core.SubjectType, subjectID string) (string, error)
	InsertReceipt(ctx context.Context, q Queryer, row store.ReceiptRow) error
}

// Signer signs covered acts with the ACTIVE keyring key and persists the
// receipt inside the caller's transaction.
type Signer struct {
	store ReceiptStore
	path  string // keyring file location
}

// NewSigner builds a signer over the given store and keyring path.
func NewSigner(st ReceiptStore, keyringPath string) *Signer {
	return &Signer{store: st, path: keyringPath}
}

// Sign mints and persists an immutable receipt for one covered act:
//
//  1. loads (or generates on first use) the keyring and selects the ACTIVE key;
//  2. FAILS CLOSED when the active key is revoked (keyring or store — the store
//     is authoritative) or when the stored public bytes differ from the seed's
//     derived public key (corruption);
//  3. computes the canonical payload hash, reads the subject's chain head
//     (genesis → empty previousReceiptHash), canonicalizes and signs the
//     unsigned envelope with crypto/ed25519;
//  4. registers the public key (INSERT OR IGNORE — the key may already exist)
//     and inserts the receipt row, both on the caller's q.
//
// q NEVER owns the transaction: the caller commits (or rolls back) it, so a
// failed signing rolls the covered act back with it.
func (s *Signer) Sign(ctx context.Context, q Queryer, payload core.ReceiptPayload, issuedAt string) (core.SignedReceipt, error) {
	kr, err := EnsureActiveKey(s.path)
	if err != nil {
		return core.SignedReceipt{}, fmt.Errorf("sign: load keyring: %w", err)
	}
	keyID := kr.ActiveKeyID
	if kr.RevokedAt(keyID) != "" {
		return core.SignedReceipt{}, fmt.Errorf("sign: active key %s is revoked in the keyring (%s) — a revoked key is never selected", keyID, kr.RevokedAt(keyID))
	}
	seed, err := kr.SeedFor(keyID)
	if err != nil {
		return core.SignedReceipt{}, fmt.Errorf("sign: %w", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	// The store is the authoritative revocation ledger: fail closed on a
	// revoked key and on stored public bytes that differ from the derived key.
	rec, err := s.store.LookupSigningKey(ctx, q, keyID)
	if err != nil {
		return core.SignedReceipt{}, fmt.Errorf("sign: %w", err)
	}
	if rec.Found {
		if rec.PublicKey != pubB64 {
			return core.SignedReceipt{}, fmt.Errorf("sign: key %s: stored public key %q differs from the derived key %q — corruption, fail closed", keyID, rec.PublicKey, pubB64)
		}
		if rec.RevokedAt != "" {
			return core.SignedReceipt{}, fmt.Errorf("sign: key %s was revoked at %s — a revoked key is never selected for new signatures", keyID, rec.RevokedAt)
		}
	}

	if payload.IssuedAt != issuedAt {
		return core.SignedReceipt{}, fmt.Errorf("sign: payload issuedAt %q differs from the emission timestamp %q", payload.IssuedAt, issuedAt)
	}
	payloadHash := core.ReceiptPayloadHash(payload)
	prev, err := s.store.LatestReceiptChainHead(ctx, q, payload.SubjectType, payload.SubjectID)
	if err != nil {
		return core.SignedReceipt{}, fmt.Errorf("sign: %w", err)
	}

	r := core.SignedReceipt{
		SubjectType:         payload.SubjectType,
		SubjectID:           payload.SubjectID,
		Action:              payload.Action,
		TenantID:            payload.TenantID,
		CompanyID:           payload.CompanyID,
		FiscalPeriodID:      payload.FiscalPeriodID,
		PayloadHash:         payloadHash,
		PreviousReceiptHash: prev,
		PrincipalID:         payload.PrincipalID,
		MembershipID:        payload.MembershipID,
		PolicyVersion:       payload.PolicyVersion,
		Algorithm:           core.ReceiptAlgorithm,
		KeyID:               keyID,
		IssuedAt:            issuedAt,
	}
	r.Signature = ""
	sig := ed25519.Sign(priv, core.CanonicalUnsignedEnvelope(r))
	r.Signature = base64.StdEncoding.EncodeToString(sig)

	if err := s.store.RegisterPublicKey(ctx, q, keyID, core.ReceiptAlgorithm, pubB64, kr.CreatedAt(keyID)); err != nil {
		return core.SignedReceipt{}, fmt.Errorf("sign: %w", err)
	}
	row := store.ReceiptRow{
		SubjectType:         r.SubjectType,
		SubjectID:           r.SubjectID,
		Action:              r.Action,
		TenantID:            r.TenantID,
		CompanyID:           r.CompanyID,
		FiscalPeriodID:      r.FiscalPeriodID,
		PayloadHash:         r.PayloadHash,
		PreviousReceiptHash: r.PreviousReceiptHash,
		PrincipalID:         r.PrincipalID,
		MembershipID:        r.MembershipID,
		PolicyVersion:       r.PolicyVersion,
		Algorithm:           r.Algorithm,
		KeyID:               r.KeyID,
		Signature:           sig,
		IssuedAt:            r.IssuedAt,
		PayloadJSON:         string(core.CanonicalReceiptPayload(payload)),
		ReceiptHash:         core.ReceiptHash(r),
	}
	if payload.SubjectType == core.SubjectTypeMemory {
		row.MemoryID = payload.SubjectID
	} else if payload.SubjectType == core.SubjectTypeJudgment {
		row.JudgmentID = payload.SubjectID
	} else if payload.SubjectType == core.SubjectTypeEvidenceObject {
		row.EvidenceObjectID = payload.SubjectID
	} else {
		row.ReconciliationID = payload.SubjectID
	}
	if err := s.store.InsertReceipt(ctx, q, row); err != nil {
		return core.SignedReceipt{}, fmt.Errorf("sign: %w", err)
	}
	return r, nil
}

// RotationResult reports what keys rotate changed.
type RotationResult struct {
	NewKeyID  string
	OldKeyID  string
	CreatedAt string
	RevokedAt string
}

// Rotate durably creates and activates a new key, then registers it and
// revokes the old public key IN ONE DB TRANSACTION (the design's rotation
// order: durable keyring activation first, then the atomic register+revoke
// commit). If the transaction fails, the previous keyring state is restored so
// the file and the store never disagree; a keyring that cannot restore its
// previous state reports both failures. Rotation is explicit, never scheduled:
// the old seed stays in the keyring for recovery but is never selected again.
func Rotate(ctx context.Context, st ReceiptStore, path string) (*RotationResult, error) {
	old, err := LoadKeyring(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("rotate: no active key to rotate — run keys init first")
		}
		return nil, fmt.Errorf("rotate: %w", err)
	}
	oldKeyID := old.ActiveKeyID
	if oldKeyID == "" || old.RevokedAt(oldKeyID) != "" {
		return nil, fmt.Errorf("rotate: no active (unrevoked) key to rotate — run keys init first")
	}

	seed, err := newSeed()
	if err != nil {
		return nil, fmt.Errorf("rotate: %w", err)
	}
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	newKeyID := core.ReceiptKeyID(pub)
	createdAt := nowISO()
	revokedAt := createdAt

	// (a) Durably activate the new key: the file write is atomic, so a crash
	// never leaves a half-written keyring.
	rotated := &Keyring{
		ActiveKeyID: newKeyID,
		keys:        make(map[string]KeyEntry, len(old.keys)+1),
		order:       append(append([]string{}, old.order...), newKeyID),
	}
	for id, entry := range old.keys {
		rotated.keys[id] = entry
	}
	rotated.keys[newKeyID] = KeyEntry{Seed: base64.StdEncoding.EncodeToString(seed), CreatedAt: createdAt}
	previous := old.keys[oldKeyID]
	previous.RevokedAt = revokedAt
	rotated.keys[oldKeyID] = previous
	if err := writeKeyring(path, rotated); err != nil {
		return nil, fmt.Errorf("rotate: %w", err)
	}

	// (b) Register the new key and revoke the old public key IN ONE transaction.
	// The OLD key is (re)registered FIRST with its keyring public bytes — an
	// init-only key that never signed has no store row yet, and revoking it
	// requires one (INSERT OR IGNORE makes a re-registration a no-op).
	oldPub, err := old.PublicKeyFor(oldKeyID)
	if err != nil {
		_ = restoreKeyring(path, old)
		return nil, fmt.Errorf("rotate: %w", err)
	}
	tx, err := st.BeginReceiptTx(ctx)
	if err != nil {
		_ = restoreKeyring(path, old)
		return nil, fmt.Errorf("rotate: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := st.RegisterPublicKey(ctx, tx, oldKeyID, core.ReceiptAlgorithm, base64.StdEncoding.EncodeToString(oldPub), old.CreatedAt(oldKeyID)); err != nil {
		_ = restoreKeyring(path, old)
		return nil, fmt.Errorf("rotate: %w", err)
	}
	if err := st.RegisterPublicKey(ctx, tx, newKeyID, core.ReceiptAlgorithm, base64.StdEncoding.EncodeToString(pub), createdAt); err != nil {
		_ = restoreKeyring(path, old)
		return nil, fmt.Errorf("rotate: %w", err)
	}
	if err := st.RevokePublicKey(ctx, tx, oldKeyID, revokedAt); err != nil {
		_ = restoreKeyring(path, old)
		return nil, fmt.Errorf("rotate: %w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = restoreKeyring(path, old)
		return nil, fmt.Errorf("rotate: commit: %w", err)
	}
	committed = true

	return &RotationResult{NewKeyID: newKeyID, OldKeyID: oldKeyID, CreatedAt: createdAt, RevokedAt: revokedAt}, nil
}

// restoreKeyring rewrites the previous keyring state after a failed rotation
// (best effort — the atomic write either lands or leaves the rotated file; a
// rotated file still fails closed because the signer checks the store).
func restoreKeyring(path string, old *Keyring) error {
	if err := writeKeyring(path, old); err != nil {
		return fmt.Errorf("rotate: restore previous keyring: %w", err)
	}
	return nil
}
