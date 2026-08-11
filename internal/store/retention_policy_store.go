// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the persisted retention-policy layer of the
// v0.8 evidence lifecycle (batch 2 — docs/architecture/evidence-lifecycle-v0.8.md
// §3.1/§4/§6/§9):
//
//   - the v8→v9 migration is ONE fail-closed transaction that (a) aborts when
//     ANY new table already exists (corruption signal), (b) creates the
//     immutable retention_policies table (exact scope columns + scope index +
//     no-update/no-delete triggers) and the tenant-scoped
//     retention_policy_idempotency_keys ledger, (c) rebuilds the receipts table
//     under receipts_v9 with the v8 layout VERBATIM and ONLY the action CHECK
//     extended by the seven new v0.8 evidence-lifecycle acts (§4 step 3 — the
//     frozen contract's receipt requirement for a newly bound policy), (d)
//     rebuilds membership_roles under membership_roles_v9 with the v3 layout
//     VERBATIM and the role CHECK extended by the four v0.8 lifecycle roles
//     (§8.1 — the identity-persistence half of the role contract: the roles the
//     policy layer authorizes become storable in membership_roles, every
//     existing role row preserved byte-for-byte, unknown tokens still
//     rejected), and (e) flips schema_version to 9 only after every step
//     succeeded;
//
//   - PutRetentionPolicy writes ONE immutable policy row per version under an
//     authenticated-principal administration gate (deny-list first, then the
//     records/tenant ownership roles, assurance ≥ standard, tenant match),
//     (tenant, requestId) idempotency on the dedicated ledger, and the
//     expected-version supersession guard (LIFECYCLE_VERSION_MISMATCH on drift);
//
//   - ResolveRetentionPolicy / EvaluatePurgeEligibility are SCOPE-FIRST reads:
//     the exact scope tuple + (jurisdiction, legislation, category) against the
//     highest version of an ENABLED policy; zero matches →
//     UNKNOWN_RETENTION_STATE (fail closed — the engine never guesses a
//     retention outcome), ambiguity → RETENTION_POLICY_AMBIGUOUS. Eligibility is
//     pure (core.EvaluateRetentionEligibility): the object's period vs the
//     deployment-declared min_period floor — NO statutory duration claim, and
//     nothing in this module ever deletes bytes.
//
//   - RECEIPT NOTE (batch 2): per design §4 step 3 the migration extends the
//     receipts action CHECK with retention_bound (the receipt for a newly bound
//     policy) and the six purge-transition acts. No batch-2 operation EMITS any
//     of them: retention_bound is object-chain-covered at binding time (design
//     §5/§6), which is the deferred binding batch; policy put/resolve/evaluate
//     are not object-chain acts. The capability (extended CHECK + closed action
//     constants in Go/TS) ships here; emission lands with object binding.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ──────────────────────────────────────────────
// v9 schema — retention policy tables (design §3.1/§4)
// ──────────────────────────────────────────────

// retentionPoliciesDDL is the immutable, versioned retention-policy table
// (§3.1 with the repository's exact scope columns): one row per version,
// supersession via supersedes_policy_id (never an in-place edit), the four
// evidence columns (jurisdiction/legislation/authority/source — a row without
// them is rejected POLICY_EVIDENCE_REQUIRED), the YYYYMM min_period floor (no
// statutory duration claim), canonical JSON arrays validated to the closed
// enums, and the exact-tuple+version uniqueness that backstops the
// expected-version guard.
const retentionPoliciesDDL = `
            CREATE TABLE retention_policies (
              id TEXT PRIMARY KEY,
              tenant_id TEXT NOT NULL,
              company_id TEXT NOT NULL DEFAULT '',
              ruc TEXT NOT NULL DEFAULT '',
              period TEXT NOT NULL DEFAULT '',
              jurisdiction TEXT NOT NULL,
              legislation TEXT NOT NULL,
              authority TEXT NOT NULL,
              source TEXT NOT NULL,
              category TEXT NOT NULL,
              min_period TEXT NOT NULL CHECK(min_period GLOB '[0-9][0-9][0-9][0-9][0-9][0-9]'),
              version INTEGER NOT NULL CHECK(version >= 1),
              supersedes_policy_id TEXT,
              dual_approval_required INTEGER NOT NULL CHECK(dual_approval_required IN (0,1)),
              dual_approver_roles TEXT NOT NULL,
              blocking_hold_kinds TEXT NOT NULL,
              enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
              created_at TEXT NOT NULL,
              created_by TEXT NOT NULL,
              UNIQUE(tenant_id, company_id, ruc, period, jurisdiction, legislation, category, version)
            );
            `

const retentionPoliciesScopeIndexDDL = `CREATE INDEX idx_retention_policies_scope
            ON retention_policies(tenant_id, company_id, ruc, period);`

const retentionPoliciesNoUpdateDDL = `
            CREATE TRIGGER retention_policies_no_update BEFORE UPDATE ON retention_policies BEGIN
              SELECT RAISE(ABORT,'IMMUTABLE_RETENTION_POLICY: a retention policy row never changes after write; supersede with a new version'); END;
            `

const retentionPoliciesNoDeleteDDL = `
            CREATE TRIGGER retention_policies_no_delete BEFORE DELETE ON retention_policies BEGIN
              SELECT RAISE(ABORT,'IMMUTABLE_RETENTION_POLICY: deletion is forbidden; supersede with a new version'); END;
            `

// retentionPolicyIdempotencyKeysDDL mirrors judgment_idempotency_keys for the
// policy put command: keyed by (tenant_id, request_id), bound to the exact
// actor that issued it, and result_json + retention_policy_id set together on
// completion (§9 — tenant-scoped idempotency).
const retentionPolicyIdempotencyKeysDDL = `
            CREATE TABLE retention_policy_idempotency_keys (
              tenant_id TEXT NOT NULL, request_id TEXT NOT NULL,
              command_hash TEXT NOT NULL, actor_binding TEXT NOT NULL,
              retention_policy_id TEXT REFERENCES retention_policies(id),
              result_json TEXT,
              created_at TEXT NOT NULL, completed_at TEXT,
              PRIMARY KEY(tenant_id,request_id),
              CHECK((retention_policy_id IS NULL) = (result_json IS NULL))
            );
            `

// receiptsV9DDL is the v9 receipts table: the v8 layout verbatim (every CHECK,
// FK, exactly-one-typed-FK constraint and the unique
// (subject_type, subject_id, action, payload_hash)) with ONLY the action CHECK
// extended by the seven new v0.8 evidence-lifecycle acts (§4 step 3). The
// subject CHECK is NOT extended — the design freezes the four subject types and
// lifecycle receipts chain on the evidence_object subject at binding time.
const receiptsV9DDL = `
            CREATE TABLE receipts_v9 (
              id INTEGER PRIMARY KEY AUTOINCREMENT,
              subject_type TEXT NOT NULL CHECK(subject_type IN ('memory','judgment','reconciliation','evidence_object')),
              subject_id TEXT NOT NULL,
              action TEXT NOT NULL CHECK(action IN
                ('memory_recorded','memory_approved','memory_rejected','memory_voided',
                 'relation_confirmed','relation_rejected','evidence_linked','memory_superseded',
                 'memory_closed','memory_reopened','reconciliation_confirmed','reconciliation_rejected',
                 'object_stored',
                 'retention_bound','purge_requested','purge_approved','purge_rejected',
                 'purge_cancelled','purge_withdrawn','purge_executed')),
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
              reconciliation_id TEXT REFERENCES reconciliations(id),
              evidence_object_id TEXT REFERENCES evidence_objects(id),
              UNIQUE(subject_type, subject_id, action, payload_hash),
              CHECK(((memory_id IS NULL) + (judgment_id IS NULL) + (reconciliation_id IS NULL) + (evidence_object_id IS NULL)) = 3),
              CHECK(COALESCE(memory_id, judgment_id, reconciliation_id, evidence_object_id) = subject_id)
            );
            `

// receiptsV9CopyDDL copies every v8 receipt row byte-preserved into the
// staging table (explicit column order — evidence_object_id copies verbatim;
// v8 rows may already carry evidence-object subjects).
const receiptsV9CopyDDL = `
            INSERT INTO receipts_v9 (id, subject_type, subject_id, action, tenant_id, company_id,
              fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
              policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
              memory_id, judgment_id, reconciliation_id, evidence_object_id)
            SELECT id, subject_type, subject_id, action, tenant_id, company_id,
              fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
              policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
              memory_id, judgment_id, reconciliation_id, evidence_object_id FROM receipts;
            `

// migrateV8ToV9 upgrades a schema_version=8 store to v9 IN ONE fail-closed
// transaction, exactly mirroring the v7→v8 pattern (design §4):
//
//	(a) validate that NONE of the new tables exists (a pre-existing
//	    retention_policies or sibling is a corruption signal; abort);
//	(b) create retention_policies (scope index + no-update/no-delete triggers)
//	    and retention_policy_idempotency_keys;
//	(c) rebuild receipts under the staging name receipts_v9: the v8 layout
//	    verbatim with ONLY the action CHECK extended by the seven new v0.8 acts;
//	    copy every row, swap the table into place, recreate the indexes and
//	    triggers;
//	(d) rebuild membership_roles under the staging name membership_roles_v9:
//	    the v3 layout verbatim with the role CHECK extended by the four v0.8
//	    lifecycle roles (design §8.1 — records_compliance_officer,
//	    tenant_records_owner, tax_responsible, operational_accountant); copy
//	    every row byte-preserved and swap the table into place. SQLite cannot
//	    alter a CHECK, so the identity-persistence half of the role contract
//	    ships here: the roles the policy layer authorizes become storable in
//	    membership_roles, and the closed set still rejects any other token;
//	(e) UPDATE schema_meta SET value = '9' ONLY after every step above
//	    succeeded; any failure rolls the whole migration back and leaves
//	    schema_version=8.
//
// No existing row is backfilled or re-hashed. Fresh schema DDL (applySchema +
// the migration chain in Open) produces the same tables/triggers.
func migrateV8ToV9(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate v8→v9: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (a) Fail closed on a pre-existing v9 table: any of the new tables already
	// existing is a corruption signal (the chain is additive and never replays).
	var existing string
	err = tx.QueryRowContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND (name = 'retention_policies' OR name = 'retention_policy_idempotency_keys')
		LIMIT 1`).Scan(&existing)
	switch {
	case err == nil:
		return fmt.Errorf("migrate v8→v9: pre-existing table %q — corruption signal, abort (additive migrations never replay)", existing)
	case errors.Is(err, sql.ErrNoRows):
		// clean — proceed
	default:
		return fmt.Errorf("migrate v8→v9: inspect existing tables: %w", err)
	}

	// (b) The retention-policy tables + their guards.
	for _, ddl := range []string{
		retentionPoliciesDDL, retentionPoliciesScopeIndexDDL,
		retentionPoliciesNoUpdateDDL, retentionPoliciesNoDeleteDDL,
		retentionPolicyIdempotencyKeysDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v8→v9: create retention policy table: %w", err)
		}
	}

	// (c) The receipts table rebuild (extended action CHECK, layout verbatim).
	if _, err := tx.ExecContext(ctx, receiptsV9DDL); err != nil {
		return fmt.Errorf("migrate v8→v9: create receipts_v9: %w", err)
	}
	if _, err := tx.ExecContext(ctx, receiptsV9CopyDDL); err != nil {
		return fmt.Errorf("migrate v8→v9: copy receipts rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dropReceiptsDDL); err != nil {
		return fmt.Errorf("migrate v8→v9: swap out v8 receipts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE receipts_v9 RENAME TO receipts`); err != nil {
		return fmt.Errorf("migrate v8→v9: rename receipts_v9: %w", err)
	}
	for _, ddl := range []string{
		receiptsSingletonIndexDDL, receiptsSubjectTimeIndexDDL, receiptsKeyTimeIndexDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v8→v9: create receipts index: %w", err)
		}
	}
	for _, ddl := range []string{receiptsNoUpdateDDL, receiptsNoDeleteDDL} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v8→v9: create receipts trigger: %w", err)
		}
	}

	// (d) The membership_roles table rebuild (role CHECK extended by the four
	// v0.8 lifecycle roles — layout verbatim, every v3 row copied
	// byte-preserved). membership_roles is a leaf table (nothing references
	// it), so the swap is safe under foreign_keys=ON inside the transaction.
	if _, err := tx.ExecContext(ctx, membershipRolesV9DDL); err != nil {
		return fmt.Errorf("migrate v8→v9: create membership_roles_v9: %w", err)
	}
	if _, err := tx.ExecContext(ctx, membershipRolesV9CopyDDL); err != nil {
		return fmt.Errorf("migrate v8→v9: copy membership_roles rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dropMembershipRolesDDL); err != nil {
		return fmt.Errorf("migrate v8→v9: swap out v8 membership_roles: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE membership_roles_v9 RENAME TO membership_roles`); err != nil {
		return fmt.Errorf("migrate v8→v9: rename membership_roles_v9: %w", err)
	}

	// (e) schema_version = 9 ONLY after the whole migration succeeded — same
	// transaction, so a failure above rolls everything back.
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = '9' WHERE key = 'schema_version'`); err != nil {
		return fmt.Errorf("migrate v8→v9: set schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate v8→v9: commit: %w", err)
	}
	committed = true
	return nil
}

// ──────────────────────────────────────────────
// Retention-policy store operations (batch 2)
// ──────────────────────────────────────────────

// retentionPolicyColumns is the fixed column projection of one policy row.
const retentionPolicyColumns = `id, tenant_id, company_id, ruc, period,
	jurisdiction, legislation, authority, source, category, min_period,
	version, COALESCE(supersedes_policy_id, ''), dual_approval_required,
	dual_approver_roles, blocking_hold_kinds, enabled, created_at, created_by`

// scanRetentionPolicy scans one retention_policies row into the core model.
func scanRetentionPolicy(row *sql.Row) (core.RetentionPolicy, error) {
	var (
		p                             core.RetentionPolicy
		supersedes                    string
		dualApprovalRequired, enabled int
		rolesJSON, holdsJSON          string
	)
	if err := row.Scan(
		&p.PolicyID, &p.TenantID, &p.CompanyID, &p.RUC, &p.Period,
		&p.Jurisdiction, &p.Legislation, &p.Authority, &p.Source, &p.Category, &p.MinPeriod,
		&p.Version, &supersedes, &dualApprovalRequired,
		&rolesJSON, &holdsJSON, &enabled, &p.CreatedAt, &p.CreatedBy,
	); err != nil {
		return core.RetentionPolicy{}, err
	}
	p.SupersedesPolicyID = supersedes
	p.DualApprovalRequired = dualApprovalRequired != 0
	p.Enabled = enabled != 0
	// The closed-enum arrays are persisted as canonical JSON TEXT — decode them
	// back; the stored bytes were validated and normalized at insert, so a
	// decode failure is an internal invariant violation and fails closed.
	if err := json.Unmarshal([]byte(rolesJSON), &p.DualApproverRoles); err != nil {
		return core.RetentionPolicy{}, fmt.Errorf("decode dual_approver_roles: %w", err)
	}
	if err := json.Unmarshal([]byte(holdsJSON), &p.BlockingHoldKinds); err != nil {
		return core.RetentionPolicy{}, fmt.Errorf("decode blocking_hold_kinds: %w", err)
	}
	return p, nil
}

// scanRetentionPolicyRows scans one retention_policies row from a Rows cursor
// into the core model (the QueryContext path of ResolveRetentionPolicy).
func scanRetentionPolicyRows(rows *sql.Rows) (core.RetentionPolicy, error) {
	var (
		p                             core.RetentionPolicy
		supersedes                    string
		dualApprovalRequired, enabled int
		rolesJSON, holdsJSON          string
	)
	if err := rows.Scan(
		&p.PolicyID, &p.TenantID, &p.CompanyID, &p.RUC, &p.Period,
		&p.Jurisdiction, &p.Legislation, &p.Authority, &p.Source, &p.Category, &p.MinPeriod,
		&p.Version, &supersedes, &dualApprovalRequired,
		&rolesJSON, &holdsJSON, &enabled, &p.CreatedAt, &p.CreatedBy,
	); err != nil {
		return core.RetentionPolicy{}, err
	}
	p.SupersedesPolicyID = supersedes
	p.DualApprovalRequired = dualApprovalRequired != 0
	p.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(rolesJSON), &p.DualApproverRoles); err != nil {
		return core.RetentionPolicy{}, fmt.Errorf("decode dual_approver_roles: %w", err)
	}
	if err := json.Unmarshal([]byte(holdsJSON), &p.BlockingHoldKinds); err != nil {
		return core.RetentionPolicy{}, fmt.Errorf("decode blocking_hold_kinds: %w", err)
	}
	return p, nil
}

// retentionPutCommandHash is the idempotency command fingerprint of a policy
// put: the canonical compact JSON of EVERY semantic field except the requestId
// (the key itself). A replay with the same requestId but a different command
// or principal is IDEMPOTENCY_CONFLICT, never a silent second write.
func retentionPutCommandHash(cmd core.PutRetentionPolicyCommand) string {
	type commandShape struct {
		Scope                core.Scope `json:"scope"`
		Jurisdiction         string     `json:"jurisdiction"`
		Legislation          string     `json:"legislation"`
		Authority            string     `json:"authority"`
		Source               string     `json:"source"`
		Category             string     `json:"category"`
		MinPeriod            string     `json:"minPeriod"`
		ExpectedVersion      int64      `json:"expectedVersion"`
		DualApprovalRequired bool       `json:"dualApprovalRequired"`
		DualApproverRoles    []string   `json:"dualApproverRoles,omitempty"`
		BlockingHoldKinds    []string   `json:"blockingHoldKinds,omitempty"`
		Enabled              bool       `json:"enabled"`
	}
	canonical, err := json.Marshal(commandShape{
		Scope: cmd.Scope, Jurisdiction: cmd.Jurisdiction, Legislation: cmd.Legislation,
		Authority: cmd.Authority, Source: cmd.Source, Category: cmd.Category,
		MinPeriod: cmd.MinPeriod, ExpectedVersion: cmd.ExpectedVersion,
		DualApprovalRequired: cmd.DualApprovalRequired,
		DualApproverRoles:    cmd.DualApproverRoles,
		BlockingHoldKinds:    cmd.BlockingHoldKinds,
		Enabled:              cmd.Enabled,
	})
	if err != nil {
		// Fixed value shapes — marshaling cannot fail; fail closed.
		panic(fmt.Sprintf("canonicalize retention put command: %v", err))
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// authorizeRetentionPolicyPut is the batch-2 administration gate for policy
// mutation (a batch-2 decision, documented in the design): the SAME frozen
// check order as the delivered evidence-lifecycle policy (§8.2) with the
// administration matrix — deny-list (operational_accountant / *admin) BEFORE
// every allow, then the records/tenant ownership roles
// (records_compliance_officer | tenant_records_owner — the §8.1 retention
// policy owners), assurance ≥ standard, and an exact tenant match. Agents and
// systems cannot reach this gate: the principal is always a verified session
// principal and the MCP surface fails closed with AUTHENTICATION_REQUIRED.
func authorizeRetentionPolicyPut(principal auth.VerifiedApprovalPrincipal, tenantID string) error {
	if principal.TenantID() != tenantID {
		return auth.New(auth.CodeTenantScopeMismatch, "principal tenant does not match the policy scope tenant")
	}
	if principal.MembershipID() == "" {
		return auth.New(auth.CodeMembershipInactive, "principal has no active membership")
	}
	if retentionRoleDenied(principal.Roles()) {
		return auth.New(auth.CodeRoleDenied, "role is deny-listed for retention policy administration")
	}
	if !retentionHasAnyRole(principal.Roles(), auth.RoleRecordsComplianceOfficer, auth.RoleTenantRecordsOwner) {
		return auth.New(auth.CodeRoleNotAuthorized, "retention policy administration requires records_compliance_officer or tenant_records_owner")
	}
	if principal.AssuranceLevel() != auth.AssuranceStandard && principal.AssuranceLevel() != auth.AssuranceStrong {
		return auth.New(auth.CodeAssuranceTooLow, "retention policy administration requires assurance ≥ standard")
	}
	return nil
}

// retentionRoleDenied reports whether the deny-list matches: the
// operational_accountant role or ANY role token containing "admin" — the same
// deny-list the delivered evidence-lifecycle policy freezes (§8.2 step 5).
func retentionRoleDenied(roles []auth.AccountingRole) bool {
	for _, r := range roles {
		if r == auth.RoleOperationalAccountant || strings.Contains(string(r), "admin") {
			return true
		}
	}
	return false
}

// retentionHasAnyRole reports whether the principal carries any of the required
// roles — explicit match, no dominance (the §8.1 convention).
func retentionHasAnyRole(roles []auth.AccountingRole, required ...auth.AccountingRole) bool {
	for _, r := range roles {
		for _, want := range required {
			if r == want {
				return true
			}
		}
	}
	return false
}

// PutRetentionPolicy writes ONE immutable retention-policy version (batch 2,
// design §3.1/§6/§9): ONE BEGIN IMMEDIATE transaction with tenant-scoped
// (tenant, requestId) idempotency, the authenticated administration gate, the
// expected-version supersession guard and the immutable row insert. A replay
// returns the stored outcome (IdempotentReplay=true) with NO new row; a reused
// requestId with a different command or principal is IDEMPOTENCY_CONFLICT; a
// version drift is LIFECYCLE_VERSION_MISMATCH. NO receipt is emitted: a policy
// put is not an object-chain act (the retention_bound receipt for a newly bound
// policy lands with object binding — design §5/§6). This operation NEVER
// deletes and makes NO statutory duration claim.
func (s *SQLiteStore) PutRetentionPolicy(ctx context.Context, cmd core.PutRetentionPolicyCommand, principal auth.VerifiedApprovalPrincipal) (core.PutRetentionPolicyResult, error) {
	// Syntax guards (defense in depth — the service validates first): an exact
	// company scope, the resolution evidence, the YYYYMM floor and the
	// idempotency key fail closed before any lock.
	if err := core.AssertValidRetentionScope(cmd.Scope); err != nil {
		return core.PutRetentionPolicyResult{}, err
	}
	if cmd.Scope.Kind != core.ScopeKindCompany {
		return core.PutRetentionPolicyResult{}, auth.New(auth.CodeInvalidTransition, "retention policies require an exact company scope (institutional is not purgeable and not policy-scoped)")
	}
	if !core.JurisdictionOK(cmd.Jurisdiction) {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("INVALID_RETENTION_JURISDICTION: jurisdiction must match ^[A-Z][A-Z0-9-]{1,15}$, got %q", cmd.Jurisdiction)
	}
	if strings.TrimSpace(cmd.Legislation) == "" || strings.TrimSpace(cmd.Authority) == "" || strings.TrimSpace(cmd.Source) == "" {
		return core.PutRetentionPolicyResult{}, auth.New(auth.CodePolicyEvidenceRequired, "a retention policy requires jurisdiction, legislation, authority and source evidence")
	}
	if strings.TrimSpace(cmd.Category) == "" {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("INVALID_RETENTION_CATEGORY: category must be non-empty")
	}
	if !core.IsValidPeriod(cmd.MinPeriod) {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("INVALID_RETENTION_MIN_PERIOD: minPeriod must be YYYYMM with month 01-12, got %q", cmd.MinPeriod)
	}
	if cmd.ExpectedVersion < 0 {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("INVALID_RETENTION_EXPECTED_VERSION: expectedVersion must be >= 0, got %d", cmd.ExpectedVersion)
	}
	if strings.TrimSpace(cmd.RequestID) == "" {
		return core.PutRetentionPolicyResult{}, auth.New(auth.CodeIdempotencyConflict, "requestId (tenant-scoped idempotency key) is required")
	}
	if err := authorizeRetentionPolicyPut(principal, cmd.Scope.OrganizationID); err != nil {
		return core.PutRetentionPolicyResult{}, err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	now := nowISO()
	commandHash := retentionPutCommandHash(cmd)
	actorBinding := principal.SubjectID()

	// 1. Tenant-scoped idempotency: one put per (tenant, requestId) on the
	// dedicated ledger. A completed put replays its stored outcome; the command
	// fingerprint and the acting principal must match exactly, else the request
	// id was reused for a different intent.
	var (
		storedPolicyID, storedResult             sql.NullString
		storedHash, storedActor, storedCreatedAt string
	)
	err = conn.QueryRowContext(ctx, `
		SELECT retention_policy_id, result_json, command_hash, actor_binding, created_at
		FROM retention_policy_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		cmd.Scope.OrganizationID, cmd.RequestID,
	).Scan(&storedPolicyID, &storedResult, &storedHash, &storedActor, &storedCreatedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedActor != actorBinding {
			return core.PutRetentionPolicyResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different command or principal")
		}
		if !storedPolicyID.Valid || !storedResult.Valid {
			return core.PutRetentionPolicyResult{}, auth.New(auth.CodeIdempotencyConflict, "request id reservation never completed")
		}
		var replayed core.RetentionPolicy
		if err := json.Unmarshal([]byte(storedResult.String), &replayed); err != nil {
			return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: decode replayed policy: %w", err)
		}
		return core.PutRetentionPolicyResult{Policy: replayed, Created: false, IdempotentReplay: true}, nil
	case errors.Is(err, sql.ErrNoRows):
		// Reserve the (tenant, requestId) key with the command fingerprint and
		// the acting principal BEFORE any guard runs: a failure later rolls the
		// reservation back with the whole transaction (the approval pattern).
		if _, err := conn.ExecContext(ctx, `
	INSERT INTO retention_policy_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, retention_policy_id, result_json, created_at, completed_at)
	VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			cmd.Scope.OrganizationID, cmd.RequestID, commandHash, actorBinding, now,
		); err != nil {
			return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: reserve idempotency key: %w", err)
		}
		// proceed — no prior put for this request id
	default:
		return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: read retention policy idempotency key: %w", err)
	}

	// 2. Expected-version supersession guard: the caller asserts the CURRENT
	// highest version of the exact chain (0 = none). Drift fails closed with
	// LIFECYCLE_VERSION_MISMATCH — the same frozen discipline as the expected
	// lifecycle hash (§9). The UNIQUE(…, version) constraint backstops the
	// derived version under BEGIN IMMEDIATE.
	var (
		currentVersion    int64
		currentPolicyID   string
		currentSupercedes string
	)
	err = conn.QueryRowContext(ctx, `
		SELECT version, id, COALESCE(supersedes_policy_id, '')
		FROM retention_policies
		WHERE tenant_id = ? AND company_id = ? AND ruc = ? AND period = ?
		  AND jurisdiction = ? AND legislation = ? AND category = ?
		ORDER BY version DESC LIMIT 1`,
		cmd.Scope.OrganizationID, cmd.Scope.CompanyID, cmd.Scope.RUC, cmd.Scope.Period,
		cmd.Jurisdiction, cmd.Legislation, cmd.Category,
	).Scan(&currentVersion, &currentPolicyID, &currentSupercedes)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		currentVersion, currentPolicyID = 0, ""
	case err != nil:
		return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: read retention policy chain head: %w", err)
	}
	if cmd.ExpectedVersion != currentVersion {
		return core.PutRetentionPolicyResult{}, auth.New(auth.CodeLifecycleVersionMismatch,
			fmt.Sprintf("expectedVersion %d does not match the current chain head %d — re-read and retry", cmd.ExpectedVersion, currentVersion))
	}

	newVersion := currentVersion + 1
	policyID, err := newUUID()
	if err != nil {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: generate policy id: %w", err)
	}

	// 3. The immutable row: canonical defaults/closed-enum normalization happen
	// here (validator), then the INSERT. A failure (e.g. the UNIQUE backstop)
	// rolls the reservation back.
	policy := core.RetentionPolicy{
		PolicyID:             policyID,
		TenantID:             cmd.Scope.OrganizationID,
		CompanyID:            cmd.Scope.CompanyID,
		RUC:                  cmd.Scope.RUC,
		Period:               cmd.Scope.Period,
		Jurisdiction:         cmd.Jurisdiction,
		Legislation:          cmd.Legislation,
		Authority:            cmd.Authority,
		Source:               cmd.Source,
		Category:             cmd.Category,
		MinPeriod:            cmd.MinPeriod,
		Version:              newVersion,
		SupersedesPolicyID:   currentPolicyID,
		DualApprovalRequired: cmd.DualApprovalRequired,
		DualApproverRoles:    cmd.DualApproverRoles,
		BlockingHoldKinds:    cmd.BlockingHoldKinds,
		Enabled:              cmd.Enabled,
		CreatedAt:            now,
		CreatedBy:            actorBinding,
	}
	if err := core.AssertValidRetentionPolicy(&policy); err != nil {
		return core.PutRetentionPolicyResult{}, err
	}
	supersedes := any(nil)
	if policy.SupersedesPolicyID != "" {
		supersedes = policy.SupersedesPolicyID
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO retention_policies (id, tenant_id, company_id, ruc, period,
			jurisdiction, legislation, authority, source, category, min_period,
			version, supersedes_policy_id, dual_approval_required,
			dual_approver_roles, blocking_hold_kinds, enabled, created_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		policy.PolicyID, policy.TenantID, policy.CompanyID, policy.RUC, policy.Period,
		policy.Jurisdiction, policy.Legislation, policy.Authority, policy.Source, policy.Category, policy.MinPeriod,
		policy.Version, supersedes, boolInt(policy.DualApprovalRequired),
		mustCanonicalJSON(policy.DualApproverRoles), mustCanonicalJSON(policy.BlockingHoldKinds),
		boolInt(policy.Enabled), policy.CreatedAt, policy.CreatedBy,
	); err != nil {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: insert retention policy: %w", err)
	}

	// 4. Complete the idempotency reservation with the created policy.
	serialized, err := json.Marshal(policy)
	if err != nil {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: encode policy result: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE retention_policy_idempotency_keys
		SET retention_policy_id = ?, result_json = ?, completed_at = ?
		WHERE tenant_id = ? AND request_id = ?`,
		policy.PolicyID, string(serialized), now, cmd.Scope.OrganizationID, cmd.RequestID,
	); err != nil {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: complete idempotency key: %w", err)
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.PutRetentionPolicyResult{}, fmt.Errorf("persistence error: commit retention policy put: %w", err)
	}
	committed = true
	return core.PutRetentionPolicyResult{Policy: policy, Created: true, IdempotentReplay: false}, nil
}

// ResolveRetentionPolicy is the SCOPE-FIRST exact resolution read (design §6):
// the exact scope tuple + (jurisdiction, legislation, category) against the
// HIGHEST version of an ENABLED policy. ok=false means no exact active policy;
// multiple enabled candidates sharing the highest version fail closed with
// RETENTION_POLICY_AMBIGUOUS (a corruption backstop under the UNIQUE — the
// engine never guesses). Reads never require a principal (scope-first, not
// authenticated).
func (s *SQLiteStore) ResolveRetentionPolicy(ctx context.Context, scope core.Scope, jurisdiction, legislation, category string) (core.RetentionPolicy, bool, error) {
	if scope.Kind == core.ScopeKindInstitutional {
		return core.RetentionPolicy{}, false, auth.New(auth.CodeNotPurgeable, "institutional (cross-company) objects are not purgeable and resolve no retention policy")
	}
	if err := core.AssertValidRetentionScope(scope); err != nil {
		return core.RetentionPolicy{}, false, err
	}
	if !core.JurisdictionOK(jurisdiction) || strings.TrimSpace(legislation) == "" || strings.TrimSpace(category) == "" {
		return core.RetentionPolicy{}, false, auth.New(auth.CodeUnknownRetentionState, "incomplete resolution evidence (jurisdiction/legislation/category)")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+retentionPolicyColumns+` FROM retention_policies
		WHERE tenant_id = ? AND company_id = ? AND ruc = ? AND period = ?
		  AND jurisdiction = ? AND legislation = ? AND category = ? AND enabled = 1
		ORDER BY version DESC`,
		scope.OrganizationID, scope.CompanyID, scope.RUC, scope.Period,
		jurisdiction, legislation, category,
	)
	if err != nil {
		return core.RetentionPolicy{}, false, fmt.Errorf("persistence error: query retention policies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var enabled []core.RetentionPolicy
	for rows.Next() {
		p, err := scanRetentionPolicyRows(rows)
		if err != nil {
			return core.RetentionPolicy{}, false, fmt.Errorf("persistence error: scan retention policy: %w", err)
		}
		enabled = append(enabled, p)
	}
	if err := rows.Err(); err != nil {
		return core.RetentionPolicy{}, false, fmt.Errorf("persistence error: iterate retention policies: %w", err)
	}

	policy, ok := core.ResolveRetentionPolicy(enabled, scope, jurisdiction, legislation, category)
	if ok {
		return policy, true, nil
	}
	if len(enabled) > 0 {
		// Enabled candidates exist but the pure resolution failed — two or more
		// share the highest version (UNIQUE-corruption backstop). Never guess.
		return core.RetentionPolicy{}, false, auth.New(auth.CodeRetentionPolicyAmbiguous, "multiple enabled retention policies share the highest version of the exact tuple")
	}
	return core.RetentionPolicy{}, false, nil
}

// EvaluatePurgeEligibility is the fail-closed eligibility read (batch 2,
// design §6): resolve the exact active policy for the input tuple; NO exact
// active policy → UNKNOWN_RETENTION_STATE (the engine never guesses a
// retention outcome); ambiguity → RETENTION_POLICY_AMBIGUOUS; otherwise the
// PURE eligibility dimension (core.EvaluateRetentionEligibility) — eligible
// when the object's period reached the deployment-declared min_period floor,
// not_due otherwise. Institutional (cross-company) objects are NOT_PURGEABLE.
// This is a READ: it never deletes, never schedules, and makes NO statutory
// duration claim. Reads are scope-first and never require a principal.
func (s *SQLiteStore) EvaluatePurgeEligibility(ctx context.Context, input core.EvaluatePurgeEligibilityInput) (core.RetentionEligibilityResult, error) {
	// Institutional (cross-company) objects are NOT_PURGEABLE — the design's
	// scope rule precedes any other validation (§10); every other scope shape
	// fails closed on syntax.
	if input.Scope.Kind == core.ScopeKindInstitutional {
		return core.RetentionEligibilityResult{}, auth.New(auth.CodeNotPurgeable, "institutional (cross-company) objects are not purgeable")
	}
	if err := core.AssertValidRetentionScope(input.Scope); err != nil {
		return core.RetentionEligibilityResult{}, err
	}
	if !core.IsValidPeriod(input.ObjectPeriod) {
		return core.RetentionEligibilityResult{}, fmt.Errorf("INVALID_OBJECT_PERIOD: objectPeriod must be YYYYMM with month 01-12, got %q", input.ObjectPeriod)
	}

	policy, matched, err := s.ResolveRetentionPolicy(ctx, input.Scope, input.Jurisdiction, input.Legislation, input.Category)
	if err != nil {
		return core.RetentionEligibilityResult{}, err
	}
	if !matched {
		return core.RetentionEligibilityResult{}, auth.New(auth.CodeUnknownRetentionState, "no exact active retention policy resolves for the scope/jurisdiction/legislation/category tuple")
	}

	eligibility := core.EvaluateRetentionEligibility(policy, input.ObjectPeriod)
	if eligibility == core.RetentionEligibilityUnknown {
		// Defense in depth: the store validated both periods, so this is an
		// internal invariant violation — fail closed, never guess.
		return core.RetentionEligibilityResult{}, auth.New(auth.CodeUnknownRetentionState, "retention state could not be evaluated")
	}
	return core.RetentionEligibilityResult{
		Eligibility:   eligibility,
		PolicyID:      policy.PolicyID,
		PolicyVersion: policy.Version,
		MinPeriod:     policy.MinPeriod,
		Jurisdiction:  policy.Jurisdiction,
		Legislation:   policy.Legislation,
		Category:      policy.Category,
		ModelVersion:  core.RetentionPolicyModelVersion,
	}, nil
}

// mustCanonicalJSON marshals a string slice as canonical compact JSON (fixed,
// sorted order — already normalized by the validator). Fixed value shapes
// cannot fail; a failure is an invariant violation and fails closed via panic.
func mustCanonicalJSON(tokens []string) string {
	b, err := json.Marshal(tokens)
	if err != nil {
		panic(fmt.Sprintf("canonical JSON: %v", err))
	}
	return string(b)
}

// boolInt maps a bool to its SQLite 0/1 integer (CHECK-constrained columns).
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
