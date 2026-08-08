// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the pure retention-policy test suite (v0.8
// batch 2 — docs/architecture/evidence-lifecycle-v0.8.md §3.1/§6):
//
//   - the fail-closed validator matrix (exact company scope, jurisdiction
//     syntax, POLICY_EVIDENCE_REQUIRED, YYYYMM minPeriod, version ≥ 1, closed
//     enums) and the canonical defaults (dual-approver roles, blocking hold
//     kinds);
//   - the PURE eligibility matrix (eligible / not_due / unknown) against the
//     deployment-declared min_period floor — no statutory duration claim;
//   - the PURE exact resolution (highest version of an ENABLED policy; zero
//     matches and ambiguity fail closed; disabled/superseded rows never
//     resolve);
//   - the FROZEN canonical bytes pinned byte-identically with the TypeScript
//     mirror (fixed property order, compact UTF-8, NO HTML escaping) —
//     CROSS-RUNTIME PARITY: the same fixture and the same pinned literal are
//     shared with core/__tests__/retention-policy.test.ts.
package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// sampleRetentionPolicy is the Go↔TS parity fixture: a tenant-level policy
// (empty company/RUC/period) with the exact resolution evidence, the canonical
// (sorted) closed-enum arrays and the v1 chain position.
func sampleRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		PolicyID:             "00000000-0000-4000-8000-000000000001",
		TenantID:             "org-001",
		Jurisdiction:         "PE",
		Legislation:          "NATIONAL-TAX",
		Authority:            "tenant-records",
		Source:               "deployment decision 2026-08-07",
		Category:             "invoice",
		MinPeriod:            "202401",
		Version:              1,
		DualApprovalRequired: true,
		DualApproverRoles:    []string{"controller", "tax_responsible"},
		BlockingHoldKinds:    []string{"audit", "dispute", "fiscalization", "legal"},
		Enabled:              true,
		CreatedAt:            "2026-08-07T12:00:00.000Z",
		CreatedBy:            "subject-1",
	}
}

// PINNED_CANONICAL_JSON is the FROZEN canonical bytes of sampleRetentionPolicy —
// the exact same literal pinned in core/__tests__/retention-policy.test.ts
// (Go↔TS canonical bytes must match byte-identically: fixed property order,
// compact UTF-8, no HTML escaping). Field order: policyId, tenantId,
// (companyId/ruc/period omitted — tenant-level), jurisdiction, legislation,
// authority, source, category, minPeriod, version, (supersedesPolicyId
// omitted), dualApprovalRequired, dualApproverRoles, blockingHoldKinds,
// enabled, createdAt, createdBy.
const PINNED_CANONICAL_JSON = `{"policyId":"00000000-0000-4000-8000-000000000001","tenantId":"org-001","jurisdiction":"PE","legislation":"NATIONAL-TAX","authority":"tenant-records","source":"deployment decision 2026-08-07","category":"invoice","minPeriod":"202401","version":1,"dualApprovalRequired":true,"dualApproverRoles":["controller","tax_responsible"],"blockingHoldKinds":["audit","dispute","fiscalization","legal"],"enabled":true,"createdAt":"2026-08-07T12:00:00.000Z","createdBy":"subject-1"}`

func TestCanonicalRetentionPolicyJSONParityPinned(t *testing.T) {
	p := sampleRetentionPolicy()
	got := string(CanonicalRetentionPolicyJSON(p))
	if got != PINNED_CANONICAL_JSON {
		t.Fatalf("canonical retention policy bytes differ from the pinned Go↔TS literal\n got: %s\nwant: %s", got, PINNED_CANONICAL_JSON)
	}
	// The canonical bytes must be the WIRE bytes (round-trip through JSON).
	var decoded RetentionPolicy
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("canonical bytes must unmarshal: %v", err)
	}
	if decoded.PolicyID != p.PolicyID || decoded.Jurisdiction != p.Jurisdiction || decoded.Version != 1 {
		t.Fatalf("round-trip mismatch: %+v", decoded)
	}
}

func TestAssertValidRetentionPolicyMatrix(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*RetentionPolicy)
		wantErr string
	}{
		{"valid tenant-level policy", func(p *RetentionPolicy) {}, ""},
		{"company-scoped policy is valid", func(p *RetentionPolicy) {
			p.CompanyID, p.RUC, p.Period = "acme", "20100039201", "202401"
		}, ""},
		{"institutional scope rejected", func(p *RetentionPolicy) {
			p.TenantID = ""
		}, "INVALID_RETENTION_SCOPE"},
		{"jurisdiction syntax lower-case rejected", func(p *RetentionPolicy) {
			p.Jurisdiction = "pe"
		}, "INVALID_RETENTION_JURISDICTION"},
		{"jurisdiction too short rejected", func(p *RetentionPolicy) {
			p.Jurisdiction = "P"
		}, "INVALID_RETENTION_JURISDICTION"},
		{"jurisdiction too long rejected", func(p *RetentionPolicy) {
			p.Jurisdiction = "P" + strings.Repeat("A", 16)
		}, "INVALID_RETENTION_JURISDICTION"},
		{"legislation missing", func(p *RetentionPolicy) {
			p.Legislation = ""
		}, "POLICY_EVIDENCE_REQUIRED"},
		{"authority missing", func(p *RetentionPolicy) {
			p.Authority = " "
		}, "POLICY_EVIDENCE_REQUIRED"},
		{"source missing", func(p *RetentionPolicy) {
			p.Source = ""
		}, "POLICY_EVIDENCE_REQUIRED"},
		{"category missing", func(p *RetentionPolicy) {
			p.Category = ""
		}, "INVALID_RETENTION_CATEGORY"},
		{"minPeriod malformed", func(p *RetentionPolicy) {
			p.MinPeriod = "2024011"
		}, "INVALID_RETENTION_MIN_PERIOD"},
		{"minPeriod month 13 rejected", func(p *RetentionPolicy) {
			p.MinPeriod = "202413"
		}, "INVALID_RETENTION_MIN_PERIOD"},
		{"version zero rejected", func(p *RetentionPolicy) {
			p.Version = 0
		}, "INVALID_RETENTION_VERSION"},
		{"dual approver role outside closed set", func(p *RetentionPolicy) {
			p.DualApproverRoles = []string{"controller", "admin"}
		}, "INVALID_RETENTION_DUALAPPROVERROLES"},
		{"hold kind outside closed set", func(p *RetentionPolicy) {
			p.BlockingHoldKinds = []string{"legal", "custom"}
		}, "INVALID_RETENTION_BLOCKINGHOLDKINDS"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p := sampleRetentionPolicy()
			tt.mutate(&p)
			err := AssertValidRetentionPolicy(&p)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want a %q error", err, tt.wantErr)
			}
		})
	}
}

func TestAssertValidRetentionPolicyCanonicalDefaults(t *testing.T) {
	p := sampleRetentionPolicy()
	p.DualApproverRoles = nil
	p.BlockingHoldKinds = nil
	if err := AssertValidRetentionPolicy(&p); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	if len(p.DualApproverRoles) != 2 || p.DualApproverRoles[0] != "controller" || p.DualApproverRoles[1] != "tax_responsible" {
		t.Fatalf("dual approver roles = %v, want the frozen default", p.DualApproverRoles)
	}
	if len(p.BlockingHoldKinds) != 4 || p.BlockingHoldKinds[0] != "audit" || p.BlockingHoldKinds[3] != "legal" {
		t.Fatalf("blocking hold kinds = %v, want the frozen default sorted", p.BlockingHoldKinds)
	}
	// Duplicates are deduplicated into canonical sorted form.
	p.DualApproverRoles = []string{"tax_responsible", "controller", "tax_responsible"}
	if err := AssertValidRetentionPolicy(&p); err != nil {
		t.Fatalf("dedupe must validate: %v", err)
	}
	if p.DualApproverRoles[0] != "controller" || len(p.DualApproverRoles) != 2 {
		t.Fatalf("roles = %v, want canonical sorted deduped", p.DualApproverRoles)
	}
}

func TestEvaluateRetentionEligibilityMatrix(t *testing.T) {
	// The pure dimension: eligible when the object's period REACHED the
	// deployment-declared min_period floor (zero-padded YYYYMM — lexicographic
	// order IS chronological order); not_due otherwise; unknown on any invalid
	// input. NO statutory duration is asserted anywhere.
	cases := []struct {
		name         string
		minPeriod    string
		objectPeriod string
		want         RetentionEligibility
	}{
		{"object reached the floor exactly", "202401", "202401", RetentionEligibilityEligible},
		{"object after the floor", "202401", "202412", RetentionEligibilityEligible},
		{"object before the floor", "202401", "202312", RetentionEligibilityNotDue},
		{"empty object period is unknown", "202401", "", RetentionEligibilityUnknown},
		{"malformed object period is unknown", "202401", "20241", RetentionEligibilityUnknown},
		{"empty min period is unknown", "", "202401", RetentionEligibilityUnknown},
		{"malformed min period is unknown", "202413", "202401", RetentionEligibilityUnknown},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p := sampleRetentionPolicy()
			p.MinPeriod = tt.minPeriod
			got := EvaluateRetentionEligibility(p, tt.objectPeriod)
			if got != tt.want {
				t.Fatalf("EvaluateRetentionEligibility(%q, %q) = %q, want %q", tt.minPeriod, tt.objectPeriod, got, tt.want)
			}
		})
	}
}

func TestResolveRetentionPolicyExactness(t *testing.T) {
	scope := Scope{Kind: ScopeKindCompany, OrganizationID: "org-001", CompanyID: "acme", RUC: "20100039201", Period: "202401"}
	base := sampleRetentionPolicy()
	base.TenantID = scope.OrganizationID
	base.CompanyID = scope.CompanyID
	base.RUC = scope.RUC
	base.Period = scope.Period

	v1 := base
	v1.PolicyID = "00000000-0000-4000-8000-000000000001"
	v1.Version = 1
	v2 := base
	v2.PolicyID = "00000000-0000-4000-8000-000000000002"
	v2.Version = 2
	v2.SupersedesPolicyID = v1.PolicyID
	v2.MinPeriod = "202501"

	// Highest version of an ENABLED policy wins.
	got, ok := ResolveRetentionPolicy([]RetentionPolicy{v1, v2}, scope, "PE", "NATIONAL-TAX", "invoice")
	if !ok || got.Version != 2 || got.PolicyID != v2.PolicyID {
		t.Fatalf("resolve = (%+v, %v), want v2", got, ok)
	}

	// Disabled candidate never resolves.
	v2Disabled := v2
	v2Disabled.Enabled = false
	got, ok = ResolveRetentionPolicy([]RetentionPolicy{v1, v2Disabled}, scope, "PE", "NATIONAL-TAX", "invoice")
	if !ok || got.Version != 1 {
		t.Fatalf("disabled v2 must not resolve; got (%+v, %v)", got, ok)
	}

	// Exact scope is part of the key: a different company never resolves.
	otherScope := scope
	otherScope.CompanyID = "other"
	otherScope.RUC = "20600995804"
	if _, ok := ResolveRetentionPolicy([]RetentionPolicy{v1, v2}, otherScope, "PE", "NATIONAL-TAX", "invoice"); ok {
		t.Fatal("a policy of another exact scope must never resolve (scope-first)")
	}

	// Jurisdiction/legislation/category are exact.
	for _, tuple := range [][3]string{
		{"CL", "NATIONAL-TAX", "invoice"},
		{"PE", "OTHER-REGIME", "invoice"},
		{"PE", "NATIONAL-TAX", "cdr"},
	} {
		if _, ok := ResolveRetentionPolicy([]RetentionPolicy{v1, v2}, scope, tuple[0], tuple[1], tuple[2]); ok {
			t.Fatalf("tuple %v must not resolve", tuple)
		}
	}

	// Zero matches → ok=false (the store fails closed UNKNOWN_RETENTION_STATE).
	if _, ok := ResolveRetentionPolicy(nil, scope, "PE", "NATIONAL-TAX", "invoice"); ok {
		t.Fatal("no policies must not resolve")
	}

	// Ambiguity (two enabled rows sharing the highest version — the schema
	// UNIQUE makes it a corruption backstop) fails closed: never guess.
	dup := v2
	dup.PolicyID = "00000000-0000-4000-8000-000000000099"
	if _, ok := ResolveRetentionPolicy([]RetentionPolicy{v2, dup}, scope, "PE", "NATIONAL-TAX", "invoice"); ok {
		t.Fatal("ambiguous candidates must not resolve")
	}
}
