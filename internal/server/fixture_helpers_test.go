// Shared test fixtures — the fictional tenant used by the close-fixture,
// rule-impact and rule-version test suites (design brief §7 demo boundary:
// NEVER real taxpayer data).
package server

import "github.com/arkelythex/drenyra-engram/internal/core"

// Frozen fixture identity (fictional — 20100039201 is a checksummed TEST RUC,
// not a real taxpayer; the comprobante adapter additionally requires the SUNAT
// mod-11 checksum and uses 20100070970 for its own fixtures).
const (
	fixtureOrg      = "cmp_org"
	fixtureCompany  = "cmp_01"
	fixtureRUC      = "20100039201"
	fixturePeriod   = "202601"
	ledger4011Ref   = "LEDGER//2026-01/4011" // external ledger source-of-truth ref (D-4)
	ruleRetentionV2 = "policy/tax/retention-rate"
)

// fixtureScope builds the exact company scope of the fictional fixture tenant.
func fixtureScope() core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: fixtureOrg,
		CompanyID:      fixtureCompany,
		RUC:            fixtureRUC,
		Period:         fixturePeriod,
	}
}

// ruleFixtureScope is an alias of fixtureScope used by the rule suites (the
// same fictional tenant).
func ruleFixtureScope() core.Scope {
	return fixtureScope()
}
