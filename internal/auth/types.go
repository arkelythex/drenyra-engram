// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module defines the verified approval
// principal contracts (v0.4.0 Step 1, ADR-003): the authentication vocabulary,
// the read-only principal, and the deliberately narrow principal snapshot.
//
// Invariant: a VerifiedApprovalPrincipal is NEVER assembled from caller-declared
// claims. Its fields are unexported and there is no public arbitrary-input
// constructor; the ONLY factory is Resolver.Authenticate (resolver.go), which
// derives the principal from an AuthenticationAssertion (credential material)
// and the session/membership store. The engine records the claim for provenance;
// it never proves authorization.
package auth

import "sort"

// AuthenticationMethod discriminates how a principal proved its identity.
//
//	v0.4.0 Step 1: oidc is RECOGNIZED but the resolver must return
//	AUTHENTICATION_REQUIRED for it (no OIDC trust/key contract yet); session and
//	service_assertion are opaque high-entropy bearer credentials resolved by
//	SHA-256 token hash; local_dev is a loopback-only development mode.
type AuthenticationMethod string

const (
	// AuthMethodOIDC is OpenID Connect (recognized; not resolvable in Step 1).
	AuthMethodOIDC AuthenticationMethod = "oidc"
	// AuthMethodSession is an interactive session bearer credential.
	AuthMethodSession AuthenticationMethod = "session"
	// AuthMethodServiceAssertion is an opaque high-entropy service bearer
	// credential (no self-declared JWT claims until a signed-assertion trust
	// contract exists).
	AuthMethodServiceAssertion AuthenticationMethod = "service_assertion"
	// AuthMethodLocalDev is the explicit local development mode.
	AuthMethodLocalDev AuthenticationMethod = "local_dev"
)

// AssuranceLevel ranks the strength of the authentication evidence.
type AssuranceLevel string

const (
	// AssuranceLow is a weak/opportunistic authentication.
	AssuranceLow AssuranceLevel = "low"
	// AssuranceStandard is the minimum for any approval (Step 1 policy).
	AssuranceStandard AssuranceLevel = "standard"
	// AssuranceStrong is required for sunat_filing approvals.
	AssuranceStrong AssuranceLevel = "strong"
)

// AccountingRole is a professional role inside the accounting ladder or the
// explicit tax track. The ladder accountant < senior_accountant < controller is
// the ONLY dominance relation; tax roles are explicit and never implied.
type AccountingRole string

const (
	RoleAccountant                AccountingRole = "accountant"
	RoleSeniorAccountant          AccountingRole = "senior_accountant"
	RoleController                AccountingRole = "controller"
	RoleTaxReviewer               AccountingRole = "tax_reviewer"
	RoleAuthorizedTaxProfessional AccountingRole = "authorized_tax_professional"
)

// VerifiedApprovalPrincipal is the authenticated, pre-verified principal used to
// authorize approvals. Fields are unexported and read-only; no struct literal or
// public arbitrary-input constructor exists outside this package. The value is
// built only by Resolver.Authenticate from verified session/membership data.
//
// The zero value is NOT a valid principal: MembershipID and TenantID are empty,
// so the authorization policy fails closed (MEMBERSHIP_INACTIVE / scope checks).
type VerifiedApprovalPrincipal struct {
	subjectId            string
	tenantId             string
	membershipId         string
	companyScopes        []string
	roles                []AccountingRole
	authenticationMethod AuthenticationMethod
	assuranceLevel       AssuranceLevel
	authenticatedAt      string // RFC3339
	sessionId            string // optional; empty when absent
}

// newVerifiedPrincipal is the package-internal constructor. Only the resolver
// uses it; slices are defensively copied so callers can never mutate the
// principal's state through the construction input.
func newVerifiedPrincipal(
	subjectID, tenantID, membershipID string,
	companyScopes []string,
	roles []AccountingRole,
	method AuthenticationMethod,
	assurance AssuranceLevel,
	authenticatedAt, sessionID string,
) VerifiedApprovalPrincipal {
	return VerifiedApprovalPrincipal{
		subjectId:            subjectID,
		tenantId:             tenantID,
		membershipId:         membershipID,
		companyScopes:        append([]string(nil), companyScopes...),
		roles:                append([]AccountingRole(nil), roles...),
		authenticationMethod: method,
		assuranceLevel:       assurance,
		authenticatedAt:      authenticatedAt,
		sessionId:            sessionID,
	}
}

// SubjectID returns the authenticated subject id.
func (p VerifiedApprovalPrincipal) SubjectID() string { return p.subjectId }

// TenantID returns the tenant id of the session's membership.
func (p VerifiedApprovalPrincipal) TenantID() string { return p.tenantId }

// MembershipID returns the active membership id. Empty means the principal was
// not derived from an active membership (policy: MEMBERSHIP_INACTIVE).
func (p VerifiedApprovalPrincipal) MembershipID() string { return p.membershipId }

// CompanyScopes returns the company ids the membership grants access to. The
// returned slice is a defensive copy.
func (p VerifiedApprovalPrincipal) CompanyScopes() []string {
	return append([]string(nil), p.companyScopes...)
}

// Roles returns the accounting roles of the membership. The returned slice is a
// defensive copy.
func (p VerifiedApprovalPrincipal) Roles() []AccountingRole {
	return append([]AccountingRole(nil), p.roles...)
}

// AuthenticationMethod returns how the principal authenticated.
func (p VerifiedApprovalPrincipal) AuthenticationMethod() AuthenticationMethod {
	return p.authenticationMethod
}

// AssuranceLevel returns the authentication assurance level.
func (p VerifiedApprovalPrincipal) AssuranceLevel() AssuranceLevel {
	return p.assuranceLevel
}

// AuthenticatedAt returns the RFC3339 authentication timestamp.
func (p VerifiedApprovalPrincipal) AuthenticatedAt() string { return p.authenticatedAt }

// SessionID returns the optional session continuity id (empty when absent).
// It is a session id, never a token or cookie.
func (p VerifiedApprovalPrincipal) SessionID() string { return p.sessionId }

// PrincipalSnapshot is the deliberately narrow, serializable view of a verified
// principal: subject, membership, canonical roles, method, assurance and time.
// It OMITS sessionId, token material, cookies and unrelated claims — the only
// fields approval events may record (ADR-003).
type PrincipalSnapshot struct {
	SubjectID            string               `json:"subjectId"`
	MembershipID         string               `json:"membershipId"`
	Roles                []AccountingRole     `json:"roles"`
	AuthenticationMethod AuthenticationMethod `json:"authenticationMethod"`
	AssuranceLevel       AssuranceLevel       `json:"assuranceLevel"`
	AuthenticatedAt      string               `json:"authenticatedAt"`
}

// PrincipalSnapshot returns the canonical snapshot of the principal. Roles are
// sorted and deduplicated so Go and TypeScript produce identical JSON bytes.
func (p VerifiedApprovalPrincipal) PrincipalSnapshot() PrincipalSnapshot {
	return PrincipalSnapshot{
		SubjectID:            p.subjectId,
		MembershipID:         p.membershipId,
		Roles:                canonicalRoles(p.roles),
		AuthenticationMethod: p.authenticationMethod,
		AssuranceLevel:       p.assuranceLevel,
		AuthenticatedAt:      p.authenticatedAt,
	}
}

// canonicalRoles sorts and deduplicates a role list canonically. Roles are a
// SET: order is not meaningful, so snapshots always carry the same bytes.
func canonicalRoles(roles []AccountingRole) []AccountingRole {
	seen := make(map[AccountingRole]struct{}, len(roles))
	out := make([]AccountingRole, 0, len(roles))
	for _, r := range roles {
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
