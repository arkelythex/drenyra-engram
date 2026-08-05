// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module verifies the v5→v6 additive
// migration (v0.5.0 close foundation — docs/architecture/close-intelligence-v0.5.md
// §2.3 and §7): fresh stores bootstrap to schema_version=6 with the
// close_snapshot_json column, the rebuilt receipts table (extended action CHECK
// accepting memory_closed/memory_reopened), the period_closures projection and
// the immutable period_closure_events ledger; existing v5 data survives
// byte-preserved; a failing migration rolls back leaving schema_version=5; the
// closure-event rows never update or delete.
package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// openV5Schema opens a raw SQLite handle and bootstraps the EXACT v5 layout
// (applySchema + the v2→v3, v3→v4 and v4→v5 migrations), so tests can exercise
// the v5→v6 migration on a genuine v5 store.
func openV5Schema(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw v5 database: %v", err)
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
	if err := migrateV2ToV3(db); err != nil {
		_ = db.Close()
		t.Fatalf("bootstrap v3 layout: %v", err)
	}
	if err := migrateV3ToV4(db); err != nil {
		_ = db.Close()
		t.Fatalf("bootstrap v4 layout: %v", err)
	}
	if err := migrateV4ToV5(db); err != nil {
		_ = db.Close()
		t.Fatalf("bootstrap v5 layout: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func v6Tables() []string {
	return []string{
		"period_closures", "period_closure_events",
		"reconciliations", "reconciliation_events",
		"reconciliation_idempotency_keys", "reconciliation_relations",
	}
}

func v6Indexes() []string {
	return []string{
		"idx_period_closure_events_scope",
		"uq_reconciliation_open_tuple", "idx_reconciliations_pair",
		"idx_reconciliations_predecessor", "idx_reconciliations_successor",
	}
}

func v6Triggers() []string {
	return []string{
		"period_closure_events_no_update", "period_closure_events_no_delete",
		"reconciliation_events_no_update", "reconciliation_events_no_delete",
		"reconciliations_no_delete", "reconciliations_immutable_update",
	}
}

// TestFreshStoreBootstrapsV6ClosureTables verifies the full additive chain: a
// fresh store boots to schema_version=6 with the closure tables, the events
// index/triggers, the extended receipts action CHECK and the close_snapshot_json
// column, while every v3/v4/v5 object survives untouched.
func TestFreshStoreBootstrapsV6ClosureTables(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 6 {
		t.Fatalf("schema_version = %d, want 6", version)
	}

	// The v3 + v4 + v5 layers survive the chain (additive migrations never drop).
	for _, table := range append(append(append(v3Tables(), v4Tables()...), v5Tables()...), v6Tables()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("fresh store missing table %q: %v", table, err)
		}
	}
	for _, idx := range append(append(v4Indexes(), v5Indexes()...), v6Indexes()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("fresh store missing index %q: %v", idx, err)
		}
	}
	for _, trigger := range append(append(append(v3Triggers(), v4Triggers()...), v5Triggers()...), v6Triggers()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&name); err != nil {
			t.Fatalf("fresh store missing trigger %q: %v", trigger, err)
		}
	}

	// observations.close_snapshot_json exists and defaults to NULL.
	var hasCol int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('observations') WHERE name = 'close_snapshot_json'`).Scan(&hasCol); err != nil {
		t.Fatalf("read observations columns: %v", err)
	}
	if hasCol != 1 {
		t.Fatalf("observations.close_snapshot_json column missing (found %d)", hasCol)
	}

	// The receipts action CHECK accepts the two v0.5.0 close actions.
	var indexSQL string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'receipts'`).Scan(&indexSQL); err != nil {
		t.Fatalf("read receipts definition: %v", err)
	}
	if !strings.Contains(indexSQL, "'memory_closed'") || !strings.Contains(indexSQL, "'memory_reopened'") {
		t.Fatalf("receipts action CHECK does not accept the v0.5.0 close actions: %s", indexSQL)
	}
}

// insertV5ReceiptRowRaw seeds a byte-shaped v5 receipt row (the v5 action CHECK
// only knows the eight original actions) against a real observation row, the way
// a genuine v5 store would have persisted it.
func insertV5ReceiptRowRaw(t *testing.T, db *sql.DB, memoryID, keyID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO signing_keys (key_id, algorithm, public_key, created_at, revoked_at)
		VALUES (?, 'Ed25519', 'base64-public-v5', ?, NULL)`,
		keyID, testT,
	); err != nil {
		t.Fatalf("insert v5 signing key: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO receipts (
			subject_type, subject_id, action, tenant_id, company_id, fiscal_period_id,
			payload_hash, previous_receipt_hash, principal_id, membership_id, policy_version,
			algorithm, key_id, signature, issued_at, payload_json, receipt_hash, memory_id, judgment_id
		) VALUES ('memory', ?, 'memory_recorded', ?, ?, ?, ?, '', 'cli', 'membership-1', 'kernel/v0.4.0',
			'Ed25519', ?, X'01020304', ?, '{"version":"receipt-payload/v0.4.0"}', ?, ?, NULL)`,
		memoryID, testOrgID, "acme", testPeriod, "payload-hash-v5", keyID, testT, "receipt-hash-v5", memoryID,
	); err != nil {
		t.Fatalf("insert v5 receipt row: %v", err)
	}
}

// TestV5StoreMigratesToV6AdditivelyPreservingRows verifies that a genuine v5
// store migrates to v6 with every row byte-preserved (observation envelope
// bytes AND the receipts table — the rebuild is a byte-identical swap), the
// whole v3/v4/v5 surface intact, and the extended receipts CHECK live.
func TestV5StoreMigratesToV6AdditivelyPreservingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5.db")
	db := openV5Schema(t, path)

	active := saveV3Row(t, db, validInput("tax.igv.v6", "first version"))
	insertV5ReceiptRowRaw(t, db, active.Identity.ID, "ed25519:key-v5")
	_ = db.Close() // release the file before Open migrates it

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open migrates v5→v6: %v", err)
	}
	defer func() { _ = s.Close() }()

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version after migration: %v", err)
	}
	if version != 6 {
		t.Fatalf("schema_version after migration = %d, want 6", version)
	}

	// Rows survive with EXACTLY the envelope bytes written at v5.
	got, ok := s.FindByID(active.Identity.ID)
	if !ok {
		t.Fatal("active row lost by migration")
	}
	if got.EnvelopeHash != active.EnvelopeHash || got.Status != core.StatusActive {
		t.Fatalf("active row changed by migration: (%s, %s)", got.EnvelopeHash, got.Status)
	}

	// The v5 receipt row survives byte-identically through the table rebuild.
	var action, payloadJSON, receiptHash string
	if err := s.db.QueryRow(`SELECT action, payload_json, receipt_hash FROM receipts WHERE receipt_hash = 'receipt-hash-v5'`).Scan(&action, &payloadJSON, &receiptHash); err != nil {
		t.Fatalf("v5 receipt row lost by migration: %v", err)
	}
	if action != "memory_recorded" || payloadJSON != `{"version":"receipt-payload/v0.4.0"}` || receiptHash != "receipt-hash-v5" {
		t.Fatalf("v5 receipt row changed by migration: action=%q payload=%q hash=%q", action, payloadJSON, receiptHash)
	}

	// The receipts indexes/triggers survive the rebuild and the extended CHECK is
	// LIVE: a memory_closed receipt inserts (the v0.5.0 close action).
	for _, idx := range v5Indexes() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("migrated store missing receipts index %q: %v", idx, err)
		}
	}
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-v6", core.ReceiptAlgorithm, "base64-public", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}
	closeRow := ReceiptRow{
		SubjectType:         core.SubjectTypeMemory,
		SubjectID:           active.Identity.ID,
		Action:              core.ReceiptActionMemoryClosed,
		TenantID:            testOrgID,
		CompanyID:           "acme",
		FiscalPeriodID:      testPeriod,
		PayloadHash:         "payload-hash-closed",
		PreviousReceiptHash: "",
		PrincipalID:         "subject-1",
		MembershipID:        "membership-1",
		PolicyVersion:       "approval-policy/v0.4.0",
		Algorithm:           core.ReceiptAlgorithm,
		KeyID:               "ed25519:key-v6",
		Signature:           make([]byte, 64),
		IssuedAt:            testT,
		PayloadJSON:         `{"version":"receipt-payload/v0.5.0"}`,
		ReceiptHash:         "receipt-hash-closed",
		MemoryID:            active.Identity.ID,
	}
	if err := s.InsertReceipt(ctxForTest(), s.db, closeRow); err != nil {
		t.Fatalf("memory_closed receipt must insert after migration (extended CHECK): %v", err)
	}

	// The whole v3/v4 surface survives the chain.
	for _, trigger := range append(v3Triggers(), v4Triggers()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&name); err != nil {
			t.Fatalf("migrated store missing trigger %q: %v", trigger, err)
		}
	}
}

// TestFreshStoreBootstrapsV6ReconciliationTables verifies the first-class
// reconciliation layer of the additive chain: fresh stores carry the four
// reconciliation tables (entity + events + idempotency + relations), the
// open-tuple partial unique index and the immutability triggers, the entity
// CHECKs (distinct endpoints, int64 cents, engine-derived variance, non-negative
// tolerance, status/adjudicator/resolution), and the receipts subject/action
// CHECKs extended to reconciliation.
func TestFreshStoreBootstrapsV6ReconciliationTables(t *testing.T) {
	s := newTestStore(t)

	// The reconciliations entity CHECKs are live.
	var ddl string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'reconciliations'`).Scan(&ddl); err != nil {
		t.Fatalf("read reconciliations definition: %v", err)
	}
	for _, want := range []string{
		"left_memory_id <> right_memory_id",
		"typeof(left_amount_cents) = 'integer'",
		"variance_cents = left_amount_cents - right_amount_cents",
		"tolerance_cents >= 0",
		"('proposed','confirmed','rejected','withdrawn','superseded')",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("reconciliations CHECK missing %q in:\n%s", want, ddl)
		}
	}

	// The open-tuple partial unique index exists and covers method.
	var idxSQL string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'uq_reconciliation_open_tuple'`).Scan(&idxSQL); err != nil {
		t.Fatalf("read uq_reconciliation_open_tuple: %v", err)
	}
	if !strings.Contains(idxSQL, "left_memory_id") || !strings.Contains(idxSQL, "right_memory_id") ||
		!strings.Contains(idxSQL, "method") || !strings.Contains(idxSQL, "status='proposed'") {
		t.Errorf("open-tuple index shape mismatch: %s", idxSQL)
	}

	// The receipts CHECKs accept the reconciliation subject and the two
	// reconciliation actions.
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'receipts'`).Scan(&ddl); err != nil {
		t.Fatalf("read receipts definition: %v", err)
	}
	for _, want := range []string{
		"'reconciliation'", "'reconciliation_confirmed'", "'reconciliation_rejected'",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("receipts CHECK missing %q", want)
		}
	}
}

// TestReconciliationEventsImmutable verifies the no-update / no-delete
// triggers: a corrupt or buggy caller cannot mutate reconciliation event
// history.
func TestReconciliationEventsImmutable(t *testing.T) {
	s := newTestStore(t)
	left, right := reconcileContext(t, s)
	proposed := proposeReconciliation(t, s, baseReconcileCmd(left, right), reconciliationProposer)

	if _, err := s.db.Exec(`
		INSERT INTO reconciliation_events (id, reconciliation_id, request_id, action, from_status, to_status, reconciliation_hash, principal_snapshot_json, policy_version, reason, created_at)
		VALUES ('rev-1', ?, 'req-1', 'withdraw', 'proposed', 'withdrawn', 'hash', NULL, NULL, '', ?)`, proposed.ReconciliationID, testT,
	); err != nil {
		t.Fatalf("insert reconciliation event: %v", err)
	}

	if _, err := s.db.Exec(`UPDATE reconciliation_events SET reason = 'mutated' WHERE id = 'rev-1'`); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_RECONCILIATION_EVENT") {
		t.Fatalf("UPDATE on reconciliation_events must abort with IMMUTABLE_RECONCILIATION_EVENT, got %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM reconciliation_events WHERE id = 'rev-1'`); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_RECONCILIATION_EVENT") {
		t.Fatalf("DELETE on reconciliation_events must abort with IMMUTABLE_RECONCILIATION_EVENT, got %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM reconciliations WHERE id = ?`, proposed.ReconciliationID); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_RECONCILIATION") {
		t.Fatalf("DELETE on reconciliations must abort with IMMUTABLE_RECONCILIATION, got %v", err)
	}
}

// TestV5ToV6MigrationRollsBackLeavingSchemaV5 verifies the single-transaction
// rollback: a conflicting pre-existing table fails the migration and the store
// stays v5 with NO v6 object surviving (and the v5 receipts table untouched).
func TestV5ToV6MigrationRollsBackLeavingSchemaV5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5-fail.db")
	db := openV5Schema(t, path)
	// A conflicting pre-existing table makes the verbatim CREATE TABLE
	// period_closures fail — the corrupt-step probe for the rollback.
	if _, err := db.Exec(`CREATE TABLE period_closures (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("pre-create conflicting table: %v", err)
	}
	_ = db.Close()

	_, err := Open(path)
	if err == nil {
		t.Fatal("Open must fail closed when the migration fails")
	}
	if !strings.Contains(err.Error(), "migrate v5→v6") {
		t.Fatalf("error %q does not identify the migration step", err)
	}

	// Re-open raw: schema_version must still be 5 and NO v6 object may survive.
	db = openRawReadHandle(t, path)
	version, err := readSchemaVersion(db)
	if err != nil {
		t.Fatalf("read schema version after failed migration: %v", err)
	}
	if version != 5 {
		t.Fatalf("schema_version = %d after failed migration, want 5 (rolled back)", version)
	}
	// period_closures is the PRE-CREATED corruption probe — it must survive the
	// rollback untouched; every OTHER v6 object (the events table, the index and
	// the two triggers) must be gone.
	for _, object := range append([]string{"period_closure_events"}, append(v6Indexes(), v6Triggers()...)...) {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE name = ?`, object).Scan(&name); err == nil {
			t.Fatalf("object %q survived the migration rollback", object)
		}
	}
	var probeCols int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('period_closures')`).Scan(&probeCols); err != nil {
		t.Fatalf("read probe period_closures table: %v", err)
	}
	if probeCols != 1 {
		t.Fatalf("period_closures must still be the probe table (only id), got %d columns", probeCols)
	}
	// The v5 receipts table survived the rollback with its original 8-action
	// CHECK (no memory_closed).
	var tableSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'receipts'`).Scan(&tableSQL); err != nil {
		t.Fatalf("read receipts definition: %v", err)
	}
	if strings.Contains(tableSQL, "'memory_closed'") {
		t.Fatalf("receipts table must be the v5 shape after rollback: %s", tableSQL)
	}
}

// TestPeriodClosureEventsImmutable verifies the no-update / no-delete triggers:
// a corrupt or buggy caller cannot mutate closure history.
func TestPeriodClosureEventsImmutable(t *testing.T) {
	s := newTestStore(t)
	closeMem, err := s.Save(closeInputForTest(t, testScope(testRucA), "closure events subject"))
	if err != nil {
		t.Fatalf("save close: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO period_closure_events (id, tenant_id, company_id, fiscal_period_id, action, close_memory_id, approval_event_id, subject_id, reason, request_id, created_at)
		VALUES ('ev-1', ?, 'acme', ?, 'closed', ?, NULL, 'subject-1', 'approved', 'req-1', ?)`,
		testOrgID, testPeriod, closeMem.Memory.Identity.ID, testT,
	); err != nil {
		t.Fatalf("insert closure event: %v", err)
	}

	if _, err := s.db.Exec(`UPDATE period_closure_events SET reason = 'mutated' WHERE id = 'ev-1'`); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_PERIOD_CLOSURE_EVENT") {
		t.Fatalf("UPDATE on period_closure_events must abort with IMMUTABLE_PERIOD_CLOSURE_EVENT, got %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM period_closure_events WHERE id = 'ev-1'`); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_PERIOD_CLOSURE_EVENT") {
		t.Fatalf("DELETE on period_closure_events must abort with IMMUTABLE_PERIOD_CLOSURE_EVENT, got %v", err)
	}
}
