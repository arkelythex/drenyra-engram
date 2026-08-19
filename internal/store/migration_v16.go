// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module implements the v15→v16 additive
// migration (sdd-060-legacy-reencrypt, D-RE-7):
//
//	(a) validate that the observations_immutable_content trigger EXISTS (a
//	    missing trigger is a corruption signal; abort);
//	(b) DROP TRIGGER observations_immutable_content;
//	(c) CREATE the v16 trigger: the ONLY content mutation it permits is the
//	    exact legacy-plaintext → encrypted-at-rest transition (content_algo
//	    passes from '' to non-empty, the four plaintext content columns are
//	    emptied, and EVERY other immutable column is byte-identical); any
//	    other UPDATE touching the immutable columns aborts exactly as before;
//	(d) UPDATE schema_meta SET value = '16' ONLY after every step succeeded;
//	    any failure rolls the whole migration back and leaves schema_version=15.
//
// The refinement is what makes `encrypt --apply` (legacy re-encryption) legal
// without weakening immutability: the decrypted memory is byte-identical, so
// content/identity/envelope hashes, receipts, relations and the transition log
// are untouched by construction.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// immutabilityTriggerV16DDL is the v16 observations guard: the v7 column list
// plus the ONLY permitted content transition — legacy plaintext → encrypted
// at rest (sdd-060-legacy-reencrypt). Every other UPDATE touching the immutable
// columns aborts with the frozen error, exactly as before.
const immutabilityTriggerV16DDL = `
CREATE TRIGGER observations_immutable_content
BEFORE UPDATE OF id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period,
                     what, why, where_text, learned, fiscal_effect, effective_at, recorded_at, observed_at,
                     expires_at, actor, timestamp, source, session, source_json, content_hash,
                     evidence_refs_json, rule_refs_json, confidence, materiality, materiality_level, close_snapshot_json, policy_rule_json, revision ON observations
WHEN NOT (
    NEW.content_algo != '' AND OLD.content_algo = ''
    AND NEW.what = '' AND NEW.why = '' AND NEW.where_text = '' AND NEW.learned = ''
    AND NEW.id IS OLD.id AND NEW.topic_key IS OLD.topic_key AND NEW.title IS OLD.title
    AND NEW.type IS OLD.type AND NEW.kind IS OLD.kind AND NEW.scope_kind IS OLD.scope_kind
    AND NEW.organization_id IS OLD.organization_id AND NEW.company_id IS OLD.company_id
    AND NEW.ruc IS OLD.ruc AND NEW.period IS OLD.period
    AND NEW.fiscal_effect IS OLD.fiscal_effect AND NEW.effective_at IS OLD.effective_at
    AND NEW.recorded_at IS OLD.recorded_at AND NEW.observed_at IS OLD.observed_at
    AND NEW.expires_at IS OLD.expires_at AND NEW.actor IS OLD.actor
    AND NEW.timestamp IS OLD.timestamp AND NEW.source IS OLD.source AND NEW.session IS OLD.session
    AND NEW.source_json IS OLD.source_json AND NEW.content_hash IS OLD.content_hash
    AND NEW.evidence_refs_json IS OLD.evidence_refs_json AND NEW.rule_refs_json IS OLD.rule_refs_json
    AND NEW.confidence IS OLD.confidence AND NEW.materiality IS OLD.materiality
    AND NEW.materiality_level IS OLD.materiality_level AND NEW.close_snapshot_json IS OLD.close_snapshot_json
    AND NEW.policy_rule_json IS OLD.policy_rule_json AND NEW.revision IS OLD.revision
)
BEGIN
    SELECT RAISE(ABORT, 'IMMUTABLE_OBSERVATION: content, scope and provenance never change after write');
END;
`

// migrateV15ToV16 refines the immutability trigger to permit the exact
// plaintext→encrypted-at-rest transition (legacy re-encryption).
func migrateV15ToV16(db *sql.DB) error {
	ctx := context.Background()
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'observations_immutable_content'`).Scan(&n); err != nil {
		return fmt.Errorf("v16 migration: inspect trigger: %w", err)
	}
	if n != 1 {
		return errors.New("v16 migration: observations_immutable_content trigger missing — corruption signal, abort")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("v16 migration: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `DROP TRIGGER observations_immutable_content`); err != nil {
		return fmt.Errorf("v16 migration: drop trigger: %w", err)
	}
	if _, err := tx.ExecContext(ctx, immutabilityTriggerV16DDL); err != nil {
		return fmt.Errorf("v16 migration: create trigger: %w", err)
	}
	if err := setSchemaVersionTx(ctx, tx, 16); err != nil {
		return fmt.Errorf("v16 migration: schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("v16 migration: commit: %w", err)
	}
	committed = true
	return nil
}
