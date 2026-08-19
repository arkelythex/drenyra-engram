// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the G-10 store read (design
// D-3): LatestMaterialDecisionHeads is a SINGLE SQL read scoped to the exact
// company scope tuple + byte-equal period, selecting only the MAXIMUM revision
// per (topic_key, exact scope) chain and applying the FZ-1 status/fiscal-effect/
// materiality predicates in SQL. Structural isolation is enforced INSIDE the
// query — another company's or period's rows are never loaded, never
// post-filtered. READ-ONLY: no transaction is ever started and no row is ever
// written (the same no-transaction discipline as SigningKeyForVerify).
package store

import (
	"context"
	"fmt"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// LatestMaterialDecisionHeads returns the FZ-1 material-decision heads of ONE
// exact company scope + period: the latest chain revision per (topic_key, exact
// scope) that is StatusApproved, has one of the six frozen fiscal effects and a
// declared material/critical materiality level. The query is fully scoped in
// SQL and returns the heads deterministically ordered by decision ID (bytewise
// ascending). A valid scope with no matching memories yields zero heads and no
// error — the metric reports zeroDenominator, never a failure. A missing,
// ambiguous or malformed scope/period fails closed with a typed error
// (FR-9 (i)); cross-tenant or partial aggregation is forbidden (IR-2).
func (s *SQLiteStore) LatestMaterialDecisionHeads(ctx context.Context, scope core.Scope) ([]core.AccountingMemory, error) {
	// FR-9 (i) fail-closed scope/period validation: the metric takes exactly one
	// company scope and one period.
	if scope.Kind != core.ScopeKindCompany {
		return nil, fmt.Errorf("INVALID_RECONSTRUCTIBILITY_SCOPE: the metric requires an exact company scope, got kind %q", scope.Kind)
	}
	if scope.Period == "" {
		return nil, fmt.Errorf("INVALID_RECONSTRUCTIBILITY_SCOPE: period is required (YYYYMM) for an exact company scope")
	}
	if err := core.AssertValidScope(scope); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT `+memoryColumns+` FROM observations
		WHERE scope_kind = 'company'
		  AND organization_id = ?
		  AND company_id = ?
		  AND ruc = ?
		  AND period = ?
		  AND status = ?
		  AND fiscal_effect IN (?, ?, ?, ?, ?, ?)
		  AND materiality_level IN (?, ?)
		  AND revision = (
			SELECT MAX(o2.revision) FROM observations o2
			WHERE o2.topic_key = observations.topic_key
			  AND o2.scope_kind = observations.scope_kind
			  AND o2.organization_id = observations.organization_id
			  AND o2.company_id = observations.company_id
			  AND o2.ruc = observations.ruc
			  AND o2.period = observations.period
		  )
		ORDER BY id ASC`,
		scope.OrganizationID, scope.CompanyID, scope.RUC, scope.Period,
		string(core.StatusApproved),
		string(core.FiscalEffectJournalEntry), string(core.FiscalEffectAdjustment),
		string(core.FiscalEffectReclassification), string(core.FiscalEffectDeclaration),
		string(core.FiscalEffectClosing), string(core.FiscalEffectSunatFiling),
		string(core.MaterialityMaterial), string(core.MaterialityCritical),
	)
	if err != nil {
		return nil, fmt.Errorf("latest material decision heads: %w", err)
	}
	memories := make([]core.AccountingMemory, 0)
	for rows.Next() {
		memory, err := scanMemory(rows, s.encMaster)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		memories = append(memories, memory)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// Close the scan BEFORE resolving links: with MaxOpenConns(1), querying the
	// link tables while this Rows is still open deadlocks on the single
	// connection (the nested query waits for the connection the open Rows holds).
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range memories {
		memories[i] = s.withLinks(memories[i])
	}
	return memories, nil
}
