// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the authenticated approval
// service (v0.4.0 Step 1, ADR-003). It validates command syntax, maps the
// verified principal to provenance (the Source records the verified claim; it
// does NOT authorize), and delegates the WHOLE state change to ONE atomic store
// operation. There is NO FindByID + ApplyStatusTransition composition anywhere
// in this path — that split is a TOCTOU hole; the store owns the complete
// approval inside a single BEGIN IMMEDIATE transaction.
package server

import (
	"context"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ApprovalStore is the narrow store surface the approval service needs. The
// SQLiteStore satisfies it; the whole approval is one store operation.
type ApprovalStore interface {
	ApproveMemory(ctx context.Context, cmd core.ApproveMemoryCommand, principal auth.VerifiedApprovalPrincipal, policy authz.ApprovalAuthorizationPolicy) (core.ApprovalResult, error)
}

// ApproveMemory approves a pending_review memory against the caller's expected
// envelope hash, atomically. The service only validates syntax and provenance;
// authorization (tenant/company scope, role, assurance, materiality) and the
// state change belong to the store's locked transaction and the pure policy.
func ApproveMemory(ctx context.Context, store ApprovalStore, policy authz.ApprovalAuthorizationPolicy, cmd core.ApproveMemoryCommand, principal auth.VerifiedApprovalPrincipal) (core.ApprovalResult, error) {
	// A zero principal cannot approve anything. There is no struct literal or
	// caller-declared-claims constructor for a verified principal; the zero
	// value fails closed here (the policy would otherwise misreport it as a
	// scope error).
	if strings.TrimSpace(principal.SubjectID()) == "" && principal.AuthenticationMethod() == "" {
		return core.ApprovalResult{}, auth.New(auth.CodePrincipalInvalid, "no verified approval principal present")
	}
	// Command syntax — the frozen transport mapping treats a malformed command
	// as a not-found/identity failure, never as an authorization decision.
	if strings.TrimSpace(cmd.MemoryID) == "" {
		return core.ApprovalResult{}, auth.New(auth.CodeMemoryNotFound, "memoryId is required")
	}
	if strings.TrimSpace(cmd.ExpectedEnvelopeHash) == "" {
		return core.ApprovalResult{}, auth.New(auth.CodeMemoryNotFound, "expectedEnvelopeHash is required")
	}
	if strings.TrimSpace(cmd.RequestID) == "" {
		return core.ApprovalResult{}, auth.New(auth.CodeMemoryNotFound, "requestId (idempotency key) is required")
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return core.ApprovalResult{}, auth.New(auth.CodeReasonRequired, "a reason is required for approval")
	}

	// ADR-003: map the verified claim to provenance. The store persists the same
	// claim fields (actor = subject id, actor kind human) in the audit trail and
	// the immutable approval event; transports use this derivation for
	// structured provenance without ever carrying principal fields in a payload.
	_ = principalProvenance(principal)

	return store.ApproveMemory(ctx, cmd, principal, policy)
}

// principalProvenance maps a verified principal to the provenance Source of the
// approval (ADR-003): System is the authentication method, ActorID the subject
// id, ActorKind always human (a verified principal IS a professional) and
// Session the optional session continuity id. The Source records the verified
// claim; it does NOT authorize — the policy does that.
func principalProvenance(p auth.VerifiedApprovalPrincipal) core.Source {
	return core.Source{
		System:    string(p.AuthenticationMethod()),
		ActorID:   p.SubjectID(),
		ActorKind: core.ActorKindHuman,
		Session:   p.SessionID(),
	}
}
