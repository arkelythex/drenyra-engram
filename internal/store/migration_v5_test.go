// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module verifies the v4→v5 additive
// migration (v0.4.0 Step 3 keys commit — Ed25519 action receipts): fresh stores
// bootstrap to schema_version=5 with the signing_keys + receipts tables and
// their indexes/triggers, existing v4 data survives untouched, a failing
// migration rolls back leaving schema_version=4, receipt rows are immutable,
// signing_keys only allow the one-way null→timestamp revocation update, and
// the receipt persistence surfaces (RegisterPublicKey / RevokePublicKey /
// LookupSigningKey / LatestReceiptChainHead / InsertReceipt) enforce the
// design's chain, uniqueness and exactly-one-typed-FK invariants
// (docs/architecture/ed25519-receipts-step3.md "SQLite schema v5").

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// openV4Schema opens a raw SQLite handle and bootstraps the EXACT v4 layout
// (applySchema + the Open pragmas + the v2→v3 and v3→v4 migrations), so tests
// can exercise the v4→v5 migration on a genuine v4 store.
func openV4Schema(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw v4 database: %v", err)
	}
	for _, pragma := range []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			t.Fatalf("%s: %v", pragma, err)
		}
	}
	if err := applySchema(db); err != nil {
		_ = db.Close()
		t.Fatalf("apply v2 schema: %v", err)
	}
	if err := migrateV2ToV3(db); err != nil {
		_ = db.Close()
		t.Fatalf("bootstrap v3 layout: %v", err)
	}
	if err := migrateV3ToV4(db); err != nil {
		_ = db.Close()
		t.Fatalf("bootstrap v4 layout: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func v5Tables() []string {
	return []string{"signing_keys", "receipts"}
}

func v5Indexes() []string {
	return []string{"uq_receipts_singleton", "idx_receipts_subject_time", "idx_receipts_key_time"}
}

func v5Triggers() []string {
	return []string{
		"signing_keys_no_delete", "signing_keys_revoke_only",
		"receipts_no_update", "receipts_no_delete",
	}
}

// TestFreshStoreBootstrapsV5ReceiptTables verifies the full additive chain: a
// fresh store boots to schema_version=5 with both v5 tables, the three indexes
// and the four triggers, while every v3/v4 object survives untouched.
func TestFreshStoreBootstrapsV5ReceiptTables(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 15 {
		t.Fatalf("schema_version = %d, want 15 (the chain continues v5→v6→v7→v8→v9→v10→v11→v12→v13→v14)", version)
	}

	// The v3 + v4 layers survive the chain (additive migrations never drop).
	for _, table := range append(append(v3Tables(), v4Tables()...), v5Tables()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("fresh store missing table %q: %v", table, err)
		}
	}
	for _, idx := range append(v4Indexes(), v5Indexes()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name); err != nil {
			t.Fatalf("fresh store missing index %q: %v", idx, err)
		}
	}
	for _, trigger := range append(append(v3Triggers(), v4Triggers()...), v5Triggers()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&name); err != nil {
			t.Fatalf("fresh store missing trigger %q: %v", trigger, err)
		}
	}

	// uq_receipts_singleton must be a PARTIAL unique index over every action
	// except the append-only evidence-link/hold/lifecycle acts — the v11 rebuild
	// excludes the seven v0.8 evidence-lifecycle acts on top of the legacy
	// evidence-link and hold exclusions (dual approval emits TWO purge_approved
	// receipts per object; retractions restart the pipeline).
	var indexSQL string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'uq_receipts_singleton'`).Scan(&indexSQL); err != nil {
		t.Fatalf("read uq_receipts_singleton definition: %v", err)
	}
	if !strings.Contains(indexSQL, "UNIQUE") || !strings.Contains(indexSQL, "'evidence_linked','hold_placed','hold_lifted',") || !strings.Contains(indexSQL, "'purge_requested','purge_approved','purge_rejected'") {
		t.Fatalf("uq_receipts_singleton is not the append-only-action partial unique index: %s", indexSQL)
	}
}

// TestV4StoreMigratesToV5AdditivelyPreservingRows verifies that a genuine v4
// store migrates to v5 with every row byte-preserved and the whole v3/v4
// surface intact (approval + judgment triggers still enforce).
func TestV4StoreMigratesToV5AdditivelyPreservingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v4.db")
	db := openV4Schema(t, path)

	active := saveV3Row(t, db, validInput("tax.igv.rate", "first version"))
	pendingInput := validInput("tax.igv.gated", "needs approval")
	pendingInput.FiscalEffect = core.FiscalEffectClosing
	pendingInput.EffectiveAt = testT
	pending := saveV3Row(t, db, pendingInput)
	_ = db.Close() // release the file before Open migrates it

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open migrates v4→v5: %v", err)
	}
	defer func() { _ = s.Close() }()

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version after migration: %v", err)
	}
	if version != 15 {
		t.Fatalf("schema_version after migration = %d, want 15 (the chain continues v5→v6→v7→v8→v9→v10→v11→v12→v13→v14)", version)
	}

	// Rows survive with EXACTLY the envelope bytes written at v4.
	gotActive, ok := s.FindByID(active.Identity.ID)
	if !ok {
		t.Fatal("active row lost by migration")
	}
	if gotActive.EnvelopeHash != active.EnvelopeHash || gotActive.Status != core.StatusActive {
		t.Fatalf("active row changed by migration: (%s, %s)", gotActive.EnvelopeHash, gotActive.Status)
	}
	gotPending, ok := s.FindByID(pending.Identity.ID)
	if !ok {
		t.Fatal("pending_review row lost by migration")
	}
	if gotPending.EnvelopeHash != pending.EnvelopeHash || gotPending.Status != core.StatusPendingReview {
		t.Fatalf("pending row changed by migration: (%s, %s)", gotPending.EnvelopeHash, gotPending.Status)
	}

	// The whole v3/v4 surface survives the chain.
	for _, trigger := range append(v3Triggers(), v4Triggers()...) {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&name); err != nil {
			t.Fatalf("migrated store missing trigger %q: %v", trigger, err)
		}
	}
	for _, table := range v5Tables() {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("migrated store missing v5 table %q: %v", table, err)
		}
	}
}

// TestV4ToV5MigrationRollsBackLeavingSchemaV4 verifies the single-transaction
// rollback: a conflicting pre-existing table fails the migration and the store
// stays v4 with NO v5 object surviving.
func TestV4ToV5MigrationRollsBackLeavingSchemaV4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v4-fail.db")
	db := openV4Schema(t, path)
	// A conflicting pre-existing table makes the verbatim CREATE TABLE receipts
	// fail — the corrupt-step probe for the single-transaction rollback.
	if _, err := db.Exec(`CREATE TABLE receipts (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("pre-create conflicting table: %v", err)
	}
	_ = db.Close()

	_, err := Open(path)
	if err == nil {
		t.Fatal("Open must fail closed when the migration fails")
	}
	if !strings.Contains(err.Error(), "migrate v4→v5") {
		t.Fatalf("error %q does not identify the migration step", err)
	}

	// Re-open raw: schema_version must still be 4 and NO v5 object may survive.
	// The pre-created conflicting `receipts` table IS the corruption probe, so its
	// presence proves the rollback (it must still carry ONLY the probe shape).
	db = openRawReadHandle(t, path)
	version, err := readSchemaVersion(db)
	if err != nil {
		t.Fatalf("read schema version after failed migration: %v", err)
	}
	if version != 4 {
		t.Fatalf("schema_version = %d after failed migration, want 4 (rolled back)", version)
	}
	for _, object := range append([]string{"signing_keys"}, append(v5Indexes(), v5Triggers()...)...) {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE name = ?`, object).Scan(&name); err == nil {
			t.Fatalf("object %q survived the migration rollback", object)
		}
	}
	var colCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('receipts')`).Scan(&colCount); err != nil {
		t.Fatalf("read probe receipts table: %v", err)
	}
	if colCount != 1 {
		t.Fatalf("receipts must still be the probe table (only id), got %d columns", colCount)
	}
}

// TestUnknownSchemaVersionFailsClosed verifies the provenance.md frozen policy:
// an unreadable/unsupported layout never opens.
func TestUnknownSchemaVersionFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open fresh store: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE schema_meta SET value = '99' WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("corrupt schema_version: %v", err)
	}
	_ = s.Close()

	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "unsupported store layout") {
		t.Fatalf("Open must fail closed on schema_version=99, got %v", err)
	}
}

// ──────────────────────────────────────────────
// Receipt persistence surfaces
// ──────────────────────────────────────────────

// registerAndReceipt seeds a signing key and returns a valid memory-subject
// receipt row referencing a REAL observation (the memory_id FK is enforced).
func registerAndReceipt(t *testing.T, s *SQLiteStore, keyID, payloadHash, prevHash string) ReceiptRow {
	t.Helper()
	memory, err := s.Save(validInput("tax.igv.receipt", "receipt subject"))
	if err != nil {
		t.Fatalf("save receipt subject memory: %v", err)
	}
	memoryID := memory.Memory.Identity.ID
	row := ReceiptRow{
		SubjectType:         core.SubjectTypeMemory,
		SubjectID:           memoryID,
		Action:              core.ReceiptActionMemoryRecorded,
		TenantID:            testOrgID,
		CompanyID:           "acme",
		FiscalPeriodID:      "202601",
		PayloadHash:         payloadHash,
		PreviousReceiptHash: prevHash,
		PrincipalID:         "cli",
		MembershipID:        "membership-1",
		PolicyVersion:       "kernel/v0.4.0",
		Algorithm:           core.ReceiptAlgorithm,
		KeyID:               keyID,
		Signature:           make([]byte, 64),
		IssuedAt:            testT,
		PayloadJSON:         `{"version":"receipt-payload/v0.4.0"}`,
		ReceiptHash:         "receipt-hash-" + payloadHash,
		MemoryID:            memoryID,
	}
	if err := s.RegisterPublicKey(ctxForTest(), s.db, keyID, core.ReceiptAlgorithm, "base64-public", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}
	if err := s.InsertReceipt(ctxForTest(), s.db, row); err != nil {
		t.Fatalf("insert receipt: %v", err)
	}
	return row
}

func ctxForTest() (ctx context.Context) { return context.Background() }

// TestReceiptPersistenceSurfaces exercises RegisterPublicKey (INSERT OR IGNORE),
// LookupSigningKey, RevokePublicKey, InsertReceipt and the chain-head contract
// (genesis empty; the next receipt chains on the latest derived hash).
func TestReceiptPersistenceSurfaces(t *testing.T) {
	s := newTestStore(t)

	// RegisterPublicKey is INSERT OR IGNORE: a repeated registration is a no-op.
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-1", core.ReceiptAlgorithm, "base64-public-1", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("register key: %v", err)
	}
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-1", core.ReceiptAlgorithm, "base64-public-1", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("re-register key must be ignored: %v", err)
	}
	rec, err := s.LookupSigningKey(ctxForTest(), s.db, "ed25519:key-1")
	if err != nil {
		t.Fatalf("lookup key: %v", err)
	}
	if !rec.Found || rec.PublicKey != "base64-public-1" || rec.RevokedAt != "" || rec.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("lookup row = %+v, want registered unrevoked key", rec)
	}

	// An unknown key is Found=false (the signer treats it as "register lazily").
	missing, err := s.LookupSigningKey(ctxForTest(), s.db, "ed25519:unknown")
	if err != nil {
		t.Fatalf("lookup unknown key: %v", err)
	}
	if missing.Found {
		t.Fatalf("unknown key must report Found=false, got %+v", missing)
	}

	// RevokePublicKey performs the one legal update; the revoked state is visible.
	if err := s.RevokePublicKey(ctxForTest(), s.db, "ed25519:key-1", "2026-02-01T00:00:00Z"); err != nil {
		t.Fatalf("revoke key: %v", err)
	}
	revoked, err := s.LookupSigningKey(ctxForTest(), s.db, "ed25519:key-1")
	if err != nil {
		t.Fatalf("lookup revoked key: %v", err)
	}
	if revoked.RevokedAt != "2026-02-01T00:00:00Z" {
		t.Fatalf("revoked_at = %q, want the revocation timestamp", revoked.RevokedAt)
	}

	// A second revocation targets no unrevoked row → fail closed.
	if err := s.RevokePublicKey(ctxForTest(), s.db, "ed25519:key-1", "2026-03-01T00:00:00Z"); err == nil || !strings.Contains(err.Error(), "no unrevoked row") {
		t.Fatalf("re-revoke must fail closed, got %v", err)
	}

	// Genesis chain head: no prior receipt → empty previousReceiptHash.
	first := registerAndReceipt(t, s, "ed25519:key-1", "payload-hash-1", "")
	head, err := s.LatestReceiptChainHead(ctxForTest(), s.db, core.SubjectTypeMemory, first.SubjectID)
	if err != nil {
		t.Fatalf("chain head: %v", err)
	}
	if head != first.ReceiptHash {
		t.Fatalf("chain head = %q, want %q", head, first.ReceiptHash)
	}

	// The next receipt chains on the LATEST derived hash.
	second := registerAndReceipt(t, s, "ed25519:key-1", "payload-hash-2", first.ReceiptHash)
	head2, err := s.LatestReceiptChainHead(ctxForTest(), s.db, core.SubjectTypeMemory, second.SubjectID)
	if err != nil {
		t.Fatalf("chain head 2: %v", err)
	}
	if head2 != second.ReceiptHash {
		t.Fatalf("chain head 2 = %q, want %q", head2, second.ReceiptHash)
	}
}

// TestReceiptRowsImmutable verifies the no-update / no-delete triggers: a
// corrupt or buggy caller cannot mutate receipt history.
func TestReceiptRowsImmutable(t *testing.T) {
	s := newTestStore(t)
	row := registerAndReceipt(t, s, "ed25519:key-imm", "payload-hash-imm", "")

	if _, err := s.db.Exec(`UPDATE receipts SET payload_json = '{"mutated":true}' WHERE receipt_hash = ?`, row.ReceiptHash); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_RECEIPT") {
		t.Fatalf("UPDATE on receipts must abort with IMMUTABLE_RECEIPT, got %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM receipts WHERE receipt_hash = ?`, row.ReceiptHash); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_RECEIPT") {
		t.Fatalf("DELETE on receipts must abort with IMMUTABLE_RECEIPT, got %v", err)
	}
}

// TestSigningKeysRevocationOnly verifies the signing_keys trigger surface:
// deletion aborts; the ONLY legal update is the one-way null→timestamp
// revocation; every other mutation aborts and the row stays byte-equal.
func TestSigningKeysRevocationOnly(t *testing.T) {
	s := newTestStore(t)
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-trig", core.ReceiptAlgorithm, "base64-public", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}

	if _, err := s.db.Exec(`DELETE FROM signing_keys WHERE key_id = 'ed25519:key-trig'`); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_SIGNING_KEY") {
		t.Fatalf("DELETE on signing_keys must abort with IMMUTABLE_SIGNING_KEY, got %v", err)
	}

	for _, tc := range []struct {
		name string
		stmt string
	}{
		{"change public key", `UPDATE signing_keys SET public_key = 'other' WHERE key_id = 'ed25519:key-trig'`},
		{"change algorithm", `UPDATE signing_keys SET algorithm = 'RSA' WHERE key_id = 'ed25519:key-trig'`},
		{"change created_at", `UPDATE signing_keys SET created_at = '2000-01-01T00:00:00Z' WHERE key_id = 'ed25519:key-trig'`},
		{"revoke with NULL", `UPDATE signing_keys SET revoked_at = NULL WHERE key_id = 'ed25519:key-trig'`},
		{"un-revoke after revoke", `UPDATE signing_keys SET revoked_at = NULL WHERE key_id = 'ed25519:key-trig'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.db.Exec(tc.stmt); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_SIGNING_KEY") {
				t.Fatalf("UPDATE %q must abort with IMMUTABLE_SIGNING_KEY, got %v", tc.name, err)
			}
		})
	}

	// The ONE legal shape: null → timestamp revocation.
	if _, err := s.db.Exec(`UPDATE signing_keys SET revoked_at = ? WHERE key_id = 'ed25519:key-trig'`, nowISO()); err != nil {
		t.Fatalf("one-way revocation must be allowed, got %v", err)
	}
	// Re-revocation is blocked (already revoked).
	if _, err := s.db.Exec(`UPDATE signing_keys SET revoked_at = ? WHERE key_id = 'ed25519:key-trig'`, nowISO()); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_SIGNING_KEY") {
		t.Fatalf("re-revocation must abort, got %v", err)
	}
}

// TestReceiptSchemaChecksAndUniqueness verifies the CHECKs, FKs and uniqueness
// constraints of the receipts table: exactly-one-typed-FK equals subject_id,
// the key FK resolves, duplicate emissions abort, and only evidence_linked may
// repeat per subject+action.
func TestReceiptSchemaChecksAndUniqueness(t *testing.T) {
	s := newTestStore(t)
	memory, err := s.Save(validInput("tax.igv.unique", "unique receipt subject"))
	if err != nil {
		t.Fatalf("save memory: %v", err)
	}
	memoryID := memory.Memory.Identity.ID
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-uq", core.ReceiptAlgorithm, "base64-public", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}

	base := ReceiptRow{
		SubjectType:         core.SubjectTypeMemory,
		SubjectID:           memoryID,
		Action:              core.ReceiptActionMemoryRecorded,
		TenantID:            testOrgID,
		CompanyID:           "acme",
		FiscalPeriodID:      "202601",
		PayloadHash:         "payload-hash-uq",
		PreviousReceiptHash: "",
		PrincipalID:         "cli",
		MembershipID:        "membership-1",
		PolicyVersion:       "kernel/v0.4.0",
		Algorithm:           core.ReceiptAlgorithm,
		KeyID:               "ed25519:key-uq",
		Signature:           make([]byte, 64),
		IssuedAt:            testT,
		PayloadJSON:         `{"version":"receipt-payload/v0.4.0"}`,
		ReceiptHash:         "receipt-hash-uq",
		MemoryID:            memoryID,
	}
	if err := s.InsertReceipt(ctxForTest(), s.db, base); err != nil {
		t.Fatalf("insert base receipt: %v", err)
	}

	// Duplicate (subject, action, payload_hash) → UNIQUE abort (idempotent retry).
	dup := base
	dup.ReceiptHash = "receipt-hash-uq-dup"
	if err := s.InsertReceipt(ctxForTest(), s.db, dup); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("duplicate emission must abort with UNIQUE, got %v", err)
	}

	// Same subject + action with a DIFFERENT payload is blocked by the partial
	// singleton index (memory_recorded is not evidence_linked).
	second := base
	second.PayloadHash = "payload-hash-uq-2"
	second.ReceiptHash = "receipt-hash-uq-2"
	if err := s.InsertReceipt(ctxForTest(), s.db, second); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("second singleton receipt must abort with UNIQUE, got %v", err)
	}

	// evidence_linked is exempt: a genuinely new link mints another receipt.
	link := base
	link.Action = core.ReceiptActionEvidenceLinked
	link.PayloadHash = "payload-hash-link-1"
	link.ReceiptHash = "receipt-hash-link-1"
	if err := s.InsertReceipt(ctxForTest(), s.db, link); err != nil {
		t.Fatalf("first evidence_linked receipt: %v", err)
	}
	link2 := link
	link2.PayloadHash = "payload-hash-link-2"
	link2.ReceiptHash = "receipt-hash-link-2"
	if err := s.InsertReceipt(ctxForTest(), s.db, link2); err != nil {
		t.Fatalf("second evidence_linked receipt must be allowed, got %v", err)
	}

	// Exactly-one-typed-FK: a memory subject must NOT carry a judgment FK.
	bothFK := base
	bothFK.ReceiptHash = "receipt-hash-both"
	bothFK.JudgmentID = memoryID
	if err := s.InsertReceipt(ctxForTest(), s.db, bothFK); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("both typed FKs must fail the CHECK, got %v", err)
	}
	// A typed FK that differs from subject_id fails the equality CHECK.
	mismatch := base
	mismatch.ReceiptHash = "receipt-hash-mismatch"
	mismatch.MemoryID = "some-other-memory"
	if err := s.InsertReceipt(ctxForTest(), s.db, mismatch); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("typed FK differing from subject_id must fail the CHECK, got %v", err)
	}
	// A missing typed FK (both NULL) fails the exactly-one CHECK.
	noFK := base
	noFK.ReceiptHash = "receipt-hash-nofk"
	noFK.MemoryID = ""
	if err := s.InsertReceipt(ctxForTest(), s.db, noFK); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("missing typed FK must fail the CHECK, got %v", err)
	}

	// The key FK resolves to signing_keys: an unregistered key aborts. A FRESH
	// subject keeps the singleton index from masking the FK error.
	fresh, err := s.Save(validInput("tax.igv.badkey", "bad-key subject"))
	if err != nil {
		t.Fatalf("save bad-key subject: %v", err)
	}
	badKey := base
	badKey.ReceiptHash = "receipt-hash-badkey"
	badKey.SubjectID = fresh.Memory.Identity.ID
	badKey.MemoryID = fresh.Memory.Identity.ID
	badKey.KeyID = "ed25519:never-registered"
	if err := s.InsertReceipt(ctxForTest(), s.db, badKey); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("unregistered key must fail the FK, got %v", err)
	}

	// Judgment subjects reference judgments: a judgment receipt needs a real
	// judgment row (FK enforced) and the judgment-side typed FK.
	fromID, toID := seedJudgmentContext(t, s)
	insertJudgment(t, s.db, proposedJudgment("j-receipt", fromID, toID))
	jRow := base
	jRow.SubjectType = core.SubjectTypeJudgment
	jRow.SubjectID = "j-receipt"
	jRow.Action = core.ReceiptActionRelationConfirmed
	jRow.ReceiptHash = "receipt-hash-judgment"
	jRow.MemoryID = ""
	jRow.JudgmentID = "j-receipt"
	if err := s.InsertReceipt(ctxForTest(), s.db, jRow); err != nil {
		t.Fatalf("judgment receipt must be insertable, got %v", err)
	}
}
