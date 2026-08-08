// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the READ-ONLY deterministic evidence-lifecycle
// export layer of the v0.8 evidence lifecycle (batch 4 —
// docs/architecture/evidence-lifecycle-v0.8.md §12; WU-3 — core/store/API
// boundary only; the HTTP/CLI/MCP surfaces are deferred to the transport work):
//
//   - ExportEvidenceLifecycle is a READ-ONLY, tenant/RUC-scoped audit query. It
//     runs inside ONE read-only transaction (sql.TxOptions{ReadOnly: true} —
//     the driver issues a deferred read transaction; the method NEVER commits,
//     only rolls back) and performs ONLY SELECTs: NO receipts, NO idempotency
//     keys, NO signing-key registration, NO row writes of any kind. The export
//     intentionally emits NO receipt — it is a query, not a material export act:
//     the bundle is DETERMINISTIC (identical data → identical canonical bytes →
//     identical content-addressed exportId), so replay/idempotency is
//     structural, and a receipt would add a wall-clock issuedAt plus a row
//     write, contradicting both the determinism and the read-only contract
//     (documented in the Store interface and in core/evidence_export.go §12);
//   - the request is an explicit RUC-scoped criteria (tenant/company/RUC,
//     optional YYYYMM period — an empty period selects ALL periods of the RUC).
//     Every query is SCOPE-FIRST: the SQL WHERE clauses pin tenant/company/RUC
//     (and the exact period when selected), and rows without their own scope
//     columns (events, approvals, executions, retention states) are joined to
//     their scope authority (the evidence_object / evidence_purge_request rows
//     in scope). Receipts are additionally pinned by JOIN to the scoped object
//     AND carry their own stamped scope columns (defense in depth). The bundle
//     then runs core.ValidateEvidenceExportScopeCoverage +
//     core.AssertValidEvidenceExportBundle — ANY cross-scope row fails closed
//     (EXPORT_SCOPE_VIOLATION), never silently dropped;
//   - the bundle carries object metadata (NEVER bytes), the current
//     lifecycle-state projection, the BOUND retention policies (the union of
//     the policy ids referenced by the scoped retention states and purge
//     requests — the policy resolution evidence), holds, purge
//     requests/approvals/executions, lifecycle events, the complete per-subject
//     receipt chains (chain order: subjectId → issuedAt → insertion) and the
//     referenced public signing keys;
//   - a purged object (a committed execution row) exports immutable
//     metadata/hash/lifecycle/receipt evidence ONLY: its object entry carries
//     bytes: "purged" with the purge_executed completion receipt hash. Object
//     bytes are never read, hashed or carried — for every object the entry
//     carries bytes: "stored" (expected present — never verified) or "purged".
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ──────────────────────────────────────────────
// Export queries (scope-first, read-only)
// ──────────────────────────────────────────────

// exportScopeClause returns the scope-first SQL WHERE suffix of the criteria:
// the qualified tenant/company/RUC equality always, plus the exact period when
// the criteria carries one (an empty criteria period selects ALL periods of the
// RUC — period is the last scope dimension and a perioded export never leaks a
// different period). qualifier is the SQL table alias (e.g. "o", "r", "h").
func exportScopeClause(qualifier string, scope core.Scope) (string, []any) {
	q := qualifier + "."
	suffix := q + "tenant_id = ? AND " + q + "company_id = ? AND " + q + "ruc = ?"
	args := []any{scope.OrganizationID, scope.CompanyID, scope.RUC}
	if scope.Period != "" {
		suffix += " AND " + q + "period = ?"
		args = append(args, scope.Period)
	}
	return suffix, args
}

// exportObjectColumns is the fixed column projection of one evidence_objects row
// (qualified for the JOIN-free scope query).
const exportObjectColumns = `o.id, o.sha256, o.size, o.content_type, o.tenant_id, o.company_id, o.ruc, o.period,
	o.source_system, o.source_reference, o.source_actor_id, o.source_actor_kind,
	o.stored_by, o.stored_at, o.rel_path`

// scanExportObject scans one evidence_objects row into the core model.
func scanExportObject(sc interface{ Scan(dest ...any) error }) (core.EvidenceObject, error) {
	var o core.EvidenceObject
	if err := sc.Scan(
		&o.ObjectID, &o.SHA256, &o.Size, &o.ContentType,
		&o.TenantID, &o.CompanyID, &o.RUC, &o.Period,
		&o.SourceSystem, &o.SourceReference, &o.SourceActorID, &o.SourceActorKind,
		&o.StoredBy, &o.StoredAt, &o.RelPath,
	); err != nil {
		return core.EvidenceObject{}, err
	}
	return o, nil
}

// exportLifecycleStateColumns is the fixed column projection of one
// evidence_retention_state row (qualified for the object JOIN).
const exportLifecycleStateColumns = `rs.object_id, rs.lifecycle_state, rs.retention_state,
	COALESCE(rs.policy_id, ''), rs.category, rs.has_active_blocking_hold, rs.current_hash, rs.updated_at`

// scanExportLifecycleState scans one projection row into the core model.
func scanExportLifecycleState(sc interface{ Scan(dest ...any) error }) (core.EvidenceRetentionState, error) {
	var (
		p           core.EvidenceRetentionState
		hasBlocking int
	)
	if err := sc.Scan(
		&p.ObjectID, &p.LifecycleState, &p.RetentionState, &p.PolicyID, &p.Category,
		&hasBlocking, &p.CurrentHash, &p.UpdatedAt,
	); err != nil {
		return core.EvidenceRetentionState{}, err
	}
	p.HasActiveBlockingHold = hasBlocking != 0
	return p, nil
}

// exportLifecycleEventColumns is the fixed column projection of one
// evidence_lifecycle_events row (qualified for the object JOIN; request_id is
// NULLable — COALESCE to the empty string).
const exportLifecycleEventColumns = `ev.id, ev.object_id, COALESCE(ev.request_id, ''), ev.action,
	ev.from_state, ev.to_state, ev.reviewed_hash, ev.resulting_hash,
	ev.principal_snapshot_json, ev.reason, ev.policy_version, ev.created_at`

// scanExportLifecycleEvent scans one immutable event row into the core model
// (the stored principal snapshot is canonical JSON — decode failure is an
// internal invariant violation and fails closed).
func scanExportLifecycleEvent(sc interface{ Scan(dest ...any) error }) (core.EvidenceLifecycleEvent, error) {
	var (
		ev           core.EvidenceLifecycleEvent
		snapshotJSON string
	)
	if err := sc.Scan(
		&ev.EventID, &ev.ObjectID, &ev.RequestID, &ev.Action,
		&ev.FromState, &ev.ToState, &ev.ReviewedHash, &ev.ResultingHash,
		&snapshotJSON, &ev.Reason, &ev.PolicyVersion, &ev.CreatedAt,
	); err != nil {
		return core.EvidenceLifecycleEvent{}, err
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &ev.PrincipalSnapshot); err != nil {
		return core.EvidenceLifecycleEvent{}, fmt.Errorf("decode event principal snapshot: %w", err)
	}
	return ev, nil
}

// exportSigningKeyColumns is the fixed column projection of one signing_keys row.
const exportSigningKeyColumns = `key_id, algorithm, public_key, created_at, COALESCE(revoked_at, '')`

// scanExportSigningKey scans one public signing-key row into the export model.
func scanExportSigningKey(sc interface{ Scan(dest ...any) error }) (core.EvidenceExportSigningKey, error) {
	var k core.EvidenceExportSigningKey
	if err := sc.Scan(&k.KeyID, &k.Algorithm, &k.PublicKey, &k.CreatedAt, &k.RevokedAt); err != nil {
		return core.EvidenceExportSigningKey{}, err
	}
	return k, nil
}

// placeholderList renders "?,?,…" for n placeholders (the dynamic IN-list
// builder of the key/policy lookups; the inputs are bounded — the referenced key
// ids and bound policy ids of the scope).
func placeholderList(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ──────────────────────────────────────────────
// ExportEvidenceLifecycle
// ──────────────────────────────────────────────

// ExportEvidenceLifecycle returns the deterministic, tenant/RUC-scoped
// evidence-lifecycle audit bundle (design §12; WU-3) for an explicit
// RUC-scoped request. The whole read runs inside ONE READ-ONLY transaction that
// is only ever rolled back (the driver issues a deferred read transaction — the
// method performs ONLY SELECTs and emits NO receipt, NO idempotency key and NO
// row write of any kind: the export is a query, and identical data yields the
// identical canonical bundle + content-addressed exportId). Scope is enforced
// structurally by every query (tenant/company/RUC, exact period when selected)
// and double-checked by the fail-closed core validators
// (ValidateEvidenceExportScopeCoverage + AssertValidEvidenceExportBundle) —
// any row that would cross the tenant/company/RUC/period boundary fails the
// export with EXPORT_SCOPE_VIOLATION, never a silent drop. A purged object
// exports immutable metadata/hash/lifecycle/receipt evidence ONLY (bytes:
// "purged" + the purge_executed completion receipt hash); object bytes are
// never read or carried.
func (s *SQLiteStore) ExportEvidenceLifecycle(ctx context.Context, criteria core.EvidenceExportCriteria) (core.EvidenceExportBundle, error) {
	if err := core.AssertValidEvidenceExportCriteria(criteria); err != nil {
		return core.EvidenceExportBundle{}, err
	}

	// ONE read-only transaction — the read-only proof: the driver issues a
	// deferred READ transaction and the method only ever ROLLS BACK (never
	// commits), so no mutation can escape even through a code path defect.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: begin read-only export transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	scope := criteria.Scope

	// 1. Objects — scope-first by their own flattened scope columns (the exact
	// tenant/company/RUC tuple, optional period).
	objectClause, objectArgs := exportScopeClause("o", scope)
	objRows, err := tx.QueryContext(ctx, `
		SELECT `+exportObjectColumns+` FROM evidence_objects o WHERE `+objectClause+`
		ORDER BY o.id`, objectArgs...)
	if err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: query export objects: %w", err)
	}
	objects := make([]core.EvidenceObject, 0)
	for objRows.Next() {
		o, err := scanExportObject(objRows)
		if err != nil {
			_ = objRows.Close()
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: scan export object: %w", err)
		}
		objects = append(objects, o)
	}
	if err := objRows.Close(); err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: close export objects: %w", err)
	}

	// 2. Lifecycle-state projection — derived queryable state of the scoped
	// objects (JOIN on the object is the scope authority; the projection FK
	// guarantees the object row exists).
	rsRows, err := tx.QueryContext(ctx, `
		SELECT `+exportLifecycleStateColumns+` FROM evidence_retention_state rs
		JOIN evidence_objects o ON o.id = rs.object_id
		WHERE `+objectClause+`
		ORDER BY rs.object_id`, objectArgs...)
	if err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: query export lifecycle states: %w", err)
	}
	lifecycleStates := make([]core.EvidenceRetentionState, 0)
	for rsRows.Next() {
		p, err := scanExportLifecycleState(rsRows)
		if err != nil {
			_ = rsRows.Close()
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: scan export lifecycle state: %w", err)
		}
		lifecycleStates = append(lifecycleStates, p)
	}
	if err := rsRows.Close(); err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: close export lifecycle states: %w", err)
	}

	// 3. Holds — scope-first by their own flattened scope columns.
	holdClause, holdArgs := exportScopeClause("h", scope)
	holdRows, err := tx.QueryContext(ctx, `
		SELECT `+evidenceHoldColumns+` FROM evidence_holds h WHERE `+holdClause+`
		ORDER BY h.object_id, h.placed_at, h.id`, holdArgs...)
	if err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: query export holds: %w", err)
	}
	holds := make([]core.EvidenceHold, 0)
	for holdRows.Next() {
		h, err := scanEvidenceHold(holdRows)
		if err != nil {
			_ = holdRows.Close()
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: scan export hold: %w", err)
		}
		holds = append(holds, h)
	}
	if err := holdRows.Close(); err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: close export holds: %w", err)
	}

	// 4. Purge requests — scope-first by their own flattened scope columns.
	reqClause, reqArgs := exportScopeClause("r", scope)
	reqRows, err := tx.QueryContext(ctx, `
		SELECT `+purgeRequestColumns+` FROM evidence_purge_requests r WHERE `+reqClause+`
		ORDER BY r.object_id, r.id`, reqArgs...)
	if err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: query export purge requests: %w", err)
	}
	requests := make([]core.EvidencePurgeRequest, 0)
	for reqRows.Next() {
		r, err := scanPurgeRequest(reqRows)
		if err != nil {
			_ = reqRows.Close()
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: scan export purge request: %w", err)
		}
		requests = append(requests, r)
	}
	if err := reqRows.Close(); err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: close export purge requests: %w", err)
	}

	// 5. Approvals — through their request (the scope authority; the approval
	// ledger carries no scope columns of its own). The projection is qualified
	// (a.*) because the join exposes the request row's id/request_id columns.
	appRows, err := tx.QueryContext(ctx, `
		SELECT a.id, a.request_id, a.approval_order, a.decision, a.reviewed_hash,
			a.resulting_hash, a.principal_snapshot_json, a.reason, a.policy_version, a.created_at
		FROM evidence_purge_approvals a
		JOIN evidence_purge_requests r ON r.id = a.request_id
		WHERE `+reqClause+`
		ORDER BY a.request_id, a.approval_order, a.id`, reqArgs...)
	if err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: query export purge approvals: %w", err)
	}
	approvals := make([]core.EvidencePurgeApproval, 0)
	for appRows.Next() {
		a, err := scanPurgeApproval(appRows)
		if err != nil {
			_ = appRows.Close()
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: scan export purge approval: %w", err)
		}
		approvals = append(approvals, a)
	}
	if err := appRows.Close(); err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: close export purge approvals: %w", err)
	}

	// 6. Executions — through their request (the scope authority). The projection
	// is qualified (e.*) because the join exposes the request row's
	// request_id/object_id columns.
	exeRows, err := tx.QueryContext(ctx, `
		SELECT e.execution_id, e.request_id, e.object_id, e.rel_path, e.size,
			e.pre_removal_hash, e.intent_reviewed_hash, e.state, e.intent_at, e.intent_by,
			COALESCE(e.completed_at, ''), COALESCE(e.completed_by, ''), COALESCE(e.completion_receipt_id, '')
		FROM evidence_purge_executions e
		JOIN evidence_purge_requests r ON r.id = e.request_id
		WHERE `+reqClause+`
		ORDER BY e.request_id, e.execution_id`, reqArgs...)
	if err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: query export purge executions: %w", err)
	}
	executions := make([]core.EvidencePurgeExecution, 0)
	for exeRows.Next() {
		e, err := scanPurgeExecution(exeRows)
		if err != nil {
			_ = exeRows.Close()
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: scan export purge execution: %w", err)
		}
		executions = append(executions, e)
	}
	if err := exeRows.Close(); err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: close export purge executions: %w", err)
	}

	// 7. Lifecycle events — through their object (the scope authority; the event
	// log carries no scope columns of its own).
	evRows, err := tx.QueryContext(ctx, `
		SELECT `+exportLifecycleEventColumns+` FROM evidence_lifecycle_events ev
		JOIN evidence_objects o ON o.id = ev.object_id
		WHERE `+objectClause+`
		ORDER BY ev.object_id, ev.created_at, ev.id`, objectArgs...)
	if err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: query export lifecycle events: %w", err)
	}
	events := make([]core.EvidenceLifecycleEvent, 0)
	for evRows.Next() {
		ev, err := scanExportLifecycleEvent(evRows)
		if err != nil {
			_ = evRows.Close()
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: scan export lifecycle event: %w", err)
		}
		events = append(events, ev)
	}
	if err := evRows.Close(); err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: close export lifecycle events: %w", err)
	}

	// 8. Receipts — the COMPLETE per-subject chains of the scoped objects, in
	// exact chain order (subjectId → issuedAt → insertion — the same order the
	// verification engine walks; receipts are immutable, so the emitted order is
	// stable across repeated exports of the same data). Every receipt also
	// carries its explicit chain ordinal: the 0-based position of the (subjectId,
	// issuedAt, insertion) emission order — the stable tie-break the core
	// canonical sort preserves, so equal issued_at (same-second) chain receipts
	// keep their emission order across canonicalization (the content hash
	// commits to it; the canonical sort NEVER falls back to receipt-hash order).
	// The JOIN pins the subject
	// to the scoped object (defense in depth on top of the stamped scope columns
	// the core validator re-checks).
	recRows, err := tx.QueryContext(ctx, `
        		SELECT r.id, r.subject_type, r.subject_id, r.action, r.tenant_id, r.company_id, r.fiscal_period_id,
        			r.payload_hash, r.previous_receipt_hash, r.principal_id, r.membership_id, r.policy_version,
        			r.algorithm, r.key_id, r.signature, r.issued_at, r.payload_json, r.receipt_hash
        		FROM receipts r JOIN evidence_objects o ON o.id = r.subject_id
        		WHERE r.subject_type = 'evidence_object' AND `+objectClause+`
        		ORDER BY r.subject_id, r.issued_at ASC, r.rowid ASC`, objectArgs...)
	if err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: query export receipts: %w", err)
	}
	receipts := make([]core.EvidenceExportReceipt, 0)
	chainPos := make(map[string]int) // per-subject chain position (0-based)
	for recRows.Next() {
		row, err := scanStoredReceiptRow(recRows)
		if err != nil {
			_ = recRows.Close()
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: scan export receipt: %w", err)
		}
		// The query order (subject_id, issued_at, rowid) IS the chain order; the
		// propagated ordinal is the per-subject position — the explicit stable
		// tie-break the canonical sort preserves (equal issued_at chain receipts
		// keep their emission order; the content hash commits to the chain order).
		pos := chainPos[row.receipt.SubjectID]
		chainPos[row.receipt.SubjectID] = pos + 1
		receipts = append(receipts, core.EvidenceExportReceipt{
			ChainOrdinal: pos,
			Receipt:      row.receipt,
			PayloadJSON:  row.payloadJSON,
			ReceiptHash:  row.storedHash,
		})
	}
	if err := recRows.Close(); err != nil {
		return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: close export receipts: %w", err)
	}

	// 9. Public signing keys referenced by the included receipts (PUBLIC keys
	// only — never private material). A receipt referencing a key with no row
	// fails closed later in AssertValidEvidenceExportBundle (an unverifiable
	// chain is never exported).
	referencedKeyIDs := make([]string, 0, len(receipts))
	seenKeys := make(map[string]struct{}, len(receipts))
	for _, r := range receipts {
		if r.Receipt.KeyID == "" {
			continue
		}
		if _, ok := seenKeys[r.Receipt.KeyID]; ok {
			continue
		}
		seenKeys[r.Receipt.KeyID] = struct{}{}
		referencedKeyIDs = append(referencedKeyIDs, r.Receipt.KeyID)
	}
	sort.Strings(referencedKeyIDs)
	signingKeys := make([]core.EvidenceExportSigningKey, 0)
	if len(referencedKeyIDs) > 0 {
		keyRows, err := tx.QueryContext(ctx, `
			SELECT `+exportSigningKeyColumns+` FROM signing_keys WHERE key_id IN (`+
			placeholderList(len(referencedKeyIDs))+`) ORDER BY key_id`,
			stringSliceToAny(referencedKeyIDs)...)
		if err != nil {
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: query export signing keys: %w", err)
		}
		for keyRows.Next() {
			k, err := scanExportSigningKey(keyRows)
			if err != nil {
				_ = keyRows.Close()
				return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: scan export signing key: %w", err)
			}
			signingKeys = append(signingKeys, k)
		}
		if err := keyRows.Close(); err != nil {
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: close export signing keys: %w", err)
		}
	}

	// 10. Bound retention policies — the union of the policy ids referenced by
	// the scoped projection rows and purge requests (the policy RESOLUTION
	// EVIDENCE of the lifecycle; a bound policy row that vanished is corruption
	// and fails closed — immutable policies never disappear).
	boundPolicyIDs := make([]string, 0, len(lifecycleStates)+len(requests))
	seenPolicies := make(map[string]struct{}, len(lifecycleStates)+len(requests))
	collect := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seenPolicies[id]; ok {
			return
		}
		seenPolicies[id] = struct{}{}
		boundPolicyIDs = append(boundPolicyIDs, id)
	}
	for _, rs := range lifecycleStates {
		collect(rs.PolicyID)
	}
	for _, r := range requests {
		collect(r.PolicyID)
	}
	sort.Strings(boundPolicyIDs)
	policies := make([]core.RetentionPolicy, 0, len(boundPolicyIDs))
	if len(boundPolicyIDs) > 0 {
		polRows, err := tx.QueryContext(ctx, `
			SELECT `+retentionPolicyColumns+` FROM retention_policies WHERE id IN (`+
			placeholderList(len(boundPolicyIDs))+`) ORDER BY id`,
			stringSliceToAny(boundPolicyIDs)...)
		if err != nil {
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: query export retention policies: %w", err)
		}
		resolved := make(map[string]bool, len(boundPolicyIDs))
		for polRows.Next() {
			p, err := scanRetentionPolicyRows(polRows)
			if err != nil {
				_ = polRows.Close()
				return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: scan export retention policy: %w", err)
			}
			resolved[p.PolicyID] = true
			policies = append(policies, p)
		}
		if err := polRows.Close(); err != nil {
			return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: close export retention policies: %w", err)
		}
		for _, id := range boundPolicyIDs {
			if !resolved[id] {
				return core.EvidenceExportBundle{}, fmt.Errorf("persistence error: bound retention policy %s not found (corruption — immutable policies never vanish)", id)
			}
		}
	}

	// 11. Per-object byte state derived from the immutable execution ledger: a
	// COMMITTED execution removed the bytes (bytes: "purged" + the completion
	// receipt hash — the design §12 audit anchor); an object with a receipt-covered
	// purge INTENT whose completion never committed (a crash-intent — an
	// interrupted/recovery-window attempt) is bytes: "intended" — its bytes may
	// or may not be present and are NEVER presented as ordinary stored bytes;
	// every other object is bytes: "stored" (expected present — the export never
	// reads or verifies bytes, it only records the lifecycle truth). A completed
	// execution wins over any interrupted history; at most one execution can
	// complete per request and a request executes once, so the per-object map is
	// unambiguous.
	purgedReceipt := make(map[string]string, len(executions))
	intended := make(map[string]bool, len(executions))
	for _, e := range executions {
		if e.State == core.PurgeExecutionCompleted {
			purgedReceipt[e.ObjectID] = e.CompletionReceiptID
		}
		if e.State == core.PurgeExecutionIntent || e.State == core.PurgeExecutionInterrupted {
			intended[e.ObjectID] = true
		}
	}
	objectExports := make([]core.EvidenceObjectExport, 0, len(objects))
	for _, o := range objects {
		entry := core.EvidenceObjectExport{Object: o, BytesState: core.EvidenceBytesStored}
		if receiptHash, purged := purgedReceipt[o.ObjectID]; purged {
			entry.BytesState = core.EvidenceBytesPurged
			entry.PurgeExecutedReceiptHash = receiptHash
		} else if intended[o.ObjectID] {
			// A crash-intent is never presented as ordinary stored bytes: the honest
			// state is "intended" (a receipt-covered intent exists; the completion
			// evidence is absent — bytes presence is not claimed).
			entry.BytesState = core.EvidenceBytesIntended
		}
		objectExports = append(objectExports, entry)
	}

	// 12. Assemble, canonicalize and self-hash (core owns the byte contract):
	// contentManifestHash covers the canonical CONTENT (every array/row in
	// canonical order, receipts in their per-subject chain order — (subjectId,
	// issuedAt, chain ordinal) — with the chain ordinal committed to the hash) and
	// bundleHash is the ENVELOPE hash (version, scope, criteria, generatedAt,
	// counts, contentManifestHash) — generatedAt participates only in the
	// envelope, never in the content hash.
	bundle := core.EvidenceExportBundle{
		Objects:           objectExports,
		LifecycleStates:   lifecycleStates,
		RetentionPolicies: policies,
		Holds:             holds,
		PurgeRequests:     requests,
		PurgeApprovals:    approvals,
		PurgeExecutions:   executions,
		LifecycleEvents:   events,
		Receipts:          receipts,
		SigningKeys:       signingKeys,
	}
	core.CanonicalizeEvidenceExportBundle(&bundle)
	generatedAt := core.EvidenceExportGeneratedAt(bundle)
	counts := core.EvidenceExportCountsOf(bundle)
	contentHash := core.ComputeEvidenceExportContentHash(bundle)
	bundle.Manifest = core.BuildEvidenceExportManifest(scope, criteria, counts, generatedAt, contentHash)

	// 13. Fail closed: scope coverage (no row crosses tenant/company/RUC/period)
	// and bundle self-consistency (version, self-hash, counts, byte states,
	// signing-key coverage).
	if err := core.AssertValidEvidenceExportBundle(bundle); err != nil {
		return core.EvidenceExportBundle{}, err
	}
	return bundle, nil
}

// stringSliceToAny adapts a string slice to the variadic any query arguments.
func stringSliceToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
