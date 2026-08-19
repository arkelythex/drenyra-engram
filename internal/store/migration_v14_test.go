// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module verifies the v13→v14 additive
// migration (v0.6.0 Batch 2 structured rule links — docs/architecture/
// fiscal-policy-memory-v0.6.md §2.2/§3):
//
//   - fresh stores bootstrap to schema_version=14 with the two structured-link
//     columns on rule_links (version, effective_at — NULL for legacy rows) and
//     the reverse lookup index idx_rule_links_ref;
//   - a genuine v13 store (with a seeded legacy rule_links row) migrates in ONE
//     fail-closed transaction: every row survives byte-preserved (the legacy
//     row keeps version NULL — no backfill, no re-hash), the new columns and
//     index land, schema_version flips to 14 only after the whole migration
//     succeeded, and the migrated layout accepts structured inserts;
//   - a pre-existing column or index aborts the migration (additive migrations
//     never replay) and the store stays schema_version=13 with NO partial
//     mutation.
package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// openV13Schema opens a raw SQLite handle and bootstraps the EXACT v13 layout
// (applySchema + the v2→v3 … v12→v13 migrations), so tests can exercise the
// v13→v14 migration on a genuine v13 store.
func openV13Schema(t *testing.T, path string) *sql.DB {
	t.Helper()
	db := openV11Schema(t, path)
	if err := migrateV11ToV12(db); err != nil {
		_ = db.Close()
		t.Fatalf("bootstrap v12 layout: %v", err)
	}
	if err := migrateV12ToV13(db); err != nil {
		_ = db.Close()
		t.Fatalf("bootstrap v13 layout: %v", err)
	}
	return db
}

// ruleLinkColumns returns the column-name set of rule_links via PRAGMA
// table_info (works on a raw handle without a transaction).
func ruleLinkColumns(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(rule_links)`)
	if err != nil {
		t.Fatalf("read rule_links columns: %v", err)
	}
	defer func() { _ = rows.Close() }()
	columns := make(map[string]bool)
	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan rule_links columns: %v", err)
		}
		columns[name] = true
	}
	return columns
}

// seedV13RuleLinksRow inserts one observations row (with all NOT NULL columns)
// plus one LEGACY unversioned rule_links row (the migration must preserve it
// byte-preserved with version NULL).
func seedV13RuleLinksRow(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO observations (
			id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
			what, why, where_text, learned, authority_status, status, fiscal_effect, effective_at, recorded_at, observed_at,
			expires_at, validity_effective_at, validity_source, actor, timestamp, source, session, source_json,
			content_hash, identity_hash, envelope_hash, evidence_refs_json, rule_refs_json,
			confidence, materiality, materiality_level, close_snapshot_json, policy_rule_json,
			receipt_id, supersedes_id, revision
		) VALUES (
			'seed-memory-v13', 'policy/seed/topic', 'seed rule', 'rule', 'rule', 'company', ?, 'acme', '20123456789', '202401',
			'what', 'why', 'where', 'learned', 'reviewed', 'active', 'none', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '',
			'', '', '', 'seed-actor', '2026-01-01T00:00:00Z', 'go-test', '', '', 'seed-hash', '', '', '[]', '[]',
			NULL, NULL, NULL, NULL, NULL, '', '', 1
		)`, testOrgID); err != nil {
		t.Fatalf("seed v13 observation: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO rule_links (memory_id, ref, actor, timestamp)
		VALUES ('seed-memory-v13', 'policy/seed/ref', 'seed-actor', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed v13 legacy rule link: %v", err)
	}
}

// TestFreshStoreBootstrapsV14StructuredRuleLinks verifies that a fresh store
// ends at schema_version=14 (the chain v2→…→v13→v14) with the structured-link
// columns and the reverse lookup index, and that legacy bare links still work.
func TestFreshStoreBootstrapsV14StructuredRuleLinks(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 16 {
		t.Fatalf("schema_version = %d, want 16 (the chain continues v2→…→v12→v13→v14)", version)
	}

	// The two structured-link columns exist on a fresh store.
	columns := ruleLinkColumns(t, s.db)
	for _, column := range []string{"version", "effective_at"} {
		if !columns[column] {
			t.Fatalf("fresh store missing rule_links.%s", column)
		}
	}
	// The reverse lookup index exists.
	var name string
	if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_rule_links_ref'`).Scan(&name); err != nil {
		t.Fatalf("fresh store missing idx_rule_links_ref: %v", err)
	}

	// A legacy bare link still inserts (version/effective_at NULL) and a legacy
	// row can never be upgraded in place (RULE_LINK_VERSION_CONFLICT).
	rule, err := s.Save(ruleInput(t, "policy/fresh/rule"))
	if err != nil {
		t.Fatalf("save rule: %v", err)
	}
	if err := s.AddRuleLink(rule.Memory.Identity.ID, "policy/fresh/rule", "cli"); err != nil {
		t.Fatalf("bare rule link: %v", err)
	}
	// A legacy row can never be upgraded in place (RULE_LINK_VERSION_CONFLICT
	// fires AFTER the target validates: the legacy ref is the rule's own
	// topicKey).
	if err := s.AddRuleLinkVersion(rule.Memory.Identity.ID, core.RuleLink{
		Ref: "policy/fresh/rule", Version: rule.Memory.Identity.ID, EffectiveAt: rule.Memory.EffectiveAt,
	}, "cli"); err == nil || !strings.Contains(err.Error(), "RULE_LINK_VERSION_CONFLICT") {
		t.Fatalf("upgrading a legacy row in place must fail RULE_LINK_VERSION_CONFLICT, got %v", err)
	}
}

// TestV13StoreMigratesToV14AdditivelyPreservingRows drives a genuine v13 store
// (with a seeded legacy rule_links row) through migrateV13ToV14: every seeded
// row survives byte-preserved (version stays NULL), schema_version flips to 14
// only after the whole migration succeeded, and the migrated layout accepts
// structured inserts.
func TestV13StoreMigratesToV14AdditivelyPreservingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-v13.db")
	db := openV13Schema(t, path)
	seedV13RuleLinksRow(t, db)

	if err := migrateV13ToV14(db); err != nil {
		t.Fatalf("migrate v13→v14: %v", err)
	}
	var version string
	if err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "14" {
		t.Fatalf("schema_version = %q, want 14", version)
	}

	// The legacy row survived byte-preserved: still there, still unversioned.
	var ref, ver, effAt string
	if err := db.QueryRow(`SELECT ref, COALESCE(version, ''), COALESCE(effective_at, '') FROM rule_links WHERE memory_id = 'seed-memory-v13'`).Scan(&ref, &ver, &effAt); err != nil {
		t.Fatalf("legacy row lost: %v", err)
	}
	if ref != "policy/seed/ref" || ver != "" || effAt != "" {
		t.Fatalf("legacy row = (%q, %q, %q), want (policy/seed/ref, unversioned, unversioned) — no backfill, no re-hash", ref, ver, effAt)
	}

	// The migrated layout accepts a structured insert.
	if _, err := db.Exec(`
		INSERT INTO rule_links (memory_id, ref, version, effective_at, actor, timestamp)
		SELECT 'seed-memory-v13', 'policy/seed/structured', 'seed-memory-v13', '2026-07-31T12:00:00Z', 'seed-actor', '2026-07-31T12:00:00Z'`); err != nil {
		t.Fatalf("structured insert on migrated layout: %v", err)
	}

	// The full chain reaches v14 via Open on the migrated file.
	opened, err := Open(path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	_ = opened.Close()
}

// TestV13StoreMigrationFailsClosedOnPreExistingColumn verifies the fail-closed
// contract: a v13 store whose rule_links already carries a version column
// (foreign or partial migration) aborts the whole migration and stays
// schema_version=13 with NO partial mutation (the second column and the index
// never land).
func TestV13StoreMigrationFailsClosedOnPreExistingColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-v13-column.db")
	db := openV13Schema(t, path)
	if _, err := db.Exec(`ALTER TABLE rule_links ADD COLUMN version TEXT NULL`); err != nil {
		t.Fatalf("simulate partial migration: %v", err)
	}

	err := migrateV13ToV14(db)
	if err == nil || !strings.Contains(err.Error(), "rule_links.version already exists") {
		t.Fatalf("migrateV13ToV14 on a store with rule_links.version = %v, want a fail-closed pre-existing-column abort", err)
	}
	var version string
	if err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "13" {
		t.Fatalf("schema_version = %q, want 13 (the migration rolled back)", version)
	}
	// The other column and the index never landed (rollback).
	columns := ruleLinkColumns(t, db)
	if columns["effective_at"] {
		t.Fatal("rule_links.effective_at must NOT exist after the aborted migration (rollback)")
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_rule_links_ref'`).Scan(&name); err != sql.ErrNoRows {
		t.Fatal("idx_rule_links_ref must NOT exist after the aborted migration (rollback)")
	}
}

// TestV13StoreMigrationFailsClosedOnPreExistingIndex mirrors the column test
// for the index: a pre-existing idx_rule_links_ref aborts the migration.
func TestV13StoreMigrationFailsClosedOnPreExistingIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-v13-index.db")
	db := openV13Schema(t, path)
	if _, err := db.Exec(`CREATE INDEX idx_rule_links_ref ON rule_links (ref, memory_id)`); err != nil {
		t.Fatalf("simulate pre-existing index: %v", err)
	}

	err := migrateV13ToV14(db)
	if err == nil || !strings.Contains(err.Error(), "idx_rule_links_ref") {
		t.Fatalf("migrateV13ToV14 on a store with idx_rule_links_ref = %v, want a fail-closed pre-existing-index abort", err)
	}
	var version string
	if err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "13" {
		t.Fatalf("schema_version = %q, want 13 (the migration rolled back)", version)
	}
	columns := ruleLinkColumns(t, db)
	if columns["version"] || columns["effective_at"] {
		t.Fatal("no column may land after the aborted index migration (rollback)")
	}
}
