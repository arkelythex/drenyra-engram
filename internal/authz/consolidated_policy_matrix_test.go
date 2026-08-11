// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test consolidates the four frozen
// authorization policies into ONE inspectable role × action × effect matrix
// (FR-J.1 / AC-J-1, design §J):
//
//   - approval-policy/v0.4.0       — version anchor approval_policy_test.go:113
//     (TestPolicyVersionConstantIsExact);
//   - judgment-policy/v0.4.0       — version anchor judgment_policy_test.go:54
//     (TestJudgmentPolicyVersionConstantIsExact);
//   - reconciliation-policy/v0.5.0 — version anchor reconciliation_policy_test.go:68
//     (TestReconciliationPolicyVersionConstantIsExact);
//   - evidence-lifecycle-policy/v0.8.0 — version anchor
//     evidence_lifecycle_policy_test.go:161 (TestLifecyclePolicyVersionConstantIsExact).
//
// Every row reuses the real public authorization functions and the frozen
// fixture constructors of the four policy suites. Each policy has at least one
// allow, one role denial, one scope denial and one assurance denial; the
// evidence-lifecycle policy adds the dual-approval gate (distinct-principal
// rule) and the segregation-of-duties denials (requester ≠ approver, second
// approver ≠ first approver); the approval policy's SoD clause (v0.9.0 review
// workspace — approval_policy.go SODViolation, design §5/§6.5.5) is pinned in
// the same matrix. The judgment and reconciliation policies define NO SoD
// clause in their frozen Authorize (their check order is tenant → company →
// membership → role → assurance); their SoD is structural at the propose
// boundary (a human source is rejected PROPOSAL_UNAUTHORIZED) and store-side
// for lifecycle acts (pinned by the store no-bypass suite, FR-J.3). A separate
// `check-order` subtest copies the ten named probes of
// TestLifecycleCheckOrderIsFrozen (evidence_lifecycle_policy_test.go:497) and
// asserts the exact existing order rather than inventing a second one.
//
// The matrix SUPPLEMENTS — never replaces — the four policy-specific suites. A
// version bump is a frozen-contract change (NFR-J.2) and MUST fail this test
// until separately approved.
package authz_test

import (
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// policyDecision is the normalized pure-policy outcome the matrix asserts.
type policyDecision struct {
	allowed       bool
	reasonCode    string
	policyVersion string
}

// policyCase is one named row of the consolidated matrix. EC-2: every row name
// carries the (policy / role / action / effect) triple.
type policyCase struct {
	name          string
	policyVersion string
	authorize     func(t *testing.T) policyDecision
	allowed       bool
	reasonCode    string
}

func approveDecision(d authz.Decision) policyDecision {
	return policyDecision{allowed: d.Allowed, reasonCode: d.ReasonCode, policyVersion: d.PolicyVersion}
}

func judgmentDecision(d authz.JudgmentAuthorizationDecision) policyDecision {
	return policyDecision{allowed: d.Allowed, reasonCode: d.ReasonCode, policyVersion: d.PolicyVersion}
}

func reconciliationDecision(d authz.ReconciliationAuthorizationDecision) policyDecision {
	return policyDecision{allowed: d.Allowed, reasonCode: d.ReasonCode, policyVersion: d.PolicyVersion}
}

func lifecycleDecision(d authz.LifecycleDecision) policyDecision {
	return policyDecision{allowed: d.Allowed, reasonCode: d.ReasonCode, policyVersion: d.PolicyVersion}
}

// sodDecision renders the approval policy's SoD clause (SODViolation —
// approval_policy.go, v0.9.0 review workspace) as a denial/allow decision: a
// reviewer whose subject id equals the pending revision's recordedBy is a
// denial (SOD_VIOLATION); a distinct subject passes.
func sodDecision(proposerRecordedBy, reviewerSubjectID string) policyDecision {
	if authz.SODViolation(proposerRecordedBy, reviewerSubjectID) {
		return policyDecision{allowed: false, reasonCode: auth.CodeSODViolation, policyVersion: authz.PolicyVersion}
	}
	return policyDecision{allowed: true, reasonCode: authz.ReasonAuthorized, policyVersion: authz.PolicyVersion}
}

func TestConsolidatedVersionedPolicyMatrix(t *testing.T) {
	// The four exact version constants (FR-J.1). A bump is a frozen-contract
	// change and must fail here.
	t.Run("version-constants", func(t *testing.T) {
		if authz.PolicyVersion != "approval-policy/v0.4.0" {
			t.Errorf("PolicyVersion = %q, want approval-policy/v0.4.0", authz.PolicyVersion)
		}
		if authz.JudgmentPolicyVersion != "judgment-policy/v0.4.0" {
			t.Errorf("JudgmentPolicyVersion = %q, want judgment-policy/v0.4.0", authz.JudgmentPolicyVersion)
		}
		if authz.ReconciliationPolicyVersion != "reconciliation-policy/v0.5.0" {
			t.Errorf("ReconciliationPolicyVersion = %q, want reconciliation-policy/v0.5.0", authz.ReconciliationPolicyVersion)
		}
		if authz.LifecyclePolicyVersion != "evidence-lifecycle-policy/v0.8.0" {
			t.Errorf("LifecyclePolicyVersion = %q, want evidence-lifecycle-policy/v0.8.0", authz.LifecyclePolicyVersion)
		}
	})

	rows := []policyCase{
		// ── approval-policy/v0.4.0 (base ladder, materiality, assurance) ──
		{
			name: "approval-policy/v0.4.0/controller/closing/allow",
			authorize: func(t *testing.T) policyDecision {
				return approveDecision(authz.NewApprovalPolicy().Authorize(controllerPrincipal(t), memory(core.FiscalEffectClosing, nil)))
			},
			allowed: true, reasonCode: authz.ReasonAuthorized,
		},
		{
			name: "approval-policy/v0.4.0/accountant/closing/role-denial",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
				return approveDecision(authz.NewApprovalPolicy().Authorize(p, memory(core.FiscalEffectClosing, nil)))
			},
			allowed: false, reasonCode: auth.CodeRoleNotAuthorized,
		},
		{
			name: "approval-policy/v0.4.0/foreign-tenant/controller/closing/scope-denial",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-2", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
				return approveDecision(authz.NewApprovalPolicy().Authorize(p, memory(core.FiscalEffectClosing, nil)))
			},
			allowed: false, reasonCode: auth.CodeTenantScopeMismatch,
		},
		{
			name: "approval-policy/v0.4.0/controller/closing/assurance-denial",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceLow))
				return approveDecision(authz.NewApprovalPolicy().Authorize(p, memory(core.FiscalEffectClosing, nil)))
			},
			allowed: false, reasonCode: auth.CodeAssuranceTooLow,
		},
		{
			name: "approval-policy/v0.4.0/accountant/material-adjustment/materiality-denial",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
				return approveDecision(authz.NewApprovalPolicy().Authorize(p, memory(core.FiscalEffectAdjustment, level(core.MaterialityMaterial))))
			},
			allowed: false, reasonCode: auth.CodeMaterialityLimitExceeded,
		},
		{
			name: "approval-policy/v0.4.0/reviewer-equals-proposer/sod-denial",
			authorize: func(t *testing.T) policyDecision {
				return sodDecision("subject-1", "subject-1")
			},
			allowed: false, reasonCode: auth.CodeSODViolation,
		},
		{
			name: "approval-policy/v0.4.0/distinct-reviewer/sod-allow",
			authorize: func(t *testing.T) policyDecision {
				return sodDecision("subject-1", "subject-2")
			},
			allowed: true, reasonCode: authz.ReasonAuthorized,
		},

		// ── judgment-policy/v0.4.0 (minimum senior_accountant, ladder) ──
		{
			name: "judgment-policy/v0.4.0/controller/confirm/allow",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
				return judgmentDecision(authz.NewJudgmentPolicy().Authorize(p, judgmentFixture()))
			},
			allowed: true, reasonCode: authz.ReasonAuthorized,
		},
		{
			name: "judgment-policy/v0.4.0/accountant/confirm/role-denial",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
				return judgmentDecision(authz.NewJudgmentPolicy().Authorize(p, judgmentFixture()))
			},
			allowed: false, reasonCode: auth.CodeRoleNotAuthorized,
		},
		{
			name: "judgment-policy/v0.4.0/foreign-tenant/controller/confirm/scope-denial",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-2", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
				return judgmentDecision(authz.NewJudgmentPolicy().Authorize(p, judgmentFixture()))
			},
			allowed: false, reasonCode: auth.CodeTenantScopeMismatch,
		},
		{
			name: "judgment-policy/v0.4.0/controller/confirm/assurance-denial",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceLow))
				return judgmentDecision(authz.NewJudgmentPolicy().Authorize(p, judgmentFixture()))
			},
			allowed: false, reasonCode: auth.CodeAssuranceTooLow,
		},

		// ── reconciliation-policy/v0.5.0 (minimum CONTROLLER — stronger) ──
		{
			name: "reconciliation-policy/v0.5.0/controller/confirm/allow",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
				return reconciliationDecision(authz.NewReconciliationPolicy().Authorize(p, reconciliationFixture()))
			},
			allowed: true, reasonCode: authz.ReasonAuthorized,
		},
		{
			name: "reconciliation-policy/v0.5.0/senior-accountant/confirm/role-denial",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleSeniorAccountant}, auth.AssuranceStandard))
				return reconciliationDecision(authz.NewReconciliationPolicy().Authorize(p, reconciliationFixture()))
			},
			allowed: false, reasonCode: auth.CodeRoleNotAuthorized,
		},
		{
			name: "reconciliation-policy/v0.5.0/foreign-tenant/controller/confirm/scope-denial",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-2", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
				return reconciliationDecision(authz.NewReconciliationPolicy().Authorize(p, reconciliationFixture()))
			},
			allowed: false, reasonCode: auth.CodeTenantScopeMismatch,
		},
		{
			name: "reconciliation-policy/v0.5.0/controller/confirm/assurance-denial",
			authorize: func(t *testing.T) policyDecision {
				p := mustPrincipal(t, fixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceLow))
				return reconciliationDecision(authz.NewReconciliationPolicy().Authorize(p, reconciliationFixture()))
			},
			allowed: false, reasonCode: auth.CodeAssuranceTooLow,
		},

		// ── evidence-lifecycle-policy/v0.8.0 (deny-list, dual approval, SoD) ──
		{
			name: "evidence-lifecycle-policy/v0.8.0/accountant/request/allow",
			authorize: func(t *testing.T) policyDecision {
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(
					lifecycleReq(authz.LifecycleActionRequestPurge, lifecyclePrincipalWithRoles(t, auth.RoleAccountant))))
			},
			allowed: true, reasonCode: authz.ReasonAuthorized,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/records-compliance-officer/approve/allow",
			authorize: func(t *testing.T) policyDecision {
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(
					lifecycleReq(authz.LifecycleActionApprovePurge, lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer))))
			},
			allowed: true, reasonCode: authz.ReasonAuthorized,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/controller/second-approve/distinct-principal/allow",
			authorize: func(t *testing.T) policyDecision {
				second := lifecyclePrincipalWithSubject(t, "subject-2", auth.RoleController)
				first := lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer)
				r := lifecycleReq(authz.LifecycleActionSecondApprove, second)
				r.DualApprovalRequired = true
				r.FirstApprover = &first
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(r))
			},
			allowed: true, reasonCode: authz.ReasonAuthorized,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/records-compliance-officer/request/role-denial",
			authorize: func(t *testing.T) policyDecision {
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(
					lifecycleReq(authz.LifecycleActionRequestPurge, lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer))))
			},
			allowed: false, reasonCode: auth.CodeRoleNotAuthorized,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/accountant/approve/role-denial",
			authorize: func(t *testing.T) policyDecision {
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(
					lifecycleReq(authz.LifecycleActionApprovePurge, lifecyclePrincipalWithRoles(t, auth.RoleAccountant))))
			},
			allowed: false, reasonCode: auth.CodeRoleNotAuthorized,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/cross-tenant/approve/scope-denial",
			authorize: func(t *testing.T) policyDecision {
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(
					lifecycleReq(authz.LifecycleActionApprovePurge, lifecycleCrossTenantPrincipal(t, auth.RoleRecordsComplianceOfficer))))
			},
			allowed: false, reasonCode: auth.CodeTenantScopeMismatch,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/out-of-scope-company/approve/scope-denial",
			authorize: func(t *testing.T) policyDecision {
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(
					lifecycleReq(authz.LifecycleActionApprovePurge, lifecycleOutOfScopePrincipal(t, auth.RoleRecordsComplianceOfficer))))
			},
			allowed: false, reasonCode: auth.CodeCompanyScopeDenied,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/accountant/request/assurance-denial",
			authorize: func(t *testing.T) policyDecision {
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(
					lifecycleReq(authz.LifecycleActionRequestPurge, lifecyclePrincipalWithAssurance(t, auth.AssuranceLow, auth.RoleAccountant))))
			},
			allowed: false, reasonCode: auth.CodeAssuranceTooLow,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/operational-accountant/request/deny-list-denial",
			authorize: func(t *testing.T) policyDecision {
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(
					lifecycleReq(authz.LifecycleActionRequestPurge, lifecyclePrincipalWithRoles(t, auth.RoleOperationalAccountant))))
			},
			allowed: false, reasonCode: auth.CodeRoleDenied,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/deployment-admin/request/deny-list-denial",
			authorize: func(t *testing.T) policyDecision {
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(
					lifecycleReq(authz.LifecycleActionRequestPurge, lifecyclePrincipalWithRoles(t, auth.AccountingRole("deployment_admin")))))
			},
			allowed: false, reasonCode: auth.CodeRoleDenied,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/controller/second-approve/dual-category-disabled/denial",
			authorize: func(t *testing.T) policyDecision {
				second := lifecyclePrincipalWithRoles(t, auth.RoleController)
				r := lifecycleReq(authz.LifecycleActionSecondApprove, second)
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(r))
			},
			allowed: false, reasonCode: auth.CodeDualApprovalRequired,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/approver-equals-requester/sod-denial",
			authorize: func(t *testing.T) policyDecision {
				approver := lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer)
				r := lifecycleReq(authz.LifecycleActionApprovePurge, approver)
				r.Requester = &approver
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(r))
			},
			allowed: false, reasonCode: auth.CodeApproverIsRequester,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/same-principal-second-approval/sod-denial",
			authorize: func(t *testing.T) policyDecision {
				same := lifecyclePrincipalWithRoles(t, auth.RoleRecordsComplianceOfficer, auth.RoleController)
				r := lifecycleReq(authz.LifecycleActionSecondApprove, same)
				r.DualApprovalRequired = true
				r.FirstApprover = &same
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(r))
			},
			allowed: false, reasonCode: auth.CodeSamePrincipalSecondApproval,
		},
		{
			name: "evidence-lifecycle-policy/v0.8.0/controller/place-hold/role-denial",
			authorize: func(t *testing.T) policyDecision {
				return lifecycleDecision(authz.NewEvidenceLifecyclePolicy().Authorize(
					lifecycleReq(authz.LifecycleActionPlaceHold, lifecyclePrincipalWithRoles(t, auth.RoleController))))
			},
			allowed: false, reasonCode: auth.CodeRoleNotAuthorized,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got := row.authorize(t)
			if got.allowed != row.allowed {
				t.Fatalf("allowed = %v, want %v (decision %+v)", got.allowed, row.allowed, got)
			}
			if got.reasonCode != row.reasonCode {
				t.Errorf("ReasonCode = %q, want %q", got.reasonCode, row.reasonCode)
			}
			if row.allowed && got.reasonCode != authz.ReasonAuthorized {
				t.Errorf("allowed ReasonCode = %q, want AUTHORIZED", got.reasonCode)
			}
			if got.policyVersion == "" {
				t.Error("decision must stamp its policy version")
			}
		})
	}

	// check-order: the ten named probes copied from TestLifecycleCheckOrderIsFrozen
	// (evidence_lifecycle_policy_test.go:497) — first frozen reason code wins,
	// denial precedes allow. Positions 2 and 3 reuse the same assertions as the
	// existing policy suite (TestLifecycleCompanyOutOfScopeDenied /
	// TestLifecycleInactiveMembershipDenied) so the frozen order is complete.
	t.Run("check-order", func(t *testing.T) {
		policy := authz.NewEvidenceLifecyclePolicy()
		probes := []struct {
			name     string
			build    func(t *testing.T) authz.LifecycleAuthorizationRequest
			wantCode string
		}{
			{
				name: "1-tenant-over-deny-list",
				build: func(t *testing.T) authz.LifecycleAuthorizationRequest {
					return lifecycleReq(authz.LifecycleActionRequestPurge,
						lifecycleCrossTenantPrincipal(t, auth.RoleOperationalAccountant))
				},
				wantCode: auth.CodeTenantScopeMismatch,
			},
			{
				name: "2-company-scope",
				build: func(t *testing.T) authz.LifecycleAuthorizationRequest {
					return lifecycleReq(authz.LifecycleActionApprovePurge,
						lifecycleOutOfScopePrincipal(t, auth.RoleRecordsComplianceOfficer))
				},
				wantCode: auth.CodeCompanyScopeDenied,
			},
			{
				name: "3-membership-active",
				build: func(t *testing.T) authz.LifecycleAuthorizationRequest {
					store := lifecycleFixtureStore("tenant-1", "acme", []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard)
					store.(*lifecycleFixedSessionStore).membership.ID = ""
					return lifecycleReq(authz.LifecycleActionApprovePurge, lifecycleMustPrincipal(t, store))
				},
				wantCode: auth.CodeMembershipInactive,
			},
			{
				name: "4-actor-kind-over-deny-list",
				build: func(t *testing.T) authz.LifecycleAuthorizationRequest {
					r := lifecycleReq(authz.LifecycleActionApprovePurge,
						lifecyclePrincipalWithRoles(t, auth.RoleOperationalAccountant))
					r.ActorKind = core.ActorKindAgent
					return r
				},
				wantCode: auth.CodeRoleDenied,
			},
			{
				name: "5-deny-list-over-role-assurance",
				build: func(t *testing.T) authz.LifecycleAuthorizationRequest {
					return lifecycleReq(authz.LifecycleActionRequestPurge,
						lifecyclePrincipalWithAssurance(t, auth.AssuranceLow, auth.RoleOperationalAccountant))
				},
				wantCode: auth.CodeRoleDenied,
			},
			{
				name: "6-role-over-assurance",
				build: func(t *testing.T) authz.LifecycleAuthorizationRequest {
					return lifecycleReq(authz.LifecycleActionApprovePurge,
						lifecyclePrincipalWithAssurance(t, auth.AssuranceLow, auth.RoleAccountant))
				},
				wantCode: auth.CodeRoleNotAuthorized,
			},
			{
				name: "7-assurance-over-sod",
				build: func(t *testing.T) authz.LifecycleAuthorizationRequest {
					r := lifecycleReq(authz.LifecycleActionApprovePurge,
						lifecyclePrincipalWithAssurance(t, auth.AssuranceLow, auth.RoleRecordsComplianceOfficer))
					low := r.Principal
					r.Requester = &low
					return r
				},
				wantCode: auth.CodeAssuranceTooLow,
			},
			{
				name: "8-sod-over-dual-distinct",
				build: func(t *testing.T) authz.LifecycleAuthorizationRequest {
					second := lifecyclePrincipalWithRoles(t, auth.RoleController)
					r := lifecycleReq(authz.LifecycleActionSecondApprove, second)
					r.DualApprovalRequired = true
					requester := lifecyclePrincipalWithRoles(t, auth.RoleAccountant)
					r.Requester = &requester
					first := lifecyclePrincipalWithSubject(t, "subject-2", auth.RoleRecordsComplianceOfficer)
					r.FirstApprover = &first
					return r
				},
				wantCode: auth.CodeApproverIsRequester,
			},
			{
				name: "9-dual-over-distinct",
				build: func(t *testing.T) authz.LifecycleAuthorizationRequest {
					second := lifecyclePrincipalWithRoles(t, auth.RoleController)
					r := lifecycleReq(authz.LifecycleActionSecondApprove, second)
					r2 := requester2(t)
					r.Requester = &r2
					r.FirstApprover = &second
					return r
				},
				wantCode: auth.CodeDualApprovalRequired,
			},
			{
				name: "10-distinct-principal",
				build: func(t *testing.T) authz.LifecycleAuthorizationRequest {
					second := lifecyclePrincipalWithRoles(t, auth.RoleController)
					r := lifecycleReq(authz.LifecycleActionSecondApprove, second)
					r2 := requester2(t)
					r.Requester = &r2
					r.DualApprovalRequired = true
					r.FirstApprover = &second
					return r
				},
				wantCode: auth.CodeSamePrincipalSecondApproval,
			},
		}
		for _, probe := range probes {
			t.Run(probe.name, func(t *testing.T) {
				d := policy.Authorize(probe.build(t))
				if d.Allowed {
					t.Fatalf("probe %s must be denied, got %+v", probe.name, d)
				}
				if d.ReasonCode != probe.wantCode {
					t.Errorf("probe %s: ReasonCode = %q, want %q (frozen check order)", probe.name, d.ReasonCode, probe.wantCode)
				}
				if d.PolicyVersion != authz.LifecyclePolicyVersion {
					t.Errorf("probe %s: PolicyVersion = %q, want %q", probe.name, d.PolicyVersion, authz.LifecyclePolicyVersion)
				}
			})
		}
	})
}
