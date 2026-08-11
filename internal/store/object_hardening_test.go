// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.7.x app-level
// WORM hardening of the LOCAL evidence-object store
// (internal/store/object_store.go, docs/architecture/evidence-object-v0.7.md
// §Hardening):
//
//   - symlink traversal below the objects root FAILS CLOSED on reads, writes
//     and doctor (OBJECT_PATH_INVALID; skip-when-unsupported on platforms
//     without symlinks);
//   - a write is temp + fsync + atomic rename + directory sync; a
//     wrong-root write failure ROLLS BACK metadata/receipts (at most an orphan
//     byte file); the portable unsupported dir-sync set is tolerated;
//   - duplicates are SCOPE-AWARE: the same content address under a DIFFERENT
//     exact scope is a typed NON-ENUMERATING OBJECT_SCOPE_CONFLICT (no scope
//     metadata in the message); the same-scope duplicate stays a NO-OP, also
//     under a concurrent same-scope race (exactly one created);
//   - doctor reports orphan_file / temp_file / invalid_path findings and NEVER
//     deletes or repairs; rows with missing bytes or invalid paths FAIL
//     CLOSED;
//   - hardlinks, corrupt bytes, and permission failures fail closed where the
//     OS supports them.
package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// newTestStoreWithRoot opens a store at a fresh temp DB with an EXPLICIT
// objects root (the hardening tests drive custom-root scenarios).
func newTestStoreWithRoot(t *testing.T, root string) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engram.db")
	s, err := OpenWithObjects(path, root)
	if err != nil {
		t.Fatalf("open store with root %s: %v", root, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// openTestStoreRootPath opens an INDEPENDENT store at an explicit path + root
// (concurrency tests open two stores against ONE WAL database file and ONE
// objects root).
func openTestStoreRootPath(t *testing.T, path, root string) *SQLiteStore {
	t.Helper()
	s, err := OpenWithObjects(path, root)
	if err != nil {
		t.Fatalf("open store at %s: %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// objectBytesWithSameBucket returns bytes DIFFERENT from original whose
// content address shares original's first four hex chars — so a second store
// targets the SAME bucket directory as the first object (the symlink-write
// test needs the collision).
func objectBytesWithSameBucket(t *testing.T, original []byte) []byte {
	t.Helper()
	prefix := core.ComputeObjectID(original)[:4]
	for i := 0; ; i++ {
		cand := []byte(fmt.Sprintf("bucket-collision-%d", i))
		if bytes.Equal(cand, original) {
			continue
		}
		if core.ComputeObjectID(cand)[:4] == prefix {
			return cand
		}
	}
}

// TestObjectSymlinkTraversalFailsClosed proves symlinked intermediate and
// final components below the objects root are NEVER followed: reads,
// VerifyObjectBytes and doctor fail closed with OBJECT_PATH_INVALID, a write
// targeting the symlinked bucket fails closed with NO row/receipt and nothing
// landing outside the root, and a final-component symlink never leaks the
// decoy bytes. Platforms without symlinks skip (portable adversarial test).
func TestObjectSymlinkTraversalFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "objects")
	s := newTestStoreWithRoot(t, root)
	result, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err != nil {
		t.Fatalf("store object: %v", err)
	}
	id := result.Object.ObjectID
	cd := filepath.Dir(filepath.Join(root, result.Object.RelPath))

	t.Run("intermediate symlink read fails closed", func(t *testing.T) {
		outside := t.TempDir()
		if err := os.RemoveAll(cd); err != nil {
			t.Fatalf("remove bucket dir: %v", err)
		}
		if err := os.Symlink(outside, cd); err != nil {
			t.Skipf("symlinks unsupported on this platform/OS: %v", err)
		}
		if _, _, err := s.GetObject(context.Background(), id, testScope(testRucA)); err == nil ||
			!strings.Contains(err.Error(), "OBJECT_PATH_INVALID") {
			t.Fatalf("read through symlinked bucket dir error = %v, want OBJECT_PATH_INVALID", err)
		}
		if err := s.VerifyObjectBytes(context.Background(), id); err == nil ||
			!strings.Contains(err.Error(), "OBJECT_PATH_INVALID") {
			t.Fatalf("VerifyObjectBytes through symlink = %v, want OBJECT_PATH_INVALID", err)
		}
		if _, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine}); err == nil || !strings.Contains(err.Error(), "OBJECT_PATH_INVALID") {
			t.Fatalf("doctor with symlinked bucket error = %v, want OBJECT_PATH_INVALID (fail closed)", err)
		}
		entries, err := os.ReadDir(outside)
		if err != nil {
			t.Fatalf("read outside dir: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("read followed the symlink outside the root: %v", entries)
		}
	})

	t.Run("intermediate symlink write fails closed", func(t *testing.T) {
		outside := t.TempDir()
		if err := os.RemoveAll(cd); err != nil {
			t.Fatalf("remove bucket dir: %v", err)
		}
		if err := os.Symlink(outside, cd); err != nil {
			t.Skipf("symlinks unsupported on this platform/OS: %v", err)
		}
		// Same bucket dir as the stored object, different bytes.
		other := objectBytesWithSameBucket(t, []byte("test"))
		otherID := core.ComputeObjectID(other)
		if _, err := s.StoreObject(context.Background(), objectInputForTest(t, other)); err == nil ||
			!strings.Contains(err.Error(), "OBJECT_PATH_INVALID") {
			t.Fatalf("write through symlinked bucket dir error = %v, want OBJECT_PATH_INVALID", err)
		}
		if _, ok := s.EvidenceObjectByID(context.Background(), otherID); ok {
			t.Fatal("failed write must leave no evidence_objects row")
		}
		if n := storedObjectReceiptCount(t, s, otherID); n != 0 {
			t.Fatalf("failed write must mint no receipt, count = %d", n)
		}
		entries, err := os.ReadDir(outside)
		if err != nil {
			t.Fatalf("read outside dir: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("write followed the symlink outside the root: %v", entries)
		}
	})

	t.Run("final symlink read fails closed", func(t *testing.T) {
		root2 := filepath.Join(t.TempDir(), "objects")
		s2 := newTestStoreWithRoot(t, root2)
		res, err := s2.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
		if err != nil {
			t.Fatalf("store object: %v", err)
		}
		full := filepath.Join(root2, res.Object.RelPath)
		decoy := filepath.Join(t.TempDir(), "decoy")
		if err := os.WriteFile(decoy, []byte("decoy bytes"), 0o600); err != nil {
			t.Fatalf("write decoy: %v", err)
		}
		if err := os.Remove(full); err != nil {
			t.Fatalf("remove byte file: %v", err)
		}
		if err := os.Symlink(decoy, full); err != nil {
			t.Skipf("symlinks unsupported on this platform/OS: %v", err)
		}
		_, bytes, err := s2.GetObject(context.Background(), res.Object.ObjectID, testScope(testRucA))
		if err == nil || !strings.Contains(err.Error(), "OBJECT_PATH_INVALID") {
			t.Fatalf("read of a symlinked byte file = %v (bytes %q), want OBJECT_PATH_INVALID — never followed", err, bytes)
		}
		if _, err := s2.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine}); err == nil || !strings.Contains(err.Error(), "OBJECT_PATH_INVALID") {
			t.Fatalf("doctor with symlinked byte file error = %v, want OBJECT_PATH_INVALID", err)
		}
	})
}

// TestObjectWrongRootWriteFailureRollback proves a failed WORM write ROLLS
// BACK the metadata/receipt transaction: no evidence_objects row, no
// object_stored receipt, at most an orphan byte file. Two portable failure
// modes: the objects root path occupied by a regular file, and a read-only
// root (Unix permission support).
func TestObjectWrongRootWriteFailureRollback(t *testing.T) {
	t.Run("root path occupied by a file", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "objects")
		if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("plant file-as-root: %v", err)
		}
		s := newTestStoreWithRoot(t, root)
		if _, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test"))); err == nil {
			t.Fatal("write into a file-as-root must fail")
		}
		id := core.ComputeObjectID([]byte("test"))
		if _, ok := s.EvidenceObjectByID(context.Background(), id); ok {
			t.Fatal("rollback: failed write must leave no evidence_objects row")
		}
		if n := storedObjectReceiptCount(t, s, id); n != 0 {
			t.Fatalf("rollback: failed write must mint no receipt, count = %d", n)
		}
	})

	t.Run("read-only root rolls back", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("permission bits not enforced on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("permission checks are meaningless as root")
		}
		root := filepath.Join(t.TempDir(), "objects")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatalf("create root: %v", err)
		}
		if err := os.Chmod(root, 0o500); err != nil {
			t.Fatalf("chmod root read-only: %v", err)
		}
		defer func() { _ = os.Chmod(root, 0o700) }()
		s := newTestStoreWithRoot(t, root)
		if _, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test"))); err == nil {
			t.Fatal("write into a read-only root must fail")
		}
		id := core.ComputeObjectID([]byte("test"))
		if _, ok := s.EvidenceObjectByID(context.Background(), id); ok {
			t.Fatal("rollback: failed write must leave no evidence_objects row")
		}
		if n := storedObjectReceiptCount(t, s, id); n != 0 {
			t.Fatalf("rollback: failed write must mint no receipt, count = %d", n)
		}
	})
}

// TestDoctorReportsOrphanAndTempFilesWithoutRepair proves doctor REPORTED
// findings: a content-addressed byte file with no row (orphan_file) and a
// leftover .tmp-* file (temp_file) appear in the report and are NEVER deleted
// or repaired — the doctor is read-only evidence.
func TestDoctorReportsOrphanAndTempFilesWithoutRepair(t *testing.T) {
	root := filepath.Join(t.TempDir(), "objects")
	s := newTestStoreWithRoot(t, root)
	if _, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test"))); err != nil {
		t.Fatalf("store object: %v", err)
	}

	orphanID := strings.Repeat("ab", 32)
	orphanPath := filepath.Join(root, "objects", orphanID[0:2], orphanID[2:4], orphanID)
	if err := os.MkdirAll(filepath.Dir(orphanPath), 0o700); err != nil {
		t.Fatalf("create orphan dir: %v", err)
	}
	if err := os.WriteFile(orphanPath, []byte("stray bytes"), 0o600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	tmpPath := filepath.Join(root, "objects", "aa", ".tmp-leftover")
	if err := os.MkdirAll(filepath.Dir(tmpPath), 0o700); err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	if err := os.WriteFile(tmpPath, []byte("partial"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	report, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
	if err != nil {
		t.Fatalf("orphan/temp files must be REPORTED, never fail the doctor: %v", err)
	}
	if report.ObjectsRoot != root {
		t.Fatalf("objectsRoot = %q, want the configured root %q", report.ObjectsRoot, root)
	}
	if report.EvidenceObjects != 1 {
		t.Fatalf("evidenceObjects = %d, want 1", report.EvidenceObjects)
	}
	var orphanFound, tempFound bool
	for _, f := range report.ObjectFindings {
		if f.Kind == objectFindingOrphanFile && f.ObjectID == orphanID {
			orphanFound = true
		}
		if f.Kind == objectFindingTempFile && strings.HasSuffix(f.RelPath, ".tmp-leftover") {
			tempFound = true
		}
	}
	if !orphanFound || !tempFound {
		t.Fatalf("findings = %+v, want orphan_file %s and temp_file .tmp-leftover", report.ObjectFindings, orphanID)
	}

	// The doctor NEVER repairs: both files survive the scan.
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("orphan must survive the doctor (no deletion): %v", err)
	}
	if _, err := os.Stat(tmpPath); err != nil {
		t.Fatalf("temp file must survive the doctor (no deletion): %v", err)
	}
}

// TestDoctorMissingBytesFailsClosed proves a row whose byte file is missing
// FAILS CLOSED the doctor (corruption is evidence — the report is not built,
// never a reported-and-skipped anomaly).
func TestDoctorMissingBytesFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "objects")
	s := newTestStoreWithRoot(t, root)
	result, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err != nil {
		t.Fatalf("store object: %v", err)
	}
	full := filepath.Join(root, result.Object.RelPath)
	if err := os.Remove(full); err != nil {
		t.Fatalf("remove byte file: %v", err)
	}
	if _, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine}); err == nil || !strings.Contains(err.Error(), "OBJECT_BYTES_MISSING") {
		t.Fatalf("doctor with missing bytes error = %v, want OBJECT_BYTES_MISSING (fail closed)", err)
	}
}

// TestDoctorInvalidRowPathFailsClosed proves a row whose rel_path escapes the
// objects root FAILS CLOSED the doctor (INVALID_OBJECT — a corrupted path is
// never followed).
func TestDoctorInvalidRowPathFailsClosed(t *testing.T) {
	s := newTestStore(t)
	evilID := strings.Repeat("ef", 32)
	if _, err := s.db.Exec(`INSERT INTO evidence_objects
		(id, sha256, size, content_type, tenant_id, company_id, ruc, period,
		 source_system, source_reference, source_actor_id, source_actor_kind,
		 stored_by, stored_at, rel_path)
		VALUES (?, ?, 0, '', 'evil', 'evil', '20100039201', '202401',
			'evil', '', 'evil', 'agent', 'evil', '2026-01-01T00:00:00Z', ?)`,
		evilID, evilID, "../escape"); err != nil {
		t.Fatalf("plant escaping row: %v", err)
	}
	if _, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine}); err == nil || !strings.Contains(err.Error(), "INVALID_OBJECT") {
		t.Fatalf("doctor with escaping rel_path error = %v, want INVALID_OBJECT (fail closed)", err)
	}
}

// TestStoreObjectCrossScopeConflictNonEnumerating proves the scope-aware
// duplicate contract: identical bytes under the SAME exact scope stay a NO-OP,
// while the same content address under a DIFFERENT tenant/company/RUC/period
// is a typed OBJECT_SCOPE_CONFLICT whose message carries NO scope value (the
// cross-scope collision is a defect signal, never an oracle), with no second
// row and no second receipt.
func TestStoreObjectCrossScopeConflictNonEnumerating(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	bytes := []byte("identical bytes")
	objectID := core.ComputeObjectID(bytes)
	inA := objectInputForTest(t, bytes)
	if result, err := s.StoreObject(context.Background(), inA); err != nil || !result.Created {
		t.Fatalf("first store = %+v, %v — want created", result, err)
	}

	dup, err := s.StoreObject(context.Background(), inA)
	if err != nil || dup.Created {
		t.Fatalf("same-scope duplicate = %+v, %v — want NO-OP", dup, err)
	}

	conflicts := []core.Scope{
		testScope(testRucB), // different RUC
		{Kind: core.ScopeKindCompany, OrganizationID: "org-other", CompanyID: "acme", RUC: testRucA, Period: testPeriod},
		{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, CompanyID: "other-co", RUC: testRucA, Period: testPeriod},
		{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, CompanyID: "acme", RUC: testRucA, Period: "202402"},
	}
	scopeValues := []string{testOrgID, "acme", testRucA, testRucB, testPeriod, "org-other", "other-co", "202402"}
	for i, sc := range conflicts {
		in := inA
		in.Scope = sc
		_, err := s.StoreObject(context.Background(), in)
		if err == nil || !strings.Contains(err.Error(), "OBJECT_SCOPE_CONFLICT") {
			t.Fatalf("cross-scope case %d error = %v, want OBJECT_SCOPE_CONFLICT", i, err)
		}
		for _, leak := range scopeValues {
			if strings.Contains(err.Error(), leak) {
				t.Fatalf("cross-scope case %d error leaks scope value %q: %v", i, leak, err)
			}
		}
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM evidence_objects WHERE id = ?`, objectID); n != 1 {
		t.Fatalf("evidence_objects rows = %d, want exactly 1", n)
	}
	if n := storedObjectReceiptCount(t, s, objectID); n != 1 {
		t.Fatalf("object_stored receipts = %d, want exactly 1", n)
	}
}

// TestStoreObjectConcurrentSameScopeRace races the SAME bytes + SAME exact
// scope through two INDEPENDENT stores against one WAL file and one objects
// root: exactly ONE created and ONE no-op (the BEGIN IMMEDIATE duplicate check
// serializes), with exactly one row and one receipt.
func TestStoreObjectConcurrentSameScopeRace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "engram.db")
	root := filepath.Join(dir, "objects")
	s1 := openTestStoreRootPath(t, path, root)
	s2 := openTestStoreRootPath(t, path, root)
	s1.SetReceiptSigner(newParitySigner(s1))
	s2.SetReceiptSigner(newParitySigner(s2))

	start := make(chan struct{})
	type outcome struct {
		res core.ObjectStoreResult
		err error
	}
	outs := make([]outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			st := s1
			if i == 1 {
				st = s2
			}
			res, err := st.StoreObject(context.Background(), objectInputForTest(t, []byte("race bytes")))
			outs[i] = outcome{res, err}
		}(i)
	}
	close(start)
	wg.Wait()

	created, noop := 0, 0
	for _, o := range outs {
		if o.err != nil {
			t.Fatalf("same-scope race error: %v", o.err)
		}
		if o.res.Created {
			created++
		} else {
			noop++
		}
	}
	if created != 1 || noop != 1 {
		t.Fatalf("created=%d noop=%d, want exactly one created and one NO-OP", created, noop)
	}
	objectID := core.ComputeObjectID([]byte("race bytes"))
	if n := countRows(t, s1, `SELECT COUNT(*) FROM evidence_objects WHERE id = ?`, objectID); n != 1 {
		t.Fatalf("evidence_objects rows = %d, want exactly 1", n)
	}
	if n := storedObjectReceiptCount(t, s1, objectID); n != 1 {
		t.Fatalf("object_stored receipts = %d, want exactly 1", n)
	}
}

// TestStoreObjectConcurrentCrossScopeRace races the SAME bytes through two
// stores with DIFFERENT exact scopes: exactly ONE created and the loser gets
// the typed OBJECT_SCOPE_CONFLICT — never two rows, never a second receipt.
func TestStoreObjectConcurrentCrossScopeRace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "engram.db")
	root := filepath.Join(dir, "objects")
	s1 := openTestStoreRootPath(t, path, root)
	s2 := openTestStoreRootPath(t, path, root)
	s1.SetReceiptSigner(newParitySigner(s1))
	s2.SetReceiptSigner(newParitySigner(s2))

	inB := objectInputForTest(t, []byte("race bytes"))
	inB.Scope = testScope(testRucB)

	start := make(chan struct{})
	errs := make([]error, 2)
	results := make([]core.ObjectStoreResult, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			st := s1
			in := objectInputForTest(t, []byte("race bytes"))
			if i == 1 {
				st = s2
				in = inB
			}
			results[i], errs[i] = st.StoreObject(context.Background(), in)
		}(i)
	}
	close(start)
	wg.Wait()

	created, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		switch {
		case errs[i] == nil && results[i].Created:
			created++
		case errs[i] != nil && strings.Contains(errs[i].Error(), "OBJECT_SCOPE_CONFLICT"):
			conflicts++
		default:
			t.Fatalf("goroutine %d: result=%+v err=%v, want created or OBJECT_SCOPE_CONFLICT", i, results[i], errs[i])
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatalf("created=%d conflicts=%d, want exactly one created and one OBJECT_SCOPE_CONFLICT", created, conflicts)
	}
	objectID := core.ComputeObjectID([]byte("race bytes"))
	if n := countRows(t, s1, `SELECT COUNT(*) FROM evidence_objects WHERE id = ?`, objectID); n != 1 {
		t.Fatalf("evidence_objects rows = %d, want exactly 1", n)
	}
	if n := storedObjectReceiptCount(t, s1, objectID); n != 1 {
		t.Fatalf("object_stored receipts = %d, want exactly 1", n)
	}
}

// TestObjectHardlinkCorruptionFailsClosed proves a hardlinked extra byte file
// is reported as an orphan by doctor and that corrupting the shared inode
// through the hardlink is DETECTED by the read-time re-hash (OBJECT_HASH_
// MISMATCH — no silent repair). Platforms without hardlinks skip.
func TestObjectHardlinkCorruptionFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "objects")
	s := newTestStoreWithRoot(t, root)
	result, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err != nil {
		t.Fatalf("store object: %v", err)
	}
	full := filepath.Join(root, result.Object.RelPath)

	otherID := strings.Repeat("cd", 32)
	otherPath := filepath.Join(root, "objects", otherID[0:2], otherID[2:4], otherID)
	if err := os.MkdirAll(filepath.Dir(otherPath), 0o700); err != nil {
		t.Fatalf("create other bucket dir: %v", err)
	}
	if err := os.Link(full, otherPath); err != nil {
		t.Skipf("hardlinks unsupported on this platform/OS: %v", err)
	}

	report, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	var orphanFound bool
	for _, f := range report.ObjectFindings {
		if f.Kind == objectFindingOrphanFile && f.ObjectID == otherID {
			orphanFound = true
		}
	}
	if !orphanFound {
		t.Fatalf("findings = %+v, want orphan_file for the hardlinked %s", report.ObjectFindings, otherID)
	}

	// Corrupt the shared inode through the hardlink: the stored object's bytes
	// now re-hash to a different digest — the read fails closed.
	if err := os.WriteFile(otherPath, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper through hardlink: %v", err)
	}
	_, _, err = s.GetObject(context.Background(), result.Object.ObjectID, testScope(testRucA))
	if err == nil || !strings.Contains(err.Error(), "OBJECT_HASH_MISMATCH") {
		t.Fatalf("corrupted read error = %v, want OBJECT_HASH_MISMATCH", err)
	}
}

// TestObjectPermissionsFailClosed proves permission-denied byte files and
// bucket directories fail closed on reads and doctor, and that restoring the
// permissions restores the reads (Unix permission support only).
func TestObjectPermissionsFailClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("permission checks are meaningless as root")
	}
	root := filepath.Join(t.TempDir(), "objects")
	s := newTestStoreWithRoot(t, root)
	result, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err != nil {
		t.Fatalf("store object: %v", err)
	}
	full := filepath.Join(root, result.Object.RelPath)

	if err := os.Chmod(full, 0); err != nil {
		t.Fatalf("chmod byte file 0000: %v", err)
	}
	if _, _, err := s.GetObject(context.Background(), result.Object.ObjectID, testScope(testRucA)); err == nil {
		t.Fatal("read of an unreadable byte file must fail closed")
	}
	if err := os.Chmod(full, 0o600); err != nil {
		t.Fatalf("restore byte file perms: %v", err)
	}
	if _, _, err := s.GetObject(context.Background(), result.Object.ObjectID, testScope(testRucA)); err != nil {
		t.Fatalf("read after restoring byte file perms: %v", err)
	}

	dir := filepath.Dir(full)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatalf("chmod bucket dir 0000: %v", err)
	}
	if _, _, err := s.GetObject(context.Background(), result.Object.ObjectID, testScope(testRucA)); err == nil {
		t.Fatal("read through an unsearchable bucket dir must fail closed")
	}
	if _, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine}); err == nil {
		t.Fatal("doctor with an unreadable subtree must fail closed (an incomplete report is not a report)")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("restore bucket dir perms: %v", err)
	}
	if _, _, err := s.GetObject(context.Background(), result.Object.ObjectID, testScope(testRucA)); err != nil {
		t.Fatalf("read after restoring bucket dir perms: %v", err)
	}
}

// TestSyncDirectoryToleranceAndHappyPath pins the portable unsupported
// directory-sync tolerance set (EINVAL/ENOTSUP/EOPNOTSUPP/ENOSYS/EBADF) and
// proves REAL failures (EIO/EROFS/EACCES) are NOT tolerated — a real
// directory-sync failure must roll the write back. A vanished directory is a
// no-op; a real directory syncs cleanly.
func TestSyncDirectoryToleranceAndHappyPath(t *testing.T) {
	for _, err := range []error{syscall.EINVAL, syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.ENOSYS, syscall.EBADF} {
		if !isUnsupportedDirSyncError(err) {
			t.Errorf("%v must be a tolerated (documented unsupported) dir-sync error", err)
		}
	}
	for _, err := range []error{syscall.EIO, syscall.EROFS, syscall.EACCES} {
		if isUnsupportedDirSyncError(err) {
			t.Errorf("%v is a REAL durability failure — must NOT be tolerated", err)
		}
	}

	dir := t.TempDir()
	if err := syncDirectory(dir); err != nil {
		t.Fatalf("sync of a real directory: %v", err)
	}
	if err := syncDirectory(filepath.Join(dir, "missing")); err != nil {
		t.Fatalf("sync of a vanished directory must be a no-op: %v", err)
	}
}

// ──────────────────────────────────────────────
// Doctor — purge lifecycle diagnostics (design §13.3, WU-1)
// ──────────────────────────────────────────────

// TestDoctorReportsLifecycleTableCounts proves the doctor's §13.3 lifecycle
// table counts: a store without lifecycle state reports zeros on every
// lifecycle table and an approved (pre-execution) pipeline is counted exactly
// (one request, one approval, the request-bound projection, the command-ledger
// keys and the events).
func TestDoctorReportsLifecycleTableCounts(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))

	report, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
	if err != nil {
		t.Fatalf("doctor on empty store: %v", err)
	}
	for name, got := range map[string]int{
		"purgeRequests": report.PurgeRequests, "purgeApprovals": report.PurgeApprovals,
		"lifecycleEvents": report.LifecycleEvents, "retentionState": report.RetentionState,
		"purgeIdempotencyKeys": report.PurgeIdempotencyKeys, "purgeExecutions": report.PurgeExecutions,
		"holds": report.Holds, "holdIdempotencyKeys": report.HoldIdempotencyKeys,
	} {
		if got != 0 {
			t.Fatalf("empty store %s = %d, want 0", name, got)
		}
	}

	approvedPurgePipeline(t, s) // one stored object, one bound policy, one request, one approval
	report, err = s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
	if err != nil {
		t.Fatalf("doctor after the pipeline: %v", err)
	}
	if report.PurgeRequests != 1 || report.PurgeApprovals != 1 {
		t.Fatalf("requests/approvals = %d/%d, want 1/1", report.PurgeRequests, report.PurgeApprovals)
	}
	if report.RetentionState != 1 {
		t.Fatalf("retentionState = %d, want 1 (the request-bound projection)", report.RetentionState)
	}
	if report.PurgeExecutions != 0 {
		t.Fatalf("purgeExecutions = %d, want 0 before any execution", report.PurgeExecutions)
	}
	if report.LifecycleEvents < 3 {
		t.Fatalf("lifecycleEvents = %d, want >= 3 (retention bound + request + approval)", report.LifecycleEvents)
	}
	if report.PurgeIdempotencyKeys < 2 {
		t.Fatalf("purgeIdempotencyKeys = %d, want >= 2 (request + approval keys)", report.PurgeIdempotencyKeys)
	}
}

// TestDoctorReportsIntentExecutionFindingBytesAbsent freezes the §13.3 doctor
// finding of the CRASH WINDOW: the durable intent committed (executions row
// 'intent'), the completion never did and the bytes are already gone (a crash
// between the authorized unlink and the completion). The doctor must NOT fail
// closed: it REPORTS the intent as an auditable finding with the exact
// execution/request/object identity, the intent metadata, NO completion receipt
// and bytesState absent, and reconciles the object-layer absence as
// documented_intent (never generic corruption). The scan makes ZERO writes.
func TestDoctorReportsIntentExecutionFindingBytesAbsent(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	execID := "00000000-0000-4000-8000-00000000a001"
	fx := crashWindowFixture(t, s, execID)

	report, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
	if err != nil {
		t.Fatalf("doctor must NOT fail closed on a documented intent absence: %v", err)
	}
	if len(report.PurgeFindings) != 1 {
		t.Fatalf("purgeFindings = %+v, want exactly the intent row", report.PurgeFindings)
	}
	f := report.PurgeFindings[0]
	if f.Kind != purgeFindingIntent || f.State != string(core.PurgeExecutionIntent) {
		t.Fatalf("finding = %+v, want PURGE_EXECUTION_INTENT / intent", f)
	}
	if f.ExecutionID != execID || f.RequestID != fx.request.RequestID || f.ObjectID != fx.objectID {
		t.Fatalf("finding identity = %+v, want the crash-window execution/request/object", f)
	}
	if f.BytesState != purgeBytesStateAbsent {
		t.Fatalf("bytesState = %q, want absent (crash after the authorized unlink)", f.BytesState)
	}
	if f.CompletionReceiptID != "" {
		t.Fatalf("an intent finding must carry NO completion receipt: %q", f.CompletionReceiptID)
	}
	if f.IntentAt == "" || f.IntentBy == "" || f.PreRemovalHash != fx.objectID || f.IntentReviewedHash == "" {
		t.Fatalf("intent metadata incomplete: %+v", f)
	}
	// The exact stored scope tuple is reported from the request row (the scope
	// authority — never a caller-declared or derived scope).
	if f.TenantID != testOrgID || f.CompanyID != "acme" || f.RUC != testRucA || f.Period != testPeriod {
		t.Fatalf("scope tuple = %+v, want the stored request scope", f)
	}

	var documented bool
	for _, of := range report.ObjectFindings {
		if of.Kind == objectFindingDocumentedIntent && of.ObjectID == fx.objectID {
			documented = true
		}
	}
	if !documented {
		t.Fatalf("objectFindings = %+v, want a documented_intent finding for %s", report.ObjectFindings, fx.objectID)
	}

	// Zero writes: the intent row and the missing-bytes state survive the scan.
	if got := executionState(t, s, execID); got != string(core.PurgeExecutionIntent) {
		t.Fatalf("execution state after doctor = %q, want intent (zero writes)", got)
	}
}

// TestDoctorReportsIntentExecutionFindingBytesPresent freezes the §13.3 doctor
// finding of a PRESENT-bytes intent: a corrupt-byte abort leaves the attempt in
// 'intent' with the bytes still on disk. The doctor reports the intent finding
// with bytesState present, produces NO documented-absence object finding and
// never repairs the corrupt bytes.
func TestDoctorReportsIntentExecutionFindingBytesPresent(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := approvedPurgePipeline(t, s)
	path := objectBytesPath(t, s, fx.object)
	if err := os.WriteFile(path, []byte("CORRUPTED-BYTES-NOT-THE-ORIGINAL"), 0o600); err != nil {
		t.Fatalf("corrupt object bytes: %v", err)
	}
	_, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-00000000a002",
	}, recordsPrincipal(t))
	if err == nil || !strings.Contains(err.Error(), objectErrHashMismatch) {
		t.Fatalf("corrupt-byte execution = %v, want OBJECT_HASH_MISMATCH", err)
	}

	report, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
	if err != nil {
		t.Fatalf("doctor on a present-bytes intent: %v", err)
	}
	if len(report.PurgeFindings) != 1 {
		t.Fatalf("purgeFindings = %+v, want exactly the intent row", report.PurgeFindings)
	}
	f := report.PurgeFindings[0]
	if f.Kind != purgeFindingIntent || f.BytesState != purgeBytesStatePresent {
		t.Fatalf("finding = %+v, want PURGE_EXECUTION_INTENT with bytesState present", f)
	}
	for _, of := range report.ObjectFindings {
		if (of.Kind == objectFindingDocumentedPurge || of.Kind == objectFindingDocumentedIntent) && of.ObjectID == fx.objectID {
			t.Fatalf("present bytes must not be a documented absence: %+v", of)
		}
	}
	// The doctor never repairs: the corrupt bytes survive.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("corrupt bytes must survive the doctor: %v", err)
	}
}

// TestDoctorReportsInterruptedExecutionAndDocumentedPurge freezes the §13.3
// findings of the FULL RETRY history: a corrupt-byte abort (attempt 1 stays
// 'intent'), a restored-bytes fresh-id retry (attempt 1 becomes TERMINAL
// 'interrupted', attempt 2 completes and removes the bytes). The doctor reports
// the interrupted attempt (PURGE_EXECUTION_INTERRUPTED, bytesState absent, NO
// completion receipt) and reconciles the object-layer absence as
// documented_purge — the receipt-covered completion — never corruption, never
// repaired, zero writes.
func TestDoctorReportsInterruptedExecutionAndDocumentedPurge(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := approvedPurgePipeline(t, s)
	path := objectBytesPath(t, s, fx.object)
	original := []byte("purge-target-bytes-0123456789")

	if err := os.WriteFile(path, []byte("CORRUPTED-BYTES-NOT-THE-ORIGINAL"), 0o600); err != nil {
		t.Fatalf("corrupt object bytes: %v", err)
	}
	if _, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-00000000a011",
	}, recordsPrincipal(t)); err == nil || !strings.Contains(err.Error(), objectErrHashMismatch) {
		t.Fatalf("attempt 1 = %v, want OBJECT_HASH_MISMATCH", err)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("restore object bytes: %v", err)
	}
	result, err := s.ExecutePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             fx.request.RequestID,
		ExpectedLifecycleHash: fx.approvedHash,
		ExecutionID:           "00000000-0000-4000-8000-00000000a012",
	}, recordsPrincipal(t))
	if err != nil || result.Execution.State != core.PurgeExecutionCompleted {
		t.Fatalf("fresh-id retry = %+v err=%v, want a completed attempt", result, err)
	}
	if got := executionState(t, s, "00000000-0000-4000-8000-00000000a011"); got != string(core.PurgeExecutionInterrupted) {
		t.Fatalf("stale attempt state = %q, want interrupted", got)
	}

	report, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
	if err != nil {
		t.Fatalf("doctor after the retry history: %v", err)
	}
	if len(report.PurgeFindings) != 1 {
		t.Fatalf("purgeFindings = %+v, want exactly the interrupted row", report.PurgeFindings)
	}
	f := report.PurgeFindings[0]
	if f.Kind != purgeFindingInterrupted || f.State != string(core.PurgeExecutionInterrupted) {
		t.Fatalf("finding = %+v, want PURGE_EXECUTION_INTERRUPTED / interrupted", f)
	}
	if f.ExecutionID != "00000000-0000-4000-8000-00000000a011" || f.BytesState != purgeBytesStateAbsent {
		t.Fatalf("finding = %+v, want the interrupted attempt with bytesState absent", f)
	}
	if f.CompletionReceiptID != "" {
		t.Fatalf("an interrupted finding must carry NO completion receipt: %q", f.CompletionReceiptID)
	}

	var documentedPurge bool
	for _, of := range report.ObjectFindings {
		if of.Kind == objectFindingDocumentedPurge && of.ObjectID == fx.objectID {
			documentedPurge = true
		}
		if of.Kind == objectFindingDocumentedIntent && of.ObjectID == fx.objectID {
			t.Fatalf("a completed execution must classify as documented_purge, not documented_intent: %+v", of)
		}
	}
	if !documentedPurge {
		t.Fatalf("objectFindings = %+v, want a documented_purge finding for %s", report.ObjectFindings, fx.objectID)
	}
	// Zero writes: the guarded interrupted row is untouched by the scan.
	if got := executionState(t, s, "00000000-0000-4000-8000-00000000a011"); got != string(core.PurgeExecutionInterrupted) {
		t.Fatalf("execution state after doctor = %q, want interrupted (zero writes)", got)
	}
}

// TestDoctorInterruptedOnlyHistoryNotAnAuthorization freezes the §13.2/§13.3
// boundary: a stale 'interrupted'-ONLY executions history (no live intent, no
// completed execution) is NOT a purge authorization — missing bytes stay the
// hard OBJECT_BYTES_MISSING integrity incident and the doctor FAILS CLOSED.
// The guarded state machine cannot produce this shape through the API (a
// fresh-id retry always inserts a new intent in the same transaction), so the
// test plants the interrupted row directly at the schema level (INSERTs are
// not guarded — only UPDATE/DELETE are).
func TestDoctorInterruptedOnlyHistoryNotAnAuthorization(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	fx := approvedPurgePipeline(t, s)
	if _, err := s.db.Exec(`INSERT INTO evidence_purge_executions
		(execution_id, request_id, object_id, rel_path, size, pre_removal_hash,
		 intent_reviewed_hash, state, intent_at, intent_by, completed_at, completed_by, completion_receipt_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'interrupted', ?, ?, NULL, NULL, NULL)`,
		"00000000-0000-4000-8000-00000000a021", fx.request.RequestID, fx.objectID,
		fx.object.RelPath, fx.object.Size, fx.objectID, strings.Repeat("c", 64),
		"2026-08-05T12:00:00Z", "exec-planted"); err != nil {
		t.Fatalf("plant interrupted-only execution: %v", err)
	}
	if err := os.Remove(objectBytesPath(t, s, fx.object)); err != nil {
		t.Fatalf("remove object bytes: %v", err)
	}
	if _, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine}); err == nil || !strings.Contains(err.Error(), objectErrBytesMissing) {
		t.Fatalf("doctor with interrupted-only missing bytes = %v, want OBJECT_BYTES_MISSING (fail closed)", err)
	}
}
