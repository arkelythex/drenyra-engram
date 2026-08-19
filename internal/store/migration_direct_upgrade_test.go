// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module carries the direct-upgrade and
// crash-recovery evidence for the migrations audit closure
// (docs/due-diligence/2026-08-product-architecture-audit.md, block G;
// contracts/provenance.md "Migration provenance for schema v14 (frozen)").
//
// This file holds the v1…v13 direct-upgrade matrix (FR-G.1 / AC-G-1),
// the deterministic migration-interruption and reopen-convergence proof
// (FR-G.2 / AC-G-2), and the fixture-fidelity triangulation (NFR-G.1). The
// interruption tests landed first so the mid-chain rollback property was
// proven before the full fixture matrix was built.
//
// Fixture fidelity (NFR-G.1): every builder derives from the frozen
// migration-era schemas/tests — v1SchemaDDL for v1, openV2Schema…openV13Schema
// for v2/3/4/5/7/8/9/10/11/13, and the REAL production migrations for the two
// versions without a dedicated builder (v6 = openV5Schema + migrateV5ToV6;
// v12 = openV11Schema + migrateV11ToV12). Nothing is cloned. Assertions
// verify structural invariants and data preservation (exact scalar/JSON/blob
// bytes + per-table row counts + the v14 invariant manifest), never version
// numbers alone (G.7 triangulates this).
//
// Interruption seam — deviation from design D-5, documented:
//
//	The design proposed creating a deliberately wrong pre-existing
//	`idx_rule_links_ref` so migrateV13ToV14 would run its earlier transactional
//	ALTER work and then fail at index creation. The real migration fail-closes
//	on ANY pre-existing index at step (a) BEFORE executing any ALTER
//	(migration_v14.go: the corruption-signal abort), so that injection can never
//	execute partial DDL — it only re-proves the already-tested fail-closed abort
//	(TestV13StoreMigrationFailsClosedOnPreExistingIndex). Per NFR-G.2 the
//	strongest achievable proof is used instead, with the gap documented rather
//	than papered over: a test-only BEFORE UPDATE trigger on schema_meta raises
//	ABORT when the migration's LAST statement (schema_version = 14 advance)
//	runs. By that point migrateV13ToV14 has already executed both ALTERs and the
//	CREATE INDEX inside its single transaction, so the failed advance must roll
//	the WHOLE step back to a coherent v13.
//
// Exactly what these tests prove (NFR-G.2): SQLite transaction rollback of a
// mid-chain failure at the schema_version advance, under the repository's
// one-transaction-per-step model (store.go: the v13→v14 migration runs in its
// own single transaction; schema_version changes only after the step succeeds),
// and deterministic re-run convergence through the normal Open path. They do
// NOT claim to emulate an OS process kill at every instruction, and no
// production migration hook, ledger, or test seam is added.
package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// schemaVersionAdvanceBlockerTrigger is the test-only failure-injection seam:
// it aborts exactly the UPDATE that advances schema_meta to '14', i.e. the
// last statement of migrateV13ToV14 after all of that step's DDL already ran
// inside the same transaction.
const schemaVersionAdvanceBlockerTrigger = `
CREATE TRIGGER sdd_test_block_schema_version_advance
BEFORE UPDATE OF value ON schema_meta
WHEN NEW.key = 'schema_version' AND NEW.value = '14'
BEGIN
    SELECT RAISE(ABORT, 'sdd-injected: schema_version advance blocked');
END
`

// injectSchemaVersionAdvanceBlocker installs the interruption trigger on a raw
// handle BEFORE the normal Open path runs the v13→v14 migration.
func injectSchemaVersionAdvanceBlocker(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(schemaVersionAdvanceBlockerTrigger); err != nil {
		t.Fatalf("inject schema_version advance blocker: %v", err)
	}
}

// dropSchemaVersionAdvanceBlocker removes the interruption trigger from a raw
// handle so a re-open can run the chain to completion.
func dropSchemaVersionAdvanceBlocker(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`DROP TRIGGER IF EXISTS sdd_test_block_schema_version_advance`); err != nil {
		t.Fatalf("drop schema_version advance blocker: %v", err)
	}
}

// rawOpen opens a bare SQLite handle for post-failure state inspection. It must
// NOT run openV13Schema (which would re-bootstrap the layout); it only opens
// the file to read whatever the interrupted Open left behind.
func rawOpen(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw handle on %s: %v", path, err)
	}
	return db
}

// assertNoRuleLinksV14Artifacts asserts the mid-chain failure rolled back ALL
// of the v13→v14 step: schema_version still 13, neither structured-link column,
// no idx_rule_links_ref index, and no staging/partial table (receipts_v*).
func assertNoRuleLinksV14Artifacts(t *testing.T, db *sql.DB) {
	t.Helper()
	version, err := readSchemaVersion(db)
	if err != nil {
		t.Fatalf("read schema_version after interrupted migration: %v", err)
	}
	if version != 13 {
		t.Fatalf("schema_version = %d after interrupted migration, want 13 (the v13→v14 step rolled back)", version)
	}
	columns := ruleLinkColumns(t, db)
	if columns["version"] || columns["effective_at"] {
		t.Fatal("rule_links.version/effective_at must NOT exist after the interrupted migration — earlier ALTER statements rolled back")
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_rule_links_ref'`).Scan(&name); err != sql.ErrNoRows {
		t.Fatal("idx_rule_links_ref must NOT exist after the interrupted migration — the CREATE INDEX rolled back")
	}
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'receipts_v%'`)
	if err != nil {
		t.Fatalf("scan for staging tables: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatal("staging/partial table (receipts_v*) must NOT exist after the interrupted migration")
	}
}

// assertSeededV13RowsIntact verifies the seeded observation and legacy
// unversioned rule link survived byte-preserved with exactly one copy each —
// no duplicate effects, no partial artifacts. It never references the
// version/effective_at columns, so it is valid against BOTH the rolled-back
// v13 state and the migrated v14 state.
func assertSeededV13RowsIntact(t *testing.T, db *sql.DB) {
	t.Helper()
	var (
		id, topicKey, orgID, status string
		count                       int
	)
	if err := db.QueryRow(`SELECT id, topic_key, organization_id, status FROM observations WHERE id = 'seed-memory-v13'`).Scan(&id, &topicKey, &orgID, &status); err != nil {
		t.Fatalf("seeded observation lost after interrupted migration: %v", err)
	}
	if id != "seed-memory-v13" || topicKey != "policy/seed/topic" || orgID != testOrgID || status != "active" {
		t.Fatalf("seeded observation corrupted: (%q, %q, %q, %q)", id, topicKey, orgID, status)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM observations WHERE id = 'seed-memory-v13'`).Scan(&count); err != nil {
		t.Fatalf("count seeded observations: %v", err)
	}
	if count != 1 {
		t.Fatalf("seeded observation count = %d, want 1 (no duplicates)", count)
	}
	var ref, actor string
	if err := db.QueryRow(`SELECT ref, actor FROM rule_links WHERE memory_id = 'seed-memory-v13'`).Scan(&ref, &actor); err != nil {
		t.Fatalf("seeded legacy rule link lost: %v", err)
	}
	if ref != "policy/seed/ref" || actor != "seed-actor" {
		t.Fatalf("legacy rule link = (%q, %q), want (policy/seed/ref, seed-actor)", ref, actor)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM rule_links WHERE memory_id = 'seed-memory-v13'`).Scan(&count); err != nil {
		t.Fatalf("count seeded rule links: %v", err)
	}
	if count != 1 {
		t.Fatalf("seeded rule link count = %d, want 1 (no duplicates)", count)
	}
}

// TestMigrationInterruptedRollsBackToPriorVersion proves the deterministic
// interruption property (AC-G-2, FR-G.2): a genuine seeded v13 store whose
// v13→v14 migration fails at its LAST statement (schema_version advance, after
// both ALTERs and the CREATE INDEX executed inside the step's single
// transaction) is left as a coherent v13 — schema_version still 13, no partial
// columns, no index, no staging tables, seeded rows byte-intact and unique.
func TestMigrationInterruptedRollsBackToPriorVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-v13-interrupted.db")
	db := openV13Schema(t, path)
	seedV13RuleLinksRow(t, db)
	injectSchemaVersionAdvanceBlocker(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close raw v13 handle: %v", err)
	}

	// Normal Open enters migrateV13ToV14, executes the ALTERs + CREATE INDEX
	// inside its transaction, and fails when the schema_version advance trips
	// the injected trigger.
	opened, err := Open(path)
	if err == nil {
		_ = opened.Close()
		t.Fatal("Open on the interrupted store must fail, got nil error")
	}
	if !strings.Contains(err.Error(), "migrate v13→v14") {
		t.Fatalf("Open error = %q, want the v13→v14 migration failure", err)
	}

	// A new raw read handle proves the end state: coherent v13 with zero
	// partial artifacts.
	after := rawOpen(t, path)
	defer func() { _ = after.Close() }()
	assertNoRuleLinksV14Artifacts(t, after)
	assertSeededV13RowsIntact(t, after)
}

// TestMigrationCrashReopenConvergesToV16 proves the recovery property
// (AC-G-2, FR-G.2): after the interruption is removed, reopening the SAME
// store through the normal Open path safely re-runs the chain to v14 with the
// correct index definition, exact data preservation, and exactly one copy of
// every seeded row — no duplicate effects from the earlier partial run.
func TestMigrationCrashReopenConvergesToV16(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-v13-reopen.db")
	db := openV13Schema(t, path)
	seedV13RuleLinksRow(t, db)
	injectSchemaVersionAdvanceBlocker(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close raw v13 handle: %v", err)
	}

	// Reproduce the interruption exactly as the sibling test does.
	if opened, err := Open(path); err == nil {
		_ = opened.Close()
		t.Fatal("Open on the interrupted store must fail, got nil error")
	}

	// Remove the injection, confirm the store is still coherent v13, then let
	// the normal Open path re-run the chain from v13 to v14.
	recover := rawOpen(t, path)
	dropSchemaVersionAdvanceBlocker(t, recover)
	version, err := readSchemaVersion(recover)
	if err != nil {
		t.Fatalf("read schema_version before reopen: %v", err)
	}
	if version != 13 {
		t.Fatalf("schema_version = %d before reopen, want 13", version)
	}
	if err := recover.Close(); err != nil {
		t.Fatalf("close raw recovery handle: %v", err)
	}

	opened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after interruption: %v", err)
	}
	defer func() { _ = opened.Close() }()

	version, err = readSchemaVersion(opened.db)
	if err != nil {
		t.Fatalf("read schema_version after reopen: %v", err)
	}
	if version != 17 {
		t.Fatalf("schema_version = %d after reopen, want 17", version)
	}

	// The migrated layout: both structured-link columns and the reverse lookup
	// index with the exact frozen definition (ref, version, effective_at,
	// memory_id) — one copy of each seeded row.
	columns := ruleLinkColumns(t, opened.db)
	for _, column := range []string{"version", "effective_at"} {
		if !columns[column] {
			t.Fatalf("reopened store missing rule_links.%s", column)
		}
	}
	rows, err := opened.db.Query(`PRAGMA index_info(idx_rule_links_ref)`)
	if err != nil {
		t.Fatalf("inspect idx_rule_links_ref: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var indexColumns []string
	for rows.Next() {
		var (
			seqno, cid int
			name       string
		)
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			t.Fatalf("scan idx_rule_links_ref columns: %v", err)
		}
		indexColumns = append(indexColumns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate idx_rule_links_ref columns: %v", err)
	}
	want := []string{"ref", "version", "effective_at", "memory_id"}
	if len(indexColumns) != len(want) {
		t.Fatalf("idx_rule_links_ref columns = %v, want %v", indexColumns, want)
	}
	for i := range want {
		if indexColumns[i] != want[i] {
			t.Fatalf("idx_rule_links_ref columns = %v, want %v", indexColumns, want)
		}
	}
	assertSeededV13RowsIntact(t, opened.db)

	// On the migrated v14 layout the legacy row must still be unversioned
	// (version NULL — no backfill, no re-hash).
	var ver, effAt string
	if err := opened.db.QueryRow(`SELECT COALESCE(version, ''), COALESCE(effective_at, '') FROM rule_links WHERE memory_id = 'seed-memory-v13'`).Scan(&ver, &effAt); err != nil {
		t.Fatalf("read legacy rule link version on v14: %v", err)
	}
	if ver != "" || effAt != "" {
		t.Fatalf("legacy rule link version = (%q, %q) on v15, want unversioned — no backfill, no re-hash", ver, effAt)
	}
}

// ──────────────────────────────────────────────
// G.1/G.2/G.3 — Direct-upgrade matrix v1…v13 → v14
// ──────────────────────────────────────────────

// legacyFixture is one direct-upgrade row: a GENUINE store built at the frozen
// source version (G.1), seeded with the representative additive rows that first
// exist at that version (G.2), opened through the normal Open path, and asserted
// for schema v14, byte preservation, row counts, the invariant manifest and the
// fail-closed guards (G.3). seed/assertPreserved are dispatched by version through
// seedLegacyRows/assertSnapshotPreserved (the frozen helpers are shared, not
// cloned per row).
type legacyFixture struct {
	version int
	build   func(t *testing.T, path string) *sql.DB
}

// baselineBytes is the exact identity bytes of the baseline observation that
// every source fixture seeds (valid at the source version's column shape).
type baselineBytes struct {
	id, topicKey, orgID, actor, source string
	revision                           int
}

// receiptBytes is the exact receipt row bytes of the receipt/key representative
// (v5+). The receipts table is REBUILT at v5→v6, v7→v8 and v12→v13, so these
// bytes prove every rebuild is byte-preserving.
type receiptBytes struct{ hash, action, payload string }

// legacySnapshot is the pre-open evidence: exact bytes + per-table row counts.
type legacySnapshot struct {
	baseline   baselineBytes
	counts     map[string]int
	receipt    receiptBytes
	policyJSON string // observations.policy_rule_json exact bytes (v7+)
	ruleRef    string // rule_links.ref exact bytes (v13+)
}

// Matrix seed identifiers (stable per row; every fixture is a fresh temp DB).
const (
	matrixObjectID    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	matrixObject2ID   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	matrixPolicyID    = "00000000-0000-4000-8000-000000000001"
	matrixHoldID      = "00000000-0000-4000-8000-000000000010"
	matrixRequestID   = "00000000-0000-4000-8000-000000000101"
	matrixEventID     = "00000000-0000-4000-8000-000000000301"
	matrixExecutionID = "00000000-0000-4000-8000-000000000401"
	matrixPolicyRule  = `{"rule":"audit.upgrade.policy.rule","version":1}`
)

// buildLegacyStore builds a GENUINE store at the source version by reusing the
// frozen migration-era builders; v6 and v12 apply the real production
// migrations on top of the adjacent frozen builder (NFR-G.1 — no cloned DDL).
func buildLegacyStore(t *testing.T, version int, path string) *sql.DB {
	t.Helper()
	if version == 1 {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open raw v1 database: %v", err)
		}
		if _, err := db.Exec(v1SchemaDDL); err != nil {
			_ = db.Close()
			t.Fatalf("apply v1 schema: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	if version == 6 {
		db := openV5Schema(t, path)
		if err := migrateV5ToV6(db); err != nil {
			_ = db.Close()
			t.Fatalf("bootstrap v6 layout: %v", err)
		}
		return db
	}
	if version == 12 {
		db := openV11Schema(t, path)
		if err := migrateV11ToV12(db); err != nil {
			_ = db.Close()
			t.Fatalf("bootstrap v12 layout: %v", err)
		}
		return db
	}
	builder, ok := legacyBuilders[version]
	if !ok {
		t.Fatalf("no frozen builder for schema_version=%d", version)
	}
	return builder(t, path)
}

// legacyBuilders maps every version with a dedicated frozen builder (the v1
// special case and the v6/v12 real-migration compositions live in buildLegacyStore).
var legacyBuilders = map[int]func(t *testing.T, path string) *sql.DB{
	2: openV2Schema, 3: openV3Schema, 4: openV4Schema, 5: openV5Schema,
	7: openV7Schema, 8: openV8Schema, 9: openV9Schema, 10: openV10Schema,
	11: openV11Schema, 13: openV13Schema,
}

// tablesAtVersion lists every table a genuine store of that version owns (the
// additive chain never drops). The snapshot counts all of them so the post-open
// assertion proves zero loss AND zero duplication.
func tablesAtVersion(version int) []string {
	tables := []string{"observations", "relations", "transition_log"}
	if version >= 2 {
		tables = append(tables, "evidence_links", "rule_links")
	}
	for threshold, names := range map[int][]string{
		3: v3Tables(), 4: v4Tables(), 5: v5Tables(), 6: v6Tables(),
		8: v8ObjectTables(), 9: v9Tables(), 10: v10Tables(),
		11: v11Tables(), 12: v12Tables(),
	} {
		if version >= threshold {
			tables = append(tables, names...)
		}
	}
	if version >= 13 {
		tables = append(tables, "memory_decision_events", "review_idempotency_keys", "review_velocity_events")
	}
	return tables
}

// countRowsDB counts rows of one table on a raw handle.
func countRowsDB(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

// seedBaseline inserts the baseline observation valid at the source version and
// returns its identity bytes: the v1 exact 21-column shape, the v2 exact v2
// column list (saveV2Row), the v3 shape (saveV3Row, adds materiality_level),
// and from v7 the 41-column shape that writes policy_rule_json INLINE — the v7
// immutability trigger guards that column, so an UPDATE after insert would abort.
func seedBaseline(t *testing.T, db *sql.DB, version int) baselineBytes {
	t.Helper()
	if version == 1 {
		if _, err := db.Exec(`
			INSERT INTO observations (
				id, topic_key, title, type, scope_kind, organization_id, company_id, ruc, period,
				what, why, where_text, learned, authority_status, effective_at, expires_at,
				actor, timestamp, source, session, revision
			) VALUES ('seed-baseline', 'audit.upgrade.v1', 'baseline', 'decision', 'company', ?, 'acme', ?, '202401',
				'what', 'why', 'where', 'learned', 'promoted', '2026-01-01T00:00:00Z', '',
				'actor-v1', '2026-01-01T00:00:00Z', 'cli', 's-1', 1)`,
			testOrgID, testRucA,
		); err != nil {
			t.Fatalf("seed v1 baseline observation: %v", err)
		}
		return baselineBytes{id: "seed-baseline", topicKey: "audit.upgrade.v1", orgID: testOrgID, actor: "actor-v1", source: "cli", revision: 1}
	}
	input := validInput("audit.upgrade.v"+strconv.Itoa(version), "baseline observation")
	if version < 7 {
		mem := saveV2Row(t, db, input)
		if version >= 3 {
			mem = saveV3Row(t, db, input)
		}
		return baselineBytes{id: mem.Identity.ID, topicKey: input.TopicKey, orgID: mem.Scope.OrganizationID, actor: mem.Source.ActorID, source: mem.Source.System, revision: 1}
	}
	id := "seed-baseline-v" + strconv.Itoa(version)
	if _, err := db.Exec(`
		INSERT INTO observations (
			id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
			what, why, where_text, learned, authority_status, status, fiscal_effect, effective_at, recorded_at, observed_at,
			expires_at, validity_effective_at, validity_source, actor, timestamp, source, session, source_json, content_hash, identity_hash, envelope_hash, evidence_refs_json, rule_refs_json,
			confidence, materiality, materiality_level, close_snapshot_json, policy_rule_json,
			receipt_id, supersedes_id, revision
		) VALUES (?, ?, 'baseline', 'rule', 'rule', 'company', ?, 'acme', ?, '202401',
			'what', 'why', 'where', 'learned', 'reviewed', 'active', 'none', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '',
			'', '', '', 'seed-actor', '2026-01-01T00:00:00Z', 'go-test', '', '', 'seed-hash', '', '', '[]', '[]',
			NULL, NULL, NULL, NULL, ?, '', '', 1)`,
		id, input.TopicKey, testOrgID, testRucA, matrixPolicyRule,
	); err != nil {
		t.Fatalf("seed v7+ baseline observation: %v", err)
	}
	return baselineBytes{id: id, topicKey: input.TopicKey, orgID: testOrgID, actor: "seed-actor", source: "go-test", revision: 1}
}

// seedApprovalRows seeds the v3 approval/membership surface (the frozen FK chain
// companies → memberships → approval_events used by TestApprovalEventsRejectUpdateAndDelete).
func seedApprovalRows(t *testing.T, db *sql.DB, memoryID string) {
	t.Helper()
	now := nowISO()
	if _, err := db.Exec(`INSERT INTO companies (id, tenant_id, ruc, name, active, created_at) VALUES ('co-1', ?, ?, 'ACME', 1, ?)`, testOrgID, testRucA, now); err != nil {
		t.Fatalf("seed company: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO memberships (id, subject_id, tenant_id, company_id, status, created_at, updated_at) VALUES ('mem-1', 'subj-1', ?, 'co-1', 'active', ?, ?)`, testOrgID, now, now); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO approval_events (id, request_id, memory_id, tenant_id, company_id, fiscal_period_id,
			action, from_status, to_status, reviewed_envelope_hash, resulting_envelope_hash, reason,
			principal_subject_id, membership_id, principal_roles_json, authentication_method,
			assurance_level, principal_authenticated_at, policy_version, authorization_reason_code, created_at)
		VALUES ('evt-1', 'req-1', ?, ?, 'acme', NULL, 'approved', 'pending_review', 'approved', 'h1', 'h2',
			'seed reason', 'subj-1', 'mem-1', '[]', 'session', 'standard', ?, 'approval-policy/v0.4.0', 'AUTHORIZED', ?)`,
		memoryID, testOrgID, now, now,
	); err != nil {
		t.Fatalf("seed approval event: %v", err)
	}
}

// seedObservationPair saves the two observations that anchor the judgment and
// reconciliation representatives.
func seedObservationPair(t *testing.T, db *sql.DB) (from, to string) {
	t.Helper()
	from = saveV3Row(t, db, validInput("audit.upgrade.pair.from", "from")).Identity.ID
	to = saveV3Row(t, db, validInput("audit.upgrade.pair.to", "to")).Identity.ID
	return from, to
}

// seedReconciliationRows seeds the v6 materiality/close + reconciliation
// surfaces: one period closure (closed) and one proposed reconciliation with its
// event, anchored on the judgment pair observations.
func seedReconciliationRows(t *testing.T, db *sql.DB, from, to string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO period_closures (tenant_id, company_id, fiscal_period_id, close_memory_id, status, closed_at)
		VALUES (?, 'acme', '202401', ?, 'closed', ?)`, testOrgID, from, testT,
	); err != nil {
		t.Fatalf("seed period closure: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO reconciliations (id, tenant_id, company_id, fiscal_period_id, left_memory_id, right_memory_id,
			method, currency, left_amount_cents, right_amount_cents, variance_cents, tolerance_cents,
			status, proposer_system, proposer_actor_id, proposer_actor_kind, proposer_session,
			proposal_reason, proposed_at)
		VALUES ('seed-recon-1', ?, 'acme', '202401', ?, ?, 'reconciles', 'PEN', 1000, 900, 100, 0,
			'proposed', 'go-test', 'agent-1', 'agent', '', 'seed reason', ?)`,
		testOrgID, from, to, testT,
	); err != nil {
		t.Fatalf("seed reconciliation: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO reconciliation_events (id, reconciliation_id, request_id, action, from_status, to_status,
			reconciliation_hash, principal_snapshot_json, policy_version, reason, created_at)
		VALUES ('rev-1', 'seed-recon-1', 'req-recon-1', 'withdraw', 'proposed', 'withdrawn', 'hash', NULL, NULL, '', ?)`,
		testT,
	); err != nil {
		t.Fatalf("seed reconciliation event: %v", err)
	}
}

// seedObjectPolicyHoldRows seeds the v8/v9/v10 evidence surfaces in place: the
// evidence object (v8), the retention policy (v9) and the blocking hold (v10).
func seedObjectPolicyHoldRows(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	if version >= 8 {
		if _, err := db.Exec(`
			INSERT INTO evidence_objects (id, sha256, size, content_type, tenant_id, company_id, ruc, period,
				source_system, source_reference, source_actor_id, source_actor_kind, stored_by, stored_at, rel_path)
			VALUES (?, ?, 4, 'application/xml', ?, 'acme', ?, '202401', 'go-test', '', 'agent-1', 'agent', 'agent-1', ?, 'objects/aa/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
			matrixObjectID, matrixObjectID, testOrgID, testRucA, testT,
		); err != nil {
			t.Fatalf("seed evidence object: %v", err)
		}
	}
	if version >= 9 {
		if _, err := db.Exec(`
			INSERT INTO retention_policies (id, tenant_id, jurisdiction, legislation, authority, source,
				category, min_period, version, dual_approval_required, dual_approver_roles,
				blocking_hold_kinds, enabled, created_at, created_by)
			VALUES (?, 'org-001', 'PE', 'NATIONAL-TAX', 'tenant-records', 'deployment decision', 'invoice', '202401', 1, 1,
				'["controller","tax_responsible"]', '["audit","dispute","fiscalization","legal"]', 1, ?, 'subject-1')`,
			matrixPolicyID, testT,
		); err != nil {
			t.Fatalf("seed retention policy: %v", err)
		}
	}
	if version >= 10 {
		if _, err := db.Exec(`
			INSERT INTO evidence_holds (id, object_id, tenant_id, company_id, ruc, period, kind, reason,
				owner_subject_id, placed_at, placed_by)
			VALUES (?, ?, ?, 'acme', ?, '202401', 'legal', 'dispute under review', 'subject-1', ?, 'subject-1')`,
			matrixHoldID, matrixObjectID, testOrgID, testRucA, testT,
		); err != nil {
			t.Fatalf("seed hold: %v", err)
		}
	}
}

// seedPurgeRows seeds the v11 purge lifecycle: one requested pipeline with its
// immutable lifecycle event.
func seedPurgeRows(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO evidence_purge_requests (id, object_id, tenant_id, company_id, ruc, period, category, policy_id,
			retention_state_snapshot, reviewed_lifecycle_hash, status, requested_at, requested_by)
		VALUES (?, ?, ?, 'acme', ?, '202401', 'invoice', ?, 'eligible', ?, 'requested', ?, 'subject-1')`,
		matrixRequestID, matrixObjectID, testOrgID, testRucA, matrixPolicyID, matrixObjectID, testT,
	); err != nil {
		t.Fatalf("seed purge request: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO evidence_lifecycle_events (id, object_id, request_id, action, from_state, to_state,
			reviewed_hash, resulting_hash, principal_snapshot_json, reason, policy_version, created_at)
		VALUES (?, ?, ?, 'purge_requested', 'stored', 'purge_requested', ?, ?, '{"subjectId":"subject-1","roles":["controller"]}',
			'retention period elapsed', 'evidence-lifecycle-policy/v0.8.0', ?)`,
		matrixEventID, matrixObjectID, matrixRequestID, matrixObjectID, matrixObject2ID, testT,
	); err != nil {
		t.Fatalf("seed lifecycle event: %v", err)
	}
}

// seedExecutionRow seeds the v12 purge execution intent row.
func seedExecutionRow(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO evidence_purge_executions (execution_id, request_id, object_id, rel_path, size, pre_removal_hash,
			intent_reviewed_hash, state, intent_at, intent_by)
		VALUES (?, ?, ?, 'objects/aa/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 4, ?, ?, 'intent', ?, 'subject-1')`,
		matrixExecutionID, matrixRequestID, matrixObjectID, matrixObjectID, matrixObject2ID, testT,
	); err != nil {
		t.Fatalf("seed purge execution: %v", err)
	}
}

// seedReviewRows seeds the v13 review-decision ledger row and the legacy
// unversioned rule link (the v13→v14 structured-link migration target).
func seedReviewRows(t *testing.T, db *sql.DB, memoryID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO memory_decision_events (id, request_id, memory_id, tenant_id, company_id, fiscal_period_id,
			action, from_status, to_status, reviewed_envelope_hash, resulting_envelope_hash, reason,
			principal_subject_id, membership_id, principal_roles_json, authentication_method, assurance_level,
			principal_authenticated_at, policy_version, authorization_reason_code, created_at)
		VALUES ('seed-decision-1', 'req-decision-1', ?, ?, 'acme', '202401', 'rejected', 'pending_review', 'rejected',
			'h1', 'h2', 'seed reason', 'subj-1', 'mem-1', '[]', 'session', 'standard', ?, 'kernel/v0.4.0', 'REJECTED', ?)`,
		memoryID, testOrgID, testT, testT,
	); err != nil {
		t.Fatalf("seed review decision: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO rule_links (memory_id, ref, actor, timestamp) VALUES (?, 'audit.upgrade.rule-ref', 'seed-actor', ?)`, memoryID, testT); err != nil {
		t.Fatalf("seed legacy rule link: %v", err)
	}
}

// seedLegacyRows seeds the baseline plus every representative whose feature
// first exists at or before the source version (G.2 — the first-version rule),
// captures the exact bytes and per-table row counts, and returns the snapshot.
func seedLegacyRows(t *testing.T, db *sql.DB, version int) legacySnapshot {
	t.Helper()
	base := seedBaseline(t, db, version)
	if version >= 3 {
		seedApprovalRows(t, db, base.id)
	}
	var from, to string
	if version >= 4 {
		from, to = seedObservationPair(t, db)
		insertJudgment(t, db, proposedJudgment("seed-judgment-1", from, to))
	}
	if version >= 6 {
		seedReconciliationRows(t, db, from, to)
	}
	if version >= 5 && version < 7 {
		insertV5ReceiptRowRaw(t, db, base.id, "ed25519:key-v5")
	}
	receiptHash := "receipt-hash-v5"
	if version >= 7 {
		insertV7ReceiptRowRaw(t, db, base.id, "ed25519:key-v7", "receipt-hash-v7")
		receiptHash = "receipt-hash-v7"
	}
	if version >= 8 {
		seedObjectPolicyHoldRows(t, db, version)
	}
	if version >= 11 {
		seedPurgeRows(t, db)
	}
	if version >= 12 {
		seedExecutionRow(t, db)
	}
	if version >= 13 {
		seedReviewRows(t, db, base.id)
	}
	snap := legacySnapshot{baseline: base, counts: map[string]int{}}
	for _, table := range tablesAtVersion(version) {
		snap.counts[table] = countRowsDB(t, db, table)
	}
	if version >= 5 {
		var action, payload string
		if err := db.QueryRow(`SELECT action, payload_json FROM receipts WHERE receipt_hash = ?`, receiptHash).Scan(&action, &payload); err != nil {
			t.Fatalf("read seeded receipt: %v", err)
		}
		snap.receipt = receiptBytes{hash: receiptHash, action: action, payload: payload}
	}
	if version >= 7 {
		if err := db.QueryRow(`SELECT policy_rule_json FROM observations WHERE id = ?`, base.id).Scan(&snap.policyJSON); err != nil {
			t.Fatalf("read seeded policy_rule_json: %v", err)
		}
	}
	if version >= 13 {
		if err := db.QueryRow(`SELECT ref FROM rule_links WHERE memory_id = ?`, base.id).Scan(&snap.ruleRef); err != nil {
			t.Fatalf("read seeded rule link: %v", err)
		}
	}
	return snap
}

// TestDirectUpgradeMatrixV1ToV16 proves every legacy schema version v1…v13
// converges to v14 through the normal Open path with exact data preservation
// and the full structural invariant manifest (AC-G-1, FR-G.1).
func TestDirectUpgradeMatrixV1ToV16(t *testing.T) {
	for _, fx := range []legacyFixture{
		{1, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 1, p) }},
		{2, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 2, p) }},
		{3, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 3, p) }},
		{4, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 4, p) }},
		{5, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 5, p) }},
		{6, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 6, p) }},
		{7, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 7, p) }},
		{8, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 8, p) }},
		{9, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 9, p) }},
		{10, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 10, p) }},
		{11, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 11, p) }},
		{12, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 12, p) }},
		{13, func(t *testing.T, p string) *sql.DB { return buildLegacyStore(t, 13, p) }},
	} {
		fx := fx
		t.Run(fmt.Sprintf("v%d_to_v16", fx.version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), fmt.Sprintf("legacy-v%d.db", fx.version))
			db := fx.build(t, path)
			version, err := readSchemaVersion(db)
			if err != nil {
				t.Fatalf("read source schema_version: %v", err)
			}
			if version != fx.version {
				t.Fatalf("builder produced schema_version=%d, want %d (genuine source layout)", version, fx.version)
			}
			before := seedLegacyRows(t, db, fx.version)
			if err := db.Close(); err != nil {
				t.Fatalf("close raw v%d handle: %v", fx.version, err)
			}

			s, err := Open(path) // exactly once, through the normal open path
			if err != nil {
				t.Fatalf("Open must migrate v%d→v14: %v", fx.version, err)
			}
			defer func() { _ = s.Close() }()
			version, err = readSchemaVersion(s.db)
			if err != nil {
				t.Fatalf("read schema_version after migration: %v", err)
			}
			if version != 17 {
				t.Fatalf("schema_version = %d, want 17", version)
			}
			assertSnapshotPreserved(t, s, before, fx.version)
			assertV14InvariantManifest(t, s)
			assertFailClosedGuards(t, s, fx.version, before.baseline.id)
		})
	}
}

// assertSnapshotPreserved compares every seeded value and per-table row count
// with the pre-open snapshot (NFR-G.1 — never version numbers alone).
func assertSnapshotPreserved(t *testing.T, s *SQLiteStore, before legacySnapshot, version int) {
	t.Helper()
	var got baselineBytes
	if err := s.db.QueryRow(`SELECT id, topic_key, organization_id, actor, source, revision FROM observations WHERE id = ?`, before.baseline.id).
		Scan(&got.id, &got.topicKey, &got.orgID, &got.actor, &got.source, &got.revision); err != nil {
		t.Fatalf("baseline observation lost after migration: %v", err)
	}
	if got != before.baseline {
		t.Fatalf("baseline bytes changed after migration: got %+v want %+v", got, before.baseline)
	}
	if before.receipt.hash != "" {
		var action, payload string
		if err := s.db.QueryRow(`SELECT action, payload_json FROM receipts WHERE receipt_hash = ?`, before.receipt.hash).
			Scan(&action, &payload); err != nil {
			t.Fatalf("receipt row lost after migration: %v", err)
		}
		if action != before.receipt.action || payload != before.receipt.payload {
			t.Fatalf("receipt bytes changed after migration: (%q,%q) want (%q,%q)", action, payload, before.receipt.action, before.receipt.payload)
		}
	}
	if before.policyJSON != "" {
		var gotJSON string
		if err := s.db.QueryRow(`SELECT policy_rule_json FROM observations WHERE id = ?`, before.baseline.id).Scan(&gotJSON); err != nil {
			t.Fatalf("policy_rule_json lost after migration: %v", err)
		}
		if gotJSON != before.policyJSON {
			t.Fatalf("policy_rule_json changed after migration: got %q want %q", gotJSON, before.policyJSON)
		}
	}
	if before.ruleRef != "" {
		var ref string
		if err := s.db.QueryRow(`SELECT ref FROM rule_links WHERE memory_id = ?`, before.baseline.id).Scan(&ref); err != nil {
			t.Fatalf("rule link lost after migration: %v", err)
		}
		if ref != before.ruleRef {
			t.Fatalf("rule link ref changed after migration: got %q want %q", ref, before.ruleRef)
		}
	}
	for _, table := range tablesAtVersion(version) {
		if count := countRowsDB(t, s.db, table); count != before.counts[table] {
			t.Fatalf("%s rows = %d after migration, want %d (nothing lost, nothing duplicated)", table, count, before.counts[table])
		}
	}
}

// assertV14InvariantManifest asserts the final v14 layout: every table/index/
// trigger from the frozen per-version helper lists, the structured-link columns
// and reverse index, and zero staging/partial tables from any receipts rebuild.
func assertV14InvariantManifest(t *testing.T, s *SQLiteStore) {
	t.Helper()
	check := func(kind string, objects []string) {
		t.Helper()
		for _, name := range objects {
			var found string
			if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = ? AND name = ?`, kind, name).Scan(&found); err != nil {
				t.Fatalf("v14 store missing %s %q: %v", kind, name, err)
			}
		}
	}
	tables := []string{"observations", "relations", "transition_log", "evidence_links", "rule_links"}
	tables = append(tables, v3Tables()...)
	tables = append(tables, v4Tables()...)
	tables = append(tables, v5Tables()...)
	tables = append(tables, v6Tables()...)
	tables = append(tables, v8ObjectTables()...)
	tables = append(tables, v9Tables()...)
	tables = append(tables, v10Tables()...)
	tables = append(tables, v11Tables()...)
	tables = append(tables, v12Tables()...)
	tables = append(tables, "memory_decision_events", "review_idempotency_keys", "review_velocity_events")
	check("table", tables)

	indexes := []string{"idx_observations_chain", "idx_observations_scope", "idx_relations_from", "idx_transition_log_obs",
		"idx_evidence_links_memory", "idx_rule_links_memory", "idx_rule_links_ref", "idx_memory_decision_events_memory"}
	indexes = append(indexes, v4Indexes()...)
	indexes = append(indexes, v5Indexes()...)
	indexes = append(indexes, v6Indexes()...)
	indexes = append(indexes, v8ObjectIndexes()...)
	indexes = append(indexes, v9Indexes()...)
	indexes = append(indexes, v10Indexes()...)
	indexes = append(indexes, v11Indexes()...)
	indexes = append(indexes, v12Indexes()...)
	check("index", indexes)

	triggers := []string{"observations_immutable_content", "observations_no_delete"}
	triggers = append(triggers, v3Triggers()...)
	triggers = append(triggers, v4Triggers()...)
	triggers = append(triggers, v5Triggers()...)
	triggers = append(triggers, v6Triggers()...)
	triggers = append(triggers, v8ObjectTriggers()...)
	triggers = append(triggers, v9Triggers()...)
	triggers = append(triggers, v10Triggers()...)
	triggers = append(triggers, v11Triggers()...)
	triggers = append(triggers, v12Triggers()...)
	triggers = append(triggers, "memory_decision_events_no_update", "memory_decision_events_no_delete",
		"review_velocity_events_no_update", "review_velocity_events_no_delete")
	check("trigger", triggers)

	// rule_links carries the v14 structured-link columns.
	columns := ruleLinkColumns(t, s.db)
	for _, column := range []string{"version", "effective_at"} {
		if !columns[column] {
			t.Fatalf("v14 store missing rule_links.%s", column)
		}
	}
	// No staging/partial table from any receipts rebuild (receipts_v*) remains.
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'receipts_v%'`)
	if err != nil {
		t.Fatalf("scan for staging tables: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatal("staging/partial table (receipts_v*) remains after migration to v14")
	}
}

// assertFailClosedGuards runs representative immutable/no-delete checks on the
// migrated structures (G.3 step 7), conditioned on the features seeded at the
// source version.
func assertFailClosedGuards(t *testing.T, s *SQLiteStore, version int, baselineID string) {
	t.Helper()
	abort := func(stmt string, want string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(stmt, args...); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s must abort with %s, got %v", stmt, want, err)
		}
	}
	abort(`UPDATE observations SET title = 'mutated' WHERE id = ?`, "IMMUTABLE_OBSERVATION", baselineID)
	abort(`DELETE FROM observations WHERE id = ?`, "IMMUTABLE_OBSERVATION", baselineID)
	if version >= 3 {
		abort(`DELETE FROM approval_events WHERE id = 'evt-1'`, "IMMUTABLE_APPROVAL_EVENT")
	}
	if version >= 4 {
		abort(`DELETE FROM judgments WHERE id = 'seed-judgment-1'`, "IMMUTABLE_JUDGMENT")
	}
	if version >= 11 {
		abort(`UPDATE evidence_lifecycle_events SET reason = 'mutated' WHERE id = ?`, "IMMUTABLE_LIFECYCLE_EVENT", matrixEventID)
	}
}
