package core

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type fiscalScopeGolden struct {
	Binding      FiscalScopeBinding `json:"binding"`
	CanonicalHex string             `json:"canonicalHex"`
	Hash         string             `json:"hash"`
}

type fiscalRUCGolden struct {
	Cases []struct {
		Input          string            `json:"input"`
		Classification RUCClassification `json:"classification"`
	} `json:"cases"`
}

func loadFiscalScopeGolden(t *testing.T) fiscalScopeGolden {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/golden/fiscal-scope-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector fiscalScopeGolden
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	return vector
}

func TestFiscalRUCGolden(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/golden/fiscal-ruc-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector fiscalRUCGolden
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	for _, test := range vector.Cases {
		t.Run(test.Input, func(t *testing.T) {
			if got := ClassifyRUC(test.Input); got != test.Classification {
				t.Fatalf("classification = %s, want %s", got, test.Classification)
			}
		})
	}
}

func TestFiscalScopeV1Golden(t *testing.T) {
	vector := loadFiscalScopeGolden(t)
	if err := ValidateFiscalScopeBinding(vector.Binding); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got := CanonicalFiscalScopeBytes(vector.Binding)
	if hex.EncodeToString(got) != vector.CanonicalHex {
		t.Fatalf("canonical bytes differ")
	}
	if hash := FiscalScopeHash(vector.Binding); hash != vector.Hash {
		t.Fatalf("hash = %s, want %s", hash, vector.Hash)
	}
}

func TestFiscalScopeV1EveryElementIsBound(t *testing.T) {
	base := loadFiscalScopeGolden(t).Binding
	mutations := map[string]func(*FiscalScopeBinding){
		"tenant":         func(v *FiscalScopeBinding) { v.Tenant += "-x" },
		"organization":   func(v *FiscalScopeBinding) { v.Organization += "-x" },
		"company":        func(v *FiscalScopeBinding) { v.Company = "20600055519" },
		"fiscalPeriod":   func(v *FiscalScopeBinding) { v.FiscalPeriod = "202602" },
		"ledgerBook":     func(v *FiscalScopeBinding) { v.LedgerBook += "-x" },
		"operationType":  func(v *FiscalScopeBinding) { v.OperationType = "memory.supersede" },
		"sourceSnapshot": func(v *FiscalScopeBinding) { v.SourceSnapshot = "a" + v.SourceSnapshot[1:] },
		"policyVersion":  func(v *FiscalScopeBinding) { v.PolicyVersion += "-x" },
		"actor":          func(v *FiscalScopeBinding) { v.Actor += "-x" },
		"authorityLevel": func(v *FiscalScopeBinding) { v.AuthorityLevel = AuthorityLevelAsk },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if FiscalScopeHash(changed) == FiscalScopeHash(base) {
				t.Fatalf("%s was not hash-bound", name)
			}
		})
	}
}

func TestDecodeFiscalScopeV1JSONFailsClosed(t *testing.T) {
	valid := `{"version":"v1","tenant":"t","organization":"o","company":"20100070970","fiscalPeriod":"202601","ledgerBook":"purchases","operationType":"memory.save","sourceSnapshot":"86f434b33c9e8cd20034742fd503a270aebf5909bf0a34cb177651cd895e5b2a","policyVersion":"p","actor":"a","authorityLevel":"PREPARE"}`
	tests := []struct{ name, raw, code string }{
		{"valid", valid, ""},
		{"duplicate", `{"version":"v1","tenant":"t","tenant":"x"}`, ScopeBindingInvalid},
		{"reordered", `{"tenant":"t","version":"v1"}`, ScopeBindingInvalid},
		{"missing", `{"version":"v1"}`, ScopeBindingRequired},
		{"unknown version", `{"version":"v2"}`, UnsupportedScopeVersion},
		{"unknown field", strings.Replace(valid, `"authorityLevel":"PREPARE"`, `"extra":"x","authorityLevel":"PREPARE"`, 1), ScopeBindingInvalid},
		{"invalid period", strings.Replace(valid, "202601", "202613", 1), ScopeBindingInvalid},
		{"invalid authority", strings.Replace(valid, "PREPARE", "EXECUTE", 1), ScopeBindingInvalid},
		{"nul", `{"version":"v1","tenant":"t\u0000x","organization":"o","company":"20100070970","fiscalPeriod":"202601","ledgerBook":"purchases","operationType":"memory.save","sourceSnapshot":"86f434b33c9e8cd20034742fd503a270aebf5909bf0a34cb177651cd895e5b2a","policyVersion":"p","actor":"a","authorityLevel":"PREPARE"}`, ScopeBindingInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeFiscalScopeV1JSON([]byte(tt.raw))
			if tt.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var scopeErr *FiscalScopeError
			if !errors.As(err, &scopeErr) || scopeErr.Code != tt.code {
				t.Fatalf("error = %v, want %s", err, tt.code)
			}
		})
	}
}

func TestDecodeFiscalScopeV1JSONRejectsInvalidUTF8(t *testing.T) {
	raw := []byte(`{"version":"v1","tenant":"x","organization":"o","company":"20100070970","fiscalPeriod":"202601","ledgerBook":"purchases","operationType":"memory.save","sourceSnapshot":"86f434b33c9e8cd20034742fd503a270aebf5909bf0a34cb177651cd895e5b2a","policyVersion":"p","actor":"a","authorityLevel":"PREPARE"}`)
	raw[strings.Index(string(raw), `"tenant":"x"`)+len(`"tenant":"`)] = 0xff

	_, err := DecodeFiscalScopeV1JSON(raw)
	var scopeErr *FiscalScopeError
	if !errors.As(err, &scopeErr) || scopeErr.Code != ScopeBindingInvalid {
		t.Fatalf("error = %v, want %s", err, ScopeBindingInvalid)
	}
}

func TestCanonicalFiscalScopeBytesUsesUTF8ByteLength(t *testing.T) {
	binding := loadFiscalScopeGolden(t).Binding
	binding.Actor = "agente:🧾"
	if err := ValidateFiscalScopeBinding(binding); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if canonical := string(CanonicalFiscalScopeBytes(binding)); !strings.Contains(canonical, "actor=11:agente:🧾\x00") {
		t.Fatalf("canonical bytes use the wrong UTF-8 actor length: %q", canonical)
	}
}

func TestReviewAcknowledgementStates(t *testing.T) {
	cases := []struct {
		name  string
		value ReviewAcknowledgement
		want  ReviewAcknowledgementState
	}{
		{"omitted", ReviewAcknowledgement{}, ReviewAcknowledgementOmitted},
		{"false", ReviewAcknowledgement{Present: true}, ReviewAcknowledgementFalse},
		{"true", ReviewAcknowledgement{Present: true, Value: true}, ReviewAcknowledgementTrue},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.State(); got != tt.want {
				t.Fatalf("state = %s, want %s", got, tt.want)
			}
		})
	}
}
