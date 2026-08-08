// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the OFFLINE verification
// engine (v0.4.0 Step 4 — see docs/architecture/offline-verification-step4.md).
//
// Verification is READ-ONLY over local SQLite: no network, no remote key
// service, no HTTP/MCP dependency. The PURE layer semantics and the report
// contract live here; all I/O orchestration lives in
// internal/server/verify_service.go; the TypeScript mirror (core/verify.ts,
// next commit) mirrors ONLY this pure logic and the report shape.
//
// The engine establishes canonical encoding, ledger/envelope integrity,
// signer/key timing, provenance continuity, scope consistency, chain
// continuity and referenced-state availability. It does NOT establish evidence
// truth, correct rule interpretation, professional soundness or accounting
// correctness. Every report ends with the exact conclusion
// `Accounting correctness: NOT ASSERTED`, regardless of the outcome.
//
// This module is PURE: no I/O, no store, no keyring. It reuses the receipt
// canonicalizers and cryptographic primitives from receipt.go and the judgment
// hash from judgment.go.
package core

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ──────────────────────────────────────────────
// Report contract (Go↔TS mirror, design §2)
// ──────────────────────────────────────────────

// VerificationStatus is the per-layer result of one check instance.
type VerificationStatus string

const (
	// VerificationPassed marks a check that established its claim.
	VerificationPassed VerificationStatus = "passed"
	// VerificationFailed marks a check that found a violation.
	VerificationFailed VerificationStatus = "failed"
	// VerificationSkipped marks a dependency-blocked or inapplicable check.
	VerificationSkipped VerificationStatus = "skipped"
)

// VerificationOutcome is the report-level conclusion: passed only when every
// applicable layer passes.
type VerificationOutcome string

const (
	VerificationOutcomePassed VerificationOutcome = "passed"
	VerificationOutcomeFailed VerificationOutcome = "failed"
)

// AccountingCorrectnessNotAsserted is the exact conclusion EVERY report ends
// with (design §1 decision 5 and §5). Verification never asserts accounting
// correctness; the constant is always populated LAST so the JSON closing brace
// follows it with no non-JSON trailer.
const AccountingCorrectnessNotAsserted = "Accounting correctness: NOT ASSERTED"

// VerificationLayer is one named check with its status and a deterministic
// detail string (Go and the TypeScript mirror must produce identical bytes).
type VerificationLayer struct {
	Name   string             `json:"name"`
	Status VerificationStatus `json:"status"`
	Detail string             `json:"detail"`
}

// ReceiptVerification is the per-receipt diagnostic block: the stored receipt
// hash, the covered action and the six receipt-layer instances.
type ReceiptVerification struct {
	ReceiptHash string              `json:"receiptHash"`
	Action      ReceiptAction       `json:"action"`
	Layers      []VerificationLayer `json:"layers"`
}

// VerificationReport is the complete offline verification result. The stable
// layer order is part of the Go↔TS fixture: the six receipt layers, then the
// subject-type object layers.
type VerificationReport struct {
	SubjectType           string                `json:"subjectType"`
	SubjectID             string                `json:"subjectId"`
	Outcome               VerificationOutcome   `json:"outcome"`
	Receipts              []ReceiptVerification `json:"receipts"`
	Layers                []VerificationLayer   `json:"layers"`
	AccountingCorrectness string                `json:"accountingCorrectness"`
}

// Stable layer names — EXACT strings shared with the TypeScript mirror
// (design §2). Receipt order: payload canonicalization, envelope integrity,
// signature, signing-key validity, tenant/company scope, chain link. Memory
// then appends principal provenance, supersession chain, evidence availability
// and rule availability; judgment appends principal provenance, judgment hash
// and supersession chain.
const (
	LayerPayloadCanonicalization = "payload canonicalization"
	LayerEnvelopeIntegrity       = "envelope integrity"
	LayerSignature               = "signature"
	LayerSigningKeyValidity      = "signing-key validity"
	LayerTenantCompanyScope      = "tenant/company scope"
	LayerChainLink               = "chain link"
	LayerPrincipalProvenance     = "principal provenance"
	LayerSupersessionChain       = "supersession chain"
	LayerEvidenceAvailability    = "evidence availability"
	LayerObjectAvailability      = "object availability"
	LayerRuleAvailability        = "rule availability"
	LayerJudgmentHash            = "judgment hash"
)

// ReceiptLayerNames returns the six receipt-layer names in the stable order —
// the per-receipt order AND the first six top-level aggregate layers.
func ReceiptLayerNames() []string {
	return []string{
		LayerPayloadCanonicalization,
		LayerEnvelopeIntegrity,
		LayerSignature,
		LayerSigningKeyValidity,
		LayerTenantCompanyScope,
		LayerChainLink,
	}
}

// ──────────────────────────────────────────────
// Pure verification inputs (store-independent values)
// ──────────────────────────────────────────────

// SigningKey is the pure verification view of a registered public signing key
// (the store's SigningKeyRecord stripped of store types). PublicKey is padded
// base64 of the RAW public key.
type SigningKey struct {
	Found     bool
	Algorithm string
	PublicKey string
	CreatedAt string
	RevokedAt string
}

// SubjectScope is the stored fiscal scope of a subject (memory or judgment) —
// what the tenant/company scope layer compares the envelope and payload
// against. Standalone receipt verification loads its FK-backed subject, so the
// check is never mere self-consistency.
type SubjectScope struct {
	TenantID       string
	CompanyID      string
	FiscalPeriodID string
}

// ActProvenance is the immutable event snapshot a covered-act receipt must
// match (design §3 principal provenance). Verified acts (memory_approved,
// relation_confirmed, relation_rejected) fill the complete snapshot; claimed
// acts (memory_recorded, memory_rejected, memory_voided, memory_superseded,
// evidence_linked) fill Action, Timestamp and PrincipalID only — the pure layer
// compares the claimed principalId with the immutable act actor/source only,
// proving attribution continuity, never authorization.
type ActProvenance struct {
	Action    string
	Timestamp string
	// Attribution: the actor recorded by the immutable event.
	PrincipalID string
	// Verified-act snapshot fields (empty for claimed acts).
	MembershipID          string
	Roles                 []string
	AuthenticationMethod  string
	AssuranceLevel        string
	AuthenticatedAt       string
	Policy                string
	Reason                string
	ReviewedEnvelopeHash  string
	ResultingEnvelopeHash string
	// RecordedJudgmentHash is the judgment_hash the immutable decision event
	// recorded (the store records the resulting state hash, design §3 — see the
	// confirm/reject event insert).
	RecordedJudgmentHash string
}

// SupersessionLink is one step of the supersession walk: the subject row, its
// successor routing ("" for the terminal step) and the stored scope. Superseded
// reports whether the row status IS superseded — the status/relation agreement
// check needs it.
type SupersessionLink struct {
	SubjectID   string
	SuccessorID string
	Superseded  bool
	Scope       SubjectScope
}

// ReceiptTarget selects one receipt for standalone verification: Hash is the
// portable 64-hex identity, ID the local SQLite row id (local convenience).
// Exactly one must be set.
type ReceiptTarget struct {
	Hash string
	ID   int64
}

// ──────────────────────────────────────────────
// Layer status helpers
// ──────────────────────────────────────────────

func layerPassed(name, detail string) VerificationLayer {
	return VerificationLayer{Name: name, Status: VerificationPassed, Detail: detail}
}

func layerFailed(name, detail string) VerificationLayer {
	return VerificationLayer{Name: name, Status: VerificationFailed, Detail: detail}
}

func layerSkipped(name, detail string) VerificationLayer {
	return VerificationLayer{Name: name, Status: VerificationSkipped, Detail: detail}
}

// ──────────────────────────────────────────────
// Payload canonicalization
// ──────────────────────────────────────────────

// DecodeStoredPayload strict-decodes exactly one ReceiptPayload from the stored
// canonical JSON: unknown fields and trailing data are rejected. The canonical
// payload is the authoritative signed input — a parse failure is corruption,
// never a successful skip (design §1 decision 3).
func DecodeStoredPayload(payloadJSON string) (ReceiptPayload, error) {
	dec := json.NewDecoder(strings.NewReader(payloadJSON))
	dec.DisallowUnknownFields()
	var p ReceiptPayload
	if err := dec.Decode(&p); err != nil {
		return ReceiptPayload{}, fmt.Errorf("stored payload_json is not a valid canonical receipt payload: %w", err)
	}
	if dec.More() {
		return ReceiptPayload{}, errors.New("stored payload_json carries trailing data after the payload object")
	}
	return p, nil
}

// VerifyPayloadCanonicalization checks the stored payload bytes against the
// canonical re-marshal (byte equality rejects alternate key order, whitespace,
// duplicate/unknown keys and non-canonical escaping) and the envelope's
// payloadHash against the canonical payload digest.
func VerifyPayloadCanonicalization(payloadJSON string, payload ReceiptPayload, receipt SignedReceipt) VerificationLayer {
	if payloadJSON != string(CanonicalReceiptPayload(payload)) {
		return layerFailed(LayerPayloadCanonicalization, "stored payload_json differs from the canonical payload bytes (alternate key order, whitespace, duplicate/unknown keys or non-canonical escaping)")
	}
	if got := ReceiptPayloadHash(payload); got != receipt.PayloadHash {
		return layerFailed(LayerPayloadCanonicalization, fmt.Sprintf("payloadHash %s does not match the canonical payload digest %s", receipt.PayloadHash, got))
	}
	return layerPassed(LayerPayloadCanonicalization, "payload is canonical and payloadHash matches the canonical digest")
}

// ──────────────────────────────────────────────
// Envelope integrity
// ──────────────────────────────────────────────

// VerifyEnvelopeIntegrity validates the closed enums, the duplicated
// payload/envelope fields and the recomputed receipt digest against the stored
// receipt_hash. The digest comparison is byte-exact: any envelope mutation
// (chain, scope, principal, timestamp, signature field) changes ReceiptHash.
func VerifyEnvelopeIntegrity(receipt SignedReceipt, payload ReceiptPayload, storedHash string) VerificationLayer {
	if !IsValidSubjectType(receipt.SubjectType) {
		return layerFailed(LayerEnvelopeIntegrity, fmt.Sprintf("unknown subjectType %q — expected memory|judgment", receipt.SubjectType))
	}
	if !IsValidReceiptAction(receipt.Action) {
		return layerFailed(LayerEnvelopeIntegrity, fmt.Sprintf("unknown action %q — the action set is closed", receipt.Action))
	}
	if receipt.Algorithm != ReceiptAlgorithm {
		return layerFailed(LayerEnvelopeIntegrity, fmt.Sprintf("algorithm %q — expected %q", receipt.Algorithm, ReceiptAlgorithm))
	}
	if err := verifyPayloadEnvelopeEquality(receipt, payload); err != nil {
		return layerFailed(LayerEnvelopeIntegrity, err.Error())
	}
	if computed := ReceiptHash(receipt); computed != storedHash {
		return layerFailed(LayerEnvelopeIntegrity, fmt.Sprintf("recomputed receipt hash %s differs from the stored receipt_hash %s", computed, storedHash))
	}
	return layerPassed(LayerEnvelopeIntegrity, "envelope is integral and the stored receipt_hash matches the recomputed digest")
}

// ──────────────────────────────────────────────
// Signature and signing-key validity
// ──────────────────────────────────────────────

// DecodeSigningPublicKey returns the raw Ed25519 public key bytes of a stored
// signing-key record, or nil when the key material is missing or invalid —
// the dependency gate of the signature layer (missing/invalid material skips
// the signature check with the failed signing-key prerequisite named).
func DecodeSigningPublicKey(key SigningKey) []byte {
	if !key.Found || key.Algorithm != ReceiptAlgorithm {
		return nil
	}
	pub, err := base64.StdEncoding.DecodeString(key.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil
	}
	return pub
}

// VerifySignature checks the padded-base64 Ed25519 signature over the
// canonical unsigned envelope. rawKey is the DECODED public key; nil skips the
// layer naming the failed signing-key prerequisite (design §3: a dependency-
// blocked check may be skipped, the prerequisite still fails the report).
func VerifySignature(receipt SignedReceipt, rawKey []byte) VerificationLayer {
	if rawKey == nil {
		return layerSkipped(LayerSignature, "skipped: prerequisite 'signing-key validity' failed (key material missing or invalid)")
	}
	sig, err := base64.StdEncoding.DecodeString(receipt.Signature)
	if err != nil {
		return layerFailed(LayerSignature, "signature is not valid padded base64")
	}
	if len(sig) != ed25519.SignatureSize {
		return layerFailed(LayerSignature, fmt.Sprintf("signature length %d — expected %d", len(sig), ed25519.SignatureSize))
	}
	if !ed25519.Verify(rawKey, CanonicalUnsignedEnvelope(receipt), sig) {
		return layerFailed(LayerSignature, "Ed25519 signature verification failed over the canonical unsigned envelope")
	}
	return layerPassed(LayerSignature, "Ed25519 signature verifies over the canonical unsigned envelope")
}

// VerifySigningKeyValidity checks the stored key row: the key exists, the
// algorithm is Ed25519, the base64 decodes to a raw Ed25519 public key,
// ReceiptKeyID(rawKey) equals the receipt keyId, created_at <= issued_at, and
// revoked_at is empty or issued_at < revoked_at. Issued before revocation
// passes; issued at/after revocation fails. Protocol timestamps must parse as
// RFC3339.
func VerifySigningKeyValidity(key SigningKey, receipt SignedReceipt) VerificationLayer {
	if !key.Found {
		return layerFailed(LayerSigningKeyValidity, fmt.Sprintf("signing key %s is not registered", receipt.KeyID))
	}
	if key.Algorithm != ReceiptAlgorithm {
		return layerFailed(LayerSigningKeyValidity, fmt.Sprintf("signing key %s algorithm %q — expected %q", receipt.KeyID, key.Algorithm, ReceiptAlgorithm))
	}
	pub, err := base64.StdEncoding.DecodeString(key.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return layerFailed(LayerSigningKeyValidity, fmt.Sprintf("signing key %s public key is not valid padded-base64 Ed25519 material", receipt.KeyID))
	}
	if derived := ReceiptKeyID(pub); derived != receipt.KeyID {
		return layerFailed(LayerSigningKeyValidity, fmt.Sprintf("keyId %s does not match the stored public key (%s)", receipt.KeyID, derived))
	}
	created, err := time.Parse(time.RFC3339, key.CreatedAt)
	if err != nil {
		return layerFailed(LayerSigningKeyValidity, fmt.Sprintf("signing key %s created_at %q is not a valid RFC3339 timestamp", receipt.KeyID, key.CreatedAt))
	}
	issued, err := time.Parse(time.RFC3339, receipt.IssuedAt)
	if err != nil {
		return layerFailed(LayerSigningKeyValidity, fmt.Sprintf("receipt issued_at %q is not a valid RFC3339 timestamp", receipt.IssuedAt))
	}
	if created.After(issued) {
		return layerFailed(LayerSigningKeyValidity, fmt.Sprintf("signing key %s was created at %s, after the receipt issuance at %s", receipt.KeyID, key.CreatedAt, receipt.IssuedAt))
	}
	if key.RevokedAt != "" {
		revoked, err := time.Parse(time.RFC3339, key.RevokedAt)
		if err != nil {
			return layerFailed(LayerSigningKeyValidity, fmt.Sprintf("signing key %s revoked_at %q is not a valid RFC3339 timestamp", receipt.KeyID, key.RevokedAt))
		}
		if !issued.Before(revoked) {
			return layerFailed(LayerSigningKeyValidity, fmt.Sprintf("signing key %s was revoked at %s; issuance at %s is at/after revocation", receipt.KeyID, key.RevokedAt, receipt.IssuedAt))
		}
	}
	return layerPassed(LayerSigningKeyValidity, "signing key is registered, matches the keyId and its validity window covers the issuance")
}

// ──────────────────────────────────────────────
// Tenant/company scope
// ──────────────────────────────────────────────

// VerifyTenantCompanyScope requires the payload and the envelope to equal the
// stored subject's tenant/company/fiscal period. Envelope==payload equality is
// enforced by the envelope-integrity layer; this layer anchors BOTH to the
// stored subject, so it is never mere self-consistency (design §3).
func VerifyTenantCompanyScope(receipt SignedReceipt, payload ReceiptPayload, subject SubjectScope) VerificationLayer {
	if receipt.TenantID != subject.TenantID || payload.TenantID != subject.TenantID {
		return layerFailed(LayerTenantCompanyScope, fmt.Sprintf("tenantId %q/%q differs from the stored subject tenant %q", receipt.TenantID, payload.TenantID, subject.TenantID))
	}
	if receipt.CompanyID != subject.CompanyID || payload.CompanyID != subject.CompanyID {
		return layerFailed(LayerTenantCompanyScope, fmt.Sprintf("companyId %q/%q differs from the stored subject company %q", receipt.CompanyID, payload.CompanyID, subject.CompanyID))
	}
	if receipt.FiscalPeriodID != subject.FiscalPeriodID || payload.FiscalPeriodID != subject.FiscalPeriodID {
		return layerFailed(LayerTenantCompanyScope, fmt.Sprintf("fiscalPeriodId %q/%q differs from the stored subject fiscal period %q", receipt.FiscalPeriodID, payload.FiscalPeriodID, subject.FiscalPeriodID))
	}
	return layerPassed(LayerTenantCompanyScope, "payload, envelope and the stored subject share the same tenant/company/fiscal period")
}

// ──────────────────────────────────────────────
// Chain link
// ──────────────────────────────────────────────

// VerifyChainLink checks the receipt's previousReceiptHash against the
// immediately preceding COMPUTED receipt hash of the same subject chain (empty
// for genesis). prevComputedHash is the recomputed digest of the previous
// receipt in the full chain, or of the resolved standalone predecessor;
// predecessorResolved distinguishes "genesis with an empty previous hash" from
// "a non-genesis predecessor that could not be resolved". Every stored hash
// equals its computed digest via the envelope-integrity layer.
func VerifyChainLink(receipt SignedReceipt, prevComputedHash string, predecessorResolved bool) VerificationLayer {
	if receipt.PreviousReceiptHash == "" {
		if prevComputedHash == "" {
			return layerPassed(LayerChainLink, "genesis receipt has an empty previousReceiptHash")
		}
		return layerFailed(LayerChainLink, "chain gap: previousReceiptHash is empty but the receipt is not first in the chain")
	}
	if !predecessorResolved {
		return layerFailed(LayerChainLink, fmt.Sprintf("previousReceiptHash %s does not resolve to a stored predecessor receipt", receipt.PreviousReceiptHash))
	}
	if prevComputedHash == "" {
		return layerFailed(LayerChainLink, "genesis receipt must have an empty previousReceiptHash")
	}
	if receipt.PreviousReceiptHash != prevComputedHash {
		return layerFailed(LayerChainLink, fmt.Sprintf("previousReceiptHash %s does not reference the immediately preceding receipt hash %s", receipt.PreviousReceiptHash, prevComputedHash))
	}
	return layerPassed(LayerChainLink, "chain link references the immediately preceding receipt hash")
}

// ──────────────────────────────────────────────
// Principal provenance
// ──────────────────────────────────────────────

// VerifyPrincipalProvenance matches a covered-act receipt against its immutable
// event snapshot. Verified acts (memory_approved, relation_confirmed,
// relation_rejected) compare the complete snapshot: principal, membership,
// roles, authentication, assurance, authenticated-at, policy, reason, hashes,
// action and timestamp. Claimed acts compare principalId with the immutable act
// actor/source ONLY — attribution continuity, never authorization (design §3).
func VerifyPrincipalProvenance(payload ReceiptPayload, act ActProvenance) VerificationLayer {
	switch payload.Action {
	case ReceiptActionMemoryApproved:
		if act.Action != "approved" {
			return layerFailed(LayerPrincipalProvenance, fmt.Sprintf("immutable event action %q differs from the claimed approval act", act.Action))
		}
		for _, cmp := range []struct {
			field string
			got   string
			want  string
		}{
			{"principalId", payload.PrincipalID, act.PrincipalID},
			{"membershipId", payload.MembershipID, act.MembershipID},
			{"authenticationMethod", payload.AuthenticationMethod, act.AuthenticationMethod},
			{"assuranceLevel", payload.AssuranceLevel, act.AssuranceLevel},
			{"principalAuthenticatedAt", payload.PrincipalAuthenticatedAt, act.AuthenticatedAt},
			{"policyVersion", payload.PolicyVersion, act.Policy},
			{"reason", payload.Reason, act.Reason},
			{"reviewedEnvelopeHash", payload.ReviewedEnvelopeHash, act.ReviewedEnvelopeHash},
			{"resultingEnvelopeHash", payload.ResultingEnvelopeHash, act.ResultingEnvelopeHash},
			{"issuedAt", payload.IssuedAt, act.Timestamp},
		} {
			if cmp.got != cmp.want {
				return provenanceFieldFail(cmp.field, cmp.got, cmp.want)
			}
		}
		if !equalStringSets(payload.PrincipalRoles, act.Roles) {
			return layerFailed(LayerPrincipalProvenance, "principal provenance mismatch: principalRoles differ from the immutable event roles")
		}
		return layerPassed(LayerPrincipalProvenance, "principal provenance matches the immutable approval event snapshot")

	case ReceiptActionRelationConfirmed, ReceiptActionRelationRejected:
		wantAction := "confirm"
		if payload.Action == ReceiptActionRelationRejected {
			wantAction = "reject"
		}
		if act.Action != wantAction {
			return layerFailed(LayerPrincipalProvenance, fmt.Sprintf("immutable event action %q differs from the claimed decision act", act.Action))
		}
		for _, cmp := range []struct {
			field string
			got   string
			want  string
		}{
			{"principalId", payload.PrincipalID, act.PrincipalID},
			{"membershipId", payload.MembershipID, act.MembershipID},
			{"authenticationMethod", payload.AuthenticationMethod, act.AuthenticationMethod},
			{"assuranceLevel", payload.AssuranceLevel, act.AssuranceLevel},
			{"principalAuthenticatedAt", payload.PrincipalAuthenticatedAt, act.AuthenticatedAt},
			{"policyVersion", payload.PolicyVersion, act.Policy},
			{"reason", payload.Reason, act.Reason},
			{"issuedAt", payload.IssuedAt, act.Timestamp},
		} {
			if cmp.got != cmp.want {
				return provenanceFieldFail(cmp.field, cmp.got, cmp.want)
			}
		}
		if !equalStringSets(payload.PrincipalRoles, act.Roles) {
			return layerFailed(LayerPrincipalProvenance, "principal provenance mismatch: principalRoles differ from the immutable event roles")
		}
		// The immutable event's recorded judgment hash must agree with the
		// committed result (the store records the resulting state hash; the
		// recompute-vs-current-row agreement lives in the judgment-hash layer).
		if payload.ResultingJudgmentHash != act.RecordedJudgmentHash {
			return provenanceFieldFail("resultingJudgmentHash", payload.ResultingJudgmentHash, act.RecordedJudgmentHash)
		}
		return layerPassed(LayerPrincipalProvenance, "principal provenance matches the immutable decision event snapshot")

	default:
		// Claimed acts: attribution continuity only.
		if payload.PrincipalID != act.PrincipalID {
			return layerFailed(LayerPrincipalProvenance, fmt.Sprintf("attribution mismatch: claimed principal %q differs from the immutable act actor %q", payload.PrincipalID, act.PrincipalID))
		}
		return layerPassed(LayerPrincipalProvenance, "attribution continuity: claimed principal matches the immutable act actor")
	}
}

func provenanceFieldFail(field, got, want string) VerificationLayer {
	return layerFailed(LayerPrincipalProvenance, fmt.Sprintf("principal provenance mismatch: %s %q differs from the immutable event %q", field, got, want))
}

// ──────────────────────────────────────────────
// Supersession chain
// ──────────────────────────────────────────────

// VerifySupersessionChain validates the full supersession walk from the subject
// to the terminal current row: fail on a missing referenced successor, a
// cycle, a cross-tenant/company link, or a disagreement between status and
// relation (a superseded row without a successor, or a successor link on a
// non-superseded row). The passed detail reports the terminal current id.
func VerifySupersessionChain(links []SupersessionLink, chainScope SubjectScope) VerificationLayer {
	if len(links) == 0 {
		return layerFailed(LayerSupersessionChain, "no subject row to walk — supersession chain cannot be established")
	}
	seen := make(map[string]struct{}, len(links))
	for i, link := range links {
		if link.Scope != chainScope {
			return layerFailed(LayerSupersessionChain, fmt.Sprintf("supersession link at %s crosses scope (tenant/company/fiscal period)", link.SubjectID))
		}
		if _, dup := seen[link.SubjectID]; dup {
			return layerFailed(LayerSupersessionChain, fmt.Sprintf("supersession cycle detected at %s", link.SubjectID))
		}
		seen[link.SubjectID] = struct{}{}
		if link.SuccessorID != "" {
			if !link.Superseded {
				return layerFailed(LayerSupersessionChain, fmt.Sprintf("status of %s disagrees with its successor relation to %s (row is not superseded)", link.SubjectID, link.SuccessorID))
			}
			continue
		}
		if link.Superseded {
			return layerFailed(LayerSupersessionChain, fmt.Sprintf("status of %s is superseded but no successor relation resolves (missing successor)", link.SubjectID))
		}
		if i != len(links)-1 {
			return layerFailed(LayerSupersessionChain, "the terminal subject is not the last step of the walk")
		}
	}
	terminal := links[len(links)-1].SubjectID
	return layerPassed(LayerSupersessionChain, fmt.Sprintf("supersession chain is current at %s", terminal))
}

// ──────────────────────────────────────────────
// Evidence / rule availability
// ──────────────────────────────────────────────

// VerifyEvidenceAvailability requires every non-empty receipt evidenceRef to
// have a current evidence_links row and the current memory envelope (rebuilt
// from immutable refs plus current links) to match the envelope the chain head
// committed. The row check plus the committed-envelope comparison detect
// direct-SQL link removal (design §3, AC7).
func VerifyEvidenceAvailability(declaredRefs, currentLinks []string, currentEnvelope, committedEnvelope string) VerificationLayer {
	for _, ref := range declaredRefs {
		if !containsString(currentLinks, ref) {
			return layerFailed(LayerEvidenceAvailability, fmt.Sprintf("evidence ref %s has no current evidence_links row (direct-SQL link removal or corruption)", ref))
		}
	}
	if currentEnvelope != committedEnvelope {
		return layerFailed(LayerEvidenceAvailability, fmt.Sprintf("current envelope %s differs from the committed head result %s", currentEnvelope, committedEnvelope))
	}
	return layerPassed(LayerEvidenceAvailability, "every declared evidence ref is linked and the current envelope matches the committed head result")
}

// VerifyObjectAvailability is the v0.7.0 object-level availability layer
// (docs/architecture/evidence-object-v0.7.md §6): it classifies every declared
// evidence ref as OBJECT-BACKED (the ref resolves to a stored EvidenceObject
// row) or LEGACY/UNRESOLVED (an arbitrary external reference — the pre-v0.7
// semantics, which stay fully backward compatible and are reported, never
// silently treated as bytes the engine can vouch for).
//
// refs is the deduplicated declared evidence-ref set (same input the evidence
// availability layer uses); resolved maps each ref that resolves to a stored
// evidence object to its metadata — the SERVICE resolves rows and verifies
// their WORM bytes before calling this layer (a resolved-but-corrupt object is
// a FAILED layer produced by VerifyObjectBytesIntegrity, never a passed one).
//
// Layer outcomes (fail closed, never break legacy data):
//   - skipped when there are no declared refs (object availability is not
//     applicable);
//   - skipped when NO declared ref resolves to an evidence object — every ref
//     is legacy/unresolved and is reported by name (backward compatible:
//     legacy data is never failed by the new layer);
//   - passed when every object-backed ref is present and byte-verified, with
//     any legacy refs named as left byte-unverified;
//   - failed ONLY via VerifyObjectBytesIntegrity for a resolved object whose
//     bytes are missing or re-hash to a different digest (corruption is
//     evidence, never a silent skip).
func VerifyObjectAvailability(refs []string, resolved map[string]EvidenceObject) VerificationLayer {
	refs = canonicalRefsList(refs)
	if len(refs) == 0 {
		return layerSkipped(LayerObjectAvailability, "no declared evidence refs — object availability not applicable")
	}
	legacy := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, ok := resolved[ref]; !ok {
			legacy = append(legacy, ref)
		}
	}
	if len(legacy) == len(refs) {
		return layerSkipped(LayerObjectAvailability,
			"no declared evidence ref resolves to a stored evidence object — legacy/unresolved refs stay backward compatible and byte-unverified: "+strings.Join(legacy, ", "))
	}
	detail := fmt.Sprintf("%d object-backed evidence refs resolve to stored objects with verified bytes", len(refs)-len(legacy))
	if len(legacy) > 0 {
		detail += "; legacy/unresolved refs left byte-unverified: " + strings.Join(legacy, ", ")
	}
	return layerPassed(LayerObjectAvailability, detail)
}

// VerifyObjectBytesIntegrity is the WORM byte-integrity layer: passed when the
// stored bytes of every object-backed ref re-hash to their content addresses,
// failed when err carries a corruption code (OBJECT_BYTES_MISSING |
// OBJECT_HASH_MISMATCH — the store fails closed, silent repair is forbidden).
// err == nil passes. A wrapped core.ErrObjectBytesPurgedExpected (missing bytes
// explained by a receipt-covered purge authorization — a committed execution or
// a valid purge intent) ALSO passes, with the purged-specific message: the
// documented expected absence is NOT an integrity violation. The error text
// identifies the failing object (the store wraps the typed corruption error with
// the object id).
func VerifyObjectBytesIntegrity(err error) VerificationLayer {
	if err == nil {
		return layerPassed(LayerObjectAvailability, "object WORM bytes re-hash to their stored content addresses")
	}
	if errors.Is(err, ErrObjectBytesPurgedExpected) {
		return layerPassed(LayerObjectAvailability, "object WORM bytes are absent by documented purge authorization (expected absence — a committed execution or a valid receipt-covered intent removed them; not an integrity violation)")
	}
	return layerFailed(LayerObjectAvailability, "object-backed evidence ref fails WORM byte integrity: "+err.Error())
}

// canonicalRefsList returns the sorted, deduplicated, non-empty ref set — the
// canonical order the pure layers classify (order-independent, like
// canonicalRefs in types.go).
func canonicalRefsList(refs []string) []string {
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

// VerifyRuleAvailability requires every dynamically declared rule ref (the
// merged refs beyond the immutable stored set) to be backed by a current
// rule_links row and the current envelope to match the committed head result.
// Since v5 has no rule_linked action, removal is detected by the
// committed-envelope mismatch, not by a new receipt action (design §3).
func VerifyRuleAvailability(storedRefs, currentLinks, mergedRefs []string, currentEnvelope, committedEnvelope string) VerificationLayer {
	for _, ref := range mergedRefs {
		if !containsString(storedRefs, ref) && !containsString(currentLinks, ref) {
			return layerFailed(LayerRuleAvailability, fmt.Sprintf("rule ref %s lacks a rule_links row (direct-SQL removal or corruption)", ref))
		}
	}
	if currentEnvelope != committedEnvelope {
		return layerFailed(LayerRuleAvailability, fmt.Sprintf("current envelope %s differs from the committed head result %s", currentEnvelope, committedEnvelope))
	}
	return layerPassed(LayerRuleAvailability, "rule refs are row-backed and the current envelope matches the committed head result")
}

// ──────────────────────────────────────────────
// Judgment hash
// ──────────────────────────────────────────────

// VerifyJudgmentHash compares the recomputed hash of the CURRENT judgment row
// with the latest decision receipt's resultingJudgmentHash (committed state ==
// current state) and requires that committed result to agree with the hash the
// immutable decision event recorded. A judgment with no decision receipt is a
// target-not-verifiable failure, never a successful skip (design §3).
func VerifyJudgmentHash(currentHash, resultingJudgmentHash, recordedEventHash string) VerificationLayer {
	if currentHash != resultingJudgmentHash {
		return layerFailed(LayerJudgmentHash, fmt.Sprintf("recomputed current judgment hash %s differs from the committed resultingJudgmentHash %s", currentHash, resultingJudgmentHash))
	}
	if resultingJudgmentHash != recordedEventHash {
		return layerFailed(LayerJudgmentHash, fmt.Sprintf("committed resultingJudgmentHash %s differs from the immutable decision event hash %s", resultingJudgmentHash, recordedEventHash))
	}
	return layerPassed(LayerJudgmentHash, "judgment hash matches the current row and the immutable decision event")
}

// ──────────────────────────────────────────────
// Aggregation, builder and finalization
// ──────────────────────────────────────────────

// AggregateLayers merges the per-receipt instances of one layer into the
// top-level result: failed if any instance fails, skipped only when ALL
// instances are inapplicable, otherwise passed (design §2). A single instance
// (the object layers and standalone reports) aggregates to itself so its detail
// is preserved. The first failed instance's detail is kept — deterministic
// because receipts are ordered.
func AggregateLayers(name string, layers []VerificationLayer) VerificationLayer {
	if len(layers) == 1 {
		out := layers[0]
		out.Name = name
		return out
	}
	if len(layers) == 0 {
		return layerSkipped(name, "inapplicable")
	}
	allSkipped := true
	for _, l := range layers {
		if l.Status == VerificationFailed {
			return layerFailed(name, l.Detail)
		}
		if l.Status != VerificationSkipped {
			allSkipped = false
		}
	}
	if allSkipped {
		out := layers[0]
		out.Name = name
		out.Status = VerificationSkipped
		return out
	}
	return layerPassed(name, fmt.Sprintf("all %d receipts passed", len(layers)))
}

// NewReport builds an empty verification report for a subject. Outcome is
// provisional until Finalize recomputes it from the layers.
func NewReport(subjectType SubjectType, subjectID string) *VerificationReport {
	return &VerificationReport{
		SubjectType: string(subjectType),
		SubjectID:   subjectID,
		Outcome:     VerificationOutcomePassed,
		Receipts:    make([]ReceiptVerification, 0, 4),
		Layers:      make([]VerificationLayer, 0, 10),
	}
}

// Finalize closes a report: it derives the outcome (passed only when every
// applicable top-level layer passes) and forces the accounting-correctness
// conclusion LAST, so the JSON closing brace always follows it (AC12).
func Finalize(report *VerificationReport) {
	outcome := VerificationOutcomePassed
	for _, l := range report.Layers {
		if l.Status == VerificationFailed {
			outcome = VerificationOutcomeFailed
			break
		}
	}
	report.Outcome = outcome
	report.AccountingCorrectness = AccountingCorrectnessNotAsserted
}

// ──────────────────────────────────────────────
// Small set helpers
// ──────────────────────────────────────────────

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// equalStringSets compares two string lists as canonical sets (sorted,
// deduplicated, empty strings dropped) — the roles contract of the protocol.
func equalStringSets(a, b []string) bool {
	sa, sb := canonicalRoles(a), canonicalRoles(b)
	if len(sa) != len(sb) {
		return false
	}
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}
