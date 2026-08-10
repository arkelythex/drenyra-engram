// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module verifies the v10→v11 additive migration (v0.8 batch 4
// purge pipeline — docs/architecture/evidence-lifecycle-v0.8.md §2/§3/§4/§5/§9):
// fresh stores bootstrap to schema_version=12 (the chain continues through the
// v11→v12 execution migration, covered by migration_v12_test.go) with the five
// purge lifecycle
// tables (the immutable ONE-OPEN-PIPELINE-PER-OBJECT evidence_purge_requests
// aggregate, the immutable evidence_purge_approvals decision ledger, the
// immutable evidence_lifecycle_events log, the guarded evidence_retention_state
// projection and the tenant-scoped evidence_purge_idempotency_keys ledger) and
// their indexes/guards; existing v10 data (every receipt, hold, policy and
// object row) survives byte-preserved; the receipts singleton index
// uq_receipts_singleton is REBUILT with the seven v0.8 evidence-lifecycle acts
// excluded (purge acts legitimately GROW per object — dual approval emits two
// purge_approved receipts, retractions restart the pipeline — while the exact
// duplicate backstop stays the UNIQUE(subject_type, subject_id, action,
// payload_hash) table constraint and legacy singleton actions still enforce);
// a pre-existing v11 table aborts the migration (fail closed — additive
// migrations never replay).
package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// v11Tables / v11Indexes / v11Triggers name the v0.8 purge-pipeline schema
// objects created by migrateV10ToV11.
func v11Tables() []string {
	return []string{
		"evidence_purge_requests", "evidence_purge_approvals", "evidence_lifecycle_events",
		"evidence_retention_state", "evidence_purge_idempotency_keys",
	}
}

func v11Indexes() []string {
	return []string{
		"idx_evidence_purge_requests_scope", "idx_evidence_purge_approvals_request",
		"idx_evidence_lifecycle_events_object",
	}
}

func v11Triggers() []string {
	return []string{
		"evidence_purge_requests_immutable", "evidence_purge_requests_status_guard", "evidence_purge_requests_no_delete",
		"evidence_purge_approvals_no_update", "evidence_purge_approvals_no_delete",
		"evidence_lifecycle_events_no_update", "evidence_lifecycle_events_no_delete",
	}
}

// openV10Schema opens a raw SQLite handle and bootstraps the EXACT v10 layout
// (applySchema + the v2→v3 … v9→v10 migrations), so tests can exercise the
// v10→v11 migration on a genuine v10 store.
func openV10Schema(t *testing.T, path string) *sql.DB {
	t.Helper()
	db := openV9Schema(t, path)
	if err := migrateV9ToV10(db); err != nil {
		_ = db.Close()
		t.Fatalf("bootstrap v10 layout: %v", err)
	}
	return db
}

// seedV10EvidenceRows seeds the representative v10 rows the migration must
// preserve byte-preserved: one evidence object, one retention policy (v9), one
// hold (v10), the signing key and two receipts — a legacy object_stored receipt
// and a v0.8 purge_requested receipt (the singleton index still covers
// purge_requested in v10: exactly one such receipt may exist per subject).
func seedV10EvidenceRows(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO evidence_objects (
			id, sha256, size, content_type, tenant_id, company_id, ruc, period,
			source_system, source_reference, source_actor_id, source_actor_kind,
			stored_by, stored_at, rel_path
		) VALUES ('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 4,
			'application/xml', ?, 'acme', ?, '202401', 'go-test', '', 'agent-1', 'agent', 'agent-1', ?, 'objects/bb/bb/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')`,
		testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("seed v10 evidence_objects row: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO retention_policies (id, tenant_id, jurisdiction, legislation, authority, source,
			category, min_period, version, dual_approval_required, dual_approver_roles,
			blocking_hold_kinds, enabled, created_at, created_by)
		VALUES ('00000000-0000-4000-8000-000000000001', 'org-001', 'PE', 'NATIONAL-TAX', 'tenant-records',
			'deployment decision', 'invoice', '202401', 1, 1, '["controller","tax_responsible"]',
			'["audit","dispute","fiscalization","legal"]', 1, '2026-08-07T12:00:00.000Z', 'subject-1')`,
	); err != nil {
		t.Fatalf("seed v10 retention_policies row: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO evidence_holds (id, object_id, tenant_id, company_id, ruc, period,
			kind, reason, owner_subject_id, placed_at, placed_by)
		VALUES ('00000000-0000-4000-8000-000000000010', ?, ?, 'acme', ?, '202401',
			'legal', 'dispute under review', 'subject-1', ?, 'subject-1')`,
		strings.Repeat("b", 64), testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("seed v10 evidence_holds row: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO signing_keys (key_id, algorithm, public_key, created_at, revoked_at)
		VALUES ('ed25519:key-v11-seed', 'Ed25519', 'base64-public-v11-seed', ?, NULL)`, testT,
	); err != nil {
		t.Fatalf("seed v10 signing key: %v", err)
	}
	// A legacy v0.7 receipt action (singleton-covered in every layout).
	if _, err := db.Exec(`
		INSERT INTO receipts (subject_type, subject_id, action, tenant_id, company_id,
			fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
			policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
			evidence_object_id)
		VALUES ('evidence_object', ?, 'object_stored', ?, 'acme', '202401',
			'payload-hash-object-stored', '', 'agent-1', '', 'kernel/v0.7.0', 'Ed25519', 'ed25519:key-v11-seed',
			X'01020304', ?, '{"version":"receipt-payload/v0.7.0"}', 'receipt-hash-object-stored-v10', ?)`,
		strings.Repeat("b", 64), testOrgID, testT, strings.Repeat("b", 64),
	); err != nil {
		t.Fatalf("seed v10 object_stored receipt: %v", err)
	}
	// A v0.8 purge_requested receipt (the migration's singleton-index rebuild is
	// what makes a SECOND such receipt legal per object).
	if _, err := db.Exec(`
		INSERT INTO receipts (subject_type, subject_id, action, tenant_id, company_id,
			fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
			policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
			evidence_object_id)
		VALUES ('evidence_object', ?, 'purge_requested', ?, 'acme', '202401',
			'payload-hash-purge-1', '', 'subject-1', 'membership-1', 'evidence-lifecycle-policy/v0.8.0',
			'Ed25519', 'ed25519:key-v11-seed', X'09090909', ?, '{"version":"receipt-payload/v0.9.0"}',
			'receipt-hash-purge-1-v10', ?)`,
		strings.Repeat("b", 64), testOrgID, testT, strings.Repeat("b", 64),
	); err != nil {
		t.Fatalf("seed v10 purge_requested receipt: %v", err)
	}
}

func TestFreshStoreBootstrapsV11PurgeLifecycle(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 14 {
		t.Fatalf("schema_version = %d, want 14 (the chain continues v2→v3→…→v10→v11→v12→v13→v14)", version)
	}

	// The whole v3…v10 surface survives the chain (additive migrations never
	// drop) plus the new v11 purge layer.
	for _, table := range append(append(append(append(append(append(v3Tables(), v4Tables()...), v5Tables()...), v6Tables()...), []string{"observations", "evidence_objects"}...), append(v9Tables(), v10Tables()...)...), v11Tables()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("fresh store missing table %q: %v", table, err)
		}
	}
	for _, idx := range append(append(append(append(append(v4Indexes(), v5Indexes()...), v6Indexes()...), v8ObjectIndexes()...), append(v9Indexes(), v10Indexes()...)...), v11Indexes()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("fresh store missing index %q: %v", idx, err)
		}
	}
	for _, trg := range append(append(append(append(v6Triggers(), []string{"observations_immutable_content", "evidence_objects_no_update", "evidence_objects_no_delete"}...), v8ObjectTriggers()...), append(v9Triggers(), v10Triggers()...)...), v11Triggers()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trg).Scan(&name); err != nil {
			t.Fatalf("fresh store missing trigger %q: %v", trg, err)
		}
	}

	// uq_receipts_singleton is the v12 rebuild (the chain continues through the
	// v11→v12 execution migration): the legacy append-only exclusions PLUS the
	// eight v0.8 evidence-lifecycle acts INCLUDING purge_intent (purge acts
	// legitimately GROW per object and per execution attempt; the exact-duplicate
	// UNIQUE stays).
	var indexSQL string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'uq_receipts_singleton'`).Scan(&indexSQL); err != nil {
		t.Fatalf("read uq_receipts_singleton definition: %v", err)
	}
	if !strings.Contains(indexSQL, "UNIQUE") || !strings.Contains(indexSQL, "'evidence_linked','hold_placed','hold_lifted',") ||
		!strings.Contains(indexSQL, "'retention_bound','purge_requested','purge_approved','purge_rejected',") ||
		!strings.Contains(indexSQL, "'purge_cancelled','purge_withdrawn','purge_intent','purge_executed'") {
		t.Fatalf("uq_receipts_singleton is not the v12 append-only-action partial unique index: %s", indexSQL)
	}

	// The receipts action CHECK is the v11 layout: a purge_requested receipt
	// with its typed evidence_object FK inserts and an unknown action is still
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
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-v11", core.ReceiptAlgorithm, "base64-public-v11", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}
	purgeRow := ReceiptRow{
		SubjectType:      core.SubjectTypeEvidenceObject,
		SubjectID:        strings.Repeat("a", 64),
		Action:           core.ReceiptActionPurgeRequested,
		TenantID:         testOrgID,
		CompanyID:        "acme",
		FiscalPeriodID:   testPeriod,
		PayloadHash:      "payload-hash-purge-fresh",
		PrincipalID:      "subject-1",
		MembershipID:     "membership-1",
		PolicyVersion:    "evidence-lifecycle-policy/v0.8.0",
		Algorithm:        core.ReceiptAlgorithm,
		KeyID:            "ed25519:key-v11",
		Signature:        []byte{11, 11, 11, 11},
		IssuedAt:         testT,
		PayloadJSON:      `{"version":"receipt-payload/v0.9.0"}`,
		ReceiptHash:      "receipt-hash-purge-fresh",
		EvidenceObjectID: strings.Repeat("a", 64),
	}
	if err := s.InsertReceipt(ctxForTest(), s.db, purgeRow); err != nil {
		t.Fatalf("fresh store must accept a purge_requested receipt with its typed FK: %v", err)
	}
	rogueRow := purgeRow
	rogueRow.Action = core.ReceiptAction("purge_reopened")
	if err := s.InsertReceipt(ctxForTest(), s.db, rogueRow); err == nil {
		t.Fatal("fresh store must reject an unknown receipt action (closed CHECK)")
	}
}

// TestV11PurgeSchemaGuardsLive freezes the v11 purge schema guards on a FRESH
// store: the ONE-OPEN-PIPELINE-PER-OBJECT request aggregate (UNIQUE object_id),
// the guarded status machine with the retraction-cycle exception, the immutable
// request evidence, the immutable approval/event rows, the guarded projection
// and the tenant-scoped idempotency ledger.
func TestV11PurgeSchemaGuardsLive(t *testing.T) {
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
		INSERT INTO retention_policies (id, tenant_id, jurisdiction, legislation, authority, source,
			category, min_period, version, dual_approval_required, dual_approver_roles,
			blocking_hold_kinds, enabled, created_at, created_by)
		VALUES ('00000000-0000-4000-8000-000000000001', 'org-001', 'PE', 'NATIONAL-TAX', 'tenant-records',
			'deployment decision', 'invoice', '202401', 1, 1, '["controller","tax_responsible"]',
			'["audit","dispute","fiscalization","legal"]', 1, '2026-08-07T12:00:00.000Z', 'subject-1')`,
	); err != nil {
		t.Fatalf("insert retention_policies row: %v", err)
	}

	requestID := "00000000-0000-4000-8000-000000000101"
	// ONE open pipeline per object: the request row inserts with its FKs, a
	// second open pipeline for the SAME object aborts (UNIQUE object_id).
	if _, err := s.db.Exec(`
		INSERT INTO evidence_purge_requests (id, object_id, tenant_id, company_id, ruc, period,
			category, policy_id, retention_state_snapshot, reviewed_lifecycle_hash,
			status, requested_at, requested_by)
		VALUES (?, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', ?, 'acme', ?, '202401',
			'invoice', '00000000-0000-4000-8000-000000000001', 'eligible',
			'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'requested', ?, 'subject-1')`,
		requestID, testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("insert purge request row: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO evidence_purge_requests (id, object_id, tenant_id, company_id, ruc, period,
			category, policy_id, retention_state_snapshot, reviewed_lifecycle_hash,
			status, requested_at, requested_by)
		VALUES ('00000000-0000-4000-8000-000000000102', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', ?, 'acme', ?, '202401',
			'invoice', '00000000-0000-4000-8000-000000000001', 'eligible',
			'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'requested', ?, 'subject-2')`,
		testOrgID, testRucA, testT,
	); err == nil {
		t.Fatal("a second open pipeline for the same object must abort (UNIQUE object_id)")
	}

	// The guarded status machine: requested → approved is legal, approved →
	// purged is NOT (execution is the next work unit — purge_approved never
	// self-executes), and a withdrawn pipeline may be re-requested as a fresh
	// act (the retraction-cycle exception).
	if _, err := s.db.Exec(`UPDATE evidence_purge_requests SET status = 'approved', approved_at = ? WHERE id = ?`, testT, requestID); err != nil {
		t.Fatalf("guarded transition requested → approved must succeed: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE evidence_purge_requests SET status = 'purged' WHERE id = ?`, requestID); err == nil {
		t.Fatal("requested → purged must fail closed (execution is the next work unit)")
	}
	if _, err := s.db.Exec(`UPDATE evidence_purge_requests SET status = 'withdrawn' WHERE id = ?`, requestID); err != nil {
		t.Fatalf("guarded transition approved → withdrawn must succeed: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE evidence_purge_requests SET status = 'requested', approved_at = NULL WHERE id = ?`, requestID); err != nil {
		t.Fatalf("retraction cycle (withdrawn → requested) must succeed: %v", err)
	}

	// The request evidence is immutable: rewriting a reviewable evidence column
	// aborts, and deletion is forbidden (the pipeline is a permanent record).
	if _, err := s.db.Exec(`UPDATE evidence_purge_requests SET category = 'other' WHERE id = ?`, requestID); err == nil {
		t.Fatal("UPDATE on a request evidence column must fail closed (IMMUTABLE_PURGE_REQUEST)")
	}
	if _, err := s.db.Exec(`DELETE FROM evidence_purge_requests WHERE id = ?`, requestID); err == nil {
		t.Fatal("DELETE on evidence_purge_requests must fail closed (IMMUTABLE_PURGE_REQUEST)")
	}

	// The approval ledger is IMMUTABLE: an approved/rejected/withdrawn decision
	// row inserts and never changes.
	if _, err := s.db.Exec(`
		INSERT INTO evidence_purge_approvals (id, request_id, approval_order, decision,
			reviewed_hash, resulting_hash, principal_snapshot_json, reason, policy_version, created_at)
		VALUES ('00000000-0000-4000-8000-000000000201', ?, 1, 'approved',
			'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
			'{"subjectId":"subject-1","membershipId":"membership-1","roles":["controller"],"authenticationMethod":"session","assuranceLevel":"standard","authenticatedAt":"2026-08-08T09:00:00.000Z"}',
			'verified against the reviewed snapshot', 'evidence-lifecycle-policy/v0.8.0', ?)`,
		requestID, testT,
	); err != nil {
		t.Fatalf("insert purge approval row: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE evidence_purge_approvals SET reason = 'changed' WHERE id = '00000000-0000-4000-8000-000000000201'`); err == nil {
		t.Fatal("UPDATE on evidence_purge_approvals must fail closed (IMMUTABLE_PURGE_APPROVAL)")
	}
	if _, err := s.db.Exec(`DELETE FROM evidence_purge_approvals WHERE id = '00000000-0000-4000-8000-000000000201'`); err == nil {
		t.Fatal("DELETE on evidence_purge_approvals must fail closed (IMMUTABLE_PURGE_APPROVAL)")
	}

	// The event log is IMMUTABLE (the queryable source of truth).
	if _, err := s.db.Exec(`
		INSERT INTO evidence_lifecycle_events (id, object_id, request_id, action,
			from_state, to_state, reviewed_hash, resulting_hash, principal_snapshot_json,
			reason, policy_version, created_at)
		VALUES ('00000000-0000-4000-8000-000000000301', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', ?,
			'purge_requested', 'stored', 'purge_requested',
			'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
			'{"subjectId":"subject-1","membershipId":"membership-1","roles":["controller"],"authenticationMethod":"session","assuranceLevel":"standard","authenticatedAt":"2026-08-08T09:00:00.000Z"}',
			'retention period elapsed', 'evidence-lifecycle-policy/v0.8.0', ?)`,
		requestID, testT,
	); err != nil {
		t.Fatalf("insert lifecycle event row: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE evidence_lifecycle_events SET reason = 'changed' WHERE id = '00000000-0000-4000-8000-000000000301'`); err == nil {
		t.Fatal("UPDATE on evidence_lifecycle_events must fail closed (IMMUTABLE_LIFECYCLE_EVENT)")
	}
	if _, err := s.db.Exec(`DELETE FROM evidence_lifecycle_events WHERE id = '00000000-0000-4000-8000-000000000301'`); err == nil {
		t.Fatal("DELETE on evidence_lifecycle_events must fail closed (IMMUTABLE_LIFECYCLE_EVENT)")
	}

	// The guarded projection upserts (derived queryable state, never a separate
	// authority).
	if _, err := s.db.Exec(`
		INSERT INTO evidence_retention_state (object_id, lifecycle_state, retention_state,
			policy_id, category, has_active_blocking_hold, current_hash, updated_at)
		VALUES ('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'purge_requested', 'eligible',
			'00000000-0000-4000-8000-000000000001', 'invoice', 0,
			'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', ?)`,
		testT,
	); err != nil {
		t.Fatalf("insert retention state projection row: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO evidence_retention_state (object_id, lifecycle_state, retention_state,
			policy_id, category, has_active_blocking_hold, current_hash, updated_at)
		VALUES ('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'stored', 'unmanaged',
			NULL, '', 0, 'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', ?)`,
		testT,
	); err == nil {
		t.Fatal("a second projection row for the same object must abort (object_id PRIMARY KEY)")
	}

	// The tenant-scoped idempotency ledger: entity_id and result_json move
	// together (CHECK((entity_id IS NULL) = (result_json IS NULL))).
	if _, err := s.db.Exec(`
		INSERT INTO evidence_purge_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, entity_id, result_json, created_at, completed_at)
		VALUES (?, 'req-fresh-1', 'command-hash-1', 'subject-1', NULL, NULL, ?, NULL)`,
		testOrgID, testT,
	); err != nil {
		t.Fatalf("insert purge idempotency reservation: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO evidence_purge_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, entity_id, result_json, created_at, completed_at)
		VALUES (?, 'req-fresh-2', 'command-hash-2', 'subject-1', '00000000-0000-4000-8000-000000000101', NULL, ?, NULL)`,
		testOrgID, testT,
	); err == nil {
		t.Fatal("an idempotency row with entity_id but NULL result_json must abort (CHECK)")
	}
}

func TestV10StoreMigratesToV11AdditivelyPreservingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-v10.db")
	db := openV10Schema(t, path)
	seedV10EvidenceRows(t, db)

	// The migration must not run while the v11 tables already exist (fail closed
	// on a corruption signal — additive migrations never replay). Pre-create
	// evidence_purge_requests on a COPY, not the migrated store.
	preExisting := filepath.Join(t.TempDir(), "engram-v10-corrupt.db")
	db2 := openV10Schema(t, preExisting)
	if _, err := db2.Exec(`CREATE TABLE evidence_purge_requests (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("pre-create evidence_purge_requests: %v", err)
	}
	if err := migrateV10ToV11(db2); err == nil || !strings.Contains(err.Error(), "pre-existing table") {
		t.Fatalf("migrateV10ToV11 on a store with evidence_purge_requests = %v, want a fail-closed pre-existing-table abort", err)
	}

	// The genuine v10 store migrates in one transaction.
	if err := migrateV10ToV11(db); err != nil {
		t.Fatalf("migrate v10→v11: %v", err)
	}
	var version string
	if err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "11" {
		t.Fatalf("schema_version = %q, want 11", version)
	}

	// Every seeded v10 row survived byte-preserved (no backfill, no re-hash):
	// the object, the retention policy, the hold and BOTH receipts verbatim.
	var storedHash, storedAction, storedPayload string
	if err := db.QueryRow(`SELECT receipt_hash, action, payload_json FROM receipts WHERE receipt_hash = 'receipt-hash-object-stored-v10'`).
		Scan(&storedHash, &storedAction, &storedPayload); err != nil {
		t.Fatalf("seeded object_stored receipt lost: %v", err)
	}
	if storedHash != "receipt-hash-object-stored-v10" || storedAction != "object_stored" || storedPayload != `{"version":"receipt-payload/v0.7.0"}` {
		t.Fatalf("object_stored receipt altered: (%q, %q, %q)", storedHash, storedAction, storedPayload)
	}
	if err := db.QueryRow(`SELECT receipt_hash, action, payload_json FROM receipts WHERE receipt_hash = 'receipt-hash-purge-1-v10'`).
		Scan(&storedHash, &storedAction, &storedPayload); err != nil {
		t.Fatalf("seeded purge_requested receipt lost: %v", err)
	}
	if storedHash != "receipt-hash-purge-1-v10" || storedAction != "purge_requested" || storedPayload != `{"version":"receipt-payload/v0.9.0"}` {
		t.Fatalf("purge_requested receipt altered: (%q, %q, %q)", storedHash, storedAction, storedPayload)
	}
	var policyID string
	if err := db.QueryRow(`SELECT id FROM retention_policies WHERE id = '00000000-0000-4000-8000-000000000001'`).Scan(&policyID); err != nil {
		t.Fatalf("seeded retention policy lost: %v", err)
	}
	var holdID string
	if err := db.QueryRow(`SELECT id FROM evidence_holds WHERE id = '00000000-0000-4000-8000-000000000010'`).Scan(&holdID); err != nil {
		t.Fatalf("seeded hold lost: %v", err)
	}

	// The v11 purge tables + guards are live after the migration.
	for _, table := range v11Tables() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("migrated store missing table %q: %v", table, err)
		}
	}
	for _, idx := range v11Indexes() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("migrated store missing index %q: %v", idx, err)
		}
	}
	for _, trg := range v11Triggers() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trg).Scan(&name); err != nil {
			t.Fatalf("migrated store missing trigger %q: %v", trg, err)
		}
	}

	// THE SINGLETON-INDEX REBUILD (design §4 step 3 / §5): the v10 index still
	// enforced ONE purge_requested receipt per subject; the v11 rebuild EXCLUDES
	// the seven v0.8 lifecycle acts, so a SECOND purge_requested receipt for the
	// same subject (a fresh act with a different payload) inserts — dual approval
	// emits two purge_approved receipts and retractions restart the pipeline.
	if _, err := db.Exec(`
		INSERT INTO receipts (subject_type, subject_id, action, tenant_id, company_id,
			fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
			policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
			evidence_object_id)
		VALUES ('evidence_object', ?, 'purge_requested', ?, 'acme', '202401',
			'payload-hash-purge-2', 'receipt-hash-purge-1-v10', 'subject-1', 'membership-1',
			'evidence-lifecycle-policy/v0.8.0', 'Ed25519', 'ed25519:key-v11-seed', X'0a0a0a0a', ?,
			'{"version":"receipt-payload/v0.9.0"}', 'receipt-hash-purge-2-v11', ?)`,
		strings.Repeat("b", 64), testOrgID, testT, strings.Repeat("b", 64),
	); err != nil {
		t.Fatalf("a second purge_requested receipt must insert after the v11 singleton-index rebuild: %v", err)
	}
	// The EXACT-duplicate backstop stays the UNIQUE(subject_type, subject_id,
	// action, payload_hash) table constraint: the identical act again aborts.
	if _, err := db.Exec(`
		INSERT INTO receipts (subject_type, subject_id, action, tenant_id, company_id,
			fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
			policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
			evidence_object_id)
		VALUES ('evidence_object', ?, 'purge_requested', ?, 'acme', '202401',
			'payload-hash-purge-2', '', 'subject-1', 'membership-1',
			'evidence-lifecycle-policy/v0.8.0', 'Ed25519', 'ed25519:key-v11-seed', X'0a0a0a0a', ?,
			'{"version":"receipt-payload/v0.9.0"}', 'receipt-hash-purge-2-duplicate', ?)`,
		strings.Repeat("b", 64), testOrgID, testT, strings.Repeat("b", 64),
	); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("an exact-duplicate purge receipt must abort on the UNIQUE backstop, got %v", err)
	}
	// A LEGACY singleton action still enforces: a second object_stored receipt
	// for the same subject aborts (the rebuild only EXCLUDED the lifecycle acts).
	if _, err := db.Exec(`
		INSERT INTO receipts (subject_type, subject_id, action, tenant_id, company_id,
			fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
			policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
			evidence_object_id)
		VALUES ('evidence_object', ?, 'object_stored', ?, 'acme', '202401',
			'payload-hash-object-stored-2', '', 'agent-1', '', 'kernel/v0.7.0', 'Ed25519',
			'ed25519:key-v11-seed', X'01020304', ?, '{"version":"receipt-payload/v0.7.0"}',
			'receipt-hash-object-stored-2', ?)`,
		strings.Repeat("b", 64), testOrgID, testT, strings.Repeat("b", 64),
	); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("a second object_stored receipt must still abort (singleton preserved for legacy actions), got %v", err)
	}

	// The migrated purge schema is usable end-to-end: a request row inserts for
	// the seeded object/policy FKs, and a second open pipeline for the same
	// object still aborts (UNIQUE object_id).
	if _, err := db.Exec(`
		INSERT INTO evidence_purge_requests (id, object_id, tenant_id, company_id, ruc, period,
			category, policy_id, retention_state_snapshot, reviewed_lifecycle_hash,
			status, requested_at, requested_by)
		VALUES ('00000000-0000-4000-8000-000000000401', ?, ?, 'acme', ?, '202401',
			'invoice', '00000000-0000-4000-8000-000000000001', 'eligible',
			'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'requested', ?, 'subject-1')`,
		strings.Repeat("b", 64), testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("migrated store must accept a purge request row: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO evidence_purge_requests (id, object_id, tenant_id, company_id, ruc, period,
			category, policy_id, retention_state_snapshot, reviewed_lifecycle_hash,
			status, requested_at, requested_by)
		VALUES ('00000000-0000-4000-8000-000000000402', ?, ?, 'acme', ?, '202401',
			'invoice', '00000000-0000-4000-8000-000000000001', 'eligible',
			'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'requested', ?, 'subject-2')`,
		strings.Repeat("b", 64), testOrgID, testRucA, testT,
	); err == nil {
		t.Fatal("migrated store must reject a second open pipeline for the same object (UNIQUE object_id)")
	}
}
