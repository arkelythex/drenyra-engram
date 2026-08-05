// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the first-class reconciliation
// orchestration service (v0.5.0 — adjudicated reconciliations;
// docs/architecture/close-intelligence-v0.5.md §3/§6). It mirrors the judgment
// service: validate command syntax with the frozen error codes, keep caller
// Source strictly provenance-only (agent|system for proposals; the verified
// principal is the ONLY authority for confirm/reject), and delegate the WHOLE
// state change to ONE atomic store operation. There is NO GetReconciliation +
// mutate composition anywhere in this path — that split is a TOCTOU hole; the
// store owns the complete transition inside a single BEGIN IMMEDIATE
// transaction (design §2/§3.2).
package server

import (
	"context"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ReconciliationStore is the narrow store surface the reconciliation services
// need. The SQLiteStore satisfies it; every proposal/decision/withdrawal is ONE
// store operation (design §3.2 — persistence and race-sensitive checks belong to
// the store, never to GetReconciliation + mutate composition).
type ReconciliationStore interface {
	ProposeReconciliation(ctx context.Context, cmd core.ProposeReconciliationCommand, caller core.Source) (core.ProposeReconciliationResult, error)
	ConfirmReconciliation(ctx context.Context, cmd core.ConfirmReconciliationCommand, principal auth.VerifiedApprovalPrincipal, policy authz.ReconciliationAuthorizationPolicy) (core.ConfirmReconciliationResult, error)
	RejectReconciliation(ctx context.Context, cmd core.RejectReconciliationCommand, principal auth.VerifiedApprovalPrincipal, policy authz.ReconciliationAuthorizationPolicy) (core.RejectReconciliationResult, error)
	WithdrawReconciliation(ctx context.Context, cmd core.WithdrawReconciliationCommand, caller core.Source) (core.WithdrawReconciliationResult, error)
}

// ProposeReconciliation validates proposal syntax and proposer provenance, then
// delegates the whole state change to one atomic store operation. The caller
// Source is provenance ONLY (agent|system) — it never authorizes; a human actor
// arrives as a verified principal at confirm/reject time, never here.
func ProposeReconciliation(ctx context.Context, store ReconciliationStore, cmd core.ProposeReconciliationCommand, caller core.Source) (core.ProposeReconciliationResult, error) {
	// Command syntax — the frozen transport mapping treats a malformed command
	// as a not-found/identity failure, never as an authorization decision
	// (mirrors the store's own defense-in-depth guards).
	if strings.TrimSpace(cmd.LeftMemoryID) == "" || strings.TrimSpace(cmd.RightMemoryID) == "" ||
		strings.TrimSpace(cmd.Method) == "" || strings.TrimSpace(cmd.Currency) == "" ||
		strings.TrimSpace(cmd.Reason) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "proposal command is incomplete (leftMemoryId, rightMemoryId, method, currency, reason and requestId are required)")
	}
	if cmd.LeftMemoryID == cmd.RightMemoryID {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "a reconciliation requires two DISTINCT observations (leftMemoryId and rightMemoryId must differ)")
	}
	if cmd.ToleranceCents < 0 {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "toleranceCents must be non-negative")
	}
	// Provenance gate: only agents and systems propose. The proposal Source
	// records the claim (provenance continuity); it never authorizes — the
	// verified principal does that at confirm/reject time.
	if !core.CanPropose(caller) {
		return core.ProposeReconciliationResult{}, auth.New(auth.CodeProposalUnauthorized, "only agents and systems may propose reconciliations (provenance, never authority)")
	}
	return store.ProposeReconciliation(ctx, cmd, caller)
}

// ConfirmReconciliation validates the confirm command and the verified
// principal, then delegates the whole authenticated adjudication (idempotency,
// fresh-hash comparison, pure reconciliation-policy/v0.5.0, guarded transition,
// immutable event, reconciles relation projection, correction supersession) to
// ONE atomic store operation.
func ConfirmReconciliation(ctx context.Context, store ReconciliationStore, policy authz.ReconciliationAuthorizationPolicy, cmd core.ConfirmReconciliationCommand, principal auth.VerifiedApprovalPrincipal) (core.ConfirmReconciliationResult, error) {
	// A zero principal cannot adjudicate anything. There is no struct literal or
	// caller-declared-claims constructor for a verified principal; the zero
	// value fails closed here (the policy would otherwise misreport it as a
	// scope error).
	if strings.TrimSpace(principal.SubjectID()) == "" && principal.AuthenticationMethod() == "" {
		return core.ConfirmReconciliationResult{}, auth.New(auth.CodePrincipalInvalid, "no verified approval principal present")
	}
	// Command syntax — malformed commands are identity failures, never
	// authorization decisions.
	if strings.TrimSpace(cmd.ReconciliationID) == "" || strings.TrimSpace(cmd.ExpectedReconciliationHash) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.ConfirmReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "confirm command is incomplete (reconciliationId, expectedReconciliationHash and requestId are required)")
	}
	if strings.TrimSpace(cmd.Resolution) == "" {
		return core.ConfirmReconciliationResult{}, auth.New(auth.CodeResolutionRequired, "a non-empty professional resolution is required for confirmation")
	}
	return store.ConfirmReconciliation(ctx, cmd, principal, policy)
}

// RejectReconciliation validates the reject command and the verified principal,
// then delegates the whole authenticated rejection to ONE atomic store
// operation. The human reason is the resolution field; the proposal reason is
// never silently promoted into a professional resolution.
func RejectReconciliation(ctx context.Context, store ReconciliationStore, policy authz.ReconciliationAuthorizationPolicy, cmd core.RejectReconciliationCommand, principal auth.VerifiedApprovalPrincipal) (core.RejectReconciliationResult, error) {
	if strings.TrimSpace(principal.SubjectID()) == "" && principal.AuthenticationMethod() == "" {
		return core.RejectReconciliationResult{}, auth.New(auth.CodePrincipalInvalid, "no verified approval principal present")
	}
	if strings.TrimSpace(cmd.ReconciliationID) == "" || strings.TrimSpace(cmd.ExpectedReconciliationHash) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.RejectReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "reject command is incomplete (reconciliationId, expectedReconciliationHash and requestId are required)")
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return core.RejectReconciliationResult{}, auth.New(auth.CodeResolutionRequired, "a non-empty human reason is required for rejection")
	}
	return store.RejectReconciliation(ctx, cmd, principal, policy)
}

// WithdrawReconciliation validates the withdrawal command and proposer
// provenance, then delegates to ONE atomic store operation. The SAME exact
// proposer identity is required (provenance continuity — never professional
// authorization, design §3.7); the store enforces it inside the transaction.
func WithdrawReconciliation(ctx context.Context, store ReconciliationStore, cmd core.WithdrawReconciliationCommand, caller core.Source) (core.WithdrawReconciliationResult, error) {
	if strings.TrimSpace(cmd.ReconciliationID) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.WithdrawReconciliationResult{}, auth.New(auth.CodeReconciliationNotFound, "withdraw command is incomplete (reconciliationId and requestId are required)")
	}
	if !core.CanPropose(caller) {
		return core.WithdrawReconciliationResult{}, auth.New(auth.CodeProposalUnauthorized, "only the proposing agent/system may withdraw")
	}
	return store.WithdrawReconciliation(ctx, cmd, caller)
}
