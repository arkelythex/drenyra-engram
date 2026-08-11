// G-6 copy-only corruption drill and restore drill (spec AC-4/AC-5, FR-5/FR-6,
// FZ-4; design D-7/D-8; task 2.5/2.6/2.7 — REMEDIATION slice after verify FAIL).
//
// AC-4 (corruption drill): a disposable marked copy of a consistent DB is
// deliberately damaged → the full doctor surface detects it → the next write
// returns the typed STORE_WRITE_FROZEN error (retry cannot clear the latch) →
// the corrupted copy is preserved byte-for-byte as evidence → no in-place repair
// is attempted → the live DB is untouched and never opened by the drill.
//
// AC-5 (restore drill): the immutable VACUUM INTO snapshot is copied to a
// separate candidate and verified in the FIXED order integrity_check →
// foreign_key_check → exact expected scope conformance → backup identity; only
// after all four pass is the output atomically published with a verified
// manifest. Every negative (corrupted candidate, wrong identity, tampered
// manifest, missing scope rows, same source/output, pre-existing output,
// interrupted candidate) fails closed with a typed error, never publishes the
// output, quarantines the candidate, and leaves the source snapshot untouched.
//
// Task 2.7 (scope isolation): a readable snapshot containing only another
// company/period fails scope conformance and the error never enumerates foreign
// rows (cross-tenant invisibility for the drill surface, IR-2).
//
// Money convention: no monetary value appears in the drills (money stays whole
// int64 cents elsewhere in the ecosystem; nothing here touches money).
//
// RED contract (strict TDD): these tests were written FIRST and failed (compile
// RED — RunCorruptionDrill / detectDrillCorruption / RunRestoreDrill and the
// restore result types did not exist; the strconvAtoi cases failed on the
// silent-prefix parse) before any production code for this slice was added.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// seedLiveStore opens a REAL live store at path, saves n memories under the
// exact scope, closes it, and returns the file SHA-256 of the live DB bytes
// (the read-only/live-untouched evidence baseline). It also returns the scope
// used so callers can assert against it.
func seedLiveStore(t *testing.T, path string, scope core.Scope, n int) string {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open live store %s: %v", path, err)
	}
	for i := 0; i < n; i++ {
		in := validInput(fmt.Sprintf("drill.topic.%d", i), "drill fixture content")
		in.Scope = scope
		if _, err := s.Save(in); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close live store: %v", err)
	}
	hash, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("hash live store: %v", err)
	}
	return hash
}

// makeMarkedEvidenceCopy byte-copies the snapshot to a dedicated evidence path,
// writes the derived adjacent drill marker (CopyPath=evidence, SourcePath=
// snapshot, SourceSHA256 = snapshot identity), then optionally applies the
// corrupt func to the evidence bytes — exactly the artifact shape the corruption
// drill operates on (a MARKED copy, never the live DB).
func makeMarkedEvidenceCopy(t *testing.T, snapshotPath, evidencePath string, corrupt func(string)) {
	t.Helper()
	bytes, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if err := os.WriteFile(evidencePath, bytes, 0o600); err != nil {
		t.Fatalf("write evidence copy: %v", err)
	}
	if err := fsyncFile(evidencePath); err != nil {
		t.Fatalf("fsync evidence copy: %v", err)
	}
	cp, err := canonicalPath(evidencePath)
	if err != nil {
		t.Fatalf("canonical evidence path: %v", err)
	}
	src, err := canonicalPath(snapshotPath)
	if err != nil {
		t.Fatalf("canonical snapshot path: %v", err)
	}
	m, err := loadDrillManifest(snapshotPath + drillMarkerSuffix)
	if err != nil {
		t.Fatalf("load snapshot manifest: %v", err)
	}
	m.SourcePath = src
	m.CopyPath = cp
	if err := atomicWriteJSON(evidencePath+drillMarkerSuffix, m); err != nil {
		t.Fatalf("write evidence marker: %v", err)
	}
	if corrupt != nil {
		corrupt(evidencePath)
	}
}

// corruptHeaderUnusedByte flips SQLite header byte 60 — a reserved/unused
// header byte that PRAGMA integrity_check does NOT validate. The store stays
// fully readable and every check reports ok, so a drill that REQUIRES detection
// must fail closed (CORRUPTION_NOT_DETECTED) instead of claiming a detection it
// cannot prove. Used for the wrong-backup-identity restore negative (identity
// catches the byte change after integrity/FK/scope pass).
func corruptHeaderUnusedByte(t *testing.T, path string) {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(bytes) <= 60 {
		t.Fatalf("%s is too small to corrupt header byte 60", path)
	}
	bytes[60] ^= 0xFF
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// countScopeRows opens the DB read-only and counts observations for the exact
// company scope tuple (the restored-output usability proof).
func countScopeRows(t *testing.T, path string, scope core.Scope) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		t.Fatalf("open %s read-only: %v", path, err)
	}
	defer func() { _ = db.Close() }()
	var n int
	err = db.QueryRow(`SELECT COUNT(*) FROM observations
		WHERE scope_kind = ? AND organization_id = ? AND company_id = ? AND ruc = ? AND period = ?`,
		string(core.ScopeKindCompany), scope.OrganizationID, scope.CompanyID, scope.RUC, scope.Period,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count rows in %s: %v", path, err)
	}
	return n
}

// ─────────────────────────────────────────────────────────────────────────────
// AC-4 — corruption drill (design D-8)
// ─────────────────────────────────────────────────────────────────────────────

// TestRunCorruptionDrillFullPath is the AC-4 journey: disposable marked copy →
// deliberate damage → check-surface detection → typed write freeze (retry-proof)
// → byte-preserved evidence → no in-place repair → live DB untouched.
func TestRunCorruptionDrillFullPath(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	liveHashBefore := seedLiveStore(t, live, testScope(testRucA), 3)

	snap, err := CreateDrillSnapshot(context.Background(), CreateDrillSnapshotInput{
		SourcePath:   live,
		SnapshotPath: filepath.Join(dir, "snapshot.db"),
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	evidence := filepath.Join(dir, "evidence.db")
	res, err := RunCorruptionDrill(context.Background(), RunCorruptionDrillInput{
		SnapshotPath: snap.SnapshotPath,
		EvidencePath: evidence,
	})
	if err != nil {
		t.Fatalf("run corruption drill: %v", err)
	}
	defer func() { _ = res.DrillStore.Close() }()

	// (iii) Detection through the FR-4 check surface: full doctor integrity_check
	// failed on the evidence copy, and the full path ran integrity_check then the
	// paired foreign_key_check (trace pin).
	if res.Report.IntegrityCheck.Status != StatusFailed {
		t.Fatalf("integrityCheck.status = %q, want failed (detail %q)",
			res.Report.IntegrityCheck.Status, res.Report.IntegrityCheck.Detail)
	}
	wantTrace := []string{"integrity_check", "foreign_key_check"}
	if strings.Join(res.DrillStore.doctorTrace, ",") != strings.Join(wantTrace, ",") {
		t.Fatalf("full doctor trace = %v, want %v", res.DrillStore.doctorTrace, wantTrace)
	}
	// The damage actually changed bytes (corrupted hash != snapshot identity).
	if res.CorruptedSHA256 == res.SnapshotSHA256 {
		t.Fatalf("corrupted evidence hash equals the snapshot hash — the drill damaged nothing")
	}
	// The evidence manifest records the snapshot as its source identity.
	if res.Manifest.SourceSHA256 != snap.SHA256 {
		t.Fatalf("evidence manifest sourceSHA256 = %s, want snapshot hash %s",
			res.Manifest.SourceSHA256, snap.SHA256)
	}
	if !res.Manifest.DrillCopy {
		t.Fatal("evidence manifest drillCopy must be true (marked copy)")
	}

	// (iv) The next write is refused with the typed error BEFORE any transaction;
	// a retry cannot clear the monotonic latch; every write entry point is frozen.
	if _, err := res.DrillStore.Save(validInput("drill.write.attempt", "must be refused")); !errors.Is(err, ErrStoreWriteFrozen) {
		t.Fatalf("Save after detection: err = %v, want ErrStoreWriteFrozen", err)
	}
	if _, err := res.DrillStore.Save(validInput("drill.write.attempt", "must still be refused")); !errors.Is(err, ErrStoreWriteFrozen) {
		t.Fatalf("Save retry: err = %v, want ErrStoreWriteFrozen (retry cannot clear the latch)", err)
	}
	if _, err := res.DrillStore.BeginReceiptTx(context.Background()); !errors.Is(err, ErrStoreWriteFrozen) {
		t.Fatalf("BeginReceiptTx after detection: err = %v, want ErrStoreWriteFrozen", err)
	}

	// (v) Byte preservation + (vi) no in-place repair: the corrupted evidence
	// bytes are IDENTICAL after doctor + refused writes to the corrupted hash the
	// drill recorded — doctor and the write refusals never mutated the artifact.
	evidenceHashAfter, err := fileSHA256(evidence)
	if err != nil {
		t.Fatalf("hash evidence after: %v", err)
	}
	if evidenceHashAfter != res.CorruptedSHA256 {
		t.Fatalf("evidence bytes changed after checking/refused writes (%s → %s) — byte preservation or no-repair violated",
			res.CorruptedSHA256, evidenceHashAfter)
	}

	// (vii) The live DB is untouched: bytes identical, still openable, and its
	// routine doctor still reports a healthy store.
	liveHashAfter, err := fileSHA256(live)
	if err != nil {
		t.Fatalf("hash live after: %v", err)
	}
	if liveHashAfter != liveHashBefore {
		t.Fatalf("live DB bytes changed across the drill (%s → %s)", liveHashBefore, liveHashAfter)
	}
	liveStore, err := Open(live)
	if err != nil {
		t.Fatalf("live DB must stay openable after the drill: %v", err)
	}
	defer func() { _ = liveStore.Close() }()
	if n := countScopeRows(t, live, testScope(testRucA)); n != 3 {
		t.Fatalf("live DB scope row count = %d, want 3 (logical state unchanged)", n)
	}
}

// TestCorruptionDrillRequiresMarkedCopy pins "operate on a MARKED COPY, never
// the live DB — enforce the mark": RunCorruptionDrill must refuse a non-marked
// path (the live DB itself) with the typed DRILL_COPY_REQUIRED error and must
// not create any evidence artifact.
func TestCorruptionDrillRequiresMarkedCopy(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	liveHashBefore := seedLiveStore(t, live, testScope(testRucA), 2)

	evidence := filepath.Join(dir, "evidence.db")
	_, err := RunCorruptionDrill(context.Background(), RunCorruptionDrillInput{
		SnapshotPath: live, // the live DB carries no drill marker
		EvidencePath: evidence,
	})
	if err == nil {
		t.Fatal("RunCorruptionDrill on the live DB must fail closed (the drill never operates on a live database)")
	}
	if !errors.Is(err, ErrDrillCopyRequired) {
		t.Fatalf("err = %v, want ErrDrillCopyRequired", err)
	}
	if _, statErr := os.Stat(evidence); !os.IsNotExist(statErr) {
		t.Fatalf("no evidence artifact may be created on a refused drill (evidence exists: %v)", statErr)
	}
	liveHashAfter, err := fileSHA256(live)
	if err != nil {
		t.Fatalf("hash live after: %v", err)
	}
	if liveHashAfter != liveHashBefore {
		t.Fatalf("live DB bytes changed by the refused drill (%s → %s)", liveHashBefore, liveHashAfter)
	}
}

// TestCorruptionDrillEvidencePathContract pins the never-clobber-evidence rules:
// a pre-existing evidence path and an evidence path equal to the snapshot are
// both refused with INVALID_DRILL_PATH.
func TestCorruptionDrillEvidencePathContract(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	seedLiveStore(t, live, testScope(testRucA), 1)
	snap, err := CreateDrillSnapshot(context.Background(), CreateDrillSnapshotInput{
		SourcePath:   live,
		SnapshotPath: filepath.Join(dir, "snapshot.db"),
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	preExisting := filepath.Join(dir, "evidence.db")
	if err := os.WriteFile(preExisting, []byte("existing evidence — never overwritten"), 0o600); err != nil {
		t.Fatalf("create pre-existing evidence: %v", err)
	}
	if _, err := RunCorruptionDrill(context.Background(), RunCorruptionDrillInput{
		SnapshotPath: snap.SnapshotPath,
		EvidencePath: preExisting,
	}); !errors.Is(err, ErrInvalidDrillPath) {
		t.Fatalf("pre-existing evidence path: err = %v, want ErrInvalidDrillPath", err)
	}
	if _, err := RunCorruptionDrill(context.Background(), RunCorruptionDrillInput{
		SnapshotPath: snap.SnapshotPath,
		EvidencePath: snap.SnapshotPath, // evidence == snapshot
	}); !errors.Is(err, ErrInvalidDrillPath) {
		t.Fatalf("evidence == snapshot: err = %v, want ErrInvalidDrillPath", err)
	}
}

// TestCorruptionDrillEvidenceCannotOpenAsLiveStore pins the marker contract in
// BOTH directions: after the drill, normal Open refuses the marked evidence copy
// with DRILL_COPY_ONLY — the artifact can never be used as a live writable store,
// and there is no unfreeze/repair path (the only way to a usable DB is the
// verified restore drill).
func TestCorruptionDrillEvidenceCannotOpenAsLiveStore(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	seedLiveStore(t, live, testScope(testRucA), 2)
	snap, err := CreateDrillSnapshot(context.Background(), CreateDrillSnapshotInput{
		SourcePath:   live,
		SnapshotPath: filepath.Join(dir, "snapshot.db"),
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	res, err := RunCorruptionDrill(context.Background(), RunCorruptionDrillInput{
		SnapshotPath: snap.SnapshotPath,
		EvidencePath: filepath.Join(dir, "evidence.db"),
	})
	if err != nil {
		t.Fatalf("run corruption drill: %v", err)
	}
	_ = res.DrillStore.Close()

	if _, err := Open(res.EvidencePath); !errors.Is(err, ErrDrillCopyOnly) {
		t.Fatalf("Open(evidence copy) = %v, want ErrDrillCopyOnly (drill artifacts can never be live stores)", err)
	}
}

// TestCorruptionDrillNotDetectedFailsClosed is the negative path: when the full
// doctor surface does NOT detect corruption, the drill fails closed with
// CORRUPTION_NOT_DETECTED instead of claiming a detection it cannot prove.
// Two sub-cases: a healthy marked copy (no damage), and a marked copy carrying
// damage that is structurally invisible to integrity_check (header-byte flip).
// In both cases the failed detection must leave the artifact byte-identical.
func TestCorruptionDrillNotDetectedFailsClosed(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	seedLiveStore(t, live, testScope(testRucA), 2)
	snap, err := CreateDrillSnapshot(context.Background(), CreateDrillSnapshotInput{
		SourcePath:   live,
		SnapshotPath: filepath.Join(dir, "snapshot.db"),
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	t.Run("healthy marked copy reports no detection", func(t *testing.T) {
		// A marked but undamaged copy: doctor reports ok → the drill must fail
		// closed with CORRUPTION_NOT_DETECTED.
		_, _, err := detectDrillCorruption(context.Background(), snap.SnapshotPath)
		if !errors.Is(err, ErrCorruptionNotDetected) {
			t.Fatalf("detect on healthy marked copy: err = %v, want ErrCorruptionNotDetected", err)
		}
	})

	t.Run("invisible damage reports no detection and preserves bytes", func(t *testing.T) {
		evidence := filepath.Join(dir, "evidence-invisible.db")
		makeMarkedEvidenceCopy(t, snap.SnapshotPath, evidence, func(p string) { corruptHeaderUnusedByte(t, p) })
		hashBefore, err := fileSHA256(evidence)
		if err != nil {
			t.Fatalf("hash evidence before: %v", err)
		}
		_, _, err = detectDrillCorruption(context.Background(), evidence)
		if !errors.Is(err, ErrCorruptionNotDetected) {
			t.Fatalf("detect on invisibly damaged copy: err = %v, want ErrCorruptionNotDetected", err)
		}
		hashAfter, err := fileSHA256(evidence)
		if err != nil {
			t.Fatalf("hash evidence after: %v", err)
		}
		if hashAfter != hashBefore {
			t.Fatalf("failed detection mutated the evidence copy (%s → %s)", hashBefore, hashAfter)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// AC-5 — restore drill (design D-7)
// ─────────────────────────────────────────────────────────────────────────────

// TestRunRestoreDrillSuccess is the AC-5 success path: the WAL-safe snapshot is
// restored to a separate output, all four verify-after-restore checks pass in
// order, the output is atomically published byte-identical to the snapshot, a
// verified manifest is emitted, the restored DB is usable, and the source
// snapshot is untouched.
func TestRunRestoreDrillSuccess(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	scope := testScope(testRucA)
	seedLiveStore(t, live, scope, 3)

	snap, err := CreateDrillSnapshot(context.Background(), CreateDrillSnapshotInput{
		SourcePath:    live,
		SnapshotPath:  filepath.Join(dir, "snapshot.db"),
		ExpectedScope: scope,
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapHashBefore, err := fileSHA256(snap.SnapshotPath)
	if err != nil {
		t.Fatalf("hash snapshot: %v", err)
	}

	output := filepath.Join(dir, "restored.db")
	res, err := RunRestoreDrill(context.Background(), RunRestoreDrillInput{
		SnapshotPath:  snap.SnapshotPath,
		OutputPath:    output,
		ExpectedScope: scope,
	})
	if err != nil {
		t.Fatalf("run restore drill: %v", err)
	}

	// (iii) All four ordered verify-after-restore checks passed.
	if res.Checks.Integrity.Status != StatusOk {
		t.Fatalf("integrity check = %+v, want ok", res.Checks.Integrity)
	}
	if res.Checks.ForeignKeys.Status != StatusOk {
		t.Fatalf("foreign key check = %+v, want ok", res.Checks.ForeignKeys)
	}
	if res.Checks.Scope.Status != StatusOk {
		t.Fatalf("scope conformance = %+v, want ok", res.Checks.Scope)
	}
	if res.Checks.BackupIdentity.Status != StatusOk {
		t.Fatalf("backup identity = %+v, want ok", res.Checks.BackupIdentity)
	}

	// (ii) Separate output, byte-identical to the snapshot, atomically published.
	outHash, err := fileSHA256(output)
	if err != nil {
		t.Fatalf("hash output: %v", err)
	}
	if outHash != snap.SHA256 {
		t.Fatalf("restored output hash %s != snapshot identity %s", outHash, snap.SHA256)
	}

	// The verified manifest is emitted beside the output and records the
	// output path and the verified identity.
	if _, err := os.Stat(res.ManifestPath); err != nil {
		t.Fatalf("verified manifest missing: %v", err)
	}
	if res.Manifest.OutputPath != output {
		t.Fatalf("verified manifest outputPath = %q, want %q", res.Manifest.OutputPath, output)
	}
	if res.Manifest.SourceSHA256 != snap.SHA256 {
		t.Fatalf("verified manifest sourceSHA256 = %q, want %q", res.Manifest.SourceSHA256, snap.SHA256)
	}

	// The restored DB is usable: it opens normally and holds the expected
	// exact-scope rows.
	if n := countScopeRows(t, output, scope); n != 3 {
		t.Fatalf("restored output scope row count = %d, want 3 (restored DB usable)", n)
	}

	// (v) The source snapshot is untouched.
	snapHashAfter, err := fileSHA256(snap.SnapshotPath)
	if err != nil {
		t.Fatalf("hash snapshot after: %v", err)
	}
	if snapHashAfter != snapHashBefore {
		t.Fatalf("source snapshot bytes changed across the restore (%s → %s)", snapHashBefore, snapHashAfter)
	}
}

// TestRunRestoreDrillNegativeMatrix is the AC-5 negative matrix: every broken
// restore is rejected with a typed error, the output is never published, the
// rejected candidate is quarantined, and the source snapshot is untouched.
func TestRunRestoreDrillNegativeMatrix(t *testing.T) {
	newSnapshot := func(t *testing.T, dir string) DrillSnapshot {
		t.Helper()
		live := filepath.Join(dir, "live.db")
		scope := testScope(testRucA)
		seedLiveStore(t, live, scope, 3)
		snap, err := CreateDrillSnapshot(context.Background(), CreateDrillSnapshotInput{
			SourcePath:    live,
			SnapshotPath:  filepath.Join(dir, "snapshot.db"),
			ExpectedScope: scope,
		})
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		return snap
	}
	assertFailedClosed := func(t *testing.T, err error, want error, snapshotPath, outputPath, snapHashBefore, outputHashBefore string, outputExistedBefore bool) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("err = %v, want %v", err, want)
		}
		// The output is never published/modified by a failed restore: if it
		// pre-existed (or IS the snapshot, in the same-path case), its bytes are
		// unchanged; otherwise it must not exist at all.
		if outputExistedBefore {
			after, hashErr := fileSHA256(outputPath)
			if hashErr != nil || after != outputHashBefore {
				t.Fatalf("pre-existing output was modified by the failed restore (%s → %s)", outputHashBefore, after)
			}
		} else if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
			t.Fatalf("output %s must NEVER be published on a failed restore (exists: %v)", outputPath, statErr)
		}
		snapHashAfter, hashErr := fileSHA256(snapshotPath)
		if hashErr != nil {
			t.Fatalf("hash snapshot after: %v", hashErr)
		}
		if snapHashAfter != snapHashBefore {
			t.Fatalf("source snapshot bytes changed by the failed restore (%s → %s)", snapHashBefore, snapHashAfter)
		}
	}

	t.Run("corrupted candidate", func(t *testing.T) {
		dir := t.TempDir()
		snap := newSnapshot(t, dir)
		// Damage the SNAPSHOT after its manifest was written: the candidate copy
		// inherits the damage, so integrity_check fails FIRST — the restore must
		// reject with RESTORE_VERIFICATION_FAILED (not BACKUP_IDENTITY_MISMATCH),
		// which behaviorally proves check 1 ran before check 4.
		corruptSigningKeysRootTypeByte(t, snap.SnapshotPath)
		snapHashBefore, err := fileSHA256(snap.SnapshotPath)
		if err != nil {
			t.Fatalf("hash snapshot: %v", err)
		}
		output := filepath.Join(dir, "restored.db")
		_, err = RunRestoreDrill(context.Background(), RunRestoreDrillInput{
			SnapshotPath:  snap.SnapshotPath,
			OutputPath:    output,
			ExpectedScope: testScope(testRucA),
		})
		assertFailedClosed(t, err, ErrRestoreVerificationFailed, snap.SnapshotPath, output, snapHashBefore, "", false)
		if _, statErr := os.Stat(output + ".candidate"); statErr != nil {
			t.Fatalf("rejected candidate must stay quarantined at %s.candidate (exists: %v)", output, statErr)
		}
	})

	t.Run("wrong backup identity", func(t *testing.T) {
		dir := t.TempDir()
		snap := newSnapshot(t, dir)
		// Structurally invisible damage (header unused byte): integrity, FK and
		// scope all PASS; the backup-identity check (check 4) catches the byte
		// change → typed BACKUP_IDENTITY_MISMATCH. This behaviorally proves the
		// identity check runs LAST, after integrity/FK/scope.
		corruptHeaderUnusedByte(t, snap.SnapshotPath)
		snapHashBefore, err := fileSHA256(snap.SnapshotPath)
		if err != nil {
			t.Fatalf("hash snapshot: %v", err)
		}
		output := filepath.Join(dir, "restored.db")
		_, err = RunRestoreDrill(context.Background(), RunRestoreDrillInput{
			SnapshotPath:  snap.SnapshotPath,
			OutputPath:    output,
			ExpectedScope: testScope(testRucA),
		})
		assertFailedClosed(t, err, ErrBackupIdentityMismatch, snap.SnapshotPath, output, snapHashBefore, "", false)
	})

	t.Run("tampered manifest identity", func(t *testing.T) {
		dir := t.TempDir()
		snap := newSnapshot(t, dir)
		// Rewrite the snapshot marker with a WRONG SourceSHA256: the candidate is
		// byte-identical to the snapshot but does not match the tampered manifest
		// identity → BACKUP_IDENTITY_MISMATCH.
		m, err := loadDrillManifest(snap.ManifestPath)
		if err != nil {
			t.Fatalf("load manifest: %v", err)
		}
		m.SourceSHA256 = strings.Repeat("0", 64)
		if err := atomicWriteJSON(snap.ManifestPath, m); err != nil {
			t.Fatalf("tamper manifest: %v", err)
		}
		snapHashBefore, err := fileSHA256(snap.SnapshotPath)
		if err != nil {
			t.Fatalf("hash snapshot: %v", err)
		}
		output := filepath.Join(dir, "restored.db")
		_, err = RunRestoreDrill(context.Background(), RunRestoreDrillInput{
			SnapshotPath:  snap.SnapshotPath,
			OutputPath:    output,
			ExpectedScope: testScope(testRucA),
		})
		assertFailedClosed(t, err, ErrBackupIdentityMismatch, snap.SnapshotPath, output, snapHashBefore, "", false)
	})

	t.Run("same source and output path", func(t *testing.T) {
		dir := t.TempDir()
		snap := newSnapshot(t, dir)
		snapHashBefore, err := fileSHA256(snap.SnapshotPath)
		if err != nil {
			t.Fatalf("hash snapshot: %v", err)
		}
		_, err = RunRestoreDrill(context.Background(), RunRestoreDrillInput{
			SnapshotPath:  snap.SnapshotPath,
			OutputPath:    snap.SnapshotPath, // source and output must never be shared
			ExpectedScope: testScope(testRucA),
		})
		// The "output" here IS the snapshot path: the never-shared rule must be
		// enforced before any file operation — the snapshot path still holds the
		// original bytes and no candidate was created beside it.
		assertFailedClosed(t, err, ErrRestoreVerificationFailed, snap.SnapshotPath, snap.SnapshotPath, snapHashBefore, snapHashBefore, true)
		if _, statErr := os.Stat(snap.SnapshotPath + ".candidate"); !os.IsNotExist(statErr) {
			t.Fatalf("no candidate may be created when source==output is refused: %v", statErr)
		}
	})

	t.Run("pre-existing output", func(t *testing.T) {
		dir := t.TempDir()
		snap := newSnapshot(t, dir)
		snapHashBefore, err := fileSHA256(snap.SnapshotPath)
		if err != nil {
			t.Fatalf("hash snapshot: %v", err)
		}
		output := filepath.Join(dir, "restored.db")
		preExistingBytes := []byte("pre-existing output — never overwritten")
		if err := os.WriteFile(output, preExistingBytes, 0o600); err != nil {
			t.Fatalf("create pre-existing output: %v", err)
		}
		outputHashBefore, err := fileSHA256(output)
		if err != nil {
			t.Fatalf("hash pre-existing output: %v", err)
		}
		_, err = RunRestoreDrill(context.Background(), RunRestoreDrillInput{
			SnapshotPath:  snap.SnapshotPath,
			OutputPath:    output,
			ExpectedScope: testScope(testRucA),
		})
		assertFailedClosed(t, err, ErrRestoreVerificationFailed, snap.SnapshotPath, output, snapHashBefore, outputHashBefore, true)
	})

	t.Run("interrupted candidate is quarantined", func(t *testing.T) {
		dir := t.TempDir()
		snap := newSnapshot(t, dir)
		snapHashBefore, err := fileSHA256(snap.SnapshotPath)
		if err != nil {
			t.Fatalf("hash snapshot: %v", err)
		}
		output := filepath.Join(dir, "restored.db")
		candidateBytes := []byte("interrupted previous attempt — evidence, never clobbered")
		if err := os.WriteFile(output+".candidate", candidateBytes, 0o600); err != nil {
			t.Fatalf("create interrupted candidate: %v", err)
		}
		_, err = RunRestoreDrill(context.Background(), RunRestoreDrillInput{
			SnapshotPath:  snap.SnapshotPath,
			OutputPath:    output,
			ExpectedScope: testScope(testRucA),
		})
		assertFailedClosed(t, err, ErrRestoreVerificationFailed, snap.SnapshotPath, output, snapHashBefore, "", false)
		kept, err := os.ReadFile(output + ".candidate")
		if err != nil {
			t.Fatalf("read quarantined candidate: %v", err)
		}
		if string(kept) != string(candidateBytes) {
			t.Fatal("the pre-existing interrupted candidate bytes were modified — quarantine violated")
		}
	})
}

// TestRunRestoreDrillScopeIsolation is task 2.7: a readable snapshot containing
// ONLY another company/period fails the scope-conformance check (IR-2) and the
// error never enumerates foreign rows; the SAME snapshot restores successfully
// under its own scope (positive control proving the failure is scope-driven, not
// corruption-driven).
func TestRunRestoreDrillScopeIsolation(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	scopeA := testScope(testRucA)
	scopeB := core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: testOrgID,
		CompanyID:      "acme-b",
		RUC:            testRucB,
		Period:         testPeriod,
	}
	// The snapshot contains ONLY company B data.
	seedLiveStore(t, live, scopeB, 3)
	snap, err := CreateDrillSnapshot(context.Background(), CreateDrillSnapshotInput{
		SourcePath:    live,
		SnapshotPath:  filepath.Join(dir, "snapshot.db"),
		ExpectedScope: scopeB,
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapHashBefore, err := fileSHA256(snap.SnapshotPath)
	if err != nil {
		t.Fatalf("hash snapshot: %v", err)
	}

	t.Run("expected scope A is rejected, foreign rows never enumerated", func(t *testing.T) {
		outputA := filepath.Join(dir, "restored-a.db")
		_, err := RunRestoreDrill(context.Background(), RunRestoreDrillInput{
			SnapshotPath:  snap.SnapshotPath,
			OutputPath:    outputA,
			ExpectedScope: scopeA, // wrong company on purpose
		})
		if !errors.Is(err, ErrRestoreVerificationFailed) {
			t.Fatalf("err = %v, want ErrRestoreVerificationFailed", err)
		}
		if _, statErr := os.Stat(outputA); !os.IsNotExist(statErr) {
			t.Fatalf("output A must not be published: %v", statErr)
		}
		// Cross-tenant invisibility: the failure detail names the EXPECTED scope
		// but must not enumerate the foreign (present) company's identity.
		if strings.Contains(err.Error(), scopeB.CompanyID) || strings.Contains(err.Error(), scopeB.RUC) {
			t.Fatalf("scope failure enumerated foreign rows: %v", err)
		}
		if !strings.Contains(err.Error(), scopeA.CompanyID) {
			t.Fatalf("scope failure must name the expected scope: %v", err)
		}
		snapHashAfter, hashErr := fileSHA256(snap.SnapshotPath)
		if hashErr != nil {
			t.Fatalf("hash snapshot after: %v", hashErr)
		}
		if snapHashAfter != snapHashBefore {
			t.Fatalf("source snapshot changed by the rejected restore (%s → %s)", snapHashBefore, snapHashAfter)
		}
	})

	t.Run("positive control: same snapshot restores under its own scope B", func(t *testing.T) {
		outputB := filepath.Join(dir, "restored-b.db")
		res, err := RunRestoreDrill(context.Background(), RunRestoreDrillInput{
			SnapshotPath:  snap.SnapshotPath,
			OutputPath:    outputB,
			ExpectedScope: scopeB,
		})
		if err != nil {
			t.Fatalf("restore under own scope: %v", err)
		}
		if res.Checks.Scope.Status != StatusOk {
			t.Fatalf("scope conformance for own scope = %+v, want ok", res.Checks.Scope)
		}
		if n := countScopeRows(t, outputB, scopeB); n != 3 {
			t.Fatalf("restored B row count = %d, want 3", n)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// WARNING remediation — strconvAtoi silent-prefix parse (fail-closed fix)
// ─────────────────────────────────────────────────────────────────────────────

// TestSchemaVersionParseFailsClosed pins the fail-closed full-string parse of
// the schema-version reader: the old fmt.Sscanf("%d") accepted "14abc" / " 14"
// / "14 " / "0xE" as silent prefix parses (the exact pattern PR-4 removed from
// comprobante.go) — corruption-detection code must NEVER guess. After the fix,
// only a complete integer string parses.
func TestSchemaVersionParseFailsClosed(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{in: "14", want: 14},
		{in: "14abc", wantErr: true}, // silent-prefix parse must fail closed
		{in: " 14", wantErr: true},   // leading space: Sscanf %d skipped it
		{in: "14 ", wantErr: true},   // trailing space: Sscanf ignored it
		{in: "0xE", wantErr: true},   // hex-looking garbage silently parsed as 0
		{in: "v14", wantErr: true},   // version-prefixed garbage
		{in: "", wantErr: true},      // empty
		{in: "14.5", wantErr: true},  // fractional garbage
	}
	for _, tc := range cases {
		got, err := strconvAtoi(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("strconvAtoi(%q) = %d, nil — silent prefix parse must fail closed", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("strconvAtoi(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("strconvAtoi(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
