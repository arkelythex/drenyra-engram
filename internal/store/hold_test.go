// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module freezes the v0.8 batch 3 object-level legal-hold
// store surface (docs/architecture/evidence-lifecycle-v0.8.md §3.2/§7/§9):
//
//   - PlaceHold writes ONE immutable hold row under the authenticated
//     preservation gate (extended evidence-lifecycle policy, place_hold action —
//     deny-list first, then records_compliance_officer | tenant_records_owner,
//     assurance ≥ standard, tenant/company match), (tenant, requestId)
//     idempotency, and emits the hold_placed receipt atomically on the
//     evidence_object chain (v0.8.0 payload version);
//   - LiftHold closes a placed hold ONE-WAY under the SAME gate (lift_hold):
//     lifted_at/lifted_by/lift_reason set together, ALREADY_DECIDED on a fresh
//     lift of an already-lifted hold, replay returns the stored outcome, and the
//     hold_lifted receipt is emitted atomically;
//   - a signing failure rolls the whole act back (no hold row, no receipt, no
//     idempotency reservation);
//   - ActiveBlockingHolds / HoldsForObject are SCOPE-FIRST (cross-tenant
//     invisibility — OBJECT_NOT_FOUND);
//   - EMERGENCY BYPASS: place/lift work INSIDE a closed period (holds only
//     preserve evidence), while storing into the same closed period still fails
//     with PERIOD_CLOSED.
package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// holdFixtureObject stores ONE object in the test scope (RUC A / 202401) and
// returns its content address — the hold target every test protects.
func holdFixtureObject(t *testing.T, s *SQLiteStore) string {
	t.Helper()
	result, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("hold-target")))
	if err != nil {
		t.Fatalf("store hold target object: %v", err)
	}
	return result.Object.ObjectID
}

func TestPlaceHoldHappyPath(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	objectID := holdFixtureObject(t, s)

	result, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       objectID,
		Kind:           core.HoldKindLegal,
		Reason:         "dispute F001-948 under review",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-place-1",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("place hold: %v", err)
	}
	if !result.Created || result.IdempotentReplay {
		t.Fatalf("result = %+v, want a created (non-replay) placement", result)
	}
	h := result.Hold
	if h.HoldID == "" || h.ObjectID != objectID {
		t.Fatalf("hold = %+v, want a minted id on the target object", h)
	}
	if h.TenantID != testOrgID || h.CompanyID != "acme" || h.RUC != testRucA || h.Period != testPeriod {
		t.Fatalf("hold scope = %+v, want the OBJECT's exact scope (never caller-declared)", h)
	}
	if h.Kind != core.HoldKindLegal || h.Reason != "dispute F001-948 under review" || h.OwnerSubjectID != "subject-9" {
		t.Fatalf("hold evidence = %+v", h)
	}
	if h.PlacedBy != "subject-1" || h.PlacedAt == "" {
		t.Fatalf("hold provenance = (%q, %q), want the acting principal + a timestamp", h.PlacedBy, h.PlacedAt)
	}
	if h.LiftedAt != "" || h.LiftedBy != "" || h.LiftReason != "" {
		t.Fatalf("a placed hold must carry no lift fields: %+v", h)
	}
	if err := core.AssertValidEvidenceHold(h); err != nil {
		t.Fatalf("stored hold must pass the model validator: %v", err)
	}

	// The hold_placed receipt is on the OBJECT's chain with the v0.8.0 payload
	// version and the complete principal snapshot.
	var payloadJSON, policyVersion, subjectID string
	if err := s.db.QueryRow(`SELECT payload_json, policy_version, subject_id FROM receipts WHERE subject_type = 'evidence_object' AND subject_id = ? AND action = 'hold_placed'`, objectID).
		Scan(&payloadJSON, &policyVersion, &subjectID); err != nil {
		t.Fatalf("read hold_placed receipt: %v", err)
	}
	if subjectID != objectID {
		t.Fatalf("receipt subject = %q, want the object chain", subjectID)
	}
	if policyVersion != "evidence-lifecycle-policy/v0.8.0" {
		t.Fatalf("policy version = %q, want the frozen evidence-lifecycle policy", policyVersion)
	}
	if !strings.Contains(payloadJSON, `"version":"receipt-payload/v0.8.0"`) ||
		!strings.Contains(payloadJSON, `"principalRoles":["records_compliance_officer"]`) ||
		!strings.Contains(payloadJSON, `"evidenceRef":"`+objectID) {
		t.Fatalf("hold_placed payload must carry the v0.8 version, the snapshot and the object ref: %s", payloadJSON)
	}

	// The chain links: the hold_placed receipt chains on the object_stored
	// receipt (previous_receipt_hash non-empty).
	var prev string
	if err := s.db.QueryRow(`SELECT previous_receipt_hash FROM receipts WHERE action = 'hold_placed' AND subject_id = ?`, objectID).Scan(&prev); err != nil {
		t.Fatalf("read previous hash: %v", err)
	}
	if prev == "" {
		t.Fatal("hold_placed must chain on the object's existing receipt chain")
	}
}

func TestPlaceHoldObjectNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       strings.Repeat("c", 64),
		Kind:           core.HoldKindLegal,
		Reason:         "reason",
		OwnerSubjectID: "subject-1",
		RequestID:      "req-1",
	}, recordsPrincipal(t))
	if err == nil || !strings.Contains(err.Error(), "OBJECT_NOT_FOUND") {
		t.Fatalf("placing on an unknown object = %v, want OBJECT_NOT_FOUND", err)
	}
}

// TestPlaceHoldRoleAndActorDenial freezes the authenticated preservation gate:
// the deny-list (operational_accountant, *admin) and non-owner roles are denied
// with the frozen codes, and a cross-tenant principal is denied before any role
// check (frozen check order §8.2). No row is ever written on a denial.
func TestPlaceHoldRoleAndActorDenial(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	objectID := holdFixtureObject(t, s)

	cases := []struct {
		name      string
		principal auth.VerifiedApprovalPrincipal
		wantCode  string
	}{
		{"controller not an owner", subjectPrincipal(t, "ctrl-1", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard), auth.CodeRoleNotAuthorized},
		{"operational accountant deny-listed", subjectPrincipal(t, "opacct-1", []auth.AccountingRole{auth.RoleOperationalAccountant}, auth.AssuranceStandard), auth.CodeRoleDenied},
		{"admin token deny-listed", subjectPrincipal(t, "admin-1", []auth.AccountingRole{auth.AccountingRole("deployment_admin")}, auth.AssuranceStandard), auth.CodeRoleDenied},
		{"low assurance", subjectPrincipal(t, "low-1", []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceLow), auth.CodeAssuranceTooLow},
		{"cross tenant", subjectPrincipalTenant(t, "other-tenant", []auth.AccountingRole{auth.RoleRecordsComplianceOfficer}, auth.AssuranceStandard), auth.CodeTenantScopeMismatch},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
				ObjectID:       objectID,
				Kind:           core.HoldKindLegal,
				Reason:         "reason",
				OwnerSubjectID: "subject-9",
				RequestID:      "req-deny-" + tt.name,
			}, tt.principal)
			if err == nil || !strings.Contains(err.Error(), tt.wantCode) {
				t.Fatalf("denial = %v, want code %s", err, tt.wantCode)
			}
			var rows int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_holds WHERE object_id = ?`, objectID).Scan(&rows); err != nil {
				t.Fatalf("count holds: %v", err)
			}
			if rows != 0 {
				t.Fatalf("a denied placement must write no hold row, count = %d", rows)
			}
		})
	}
}

func TestPlaceHoldIdempotency(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	objectID := holdFixtureObject(t, s)
	principal := recordsPrincipal(t)

	cmd := core.PlaceHoldCommand{
		ObjectID:       objectID,
		Kind:           core.HoldKindAudit,
		Reason:         "audit in progress",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-same",
	}
	first, err := s.PlaceHold(context.Background(), cmd, principal)
	if err != nil {
		t.Fatalf("first place: %v", err)
	}
	replay, err := s.PlaceHold(context.Background(), cmd, principal)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.IdempotentReplay || replay.Created || replay.Hold.HoldID != first.Hold.HoldID {
		t.Fatalf("replay = %+v, want the stored outcome with idempotentReplay=true", replay)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_holds WHERE object_id = ?`, objectID).Scan(&rows); err != nil {
		t.Fatalf("count holds: %v", err)
	}
	if rows != 1 {
		t.Fatalf("replay must not write a second row, count = %d", rows)
	}
	var receipts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM receipts WHERE action = 'hold_placed' AND subject_id = ?`, objectID).Scan(&receipts); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if receipts != 1 {
		t.Fatalf("replay must not emit a second receipt, count = %d", receipts)
	}

	// Same requestId with a DIFFERENT command → IDEMPOTENCY_CONFLICT.
	other := cmd
	other.Reason = "changed intent"
	if _, err := s.PlaceHold(context.Background(), other, principal); err == nil || !strings.Contains(err.Error(), auth.CodeIdempotencyConflict) {
		t.Fatalf("reused requestId with a different command = %v, want IDEMPOTENCY_CONFLICT", err)
	}
	// Same requestId with a DIFFERENT principal → IDEMPOTENCY_CONFLICT.
	otherPrincipal := subjectPrincipal(t, "owner-2", []auth.AccountingRole{auth.RoleTenantRecordsOwner}, auth.AssuranceStandard)
	if _, err := s.PlaceHold(context.Background(), cmd, otherPrincipal); err == nil || !strings.Contains(err.Error(), auth.CodeIdempotencyConflict) {
		t.Fatalf("reused requestId with a different principal = %v, want IDEMPOTENCY_CONFLICT", err)
	}
}

// TestPlaceHoldReceiptRollback: a signing failure rolls the WHOLE act back — no
// hold row, no idempotency reservation, no receipt (the frozen atomicity
// invariant of contracts/receipts.md).
func TestPlaceHoldReceiptRollback(t *testing.T) {
	s := newTestStore(t)
	signer := newParitySigner(s)
	s.SetReceiptSigner(signer)
	objectID := holdFixtureObject(t, s)

	signer.fail = true
	_, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       objectID,
		Kind:           core.HoldKindLegal,
		Reason:         "reason",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-rollback",
	}, recordsPrincipal(t))
	if err == nil {
		t.Fatal("an injected signing failure must fail the placement")
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_holds WHERE object_id = ?`, objectID).Scan(&rows); err != nil {
		t.Fatalf("count holds: %v", err)
	}
	if rows != 0 {
		t.Fatalf("a rolled-back placement must leave no hold row, count = %d", rows)
	}
	var idem int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM evidence_hold_idempotency_keys WHERE request_id = 'req-rollback'`).Scan(&idem); err != nil {
		t.Fatalf("count idempotency keys: %v", err)
	}
	if idem != 0 {
		t.Fatalf("a rolled-back placement must leave no idempotency reservation, count = %d", idem)
	}

	// The same requestId can be retried cleanly once the signer works (the
	// reservation rolled back with the transaction).
	signer.fail = false
	result, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       objectID,
		Kind:           core.HoldKindLegal,
		Reason:         "reason",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-rollback",
	}, recordsPrincipal(t))
	if err != nil || !result.Created {
		t.Fatalf("retry after rollback = (%+v, %v), want a created placement", result, err)
	}
}

func TestLiftHoldOneWay(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	objectID := holdFixtureObject(t, s)
	placed, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       objectID,
		Kind:           core.HoldKindDispute,
		Reason:         "dispute open",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-place-lift",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	lifted, err := s.LiftHold(context.Background(), core.LiftHoldCommand{
		HoldID:    placed.Hold.HoldID,
		Reason:    "dispute resolved",
		RequestID: "req-lift-1",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("lift: %v", err)
	}
	if !lifted.Lifted || lifted.IdempotentReplay {
		t.Fatalf("lift result = %+v, want a fresh one-way closure", lifted)
	}
	h := lifted.Hold
	if h.LiftedAt == "" || h.LiftedBy != "subject-1" || h.LiftReason != "dispute resolved" {
		t.Fatalf("lifted hold = %+v, want the closure fields set together by the principal", h)
	}
	if err := core.AssertValidEvidenceHold(h); err != nil {
		t.Fatalf("lifted hold must pass the model validator: %v", err)
	}
	// The hold_lifted receipt is on the OBJECT's chain.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM receipts WHERE action = 'hold_lifted' AND subject_id = ?`, objectID).Scan(&n); err != nil {
		t.Fatalf("count lift receipts: %v", err)
	}
	if n != 1 {
		t.Fatalf("hold_lifted receipt count = %d, want 1", n)
	}
	// The active blocking query no longer reports it.
	active, err := s.ActiveBlockingHolds(context.Background(), objectID, testScope(testRucA), core.DefaultBlockingHoldKinds)
	if err != nil {
		t.Fatalf("active blocking holds: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active blocking holds after lift = %+v, want none", active)
	}

	// A FRESH lift of the already-lifted hold → ALREADY_DECIDED (never reopens).
	_, err = s.LiftHold(context.Background(), core.LiftHoldCommand{
		HoldID:    placed.Hold.HoldID,
		Reason:    "second attempt",
		RequestID: "req-lift-2",
	}, recordsPrincipal(t))
	if err == nil || !strings.Contains(err.Error(), auth.CodeAlreadyDecided) {
		t.Fatalf("fresh lift of a lifted hold = %v, want ALREADY_DECIDED", err)
	}

	// A REPLAY of the successful lift returns the stored (lifted) outcome.
	replay, err := s.LiftHold(context.Background(), core.LiftHoldCommand{
		HoldID:    placed.Hold.HoldID,
		Reason:    "dispute resolved",
		RequestID: "req-lift-1",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("lift replay: %v", err)
	}
	if !replay.IdempotentReplay || !replay.Lifted || replay.Hold.HoldID != placed.Hold.HoldID {
		t.Fatalf("lift replay = %+v, want the stored lifted outcome", replay)
	}

	// Lifting an unknown hold → HOLD_NOT_FOUND.
	if _, err := s.LiftHold(context.Background(), core.LiftHoldCommand{
		HoldID:    "00000000-0000-4000-8000-00000000dead",
		Reason:    "why",
		RequestID: "req-lift-missing",
	}, recordsPrincipal(t)); err == nil || !strings.Contains(err.Error(), "HOLD_NOT_FOUND") {
		t.Fatalf("lift of an unknown hold = %v, want HOLD_NOT_FOUND", err)
	}
}

func TestLiftHoldRoleDenied(t *testing.T) {
	s := newTestStore(t)
	objectID := holdFixtureObject(t, s)
	placed, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       objectID,
		Kind:           core.HoldKindLegal,
		Reason:         "reason",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-place-denied-lift",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	// The same deny matrix applies to lift_hold: a controller never lifts.
	_, err = s.LiftHold(context.Background(), core.LiftHoldCommand{
		HoldID:    placed.Hold.HoldID,
		Reason:    "resolved",
		RequestID: "req-lift-denied",
	}, subjectPrincipal(t, "ctrl-1", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	if err == nil || !strings.Contains(err.Error(), auth.CodeRoleNotAuthorized) {
		t.Fatalf("lift by controller = %v, want ROLE_NOT_AUTHORIZED", err)
	}
	var liftAt any
	if err := s.db.QueryRow(`SELECT lifted_at FROM evidence_holds WHERE id = ?`, placed.Hold.HoldID).Scan(&liftAt); err != nil {
		t.Fatalf("read hold: %v", err)
	}
	if liftAt != nil {
		t.Fatal("a denied lift must leave the hold placed")
	}
}

func TestActiveBlockingHoldsQuery(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	objectID := holdFixtureObject(t, s)

	// One legal (blocking by default) + one other (never blocking by default).
	if _, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID: objectID, Kind: core.HoldKindLegal, Reason: "legal", OwnerSubjectID: "s1", RequestID: "rq-1",
	}, recordsPrincipal(t)); err != nil {
		t.Fatalf("place legal: %v", err)
	}
	if _, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID: objectID, Kind: core.HoldKindOther, Reason: "internal note", OwnerSubjectID: "s1", RequestID: "rq-2",
	}, recordsPrincipal(t)); err != nil {
		t.Fatalf("place other: %v", err)
	}

	// Default blocking set → only the legal hold blocks.
	active, err := s.ActiveBlockingHolds(context.Background(), objectID, testScope(testRucA), core.DefaultBlockingHoldKinds)
	if err != nil {
		t.Fatalf("active blocking holds: %v", err)
	}
	if len(active) != 1 || active[0].Kind != core.HoldKindLegal {
		t.Fatalf("active blocking holds = %+v, want only the legal hold", active)
	}
	// A narrowed set (audit only) → nothing blocks.
	narrowed, err := s.ActiveBlockingHolds(context.Background(), objectID, testScope(testRucA), []string{core.RetentionHoldKindAudit})
	if err != nil {
		t.Fatalf("narrowed blocking holds: %v", err)
	}
	if len(narrowed) != 0 {
		t.Fatalf("narrowed blocking holds = %+v, want none", narrowed)
	}
	// An EMPTY blocking set → nothing blocks (fail closed).
	empty, err := s.ActiveBlockingHolds(context.Background(), objectID, testScope(testRucA), nil)
	if err != nil {
		t.Fatalf("empty blocking holds: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty blocking set must block nothing, got %+v", empty)
	}
	// The full audit surface sees BOTH holds (placed and lifted, all kinds).
	all, err := s.HoldsForObject(context.Background(), objectID, testScope(testRucA))
	if err != nil {
		t.Fatalf("holds for object: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("holds for object = %+v, want the two placed holds", all)
	}
}

func TestHoldsForObjectScopeFirst(t *testing.T) {
	s := newTestStore(t)
	objectID := holdFixtureObject(t, s)
	if _, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID: objectID, Kind: core.HoldKindLegal, Reason: "r", OwnerSubjectID: "s1", RequestID: "rq-1",
	}, recordsPrincipal(t)); err != nil {
		t.Fatalf("place: %v", err)
	}

	// A different tenant/company/RUC/period caller sees OBJECT_NOT_FOUND (cross-
	// tenant invisibility — never the holds).
	if _, err := s.HoldsForObject(context.Background(), objectID, testScope(testRucB)); err == nil || !strings.Contains(err.Error(), "OBJECT_NOT_FOUND") {
		t.Fatalf("cross-scope holds read = %v, want OBJECT_NOT_FOUND", err)
	}
	if _, err := s.ActiveBlockingHolds(context.Background(), objectID, testScope(testRucB), core.DefaultBlockingHoldKinds); err == nil || !strings.Contains(err.Error(), "OBJECT_NOT_FOUND") {
		t.Fatalf("cross-scope blocking read = %v, want OBJECT_NOT_FOUND", err)
	}
	// An unknown object id → OBJECT_NOT_FOUND (no scope metadata disclosed).
	if _, err := s.HoldsForObject(context.Background(), strings.Repeat("d", 64), testScope(testRucA)); err == nil || !strings.Contains(err.Error(), "OBJECT_NOT_FOUND") {
		t.Fatalf("unknown object holds read = %v, want OBJECT_NOT_FOUND", err)
	}
	// A known object with no holds → empty list, not an error.
	other := s
	_ = other
	result, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("no-holds-here")))
	if err != nil {
		t.Fatalf("store second object: %v", err)
	}
	none, err := s.HoldsForObject(context.Background(), result.Object.ObjectID, testScope(testRucA))
	if err != nil {
		t.Fatalf("no-holds object read: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("no-holds object = %+v, want an empty list", none)
	}
}

// TestHoldEmergencyClosedPeriodBypass: placing and lifting a hold INSIDE a
// closed exact company period SUCCEEDS (holds only preserve evidence — the
// closed-period gate applies to purge because purge reduces availability, design
// §10), while storing an object into the same closed period still fails
// PERIOD_CLOSED. This is the emergency-bypass contract of batch 3.
func TestHoldEmergencyClosedPeriodBypass(t *testing.T) {
	s := newTestStore(t)
	s.SetReceiptSigner(newParitySigner(s))
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saveAndApproveClose(t, s, testScope(testRucA), "close blocks writes", "req-close-hold")

	// The period is genuinely closed for object writes.
	_, err := s.StoreObject(context.Background(), objectInputForTest(t, []byte("test")))
	if err == nil || !strings.Contains(err.Error(), "PERIOD_CLOSED") {
		t.Fatalf("storing into the closed period = %v, want PERIOD_CLOSED", err)
	}

	// The hold target must exist BEFORE the close (no object write can enter the
	// closed period). Seed it in a SEPARATE fresh store scope? No — seed before
	// the close on the same store: reorder — the object below was stored before
	// the close.
	objectID := core.ComputeObjectID([]byte("test"))
	if _, ok := s.EvidenceObjectByID(context.Background(), objectID); ok {
		t.Fatal("the rejected object write must not exist")
	}
	// Seed the hold target first, THEN close the period.
	pre := newTestStore(t)
	pre.SetReceiptSigner(newParitySigner(s))
	seedAcmeIdentity(t, pre, []auth.AccountingRole{auth.RoleController})
	target := holdFixtureObject(t, pre)
	saveAndApproveClose(t, pre, testScope(testRucA), "close blocks writes", "req-close-hold-2")

	// Place + lift inside the closed period succeed (emergency bypass).
	placed, err := pre.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       target,
		Kind:           core.HoldKindLegal,
		Reason:         "emergency legal hold inside a closed period",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-emergency-place",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("emergency place inside a closed period must succeed: %v", err)
	}
	lifted, err := pre.LiftHold(context.Background(), core.LiftHoldCommand{
		HoldID:    placed.Hold.HoldID,
		Reason:    "emergency lift",
		RequestID: "req-emergency-lift",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("emergency lift inside a closed period must succeed: %v", err)
	}
	if !lifted.Lifted {
		t.Fatalf("emergency lift = %+v, want the closure fields set", lifted)
	}
}

// subjectPrincipalTenant builds a verified principal in a DIFFERENT tenant (the
// cross-tenant denial fixture).
func subjectPrincipalTenant(t *testing.T, tenantID string, roles []auth.AccountingRole, assurance auth.AssuranceLevel) auth.VerifiedApprovalPrincipal {
	t.Helper()
	return mustPrincipal(t, &fixedSessionStore{
		session: auth.StoredSession{
			ID:                   "session-" + tenantID,
			MembershipID:         "membership-" + tenantID,
			AuthenticationMethod: auth.AuthMethodSession,
			AssuranceLevel:       assurance,
			AuthenticatedAt:      "2026-08-05T12:00:00Z",
			ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
		membership: auth.MembershipRecord{
			ID:            "membership-" + tenantID,
			SubjectID:     "subject-" + tenantID,
			TenantID:      tenantID,
			CompanyID:     "acme",
			Status:        "active",
			Roles:         roles,
			CompanyActive: true,
		},
	})
}

func TestPlaceHoldStoredResultRoundTrips(t *testing.T) {
	s := newTestStore(t)
	objectID := holdFixtureObject(t, s)
	result, err := s.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       objectID,
		Kind:           core.HoldKindFiscalization,
		Reason:         "fiscalization review",
		OwnerSubjectID: "subject-9",
		RequestID:      "req-roundtrip",
	}, recordsPrincipal(t))
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	// The idempotency ledger's stored result is the CANONICAL model (decodes to
	// the same bytes an audit re-canonicalizes).
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM evidence_hold_idempotency_keys WHERE request_id = 'req-roundtrip'`).Scan(&raw); err != nil {
		t.Fatalf("read stored result: %v", err)
	}
	var stored core.EvidenceHold
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("decode stored result: %v", err)
	}
	if string(core.CanonicalEvidenceHoldJSON(stored)) != string(core.CanonicalEvidenceHoldJSON(result.Hold)) {
		t.Fatalf("stored result does not match the returned hold")
	}
}
