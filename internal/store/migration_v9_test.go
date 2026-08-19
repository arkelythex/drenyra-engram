// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module verifies the v8→v9 additive migration (v0.8 batch 2
// retention policies — docs/architecture/evidence-lifecycle-v0.8.md §4):
// fresh stores bootstrap to schema_version=9 with the immutable
// retention_policies table (exact scope columns, scope index, no-update/
// no-delete triggers) and the tenant-scoped idempotency ledger; existing v8
// data (every receipt row) survives the receipts_v9 rebuild byte-preserved;
// the extended action CHECK is LIVE (a retention_bound receipt with its typed
// FK inserts); the membership_roles role CHECK is extended by the four v0.8
// lifecycle roles (records_compliance_officer, tenant_records_owner,
// tax_responsible, operational_accountant — the identity-persistence half of
// the role contract, design §8.1) with every existing role row preserved
// byte-for-byte and unknown roles still rejected; a pre-existing v9 table
// aborts the migration (fail closed — additive migrations never replay).
package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// v9Tables / v9Indexes / v9Triggers name the v0.8 retention-policy schema
// objects created by migrateV8ToV9.
func v9Tables() []string {
	return []string{"retention_policies", "retention_policy_idempotency_keys"}
}

func v9Indexes() []string { return []string{"idx_retention_policies_scope"} }

func v9Triggers() []string {
	return []string{"retention_policies_no_update", "retention_policies_no_delete"}
}

// openV8Schema opens a raw SQLite handle and bootstraps the EXACT v8 layout
// (applySchema + the v2→v3 … v7→v8 migrations), so tests can exercise the
// v8→v9 migration on a genuine v8 store.
func openV8Schema(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw v8 database: %v", err)
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
		migrateV2ToV3, migrateV3ToV4, migrateV4ToV5, migrateV5ToV6, migrateV6ToV7, migrateV7ToV8,
	} {
		if err := migrate(db); err != nil {
			_ = db.Close()
			t.Fatalf("bootstrap v8 layout: %v", err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestFreshStoreBootstrapsV9RetentionPolicies(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 17 {
		t.Fatalf("schema_version = %d, want 17 (the chain continues v2→v3→v4→v5→v6→v7→v8→v9→v10→v11→v12→v13→v14)", version)
	}

	// The whole v3…v8 surface survives the chain (additive migrations never
	// drop) plus the new v9 retention-policy layer.
	for _, table := range append(append(append(append(append(v3Tables(), v4Tables()...), v5Tables()...), v6Tables()...), []string{"observations", "evidence_objects"}...), v9Tables()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("fresh store missing table %q: %v", table, err)
		}
	}
	for _, idx := range append(append(append(append(v4Indexes(), v5Indexes()...), v6Indexes()...), v8ObjectIndexes()...), v9Indexes()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("fresh store missing index %q: %v", idx, err)
		}
	}
	for _, trg := range append(append(append(v6Triggers(), []string{"observations_immutable_content", "evidence_objects_no_update", "evidence_objects_no_delete"}...), v8ObjectTriggers()...), v9Triggers()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trg).Scan(&name); err != nil {
			t.Fatalf("fresh store missing trigger %q: %v", trg, err)
		}
	}

	// The receipts table is the v9 layout: the extended action CHECK is LIVE —
	// a retention_bound receipt with its typed evidence_object FK inserts, and
	// a legacy v0.4–v0.7 action still inserts (layout verbatim).
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
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-v9", core.ReceiptAlgorithm, "base64-public-v9", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}
	// The receipt for a newly bound policy (retention_bound, §5) must be
	// expressible in the v9 schema — this is the frozen contract's receipt
	// capability this batch prepares; emission lands with object binding.
	retentionBoundRow := ReceiptRow{
		SubjectType:         core.SubjectTypeEvidenceObject,
		SubjectID:           strings.Repeat("a", 64),
		Action:              core.ReceiptActionRetentionBound,
		TenantID:            testOrgID,
		CompanyID:           "acme",
		FiscalPeriodID:      testPeriod,
		PayloadHash:         "payload-hash-bound",
		PreviousReceiptHash: "",
		PrincipalID:         "subject-1",
		MembershipID:        "membership-1",
		PolicyVersion:       "evidence-lifecycle-policy/v0.8.0",
		Algorithm:           core.ReceiptAlgorithm,
		KeyID:               "ed25519:key-v9",
		Signature:           []byte{9, 9, 9, 9},
		IssuedAt:            testT,
		PayloadJSON:         `{"version":"receipt-payload/v0.8.0"}`,
		ReceiptHash:         "receipt-hash-bound-v9",
		EvidenceObjectID:    strings.Repeat("a", 64),
	}
	if err := s.InsertReceipt(ctxForTest(), s.db, retentionBoundRow); err != nil {
		t.Fatalf("fresh store must accept a retention_bound receipt with its typed FK: %v", err)
	}
	// The six purge-transition acts are expressible too (frozen §4 step 3).
	for i, action := range []core.ReceiptAction{
		core.ReceiptActionPurgeRequested, core.ReceiptActionPurgeApproved,
		core.ReceiptActionPurgeRejected, core.ReceiptActionPurgeCancelled,
		core.ReceiptActionPurgeWithdrawn, core.ReceiptActionPurgeExecuted,
	} {
		row := retentionBoundRow
		row.Action = action
		row.ReceiptHash = "receipt-hash-purge-v9-" + string(action)
		if err := s.InsertReceipt(ctxForTest(), s.db, row); err != nil {
			t.Fatalf("fresh store must accept action %q: %v", action, err)
		}
		_ = i
	}
	// A legacy v0.7 action still inserts (the extended CHECK is additive).
	legacyRow := retentionBoundRow
	legacyRow.Action = core.ReceiptActionObjectStored
	legacyRow.ReceiptHash = "receipt-hash-legacy-v9"
	if err := s.InsertReceipt(ctxForTest(), s.db, legacyRow); err != nil {
		t.Fatalf("fresh store must still accept object_stored: %v", err)
	}
}

// TestFreshStoreAcceptsLifecycleRolesInMembershipRoles: the four v0.8 lifecycle
// roles are FIRST-CLASS members of the membership_roles closed role set on a
// fresh store (schema_version=9): ALL NINE role tokens persist through the REAL
// seed path (SeedIdentity → membership_roles, the path the authenticated
// resolver reads via LoadMembership) and any other token is rejected by the
// closed CHECK. This is the identity-persistence half of the v0.8 role contract
// (design §8.1).
func TestFreshStoreAcceptsLifecycleRolesInMembershipRoles(t *testing.T) {
	s := newTestStore(t)
	if err := s.SeedIdentity(IdentitySeed{
		TenantID: testOrgID, CompanyID: "acme", CompanyRUC: testRucA, CompanyName: "ACME SA",
		MembershipID: "membership-lifecycle", SubjectID: "subject-1",
		Roles: []auth.AccountingRole{
			auth.RoleAccountant, auth.RoleSeniorAccountant, auth.RoleController,
			auth.RoleTaxReviewer, auth.RoleAuthorizedTaxProfessional,
			auth.RoleRecordsComplianceOfficer, auth.RoleTenantRecordsOwner,
			auth.RoleTaxResponsible, auth.RoleOperationalAccountant,
		},
	}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}

	var got []string
	rows, err := s.db.Query(`SELECT role FROM membership_roles WHERE membership_id = 'membership-lifecycle' ORDER BY role`)
	if err != nil {
		t.Fatalf("query membership_roles: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			t.Fatalf("scan membership role: %v", err)
		}
		got = append(got, role)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate membership roles: %v", err)
	}
	if len(got) != 9 {
		t.Fatalf("membership_roles persisted %d roles, want 9: %v", len(got), got)
	}

	// An unknown role token is still rejected by the closed CHECK (never widened
	// into an open set — the deny-list contract depends on the closed role set).
	// The REAL seed path surfaces the CHECK failure, proving the rejection is
	// enforceable at the persistence boundary.
	err = s.SeedIdentity(IdentitySeed{
		TenantID:     testOrgID,
		CompanyID:    "acme-rogue",
		CompanyRUC:   testRucB,
		CompanyName:  "Rogue SA",
		MembershipID: "membership-rogue",
		SubjectID:    "subject-rogue",
		Roles:        []auth.AccountingRole{"rogue_role"},
	})
	if err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("unknown role must be rejected by the CHECK, got %v", err)
	}
}

func TestV8StoreMigratesToV9AdditivelyPreservingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-v8.db")
	db := openV8Schema(t, path)

	// Seed ONE v8-shape evidence-object receipt row so the receipts_v9 rebuild
	// has a byte-preserved copy to prove (the v7→v8 migration already proved the
	// copy pattern; here the evidence_object_id column must copy VERBATIM).
	if _, err := db.Exec(`
		INSERT INTO evidence_objects (
			id, sha256, size, content_type, tenant_id, company_id, ruc, period,
			source_system, source_reference, source_actor_id, source_actor_kind,
			stored_by, stored_at, rel_path
		) VALUES ('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 4,
			'application/xml', ?, 'acme', ?, '202401', 'go-test', '', 'agent-1', 'agent', 'agent-1', ?, 'objects/bb/bb/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')`,
		testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("seed v8 evidence_objects row: %v", err)
	}
	// Seed a v8-shape identity: the v9 migration also REBUILDS membership_roles
	// (SQLite cannot alter a CHECK), so the seeded ladder-only role rows must
	// survive the rebuild byte-for-byte and the four v0.8 lifecycle roles must
	// become insertable AFTER it (the identity-persistence half of §8.1).
	if _, err := db.Exec(`
		INSERT INTO companies (id, tenant_id, ruc, name, active, created_at)
		VALUES ('acme', ?, ?, 'ACME SA', 1, ?)`, testOrgID, testRucA, testT,
	); err != nil {
		t.Fatalf("seed v8 company: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO memberships (id, subject_id, tenant_id, company_id, status, created_at, updated_at)
		VALUES ('membership-v8', 'subject-1', ?, 'acme', 'active', ?, ?)`, testOrgID, testT, testT,
	); err != nil {
		t.Fatalf("seed v8 membership: %v", err)
	}
	for _, seededRole := range []struct {
		role      string
		createdAt string
	}{
		{"controller", "2025-01-10T08:00:00.000Z"},
		{"tax_reviewer", "2025-02-20T09:30:00.000Z"},
	} {
		if _, err := db.Exec(`
			INSERT INTO membership_roles (membership_id, role, created_at) VALUES ('membership-v8', ?, ?)`,
			seededRole.role, seededRole.createdAt,
		); err != nil {
			t.Fatalf("seed v8 membership role %s: %v", seededRole.role, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO signing_keys (key_id, algorithm, public_key, created_at, revoked_at)
		VALUES ('ed25519:key-v8-seed', 'Ed25519', 'base64-public-v8-seed', ?, NULL)`, testT,
	); err != nil {
		t.Fatalf("seed v8 signing key: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO receipts (subject_type, subject_id, action, tenant_id, company_id,
			fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
			policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
			evidence_object_id)
		VALUES ('evidence_object', ?, 'object_stored', ?, 'acme', '202401',
			'payload-hash-seed', '', 'agent-1', '', 'kernel/v0.7.0', 'Ed25519', 'ed25519:key-v8-seed',
			X'01020304', ?, '{"version":"receipt-payload/v0.7.0"}', 'receipt-hash-seed-v8', ?)`,
		strings.Repeat("b", 64), testOrgID, testT, strings.Repeat("b", 64),
	); err != nil {
		t.Fatalf("seed v8 receipt row: %v", err)
	}

	// The migration must not run while the v9 tables already exist (fail closed
	// on a corruption signal — additive migrations never replay). Pre-create
	// retention_policies on a COPY, not the migrated store.
	preExisting := filepath.Join(t.TempDir(), "engram-v8-corrupt.db")
	db2 := openV8Schema(t, preExisting)
	if _, err := db2.Exec(`CREATE TABLE retention_policies (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("pre-create retention_policies: %v", err)
	}
	if err := migrateV8ToV9(db2); err == nil || !strings.Contains(err.Error(), "pre-existing table") {
		t.Fatalf("migrateV8ToV9 on a store with retention_policies = %v, want a fail-closed pre-existing-table abort", err)
	}

	// The genuine v8 store migrates in one transaction.
	if err := migrateV8ToV9(db); err != nil {
		t.Fatalf("migrate v8→v9: %v", err)
	}
	var version string
	if err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "9" {
		t.Fatalf("schema_version = %q, want 10", version)
	}

	// Every v8 receipt row survived the receipts_v9 rebuild byte-preserved
	// (including the evidence_object typed FK).
	var storedHash, storedAction, storedPayload string
	if err := db.QueryRow(`SELECT receipt_hash, action, payload_json FROM receipts WHERE receipt_hash = 'receipt-hash-seed-v8'`).
		Scan(&storedHash, &storedAction, &storedPayload); err != nil {
		t.Fatalf("seeded receipt lost in the rebuild: %v", err)
	}
	if storedHash != "receipt-hash-seed-v8" || storedAction != "object_stored" || storedPayload != `{"version":"receipt-payload/v0.7.0"}` {
		t.Fatalf("rebuild altered the seeded receipt: (%q, %q, %q)", storedHash, storedAction, storedPayload)
	}
	var fk any
	if err := db.QueryRow(`SELECT evidence_object_id FROM receipts WHERE receipt_hash = 'receipt-hash-seed-v8'`).Scan(&fk); err != nil {
		t.Fatalf("read evidence_object_id after rebuild: %v", err)
	}
	if id, ok := fk.(string); !ok || id != strings.Repeat("b", 64) {
		t.Fatalf("evidence_object_id = %v, want the seeded object id verbatim", fk)
	}

	// Every v8 membership_roles row survived the membership_roles_v9 rebuild
	// byte-for-byte (membership_id, role, created_at VERBATIM).
	var persistedRoles []string
	roleRows, err := db.Query(`SELECT role FROM membership_roles WHERE membership_id = 'membership-v8' ORDER BY role`)
	if err != nil {
		t.Fatalf("query migrated membership_roles: %v", err)
	}
	for roleRows.Next() {
		var role string
		if err := roleRows.Scan(&role); err != nil {
			_ = roleRows.Close()
			t.Fatalf("scan migrated membership role: %v", err)
		}
		persistedRoles = append(persistedRoles, role)
	}
	if err := roleRows.Err(); err != nil {
		_ = roleRows.Close()
		t.Fatalf("iterate migrated membership roles: %v", err)
	}
	if err := roleRows.Close(); err != nil {
		t.Fatalf("close migrated membership roles: %v", err)
	}
	if len(persistedRoles) != 2 || persistedRoles[0] != "controller" || persistedRoles[1] != "tax_reviewer" {
		t.Fatalf("migrated membership_roles = %v, want the seeded [controller tax_reviewer] verbatim", persistedRoles)
	}
	var controllerCreatedAt string
	if err := db.QueryRow(`SELECT created_at FROM membership_roles WHERE membership_id = 'membership-v8' AND role = 'controller'`).Scan(&controllerCreatedAt); err != nil {
		t.Fatalf("read migrated role created_at: %v", err)
	}
	if controllerCreatedAt != "2025-01-10T08:00:00.000Z" {
		t.Fatalf("migrated role created_at = %q, want the seeded bytes verbatim", controllerCreatedAt)
	}

	// The v9 membership_roles CHECK is LIVE after the migration: the four v0.8
	// lifecycle roles insert, and an unknown role is still rejected.
	for _, role := range []string{
		"records_compliance_officer", "tenant_records_owner", "tax_responsible", "operational_accountant",
	} {
		if _, err := db.Exec(`
			INSERT INTO membership_roles (membership_id, role, created_at) VALUES ('membership-v8', ?, ?)`,
			role, testT,
		); err != nil {
			t.Fatalf("migrated store must accept role %q: %v", role, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO membership_roles (membership_id, role, created_at) VALUES ('membership-v8', 'rogue_role', ?)`,
		testT,
	); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("migrated store must reject an unknown role, got %v", err)
	}

	// The v9 retention tables + guards are live after the migration.
	for _, table := range v9Tables() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("migrated store missing table %q: %v", table, err)
		}
	}
	for _, trg := range v9Triggers() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trg).Scan(&name); err != nil {
			t.Fatalf("migrated store missing trigger %q: %v", trg, err)
		}
	}
}

func TestRetentionPoliciesImmutableTriggers(t *testing.T) {
	s := newTestStore(t)
	// An UPDATE and a DELETE on the immutable retention_policies row must fail
	// closed (frozen pattern — supersede with a new version, never edit).
	if _, err := s.db.Exec(`
		INSERT INTO retention_policies (id, tenant_id, jurisdiction, legislation, authority, source,
			category, min_period, version, dual_approval_required, dual_approver_roles,
			blocking_hold_kinds, enabled, created_at, created_by)
		VALUES ('00000000-0000-4000-8000-000000000001', 'org-001', 'PE', 'NATIONAL-TAX', 'tenant-records',
			'deployment decision', 'invoice', '202401', 1, 1, '["controller","tax_responsible"]',
			'["audit","dispute","fiscalization","legal"]', 1, '2026-08-07T12:00:00.000Z', 'subject-1')`,
	); err != nil {
		t.Fatalf("insert retention policy: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE retention_policies SET enabled = 0 WHERE id = '00000000-0000-4000-8000-000000000001'`); err == nil {
		t.Fatal("UPDATE on retention_policies must fail closed (IMMUTABLE_RETENTION_POLICY)")
	}
	if _, err := s.db.Exec(`DELETE FROM retention_policies WHERE id = '00000000-0000-4000-8000-000000000001'`); err == nil {
		t.Fatal("DELETE on retention_policies must fail closed (IMMUTABLE_RETENTION_POLICY)")
	}
}
