// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the CLI-local identity→scope
// binding (scope-param-rollout D-SPR-1/D-SPR-4, FR-SPR-3, FD-SPR-1/FD-SPR-3/
// FD-SPR-5/FD-SPR-6): it validates an already-derived company scope against a
// session principal's membership scope with EXACT-match semantics and NO
// fallback. It grants no authority — it only constrains already-authenticated
// callers' scope (IR-3); the store-level scope assertions stay exactly as
// hardened (NFR-SPR-5). Go cannot import the server package's twin helper, so
// the CLI binary carries its own copy (D-SPR-1).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// bindScopeToPrincipal returns the frozen typed scope-denied error when the
// effective scope does not exactly match the principal's membership scope, or
// nil when it does. It mirrors the server helper and the frozen approval-policy
// comparison (internal/authz/approval_policy.go): the scope's organizationId
// MUST equal the principal's tenant and the scope's companyId MUST be inside the
// principal's company scopes. Institutional (and any non-company) scopes pass
// through untouched (FD-SPR-4). There is NO fallback and NO rewrite: on mismatch
// the caller fails closed with TENANT_SCOPE_MISMATCH / COMPANY_SCOPE_DENIED
// (FD-SPR-1/FD-SPR-5).
func bindScopeToPrincipal(scope core.Scope, p auth.VerifiedApprovalPrincipal) error {
	if scope.Kind != core.ScopeKindCompany {
		return nil // FD-SPR-4: institutional orthogonal
	}
	if scope.OrganizationID != p.TenantID() {
		return auth.ErrTenantScopeMismatch // TENANT_SCOPE_MISMATCH
	}
	if !contains(p.CompanyScopes(), scope.CompanyID) {
		return auth.ErrCompanyScopeDenied // COMPANY_SCOPE_DENIED
	}
	return nil
}

// contains reports whether the slice holds the target string — the same
// membership-company comparison the frozen approval policy freezes at
// internal/authz/approval_policy.go (D-SPR-1).
func contains(scopes []string, target string) bool {
	for _, s := range scopes {
		if s == target {
			return true
		}
	}
	return false
}

// cliBindScope enforces the identity→scope binding at the CLI seam
// (scope-param-rollout FR-SPR-3/D-SPR-4/FD-SPR-6): when a session credential is
// present in the session file AND resolves to a VerifiedApprovalPrincipal, the
// derived effective scope MUST exactly match the principal's membership scope
// before any store data access. On mismatch it prints the typed denial and
// returns a non-zero exit code; on match, or when no session resolves a
// principal (session-less reference mode, FD-SPR-3), it returns 0 so the command
// proceeds unchanged. It never invents a principal where none resolves (FD-SPR-6)
// — a stale/invalid session token is treated as no principal.
func cliBindScope(scope core.Scope, dbPath string) int {
	token, err := loadSessionToken()
	if err != nil {
		return 0 // no session file → unbound reference mode (FD-SPR-3)
	}
	st, err := openStore(dbPath)
	if err != nil {
		return 0 // the command's own store open reports the error unchanged
	}
	defer func() { _ = st.Close() }()
	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		return 0 // no principal resolved (FD-SPR-6): never invent one
	}
	if err := bindScopeToPrincipal(scope, principal); err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 1
	}
	return 0
}
