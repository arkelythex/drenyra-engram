// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the Delivery Slice 5
// TRIANGULATE audit/verification assertion (openspec/changes/
// fiscal-runtime-foundations, tasks.md: "audit/verification assertions for
// trusted actor, RUC, period, reason, receipt continuity, and 'Accounting
// correctness: NOT ASSERTED'"): a memory approved with a v1 fiscal intent
// must be reported by the EXISTING offline verification layer (wired in
// Slice 4) as a passing, hash-consistent fiscal act — with zero changes to
// verify.go/verify_service.go needed for this slice, since the approval act
// reuses the SAME fiscal_binding_links schema and "observation" audit anchor
// as every other protected write.
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// TestVerifyMemoryPassesForFiscalBoundApproval: a memory saved WITHOUT a
// fiscal intent, then approved WITH one, accumulates exactly one fiscal act;
// VerifyMemory's fiscal layer passes with "1 act(s)" and the report still
// ends with the mandatory non-authorization conclusion.
func TestVerifyMemoryPassesForFiscalBoundApproval(t *testing.T) {
	// The initial save carries no FiscalIntent (legacy-classified, needs
	// legacy_compat); the approval that follows carries one (v1-classified,
	// needs enforce) and must succeed — reopen the same underlying file
	// across the mode each phase needs (see closeAcceptanceStore's and
	// internal/store's reopenTestStoreMode doc comments).
	api, path, keysPath := closeAcceptanceStorePathMode(t, "legacy_compat")
	const tenantID, companyID, ruc, period = "t-fiscal-verify", "c-fiscal-verify", "20100070970", "202401"
	token := seedApprovalIdentity(t, api, tenantID, companyID, ruc, []auth.AccountingRole{auth.RoleController})
	controller := resolvePrincipal(t, api, token)
	scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: tenantID, CompanyID: companyID, RUC: ruc, Period: period}

	material := core.MaterialityMaterial
	saved, err := api.Save(core.SaveInput{
		TopicKey:         "verify/fiscal-approve",
		Title:            "material entry",
		Kind:             core.KindDecision,
		Scope:            scope,
		Content:          core.Content{What: "w", Why: "y", Where: "Peru", Learned: "l"},
		FiscalEffect:     core.FiscalEffectJournalEntry,
		EffectiveAt:      "2024-01-15T00:00:00.000Z",
		MaterialityLevel: &material,
		Source:           core.Source{System: "verify-test", ActorID: "agent-1", ActorKind: core.ActorKindAgent},
		Confidence:       0.8,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	h1 := core.ComputeEnvelopeHash(saved.Memory)

	api = reopenCloseAcceptanceStoreMode(t, api, path, keysPath, "enforce")
	intent := &core.FiscalWriteIntent{Binding: core.FiscalScopeBinding{
		Version: "v1", Tenant: tenantID, Organization: companyID, Company: ruc, FiscalPeriod: period,
		LedgerBook: "purchases", OperationType: "memory.approve", SourceSnapshot: strings.Repeat("a", 64),
		PolicyVersion: "fiscal-v1", Actor: "maria.torres", AuthorityLevel: core.AuthorityLevelExecute,
	}}
	trueAck := core.ReviewAcknowledgement{Present: true, Value: true}
	_, err = ApproveMemory(context.Background(), api.Store.(ApprovalStore), authz.NewApprovalPolicy(), core.ApproveMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "reviewed evidence and applicable rules",
		RequestID:    "req-verify-fiscal-approve",
		ReviewChecks: core.ReviewChecksV1{EvidenceInspected: trueAck, RuleInspected: trueAck},
		FiscalIntent: intent,
	}, controller)
	if err != nil {
		t.Fatalf("fiscal-bound approval: %v", err)
	}

	report, err := VerifyMemory(context.Background(), api.Store.(*store.SQLiteStore), id)
	if err != nil {
		t.Fatalf("VerifyMemory: %v", err)
	}
	if report.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("outcome = %s, want passed (%+v)", report.Outcome, report.Layers)
	}
	fiscal := findLayer(t, report.Layers, core.LayerFiscalScopeBinding)
	if fiscal.Status != core.VerificationPassed || !strings.Contains(fiscal.Detail, "1 act(s)") {
		t.Fatalf("fiscal layer = %+v, want passed with 1 act", fiscal)
	}
	if report.AccountingCorrectness != core.AccountingCorrectnessNotAsserted {
		t.Fatalf("conclusion = %q, want %q", report.AccountingCorrectness, core.AccountingCorrectnessNotAsserted)
	}
}
