// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module verifies the v7→v8 additive
// migration (v0.7.0 evidence objects — docs/architecture/evidence-object-v0.7.md
// §3): fresh stores bootstrap to schema_version=8 with the immutable
// evidence_objects table (scope index + no-update/no-delete triggers) and the
// rebuilt receipts table (subject CHECK extended to 'evidence_object', action
// CHECK extended to 'object_stored', fourth typed FK evidence_object_id);
// existing v7 data (observations.policy_rule_json and every receipt row)
// survives the receipts rebuild byte-preserved; the extended CHECKs are LIVE
// (an object_stored receipt with its evidence_object FK inserts).
package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// v8Tables / v8Indexes / v8Triggers name the v0.7.0 evidence-object schema
// objects created by migrateV7ToV8.
func v8ObjectTables() []string { return []string{"evidence_objects"} }

func v8ObjectIndexes() []string {
	return []string{"idx_evidence_objects_scope"}
}

func v8ObjectTriggers() []string {
	return []string{"evidence_objects_no_update", "evidence_objects_no_delete"}
}

// openV7Schema opens a raw SQLite handle and bootstraps the EXACT v7 layout
// (applySchema + the v2→v3, v3→v4, v4→v5, v5→v6 and v6→v7 migrations), so
// tests can exercise the v7→v8 migration on a genuine v7 store.
func openV7Schema(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw v7 database: %v", err)
	}
	for _, pragma := range []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			t.Fatalf("%s: %v", pragma, err)
		}
	}
	if err := applySchema(db); err != nil {
		_ = db.Close()
		t.Fatalf("apply v2 schema: %v", err)
	}
	for _, migrate := range []func(*sql.DB) error{
		migrateV2ToV3, migrateV3ToV4, migrateV4ToV5, migrateV5ToV6, migrateV6ToV7,
	} {
		if err := migrate(db); err != nil {
			_ = db.Close()
			t.Fatalf("bootstrap v7 layout: %v", err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// insertV7ReceiptRowRaw inserts a v7-shape receipt row (no evidence_object
// subject/action exist at v7) with the exact column list of the v7 receipts
// table, so the migration's byte-preserving copy can be asserted.
func insertV7ReceiptRowRaw(t *testing.T, db *sql.DB, memoryID, keyID, receiptHash string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO signing_keys (key_id, algorithm, public_key, created_at, revoked_at)
		VALUES (?, 'Ed25519', 'base64-public-v7', ?, NULL)`,
		keyID, testT,
	); err != nil {
		t.Fatalf("insert v7 signing key: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO receipts (
			subject_type, subject_id, action, tenant_id, company_id, fiscal_period_id,
			payload_hash, previous_receipt_hash, principal_id, membership_id, policy_version,
			algorithm, key_id, signature, issued_at, payload_json, receipt_hash, memory_id, judgment_id, reconciliation_id
		) VALUES ('memory', ?, 'memory_recorded', ?, ?, ?, ?, '', 'cli', 'membership-1', 'kernel/v0.4.0',
			'Ed25519', ?, X'01020304', ?, '{"version":"receipt-payload/v0.4.0"}', ?, ?, NULL, NULL)`,
		memoryID, testOrgID, "acme", testPeriod, "payload-hash-v7", keyID, testT, receiptHash, memoryID,
	); err != nil {
		t.Fatalf("insert v7 receipt row: %v", err)
	}
}

// TestFreshStoreBootstrapsV8EvidenceObjects verifies the full additive chain: a
// fresh store boots to schema_version=8 with the evidence_objects table, its
// scope index, its immutability triggers, the receipts table carrying the
// evidence_object subject / object_stored action CHECKs and the fourth typed FK
// — while every v3/v4/v5/v6/v7 object survives untouched.
func TestFreshStoreBootstrapsV8EvidenceObjects(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 10 {
		t.Fatalf("schema_version = %d, want 10 (the chain continues v3→v4→v5→v6→v7→v8→v9→v10)", version)
	}

	// The v3 + v4 + v5 + v6 + v7 layers survive the chain (additive migrations
	// never drop) plus the new v8 evidence-object layer.
	for _, table := range append(append(append(append(append(v3Tables(), v4Tables()...), v5Tables()...), v6Tables()...), []string{"observations"}...), v8ObjectTables()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("fresh store missing table %q: %v", table, err)
		}
	}
	for _, idx := range append(append(append(v4Indexes(), v5Indexes()...), v6Indexes()...), v8ObjectIndexes()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("fresh store missing index %q: %v", idx, err)
		}
	}
	for _, trg := range append(append(v6Triggers(), []string{"observations_immutable_content"}...), v8ObjectTriggers()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trg).Scan(&name); err != nil {
			t.Fatalf("fresh store missing trigger %q: %v", trg, err)
		}
	}

	// The receipts table is the v8 layout: the fourth typed FK column exists and
	// the extended CHECKs are LIVE — an evidence_object row plus an object_stored
	// receipt with its FK inserts cleanly.
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
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-v8", core.ReceiptAlgorithm, "base64-public-v8", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}
	objRow := ReceiptRow{
		SubjectType:         core.SubjectTypeEvidenceObject,
		SubjectID:           strings.Repeat("a", 64),
		Action:              core.ReceiptActionObjectStored,
		TenantID:            testOrgID,
		CompanyID:           "acme",
		FiscalPeriodID:      testPeriod,
		PayloadHash:         "payload-hash-obj",
		PreviousReceiptHash: "",
		PrincipalID:         "agent-1",
		MembershipID:        "",
		PolicyVersion:       kernelPolicyVersion,
		Algorithm:           core.ReceiptAlgorithm,
		KeyID:               "ed25519:key-v8",
		Signature:           []byte{1, 2, 3, 4},
		IssuedAt:            testT,
		PayloadJSON:         `{"version":"receipt-payload/v0.7.0"}`,
		ReceiptHash:         "receipt-hash-obj-v8",
		EvidenceObjectID:    strings.Repeat("a", 64),
	}
	if err := s.InsertReceipt(ctxForTest(), s.db, objRow); err != nil {
		t.Fatalf("fresh store must accept an object_stored receipt with its typed FK: %v", err)
	}
}

// TestV7StoreMigratesToV8AdditivelyPreservingRows verifies that a genuine v7
// store migrates to v8 with every row byte-preserved (observation envelope
// bytes AND the receipts table — the rebuild is a byte-identical swap), the
// whole v3/v4/v5/v6/v7 surface intact and the extended receipts CHECKs live.
func TestV7StoreMigratesToV8AdditivelyPreservingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v7.db")
	db := openV7Schema(t, path)

	active := saveV3Row(t, db, validInput("tax.igv.v8", "first version"))
	insertV7ReceiptRowRaw(t, db, active.Identity.ID, "ed25519:key-v7", "receipt-hash-v7")
	_ = db.Close() // release the file before Open migrates it

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open migrates v7→v8: %v", err)
	}
	defer func() { _ = s.Close() }()

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version after migration: %v", err)
	}
	if version != 10 {
		t.Fatalf("schema_version after migration = %d, want 10", version)
	}

	// Rows survive with EXACTLY the envelope bytes written at v7.
	got, ok := s.FindByID(active.Identity.ID)
	if !ok {
		t.Fatal("active row lost by migration")
	}
	if got.EnvelopeHash != active.EnvelopeHash || got.Status != core.StatusActive {
		t.Fatalf("active row changed by migration: (%s, %s)", got.EnvelopeHash, got.Status)
	}

	// The v7 receipt row survives byte-identically through the table rebuild.
	var action, payloadJSON, receiptHash string
	if err := s.db.QueryRow(`SELECT action, payload_json, receipt_hash FROM receipts WHERE receipt_hash = 'receipt-hash-v7'`).Scan(&action, &payloadJSON, &receiptHash); err != nil {
		t.Fatalf("v7 receipt row lost by migration: %v", err)
	}
	if action != "memory_recorded" || payloadJSON != `{"version":"receipt-payload/v0.4.0"}` || receiptHash != "receipt-hash-v7" {
		t.Fatalf("v7 receipt row changed by migration: action=%q payload=%q hash=%q", action, payloadJSON, receiptHash)
	}

	// The new evidence-object layer is live after migration.
	for _, table := range v8ObjectTables() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("migrated store missing table %q: %v", table, err)
		}
	}
	var fkCol int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('receipts') WHERE name = 'evidence_object_id'`).Scan(&fkCol); err != nil {
		t.Fatalf("read receipts columns: %v", err)
	}
	if fkCol != 1 {
		t.Fatal("migrated receipts table must carry the evidence_object_id typed FK")
	}
}
