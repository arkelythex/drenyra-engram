// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.9.0 REVIEW
// WORKSPACE service contract (docs/architecture/review-workspace-v0.9.md §7):
// command syntax validation, the fail-closed zero-principal guard, the
// principal → provenance mapping, and the delegation of the WHOLE state change
// to ONE atomic store operation (no FindByID + ApplyStatusTransition
// composition anywhere in this path).
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// recordingReviewStore records the delegated calls and returns the configured
// results — the services must hand over the complete state change.
type recordingReviewStore struct {
	queueCalled  bool
	queueQuery   core.ReviewQueueQuery
	queueResult  core.ReviewQueuePage
	queueErr     error
	detailCalled bool
	detailID     string
	detailScope  core.Scope
	detailResult core.ReviewDetail
	detailErr    error
	rejectCalled bool
	rejectCmd    core.RejectMemoryCommand
	rejectP      auth.VerifiedApprovalPrincipal
	rejectResult core.RejectMemoryResult
	rejectErr    error
	returnCalled bool
	returnCmd    core.ReturnMemoryCommand
	returnP      auth.VerifiedApprovalPrincipal
	returnResult core.ReturnMemoryResult
	returnErr    error
}

func (r *recordingReviewStore) ListReviewQueue(ctx context.Context, query core.ReviewQueueQuery) (core.ReviewQueuePage, error) {
	r.queueCalled = true
	r.queueQuery = query
	return r.queueResult, r.queueErr
}

func (r *recordingReviewStore) ReviewDetail(ctx context.Context, memoryID string, scope core.Scope) (core.ReviewDetail, error) {
	r.detailCalled = true
	r.detailID = memoryID
	r.detailScope = scope
	return r.detailResult, r.detailErr
}

func (r *recordingReviewStore) RejectMemory(ctx context.Context, cmd core.RejectMemoryCommand, principal auth.VerifiedApprovalPrincipal) (core.RejectMemoryResult, error) {
	r.rejectCalled = true
	r.rejectCmd = cmd
	r.rejectP = principal
	return r.rejectResult, r.rejectErr
}

func (r *recordingReviewStore) ReturnMemory(ctx context.Context, cmd core.ReturnMemoryCommand, principal auth.VerifiedApprovalPrincipal) (core.ReturnMemoryResult, error) {
	r.returnCalled = true
	r.returnCmd = cmd
	r.returnP = principal
	return r.returnResult, r.returnErr
}

func TestReviewQueueDelegatesWithValidScope(t *testing.T) {
	store := &recordingReviewStore{}
	query := core.ReviewQueueQuery{Scope: demoScope(), Limit: 10, Offset: 5}
	_, err := ReviewQueue(context.Background(), store, query)
	if err != nil {
		t.Fatalf("ReviewQueue: %v", err)
	}
	if !store.queueCalled {
		t.Fatal("ReviewQueue must delegate to the store")
	}
	if store.queueQuery.Scope != query.Scope || store.queueQuery.Limit != 10 || store.queueQuery.Offset != 5 {
		t.Fatalf("delegated query = %+v, want %+v", store.queueQuery, query)
	}
}

func TestReviewQueueRejectsInvalidScope(t *testing.T) {
	store := &recordingReviewStore{}
	_, err := ReviewQueue(context.Background(), store, core.ReviewQueueQuery{})
	if err == nil {
		t.Fatal("ReviewQueue with an empty scope must fail closed")
	}
	if store.queueCalled {
		t.Fatal("an invalid scope must never reach the store")
	}
	if !strings.Contains(err.Error(), "INVALID_SCOPE") {
		t.Fatalf("scope error must carry the frozen INVALID_SCOPE code, got %v", err)
	}
}

func TestReviewDetailValidatesAndDelegates(t *testing.T) {
	store := &recordingReviewStore{}
	_, err := ReviewDetail(context.Background(), store, "", demoScope())
	if err == nil || auth.Code(err) != auth.CodeMemoryNotFound {
		t.Fatalf("empty memoryId = %v, want MEMORY_NOT_FOUND", err)
	}
	if store.detailCalled {
		t.Fatal("an empty memoryId must never reach the store")
	}
	_, err = ReviewDetail(context.Background(), store, "mem-1", core.Scope{})
	if err == nil {
		t.Fatal("an empty scope must fail closed")
	}
	_, err = ReviewDetail(context.Background(), store, "mem-1", demoScope())
	if err != nil {
		t.Fatalf("valid detail: %v", err)
	}
	if !store.detailCalled || store.detailID != "mem-1" || store.detailScope != demoScope() {
		t.Fatalf("delegated detail = %s %+v", store.detailID, store.detailScope)
	}
}

func TestRejectMemoryFailClosedWithoutPrincipal(t *testing.T) {
	store := &recordingReviewStore{}
	_, err := RejectMemory(context.Background(), store, core.RejectMemoryCommand{
		MemoryID: "mem-1", ExpectedEnvelopeHash: "h1", RequestID: "req-1",
	}, auth.VerifiedApprovalPrincipal{})
	if err == nil || auth.Code(err) != auth.CodePrincipalInvalid {
		t.Fatalf("zero principal = %v, want PRINCIPAL_INVALID", err)
	}
	if store.rejectCalled {
		t.Fatal("a zero principal must never reach the store")
	}
}

func TestRejectMemoryCommandSyntax(t *testing.T) {
	store := &recordingReviewStore{}
	p := approvalMustPrincipal(t)
	cases := []struct {
		name string
		cmd  core.RejectMemoryCommand
	}{
		{"empty memoryId", core.RejectMemoryCommand{ExpectedEnvelopeHash: "h", RequestID: "r"}},
		{"empty envelope", core.RejectMemoryCommand{MemoryID: "m", RequestID: "r"}},
		{"empty requestId", core.RejectMemoryCommand{MemoryID: "m", ExpectedEnvelopeHash: "h"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := RejectMemory(context.Background(), store, c.cmd, p); err == nil || auth.Code(err) != auth.CodeMemoryNotFound {
				t.Fatalf("incomplete command = %v, want MEMORY_NOT_FOUND", err)
			}
		})
	}
	if store.rejectCalled {
		t.Fatal("an incomplete command must never reach the store")
	}
}

func TestRejectMemoryDelegates(t *testing.T) {
	store := &recordingReviewStore{}
	p := approvalMustPrincipal(t)
	cmd := core.RejectMemoryCommand{
		MemoryID: "mem-1", ExpectedEnvelopeHash: "h1", Reason: "fuera de alcance", RequestID: "req-1",
	}
	if _, err := RejectMemory(context.Background(), store, cmd, p); err != nil {
		t.Fatalf("RejectMemory: %v", err)
	}
	if !store.rejectCalled {
		t.Fatal("RejectMemory must delegate to the store")
	}
	if store.rejectCmd != cmd {
		t.Fatalf("delegated command = %+v, want %+v", store.rejectCmd, cmd)
	}
	if store.rejectP.SubjectID() != p.SubjectID() {
		t.Fatalf("delegated principal = %s, want %s", store.rejectP.SubjectID(), p.SubjectID())
	}
}

func TestReturnMemoryFailClosedWithoutPrincipalAndRequiresReason(t *testing.T) {
	store := &recordingReviewStore{}
	_, err := ReturnMemory(context.Background(), store, core.ReturnMemoryCommand{
		MemoryID: "mem-1", ExpectedEnvelopeHash: "h1", Reason: "x", RequestID: "req-1",
	}, auth.VerifiedApprovalPrincipal{})
	if err == nil || auth.Code(err) != auth.CodePrincipalInvalid {
		t.Fatalf("zero principal = %v, want PRINCIPAL_INVALID", err)
	}
	p := approvalMustPrincipal(t)
	_, err = ReturnMemory(context.Background(), store, core.ReturnMemoryCommand{
		MemoryID: "mem-1", ExpectedEnvelopeHash: "h1", RequestID: "req-1",
	}, p)
	if err == nil || auth.Code(err) != auth.CodeReasonRequired {
		t.Fatalf("empty return reason = %v, want REASON_REQUIRED", err)
	}
	if store.returnCalled {
		t.Fatal("an incomplete return command must never reach the store")
	}
}

func TestReturnMemoryDelegates(t *testing.T) {
	store := &recordingReviewStore{}
	p := approvalMustPrincipal(t)
	cmd := core.ReturnMemoryCommand{
		MemoryID: "mem-1", ExpectedEnvelopeHash: "h1", Reason: "falta evidencia del CDR", RequestID: "req-1",
	}
	if _, err := ReturnMemory(context.Background(), store, cmd, p); err != nil {
		t.Fatalf("ReturnMemory: %v", err)
	}
	if !store.returnCalled || store.returnCmd != cmd {
		t.Fatalf("delegated return = %+v (called %v), want %+v", store.returnCmd, store.returnCalled, cmd)
	}
}
