// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test proves the fix for
// review-d630ac6a761a3e19's R3-1 (BLOCKER, reliability): a second
// addLinksBound call under the SAME binding on the SAME subject, adding
// GENUINELY NEW refs (not a replay — see
// TestAddEvidenceLinksBoundIdempotentReplayMintsNoPhantomAct in
// fiscal_binding_enforcement_test.go for the replay case), must persist the
// new refs instead of discarding them behind a raw UNIQUE constraint error.
package store

import (
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

func TestAddEvidenceLinksBoundSecondBatchSameBindingPersistsNewRefs(t *testing.T) {
	s, path := newTestStorePathMode(t, "legacy_compat")
	input := validInput("topic/fiscal/evidence-second-batch", "evidence target")
	input.Scope = testScope(fiscalRucA)
	saved, err := s.Save(input)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	s = reopenTestStoreMode(t, s, path, "enforce")
	memoryID := saved.Memory.Identity.ID
	intent := fiscalIntentFor("evidence.link", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)

	if _, err := s.AddEvidenceLinksBound(memoryID, []string{"evidence/a"}, "agent-1", intent); err != nil {
		t.Fatalf("first batch: %v", err)
	}

	// Second batch: SAME intent/binding, but genuinely NEW refs — must not be
	// discarded by a fiscal_binding_links UNIQUE(subject,binding,act_evidence)
	// collision, since ComputeActEvidenceHash for a non-approval act folds in
	// no ref content and is therefore identical to the first batch's.
	refs, err := s.AddEvidenceLinksBound(memoryID, []string{"evidence/b"}, "agent-1", intent)
	if err != nil {
		t.Fatalf("second batch must succeed, not discard the new ref behind a raw constraint error: %v", err)
	}
	if len(refs) != 2 || refs[0] != "evidence/a" || refs[1] != "evidence/b" {
		t.Fatalf("refs after second batch = %v, want [evidence/a evidence/b]", refs)
	}

	memory, ok := s.FindByID(memoryID)
	if !ok {
		t.Fatal("memory not found after second batch")
	}
	if len(memory.EvidenceRefs) != 2 {
		t.Fatalf("persisted EvidenceRefs = %v, want both refs retained", memory.EvidenceRefs)
	}
}
