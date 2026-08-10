// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.6.0 structured
// rule-link behavior (docs/architecture/fiscal-policy-memory-v0.6.md §2.2):
// the atomic Save integration (validate + dedupe the transport-only list, pin
// every link to its immutable rule-memory version at the decision time, same
// transaction as the memory insert), the read surface (RuleLinks on stored
// memories), the post-save AddRuleLinkVersion API (closed-period gate, target
// validation, conflict discipline, envelope refresh only when the bare ref is
// new), the typed failure matrix and the cross-process concurrency proof (two
// writers pinning different versions → exactly one commit + one
// RULE_LINK_VERSION_CONFLICT).
package store

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ruleContent is the structured content of every rule-link fixture.
func ruleContent() core.Content {
	return core.Content{What: "decision content", Why: "applies the pinned rule", Where: "Peru", Learned: "fixture"}
}

// ruleInput builds a kind=rule save under a stable topic with PE jurisdiction
// metadata and a declared vigencia window (the design §8 acceptance fixture).
func ruleInput(t *testing.T, topicKey string) core.SaveInput {
	t.Helper()
	in := validInput(topicKey, "rule with declared vigencia")
	in.Kind = core.KindRule
	in.EffectiveAt = "2026-01-01T00:00:00Z"
	in.Validity = &core.Validity{
		EffectiveAt: "2026-01-01T00:00:00Z",
		ExpiresAt:   "2026-08-01T00:00:00Z",
		Source:      "declared",
	}
	in.PolicyRule = &core.PolicyRule{
		Jurisdiction: "PE",
		Legislation:  "NATIONAL-TAX",
		Authority:    "National tax authority",
		Tags:         []string{"indirect-tax", "late-document"},
	}
	return in
}

// decisionInput builds a kind=decision save at the given accounting time.
func decisionInput(t *testing.T, topicKey, effectiveAt string) core.SaveInput {
	t.Helper()
	in := validInput(topicKey, "decision applying a rule")
	in.Kind = core.KindDecision
	in.EffectiveAt = effectiveAt
	in.Content = ruleContent()
	in.FiscalEffect = core.FiscalEffectNone
	return in
}

// mustRuleLink runs AddRuleLinkVersion and fails the test on error.
func mustRuleLink(t *testing.T, s *SQLiteStore, memoryID string, link core.RuleLink, actor string) {
	t.Helper()
	if err := s.AddRuleLinkVersion(memoryID, link, actor); err != nil {
		t.Fatalf("AddRuleLinkVersion: %v", err)
	}
}

// TestSaveWithStructuredRuleLinkAtomically is design §8 acceptance 1+2: save a
// rule v1 under a stable topic with PE jurisdiction + a declared vigencia, then
// ATOMICALLY save a decision whose structured link pins v1 at the decision
// EffectiveAt. The bare ref is derived into RuleRefs (the envelope hashes it),
// the structured row lands in the SAME transaction, and the read surface
// returns the link.
func TestSaveWithStructuredRuleLinkAtomically(t *testing.T) {
	s := newTestStore(t)

	// Acceptance 1: rule v1 with policyRule + vigencia.
	ruleV1, err := s.Save(ruleInput(t, "policy/indirect-tax/late-document"))
	if err != nil {
		t.Fatalf("save rule v1: %v", err)
	}
	if ruleV1.Memory.PolicyRule == nil || ruleV1.Memory.PolicyRule.Jurisdiction != "PE" {
		t.Fatalf("rule v1 must carry the PE policy metadata")
	}

	// Acceptance 2: a decision pins v1 at the decision EffectiveAt, atomically.
	decisionAt := "2026-07-31T12:00:00Z"
	decision, err := s.Save(core.SaveInput{
		TopicKey:     "decision/late-document-0724",
		Title:        "late document classified with the pinned rule",
		Kind:         core.KindDecision,
		Scope:        testScope(testRucA),
		Content:      ruleContent(),
		Source:       testAgentSource,
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  decisionAt,
		RuleLinks: []core.RuleLink{{
			Ref:         "policy/indirect-tax/late-document",
			Version:     ruleV1.Memory.Identity.ID,
			EffectiveAt: decisionAt,
		}},
	})
	if err != nil {
		t.Fatalf("save decision with structured link: %v", err)
	}
	decisionID := decision.Memory.Identity.ID

	// The bare ref was derived into RuleRefs and PARTICIPATES in the envelope:
	// the stored envelope must differ from the same memory WITHOUT the refs.
	got, ok := s.FindByID(decisionID)
	if !ok {
		t.Fatalf("decision not found")
	}
	if len(got.RuleRefs) != 1 || got.RuleRefs[0] != "policy/indirect-tax/late-document" {
		t.Fatalf("ruleRefs = %v, want the derived bare ref", got.RuleRefs)
	}
	withoutRefs := core.CloneMemory(got)
	withoutRefs.RuleRefs = []string{}
	if core.ComputeEnvelopeHash(withoutRefs) == got.EnvelopeHash {
		t.Fatal("the derived bare ref must participate in the envelope hash")
	}
	// The read surface returns the structured link.
	if len(got.RuleLinks) != 1 {
		t.Fatalf("ruleLinks = %v, want exactly one structured link", got.RuleLinks)
	}
	link := got.RuleLinks[0]
	if link.Ref != "policy/indirect-tax/late-document" || link.Version != ruleV1.Memory.Identity.ID || link.EffectiveAt != decisionAt {
		t.Fatalf("ruleLinks[0] = %+v, want the pinned v1 at %s", link, decisionAt)
	}
	// The stored row carries the actor + timestamp provenance.
	var actor, timestamp string
	if err := s.db.QueryRow(`SELECT actor, timestamp FROM rule_links WHERE memory_id = ? AND ref = ?`, decisionID, "policy/indirect-tax/late-document").Scan(&actor, &timestamp); err != nil {
		t.Fatalf("read structured row: %v", err)
	}
	if actor == "" || timestamp == "" {
		t.Fatalf("structured row provenance missing (actor=%q timestamp=%q)", actor, timestamp)
	}
}

// TestSaveRuleLinkValidationFailures covers the typed failure matrix at save
// time: malformed links fail closed (INVALID_RULE_LINK), a conflicting double
// pin fails RULE_LINK_VERSION_CONFLICT, an identical repeated pin is a no-op,
// the decision-time contract fails RULE_LINK_EFFECTIVE_AT_MISMATCH and every
// invalid target fails with its typed error. A failing save leaves NO partial
// memory/link state.
func TestSaveRuleLinkValidationFailures(t *testing.T) {
	s := newTestStore(t)
	ruleV1, err := s.Save(ruleInput(t, "policy/validation/rule"))
	if err != nil {
		t.Fatalf("save rule: %v", err)
	}
	ruleID := ruleV1.Memory.Identity.ID
	at := "2026-07-31T12:00:00Z"

	base := core.SaveInput{
		TopicKey:     "decision/validation",
		Title:        "validation decision",
		Kind:         core.KindDecision,
		Scope:        testScope(testRucA),
		Content:      ruleContent(),
		Source:       testAgentSource,
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  at,
	}

	t.Run("malformed link fails closed", func(t *testing.T) {
		for name, link := range map[string]core.RuleLink{
			"empty ref":     {Ref: "", Version: ruleID, EffectiveAt: at},
			"empty version": {Ref: "policy/validation/rule", Version: " ", EffectiveAt: at},
			"non-RFC3339":   {Ref: "policy/validation/rule", Version: ruleID, EffectiveAt: "2026-07-31"},
		} {
			in := base
			in.TopicKey = "decision/validation-" + strings.ReplaceAll(name, " ", "-")
			in.RuleLinks = []core.RuleLink{link}
			if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "INVALID_RULE_LINK") {
				t.Fatalf("%s: err = %v, want INVALID_RULE_LINK", name, err)
			}
		}
	})

	t.Run("conflicting double pin", func(t *testing.T) {
		in := base
		in.TopicKey = "decision/validation-conflict"
		in.RuleLinks = []core.RuleLink{
			{Ref: "policy/validation/rule", Version: ruleID, EffectiveAt: at},
			{Ref: "policy/validation/rule", Version: "some-other-version", EffectiveAt: at},
		}
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "RULE_LINK_VERSION_CONFLICT") {
			t.Fatalf("err = %v, want RULE_LINK_VERSION_CONFLICT", err)
		}
	})

	t.Run("identical repeated pin is a no-op", func(t *testing.T) {
		in := base
		in.TopicKey = "decision/validation-identical"
		in.RuleLinks = []core.RuleLink{
			{Ref: "policy/validation/rule", Version: ruleID, EffectiveAt: at},
			{Ref: "policy/validation/rule", Version: ruleID, EffectiveAt: at},
		}
		res, err := s.Save(in)
		if err != nil {
			t.Fatalf("err = %v, want success (identical links dedupe)", err)
		}
		got, _ := s.FindByID(res.Memory.Identity.ID)
		if len(got.RuleLinks) != 1 {
			t.Fatalf("ruleLinks = %v, want exactly ONE row after dedupe", got.RuleLinks)
		}
	})

	t.Run("decision-time mismatch", func(t *testing.T) {
		in := base
		in.TopicKey = "decision/validation-effective-at"
		in.RuleLinks = []core.RuleLink{{Ref: "policy/validation/rule", Version: ruleID, EffectiveAt: "2026-08-15T00:00:00Z"}}
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "RULE_LINK_EFFECTIVE_AT_MISMATCH") {
			t.Fatalf("err = %v, want RULE_LINK_EFFECTIVE_AT_MISMATCH", err)
		}
	})

	t.Run("invalid targets", func(t *testing.T) {
		otherTenant := testScope(testRucA)
		otherTenant.OrganizationID = "tenant-other"
		foreign, err := s.Save(core.SaveInput{
			TopicKey: "policy/foreign/rule", Title: "foreign rule", Kind: core.KindRule,
			Scope: otherTenant, Content: ruleContent(), Source: testAgentSource,
			FiscalEffect: core.FiscalEffectNone, EffectiveAt: "2026-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("save foreign rule: %v", err)
		}
		fact, err := s.Save(validInput("fact/validation/not-a-rule", "a fact"))
		if err != nil {
			t.Fatalf("save fact: %v", err)
		}
		otherRule, err := s.Save(ruleInput(t, "policy/other-topic/rule"))
		if err != nil {
			t.Fatalf("save other rule: %v", err)
		}
		for name, link := range map[string]core.RuleLink{
			"missing version": {Ref: "policy/validation/rule", Version: "does-not-exist", EffectiveAt: at},
			"not a rule":      {Ref: "policy/validation/rule", Version: fact.Memory.Identity.ID, EffectiveAt: at},
			"topic mismatch":  {Ref: "policy/validation/rule", Version: otherRule.Memory.Identity.ID, EffectiveAt: at},
			"tenant mismatch": {Ref: "policy/foreign/rule", Version: foreign.Memory.Identity.ID, EffectiveAt: at},
		} {
			in := base
			in.TopicKey = "decision/validation-target-" + name
			in.RuleLinks = []core.RuleLink{link}
			if _, err := s.Save(in); err == nil {
				t.Fatalf("%s: err = nil, want a typed target failure", name)
			}
		}
	})

	t.Run("whole save rolls back on a link failure", func(t *testing.T) {
		before := countRows(t, s, `SELECT COUNT(*) FROM observations`)
		in := base
		in.TopicKey = "decision/validation-atomic"
		in.RuleLinks = []core.RuleLink{
			{Ref: "policy/validation/rule", Version: ruleID, EffectiveAt: at},
			{Ref: "policy/validation/rule", Version: "conflicting-version", EffectiveAt: at},
		}
		if _, err := s.Save(in); err == nil || !strings.Contains(err.Error(), "RULE_LINK_VERSION_CONFLICT") {
			t.Fatalf("err = %v, want RULE_LINK_VERSION_CONFLICT", err)
		}
		if after := countRows(t, s, `SELECT COUNT(*) FROM observations`); after != before {
			t.Fatalf("observations = %d after the failed save, want %d — no partial memory state", after, before)
		}
	})
}

// TestLegacyRuleRefsStayValidWithoutMetadata freezes the compatibility rule: a
// legacy bare ruleRef (no structured link) stays a valid ref, its temporal
// layer is skipped (no version row), and RuleLinks stays empty on read.
func TestLegacyRuleRefsStayValidWithoutMetadata(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(ruleInput(t, "policy/legacy/rule")); err != nil {
		t.Fatalf("save rule: %v", err)
	}
	at := "2026-07-31T12:00:00Z"
	res, err := s.Save(core.SaveInput{
		TopicKey: "decision/legacy", Title: "legacy decision", Kind: core.KindDecision,
		Scope: testScope(testRucA), Content: ruleContent(), Source: testAgentSource,
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: at,
		RuleRefs: []string{"policy/legacy/rule"},
	})
	if err != nil {
		t.Fatalf("save legacy decision: %v", err)
	}
	got, _ := s.FindByID(res.Memory.Identity.ID)
	if len(got.RuleRefs) != 1 || got.RuleRefs[0] != "policy/legacy/rule" {
		t.Fatalf("ruleRefs = %v, want the legacy bare ref", got.RuleRefs)
	}
	if len(got.RuleLinks) != 0 {
		t.Fatalf("ruleLinks = %v, want empty (legacy refs are skipped, never versioned)", got.RuleLinks)
	}
	var versionCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM rule_links WHERE memory_id = ? AND version IS NOT NULL`, res.Memory.Identity.ID).Scan(&versionCount); err != nil {
		t.Fatalf("count versioned rows: %v", err)
	}
	if versionCount != 0 {
		t.Fatalf("versioned rows = %d, want 0 for a legacy ref", versionCount)
	}
	// The legacy ref is still linkable the legacy way.
	if err := s.AddRuleLink(res.Memory.Identity.ID, "policy/legacy/rule-2", "cli"); err != nil {
		t.Fatalf("legacy AddRuleLink: %v", err)
	}
}

// TestAddRuleLinkVersionPostSave covers the post-save API: happy path with a
// NEW bare ref (envelope refreshes), identical-link no-op, conflicting version
// (RULE_LINK_VERSION_CONFLICT), legacy-row upgrade conflict, and a metadata
// insert for an already-present bare ref WITHOUT an envelope change.
func TestAddRuleLinkVersionPostSave(t *testing.T) {
	s := newTestStore(t)
	ruleV1, err := s.Save(ruleInput(t, "policy/postsave/rule"))
	if err != nil {
		t.Fatalf("save rule: %v", err)
	}
	ruleV2, err := s.Save(ruleInput(t, "policy/postsave/rule"))
	if err != nil {
		t.Fatalf("save rule v2: %v", err)
	}
	ruleID := ruleV1.Memory.Identity.ID
	at := "2026-07-31T12:00:00Z"

	// The consuming decision carries NO refs at write — the pin adds a NEW bare
	// ref, so the envelope must refresh.
	decision, err := s.Save(decisionInput(t, "decision/postsave", at))
	if err != nil {
		t.Fatalf("save decision: %v", err)
	}
	decisionID := decision.Memory.Identity.ID
	envelopeBefore := currentEnvelope(decision)

	link := core.RuleLink{Ref: "policy/postsave/rule", Version: ruleID, EffectiveAt: at}
	mustRuleLink(t, s, decisionID, link, "cli")

	got, _ := s.FindByID(decisionID)
	if len(got.RuleLinks) != 1 || got.RuleLinks[0] != link {
		t.Fatalf("ruleLinks = %v, want the pinned link", got.RuleLinks)
	}
	if len(got.RuleRefs) != 1 || got.RuleRefs[0] != "policy/postsave/rule" {
		t.Fatalf("ruleRefs = %v, want the new bare ref", got.RuleRefs)
	}
	if got.EnvelopeHash == envelopeBefore {
		t.Fatal("the envelope must refresh when the bare ref is NEW")
	}

	// Identical link: no-op, still exactly one row.
	mustRuleLink(t, s, decisionID, link, "cli-again")
	got, _ = s.FindByID(decisionID)
	if len(got.RuleLinks) != 1 {
		t.Fatalf("ruleLinks = %v, want exactly one row after the identical no-op", got.RuleLinks)
	}

	// Conflicting version: RULE_LINK_VERSION_CONFLICT, row untouched.
	err = s.AddRuleLinkVersion(decisionID, core.RuleLink{Ref: "policy/postsave/rule", Version: ruleV2.Memory.Identity.ID, EffectiveAt: at}, "cli")
	if err == nil || !strings.Contains(err.Error(), "RULE_LINK_VERSION_CONFLICT") {
		t.Fatalf("err = %v, want RULE_LINK_VERSION_CONFLICT", err)
	}
	got, _ = s.FindByID(decisionID)
	if len(got.RuleLinks) != 1 || got.RuleLinks[0].Version != ruleID {
		t.Fatalf("conflicting pin must not mutate the stored link, got %v", got.RuleLinks)
	}

	// A legacy bare row can never be upgraded in place.
	legacy, err := s.Save(decisionInput(t, "decision/postsave-legacy", at))
	if err != nil {
		t.Fatalf("save legacy decision: %v", err)
	}
	if err := s.AddRuleLink(legacy.Memory.Identity.ID, "policy/postsave/rule", "cli"); err != nil {
		t.Fatalf("legacy bare link: %v", err)
	}
	err = s.AddRuleLinkVersion(legacy.Memory.Identity.ID, core.RuleLink{Ref: "policy/postsave/rule", Version: ruleID, EffectiveAt: at}, "cli")
	if err == nil || !strings.Contains(err.Error(), "RULE_LINK_VERSION_CONFLICT") {
		t.Fatalf("upgrading a legacy row = %v, want RULE_LINK_VERSION_CONFLICT", err)
	}

	// A metadata insert for an already-present STORED bare ref (written at save)
	// changes no envelope: the bare ref was already hashed.
	predeclared, err := s.Save(core.SaveInput{
		TopicKey: "decision/postsave-predeclared", Title: "predeclared ref", Kind: core.KindDecision,
		Scope: testScope(testRucA), Content: ruleContent(), Source: testAgentSource,
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: at, RuleRefs: []string{"policy/postsave/rule"},
	})
	if err != nil {
		t.Fatalf("save predeclared decision: %v", err)
	}
	preEnv := currentEnvelope(predeclared)
	mustRuleLink(t, s, predeclared.Memory.Identity.ID, link, "cli")
	got, _ = s.FindByID(predeclared.Memory.Identity.ID)
	if len(got.RuleLinks) != 1 {
		t.Fatalf("ruleLinks = %v, want the pinned metadata", got.RuleLinks)
	}
	if got.EnvelopeHash != preEnv {
		t.Fatal("the envelope must NOT change when the bare ref already existed")
	}
}

// TestAddRuleLinkVersionClosedPeriodGate freezes the closed-period gate on the
// post-save API: inside a CLOSED exact company period a structured pin fails
// with PERIOD_CLOSED before any mutation.
func TestAddRuleLinkVersionClosedPeriodGate(t *testing.T) {
	s := newTestStore(t)
	scope := testScope(testRucA)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})

	inPeriod, err := s.Save(validInput("tax.gate.pin", "in-period memory"))
	if err != nil {
		t.Fatalf("save in-period memory: %v", err)
	}
	rule, err := s.Save(ruleInput(t, "policy/gate/rule"))
	if err != nil {
		t.Fatalf("save rule: %v", err)
	}
	// Close the period AFTER the fixtures exist.
	_, _ = saveAndApproveClose(t, s, scope, "gate blocks structured pins", "req-close-pin")

	err = s.AddRuleLinkVersion(inPeriod.Memory.Identity.ID, core.RuleLink{
		Ref: "policy/gate/rule", Version: rule.Memory.Identity.ID, EffectiveAt: inPeriod.Memory.EffectiveAt,
	}, "cli")
	if auth.Code(err) != auth.CodePeriodClosed {
		t.Fatalf("code = %q, want PERIOD_CLOSED (err: %v)", auth.Code(err), err)
	}
}

// TestConcurrentRuleLinkVersionPins is the concurrency proof (design §8): two
// INDEPENDENTLY opened stores (separate *sql.DB handles against ONE WAL file —
// MaxOpenConns(1) cannot serialize them) race AddRuleLinkVersion on the SAME
// (memory_id, ref) with DIFFERENT rule versions. BEGIN IMMEDIATE serializes the
// pair: exactly ONE commit and ONE RULE_LINK_VERSION_CONFLICT; no partial
// memory/link/envelope state is visible. Run with -race.
func TestConcurrentRuleLinkVersionPins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-rule-link-concurrent.db")
	s1 := openTestStorePath(t, path)
	s2 := openTestStorePath(t, path)

	// Rule v1 and rule v2 are two immutable revisions of the SAME chain: the
	// loser of the race must observe the winner's committed pin.
	ruleV1, err := s1.Save(ruleInput(t, "policy/concurrent/rule"))
	if err != nil {
		t.Fatalf("save rule v1: %v", err)
	}
	ruleV2, err := s1.Save(ruleInput(t, "policy/concurrent/rule"))
	if err != nil {
		t.Fatalf("save rule v2: %v", err)
	}
	at := "2026-07-31T12:00:00Z"
	decision, err := s1.Save(decisionInput(t, "decision/concurrent", at))
	if err != nil {
		t.Fatalf("save decision: %v", err)
	}
	decisionID := decision.Memory.Identity.ID

	links := []core.RuleLink{
		{Ref: "policy/concurrent/rule", Version: ruleV1.Memory.Identity.ID, EffectiveAt: at},
		{Ref: "policy/concurrent/rule", Version: ruleV2.Memory.Identity.ID, EffectiveAt: at},
	}
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			st := s1
			if i == 1 {
				st = s2
			}
			errCh <- st.AddRuleLinkVersion(decisionID, links[i], "racer")
		}(i)
	}
	close(start)
	wg.Wait()
	close(errCh)
	errs := make([]error, 0, 2)
	for err := range errCh {
		errs = append(errs, err)
	}

	// Exactly one commit and one conflict.
	committed, conflicted := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			committed++
		case strings.Contains(err.Error(), "RULE_LINK_VERSION_CONFLICT"):
			conflicted++
		default:
			t.Fatalf("unexpected racer error: %v", err)
		}
	}
	if committed != 1 || conflicted != 1 {
		t.Fatalf("commits = %d, conflicts = %d, want exactly one of each", committed, conflicted)
	}

	// The committed state is the winner's pin — exactly one versioned row, and
	// the read surface agrees.
	var count int
	if err := s1.db.QueryRow(`SELECT COUNT(*) FROM rule_links WHERE memory_id = ? AND ref = ? AND version IS NOT NULL`, decisionID, "policy/concurrent/rule").Scan(&count); err != nil {
		t.Fatalf("count versioned rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("versioned rows = %d, want exactly 1", count)
	}
	got, ok := s1.FindByID(decisionID)
	if !ok {
		t.Fatalf("decision lost")
	}
	if len(got.RuleLinks) != 1 {
		t.Fatalf("ruleLinks = %v, want exactly one committed pin", got.RuleLinks)
	}
	pinned := got.RuleLinks[0].Version
	if pinned != ruleV1.Memory.Identity.ID && pinned != ruleV2.Memory.Identity.ID {
		t.Fatalf("committed pin %q is neither v1 nor v2", pinned)
	}
}

// TestRuleLinksReadSurfaceExcludesLegacyRows verifies the read surface is
// exactly the versioned rows: a memory with BOTH a legacy bare ref and a
// structured pin surfaces only the structured link in RuleLinks while RuleRefs
// carries both.
func TestRuleLinksReadSurfaceExcludesLegacyRows(t *testing.T) {
	s := newTestStore(t)
	rule, err := s.Save(ruleInput(t, "policy/surface/rule"))
	if err != nil {
		t.Fatalf("save rule: %v", err)
	}
	at := "2026-07-31T12:00:00Z"
	res, err := s.Save(core.SaveInput{
		TopicKey: "decision/surface", Title: "mixed surface", Kind: core.KindDecision,
		Scope: testScope(testRucA), Content: ruleContent(), Source: testAgentSource,
		FiscalEffect: core.FiscalEffectNone, EffectiveAt: at,
		RuleRefs: []string{"policy/surface/legacy"},
		RuleLinks: []core.RuleLink{{
			Ref: "policy/surface/rule", Version: rule.Memory.Identity.ID, EffectiveAt: at,
		}},
	})
	if err != nil {
		t.Fatalf("save mixed decision: %v", err)
	}
	got, _ := s.FindByID(res.Memory.Identity.ID)
	if len(got.RuleRefs) != 2 {
		t.Fatalf("ruleRefs = %v, want the legacy ref AND the pinned ref", got.RuleRefs)
	}
	if len(got.RuleLinks) != 1 || got.RuleLinks[0].Ref != "policy/surface/rule" {
		t.Fatalf("ruleLinks = %v, want ONLY the versioned link", got.RuleLinks)
	}
}
