// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the mandatory v2 vocabulary
// surface: kinds, statuses, fiscal effects, actor kinds, relations, the content
// hash contract, the approval gate and the legacy mappings. No money value is
// computed here.

package core

import (
	"strings"
	"testing"
)

func TestIsValidMemoryKind(t *testing.T) {
	valid := []MemoryKind{
		KindFact, KindEvidence, KindDecision, KindRule,
		KindException, KindControl, KindObligation, KindSummary,
	}
	for _, kind := range valid {
		t.Run(string(kind), func(t *testing.T) {
			if !IsValidMemoryKind(kind) {
				t.Fatalf("IsValidMemoryKind(%q) = false, want true", kind)
			}
		})
	}
	invalid := []MemoryKind{"", "unknown", "policy", "DRAFT"}
	for _, kind := range invalid {
		t.Run("invalid "+string(kind), func(t *testing.T) {
			if IsValidMemoryKind(kind) {
				t.Fatalf("IsValidMemoryKind(%q) = true, want false (empty/unknown kinds are invalid for v2 writes)", kind)
			}
		})
	}
}

func TestIsValidMemoryStatus(t *testing.T) {
	valid := []MemoryStatus{
		StatusActive, StatusPendingReview, StatusApproved,
		StatusRejected, StatusSuperseded, StatusVoided,
	}
	for _, status := range valid {
		t.Run(string(status), func(t *testing.T) {
			if !IsValidMemoryStatus(status) {
				t.Fatalf("IsValidMemoryStatus(%q) = false, want true", status)
			}
		})
	}
	invalid := []MemoryStatus{"", "draft", "reviewed", "promoted", "WIP"}
	for _, status := range invalid {
		t.Run("invalid "+string(status), func(t *testing.T) {
			if IsValidMemoryStatus(status) {
				t.Fatalf("IsValidMemoryStatus(%q) = true, want false", status)
			}
		})
	}
}

func TestIsValidFiscalEffect(t *testing.T) {
	valid := []FiscalEffect{
		FiscalEffectNone, FiscalEffectJournalEntry, FiscalEffectDeclaration,
		FiscalEffectClosing, FiscalEffectAdjustment, FiscalEffectReclassification,
		FiscalEffectApproval, FiscalEffectSunatFiling,
	}
	for _, effect := range valid {
		t.Run(string(effect), func(t *testing.T) {
			if !IsValidFiscalEffect(effect) {
				t.Fatalf("IsValidFiscalEffect(%q) = false, want true", effect)
			}
		})
	}
	invalid := []FiscalEffect{"", "void", "nonexistent"}
	for _, effect := range invalid {
		t.Run("invalid "+string(effect), func(t *testing.T) {
			if IsValidFiscalEffect(effect) {
				t.Fatalf("IsValidFiscalEffect(%q) = true, want false", effect)
			}
		})
	}
}

func TestIsValidActorKind(t *testing.T) {
	for _, kind := range []ActorKind{ActorKindHuman, ActorKindAgent, ActorKindSystem} {
		if !IsValidActorKind(kind) {
			t.Fatalf("IsValidActorKind(%q) = false, want true", kind)
		}
	}
	for _, kind := range []ActorKind{"", "robot", "machine"} {
		if IsValidActorKind(kind) {
			t.Fatalf("IsValidActorKind(%q) = true, want false", kind)
		}
	}
}

func TestIsValidRelation(t *testing.T) {
	all := []Relation{
		// 6 legacy
		RelationRelated, RelationCompatible, RelationScoped,
		RelationConflictsWith, RelationSupersedes, RelationNotConflict,
		// 11 v2 accounting-evidence
		RelationSupports, RelationContradicts, RelationExplains,
		RelationDerivedFrom, RelationPostedAs, RelationReconciles,
		RelationReverses, RelationRequires, RelationViolates,
		RelationApprovedBy, RelationRejectedBy,
	}
	if len(all) != 17 {
		t.Fatalf("relation vocabulary has %d members, want 17", len(all))
	}
	for _, relation := range all {
		t.Run(string(relation), func(t *testing.T) {
			if !IsValidRelation(relation) {
				t.Fatalf("IsValidRelation(%q) = false, want true", relation)
			}
		})
	}
	if IsValidRelation("") || IsValidRelation("supersedes_") || IsValidRelation("arbitrary") {
		t.Fatal("unknown/empty relations must be invalid")
	}
}

// contentHashFixture builds two memories that differ ONLY in the envelope fields
// (id, status, recordedAt, revision) while sharing every content field.
func contentHashFixture() (AccountingMemory, AccountingMemory) {
	base := AccountingMemory{
		Title: "Saldo inicial 4011",
		Kind:  KindFact,
		Scope: Scope{
			Kind: ScopeKindCompany, OrganizationID: "cmp_org",
			CompanyID: "cmp_01", RUC: "20601234567", Period: "202607",
		},
		Content:      Content{What: "Saldo inicial S/ 31,804.50", Why: "apertura", Where: "libro mayor", Learned: "verificado"},
		Status:       StatusActive,
		FiscalEffect: FiscalEffectNone,
		EffectiveAt:  "2026-07-01",
		RecordedAt:   "2026-07-01T08:00:00Z",
		Source:       Source{System: "manual", ActorKind: ActorKindHuman, ActorID: "maria.torres"},
	}
	a := base
	a.Identity = Identity{ID: "id-A", TopicKey: "t/4011"}
	b := base
	b.Identity = Identity{ID: "id-B", TopicKey: "t/4011"}
	b.Status = StatusApproved
	b.RecordedAt = "2026-09-01T08:00:00Z"
	b.Revision = 3
	return a, b
}

func TestComputeContentHash(t *testing.T) {
	a, b := contentHashFixture()
	hashA, hashB := ComputeContentHash(a), ComputeContentHash(b)
	if hashA == "" {
		t.Fatal("content hash must not be empty")
	}
	if hashA != hashB {
		t.Fatalf("content hash must not depend on id/status/recordedAt/revision: %s != %s", hashA, hashB)
	}
	if ComputeContentHash(a) != ComputeContentHash(a) {
		t.Fatal("content hash must be deterministic (same input → same hash)")
	}

	t.Run("changes with content", func(t *testing.T) {
		changed := a
		changed.Content.What = "Saldo inicial S/ 32,000.00"
		if ComputeContentHash(changed) == hashA {
			t.Fatal("hash must change when content changes")
		}
	})
	t.Run("changes with title", func(t *testing.T) {
		changed := a
		changed.Title = "Otra memoria"
		if ComputeContentHash(changed) == hashA {
			t.Fatal("hash must change when title changes")
		}
	})
	t.Run("changes with kind", func(t *testing.T) {
		changed := a
		changed.Kind = KindDecision
		if ComputeContentHash(changed) == hashA {
			t.Fatal("hash must change when kind changes")
		}
	})
	t.Run("changes with scope", func(t *testing.T) {
		changed := a
		changed.Scope.Period = "202608"
		if ComputeContentHash(changed) == hashA {
			t.Fatal("hash must change when scope changes")
		}
	})
	t.Run("changes with effectiveAt", func(t *testing.T) {
		changed := a
		changed.EffectiveAt = "2026-07-02"
		if ComputeContentHash(changed) == hashA {
			t.Fatal("hash must change when effectiveAt changes")
		}
	})
	t.Run("changes with source system", func(t *testing.T) {
		changed := a
		changed.Source.System = "sire"
		if ComputeContentHash(changed) == hashA {
			t.Fatal("hash must change when source system changes")
		}
	})
	t.Run("changes with actorKind", func(t *testing.T) {
		changed := a
		changed.Source.ActorKind = ActorKindSystem
		if ComputeContentHash(changed) == hashA {
			t.Fatal("hash must change when actorKind changes")
		}
	})
	t.Run("does not change with fiscalEffect= none vs nothing", func(t *testing.T) {
		// The fiscal effect participates; an empty fiscal effect is invalid for
		// writes but must still be a distinct canonical input.
		changed := a
		changed.FiscalEffect = FiscalEffectAdjustment
		if ComputeContentHash(changed) == hashA {
			t.Fatal("hash must change when fiscalEffect changes")
		}
	})
}

func TestInitialStatusGate(t *testing.T) {
	if got := InitialStatus(FiscalEffectNone); got != StatusActive {
		t.Fatalf("InitialStatus(none) = %s, want active", got)
	}
	if IsGated(FiscalEffectNone) {
		t.Fatal("IsGated(none) must be false")
	}
	for _, effect := range []FiscalEffect{
		FiscalEffectJournalEntry, FiscalEffectDeclaration, FiscalEffectClosing,
		FiscalEffectAdjustment, FiscalEffectReclassification, FiscalEffectApproval,
		FiscalEffectSunatFiling,
	} {
		t.Run(string(effect), func(t *testing.T) {
			if got := InitialStatus(effect); got != StatusPendingReview {
				t.Fatalf("InitialStatus(%s) = %s, want pending_review", effect, got)
			}
			if !IsGated(effect) {
				t.Fatalf("IsGated(%s) = false, want true", effect)
			}
		})
	}
}

func TestHumanGate(t *testing.T) {
	human := Source{ActorID: "maria.torres", ActorKind: ActorKindHuman, System: "manual"}
	agent := Source{ActorID: "agent-1", ActorKind: ActorKindAgent, System: "drenyra-core"}
	system := Source{ActorID: "system-1", ActorKind: ActorKindSystem, System: "drenyra-core"}

	t.Run("approve requires human", func(t *testing.T) {
		for _, actor := range []Source{agent, system} {
			m := pendingReviewMemory()
			if err := Approve(&m, actor); err == nil || !strings.Contains(err.Error(), ErrGateRequiresHuman.Error()) {
				t.Fatalf("approve by %s: expected GATE_REQUIRES_HUMAN, got %v", actor.ActorKind, err)
			}
			if m.Status != StatusPendingReview {
				t.Fatalf("memory mutated by failed gate: status = %s", m.Status)
			}
		}
		m := pendingReviewMemory()
		if err := Approve(&m, human); err != nil {
			t.Fatalf("approve by human failed: %v", err)
		}
		if m.Status != StatusApproved {
			t.Fatalf("status = %s, want approved", m.Status)
		}
	})

	t.Run("reject requires human", func(t *testing.T) {
		for _, actor := range []Source{agent, system} {
			m := pendingReviewMemory()
			if err := Reject(&m, actor); err == nil || !strings.Contains(err.Error(), ErrGateRequiresHuman.Error()) {
				t.Fatalf("reject by %s: expected GATE_REQUIRES_HUMAN, got %v", actor.ActorKind, err)
			}
		}
		m := pendingReviewMemory()
		if err := Reject(&m, human); err != nil {
			t.Fatalf("reject by human failed: %v", err)
		}
		if m.Status != StatusRejected {
			t.Fatalf("status = %s, want rejected", m.Status)
		}
	})

	t.Run("void admits human and system, never agent", func(t *testing.T) {
		for _, from := range []MemoryStatus{StatusActive, StatusPendingReview, StatusApproved} {
			m := AccountingMemory{Identity: Identity{ID: "m"}, Status: from}
			if err := Void(&m, agent); err == nil || !strings.Contains(err.Error(), "GATE_AGENT_CANNOT_VOID") {
				t.Fatalf("void by agent from %s: expected GATE_AGENT_CANNOT_VOID, got %v", from, err)
			}
			for _, actor := range []Source{human, system} {
				m := AccountingMemory{Identity: Identity{ID: "m"}, Status: from}
				if err := Void(&m, actor); err != nil {
					t.Fatalf("void by %s from %s failed: %v", actor.ActorKind, from, err)
				}
				if m.Status != StatusVoided {
					t.Fatalf("status = %s, want voided", m.Status)
				}
			}
		}
	})

	t.Run("illegal transitions fail closed", func(t *testing.T) {
		for _, from := range []MemoryStatus{StatusActive, StatusApproved, StatusRejected, StatusSuperseded, StatusVoided} {
			m := AccountingMemory{Identity: Identity{ID: "m"}, Status: from}
			if err := Approve(&m, human); err == nil || !strings.Contains(err.Error(), ErrInvalidTransition.Error()) {
				t.Fatalf("approve from %s: expected INVALID_TRANSITION, got %v", from, err)
			}
		}
		for _, from := range []MemoryStatus{StatusRejected, StatusSuperseded, StatusVoided} {
			m := AccountingMemory{Identity: Identity{ID: "m"}, Status: from}
			if err := Void(&m, human); err == nil || !strings.Contains(err.Error(), ErrInvalidTransition.Error()) {
				t.Fatalf("void from %s: expected INVALID_TRANSITION, got %v", from, err)
			}
		}
	})
}

func pendingReviewMemory() AccountingMemory {
	return AccountingMemory{
		Identity:     Identity{ID: "m-1", TopicKey: "t"},
		Title:        "Ajuste",
		Kind:         KindDecision,
		Status:       StatusPendingReview,
		FiscalEffect: FiscalEffectAdjustment,
		EffectiveAt:  "2026-07-31",
		Source:       Source{System: "drenyra-core", ActorKind: ActorKindAgent},
	}
}

func TestSupersedePrev(t *testing.T) {
	t.Run("supersedable states become superseded with successor id", func(t *testing.T) {
		for _, from := range []MemoryStatus{StatusActive, StatusPendingReview, StatusApproved} {
			prev := AccountingMemory{Identity: Identity{ID: "old"}, Status: from}
			if err := SupersedePrev(&prev, "new-id"); err != nil {
				t.Fatalf("supersede from %s: %v", from, err)
			}
			if prev.Status != StatusSuperseded {
				t.Fatalf("status = %s, want superseded", prev.Status)
			}
			if prev.SupersedesID != "new-id" {
				t.Fatalf("SupersedesID = %q, want new-id", prev.SupersedesID)
			}
		}
	})
	t.Run("terminal states never re-open", func(t *testing.T) {
		for _, from := range []MemoryStatus{StatusRejected, StatusSuperseded, StatusVoided} {
			prev := AccountingMemory{Identity: Identity{ID: "old"}, Status: from}
			if err := SupersedePrev(&prev, "new-id"); err == nil || !strings.Contains(err.Error(), ErrInvalidTransition.Error()) {
				t.Fatalf("supersede from %s: expected INVALID_TRANSITION, got %v", from, err)
			}
			if prev.Status != from {
				t.Fatalf("memory mutated by failed supersede: status = %s", prev.Status)
			}
		}
	})
}

func TestLegacyMappings(t *testing.T) {
	t.Run("LegacyTypeToKind", func(t *testing.T) {
		decision := []string{"decision", "judgment"}
		for _, typ := range decision {
			if got := LegacyTypeToKind(typ); got != KindDecision {
				t.Fatalf("LegacyTypeToKind(%q) = %s, want decision", typ, got)
			}
		}
		rule := []string{"policy", "pattern", "config", "preference"}
		for _, typ := range rule {
			if got := LegacyTypeToKind(typ); got != KindRule {
				t.Fatalf("LegacyTypeToKind(%q) = %s, want rule", typ, got)
			}
		}
		fact := []string{"discovery", "bugfix"}
		for _, typ := range fact {
			if got := LegacyTypeToKind(typ); got != KindFact {
				t.Fatalf("LegacyTypeToKind(%q) = %s, want fact", typ, got)
			}
		}
		if got := LegacyTypeToKind("architecture"); got != KindSummary {
			t.Fatalf("LegacyTypeToKind(architecture) = %s, want summary", got)
		}
		for _, typ := range []string{"", "unknown", "random"} {
			if got := LegacyTypeToKind(typ); got != KindFact {
				t.Fatalf("LegacyTypeToKind(%q) = %s, want fact (default)", typ, got)
			}
		}
	})
	t.Run("LegacyStatusToStatus", func(t *testing.T) {
		if got := LegacyStatusToStatus("promoted"); got != StatusApproved {
			t.Fatalf("LegacyStatusToStatus(promoted) = %s, want approved", got)
		}
		if got := LegacyStatusToStatus("superseded"); got != StatusSuperseded {
			t.Fatalf("LegacyStatusToStatus(superseded) = %s, want superseded", got)
		}
		for _, status := range []string{"draft", "reviewed", "", "weird"} {
			if got := LegacyStatusToStatus(status); got != StatusActive {
				t.Fatalf("LegacyStatusToStatus(%q) = %s, want active", status, got)
			}
		}
	})
}

func TestAssertValidMemory(t *testing.T) {
	valid := AccountingMemory{
		Identity:     Identity{ID: "id-1", TopicKey: "t"},
		Title:        "Memoria válida",
		Kind:         KindFact,
		Scope:        Scope{Kind: ScopeKindCompany, OrganizationID: "o", CompanyID: "c", RUC: "20601234567", Period: "202607"},
		Content:      Content{What: "w", Why: "y", Where: "r", Learned: "l"},
		Status:       StatusActive,
		FiscalEffect: FiscalEffectNone,
		EffectiveAt:  "2026-07-01",
		RecordedAt:   "2026-07-01T08:00:00Z",
		Source:       Source{System: "manual", ActorID: "maria.torres", ActorKind: ActorKindHuman},
	}
	if err := AssertValidMemory(valid); err != nil {
		t.Fatalf("valid memory rejected: %v", err)
	}

	t.Run("source without system rejected", func(t *testing.T) {
		bad := valid
		bad.Source.System = ""
		if err := AssertValidMemory(bad); err == nil || !strings.Contains(err.Error(), "system") {
			t.Fatalf("expected system error, got %v", err)
		}
	})

	t.Run("human source without actorId rejected", func(t *testing.T) {
		bad := valid
		bad.Source.ActorID = ""
		if err := AssertValidMemory(bad); err == nil || !strings.Contains(err.Error(), "actorId") {
			t.Fatalf("expected actorId error, got %v", err)
		}
	})

	t.Run("missing effectiveAt rejected", func(t *testing.T) {
		bad := valid
		bad.EffectiveAt = ""
		if err := AssertValidMemory(bad); err == nil || !strings.Contains(err.Error(), "effectiveAt") {
			t.Fatalf("expected effectiveAt error, got %v", err)
		}
	})

	t.Run("unparseable effectiveAt rejected", func(t *testing.T) {
		bad := valid
		bad.EffectiveAt = "2026-13-45"
		if err := AssertValidMemory(bad); err == nil || !strings.Contains(err.Error(), "effectiveAt") {
			t.Fatalf("expected effectiveAt error, got %v", err)
		}
	})

	t.Run("confidence outside [0,1] rejected", func(t *testing.T) {
		for _, value := range []float64{-0.1, 1.1} {
			bad := valid
			bad.Confidence = &value
			if err := AssertValidMemory(bad); err == nil || !strings.Contains(err.Error(), "INVALID_CONFIDENCE") {
				t.Fatalf("confidence %v: expected INVALID_CONFIDENCE, got %v", value, err)
			}
		}
	})

	t.Run("negative materiality rejected", func(t *testing.T) {
		bad := valid
		value := int64(-5)
		bad.Materiality = &value
		if err := AssertValidMemory(bad); err == nil || !strings.Contains(err.Error(), "INVALID_MATERIALITY") {
			t.Fatalf("expected INVALID_MATERIALITY, got %v", err)
		}
	})

	t.Run("unknown kind rejected", func(t *testing.T) {
		bad := valid
		bad.Kind = "policy"
		if err := AssertValidMemory(bad); err == nil || !strings.Contains(err.Error(), "INVALID_KIND") {
			t.Fatalf("expected INVALID_KIND, got %v", err)
		}
	})

	t.Run("empty status rejected", func(t *testing.T) {
		bad := valid
		bad.Status = ""
		if err := AssertValidMemory(bad); err == nil || !strings.Contains(err.Error(), "INVALID_STATUS") {
			t.Fatalf("expected INVALID_STATUS, got %v", err)
		}
	})

	t.Run("empty fiscal effect rejected", func(t *testing.T) {
		bad := valid
		bad.FiscalEffect = ""
		if err := AssertValidMemory(bad); err == nil || !strings.Contains(err.Error(), "INVALID_FISCAL_EFFECT") {
			t.Fatalf("expected INVALID_FISCAL_EFFECT, got %v", err)
		}
	})
}
