// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the authenticated review
// workspace service layer (v0.9.0, docs/architecture/review-workspace-v0.9.md):
// the pending_review queue, the detail assembly and the AUTHENTICATED reject/
// return decisions. It mirrors the approval service discipline (ADR-003): it
// validates command syntax, maps the verified principal to provenance (the
// Source records the verified claim; it does NOT authorize), and delegates the
// WHOLE state change to ONE atomic store operation. There is NO FindByID +
// ApplyStatusTransition composition anywhere in this path — that split is a
// TOCTOU hole; the store owns the complete decision inside a single BEGIN
// IMMEDIATE transaction (SoD, reason policy, hash guard, idempotency, decision
// event + signed receipt).
package server

import (
	"context"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ReviewStore is the narrow store surface the review workspace services need.
// The SQLiteStore satisfies it; every queue/detail read and every reject/return
// decision is ONE store operation.
type ReviewStore interface {
	ListReviewQueue(ctx context.Context, query core.ReviewQueueQuery) (core.ReviewQueuePage, error)
	ReviewDetail(ctx context.Context, memoryID string, scope core.Scope) (core.ReviewDetail, error)
	RejectMemory(ctx context.Context, cmd core.RejectMemoryCommand, principal auth.VerifiedApprovalPrincipal) (core.RejectMemoryResult, error)
	ReturnMemory(ctx context.Context, cmd core.ReturnMemoryCommand, principal auth.VerifiedApprovalPrincipal) (core.ReturnMemoryResult, error)
}

// ReviewQueue returns the pending_review queue of an EXACT company scope (design
// §3): scope-first (structural filter, never a post-filter), deterministically
// ordered, with bounded pagination. The store owns scope validation, ordering and
// the limit bounds; the service only validates the scope shape so a malformed
// call fails closed before any read.
func ReviewQueue(ctx context.Context, store ReviewStore, query core.ReviewQueueQuery) (core.ReviewQueuePage, error) {
	if err := core.AssertValidScope(query.Scope); err != nil {
		return core.ReviewQueuePage{}, err
	}
	return store.ListReviewQueue(ctx, query)
}

// ReviewDetail composes the review of ONE pending revision, scope-guarded (design
// §4): the full pending revision, the structured content diff, the evidence state
// with WORM availability, the best-effort rule state, the open proposed judgments
// and the decision-relevant metadata with the boundary notice. Read-only and
// scope-first (no authenticated session required — reads never authorize).
func ReviewDetail(ctx context.Context, store ReviewStore, memoryID string, scope core.Scope) (core.ReviewDetail, error) {
	if strings.TrimSpace(memoryID) == "" {
		return core.ReviewDetail{}, auth.New(auth.CodeMemoryNotFound, "memoryId is required")
	}
	if err := core.AssertValidScope(scope); err != nil {
		return core.ReviewDetail{}, err
	}
	return store.ReviewDetail(ctx, memoryID, scope)
}

// RejectMemory rejects a pending_review memory against the caller's expected
// envelope hash, atomically (design §5). The service only validates syntax and
// provenance; authorization (tenant/company scope, SoD, reason policy, hash
// guard, idempotency) and the state change belong to the store's locked
// transaction. The reason requirement is RISK-CLASS-dependent (materiality ≥
// material OR fiscalEffect ∈ {closing, declaration, sunat_filing}) and is
// enforced store-side against the locked row — the service never pre-decides it.
func RejectMemory(ctx context.Context, store ReviewStore, cmd core.RejectMemoryCommand, principal auth.VerifiedApprovalPrincipal) (core.RejectMemoryResult, error) {
	// A zero principal cannot decide anything. There is no struct literal or
	// caller-declared-claims constructor for a verified principal; the zero
	// value fails closed here.
	if strings.TrimSpace(principal.SubjectID()) == "" && principal.AuthenticationMethod() == "" {
		return core.RejectMemoryResult{}, auth.New(auth.CodePrincipalInvalid, "no verified approval principal present")
	}
	// Command syntax — the frozen transport mapping treats a malformed command
	// as a not-found/identity failure, never as an authorization decision.
	if strings.TrimSpace(cmd.MemoryID) == "" {
		return core.RejectMemoryResult{}, auth.New(auth.CodeMemoryNotFound, "memoryId is required")
	}
	if strings.TrimSpace(cmd.ExpectedEnvelopeHash) == "" {
		return core.RejectMemoryResult{}, auth.New(auth.CodeMemoryNotFound, "expectedEnvelopeHash is required")
	}
	if strings.TrimSpace(cmd.RequestID) == "" {
		return core.RejectMemoryResult{}, auth.New(auth.CodeMemoryNotFound, "requestId (idempotency key) is required")
	}

	// ADR-003: map the verified claim to provenance (the store persists the same
	// claim fields in the audit trail and the immutable decision event).
	_ = principalProvenance(principal)

	return store.RejectMemory(ctx, cmd, principal)
}

// ReturnMemory RETURNS a pending_review memory to its proposer for correction,
// atomically (design §5): pending_review → returned (NON-terminal — an agent
// Save on the returned memory creates a NEW revision that re-enters
// pending_review). The reason is REQUIRED for a return (a return is a
// correction request — the reason tells the agent what to fix), so the service
// fails fast with REASON_REQUIRED; everything else belongs to the store's
// locked transaction exactly like reject.
func ReturnMemory(ctx context.Context, store ReviewStore, cmd core.ReturnMemoryCommand, principal auth.VerifiedApprovalPrincipal) (core.ReturnMemoryResult, error) {
	if strings.TrimSpace(principal.SubjectID()) == "" && principal.AuthenticationMethod() == "" {
		return core.ReturnMemoryResult{}, auth.New(auth.CodePrincipalInvalid, "no verified approval principal present")
	}
	if strings.TrimSpace(cmd.MemoryID) == "" {
		return core.ReturnMemoryResult{}, auth.New(auth.CodeMemoryNotFound, "memoryId is required")
	}
	if strings.TrimSpace(cmd.ExpectedEnvelopeHash) == "" {
		return core.ReturnMemoryResult{}, auth.New(auth.CodeMemoryNotFound, "expectedEnvelopeHash is required")
	}
	if strings.TrimSpace(cmd.RequestID) == "" {
		return core.ReturnMemoryResult{}, auth.New(auth.CodeMemoryNotFound, "requestId (idempotency key) is required")
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return core.ReturnMemoryResult{}, auth.New(auth.CodeReasonRequired, "a reason is required for a return (a correction request)")
	}

	_ = principalProvenance(principal)

	return store.ReturnMemory(ctx, cmd, principal)
}
