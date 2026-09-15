// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the Delivery Slice 5 RED/
// GREEN/TRIANGULATE suite (openspec/changes/fiscal-runtime-foundations,
// design.md "Authenticated approval transaction"): ApproveMemory carries an
// OPTIONAL v1 fiscal binding intent through the existing atomic transaction —
// structural/axis validation before any status change, the direct-store-
// bypass guard applied to the approval boundary, a v1-specific idempotency
// hash that additionally commits to the binding and acknowledgement
// tri-state, and one immutable fiscal_binding_links row appended atomically
// with the approval act. Every case proves zero partial state on rejection:
// no status flip, no fiscal row, no completed idempotency reservation.
package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// approveAck is the table-test shorthand for one tri-state acknowledgement.
func approveAck(present, value bool) core.ReviewAcknowledgement {
	return core.ReviewAcknowledgement{Present: present, Value: value}
}

// bothTrueChecks is the ONLY tri-state that satisfies a material/critical
// approval — both acknowledgements explicitly present and true.
func bothTrueChecks() core.ReviewChecksV1 {
	return core.ReviewChecksV1{EvidenceInspected: approveAck(true, true), RuleInspected: approveAck(true, true)}
}

// fiscalMaterialGatedInput is a material, fiscalEffect=closing save: it lands
// pending_review behind the human gate (gatedInput) AND declares materiality
// so the anti-rubber-stamp clause (REVIEW_CHECKS_REQUIRED) actually applies —
// the scope is overridden to the checksum-VALID fiscalRucA (unlike the
// package's default checksum-invalid testRucA) so a FiscalWriteIntent's
// company axis can legitimately match it.
func fiscalMaterialGatedInput(topicKey, what string) core.SaveInput {
	input := gatedInput(topicKey, what)
	input.Scope = testScope(fiscalRucA)
	material := core.MaterialityMaterial
	input.MaterialityLevel = &material
	return input
}

// fiscalRowsForSubject returns the fiscal_binding_links rows for one memory
// subject, in sequence order, for direct assertion on the columns
// ApproveMemory must populate (reviewedEnvelopeHash, resultingEnvelopeHash,
// the nullable acknowledgement columns).
type fiscalLinkRow struct {
	sequence                                 int
	bindingHash, reviewedHash, resultingHash string
	evidenceInspected, rulesInspected        sql.NullBool
}

func fiscalRowsForSubject(t *testing.T, s *SQLiteStore, memoryID string) []fiscalLinkRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT sequence, binding_hash, reviewed_envelope_hash, resulting_envelope_hash, evidence_inspected, rules_inspected FROM fiscal_binding_links WHERE subject_type='memory' AND subject_id=? ORDER BY sequence`, memoryID)
	if err != nil {
		t.Fatalf("query fiscal_binding_links: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []fiscalLinkRow
	for rows.Next() {
		var r fiscalLinkRow
		if err := rows.Scan(&r.sequence, &r.bindingHash, &r.reviewedHash, &r.resultingHash, &r.evidenceInspected, &r.rulesInspected); err != nil {
			t.Fatalf("scan fiscal_binding_links: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// TestApproveMemoryWithFiscalIntentSucceedsAndPersistsActEvidence (RED->GREEN,
// spec.md "Eligible professional approves exact material envelope"): a
// material approval carrying a matching v1 fiscal intent and both explicit
// positive acknowledgements transitions exactly once and appends exactly one
// immutable fiscal_binding_links row whose reviewedEnvelopeHash is H1,
// resultingEnvelopeHash is H2 (the returned ResultingEnvelopeHash), and whose
// acknowledgement columns are 1/1 (provided true) — never NULL (omitted).
func TestApproveMemoryWithFiscalIntentSucceedsAndPersistsActEvidence(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, err := s.Save(fiscalMaterialGatedInput("topic/fiscal/approve-ok", "material, fiscal-bound approval"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	h1 := currentEnvelope(saved)
	intent := fiscalIntentFor("memory.approve", core.AuthorityLevelExecute, "controller-1", fiscalRucA, testPeriod)

	res, err := s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "reviewed evidence and applicable rules",
		RequestID: "req-fiscal-approve-ok", ReviewChecks: bothTrueChecks(), FiscalIntent: intent,
	}, controllerPrincipal(t), authz.NewApprovalPolicy())
	if err != nil {
		t.Fatalf("fiscal-bound material approval must succeed: %v", err)
	}
	if res.CurrentStatus != string(core.StatusApproved) {
		t.Fatalf("status = %q, want approved", res.CurrentStatus)
	}

	rows := fiscalRowsForSubject(t, s, id)
	if len(rows) != 1 {
		t.Fatalf("fiscal_binding_links rows = %d, want exactly 1", len(rows))
	}
	link := rows[0]
	if link.sequence != 1 {
		t.Fatalf("sequence = %d, want 1 (first fiscal act on this subject)", link.sequence)
	}
	if link.reviewedHash != h1 {
		t.Fatalf("reviewedEnvelopeHash = %q, want H1 %q", link.reviewedHash, h1)
	}
	if link.resultingHash != res.ResultingEnvelopeHash {
		t.Fatalf("resultingEnvelopeHash = %q, want H2 %q", link.resultingHash, res.ResultingEnvelopeHash)
	}
	if !link.evidenceInspected.Valid || !link.evidenceInspected.Bool {
		t.Fatalf("evidence_inspected = %+v, want provided true (never NULL/omitted for an explicit true ack)", link.evidenceInspected)
	}
	if !link.rulesInspected.Valid || !link.rulesInspected.Bool {
		t.Fatalf("rules_inspected = %+v, want provided true", link.rulesInspected)
	}
}

// TestApproveMemoryFiscalReviewChecksTriState (RED->GREEN, spec.md "Missing or
// false acknowledgement is rejected"): table-driven over the tri-state of
// both acknowledgements on a MATERIAL fiscal-bound approval — every
// combination except both-explicit-true fails REVIEW_CHECKS_REQUIRED with
// ZERO side effects (no status flip, no fiscal row, no receipt).
func TestApproveMemoryFiscalReviewChecksTriState(t *testing.T) {
	cases := []struct {
		name           string
		evidence, rule core.ReviewAcknowledgement
		wantSuccess    bool
	}{
		{"both omitted", approveAck(false, false), approveAck(false, false), false},
		{"both explicit false", approveAck(true, false), approveAck(true, false), false},
		{"evidence true, rule omitted", approveAck(true, true), approveAck(false, false), false},
		{"evidence omitted, rule true", approveAck(false, false), approveAck(true, true), false},
		{"both explicit true", approveAck(true, true), approveAck(true, true), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestStore(t)
			seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
			saved, err := s.Save(fiscalMaterialGatedInput("topic/fiscal/tristate/"+c.name, "tri-state case"))
			if err != nil {
				t.Fatalf("save: %v", err)
			}
			id := saved.Memory.Identity.ID
			h1 := currentEnvelope(saved)
			intent := fiscalIntentFor("memory.approve", core.AuthorityLevelExecute, "controller-1", fiscalRucA, testPeriod)

			_, err = s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
				MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "reviewed",
				RequestID: "req-" + c.name, ReviewChecks: core.ReviewChecksV1{EvidenceInspected: c.evidence, RuleInspected: c.rule},
				FiscalIntent: intent,
			}, controllerPrincipal(t), authz.NewApprovalPolicy())

			if c.wantSuccess {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected REVIEW_CHECKS_REQUIRED")
			}
			if auth.Code(err) != auth.CodeReviewChecksRequired {
				t.Fatalf("code = %q, want %q (err %v)", auth.Code(err), auth.CodeReviewChecksRequired, err)
			}
			stillPending, ok := s.FindByID(id)
			if !ok || stillPending.Status != core.StatusPendingReview {
				t.Fatalf("rejected approval mutated status: %+v ok=%v", stillPending, ok)
			}
			if _, links := fiscalRowCounts(t, s); links != 0 {
				t.Fatalf("rejected approval created %d fiscal_binding_links rows, want 0", links)
			}
		})
	}
}

// TestApproveMemoryFiscalIntentOperationMismatchFailsClosed (TRIANGULATE): an
// otherwise-valid binding whose operationType names a DIFFERENT protected
// boundary (memory.save instead of memory.approve) fails closed before any
// status change or fiscal row.
func TestApproveMemoryFiscalIntentOperationMismatchFailsClosed(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, err := s.Save(fiscalMaterialGatedInput("topic/fiscal/approve-opmismatch", "wrong operation"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	h1 := currentEnvelope(saved)
	wrongOp := fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "controller-1", fiscalRucA, testPeriod)

	_, err = s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "reviewed",
		RequestID: "req-approve-opmismatch", ReviewChecks: bothTrueChecks(), FiscalIntent: wrongOp,
	}, controllerPrincipal(t), authz.NewApprovalPolicy())
	if err == nil {
		t.Fatal("operation-mismatched fiscal intent must fail closed")
	}
	if !strings.Contains(err.Error(), core.ScopeBindingInvalid) && !strings.Contains(err.Error(), core.ScopeMismatch) {
		t.Fatalf("error = %v, want SCOPE_BINDING_INVALID/SCOPE_MISMATCH", err)
	}
	stillPending, ok := s.FindByID(id)
	if !ok || stillPending.Status != core.StatusPendingReview {
		t.Fatalf("rejected approval mutated status: %+v ok=%v", stillPending, ok)
	}
	if _, links := fiscalRowCounts(t, s); links != 0 {
		t.Fatalf("rejected approval created %d fiscal_binding_links rows, want 0", links)
	}
}

// TestApproveMemoryFiscalIntentAxisMismatchFailsClosed (TRIANGULATE — a
// DIFFERENT failure mode): a structurally valid, correctly-operationed
// binding whose trusted company axis (RUC) disagrees with the memory's OWN
// scope fails SCOPE_MISMATCH and discloses no foreign RUC.
func TestApproveMemoryFiscalIntentAxisMismatchFailsClosed(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, err := s.Save(fiscalMaterialGatedInput("topic/fiscal/approve-axismismatch", "wrong axis"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	h1 := currentEnvelope(saved)
	wrongAxis := fiscalIntentFor("memory.approve", core.AuthorityLevelExecute, "controller-1", fiscalRucB, testPeriod)

	_, err = s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "reviewed",
		RequestID: "req-approve-axismismatch", ReviewChecks: bothTrueChecks(), FiscalIntent: wrongAxis,
	}, controllerPrincipal(t), authz.NewApprovalPolicy())
	if err == nil || !strings.Contains(err.Error(), core.ScopeMismatch) {
		t.Fatalf("error = %v, want SCOPE_MISMATCH", err)
	}
	if strings.Contains(err.Error(), fiscalRucB) {
		t.Fatal("error must not disclose the foreign RUC")
	}
	stillPending, ok := s.FindByID(id)
	if !ok || stillPending.Status != core.StatusPendingReview {
		t.Fatalf("rejected approval mutated status: %+v ok=%v", stillPending, ok)
	}
}

// TestApproveMemoryDirectBypassDeniedForV1BoundMemory is the literal spec
// scenario "Direct store bypass is denied" applied to the approval boundary:
// once a memory is v1-bound at SAVE time, approving it with NO fiscal intent
// (the legacy caller shape) fails closed and mutates nothing.
func TestApproveMemoryDirectBypassDeniedForV1BoundMemory(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	input := fiscalMaterialGatedInput("topic/fiscal/approve-bypass", "v1-bound at save time")
	input.FiscalIntent = fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	saved, err := s.Save(input)
	if err != nil {
		t.Fatalf("bound save: %v", err)
	}
	id := saved.Memory.Identity.ID
	h1 := currentEnvelope(saved)

	_, err = s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "reviewed",
		RequestID: "req-approve-bypass", ReviewChecks: bothTrueChecks(),
		// FiscalIntent deliberately nil — the legacy/bypass shape.
	}, controllerPrincipal(t), authz.NewApprovalPolicy())
	if err == nil {
		t.Fatal("a legacy (nil-intent) approval of a v1-bound subject must fail closed")
	}
	if !strings.Contains(err.Error(), core.ScopeBindingRequired) {
		t.Fatalf("error = %v, want SCOPE_BINDING_REQUIRED", err)
	}
	stillPending, ok := s.FindByID(id)
	if !ok || stillPending.Status != core.StatusPendingReview {
		t.Fatalf("bypassed approval mutated status: %+v ok=%v", stillPending, ok)
	}
	if _, links := fiscalRowCounts(t, s); links != 1 {
		t.Fatalf("bypass attempt changed fiscal link count: links=%d, want 1 (unchanged from save)", links)
	}
}

// TestApproveCloseMemoryRequiresCloseApproveOperationToken (TRIANGULATE,
// design.md's operation map: memory.approve vs close.approve are DISTINCT
// tokens): a close memory's approval fiscal intent must name "close.approve",
// not "memory.approve" — proving the expected operation token is derived
// from the LOADED subject, not a fixed constant.
func TestApproveCloseMemoryRequiresCloseApproveOperationToken(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(fiscalRucA)

	// CreateClose lives in internal/server (which imports internal/store —
	// importing it here would cycle), so this builds the same VALID
	// close-shaped save (kind=summary, fiscalEffect=closing, topic
	// closing/CIERRE-<period>) directly, matching closeInputForTest's proven
	// pattern from period_closure_test.go (same package).
	saved, err := s.Save(closeInputForTest(t, scope, "cierre mensual"))
	if err != nil {
		t.Fatalf("save close: %v", err)
	}
	id := saved.Memory.Identity.ID
	h1 := currentEnvelope(saved)

	wrongToken := fiscalIntentFor("memory.approve", core.AuthorityLevelExecute, "controller-1", fiscalRucA, testPeriod)
	_, err = s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "cierre revisado",
		RequestID: "req-close-wrong-token", ReviewChecks: bothTrueChecks(), FiscalIntent: wrongToken,
	}, controllerPrincipal(t), authz.NewApprovalPolicy())
	if err == nil {
		t.Fatal("a close approval carrying the memory.approve token must fail closed")
	}
	if !strings.Contains(err.Error(), core.ScopeBindingInvalid) && !strings.Contains(err.Error(), core.ScopeMismatch) {
		t.Fatalf("error = %v, want SCOPE_BINDING_INVALID/SCOPE_MISMATCH", err)
	}

	rightToken := fiscalIntentFor("close.approve", core.AuthorityLevelExecute, "controller-1", fiscalRucA, testPeriod)
	res, err := s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "cierre revisado",
		RequestID: "req-close-right-token", ReviewChecks: bothTrueChecks(), FiscalIntent: rightToken,
	}, controllerPrincipal(t), authz.NewApprovalPolicy())
	if err != nil {
		t.Fatalf("close.approve token must succeed: %v", err)
	}
	if res.CurrentStatus != string(core.StatusApproved) {
		t.Fatalf("status = %q, want approved", res.CurrentStatus)
	}
	closure, ok := s.FindPeriodClosure(scope)
	if !ok || closure.Status != "closed" {
		t.Fatalf("period closure = %+v (ok=%v), want closed", closure, ok)
	}
}

// TestApproveMemoryFiscalIdempotencyConflictOnDifferentBinding (RED->GREEN,
// design.md step 5): a requestId reused with the SAME memory/envelope/reason
// but a DIFFERENT binding hash is rejected as IDEMPOTENCY_CONFLICT rather
// than silently reusing the earlier reservation.
func TestApproveMemoryFiscalIdempotencyConflictOnDifferentBinding(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, err := s.Save(fiscalMaterialGatedInput("topic/fiscal/approve-conflict", "conflict case"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	h1 := currentEnvelope(saved)
	principal := controllerPrincipal(t)
	policy := authz.NewApprovalPolicy()

	first := fiscalIntentFor("memory.approve", core.AuthorityLevelExecute, "controller-1", fiscalRucA, testPeriod)
	if _, err := s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "reviewed",
		RequestID: "req-conflict", ReviewChecks: bothTrueChecks(), FiscalIntent: first,
	}, principal, policy); err != nil {
		t.Fatalf("first approval must succeed: %v", err)
	}

	// A DIFFERENT binding (different actor) under the SAME requestId, same
	// memory/envelope/reason: the v1 hash differs (it commits to the binding
	// hash), so this must be an idempotency conflict, not a silent replay.
	different := fiscalIntentFor("memory.approve", core.AuthorityLevelExecute, "controller-2", fiscalRucA, testPeriod)
	_, err = s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "reviewed",
		RequestID: "req-conflict", ReviewChecks: bothTrueChecks(), FiscalIntent: different,
	}, principal, policy)
	if err == nil || auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Fatalf("code = %q, want %q (err %v)", auth.Code(err), auth.CodeIdempotencyConflict, err)
	}
	if _, links := fiscalRowCounts(t, s); links != 1 {
		t.Fatalf("conflicting replay changed fiscal link count: links=%d, want 1 (only the first act)", links)
	}
}

// TestApproveMemoryFiscalIdempotentReplayReturnsStoredResult (TRIANGULATE): an
// EXACT replay (same requestId, memory, envelope, reason, binding and
// acknowledgements) returns the stored result with IdempotentReplay=true and
// creates no second fiscal_binding_links row.
func TestApproveMemoryFiscalIdempotentReplayReturnsStoredResult(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, err := s.Save(fiscalMaterialGatedInput("topic/fiscal/approve-replay", "replay case"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	h1 := currentEnvelope(saved)
	principal := controllerPrincipal(t)
	policy := authz.NewApprovalPolicy()
	intent := fiscalIntentFor("memory.approve", core.AuthorityLevelExecute, "controller-1", fiscalRucA, testPeriod)

	cmd := core.ApproveMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: h1, Reason: "reviewed",
		RequestID: "req-replay", ReviewChecks: bothTrueChecks(), FiscalIntent: intent,
	}
	first, err := s.ApproveMemory(context.Background(), cmd, principal, policy)
	if err != nil {
		t.Fatalf("first approval must succeed: %v", err)
	}
	if first.IdempotentReplay {
		t.Fatal("the first, fresh approval must not report IdempotentReplay")
	}

	replay, err := s.ApproveMemory(context.Background(), cmd, principal, policy)
	if err != nil {
		t.Fatalf("exact replay must succeed: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("exact replay must report IdempotentReplay=true")
	}
	if replay.ResultingEnvelopeHash != first.ResultingEnvelopeHash {
		t.Fatalf("replay resulting envelope = %q, want %q (same stored result)", replay.ResultingEnvelopeHash, first.ResultingEnvelopeHash)
	}
	if _, links := fiscalRowCounts(t, s); links != 1 {
		t.Fatalf("replay created %d fiscal_binding_links rows, want 1 (no duplicate act)", links)
	}
}
