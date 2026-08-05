// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the v0.4.0 Step 3 receipt
// PROTOCOL: the closed model, the canonical byte contract (deterministic,
// fixed-order, no HTML escaping, roles canonicalized), the digest/key-id
// derivation and VerifyReceipt's fail-closed checks.
//
// CROSS-RUNTIME PARITY: the fixtures and pinned hex constants below are SHARED
// with the TypeScript mirror (core/__tests__/receipt.test.ts). The fixed seed
// (32×0x01) derives a deterministic Ed25519 keypair in both runtimes; Go signs
// and TS asserts the SAME public key, payload hash, signature and receipt hash
// (AC9/AC10 constant agreement — the full golden vector file lands in batch 3).
// A divergence between runtimes fails one of the two runners, never silently.
package core_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// Fixed parity seed (AC9/AC10): 32 bytes of 0x01. The SAME seed is documented
// in core/__tests__/receipt.test.ts — both runtimes derive the identical
// RFC 8032 keypair and signature from it. (Node's `generateKeyPairSync` seed
// option is NOT RFC 8032 compliant — the mirror imports the seed via JWK
// `d` instead, which re-derives the RFC 8032 public key.)
const paritySeedHex = "0101010101010101010101010101010101010101010101010101010101010101"

// Pinned constants computed by the reference canonicalization + RFC 8032
// Ed25519 (Node crypto and Go crypto/ed25519 must agree byte-for-byte on every
// one of these).
const (
	parityPublicKeyHex = "8a88e3dd7409f195fd52db2d3cba5d72ca6709bf1d94121bf3748801b40f6f5c"
	parityKeyID        = "ed25519:34750f98bd59fcfc946da45aaabe933be154a4b5094e1c4abf42866505f3c97e"
	parityPayloadHash  = "145911e45560f4cda235ca4641b881fa10ec00c52413f0abcfdd7a06c4d41fc6"
	paritySignatureHex = "662c77c6de213aa692160ecb08afaabd4e4c105a43ce80df30084b635e86b5e3c0066a4e4943c53efecf6c8dd4d1a0b20b31f08507d73c9e8f6979f49181120d"
	parityReceiptHash  = "9a26e2c5a570619b12bbcb9499e89c190e811c998297ea14034b0ba5c3e1826b"
	// Hex of the canonical unsigned-envelope JSON bytes (signature NOT signed)
	// and of the complete signed receipt bytes (signature between keyId and
	// issuedAt) — the strongest byte-parity proof shared with the mirror.
	parityUnsignedEnvelopeHex = "7b227375626a65637454797065223a226d656d6f7279222c227375626a6563744964223a226d656d6f72792d31222c22616374696f6e223a226d656d6f72795f617070726f766564222c2274656e616e744964223a2274656e616e742d31222c22636f6d70616e794964223a2261636d65222c2266697363616c506572696f644964223a22323032363031222c227061796c6f616448617368223a2231343539313165343535363066346364613233356361343634316238383166613130656330306335323431336630616263666464376130366334643431666336222c2270726576696f75735265636569707448617368223a22222c227072696e636970616c4964223a227375626a6563742d31222c226d656d626572736869704964223a226d656d626572736869702d31222c22706f6c69637956657273696f6e223a22617070726f76616c2d706f6c6963792f76302e342e30222c22616c676f726974686d223a2245643235353139222c226b65794964223a22656432353531393a33343735306639386264353966636663393436646134356161616265393333626531353461346235303934653163346162663432383636353035663363393765222c226973737565644174223a22323032362d30382d30355431333a30303a30305a227d"
	parityCompleteBytesHex    = "7b227375626a65637454797065223a226d656d6f7279222c227375626a6563744964223a226d656d6f72792d31222c22616374696f6e223a226d656d6f72795f617070726f766564222c2274656e616e744964223a2274656e616e742d31222c22636f6d70616e794964223a2261636d65222c2266697363616c506572696f644964223a22323032363031222c227061796c6f616448617368223a2231343539313165343535363066346364613233356361343634316238383166613130656330306335323431336630616263666464376130366334643431666336222c2270726576696f75735265636569707448617368223a22222c227072696e636970616c4964223a227375626a6563742d31222c226d656d626572736869704964223a226d656d626572736869702d31222c22706f6c69637956657273696f6e223a22617070726f76616c2d706f6c6963792f76302e342e30222c22616c676f726974686d223a2245643235353139222c226b65794964223a22656432353531393a33343735306639386264353966636663393436646134356161616265393333626531353461346235303934653163346162663432383636353035663363393765222c227369676e6174757265223a225a697833787434684f7161534667374c434b2b717655354d454670447a6f44664d41684c5931364774655041426d704f53555046507637506249335530614379437a487768516658504a365061586e306b59455344513d3d222c226973737565644174223a22323032362d30382d30355431333a30303a30305a227d"
)

// testKeypair derives the deterministic parity keypair from the fixed seed
// (32×0x01) — the same seed the TypeScript mirror documents.
func testKeypair() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed, err := hex.DecodeString(paritySeedHex)
	if err != nil {
		panic(err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv.Public().(ed25519.PublicKey), priv
}

// baseReceiptPayload is the canonical fixture of the parity constants: a
// memory_approved payload exercising the complete principal snapshot, empty
// inapplicable fields, a `&` (no HTML escaping) and a `"` (JSON escaping) in
// the reason, and UNSORTED + DUPLICATED roles (canonicalized by the payload
// hash contract). The identical fixture lives in the TS mirror test.
func baseReceiptPayload() core.ReceiptPayload {
	return core.ReceiptPayload{
		Version:                  core.ReceiptPayloadVersion,
		SubjectType:              core.SubjectTypeMemory,
		SubjectID:                "memory-1",
		Action:                   core.ReceiptActionMemoryApproved,
		TenantID:                 "tenant-1",
		CompanyID:                "acme",
		FiscalPeriodID:           "202601",
		ReviewedEnvelopeHash:     "h1-reviewed-envelope",
		ResultingEnvelopeHash:    "h2-resulting-envelope",
		Reason:                   `approved & verified "controller" review`,
		PrincipalID:              "subject-1",
		MembershipID:             "membership-1",
		PrincipalRoles:           []string{"controller", "accountant", "controller"},
		AuthenticationMethod:     "session",
		AssuranceLevel:           "standard",
		PrincipalAuthenticatedAt: "2026-08-05T13:00:00Z",
		PolicyVersion:            "approval-policy/v0.4.0",
		IssuedAt:                 "2026-08-05T13:00:00Z",
	}
}

// signFixture builds the signed parity receipt: the envelope copies the payload
// scope/principal/policy/timestamp/subject/action fields (the design invariant
// VerifyReceipt enforces) and signs the canonical unsigned envelope with the
// fixed seed. previousReceiptHash is genesis (empty).
func signFixture(t *testing.T) (core.SignedReceipt, core.ReceiptPayload, ed25519.PublicKey) {
	t.Helper()
	pub, priv := testKeypair()
	payload := baseReceiptPayload()
	r := core.SignedReceipt{
		SubjectType:    payload.SubjectType,
		SubjectID:      payload.SubjectID,
		Action:         payload.Action,
		TenantID:       payload.TenantID,
		CompanyID:      payload.CompanyID,
		FiscalPeriodID: payload.FiscalPeriodID,
		PayloadHash:    core.ReceiptPayloadHash(payload),
		PrincipalID:    payload.PrincipalID,
		MembershipID:   payload.MembershipID,
		PolicyVersion:  payload.PolicyVersion,
		Algorithm:      core.ReceiptAlgorithm,
		KeyID:          core.ReceiptKeyID(pub),
		IssuedAt:       payload.IssuedAt,
	}
	sig := ed25519.Sign(priv, core.CanonicalUnsignedEnvelope(r))
	r.Signature = base64.StdEncoding.EncodeToString(sig)
	return r, payload, pub
}

// ──────────────────────────────────────────────
// Closed model
// ──────────────────────────────────────────────

// TestReceiptClosedEnums freezes the two subject types, the eight actions and
// the algorithm constant; unknown values fail closed.
func TestReceiptClosedEnums(t *testing.T) {
	for _, s := range []core.SubjectType{core.SubjectTypeMemory, core.SubjectTypeJudgment} {
		if !core.IsValidSubjectType(s) {
			t.Errorf("IsValidSubjectType(%q) = false, want true", s)
		}
	}
	for _, a := range []core.ReceiptAction{
		core.ReceiptActionMemoryRecorded, core.ReceiptActionMemoryApproved,
		core.ReceiptActionMemoryRejected, core.ReceiptActionMemoryVoided,
		core.ReceiptActionRelationConfirmed, core.ReceiptActionRelationRejected,
		core.ReceiptActionEvidenceLinked, core.ReceiptActionMemorySuperseded,
	} {
		if !core.IsValidReceiptAction(a) {
			t.Errorf("IsValidReceiptAction(%q) = false, want true", a)
		}
	}
	if core.IsValidSubjectType("envelope") {
		t.Error("IsValidSubjectType(envelope) = true, want false")
	}
	if core.IsValidSubjectType("") {
		t.Error("IsValidSubjectType() = true, want false")
	}
	if core.IsValidReceiptAction("memory_deleted") {
		t.Error("IsValidReceiptAction(memory_deleted) = true, want false (closed set)")
	}
	if core.IsValidReceiptAction("") {
		t.Error("IsValidReceiptAction() = true, want false")
	}
	if core.ReceiptAlgorithm != "Ed25519" {
		t.Errorf("ReceiptAlgorithm = %q, want Ed25519", core.ReceiptAlgorithm)
	}
	if core.ReceiptPayloadVersion != "receipt-payload/v0.4.0" {
		t.Errorf("ReceiptPayloadVersion = %q", core.ReceiptPayloadVersion)
	}
}

// TestReceiptModelRoundTrip marshals/unmarshals a signed receipt and a payload
// and requires exact equality (including the roles slice).
func TestReceiptModelRoundTrip(t *testing.T) {
	r, payload, _ := signFixture(t)
	for name, v := range map[string]any{"SignedReceipt": r, "ReceiptPayload": payload} {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		switch name {
		case "SignedReceipt":
			var got core.SignedReceipt
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, r) {
				t.Fatalf("SignedReceipt round trip mismatch:\n got %+v\nwant %+v", got, r)
			}
		case "ReceiptPayload":
			var got core.ReceiptPayload
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, payload) {
				t.Fatalf("ReceiptPayload round trip mismatch:\n got %+v\nwant %+v", got, payload)
			}
		}
	}
}

// ──────────────────────────────────────────────
// Canonicalization and digests
// ──────────────────────────────────────────────

// TestCanonicalReceiptPayload freezes the canonical payload bytes: deterministic,
// no HTML escaping (the fixture's `&` stays raw), JSON escaping (`"` becomes
// `\"`), roles canonicalized (sorted + deduplicated, empty dropped) and every
// key present even when empty.
func TestCanonicalReceiptPayload(t *testing.T) {
	payload := baseReceiptPayload()
	canonical := core.CanonicalReceiptPayload(payload)

	t.Run("deterministic", func(t *testing.T) {
		if !bytes.Equal(canonical, core.CanonicalReceiptPayload(payload)) {
			t.Error("canonicalization must be deterministic for the same input")
		}
	})

	t.Run("no html escaping and json escaping", func(t *testing.T) {
		if !strings.Contains(string(canonical), `"reason":"approved & verified \"controller\" review"`) {
			t.Errorf("canonical payload must keep & raw and escape quotes: %s", canonical)
		}
	})

	t.Run("roles canonicalized", func(t *testing.T) {
		if !strings.Contains(string(canonical), `"principalRoles":["accountant","controller"]`) {
			t.Errorf("roles must be sorted and deduplicated: %s", canonical)
		}
	})

	t.Run("every key present when empty", func(t *testing.T) {
		for _, key := range []string{
			`"version":"receipt-payload/v0.4.0"`, `"reviewedJudgmentHash":""`,
			`"resultingJudgmentHash":""`, `"fromMemoryId":""`, `"fromEnvelopeHash":""`,
			`"toMemoryId":""`, `"toEnvelopeHash":""`, `"successorId":""`, `"evidenceRef":""`,
		} {
			if !strings.Contains(string(canonical), key) {
				t.Errorf("canonical payload missing key/value %s: %s", key, canonical)
			}
		}
	})

	t.Run("empty roles marshal as array not null", func(t *testing.T) {
		empty := payload
		empty.PrincipalRoles = []string{"", "", ""}
		if !strings.Contains(string(core.CanonicalReceiptPayload(empty)), `"principalRoles":[]`) {
			t.Errorf("empty roles must marshal as [] (never null): %s", core.CanonicalReceiptPayload(empty))
		}
	})
}

// TestReceiptPayloadHashPinned pins the canonical payload digest shared with
// the TypeScript mirror.
func TestReceiptPayloadHashPinned(t *testing.T) {
	if got := core.ReceiptPayloadHash(baseReceiptPayload()); got != parityPayloadHash {
		t.Errorf("ReceiptPayloadHash = %s, want %s", got, parityPayloadHash)
	}
}

// TestCanonicalEnvelopes freezes the unsigned-envelope and complete-receipt
// bytes: signature is NOT part of the signed envelope and sits between keyId
// and issuedAt in the complete receipt. Both hex constants are shared with the
// TypeScript mirror.
func TestCanonicalEnvelopes(t *testing.T) {
	r, _, _ := signFixture(t)

	if got := hex.EncodeToString(core.CanonicalUnsignedEnvelope(r)); got != parityUnsignedEnvelopeHex {
		t.Errorf("unsigned envelope bytes mismatch:\n got %s\nwant %s", got, parityUnsignedEnvelopeHex)
	}
	if strings.Contains(string(core.CanonicalUnsignedEnvelope(r)), "signature") {
		t.Error("the unsigned envelope must NOT contain the signature")
	}
	if got := hex.EncodeToString(core.CompleteReceiptBytes(r)); got != parityCompleteBytesHex {
		t.Errorf("complete receipt bytes mismatch:\n got %s\nwant %s", got, parityCompleteBytesHex)
	}
	// signature sits between keyId and issuedAt in the complete bytes
	complete := string(core.CompleteReceiptBytes(r))
	keyIdx := strings.Index(complete, `"keyId"`)
	sigIdx := strings.Index(complete, `"signature"`)
	issuedIdx := strings.Index(complete, `"issuedAt"`)
	if keyIdx == -1 || sigIdx == -1 || issuedIdx == -1 || !(keyIdx < sigIdx && sigIdx < issuedIdx) {
		t.Errorf("complete receipt field order must be keyId, signature, issuedAt: %s", complete)
	}
}

// TestReceiptHashPinned freezes the chain digest (previousReceiptHash chains on
// it) and proves determinism.
func TestReceiptHashPinned(t *testing.T) {
	r, _, _ := signFixture(t)
	if got := core.ReceiptHash(r); got != parityReceiptHash {
		t.Errorf("ReceiptHash = %s, want %s", got, parityReceiptHash)
	}
	if a, b := core.ReceiptHash(r), core.ReceiptHash(r); a != b {
		t.Error("ReceiptHash must be deterministic")
	}
}

// TestReceiptKeyID freezes the key-id derivation: "ed25519:" + the FULL SHA-256
// hex digest of the raw public key (never truncated).
func TestReceiptKeyID(t *testing.T) {
	pub, _ := testKeypair()
	if got := core.ReceiptKeyID(pub); got != parityKeyID {
		t.Errorf("ReceiptKeyID = %s, want %s", got, parityKeyID)
	}
	if !strings.HasPrefix(core.ReceiptKeyID(pub), "ed25519:") || len(core.ReceiptKeyID(pub)) != len("ed25519:")+64 {
		t.Errorf("keyId format broken: %q", core.ReceiptKeyID(pub))
	}
}

// ──────────────────────────────────────────────
// VerifyReceipt
// ──────────────────────────────────────────────

// TestVerifyReceiptHappyPath: a receipt signed with the parity seed verifies
// against its payload and the derived raw public key.
func TestVerifyReceiptHappyPath(t *testing.T) {
	r, payload, pub := signFixture(t)
	if err := core.VerifyReceipt(r, payload, pub); err != nil {
		t.Fatalf("VerifyReceipt must succeed, got %v", err)
	}
}

// TestVerifyReceiptRejectsModifiedPayload: any payload change breaks the pinned
// payload hash — RECEIPT_PAYLOAD_HASH_MISMATCH, never success.
func TestVerifyReceiptRejectsModifiedPayload(t *testing.T) {
	r, payload, pub := signFixture(t)
	tampered := payload
	tampered.Reason = "a different reason"
	err := core.VerifyReceipt(r, tampered, pub)
	if got := core.ReceiptCode(err); got != core.CodeReceiptPayloadHashMismatch {
		t.Fatalf("code = %q, want RECEIPT_PAYLOAD_HASH_MISMATCH (err: %v)", got, err)
	}
}

// TestVerifyReceiptRejectsModifiedSignature: flipping any signature byte fails
// closed with RECEIPT_SIGNATURE_INVALID.
func TestVerifyReceiptRejectsModifiedSignature(t *testing.T) {
	r, payload, pub := signFixture(t)
	sig, err := base64.StdEncoding.DecodeString(r.Signature)
	if err != nil {
		t.Fatalf("fixture signature: %v", err)
	}
	sig[0] ^= 0x01
	r.Signature = base64.StdEncoding.EncodeToString(sig)
	if err := core.VerifyReceipt(r, payload, pub); core.ReceiptCode(err) != core.CodeReceiptSignatureInvalid {
		t.Fatalf("code = %q, want RECEIPT_SIGNATURE_INVALID (err: %v)", core.ReceiptCode(err), err)
	}
}

// TestVerifyReceiptRejectsModifiedEnvelopeField covers every envelope mutation
// class: a field the payload shares (issuedAt) breaks equality
// (RECEIPT_INVALID), a chain field (previousReceiptHash) breaks the signature
// (RECEIPT_SIGNATURE_INVALID) and the payloadHash field itself breaks the
// digest check (RECEIPT_PAYLOAD_HASH_MISMATCH).
func TestVerifyReceiptRejectsModifiedEnvelopeField(t *testing.T) {
	r, payload, pub := signFixture(t)

	t.Run("issuedAt differs from payload", func(t *testing.T) {
		mutated := r
		mutated.IssuedAt = "2026-08-05T14:00:00Z"
		err := core.VerifyReceipt(mutated, payload, pub)
		if got := core.ReceiptCode(err); got != core.CodeReceiptInvalid {
			t.Fatalf("code = %q, want RECEIPT_INVALID (err: %v)", got, err)
		}
	})

	t.Run("previousReceiptHash modified", func(t *testing.T) {
		mutated := r
		mutated.PreviousReceiptHash = "deadbeef"
		err := core.VerifyReceipt(mutated, payload, pub)
		if got := core.ReceiptCode(err); got != core.CodeReceiptSignatureInvalid {
			t.Fatalf("code = %q, want RECEIPT_SIGNATURE_INVALID (err: %v)", got, err)
		}
	})

	t.Run("payloadHash field modified", func(t *testing.T) {
		mutated := r
		mutated.PayloadHash = "deadbeef"
		err := core.VerifyReceipt(mutated, payload, pub)
		if got := core.ReceiptCode(err); got != core.CodeReceiptPayloadHashMismatch {
			t.Fatalf("code = %q, want RECEIPT_PAYLOAD_HASH_MISMATCH (err: %v)", got, err)
		}
	})
}

// TestVerifyReceiptRejectsWrongPublicKey: a valid signature under ANOTHER key
// fails the key-id derivation — RECEIPT_KEY_MISMATCH.
func TestVerifyReceiptRejectsWrongPublicKey(t *testing.T) {
	r, payload, _ := signFixture(t)
	wrongSeed := make([]byte, ed25519.SeedSize)
	wrongSeed[0] = 0x02
	wrongPub := ed25519.NewKeyFromSeed(wrongSeed).Public().(ed25519.PublicKey)
	err := core.VerifyReceipt(r, payload, wrongPub)
	if got := core.ReceiptCode(err); got != core.CodeReceiptKeyMismatch {
		t.Fatalf("code = %q, want RECEIPT_KEY_MISMATCH (err: %v)", got, err)
	}
}

// TestVerifyReceiptFailsClosedOnUnknownEnums: an unknown subject type or action
// (or a tampered algorithm) fails closed with RECEIPT_INVALID before any
// cryptographic check.
func TestVerifyReceiptFailsClosedOnUnknownEnums(t *testing.T) {
	r, payload, pub := signFixture(t)

	unknownAction := r
	unknownAction.Action = core.ReceiptAction("memory_deleted")
	if err := core.VerifyReceipt(unknownAction, payload, pub); core.ReceiptCode(err) != core.CodeReceiptInvalid {
		t.Fatalf("unknown action: code = %q, want RECEIPT_INVALID (err: %v)", core.ReceiptCode(err), err)
	}

	unknownSubject := r
	unknownSubject.SubjectType = core.SubjectType("envelope")
	if err := core.VerifyReceipt(unknownSubject, payload, pub); core.ReceiptCode(err) != core.CodeReceiptInvalid {
		t.Fatalf("unknown subjectType: code = %q, want RECEIPT_INVALID (err: %v)", core.ReceiptCode(err), err)
	}

	unknownAlgorithm := r
	unknownAlgorithm.Algorithm = "RSA"
	if err := core.VerifyReceipt(unknownAlgorithm, payload, pub); core.ReceiptCode(err) != core.CodeReceiptInvalid {
		t.Fatalf("unknown algorithm: code = %q, want RECEIPT_INVALID (err: %v)", core.ReceiptCode(err), err)
	}
}

// TestVerifyReceiptRejectsMalformedSignature: non-base64 fails RECEIPT_INVALID;
// a well-formed base64 of the wrong length fails RECEIPT_SIGNATURE_INVALID.
func TestVerifyReceiptRejectsMalformedSignature(t *testing.T) {
	r, payload, pub := signFixture(t)

	notBase64 := r
	notBase64.Signature = "!!!not-base64!!!"
	if err := core.VerifyReceipt(notBase64, payload, pub); core.ReceiptCode(err) != core.CodeReceiptInvalid {
		t.Fatalf("bad base64: code = %q, want RECEIPT_INVALID (err: %v)", core.ReceiptCode(err), err)
	}

	short := r
	short.Signature = base64.StdEncoding.EncodeToString([]byte("too short"))
	if err := core.VerifyReceipt(short, payload, pub); core.ReceiptCode(err) != core.CodeReceiptSignatureInvalid {
		t.Fatalf("short signature: code = %q, want RECEIPT_SIGNATURE_INVALID (err: %v)", core.ReceiptCode(err), err)
	}
}

// TestReceiptCrossRuntimeParity is the AC9/AC10 constant agreement: Go signs
// the fixed payload with the fixed seed and asserts the SAME public key,
// payload hash, signature and receipt hash that the TypeScript mirror asserts
// in core/__tests__/receipt.test.ts. Deterministic Ed25519 makes this a
// byte-for-byte cross-runtime proof (the full golden vector file lands in
// batch 3).
func TestReceiptCrossRuntimeParity(t *testing.T) {
	r, payload, pub := signFixture(t)

	if got := hex.EncodeToString(pub); got != parityPublicKeyHex {
		t.Errorf("public key = %s, want %s", got, parityPublicKeyHex)
	}
	if got := core.ReceiptPayloadHash(payload); got != parityPayloadHash {
		t.Errorf("payload hash = %s, want %s", got, parityPayloadHash)
	}
	sig, err := base64.StdEncoding.DecodeString(r.Signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if got := hex.EncodeToString(sig); got != paritySignatureHex {
		t.Errorf("signature = %s, want %s", got, paritySignatureHex)
	}
	if got := core.ReceiptHash(r); got != parityReceiptHash {
		t.Errorf("receipt hash = %s, want %s", got, parityReceiptHash)
	}
	if err := core.VerifyReceipt(r, payload, pub); err != nil {
		t.Errorf("parity receipt must verify: %v", err)
	}
}
