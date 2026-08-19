// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module verifies the v3→v4 additive
// migration (v0.4.0 Step 2 — adjudicable conflicts): fresh stores bootstrap to
// schema_version=5 with the four judgment persistence tables and their indexes,
// existing v3 data survives untouched, a failing migration rolls back leaving
// schema_version=3, and the judgment immutability surface (triggers + open-tuple
// partial unique index) enforces exactly the updates the design §4 allows.

package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// openV3Schema opens a raw SQLite handle and bootstraps the EXACT v3 layout
// (applySchema + the Open pragmas + the v2→v3 migration), so tests can exercise
// the v3→v4 migration on a genuine v3 store.
func openV3Schema(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw v3 database: %v", err)
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
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// openRawReadHandle opens a database WITHOUT running any migration (applySchema
// is idempotent), used to inspect the on-disk state after a failed migration.
func openRawReadHandle(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
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
		t.Fatalf("apply base schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// saveV3Row inserts a row with the EXACT v3 column list (materiality_level
// included, NULL), the way a genuine v3 store would have written it. Production
// never writes to a v3 store (Open migrates first); this helper exists so the
// migration test can seed legacy-shaped rows.
func saveV3Row(t *testing.T, db *sql.DB, input core.SaveInput) core.AccountingMemory {
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
			confidence, materiality, materiality_level, receipt_id, supersedes_id, revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.TopicKey, memory.Title, string(memory.Kind), string(memory.Kind), string(memory.Scope.Kind),
		memory.Scope.OrganizationID, memory.Scope.CompanyID, memory.Scope.RUC, memory.Scope.Period,
		memory.Content.What, memory.Content.Why, memory.Content.Where, memory.Content.Learned,
		legacyStatusFor(memory.Status), string(memory.Status), string(memory.FiscalEffect),
		memory.EffectiveAt, memory.RecordedAt, memory.ObservedAt,
		validityExpiresAt(memory.Validity), validityEffectiveAt(memory.Validity), validitySource(memory.Validity),
		memory.Source.ActorID, memory.RecordedAt, memory.Source.System, memory.Source.Session,
		encodeSource(memory.Source), memory.ContentHash, memory.IdentityHash, memory.EnvelopeHash, encodeRefs(memory.EvidenceRefs), encodeRefs(memory.RuleRefs),
		nullableFloat(&memory.Confidence), nullableInt(memory.Materiality), nil, memory.ReceiptID, memory.SupersedesID,
		memory.Revision,
	)
	if err != nil {
		t.Fatalf("insert v3 row: %v", err)
	}
	return memory
}

func v4Tables() []string {
	return []string{"judgments", "judgment_events", "judgment_idempotency_keys", "judgment_relations"}
}

func v4Indexes() []string {
	return []string{"uq_judgment_open_tuple", "idx_judgments_pair", "idx_judgments_predecessor", "idx_judgments_successor"}
}

func v4Triggers() []string {
	return []string{"judgment_events_no_update", "judgment_events_no_delete", "judgments_no_delete", "judgments_immutable_update"}
}

func TestFreshStoreBootstrapsV4JudgmentPersistence(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 17 {
		t.Fatalf("schema_version = %d, want 17 (the chain continues v4→v5→v6→v7→v8→v9→v10→v11→v12→v13→v14)", version)
	}

	// The v3 layer survives the chain (additive migrations never drop objects).
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

	for _, table := range v4Tables() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("fresh store missing v4 table %q: %v", table, err)
		}
	}
	for _, idx := range v4Indexes() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("fresh store missing v4 index %q: %v", idx, err)
		}
	}
	for _, trigger := range v4Triggers() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&name); err != nil {
			t.Fatalf("fresh store missing v4 trigger %q: %v", trigger, err)
		}
	}

	// uq_judgment_open_tuple must be a PARTIAL unique index over the open
	// proposal tuple only.
	var indexSQL string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'uq_judgment_open_tuple'`).Scan(&indexSQL); err != nil {
		t.Fatalf("read uq_judgment_open_tuple definition: %v", err)
	}
	if !strings.Contains(indexSQL, "WHERE status='proposed'") || !strings.Contains(indexSQL, "UNIQUE") {
		t.Fatalf("uq_judgment_open_tuple is not the partial unique open-tuple index: %s", indexSQL)
	}
}

func TestV3StoreMigratesToV5AdditivelyPreservingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")
	db := openV3Schema(t, path)

	active := saveV3Row(t, db, validInput("tax.igv.rate", "first version"))
	gatedInput := validInput("tax.igv.gated", "needs approval")
	gatedInput.FiscalEffect = core.FiscalEffectClosing
	gatedInput.EffectiveAt = testT
	pending := saveV3Row(t, db, gatedInput)
	_ = db.Close() // release the file before Open migrates it

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open migrates v3→v4: %v", err)
	}
	defer func() { _ = s.Close() }()

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version after migration: %v", err)
	}
	if version != 17 {
		t.Fatalf("schema_version after migration = %d, want 17 (the chain continues v4→v5→v6→v7→v8→v9→v10→v11→v12→v13→v14)", version)
	}

	// Rows survive additively with EXACTLY the envelope bytes written at v3.
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

	// The v3 approval surface is intact after the chain (immutability triggers
	// still enforce).
	for _, trigger := range v3Triggers() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&name); err != nil {
			t.Fatalf("migrated store missing v3 trigger %q: %v", trigger, err)
		}
	}
	for _, table := range v4Tables() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("migrated store missing v4 table %q: %v", table, err)
		}
	}
}

func TestV3ToV4MigrationRollsBackLeavingSchemaV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3-fail.db")
	db := openV3Schema(t, path)
	// A conflicting pre-existing table makes the verbatim CREATE TABLE judgments
	// fail — the corrupt-step probe for the single-transaction rollback.
	if _, err := db.Exec(`CREATE TABLE judgments (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("pre-create conflicting table: %v", err)
	}
	_ = db.Close()

	_, err := Open(path)
	if err == nil {
		t.Fatal("Open must fail closed when the migration fails")
	}
	if !strings.Contains(err.Error(), "migrate v3→v4") {
		t.Fatalf("error %q does not identify the migration step", err)
	}

	// Re-open raw: schema_version must still be 3 and NO v4 object may survive.
	db = openRawReadHandle(t, path)
	version, err := readSchemaVersion(db)
	if err != nil {
		t.Fatalf("read schema version after failed migration: %v", err)
	}
	if version != 3 {
		t.Fatalf("schema_version = %d after failed migration, want 3 (rolled back)", version)
	}
	for _, table := range []string{"judgment_events", "judgment_idempotency_keys", "judgment_relations"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err == nil {
			t.Fatalf("table %q survived the migration rollback", table)
		}
	}
	for _, idx := range v4Indexes() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err == nil {
			t.Fatalf("index %q survived the migration rollback", idx)
		}
	}
	for _, trigger := range v4Triggers() {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&name); err == nil {
			t.Fatalf("trigger %q survived the migration rollback", trigger)
		}
	}
}

// ──────────────────────────────────────────────
// Judgment seeding helpers
// ──────────────────────────────────────────────

// seedJudgmentContext saves two observations and the minimal FK chain
// (companies → memberships) that a confirmed/rejected judgment needs for its
// adjudicator FK, returning the two observation ids.
func seedJudgmentContext(t *testing.T, s *SQLiteStore) (fromID, toID string) {
	t.Helper()
	a, err := s.Save(validInput("tax.judgment.from", "from observation"))
	if err != nil {
		t.Fatalf("save from observation: %v", err)
	}
	b, err := s.Save(validInput("tax.judgment.to", "to observation"))
	if err != nil {
		t.Fatalf("save to observation: %v", err)
	}
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
	return a.Memory.Identity.ID, b.Memory.Identity.ID
}

// proposedJudgment builds a legal proposed judgment over the given pair.
func proposedJudgment(id, fromID, toID string) core.AccountingJudgment {
	return core.AccountingJudgment{
		ID:             id,
		TenantID:       testOrgID,
		CompanyID:      "acme",
		FromID:         fromID,
		ToID:           toID,
		Relation:       core.RelationSupports,
		Status:         core.JudgmentProposed,
		Proposer:       core.Source{System: "go-test", ActorID: "agent-1", ActorKind: core.ActorKindAgent},
		ProposalReason: "proposal reason",
		ProposedAt:     testT,
		UpdatedAt:      testT,
	}
}

// confirmedJudgment builds a legal confirmed judgment (resolution + canonical
// adjudicator snapshot + frozen policy version) over the given pair.
func confirmedJudgment(id, fromID, toID string) core.AccountingJudgment {
	j := proposedJudgment(id, fromID, toID)
	j.Status = core.JudgmentConfirmed
	j.Resolution = "professional resolution"
	j.PolicyVersion = "judgment-policy/v0.4.0"
	j.DecidedAt = testT
	j.Adjudicator = &auth.PrincipalSnapshot{
		SubjectID:            "subj-1",
		MembershipID:         "mem-1",
		Roles:                []auth.AccountingRole{auth.RoleSeniorAccountant},
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      testT,
	}
	return j
}

func nullableStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// insertJudgmentRaw persists a judgment row from the core model. The schema
// CHECKs (from_id<>to_id, decided_at present exactly when not proposed, etc.)
// are enforced by SQLite.
func insertJudgmentRaw(db *sql.DB, j core.AccountingJudgment) error {
	adjudicatorSubject, adjudicatorMembership, adjudicatorRoles := "", "", ""
	authMethod, assurance, authAt := "", "", ""
	var resolution, policyVersion string
	if j.Adjudicator != nil {
		adjudicatorSubject = j.Adjudicator.SubjectID
		adjudicatorMembership = j.Adjudicator.MembershipID
		rolesJSON, err := json.Marshal(j.Adjudicator.Roles)
		if err != nil {
			return err
		}
		adjudicatorRoles = string(rolesJSON)
		authMethod = string(j.Adjudicator.AuthenticationMethod)
		assurance = string(j.Adjudicator.AssuranceLevel)
		authAt = j.Adjudicator.AuthenticatedAt
	}
	resolution = j.Resolution
	policyVersion = j.PolicyVersion
	_, err := db.Exec(`
		INSERT INTO judgments (
			id, tenant_id, company_id, fiscal_period_id, from_id, to_id, relation, status,
			proposer_system, proposer_actor_id, proposer_actor_kind, proposer_session,
			proposal_reason, resolution, policy_version,
			adjudicator_subject_id, adjudicator_membership_id, adjudicator_roles_json,
			authentication_method, assurance_level, principal_authenticated_at,
			predecessor_id, supersedes_id, proposed_at, updated_at, decided_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.TenantID, j.CompanyID, nullableStr(j.FiscalPeriodID),
		j.FromID, j.ToID, string(j.Relation), string(j.Status),
		j.Proposer.System, j.Proposer.ActorID, string(j.Proposer.ActorKind), j.Proposer.Session,
		j.ProposalReason, nullableStr(resolution), nullableStr(policyVersion),
		nullableStr(adjudicatorSubject), nullableStr(adjudicatorMembership), nullableStr(adjudicatorRoles),
		nullableStr(authMethod), nullableStr(assurance), nullableStr(authAt),
		nullableStr(j.PredecessorID), nullableStr(j.SupersedesID),
		j.ProposedAt, j.UpdatedAt, nullableStr(j.DecidedAt),
	)
	return err
}

func insertJudgment(t *testing.T, db *sql.DB, j core.AccountingJudgment) {
	t.Helper()
	if err := insertJudgmentRaw(db, j); err != nil {
		t.Fatalf("insert judgment %s: %v", j.ID, err)
	}
}

// confirmJudgmentRow performs the machine transition proposed → confirmed on a
// stored row (the atomic adjudication commit later performs the same UPDATE
// inside BEGIN IMMEDIATE).
func confirmJudgmentRow(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	now := nowISO()
	if _, err := db.Exec(`
		UPDATE judgments SET status = 'confirmed', resolution = ?, policy_version = ?,
			adjudicator_subject_id = 'subj-1', adjudicator_membership_id = 'mem-1',
			adjudicator_roles_json = '["senior_accountant"]', authentication_method = 'session',
			assurance_level = 'standard', principal_authenticated_at = ?,
			decided_at = ?, updated_at = ?
		WHERE id = ?`,
		"professional resolution", "judgment-policy/v0.4.0", now, now, now, id,
	); err != nil {
		t.Fatalf("confirm judgment row: %v", err)
	}
}

// rejectJudgmentRow performs the machine transition proposed → rejected on a
// stored row, storing the human reason as the resolution.
func rejectJudgmentRow(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	now := nowISO()
	if _, err := db.Exec(`
		UPDATE judgments SET status = 'rejected', resolution = ?, policy_version = ?,
			adjudicator_subject_id = 'subj-1', adjudicator_membership_id = 'mem-1',
			adjudicator_roles_json = '["senior_accountant"]', authentication_method = 'session',
			assurance_level = 'standard', principal_authenticated_at = ?,
			decided_at = ?, updated_at = ?
		WHERE id = ?`,
		"human rejection reason", "judgment-policy/v0.4.0", now, now, now, id,
	); err != nil {
		t.Fatalf("reject judgment row: %v", err)
	}
}

// withdrawJudgmentRow performs the machine transition proposed → withdrawn on a
// stored row. decided_at records the closing timestamp: the schema CHECK
// (status='proposed') = (decided_at IS NULL) requires a closing time on every
// non-proposed row.
func withdrawJudgmentRow(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	now := nowISO()
	if _, err := db.Exec(
		`UPDATE judgments SET status = 'withdrawn', decided_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id,
	); err != nil {
		t.Fatalf("withdraw judgment row: %v", err)
	}
}

// ──────────────────────────────────────────────
// Immutability: the design §4 trigger surface
// ──────────────────────────────────────────────

func TestJudgmentDeleteAborts(t *testing.T) {
	s := newTestStore(t)
	from, to := seedJudgmentContext(t, s)
	insertJudgment(t, s.db, proposedJudgment("j-del", from, to))

	if _, err := s.db.Exec(`DELETE FROM judgments WHERE id = 'j-del'`); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT") {
		t.Fatalf("DELETE on judgments must abort with IMMUTABLE_JUDGMENT, got %v", err)
	}
}

func TestJudgmentConfirmedSupersedeUpdateAllowed(t *testing.T) {
	s := newTestStore(t)
	from, to := seedJudgmentContext(t, s)
	insertJudgment(t, s.db, confirmedJudgment("j-conf", from, to))
	// The successor judgment must exist before the routing FK allows the update.
	insertJudgment(t, s.db, proposedJudgment("j-correction", from, to))

	// The ONLY legal confirmed-row update: status confirmed→superseded while
	// setting a previously-empty supersedes_id; routing fields (updated_at) may
	// change, proposal/adjudication fields stay byte-equal.
	if _, err := s.db.Exec(
		`UPDATE judgments SET status = 'superseded', supersedes_id = ?, updated_at = ? WHERE id = ?`,
		"j-correction", "2026-02-01T12:00:00.000Z", "j-conf",
	); err != nil {
		t.Fatalf("confirmed→superseded routing update must be allowed, got %v", err)
	}

	var status, supersedesID string
	if err := s.db.QueryRow(`SELECT status, supersedes_id FROM judgments WHERE id = 'j-conf'`).Scan(&status, &supersedesID); err != nil {
		t.Fatalf("read superseded row: %v", err)
	}
	if status != "superseded" || supersedesID != "j-correction" {
		t.Fatalf("row = (%s, %s), want (superseded, j-correction)", status, supersedesID)
	}

	// The superseded row is now terminal: no further UPDATE may touch it.
	if _, err := s.db.Exec(
		`UPDATE judgments SET status = 'superseded', supersedes_id = ?, updated_at = ? WHERE id = ?`,
		"j-other", "2026-03-01T12:00:00.000Z", "j-conf",
	); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT") {
		t.Fatalf("UPDATE on a superseded row must abort, got %v", err)
	}
}

func TestJudgmentConfirmedRowUpdateAborts(t *testing.T) {
	s := newTestStore(t)
	from, to := seedJudgmentContext(t, s)
	insertJudgment(t, s.db, confirmedJudgment("j-conf", from, to))

	cases := []struct {
		name string
		stmt string
	}{
		{"change resolution", `UPDATE judgments SET resolution = 'mutated' WHERE id = 'j-conf'`},
		{"change relation", `UPDATE judgments SET relation = 'contradicts' WHERE id = 'j-conf'`},
		{"change proposer", `UPDATE judgments SET proposer_actor_id = 'other-agent' WHERE id = 'j-conf'`},
		{"change adjudicator", `UPDATE judgments SET adjudicator_subject_id = 'other-subject' WHERE id = 'j-conf'`},
		{"change decided_at", `UPDATE judgments SET decided_at = '2026-03-01T12:00:00.000Z' WHERE id = 'j-conf'`},
		{"change proposed_at", `UPDATE judgments SET proposed_at = '2025-01-01T00:00:00.000Z' WHERE id = 'j-conf'`},
		{"status to rejected", `UPDATE judgments SET status = 'rejected' WHERE id = 'j-conf'`},
		{"status to withdrawn", `UPDATE judgments SET status = 'withdrawn' WHERE id = 'j-conf'`},
		{"status to confirmed noop", `UPDATE judgments SET status = 'confirmed' WHERE id = 'j-conf'`},
		{"supersede with mutated resolution", `UPDATE judgments SET status = 'superseded', supersedes_id = 'j-x', resolution = 'mutated' WHERE id = 'j-conf'`},
		{"supersede without supersedes_id", `UPDATE judgments SET status = 'superseded' WHERE id = 'j-conf'`},
		{"set supersedes_id while confirmed", `UPDATE judgments SET supersedes_id = 'j-x' WHERE id = 'j-conf'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.db.Exec(tc.stmt); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT") {
				t.Fatalf("UPDATE %q on a confirmed row must abort with IMMUTABLE_JUDGMENT, got %v", tc.name, err)
			}
		})
	}

	// The row is untouched by every aborted UPDATE.
	var status, resolution string
	if err := s.db.QueryRow(`SELECT status, resolution FROM judgments WHERE id = 'j-conf'`).Scan(&status, &resolution); err != nil {
		t.Fatalf("read confirmed row: %v", err)
	}
	if status != "confirmed" || resolution != "professional resolution" {
		t.Fatalf("confirmed row was mutated by aborted updates: (%s, %s)", status, resolution)
	}
}

func TestJudgmentTerminalRowUpdateAborts(t *testing.T) {
	s := newTestStore(t)
	from, to := seedJudgmentContext(t, s)

	// rejected
	rejected := proposedJudgment("j-rej", from, to)
	rejected.Status = core.JudgmentRejected
	rejected.Resolution = "human rejection reason"
	rejected.PolicyVersion = "judgment-policy/v0.4.0"
	rejected.DecidedAt = testT
	rejected.Adjudicator = &auth.PrincipalSnapshot{
		SubjectID:            "subj-1",
		MembershipID:         "mem-1",
		Roles:                []auth.AccountingRole{auth.RoleSeniorAccountant},
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      testT,
	}
	insertJudgment(t, s.db, rejected)

	// withdrawn
	withdrawn := proposedJudgment("j-wd", from, to)
	withdrawn.Status = core.JudgmentWithdrawn
	withdrawn.DecidedAt = testT
	insertJudgment(t, s.db, withdrawn)

	// superseded (routed via the single legal confirmed update; the successor
	// judgment must exist before the routing FK allows the update)
	insertJudgment(t, s.db, confirmedJudgment("j-ss", from, to))
	insertJudgment(t, s.db, proposedJudgment("j-x", from, to))
	if _, err := s.db.Exec(
		`UPDATE judgments SET status = 'superseded', supersedes_id = 'j-x', updated_at = ? WHERE id = 'j-ss'`,
		nowISO(),
	); err != nil {
		t.Fatalf("route confirmed→superseded: %v", err)
	}

	for _, tc := range []struct {
		name string
		stmt string
	}{
		{"rejected reopens to confirmed", `UPDATE judgments SET status = 'confirmed', resolution = 'x', policy_version = 'judgment-policy/v0.4.0', adjudicator_subject_id = 'subj-1', adjudicator_membership_id = 'mem-1', decided_at = ? WHERE id = 'j-rej'`},
		{"rejected touches resolution", `UPDATE judgments SET resolution = 'mutated' WHERE id = 'j-rej'`},
		{"withdrawn reopens to proposed", `UPDATE judgments SET status = 'proposed', decided_at = NULL WHERE id = 'j-wd'`},
		{"withdrawn touches proposer", `UPDATE judgments SET proposer_actor_id = 'other' WHERE id = 'j-wd'`},
		{"superseded reopens", `UPDATE judgments SET status = 'confirmed' WHERE id = 'j-ss'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.db.Exec(tc.stmt, nowISO()); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT") {
				t.Fatalf("UPDATE %q on a terminal row must abort with IMMUTABLE_JUDGMENT, got %v", tc.name, err)
			}
		})
	}
}

func TestJudgmentProposedRowUpdateAllowed(t *testing.T) {
	// The machine's work area: a proposed row may be transitioned (proposed →
	// confirmed) — the trigger must not lock the legitimate state-machine
	// updates the atomic adjudication commit performs.
	s := newTestStore(t)
	from, to := seedJudgmentContext(t, s)
	insertJudgment(t, s.db, proposedJudgment("j-prop", from, to))

	confirmJudgmentRow(t, s.db, "j-prop")

	var status string
	if err := s.db.QueryRow(`SELECT status FROM judgments WHERE id = 'j-prop'`).Scan(&status); err != nil {
		t.Fatalf("read transitioned row: %v", err)
	}
	if status != "confirmed" {
		t.Fatalf("status = %s, want confirmed", status)
	}
}

func TestJudgmentEventsRejectUpdateAndDelete(t *testing.T) {
	s := newTestStore(t)
	from, to := seedJudgmentContext(t, s)
	insertJudgment(t, s.db, proposedJudgment("j-ev", from, to))

	if _, err := s.db.Exec(`
		INSERT INTO judgment_events (
			id, judgment_id, request_id, action, from_status, to_status, judgment_hash,
			principal_snapshot_json, policy_version, reason, created_at
		) VALUES (?, ?, ?, 'withdraw', 'proposed', 'withdrawn', ?, NULL, NULL, '', ?)`,
		"je-1", "j-ev", "req-1", "hash-1", nowISO(),
	); err != nil {
		t.Fatalf("seed judgment event: %v", err)
	}

	if _, err := s.db.Exec(`UPDATE judgment_events SET reason = 'mutated' WHERE id = 'je-1'`); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT_EVENT") {
		t.Fatalf("UPDATE on judgment_events must abort with IMMUTABLE_JUDGMENT_EVENT, got %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM judgment_events WHERE id = 'je-1'`); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_JUDGMENT_EVENT") {
		t.Fatalf("DELETE on judgment_events must abort with IMMUTABLE_JUDGMENT_EVENT, got %v", err)
	}
}

func TestJudgmentEventActionChecks(t *testing.T) {
	s := newTestStore(t)
	from, to := seedJudgmentContext(t, s)
	insertJudgment(t, s.db, proposedJudgment("j-ev", from, to))
	now := nowISO()

	// A confirm event MUST carry the principal snapshot and policy version.
	if _, err := s.db.Exec(`
		INSERT INTO judgment_events (
			id, judgment_id, request_id, action, from_status, to_status, judgment_hash,
			principal_snapshot_json, policy_version, reason, created_at
		) VALUES (?, ?, ?, 'confirm', 'proposed', 'confirmed', ?, NULL, NULL, '', ?)`,
		"je-bad-snapshot", "j-ev", "req-bad", "hash", now,
	); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("confirm event without a principal snapshot must fail the CHECK, got %v", err)
	}

	// The action/status CHECKs mirror the transition table: confirm never lands
	// in 'withdrawn'.
	if _, err := s.db.Exec(`
		INSERT INTO judgment_events (
			id, judgment_id, request_id, action, from_status, to_status, judgment_hash,
			principal_snapshot_json, policy_version, reason, created_at
		) VALUES (?, ?, ?, 'confirm', 'proposed', 'withdrawn', ?, '{}', 'judgment-policy/v0.4.0', '', ?)`,
		"je-bad-status", "j-ev", "req-bad", "hash", now,
	); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("confirm event with a non-confirm target must fail the CHECK, got %v", err)
	}
}

func TestJudgmentOpenTuplePartialUniqueIndex(t *testing.T) {
	// Design §3 rule 6: at most ONE open (proposed) proposal per
	// (tenant, company, from, to, relation) tuple; once the first is resolved
	// (confirmed | rejected | withdrawn) a new proposal for the tuple is legal.
	for _, tc := range []struct {
		name   string
		status core.JudgmentStatus
	}{
		{"confirmed", core.JudgmentConfirmed},
		{"rejected", core.JudgmentRejected},
		{"withdrawn", core.JudgmentWithdrawn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			from, to := seedJudgmentContext(t, s)
			insertJudgment(t, s.db, proposedJudgment("j-1", from, to))

			// A second OPEN proposal for the same tuple is rejected.
			second := proposedJudgment("j-2", from, to)
			if err := insertJudgmentRaw(s.db, second); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
				t.Fatalf("second open proposal must be rejected by uq_judgment_open_tuple, got %v", err)
			}

			switch tc.status {
			case core.JudgmentConfirmed:
				confirmJudgmentRow(t, s.db, "j-1")
			case core.JudgmentRejected:
				rejectJudgmentRow(t, s.db, "j-1")
			case core.JudgmentWithdrawn:
				withdrawJudgmentRow(t, s.db, "j-1")
			}

			// Resolved tuples are no longer constrained: a new open proposal for
			// the same (tenant, company, from, to, relation) is legal.
			insertJudgment(t, s.db, proposedJudgment("j-3", from, to))
		})
	}
}

func TestJudgmentSchemaChecks(t *testing.T) {
	// The verbatim design CHECKs guard row integrity beyond the triggers.
	s := newTestStore(t)
	from, to := seedJudgmentContext(t, s)

	// A proposal must concern two distinct observations (from_id <> to_id).
	if err := insertJudgmentRaw(s.db, proposedJudgment("j-same", from, from)); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("from_id = to_id must fail the CHECK, got %v", err)
	}

	// A non-proposed status requires decided_at ((status='proposed') =
	// (decided_at IS NULL)).
	noDecided := proposedJudgment("j-nod", from, to)
	noDecided.Status = core.JudgmentWithdrawn
	if err := insertJudgmentRaw(s.db, noDecided); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("withdrawn without decided_at must fail the CHECK, got %v", err)
	}

	// A confirmed judgment requires an adjudicator subject.
	noAdjudicator := confirmedJudgment("j-noadj", from, to)
	noAdjudicator.Adjudicator = nil
	if err := insertJudgmentRaw(s.db, noAdjudicator); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("confirmed without adjudicator_subject_id must fail the CHECK, got %v", err)
	}

	// The idempotency CHECK mirrors approval: event and result are set together.
	insertJudgment(t, s.db, proposedJudgment("j-ido", from, to))
	if _, err := s.db.Exec(`
		INSERT INTO judgment_idempotency_keys (
			tenant_id, request_id, command_hash, actor_binding, result_json,
			judgment_event_id, created_at, completed_at
		) VALUES (?, ?, ?, ?, '{}', NULL, ?, NULL)`,
		testOrgID, "req-1", "cmd-hash", "agent-1", nowISO(),
	); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("result_json without an event must fail the CHECK, got %v", err)
	}
}

func TestJudgmentRelationsTableRoutesSupersessionOnly(t *testing.T) {
	// judgment_relations is the supersession router: relation is frozen to
	// 'supersedes' and judgment ids never enter the observation relations table.
	s := newTestStore(t)
	from, to := seedJudgmentContext(t, s)
	insertJudgment(t, s.db, confirmedJudgment("j-a", from, to))
	insertJudgment(t, s.db, proposedJudgment("j-b", from, to))

	if _, err := s.db.Exec(`
		INSERT INTO judgment_relations (from_judgment_id, to_judgment_id, relation, actor, timestamp)
		VALUES (?, ?, ?, ?, ?)`,
		"j-a", "j-b", "supersedes", "subj-1", nowISO(),
	); err != nil {
		t.Fatalf("insert supersedes routing row: %v", err)
	}

	// A non-supersedes relation is rejected by the CHECK.
	if _, err := s.db.Exec(`
		INSERT INTO judgment_relations (from_judgment_id, to_judgment_id, relation, actor, timestamp)
		VALUES (?, ?, ?, ?, ?)`,
		"j-a", "j-b", "supports", "subj-1", nowISO(),
	); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("non-supersedes judgment relation must fail the CHECK, got %v", err)
	}

	// The pair is the primary key: a duplicate pair is rejected.
	if _, err := s.db.Exec(`
		INSERT INTO judgment_relations (from_judgment_id, to_judgment_id, relation, actor, timestamp)
		VALUES (?, ?, ?, ?, ?)`,
		"j-a", "j-b", "supersedes", "other", nowISO(),
	); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("duplicate judgment relation pair must be rejected, got %v", err)
	}

	// Judgment ids never leak into the observation relations table.
	_, err := s.db.Exec(`INSERT INTO relations (from_id, to_id, relation, actor, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"j-a", "j-b", "supersedes", "subj-1", nowISO())
	if err == nil {
		t.Fatal("judgment ids must never be insertable into the observation relations table")
	}
}
