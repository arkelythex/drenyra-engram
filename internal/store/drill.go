// G-6 copy-only corruption drill and VACUUM INTO restore drill (spec FR-4/FR-5/
// FR-6, design D-6/D-7/D-8, task 2.3–2.7).
//
// Drill model: a drill NEVER touches a live database. CreateDrillSnapshot makes
// a transactionally consistent standalone snapshot via VACUUM INTO (WAL-safe,
// never a blind copy of a live WAL DB) and writes an atomic sidecar drill
// manifest beside it (<snapshot>.drenyra-drill.json). A drill copy is a MARKED
// artifact: normal store open refuses it with ErrDrillCopyOnly so it can never
// be used as a live writable store. The corruption drill copies the snapshot to
// a dedicated evidence path, deterministically damages a non-header b-tree page,
// runs the full doctor surface on it, and FREEZES writes closed on that drill
// store handle (monotonic latch, no unfreeze). The restore drill copies the
// immutable snapshot to a separate candidate, verifies in order — integrity →
// foreign keys → exact expected scope conformance → backup identity — and only
// then atomically publishes the separate output. No repair and no authorization
// exists anywhere in this surface; money is never involved (money stays whole
// int64 cents elsewhere in the ecosystem).
//
// Sentinel errors: messages begin with the stable code so CLI/operator output
// stays greppable, and errors.Is works for typed programmatic handling.

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

var (
	// ErrStoreWriteFrozen is returned by EVERY write entry point when the drill
	// write-freeze latch is set on that drill store handle: the corrupted copy
	// fails closed before any transaction begins. The latch is monotonic —
	// retry cannot clear it and no unfreeze method exists (design D-8).
	ErrStoreWriteFrozen = errors.New("STORE_WRITE_FROZEN: writes are frozen on this drill copy (corruption detected; restore via the verified drill path)")

	// ErrDrillCopyOnly is returned by normal store open when the database path
	// carries the adjacent <path>.drenyra-drill.json drill marker: a drill
	// artifact must never be opened as a live writable store.
	ErrDrillCopyOnly = errors.New("DRILL_COPY_ONLY: marked drill copy cannot be opened as a live store")

	// ErrDrillCopyRequired is returned when the full diagnostic surface
	// (integrity_check) is requested on anything but a marked drill copy opened
	// through OpenDrillCopy. Full integrity is never reachable through the
	// routine doctor API.
	ErrDrillCopyRequired = errors.New("DRILL_COPY_REQUIRED: full integrity_check requires a marked drill copy (doctor --drill-copy <copy.db> --snapshot-manifest <manifest.json>)")

	// ErrInvalidDrillPath is returned when a drill path fails the canonical
	// path/sidecar contract: source equals copy, a required sidecar is missing,
	// or the manifest/copy paths disagree.
	ErrInvalidDrillPath = errors.New("INVALID_DRILL_PATH: drill path/sidecar contract violation")

	// ErrCorruptionNotDetected is returned by the corruption drill when the
	// full doctor surface does not detect the deliberately applied damage: the
	// drill fails closed rather than claim a detection it cannot prove.
	ErrCorruptionNotDetected = errors.New("CORRUPTION_NOT_DETECTED: full doctor did not detect the drill corruption")

	// ErrRestoreVerificationFailed is returned by the restore drill when any of
	// the four verify-after-restore checks fails (or an input/path precondition
	// fails): the restored output is never published and the candidate stays
	// quarantined.
	ErrRestoreVerificationFailed = errors.New("RESTORE_VERIFICATION_FAILED: verify-after-restore did not pass; output not released")

	// ErrBackupIdentityMismatch is returned by the restore drill when the
	// candidate bytes do not match the snapshot manifest identity (SHA-256 or
	// schema/identity metadata): the output is never published.
	ErrBackupIdentityMismatch = errors.New("BACKUP_IDENTITY_MISMATCH: restored candidate does not match the snapshot identity")
)

// drillMarkerSuffix is the sidecar drill marker name. A database carrying the
// adjacent <path>.drenyra-drill.json marker is a MARKED drill copy: normal store
// open refuses it with ErrDrillCopyOnly so the artifact can never be used as a
// live writable store (design D-6).
const drillMarkerSuffix = ".drenyra-drill.json"

// drillManifestVersion is the current DrillManifest format version (design D-6:
// the manifest records a format version so future formats fail closed).
const drillManifestVersion = 1

// DrillManifest is the sidecar evidence manifest of a drill copy (design D-6).
// It records the format version, the canonical source and copy paths, the
// SHA-256 of the snapshot (VACUUM INTO output) bytes — the backup identity the
// restore drill verifies — the creation timestamp and the expected scope used
// by restore scope-conformance. The JSON is written atomically beside the copy
// and is never modified afterwards (byte-preserved evidence).
type DrillManifest struct {
	FormatVersion int        `json:"formatVersion"`
	DrillCopy     bool       `json:"drillCopy"`
	SourcePath    string     `json:"sourcePath"`
	CopyPath      string     `json:"copyPath"`
	SourceSHA256  string     `json:"sourceSHA256"`
	SchemaVersion int        `json:"schemaVersion"`
	CreatedAt     string     `json:"createdAt"`
	Scope         core.Scope `json:"scope"`
}

// CreateDrillSnapshotInput selects the source and the distinct snapshot output
// for a VACUUM INTO snapshot. ExpectedScope is the exact company scope used by
// restore scope-conformance (empty for a pure corruption drill).
type CreateDrillSnapshotInput struct {
	SourcePath    string
	SnapshotPath  string
	ExpectedScope core.Scope
}

// DrillSnapshot is the result of CreateDrillSnapshot: the consistent standalone
// snapshot path, its sidecar manifest path, the manifest itself and the snapshot
// SHA-256 (the backup identity).
type DrillSnapshot struct {
	SnapshotPath string
	ManifestPath string
	Manifest     DrillManifest
	SHA256       string
}

// CreateDrillSnapshot makes a WAL-safe consistent snapshot of the source via
// VACUUM INTO on a mode=ro connection (design D-7): the source is never
// modified and never blindly copied, the output is a standalone consistent
// database, it is fsynced and hashed, and the sidecar drill manifest is written
// atomically beside it. Distinct canonical paths are required and overwrite is
// refused — a drill never clobbers evidence.
func CreateDrillSnapshot(ctx context.Context, input CreateDrillSnapshotInput) (DrillSnapshot, error) {
	src, err := canonicalPath(input.SourcePath)
	if err != nil {
		return DrillSnapshot{}, fmt.Errorf("%w: resolve source: %v", ErrInvalidDrillPath, err)
	}
	snap, err := canonicalPath(input.SnapshotPath)
	if err != nil {
		return DrillSnapshot{}, fmt.Errorf("%w: resolve snapshot: %v", ErrInvalidDrillPath, err)
	}
	if src == snap {
		return DrillSnapshot{}, fmt.Errorf("%w: source and snapshot path must differ", ErrInvalidDrillPath)
	}
	if _, err := os.Stat(src); err != nil {
		return DrillSnapshot{}, fmt.Errorf("%w: source database not readable: %v", ErrInvalidDrillPath, err)
	}
	if _, err := os.Stat(snap); err == nil {
		return DrillSnapshot{}, fmt.Errorf("%w: snapshot path already exists (never overwrite evidence)", ErrInvalidDrillPath)
	}
	if _, err := os.Stat(snap + drillMarkerSuffix); err == nil {
		return DrillSnapshot{}, fmt.Errorf("%w: snapshot marker already exists (never overwrite evidence)", ErrInvalidDrillPath)
	}

	// mode=ro (not query_only): VACUUM INTO may write its SEPARATE output file
	// but can never modify the source — a transactionally consistent, WAL-safe
	// snapshot without a blind copy of a live WAL database (FR-5).
	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro")
	if err != nil {
		return DrillSnapshot{}, fmt.Errorf("open source read-only: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, snap); err != nil {
		return DrillSnapshot{}, fmt.Errorf("VACUUM INTO snapshot: %w", err)
	}
	if err := fsyncFile(snap); err != nil {
		return DrillSnapshot{}, fmt.Errorf("fsync snapshot: %w", err)
	}
	sum, err := fileSHA256(snap)
	if err != nil {
		return DrillSnapshot{}, fmt.Errorf("hash snapshot: %w", err)
	}
	schemaVersion, err := schemaVersionOf(snap)
	if err != nil {
		return DrillSnapshot{}, fmt.Errorf("read snapshot schema: %w", err)
	}

	manifest := DrillManifest{
		FormatVersion: drillManifestVersion,
		DrillCopy:     true,
		SourcePath:    src,
		CopyPath:      snap,
		SourceSHA256:  sum,
		SchemaVersion: schemaVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Scope:         input.ExpectedScope,
	}
	if err := atomicWriteJSON(snap+drillMarkerSuffix, manifest); err != nil {
		return DrillSnapshot{}, fmt.Errorf("write drill manifest: %w", err)
	}
	return DrillSnapshot{
		SnapshotPath: snap,
		ManifestPath: snap + drillMarkerSuffix,
		Manifest:     manifest,
		SHA256:       sum,
	}, nil
}

// canonicalPath resolves a path to its canonical absolute form: symlinks are
// resolved when the path exists, otherwise absolute+clean. This makes the
// distinctness/aliasing checks (source ≠ copy, copy path equality) immune to
// symlink/canonical-path aliasing (design security section).
func canonicalPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}
	return filepath.Clean(abs), nil
}

// loadDrillManifest reads and validates the drill marker at path: it must
// parse as a DrillManifest at the current format version.
func loadDrillManifest(path string) (DrillManifest, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return DrillManifest{}, fmt.Errorf("%w: read drill manifest: %v", ErrInvalidDrillPath, err)
	}
	var m DrillManifest
	if err := json.Unmarshal(bytes, &m); err != nil {
		return DrillManifest{}, fmt.Errorf("%w: decode drill manifest: %v", ErrInvalidDrillPath, err)
	}
	if m.FormatVersion != drillManifestVersion {
		return DrillManifest{}, fmt.Errorf("%w: unsupported drill manifest format version %d", ErrInvalidDrillPath, m.FormatVersion)
	}
	return m, nil
}

// OpenDrillCopy opens a MARKED drill copy for the full diagnostic surface
// (design D-6): the manifest must be the adjacent <copy>.drenyra-drill.json
// marker, must carry drillCopy:true, its canonical copy path must equal the
// copy path, and the source path must differ from the copy path — then the
// copy is opened read-only (mode=ro, query_only) WITHOUT migrations or pragma
// writes. The returned handle is the only handle on which Doctor(ModeFull) may
// run; normal store open refuses the same file with ErrDrillCopyOnly. The
// manifest's source path is never opened.
func OpenDrillCopy(copyPath, manifestPath string) (*SQLiteStore, error) {
	copyPath, err := canonicalPath(copyPath)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve copy path: %v", ErrInvalidDrillPath, err)
	}
	manifestPath, err = canonicalPath(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve manifest path: %v", ErrInvalidDrillPath, err)
	}
	want := copyPath + drillMarkerSuffix
	if manifestPath != want {
		return nil, fmt.Errorf("%w: snapshot manifest must be the adjacent drill marker %s", ErrInvalidDrillPath, want)
	}
	manifest, err := loadDrillManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if !manifest.DrillCopy {
		return nil, fmt.Errorf("%w: manifest drillCopy is false — not a drill copy", ErrInvalidDrillPath)
	}
	if manifest.CopyPath != copyPath {
		return nil, fmt.Errorf("%w: manifest copyPath %q does not match the copy %q", ErrInvalidDrillPath, manifest.CopyPath, copyPath)
	}
	src, err := canonicalPath(manifest.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve manifest source: %v", ErrInvalidDrillPath, err)
	}
	if src == copyPath {
		return nil, fmt.Errorf("%w: source and copy path must differ (never open the manifest's source)", ErrInvalidDrillPath)
	}

	db, err := sql.Open("sqlite", "file:"+copyPath+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return nil, fmt.Errorf("open drill copy read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &SQLiteStore{db: db, drillCopy: true}, nil
}

// fileSHA256 hashes a file's bytes.
func fileSHA256(path string) (string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), nil
}

// fsyncFile opens the file and fsyncs its bytes so evidence survives a crash.
func fsyncFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// fsyncDir fsyncs a directory so a rename inside it is durable.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// atomicWriteJSON writes value as pretty JSON via temp-file + fsync + rename +
// directory fsync so the sidecar manifest is never observed partially written.
func atomicWriteJSON(path string, value any) error {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, bytes, 0o600); err != nil {
		return err
	}
	if err := fsyncFile(tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return fsyncDir(filepath.Dir(path))
}

// schemaVersionOf reads the schema_version of a standalone database file via a
// read-only connection (no migrations, no writes).
func schemaVersionOf(path string) (int, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	var raw string
	if err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&raw); err != nil {
		return 0, err
	}
	return strconvAtoi(raw)
}

// strconvAtoi strictly parses a FULL integer string via strconv.Atoi. The
// previous fmt.Sscanf("%d") accepted silent prefix parses — "14abc" became 14,
// " 14" and "14 " were tolerated, "0xE" silently became 0 — the exact
// silent-guess pattern PR-4 removed from comprobante.go. Corruption-detection
// code must NEVER guess: any trailing/embedded/leading garbage is a parse
// error, so a tampered schema_meta value fails closed instead of being
// silently misread.
func strconvAtoi(s string) (int, error) {
	return strconv.Atoi(s)
}

// copyFile copies src bytes to dst and fsyncs the output so evidence survives a
// crash. Used by the drills for byte-for-byte copies (snapshot → evidence,
// snapshot → restore candidate).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// ─────────────────────────────────────────────────────────────────────────────
// Corruption drill (design D-8, spec FR-5 / AC-4)
// ─────────────────────────────────────────────────────────────────────────────

// RunCorruptionDrillInput selects the MARKED drill copy (a snapshot produced by
// CreateDrillSnapshot) to damage and the dedicated evidence output path.
// The input snapshot MUST be a marked drill copy — the live database is never
// accepted and never opened.
type RunCorruptionDrillInput struct {
	SnapshotPath string
	EvidencePath string
}

// CorruptionDrillResult is the byte-preserved evidence of one corruption drill:
// the corrupted evidence path, its derived marker manifest, the original
// snapshot identity hash, the corrupted-bytes hash, the full doctor report that
// detected the damage, and the LATCHED drill store handle — the only handle on
// which the write-freeze is observable (the latch is per-handle; retry cannot
// clear it and no unfreeze method exists). The caller MUST Close DrillStore.
type CorruptionDrillResult struct {
	EvidencePath    string
	ManifestPath    string
	Manifest        DrillManifest
	SnapshotSHA256  string
	CorruptedSHA256 string
	Report          DoctorReport
	DrillStore      *SQLiteStore
}

// RunCorruptionDrill executes the AC-4 corruption drill on a MARKED COPY only
// (never the live database — the mark is enforced): it byte-copies the snapshot
// to the dedicated evidence path, derives the evidence marker, deterministically
// damages a non-header b-tree page, hashes the corrupted bytes, opens ONLY that
// evidence copy through the drill-only store, runs the full doctor surface and
// REQUIRES detection. Detection sets the monotonic write-freeze latch on the
// returned drill store handle, so every write entry point returns the typed
// STORE_WRITE_FROZEN error before any transaction begins (retry-proof, no
// unfreeze). On no detection the drill fails closed with CORRUPTION_NOT_DETECTED.
// There is no repair function; the only path back to a usable database is the
// verified restore drill.
func RunCorruptionDrill(ctx context.Context, input RunCorruptionDrillInput) (CorruptionDrillResult, error) {
	snap, err := canonicalPath(input.SnapshotPath)
	if err != nil {
		return CorruptionDrillResult{}, fmt.Errorf("%w: resolve snapshot: %v", ErrInvalidDrillPath, err)
	}
	if _, err := os.Stat(snap + drillMarkerSuffix); err != nil {
		return CorruptionDrillResult{}, fmt.Errorf("%w: the corruption drill operates only on a MARKED drill copy — %s carries no drill marker", ErrDrillCopyRequired, snap)
	}
	manifest, err := loadDrillManifest(snap + drillMarkerSuffix)
	if err != nil {
		return CorruptionDrillResult{}, err
	}
	if !manifest.DrillCopy {
		return CorruptionDrillResult{}, fmt.Errorf("%w: manifest drillCopy is false — not a drill copy", ErrDrillCopyRequired)
	}
	if manifest.CopyPath != snap {
		return CorruptionDrillResult{}, fmt.Errorf("%w: manifest copyPath %q does not match the snapshot %q", ErrInvalidDrillPath, manifest.CopyPath, snap)
	}

	evidence, err := canonicalPath(input.EvidencePath)
	if err != nil {
		return CorruptionDrillResult{}, fmt.Errorf("%w: resolve evidence path: %v", ErrInvalidDrillPath, err)
	}
	if evidence == snap {
		return CorruptionDrillResult{}, fmt.Errorf("%w: evidence path must differ from the snapshot path", ErrInvalidDrillPath)
	}
	if _, err := os.Stat(evidence); err == nil {
		return CorruptionDrillResult{}, fmt.Errorf("%w: evidence path already exists (never overwrite evidence)", ErrInvalidDrillPath)
	}
	if _, err := os.Stat(evidence + drillMarkerSuffix); err == nil {
		return CorruptionDrillResult{}, fmt.Errorf("%w: evidence marker already exists (never overwrite evidence)", ErrInvalidDrillPath)
	}

	// Copy the snapshot bytes to the dedicated evidence path and fsync them.
	if err := copyFile(snap, evidence); err != nil {
		return CorruptionDrillResult{}, fmt.Errorf("%w: copy evidence: %v", ErrInvalidDrillPath, err)
	}

	// Derive the evidence marker from the snapshot manifest: SourcePath = the
	// snapshot, SourceSHA256 = the snapshot identity. The evidence bytes are
	// corrupted AFTER the marker is written; the corrupted-bytes hash is
	// reported separately as CorruptedSHA256 so the evidence trail documents
	// exactly what was damaged.
	evidenceManifest := manifest
	evidenceManifest.SourcePath = snap
	evidenceManifest.CopyPath = evidence
	evidenceManifest.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := atomicWriteJSON(evidence+drillMarkerSuffix, evidenceManifest); err != nil {
		return CorruptionDrillResult{}, fmt.Errorf("%w: write evidence marker: %v", ErrInvalidDrillPath, err)
	}

	// Deterministically damage a NON-header database page of the evidence copy.
	if err := corruptDrillCopyPage(evidence); err != nil {
		return CorruptionDrillResult{}, fmt.Errorf("%w: damage evidence copy: %v", ErrInvalidDrillPath, err)
	}
	corruptedSHA, err := fileSHA256(evidence)
	if err != nil {
		return CorruptionDrillResult{}, fmt.Errorf("%w: hash corrupted evidence: %v", ErrInvalidDrillPath, err)
	}

	// Open ONLY the evidence copy through the drill-only store, run the full
	// doctor surface, and require detection.
	drillStore, report, err := detectDrillCorruption(ctx, evidence)
	if err != nil {
		return CorruptionDrillResult{}, err
	}
	return CorruptionDrillResult{
		EvidencePath:    evidence,
		ManifestPath:    evidence + drillMarkerSuffix,
		Manifest:        evidenceManifest,
		SnapshotSHA256:  manifest.SourceSHA256,
		CorruptedSHA256: corruptedSHA,
		Report:          report,
		DrillStore:      drillStore,
	}, nil
}

// detectDrillCorruption opens the MARKED evidence copy through the drill-only
// store, runs the full doctor surface, and requires detection (design D-8). On
// an integrity failure the monotonic write-freeze latch is set on the returned
// handle, which is returned for the operator to observe the write freeze. On NO
// detection the drill fails closed with CORRUPTION_NOT_DETECTED and the handle
// is closed — nothing is claimed that the check surface did not prove. If the
// full doctor cannot build on the copy (schema-level corruption), the drill
// fails closed with that error and no detection claim.
func detectDrillCorruption(ctx context.Context, evidencePath string) (*SQLiteStore, DoctorReport, error) {
	ds, err := OpenDrillCopy(evidencePath, evidencePath+drillMarkerSuffix)
	if err != nil {
		return nil, DoctorReport{}, err
	}
	report, err := ds.Doctor(ctx, DoctorOptions{Mode: ModeFull})
	if err != nil {
		_ = ds.Close()
		return nil, DoctorReport{}, fmt.Errorf("corruption drill: full doctor could not build on the evidence copy: %w", err)
	}
	if report.IntegrityCheck.Status != StatusFailed {
		_ = ds.Close()
		return nil, report, ErrCorruptionNotDetected
	}
	return ds, report, nil
}

// corruptDrillCopyPage deterministically damages a NON-header database page of
// the evidence copy: it flips the page-type byte (byte 0) of the signing_keys
// b-tree root page, located via sqlite_master so the flip is layout- independent.
// The page-type byte is structural: PRAGMA integrity_check (and quick_check)
// report the damaged b-tree page, so detection is deterministic — while schema
// reads, the schema version and the doctor counts stay healthy so the full
// report still builds. The page is verified to carry a b-tree page type before
// the flip; anything unexpected fails closed rather than corrupting blindly.
func corruptDrillCopyPage(path string) error {
	ro, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return fmt.Errorf("open evidence copy read-only: %w", err)
	}
	defer func() { _ = ro.Close() }()
	var root int
	if err := ro.QueryRow(`SELECT rootpage FROM sqlite_master WHERE type = 'table' AND name = 'signing_keys'`).Scan(&root); err != nil {
		return fmt.Errorf("locate signing_keys root page: %w", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(bytes) < 18 {
		return fmt.Errorf("database file too small to read the page size")
	}
	pageSize := int(binary.BigEndian.Uint16(bytes[16:18]))
	off := (root - 1) * pageSize
	if off < 0 || off+1 > len(bytes) {
		return fmt.Errorf("root page %d out of range", root)
	}
	if got := bytes[off]; got != 0x02 && got != 0x05 && got != 0x0a && got != 0x0d {
		return fmt.Errorf("page %d type byte 0x%02x is not a b-tree page type — refusing to corrupt blindly", root, got)
	}
	bytes[off] ^= 0xFF
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return err
	}
	return fsyncFile(path)
}

// ─────────────────────────────────────────────────────────────────────────────
// Restore drill (design D-7, spec FR-6 / AC-5)
// ─────────────────────────────────────────────────────────────────────────────

const (
	// restoreManifestVersion is the current RestoreManifest format version.
	restoreManifestVersion = 1
	// restoreManifestSuffix is the verified-manifest suffix emitted beside a
	// successfully restored output. It is DISTINCT from the drill marker suffix
	// so the restored output opens as a normal usable database (normal store
	// open only refuses paths carrying the .drenyra-drill.json marker).
	restoreManifestSuffix = ".drenyra-verified.json"
)

// ScopeConformanceCheckResult is the third verify-after-restore check (FR-6
// iii): the restored candidate must contain rows for the EXACT expected company
// scope. A failure names only the expected scope — foreign rows are never
// enumerated (cross-tenant invisibility, IR-2).
type ScopeConformanceCheckResult struct {
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
}

// BackupIdentityCheckResult is the fourth verify-after-restore check (FR-6
// iii/iv): the candidate bytes and schema metadata must match the snapshot
// manifest identity.
type BackupIdentityCheckResult struct {
	Status         CheckStatus `json:"status"`
	Detail         string      `json:"detail"`
	ExpectedSHA256 string      `json:"expectedSHA256,omitempty"`
	ActualSHA256   string      `json:"actualSHA256,omitempty"`
}

// RestoreChecks is the ordered verify-after-restore evidence (FR-6 iv):
// integrity → foreign keys → scope conformance → backup identity.
type RestoreChecks struct {
	Integrity      CheckResult                 `json:"integrity"`
	ForeignKeys    ForeignKeyCheckResult       `json:"foreignKeys"`
	Scope          ScopeConformanceCheckResult `json:"scope"`
	BackupIdentity BackupIdentityCheckResult   `json:"backupIdentity"`
}

// RestoreManifest is the verified-manifest emitted beside a successfully
// restored output. It records the output path, the source snapshot, the
// verified identity (source SHA-256 + schema version), the expected scope and
// the per-check evidence.
type RestoreManifest struct {
	FormatVersion int           `json:"formatVersion"`
	OutputPath    string        `json:"outputPath"`
	SourcePath    string        `json:"sourcePath"`
	SourceSHA256  string        `json:"sourceSHA256"`
	SchemaVersion int           `json:"schemaVersion"`
	Scope         core.Scope    `json:"scope"`
	CreatedAt     string        `json:"createdAt"`
	Checks        RestoreChecks `json:"checks"`
}

// RunRestoreDrillInput selects the immutable VACUUM INTO snapshot and the
// separate requested output. ExpectedScope is the exact company scope the
// scope-conformance check requires (a restore is only verified for the scope
// the operator names — ambiguity fails closed, IR-2).
type RunRestoreDrillInput struct {
	SnapshotPath  string
	OutputPath    string
	ExpectedScope core.Scope
}

// RestoreDrillResult is the published restore evidence: the separate output
// path, the verified manifest beside it, and the per-check results.
type RestoreDrillResult struct {
	OutputPath   string
	ManifestPath string
	Manifest     RestoreManifest
	Checks       RestoreChecks
}

// RunRestoreDrill restores the immutable snapshot to a SEPARATE output and
// runs the four verify-after-restore checks in the FIXED order (FR-6 iii):
// integrity_check → foreign_key_check → exact expected scope-row conformance →
// expected backup identity (manifest SHA-256 + schema metadata). Only after ALL
// four pass is the candidate atomically renamed to the requested output and a
// verified manifest emitted beside it. ANY failure returns a typed rejection
// (RESTORE_VERIFICATION_FAILED / BACKUP_IDENTITY_MISMATCH), leaves the source
// snapshot untouched, leaves the rejected candidate quarantined, and never
// publishes the output. A readable database alone is insufficient.
func RunRestoreDrill(ctx context.Context, input RunRestoreDrillInput) (RestoreDrillResult, error) {
	snap, err := canonicalPath(input.SnapshotPath)
	if err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: resolve snapshot: %v", ErrRestoreVerificationFailed, err)
	}
	out, err := canonicalPath(input.OutputPath)
	if err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: resolve output path: %v", ErrRestoreVerificationFailed, err)
	}
	if snap == out {
		return RestoreDrillResult{}, fmt.Errorf("%w: source and output path must never be shared", ErrRestoreVerificationFailed)
	}
	if _, err := os.Stat(out); err == nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: output path already exists (never overwrite evidence)", ErrRestoreVerificationFailed)
	}
	if _, err := os.Stat(out + restoreManifestSuffix); err == nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: verified manifest path already exists (never overwrite evidence)", ErrRestoreVerificationFailed)
	}
	candidate := out + ".candidate"
	if _, err := os.Stat(candidate); err == nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: interrupted candidate %s already exists — quarantined evidence; remove it explicitly before retrying", ErrRestoreVerificationFailed, candidate)
	}
	// The restore verifies a MARKED drill snapshot: the adjacent marker is the
	// backup identity the fourth check compares against.
	if _, err := os.Stat(snap + drillMarkerSuffix); err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: snapshot is not a MARKED drill copy (missing %s) — identity cannot be verified", ErrRestoreVerificationFailed, snap+drillMarkerSuffix)
	}
	manifest, err := loadDrillManifest(snap + drillMarkerSuffix)
	if err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: %v", ErrRestoreVerificationFailed, err)
	}
	if !manifest.DrillCopy || manifest.CopyPath != snap {
		return RestoreDrillResult{}, fmt.Errorf("%w: snapshot manifest is not a valid marked drill copy for %s", ErrRestoreVerificationFailed, snap)
	}

	// Copy the immutable snapshot bytes to the separate candidate and fsync
	// file and directory so the candidate is durable (and stays quarantined on
	// any failure).
	if err := copyFile(snap, candidate); err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: copy candidate: %v", ErrRestoreVerificationFailed, err)
	}
	if err := fsyncDir(filepath.Dir(candidate)); err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: fsync candidate directory: %v", ErrRestoreVerificationFailed, err)
	}

	cand, err := sql.Open("sqlite", "file:"+candidate+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: open candidate read-only: %v", ErrRestoreVerificationFailed, err)
	}
	defer func() { _ = cand.Close() }()

	var checks RestoreChecks

	// Check 1 — integrity. On corruption the candidate is rejected here; the
	// typed RESTORE_VERIFICATION_FAILED (not BACKUP_IDENTITY_MISMATCH) proves
	// the integrity check ran before the identity check.
	checks.Integrity = scanCheck(ctx, cand, "integrity_check")
	if checks.Integrity.Status != StatusOk {
		return RestoreDrillResult{}, fmt.Errorf("%w: integrity_check failed on the restored candidate: %s", ErrRestoreVerificationFailed, checks.Integrity.Detail)
	}

	// Check 2 — foreign keys (always paired with the integrity check, FZ-4).
	checks.ForeignKeys = scanFKCheck(ctx, cand)
	if checks.ForeignKeys.Status != StatusOk {
		return RestoreDrillResult{}, fmt.Errorf("%w: foreign_key_check failed on the restored candidate: %s", ErrRestoreVerificationFailed, checks.ForeignKeys.Detail)
	}

	// Check 3 — exact expected scope-row conformance (fails closed on mismatch;
	// never enumerates foreign rows).
	checks.Scope = scopeConformanceCheck(ctx, cand, input.ExpectedScope)
	if checks.Scope.Status != StatusOk {
		return RestoreDrillResult{}, fmt.Errorf("%w: %s", ErrRestoreVerificationFailed, checks.Scope.Detail)
	}

	// Check 4 — expected backup identity: candidate bytes and schema metadata
	// must match the snapshot manifest identity.
	candHash, err := fileSHA256(candidate)
	if err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: hash candidate: %v", ErrRestoreVerificationFailed, err)
	}
	candSchema, schemaErr := schemaVersionOf(candidate)
	checks.BackupIdentity = BackupIdentityCheckResult{
		Status:         StatusOk,
		Detail:         "candidate bytes and schema version match the snapshot manifest identity",
		ExpectedSHA256: manifest.SourceSHA256,
		ActualSHA256:   candHash,
	}
	if schemaErr != nil || candHash != manifest.SourceSHA256 || candSchema != manifest.SchemaVersion {
		checks.BackupIdentity.Status = StatusFailed
		checks.BackupIdentity.Detail = fmt.Sprintf(
			"candidate identity does not match the snapshot manifest (expected sha256 %s, got %s; expected schema %d, got %d)",
			manifest.SourceSHA256, candHash, manifest.SchemaVersion, candSchema)
		return RestoreDrillResult{}, fmt.Errorf("%w: %s", ErrBackupIdentityMismatch, checks.BackupIdentity.Detail)
	}

	// All four checks pass → atomically publish the separate output (rename,
	// never a second copy — the output bytes are exactly the verified bytes)
	// and emit the verified manifest beside it.
	if err := os.Rename(candidate, out); err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: publish output: %v", ErrRestoreVerificationFailed, err)
	}
	if err := fsyncDir(filepath.Dir(out)); err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: fsync output directory: %v", ErrRestoreVerificationFailed, err)
	}
	verified := RestoreManifest{
		FormatVersion: restoreManifestVersion,
		OutputPath:    out,
		SourcePath:    snap,
		SourceSHA256:  manifest.SourceSHA256,
		SchemaVersion: manifest.SchemaVersion,
		Scope:         input.ExpectedScope,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Checks:        checks,
	}
	manifestPath := out + restoreManifestSuffix
	if err := atomicWriteJSON(manifestPath, verified); err != nil {
		return RestoreDrillResult{}, fmt.Errorf("%w: write verified manifest: %v", ErrRestoreVerificationFailed, err)
	}
	return RestoreDrillResult{
		OutputPath:   out,
		ManifestPath: manifestPath,
		Manifest:     verified,
		Checks:       checks,
	}, nil
}

// scopeConformanceCheck verifies the EXACT expected company scope rows are
// present in the restored candidate (FR-6 iii, task 2.7). It never enumerates
// foreign rows: a failure names only the expected scope (cross-tenant
// invisibility, IR-2). An expected scope that is not an exact company scope
// fails closed.
func scopeConformanceCheck(ctx context.Context, db *sql.DB, scope core.Scope) ScopeConformanceCheckResult {
	if scope.Kind != core.ScopeKindCompany || scope.OrganizationID == "" || scope.CompanyID == "" || scope.RUC == "" {
		return ScopeConformanceCheckResult{
			Status: StatusFailed,
			Detail: "scope conformance: expected scope must be an exact company scope (kind=company with organization, company and RUC)",
		}
	}
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM observations
		WHERE scope_kind = ? AND organization_id = ? AND company_id = ? AND ruc = ? AND period = ?`,
		string(core.ScopeKindCompany), scope.OrganizationID, scope.CompanyID, scope.RUC, scope.Period,
	).Scan(&n)
	if err != nil {
		return ScopeConformanceCheckResult{
			Status: StatusFailed,
			Detail: fmt.Sprintf("scope conformance: candidate scope unreadable: %v", err),
		}
	}
	if n == 0 {
		return ScopeConformanceCheckResult{
			Status: StatusFailed,
			Detail: fmt.Sprintf("scope conformance: 0 rows for the expected company scope (organization %s / company %s / ruc %s / period %s) — the restored candidate does not contain the expected scope",
				scope.OrganizationID, scope.CompanyID, scope.RUC, scope.Period),
		}
	}
	return ScopeConformanceCheckResult{
		Status: StatusOk,
		Detail: fmt.Sprintf("scope conformance: %d rows present for the exact expected company scope", n),
	}
}
