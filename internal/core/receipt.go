// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module defines the v0.4.0 Step 3 action
// receipt PROTOCOL (pure values, canonicalization and minimal verification; see
// docs/architecture/ed25519-receipts-step3.md).
//
// An Ed25519 receipt is an immutable, signed record of one covered act
// (memory_recorded | memory_approved | memory_rejected | memory_voided |
// relation_confirmed | relation_rejected | evidence_linked | memory_superseded |
// memory_closed | memory_reopened | reconciliation_confirmed |
// reconciliation_rejected).
// It proves INTEGRITY and SIGNER POSSESSION — never accounting correctness
// (documented: Accounting correctness: NOT ASSERTED).
//
// This module is PURE: no I/O, no keyring, no store. It owns the closed model,
// the canonical byte contract (fixed property order, compact UTF-8 JSON, JSON
// string escaping, NO HTML escaping — byte-identical with the TypeScript mirror
// in core/receipt.ts), the lowercase SHA-256 hex digests, and VerifyReceipt's
// fail-closed checks. Signing orchestration, key storage and atomic emission
// live in later commits (internal/receipts + store batch).
package core

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ──────────────────────────────────────────────
// Closed vocabulary
// ──────────────────────────────────────────────

// SubjectType is the kind of entity a receipt covers: an immutable memory
// observation or an accounting judgment.
type SubjectType string

const (
	// SubjectTypeMemory is a memory observation subject.
	SubjectTypeMemory SubjectType = "memory"
	// SubjectTypeJudgment is an accounting judgment subject.
	SubjectTypeJudgment SubjectType = "judgment"
	// SubjectTypeReconciliation is a first-class reconciliation subject
	// (v0.5.0 — adjudicated reconciliations; design §3.2).
	SubjectTypeReconciliation SubjectType = "reconciliation"
	// SubjectTypeEvidenceObject is an immutable EvidenceObject subject
	// (v0.7.0 local-first slice — docs/architecture/evidence-object-v0.7.md).
	// The subject id is the content-addressed SHA-256 hex of the object bytes.
	SubjectTypeEvidenceObject SubjectType = "evidence_object"
)

// IsValidSubjectType reports whether t is a known receipt subject type.
func IsValidSubjectType(t SubjectType) bool {
	return t == SubjectTypeMemory || t == SubjectTypeJudgment || t == SubjectTypeReconciliation || t == SubjectTypeEvidenceObject
}

// ReceiptAction is a covered act. The set is CLOSED — an unknown action fails
// closed (VerifyReceipt rejects it; emission points can never mint it).
type ReceiptAction string

const (
	// ReceiptActionMemoryRecorded covers a new memory and its resulting
	// envelope hash.
	ReceiptActionMemoryRecorded ReceiptAction = "memory_recorded"
	// ReceiptActionMemoryApproved covers a reviewed H1, resulting H2, reason
	// and the complete verified principal snapshot.
	ReceiptActionMemoryApproved ReceiptAction = "memory_approved"
	// ReceiptActionMemoryRejected covers a rejection transition (hashes before
	// and after).
	ReceiptActionMemoryRejected ReceiptAction = "memory_rejected"
	// ReceiptActionMemoryVoided covers a void transition (hashes before and
	// after).
	ReceiptActionMemoryVoided ReceiptAction = "memory_voided"
	// ReceiptActionRelationConfirmed covers a confirmed judgment (proposed and
	// resulting judgment hashes, both observation ids and current envelope
	// hashes, resolution, verified principal snapshot).
	ReceiptActionRelationConfirmed ReceiptAction = "relation_confirmed"
	// ReceiptActionRelationRejected covers a rejected judgment (same coverage
	// as relation_confirmed).
	ReceiptActionRelationRejected ReceiptAction = "relation_rejected"
	// ReceiptActionEvidenceLinked covers a genuinely new evidence link (pre and
	// post link envelope hashes + the exact evidence reference).
	ReceiptActionEvidenceLinked ReceiptAction = "evidence_linked"
	// ReceiptActionMemorySuperseded covers a memory superseded by a successor.
	ReceiptActionMemorySuperseded ReceiptAction = "memory_superseded"
	// ReceiptActionMemoryClosed covers an APPROVED monthly close: the close
	// approval emits memory_approved THEN memory_closed atomically on the close
	// memory's receipt chain (v0.5.0 close foundation). The payload covers H1/H2,
	// scope, the approval principal, policy and reason; the snapshot itself is
	// covered transitively by H2 (the resulting envelope hash includes the close
	// snapshot).
	ReceiptActionMemoryClosed ReceiptAction = "memory_closed"
	// ReceiptActionMemoryReopened covers an explicit controller reopen of a
	// closed period (v0.5.0; emitted by ReopenPeriod — next batch). Defined now
	// so the closed set and schema CHECK stay in parity.
	ReceiptActionMemoryReopened ReceiptAction = "memory_reopened"
	// ReceiptActionReconciliationConfirmed covers a CONFIRMED first-class
	// reconciliation (v0.5.0): the reviewed and resulting reconciliation
	// hashes, both endpoint observation ids and envelope hashes, the resolution
	// and the complete verified principal snapshot. Emission is atomic with the
	// confirmation and its reconciles relation projection.
	ReceiptActionReconciliationConfirmed ReceiptAction = "reconciliation_confirmed"
	// ReceiptActionReconciliationRejected covers a REJECTED first-class
	// reconciliation (v0.5.0): same coverage as reconciliation_confirmed; a
	// rejected proposal projects no relation.
	ReceiptActionReconciliationRejected ReceiptAction = "reconciliation_rejected"
	// ReceiptActionObjectStored covers a genuinely NEW EvidenceObject write
	// (v0.7.0): the immutable capture of an artifact under its deterministic
	// content address. The payload's EvidenceRef carries the object id (the
	// SHA-256 hex — the object's identity); a content-addressed duplicate
	// (identical bytes already stored) is a NO-OP and emits NOTHING.
	ReceiptActionObjectStored ReceiptAction = "object_stored"
	// ── v0.8.0 evidence-lifecycle acts (design §4 step 3 / §5) ──
	// The v8→v9 migration extends the receipts action CHECK with these seven
	// acts. Emission is receipt-covered ONLY for a newly bound policy
	// (retention_bound, on the OBJECT's chain at binding time — design §5/§6,
	// deferred to the binding batch); the purge-transition acts are frozen for
	// the request/approval/execution batches. No batch-2 operation emits any of
	// these: policy put/resolve/evaluate are not object-chain acts.
	//
	// ReceiptActionRetentionBound covers the retention_bound act: the
	// resolution snapshot (policy id, version, eligibility, resolution time)
	// bound to an object so a later policy change is auditable against what
	// was bound at request time (design §6).
	ReceiptActionRetentionBound ReceiptAction = "retention_bound"
	// ReceiptActionPurgeRequested covers a purge request transition.
	ReceiptActionPurgeRequested ReceiptAction = "purge_requested"
	// ReceiptActionPurgeApproved covers an approval transition.
	ReceiptActionPurgeApproved ReceiptAction = "purge_approved"
	// ReceiptActionPurgeRejected covers a rejection transition (terminal).
	ReceiptActionPurgeRejected ReceiptAction = "purge_rejected"
	// ReceiptActionPurgeCancelled covers a requester retraction.
	ReceiptActionPurgeCancelled ReceiptAction = "purge_cancelled"
	// ReceiptActionPurgeWithdrawn covers an approval retraction.
	ReceiptActionPurgeWithdrawn ReceiptAction = "purge_withdrawn"
	// ReceiptActionPurgeExecuted covers the physical execution (terminal).
	ReceiptActionPurgeExecuted ReceiptAction = "purge_executed"
)

// IsValidReceiptAction reports whether a is one of the twenty closed actions
// (thirteen v0.4–v0.7 acts plus the seven v0.8 evidence-lifecycle acts).
func IsValidReceiptAction(a ReceiptAction) bool {
	switch a {
	case ReceiptActionMemoryRecorded, ReceiptActionMemoryApproved,
		ReceiptActionMemoryRejected, ReceiptActionMemoryVoided,
		ReceiptActionRelationConfirmed, ReceiptActionRelationRejected,
		ReceiptActionEvidenceLinked, ReceiptActionMemorySuperseded,
		ReceiptActionMemoryClosed, ReceiptActionMemoryReopened,
		ReceiptActionReconciliationConfirmed, ReceiptActionReconciliationRejected,
		ReceiptActionObjectStored,
		ReceiptActionRetentionBound, ReceiptActionPurgeRequested,
		ReceiptActionPurgeApproved, ReceiptActionPurgeRejected,
		ReceiptActionPurgeCancelled, ReceiptActionPurgeWithdrawn,
		ReceiptActionPurgeExecuted:
		return true
	}
	return false
}

// ReceiptPayloadVersion is the frozen payload version stamped on the eight
// original v0.4.0 actions (memory_recorded … memory_superseded): their receipts
// are byte-identical to the pre-v0.5 protocol.
const ReceiptPayloadVersion = "receipt-payload/v0.4.0"

// ReceiptPayloadVersionV05 is the payload version stamped on the v0.5.0
// actions (memory_closed, memory_reopened, reconciliation_confirmed,
// reconciliation_rejected). Canonicalization is version-agnostic
// (the payload shape is unchanged — the snapshot is covered via the resulting
// envelope hash), so verifiers accept both v0.4.0 and v0.5.0 payloads unchanged
// (design §2.5: “verifiers continue accepting v0.4”).
const ReceiptPayloadVersionV05 = "receipt-payload/v0.5.0"

// ReceiptPayloadVersionV07 is the payload version stamped on the v0.7.0 action
// (object_stored). Canonicalization is version-agnostic (the payload SHAPE is
// unchanged — the object identity rides the existing evidenceRef field, the
// scope rides the existing tenant/company/fiscalPeriod fields and the claimed
// actor rides principalId), so verifiers keep accepting v0.4.0/v0.5.0 payloads
// unchanged AND accept v0.7.0 payloads without a protocol break (design §5 —
// the versioned protocol decision). Existing receipts never re-version.
const ReceiptPayloadVersionV07 = "receipt-payload/v0.7.0"

// ReceiptAlgorithm is the frozen signing algorithm.
const ReceiptAlgorithm = "Ed25519"

// ──────────────────────────────────────────────
// Model — frozen envelope + canonical payload
// ──────────────────────────────────────────────

// SignedReceipt is the frozen signed envelope. Field ORDER is part of the byte
// contract (see CanonicalUnsignedEnvelope/CompleteReceiptBytes). Signature is
// PADDED BASE64 in the model (raw bytes live in SQLite); previousReceiptHash is
// the digest of the prior complete canonical signed receipt for the same
// subject (genesis is empty). Algorithm is exactly "Ed25519".
type SignedReceipt struct {
	SubjectType         SubjectType   `json:"subjectType"`
	SubjectID           string        `json:"subjectId"`
	Action              ReceiptAction `json:"action"`
	TenantID            string        `json:"tenantId"`
	CompanyID           string        `json:"companyId"`
	FiscalPeriodID      string        `json:"fiscalPeriodId"`
	PayloadHash         string        `json:"payloadHash"`
	PreviousReceiptHash string        `json:"previousReceiptHash"`
	PrincipalID         string        `json:"principalId"`
	MembershipID        string        `json:"membershipId"`
	PolicyVersion       string        `json:"policyVersion"`
	Algorithm           string        `json:"algorithm"`
	KeyID               string        `json:"keyId"`
	Signature           string        `json:"signature"`
	IssuedAt            string        `json:"issuedAt"`
}

// ReceiptPayload is the canonical payload of a receipt. EVERY key is present in
// this exact order — inapplicable fields are EMPTY, never omitted; there are no
// optional fields, maps or nulls. Payload scope, principal, policy, timestamp,
// subject and action equal the envelope (VerifyReceipt enforces it). Roles are
// sorted and deduplicated canonically.
type ReceiptPayload struct {
	Version                  string        `json:"version"`
	SubjectType              SubjectType   `json:"subjectType"`
	SubjectID                string        `json:"subjectId"`
	Action                   ReceiptAction `json:"action"`
	TenantID                 string        `json:"tenantId"`
	CompanyID                string        `json:"companyId"`
	FiscalPeriodID           string        `json:"fiscalPeriodId"`
	ReviewedEnvelopeHash     string        `json:"reviewedEnvelopeHash"`
	ResultingEnvelopeHash    string        `json:"resultingEnvelopeHash"`
	ReviewedJudgmentHash     string        `json:"reviewedJudgmentHash"`
	ResultingJudgmentHash    string        `json:"resultingJudgmentHash"`
	FromMemoryID             string        `json:"fromMemoryId"`
	FromEnvelopeHash         string        `json:"fromEnvelopeHash"`
	ToMemoryID               string        `json:"toMemoryId"`
	ToEnvelopeHash           string        `json:"toEnvelopeHash"`
	SuccessorID              string        `json:"successorId"`
	EvidenceRef              string        `json:"evidenceRef"`
	Reason                   string        `json:"reason"`
	PrincipalID              string        `json:"principalId"`
	MembershipID             string        `json:"membershipId"`
	PrincipalRoles           []string      `json:"principalRoles"`
	AuthenticationMethod     string        `json:"authenticationMethod"`
	AssuranceLevel           string        `json:"assuranceLevel"`
	PrincipalAuthenticatedAt string        `json:"principalAuthenticatedAt"`
	PolicyVersion            string        `json:"policyVersion"`
	IssuedAt                 string        `json:"issuedAt"`
}

// ──────────────────────────────────────────────
// Typed verification errors (receipt protocol)
// ──────────────────────────────────────────────

// Frozen receipt error codes. VerifyReceipt NEVER claims accounting
// correctness — it only asserts integrity and signer possession.
const (
	CodeReceiptInvalid             = "RECEIPT_INVALID"
	CodeReceiptPayloadHashMismatch = "RECEIPT_PAYLOAD_HASH_MISMATCH"
	CodeReceiptKeyMismatch         = "RECEIPT_KEY_MISMATCH"
	CodeReceiptSignatureInvalid    = "RECEIPT_SIGNATURE_INVALID"
)

// ReceiptError is the typed verification error: a frozen code plus a human
// message. errors.Is matches by code (sentinel semantics, same shape as
// auth.Error).
type ReceiptError struct {
	Code    string
	Message string
}

func (e *ReceiptError) Error() string { return e.Code + ": " + e.Message }

// Is makes every *ReceiptError compare equal, via errors.Is, to any other
// *ReceiptError with the same code.
func (e *ReceiptError) Is(target error) bool {
	t, ok := target.(*ReceiptError)
	if !ok {
		return false
	}
	return e.Code == t.Code
}

// ReceiptCode returns the frozen receipt error code carried by err, or "" when
// err is nil or not a *ReceiptError.
func ReceiptCode(err error) string {
	if err == nil {
		return ""
	}
	var e *ReceiptError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// receiptErr returns a typed receipt error carrying the frozen code.
func receiptErr(code, format string, args ...any) error {
	return &ReceiptError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ──────────────────────────────────────────────
// Canonicalization (the byte contract)
// ──────────────────────────────────────────────

// canonicalReceiptPayload is the canonical JSON shape of a receipt payload: the
// struct field order IS the property order (Go marshals in declaration order).
// No omitempty — every key present, empty strings stay "". The roles array is
// always a JSON array (never null) and is canonicalized (sorted, deduplicated,
// empty strings dropped).
type canonicalReceiptPayload ReceiptPayload

// canonicalRoles returns a defensive copy of roles as a sorted, deduplicated
// set with empty strings dropped — the canonical order the payload covers (Go
// and TS bytes match; the contract must not depend on the caller's ordering).
func canonicalRoles(roles []string) []string {
	seen := make(map[string]struct{}, len(roles))
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		if r == "" {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// CanonicalReceiptPayload returns the canonical compact UTF-8 JSON bytes of a
// receipt payload: fixed property order (exactly the struct field order above),
// JSON string escaping, NO HTML escaping (matching canonicalJSON in
// judgment.go — Go escapes <,>,& by default, the mirror does not; disabling it
// keeps Go and TS bytes identical). Roles are canonicalized (sorted +
// deduplicated). Maps, nulls and optional properties are forbidden.
func CanonicalReceiptPayload(p ReceiptPayload) []byte {
	canonical := canonicalReceiptPayload(p)
	canonical.PrincipalRoles = canonicalRoles(p.PrincipalRoles)
	return canonicalJSONBytes(canonical)
}

// canonicalUnsignedEnvelope is the canonical JSON shape of the UNSIGNED
// envelope: the design's exact order (signature NOT included). Ed25519 signs
// these bytes, transitively signing the payload (payloadHash is part of the
// envelope).
type canonicalUnsignedEnvelope struct {
	SubjectType         SubjectType   `json:"subjectType"`
	SubjectID           string        `json:"subjectId"`
	Action              ReceiptAction `json:"action"`
	TenantID            string        `json:"tenantId"`
	CompanyID           string        `json:"companyId"`
	FiscalPeriodID      string        `json:"fiscalPeriodId"`
	PayloadHash         string        `json:"payloadHash"`
	PreviousReceiptHash string        `json:"previousReceiptHash"`
	PrincipalID         string        `json:"principalId"`
	MembershipID        string        `json:"membershipId"`
	PolicyVersion       string        `json:"policyVersion"`
	Algorithm           string        `json:"algorithm"`
	KeyID               string        `json:"keyId"`
	IssuedAt            string        `json:"issuedAt"`
}

// canonicalCompleteReceipt is the canonical JSON shape of the COMPLETE signed
// receipt: identical to the unsigned envelope with signature between keyId and
// issuedAt. ReceiptHash digests these bytes (previousReceiptHash chains on it).
type canonicalCompleteReceipt struct {
	SubjectType         SubjectType   `json:"subjectType"`
	SubjectID           string        `json:"subjectId"`
	Action              ReceiptAction `json:"action"`
	TenantID            string        `json:"tenantId"`
	CompanyID           string        `json:"companyId"`
	FiscalPeriodID      string        `json:"fiscalPeriodId"`
	PayloadHash         string        `json:"payloadHash"`
	PreviousReceiptHash string        `json:"previousReceiptHash"`
	PrincipalID         string        `json:"principalId"`
	MembershipID        string        `json:"membershipId"`
	PolicyVersion       string        `json:"policyVersion"`
	Algorithm           string        `json:"algorithm"`
	KeyID               string        `json:"keyId"`
	Signature           string        `json:"signature"`
	IssuedAt            string        `json:"issuedAt"`
}

// canonicalJSONBytes marshals v to compact UTF-8 JSON bytes WITHOUT HTML
// escaping, matching canonicalJSON (judgment.go). The protocol objects are
// fixed to strings plus the roles array, so Encode cannot fail; a failure is an
// internal invariant violation and fails closed via panic.
func canonicalJSONBytes(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(fmt.Sprintf("receipt: canonical marshal failed: %v", err))
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// CanonicalUnsignedEnvelope returns the compact UTF-8 JSON bytes Ed25519 signs:
// subjectType, subjectId, action, tenantId, companyId, fiscalPeriodId,
// payloadHash, previousReceiptHash, principalId, membershipId, policyVersion,
// algorithm, keyId, issuedAt (the design's order — signature NOT included).
func CanonicalUnsignedEnvelope(r SignedReceipt) []byte {
	return canonicalJSONBytes(canonicalUnsignedEnvelope{
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
		IssuedAt:            r.IssuedAt,
	})
}

// CompleteReceiptBytes returns the compact UTF-8 JSON bytes of the complete
// signed receipt: identical to the unsigned envelope with signature (padded
// base64) between keyId and issuedAt. ReceiptHash digests these bytes; the
// next receipt for the same subject chains on that digest via
// previousReceiptHash.
func CompleteReceiptBytes(r SignedReceipt) []byte {
	return canonicalJSONBytes(canonicalCompleteReceipt{
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
		Signature:           r.Signature,
		IssuedAt:            r.IssuedAt,
	})
}

// ──────────────────────────────────────────────
// Digests and key ids
// ──────────────────────────────────────────────

// ReceiptPayloadHash is the lowercase SHA-256 hex of the canonical payload
// bytes — the digest the envelope's payloadHash field carries.
func ReceiptPayloadHash(p ReceiptPayload) string {
	return sha256HexBytes(CanonicalReceiptPayload(p))
}

// ReceiptHash is the lowercase SHA-256 hex of the complete canonical signed
// receipt bytes. previousReceiptHash of the NEXT receipt for the same subject
// chains on this digest (genesis is empty).
func ReceiptHash(r SignedReceipt) string {
	return sha256HexBytes(CompleteReceiptBytes(r))
}

// ReceiptKeyID derives the canonical key id: "ed25519:" plus the full SHA-256
// hexadecimal digest of the RAW public key (never truncated).
func ReceiptKeyID(publicKey []byte) string {
	return "ed25519:" + sha256HexBytes(publicKey)
}

func sha256HexBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ──────────────────────────────────────────────
// Minimal verification (Step 3)
// ──────────────────────────────────────────────

// VerifyReceipt is the MINIMAL fail-closed verification of a receipt against
// its payload and the signer's raw public key. It checks, in order:
//
//  1. closed enums (subjectType, action, algorithm — unknown fails closed);
//  2. payload/envelope equality (payload scope, subject, action, principal,
//     policy and timestamp fields equal the envelope);
//  3. payloadHash matches the canonical payload digest;
//  4. keyId equals "ed25519:" + SHA-256 hex of the raw public key;
//  5. the Ed25519 signature over the reconstructed unsigned envelope.
//
// It proves INTEGRITY and SIGNER POSSESSION only. Chain traversal, key
// lookup/revocation, principal provenance, evidence/rule availability and the
// verification CLI remain Step 4. Accounting correctness: NOT ASSERTED.
// Typed codes: RECEIPT_INVALID, RECEIPT_PAYLOAD_HASH_MISMATCH,
// RECEIPT_KEY_MISMATCH, RECEIPT_SIGNATURE_INVALID.
func VerifyReceipt(r SignedReceipt, payload ReceiptPayload, publicKey []byte) error {
	if !IsValidSubjectType(r.SubjectType) {
		return receiptErr(CodeReceiptInvalid, "unknown subjectType %q — expected memory|judgment|reconciliation|evidence_object", r.SubjectType)
	}
	if !IsValidReceiptAction(r.Action) {
		return receiptErr(CodeReceiptInvalid, "unknown action %q — the action set is closed", r.Action)
	}
	if r.Algorithm != ReceiptAlgorithm {
		return receiptErr(CodeReceiptInvalid, "algorithm %q — expected %q", r.Algorithm, ReceiptAlgorithm)
	}
	if err := verifyPayloadEnvelopeEquality(r, payload); err != nil {
		return err
	}
	if r.PayloadHash != ReceiptPayloadHash(payload) {
		return receiptErr(CodeReceiptPayloadHashMismatch, "payloadHash %q does not match the canonical payload digest %q", r.PayloadHash, ReceiptPayloadHash(payload))
	}
	if r.KeyID != ReceiptKeyID(publicKey) {
		return receiptErr(CodeReceiptKeyMismatch, "keyId %q does not match the supplied public key (%q)", r.KeyID, ReceiptKeyID(publicKey))
	}
	sig, err := base64.StdEncoding.DecodeString(r.Signature)
	if err != nil {
		return receiptErr(CodeReceiptInvalid, "signature is not valid padded base64: %v", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return receiptErr(CodeReceiptSignatureInvalid, "signature length %d — expected %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(publicKey, CanonicalUnsignedEnvelope(r), sig) {
		return receiptErr(CodeReceiptSignatureInvalid, "Ed25519 signature verification failed over the unsigned envelope")
	}
	return nil
}

// verifyPayloadEnvelopeEquality enforces the design invariant that payload
// scope, principal, policy, timestamp, subject and action equal the envelope.
func verifyPayloadEnvelopeEquality(r SignedReceipt, payload ReceiptPayload) error {
	if payload.SubjectType != r.SubjectType {
		return receiptErr(CodeReceiptInvalid, "payload subjectType %q differs from envelope %q", payload.SubjectType, r.SubjectType)
	}
	if payload.SubjectID != r.SubjectID {
		return receiptErr(CodeReceiptInvalid, "payload subjectId %q differs from envelope %q", payload.SubjectID, r.SubjectID)
	}
	if payload.Action != r.Action {
		return receiptErr(CodeReceiptInvalid, "payload action %q differs from envelope %q", payload.Action, r.Action)
	}
	if payload.TenantID != r.TenantID {
		return receiptErr(CodeReceiptInvalid, "payload tenantId %q differs from envelope %q", payload.TenantID, r.TenantID)
	}
	if payload.CompanyID != r.CompanyID {
		return receiptErr(CodeReceiptInvalid, "payload companyId %q differs from envelope %q", payload.CompanyID, r.CompanyID)
	}
	if payload.FiscalPeriodID != r.FiscalPeriodID {
		return receiptErr(CodeReceiptInvalid, "payload fiscalPeriodId %q differs from envelope %q", payload.FiscalPeriodID, r.FiscalPeriodID)
	}
	if payload.PrincipalID != r.PrincipalID {
		return receiptErr(CodeReceiptInvalid, "payload principalId %q differs from envelope %q", payload.PrincipalID, r.PrincipalID)
	}
	if payload.MembershipID != r.MembershipID {
		return receiptErr(CodeReceiptInvalid, "payload membershipId %q differs from envelope %q", payload.MembershipID, r.MembershipID)
	}
	if payload.PolicyVersion != r.PolicyVersion {
		return receiptErr(CodeReceiptInvalid, "payload policyVersion %q differs from envelope %q", payload.PolicyVersion, r.PolicyVersion)
	}
	if payload.IssuedAt != r.IssuedAt {
		return receiptErr(CodeReceiptInvalid, "payload issuedAt %q differs from envelope %q", payload.IssuedAt, r.IssuedAt)
	}
	return nil
}
