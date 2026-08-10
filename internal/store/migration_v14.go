// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module implements the v13→v14 additive
// migration (v0.6.0 Batch 2 — structured rule links, docs/architecture/
// fiscal-policy-memory-v0.6.md §2.2/§3):
//
//	(a) validate that rule_links has NEITHER the version/effective_at columns
//	    NOR the idx_rule_links_ref index (a pre-existing column/index is a
//	    corruption signal; abort — additive migrations never replay);
//	(b) ALTER TABLE rule_links ADD COLUMN version TEXT NULL;
//	    ALTER TABLE rule_links ADD COLUMN effective_at TEXT NULL;
//	    CREATE INDEX idx_rule_links_ref ON rule_links(ref, version,
//	    effective_at, memory_id);
//	(c) UPDATE schema_meta SET value = '14' ONLY after every step succeeded;
//	    any failure rolls the whole migration back and leaves schema_version=13.
//
// No existing row is backfilled or re-hashed — NULL version/effective_at means
// legacy/unversioned (choosing a historical revision is a fiscal assertion and
// is never done automatically). Fresh stores reach the same layout through the
// migration chain in Open (applySchema keeps the v2 base DDL; the v13→v14
// migration is the sole carrier of the new columns, exactly like the
// observations.policy_rule_json / close_snapshot_json column additions).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ruleLinksV14IndexDDL is the reverse lookup index the Batch 3 impact service
// consumes (design §5): exact ref → versioned/unversioned link rows.
const ruleLinksV14IndexDDL = `
CREATE INDEX idx_rule_links_ref
    ON rule_links (ref, version, effective_at, memory_id);
`

// migrateV13ToV14 upgrades a schema_version=13 store to v14 IN ONE fail-closed
// transaction (design §3): the two structured-link columns on rule_links and
// the reverse lookup index, then schema_version=14. The columns are validated
// ABSENT before mutation; a pre-existing column or index aborts the whole
// migration (additive migrations never replay) and the store stays v13.
func migrateV13ToV14(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate v13→v14: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// (a) Fail closed on a pre-existing column or index: the chain is additive
	// and never replays — a foreign or partial migration is a corruption signal.
	columns, err := tableColumns(ctx, tx, "rule_links")
	if err != nil {
		return fmt.Errorf("migrate v13→v14: read rule_links columns: %w", err)
	}
	for _, column := range []string{"version", "effective_at"} {
		if columns[column] {
			return fmt.Errorf("migrate v13→v14: rule_links.%s already exists — foreign or partial migration, fail closed", column)
		}
	}
	var existing string
	err = tx.QueryRowContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_rule_links_ref'
		LIMIT 1`).Scan(&existing)
	switch {
	case err == nil:
		return fmt.Errorf("migrate v13→v14: pre-existing index %q — corruption signal, abort (additive migrations never replay)", existing)
	case errors.Is(err, sql.ErrNoRows):
		// clean — proceed
	default:
		return fmt.Errorf("migrate v13→v14: inspect existing indexes: %w", err)
	}

	// (b) The structured-link columns + the reverse lookup index.
	if _, err := tx.ExecContext(ctx, `ALTER TABLE rule_links ADD COLUMN version TEXT NULL`); err != nil {
		return fmt.Errorf("migrate v13→v14: add rule_links.version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE rule_links ADD COLUMN effective_at TEXT NULL`); err != nil {
		return fmt.Errorf("migrate v13→v14: add rule_links.effective_at: %w", err)
	}
	if _, err := tx.ExecContext(ctx, ruleLinksV14IndexDDL); err != nil {
		return fmt.Errorf("migrate v13→v14: create idx_rule_links_ref: %w", err)
	}

	// (c) schema_version = 14 ONLY after the whole migration succeeded — same
	// transaction, so a failure above rolls everything back.
	if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = '14' WHERE key = 'schema_version'`); err != nil {
		return fmt.Errorf("migrate v13→v14: set schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate v13→v14: commit: %w", err)
	}
	committed = true
	return nil
}
