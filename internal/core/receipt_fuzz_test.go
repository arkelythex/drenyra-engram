// Fuzz target for the receipt canonicalization byte contract (spec FR-1 /
// design D-10, WU-4). Drives CanonicalReceiptPayload, CompleteReceiptBytes and
// ReceiptHash (internal/core/receipt.go) across the frozen payload versions
// (receipt-payload/v0.4.0 … v0.10.0) with arbitrary payload documents and
// enforces the frozen invariants: no panic, deterministic bytes, round-trip
// stability (canonical bytes re-parse and re-canonicalize byte-identically —
// the no-invalid-success contract), and stable complete-receipt bytes/hashes.
//
// Input cap: any input strictly larger than 1 MiB is ignored immediately
// (spec FR-1 v, design D-10).
package core

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// frozenPayloadVersions is the closed set of payload versions the harness
// rotates through (spec FR-1 / design D-10: receipt-payload/v0.4.0 … v0.10.0).
var frozenPayloadVersions = []string{
	ReceiptPayloadVersion,    // receipt-payload/v0.4.0
	ReceiptPayloadVersionV05, // receipt-payload/v0.5.0
	ReceiptPayloadVersionV07, // receipt-payload/v0.7.0
	ReceiptPayloadVersionV08, // receipt-payload/v0.8.0
	ReceiptPayloadVersionV09, // receipt-payload/v0.9.0
	ReceiptPayloadVersionV10, // receipt-payload/v0.10.0
}

// validPayloadJSONv04 is a production-shaped v0.4.0 memory_approved payload
// with UNSORTED + DUPLICATED roles (canonicalization must sort and deduplicate)
// and JSON-escaping-sensitive characters, mirroring the parity fixture in
// receipt_test.go. All values are fictional.
const validPayloadJSONv04 = `{"version":"receipt-payload/v0.4.0","subjectType":"memory","subjectId":"memory-1","action":"memory_approved","tenantId":"tenant-1","companyId":"acme","fiscalPeriodId":"202601","reviewedEnvelopeHash":"h1-reviewed-envelope","resultingEnvelopeHash":"h2-resulting-envelope","reviewedJudgmentHash":"","resultingJudgmentHash":"","fromMemoryId":"","fromEnvelopeHash":"","toMemoryId":"","toEnvelopeHash":"","successorId":"","evidenceRef":"","reason":"approved & verified \"controller\" review","principalId":"subject-1","membershipId":"membership-1","principalRoles":["controller","accountant","controller"],"authenticationMethod":"session","assuranceLevel":"standard","principalAuthenticatedAt":"2026-08-05T13:00:00Z","policyVersion":"approval-policy/v0.4.0","issuedAt":"2026-08-05T13:00:00Z","reviewedLifecycleHash":"","resultingLifecycleHash":"","executionAttemptId":""}`

// validPayloadJSONv09 is a production-shaped v0.9.0 evidence-lifecycle
// purge_intent payload carrying the two lifecycle hashes and the per-attempt
// execution identifier (all fictional).
const validPayloadJSONv09 = `{"version":"receipt-payload/v0.9.0","subjectType":"evidence_object","subjectId":"obj-1","action":"purge_intent","tenantId":"tenant-1","companyId":"acme","fiscalPeriodId":"202601","reviewedEnvelopeHash":"","resultingEnvelopeHash":"","reviewedJudgmentHash":"","resultingJudgmentHash":"","fromMemoryId":"","fromEnvelopeHash":"","toMemoryId":"","toEnvelopeHash":"","successorId":"","evidenceRef":"sha256:abc","reason":"retention expired","principalId":"subject-1","membershipId":"membership-1","principalRoles":["controller"],"authenticationMethod":"session","assuranceLevel":"standard","principalAuthenticatedAt":"2026-08-05T13:00:00Z","policyVersion":"retention-policy/v0.9.0","issuedAt":"2026-08-05T13:00:00Z","reviewedLifecycleHash":"h1-lifecycle","resultingLifecycleHash":"h2-lifecycle","executionAttemptId":"(tenant-1, exec-42)"}`

// checkReceiptInvariants runs the frozen FuzzCanonicalReceiptPayload invariant
// set over a payload document (spec FR-1, AC-1). It is shared by the fuzz
// target and the named regression/corpus tests so the corpus replay and the
// unit tests exercise the exact same checks.
func checkReceiptInvariants(t testing.TB, p ReceiptPayload) {
	t.Helper()
	for _, version := range frozenPayloadVersions {
		p.Version = version

		// Determinism: identical payload yields identical canonical bytes.
		canonical := CanonicalReceiptPayload(p)
		if !bytes.Equal(canonical, CanonicalReceiptPayload(p)) {
			t.Fatalf("non-deterministic canonical bytes for version %q", version)
		}

		// No-invalid-success + round-trip (spec FR-1 iii/iv): canonical bytes
		// must re-parse to a payload that re-canonicalizes byte-identically.
		var reparsed ReceiptPayload
		if err := json.Unmarshal(canonical, &reparsed); err != nil {
			t.Fatalf("canonical payload (version %q) does not re-parse: %v", version, err)
		}
		if got := CanonicalReceiptPayload(reparsed); !bytes.Equal(got, canonical) {
			t.Fatalf("canonical payload (version %q) does not round-trip", version)
		}

		// Complete receipt bytes and digests are deterministic for the same
		// envelope.
		envelope := SignedReceipt{
			SubjectType:    p.SubjectType,
			SubjectID:      p.SubjectID,
			Action:         p.Action,
			TenantID:       p.TenantID,
			CompanyID:      p.CompanyID,
			FiscalPeriodID: p.FiscalPeriodID,
			PayloadHash:    ReceiptPayloadHash(p),
			PrincipalID:    p.PrincipalID,
			MembershipID:   p.MembershipID,
			PolicyVersion:  p.PolicyVersion,
			Algorithm:      ReceiptAlgorithm,
			KeyID:          "ed25519:" + strings.Repeat("0", 64),
			IssuedAt:       p.IssuedAt,
		}
		b1 := CompleteReceiptBytes(envelope)
		if !bytes.Equal(b1, CompleteReceiptBytes(envelope)) {
			t.Fatalf("non-deterministic complete receipt bytes for version %q", version)
		}
		if ReceiptHash(envelope) != ReceiptHash(envelope) {
			t.Fatalf("non-deterministic receipt hash for version %q", version)
		}
	}
}

// FuzzCanonicalReceiptPayload fuzzes the receipt canonicalization byte
// contract (CanonicalReceiptPayload / CompleteReceiptBytes / ReceiptHash,
// internal/core/receipt.go) across the frozen payload versions. Input cap:
// 1 MiB (fuzzMaxInputBytes) — larger inputs are ignored before parsing.
func FuzzCanonicalReceiptPayload(f *testing.F) {
	// Starting seeds: the committed corpus under
	// testdata/fuzz/FuzzCanonicalReceiptPayload/ mirrors these (valid v0.4.0
	// and v0.9.0 payload documents, empty, 1 byte, partial/canonical-shape
	// violations, non-JSON).
	f.Add([]byte(validPayloadJSONv04))
	f.Add([]byte(validPayloadJSONv09))
	f.Add([]byte{})
	f.Add([]byte("{"))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"version":"receipt-payload/v0.4.0","principalRoles":["z","a","z",""]}`))
	f.Add([]byte(`not json`))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzMaxInputBytes {
			return
		}
		var p ReceiptPayload
		if err := json.Unmarshal(data, &p); err != nil {
			// Not a payload document: deterministic rejection, never a panic.
			return
		}
		checkReceiptInvariants(t, p)
	})
}
