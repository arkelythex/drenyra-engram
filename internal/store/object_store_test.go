// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.7.0 LOCAL WORM
// evidence-object store (internal/store/object_store.go,
// docs/architecture/evidence-object-v0.7.md §4/§5):
//
//   - a store writes the artifact bytes temp+fsync+atomic-rename at the
//     deterministic content-addressed rel_path and commits the immutable
//     evidence_objects row + the object_stored receipt in ONE transaction
//     (created=true);
//   - a content-addressed duplicate (identical bytes already stored) is a NO-OP
//     (created=false — no new row, no receipt), and the no-op path still fails
//     closed when the stored bytes are missing;
//   - the closed-period gate blocks object writes into a CLOSED exact company
//     period (PERIOD_CLOSED) with no partial mutation;
//   - reads are SCOPE-FIRST: a caller whose exact scope differs from the stored
//     scope sees OBJECT_NOT_FOUND (cross-tenant invisibility, fail closed);
//   - every read re-hashes the stored bytes: missing or mismatched bytes fail
//     closed (OBJECT_BYTES_MISSING | OBJECT_HASH_MISMATCH) — silent repair is
//     forbidden;
//   - evidence_objects rows are immutable (no-update / no-delete triggers);
//   - ObjectAvailability resolves object-backed evidence refs and leaves
//     legacy/unresolved refs absent (backward compatible), failing closed on a
//     resolved-but-corrupt object.
package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// objectInputForTest builds a valid v0.7.0 object store input: the sample
// artifact bytes ("test" — the pinned parity digest), the exact company test
// scope and the fixture capture source.
func objectInputForTest(t *testing.T, bytes []byte) core.ObjectStoreInput {
	t.Helper()
	return core.ObjectStoreInput{
		Bytes:       bytes,
		ContentType: "application/xml",
		Scope:       testScope(testRucA),
		Source:      testAgentSource,
	}
}

// storedObjectReceiptCount returns how many receipts cover the given
// evidence-object subject (object_stored chain length).
func storedObjectReceiptCount(t *testing.T, s *SQLiteStore, objectID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM receipts WHERE subject_type = 'evidence_object' AND subject_id = ?`, objectID).Scan(&n); err != nil {
		t.Fatalf("count object receipts: %v", err)
	}
	return n
}

// TestStoreObjectCreatesWormRowAndReceipt verifies the happy path: the bytes
// land at the deterministic content-addressed path, the immutable row commits
// with the exact scope/provenance, the object_stored receipt is emitted
// atomically (v0.7.0 payload version, evidenceRef = the content address) and
// the returned metadata round-trips through the validators.
func TestStoreObjectCreatesWormRowAndReceipt(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	input := objectInputForTest(t, []byte("test"))
	result, err := s.StoreObject(context.Background(), input)
	if err != nil {
		t.Fatalf("store object: %v", err)
	}
	if !result.Created {
		t.Fatal("a genuinely new object must report created=true")
	}
	if err := core.AssertValidEvidenceObject(result.Object); err != nil {
		t.Fatalf("stored metadata must pass the model validator: %v", err)
	}
	if result.Object.ObjectID != core.ComputeObjectID(input.Bytes) {
		t.Fatalf("object id = %s, want the SHA-256 content address", result.Object.ObjectID)
	}
	// Bytes are on disk at the content-addressed path (WORM layout).
	full := filepath.Join(s.objectsRoot, result.Object.RelPath)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read WORM bytes at %s: %v", full, err)
	}
	if string(data) != "test" {
		t.Fatalf("WORM bytes = %q, want the artifact bytes", data)
	}
	// The row committed with the exact scope and provenance.
	obj, ok := s.EvidenceObjectByID(context.Background(), result.Object.ObjectID)
	if !ok {
		t.Fatal("stored object must resolve by id")
	}
	if obj.TenantID != testOrgID || obj.CompanyID != "acme" || obj.RUC != testRucA || obj.Period != testPeriod {
		t.Fatalf("stored scope = %+v, want the exact company scope", obj)
	}
	// The object_stored receipt exists with the v0.7.0 payload version.
	if n := storedObjectReceiptCount(t, s, result.Object.ObjectID); n != 1 {
		t.Fatalf("object_stored receipt count = %d, want 1", n)
	}
	var payloadJSON, version string
	if err := s.db.QueryRow(`SELECT payload_json, policy_version FROM receipts WHERE subject_type = 'evidence_object' AND subject_id = ?`, result.Object.ObjectID).Scan(&payloadJSON, &version); err != nil {
		t.Fatalf("read object receipt: %v", err)
	}
	if !strings.Contains(payloadJSON, `"version":"receipt-payload/v0.7.0"`) {
		t.Fatalf("object_stored payload must stamp the v0.7.0 version, got %s", payloadJSON)
	}
	if !strings.Contains(payloadJSON, `"evidenceRef":"`+result.Object.ObjectID+`"`) {
		t.Fatalf("object_stored payload must carry the object id as evidenceRef, got %s", payloadJSON)
	}
}

// TestStoreObjectDuplicateNoOp verifies the content-addressed duplicate path:
// identical bytes are the SAME object — the second store returns created=false
// with the existing metadata, mints NO second receipt and does not mutate
// anything.
func TestStoreObjectDuplicateNoOp(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	first, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err != nil {
		t.Fatalf("first store: %v", err)
	}
	second, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err != nil {
		t.Fatalf("duplicate store: %v", err)
	}
	if second.Created {
		t.Fatal("a content-addressed duplicate must be a NO-OP (created=false)")
	}
	if second.Object.ObjectID != first.Object.ObjectID {
		t.Fatalf("duplicate resolved to %s, want %s", second.Object.ObjectID, first.Object.ObjectID)
	}
	if n := storedObjectReceiptCount(t, s, first.Object.ObjectID); n != 1 {
		t.Fatalf("duplicate store must mint no second receipt, count = %d, want 1", n)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_objects WHERE id = ?`, first.Object.ObjectID).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("duplicate store must not insert a second row, count = %d, want 1", rows)
	}
}

// TestStoreObjectClosedPeriodGate verifies the write gate: storing into a CLOSED
// exact company period fails with PERIOD_CLOSED and leaves no row, no bytes and
// no receipt (the gate runs INSIDE the write transaction before any mutation).
func TestStoreObjectClosedPeriodGate(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saveAndApproveClose(t, s, testScope(testRucA), "close blocks object writes", "req-object-close")

	_, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err == nil {
		t.Fatal("storing into a closed period must fail")
	}
	if !strings.Contains(err.Error(), "PERIOD_CLOSED") {
		t.Fatalf("error = %v, want PERIOD_CLOSED", err)
	}
	objectID := core.ComputeObjectID([]byte("test"))
	if n := storedObjectReceiptCount(t, s, objectID); n != 0 {
		t.Fatalf("a rejected write must mint no receipt, count = %d", n)
	}
	if _, ok := s.EvidenceObjectByID(context.Background(), objectID); ok {
		t.Fatal("a rejected write must leave no evidence_objects row")
	}
	if _, err := os.Stat(filepath.Join(s.objectsRoot, core.ObjectRelPath(objectID))); !os.IsNotExist(err) {
		t.Fatal("a rejected write must leave no bytes on disk")
	}
}

// TestGetObjectScopeFirst verifies cross-tenant invisibility: the object exists
// but a caller whose exact scope differs (different RUC, different period,
// institutional) sees OBJECT_NOT_FOUND, never the object.
func TestGetObjectScopeFirst(t *testing.T) {
	s := newTestStore(t)
	result, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err != nil {
		t.Fatalf("store object: %v", err)
	}
	obj, bytes, err := s.GetObject(context.Background(), result.Object.ObjectID, testScope(testRucA))
	if err != nil {
		t.Fatalf("exact-scope read must succeed: %v", err)
	}
	if obj.ObjectID != result.Object.ObjectID || string(bytes) != "test" {
		t.Fatalf("read returned %+v / %q, want the stored object", obj, bytes)
	}
	otherRuc := testScope(testRucB)
	_, _, err = s.GetObject(context.Background(), result.Object.ObjectID, otherRuc)
	if !strings.Contains(err.Error(), "OBJECT_NOT_FOUND") {
		t.Fatalf("different-RUC read error = %v, want OBJECT_NOT_FOUND (cross-tenant invisibility)", err)
	}
	otherPeriod := testScope(testRucA)
	otherPeriod.Period = "202402"
	_, _, err = s.GetObject(context.Background(), result.Object.ObjectID, otherPeriod)
	if !strings.Contains(err.Error(), "OBJECT_NOT_FOUND") {
		t.Fatalf("different-period read error = %v, want OBJECT_NOT_FOUND (exact scope required)", err)
	}
	institutional := core.Scope{Kind: core.ScopeKindInstitutional, OrganizationID: testOrgID}
	_, _, err = s.GetObject(context.Background(), result.Object.ObjectID, institutional)
	if !strings.Contains(err.Error(), "OBJECT_NOT_FOUND") {
		t.Fatalf("institutional read error = %v, want OBJECT_NOT_FOUND", err)
	}
}

// TestGetObjectRehashFailsClosedOnCorruption verifies the WORM re-hash
// contract: every read re-hashes the stored bytes; bytes that re-hash to a
// different digest fail closed with OBJECT_HASH_MISMATCH and missing bytes fail
// closed with OBJECT_BYTES_MISSING — silent repair is forbidden.
func TestGetObjectRehashFailsClosedOnCorruption(t *testing.T) {
	s := newTestStore(t)
	result, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err != nil {
		t.Fatalf("store object: %v", err)
	}
	full := filepath.Join(s.objectsRoot, result.Object.RelPath)

	// Byte mutation: the digest changes → typed corruption error.
	if err := os.WriteFile(full, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper bytes: %v", err)
	}
	_, _, err = s.GetObject(context.Background(), result.Object.ObjectID, testScope(testRucA))
	if !strings.Contains(err.Error(), "OBJECT_HASH_MISMATCH") {
		t.Fatalf("tampered read error = %v, want OBJECT_HASH_MISMATCH (no silent repair)", err)
	}
	if err := s.VerifyObjectBytes(context.Background(), result.Object.ObjectID); !strings.Contains(err.Error(), "OBJECT_HASH_MISMATCH") {
		t.Fatalf("VerifyObjectBytes on tampered bytes = %v, want OBJECT_HASH_MISMATCH", err)
	}

	// Byte loss: the file disappears → typed corruption error.
	if err := os.Remove(full); err != nil {
		t.Fatalf("remove bytes: %v", err)
	}
	_, _, err = s.GetObject(context.Background(), result.Object.ObjectID, testScope(testRucA))
	if !strings.Contains(err.Error(), "OBJECT_BYTES_MISSING") {
		t.Fatalf("missing-bytes read error = %v, want OBJECT_BYTES_MISSING (never recreated)", err)
	}
}

// TestEvidenceObjectsRowsAreImmutable verifies the schema-level WORM guard:
// evidence_objects rows can neither update nor delete.
func TestEvidenceObjectsRowsAreImmutable(t *testing.T) {
	s := newTestStore(t)
	result, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err != nil {
		t.Fatalf("store object: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE evidence_objects SET stored_by = 'evil' WHERE id = ?`, result.Object.ObjectID); err == nil {
		t.Fatal("UPDATE on evidence_objects must be rejected by the immutability trigger")
	}
	if _, err := s.db.Exec(`DELETE FROM evidence_objects WHERE id = ?`, result.Object.ObjectID); err == nil {
		t.Fatal("DELETE on evidence_objects must be rejected by the immutability trigger")
	}
}

// TestObjectAvailabilityClassifiesRefs verifies the availability layer:
// object-backed refs resolve to their metadata with verified bytes, legacy
// refs (no row) stay absent — backward compatible — and a resolved-but-corrupt
// object fails closed with the typed corruption error naming the object.
func TestObjectAvailabilityClassifiesRefs(t *testing.T) {
	s := newTestStore(t)
	result, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err != nil {
		t.Fatalf("store object: %v", err)
	}
	objectID := result.Object.ObjectID

	// Object-backed + legacy refs in one call: the object resolves, legacy stays absent.
	resolved, err := s.ObjectAvailability(context.Background(), []string{objectID, "xml:F001", "cdr:F001"})
	if err != nil {
		t.Fatalf("availability: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved = %+v, want exactly the object-backed ref", resolved)
	}
	if _, ok := resolved[objectID]; !ok {
		t.Fatalf("object-backed ref %s must resolve", objectID)
	}
	if _, ok := resolved["xml:F001"]; ok {
		t.Fatal("legacy refs must stay absent (backward compatible, byte-unverified)")
	}

	// Deduplication and sorting are order-independent.
	resolved2, err := s.ObjectAvailability(context.Background(), []string{"cdr:F001", objectID, objectID, ""})
	if err != nil {
		t.Fatalf("availability (dedup): %v", err)
	}
	if len(resolved2) != 1 {
		t.Fatalf("deduped availability resolved %d refs, want 1", len(resolved2))
	}

	// A resolved-but-corrupt object fails closed, naming the object.
	full := filepath.Join(s.objectsRoot, result.Object.RelPath)
	if err := os.WriteFile(full, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper bytes: %v", err)
	}
	_, err = s.ObjectAvailability(context.Background(), []string{objectID})
	if !strings.Contains(err.Error(), objectID) || !strings.Contains(err.Error(), "OBJECT_HASH_MISMATCH") {
		t.Fatalf("corrupt-object availability error = %v, want the object id + OBJECT_HASH_MISMATCH", err)
	}
}

// TestStoreObjectRejectsInvalidScopeAndSource pins the fail-closed input
// validation: an institutional scope and an incomplete source are rejected
// before any mutation.
func TestStoreObjectRejectsInvalidScopeAndSource(t *testing.T) {
	s := newTestStore(t)
	institutional := core.ObjectStoreInput{
		Bytes:  []byte("test"),
		Scope:  core.Scope{Kind: core.ScopeKindInstitutional, OrganizationID: testOrgID},
		Source: testAgentSource,
	}
	if _, err := s.StoreObject(context.Background(), institutional); !strings.Contains(err.Error(), "INVALID_OBJECT_SCOPE") {
		t.Fatalf("institutional scope error = %v, want INVALID_OBJECT_SCOPE", err)
	}
	badSource := core.ObjectStoreInput{
		Bytes:  []byte("test"),
		Scope:  testScope(testRucA),
		Source: core.Source{System: " ", ActorID: "", ActorKind: ""},
	}
	if _, err := s.StoreObject(context.Background(), badSource); !strings.Contains(err.Error(), "INVALID_SOURCE") {
		t.Fatalf("invalid source error = %v, want INVALID_SOURCE", err)
	}
	if _, ok := s.EvidenceObjectByID(context.Background(), core.ComputeObjectID([]byte("test"))); ok {
		t.Fatal("rejected input must leave no row")
	}
}

// RoleForTest aliases the auth role type used by the store test fixtures.
