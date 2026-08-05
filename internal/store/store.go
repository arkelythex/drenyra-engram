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
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// schemaVersion is the store layout version (contracts/provenance.md frozen
// migration policy: versioned layout, additive migrations only).
//
// v3 (v0.4.0 Step 1): adds observations.materiality_level (the declared
// materiality classification set by the writing agent) and the six identity /
// approval tables (companies, memberships, membership_roles, sessions,
// approval_events, idempotency_keys) with their indexes and the two
// approval_events immutability triggers.
//
// v4 (v0.4.0 Step 2): adds the four accounting-judgment persistence tables
// (judgments, judgment_events, judgment_idempotency_keys,
// judgment_relations) with their indexes and the judgment immutability
// triggers (docs/architecture/conflict-judgment-step2.md section 4).
//
// v5 (v0.4.0 Step 3, keys commit): adds the signing-key lifecycle table
// (signing_keys — public keys ONLY; private seeds stay in the user-owned
// 0600 keyring file) and the immutable action-receipt table (receipts) with
// their indexes and the immutability/revocation-only triggers
// (docs/architecture/ed25519-receipts-step3.md "Signing-key lifecycle" and
// "SQLite schema v5").
const schemaVersion = 5

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
	// signer mints and persists an immutable action receipt INSIDE the caller's
	// transaction for every covered act (v0.4.0 Step 3 atomic emission). nil
	// disables receipt emission entirely — the default for callers that never
	// attach one (imports, read-only surfaces, tests).
	signer ReceiptSigner
}

// ReceiptSigner mints and persists an immutable receipt for one covered act. The
// signer runs on the caller-provided Queryer and NEVER starts or commits a
// transaction — the caller's tx owns atomicity, so a signing failure rolls the
// covered act back with it. *receipts.Signer satisfies this interface (its Sign
// reads the subject chain head, signs with the active keyring key, registers the
// public key and inserts the receipt row on q).
type ReceiptSigner interface {
	Sign(ctx context.Context, q Queryer, payload core.ReceiptPayload, issuedAt string) (core.SignedReceipt, error)
}

// Open opens (creating if needed) the SQLite database at path and applies the
// versioned schema. Fresh stores bootstrap to the v2 layout exactly, then run
// the SAME additive migration chain used for existing stores
// (v2→v3→v4→v5) — one tested migration path. A v1 store is migrated
// additively (single transaction), then the v2→v3, v3→v4 and v4→v5
// migrations each run in their own single transaction. A corrupt or
// unsupported store fails closed: it never fabricates data
// (contracts/provenance.md frozen policy). An OPTIONAL receipt signer may be
// attached at open (nil signer → no receipt emission); the store↔signer
// construction cycle (the signer needs the opened store) is resolved by the
// adapter via SetReceiptSigner right after Open.
func Open(path string, signers ...ReceiptSigner) (*SQLiteStore, error) {
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
	// store is migrated additively in ONE transaction, then the v2→v3, v3→v4 and
	// v4→v5 migrations each run in their own single transaction before use.
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
		version = 2
	}
	if version == 2 {
		if err := migrateV2ToV3(db); err != nil {
			_ = db.Close()
			return nil, err
		}
		version = 3
	}
	if version == 3 {
		if err := migrateV3ToV4(db); err != nil {
			_ = db.Close()
			return nil, err
		}
		version = 4
	}
	if version == 4 {
		if err := migrateV4ToV5(db); err != nil {
			_ = db.Close()
			return nil, err
		}
		version = schemaVersion
	}
	if version != schemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("unsupported store layout: schema_version=%d, supported=%d — fail closed; migrate additively, never rewrite", version, schemaVersion)
	}

	if len(signers) > 1 {
		_ = db.Close()
		return nil, fmt.Errorf("open sqlite store: at most one receipt signer may be attached")
	}
	st := &SQLiteStore{db: db}
	if len(signers) == 1 {
		st.signer = signers[0]
	}
	return st, nil
}

// SetReceiptSigner attaches the signer that mints immutable action receipts for
// covered mutations (v0.4.0 Step 3 atomic emission). It is attached at Open,
// below the HTTP/MCP/CLI adapters, so every surface emits identically; a nil
// signer disables emission (the default). SetReceiptSigner exists for the
// store↔signer construction cycle (the signer itself needs the store): the
// adapter opens the store, builds the signer over it, and attaches it before any
// command runs. A signer attached later only affects acts emitted after the
// attachment.
func (s *SQLiteStore) SetReceiptSigner(signer ReceiptSigner) { s.signer = signer }

// kernelPolicyVersion is the frozen policy version stamped on NON-policy acts
// (memory_recorded / memory_rejected / memory_voided / memory_superseded /
// evidence_linked): the design's rule — non-policy acts use kernel/v0.4.0,
// avoiding an ambiguous empty policy.
const kernelPolicyVersion = "kernel/v0.4.0"

// emitReceipt mints and persists ONE immutable receipt for a covered act INSIDE
// the caller's transaction — it NEVER starts or commits a transaction (the
// caller's tx owns atomicity, so a signing failure rolls the act back with it).
//
// The helper fixes the subject/action/issuedAt on the payload (the caller
// provides every other payload field — scope, envelope/judgment hashes, reason,
// principal snapshot, successor, evidence ref, policy version) and delegates to
// the attached signer, which reads the subject's chain head (LatestReceiptChainHead),
// canonicalizes, signs, registers the public key and inserts the receipt row, all
// on the given Queryer. With no signer attached (nil), receipts are NOT emitted
// and the call is a no-op returning the zero receipt.
func (s *SQLiteStore) emitReceipt(ctx context.Context, q Queryer, subjectType core.SubjectType, subjectID string, action core.ReceiptAction, payload core.ReceiptPayload, issuedAt string) (core.SignedReceipt, error) {
	if s.signer == nil {
		return core.SignedReceipt{}, nil
	}
	payload.Version = core.ReceiptPayloadVersion
	payload.SubjectType = subjectType
	payload.SubjectID = subjectID
	payload.Action = action
	payload.IssuedAt = issuedAt
	return s.signer.Sign(ctx, q, payload, issuedAt)
}

// receiptPrincipalRoles converts the canonical snapshot roles (sorted,
// deduplicated) to the payload's string list; the receipt payload canonicalizes
// them again defensively (sorted + deduplicated — the contract never depends on
// the caller's ordering).
func receiptPrincipalRoles(snapshot auth.PrincipalSnapshot) []string {
	out := make([]string, 0, len(snapshot.Roles))
	for _, r := range snapshot.Roles {
		out = append(out, string(r))
	}
	return out
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
    validity_effective_at TEXT NOT NULL DEFAULT '',
    validity_source      TEXT NOT NULL DEFAULT '',
    actor            TEXT NOT NULL DEFAULT '',
    timestamp        TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL DEFAULT '',
    session          TEXT NOT NULL DEFAULT '',
    source_json      TEXT NOT NULL DEFAULT '',
    content_hash     TEXT NOT NULL,
    identity_hash    TEXT NOT NULL DEFAULT '',
    envelope_hash    TEXT NOT NULL DEFAULT '',
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
		{"recorded_at", "TEXT"}, {"observed_at", "TEXT"}, {"validity_effective_at", "TEXT"},
		{"validity_source", "TEXT"},
		{"source_json", "TEXT"}, {"content_hash", "TEXT"},
		{"identity_hash", "TEXT"}, {"envelope_hash", "TEXT"},
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
		    effective_at = ?, validity_effective_at = ?, validity_source = ?, source_json = ?, content_hash = ?,
		    identity_hash = ?, envelope_hash = ?
		WHERE id = ?`)
	if err != nil {
		_ = rows.Close()
		return fmt.Errorf("migrate v1→v2: prepare backfill: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	type backfillRow struct {
		id, kind, status, recordedAt, effectiveAt, validityEffectiveAt, validitySource, sourceJSON, contentHash, identityHash, envelopeHash string
	}
	batch := make([]backfillRow, 0, migrationBatchSize)
	flush := func() error {
		for _, r := range batch {
			if _, err := stmt.ExecContext(ctx, r.kind, r.status, r.recordedAt, r.effectiveAt, r.validityEffectiveAt, r.validitySource, r.sourceJSON, r.contentHash, r.identityHash, r.envelopeHash, r.id); err != nil {
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
		contentHash := core.ComputeContentHash(memory)
		identityHash := core.ComputeIdentityHash(memory)
		envelopeHash := core.ComputeEnvelopeHash(memory)
		batch = append(batch, backfillRow{
			id:                  id,
			kind:                string(kind),
			status:              string(status),
			recordedAt:          recordedAt,
			effectiveAt:         effectiveAt,
			validityEffectiveAt: effAt,
			validitySource:      migratedValiditySource(effAt),
			sourceJSON:          string(sourceJSON),
			contentHash:         contentHash,
			identityHash:        identityHash,
			envelopeHash:        envelopeHash,
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

// ──────────────────────────────────────────────
// v2 → v3 additive migration (single transaction, fail closed)
// ──────────────────────────────────────────────

// migrateV2ToV3 upgrades a schema_version=2 store to v3 IN ONE TRANSACTION:
// observations gains the materiality_level column, the six v3 tables and the
// three supporting indexes are created with the CREATE TABLE statements of
// docs/architecture/approval-principal-step1.md section 4 verbatim (including
// every UNIQUE constraint, CHECK, FK and the two approval_events immutability
// triggers), the observations immutability guard is recreated to also protect
// the declared materiality classification, and schema_version=3 is set ONLY
// after the whole migration succeeded. On any failure the transaction rolls
// back and the store stays v2. No uniqueness constraint is retrofitted onto
// legacy transition_log (frozen decision: sync uses value-based idempotency;
// approval uniqueness lives in approval_events + idempotency_keys).
func migrateV2ToV3(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate v2→v3: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (a) observations.materiality_level — NULL | normal | material | critical
	// (NULL is treated as normal by the approval policy).
	existing, err := tableColumns(ctx, tx, "observations")
	if err != nil {
		return fmt.Errorf("migrate v2→v3: read observations columns: %w", err)
	}
	if !existing["materiality_level"] {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE observations ADD COLUMN materiality_level TEXT`); err != nil {
			return fmt.Errorf("migrate v2→v3: add observations.materiality_level: %w", err)
		}
	}

	// (b) The six v3 tables — CREATE TABLE statements verbatim from the design
	// (no IF NOT EXISTS: a pre-existing table with a conflicting shape is a
	// corruption signal and fails the migration closed).
	for _, ddl := range []string{
		companiesDDL, membershipsDDL, membershipRolesDDL, sessionsDDL,
		approvalEventsDDL, idempotencyKeysDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v2→v3: create table: %w", err)
		}
	}

	// (c) The three supporting indexes.
	for _, ddl := range []string{
		membershipsSubjectIndexDDL, sessionsMembershipIndexDDL, approvalEventsMemoryIndexDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v2→v3: create index: %w", err)
		}
	}

	// (d) The two approval_events immutability triggers.
	for _, ddl := range []string{approvalEventsNoUpdateDDL, approvalEventsNoDeleteDDL} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v2→v3: create approval_events trigger: %w", err)
		}
	}

	// (e) The declared materiality classification is immutable content: recreate
	// the observations guard so materiality_level is protected like every other
	// immutable column (the v2 guard does not know the column).
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS observations_immutable_content`); err != nil {
		return fmt.Errorf("migrate v2→v3: drop v2 guard: %w", err)
	}
	if _, err := tx.ExecContext(ctx, immutabilityTriggerV3DDL); err != nil {
		return fmt.Errorf("migrate v2→v3: install v3 guard: %w", err)
	}

	// (f) schema_version = 3 ONLY after the whole migration succeeded — same
	// transaction, so a failure above rolls everything back.
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = '3' WHERE key = 'schema_version'`); err != nil {
		return fmt.Errorf("migrate v2→v3: set schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate v2→v3: commit: %w", err)
	}
	committed = true
	return nil
}

// ──────────────────────────────────────────────
// v3 → v4 additive migration (single transaction, fail closed)
// ──────────────────────────────────────────────

// migrateV3ToV4 upgrades a schema_version=3 store to v4 IN ONE TRANSACTION:
// the four accounting-judgment persistence tables (judgments,
// judgment_events, judgment_idempotency_keys, judgment_relations), the four
// supporting indexes and the judgment immutability triggers are created with
// the CREATE statements of docs/architecture/conflict-judgment-step2.md
// section 4 (the judgments table verbatim, including every CHECK, FK and the
// open-tuple partial unique index), and schema_version=4 is set ONLY after
// the whole migration succeeded. On any failure the transaction rolls back
// and the store stays v3. No IF NOT EXISTS is used: a pre-existing table or
// trigger with a conflicting shape is a corruption signal and fails the
// migration closed.
func migrateV3ToV4(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate v3→v4: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (a) The four v4 tables — CREATE statements verbatim from the design.
	for _, ddl := range []string{
		judgmentsDDL, judgmentEventsDDL, judgmentIdempotencyKeysDDL, judgmentRelationsDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v3→v4: create table: %w", err)
		}
	}

	// (b) The four supporting indexes (incl. the open-tuple partial unique
	// index, which only constrains open proposals).
	for _, ddl := range []string{
		judgmentOpenTupleIndexDDL, judgmentsPairIndexDDL,
		judgmentsPredecessorIndexDDL, judgmentsSuccessorIndexDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v3→v4: create index: %w", err)
		}
	}

	// (c) The judgment immutability triggers (events frozen; deletes frozen;
	// confirmed rows only supersede with routing-only changes; terminal rows
	// never re-open).
	for _, ddl := range []string{
		judgmentEventsNoUpdateDDL, judgmentEventsNoDeleteDDL,
		judgmentsNoDeleteDDL, judgmentsImmutableUpdateDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v3→v4: create judgment trigger: %w", err)
		}
	}

	// (d) schema_version = 4 ONLY after the whole migration succeeded — same
	// transaction, so a failure above rolls everything back.
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = '4' WHERE key = 'schema_version'`); err != nil {
		return fmt.Errorf("migrate v3→v4: set schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate v3→v4: commit: %w", err)
	}
	committed = true
	return nil
}

// ──────────────────────────────────────────────
// v4 → v5 additive migration (single transaction, fail closed)
// ──────────────────────────────────────────────

// migrateV4ToV5 upgrades a schema_version=4 store to v5 IN ONE TRANSACTION:
// the signing-key lifecycle table (signing_keys — public keys only; private
// seeds stay in the user-owned keyring file) and the immutable action-receipt
// table (receipts) are created with the CREATE statements of
// docs/architecture/ed25519-receipts-step3.md "SQLite schema v5" (every CHECK,
// FK, the revocation-only signing_keys trigger, the receipts no-update /
// no-delete triggers, the unique (subject_type, subject_id, action,
// payload_hash) constraint, the evidence_linked-excluding partial singleton
// index and the subject/time + key/time indexes), and schema_version=5 is set
// ONLY after the whole migration succeeded. On any failure the transaction
// rolls back and the store stays v4. No IF NOT EXISTS is used: a pre-existing
// table or trigger with a conflicting shape is a corruption signal and fails
// the migration closed. No historical receipt is backfilled — retrospective
// signing would falsely claim contemporaneous issuance (design decision).
func migrateV4ToV5(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate v4→v5: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (a) The two v5 tables.
	for _, ddl := range []string{signingKeysDDL, receiptsDDL} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v4→v5: create table: %w", err)
		}
	}

	// (b) The three supporting indexes.
	for _, ddl := range []string{
		receiptsSingletonIndexDDL, receiptsSubjectTimeIndexDDL, receiptsKeyTimeIndexDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v4→v5: create index: %w", err)
		}
	}

	// (c) The immutability / revocation-only triggers (deletion forbidden;
	// signing_keys updates may only revoke; receipts never update or delete).
	for _, ddl := range []string{
		signingKeysNoDeleteDDL, signingKeysRevokeOnlyDDL,
		receiptsNoUpdateDDL, receiptsNoDeleteDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v4→v5: create trigger: %w", err)
		}
	}

	// (d) schema_version = 5 ONLY after the whole migration succeeded — same
	// transaction, so a failure above rolls everything back.
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = '5' WHERE key = 'schema_version'`); err != nil {
		return fmt.Errorf("migrate v4→v5: set schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate v4→v5: commit: %w", err)
	}
	committed = true
	return nil
}

// v4 tables and supporting objects — CREATE statements verbatim from
// docs/architecture/conflict-judgment-step2.md section 4.

const judgmentsDDL = `
    CREATE TABLE judgments (
      id TEXT PRIMARY KEY,
      tenant_id TEXT NOT NULL, company_id TEXT NOT NULL, fiscal_period_id TEXT,
      from_id TEXT NOT NULL REFERENCES observations(id),
      to_id TEXT NOT NULL REFERENCES observations(id),
      relation TEXT NOT NULL CHECK(relation IN
        ('supports','contradicts','explains','reconciles','reverses','supersedes')),
      status TEXT NOT NULL CHECK(status IN
        ('proposed','confirmed','rejected','withdrawn','superseded')),
      proposer_system TEXT NOT NULL, proposer_actor_id TEXT NOT NULL DEFAULT '',
      proposer_actor_kind TEXT NOT NULL CHECK(proposer_actor_kind IN ('agent','system')),
      proposer_session TEXT NOT NULL DEFAULT '', proposal_reason TEXT NOT NULL,
      resolution TEXT, policy_version TEXT,
      adjudicator_subject_id TEXT, adjudicator_membership_id TEXT REFERENCES memberships(id),
      adjudicator_roles_json TEXT, authentication_method TEXT, assurance_level TEXT,
      principal_authenticated_at TEXT,
      predecessor_id TEXT REFERENCES judgments(id), supersedes_id TEXT REFERENCES judgments(id),
      proposed_at TEXT NOT NULL, updated_at TEXT NOT NULL, decided_at TEXT,
      CHECK(from_id <> to_id),
      CHECK((status='proposed') = (decided_at IS NULL)),
      CHECK(status NOT IN ('confirmed','rejected') OR adjudicator_subject_id IS NOT NULL),
      CHECK(adjudicator_subject_id IS NULL OR status IN ('confirmed','rejected','superseded')),
      CHECK(status NOT IN ('confirmed','rejected') OR
        (length(trim(resolution))>0 AND length(policy_version)>0))
    );
    `

const judgmentOpenTupleIndexDDL = `CREATE UNIQUE INDEX uq_judgment_open_tuple
      ON judgments(tenant_id,company_id,from_id,to_id,relation) WHERE status='proposed';`

const judgmentsPairIndexDDL = `CREATE INDEX idx_judgments_pair ON judgments(tenant_id,company_id,from_id,to_id,status);`

const judgmentsPredecessorIndexDDL = `CREATE INDEX idx_judgments_predecessor ON judgments(predecessor_id);`

const judgmentsSuccessorIndexDDL = `CREATE INDEX idx_judgments_successor ON judgments(supersedes_id);`

// judgment_events is the immutable transition log of the judgment machine.
// Every legal transition writes exactly one event; confirm/reject events carry
// the principal snapshot and the frozen policy version. The action/status
// CHECKs mirror the judgment transition table: confirm/reject/withdraw leave
// proposed, supersede leaves proposed or confirmed; all land in the design's
// target statuses.
const judgmentEventsDDL = `
    CREATE TABLE judgment_events (
      id TEXT PRIMARY KEY, judgment_id TEXT NOT NULL REFERENCES judgments(id),
      request_id TEXT NOT NULL,
      action TEXT NOT NULL CHECK(action IN ('confirm','reject','withdraw','supersede')),
      from_status TEXT NOT NULL CHECK(from_status IN ('proposed','confirmed')),
      to_status TEXT NOT NULL CHECK(to_status IN ('confirmed','rejected','withdrawn','superseded')),
      judgment_hash TEXT NOT NULL,
      principal_snapshot_json TEXT, policy_version TEXT,
      reason TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
      CHECK((action IN ('confirm','reject')) = (principal_snapshot_json IS NOT NULL)),
      CHECK((action IN ('confirm','reject')) = (policy_version IS NOT NULL)),
      CHECK(
        (action='confirm' AND from_status='proposed' AND to_status='confirmed') OR
        (action='reject' AND from_status='proposed' AND to_status='rejected') OR
        (action='withdraw' AND from_status='proposed' AND to_status='withdrawn') OR
        (action='supersede' AND from_status IN ('proposed','confirmed') AND to_status='superseded')
      )
    );
    `

const judgmentEventsNoUpdateDDL = `
    CREATE TRIGGER judgment_events_no_update BEFORE UPDATE ON judgment_events BEGIN
      SELECT RAISE(ABORT,'IMMUTABLE_JUDGMENT_EVENT'); END;
    `

const judgmentEventsNoDeleteDDL = `
    CREATE TRIGGER judgment_events_no_delete BEFORE DELETE ON judgment_events BEGIN
      SELECT RAISE(ABORT,'IMMUTABLE_JUDGMENT_EVENT'); END;
    `

// judgment_idempotency_keys mirrors idempotency_keys for the judgment
// commands: a command is keyed by (tenant_id, request_id) and bound to the
// exact actor identity that issued it (proposer for propose/withdraw, verified
// principal for confirm/reject). actor_binding is the canonical identity
// string of that actor; result_json and judgment_event_id are set together
// when the command completed.
const judgmentIdempotencyKeysDDL = `
    CREATE TABLE judgment_idempotency_keys (
      tenant_id TEXT NOT NULL, request_id TEXT NOT NULL,
      command_hash TEXT NOT NULL, actor_binding TEXT NOT NULL,
      judgment_id TEXT REFERENCES judgments(id),
      result_json TEXT, judgment_event_id TEXT REFERENCES judgment_events(id),
      created_at TEXT NOT NULL, completed_at TEXT,
      PRIMARY KEY(tenant_id,request_id),
      CHECK((judgment_event_id IS NULL) = (result_json IS NULL))
    );
    `

// judgment_relations routes judgment supersession ONLY: judgment ids never
// enter the observation relations table. JudgmentSuccessorOf reads this
// table; the pair is the primary key and the relation is frozen to
// 'supersedes' (a correction routes readers from the superseded predecessor
// to the successor).
const judgmentRelationsDDL = `
    CREATE TABLE judgment_relations (
      from_judgment_id TEXT NOT NULL REFERENCES judgments(id),
      to_judgment_id TEXT NOT NULL REFERENCES judgments(id),
      relation TEXT NOT NULL CHECK(relation='supersedes'),
      actor TEXT NOT NULL DEFAULT '',
      timestamp TEXT NOT NULL DEFAULT '',
      PRIMARY KEY(from_judgment_id, to_judgment_id)
    );
    `

// judgments_no_delete freezes the adjudication history: a judgment is never
// deleted (IMMUTABLE_JUDGMENT).
const judgmentsNoDeleteDDL = `
    CREATE TRIGGER judgments_no_delete BEFORE DELETE ON judgments BEGIN
      SELECT RAISE(ABORT,'IMMUTABLE_JUDGMENT'); END;
    `

// judgments_immutable_update enforces the design §4 update rules:
//   - rejected | withdrawn | superseded rows are terminal and never re-open;
//   - a confirmed row may ONLY be superseded: status confirmed->superseded
//     while setting a previously-empty supersedes_id, with every proposal and
//     adjudication field byte-equal (NULL-safe IS) — only the routing fields
//     (status, supersedes_id, updated_at) may change;
//   - proposed rows are the machine's work area (transitions and withdrawal
//     are legitimate state-machine updates) and stay writable.
//
// COALESCE keeps the supersedes_id comparisons definitive (a freshly
// confirmed row stores NULL, never the empty string): NULL and the empty
// string are both "empty", and the NOT(...) legality check must not collapse
// to NULL (NULL would be falsy and silently let an illegal update through).
const judgmentsImmutableUpdateDDL = `
    CREATE TRIGGER judgments_immutable_update
    BEFORE UPDATE ON judgments
    BEGIN
      SELECT RAISE(ABORT,'IMMUTABLE_JUDGMENT: terminal judgments never re-open')
        WHERE OLD.status IN ('rejected','withdrawn','superseded');
      SELECT RAISE(ABORT,'IMMUTABLE_JUDGMENT: confirmed judgments may only be superseded with routing-only changes')
        WHERE OLD.status = 'confirmed' AND NOT (
          NEW.status = 'superseded'
          AND COALESCE(OLD.supersedes_id, '') = ''
          AND COALESCE(NEW.supersedes_id, '') <> ''
          AND OLD.id IS NEW.id
          AND OLD.tenant_id IS NEW.tenant_id
          AND OLD.company_id IS NEW.company_id
          AND OLD.fiscal_period_id IS NEW.fiscal_period_id
          AND OLD.from_id IS NEW.from_id
          AND OLD.to_id IS NEW.to_id
          AND OLD.relation IS NEW.relation
          AND OLD.proposer_system IS NEW.proposer_system
          AND OLD.proposer_actor_id IS NEW.proposer_actor_id
          AND OLD.proposer_actor_kind IS NEW.proposer_actor_kind
          AND OLD.proposer_session IS NEW.proposer_session
          AND OLD.proposal_reason IS NEW.proposal_reason
          AND OLD.resolution IS NEW.resolution
          AND OLD.policy_version IS NEW.policy_version
          AND OLD.adjudicator_subject_id IS NEW.adjudicator_subject_id
          AND OLD.adjudicator_membership_id IS NEW.adjudicator_membership_id
          AND OLD.adjudicator_roles_json IS NEW.adjudicator_roles_json
          AND OLD.authentication_method IS NEW.authentication_method
          AND OLD.assurance_level IS NEW.assurance_level
          AND OLD.principal_authenticated_at IS NEW.principal_authenticated_at
          AND OLD.predecessor_id IS NEW.predecessor_id
          AND OLD.proposed_at IS NEW.proposed_at
          AND OLD.decided_at IS NEW.decided_at
        );
    END;
    `

// v5 tables and supporting objects — CREATE statements per
// docs/architecture/ed25519-receipts-step3.md "SQLite schema v5".

// signing_keys is the PUBLIC half of the signing-key lifecycle: SQLite stores
// only raw public keys; private Ed25519 seeds stay in the user-owned 0600
// keyring file. Public key and creation are immutable; deletion is forbidden;
// revocation is a one-way null → timestamp UPDATE (see the triggers below).
const signingKeysDDL = `
        CREATE TABLE signing_keys (
          key_id TEXT PRIMARY KEY,
          algorithm TEXT NOT NULL CHECK(algorithm='Ed25519'),
          public_key TEXT NOT NULL,
          created_at TEXT NOT NULL,
          revoked_at TEXT
        );
        `

// signing_keys_no_delete freezes the public-key ledger: a registered key is
// never deleted (revocation is the only lifecycle ending).
const signingKeysNoDeleteDDL = `
        CREATE TRIGGER signing_keys_no_delete BEFORE DELETE ON signing_keys BEGIN
          SELECT RAISE(ABORT,'IMMUTABLE_SIGNING_KEY'); END;
        `

// signing_keys_revoke_only allows EXACTLY ONE update shape: setting a
// previously-NULL revoked_at to a timestamp while every other column stays
// byte-equal. Re-revocation, un-revocation (back to NULL), and any touch of
// key_id / algorithm / public_key / created_at abort — the ledger is append-
// only and revocation is one-way.
const signingKeysRevokeOnlyDDL = `
        CREATE TRIGGER signing_keys_revoke_only
        BEFORE UPDATE ON signing_keys
        BEGIN
          SELECT RAISE(ABORT,'IMMUTABLE_SIGNING_KEY: only a one-way null-to-timestamp revocation is allowed')
            WHERE
              NEW.key_id IS NOT OLD.key_id OR
              NEW.algorithm IS NOT OLD.algorithm OR
              NEW.public_key IS NOT OLD.public_key OR
              NEW.created_at IS NOT OLD.created_at OR
              OLD.revoked_at IS NOT NULL OR
              NEW.revoked_at IS NULL;
        END;
        `

// receipts is the immutable signed-act ledger. Every covered act stores the
// frozen signed envelope (subject/action/scope/payload-hash/chain/principal/
// policy/algorithm/key), the RAW signature bytes, the canonical payload JSON,
// the derived unique receipt_hash (the chain links on it via
// previous_receipt_hash), and exactly ONE typed FK (memory_id for memory
// subjects, judgment_id for judgment subjects) that must equal the signed
// subject_id. No-update and no-delete triggers make receipt rows immutable;
// the unique (subject_type, subject_id, action, payload_hash) constraint
// makes duplicate emission a no-op retry.
const receiptsDDL = `
        CREATE TABLE receipts (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          subject_type TEXT NOT NULL CHECK(subject_type IN ('memory','judgment')),
          subject_id TEXT NOT NULL,
          action TEXT NOT NULL CHECK(action IN
            ('memory_recorded','memory_approved','memory_rejected','memory_voided',
             'relation_confirmed','relation_rejected','evidence_linked','memory_superseded')),
          tenant_id TEXT NOT NULL,
          company_id TEXT NOT NULL,
          fiscal_period_id TEXT NOT NULL,
          payload_hash TEXT NOT NULL,
          previous_receipt_hash TEXT NOT NULL,
          principal_id TEXT NOT NULL,
          membership_id TEXT NOT NULL,
          policy_version TEXT NOT NULL,
          algorithm TEXT NOT NULL CHECK(algorithm='Ed25519'),
          key_id TEXT NOT NULL REFERENCES signing_keys(key_id),
          signature BLOB NOT NULL,
          issued_at TEXT NOT NULL,
          payload_json TEXT NOT NULL,
          receipt_hash TEXT NOT NULL UNIQUE,
          memory_id TEXT REFERENCES observations(id),
          judgment_id TEXT REFERENCES judgments(id),
          UNIQUE(subject_type, subject_id, action, payload_hash),
          CHECK((memory_id IS NULL) <> (judgment_id IS NULL)),
          CHECK(COALESCE(memory_id, judgment_id) = subject_id)
        );
        `

// receipts_singleton guarantees at most one live receipt per (subject, action)
// for every action except evidence_linked (evidence links legitimately grow; a
// genuinely new link mints a new receipt, duplicates stay no-ops).
const receiptsSingletonIndexDDL = `CREATE UNIQUE INDEX uq_receipts_singleton
        ON receipts(subject_type, subject_id, action) WHERE action <> 'evidence_linked';`

const receiptsSubjectTimeIndexDDL = `CREATE INDEX idx_receipts_subject_time
        ON receipts(subject_type, subject_id, issued_at);`

const receiptsKeyTimeIndexDDL = `CREATE INDEX idx_receipts_key_time
        ON receipts(key_id, issued_at);`

// Receipt rows are frozen: no UPDATE and no DELETE ever (immutability is
// schema-enforced, a corrupt or buggy caller cannot mutate history).
const receiptsNoUpdateDDL = `
        CREATE TRIGGER receipts_no_update BEFORE UPDATE ON receipts BEGIN
          SELECT RAISE(ABORT,'IMMUTABLE_RECEIPT'); END;
        `

const receiptsNoDeleteDDL = `
        CREATE TRIGGER receipts_no_delete BEFORE DELETE ON receipts BEGIN
          SELECT RAISE(ABORT,'IMMUTABLE_RECEIPT'); END;
        `

// v3 tables and supporting objects — CREATE statements verbatim from
// docs/architecture/approval-principal-step1.md section 4.

const companiesDDL = `
CREATE TABLE companies (
  id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, ruc TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '', active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
  created_at TEXT NOT NULL,
  UNIQUE(tenant_id,id), UNIQUE(tenant_id,ruc)
);
`

const membershipsDDL = `
CREATE TABLE memberships (
  id TEXT PRIMARY KEY, subject_id TEXT NOT NULL, tenant_id TEXT NOT NULL,
  company_id TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active','inactive')),
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(tenant_id,id), UNIQUE(subject_id,tenant_id,company_id),
  FOREIGN KEY(tenant_id,company_id) REFERENCES companies(tenant_id,id)
);
`

const membershipRolesDDL = `
CREATE TABLE membership_roles (
  membership_id TEXT NOT NULL REFERENCES memberships(id),
  role TEXT NOT NULL CHECK(role IN ('accountant','senior_accountant','controller','tax_reviewer','authorized_tax_professional')),
  created_at TEXT NOT NULL, PRIMARY KEY(membership_id,role)
);
`

const sessionsDDL = `
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE,
  membership_id TEXT NOT NULL REFERENCES memberships(id),
  authentication_method TEXT NOT NULL CHECK(authentication_method IN ('session','service_assertion','local_dev')),
  assurance_level TEXT NOT NULL CHECK(assurance_level IN ('low','standard','strong')),
  authenticated_at TEXT NOT NULL, expires_at TEXT NOT NULL,
  revoked_at TEXT, created_at TEXT NOT NULL
);
`

const approvalEventsDDL = `
CREATE TABLE approval_events (
  id TEXT PRIMARY KEY, request_id TEXT NOT NULL, memory_id TEXT NOT NULL REFERENCES observations(id),
  tenant_id TEXT NOT NULL, company_id TEXT NOT NULL, fiscal_period_id TEXT,
  action TEXT NOT NULL CHECK(action='approved'),
  from_status TEXT NOT NULL CHECK(from_status='pending_review'),
  to_status TEXT NOT NULL CHECK(to_status='approved'),
  reviewed_envelope_hash TEXT NOT NULL, resulting_envelope_hash TEXT NOT NULL,
  reason TEXT NOT NULL, principal_subject_id TEXT NOT NULL,
  membership_id TEXT NOT NULL REFERENCES memberships(id), principal_roles_json TEXT NOT NULL,
  authentication_method TEXT NOT NULL, assurance_level TEXT NOT NULL,
  principal_authenticated_at TEXT NOT NULL, policy_version TEXT NOT NULL,
  authorization_reason_code TEXT NOT NULL CHECK(authorization_reason_code='AUTHORIZED'),
  created_at TEXT NOT NULL,
  UNIQUE(tenant_id,request_id), UNIQUE(memory_id)
);
`

const idempotencyKeysDDL = `
CREATE TABLE idempotency_keys (
  tenant_id TEXT NOT NULL, request_id TEXT NOT NULL,
  command_hash TEXT NOT NULL, principal_subject_id TEXT NOT NULL,
  membership_id TEXT NOT NULL, approval_event_id TEXT REFERENCES approval_events(id),
  result_json TEXT, created_at TEXT NOT NULL, completed_at TEXT,
  PRIMARY KEY(tenant_id,request_id),
  CHECK((approval_event_id IS NULL) = (result_json IS NULL))
);
`

const membershipsSubjectIndexDDL = `CREATE INDEX idx_memberships_subject ON memberships(subject_id,tenant_id,status);`

const sessionsMembershipIndexDDL = `CREATE INDEX idx_sessions_membership ON sessions(membership_id,expires_at);`

const approvalEventsMemoryIndexDDL = `CREATE INDEX idx_approval_events_memory ON approval_events(memory_id,created_at);`

const approvalEventsNoUpdateDDL = `
CREATE TRIGGER approval_events_no_update BEFORE UPDATE ON approval_events BEGIN
  SELECT RAISE(ABORT,'IMMUTABLE_APPROVAL_EVENT'); END;
`

const approvalEventsNoDeleteDDL = `
CREATE TRIGGER approval_events_no_delete BEFORE DELETE ON approval_events BEGIN
  SELECT RAISE(ABORT,'IMMUTABLE_APPROVAL_EVENT'); END;
`

// immutabilityTriggerV3DDL is the v3 observations guard: the v2 column list
// plus materiality_level (the declared classification is immutable content).
const immutabilityTriggerV3DDL = `
CREATE TRIGGER observations_immutable_content
BEFORE UPDATE OF id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
                     what, why, where_text, learned, fiscal_effect, effective_at, recorded_at, observed_at,
                     expires_at, actor, timestamp, source, session, source_json, content_hash,
                     evidence_refs_json, rule_refs_json, confidence, materiality, materiality_level, revision ON observations
BEGIN
    SELECT RAISE(ABORT, 'IMMUTABLE_OBSERVATION: content, scope and provenance never change after write');
END;
`

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

	// Auto-supersession receipt state: when the new revision supersedes the prior
	// current one, the design emits memory_superseded for the PRIOR subject FIRST
	// (pre/post envelope hashes + successor id), then memory_recorded for the new
	// subject — both inside this one transaction with the same captured timestamp.
	var (
		autoSuperseded         bool
		supersededFromEnvelope string
		supersededToEnvelope   string
	)
	if hasPrev {
		// Load the FULL previous revision (with its current link rows) through the
		// transaction BEFORE the supersede update: the receipt covers the act with
		// the envelope hashes BEFORE (status/supersedes_id as stored) and AFTER
		// (status superseded, supersedes_id = new id) — status and supersession
		// participate in the envelope, so they are recomputed, never cached.
		prev, ok := s.readMemoryWithLinks(ctx, tx, prevID)
		if !ok {
			return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: read previous revision row: %w", err)
		}
		prevEnvelope := core.ComputeEnvelopeHash(prev)
		if err := core.SupersedePrev(&prev, id); err == nil {
			// The auto-supersession is a lifecycle transition: it must trace to
			// actor + actorKind + timestamp in the audit trail (lifecycle.md
			// rule 4, provenance.md rule 3) so sync can replay it.
			if _, err := tx.ExecContext(ctx,
				`UPDATE observations SET status = ?, supersedes_id = ? WHERE id = ?`,
				string(prev.Status), prev.SupersedesID, prevID,
			); err != nil {
				return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: supersede previous revision: %w", err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO transition_log (observation_id, from_status, to_status, actor, actor_kind, timestamp) VALUES (?, ?, ?, ?, ?, ?)`,
				prevID, string(prevStatus), string(core.StatusSuperseded), input.Source.ActorID, string(input.Source.ActorKind), recordedAt,
			); err != nil {
				return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: record supersede audit: %w", err)
			}
			autoSuperseded = true
			supersededFromEnvelope = prevEnvelope
			supersededToEnvelope = core.ComputeEnvelopeHash(prev) // SupersedePrev mutated status + supersedes_id
		} else if !errors.Is(err, core.ErrInvalidTransition) {
			// Only terminal predecessors are skipped; anything else is a bug.
			return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, err
		}
	}

	_, err = tx.ExecContext(ctx, `
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
		nullableFloat(memory.Confidence), nullableInt(memory.Materiality), nullableMaterialityLevel(memory.MaterialityLevel), memory.ReceiptID, memory.SupersedesID,
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

	// Atomic receipt emission (v0.4.0 Step 3): inside the SAME transaction, before
	// COMMIT, with the captured recordedAt — never a fresh time call. A signing
	// failure returns an error and rolls the whole save back (no act, no receipt).
	// If auto-supersession changed the prior observation, memory_superseded for the
	// prior subject is emitted FIRST (it chains on the prior's own receipts), then
	// memory_recorded for the new subject. The new memory's envelope hash is
	// recomputed fresh (the stored envelope cache is not trusted).
	if autoSuperseded {
		if _, err := s.emitReceipt(ctx, tx, core.SubjectTypeMemory, prevID, core.ReceiptActionMemorySuperseded, core.ReceiptPayload{
			TenantID:         memory.Scope.OrganizationID,
			CompanyID:        memory.Scope.CompanyID,
			FiscalPeriodID:   memory.Scope.Period,
			FromEnvelopeHash: supersededFromEnvelope,
			ToEnvelopeHash:   supersededToEnvelope,
			SuccessorID:      id,
			PrincipalID:      input.Source.ActorID,
			PolicyVersion:    kernelPolicyVersion,
		}, recordedAt); err != nil {
			return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: emit superseded receipt: %w", err)
		}
	}
	if _, err := s.emitReceipt(ctx, tx, core.SubjectTypeMemory, id, core.ReceiptActionMemoryRecorded, core.ReceiptPayload{
		TenantID:              memory.Scope.OrganizationID,
		CompanyID:             memory.Scope.CompanyID,
		FiscalPeriodID:        memory.Scope.Period,
		ResultingEnvelopeHash: core.ComputeEnvelopeHash(memory),
		PrincipalID:           input.Source.ActorID,
		PolicyVersion:         kernelPolicyVersion,
	}, recordedAt); err != nil {
		return core.WriteResult{Memory: memory, Outcome: core.WriteUnknown}, fmt.Errorf("persistence error: emit recorded receipt: %w", err)
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
	var materialityLevel *core.MaterialityLevel
	if input.MaterialityLevel != nil {
		ml := *input.MaterialityLevel
		materialityLevel = &ml
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
		// MaterialityLevel is the DECLARED classification (normal | material |
		// critical), set by the writing agent (v3 column). It does NOT
		// participate in the envelope hash (frozen decision).
		MaterialityLevel: materialityLevel,
		ReceiptID:        input.ReceiptID,
		Revision:         revision,
	}
	memory.ContentHash = core.ComputeContentHash(memory)
	return memory
}

func nullableMaterialityLevel(v *core.MaterialityLevel) any {
	if v == nil {
		return nil
	}
	return string(*v)
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

// validitySource returns the vigencia provenance for WRITTEN v2 memories:
// a caller-supplied vigencia is "declared". Migrated rows carry
// "migrated_from_effective_at_v1" (set by the backfill).
// migratedValiditySource marks the vigencia provenance of a v1 row: the v1
// effective_at doubled as the vigencia start, so an inferred vigencia is
// explicitly recorded as such — it never masquerades as declared data.
func migratedValiditySource(effAt string) string {
	if effAt == "" {
		return ""
	}
	return "migrated_from_effective_at_v1"
}

func validitySource(v *core.Validity) string {
	if v == nil || (v.EffectiveAt == "" && v.ExpiresAt == "") {
		return ""
	}
	if v.Source != "" {
		return v.Source
	}
	return "declared"
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
	expires_at, validity_effective_at, validity_source, actor, timestamp, source, session, source_json, content_hash, identity_hash, envelope_hash, evidence_refs_json, rule_refs_json,
	confidence, materiality, materiality_level, receipt_id, supersedes_id, revision`

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
		kind, status, fiscalEffect, observedAt, validityEffectiveAtVal, validitySourceVal         sql.NullString
		sourceJSON, contentHash, identityHashVal, envelopeHashVal, evidenceRefsJSON, ruleRefsJSON sql.NullString
		supersedesID, receiptID, materialityLevelVal                                              sql.NullString
		confidence                                                                                sql.NullFloat64
		materiality                                                                               sql.NullInt64
	)
	if err := rs.Scan(
		&id, &topicKey, &title, &typ, &kind, &scopeKind, &orgID, &companyID, &ruc, &period,
		&what, &why, &whereText, &learned, &authorityStatus, &status, &fiscalEffect, &effAt, &recordedAt, &observedAt,
		&expiresAt, &validityEffectiveAtVal, &validitySourceVal, &actor, &timestamp, &source, &session, &sourceJSON, &contentHash, &identityHashVal, &envelopeHashVal, &evidenceRefsJSON, &ruleRefsJSON,
		&confidence, &materiality, &materialityLevelVal, &receiptID, &supersedesID, &revision,
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
		IdentityHash: identityHashVal.String,
		EnvelopeHash: envelopeHashVal.String,
		ReceiptID:    receiptID.String,
		SupersedesID: supersedesID.String,
		Revision:     revision,
	}
	if expiresAt != "" || validityEffectiveAtVal.String != "" {
		memory.Validity = &core.Validity{EffectiveAt: validityEffectiveAtVal.String, ExpiresAt: expiresAt, Source: validitySourceVal.String}
	}
	if confidence.Valid {
		v := confidence.Float64
		memory.Confidence = &v
	}
	if materiality.Valid {
		v := materiality.Int64
		memory.Materiality = &v
	}
	if materialityLevelVal.Valid && materialityLevelVal.String != "" {
		l := core.MaterialityLevel(materialityLevelVal.String)
		memory.MaterialityLevel = &l
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

// ──────────────────────────────────────────────
// Authenticated approval (v0.4.0 Step 1, ADR-003)
// ──────────────────────────────────────────────

// Queryer is the statement surface the approval, judgment, link and receipt
// writers run through: *sql.Conn, *sql.Tx and *sql.DB satisfy it, so every read
// and write of an atomic unit stays on the SAME connection/transaction and the
// transaction boundary is explicit (the receipt surfaces NEVER start or commit
// one — the caller's tx owns atomicity).
type Queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// readMemoryWithLinks reads an observation row and merges the current
// evidence/rule link rows THROUGH the given connection (the approval and link
// paths reuse the withLinks merge scoped to their own transaction — never a
// separate pooled connection).
func (s *SQLiteStore) readMemoryWithLinks(ctx context.Context, q Queryer, id string) (core.AccountingMemory, bool) {
	row := q.QueryRowContext(ctx, `SELECT `+memoryColumns+` FROM observations WHERE id = ?`, id)
	memory, err := scanMemory(row)
	if err != nil {
		return core.AccountingMemory{}, false
	}
	memory.EvidenceRefs = mergeRefs(memory.EvidenceRefs, linkRefsQuery(ctx, q, `evidence_links`, memory.Identity.ID))
	memory.RuleRefs = mergeRefs(memory.RuleRefs, linkRefsQuery(ctx, q, `rule_links`, memory.Identity.ID))
	return memory, true
}

// linkRefsQuery returns the link rows of a memory in insertion order, scoped to
// the given connection.
func linkRefsQuery(ctx context.Context, q Queryer, table, memoryID string) []string {
	rows, err := q.QueryContext(ctx, `SELECT ref FROM `+table+` WHERE memory_id = ? ORDER BY rowid`, memoryID)
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

// refreshEnvelopeCache recomputes the DERIVED envelope cache of memoryID from
// the CURRENT row plus the CURRENT link rows (never from the stored hash) and
// persists it on the given connection. The persisted observations.envelope_hash
// is a cache only: approval always recomputes H1 fresh inside its own locked
// transaction (design §5 — the cache is not trusted).
func refreshEnvelopeCache(ctx context.Context, q Queryer, memoryID string) error {
	memory, err := scanMemory(q.QueryRowContext(ctx, `SELECT `+memoryColumns+` FROM observations WHERE id = ?`, memoryID))
	if err != nil {
		return fmt.Errorf("persistence error: refresh envelope cache read: %w", err)
	}
	memory.EvidenceRefs = mergeRefs(memory.EvidenceRefs, linkRefsQuery(ctx, q, `evidence_links`, memoryID))
	memory.RuleRefs = mergeRefs(memory.RuleRefs, linkRefsQuery(ctx, q, `rule_links`, memoryID))
	hash := core.ComputeEnvelopeHash(memory)
	if _, err := q.ExecContext(ctx, `UPDATE observations SET envelope_hash = ? WHERE id = ?`, hash, memoryID); err != nil {
		return fmt.Errorf("persistence error: refresh envelope cache update: %w", err)
	}
	return nil
}

// approveCommandHash is the canonical idempotency command hash: SHA-256 hex of
// memoryId NUL lowercase(expectedEnvelopeHash) NUL exact reason.
func approveCommandHash(memoryID, expectedEnvelopeHash, reason string) string {
	canonical := memoryID + "\x00" + strings.ToLower(expectedEnvelopeHash) + "\x00" + reason
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func principalHasCompanyScope(p auth.VerifiedApprovalPrincipal, companyID string) bool {
	for _, c := range p.CompanyScopes() {
		if c == companyID {
			return true
		}
	}
	return false
}

// IdentitySeed describes the identity rows the authenticated approval path needs
// before a principal can approve: one company, one membership and its roles. It
// exists so tests and the local-dev seed flow never depend on environment state
// (design §8); production transports never call it with caller data.
type IdentitySeed struct {
	TenantID     string
	CompanyID    string
	CompanyRUC   string
	CompanyName  string
	MembershipID string
	SubjectID    string
	Roles        []auth.AccountingRole
}

// SeedIdentity inserts the company, membership and role rows for an identity
// fixture in ONE transaction (FK order: companies → memberships →
// membership_roles). Duplicate rows fail loudly — seeding is explicit, never a
// silent overwrite.
func (s *SQLiteStore) SeedIdentity(seed IdentitySeed) error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("seed identity: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	now := nowISO()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO companies (id, tenant_id, ruc, name, active, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		seed.CompanyID, seed.TenantID, seed.CompanyRUC, seed.CompanyName, now,
	); err != nil {
		return fmt.Errorf("seed identity: company: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memberships (id, subject_id, tenant_id, company_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, 'active', ?, ?)`,
		seed.MembershipID, seed.SubjectID, seed.TenantID, seed.CompanyID, now, now,
	); err != nil {
		return fmt.Errorf("seed identity: membership: %w", err)
	}
	for _, role := range seed.Roles {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO membership_roles (membership_id, role, created_at) VALUES (?, ?, ?)`,
			seed.MembershipID, string(role), now,
		); err != nil {
			return fmt.Errorf("seed identity: role %s: %w", role, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("seed identity: commit: %w", err)
	}
	committed = true
	return nil
}

// ApproveMemory atomically approves a pending_review memory against the caller's
// expected envelope hash — THE authenticated approval path (v0.4.0 Step 1,
// ADR-003). Do NOT compose FindByID + ApplyStatusTransition for approval: that
// split is a TOCTOU hole (the low-level update does not compare the old status).
// This operation owns the whole state change inside ONE BEGIN IMMEDIATE
// transaction on a dedicated connection:
//
//	idempotency reservation → locked re-read of row + links → scope checks →
//	status check → fresh H1 recompute vs expected → pure policy → guarded
//	status flip + envelope cache update → immutable approval event + legacy
//	transition row → completed reservation → commit.
//
// BEGIN IMMEDIATE (not BeginTx(nil), which defers the write lock) takes
// SQLite's reserved writer lock BEFORE any race-sensitive read; MaxOpenConns(1)
// only serializes THIS process, not another process on the same WAL file. A
// concurrent loser waits at BEGIN IMMEDIATE, reads the committed approved
// status and returns ALREADY_DECIDED; its reservation rolls back. A retry with
// the same request id + payload replays the committed result with
// IdempotentReplay=true. All failure codes are the frozen codes of
// internal/auth/errors.go.
func (s *SQLiteStore) ApproveMemory(ctx context.Context, cmd core.ApproveMemoryCommand, principal auth.VerifiedApprovalPrincipal, policy authz.ApprovalAuthorizationPolicy) (core.ApprovalResult, error) {
	// Syntax guards (defense in depth — the service validates first): an
	// incomplete command or a missing reason fails closed before any lock.
	if strings.TrimSpace(cmd.Reason) == "" {
		return core.ApprovalResult{}, auth.New(auth.CodeReasonRequired, "a reason is required for approval")
	}
	if strings.TrimSpace(cmd.MemoryID) == "" || strings.TrimSpace(cmd.ExpectedEnvelopeHash) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.ApprovalResult{}, auth.New(auth.CodeMemoryNotFound, "approval command is incomplete (memoryId, expectedEnvelopeHash and requestId are required)")
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// BEGIN IMMEDIATE is the write intent (design §5): SQLite's reserved writer
	// lock is taken here, before any race-sensitive read.
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	now := nowISO()
	commandHash := approveCommandHash(cmd.MemoryID, cmd.ExpectedEnvelopeHash, cmd.Reason)

	// 1. Idempotency: one reservation per (tenant, requestId).
	var (
		storedHash, storedSubject, storedMembership string
		storedResultJSON, completedAt               sql.NullString
	)
	err = conn.QueryRowContext(ctx, `
		SELECT command_hash, principal_subject_id, membership_id, result_json, completed_at
		FROM idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		principal.TenantID(), cmd.RequestID,
	).Scan(&storedHash, &storedSubject, &storedMembership, &storedResultJSON, &completedAt)
	switch {
	case err == nil:
		// The reservation exists: the command AND the principal binding must
		// match exactly, else the request id was reused for a different intent.
		if storedHash != commandHash || storedSubject != principal.SubjectID() || storedMembership != principal.MembershipID() {
			return core.ApprovalResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different command or principal")
		}
		if completedAt.Valid {
			// Completed replay: decode the stored result and mark it as such.
			var replay core.ApprovalResult
			if err := json.Unmarshal([]byte(storedResultJSON.String), &replay); err != nil {
				return core.ApprovalResult{}, fmt.Errorf("persistence error: decode replayed approval result: %w", err)
			}
			replay.IdempotentReplay = true
			return replay, nil
		}
		// Incomplete reservation (an interrupted attempt that never committed):
		// reuse it — the memory re-check below decides ALREADY_DECIDED when the
		// memory was decided by another request.
	case errors.Is(err, sql.ErrNoRows):
		// 2. Reserve: command_hash plus the compared principal binding; the
		// result/completion stay NULL until the approval commits.
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO idempotency_keys (tenant_id, request_id, command_hash, principal_subject_id, membership_id, approval_event_id, result_json, created_at, completed_at)
			VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			principal.TenantID(), cmd.RequestID, commandHash, principal.SubjectID(), principal.MembershipID(), now,
		); err != nil {
			return core.ApprovalResult{}, fmt.Errorf("persistence error: reserve idempotency key: %w", err)
		}
	default:
		return core.ApprovalResult{}, fmt.Errorf("persistence error: read idempotency key: %w", err)
	}

	// 3. Read the observation row + all evidence/rule refs THROUGH the same
	// connection (the withLinks merge is scoped to this transaction).
	memory, ok := s.readMemoryWithLinks(ctx, conn, cmd.MemoryID)
	if !ok {
		return core.ApprovalResult{}, auth.New(auth.CodeMemoryNotFound, "memory not found: "+cmd.MemoryID)
	}

	// 4. Derive tenant/company/period from the row's scope (never caller
	// claims). Institutional memories have no company to authorize.
	if memory.Scope.Kind != core.ScopeKindCompany {
		return core.ApprovalResult{}, auth.New(auth.CodeCompanyScopeDenied, "institutional memories cannot be approved by a company-scoped principal")
	}
	if principal.TenantID() != memory.Scope.OrganizationID {
		return core.ApprovalResult{}, auth.New(auth.CodeTenantScopeMismatch, "principal tenant does not match the memory tenant")
	}
	if !principalHasCompanyScope(principal, memory.Scope.CompanyID) {
		return core.ApprovalResult{}, auth.New(auth.CodeCompanyScopeDenied, "company is outside the principal's scope")
	}

	// 5. Status gate: only pending_review can be approved; a decided memory is
	// ALREADY_DECIDED (the concurrent loser lands here after the winner
	// commits); anything else is an invalid transition.
	switch memory.Status {
	case core.StatusPendingReview:
		// proceed
	case core.StatusApproved, core.StatusRejected:
		return core.ApprovalResult{}, auth.New(auth.CodeAlreadyDecided, "memory is already decided")
	default:
		return core.ApprovalResult{}, auth.New(auth.CodeInvalidTransition, fmt.Sprintf("approval is not legal from status %q", memory.Status))
	}

	// 6. H1 is recomputed FRESH from the locked row + current canonical refs —
	// never from the stored envelope cache (the cache is derived and can be
	// stale; the read merge combines stored refs + link rows). A mismatch
	// returns ENVELOPE_MISMATCH carrying ONLY the two hashes, never content.
	h1 := core.ComputeEnvelopeHash(memory)
	if !strings.EqualFold(strings.TrimSpace(cmd.ExpectedEnvelopeHash), h1) {
		return core.ApprovalResult{}, auth.NewEnvelopeMismatch(cmd.ExpectedEnvelopeHash, h1, "memory envelope changed after review; expected hash does not match the current envelope")
	}

	// 7. Pure policy in-transaction: any denial rolls back the reservation and
	// returns its frozen reason code.
	decision := policy.Authorize(principal, memory)
	if !decision.Allowed {
		return core.ApprovalResult{}, auth.New(decision.ReasonCode, "authorization policy denied the approval")
	}

	// 8. H2 is computed from the same snapshot with status=approved and must
	// differ from H1 (status participates in the envelope hash). The guarded
	// UPDATE requires EXACTLY one pending_review row — the write lock makes a
	// lost update impossible; the guard is a final invariant check.
	approvedSnapshot := memory
	approvedSnapshot.Status = core.StatusApproved
	h2 := core.ComputeEnvelopeHash(approvedSnapshot)
	if h2 == h1 {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: resulting envelope equals reviewed envelope — status change did not affect the hash")
	}
	res, err := conn.ExecContext(ctx,
		`UPDATE observations SET status = ?, authority_status = ?, envelope_hash = ? WHERE id = ? AND status = ?`,
		string(core.StatusApproved), legacyStatusFor(core.StatusApproved), h2, cmd.MemoryID, string(core.StatusPendingReview),
	)
	if err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: approve update: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: approve rows affected: %w", err)
	}
	if affected != 1 {
		return core.ApprovalResult{}, auth.New(auth.CodeInvalidTransition, "guarded status update did not match exactly one pending_review row")
	}

	// 9. The immutable approval event + the legacy transition mirror, sharing
	// ONE captured UTC timestamp. The event's principal fields come from the
	// canonical snapshot (roles sorted/deduplicated before JSON encoding).
	eventID, err := newUUID()
	if err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: generate approval event id: %w", err)
	}
	snapshot := principal.PrincipalSnapshot()
	rolesJSON, err := json.Marshal(snapshot.Roles)
	if err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: encode principal roles: %w", err)
	}
	var fiscalPeriodID any
	if memory.Scope.Period != "" {
		fiscalPeriodID = memory.Scope.Period
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO approval_events (
			id, request_id, memory_id, tenant_id, company_id, fiscal_period_id,
			action, from_status, to_status, reviewed_envelope_hash, resulting_envelope_hash,
			reason, principal_subject_id, membership_id, principal_roles_json,
			authentication_method, assurance_level, principal_authenticated_at,
			policy_version, authorization_reason_code, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID, cmd.RequestID, cmd.MemoryID, memory.Scope.OrganizationID, memory.Scope.CompanyID, fiscalPeriodID,
		"approved", string(core.StatusPendingReview), string(core.StatusApproved), h1, h2,
		cmd.Reason, snapshot.SubjectID, snapshot.MembershipID, string(rolesJSON),
		string(snapshot.AuthenticationMethod), string(snapshot.AssuranceLevel), snapshot.AuthenticatedAt,
		decision.PolicyVersion, decision.ReasonCode, now,
	); err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: insert approval event: %w", err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO transition_log (observation_id, from_status, to_status, actor, actor_kind, timestamp) VALUES (?, ?, ?, ?, ?, ?)`,
		cmd.MemoryID, string(core.StatusPendingReview), string(core.StatusApproved), principal.SubjectID(), string(core.ActorKindHuman), now,
	); err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: record approval transition: %w", err)
	}

	// Atomic receipt emission (v0.4.0 Step 3): after the event + transition
	// insertion and BEFORE the idempotency completion, inside the SAME transaction
	// with the captured now. memory_approved carries H1/H2, the reason and the
	// complete verified principal snapshot; a signing failure rolls the whole
	// approval back (no event, no receipt).
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeMemory, cmd.MemoryID, core.ReceiptActionMemoryApproved, core.ReceiptPayload{
		TenantID:                 memory.Scope.OrganizationID,
		CompanyID:                memory.Scope.CompanyID,
		FiscalPeriodID:           memory.Scope.Period,
		ReviewedEnvelopeHash:     h1,
		ResultingEnvelopeHash:    h2,
		Reason:                   cmd.Reason,
		PrincipalID:              snapshot.SubjectID,
		MembershipID:             snapshot.MembershipID,
		PrincipalRoles:           receiptPrincipalRoles(snapshot),
		AuthenticationMethod:     string(snapshot.AuthenticationMethod),
		AssuranceLevel:           string(snapshot.AssuranceLevel),
		PrincipalAuthenticatedAt: snapshot.AuthenticatedAt,
		PolicyVersion:            decision.PolicyVersion,
	}, now); err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: emit approval receipt: %w", err)
	}

	result := core.ApprovalResult{
		MemoryID:              cmd.MemoryID,
		ApprovalEventID:       eventID,
		PreviousStatus:        string(core.StatusPendingReview),
		CurrentStatus:         string(core.StatusApproved),
		ReviewedEnvelopeHash:  h1,
		ResultingEnvelopeHash: h2,
		PrincipalSubjectID:    snapshot.SubjectID,
		MembershipID:          snapshot.MembershipID,
		PolicyVersion:         decision.PolicyVersion,
		ApprovedAt:            now,
		IdempotentReplay:      false,
	}

	// 10. Complete the reservation (result + event link + completion time) and
	// commit — the whole approval is one atomic unit. The CHECK on the table
	// requires approval_event_id and result_json to be set together.
	serializedResult, err := json.Marshal(result)
	if err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: encode approval result: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE idempotency_keys SET result_json = ?, completed_at = ?, approval_event_id = ? WHERE tenant_id = ? AND request_id = ?`,
		string(serializedResult), now, eventID, principal.TenantID(), cmd.RequestID,
	); err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: complete idempotency key: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.ApprovalResult{}, fmt.Errorf("persistence error: commit approval: %w", err)
	}
	committed = true
	return result, nil
}

// ──────────────────────────────────────────────
// Atomic judgment adjudication (v0.4.0 Step 2)
// ──────────────────────────────────────────────

// judgmentColumns is the column list of the v4 judgments table (design §4),
// mirrored field-for-field by scanJudgment.
const judgmentColumns = `id, tenant_id, company_id, fiscal_period_id, from_id, to_id, relation, status,
	proposer_system, proposer_actor_id, proposer_actor_kind, proposer_session, proposal_reason,
	resolution, policy_version, adjudicator_subject_id, adjudicator_membership_id, adjudicator_roles_json,
	authentication_method, assurance_level, principal_authenticated_at, predecessor_id, supersedes_id,
	proposed_at, updated_at, decided_at`

// scanJudgment decodes a judgments row into the core entity. The proposer is
// reconstructed from the four stored provenance columns ONLY (system, actorId,
// actorKind, session) — the canonical identity the design compares — so the
// decoded entity is byte-identical to the entity the store returns from its own
// constructed results.
func scanJudgment(rs rowScanner) (core.AccountingJudgment, error) {
	var (
		id, tenantID, companyID, fromID, toID, relation, status             string
		proposerSystem, proposerActorID, proposerActorKind, proposerSession string
		proposalReason, proposedAt, updatedAt                               string
	)
	var (
		fiscalPeriodID, resolution, policyVersion                  sql.NullString
		adjSubject, adjMembership, adjRoles, authMethod, assurance sql.NullString
		authAt, predecessorID, supersedesID, decidedAt             sql.NullString
	)
	if err := rs.Scan(
		&id, &tenantID, &companyID, &fiscalPeriodID, &fromID, &toID, &relation, &status,
		&proposerSystem, &proposerActorID, &proposerActorKind, &proposerSession, &proposalReason,
		&resolution, &policyVersion, &adjSubject, &adjMembership, &adjRoles, &authMethod, &assurance, &authAt,
		&predecessorID, &supersedesID, &proposedAt, &updatedAt, &decidedAt,
	); err != nil {
		return core.AccountingJudgment{}, err
	}
	j := core.AccountingJudgment{
		ID:             id,
		TenantID:       tenantID,
		CompanyID:      companyID,
		FiscalPeriodID: fiscalPeriodID.String,
		FromID:         fromID,
		ToID:           toID,
		Relation:       core.Relation(relation),
		Status:         core.JudgmentStatus(status),
		Proposer: core.Source{
			System:    proposerSystem,
			ActorID:   proposerActorID,
			ActorKind: core.ActorKind(proposerActorKind),
			Session:   proposerSession,
		},
		ProposalReason: proposalReason,
		Resolution:     resolution.String,
		PolicyVersion:  policyVersion.String,
		PredecessorID:  predecessorID.String,
		SupersedesID:   supersedesID.String,
		ProposedAt:     proposedAt,
		UpdatedAt:      updatedAt,
		DecidedAt:      decidedAt.String,
	}
	if adjSubject.Valid && adjSubject.String != "" {
		roles := make([]auth.AccountingRole, 0)
		_ = json.Unmarshal([]byte(adjRoles.String), &roles)
		j.Adjudicator = &auth.PrincipalSnapshot{
			SubjectID:            adjSubject.String,
			MembershipID:         adjMembership.String,
			Roles:                roles,
			AuthenticationMethod: auth.AuthenticationMethod(authMethod.String),
			AssuranceLevel:       auth.AssuranceLevel(assurance.String),
			AuthenticatedAt:      authAt.String,
		}
	}
	return j, nil
}

// readJudgment reads one judgment row THROUGH the given connection, so every
// race-sensitive read of an adjudication stays inside its own transaction.
func (s *SQLiteStore) readJudgment(ctx context.Context, q Queryer, id string) (core.AccountingJudgment, bool) {
	row := q.QueryRowContext(ctx, `SELECT `+judgmentColumns+` FROM judgments WHERE id = ?`, id)
	j, err := scanJudgment(row)
	if err != nil {
		return core.AccountingJudgment{}, false
	}
	return j, true
}

// storedJudgmentResult is the store-private JSON shape stored in
// judgment_idempotency_keys.result_json for a completed decision (confirm /
// reject / withdraw): the resulting judgment plus the immutable event id. The
// IdempotentReplay marker is set at DECODE time, never persisted, so the
// stored bytes stay the exact original outcome.
type storedJudgmentResult struct {
	JudgmentID      string                  `json:"judgmentId"`
	Judgment        core.AccountingJudgment `json:"judgment"`
	JudgmentEventID string                  `json:"judgmentEventId"`
}

// proposerBinding is the canonical identity string of a provenance Source
// (system NUL actorId NUL actorKind NUL session) — the EXACT identity the
// design requires for same-proposer supersession and withdrawal (design §3.7):
// Source is provenance continuity, never professional authority.
func proposerBinding(caller core.Source) string {
	return strings.Join([]string{caller.System, caller.ActorID, string(caller.ActorKind), caller.Session}, "\x00")
}

// proposeJudgmentCommandHash is the canonical idempotency command hash of a
// proposal: SHA-256 hex of fromId NUL toId NUL relation NUL reason NUL
// predecessorId. RequestID is the KEY, not part of the payload.
func proposeJudgmentCommandHash(cmd core.ProposeJudgmentCommand) string {
	canonical := cmd.FromID + "\x00" + cmd.ToID + "\x00" + string(cmd.Relation) + "\x00" + cmd.Reason + "\x00" + cmd.PredecessorID
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// decideJudgmentCommandHash is the canonical idempotency command hash of a
// confirm/reject decision: judgmentId NUL lowercase(expectedHash) NUL
// resolution — mirroring approveCommandHash.
func decideJudgmentCommandHash(judgmentID, expectedHash, resolution string) string {
	canonical := judgmentID + "\x00" + strings.ToLower(expectedHash) + "\x00" + resolution
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// isOpenTupleConflict reports whether err is the uq_judgment_open_tuple partial
// unique index violation (SQLite extended result code SQLITE_CONSTRAINT_UNIQUE =
// 2067). The judgments INSERT can only trip TWO unique constraints — the id
// primary key and the open-tuple partial index; a generated-UUID id collision is
// effectively impossible, so the extended code alone identifies the
// JUDGMENT_CONFLICT case without depending on SQLite's message wording (which
// lists the TABLE columns, not the index name).
func isOpenTupleConflict(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == 2067
}

// withdrawJudgmentCommandHash is the canonical idempotency command hash of a
// withdrawal: SHA-256 hex of the judgment id (a withdrawal has no payload).
func withdrawJudgmentCommandHash(judgmentID string) string {
	sum := sha256.Sum256([]byte(judgmentID))
	return hex.EncodeToString(sum[:])
}

// ProposeJudgment atomically creates an OPEN proposal over two observations —
// the proposal half of the adjudication machine (design §3). The caller Source
// is provenance ONLY (agent|system); it never authorizes. Tenant/company are
// DERIVED from the observations' scopes, never from caller claims; the
// (tenant, requestId) reservation makes a same-request retry replay the
// original proposal while a different payload returns IDEMPOTENCY_CONFLICT and
// a second open proposal for the tuple returns JUDGMENT_CONFLICT (the partial
// unique index is the arbiter). A proposal writes NO judgment event: the frozen
// events CHECK admits only confirm|reject|withdraw|supersede, so the
// reservation completes with result/event NULL and a replay re-derives the
// proposal from the (tenant, company, from, to, relation) tuple.
func (s *SQLiteStore) ProposeJudgment(ctx context.Context, cmd core.ProposeJudgmentCommand, caller core.Source) (core.ProposeJudgmentResult, error) {
	// Syntax guards (defense in depth — the service validates first): an
	// incomplete command, a non-proposable relation or a non-agent/system
	// proposer fails closed before any lock.
	if strings.TrimSpace(cmd.FromID) == "" || strings.TrimSpace(cmd.ToID) == "" ||
		strings.TrimSpace(cmd.Reason) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeMemoryNotFound, "proposal command is incomplete (fromId, toId, reason and requestId are required)")
	}
	if !core.IsProposableRelation(cmd.Relation) {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeRelationNotProposable, fmt.Sprintf("relation %q is not proposable (supports|contradicts|explains|reconciles|reverses|supersedes only)", cmd.Relation))
	}
	if !core.CanPropose(caller) {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeProposalUnauthorized, "only agents and systems may propose judgments (provenance, never authority)")
	}
	if cmd.FromID == cmd.ToID {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeMemoryNotFound, "a judgment requires two DISTINCT observations (fromId and toId must differ)")
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.ProposeJudgmentResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// BEGIN IMMEDIATE is the write intent: the reserved writer lock is taken
	// before any race-sensitive read (design §3 — one open proposal per tuple).
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.ProposeJudgmentResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	// defer_foreign_keys postpones ALL FK enforcement of this transaction to
	// COMMIT: the idempotency reservation references the judgment row that is
	// created later in the same transaction (and the proposed-predecessor
	// supersession crosses rows in the same way). At COMMIT every FK is
	// re-checked, so no dangling reference can survive.
	if _, err := conn.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return core.ProposeJudgmentResult{}, fmt.Errorf("persistence error: defer foreign keys: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	now := nowISO()
	commandHash := proposeJudgmentCommandHash(cmd)
	binding := proposerBinding(caller)

	// 1. Both observations must exist; tenant/company/period are derived from
	// their scopes (the pair must be coherent: same tenant, same company;
	// cross-period pairs are allowed and keep fiscal_period_id NULL).
	from, okFrom := s.readMemoryWithLinks(ctx, conn, cmd.FromID)
	to, okTo := s.readMemoryWithLinks(ctx, conn, cmd.ToID)
	if !okFrom || !okTo {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeMemoryNotFound, "a judgment requires two existing observations")
	}
	if from.Scope.Kind != core.ScopeKindCompany || to.Scope.Kind != core.ScopeKindCompany {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeCompanyScopeDenied, "institutional observations have no company to adjudicate")
	}
	if from.Scope.OrganizationID != to.Scope.OrganizationID {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeTenantScopeMismatch, "judgment observations must belong to the same tenant")
	}
	if from.Scope.CompanyID != to.Scope.CompanyID {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeCompanyScopeDenied, "judgment observations must belong to the same company")
	}
	tenantID, companyID := from.Scope.OrganizationID, from.Scope.CompanyID
	fiscalPeriod := ""
	if from.Scope.Period != "" && from.Scope.Period == to.Scope.Period {
		fiscalPeriod = from.Scope.Period
	}

	// The judgment id is generated BEFORE the idempotency reservation so the
	// reservation can record the exact judgment it will create (the replay then
	// returns this original row, never a newer proposal of the same tuple).
	id, err := newUUID()
	if err != nil {
		return core.ProposeJudgmentResult{}, fmt.Errorf("persistence error: generate judgment id: %w", err)
	}

	// 2. Idempotency by (tenant, requestId), bound to the exact proposer
	// identity. A completed reservation replays the original proposal; an
	// incomplete one (an interrupted attempt) is reused and the open-tuple
	// index decides below.
	var storedHash, storedBinding string
	var storedResultJSON, storedJudgmentID, completedAt sql.NullString
	err = conn.QueryRowContext(ctx, `
		SELECT command_hash, actor_binding, judgment_id, result_json, completed_at
		FROM judgment_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		tenantID, cmd.RequestID,
	).Scan(&storedHash, &storedBinding, &storedJudgmentID, &storedResultJSON, &completedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedBinding != binding {
			return core.ProposeJudgmentResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different proposal or proposer")
		}
		if completedAt.Valid {
			// Replay: return the ORIGINAL proposal the reservation created. The
			// reservation stores the judgment id it produced, so a same-request
			// retry always replays that exact row — never a newer proposal of the
			// same tuple. The tuple re-derivation remains only as a defensive
			// fallback for reservations created before judgment_id was recorded.
			if storedJudgmentID.Valid {
				if j, ok := s.readJudgment(ctx, conn, storedJudgmentID.String); ok {
					return core.ProposeJudgmentResult{JudgmentID: j.ID, Judgment: j, IdempotentReplay: true}, nil
				}
			}
			j, ok := s.readOpenProposal(ctx, conn, tenantID, companyID, cmd.FromID, cmd.ToID, cmd.Relation)
			if !ok {
				j, ok = s.readLatestTupleJudgment(ctx, conn, tenantID, companyID, cmd.FromID, cmd.ToID, cmd.Relation)
			}
			if !ok {
				return core.ProposeJudgmentResult{}, auth.New(auth.CodeJudgmentNotFound, "proposal reservation completed but no judgment row found for the tuple")
			}
			return core.ProposeJudgmentResult{JudgmentID: j.ID, Judgment: j, IdempotentReplay: true}, nil
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO judgment_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, judgment_id, result_json, judgment_event_id, created_at, completed_at)
			VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			tenantID, cmd.RequestID, commandHash, binding, id, now,
		); err != nil {
			return core.ProposeJudgmentResult{}, fmt.Errorf("persistence error: reserve judgment idempotency key: %w", err)
		}
	default:
		return core.ProposeJudgmentResult{}, fmt.Errorf("persistence error: read judgment idempotency key: %w", err)
	}

	// 4. Predecessor (design §3.7): a predecessor must concern the same pair
	// and relation; a CONFIRMED predecessor stays current until the correction
	// confirms (supersession is atomic with confirmation — design §5 step 7); a
	// PROPOSED predecessor may be superseded IMMEDIATELY but only by the same
	// proposer identity (which frees the open tuple for the correction);
	// terminal predecessors never re-open.
	supersededPred := ""
	if cmd.PredecessorID != "" {
		pred, ok := s.readJudgment(ctx, conn, cmd.PredecessorID)
		if !ok {
			return core.ProposeJudgmentResult{}, auth.New(auth.CodeJudgmentNotFound, "predecessor judgment not found: "+cmd.PredecessorID)
		}
		if pred.FromID != cmd.FromID || pred.ToID != cmd.ToID || pred.Relation != cmd.Relation {
			return core.ProposeJudgmentResult{}, auth.New(auth.CodeJudgmentConflict, "a predecessor must concern the same pair and relation")
		}
		switch pred.Status {
		case core.JudgmentConfirmed:
			// Deferred to confirm time (design §5 step 7).
		case core.JudgmentProposed:
			if proposerBinding(pred.Proposer) != binding {
				return core.ProposeJudgmentResult{}, auth.New(auth.CodeProposalUnauthorized, "a proposed judgment may only be corrected by its own proposer")
			}
			// The supersede UPDATE sets predecessor.supersedes_id to the new
			// judgment row created in this transaction; FK enforcement is already
			// deferred to COMMIT (see above), so the cross-row ordering is safe.
			if _, err := s.supersedeProposedPredecessor(ctx, conn, pred, id, cmd.RequestID, now); err != nil {
				return core.ProposeJudgmentResult{}, err
			}
			supersededPred = pred.ID
		default:
			return core.ProposeJudgmentResult{}, auth.New(auth.CodeInvalidJudgmentTransition, fmt.Sprintf("a %q predecessor cannot be corrected", pred.Status))
		}
	}

	// 5. Insert the proposed row. The partial unique index on
	// (tenant, company, from, to, relation) WHERE status='proposed' rejects a
	// second open proposal for the tuple → JUDGMENT_CONFLICT (design §3 rule 6:
	// another request never silently deduplicates authorship).
	var predecessorCol any
	if cmd.PredecessorID != "" {
		predecessorCol = cmd.PredecessorID
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO judgments (
			id, tenant_id, company_id, fiscal_period_id, from_id, to_id, relation, status,
			proposer_system, proposer_actor_id, proposer_actor_kind, proposer_session, proposal_reason,
			resolution, policy_version, adjudicator_subject_id, adjudicator_membership_id, adjudicator_roles_json,
			authentication_method, assurance_level, principal_authenticated_at,
			predecessor_id, supersedes_id, proposed_at, updated_at, decided_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'proposed', ?, ?, ?, ?, ?, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, ?, NULL, ?, ?, NULL)`,
		id, tenantID, companyID, nullableOrNil(fiscalPeriod), cmd.FromID, cmd.ToID, string(cmd.Relation),
		caller.System, caller.ActorID, string(caller.ActorKind), caller.Session, cmd.Reason,
		predecessorCol, now, now,
	); err != nil {
		if isOpenTupleConflict(err) {
			return core.ProposeJudgmentResult{}, auth.New(auth.CodeJudgmentConflict, "an open proposal already exists for this observation pair and relation")
		}
		return core.ProposeJudgmentResult{}, fmt.Errorf("persistence error: insert judgment: %w", err)
	}

	// 6. The supersedes routing row is inserted only now that BOTH judgment
	// rows exist (FK order): predecessor → successor with relation frozen to
	// 'supersedes' (design §4 — judgment ids never enter the observation
	// relations table).
	if supersededPred != "" {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO judgment_relations (from_judgment_id, to_judgment_id, relation, actor, timestamp)
			VALUES (?, ?, 'supersedes', ?, ?)`,
			supersededPred, id, binding, now,
		); err != nil {
			return core.ProposeJudgmentResult{}, fmt.Errorf("persistence error: insert supersedes relation: %w", err)
		}
	}

	// 7. Complete the reservation. A proposal has NO event (the events CHECK
	// freezes actions to confirm|reject|withdraw|supersede), so the CHECK
	// (judgment_event_id IS NULL) = (result_json IS NULL) keeps both NULL and
	// the completion time is the only change; judgment_id records the created
	// proposal so a same-request replay returns THAT exact row.
	if _, err := conn.ExecContext(ctx, `
		UPDATE judgment_idempotency_keys SET judgment_id = ?, completed_at = ? WHERE tenant_id = ? AND request_id = ?`,
		id, now, tenantID, cmd.RequestID,
	); err != nil {
		return core.ProposeJudgmentResult{}, fmt.Errorf("persistence error: complete judgment idempotency key: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.ProposeJudgmentResult{}, fmt.Errorf("persistence error: commit proposal: %w", err)
	}
	committed = true

	judgment := core.AccountingJudgment{
		ID:             id,
		TenantID:       tenantID,
		CompanyID:      companyID,
		FiscalPeriodID: fiscalPeriod,
		FromID:         cmd.FromID,
		ToID:           cmd.ToID,
		Relation:       cmd.Relation,
		Status:         core.JudgmentProposed,
		Proposer: core.Source{
			System:    caller.System,
			ActorID:   caller.ActorID,
			ActorKind: caller.ActorKind,
			Session:   caller.Session,
		},
		ProposalReason: cmd.Reason,
		PredecessorID:  cmd.PredecessorID,
		ProposedAt:     now,
		UpdatedAt:      now,
	}
	return core.ProposeJudgmentResult{JudgmentID: id, Judgment: judgment, IdempotentReplay: false}, nil
}

// supersedeProposedPredecessor performs design §3.7's immediate same-proposer
// supersession: the OLD OPEN proposal (by the same identity) is closed as
// superseded and routed to the correction, so the open-tuple index accepts it.
// The immutable 'supersede' event records the closed state; the caller inserts
// the judgment_relations routing row AFTER the successor judgment exists (FK
// order). The pure core.SupersedeJudgment helper only covers confirmed→
// superseded, so the proposed predecessor's routing fields are set directly
// here (proposed rows are the machine's work area — the trigger allows it).
func (s *SQLiteStore) supersedeProposedPredecessor(ctx context.Context, q Queryer, pred core.AccountingJudgment, successorID, requestID, now string) (string, error) {
	superseded := pred
	superseded.Status = core.JudgmentSuperseded
	superseded.SupersedesID = successorID
	superseded.UpdatedAt = now
	superseded.DecidedAt = now // schema CHECK: every non-proposed row carries decided_at
	res, err := q.ExecContext(ctx, `
		UPDATE judgments SET status = 'superseded', supersedes_id = ?, decided_at = ?, updated_at = ?
		WHERE id = ? AND status = 'proposed'`,
		successorID, now, now, pred.ID,
	)
	if err != nil {
		return "", fmt.Errorf("persistence error: supersede proposed predecessor: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("persistence error: supersede proposed predecessor rows affected: %w", err)
	}
	if affected != 1 {
		return "", auth.New(auth.CodeInvalidJudgmentTransition, "guarded predecessor supersession did not match exactly one proposed row")
	}
	eventID, err := newUUID()
	if err != nil {
		return "", fmt.Errorf("persistence error: generate predecessor event id: %w", err)
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO judgment_events (
			id, judgment_id, request_id, action, from_status, to_status, judgment_hash,
			principal_snapshot_json, policy_version, reason, created_at
		) VALUES (?, ?, ?, 'supersede', 'proposed', 'superseded', ?, NULL, NULL, '', ?)`,
		eventID, pred.ID, requestID, core.ComputeJudgmentHash(superseded), now,
	); err != nil {
		return "", fmt.Errorf("persistence error: insert predecessor supersede event: %w", err)
	}
	return eventID, nil
}

// readOpenProposal returns the OPEN proposal for the tuple — the partial unique
// index guarantees at most one (design §3 rule 6).
func (s *SQLiteStore) readOpenProposal(ctx context.Context, q Queryer, tenantID, companyID, fromID, toID string, relation core.Relation) (core.AccountingJudgment, bool) {
	row := q.QueryRowContext(ctx, `SELECT `+judgmentColumns+` FROM judgments
		WHERE tenant_id = ? AND company_id = ? AND from_id = ? AND to_id = ? AND relation = ? AND status = 'proposed'`,
		tenantID, companyID, fromID, toID, string(relation))
	j, err := scanJudgment(row)
	if err != nil {
		return core.AccountingJudgment{}, false
	}
	return j, true
}

// readLatestTupleJudgment returns the most recent judgment row of the tuple
// (any status) — the replay fallback when the replayed proposal was already
// decided before the retry arrived.
func (s *SQLiteStore) readLatestTupleJudgment(ctx context.Context, q Queryer, tenantID, companyID, fromID, toID string, relation core.Relation) (core.AccountingJudgment, bool) {
	row := q.QueryRowContext(ctx, `SELECT `+judgmentColumns+` FROM judgments
		WHERE tenant_id = ? AND company_id = ? AND from_id = ? AND to_id = ? AND relation = ?
		ORDER BY rowid DESC LIMIT 1`,
		tenantID, companyID, fromID, toID, string(relation))
	j, err := scanJudgment(row)
	if err != nil {
		return core.AccountingJudgment{}, false
	}
	return j, true
}

// judgmentDecisionParams carries the shared confirm/reject decision inputs.
// confirm: Resolution is the professional resolution and both RelationProjection
// and SupersedePredecessor are true. reject: Resolution is the human reason and
// both flags stay false (terminal — no relation projection, no supersession).
type judgmentDecisionParams struct {
	JudgmentID           string
	Resolution           string
	ExpectedJudgmentHash string
	RequestID            string
	Action               string // 'confirm' | 'reject' (frozen events CHECK)
	ToStatus             core.JudgmentStatus
	RelationProjection   bool // confirm only: compatibility observation relation
	SupersedePredecessor bool // confirm only: atomic supersession of a confirmed predecessor
}

// adjudicateJudgment is THE authenticated decision transaction (design §5) —
// shared by confirm and reject. One BEGIN IMMEDIATE on a dedicated connection:
// idempotency resolution → locked re-read of the judgment + observations →
// status gate → fresh hash vs expected → pure frozen policy → guarded status
// flip → immutable decision event (+ relation projection / predecessor
// supersession for confirm) → completed reservation → commit. Two concurrent
// confirms serialize at BEGIN IMMEDIATE: exactly one flips the row; the loser
// reads the committed status and returns INVALID_JUDGMENT_TRANSITION (or a
// replay when it carries the winner's identical request id).
func (s *SQLiteStore) adjudicateJudgment(ctx context.Context, p judgmentDecisionParams, principal auth.VerifiedApprovalPrincipal, policy authz.JudgmentAuthorizationPolicy) (core.AccountingJudgment, string, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	now := nowISO()
	commandHash := decideJudgmentCommandHash(p.JudgmentID, p.ExpectedJudgmentHash, p.Resolution)
	binding := principal.SubjectID()

	// 1. Idempotency by (principal tenant, requestId), bound to the exact
	// adjudicator subject: a different command or principal returns
	// IDEMPOTENCY_CONFLICT; a completed match replays the stored result.
	var storedHash, storedBinding string
	var storedResultJSON, completedAt sql.NullString
	err = conn.QueryRowContext(ctx, `
		SELECT command_hash, actor_binding, result_json, completed_at
		FROM judgment_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		principal.TenantID(), p.RequestID,
	).Scan(&storedHash, &storedBinding, &storedResultJSON, &completedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedBinding != binding {
			return core.AccountingJudgment{}, "", false, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different decision or adjudicator")
		}
		if completedAt.Valid {
			var replay storedJudgmentResult
			if err := json.Unmarshal([]byte(storedResultJSON.String), &replay); err != nil {
				return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: decode replayed judgment result: %w", err)
			}
			return replay.Judgment, replay.JudgmentEventID, true, nil
		}
		// Incomplete reservation (an interrupted attempt that never committed):
		// reuse it — the status gate below decides the outcome.
	case errors.Is(err, sql.ErrNoRows):
		// 2. Reserve.
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO judgment_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, result_json, judgment_event_id, created_at, completed_at)
			VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			principal.TenantID(), p.RequestID, commandHash, binding, now,
		); err != nil {
			return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: reserve judgment idempotency key: %w", err)
		}
	default:
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: read judgment idempotency key: %w", err)
	}

	// 3. Read the judgment plus both observations on the SAME connection (the
	// locked observations feed the relation receipt's from/to envelope hashes —
	// recomputed fresh from the locked rows, never from the stored cache).
	judgment, ok := s.readJudgment(ctx, conn, p.JudgmentID)
	if !ok {
		return core.AccountingJudgment{}, "", false, auth.New(auth.CodeJudgmentNotFound, "judgment not found: "+p.JudgmentID)
	}
	var fromObs, toObs core.AccountingMemory
	for _, obsID := range []string{judgment.FromID, judgment.ToID} {
		obs, ok := s.readMemoryWithLinks(ctx, conn, obsID)
		if !ok {
			return core.AccountingJudgment{}, "", false, auth.New(auth.CodeMemoryNotFound, "judgment observation not found: "+obsID)
		}
		if obsID == judgment.FromID {
			fromObs = obs
		} else {
			toObs = obs
		}
	}

	// 4. Status gate: only a proposed judgment may be decided. A concurrent
	// loser lands here after the winner commits and sees the new status.
	if judgment.Status != core.JudgmentProposed {
		return core.AccountingJudgment{}, "", false, auth.New(auth.CodeInvalidJudgmentTransition, fmt.Sprintf("%s is not legal from status %q", p.Action, judgment.Status))
	}

	// 5. The reviewed hash is recomputed FRESH from the locked row and compared
	// against what the adjudicator actually reviewed; a mismatch returns
	// JUDGMENT_HASH_MISMATCH carrying ONLY expected/actual (design §6).
	actual := core.ComputeJudgmentHash(judgment)
	if !strings.EqualFold(strings.TrimSpace(p.ExpectedJudgmentHash), actual) {
		return core.AccountingJudgment{}, "", false, auth.NewJudgmentHashMismatch(p.ExpectedJudgmentHash, actual, "judgment changed after review; expected hash does not match the current proposed state")
	}

	// 6. Pure policy in-transaction (tenant → company → membership → role →
	// assurance); any denial returns its frozen reason code.
	decision := policy.Authorize(principal, judgment)
	if !decision.Allowed {
		return core.AccountingJudgment{}, "", false, auth.New(decision.ReasonCode, "judgment authorization policy denied the "+p.Action+" decision")
	}

	// 7. Apply the pure machine transition on the snapshot with ONE captured
	// timestamp; the canonical snapshot carries sorted/deduplicated roles.
	snapshot := principal.PrincipalSnapshot()
	resulting := judgment
	if p.Action == "confirm" {
		if err := core.ConfirmJudgment(&resulting, p.Resolution, &snapshot, decision.PolicyVersion, now); err != nil {
			return core.AccountingJudgment{}, "", false, err
		}
	} else {
		if err := core.RejectJudgment(&resulting, p.Resolution, &snapshot, decision.PolicyVersion, now); err != nil {
			return core.AccountingJudgment{}, "", false, err
		}
	}

	rolesJSON, err := json.Marshal(snapshot.Roles)
	if err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: encode adjudicator roles: %w", err)
	}

	// 8. Guarded UPDATE: exactly one proposed row flips to the target status.
	res, err := conn.ExecContext(ctx, `
		UPDATE judgments SET status = ?, resolution = ?, policy_version = ?,
			adjudicator_subject_id = ?, adjudicator_membership_id = ?, adjudicator_roles_json = ?,
			authentication_method = ?, assurance_level = ?, principal_authenticated_at = ?,
			decided_at = ?, updated_at = ?
		WHERE id = ? AND status = 'proposed'`,
		string(p.ToStatus), p.Resolution, resulting.PolicyVersion,
		snapshot.SubjectID, snapshot.MembershipID, string(rolesJSON),
		string(snapshot.AuthenticationMethod), string(snapshot.AssuranceLevel), snapshot.AuthenticatedAt,
		now, now, p.JudgmentID,
	)
	if err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: "+p.Action+" update: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: "+p.Action+" rows affected: %w", err)
	}
	if affected != 1 {
		return core.AccountingJudgment{}, "", false, auth.New(auth.CodeInvalidJudgmentTransition, "guarded status update did not match exactly one proposed row")
	}

	// 9. The immutable decision event; confirm/reject events carry the principal
	// snapshot and the frozen policy version (events CHECK). The judgment_hash
	// records the resulting state (the exact hash the confirmed/rejected row
	// now hashes to).
	eventID, err := newUUID()
	if err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: generate judgment event id: %w", err)
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: encode principal snapshot: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO judgment_events (
			id, judgment_id, request_id, action, from_status, to_status, judgment_hash,
			principal_snapshot_json, policy_version, reason, created_at
		) VALUES (?, ?, ?, ?, 'proposed', ?, ?, ?, ?, ?, ?)`,
		eventID, p.JudgmentID, p.RequestID, p.Action, string(p.ToStatus),
		core.ComputeJudgmentHash(resulting), string(snapshotJSON), resulting.PolicyVersion, p.Resolution, now,
	); err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: insert "+p.Action+" event: %w", err)
	}

	// 10. Confirm-only: the compatibility observation relation projection
	// (INSERT ... SELECT ... WHERE NOT EXISTS — observations.relations is a
	// projection; judgments remain authoritative). Its actor is the verified
	// subject.
	if p.RelationProjection {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO relations (from_id, to_id, relation, actor, timestamp)
			SELECT ?, ?, ?, ?, ?
			WHERE NOT EXISTS (SELECT 1 FROM relations WHERE from_id = ? AND to_id = ? AND relation = ?)`,
			judgment.FromID, judgment.ToID, string(judgment.Relation), snapshot.SubjectID, now,
			judgment.FromID, judgment.ToID, string(judgment.Relation),
		); err != nil {
			return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: insert observation relation projection: %w", err)
		}
	}

	// 11. Confirm-only, correction: the predecessor must be confirmed for the
	// supersession to be atomic with this confirmation (design §5 step 7), or
	// already superseded by THIS very proposal at propose time (design §3.7);
	// anything else is an invalid transition.
	if p.SupersedePredecessor && judgment.PredecessorID != "" {
		pred, ok := s.readJudgment(ctx, conn, judgment.PredecessorID)
		if !ok {
			return core.AccountingJudgment{}, "", false, auth.New(auth.CodeJudgmentNotFound, "predecessor judgment not found: "+judgment.PredecessorID)
		}
		switch pred.Status {
		case core.JudgmentConfirmed:
			// The superseded predecessor keeps its original decided_at (the
			// immutability trigger allows ONLY routing-field changes).
			res, err := conn.ExecContext(ctx, `
				UPDATE judgments SET status = 'superseded', supersedes_id = ?, updated_at = ?
				WHERE id = ? AND status = 'confirmed'`,
				judgment.ID, now, pred.ID,
			)
			if err != nil {
				return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: supersede predecessor: %w", err)
			}
			affected, err := res.RowsAffected()
			if err != nil {
				return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: supersede predecessor rows affected: %w", err)
			}
			if affected != 1 {
				return core.AccountingJudgment{}, "", false, auth.New(auth.CodeInvalidJudgmentTransition, "guarded predecessor supersession did not match exactly one confirmed row")
			}
			superseded := pred
			if err := core.SupersedeJudgment(&superseded, judgment.ID, now); err != nil {
				return core.AccountingJudgment{}, "", false, err
			}
			predEventID, err := newUUID()
			if err != nil {
				return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: generate predecessor event id: %w", err)
			}
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO judgment_events (
					id, judgment_id, request_id, action, from_status, to_status, judgment_hash,
					principal_snapshot_json, policy_version, reason, created_at
				) VALUES (?, ?, ?, 'supersede', 'confirmed', 'superseded', ?, NULL, NULL, ?, ?)`,
				predEventID, pred.ID, p.RequestID, core.ComputeJudgmentHash(superseded), p.Resolution, now,
			); err != nil {
				return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: insert predecessor supersede event: %w", err)
			}
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO judgment_relations (from_judgment_id, to_judgment_id, relation, actor, timestamp)
				VALUES (?, ?, 'supersedes', ?, ?)`,
				pred.ID, judgment.ID, snapshot.SubjectID, now,
			); err != nil {
				return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: insert supersedes relation: %w", err)
			}
		case core.JudgmentSuperseded:
			if pred.SupersedesID != judgment.ID {
				return core.AccountingJudgment{}, "", false, auth.New(auth.CodeInvalidJudgmentTransition, "predecessor is already superseded by a different judgment")
			}
			// Already superseded by this very correction at propose time.
		default:
			return core.AccountingJudgment{}, "", false, auth.New(auth.CodeInvalidJudgmentTransition, fmt.Sprintf("predecessor status %q cannot be superseded by a correction", pred.Status))
		}
	}

	// 12. Atomic receipt emission (v0.4.0 Step 3): after the decision event, the
	// projections and the (covered) predecessor supersession, BEFORE the
	// idempotency completion, inside the SAME transaction with the captured now.
	// relation_confirmed / relation_rejected carries the proposed/resulting
	// judgment hashes, both locked observation envelope hashes, the resolution and
	// the complete verified principal snapshot. The predecessor supersession is
	// covered inside relation_confirmed — it never creates another action. A
	// signing failure rolls the whole decision back (no event, no receipt).
	relationAction := core.ReceiptActionRelationConfirmed
	if p.Action == "reject" {
		relationAction = core.ReceiptActionRelationRejected
	}
	receiptSnapshot := snapshot
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeJudgment, judgment.ID, relationAction, core.ReceiptPayload{
		TenantID:                 judgment.TenantID,
		CompanyID:                judgment.CompanyID,
		FiscalPeriodID:           judgment.FiscalPeriodID,
		ReviewedJudgmentHash:     actual,
		ResultingJudgmentHash:    core.ComputeJudgmentHash(resulting),
		FromMemoryID:             judgment.FromID,
		FromEnvelopeHash:         core.ComputeEnvelopeHash(fromObs),
		ToMemoryID:               judgment.ToID,
		ToEnvelopeHash:           core.ComputeEnvelopeHash(toObs),
		Reason:                   p.Resolution,
		PrincipalID:              receiptSnapshot.SubjectID,
		MembershipID:             receiptSnapshot.MembershipID,
		PrincipalRoles:           receiptPrincipalRoles(receiptSnapshot),
		AuthenticationMethod:     string(receiptSnapshot.AuthenticationMethod),
		AssuranceLevel:           string(receiptSnapshot.AssuranceLevel),
		PrincipalAuthenticatedAt: receiptSnapshot.AuthenticatedAt,
		PolicyVersion:            decision.PolicyVersion,
	}, now); err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: emit "+p.Action+" receipt: %w", err)
	}

	// 13. Complete the reservation (result + event link + completion time — the
	// CHECK requires result_json and judgment_event_id to be set together) and
	// commit; the whole decision is one atomic unit.
	result := storedJudgmentResult{JudgmentID: judgment.ID, Judgment: resulting, JudgmentEventID: eventID}
	serializedResult, err := json.Marshal(result)
	if err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: encode "+p.Action+" result: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE judgment_idempotency_keys SET result_json = ?, judgment_event_id = ?, completed_at = ?
		WHERE tenant_id = ? AND request_id = ?`,
		string(serializedResult), eventID, now, principal.TenantID(), p.RequestID,
	); err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: complete judgment idempotency key: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.AccountingJudgment{}, "", false, fmt.Errorf("persistence error: commit "+p.Action+": %w", err)
	}
	committed = true
	return resulting, eventID, false, nil
}

// ConfirmJudgment atomically confirms a proposed judgment — the authenticated
// adjudication act (design §5). It mirrors Step 1's ApproveMemory: dedicated
// connection, literal BEGIN IMMEDIATE, idempotency reservation, fresh-hash
// comparison, pure policy, guarded UPDATE, immutable event, and — for a
// correction — the atomic supersession of the confirmed predecessor. Agents can
// never reach this method: the signature REQUIRES a verified principal (an
// agent Source is provenance only and carries no authority).
func (s *SQLiteStore) ConfirmJudgment(ctx context.Context, cmd core.ConfirmJudgmentCommand, principal auth.VerifiedApprovalPrincipal, policy authz.JudgmentAuthorizationPolicy) (core.ConfirmJudgmentResult, error) {
	if strings.TrimSpace(cmd.Resolution) == "" {
		return core.ConfirmJudgmentResult{}, auth.New(auth.CodeResolutionRequired, "confirmation requires a non-empty professional resolution")
	}
	if strings.TrimSpace(cmd.JudgmentID) == "" || strings.TrimSpace(cmd.ExpectedJudgmentHash) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.ConfirmJudgmentResult{}, auth.New(auth.CodeJudgmentNotFound, "confirm command is incomplete (judgmentId, expectedJudgmentHash and requestId are required)")
	}
	j, eventID, replay, err := s.adjudicateJudgment(ctx, judgmentDecisionParams{
		JudgmentID:           cmd.JudgmentID,
		Resolution:           cmd.Resolution,
		ExpectedJudgmentHash: cmd.ExpectedJudgmentHash,
		RequestID:            cmd.RequestID,
		Action:               "confirm",
		ToStatus:             core.JudgmentConfirmed,
		RelationProjection:   true,
		SupersedePredecessor: true,
	}, principal, policy)
	if err != nil {
		return core.ConfirmJudgmentResult{}, err
	}
	return core.ConfirmJudgmentResult{JudgmentID: j.ID, Judgment: j, JudgmentEventID: eventID, IdempotentReplay: replay}, nil
}

// RejectJudgment atomically rejects a proposed judgment: the same
// lock/hash/policy/idempotency path as confirmation, storing the HUMAN reason
// as the resolution and becoming terminal. It writes NO observation relation
// projection and performs no supersession (a rejected correction leaves its
// predecessor current).
func (s *SQLiteStore) RejectJudgment(ctx context.Context, cmd core.RejectJudgmentCommand, principal auth.VerifiedApprovalPrincipal, policy authz.JudgmentAuthorizationPolicy) (core.RejectJudgmentResult, error) {
	if strings.TrimSpace(cmd.Reason) == "" {
		return core.RejectJudgmentResult{}, auth.New(auth.CodeResolutionRequired, "rejection requires a non-empty human reason")
	}
	if strings.TrimSpace(cmd.JudgmentID) == "" || strings.TrimSpace(cmd.ExpectedJudgmentHash) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.RejectJudgmentResult{}, auth.New(auth.CodeJudgmentNotFound, "reject command is incomplete (judgmentId, expectedJudgmentHash and requestId are required)")
	}
	j, eventID, replay, err := s.adjudicateJudgment(ctx, judgmentDecisionParams{
		JudgmentID:           cmd.JudgmentID,
		Resolution:           cmd.Reason,
		ExpectedJudgmentHash: cmd.ExpectedJudgmentHash,
		RequestID:            cmd.RequestID,
		Action:               "reject",
		ToStatus:             core.JudgmentRejected,
	}, principal, policy)
	if err != nil {
		return core.RejectJudgmentResult{}, err
	}
	return core.RejectJudgmentResult{JudgmentID: j.ID, Judgment: j, JudgmentEventID: eventID, IdempotentReplay: replay}, nil
}

// WithdrawJudgment withdraws the caller's OWN proposed judgment (terminal). The
// SAME exact proposer identity (system+actorId+actorKind+session) is required —
// mismatch is PROPOSAL_UNAUTHORIZED (provenance continuity, never professional
// authorization — design §3.7). Idempotency is keyed by (tenant from the
// judgment, requestId); the schema CHECK requires decided_at on every
// non-proposed row, so the withdrawal stamps it.
func (s *SQLiteStore) WithdrawJudgment(ctx context.Context, cmd core.WithdrawJudgmentCommand, caller core.Source) (core.WithdrawJudgmentResult, error) {
	if strings.TrimSpace(cmd.JudgmentID) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.WithdrawJudgmentResult{}, auth.New(auth.CodeJudgmentNotFound, "withdraw command is incomplete (judgmentId and requestId are required)")
	}
	if !core.CanPropose(caller) {
		return core.WithdrawJudgmentResult{}, auth.New(auth.CodeProposalUnauthorized, "only the proposing agent/system may withdraw")
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	now := nowISO()
	commandHash := withdrawJudgmentCommandHash(cmd.JudgmentID)
	binding := proposerBinding(caller)

	// 1. Read the judgment on the locked connection: the tenant for the
	// idempotency key comes from the judgment, never from caller claims.
	judgment, ok := s.readJudgment(ctx, conn, cmd.JudgmentID)
	if !ok {
		return core.WithdrawJudgmentResult{}, auth.New(auth.CodeJudgmentNotFound, "judgment not found: "+cmd.JudgmentID)
	}

	// 2. Idempotency by (judgment tenant, requestId), bound to the exact
	// proposer identity. The resolution runs BEFORE the status/identity gates so
	// a completed reservation REPLAYS even though the row is already withdrawn
	// (mirroring ApproveMemory: idempotency first, status gate second).
	var storedHash, storedBinding string
	var storedResultJSON, completedAt sql.NullString
	err = conn.QueryRowContext(ctx, `
		SELECT command_hash, actor_binding, result_json, completed_at
		FROM judgment_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		judgment.TenantID, cmd.RequestID,
	).Scan(&storedHash, &storedBinding, &storedResultJSON, &completedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedBinding != binding {
			return core.WithdrawJudgmentResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different command or proposer")
		}
		if completedAt.Valid {
			var replay storedJudgmentResult
			if err := json.Unmarshal([]byte(storedResultJSON.String), &replay); err != nil {
				return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: decode replayed judgment result: %w", err)
			}
			return core.WithdrawJudgmentResult{JudgmentID: replay.JudgmentID, Judgment: replay.Judgment, JudgmentEventID: replay.JudgmentEventID, IdempotentReplay: true}, nil
		}
		// Incomplete reservation (an interrupted attempt that never committed):
		// reuse it — the status gate below decides the outcome.
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO judgment_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, result_json, judgment_event_id, created_at, completed_at)
			VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			judgment.TenantID, cmd.RequestID, commandHash, binding, now,
		); err != nil {
			return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: reserve judgment idempotency key: %w", err)
		}
	default:
		return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: read judgment idempotency key: %w", err)
	}

	// 3. Only an open proposal may be withdrawn, and only by its OWN proposer
	// (provenance continuity — never professional authorization, design §3.7).
	if judgment.Status != core.JudgmentProposed {
		return core.WithdrawJudgmentResult{}, auth.New(auth.CodeInvalidJudgmentTransition, fmt.Sprintf("withdrawal is not legal from status %q", judgment.Status))
	}
	if proposerBinding(judgment.Proposer) != binding {
		return core.WithdrawJudgmentResult{}, auth.New(auth.CodeProposalUnauthorized, "a judgment may only be withdrawn by its own proposer")
	}

	// 4. Guarded UPDATE: exactly one proposed row closes as withdrawn; the
	// schema CHECK requires decided_at here.
	res, err := conn.ExecContext(ctx, `
		UPDATE judgments SET status = 'withdrawn', decided_at = ?, updated_at = ?
		WHERE id = ? AND status = 'proposed'`,
		now, now, cmd.JudgmentID,
	)
	if err != nil {
		return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: withdraw update: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: withdraw rows affected: %w", err)
	}
	if affected != 1 {
		return core.WithdrawJudgmentResult{}, auth.New(auth.CodeInvalidJudgmentTransition, "guarded status update did not match exactly one proposed row")
	}

	// 5. The immutable 'withdraw' event (no snapshot, no policy version).
	withdrawn := judgment
	if err := core.WithdrawJudgment(&withdrawn, now); err != nil {
		return core.WithdrawJudgmentResult{}, err
	}
	withdrawn.DecidedAt = now // the row stores decided_at; the entity mirrors it
	eventID, err := newUUID()
	if err != nil {
		return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: generate judgment event id: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO judgment_events (
			id, judgment_id, request_id, action, from_status, to_status, judgment_hash,
			principal_snapshot_json, policy_version, reason, created_at
		) VALUES (?, ?, ?, 'withdraw', 'proposed', 'withdrawn', ?, NULL, NULL, '', ?)`,
		eventID, cmd.JudgmentID, cmd.RequestID, core.ComputeJudgmentHash(withdrawn), now,
	); err != nil {
		return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: insert withdraw event: %w", err)
	}

	// 6. Complete the reservation (event exists → result_json is set with it)
	// and commit.
	result := storedJudgmentResult{JudgmentID: judgment.ID, Judgment: withdrawn, JudgmentEventID: eventID}
	serializedResult, err := json.Marshal(result)
	if err != nil {
		return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: encode withdraw result: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE judgment_idempotency_keys SET result_json = ?, judgment_event_id = ?, completed_at = ?
		WHERE tenant_id = ? AND request_id = ?`,
		string(serializedResult), eventID, now, judgment.TenantID, cmd.RequestID,
	); err != nil {
		return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: complete judgment idempotency key: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.WithdrawJudgmentResult{}, fmt.Errorf("persistence error: commit withdraw: %w", err)
	}
	committed = true

	return core.WithdrawJudgmentResult{JudgmentID: judgment.ID, Judgment: withdrawn, JudgmentEventID: eventID, IdempotentReplay: false}, nil
}

// GetJudgment returns the judgment with the given id, if any — the public
// read-only surface of the judgment store (CLI `judge show`). It reads through
// the pool connection and never participates in an adjudication transition:
// every race-sensitive read lives inside the ProposeJudgment/ConfirmJudgment/
// RejectJudgment/WithdrawJudgment transactions (design §2 — no FindJudgment +
// mutate composition anywhere).
func (s *SQLiteStore) GetJudgment(ctx context.Context, id string) (core.AccountingJudgment, bool) {
	return s.readJudgment(ctx, s.db, id)
}

// JudgmentSuccessorOf routes readers from a superseded judgment to its
// correction: it reads judgment_relations (frozen to 'supersedes' — design §4;
// judgment ids never enter the observation relations table) and returns the
// successor judgment.
func (s *SQLiteStore) JudgmentSuccessorOf(ctx context.Context, judgmentID string) (core.AccountingJudgment, bool) {
	var toID string
	err := s.db.QueryRowContext(ctx, `
		SELECT to_judgment_id FROM judgment_relations
		WHERE from_judgment_id = ? AND relation = 'supersedes' ORDER BY rowid LIMIT 1`, judgmentID).Scan(&toID)
	if err != nil {
		return core.AccountingJudgment{}, false
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+judgmentColumns+` FROM judgments WHERE id = ?`, toID)
	j, err := scanJudgment(row)
	if err != nil {
		return core.AccountingJudgment{}, false
	}
	return j, true
}

// nullableOrNil maps an empty string to NULL and any other value to the string
// itself — the v4 style for optional TEXT columns.
func nullableOrNil(v string) any {
	if v == "" {
		return nil
	}
	return v
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

	// Read the FULL memory (with its current link rows) through the transaction
	// BEFORE the transition: the supersession receipt covers the act with the
	// envelope hashes BEFORE (status/supersedes_id as stored) and AFTER (status
	// superseded, supersedes_id = successor) — recomputed fresh, never cached.
	memory, ok := s.readMemoryWithLinks(ctx, tx, memoryID)
	if !ok {
		return core.AccountingMemory{}, fmt.Errorf("MEMORY_NOT_FOUND: %s", memoryID)
	}
	from := memory.Status
	fromEnvelope := core.ComputeEnvelopeHash(memory)
	superseded := memory
	superseded.Status = core.StatusSuperseded
	superseded.SupersedesID = successorID
	toEnvelope := core.ComputeEnvelopeHash(superseded)

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

	// Atomic receipt emission (v0.4.0 Step 3): inside the SAME transaction, with
	// the transition's own timestamp (never a fresh time call). The claimed
	// supersession act uses the transition actor as principalId. A signing
	// failure rolls the whole supersession back (no transition, no receipt).
	if _, err := s.emitReceipt(ctx, tx, core.SubjectTypeMemory, memoryID, core.ReceiptActionMemorySuperseded, core.ReceiptPayload{
		TenantID:         memory.Scope.OrganizationID,
		CompanyID:        memory.Scope.CompanyID,
		FiscalPeriodID:   memory.Scope.Period,
		FromEnvelopeHash: fromEnvelope,
		ToEnvelopeHash:   toEnvelope,
		SuccessorID:      successorID,
		PrincipalID:      meta.Actor,
		PolicyVersion:    kernelPolicyVersion,
	}, meta.Timestamp); err != nil {
		return core.AccountingMemory{}, err
	}

	if err := tx.Commit(); err != nil {
		return core.AccountingMemory{}, err
	}
	committed = true
	return superseded, nil
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

	// Read the FULL memory (with its current link rows) through the transaction
	// BEFORE the transition: reject/void receipts cover the act with the envelope
	// hashes BEFORE and AFTER (status participates in the envelope) — recomputed
	// fresh, never cached.
	memory, ok := s.readMemoryWithLinks(ctx, tx, memoryID)
	if !ok {
		return core.AccountingMemory{}, fmt.Errorf("OBSERVATION_NOT_FOUND: %s", memoryID)
	}
	from := memory.Status
	fromEnvelope := core.ComputeEnvelopeHash(memory)
	resulting := memory
	resulting.Status = to
	toEnvelope := core.ComputeEnvelopeHash(resulting)

	if _, err := tx.ExecContext(ctx, `UPDATE observations SET status = ?, authority_status = ? WHERE id = ?`, string(to), legacyStatusFor(to), memoryID); err != nil {
		return core.AccountingMemory{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transition_log (observation_id, from_status, to_status, actor, actor_kind, timestamp) VALUES (?, ?, ?, ?, ?, ?)`,
		memoryID, from, string(to), meta.Actor, string(meta.ActorKind), meta.Timestamp,
	); err != nil {
		return core.AccountingMemory{}, err
	}

	// Atomic receipt emission (v0.4.0 Step 3): ONLY the covered terminal
	// transitions mint receipts — rejected → memory_rejected, voided →
	// memory_voided (the closed action set has no receipt for any other
	// transition; approvals are covered by the authenticated ApproveMemory path).
	// The emission runs inside the SAME transaction with the transition's own
	// timestamp; the claimed act uses the transition actor as principalId. A
	// signing failure rolls the whole transition back (no change, no receipt).
	switch to {
	case core.StatusRejected:
		if _, err := s.emitReceipt(ctx, tx, core.SubjectTypeMemory, memoryID, core.ReceiptActionMemoryRejected, core.ReceiptPayload{
			TenantID:              memory.Scope.OrganizationID,
			CompanyID:             memory.Scope.CompanyID,
			FiscalPeriodID:        memory.Scope.Period,
			ReviewedEnvelopeHash:  fromEnvelope,
			ResultingEnvelopeHash: toEnvelope,
			PrincipalID:           meta.Actor,
			PolicyVersion:         kernelPolicyVersion,
		}, meta.Timestamp); err != nil {
			return core.AccountingMemory{}, err
		}
	case core.StatusVoided:
		if _, err := s.emitReceipt(ctx, tx, core.SubjectTypeMemory, memoryID, core.ReceiptActionMemoryVoided, core.ReceiptPayload{
			TenantID:              memory.Scope.OrganizationID,
			CompanyID:             memory.Scope.CompanyID,
			FiscalPeriodID:        memory.Scope.Period,
			ReviewedEnvelopeHash:  fromEnvelope,
			ResultingEnvelopeHash: toEnvelope,
			PrincipalID:           meta.Actor,
			PolicyVersion:         kernelPolicyVersion,
		}, meta.Timestamp); err != nil {
			return core.AccountingMemory{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return core.AccountingMemory{}, err
	}
	committed = true
	return resulting, nil
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
	rows, err := s.db.QueryContext(ctx, `SELECT envelope_hash, identity_hash FROM observations WHERE id = ?`, memory.Identity.ID)
	if err != nil {
		return false, err
	}
	has := rows.Next()
	var existingEnvelope, existingIdentity string
	if has {
		if err := rows.Scan(&existingEnvelope, &existingIdentity); err != nil {
			_ = rows.Close()
			return false, err
		}
	}
	_ = rows.Close()
	if has {
		// The envelope hash covers EVERYTHING signable: identity + content +
		// fiscal effect + source + evidence/rule refs + timestamps + supersession.
		// A matching envelope is an exact duplicate (idempotent no-op); a
		// differing envelope on the SAME identity is an immutable conflict — the
		// import surfaces it, never overwrites. Migrated v1 rows may lack the new
		// envelope column; fall back to the content hash for those.
		if existingEnvelope != "" {
			if existingEnvelope == memory.EnvelopeHash {
				return false, nil // exact duplicate — no-op
			}
			if existingIdentity == memory.IdentityHash {
				return false, fmt.Errorf("IMPORT_CONFLICT: id %s exists with different immutable bytes on the same domain identity", memory.Identity.ID)
			}
			return false, fmt.Errorf("IMPORT_CONFLICT: id %s exists with different immutable bytes", memory.Identity.ID)
		}
		if existingHash := existingEnvelope; existingHash == "" {
			// legacy row without envelope: compare the canonical content hash
			rows2, err := s.db.QueryContext(ctx, `SELECT content_hash FROM observations WHERE id = ?`, memory.Identity.ID)
			if err != nil {
				return false, err
			}
			has2 := rows2.Next()
			var legacyHash string
			if has2 {
				_ = rows2.Scan(&legacyHash)
			}
			_ = rows2.Close()
			if legacyHash == memory.ContentHash {
				return false, nil // identical content — no-op
			}
		}
		return false, fmt.Errorf("IMPORT_CONFLICT: id %s exists with different immutable bytes", memory.Identity.ID)
	}

	built := core.CloneMemory(memory)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO observations (
			id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
			what, why, where_text, learned, authority_status, status, fiscal_effect, effective_at, recorded_at, observed_at,
			expires_at, validity_effective_at, validity_source, actor, timestamp, source, session, source_json, content_hash, identity_hash, envelope_hash, evidence_refs_json, rule_refs_json,
			confidence, materiality, materiality_level, receipt_id, supersedes_id, revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		built.Identity.ID, built.Identity.TopicKey, built.Title, string(built.Kind), string(built.Kind), string(built.Scope.Kind),
		built.Scope.OrganizationID, built.Scope.CompanyID, built.Scope.RUC, built.Scope.Period,
		built.Content.What, built.Content.Why, built.Content.Where, built.Content.Learned,
		legacyStatusFor(built.Status), string(built.Status), string(built.FiscalEffect),
		built.EffectiveAt, built.RecordedAt, built.ObservedAt,
		validityExpiresAt(built.Validity), validityEffectiveAt(built.Validity), validitySource(built.Validity),
		built.Source.ActorID, built.RecordedAt, built.Source.System, built.Source.Session,
		encodeSource(built.Source), built.ContentHash, built.IdentityHash, built.EnvelopeHash, encodeRefs(built.EvidenceRefs), encodeRefs(built.RuleRefs),
		nullableFloat(built.Confidence), nullableInt(built.Materiality), nullableMaterialityLevel(built.MaterialityLevel), built.ReceiptID, built.SupersedesID,
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
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// BEGIN IMMEDIATE is the write intent: the link insertion and the derived
	// envelope-cache refresh are ONE atomic unit (a deferred transaction could
	// interleave with an approval on another connection).
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	// Read the FULL memory (with its current link rows) through the transaction
	// BEFORE the insert: the evidence receipt needs the scope and the PRE-link
	// envelope (recomputed fresh — the stored envelope cache is not trusted).
	memory, ok := s.readMemoryWithLinks(ctx, conn, memoryID)
	if !ok {
		return fmt.Errorf("OBSERVATION_NOT_FOUND: %s", memoryID)
	}
	now := nowISO() // ONE captured timestamp for the link row and its receipt
	fromEnvelope := core.ComputeEnvelopeHash(memory)
	res, err := conn.ExecContext(ctx,
		`INSERT OR IGNORE INTO `+table+` (memory_id, ref, actor, timestamp) VALUES (?, ?, ?, ?)`,
		memoryID, ref, actor, now,
	)
	if err != nil {
		return fmt.Errorf("persistence error: add link: %w", err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("persistence error: link rows affected: %w", err)
	}
	// A link changes the canonical refs → the derived envelope cache changes.
	// Refresh it in the SAME transaction, so a link added AFTER review produces
	// a NEW actual H1 (the stale expected hash then triggers ENVELOPE_MISMATCH).
	// Ref ordering semantics are unchanged (stored refs first, links in
	// insertion order; canonical refs dedup + sort inside the hash).
	if err := refreshEnvelopeCache(ctx, conn, memoryID); err != nil {
		return err
	}

	// Atomic receipt emission (v0.4.0 Step 3): ONLY a genuinely NEW evidence row
	// mints evidence_linked (a duplicate insert is a no-op and stays a no-op —
	// no receipt). Rule links are NOT covered by the closed action set. The
	// post-link envelope is recomputed with the merged links; the claimed act
	// uses the link actor as principalId. A signing failure rolls the whole link
	// back (no link row, no receipt).
	if table == `evidence_links` && inserted == 1 {
		linked, ok := s.readMemoryWithLinks(ctx, conn, memoryID)
		if !ok {
			return fmt.Errorf("OBSERVATION_NOT_FOUND: %s", memoryID)
		}
		if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeMemory, memoryID, core.ReceiptActionEvidenceLinked, core.ReceiptPayload{
			TenantID:         memory.Scope.OrganizationID,
			CompanyID:        memory.Scope.CompanyID,
			FiscalPeriodID:   memory.Scope.Period,
			FromEnvelopeHash: fromEnvelope,
			ToEnvelopeHash:   core.ComputeEnvelopeHash(linked),
			EvidenceRef:      ref,
			PrincipalID:      actor,
			PolicyVersion:    kernelPolicyVersion,
		}, now); err != nil {
			return err
		}
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("persistence error: commit link: %w", err)
	}
	committed = true
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
// Receipt persistence surfaces (v5, v0.4.0 Step 3 keys commit)
// ──────────────────────────────────────────────

// SigningKeyRecord is the stored public-key row the signer validates against
// (public bytes must equal the derived key, revoked keys never sign).
type SigningKeyRecord struct {
	Algorithm string
	PublicKey string
	CreatedAt string
	RevokedAt string
	Found     bool
}

// BeginReceiptTx begins the transaction the receipt surfaces run inside — the
// ONLY transaction owner on this surface (the surfaces themselves never start
// or commit one). Rotation and the batch-2 emission points commit
// register+revoke / act+receipt atomically through it.
func (s *SQLiteStore) BeginReceiptTx(ctx context.Context) (*sql.Tx, error) {
	return s.db.BeginTx(ctx, nil)
}

// RegisterPublicKey records a public signing key (INSERT OR IGNORE — the key
// may already exist; the signer validates the stored bytes BEFORE this call).
// It never starts or commits a transaction: the caller's tx owns atomicity.
func (s *SQLiteStore) RegisterPublicKey(ctx context.Context, q Queryer, keyID, algorithm, publicKey, createdAt string) error {
	if _, err := q.ExecContext(ctx, `
    		INSERT OR IGNORE INTO signing_keys (key_id, algorithm, public_key, created_at)
    		VALUES (?, ?, ?, ?)`,
		keyID, algorithm, publicKey, createdAt,
	); err != nil {
		return fmt.Errorf("register signing key %s: %w", keyID, err)
	}
	return nil
}

// RevokePublicKey performs the ONE legal signing_keys update: setting a
// previously-NULL revoked_at (the revoke-only trigger aborts anything else).
// It never starts or commits a transaction — the caller's tx owns atomicity.
func (s *SQLiteStore) RevokePublicKey(ctx context.Context, q Queryer, keyID, revokedAt string) error {
	res, err := q.ExecContext(ctx, `
    		UPDATE signing_keys SET revoked_at = ? WHERE key_id = ? AND revoked_at IS NULL`,
		revokedAt, keyID,
	)
	if err != nil {
		return fmt.Errorf("revoke signing key %s: %w", keyID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("revoke signing key %s: no unrevoked row matched", keyID)
	}
	return nil
}

// LookupSigningKey reads the stored public-key row for a key id. Found=false
// when the key was never registered. The signer uses it to fail closed on a
// revoked key and on public bytes that differ from the stored key (corruption).
func (s *SQLiteStore) LookupSigningKey(ctx context.Context, q Queryer, keyID string) (SigningKeyRecord, error) {
	var rec SigningKeyRecord
	var revokedAt sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT algorithm, public_key, created_at, revoked_at FROM signing_keys WHERE key_id = ?`,
		keyID,
	).Scan(&rec.Algorithm, &rec.PublicKey, &rec.CreatedAt, &revokedAt)
	if err == sql.ErrNoRows {
		return SigningKeyRecord{}, nil
	}
	if err != nil {
		return SigningKeyRecord{}, fmt.Errorf("lookup signing key %s: %w", keyID, err)
	}
	rec.RevokedAt = revokedAt.String
	rec.Found = true
	return rec, nil
}

// LatestReceiptChainHead returns the receipt_hash of the LATEST receipt for
// the subject (ordered by issued_at, insertion as tie-break) — the digest the
// next receipt copies into previousReceiptHash. Empty when no prior row
// exists (genesis). It never starts or commits a transaction.
func (s *SQLiteStore) LatestReceiptChainHead(ctx context.Context, q Queryer, subjectType core.SubjectType, subjectID string) (string, error) {
	var receiptHash string
	err := q.QueryRowContext(ctx, `
    		SELECT receipt_hash FROM receipts
    		WHERE subject_type = ? AND subject_id = ?
    		ORDER BY issued_at DESC, rowid DESC LIMIT 1`,
		string(subjectType), subjectID,
	).Scan(&receiptHash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read receipt chain head for %s %s: %w", subjectType, subjectID, err)
	}
	return receiptHash, nil
}

// ReceiptRow is the full stored shape of one signed receipt (the v5 receipts
// row). Signature is the RAW signature bytes; PayloadJSON is the canonical
// payload; ReceiptHash is the derived digest of the complete canonical signed
// receipt; exactly one of MemoryID/JudgmentID is set and equals SubjectID.
type ReceiptRow struct {
	SubjectType         core.SubjectType
	SubjectID           string
	Action              core.ReceiptAction
	TenantID            string
	CompanyID           string
	FiscalPeriodID      string
	PayloadHash         string
	PreviousReceiptHash string
	PrincipalID         string
	MembershipID        string
	PolicyVersion       string
	Algorithm           string
	KeyID               string
	Signature           []byte
	IssuedAt            string
	PayloadJSON         string
	ReceiptHash         string
	MemoryID            string
	JudgmentID          string
}

// InsertReceipt persists a signed receipt row. The schema guarantees
// immutability (no-update/no-delete triggers), chain identity (derived
// receipt_hash UNIQUE), exactly-one-typed-FK = subject_id, and idempotent
// retries (unique (subject_type, subject_id, action, payload_hash) aborts a
// duplicate emission). It never starts or commits a transaction — the
// caller's tx owns atomicity.
func (s *SQLiteStore) InsertReceipt(ctx context.Context, q Queryer, row ReceiptRow) error {
	var memoryID, judgmentID any
	if row.MemoryID != "" {
		memoryID = row.MemoryID
	}
	if row.JudgmentID != "" {
		judgmentID = row.JudgmentID
	}
	if _, err := q.ExecContext(ctx, `
    		INSERT INTO receipts (
    			subject_type, subject_id, action, tenant_id, company_id, fiscal_period_id,
    			payload_hash, previous_receipt_hash, principal_id, membership_id, policy_version,
    			algorithm, key_id, signature, issued_at, payload_json, receipt_hash, memory_id, judgment_id
    		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(row.SubjectType), row.SubjectID, string(row.Action),
		row.TenantID, row.CompanyID, row.FiscalPeriodID,
		row.PayloadHash, row.PreviousReceiptHash, row.PrincipalID, row.MembershipID, row.PolicyVersion,
		row.Algorithm, row.KeyID, row.Signature, row.IssuedAt,
		row.PayloadJSON, row.ReceiptHash, memoryID, judgmentID,
	); err != nil {
		return fmt.Errorf("insert receipt %s: %w", row.ReceiptHash, err)
	}
	return nil
}

// ──────────────────────────────────────────────
// Offline verification read surface (v0.4.0 Step 4)
// ──────────────────────────────────────────────

// ErrReceiptNotFound is the sentinel for a receipt lookup that matched no
// row: a bad target (unknown hash/id), never corruption — corruption
// surfaces as a wrapped error with the underlying cause. Typed/sentinel
// errors distinguish bad targets from corruption (design §4).
var ErrReceiptNotFound = errors.New("receipt not found")

// storedReceiptRow is the full stored shape of one receipt row: the signed
// envelope, the canonical payload JSON, the stored receipt_hash and the
// local row id. The unexported single-query scan keeps the narrow public
// read contracts below while avoiding N+1 payload lookups (design §4 —
// SignedReceipt intentionally excludes persistence metadata and the
// canonical payload_json).
type storedReceiptRow struct {
	rowID       int64
	receipt     core.SignedReceipt
	payloadJSON string
	storedHash  string
}

// receiptRowSelect reads every stored column the verification engine needs.
const receiptRowSelect = `SELECT id, subject_type, subject_id, action, tenant_id, company_id, fiscal_period_id,
	payload_hash, previous_receipt_hash, principal_id, membership_id, policy_version,
	algorithm, key_id, signature, issued_at, payload_json, receipt_hash FROM receipts`

// scanStoredReceiptRow maps one receipt row to its stored shape; the raw
// signature BLOB is converted to the protocol's padded base64.
func scanStoredReceiptRow(sc interface{ Scan(dest ...any) error }) (storedReceiptRow, error) {
	var (
		row         storedReceiptRow
		subjectType string
		action      string
		signature   []byte
	)
	if err := sc.Scan(
		&row.rowID, &subjectType, &row.receipt.SubjectID, &action,
		&row.receipt.TenantID, &row.receipt.CompanyID, &row.receipt.FiscalPeriodID,
		&row.receipt.PayloadHash, &row.receipt.PreviousReceiptHash,
		&row.receipt.PrincipalID, &row.receipt.MembershipID, &row.receipt.PolicyVersion,
		&row.receipt.Algorithm, &row.receipt.KeyID, &signature, &row.receipt.IssuedAt,
		&row.payloadJSON, &row.storedHash,
	); err != nil {
		return storedReceiptRow{}, err
	}
	row.receipt.SubjectType = core.SubjectType(subjectType)
	row.receipt.Action = core.ReceiptAction(action)
	row.receipt.Signature = base64.StdEncoding.EncodeToString(signature)
	return row, nil
}

// queryStoredReceiptRows runs the shared receipt-row query with the given
// WHERE/ORDER suffix (verification is read-only; it never starts a
// transaction).
func (s *SQLiteStore) queryStoredReceiptRows(ctx context.Context, suffix string, args ...any) ([]storedReceiptRow, error) {
	rows, err := s.db.QueryContext(ctx, receiptRowSelect+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]storedReceiptRow, 0, 8)
	for rows.Next() {
		row, err := scanStoredReceiptRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ReceiptsForSubject returns every signed receipt of a subject ordered by
// issued_at ASC, rowid ASC — the full chain the verification engine walks
// (design §4; ordered receipts verify complete chains, so no head-only
// shortcut is needed).
func (s *SQLiteStore) ReceiptsForSubject(ctx context.Context, subjectType core.SubjectType, subjectID string) ([]core.SignedReceipt, error) {
	rows, err := s.queryStoredReceiptRows(ctx,
		` WHERE subject_type = ? AND subject_id = ? ORDER BY issued_at ASC, rowid ASC`,
		string(subjectType), subjectID,
	)
	if err != nil {
		return nil, err
	}
	out := make([]core.SignedReceipt, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.receipt)
	}
	return out, nil
}

// ReceiptByHash returns the receipt whose stored receipt_hash equals the
// given digest — the standalone predecessor resolution (design §4).
// ErrReceiptNotFound when no row matches.
func (s *SQLiteStore) ReceiptByHash(ctx context.Context, receiptHash string) (core.SignedReceipt, error) {
	rows, err := s.queryStoredReceiptRows(ctx, ` WHERE receipt_hash = ? LIMIT 1`, receiptHash)
	if err != nil {
		return core.SignedReceipt{}, err
	}
	if len(rows) == 0 {
		return core.SignedReceipt{}, fmt.Errorf("%w: %s", ErrReceiptNotFound, receiptHash)
	}
	return rows[0].receipt, nil
}

// ReceiptByID returns the receipt with the given local SQLite row id — the
// numeric convenience selector (design §5; the hash is the portable
// identity). ErrReceiptNotFound when no row matches.
func (s *SQLiteStore) ReceiptByID(ctx context.Context, id int64) (core.SignedReceipt, error) {
	rows, err := s.queryStoredReceiptRows(ctx, ` WHERE id = ? LIMIT 1`, id)
	if err != nil {
		return core.SignedReceipt{}, err
	}
	if len(rows) == 0 {
		return core.SignedReceipt{}, fmt.Errorf("%w: id %d", ErrReceiptNotFound, id)
	}
	return rows[0].receipt, nil
}

// ReceiptPayloadByHash returns the canonical payload JSON, the stored
// receipt_hash and the row id of the receipt identified by its stored hash
// — the accessor the verification engine needs because SignedReceipt
// intentionally excludes persistence metadata and the canonical payload
// (design §4). ErrReceiptNotFound when no row matches.
func (s *SQLiteStore) ReceiptPayloadByHash(ctx context.Context, receiptHash string) (payloadJSON string, storedHash string, rowID int64, err error) {
	rows, err := s.queryStoredReceiptRows(ctx, ` WHERE receipt_hash = ? LIMIT 1`, receiptHash)
	if err != nil {
		return "", "", 0, err
	}
	if len(rows) == 0 {
		return "", "", 0, fmt.Errorf("%w: %s", ErrReceiptNotFound, receiptHash)
	}
	return rows[0].payloadJSON, rows[0].storedHash, rows[0].rowID, nil
}

// SigningKeyForVerify reads a signing-key row through the pool connection
// — the read-only verification surface. It reuses LookupSigningKey
// (verification never starts a transaction).
func (s *SQLiteStore) SigningKeyForVerify(ctx context.Context, keyID string) (SigningKeyRecord, error) {
	return s.LookupSigningKey(ctx, s.db, keyID)
}

// ReceiptActProvenance resolves the immutable event snapshot a covered-act
// receipt must match: approval_events for memory_approved, judgment_events
// for relation_confirmed/relation_rejected, transition_log for the terminal
// memory transitions, the observations row for memory_recorded and
// evidence_links for evidence_linked (design §4). Records correlate by
// (subject, action, issued_at) because receipts carry no event-id column;
// ZERO matches return found=false and MULTIPLE matches are rejected as
// ambiguity — fail closed (corruption is never a successful skip).
func (s *SQLiteStore) ReceiptActProvenance(ctx context.Context, subjectType core.SubjectType, subjectID string, action core.ReceiptAction, issuedAt string) (core.ActProvenance, bool, error) {
	switch action {
	case core.ReceiptActionMemoryApproved:
		return s.approvalEventProvenance(ctx, subjectID, issuedAt)
	case core.ReceiptActionRelationConfirmed:
		return s.judgmentEventProvenance(ctx, subjectID, "confirm", issuedAt)
	case core.ReceiptActionRelationRejected:
		return s.judgmentEventProvenance(ctx, subjectID, "reject", issuedAt)
	case core.ReceiptActionMemoryRecorded:
		return s.recordedObservationProvenance(ctx, subjectID, issuedAt)
	case core.ReceiptActionMemoryRejected, core.ReceiptActionMemoryVoided, core.ReceiptActionMemorySuperseded:
		return s.transitionLogProvenance(ctx, subjectID, strings.TrimPrefix(string(action), "memory_"), issuedAt)
	case core.ReceiptActionEvidenceLinked:
		return s.evidenceLinkProvenance(ctx, subjectID, issuedAt)
	default:
		return core.ActProvenance{}, false, fmt.Errorf("provenance: unknown receipt action %q", action)
	}
}

// approvalEventProvenance maps an approval_events row to the immutable
// snapshot a memory_approved receipt must match.
func (s *SQLiteStore) approvalEventProvenance(ctx context.Context, memoryID, issuedAt string) (core.ActProvenance, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT principal_subject_id, membership_id, principal_roles_json, authentication_method,
			assurance_level, principal_authenticated_at, policy_version, reason,
			reviewed_envelope_hash, resulting_envelope_hash, created_at
		FROM approval_events WHERE memory_id = ? AND created_at = ?`, memoryID, issuedAt)
	if err != nil {
		return core.ActProvenance{}, false, err
	}
	defer func() { _ = rows.Close() }()
	var (
		count      int
		principal  string
		membership string
		rolesJSON  string
		authMethod string
		assurance  string
		authdAt    string
		policy     string
		reason     string
		revEnv     string
		resEnv     string
		created    string
	)
	for rows.Next() {
		count++
		if err := rows.Scan(&principal, &membership, &rolesJSON, &authMethod,
			&assurance, &authdAt, &policy, &reason, &revEnv, &resEnv, &created); err != nil {
			return core.ActProvenance{}, false, err
		}
	}
	if err := rows.Err(); err != nil {
		return core.ActProvenance{}, false, err
	}
	if count == 0 {
		return core.ActProvenance{}, false, nil
	}
	if count > 1 {
		return core.ActProvenance{}, false, fmt.Errorf("provenance: %d approval_events match memory %s at %s — ambiguity", count, memoryID, issuedAt)
	}
	var roles []string
	if err := json.Unmarshal([]byte(rolesJSON), &roles); err != nil {
		return core.ActProvenance{}, false, fmt.Errorf("provenance: corrupt approval event roles for memory %s: %w", memoryID, err)
	}
	return core.ActProvenance{
		Action:                "approved",
		Timestamp:             created,
		PrincipalID:           principal,
		MembershipID:          membership,
		Roles:                 roles,
		AuthenticationMethod:  authMethod,
		AssuranceLevel:        assurance,
		AuthenticatedAt:       authdAt,
		Policy:                policy,
		Reason:                reason,
		ReviewedEnvelopeHash:  revEnv,
		ResultingEnvelopeHash: resEnv,
	}, true, nil
}

// judgmentEventProvenance maps a confirm/reject judgment_events row to the
// immutable snapshot a relation_confirmed/relation_rejected receipt must
// match. The event records the RESULTING state hash (the exact hash the
// decided row now hashes to) in judgment_hash — the recordedJudgmentHash
// agreement the verification engine checks.
func (s *SQLiteStore) judgmentEventProvenance(ctx context.Context, judgmentID, eventAction, issuedAt string) (core.ActProvenance, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT judgment_hash, principal_snapshot_json, policy_version, reason, created_at
		FROM judgment_events WHERE judgment_id = ? AND created_at = ? AND action = ?`,
		judgmentID, issuedAt, eventAction)
	if err != nil {
		return core.ActProvenance{}, false, err
	}
	defer func() { _ = rows.Close() }()
	var (
		count        int
		judgmentHash string
		snapshotJSON string
		policy       string
		reason       string
		created      string
	)
	for rows.Next() {
		count++
		if err := rows.Scan(&judgmentHash, &snapshotJSON, &policy, &reason, &created); err != nil {
			return core.ActProvenance{}, false, err
		}
	}
	if err := rows.Err(); err != nil {
		return core.ActProvenance{}, false, err
	}
	if count == 0 {
		return core.ActProvenance{}, false, nil
	}
	if count > 1 {
		return core.ActProvenance{}, false, fmt.Errorf("provenance: %d %s events match judgment %s at %s — ambiguity", count, eventAction, judgmentID, issuedAt)
	}
	var snapshot auth.PrincipalSnapshot
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
		return core.ActProvenance{}, false, fmt.Errorf("provenance: corrupt %s event snapshot for judgment %s: %w", eventAction, judgmentID, err)
	}
	roles := make([]string, 0, len(snapshot.Roles))
	for _, r := range snapshot.Roles {
		roles = append(roles, string(r))
	}
	return core.ActProvenance{
		Action:               eventAction,
		Timestamp:            created,
		PrincipalID:          snapshot.SubjectID,
		MembershipID:         snapshot.MembershipID,
		Roles:                roles,
		AuthenticationMethod: string(snapshot.AuthenticationMethod),
		AssuranceLevel:       string(snapshot.AssuranceLevel),
		AuthenticatedAt:      snapshot.AuthenticatedAt,
		Policy:               policy,
		Reason:               reason,
		RecordedJudgmentHash: judgmentHash,
	}, true, nil
}

// recordedObservationProvenance maps the immutable recorded source of a
// memory (the observations row) to the claimed-act snapshot a
// memory_recorded receipt must match (attribution continuity).
func (s *SQLiteStore) recordedObservationProvenance(ctx context.Context, memoryID, issuedAt string) (core.ActProvenance, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT actor, timestamp FROM observations WHERE id = ? AND timestamp = ?`, memoryID, issuedAt)
	if err != nil {
		return core.ActProvenance{}, false, err
	}
	defer func() { _ = rows.Close() }()
	var (
		count int
		actor string
		ts    string
	)
	for rows.Next() {
		count++
		if err := rows.Scan(&actor, &ts); err != nil {
			return core.ActProvenance{}, false, err
		}
	}
	if err := rows.Err(); err != nil {
		return core.ActProvenance{}, false, err
	}
	if count == 0 {
		return core.ActProvenance{}, false, nil
	}
	if count > 1 {
		return core.ActProvenance{}, false, fmt.Errorf("provenance: %d observation rows match memory %s at %s — ambiguity", count, memoryID, issuedAt)
	}
	return core.ActProvenance{Action: "recorded", Timestamp: ts, PrincipalID: actor}, true, nil
}

// transitionLogProvenance maps a transition_log row to the claimed-act
// snapshot a memory_rejected/memory_voided/memory_superseded receipt must
// match (attribution continuity).
func (s *SQLiteStore) transitionLogProvenance(ctx context.Context, memoryID, toStatus, issuedAt string) (core.ActProvenance, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT actor, to_status, timestamp FROM transition_log
		WHERE observation_id = ? AND timestamp = ? AND to_status = ?`, memoryID, issuedAt, toStatus)
	if err != nil {
		return core.ActProvenance{}, false, err
	}
	defer func() { _ = rows.Close() }()
	var (
		count  int
		actor  string
		status string
		ts     string
	)
	for rows.Next() {
		count++
		if err := rows.Scan(&actor, &status, &ts); err != nil {
			return core.ActProvenance{}, false, err
		}
	}
	if err := rows.Err(); err != nil {
		return core.ActProvenance{}, false, err
	}
	if count == 0 {
		return core.ActProvenance{}, false, nil
	}
	if count > 1 {
		return core.ActProvenance{}, false, fmt.Errorf("provenance: %d transition_log rows match memory %s at %s — ambiguity", count, memoryID, issuedAt)
	}
	return core.ActProvenance{Action: status, Timestamp: ts, PrincipalID: actor}, true, nil
}

// evidenceLinkProvenance maps an evidence_links row to the claimed-act
// snapshot an evidence_linked receipt must match (attribution continuity).
func (s *SQLiteStore) evidenceLinkProvenance(ctx context.Context, memoryID, issuedAt string) (core.ActProvenance, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT actor, timestamp FROM evidence_links WHERE memory_id = ? AND timestamp = ?`, memoryID, issuedAt)
	if err != nil {
		return core.ActProvenance{}, false, err
	}
	defer func() { _ = rows.Close() }()
	var (
		count int
		actor string
		ts    string
	)
	for rows.Next() {
		count++
		if err := rows.Scan(&actor, &ts); err != nil {
			return core.ActProvenance{}, false, err
		}
	}
	if err := rows.Err(); err != nil {
		return core.ActProvenance{}, false, err
	}
	if count == 0 {
		return core.ActProvenance{}, false, nil
	}
	if count > 1 {
		return core.ActProvenance{}, false, fmt.Errorf("provenance: %d evidence_links rows match memory %s at %s — ambiguity", count, memoryID, issuedAt)
	}
	return core.ActProvenance{Action: "linked", Timestamp: ts, PrincipalID: actor}, true, nil
}

// EvidenceLinkRefs returns the CURRENT evidence_links rows of a memory — the
// immutable snapshot of what evidence is available now (design §3; the
// verification engine reads it through the store, never raw SQL).
func (s *SQLiteStore) EvidenceLinkRefs(ctx context.Context, memoryID string) ([]string, error) {
	return s.linkRefsByID(ctx, `evidence_links`, memoryID)
}

// RuleLinkRefs returns the CURRENT rule_links rows of a memory (design §3
// rule availability).
func (s *SQLiteStore) RuleLinkRefs(ctx context.Context, memoryID string) ([]string, error) {
	return s.linkRefsByID(ctx, `rule_links`, memoryID)
}

func (s *SQLiteStore) linkRefsByID(ctx context.Context, table, memoryID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ref FROM `+table+` WHERE memory_id = ? ORDER BY rowid`, memoryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
