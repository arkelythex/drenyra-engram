package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"strings"
)

const fiscalScopeV18DDL = `
CREATE TABLE fiscal_scope_bindings (binding_hash TEXT PRIMARY KEY, version TEXT NOT NULL CHECK(version='v1'), tenant TEXT NOT NULL, organization TEXT NOT NULL, company TEXT NOT NULL, fiscal_period TEXT NOT NULL, ledger_book TEXT NOT NULL, operation_type TEXT NOT NULL, source_snapshot TEXT NOT NULL, policy_version TEXT NOT NULL, actor TEXT NOT NULL, authority_level TEXT NOT NULL CHECK(authority_level IN ('ASK','ANALYZE','PREPARE','EXECUTE')), canonical_bytes BLOB NOT NULL, created_at TEXT NOT NULL CHECK(length(created_at)>0));
CREATE INDEX idx_fiscal_scope_exact ON fiscal_scope_bindings(tenant,organization,company,fiscal_period);
CREATE TABLE fiscal_binding_links (id TEXT PRIMARY KEY CHECK(length(id)>0), subject_type TEXT NOT NULL CHECK(length(subject_type)>0), subject_id TEXT NOT NULL CHECK(length(subject_id)>0), sequence INTEGER NOT NULL CHECK(sequence>0), binding_hash TEXT NOT NULL REFERENCES fiscal_scope_bindings(binding_hash), act_evidence_hash TEXT NOT NULL, reviewed_envelope_hash TEXT NOT NULL DEFAULT '', resulting_envelope_hash TEXT NOT NULL, evidence_inspected INTEGER CHECK(evidence_inspected IS NULL OR evidence_inspected IN (0,1)), rules_inspected INTEGER CHECK(rules_inspected IS NULL OR rules_inspected IN (0,1)), audit_ref_type TEXT NOT NULL CHECK(length(audit_ref_type)>0), audit_ref_id TEXT NOT NULL CHECK(length(audit_ref_id)>0), created_at TEXT NOT NULL CHECK(length(created_at)>0), UNIQUE(subject_type,subject_id,sequence), UNIQUE(subject_type,subject_id,binding_hash,act_evidence_hash));
CREATE INDEX idx_fiscal_links_subject ON fiscal_binding_links(subject_type,subject_id,sequence);
CREATE TRIGGER fiscal_scope_bindings_no_update BEFORE UPDATE ON fiscal_scope_bindings BEGIN SELECT RAISE(ABORT,'IMMUTABLE_FISCAL_SCOPE_BINDING'); END;
CREATE TRIGGER fiscal_scope_bindings_no_delete BEFORE DELETE ON fiscal_scope_bindings BEGIN SELECT RAISE(ABORT,'IMMUTABLE_FISCAL_SCOPE_BINDING'); END;
CREATE TRIGGER fiscal_binding_links_no_update BEFORE UPDATE ON fiscal_binding_links BEGIN SELECT RAISE(ABORT,'IMMUTABLE_FISCAL_BINDING_LINK'); END;
CREATE TRIGGER fiscal_binding_links_no_delete BEFORE DELETE ON fiscal_binding_links BEGIN SELECT RAISE(ABORT,'IMMUTABLE_FISCAL_BINDING_LINK'); END;`

func requireSupportedSchemaVersion(version, supported int) error {
	if version == supported {
		return nil
	}
	return fmt.Errorf("unsupported store layout: schema_version=%d, supported=%d — fail closed; migrate additively, never rewrite", version, supported)
}
func migrateV17ToV18(db *sql.DB) error {
	ctx := context.Background()
	for _, name := range []string{"fiscal_scope_bindings", "fiscal_binding_links"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil || n != 0 {
			if err != nil {
				return fmt.Errorf("v18 migration: inspect %s: %w", name, err)
			}
			return fmt.Errorf("v18 migration: pre-existing %s is a partial/foreign migration", name)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("v18 migration: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, fiscalScopeV18DDL); err != nil {
		return fmt.Errorf("v18 migration: additive schema: %w", err)
	}
	if err := setSchemaVersionTx(ctx, tx, 18); err != nil {
		return fmt.Errorf("v18 migration: schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("v18 migration: commit: %w", err)
	}
	committed = true
	return nil
}

type FiscalScopeClass string

const (
	FiscalScopeLegacy       FiscalScopeClass = "legacy"
	FiscalScopeV1           FiscalScopeClass = "v1"
	FiscalScopeUnverifiable FiscalScopeClass = "unverifiable"
)

type FiscalBindingLink struct {
	ID, SubjectType, SubjectID                                                string
	Sequence                                                                  int
	BindingHash, ActEvidenceHash, ReviewedEnvelopeHash, ResultingEnvelopeHash string
	EvidenceInspected, RulesInspected                                         *bool
	AuditRefType, AuditRefID, CreatedAt                                       string
}

func (s *SQLiteStore) StoreFiscalScopeBinding(ctx context.Context, binding core.FiscalScopeBinding, createdAt string) error {
	if err := core.ValidateFiscalScopeBinding(binding); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO fiscal_scope_bindings VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		core.FiscalScopeHash(binding), binding.Version, binding.Tenant, binding.Organization, binding.Company,
		binding.FiscalPeriod, binding.LedgerBook, binding.OperationType, binding.SourceSnapshot, binding.PolicyVersion,
		binding.Actor, binding.AuthorityLevel, core.CanonicalFiscalScopeBytes(binding), createdAt)
	return err
}
func (s *SQLiteStore) StoreFiscalBindingLink(ctx context.Context, link FiscalBindingLink) error {
	if link.Sequence < 1 || !sha256Text(link.BindingHash) || !sha256Text(link.ActEvidenceHash) || !sha256Text(link.ResultingEnvelopeHash) || (link.ReviewedEnvelopeHash != "" && !sha256Text(link.ReviewedEnvelopeHash)) {
		return fmt.Errorf("SCOPE_BINDING_INVALID: invalid fiscal link sequence or hash")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO fiscal_binding_links VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, link.ID, link.SubjectType, link.SubjectID,
		link.Sequence, link.BindingHash, link.ActEvidenceHash, link.ReviewedEnvelopeHash, link.ResultingEnvelopeHash,
		link.EvidenceInspected, link.RulesInspected, link.AuditRefType, link.AuditRefID, link.CreatedAt)
	return err
}
func sha256Text(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == 64 && value == strings.ToLower(value) && err == nil
}
func (s *SQLiteStore) ClassifyFiscalSubject(ctx context.Context, subjectType, subjectID string) (FiscalScopeClass, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.sequence,b.binding_hash,b.version,b.tenant,b.organization,b.company,b.fiscal_period,b.ledger_book,b.operation_type,b.source_snapshot,b.policy_version,b.actor,b.authority_level,b.canonical_bytes,l.act_evidence_hash,l.reviewed_envelope_hash,l.resulting_envelope_hash,l.audit_ref_type,l.audit_ref_id FROM fiscal_binding_links l JOIN fiscal_scope_bindings b ON b.binding_hash=l.binding_hash WHERE l.subject_type=? AND l.subject_id=? ORDER BY l.sequence`, subjectType, subjectID)
	if err != nil {
		return FiscalScopeUnverifiable, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var sequence int
		var hash, canonical, act, reviewed, resulting, auditType, auditID string
		var b core.FiscalScopeBinding
		if err := rows.Scan(&sequence, &hash, &b.Version, &b.Tenant, &b.Organization, &b.Company, &b.FiscalPeriod, &b.LedgerBook, &b.OperationType, &b.SourceSnapshot, &b.PolicyVersion, &b.Actor, &b.AuthorityLevel, &canonical, &act, &reviewed, &resulting, &auditType, &auditID); err != nil {
			return FiscalScopeUnverifiable, err
		}
		if sequence != count || core.ValidateFiscalScopeBinding(b) != nil || hash != core.FiscalScopeHash(b) || !bytes.Equal([]byte(canonical), core.CanonicalFiscalScopeBytes(b)) || !sha256Text(act) || !sha256Text(resulting) || (reviewed != "" && !sha256Text(reviewed)) || auditType == "" || auditID == "" {
			return FiscalScopeUnverifiable, nil
		}
	}
	if err := rows.Err(); err != nil {
		return FiscalScopeUnverifiable, err
	}
	if count == 0 {
		return FiscalScopeLegacy, nil
	}
	return FiscalScopeV1, nil
}

// FiscalBindingEvidence is the read-only, VERIFICATION-oriented projection of
// one subject's persisted fiscal_binding_links + joined fiscal_scope_bindings
// rows, in sequence order, with each link's logical audit reference resolved
// against its declared type (design.md "Verification and audit" — audit-
// anchor resolution). Unlike ClassifyFiscalSubject (a cheap legacy/v1/
// unverifiable classification consumed by mutation guards), this method loads
// every field the pure core.VerifyFiscalScopeBinding layer needs, including
// the review-acknowledgement tri-state and audit-anchor existence. A subject
// with no persisted links returns (nil, nil) — the legacy/unbound case.
func (s *SQLiteStore) FiscalBindingEvidence(ctx context.Context, subjectType, subjectID string) ([]core.FiscalBindingLinkEvidence, error) {
	// IMPORTANT: the store's *sql.DB is configured with SetMaxOpenConns(1)
	// (store.go Open) — the pool has exactly ONE connection. This query's rows
	// cursor MUST be fully drained and closed BEFORE any further query runs on
	// s.db (including resolveFiscalAuditAnchor below); nesting a second
	// s.db.QueryRowContext call while this rows cursor is still open would wait
	// forever for a connection the open cursor itself is holding — a real
	// self-deadlock this method used to have until a focused test caught it.
	rows, err := s.db.QueryContext(ctx, `SELECT l.sequence,b.binding_hash,b.version,b.tenant,b.organization,b.company,b.fiscal_period,b.ledger_book,b.operation_type,b.source_snapshot,b.policy_version,b.actor,b.authority_level,b.canonical_bytes,l.act_evidence_hash,l.reviewed_envelope_hash,l.resulting_envelope_hash,l.evidence_inspected,l.rules_inspected,l.audit_ref_type,l.audit_ref_id FROM fiscal_binding_links l JOIN fiscal_scope_bindings b ON b.binding_hash=l.binding_hash WHERE l.subject_type=? AND l.subject_id=? ORDER BY l.sequence`, subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	type rawLink struct {
		sequence                                  int
		hash, canonical, act, reviewed, resulting string
		auditType, auditID                        string
		binding                                   core.FiscalScopeBinding
		evidenceInspected, rulesInspected         sql.NullBool
	}
	var raw []rawLink
	for rows.Next() {
		var r rawLink
		if err := rows.Scan(&r.sequence, &r.hash, &r.binding.Version, &r.binding.Tenant, &r.binding.Organization, &r.binding.Company, &r.binding.FiscalPeriod, &r.binding.LedgerBook, &r.binding.OperationType, &r.binding.SourceSnapshot, &r.binding.PolicyVersion, &r.binding.Actor, &r.binding.AuthorityLevel, &r.canonical, &r.act, &r.reviewed, &r.resulting, &r.evidenceInspected, &r.rulesInspected, &r.auditType, &r.auditID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		raw = append(raw, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []core.FiscalBindingLinkEvidence
	for _, r := range raw {
		resolved, err := s.resolveFiscalAuditAnchor(ctx, r.auditType, r.auditID)
		if err != nil {
			return nil, err
		}
		out = append(out, core.FiscalBindingLinkEvidence{
			Sequence:              r.sequence,
			Binding:               r.binding,
			BindingHash:           r.hash,
			CanonicalBytes:        []byte(r.canonical),
			ActEvidenceHash:       r.act,
			ReviewedEnvelopeHash:  r.reviewed,
			ResultingEnvelopeHash: r.resulting,
			EvidenceInspected:     fiscalAckFromNullable(r.evidenceInspected),
			RuleInspected:         fiscalAckFromNullable(r.rulesInspected),
			AuditAnchorResolved:   resolved,
		})
	}
	return out, nil
}

// fiscalAckFromNullable maps a nullable SQLite acknowledgement column
// (NULL=omitted, 0=provided false, 1=provided true — design.md "Immutable act
// evidence and envelope linkage") back into the tri-state
// core.ReviewAcknowledgement.
func fiscalAckFromNullable(v sql.NullBool) core.ReviewAcknowledgement {
	if !v.Valid {
		return core.ReviewAcknowledgement{}
	}
	return core.ReviewAcknowledgement{Present: true, Value: v.Bool}
}

// nullableFiscalAck is the Slice 5 forward mapping (the symmetric inverse of
// fiscalAckFromNullable above): a tri-state core.ReviewAcknowledgement into
// the nullable SQLite column value a fiscal_binding_links INSERT binds
// (omitted -> nil/NULL, provided false/true -> the bool). Centralizing this
// one mapping keeps every fiscal act-evidence writer (ApproveMemory today)
// from re-deriving the NULL-vs-bool rule inline.
func nullableFiscalAck(a core.ReviewAcknowledgement) any {
	if !a.Present {
		return nil
	}
	return a.Value
}

// resolveFiscalAuditAnchor reports whether one fiscal_binding_links row's
// logical audit reference resolves to a persisted record of its declared type
// (design.md "Verification and audit" — "unresolved audit anchor" fails
// closed). Every Slice 3 protected write records audit_ref_type as either
// "observation" (memory.save/supersede/evidence.link/rule.link — the memory
// subject itself) or "evidence_object" (evidence.store); an unknown type never
// resolves — the store never guesses a new anchor kind.
func (s *SQLiteStore) resolveFiscalAuditAnchor(ctx context.Context, auditRefType, auditRefID string) (bool, error) {
	var query string
	switch auditRefType {
	case "observation":
		query = `SELECT COUNT(*) FROM observations WHERE id = ?`
	case "evidence_object":
		query = `SELECT COUNT(*) FROM evidence_objects WHERE id = ?`
	default:
		return false, nil
	}
	var n int
	if err := s.db.QueryRowContext(ctx, query, auditRefID).Scan(&n); err != nil {
		return false, err
	}
	return n == 1, nil
}

type FiscalInventoryReport struct {
	LegacyCount, V1Count, UnverifiableCount int
	InvalidLegacyRUCCount                   int
	InvalidLegacyOpaqueIDs                  []string
}

func (s *SQLiteStore) FiscalInventory(ctx context.Context) (FiscalInventoryReport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,organization_id,ruc FROM observations WHERE scope_kind='company' ORDER BY id`)
	if err != nil {
		return FiscalInventoryReport{}, err
	}
	var records []struct{ id, tenant, ruc string }
	for rows.Next() {
		var record struct{ id, tenant, ruc string }
		if err := rows.Scan(&record.id, &record.tenant, &record.ruc); err != nil {
			_ = rows.Close()
			return FiscalInventoryReport{}, err
		}
		records = append(records, record)
	}
	_ = rows.Close()
	var report FiscalInventoryReport
	for _, record := range records {
		class, err := s.ClassifyFiscalSubject(ctx, "memory", record.id)
		if err != nil {
			return report, err
		}
		switch class {
		case FiscalScopeLegacy:
			report.LegacyCount++
		case FiscalScopeV1:
			report.V1Count++
		default:
			report.UnverifiableCount++
		}
		if class == FiscalScopeLegacy && !core.IsValidFiscalRUC(record.ruc) {
			sum := sha256.Sum256([]byte("drenyra:fiscal-inventory:v1\x00" + record.tenant + "\x00observations\x00" + record.id))
			report.InvalidLegacyRUCCount++
			report.InvalidLegacyOpaqueIDs = append(report.InvalidLegacyOpaqueIDs, hex.EncodeToString(sum[:]))
		}
	}
	return report, nil
}

type FiscalRuntimeMode string

const (
	FiscalRuntimeShadow       FiscalRuntimeMode = "shadow"
	FiscalRuntimeEnforce      FiscalRuntimeMode = "enforce"
	FiscalRuntimeLegacyCompat FiscalRuntimeMode = "legacy_compat"
	FiscalRuntimeReadOnly     FiscalRuntimeMode = "read_only"
)

func ParseFiscalRuntimeMode(raw string) (FiscalRuntimeMode, error) {
	if raw == "" {
		return FiscalRuntimeShadow, nil
	}
	mode := FiscalRuntimeMode(raw)
	switch mode {
	case FiscalRuntimeShadow, FiscalRuntimeEnforce, FiscalRuntimeLegacyCompat, FiscalRuntimeReadOnly:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid DRENYRA_FISCAL_RUNTIME_MODE %q", raw)
	}
}
func (mode FiscalRuntimeMode) CheckProtectedWrite(class FiscalScopeClass) error {
	if mode == FiscalRuntimeEnforce && class == FiscalScopeV1 || mode == FiscalRuntimeLegacyCompat && class == FiscalScopeLegacy {
		return nil
	}
	if mode == FiscalRuntimeEnforce && class == FiscalScopeLegacy {
		return &core.FiscalScopeError{Code: core.ScopeBindingRequired, Message: "legacy protected mutation requires an explicit compatibility mode"}
	}
	return auth.New(auth.CodeFiscalWriteGateClosed, "fiscal protected writes are disabled by runtime mode")
}

// ──────────────────────────────────────────────
// Delivery Slice 3 — envelope linkage and store-authoritative enforcement
// ──────────────────────────────────────────────
//
// The helpers below are the store-authoritative primitives that let
// Save/SupersedeExplicit/AddEvidenceLinksBound/AddRuleLinksBound/
// StoreObjectWithFiscalIntent persist ONE immutable fiscal act-evidence link
// atomically with the protected act (design.md "Immutable act evidence and
// envelope linkage" and "Enforcement and data flow"). They never authorize
// anything: a core.FiscalWriteIntent only proves the caller's SHAPE-correct
// command intent; authentication, membership, role, assurance, separation of
// duties and the closed-period gate remain independently enforced exactly as
// before.

// fiscalBindingLinkContributionsTx returns a subject's persisted fiscal
// binding links in sequence order, on the caller's connection/transaction.
// Callers that gate mutation on the result (bypass/tamper enforcement) MUST
// use this error-propagating form; fiscalBindingLinkContributionsBestEffort
// below is for envelope-hash population only (mirrors the tolerant
// linkRefsQuery style already used for evidence/rule refs).
func fiscalBindingLinkContributionsTx(ctx context.Context, q Queryer, subjectType, subjectID string) ([]core.FiscalBindingLinkContribution, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence, binding_hash, act_evidence_hash FROM fiscal_binding_links WHERE subject_type = ? AND subject_id = ? ORDER BY sequence`, subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []core.FiscalBindingLinkContribution
	for rows.Next() {
		var c core.FiscalBindingLinkContribution
		if err := rows.Scan(&c.Sequence, &c.BindingHash, &c.ActEvidenceHash); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// fiscalBindingLinkContributionsBestEffort is the tolerant read used to
// populate AccountingMemory.FiscalLinks for a read path only (readMemory /
// readMemoryWithLinks): a read failure degrades to "no links" (legacy
// contribution), mirroring linkRefsQuery's existing tolerance for
// evidence/rule refs. It is NEVER used to decide whether a protected mutation
// may proceed — that decision always uses the error-propagating form above —
// and it is NOT used to populate what gets PERSISTED into the envelope-hash
// cache (refreshEnvelopeCache uses the error-propagating form for exactly
// that reason: a transient read failure there must abort the refresh, not
// silently persist a wrong cached hash for a v1 fiscal-bound subject).
func fiscalBindingLinkContributionsBestEffort(ctx context.Context, q Queryer, subjectType, subjectID string) []core.FiscalBindingLinkContribution {
	links, err := fiscalBindingLinkContributionsTx(ctx, q, subjectType, subjectID)
	if err != nil {
		return nil
	}
	return links
}

// storeFiscalScopeBindingTx persists (or reuses) the content-addressed
// fiscal_scope_bindings row for one binding, on the caller's
// connection/transaction, and returns its canonical hash. The binding is
// structurally validated first — an invalid binding is never persisted.
func storeFiscalScopeBindingTx(ctx context.Context, q Queryer, binding core.FiscalScopeBinding, createdAt string) (string, error) {
	if err := core.ValidateFiscalScopeBinding(binding); err != nil {
		return "", err
	}
	hash := core.FiscalScopeHash(binding)
	var exists int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM fiscal_scope_bindings WHERE binding_hash = ?`, hash).Scan(&exists); err != nil {
		return "", err
	}
	if exists == 0 {
		if _, err := q.ExecContext(ctx, `INSERT INTO fiscal_scope_bindings VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			hash, binding.Version, binding.Tenant, binding.Organization, binding.Company, binding.FiscalPeriod,
			binding.LedgerBook, binding.OperationType, binding.SourceSnapshot, binding.PolicyVersion, binding.Actor,
			binding.AuthorityLevel, core.CanonicalFiscalScopeBytes(binding), createdAt,
		); err != nil {
			return "", err
		}
	}
	return hash, nil
}

// verifyFiscalIntentAxes validates a caller-supplied intent's SHAPE (a nil
// intent is always accepted — the legacy path) and, when present, that it
// names the exact protected boundary being invoked and that its trusted
// company axes (tenant, organization, company/RUC, fiscal period) agree with
// the subject's OWN immutable scope. It never inspects or requires any prior
// binding history — operationType/actor/authorityLevel MAY legitimately
// differ across acts on the same subject (design.md).
func verifyFiscalIntentAxes(intent *core.FiscalWriteIntent, expectedOperation string, scope core.Scope) error {
	if intent == nil {
		return nil
	}
	if err := core.ValidateFiscalScopeBinding(intent.Binding); err != nil {
		return err
	}
	if intent.Binding.OperationType != expectedOperation {
		return &core.FiscalScopeError{Code: core.ScopeBindingInvalid, Message: "operationType does not match the invoked protected boundary"}
	}
	if intent.Binding.Tenant != scope.OrganizationID ||
		intent.Binding.Organization != scope.CompanyID ||
		intent.Binding.Company != scope.RUC ||
		(scope.Period != "" && intent.Binding.FiscalPeriod != scope.Period) {
		return &core.FiscalScopeError{Code: core.ScopeMismatch, Message: "binding does not match the trusted scope axes"}
	}
	return nil
}

// requireFiscalIntentForBoundSubject is the direct-store-bypass guard
// (spec.md "Direct store bypass is denied"): once a subject already carries
// at least one persisted fiscal binding link (v1-bound), every further
// protected mutation MUST supply a (shape/axis-verified) intent — a legacy
// caller presenting only the old tuple, or no intent at all, fails closed. A
// subject with NO existing links is still legacy/unbound: supplying an
// intent for the FIRST time is the explicit upgrade moment, never a bypass.
func requireFiscalIntentForBoundSubject(existing []core.FiscalBindingLinkContribution, intent *core.FiscalWriteIntent) error {
	if len(existing) == 0 {
		return nil
	}
	if intent == nil {
		return &core.FiscalScopeError{Code: core.ScopeBindingRequired, Message: "a v1-bound subject requires the complete current binding"}
	}
	return nil
}

// appendFiscalBindingLinkTx persists the fiscal_scope_bindings row (if new)
// and appends the NEXT-sequence fiscal_binding_links row for one subject, on
// the caller's connection/transaction. resultingEnvelopeHash is the caller's
// ALREADY-COMPUTED envelope/content-address hash of the act's resulting
// state (memory envelope hash for "memory" subjects; the WORM content
// address for "evidence_object" subjects) — it is opaque to this helper. It
// returns the new link's contribution (for building the resulting in-memory
// FiscalLinks slice / receipt fields).
func appendFiscalBindingLinkTx(ctx context.Context, q Queryer, subjectType, subjectID string, existing []core.FiscalBindingLinkContribution, intent core.FiscalWriteIntent, resultingEnvelopeHash, auditRefType, auditRefID, createdAt string) (core.FiscalBindingLinkContribution, error) {
	bindingHash, err := storeFiscalScopeBindingTx(ctx, q, intent.Binding, createdAt)
	if err != nil {
		return core.FiscalBindingLinkContribution{}, err
	}
	actEvidenceHash := core.ComputeActEvidenceHash(bindingHash, "", core.ReviewAcknowledgement{}, core.ReviewAcknowledgement{})
	sequence := len(existing) + 1
	linkID, err := newUUID()
	if err != nil {
		return core.FiscalBindingLinkContribution{}, err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO fiscal_binding_links VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		linkID, subjectType, subjectID, sequence, bindingHash, actEvidenceHash, "", resultingEnvelopeHash, nil, nil, auditRefType, auditRefID, createdAt,
	); err != nil {
		return core.FiscalBindingLinkContribution{}, fmt.Errorf("persistence error: insert fiscal binding link: %w", err)
	}
	return core.FiscalBindingLinkContribution{Sequence: sequence, BindingHash: bindingHash, ActEvidenceHash: actEvidenceHash}, nil
}
