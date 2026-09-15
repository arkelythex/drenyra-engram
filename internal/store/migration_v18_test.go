package store

import (
	"context"
	"database/sql"
	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"path/filepath"
	"strings"
	"testing"
)

func fiscalTestBinding(actor string) core.FiscalScopeBinding {
	return core.FiscalScopeBinding{Version: "v1", Tenant: "tenant-a", Organization: "company-a", Company: "20100070970", FiscalPeriod: "202401", LedgerBook: "purchases", OperationType: "memory.save", SourceSnapshot: strings.Repeat("a", 64), PolicyVersion: "fiscal-v1", Actor: actor, AuthorityLevel: core.AuthorityLevelPrepare}
}
func openV17FiscalFixture(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	db := openV13Schema(t, path)
	for _, migrate := range []func(*sql.DB) error{migrateV13ToV14, migrateV14ToV15, migrateV15ToV16, migrateV16ToV17} {
		if err := migrate(db); err != nil {
			t.Fatal(err)
		}
	}
	// This raw struct literal bypasses openInternal (deliberately — it drives
	// the migration chain directly rather than through Open), so
	// fiscalRuntimeMode is never resolved from DRENYRA_FISCAL_RUNTIME_MODE
	// and stays at its zero value (shadow-equivalent, fail-closed — see the
	// field's doc comment in store.go). saveFiscalFixture's only caller of
	// this fixture (TestMigrationV18IsAdditiveAndFailsClosed) never passes a
	// FiscalIntent, so set the mode directly on the struct field to the
	// legacy-permitting mode that call needs.
	return &SQLiteStore{db: db, objectsRoot: defaultObjectsRoot(path), fiscalRuntimeMode: FiscalRuntimeLegacyCompat}
}
func removeV18FixtureArtifacts(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"fiscal_binding_links", "fiscal_scope_bindings"} {
		if _, err := db.Exec("DROP " + "TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
}
func saveFiscalFixture(t *testing.T, s *SQLiteStore, topic, ruc string) string {
	t.Helper()
	result, err := s.Save(core.SaveInput{TopicKey: topic, Title: topic, Kind: core.KindFact, Scope: core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "tenant-a", CompanyID: "company-a", RUC: ruc, Period: "202401"}, Content: core.Content{What: "fixture", Why: "migration", Where: "test", Learned: "none"}, FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2024-01-01T00:00:00Z", Source: core.Source{System: "go-test", ActorID: "agent", ActorKind: core.ActorKindAgent}})
	if err != nil {
		t.Fatal(err)
	}
	return result.Memory.Identity.ID
}
func TestMigrationV18IsAdditiveAndFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v17.db")
	s := openV17FiscalFixture(t, path)
	id := saveFiscalFixture(t, s, "legacy/invalid", "20123456789")
	var before string
	if err := s.db.QueryRow(`SELECT ruc||envelope_hash FROM observations WHERE id=?`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var after string
	if err := s.db.QueryRow(`SELECT ruc||envelope_hash FROM observations WHERE id=?`, id).Scan(&after); err != nil || after != before {
		t.Fatalf("legacy row changed: %v", err)
	}
	if v, _ := readSchemaVersion(s.db); v != 18 {
		t.Fatalf("version=%d", v)
	}
	if class, err := s.ClassifyFiscalSubject(context.Background(), "memory", id); err != nil || class != FiscalScopeLegacy {
		t.Fatalf("class=%q err=%v", class, err)
	}
	for _, table := range []string{"fiscal_scope_bindings", "fiscal_binding_links"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s rows=%d err=%v", table, n, err)
		}
	}
	partial := openV17FiscalFixture(t, filepath.Join(t.TempDir(), "partial.db"))
	if _, err := partial.db.Exec(`CREATE TABLE fiscal_scope_bindings(binding_hash TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := migrateV17ToV18(partial.db); err == nil {
		t.Fatal("partial migration accepted")
	}
	if v, _ := readSchemaVersion(partial.db); v != 17 {
		t.Fatalf("rollback version=%d", v)
	}
}
func TestFiscalPersistenceInventoryAndImmutability(t *testing.T) {
	s := newTestStore(t)
	if v, _ := readSchemaVersion(s.db); v != 18 {
		t.Fatalf("fresh version=%d", v)
	}
	legacyID := saveFiscalFixture(t, s, "legacy/opaque", "20123456789")
	v1ID, badID := saveFiscalFixture(t, s, "v1/valid", "20100070970"), saveFiscalFixture(t, s, "v1/bad", "20100070970")
	binding := fiscalTestBinding("agent")
	if err := s.StoreFiscalScopeBinding(context.Background(), binding, "2024-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	link := FiscalBindingLink{ID: "link-1", SubjectType: "memory", SubjectID: v1ID, Sequence: 1, BindingHash: core.FiscalScopeHash(binding), ActEvidenceHash: strings.Repeat("b", 64), ResultingEnvelopeHash: strings.Repeat("c", 64), AuditRefType: "observation", AuditRefID: v1ID, CreatedAt: "2024-01-01T00:00:00Z"}
	if err := s.StoreFiscalBindingLink(context.Background(), link); err != nil {
		t.Fatal(err)
	}
	bad := fiscalTestBinding("other")
	badHash := core.FiscalScopeHash(bad)
	if _, err := s.db.Exec(`INSERT INTO fiscal_scope_bindings VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, badHash, "v1", bad.Tenant, bad.Organization, bad.Company, bad.FiscalPeriod, bad.LedgerBook, bad.OperationType, bad.SourceSnapshot, bad.PolicyVersion, bad.Actor, bad.AuthorityLevel, []byte("tampered"), "2024-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	link.ID, link.SubjectID, link.BindingHash, link.AuditRefID = "link-2", badID, badHash, badID
	if err := s.StoreFiscalBindingLink(context.Background(), link); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]FiscalScopeClass{legacyID: FiscalScopeLegacy, v1ID: FiscalScopeV1, badID: FiscalScopeUnverifiable} {
		if got, err := s.ClassifyFiscalSubject(context.Background(), "memory", id); err != nil || got != want {
			t.Fatalf("class=%q want=%q err=%v", got, want, err)
		}
	}
	report, err := s.FiscalInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.InvalidLegacyRUCCount != 1 || len(report.InvalidLegacyOpaqueIDs) != 1 || strings.Contains(report.InvalidLegacyOpaqueIDs[0], "20123456789") || strings.Contains(report.InvalidLegacyOpaqueIDs[0], legacyID) {
		t.Fatalf("unsafe inventory: %+v", report)
	}
	for _, statement := range []string{`UPDATE fiscal_scope_bindings SET actor='x'`, `DELETE FROM fiscal_binding_links WHERE id='link-1'`} {
		if _, err := s.db.Exec(statement); err == nil {
			t.Fatalf("immutability accepted %q", statement)
		}
	}
}
func TestFiscalRuntimeModesAndDowngradeRefusal(t *testing.T) {
	cases := []struct {
		raw        string
		mode       FiscalRuntimeMode
		legacy, v1 string
	}{{"", FiscalRuntimeShadow, auth.CodeFiscalWriteGateClosed, auth.CodeFiscalWriteGateClosed}, {"shadow", FiscalRuntimeShadow, auth.CodeFiscalWriteGateClosed, auth.CodeFiscalWriteGateClosed}, {"enforce", FiscalRuntimeEnforce, core.ScopeBindingRequired, ""}, {"legacy_compat", FiscalRuntimeLegacyCompat, "", auth.CodeFiscalWriteGateClosed}, {"read_only", FiscalRuntimeReadOnly, auth.CodeFiscalWriteGateClosed, auth.CodeFiscalWriteGateClosed}}
	for _, tt := range cases {
		mode, err := ParseFiscalRuntimeMode(tt.raw)
		if err != nil || mode != tt.mode {
			t.Fatalf("mode=%q err=%v", mode, err)
		}
		for class, want := range map[FiscalScopeClass]string{FiscalScopeLegacy: tt.legacy, FiscalScopeV1: tt.v1} {
			err := mode.CheckProtectedWrite(class)
			got := auth.Code(err)
			if e, ok := err.(*core.FiscalScopeError); ok {
				got = e.Code
			}
			if got != want {
				t.Fatalf("mode=%s class=%s code=%q want=%q", mode, class, got, want)
			}
		}
	}
	if _, err := ParseFiscalRuntimeMode("unsafe"); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if err := requireSupportedSchemaVersion(18, 17); err == nil {
		t.Fatal("pre-v18 binary accepted v18")
	}
}
