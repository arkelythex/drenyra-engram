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
