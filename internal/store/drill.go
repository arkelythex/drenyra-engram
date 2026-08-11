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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// strconvAtoi wraps strconv.Atoi (kept local so drill.go needs no extra
// imports beyond the standard set used above).
func strconvAtoi(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	return n, nil
}
