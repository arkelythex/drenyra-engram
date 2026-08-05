// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module derives a VerifiedApprovalPrincipal
// from an AuthenticationAssertion (credential material) — the ONLY factory path
// (ADR-003). The engine records the claim for provenance; it never proves
// authorization.
//
// v0.4.0 Step 1 resolution rules:
//   - oidc: recognized but NOT resolvable → AUTHENTICATION_REQUIRED (no trust
//     contract yet).
//   - session / service_assertion: SHA-256(token) → session lookup; revoked or
//     expired → PRINCIPAL_INVALID; then membership lookup; inactive membership
//     or inactive company → MEMBERSHIP_INACTIVE.
//   - local_dev: allowed ONLY in RuntimeLocalDev mode with a LocalDevSubjectID;
//     builds a standard-assurance principal with the explicit local_dev marker.
//   - NO silent fallback: an absent or unknown assertion is AUTHENTICATION_REQUIRED.
//
// Raw credentials are hashed immediately and are never stored, logged or
// returned.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// RuntimeMode is the runtime the resolver runs under.
type RuntimeMode string

const (
	// RuntimeLocalDev is the explicit local development mode.
	RuntimeLocalDev RuntimeMode = "local_dev"
	// RuntimeProduction is any non-local deployment.
	RuntimeProduction RuntimeMode = "production"
)

// Resolver derives verified principals. Sessions is the session/membership
// store; Mode gates local_dev authentication.
type Resolver struct {
	Sessions SessionStore
	Mode     RuntimeMode
}

// AuthenticationAssertion carries CREDENTIAL material produced by a
// transport-specific parser — never identity claims. A principal can only be
// derived from these credentials via the store; callers cannot inject roles,
// tenant or membership claims.
type AuthenticationAssertion struct {
	// Method is how the credential should be interpreted.
	Method AuthenticationMethod
	// Credential is the bearer credential (raw token) for session /
	// service_assertion. It is hashed with SHA-256 before any lookup and never
	// stored or returned.
	Credential string
	// LocalDevSubjectID is the subject id for local_dev authentication; it is
	// only honored when Method == local_dev AND Mode == RuntimeLocalDev.
	LocalDevSubjectID string
}

// Authenticate is the ONLY factory for VerifiedApprovalPrincipal. It returns
// the zero principal plus a frozen-code error when the assertion cannot be
// verified.
func (r *Resolver) Authenticate(ctx context.Context, a AuthenticationAssertion) (VerifiedApprovalPrincipal, error) {
	switch a.Method {
	case AuthMethodOIDC:
		// Recognized but intentionally not resolvable in Step 1.
		return VerifiedApprovalPrincipal{}, New(
			CodeAuthenticationRequired,
			"oidc authentication is not available in this slice; present a session or service_assertion credential",
		)
	case AuthMethodSession, AuthMethodServiceAssertion:
		return r.authenticateBearer(ctx, a)
	case AuthMethodLocalDev:
		return r.authenticateLocalDev(a)
	default:
		// No silent fallback when the assertion is absent or unknown.
		return VerifiedApprovalPrincipal{}, New(
			CodeAuthenticationRequired,
			"no authenticatable credential present",
		)
	}
}

func (r *Resolver) authenticateBearer(ctx context.Context, a AuthenticationAssertion) (VerifiedApprovalPrincipal, error) {
	if strings.TrimSpace(a.Credential) == "" {
		return VerifiedApprovalPrincipal{}, New(CodeAuthenticationRequired, "missing bearer credential")
	}
	tokenHash := sha256Hex(a.Credential)

	session, err := r.Sessions.LookupByTokenHash(ctx, tokenHash)
	if err != nil {
		return VerifiedApprovalPrincipal{}, New(CodePrincipalInvalid, "unknown or invalid session credential")
	}
	if session.RevokedAt != nil && *session.RevokedAt != "" {
		return VerifiedApprovalPrincipal{}, New(CodePrincipalInvalid, "session is revoked")
	}
	if !sessionValidAt(session, time.Now().UTC()) {
		return VerifiedApprovalPrincipal{}, New(CodePrincipalInvalid, "session is expired or has no valid expiry")
	}

	membership, err := r.Sessions.LoadMembership(ctx, session.MembershipID)
	if err != nil {
		return VerifiedApprovalPrincipal{}, New(CodePrincipalInvalid, "session does not resolve to a membership")
	}
	if membership.Status != "active" || !membership.CompanyActive {
		return VerifiedApprovalPrincipal{}, New(CodeMembershipInactive, "membership is not active")
	}

	return newVerifiedPrincipal(
		membership.SubjectID,
		membership.TenantID,
		membership.ID,
		[]string{membership.CompanyID},
		membership.Roles,
		session.AuthenticationMethod,
		session.AssuranceLevel,
		session.AuthenticatedAt,
		session.ID,
	), nil
}

func (r *Resolver) authenticateLocalDev(a AuthenticationAssertion) (VerifiedApprovalPrincipal, error) {
	if r.Mode != RuntimeLocalDev {
		return VerifiedApprovalPrincipal{}, New(
			CodeAuthenticationRequired,
			"local_dev authentication requires an explicit local_dev runtime mode",
		)
	}
	if strings.TrimSpace(a.LocalDevSubjectID) == "" {
		return VerifiedApprovalPrincipal{}, New(
			CodeAuthenticationRequired,
			"local_dev authentication requires a subject id",
		)
	}
	// Step 1 local_dev builds a standard-assurance principal carrying the
	// explicit local_dev method marker. Tenant/membership/company context is
	// filled by the session-backed local_dev flow in the surfaces batch; a
	// principal without membership context is still denied by the policy.
	return newVerifiedPrincipal(
		a.LocalDevSubjectID,
		"",
		"",
		nil,
		nil,
		AuthMethodLocalDev,
		AssuranceStandard,
		time.Now().UTC().Format(time.RFC3339),
		"",
	), nil
}

// sessionValidAt reports whether the session is valid at the given instant:
// expires_at must parse (RFC3339 family) and be strictly after now.
func sessionValidAt(s StoredSession, now time.Time) bool {
	if strings.TrimSpace(s.ExpiresAt) == "" {
		return false
	}
	t, ok := ParseSessionTime(s.ExpiresAt)
	if !ok {
		return false
	}
	return t.After(now)
}

// ParseSessionTime parses the stored RFC3339 timestamp (fractional seconds
// allowed).
func ParseSessionTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// sha256Hex is the canonical token-hash helper: raw credentials become
// SHA-256 hex BEFORE any store lookup; the raw value never leaves this frame.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
