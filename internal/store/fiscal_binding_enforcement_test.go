// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the Delivery Slice 3 RED/
// GREEN/TRIANGULATE suite (openspec/changes/fiscal-runtime-foundations):
// save/supersede/evidence-link/rule-link/object-store persist ONE immutable
// fiscal act-evidence link atomically with the act, a v1-bound subject's
// envelope hash transitively commits to it, and a direct store bypass (an old
// tuple or a mismatched/absent binding against an already v1-bound subject)
// fails closed with zero partial state.
package store

import (
	"context"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// fiscalRucA/fiscalRucB are checksum-VALID SUNAT RUCs (unlike the package's
// testRucA/testRucB fixtures, which are deliberately checksum-invalid for
// pre-fiscal legacy tests) — every Slice 3 fiscal-bound test needs a company
// axis that survives core.ValidateFiscalScopeBinding.
const (
	fiscalRucA = "20100070970"
	fiscalRucB = "20600055519"
)

func fiscalIntentFor(operation string, authority core.AuthorityLevel, actor, ruc, period string) *core.FiscalWriteIntent {
	return &core.FiscalWriteIntent{Binding: core.FiscalScopeBinding{
		Version:        "v1",
		Tenant:         testOrgID,
		Organization:   "acme",
		Company:        ruc,
		FiscalPeriod:   period,
		LedgerBook:     "purchases",
		OperationType:  operation,
		SourceSnapshot: strings.Repeat("a", 64),
		PolicyVersion:  "fiscal-v1",
		Actor:          actor,
		AuthorityLevel: authority,
	}}
}

func fiscalRowCounts(t *testing.T, s *SQLiteStore) (bindings, links int) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM fiscal_scope_bindings`).Scan(&bindings); err != nil {
		t.Fatalf("count fiscal_scope_bindings: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM fiscal_binding_links`).Scan(&links); err != nil {
		t.Fatalf("count fiscal_binding_links: %v", err)
	}
	return bindings, links
}

// TestSaveWithFiscalIntentPersistsImmutableActEvidence (RED->GREEN): a Save
// carrying a complete, matching v1 FiscalWriteIntent persists exactly one
// fiscal_scope_bindings row and one fiscal_binding_links row atomically with
// the memory, classifies the subject v1 (Slice 2's ClassifyFiscalSubject) and
// changes the memory's envelope hash relative to the same save without an
// intent (the envelope transitively commits to the immutable act evidence).
func TestSaveWithFiscalIntentPersistsImmutableActEvidence(t *testing.T) {
	// This test needs BOTH a legacy-class save and a v1-class save to
	// coexist in the SAME database (compared via s.ClassifyFiscalSubject
	// below) — no single DRENYRA_FISCAL_RUNTIME_MODE permits both classes at
	// once (design.md "Runtime and downgrade modes"), so the legacy phase
	// and the v1 phase reopen the identical underlying file under the mode
	// each needs (see reopenTestStoreMode's doc comment).
	s, path := newTestStorePathMode(t, "legacy_compat")
	scope := testScope(fiscalRucA)

	legacy := validInput("topic/fiscal/legacy", "legacy save")
	legacy.Scope = scope
	legacySaved, err := s.Save(legacy)
	if err != nil {
		t.Fatalf("legacy save: %v", err)
	}

	s = reopenTestStoreMode(t, s, path, "enforce")
	bound := validInput("topic/fiscal/bound", "bound save")
	bound.Scope = scope
	bound.FiscalIntent = fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	boundSaved, err := s.Save(bound)
	if err != nil {
		t.Fatalf("bound save: %v", err)
	}

	bindings, links := fiscalRowCounts(t, s)
	if bindings != 1 || links != 1 {
		t.Fatalf("bindings=%d links=%d, want 1/1 for one bound save", bindings, links)
	}
	class, err := s.ClassifyFiscalSubject(context.Background(), "memory", boundSaved.Memory.Identity.ID)
	if err != nil || class != FiscalScopeV1 {
		t.Fatalf("class=%q err=%v, want v1", class, err)
	}
	legacyClass, err := s.ClassifyFiscalSubject(context.Background(), "memory", legacySaved.Memory.Identity.ID)
	if err != nil || legacyClass != FiscalScopeLegacy {
		t.Fatalf("legacy class=%q err=%v, want legacy (byte-identical, no fiscal contribution)", legacyClass, err)
	}

	reloaded, ok := s.FindByID(boundSaved.Memory.Identity.ID)
	if !ok {
		t.Fatal("bound memory not found")
	}
	unbound := reloaded
	unbound.FiscalLinks = nil
	if core.ComputeEnvelopeHash(reloaded) == core.ComputeEnvelopeHash(unbound) {
		t.Fatal("bound save produced no envelope-hash contribution — immutable act evidence not linked")
	}
}

// TestSaveFiscalIntentOperationMismatchFailsClosed (TRIANGULATE case 1): a
// Save call carrying an otherwise-structurally-valid binding whose
// operationType names a DIFFERENT protected boundary (memory.supersede
// instead of memory.save) fails closed BEFORE any row, binding or link is
// created.
func TestSaveFiscalIntentOperationMismatchFailsClosed(t *testing.T) {
	s := newTestStoreMode(t, "enforce")
	input := validInput("topic/fiscal/opmismatch", "wrong operation")
	input.Scope = testScope(fiscalRucA)
	input.FiscalIntent = fiscalIntentFor("memory.supersede", core.AuthorityLevelExecute, "agent-1", fiscalRucA, testPeriod)

	if _, err := s.Save(input); err == nil {
		t.Fatal("operation-mismatched fiscal intent must fail closed")
	} else if !strings.Contains(err.Error(), core.ScopeBindingInvalid) && !strings.Contains(err.Error(), core.ScopeMismatch) {
		t.Fatalf("error = %v, want SCOPE_BINDING_INVALID/SCOPE_MISMATCH", err)
	}
	bindings, links := fiscalRowCounts(t, s)
	if bindings != 0 || links != 0 {
		t.Fatalf("bindings=%d links=%d, want 0/0 on a rejected save", bindings, links)
	}
	if _, ok := s.FindByTopicKey("topic/fiscal/opmismatch", testScope(fiscalRucA)); ok {
		t.Fatal("rejected save must not create an observation row")
	}
}

// TestSaveFiscalIntentAxisMismatchFailsClosed (TRIANGULATE case 2 — a
// DIFFERENT failure mode than the operation mismatch above): a structurally
// valid, correctly-operationed binding whose trusted company axis (RUC)
// disagrees with the scope's own RUC fails SCOPE_MISMATCH with no partial
// state and discloses no foreign RUC.
func TestSaveFiscalIntentAxisMismatchFailsClosed(t *testing.T) {
	s := newTestStoreMode(t, "enforce")
	input := validInput("topic/fiscal/axismismatch", "wrong axis")
	input.Scope = testScope(fiscalRucA)
	input.FiscalIntent = fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucB, testPeriod)

	err := func() error { _, err := s.Save(input); return err }()
	if err == nil {
		t.Fatal("axis-mismatched fiscal intent must fail closed")
	}
	if !strings.Contains(err.Error(), core.ScopeMismatch) {
		t.Fatalf("error = %v, want SCOPE_MISMATCH", err)
	}
	if strings.Contains(err.Error(), fiscalRucB) {
		t.Fatal("error must not disclose the foreign RUC")
	}
	bindings, links := fiscalRowCounts(t, s)
	if bindings != 0 || links != 0 {
		t.Fatalf("bindings=%d links=%d, want 0/0 on a rejected save", bindings, links)
	}
}

// TestSupersedeDirectBypassOfV1BoundSubjectDenied is the literal spec
// scenario "Direct store bypass is denied": once a subject carries a fiscal
// binding link (v1-bound), a plain SupersedeExplicit call with NO fiscal
// intent (the legacy/old-tuple caller shape) must fail closed and mutate
// nothing — no status flip, no transition-log row, no relation, no receipt.
func TestSupersedeDirectBypassOfV1BoundSubjectDenied(t *testing.T) {
	// The v1-bound subject and its legacy successor must coexist in the
	// SAME database for the SupersedeExplicit(id, successorID, ...) call
	// below to resolve both ids — reopen the same file across the mode each
	// phase needs (see reopenTestStoreMode's doc comment); the bypass
	// SupersedeExplicit call itself fails via requireFiscalIntentForBoundSubject
	// BEFORE ever reaching the runtime gate, so the mode active at that final
	// call is immaterial.
	s, path := newTestStorePathMode(t, "enforce")
	scope := testScope(fiscalRucA)
	bound := validInput("topic/fiscal/supersede-bypass", "v1 subject")
	bound.Scope = scope
	bound.FiscalIntent = fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	saved, err := s.Save(bound)
	if err != nil {
		t.Fatalf("bound save: %v", err)
	}
	id := saved.Memory.Identity.ID

	s = reopenTestStoreMode(t, s, path, "legacy_compat")
	successor := validInput("topic/fiscal/supersede-successor", "successor")
	successor.Scope = scope
	successorSaved, err := s.Save(successor)
	if err != nil {
		t.Fatalf("save successor: %v", err)
	}

	_, err = s.SupersedeExplicit(id, successorSaved.Memory.Identity.ID, core.TransitionMeta{
		Actor: "agent-1", ActorKind: core.ActorKindAgent, Timestamp: testT,
	})
	if err == nil {
		t.Fatal("a legacy (nil-intent) supersede of a v1-bound subject must fail closed")
	}
	if !strings.Contains(err.Error(), core.ScopeBindingRequired) {
		t.Fatalf("error = %v, want SCOPE_BINDING_REQUIRED", err)
	}
	stillActive, ok := s.FindByID(id)
	if !ok || stillActive.Status != core.StatusActive {
		t.Fatalf("bypassed supersede mutated status: %+v ok=%v", stillActive, ok)
	}
	if _, links := fiscalRowCounts(t, s); links != 1 {
		t.Fatalf("bypass attempt changed fiscal link count: links=%d, want 1 (unchanged)", links)
	}
}

// TestSupersedeMismatchedFiscalIntentDenied (TRIANGULATE — a DIFFERENT bypass
// shape than the nil-intent case above): a supersede carrying a
// STRUCTURALLY VALID but wrong-RUC binding against a v1-bound subject also
// fails closed, proving the guard checks the trusted axes, not merely
// presence.
func TestSupersedeMismatchedFiscalIntentDenied(t *testing.T) {
	// Same coexistence requirement as TestSupersedeDirectBypassOfV1BoundSubjectDenied
	// above — reopen the same file across modes; the mismatched-intent
	// supersede call fails via verifyFiscalIntentAxes BEFORE reaching the
	// runtime gate, so the mode active at that final call is immaterial.
	s, path := newTestStorePathMode(t, "enforce")
	scope := testScope(fiscalRucA)
	bound := validInput("topic/fiscal/supersede-mismatch", "v1 subject")
	bound.Scope = scope
	bound.FiscalIntent = fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	saved, err := s.Save(bound)
	if err != nil {
		t.Fatalf("bound save: %v", err)
	}
	s = reopenTestStoreMode(t, s, path, "legacy_compat")
	successor := validInput("topic/fiscal/supersede-mismatch-successor", "successor")
	successor.Scope = scope
	successorSaved, err := s.Save(successor)
	if err != nil {
		t.Fatalf("save successor: %v", err)
	}

	wrongIntent := fiscalIntentFor("memory.supersede", core.AuthorityLevelPrepare, "agent-1", fiscalRucB, testPeriod)
	_, err = s.SupersedeExplicit(saved.Memory.Identity.ID, successorSaved.Memory.Identity.ID, core.TransitionMeta{
		Actor: "agent-1", ActorKind: core.ActorKindAgent, Timestamp: testT, FiscalIntent: wrongIntent,
	})
	if err == nil || !strings.Contains(err.Error(), core.ScopeMismatch) {
		t.Fatalf("error = %v, want SCOPE_MISMATCH", err)
	}
	stillActive, ok := s.FindByID(saved.Memory.Identity.ID)
	if !ok || stillActive.Status != core.StatusActive {
		t.Fatal("mismatched supersede attempt mutated the subject")
	}
}

// TestSupersedeWithMatchingFiscalIntentAppendsSecondLink proves the positive
// path: a supersede with the correct binding on an already v1-bound subject
// succeeds, appends sequence 2 (H1/H2 linkage), and the pre/post envelope
// hashes recomputed from the reloaded state differ from each other.
func TestSupersedeWithMatchingFiscalIntentAppendsSecondLink(t *testing.T) {
	// Three phases against the SAME underlying file: v1-bound save
	// (enforce), legacy successor save (legacy_compat), then the matching
	// supersede — which, unlike the two bypass tests above, genuinely
	// reaches checkFiscalRuntimeGate and must succeed, so it needs enforce
	// again (class comes from the ALREADY-BOUND subject's existing links,
	// not this call's own intent — see checkFiscalRuntimeGate's doc comment).
	s, path := newTestStorePathMode(t, "enforce")
	scope := testScope(fiscalRucA)
	bound := validInput("topic/fiscal/supersede-ok", "v1 subject")
	bound.Scope = scope
	bound.FiscalIntent = fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	saved, err := s.Save(bound)
	if err != nil {
		t.Fatalf("bound save: %v", err)
	}
	s = reopenTestStoreMode(t, s, path, "legacy_compat")
	successor := validInput("topic/fiscal/supersede-ok-successor", "successor")
	successor.Scope = scope
	successorSaved, err := s.Save(successor)
	if err != nil {
		t.Fatalf("save successor: %v", err)
	}

	s = reopenTestStoreMode(t, s, path, "enforce")
	intent := fiscalIntentFor("memory.supersede", core.AuthorityLevelPrepare, "controller-1", fiscalRucA, testPeriod)
	updated, err := s.SupersedeExplicit(saved.Memory.Identity.ID, successorSaved.Memory.Identity.ID, core.TransitionMeta{
		Actor: "controller-1", ActorKind: core.ActorKindHuman, Timestamp: testT, FiscalIntent: intent,
	})
	if err != nil {
		t.Fatalf("matching supersede must succeed: %v", err)
	}
	if updated.Status != core.StatusSuperseded {
		t.Fatalf("status = %s, want superseded", updated.Status)
	}
	var maxSeq int
	if err := s.db.QueryRow(`SELECT MAX(sequence) FROM fiscal_binding_links WHERE subject_type='memory' AND subject_id=?`, saved.Memory.Identity.ID).Scan(&maxSeq); err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if maxSeq != 2 {
		t.Fatalf("sequence = %d, want 2 (H1 save link + H2 supersede link)", maxSeq)
	}
	class, err := s.ClassifyFiscalSubject(context.Background(), "memory", saved.Memory.Identity.ID)
	if err != nil || class != FiscalScopeV1 {
		t.Fatalf("class=%q err=%v, want v1 (sequence must not gap)", class, err)
	}
}

// TestAddEvidenceLinksBoundAtomicBatch proves the atomic-batch requirement: a
// batch containing ONE invalid ref fails the WHOLE command — zero refs are
// added and zero fiscal evidence is recorded (no partial subset commits).
func TestAddEvidenceLinksBoundAtomicBatch(t *testing.T) {
	// The target memory (legacy scaffold save) and its later v1-bound
	// evidence batch must live in the SAME database — reopen the same file
	// across modes (see reopenTestStoreMode's doc comment). The invalid-ref
	// batch call carries a nil intent, but the target has no existing fiscal
	// links yet, so it still classifies legacy and is unaffected by which
	// mode is active as long as it's a mode that allows legacy (legacy_compat).
	s, path := newTestStorePathMode(t, "legacy_compat")
	input := validInput("topic/fiscal/evidence-batch", "evidence target")
	input.Scope = testScope(fiscalRucA)
	saved, err := s.Save(input)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := s.AddEvidenceLinksBound(saved.Memory.Identity.ID, []string{"evidence/ok-1", "", "evidence/ok-2"}, "agent-1", nil); err == nil {
		t.Fatal("a batch containing an invalid ref must fail entirely")
	}
	refs, err := s.EvidenceRefs(saved.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("evidence refs: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("refs = %v, want none (atomic batch must not commit a partial subset)", refs)
	}

	// TRIANGULATE: a fully valid batch WITH a fiscal intent commits every ref
	// and exactly one fiscal binding link. This call is the target's FIRST
	// fiscal act (v1-classified by its own intent), so it needs enforce.
	s = reopenTestStoreMode(t, s, path, "enforce")
	out, err := s.AddEvidenceLinksBound(saved.Memory.Identity.ID, []string{"evidence/a", "evidence/b"}, "agent-1",
		fiscalIntentFor("evidence.link", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod))
	if err != nil {
		t.Fatalf("valid batch: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("linked refs = %v, want 2", out)
	}
	if _, links := fiscalRowCounts(t, s); links != 1 {
		t.Fatalf("links=%d, want 1 for one bound batch", links)
	}
	class, err := s.ClassifyFiscalSubject(context.Background(), "memory", saved.Memory.Identity.ID)
	if err != nil || class != FiscalScopeV1 {
		t.Fatalf("class=%q err=%v, want v1", class, err)
	}
}

// TestAddEvidenceLinksBoundIdempotentReplayMintsNoPhantomAct is the
// "idempotent replay" Slice 3 acceptance scenario: replaying the EXACT same
// bound batch (identical refs, all already persisted from the first call)
// must be a true no-op for fiscal evidence — INSERT OR IGNORE already makes
// the ref-insert side idempotent, and a phantom fiscal_binding_links row
// (bumping the sequence and envelope hash for a command that persisted no
// new ref) is NOT a real act and must not be minted.
func TestAddEvidenceLinksBoundIdempotentReplayMintsNoPhantomAct(t *testing.T) {
	// Legacy scaffold save, then every subsequent call carries a fiscal
	// intent (v1) — reopen the same file from legacy_compat to enforce (see
	// reopenTestStoreMode's doc comment); one reopen covers the whole v1
	// phase since every later call on this subject stays v1-classified.
	s, path := newTestStorePathMode(t, "legacy_compat")
	input := validInput("topic/fiscal/evidence-replay", "evidence target")
	input.Scope = testScope(fiscalRucA)
	saved, err := s.Save(input)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	s = reopenTestStoreMode(t, s, path, "enforce")
	memoryID := saved.Memory.Identity.ID
	intent := fiscalIntentFor("evidence.link", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)

	if _, err := s.AddEvidenceLinksBound(memoryID, []string{"evidence/a", "evidence/b"}, "agent-1", intent); err != nil {
		t.Fatalf("first bound batch: %v", err)
	}
	_, linksAfterFirst := fiscalRowCounts(t, s)
	if linksAfterFirst != 1 {
		t.Fatalf("links after first batch = %d, want 1", linksAfterFirst)
	}
	afterFirst, ok := s.FindByID(memoryID)
	if !ok {
		t.Fatal("memory not found after first batch")
	}
	envelopeAfterFirst := core.ComputeEnvelopeHash(afterFirst)

	// Replay: identical refs, identical intent — every ref already exists,
	// so INSERT OR IGNORE inserts nothing new.
	out, err := s.AddEvidenceLinksBound(memoryID, []string{"evidence/a", "evidence/b"}, "agent-1", intent)
	if err != nil {
		t.Fatalf("replayed batch must not fail: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("replayed refs = %v, want the same 2 (unchanged)", out)
	}

	_, linksAfterReplay := fiscalRowCounts(t, s)
	if linksAfterReplay != 1 {
		t.Fatalf("links after replay = %d, want STILL 1 (replay minted a phantom act)", linksAfterReplay)
	}
	var maxSeq int
	if err := s.db.QueryRow(`SELECT MAX(sequence) FROM fiscal_binding_links WHERE subject_type='memory' AND subject_id=?`, memoryID).Scan(&maxSeq); err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if maxSeq != 1 {
		t.Fatalf("sequence after replay = %d, want STILL 1 (no phantom sequence advance)", maxSeq)
	}
	afterReplay, ok := s.FindByID(memoryID)
	if !ok {
		t.Fatal("memory not found after replay")
	}
	if core.ComputeEnvelopeHash(afterReplay) != envelopeAfterFirst {
		t.Fatal("envelope hash changed on a pure replay — a phantom act was recorded")
	}

	// TRIANGULATE: a batch that mixes an already-existing ref with a
	// genuinely NEW one still mints exactly one more act (sequence 2) —
	// idempotent replay only suppresses acts that add NOTHING. A DIFFERENT
	// actor is used for this second act: the act-evidence hash is a function
	// of (bindingHash, reviewedEnvelopeHash, tri-state) only — design.md does
	// not fold in ref content — so two acts sharing the IDENTICAL binding are
	// intentionally the same "act identity" under fiscal_binding_links'
	// UNIQUE(subject,binding_hash,act_evidence_hash) constraint (Slice 2
	// schema). A distinct actor changes the binding hash, proving the "new
	// content mints a new act" path without colliding with that constraint.
	secondActorIntent := fiscalIntentFor("evidence.link", core.AuthorityLevelPrepare, "controller-1", fiscalRucA, testPeriod)
	if _, err := s.AddEvidenceLinksBound(memoryID, []string{"evidence/a", "evidence/c"}, "controller-1", secondActorIntent); err != nil {
		t.Fatalf("mixed batch with one new ref: %v", err)
	}
	if err := s.db.QueryRow(`SELECT MAX(sequence) FROM fiscal_binding_links WHERE subject_type='memory' AND subject_id=?`, memoryID).Scan(&maxSeq); err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if maxSeq != 2 {
		t.Fatalf("sequence after a batch with one genuinely new ref = %d, want 2", maxSeq)
	}
}

// TestAddEvidenceLinksBoundDirectBypassDenied: once a memory carries a fiscal
// evidence-link binding, a further legacy (nil-intent) evidence link attempt
// is denied and adds no new ref.
func TestAddEvidenceLinksBoundDirectBypassDenied(t *testing.T) {
	// Legacy scaffold save, then the bound (v1) evidence link — reopen the
	// same file from legacy_compat to enforce (see reopenTestStoreMode's doc
	// comment). The final bypass call (nil intent against a now-bound
	// subject) fails via requireFiscalIntentForBoundSubject BEFORE reaching
	// the runtime gate, so staying in enforce for it is immaterial.
	s, path := newTestStorePathMode(t, "legacy_compat")
	input := validInput("topic/fiscal/evidence-bypass", "evidence target")
	input.Scope = testScope(fiscalRucA)
	saved, err := s.Save(input)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	s = reopenTestStoreMode(t, s, path, "enforce")
	if _, err := s.AddEvidenceLinksBound(saved.Memory.Identity.ID, []string{"evidence/a"}, "agent-1",
		fiscalIntentFor("evidence.link", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)); err != nil {
		t.Fatalf("bound batch: %v", err)
	}
	if _, err := s.AddEvidenceLinksBound(saved.Memory.Identity.ID, []string{"evidence/b"}, "agent-1", nil); err == nil {
		t.Fatal("a legacy bypass of a v1-bound subject's evidence links must fail closed")
	} else if !strings.Contains(err.Error(), core.ScopeBindingRequired) {
		t.Fatalf("error = %v, want SCOPE_BINDING_REQUIRED", err)
	}
	refs, _ := s.EvidenceRefs(saved.Memory.Identity.ID)
	if len(refs) != 1 {
		t.Fatalf("refs = %v, want exactly the first bound ref (bypass added nothing)", refs)
	}
}

// TestStoreObjectWithFiscalIntentPersistsActEvidence proves the object-store
// boundary: a bound StoreObject call persists one fiscal binding link on the
// "evidence_object" subject atomically with the WORM row, and an
// operation-mismatched intent fails BEFORE any byte write, row insert or
// receipt (closed-period-style ordering, the "invalid input before
// reservation/object access" requirement).
func TestStoreObjectWithFiscalIntentPersistsActEvidence(t *testing.T) {
	s := newTestStoreMode(t, "enforce")
	input := objectInputForTest(t, []byte("fiscal-object"))
	input.Scope = testScope(fiscalRucA)

	badIntent := fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	if _, err := s.StoreObjectWithFiscalIntent(context.Background(), input, badIntent); err == nil {
		t.Fatal("operation-mismatched intent must fail closed before any object write")
	}
	objectID := core.ComputeObjectID(input.Bytes)
	if _, ok := s.EvidenceObjectByID(context.Background(), objectID); ok {
		t.Fatal("a rejected bound object store must leave no evidence_objects row")
	}
	if bindings, links := fiscalRowCounts(t, s); bindings != 0 || links != 0 {
		t.Fatalf("bindings=%d links=%d, want 0/0 on rejection", bindings, links)
	}

	goodIntent := fiscalIntentFor("evidence.store", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	result, err := s.StoreObjectWithFiscalIntent(context.Background(), input, goodIntent)
	if err != nil {
		t.Fatalf("bound object store: %v", err)
	}
	if !result.Created {
		t.Fatal("first store of new bytes must report created=true")
	}
	class, err := s.ClassifyFiscalSubject(context.Background(), "evidence_object", objectID)
	if err != nil || class != FiscalScopeV1 {
		t.Fatalf("class=%q err=%v, want v1", class, err)
	}
}

// TestBoundSaveIntoClosedPeriodFailsWithZeroFiscalState is the "PERIOD_CLOSED"
// slice-3 requirement: a bound Save into a CLOSED exact company period still
// fails PERIOD_CLOSED and leaves NO observation, NO fiscal binding and NO
// fiscal link (the existing closed-period gate runs before the new fiscal
// append code).
func TestBoundSaveIntoClosedPeriodFailsWithZeroFiscalState(t *testing.T) {
	// The identity seed + close save/approve are all legacy (no FiscalIntent)
	// and need legacy_compat; the final bound (v1) Save that must reach the
	// PERIOD_CLOSED check (deeper than checkFiscalRuntimeGate in Save) needs
	// enforce — reopen the same file (see reopenTestStoreMode's doc comment)
	// so the closed-period projection persists across the mode switch.
	s, path := newTestStorePathMode(t, "legacy_compat")
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	scope := testScope(fiscalRucA)
	saveAndApproveClose(t, s, scope, "close blocks bound save", "req-fiscal-close")

	s = reopenTestStoreMode(t, s, path, "enforce")
	input := validInput("topic/fiscal/closed-period", "must not land")
	input.Scope = scope
	input.FiscalIntent = fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	_, err := s.Save(input)
	if err == nil || !strings.Contains(err.Error(), "PERIOD_CLOSED") {
		t.Fatalf("error = %v, want PERIOD_CLOSED", err)
	}
	if bindings, links := fiscalRowCounts(t, s); bindings != 0 || links != 0 {
		t.Fatalf("bindings=%d links=%d, want 0/0 (PERIOD_CLOSED must precede any fiscal append)", bindings, links)
	}
	if _, ok := s.FindByTopicKey("topic/fiscal/closed-period", scope); ok {
		t.Fatal("rejected save must not create an observation row")
	}
}

// ──────────────────────────────────────────────
// Delivery Slice 4 — FiscalBindingEvidence (design.md "Verification and
// audit" — audit-anchor resolution). These tests exercise the STORE-side I/O
// loader that internal/server's FiscalScopeBindingLayer/core.
// VerifyFiscalScopeBinding consume; the pure classification itself is fully
// covered at internal/core/verify_test.go.
// ──────────────────────────────────────────────

// TestFiscalBindingEvidenceLegacySubjectHasNoEvidence: a subject with no
// fiscal intent ever supplied has no persisted links — FiscalBindingEvidence
// returns nil/nil (the legacy/unbound input core.VerifyFiscalScopeBinding
// classifies as SKIPPED).
func TestFiscalBindingEvidenceLegacySubjectHasNoEvidence(t *testing.T) {
	s := newTestStore(t)
	legacy := validInput("topic/fiscal/evidence-legacy", "legacy")
	legacy.Scope = testScope(fiscalRucA)
	saved, err := s.Save(legacy)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	evidence, err := s.FiscalBindingEvidence(context.Background(), "memory", saved.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("FiscalBindingEvidence: %v", err)
	}
	if len(evidence) != 0 {
		t.Fatalf("evidence = %+v, want none for a legacy/unbound subject", evidence)
	}
}

// TestFiscalBindingEvidenceResolvesAuditAnchorForBoundSubject: a genuinely
// v1-bound memory save resolves its own "observation" audit anchor (the
// memory row IS the subject that was just created) and every recomputed field
// agrees with core.VerifyFiscalScopeBinding, so the layer PASSES.
func TestFiscalBindingEvidenceResolvesAuditAnchorForBoundSubject(t *testing.T) {
	s := newTestStoreMode(t, "enforce")
	bound := validInput("topic/fiscal/evidence-bound", "bound")
	scope := testScope(fiscalRucA)
	bound.Scope = scope
	bound.FiscalIntent = fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	saved, err := s.Save(bound)
	if err != nil {
		t.Fatalf("bound save: %v", err)
	}

	ctx := context.Background()
	evidence, err := s.FiscalBindingEvidence(ctx, "memory", saved.Memory.Identity.ID)
	if err != nil {
		t.Fatalf("FiscalBindingEvidence: %v", err)
	}
	if len(evidence) != 1 {
		t.Fatalf("evidence = %+v, want exactly one link", evidence)
	}
	if !evidence[0].AuditAnchorResolved {
		t.Fatal("the memory's own observation row must resolve as the audit anchor")
	}

	reloaded, ok := s.FindByID(saved.Memory.Identity.ID)
	if !ok {
		t.Fatal("reloaded memory not found")
	}
	layer := core.VerifyFiscalScopeBinding(evidence, core.ComputeEnvelopeHash(reloaded))
	if layer.Status != core.VerificationPassed {
		t.Fatalf("layer status = %s, detail = %q, want passed", layer.Status, layer.Detail)
	}
}

// TestFiscalBindingEvidenceDetectsUnresolvedAuditAnchor: a SECOND
// fiscal_binding_links row inserted with an audit_ref_id that resolves to no
// persisted "observation" row (the immutability triggers block UPDATE/DELETE
// on existing rows — see the Slice 3 "no update/no delete" schema — so a
// tampered anchor can only be modeled by a plain INSERT of a row that never
// legitimately existed; this is the direct-SQL-bypass shape the design's
// "unresolved audit anchor" failure mode defends against) is detected as
// AuditAnchorResolved=false, and core.VerifyFiscalScopeBinding fails the
// subject closed — never a silent v1 pass.
func TestFiscalBindingEvidenceDetectsUnresolvedAuditAnchor(t *testing.T) {
	s := newTestStoreMode(t, "enforce")
	bound := validInput("topic/fiscal/evidence-tampered", "bound")
	scope := testScope(fiscalRucA)
	bound.Scope = scope
	bound.FiscalIntent = fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod)
	saved, err := s.Save(bound)
	if err != nil {
		t.Fatalf("bound save: %v", err)
	}
	memoryID := saved.Memory.Identity.ID

	binding := fiscalIntentFor("memory.save", core.AuthorityLevelPrepare, "agent-1", fiscalRucA, testPeriod).Binding
	bindingHash := core.FiscalScopeHash(binding)
	// reviewedEnvelopeHash MUST differ from the first (real) act's ("") so this
	// synthetic second act's act_evidence_hash differs too — otherwise it
	// collides with fiscal_binding_links' own
	// UNIQUE(subject_type,subject_id,binding_hash,act_evidence_hash) constraint
	// (the exact Slice 3 "open design question" documented in
	// apply-progress.md), which is a DIFFERENT concern from the "unresolved
	// audit anchor" this test targets.
	reviewedEnvelopeHash := strings.Repeat("c", 64)
	actEvidenceHash := core.ComputeActEvidenceHash(bindingHash, reviewedEnvelopeHash, core.ReviewAcknowledgement{}, core.ReviewAcknowledgement{})
	linkID, err := newUUID()
	if err != nil {
		t.Fatalf("newUUID: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO fiscal_binding_links VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		linkID, "memory", memoryID, 2, bindingHash, actEvidenceHash, reviewedEnvelopeHash, strings.Repeat("b", 64), nil, nil,
		"observation", "an-observation-id-that-was-never-persisted", testT,
	); err != nil {
		t.Fatalf("insert stray fiscal_binding_links row: %v", err)
	}

	ctx := context.Background()
	evidence, err := s.FiscalBindingEvidence(ctx, "memory", memoryID)
	if err != nil {
		t.Fatalf("FiscalBindingEvidence: %v", err)
	}
	if len(evidence) != 2 {
		t.Fatalf("evidence = %+v, want two links", evidence)
	}
	if evidence[1].AuditAnchorResolved {
		t.Fatal("an audit_ref_id naming no persisted observation must not resolve")
	}

	layer := core.VerifyFiscalScopeBinding(evidence, strings.Repeat("b", 64))
	if layer.Status != core.VerificationFailed {
		t.Fatalf("layer status = %s, want failed for an unresolved audit anchor", layer.Status)
	}
}
