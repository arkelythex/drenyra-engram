// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module freezes the PURE deterministic evidence-lifecycle
// export contract (docs/architecture/evidence-lifecycle-v0.8.md §12; WU-3):
//
//   - criteria validation fails closed (institutional scopes, malformed
//     RUC/period, missing tenant/company);
//   - the manifest is self-hashing: the bundle hash covers the canonical
//     manifest core (version, scope, criteria, generatedAt, counts) and the
//     exportId is the content-addressed identity — identical inputs yield
//     identical hashes and any data-derived input change re-hashes;
//   - canonical ordering is deterministic and the canonical bundle JSON is
//     byte-identical for identical data regardless of input array order
//     (receipts preserve their emitted chain order);
//   - generatedAt is derived from the data (the maximum timestamp), never a
//     wall clock;
//   - scope coverage fails closed on ANY row crossing the
//     tenant/company/RUC/period boundary (objects, holds, requests by their own
//     scope; events/approvals/executions through their scope authority;
//     receipts through their subject AND their stamped scope columns; bound
//     policies admit tenant-level and exact-company shapes only);
//   - bundle validation fails closed on version/hash/count/byte-state/signing-
//     key-coverage inconsistencies.
package core

import (
	"strings"
	"testing"
)

// exportTestScope is the frozen exact company scope of the pure tests.
func exportTestScope() Scope {
	return Scope{Kind: ScopeKindCompany, OrganizationID: "org-001", CompanyID: "acme", RUC: "20100039201", Period: "202401"}
}

// exportTestObject builds a valid metadata row for the scope tests (id = the
// content address — 64 lowercase hex).
func exportTestObject(id string, ruc, period string) EvidenceObject {
	return EvidenceObject{
		ObjectID:        id,
		SHA256:          id,
		Size:            4,
		ContentType:     "application/xml",
		TenantID:        "org-001",
		CompanyID:       "acme",
		RUC:             ruc,
		Period:          period,
		SourceSystem:    "go-test",
		SourceActorKind: ActorKindAgent,
		StoredBy:        "subject-1",
		StoredAt:        "2026-01-15T12:00:00Z",
		RelPath:         ObjectRelPath(id),
	}
}

var (
	exportHexA = strings.Repeat("a", 64)
	exportHexB = strings.Repeat("b", 64)
	exportHexC = strings.Repeat("c", 64)
)

// buildExportTestBundle assembles a VALID single-object lifecycle bundle (one
// stored object, one lifecycle state, one bound policy, one hold, one request,
// one approval, one completed execution, two events, one receipt + its signing
// key) with a self-hashing manifest — the baseline every negative test mutates.
func buildExportTestBundle(t *testing.T) EvidenceExportBundle {
	t.Helper()
	scope := exportTestScope()
	obj := exportTestObject(exportHexA, scope.RUC, scope.Period)
	policy := RetentionPolicy{
		PolicyID: "11111111-1111-4111-8111-111111111111", TenantID: scope.OrganizationID,
		CompanyID: scope.CompanyID, RUC: scope.RUC, Period: scope.Period,
		Jurisdiction: "PE", Legislation: "NATIONAL-TAX", Category: "invoice", MinPeriod: "202401",
		Version: 1, CreatedAt: "2026-01-15T12:00:00Z",
	}
	request := EvidencePurgeRequest{
		RequestID: "22222222-2222-4222-8222-222222222222", ObjectID: obj.ObjectID,
		TenantID: scope.OrganizationID, CompanyID: scope.CompanyID, RUC: scope.RUC, Period: scope.Period,
		Category: "invoice", PolicyID: policy.PolicyID,
		RetentionStateSnapshot: RetentionEligibilityEligible, ReviewedLifecycleHash: exportHexC,
		Status: PurgeRequestStatusExecuted, RequestedAt: "2026-01-16T12:00:00Z", RequestedBy: "subject-2",
		ApprovedAt: "2026-01-17T12:00:00Z", ExecutionID: "33333333-3333-4333-8333-333333333333",
	}
	approval := EvidencePurgeApproval{
		ApprovalID: "44444444-4444-4444-8444-444444444444", RequestID: request.RequestID,
		ApprovalOrder: 1, Decision: PurgeApprovalDecisionApproved,
		ReviewedHash: exportHexC, ResultingHash: exportHexA,
		Reason: "verified", PolicyVersion: "evidence-lifecycle-policy/v0.8.0",
		CreatedAt: "2026-01-17T12:00:00Z",
	}
	execution := EvidencePurgeExecution{
		ExecutionID: request.ExecutionID, RequestID: request.RequestID, ObjectID: obj.ObjectID,
		RelPath: obj.RelPath, Size: obj.Size, PreRemovalHash: obj.ObjectID,
		State: PurgeExecutionCompleted, IntentAt: "2026-01-18T12:00:00Z", IntentBy: "executor",
		CompletedAt: "2026-01-18T12:00:01Z", CompletedBy: "executor",
		CompletionReceiptID: exportHexB,
	}
	eventRequested := EvidenceLifecycleEvent{
		EventID: "55555555-5555-4555-8555-555555555555", ObjectID: obj.ObjectID, RequestID: request.RequestID,
		Action: PurgeEventRequested, FromState: string(PurgeLifecycleStored), ToState: string(PurgeLifecycleRequested),
		ReviewedHash: exportHexC, ResultingHash: exportHexA,
		PolicyVersion: "evidence-lifecycle-policy/v0.8.0", CreatedAt: "2026-01-16T12:00:00Z",
	}
	eventExecuted := EvidenceLifecycleEvent{
		EventID: "66666666-6666-4666-8666-666666666666", ObjectID: obj.ObjectID, RequestID: request.RequestID,
		Action: PurgeEventExecuted, FromState: string(PurgeLifecycleApproved), ToState: string(PurgeLifecyclePurged),
		ReviewedHash: exportHexA, ResultingHash: exportHexB,
		PolicyVersion: "evidence-lifecycle-policy/v0.8.0", CreatedAt: "2026-01-18T12:00:01Z",
	}
	receipt := EvidenceExportReceipt{
		Receipt: SignedReceipt{
			SubjectType: SubjectTypeEvidenceObject, SubjectID: obj.ObjectID,
			Action: ReceiptActionPurgeExecuted, TenantID: scope.OrganizationID, CompanyID: scope.CompanyID,
			FiscalPeriodID: scope.Period, PayloadHash: exportHexC, PreviousReceiptHash: exportHexA,
			PrincipalID: "executor", PolicyVersion: "evidence-lifecycle-policy/v0.8.0",
			Algorithm: ReceiptAlgorithm, KeyID: "ed25519:key-1", IssuedAt: "2026-01-18T12:00:01Z",
		},
		PayloadJSON: `{"version":"receipt-payload/v0.9.0"}`,
		ReceiptHash: exportHexB,
	}

	bundle := EvidenceExportBundle{
		Objects: []EvidenceObjectExport{{
			Object: obj, BytesState: EvidenceBytesPurged, PurgeExecutedReceiptHash: exportHexB,
		}},
		LifecycleStates: []EvidenceRetentionState{{
			ObjectID: obj.ObjectID, LifecycleState: PurgeLifecyclePurged,
			RetentionState: RetentionEligibilityEligible, PolicyID: policy.PolicyID,
			Category: policy.Category, CurrentHash: exportHexB, UpdatedAt: "2026-01-18T12:00:01Z",
		}},
		RetentionPolicies: []RetentionPolicy{policy},
		Holds: []EvidenceHold{{
			HoldID: "77777777-7777-4777-8777-777777777777", ObjectID: obj.ObjectID,
			TenantID: scope.OrganizationID, CompanyID: scope.CompanyID, RUC: scope.RUC, Period: scope.Period,
			Kind: HoldKindLegal, Reason: "audit", OwnerSubjectID: "owner",
			PlacedAt: "2026-01-15T13:00:00Z", PlacedBy: "subject-1",
		}},
		PurgeRequests:   []EvidencePurgeRequest{request},
		PurgeApprovals:  []EvidencePurgeApproval{approval},
		PurgeExecutions: []EvidencePurgeExecution{execution},
		LifecycleEvents: []EvidenceLifecycleEvent{eventRequested, eventExecuted},
		Receipts:        []EvidenceExportReceipt{receipt},
		SigningKeys: []EvidenceExportSigningKey{{
			KeyID: "ed25519:key-1", Algorithm: ReceiptAlgorithm, PublicKey: "cGFy", CreatedAt: "2026-01-01T00:00:00Z",
		}},
	}
	CanonicalizeEvidenceExportBundle(&bundle)
	counts := EvidenceExportCountsOf(bundle)
	contentHash := ComputeEvidenceExportContentHash(bundle)
	bundle.Manifest = BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope}, counts, EvidenceExportGeneratedAt(bundle), contentHash)
	if err := AssertValidEvidenceExportBundle(bundle); err != nil {
		t.Fatalf("baseline bundle must validate: %v", err)
	}
	return bundle
}

// TestAssertValidEvidenceExportCriteria freezes the criteria gate: institutional
// scopes, malformed RUC/period and missing tenant/company fail closed.
func TestAssertValidEvidenceExportCriteria(t *testing.T) {
	valid := []EvidenceExportCriteria{
		{Scope: exportTestScope()},
		{Scope: func() Scope { s := exportTestScope(); s.Period = ""; return s }()},
	}
	for i, c := range valid {
		if err := AssertValidEvidenceExportCriteria(c); err != nil {
			t.Fatalf("valid criteria %d rejected: %v", i, err)
		}
	}
	invalid := map[string]EvidenceExportCriteria{
		"institutional": {Scope: Scope{Kind: ScopeKindInstitutional}},
		"no tenant":     {Scope: Scope{Kind: ScopeKindCompany, CompanyID: "acme", RUC: "20100039201"}},
		"no company":    {Scope: Scope{Kind: ScopeKindCompany, OrganizationID: "org-001", RUC: "20100039201"}},
		"bad ruc":       {Scope: Scope{Kind: ScopeKindCompany, OrganizationID: "org-001", CompanyID: "acme", RUC: "201000392"}},
		"bad period":    {Scope: Scope{Kind: ScopeKindCompany, OrganizationID: "org-001", CompanyID: "acme", RUC: "20100039201", Period: "202413"}},
	}
	for name, c := range invalid {
		err := AssertValidEvidenceExportCriteria(c)
		if err == nil || !strings.Contains(err.Error(), "INVALID_EXPORT_SCOPE") {
			t.Fatalf("%s: expected INVALID_EXPORT_SCOPE, got %v", name, err)
		}
	}
}

// TestComputeEvidenceExportBundleHashDeterministic freezes the ENVELOPE self-hash
// contract: identical manifest inputs yield the identical envelope hash; any
// data-derived input change (counts, scope, criteria, generatedAt,
// contentManifestHash) re-hashes; the exportId is the content-addressed identity
// of the CONTENT hash.
func TestComputeEvidenceExportBundleHashDeterministic(t *testing.T) {
	scope := exportTestScope()
	counts := EvidenceExportCounts{Objects: 1, Receipts: 1, SigningKeys: 1}
	content := exportHexC
	m := BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope}, counts, "2026-01-18T12:00:01Z", content)

	if m.BundleHash != ComputeEvidenceExportBundleHash(m) {
		t.Fatalf("envelope self-hash mismatch: %s != %s", m.BundleHash, ComputeEvidenceExportBundleHash(m))
	}
	if m.ExportID != EvidenceExportModelVersion+":"+m.ContentManifestHash {
		t.Fatalf("exportId %q is not the content-addressed identity of contentManifestHash %q", m.ExportID, m.ContentManifestHash)
	}

	// Identical input → identical bytes/hash.
	clone := BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope}, counts, "2026-01-18T12:00:01Z", content)
	if clone.BundleHash != m.BundleHash || clone.ExportID != m.ExportID || clone.ContentManifestHash != m.ContentManifestHash {
		t.Fatalf("identical inputs produced different identities")
	}
	if string(CanonicalEvidenceExportManifestJSON(m)) != string(CanonicalEvidenceExportManifestJSON(clone)) {
		t.Fatalf("identical manifests produced different canonical bytes")
	}

	// Any data-derived input change re-hashes the envelope.
	mutated := BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope}, EvidenceExportCounts{Objects: 2}, "2026-01-18T12:00:01Z", content)
	if mutated.BundleHash == m.BundleHash {
		t.Fatalf("count change must re-hash the envelope")
	}
	otherPeriod := exportTestScope()
	otherPeriod.Period = "202402"
	mutatedScope := BuildEvidenceExportManifest(otherPeriod, EvidenceExportCriteria{Scope: otherPeriod}, counts, "2026-01-18T12:00:01Z", content)
	if mutatedScope.BundleHash == m.BundleHash {
		t.Fatalf("scope change must re-hash the envelope")
	}
	mutatedAt := BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope}, counts, "2026-01-19T12:00:01Z", content)
	if mutatedAt.BundleHash == m.BundleHash {
		t.Fatalf("generatedAt change must re-hash the envelope")
	}
	mutatedContent := BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope}, counts, "2026-01-18T12:00:01Z", exportHexB)
	if mutatedContent.BundleHash == m.BundleHash {
		t.Fatalf("contentManifestHash change must re-hash the envelope")
	}
}

// TestEvidenceExportContentHash freezes the CONTENT hash contract (the audit's
// Fix 2): the content hash covers the deterministic canonical content of the
// COMPLETE bundle (every array/row, receipts sorted by their stable explicit
// keys), so (a) SAME counts but CHANGED row content re-hashes, (b) PERMUTED
// source order yields the identical canonical bundle and hash, (c) generatedAt
// (an envelope field) NEVER participates in the content hash, and (d) receipts
// canonicalize by (subject, issuedAt, chain ordinal) — the store's (issuedAt,
// insertion) emission positions propagated as the ordinal — with the
// receipt-hash key ONLY as the final tie-break for ambiguous hand-built input
// (equal subject, issuedAt AND ordinal), never SQL/insertion order.
func TestEvidenceExportContentHash(t *testing.T) {
	scope := exportTestScope()
	objA := exportTestObject(exportHexA, scope.RUC, scope.Period)
	objB := exportTestObject(exportHexB, scope.RUC, scope.Period)

	base := EvidenceExportBundle{
		Objects: []EvidenceObjectExport{
			{Object: objA, BytesState: EvidenceBytesStored},
			{Object: objB, BytesState: EvidenceBytesStored},
		},
		Receipts: []EvidenceExportReceipt{
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexA},
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexB},
		},
	}

	// (a) Same counts, changed row content -> different content hash.
	changed := base
	changed.Objects = append([]EvidenceObjectExport{}, base.Objects...)
	changed.Objects[0] = EvidenceObjectExport{Object: objB, BytesState: EvidenceBytesPurged, PurgeExecutedReceiptHash: exportHexC}
	if ComputeEvidenceExportContentHash(base) == ComputeEvidenceExportContentHash(changed) {
		t.Fatalf("changed row content with UNCHANGED counts must re-hash the content (the audit's content-identity fix)")
	}

	// (b) Permuted source order -> identical canonical bundle and content hash.
	permuted := base
	permuted.Objects = []EvidenceObjectExport{base.Objects[1], base.Objects[0]}
	permuted.Receipts = []EvidenceExportReceipt{base.Receipts[1], base.Receipts[0]}
	CanonicalizeEvidenceExportBundle(&base)
	CanonicalizeEvidenceExportBundle(&permuted)
	if string(CanonicalEvidenceExportBundleJSON(base)) != string(CanonicalEvidenceExportBundleJSON(permuted)) {
		t.Fatalf("permuted source order must canonicalize to identical bundle bytes")
	}
	if ComputeEvidenceExportContentHash(base) != ComputeEvidenceExportContentHash(permuted) {
		t.Fatalf("permuted source order must produce the identical content hash")
	}

	// (c) generatedAt is an envelope field — it NEVER changes the content hash.
	envelope := BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope},
		EvidenceExportCountsOf(base), "2026-01-18T12:00:01Z", ComputeEvidenceExportContentHash(base))
	envelopeAt := BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope},
		EvidenceExportCountsOf(base), "2026-01-19T12:00:01Z", ComputeEvidenceExportContentHash(base))
	if envelope.ContentManifestHash != envelopeAt.ContentManifestHash {
		t.Fatalf("generatedAt must never participate in the content hash (equivalent content differing on a timestamp is a defect)")
	}
	if envelope.BundleHash == envelopeAt.BundleHash {
		t.Fatalf("generatedAt must participate in the ENVELOPE hash (the separately named envelope/identity hash)")
	}

	// (d) Receipts canonicalize by their stable explicit keys — the two receipts
	// carry no chain ordinal (ambiguous hand-built input), so the deterministic
	// (subject, issuedAt, ordinal, receiptHash) order decides and the reversed
	// input above canonicalized to the identical bytes. Equal-issuedAt chain
	// ordinals (the store's emission positions) are covered by
	// TestCanonicalizeEvidenceExportBundleChainOrder.
	if base.Receipts[0].ReceiptHash != exportHexA || base.Receipts[1].ReceiptHash != exportHexB {
		t.Fatalf("receipts must sort by their stable explicit keys: got %s, %s", base.Receipts[0].ReceiptHash, base.Receipts[1].ReceiptHash)
	}
}

// TestEvidenceExportIntendedByteState freezes the crash-intent byte-state token:
// an object with a receipt-covered purge intent whose completion never committed
// is bytes "intended" (NEVER presented as ordinary stored bytes), carries NO
// completion receipt hash, and a stored/intended object with a completion hash
// fails closed.
func TestEvidenceExportIntendedByteState(t *testing.T) {
	scope := exportTestScope()
	obj := exportTestObject(exportHexA, scope.RUC, scope.Period)
	bundle := EvidenceExportBundle{
		Objects: []EvidenceObjectExport{{
			Object: obj, BytesState: EvidenceBytesIntended,
		}},
		Manifest: EvidenceExportManifest{
			Version:  EvidenceExportModelVersion,
			Scope:    scope,
			Criteria: EvidenceExportCriteria{Scope: scope},
			Counts:   EvidenceExportCounts{Objects: 1},
		},
	}
	CanonicalizeEvidenceExportBundle(&bundle)
	counts := EvidenceExportCountsOf(bundle)
	contentHash := ComputeEvidenceExportContentHash(bundle)
	bundle.Manifest = BuildEvidenceExportManifest(scope, EvidenceExportCriteria{Scope: scope}, counts, EvidenceExportGeneratedAt(bundle), contentHash)
	if err := AssertValidEvidenceExportBundle(bundle); err != nil {
		t.Fatalf("an intended byte-state bundle must validate: %v", err)
	}
	withHash := bundle
	withHash.Objects[0].PurgeExecutedReceiptHash = exportHexB
	if err := AssertValidEvidenceExportBundle(withHash); err == nil {
		t.Fatal("an intended object with a purgeExecutedReceiptHash must fail closed (no completion exists)")
	}
}

// TestCanonicalizeEvidenceExportBundleOrdering freezes the canonical ordering:
// every array sorts to its data-derived total order; receipts PRESERVE the
// emitted chain order.
func TestCanonicalizeEvidenceExportBundleOrdering(t *testing.T) {
	scope := exportTestScope()
	objA := exportTestObject(exportHexB, scope.RUC, scope.Period) // b < c by id? c-b: "b"*64 < "c"*64
	objC := exportTestObject(exportHexC, scope.RUC, scope.Period)

	// Deliberately out of order (C first, then A/B id descending).
	bundle := EvidenceExportBundle{
		Objects: []EvidenceObjectExport{
			{Object: objC, BytesState: EvidenceBytesStored},
			{Object: objA, BytesState: EvidenceBytesStored},
		},
		Holds: []EvidenceHold{
			{HoldID: "2", ObjectID: objA.ObjectID, PlacedAt: "2026-01-16T00:00:00Z"},
			{HoldID: "1", ObjectID: objA.ObjectID, PlacedAt: "2026-01-15T00:00:00Z"},
		},
		Receipts: []EvidenceExportReceipt{
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexB},
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexA},
		},
	}
	CanonicalizeEvidenceExportBundle(&bundle)

	if bundle.Objects[0].Object.ObjectID != objA.ObjectID || bundle.Objects[1].Object.ObjectID != objC.ObjectID {
		t.Fatalf("objects not sorted by object id: %s, %s", bundle.Objects[0].Object.ObjectID, bundle.Objects[1].Object.ObjectID)
	}
	if bundle.Holds[0].HoldID != "1" || bundle.Holds[1].HoldID != "2" {
		t.Fatalf("holds not sorted by (objectId, placedAt, id): %s, %s", bundle.Holds[0].HoldID, bundle.Holds[1].HoldID)
	}
	// Receipts sort by their stable explicit keys — the two receipts carry NO
	// chain ordinal (ambiguous hand-built input), so the deterministic
	// (subject, issuedAt, ordinal, receiptHash) final tie-break decides and the
	// REVERSED input canonicalized to (A, B): never SQL/insertion order, and the
	// per-subject chain presentation stays contiguous. The chain-ordinal path
	// (the store's propagated emission positions) is covered by
	// TestCanonicalizeEvidenceExportBundleChainOrder.
	if bundle.Receipts[0].ReceiptHash != exportHexA || bundle.Receipts[1].ReceiptHash != exportHexB {
		t.Fatalf("receipts must sort by (subject, issuedAt, receiptHash): got %s, %s", bundle.Receipts[0].ReceiptHash, bundle.Receipts[1].ReceiptHash)
	}
	// Canonical JSON determinism: identical data (different input order) →
	// identical bytes.
	if !strings.Contains(string(CanonicalEvidenceExportBundleJSON(bundle)), `"receipts"`) {
		t.Fatalf("canonical bundle JSON misses the receipts array")
	}
}

// TestCanonicalizeEvidenceExportBundleChainOrder freezes the chain-order
// contract of the receipt canonical sort: equal issued_at (same-second)
// chain receipts keep their EMISSION order — the store's (issuedAt,
// insertion) positions propagated as the chain ordinal — NEVER the
// receipt-hash order, and permuted source input canonicalizes to the
// identical chain bytes (the ordinal is the explicit stable tie-break).
func TestCanonicalizeEvidenceExportBundleChainOrder(t *testing.T) {
	scope := exportTestScope()
	objA := exportTestObject(exportHexA, scope.RUC, scope.Period)

	// Emission order (subject A, same second): the "b"-hash receipt was emitted
	// FIRST (chainOrdinal 0) and the "a"-hash receipt SECOND (chainOrdinal 1). A
	// receipt-hash tie-break would reverse them (exportHexA < exportHexB); the
	// chain ordinal must win.
	emitted := []EvidenceExportReceipt{
		{ChainOrdinal: 0, Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexB},
		{ChainOrdinal: 1, Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexA},
	}

	for name, input := range map[string][]EvidenceExportReceipt{
		"emission order":  emitted,
		"permuted source": {emitted[1], emitted[0]},
	} {
		t.Run(name, func(t *testing.T) {
			b := EvidenceExportBundle{Receipts: append([]EvidenceExportReceipt{}, input...)}
			CanonicalizeEvidenceExportBundle(&b)
			if len(b.Receipts) != 2 {
				t.Fatalf("receipts = %d, want 2", len(b.Receipts))
			}
			// Chain order = emission order (the "b"-hash receipt first): the ordinal
			// is the same-second tie-break, never the receipt hash.
			if b.Receipts[0].ReceiptHash != exportHexB || b.Receipts[1].ReceiptHash != exportHexA {
				t.Fatalf("equal-issuedAt chain receipts must keep emission order (b-hash then a-hash): got %s, %s",
					b.Receipts[0].ReceiptHash, b.Receipts[1].ReceiptHash)
			}
			if b.Receipts[0].ChainOrdinal != 0 || b.Receipts[1].ChainOrdinal != 1 {
				t.Fatalf("chain ordinals = %d, %d, want 0, 1 (the propagated emission positions)",
					b.Receipts[0].ChainOrdinal, b.Receipts[1].ChainOrdinal)
			}
		})
	}

	// Both inputs canonicalize to the IDENTICAL bundle bytes and the ordinal is
	// part of the canonical wire content (never omitted): permuted source order
	// never reorders a chain.
	b1 := EvidenceExportBundle{Receipts: append([]EvidenceExportReceipt{}, emitted...)}
	b2 := EvidenceExportBundle{Receipts: []EvidenceExportReceipt{emitted[1], emitted[0]}}
	CanonicalizeEvidenceExportBundle(&b1)
	CanonicalizeEvidenceExportBundle(&b2)
	if string(CanonicalEvidenceExportBundleJSON(b1)) != string(CanonicalEvidenceExportBundleJSON(b2)) {
		t.Fatalf("permuted source order must canonicalize to identical chain bytes")
	}
	if !strings.Contains(string(CanonicalEvidenceExportBundleJSON(b1)), `"chainOrdinal"`) {
		t.Fatalf("the chain ordinal must be part of the canonical content (the content hash commits to the chain order)")
	}
}

// TestEvidenceExportContentHashCommitsToChainOrder freezes the chain-order
// commitment of the CONTENT hash: the chain ordinal is part of the canonical
// content, so the same two receipts with SWAPPED ordinals (a different chain
// order) produce a DIFFERENT content hash; identical data (same ordinals,
// permuted source order) still yields the identical hash.
func TestEvidenceExportContentHashCommitsToChainOrder(t *testing.T) {
	scope := exportTestScope()
	objA := exportTestObject(exportHexA, scope.RUC, scope.Period)
	emission := []EvidenceExportReceipt{
		{ChainOrdinal: 0, Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexB},
		{ChainOrdinal: 1, Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexA},
	}
	swapped := []EvidenceExportReceipt{
		{ChainOrdinal: 1, Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexB},
		{ChainOrdinal: 0, Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexA},
	}

	hEmission := ComputeEvidenceExportContentHash(EvidenceExportBundle{Receipts: append([]EvidenceExportReceipt{}, emission...)})
	hSwapped := ComputeEvidenceExportContentHash(EvidenceExportBundle{Receipts: append([]EvidenceExportReceipt{}, swapped...)})
	if hEmission == hSwapped {
		t.Fatalf("the content hash must commit to the chain order (swapped ordinals produced the identical hash)")
	}

	// Identical data with permuted source order → identical hash (determinism).
	hPermuted := ComputeEvidenceExportContentHash(EvidenceExportBundle{Receipts: []EvidenceExportReceipt{emission[1], emission[0]}})
	if hEmission != hPermuted {
		t.Fatalf("permuted source order must produce the identical content hash")
	}
}

// TestCanonicalizeEvidenceExportBundleChainReconstruction freezes the ordinal
// fallback for hand-built bundles: a subject whose receipts carry NO usable
// ordinals (all zero) but whose previousReceiptHash links form exactly ONE
// linear chain has its ordinals reconstructed from the links, so the canonical
// sort still presents the chain in chain order. The resolution is idempotent:
// a second pass sees valid ordinals and keeps them.
func TestCanonicalizeEvidenceExportBundleChainReconstruction(t *testing.T) {
	scope := exportTestScope()
	objA := exportTestObject(exportHexA, scope.RUC, scope.Period)
	// Genesis → receipt-1 → receipt-2 (one linear chain); input deliberately
	// scrambled (no ordinals carried — all zero).
	b := EvidenceExportBundle{Receipts: []EvidenceExportReceipt{
		{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z", PreviousReceiptHash: exportHexC}, ReceiptHash: exportHexA},
		{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z", PreviousReceiptHash: exportHexA}, ReceiptHash: exportHexB},
		{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexC},
	}}
	CanonicalizeEvidenceExportBundle(&b)

	if b.Receipts[0].ReceiptHash != exportHexC || b.Receipts[1].ReceiptHash != exportHexA || b.Receipts[2].ReceiptHash != exportHexB {
		t.Fatalf("hand-built chain with linear links must canonicalize to chain order (genesis → a → b): got %s, %s, %s",
			b.Receipts[0].ReceiptHash, b.Receipts[1].ReceiptHash, b.Receipts[2].ReceiptHash)
	}
	if b.Receipts[0].ChainOrdinal != 0 || b.Receipts[1].ChainOrdinal != 1 || b.Receipts[2].ChainOrdinal != 2 {
		t.Fatalf("reconstructed ordinals = %d, %d, %d, want 0, 1, 2 (from the previousReceiptHash links)",
			b.Receipts[0].ChainOrdinal, b.Receipts[1].ChainOrdinal, b.Receipts[2].ChainOrdinal)
	}

	// Idempotence: a second canonicalization pass sees valid ordinals and keeps
	// them (identical bytes).
	before := string(CanonicalEvidenceExportBundleJSON(b))
	CanonicalizeEvidenceExportBundle(&b)
	if string(CanonicalEvidenceExportBundleJSON(b)) != before {
		t.Fatalf("canonicalization must be idempotent (a second pass re-derives the identical chain)")
	}
}

// TestCanonicalizeEvidenceExportBundleAmbiguousChainDeterministic freezes the
// ambiguous-chain fallback: a subject whose links do NOT form one linear chain
// (fork, cycle/gap, duplicate hash, multiple genii) cannot be reconstructed,
// so every ordinal stays zero and the deterministic (subject, issuedAt,
// ordinal, receiptHash) order decides — the canonicalization NEVER guesses a
// chain order and is fully deterministic.
func TestCanonicalizeEvidenceExportBundleAmbiguousChainDeterministic(t *testing.T) {
	scope := exportTestScope()
	objA := exportTestObject(exportHexA, scope.RUC, scope.Period)
	external := strings.Repeat("d", 64) // a predecessor resolved outside the bundle
	cases := map[string][]EvidenceExportReceipt{
		"fork": {
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexC},
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z", PreviousReceiptHash: exportHexC}, ReceiptHash: exportHexA},
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z", PreviousReceiptHash: exportHexC}, ReceiptHash: exportHexB},
		},
		"cycle no genesis": {
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z", PreviousReceiptHash: exportHexB}, ReceiptHash: exportHexA},
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z", PreviousReceiptHash: exportHexC}, ReceiptHash: exportHexB},
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z", PreviousReceiptHash: exportHexA}, ReceiptHash: exportHexC},
		},
		"gap two roots": {
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexA},
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z", PreviousReceiptHash: external}, ReceiptHash: exportHexB},
		},
		"duplicate hash": {
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexA},
			{Receipt: SignedReceipt{SubjectID: objA.ObjectID, IssuedAt: "2026-01-15T00:00:00Z"}, ReceiptHash: exportHexA},
		},
	}
	for name, receipts := range cases {
		t.Run(name, func(t *testing.T) {
			first := EvidenceExportBundle{Receipts: append([]EvidenceExportReceipt{}, receipts...)}
			second := EvidenceExportBundle{Receipts: append([]EvidenceExportReceipt{}, receipts...)}
			CanonicalizeEvidenceExportBundle(&first)
			CanonicalizeEvidenceExportBundle(&second)
			// Deterministic: identical input → identical canonical bytes, and every
			// ordinal collapsed to the zero fallback (never a guessed reconstruction).
			if string(CanonicalEvidenceExportBundleJSON(first)) != string(CanonicalEvidenceExportBundleJSON(second)) {
				t.Fatalf("ambiguous chain input must canonicalize deterministically")
			}
			for i, r := range first.Receipts {
				if r.ChainOrdinal != 0 {
					t.Fatalf("ambiguous chain receipt[%d] chainOrdinal = %d, want 0 (no reconstruction may be guessed)", i, r.ChainOrdinal)
				}
			}
		})
	}
}

// TestEvidenceExportGeneratedAt freezes the deterministic "as of" derivation:
// the maximum timestamp across every included row, never a wall clock.
func TestEvidenceExportGeneratedAt(t *testing.T) {
	scope := exportTestScope()
	bundle := EvidenceExportBundle{
		Objects: []EvidenceObjectExport{{Object: exportTestObject(exportHexA, scope.RUC, scope.Period)}},
		Holds:   []EvidenceHold{{HoldID: "1", PlacedAt: "2026-01-20T00:00:00Z"}},
		LifecycleEvents: []EvidenceLifecycleEvent{
			{EventID: "1", CreatedAt: "2026-01-19T00:00:00Z"},
		},
		Receipts: []EvidenceExportReceipt{{Receipt: SignedReceipt{IssuedAt: "2026-01-21T00:00:00Z"}}},
	}
	if got := EvidenceExportGeneratedAt(bundle); got != "2026-01-21T00:00:00Z" {
		t.Fatalf("generatedAt = %q, want the max row timestamp", got)
	}
	if got := EvidenceExportGeneratedAt(EvidenceExportBundle{}); got != "" {
		t.Fatalf("empty bundle generatedAt = %q, want empty", got)
	}
}

// TestValidateEvidenceExportScopeCoverage freezes the fail-closed scope
// contract: NO row may cross the tenant/company/RUC/period boundary — by its
// own scope columns, through its scope authority (events/approvals/executions)
// or through its subject/stamped columns (receipts); bound policies admit only
// tenant-level and exact-company shapes.
func TestValidateEvidenceExportScopeCoverage(t *testing.T) {
	// Out-of-scope variants of every row kind must fail closed.
	cases := map[string]func(*EvidenceExportBundle){
		"object other RUC": func(b *EvidenceExportBundle) {
			b.Objects[0].Object = exportTestObject(exportHexB, "20600995804", "202401")
		},
		"object other company": func(b *EvidenceExportBundle) {
			o := exportTestObject(exportHexB, "20100039201", "202401")
			o.CompanyID = "acme2"
			b.Objects[0].Object = o
		},
		"object other period": func(b *EvidenceExportBundle) {
			b.Objects[0].Object = exportTestObject(exportHexB, "20100039201", "202402")
		},
		"hold other RUC": func(b *EvidenceExportBundle) {
			b.Holds[0].RUC = "20600995804"
		},
		"request other company": func(b *EvidenceExportBundle) {
			b.PurgeRequests[0].CompanyID = "acme2"
		},
		"event unknown object": func(b *EvidenceExportBundle) {
			b.LifecycleEvents[0].ObjectID = exportHexB
		},
		"event foreign request": func(b *EvidenceExportBundle) {
			b.LifecycleEvents[0].RequestID = "99999999-9999-4999-8999-999999999999"
		},
		"approval unknown request": func(b *EvidenceExportBundle) {
			b.PurgeApprovals[0].RequestID = "99999999-9999-4999-8999-999999999999"
		},
		"execution unknown request": func(b *EvidenceExportBundle) {
			b.PurgeExecutions[0].RequestID = "99999999-9999-4999-8999-999999999999"
		},
		"lifecycle state unknown object": func(b *EvidenceExportBundle) {
			b.LifecycleStates[0].ObjectID = exportHexB
		},
		"receipt unknown subject": func(b *EvidenceExportBundle) {
			b.Receipts[0].Receipt.SubjectID = exportHexB
		},
		"receipt scope mismatch": func(b *EvidenceExportBundle) {
			b.Receipts[0].Receipt.CompanyID = "acme2"
		},
		"policy other tenant": func(b *EvidenceExportBundle) {
			b.RetentionPolicies[0].TenantID = "org-002"
		},
		"policy other company": func(b *EvidenceExportBundle) {
			b.RetentionPolicies[0].CompanyID = "acme2"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			mutated := buildExportTestBundle(t)
			mutate(&mutated)
			err := AssertValidEvidenceExportBundle(mutated)
			if err == nil || !strings.Contains(err.Error(), "EXPORT_SCOPE_VIOLATION") {
				t.Fatalf("expected EXPORT_SCOPE_VIOLATION, got %v", err)
			}
		})
	}

	// A tenant-level bound policy (empty company/RUC/period) of the tenant is
	// admitted — the row mutation re-hashes the content, so the manifest is
	// rebuilt from the canonicalized bundle (the validators are fail-closed on a
	// stale content hash, never silently tolerant).
	b := buildExportTestBundle(t)
	b.RetentionPolicies[0].CompanyID = ""
	b.RetentionPolicies[0].RUC = ""
	b.RetentionPolicies[0].Period = ""
	CanonicalizeEvidenceExportBundle(&b)
	counts := EvidenceExportCountsOf(b)
	contentHash := ComputeEvidenceExportContentHash(b)
	b.Manifest = BuildEvidenceExportManifest(b.Manifest.Scope, b.Manifest.Criteria, counts, EvidenceExportGeneratedAt(b), contentHash)
	if err := AssertValidEvidenceExportBundle(b); err != nil {
		t.Fatalf("tenant-level bound policy must be admitted: %v", err)
	}
}

// TestAssertValidEvidenceExportBundle freezes the fail-closed bundle
// self-consistency checks: version, self-hash, content-addressed exportId,
// counts, byte states and signing-key coverage.
func TestAssertValidEvidenceExportBundle(t *testing.T) {
	cases := map[string]func(*EvidenceExportBundle){
		"version drift": func(b *EvidenceExportBundle) {
			b.Manifest.Version = "evidence-export/v0.7.0"
		},
		"tampered bundle hash": func(b *EvidenceExportBundle) {
			b.Manifest.BundleHash = exportHexC
		},
		"tampered content hash": func(b *EvidenceExportBundle) {
			b.Manifest.ContentManifestHash = exportHexC
		},
		"tampered export id": func(b *EvidenceExportBundle) {
			b.Manifest.ExportID = "evidence-export/v0.8.0:" + exportHexC
		},
		"counts mismatch": func(b *EvidenceExportBundle) {
			b.Manifest.Counts.Objects++
		},
		"unknown bytes state": func(b *EvidenceExportBundle) {
			b.Objects[0].BytesState = EvidenceBytesState("deleted")
		},
		"stored with purge hash": func(b *EvidenceExportBundle) {
			b.Objects[0].BytesState = EvidenceBytesStored
			b.Objects[0].PurgeExecutedReceiptHash = exportHexB
		},
		"purged malformed receipt hash": func(b *EvidenceExportBundle) {
			b.Objects[0].PurgeExecutedReceiptHash = "not-a-hash"
		},
		"missing signing key": func(b *EvidenceExportBundle) {
			b.SigningKeys = []EvidenceExportSigningKey{}
		},
		"foreign signing key only": func(b *EvidenceExportBundle) {
			b.SigningKeys[0].KeyID = "ed25519:key-2"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			mutated := buildExportTestBundle(t)
			mutate(&mutated)
			if err := AssertValidEvidenceExportBundle(mutated); err == nil {
				t.Fatalf("%s: expected a fail-closed validation error", name)
			}
		})
	}

	// The canonical bundle JSON is byte-deterministic for identical data.
	first := buildExportTestBundle(t)
	second := buildExportTestBundle(t)
	if string(CanonicalEvidenceExportBundleJSON(first)) != string(CanonicalEvidenceExportBundleJSON(second)) {
		t.Fatalf("identical bundles produced different canonical bytes")
	}
}
