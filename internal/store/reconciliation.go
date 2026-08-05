// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the atomic persistence half
// of the first-class reconciliation machine (v0.5.0 —
// docs/architecture/close-intelligence-v0.5.md §3): ProposeReconciliation,
// ConfirmReconciliation, RejectReconciliation and WithdrawReconciliation as ONE
// BEGIN IMMEDIATE transaction per operation, the frozen error codes, the
// RECONCILIATION_HASH_MISMATCH details carrier, idempotency replay/conflict,
// the confirm-time reconciles relation projection, and the correction
// supersession routing (ReconciliationSuccessorOf).
//
// Endpoints must exist, differ, and share tenant/company; they either share a
// non-empty period (fiscal_period_id set) or explicitly declare cross-period
// reconciliation (fiscal_period_id NULL), matching judgment convention.
// variance_cents is ENGINE-derived (left - right) and schema-enforced.
// Confirmation atomically projects one observation relation
// leftMemoryId --reconciles--> rightMemoryId; rejected/withdrawn proposals
// project none. Agents can never confirm/reject: the signatures REQUIRE a
// verified principal (compile-level contract — an agent Source is provenance
// only).
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// reconciliationColumns is the column list of the v6 reconciliations table
// (design §3.2), mirrored field-for-field by scanReconciliation.
const reconciliationColumns = `id, tenant_id, company_id, fiscal_period_id, left_memory_id, right_memory_id,
	method, currency, left_amount_cents, right_amount_cents, variance_cents, tolerance_cents,
	status, proposer_system, proposer_actor_id, proposer_actor_kind, proposer_session, proposal_reason,
	resolution, policy_version, adjudicator_subject_id, adjudicator_membership_id, adjudicator_roles_json,
	authentication_method, assurance_level, principal_authenticated_at, predecessor_id, supersedes_id,
	proposed_at, decided_at`

// scanReconciliation decodes a reconciliations row into the core entity. The
// proposer is reconstructed from the four stored provenance columns ONLY
// (system, actorId, actorKind, session) — the canonical identity the design
// compares — so the decoded entity is byte-identical to the entity the store
// returns from its own constructed results.
func scanReconciliation(rs rowScanner) (core.Reconciliation, error) {
	var (
		id, tenantID, companyID, leftID, rightID, method, currency, status  string
		proposerSystem, proposerActorID, proposerActorKind, proposerSession string
		proposalReason, proposedAt                                          string
		leftAmountCents, rightAmountCents, varianceCents, toleranceCents    int64
	)
	var (
		fiscalPeriodID, resolution, policyVersion                  sql.NullString
		adjSubject, adjMembership, adjRoles, authMethod, assurance sql.NullString
		authAt, predecessorID, supersedesID, decidedAt             sql.NullString
	)
	if err := rs.Scan(
		&id, &tenantID, &companyID, &fiscalPeriodID, &leftID, &rightID, &method, &currency,
		&leftAmountCents, &rightAmountCents, &varianceCents, &toleranceCents, &status,
		&proposerSystem, &proposerActorID, &proposerActorKind, &proposerSession, &proposalReason,
		&resolution, &policyVersion, &adjSubject, &adjMembership, &adjRoles, &authMethod, &assurance, &authAt,
		&predecessorID, &supersedesID, &proposedAt, &decidedAt,
	); err != nil {
		return core.Reconciliation{}, err
	}
	r := core.Reconciliation{
		ID:               id,
		TenantID:         tenantID,
		CompanyID:        companyID,
		FiscalPeriodID:   fiscalPeriodID.String,
		LeftMemoryID:     leftID,
		RightMemoryID:    rightID,
		Method:           method,
		Currency:         currency,
		LeftAmountCents:  leftAmountCents,
		RightAmountCents: rightAmountCents,
		VarianceCents:    varianceCents,
		ToleranceCents:   toleranceCents,
		Status:           core.ReconciliationStatus(status),
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
		DecidedAt:      decidedAt.String,
	}
	if adjSubject.Valid && adjSubject.String != "" {
		roles := make([]auth.AccountingRole, 0)
		_ = json.Unmarshal([]byte(adjRoles.String), &roles)
		r.Adjudicator = &auth.PrincipalSnapshot{
			SubjectID:            adjSubject.String,
			MembershipID:         adjMembership.String,
			Roles:                roles,
			AuthenticationMethod: auth.AuthenticationMethod(authMethod.String),
			AssuranceLevel:       auth.AssuranceLevel(assurance.String),
			AuthenticatedAt:      authAt.String,
		}
	}
	return r, nil
}

// readReconciliation reads one reconciliation row THROUGH the given connection,
// so every race-sensitive read of an adjudication stays inside its own
// transaction.
func (s *SQLiteStore) readReconciliation(ctx context.Context, q Queryer, id string) (core.Reconciliation, bool) {
	row := q.QueryRowContext(ctx, `SELECT `+reconciliationColumns+` FROM reconciliations WHERE id = ?`, id)
	r, err := scanReconciliation(row)
	if err != nil {
		return core.Reconciliation{}, false
	}
	return r, true
}

// storedReconciliationResult is the store-private JSON shape stored in
// reconciliation_idempotency_keys.result_json for a completed decision
// (confirm / reject / withdraw): the resulting reconciliation plus the
// immutable event id. The IdempotentReplay marker is set at DECODE time, never
// persisted, so the stored bytes stay the exact original outcome.
type storedReconciliationResult struct {
	ReconciliationID      string              `json:"reconciliationId"`
	Reconciliation        core.Reconciliation `json:"reconciliation"`
	ReconciliationEventID string              `json:"reconciliationEventId"`
}

// proposeReconciliationCommandHash is the canonical idempotency command hash of
// a proposal: SHA-256 hex of leftMemoryId NUL rightMemoryId NUL method NUL
// currency NUL leftAmountCents NUL rightAmountCents NUL toleranceCents NUL
// reason NUL predecessorId (amounts canonicalized as decimal int64). RequestID
// is the KEY, not part of the payload.
func proposeReconciliationCommandHash(cmd core.ProposeReconciliationCommand) string {
	canonical := cmd.LeftMemoryID + "\x00" + cmd.RightMemoryID + "\x00" + cmd.Method + "\x00" + cmd.Currency + "\x00" +
		strconv.FormatInt(cmd.LeftAmountCents, 10) + "\x00" + strconv.FormatInt(cmd.RightAmountCents, 10) + "\x00" +
		strconv.FormatInt(cmd.ToleranceCents, 10) + "\x00" + cmd.Reason + "\x00" + cmd.PredecessorID
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// decideReconciliationCommandHash is the canonical idempotency command hash of
// a confirm/reject decision: reconciliationId NUL lowercase(expectedHash) NUL
// resolution — mirroring decideJudgmentCommandHash.
func decideReconciliationCommandHash(reconciliationID, expectedHash, resolution string) string {
	canonical := reconciliationID + "\x00" + strings.ToLower(expectedHash) + "\x00" + resolution
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// withdrawReconciliationCommandHash is the canonical idempotency command hash
// of a withdrawal: SHA-256 hex of the reconciliation id (a withdrawal has no
// payload).
func withdrawReconciliationCommandHash(reconciliationID string) string {
	sum := sha256.Sum256([]byte(reconciliationID))
	return hex.EncodeToString(sum[:])
}

// ProposeReconciliation atomically creates an OPEN proposal over two
// observations — the proposal half of the reconciliation machine (design §3.2).
// The caller Source is provenance ONLY (agent|system); it never authorizes.
// Tenant/company are DERIVED from the observations' scopes, never from caller
// claims; the (tenant, requestId) reservation makes a same-request retry replay
// the original proposal while a different payload returns IDEMPOTENCY_CONFLICT
// and a second open proposal for the tuple returns RECONCILIATION_CONFLICT (the
// partial unique index is the arbiter). A proposal writes NO reconciliation
// event: the frozen events CHECK admits only confirm|reject|withdraw|supersede,
// so the reservation completes with result/event NULL and a replay re-derives
// the proposal from the (tenant, company, left, right, method) tuple. The
// period write gate applies when EITHER endpoint is in a CLOSED exact company
// period.
func (s *SQLiteStore) ProposeReconciliation(ctx context.Context, cmd core.ProposeReconciliationCommand, caller core.Source) (core.ProposeReconciliationResult, error) {
	// Syntax guards (defense in depth — the service validates first): an
	// incomplete command, an invalid proposer or a negative tolerance fails
	// closed before any lock.
	if strings.TrimSpace(cmd.LeftMemoryID) == "" || strings.TrimSpace(cmd.RightMemoryID) == "" ||
		strings.TrimSpace(cmd.Method) == "" || strings.TrimSpace(cmd.Currency) == "" ||
		strings.TrimSpace(cmd.Reason) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "proposal command is incomplete (leftMemoryId, rightMemoryId, method, currency, reason and requestId are required)")
	}
	if !core.CanPropose(caller) {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeProposalUnauthorized, "only agents and systems may propose reconciliations (provenance, never authority)")
	}
	if cmd.LeftMemoryID == cmd.RightMemoryID {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "a reconciliation requires two DISTINCT observations (leftMemoryId and rightMemoryId must differ)")
	}
	if cmd.ToleranceCents < 0 {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "toleranceCents must be non-negative")
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.ProposeReconciliationResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// BEGIN IMMEDIATE is the write intent: the reserved writer lock is taken
	// before any race-sensitive read (design §3.2 — one open proposal per tuple).
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.ProposeReconciliationResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	// defer_foreign_keys postpones ALL FK enforcement of this transaction to
	// COMMIT: the idempotency reservation references the reconciliation row that
	// is created later in the same transaction (and the proposed-predecessor
	// supersession crosses rows in the same way). At COMMIT every FK is
	// re-checked, so no dangling reference can survive.
	if _, err := conn.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return core.ProposeReconciliationResult{}, fmt.Errorf("persistence error: defer foreign keys: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	now := nowISO()
	commandHash := proposeReconciliationCommandHash(cmd)
	binding := proposerBinding(caller)

	// 1. Both observations must exist; tenant/company/period are derived from
	// their scopes (the pair must be coherent: same tenant, same company;
	// cross-period pairs are allowed and keep fiscal_period_id NULL).
	left, okLeft := s.readMemoryWithLinks(ctx, conn, cmd.LeftMemoryID)
	right, okRight := s.readMemoryWithLinks(ctx, conn, cmd.RightMemoryID)
	if !okLeft || !okRight {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeMemoryNotFound, "a reconciliation requires two existing observations")
	}
	if left.Scope.Kind != core.ScopeKindCompany || right.Scope.Kind != core.ScopeKindCompany {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeCompanyScopeDenied, "institutional observations have no company to reconcile")
	}
	if left.Scope.OrganizationID != right.Scope.OrganizationID {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeTenantScopeMismatch, "reconciliation observations must belong to the same tenant")
	}
	if left.Scope.CompanyID != right.Scope.CompanyID {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeCompanyScopeDenied, "reconciliation observations must belong to the same company")
	}
	tenantID, companyID := left.Scope.OrganizationID, left.Scope.CompanyID
	fiscalPeriod := ""
	if left.Scope.Period != "" && left.Scope.Period == right.Scope.Period {
		fiscalPeriod = left.Scope.Period
	}

	// Close write gate (v0.5.0): a reconciliation proposal touching EITHER
	// endpoint observation in a CLOSED exact company period fails with
	// PERIOD_CLOSED — both endpoint scopes are checked (cross-period pairs gate
	// each endpoint independently). The check runs inside the BEGIN IMMEDIATE
	// transaction, before the first mutation, so a concurrent close cannot race
	// it.
	if err := s.assertPeriodWritable(ctx, conn, left.Scope, "propose reconciliation"); err != nil {
		return core.ProposeReconciliationResult{}, err
	}
	if err := s.assertPeriodWritable(ctx, conn, right.Scope, "propose reconciliation"); err != nil {
		return core.ProposeReconciliationResult{}, err
	}

	// The reconciliation id is generated BEFORE the idempotency reservation so
	// the reservation can record the exact reconciliation it will create (the
	// replay then returns this original row, never a newer proposal of the same
	// tuple).
	id, err := newUUID()
	if err != nil {
		return core.ProposeReconciliationResult{}, fmt.Errorf("persistence error: generate reconciliation id: %w", err)
	}

	// 2. Idempotency by (tenant, requestId), bound to the exact proposer
	// identity. A completed reservation replays the original proposal; an
	// incomplete one (an interrupted attempt) is reused and the open-tuple
	// index decides below.
	var storedHash, storedBinding string
	var storedResultJSON, storedReconciliationID, completedAt sql.NullString
	err = conn.QueryRowContext(ctx, `
		SELECT command_hash, actor_binding, reconciliation_id, result_json, completed_at
		FROM reconciliation_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		tenantID, cmd.RequestID,
	).Scan(&storedHash, &storedBinding, &storedReconciliationID, &storedResultJSON, &completedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedBinding != binding {
			return core.ProposeReconciliationResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different proposal or proposer")
		}
		if completedAt.Valid {
			// Replay: return the ORIGINAL proposal the reservation created. The
			// reservation stores the reconciliation id it produced, so a
			// same-request retry always replays that exact row — never a newer
			// proposal of the same tuple. The tuple re-derivation remains only
			// as a defensive fallback for reservations created before
			// reconciliation_id was recorded.
			if storedReconciliationID.Valid {
				if r, ok := s.readReconciliation(ctx, conn, storedReconciliationID.String); ok {
					return core.ProposeReconciliationResult{ReconciliationID: r.ID, Reconciliation: r, IdempotentReplay: true}, nil
				}
			}
			r, ok := s.readOpenReconciliation(ctx, conn, tenantID, companyID, cmd.LeftMemoryID, cmd.RightMemoryID, cmd.Method)
			if !ok {
				r, ok = s.readLatestTupleReconciliation(ctx, conn, tenantID, companyID, cmd.LeftMemoryID, cmd.RightMemoryID, cmd.Method)
			}
			if !ok {
				return core.ProposeReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "proposal reservation completed but no reconciliation row found for the tuple")
			}
			return core.ProposeReconciliationResult{ReconciliationID: r.ID, Reconciliation: r, IdempotentReplay: true}, nil
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO reconciliation_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, reconciliation_id, result_json, reconciliation_event_id, created_at, completed_at)
			VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			tenantID, cmd.RequestID, commandHash, binding, id, now,
		); err != nil {
			return core.ProposeReconciliationResult{}, fmt.Errorf("persistence error: reserve reconciliation idempotency key: %w", err)
		}
	default:
		return core.ProposeReconciliationResult{}, fmt.Errorf("persistence error: read reconciliation idempotency key: %w", err)
	}

	// 4. Predecessor (design §3.2): a predecessor must concern the same pair and
	// method; a CONFIRMED predecessor stays current until the correction
	// confirms (supersession is atomic with confirmation); a PROPOSED
	// predecessor may be superseded IMMEDIATELY but only by the same proposer
	// identity (which frees the open tuple for the correction); terminal
	// predecessors never re-open.
	supersededPred := ""
	if cmd.PredecessorID != "" {
		pred, ok := s.readReconciliation(ctx, conn, cmd.PredecessorID)
		if !ok {
			return core.ProposeReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "predecessor reconciliation not found: "+cmd.PredecessorID)
		}
		if pred.LeftMemoryID != cmd.LeftMemoryID || pred.RightMemoryID != cmd.RightMemoryID || pred.Method != cmd.Method {
			return core.ProposeReconciliationResult{}, auth.New(auth.CodeReconciliationConflict, "a predecessor must concern the same pair and method")
		}
		switch pred.Status {
		case core.ReconciliationConfirmed:
			// Deferred to confirm time.
		case core.ReconciliationProposed:
			if proposerBinding(pred.Proposer) != binding {
				return core.ProposeReconciliationResult{}, auth.New(auth.CodeProposalUnauthorized, "a proposed reconciliation may only be corrected by its own proposer")
			}
			// The supersede UPDATE sets predecessor.supersedes_id to the new
			// reconciliation row created in this transaction; FK enforcement is
			// already deferred to COMMIT (see above), so the cross-row ordering
			// is safe.
			if _, err := s.supersedeProposedReconciliationPredecessor(ctx, conn, pred, id, cmd.RequestID, now); err != nil {
				return core.ProposeReconciliationResult{}, err
			}
			supersededPred = pred.ID
		default:
			return core.ProposeReconciliationResult{}, auth.New(auth.CodeInvalidReconciliationTransition, fmt.Sprintf("a %q predecessor cannot be corrected", pred.Status))
		}
	}

	// 5. Insert the proposed row. The partial unique index on
	// (tenant, company, left, right, method) WHERE status='proposed' rejects a
	// second open proposal for the tuple → RECONCILIATION_CONFLICT (another
	// request never silently deduplicates authorship).
	var predecessorCol any
	if cmd.PredecessorID != "" {
		predecessorCol = cmd.PredecessorID
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO reconciliations (
			id, tenant_id, company_id, fiscal_period_id, left_memory_id, right_memory_id,
			method, currency, left_amount_cents, right_amount_cents, variance_cents, tolerance_cents, status,
			proposer_system, proposer_actor_id, proposer_actor_kind, proposer_session, proposal_reason,
			resolution, policy_version, adjudicator_subject_id, adjudicator_membership_id, adjudicator_roles_json,
			authentication_method, assurance_level, principal_authenticated_at,
			predecessor_id, supersedes_id, proposed_at, decided_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'proposed', ?, ?, ?, ?, ?, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, ?, NULL, ?, NULL)`,
		id, tenantID, companyID, nullableOrNil(fiscalPeriod), cmd.LeftMemoryID, cmd.RightMemoryID,
		cmd.Method, cmd.Currency, cmd.LeftAmountCents, cmd.RightAmountCents, cmd.LeftAmountCents-cmd.RightAmountCents, cmd.ToleranceCents,
		caller.System, caller.ActorID, string(caller.ActorKind), caller.Session, cmd.Reason,
		predecessorCol, now, now,
	); err != nil {
		if isOpenTupleConflict(err) {
			return core.ProposeReconciliationResult{}, auth.New(auth.CodeReconciliationConflict, "an open reconciliation already exists for this observation pair and method")
		}
		return core.ProposeReconciliationResult{}, fmt.Errorf("persistence error: insert reconciliation: %w", err)
	}

	// 6. The supersedes routing row is inserted only now that BOTH
	// reconciliation rows exist (FK order): predecessor → successor with
	// relation frozen to 'supersedes' (reconciliation ids never enter the
	// observation relations table).
	if supersededPred != "" {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO reconciliation_relations (from_reconciliation_id, to_reconciliation_id, relation, actor, timestamp)
			VALUES (?, ?, 'supersedes', ?, ?)`,
			supersededPred, id, binding, now,
		); err != nil {
			return core.ProposeReconciliationResult{}, fmt.Errorf("persistence error: insert supersedes relation: %w", err)
		}
	}

	// 7. Complete the reservation. A proposal has NO event (the events CHECK
	// freezes actions to confirm|reject|withdraw|supersede), so the CHECK
	// (reconciliation_event_id IS NULL) = (result_json IS NULL) keeps both NULL
	// and the completion time is the only change; reconciliation_id records the
	// created proposal so a same-request replay returns THAT exact row.
	if _, err := conn.ExecContext(ctx, `
		UPDATE reconciliation_idempotency_keys SET reconciliation_id = ?, completed_at = ? WHERE tenant_id = ? AND request_id = ?`,
		id, now, tenantID, cmd.RequestID,
	); err != nil {
		return core.ProposeReconciliationResult{}, fmt.Errorf("persistence error: complete reconciliation idempotency key: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.ProposeReconciliationResult{}, fmt.Errorf("persistence error: commit proposal: %w", err)
	}
	committed = true

	reconciliation := core.Reconciliation{
		ID:               id,
		TenantID:         tenantID,
		CompanyID:        companyID,
		FiscalPeriodID:   fiscalPeriod,
		LeftMemoryID:     cmd.LeftMemoryID,
		RightMemoryID:    cmd.RightMemoryID,
		Method:           cmd.Method,
		Currency:         cmd.Currency,
		LeftAmountCents:  cmd.LeftAmountCents,
		RightAmountCents: cmd.RightAmountCents,
		VarianceCents:    cmd.LeftAmountCents - cmd.RightAmountCents,
		ToleranceCents:   cmd.ToleranceCents,
		Status:           core.ReconciliationProposed,
		Proposer: core.Source{
			System:    caller.System,
			ActorID:   caller.ActorID,
			ActorKind: caller.ActorKind,
			Session:   caller.Session,
		},
		ProposalReason: cmd.Reason,
		PredecessorID:  cmd.PredecessorID,
		ProposedAt:     now,
	}
	return core.ProposeReconciliationResult{ReconciliationID: id, Reconciliation: reconciliation, IdempotentReplay: false}, nil
}

// supersedeProposedReconciliationPredecessor performs design §3.2's immediate
// same-proposer supersession: the OLD OPEN proposal (by the same identity) is
// closed as superseded and routed to the correction, so the open-tuple index
// accepts it. The immutable 'supersede' event records the closed state; the
// caller inserts the reconciliation_relations routing row AFTER the successor
// reconciliation exists (FK order). The pure core.SupersedeReconciliation
// helper only covers confirmed→superseded, so the proposed predecessor's
// routing fields are set directly here (proposed rows are the machine's work
// area — the trigger allows it).
func (s *SQLiteStore) supersedeProposedReconciliationPredecessor(ctx context.Context, q Queryer, pred core.Reconciliation, successorID, requestID, now string) (string, error) {
	superseded := pred
	superseded.Status = core.ReconciliationSuperseded
	superseded.SupersedesID = successorID
	superseded.DecidedAt = now // schema CHECK: every non-proposed row carries decided_at
	res, err := q.ExecContext(ctx, `
		UPDATE reconciliations SET status = 'superseded', supersedes_id = ?, decided_at = ?
		WHERE id = ? AND status = 'proposed'`,
		successorID, now, pred.ID,
	)
	if err != nil {
		return "", fmt.Errorf("persistence error: supersede proposed predecessor: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("persistence error: supersede proposed predecessor rows affected: %w", err)
	}
	if affected != 1 {
		return "", auth.New(auth.CodeInvalidReconciliationTransition, "guarded predecessor supersession did not match exactly one proposed row")
	}
	eventID, err := newUUID()
	if err != nil {
		return "", fmt.Errorf("persistence error: generate predecessor event id: %w", err)
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO reconciliation_events (
			id, reconciliation_id, request_id, action, from_status, to_status, reconciliation_hash,
			principal_snapshot_json, policy_version, reason, created_at
		) VALUES (?, ?, ?, 'supersede', 'proposed', 'superseded', ?, NULL, NULL, '', ?)`,
		eventID, pred.ID, requestID, core.ComputeReconciliationHash(superseded), now,
	); err != nil {
		return "", fmt.Errorf("persistence error: insert predecessor supersede event: %w", err)
	}
	return eventID, nil
}

// readOpenReconciliation returns the OPEN proposal for the tuple — the partial
// unique index guarantees at most one (design §3.2).
func (s *SQLiteStore) readOpenReconciliation(ctx context.Context, q Queryer, tenantID, companyID, leftID, rightID, method string) (core.Reconciliation, bool) {
	row := q.QueryRowContext(ctx, `SELECT `+reconciliationColumns+` FROM reconciliations
		WHERE tenant_id = ? AND company_id = ? AND left_memory_id = ? AND right_memory_id = ? AND method = ? AND status = 'proposed'`,
		tenantID, companyID, leftID, rightID, method)
	r, err := scanReconciliation(row)
	if err != nil {
		return core.Reconciliation{}, false
	}
	return r, true
}

// readLatestTupleReconciliation returns the most recent reconciliation row of
// the tuple (any status) — the replay fallback when the replayed proposal was
// already decided before the retry arrived.
func (s *SQLiteStore) readLatestTupleReconciliation(ctx context.Context, q Queryer, tenantID, companyID, leftID, rightID, method string) (core.Reconciliation, bool) {
	row := q.QueryRowContext(ctx, `SELECT `+reconciliationColumns+` FROM reconciliations
		WHERE tenant_id = ? AND company_id = ? AND left_memory_id = ? AND right_memory_id = ? AND method = ?
		ORDER BY rowid DESC LIMIT 1`,
		tenantID, companyID, leftID, rightID, method)
	r, err := scanReconciliation(row)
	if err != nil {
		return core.Reconciliation{}, false
	}
	return r, true
}

// reconciliationDecisionParams carries the shared confirm/reject decision
// inputs. confirm: Resolution is the professional resolution and both
// RelationProjection and SupersedePredecessor are true. reject: Resolution is
// the human reason and both flags stay false (terminal — no relation
// projection, no supersession).
type reconciliationDecisionParams struct {
	ReconciliationID           string
	Resolution                 string
	ExpectedReconciliationHash string
	RequestID                  string
	Action                     string // 'confirm' | 'reject' (frozen events CHECK)
	ToStatus                   core.ReconciliationStatus
	RelationProjection         bool // confirm only: reconciles observation relation
	SupersedePredecessor       bool // confirm only: atomic supersession of a confirmed predecessor
}

// adjudicateReconciliation is THE authenticated decision transaction (design
// §3.2) — shared by confirm and reject. One BEGIN IMMEDIATE on a dedicated
// connection: idempotency resolution → locked re-read of the reconciliation +
// observations → status gate → fresh hash vs expected → pure frozen policy →
// guarded status flip → immutable decision event (+ reconciles relation
// projection / predecessor supersession for confirm) → completed reservation →
// commit. Two concurrent confirms serialize at BEGIN IMMEDIATE: exactly one
// flips the row; the loser reads the committed status and returns
// INVALID_RECONCILIATION_TRANSITION (or a replay when it carries the winner's
// identical request id).
func (s *SQLiteStore) adjudicateReconciliation(ctx context.Context, p reconciliationDecisionParams, principal auth.VerifiedApprovalPrincipal, policy authz.ReconciliationAuthorizationPolicy) (core.Reconciliation, string, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	now := nowISO()
	commandHash := decideReconciliationCommandHash(p.ReconciliationID, p.ExpectedReconciliationHash, p.Resolution)
	binding := principal.SubjectID()

	// 1. Idempotency by (principal tenant, requestId), bound to the exact
	// adjudicator subject: a different command or principal returns
	// IDEMPOTENCY_CONFLICT; a completed match replays the stored result.
	var storedHash, storedBinding string
	var storedResultJSON, completedAt sql.NullString
	err = conn.QueryRowContext(ctx, `
		SELECT command_hash, actor_binding, result_json, completed_at
		FROM reconciliation_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		principal.TenantID(), p.RequestID,
	).Scan(&storedHash, &storedBinding, &storedResultJSON, &completedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedBinding != binding {
			return core.Reconciliation{}, "", false, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different decision or adjudicator")
		}
		if completedAt.Valid {
			var replay storedReconciliationResult
			if err := json.Unmarshal([]byte(storedResultJSON.String), &replay); err != nil {
				return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: decode replayed reconciliation result: %w", err)
			}
			return replay.Reconciliation, replay.ReconciliationEventID, true, nil
		}
		// Incomplete reservation (an interrupted attempt that never committed):
		// reuse it — the status gate below decides the outcome.
	case errors.Is(err, sql.ErrNoRows):
		// 2. Reserve.
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO reconciliation_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, result_json, reconciliation_event_id, created_at, completed_at)
			VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			principal.TenantID(), p.RequestID, commandHash, binding, now,
		); err != nil {
			return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: reserve reconciliation idempotency key: %w", err)
		}
	default:
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: read reconciliation idempotency key: %w", err)
	}

	// 3. Read the reconciliation plus both observations on the SAME connection
	// (the locked observations feed the decision receipt's left/right envelope
	// hashes — recomputed fresh from the locked rows, never from the stored
	// cache).
	reconciliation, ok := s.readReconciliation(ctx, conn, p.ReconciliationID)
	if !ok {
		return core.Reconciliation{}, "", false, auth.New(auth.CodeReconciliationNotFound, "reconciliation not found: "+p.ReconciliationID)
	}
	var leftObs, rightObs core.AccountingMemory
	for _, obsID := range []string{reconciliation.LeftMemoryID, reconciliation.RightMemoryID} {
		obs, ok := s.readMemoryWithLinks(ctx, conn, obsID)
		if !ok {
			return core.Reconciliation{}, "", false, auth.New(auth.CodeMemoryNotFound, "reconciliation observation not found: "+obsID)
		}
		if obsID == reconciliation.LeftMemoryID {
			leftObs = obs
		} else {
			rightObs = obs
		}
	}

	// Close write gate (v0.5.0): a confirm/reject decision whose reconciliation
	// touches EITHER endpoint observation in a CLOSED exact company period fails
	// with PERIOD_CLOSED (both endpoint scopes are checked). The check runs
	// inside the BEGIN IMMEDIATE transaction before the guarded UPDATE, so a
	// concurrent close approval cannot race it.
	if err := s.assertPeriodWritable(ctx, conn, leftObs.Scope, p.Action+" reconciliation"); err != nil {
		return core.Reconciliation{}, "", false, err
	}
	if err := s.assertPeriodWritable(ctx, conn, rightObs.Scope, p.Action+" reconciliation"); err != nil {
		return core.Reconciliation{}, "", false, err
	}

	// 4. Status gate: only a proposed reconciliation may be decided. A
	// concurrent loser lands here after the winner commits and sees the new
	// status.
	if reconciliation.Status != core.ReconciliationProposed {
		return core.Reconciliation{}, "", false, auth.New(auth.CodeInvalidReconciliationTransition, fmt.Sprintf("%s is not legal from status %q", p.Action, reconciliation.Status))
	}

	// 5. The reviewed hash is recomputed FRESH from the locked row and compared
	// against what the adjudicator actually reviewed; a mismatch returns
	// RECONCILIATION_HASH_MISMATCH carrying ONLY expected/actual (design §6).
	actual := core.ComputeReconciliationHash(reconciliation)
	if !strings.EqualFold(strings.TrimSpace(p.ExpectedReconciliationHash), actual) {
		return core.Reconciliation{}, "", false, auth.NewReconciliationHashMismatch(p.ExpectedReconciliationHash, actual, "reconciliation changed after review; expected hash does not match the current proposed state")
	}

	// 6. Pure policy in-transaction (tenant → company → membership → role →
	// assurance); any denial returns its frozen reason code.
	decision := policy.Authorize(principal, reconciliation)
	if !decision.Allowed {
		return core.Reconciliation{}, "", false, auth.New(decision.ReasonCode, "reconciliation authorization policy denied the "+p.Action+" decision")
	}

	// 7. Apply the pure machine transition on the snapshot with ONE captured
	// timestamp; the canonical snapshot carries sorted/deduplicated roles.
	snapshot := principal.PrincipalSnapshot()
	resulting := reconciliation
	if p.Action == "confirm" {
		if err := core.ConfirmReconciliation(&resulting, p.Resolution, &snapshot, decision.PolicyVersion, now); err != nil {
			return core.Reconciliation{}, "", false, err
		}
	} else {
		if err := core.RejectReconciliation(&resulting, p.Resolution, &snapshot, decision.PolicyVersion, now); err != nil {
			return core.Reconciliation{}, "", false, err
		}
	}

	rolesJSON, err := json.Marshal(snapshot.Roles)
	if err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: encode adjudicator roles: %w", err)
	}

	// 8. Guarded UPDATE: exactly one proposed row flips to the target status.
	res, err := conn.ExecContext(ctx, `
		UPDATE reconciliations SET status = ?, resolution = ?, policy_version = ?,
			adjudicator_subject_id = ?, adjudicator_membership_id = ?, adjudicator_roles_json = ?,
			authentication_method = ?, assurance_level = ?, principal_authenticated_at = ?,
			decided_at = ?
		WHERE id = ? AND status = 'proposed'`,
		string(p.ToStatus), p.Resolution, resulting.PolicyVersion,
		snapshot.SubjectID, snapshot.MembershipID, string(rolesJSON),
		string(snapshot.AuthenticationMethod), string(snapshot.AssuranceLevel), snapshot.AuthenticatedAt,
		now, p.ReconciliationID,
	)
	if err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: "+p.Action+" update: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: "+p.Action+" rows affected: %w", err)
	}
	if affected != 1 {
		return core.Reconciliation{}, "", false, auth.New(auth.CodeInvalidReconciliationTransition, "guarded status update did not match exactly one proposed row")
	}

	// 9. The immutable decision event; confirm/reject events carry the principal
	// snapshot and the frozen policy version (events CHECK). The
	// reconciliation_hash records the resulting state (the exact hash the
	// confirmed/rejected row now hashes to).
	eventID, err := newUUID()
	if err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: generate reconciliation event id: %w", err)
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: encode principal snapshot: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO reconciliation_events (
			id, reconciliation_id, request_id, action, from_status, to_status, reconciliation_hash,
			principal_snapshot_json, policy_version, reason, created_at
		) VALUES (?, ?, ?, ?, 'proposed', ?, ?, ?, ?, ?, ?)`,
		eventID, p.ReconciliationID, p.RequestID, p.Action, string(p.ToStatus),
		core.ComputeReconciliationHash(resulting), string(snapshotJSON), resulting.PolicyVersion, p.Resolution, now,
	); err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: insert "+p.Action+" event: %w", err)
	}

	// 10. Confirm-only: the reconciles observation relation projection
	// (INSERT ... SELECT ... WHERE NOT EXISTS — observations.relations is a
	// projection; reconciliations remain authoritative). The relation is frozen
	// to 'reconciles' and its actor is the verified subject.
	if p.RelationProjection {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO relations (from_id, to_id, relation, actor, timestamp)
			SELECT ?, ?, 'reconciles', ?, ?
			WHERE NOT EXISTS (SELECT 1 FROM relations WHERE from_id = ? AND to_id = ? AND relation = 'reconciles')`,
			reconciliation.LeftMemoryID, reconciliation.RightMemoryID, snapshot.SubjectID, now,
			reconciliation.LeftMemoryID, reconciliation.RightMemoryID,
		); err != nil {
			return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: insert reconciles relation projection: %w", err)
		}
	}

	// 11. Confirm-only, correction: the predecessor must be confirmed for the
	// supersession to be atomic with this confirmation, or already superseded by
	// THIS very proposal at propose time; anything else is an invalid
	// transition.
	if p.SupersedePredecessor && reconciliation.PredecessorID != "" {
		pred, ok := s.readReconciliation(ctx, conn, reconciliation.PredecessorID)
		if !ok {
			return core.Reconciliation{}, "", false, auth.New(auth.CodeReconciliationNotFound, "predecessor reconciliation not found: "+reconciliation.PredecessorID)
		}
		switch pred.Status {
		case core.ReconciliationConfirmed:
			// The superseded predecessor keeps its original decided_at (the
			// immutability trigger allows ONLY routing-field changes).
			res, err := conn.ExecContext(ctx, `
				UPDATE reconciliations SET status = 'superseded', supersedes_id = ?
				WHERE id = ? AND status = 'confirmed'`,
				reconciliation.ID, pred.ID,
			)
			if err != nil {
				return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: supersede predecessor: %w", err)
			}
			affected, err := res.RowsAffected()
			if err != nil {
				return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: supersede predecessor rows affected: %w", err)
			}
			if affected != 1 {
				return core.Reconciliation{}, "", false, auth.New(auth.CodeInvalidReconciliationTransition, "guarded predecessor supersession did not match exactly one confirmed row")
			}
			superseded := pred
			if err := core.SupersedeReconciliation(&superseded, reconciliation.ID, now); err != nil {
				return core.Reconciliation{}, "", false, err
			}
			predEventID, err := newUUID()
			if err != nil {
				return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: generate predecessor event id: %w", err)
			}
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO reconciliation_events (
					id, reconciliation_id, request_id, action, from_status, to_status, reconciliation_hash,
					principal_snapshot_json, policy_version, reason, created_at
				) VALUES (?, ?, ?, 'supersede', 'confirmed', 'superseded', ?, NULL, NULL, ?, ?)`,
				predEventID, pred.ID, p.RequestID, core.ComputeReconciliationHash(superseded), p.Resolution, now,
			); err != nil {
				return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: insert predecessor supersede event: %w", err)
			}
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO reconciliation_relations (from_reconciliation_id, to_reconciliation_id, relation, actor, timestamp)
				VALUES (?, ?, 'supersedes', ?, ?)`,
				pred.ID, reconciliation.ID, snapshot.SubjectID, now,
			); err != nil {
				return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: insert supersedes relation: %w", err)
			}
		case core.ReconciliationSuperseded:
			if pred.SupersedesID != reconciliation.ID {
				return core.Reconciliation{}, "", false, auth.New(auth.CodeInvalidReconciliationTransition, "predecessor is already superseded by a different reconciliation")
			}
			// Already superseded by this very correction at propose time.
		default:
			return core.Reconciliation{}, "", false, auth.New(auth.CodeInvalidReconciliationTransition, fmt.Sprintf("predecessor status %q cannot be superseded by a correction", pred.Status))
		}
	}

	// 12. Atomic receipt emission: after the decision event, the projection and
	// the (covered) predecessor supersession, BEFORE the idempotency completion,
	// inside the SAME transaction with the captured now.
	// reconciliation_confirmed / reconciliation_rejected carries the
	// reviewed/resulting reconciliation hashes (in the payload's judgment-hash
	// fields — the canonical payload shape is FROZEN and never gains new keys,
	// so prior receipts stay byte-valid), both locked observation ids and
	// envelope hashes, the resolution and the complete verified principal
	// snapshot. The predecessor supersession is covered inside
	// reconciliation_confirmed — it never creates another action. A signing
	// failure rolls the whole decision back (no event, no receipt).
	reconciliationAction := core.ReceiptActionReconciliationConfirmed
	if p.Action == "reject" {
		reconciliationAction = core.ReceiptActionReconciliationRejected
	}
	receiptSnapshot := snapshot
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeReconciliation, reconciliation.ID, reconciliationAction, core.ReceiptPayload{
		Version:                  core.ReceiptPayloadVersionV05,
		TenantID:                 reconciliation.TenantID,
		CompanyID:                reconciliation.CompanyID,
		FiscalPeriodID:           reconciliation.FiscalPeriodID,
		ReviewedJudgmentHash:     actual,
		ResultingJudgmentHash:    core.ComputeReconciliationHash(resulting),
		FromMemoryID:             reconciliation.LeftMemoryID,
		FromEnvelopeHash:         core.ComputeEnvelopeHash(leftObs),
		ToMemoryID:               reconciliation.RightMemoryID,
		ToEnvelopeHash:           core.ComputeEnvelopeHash(rightObs),
		Reason:                   p.Resolution,
		PrincipalID:              receiptSnapshot.SubjectID,
		MembershipID:             receiptSnapshot.MembershipID,
		PrincipalRoles:           receiptPrincipalRoles(receiptSnapshot),
		AuthenticationMethod:     string(receiptSnapshot.AuthenticationMethod),
		AssuranceLevel:           string(receiptSnapshot.AssuranceLevel),
		PrincipalAuthenticatedAt: receiptSnapshot.AuthenticatedAt,
		PolicyVersion:            decision.PolicyVersion,
	}, now); err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: emit "+p.Action+" receipt: %w", err)
	}

	// 13. Complete the reservation (result + event link + completion time — the
	// CHECK requires result_json and reconciliation_event_id to be set together)
	// and commit; the whole decision is one atomic unit.
	result := storedReconciliationResult{ReconciliationID: reconciliation.ID, Reconciliation: resulting, ReconciliationEventID: eventID}
	serializedResult, err := json.Marshal(result)
	if err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: encode "+p.Action+" result: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE reconciliation_idempotency_keys SET result_json = ?, reconciliation_event_id = ?, completed_at = ?
		WHERE tenant_id = ? AND request_id = ?`,
		string(serializedResult), eventID, now, principal.TenantID(), p.RequestID,
	); err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: complete reconciliation idempotency key: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.Reconciliation{}, "", false, fmt.Errorf("persistence error: commit "+p.Action+": %w", err)
	}
	committed = true
	return resulting, eventID, false, nil
}

// ConfirmReconciliation atomically confirms a proposed reconciliation — the
// authenticated adjudication act (design §3.2). It mirrors ConfirmJudgment:
// dedicated connection, literal BEGIN IMMEDIATE, idempotency reservation,
// fresh-hash comparison, pure policy, guarded UPDATE, immutable event, the
// reconciles relation projection and — for a correction — the atomic
// supersession of the confirmed predecessor. Agents can never reach this
// method: the signature REQUIRES a verified principal (an agent Source is
// provenance only and carries no authority).
func (s *SQLiteStore) ConfirmReconciliation(ctx context.Context, cmd core.ConfirmReconciliationCommand, principal auth.VerifiedApprovalPrincipal, policy authz.ReconciliationAuthorizationPolicy) (core.ConfirmReconciliationResult, error) {
	if strings.TrimSpace(cmd.Resolution) == "" {
		return core.ConfirmReconciliationResult{}, auth.New(auth.CodeResolutionRequired, "confirmation requires a non-empty professional resolution")
	}
	if strings.TrimSpace(cmd.ReconciliationID) == "" || strings.TrimSpace(cmd.ExpectedReconciliationHash) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.ConfirmReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "confirm command is incomplete (reconciliationId, expectedReconciliationHash and requestId are required)")
	}
	r, eventID, replay, err := s.adjudicateReconciliation(ctx, reconciliationDecisionParams{
		ReconciliationID:           cmd.ReconciliationID,
		Resolution:                 cmd.Resolution,
		ExpectedReconciliationHash: cmd.ExpectedReconciliationHash,
		RequestID:                  cmd.RequestID,
		Action:                     "confirm",
		ToStatus:                   core.ReconciliationConfirmed,
		RelationProjection:         true,
		SupersedePredecessor:       true,
	}, principal, policy)
	if err != nil {
		return core.ConfirmReconciliationResult{}, err
	}
	return core.ConfirmReconciliationResult{ReconciliationID: r.ID, Reconciliation: r, ReconciliationEventID: eventID, IdempotentReplay: replay}, nil
}

// RejectReconciliation atomically rejects a proposed reconciliation: the same
// lock/hash/policy/idempotency path as confirmation, storing the HUMAN reason
// as the resolution and becoming terminal. It writes NO observation relation
// projection and performs no supersession (a rejected correction leaves its
// predecessor current).
func (s *SQLiteStore) RejectReconciliation(ctx context.Context, cmd core.RejectReconciliationCommand, principal auth.VerifiedApprovalPrincipal, policy authz.ReconciliationAuthorizationPolicy) (core.RejectReconciliationResult, error) {
	if strings.TrimSpace(cmd.Reason) == "" {
		return core.RejectReconciliationResult{}, auth.New(auth.CodeResolutionRequired, "rejection requires a non-empty human reason")
	}
	if strings.TrimSpace(cmd.ReconciliationID) == "" || strings.TrimSpace(cmd.ExpectedReconciliationHash) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.RejectReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "reject command is incomplete (reconciliationId, expectedReconciliationHash and requestId are required)")
	}
	r, eventID, replay, err := s.adjudicateReconciliation(ctx, reconciliationDecisionParams{
		ReconciliationID:           cmd.ReconciliationID,
		Resolution:                 cmd.Reason,
		ExpectedReconciliationHash: cmd.ExpectedReconciliationHash,
		RequestID:                  cmd.RequestID,
		Action:                     "reject",
		ToStatus:                   core.ReconciliationRejected,
	}, principal, policy)
	if err != nil {
		return core.RejectReconciliationResult{}, err
	}
	return core.RejectReconciliationResult{ReconciliationID: r.ID, Reconciliation: r, ReconciliationEventID: eventID, IdempotentReplay: replay}, nil
}

// WithdrawReconciliation withdraws the caller's OWN proposed reconciliation
// (terminal). The SAME exact proposer identity (system+actorId+actorKind+
// session) is required — mismatch is PROPOSAL_UNAUTHORIZED (provenance
// continuity, never professional authorization). Idempotency is keyed by
// (tenant from the reconciliation, requestId); the schema CHECK requires
// decided_at on every non-proposed row, so the withdrawal stamps it.
func (s *SQLiteStore) WithdrawReconciliation(ctx context.Context, cmd core.WithdrawReconciliationCommand, caller core.Source) (core.WithdrawReconciliationResult, error) {
	if strings.TrimSpace(cmd.ReconciliationID) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.WithdrawReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "withdraw command is incomplete (reconciliationId and requestId are required)")
	}
	if !core.CanPropose(caller) {
		return core.WithdrawReconciliationResult{}, auth.New(auth.CodeProposalUnauthorized, "only the proposing agent/system may withdraw")
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	now := nowISO()
	commandHash := withdrawReconciliationCommandHash(cmd.ReconciliationID)
	binding := proposerBinding(caller)

	// 1. Read the reconciliation on the locked connection: the tenant for the
	// idempotency key comes from the reconciliation, never from caller claims.
	reconciliation, ok := s.readReconciliation(ctx, conn, cmd.ReconciliationID)
	if !ok {
		return core.WithdrawReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "reconciliation not found: "+cmd.ReconciliationID)
	}

	// 2. Idempotency by (reconciliation tenant, requestId), bound to the exact
	// proposer identity. The resolution runs BEFORE the status/identity gates so
	// a completed reservation REPLAYS even though the row is already withdrawn
	// (idempotency first, status gate second).
	var storedHash, storedBinding string
	var storedResultJSON, completedAt sql.NullString
	err = conn.QueryRowContext(ctx, `
		SELECT command_hash, actor_binding, result_json, completed_at
		FROM reconciliation_idempotency_keys WHERE tenant_id = ? AND request_id = ?`,
		reconciliation.TenantID, cmd.RequestID,
	).Scan(&storedHash, &storedBinding, &storedResultJSON, &completedAt)
	switch {
	case err == nil:
		if storedHash != commandHash || storedBinding != binding {
			return core.WithdrawReconciliationResult{}, auth.New(auth.CodeIdempotencyConflict, "request id already used with a different command or proposer")
		}
		if completedAt.Valid {
			var replay storedReconciliationResult
			if err := json.Unmarshal([]byte(storedResultJSON.String), &replay); err != nil {
				return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: decode replayed reconciliation result: %w", err)
			}
			return core.WithdrawReconciliationResult{ReconciliationID: replay.ReconciliationID, Reconciliation: replay.Reconciliation, ReconciliationEventID: replay.ReconciliationEventID, IdempotentReplay: true}, nil
		}
		// Incomplete reservation (an interrupted attempt that never committed):
		// reuse it — the status gate below decides the outcome.
	case errors.Is(err, sql.ErrNoRows):
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO reconciliation_idempotency_keys (tenant_id, request_id, command_hash, actor_binding, result_json, reconciliation_event_id, created_at, completed_at)
			VALUES (?, ?, ?, ?, NULL, NULL, ?, NULL)`,
			reconciliation.TenantID, cmd.RequestID, commandHash, binding, now,
		); err != nil {
			return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: reserve reconciliation idempotency key: %w", err)
		}
	default:
		return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: read reconciliation idempotency key: %w", err)
	}

	// 3. Only an open proposal may be withdrawn, and only by its OWN proposer
	// (provenance continuity — never professional authorization).
	if reconciliation.Status != core.ReconciliationProposed {
		return core.WithdrawReconciliationResult{}, auth.New(auth.CodeInvalidReconciliationTransition, fmt.Sprintf("withdrawal is not legal from status %q", reconciliation.Status))
	}
	if proposerBinding(reconciliation.Proposer) != binding {
		return core.WithdrawReconciliationResult{}, auth.New(auth.CodeProposalUnauthorized, "a reconciliation may only be withdrawn by its own proposer")
	}

	// 4. Guarded UPDATE: exactly one proposed row closes as withdrawn; the
	// schema CHECK requires decided_at here.
	res, err := conn.ExecContext(ctx, `
		UPDATE reconciliations SET status = 'withdrawn', decided_at = ?
		WHERE id = ? AND status = 'proposed'`,
		now, cmd.ReconciliationID,
	)
	if err != nil {
		return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: withdraw update: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: withdraw rows affected: %w", err)
	}
	if affected != 1 {
		return core.WithdrawReconciliationResult{}, auth.New(auth.CodeInvalidReconciliationTransition, "guarded status update did not match exactly one proposed row")
	}

	// 5. The immutable 'withdraw' event (no snapshot, no policy version).
	withdrawn := reconciliation
	if err := core.WithdrawReconciliation(&withdrawn, now); err != nil {
		return core.WithdrawReconciliationResult{}, err
	}
	eventID, err := newUUID()
	if err != nil {
		return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: generate reconciliation event id: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO reconciliation_events (
			id, reconciliation_id, request_id, action, from_status, to_status, reconciliation_hash,
			principal_snapshot_json, policy_version, reason, created_at
		) VALUES (?, ?, ?, 'withdraw', 'proposed', 'withdrawn', ?, NULL, NULL, '', ?)`,
		eventID, cmd.ReconciliationID, cmd.RequestID, core.ComputeReconciliationHash(withdrawn), now,
	); err != nil {
		return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: insert withdraw event: %w", err)
	}

	// 6. Complete the reservation (event exists → result_json is set with it)
	// and commit.
	result := storedReconciliationResult{ReconciliationID: reconciliation.ID, Reconciliation: withdrawn, ReconciliationEventID: eventID}
	serializedResult, err := json.Marshal(result)
	if err != nil {
		return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: encode withdraw result: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE reconciliation_idempotency_keys SET result_json = ?, reconciliation_event_id = ?, completed_at = ?
		WHERE tenant_id = ? AND request_id = ?`,
		string(serializedResult), eventID, now, reconciliation.TenantID, cmd.RequestID,
	); err != nil {
		return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: complete reconciliation idempotency key: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.WithdrawReconciliationResult{}, fmt.Errorf("persistence error: commit withdraw: %w", err)
	}
	committed = true

	return core.WithdrawReconciliationResult{ReconciliationID: reconciliation.ID, Reconciliation: withdrawn, ReconciliationEventID: eventID, IdempotentReplay: false}, nil
}

// GetReconciliation returns the reconciliation with the given id, if any — the
// public read-only surface of the reconciliation store. It reads through the
// pool connection and never participates in an adjudication transition; every
// race-sensitive read lives inside the ProposeReconciliation/
// ConfirmReconciliation/RejectReconciliation/WithdrawReconciliation
// transactions.
func (s *SQLiteStore) GetReconciliation(ctx context.Context, id string) (core.Reconciliation, bool) {
	return s.readReconciliation(ctx, s.db, id)
}

// ReconciliationSuccessorOf routes readers from a superseded reconciliation to
// its correction: it reads reconciliation_relations (frozen to 'supersedes';
// reconciliation ids never enter the observation relations table) and returns
// the successor reconciliation.
func (s *SQLiteStore) ReconciliationSuccessorOf(ctx context.Context, reconciliationID string) (core.Reconciliation, bool) {
	var toID string
	err := s.db.QueryRowContext(ctx, `
		SELECT to_reconciliation_id FROM reconciliation_relations
		WHERE from_reconciliation_id = ? AND relation = 'supersedes' ORDER BY rowid LIMIT 1`, reconciliationID).Scan(&toID)
	if err != nil {
		return core.Reconciliation{}, false
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+reconciliationColumns+` FROM reconciliations WHERE id = ?`, toID)
	r, err := scanReconciliation(row)
	if err != nil {
		return core.Reconciliation{}, false
	}
	return r, true
}
