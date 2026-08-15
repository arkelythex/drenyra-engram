// Reconstructible close fixture — design brief §7.
//
// The deterministic monthly-close drill proves the §7.1 product promise WITHOUT
// the original agent: a material balance is explainable as
//
//	balance → external ledger reference → entry/adjustment → evidence object
//	(XML/CDR via the v0.7 WORM object store) → applicable rule + vigencia →
//	judgment → professional approval → offline-verifiable provenance.
//
// Demo boundary (§7.2/§7.3): the tenant and RUC are FICTIONAL (20100039201 is
// a checksummed test RUC, not a real taxpayer), evidence ingestion is MANUAL
// through the object store, ledger references are frozen strings, and NOTHING
// here claims SUNAT/ERP ingestion — no production integration is implemented.
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

// Shared fixture identity/helpers live in fixture_helpers_test.go (same
// package): fixtureOrg, fixtureScope, ruleFixtureScope, ledger4011Ref,
// ruleRetentionV2. This file keeps only the fixture drill itself.

// approveFixtureMemory approves ONE memory through the real authenticated
// service against the exact reviewed envelope (post-link), with idempotency and
// the review-checks for high-risk approvals.
func approveFixtureMemory(t *testing.T, api *API, principal auth.VerifiedApprovalPrincipal, mem core.AccountingMemory, requestID string, checks core.ReviewChecks) {
	t.Helper()
	res, err := ApproveMemory(context.Background(), api.Store.(ApprovalStore), authz.NewApprovalPolicy(), core.ApproveMemoryCommand{
		MemoryID:             mem.Identity.ID,
		ExpectedEnvelopeHash: core.ComputeEnvelopeHash(mem),
		Reason:               "reviewed by the professional (fixture)",
		RequestID:            requestID,
		ReviewChecks:         checks,
	}, principal)
	if err != nil {
		t.Fatalf("approve %s: %v", mem.Identity.ID, err)
	}
	if res.CurrentStatus != string(core.StatusApproved) {
		t.Fatalf("approve %s status = %q, want approved", mem.Identity.ID, res.CurrentStatus)
	}
}

func TestReconstructibleCloseFixture(t *testing.T) {
	ctx := context.Background()
	api := closeAcceptanceStore(t)

	// The professional: MARÍA TORRES, controller. Every proposer below is an
	// AGENT — the SoD clause (reviewer ≠ proposer) holds end to end.
	controllerToken := seedApprovalIdentity(t, api, fixtureOrg, fixtureCompany, fixtureRUC, []auth.AccountingRole{auth.RoleController})
	controller := resolvePrincipal(t, api, controllerToken)

	// ── 1. Versioned fiscal rule: v1 superseded by v2 (vigencia window) ──
	if _, err := api.Save(core.SaveInput{
		TopicKey: ruleRetentionV2, Title: "Retention rate v1", Kind: core.KindRule,
		Scope:        fixtureScope(),
		Content:      core.Content{What: "Retention rate 3 percent from 2026-01", Why: "rule v1", Where: "fixture", Learned: "superseded by v2"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-01T00:00:00Z",
		Validity: &core.Validity{EffectiveAt: "2026-01-01T00:00:00Z", ExpiresAt: "2026-03-31T23:59:59Z", Source: "declared"},
		Source:   testAgentSource,
	}); err != nil {
		t.Fatalf("save rule v1: %v", err)
	}
	v2, err := api.Save(core.SaveInput{
		TopicKey: ruleRetentionV2, Title: "Retention rate v2", Kind: core.KindRule,
		Scope:        fixtureScope(),
		Content:      core.Content{What: "Retention rate 4 percent from 2026-04", Why: "rule v2 supersedes v1", Where: "fixture", Learned: "current rule"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-04-01T00:00:00Z",
		Validity: &core.Validity{EffectiveAt: "2026-04-01T00:00:00Z", Source: "declared"},
		Source:   testAgentSource,
	})
	if err != nil {
		t.Fatalf("save rule v2: %v", err)
	}
	ruleChain, err := api.Chain(ruleRetentionV2, fixtureScope())
	if err != nil || len(ruleChain) != 2 {
		t.Fatalf("rule chain = %d revisions (%v), want 2", len(ruleChain), err)
	}
	if ruleChain[0].Status != core.StatusSuperseded || ruleChain[1].Identity.ID != v2.Memory.Identity.ID {
		t.Fatalf("rule chain must show v1 superseded and v2 head: %+v", ruleChain)
	}

	// ── 2. Manual evidence ingestion — XML + CDR via the v0.7 WORM store ──
	invoice947XML, err := api.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       []byte("<Invoice><ID>F001-947</ID><Total>2241780</Total></Invoice>"),
		ContentType: "application/xml", Scope: fixtureScope(), Source: testAgentSource,
	})
	if err != nil {
		t.Fatalf("store invoice F001-947 XML: %v", err)
	}
	invoice947CDR, err := api.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       []byte("CDR:F001-947:ACEPTADO:hash:0b1a"),
		ContentType: "application/octet-stream", Scope: fixtureScope(), Source: testAgentSource,
	})
	if err != nil {
		t.Fatalf("store invoice F001-947 CDR: %v", err)
	}
	invoice948XML, err := api.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       []byte("<Invoice><ID>F001-948</ID><Total>128430</Total></Invoice>"),
		ContentType: "application/xml", Scope: fixtureScope(), Source: testAgentSource,
	})
	if err != nil {
		t.Fatalf("store invoice F001-948 XML: %v", err)
	}
	invoice948CDR, err := api.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       []byte("CDR:F001-948:ACEPTADO:hash:2c3d"),
		ContentType: "application/octet-stream", Scope: fixtureScope(), Source: testAgentSource,
	})
	if err != nil {
		t.Fatalf("store invoice F001-948 CDR: %v", err)
	}
	// The object id IS the SHA-256 of the bytes; a read returns the exact bytes.
	gotBytes, err := fixtureObjectBytes(ctx, api, invoice947XML.Object.ObjectID)
	if err != nil {
		t.Fatalf("read evidence object: %v", err)
	}
	if string(gotBytes) != "<Invoice><ID>F001-947</ID><Total>2241780</Total></Invoice>" {
		t.Fatalf("object bytes mismatch — WORM read must return the exact stored bytes")
	}

	// ── 3. Opening balance (fact, informative) with the frozen ledger ref ──
	if _, err := api.Save(core.SaveInput{
		TopicKey: "account/4011/opening-balance", Title: "Opening balance 4011", Kind: core.KindFact,
		Scope:        fixtureScope(),
		Content:      core.Content{What: "Opening balance account 4011 S/ 31804.50 — " + ledger4011Ref, Why: "ledger opening", Where: "fixture", Learned: "source of truth: " + ledger4011Ref},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-01T00:00:00Z",
		Source: testAgentSource,
	}); err != nil {
		t.Fatalf("save opening balance: %v", err)
	}

	// ── 4a. IGV compras (journal_entry → pending_review → approved) ──
	compras, err := api.Save(core.SaveInput{
		TopicKey: "entry/4011/igv-compras", Title: "IGV purchases for the period", Kind: core.KindDecision,
		Scope:        fixtureScope(),
		Content:      core.Content{What: "IGV purchases S/ 22417.80 — invoice F001-947", Why: "purchases of the period", Where: "fixture", Learned: "evidence F001-947"},
		FiscalEffect: core.FiscalEffectJournalEntry, EffectiveAt: "2026-01-15T00:00:00Z",
		RuleRefs: []string{ruleRetentionV2},
		Source:   testAgentSource,
	})
	if err != nil {
		t.Fatalf("save compras entry: %v", err)
	}
	if _, err := api.LinkEvidence(compras.Memory.Identity.ID, []string{invoice947XML.Object.ObjectID}, "test-agent", fixtureScope()); err != nil {
		t.Fatalf("link F001-947 XML evidence: %v", err)
	}
	if _, err := api.LinkEvidence(compras.Memory.Identity.ID, []string{invoice947CDR.Object.ObjectID}, "test-agent", fixtureScope()); err != nil {
		t.Fatalf("link F001-947 CDR evidence: %v", err)
	}
	comprasMem, err := api.Get(compras.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("get compras entry: %v", err)
	}
	approveFixtureMemory(t, api, controller, comprasMem, "req-approve-compras", core.ReviewChecks{})

	// ── 4b. Comprobante tardío F001-948 (adjustment, MATERIAL, late event) ──
	// effectiveAt en enero, observedAt en febrero — el evento posterior que
	// afecta un periodo ya cerrado (triple timestamp).
	matLvl := core.MaterialityMaterial
	lateAdj, err := api.Save(core.SaveInput{
		TopicKey: "adjustment/F001-948-late", Title: "Late comprobante adjustment F001-948", Kind: core.KindDecision,
		Scope: fixtureScope(),
		Content: core.Content{
			What:  "Adjustment S/ 1284.30 — F001-948 credit recognized in January despite arriving after the close",
			Why:   "comprobante arriving after the preliminary close (triple timestamp: effectiveAt January, observedAt February)",
			Where: "fixture", Learned: "late comprobantes are recognized in the period of the underlying fact",
		},
		FiscalEffect: core.FiscalEffectAdjustment, EffectiveAt: "2026-01-31T12:00:00Z", ObservedAt: "2026-02-03T09:00:00Z",
		MaterialityLevel: &matLvl,
		RuleRefs:         []string{ruleRetentionV2},
		Source:           testAgentSource,
	})
	if err != nil {
		t.Fatalf("save late adjustment: %v", err)
	}
	if _, err := api.LinkEvidence(lateAdj.Memory.Identity.ID, []string{invoice948XML.Object.ObjectID}, "test-agent", fixtureScope()); err != nil {
		t.Fatalf("link F001-948 XML evidence: %v", err)
	}
	if _, err := api.LinkEvidence(lateAdj.Memory.Identity.ID, []string{invoice948CDR.Object.ObjectID}, "test-agent", fixtureScope()); err != nil {
		t.Fatalf("link F001-948 CDR evidence: %v", err)
	}
	lateMem, err := api.Get(lateAdj.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("get late adjustment: %v", err)
	}

	// ── 4c. Exception: the same comprobante deferred to February (informative) ──
	// CONTRADICTS adjustment 4b: does the credit apply in January or February?
	excep, err := api.Save(core.SaveInput{
		TopicKey: "exception/F001-948-late", Title: "Deferred comprobante F001-948", Kind: core.KindException,
		Scope:        fixtureScope(),
		Content:      core.Content{What: "Comprobante F001-948 arrived after close; credit deferred to 2026-02", Why: "event after the period close", Where: "fixture", Learned: "contradicts the January adjustment"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-31T00:00:00Z", ObservedAt: "2026-02-03T09:00:00Z",
		Source: testAgentSource,
	})
	if err != nil {
		t.Fatalf("save late exception: %v", err)
	}

	// ── 5. Contradicción adjudicada: el profesional CONFIRMA que el credito
	// aplica en enero (el ajuste prevalece sobre la excepción). ──
	proposed, err := ProposeJudgment(ctx, api.Store.(JudgmentStore), core.ProposeJudgmentCommand{
		FromID:    lateAdj.Memory.Identity.ID,
		ToID:      excep.Memory.Identity.ID,
		Relation:  core.RelationContradicts,
		Reason:    "el ajuste reconoce F001-948 en enero; la excepcion lo difiere a febrero",
		RequestID: "req-judge-948-1",
	}, testAgentSource)
	if err != nil {
		t.Fatalf("propose contradiction: %v", err)
	}
	confirmed, err := ConfirmJudgment(ctx, api.Store.(JudgmentStore), authz.NewJudgmentPolicy(), core.ConfirmJudgmentCommand{
		JudgmentID:           proposed.JudgmentID,
		Resolution:           "The F001-948 credit applies in January: the economic fact belongs to the period (adjustment prevails)",
		ExpectedJudgmentHash: core.ComputeJudgmentHash(proposed.Judgment),
		RequestID:            "req-judge-confirm-948-1",
	}, controller)
	if err != nil {
		t.Fatalf("confirm contradiction: %v", err)
	}
	if confirmed.Judgment.Status != core.JudgmentConfirmed {
		t.Fatalf("judgment status = %q, want confirmed", confirmed.Judgment.Status)
	}

	// ── Aprobar el ajuste MATERIAL (anti-rubber-stamp: ambas review checks) ──
	approveFixtureMemory(t, api, controller, lateMem, "req-approve-late", core.ReviewChecks{EvidenceInspected: true, RuleInspected: true})

	// ── 6. Cierre mensual + aprobación del controller → periodo cerrado ──
	closeMem, err := CreateClose(ctx, api, fixtureScope(), core.CreateCloseInput{
		Period: fixturePeriod,
		Totals: []core.CloseTotal{{
			Code: "igv-4011", Currency: "PEN", AmountCents: 6272200,
			SourceMemoryIDs: []string{comprasMem.Identity.ID, lateMem.Identity.ID},
		}},
		Reason: "cierre mensual enero 2026",
		Source: testAgentSource,
	})
	if err != nil {
		t.Fatalf("create close: %v", err)
	}
	approveFixtureMemory(t, api, controller, closeMem, "req-approve-close", core.ReviewChecks{})
	closure, ok := api.FindPeriodClosure(fixtureScope())
	if !ok || closure.Status != "closed" {
		t.Fatalf("period closure = %+v (ok=%v), want closed", closure, ok)
	}
	// Un save tardío en el periodo cerrado falla PERIOD_CLOSED (write gate).
	_, err = api.Save(core.SaveInput{
		TopicKey: "late/save-after-close", Title: "tarde", Kind: core.KindFact,
		Scope: fixtureScope(), Content: core.Content{What: "despues del cierre", Why: "x", Where: "fixture", Learned: "x"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-31T23:59:00Z", Source: testAgentSource,
	})
	if err == nil || !strings.Contains(err.Error(), auth.CodePeriodClosed) {
		t.Fatalf("late save after close = %v, want PERIOD_CLOSED", err)
	}

	// ── 7. Verificación offline de la cadena ──
	memReport, err := VerifyMemory(ctx, api.Store.(*store.SQLiteStore), lateAdj.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("verify memory: %v", err)
	}
	if memReport.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("memory verification outcome = %q, want passed: %+v", memReport.Outcome, memReport.Layers)
	}
	if memReport.AccountingCorrectness != core.AccountingCorrectnessNotAsserted {
		t.Fatalf("accountingCorrectness = %q, want %q (crypto integrity never asserts accounting correctness)", memReport.AccountingCorrectness, core.AccountingCorrectnessNotAsserted)
	}
	judReport, err := VerifyJudgment(ctx, api.Store.(*store.SQLiteStore), confirmed.JudgmentID)
	if err != nil {
		t.Fatalf("verify judgment: %v", err)
	}
	if judReport.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("judgment verification outcome = %q, want passed", judReport.Outcome)
	}

	// ── 8. Reconstrucción SIN el agente original: el resumen explica 4011 ──
	summary, err := api.PeriodSummary(fixtureScope())
	if err != nil {
		t.Fatalf("period summary: %v", err)
	}
	joined := summary.NarrativeText + "\n" + strings.Join(func() []string {
		out := make([]string, 0, len(summary.Narrative))
		for _, n := range summary.Narrative {
			out = append(out, n.Title)
		}
		return out
	}(), "\n")
	for _, want := range []string{"Opening balance 4011", "IGV purchases for the period", "Late comprobante adjustment F001-948", "Deferred comprobante F001-948"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("reconstruction narrative must mention %q:\n%s", want, joined)
		}
	}
	// La regla vigente resuelve a v2 (la v1 quedó superseded en la cadena).
	headRule, err := api.GetByTopic(ruleRetentionV2, fixtureScope())
	if err != nil || headRule.Identity.ID != v2.Memory.Identity.ID {
		t.Fatalf("rule resolution = %+v (err=%v), want v2 head", headRule, err)
	}
	// El ajuste aprobado conserva evidencia objetual + regla + juicio confirmado.
	adj, err := api.Get(lateAdj.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("get adjustment: %v", err)
	}
	if !containsStr(adj.EvidenceRefs, invoice948XML.Object.ObjectID) || !containsStr(adj.EvidenceRefs, invoice948CDR.Object.ObjectID) {
		t.Fatalf("adjustment evidence refs = %v, want the F001-948 object ids", adj.EvidenceRefs)
	}
	if len(adj.RuleRefs) != 1 || adj.RuleRefs[0] != ruleRetentionV2 {
		t.Fatalf("adjustment rule refs = %v, want [%s]", adj.RuleRefs, ruleRetentionV2)
	}
}

// fixtureObjectBytes reads one stored evidence object and returns its bytes.
func fixtureObjectBytes(ctx context.Context, api *API, objectID string) ([]byte, error) {
	_, bytes, err := api.GetObject(ctx, objectID, fixtureScope())
	return bytes, err
}
