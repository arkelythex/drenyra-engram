// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test verifies the verified-approval-
// principal contracts: read-only getters, defensive copies, and the deliberately
// narrow PrincipalSnapshot (no session/token material; canonical role bytes).
package auth

import (
	"reflect"
	"testing"
)

// testPrincipal builds a principal directly (in-package test) with deliberately
// unsorted, duplicated roles and a session id to prove canonicalization and the
// snapshot's omission of session material.
func testPrincipal() VerifiedApprovalPrincipal {
	return VerifiedApprovalPrincipal{
		subjectId:            "subject-1",
		tenantId:             "tenant-1",
		membershipId:         "membership-1",
		companyScopes:        []string{"acme", "sire-sa"},
		roles:                []AccountingRole{RoleController, RoleAccountant, RoleAccountant, RoleSeniorAccountant},
		authenticationMethod: AuthMethodSession,
		assuranceLevel:       AssuranceStandard,
		authenticatedAt:      "2026-08-05T12:00:00Z",
		sessionId:            "session-secret-id",
	}
}

func TestPrincipalSnapshotOmitsSessionAndTokenMaterial(t *testing.T) {
	p := testPrincipal()
	snap := p.PrincipalSnapshot()

	// The snapshot type has NO sessionId/token/cookie field (compile-time
	// guarantee); assert every exposed field matches the source principal.
	if snap.SubjectID != p.SubjectID() {
		t.Errorf("snapshot subjectId = %q, want %q", snap.SubjectID, p.SubjectID())
	}
	if snap.MembershipID != p.MembershipID() {
		t.Errorf("snapshot membershipId = %q, want %q", snap.MembershipID, p.MembershipID())
	}
	if snap.AuthenticationMethod != p.AuthenticationMethod() {
		t.Errorf("snapshot authenticationMethod = %q, want %q", snap.AuthenticationMethod, p.AuthenticationMethod())
	}
	if snap.AssuranceLevel != p.AssuranceLevel() {
		t.Errorf("snapshot assuranceLevel = %q, want %q", snap.AssuranceLevel, p.AssuranceLevel())
	}
	if snap.AuthenticatedAt != p.AuthenticatedAt() {
		t.Errorf("snapshot authenticatedAt = %q, want %q", snap.AuthenticatedAt, p.AuthenticatedAt())
	}
	// Session continuity must never leak into the snapshot.
	if snap.SubjectID == p.SessionID() {
		t.Errorf("snapshot unexpectedly exposes the session id as subject")
	}
}

func TestPrincipalSnapshotCanonicalizesRoles(t *testing.T) {
	snap := testPrincipal().PrincipalSnapshot()
	// Canonical order is lexicographic (Go sort.Strings == TS default sort),
	// NOT ladder order — parity is what matters for identical JSON bytes.
	want := []AccountingRole{RoleAccountant, RoleController, RoleSeniorAccountant}
	if !reflect.DeepEqual(snap.Roles, want) {
		t.Errorf("snapshot roles = %v, want sorted+deduped %v", snap.Roles, want)
	}
}

func TestPrincipalGettersReturnDefensiveCopies(t *testing.T) {
	p := testPrincipal()

	// Mutating the returned company scopes must not affect the principal.
	scopes := p.CompanyScopes()
	scopes[0] = "mutated"
	if got := p.CompanyScopes(); got[0] != "acme" {
		t.Errorf("companyScopes mutated through getter: got %q, want %q", got[0], "acme")
	}

	// Mutating the returned roles must not affect the principal.
	roles := p.Roles()
	roles[0] = RoleTaxReviewer
	if got := p.Roles(); got[0] != RoleController {
		t.Errorf("roles mutated through getter: got %q, want %q", got[0], RoleController)
	}
}

func TestPrincipalGetters(t *testing.T) {
	p := testPrincipal()
	if got := p.SubjectID(); got != "subject-1" {
		t.Errorf("SubjectID = %q, want subject-1", got)
	}
	if got := p.TenantID(); got != "tenant-1" {
		t.Errorf("TenantID = %q, want tenant-1", got)
	}
	if got := p.MembershipID(); got != "membership-1" {
		t.Errorf("MembershipID = %q, want membership-1", got)
	}
	if got := p.SessionID(); got != "session-secret-id" {
		t.Errorf("SessionID = %q, want session-secret-id", got)
	}
}

func TestZeroPrincipalIsNotValid(t *testing.T) {
	var zero VerifiedApprovalPrincipal
	if zero.MembershipID() != "" || zero.TenantID() != "" || zero.SubjectID() != "" {
		t.Errorf("zero principal must carry no identity, got %+v", zero)
	}
	// A zero principal's snapshot must not carry any identity either.
	snap := zero.PrincipalSnapshot()
	if snap.SubjectID != "" || snap.MembershipID != "" || len(snap.Roles) != 0 {
		t.Errorf("zero principal snapshot must be empty, got %+v", snap)
	}
}
