// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money.
//
// v0.4.0 Step 4 — store read surface for offline verification: ordered receipt
// chains, hash/id lookups, canonical payload retrieval, provenance resolution
// and current evidence/rule link refs. All fixtures use the deterministic
// parity signer so the emitted receipts are byte-stable.

package store

import (
	"context"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// approveFixture stores a gated memory via the parity signer, then approves it
// with a controller principal, returning the store, the memory id and the two
// ordered receipts (recorded, approved).
func approveFixture(t *testing.T) (*SQLiteStore, string, []storedReceipt) {
	t.Helper()
	s, _ := receiptStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})

	wr, err := s.Save(gatedInput("verify/approved", "base difiere"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := wr.Memory.Identity.ID

	_, err = s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
		MemoryID:             id,
		ExpectedEnvelopeHash: currentEnvelope(wr),
		Reason:               "revisado contra XML y CDR",
		RequestID:            "req-verify-approve",
	}, controllerPrincipal(t), authz.NewApprovalPolicy())
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	receipts := readReceipts(t, s)
	if len(receipts) != 2 {
		t.Fatalf("receipts = %d, want 2 (recorded, approved)", len(receipts))
	}
	return s, id, receipts
}

func TestReceiptsForSubjectOrdered(t *testing.T) {
	s, id, receipts := approveFixture(t)

	ctx := context.Background()
	got, err := s.ReceiptsForSubject(ctx, core.SubjectTypeMemory, id)
	if err != nil {
		t.Fatalf("ReceiptsForSubject: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chain = %d receipts, want 2", len(got))
	}
	// Ordered: memory_recorded first, memory_approved second.
	if got[0].Action != core.ReceiptActionMemoryRecorded {
		t.Fatalf("first action = %s, want memory_recorded", got[0].Action)
	}
	if got[1].Action != core.ReceiptActionMemoryApproved {
		t.Fatalf("second action = %s, want memory_approved", got[1].Action)
	}
	// The second receipt chains on the first.
	if got[1].PreviousReceiptHash != receipts[0].ReceiptHash {
		t.Fatalf("approved.previous = %s, want recorded.receipt_hash %s", got[1].PreviousReceiptHash, receipts[0].ReceiptHash)
	}
}

func TestReceiptByHashAndID(t *testing.T) {
	s, _, receipts := approveFixture(t)

	ctx := context.Background()
	byHash, err := s.ReceiptByHash(ctx, receipts[0].ReceiptHash)
	if err != nil {
		t.Fatalf("ReceiptByHash: %v", err)
	}
	if byHash.Action != core.ReceiptActionMemoryRecorded {
		t.Fatalf("by hash action = %s, want memory_recorded", byHash.Action)
	}
	if byHash.PayloadHash != receipts[0].PayloadHash {
		t.Fatalf("by hash payload = %s, want %s", byHash.PayloadHash, receipts[0].PayloadHash)
	}

	// rowid 1 = the first inserted receipt (memory_recorded, genesis).
	byID, err := s.ReceiptByID(ctx, 1)
	if err != nil {
		t.Fatalf("ReceiptByID: %v", err)
	}
	if byID.Action != core.ReceiptActionMemoryRecorded {
		t.Fatalf("by id action = %s, want memory_recorded", byID.Action)
	}

	if _, err := s.ReceiptByHash(ctx, "deadbeef"); err == nil {
		t.Fatal("ReceiptByHash with unknown hash must error")
	}
}

func TestReceiptPayloadByHash(t *testing.T) {
	s, _, receipts := approveFixture(t)

	ctx := context.Background()
	payloadJSON, storedHash, rowID, err := s.ReceiptPayloadByHash(ctx, receipts[1].ReceiptHash)
	if err != nil {
		t.Fatalf("ReceiptPayloadByHash: %v", err)
	}
	if storedHash != receipts[1].ReceiptHash {
		t.Fatalf("stored hash = %s, want receipt hash %s", storedHash, receipts[1].ReceiptHash)
	}
	if rowID < 1 {
		t.Fatalf("row id = %d, want a positive internal id", rowID)
	}
	if len(payloadJSON) == 0 {
		t.Fatal("payload JSON must not be empty")
	}
}

func TestEvidenceAndRuleLinkRefs(t *testing.T) {
	s, id, _ := approveFixture(t)

	ctx := context.Background()
	if err := s.AddEvidenceLink(id, "xml:FA01-0001", "auditor"); err != nil {
		t.Fatalf("add evidence link: %v", err)
	}
	if err := s.AddRuleLink(id, "policy/igv/late-document-v3", "auditor"); err != nil {
		t.Fatalf("add rule link: %v", err)
	}

	ev, err := s.EvidenceLinkRefs(ctx, id)
	if err != nil {
		t.Fatalf("EvidenceLinkRefs: %v", err)
	}
	if len(ev) != 1 || ev[0] != "xml:FA01-0001" {
		t.Fatalf("evidence refs = %v, want [xml:FA01-0001]", ev)
	}

	rules, err := s.RuleLinkRefs(ctx, id)
	if err != nil {
		t.Fatalf("RuleLinkRefs: %v", err)
	}
	if len(rules) != 1 || rules[0] != "policy/igv/late-document-v3" {
		t.Fatalf("rule refs = %v, want [policy/igv/late-document-v3]", rules)
	}
}

func TestReceiptActProvenanceResolvedFromApprovalEvent(t *testing.T) {
	s, id, receipts := approveFixture(t)

	ctx := context.Background()
	provenance, ok, err := s.ReceiptActProvenance(ctx, core.SubjectTypeMemory, id, core.ReceiptActionMemoryApproved, receipts[1].IssuedAt, "")
	if err != nil {
		t.Fatalf("ReceiptActProvenance: %v", err)
	}
	if !ok {
		t.Fatal("approval provenance must resolve")
	}
	if provenance.PrincipalID != "subject-1" {
		t.Fatalf("principal = %q, want subject-1 (seedAcmeIdentity subject)", provenance.PrincipalID)
	}
	if provenance.Policy != authz.PolicyVersion {
		t.Fatalf("policy = %q, want %q", provenance.Policy, authz.PolicyVersion)
	}
}
