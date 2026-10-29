// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the Delivery Slice 4 RED/
// GREEN/TRIANGULATE suite for internal/server/fiscal_scope_service.go:
// ValidateFiscalReadIntent's service-level defense-in-depth axis comparison,
// and FiscalScopeBindingLayer's I/O orchestration over a fake store.
package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// fiscalServiceTestBinding is a complete, structurally VALID v1 binding
// fixture — a checksum-valid RUC shared with the store package's Slice 3
// fixtures (fiscalRucA) — the starting point every test case mutates from.
func fiscalServiceTestBinding() core.FiscalScopeBinding {
	return core.FiscalScopeBinding{
		Version:        "v1",
		Tenant:         "t9",
		Organization:   "c9",
		Company:        "20100070970", // checksum-valid SUNAT RUC
		FiscalPeriod:   "202401",
		LedgerBook:     "purchases",
		OperationType:  "context.read",
		SourceSnapshot: strings.Repeat("a", 64),
		PolicyVersion:  "fiscal-v1",
		Actor:          "agent-1",
		AuthorityLevel: core.AuthorityLevelAsk,
	}
}

func fiscalServiceTestScope() core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: "t9",
		CompanyID:      "c9",
		RUC:            "20100070970",
		Period:         "202401",
	}
}

// TestValidateFiscalReadIntentNilAlwaysPasses (spec.md "Missing or unknown
// binding data fails closed" implies the converse too: a caller that never
// supplies a binding at all must not be forced through the v1 path — every
// legacy/unbound read stays reachable).
func TestValidateFiscalReadIntentNilAlwaysPasses(t *testing.T) {
	if err := ValidateFiscalReadIntent(nil, "context.read", fiscalServiceTestScope()); err != nil {
		t.Fatalf("nil intent must always pass: %v", err)
	}
}

// TestValidateFiscalReadIntentMatchingBindingPasses is the positive case: a
// structurally valid binding naming the EXACT invoked operation and matching
// every trusted scope axis passes.
func TestValidateFiscalReadIntentMatchingBindingPasses(t *testing.T) {
	intent := &core.FiscalWriteIntent{Binding: fiscalServiceTestBinding()}
	if err := ValidateFiscalReadIntent(intent, "context.read", fiscalServiceTestScope()); err != nil {
		t.Fatalf("matching intent must pass: %v", err)
	}
}

// TestValidateFiscalReadIntentOperationMismatchFailsClosed (design.md
// operation map): a structurally valid binding naming a DIFFERENT protected
// boundary than the one actually invoked fails SCOPE_BINDING_INVALID.
func TestValidateFiscalReadIntentOperationMismatchFailsClosed(t *testing.T) {
	binding := fiscalServiceTestBinding()
	binding.OperationType = "review.queue"
	binding.AuthorityLevel = core.AuthorityLevelAsk
	intent := &core.FiscalWriteIntent{Binding: binding}
	err := ValidateFiscalReadIntent(intent, "context.read", fiscalServiceTestScope())
	var scopeErr *core.FiscalScopeError
	if !errors.As(err, &scopeErr) || scopeErr.Code != core.ScopeBindingInvalid {
		t.Fatalf("err = %v, want SCOPE_BINDING_INVALID", err)
	}
}

// TestValidateFiscalReadIntentAxisMismatchFailsClosedWithoutDisclosure (spec.md
// "Cross-scope probes disclose nothing"): a structurally valid binding naming
// the RIGHT operation but a DIFFERENT tenant/organization/company/period than
// the trusted scope fails SCOPE_MISMATCH, and the error text never echoes the
// foreign value verbatim.
func TestValidateFiscalReadIntentAxisMismatchFailsClosedWithoutDisclosure(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*core.FiscalScopeBinding)
	}{
		{"tenant", func(b *core.FiscalScopeBinding) { b.Tenant = "foreign-tenant-should-not-leak" }},
		{"organization", func(b *core.FiscalScopeBinding) { b.Organization = "foreign-org-should-not-leak" }},
		{"company", func(b *core.FiscalScopeBinding) { b.Company = "20600055519" }}, // a DIFFERENT checksum-valid RUC
		{"period", func(b *core.FiscalScopeBinding) { b.FiscalPeriod = "202402" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			binding := fiscalServiceTestBinding()
			tc.mutate(&binding)
			intent := &core.FiscalWriteIntent{Binding: binding}
			err := ValidateFiscalReadIntent(intent, "context.read", fiscalServiceTestScope())
			var scopeErr *core.FiscalScopeError
			if !errors.As(err, &scopeErr) || scopeErr.Code != core.ScopeMismatch {
				t.Fatalf("err = %v, want SCOPE_MISMATCH", err)
			}
			if strings.Contains(err.Error(), "foreign-tenant-should-not-leak") || strings.Contains(err.Error(), "foreign-org-should-not-leak") {
				t.Fatalf("error must not disclose the foreign axis value: %v", err)
			}
		})
	}
}

// fakeFiscalBindingEvidenceStore is a minimal FiscalBindingEvidenceStore test
// double: it returns exactly the evidence/error the test configures, with no
// real I/O.
type fakeFiscalBindingEvidenceStore struct {
	evidence []core.FiscalBindingLinkEvidence
	err      error
}

func (f fakeFiscalBindingEvidenceStore) FiscalBindingEvidence(context.Context, string, string) ([]core.FiscalBindingLinkEvidence, error) {
	return f.evidence, f.err
}

// TestFiscalScopeBindingLayerPropagatesStoreErrors: a store read failure
// aborts report building — the caller must treat it exactly like any other
// verification I/O error, never a silent skip or a fabricated pass.
func TestFiscalScopeBindingLayerPropagatesStoreErrors(t *testing.T) {
	wantErr := errors.New("boom")
	_, err := FiscalScopeBindingLayer(context.Background(), fakeFiscalBindingEvidenceStore{err: wantErr}, "memory", "m1", "envelope-hash")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

// TestFiscalScopeBindingLayerDelegatesClassificationToPureLayer: with no
// store error, the layer is exactly core.VerifyFiscalScopeBinding's result
// for the returned evidence — a legacy/unbound subject (no evidence) yields
// SKIPPED, never FAILED or a fabricated PASSED.
func TestFiscalScopeBindingLayerDelegatesClassificationToPureLayer(t *testing.T) {
	layer, err := FiscalScopeBindingLayer(context.Background(), fakeFiscalBindingEvidenceStore{}, "memory", "m1", "envelope-hash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if layer.Status != core.VerificationSkipped || layer.Name != core.LayerFiscalScopeBinding {
		t.Fatalf("layer = %+v, want skipped fiscal scope binding (legacy/unbound)", layer)
	}
}
