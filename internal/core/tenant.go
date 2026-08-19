// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module carries the pure tenant-summary
// shapes of the operator CLI surface (sdd-060-tenant-cli, FR-TEN-1): tenant
// enumeration emits ids/counts only — NEVER per-tenant content (topic keys,
// narrative). No monetary field exists anywhere in this file.
package core

// TenantCompany summarizes one company under a tenant. Name is the
// identity-known company name when a companies row exists ("" otherwise).
type TenantCompany struct {
	CompanyID   string `json:"companyId"`
	RUC         string `json:"ruc"`
	Name        string `json:"name"`
	MemoryCount int    `json:"memoryCount"`
}

// TenantSummary enumerates one tenant's companies, periods and memory volume.
// It is an OPERATOR summary (ids/counts only), never a data-read surface.
type TenantSummary struct {
	OrganizationID string          `json:"organizationId"`
	Companies      []TenantCompany `json:"companies"`
	Periods        []string        `json:"periods"`
	MemoryCount    int             `json:"memoryCount"`
}

// TenantListResult is the `tenant list` document: deterministic ordering
// (organizationId, then companyId, then period).
type TenantListResult struct {
	Tenants      []TenantSummary `json:"tenants"`
	TotalTenants int             `json:"totalTenants"`
}
