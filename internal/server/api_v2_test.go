// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test drives the v2 shared domain
// services (approval gate, judge, evidence links, period summary) with the
// killer demo: explainable institutional memory of account 4011's July balance.

package server

import (
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// demoScope is the killer-demo company: cmp_org / cmp_01, RUC 20601234567,
// fiscal period 2026-07.
func demoScope() core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: "cmp_org",
		CompanyID:      "cmp_01",
		RUC:            "20601234567",
		Period:         "202607",
	}
}

// saveDemoFact saves an informative fact (fiscalEffect none → active).
func saveDemoFact(t *testing.T, api *API, topicKey, title, what, effectiveAt string) core.AccountingMemory {
	t.Helper()
	result, err := api.Save(core.SaveInput{
		TopicKey:     topicKey,
		Title:        title,
		Kind:         core.KindFact,
		Scope:        demoScope(),
		Content:      core.Content{What: what, Why: "escenario demo 4011", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  effectiveAt,
		Source:       testAgentSource,
		Confidence:   0.8,
	})
	if err != nil {
		t.Fatalf("save fact %q: %v", title, err)
	}
	return result.Memory
}

// TestPeriodSummaryKillerDemo4011 is the acceptance test of the killer demo:
// "why did account 4011 end with this balance in July?" — the period summary
// must compose an EXPLAINABLE narrative (memoria institucional explicable)
// ordered by accounting-effective date, with the approved adjustment, its rule
// refs, its evidence, and the late-document exception.
func TestPeriodSummaryKillerDemo4011(t *testing.T) {
	api := newTestAPI(t)

	// 1. Saldo inicial de la cuenta 4011 (hecho, informativo).
	saveDemoFact(t, api, "account/4011/opening-balance", "Saldo inicial cuenta 4011",
		"Saldo inicial S/ 31,804.50", "2026-07-01T00:00:00Z")

	// 2. IGV de compras reconocido en el periodo.
	saveDemoFact(t, api, "account/4011/igv-purchases", "IGV de compras reconocido",
		"IGV de compras reconocido S/ 22,417.80", "2026-07-15T00:00:00Z")

	// 3. IGV de ventas del periodo.
	saveDemoFact(t, api, "account/4011/igv-sales", "IGV de ventas",
		"IGV de ventas S/ 7,215.40", "2026-07-20T00:00:00Z")

	// 4. Ajuste AJ-2026-07-019: decision con efecto contable (adjustment) →
	//    pending_review → aprobado por MARÍA TORRES (humana) con la regla
	//    policy/igv/late-document-v3 y evidencias XML/CDR vinculadas.
	ajuste, err := api.Save(core.SaveInput{
		TopicKey:     "adjustment/AJ-2026-07-019",
		Title:        "Ajuste AJ-2026-07-019",
		Kind:         core.KindDecision,
		Scope:        demoScope(),
		Content:      core.Content{What: "Ajuste S/ 1,284.30 por comprobante F001-948 posterior al cierre preliminar", Why: "comprobante tardío afecta el IGV del periodo", Where: "internal/server", Learned: "los comprobantes posteriores al cierre se difieren"},
		FiscalEffect: core.FiscalEffectAdjustment,
		EffectiveAt:  "2026-07-31T12:00:00Z",
		RuleRefs:     []string{"policy/igv/late-document-v3"},
		Source:       testAgentSource,
		Confidence:   0.8,
	})
	if err != nil {
		t.Fatalf("save ajuste: %v", err)
	}
	if ajuste.Memory.Status != core.StatusPendingReview {
		t.Fatalf("ajuste status = %q, want pending_review (fiscal effect gate)", ajuste.Memory.Status)
	}
	approved, err := api.Approve(ajuste.Memory.Identity.ID, humanSource("maria.torres"))
	if err != nil {
		t.Fatalf("approve ajuste: %v", err)
	}
	if approved.Status != core.StatusApproved {
		t.Fatalf("ajuste status = %q, want approved", approved.Status)
	}
	refs, err := api.LinkEvidence(approved.Identity.ID, []string{"xml:F001-948", "cdr:F001-948"}, "maria.torres", demoScope())
	if err != nil {
		t.Fatalf("link evidence: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("evidence refs = %v, want xml + cdr", refs)
	}

	// 5. Excepción: comprobante F001-948 llegó DESPUÉS del cierre (observedAt en
	//    agosto, effectiveAt en julio — el evento posterior que afecta un periodo
	//    previo cerrado).
	late, err := api.Save(core.SaveInput{
		TopicKey:     "exception/F001-948-late",
		Title:        "Comprobante F001-948 diferido",
		Kind:         core.KindException,
		Scope:        demoScope(),
		Content:      core.Content{What: "Comprobante F001-948 ingresó después del cierre; crédito diferido a 2026-08", Why: "evento posterior al cierre del periodo", Where: "internal/server", Learned: "los eventos posteriores afectan el periodo previo cerrado"},
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  "2026-07-31T00:00:00Z",
		ObservedAt:   "2026-08-03T09:00:00Z",
		Source:       testAgentSource,
		Confidence:   0.8,
	})
	if err != nil {
		t.Fatalf("save exception: %v", err)
	}
	_ = late

	// ── El killer demo: el resumen del periodo explica el saldo de 4011 ──
	summary, err := api.PeriodSummary(demoScope())
	if err != nil {
		t.Fatalf("period summary: %v", err)
	}
	if summary.Total != 5 {
		t.Fatalf("summary total = %d, want 5", summary.Total)
	}
	if len(summary.Narrative) != 5 {
		t.Fatalf("narrative length = %d, want 5", len(summary.Narrative))
	}

	// Orden efectivo ASC: saldo inicial → IGV compras → IGV ventas → ajuste →
	// excepción.
	wantOrder := []string{
		"Saldo inicial cuenta 4011",
		"IGV de compras reconocido",
		"IGV de ventas",
		"Comprobante F001-948 diferido",
		"Ajuste AJ-2026-07-019",
	}
	for i, want := range wantOrder {
		if summary.Narrative[i].Title != want {
			t.Fatalf("narrative[%d] = %q, want %q (ordered by effectiveAt ASC)", i, summary.Narrative[i].Title, want)
		}
	}

	// La excepción es un pendiente activo.
	if len(summary.ActiveExceptions) != 1 || summary.ActiveExceptions[0].Title != "Comprobante F001-948 diferido" {
		t.Fatalf("active exceptions = %+v, want the late-document exception", summary.ActiveExceptions)
	}

	// El ajuste aprobado lleva su regla aplicada y sus evidencias.
	approvedMem, err := api.Get(approved.Identity.ID)
	if err != nil {
		t.Fatalf("get ajuste: %v", err)
	}
	if len(approvedMem.RuleRefs) != 1 || approvedMem.RuleRefs[0] != "policy/igv/late-document-v3" {
		t.Fatalf("ajuste ruleRefs = %v, want [policy/igv/late-document-v3]", approvedMem.RuleRefs)
	}
	if !containsStr(approvedMem.EvidenceRefs, "xml:F001-948") || !containsStr(approvedMem.EvidenceRefs, "cdr:F001-948") {
		t.Fatalf("ajuste evidenceRefs = %v, want xml:F001-948 and cdr:F001-948", approvedMem.EvidenceRefs)
	}

	// El narrative es legible y menciona el ajuste.
	if !strings.Contains(summary.NarrativeText, "Ajuste AJ-2026-07-019") {
		t.Fatalf("narrative text must mention the adjustment:\n%s", summary.NarrativeText)
	}
}

// TestPeriodSummaryGateCounts verifies the structured gate view: a gated memory
// awaiting human approval surfaces as pending, never as narrative current fact.
func TestPeriodSummaryGateCounts(t *testing.T) {
	api := newTestAPI(t)
	scope := demoScope()

	if _, err := api.Save(core.SaveInput{
		TopicKey:     "adjustment/pending-001",
		Title:        "Ajuste pendiente",
		Kind:         core.KindDecision,
		Scope:        scope,
		Content:      core.Content{What: "ajuste en espera de aprobación humana", Why: "gate", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: core.FiscalEffectAdjustment,
		EffectiveAt:  "2026-07-25T00:00:00Z",
		Source:       testAgentSource,
		Confidence:   0.8,
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}

	summary, err := api.PeriodSummary(scope)
	if err != nil {
		t.Fatalf("period summary: %v", err)
	}
	if len(summary.PendingApprovals) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(summary.PendingApprovals))
	}
	if summary.ByStatus[core.StatusPendingReview] != 1 {
		t.Fatalf("byStatus[pending_review] = %d, want 1", summary.ByStatus[core.StatusPendingReview])
	}
}

// TestJudgeRecordsProfessionalResolution freezes the DEPRECATED legacy
// caller-declared adjudication path (design §4): since v0.4.0 Step 2 it is
// FAIL-CLOSED. A machine AND a human-without-a-principal both get
// AUTHENTICATION_REQUIRED and NOTHING is written — no decision memory, no
// explains relation. The legacy path cannot adjudicate anymore; confirm/reject
// happen only through the authenticated judgment services.
func TestJudgeRecordsProfessionalResolution(t *testing.T) {
	api := newTestAPI(t)

	conflict := saveDemoFact(t, api, "exception/mismatch-001", "Diferencia de saldo",
		"Diferencia S/ 1,284.30 entre mayor y SIRE", "2026-07-28T00:00:00Z")

	// A machine cannot adjudicate: the fail-closed path answers
	// AUTHENTICATION_REQUIRED (provenance is never authority).
	if _, err := api.Judge(conflict.Identity.ID, "resolución de máquina", testAgentSource); auth.Code(err) != auth.CodeAuthenticationRequired {
		t.Fatalf("machine judge: code = %q, want AUTHENTICATION_REQUIRED; err = %v", auth.Code(err), err)
	}

	// A human WITHOUT a verified principal cannot adjudicate either — the legacy
	// caller-declared actor never becomes a VerifiedApprovalPrincipal.
	if _, err := api.Judge(conflict.Identity.ID, "El comprobante F001-948 llegó tarde; el crédito se difiere a 2026-08.", humanSource("maria.torres")); auth.Code(err) != auth.CodeAuthenticationRequired {
		t.Fatalf("human judge: code = %q, want AUTHENTICATION_REQUIRED; err = %v", auth.Code(err), err)
	}

	// Fail-closed means NO write: no decision memory was created for the
	// conflict topic and no explains relation points at the conflict.
	if _, err := api.GetByTopic("judgment/"+conflict.Identity.ID, conflict.Scope); err == nil {
		t.Fatal("legacy Judge must not write a decision memory; a judgment/ topic exists")
	}

	relations, err := api.Relations()
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	for _, relation := range relations {
		if relation.ToID == conflict.Identity.ID && relation.Relation == core.RelationExplains {
			t.Fatalf("legacy Judge must not write an explains relation to the conflict; found %+v", relation)
		}
	}
}

func containsStr(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
