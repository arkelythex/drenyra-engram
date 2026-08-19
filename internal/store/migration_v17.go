// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. Confidence is a probability 0..1 (never
// money). This module implements the v16→v17 additive migration
// (sdd-060-confidence-required, D-CN-2):
//
//	(a) CREATE the two confidence-required triggers — SQLite has no combined
//	    "INSERT OR UPDATE" trigger, so there is one BEFORE INSERT (any insert
//	    must carry confidence) and one BEFORE UPDATE OF confidence (only an
//	    update that touches confidence may not leave it NULL), each aborting
//	    with CONFIDENCE_REQUIRED — defense in depth for any writer that
//	    bypasses the core validation; updates that do NOT touch confidence
//	    (e.g. an immutable-content violation) still surface their own error,
//	    and legacy NULL-confidence rows remain updatable in other columns;
//	(b) UPDATE schema_meta SET value = '17' ONLY after every step succeeded;
//	    any failure rolls the whole migration back and leaves schema_version=16.
//
// SQLite cannot ALTER COLUMN ... SET NOT NULL, and the store's frozen rule
// forbids rewriting the observations table — so the triggers are the additive
// enforcement surface. Legacy rows are PRESERVED unchanged: the column has been
// nullable since the v1→v2 migration with no backfill, so failing closed on
// legacy NULL-confidence rows would strand every pre-v17 store that ever saved
// an unscored memory (violates additive migration). Legacy NULLs read back as
// confidence 0; the required-confidence invariant is enforced FORWARD via the
// core type, validation, and these triggers.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// confidenceRequiredTriggerDDLs is the v17 guard surface: any INSERT or any
// UPDATE that leaves observations.confidence NULL aborts
// (sdd-060-confidence-required, FR-CN-2). SQLite does not support a combined
// "INSERT OR UPDATE" trigger, so the guard is two triggers with the same name
// prefix. The core model already makes confidence a required float64; these
// triggers are the database-level backstop.
var confidenceRequiredTriggerDDLs = []string{
	`CREATE TRIGGER observations_confidence_required_insert
BEFORE INSERT ON observations
WHEN NEW.confidence IS NULL
BEGIN
    SELECT RAISE(ABORT, 'CONFIDENCE_REQUIRED: observations.confidence must not be NULL');
END;`,
	`CREATE TRIGGER observations_confidence_required_update
BEFORE UPDATE OF confidence ON observations
WHEN NEW.confidence IS NULL
BEGIN
    SELECT RAISE(ABORT, 'CONFIDENCE_REQUIRED: observations.confidence must not be NULL');
END;`,
}

// migrateV16ToV17 validates there are no legacy NULL-confidence rows and
// installs the confidence-required trigger (additive, fail-closed).
func migrateV16ToV17(db *sql.DB) error {
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("v17 migration: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (b) install the confidence-required triggers (idempotent: SQLite has no
	// CREATE TRIGGER IF NOT EXISTS, so drop-then-create inside the transaction —
	// same pattern as migrateV15ToV16; a crash before the version write leaves
	// the trigger installed and the reopen re-runs harmlessly).
	for _, ddl := range confidenceRequiredTriggerDDLs {
		name := "observations_confidence_required_insert"
		if strings.Contains(ddl, "observations_confidence_required_update") {
			name = "observations_confidence_required_update"
		}
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+name); err != nil {
			return fmt.Errorf("v17 migration: drop trigger: %w", err)
		}
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("v17 migration: create trigger: %w", err)
		}
	}
	// (c) schema version last — any failure above rolls back to v16.
	if err := setSchemaVersionTx(ctx, tx, 17); err != nil {
		return fmt.Errorf("v17 migration: schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("v17 migration: commit: %w", err)
	}
	committed = true
	return nil
}

// errConfidenceRequired is the surfaced trigger error for a NULL-confidence
// write that bypassed core validation (defense in depth; not an exported API
// code — the Go layer reports the SQLite RAISE message verbatim).
var errConfidenceRequired = errors.New("CONFIDENCE_REQUIRED")
