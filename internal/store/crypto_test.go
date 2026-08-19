// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the at-rest content
// encryption (sdd-060-at-rest-encryption, AC-ENC-1/2/3/4/5): roundtrip
// byte-identity, fail-closed without/wrong key, per-tenant key separation,
// legacy plaintext compatibility and the additive v15 migration. No monetary
// field exists anywhere in this file.
package store

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

var testMasterKey = bytes.Repeat([]byte{0x42}, 32)

// seedEncryptedMemory saves ONE company-scope memory under a store with the
// master key and returns its id + raw ciphertext presence check.
func seedEncryptedMemory(t *testing.T, st *SQLiteStore, scope core.Scope) string {
	t.Helper()
	result, err := st.Save(core.SaveInput{
		TopicKey:     "encryption/fixture",
		Title:        "encryption fixture",
		Kind:         core.KindFact,
		Scope:        scope,
		Content:      core.Content{What: "secret what", Why: "secret why", Where: "secret where", Learned: "secret learned"},
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  "2024-01-15T00:00:00Z",
		Source:       core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	return result.Memory.Identity.ID
}

// TestEncryptionRoundtrip — AC-ENC-1: save with key → read byte-identical; raw
// SQL proves ciphertext on the row and empty plaintext columns.
func TestEncryptionRoundtrip(t *testing.T) {
	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "engram.db"), Options{EncryptionKey: testMasterKey})
	if err != nil {
		t.Fatalf("open encrypted store: %v", err)
	}
	defer func() { _ = st.Close() }()
	scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-a", CompanyID: "co_a", RUC: "20100039201", Period: "202401"}
	id := seedEncryptedMemory(t, st, scope)

	memory, err := st.GetByID(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if memory.Content.What != "secret what" || memory.Content.Learned != "secret learned" {
		t.Fatalf("roundtrip content = %+v, want byte-identical narrative", memory.Content)
	}

	// Raw SQL: ciphertext present, plaintext columns empty, algo marker set.
	var what, why, algo string
	var cipher []byte
	if err := st.db.QueryRow(`SELECT what, why, content_cipher, content_algo FROM observations WHERE id = ?`, id).
		Scan(&what, &why, &cipher, &algo); err != nil {
		t.Fatalf("raw select: %v", err)
	}
	if what != "" || why != "" {
		t.Fatalf("plaintext columns not redacted: what=%q why=%q", what, why)
	}
	if len(cipher) == 0 {
		t.Fatal("content_cipher empty — content not encrypted at rest")
	}
	if algo != contentEncAlgoV1 {
		t.Fatalf("content_algo = %q, want %q", algo, contentEncAlgoV1)
	}
}

// TestEncryptionFailClosed — AC-ENC-2: no key → ENCRYPTION_REQUIRED; wrong key
// → DECRYPTION_FAILED; never partial.
func TestEncryptionFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram.db")
	stEnc, err := OpenWithOptions(path, Options{EncryptionKey: testMasterKey})
	if err != nil {
		t.Fatalf("open encrypted: %v", err)
	}
	scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-a", CompanyID: "co_a", RUC: "20100039201", Period: "202401"}
	id := seedEncryptedMemory(t, stEnc, scope)
	_ = stEnc.Close()

	t.Run("no key", func(t *testing.T) {
		stPlain, err := Open(path)
		if err != nil {
			t.Fatalf("open plain: %v", err)
		}
		defer func() { _ = stPlain.Close() }()
		_, err = stPlain.GetByID(id)
		if err == nil || !strings.Contains(err.Error(), "ENCRYPTION_REQUIRED") {
			t.Fatalf("no-key read err = %v, want ENCRYPTION_REQUIRED", err)
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		wrong := bytes.Repeat([]byte{0x24}, 32)
		stWrong, err := OpenWithOptions(path, Options{EncryptionKey: wrong})
		if err != nil {
			t.Fatalf("open wrong-key: %v", err)
		}
		defer func() { _ = stWrong.Close() }()
		_, err = stWrong.GetByID(id)
		if err == nil || !strings.Contains(err.Error(), "DECRYPTION_FAILED") {
			t.Fatalf("wrong-key read err = %v, want DECRYPTION_FAILED", err)
		}
	})
}

// TestEncryptionTenantSeparation — AC-ENC-3: tenant A's ciphertext fails GCM
// under tenant B's derived key (separable keys).
func TestEncryptionTenantSeparation(t *testing.T) {
	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "engram.db"), Options{EncryptionKey: testMasterKey})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close() }()
	scopeA := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-a", CompanyID: "co_a", RUC: "20100039201", Period: "202401"}
	seedEncryptedMemory(t, st, scopeA)

	// Simulate a read of org-a's row UNDER org-b's derived key: derive b's key
	// and attempt decrypt of a's envelope.
	var cipher, nonce []byte
	var algo, orgID string
	if err := st.db.QueryRow(`SELECT organization_id, content_cipher, content_nonce, content_algo FROM observations LIMIT 1`).
		Scan(&orgID, &cipher, &nonce, &algo); err != nil {
		t.Fatalf("raw select: %v", err)
	}
	if orgID != "org-a" {
		t.Fatalf("fixture org = %q, want org-a", orgID)
	}
	if _, err := decryptContent(testMasterKey, "org-b", nonce, cipher); err == nil {
		t.Fatal("tenant B's derived key decrypted tenant A's content — separation broken")
	}
}

// TestEncryptionLegacyRows — AC-ENC-4: plaintext rows readable in both modes.
func TestEncryptionLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram.db")
	stPlain, err := Open(path)
	if err != nil {
		t.Fatalf("open plain: %v", err)
	}
	scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-a", CompanyID: "co_a", RUC: "20100039201", Period: "202401"}
	id := seedEncryptedMemory(t, stPlain, scope) // legacy plaintext write (no key)
	_ = stPlain.Close()

	// Readable from an encryption-enabled store (legacy rows pass through).
	stEnc, err := OpenWithOptions(path, Options{EncryptionKey: testMasterKey})
	if err != nil {
		t.Fatalf("open encrypted: %v", err)
	}
	defer func() { _ = stEnc.Close() }()
	memory, err := stEnc.GetByID(id)
	if err != nil {
		t.Fatalf("legacy read via encrypted store: %v", err)
	}
	if memory.Content.What != "secret what" {
		t.Fatalf("legacy content = %+v, want plaintext as-is", memory.Content)
	}
}

// TestMigrationV15Additive — AC-ENC-5: a v14 store upgrades additively to v15
// and the schema guard stays green.
func TestMigrationV15Additive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Downgrade the marker to v14 and drop the v15 columns → a genuine v14
	// store. The v16 immutability trigger references the cipher columns, so it
	// must be dropped BEFORE the column drops (SQLite forbids dropping a column
	// a trigger references); the migration chain recreates it on reopen.
	if _, err := st.db.Exec(`DROP TRIGGER observations_immutable_content`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE schema_meta SET value = '14' WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("downgrade marker: %v", err)
	}
	for _, col := range []string{"content_cipher", "content_nonce", "content_algo"} {
		if _, err := st.db.Exec(`ALTER TABLE observations DROP COLUMN ` + col); err != nil {
			t.Fatalf("drop %s: %v", col, err)
		}
	}
	_ = st.Close()

	st2, err := OpenWithOptions(path, Options{EncryptionKey: testMasterKey})
	if err != nil {
		t.Fatalf("reopen after v14→v15: %v", err)
	}
	defer func() { _ = st2.Close() }()
	var version int
	if err := st2.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 16 {
		t.Fatalf("schema version = %d, want 16", version)
	}
	// The store is fully writable post-migration.
	scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-a", CompanyID: "co_a", RUC: "20100039201", Period: "202401"}
	seedEncryptedMemory(t, st2, scope)
}

// GetByID is the small read seam the encryption tests use (returns the memory
// by id through the standard scanMemory path).
func (s *SQLiteStore) GetByID(id string) (core.AccountingMemory, error) {
	row := s.db.QueryRow(`SELECT `+memoryColumns+` FROM observations WHERE id = ?`, id)
	return scanMemory(row, s.encMaster)
}

// seedLegacyReencryptRows seeds a legacy (plaintext) + an already-encrypted + an
// institutional row across two tenants, returning the store. Encryption is
// enabled for the SAVE of the encrypted row only.
func seedLegacyReencryptRows(t *testing.T) (*SQLiteStore, []string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engram.db")
	// Genuine legacy (plaintext) rows require a NO-KEY write: open plain first.
	stPlain, err := Open(path)
	if err != nil {
		t.Fatalf("open plain store: %v", err)
	}
	scopeA := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-a", CompanyID: "co_a", RUC: "20100039201", Period: "202401"}
	scopeB := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-b", CompanyID: "co_b", RUC: "20600995804", Period: "202402"}
	institutional := core.Scope{Kind: core.ScopeKindInstitutional, OrganizationID: "org-a"}
	legacyA1 := saveVia(stPlain, "legacy/a/one", scopeA)
	legacyA2 := saveVia(stPlain, "legacy/a/two", scopeA)
	legacyB := saveVia(stPlain, "legacy/b/one", scopeB)
	_ = stPlain.Close()

	// Reopen WITH the key: one new write is ENCRYPTED (already-encrypted row)
	// and one institutional row lands (never a re-encryption target).
	st2, err := OpenWithOptions(path, Options{EncryptionKey: testMasterKey})
	if err != nil {
		t.Fatalf("reopen with key: %v", err)
	}
	encryptedID := saveVia(st2, "legacy/encrypted", scopeA)
	_ = saveVia(st2, "legacy/institutional", institutional)
	return st2, []string{legacyA1, legacyA2, legacyB, encryptedID}, path
}

// saveVia is the plain Save helper used by the legacy fixtures.
func saveVia(st *SQLiteStore, topic string, scope core.Scope) string {
	result, err := st.Save(core.SaveInput{
		TopicKey: topic, Title: "legacy fixture", Kind: core.KindFact, Scope: scope,
		Content:      core.Content{What: "legacy what " + topic, Why: "why", Where: "where", Learned: "learned"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2024-01-15T00:00:00Z",
		Source: core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		panic(err)
	}
	return result.Memory.Identity.ID
}

// TestReencryptDryRunZeroWrites — AC-RE-1: the report counts only legacy
// company rows, excludes encrypted + institutional, and performs ZERO writes.
func TestReencryptDryRunZeroWrites(t *testing.T) {
	st, ids, _ := seedLegacyReencryptRows(t)
	defer func() { _ = st.Close() }()

	report, err := st.LegacyEncryptionCounts(context.Background())
	if err != nil {
		t.Fatalf("LegacyEncryptionCounts: %v", err)
	}
	if report.TotalLegacy != 3 {
		t.Fatalf("totalLegacy = %d, want 3 (tenant A ×2, tenant B ×1)", report.TotalLegacy)
	}
	if len(report.PerTenant) != 2 {
		t.Fatalf("perTenant = %+v, want 2 tenants", report.PerTenant)
	}
	byOrg := map[string]int{}
	for _, c := range report.PerTenant {
		byOrg[c.OrganizationID] = c.LegacyRows
	}
	if byOrg["org-a"] != 2 || byOrg["org-b"] != 1 {
		t.Fatalf("per-tenant counts = %+v, want org-a 2, org-b 1", byOrg)
	}

	// ZERO writes: the encrypted row still has its ciphertext; legacy rows
	// still plaintext; no new ciphertext anywhere.
	var algo string
	if err := st.db.QueryRow(`SELECT content_algo FROM observations WHERE id = ?`, ids[3]).Scan(&algo); err != nil {
		t.Fatalf("read encrypted row: %v", err)
	}
	if algo == "" {
		t.Fatal("encrypted row lost its marker after dry-run")
	}
	var cipher []byte
	if err := st.db.QueryRow(`SELECT content_cipher FROM observations WHERE id = ?`, ids[0]).Scan(&cipher); err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	if len(cipher) != 0 {
		t.Fatal("dry-run wrote ciphertext — ZERO mutation required")
	}
}

// TestReencryptApplyHashesUnchanged — AC-RE-2/3/4: --apply re-encrypts every
// legacy company row with hashes unchanged; institutional and already-encrypted
// rows untouched; idempotent on rerun.
func TestReencryptApplyHashesUnchanged(t *testing.T) {
	st, ids, _ := seedLegacyReencryptRows(t)
	defer func() { _ = st.Close() }()

	// Capture hashes BEFORE.
	type rowHashes struct{ content, identity, envelope string }
	before := map[string]rowHashes{}
	for _, id := range ids {
		var c, i, e string
		if err := st.db.QueryRow(`SELECT content_hash, identity_hash, envelope_hash FROM observations WHERE id = ?`, id).
			Scan(&c, &i, &e); err != nil {
			t.Fatalf("read hashes %s: %v", id, err)
		}
		before[id] = rowHashes{c, i, e}
	}

	report, err := st.ReencryptLegacyContent(context.Background())
	if err != nil {
		t.Fatalf("ReencryptLegacyContent: %v", err)
	}
	if report.TotalLegacy != 3 {
		t.Fatalf("re-encrypted = %d, want 3", report.TotalLegacy)
	}

	// Legacy rows now carry ciphertext + algo; plaintext columns redacted.
	for _, id := range ids[:3] {
		var what, algo string
		var cipher []byte
		if err := st.db.QueryRow(`SELECT what, content_cipher, content_algo FROM observations WHERE id = ?`, id).
			Scan(&what, &cipher, &algo); err != nil {
			t.Fatalf("read re-encrypted %s: %v", id, err)
		}
		if len(cipher) == 0 || algo != contentEncAlgoV1 {
			t.Fatalf("row %s not re-encrypted (algo=%q cipher=%d)", id, algo, len(cipher))
		}
		if what != "" {
			t.Fatalf("row %s plaintext column not redacted: %q", id, what)
		}
	}
	// Institutional row: still plaintext (never touched).
	var instWhat, instAlgo string
	if err := st.db.QueryRow(`SELECT what, content_algo FROM observations WHERE topic_key = 'legacy/institutional'`).
		Scan(&instWhat, &instAlgo); err != nil {
		t.Fatalf("read institutional: %v", err)
	}
	if instAlgo != "" || !strings.Contains(instWhat, "legacy what") {
		t.Fatalf("institutional row touched: algo=%q what=%q", instAlgo, instWhat)
	}
	// Already-encrypted row: untouched (its algo is still the marker).
	var encAlgo string
	if err := st.db.QueryRow(`SELECT content_algo FROM observations WHERE id = ?`, ids[3]).Scan(&encAlgo); err != nil {
		t.Fatalf("read encrypted row: %v", err)
	}
	if encAlgo != contentEncAlgoV1 {
		t.Fatalf("encrypted row algo = %q after apply", encAlgo)
	}

	// Hashes UNCHANGED for every row (AC-RE-3).
	for _, id := range ids {
		var c, i, e string
		if err := st.db.QueryRow(`SELECT content_hash, identity_hash, envelope_hash FROM observations WHERE id = ?`, id).
			Scan(&c, &i, &e); err != nil {
			t.Fatalf("read hashes after %s: %v", id, err)
		}
		want := before[id]
		if c != want.content || i != want.identity || e != want.envelope {
			t.Fatalf("hashes changed for %s: got (%s,%s,%s) want (%s,%s,%s)",
				id, c, i, e, want.content, want.identity, want.envelope)
		}
	}

	// Idempotent: a second apply re-encrypts nothing.
	again, err := st.ReencryptLegacyContent(context.Background())
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if again.TotalLegacy != 0 {
		t.Fatalf("second apply re-encrypted %d rows — not idempotent", again.TotalLegacy)
	}
}

// TestReencryptFailClosed — AC-RE-5/6: no key → re-encryption fails closed;
// the read-only report is safe without a key.
func TestReencryptFailClosed(t *testing.T) {
	st, _, path := seedLegacyReencryptRows(t)
	_ = st.Close()

	plain, err := Open(path)
	if err != nil {
		t.Fatalf("open plain: %v", err)
	}
	defer func() { _ = plain.Close() }()
	if _, err := plain.ReencryptLegacyContent(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "ENCRYPTION_REQUIRED") {
		t.Fatalf("no-key re-encrypt err = %v, want ENCRYPTION_REQUIRED", err)
	}
	// The read-only report still works (safe) — the operator can see the gap.
	report, err := plain.LegacyEncryptionCounts(context.Background())
	if err != nil {
		t.Fatalf("no-key counts: %v", err)
	}
	if report.TotalLegacy != 3 {
		t.Fatalf("no-key totalLegacy = %d, want 3", report.TotalLegacy)
	}
}
