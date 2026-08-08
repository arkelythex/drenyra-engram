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
	CodeAuthenticationRequired   = "AUTHENTICATION_REQUIRED"
	CodePrincipalInvalid         = "PRINCIPAL_INVALID"
	CodeMembershipInactive       = "MEMBERSHIP_INACTIVE"
	CodeTenantScopeMismatch      = "TENANT_SCOPE_MISMATCH"
	CodeCompanyScopeDenied       = "COMPANY_SCOPE_DENIED"
	CodeRoleNotAuthorized        = "ROLE_NOT_AUTHORIZED"
	CodeAssuranceTooLow          = "ASSURANCE_TOO_LOW"
	CodeMaterialityLimitExceeded = "MATERIALITY_LIMIT_EXCEEDED"
	CodeReasonRequired           = "REASON_REQUIRED"
	CodeMemoryNotFound           = "MEMORY_NOT_FOUND"
	CodeInvalidTransition        = "INVALID_TRANSITION"
	CodeEnvelopeMismatch         = "ENVELOPE_MISMATCH"
	CodeAlreadyDecided           = "ALREADY_DECIDED"
	CodeIdempotencyConflict      = "IDEMPOTENCY_CONFLICT"
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

// Frozen error codes (v0.5.0 close foundation — do not rename or reuse).
// PERIOD_CLOSED is the close write gate: an exact company period whose closure
// projection is status='closed' is immutable until an explicit controller
// reopen. It carries ONLY the scope tuple and the close memory ID (never
// private content). PERIOD_ALREADY_CLOSED is the approval-time guard: a second
// close for an already-closed period is rejected.
const (
	CodePeriodClosed        = "PERIOD_CLOSED"
	CodePeriodAlreadyClosed = "PERIOD_ALREADY_CLOSED"
)

// Frozen error codes (v0.5.0 reconciliation foundation — do not rename or
// reuse). RECONCILIATION_HASH_MISMATCH is the reconciliation-hash analogue of
// JUDGMENT_HASH_MISMATCH: reconciliation hashes are NEVER compared against
// envelope hashes or judgment hashes (separately versioned contracts).
const (
	CodeReconciliationNotFound          = "RECONCILIATION_NOT_FOUND"
	CodeInvalidReconciliationTransition = "INVALID_RECONCILIATION_TRANSITION"
	CodeReconciliationConflict          = "RECONCILIATION_CONFLICT"
	CodeReconciliationHashMismatch      = "RECONCILIATION_HASH_MISMATCH"
)

// Frozen error codes (v0.8.0 evidence-lifecycle policy — do not rename or
// reuse; design §8.3). ROLE_DENIED is the deny-list outcome (operational_accountant,
// any role token containing "admin", agent and system actor kinds — it precedes
// every allow). APPROVER_IS_REQUESTER is the SoD rule on approve; the dual-approval
// codes gate the second approval: DUAL_APPROVAL_REQUIRED fires when a second
// approval is attempted for a category that is not configured for dual approval,
// SAME_PRINCIPAL_SECOND_APPROVAL when the second approver IS the first approver.
const (
	CodeRoleDenied                  = "ROLE_DENIED"
	CodeApproverIsRequester         = "APPROVER_IS_REQUESTER"
	CodeDualApprovalRequired        = "DUAL_APPROVAL_REQUIRED"
	CodeSamePrincipalSecondApproval = "SAME_PRINCIPAL_SECOND_APPROVAL"
)

// Frozen blocker/state codes (v0.8.0 evidence-lifecycle — design §8.3, batch 2
// delivery). UNKNOWN_RETENTION_STATE is the fail-closed outcome when no EXACT
// active policy resolves (the engine never guesses a retention outcome);
// RETENTION_POLICY_AMBIGUOUS fires when multiple enabled candidates share the
// highest version of the exact tuple (schema UNIQUE is the corruption backstop);
// RETENTION_NOT_DUE blocks a request for a resolved policy whose min_period has
// not been reached; LIFECYCLE_VERSION_MISMATCH is the expected-version drift guard
// of policy supersession; NOT_PURGEABLE rejects institutional (cross-company)
// objects; POLICY_EVIDENCE_REQUIRED rejects a policy row without
// jurisdiction/legislation/authority/source. HOLD_ACTIVE and the purge-transition
// codes of §8.3 belong to the deferred holds/request/execution batches.
const (
	CodeUnknownRetentionState  = "UNKNOWN_RETENTION_STATE"
	CodeRetentionPolicyAmbiguous = "RETENTION_POLICY_AMBIGUOUS"
	CodeRetentionNotDue        = "RETENTION_NOT_DUE"
	CodeLifecycleVersionMismatch = "LIFECYCLE_VERSION_MISMATCH"
	CodeNotPurgeable           = "NOT_PURGEABLE"
	CodePolicyEvidenceRequired = "POLICY_EVIDENCE_REQUIRED"
)

// Error is the typed approval/judgment/reconciliation error: a frozen code
// plus a human message. Only ENVELOPE_MISMATCH carries
// ExpectedEnvelopeHash/ActualEnvelopeHash, only JUDGMENT_HASH_MISMATCH and
// RECONCILIATION_HASH_MISMATCH carry ExpectedJudgmentHash/ActualJudgmentHash
// (the shared hash-details carrier) and only PERIOD_CLOSED carries the
// TenantID/CompanyID/FiscalPeriodID/CloseMemoryID tuple; all other codes
// leave every field empty (design §6 — hash contracts are versioned
// separately and NEVER compared against each other or envelope hashes).
type Error struct {
	Code                 string
	Message              string
	ExpectedEnvelopeHash string
	ActualEnvelopeHash   string
	ExpectedJudgmentHash string
	ActualJudgmentHash   string
	TenantID             string
	CompanyID            string
	FiscalPeriodID       string
	CloseMemoryID        string
}

func (e *Error) Error() string {
	if e.Code == CodeEnvelopeMismatch && e.ExpectedEnvelopeHash != "" && e.ActualEnvelopeHash != "" {
		return fmt.Sprintf("%s: %s (expected envelope %s, actual envelope %s)",
			e.Code, e.Message, e.ExpectedEnvelopeHash, e.ActualEnvelopeHash)
	}
	if e.Code == CodeJudgmentHashMismatch && e.ExpectedJudgmentHash != "" && e.ActualJudgmentHash != "" {
		return fmt.Sprintf("%s: %s (expected judgment hash %s, actual judgment hash %s)",
			e.Code, e.Message, e.ExpectedJudgmentHash, e.ActualJudgmentHash)
	}
	if e.Code == CodeReconciliationHashMismatch && e.ExpectedJudgmentHash != "" && e.ActualJudgmentHash != "" {
		return fmt.Sprintf("%s: %s (expected reconciliation hash %s, actual reconciliation hash %s)",
			e.Code, e.Message, e.ExpectedJudgmentHash, e.ActualJudgmentHash)
	}
	if e.Code == CodePeriodClosed && e.TenantID != "" && e.CompanyID != "" {
		return fmt.Sprintf("%s: %s (tenant %s, company %s, period %s, close memory %s)",
			e.Code, e.Message, e.TenantID, e.CompanyID, e.FiscalPeriodID, e.CloseMemoryID)
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

// NewJudgmentHashMismatch returns a JUDGMENT_HASH_MISMATCH error carrying ONLY
// the expected and actual judgment hashes (never judgment content) — the
// judgment-hash analogue of NewEnvelopeMismatch (design §6).
func NewJudgmentHashMismatch(expectedHash, actualHash, message string) error {
	return &Error{
		Code:                 CodeJudgmentHashMismatch,
		Message:              message,
		ExpectedJudgmentHash: expectedHash,
		ActualJudgmentHash:   actualHash,
	}
}

// NewReconciliationHashMismatch returns a RECONCILIATION_HASH_MISMATCH error
// carrying ONLY the expected and actual reconciliation hashes (never
// reconciliation content) — the reconciliation-hash analogue of
// NewJudgmentHashMismatch (design §6).
func NewReconciliationHashMismatch(expectedHash, actualHash, message string) error {
	return &Error{
		Code:                 CodeReconciliationHashMismatch,
		Message:              message,
		ExpectedJudgmentHash: expectedHash,
		ActualJudgmentHash:   actualHash,
	}
}

// NewPeriodClosed returns a PERIOD_CLOSED error carrying ONLY the exact scope
// tuple and the close memory ID (never private content): the close write gate's
// typed error. Adapters map it to a closed-period HTTP/MCP/CLI result.
func NewPeriodClosed(tenantID, companyID, fiscalPeriodID, closeMemoryID, message string) error {
	return &Error{
		Code:           CodePeriodClosed,
		Message:        message,
		TenantID:       tenantID,
		CompanyID:      companyID,
		FiscalPeriodID: fiscalPeriodID,
		CloseMemoryID:  closeMemoryID,
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
	// ── v0.5.0 close foundation codes ──
	ErrPeriodClosed        = &Error{Code: CodePeriodClosed, Message: "period is closed; an explicit controller reopen is required before writing"}
	ErrPeriodAlreadyClosed = &Error{Code: CodePeriodAlreadyClosed, Message: "period is already closed by another close"}
	// ── v0.5.0 reconciliation foundation codes ──
	ErrReconciliationNotFound          = &Error{Code: CodeReconciliationNotFound, Message: "reconciliation not found"}
	ErrInvalidReconciliationTransition = &Error{Code: CodeInvalidReconciliationTransition, Message: "invalid reconciliation transition"}
	ErrReconciliationConflict          = &Error{Code: CodeReconciliationConflict, Message: "reconciliation conflict"}
	ErrReconciliationHashMismatch      = &Error{Code: CodeReconciliationHashMismatch, Message: "reconciliation hash mismatch"}
	// ── v0.8.0 evidence-lifecycle policy codes ──
	ErrRoleDenied                  = &Error{Code: CodeRoleDenied, Message: "role is deny-listed for evidence lifecycle acts"}
	ErrApproverIsRequester         = &Error{Code: CodeApproverIsRequester, Message: "the approver cannot be the requester (separation of duties)"}
	ErrDualApprovalRequired        = &Error{Code: CodeDualApprovalRequired, Message: "a second approval requires a dual-approval-configured category"}
	ErrSamePrincipalSecondApproval = &Error{Code: CodeSamePrincipalSecondApproval, Message: "the second approver must be a distinct principal"}
)
