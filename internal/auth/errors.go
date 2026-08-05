// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the approval error codes
// as typed sentinels/wrappers. The code strings are the SINGLE source for the
// HTTP/MCP mapping (later batches); only ENVELOPE_MISMATCH carries the two
// envelope hashes.
package auth

import (
	"errors"
	"fmt"
)

// Frozen error codes (v0.4.0 Step 1 — do not rename or reuse).
const (
	CodeAuthenticationRequired    = "AUTHENTICATION_REQUIRED"
	CodePrincipalInvalid          = "PRINCIPAL_INVALID"
	CodeMembershipInactive        = "MEMBERSHIP_INACTIVE"
	CodeTenantScopeMismatch       = "TENANT_SCOPE_MISMATCH"
	CodeCompanyScopeDenied        = "COMPANY_SCOPE_DENIED"
	CodeRoleNotAuthorized         = "ROLE_NOT_AUTHORIZED"
	CodeAssuranceTooLow           = "ASSURANCE_TOO_LOW"
	CodeMaterialityLimitExceeded  = "MATERIALITY_LIMIT_EXCEEDED"
	CodeReasonRequired            = "REASON_REQUIRED"
	CodeMemoryNotFound            = "MEMORY_NOT_FOUND"
	CodeInvalidTransition         = "INVALID_TRANSITION"
	CodeEnvelopeMismatch          = "ENVELOPE_MISMATCH"
	CodeAlreadyDecided            = "ALREADY_DECIDED"
	CodeIdempotencyConflict       = "IDEMPOTENCY_CONFLICT"
)

// Frozen error codes (v0.4.0 Step 2 — judgment lifecycle; do not rename or
// reuse). JUDGMENT_HASH_MISMATCH is the judgment-hash analogue of
// ENVELOPE_MISMATCH: a separately versioned contract (judgment hashes are
// NEVER compared against envelope hashes).
const (
	CodeJudgmentNotFound          = "JUDGMENT_NOT_FOUND"
	CodeRelationNotProposable     = "RELATION_NOT_PROPOSABLE"
	CodeResolutionRequired        = "RESOLUTION_REQUIRED"
	CodeProposalUnauthorized      = "PROPOSAL_UNAUTHORIZED"
	CodeInvalidJudgmentTransition = "INVALID_JUDGMENT_TRANSITION"
	CodeJudgmentConflict          = "JUDGMENT_CONFLICT"
	CodeJudgmentHashMismatch      = "JUDGMENT_HASH_MISMATCH"
)

// Error is the typed approval error: a frozen code plus a human message.
// Only ENVELOPE_MISMATCH carries ExpectedEnvelopeHash/ActualEnvelopeHash; all
// other codes leave them empty.
type Error struct {
	Code                 string
	Message              string
	ExpectedEnvelopeHash string
	ActualEnvelopeHash   string
}

func (e *Error) Error() string {
	if e.Code == CodeEnvelopeMismatch && e.ExpectedEnvelopeHash != "" && e.ActualEnvelopeHash != "" {
		return fmt.Sprintf("%s: %s (expected envelope %s, actual envelope %s)",
			e.Code, e.Message, e.ExpectedEnvelopeHash, e.ActualEnvelopeHash)
	}
	return e.Code + ": " + e.Message
}

// Is makes every *Error compare equal, via errors.Is, to any other *Error with
// the same code — the sentinel semantics for a typed, code-carrying error.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Code == t.Code
}

// New returns a typed *Error carrying the frozen code.
func New(code, message string) error {
	return &Error{Code: code, Message: message}
}

// NewEnvelopeMismatch returns an ENVELOPE_MISMATCH error carrying ONLY the
// expected and actual envelope hashes (never memory content).
func NewEnvelopeMismatch(expectedHash, actualHash, message string) error {
	return &Error{
		Code:                 CodeEnvelopeMismatch,
		Message:              message,
		ExpectedEnvelopeHash: expectedHash,
		ActualEnvelopeHash:   actualHash,
	}
}

// Code returns the frozen error code carried by err, or "" when err is not an
// *Error (including nil). Wrapped errors are unwrapped via errors.As.
func Code(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Typed sentinels for errors.Is matching by code. Message is descriptive only;
// comparison is by Code (see Error.Is).
var (
	ErrAuthenticationRequired   = &Error{Code: CodeAuthenticationRequired, Message: "authentication required"}
	ErrPrincipalInvalid         = &Error{Code: CodePrincipalInvalid, Message: "principal invalid"}
	ErrMembershipInactive       = &Error{Code: CodeMembershipInactive, Message: "membership is not active"}
	ErrTenantScopeMismatch      = &Error{Code: CodeTenantScopeMismatch, Message: "tenant scope mismatch"}
	ErrCompanyScopeDenied       = &Error{Code: CodeCompanyScopeDenied, Message: "company scope denied"}
	ErrRoleNotAuthorized        = &Error{Code: CodeRoleNotAuthorized, Message: "role not authorized"}
	ErrAssuranceTooLow          = &Error{Code: CodeAssuranceTooLow, Message: "assurance too low"}
	ErrMaterialityLimitExceeded = &Error{Code: CodeMaterialityLimitExceeded, Message: "materiality limit exceeded"}
	ErrReasonRequired           = &Error{Code: CodeReasonRequired, Message: "reason required"}
	ErrMemoryNotFound           = &Error{Code: CodeMemoryNotFound, Message: "memory not found"}
	ErrInvalidTransition        = &Error{Code: CodeInvalidTransition, Message: "invalid transition"}
	ErrEnvelopeMismatch         = &Error{Code: CodeEnvelopeMismatch, Message: "envelope mismatch"}
	ErrAlreadyDecided           = &Error{Code: CodeAlreadyDecided, Message: "already decided"}
	ErrIdempotencyConflict      = &Error{Code: CodeIdempotencyConflict, Message: "idempotency conflict"}
	// ── v0.4.0 Step 2 judgment lifecycle codes ──
	ErrJudgmentNotFound          = &Error{Code: CodeJudgmentNotFound, Message: "judgment not found"}
	ErrRelationNotProposable     = &Error{Code: CodeRelationNotProposable, Message: "relation is not proposable"}
	ErrResolutionRequired        = &Error{Code: CodeResolutionRequired, Message: "a non-empty resolution is required"}
	ErrProposalUnauthorized      = &Error{Code: CodeProposalUnauthorized, Message: "proposal unauthorized"}
	ErrInvalidJudgmentTransition = &Error{Code: CodeInvalidJudgmentTransition, Message: "invalid judgment transition"}
	ErrJudgmentConflict          = &Error{Code: CodeJudgmentConflict, Message: "judgment conflict"}
	ErrJudgmentHashMismatch      = &Error{Code: CodeJudgmentHashMismatch, Message: "judgment hash mismatch"}
)
