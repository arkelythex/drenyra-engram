// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the LOCAL WORM evidence
// object store (v0.7.0 local-first slice — docs/architecture/
// evidence-object-v0.7.md §4/§5).
//
// WORM contract (write-once, read-many):
//   - identity is content-addressed: the lowercase SHA-256 hex of the bytes IS
//     the object id (core.ComputeObjectID); identical bytes are the SAME
//     object, so a same-scope duplicate store is a NO-OP (created=false — no
//     new row, no receipt); identical bytes stored under a DIFFERENT exact
//     scope are a typed NON-ENUMERATING conflict (OBJECT_SCOPE_CONFLICT);
//   - the on-disk layout is deterministic and content-addressed:
//     <objectsRoot>/objects/<sha[0:2]>/<sha[2:4]>/<sha>; no caller-controlled
//     name ever enters the path (path traversal cannot be expressed); symlink
//     traversal BELOW the objects root fails closed on every read and write
//     (OBJECT_PATH_INVALID — intermediate components are verified real
//     directories component-by-component, never followed);
//   - a write is temp-file + fsync + atomic rename in the SAME directory + a
//     directory fsync, then the immutable evidence_objects row + the
//     object_stored receipt commit in ONE SQLite transaction; a signing or
//     real directory-sync failure rolls the row back (a leftover orphan byte
//     file is harmless — content-addressed, unreferenced, never served);
//     documented unsupported-filesystem directory-sync errors (EINVAL /
//     ENOTSUP / EOPNOTSUPP / ENOSYS / EBADF, plus Windows' inability to open
//     a directory) are tolerated — the rename itself is already atomic;
//   - a read re-hashes the bytes and fails closed on any mismatch
//     (OBJECT_BYTES_MISSING | OBJECT_HASH_MISMATCH); silent repair is
//     FORBIDDEN (contracts/provenance.md frozen policy);
//   - there is NO overwrite API and NO delete API: evidence_objects rows are
//     guarded by no-update/no-delete triggers at the schema level and the
//     object methods expose no mutation beyond the initial store;
//   - doctor scans the object layer and FAILS CLOSED on rows with missing
//     bytes or invalid paths while REPORTING orphan and temp files (never
//     deleted, never repaired).
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
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// Frozen object error codes (fail closed — corruption is evidence, never a
// silent repair or a successful skip).
const (
	objectErrNotFound      = "OBJECT_NOT_FOUND"
	objectErrInvalid       = "INVALID_OBJECT"
	objectErrBytesMissing  = "OBJECT_BYTES_MISSING"
	objectErrHashMismatch  = "OBJECT_HASH_MISMATCH"
	objectErrPathInvalid   = "OBJECT_PATH_INVALID"
	objectErrScopeConflict = "OBJECT_SCOPE_CONFLICT"
)

// objectBytesCorruptionError is the typed corruption error returned when a
// stored row's WORM bytes are missing or re-hash to a different digest. The
// code string is the stable wire code; silent repair is forbidden.
type objectBytesCorruptionError struct {
	code string
	msg  string
}

func (e *objectBytesCorruptionError) Error() string { return e.code + ": " + e.msg }

// objectPathCorruptionError is the typed error returned when an object path
// escapes the objects root, traverses a symlink, or resolves to a
// non-regular-file component — a path-level corruption signal that always
// fails closed (never followed, never repaired).
type objectPathCorruptionError struct {
	code string
	msg  string
}

func (e *objectPathCorruptionError) Error() string { return e.code + ": " + e.msg }

// objectScopeConflictError is the typed non-enumerating duplicate-scope error.
type objectScopeConflictError struct {
	code string
	msg  string
}

func (e *objectScopeConflictError) Error() string { return e.code + ": " + e.msg }

// StoreObject captures ONE artifact WORM-style (v0.7.0). The exact company
// scope and the source are validated first (fail closed); the closed-period
// gate runs INSIDE the write transaction before any mutation; a same-scope
// content-addressed duplicate (identical bytes already stored) is a NO-OP
// returning created=false with no row and no receipt; identical bytes under a
// DIFFERENT exact scope are a typed NON-ENUMERATING conflict
// (OBJECT_SCOPE_CONFLICT, no scope metadata in the message); a genuinely new
// object writes its bytes temp+sync+atomic-rename+directory-sync, inserts the
// immutable evidence_objects row and emits the object_stored receipt in ONE
// transaction (a signing or real directory-sync failure rolls the row back,
// leaving at most an orphan byte file).
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

	// Content-addressed duplicate detection is SCOPE-AWARE (v0.7.x hardening):
	// a row with the SAME content address stored under the SAME exact scope is
	// the WORM no-op (created=false — no new row, no receipt); the same content
	// address under a DIFFERENT exact scope is a typed NON-ENUMERATING conflict
	// (OBJECT_SCOPE_CONFLICT) that discloses NO scope metadata — a cross-scope
	// collision is a defect signal, never an oracle. The no-op path still
	// re-checks the WORM invariant: the stored bytes must be present on disk (a
	// row whose file is missing is corruption, fail closed).
	var dupTenant, dupCompany, dupRUC, dupPeriod string
	err = conn.QueryRowContext(ctx, `SELECT tenant_id, company_id, ruc, period FROM evidence_objects WHERE id = ?`, objectID).
		Scan(&dupTenant, &dupCompany, &dupRUC, &dupPeriod)
	if err == nil {
		if dupTenant != input.Scope.OrganizationID || dupCompany != input.Scope.CompanyID ||
			dupRUC != input.Scope.RUC || dupPeriod != input.Scope.Period {
			// Different-scope collision: typed, non-enumerating, no mutation.
			return core.ObjectStoreResult{}, &objectScopeConflictError{
				code: objectErrScopeConflict,
				msg:  "identical object bytes are already stored under a different exact scope — existing scope metadata is withheld",
			}
		}
		// Same-scope duplicate: metadata read on the SAME connection/transaction
		// (the connection is pinned — s.db would deadlock on the held tx).
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

	// WORM byte write (temp + fsync + atomic rename + directory sync). One
	// captured timestamp covers the row AND the receipt (provenance continuity).
	// A real (non-benign) write failure — including a real directory-sync
	// failure — returns BEFORE the row/receipt transaction commits, so the
	// deferred ROLLBACK leaves at most an orphan byte file.
	now := nowISO()
	if err := s.writeObjectBytes(relPath, input.Bytes); err != nil {
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
// forbidden. RECONCILED: missing bytes are the DOCUMENTED EXPECTED ABSENCE (a
// wrapped core.ErrObjectBytesPurgedExpected — NOT corruption) when the
// immutable executions ledger carries a receipt-covered purge authorization
// bound to the object identity (a completed execution or a valid intent row);
// without that authorization missing bytes stay OBJECT_BYTES_MISSING.
func (s *SQLiteStore) VerifyObjectBytes(ctx context.Context, objectID string) error {
	obj, ok := s.EvidenceObjectByID(ctx, objectID)
	if !ok {
		return fmt.Errorf("%s: %s", objectErrNotFound, objectID)
	}
	bytes, err := s.readObjectBytes(obj.RelPath)
	if err != nil {
		var corruption *objectBytesCorruptionError
		if errors.As(err, &corruption) && corruption.code == objectErrBytesMissing {
			authorized, aerr := authorizedPurgeAbsenceOn(ctx, s.db, objectID, obj.Size)
			if aerr != nil {
				return aerr
			}
			if authorized {
				return fmt.Errorf("%s: %w", objectID, core.ErrObjectBytesPurgedExpected)
			}
		}
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

// effectiveObjectsRoot resolves the configured objects root to its canonical
// path. The ROOT itself may be a symlink (an operator-configured mount, e.g.
// --objects); what is forbidden is symlink traversal BELOW the root. A missing
// root is created (0700) only when create is true (write paths); reads against
// a missing root see no bytes (OBJECT_BYTES_MISSING).
func (s *SQLiteStore) effectiveObjectsRoot(create bool) (string, error) {
	root := filepath.Clean(s.objectsRoot)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("persistence error: resolve objects root %s: %w", root, err)
		}
		if !create {
			return "", nil
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", fmt.Errorf("persistence error: create objects root %s: %w", root, err)
		}
		resolved, err = filepath.EvalSymlinks(root)
		if err != nil {
			return "", fmt.Errorf("persistence error: resolve objects root %s: %w", root, err)
		}
	}
	return resolved, nil
}

// objectPathFor resolves a stored rel_path to an absolute path under the
// EFFECTIVE objects root, failing closed on:
//   - a lexical escape of the root (INVALID_OBJECT — a corrupted rel_path is
//     a corruption signal, never a path to follow);
//   - a symlinked or non-directory INTERMEDIATE component (OBJECT_PATH_INVALID
//     — symlink traversal below the root is forbidden; the walk uses Lstat
//     component-by-component from the resolved root, never following a link);
//   - a symlinked or non-regular FINAL component when it exists (reads never
//     follow a symlink; writes never target one blindly).
//
// When create is true the missing intermediate directories are created
// SECURELY — one component at a time with os.Mkdir + Lstat verification, never
// os.MkdirAll (which would follow an attacker-placed intermediate symlink) —
// and the created directories are returned deepest-last for durability syncs.
// When create is false a missing intermediate fails as OBJECT_BYTES_MISSING
// (no WORM bytes can exist under a missing path). The residual TOCTOU window
// (a component swapped between the Lstat walk and the open) is documented in
// docs/security/evidence-lifecycle-and-threat-model.md: closing it would
// require openat2/O_NOFOLLOW semantics, which are not portable in Go; host
// access is the effective trust boundary.
func (s *SQLiteStore) objectPathFor(relPath string, create bool) (string, []string, error) {
	// Lexical containment against the CONFIGURED root first: fail closed even
	// when the root does not exist yet — a corrupt rel_path is never probed.
	cleanRoot := filepath.Clean(s.objectsRoot)
	if full := filepath.Clean(filepath.Join(cleanRoot, relPath)); full != cleanRoot &&
		!strings.HasPrefix(full, cleanRoot+string(filepath.Separator)) {
		return "", nil, &objectBytesCorruptionError{code: objectErrInvalid,
			msg: fmt.Sprintf("rel_path %q escapes the objects root — corruption", relPath)}
	}
	root, err := s.effectiveObjectsRoot(create)
	if err != nil {
		return "", nil, err
	}
	if root == "" {
		return "", nil, &objectBytesCorruptionError{code: objectErrBytesMissing,
			msg: "objects root does not exist — no WORM bytes can be present"}
	}
	full := filepath.Clean(filepath.Join(root, relPath))
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", nil, &objectBytesCorruptionError{code: objectErrInvalid,
			msg: fmt.Sprintf("rel_path %q escapes the objects root — corruption", relPath)}
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", nil, &objectBytesCorruptionError{code: objectErrInvalid,
			msg: fmt.Sprintf("rel_path %q is not under the objects root — corruption", relPath)}
	}
	if rel == "." {
		return full, nil, nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	cur := root
	var created []string
	for _, part := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, part)
		fi, lerr := os.Lstat(cur)
		if lerr != nil {
			if os.IsNotExist(lerr) && create {
				// The parent (cur's dirname) was verified a real directory in the
				// previous iteration; os.Mkdir creates ONE component without
				// traversing any intermediate symlink.
				if merr := os.Mkdir(cur, 0o700); merr != nil {
					return "", nil, fmt.Errorf("persistence error: create object path component %s: %w", cur, merr)
				}
				created = append(created, cur)
				fi, lerr = os.Lstat(cur)
			}
			if lerr != nil {
				if os.IsNotExist(lerr) {
					return "", nil, &objectBytesCorruptionError{code: objectErrBytesMissing,
						msg: fmt.Sprintf("WORM path component %s is missing (row without bytes — corruption, no silent repair)", cur)}
				}
				return "", nil, fmt.Errorf("persistence error: stat object path component %s: %w", cur, lerr)
			}
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", nil, &objectPathCorruptionError{code: objectErrPathInvalid,
				msg: fmt.Sprintf("WORM path component %s is a symlink — symlink traversal below the objects root is forbidden", cur)}
		}
		if !fi.IsDir() {
			return "", nil, &objectPathCorruptionError{code: objectErrPathInvalid,
				msg: fmt.Sprintf("WORM path component %s is not a directory — corruption", cur)}
		}
	}
	// Final component: when it exists it must be a REGULAR file, never a
	// symlink (a read must not follow one; a write replacing a leftover orphan
	// via rename replaces the directory entry itself, never its target).
	fi, ferr := os.Lstat(full)
	if ferr == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", nil, &objectPathCorruptionError{code: objectErrPathInvalid,
				msg: fmt.Sprintf("WORM bytes %s are a symlink — symlink traversal below the objects root is forbidden", full)}
		}
		if !fi.Mode().IsRegular() {
			return "", nil, &objectPathCorruptionError{code: objectErrPathInvalid,
				msg: fmt.Sprintf("WORM bytes %s are not a regular file — corruption", full)}
		}
	} else if !os.IsNotExist(ferr) {
		return "", nil, fmt.Errorf("persistence error: stat object bytes %s: %w", full, ferr)
	}
	return full, created, nil
}

// readObjectBytes reads the stored bytes of one object by its rel_path; a
// missing file fails closed (OBJECT_BYTES_MISSING — never recreated, never
// repaired) and a symlinked/escaped path fails closed (OBJECT_PATH_INVALID /
// INVALID_OBJECT — never followed).
func (s *SQLiteStore) readObjectBytes(relPath string) ([]byte, error) {
	full, _, err := s.objectPathFor(relPath, false)
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
// or on an invalid/symlinked path (the duplicate-no-op path and doctor re-check
// the WORM invariant cheaply).
func (s *SQLiteStore) assertObjectBytesPresent(relPath string) error {
	full, _, err := s.objectPathFor(relPath, false)
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

// objectBytesExist reports whether the object's exact content-addressed bytes are
// present (read-only stat through the SAME path-containment defenses as reads and
// writes; an invalid/symlinked path is a typed failure, never a silent no).
func (s *SQLiteStore) objectBytesExist(relPath string) (bool, error) {
	full, _, err := s.objectPathFor(relPath, false)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(full); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("persistence error: stat object bytes: %w", err)
	}
	return true, nil
}

// authorizedPurgeAbsenceOn reports whether the object's missing bytes are the
// DOCUMENTED EXPECTED ABSENCE of the physical purge lifecycle instead of
// corruption: TRUE when the immutable executions ledger carries a receipt-covered
// purge authorization bound to the EXACT object identity — a completed execution
// (bytes were removed and the completion committed) or a valid 'intent' row whose
// frozen pre-removal hash and size equal the object's immutable identity (a crash
// between the durable intent and the completion — the recovery window). A stale
// 'interrupted'-only history is NOT an authorization (no intent is live), so
// missing bytes without a live intent or a completion remain the typed
// OBJECT_BYTES_MISSING integrity incident. Runs on the caller's Queryer (read-only).
func authorizedPurgeAbsenceOn(ctx context.Context, q Queryer, objectID string, size int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM evidence_purge_executions
		WHERE object_id = ? AND pre_removal_hash = ? AND size = ? AND state IN ('intent','completed')`,
		objectID, objectID, size,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("persistence error: read purge execution authorization: %w", err)
	}
	return n > 0, nil
}

// purgeAbsenceAuthorization is ONE executions-ledger row that authorizes the
// DOCUMENTED EXPECTED ABSENCE of an object's bytes (design §3.7/§13.2): the
// exact execution identity, its request, the guarded state and the completion
// receipt hash (empty for a live 'intent' — the crash recovery window).
type purgeAbsenceAuthorization struct {
	ExecutionID         string
	RequestID           string
	State               string
	CompletionReceiptID string
}

// purgeAbsenceAuthorizationsOn returns EVERY receipt-covered purge
// authorization bound to the object's EXACT immutable identity (object_id =
// pre_removal_hash = the content address AND the recorded size — never a
// tenant- or request-scoped wildcard): a completed execution (the bytes were
// removed and the completion committed — documented purge) or a live 'intent'
// execution (a crash between the durable intent and the completion — the
// recovery window). A stale 'interrupted'-only history is NOT an authorization
// (no intent is live and no completion exists): missing bytes without any
// returned row stay the typed OBJECT_BYTES_MISSING integrity incident. Runs on
// the caller's Queryer (read-only). The doctor surfaces every returned row as
// an auditable finding (§13.3); the verification path only needs the existence
// check (authorizedPurgeAbsenceOn).
func purgeAbsenceAuthorizationsOn(ctx context.Context, q Queryer, objectID string, size int64) ([]purgeAbsenceAuthorization, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT execution_id, request_id, state, COALESCE(completion_receipt_id, '')
		FROM evidence_purge_executions
		WHERE object_id = ? AND pre_removal_hash = ? AND size = ? AND state IN ('intent','completed')
		ORDER BY intent_at, execution_id`,
		objectID, objectID, size,
	)
	if err != nil {
		return nil, fmt.Errorf("persistence error: read purge execution authorizations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []purgeAbsenceAuthorization
	for rows.Next() {
		var a purgeAbsenceAuthorization
		if err := rows.Scan(&a.ExecutionID, &a.RequestID, &a.State, &a.CompletionReceiptID); err != nil {
			return nil, fmt.Errorf("persistence error: scan purge execution authorization: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("persistence error: read purge execution authorizations: %w", err)
	}
	return out, nil
}

// documentedAbsenceObjectFinding builds the auditable object-layer finding for
// a RECEIPT-COVERED documented byte absence (design §13.2 outcome 2 / §13.3):
// the exact object whose bytes are gone plus the authorizing execution — a
// completed execution is a documented purge; a live 'intent' execution is the
// recovery window. REPORTED, never a corruption finding, never repaired.
func documentedAbsenceObjectFinding(auth purgeAbsenceAuthorization, objectID, relPath string) ObjectDoctorFinding {
	kind, detail := objectFindingDocumentedIntent,
		fmt.Sprintf("documented expected absence: durable purge intent %s covers the missing bytes (crash between the authorized unlink and the completion — recovery window, never corruption)", auth.ExecutionID)
	if auth.State == string(core.PurgeExecutionCompleted) {
		kind = objectFindingDocumentedPurge
		detail = fmt.Sprintf("documented expected absence: receipt-covered completion of execution %s removed the bytes", auth.ExecutionID)
	}
	if auth.CompletionReceiptID != "" {
		detail += fmt.Sprintf(" (completion receipt %s)", auth.CompletionReceiptID)
	}
	return ObjectDoctorFinding{
		Kind:     kind,
		ObjectID: objectID,
		RelPath:  relPath,
		Detail:   detail,
	}
}

// removeObjectBytes is the ONLY byte-removal primitive of the object layer
// (v0.8 execution — design §11 step 2): it removes the exact WORM bytes of
// ONE object by its stored rel_path. It (a) resolves the exact
// content-addressed path through objectPathFor — the SAME path-containment /
// symlink defenses as reads and writes, so a corrupt rel_path never resolves
// and symlink traversal below the objects root is impossible; (b) reads the
// bytes and RECOMPUTES the content address, requiring it to equal the
// immutable object id AND the recorded size (a mismatch ABORTS — the engine
// never deletes bytes that do not match the recorded identity, no silent
// repair); and (c) unlinks ONLY that exact file and fsyncs its directory. No
// wildcards, no directory or parent-directory removals, no repair. The caller
// owns the two-phase intent/completion protocol (the executions row, the
// purge_intent/purge_executed events and receipts); this primitive never
// touches SQL and never runs inside a SQL transaction.
func (s *SQLiteStore) removeObjectBytes(relPath, objectID string, expectedSize int64) error {
	full, _, err := s.objectPathFor(relPath, false)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return &objectBytesCorruptionError{code: objectErrBytesMissing,
				msg: fmt.Sprintf("WORM bytes %s are missing (row without file — corruption, no silent repair, nothing unlinked)", full)}
		}
		return fmt.Errorf("persistence error: read object bytes before removal: %w", err)
	}
	if got := core.ComputeObjectID(data); got != objectID {
		return &objectBytesCorruptionError{code: objectErrHashMismatch,
			msg: fmt.Sprintf("stored bytes of %s re-hash to %s — the purge execution ABORTS and nothing is unlinked (corruption, no silent repair)", objectID, got)}
	}
	if int64(len(data)) != expectedSize {
		return &objectBytesCorruptionError{code: objectErrHashMismatch,
			msg: fmt.Sprintf("stored bytes of %s have size %d but the recorded size is %d — the purge execution ABORTS and nothing is unlinked (corruption, no silent repair)", objectID, len(data), expectedSize)}
	}
	if err := os.Remove(full); err != nil {
		return fmt.Errorf("persistence error: unlink object bytes %s: %w", full, err)
	}
	if err := syncDirectory(filepath.Dir(full)); err != nil {
		return err
	}
	return nil
}

// writeObjectBytes performs the durable WORM write (v0.7.x hardening): secure
// directory creation (component-by-component, never following a symlink), temp
// file in the SAME directory, write + fsync, atomic rename into place, then a
// directory fsync of the target directory and any newly created ancestors
// (deepest first). A REAL directory-sync failure — one outside the documented
// unsupported set — is returned so the caller rolls back metadata/receipt,
// leaving at most an orphan byte file (content-addressed, unreferenced, never
// served). Rename atomically replaces a leftover orphan (identical
// content-addressed bytes from a previously rolled-back transaction).
func (s *SQLiteStore) writeObjectBytes(relPath string, data []byte) error {
	full, createdDirs, err := s.objectPathFor(relPath, true)
	if err != nil {
		return err
	}
	dir := filepath.Dir(full)
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
	// Durability: the renamed entry must survive a crash. The target directory
	// first, then the newly created ancestors deepest-first (their own
	// creation must be durable for the file to be reachable after a crash).
	if err := syncDirectory(dir); err != nil {
		return err
	}
	for i := len(createdDirs) - 1; i >= 0; i-- {
		if err := syncDirectory(createdDirs[i]); err != nil {
			return err
		}
	}
	return nil
}

// syncDirectory fsyncs a directory so a renamed/created entry survives a
// crash. A REAL sync failure (one outside the documented unsupported set) is
// returned so the caller rolls back metadata/receipts; unsupported-filesystem
// errors — EINVAL / ENOTSUP / EOPNOTSUPP / ENOSYS / EBADF on platforms that
// cannot fsync a directory, plus Windows' inability to open a directory at all
// — are documented and tolerated: the rename itself is already atomic on
// POSIX, so only crash-durability of the directory entry is at risk, never
// byte integrity.
func syncDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // the directory vanished concurrently — nothing to sync
		}
		if runtime.GOOS == "windows" {
			return nil // Windows cannot open a directory for fsync — documented unsupported
		}
		return fmt.Errorf("persistence error: open objects directory %s for sync: %w", dir, err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		if isUnsupportedDirSyncError(err) {
			return nil // documented unsupported-filesystem error — tolerated
		}
		return fmt.Errorf("persistence error: sync objects directory %s: %w", dir, err)
	}
	return nil
}

// isUnsupportedDirSyncError classifies the portable set of directory-fsync
// errors that are tolerated (documented unsupported-filesystem behavior).
// Everything else — EIO, EROFS, EACCES, … — is a REAL durability failure and
// rolls the write transaction back.
func isUnsupportedDirSyncError(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOSYS) ||
		errors.Is(err, syscall.EBADF)
}

// ──────────────────────────────────────────────
// Doctor object-layer scan (v0.7.x hardening)
// ──────────────────────────────────────────────

// ObjectDoctorFinding classifies ONE object-layer anomaly the doctor surface
// reports. missing_bytes and invalid_path anomalies FAIL CLOSED instead
// (Doctor returns an error — corruption is evidence, never a skip); the
// reported kinds below are orphan_file and temp_file (plus invalid_path for
// stray files outside the layout) and the DOCUMENTED EXPECTED ABSENCE of the
// physical purge lifecycle (documented_purge / documented_intent — see
// purgeAbsenceAuthorizationsOn): a receipt-covered completion or a live durable
// intent explains the missing bytes, so they are REPORTED as auditable
// findings, never a corruption finding, never repaired. The doctor NEVER
// deletes or repairs anything: findings are read-only evidence.
type ObjectDoctorFinding struct {
	Kind     string `json:"kind"` // orphan_file | temp_file | invalid_path | documented_purge | documented_intent
	ObjectID string `json:"objectId,omitempty"`
	RelPath  string `json:"relPath,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Object doctor finding kinds reported by the object-layer scan.
const (
	objectFindingInvalidPath = "invalid_path"
	objectFindingOrphanFile  = "orphan_file"
	objectFindingTempFile    = "temp_file"
	// objectFindingDocumentedPurge: the object's bytes are absent AND a
	// receipt-covered COMPLETED purge execution bound to the exact object
	// identity removed them (design §13.2 outcome 2) — expected, auditable.
	objectFindingDocumentedPurge = "documented_purge"
	// objectFindingDocumentedIntent: the object's bytes are absent AND a LIVE
	// durable 'intent' execution covers them (a crash between the authorized
	// unlink and the completion — the recovery window) — expected, auditable.
	objectFindingDocumentedIntent = "documented_intent"
)

// doctorObjectScan audits the WORM object layer for the doctor surface
// (v0.7.x hardening):
//   - every evidence_objects row must resolve to a VALID path whose bytes are
//     PRESENT — a missing byte file or an invalid/symlinked path FAILS CLOSED
//     (the report cannot be built: corruption is evidence, never a skip);
//     RECONCILED: missing bytes are the documented EXPECTED ABSENCE of the
//     physical purge lifecycle (design §13.2/§13.3 — REPORTED as an auditable
//     finding documented_purge / documented_intent, never a corruption finding)
//     when the immutable executions ledger carries a receipt-covered purge
//     authorization bound to the object identity (a completed execution or a
//     valid intent row — see purgeAbsenceAuthorizationsOn);
//   - every byte file under the objects root is classified and REPORTED:
//     orphan_file (valid content-addressed path with no row), temp_file
//     (leftover .tmp-*), invalid_path (outside the content-addressed layout
//     or a symlink).
//
// The scan is strictly READ-ONLY: nothing is deleted, moved or repaired.
func (s *SQLiteStore) doctorObjectScan() ([]ObjectDoctorFinding, error) {
	rows, err := s.db.Query(`SELECT id, rel_path, size FROM evidence_objects`)
	if err != nil {
		return nil, fmt.Errorf("corrupt store: read evidence_objects: %w", err)
	}
	rowByID := make(map[string]string) // object id → rel_path
	rowSizes := make(map[string]int64) // object id → recorded size
	for rows.Next() {
		var id, relPath string
		var size int64
		if err := rows.Scan(&id, &relPath, &size); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("corrupt store: scan evidence_objects: %w", err)
		}
		rowByID[id] = relPath
		rowSizes[id] = size
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("corrupt store: read evidence_objects: %w", err)
	}
	_ = rows.Close()

	var findings []ObjectDoctorFinding

	// Rows: path validity + byte presence. Missing bytes FAIL CLOSED as corruption
	// UNLESS a receipt-covered purge authorization explains them (the documented
	// expected absence — a purged object or a live purge intent); the documented
	// absences are REPORTED as auditable findings (documented_purge /
	// documented_intent), never silently skipped; invalid paths always fail
	// closed.
	for id, relPath := range rowByID {
		if err := s.assertObjectBytesPresent(relPath); err != nil {
			var corruption *objectBytesCorruptionError
			if errors.As(err, &corruption) && corruption.code == objectErrBytesMissing {
				authorizations, aerr := purgeAbsenceAuthorizationsOn(context.Background(), s.db, id, rowSizes[id])
				if aerr != nil {
					return nil, aerr
				}
				if len(authorizations) > 0 {
					// Documented expected absence (design §13.2/§13.3): the bytes
					// were removed by a receipt-covered completion or a live durable
					// intent covers the crash window — an auditable finding, never a
					// corruption finding, never repaired.
					for _, auth := range authorizations {
						findings = append(findings, documentedAbsenceObjectFinding(auth, id, relPath))
					}
					continue
				}
			}
			return nil, fmt.Errorf("corrupt store: evidence object %s: %w", id, err)
		}
	}

	root, err := s.effectiveObjectsRoot(false)
	if err != nil {
		return nil, err
	}
	if root == "" {
		// No root exists: any row above either failed closed (its bytes cannot
		// exist without a root) or is a documented purge absence already reported
		// above — keep those auditable findings.
		return findings, nil
	}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // a subtree vanished mid-walk — not an object anomaly
			}
			return err // an unreadable subtree — an incomplete report is not a report
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if d.Type()&fs.ModeSymlink != 0 {
			// A symlink below the objects root is never followed: report it as an
			// invalid path (a row referencing it would have failed closed above).
			findings = append(findings, ObjectDoctorFinding{
				Kind:    objectFindingInvalidPath,
				RelPath: objectRelToRoot(root, path),
				Detail:  "symlink below the objects root — never followed, never repaired",
			})
			return nil
		}
		if strings.HasPrefix(name, ".tmp-") {
			findings = append(findings, ObjectDoctorFinding{
				Kind:    objectFindingTempFile,
				RelPath: objectRelToRoot(root, path),
				Detail:  "leftover temp file from an interrupted or failed write — never served, report only",
			})
			return nil
		}
		if id, ok := objectIDFromBytePath(root, path); ok {
			if _, known := rowByID[id]; !known {
				findings = append(findings, ObjectDoctorFinding{
					Kind:     objectFindingOrphanFile,
					ObjectID: id,
					RelPath:  objectRelToRoot(root, path),
					Detail:   "content-addressed byte file with no evidence_objects row — unreferenced, never served, never deleted",
				})
			}
			return nil
		}
		findings = append(findings, ObjectDoctorFinding{
			Kind:    objectFindingInvalidPath,
			RelPath: objectRelToRoot(root, path),
			Detail:  "byte file outside the content-addressed layout — report only, never followed",
		})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("corrupt store: scan objects root %s: %w", root, walkErr)
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].RelPath < findings[j].RelPath })
	return findings, nil
}

// objectIDFromBytePath reports whether path is a byte file at a VALID
// content-addressed path under root: objects/<ab>/<cd>/<sha> with sha the
// 64-lowercase-hex basename and <ab>/<cd> its first two byte-pairs.
func objectIDFromBytePath(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 4 || parts[0] != "objects" {
		return "", false
	}
	id := parts[3]
	if len(id) != 64 || parts[1] != id[0:2] || parts[2] != id[2:4] {
		return "", false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return id, true
}

// objectRelToRoot returns the root-relative form of path for doctor findings
// (the content-addressed layout form, portable across OS separators).
func objectRelToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
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
