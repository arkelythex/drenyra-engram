// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the storage adapter for the
// observation model; the model has no monetary fields (content is structured
// text), so no money value is persisted or computed here.
//
// SQLite memory store — immutable revision history, scope-partitioned.
// Implements the storage surface of contracts/memory.md (frozen-for-0.1
// semantics) on modernc.org/sqlite (pure Go, no CGO) per ADR-001 (v0.2 local
// SQLite) and ADR-002 (fail-closed corruption behavior, additive migrations).
// It mirrors store/memory-store.ts semantically.
//
// Semantics:
//   - Upsert by (topicKey, exact scope): each Save creates a NEW revision and
//     NEVER edits a stored observation in place. History stays retrievable by id.
//   - Immutability is enforced at the schema level: an UPDATE that touches any
//     column other than authority_status aborts, and DELETE aborts — a corrupt
//     or buggy caller cannot mutate history.
//   - ApplyStatusTransition is the single status-only mutation the lifecycle
//     machine may perform; content/scope/provenance stay immutable. Legality of
//     transitions is enforced by internal/core/lifecycle.go, not re-derived
//     here.
//   - Write outcomes: created / updated on success; unknown is the documented
//     fallback when an unexpected persistence error occurs — in that case the
//     observation is NOT stored and callers must re-read state before acting on
//     anything. Invalid input fails fast (deterministic caller errors, fail
//     closed).
//   - conflict is reserved for a future optimistic-concurrency slice and is
//     never produced by plain sequential saves.
//
// Note: the Content `where` field is stored in the DB column `where_text`
// because `where` is a reserved word in SQL; the wire format (JSON) keeps the
// contract name `where` via the json tags in internal/core.

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// schemaVersion is the store layout version (contracts/provenance.md frozen
// migration policy: versioned layout, additive migrations only).
const schemaVersion = 1

// Store is the storage surface consumed by search, lifecycle and the CLI.
// It mirrors the MemoryStore interface of the TypeScript reference.
type Store interface {
	Save(input core.SaveInput) (core.WriteResult, error)
	FindByID(id string) (core.Observation, bool)
	// FindByTopicKey returns the latest revision of the (topicKey, exact scope)
	// chain, if any.
	FindByTopicKey(topicKey string, scope core.Scope) (core.Observation, bool)
	// FindChain returns the FULL revision history of the (topicKey, exact scope)
	// chain, ordered by revision ascending.
	FindChain(topicKey string, scope core.Scope) ([]core.Observation, error)
	// FindByScope returns every stored observation whose scope equals the query
	// scope (full revision history).
	FindByScope(scope core.Scope) ([]core.Observation, error)
	// List returns every stored observation (full revision history), insertion
	// order.
	List() ([]core.Observation, error)
	// ListByAuthority returns every stored observation with the given authority
	// status.
	ListByAuthority(status core.AuthorityStatus) ([]core.Observation, error)
	Relate(fromID, toID string, relation core.Relation, meta *core.RelationMeta) error
	// RelationBetween returns the relation recorded from fromID to toID (the
	// first matching row in insertion order), if any.
	RelationBetween(fromID, toID string) (string, bool)
	// SuccessorOf returns the successor of a superseded observation (routes
	// readers onward).
	SuccessorOf(observationID string) (core.Observation, bool)
	// ApplyStatusTransition is the status-only lifecycle mutation; it records an
	// audit-trail entry.
	ApplyStatusTransition(observationID string, to core.AuthorityStatus, meta core.TransitionMeta) (core.Observation, error)
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
// versioned schema. A corrupt or unsupported store fails closed: it never
// fabricates data (contracts/provenance.md frozen policy).
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

	// Fail closed on an unknown layout (provenance.md migration policy).
	version, err := readSchemaVersion(db)
	if err != nil {
		_ = db.Close()
		return nil, err
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
INSERT OR IGNORE INTO schema_meta (key, value) VALUES ('schema_version', '1');

CREATE TABLE IF NOT EXISTS observations (
    id               TEXT PRIMARY KEY,
    topic_key        TEXT NOT NULL,
    title            TEXT NOT NULL,
    type             TEXT NOT NULL,
    scope_kind       TEXT NOT NULL,
    organization_id  TEXT NOT NULL DEFAULT '',
    company_id       TEXT NOT NULL DEFAULT '',
    ruc              TEXT NOT NULL DEFAULT '',
    period           TEXT NOT NULL DEFAULT '',
    what             TEXT NOT NULL,
    why              TEXT NOT NULL,
    where_text       TEXT NOT NULL,
    learned          TEXT NOT NULL,
    authority_status TEXT NOT NULL,
    effective_at     TEXT NOT NULL DEFAULT '',
    expires_at       TEXT NOT NULL DEFAULT '',
    actor            TEXT NOT NULL,
    timestamp        TEXT NOT NULL,
    source           TEXT NOT NULL,
    session          TEXT NOT NULL DEFAULT '',
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
    timestamp      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_transition_log_obs ON transition_log (observation_id);

-- Immutable history (contracts/memory.md rule 3, provenance.md rule 1):
-- content, scope and provenance never change after write. The ONLY mutable
-- column is authority_status, updated exclusively by ApplyStatusTransition.
-- An UPDATE touching any other column aborts; a DELETE aborts.
CREATE TRIGGER IF NOT EXISTS observations_immutable_content
BEFORE UPDATE OF id, topic_key, title, type, scope_kind, organization_id, company_id, ruc, period,
                 what, why, where_text, learned, effective_at, expires_at, actor, timestamp, source,
                 session, revision ON observations
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
// Save — immutable upsert
// ──────────────────────────────────────────────

// Save upserts under (topicKey, exact scope): the first save creates revision 1
// (outcome created); every later save of the same chain creates a NEW immutable
// revision with revision+1 (outcome updated), whether the content is identical
// or evolved (contracts/memory.md frozen semantics — a topic key is a stable
// handle for evolving knowledge, never a unique-content constraint).
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
	if err := core.AssertValidProvenance(input.Provenance); err != nil {
		return core.WriteResult{}, err
	}
	if err := core.AssertValidValidity(input.Validity); err != nil {
		return core.WriteResult{}, err
	}

	status := input.AuthorityStatus
	if status == "" {
		status = core.StatusDraft
	}

	id, err := newUUID()
	if err != nil {
		return core.WriteResult{}, fmt.Errorf("persistence error: generate id: %w", err)
	}

	// Build the would-be observation up front so the unknown outcome can report
	// it (the observation is NOT stored in that case — mirror of the reference).
	revision := 1
	observation := buildObservation(input, id, revision, status)

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.WriteResult{Observation: observation, Outcome: core.WriteUnknown}, err
	}
	defer func() { _ = tx.Rollback() }()

	var maxRev sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT MAX(revision) FROM observations
		WHERE topic_key = ? AND scope_kind = ? AND organization_id = ? AND company_id = ? AND ruc = ? AND period = ?`,
		input.TopicKey, string(input.Scope.Kind), input.Scope.OrganizationID, input.Scope.CompanyID, input.Scope.RUC, input.Scope.Period,
	).Scan(&maxRev)
	if err != nil {
		return core.WriteResult{Observation: observation, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: read chain: %w", err)
	}
	if maxRev.Valid {
		revision = int(maxRev.Int64) + 1
		observation.Revision = revision
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO observations (
			id, topic_key, title, type, scope_kind, organization_id, company_id, ruc, period,
			what, why, where_text, learned, authority_status, effective_at, expires_at,
			actor, timestamp, source, session, revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.TopicKey, input.Title, input.Type, string(input.Scope.Kind),
		input.Scope.OrganizationID, input.Scope.CompanyID, input.Scope.RUC, input.Scope.Period,
		input.Content.What, input.Content.Why, input.Content.Where, input.Content.Learned,
		string(status), validityEffectiveAt(input.Validity), validityExpiresAt(input.Validity),
		input.Provenance.Actor, input.Provenance.Timestamp, input.Provenance.Source, input.Provenance.Session,
		revision,
	)
	if err != nil {
		return core.WriteResult{Observation: observation, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return core.WriteResult{Observation: observation, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: commit: %w", err)
	}

	outcome := core.WriteCreated
	if revision > 1 {
		outcome = core.WriteUpdated
	}
	return core.WriteResult{Observation: observation, Outcome: outcome}, nil
}

func buildObservation(input core.SaveInput, id string, revision int, status core.AuthorityStatus) core.Observation {
	var validity *core.Validity
	if input.Validity != nil {
		v := *input.Validity
		validity = &v
	}
	return core.Observation{
		Identity:        core.Identity{ID: id, TopicKey: input.TopicKey},
		Title:           input.Title,
		Type:            input.Type,
		Scope:           input.Scope,
		Content:         input.Content,
		AuthorityStatus: status,
		Validity:        validity,
		Provenance:      input.Provenance,
		Revision:        revision,
	}
}

func validityEffectiveAt(v *core.Validity) string {
	if v == nil {
		return ""
	}
	return v.EffectiveAt
}

func validityExpiresAt(v *core.Validity) string {
	if v == nil {
		return ""
	}
	return v.ExpiresAt
}

// ──────────────────────────────────────────────
// Sync import — additive, validated, idempotent
// ──────────────────────────────────────────────

// ImportObservation inserts an observation VERBATIM (original id, revision,
// scope, content and provenance) — the sync path (internal/sync). Save never
// does this: Save always creates a NEW revision on the (topicKey, scope) chain,
// which would destroy cross-store identity. Import semantics:
//
//   - additive: an INSERT only; the immutability triggers keep enforcing that
//     content, scope and provenance never change after write;
//   - validated: the same fail-closed validators as Save (topic key, title,
//     scope, content, provenance, validity, revision >= 1);
//   - idempotent: importing an id that already exists with IDENTICAL immutable
//     columns (status excluded — lifecycle converges via transition replay, the
//     sync engine's job) is a no-op (false, nil);
//   - fail closed on tamper: an id that exists with DIFFERENT immutable bytes
//     is IMPORT_CONFLICT — sync never silently overwrites history.
func (s *SQLiteStore) ImportObservation(observation core.Observation) (bool, error) {
	if strings.TrimSpace(observation.Identity.ID) == "" {
		return false, errors.New("INVALID_IMPORT: id must be a non-empty string")
	}
	if strings.TrimSpace(observation.Identity.TopicKey) == "" {
		return false, errors.New("INVALID_TOPIC_KEY: topicKey must be a non-empty string")
	}
	if strings.TrimSpace(observation.Title) == "" {
		return false, errors.New("INVALID_TITLE: title must be a non-empty string")
	}
	if observation.Revision < 1 {
		return false, errors.New("INVALID_IMPORT: revision must be a positive integer")
	}
	if err := core.AssertValidScope(observation.Scope); err != nil {
		return false, err
	}
	if err := core.AssertValidContent(observation.Content); err != nil {
		return false, err
	}
	if err := core.AssertValidProvenance(observation.Provenance); err != nil {
		return false, err
	}
	if err := core.AssertValidValidity(observation.Validity); err != nil {
		return false, err
	}
	status := observation.AuthorityStatus
	if status == "" {
		status = core.StatusDraft
	}
	if !validAuthorityStatus(status) {
		return false, fmt.Errorf("INVALID_IMPORT: unknown authority status %q", status)
	}

	if existing, ok := s.FindByID(observation.Identity.ID); ok {
		if sameImmutableObservation(existing, observation) {
			return false, nil // already present and identical — no-op
		}
		return false, fmt.Errorf("IMPORT_CONFLICT: observation %s already exists with different immutable content — sync never overwrites history", observation.Identity.ID)
	}

	_, err := s.db.Exec(`
		INSERT INTO observations (
			id, topic_key, title, type, scope_kind, organization_id, company_id, ruc, period,
			what, why, where_text, learned, authority_status, effective_at, expires_at,
			actor, timestamp, source, session, revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		observation.Identity.ID, observation.Identity.TopicKey, observation.Title, observation.Type,
		string(observation.Scope.Kind), observation.Scope.OrganizationID, observation.Scope.CompanyID,
		observation.Scope.RUC, observation.Scope.Period,
		observation.Content.What, observation.Content.Why, observation.Content.Where, observation.Content.Learned,
		string(observation.AuthorityStatus), validityEffectiveAt(observation.Validity), validityExpiresAt(observation.Validity),
		observation.Provenance.Actor, observation.Provenance.Timestamp, observation.Provenance.Source, observation.Provenance.Session,
		observation.Revision,
	)
	if err != nil {
		return false, fmt.Errorf("persistence error: import observation: %w", err)
	}
	return true, nil
}

// validAuthorityStatus reports whether status is one of the four known
// lifecycle states (fail closed on anything else).
func validAuthorityStatus(status core.AuthorityStatus) bool {
	switch status {
	case core.StatusDraft, core.StatusReviewed, core.StatusPromoted, core.StatusSuperseded:
		return true
	}
	return false
}

// sameImmutableObservation compares every column the immutability triggers
// protect (status excluded — the only mutable field, owned by the lifecycle
// machine and the transition-replay path).
func sameImmutableObservation(a, b core.Observation) bool {
	return a.Identity.TopicKey == b.Identity.TopicKey &&
		a.Title == b.Title &&
		a.Type == b.Type &&
		core.ScopeEquals(a.Scope, b.Scope) &&
		a.Content == b.Content &&
		a.Revision == b.Revision &&
		validityEffectiveAt(a.Validity) == validityEffectiveAt(b.Validity) &&
		validityExpiresAt(a.Validity) == validityExpiresAt(b.Validity) &&
		a.Provenance == b.Provenance
}

// ImportTransition inserts a lifecycle audit-trail record VERBATIM (the sync
// path). Idempotent: an identical (observation_id, from_status, to_status,
// actor, timestamp) row is a no-op (false, nil); a NEW row is inserted with
// the record's original provenance (true, nil). The audit trail is history —
// it crosses stores verbatim, never rewritten.
func (s *SQLiteStore) ImportTransition(record core.StatusTransitionRecord) (bool, error) {
	if strings.TrimSpace(record.ObservationID) == "" {
		return false, errors.New("INVALID_IMPORT: observationId must be a non-empty string")
	}
	if !validAuthorityStatus(record.From) || !validAuthorityStatus(record.To) {
		return false, errors.New("INVALID_IMPORT: from and to statuses must be known lifecycle states")
	}

	var existing int
	err := s.db.QueryRow(`
		SELECT 1 FROM transition_log
		WHERE observation_id = ? AND from_status = ? AND to_status = ? AND actor = ? AND timestamp = ?`,
		record.ObservationID, string(record.From), string(record.To), record.Actor, record.Timestamp,
	).Scan(&existing)
	if err == nil {
		return false, nil // identical row already present — no-op
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("persistence error: check transition: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO transition_log (observation_id, from_status, to_status, actor, timestamp)
		VALUES (?, ?, ?, ?, ?)`,
		record.ObservationID, string(record.From), string(record.To), record.Actor, record.Timestamp,
	)
	if err != nil {
		return false, fmt.Errorf("persistence error: import transition: %w", err)
	}
	return true, nil
}

// ApplyImportedStatus advances an observation's authority status WITHOUT
// writing a transition_log row — SYNC-ONLY: the corresponding audit record is
// imported separately by the sync engine (ImportTransition), so the log row
// already exists; writing a second one would duplicate the audit trail. Never
// used by the CLI/lifecycle surface (which goes through
// ApplyStatusTransition). The advance is forward-only by contract: the sync
// engine checks the sink is BEHIND before calling.
func (s *SQLiteStore) ApplyImportedStatus(observationID string, to core.AuthorityStatus, meta core.TransitionMeta) (core.Observation, error) {
	if !validAuthorityStatus(to) {
		return core.Observation{}, errors.New("INVALID_TRANSITION: unknown target status")
	}
	result, err := s.db.Exec(`UPDATE observations SET authority_status = ? WHERE id = ?`, string(to), observationID)
	if err != nil {
		return core.Observation{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return core.Observation{}, err
	}
	if affected == 0 {
		return core.Observation{}, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", observationID)
	}
	observation, ok := s.FindByID(observationID)
	if !ok {
		return core.Observation{}, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", observationID)
	}
	return observation, nil
}

// ──────────────────────────────────────────────
// Reads
// ──────────────────────────────────────────────

const observationColumns = `id, topic_key, title, type, scope_kind, organization_id, company_id, ruc, period,
	what, why, where_text, learned, authority_status, effective_at, expires_at,
	actor, timestamp, source, session, revision`

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanObservation(rs rowScanner) (core.Observation, error) {
	var (
		id, topicKey, title, typ, scopeKind, orgID, companyID, ruc, period string
		what, why, whereText, learned, status, effAt, expAt                string
		actor, timestamp, source, session                                  string
		revision                                                           int
	)
	if err := rs.Scan(
		&id, &topicKey, &title, &typ, &scopeKind, &orgID, &companyID, &ruc, &period,
		&what, &why, &whereText, &learned, &status, &effAt, &expAt,
		&actor, &timestamp, &source, &session, &revision,
	); err != nil {
		return core.Observation{}, err
	}
	var validity *core.Validity
	if effAt != "" || expAt != "" {
		validity = &core.Validity{EffectiveAt: effAt, ExpiresAt: expAt}
	}
	return core.Observation{
		Identity:        core.Identity{ID: id, TopicKey: topicKey},
		Title:           title,
		Type:            typ,
		Scope:           core.Scope{Kind: core.ScopeKind(scopeKind), OrganizationID: orgID, CompanyID: companyID, RUC: ruc, Period: period},
		Content:         core.Content{What: what, Why: why, Where: whereText, Learned: learned},
		AuthorityStatus: core.AuthorityStatus(status),
		Validity:        validity,
		Provenance:      core.Provenance{Actor: actor, Timestamp: timestamp, Source: source, Session: session},
		Revision:        revision,
	}, nil
}

// FindByID returns the observation with the given id, if any.
func (s *SQLiteStore) FindByID(id string) (core.Observation, bool) {
	row := s.db.QueryRow(`SELECT `+observationColumns+` FROM observations WHERE id = ?`, id)
	observation, err := scanObservation(row)
	if err != nil {
		return core.Observation{}, false
	}
	return observation, true
}

// FindByTopicKey returns the latest revision of the (topicKey, exact scope)
// chain, if any.
func (s *SQLiteStore) FindByTopicKey(topicKey string, scope core.Scope) (core.Observation, bool) {
	where, args := chainWhere(topicKey, scope)
	row := s.db.QueryRow(`SELECT `+observationColumns+` FROM observations WHERE `+where+` ORDER BY revision DESC LIMIT 1`, args...)
	observation, err := scanObservation(row)
	if err != nil {
		return core.Observation{}, false
	}
	return observation, true
}

// FindByScope returns every stored observation whose scope equals the query
// scope (full revision history), insertion order.
func (s *SQLiteStore) FindByScope(scope core.Scope) ([]core.Observation, error) {
	where, args := scopeWhere(scope)
	return s.queryObservations(`WHERE `+where+` ORDER BY rowid`, args...)
}

// List returns every stored observation (full revision history), insertion
// order.
func (s *SQLiteStore) List() ([]core.Observation, error) {
	return s.queryObservations(`ORDER BY rowid`)
}

// ListByAuthority returns every stored observation with the given authority
// status, insertion order.
func (s *SQLiteStore) ListByAuthority(status core.AuthorityStatus) ([]core.Observation, error) {
	return s.queryObservations(`WHERE authority_status = ? ORDER BY rowid`, string(status))
}

func (s *SQLiteStore) queryObservations(suffix string, args ...any) ([]core.Observation, error) {
	rows, err := s.db.Query(`SELECT `+observationColumns+` FROM observations `+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	observations := make([]core.Observation, 0)
	for rows.Next() {
		observation, err := scanObservation(rows)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return observations, nil
}

// FindChain returns the FULL revision history of a (topicKey, exact scope)
// chain, ordered by revision ascending — the counterpart of FindByTopicKey
// (which returns only the latest). The sync path and the HTTP chain surface
// (GET /v1/chain) use it to serve every revision of a topic key.
func (s *SQLiteStore) FindChain(topicKey string, scope core.Scope) ([]core.Observation, error) {
	where, args := chainWhere(topicKey, scope)
	return s.queryObservations(`WHERE `+where+` ORDER BY revision ASC`, args...)
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

// Relate records a relation between two existing observations. A duplicate
// (fromId, toId, relation) is a no-op; an observation never relates to itself.
func (s *SQLiteStore) Relate(fromID, toID string, relation core.Relation, meta *core.RelationMeta) error {
	if fromID == toID {
		return errors.New("INVALID_RELATION: an observation cannot relate to itself")
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
	defer func() { _ = tx.Rollback() }()

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
		return tx.Commit()
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
	return tx.Commit()
}

// RelationBetween returns the relation recorded from fromID to toID (the first
// matching row in insertion order), if any. Relations are directional: a
// supersedes row is stored from the superseded observation to its replacement,
// so RelationBetween(old, replacement) returns "supersedes" while the reverse
// pair does not.
func (s *SQLiteStore) RelationBetween(fromID, toID string) (string, bool) {
	var relation string
	err := s.db.QueryRow(`SELECT relation FROM relations WHERE from_id = ? AND to_id = ? ORDER BY rowid LIMIT 1`, fromID, toID).Scan(&relation)
	if err != nil {
		return "", false
	}
	return relation, true
}

// SuccessorOf returns the successor of a superseded observation (the first
// `supersedes` relation recorded from it), routing readers onward.
func (s *SQLiteStore) SuccessorOf(observationID string) (core.Observation, bool) {
	var toID string
	err := s.db.QueryRow(`SELECT to_id FROM relations WHERE from_id = ? AND relation = 'supersedes' ORDER BY rowid LIMIT 1`, observationID).Scan(&toID)
	if err != nil {
		return core.Observation{}, false
	}
	return s.FindByID(toID)
}

// ApplyStatusTransition is the single status-only mutation the lifecycle
// machine may perform; it records an audit-trail entry in the same transaction
// (contracts/provenance.md rule 3).
func (s *SQLiteStore) ApplyStatusTransition(observationID string, to core.AuthorityStatus, meta core.TransitionMeta) (core.Observation, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Observation{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var from string
	err = tx.QueryRowContext(ctx, `SELECT authority_status FROM observations WHERE id = ?`, observationID).Scan(&from)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.Observation{}, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", observationID)
		}
		return core.Observation{}, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE observations SET authority_status = ? WHERE id = ?`, string(to), observationID); err != nil {
		return core.Observation{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transition_log (observation_id, from_status, to_status, actor, timestamp) VALUES (?, ?, ?, ?, ?)`,
		observationID, from, string(to), meta.Actor, meta.Timestamp,
	); err != nil {
		return core.Observation{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.Observation{}, err
	}
	observation, ok := s.FindByID(observationID)
	if !ok {
		return core.Observation{}, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", observationID)
	}
	return observation, nil
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
	rows, err := s.db.Query(`SELECT observation_id, from_status, to_status, actor, timestamp FROM transition_log ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	records := make([]core.StatusTransitionRecord, 0)
	for rows.Next() {
		var (
			observationID, from, to, actor, timestamp string
		)
		if err := rows.Scan(&observationID, &from, &to, &actor, &timestamp); err != nil {
			return nil, err
		}
		records = append(records, core.StatusTransitionRecord{
			ObservationID: observationID,
			From:          core.AuthorityStatus(from),
			To:            core.AuthorityStatus(to),
			Actor:         actor,
			Timestamp:     timestamp,
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
	SchemaVersion  int    `json:"schemaVersion"`
	Storage        string `json:"storage"`
	DBPath         string `json:"dbPath"`
	Observations   int    `json:"observations"`
	RevisionChains int    `json:"revisionChains"`
	Transitions    int    `json:"transitions"`
	Relations      int    `json:"relations"`
}

// Doctor verifies the schema (fail closed on corruption) and reports counts.
func (s *SQLiteStore) Doctor() (DoctorReport, error) {
	// Fail closed: every expected table and the immutability guard must exist.
	for _, table := range []string{"schema_meta", "observations", "relations", "transition_log"} {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			return DoctorReport{}, fmt.Errorf("corrupt store: expected table %q is missing: %w", table, err)
		}
	}
	var triggerName string
	if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = 'observations_no_delete'`).Scan(&triggerName); err != nil {
		return DoctorReport{}, fmt.Errorf("corrupt store: immutability trigger missing: %w", err)
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
