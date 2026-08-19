// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module verifies the v9→v10 additive migration (v0.8 batch 3
// object-level legal holds — docs/architecture/evidence-lifecycle-v0.8.md §3.2/
// §4/§7): fresh stores bootstrap to schema_version=11 with the immutable
// evidence_holds table (OBJECT-LEVEL ONLY — object_id NOT NULL FK, exact scope
// columns, object index, no-delete trigger, placed-columns immutability trigger
// and the one-way lift closure trigger) and the tenant-scoped
// evidence_hold_idempotency_keys ledger; existing v9 data (every receipt row)
// survives the receipts_v10 rebuild byte-preserved; the extended action CHECK is
// LIVE (a hold_placed receipt with its typed evidence_object FK inserts and a
// legacy v0.4–v0.8 action still inserts); a pre-existing v10 table aborts the
// migration (fail closed — additive migrations never replay).
package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// v10Tables / v10Triggers name the v0.8 hold schema objects created by
// migrateV9ToV10.
func v10Tables() []string {
	return []string{"evidence_holds", "evidence_hold_idempotency_keys"}
}

func v10Indexes() []string { return []string{"idx_evidence_holds_object"} }

func v10Triggers() []string {
	return []string{
		"evidence_holds_immutable_placed", "evidence_holds_one_way_lift", "evidence_holds_no_delete",
	}
}

// openV9Schema opens a raw SQLite handle and bootstraps the EXACT v9 layout
// (applySchema + the v2→v3 … v8→v9 migrations), so tests can exercise the
// v9→v10 migration on a genuine v9 store.
func openV9Schema(t *testing.T, path string) *sql.DB {
	t.Helper()
	db := openV8Schema(t, path)
	if err := migrateV8ToV9(db); err != nil {
		_ = db.Close()
		t.Fatalf("bootstrap v9 layout: %v", err)
	}
	return db
}

func TestFreshStoreBootstrapsV10Holds(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 17 {
		t.Fatalf("schema_version = %d, want 17 (the chain continues v2→v3→…→v10→v11→v12→v13→v14)", version)
	}

	// The whole v3…v9 surface survives the chain (additive migrations never
	// drop) plus the new v10 hold layer.
	for _, table := range append(append(append(append(append(append(v3Tables(), v4Tables()...), v5Tables()...), v6Tables()...), []string{"observations", "evidence_objects"}...), v9Tables()...), v10Tables()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("fresh store missing table %q: %v", table, err)
		}
	}
	for _, idx := range append(append(append(append(append(v4Indexes(), v5Indexes()...), v6Indexes()...), v8ObjectIndexes()...), v9Indexes()...), v10Indexes()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("fresh store missing index %q: %v", idx, err)
		}
	}
	for _, trg := range append(append(append(append(v6Triggers(), []string{"observations_immutable_content", "evidence_objects_no_update", "evidence_objects_no_delete"}...), v8ObjectTriggers()...), v9Triggers()...), v10Triggers()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trg).Scan(&name); err != nil {
			t.Fatalf("fresh store missing trigger %q: %v", trg, err)
		}
	}

	// The receipts table is the v10 layout: the extended action CHECK is LIVE —
	// a hold_placed receipt with its typed evidence_object FK inserts, and a
	// legacy v0.4–v0.8 action still inserts (layout verbatim).
	if _, err := s.db.Exec(`
		INSERT INTO evidence_objects (
			id, sha256, size, content_type, tenant_id, company_id, ruc, period,
			source_system, source_reference, source_actor_id, source_actor_kind,
			stored_by, stored_at, rel_path
		) VALUES ('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 4,
			'application/xml', ?, 'acme', ?, '202401', 'go-test', '', 'agent-1', 'agent', 'agent-1', ?, 'objects/aa/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("fresh store must accept an evidence_objects row: %v", err)
	}
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-v10", core.ReceiptAlgorithm, "base64-public-v10", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}
	holdPlacedRow := ReceiptRow{
		SubjectType:      core.SubjectTypeEvidenceObject,
		SubjectID:        strings.Repeat("a", 64),
		Action:           core.ReceiptActionHoldPlaced,
		TenantID:         testOrgID,
		CompanyID:        "acme",
		FiscalPeriodID:   testPeriod,
		PayloadHash:      "payload-hash-hold-placed",
		PrincipalID:      "subject-1",
		MembershipID:     "membership-1",
		PolicyVersion:    "evidence-lifecycle-policy/v0.8.0",
		Algorithm:        core.ReceiptAlgorithm,
		KeyID:            "ed25519:key-v10",
		Signature:        []byte{10, 10, 10, 10},
		IssuedAt:         testT,
		PayloadJSON:      `{"version":"receipt-payload/v0.8.0"}`,
		ReceiptHash:      "receipt-hash-hold-placed-v10",
		EvidenceObjectID: strings.Repeat("a", 64),
	}
	if err := s.InsertReceipt(ctxForTest(), s.db, holdPlacedRow); err != nil {
		t.Fatalf("fresh store must accept a hold_placed receipt with its typed FK: %v", err)
	}
	// hold_lifted is expressible too.
	liftedRow := holdPlacedRow
	liftedRow.Action = core.ReceiptActionHoldLifted
	liftedRow.ReceiptHash = "receipt-hash-hold-lifted-v10"
	if err := s.InsertReceipt(ctxForTest(), s.db, liftedRow); err != nil {
		t.Fatalf("fresh store must accept hold_lifted: %v", err)
	}
	// A legacy v0.7 action still inserts (the extended CHECK is additive).
	legacyRow := holdPlacedRow
	legacyRow.Action = core.ReceiptActionObjectStored
	legacyRow.ReceiptHash = "receipt-hash-legacy-v10"
	if err := s.InsertReceipt(ctxForTest(), s.db, legacyRow); err != nil {
		t.Fatalf("fresh store must still accept object_stored: %v", err)
	}
	// An unknown action is still rejected by the closed CHECK.
	rogueRow := holdPlacedRow
	rogueRow.Action = core.ReceiptAction("hold_reopened")
	if err := s.InsertReceipt(ctxForTest(), s.db, rogueRow); err == nil {
		t.Fatal("fresh store must reject an unknown receipt action (closed CHECK)")
	}
}

func TestV9StoreMigratesToV10AdditivelyPreservingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-v9.db")
	db := openV9Schema(t, path)

	// Seed ONE v9-shape receipt row (a v0.8 purge act) so the receipts_v10
	// rebuild has a byte-preserved copy to prove (the v8→v9 migration already
	// proved the copy pattern; here the v9-only purge action must copy VERBATIM).
	if _, err := db.Exec(`
		INSERT INTO evidence_objects (
			id, sha256, size, content_type, tenant_id, company_id, ruc, period,
			source_system, source_reference, source_actor_id, source_actor_kind,
			stored_by, stored_at, rel_path
		) VALUES ('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 4,
			'application/xml', ?, 'acme', ?, '202401', 'go-test', '', 'agent-1', 'agent', 'agent-1', ?, 'objects/bb/bb/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')`,
		testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("seed v9 evidence_objects row: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO signing_keys (key_id, algorithm, public_key, created_at, revoked_at)
		VALUES ('ed25519:key-v9-seed', 'Ed25519', 'base64-public-v9-seed', ?, NULL)`, testT,
	); err != nil {
		t.Fatalf("seed v9 signing key: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO receipts (subject_type, subject_id, action, tenant_id, company_id,
			fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
			policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
			evidence_object_id)
		VALUES ('evidence_object', ?, 'purge_requested', ?, 'acme', '202401',
			'payload-hash-v9-seed', '', 'subject-1', 'membership-1', 'evidence-lifecycle-policy/v0.8.0',
			'Ed25519', 'ed25519:key-v9-seed', X'09090909', ?, '{"version":"receipt-payload/v0.8.0"}',
			'receipt-hash-seed-v9', ?)`,
		strings.Repeat("b", 64), testOrgID, testT, strings.Repeat("b", 64),
	); err != nil {
		t.Fatalf("seed v9 receipt row: %v", err)
	}

	// The migration must not run while the v10 tables already exist (fail closed
	// on a corruption signal — additive migrations never replay). Pre-create
	// evidence_holds on a COPY, not the migrated store.
	preExisting := filepath.Join(t.TempDir(), "engram-v9-corrupt.db")
	db2 := openV9Schema(t, preExisting)
	if _, err := db2.Exec(`CREATE TABLE evidence_holds (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("pre-create evidence_holds: %v", err)
	}
	if err := migrateV9ToV10(db2); err == nil || !strings.Contains(err.Error(), "pre-existing table") {
		t.Fatalf("migrateV9ToV10 on a store with evidence_holds = %v, want a fail-closed pre-existing-table abort", err)
	}

	// The genuine v9 store migrates in one transaction.
	if err := migrateV9ToV10(db); err != nil {
		t.Fatalf("migrate v9→v10: %v", err)
	}
	var version string
	if err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "10" {
		t.Fatalf("schema_version = %q, want 10", version)
	}

	// Every v9 receipt row survived the receipts_v10 rebuild byte-preserved
	// (including the v9-only purge action and the evidence_object typed FK).
	var storedHash, storedAction, storedPayload string
	if err := db.QueryRow(`SELECT receipt_hash, action, payload_json FROM receipts WHERE receipt_hash = 'receipt-hash-seed-v9'`).
		Scan(&storedHash, &storedAction, &storedPayload); err != nil {
		t.Fatalf("seeded receipt lost in the rebuild: %v", err)
	}
	if storedHash != "receipt-hash-seed-v9" || storedAction != "purge_requested" || storedPayload != `{"version":"receipt-payload/v0.8.0"}` {
		t.Fatalf("rebuild altered the seeded receipt: (%q, %q, %q)", storedHash, storedAction, storedPayload)
	}
	var fk any
	if err := db.QueryRow(`SELECT evidence_object_id FROM receipts WHERE receipt_hash = 'receipt-hash-seed-v9'`).Scan(&fk); err != nil {
		t.Fatalf("read evidence_object_id after rebuild: %v", err)
	}
	if id, ok := fk.(string); !ok || id != strings.Repeat("b", 64) {
		t.Fatalf("evidence_object_id = %v, want the seeded object id verbatim", fk)
	}

	// The v10 hold tables + guards are live after the migration.
	for _, table := range v10Tables() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("migrated store missing table %q: %v", table, err)
		}
	}
	for _, trg := range v10Triggers() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trg).Scan(&name); err != nil {
			t.Fatalf("migrated store missing trigger %q: %v", trg, err)
		}
	}

	// The migrated evidence_holds CHECK/FK are LIVE: a hold row inserts for the
	// seeded object, and a scope-level hold (object_id NULL — the deferred
	// shape) is rejected by the NOT NULL constraint (object-level ONLY batch).
	if _, err := db.Exec(`
		INSERT INTO evidence_holds (id, object_id, tenant_id, company_id, ruc, period,
			kind, reason, owner_subject_id, placed_at, placed_by)
		VALUES ('00000000-0000-4000-8000-000000000010', ?, ?, 'acme', ?, '202401',
			'legal', 'dispute under review', 'subject-1', ?, 'subject-1')`,
		strings.Repeat("b", 64), testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("migrated store must accept an object-level hold row: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO evidence_holds (id, object_id, tenant_id, company_id, ruc, period,
			kind, reason, owner_subject_id, placed_at, placed_by)
		VALUES ('00000000-0000-4000-8000-000000000011', NULL, ?, 'acme', ?, '202401',
			'legal', 'scope-level', 'subject-1', ?, 'subject-1')`,
		testOrgID, testRucA, testT,
	); err == nil {
		t.Fatal("migrated store must reject a scope-level hold (object_id NOT NULL — object-level ONLY batch)")
	}
}

// TestEvidenceHoldsImmutableTriggers freezes the schema-level hold guards: the
// placed columns never change (IMMUTABLE_HOLD), deletion is forbidden, the lift
// fields are ONE-WAY (a lift must set all three together; once lifted, nothing
// may be cleared or rewritten).
func TestEvidenceHoldsImmutableTriggers(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`
		INSERT INTO evidence_objects (
			id, sha256, size, content_type, tenant_id, company_id, ruc, period,
			source_system, source_reference, source_actor_id, source_actor_kind,
			stored_by, stored_at, rel_path
		) VALUES ('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 4,
			'application/xml', ?, 'acme', ?, '202401', 'go-test', '', 'agent-1', 'agent', 'agent-1', ?, 'objects/aa/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("insert evidence_objects row: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO evidence_holds (id, object_id, tenant_id, company_id, ruc, period,
			kind, reason, owner_subject_id, placed_at, placed_by)
		VALUES ('00000000-0000-4000-8000-000000000001', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', ?, 'acme', ?, '202401',
			'legal', 'dispute under review', 'subject-1', ?, 'subject-1')`,
		testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("insert hold: %v", err)
	}

	// Placed columns never change.
	if _, err := s.db.Exec(`UPDATE evidence_holds SET reason = 'rewritten' WHERE id = '00000000-0000-4000-8000-000000000001'`); err == nil {
		t.Fatal("UPDATE on a placed column must fail closed (IMMUTABLE_HOLD)")
	}
	// Deletion is forbidden.
	if _, err := s.db.Exec(`DELETE FROM evidence_holds WHERE id = '00000000-0000-4000-8000-000000000001'`); err == nil {
		t.Fatal("DELETE on evidence_holds must fail closed (IMMUTABLE_HOLD)")
	}
	// A PARTIAL lift (only one field) is rejected — the lift fields move
	// together.
	if _, err := s.db.Exec(`UPDATE evidence_holds SET lifted_at = ? WHERE id = '00000000-0000-4000-8000-000000000001'`, testT); err == nil {
		t.Fatal("a partial lift must fail closed (one-way: all three set together)")
	}
	// A FULL lift succeeds.
	if _, err := s.db.Exec(`UPDATE evidence_holds SET lifted_at = ?, lifted_by = 'subject-2', lift_reason = 'resolved' WHERE id = '00000000-0000-4000-8000-000000000001'`, testT); err != nil {
		t.Fatalf("full lift must succeed: %v", err)
	}
	// A lifted hold never reopens: clearing or rewriting any lift field fails.
	if _, err := s.db.Exec(`UPDATE evidence_holds SET lifted_at = NULL WHERE id = '00000000-0000-4000-8000-000000000001'`); err == nil {
		t.Fatal("clearing lifted_at must fail closed (a lifted hold never reopens)")
	}
	if _, err := s.db.Exec(`UPDATE evidence_holds SET lift_reason = 'changed' WHERE id = '00000000-0000-4000-8000-000000000001'`); err == nil {
		t.Fatal("rewriting a lift field must fail closed (a lifted hold never reopens)")
	}
}
