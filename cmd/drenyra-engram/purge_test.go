// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This test freezes the v0.8 batch 4 purge + export CLI surface
// (WU-4 — docs/architecture/evidence-lifecycle-v0.8.md §2/§3/§9/§10/§12):
//
//   - purge request/cancel derive the principal ONLY from the stored CLI
//     session (auth login — never from a caller flag; there is deliberately NO
//     --actor/--subject/--role flag); without a session the mutation fails
//     closed with AUTHENTICATION_REQUIRED;
//   - cancel is the ORIGINAL requester's retraction, so ONE seeded identity
//     (accountant + records_compliance_officer) covers the full request →
//     cancel cycle — the approve/reject/execute CLI commands share the same
//     session-derivation + server-delegation shape (their deep SoD/distinct-
//     principal semantics are frozen at the store and HTTP levels);
//   - export lifecycle is a READ-ONLY scope-first query (no session) whose
//     output is the deterministic self-hashing bundle.
//
// Fixtures seed the store directly (design section 8: test helpers call
// store.SeedIdentity/SeedSession; they never depend on environment state) and
// the policy is put through the REAL resolver over the seeded session.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// cliPurgeRUC is the fixed company RUC of the CLI purge fixtures (the CLI
// derives companyId from the RUC).
const cliPurgeRUC = "20100039201"

// cliPurgePeriod is the exact fiscal period of the CLI purge fixtures (a purge
// object requires a period — the eligibility dimension resolves against it).
const cliPurgePeriod = "202401"

// cliPurgeScope is the EXACT company scope of the CLI purge fixtures: tenant
// cli (the CLI's fixed organization id), companyId derived from the RUC.
func cliPurgeScope() core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: cliOrganizationID,
		CompanyID:      cliPurgeRUC,
		RUC:            cliPurgeRUC,
		Period:         cliPurgePeriod,
	}
}

// seedPurgeCLIIdentity seeds ONE identity (roles may include several v0.8
// lifecycle roles) + one expiring session directly on db and returns the raw
// token (only its SHA-256 hash is stored).
func seedPurgeCLIIdentity(t *testing.T, db string, roles []auth.AccountingRole) string {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	membershipID := "membership-cli-purge"
	if err := st.SeedIdentity(store.IdentitySeed{
		TenantID:     cliOrganizationID,
		CompanyID:    cliPurgeRUC,
		CompanyRUC:   cliPurgeRUC,
		CompanyName:  "CLI Demo SAC",
		MembershipID: membershipID,
		SubjectID:    "ana.garcia",
		Roles:        roles,
	}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	token := "cli-purge-fixture-token"
	if err := st.SeedSession(store.SessionSeed{
		ID:                   "session-cli-purge",
		TokenHash:            sha256HexCLI(token),
		MembershipID:         membershipID,
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return token
}

// purgeCLIPrincipal mints the pre-verified principal of the seeded session
// through the REAL resolver — the same factory the CLI surface uses.
func purgeCLIPrincipal(t *testing.T, st *store.SQLiteStore, token string) auth.VerifiedApprovalPrincipal {
	t.Helper()
	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		t.Fatalf("mint fixture principal: %v", err)
	}
	return principal
}

// purgeCLIFixture seeds the evidence object + one enabled retention policy at
// the exact CLI scope directly on db and returns the object id and the
// PRE-REQUEST lifecycle snapshot hash (H_request — the virtual stored/unmanaged
// snapshot of an unbound object, §14).
func purgeCLIFixture(t *testing.T, db, token string) (string, string) {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	objResult, err := st.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       []byte("cli-purge-target-bytes-0123456789"),
		ContentType: "application/xml",
		Scope:       cliPurgeScope(),
		Source:      core.Source{System: "cli-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		t.Fatalf("store purge target object: %v", err)
	}
	_, err = st.PutRetentionPolicy(ctx, core.PutRetentionPolicyCommand{
		Scope:           cliPurgeScope(),
		Jurisdiction:    "PE",
		Legislation:     "NATIONAL-TAX",
		Authority:       "tenant-records",
		Source:          "deployment decision 2026-08-07",
		Category:        "invoice",
		MinPeriod:       "202401",
		ExpectedVersion: 0,
		Enabled:         true,
		RequestID:       "req-policy-cli-fixture",
	}, purgeCLIPrincipal(t, st, token))
	if err != nil {
		t.Fatalf("seed retention policy: %v", err)
	}
	objectID := objResult.Object.ObjectID
	h := core.ComputeLifecycleSnapshotHash(core.LifecycleSnapshot{
		ObjectID:       objectID,
		TenantID:       cliOrganizationID,
		CompanyID:      cliPurgeRUC,
		RUC:            cliPurgeRUC,
		Period:         cliPurgePeriod,
		LifecycleState: core.PurgeLifecycleStored,
		RetentionState: core.RetentionEligibility("unmanaged"),
		BlockingHolds:  []core.LifecycleHoldRef{},
	})
	return objectID, h
}

// TestCLIPurgeRequestRequiresAuthentication: purge request without an
// authenticated CLI session fails closed with AUTHENTICATION_REQUIRED and
// points at auth login — the CLI never invents identity.
func TestCLIPurgeRequestRequiresAuthentication(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	objectID := strings.Repeat("0", 64)

	// Point DRENYRA_ENGRAM_SESSION at an empty dir so a host session file can
	// never leak into the test (the same isolation the existing CLI tests use).
	stdout, stderr, code := runCLIEnv(t, sessionFileEnv(t.TempDir()),
		"purge", "request", objectID,
		"--jurisdiction", "PE", "--legislation", "NATIONAL-TAX", "--category", "invoice",
		"--expected-hash", strings.Repeat("a", 64), "--reason", "retention period elapsed",
		"--db", db)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "AUTHENTICATION_REQUIRED") || !strings.Contains(stderr, "auth login") {
		t.Fatalf("stderr must carry AUTHENTICATION_REQUIRED and point at auth login: %q", stderr)
	}
}

// TestCLIPurgeUnknownSubcommand: an unknown purge subcommand is a usage error
// (exit 2).
func TestCLIPurgeUnknownSubcommand(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	_, stderr, code := runCLI(t, "purge", "bogus", "--db", db)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "unknown purge subcommand") {
		t.Fatalf("stderr must name the unknown subcommand: %q", stderr)
	}
}

// TestCLIPurgeRequestCancelAndExport drives the full CLI cycle with ONE seeded
// identity (accountant + records_compliance_officer): purge request → purge
// cancel (the ORIGINAL requester's retraction — the pipeline returns to stored)
// → export lifecycle (the read-only deterministic bundle). Identity comes ONLY
// from the stored session file, never from a flag.
func TestCLIPurgeRequestCancelAndExport(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedPurgeCLIIdentity(t, db, []auth.AccountingRole{auth.RoleAccountant, auth.RoleRecordsComplianceOfficer})
	objectID, h := purgeCLIFixture(t, db, token)
	env := writeSessionFile(t, t.TempDir(), token)

	// request — the accountant's session derives the principal.
	stdout, stderr, code := runCLIEnv(t, env, "purge", "request", objectID,
		"--jurisdiction", "PE", "--legislation", "NATIONAL-TAX", "--category", "invoice",
		"--expected-hash", h, "--reason", "retention period elapsed",
		"--request-id", "req-purge-cli-001", "--db", db)
	if code != 0 {
		t.Fatalf("purge request exit = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	var requested core.RequestPurgeResult
	if err := json.Unmarshal([]byte(stdout), &requested); err != nil {
		t.Fatalf("decode purge request output: %v (stdout %q)", err, stdout)
	}
	if requested.IdempotentReplay || !requested.Created {
		t.Fatalf("request result = %+v, want a created pipeline", requested)
	}
	if requested.Request.Status != core.PurgeRequestStatusRequested {
		t.Fatalf("request status = %q, want requested", requested.Request.Status)
	}
	if requested.Request.RequestedBy != "ana.garcia" {
		t.Fatalf("requestedBy = %q, want ana.garcia (derived from the session, never from flags)", requested.Request.RequestedBy)
	}

	// cancel — the SAME original requester retracts; the pipeline returns to
	// stored (fresh request on the same row is a fresh act).
	stdout, stderr, code = runCLIEnv(t, env, "purge", "cancel", requested.Request.RequestID,
		"--request-id-key", "req-cancel-cli-001", "--db", db)
	if code != 0 {
		t.Fatalf("purge cancel exit = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	var cancelled core.CancelPurgeResult
	if err := json.Unmarshal([]byte(stdout), &cancelled); err != nil {
		t.Fatalf("decode purge cancel output: %v (stdout %q)", err, stdout)
	}
	if cancelled.IdempotentReplay || cancelled.Request.Status != core.PurgeRequestStatusCancelled {
		t.Fatalf("cancel result = %+v, want a fresh cancellation (status cancelled)", cancelled)
	}

	// export lifecycle — a READ-ONLY scope-first query, no session needed; the
	// output is the deterministic self-hashing bundle.
	stdout, stderr, code = runCLIEnv(t, env, "export", "lifecycle",
		"--ruc", cliPurgeRUC, "--period", cliPurgePeriod, "--db", db)
	if code != 0 {
		t.Fatalf("export lifecycle exit = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	var bundle core.EvidenceExportBundle
	if err := json.Unmarshal([]byte(stdout), &bundle); err != nil {
		t.Fatalf("decode export output: %v", err)
	}
	if bundle.Manifest.Version != core.EvidenceExportModelVersion {
		t.Fatalf("manifest version = %q, want %q", bundle.Manifest.Version, core.EvidenceExportModelVersion)
	}
	if !strings.HasPrefix(bundle.Manifest.ExportID, core.EvidenceExportModelVersion+":") {
		t.Fatalf("exportId = %q, want the content-addressed prefix", bundle.Manifest.ExportID)
	}
	if bundle.Manifest.Counts.Objects < 1 || bundle.Manifest.Counts.PurgeRequests < 1 {
		t.Fatalf("counts = %+v, want the fixture object + the cancelled request row", bundle.Manifest.Counts)
	}
	found := false
	for _, o := range bundle.Objects {
		if o.Object.ObjectID == objectID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("bundle must carry the fixture object %s", objectID)
	}
}

// TestCLIExportLifecycleRequiresRUC: export lifecycle is an exact-company-scope
// query — a missing --ruc is a usage error (exit 2), never a guessed scope.
func TestCLIExportLifecycleRequiresRUC(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	stdout, stderr, code := runCLI(t, "export", "lifecycle", "--db", db)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "--ruc") {
		t.Fatalf("stderr must require --ruc: %q", stderr)
	}
}

// cliPurgeReplaySeed is the fixed pipeline state a CLI purge replay row needs:
// the request id, the bound policy identity and the exact canonical snapshot
// hashes at each stage (all int64 cents / integer versions; no floats).
type cliPurgeReplaySeed struct {
	objectID   string
	requestID  string
	policyID   string
	policyVer  int64
	category   string
	scope      core.Scope
	hRequest   string // stored/unmanaged snapshot
	hRequested string // requested snapshot (after the request row)
	hApproved  string // approved snapshot (after one approval)
	approval   core.EvidencePurgeApproval
}

// seedCLIPurgeReplayStage opens db, seeds the evidence object + enabled
// retention policy and advances the pipeline to the given stage ("request" or
// "approved") through the REAL store path with the fixture principals. The CLI
// binary opens the SAME db path with the same derived objects root, so the
// seeded pipeline is exactly what the CLI commands operate on.
func seedCLIPurgeReplayStage(t *testing.T, db, accountantToken, recordsToken, stage string) cliPurgeReplaySeed {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	accountant := purgeCLIPrincipal(t, st, accountantToken)
	records := purgeCLIPrincipal(t, st, recordsToken)
	scope := cliPurgeScope()

	objResult, err := st.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       []byte("cli-purge-replay-target-bytes-0123456789"),
		ContentType: "application/xml",
		Scope:       scope,
		Source:      core.Source{System: "cli-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		t.Fatalf("store purge target object: %v", err)
	}
	polResult, err := st.PutRetentionPolicy(ctx, core.PutRetentionPolicyCommand{
		Scope:           scope,
		Jurisdiction:    "PE",
		Legislation:     "NATIONAL-TAX",
		Authority:       "tenant-records",
		Source:          "deployment decision 2026-08-07",
		Category:        "invoice",
		MinPeriod:       "202401",
		ExpectedVersion: 0,
		Enabled:         true,
		RequestID:       "req-policy-cli-replay-" + stage,
	}, records)
	if err != nil {
		t.Fatalf("seed retention policy: %v", err)
	}
	policy := polResult.Policy
	objectID := objResult.Object.ObjectID
	hRequest := core.ComputeLifecycleSnapshotHash(core.LifecycleSnapshot{
		ObjectID:       objectID,
		TenantID:       scope.OrganizationID,
		CompanyID:      scope.CompanyID,
		RUC:            scope.RUC,
		Period:         scope.Period,
		LifecycleState: core.PurgeLifecycleStored,
		RetentionState: core.RetentionEligibility("unmanaged"),
		BlockingHolds:  []core.LifecycleHoldRef{},
	})
	reqResult, err := st.RequestPurge(ctx, core.RequestPurgeCommand{
		ObjectID:              objectID,
		Jurisdiction:          "PE",
		Legislation:           "NATIONAL-TAX",
		Category:              "invoice",
		ExpectedLifecycleHash: hRequest,
		Reason:                "retention period elapsed",
		RequestID:             "req-purge-cli-replay-" + stage,
	}, accountant)
	if err != nil {
		t.Fatalf("request purge fixture: %v", err)
	}
	request := reqResult.Request
	hRequested := core.ComputeLifecycleSnapshotHash(core.LifecycleSnapshot{
		ObjectID:       objectID,
		TenantID:       scope.OrganizationID,
		CompanyID:      scope.CompanyID,
		RUC:            scope.RUC,
		Period:         scope.Period,
		LifecycleState: core.PurgeLifecycleRequested,
		RetentionState: core.RetentionEligibilityEligible,
		PolicyID:       policy.PolicyID,
		PolicyVersion:  policy.Version,
		Category:       policy.Category,
		BlockingHolds:  []core.LifecycleHoldRef{},
		RequestID:      request.RequestID,
		ApprovalIDs:    []string{},
	})
	seed := cliPurgeReplaySeed{
		objectID: objectID, requestID: request.RequestID,
		policyID: policy.PolicyID, policyVer: policy.Version, category: policy.Category,
		scope: scope, hRequest: hRequest, hRequested: hRequested,
	}
	if stage == "request" {
		return seed
	}
	appResult, err := st.ApprovePurge(ctx, core.ApprovePurgeCommand{
		RequestID:             request.RequestID,
		ExpectedLifecycleHash: hRequested,
		Reason:                "verified against the reviewed snapshot",
		RequestIDKey:          "req-approve-cli-replay-" + stage,
	}, records)
	if err != nil {
		t.Fatalf("approve purge fixture: %v", err)
	}
	seed.approval = appResult.Approval
	seed.hApproved = core.ComputeLifecycleSnapshotHash(core.LifecycleSnapshot{
		ObjectID:       objectID,
		TenantID:       scope.OrganizationID,
		CompanyID:      scope.CompanyID,
		RUC:            scope.RUC,
		Period:         scope.Period,
		LifecycleState: core.PurgeLifecycleApproved,
		RetentionState: core.RetentionEligibilityEligible,
		PolicyID:       policy.PolicyID,
		PolicyVersion:  policy.Version,
		Category:       policy.Category,
		BlockingHolds:  []core.LifecycleHoldRef{},
		RequestID:      request.RequestID,
		ApprovalIDs:    []string{appResult.Approval.ApprovalID},
	})
	return seed
}

// TestCLIPurgeRequestReplay (AC-L-3, FR-L.4): purge request with the SAME
// --request-id submitted twice — the second run prints the FIRST stored request
// with idempotentReplay=true, and the doctor digest (request/event/idempotency
// counts) is unchanged between the runs: no duplicate request row, event or
// receipt. The fresh-only cycle test above is NOT sufficient alone.
func TestCLIPurgeRequestReplay(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedPurgeCLIIdentity(t, db, []auth.AccountingRole{auth.RoleAccountant, auth.RoleRecordsComplianceOfficer})
	objectID, h := purgeCLIFixture(t, db, token)
	env := writeSessionFile(t, t.TempDir(), token)
	const key = "req-purge-cli-replay-request"

	runRequest := func() core.RequestPurgeResult {
		t.Helper()
		stdout, stderr, code := runCLIEnv(t, env, "purge", "request", objectID,
			"--jurisdiction", "PE", "--legislation", "NATIONAL-TAX", "--category", "invoice",
			"--expected-hash", h, "--reason", "retention period elapsed",
			"--request-id", key, "--db", db)
		if code != 0 {
			t.Fatalf("purge request failed (exit %d): %s", code, stderr)
		}
		var result core.RequestPurgeResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("purge request output not JSON: %v (stdout %q)", err, stdout)
		}
		return result
	}

	first := runRequest()
	if first.IdempotentReplay || !first.Created || first.Request.RequestID == "" {
		t.Fatalf("first request = %+v, want a created pipeline", first)
	}
	afterFirst := cliDoctorDigest(t, db)

	second := runRequest()
	if !second.IdempotentReplay || second.Request.RequestID != first.Request.RequestID {
		t.Fatalf("replay request = %+v, want the stored request %s with idempotentReplay", second, first.Request.RequestID)
	}
	if second.Request.Status != core.PurgeRequestStatusRequested {
		t.Fatalf("replay status = %q, want requested (stored outcome)", second.Request.Status)
	}
	afterSecond := cliDoctorDigest(t, db)
	if afterFirst != afterSecond {
		t.Fatalf("purge request replay duplicated effects: before %s after %s", afterFirst, afterSecond)
	}
}

// TestCLIPurgeSubcommandReplays (AC-L-3, FR-L.4): every other idempotent purge
// subcommand exposed by the parser — approve, reject, cancel, withdraw, execute
// — is invoked twice with the SAME tenant-scoped idempotency key
// (--request-id-key / --execution-id). The second invocation MUST return the
// stored outcome with idempotentReplay=true and leave the doctor digest
// unchanged: no duplicate decision, event, receipt or execution.
func TestCLIPurgeSubcommandReplays(t *testing.T) {
	rows := []struct {
		name      string
		stage     string // "request" or "approved"
		session   string // "accountant" or "records"
		idemFlags []string
		// uuidKey marks the execute row: --execution-id must be a real UUID
		// (the tenant-scoped idempotency key of the execution attempt), unlike
		// --request-id-key which accepts an arbitrary string.
		uuidKey bool
		run     func(t *testing.T, seed cliPurgeReplaySeed, env []string, db, key string) (string, string, int)
		assert  func(t *testing.T, firstRaw, secondRaw string, seed cliPurgeReplaySeed)
	}{
		{
			name: "approve", stage: "request", session: "records",
			idemFlags: []string{"--request-id-key"},
			run: func(t *testing.T, seed cliPurgeReplaySeed, env []string, db, key string) (string, string, int) {
				return runCLIEnv(t, env, "purge", "approve", seed.requestID,
					"--expected-hash", seed.hRequested, "--reason", "verified",
					"--request-id-key", key, "--db", db)
			},
			assert: func(t *testing.T, firstRaw, secondRaw string, seed cliPurgeReplaySeed) {
				var first, second core.ApprovePurgeResult
				mustJSON(t, firstRaw, &first)
				mustJSON(t, secondRaw, &second)
				if first.IdempotentReplay || second.IdempotentReplay != true || second.Approval.ApprovalID != first.Approval.ApprovalID {
					t.Fatalf("approve replay = %+v, want the stored approval %s with idempotentReplay", second, first.Approval.ApprovalID)
				}
			},
		},
		{
			name: "reject", stage: "request", session: "records",
			idemFlags: []string{"--request-id-key"},
			run: func(t *testing.T, seed cliPurgeReplaySeed, env []string, db, key string) (string, string, int) {
				return runCLIEnv(t, env, "purge", "reject", seed.requestID,
					"--reason", "not eligible", "--request-id-key", key, "--db", db)
			},
			assert: func(t *testing.T, firstRaw, secondRaw string, seed cliPurgeReplaySeed) {
				var first, second core.RejectPurgeResult
				mustJSON(t, firstRaw, &first)
				mustJSON(t, secondRaw, &second)
				if first.IdempotentReplay || second.IdempotentReplay != true || second.Request.Status != core.PurgeRequestStatusRejected || second.Request.Status != first.Request.Status {
					t.Fatalf("reject replay = %+v, want the stored rejected outcome with idempotentReplay", second)
				}
			},
		},
		{
			name: "cancel", stage: "request", session: "accountant",
			idemFlags: []string{"--request-id-key"},
			run: func(t *testing.T, seed cliPurgeReplaySeed, env []string, db, key string) (string, string, int) {
				return runCLIEnv(t, env, "purge", "cancel", seed.requestID,
					"--request-id-key", key, "--db", db)
			},
			assert: func(t *testing.T, firstRaw, secondRaw string, seed cliPurgeReplaySeed) {
				var first, second core.CancelPurgeResult
				mustJSON(t, firstRaw, &first)
				mustJSON(t, secondRaw, &second)
				if first.IdempotentReplay || second.IdempotentReplay != true || second.Request.Status != core.PurgeRequestStatusCancelled || second.Request.Status != first.Request.Status {
					t.Fatalf("cancel replay = %+v, want the stored cancelled outcome with idempotentReplay", second)
				}
			},
		},
		{
			name: "withdraw", stage: "approved", session: "records",
			idemFlags: []string{"--request-id-key"},
			run: func(t *testing.T, seed cliPurgeReplaySeed, env []string, db, key string) (string, string, int) {
				return runCLIEnv(t, env, "purge", "withdraw", seed.requestID,
					"--reason", "cleanup", "--request-id-key", key, "--db", db)
			},
			assert: func(t *testing.T, firstRaw, secondRaw string, seed cliPurgeReplaySeed) {
				var first, second core.WithdrawPurgeResult
				mustJSON(t, firstRaw, &first)
				mustJSON(t, secondRaw, &second)
				if first.IdempotentReplay || second.IdempotentReplay != true || second.Request.Status != first.Request.Status {
					t.Fatalf("withdraw replay = %+v, want the stored outcome with idempotentReplay", second)
				}
			},
		},
		{
			name: "execute", stage: "approved", session: "records",
			idemFlags: []string{"--execution-id"}, uuidKey: true,
			run: func(t *testing.T, seed cliPurgeReplaySeed, env []string, db, key string) (string, string, int) {
				return runCLIEnv(t, env, "purge", "execute", seed.requestID,
					"--expected-hash", seed.hApproved, "--reason", "execution batch approved",
					"--execution-id", key, "--db", db)
			},
			assert: func(t *testing.T, firstRaw, secondRaw string, seed cliPurgeReplaySeed) {
				var first, second core.ExecutePurgeResult
				mustJSON(t, firstRaw, &first)
				mustJSON(t, secondRaw, &second)
				if first.IdempotentReplay || second.IdempotentReplay != true || second.Execution.ExecutionID != first.Execution.ExecutionID {
					t.Fatalf("execute replay = %+v, want the stored execution %s with idempotentReplay", second, first.Execution.ExecutionID)
				}
				if second.Execution.State != core.PurgeExecutionCompleted || first.Execution.State != core.PurgeExecutionCompleted {
					t.Fatalf("execute states = %q/%q, want completed/completed", first.Execution.State, second.Execution.State)
				}
			},
		},
	}

	for _, row := range rows {
		t.Run(row.name+"/replay", func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "engram.db")
			accountantToken := seedPurgeCLIIdentity(t, db, []auth.AccountingRole{auth.RoleAccountant})
			recordsToken := seedPurgeRecordsCLIIdentity(t, db)
			seed := seedCLIPurgeReplayStage(t, db, accountantToken, recordsToken, row.stage)

			sessionToken := accountantToken
			if row.session == "records" {
				sessionToken = recordsToken
			}
			env := writeSessionFile(t, t.TempDir(), sessionToken)
			key := "req-purge-cli-replay-key"
			if row.uuidKey {
				key = "00000000-0000-4000-8000-0000000000" + fmt.Sprintf("%02d", len(rows))
			}

			stdout, stderr, code := row.run(t, seed, env, db, key)
			if code != 0 {
				t.Fatalf("%s failed (exit %d): %s", row.name, code, stderr)
			}
			afterFirst := cliDoctorDigest(t, db)

			stdout2, stderr2, code2 := row.run(t, seed, env, db, key)
			if code2 != 0 {
				t.Fatalf("%s replay failed (exit %d): %s", row.name, code2, stderr2)
			}
			row.assert(t, stdout, stdout2, seed)
			afterSecond := cliDoctorDigest(t, db)
			if afterFirst != afterSecond {
				t.Fatalf("%s replay duplicated effects: before %s after %s", row.name, afterFirst, afterSecond)
			}
		})
	}
}

// mustJSON decodes a CLI stdout payload as the given result type.
func mustJSON(t *testing.T, raw string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		t.Fatalf("CLI output not JSON: %v\n%s", err, raw)
	}
}

// compliance officer — the SoD-DISTINCT approver/executor) + one expiring
// session directly on db and returns the raw token. The existing
// seedPurgeCLIIdentity is hardcoded to the accountant "ana.garcia", so the
// doctor lifecycle fixture seeds a DISTINCT subject id for the approval and
// execution gates (approver ≠ requester is enforced store-side).
func seedPurgeRecordsCLIIdentity(t *testing.T, db string) string {
	t.Helper()
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	membershipID := "membership-cli-records"
	if err := st.SeedIdentity(store.IdentitySeed{
		TenantID:     cliOrganizationID,
		CompanyID:    cliPurgeRUC,
		CompanyRUC:   cliPurgeRUC,
		CompanyName:  "CLI Demo SAC",
		MembershipID: membershipID,
		SubjectID:    "lucia.ramirez",
		Roles:        []auth.AccountingRole{auth.RoleRecordsComplianceOfficer},
	}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	token := "cli-purge-records-token"
	if err := st.SeedSession(store.SessionSeed{
		ID:                   "session-cli-records",
		TokenHash:            sha256HexCLI(token),
		MembershipID:         membershipID,
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return token
}

// TestCLIDoctorReportsPurgeLifecycle proves the CLI doctor command serializes
// the §13.3 lifecycle surface automatically (additive report fields over the
// EXISTING command — no new CLI operation): after a full request → approve →
// execute pipeline seeded through the store (the same openStore the CLI doctor
// reads), the doctor JSON carries the lifecycle table counts and the
// documented-purge object finding — the receipt-covered completion reconciles
// the missing bytes as an auditable finding, never a corruption failure.
func TestCLIDoctorReportsPurgeLifecycle(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	accountantToken := seedPurgeCLIIdentity(t, db, []auth.AccountingRole{auth.RoleAccountant})
	recordsToken := seedPurgeRecordsCLIIdentity(t, db)

	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	accountant := purgeCLIPrincipal(t, st, accountantToken)
	records := purgeCLIPrincipal(t, st, recordsToken)

	objResult, err := st.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       []byte("cli-doctor-target-bytes-0123456789"),
		ContentType: "application/xml",
		Scope:       cliPurgeScope(),
		Source:      core.Source{System: "cli-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		t.Fatalf("store purge target object: %v", err)
	}
	polResult, err := st.PutRetentionPolicy(ctx, core.PutRetentionPolicyCommand{
		Scope:           cliPurgeScope(),
		Jurisdiction:    "PE",
		Legislation:     "NATIONAL-TAX",
		Authority:       "tenant-records",
		Source:          "deployment decision 2026-08-07",
		Category:        "invoice",
		MinPeriod:       "202401",
		ExpectedVersion: 0,
		Enabled:         true,
		RequestID:       "req-policy-cli-doctor",
	}, records)
	if err != nil {
		t.Fatalf("seed retention policy: %v", err)
	}
	objectID := objResult.Object.ObjectID
	scope := cliPurgeScope()
	hRequest := core.ComputeLifecycleSnapshotHash(core.LifecycleSnapshot{
		ObjectID:       objectID,
		TenantID:       scope.OrganizationID,
		CompanyID:      scope.CompanyID,
		RUC:            scope.RUC,
		Period:         scope.Period,
		LifecycleState: core.PurgeLifecycleStored,
		RetentionState: core.RetentionEligibility("unmanaged"),
		BlockingHolds:  []core.LifecycleHoldRef{},
	})
	reqResult, err := st.RequestPurge(ctx, core.RequestPurgeCommand{
		ObjectID:              objectID,
		Jurisdiction:          "PE",
		Legislation:           "NATIONAL-TAX",
		Category:              "invoice",
		ExpectedLifecycleHash: hRequest,
		Reason:                "retention period elapsed",
		RequestID:             "req-purge-cli-doctor",
	}, accountant)
	if err != nil {
		t.Fatalf("request purge: %v", err)
	}
	request := reqResult.Request
	hRequested := core.ComputeLifecycleSnapshotHash(core.LifecycleSnapshot{
		ObjectID:       objectID,
		TenantID:       scope.OrganizationID,
		CompanyID:      scope.CompanyID,
		RUC:            scope.RUC,
		Period:         scope.Period,
		LifecycleState: core.PurgeLifecycleRequested,
		RetentionState: core.RetentionEligibilityEligible,
		PolicyID:       polResult.Policy.PolicyID,
		PolicyVersion:  polResult.Policy.Version,
		Category:       polResult.Policy.Category,
		BlockingHolds:  []core.LifecycleHoldRef{},
		RequestID:      request.RequestID,
		ApprovalIDs:    []string{},
	})
	appResult, err := st.ApprovePurge(ctx, core.ApprovePurgeCommand{
		RequestID:             request.RequestID,
		ExpectedLifecycleHash: hRequested,
		Reason:                "verified against the reviewed snapshot",
		RequestIDKey:          "req-approve-cli-doctor",
	}, records)
	if err != nil {
		t.Fatalf("approve purge: %v", err)
	}
	hApproved := core.ComputeLifecycleSnapshotHash(core.LifecycleSnapshot{
		ObjectID:       objectID,
		TenantID:       scope.OrganizationID,
		CompanyID:      scope.CompanyID,
		RUC:            scope.RUC,
		Period:         scope.Period,
		LifecycleState: core.PurgeLifecycleApproved,
		RetentionState: core.RetentionEligibilityEligible,
		PolicyID:       polResult.Policy.PolicyID,
		PolicyVersion:  polResult.Policy.Version,
		Category:       polResult.Policy.Category,
		BlockingHolds:  []core.LifecycleHoldRef{},
		RequestID:      request.RequestID,
		ApprovalIDs:    []string{appResult.Approval.ApprovalID},
	})
	if _, err := st.ExecutePurge(ctx, core.ExecutePurgeCommand{
		RequestID:             request.RequestID,
		ExpectedLifecycleHash: hApproved,
		Reason:                "execution batch approved",
		ExecutionID:           "00000000-0000-4000-8000-00000000c001",
	}, records); err != nil {
		t.Fatalf("execute purge: %v", err)
	}

	stdout, stderr, code := runCLI(t, "doctor", "--db", db)
	if code != 0 {
		t.Fatalf("doctor failed (exit %d): %s", code, stderr)
	}
	var report store.DoctorReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("doctor output not JSON: %v\n%s", err, stdout)
	}
	if report.SchemaVersion != 17 || report.PurgeRequests != 1 || report.PurgeApprovals != 1 || report.PurgeExecutions != 1 {
		t.Fatalf("doctor report = %+v, want schemaVersion 17 and lifecycle counts (1,1,1)", report)
	}
	if len(report.PurgeFindings) != 0 {
		t.Fatalf("purgeFindings = %+v, want none (the execution completed)", report.PurgeFindings)
	}
	var documentedPurge bool
	for _, of := range report.ObjectFindings {
		if of.Kind == "documented_purge" && of.ObjectID == objectID {
			documentedPurge = true
		}
	}
	if !documentedPurge {
		t.Fatalf("objectFindings = %+v, want a documented_purge finding for %s", report.ObjectFindings, objectID)
	}
}
