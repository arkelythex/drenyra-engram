// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.4.0 Step 3 ATOMIC
// receipt emission at the store boundary: every covered act (memory_recorded,
// memory_approved, memory_rejected, memory_voided, relation_confirmed,
// relation_rejected, evidence_linked, memory_superseded) mints EXACTLY ONE
// immutable Ed25519 receipt inside its own transaction with the captured
// timestamp; a signing failure rolls the act back (no act, no receipt);
// idempotent retries and duplicate links emit nothing; subject chains link on
// the previous receipt's hash; imported/sync records never mint receipts
// (docs/architecture/ed25519-receipts-step3.md "Atomic emission points").
//
// The deterministic test signer reuses the PROTOCOL's pinned parity seed
// (32×0x01, RFC 8032 keypair derivation via ed25519.NewKeyFromSeed — the SAME
// seed the TypeScript mirror documents), so every asserted receipt verifies with
// core.VerifyReceipt against the derived public key.
package store

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// paritySeedHex is the protocol's fixed parity seed: 32 bytes of 0x01 (shared
// with core/receipt_test.go and the TypeScript mirror).
const paritySeedHex = "0101010101010101010101010101010101010101010101010101010101010101"

// paritySigner is the deterministic ReceiptSigner used by the emission tests: it
// mirrors the real receipts.Signer orchestration (chain-head read, canonical
// envelope, Ed25519 signature, public-key registration and receipt insertion on
// the caller's Queryer) but derives its keypair from the pinned parity seed, so
// every receipt verifies with the same public key. `fail` injects a signing
// failure for the rollback contract.
type paritySigner struct {
	s         *SQLiteStore
	pub       ed25519.PublicKey
	priv      ed25519.PrivateKey
	keyID     string
	createdAt string
	fail      bool
}

func newParitySigner(s *SQLiteStore) *paritySigner {
	seed, err := hex.DecodeString(paritySeedHex)
	if err != nil {
		panic(err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return &paritySigner{s: s, pub: pub, priv: priv, keyID: core.ReceiptKeyID(pub), createdAt: "2026-08-05T12:00:00Z"}
}

// Sign implements store.ReceiptSigner: it runs entirely on the caller-provided
// Queryer and never starts or commits a transaction.
func (p *paritySigner) Sign(ctx context.Context, q Queryer, payload core.ReceiptPayload, issuedAt string) (core.SignedReceipt, error) {
	if p.fail {
		return core.SignedReceipt{}, errors.New("parity signer: injected signing failure")
	}
	if issuedAt == "" {
		return core.SignedReceipt{}, errors.New("parity signer: emission timestamp is empty")
	}
	if payload.IssuedAt != issuedAt {
		return core.SignedReceipt{}, errors.New("parity signer: payload issuedAt differs from the emission timestamp")
	}
	prev, err := p.s.LatestReceiptChainHead(ctx, q, payload.SubjectType, payload.SubjectID)
	if err != nil {
		return core.SignedReceipt{}, err
	}
	r := core.SignedReceipt{
		SubjectType:         payload.SubjectType,
		SubjectID:           payload.SubjectID,
		Action:              payload.Action,
		TenantID:            payload.TenantID,
		CompanyID:           payload.CompanyID,
		FiscalPeriodID:      payload.FiscalPeriodID,
		PayloadHash:         core.ReceiptPayloadHash(payload),
		PreviousReceiptHash: prev,
		PrincipalID:         payload.PrincipalID,
		MembershipID:        payload.MembershipID,
		PolicyVersion:       payload.PolicyVersion,
		Algorithm:           core.ReceiptAlgorithm,
		KeyID:               p.keyID,
		IssuedAt:            issuedAt,
	}
	sig := ed25519.Sign(p.priv, core.CanonicalUnsignedEnvelope(r))
	r.Signature = base64.StdEncoding.EncodeToString(sig)
	if err := p.s.RegisterPublicKey(ctx, q, p.keyID, core.ReceiptAlgorithm, base64.StdEncoding.EncodeToString(p.pub), p.createdAt); err != nil {
		return core.SignedReceipt{}, err
	}
	row := ReceiptRow{
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
	} else {
		row.JudgmentID = payload.SubjectID
	}
	if err := p.s.InsertReceipt(ctx, q, row); err != nil {
		return core.SignedReceipt{}, err
	}
	return r, nil
}

// ──────────────────────────────────────────────
// Receipt read-back and assertion helpers
// ──────────────────────────────────────────────

// storedReceipt is the full persisted shape of one receipt row.
type storedReceipt struct {
	SubjectType         string
	SubjectID           string
	Action              string
	TenantID            string
	CompanyID           string
	FiscalPeriodID      string
	PayloadHash         string
	PreviousReceiptHash string
	PrincipalID         string
	MembershipID        string
	PolicyVersion       string
	Algorithm           string
	KeyID               string
	Signature           []byte
	IssuedAt            string
	PayloadJSON         string
	ReceiptHash         string
	MemoryID            sql.NullString
	JudgmentID          sql.NullString
}

func readReceipts(t *testing.T, s *SQLiteStore) []storedReceipt {
	t.Helper()
	rows, err := s.db.Query(`SELECT subject_type, subject_id, action, tenant_id, company_id, fiscal_period_id,
		payload_hash, previous_receipt_hash, principal_id, membership_id, policy_version, algorithm, key_id,
		signature, issued_at, payload_json, receipt_hash, memory_id, judgment_id FROM receipts ORDER BY rowid`)
	if err != nil {
		t.Fatalf("query receipts: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []storedReceipt
	for rows.Next() {
		var r storedReceipt
		if err := rows.Scan(&r.SubjectType, &r.SubjectID, &r.Action, &r.TenantID, &r.CompanyID, &r.FiscalPeriodID,
			&r.PayloadHash, &r.PreviousReceiptHash, &r.PrincipalID, &r.MembershipID, &r.PolicyVersion,
			&r.Algorithm, &r.KeyID, &r.Signature, &r.IssuedAt, &r.PayloadJSON, &r.ReceiptHash,
			&r.MemoryID, &r.JudgmentID); err != nil {
			t.Fatalf("scan receipt: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate receipts: %v", err)
	}
	return out
}

func (r storedReceipt) payload(t *testing.T) core.ReceiptPayload {
	t.Helper()
	var p core.ReceiptPayload
	if err := json.Unmarshal([]byte(r.PayloadJSON), &p); err != nil {
		t.Fatalf("decode receipt payload_json: %v", err)
	}
	return p
}

// byAction returns the FIRST stored receipt carrying action (fail on missing).
func byAction(t *testing.T, receipts []storedReceipt, action string) storedReceipt {
	t.Helper()
	for _, r := range receipts {
		if r.Action == action {
			return r
		}
	}
	t.Fatalf("no receipt with action %q among %d stored receipts", action, len(receipts))
	return storedReceipt{}
}

// receiptStore opens a fresh store and attaches the deterministic parity signer.
func receiptStore(t *testing.T) (*SQLiteStore, *paritySigner) {
	t.Helper()
	s := newTestStore(t)
	signer := newParitySigner(s)
	s.SetReceiptSigner(signer)
	return s, signer
}

// verifyStored asserts that the stored receipt verifies offline with the parity
// public key and that the stored receipt_hash equals the derived digest.
func verifyStored(t *testing.T, signer *paritySigner, r storedReceipt) {
	t.Helper()
	signed := core.SignedReceipt{
		SubjectType:         core.SubjectType(r.SubjectType),
		SubjectID:           r.SubjectID,
		Action:              core.ReceiptAction(r.Action),
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
		Signature:           base64.StdEncoding.EncodeToString(r.Signature),
		IssuedAt:            r.IssuedAt,
	}
	if err := core.VerifyReceipt(signed, r.payload(t), signer.pub); err != nil {
		t.Fatalf("stored receipt %s/%s must verify: %v", r.Action, r.SubjectID, err)
	}
	if got := core.ReceiptHash(signed); got != r.ReceiptHash {
		t.Fatalf("stored receipt_hash %q, want derived %q", r.ReceiptHash, got)
	}
}

// supersededCopy returns a copy of a memory in its post-supersession state.
func supersededCopy(memory core.AccountingMemory, successorID string) core.AccountingMemory {
	post := core.CloneMemory(memory)
	post.Status = core.StatusSuperseded
	post.SupersedesID = successorID
	return post
}

// ──────────────────────────────────────────────
// memory_recorded
// ──────────────────────────────────────────────

func TestOpenWithoutSignerEmitsNoReceipts(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(validInput("receipt.nil-signer", "no signer attached")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts`); n != 0 {
		t.Fatalf("receipts rows = %d, want 0 (nil signer → no emission)", n)
	}
}

func TestSaveEmitsMemoryRecordedReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	res, err := s.Save(validInput("receipt.recorded", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := res.Memory.Identity.ID

	receipts := readReceipts(t, s)
	if len(receipts) != 1 {
		t.Fatalf("receipts = %d, want exactly 1", len(receipts))
	}
	r := receipts[0]
	if r.Action != string(core.ReceiptActionMemoryRecorded) || r.SubjectID != id {
		t.Fatalf("receipt = %s/%s, want memory_recorded/%s", r.Action, r.SubjectID, id)
	}
	if r.SubjectType != string(core.SubjectTypeMemory) || !r.MemoryID.Valid || r.MemoryID.String != id || r.JudgmentID.Valid {
		t.Fatalf("typed FK = (memory %q, judgment %q), want exactly memory %s", r.MemoryID.String, r.JudgmentID.String, id)
	}
	if r.PreviousReceiptHash != "" {
		t.Fatalf("genesis receipt chains on %q, want empty", r.PreviousReceiptHash)
	}
	p := r.payload(t)
	if p.SubjectType != core.SubjectTypeMemory || p.SubjectID != id || p.Action != core.ReceiptActionMemoryRecorded {
		t.Fatalf("payload subject/action drift: %s/%s/%s", p.SubjectType, p.SubjectID, p.Action)
	}
	if p.Version != core.ReceiptPayloadVersion {
		t.Fatalf("payload version = %q, want %q", p.Version, core.ReceiptPayloadVersion)
	}
	if p.TenantID != testOrgID || p.CompanyID != "acme" || p.FiscalPeriodID != testPeriod {
		t.Fatalf("payload scope = %s/%s/%s, want %s/acme/%s", p.TenantID, p.CompanyID, p.FiscalPeriodID, testOrgID, testPeriod)
	}
	if want := core.ComputeEnvelopeHash(res.Memory); p.ResultingEnvelopeHash != want {
		t.Fatalf("resultingEnvelopeHash = %q, want the new memory envelope %q", p.ResultingEnvelopeHash, want)
	}
	if p.PrincipalID != testAgentSource.ActorID {
		t.Fatalf("principalId = %q, want the recorded Source actor %q", p.PrincipalID, testAgentSource.ActorID)
	}
	if p.PolicyVersion != "kernel/v0.4.0" {
		t.Fatalf("policyVersion = %q, want kernel/v0.4.0 (non-policy act)", p.PolicyVersion)
	}
	if r.IssuedAt != res.Memory.RecordedAt || p.IssuedAt != r.IssuedAt {
		t.Fatalf("issuedAt = %q, want the save's recordedAt %q (shared transaction timestamp)", r.IssuedAt, res.Memory.RecordedAt)
	}
	verifyStored(t, signer, r)
}

func TestSaveAutoSupersessionEmitsSupersededThenRecorded(t *testing.T) {
	s, signer := receiptStore(t)
	first, err := s.Save(validInput("receipt.chain", "v1"))
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := s.Save(validInput("receipt.chain", "v2"))
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	firstID, secondID := first.Memory.Identity.ID, second.Memory.Identity.ID

	receipts := readReceipts(t, s)
	if len(receipts) != 3 {
		t.Fatalf("receipts = %d, want 3 (recorded#1, superseded#1, recorded#2)", len(receipts))
	}

	rec1 := receipts[0]
	if rec1.Action != string(core.ReceiptActionMemoryRecorded) || rec1.SubjectID != firstID || rec1.PreviousReceiptHash != "" {
		t.Fatalf("receipt[0] = %s/%s prev=%q, want genesis memory_recorded for the first subject", rec1.Action, rec1.SubjectID, rec1.PreviousReceiptHash)
	}

	sup := receipts[1]
	if sup.Action != string(core.ReceiptActionMemorySuperseded) || sup.SubjectID != firstID {
		t.Fatalf("receipt[1] = %s/%s, want memory_superseded for the PRIOR subject", sup.Action, sup.SubjectID)
	}
	p := sup.payload(t)
	if p.SuccessorID != secondID {
		t.Fatalf("successorId = %q, want the new revision %q", p.SuccessorID, secondID)
	}
	fromEnv := core.ComputeEnvelopeHash(first.Memory)
	toEnv := core.ComputeEnvelopeHash(supersededCopy(first.Memory, secondID))
	if p.FromEnvelopeHash != fromEnv || p.ToEnvelopeHash != toEnv {
		t.Fatalf("superseded envelopes = %q→%q, want %q→%q (pre/post transition)", p.FromEnvelopeHash, p.ToEnvelopeHash, fromEnv, toEnv)
	}
	if p.PrincipalID != testAgentSource.ActorID || p.PolicyVersion != "kernel/v0.4.0" {
		t.Fatalf("superseded principal/policy = %q/%q, want claimed %q/kernel/v0.4.0", p.PrincipalID, p.PolicyVersion, testAgentSource.ActorID)
	}
	// Chain: the supersession receipt chains on the FIRST recorded receipt.
	if sup.PreviousReceiptHash != rec1.ReceiptHash {
		t.Fatalf("superseded previousReceiptHash = %q, want the first recorded receipt hash %q", sup.PreviousReceiptHash, rec1.ReceiptHash)
	}
	if sup.IssuedAt != first.Memory.RecordedAt || sup.IssuedAt != second.Memory.RecordedAt {
		t.Fatalf("superseded issuedAt = %q, want the shared save timestamp %q/%q", sup.IssuedAt, first.Memory.RecordedAt, second.Memory.RecordedAt)
	}
	verifyStored(t, signer, sup)

	rec2 := receipts[2]
	if rec2.Action != string(core.ReceiptActionMemoryRecorded) || rec2.SubjectID != secondID {
		t.Fatalf("receipt[2] = %s/%s, want memory_recorded for the new subject", rec2.Action, rec2.SubjectID)
	}
	if rec2.PreviousReceiptHash != "" {
		t.Fatalf("recorded#2 previousReceiptHash = %q, want empty (new subject chain)", rec2.PreviousReceiptHash)
	}
	verifyStored(t, signer, rec2)
}

// ──────────────────────────────────────────────
// memory_approved
// ──────────────────────────────────────────────

func TestApproveMemoryEmitsApprovalReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, err := s.Save(gatedInput("receipt.approve", "needs review"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	principal := controllerPrincipal(t)
	expected := currentEnvelope(saved)
	result, err := approve(s, id, expected, "req-approve-receipt", principal)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	receipts := readReceipts(t, s)
	if len(receipts) != 2 {
		t.Fatalf("receipts = %d, want 2 (recorded + approved)", len(receipts))
	}
	r := byAction(t, receipts, string(core.ReceiptActionMemoryApproved))
	if r.SubjectID != id || !r.MemoryID.Valid || r.MemoryID.String != id || r.JudgmentID.Valid {
		t.Fatalf("approved receipt subject = %q (memory %q, judgment %q), want %q", r.SubjectID, r.MemoryID.String, r.JudgmentID.String, id)
	}
	p := r.payload(t)
	if p.ReviewedEnvelopeHash != expected {
		t.Fatalf("reviewedEnvelopeHash = %q, want H1 %q", p.ReviewedEnvelopeHash, expected)
	}
	if p.ResultingEnvelopeHash != result.ResultingEnvelopeHash {
		t.Fatalf("resultingEnvelopeHash = %q, want H2 %q", p.ResultingEnvelopeHash, result.ResultingEnvelopeHash)
	}
	if p.Reason != "approved by fixture reviewer" {
		t.Fatalf("reason = %q, want the approval reason", p.Reason)
	}
	// The complete verified principal snapshot — never a claimed actor.
	if p.PrincipalID != "subject-1" || p.MembershipID != "membership-1" {
		t.Fatalf("principal = %q/%q, want subject-1/membership-1", p.PrincipalID, p.MembershipID)
	}
	if !reflect.DeepEqual(p.PrincipalRoles, []string{"controller"}) {
		t.Fatalf("principalRoles = %v, want [controller] (sorted snapshot roles)", p.PrincipalRoles)
	}
	if p.AuthenticationMethod != "session" || p.AssuranceLevel != "standard" || p.PrincipalAuthenticatedAt != "2026-08-05T12:00:00Z" {
		t.Fatalf("authentication snapshot = %s/%s/%s, want session/standard/2026-08-05T12:00:00Z",
			p.AuthenticationMethod, p.AssuranceLevel, p.PrincipalAuthenticatedAt)
	}
	if p.PolicyVersion != "approval-policy/v0.4.0" {
		t.Fatalf("policyVersion = %q, want approval-policy/v0.4.0 (the decision's policy)", p.PolicyVersion)
	}
	if r.IssuedAt != result.ApprovedAt || p.IssuedAt != r.IssuedAt {
		t.Fatalf("issuedAt = %q, want the approval's captured now %q", r.IssuedAt, result.ApprovedAt)
	}
	verifyStored(t, signer, r)
}

func TestApproveMemoryIdempotentRetryEmitsNoDuplicateReceipt(t *testing.T) {
	s, _ := receiptStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, err := s.Save(gatedInput("receipt.approve-idem", "idempotent approval"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	principal := controllerPrincipal(t)
	expected := currentEnvelope(saved)

	first, err := approve(s, id, expected, "req-approve-idem", principal)
	if err != nil {
		t.Fatalf("first approve: %v", err)
	}
	replay, err := approve(s, id, expected, "req-approve-idem", principal)
	if err != nil {
		t.Fatalf("replay approve: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("retry must be an idempotent replay")
	}
	if first.ApprovalEventID != replay.ApprovalEventID {
		t.Fatalf("replayed event %q, want the original %q", replay.ApprovalEventID, first.ApprovalEventID)
	}

	receipts := readReceipts(t, s)
	if len(receipts) != 2 {
		t.Fatalf("receipts = %d, want 2 (recorded + approved) — the idempotent retry must NOT mint a duplicate", len(receipts))
	}
}

// ──────────────────────────────────────────────
// memory_rejected / memory_voided
// ──────────────────────────────────────────────

func TestRejectEmitsMemoryRejectedReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	saved, err := s.Save(gatedInput("receipt.reject", "to reject"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	meta := core.TransitionMeta{Actor: "reviewer-9", ActorKind: core.ActorKindHuman, Timestamp: "2026-08-05T14:00:00Z"}
	if _, err := s.ApplyStatusTransition(id, core.StatusRejected, meta); err != nil {
		t.Fatalf("reject transition: %v", err)
	}

	receipts := readReceipts(t, s)
	if len(receipts) != 2 {
		t.Fatalf("receipts = %d, want 2 (recorded + rejected)", len(receipts))
	}
	r := byAction(t, receipts, string(core.ReceiptActionMemoryRejected))
	p := r.payload(t)
	fromEnv := core.ComputeEnvelopeHash(saved.Memory)
	toEnv := core.ComputeEnvelopeHash(memoryWithStatus(saved.Memory, core.StatusRejected))
	if p.ReviewedEnvelopeHash != fromEnv || p.ResultingEnvelopeHash != toEnv {
		t.Fatalf("rejected envelopes = %q→%q, want %q→%q (before/after the transition)", p.ReviewedEnvelopeHash, p.ResultingEnvelopeHash, fromEnv, toEnv)
	}
	if p.PrincipalID != "reviewer-9" || p.PolicyVersion != "kernel/v0.4.0" {
		t.Fatalf("rejected principal/policy = %q/%q, want claimed reviewer-9/kernel/v0.4.0", p.PrincipalID, p.PolicyVersion)
	}
	if r.IssuedAt != meta.Timestamp || p.IssuedAt != r.IssuedAt {
		t.Fatalf("rejected issuedAt = %q, want the transition timestamp %q", r.IssuedAt, meta.Timestamp)
	}
	verifyStored(t, signer, r)
}

func TestVoidEmitsMemoryVoidedReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	saved, err := s.Save(validInput("receipt.void", "to void"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	meta := core.TransitionMeta{Actor: "auditor-4", ActorKind: core.ActorKindHuman, Timestamp: "2026-08-05T14:30:00Z"}
	if _, err := s.ApplyStatusTransition(id, core.StatusVoided, meta); err != nil {
		t.Fatalf("void transition: %v", err)
	}

	receipts := readReceipts(t, s)
	if len(receipts) != 2 {
		t.Fatalf("receipts = %d, want 2 (recorded + voided)", len(receipts))
	}
	r := byAction(t, receipts, string(core.ReceiptActionMemoryVoided))
	p := r.payload(t)
	fromEnv := core.ComputeEnvelopeHash(saved.Memory)
	toEnv := core.ComputeEnvelopeHash(memoryWithStatus(saved.Memory, core.StatusVoided))
	if p.ReviewedEnvelopeHash != fromEnv || p.ResultingEnvelopeHash != toEnv {
		t.Fatalf("voided envelopes = %q→%q, want %q→%q (before/after the transition)", p.ReviewedEnvelopeHash, p.ResultingEnvelopeHash, fromEnv, toEnv)
	}
	if p.PrincipalID != "auditor-4" || p.PolicyVersion != "kernel/v0.4.0" {
		t.Fatalf("voided principal/policy = %q/%q, want claimed auditor-4/kernel/v0.4.0", p.PrincipalID, p.PolicyVersion)
	}
	if r.IssuedAt != meta.Timestamp || p.IssuedAt != r.IssuedAt {
		t.Fatalf("voided issuedAt = %q, want the transition timestamp %q", r.IssuedAt, meta.Timestamp)
	}
	verifyStored(t, signer, r)
}

// memoryWithStatus returns a copy of memory carrying the given status (the
// envelope before/after a status transition).
func memoryWithStatus(memory core.AccountingMemory, status core.MemoryStatus) core.AccountingMemory {
	post := core.CloneMemory(memory)
	post.Status = status
	return post
}

// ──────────────────────────────────────────────
// memory_superseded (explicit)
// ──────────────────────────────────────────────

func TestSupersedeExplicitEmitsMemorySupersededReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	a, err := s.Save(validInput("receipt.supersede.a", "superseded memory"))
	if err != nil {
		t.Fatalf("save a: %v", err)
	}
	b, err := s.Save(validInput("receipt.supersede.b", "successor memory"))
	if err != nil {
		t.Fatalf("save b: %v", err)
	}
	aID, bID := a.Memory.Identity.ID, b.Memory.Identity.ID
	meta := core.TransitionMeta{Actor: "cli-1", ActorKind: core.ActorKindHuman, Timestamp: "2026-08-05T15:00:00Z"}
	if _, err := s.SupersedeExplicit(aID, bID, meta); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	receipts := readReceipts(t, s)
	if len(receipts) != 3 {
		t.Fatalf("receipts = %d, want 3 (recorded a, recorded b, superseded a)", len(receipts))
	}
	r := byAction(t, receipts, string(core.ReceiptActionMemorySuperseded))
	if r.SubjectID != aID || !r.MemoryID.Valid || r.MemoryID.String != aID {
		t.Fatalf("superseded subject = %q (memory %q), want %q", r.SubjectID, r.MemoryID.String, aID)
	}
	p := r.payload(t)
	if p.SuccessorID != bID {
		t.Fatalf("successorId = %q, want %q", p.SuccessorID, bID)
	}
	fromEnv := core.ComputeEnvelopeHash(a.Memory)
	toEnv := core.ComputeEnvelopeHash(supersededCopy(a.Memory, bID))
	if p.FromEnvelopeHash != fromEnv || p.ToEnvelopeHash != toEnv {
		t.Fatalf("superseded envelopes = %q→%q, want %q→%q (pre/post transition)", p.FromEnvelopeHash, p.ToEnvelopeHash, fromEnv, toEnv)
	}
	if p.PrincipalID != "cli-1" || p.PolicyVersion != "kernel/v0.4.0" {
		t.Fatalf("superseded principal/policy = %q/%q, want claimed cli-1/kernel/v0.4.0", p.PrincipalID, p.PolicyVersion)
	}
	if r.IssuedAt != meta.Timestamp || p.IssuedAt != r.IssuedAt {
		t.Fatalf("superseded issuedAt = %q, want the transition timestamp %q", r.IssuedAt, meta.Timestamp)
	}
	// Chain: the supersession receipt chains on the superseded memory's recorded receipt.
	recA := byAction(t, receipts, string(core.ReceiptActionMemoryRecorded))
	if recA.SubjectID != aID {
		t.Fatalf("first recorded receipt subject = %q, want %q", recA.SubjectID, aID)
	}
	if r.PreviousReceiptHash != recA.ReceiptHash {
		t.Fatalf("superseded previousReceiptHash = %q, want the recorded receipt hash %q", r.PreviousReceiptHash, recA.ReceiptHash)
	}
	verifyStored(t, signer, r)
}

// ──────────────────────────────────────────────
// relation_confirmed / relation_rejected
// ──────────────────────────────────────────────

func TestConfirmJudgmentEmitsRelationConfirmedReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	principal := controllerPrincipal(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal", RequestID: "req-propose-confirm",
	}, testProposer)
	proposedHash := core.ComputeJudgmentHash(proposed.Judgment)
	confirmed := confirmJudgment(t, s, proposed.JudgmentID, proposedHash, "req-confirm-receipt", "resolution text", principal)

	receipts := readReceipts(t, s)
	if len(receipts) != 3 {
		t.Fatalf("receipts = %d, want 3 (recorded from, recorded to, relation_confirmed)", len(receipts))
	}
	r := byAction(t, receipts, string(core.ReceiptActionRelationConfirmed))
	if r.SubjectType != string(core.SubjectTypeJudgment) || r.SubjectID != proposed.JudgmentID ||
		!r.JudgmentID.Valid || r.JudgmentID.String != proposed.JudgmentID || r.MemoryID.Valid {
		t.Fatalf("relation_confirmed subject = %s/%s (memory %q, judgment %q), want judgment/%s",
			r.SubjectType, r.SubjectID, r.MemoryID.String, r.JudgmentID.String, proposed.JudgmentID)
	}
	p := r.payload(t)
	if p.ReviewedJudgmentHash != proposedHash {
		t.Fatalf("reviewedJudgmentHash = %q, want the proposed hash %q", p.ReviewedJudgmentHash, proposedHash)
	}
	if p.ResultingJudgmentHash != core.ComputeJudgmentHash(confirmed.Judgment) {
		t.Fatalf("resultingJudgmentHash = %q, want the confirmed judgment hash %q", p.ResultingJudgmentHash, core.ComputeJudgmentHash(confirmed.Judgment))
	}
	if p.FromMemoryID != from || p.ToMemoryID != to {
		t.Fatalf("from/to memory ids = %q/%q, want %q/%q", p.FromMemoryID, p.ToMemoryID, from, to)
	}
	fromMem, okFrom := s.FindByID(from)
	toMem, okTo := s.FindByID(to)
	if !okFrom || !okTo {
		t.Fatalf("read observation envelopes: from ok=%v to ok=%v", okFrom, okTo)
	}
	if p.FromEnvelopeHash != core.ComputeEnvelopeHash(fromMem) || p.ToEnvelopeHash != core.ComputeEnvelopeHash(toMem) {
		t.Fatalf("observation envelopes = %q/%q, want %q/%q (current hashes)",
			p.FromEnvelopeHash, p.ToEnvelopeHash, core.ComputeEnvelopeHash(fromMem), core.ComputeEnvelopeHash(toMem))
	}
	if p.Reason != "resolution text" {
		t.Fatalf("resolution = %q, want the confirm resolution", p.Reason)
	}
	if p.PrincipalID != "subject-1" || p.MembershipID != "membership-1" || !reflect.DeepEqual(p.PrincipalRoles, []string{"controller"}) {
		t.Fatalf("principal snapshot = %q/%q/%v, want subject-1/membership-1/[controller]", p.PrincipalID, p.MembershipID, p.PrincipalRoles)
	}
	if p.PolicyVersion != "judgment-policy/v0.4.0" {
		t.Fatalf("policyVersion = %q, want judgment-policy/v0.4.0", p.PolicyVersion)
	}
	if r.IssuedAt != confirmed.Judgment.DecidedAt || p.IssuedAt != r.IssuedAt {
		t.Fatalf("issuedAt = %q, want the decision's captured now %q", r.IssuedAt, confirmed.Judgment.DecidedAt)
	}
	verifyStored(t, signer, r)
}

func TestRejectJudgmentEmitsRelationRejectedReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	principal := controllerPrincipal(t)
	from, to := proposeContext(t, s)
	proposed := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "proposal", RequestID: "req-propose-reject",
	}, testProposer)
	proposedHash := core.ComputeJudgmentHash(proposed.Judgment)
	rejected := rejectJudgment(t, s, proposed.JudgmentID, proposedHash, "req-reject-receipt", "not supported by evidence", principal)

	receipts := readReceipts(t, s)
	if len(receipts) != 3 {
		t.Fatalf("receipts = %d, want 3 (recorded from, recorded to, relation_rejected)", len(receipts))
	}
	r := byAction(t, receipts, string(core.ReceiptActionRelationRejected))
	p := r.payload(t)
	if p.ReviewedJudgmentHash != proposedHash {
		t.Fatalf("reviewedJudgmentHash = %q, want the proposed hash %q", p.ReviewedJudgmentHash, proposedHash)
	}
	if p.ResultingJudgmentHash != core.ComputeJudgmentHash(rejected.Judgment) {
		t.Fatalf("resultingJudgmentHash = %q, want the rejected judgment hash %q", p.ResultingJudgmentHash, core.ComputeJudgmentHash(rejected.Judgment))
	}
	if p.FromMemoryID != from || p.ToMemoryID != to {
		t.Fatalf("from/to memory ids = %q/%q, want %q/%q", p.FromMemoryID, p.ToMemoryID, from, to)
	}
	fromMem, okFrom := s.FindByID(from)
	toMem, okTo := s.FindByID(to)
	if !okFrom || !okTo {
		t.Fatalf("read observation envelopes: from ok=%v to ok=%v", okFrom, okTo)
	}
	if p.FromEnvelopeHash != core.ComputeEnvelopeHash(fromMem) || p.ToEnvelopeHash != core.ComputeEnvelopeHash(toMem) {
		t.Fatalf("observation envelopes = %q/%q, want %q/%q", p.FromEnvelopeHash, p.ToEnvelopeHash, core.ComputeEnvelopeHash(fromMem), core.ComputeEnvelopeHash(toMem))
	}
	if p.Reason != "not supported by evidence" {
		t.Fatalf("resolution = %q, want the rejection reason", p.Reason)
	}
	if p.PrincipalID != "subject-1" || p.PolicyVersion != "judgment-policy/v0.4.0" {
		t.Fatalf("principal/policy = %q/%q, want subject-1/judgment-policy/v0.4.0", p.PrincipalID, p.PolicyVersion)
	}
	if r.IssuedAt != rejected.Judgment.DecidedAt || p.IssuedAt != r.IssuedAt {
		t.Fatalf("issuedAt = %q, want the decision's captured now %q", r.IssuedAt, rejected.Judgment.DecidedAt)
	}
	verifyStored(t, signer, r)
}

func TestJudgmentCorrectionCoversPredecessorSupersession(t *testing.T) {
	s, _ := receiptStore(t)
	principal := controllerPrincipal(t)
	from, to := proposeContext(t, s)

	first := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "original", RequestID: "req-corr-first",
	}, testProposer)
	confirmJudgment(t, s, first.JudgmentID, core.ComputeJudgmentHash(first.Judgment), "req-corr-confirm-1", "first resolution", principal)

	second := proposeJudgment(t, s, core.ProposeJudgmentCommand{
		FromID: from, ToID: to, Relation: core.RelationSupports,
		Reason: "correction", RequestID: "req-corr-second", PredecessorID: first.JudgmentID,
	}, testProposer)
	confirmJudgment(t, s, second.JudgmentID, core.ComputeJudgmentHash(second.Judgment), "req-corr-confirm-2", "corrected resolution", principal)

	// The predecessor supersession is COVERED inside the correction's
	// relation_confirmed: exactly one receipt per confirmed judgment — no extra
	// action for the predecessor (it is a judgment subject, never a memory act).
	receipts := readReceipts(t, s)
	if len(receipts) != 4 {
		t.Fatalf("receipts = %d, want 4 (2 recorded + 2 relation_confirmed)", len(receipts))
	}
	var confirmedReceipts int
	for _, r := range receipts {
		switch r.Action {
		case string(core.ReceiptActionMemoryRecorded):
			if r.SubjectType != string(core.SubjectTypeMemory) {
				t.Fatalf("recorded receipt subject type = %s, want memory", r.SubjectType)
			}
		case string(core.ReceiptActionRelationConfirmed):
			confirmedReceipts++
		default:
			t.Fatalf("unexpected action %q in a correction flow — predecessor supersession must not mint a separate receipt", r.Action)
		}
	}
	if confirmedReceipts != 2 {
		t.Fatalf("relation_confirmed receipts = %d, want 2 (one per confirmed judgment)", confirmedReceipts)
	}
}

// ──────────────────────────────────────────────
// evidence_linked
// ──────────────────────────────────────────────

func TestAddEvidenceLinkEmitsEvidenceLinkedReceipt(t *testing.T) {
	s, signer := receiptStore(t)
	saved, err := s.Save(validInput("receipt.link", "linked memory"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	const ref = "evidence://factura-2024-0001.pdf"
	if err := s.AddEvidenceLink(id, ref, "cli"); err != nil {
		t.Fatalf("add evidence link: %v", err)
	}

	receipts := readReceipts(t, s)
	if len(receipts) != 2 {
		t.Fatalf("receipts = %d, want 2 (recorded + evidence_linked)", len(receipts))
	}
	r := byAction(t, receipts, string(core.ReceiptActionEvidenceLinked))
	p := r.payload(t)
	fromEnv := core.ComputeEnvelopeHash(saved.Memory)
	linked, ok := s.FindByID(id)
	if !ok {
		t.Fatalf("read linked memory: not found")
	}
	toEnv := core.ComputeEnvelopeHash(linked)
	if p.FromEnvelopeHash != fromEnv || p.ToEnvelopeHash != toEnv {
		t.Fatalf("link envelopes = %q→%q, want %q→%q (pre-link vs merged post-link)", p.FromEnvelopeHash, p.ToEnvelopeHash, fromEnv, toEnv)
	}
	if p.EvidenceRef != ref {
		t.Fatalf("evidenceRef = %q, want the exact ref %q", p.EvidenceRef, ref)
	}
	if p.PrincipalID != "cli" || p.PolicyVersion != "kernel/v0.4.0" {
		t.Fatalf("link principal/policy = %q/%q, want claimed cli/kernel/v0.4.0", p.PrincipalID, p.PolicyVersion)
	}
	rec := byAction(t, receipts, string(core.ReceiptActionMemoryRecorded))
	if rec.SubjectID != id {
		t.Fatalf("recorded receipt subject = %q, want %q", rec.SubjectID, id)
	}
	if r.PreviousReceiptHash != rec.ReceiptHash {
		t.Fatalf("link previousReceiptHash = %q, want the recorded receipt hash %q", r.PreviousReceiptHash, rec.ReceiptHash)
	}
	verifyStored(t, signer, r)

	// A duplicate evidence link is a NO-OP: no new receipt, still exactly two.
	if err := s.AddEvidenceLink(id, ref, "cli"); err != nil {
		t.Fatalf("duplicate evidence link: %v", err)
	}
	if receipts := readReceipts(t, s); len(receipts) != 2 {
		t.Fatalf("receipts after duplicate link = %d, want 2 (duplicates stay no-ops)", len(receipts))
	}
}

func TestAddRuleLinkEmitsNoReceipt(t *testing.T) {
	s, _ := receiptStore(t)
	saved, err := s.Save(validInput("receipt.rule-link", "rule-linked memory"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.AddRuleLink(saved.Memory.Identity.ID, "rule://tax-2024-008", "cli"); err != nil {
		t.Fatalf("add rule link: %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts`); n != 1 {
		t.Fatalf("receipts = %d, want 1 (recorded only — rule links are NOT covered)", n)
	}
}

// ──────────────────────────────────────────────
// Rollback: a signing failure undoes the act AND its receipt
// ──────────────────────────────────────────────

func TestSigningFailureRollsBackSave(t *testing.T) {
	s, signer := receiptStore(t)
	signer.fail = true
	if _, err := s.Save(validInput("receipt.rollback.save", "must not persist")); err == nil {
		t.Fatal("save must fail when the signer fails")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM observations`); n != 0 {
		t.Fatalf("observations = %d, want 0 — the act must roll back", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts`); n != 0 {
		t.Fatalf("receipts = %d, want 0 — the receipt must roll back with the act", n)
	}

	signer.fail = false
	if _, err := s.Save(validInput("receipt.rollback.save", "now it persists")); err != nil {
		t.Fatalf("save after recovery: %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts`); n != 1 {
		t.Fatalf("receipts = %d, want 1 after recovery", n)
	}
}

func TestSigningFailureRollsBackApproval(t *testing.T) {
	s, signer := receiptStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, err := s.Save(gatedInput("receipt.rollback.approve", "must stay pending"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	principal := controllerPrincipal(t)
	expected := currentEnvelope(saved)

	signer.fail = true
	if _, err := approve(s, id, expected, "req-rollback-approve", principal); err == nil {
		t.Fatal("approval must fail when the signer fails")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM approval_events`); n != 0 {
		t.Fatalf("approval_events = %d, want 0 — the approval must roll back", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts`); n != 1 {
		t.Fatalf("receipts = %d, want 1 (only the recorded receipt — the approval receipt must roll back)", n)
	}
	if stored := readStoredMemoryStatus(t, s, id); stored != core.StatusPendingReview {
		t.Fatalf("memory status = %s, want pending_review (unchanged)", stored)
	}

	signer.fail = false
	if _, err := approve(s, id, expected, "req-rollback-approve", principal); err != nil {
		t.Fatalf("approval after recovery: %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM receipts`); n != 2 {
		t.Fatalf("receipts = %d, want 2 after recovery (recorded + approved)", n)
	}
}

// readStoredMemoryStatus reads the current status column of a memory row.
func readStoredMemoryStatus(t *testing.T, s *SQLiteStore, id string) core.MemoryStatus {
	t.Helper()
	var status string
	if err := s.db.QueryRow(`SELECT status FROM observations WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("read status of %s: %v", id, err)
	}
	return core.MemoryStatus(status)
}

// ──────────────────────────────────────────────
// Imported / sync records never mint receipts
// ──────────────────────────────────────────────

func TestImportedRecordsEmitNoReceipts(t *testing.T) {
	src, _ := receiptStore(t)
	saved, err := src.Save(validInput("receipt.import.source", "source observation"))
	if err != nil {
		t.Fatalf("save source: %v", err)
	}
	id := saved.Memory.Identity.ID

	sink, _ := receiptStore(t)
	imported, err := sink.ImportObservation(core.CloneMemory(saved.Memory))
	if err != nil {
		t.Fatalf("import observation: %v", err)
	}
	if !imported {
		t.Fatal("fresh import must insert")
	}
	if _, err := sink.ImportTransition(core.StatusTransitionRecord{
		MemoryID: id, From: core.StatusActive, To: core.StatusSuperseded,
		Actor: "remote-agent", ActorKind: core.ActorKindAgent, Timestamp: "2026-08-05T10:00:00Z",
	}); err != nil {
		t.Fatalf("import transition: %v", err)
	}
	if _, err := sink.ApplyImportedStatus(id, core.StatusVoided, core.TransitionMeta{
		Actor: "remote-agent", ActorKind: core.ActorKindAgent, Timestamp: "2026-08-05T11:00:00Z",
	}); err != nil {
		t.Fatalf("apply imported status: %v", err)
	}

	if n := countRows(t, sink, `SELECT COUNT(*) FROM receipts`); n != 0 {
		t.Fatalf("sink receipts = %d, want 0 — imported/sync records never mint local receipts for remote historical acts", n)
	}
	if n := countRows(t, src, `SELECT COUNT(*) FROM receipts`); n != 1 {
		t.Fatalf("source receipts = %d, want 1 (the source's own recorded receipt)", n)
	}
}
