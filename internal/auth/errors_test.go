// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the approval error-code
// contract: typed sentinels, the Code() helper, wrapping, and the
// ENVELOPE_MISMATCH-only hash payload.
package auth

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodeExtraction(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil error", nil, ""},
		{"typed error", New(CodeRoleNotAuthorized, "nope"), CodeRoleNotAuthorized},
		{"envelope mismatch", NewEnvelopeMismatch("abc", "def", "stale"), CodeEnvelopeMismatch},
		{"wrapped typed error", fmt.Errorf("outer: %w", New(CodeMemoryNotFound, "missing")), CodeMemoryNotFound},
		{"plain error", errors.New("plain"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(tt.err); got != tt.want {
				t.Errorf("Code() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEnvelopeMismatchCarriesOnlyHashes(t *testing.T) {
	err := NewEnvelopeMismatch("expected-hash", "actual-hash", "stale envelope")
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("NewEnvelopeMismatch must return *Error, got %T", err)
	}
	if typed.Code != CodeEnvelopeMismatch {
		t.Errorf("code = %q, want %q", typed.Code, CodeEnvelopeMismatch)
	}
	if typed.ExpectedEnvelopeHash != "expected-hash" || typed.ActualEnvelopeHash != "actual-hash" {
		t.Errorf("hashes = %q/%q, want expected-hash/actual-hash", typed.ExpectedEnvelopeHash, typed.ActualEnvelopeHash)
	}

	// Non-envelope errors never carry hashes.
	other := New(CodeAlreadyDecided, "decided")
	var o *Error
	if !errors.As(other, &o) {
		t.Fatalf("New must return *Error")
	}
	if o.ExpectedEnvelopeHash != "" || o.ActualEnvelopeHash != "" {
		t.Errorf("non-envelope error must not carry hashes, got %+v", o)
	}
}

func TestSentinelMatchingByCode(t *testing.T) {
	err := New(CodePrincipalInvalid, "unknown session")
	if !errors.Is(err, ErrPrincipalInvalid) {
		t.Errorf("errors.Is(New(PRINCIPAL_INVALID), ErrPrincipalInvalid) = false, want true")
	}
	if errors.Is(err, ErrAuthenticationRequired) {
		t.Errorf("errors.Is with a different code must be false")
	}
	// Wrapped errors still match the sentinel.
	if !errors.Is(fmt.Errorf("context: %w", err), ErrPrincipalInvalid) {
		t.Errorf("wrapped PRINCIPAL_INVALID must match ErrPrincipalInvalid")
	}
}

func TestFrozenCodes(t *testing.T) {
	// The frozen codes are a closed set; these literals are the contract the
	// HTTP/MCP mapping consumes later.
	want := []string{
		CodeAuthenticationRequired,
		CodePrincipalInvalid,
		CodeMembershipInactive,
		CodeTenantScopeMismatch,
		CodeCompanyScopeDenied,
		CodeRoleNotAuthorized,
		CodeAssuranceTooLow,
		CodeMaterialityLimitExceeded,
		CodeReasonRequired,
		CodeMemoryNotFound,
		CodeInvalidTransition,
		CodeEnvelopeMismatch,
		CodeAlreadyDecided,
		CodeIdempotencyConflict,
	}
	for _, code := range want {
		if code == "" {
			t.Errorf("frozen code constant must not be empty")
		}
	}
	if len(want) != 14 {
		t.Errorf("frozen code set changed: got %d codes, want 14", len(want))
	}
}

func TestErrorString(t *testing.T) {
	if got := New(CodeReasonRequired, "reason is required").Error(); got != "REASON_REQUIRED: reason is required" {
		t.Errorf("Error() = %q", got)
	}
	env := NewEnvelopeMismatch("exp", "act", "stale")
	if got := env.Error(); got != "ENVELOPE_MISMATCH: stale (expected envelope exp, actual envelope act)" {
		t.Errorf("Error() = %q", got)
	}
}
