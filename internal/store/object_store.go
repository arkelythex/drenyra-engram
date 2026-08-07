// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the LOCAL WORM evidence
// object store (v0.7.0 local-first slice — docs/architecture/
// evidence-object-v0.7.md §4/§5).
//
// WORM contract (write-once, read-many):
//   - identity is content-addressed: the lowercase SHA-256 hex of the bytes IS
//     the object id (core.ComputeObjectID); identical bytes are the SAME
//     object, so a duplicate store is a NO-OP (created=false — no new row, no
//     receipt);
//   - the on-disk layout is deterministic and content-addressed:
//     <objectsRoot>/objects/<sha[0:2]>/<sha[2:4]>/<sha>; no caller-controlled
//     name ever enters the path (path traversal cannot be expressed);
//   - a write is temp-file + fsync + atomic rename in the SAME directory, then
//     the immutable evidence_objects row + the object_stored receipt commit in
//     ONE SQLite transaction; a signing failure rolls the row back (a leftover
//     orphan file is harmless — content-addressed, unreferenced, never served);
//   - a read re-hashes the bytes and fails closed on any mismatch
//     (OBJECT_BYTES_MISSING | OBJECT_HASH_MISMATCH); silent repair is
//     FORBIDDEN (contracts/provenance.md frozen policy);
//   - there is NO overwrite API and NO delete API: evidence_objects rows are
//     guarded by no-update/no-delete triggers at the schema level and the
//     object methods expose no mutation beyond the initial store.
//
// Object bytes are DATA, never instructions: this layer only hashes, stores,
// reads and re-hashes — it never parses, executes or interprets content, and
// storing an object never authorizes anything (non-authorization boundary).
//
// Scope discipline: writes require an exact company scope (institutional
// objects are a documented deferral) and enforce the closed-period gate
// (assertPeriodWritable → PERIOD_CLOSED inside a closed exact company period);
// reads are SCOPE-FIRST — a caller whose exact scope differs from the stored
// scope sees OBJECT_NOT_FOUND, never the object (contracts/scope.md rule:
// cross-tenant invisibility is a defect with a required negative test).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// Frozen object error codes (fail closed — corruption is evidence, never a
// silent repair or a successful skip).
const (
	objectErrNotFound     = "OBJECT_NOT_FOUND"
	objectErrInvalid      = "INVALID_OBJECT"
	objectErrBytesMissing = "OBJECT_BYTES_MISSING"
	objectErrHashMismatch = "OBJECT_HASH_MISMATCH"
)

// objectBytesCorruptionError is the typed corruption error returned when a
// stored row's WORM bytes are missing or re-hash to a different digest. The
// code string is the stable wire code; silent repair is forbidden.
type objectBytesCorruptionError struct {
	code string
	msg  string
}

func (e *objectBytesCorruptionError) Error() string { return e.code + ": " + e.msg }

// StoreObject captures ONE artifact WORM-style (v0.7.0). The exact company
// scope and the source are validated first (fail closed); the closed-period
// gate runs INSIDE the write transaction before any mutation; a content-
// addressed duplicate (identical bytes already stored) is a NO-OP returning
// created=false with no row and no receipt; a genuinely new object writes its
// bytes temp+sync+atomic-rename, inserts the immutable evidence_objects row and
// emits the object_stored receipt in ONE transaction (a signing failure rolls
// the row back).
func (s *SQLiteStore) StoreObject(ctx context.Context, input core.ObjectStoreInput) (core.ObjectStoreResult, error) {
	if err := core.AssertValidObjectScope(input.Scope); err != nil {
		return core.ObjectStoreResult{}, err
	}
	if err := core.AssertValidSource(input.Source); err != nil {
		return core.ObjectStoreResult{}, err
	}
	objectID := core.ComputeObjectID(input.Bytes)
	relPath := core.ObjectRelPath(objectID)

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return core.ObjectStoreResult{}, fmt.Errorf("persistence error: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// BEGIN IMMEDIATE is the write intent: the gate check, the duplicate
	// detection, the row insert and the receipt emission are ONE atomic unit.
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return core.ObjectStoreResult{}, fmt.Errorf("persistence error: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	// Closed-period gate BEFORE any file write: a rejected write never pollutes
	// the object root (the gate is re-asserted inside the same tx the row
	// commits in, so a concurrent approval cannot race it).
	if err := s.assertPeriodWritable(ctx, conn, input.Scope, "store object"); err != nil {
		return core.ObjectStoreResult{}, err
	}

	// Content-addressed duplicate detection: identical bytes → same id → the
	// object already exists → NO-OP. The stored bytes must still be present on
	// disk (a row whose file is missing is corruption, fail closed — the WORM
	// invariant is checked on every path, including the no-op path).
	var existingID string
	err = conn.QueryRowContext(ctx, `SELECT id FROM evidence_objects WHERE id = ?`, objectID).Scan(&existingID)
	if err == nil {
		// Metadata read on the SAME connection/transaction: the connection is
		// pinned (SetMaxOpenConns(1)), so any s.db query here would deadlock on
		// the held transaction.
		existing, ok := s.evidenceObjectByIDOn(ctx, conn, objectID)
		if !ok {
			return core.ObjectStoreResult{}, &objectBytesCorruptionError{code: objectErrBytesMissing, msg: fmt.Sprintf("evidence_objects row %s exists but its metadata is unreadable", objectID)}
		}
		if err := s.assertObjectBytesPresent(existing.RelPath); err != nil {
			return core.ObjectStoreResult{}, err
		}
		if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
			return core.ObjectStoreResult{}, fmt.Errorf("persistence error: commit duplicate object: %w", err)
		}
		committed = true
		return core.ObjectStoreResult{Object: existing, Created: false}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return core.ObjectStoreResult{}, fmt.Errorf("persistence error: duplicate object check: %w", err)
	}

	// WORM byte write (temp + fsync + atomic rename). One captured timestamp
	// covers the row AND the receipt (provenance continuity).
	now := nowISO()
	if err := writeObjectBytes(s.objectsRoot, relPath, input.Bytes); err != nil {
		return core.ObjectStoreResult{}, err
	}

	if _, err := conn.ExecContext(ctx, `INSERT INTO evidence_objects
		(id, sha256, size, content_type, tenant_id, company_id, ruc, period,
		 source_system, source_reference, source_actor_id, source_actor_kind,
		 stored_by, stored_at, rel_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		objectID, objectID, int64(len(input.Bytes)), input.ContentType,
		input.Scope.OrganizationID, input.Scope.CompanyID, input.Scope.RUC, input.Scope.Period,
		input.Source.System, input.Source.Reference, input.Source.ActorID, string(input.Source.ActorKind),
		input.Source.ActorID, now, relPath,
	); err != nil {
		return core.ObjectStoreResult{}, fmt.Errorf("persistence error: insert evidence_objects: %w", err)
	}

	// Atomic object_stored receipt (v0.7.0): the claimed capture uses the source
	// actor as principalId (non-policy act, kernel policy version); the payload
	// rides the FROZEN v0.7.0 version string while keeping the v0.4.0 payload
	// SHAPE unchanged (versioned protocol decision — verifiers accept v0.4.0 /
	// v0.5.0 / v0.7.0 payloads identically). A signing failure rolls the row
	// back with the receipt (the file may stay as an unreferenced orphan —
	// content-addressed, never served, harmless).
	if _, err := s.emitReceipt(ctx, conn, core.SubjectTypeEvidenceObject, objectID, core.ReceiptActionObjectStored, core.ReceiptPayload{
		Version:        core.ReceiptPayloadVersionV07,
		TenantID:       input.Scope.OrganizationID,
		CompanyID:      input.Scope.CompanyID,
		FiscalPeriodID: input.Scope.Period,
		EvidenceRef:    objectID,
		PrincipalID:    input.Source.ActorID,
		PolicyVersion:  kernelPolicyVersion,
	}, now); err != nil {
		return core.ObjectStoreResult{}, err
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return core.ObjectStoreResult{}, fmt.Errorf("persistence error: commit object: %w", err)
	}
	committed = true

	obj := core.EvidenceObject{
		ObjectID:        objectID,
		SHA256:          objectID,
		Size:            int64(len(input.Bytes)),
		ContentType:     input.ContentType,
		TenantID:        input.Scope.OrganizationID,
		CompanyID:       input.Scope.CompanyID,
		RUC:             input.Scope.RUC,
		Period:          input.Scope.Period,
		SourceSystem:    input.Source.System,
		SourceReference: input.Source.Reference,
		SourceActorID:   input.Source.ActorID,
		SourceActorKind: input.Source.ActorKind,
		StoredBy:        input.Source.ActorID,
		StoredAt:        now,
		RelPath:         relPath,
	}
	return core.ObjectStoreResult{Object: obj, Created: true}, nil
}

// GetObject reads one object SCOPE-FIRST and re-hashes the stored bytes on
// every read. A caller whose exact scope differs from the stored scope gets
// OBJECT_NOT_FOUND (cross-tenant invisibility, fail closed); missing or
// mismatched bytes fail closed with the typed corruption codes — never a
// silent repair.
func (s *SQLiteStore) GetObject(ctx context.Context, objectID string, scope core.Scope) (core.EvidenceObject, []byte, error) {
	obj, ok := s.EvidenceObjectByID(ctx, objectID)
	if !ok {
		return core.EvidenceObject{}, nil, fmt.Errorf("%s: %s", objectErrNotFound, objectID)
	}
	if !objectScopeMatches(obj, scope) {
		// Scope-first invisibility: the object exists but the caller's scope
		// does not equal it — reported as NOT_FOUND, never as a leak.
		return core.EvidenceObject{}, nil, fmt.Errorf("%s: %s", objectErrNotFound, objectID)
	}
	bytes, err := s.readObjectBytes(obj.RelPath)
	if err != nil {
		return core.EvidenceObject{}, nil, err
	}
	if got := core.ComputeObjectID(bytes); got != obj.ObjectID {
		return core.EvidenceObject{}, nil, &objectBytesCorruptionError{code: objectErrHashMismatch,
			msg: fmt.Sprintf("stored bytes of %s re-hash to %s (content address mismatch — corruption, no silent repair)", obj.ObjectID, got)}
	}
	return obj, bytes, nil
}

// EvidenceObjectByID resolves one object METADATA by its content address
// (no bytes, no caller scope — the availability layer and the offline
// verification engine classify refs as object-backed vs legacy through it).
func (s *SQLiteStore) EvidenceObjectByID(ctx context.Context, objectID string) (core.EvidenceObject, bool) {
	return s.evidenceObjectByIDOn(ctx, s.db, objectID)
}

// evidenceObjectByIDOn is the shared row resolution on a caller-provided
// Queryer (the store surface uses s.db; StoreObject's duplicate path uses its
// pinned transaction connection — never s.db, which would deadlock on the
// single-connection pool while the transaction is held).
func (s *SQLiteStore) evidenceObjectByIDOn(ctx context.Context, q Queryer, objectID string) (core.EvidenceObject, bool) {
	var o core.EvidenceObject
	err := q.QueryRowContext(ctx, `SELECT id, sha256, size, content_type,
		tenant_id, company_id, ruc, period,
		source_system, source_reference, source_actor_id, source_actor_kind,
		stored_by, stored_at, rel_path
		FROM evidence_objects WHERE id = ?`, objectID).Scan(
		&o.ObjectID, &o.SHA256, &o.Size, &o.ContentType,
		&o.TenantID, &o.CompanyID, &o.RUC, &o.Period,
		&o.SourceSystem, &o.SourceReference, &o.SourceActorID, &o.SourceActorKind,
		&o.StoredBy, &o.StoredAt, &o.RelPath,
	)
	if err != nil {
		return core.EvidenceObject{}, false
	}
	return o, true
}

// VerifyObjectBytes re-hashes the stored WORM bytes of one object: nil when
// they match the content address, a typed corruption error otherwise. Read-only
// — used by verification and the availability layer; silent repair is
// forbidden.
func (s *SQLiteStore) VerifyObjectBytes(ctx context.Context, objectID string) error {
	obj, ok := s.EvidenceObjectByID(ctx, objectID)
	if !ok {
		return fmt.Errorf("%s: %s", objectErrNotFound, objectID)
	}
	bytes, err := s.readObjectBytes(obj.RelPath)
	if err != nil {
		return err
	}
	if got := core.ComputeObjectID(bytes); got != obj.ObjectID {
		return &objectBytesCorruptionError{code: objectErrHashMismatch,
			msg: fmt.Sprintf("stored bytes of %s re-hash to %s (content address mismatch — corruption, no silent repair)", obj.ObjectID, got)}
	}
	return nil
}

// ObjectAvailability resolves which of the given evidence refs are
// OBJECT-BACKED (they identify stored evidence objects whose WORM bytes
// re-hash to their content address) and returns their metadata. A ref that
// resolves to a row whose bytes are missing/corrupt FAILS CLOSED with a typed
// corruption error (corruption is evidence, never a silent skip); refs with no
// row are simply absent from the result (legacy/unresolved — backward
// compatible).
func (s *SQLiteStore) ObjectAvailability(ctx context.Context, refs []string) (map[string]core.EvidenceObject, error) {
	refs = dedupeSortedRefs(refs)
	resolved := make(map[string]core.EvidenceObject, len(refs))
	for _, ref := range refs {
		obj, ok := s.EvidenceObjectByID(ctx, ref)
		if !ok {
			continue
		}
		if err := s.VerifyObjectBytes(ctx, ref); err != nil {
			// Wrap with the failing ref so the verification layer names the
			// object (corruption is evidence, never a silent skip).
			return nil, fmt.Errorf("%s: %w", ref, err)
		}
		resolved[ref] = obj
	}
	return resolved, nil
}

// ──────────────────────────────────────────────
// WORM byte helpers (fail closed, no silent repair)
// ──────────────────────────────────────────────

// objectPath builds the absolute path of an object's bytes from its stored
// rel_path and FAILS CLOSED when the resolved path escapes the objects root (a
// corrupted rel_path is a corruption signal, never a path to follow).
func (s *SQLiteStore) objectPath(relPath string) (string, error) {
	root := filepath.Clean(s.objectsRoot)
	full := filepath.Clean(filepath.Join(root, relPath))
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", &objectBytesCorruptionError{code: objectErrInvalid,
			msg: fmt.Sprintf("rel_path %q escapes the objects root — corruption", relPath)}
	}
	return full, nil
}

// readObjectBytes reads the stored bytes of one object by its rel_path; a
// missing file fails closed (OBJECT_BYTES_MISSING — never recreated, never
// repaired).
func (s *SQLiteStore) readObjectBytes(relPath string) ([]byte, error) {
	full, err := s.objectPath(relPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &objectBytesCorruptionError{code: objectErrBytesMissing,
				msg: fmt.Sprintf("WORM bytes %s are missing (row without file — corruption, no silent repair)", full)}
		}
		return nil, fmt.Errorf("persistence error: read object bytes: %w", err)
	}
	return data, nil
}

// assertObjectBytesPresent fails closed when a stored row's bytes are missing
// (the duplicate-no-op path re-checks the WORM invariant cheaply).
func (s *SQLiteStore) assertObjectBytesPresent(relPath string) error {
	full, err := s.objectPath(relPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(full); err != nil {
		if os.IsNotExist(err) {
			return &objectBytesCorruptionError{code: objectErrBytesMissing,
				msg: fmt.Sprintf("WORM bytes %s are missing (row without file — corruption, no silent repair)", full)}
		}
		return fmt.Errorf("persistence error: stat object bytes: %w", err)
	}
	return nil
}

// writeObjectBytes performs the WORM write: MkdirAll (0700), temp file in the
// SAME directory, write + fsync, atomic rename into place, then a best-effort
// directory fsync. Rename atomically replaces a leftover orphan (identical
// content-addressed bytes from a previously rolled-back transaction).
func writeObjectBytes(root, relPath string, data []byte) error {
	full := filepath.Clean(filepath.Join(root, relPath))
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return &objectBytesCorruptionError{code: objectErrInvalid,
			msg: fmt.Sprintf("rel_path %q escapes the objects root — corruption", relPath)}
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("persistence error: create object dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("persistence error: create object temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Never leave a half-written temp behind on failure.
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("persistence error: write object temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("persistence error: sync object temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("persistence error: close object temp file: %w", err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		cleanup()
		return fmt.Errorf("persistence error: atomic rename object bytes: %w", err)
	}
	// Best-effort directory durability; a failure is reported but the rename
	// already happened (POSIX atomicity is guaranteed by the rename itself).
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// objectScopeMatches reports whether the caller's exact scope equals the
// object's stored scope. Objects are tenant artifacts: the caller must name the
// exact company scope (institutional callers never match — objects are never
// visible outside their tenant).
func objectScopeMatches(o core.EvidenceObject, scope core.Scope) bool {
	if scope.Kind != core.ScopeKindCompany {
		return false
	}
	return o.TenantID == scope.OrganizationID &&
		o.CompanyID == scope.CompanyID &&
		o.RUC == scope.RUC &&
		o.Period == scope.Period
}

// dedupeSortedRefs returns the sorted, deduplicated, non-empty ref set (the
// canonical order the availability layer resolves — order-independent).
func dedupeSortedRefs(refs []string) []string {
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}
