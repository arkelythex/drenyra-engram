// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the monthly-close
// ACCEPTANCE story at the service boundary
// (docs/architecture/close-intelligence-v0.5.md §9.1-§9.4):
//
//   1. create a July close from a seeded period → pending_review with the frozen
//      pending items/totals in the CloseSnapshot;
//   2. accountant approval is DENIED (ROLE_NOT_AUTHORIZED — closing requires
//      controller); controller approval SUCCEEDS against the reviewed envelope;
//   3. after approval: the closure projection is 'closed' and the memory_closed
//      receipt is on the close chain; an ordinary July save FAILS PERIOD_CLOSED
//      with no new row;
//   4. reopen with the controller → the period is writable again (a save
//      succeeds), the memory_reopened receipt is emitted and the closure event
//      appended; a new close revision re-closes the period on approval.
//
// Receipts are asserted through the store's exported ReceiptsForSubject surface
// with the REAL signer on a temp keyring (the parity-signature proofs live at
// the store boundary).
package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/receipts"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// closeAcceptanceStore opens a store with the REAL receipt signer attached (a
// temp keyring, generated lazily on the first covered act) and wraps it in the
// shared API.
func closeAcceptanceStore(t *testing.T) *API {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "engram.db"))
	if err != nil {
		t.Fatalf("open acceptance store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.SetReceiptSigner(receipts.NewSigner(st, filepath.Join(t.TempDir(), "signing-keys.json")))
	return New(st, "test")
}

// resolvePrincipal mints the REAL verified principal for a seeded session token
// (the same resolver path production middleware uses).
func resolvePrincipal(t *testing.T, api *API, token string) auth.VerifiedApprovalPrincipal {
	t.Helper()
	st := api.Store.(*store.SQLiteStore)
	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	p, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		t.Fatalf("resolve principal: %v", err)
	}
	return p
}

// saveInPeriod saves one memory in the demo July scope with the given kind and
// fiscal effect (none → active; adjustment → pending_review).
func saveInPeriod(t *testing.T, api *API, topicKey, kind, title, what, effectiveAt string) core.AccountingMemory {
	t.Helper()
	effect := core.FiscalEffectNone
	if kind == string(core.KindDecision) {
		effect = core.FiscalEffectAdjustment
	}
	result, err := api.Save(core.SaveInput{
		TopicKey:     topicKey,
		Title:        title,
		Kind:         core.MemoryKind(kind),
		Scope:        demoScope(),
		Content:      core.Content{What: what, Why: "fixture", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: effect,
		EffectiveAt:  effectiveAt,
		Source:       testAgentSource,
	})
	if err != nil {
		t.Fatalf("save fixture %q: %v", topicKey, err)
	}
	return result.Memory
}

// seedJulyPeriod seeds the killer-demo July 2026 scope with the acceptance
// fixtures: an active fact (the total's source), a pending adjustment decision,
// an active obligation, an active exception and a pending fact.
func seedJulyPeriod(t *testing.T, api *API) (sourceFact core.AccountingMemory) {
	t.Helper()
	sourceFact = saveInPeriod(t, api, "account/4011/ventas-julio", "fact", "Ventas de julio", "ventas del periodo", "2026-07-10T00:00:00Z")
	saveInPeriod(t, api, "adjust/aj-001", "decision", "Ajuste AJ-001", "ajuste por comprobante tardio", "2026-07-15T00:00:00Z")
	saveInPeriod(t, api, "obligation/igv-621", "obligation", "Obligacion PDT 621", "declarar IGV julio", "2026-07-20T00:00:00Z")
	saveInPeriod(t, api, "exception/banco-001", "exception", "Diferencia banco", "extracto vs libro", "2026-07-25T00:00:00Z")
	saveInPeriod(t, api, "fact/igv-tasa", "fact", "Tasa IGV", "tasa vigente 18%", "2026-07-05T00:00:00Z")
	return sourceFact
}

// createJulyClose runs the canonical CreateClose service with the two acceptance
// totals (one negative to prove signed int64 cents).
func createJulyClose(t *testing.T, api *API, sourceFact core.AccountingMemory, reason string) core.AccountingMemory {
	t.Helper()
	memory, err := CreateClose(context.Background(), api, demoScope(), core.CreateCloseInput{
		Period: "202607",
		Totals: []core.CloseTotal{
			{Code: "ventas", Currency: "PEN", AmountCents: 450000000, SourceMemoryIDs: []string{sourceFact.Identity.ID}},
			{Code: "notas_credito", Currency: "PEN", AmountCents: -123456, SourceMemoryIDs: []string{sourceFact.Identity.ID}},
		},
		Reason: reason,
		Source: testAgentSource,
	})
	if err != nil {
		t.Fatalf("create close: %v", err)
	}
	return memory
}

// closeReceiptActions returns the ordered receipt actions of the close memory's
// chain (the exported ReceiptsForSubject surface).
func closeReceiptActions(t *testing.T, api *API, closeID string) []string {
	t.Helper()
	st := api.Store.(*store.SQLiteStore)
	receipts, err := st.ReceiptsForSubject(context.Background(), core.SubjectTypeMemory, closeID)
	if err != nil {
		t.Fatalf("receipts for close %s: %v", closeID, err)
	}
	out := make([]string, 0, len(receipts))
	for _, r := range receipts {
		out = append(out, string(r.Action))
	}
	return out
}

// TestCreateCloseFreezesPendingItemsAndTotals is acceptance step 1 (§9.1): the
// created close is pending_review with the canonical topic, month-end effectiveAt
// and the frozen CloseSnapshot (counts, totals with same-scope sources, pending
// item digest, verifiable summary hash).
func TestCreateCloseFreezesPendingItemsAndTotals(t *testing.T) {
	api := closeAcceptanceStore(t)
	sourceFact := seedJulyPeriod(t, api)
	closeMemory := createJulyClose(t, api, sourceFact, "cierre de julio")

	if closeMemory.Status != core.StatusPendingReview {
		t.Fatalf("status = %q, want pending_review (fiscal effect gate)", closeMemory.Status)
	}
	if closeMemory.Kind != core.KindSummary || closeMemory.FiscalEffect != core.FiscalEffectClosing {
		t.Fatalf("kind/effect = %s/%s, want summary/closing", closeMemory.Kind, closeMemory.FiscalEffect)
	}
	if closeMemory.Identity.TopicKey != core.CloseTopicPrefix+"202607" {
		t.Fatalf("topic = %q, want closing/CIERRE-202607", closeMemory.Identity.TopicKey)
	}
	if closeMemory.EffectiveAt != "2026-07-31T23:59:59Z" {
		t.Fatalf("effectiveAt = %q, want month end UTC 2026-07-31T23:59:59Z", closeMemory.EffectiveAt)
	}

	snapshot := closeMemory.CloseSnapshot
	if snapshot == nil {
		t.Fatal("the close memory must carry the structured CloseSnapshot")
	}
	if snapshot.Period != "202607" || snapshot.GeneratedAt == "" {
		t.Fatalf("snapshot period/generatedAt = %q/%q, want 202607 and a timestamp", snapshot.Period, snapshot.GeneratedAt)
	}
	if snapshot.Counts.Total != 5 {
		t.Fatalf("counts.total = %d, want 5 (five fixture memories; the close is excluded)", snapshot.Counts.Total)
	}
	if snapshot.Counts.ByKind["fact"] != 2 || snapshot.Counts.ByKind["decision"] != 1 ||
		snapshot.Counts.ByKind["obligation"] != 1 || snapshot.Counts.ByKind["exception"] != 1 {
		t.Fatalf("counts.byKind = %v, want fact:2 decision:1 obligation:1 exception:1", snapshot.Counts.ByKind)
	}
	if snapshot.Counts.ByStatus["active"] != 4 || snapshot.Counts.ByStatus["pending_review"] != 1 {
		t.Fatalf("counts.byStatus = %v, want active:4 pending_review:1 (the decision; the close itself is not counted)", snapshot.Counts.ByStatus)
	}

	// The frozen totals preserve code/currency/signed cents and the same-scope
	// source memory ids.
	if len(snapshot.Totals) != 2 {
		t.Fatalf("totals = %d, want 2", len(snapshot.Totals))
	}
	if snapshot.Totals[0].Code != "ventas" || snapshot.Totals[0].Currency != "PEN" ||
		snapshot.Totals[0].AmountCents != 450000000 {
		t.Fatalf("total[0] = %+v, want ventas PEN 450000000", snapshot.Totals[0])
	}
	if snapshot.Totals[1].AmountCents != -123456 {
		t.Fatalf("total[1] = %+v, want a negative signed total -123456", snapshot.Totals[1])
	}
	for _, total := range snapshot.Totals {
		if len(total.SourceMemoryIDs) != 1 || total.SourceMemoryIDs[0] != sourceFact.Identity.ID {
			t.Fatalf("total %s source = %v, want [%s]", total.Code, total.SourceMemoryIDs, sourceFact.Identity.ID)
		}
	}

	// The frozen pending-item digest (design §2.2): pending_review memories plus
	// active obligations/exceptions (facts are NOT pending items), deduped,
	// sorted by kind/effectiveAt/memoryId — the close being created is never a
	// pending item. Fixture: the decision (pending_review), the obligation and
	// the exception = 3 items.
	if len(snapshot.PendingItems) != 3 {
		t.Fatalf("pendingItems = %d, want 3 (decision, exception, obligation): %+v", len(snapshot.PendingItems), snapshot.PendingItems)
	}
	wantKinds := []string{"decision", "exception", "obligation"}
	for i, item := range snapshot.PendingItems {
		if item.Kind != wantKinds[i] {
			t.Fatalf("pendingItems[%d].kind = %q, want %q (sorted by kind)", i, item.Kind, wantKinds[i])
		}
		if item.MemoryID == "" || item.Status == "" || item.Title == "" {
			t.Fatalf("pendingItems[%d] is incomplete: %+v", i, item)
		}
	}

	// The narrative memory ids cover the explainable story (facts/decisions/
	// exceptions in current states — obligations are not narrative kinds).
	if len(snapshot.NarrativeMemoryIDs) != 4 {
		t.Fatalf("narrativeMemoryIds = %d, want 4: %v", len(snapshot.NarrativeMemoryIDs), snapshot.NarrativeMemoryIDs)
	}
	if snapshot.Reconciliation != (core.CloseReconciliation{Proposed: 0, Confirmed: 0, Rejected: 0}) {
		t.Fatalf("reconciliation = %+v, want zeros (reconciliation ships in a later batch)", snapshot.Reconciliation)
	}

	// The summary hash is the self-hash of the canonical bytes with the field
	// cleared: clearing + re-hashing reproduces the stamped digest.
	cleared := core.CloneCloseSnapshot(snapshot)
	cleared.SummaryHash = ""
	if want := core.CloseSnapshotSummaryHash(cleared); snapshot.SummaryHash != want {
		t.Fatalf("summaryHash = %q, want %q (self-hash of the canonical bytes)", snapshot.SummaryHash, want)
	}

	// The snapshot round-trips through the store unchanged (canonical bytes are
	// persisted verbatim and participate in the content/envelope hashes).
	got, err := api.Get(closeMemory.Identity.ID)
	if err != nil {
		t.Fatalf("get close: %v", err)
	}
	if got.CloseSnapshot == nil || got.CloseSnapshot.SummaryHash != snapshot.SummaryHash {
		t.Fatal("stored close must round-trip the frozen snapshot")
	}
	if got.ContentHash == "" {
		t.Fatal("the close memory must carry its content hash")
	}
	// The envelope hash cache is derived (recomputed fresh at approval — the
	// stored cache is empty after save by design); it must be CALCULABLE.
	if core.ComputeEnvelopeHash(got) == "" {
		t.Fatal("the close memory envelope hash must be computable")
	}
	if !strings.Contains(got.Content.What, "202607") || !strings.Contains(got.Content.Where, sourceFact.Identity.ID) {
		t.Fatalf("close content must mirror period + source memory ids: %+v", got.Content)
	}

	// No closure projection yet (only approval projects it).
	if _, ok := api.FindPeriodClosure(demoScope()); ok {
		t.Fatal("a pending close must NOT project a period closure")
	}
}

// TestCreateCloseValidations freezes the create-time guards: malformed periods,
// mismatched input/scope periods, non-company scopes, duplicate current closes
// and totals without a verifiable same-scope source.
func TestCreateCloseValidations(t *testing.T) {
	t.Run("invalid period", func(t *testing.T) {
		api := closeAcceptanceStore(t)
		scope := demoScope()
		scope.Period = "202613"
		_, err := CreateClose(context.Background(), api, scope, core.CreateCloseInput{Period: "202613", Source: testAgentSource})
		if !strings.Contains(err.Error(), "INVALID_PERIOD") {
			t.Fatalf("err = %v, want INVALID_PERIOD", err)
		}
	})
	t.Run("period mismatch", func(t *testing.T) {
		api := closeAcceptanceStore(t)
		_, err := CreateClose(context.Background(), api, demoScope(), core.CreateCloseInput{Period: "202608", Source: testAgentSource})
		if auth.Code(err) != auth.CodeInvalidTransition {
			t.Fatalf("code = %q, want INVALID_TRANSITION (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("non-company scope", func(t *testing.T) {
		api := closeAcceptanceStore(t)
		_, err := CreateClose(context.Background(), api, core.Scope{Kind: core.ScopeKindInstitutional}, core.CreateCloseInput{Period: "202607", Source: testAgentSource})
		if !strings.Contains(err.Error(), "INVALID_PERIOD") {
			t.Fatalf("err = %v, want INVALID_PERIOD", err)
		}
	})
	t.Run("duplicate pending close", func(t *testing.T) {
		api := closeAcceptanceStore(t)
		source := saveInPeriod(t, api, "fact/dup-source", "fact", "Fuente", "fuente", "2026-07-01T00:00:00Z")
		createJulyClose(t, api, source, "first")
		_, err := CreateClose(context.Background(), api, demoScope(), core.CreateCloseInput{
			Period: "202607",
			Totals: []core.CloseTotal{{Code: "ventas", Currency: "PEN", AmountCents: 100, SourceMemoryIDs: []string{source.Identity.ID}}},
			Source: testAgentSource,
		})
		if auth.Code(err) != auth.CodePeriodAlreadyClosed {
			t.Fatalf("code = %q, want PERIOD_ALREADY_CLOSED (err: %v)", auth.Code(err), err)
		}
	})
	t.Run("total without source memory", func(t *testing.T) {
		api := closeAcceptanceStore(t)
		_, err := CreateClose(context.Background(), api, demoScope(), core.CreateCloseInput{
			Period: "202607",
			Totals: []core.CloseTotal{{Code: "ventas", Currency: "PEN", AmountCents: 100, SourceMemoryIDs: nil}},
			Source: testAgentSource,
		})
		if !strings.Contains(err.Error(), "INVALID_TOTAL") {
			t.Fatalf("err = %v, want INVALID_TOTAL", err)
		}
	})
	t.Run("total with out-of-scope source", func(t *testing.T) {
		api := closeAcceptanceStore(t)
		otherScope := demoScope()
		otherScope.RUC = "20100039201"
		otherScope.CompanyID = "20100039201"
		otherOut, err := api.Save(core.SaveInput{
			TopicKey: "fact/other-scope", Title: "Otra empresa", Kind: core.KindFact,
			Scope: otherScope, Content: core.Content{What: "x", Why: "y", Where: "z", Learned: "w"},
			FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-07-01T00:00:00Z", Source: testAgentSource,
		})
		if err != nil {
			t.Fatalf("save other-scope fixture: %v", err)
		}
		_, err = CreateClose(context.Background(), api, demoScope(), core.CreateCloseInput{
			Period: "202607",
			Totals: []core.CloseTotal{{Code: "ventas", Currency: "PEN", AmountCents: 100, SourceMemoryIDs: []string{otherOut.Memory.Identity.ID}}},
			Source: testAgentSource,
		})
		if !strings.Contains(err.Error(), "INVALID_TOTAL") {
			t.Fatalf("err = %v, want INVALID_TOTAL (out-of-scope source)", err)
		}
	})
}

// TestCloseApprovalDeniedForAccountant is acceptance step 2 (§9.2, denial half):
// an accountant (same tenant, same company) cannot approve a close — closing
// requires controller, frozen ROLE_NOT_AUTHORIZED, and the close stays
// pending_review.
func TestCloseApprovalDeniedForAccountant(t *testing.T) {
	api := closeAcceptanceStore(t)
	accountantToken := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleAccountant})
	sourceFact := seedJulyPeriod(t, api)
	closeMemory := createJulyClose(t, api, sourceFact, "cierre julio")

	principal := resolvePrincipal(t, api, accountantToken)
	_, err := ApproveMemory(context.Background(), api.Store.(ApprovalStore), authz.NewApprovalPolicy(), core.ApproveMemoryCommand{
		MemoryID:             closeMemory.Identity.ID,
		ExpectedEnvelopeHash: core.ComputeEnvelopeHash(closeMemory),
		Reason:               "revisado por el contador",
		RequestID:            "req-close-acct-denied",
	}, principal)
	if auth.Code(err) != auth.CodeRoleNotAuthorized {
		t.Fatalf("code = %q, want ROLE_NOT_AUTHORIZED (err: %v)", auth.Code(err), err)
	}
	got, _ := api.Get(closeMemory.Identity.ID)
	if got.Status != core.StatusPendingReview {
		t.Fatalf("close status = %q, want pending_review after the denial", got.Status)
	}
	if _, ok := api.FindPeriodClosure(demoScope()); ok {
		t.Fatal("a denied approval must not project a period closure")
	}
}

// TestCloseApprovalReopenReCloseCycle is acceptance steps 2-4 (§9.2-§9.4, the
// controller half): controller approval succeeds against the reviewed envelope,
// the projection closes and memory_closed is emitted; an ordinary July save then
// fails PERIOD_CLOSED with no new row; a controller reopen restores writability,
// emits memory_reopened and appends the closure event; a NEW close revision
// re-closes the period on approval.
func TestCloseApprovalReopenReCloseCycle(t *testing.T) {
	api := closeAcceptanceStore(t)
	controllerToken := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleController})
	sourceFact := seedJulyPeriod(t, api)
	firstClose := createJulyClose(t, api, sourceFact, "cierre de julio")
	principal := resolvePrincipal(t, api, controllerToken)
	st := api.Store.(*store.SQLiteStore)

	// ── §9.2 controller approval succeeds against the reviewed envelope ──
	h1 := core.ComputeEnvelopeHash(firstClose)
	result, err := ApproveMemory(context.Background(), st, authz.NewApprovalPolicy(), core.ApproveMemoryCommand{
		MemoryID:             firstClose.Identity.ID,
		ExpectedEnvelopeHash: h1,
		Reason:               "cierre revisado y conforme",
		RequestID:            "req-close-controller",
	}, principal)
	if err != nil {
		t.Fatalf("controller approval: %v", err)
	}
	if result.ReviewedEnvelopeHash != h1 || result.CurrentStatus != string(core.StatusApproved) {
		t.Fatalf("approval = %+v, want H1 reviewed → approved", result)
	}

	// ── §9.3 closure projection closed + memory_closed receipt ──
	closure, ok := api.FindPeriodClosure(demoScope())
	if !ok || closure.Status != "closed" || closure.CloseMemoryID != firstClose.Identity.ID {
		t.Fatalf("closure = (%v, %v), want closed by %s", closure, ok, firstClose.Identity.ID)
	}
	actions := closeReceiptActions(t, api, firstClose.Identity.ID)
	want := []string{"memory_recorded", "memory_approved", "memory_closed"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("close chain receipts = %v, want %v", actions, want)
	}
	summary, err := api.PeriodSummary(demoScope())
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.ClosureState != "closed" || summary.LatestClose == nil {
		t.Fatalf("summary closureState/latestClose = %q/%v, want closed and the close memory", summary.ClosureState, summary.LatestClose)
	}

	// An ordinary July save FAILS PERIOD_CLOSED with no new row.
	before := len(mustContext(t, api))
	_, err = api.Save(core.SaveInput{
		TopicKey: "tax.after.close", Title: "Pendiente bloqueado", Kind: core.KindFact,
		Scope: demoScope(), Content: core.Content{What: "x", Why: "y", Where: "z", Learned: "w"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-07-31T12:00:00Z", Source: testAgentSource,
	})
	if auth.Code(err) != auth.CodePeriodClosed {
		t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
	}
	if after := len(mustContext(t, api)); after != before {
		t.Fatalf("period memories = %d → %d, want unchanged (the blocked save leaves no row)", before, after)
	}
	closureAfter, _ := api.FindPeriodClosure(demoScope())
	if closureAfter.CloseMemoryID != firstClose.Identity.ID {
		t.Fatalf("closure must keep the first close after the blocked save")
	}

	// ── §9.4 controller reopen → writable again + memory_reopened + event ──
	reopenResult, err := ReopenPeriod(context.Background(), st, authz.NewApprovalPolicy(), demoScope(),
		firstClose.Identity.ID, "correccion de julio", "req-reopen-july", principal)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopenResult.Status != "reopened" || reopenResult.CloseMemoryID != firstClose.Identity.ID {
		t.Fatalf("reopen = %+v, want reopened for the first close", reopenResult)
	}
	actions = closeReceiptActions(t, api, firstClose.Identity.ID)
	want = []string{"memory_recorded", "memory_approved", "memory_closed", "memory_reopened"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("close chain receipts after reopen = %v, want %v", actions, want)
	}
	if _, ok := api.FindPeriodClosure(demoScope()); !ok {
		t.Fatal("closure projection must survive the reopen")
	}
	closure, _ = api.FindPeriodClosure(demoScope())
	if closure.Status != "reopened" {
		t.Fatalf("closure status = %q, want reopened", closure.Status)
	}

	// The period is writable again: an ordinary July save succeeds.
	if _, err := api.Save(core.SaveInput{
		TopicKey: "tax.after.reopen", Title: "Correccion admitida", Kind: core.KindFact,
		Scope: demoScope(), Content: core.Content{What: "x", Why: "y", Where: "z", Learned: "w"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-07-31T12:00:00Z", Source: testAgentSource,
	}); err != nil {
		t.Fatalf("save after reopen must succeed: %v", err)
	}

	// ── §9.4 re-close: a NEW close revision of the same close topic ──
	secondClose := createJulyClose(t, api, sourceFact, "re-cierre de julio")
	if secondClose.Revision != 2 {
		t.Fatalf("re-close revision = %d, want 2 (same close topic chain)", secondClose.Revision)
	}
	h1b := core.ComputeEnvelopeHash(secondClose)
	resultB, err := ApproveMemory(context.Background(), st, authz.NewApprovalPolicy(), core.ApproveMemoryCommand{
		MemoryID:             secondClose.Identity.ID,
		ExpectedEnvelopeHash: h1b,
		Reason:               "re-cierre revisado",
		RequestID:            "req-close-reclose",
	}, principal)
	if err != nil {
		t.Fatalf("re-close approval: %v", err)
	}
	closure, ok = api.FindPeriodClosure(demoScope())
	if !ok || closure.Status != "closed" || closure.CloseMemoryID != secondClose.Identity.ID {
		t.Fatalf("re-close closure = (%v, %v), want closed by %s", closure, ok, secondClose.Identity.ID)
	}
	if closure.CloseApprovalEventID != resultB.ApprovalEventID {
		t.Fatalf("re-close event = %q, want %q", closure.CloseApprovalEventID, resultB.ApprovalEventID)
	}
	// The first close revision is superseded by the re-close revision.
	first, _ := api.Get(firstClose.Identity.ID)
	if first.Status != core.StatusSuperseded {
		t.Fatalf("first close status = %q, want superseded", first.Status)
	}
	// And the period is gated again.
	if _, err := api.Save(core.SaveInput{
		TopicKey: "tax.after.reclose", Title: "Bloqueado de nuevo", Kind: core.KindFact,
		Scope: demoScope(), Content: core.Content{What: "x", Why: "y", Where: "z", Learned: "w"},
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-07-31T12:00:00Z", Source: testAgentSource,
	}); auth.Code(err) != auth.CodePeriodClosed {
		t.Fatalf("code = %q, want PERIOD_CLOSED after the re-close (err: %v)", auth.Code(err), err)
	}
}

// mustContext returns the current memory list of the demo scope (test helper).
func mustContext(t *testing.T, api *API) []core.AccountingMemory {
	t.Helper()
	memories, err := api.Context(demoScope())
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	return memories
}
