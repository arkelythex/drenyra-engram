// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the shared HTTP/MCP
// identity→scope binding (scope-param-rollout, FD-SPR-1/FD-SPR-5): it validates
// an already-derived effective scope against a verified approval principal's
// membership scope with EXACT-match semantics and NO fallback. It grants no
// authority — it only constrains already-authorized callers' scope (IR-3); the
// store-level scope assertions stay exactly as hardened (NFR-SPR-5).
package server

import (
	"context"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// bindScopeToPrincipal returns the frozen typed scope-denied error when the
// effective scope does not exactly match the principal's membership scope, or
// nil when it does. It mirrors the frozen approval-policy comparison at
// internal/authz/approval_policy.go (contains(p.CompanyScopes(), ...)): the
// scope's organizationId MUST equal the principal's tenant and the scope's
// companyId MUST be inside the principal's company scopes. Institutional (and
// any non-company) scopes pass through untouched (FD-SPR-4). There is NO
// fallback and NO rewrite: on mismatch the caller fails closed with the frozen
// TENANT_SCOPE_MISMATCH / COMPANY_SCOPE_DENIED codes (FD-SPR-1/FD-SPR-5).
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
// internal/authz/approval_policy.go (D-SPR-1). The server package has no other
// reachable copy, so the helper lives here beside the binding.
func contains(scopes []string, target string) bool {
	for _, s := range scopes {
		if s == target {
			return true
		}
	}
	return false
}

// boundScope enforces the identity→scope binding for one effective scope
// (scope-param-rollout FR-SPR-2/FD-SPR-6): when a verified approval principal
// is present in ctx (resolved by the authenticate middleware), the effective
// scope MUST exactly match the principal's membership scope — the frozen typed
// denial otherwise. A context without a principal is unbound reference mode
// (FD-SPR-3) and passes. It never resolves a principal where none exists and
// never re-derives scope.
func boundScope(ctx context.Context, scope core.Scope) error {
	if p, ok := PrincipalFromContext(ctx); ok {
		return bindScopeToPrincipal(scope, p)
	}
	return nil
}
