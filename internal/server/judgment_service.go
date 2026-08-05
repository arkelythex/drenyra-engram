// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the authenticated judgment
// orchestration service (v0.4.0 Step 2 — adjudicable conflicts, design §2/§6).
// It mirrors the Step 1 approval service: validate command syntax with the
// frozen error codes, keep caller Source strictly provenance-only (agent|system
// for proposals; the verified principal is the ONLY authority for confirm/
// reject), and delegate the WHOLE state change to ONE atomic store operation.
// There is NO FindJudgment + mutate composition anywhere in this path — that
// split is a TOCTOU hole; the store owns the complete transition inside a
// single BEGIN IMMEDIATE transaction (design §2).
package server

import (
	"context"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// JudgmentStore is the narrow store surface the judgment services need. The
// SQLiteStore satisfies it; every proposal/decision/withdrawal is ONE store
// operation (design §2 — persistence and race-sensitive checks belong to the
// store, never to FindJudgment + mutate composition).
type JudgmentStore interface {
	ProposeJudgment(ctx context.Context, cmd core.ProposeJudgmentCommand, caller core.Source) (core.ProposeJudgmentResult, error)
	ConfirmJudgment(ctx context.Context, cmd core.ConfirmJudgmentCommand, principal auth.VerifiedApprovalPrincipal, policy authz.JudgmentAuthorizationPolicy) (core.ConfirmJudgmentResult, error)
	RejectJudgment(ctx context.Context, cmd core.RejectJudgmentCommand, principal auth.VerifiedApprovalPrincipal, policy authz.JudgmentAuthorizationPolicy) (core.RejectJudgmentResult, error)
	WithdrawJudgment(ctx context.Context, cmd core.WithdrawJudgmentCommand, caller core.Source) (core.WithdrawJudgmentResult, error)
}

// ProposeJudgment validates proposal syntax and proposer provenance, then
// delegates the whole state change to one atomic store operation. The caller
// Source is provenance ONLY (agent|system) — it never authorizes; a human
// actor arrives as a verified principal at confirm/reject time, never here.
func ProposeJudgment(ctx context.Context, store JudgmentStore, cmd core.ProposeJudgmentCommand, caller core.Source) (core.ProposeJudgmentResult, error) {
	// Command syntax — the frozen transport mapping treats a malformed command
	// as a not-found/identity failure, never as an authorization decision.
	if strings.TrimSpace(cmd.FromID) == "" || strings.TrimSpace(cmd.ToID) == "" ||
		strings.TrimSpace(cmd.Reason) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeMemoryNotFound, "proposal command is incomplete (fromId, toId, reason and requestId are required)")
	}
	if !core.IsProposableRelation(cmd.Relation) {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeRelationNotProposable, "relation is not proposable (supports|contradicts|explains|reconciles|reverses|supersedes only)")
	}
	if cmd.FromID == cmd.ToID {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeMemoryNotFound, "a judgment requires two DISTINCT observations (fromId and toId must differ)")
	}
	// Provenance gate: only agents and systems propose. The proposal Source
	// records the claim (provenance continuity); it never authorizes — the
	// verified principal does that at confirm/reject time.
	if !core.CanPropose(caller) {
		return core.ProposeJudgmentResult{}, auth.New(auth.CodeProposalUnauthorized, "only agents and systems may propose judgments (provenance, never authority)")
	}
	return store.ProposeJudgment(ctx, cmd, caller)
}

// ConfirmJudgment validates the confirm command and the verified principal,
// then delegates the whole authenticated adjudication (idempotency, fresh-hash
// comparison, pure policy, guarded transition, immutable event, correction
// supersession) to ONE atomic store operation.
func ConfirmJudgment(ctx context.Context, store JudgmentStore, policy authz.JudgmentAuthorizationPolicy, cmd core.ConfirmJudgmentCommand, principal auth.VerifiedApprovalPrincipal) (core.ConfirmJudgmentResult, error) {
	// A zero principal cannot adjudicate anything. There is no struct literal or
	// caller-declared-claims constructor for a verified principal; the zero
	// value fails closed here (the policy would otherwise misreport it as a
	// scope error).
	if strings.TrimSpace(principal.SubjectID()) == "" && principal.AuthenticationMethod() == "" {
		return core.ConfirmJudgmentResult{}, auth.New(auth.CodePrincipalInvalid, "no verified approval principal present")
	}
	// Command syntax — malformed commands are identity failures, never
	// authorization decisions.
	if strings.TrimSpace(cmd.JudgmentID) == "" || strings.TrimSpace(cmd.ExpectedJudgmentHash) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.ConfirmJudgmentResult{}, auth.New(auth.CodeJudgmentNotFound, "confirm command is incomplete (judgmentId, expectedJudgmentHash and requestId are required)")
	}
	if strings.TrimSpace(cmd.Resolution) == "" {
		return core.ConfirmJudgmentResult{}, auth.New(auth.CodeResolutionRequired, "a non-empty professional resolution is required for confirmation")
	}
	return store.ConfirmJudgment(ctx, cmd, principal, policy)
}

// RejectJudgment validates the reject command and the verified principal, then
// delegates the whole authenticated rejection to ONE atomic store operation.
// The human reason is the resolution field; the proposal reason is never
// silently promoted into a professional resolution.
func RejectJudgment(ctx context.Context, store JudgmentStore, policy authz.JudgmentAuthorizationPolicy, cmd core.RejectJudgmentCommand, principal auth.VerifiedApprovalPrincipal) (core.RejectJudgmentResult, error) {
	if strings.TrimSpace(principal.SubjectID()) == "" && principal.AuthenticationMethod() == "" {
		return core.RejectJudgmentResult{}, auth.New(auth.CodePrincipalInvalid, "no verified approval principal present")
	}
	if strings.TrimSpace(cmd.JudgmentID) == "" || strings.TrimSpace(cmd.ExpectedJudgmentHash) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.RejectJudgmentResult{}, auth.New(auth.CodeJudgmentNotFound, "reject command is incomplete (judgmentId, expectedJudgmentHash and requestId are required)")
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return core.RejectJudgmentResult{}, auth.New(auth.CodeResolutionRequired, "a non-empty human reason is required for rejection")
	}
	return store.RejectJudgment(ctx, cmd, principal, policy)
}

// WithdrawJudgment validates the withdrawal command and proposer provenance,
// then delegates to ONE atomic store operation. The SAME exact proposer
// identity is required (provenance continuity — never professional
// authorization, design §3.7); the store enforces it inside the transaction.
func WithdrawJudgment(ctx context.Context, store JudgmentStore, cmd core.WithdrawJudgmentCommand, caller core.Source) (core.WithdrawJudgmentResult, error) {
	if strings.TrimSpace(cmd.JudgmentID) == "" || strings.TrimSpace(cmd.RequestID) == "" {
		return core.WithdrawJudgmentResult{}, auth.New(auth.CodeJudgmentNotFound, "withdraw command is incomplete (judgmentId and requestId are required)")
	}
	if !core.CanPropose(caller) {
		return core.WithdrawJudgmentResult{}, auth.New(auth.CodeProposalUnauthorized, "only the proposing agent/system may withdraw")
	}
	return store.WithdrawJudgment(ctx, cmd, caller)
}
