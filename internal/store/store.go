// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the storage adapter for the
// v2 AccountingMemory model. The model has no monetary fields (content is
// structured text; Materiality is an optional int64-cents threshold that is
// stored verbatim, never computed), so no money value is computed here.
//
// SQLite memory store — immutable revision history, scope-partitioned.
// Implements the storage surface of contracts/memory.md (frozen-for-0.1
// semantics) on modernc.org/sqlite (pure Go, no CGO) per ADR-001 (v0.2 local
// SQLite) and ADR-002 (fail-closed corruption behavior, additive migrations).
// It mirrors store/memory-store.ts semantically.
//
// v2 semantics:
//   - Upsert by (topicKey, exact scope): each Save creates a NEW revision and
//     NEVER edits a stored memory in place. The previous current revision is
//     superseded (core.SupersedePrev, supersedes_id = new id) when it is in a
//     supersedable state (active | pending_review | approved); a TERMINAL
//     previous revision (rejected/superseded/voided) stays terminal — history
//     never re-opens, and the new revision simply becomes current.
//   - Immutability is enforced at the schema level: an UPDATE that touches any
//     column other than status / supersedes_id / authority_status aborts, and
//     DELETE aborts — a corrupt or buggy caller cannot mutate history.
//   - ApplyStatusTransition is the single status-only mutation the lifecycle
//     machine may perform; content/scope/source stay immutable. Legality of
//     transitions is enforced by internal/core/lifecycle.go, not re-derived
//     here (the store persists; the machine decides).
//   - The v1 columns (type, authority_status, actor, timestamp, source, session)
//     are KEPT and maintained for legacy reads: every v2 write also writes
//     type ← string(kind) and authority_status ← legacyStatusFor(status).
//   - EvidenceRefs/RuleRefs grow through dedicated link tables AFTER write
//     (immutability); reads merge the stored refs with the link rows (dedup,
//     stable order: stored refs first, link rows in insertion order).
//   - Write outcomes: created / updated on success; unknown is the documented
//     fallback when an unexpected persistence error occurs — in that case the
//     memory is NOT stored and callers must re-read state before acting on
//     anything. Invalid input fails fast (deterministic caller errors, fail
//     closed).
//
// Migration (v1 → v2, additive, single transaction): adds the v2 columns, drops
// and recreates the immutability trigger, backfills kind/status/fiscal_effect/
// effective_at/recorded_at/source_json/content_hash from the v1 columns via the
// core legacy mappings, creates the evidence_links/rule_links tables and sets
// schema_version=2. The v1 columns are preserved untouched.
//
// Note: the Content `where` field is stored in the DB column `where_text`
// because `where` is a reserved word in SQL; the wire format (JSON) keeps the
// contract name `where` via the json tags in internal/core.

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// schemaVersion is the store layout version (contracts/provenance.md frozen
// migration policy: versioned layout, additive migrations only).
const schemaVersion = 2

// migrationBatchSize chunks the v1→v2 backfill into batched UPDATEs inside the
// single migration transaction.
const migrationBatchSize = 500

// Store is the storage surface consumed by search, lifecycle and the CLI.
// It mirrors the MemoryStore interface of the TypeScript reference.
type Store interface {
	Save(input core.SaveInput) (core.WriteResult, error)
	FindByID(id string) (core.AccountingMemory, bool)
	// FindByTopicKey returns the latest revision of the (topicKey, exact scope)
	// chain, if any.
	FindByTopicKey(topicKey string, scope core.Scope) (core.AccountingMemory, bool)
	// FindChain returns the FULL revision history of the (topicKey, exact scope)
	// chain, ordered by revision ascending.
	FindChain(topicKey string, scope core.Scope) ([]core.AccountingMemory, error)
	// FindByScope returns every stored memory whose scope equals the query
	// scope (full revision history).
	FindByScope(scope core.Scope) ([]core.AccountingMemory, error)
	// List returns every stored memory (full revision history), insertion order.
	List() ([]core.AccountingMemory, error)
	// ListByStatus returns every stored memory with the given v2 status,
	// insertion order.
	ListByStatus(status core.MemoryStatus) ([]core.AccountingMemory, error)
	Relate(fromID, toID string, relation core.Relation, meta *core.RelationMeta) error
	// RelationBetween returns the relation recorded from fromID to toID (the
	// first matching row in insertion order), if any.
	RelationBetween(fromID, toID string) (string, bool)
	// SuccessorOf returns the successor of a superseded memory (routes readers
	// onward).
	SuccessorOf(memoryID string) (core.AccountingMemory, bool)
	// SupersedeExplicit marks a memory superseded routing readers to a successor
	// in one transaction (status + supersedes_id + audit + relation).
	SupersedeExplicit(memoryID, successorID string, meta core.TransitionMeta) (core.AccountingMemory, error)
	// ImportObservation imports a verbatim memory: true when inserted, false when
	// an identical id already exists, IMPORT_CONFLICT when the id exists with
	// different immutable bytes (sync surfaces it, never overwrites).
	ImportObservation(memory core.AccountingMemory) (bool, error)
	// ImportTransition imports a verbatim audit record: true when inserted, false
	// when an identical record already exists.
	ImportTransition(record core.StatusTransitionRecord) (bool, error)
	// ApplyImportedStatus advances status WITHOUT logging the audit row (the row
	// is imported separately) — sync-only, forward-only by contract.
	ApplyImportedStatus(memoryID string, to core.MemoryStatus, meta core.TransitionMeta) (core.AccountingMemory, error)
	// ApplyStatusTransition is the status-only lifecycle mutation; it records an
	// audit-trail entry (actor + actorKind).
	ApplyStatusTransition(memoryID string, to core.MemoryStatus, meta core.TransitionMeta) (core.AccountingMemory, error)
	// AddEvidenceLink attaches an evidence reference to a memory AFTER write,
	// without mutating the immutable memory (dedicated link table). A duplicate
	// (memoryID, ref) is a no-op.
	AddEvidenceLink(memoryID, ref, actor string) error
	// EvidenceRefs returns the full evidence list for a memory: stored refs +
	// linked refs, deduped, stable order.
	EvidenceRefs(memoryID string) ([]string, error)
	// AddRuleLink attaches a rule/policy reference to a memory AFTER write
	// (rule_links table). A duplicate (memoryID, ref) is a no-op.
	AddRuleLink(memoryID, ref, actor string) error
	// RuleRefs returns the full rule list for a memory: stored refs + linked
	// refs, deduped, stable order.
	RuleRefs(memoryID string) ([]string, error)
	Relations() ([]core.RelationRecord, error)
	TransitionLog() ([]core.StatusTransitionRecord, error)
	Doctor() (DoctorReport, error)
	Close() error
}

// SQLiteStore is a Store backed by a local SQLite database (modernc.org/sqlite,
// pure Go). It is safe for the single-writer CLI/daemon pattern this slice
// targets; concurrent writers are serialized through a single connection.
type SQLiteStore struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies the
// versioned schema. A v1 store is migrated additively (single transaction); a
// corrupt or unsupported store fails closed: it never fabricates data
// (contracts/provenance.md frozen policy).
func Open(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	for _, pragma := range []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}

	if err := applySchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	// Fail closed on an unknown layout (provenance.md migration policy). A v1
	// store is migrated additively in ONE transaction before use.
	version, err := readSchemaVersion(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if version == 1 {
		if err := migrateV1ToV2(db); err != nil {
			_ = db.Close()
			return nil, err
		}
		version = schemaVersion
	}
	if version != schemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("unsupported store layout: schema_version=%d, supported=%d — fail closed; migrate additively, never rewrite", version, schemaVersion)
	}

	return &SQLiteStore{db: db}, nil
}

// Close releases the underlying database.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// ──────────────────────────────────────────────
// Schema
// ──────────────────────────────────────────────

const schemaDDL = `
CREATE TABLE IF NOT EXISTS schema_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT OR IGNORE INTO schema_meta (key, value) VALUES ('schema_version', '2');

CREATE TABLE IF NOT EXISTS observations (
    id               TEXT PRIMARY KEY,
    topic_key        TEXT NOT NULL,
    title            TEXT NOT NULL,
    type             TEXT NOT NULL DEFAULT '',
    kind             TEXT NOT NULL,
    scope_kind       TEXT NOT NULL,
    organization_id  TEXT NOT NULL DEFAULT '',
    company_id       TEXT NOT NULL DEFAULT '',
    ruc              TEXT NOT NULL DEFAULT '',
    period           TEXT NOT NULL DEFAULT '',
    what             TEXT NOT NULL,
    why              TEXT NOT NULL,
    where_text       TEXT NOT NULL,
    learned          TEXT NOT NULL,
    authority_status TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL,
    fiscal_effect    TEXT NOT NULL DEFAULT 'none',
    effective_at     TEXT NOT NULL DEFAULT '',
    recorded_at      TEXT NOT NULL,
    observed_at      TEXT NOT NULL DEFAULT '',
    expires_at       TEXT NOT NULL DEFAULT '',
    actor            TEXT NOT NULL DEFAULT '',
    timestamp        TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL DEFAULT '',
    session          TEXT NOT NULL DEFAULT '',
    source_json      TEXT NOT NULL DEFAULT '',
    content_hash     TEXT NOT NULL,
    evidence_refs_json TEXT NOT NULL DEFAULT '[]',
    rule_refs_json     TEXT NOT NULL DEFAULT '[]',
    confidence       REAL,
    materiality      INTEGER,

    receipt_id       TEXT NOT NULL DEFAULT '',

    supersedes_id    TEXT NOT NULL DEFAULT '',
    revision         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_observations_chain
    ON observations (topic_key, scope_kind, organization_id, company_id, ruc, period, revision DESC);
CREATE INDEX IF NOT EXISTS idx_observations_scope
    ON observations (scope_kind, organization_id, company_id, ruc, period);

CREATE TABLE IF NOT EXISTS relations (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id   TEXT NOT NULL REFERENCES observations(id),
    to_id     TEXT NOT NULL REFERENCES observations(id),
    relation  TEXT NOT NULL,
    actor     TEXT NOT NULL DEFAULT '',
    timestamp TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_relations_from ON relations (from_id);

CREATE TABLE IF NOT EXISTS transition_log (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    observation_id TEXT NOT NULL REFERENCES observations(id),
    from_status    TEXT NOT NULL,
    to_status      TEXT NOT NULL,
    actor          TEXT NOT NULL,
    actor_kind     TEXT NOT NULL DEFAULT '',
    timestamp      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_transition_log_obs ON transition_log (observation_id);

CREATE TABLE IF NOT EXISTS evidence_links (
    memory_id  TEXT NOT NULL REFERENCES observations(id),
    ref        TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    timestamp  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (memory_id, ref)
);
CREATE INDEX IF NOT EXISTS idx_evidence_links_memory ON evidence_links (memory_id);

CREATE TABLE IF NOT EXISTS rule_links (
    memory_id  TEXT NOT NULL REFERENCES observations(id),
    ref        TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    timestamp  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (memory_id, ref)
);
CREATE INDEX IF NOT EXISTS idx_rule_links_memory ON rule_links (memory_id);

-- Immutable history (contracts/memory.md rule 3, provenance.md rule 1):
-- content, scope and source never change after write. The ONLY mutable columns
-- are status and supersedes_id (lifecycle) and the legacy authority_status
-- mirror. An UPDATE touching any other column aborts; a DELETE aborts.
-- NOTE: on a v1 store the OLD trigger definition survives this statement (IF
-- NOT EXISTS); migrateV1ToV2 drops it and installs this v2 guard after the
-- column backfill.
CREATE TRIGGER IF NOT EXISTS observations_immutable_content
BEFORE UPDATE OF id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
                     what, why, where_text, learned, fiscal_effect, effective_at, recorded_at, observed_at,
                     expires_at, actor, timestamp, source, session, source_json, content_hash,
                     evidence_refs_json, rule_refs_json, confidence, materiality, revision ON observations
BEGIN
    SELECT RAISE(ABORT, 'IMMUTABLE_OBSERVATION: content, scope and provenance never change after write');
END;

CREATE TRIGGER IF NOT EXISTS observations_no_delete
BEFORE DELETE ON observations
BEGIN
    SELECT RAISE(ABORT, 'IMMUTABLE_OBSERVATION: history is never deleted');
END;
`

func applySchema(db *sql.DB) error {
	if _, err := db.Exec(schemaDDL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

func readSchemaVersion(db *sql.DB) (int, error) {
	var raw string
	err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&raw)
	if err != nil {
		return 0, fmt.Errorf("corrupt store: schema_version unreadable: %w", err)
	}
	version, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("corrupt store: schema_version %q is not an integer", raw)
	}
	return version, nil
}

// ──────────────────────────────────────────────
// v1 → v2 additive migration (single transaction, fail closed)
// ──────────────────────────────────────────────

// migrateV1ToV2 upgrades a schema_version=1 store to v2 IN ONE TRANSACTION:
// the v2 columns are added (effective_at already exists in v1 and is reused),
// the v1 immutability trigger is replaced by the v2 guard, every row is
// backfilled via the core legacy mappings, the link tables are created, and
// schema_version=2 is set only after the whole migration succeeds. On any
// failure the transaction rolls back and the store stays v1.
func migrateV1ToV2(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate v1→v2: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (a) ALTER TABLE observations — add every missing v2 column (effective_at
	// already exists in v1, so it is intentionally not re-added).
	existing, err := tableColumns(ctx, tx, "observations")
	if err != nil {
		return fmt.Errorf("migrate v1→v2: read observations columns: %w", err)
	}
	added := []struct {
		column string
		kind   string
	}{
		{"kind", "TEXT"}, {"status", "TEXT"}, {"fiscal_effect", "TEXT"},
		{"recorded_at", "TEXT"}, {"observed_at", "TEXT"},
		{"source_json", "TEXT"}, {"content_hash", "TEXT"},
		{"evidence_refs_json", "TEXT"}, {"rule_refs_json", "TEXT"},
		{"receipt_id", "TEXT"},
		{"confidence", "REAL"}, {"materiality", "INTEGER"}, {"supersedes_id", "TEXT"},
	}
	for _, add := range added {
		if existing[add.column] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE observations ADD COLUMN `+add.column+` `+add.kind); err != nil {
			return fmt.Errorf("migrate v1→v2: add column %s: %w", add.column, err)
		}
	}

	// transition_log gains the actor_kind column (v2 provenance requirement).
	logColumns, err := tableColumns(ctx, tx, "transition_log")
	if err != nil {
		return fmt.Errorf("migrate v1→v2: read transition_log columns: %w", err)
	}
	if !logColumns["actor_kind"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE transition_log ADD COLUMN actor_kind TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate v1→v2: add transition_log.actor_kind: %w", err)
		}
	}

	// Link tables (idempotent — applySchema may already have created them).
	for _, ddl := range []string{evidenceLinksDDL, ruleLinksDDL} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v1→v2: create link table: %w", err)
		}
	}

	// The v1 trigger guards `effective_at` and would abort the backfill — drop
	// it now; the v2 guard is installed after the backfill.
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS observations_immutable_content`); err != nil {
		return fmt.Errorf("migrate v1→v2: drop legacy trigger: %w", err)
	}

	// (b) Backfill — batched UPDATEs derived from the v1 columns via the core
	// legacy mappings. v1 columns are preserved untouched.
	rows, err := tx.QueryContext(ctx, `
		SELECT id, type, authority_status, actor, timestamp, source, session, effective_at,
		       scope_kind, organization_id, company_id, ruc, period,
		       title, what, why, where_text, learned
		FROM observations ORDER BY rowid`)
	if err != nil {
		return fmt.Errorf("migrate v1→v2: read rows: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE observations
		SET kind = ?, status = ?, fiscal_effect = 'none', recorded_at = ?,
		    effective_at = ?, source_json = ?, content_hash = ?
		WHERE id = ?`)
	if err != nil {
		_ = rows.Close()
		return fmt.Errorf("migrate v1→v2: prepare backfill: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	type backfillRow struct {
		id, kind, status, recordedAt, effectiveAt, sourceJSON, contentHash string
	}
	batch := make([]backfillRow, 0, migrationBatchSize)
	flush := func() error {
		for _, r := range batch {
			if _, err := stmt.ExecContext(ctx, r.kind, r.status, r.recordedAt, r.effectiveAt, r.sourceJSON, r.contentHash, r.id); err != nil {
				return fmt.Errorf("migrate v1→v2: backfill row %s: %w", r.id, err)
			}
		}
		batch = batch[:0]
		return nil
	}

	for rows.Next() {
		var (
			id, typ, authorityStatus, actor, timestamp, source, session, effAt string
			scopeKind, orgID, companyID, ruc, period                           string
			title, what, why, whereText, learned                               string
		)
		if err := rows.Scan(&id, &typ, &authorityStatus, &actor, &timestamp, &source, &session, &effAt,
			&scopeKind, &orgID, &companyID, &ruc, &period, &title, &what, &why, &whereText, &learned); err != nil {
			_ = rows.Close()
			return fmt.Errorf("migrate v1→v2: scan row: %w", err)
		}

		kind := core.LegacyTypeToKind(typ)
		status := core.LegacyStatusToStatus(authorityStatus)
		recordedAt := timestamp // provenance.timestamp → RecordedAt
		effectiveAt := effAt    // validity.effectiveAt when present
		if effectiveAt == "" {
			effectiveAt = timestamp
		}
		sourceStruct := core.Source{
			System:    source,
			ActorID:   actor,
			ActorKind: core.ActorKindHuman, // v1 provenance never carried an actor kind; migrated as human-recorded
			Session:   session,
		}
		sourceJSON, err := json.Marshal(sourceStruct)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("migrate v1→v2: marshal source: %w", err)
		}
		memory := core.AccountingMemory{
			Scope: core.Scope{
				Kind:           core.ScopeKind(scopeKind),
				OrganizationID: orgID,
				CompanyID:      companyID,
				RUC:            ruc,
				Period:         period,
			},
			Title:        title,
			Kind:         kind,
			Content:      core.Content{What: what, Why: why, Where: whereText, Learned: learned},
			FiscalEffect: core.FiscalEffectNone,
			EffectiveAt:  effectiveAt,
			Source:       sourceStruct,
		}
		batch = append(batch, backfillRow{
			id:          id,
			kind:        string(kind),
			status:      string(status),
			recordedAt:  recordedAt,
			effectiveAt: effectiveAt,
			sourceJSON:  string(sourceJSON),
			contentHash: core.ComputeContentHash(memory),
		})
		if len(batch) >= migrationBatchSize {
			if err := flush(); err != nil {
				_ = rows.Close()
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("migrate v1→v2: iterate rows: %w", err)
	}
	_ = rows.Close()
	if err := flush(); err != nil {
		return err
	}

	// Install the v2 immutability guard (now that every guarded column exists).
	if _, err := tx.ExecContext(ctx, immutabilityTriggerDDL); err != nil {
		return fmt.Errorf("migrate v1→v2: install v2 trigger: %w", err)
	}

	// (c) schema_version = 2 ONLY after the whole migration succeeded — same
	// transaction, so a failure above rolls everything back.
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = '2' WHERE key = 'schema_version'`); err != nil {
		return fmt.Errorf("migrate v1→v2: set schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate v1→v2: commit: %w", err)
	}
	committed = true
	return nil
}

const evidenceLinksDDL = `
CREATE TABLE IF NOT EXISTS evidence_links (
    memory_id  TEXT NOT NULL REFERENCES observations(id),
    ref        TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    timestamp  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (memory_id, ref)
);
CREATE INDEX IF NOT EXISTS idx_evidence_links_memory ON evidence_links (memory_id);
`

const ruleLinksDDL = `
CREATE TABLE IF NOT EXISTS rule_links (
    memory_id  TEXT NOT NULL REFERENCES observations(id),
    ref        TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    timestamp  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (memory_id, ref)
);
CREATE INDEX IF NOT EXISTS idx_rule_links_memory ON rule_links (memory_id);
`

const immutabilityTriggerDDL = `
CREATE TRIGGER observations_immutable_content
BEFORE UPDATE OF id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
                     what, why, where_text, learned, fiscal_effect, effective_at, recorded_at, observed_at,
                     expires_at, actor, timestamp, source, session, source_json, content_hash,
                     evidence_refs_json, rule_refs_json, confidence, materiality, revision ON observations
BEGIN
    SELECT RAISE(ABORT, 'IMMUTABLE_OBSERVATION: content, scope and provenance never change after write');
END;
`

// tableColumns returns the column-name set of a table (PRAGMA table_info).
func tableColumns(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

// ──────────────────────────────────────────────
// Save — immutable upsert with auto-supersession
// ──────────────────────────────────────────────

// Save upserts under (topicKey, exact scope): the first save creates revision 1
// (outcome created); every later save of the same chain creates a NEW immutable
// revision with revision+1 (outcome updated) and marks the previous current
// revision superseded (core.SupersedePrev, supersedes_id = new id) when it is
// in a supersedable state. A terminal previous revision (rejected/superseded/
// voided) stays terminal — the new revision simply becomes current. Whether the
// content is identical or evolved, every save is a new revision
// (contracts/memory.md frozen semantics — a topic key is a stable handle for
// evolving knowledge, never a unique-content constraint).
func (s *SQLiteStore) Save(input core.SaveInput) (core.WriteResult, error) {
	if strings.TrimSpace(input.TopicKey) == "" {
		return core.WriteResult{}, errors.New("INVALID_TOPIC_KEY: topicKey must be a non-empty string")
	}
	if strings.TrimSpace(input.Title) == "" {
		return core.WriteResult{}, errors.New("INVALID_TITLE: title must be a non-empty string")
	}
	if err := core.AssertValidScope(input.Scope); err != nil {
		return core.WriteResult{}, err
	}
	if err := core.AssertValidContent(input.Content); err != nil {
		return core.WriteResult{}, err
	}
	if err := core.AssertValidSource(input.Source); err != nil {
		return core.WriteResult{}, err
	}
	if !core.IsValidMemoryKind(input.Kind) {
		return core.WriteResult{}, fmt.Errorf("INVALID_KIND: unknown memory kind %q — expected fact|evidence|decision|rule|exception|control|obligation|summary", input.Kind)
	}
	if !core.IsValidFiscalEffect(input.FiscalEffect) {
		return core.WriteResult{}, fmt.Errorf("INVALID_FISCAL_EFFECT: unknown fiscal effect %q — expected none|journal_entry|declaration|closing|adjustment|reclassification|approval|sunat_filing", input.FiscalEffect)
	}
	if err := core.AssertValidValidity(input.Validity); err != nil {
		return core.WriteResult{}, err
	}

	// Status and RecordedAt are derived by the engine (approval gate + clock),
	// never caller-supplied (core.SaveInput contract).
	recordedAt := nowISO()
	if input.EffectiveAt == "" {
		input.EffectiveAt = recordedAt
	}
	status := core.InitialStatus(input.FiscalEffect)

	id, err := newUUID()
	if err != nil {
		return core.WriteResult{}, fmt.Errorf("persistence error: generate id: %w", err)
	}

	// Build the would-be memory up front so the unknown outcome can report it
	// (the memory is NOT stored in that case — mirror of the reference).
	revision := 1
	memory := buildMemory(input, id, revision, status, recordedAt)
	if err := core.AssertValidMemory(memory); err != nil {
		return core.WriteResult{}, err
	}

	// Chain reads and the supersede+insert write share ONE transaction. With
	// MaxOpenConns(1) the full read-modify-write sequence is atomic on the single
	// connection: no other writer can interleave between reading the chain head
	// and inserting the new revision (no TOCTOU, no duplicate revisions). The
	// transaction is committed and ONLY rolled back when the commit did not
	// happen (modernc.org/sqlite hangs the next connection use when Rollback runs
	// on an already-committed transaction).
	ctx := context.Background()
	chainArgs := []any{
		input.TopicKey, string(input.Scope.Kind), input.Scope.OrganizationID,
		input.Scope.CompanyID, input.Scope.RUC, input.Scope.Period,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var (
		maxRev     sql.NullInt64
		prevID     string
		prevStatus core.MemoryStatus
		hasPrev    bool
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(revision) FROM observations
		WHERE topic_key = ? AND scope_kind = ? AND organization_id = ? AND company_id = ? AND ruc = ? AND period = ?`,
		chainArgs...,
	).Scan(&maxRev); err != nil {
		return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: read chain: %w", err)
	}
	if maxRev.Valid {
		revision = int(maxRev.Int64) + 1
		memory.Revision = revision
		var rawStatus string
		if err := tx.QueryRowContext(ctx, `
			SELECT id, status FROM observations
			WHERE topic_key = ? AND scope_kind = ? AND organization_id = ? AND company_id = ? AND ruc = ? AND period = ?
			ORDER BY revision DESC LIMIT 1`,
			chainArgs...,
		).Scan(&prevID, &rawStatus); err != nil {
			return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: read previous revision: %w", err)
		}
		prevStatus = core.MemoryStatus(rawStatus)
		hasPrev = true
	}

	if hasPrev {
		// Supersede the previous current revision (same (topicKey, scope) chain).
		// The supersedes relation is recorded in the SAME transaction, atomic
		// with the status flip (the immutable-history chain is visible in the
		// relation graph).
		prev := core.AccountingMemory{Identity: core.Identity{ID: prevID}, Status: prevStatus}
		if err := core.SupersedePrev(&prev, id); err == nil {
			if _, err := tx.ExecContext(ctx,
				`UPDATE observations SET status = ?, supersedes_id = ? WHERE id = ?`,
				string(prev.Status), prev.SupersedesID, prevID,
			); err != nil {
				return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: supersede previous revision: %w", err)
			}
			// The auto-supersession is a lifecycle transition: it must trace to
			// actor + actorKind + timestamp in the audit trail (lifecycle.md
			// rule 4, provenance.md rule 3) so sync can replay it.
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO transition_log (observation_id, from_status, to_status, actor, actor_kind, timestamp) VALUES (?, ?, ?, ?, ?, ?)`,
				prevID, string(prevStatus), string(core.StatusSuperseded), input.Source.ActorID, string(input.Source.ActorKind), recordedAt,
			); err != nil {
				return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: record supersede audit: %w", err)
			}
		} else if !errors.Is(err, core.ErrInvalidTransition) {
			// Only terminal predecessors are skipped; anything else is a bug.
			return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, err
		}
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO observations (
			id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
			what, why, where_text, learned, authority_status, status, fiscal_effect, effective_at, recorded_at, observed_at,
			expires_at, actor, timestamp, source, session, source_json, content_hash, evidence_refs_json, rule_refs_json,
			confidence, materiality, receipt_id, supersedes_id, revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.TopicKey, memory.Title, string(memory.Kind), string(memory.Kind), string(memory.Scope.Kind),
		memory.Scope.OrganizationID, memory.Scope.CompanyID, memory.Scope.RUC, memory.Scope.Period,
		memory.Content.What, memory.Content.Why, memory.Content.Where, memory.Content.Learned,
		legacyStatusFor(memory.Status), string(memory.Status), string(memory.FiscalEffect),
		memory.EffectiveAt, memory.RecordedAt, memory.ObservedAt,
		validityExpiresAt(memory.Validity),
		memory.Source.ActorID, memory.RecordedAt, memory.Source.System, memory.Source.Session,
		encodeSource(memory.Source), memory.ContentHash, encodeRefs(memory.EvidenceRefs), encodeRefs(memory.RuleRefs),
		nullableFloat(memory.Confidence), nullableInt(memory.Materiality), memory.ReceiptID, memory.SupersedesID,
		revision,
	)
	if err != nil {
		return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: insert: %w", err)
	}
	if hasPrev && prevStatus != core.StatusSuperseded && prevStatus != core.StatusVoided && prevStatus != core.StatusRejected {
		// Record the supersedes relation AFTER the successor exists (FK order):
		// from the superseded predecessor to the new revision, atomic in the tx.
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO relations (from_id, to_id, relation, actor, timestamp) VALUES (?, ?, ?, ?, ?)`,
			prevID, id, string(core.RelationSupersedes), input.Source.ActorID, recordedAt,
		); err != nil {
			return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: record supersedes relation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: commit: %w", err)
	}
	committed = true

	outcome := core.WriteCreated
	if revision > 1 {
		outcome = core.WriteUpdated
	}
	return core.WriteResult{Memory: memory, Outcome: outcome}, nil
}

func buildMemory(input core.SaveInput, id string, revision int, status core.MemoryStatus, recordedAt string) core.AccountingMemory {
	var validity *core.Validity
	if input.Validity != nil {
		v := *input.Validity
		validity = &v
	}
	var confidence *float64
	if input.Confidence != nil {
		c := *input.Confidence
		confidence = &c
	}
	var materiality *int64
	if input.Materiality != nil {
		m := *input.Materiality
		materiality = &m
	}
	memory := core.AccountingMemory{
		Identity:     core.Identity{ID: id, TopicKey: input.TopicKey},
		Title:        input.Title,
		Kind:         input.Kind,
		Scope:        input.Scope,
		Content:      input.Content,
		Status:       status,
		FiscalEffect: input.FiscalEffect,
		EffectiveAt:  input.EffectiveAt,
		RecordedAt:   recordedAt,
		ObservedAt:   input.ObservedAt,
		Source:       input.Source,
		Validity:     validity,
		RuleRefs:     append([]string(nil), input.RuleRefs...),
		Confidence:   confidence,
		Materiality:  materiality,
		ReceiptID:    input.ReceiptID,
		Revision:     revision,
	}
	memory.ContentHash = core.ComputeContentHash(memory)
	return memory
}

// legacyStatusFor maps a v2 status to the v1 authority_status vocabulary for
// legacy reads (active→promoted, pending_review→reviewed, approved→promoted,
// rejected→reviewed, superseded→superseded, voided→reviewed).
func legacyStatusFor(status core.MemoryStatus) string {
	switch status {
	case core.StatusActive:
		return "promoted"
	case core.StatusPendingReview:
		return "reviewed"
	case core.StatusApproved:
		return "promoted"
	case core.StatusRejected:
		return "reviewed"
	case core.StatusSuperseded:
		return "superseded"
	case core.StatusVoided:
		return "reviewed"
	}
	return "reviewed"
}

func validityExpiresAt(v *core.Validity) string {
	if v == nil {
		return ""
	}
	return v.ExpiresAt
}

// encodeRefs serializes a reference list as a JSON array (never nil; the
// scan side normalizes nil to []).
func encodeRefs(refs []string) string {
	if len(refs) == 0 {
		return `[]`
	}
	encoded, err := json.Marshal(refs)
	if err != nil {
		return `[]`
	}
	return string(encoded)
}

func encodeSource(source core.Source) string {
	encoded, err := json.Marshal(source)
	if err != nil {
		// Source fields are plain strings — marshaling cannot fail.
		return ""
	}
	return string(encoded)
}

func nullableFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// ──────────────────────────────────────────────
// Reads
// ──────────────────────────────────────────────

const memoryColumns = `id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
	what, why, where_text, learned, authority_status, status, fiscal_effect, effective_at, recorded_at, observed_at,
	expires_at, actor, timestamp, source, session, source_json, content_hash, evidence_refs_json, rule_refs_json,
	confidence, materiality, receipt_id, supersedes_id, revision`

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanMemory(rs rowScanner) (core.AccountingMemory, error) {
	var (
		id, topicKey, title, typ, scopeKind, orgID, companyID, ruc, period string
		what, why, whereText, learned, authorityStatus, effAt              string
		recordedAt, expiresAt, actor, timestamp, source, session           string
		revision                                                           int
	)
	var (
		kind, status, fiscalEffect, observedAt                  sql.NullString
		sourceJSON, contentHash, evidenceRefsJSON, ruleRefsJSON sql.NullString
		supersedesID, receiptID                                 sql.NullString
		confidence                                              sql.NullFloat64
		materiality                                             sql.NullInt64
	)
	if err := rs.Scan(
		&id, &topicKey, &title, &typ, &kind, &scopeKind, &orgID, &companyID, &ruc, &period,
		&what, &why, &whereText, &learned, &authorityStatus, &status, &fiscalEffect, &effAt, &recordedAt, &observedAt,
		&expiresAt, &actor, &timestamp, &source, &session, &sourceJSON, &contentHash, &evidenceRefsJSON, &ruleRefsJSON,
		&confidence, &materiality, &receiptID, &supersedesID, &revision,
	); err != nil {
		return core.AccountingMemory{}, err
	}

	memoryKind := core.MemoryKind(kind.String)
	if !core.IsValidMemoryKind(memoryKind) {
		// Pre-v2 rows or a legacy write path: derive from the v1 type.
		memoryKind = core.LegacyTypeToKind(typ)
	}
	memoryStatus := core.MemoryStatus(status.String)
	if !core.IsValidMemoryStatus(memoryStatus) {
		memoryStatus = core.LegacyStatusToStatus(authorityStatus)
	}
	fiscalEffectValue := core.FiscalEffect(fiscalEffect.String)
	if !core.IsValidFiscalEffect(fiscalEffectValue) {
		fiscalEffectValue = core.FiscalEffectNone
	}

	memory := core.AccountingMemory{
		Identity:     core.Identity{ID: id, TopicKey: topicKey},
		Title:        title,
		Kind:         memoryKind,
		Scope:        core.Scope{Kind: core.ScopeKind(scopeKind), OrganizationID: orgID, CompanyID: companyID, RUC: ruc, Period: period},
		Content:      core.Content{What: what, Why: why, Where: whereText, Learned: learned},
		Status:       memoryStatus,
		FiscalEffect: fiscalEffectValue,
		EffectiveAt:  effAt,
		RecordedAt:   recordedAt,
		ObservedAt:   observedAt.String,
		Source:       sourceFromJSON(sourceJSON.String, actor, timestamp, source, session),
		ContentHash:  contentHash.String,
		ReceiptID:    receiptID.String,
		SupersedesID: supersedesID.String,
		Revision:     revision,
	}
	if expiresAt != "" {
		memory.Validity = &core.Validity{ExpiresAt: expiresAt}
	}
	if confidence.Valid {
		v := confidence.Float64
		memory.Confidence = &v
	}
	if materiality.Valid {
		v := materiality.Int64
		memory.Materiality = &v
	}
	_ = json.Unmarshal([]byte(evidenceRefsJSON.String), &memory.EvidenceRefs)
	_ = json.Unmarshal([]byte(ruleRefsJSON.String), &memory.RuleRefs)
	if memory.EvidenceRefs == nil {
		memory.EvidenceRefs = []string{}
	}
	if memory.RuleRefs == nil {
		memory.RuleRefs = []string{}
	}
	return memory, nil
}

// sourceFromJSON decodes the v2 source_json column; when absent (legacy rows)
// it falls back to the v1 provenance columns, classifying the actor as human
// (the v1 model never carried an actor kind).
func sourceFromJSON(sourceJSON, actor, timestamp, source, session string) core.Source {
	if sourceJSON != "" {
		var src core.Source
		if err := json.Unmarshal([]byte(sourceJSON), &src); err == nil {
			return src
		}
	}
	return core.Source{
		System:    source,
		ActorID:   actor,
		ActorKind: core.ActorKindHuman,
		Session:   session,
	}
}

// withLinks merges the stored refs with the dedicated link rows (dedup, stable
// order: stored refs first, link rows in insertion order). The stored memory
// row itself is never mutated.
func (s *SQLiteStore) withLinks(memory core.AccountingMemory) core.AccountingMemory {
	memory.EvidenceRefs = mergeRefs(memory.EvidenceRefs, s.linkRefs(`evidence_links`, memory.Identity.ID))
	memory.RuleRefs = mergeRefs(memory.RuleRefs, s.linkRefs(`rule_links`, memory.Identity.ID))
	return memory
}

func mergeRefs(stored, linked []string) []string {
	seen := make(map[string]struct{}, len(stored)+len(linked))
	out := make([]string, 0, len(stored)+len(linked))
	for _, ref := range append(append([]string{}, stored...), linked...) {
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func (s *SQLiteStore) linkRefs(table, memoryID string) []string {
	rows, err := s.db.Query(`SELECT ref FROM `+table+` WHERE memory_id = ? ORDER BY rowid`, memoryID)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	refs := make([]string, 0)
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil
		}
		refs = append(refs, ref)
	}
	return refs
}

// FindByID returns the memory with the given id, if any (evidence/rule links
// merged into the read view).
func (s *SQLiteStore) FindByID(id string) (core.AccountingMemory, bool) {
	row := s.db.QueryRow(`SELECT `+memoryColumns+` FROM observations WHERE id = ?`, id)
	memory, err := scanMemory(row)
	if err != nil {
		return core.AccountingMemory{}, false
	}
	return s.withLinks(memory), true
}

// FindByTopicKey returns the latest revision of the (topicKey, exact scope)
// chain, if any.
func (s *SQLiteStore) FindByTopicKey(topicKey string, scope core.Scope) (core.AccountingMemory, bool) {
	where, args := chainWhere(topicKey, scope)
	row := s.db.QueryRow(`SELECT `+memoryColumns+` FROM observations WHERE `+where+` ORDER BY revision DESC LIMIT 1`, args...)
	memory, err := scanMemory(row)
	if err != nil {
		return core.AccountingMemory{}, false
	}
	return s.withLinks(memory), true
}

// FindByScope returns every stored memory whose scope equals the query scope
// (full revision history), insertion order.
func (s *SQLiteStore) FindByScope(scope core.Scope) ([]core.AccountingMemory, error) {
	where, args := scopeWhere(scope)
	return s.queryMemories(`WHERE `+where+` ORDER BY rowid`, args...)
}

// List returns every stored memory (full revision history), insertion order.
func (s *SQLiteStore) List() ([]core.AccountingMemory, error) {
	return s.queryMemories(`ORDER BY rowid`)
}

// ListByStatus returns every stored memory with the given v2 status, insertion
// order.
func (s *SQLiteStore) ListByStatus(status core.MemoryStatus) ([]core.AccountingMemory, error) {
	if !core.IsValidMemoryStatus(status) {
		return nil, fmt.Errorf("INVALID_STATUS: unknown memory status %q", status)
	}
	return s.queryMemories(`WHERE status = ? ORDER BY rowid`, string(status))
}

func (s *SQLiteStore) queryMemories(suffix string, args ...any) ([]core.AccountingMemory, error) {
	rows, err := s.db.Query(`SELECT `+memoryColumns+` FROM observations `+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	memories := make([]core.AccountingMemory, 0)
	for rows.Next() {
		memory, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, memory)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Close the scan BEFORE resolving links: with MaxOpenConns(1), querying the
	// link tables while this Rows is still open deadlocks on the single
	// connection (the nested query waits for the connection the open Rows holds).
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range memories {
		memories[i] = s.withLinks(memories[i])
	}
	return memories, nil
}

// FindChain returns the FULL revision history of a (topicKey, exact scope)
// chain, ordered by revision ascending — the counterpart of FindByTopicKey
// (which returns only the latest). The HTTP chain surface (GET /v1/chain) uses
// it to serve every revision of a topic key.
func (s *SQLiteStore) FindChain(topicKey string, scope core.Scope) ([]core.AccountingMemory, error) {
	where, args := chainWhere(topicKey, scope)
	return s.queryMemories(`WHERE `+where+` ORDER BY revision ASC`, args...)
}

// chainWhere builds the exact-chain predicate for (topicKey, exact scope).
func chainWhere(topicKey string, scope core.Scope) (string, []any) {
	where, args := scopeWhere(scope)
	return "topic_key = ? AND " + where, append([]any{topicKey}, args...)
}

// scopeWhere builds the exact-scope predicate. Scope equality is exact
// (scope.md rule 5): a perioded scope never matches an unperioded one.
func scopeWhere(scope core.Scope) (string, []any) {
	if scope.Kind == core.ScopeKindInstitutional {
		return `scope_kind = 'institutional'`, nil
	}
	return `scope_kind = 'company' AND organization_id = ? AND company_id = ? AND ruc = ? AND period = ?`,
		[]any{scope.OrganizationID, scope.CompanyID, scope.RUC, scope.Period}
}

// ──────────────────────────────────────────────
// Relations / lifecycle mutations
// ──────────────────────────────────────────────

// Relate records a relation between two existing memories. A duplicate
// (fromId, toId, relation) is a no-op; a memory never relates to itself.
func (s *SQLiteStore) Relate(fromID, toID string, relation core.Relation, meta *core.RelationMeta) error {
	if fromID == toID {
		return errors.New("INVALID_RELATION: a memory cannot relate to itself")
	}
	if !core.IsValidRelation(relation) {
		return fmt.Errorf("INVALID_RELATION: unknown relation %q", relation)
	}
	actor, timestamp := "", ""
	if meta != nil {
		actor, timestamp = meta.Actor, meta.Timestamp
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, id := range []string{fromID, toID} {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM observations WHERE id = ?`, id).Scan(&exists); err != nil {
			return fmt.Errorf("OBSERVATION_NOT_FOUND: %s", id)
		}
	}

	var duplicate int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM relations WHERE from_id = ? AND to_id = ? AND relation = ?`, fromID, toID, string(relation)).Scan(&duplicate)
	if err == nil {
		// Already recorded: no-op, commit the read transaction.
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO relations (from_id, to_id, relation, actor, timestamp) VALUES (?, ?, ?, ?, ?)`,
		fromID, toID, string(relation), actor, timestamp,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// SupersedeExplicit marks memoryID superseded, routing readers to successorID,
// in ONE transaction: the status flip + supersedes_id, the audit-trail entry,
// and the supersedes relation. The caller (API) validates legality via
// core.SupersedePrev before persisting; this is the low-level mutation.
func (s *SQLiteStore) SupersedeExplicit(memoryID, successorID string, meta core.TransitionMeta) (core.AccountingMemory, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.AccountingMemory{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var from string
	err = tx.QueryRowContext(ctx, `SELECT status FROM observations WHERE id = ?`, memoryID).Scan(&from)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.AccountingMemory{}, fmt.Errorf("MEMORY_NOT_FOUND: %s", memoryID)
		}
		return core.AccountingMemory{}, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE observations SET status = ?, authority_status = ?, supersedes_id = ? WHERE id = ?`,
		string(core.StatusSuperseded), legacyStatusFor(core.StatusSuperseded), successorID, memoryID,
	); err != nil {
		return core.AccountingMemory{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transition_log (observation_id, from_status, to_status, actor, actor_kind, timestamp) VALUES (?, ?, ?, ?, ?, ?)`,
		memoryID, from, string(core.StatusSuperseded), meta.Actor, string(meta.ActorKind), meta.Timestamp,
	); err != nil {
		return core.AccountingMemory{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO relations (from_id, to_id, relation, actor, timestamp) VALUES (?, ?, ?, ?, ?)`,
		memoryID, successorID, string(core.RelationSupersedes), meta.Actor, meta.Timestamp,
	); err != nil {
		return core.AccountingMemory{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.AccountingMemory{}, err
	}
	committed = true
	memory, ok := s.FindByID(memoryID)
	if !ok {
		return core.AccountingMemory{}, fmt.Errorf("MEMORY_NOT_FOUND: %s", memoryID)
	}
	return memory, nil
}

// RelationBetween returns the relation recorded from fromID to toID (the first
// matching row in insertion order), if any. Relations are directional: a
// supersedes row is stored from the superseded memory to its replacement, so
// RelationBetween(old, replacement) returns "supersedes" while the reverse pair
// does not.
func (s *SQLiteStore) RelationBetween(fromID, toID string) (string, bool) {
	var relation string
	err := s.db.QueryRow(`SELECT relation FROM relations WHERE from_id = ? AND to_id = ? ORDER BY rowid LIMIT 1`, fromID, toID).Scan(&relation)
	if err != nil {
		return "", false
	}
	return relation, true
}

// SuccessorOf returns the successor of a superseded memory (the first
// `supersedes` relation recorded from it), routing readers onward.
func (s *SQLiteStore) SuccessorOf(memoryID string) (core.AccountingMemory, bool) {
	var toID string
	err := s.db.QueryRow(`SELECT to_id FROM relations WHERE from_id = ? AND relation = 'supersedes' ORDER BY rowid LIMIT 1`, memoryID).Scan(&toID)
	if err != nil {
		return core.AccountingMemory{}, false
	}
	return s.FindByID(toID)
}

// ApplyStatusTransition is the single status-only mutation the lifecycle
// machine may perform; it records an audit-trail entry (actor + actorKind) in
// the same transaction (contracts/provenance.md rule 3) and keeps the legacy
// authority_status mirror in sync. Legality is enforced by the lifecycle
// machine before this call.
func (s *SQLiteStore) ApplyStatusTransition(memoryID string, to core.MemoryStatus, meta core.TransitionMeta) (core.AccountingMemory, error) {
	if !core.IsValidMemoryStatus(to) {
		return core.AccountingMemory{}, fmt.Errorf("INVALID_TRANSITION: unknown target status %q", to)
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.AccountingMemory{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var from string
	err = tx.QueryRowContext(ctx, `SELECT status FROM observations WHERE id = ?`, memoryID).Scan(&from)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.AccountingMemory{}, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", memoryID)
		}
		return core.AccountingMemory{}, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE observations SET status = ?, authority_status = ? WHERE id = ?`, string(to), legacyStatusFor(to), memoryID); err != nil {
		return core.AccountingMemory{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transition_log (observation_id, from_status, to_status, actor, actor_kind, timestamp) VALUES (?, ?, ?, ?, ?, ?)`,
		memoryID, from, string(to), meta.Actor, string(meta.ActorKind), meta.Timestamp,
	); err != nil {
		return core.AccountingMemory{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.AccountingMemory{}, err
	}
	committed = true
	memory, ok := s.FindByID(memoryID)
	if !ok {
		return core.AccountingMemory{}, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", memoryID)
	}
	return memory, nil
}

// ──────────────────────────────────────────────
// Evidence / rule links (immutability-preserving growth)
// ──────────────────────────────────────────────

// ApplyImportedStatus advances status without recording an audit-trail row (the
// row is imported separately by sync). Forward-only by contract: the caller
// (sync) has already validated the advance direction.
func (s *SQLiteStore) ApplyImportedStatus(memoryID string, to core.MemoryStatus, meta core.TransitionMeta) (core.AccountingMemory, error) {
	if !core.IsValidMemoryStatus(to) {
		return core.AccountingMemory{}, fmt.Errorf("INVALID_TRANSITION: unknown target status %q", to)
	}
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE observations SET status = ?, authority_status = ? WHERE id = ?`,
		string(to), legacyStatusFor(to), memoryID,
	); err != nil {
		return core.AccountingMemory{}, err
	}
	memory, ok := s.FindByID(memoryID)
	if !ok {
		return core.AccountingMemory{}, fmt.Errorf("MEMORY_NOT_FOUND: %s", memoryID)
	}
	return memory, nil
}

// ImportObservation imports a verbatim memory into the store (sync transport).
// Idempotent: true when inserted, false when an identical id already exists.
// An id that exists with DIFFERENT immutable bytes is an immutable conflict —
// IMPORT_CONFLICT, surfaced by sync and never overwritten.
func (s *SQLiteStore) ImportObservation(memory core.AccountingMemory) (bool, error) {
	if err := core.AssertValidMemory(memory); err != nil {
		return false, err
	}
	if memory.Identity.ID == "" {
		return false, errors.New("INVALID_ID: imported memory must carry its id")
	}
	if memory.Revision <= 0 {
		return false, errors.New("INVALID_REVISION: imported memory must carry a positive revision")
	}
	ctx := context.Background()
	rows, err := s.db.QueryContext(ctx, `SELECT content_hash FROM observations WHERE id = ?`, memory.Identity.ID)
	if err != nil {
		return false, err
	}
	has := rows.Next()
	var existingHash string
	if has {
		if err := rows.Scan(&existingHash); err != nil {
			_ = rows.Close()
			return false, err
		}
	}
	_ = rows.Close()
	if has {
		if existingHash == memory.ContentHash {
			return false, nil // identical — no-op
		}
		return false, fmt.Errorf("IMPORT_CONFLICT: id %s exists with different immutable bytes", memory.Identity.ID)
	}

	built := core.CloneMemory(memory)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO observations (
			id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
			what, why, where_text, learned, authority_status, status, fiscal_effect, effective_at, recorded_at, observed_at,
			expires_at, actor, timestamp, source, session, source_json, content_hash, evidence_refs_json, rule_refs_json,
			confidence, materiality, receipt_id, supersedes_id, revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		built.Identity.ID, built.Identity.TopicKey, built.Title, string(built.Kind), string(built.Kind), string(built.Scope.Kind),
		built.Scope.OrganizationID, built.Scope.CompanyID, built.Scope.RUC, built.Scope.Period,
		built.Content.What, built.Content.Why, built.Content.Where, built.Content.Learned,
		legacyStatusFor(built.Status), string(built.Status), string(built.FiscalEffect),
		built.EffectiveAt, built.RecordedAt, built.ObservedAt,
		validityExpiresAt(built.Validity),
		built.Source.ActorID, built.RecordedAt, built.Source.System, built.Source.Session,
		encodeSource(built.Source), built.ContentHash, encodeRefs(built.EvidenceRefs), encodeRefs(built.RuleRefs),
		nullableFloat(built.Confidence), nullableInt(built.Materiality), built.ReceiptID, built.SupersedesID,
		built.Revision,
	); err != nil {
		return false, fmt.Errorf("persistence error: import observation: %w", err)
	}
	return true, nil
}

// ImportTransition imports a verbatim audit-trail record (sync transport).
// Idempotent: true when inserted, false when an identical record exists.
func (s *SQLiteStore) ImportTransition(record core.StatusTransitionRecord) (bool, error) {
	// Fail closed on crafted/corrupt audit records (v1 defense restored and
	// adapted to the v2 machine): a non-empty observation id, known statuses,
	// and a LEGAL v2 transition pair. The source's own log is produced by the
	// lifecycle machine, so a record like active→approved can only come from a
	// corrupt/crafted source — it is rejected here so sync never jumps states,
	// bypasses the human gate, or fabricates provenance.
	if strings.TrimSpace(record.MemoryID) == "" {
		return false, errors.New("INVALID_TRANSITION: imported record must carry a memory id")
	}
	if !core.IsValidMemoryStatus(record.From) || !core.IsValidMemoryStatus(record.To) {
		return false, errors.New("INVALID_TRANSITION: imported record has unknown statuses")
	}
	if !core.IsLegalV2Transition(record.From, record.To) {
		return false, fmt.Errorf("INVALID_TRANSITION: %s → %s is not a legal v2 lifecycle transition", record.From, record.To)
	}
	ctx := context.Background()
	rows, err := s.db.QueryContext(ctx,
		`SELECT 1 FROM transition_log WHERE observation_id = ? AND from_status = ? AND to_status = ? AND actor = ? AND actor_kind = ? AND timestamp = ?`,
		record.MemoryID, string(record.From), string(record.To), record.Actor, string(record.ActorKind), record.Timestamp,
	)
	if err != nil {
		return false, err
	}
	has := rows.Next()
	_ = rows.Close()
	if has {
		return false, nil
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO transition_log (observation_id, from_status, to_status, actor, actor_kind, timestamp) VALUES (?, ?, ?, ?, ?, ?)`,
		record.MemoryID, string(record.From), string(record.To), record.Actor, string(record.ActorKind), record.Timestamp,
	); err != nil {
		return false, fmt.Errorf("persistence error: import transition: %w", err)
	}
	return true, nil
}

// AddEvidenceLink attaches an evidence reference to a memory AFTER write; the
// immutable memory row is never mutated (dedicated evidence_links table). A
// duplicate (memoryID, ref) is a no-op.
func (s *SQLiteStore) AddEvidenceLink(memoryID, ref, actor string) error {
	return s.addLink(`evidence_links`, memoryID, ref, actor)
}

// AddRuleLink attaches a rule/policy reference to a memory AFTER write
// (rule_links table). A duplicate (memoryID, ref) is a no-op.
func (s *SQLiteStore) AddRuleLink(memoryID, ref, actor string) error {
	return s.addLink(`rule_links`, memoryID, ref, actor)
}

func (s *SQLiteStore) addLink(table, memoryID, ref, actor string) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("INVALID_REF: ref must be a non-empty string")
	}
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM observations WHERE id = ?`, memoryID).Scan(&exists); err != nil {
		return fmt.Errorf("OBSERVATION_NOT_FOUND: %s", memoryID)
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO `+table+` (memory_id, ref, actor, timestamp) VALUES (?, ?, ?, ?)`,
		memoryID, ref, actor, nowISO(),
	)
	if err != nil {
		return fmt.Errorf("persistence error: add link: %w", err)
	}
	return nil
}

// EvidenceRefs returns the full evidence list for a memory: stored refs + linked
// refs, deduped, stable order (stored first, links in insertion order).
func (s *SQLiteStore) EvidenceRefs(memoryID string) ([]string, error) {
	memory, ok := s.FindByID(memoryID)
	if !ok {
		return nil, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", memoryID)
	}
	return memory.EvidenceRefs, nil
}

// RuleRefs returns the full rule list for a memory: stored refs + linked refs,
// deduped, stable order.
func (s *SQLiteStore) RuleRefs(memoryID string) ([]string, error) {
	memory, ok := s.FindByID(memoryID)
	if !ok {
		return nil, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", memoryID)
	}
	return memory.RuleRefs, nil
}

// Relations returns every recorded relation, insertion order.
func (s *SQLiteStore) Relations() ([]core.RelationRecord, error) {
	rows, err := s.db.Query(`SELECT from_id, to_id, relation, actor, timestamp FROM relations ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	records := make([]core.RelationRecord, 0)
	for rows.Next() {
		var (
			fromID, toID, relation, actor, timestamp string
		)
		if err := rows.Scan(&fromID, &toID, &relation, &actor, &timestamp); err != nil {
			return nil, err
		}
		records = append(records, core.RelationRecord{
			FromID:    fromID,
			ToID:      toID,
			Relation:  core.Relation(relation),
			Actor:     actor,
			Timestamp: timestamp,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// TransitionLog returns every lifecycle audit-trail entry, insertion order.
func (s *SQLiteStore) TransitionLog() ([]core.StatusTransitionRecord, error) {
	rows, err := s.db.Query(`SELECT observation_id, from_status, to_status, actor, actor_kind, timestamp FROM transition_log ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	records := make([]core.StatusTransitionRecord, 0)
	for rows.Next() {
		var (
			observationID, from, to, actor, actorKind, timestamp string
		)
		if err := rows.Scan(&observationID, &from, &to, &actor, &actorKind, &timestamp); err != nil {
			return nil, err
		}
		records = append(records, core.StatusTransitionRecord{
			MemoryID:  observationID,
			From:      core.MemoryStatus(from),
			To:        core.MemoryStatus(to),
			Actor:     actor,
			ActorKind: core.ActorKind(actorKind),
			Timestamp: timestamp,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// ──────────────────────────────────────────────
// Doctor — schema check + counts (fail closed)
// ──────────────────────────────────────────────

// DoctorReport is the store health snapshot reported by the doctor command.
type DoctorReport struct {
	SchemaVersion    int    `json:"schemaVersion"`
	Storage          string `json:"storage"`
	DBPath           string `json:"dbPath"`
	Observations     int    `json:"observations"`
	RevisionChains   int    `json:"revisionChains"`
	Transitions      int    `json:"transitions"`
	Relations        int    `json:"relations"`
	EvidenceLinks    int    `json:"evidenceLinks"`
	RuleLinks        int    `json:"ruleLinks"`
	PendingApprovals int    `json:"pendingApprovals"`
}

// Doctor verifies the schema (fail closed on corruption) and reports counts.
func (s *SQLiteStore) Doctor() (DoctorReport, error) {
	// Fail closed: every expected table and the immutability guard must exist.
	for _, table := range []string{"schema_meta", "observations", "relations", "transition_log", "evidence_links", "rule_links"} {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			return DoctorReport{}, fmt.Errorf("corrupt store: expected table %q is missing: %w", table, err)
		}
	}
	for _, trigger := range []string{"observations_no_delete", "observations_immutable_content"} {
		var triggerName string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&triggerName); err != nil {
			return DoctorReport{}, fmt.Errorf("corrupt store: immutability trigger %q missing: %w", trigger, err)
		}
	}

	version, err := readSchemaVersion(s.db)
	if err != nil {
		return DoctorReport{}, err
	}

	report := DoctorReport{
		SchemaVersion: version,
		Storage:       "sqlite (modernc.org/sqlite)",
		DBPath:        s.dbPath(),
	}
	counts := []struct {
		query string
		dst   *int
	}{
		{`SELECT COUNT(*) FROM observations`, &report.Observations},
		{`SELECT COUNT(*) FROM (SELECT topic_key, scope_kind, organization_id, company_id, ruc, period FROM observations GROUP BY 1, 2, 3, 4, 5, 6)`, &report.RevisionChains},
		{`SELECT COUNT(*) FROM transition_log`, &report.Transitions},
		{`SELECT COUNT(*) FROM relations`, &report.Relations},
		{`SELECT COUNT(*) FROM evidence_links`, &report.EvidenceLinks},
		{`SELECT COUNT(*) FROM rule_links`, &report.RuleLinks},
		{`SELECT COUNT(*) FROM observations WHERE status = 'pending_review'`, &report.PendingApprovals},
	}
	for _, c := range counts {
		if err := s.db.QueryRow(c.query).Scan(c.dst); err != nil {
			return DoctorReport{}, fmt.Errorf("corrupt store: count failed: %w", err)
		}
	}
	return report, nil
}

func (s *SQLiteStore) dbPath() string {
	var path string
	// modernc.org/sqlite exposes the file path via PRAGMA database_list.
	rows, err := s.db.Query(`PRAGMA database_list`)
	if err != nil {
		return ""
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return ""
		}
		if name == "main" {
			path = file
		}
	}
	return path
}

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

// nowISO is the store's event timestamp: current UTC time in RFC3339, which the
// core timestamp grammar accepts (contracts/provenance.md rule 3).
func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// newUUID returns a random (v4) UUID, mirroring the reference randomUUID.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
