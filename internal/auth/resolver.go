// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module derives a VerifiedApprovalPrincipal
// from an AuthenticationAssertion (credential material) — the ONLY factory path
// (ADR-003). The engine records the claim for provenance; it never proves
// authorization.
//
// v0.4.0 Step 1 resolution rules (session-based) plus the first Production
// Identity slice (stateless OIDC access tokens):
//   - oidc: resolvable ONLY when the resolver carries a configured OIDC
//     validator; the raw access token is verified in memory (RS256, exact
//     issuer/audience, bounded time claims), then the verified tenant/company
//     claims are cross-checked against the ACTIVE DB membership for `sub`
//     (missing/mismatched tuple → PRINCIPAL_INVALID; inactive membership →
//     MEMBERSHIP_INACTIVE). Without a configured validator oidc fails closed
//     with AUTHENTICATION_REQUIRED. OIDC is STATELESS: no session is ever
//     created and no raw token is ever persisted.
//   - session / service_assertion: SHA-256(token) → session lookup; revoked or
//     expired → PRINCIPAL_INVALID; then membership lookup; inactive membership
//     or inactive company → MEMBERSHIP_INACTIVE.
//   - local_dev: allowed ONLY in RuntimeLocalDev mode with a LocalDevSubjectID;
//     builds a standard-assurance principal with the explicit local_dev marker.
//   - NO silent fallback: an absent or unknown assertion is AUTHENTICATION_REQUIRED.
//
// Raw credentials are hashed (session paths) or verified in memory (oidc) and
// are never stored, logged or returned.
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
// store; Mode gates local_dev authentication; OIDC is the stateless access-token
// validator of the first Production Identity slice. A nil OIDC validator keeps
// the Step 1 fail-closed behavior (AuthMethodOIDC → AUTHENTICATION_REQUIRED).
type Resolver struct {
	Sessions SessionStore
	Mode     RuntimeMode
	OIDC     *OIDCValidator
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
		return r.authenticateOIDC(ctx, a)
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

func (r *Resolver) authenticateOIDC(ctx context.Context, a AuthenticationAssertion) (VerifiedApprovalPrincipal, error) {
	if r.OIDC == nil {
		// Fail closed: without a configured trust contract oidc is not
		// resolvable — same behavior as the Step 1 stub.
		return VerifiedApprovalPrincipal{}, New(
			CodeAuthenticationRequired,
			"oidc authentication is not configured; present a session or service_assertion credential",
		)
	}
	if strings.TrimSpace(a.Credential) == "" {
		return VerifiedApprovalPrincipal{}, New(CodeAuthenticationRequired, "missing bearer credential")
	}
	// The raw access token is verified IN MEMORY (RS256, exact issuer/audience,
	// bounded time claims, JWKS cache with one unknown-kid refresh). It is never
	// hashed into a session, never stored and never returned.
	claims, err := r.OIDC.Validate(ctx, a.Credential)
	if err != nil {
		return VerifiedApprovalPrincipal{}, New(CodePrincipalInvalid, "oidc access token rejected: "+err.Error())
	}
	// DB-backed cross-check: the claimed tenant/company tuple must resolve to
	// the ACTIVE membership of the verified `sub`. A missing tuple is the
	// mismatch — ambiguity or drift fails closed as PRINCIPAL_INVALID.
	membership, err := r.Sessions.LookupMembershipByScope(ctx, claims.Subject, claims.TenantID, claims.CompanyID)
	if err != nil {
		return VerifiedApprovalPrincipal{}, New(
			CodePrincipalInvalid,
			"oidc subject has no membership for the claimed tenant/company scope",
		)
	}
	if membership.Status != "active" || !membership.CompanyActive {
		return VerifiedApprovalPrincipal{}, New(CodeMembershipInactive, "membership is not active")
	}
	// Default OIDC assurance is standard; no unconfigured ACR/MFA elevation.
	// OIDC is stateless: the principal carries no session id. The snapshot's
	// authenticatedAt is the SERVER-OBSERVED validation time (ValidatedAt — the
	// instant this resolver verified the token), never the token `iat`: audit
	// provenance records when the server authenticated the caller.
	return newVerifiedPrincipal(
		membership.SubjectID,
		membership.TenantID,
		membership.ID,
		[]string{membership.CompanyID},
		membership.Roles,
		AuthMethodOIDC,
		AssuranceStandard,
		claims.ValidatedAt.UTC().Format(time.RFC3339),
		"",
	), nil
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
