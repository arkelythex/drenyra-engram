// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test is the store-half behavioral proof
// of negative override conformance (FR-J.3 / AC-J-3, design §J): a policy
// DENIED operation cannot be bypassed through any call path. Each table row
// invokes the SAME denied scenario at three boundaries and requires the SAME
// frozen auth reason code with zero persisted state change:
//
//   - policy boundary — the pure frozen policy (authz) decides first;
//   - server/service boundary — the exact SQLiteStore method the HTTP/MCP/CLI
//     adapters delegate to (the server layer validates syntax and delegates; it
//     performs NO authorization of its own — the authorization gate lives in the
//     store transaction, so the adapter entry point IS the service boundary);
//   - store boundary — the same method's transactional depth: the denial commits
//     NOTHING (logical doctor-count digest and operation-specific rows unchanged
//     before/after, mirroring the SoD anchors purge_sod_test.go:74,
//     review_store_test.go:555, review_test.go:229, purge_http_test.go:391).
//
// Rows include an administrative principal (deny-listed *admin token — NFR-J.1)
// and same-principal SoD cases (requester-cannot-approve, first-approver-cannot-
// second-approve, reviewer-cannot-decide-own-proposal). If any layer silently
// accepts a denied operation or changes state, the row stays RED and the slice
// stops — never a weakened assertion.
package store

import (
	"context"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// noBypassFixture carries the seeded identifiers a row's boundaries share.
type noBypassFixture struct {
	objectID     string
	requestID    string
	expectedHash string
	memoryID     string
	envelopeHash string
}

// deniedBoundaryCase is one denied scenario asserted at policy, service and
// store boundaries. policy returns the frozen reason code of the pure policy;
// service returns the error of the store method the adapters call.
type deniedBoundaryCase struct {
	name                string
	wantCode            string
	seed                func(t *testing.T, s *SQLiteStore) noBypassFixture
	policy              func(t *testing.T, f noBypassFixture) string
	service             func(t *testing.T, s *SQLiteStore, f noBypassFixture) error
	assertNoStateChange func(t *testing.T, s *SQLiteStore, f noBypassFixture)
}

// storeDigest is a deterministic logical digest over the doctor counts (state,
// transitions, events, purge lifecycle, holds, idempotency rows) — never raw
// SQLite bytes.
func storeDigest(t *testing.T, s *SQLiteStore) string {
	t.Helper()
	report, err := s.Doctor(context.Background(), DoctorOptions{Mode: ModeRoutine})
	if err != nil {
		t.Fatalf("doctor digest: %v", err)
	}
	return "obs=" + itoa(report.Observations) + ";trans=" + itoa(report.Transitions) +
		";pending=" + itoa(report.PendingApprovals) + ";events=" + itoa(report.LifecycleEvents) +
		";objects=" + itoa(report.EvidenceObjects) + ";purgeq=" + itoa(report.PurgeRequests) +
		";approvals=" + itoa(report.PurgeApprovals) + ";holds=" + itoa(report.Holds) +
		";purgeidem=" + itoa(report.PurgeIdempotencyKeys) + ";holdidem=" + itoa(report.HoldIdempotencyKeys)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// TestDeniedOperationNotBypassableViaAdapterOrStore (AC-J-3 / FR-J.3): table
// rows invoke the same denied scenario at policy, server/service and store
// boundaries; each layer returns the same frozen auth reason code and store
// snapshots remain unchanged.
func TestDeniedOperationNotBypassableViaAdapterOrStore(t *testing.T) {
	ctx := context.Background()

	cases := []deniedBoundaryCase{
		{
			// NFR-J.1: an administrative (deny-listed *admin) principal cannot
			// request a purge at ANY boundary — the deny-list precedes every allow.
			name:     "purge-request/deny-listed-admin-principal",
			wantCode: auth.CodeRoleDenied,
			seed: func(t *testing.T, s *SQLiteStore) noBypassFixture {
				scope := testScope(testRucA)
				objResult, err := s.StoreObject(ctx, objectInputForTest(t, []byte("no-bypass-admin-bytes")))
				if err != nil {
					t.Fatalf("store purge target object: %v", err)
				}
				polResult, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
					cmd.Scope = companyPolicyScope()
					cmd.MinPeriod = "202401"
					cmd.RequestID = "req-policy-nobypass-admin"
				}, recordsPrincipal(t))
				if err != nil {
					t.Fatalf("put retention policy: %v", err)
				}
				_, currentHash, err := currentPurgeSnapshotOn(ctx, s.db, objResult.Object.ObjectID, scope, polResult.Policy.BlockingHoldKinds)
				if err != nil {
					t.Fatalf("read pre-request lifecycle snapshot: %v", err)
				}
				return noBypassFixture{objectID: objResult.Object.ObjectID, expectedHash: currentHash}
			},
			policy: func(t *testing.T, f noBypassFixture) string {
				admin := subjectPrincipal(t, "subject-admin",
					[]auth.AccountingRole{auth.AccountingRole("deployment_admin")}, auth.AssuranceStandard)
				d := authz.NewEvidenceLifecyclePolicy().Authorize(authz.LifecycleAuthorizationRequest{
					Action: authz.LifecycleActionRequestPurge, Principal: admin,
					ActorKind: core.ActorKindHuman, TenantID: testOrgID, CompanyID: "acme", Category: "invoice",
				})
				return d.ReasonCode
			},
			service: func(t *testing.T, s *SQLiteStore, f noBypassFixture) error {
				admin := subjectPrincipal(t, "subject-admin",
					[]auth.AccountingRole{auth.AccountingRole("deployment_admin")}, auth.AssuranceStandard)
				_, err := s.RequestPurge(ctx, core.RequestPurgeCommand{
					ObjectID: f.objectID, Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
					Category: "invoice", ExpectedLifecycleHash: f.expectedHash,
					Reason: "must be denied: deny-listed admin", RequestID: "req-nobypass-admin",
				}, admin)
				return err
			},
			assertNoStateChange: func(t *testing.T, s *SQLiteStore, f noBypassFixture) {
				var requests int
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_purge_requests`).Scan(&requests); err != nil {
					t.Fatalf("count purge requests: %v", err)
				}
				if requests != 0 {
					t.Fatalf("denied admin request created %d purge request rows, want 0", requests)
				}
			},
		},
		{
			// Same-principal SoD: the requester approving its OWN request is denied
			// with APPROVER_IS_REQUESTER at every boundary (stored requester binding).
			name:     "purge-approve/requester-equals-approver",
			wantCode: auth.CodeApproverIsRequester,
			seed: func(t *testing.T, s *SQLiteStore) noBypassFixture {
				s.SetReceiptSigner(newParitySigner(s))
				scope := testScope(testRucA)
				objResult, err := s.StoreObject(ctx, objectInputForTest(t, []byte("no-bypass-requester-bytes")))
				if err != nil {
					t.Fatalf("store purge target object: %v", err)
				}
				polResult, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
					cmd.Scope = companyPolicyScope()
					cmd.MinPeriod = "202401"
					cmd.RequestID = "req-policy-nobypass-requester"
				}, recordsPrincipal(t))
				if err != nil {
					t.Fatalf("put retention policy: %v", err)
				}
				policy := polResult.Policy
				_, currentHash, err := currentPurgeSnapshotOn(ctx, s.db, objResult.Object.ObjectID, scope, policy.BlockingHoldKinds)
				if err != nil {
					t.Fatalf("read pre-request lifecycle snapshot: %v", err)
				}
				requester := subjectPrincipal(t, "subject-nobypass-requester",
					[]auth.AccountingRole{auth.RoleAccountant, auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard)
				reqResult, err := s.RequestPurge(ctx, core.RequestPurgeCommand{
					ObjectID: objResult.Object.ObjectID, Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
					Category: "invoice", ExpectedLifecycleHash: currentHash,
					Reason: "retention period elapsed", RequestID: "req-nobypass-requester",
				}, requester)
				if err != nil {
					t.Fatalf("request purge: %v", err)
				}
				request := reqResult.Request
				h2 := core.ComputeLifecycleSnapshotHash(assembleSnapshot(objResult.Object.ObjectID, scope,
					core.PurgeLifecycleRequested, core.RetentionEligibilityEligible, policy.PolicyID,
					policy.Category, policy.Version, []core.LifecycleHoldRef{}, request.RequestID, []string{}))
				return noBypassFixture{objectID: objResult.Object.ObjectID, requestID: request.RequestID, expectedHash: h2}
			},
			policy: func(t *testing.T, f noBypassFixture) string {
				requester := subjectPrincipal(t, "subject-nobypass-requester",
					[]auth.AccountingRole{auth.RoleAccountant, auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard)
				d := authz.NewEvidenceLifecyclePolicy().Authorize(authz.LifecycleAuthorizationRequest{
					Action: authz.LifecycleActionApprovePurge, Principal: requester,
					ActorKind: core.ActorKindHuman, TenantID: testOrgID, CompanyID: "acme",
					Category: "invoice", Requester: &requester,
				})
				return d.ReasonCode
			},
			service: func(t *testing.T, s *SQLiteStore, f noBypassFixture) error {
				requester := subjectPrincipal(t, "subject-nobypass-requester",
					[]auth.AccountingRole{auth.RoleAccountant, auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard)
				_, err := s.ApprovePurge(ctx, core.ApprovePurgeCommand{
					RequestID: f.requestID, ExpectedLifecycleHash: f.expectedHash,
					Reason:       "must be denied: the requester approves its own purge",
					RequestIDKey: "req-approve-nobypass-requester",
				}, requester)
				return err
			},
			assertNoStateChange: func(t *testing.T, s *SQLiteStore, f noBypassFixture) {
				assertApprovalSoDDenied(t, s, f.objectID, f.requestID, 0, 0, 0, core.PurgeRequestStatusRequested)
			},
		},
		{
			// Same-principal SoD: the FIRST approver cannot supply the required
			// dual SECOND approval (stored first-approval ledger).
			name:     "purge-approve/same-principal-second-approval",
			wantCode: auth.CodeSamePrincipalSecondApproval,
			seed: func(t *testing.T, s *SQLiteStore) noBypassFixture {
				s.SetReceiptSigner(newParitySigner(s))
				scope := testScope(testRucA)
				objResult, err := s.StoreObject(ctx, objectInputForTest(t, []byte("no-bypass-dual-bytes")))
				if err != nil {
					t.Fatalf("store purge target object: %v", err)
				}
				polResult, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
					cmd.Scope = companyPolicyScope()
					cmd.MinPeriod = "202401"
					cmd.DualApprovalRequired = true
					cmd.RequestID = "req-policy-nobypass-dual"
				}, recordsPrincipal(t))
				if err != nil {
					t.Fatalf("put dual-approval retention policy: %v", err)
				}
				policy := polResult.Policy
				_, currentHash, err := currentPurgeSnapshotOn(ctx, s.db, objResult.Object.ObjectID, scope, policy.BlockingHoldKinds)
				if err != nil {
					t.Fatalf("read pre-request lifecycle snapshot: %v", err)
				}
				requester := subjectPrincipal(t, "subject-nobypass-dual-requester",
					[]auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard)
				reqResult, err := s.RequestPurge(ctx, core.RequestPurgeCommand{
					ObjectID: objResult.Object.ObjectID, Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
					Category: "invoice", ExpectedLifecycleHash: currentHash,
					Reason: "retention period elapsed", RequestID: "req-nobypass-dual",
				}, requester)
				if err != nil {
					t.Fatalf("request purge: %v", err)
				}
				request := reqResult.Request
				h2 := core.ComputeLifecycleSnapshotHash(assembleSnapshot(objResult.Object.ObjectID, scope,
					core.PurgeLifecycleRequested, core.RetentionEligibilityEligible, policy.PolicyID,
					policy.Category, policy.Version, []core.LifecycleHoldRef{}, request.RequestID, []string{}))
				first := subjectPrincipal(t, "subject-nobypass-first-approver",
					[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard)
				firstResult, err := s.ApprovePurge(ctx, core.ApprovePurgeCommand{
					RequestID: request.RequestID, ExpectedLifecycleHash: h2,
					Reason: "first approval of the dual pipeline", RequestIDKey: "req-approve-nobypass-first",
				}, first)
				if err != nil {
					t.Fatalf("first approval: %v", err)
				}
				return noBypassFixture{objectID: objResult.Object.ObjectID, requestID: request.RequestID, expectedHash: firstResult.Approval.ResultingHash}
			},
			policy: func(t *testing.T, f noBypassFixture) string {
				same := subjectPrincipal(t, "subject-nobypass-first-approver",
					[]auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard)
				d := authz.NewEvidenceLifecyclePolicy().Authorize(authz.LifecycleAuthorizationRequest{
					Action: authz.LifecycleActionSecondApprove, Principal: same,
					ActorKind: core.ActorKindHuman, TenantID: testOrgID, CompanyID: "acme",
					Category: "invoice", DualApprovalRequired: true, FirstApprover: &same,
				})
				return d.ReasonCode
			},
			service: func(t *testing.T, s *SQLiteStore, f noBypassFixture) error {
				same := subjectPrincipal(t, "subject-nobypass-first-approver",
					[]auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard)
				_, err := s.ApprovePurge(ctx, core.ApprovePurgeCommand{
					RequestID: f.requestID, ExpectedLifecycleHash: f.expectedHash,
					Reason:       "must be denied: the first approver supplies the second approval",
					RequestIDKey: "req-approve-nobypass-second",
				}, same)
				return err
			},
			assertNoStateChange: func(t *testing.T, s *SQLiteStore, f noBypassFixture) {
				assertApprovalSoDDenied(t, s, f.objectID, f.requestID, 1, 1, 1, core.PurgeRequestStatusRequested)
			},
		},
		{
			// Non-admin role matrix: a controller is a second-approver only and can
			// NEVER be the default approver — ROLE_NOT_AUTHORIZED at every boundary.
			name:     "purge-approve/controller-default-approver-role-denial",
			wantCode: auth.CodeRoleNotAuthorized,
			seed: func(t *testing.T, s *SQLiteStore) noBypassFixture {
				s.SetReceiptSigner(newParitySigner(s))
				scope := testScope(testRucA)
				objResult, err := s.StoreObject(ctx, objectInputForTest(t, []byte("no-bypass-role-bytes")))
				if err != nil {
					t.Fatalf("store purge target object: %v", err)
				}
				polResult, err := putPolicy(t, s, func(cmd *core.PutRetentionPolicyCommand) {
					cmd.Scope = companyPolicyScope()
					cmd.MinPeriod = "202401"
					cmd.RequestID = "req-policy-nobypass-role"
				}, recordsPrincipal(t))
				if err != nil {
					t.Fatalf("put retention policy: %v", err)
				}
				policy := polResult.Policy
				_, currentHash, err := currentPurgeSnapshotOn(ctx, s.db, objResult.Object.ObjectID, scope, policy.BlockingHoldKinds)
				if err != nil {
					t.Fatalf("read pre-request lifecycle snapshot: %v", err)
				}
				requester := subjectPrincipal(t, "subject-nobypass-role-requester",
					[]auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard)
				reqResult, err := s.RequestPurge(ctx, core.RequestPurgeCommand{
					ObjectID: objResult.Object.ObjectID, Jurisdiction: "PE", Legislation: "NATIONAL-TAX",
					Category: "invoice", ExpectedLifecycleHash: currentHash,
					Reason: "retention period elapsed", RequestID: "req-nobypass-role",
				}, requester)
				if err != nil {
					t.Fatalf("request purge: %v", err)
				}
				request := reqResult.Request
				h2 := core.ComputeLifecycleSnapshotHash(assembleSnapshot(objResult.Object.ObjectID, scope,
					core.PurgeLifecycleRequested, core.RetentionEligibilityEligible, policy.PolicyID,
					policy.Category, policy.Version, []core.LifecycleHoldRef{}, request.RequestID, []string{}))
				return noBypassFixture{objectID: objResult.Object.ObjectID, requestID: request.RequestID, expectedHash: h2}
			},
			policy: func(t *testing.T, f noBypassFixture) string {
				controller := controllerPrincipal(t)
				d := authz.NewEvidenceLifecyclePolicy().Authorize(authz.LifecycleAuthorizationRequest{
					Action: authz.LifecycleActionApprovePurge, Principal: controller,
					ActorKind: core.ActorKindHuman, TenantID: testOrgID, CompanyID: "acme", Category: "invoice",
				})
				return d.ReasonCode
			},
			service: func(t *testing.T, s *SQLiteStore, f noBypassFixture) error {
				controller := controllerPrincipal(t)
				_, err := s.ApprovePurge(ctx, core.ApprovePurgeCommand{
					RequestID: f.requestID, ExpectedLifecycleHash: f.expectedHash,
					Reason:       "must be denied: controller is second-approver only",
					RequestIDKey: "req-approve-nobypass-role",
				}, controller)
				return err
			},
			assertNoStateChange: func(t *testing.T, s *SQLiteStore, f noBypassFixture) {
				assertApprovalSoDDenied(t, s, f.objectID, f.requestID, 0, 0, 0, core.PurgeRequestStatusRequested)
			},
		},
		{
			// Review workspace SoD (review_store_test.go:555 anchor): the reviewer
			// cannot decide its OWN proposal — SOD_VIOLATION at policy (the pure
			// SODViolation clause) and store boundaries, memory stays pending.
			name:     "review-reject/reviewer-equals-proposer",
			wantCode: auth.CodeSODViolation,
			seed: func(t *testing.T, s *SQLiteStore) noBypassFixture {
				seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
				res := mustSave(t, s, humanRecordedInput("review.nobypass.sod", "self proposal"))
				return noBypassFixture{memoryID: res.Memory.Identity.ID, envelopeHash: currentEnvelope(res)}
			},
			policy: func(t *testing.T, f noBypassFixture) string {
				if authz.SODViolation("subject-1", "subject-1") {
					return auth.CodeSODViolation
				}
				return authz.ReasonAuthorized
			},
			service: func(t *testing.T, s *SQLiteStore, f noBypassFixture) error {
				_, err := s.RejectMemory(ctx, core.RejectMemoryCommand{
					MemoryID: f.memoryID, ExpectedEnvelopeHash: f.envelopeHash,
					Reason: "self-reject", RequestID: "req-reject-nobypass-sod",
				}, controllerPrincipal(t))
				return err
			},
			assertNoStateChange: func(t *testing.T, s *SQLiteStore, f noBypassFixture) {
				if mem, ok := s.FindByID(f.memoryID); !ok || mem.Status != core.StatusPendingReview {
					t.Fatal("SOD failure must leave the memory pending_review (fail-closed, no partial decision)")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			f := tc.seed(t, s)

			// 1. Policy boundary: the pure frozen policy denies with the code.
			if got := tc.policy(t, f); got != tc.wantCode {
				t.Fatalf("policy boundary = %q, want %q", got, tc.wantCode)
			}

			// 2. Server/service boundary: the store method the adapters call
			//    returns the SAME frozen code (auth.Code).
			before := storeDigest(t, s)
			err := tc.service(t, s, f)
			if err == nil {
				t.Fatalf("service boundary accepted the denied operation (want %s)", tc.wantCode)
			}
			if code := auth.Code(err); code != tc.wantCode {
				t.Fatalf("service boundary code = %q (%v), want %q", code, err, tc.wantCode)
			}

			// 3. Store boundary: the denial committed NOTHING — the logical digest
			//    is unchanged and the operation-specific assertion holds.
			after := storeDigest(t, s)
			if before != after {
				t.Fatalf("store state changed across the denial: before %s, after %s", before, after)
			}
			tc.assertNoStateChange(t, s, f)
		})
	}
}
