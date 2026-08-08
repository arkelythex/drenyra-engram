// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module freezes the READ-ONLY deterministic
// evidence-lifecycle export surface (docs/architecture/evidence-lifecycle-v0.8.md
// §12; WU-3 — core/store/API boundary only):
//
//   - the export is DETERMINISTIC: two exports of identical data produce the
//     identical canonical bundle bytes and the identical content-addressed
//     exportId (replay/idempotency is structural);
//   - the export is TENANT/RUC-SCOPED: objects of another RUC or another
//     company of the same tenant never appear, and a perioded criteria never
//     leaks a different period (no data crosses tenant/company/RUC/period);
//   - a PURGED object exports immutable metadata/hash/lifecycle/receipt evidence
//     ONLY: bytes: "purged" with the purge_executed completion receipt hash, and
//     the export succeeds even though the bytes are physically gone (the bundle
//     never carries or reads object bytes — every object entry is an audit
//     manifest entry, never a byte export);
//   - the export is READ-ONLY: it runs inside a read-only transaction that is
//     only rolled back, emits NO receipt, reserves NO idempotency key and
//     registers NO signing key — the receipts/idempotency/signing-key row
//     counts are byte-for-byte unchanged after the export;
//   - every exported receipt is offline-verifiable: the exported envelope +
//     canonical payload bytes + receipt hash + public key re-verify with
//     core.VerifyReceipt.
package store

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// exportFixture builds a two-object lifecycle store: object A (stored, with one
// legal hold placed) and object B (fully purged through the approved pipeline —
// request → approval → execute). The fixture carries the executed pipeline so
// the test can cross-check the export against the immutable ledger.
type exportFixture struct {
	objectA     core.EvidenceObject
	objectB     core.EvidenceObject
	hold        core.EvidenceHold
	request     core.EvidencePurgeRequest
	approval    core.EvidencePurgeApproval
	execution   core.EvidencePurgeExecution
	completion  string // the purge_executed completion receipt hash
	scope       core.Scope
	policyCount int
}

// buildExportFixture drives object store (A + the pipeline object) → policy put
// → hold placement on A → purge request/approval/execute for B and returns the
// frozen fixture.
func buildExportFixture(t *testing.T, s *SQLiteStore) exportFixture {
	t.Helper()
	ctx := context.Background()
	scope := testScope(testRucA)

	aResult, err := s.StoreObject(ctx, objectInputForTest(t, []byte("export-object-a-bytes-00000001")))
	if err != nil {
		t.Fatalf("store export object A: %v", err)
	}
	fx := approvedPurgePipeline(t, s)
	objectB := fx.object

	// Execute the approved pipeline: B is now purged (bytes physically removed,
	// metadata/hash/events/receipts immutable).
	execResult, err := s.ExecutePurge(ctx, core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		Reason:                "export fixture execution",
		ExecutionID:           "00000000-0000-4000-8000-000000000601",
	}, subjectPrincipal(t, "export-executor", []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard))
	if err != nil {
		t.Fatalf("execute purge: %v", err)
	}

	// A legal hold on object A (preservation act; holds are receipts-only, no
	// lifecycle event row).
	holdResult, err := s.PlaceHold(ctx, core.PlaceHoldCommand{
		ObjectID:       aResult.Object.ObjectID,
		Kind:           core.HoldKindLegal,
		Reason:         "export fixture hold",
		OwnerSubjectID: "export-hold-owner",
		RequestID:      "req-hold-export-fixture",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("place export hold: %v", err)
	}

	return exportFixture{
		objectA:     aResult.Object,
		objectB:     objectB,
		hold:        holdResult.Hold,
		request:     fx.request,
		approval:    fx.approval,
		execution:   execResult.Execution,
		completion:  execResult.Execution.CompletionReceiptID,
		scope:       scope,
		policyCount: 1,
	}
}

// tableCount reads one COUNT(*) of the export-relevant mutation tables (the
// read-only proof).
func tableCount(t *testing.T, s *SQLiteStore, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestExportEvidenceLifecycleDeterministicBundle freezes the full-bundle
// determinism contract: two exports of identical data produce the identical
// canonical bundle bytes and the identical content-addressed exportId, every
// count is exact, the per-subject receipt chains are complete and contiguous
// (each receipt chains on the previous one), a purged object exports
// metadata/hash/lifecycle/receipt evidence ONLY (bytes: "purged" + the
// completion receipt hash) while a stored object carries bytes: "stored", and
// every exported receipt is offline-verifiable with core.VerifyReceipt against
// the exported public key.
func TestExportEvidenceLifecycleDeterministicBundle(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := buildExportFixture(t, s)

	criteria := core.EvidenceExportCriteria{Scope: fx.scope}
	bundle, err := s.ExportEvidenceLifecycle(context.Background(), criteria)
	if err != nil {
		t.Fatalf("export evidence lifecycle: %v", err)
	}

	// Determinism: a second export of identical data is byte-identical (canonical
	// bundle JSON + export id + bundle hash).
	again, err := s.ExportEvidenceLifecycle(context.Background(), criteria)
	if err != nil {
		t.Fatalf("re-export evidence lifecycle: %v", err)
	}
	firstBytes := core.CanonicalEvidenceExportBundleJSON(bundle)
	secondBytes := core.CanonicalEvidenceExportBundleJSON(again)
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("deterministic export violated: two exports of identical data differ\nfirst:  %s\nsecond: %s", firstBytes, secondBytes)
	}
	if bundle.Manifest.ExportID != again.Manifest.ExportID || bundle.Manifest.BundleHash != again.Manifest.BundleHash {
		t.Fatalf("content-addressed identity differs across identical exports: %s vs %s",
			bundle.Manifest.ExportID, again.Manifest.ExportID)
	}

	// The manifest is self-hashing and version-frozen.
	if bundle.Manifest.Version != core.EvidenceExportModelVersion {
		t.Fatalf("manifest version = %q, want %q", bundle.Manifest.Version, core.EvidenceExportModelVersion)
	}
	if bundle.Manifest.ExportID != core.EvidenceExportModelVersion+":"+bundle.Manifest.ContentManifestHash {
		t.Fatalf("exportId %q is not the content-addressed identity of contentManifestHash %q", bundle.Manifest.ExportID, bundle.Manifest.ContentManifestHash)
	}

	// Exact counts: 2 objects (A stored, B purged), 1 lifecycle state (the
	// projection is created by the purge pipeline only — holds are
	// receipts-only, never a projection write), 1 bound policy, 1 hold, 1
	// request, 1 approval, 1 completed execution, 5 lifecycle events
	// (retention_bound, purge_requested, purge_approved, purge_intent,
	// purge_executed), 8 receipts (A: object_stored + hold_placed; B:
	// object_stored + retention_bound + purge_requested + purge_approved +
	// purge_intent + purge_executed), 1 public signing key.
	expected := core.EvidenceExportCounts{
		Objects: 2, LifecycleStates: 1, RetentionPolicies: 1, Holds: 1,
		PurgeRequests: 1, PurgeApprovals: 1, PurgeExecutions: 1,
		LifecycleEvents: 5, Receipts: 8, SigningKeys: 1,
	}
	if got := bundle.Manifest.Counts; got != expected {
		t.Fatalf("manifest counts = %+v, want %+v (bundle arrays: objects=%d states=%d policies=%d holds=%d requests=%d approvals=%d executions=%d events=%d receipts=%d keys=%d)",
			got, expected,
			len(bundle.Objects), len(bundle.LifecycleStates), len(bundle.RetentionPolicies), len(bundle.Holds),
			len(bundle.PurgeRequests), len(bundle.PurgeApprovals), len(bundle.PurgeExecutions),
			len(bundle.LifecycleEvents), len(bundle.Receipts), len(bundle.SigningKeys))
	}

	// Object entries: A is stored (no purge receipt hash); B is purged with the
	// execution's completion receipt hash — and the bundle never carries bytes.
	if strings.Contains(string(firstBytes), "purge-target-bytes") {
		t.Fatalf("the audit bundle carries object byte content — a manifest must never be a byte export")
	}
	byID := map[string]core.EvidenceObjectExport{}
	for _, o := range bundle.Objects {
		byID[o.Object.ObjectID] = o
	}
	aEntry, ok := byID[fx.objectA.ObjectID]
	if !ok {
		t.Fatalf("export misses stored object %s", fx.objectA.ObjectID)
	}
	if aEntry.BytesState != core.EvidenceBytesStored || aEntry.PurgeExecutedReceiptHash != "" {
		t.Fatalf("stored object entry = bytes %q / purge hash %q, want stored / empty", aEntry.BytesState, aEntry.PurgeExecutedReceiptHash)
	}
	bEntry, ok := byID[fx.objectB.ObjectID]
	if !ok {
		t.Fatalf("export misses purged object %s", fx.objectB.ObjectID)
	}
	if bEntry.BytesState != core.EvidenceBytesPurged {
		t.Fatalf("purged object entry bytes = %q, want purged", bEntry.BytesState)
	}
	if bEntry.PurgeExecutedReceiptHash != fx.completion {
		t.Fatalf("purged object completion receipt hash = %q, want %q (from the executions ledger)", bEntry.PurgeExecutedReceiptHash, fx.completion)
	}
	if fx.completion == "" {
		t.Fatalf("fixture completion receipt hash is empty — the parity signer must emit receipts")
	}

	// The completed execution's receipt hash is the design §12 audit anchor and
	// the receipt chain includes the purge_executed act.
	bReceipts := []core.EvidenceExportReceipt{}
	for _, r := range bundle.Receipts {
		if r.Receipt.SubjectID == fx.objectB.ObjectID {
			bReceipts = append(bReceipts, r)
		}
	}
	if len(bReceipts) != 6 {
		t.Fatalf("purged object receipt chain length = %d, want 6 (object_stored → retention_bound → purge_requested → purge_approved → purge_intent → purge_executed)", len(bReceipts))
	}
	wantActions := []core.ReceiptAction{
		core.ReceiptActionObjectStored, core.ReceiptActionRetentionBound, core.ReceiptActionPurgeRequested,
		core.ReceiptActionPurgeApproved, core.ReceiptActionPurgeIntent, core.ReceiptActionPurgeExecuted,
	}
	for i, want := range wantActions {
		if bReceipts[i].Receipt.Action != want {
			t.Fatalf("purged chain receipt[%d] action = %s, want %s", i, bReceipts[i].Receipt.Action, want)
		}
	}
	// Contiguous chain: each receipt chains on the previous one's hash.
	for i := 1; i < len(bReceipts); i++ {
		if bReceipts[i].Receipt.PreviousReceiptHash != bReceipts[i-1].ReceiptHash {
			t.Fatalf("purged chain broken at receipt[%d]: previous %q != prior stored hash %q",
				i, bReceipts[i].Receipt.PreviousReceiptHash, bReceipts[i-1].ReceiptHash)
		}
	}
	// The propagated chain ordinals are the complete per-subject emission
	// sequence 0..n-1 — the store's (subjectId, issuedAt, insertion) positions
	// (insertion = rowid), the exact same-second tie-break the canonical sort
	// preserves and the content hash commits to.
	for i, r := range bReceipts {
		if r.ChainOrdinal != i {
			t.Fatalf("purged chain receipt[%d] chainOrdinal = %d, want %d (the store's emission position)", i, r.ChainOrdinal, i)
		}
	}
	if bReceipts[len(bReceipts)-1].ReceiptHash != fx.completion {
		t.Fatalf("chain head %q != execution completion receipt hash %q", bReceipts[len(bReceipts)-1].ReceiptHash, fx.completion)
	}

	// Offline verification: the exported envelope + canonical payload bytes +
	// public key re-verify with core.VerifyReceipt (integrity + signer
	// possession; accounting correctness: NOT ASSERTED).
	if len(bundle.SigningKeys) != 1 {
		t.Fatalf("signing keys exported = %d, want 1", len(bundle.SigningKeys))
	}
	pubRaw, err := base64.StdEncoding.DecodeString(bundle.SigningKeys[0].PublicKey)
	if err != nil {
		t.Fatalf("decode exported public key: %v", err)
	}
	if len(pubRaw) != ed25519.PublicKeySize {
		t.Fatalf("exported public key length = %d, want %d", len(pubRaw), ed25519.PublicKeySize)
	}
	for _, r := range bundle.Receipts {
		var payload core.ReceiptPayload
		if err := json.Unmarshal([]byte(r.PayloadJSON), &payload); err != nil {
			t.Fatalf("exported payload JSON is not a valid receipt payload: %v", err)
		}
		if err := core.VerifyReceipt(r.Receipt, payload, pubRaw); err != nil {
			t.Fatalf("exported receipt %s (action %s) does not verify offline: %v", r.ReceiptHash, r.Receipt.Action, err)
		}
		if core.ReceiptHash(r.Receipt) != r.ReceiptHash {
			t.Fatalf("exported receipt hash %s does not match the derived digest", r.ReceiptHash)
		}
		if r.Receipt.PayloadHash != core.ReceiptPayloadHash(payload) {
			t.Fatalf("exported payloadHash %s does not match the canonical payload digest", r.Receipt.PayloadHash)
		}
	}

	// The exported lifecycle events are the immutable source of truth (the
	// purge_executed event is present and its resulting hash is the projection's
	// current hash — the purged terminal state).
	var executedEvent *core.EvidenceLifecycleEvent
	for i := range bundle.LifecycleEvents {
		if bundle.LifecycleEvents[i].Action == core.PurgeEventExecuted {
			executedEvent = &bundle.LifecycleEvents[i]
		}
	}
	if executedEvent == nil {
		t.Fatalf("export misses the purge_executed lifecycle event")
	}
	for _, rs := range bundle.LifecycleStates {
		if rs.ObjectID == fx.objectB.ObjectID {
			if rs.LifecycleState != core.PurgeLifecyclePurged {
				t.Fatalf("purged object projection lifecycle state = %s, want purged", rs.LifecycleState)
			}
			if rs.CurrentHash != executedEvent.ResultingHash {
				t.Fatalf("projection current hash %s != purge_executed resulting hash %s", rs.CurrentHash, executedEvent.ResultingHash)
			}
		}
	}
}

// TestExportEvidenceLifecycleReadOnly proves the export is a READ-ONLY query:
// with a receipt signer attached, the receipts, idempotency-key and signing-key
// row counts are byte-for-byte unchanged after the export, and the export never
// writes any row (it runs inside a read-only transaction that is only rolled
// back). The export intentionally emits NO receipt — identical data yields the
// identical deterministic bundle and content-addressed exportId, so there is no
// material export act to receipt.
func TestExportEvidenceLifecycleReadOnly(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := buildExportFixture(t, s)

	before := []int{
		tableCount(t, s, "receipts"),
		tableCount(t, s, "evidence_purge_idempotency_keys"),
		tableCount(t, s, "signing_keys"),
		tableCount(t, s, "evidence_lifecycle_events"),
		tableCount(t, s, "evidence_retention_state"),
		tableCount(t, s, "evidence_purge_requests"),
		tableCount(t, s, "evidence_purge_approvals"),
		tableCount(t, s, "evidence_purge_executions"),
		tableCount(t, s, "evidence_holds"),
	}

	criteria := core.EvidenceExportCriteria{Scope: fx.scope}
	for i := 0; i < 2; i++ {
		if _, err := s.ExportEvidenceLifecycle(context.Background(), criteria); err != nil {
			t.Fatalf("export evidence lifecycle (run %d): %v", i, err)
		}
	}

	after := []int{
		tableCount(t, s, "receipts"),
		tableCount(t, s, "evidence_purge_idempotency_keys"),
		tableCount(t, s, "signing_keys"),
		tableCount(t, s, "evidence_lifecycle_events"),
		tableCount(t, s, "evidence_retention_state"),
		tableCount(t, s, "evidence_purge_requests"),
		tableCount(t, s, "evidence_purge_approvals"),
		tableCount(t, s, "evidence_purge_executions"),
		tableCount(t, s, "evidence_holds"),
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("export wrote rows: table index %d changed %d → %d (the export must be read-only)", i, before[i], after[i])
		}
	}
}

// TestExportEvidenceLifecycleTenantIsolation proves no data crosses the
// tenant/company/RUC/period boundary: objects of another RUC and another
// company of the same tenant never appear in the bundle, the bound-policy
// gathering never leaks a foreign policy, and a perioded criteria never leaks a
// different period.
func TestExportEvidenceLifecycleTenantIsolation(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := buildExportFixture(t, s)

	ctx := context.Background()
	// Another RUC of the same company.
	rucBInput := objectInputForTest(t, []byte("export-object-ruc-b-bytes-00000002"))
	rucBInput.Scope = testScope(testRucB)
	if _, err := s.StoreObject(ctx, rucBInput); err != nil {
		t.Fatalf("store RUC-B object: %v", err)
	}
	// Another company of the same tenant and RUC.
	otherCompanyInput := objectInputForTest(t, []byte("export-object-company2-bytes-00000003"))
	otherCompanyInput.Scope = testScope(testRucA)
	otherCompanyInput.Scope.CompanyID = "acme2"
	if _, err := s.StoreObject(ctx, otherCompanyInput); err != nil {
		t.Fatalf("store other-company object: %v", err)
	}
	// A second period of the same company/RUC.
	otherPeriodInput := objectInputForTest(t, []byte("export-object-period2-bytes-00000004"))
	otherPeriodInput.Scope = testScope(testRucA)
	otherPeriodInput.Scope.Period = "202402"
	if _, err := s.StoreObject(ctx, otherPeriodInput); err != nil {
		t.Fatalf("store other-period object: %v", err)
	}
	// A foreign policy (RUC B) — the bound-policy gathering must never leak it.
	if _, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.Scope = core.Scope{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, CompanyID: "acme", RUC: testRucB, Period: testPeriod}
		cmd.RequestID = "req-policy-rucb-export"
	}, recordsPrincipal(t)); err != nil {
		t.Fatalf("put RUC-B policy: %v", err)
	}

	// Exact RUC-A / company acme / period 202401 export: ONLY A and B appear.
	bundle, err := s.ExportEvidenceLifecycle(ctx, core.EvidenceExportCriteria{Scope: fx.scope})
	if err != nil {
		t.Fatalf("export RUC-A scope: %v", err)
	}
	if len(bundle.Objects) != 2 {
		t.Fatalf("RUC-A export objects = %d, want 2 (cross-RUC/company/period leakage)", len(bundle.Objects))
	}
	for _, o := range bundle.Objects {
		if o.Object.RUC != testRucA || o.Object.CompanyID != "acme" || o.Object.Period != testPeriod {
			t.Fatalf("RUC-A export carries out-of-scope object %s (ruc=%s company=%s period=%s)", o.Object.ObjectID, o.Object.RUC, o.Object.CompanyID, o.Object.Period)
		}
	}
	if len(bundle.RetentionPolicies) != fx.policyCount {
		t.Fatalf("RUC-A export policies = %d, want %d (foreign policy leaked)", len(bundle.RetentionPolicies), fx.policyCount)
	}

	// The RUC-B export sees ONLY the RUC-B object.
	rucBBundle, err := s.ExportEvidenceLifecycle(ctx, core.EvidenceExportCriteria{Scope: testScope(testRucB)})
	if err != nil {
		t.Fatalf("export RUC-B scope: %v", err)
	}
	if len(rucBBundle.Objects) != 1 || rucBBundle.Objects[0].Object.RUC != testRucB {
		t.Fatalf("RUC-B export objects = %d, want exactly the RUC-B object", len(rucBBundle.Objects))
	}

	// The other-company export sees ONLY the other-company object.
	companyScope := testScope(testRucA)
	companyScope.CompanyID = "acme2"
	companyBundle, err := s.ExportEvidenceLifecycle(ctx, core.EvidenceExportCriteria{Scope: companyScope})
	if err != nil {
		t.Fatalf("export other-company scope: %v", err)
	}
	if len(companyBundle.Objects) != 1 || companyBundle.Objects[0].Object.CompanyID != "acme2" {
		t.Fatalf("other-company export objects = %d, want exactly the acme2 object", len(companyBundle.Objects))
	}

	// A perioded criteria never leaks a different period.
	period202402 := testScope(testRucA)
	period202402.Period = "202402"
	periodBundle, err := s.ExportEvidenceLifecycle(ctx, core.EvidenceExportCriteria{Scope: period202402})
	if err != nil {
		t.Fatalf("export period-202402 scope: %v", err)
	}
	if len(periodBundle.Objects) != 1 || periodBundle.Objects[0].Object.Period != "202402" {
		t.Fatalf("perioded export objects = %d, want exactly the 202402 object (period leak)", len(periodBundle.Objects))
	}

	// An EMPTY period criteria selects ALL periods of the RUC.
	allPeriods := testScope(testRucA)
	allPeriods.Period = ""
	allBundle, err := s.ExportEvidenceLifecycle(ctx, core.EvidenceExportCriteria{Scope: allPeriods})
	if err != nil {
		t.Fatalf("export all-periods scope: %v", err)
	}
	if len(allBundle.Objects) != 3 {
		t.Fatalf("all-periods export objects = %d, want 3 (A, B and the 202402 object)", len(allBundle.Objects))
	}
}

// TestExportEvidenceLifecycleEmptyScope proves an empty scope exports a valid,
// deterministic, self-hashing bundle with zero counts and no generatedAt.
func TestExportEvidenceLifecycleEmptyScope(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	criteria := core.EvidenceExportCriteria{Scope: testScope(testRucA)}

	bundle, err := s.ExportEvidenceLifecycle(ctx, criteria)
	if err != nil {
		t.Fatalf("export empty scope: %v", err)
	}
	if bundle.Manifest.Counts != (core.EvidenceExportCounts{}) {
		t.Fatalf("empty export counts = %+v, want all zero", bundle.Manifest.Counts)
	}
	if bundle.Manifest.GeneratedAt != "" {
		t.Fatalf("empty export generatedAt = %q, want empty", bundle.Manifest.GeneratedAt)
	}
	if bundle.Manifest.ExportID != core.EvidenceExportModelVersion+":"+bundle.Manifest.ContentManifestHash {
		t.Fatalf("empty export is not content-addressed: %q", bundle.Manifest.ExportID)
	}
	again, err := s.ExportEvidenceLifecycle(ctx, criteria)
	if err != nil {
		t.Fatalf("re-export empty scope: %v", err)
	}
	if string(core.CanonicalEvidenceExportBundleJSON(bundle)) != string(core.CanonicalEvidenceExportBundleJSON(again)) {
		t.Fatalf("empty export is not deterministic")
	}

	// Invalid criteria fail closed before any query.
	for name, bad := range map[string]core.EvidenceExportCriteria{
		"institutional": {Scope: core.Scope{Kind: core.ScopeKindInstitutional}},
		"bad ruc":       {Scope: core.Scope{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, CompanyID: "acme", RUC: "123"}},
		"bad period":    {Scope: core.Scope{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, CompanyID: "acme", RUC: testRucA, Period: "202413"}},
		"no company":    {Scope: core.Scope{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, RUC: testRucA}},
	} {
		if _, err := s.ExportEvidenceLifecycle(ctx, bad); err == nil || !strings.Contains(err.Error(), "INVALID_EXPORT_SCOPE") {
			t.Fatalf("%s: expected INVALID_EXPORT_SCOPE, got %v", name, err)
		}
	}
}

// TestExportCrashIntentNotStoredAndRecoveryConverges freezes the audit's Fix 1 +
// Fix 2 interaction: a crash-intent (a receipt-covered purge intent whose bytes
// are already missing and whose completion never committed) is exported as
// bytes "intended" — NEVER as ordinary stored bytes — with its full immutable
// history (request/approval/intent event/intent receipt) and NO completion
// receipt hash; then a same-execution-id retry CONVERGES the interrupted
// attempt to 'purged' (Recovered=true, exactly ONE purge_executed event and
// receipt, no second unlink), and the re-export presents the object as
// "purged" with the completion receipt hash while its history rows survive
// (purged evidence retains history without bytes).
func TestExportCrashIntentNotStoredAndRecoveryConverges(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	ctx := context.Background()
	fx := approvedPurgePipeline(t, s)

	// Simulate the crash-after-unlink window: the bytes are removed BEFORE any
	// execution completes, so ExecutePurge commits its durable intent and the
	// execution transaction fails closed on the missing bytes (the exact state a
	// crash between the authorized unlink and the completion commit leaves
	// behind: executions row 'intent', no purge_executed, bytes gone).
	if err := os.Remove(objectBytesPath(t, s, fx.object)); err != nil {
		t.Fatalf("remove object bytes (simulated crash window): %v", err)
	}
	execID := "00000000-0000-4000-8000-000000000701"
	if _, err := s.ExecutePurge(ctx, core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           execID,
	}, recordsPrincipal(t)); err == nil || !strings.Contains(err.Error(), objectErrBytesMissing) {
		t.Fatalf("execute purge on pre-missing bytes = %v, want OBJECT_BYTES_MISSING (the interrupted window)", err)
	}
	if got := executionState(t, s, execID); got != string(core.PurgeExecutionIntent) {
		t.Fatalf("execution state = %q, want intent (the interrupted window)", got)
	}

	// The crash-intent export: bytes "intended", full history, NO completion
	// receipt hash, and the bundle validates.
	criteria := core.EvidenceExportCriteria{Scope: fx.scope}
	bundle, err := s.ExportEvidenceLifecycle(ctx, criteria)
	if err != nil {
		t.Fatalf("export with a crash intent: %v", err)
	}
	if len(bundle.Objects) != 1 {
		t.Fatalf("export objects = %d, want 1", len(bundle.Objects))
	}
	entry := bundle.Objects[0]
	if entry.BytesState != core.EvidenceBytesIntended {
		t.Fatalf("crash-intent byte state = %q, want %q (never ordinary stored bytes)", entry.BytesState, core.EvidenceBytesIntended)
	}
	if entry.PurgeExecutedReceiptHash != "" {
		t.Fatalf("a crash-intent object must carry NO completion receipt hash, got %q", entry.PurgeExecutedReceiptHash)
	}
	if len(bundle.PurgeExecutions) != 1 || bundle.PurgeExecutions[0].State != core.PurgeExecutionIntent {
		t.Fatalf("the export must carry the intent execution evidence: %+v", bundle.PurgeExecutions)
	}
	hasIntentReceipt := false
	for _, r := range bundle.Receipts {
		if r.Receipt.Action == core.ReceiptActionPurgeIntent && r.Receipt.SubjectID == fx.objectID {
			hasIntentReceipt = true
		}
		if r.Receipt.Action == core.ReceiptActionPurgeExecuted && r.Receipt.SubjectID == fx.objectID {
			t.Fatal("a crash-intent export must NEVER claim a purge_executed receipt")
		}
	}
	if !hasIntentReceipt {
		t.Fatal("a crash-intent export must carry the purge_intent receipt (history without bytes)")
	}

	// Same-execution-id retry CONVERGES the interrupted attempt under its bound
	// immutable authorization: exactly ONE purge_executed event + receipt, no
	// second unlink (the bytes were already gone), Recovered=true.
	recovered, err := s.ExecutePurge(ctx, core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           execID,
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("same-id recovery must converge, got: %v", err)
	}
	if !recovered.Recovered || recovered.IdempotentReplay {
		t.Fatalf("recovery result = %+v, want Recovered=true and NOT an idempotent replay", recovered)
	}
	if recovered.Execution.State != core.PurgeExecutionCompleted || recovered.Execution.CompletionReceiptID == "" {
		t.Fatalf("converged execution = %+v, want completed with the completion receipt", recovered.Execution)
	}
	var executedEvents, executedReceipts, executionRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_lifecycle_events WHERE object_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedEvents); err != nil {
		t.Fatalf("count executed events: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM receipts WHERE subject_id = ? AND action = 'purge_executed'`, fx.objectID).Scan(&executedReceipts); err != nil {
		t.Fatalf("count executed receipts: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_purge_executions WHERE request_id = ?`, fx.request.RequestID).Scan(&executionRows); err != nil {
		t.Fatalf("count executions: %v", err)
	}
	if executedEvents != 1 || executedReceipts != 1 {
		t.Fatalf("convergence completion = (%d events, %d receipts), want exactly ONE of each", executedEvents, executedReceipts)
	}
	if executionRows != 1 {
		t.Fatalf("executions rows = %d, want exactly 1 (a recovery never creates a duplicate attempt)", executionRows)
	}

	// The re-export presents the object as purged WITH the completion receipt
	// hash and the same immutable history (purged evidence retains history
	// without bytes).
	again, err := s.ExportEvidenceLifecycle(ctx, criteria)
	if err != nil {
		t.Fatalf("re-export after convergence: %v", err)
	}
	if again.Objects[0].BytesState != core.EvidenceBytesPurged {
		t.Fatalf("post-convergence byte state = %q, want purged", again.Objects[0].BytesState)
	}
	if again.Objects[0].PurgeExecutedReceiptHash != recovered.Execution.CompletionReceiptID {
		t.Fatalf("purged completion receipt hash = %q, want %q", again.Objects[0].PurgeExecutedReceiptHash, recovered.Execution.CompletionReceiptID)
	}
	if len(again.PurgeRequests) != 1 || len(again.PurgeApprovals) != 1 || len(again.LifecycleEvents) < 2 {
		t.Fatalf("purged evidence must retain its history without bytes: requests=%d approvals=%d events=%d",
			len(again.PurgeRequests), len(again.PurgeApprovals), len(again.LifecycleEvents))
	}
}
