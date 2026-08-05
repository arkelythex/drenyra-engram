// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the v0.4.0 MaterialityLevel
// contract: the declared classification is validated at write time, survives
// cloning, and does NOT participate in the envelope hash (frozen decision — the
// policy classifies by level; the hash bytes stay v0.3-identical).
package core

import (
	"strings"
	"testing"
)

func validMaterialityMemory() AccountingMemory {
	return AccountingMemory{
		Identity: Identity{ID: "mem-1", TopicKey: "dk/acme/202601/fact-1"},
		Title:    "material adjustment",
		Kind:     KindDecision,
		Scope: Scope{
			Kind:           ScopeKindCompany,
			OrganizationID: "tenant-1",
			CompanyID:      "acme",
			RUC:            "20100039201",
			Period:         "202601",
		},
		Content: Content{
			What:    "adjustment",
			Why:     "late document",
			Where:   "SUNAT",
			Learned: "pending review",
		},
		Status:       StatusPendingReview,
		FiscalEffect: FiscalEffectAdjustment,
		EffectiveAt:  "2026-01-15T00:00:00Z",
		RecordedAt:   "2026-01-16T00:00:00Z",
		Source: Source{
			System:    "drenyra-core",
			ActorID:   "agent-1",
			ActorKind: ActorKindAgent,
		},
		ContentHash: "content-hash",
	}
}

func TestIsValidMaterialityLevel(t *testing.T) {
	tests := []struct {
		level MaterialityLevel
		want  bool
	}{
		{MaterialityNormal, true},
		{MaterialityMaterial, true},
		{MaterialityCritical, true},
		{"", false},
		{"huge", false},
		{"Material", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			if got := IsValidMaterialityLevel(tt.level); got != tt.want {
				t.Errorf("IsValidMaterialityLevel(%q) = %v, want %v", tt.level, got, tt.want)
			}
		})
	}
}

func TestAssertValidMemoryAcceptsKnownMaterialityLevels(t *testing.T) {
	for _, level := range []MaterialityLevel{MaterialityNormal, MaterialityMaterial, MaterialityCritical} {
		m := validMaterialityMemory()
		m.MaterialityLevel = &level
		if err := AssertValidMemory(m); err != nil {
			t.Errorf("AssertValidMemory rejected known level %q: %v", level, err)
		}
	}
	// NULL (nil pointer) stays valid and means normal.
	if err := AssertValidMemory(validMaterialityMemory()); err != nil {
		t.Errorf("AssertValidMemory rejected NULL level: %v", err)
	}
}

func TestAssertValidMemoryRejectsUnknownMaterialityLevel(t *testing.T) {
	bad := MaterialityLevel("mega")
	m := validMaterialityMemory()
	m.MaterialityLevel = &bad
	err := AssertValidMemory(m)
	if err == nil {
		t.Fatal("AssertValidMemory accepted unknown materiality level")
	}
	if !strings.Contains(err.Error(), "INVALID_MATERIALITY_LEVEL") {
		t.Errorf("error must carry INVALID_MATERIALITY_LEVEL, got %v", err)
	}
}

func TestCloneMemoryCarriesMaterialityLevel(t *testing.T) {
	level := MaterialityCritical
	m := validMaterialityMemory()
	m.MaterialityLevel = &level

	cloned := CloneMemory(m)
	if cloned.MaterialityLevel == nil || *cloned.MaterialityLevel != MaterialityCritical {
		t.Fatalf("clone lost materialityLevel: got %v, want critical", cloned.MaterialityLevel)
	}

	// The clone is defensive: mutating the source pointer must not affect it.
	normal := MaterialityNormal
	m.MaterialityLevel = &normal
	if *cloned.MaterialityLevel != MaterialityCritical {
		t.Errorf("clone aliases the source materialityLevel pointer")
	}
}

func TestEnvelopeHashIgnoresMaterialityLevel(t *testing.T) {
	base := validMaterialityMemory()
	base.ContentHash = ComputeContentHash(base)
	base.IdentityHash = ComputeIdentityHash(base)
	base.EnvelopeHash = ComputeEnvelopeHash(base)

	critical := MaterialityCritical
	raised := CloneMemory(base)
	raised.MaterialityLevel = &critical
	raised.EnvelopeHash = ComputeEnvelopeHash(raised)

	if raised.EnvelopeHash != base.EnvelopeHash {
		t.Errorf("envelope hash changed with materialityLevel: base %q, raised %q — MaterialityLevel must NOT participate (frozen decision)", base.EnvelopeHash, raised.EnvelopeHash)
	}
}
