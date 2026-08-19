// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module tests the operator tenant
// enumeration surface (sdd-060-tenant-cli, FR-TEN-1/AC-TEN-1): TenantList must
// return deterministic ids/counts from identities + observations, never
// per-tenant content. No monetary field exists anywhere in this file.
package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

func TestTenantList(t *testing.T) {
	s := openTestStorePath(t, filepath.Join(t.TempDir(), "engram.db"))

	// Two tenants via the identity seed (companies/memberships side).
	if err := s.SeedIdentity(IdentitySeed{
		TenantID: "org-tenant-a", CompanyID: "co_a", CompanyRUC: "20100039201",
		CompanyName: "Tenant A SAC", MembershipID: "mem-a-1", SubjectID: "alice.a",
		Roles: []auth.AccountingRole{auth.RoleRecordsComplianceOfficer},
	}); err != nil {
		t.Fatalf("seed identity A: %v", err)
	}
	if err := s.SeedIdentity(IdentitySeed{
		TenantID: "org-tenant-b", CompanyID: "co_b", CompanyRUC: "20600995804",
		CompanyName: "Tenant B SAC", MembershipID: "mem-b-1", SubjectID: "bob.b",
		Roles: []auth.AccountingRole{auth.RoleController},
	}); err != nil {
		t.Fatalf("seed identity B: %v", err)
	}

	scopeA1 := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-tenant-a", CompanyID: "co_a", RUC: "20100039201", Period: "202401"}
	scopeA2 := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-tenant-a", CompanyID: "co_a", RUC: "20100039201", Period: "202402"}
	scopeB1 := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "org-tenant-b", CompanyID: "co_b", RUC: "20600995804", Period: "202402"}

	for i, sc := range []core.Scope{scopeA1, scopeA2, scopeA1, scopeB1} {
		if _, err := s.Save(core.SaveInput{
			TopicKey:     "tenant/list/fixture-" + string(rune('a'+i)),
			Title:        "tenant list fixture",
			Kind:         core.KindFact,
			Scope:        sc,
			Content:      core.Content{What: "fixture content", Why: "test", Where: "internal/store", Learned: "n/a"},
			FiscalEffect: core.FiscalEffectNone,
			EffectiveAt:  "2024-01-15T00:00:00Z",
			Source:       core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
			Confidence:   0.8,
		}); err != nil {
			t.Fatalf("save fixture %d: %v", i, err)
		}
	}

	got, err := s.TenantList(context.Background())
	if err != nil {
		t.Fatalf("TenantList: %v", err)
	}

	want := core.TenantListResult{
		TotalTenants: 2,
		Tenants: []core.TenantSummary{
			{
				OrganizationID: "org-tenant-a",
				Companies: []core.TenantCompany{
					{CompanyID: "co_a", RUC: "20100039201", Name: "Tenant A SAC", MemoryCount: 3},
				},
				Periods:     []string{"202401", "202402"},
				MemoryCount: 3,
			},
			{
				OrganizationID: "org-tenant-b",
				Companies: []core.TenantCompany{
					{CompanyID: "co_b", RUC: "20600995804", Name: "Tenant B SAC", MemoryCount: 1},
				},
				Periods:     []string{"202402"},
				MemoryCount: 1,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TenantList:\n got %+v\nwant %+v", got, want)
	}
}

// TestTenantListEmpty asserts the deterministic empty document.
func TestTenantListEmpty(t *testing.T) {
	s := openTestStorePath(t, filepath.Join(t.TempDir(), "engram.db"))
	got, err := s.TenantList(context.Background())
	if err != nil {
		t.Fatalf("TenantList empty: %v", err)
	}
	want := core.TenantListResult{Tenants: []core.TenantSummary{}, TotalTenants: 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TenantList empty: got %+v want %+v", got, want)
	}
}
