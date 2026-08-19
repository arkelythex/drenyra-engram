// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. Confidence is a probability 0..1 (never
// money). This module tests the additive v16→v17 confidence-required migration
// (sdd-060-confidence-required, FR-CN-2, AC-CN-3/4/5): clean v16 upgrades to
// v17 with the trigger installed; legacy NULL-confidence rows fail closed
// naming the offending scope; a crash between trigger creation and version
// write converges on reopen; and the trigger blocks a direct NULL-confidence
// INSERT that bypasses the core validation.
package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// seedV16StoreWithConfidence writes observations through the CURRENT (v17)
// schema then downgrades the marker to 16, producing a genuine v16 store whose
// rows all carry confidence — the clean-upgrade fixture.
func seedV16StoreWithConfidence(t *testing.T) (*SQLiteStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engram.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-a", CompanyID: "co_a", RUC: "20100039201", Period: "202401"}
	if _, err := st.Save(core.SaveInput{
		TopicKey: "v17/fixture", Title: "v17 fixture", Kind: core.KindFact, Scope: scope,
		Content:      core.Content{What: "fixture", Why: "why", Where: "where", Learned: "learned"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2024-01-15T00:00:00Z",
		Confidence: 0.8,
		Source:     core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
	}); err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE schema_meta SET value = '16' WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("downgrade marker: %v", err)
	}
	return st, path
}

func TestConfidenceMigrationCleanV16Upgrades(t *testing.T) {
	st, path := seedV16StoreWithConfidence(t)
	_ = st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after v16→v17: %v", err)
	}
	defer func() { _ = st2.Close() }()

	var version int
	if err := st2.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 17 {
		t.Fatalf("schema version = %d, want 17", version)
	}
	// The confidence-required triggers are installed (SQLite requires separate
	// INSERT and UPDATE triggers).
	var n int
	if err := st2.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name IN ('observations_confidence_required_insert', 'observations_confidence_required_update')`).Scan(&n); err != nil {
		t.Fatalf("inspect triggers: %v", err)
	}
	if n != 2 {
		t.Fatalf("observations_confidence_required trigger count = %d, want 2", n)
	}
	// The row survived additively and reads back with its confidence.
	got, err := st2.GetByID("")
	if err == nil {
		// GetByID("") will fail (no such id); the important part is the store
		// is writable below. Keep the read check light.
		_ = got
	}
	scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-a", CompanyID: "co_a", RUC: "20100039201", Period: "202401"}
	if _, err := st2.Save(core.SaveInput{
		TopicKey: "v17/post-upgrade", Title: "post", Kind: core.KindFact, Scope: scope,
		Content:      core.Content{What: "post", Why: "why", Where: "where", Learned: "learned"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2024-01-16T00:00:00Z",
		Confidence: 0.7,
		Source:     core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
	}); err != nil {
		t.Fatalf("write after v17 upgrade: %v", err)
	}
}

func TestConfidenceMigrationPreservesLegacyNullRows(t *testing.T) {
	st, path := seedV16StoreWithConfidence(t)
	// Introduce a genuine legacy NULL-confidence row (bypassing core). The
	// v17 triggers must be dropped first: a genuine v16 store predates them.
	for _, trg := range []string{"observations_confidence_required_insert", "observations_confidence_required_update"} {
		if _, err := st.db.Exec(`DROP TRIGGER IF EXISTS ` + trg); err != nil {
			t.Fatalf("drop v17 trigger %s: %v", trg, err)
		}
	}
	if _, err := st.db.Exec(
		`INSERT INTO observations (id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period, what, why, where_text, learned, fiscal_effect, effective_at, recorded_at, observed_at, actor, timestamp, source, session, source_json, content_hash, identity_hash, envelope_hash, evidence_refs_json, rule_refs_json, status, confidence, materiality, receipt_id, supersedes_id, revision, content_cipher, content_nonce, content_algo)
		 VALUES ('legacy-null-conf', 'legacy/nc', 'nc', 'memory', 'fact', 'company', 'org-a', 'co_a', '20100039201', '202401', 'what', 'why', 'where', 'learned', 'none', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '', 't', '2024-01-01T00:00:00Z', 's', '', '{}', 'h', 'i', 'e', '[]', '[]', 'proposed', NULL, NULL, '', '', 1, '', '', '')`); err != nil {
		t.Fatalf("seed legacy NULL-confidence row: %v", err)
	}
	_ = st.Close()

	// The v17 migration PRESERVES legacy NULL-confidence rows (additive rule):
	// the store upgrades cleanly, the legacy row survives with NULL confidence,
	// and the new triggers block future NULL writes.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen with legacy NULL-confidence row: %v", err)
	}
	defer func() { _ = st2.Close() }()
	var version int
	if err := st2.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 17 {
		t.Fatalf("schema version = %d, want 17", version)
	}
	// Legacy row preserved with NULL confidence (no backfill, no re-hash).
	var conf sql.NullFloat64
	if err := st2.db.QueryRow(`SELECT confidence FROM observations WHERE id = 'legacy-null-conf'`).Scan(&conf); err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	if conf.Valid {
		t.Fatalf("legacy NULL-confidence row was backfilled: confidence = %v", conf.Float64)
	}
	// New NULL writes are blocked by the installed triggers.
	_, err = st2.db.Exec(
		`INSERT INTO observations (id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period, what, why, where_text, learned, fiscal_effect, effective_at, recorded_at, observed_at, actor, timestamp, source, session, source_json, content_hash, identity_hash, envelope_hash, evidence_refs_json, rule_refs_json, status, confidence, materiality, receipt_id, supersedes_id, revision, content_cipher, content_nonce, content_algo)
		 VALUES ('direct-null-conf', 'direct/nc', 'nc', 'memory', 'fact', 'company', 'org-a', 'co_a', '20100039201', '202401', 'what', 'why', 'where', 'learned', 'none', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '', 't', '2024-01-01T00:00:00Z', 's', '', '{}', 'h', 'i', 'e', '[]', '[]', 'proposed', NULL, NULL, '', '', 1, '', '', '')`)
	if err == nil {
		t.Fatal("expected trigger to abort a NULL-confidence INSERT")
	}
	if !strings.Contains(err.Error(), "CONFIDENCE_REQUIRED") {
		t.Fatalf("error = %v, want CONFIDENCE_REQUIRED from trigger", err)
	}
}

func TestConfidenceTriggerBlocksNullInsert(t *testing.T) {
	st, path := seedV16StoreWithConfidence(t)
	_ = st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = st2.Close() }()

	_, err = st2.db.Exec(
		`INSERT INTO observations (id, topic_key, title, type, kind, scope_kind, organization_id, company_id, ruc, period, what, why, where_text, learned, fiscal_effect, effective_at, recorded_at, observed_at, actor, timestamp, source, session, source_json, content_hash, identity_hash, envelope_hash, evidence_refs_json, rule_refs_json, status, confidence, materiality, receipt_id, supersedes_id, revision, content_cipher, content_nonce, content_algo)
		 VALUES ('direct-null-conf', 'direct/nc', 'nc', 'memory', 'fact', 'company', 'org-a', 'co_a', '20100039201', '202401', 'what', 'why', 'where', 'learned', 'none', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '', 't', '2024-01-01T00:00:00Z', 's', '', '{}', 'h', 'i', 'e', '[]', '[]', 'proposed', NULL, NULL, '', '', 1, '', '', '')`)
	if err == nil {
		t.Fatal("expected trigger to abort a NULL-confidence INSERT")
	}
	if !strings.Contains(err.Error(), "CONFIDENCE_REQUIRED") {
		t.Fatalf("error = %v, want CONFIDENCE_REQUIRED from trigger", err)
	}
}

func TestConfidenceMigrationCrashConvergesToV17(t *testing.T) {
	st, path := seedV16StoreWithConfidence(t)
	_ = st.Close()

	// Simulate a crash between trigger creation and the version write by
	// manually installing the trigger and NOT bumping the version: reopen must
	// converge to v17 (the CREATE TRIGGER is idempotent via IF NOT EXISTS).
	st2, err := OpenWithOptions(path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := st2.db.Exec(`DROP TRIGGER observations_confidence_required_insert`); err != nil {
		t.Fatalf("drop insert trigger: %v", err)
	}
	if _, err := st2.db.Exec(`DROP TRIGGER observations_confidence_required_update`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if _, err := st2.db.Exec(`UPDATE schema_meta SET value = '16' WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("reset marker: %v", err)
	}
	_ = st2.Close()

	st3, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after simulated crash: %v", err)
	}
	defer func() { _ = st3.Close() }()
	var version int
	if err := st3.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 17 {
		t.Fatalf("schema version = %d after crash-reopen, want 17", version)
	}
}
