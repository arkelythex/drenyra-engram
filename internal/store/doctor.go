// G-6 SQLite health-check surface (spec FZ-4/FR-4, design D-5, task 2.1/2.2).
//
// The routine doctor path runs PRAGMA quick_check then PRAGMA foreign_key_check
// and reports integrityCheck as an explicit "not_run". Full PRAGMA
// integrity_check is reachable ONLY through the drill path — Doctor with
// DoctorOptions{Mode: ModeFull} on a MARKED DRILL COPY (opened via
// OpenDrillCopy) — and is always paired with foreign_key_check. Live stores
// refuse ModeFull with ErrDrillCopyRequired. cell_size_check=ON is enabled at
// connection initialization where compatible, and the report always states the
// effective value so the evidence is inspectable. All checks are read-only
// observations: they never mutate the database.
//
// Detection semantics (design D-8): when the full path observes an integrity
// failure, the process-local, monotonic write-freeze latch on that drill store
// is set; every write entry point checks the latch before beginning a
// transaction and returns typed STORE_WRITE_FROZEN. There is no unfreeze path.
//
// Money convention: these checks never touch monetary values (money stays whole
// int64 cents elsewhere in the ecosystem; nothing here participates in money
// arithmetic).

package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// CheckStatus is the frozen status vocabulary of the SQLite health checks.
// "not_run" is explicit and never omitted so the report is always inspectable.
type CheckStatus string

const (
	StatusOk     CheckStatus = "ok"
	StatusFailed CheckStatus = "failed"
	StatusNotRun CheckStatus = "not_run"
)

// CheckResult is the {status, detail} pair shared by quickCheck and
// integrityCheck (design D-5). A failed check always carries the PRAGMA detail
// (for corruption, the multi-line "*** in database main ***" report).
type CheckResult struct {
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
}

// ForeignKeyCheckResult reports PRAGMA foreign_key_check: ok with a violation
// count of 0, or failed with the violation count and detail. It runs on BOTH
// routine and full paths (paired with every integrity check).
type ForeignKeyCheckResult struct {
	Status         CheckStatus `json:"status"`
	ViolationCount int         `json:"violationCount"`
	Detail         string      `json:"detail"`
}

// CellSizeCheckResult reports the EFFECTIVE PRAGMA cell_size_check value ("on"
// or "off") plus why — the report always states it so the evidence is
// inspectable (FZ-4).
type CellSizeCheckResult struct {
	Effective string `json:"effective"`
	Detail    string `json:"detail"`
}

// DoctorMode selects the health-check surface (design D-5).
type DoctorMode int

const (
	// ModeRoutine runs quick_check then foreign_key_check; integrityCheck
	// reports "not_run". This is the ONLY mode reachable on live stores.
	ModeRoutine DoctorMode = iota
	// ModeFull runs integrity_check then foreign_key_check on a MARKED DRILL
	// COPY only; live stores refuse it with ErrDrillCopyRequired. An integrity
	// failure sets the monotonic write-freeze latch on that drill store handle.
	ModeFull
)

// DoctorOptions configures a Doctor run. The zero value (ModeRoutine) is the
// safe default — full integrity is never reachable implicitly.
type DoctorOptions struct {
	Mode DoctorMode
}

// scanCheck executes one PRAGMA health check (quick_check or integrity_check)
// on the given connection and returns its result. The pragma returns one row on
// a healthy database and a multi-line report on corruption; every row is
// collected into Detail. Shared by the doctor methods (which append to
// doctorTrace) and by the restore drill's verify-after-restore (which runs the
// identical check on the read-only candidate).
func scanCheck(ctx context.Context, db *sql.DB, pragma string) CheckResult {
	rows, err := db.QueryContext(ctx, `PRAGMA `+pragma)
	if err != nil {
		return CheckResult{Status: StatusFailed, Detail: fmt.Sprintf("%s: %v", pragma, err)}
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return CheckResult{Status: StatusFailed, Detail: fmt.Sprintf("%s: %v", pragma, err)}
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return CheckResult{Status: StatusFailed, Detail: fmt.Sprintf("%s: %v", pragma, err)}
	}
	if len(lines) == 0 {
		return CheckResult{Status: StatusFailed, Detail: fmt.Sprintf("%s returned no rows", pragma)}
	}
	if lines[0] == "ok" {
		return CheckResult{Status: StatusOk, Detail: strings.Join(lines, "\n")}
	}
	return CheckResult{Status: StatusFailed, Detail: strings.Join(lines, "\n")}
}

// scanFKCheck executes PRAGMA foreign_key_check on the given connection and
// counts violations. A scan failure on a damaged store is reported as failed
// (never a fabricated zero). Shared by the doctor methods and the restore
// drill's verify-after-restore.
func scanFKCheck(ctx context.Context, db *sql.DB) ForeignKeyCheckResult {
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return ForeignKeyCheckResult{
			Status: StatusFailed,
			Detail: fmt.Sprintf("foreign_key_check: %v", err),
		}
	}
	defer func() { _ = rows.Close() }()
	violations := 0
	for rows.Next() {
		violations++
	}
	if err := rows.Err(); err != nil {
		return ForeignKeyCheckResult{
			Status: StatusFailed,
			Detail: fmt.Sprintf("foreign_key_check: %v", err),
		}
	}
	if violations > 0 {
		return ForeignKeyCheckResult{
			Status:         StatusFailed,
			ViolationCount: violations,
			Detail:         fmt.Sprintf("%d foreign key violation(s)", violations),
		}
	}
	return ForeignKeyCheckResult{
		Status: StatusOk,
		Detail: "no foreign key violations",
	}
}

// runCheck executes one PRAGMA health check through the store's connection,
// recording the statement in doctorTrace (query-order instrumentation).
func (s *SQLiteStore) runCheck(ctx context.Context, pragma string) CheckResult {
	s.doctorTrace = append(s.doctorTrace, pragma)
	return scanCheck(ctx, s.db, pragma)
}

// runFKCheck executes PRAGMA foreign_key_check through the store's connection,
// recording the statement in doctorTrace (query-order instrumentation).
func (s *SQLiteStore) runFKCheck(ctx context.Context) ForeignKeyCheckResult {
	s.doctorTrace = append(s.doctorTrace, "foreign_key_check")
	return scanFKCheck(ctx, s.db)
}

// cellSizeCheck reads the EFFECTIVE PRAGMA cell_size_check value. The pragma is
// enabled at connection initialization (OpenWithObjects); the report always
// states the effective value so the evidence is inspectable (FZ-4).
func (s *SQLiteStore) cellSizeCheck(ctx context.Context) CellSizeCheckResult {
	var raw int
	if err := s.db.QueryRowContext(ctx, `PRAGMA cell_size_check`).Scan(&raw); err != nil {
		return CellSizeCheckResult{
			Effective: "off",
			Detail:    fmt.Sprintf("cell_size_check unreadable on this connection: %v", err),
		}
	}
	if raw == 1 {
		return CellSizeCheckResult{
			Effective: "on",
			Detail:    "PRAGMA cell_size_check = ON (enabled at connection initialization)",
		}
	}
	return CellSizeCheckResult{
		Effective: "off",
		Detail:    "PRAGMA cell_size_check = OFF (not enabled on this connection)",
	}
}
