// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the Delivery Slice 4
// "shared operation/trusted-context checks" service (design.md "Proposed
// modules > New" — internal/server/fiscal_scope_service.go).
//
// It has two responsibilities, both defense-in-depth (design.md "Enforcement
// and data flow": "adapter validation is defense in depth, not the only
// enforcement point"):
//
//  1. ValidateFiscalReadIntent — the SERVICE-layer mirror of the store's
//     private verifyFiscalIntentAxes (internal/store/fiscal_scope_store.go):
//     when a caller supplies a v1 fiscal binding for a READ-ONLY protected
//     operation (the ASK/ANALYZE classification — context.read, review.queue,
//     review.detail, timeline.read, evidence.get, reconstruct.read,
//     verify.read), it must be a structurally valid binding naming the EXACT
//     invoked operation and matching the caller's trusted scope axes before
//     any read proceeds. A nil intent always passes — every read path in this
//     slice remains reachable without a binding (legacy/unbound reads are
//     unaffected); this helper never fills a missing element and never grants
//     authority (proposal.md "Scope metadata does not authorize"). No adapter
//     wires a caller-supplied intent into a read path yet (Slice 6); this
//     helper is the shared primitive that wiring will call.
//
//  2. FiscalScopeBindingLayer — the I/O orchestration that loads one subject's
//     persisted, audit-anchor-resolved fiscal binding evidence and delegates
//     the PURE classification to core.VerifyFiscalScopeBinding (design.md
//     "Verification and audit"). It is wired into VerifyMemory and
//     VerifyEvidenceObject (verify_service.go) as an ADDITIVE report layer:
//     no caller-supplied binding is required to read a report — the layer is
//     entirely derived from persisted state, exactly like the store's
//     ClassifyFiscalSubject.
package server

import (
	"context"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// FiscalBindingEvidenceStore is the narrow read surface FiscalScopeBindingLayer
// needs: the persisted, audit-anchor-resolved fiscal binding evidence of one
// subject, in sequence order (nil for a legacy/unbound subject).
// *store.SQLiteStore satisfies it.
type FiscalBindingEvidenceStore interface {
	FiscalBindingEvidence(ctx context.Context, subjectType, subjectID string) ([]core.FiscalBindingLinkEvidence, error)
}

// FiscalScopeBindingLayer builds the read-only "fiscal scope binding"
// verification layer for one subject (design.md "Verification and audit"): it
// loads the subject's persisted, audit-anchor-resolved fiscal binding evidence
// and delegates the PURE classification (legacy/unbound, complete, or failed)
// to core.VerifyFiscalScopeBinding. currentEnvelopeHash is the subject's
// CURRENT, already recomputed envelope/content-address hash — the same
// authority the existing evidence/rule availability layers compare a
// committed result against (core.ComputeEnvelopeHash for a memory; the object
// id for an evidence object). A store read failure aborts report building
// (the caller treats it exactly like any other verification I/O error), never
// a silent skip.
func FiscalScopeBindingLayer(ctx context.Context, st FiscalBindingEvidenceStore, subjectType, subjectID, currentEnvelopeHash string) (core.VerificationLayer, error) {
	evidence, err := st.FiscalBindingEvidence(ctx, subjectType, subjectID)
	if err != nil {
		return core.VerificationLayer{}, err
	}
	return core.VerifyFiscalScopeBinding(evidence, currentEnvelopeHash), nil
}

// ValidateFiscalReadIntent is the service-level defense-in-depth check for a
// READ-ONLY protected fiscal operation. It mirrors the store's
// verifyFiscalIntentAxes at the SERVICE boundary: a nil intent always passes;
// a present intent must be a structurally valid v1 binding (core.
// ValidateFiscalScopeBinding) naming the EXACT expectedOperation and matching
// the trusted scope's tenant/organization/company-RUC/period axes. It never
// fills a missing element, never infers a binding from the scope, and never
// treats actor/authorityLevel as authorization — the caller's authenticated
// principal and policy remain the sole authority (proposal.md "Scope metadata
// does not authorize"). Errors are the same typed *core.FiscalScopeError the
// store itself returns, so callers get one consistent SCOPE_BINDING_INVALID/
// SCOPE_MISMATCH vocabulary at every layer.
func ValidateFiscalReadIntent(intent *core.FiscalWriteIntent, expectedOperation string, scope core.Scope) error {
	if intent == nil {
		return nil
	}
	if err := core.ValidateFiscalScopeBinding(intent.Binding); err != nil {
		return err
	}
	if intent.Binding.OperationType != expectedOperation {
		return &core.FiscalScopeError{Code: core.ScopeBindingInvalid, Message: "operationType does not match the invoked protected boundary"}
	}
	if intent.Binding.Tenant != scope.OrganizationID ||
		intent.Binding.Organization != scope.CompanyID ||
		intent.Binding.Company != scope.RUC ||
		(scope.Period != "" && intent.Binding.FiscalPeriod != scope.Period) {
		return &core.FiscalScopeError{Code: core.ScopeMismatch, Message: "binding does not match the trusted scope axes"}
	}
	return nil
}
