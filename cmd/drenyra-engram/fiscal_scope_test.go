// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the Delivery Slice 6 CLI
// fiscal-scope binding-file loader and presence-aware acknowledgement flag
// helper (openspec/changes/fiscal-runtime-foundations, design.md "Public
// contracts > CLI"): both are PURE/IO-boundary primitives consumed by
// cmdApprove — no store, no session, no network.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// fiscalCLIRuc is a checksum-VALID SUNAT RUC (unlike the package's default
// cliRucA fixture, which is checksum-invalid — the legacy-scope test RUC and
// this new v1-binding RUC are deliberately different fixtures, matching
// design.md's "frozen legacy scope stays frozen" boundary).
const fiscalCLIRuc = "20100070970"

// seedCLIFiscalIdentity seeds one identity + expiring session for a company
// scoped by ruc (parameterized so the fiscal-scope end-to-end tests use a
// checksum-valid RUC without disturbing the package's default cliRucA
// fixtures/identity).
func seedCLIFiscalIdentity(t *testing.T, db, ruc string) string {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	membershipID := "membership-cli-fiscal"
	if err := st.SeedIdentity(store.IdentitySeed{
		TenantID:     cliOrganizationID,
		CompanyID:    ruc,
		CompanyRUC:   ruc,
		CompanyName:  "CLI Fiscal Demo SAC",
		MembershipID: membershipID,
		SubjectID:    "maria.torres",
		Roles:        []auth.AccountingRole{auth.RoleController},
	}); err != nil {
		t.Fatalf("seed fiscal identity: %v", err)
	}
	token := "cli-fiscal-fixture-token"
	if err := st.SeedSession(store.SessionSeed{
		ID:                   "session-cli-fiscal",
		TokenHash:            sha256HexCLI(token),
		MembershipID:         membershipID,
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed fiscal session: %v", err)
	}
	return token
}

// saveMaterialFiscalMemory saves a MATERIAL, gated memory in ruc's exact scope
// through the built CLI and returns its id — the anti-rubber-stamp policy
// (authz.ValidateReviewChecksV1) requires explicit review acknowledgements to
// approve it.
func saveMaterialFiscalMemory(t *testing.T, db, ruc, period string) string {
	t.Helper()
	fixture := fmt.Sprintf(`{"topicKey":"fiscal/cli/%s","title":"Ajuste material","kind":"decision","scope":{"kind":"company","organizationId":"cli","companyId":%q,"ruc":%q,"period":%q},"content":{"what":"ajuste material","why":"comprobante tardio","where":"cli","learned":"n/a"},"fiscalEffect":"adjustment","materialityLevel":"material","effectiveAt":"2026-01-31T00:00:00.000Z","source":{"system":"cli","actorId":"cli-user","actorKind":"agent"}}`,
		period, ruc, ruc, period)
	path := filepath.Join(t.TempDir(), "material.json")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write material fixture: %v", err)
	}
	return saveViaCLI(t, db, path)
}

// fiscalScopeFixtureJSON builds a strict v1 binding document matching ruc's
// exact legacy scope axes (tenant=cliOrganizationID, organization=ruc,
// company=ruc — the CLI derives companyId=ruc, contracts/scope.md) with the
// given operationType/authorityLevel.
func fiscalScopeFixtureJSON(ruc, period, operationType, authorityLevel string) string {
	return fmt.Sprintf(`{
		"version": "v1",
		"tenant": %q,
		"organization": %q,
		"company": %q,
		"fiscalPeriod": %q,
		"ledgerBook": "purchases",
		"operationType": %q,
		"sourceSnapshot": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"policyVersion": "fiscal-v1",
		"actor": "controller-1",
		"authorityLevel": %q
	}`, cliOrganizationID, ruc, ruc, period, operationType, authorityLevel)
}

// TestCLIApproveWithFiscalScopeSucceeds (RED->GREEN, spec.md "Eligible
// professional approves exact material envelope" through the CLI surface):
// a material memory approved with a matching --fiscal-scope binding and both
// explicit positive acknowledgements succeeds and the machine-readable output
// carries the v1 binding version/hash and the recorded review-check state.
func TestCLIApproveWithFiscalScopeSucceeds(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	const period = "202601"
	token := seedCLIFiscalIdentity(t, db, fiscalCLIRuc)
	id := saveMaterialFiscalMemory(t, db, fiscalCLIRuc, period)
	h1 := memoryEnvelope(t, db, id)

	dir := t.TempDir()
	env := writeSessionFile(t, dir, token)
	bindingPath := filepath.Join(t.TempDir(), "binding.json")
	if err := os.WriteFile(bindingPath, []byte(fiscalScopeFixtureJSON(fiscalCLIRuc, period, "memory.approve", "EXECUTE")), 0o600); err != nil {
		t.Fatalf("write binding fixture: %v", err)
	}

	// The setup save above (saveMaterialFiscalMemory) is a plain legacy CLI
	// save (needs the TestMain default legacy_compat); this approve carries
	// --fiscal-scope and must SUCCEED (v1-classified, needs enforce) — each
	// runCLIEnv call spawns its own subprocess reading the CURRENT env at
	// launch (see TestMain's doc comment), so overriding here doesn't disturb
	// the earlier legacy calls.
	t.Setenv("DRENYRA_FISCAL_RUNTIME_MODE", "enforce")
	stdout, stderr, code := runCLIEnv(t, env,
		"approve", id, "--fiscal-scope", bindingPath,
		"--expected-envelope", h1, "--reason", "revisado evidencia y reglas aplicables",
		"--evidence-inspected", "--applicable-rules-inspected", "--db", db)
	if code != 0 {
		t.Fatalf("fiscal-bound approve failed (exit %d): stdout=%q stderr=%q", code, stdout, stderr)
	}
	var output struct {
		CurrentStatus        string `json:"currentStatus"`
		FiscalScopeVersion   string `json:"fiscalScopeVersion"`
		FiscalScopeHash      string `json:"fiscalScopeHash"`
		ReviewChecksRecorded struct {
			EvidenceInspected        struct{ Present, Value bool } `json:"evidenceInspected"`
			ApplicableRulesInspected struct{ Present, Value bool } `json:"applicableRulesInspected"`
		} `json:"reviewChecksRecorded"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("decode approve output: %v\n%s", err, stdout)
	}
	if output.CurrentStatus != "approved" {
		t.Fatalf("currentStatus = %q, want approved", output.CurrentStatus)
	}
	if output.FiscalScopeVersion != "v1" {
		t.Fatalf("fiscalScopeVersion = %q, want v1", output.FiscalScopeVersion)
	}
	if len(output.FiscalScopeHash) != 64 {
		t.Fatalf("fiscalScopeHash = %q, want 64 lowercase hex chars", output.FiscalScopeHash)
	}
	if !output.ReviewChecksRecorded.EvidenceInspected.Present || !output.ReviewChecksRecorded.EvidenceInspected.Value {
		t.Fatalf("evidenceInspected recorded = %+v, want present+true", output.ReviewChecksRecorded.EvidenceInspected)
	}
	if !output.ReviewChecksRecorded.ApplicableRulesInspected.Present || !output.ReviewChecksRecorded.ApplicableRulesInspected.Value {
		t.Fatalf("applicableRulesInspected recorded = %+v, want present+true", output.ReviewChecksRecorded.ApplicableRulesInspected)
	}
}

// TestCLIApproveMaterialWithoutAcknowledgementsFailsClosed (TRIANGULATE, spec.md
// "Missing or false acknowledgement is rejected"): the presence-aware
// acknowledgement flags are required REGARDLESS of --fiscal-scope — omitting
// them on a material memory fails REVIEW_CHECKS_REQUIRED and leaves the memory
// unchanged (still pending_review).
func TestCLIApproveMaterialWithoutAcknowledgementsFailsClosed(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	const period = "202602"
	token := seedCLIFiscalIdentity(t, db, fiscalCLIRuc)
	id := saveMaterialFiscalMemory(t, db, fiscalCLIRuc, period)
	h1 := memoryEnvelope(t, db, id)

	dir := t.TempDir()
	env := writeSessionFile(t, dir, token)
	stdout, stderr, code := runCLIEnv(t, env,
		"approve", id, "--expected-envelope", h1, "--reason", "sin acuse explicito", "--db", db)
	if code == 0 {
		t.Fatalf("material approval without acknowledgements must fail closed; stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "REVIEW_CHECKS_REQUIRED") {
		t.Fatalf("stderr must carry REVIEW_CHECKS_REQUIRED: %q", stderr)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	mem, ok := st.FindByID(id)
	if !ok || mem.Status != core.StatusPendingReview {
		t.Fatalf("rejected approval mutated status: %+v ok=%v", mem, ok)
	}
}

// TestCLIApproveInvalidFiscalScopeFailsBeforeStoreOpen (RED->GREEN, design.md
// boundary matrix "CLI approval | Binding file | Before token/auth"): a
// malformed --fiscal-scope document (a duplicate canonical key) fails closed
// BEFORE the session token is loaded or the store is opened — proven here by
// pointing --db at a path that does not yet exist and confirming the failed
// command never creates it (SQLite creates the file eagerly on open).
func TestCLIApproveInvalidFiscalScopeFailsBeforeStoreOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "never-created.db")
	malformed := `{"version":"v1","version":"v1","tenant":"t","organization":"o","company":"20100070970","fiscalPeriod":"202601","ledgerBook":"purchases","operationType":"memory.approve","sourceSnapshot":"` +
		strings.Repeat("a", 64) + `","policyVersion":"p","actor":"a","authorityLevel":"EXECUTE"}`
	bindingPath := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(bindingPath, []byte(malformed), 0o600); err != nil {
		t.Fatalf("write malformed binding fixture: %v", err)
	}

	// No session file is set up at all — if the command reached token loading
	// it would fail with AUTHENTICATION_REQUIRED instead, which would also
	// prove nothing was mutated but would NOT prove ordering. The binding
	// error must be the one actually observed.
	stdout, stderr, code := runCLI(t,
		"approve", "some-memory-id", "--fiscal-scope", bindingPath,
		"--expected-envelope", "h1", "--reason", "x", "--db", dbPath)
	if code == 0 {
		t.Fatalf("malformed fiscal-scope binding must fail closed; stdout=%q", stdout)
	}
	if !strings.Contains(stderr, core.ScopeBindingInvalid) {
		t.Fatalf("stderr must carry %s: %q", core.ScopeBindingInvalid, stderr)
	}
	if strings.Contains(stderr, "AUTHENTICATION_REQUIRED") {
		t.Fatalf("binding validation must fail BEFORE session/token work, not after: %q", stderr)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("store must never be opened before binding validation; db file state = %v", err)
	}
}

// TestCLISeedLocalDevRejectsChecksumInvalidRUC (RED->GREEN, proposal.md "Keep
// `auth seed-local-dev` explicitly local-development-only and checksum-valid";
// design.md boundary matrix "CLI local seed | RUC flag | Before DB open"): a
// shape-valid but checksum-invalid RUC (the package's cliRucA fixture) is now
// REJECTED before the store is ever opened — no identity/session row is
// created. This is a deliberate behavior change from the pre-Slice-6 shape-
// only core.IsValidRUC check (proven by the companion database-file-absence
// assertion, mirroring the production-rejection test's non-mutation proof).
func TestCLISeedLocalDevRejectsChecksumInvalidRUC(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "never-created.db")
	stdout, stderr, code := runCLIEnv(t, []string{"DRENYRA_ENV=local_dev"},
		"auth", "seed-local-dev", "--db", dbPath, "--tenant", cliOrganizationID, "--company", cliRucA,
		"--ruc", cliRucA, "--subject", "maria.torres", "--roles", "controller")
	if code == 0 {
		t.Fatalf("a checksum-invalid RUC must be rejected even in local_dev mode; stdout=%q", stdout)
	}
	if stdout != "" {
		t.Fatalf("rejected seed must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "invalid") && !strings.Contains(stderr, "RUC") {
		t.Fatalf("stderr must explain the RUC rejection: %q", stderr)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("store must never be opened for a checksum-invalid RUC; db file state = %v", err)
	}
}

// validFiscalScopeJSON is one canonical, checksum-valid v1 binding document —
// exactly the ten canonical elements in canonical order, matching
// internal/core/fiscal_scope.go's DecodeFiscalScopeV1JSON contract.
const validFiscalScopeJSON = `{
	"version": "v1",
	"tenant": "cli-tenant",
	"organization": "cli-org",
	"company": "20100070970",
	"fiscalPeriod": "202601",
	"ledgerBook": "purchases",
	"operationType": "memory.approve",
	"sourceSnapshot": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"policyVersion": "fiscal-v1",
	"actor": "controller-1",
	"authorityLevel": "EXECUTE"
}`

func writeTempFiscalScopeFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "binding.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture binding file: %v", err)
	}
	return path
}

// TestLoadFiscalScopeBindingFileValid (RED->GREEN): a well-formed strict v1
// binding file decodes into a *core.FiscalWriteIntent carrying the exact
// decoded binding — the CLI's canonical representation (design.md "Strict
// input and canonical hash").
func TestLoadFiscalScopeBindingFileValid(t *testing.T) {
	path := writeTempFiscalScopeFile(t, validFiscalScopeJSON)
	intent, err := loadFiscalScopeBindingFile(path)
	if err != nil {
		t.Fatalf("load valid binding file: %v", err)
	}
	if intent == nil {
		t.Fatal("intent must not be nil for a valid binding file")
	}
	if intent.Binding.Company != "20100070970" {
		t.Fatalf("company = %q, want the checksum-valid fixture RUC", intent.Binding.Company)
	}
	if intent.Binding.OperationType != "memory.approve" {
		t.Fatalf("operationType = %q, want memory.approve", intent.Binding.OperationType)
	}
	if intent.Binding.AuthorityLevel != core.AuthorityLevelExecute {
		t.Fatalf("authorityLevel = %q, want EXECUTE", intent.Binding.AuthorityLevel)
	}
}

// TestLoadFiscalScopeBindingFileRejectsDuplicateKey (TRIANGULATE — spec.md
// "Ambiguous encoding is rejected"): a binding document repeating a canonical
// key fails closed with the typed *core.FiscalScopeError BEFORE any file
// content is trusted — this proves the CLI reuses the SAME canonical decoder
// as the core/store boundary rather than a private, weaker parser.
func TestLoadFiscalScopeBindingFileRejectsDuplicateKey(t *testing.T) {
	malformed := `{"version":"v1","version":"v1","tenant":"t","organization":"o","company":"20100070970","fiscalPeriod":"202601","ledgerBook":"purchases","operationType":"memory.approve","sourceSnapshot":"` +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `","policyVersion":"p","actor":"a","authorityLevel":"EXECUTE"}`
	path := writeTempFiscalScopeFile(t, malformed)
	_, err := loadFiscalScopeBindingFile(path)
	if err == nil {
		t.Fatal("a duplicate-key binding document must fail closed")
	}
	fse, ok := err.(*core.FiscalScopeError)
	if !ok {
		t.Fatalf("error = %T (%v), want *core.FiscalScopeError", err, err)
	}
	if fse.Code != core.ScopeBindingInvalid {
		t.Fatalf("code = %q, want %q", fse.Code, core.ScopeBindingInvalid)
	}
}

// TestLoadFiscalScopeBindingFileMissingFile (TRIANGULATE): a non-existent path
// fails closed with a plain I/O error — never a nil intent silently treated as
// "no binding requested" (the caller decides that only from an EMPTY --fiscal-
// scope flag, never from a read failure on a non-empty one).
func TestLoadFiscalScopeBindingFileMissingFile(t *testing.T) {
	_, err := loadFiscalScopeBindingFile(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("a missing binding file must fail closed, not silently resolve to no binding")
	}
}

// TestReviewAckFromFlagPresence (RED->GREEN, design.md "Strict input and
// canonical hash" — presence-aware flag semantics): omission maps to
// {Present:false, Value:false}; --flag=false maps to {Present:true,
// Value:false}; a bare --flag maps to {Present:true, Value:true}. This is the
// exact three-state table the spec's "Authenticated professional approval
// acknowledgements" requirement demands distinguishing omission from false.
func TestReviewAckFromFlagPresence(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want core.ReviewAcknowledgement
	}{
		{"omitted", nil, core.ReviewAcknowledgement{Present: false, Value: false}},
		{"bare flag (implicit true)", []string{"--evidence-inspected"}, core.ReviewAcknowledgement{Present: true, Value: true}},
		{"explicit true", []string{"--evidence-inspected=true"}, core.ReviewAcknowledgement{Present: true, Value: true}},
		{"explicit false", []string{"--evidence-inspected=false"}, core.ReviewAcknowledgement{Present: true, Value: false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			value := fs.Bool("evidence-inspected", false, "")
			if err := fs.Parse(c.args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := reviewAckFromFlag(fs, "evidence-inspected", *value)
			if got != c.want {
				t.Fatalf("reviewAckFromFlag = %+v, want %+v", got, c.want)
			}
		})
	}
}
