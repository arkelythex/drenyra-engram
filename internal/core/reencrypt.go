// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module carries the pure report shape of
// the legacy re-encryption operator surface (sdd-060-legacy-reencrypt,
// FR-RE-2/4): per-tenant legacy-row counts, ids/counts only — never content.
// No monetary field exists anywhere in this file.
package core

// ReencryptTenantCount is one tenant's legacy-plaintext row count.
type ReencryptTenantCount struct {
	OrganizationID string `json:"organizationId"`
	LegacyRows     int    `json:"legacyRows"`
}

// ReencryptReport is the `encrypt` command document (dry-run and apply):
// how many company-scope legacy rows exist / were re-encrypted, per tenant.
type ReencryptReport struct {
	DryRun      bool                   `json:"dryRun"`
	TotalLegacy int                    `json:"totalLegacy"`
	PerTenant   []ReencryptTenantCount `json:"perTenant"`
}
