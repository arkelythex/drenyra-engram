// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module contains no monetary fields and
// tests the observation core model validators and scope helpers.

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

func TestAssertValidProvenance(t *testing.T) {
	valid := Provenance{Actor: "agent", Timestamp: "2026-01-15T12:00:00.000Z", Source: "cli"}
	if err := AssertValidProvenance(valid); err != nil {
		t.Fatalf("valid provenance rejected: %v", err)
	}

	t.Run("empty actor rejected", func(t *testing.T) {
		bad := valid
		bad.Actor = ""
		if err := AssertValidProvenance(bad); err == nil || !strings.Contains(err.Error(), "actor") {
			t.Fatalf("expected actor error, got %v", err)
		}
	})

	t.Run("unparseable timestamp rejected", func(t *testing.T) {
		bad := valid
		bad.Timestamp = "not-a-date"
		if err := AssertValidProvenance(bad); err == nil || !strings.Contains(err.Error(), "timestamp") {
			t.Fatalf("expected timestamp error, got %v", err)
		}
	})

	t.Run("empty source rejected", func(t *testing.T) {
		bad := valid
		bad.Source = ""
		if err := AssertValidProvenance(bad); err == nil || !strings.Contains(err.Error(), "source") {
			t.Fatalf("expected source error, got %v", err)
		}
	})

	t.Run("session is optional", func(t *testing.T) {
		if err := AssertValidProvenance(valid); err != nil {
			t.Fatalf("session-less provenance rejected: %v", err)
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

func TestCloneObservationCopiesValidity(t *testing.T) {
	obs := Observation{
		Identity:        Identity{ID: "id-1", TopicKey: "topic"},
		Title:           "t",
		Type:            "policy",
		Scope:           Scope{Kind: ScopeKindInstitutional},
		Content:         Content{What: "w", Why: "y", Where: "r", Learned: "l"},
		AuthorityStatus: StatusDraft,
		Validity:        &Validity{ExpiresAt: "2025-01-01T00:00:00.000Z"},
		Provenance:      Provenance{Actor: "a", Timestamp: "2026-01-15T12:00:00.000Z", Source: "s"},
		Revision:        1,
	}
	cloned := CloneObservation(obs)
	cloned.Validity.ExpiresAt = "mutated"
	if obs.Validity.ExpiresAt == "mutated" {
		t.Fatal("clone must not share the validity pointer")
	}
}
