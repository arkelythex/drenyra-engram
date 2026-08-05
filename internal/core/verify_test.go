// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the OFFLINE verification
// engine (v0.4.0 Step 4) at the PURE layer: canonical/non-canonical payloads,
// envelope integrity, signature and key timing, tenant/company scope, chain
// links, principal provenance, supersession chains, evidence/rule
// availability, judgment hashes, aggregation and the mandatory conclusion.
//
// The fixtures reuse the receipt protocol's pinned parity seed
// (receipt_test.go), so every asserted layer input is a REAL signed receipt.
// Every detail string asserted here is part of the Go↔TS fixture contract
// (design §2 — names, statuses and details must be byte-identical).
package core_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ──────────────────────────────────────────────
// Payload canonicalization
// ──────────────────────────────────────────────

func TestDecodeStoredPayload(t *testing.T) {
	_, payload, _ := signFixture(t)
	canonical := string(core.CanonicalReceiptPayload(payload))
	want := payload
	want.PrincipalRoles = []string{"accountant", "controller"} // canonical (sorted, deduped) form the stored bytes carry

	if got, err := core.DecodeStoredPayload(canonical); err != nil {
		t.Fatalf("canonical payload must decode: %v", err)
	} else if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded payload differs:\n got %+v\nwant %+v", got, want)
	}

	unknownKey := strings.Replace(canonical, `"version"`, `"unknownField":"x","version"`, 1)
	if _, err := core.DecodeStoredPayload(unknownKey); err == nil {
		t.Fatal("unknown field must be rejected (strict decode)")
	}

	if _, err := core.DecodeStoredPayload(canonical + ` {"extra":1}`); err == nil {
		t.Fatal("trailing data must be rejected")
	}
}

func TestVerifyPayloadCanonicalization(t *testing.T) {
	r, payload, _ := signFixture(t)
	canonical := string(core.CanonicalReceiptPayload(payload))

	t.Run("canonical passes", func(t *testing.T) {
		layer := core.VerifyPayloadCanonicalization(canonical, payload, r)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed (detail %q)", layer.Status, layer.Detail)
		}
	})

	t.Run("non-canonical byte order fails", func(t *testing.T) {
		// Swap two adjacent keys — the decoded payload is identical, but the
		// stored bytes are not the canonical re-marshal.
		reordered := strings.Replace(canonical, `"tenantId":"tenant-1","companyId":"acme"`, `"companyId":"acme","tenantId":"tenant-1"`, 1)
		if reordered == canonical {
			t.Fatal("fixture: key swap did not change the bytes")
		}
		layer := core.VerifyPayloadCanonicalization(reordered, payload, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("whitespace fails", func(t *testing.T) {
		spaced := strings.Replace(canonical, `"tenantId":`, `"tenantId" : `, 1)
		layer := core.VerifyPayloadCanonicalization(spaced, payload, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("payloadHash mismatch fails", func(t *testing.T) {
		mutated := r
		mutated.PayloadHash = strings.Repeat("0", 64)
		layer := core.VerifyPayloadCanonicalization(canonical, payload, mutated)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})
}

// ──────────────────────────────────────────────
// Envelope integrity
// ──────────────────────────────────────────────

func TestVerifyEnvelopeIntegrity(t *testing.T) {
	r, payload, _ := signFixture(t)
	stored := core.ReceiptHash(r)

	t.Run("integral passes", func(t *testing.T) {
		layer := core.VerifyEnvelopeIntegrity(r, payload, stored)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed (detail %q)", layer.Status, layer.Detail)
		}
	})

	t.Run("unknown action fails closed", func(t *testing.T) {
		mutated := r
		mutated.Action = core.ReceiptAction("memory_deleted")
		layer := core.VerifyEnvelopeIntegrity(mutated, payload, stored)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("envelope/payload drift fails", func(t *testing.T) {
		mutated := r
		mutated.IssuedAt = "2026-08-05T14:00:00Z"
		layer := core.VerifyEnvelopeIntegrity(mutated, payload, stored)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("stored hash mismatch fails", func(t *testing.T) {
		layer := core.VerifyEnvelopeIntegrity(r, payload, strings.Repeat("0", 64))
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
		if !strings.Contains(layer.Detail, "recomputed receipt hash") {
			t.Fatalf("detail must name the recomputed/stored mismatch: %q", layer.Detail)
		}
	})
}

// ──────────────────────────────────────────────
// Signature and signing-key validity
// ──────────────────────────────────────────────

func TestVerifySignature(t *testing.T) {
	r, _, pub := signFixture(t)

	t.Run("valid signature passes", func(t *testing.T) {
		layer := core.VerifySignature(r, pub)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed", layer.Status)
		}
	})

	t.Run("altered signature fails", func(t *testing.T) {
		mutated := r
		sig, _ := base64.StdEncoding.DecodeString(mutated.Signature)
		sig[0] ^= 0x01
		mutated.Signature = base64.StdEncoding.EncodeToString(sig)
		layer := core.VerifySignature(mutated, pub)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("malformed base64 fails", func(t *testing.T) {
		mutated := r
		mutated.Signature = "!!!not-base64!!!"
		layer := core.VerifySignature(mutated, pub)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("missing key material skips with the failed prerequisite", func(t *testing.T) {
		layer := core.VerifySignature(r, nil)
		if layer.Status != core.VerificationSkipped {
			t.Fatalf("status = %s, want skipped", layer.Status)
		}
		if !strings.Contains(layer.Detail, "signing-key validity") {
			t.Fatalf("detail must name the failed prerequisite: %q", layer.Detail)
		}
	})
}

func TestVerifySigningKeyValidity(t *testing.T) {
	pub, _ := testKeypair()
	r, _, _ := signFixture(t) // issuedAt 2026-08-05T13:00:00Z

	good := func() core.SigningKey {
		return core.SigningKey{
			Found:     true,
			Algorithm: core.ReceiptAlgorithm,
			PublicKey: base64.StdEncoding.EncodeToString(pub),
			CreatedAt: "2026-08-05T10:00:00Z",
		}
	}

	t.Run("valid key passes", func(t *testing.T) {
		layer := core.VerifySigningKeyValidity(good(), r)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed (detail %q)", layer.Status, layer.Detail)
		}
	})

	t.Run("before revocation passes", func(t *testing.T) {
		key := good()
		key.RevokedAt = "2026-08-05T14:00:00Z"
		layer := core.VerifySigningKeyValidity(key, r)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed (issued before revocation)", layer.Status)
		}
	})

	t.Run("at revocation fails", func(t *testing.T) {
		key := good()
		key.RevokedAt = "2026-08-05T13:00:00Z"
		layer := core.VerifySigningKeyValidity(key, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed (issued at revocation)", layer.Status)
		}
	})

	t.Run("after revocation fails", func(t *testing.T) {
		key := good()
		key.RevokedAt = "2026-08-05T12:00:00Z"
		layer := core.VerifySigningKeyValidity(key, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed (issued after revocation)", layer.Status)
		}
	})

	t.Run("unregistered key fails", func(t *testing.T) {
		key := good()
		key.Found = false
		layer := core.VerifySigningKeyValidity(key, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("wrong algorithm fails", func(t *testing.T) {
		key := good()
		key.Algorithm = "RSA"
		layer := core.VerifySigningKeyValidity(key, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("malformed public key fails", func(t *testing.T) {
		key := good()
		key.PublicKey = "!!!not-base64!!!"
		layer := core.VerifySigningKeyValidity(key, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("key id mismatch fails", func(t *testing.T) {
		wrongSeed := make([]byte, ed25519.SeedSize)
		wrongSeed[0] = 0x02
		wrongPub := ed25519.NewKeyFromSeed(wrongSeed).Public().(ed25519.PublicKey)
		key := good()
		key.PublicKey = base64.StdEncoding.EncodeToString(wrongPub)
		layer := core.VerifySigningKeyValidity(key, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("created after issued fails", func(t *testing.T) {
		key := good()
		key.CreatedAt = "2026-08-05T14:00:00Z"
		layer := core.VerifySigningKeyValidity(key, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("malformed created_at fails", func(t *testing.T) {
		key := good()
		key.CreatedAt = "not-a-timestamp"
		layer := core.VerifySigningKeyValidity(key, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("malformed revoked_at fails", func(t *testing.T) {
		key := good()
		key.RevokedAt = "not-a-timestamp"
		layer := core.VerifySigningKeyValidity(key, r)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("malformed issued_at fails", func(t *testing.T) {
		mutated := r
		mutated.IssuedAt = "not-a-timestamp"
		layer := core.VerifySigningKeyValidity(good(), mutated)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})
}

// ──────────────────────────────────────────────
// Tenant/company scope and chain link
// ──────────────────────────────────────────────

func TestVerifyTenantCompanyScope(t *testing.T) {
	r, payload, _ := signFixture(t)
	subject := core.SubjectScope{TenantID: "tenant-1", CompanyID: "acme", FiscalPeriodID: "202601"}

	if layer := core.VerifyTenantCompanyScope(r, payload, subject); layer.Status != core.VerificationPassed {
		t.Fatalf("matching scope must pass: %s %q", layer.Status, layer.Detail)
	}

	other := core.SubjectScope{TenantID: "tenant-2", CompanyID: "acme", FiscalPeriodID: "202601"}
	if layer := core.VerifyTenantCompanyScope(r, payload, other); layer.Status != core.VerificationFailed {
		t.Fatalf("tenant drift must fail: %s", layer.Status)
	}
}

func TestVerifyChainLink(t *testing.T) {
	r1, _, _ := signFixture(t)
	r1.PreviousReceiptHash = ""
	r1hash := core.ReceiptHash(r1)

	r2 := r1
	r2.PreviousReceiptHash = r1hash
	r2hash := core.ReceiptHash(r2)

	t.Run("genesis passes", func(t *testing.T) {
		layer := core.VerifyChainLink(r1, "", true)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed", layer.Status)
		}
	})

	t.Run("chained receipt passes", func(t *testing.T) {
		layer := core.VerifyChainLink(r2, r1hash, true)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed (detail %q)", layer.Status, layer.Detail)
		}
	})

	t.Run("broken genesis fails", func(t *testing.T) {
		layer := core.VerifyChainLink(r2, "", true)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed (first receipt must be genesis)", layer.Status)
		}
	})

	t.Run("chain gap fails", func(t *testing.T) {
		gap := r2
		gap.PreviousReceiptHash = ""
		layer := core.VerifyChainLink(gap, r1hash, true)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed (chain gap)", layer.Status)
		}
	})

	t.Run("missing predecessor fails", func(t *testing.T) {
		layer := core.VerifyChainLink(r2, "", false)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
		if !strings.Contains(layer.Detail, "does not resolve") {
			t.Fatalf("detail must name the unresolved predecessor: %q", layer.Detail)
		}
	})

	t.Run("hash mismatch fails", func(t *testing.T) {
		layer := core.VerifyChainLink(r2, r2hash, true)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})
}

// ──────────────────────────────────────────────
// Principal provenance
// ──────────────────────────────────────────────

func TestVerifyPrincipalProvenance(t *testing.T) {
	approvedPayload := baseReceiptPayload() // memory_approved with the full snapshot

	t.Run("approved snapshot matches", func(t *testing.T) {
		act := core.ActProvenance{
			Action:                "approved",
			Timestamp:             "2026-08-05T13:00:00Z",
			PrincipalID:           "subject-1",
			MembershipID:          "membership-1",
			Roles:                 []string{"controller", "accountant"},
			AuthenticationMethod:  "session",
			AssuranceLevel:        "standard",
			AuthenticatedAt:       "2026-08-05T13:00:00Z",
			Policy:                "approval-policy/v0.4.0",
			Reason:                `approved & verified "controller" review`,
			ReviewedEnvelopeHash:  "h1-reviewed-envelope",
			ResultingEnvelopeHash: "h2-resulting-envelope",
		}
		layer := core.VerifyPrincipalProvenance(approvedPayload, act)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed (detail %q)", layer.Status, layer.Detail)
		}
	})

	t.Run("approved reason mismatch fails", func(t *testing.T) {
		act := core.ActProvenance{
			Action: "approved", Timestamp: "2026-08-05T13:00:00Z",
			PrincipalID: "subject-1", MembershipID: "membership-1",
			Roles: []string{"controller", "accountant"},
			AuthenticationMethod: "session", AssuranceLevel: "standard",
			AuthenticatedAt: "2026-08-05T13:00:00Z",
			Policy:          "approval-policy/v0.4.0",
			Reason:          "a DIFFERENT reason",
			ReviewedEnvelopeHash:  "h1-reviewed-envelope",
			ResultingEnvelopeHash: "h2-resulting-envelope",
		}
		layer := core.VerifyPrincipalProvenance(approvedPayload, act)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
		if !strings.Contains(layer.Detail, "reason") {
			t.Fatalf("detail must name the mismatched field: %q", layer.Detail)
		}
	})

	t.Run("approved roles mismatch fails", func(t *testing.T) {
		act := core.ActProvenance{
			Action: "approved", Timestamp: "2026-08-05T13:00:00Z",
			PrincipalID: "subject-1", MembershipID: "membership-1",
			Roles: []string{"controller"},
			AuthenticationMethod: "session", AssuranceLevel: "standard",
			AuthenticatedAt: "2026-08-05T13:00:00Z",
			Policy:          "approval-policy/v0.4.0",
			Reason:          `approved & verified "controller" review`,
			ReviewedEnvelopeHash:  "h1-reviewed-envelope",
			ResultingEnvelopeHash: "h2-resulting-envelope",
		}
		layer := core.VerifyPrincipalProvenance(approvedPayload, act)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})

	t.Run("claimed act attribution matches", func(t *testing.T) {
		claimed := core.ReceiptPayload{Action: core.ReceiptActionMemoryRecorded, PrincipalID: "test-agent"}
		act := core.ActProvenance{Action: "recorded", Timestamp: "2026-08-05T13:00:00Z", PrincipalID: "test-agent"}
		layer := core.VerifyPrincipalProvenance(claimed, act)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed (attribution continuity)", layer.Status)
		}
	})

	t.Run("claimed act attribution mismatch fails", func(t *testing.T) {
		claimed := core.ReceiptPayload{Action: core.ReceiptActionEvidenceLinked, PrincipalID: "cli"}
		act := core.ActProvenance{Action: "linked", Timestamp: "2026-08-05T13:00:00Z", PrincipalID: "someone-else"}
		layer := core.VerifyPrincipalProvenance(claimed, act)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
		if !strings.Contains(layer.Detail, "attribution mismatch") {
			t.Fatalf("detail must name the attribution mismatch: %q", layer.Detail)
		}
	})

	t.Run("relation confirmed snapshot matches", func(t *testing.T) {
		decided := core.ReceiptPayload{
			Action:                 core.ReceiptActionRelationConfirmed,
			PrincipalID:            "subject-1",
			MembershipID:           "membership-1",
			PrincipalRoles:         []string{"controller", "controller"},
			AuthenticationMethod:   "session",
			AssuranceLevel:         "standard",
			PrincipalAuthenticatedAt: "2026-08-05T12:00:00Z",
			PolicyVersion:          "judgment-policy/v0.4.0",
			Reason:                 "resolution text",
			ResultingJudgmentHash:  strings.Repeat("a", 64),
			IssuedAt:               "2026-08-05T14:00:00Z",
		}
		act := core.ActProvenance{
			Action: "confirm", Timestamp: "2026-08-05T14:00:00Z",
			PrincipalID: "subject-1", MembershipID: "membership-1",
			Roles: []string{"controller"},
			AuthenticationMethod: "session", AssuranceLevel: "standard",
			AuthenticatedAt: "2026-08-05T12:00:00Z",
			Policy: "judgment-policy/v0.4.0", Reason: "resolution text",
			RecordedJudgmentHash: strings.Repeat("a", 64),
		}
		layer := core.VerifyPrincipalProvenance(decided, act)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed (detail %q)", layer.Status, layer.Detail)
		}
	})

	t.Run("relation rejected action mapping", func(t *testing.T) {
		rejected := core.ReceiptPayload{
			Action: core.ReceiptActionRelationRejected,
			PrincipalID: "subject-1", MembershipID: "membership-1",
			PrincipalRoles: []string{"controller"},
			AuthenticationMethod: "session", AssuranceLevel: "standard",
			PrincipalAuthenticatedAt: "2026-08-05T12:00:00Z",
			PolicyVersion: "judgment-policy/v0.4.0", Reason: "no",
			ResultingJudgmentHash: strings.Repeat("b", 64),
			IssuedAt:              "2026-08-05T14:00:00Z",
		}
		act := core.ActProvenance{
			Action: "reject", Timestamp: "2026-08-05T14:00:00Z",
			PrincipalID: "subject-1", MembershipID: "membership-1",
			Roles: []string{"controller"},
			AuthenticationMethod: "session", AssuranceLevel: "standard",
			AuthenticatedAt: "2026-08-05T12:00:00Z",
			Policy: "judgment-policy/v0.4.0", Reason: "no",
			RecordedJudgmentHash: strings.Repeat("b", 64),
		}
		layer := core.VerifyPrincipalProvenance(rejected, act)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed", layer.Status)
		}
	})

	t.Run("event action mismatch fails", func(t *testing.T) {
		act := core.ActProvenance{
			Action: "reject", Timestamp: "2026-08-05T13:00:00Z",
			PrincipalID: "subject-1", MembershipID: "membership-1",
			Roles: []string{"controller", "accountant"},
			AuthenticationMethod: "session", AssuranceLevel: "standard",
			AuthenticatedAt: "2026-08-05T13:00:00Z",
			Policy: "approval-policy/v0.4.0", Reason: `approved & verified "controller" review`,
			ReviewedEnvelopeHash: "h1-reviewed-envelope", ResultingEnvelopeHash: "h2-resulting-envelope",
		}
		layer := core.VerifyPrincipalProvenance(approvedPayload, act)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})
}

// ──────────────────────────────────────────────
// Supersession chain
// ──────────────────────────────────────────────

func TestVerifySupersessionChain(t *testing.T) {
	scopeA := core.SubjectScope{TenantID: "tenant-1", CompanyID: "acme", FiscalPeriodID: "202601"}
	scopeB := core.SubjectScope{TenantID: "tenant-2", CompanyID: "other", FiscalPeriodID: "202601"}

	t.Run("current terminal passes and reports the current id", func(t *testing.T) {
		links := []core.SupersessionLink{
			{SubjectID: "mem-a", SuccessorID: "mem-b", Superseded: true, Scope: scopeA},
			{SubjectID: "mem-b", SuccessorID: "", Superseded: false, Scope: scopeA},
		}
		layer := core.VerifySupersessionChain(links, scopeA)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed (detail %q)", layer.Status, layer.Detail)
		}
		if !strings.Contains(layer.Detail, "mem-b") {
			t.Fatalf("detail must report the terminal current id: %q", layer.Detail)
		}
	})

	t.Run("missing successor fails", func(t *testing.T) {
		links := []core.SupersessionLink{{SubjectID: "mem-a", Superseded: true, Scope: scopeA}}
		layer := core.VerifySupersessionChain(links, scopeA)
		if layer.Status != core.VerificationFailed || !strings.Contains(layer.Detail, "missing successor") {
			t.Fatalf("status = %s detail %q, want failed with the missing successor", layer.Status, layer.Detail)
		}
	})

	t.Run("cycle fails", func(t *testing.T) {
		links := []core.SupersessionLink{
			{SubjectID: "mem-a", SuccessorID: "mem-b", Superseded: true, Scope: scopeA},
			{SubjectID: "mem-b", SuccessorID: "mem-a", Superseded: true, Scope: scopeA},
			{SubjectID: "mem-a", SuccessorID: "mem-b", Superseded: true, Scope: scopeA},
		}
		layer := core.VerifySupersessionChain(links, scopeA)
		if layer.Status != core.VerificationFailed || !strings.Contains(layer.Detail, "cycle") {
			t.Fatalf("status = %s detail %q, want failed with the cycle", layer.Status, layer.Detail)
		}
	})

	t.Run("cross-scope link fails", func(t *testing.T) {
		links := []core.SupersessionLink{
			{SubjectID: "mem-a", SuccessorID: "mem-b", Superseded: true, Scope: scopeA},
			{SubjectID: "mem-b", Superseded: false, Scope: scopeB},
		}
		layer := core.VerifySupersessionChain(links, scopeA)
		if layer.Status != core.VerificationFailed || !strings.Contains(layer.Detail, "crosses scope") {
			t.Fatalf("status = %s detail %q, want failed with the cross-scope link", layer.Status, layer.Detail)
		}
	})

	t.Run("status/relation disagreement fails", func(t *testing.T) {
		links := []core.SupersessionLink{
			{SubjectID: "mem-a", SuccessorID: "mem-b", Superseded: false, Scope: scopeA},
		}
		layer := core.VerifySupersessionChain(links, scopeA)
		if layer.Status != core.VerificationFailed || !strings.Contains(layer.Detail, "disagrees") {
			t.Fatalf("status = %s detail %q, want failed with the status/relation disagreement", layer.Status, layer.Detail)
		}
	})
}

// ──────────────────────────────────────────────
// Evidence / rule availability
// ──────────────────────────────────────────────

func TestVerifyEvidenceAvailability(t *testing.T) {
	env := strings.Repeat("e", 64)
	t.Run("declared refs linked and envelope matches", func(t *testing.T) {
		layer := core.VerifyEvidenceAvailability([]string{"ref-1"}, []string{"ref-1"}, env, env)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed", layer.Status)
		}
	})

	t.Run("removed link row fails", func(t *testing.T) {
		layer := core.VerifyEvidenceAvailability([]string{"ref-1"}, nil, env, env)
		if layer.Status != core.VerificationFailed || !strings.Contains(layer.Detail, "ref-1") {
			t.Fatalf("status = %s detail %q, want failed naming ref-1", layer.Status, layer.Detail)
		}
	})

	t.Run("envelope mismatch fails", func(t *testing.T) {
		layer := core.VerifyEvidenceAvailability([]string{"ref-1"}, []string{"ref-1"}, strings.Repeat("f", 64), env)
		if layer.Status != core.VerificationFailed || !strings.Contains(layer.Detail, "head result") {
			t.Fatalf("status = %s detail %q, want failed with the envelope mismatch", layer.Status, layer.Detail)
		}
	})
}

func TestVerifyRuleAvailability(t *testing.T) {
	env := strings.Repeat("e", 64)
	t.Run("dynamic refs row-backed and envelope matches", func(t *testing.T) {
		layer := core.VerifyRuleAvailability([]string{"stored-1"}, []string{"linked-1"}, []string{"stored-1", "linked-1"}, env, env)
		if layer.Status != core.VerificationPassed {
			t.Fatalf("status = %s, want passed", layer.Status)
		}
	})

	t.Run("unbacked dynamic ref fails", func(t *testing.T) {
		layer := core.VerifyRuleAvailability([]string{"stored-1"}, nil, []string{"stored-1", "linked-1"}, env, env)
		if layer.Status != core.VerificationFailed || !strings.Contains(layer.Detail, "linked-1") {
			t.Fatalf("status = %s detail %q, want failed naming the unbacked ref", layer.Status, layer.Detail)
		}
	})

	t.Run("envelope mismatch fails", func(t *testing.T) {
		layer := core.VerifyRuleAvailability([]string{"stored-1"}, []string{"linked-1"}, []string{"stored-1", "linked-1"}, strings.Repeat("f", 64), env)
		if layer.Status != core.VerificationFailed {
			t.Fatalf("status = %s, want failed", layer.Status)
		}
	})
}

// ──────────────────────────────────────────────
// Judgment hash
// ──────────────────────────────────────────────

func TestVerifyJudgmentHash(t *testing.T) {
	hash := strings.Repeat("a", 64)
	if layer := core.VerifyJudgmentHash(hash, hash, hash); layer.Status != core.VerificationPassed {
		t.Fatalf("matching hashes must pass: %s %q", layer.Status, layer.Detail)
	}
	if layer := core.VerifyJudgmentHash(strings.Repeat("b", 64), hash, hash); layer.Status != core.VerificationFailed {
		t.Fatalf("current-row mismatch must fail: %s", layer.Status)
	}
	if layer := core.VerifyJudgmentHash(hash, strings.Repeat("c", 64), hash); layer.Status != core.VerificationFailed {
		t.Fatalf("event mismatch must fail: %s", layer.Status)
	}
}

// ──────────────────────────────────────────────
// Aggregation and finalization
// ──────────────────────────────────────────────

func TestAggregateLayers(t *testing.T) {
	pass := core.VerificationLayer{Name: "x", Status: core.VerificationPassed, Detail: "ok"}
	fail := core.VerificationLayer{Name: "x", Status: core.VerificationFailed, Detail: "boom"}
	skip := core.VerificationLayer{Name: "x", Status: core.VerificationSkipped, Detail: "skipped: prerequisite"}

	t.Run("any failure fails", func(t *testing.T) {
		layer := core.AggregateLayers("x", []core.VerificationLayer{pass, fail, pass})
		if layer.Status != core.VerificationFailed || layer.Detail != "boom" {
			t.Fatalf("got %s %q, want failed with the first failure detail", layer.Status, layer.Detail)
		}
	})

	t.Run("all skipped stays skipped", func(t *testing.T) {
		layer := core.AggregateLayers("x", []core.VerificationLayer{skip, skip})
		if layer.Status != core.VerificationSkipped {
			t.Fatalf("got %s, want skipped", layer.Status)
		}
	})

	t.Run("mixed skip and pass passes", func(t *testing.T) {
		layer := core.AggregateLayers("x", []core.VerificationLayer{skip, pass})
		if layer.Status != core.VerificationPassed || !strings.Contains(layer.Detail, "2 receipts") {
			t.Fatalf("got %s %q, want passed with the receipt count", layer.Status, layer.Detail)
		}
	})

	t.Run("single instance is identity", func(t *testing.T) {
		layer := core.AggregateLayers("supersession chain", []core.VerificationLayer{{Name: "supersession chain", Status: core.VerificationPassed, Detail: "chain current at mem-b"}})
		if layer.Status != core.VerificationPassed || layer.Detail != "chain current at mem-b" {
			t.Fatalf("got %s %q, want the preserved detail", layer.Status, layer.Detail)
		}
	})
}

// TestFinalizeMandatoryConclusion freezes the report contract: Finalize derives
// the outcome from the top-level layers and forces the accounting-correctness
// conclusion LAST (AC12 — the JSON closing brace always follows the exact
// constant, in both all-pass and failed reports).
func TestFinalizeMandatoryConclusion(t *testing.T) {
	const suffix = `"accountingCorrectness":"Accounting correctness: NOT ASSERTED"}`

	build := func(status core.VerificationStatus) *core.VerificationReport {
		report := core.NewReport(core.SubjectTypeMemory, "mem-1")
		report.Layers = append(report.Layers, core.VerificationLayer{Name: "x", Status: status, Detail: "d"})
		core.Finalize(report)
		return report
	}

	passed := build(core.VerificationPassed)
	if passed.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("outcome = %s, want passed", passed.Outcome)
	}
	data, err := json.Marshal(passed)
	if err != nil {
		t.Fatalf("marshal passed report: %v", err)
	}
	if !strings.HasSuffix(string(data), suffix) {
		t.Fatalf("passed report must end with the exact conclusion: %s", data)
	}

	failed := build(core.VerificationFailed)
	if failed.Outcome != core.VerificationOutcomeFailed {
		t.Fatalf("outcome = %s, want failed", failed.Outcome)
	}
	data, err = json.Marshal(failed)
	if err != nil {
		t.Fatalf("marshal failed report: %v", err)
	}
	if !strings.HasSuffix(string(data), suffix) {
		t.Fatalf("failed report must end with the exact conclusion: %s", data)
	}
	if failed.AccountingCorrectness != core.AccountingCorrectnessNotAsserted {
		t.Fatalf("conclusion = %q", failed.AccountingCorrectness)
	}
}

// TestNewReportStableLayerNames freezes the stable receipt-layer names shared
// with the TypeScript mirror (design §2).
func TestNewReportStableLayerNames(t *testing.T) {
	want := []string{
		"payload canonicalization",
		"envelope integrity",
		"signature",
		"signing-key validity",
		"tenant/company scope",
		"chain link",
	}
	got := core.ReceiptLayerNames()
	if len(got) != len(want) {
		t.Fatalf("layer count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("layer[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// ──────────────────────────────────────────────
// Go↔TS parity fixture (design §7)
// ──────────────────────────────────────────────

// The fixture structs below are TEST-LOCAL with json tags because the core
// verification input types (SigningKey, SubjectScope, ActProvenance,
// SupersessionLink) intentionally carry no JSON tags — they are store-independent
// pure inputs, not wire objects. The fixture shape is therefore part of the
// Go↔TS contract (design §7) and mirrors the TS test-local interfaces in
// core/__tests__/verify.test.ts.

type parityKey struct {
	Found     bool   `json:"found"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"publicKey"`
	CreatedAt string `json:"createdAt"`
	RevokedAt string `json:"revokedAt"`
}

func (k parityKey) signingKey() core.SigningKey {
	return core.SigningKey{Found: k.Found, Algorithm: k.Algorithm, PublicKey: k.PublicKey, CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt}
}

type parityScope struct {
	TenantID       string `json:"tenantId"`
	CompanyID      string `json:"companyId"`
	FiscalPeriodID string `json:"fiscalPeriodId"`
}

func (s parityScope) subjectScope() core.SubjectScope {
	return core.SubjectScope{TenantID: s.TenantID, CompanyID: s.CompanyID, FiscalPeriodID: s.FiscalPeriodID}
}

type parityProvenance struct {
	Action                string   `json:"action"`
	Timestamp             string   `json:"timestamp"`
	PrincipalID           string   `json:"principalId"`
	MembershipID          string   `json:"membershipId"`
	Roles                 []string `json:"roles"`
	AuthenticationMethod  string   `json:"authenticationMethod"`
	AssuranceLevel        string   `json:"assuranceLevel"`
	AuthenticatedAt       string   `json:"authenticatedAt"`
	Policy                string   `json:"policy"`
	Reason                string   `json:"reason"`
	ReviewedEnvelopeHash  string   `json:"reviewedEnvelopeHash"`
	ResultingEnvelopeHash string   `json:"resultingEnvelopeHash"`
	RecordedJudgmentHash  string   `json:"recordedJudgmentHash"`
}

func (p parityProvenance) act() core.ActProvenance {
	return core.ActProvenance{
		Action: p.Action, Timestamp: p.Timestamp, PrincipalID: p.PrincipalID,
		MembershipID: p.MembershipID, Roles: p.Roles,
		AuthenticationMethod: p.AuthenticationMethod, AssuranceLevel: p.AssuranceLevel,
		AuthenticatedAt: p.AuthenticatedAt, Policy: p.Policy, Reason: p.Reason,
		ReviewedEnvelopeHash: p.ReviewedEnvelopeHash, ResultingEnvelopeHash: p.ResultingEnvelopeHash,
		RecordedJudgmentHash: p.RecordedJudgmentHash,
	}
}

type paritySupersessionLink struct {
	SubjectID   string      `json:"subjectId"`
	SuccessorID string      `json:"successorId"`
	Superseded  bool        `json:"superseded"`
	Scope       parityScope `json:"scope"`
}

func (l paritySupersessionLink) link() core.SupersessionLink {
	return core.SupersessionLink{SubjectID: l.SubjectID, SuccessorID: l.SuccessorID, Superseded: l.Superseded, Scope: l.Scope.subjectScope()}
}

type parityAvailability struct {
	DeclaredRefs      []string `json:"declaredRefs"`
	CurrentLinks      []string `json:"currentLinks"`
	CurrentEnvelope   string   `json:"currentEnvelope"`
	CommittedEnvelope string   `json:"committedEnvelope"`
}

type parityRuleAvailability struct {
	StoredRefs        []string `json:"storedRefs"`
	CurrentLinks      []string `json:"currentLinks"`
	MergedRefs        []string `json:"mergedRefs"`
	CurrentEnvelope   string   `json:"currentEnvelope"`
	CommittedEnvelope string   `json:"committedEnvelope"`
}

type parityExpectedReport struct {
	Outcome               core.VerificationOutcome   `json:"outcome"`
	AccountingCorrectness string                     `json:"accountingCorrectness"`
	Receipts              []core.ReceiptVerification `json:"receipts"`
	Layers                []core.VerificationLayer   `json:"layers"`
}

type parityScenario struct {
	Key      string               `json:"key"`
	Expected parityExpectedReport `json:"expected"`
}

type parityFixture struct {
	Name             string                       `json:"name"`
	SubjectType      core.SubjectType             `json:"subjectType"`
	SubjectID        string                       `json:"subjectId"`
	Seed             string                       `json:"seed"`
	SubjectScope     parityScope                  `json:"subjectScope"`
	Keys             map[string]parityKey         `json:"keys"`
	Receipts         []core.SignedReceipt         `json:"receipts"`
	Payloads         []core.ReceiptPayload        `json:"payloads"`
	Provenance       []parityProvenance           `json:"provenance"`
	SupersessionLink []paritySupersessionLink     `json:"supersessionLinks"`
	Evidence         parityAvailability           `json:"evidence"`
	Rules            parityRuleAvailability       `json:"rules"`
	Scenarios        map[string]parityScenario    `json:"scenarios"`
}

// buildParityReport assembles the full memory verification report exactly as
// internal/server/verify_service.go does (design §5): per-receipt six layers in
// the stable order, the six aggregated top-level layers, the per-receipt
// diagnostic blocks, then the memory object layers (principal provenance
// aggregate, supersession chain, evidence availability, rule availability) and
// Finalize. The TypeScript mirror test runs the IDENTICAL sequence over the same
// fixture (core/__tests__/verify.test.ts) and must produce byte-identical
// names, statuses and details.
func buildParityReport(t *testing.T, f parityFixture, key core.SigningKey) core.VerificationReport {
	t.Helper()
	report := *core.NewReport(f.SubjectType, f.SubjectID)
	perReceipt := make([][]core.VerificationLayer, len(f.Receipts))
	rawKey := core.DecodeSigningPublicKey(key)
	prevComputed := ""
	for i, r := range f.Receipts {
		payload := f.Payloads[i]
		payloadJSON := string(core.CanonicalReceiptPayload(payload))
		storedHash := core.ReceiptHash(r)
		layers := []core.VerificationLayer{
			core.VerifyPayloadCanonicalization(payloadJSON, payload, r),
			core.VerifyEnvelopeIntegrity(r, payload, storedHash),
			core.VerifySignature(r, rawKey),
			core.VerifySigningKeyValidity(key, r),
			core.VerifyTenantCompanyScope(r, payload, f.SubjectScope.subjectScope()),
		}
		layers = append(layers, core.VerifyChainLink(r, prevComputed, true))
		prevComputed = storedHash
		perReceipt[i] = layers
	}
	for col, name := range core.ReceiptLayerNames() {
		instances := make([]core.VerificationLayer, len(f.Receipts))
		for i := range f.Receipts {
			instances[i] = perReceipt[i][col]
		}
		report.Layers = append(report.Layers, core.AggregateLayers(name, instances))
	}
	for i, r := range f.Receipts {
		report.Receipts = append(report.Receipts, core.ReceiptVerification{ReceiptHash: core.ReceiptHash(r), Action: r.Action, Layers: perReceipt[i]})
	}
	provInstances := make([]core.VerificationLayer, len(f.Payloads))
	for i, p := range f.Payloads {
		provInstances[i] = core.VerifyPrincipalProvenance(p, f.Provenance[i].act())
	}
	report.Layers = append(report.Layers, core.AggregateLayers(core.LayerPrincipalProvenance, provInstances))
	links := make([]core.SupersessionLink, len(f.SupersessionLink))
	for i, l := range f.SupersessionLink {
		links[i] = l.link()
	}
	report.Layers = append(report.Layers, core.VerifySupersessionChain(links, f.SubjectScope.subjectScope()))
	report.Layers = append(report.Layers, core.VerifyEvidenceAvailability(f.Evidence.DeclaredRefs, f.Evidence.CurrentLinks, f.Evidence.CurrentEnvelope, f.Evidence.CommittedEnvelope))
	report.Layers = append(report.Layers, core.VerifyRuleAvailability(f.Rules.StoredRefs, f.Rules.CurrentLinks, f.Rules.MergedRefs, f.Rules.CurrentEnvelope, f.Rules.CommittedEnvelope))
	core.Finalize(&report)
	return report
}

// TestVerifyParityFixture runs the SHARED parity fixture (testdata/verify-parity.json)
// against the Go implementation: two real signed receipts (memory_recorded
// genesis + memory_approved chained, fixed parity seed), two key records (valid
// and revoked at issuance) and the EXPECTED ordered report for each scenario
// (names, statuses, details, outcome and conclusion). The SAME fixture runs from
// TypeScript (core/__tests__/verify.test.ts): a divergence between runtimes
// fails one of the two runners, never silently (design §8, AC12).
func TestVerifyParityFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "verify-parity.json"))
	if err != nil {
		t.Fatalf("read parity fixture: %v", err)
	}
	var f parityFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode parity fixture: %v", err)
	}
	if f.Seed != paritySeedHex {
		t.Fatalf("fixture seed = %q, want the fixed parity seed", f.Seed)
	}
	if len(f.Receipts) != 2 {
		t.Fatalf("fixture must carry a 2-receipt chain, got %d", len(f.Receipts))
	}

	for name, sc := range f.Scenarios {
		t.Run(name, func(t *testing.T) {
			key, ok := f.Keys[sc.Key]
			if !ok {
				t.Fatalf("scenario references unknown key %q", sc.Key)
			}
			report := buildParityReport(t, f, key.signingKey())
			if report.SubjectType != string(f.SubjectType) || report.SubjectID != f.SubjectID {
				t.Errorf("subject = %s %s, want %s %s", report.SubjectType, report.SubjectID, f.SubjectType, f.SubjectID)
			}
			if report.Outcome != sc.Expected.Outcome {
				t.Errorf("outcome = %s, want %s", report.Outcome, sc.Expected.Outcome)
			}
			if report.AccountingCorrectness != core.AccountingCorrectnessNotAsserted {
				t.Errorf("conclusion = %q", report.AccountingCorrectness)
			}
			if report.AccountingCorrectness != sc.Expected.AccountingCorrectness {
				t.Errorf("accountingCorrectness = %q, want %q", report.AccountingCorrectness, sc.Expected.AccountingCorrectness)
			}
			if !reflect.DeepEqual(report.Receipts, sc.Expected.Receipts) {
				t.Errorf("receipts differ:\n got %+v\nwant %+v", report.Receipts, sc.Expected.Receipts)
			}
			if len(report.Layers) != len(sc.Expected.Layers) {
				t.Fatalf("layer count = %d, want %d", len(report.Layers), len(sc.Expected.Layers))
			}
			for i := range sc.Expected.Layers {
				if report.Layers[i] != sc.Expected.Layers[i] {
					t.Errorf("layer[%d] = %+v, want %+v", i, report.Layers[i], sc.Expected.Layers[i])
				}
			}
			// AC12: the serialized report ends with the exact conclusion in BOTH
			// the all-pass and the failed report (the fixture pins the constant).
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatalf("marshal report: %v", err)
			}
			suffix := `"accountingCorrectness":` + strconv.Quote(sc.Expected.AccountingCorrectness) + `}`
			if !strings.HasSuffix(string(data), suffix) {
				t.Errorf("report must end with the exact conclusion: %s", data)
			}
		})
	}
}
