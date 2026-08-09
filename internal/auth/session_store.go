// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module defines the session/membership
// store view the resolver needs. Interface only in this batch: the SQLite
// implementation arrives with the store/schema batch (Batch B). Tests use a
// fake; raw bearer credentials never cross this boundary — only SHA-256 hashes.
package auth

import "context"

// StoredSession is one authenticated session row. RevokedAt is a pointer so a
// NULL column (not revoked) is distinguishable from an empty string; any
// non-empty revoked_at makes the session invalid.
type StoredSession struct {
	ID                   string
	MembershipID         string
	AuthenticationMethod AuthenticationMethod
	AssuranceLevel       AssuranceLevel
	AuthenticatedAt      string // RFC3339
	ExpiresAt            string // RFC3339; the session is invalid after this instant
	RevokedAt            *string
}

// MembershipRecord is the membership the session binds to: the subject, the
// tenant/company scope, the status, the accounting roles and the company
// active flag.
type MembershipRecord struct {
	ID            string
	SubjectID     string
	TenantID      string
	CompanyID     string
	Status        string
	Roles         []AccountingRole
	CompanyActive bool
}

// SessionStore resolves token hashes to sessions and sessions to memberships,
// and memberships by their exact (subject, tenant, company) scope. It is the
// DB-backed membership authority the OIDC resolver cross-checks claims against.
// LookupByTokenHash receives the SHA-256 of a bearer credential — never the raw
// credential. A not-found token or a broken session row is an error that the
// resolver maps to PRINCIPAL_INVALID.
type SessionStore interface {
	LookupByTokenHash(ctx context.Context, tokenHash string) (StoredSession, error)
	LoadMembership(ctx context.Context, membershipID string) (MembershipRecord, error)
	// LookupMembershipByScope resolves the membership for the exact
	// (subject, tenant, company) tuple — the DB-backed cross-check the OIDC
	// resolver applies to the token's verified `sub` and custom tenant/company
	// claims. At most one row can exist (schema UNIQUE
	// subject_id,tenant_id,company_id), so a missing tuple IS the mismatch and
	// the resolver maps it to PRINCIPAL_INVALID (fail closed, never a guess).
	LookupMembershipByScope(ctx context.Context, subjectID, tenantID, companyID string) (MembershipRecord, error)
}
