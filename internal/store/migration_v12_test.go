// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module verifies the v11→v12 additive migration (v0.8 batch 4
// physical purge execution — docs/architecture/evidence-lifecycle-v0.8.md
// §2/§3.7/§4/§11):
//
//   - fresh stores bootstrap to schema_version=12 with the immutable
//     evidence_purge_executions attempt ledger (the exact rel_path, the recorded
//     size, the pre-removal hash and the guarded intent → completed/interrupted
//     machine) and its request index + triggers;
//   - the receipts table is REBUILT with the action CHECK extended by the v0.8
//     execution-intent act (purge_intent — the intent transaction is
//     receipt-covered so an interrupted execution is auditable) and the receipts
//     singleton index uq_receipts_singleton excludes purge_intent (retries
//     legitimately emit ONE purge_intent receipt per execution attempt, while
//     the exact-duplicate backstop stays the UNIQUE(subject_type, subject_id,
//     action, payload_hash) table constraint);
//   - existing v11 rows (every receipt, lifecycle row, hold, policy and object)
//     survive byte-preserved; a pre-existing evidence_purge_executions table
//     aborts the migration (fail closed — additive migrations never replay).
package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// v12Tables / v12Indexes / v12Triggers name the v0.8 execution-layer schema
// objects created by migrateV11ToV12.
func v12Tables() []string {
	return []string{"evidence_purge_executions"}
}

func v12Indexes() []string {
	return []string{"idx_evidence_purge_executions_request"}
}

func v12Triggers() []string {
	return []string{"evidence_purge_executions_guarded", "evidence_purge_executions_no_delete"}
}

// openV11Schema opens a raw SQLite handle and bootstraps the EXACT v11 layout
// (applySchema + the v2→v3 … v10→v11 migrations), so tests can exercise the
// v11→v12 migration on a genuine v11 store.
func openV11Schema(t *testing.T, path string) *sql.DB {
	t.Helper()
	db := openV10Schema(t, path)
	if err := migrateV10ToV11(db); err != nil {
		_ = db.Close()
		t.Fatalf("bootstrap v11 layout: %v", err)
	}
	return db
}

func TestFreshStoreBootstrapsV12Executions(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 14 {
		t.Fatalf("schema_version = %d, want 14 (the chain continues v2→v3→…→v11→v12→v13→v14)", version)
	}

	// The whole v3…v11 surface survives the chain (additive migrations never
	// drop) plus the new v12 execution layer.
	for _, table := range append(append(v11Tables(), v12Tables()...), []string{"observations", "evidence_objects"}...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("fresh store missing table %q: %v", table, err)
		}
	}
	for _, idx := range v12Indexes() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("fresh store missing index %q: %v", idx, err)
		}
	}
	for _, trg := range v12Triggers() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trg).Scan(&name); err != nil {
			t.Fatalf("fresh store missing trigger %q: %v", trg, err)
		}
	}

	// The receipts action CHECK is the v12 layout: a purge_intent receipt with
	// its typed evidence_object FK inserts, a SECOND purge_intent receipt for the
	// same subject inserts too (the singleton index excludes the execution
	// intents — retries emit one per attempt), and an unknown action is still
	// rejected by the closed CHECK.
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
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-v12", core.ReceiptAlgorithm, "base64-public-v12", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}
	intentRow := ReceiptRow{
		SubjectType:      core.SubjectTypeEvidenceObject,
		SubjectID:        strings.Repeat("a", 64),
		Action:           core.ReceiptActionPurgeIntent,
		TenantID:         testOrgID,
		CompanyID:        "acme",
		FiscalPeriodID:   testPeriod,
		PayloadHash:      "payload-hash-intent-1",
		PrincipalID:      "subject-1",
		MembershipID:     "membership-1",
		PolicyVersion:    "evidence-lifecycle-policy/v0.8.0",
		Algorithm:        core.ReceiptAlgorithm,
		KeyID:            "ed25519:key-v12",
		Signature:        []byte{12, 12, 12, 12},
		IssuedAt:         testT,
		PayloadJSON:      `{"version":"receipt-payload/v0.9.0"}`,
		ReceiptHash:      "receipt-hash-intent-1",
		EvidenceObjectID: strings.Repeat("a", 64),
	}
	if err := s.InsertReceipt(ctxForTest(), s.db, intentRow); err != nil {
		t.Fatalf("fresh store must accept a purge_intent receipt with its typed FK: %v", err)
	}
	secondIntent := intentRow
	secondIntent.PayloadHash = "payload-hash-intent-2"
	secondIntent.ReceiptHash = "receipt-hash-intent-2"
	if err := s.InsertReceipt(ctxForTest(), s.db, secondIntent); err != nil {
		t.Fatalf("a second purge_intent receipt for the same subject must insert (singleton exemption — retries emit one per attempt): %v", err)
	}
	rogue := intentRow
	rogue.Action = core.ReceiptAction("purge_intent_rogue")
	rogue.ReceiptHash = "receipt-hash-intent-rogue"
	if err := s.InsertReceipt(ctxForTest(), s.db, rogue); err == nil {
		t.Fatal("fresh store must reject an unknown receipt action (closed CHECK)")
	}
}

// TestV12ExecutionsSchemaGuardsLive freezes the v12 executions schema guards on
// a FRESH store: the attempt state machine (intent → completed/interrupted,
// terminal states never re-open), the receipt-covered completion shape
// (completed REQUIRES the completion receipt id; interrupted carries none) and
// the immutability of the recorded evidence columns + deletion.
func TestV12ExecutionsSchemaGuardsLive(t *testing.T) {
	s := newTestStore(t)
	objectID := strings.Repeat("a", 64)
	requestID := "00000000-0000-4000-8000-000000000101"
	if _, err := s.db.Exec(`
		INSERT INTO evidence_objects (
			id, sha256, size, content_type, tenant_id, company_id, ruc, period,
			source_system, source_reference, source_actor_id, source_actor_kind,
			stored_by, stored_at, rel_path
		) VALUES (?, ?, 4, 'application/xml', ?, 'acme', ?, '202401', 'go-test', '', 'agent-1', 'agent', 'agent-1', ?, 'objects/aa/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		objectID, objectID, testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("insert evidence_objects row: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO retention_policies (id, tenant_id, jurisdiction, legislation, authority, source,
			category, min_period, version, dual_approval_required, dual_approver_roles,
			blocking_hold_kinds, enabled, created_at, created_by)
		VALUES ('00000000-0000-4000-8000-000000000001', 'org-001', 'PE', 'NATIONAL-TAX', 'tenant-records',
			'deployment decision', 'invoice', '202401', 1, 1, '["controller","tax_responsible"]',
			'["audit","dispute","fiscalization","legal"]', 1, '2026-08-07T12:00:00.000Z', 'subject-1')`,
	); err != nil {
		t.Fatalf("insert retention_policies row: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO evidence_purge_requests (id, object_id, tenant_id, company_id, ruc, period,
			category, policy_id, retention_state_snapshot, reviewed_lifecycle_hash,
			status, requested_at, requested_by)
		VALUES (?, ?, ?, 'acme', ?, '202401',
			'invoice', '00000000-0000-4000-8000-000000000001', 'eligible',
			'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'approved', ?, 'subject-1')`,
		requestID, objectID, testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("insert purge request row: %v", err)
	}

	insertIntent := func(executionID string) {
		t.Helper()
		if _, err := s.db.Exec(`
			INSERT INTO evidence_purge_executions (execution_id, request_id, object_id, rel_path, size,
				pre_removal_hash, intent_reviewed_hash, state, intent_at, intent_by)
			VALUES (?, ?, ?, 'objects/aa/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 4,
				'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'intent', ?, 'subject-1')`,
			executionID, requestID, objectID, testT,
		); err != nil {
			t.Fatalf("insert intent execution row %s: %v", executionID, err)
		}
	}

	// intent → completed with the receipt-covered completion shape is the ONE
	// legal completion transition.
	insertIntent("00000000-0000-4000-8000-000000000901")
	if _, err := s.db.Exec(`
		UPDATE evidence_purge_executions SET state = 'completed', completed_at = ?, completed_by = 'subject-1', completion_receipt_id = 'receipt-hash-completion-1'
		WHERE execution_id = '00000000-0000-4000-8000-000000000901'`, testT); err != nil {
		t.Fatalf("intent → completed with the completion receipt must succeed: %v", err)
	}
	// A terminal completed attempt never re-opens.
	if _, err := s.db.Exec(`
		UPDATE evidence_purge_executions SET state = 'interrupted'
		WHERE execution_id = '00000000-0000-4000-8000-000000000901'`); err == nil {
		t.Fatal("a completed attempt must never transition (terminal)")
	}
	// intent → interrupted (no completion columns) is the legal retry-marking
	// transition; an interrupted attempt is terminal.
	insertIntent("00000000-0000-4000-8000-000000000902")
	if _, err := s.db.Exec(`
		UPDATE evidence_purge_executions SET state = 'interrupted'
		WHERE execution_id = '00000000-0000-4000-8000-000000000902'`); err != nil {
		t.Fatalf("intent → interrupted must succeed: %v", err)
	}
	if _, err := s.db.Exec(`
		UPDATE evidence_purge_executions SET state = 'completed', completed_at = ?, completed_by = 'subject-1', completion_receipt_id = 'x'
		WHERE execution_id = '00000000-0000-4000-8000-000000000902'`, testT); err == nil {
		t.Fatal("an interrupted attempt must never transition to completed (terminal)")
	}
	// completed REQUIRES the completion receipt id (the completion is
	// receipt-covered); interrupted carries NO completion columns.
	insertIntent("00000000-0000-4000-8000-000000000903")
	if _, err := s.db.Exec(`
		UPDATE evidence_purge_executions SET state = 'completed', completed_at = ?, completed_by = 'subject-1', completion_receipt_id = NULL
		WHERE execution_id = '00000000-0000-4000-8000-000000000903'`, testT); err == nil {
		t.Fatal("a completion without the completion receipt id must fail closed (receipt-covered completion)")
	}
	insertIntent("00000000-0000-4000-8000-000000000904")
	if _, err := s.db.Exec(`
		UPDATE evidence_purge_executions SET state = 'interrupted', completed_at = ?, completed_by = 'subject-1', completion_receipt_id = NULL
		WHERE execution_id = '00000000-0000-4000-8000-000000000904'`, testT); err == nil {
		t.Fatal("an interrupted attempt must carry NO completion columns")
	}
	// The recorded evidence columns never change (even alongside a legal state
	// transition).
	insertIntent("00000000-0000-4000-8000-000000000905")
	if _, err := s.db.Exec(`
		UPDATE evidence_purge_executions SET rel_path = 'objects/zz/zz/zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz'
		WHERE execution_id = '00000000-0000-4000-8000-000000000905'`); err == nil {
		t.Fatal("UPDATE on the recorded evidence columns must fail closed (IMMUTABLE_PURGE_EXECUTION)")
	}
	// The BOUND intent_reviewed_hash is immutable too: the guarded transition
	// must keep the frozen authorization byte-identical.
	insertIntent("00000000-0000-4000-8000-000000000906")
	if _, err := s.db.Exec(`
		UPDATE evidence_purge_executions SET intent_reviewed_hash = 'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc'
		WHERE execution_id = '00000000-0000-4000-8000-000000000906'`); err == nil {
		t.Fatal("UPDATE on intent_reviewed_hash must fail closed (IMMUTABLE_PURGE_EXECUTION)")
	}
	// Deletion is forbidden — execution records are permanent.
	if _, err := s.db.Exec(`DELETE FROM evidence_purge_executions WHERE execution_id = '00000000-0000-4000-8000-000000000901'`); err == nil {
		t.Fatal("DELETE on evidence_purge_executions must fail closed (IMMUTABLE_PURGE_EXECUTION)")
	}
}

// TestV11StoreMigratesToV12AdditivelyPreservingRows drives a genuine v11 store
// (with seeded receipts and lifecycle rows) through migrateV11ToV12: every
// seeded row survives byte-preserved, schema_version flips to 12 only after the
// whole migration succeeded, a purge_intent receipt inserts on the migrated
// layout, and a pre-existing evidence_purge_executions table aborts the
// migration (fail closed — additive migrations never replay).
func TestV11StoreMigratesToV12AdditivelyPreservingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-v11.db")
	db := openV11Schema(t, path)

	// Seed the v11 rows the migration must preserve byte-preserved: one object,
	// one retention policy, one approved purge request and a purge_requested
	// receipt.
	objectID := strings.Repeat("b", 64)
	if _, err := db.Exec(`
		INSERT INTO evidence_objects (
			id, sha256, size, content_type, tenant_id, company_id, ruc, period,
			source_system, source_reference, source_actor_id, source_actor_kind,
			stored_by, stored_at, rel_path
		) VALUES (?, ?, 4, 'application/xml', ?, 'acme', ?, '202401', 'go-test', '', 'agent-1', 'agent', 'agent-1', ?, 'objects/bb/bb/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')`,
		objectID, objectID, testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("seed v11 evidence_objects row: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO retention_policies (id, tenant_id, jurisdiction, legislation, authority, source,
			category, min_period, version, dual_approval_required, dual_approver_roles,
			blocking_hold_kinds, enabled, created_at, created_by)
		VALUES ('00000000-0000-4000-8000-000000000001', 'org-001', 'PE', 'NATIONAL-TAX', 'tenant-records',
			'deployment decision', 'invoice', '202401', 1, 1, '["controller","tax_responsible"]',
			'["audit","dispute","fiscalization","legal"]', 1, '2026-08-07T12:00:00.000Z', 'subject-1')`,
	); err != nil {
		t.Fatalf("seed v11 retention_policies row: %v", err)
	}
	requestID := "00000000-0000-4000-8000-000000000101"
	if _, err := db.Exec(`
		INSERT INTO evidence_purge_requests (id, object_id, tenant_id, company_id, ruc, period,
			category, policy_id, retention_state_snapshot, reviewed_lifecycle_hash,
			status, requested_at, requested_by)
		VALUES (?, ?, ?, 'acme', ?, '202401',
			'invoice', '00000000-0000-4000-8000-000000000001', 'eligible',
			'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'approved', ?, 'subject-1')`,
		requestID, objectID, testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("seed v11 purge request row: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO signing_keys (key_id, algorithm, public_key, created_at, revoked_at)
		VALUES ('ed25519:key-v12-seed', 'Ed25519', 'base64-public-v12-seed', ?, NULL)`, testT,
	); err != nil {
		t.Fatalf("seed v11 signing key: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO receipts (subject_type, subject_id, action, tenant_id, company_id,
			fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
			policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
			evidence_object_id)
		VALUES ('evidence_object', ?, 'purge_requested', ?, 'acme', '202401',
			'payload-hash-v11-seed', '', 'subject-1', 'membership-1', 'evidence-lifecycle-policy/v0.8.0',
			'Ed25519', 'ed25519:key-v12-seed', X'0b0b0b0b', ?, '{"version":"receipt-payload/v0.9.0"}',
			'receipt-hash-v11-seed', ?)`,
		objectID, testOrgID, testT, objectID,
	); err != nil {
		t.Fatalf("seed v11 purge_requested receipt: %v", err)
	}

	// The migration must not run while evidence_purge_executions already exists
	// (fail closed on a corruption signal — additive migrations never replay).
	preExisting := filepath.Join(t.TempDir(), "engram-v11-corrupt.db")
	db2 := openV11Schema(t, preExisting)
	if _, err := db2.Exec(`CREATE TABLE evidence_purge_executions (execution_id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("pre-create evidence_purge_executions: %v", err)
	}
	if err := migrateV11ToV12(db2); err == nil || !strings.Contains(err.Error(), "pre-existing table") {
		t.Fatalf("migrateV11ToV12 on a store with evidence_purge_executions = %v, want a fail-closed pre-existing-table abort", err)
	}

	// The genuine v11 store migrates in one transaction.
	if err := migrateV11ToV12(db); err != nil {
		t.Fatalf("migrate v11→v12: %v", err)
	}
	var version string
	if err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "12" {
		t.Fatalf("schema_version = %q, want 12 (the direct v11→v12 migration stops at 12; the full chain reaches v14 via Open)", version)
	}

	// The seeded receipt survived byte-preserved (no backfill, no re-hash).
	var storedHash, storedAction, storedPayload string
	if err := db.QueryRow(`SELECT receipt_hash, action, payload_json FROM receipts WHERE receipt_hash = 'receipt-hash-v11-seed'`).
		Scan(&storedHash, &storedAction, &storedPayload); err != nil {
		t.Fatalf("seeded purge_requested receipt lost: %v", err)
	}
	if storedHash != "receipt-hash-v11-seed" || storedAction != "purge_requested" || storedPayload != `{"version":"receipt-payload/v0.9.0"}` {
		t.Fatalf("purge_requested receipt altered: (%q, %q, %q)", storedHash, storedAction, storedPayload)
	}
	var requestCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM evidence_purge_requests`).Scan(&requestCount); err != nil {
		t.Fatalf("read request count: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("seeded purge request lost: count = %d", requestCount)
	}

	// The v12 executions table + guards are live after the migration.
	for _, table := range v12Tables() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("migrated store missing table %q: %v", table, err)
		}
	}
	for _, idx := range v12Indexes() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("migrated store missing index %q: %v", idx, err)
		}
	}
	for _, trg := range v12Triggers() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trg).Scan(&name); err != nil {
			t.Fatalf("migrated store missing trigger %q: %v", trg, err)
		}
	}

	// The extended action CHECK is LIVE: a purge_intent receipt inserts on the
	// migrated layout with its typed FK.
	if _, err := db.Exec(`
		INSERT INTO receipts (subject_type, subject_id, action, tenant_id, company_id,
			fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
			policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
			evidence_object_id)
		VALUES ('evidence_object', ?, 'purge_intent', ?, 'acme', '202401',
			'payload-hash-intent-v12', 'receipt-hash-v11-seed', 'subject-1', 'membership-1',
			'evidence-lifecycle-policy/v0.8.0', 'Ed25519', 'ed25519:key-v12-seed', X'0c0c0c0c', ?,
			'{"version":"receipt-payload/v0.9.0"}', 'receipt-hash-intent-v12', ?)`,
		objectID, testOrgID, testT, objectID,
	); err != nil {
		t.Fatalf("a purge_intent receipt must insert after the v12 action-CHECK extension: %v", err)
	}

	// The migrated executions table is usable end-to-end: an intent row inserts
	// for the seeded object/request FKs, and the guarded completion shape holds.
	if _, err := db.Exec(`
		INSERT INTO evidence_purge_executions (execution_id, request_id, object_id, rel_path, size,
			pre_removal_hash, intent_reviewed_hash, state, intent_at, intent_by)
		VALUES ('00000000-0000-4000-8000-000000000999', ?, ?, 'objects/bb/bb/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 4,
			'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'intent', ?, 'subject-1')`,
		requestID, objectID, testT,
	); err != nil {
		t.Fatalf("migrated store must accept an intent execution row: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE evidence_purge_executions SET state = 'interrupted'
		WHERE execution_id = '00000000-0000-4000-8000-000000000999'`); err != nil {
		t.Fatalf("migrated store must accept the guarded intent → interrupted transition: %v", err)
	}
}
