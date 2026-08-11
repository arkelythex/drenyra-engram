// G-6 SQLite health-check surface (spec FZ-4/FR-4, design D-5, task 2.1/2.2):
// the routine doctor path runs PRAGMA quick_check then PRAGMA foreign_key_check
// and reports integrityCheck as an explicit "not_run" — full integrity_check is
// reachable ONLY through the drill path (DoctorOptions{Mode: ModeFull} on a
// marked drill copy) and is always paired with foreign_key_check. cell_size_check
// is enabled at connection initialization where compatible and the report always
// states the effective value.
//
// Money convention: no monetary value appears in these health checks (money is
// whole int64 cents elsewhere in the ecosystem; nothing here touches money).

package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// corruptSigningKeysRootTypeByte deterministically flips the page-type byte
// (byte 0) of the signing_keys b-tree root page — a NON-header database page
// that the routine doctor never counts (verified: quick_check and
// integrity_check both fail, while schema reads, schema version, and every
// doctor count stay healthy, so the report still builds). The page-type byte is
// structural: flipping it makes PRAGMA quick_check / integrity_check report the
// damaged b-tree page ("btreeInitPage() returns error code"), so detection is
// deterministic and layout-independent (the root is looked up in sqlite_master).
func corruptSigningKeysRootTypeByte(t *testing.T, path string) {
	t.Helper()
	ro, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		t.Fatalf("open %s read-only: %v", path, err)
	}
	defer func() { _ = ro.Close() }()
	var root int
	if err := ro.QueryRow(`SELECT rootpage FROM sqlite_master WHERE type = 'table' AND name = 'signing_keys'`).Scan(&root); err != nil {
		t.Fatalf("locate signing_keys root in %s: %v", path, err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	pageSize := int(binary.BigEndian.Uint16(bytes[16:18]))
	off := (root - 1) * pageSize
	if got := bytes[off]; got != 0x02 && got != 0x05 && got != 0x0a && got != 0x0d {
		t.Fatalf("page %d type byte 0x%02x is not a b-tree page type at %s", root, got, path)
	}
	bytes[off] ^= 0xFF
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		t.Fatalf("write corrupted %s: %v", path, err)
	}
}

// TestDoctorRoutineRunsQuickCheckThenForeignKeyCheck pins the AC-3 routine
// contract: quick_check then foreign_key_check, integrityCheck explicitly
// not_run, cell_size_check effective value reported. The query-order trace is
// the store's own instrumentation (doctorTrace); the corrupted-store test below
// proves behaviorally that integrity_check truly never executes on routine
// doctor.
func TestDoctorRoutineRunsQuickCheckThenForeignKeyCheck(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(validInput("tax.igv.rate", "first version")); err != nil {
		t.Fatalf("save: %v", err)
	}

	report, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	if report.QuickCheck.Status != "ok" {
		t.Fatalf("quickCheck.status = %q, want ok (detail %q)", report.QuickCheck.Status, report.QuickCheck.Detail)
	}
	if report.QuickCheck.Detail == "" {
		t.Fatal("quickCheck.detail must not be empty")
	}
	if report.IntegrityCheck.Status != "not_run" {
		t.Fatalf("integrityCheck.status = %q, want not_run on routine doctor", report.IntegrityCheck.Status)
	}
	if report.ForeignKeyCheck.Status != "ok" {
		t.Fatalf("foreignKeyCheck.status = %q, want ok (detail %q)", report.ForeignKeyCheck.Status, report.ForeignKeyCheck.Detail)
	}
	if report.ForeignKeyCheck.ViolationCount != 0 {
		t.Fatalf("foreignKeyCheck.violationCount = %d, want 0 on a clean store", report.ForeignKeyCheck.ViolationCount)
	}
	if report.ForeignKeyCheck.Detail == "" {
		t.Fatal("foreignKeyCheck.detail must not be empty")
	}
	if report.CellSizeCheck.Effective != "on" {
		t.Fatalf("cellSizeCheck.effective = %q, want on (enabled at connection init; detail %q)",
			report.CellSizeCheck.Effective, report.CellSizeCheck.Detail)
	}
	if report.CellSizeCheck.Detail == "" {
		t.Fatal("cellSizeCheck.detail must not be empty")
	}

	// Query-order contract (query-hook instrumentation): quick_check FIRST, then
	// foreign_key_check, and never integrity_check on the routine path.
	want := []string{"quick_check", "foreign_key_check"}
	if strings.Join(s.doctorTrace, ",") != strings.Join(want, ",") {
		t.Fatalf("doctor statement trace = %v, want %v", s.doctorTrace, want)
	}
	for _, stmt := range s.doctorTrace {
		if strings.Contains(stmt, "integrity_check") {
			t.Fatalf("routine doctor ran %q — integrity_check must never run on the routine path", stmt)
		}
	}
}

// TestDoctorRoutineNeverRunsIntegrityCheckOnCorruptStore proves behaviorally
// that the routine path does not execute integrity_check: a store whose
// signing_keys b-tree root is damaged still opens and reports quickCheck failed,
// while integrityCheck stays not_run — if integrity_check had run, it would have
// reported failed on this exact store.
func TestDoctorRoutineNeverRunsIntegrityCheckOnCorruptStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 60; i++ {
		in := validInput("tax.igv.rate", "version")
		in.TopicKey = string(rune('a'+i%26)) + "tax"
		if _, err := s.Save(in); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	corruptSigningKeysRootTypeByte(t, path)

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen corrupted store: %v", err)
	}
	defer func() { _ = s2.Close() }()

	report, err := s2.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
	if err != nil {
		t.Fatalf("doctor on corrupted store: %v", err)
	}
	if report.QuickCheck.Status != "failed" {
		t.Fatalf("quickCheck.status = %q on corrupted store, want failed (detail %q)",
			report.QuickCheck.Status, report.QuickCheck.Detail)
	}
	if report.QuickCheck.Detail == "" {
		t.Fatal("quickCheck.detail must carry the corruption evidence")
	}
	// The behavioral proof: integrity_check WOULD have failed on this store, so a
	// not_run status proves it never executed.
	if report.IntegrityCheck.Status != "not_run" {
		t.Fatalf("integrityCheck.status = %q, want not_run (integrity_check must never run on routine doctor)", report.IntegrityCheck.Status)
	}
	want := []string{"quick_check", "foreign_key_check"}
	if strings.Join(s2.doctorTrace, ",") != strings.Join(want, ",") {
		t.Fatalf("doctor statement trace = %v, want %v", s2.doctorTrace, want)
	}
}

// TestDoctorFullRequiresMarkedDrillCopy pins the drill-only boundary: full
// integrity_check is not reachable through the routine API — a normal (live)
// store must refuse ModeFull with the typed DRILL_COPY_REQUIRED error.
func TestDoctorFullRequiresMarkedDrillCopy(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(validInput("tax.igv.rate", "first version")); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeFull})
	if err == nil {
		t.Fatal("Doctor(ModeFull) on a live store must fail closed (DRILL_COPY_REQUIRED)")
	}
	if !errors.Is(err, ErrDrillCopyRequired) {
		t.Fatalf("err = %v, want ErrDrillCopyRequired", err)
	}
}
