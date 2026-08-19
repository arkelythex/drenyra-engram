// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module implements the v14→v15 additive
// migration (sdd-060-at-rest-encryption, FR-ENC-3):
//
//	(a) validate that observations has NONE of the content_cipher /
//	    content_nonce / content_algo columns (a pre-existing column is a
//	    corruption signal; abort — additive migrations never replay);
//	(b) ALTER TABLE observations ADD COLUMN content_cipher BLOB;
//	    ALTER TABLE observations ADD COLUMN content_nonce BLOB;
//	    ALTER TABLE observations ADD COLUMN content_algo TEXT NOT NULL DEFAULT '';
//	(c) UPDATE schema_meta SET value = '15' ONLY after every step succeeded;
//	    any failure rolls the whole migration back and leaves schema_version=14.
//
// No existing row is backfilled or re-encrypted: content_algo = ” marks legacy
// plaintext rows, readable in both encryption modes (FR-ENC-4). Fresh stores
// reach the same layout through the migration chain in Open.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// migrateV14ToV15 adds the at-rest content-encryption columns to observations.
func migrateV14ToV15(db *sql.DB) error {
	ctx := context.Background()
	for _, col := range []string{"content_cipher", "content_nonce", "content_algo"} {
		var found int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('observations') WHERE name = ?`, col).Scan(&found); err != nil {
			return fmt.Errorf("v15 migration: inspect column %s: %w", col, err)
		}
		if found != 0 {
			return fmt.Errorf("v15 migration: observations.%s already exists — additive migrations never replay", col)
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("v15 migration: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	statements := []string{
		`ALTER TABLE observations ADD COLUMN content_cipher BLOB`,
		`ALTER TABLE observations ADD COLUMN content_nonce BLOB`,
		`ALTER TABLE observations ADD COLUMN content_algo TEXT NOT NULL DEFAULT ''`,
	}
	// The statements are a hardcoded allowlist (never user input) — the three
	// column additions are fixed migration constants.
	for i := range statements {
		stmt := statements[i]
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("v15 migration: %w", err)
		}
	}
	if err := setSchemaVersionTx(ctx, tx, 15); err != nil {
		return fmt.Errorf("v15 migration: schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("v15 migration: commit: %w", err)
	}
	committed = true
	return nil
}

// setSchemaVersionTx writes the schema version inside the migration transaction.
func setSchemaVersionTx(ctx context.Context, tx *sql.Tx, version int) error {
	res, err := tx.ExecContext(ctx, `UPDATE schema_meta SET value = ? WHERE key = 'schema_version'`, fmt.Sprintf("%d", version))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("schema_meta version row missing")
	}
	return nil
}
