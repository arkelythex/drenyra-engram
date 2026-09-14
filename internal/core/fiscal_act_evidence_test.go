package core

import "testing"

func fiscalTestBindingForEvidence(actor string) FiscalScopeBinding {
	return FiscalScopeBinding{
		Version: "v1", Tenant: "tenant-a", Organization: "company-a", Company: "20100070970",
		FiscalPeriod: "202401", LedgerBook: "purchases", OperationType: "memory.save",
		SourceSnapshot: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PolicyVersion:  "fiscal-v1", Actor: actor, AuthorityLevel: AuthorityLevelPrepare,
	}
}

// TestComputeActEvidenceHashDistinguishesTriState proves the ordered tri-state
// of both acknowledgements participates in the act-evidence hash: omitted,
// false and true are three DIFFERENT canonical states even though omitted and
// false lead to the same policy outcome elsewhere (design.md).
func TestComputeActEvidenceHashDistinguishesTriState(t *testing.T) {
	bindingHash := FiscalScopeHash(fiscalTestBindingForEvidence("agent-1"))
	omitted := ReviewAcknowledgement{}
	explicitFalse := ReviewAcknowledgement{Present: true, Value: false}
	explicitTrue := ReviewAcknowledgement{Present: true, Value: true}

	hOmitted := ComputeActEvidenceHash(bindingHash, "", omitted, omitted)
	hFalse := ComputeActEvidenceHash(bindingHash, "", explicitFalse, omitted)
	hTrue := ComputeActEvidenceHash(bindingHash, "", explicitTrue, omitted)

	if hOmitted == hFalse || hOmitted == hTrue || hFalse == hTrue {
		t.Fatalf("tri-state collision: omitted=%s false=%s true=%s", hOmitted, hFalse, hTrue)
	}
	// Determinism: same inputs -> same hash.
	if again := ComputeActEvidenceHash(bindingHash, "", omitted, omitted); again != hOmitted {
		t.Fatalf("non-deterministic: %s != %s", again, hOmitted)
	}
}

// TestComputeActEvidenceHashBindsReviewedEnvelope proves the reviewed
// envelope hash (H1) participates: two acts sharing the same binding and
// checks but reviewing a DIFFERENT envelope produce different act-evidence
// hashes (triangulation vs the tri-state test above, which held H1 fixed).
func TestComputeActEvidenceHashBindsReviewedEnvelope(t *testing.T) {
	bindingHash := FiscalScopeHash(fiscalTestBindingForEvidence("agent-1"))
	checks := ReviewAcknowledgement{Present: true, Value: true}
	h1 := ComputeActEvidenceHash(bindingHash, "envelope-aaa", checks, checks)
	h2 := ComputeActEvidenceHash(bindingHash, "envelope-bbb", checks, checks)
	if h1 == h2 {
		t.Fatal("act-evidence hash ignored the reviewed envelope hash")
	}
}

// TestFiscalLinksEnvelopeContributionEmptyForLegacy proves a nil/empty link
// slice contributes nothing, so ComputeEnvelopeHash on a legacy memory is
// byte-identical whether or not the fiscal binding feature exists at all —
// the frozen receipt-continuity guarantee.
func TestFiscalLinksEnvelopeContributionEmptyForLegacy(t *testing.T) {
	if got := fiscalLinksEnvelopeContribution(nil); got != "" {
		t.Fatalf("nil links contributed %q, want empty", got)
	}
	if got := fiscalLinksEnvelopeContribution([]FiscalBindingLinkContribution{}); got != "" {
		t.Fatalf("empty links contributed %q, want empty", got)
	}
}

// TestComputeEnvelopeHashChangesWithFiscalLinks proves a v1-bound subject's
// envelope hash differs from the SAME memory without links (design.md: the
// envelope transitively commits to immutable fiscal act evidence), and that
// changing ANY one link element (sequence, binding hash or act-evidence
// hash) changes the resulting envelope hash (every-element drift,
// triangulated across the three fields).
func TestComputeEnvelopeHashChangesWithFiscalLinks(t *testing.T) {
	base := AccountingMemory{
		Identity:     Identity{ID: "mem-1", TopicKey: "topic/x"},
		Scope:        Scope{Kind: ScopeKindCompany, OrganizationID: "org-1", CompanyID: "acme", RUC: "20100070970", Period: "202401"},
		Status:       StatusActive,
		FiscalEffect: FiscalEffectNone,
		Source:       Source{System: "go-test", ActorID: "agent", ActorKind: ActorKindAgent},
		RecordedAt:   "2024-01-01T00:00:00Z",
		EffectiveAt:  "2024-01-01T00:00:00Z",
		ContentHash:  "content-hash-x",
		Revision:     1,
	}
	legacyHash := ComputeEnvelopeHash(base)

	link := FiscalBindingLinkContribution{Sequence: 1, BindingHash: "binding-hash-a", ActEvidenceHash: "act-hash-a"}
	withLink := base
	withLink.FiscalLinks = []FiscalBindingLinkContribution{link}
	boundHash := ComputeEnvelopeHash(withLink)

	if boundHash == legacyHash {
		t.Fatal("envelope hash unchanged after adding a fiscal binding link")
	}

	// Drift the binding hash only.
	driftBinding := withLink
	driftBinding.FiscalLinks = []FiscalBindingLinkContribution{{Sequence: 1, BindingHash: "binding-hash-b", ActEvidenceHash: "act-hash-a"}}
	if ComputeEnvelopeHash(driftBinding) == boundHash {
		t.Fatal("envelope hash ignored a changed binding hash in the fiscal link")
	}

	// Drift the act-evidence hash only.
	driftAct := withLink
	driftAct.FiscalLinks = []FiscalBindingLinkContribution{{Sequence: 1, BindingHash: "binding-hash-a", ActEvidenceHash: "act-hash-b"}}
	if ComputeEnvelopeHash(driftAct) == boundHash {
		t.Fatal("envelope hash ignored a changed act-evidence hash in the fiscal link")
	}

	// Drift the sequence only.
	driftSeq := withLink
	driftSeq.FiscalLinks = []FiscalBindingLinkContribution{{Sequence: 2, BindingHash: "binding-hash-a", ActEvidenceHash: "act-hash-a"}}
	if ComputeEnvelopeHash(driftSeq) == boundHash {
		t.Fatal("envelope hash ignored a changed sequence in the fiscal link")
	}

	// A second link appended changes the hash again (cumulative chain).
	twoLinks := withLink
	twoLinks.FiscalLinks = []FiscalBindingLinkContribution{link, {Sequence: 2, BindingHash: "binding-hash-c", ActEvidenceHash: "act-hash-c"}}
	if ComputeEnvelopeHash(twoLinks) == boundHash {
		t.Fatal("envelope hash unchanged after appending a second fiscal link")
	}
}
