// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module carries the verified approval
// principal in the HTTP request context. Middleware resolves the Authorization
// header ONCE and places only the verified principal here; handlers never see
// the raw credential. Wiring to the HTTP routes happens in the surfaces batch
// (Batch C) — here are just the context helpers.
package server

import (
	"context"

	"github.com/arkelythex/drenyra-engram/internal/auth"
)

// ctxKey is the unexported context key for the verified principal.
type ctxKey struct{}

// authErrorKey is the unexported context key for a REJECTED authentication
// credential (frozen code carried by the middleware when the resolver rejects a
// presented credential).
type authErrorKey struct{}

// WithPrincipal returns a context carrying the verified approval principal.
func WithPrincipal(ctx context.Context, principal auth.VerifiedApprovalPrincipal) context.Context {
	return context.WithValue(ctx, ctxKey{}, principal)
}

// PrincipalFromContext returns the verified principal and whether it is
// present. A zero principal with ok=false means the request was not
// authenticated.
func PrincipalFromContext(ctx context.Context) (auth.VerifiedApprovalPrincipal, bool) {
	p, ok := ctx.Value(ctxKey{}).(auth.VerifiedApprovalPrincipal)
	return p, ok
}

// RequirePrincipal returns the verified principal or AUTHENTICATION_REQUIRED
// when the context carries none. There is no silent fallback.
func RequirePrincipal(ctx context.Context) (auth.VerifiedApprovalPrincipal, error) {
	p, ok := PrincipalFromContext(ctx)
	if !ok {
		return auth.VerifiedApprovalPrincipal{}, auth.New(
			auth.CodeAuthenticationRequired,
			"no verified approval principal in request context",
		)
	}
	return p, nil
}

// WithAuthError returns a context carrying the typed error of a REJECTED
// authentication credential (the resolver's frozen code). It is stored only
// when a credential WAS presented and could not be verified — never for a
// missing or malformed Authorization header (that path leaves the context empty
// so the handler answers AUTHENTICATION_REQUIRED).
func WithAuthError(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, authErrorKey{}, err)
}

// AuthErrorFromContext returns the rejected-credential error carried in ctx, or
// nil when no credential was presented and rejected.
func AuthErrorFromContext(ctx context.Context) error {
	err, _ := ctx.Value(authErrorKey{}).(error)
	return err
}
