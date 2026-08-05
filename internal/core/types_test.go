// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the v2 core model: scope
// helpers, validators, content hashing and the v1→v2 legacy mappings. No money
// value is computed here.

package core

import (
	"strings"
	"testing"
)

func TestIsValidRUC(t *testing.T) {
	tests := []struct {
		name string
		ruc  string
		want bool
	}{
		{"eleven digits", "20100039201", true},
		{"another valid ruc", "20600995804", true},
		{"ten digits", "2010003920", false},
		{"twelve digits", "201000392011", false},
		{"contains a letter", "2010003920a", false},
		{"empty", "", false},
		{"spaces", " 2010003920", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidRUC(tt.ruc); got != tt.want {
				t.Errorf("IsValidRUC(%q) = %v, want %v", tt.ruc, got, tt.want)
			}
		})
	}
}

func TestIsValidPeriod(t *testing.T) {
	tests := []struct {
		name   string
		period string
		want   bool
	}{
		{"january", "202401", true},
		{"december", "202512", true},
		{"month thirteen", "202413", false},
		{"month zero", "202400", false},
		{"five digits", "20241", false},
		{"non numeric", "2024a1", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidPeriod(tt.period); got != tt.want {
				t.Errorf("IsValidPeriod(%q) = %v, want %v", tt.period, got, tt.want)
			}
		})
	}
}

func TestAssertValidScope(t *testing.T) {
	company := Scope{
		Kind:           ScopeKindCompany,
		OrganizationID: "org-001",
		CompanyID:      "acme",
		RUC:            "20100039201",
		Period:         "202401",
	}

	t.Run("institutional scope is always valid", func(t *testing.T) {
		if err := AssertValidScope(Scope{Kind: ScopeKindInstitutional}); err != nil {
			t.Fatalf("institutional scope rejected: %v", err)
		}
	})

	t.Run("valid company scope", func(t *testing.T) {
		if err := AssertValidScope(company); err != nil {
			t.Fatalf("valid company scope rejected: %v", err)
		}
	})

	t.Run("company scope without period is valid", func(t *testing.T) {
		noPeriod := company
		noPeriod.Period = ""
		if err := AssertValidScope(noPeriod); err != nil {
			t.Fatalf("period-less company scope rejected: %v", err)
		}
	})

	t.Run("unknown scope kind fails closed", func(t *testing.T) {
		bad := company
		bad.Kind = "fiscal"
		if err := AssertValidScope(bad); err == nil || !strings.Contains(err.Error(), "INVALID_SCOPE") {
			t.Fatalf("expected INVALID_SCOPE, got %v", err)
		}
	})

	t.Run("empty organizationId rejected", func(t *testing.T) {
		bad := company
		bad.OrganizationID = ""
		if err := AssertValidScope(bad); err == nil || !strings.Contains(err.Error(), "organizationId") {
			t.Fatalf("expected organizationId error, got %v", err)
		}
	})

	t.Run("empty companyId rejected", func(t *testing.T) {
		bad := company
		bad.CompanyID = ""
		if err := AssertValidScope(bad); err == nil || !strings.Contains(err.Error(), "companyId") {
			t.Fatalf("expected companyId error, got %v", err)
		}
	})

	t.Run("malformed ruc rejected", func(t *testing.T) {
		bad := company
		bad.RUC = "20100"
		if err := AssertValidScope(bad); err == nil || !strings.Contains(err.Error(), "INVALID_RUC") {
			t.Fatalf("expected INVALID_RUC, got %v", err)
		}
	})

	t.Run("malformed period rejected", func(t *testing.T) {
		bad := company
		bad.Period = "202413"
		if err := AssertValidScope(bad); err == nil || !strings.Contains(err.Error(), "INVALID_PERIOD") {
			t.Fatalf("expected INVALID_PERIOD, got %v", err)
		}
	})
}

func TestAssertValidContent(t *testing.T) {
	valid := Content{What: "w", Why: "y", Where: "r", Learned: "l"}
	if err := AssertValidContent(valid); err != nil {
		t.Fatalf("valid content rejected: %v", err)
	}
	for _, field := range []string{"what", "why", "where", "learned"} {
		bad := valid
		switch field {
		case "what":
			bad.What = "  "
		case "why":
			bad.Why = ""
		case "where":
			bad.Where = "\t"
		case "learned":
			bad.Learned = ""
		}
		if err := AssertValidContent(bad); err == nil || !strings.Contains(err.Error(), "INVALID_CONTENT") {
			t.Fatalf("field %q: expected INVALID_CONTENT, got %v", field, err)
		}
	}
}

func TestAssertValidSource(t *testing.T) {
	valid := Source{
		System:    "drenyra-core",
		Reference: "F001-948",
		ActorID:   "maria.torres",
		ActorKind: ActorKindHuman,
		Session:   "s-1",
	}
	if err := AssertValidSource(valid); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}

	t.Run("empty system rejected", func(t *testing.T) {
		bad := valid
		bad.System = ""
		if err := AssertValidSource(bad); err == nil || !strings.Contains(err.Error(), "system") {
			t.Fatalf("expected system error, got %v", err)
		}
	})

	t.Run("unknown actorKind rejected", func(t *testing.T) {
		bad := valid
		bad.ActorKind = "robot"
		if err := AssertValidSource(bad); err == nil || !strings.Contains(err.Error(), "actorKind") {
			t.Fatalf("expected actorKind error, got %v", err)
		}
	})

	t.Run("human without actorId rejected", func(t *testing.T) {
		bad := valid
		bad.ActorID = ""
		if err := AssertValidSource(bad); err == nil || !strings.Contains(err.Error(), "actorId") {
			t.Fatalf("expected actorId error, got %v", err)
		}
	})

	t.Run("agent without actorId is valid", func(t *testing.T) {
		agent := valid
		agent.ActorID = ""
		agent.ActorKind = ActorKindAgent
		if err := AssertValidSource(agent); err != nil {
			t.Fatalf("actorId-less agent source rejected: %v", err)
		}
	})

	t.Run("system actor without actorId is valid", func(t *testing.T) {
		sys := valid
		sys.ActorID = ""
		sys.ActorKind = ActorKindSystem
		if err := AssertValidSource(sys); err != nil {
			t.Fatalf("actorId-less system source rejected: %v", err)
		}
	})
}

func TestAssertValidValidity(t *testing.T) {
	if err := AssertValidValidity(nil); err != nil {
		t.Fatalf("nil validity rejected: %v", err)
	}
	valid := &Validity{EffectiveAt: "2024-01-01T00:00:00.000Z", ExpiresAt: "2025-01-01T00:00:00.000Z"}
	if err := AssertValidValidity(valid); err != nil {
		t.Fatalf("valid validity rejected: %v", err)
	}
	bad := &Validity{ExpiresAt: "yesterday-ish"}
	if err := AssertValidValidity(bad); err == nil || !strings.Contains(err.Error(), "INVALID_VALIDITY") {
		t.Fatalf("expected INVALID_VALIDITY, got %v", err)
	}
}

func TestScopeEqualsPeriodParticipates(t *testing.T) {
	base := Scope{
		Kind:           ScopeKindCompany,
		OrganizationID: "org-001",
		CompanyID:      "acme",
		RUC:            "20100039201",
		Period:         "202401",
	}

	t.Run("identical scopes are equal", func(t *testing.T) {
		if !ScopeEquals(base, base) {
			t.Fatal("identical scopes must be equal")
		}
	})

	t.Run("period participates in equality", func(t *testing.T) {
		other := base
		other.Period = ""
		if ScopeEquals(base, other) {
			t.Fatal("a perioded scope must never equal an unperioded one")
		}
	})

	t.Run("different ruc is a different scope", func(t *testing.T) {
		other := base
		other.RUC = "20600995804"
		if ScopeEquals(base, other) {
			t.Fatal("scopes differing in ruc must not be equal")
		}
	})

	t.Run("institutional scopes are equal to each other only", func(t *testing.T) {
		inst := Scope{Kind: ScopeKindInstitutional}
		if !ScopeEquals(inst, Scope{Kind: ScopeKindInstitutional}) {
			t.Fatal("institutional scopes must equal each other")
		}
		if ScopeEquals(inst, base) {
			t.Fatal("institutional must never equal a company scope")
		}
	})
}

func TestScopeKeyCanonical(t *testing.T) {
	company := Scope{
		Kind:           ScopeKindCompany,
		OrganizationID: "org-001",
		CompanyID:      "acme",
		RUC:            "20100039201",
		Period:         "202401",
	}
	if got := ScopeKey(Scope{Kind: ScopeKindInstitutional}); got != "institutional" {
		t.Fatalf("institutional scopeKey = %q, want institutional", got)
	}
	expected := "company\x00org-001\x00acme\x0020100039201\x00202401"
	if got := ScopeKey(company); got != expected {
		t.Fatalf("company scopeKey = %q, want %q", got, expected)
	}
	noPeriod := company
	noPeriod.Period = ""
	if got := ScopeKey(noPeriod); strings.HasSuffix(got, "\x00202401") {
		t.Fatalf("period-less scopeKey must not contain the period: %q", got)
	}
}

func TestParseDateTime(t *testing.T) {
	for _, ts := range []string{
		"2026-01-15T12:00:00.000Z",
		"2026-01-15T12:00:00Z",
		"2026-01-15 12:00:00",
		"2026-01-15",
	} {
		if _, ok := ParseDateTime(ts); !ok {
			t.Errorf("ParseDateTime(%q) failed", ts)
		}
	}
	if _, ok := ParseDateTime("not a date"); ok {
		t.Fatal("ParseDateTime accepted garbage")
	}
}

func TestCloneMemoryCopiesPointersAndSlices(t *testing.T) {
	memory := AccountingMemory{
		Identity:     Identity{ID: "id-1", TopicKey: "topic"},
		Title:        "t",
		Kind:         KindRule,
		Scope:        Scope{Kind: ScopeKindInstitutional},
		Content:      Content{What: "w", Why: "y", Where: "r", Learned: "l"},
		Status:       StatusActive,
		FiscalEffect: FiscalEffectNone,
		EffectiveAt:  "2026-01-15T12:00:00.000Z",
		RecordedAt:   "2026-01-15T12:00:00.000Z",
		Source:       Source{System: "s", ActorKind: ActorKindAgent},
		Validity:     &Validity{ExpiresAt: "2025-01-01T00:00:00.000Z"},
		EvidenceRefs: []string{"xml:1"},
		RuleRefs:     []string{"rule:1"},
		Confidence:   floatPtr(0.9),
		Materiality:  int64Ptr(1000),
		ContentHash:  "hash",
		Revision:     1,
	}
	cloned := CloneMemory(memory)
	cloned.Validity.ExpiresAt = "mutated"
	cloned.Confidence = floatPtr(0.1)
	cloned.Materiality = int64Ptr(1)
	cloned.EvidenceRefs[0] = "mutated"
	cloned.RuleRefs[0] = "mutated"

	if memory.Validity.ExpiresAt == "mutated" {
		t.Fatal("clone must not share the validity pointer")
	}
	if *memory.Confidence != 0.9 {
		t.Fatal("clone must not share the confidence pointer")
	}
	if *memory.Materiality != 1000 {
		t.Fatal("clone must not share the materiality pointer")
	}
	if memory.EvidenceRefs[0] == "mutated" || memory.RuleRefs[0] == "mutated" {
		t.Fatal("clone must not share the ref slices")
	}
}

func floatPtr(v float64) *float64 { return &v }
func int64Ptr(v int64) *int64     { return &v }
