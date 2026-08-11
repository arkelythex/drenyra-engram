// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the persisted object-level legal-hold layer of
// the v0.8 evidence lifecycle (batch 3 — docs/architecture/evidence-lifecycle-v0.8.md
// §3.2/§4/§7/§9):
//
//   - the v9→v10 migration is ONE fail-closed transaction that (a) aborts when
//     ANY new table already exists (corruption signal), (b) creates the
//     immutable evidence_holds table (OBJECT-LEVEL ONLY — object_id NOT NULL FK
//     to evidence_objects, exact scope columns, object index, no-delete trigger,
//     placed-columns immutability trigger and the one-way lift closure trigger)
//     and the tenant-scoped evidence_hold_idempotency_keys ledger, (c) rebuilds
//     the receipts table under receipts_v10 with the v9 layout VERBATIM and ONLY
//     the action CHECK extended by the two v0.8 hold acts (hold_placed,
//     hold_lifted — §4 step 3), and (d) flips schema_version to 10 only after
//     every step succeeded;
//
//   - PlaceHold writes ONE immutable hold row per placement under the
//     authenticated preservation gate (the EXTENDED evidence-lifecycle policy
//     with the place_hold action — deny-list first, then
//     records_compliance_officer | tenant_records_owner, assurance ≥ standard,
//     tenant/company match), (tenant, requestId) idempotency on the dedicated
//     ledger, and emits the hold_placed receipt atomically on the
//     evidence_object subject chain (design §5 — receipts certify gate inputs);
//
//   - LiftHold closes a placed hold ONE-WAY (lifted_at/lifted_by/lift_reason set
//     together by the guarded API; a fresh lift of an already-lifted hold is
//     ALREADY_DECIDED, never a reopen) under the SAME gate (lift_hold action)
//     and emits the hold_lifted receipt atomically;
//
//   - ActiveBlockingHolds / HoldsForObject are SCOPE-FIRST reads (design §10):
//     the caller's exact scope must equal the object's stored scope
//     (OBJECT_NOT_FOUND otherwise — cross-tenant invisibility), and the
//     blocking query returns the ACTIVE holds whose kind is in the deployment's
//     blocking set (empty set → nothing blocks — fail closed);
//
//   - EMERGENCY BYPASS: holds only PRESERVE evidence (they never reduce
//     availability), so PlaceHold/LiftHold deliberately do NOT run the
//     closed-period gate — a legal/audit hold can be placed or lifted inside a
//     closed period (design §10 documents the gate as applying to purge because
//     purge REDUCES availability; holds are the opposite).
//
//   - DEFERRED (documented, never implemented here): lifecycle snapshot hashes
//     (H1/H2 — §3.8/§9), purge request/approval/execution (§2/§11), deterministic
//     export (§12), scope-level holds (§3.2 CHECK shape) and the verification/
//     doctor layers (§13). The blocking query exists NOW so the future purge gate
//     can call it; nothing in this module deletes, purges or exports.
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
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ──────────────────────────────────────────────
// v10 schema — evidence_holds (design §3.2/§4)
// ──────────────────────────────────────────────

// evidenceHoldsDDL is the immutable, OBJECT-LEVEL hold table (§3.2 with the
// object-only decision of this batch): object_id is NOT NULL and references the
// evidence_objects row it protects (a scope-level hold — object_id NULL — is a
// documented deferral, never a silent fallback), the exact scope tuple is
// flattened on the row (repository convention), kind is the closed
// hold-kind CHECK set, and the lift columns are nullable one-way closure fields
// (lifted_at/lifted_by/lift_reason set together by the guarded store API only).
const evidenceHoldsDDL = `
                CREATE TABLE evidence_holds (
                  id TEXT PRIMARY KEY,
                  object_id TEXT NOT NULL REFERENCES evidence_objects(id),
                  tenant_id TEXT NOT NULL,
                  company_id TEXT NOT NULL,
                  ruc TEXT NOT NULL,
                  period TEXT NOT NULL DEFAULT '',
                  kind TEXT NOT NULL CHECK(kind IN ('legal','audit','dispute','fiscalization','other')),
                  reason TEXT NOT NULL,
                  owner_subject_id TEXT NOT NULL,
                  placed_at TEXT NOT NULL,
                  placed_by TEXT NOT NULL,
                  lifted_at TEXT,
                  lifted_by TEXT,
                  lift_reason TEXT
                );
                `

const evidenceHoldsObjectIndexDDL = `CREATE INDEX idx_evidence_holds_object
                ON evidence_holds(object_id);`

// evidenceHoldsImmutablePlacedDDL aborts any UPDATE touching a PLACED column:
// the placement provenance (id/object/scope/kind/reason/owner/placed_at/placed_by)
// never changes after write. The lift columns are NOT listed — they are the
// one-way closure handled by evidenceHoldsOneWayLiftDDL.
const evidenceHoldsImmutablePlacedDDL = `
                CREATE TRIGGER evidence_holds_immutable_placed BEFORE UPDATE OF
                id, object_id, tenant_id, company_id, ruc, period, kind, reason,
                owner_subject_id, placed_at, placed_by ON evidence_holds
                BEGIN
                  SELECT RAISE(ABORT,'IMMUTABLE_HOLD: placed columns never change after write; only the guarded lift closure may update the lift fields'); END;
                `

// evidenceHoldsOneWayLiftDDL enforces the ONE-WAY closure at the schema level
// (the same discipline as signing_keys.revoked_at): while the hold is placed,
// an update must set all three lift fields together (NULL → value); once lifted,
// no lift field may change (never cleared, never rewritten, never reopened).
const evidenceHoldsOneWayLiftDDL = `
                CREATE TRIGGER evidence_holds_one_way_lift BEFORE UPDATE OF
                lifted_at, lifted_by, lift_reason ON evidence_holds
                BEGIN
                  SELECT RAISE(ABORT,'IMMUTABLE_HOLD: lift fields are one-way — all three set together on lift, never cleared or rewritten')
                  WHERE (OLD.lifted_at IS NULL
                          AND NOT (NEW.lifted_at IS NOT NULL AND NEW.lifted_by IS NOT NULL AND NEW.lift_reason IS NOT NULL)
                          AND NOT (NEW.lifted_at IS NULL AND NEW.lifted_by IS NULL AND NEW.lift_reason IS NULL))
                     OR (OLD.lifted_at IS NOT NULL
                          AND NOT (NEW.lifted_at IS OLD.lifted_at AND NEW.lifted_by IS OLD.lifted_by AND NEW.lift_reason IS OLD.lift_reason));
                END;
                `

const evidenceHoldsNoDeleteDDL = `
                CREATE TRIGGER evidence_holds_no_delete BEFORE DELETE ON evidence_holds
                BEGIN
                  SELECT RAISE(ABORT,'IMMUTABLE_HOLD: deletion is forbidden; a hold is a permanent record'); END;
                `

// evidenceHoldIdempotencyKeysDDL mirrors retention_policy_idempotency_keys for
// the hold commands: keyed by (tenant_id, request_id), bound to the exact actor
// that issued it, and hold_id + result_json set together on completion (§9 —
// tenant-scoped idempotency).
const evidenceHoldIdempotencyKeysDDL = `
                CREATE TABLE evidence_hold_idempotency_keys (
                  tenant_id TEXT NOT NULL, request_id TEXT NOT NULL,
                  command_hash TEXT NOT NULL, actor_binding TEXT NOT NULL,
                  hold_id TEXT REFERENCES evidence_holds(id),
                  result_json TEXT,
                  created_at TEXT NOT NULL, completed_at TEXT,
                  PRIMARY KEY(tenant_id,request_id),
                  CHECK((hold_id IS NULL) = (result_json IS NULL))
                );
                `

// receiptsV10DDL is the v10 receipts table: the v9 layout verbatim (every CHECK,
// FK, exactly-one-typed-FK constraint and the unique
// (subject_type, subject_id, action, payload_hash)) with ONLY the action CHECK
// extended by the two v0.8 hold acts (hold_placed, hold_lifted — §4 step 3).
// The subject CHECK is NOT extended — hold receipts chain on the evidence_object
// subject (design §5).
const receiptsV10DDL = `
                CREATE TABLE receipts_v10 (
                  id INTEGER PRIMARY KEY AUTOINCREMENT,
                  subject_type TEXT NOT NULL CHECK(subject_type IN ('memory','judgment','reconciliation','evidence_object')),
                  subject_id TEXT NOT NULL,
                  action TEXT NOT NULL CHECK(action IN
                    ('memory_recorded','memory_approved','memory_rejected','memory_voided',
                     'relation_confirmed','relation_rejected','evidence_linked','memory_superseded',
                     'memory_closed','memory_reopened','reconciliation_confirmed','reconciliation_rejected',
                     'object_stored',
                     'retention_bound','purge_requested','purge_approved','purge_rejected',
                     'purge_cancelled','purge_withdrawn','purge_executed',
                     'hold_placed','hold_lifted')),
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

// receiptsV10CopyDDL copies every v9 receipt row byte-preserved into the staging
// table (explicit column order — the evidence_object_id typed FK copies verbatim).
const receiptsV10CopyDDL = `
                INSERT INTO receipts_v10 (id, subject_type, subject_id, action, tenant_id, company_id,
                  fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
                  policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
                  memory_id, judgment_id, reconciliation_id, evidence_object_id)
                SELECT id, subject_type, subject_id, action, tenant_id, company_id,
                  fiscal_period_id, payload_hash, previous_receipt_hash, principal_id, membership_id,
                  policy_version, algorithm, key_id, signature, issued_at, payload_json, receipt_hash,
                  memory_id, judgment_id, reconciliation_id, evidence_object_id FROM receipts;
                `

// migrateV9ToV10 upgrades a schema_version=9 store to v10 IN ONE fail-closed
// transaction, exactly mirroring the v8→v9 pattern (design §4):
//
//	(a) validate that NONE of the new tables exists (a pre-existing
//	    evidence_holds or sibling is a corruption signal; abort);
//	(b) create evidence_holds (object index + no-delete + placed-immutability +
//	    one-way-lift triggers) and evidence_hold_idempotency_keys;
//	(c) rebuild receipts under the staging name receipts_v10: the v9 layout
//	    verbatim with ONLY the action CHECK extended by the two v0.8 hold acts;
//	    copy every row, swap the table into place, recreate the indexes and
//	    triggers;
//	(d) UPDATE schema_meta SET value = '10' ONLY after every step above
//	    succeeded; any failure rolls the whole migration back and leaves
//	    schema_version=9.
//
// No existing row is backfilled or re-hashed. Fresh schema DDL (applySchema +
// the migration chain in Open) produces the same tables/triggers.
func migrateV9ToV10(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate v9→v10: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (a) Fail closed on a pre-existing v10 table: any of the new tables already
	// existing is a corruption signal (the chain is additive and never replays).
	var existing string
	err = tx.QueryRowContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND (name = 'evidence_holds' OR name = 'evidence_hold_idempotency_keys')
		LIMIT 1`).Scan(&existing)
	switch {
	case err == nil:
		return fmt.Errorf("migrate v9→v10: pre-existing table %q — corruption signal, abort (additive migrations never replay)", existing)
	case errors.Is(err, sql.ErrNoRows):
		// clean — proceed
	default:
		return fmt.Errorf("migrate v9→v10: inspect existing tables: %w", err)
	}

	// (b) The hold tables + their guards.
	for _, ddl := range []string{
		evidenceHoldsDDL, evidenceHoldsObjectIndexDDL,
		evidenceHoldsImmutablePlacedDDL, evidenceHoldsOneWayLiftDDL, evidenceHoldsNoDeleteDDL,
		evidenceHoldIdempotencyKeysDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v9→v10: create evidence_holds table: %w", err)
		}
	}

	// (c) The receipts table rebuild (extended action CHECK, layout verbatim).
	if _, err := tx.ExecContext(ctx, receiptsV10DDL); err != nil {
		return fmt.Errorf("migrate v9→v10: create receipts_v10: %w", err)
	}
	if _, err := tx.ExecContext(ctx, receiptsV10CopyDDL); err != nil {
		return fmt.Errorf("migrate v9→v10: copy receipts rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dropReceiptsDDL); err != nil {
		return fmt.Errorf("migrate v9→v10: swap out v9 receipts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE receipts_v10 RENAME TO receipts`); err != nil {
		return fmt.Errorf("migrate v9→v10: rename receipts_v10: %w", err)
	}
	for _, ddl := range []string{
		receiptsSingletonIndexDDL, receiptsSubjectTimeIndexDDL, receiptsKeyTimeIndexDDL,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v9→v10: create receipts index: %w", err)
		}
	}
	for _, ddl := range []string{receiptsNoUpdateDDL, receiptsNoDeleteDDL} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate v9→v10: create receipts trigger: %w", err)
		}
	}

	// (d) schema_version = 10 ONLY after the whole migration succeeded — same
	// transaction, so a failure above rolls everything back.
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = '10' WHERE key = 'schema_version'`); err != nil {
		return fmt.Errorf("migrate v9→v10: set schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate v9→v10: commit: %w", err)
	}
	committed = true
	return nil
}

// ──────────────────────────────────────────────
// Hold store operations (batch 3)
// ──────────────────────────────────────────────

// evidenceHoldColumns is the fixed column projection of one hold row.
const evidenceHoldColumns = `id, object_id, tenant_id, company_id, ruc, period,
	kind, reason, owner_subject_id, placed_at, placed_by,
	COALESCE(lifted_at, ''), COALESCE(lifted_by, ''), COALESCE(lift_reason, '')`

// scanEvidenceHold scans one evidence_holds row into the core model.
func scanEvidenceHold(sc interface{ Scan(dest ...any) error }) (core.EvidenceHold, error) {
	var h core.EvidenceHold
	if err := sc.Scan(
		&h.HoldID, &h.ObjectID, &h.TenantID, &h.CompanyID, &h.RUC, &h.Period,
		&h.Kind, &h.Reason, &h.OwnerSubjectID, &h.PlacedAt, &h.PlacedBy,
		&h.LiftedAt, &h.LiftedBy, &h.LiftReason,
	); err != nil {
		return core.EvidenceHold{}, err
	}
	return h, nil
}

// holdCommandHash is the idempotency command fingerprint of a hold command: the
// canonical compact JSON of EVERY semantic field except the requestId (the key
// itself). A replay with the same requestId but a different command or principal
// is IDEMPOTENCY_CONFLICT, never a silent second write.
func holdCommandHash(shape any) string {
	canonical, err := json.Marshal(shape)
	if err != nil {
		// Fixed value shapes — marshaling cannot fail; fail closed.
		panic(fmt.Sprintf("canonicalize hold command: %v", err))
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// authorizeHoldAct is the batch-3 preservation gate for place_hold/lift_hold:
// it delegates to the EXTENDED evidence-lifecycle policy (the pure
// evidence-lifecycle-policy/v0.8.0 with the two hold actions — design §8.1/§8.2)
// over the object's exact scope tuple. The check order is the frozen one
// (tenant → company scope → membership active → actor-kind deny → role deny-list
// → role allow → assurance ≥ standard); place_hold/lift_hold have no requester,
// no dual-approval configuration and no second approver, so the SoD and
// dual-approval checks of the policy are trivially skipped for them. Agents and
// systems cannot reach this gate: the principal is always a verified session
// principal (the MCP surface fails closed with AUTHENTICATION_REQUIRED).
func authorizeHoldAct(principal auth.VerifiedApprovalPrincipal, scope core.Scope, action authz.LifecycleAction) error {
	decision := authz.NewEvidenceLifecyclePolicy().Authorize(authz.LifecycleAuthorizationRequest{
		Action:    action,
		Principal: principal,
		ActorKind: core.ActorKindHuman,
		TenantID:  scope.OrganizationID,
		CompanyID: scope.CompanyID,
	})
	if !decision.Allowed {
		return auth.New(decision.ReasonCode, "evidence-lifecycle-policy denied "+string(action))
	}
	return nil
}

// PlaceHold places ONE object-level legal hold (batch 3, design §3.2/§7/§9): ONE
// BEGIN IMMEDIATE transaction with the locked object re-read (OBJECT_NOT_FOUND
// on an unknown content address), tenant-scoped (tenant, requestId) idempotency,
// the authenticated preservation gate (extended policy, place_hold action) and
// the immutable row insert + the hold_placed receipt on the evidence_object
// chain. A replay returns the stored outcome (IdempotentReplay=true) with NO new
// row and NO new receipt; a reused requestId with a different command or
// principal is IDEMPOTENCY_CONFLICT. EMERGENCY BYPASS: the closed-period gate is
// deliberately NOT applied — a hold only preserves evidence (design §10). This
// operation NEVER deletes, purges or exports anything.
func (s *SQLiteStore) PlaceHold(ctx context.Context, cmd core.PlaceHoldCommand, principal auth.VerifiedApprovalPrincipal) (core.PlaceHoldResult, error) {
	// Syntax guards (defense in depth — the service validates first): the object
	// id, the closed kind, the reason/owner evidence and the idempotency key fail
	// closed before any lock.
	if err := core.AssertValidPlaceHoldCommand(cmd); err != nil {
		return core.PlaceHoldResult{}, err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.PlaceHoldResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.PlaceHoldResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	// 1. Locked object re-read: the hold row inherits the EXACT scope tuple of the
	// protected evidence_object row (never a caller-declared scope). An unknown
	// content address fails closed OBJECT_NOT_FOUND (evidence_objects rows are
	// immutable, so a row read here can only vanish through corruption — the
	// migration/trigger guards make that a fail-closed database abort).
	var scope core.Scope
	err = conn.QueryRowContext(ctx, `SELECT tenant_id, company_id, ruc, period FROM evidence_objects WHERE id = ?`, cmd.ObjectID).
		Scan(&scope.OrganizationID, &scope.CompanyID, &scope.RUC, &scope.Period)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return core.PlaceHoldResult{}, fmt.Errorf("%s: %s", objectErrNotFound, cmd.ObjectID)
	case err != nil:
		return core.PlaceHoldResult{}, fmt.Errorf("persistence error: read hold object: %w", err)
	}
	scope.Kind = core.ScopeKindCompany

	now := nowISO()
	commandHash := holdCommandHash(placeHoldCommandShape{
		ObjectID: cmd.ObjectID, Kind: string(cmd.Kind), Reason: cmd.Reason, OwnerSubjectID: cmd.OwnerSubjectID,
	})
	actorBinding := principal.SubjectID()

	// 2. Tenant-scoped idempotency: one place per (tenant, requestId) on the
	// dedicated ledger. A completed place replays its stored outcome; the command
	// fingerprint and the acting principal must match exactly, else the request
	// id was reused for a different intent.
	var (
		storedHoldID, storedResult               sql.NullString
		storedHash, storedActor, storedCreatedAt string
	)
	err = conn.QueryRowContext(ctx, `
		SELECT hold_id, result_json, command_hash, actor_binding, created_at
		FROM evidence_hold_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		scope.OrganizationID, cmd.RequestID,
	).Scan(&storedHoldID, &storedResult, &storedHash, &storedActor, &storedCreatedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedActor != actorBinding {
			return core.PlaceHoldResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different command or principal")
		}
		if !storedHoldID.Valid || !storedResult.Valid {
			return core.PlaceHoldResult{}, auth.New(auth.CodeIdempotencyConflict, "request id reservation never completed")
		}
		var replayed core.EvidenceHold
		if err := json.Unmarshal([]byte(storedResult.String), &replayed); err != nil {
			return core.PlaceHoldResult{}, fmt.Errorf("persistence error: decode replayed hold: %w", err)
		}
		return core.PlaceHoldResult{Hold: replayed, Created: false, IdempotentReplay: true}, nil
	case errors.Is(err, sql.ErrNoRows):
		// Reserve the (tenant, requestId) key with the command fingerprint and
		// the acting principal BEFORE the authorization gate: a failure later
		// rolls the reservation back with the whole transaction (the approval
		// pattern).
		if _, err := conn.ExecContext(ctx, `
	INSERT INTO evidence_hold_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, hold_id, result_json, created_at, completed_at)
	VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			scope.OrganizationID, cmd.RequestID, commandHash, actorBinding, now,
		); err != nil {
			return core.PlaceHoldResult{}, fmt.Errorf("persistence error: reserve idempotency key: %w", err)
		}
		// proceed — no prior place for this request id
	default:
		return core.PlaceHoldResult{}, fmt.Errorf("persistence error: read hold idempotency key: %w", err)
	}

	// 3. The authenticated preservation gate (extended evidence-lifecycle policy,
	// place_hold action): deny-list first, then the records/tenant ownership
	// roles, assurance ≥ standard, exact tenant/company match. A denied attempt
	// rolls the reservation back with the transaction (nothing is recorded).
	if err := authorizeHoldAct(principal, scope, authz.LifecycleActionPlaceHold); err != nil {
		return core.PlaceHoldResult{}, err
	}

	// 4. The immutable row: the exact scope tuple from the object, the closed
	// kind, the placement evidence and the acting principal's provenance. The
	// lift fields stay NULL (placed). A failure (e.g. the no-delete/immutability
	// backstop) rolls the reservation back.
	holdID, err := newUUID()
	if err != nil {
		return core.PlaceHoldResult{}, fmt.Errorf("persistence error: generate hold id: %w", err)
	}
	hold := core.EvidenceHold{
		HoldID:         holdID,
		ObjectID:       cmd.ObjectID,
		TenantID:       scope.OrganizationID,
		CompanyID:      scope.CompanyID,
		RUC:            scope.RUC,
		Period:         scope.Period,
		Kind:           cmd.Kind,
		Reason:         cmd.Reason,
		OwnerSubjectID: cmd.OwnerSubjectID,
		PlacedAt:       now,
		PlacedBy:       actorBinding,
	}
	if err := core.AssertValidEvidenceHold(hold); err != nil {
		return core.PlaceHoldResult{}, err
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO evidence_holds (id, object_id, tenant_id, company_id, ruc, period,
			kind, reason, owner_subject_id, placed_at, placed_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hold.HoldID, hold.ObjectID, hold.TenantID, hold.CompanyID, hold.RUC, hold.Period,
		string(hold.Kind), hold.Reason, hold.OwnerSubjectID, hold.PlacedAt, hold.PlacedBy,
	); err != nil {
		return core.PlaceHoldResult{}, fmt.Errorf("persistence error: insert evidence hold: %w", err)
	}

	// 5. Atomic hold_placed receipt on the OBJECT's chain (design §5): the object
	// identity rides EvidenceRef, the placement reason rides Reason and the
	// acting principal rides the complete verified snapshot; payload version
	// v0.8.0 (additive — verifiers keep accepting v0.4+ payloads). A signing
	// failure rolls the row back with the receipt (no orphan hold).
	snapshot := principal.PrincipalSnapshot()
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeEvidenceObject, cmd.ObjectID, core.ReceiptActionHoldPlaced, core.ReceiptPayload{
		Version:                  core.ReceiptPayloadVersionV08,
		TenantID:                 scope.OrganizationID,
		CompanyID:                scope.CompanyID,
		FiscalPeriodID:           scope.Period,
		EvidenceRef:              cmd.ObjectID,
		Reason:                   cmd.Reason,
		PrincipalID:              snapshot.SubjectID,
		MembershipID:             snapshot.MembershipID,
		PrincipalRoles:           receiptPrincipalRoles(snapshot),
		AuthenticationMethod:     string(snapshot.AuthenticationMethod),
		AssuranceLevel:           string(snapshot.AssuranceLevel),
		PrincipalAuthenticatedAt: snapshot.AuthenticatedAt,
		PolicyVersion:            authz.LifecyclePolicyVersion,
	}, now); err != nil {
		return core.PlaceHoldResult{}, fmt.Errorf("persistence error: emit hold_placed receipt: %w", err)
	}

	// 6. Complete the idempotency reservation with the created hold.
	serialized, err := json.Marshal(hold)
	if err != nil {
		return core.PlaceHoldResult{}, fmt.Errorf("persistence error: encode hold result: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE evidence_hold_idempotency_keys
		SET hold_id = ?, result_json = ?, completed_at = ?
		WHERE tenant_id = ? AND request_id = ?`,
		hold.HoldID, string(serialized), now, scope.OrganizationID, cmd.RequestID,
	); err != nil {
		return core.PlaceHoldResult{}, fmt.Errorf("persistence error: complete idempotency key: %w", err)
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.PlaceHoldResult{}, fmt.Errorf("persistence error: commit hold place: %w", err)
	}
	committed = true
	return core.PlaceHoldResult{Hold: hold, Created: true, IdempotentReplay: false}, nil
}

// placeHoldCommandShape is the canonical idempotency fingerprint of a hold
// placement (the requestId is the key itself, never hashed).
type placeHoldCommandShape struct {
	ObjectID       string `json:"objectId"`
	Kind           string `json:"kind"`
	Reason         string `json:"reason"`
	OwnerSubjectID string `json:"ownerSubjectId"`
}

// LiftHold closes ONE placed hold ONE-WAY (batch 3, design §3.2/§7/§9): ONE
// BEGIN IMMEDIATE transaction with the locked hold re-read (HOLD_NOT_FOUND on an
// unknown hold id), tenant-scoped (tenant, requestId) idempotency, the
// authenticated preservation gate (extended policy, lift_hold action), the
// one-way closure guard (an already-lifted hold is ALREADY_DECIDED for a FRESH
// request — a replay of the successful lift returns its stored outcome) and the
// hold_lifted receipt on the object's chain. EMERGENCY BYPASS: the closed-period
// gate is deliberately NOT applied — lifting a hold only preserves evidence
// (design §10).
func (s *SQLiteStore) LiftHold(ctx context.Context, cmd core.LiftHoldCommand, principal auth.VerifiedApprovalPrincipal) (core.LiftHoldResult, error) {
	if err := core.AssertValidLiftHoldCommand(cmd); err != nil {
		return core.LiftHoldResult{}, err
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.LiftHoldResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.LiftHoldResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	// 1. Locked hold re-read: the hold row carries the exact scope tuple and the
	// protected object id (the receipt chains on that object). An unknown hold id
	// fails closed HOLD_NOT_FOUND.
	hold, err := scanEvidenceHold(conn.QueryRowContext(ctx,
		`SELECT `+evidenceHoldColumns+` FROM evidence_holds WHERE id = ?`, cmd.HoldID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.LiftHoldResult{}, fmt.Errorf("HOLD_NOT_FOUND: %s", cmd.HoldID)
		}
		return core.LiftHoldResult{}, fmt.Errorf("persistence error: read hold: %w", err)
	}
	scope := core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: hold.TenantID,
		CompanyID:      hold.CompanyID,
		RUC:            hold.RUC,
		Period:         hold.Period,
	}

	now := nowISO()
	commandHash := holdCommandHash(liftHoldCommandShape{HoldID: cmd.HoldID, Reason: cmd.Reason})
	actorBinding := principal.SubjectID()

	// 2. Tenant-scoped idempotency: one lift per (tenant, requestId) on the
	// dedicated ledger. A completed lift replays its stored (already lifted)
	// outcome; the command fingerprint and the acting principal must match
	// exactly, else the request id was reused for a different intent.
	var (
		storedHoldID, storedResult               sql.NullString
		storedHash, storedActor, storedCreatedAt string
	)
	err = conn.QueryRowContext(ctx, `
		SELECT hold_id, result_json, command_hash, actor_binding, created_at
		FROM evidence_hold_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		scope.OrganizationID, cmd.RequestID,
	).Scan(&storedHoldID, &storedResult, &storedHash, &storedActor, &storedCreatedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedActor != actorBinding {
			return core.LiftHoldResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different command or principal")
		}
		if !storedHoldID.Valid || !storedResult.Valid {
			return core.LiftHoldResult{}, auth.New(auth.CodeIdempotencyConflict, "request id reservation never completed")
		}
		var replayed core.EvidenceHold
		if err := json.Unmarshal([]byte(storedResult.String), &replayed); err != nil {
			return core.LiftHoldResult{}, fmt.Errorf("persistence error: decode replayed hold: %w", err)
		}
		return core.LiftHoldResult{Hold: replayed, Lifted: true, IdempotentReplay: true}, nil
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx, `
	INSERT INTO evidence_hold_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, hold_id, result_json, created_at, completed_at)
	VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			scope.OrganizationID, cmd.RequestID, commandHash, actorBinding, now,
		); err != nil {
			return core.LiftHoldResult{}, fmt.Errorf("persistence error: reserve idempotency key: %w", err)
		}
		// proceed — no prior lift for this request id
	default:
		return core.LiftHoldResult{}, fmt.Errorf("persistence error: read hold idempotency key: %w", err)
	}

	// 3. The authenticated preservation gate (extended policy, lift_hold action).
	if err := authorizeHoldAct(principal, scope, authz.LifecycleActionLiftHold); err != nil {
		return core.LiftHoldResult{}, err
	}

	// 4. The one-way closure guard: a FRESH lift of an already-lifted hold is
	// ALREADY_DECIDED — a lifted hold never reopens (design §7: "Lifting is a
	// one-way closure ... lifted holds remain visible forever"). The schema-level
	// one-way trigger is the defense-in-depth backstop for the guarded UPDATE.
	if hold.LiftedAt != "" {
		return core.LiftHoldResult{}, auth.New(auth.CodeAlreadyDecided,
			fmt.Sprintf("hold %s is already lifted — a lifted hold never reopens", cmd.HoldID))
	}

	// 5. The guarded one-way closure: lifted_at/lifted_by/lift_reason set
	// together by the acting principal. A concurrent double-lift is impossible
	// inside BEGIN IMMEDIATE (single writer); the WHERE lifted_at IS NULL and the
	// schema trigger are the layered backstops.
	if _, err := conn.ExecContext(ctx, `
		UPDATE evidence_holds SET lifted_at = ?, lifted_by = ?, lift_reason = ?
		WHERE id = ? AND lifted_at IS NULL`,
		now, actorBinding, cmd.Reason, cmd.HoldID,
	); err != nil {
		return core.LiftHoldResult{}, fmt.Errorf("persistence error: lift evidence hold: %w", err)
	}
	hold.LiftedAt = now
	hold.LiftedBy = actorBinding
	hold.LiftReason = cmd.Reason

	// 6. Atomic hold_lifted receipt on the OBJECT's chain (design §5): the lift
	// reason rides Reason and the acting principal rides the complete verified
	// snapshot; payload version v0.8.0. A signing failure rolls the closure back
	// with the receipt (the hold stays placed).
	snapshot := principal.PrincipalSnapshot()
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeEvidenceObject, hold.ObjectID, core.ReceiptActionHoldLifted, core.ReceiptPayload{
		Version:                  core.ReceiptPayloadVersionV08,
		TenantID:                 hold.TenantID,
		CompanyID:                hold.CompanyID,
		FiscalPeriodID:           hold.Period,
		EvidenceRef:              hold.ObjectID,
		Reason:                   cmd.Reason,
		PrincipalID:              snapshot.SubjectID,
		MembershipID:             snapshot.MembershipID,
		PrincipalRoles:           receiptPrincipalRoles(snapshot),
		AuthenticationMethod:     string(snapshot.AuthenticationMethod),
		AssuranceLevel:           string(snapshot.AssuranceLevel),
		PrincipalAuthenticatedAt: snapshot.AuthenticatedAt,
		PolicyVersion:            authz.LifecyclePolicyVersion,
	}, now); err != nil {
		return core.LiftHoldResult{}, fmt.Errorf("persistence error: emit hold_lifted receipt: %w", err)
	}

	// 7. Complete the idempotency reservation with the lifted hold.
	serialized, err := json.Marshal(hold)
	if err != nil {
		return core.LiftHoldResult{}, fmt.Errorf("persistence error: encode lift result: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE evidence_hold_idempotency_keys
		SET hold_id = ?, result_json = ?, completed_at = ?
		WHERE tenant_id = ? AND request_id = ?`,
		hold.HoldID, string(serialized), now, scope.OrganizationID, cmd.RequestID,
	); err != nil {
		return core.LiftHoldResult{}, fmt.Errorf("persistence error: complete idempotency key: %w", err)
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.LiftHoldResult{}, fmt.Errorf("persistence error: commit hold lift: %w", err)
	}
	committed = true
	return core.LiftHoldResult{Hold: hold, Lifted: true, IdempotentReplay: false}, nil
}

// liftHoldCommandShape is the canonical idempotency fingerprint of a hold lift
// (the requestId is the key itself, never hashed).
type liftHoldCommandShape struct {
	HoldID string `json:"holdId"`
	Reason string `json:"reason"`
}

// ActiveBlockingHolds is the SCOPE-FIRST active-blocking-hold query (batch 3,
// design §7): the caller's exact scope must equal the object's stored scope
// (OBJECT_NOT_FOUND otherwise — cross-tenant invisibility), and the result is
// the ACTIVE (not lifted) holds of the object whose kind is in the deployment's
// blocking set, placement order. An EMPTY blocking set blocks NOTHING (returns
// an empty list — fail closed: a caller that does not know its blocking policy
// cannot claim a block). Read-only; used by the future purge gate and exposed
// by the adapters.
func (s *SQLiteStore) ActiveBlockingHolds(ctx context.Context, objectID string, scope core.Scope, blockingKinds []string) ([]core.EvidenceHold, error) {
	obj, ok := s.evidenceObjectByIDOn(ctx, s.db, objectID)
	if !ok {
		return nil, fmt.Errorf("%s: %s", objectErrNotFound, objectID)
	}
	if !objectScopeMatches(obj, scope) {
		return nil, fmt.Errorf("%s: %s", objectErrNotFound, objectID)
	}
	if len(blockingKinds) == 0 {
		return []core.EvidenceHold{}, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(blockingKinds)), ",")
	args := make([]any, 0, len(blockingKinds)+1)
	args = append(args, objectID)
	for _, k := range blockingKinds {
		args = append(args, k)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+evidenceHoldColumns+` FROM evidence_holds
		WHERE object_id = ? AND lifted_at IS NULL AND kind IN (`+placeholders+`)
		ORDER BY placed_at, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("persistence error: query active blocking holds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var holds []core.EvidenceHold
	for rows.Next() {
		h, err := scanEvidenceHold(rows)
		if err != nil {
			return nil, fmt.Errorf("persistence error: scan active blocking hold: %w", err)
		}
		holds = append(holds, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("persistence error: iterate active blocking holds: %w", err)
	}
	if holds == nil {
		holds = []core.EvidenceHold{}
	}
	return holds, nil
}

// HoldsForObject returns EVERY hold record of the object (placed AND lifted),
// placement order — SCOPE-FIRST exactly like ActiveBlockingHolds
// (OBJECT_NOT_FOUND when the caller's exact scope differs from the stored
// scope). Read-only audit surface: lifted holds remain visible forever.
func (s *SQLiteStore) HoldsForObject(ctx context.Context, objectID string, scope core.Scope) ([]core.EvidenceHold, error) {
	obj, ok := s.evidenceObjectByIDOn(ctx, s.db, objectID)
	if !ok {
		return nil, fmt.Errorf("%s: %s", objectErrNotFound, objectID)
	}
	if !objectScopeMatches(obj, scope) {
		return nil, fmt.Errorf("%s: %s", objectErrNotFound, objectID)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+evidenceHoldColumns+` FROM evidence_holds
		WHERE object_id = ? ORDER BY placed_at, id`, objectID)
	if err != nil {
		return nil, fmt.Errorf("persistence error: query holds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var holds []core.EvidenceHold
	for rows.Next() {
		h, err := scanEvidenceHold(rows)
		if err != nil {
			return nil, fmt.Errorf("persistence error: scan hold: %w", err)
		}
		holds = append(holds, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("persistence error: iterate holds: %w", err)
	}
	if holds == nil {
		holds = []core.EvidenceHold{}
	}
	return holds, nil
}
