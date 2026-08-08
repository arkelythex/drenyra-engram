// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module freezes the v0.8 batch 2 retention-policy store
// surface (docs/architecture/evidence-lifecycle-v0.8.md §3.1/§6/§9):
//
//   - PutRetentionPolicy writes ONE immutable version per put under the
//     authenticated administration gate (deny-list first, then
//     records_compliance_officer | tenant_records_owner, assurance ≥ standard,
//     tenant match), (tenant, requestId) idempotency, and the expected-version
//     supersession guard — never deletes, never claims a statutory duration;
//   - ResolveRetentionPolicy is SCOPE-FIRST exact (scope +
//     jurisdiction/legislation/category, highest ENABLED version); a different
//     tenant, company or tuple never resolves;
//   - EvaluatePurgeEligibility fails closed: UNKNOWN_RETENTION_STATE without an
//     exact active policy, RETENTION_POLICY_AMBIGUOUS on a corrupted ambiguity,
//     NOT_PURGEABLE for institutional scopes, and the pure eligible/not_due
//     dimension against the deployment-declared min_period floor otherwise;
//   - the immutable triggers reject UPDATE/DELETE; a policy put emits NO
//     receipt (a policy put is not an object-chain act — the retention_bound
//     receipt for a newly bound policy lands with object binding).
package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// tenantPolicyScope is the exact tenant-level policy scope used by this suite.
func tenantPolicyScope() core.Scope {
	return core.Scope{Kind: core.ScopeKindCompany, OrganizationID: testOrgID}
}

// companyPolicyScope is the exact company-level policy scope used by this suite.
func companyPolicyScope() core.Scope {
	return core.Scope{Kind: core.ScopeKindCompany, OrganizationID: testOrgID, CompanyID: "acme", RUC: testRucA, Period: testPeriod}
}

// putPolicy runs one policy put with the records-compliance fixture principal.
func putPolicy(t *testing.T, s *SQLiteStore, mutate func(*core.PutRetentionPolicyCommand), principal auth.VerifiedApprovalPrincipal) (core.PutRetentionPolicyResult, error) {
	t.Helper()
	cmd := core.PutRetentionPolicyCommand{
		Scope:           tenantPolicyScope(),
		Jurisdiction:    "PE",
		Legislation:     "NATIONAL-TAX",
		Authority:       "tenant-records",
		Source:          "deployment decision 2026-08-07",
		Category:        "invoice",
		MinPeriod:       "202401",
		ExpectedVersion: 0,
		Enabled:         true,
		RequestID:       "req-policy-1",
	}
	if mutate != nil {
		mutate(&cmd)
	}
	return s.PutRetentionPolicy(context.Background(), cmd, principal)
}

// recordsPrincipal is a records_compliance_officer in tenant-1 with standard
// assurance (the §8.1 retention-policy owning role).
func recordsPrincipal(t *testing.T) auth.VerifiedApprovalPrincipal {
	return mustPrincipal(t, fixtureSessionStore(testOrgID, "acme", []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard))
}

// subjectPrincipal builds a verified principal with an EXPLICIT subject id (the
// fixture default is always subject-1; the idempotency actor binding must be
// able to differ).
func subjectPrincipal(t *testing.T, subjectID string, roles []auth.AccountingRole, assurance auth.AssuranceLevel) auth.VerifiedApprovalPrincipal {
	t.Helper()
	return mustPrincipal(t, &fixedSessionStore{
		session: auth.StoredSession{
			ID:                   "session-" + subjectID,
			MembershipID:         "membership-" + subjectID,
			AuthenticationMethod: auth.AuthMethodSession,
			AssuranceLevel:       assurance,
			AuthenticatedAt:      "2026-08-05T12:00:00Z",
			ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
		membership: auth.MembershipRecord{
			ID:            "membership-" + subjectID,
			SubjectID:     subjectID,
			TenantID:      testOrgID,
			CompanyID:     "acme",
			Status:        "active",
			Roles:         roles,
			CompanyActive: true,
		},
	})
}

func TestPutRetentionPolicyHappyPath(t *testing.T) {
	s := newTestStore(t)

	result, err := putPolicy(t, s, nil, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if !result.Created || result.IdempotentReplay {
		t.Fatalf("result = %+v, want a created (non-replay) put", result)
	}
	p := result.Policy
	if p.Version != 1 || p.PolicyID == "" || p.SupersedesPolicyID != "" {
		t.Fatalf("policy = %+v, want version 1 with no superseded row", p)
	}
	if p.CreatedBy != "subject-1" || p.CreatedAt == "" {
		t.Fatalf("policy provenance = (%q, %q), want the principal + a timestamp", p.CreatedBy, p.CreatedAt)
	}
	if p.TenantID != testOrgID || p.Jurisdiction != "PE" || p.MinPeriod != "202401" {
		t.Fatalf("policy evidence mismatch: %+v", p)
	}
	// Canonical defaults materialized.
	if len(p.DualApproverRoles) != 2 || p.DualApproverRoles[0] != "controller" {
		t.Fatalf("dualApproverRoles = %v, want the frozen default", p.DualApproverRoles)
	}
	if len(p.BlockingHoldKinds) != 4 {
		t.Fatalf("blockingHoldKinds = %v, want the frozen default", p.BlockingHoldKinds)
	}

	// The row is genuinely persisted and resolves.
	resolved, matched, err := s.ResolveRetentionPolicy(context.Background(), tenantPolicyScope(), "PE", "NATIONAL-TAX", "invoice")
	if err != nil || !matched || resolved.PolicyID != p.PolicyID {
		t.Fatalf("resolve = (%+v, %v, %v), want the put policy", resolved, matched, err)
	}
}

func TestPutRetentionPolicySupersessionGuard(t *testing.T) {
	s := newTestStore(t)
	principal := recordsPrincipal(t)

	// v1 lands with expectedVersion=0.
	v1, err := putPolicy(t, s, nil, principal)
	if err != nil {
		t.Fatalf("put v1: %v", err)
	}

	// A second put asserting "no current policy" is LIFECYCLE_VERSION_MISMATCH.
	if _, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.RequestID = "req-policy-stale"
		cmd.ExpectedVersion = 0
	}, principal); err == nil || auth.Code(err) != auth.CodeLifecycleVersionMismatch {
		t.Fatalf("stale expectedVersion = %v, want LIFECYCLE_VERSION_MISMATCH", err)
	}

	// The correct expectedVersion supersedes: v2 chains on v1.
	v2, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.RequestID = "req-policy-2"
		cmd.ExpectedVersion = 1
		cmd.MinPeriod = "202501"
		cmd.Source = "supersession decision 2026-09-01"
	}, principal)
	if err != nil {
		t.Fatalf("put v2: %v", err)
	}
	if v2.Policy.Version != 2 || v2.Policy.SupersedesPolicyID != v1.Policy.PolicyID {
		t.Fatalf("v2 = %+v, want version 2 superseding %s", v2.Policy, v1.Policy.PolicyID)
	}

	// Resolution returns the HIGHEST version (v2).
	resolved, matched, err := s.ResolveRetentionPolicy(context.Background(), tenantPolicyScope(), "PE", "NATIONAL-TAX", "invoice")
	if err != nil || !matched || resolved.Version != 2 {
		t.Fatalf("resolve = (%+v, %v, %v), want v2", resolved, matched, err)
	}

	// An outdated expectedVersion (1 while the chain is at 2) fails closed.
	if _, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.RequestID = "req-policy-3"
		cmd.ExpectedVersion = 1
	}, principal); err == nil || auth.Code(err) != auth.CodeLifecycleVersionMismatch {
		t.Fatalf("stale v3 put = %v, want LIFECYCLE_VERSION_MISMATCH", err)
	}
}

func TestPutRetentionPolicyIdempotency(t *testing.T) {
	s := newTestStore(t)
	principal := recordsPrincipal(t)

	first, err := putPolicy(t, s, nil, principal)
	if err != nil {
		t.Fatalf("first put: %v", err)
	}

	// Replay: same (tenant, requestId) + same command + same principal returns
	// the stored outcome with NO new row.
	replay, err := putPolicy(t, s, nil, principal)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.IdempotentReplay || replay.Created || replay.Policy.PolicyID != first.Policy.PolicyID {
		t.Fatalf("replay = %+v, want the stored outcome with idempotentReplay", replay)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM retention_policies`).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("retention_policies rows = %d, want exactly 1 (replay wrote nothing)", n)
	}

	// Same requestId + DIFFERENT command → IDEMPOTENCY_CONFLICT.
	if _, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.MinPeriod = "202402"
	}, principal); err == nil || auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Fatalf("reused requestId with different command = %v, want IDEMPOTENCY_CONFLICT", err)
	}

	// Same requestId + DIFFERENT principal (a distinct subject id) →
	// IDEMPOTENCY_CONFLICT.
	other := subjectPrincipal(t, "subject-2", []auth.AccountingRole{auth.RoleTenantRecordsOwner}, auth.AssuranceStandard)
	if _, err := putPolicy(t, s, nil, other); err == nil || auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Fatalf("reused requestId with different principal = %v, want IDEMPOTENCY_CONFLICT", err)
	}

	// The same command under a FRESH requestId is a new act (a new version).
	v2, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.RequestID = "req-policy-fresh"
		cmd.ExpectedVersion = 1
	}, principal)
	if err != nil {
		t.Fatalf("fresh requestId put: %v", err)
	}
	if v2.Policy.Version != 2 || v2.IdempotentReplay {
		t.Fatalf("fresh put = %+v, want a new version 2", v2)
	}
}

func TestPutRetentionPolicyAuthorizationMatrix(t *testing.T) {
	s := newTestStore(t)

	cases := []struct {
		name      string
		roles     []auth.AccountingRole
		assurance auth.AssuranceLevel
		wantCode  string
	}{
		{"records_compliance_officer allowed", []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard, ""},
		{"tenant_records_owner allowed", []auth.AccountingRole{auth.RoleTenantRecordsOwner}, auth.AssuranceStandard, ""},
		{"controller denied (not a records role)", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard, auth.CodeRoleNotAuthorized},
		{"accountant denied (not a records role)", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard, auth.CodeRoleNotAuthorized},
		{"operational_accountant deny-listed", []auth.AccountingRole{auth.RoleOperationalAccountant}, auth.AssuranceStandard, auth.CodeRoleDenied},
		{"admin deny-listed", []auth.AccountingRole{auth.RoleRecordsComplianceOfficer, auth.RoleAccountant, "deployment_admin"}, auth.AssuranceStandard, auth.CodeRoleDenied},
		{"low assurance denied", []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceLow, auth.CodeAssuranceTooLow},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			principal := mustPrincipal(t, fixtureSessionStore(testOrgID, "acme", tt.roles, tt.assurance))
			// Each case uses its OWN (category, requestId) chain so the
			// expectedVersion=0 assertion stays isolated.
			_, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
				cmd.Category = "authz-" + strings.ReplaceAll(tt.name, " ", "-")
				cmd.RequestID = "req-auth-" + strings.ReplaceAll(tt.name, " ", "-")
			}, principal)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("expected allowed, got %v", err)
				}
				return
			}
			if err == nil || auth.Code(err) != tt.wantCode {
				t.Fatalf("error = %v, want code %s", err, tt.wantCode)
			}
		})
	}

	// Cross-tenant principal: a tenant-2 records officer cannot write a
	// tenant-1 policy (TENANT_SCOPE_MISMATCH).
	crossTenant := mustPrincipal(t, fixtureSessionStore("org-002", "acme", []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard))
	if _, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.RequestID = "req-cross-tenant"
	}, crossTenant); err == nil || auth.Code(err) != auth.CodeTenantScopeMismatch {
		t.Fatalf("cross-tenant put = %v, want TENANT_SCOPE_MISMATCH", err)
	}
}

func TestPutRetentionPolicyEvidenceRequired(t *testing.T) {
	s := newTestStore(t)
	principal := recordsPrincipal(t)

	for name, mutate := range map[string]func(*core.PutRetentionPolicyCommand){
		"missing jurisdiction":   func(cmd *core.PutRetentionPolicyCommand) { cmd.Jurisdiction = "" },
		"malformed jurisdiction": func(cmd *core.PutRetentionPolicyCommand) { cmd.Jurisdiction = "pe" },
		"missing legislation":    func(cmd *core.PutRetentionPolicyCommand) { cmd.Legislation = "" },
		"missing authority":      func(cmd *core.PutRetentionPolicyCommand) { cmd.Authority = "" },
		"missing source":         func(cmd *core.PutRetentionPolicyCommand) { cmd.Source = "" },
		"missing category":       func(cmd *core.PutRetentionPolicyCommand) { cmd.Category = "" },
		"malformed minPeriod":    func(cmd *core.PutRetentionPolicyCommand) { cmd.MinPeriod = "20241" },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
				mutate(cmd)
				cmd.RequestID = "req-evidence-" + strings.ReplaceAll(name, " ", "-")
			}, principal)
			if err == nil {
				t.Fatalf("expected a fail-closed validation error, got nil")
			}
			if name == "missing jurisdiction" || name == "malformed jurisdiction" {
				if !strings.Contains(err.Error(), "INVALID_RETENTION_JURISDICTION") {
					t.Fatalf("error = %v, want INVALID_RETENTION_JURISDICTION", err)
				}
			}
			if name == "missing legislation" || name == "missing authority" || name == "missing source" {
				if auth.Code(err) != auth.CodePolicyEvidenceRequired {
					t.Fatalf("error = %v, want POLICY_EVIDENCE_REQUIRED", err)
				}
			}
		})
	}
}

func TestResolveRetentionPolicyScopeFirst(t *testing.T) {
	s := newTestStore(t)
	principal := recordsPrincipal(t)

	// Company-level policy for acme.
	if _, err := s.PutRetentionPolicy(context.Background(), core.PutRetentionPolicyCommand{
		Scope: companyPolicyScope(), Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
		Authority: "tenant-records", Source: "deployment decision", Category: "invoice",
		MinPeriod: "202401", ExpectedVersion: 0, Enabled: true, RequestID: "req-company-policy",
	}, principal); err != nil {
		t.Fatalf("put company policy: %v", err)
	}

	// Exact company scope resolves; the tenant-level scope does NOT see a
	// company-level policy (exact scope is part of the key — never partial).
	resolved, matched, err := s.ResolveRetentionPolicy(context.Background(), companyPolicyScope(), "PE", "NATIONAL-TAX", "invoice")
	if err != nil || !matched || resolved.CompanyID != "acme" {
		t.Fatalf("company resolve = (%+v, %v, %v), want the acme policy", resolved, matched, err)
	}
	if _, matched, err := s.ResolveRetentionPolicy(context.Background(), tenantPolicyScope(), "PE", "NATIONAL-TAX", "invoice"); err != nil || matched {
		t.Fatalf("tenant-level resolve of a company policy = (%v, %v), want no match", matched, err)
	}

	// CROSS-TENANT isolation: a tenant-2 scope never resolves tenant-1 policies.
	otherTenant := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-002", CompanyID: "acme", RUC: testRucA, Period: testPeriod}
	if _, matched, err := s.ResolveRetentionPolicy(context.Background(), otherTenant, "PE", "NATIONAL-TAX", "invoice"); err != nil || matched {
		t.Fatalf("cross-tenant resolve = (%v, %v), want no match (scope-first)", matched, err)
	}

	// A different jurisdiction/legislation/category never resolves.
	for _, tuple := range [][3]string{{"CL", "NATIONAL-TAX", "invoice"}, {"PE", "OTHER-REGIME", "invoice"}, {"PE", "NATIONAL-TAX", "cdr"}} {
		if _, matched, err := s.ResolveRetentionPolicy(context.Background(), companyPolicyScope(), tuple[0], tuple[1], tuple[2]); err != nil || matched {
			t.Fatalf("tuple %v must not resolve: (%v, %v)", tuple, matched, err)
		}
	}

	// Institutional scope is NOT_PURGEABLE.
	inst := core.Scope{Kind: core.ScopeKindInstitutional}
	if _, _, err := s.ResolveRetentionPolicy(context.Background(), inst, "PE", "NATIONAL-TAX", "invoice"); err == nil || auth.Code(err) != auth.CodeNotPurgeable {
		t.Fatalf("institutional resolve = %v, want NOT_PURGEABLE", err)
	}
}

func TestEvaluatePurgeEligibilityFailClosed(t *testing.T) {
	s := newTestStore(t)
	principal := recordsPrincipal(t)

	// No policy at all → UNKNOWN_RETENTION_STATE (the engine never guesses).
	_, err := s.EvaluatePurgeEligibility(context.Background(), core.EvaluatePurgeEligibilityInput{
		Scope: tenantPolicyScope(), Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
		Category: "invoice", ObjectPeriod: "202401",
	})
	if err == nil || auth.Code(err) != auth.CodeUnknownRetentionState {
		t.Fatalf("no-policy evaluate = %v, want UNKNOWN_RETENTION_STATE", err)
	}

	// A policy exists but a DIFFERENT jurisdiction → still UNKNOWN (exactness).
	if _, err := putPolicy(t, s, nil, principal); err != nil {
		t.Fatalf("put: %v", err)
	}
	_, err = s.EvaluatePurgeEligibility(context.Background(), core.EvaluatePurgeEligibilityInput{
		Scope: tenantPolicyScope(), Jurisdiction: "CL", Legislation: "NATIONAL-TAX",
		Category: "invoice", ObjectPeriod: "202401",
	})
	if err == nil || auth.Code(err) != auth.CodeUnknownRetentionState {
		t.Fatalf("cross-jurisdiction evaluate = %v, want UNKNOWN_RETENTION_STATE", err)
	}

	// A DISABLED policy never resolves → UNKNOWN.
	if _, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.RequestID = "req-disabled"
		cmd.Enabled = false
		cmd.Category = "cdr"
	}, principal); err != nil {
		t.Fatalf("put disabled: %v", err)
	}
	_, err = s.EvaluatePurgeEligibility(context.Background(), core.EvaluatePurgeEligibilityInput{
		Scope: tenantPolicyScope(), Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
		Category: "cdr", ObjectPeriod: "202401",
	})
	if err == nil || auth.Code(err) != auth.CodeUnknownRetentionState {
		t.Fatalf("disabled-policy evaluate = %v, want UNKNOWN_RETENTION_STATE", err)
	}

	// Institutional objects are NOT_PURGEABLE.
	_, err = s.EvaluatePurgeEligibility(context.Background(), core.EvaluatePurgeEligibilityInput{
		Scope: core.Scope{Kind: core.ScopeKindInstitutional}, Jurisdiction: "PE",
		Legislation: "NATIONAL-TAX", Category: "invoice", ObjectPeriod: "202401",
	})
	if err == nil || auth.Code(err) != auth.CodeNotPurgeable {
		t.Fatalf("institutional evaluate = %v, want NOT_PURGEABLE", err)
	}
}

func TestEvaluatePurgeEligibilityDimension(t *testing.T) {
	s := newTestStore(t)
	principal := recordsPrincipal(t)

	if _, err := putPolicy(t, s, nil, principal); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Object reached the deployment-declared min_period floor → eligible.
	res, err := s.EvaluatePurgeEligibility(context.Background(), core.EvaluatePurgeEligibilityInput{
		Scope: tenantPolicyScope(), Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
		Category: "invoice", ObjectPeriod: "202401",
	})
	if err != nil {
		t.Fatalf("eligible evaluate: %v", err)
	}
	if res.Eligibility != core.RetentionEligibilityEligible {
		t.Fatalf("eligibility = %q, want eligible", res.Eligibility)
	}
	if res.PolicyID == "" || res.MinPeriod != "202401" || res.ModelVersion != core.RetentionPolicyModelVersion {
		t.Fatalf("result evidence = %+v, want the resolved policy evidence", res)
	}

	// Object BEFORE the floor → not_due (the request layer blocks with
	// RETENTION_NOT_DUE; evaluate reports the dimension).
	res, err = s.EvaluatePurgeEligibility(context.Background(), core.EvaluatePurgeEligibilityInput{
		Scope: tenantPolicyScope(), Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
		Category: "invoice", ObjectPeriod: "202312",
	})
	if err != nil {
		t.Fatalf("not_due evaluate: %v", err)
	}
	if res.Eligibility != core.RetentionEligibilityNotDue {
		t.Fatalf("eligibility = %q, want not_due", res.Eligibility)
	}

	// A supersession that raises the floor changes the dimension: v2 with
	// minPeriod 202501 makes a 202401 object not_due again (exact active
	// policy = highest version wins).
	if _, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
		cmd.RequestID = "req-floor-v2"
		cmd.ExpectedVersion = 1
		cmd.MinPeriod = "202501"
		cmd.Source = "floor raised 2026-09-01"
	}, principal); err != nil {
		t.Fatalf("put v2: %v", err)
	}
	res, err = s.EvaluatePurgeEligibility(context.Background(), core.EvaluatePurgeEligibilityInput{
		Scope: tenantPolicyScope(), Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
		Category: "invoice", ObjectPeriod: "202401",
	})
	if err != nil {
		t.Fatalf("v2 evaluate: %v", err)
	}
	if res.Eligibility != core.RetentionEligibilityNotDue || res.PolicyVersion != 2 {
		t.Fatalf("eligibility = %q (policy v%d), want not_due under v2", res.Eligibility, res.PolicyVersion)
	}
}

func TestPutRetentionPolicyEmitsNoReceipt(t *testing.T) {
	// A policy put is NOT an object-chain act: per design §5/§6 the
	// retention_bound receipt for a newly bound policy lands with OBJECT
	// binding (deferred batch); this put must emit NO receipt at all — even
	// with a signer attached.
	s := newTestStore(t)
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-put", core.ReceiptAlgorithm, "base64-public-put", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}
	signer := newParitySigner(s)
	s.SetReceiptSigner(signer)

	if _, err := putPolicy(t, s, nil, recordsPrincipal(t)); err != nil {
		t.Fatalf("put: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM receipts`).Scan(&n); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if n != 0 {
		t.Fatalf("receipts rows = %d, want 0 (policy put emits no receipt; retention_bound lands with object binding)", n)
	}
}
