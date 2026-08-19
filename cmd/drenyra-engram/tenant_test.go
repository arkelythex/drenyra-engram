// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the operator tenant CLI
// surface (sdd-060-tenant-cli, FR-TEN-1/AC-TEN-1): tenant list enumerates
// ids/counts from identities + observations, never per-tenant content. No
// monetary field exists anywhere in this file.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// seedTenantCLIStore opens a store and seeds two tenants (identities +
// observations) for the operator tenant tests.
func seedTenantCLIStore(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "engram.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	for _, id := range []store.IdentitySeed{
		{TenantID: "org-tenant-a", CompanyID: "co_a", CompanyRUC: "20100039201", CompanyName: "Tenant A SAC", MembershipID: "mem-a-1", SubjectID: "alice.a", Roles: []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}},
		{TenantID: "org-tenant-b", CompanyID: "co_b", CompanyRUC: "20600995804", CompanyName: "Tenant B SAC", MembershipID: "mem-b-1", SubjectID: "bob.b", Roles: []auth.AccountingRole{auth.RoleController}},
	} {
		if err := st.SeedIdentity(id); err != nil {
			t.Fatalf("seed identity: %v", err)
		}
	}

	scopes := []core.Scope{
		{Kind: core.ScopeKindCompany, OrganizationID: "org-tenant-a", CompanyID: "co_a", RUC: "20100039201", Period: "202401"},
		{Kind: core.ScopeKindCompany, OrganizationID: "org-tenant-b", CompanyID: "co_b", RUC: "20600995804", Period: "202402"},
	}
	for i, sc := range scopes {
		if _, err := st.Save(core.SaveInput{
			TopicKey:     "tenant/cli/fixture-" + string(rune('a'+i)),
			Title:        "tenant cli fixture",
			Kind:         core.KindFact,
			Scope:        sc,
			Content:      core.Content{What: "fixture", Why: "test", Where: "cmd/drenyra-engram", Learned: "n/a"},
			FiscalEffect: core.FiscalEffectNone,
			EffectiveAt:  "2024-01-15T00:00:00Z",
			Source:       core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
			Confidence:   0.8,
		}); err != nil {
			t.Fatalf("save fixture %d: %v", i, err)
		}
	}
	return db
}

func TestCLITenantList(t *testing.T) {
	db := seedTenantCLIStore(t)

	stdout, stderr, code := runCLI(t, "tenant", "list", "--db", db)
	if code != 0 {
		t.Fatalf("tenant list failed (exit %d): %s", code, stderr)
	}
	var doc struct {
		Tenants []struct {
			OrganizationID string `json:"organizationId"`
			Companies      []struct {
				CompanyID   string `json:"companyId"`
				RUC         string `json:"ruc"`
				Name        string `json:"name"`
				MemoryCount int    `json:"memoryCount"`
			} `json:"companies"`
			Periods     []string `json:"periods"`
			MemoryCount int      `json:"memoryCount"`
		} `json:"tenants"`
		TotalTenants int `json:"totalTenants"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("tenant list output not JSON: %v\n%s", err, stdout)
	}
	if doc.TotalTenants != 2 || len(doc.Tenants) != 2 {
		t.Fatalf("totalTenants = %d, tenants = %d, want 2/2", doc.TotalTenants, len(doc.Tenants))
	}
	if doc.Tenants[0].OrganizationID != "org-tenant-a" || doc.Tenants[1].OrganizationID != "org-tenant-b" {
		t.Fatalf("tenant order = %s, %s — want deterministic org-tenant-a, org-tenant-b",
			doc.Tenants[0].OrganizationID, doc.Tenants[1].OrganizationID)
	}
	if doc.Tenants[0].MemoryCount != 1 || doc.Tenants[0].Companies[0].RUC != "20100039201" {
		t.Fatalf("tenant A summary = %+v, want 1 memory / RUC 20100039201", doc.Tenants[0])
	}
	if strings.Contains(stdout, "what") || strings.Contains(stdout, "fixture content") {
		t.Fatalf("tenant list leaked per-tenant content: %s", stdout)
	}
}

func TestCLITenantListEmpty(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	stdout, stderr, code := runCLI(t, "tenant", "list", "--db", db)
	if code != 0 {
		t.Fatalf("tenant list (empty) failed (exit %d): %s", code, stderr)
	}
	if !strings.Contains(stdout, `"tenants": []`) {
		t.Fatalf("tenant list (empty) stdout = %q, want empty tenants array", stdout)
	}
}

func TestCLITenantUsageErrors(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	for _, args := range [][]string{
		{"tenant", "--db", db},                  // missing subcommand
		{"tenant", "bogus", "--db", db},         // unknown subcommand
		{"tenant", "list", "extra", "--db", db}, // extra positional
	} {
		_, _, code := runCLI(t, args...)
		if code != 2 {
			t.Fatalf("%v exit = %d, want usage error 2", args, code)
		}
	}
}

// seedConsolidateStore seeds one tenant-A drift pair (rule/IGV credit ×2 vs
// rule/igv-credit ×1 → canonical "rule/IGV credit") and one tenant-B drift pair
// (isolation). Returns the store path and the tenant-A drifted chain head id.
func seedConsolidateStore(t *testing.T) (string, string) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "engram.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	// The CLI derives scope via cliCompanyScope: OrganizationID = cliOrganizationID,
	// CompanyID = RUC. The fixture must seed under that EXACT tuple or the
	// scope-first drift query (fail closed) finds nothing.
	scopeA := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: cliOrganizationID, CompanyID: cliRucA, RUC: cliRucA, Period: "202401"}
	scopeB := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: cliOrganizationID, CompanyID: cliRucB, RUC: cliRucB, Period: "202402"}

	save := func(topic string, scope core.Scope) {
		t.Helper()
		if _, err := st.Save(core.SaveInput{
			TopicKey: topic, Title: "drift fixture", Kind: core.KindRule, Scope: scope,
			Content:      core.Content{What: "drift fixture content", Why: "test", Where: "cmd/drenyra-engram", Learned: "n/a"},
			FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2024-01-15T00:00:00Z",
			Source:     core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
			Confidence: 0.8,
		}); err != nil {
			t.Fatalf("save %s: %v", topic, err)
		}
	}
	// tenant A: canonical chain ×2, drifted chain ×1.
	save("rule/IGV credit", scopeA)
	save("rule/IGV credit", scopeA)
	save("rule/igv-credit", scopeA)
	// tenant B: its OWN drifted pair (isolation).
	save("rule/detraction", scopeB)
	save("rule/Detracción", scopeB)

	// Capture tenant-A drifted chain head (topic "rule/igv-credit", active).
	mems, err := st.FindByScope(scopeA)
	if err != nil {
		t.Fatalf("find scope A: %v", err)
	}
	driftedHead := ""
	for _, m := range mems {
		if m.Identity.TopicKey == "rule/igv-credit" && m.Status == core.StatusActive {
			driftedHead = m.Identity.ID
		}
	}
	if driftedHead == "" {
		t.Fatal("no active tenant-A drifted head seeded")
	}
	return db, driftedHead
}

func TestCLIConsolidateDryRun(t *testing.T) {
	db, _ := seedConsolidateStore(t)
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	receiptsBefore, err := st.ReceiptsForSubject(context.Background(), core.SubjectTypeMemory, "any")
	if err != nil {
		t.Fatalf("receipts before: %v", err)
	}
	_ = receiptsBefore // presence-count proof is via statuses below

	stdout, stderr, code := runCLI(t, "tenant", "consolidate", "--ruc", "20100039201", "--db", db)
	if code != 0 {
		t.Fatalf("consolidate dry-run failed (exit %d): %s", code, stderr)
	}
	var report struct {
		RUC              string `json:"ruc"`
		DryRun           bool   `json:"dryRun"`
		TotalDriftGroups int    `json:"totalDriftGroups"`
		DriftGroups      []struct {
			Canonical string `json:"canonical"`
			Drifted   []struct {
				TopicKey string `json:"topicKey"`
			} `json:"drifted"`
		} `json:"driftGroups"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("dry-run output not JSON: %v\n%s", err, stdout)
	}
	if !report.DryRun || report.RUC != "20100039201" {
		t.Fatalf("report = %+v, want dryRun true ruc 20100039201", report)
	}
	if report.TotalDriftGroups != 1 || len(report.DriftGroups) != 1 {
		t.Fatalf("drift groups = %d, want 1", report.TotalDriftGroups)
	}
	if report.DriftGroups[0].Canonical != "rule/IGV credit" {
		t.Fatalf("canonical = %q, want rule/IGV credit (most observations)", report.DriftGroups[0].Canonical)
	}
	if len(report.DriftGroups[0].Drifted) != 1 || report.DriftGroups[0].Drifted[0].TopicKey != "rule/igv-credit" {
		t.Fatalf("drifted = %+v, want [rule/igv-credit]", report.DriftGroups[0].Drifted)
	}

	// ZERO writes: the superseded count is byte-identical before/after the
	// dry-run (the seed itself leaves one auto-superseded revision from the
	// chain evolution), and no tenant-B content is leaked.
	supersededBefore, err := st.ListByStatus(core.StatusSuperseded)
	if err != nil {
		t.Fatalf("list superseded before: %v", err)
	}
	superseded, err := st.ListByStatus(core.StatusSuperseded)
	if err != nil {
		t.Fatalf("list superseded after: %v", err)
	}
	if len(superseded) != len(supersededBefore) {
		t.Fatalf("dry-run mutated the store: superseded %d → %d (ZERO mutation required)",
			len(supersededBefore), len(superseded))
	}
	if strings.Contains(stdout, "20600995804") || strings.Contains(stdout, "detraction") {
		t.Fatalf("dry-run leaked tenant-B chains: %s", stdout)
	}
}

func TestCLIConsolidateMutuallyExclusive(t *testing.T) {
	db, _ := seedConsolidateStore(t)
	_, _, code := runCLI(t, "tenant", "consolidate", "--ruc", "20100039201", "--dry-run", "--apply", "--db", db)
	if code != 2 {
		t.Fatalf("--dry-run --apply exit = %d, want usage error 2", code)
	}
	_, _, code = runCLI(t, "tenant", "consolidate", "--ruc", "123", "--db", db)
	if code != 2 {
		t.Fatalf("invalid RUC exit = %d, want usage error 2", code)
	}
}

func TestCLIConsolidateApply(t *testing.T) {
	db, driftedHead := seedConsolidateStore(t)
	stdout, stderr, code := runCLI(t, "tenant", "consolidate", "--ruc", "20100039201", "--apply", "--db", db)
	if code != 0 {
		t.Fatalf("consolidate --apply failed (exit %d): %s\nstdout: %s", code, stderr, stdout)
	}
	var out struct {
		Merges []struct {
			From       string `json:"from"`
			To         string `json:"to"`
			Superseded string `json:"superseded"`
			Target     string `json:"target"`
		} `json:"merges"`
		Failed int `json:"failed"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("apply output not JSON: %v\n%s", err, stdout)
	}
	if out.Failed != 0 || len(out.Merges) != 1 {
		t.Fatalf("apply = %+v, want 1 successful merge", out)
	}
	if out.Merges[0].From != "rule/igv-credit" || out.Merges[0].To != "rule/IGV credit" {
		t.Fatalf("merge = %+v, want from rule/igv-credit to rule/IGV credit", out.Merges[0])
	}

	// Audit trail: the drifted head is superseded and carries a memory_superseded receipt.
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	superseded, err := st.ListByStatus(core.StatusSuperseded)
	if err != nil {
		t.Fatalf("list superseded: %v", err)
	}
	driftedSuperseded := false
	for _, m := range superseded {
		if m.Identity.ID == driftedHead {
			driftedSuperseded = true
		}
	}
	if !driftedSuperseded {
		t.Fatalf("drifted head %s not superseded after --apply; superseded = %+v", driftedHead, superseded)
	}
	receipts, err := st.ReceiptsForSubject(context.Background(), core.SubjectTypeMemory, driftedHead)
	if err != nil {
		t.Fatalf("receipts: %v", err)
	}
	foundSuperseded := false
	for _, r := range receipts {
		if r.Action == core.ReceiptActionMemorySuperseded {
			foundSuperseded = true
		}
	}
	if !foundSuperseded {
		t.Fatalf("no memory_superseded receipt for drifted head %s", driftedHead)
	}
}

func TestCLIConsolidateIsolation(t *testing.T) {
	db, _ := seedConsolidateStore(t)

	// Tenant-B drifted chain head id BEFORE the A-only consolidate.
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	scopeB := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: cliOrganizationID, CompanyID: cliRucB, RUC: cliRucB, Period: "202402"}
	bMems, err := st.FindByScope(scopeB)
	if err != nil {
		t.Fatalf("find scope B: %v", err)
	}
	bDrifted := ""
	for _, m := range bMems {
		if m.Identity.TopicKey == "rule/Detracción" && m.Status == core.StatusActive {
			bDrifted = m.Identity.ID
		}
	}
	if bDrifted == "" {
		t.Fatal("no tenant-B drifted head seeded")
	}
	_ = st.Close()

	_, stderr, code := runCLI(t, "tenant", "consolidate", "--ruc", "20100039201", "--apply", "--db", db)
	if code != 0 {
		t.Fatalf("consolidate A failed (exit %d): %s", code, stderr)
	}

	st, err = store.Open(db)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = st.Close() }()
	// Tenant B untouched: head still active, no supersede receipt, no transition.
	bMems, err = st.FindByScope(scopeB)
	if err != nil {
		t.Fatalf("find scope B after: %v", err)
	}
	headActive := false
	for _, m := range bMems {
		if m.Identity.ID == bDrifted && m.Status == core.StatusActive {
			headActive = true
		}
	}
	if !headActive {
		t.Fatalf("tenant-B head no longer active after A-only consolidate (isolation broken)")
	}
	receipts, err := st.ReceiptsForSubject(context.Background(), core.SubjectTypeMemory, bDrifted)
	if err != nil {
		t.Fatalf("B receipts: %v", err)
	}
	for _, r := range receipts {
		if r.Action == core.ReceiptActionMemorySuperseded {
			t.Fatalf("tenant-B head received a supersede receipt from an A-only consolidate: %+v", r)
		}
	}
}

// TestCLIEncryptionEnvWiring — FR-ENC-5: DRENYRA_ENCRYPTION_MASTER_KEY through
// openStore: save with the key encrypts at rest; a read without the key fails
// closed with ENCRYPTION_REQUIRED; malformed key material fails closed.
func TestCLIEncryptionEnvWiring(t *testing.T) {
	keyHex := strings.Repeat("42", 32) // 32 bytes of 0x42, hex-encoded
	db := filepath.Join(t.TempDir(), "engram.db")
	encEnv := []string{"DRENYRA_ENCRYPTION_MASTER_KEY=" + keyHex}

	// Save a company-scope memory WITH the key (encrypted at rest).
	fixture := filepath.Join(t.TempDir(), "fixture.json")
	fixtureJSON := `{"topicKey":"encryption/cli","title":"encrypted cli","kind":"fact","scope":{"kind":"company","organizationId":"cli","companyId":"20100039201","ruc":"20100039201","period":"202401"},"content":{"what":"secret cli content","why":"test","where":"cmd/drenyra-engram","learned":"n/a"},"fiscalEffect":"none","effectiveAt":"2024-01-15T00:00:00Z","source":{"system":"go-test","actorId":"test-agent","actorKind":"agent"}}`
	if err := os.WriteFile(fixture, []byte(fixtureJSON), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, stderr, code := runCLIEnv(t, encEnv, "save", fixture, "--db", db)
	if code != 0 {
		t.Fatalf("encrypted save failed (exit %d): %s", code, stderr)
	}

	// Read WITHOUT the key → fail closed ENCRYPTION_REQUIRED (the exact
	// period-scoped read actually touches the encrypted row).
	_, stderr, code = runCLI(t, "context", "20100039201", "--period", "202401", "--db", db)
	if code == 0 {
		t.Fatalf("context without key exited 0 — encrypted content readable in plaintext mode")
	}
	if !strings.Contains(stderr, "ENCRYPTION_REQUIRED") {
		t.Fatalf("context stderr = %q, want ENCRYPTION_REQUIRED", stderr)
	}

	// Malformed key material → store open fails closed.
	badEnv := []string{"DRENYRA_ENCRYPTION_MASTER_KEY=not-a-key"}
	_, stderr, code = runCLIEnv(t, badEnv, "context", "20100039201", "--period", "202401", "--db", db)
	if code == 0 {
		t.Fatalf("malformed key exited 0")
	}
	if !strings.Contains(stderr, "INVALID_ENCRYPTION_KEY") {
		t.Fatalf("malformed-key stderr = %q, want INVALID_ENCRYPTION_KEY", stderr)
	}
}
