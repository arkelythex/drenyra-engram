// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the operator tenant surface
// of the SQLite store (sdd-060-tenant-cli, FR-TEN-1/FR-TEN-3): read-only
// tenant enumeration and topic-key drift candidates. It never exposes
// per-tenant CONTENT (topic keys/narrative) and never writes — merge happens
// through the existing supersede path (D-TEN-3). No schema change (D-TEN-7).
package store

import (
	"context"
	"sort"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// TenantList returns the deterministic operator enumeration of tenants present
// in the store (identities UNION observations): ids/counts only, ordered by
// organizationId then companyId, periods sorted (FR-TEN-1/AC-TEN-1). It is an
// OPERATOR surface — no per-tenant content is read or emitted.
func (s *SQLiteStore) TenantList(ctx context.Context) (core.TenantListResult, error) {
	result := core.TenantListResult{Tenants: []core.TenantSummary{}}
	tenantIdx := map[string]int{}       // organizationId → index in result.Tenants
	companyCount := map[[2]string]int{} // {organizationId, companyId} → memory count
	companyRUC := map[[2]string]string{}
	periodsByTenant := map[string]map[string]struct{}{}

	// Identity side: companies rows carry the identity-known tenant/company/RUC/name.
	compRows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id, id, ruc, name FROM companies ORDER BY tenant_id, id`)
	if err != nil {
		return core.TenantListResult{}, err
	}
	for compRows.Next() {
		var tenantID, companyID, ruc, name string
		if err := compRows.Scan(&tenantID, &companyID, &ruc, &name); err != nil {
			_ = compRows.Close()
			return core.TenantListResult{}, err
		}
		idx, ok := tenantIdx[tenantID]
		if !ok {
			result.Tenants = append(result.Tenants, core.TenantSummary{OrganizationID: tenantID, Companies: []core.TenantCompany{}})
			idx = len(result.Tenants) - 1
			tenantIdx[tenantID] = idx
		}
		result.Tenants[idx].Companies = append(result.Tenants[idx].Companies,
			core.TenantCompany{CompanyID: companyID, RUC: ruc, Name: name})
	}
	if err := compRows.Close(); err != nil {
		return core.TenantListResult{}, err
	}
	if err := compRows.Err(); err != nil {
		return core.TenantListResult{}, err
	}

	// Data side: company-kind observations grouped by the exact scope tuple.
	obsRows, err := s.db.QueryContext(ctx,
		`SELECT organization_id, company_id, ruc, period, COUNT(*)
		 FROM observations
		 WHERE scope_kind = ?
		 GROUP BY organization_id, company_id, ruc, period
		 ORDER BY organization_id, company_id, period`, string(core.ScopeKindCompany))
	if err != nil {
		return core.TenantListResult{}, err
	}
	defer func() { _ = obsRows.Close() }()
	for obsRows.Next() {
		var orgID, companyID, ruc, period string
		var count int
		if err := obsRows.Scan(&orgID, &companyID, &ruc, &period, &count); err != nil {
			return core.TenantListResult{}, err
		}
		if orgID == "" && companyID == "" {
			continue // never surface empty-scope rows
		}
		idx, ok := tenantIdx[orgID]
		if !ok {
			result.Tenants = append(result.Tenants, core.TenantSummary{OrganizationID: orgID, Companies: []core.TenantCompany{}})
			idx = len(result.Tenants) - 1
			tenantIdx[orgID] = idx
		}
		key := [2]string{orgID, companyID}
		companyCount[key] += count
		if ruc != "" {
			companyRUC[key] = ruc
		}
		if periodsByTenant[orgID] == nil {
			periodsByTenant[orgID] = map[string]struct{}{}
		}
		if period != "" {
			periodsByTenant[orgID][period] = struct{}{}
		}
	}
	if err := obsRows.Err(); err != nil {
		return core.TenantListResult{}, err
	}

	// Merge data-side counts/periods into the identity-side summaries.
	for i := range result.Tenants {
		t := &result.Tenants[i]
		periods := periodsByTenant[t.OrganizationID]
		for j := range t.Companies {
			c := &t.Companies[j]
			key := [2]string{t.OrganizationID, c.CompanyID}
			if n := companyCount[key]; n > 0 {
				c.MemoryCount = n
			}
			if ruc := companyRUC[key]; ruc != "" && c.RUC == "" {
				c.RUC = ruc
			}
			t.MemoryCount += c.MemoryCount
		}
		if periods != nil {
			for p := range periods {
				t.Periods = append(t.Periods, p)
			}
			sort.Strings(t.Periods)
		}
		sort.Slice(t.Companies, func(a, b int) bool { return t.Companies[a].CompanyID < t.Companies[b].CompanyID })
	}
	sort.Slice(result.Tenants, func(a, b int) bool {
		return result.Tenants[a].OrganizationID < result.Tenants[b].OrganizationID
	})
	result.TotalTenants = len(result.Tenants)
	return result, nil
}

// DriftCandidates returns topic-key drift groups for ONE tenant
// (FR-TEN-3/FR-TEN-7): chains whose FoldTopicKey collides but raw topic keys
// differ. The scope pins organization/company/RUC; an EMPTY period means the
// WHOLE tenant — groups are then per (folded key, period) so a merge always
// stays inside one exact period scope. The canonical candidate per group is the
// raw key with the most observations (ties broken lexicographically). Chains
// outside the tenant tuple are never returned (fail closed). Read-only — no
// writes.
func (s *SQLiteStore) DriftCandidates(ctx context.Context, scope core.Scope) ([]core.DriftGroup, error) {
	where, args := tenantScopeWhere(scope)
	rows, err := s.db.QueryContext(ctx,
		`SELECT topic_key, period, COUNT(*) FROM observations WHERE `+where+` GROUP BY topic_key, period`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	// (folded, period) → rawKey → count
	type rawKey struct {
		key   string
		count int
	}
	groups := map[[2]string][]rawKey{}
	for rows.Next() {
		var key, period string
		var count int
		if err := rows.Scan(&key, &period, &count); err != nil {
			return nil, err
		}
		folded := core.FoldTopicKey(key)
		groupKey := [2]string{folded, period}
		groups[groupKey] = append(groups[groupKey], rawKey{key: key, count: count})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	drift := []core.DriftGroup{}
	for groupKey, keys := range groups {
		if len(keys) < 2 {
			continue // no drift: a single raw key per folded form in this period
		}
		sort.Slice(keys, func(a, b int) bool {
			if keys[a].count != keys[b].count {
				return keys[a].count > keys[b].count // most observations first
			}
			return keys[a].key < keys[b].key // tie → lexicographic
		})
		group := core.DriftGroup{
			Period:             groupKey[1],
			Canonical:          keys[0].key,
			CanonicalChainSize: keys[0].count,
			Drifted:            []core.DriftedChain{},
		}
		for _, k := range keys[1:] {
			group.Drifted = append(group.Drifted, core.DriftedChain{TopicKey: k.key, ChainSize: k.count})
		}
		drift = append(drift, group)
	}
	sort.Slice(drift, func(a, b int) bool {
		if drift[a].Period != drift[b].Period {
			return drift[a].Period < drift[b].Period
		}
		return drift[a].Canonical < drift[b].Canonical
	})
	return drift, nil
}

// tenantScopeWhere pins the tenant tuple (kind/organization/company/RUC) and
// pins the period ONLY when it is non-empty — an empty period scans the whole
// tenant (FR-TEN-2: --period optional, empty = whole tenant). Company-kind only;
// institutional never reaches the tenant surface.
func tenantScopeWhere(scope core.Scope) (string, []any) {
	if scope.Period == "" {
		return `scope_kind = 'company' AND organization_id = ? AND company_id = ? AND ruc = ?`,
			[]any{scope.OrganizationID, scope.CompanyID, scope.RUC}
	}
	return scopeWhere(scope)
}
