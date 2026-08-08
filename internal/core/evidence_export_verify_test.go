// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module freezes the PURE OFFLINE VERIFIER of the
// deterministic evidence-lifecycle export bundle (WU-2 —
// core/evidence_export_verify.go):
//
//   - a valid store-shaped bundle (three objects — stored / purged /
//     intended — with REAL Ed25519-signed receipt chains and the immutable
//     execution ledger) verifies PASSED through every applicable layer;
//   - every listed tamper fails closed, and the failing LAYER is asserted per
//     case: a modified row re-hashes (bundle structural validity), a mutated
//     receipt envelope/payload is caught by the per-receipt layers even when
//     the tamperer re-committed the hashes (envelope integrity / payload
//     canonicalization), a swapped signing key is caught by signing-key
//     validity + signature, a swapped chain order is caught by chain
//     continuity even when re-hashed (the content hash commits to the chain
//     order), a wrong byte-state token is caught by byte-state consistency and
//     a cross-scope row by the fail-closed scope coverage;
//   - a no-signer-store bundle (completed purge, no receipts, no keys) also
//     verifies PASSED — skipped receipt layers never fail a report;
//   - the serialized entry strict-decodes (unknown fields and trailing data
//     rejected) and verifies the decoded model: any valid JSON encoding of the
//     same bundle verifies identically.
//
// The fixture uses a FIXED Ed25519 seed, so keys and Ed25519 signatures are
// deterministic across runs (Ed25519 is deterministic given the message).
package core

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// verifyExportKey is the deterministic Ed25519 signing key of the fixture.
type verifyExportKey struct {
	priv         ed25519.PrivateKey
	keyID        string
	publicKeyB64 string
}

// newVerifyExportKey mints a FIXED-seed Ed25519 key (deterministic across
// runs) with its canonical receipt key id and the padded-base64 public key the
// export model carries. offset varies the seed so a second key never collides.
func newVerifyExportKey(t *testing.T, offset byte) verifyExportKey {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i) + offset
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return verifyExportKey{
		priv:         priv,
		keyID:        ReceiptKeyID(pub),
		publicKeyB64: base64.StdEncoding.EncodeToString(pub),
	}
}

// signedExportReceipt builds ONE offline-verifiable exported receipt of the
// fixture: the canonical payload bytes (v0.9.0 for the purge acts, v0.7.0 for
// object_stored), the envelope's payload hash, a REAL Ed25519 signature over
// the canonical unsigned envelope and the recomputed receipt hash chained on
// prevHash.
func signedExportReceipt(t *testing.T, k verifyExportKey, subjectID string, action ReceiptAction, issuedAt, prevHash string) EvidenceExportReceipt {
	t.Helper()
	scope := exportTestScope()
	payload := ReceiptPayload{
		Version:        ReceiptPayloadVersionV07,
		SubjectType:    SubjectTypeEvidenceObject,
		SubjectID:      subjectID,
		Action:         action,
		TenantID:       scope.OrganizationID,
		CompanyID:      scope.CompanyID,
		FiscalPeriodID: scope.Period,
		EvidenceRef:    subjectID,
		PrincipalID:    "verify-principal",
		PolicyVersion:  "evidence-lifecycle-policy/v0.8.0",
		IssuedAt:       issuedAt,
	}
	switch action {
	case ReceiptActionPurgeIntent, ReceiptActionPurgeExecuted:
		payload.Version = ReceiptPayloadVersionV09
		payload.ReviewedLifecycleHash = exportHexA
		payload.ResultingLifecycleHash = exportHexB
		if action == ReceiptActionPurgeIntent {
			payload.ExecutionAttemptID = "00000000-0000-4000-8000-000000000601"
		}
	}
	receipt := SignedReceipt{
		SubjectType:         SubjectTypeEvidenceObject,
		SubjectID:           subjectID,
		Action:              action,
		TenantID:            scope.OrganizationID,
		CompanyID:           scope.CompanyID,
		FiscalPeriodID:      scope.Period,
		PayloadHash:         ReceiptPayloadHash(payload),
		PreviousReceiptHash: prevHash,
		PrincipalID:         payload.PrincipalID,
		PolicyVersion:       payload.PolicyVersion,
		Algorithm:           ReceiptAlgorithm,
		KeyID:               k.keyID,
		IssuedAt:            issuedAt,
	}
	receipt.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(k.priv, CanonicalUnsignedEnvelope(receipt)))
	return EvidenceExportReceipt{
		Receipt:     receipt,
		PayloadJSON: string(CanonicalReceiptPayload(payload)),
		ReceiptHash: ReceiptHash(receipt),
	}
}

// buildExportVerifyBundle assembles the VALID store-shaped fixture: object A
// stored (one object_stored receipt), object B purged (a SAME-SECOND
// two-receipt chain — object_stored genesis then the purge_executed completion;
// the canonical (subject, issuedAt, chain ordinal) order IS the chain order and
// the ordinal is the same-second tie-break) and object C intended (a
// receipt-covered purge intent whose completion never committed), with the
// immutable purge request/execution ledger and the exported public signing key.
// The manifest is built from the canonicalized bundle (self-hashing) and the
// baseline must fully validate.
func buildExportVerifyBundle(t *testing.T) (EvidenceExportBundle, verifyExportKey) {
	t.Helper()
	scope := exportTestScope()
	key := newVerifyExportKey(t, 1)

	objA := exportTestObject(exportHexA, scope.RUC, scope.Period)
	objB := exportTestObject(exportHexB, scope.RUC, scope.Period)
	objC := exportTestObject(exportHexC, scope.RUC, scope.Period)

	// Object A (stored): the object_stored genesis receipt.
	aStored := signedExportReceipt(t, key, objA.ObjectID, ReceiptActionObjectStored, "2026-01-15T12:00:00Z", "")

	// Object B (purged): object_stored genesis + purge_executed completion at the
	// SAME issuedAt — the chain ordinal is the same-second tie-break the
	// canonical sort preserves (the store's (issuedAt, insertion) emission
	// order).
	bStored := signedExportReceipt(t, key, objB.ObjectID, ReceiptActionObjectStored, "2026-01-18T12:00:00Z", "")
	bStored.ChainOrdinal = 0
	bExecuted := signedExportReceipt(t, key, objB.ObjectID, ReceiptActionPurgeExecuted, "2026-01-18T12:00:00Z", bStored.ReceiptHash)
	bExecuted.ChainOrdinal = 1

	// Object C (intended): object_stored genesis + purge_intent (crash-intent —
	// durable intent, no completion).
	cStored := signedExportReceipt(t, key, objC.ObjectID, ReceiptActionObjectStored, "2026-01-15T12:00:00Z", "")
	cStored.ChainOrdinal = 0
	cIntent := signedExportReceipt(t, key, objC.ObjectID, ReceiptActionPurgeIntent, "2026-01-17T12:00:00Z", cStored.ReceiptHash)
	cIntent.ChainOrdinal = 1

	requestB := EvidencePurgeRequest{
		RequestID: "22222222-2222-4222-8222-222222222222", ObjectID: objB.ObjectID,
		TenantID: scope.OrganizationID, CompanyID: scope.CompanyID, RUC: scope.RUC, Period: scope.Period,
		Category: "invoice", PolicyID: "11111111-1111-4111-8111-111111111111",
		RetentionStateSnapshot: RetentionEligibilityEligible, ReviewedLifecycleHash: exportHexA,
		Status: PurgeRequestStatusExecuted, RequestedAt: "2026-01-16T12:00:00Z", RequestedBy: "verify-requester",
		ApprovedAt: "2026-01-17T12:00:00Z", ExecutionID: "33333333-3333-4333-8333-333333333333",
	}
	requestC := EvidencePurgeRequest{
		RequestID: "22222222-2222-4222-8222-222222222221", ObjectID: objC.ObjectID,
		TenantID: scope.OrganizationID, CompanyID: scope.CompanyID, RUC: scope.RUC, Period: scope.Period,
		Category: "invoice", PolicyID: "11111111-1111-4111-8111-111111111111",
		RetentionStateSnapshot: RetentionEligibilityEligible, ReviewedLifecycleHash: exportHexA,
		Status: PurgeRequestStatusApproved, RequestedAt: "2026-01-16T12:00:00Z", RequestedBy: "verify-requester",
		ApprovedAt: "2026-01-17T12:00:00Z",
	}
	executionB := EvidencePurgeExecution{
		ExecutionID: "33333333-3333-4333-8333-333333333333", RequestID: requestB.RequestID, ObjectID: objB.ObjectID,
		RelPath: objB.RelPath, Size: objB.Size, PreRemovalHash: objB.ObjectID, IntentReviewedHash: exportHexA,
		State: PurgeExecutionCompleted, IntentAt: "2026-01-18T12:00:00Z", IntentBy: "verify-executor",
		CompletedAt: "2026-01-18T12:00:00Z", CompletedBy: "verify-executor",
		CompletionReceiptID: bExecuted.ReceiptHash,
	}
	executionC := EvidencePurgeExecution{
		ExecutionID: "33333333-3333-4333-8333-333333333331", RequestID: requestC.RequestID, ObjectID: objC.ObjectID,
		RelPath: objC.RelPath, Size: objC.Size, PreRemovalHash: objC.ObjectID, IntentReviewedHash: exportHexA,
		State: PurgeExecutionIntent, IntentAt: "2026-01-17T12:00:00Z", IntentBy: "verify-executor",
	}

	bundle := EvidenceExportBundle{
		Objects: []EvidenceObjectExport{
			{Object: objA, BytesState: EvidenceBytesStored},
			{Object: objB, BytesState: EvidenceBytesPurged, PurgeExecutedReceiptHash: bExecuted.ReceiptHash},
			{Object: objC, BytesState: EvidenceBytesIntended},
		},
		PurgeRequests:   []EvidencePurgeRequest{requestB, requestC},
		PurgeExecutions: []EvidencePurgeExecution{executionB, executionC},
		Receipts:        []EvidenceExportReceipt{aStored, bStored, bExecuted, cStored, cIntent},
		SigningKeys: []EvidenceExportSigningKey{{
			KeyID: key.keyID, Algorithm: ReceiptAlgorithm, PublicKey: key.publicKeyB64,
			CreatedAt: "2026-01-01T00:00:00Z",
		}},
	}
	CanonicalizeEvidenceExportBundle(&bundle)
	counts := EvidenceExportCountsOf(bundle)
	contentHash := ComputeEvidenceExportContentHash(bundle)
	bundle.Manifest = BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope}, counts, EvidenceExportGeneratedAt(bundle), contentHash)
	if err := AssertValidEvidenceExportBundle(bundle); err != nil {
		t.Fatalf("baseline verify bundle must validate: %v", err)
	}
	return bundle, key
}

// rehashExportBundle rebuilds the self-hashing manifest from the current
// bundle rows (the way the store emits a bundle) — it simulates a tamperer who
// re-committed the hashes, isolating the non-hash verifier layers.
func rehashExportBundle(b *EvidenceExportBundle) {
	CanonicalizeEvidenceExportBundle(b)
	counts := EvidenceExportCountsOf(*b)
	contentHash := ComputeEvidenceExportContentHash(*b)
	b.Manifest = BuildEvidenceExportManifest(b.Manifest.Scope, b.Manifest.Criteria, counts, EvidenceExportGeneratedAt(*b), contentHash)
}

// mutateExportReceipt applies fn to the receipt of one subject/action.
func mutateExportReceipt(t *testing.T, b *EvidenceExportBundle, subjectID string, action ReceiptAction, fn func(*EvidenceExportReceipt)) {
	t.Helper()
	for i := range b.Receipts {
		if b.Receipts[i].Receipt.SubjectID == subjectID && b.Receipts[i].Receipt.Action == action {
			fn(&b.Receipts[i])
			return
		}
	}
	t.Fatalf("test receipt not found: %s %s", subjectID, action)
}

// swapExportChainOrdinals swaps the chain ordinals of two receipts of one
// subject — a chain-ORDER mutation (the canonical (subject, issuedAt, ordinal)
// tie-break and the content hash commit to the chain order).
func swapExportChainOrdinals(t *testing.T, b *EvidenceExportBundle, subjectID string, first, second ReceiptAction) {
	t.Helper()
	var ords [2]int
	for i := range b.Receipts {
		if b.Receipts[i].Receipt.SubjectID != subjectID {
			continue
		}
		switch b.Receipts[i].Receipt.Action {
		case first:
			ords[0] = b.Receipts[i].ChainOrdinal
		case second:
			ords[1] = b.Receipts[i].ChainOrdinal
		}
	}
	for i := range b.Receipts {
		if b.Receipts[i].Receipt.SubjectID != subjectID {
			continue
		}
		switch b.Receipts[i].Receipt.Action {
		case first:
			b.Receipts[i].ChainOrdinal = ords[1]
		case second:
			b.Receipts[i].ChainOrdinal = ords[0]
		}
	}
}

// exportVerifyLayer returns one top-level layer of the report by name.
func exportVerifyLayer(t *testing.T, report EvidenceExportVerificationReport, name string) VerificationLayer {
	t.Helper()
	for _, l := range report.Layers {
		if l.Name == name {
			return l
		}
	}
	t.Fatalf("report misses layer %q (layers: %d)", name, len(report.Layers))
	return VerificationLayer{}
}

// TestVerifyEvidenceExportBundle freezes the offline verifier: the valid
// store-shaped fixture verifies PASSED, and every listed tamper fails closed
// with the EXACT layer that must catch it — even when the tamperer re-committed
// the hashes (the per-receipt, signing-key, chain-continuity and byte-state
// layers never trust the manifest).
func TestVerifyEvidenceExportBundle(t *testing.T) {
	// PASS — the valid fixture is fully offline-verifiable end to end.
	t.Run("export to verify passes", func(t *testing.T) {
		bundle, _ := buildExportVerifyBundle(t)
		report := VerifyEvidenceExportBundle(bundle)
		if report.Outcome != VerificationOutcomePassed {
			t.Fatalf("valid bundle must verify PASSED, got %s", report.Outcome)
		}
		for _, l := range report.Layers {
			if l.Status == VerificationFailed {
				t.Fatalf("layer %q failed on a valid bundle: %s", l.Name, l.Detail)
			}
		}
		if len(report.Receipts) != 5 || len(report.Objects) != 3 {
			t.Fatalf("diagnostics = %d receipts / %d objects, want 5 / 3", len(report.Receipts), len(report.Objects))
		}
		for i, r := range report.Receipts {
			if len(r.Layers) != len(ReceiptLayerNames()) {
				t.Fatalf("receipt[%d] layers = %d, want %d", i, len(r.Layers), len(ReceiptLayerNames()))
			}
		}
		if report.AccountingCorrectness != AccountingCorrectnessNotAsserted {
			t.Fatalf("report must end with %q, got %q", AccountingCorrectnessNotAsserted, report.AccountingCorrectness)
		}
	})

	// FAIL-CLOSED matrix: every tamper fails, and the layer that MUST catch it
	// is asserted. rehash=true simulates a tamperer who re-committed the
	// manifest hashes — the non-hash layers still fail closed.
	cases := map[string]struct {
		mutate           func(t *testing.T, b *EvidenceExportBundle)
		rehash           bool
		wantFailedLayer  string
		wantFailedDetail string
	}{
		"modified object row": {
			// A content tamper WITHOUT re-hashing: the recomputed content hash
			// differs from the committed contentManifestHash.
			mutate: func(t *testing.T, b *EvidenceExportBundle) {
				b.Objects[0].Object.SourceSystem = "tampered"
			},
			wantFailedLayer: LayerExportBundleStructural,
		},
		"receipt envelope mutation": {
			// A receipt envelope field tamper WITH re-hashed manifest: the
			// per-receipt envelope integrity layer catches the changed envelope
			// (recomputed digest differs from the stored receipt hash).
			mutate: func(t *testing.T, b *EvidenceExportBundle) {
				mutateExportReceipt(t, b, exportHexB, ReceiptActionPurgeExecuted, func(r *EvidenceExportReceipt) {
					r.Receipt.PrincipalID = "attacker"
				})
			},
			rehash:          true,
			wantFailedLayer: LayerEnvelopeIntegrity,
		},
		"receipt payload mutation": {
			// A payload bytes tamper WITH re-hashed manifest: the strict payload
			// decode rejects the garbage payload (unknown fields).
			mutate: func(t *testing.T, b *EvidenceExportBundle) {
				mutateExportReceipt(t, b, exportHexC, ReceiptActionPurgeIntent, func(r *EvidenceExportReceipt) {
					r.PayloadJSON = `{"bogus":true}`
				})
			},
			rehash:          true,
			wantFailedLayer: LayerPayloadCanonicalization,
		},
		"signing-key mutation": {
			// A swapped public key WITH re-hashed manifest: signing-key validity
			// derives the keyId from the new key and fails it (and the signature
			// no longer verifies).
			mutate: func(t *testing.T, b *EvidenceExportBundle) {
				other := newVerifyExportKey(t, 200)
				b.SigningKeys[0].PublicKey = other.publicKeyB64
			},
			rehash:          true,
			wantFailedLayer: LayerSigningKeyValidity,
		},
		"order mutation (swapped chain ordinals)": {
			// A chain-ORDER tamper WITH re-hashed manifest: the swapped ordinals
			// reorder the same-second chain, so the canonical chain walk breaks —
			// the completion receipt (previousReceiptHash non-empty) sorts first
			// and chain continuity fails (the content hash would also fail
			// without the re-hash — the ordinal is part of the canonical
			// content).
			mutate: func(t *testing.T, b *EvidenceExportBundle) {
				swapExportChainOrdinals(t, b, exportHexB, ReceiptActionObjectStored, ReceiptActionPurgeExecuted)
			},
			rehash:          true,
			wantFailedLayer: LayerChainLink,
		},
		"wrong byte state": {
			// A stored object relabeled purged WITH re-hashed manifest: the
			// byte-state consistency layer fails it (no completed execution
			// exists for the object).
			mutate: func(t *testing.T, b *EvidenceExportBundle) {
				b.Objects[0].BytesState = EvidenceBytesPurged
			},
			rehash:          true,
			wantFailedLayer: LayerExportByteStateConsistency,
		},
		"cross-scope object": {
			// A row that crosses the RUC boundary: the fail-closed scope
			// coverage rejects it with EXPORT_SCOPE_VIOLATION.
			mutate: func(t *testing.T, b *EvidenceExportBundle) {
				b.Objects[0].Object.RUC = "20600995804"
			},
			wantFailedLayer:  LayerExportBundleStructural,
			wantFailedDetail: "EXPORT_SCOPE_VIOLATION",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			bundle, _ := buildExportVerifyBundle(t)
			tc.mutate(t, &bundle)
			if tc.rehash {
				rehashExportBundle(&bundle)
			}
			report := VerifyEvidenceExportBundle(bundle)
			if report.Outcome != VerificationOutcomeFailed {
				t.Fatalf("tampered bundle must verify FAILED, got %s", report.Outcome)
			}
			layer := exportVerifyLayer(t, report, tc.wantFailedLayer)
			if layer.Status != VerificationFailed {
				t.Fatalf("layer %q = %s (%s), want failed", tc.wantFailedLayer, layer.Status, layer.Detail)
			}
			if tc.wantFailedDetail != "" && !strings.Contains(layer.Detail, tc.wantFailedDetail) {
				t.Fatalf("layer %q detail %q misses %q", tc.wantFailedLayer, layer.Detail, tc.wantFailedDetail)
			}
		})
	}
}

// TestVerifyEvidenceExportBundleNoSignerStore freezes the no-signer-store path:
// a purged object with a completed execution whose completion receipt id is
// empty (a deployment without a receipt signer) exports no receipts and no
// keys, and the verifier PASSES — the receipt layers are inapplicable and are
// skipped, never failing the report (an empty byte-state ledger claim is
// consistent when both the object row and the execution row carry no hash).
func TestVerifyEvidenceExportBundleNoSignerStore(t *testing.T) {
	scope := exportTestScope()
	obj := exportTestObject(exportHexA, scope.RUC, scope.Period)
	request := EvidencePurgeRequest{
		RequestID: "22222222-2222-4222-8222-222222222222", ObjectID: obj.ObjectID,
		TenantID: scope.OrganizationID, CompanyID: scope.CompanyID, RUC: scope.RUC, Period: scope.Period,
		Category: "invoice", PolicyID: "11111111-1111-4111-8111-111111111111",
		RetentionStateSnapshot: RetentionEligibilityEligible, ReviewedLifecycleHash: exportHexA,
		Status: PurgeRequestStatusExecuted, RequestedAt: "2026-01-16T12:00:00Z", RequestedBy: "verify-requester",
		ApprovedAt: "2026-01-17T12:00:00Z", ExecutionID: "33333333-3333-4333-8333-333333333333",
	}
	execution := EvidencePurgeExecution{
		ExecutionID: "33333333-3333-4333-8333-333333333333", RequestID: request.RequestID, ObjectID: obj.ObjectID,
		RelPath: obj.RelPath, Size: obj.Size, PreRemovalHash: obj.ObjectID, IntentReviewedHash: exportHexA,
		State: PurgeExecutionCompleted, IntentAt: "2026-01-18T12:00:00Z", IntentBy: "verify-executor",
		CompletedAt: "2026-01-18T12:00:00Z", CompletedBy: "verify-executor",
	}
	bundle := EvidenceExportBundle{
		Objects: []EvidenceObjectExport{{
			Object: obj, BytesState: EvidenceBytesPurged,
		}},
		PurgeRequests:   []EvidencePurgeRequest{request},
		PurgeExecutions: []EvidencePurgeExecution{execution},
	}
	// The no-signer store still emits a VALID self-hashing manifest stamped with
	// the applied scope and the verbatim criteria (the export is a read-only
	// query of the exact scope). The manifest must carry the fixture's actual
	// scope/criteria BEFORE the self-hash is (re)computed: rehashing an EMPTY
	// manifest would fail the structural scope validation instead of proving the
	// no-signer verification path.
	CanonicalizeEvidenceExportBundle(&bundle)
	counts := EvidenceExportCountsOf(bundle)
	contentHash := ComputeEvidenceExportContentHash(bundle)
	bundle.Manifest = BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope}, counts, EvidenceExportGeneratedAt(bundle), contentHash)
	rehashExportBundle(&bundle)

	report := VerifyEvidenceExportBundle(bundle)
	if report.Outcome != VerificationOutcomePassed {
		t.Fatalf("no-signer-store bundle must verify PASSED, got %s", report.Outcome)
	}
	if got := exportVerifyLayer(t, report, LayerExportByteStateConsistency); got.Status != VerificationPassed {
		t.Fatalf("byte-state layer = %s (%s), want passed", got.Status, got.Detail)
	}
	// The receipt layers are inapplicable — skipped, never failing.
	for _, name := range ReceiptLayerNames() {
		if got := exportVerifyLayer(t, report, name); got.Status != VerificationSkipped {
			t.Fatalf("receipt layer %q = %s (%s), want skipped (no receipts)", name, got.Status, got.Detail)
		}
	}
}

// TestVerifyEvidenceExportBundleJSON freezes the serialized entry point: the
// canonical wire bytes verify PASSED, ANY valid JSON encoding of the same
// bundle verifies identically (the verifier works on the decoded model), a
// malformed document (unknown fields, trailing data) is a decode error, and a
// decode-valid but self-inconsistent bundle yields a FAILED report, not an
// error.
func TestVerifyEvidenceExportBundleJSON(t *testing.T) {
	bundle, _ := buildExportVerifyBundle(t)
	data := CanonicalEvidenceExportBundleJSON(bundle)

	report, err := VerifyEvidenceExportBundleJSON(data)
	if err != nil {
		t.Fatalf("canonical bundle rejected by the serialized entry: %v", err)
	}
	if report.Outcome != VerificationOutcomePassed {
		t.Fatalf("canonical bundle must verify PASSED, got %s", report.Outcome)
	}

	// Alternate (pretty-printed) JSON encoding of the SAME model verifies
	// identically — the verifier consumes the decoded model, not the wire bytes.
	var pretty bytes.Buffer
	enc := json.NewEncoder(&pretty)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(bundle); err != nil {
		t.Fatalf("pretty-encode bundle: %v", err)
	}
	prettyReport, err := VerifyEvidenceExportBundleJSON(pretty.Bytes())
	if err != nil {
		t.Fatalf("pretty-printed bundle rejected: %v", err)
	}
	if prettyReport.Outcome != VerificationOutcomePassed || prettyReport.ExportID != report.ExportID {
		t.Fatalf("alternate encoding must verify identically: outcome %s / exportId %s", prettyReport.Outcome, prettyReport.ExportID)
	}

	// Malformed documents are decode errors (never a silent skip).
	if _, err := VerifyEvidenceExportBundleJSON([]byte(`{"bogus":true}`)); err == nil {
		t.Fatal("an unknown top-level field must be rejected by the strict decoder")
	}
	if _, err := VerifyEvidenceExportBundleJSON(append(append([]byte{}, data...), []byte(` {}`)...)); err == nil {
		t.Fatal("trailing data after the bundle object must be rejected by the strict decoder")
	}

	// A decode-valid but self-inconsistent bundle yields a FAILED report — the
	// verifier never trusts the manifest version/hashes.
	inconsistent, err := VerifyEvidenceExportBundleJSON([]byte(`{"manifest":{"version":"evidence-export/v0.8.0"}}`))
	if err != nil {
		t.Fatalf("a decode-valid bundle must not be a decode error: %v", err)
	}
	if inconsistent.Outcome != VerificationOutcomeFailed {
		t.Fatalf("a self-inconsistent bundle must verify FAILED, got %s", inconsistent.Outcome)
	}
}
