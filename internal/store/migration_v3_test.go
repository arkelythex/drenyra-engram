// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module verifies the v2→v3 additive
// migration step (v0.4.0 Step 1) inside the current additive chain: fresh
// stores bootstrap to v2 and then run the SAME v2→v3 migration as existing
// stores (the chain continues v3→v4→v5 — full-layout assertions live in
// migration_v4_test.go), existing v2 data survives untouched, a failing
// migration rolls back leaving schema_version=2, and the two
// approval_events immutability triggers reject UPDATE and DELETE.

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// openV2Schema opens a raw SQLite handle and bootstraps the EXACT v2 layout
// (applySchema + the Open pragmas), so tests can exercise the v2→v3 migration
// on a genuine v2 store.
func openV2Schema(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw v2 database: %v", err)
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
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// saveV2Row inserts a row with the EXACT v2 column list (no materiality_level),
// the way a genuine v2 store would have written it. Production never writes to a
// v2 store (Open migrates first); this helper exists so the migration test can
// seed legacy-shaped rows the same way the v1→v2 test seeds v1 rows.
func saveV2Row(t *testing.T, db *sql.DB, input core.SaveInput) core.AccountingMemory {
	t.Helper()
	id, err := newUUID()
	if err != nil {
		t.Fatalf("generate id: %v", err)
	}
	recordedAt := nowISO()
	if input.EffectiveAt == "" {
		input.EffectiveAt = recordedAt
	}
	status := core.InitialStatus(input.FiscalEffect)
	memory := buildMemory(input, id, 1, status, recordedAt)
	memory.IdentityHash = core.ComputeIdentityHash(memory)
	memory.EnvelopeHash = core.ComputeEnvelopeHash(memory)
	_, err = db.Exec(`
		INSERT INTO observations (
			id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
			what, why, where_text, learned, authority_status, status, fiscal_effect, effective_at, recorded_at, observed_at,
			expires_at, validity_effective_at, validity_source, actor, timestamp, source, session, source_json, content_hash, identity_hash, envelope_hash, evidence_refs_json, rule_refs_json,
			confidence, materiality, receipt_id, supersedes_id, revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.TopicKey, memory.Title, string(memory.Kind), string(memory.Kind), string(memory.Scope.Kind),
		memory.Scope.OrganizationID, memory.Scope.CompanyID, memory.Scope.RUC, memory.Scope.Period,
		memory.Content.What, memory.Content.Why, memory.Content.Where, memory.Content.Learned,
		legacyStatusFor(memory.Status), string(memory.Status), string(memory.FiscalEffect),
		memory.EffectiveAt, memory.RecordedAt, memory.ObservedAt,
		validityExpiresAt(memory.Validity), validityEffectiveAt(memory.Validity), validitySource(memory.Validity),
		memory.Source.ActorID, memory.RecordedAt, memory.Source.System, memory.Source.Session,
		encodeSource(memory.Source), memory.ContentHash, memory.IdentityHash, memory.EnvelopeHash, encodeRefs(memory.EvidenceRefs), encodeRefs(memory.RuleRefs),
		nullableFloat(memory.Confidence), nullableInt(memory.Materiality), memory.ReceiptID, memory.SupersedesID,
		memory.Revision,
	)
	if err != nil {
		t.Fatalf("insert v2 row: %v", err)
	}
	return memory
}

func v3Tables() []string {
	return []string{
		"companies", "memberships", "membership_roles",
		"sessions", "approval_events", "idempotency_keys",
	}
}

func v3Indexes() []string {
	return []string{"idx_memberships_subject", "idx_sessions_membership", "idx_approval_events_memory"}
}

func v3Triggers() []string {
	return []string{"approval_events_no_update", "approval_events_no_delete"}
}

func TestFreshStoreBootstrapsToSchemaV5(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 9 {
		t.Fatalf("schema_version = %d, want 9 (the chain continues v3→v4→v5→v6→v7→v8→v9)", version)
	}

	for _, table := range v3Tables() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("fresh store missing v3 table %q: %v", table, err)
		}
	}
	for _, idx := range v3Indexes() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("fresh store missing v3 index %q: %v", idx, err)
		}
	}
	for _, trigger := range v3Triggers() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&name); err != nil {
			t.Fatalf("fresh store missing v3 trigger %q: %v", trigger, err)
		}
	}

	// materiality_level column exists on observations.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	columns, err := tableColumns(context.Background(), tx, "observations")
	_ = tx.Commit()
	if err != nil {
		t.Fatalf("read observations columns: %v", err)
	}
	if !columns["materiality_level"] {
		t.Fatal("observations.materiality_level is missing on a fresh v3 store")
	}
}

func TestV2StoreMigratesToV5AdditivelyPreservingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	db := openV2Schema(t, path)

	active := saveV2Row(t, db, validInput("tax.igv.rate", "first version"))
	gatedInput := validInput("tax.igv.gated", "needs approval")
	gatedInput.FiscalEffect = core.FiscalEffectClosing
	gatedInput.EffectiveAt = testT
	pending := saveV2Row(t, db, gatedInput)
	_ = db.Close() // release the file before Open migrates it

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open migrates v2→v3: %v", err)
	}
	defer func() { _ = s.Close() }()

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version after migration: %v", err)
	}
	if version != 9 {
		t.Fatalf("schema_version after migration = %d, want 9 (the chain continues v3→v4→v5→v6→v7→v8→v9)", version)
	}

	// Rows survive additively with EXACTLY the envelope bytes written at v2.
	gotActive, ok := s.FindByID(active.Identity.ID)
	if !ok {
		t.Fatal("active row lost by migration")
	}
	if gotActive.EnvelopeHash != active.EnvelopeHash {
		t.Fatalf("active envelope changed by migration: got %s want %s", gotActive.EnvelopeHash, active.EnvelopeHash)
	}
	if gotActive.Status != core.StatusActive {
		t.Fatalf("active status = %s, want active", gotActive.Status)
	}
	if gotActive.Title != "IGV base rate" {
		t.Fatalf("active title = %q, want IGV base rate", gotActive.Title)
	}
	gotPending, ok := s.FindByID(pending.Identity.ID)
	if !ok {
		t.Fatal("pending_review row lost by migration")
	}
	if gotPending.EnvelopeHash != pending.EnvelopeHash {
		t.Fatalf("pending envelope changed by migration: got %s want %s", gotPending.EnvelopeHash, pending.EnvelopeHash)
	}
	if gotPending.Status != core.StatusPendingReview {
		t.Fatalf("pending status = %s, want pending_review", gotPending.Status)
	}

	// The new column exists and is NULL for migrated rows (NULL = normal).
	var level sql.NullString
	if err := s.db.QueryRow(`SELECT materiality_level FROM observations WHERE id = ?`, active.Identity.ID).Scan(&level); err != nil {
		t.Fatalf("read materiality_level: %v", err)
	}
	if level.Valid {
		t.Fatalf("migrated row carries a materiality_level %q, want NULL", level.String)
	}
}

func TestV2ToV3MigrationRollsBackLeavingSchemaV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2-fail.db")
	db := openV2Schema(t, path)
	// A conflicting pre-existing table makes the verbatim CREATE TABLE companies
	// fail — the corrupt-step probe for the single-transaction rollback.
	if _, err := db.Exec(`CREATE TABLE companies (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("pre-create conflicting table: %v", err)
	}
	_ = db.Close()

	_, err := Open(path)
	if err == nil {
		t.Fatal("Open must fail closed when the migration fails")
	}
	if !strings.Contains(err.Error(), "migrate v2→v3") {
		t.Fatalf("error %q does not identify the migration step", err)
	}

	// Re-open raw: schema_version must still be 2 and NO v3 object may survive.
	db = openV2Schema(t, path)
	version, err := readSchemaVersion(db)
	if err != nil {
		t.Fatalf("read schema version after failed migration: %v", err)
	}
	if version != 2 {
		t.Fatalf("schema_version = %d after failed migration, want 2 (rolled back)", version)
	}
	for _, table := range []string{"memberships", "membership_roles", "sessions", "approval_events", "idempotency_keys"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err == nil {
			t.Fatalf("table %q survived the migration rollback", table)
		}
	}
	var found int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('observations') WHERE name = 'materiality_level'`).Scan(&found); err != nil {
		t.Fatalf("read observations columns: %v", err)
	}
	if found != 0 {
		t.Fatal("observations.materiality_level survived the migration rollback")
	}
}

func TestV3PersistsDeclaredMaterialityLevel(t *testing.T) {
	s := newTestStore(t)

	input := validInput("tax.igv.critical", "critical adjustment")
	input.FiscalEffect = core.FiscalEffectAdjustment
	input.EffectiveAt = testT
	level := core.MaterialityCritical
	input.MaterialityLevel = &level
	result, err := s.Save(input)
	if err != nil {
		t.Fatalf("save with materiality level: %v", err)
	}

	got, ok := s.FindByID(result.Memory.Identity.ID)
	if !ok {
		t.Fatal("memory not found")
	}
	if got.MaterialityLevel == nil || *got.MaterialityLevel != core.MaterialityCritical {
		t.Fatalf("MaterialityLevel = %v, want critical", got.MaterialityLevel)
	}

	// The declared classification is immutable content (v3 guard): an UPDATE
	// touching it aborts.
	if _, err := s.db.Exec(`UPDATE observations SET materiality_level = 'normal' WHERE id = ?`, result.Memory.Identity.ID); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_OBSERVATION") {
		t.Fatalf("UPDATE of materiality_level must abort, got %v", err)
	}

	// NULL (unset) stays NULL — the policy treats it as normal.
	plain, err := s.Save(validInput("tax.igv.plain", "no declared level"))
	if err != nil {
		t.Fatalf("save without level: %v", err)
	}
	gotPlain, ok := s.FindByID(plain.Memory.Identity.ID)
	if !ok {
		t.Fatal("plain memory not found")
	}
	if gotPlain.MaterialityLevel != nil {
		t.Fatalf("unset level must stay NULL, got %v", *gotPlain.MaterialityLevel)
	}
}

func TestApprovalEventsRejectUpdateAndDelete(t *testing.T) {
	s := newTestStore(t)

	// Minimal FK chain: companies → memberships → observations → approval_events.
	now := nowISO()
	if _, err := s.db.Exec(
		`INSERT INTO companies (id, tenant_id, ruc, name, active, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		"co-1", testOrgID, testRucA, "ACME", now,
	); err != nil {
		t.Fatalf("seed company: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO memberships (id, subject_id, tenant_id, company_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, 'active', ?, ?)`,
		"mem-1", "subj-1", testOrgID, "co-1", now, now,
	); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	saved, err := s.Save(validInput("tax.igv.approved", "needs approval"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO approval_events (
			id, request_id, memory_id, tenant_id, company_id, fiscal_period_id,
			action, from_status, to_status, reviewed_envelope_hash, resulting_envelope_hash,
			reason, principal_subject_id, membership_id, principal_roles_json,
			authentication_method, assurance_level, principal_authenticated_at,
			policy_version, authorization_reason_code, created_at
		) VALUES (?, ?, ?, ?, ?, NULL, 'approved', 'pending_review', 'approved', ?, ?, 'seed reason', 'subj-1', 'mem-1', '[]', 'session', 'standard', ?, 'approval-policy/v0.4.0', 'AUTHORIZED', ?)`,
		"evt-1", "req-1", saved.Memory.Identity.ID, testOrgID, "acme", "h1", "h2", now, now,
	); err != nil {
		t.Fatalf("seed approval event: %v", err)
	}

	if _, err := s.db.Exec(`UPDATE approval_events SET reason = 'mutated' WHERE id = 'evt-1'`); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_APPROVAL_EVENT") {
		t.Fatalf("UPDATE on approval_events must abort with IMMUTABLE_APPROVAL_EVENT, got %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM approval_events WHERE id = 'evt-1'`); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_APPROVAL_EVENT") {
		t.Fatalf("DELETE on approval_events must abort with IMMUTABLE_APPROVAL_EVENT, got %v", err)
	}
}
