// Rule surfaces — Phase 6 v0.6.0, design §5/§6.
//
// RuleImpact reconstructs regulatory-change impact: which decisions referenced
// this rule chain, and does each consuming interval overlap the SELECTED
// changed revision's vigencia window? READ-ONLY — never mutates status, links,
// receipts, or envelopes; never asserts fiscal or accounting correctness
// (design §1 correctness boundary).
package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// RuleImpactStore is the narrow read surface the impact/history services
// delegate to (tenant visibility is enforced INSIDE the reverse query).
type RuleImpactStore interface {
	RuleLinksByRef(ctx context.Context, organizationID, companyID, ref string) ([]core.RuleLinkConsuming, error)
	FindByID(id string) (core.AccountingMemory, bool)
	FindChain(topicKey string, scope core.Scope) ([]core.AccountingMemory, error)
}

// RuleImpact computes the impact of one rule chain revision (the caller's
// revision, else the chain head) on every consuming memory, TENANT-VISIBLE.
// A scope selector pins the exact chain; without one, the chain is derived
// from the consuming links' pinned versions — multiple distinct chains fail
// RULE_CHAIN_AMBIGUOUS rather than merge (design §5).
func RuleImpact(ctx context.Context, st RuleImpactStore, organizationID, topicKey string, scope *core.Scope, revision int) (core.RuleImpactResult, error) {
	if strings.TrimSpace(organizationID) == "" || strings.TrimSpace(topicKey) == "" {
		return core.RuleImpactResult{}, auth.New(auth.CodeInvalidTransition, "rule impact requires tenant and topic key")
	}
	links, err := st.RuleLinksByRef(ctx, organizationID, companyIDFor(scope), topicKey)
	if err != nil {
		return core.RuleImpactResult{}, fmt.Errorf("persistence error: rule links by ref: %w", err)
	}

	// Resolve the rule chain: explicit scope, or derived from the pins.
	chain, err := resolveRuleChain(ctx, st, organizationID, topicKey, scope, links)
	if err != nil {
		return core.RuleImpactResult{}, err
	}
	if len(chain) == 0 {
		return core.RuleImpactResult{}, auth.New(auth.CodeMemoryNotFound, "no rule chain found for "+topicKey)
	}
	// Selected revision: caller's numeric revision (1-based) or the chain head.
	selected := chain[len(chain)-1]
	if revision >= 1 {
		if revision > len(chain) {
			return core.RuleImpactResult{}, auth.New(auth.CodeInvalidTransition, fmt.Sprintf("revision %d out of range: chain has %d revisions", revision, len(chain)))
		}
		selected = chain[revision-1]
	}

	result := core.RuleImpactResult{
		Ref:              topicKey,
		SelectedRevision: selected.Identity.ID,
		Revision:         revisionNum(chain, selected.Identity.ID),
		Items:            []core.RuleImpactItem{},
	}
	if selected.PolicyRule != nil {
		result.Jurisdiction = selected.PolicyRule.Jurisdiction
	}
	result.Validity = cloneValidity(selected.Validity)

	// Changed window: the selected revision's vigencia ([start, end), half-open).
	changedStart, changedEnd := validityWindow(selected.Validity)
	for _, link := range links {
		item := core.RuleImpactItem{
			MemoryID:      link.MemoryID,
			TopicKey:      link.ConsumingTopicKey,
			Kind:          link.ConsumingKind,
			Status:        link.ConsumingStatus,
			DecisionTime:  link.DecisionTime,
			LinkedVersion: link.LinkedVersion,
		}
		if link.LinkedVersion == "" {
			item.Outcome = core.RuleImpactLegacyUnpinned
			item.Detail = "legacy rule ref without a pinned version"
			result.UnresolvedLegacy++
		} else if rule, ok := st.FindByID(link.LinkedVersion); ok && rule.Kind == core.KindRule && rule.Identity.TopicKey == topicKey {
			item.ResolvedVersion = link.LinkedVersion
			if rule.PolicyRule != nil {
				item.Jurisdiction = rule.PolicyRule.Jurisdiction
			}
			item.Outcome = core.RuleImpactResolved
			item.Detail = "pinned rule revision resolved"
		} else {
			item.Outcome = core.RuleImpactFailed
			item.Detail = "pinned rule revision missing or not a matching rule"
		}
		// Pure overlap classification (Go↔TS mirrored).
		dStart, dEnd := link.Interval()
		item = core.ClassifyRuleImpactItem(item, dStart, dEnd, changedStart, changedEnd)
		item = core.NormalizeRuleImpactItem(item)
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// companyIDFor returns the exact company when the caller pinned the chain with
// a scope selector, else "" (whole-tenant reverse read).
func companyIDFor(scope *core.Scope) string {
	if scope == nil {
		return ""
	}
	return scope.CompanyID
}

// resolveRuleChain finds the (topicKey, exact Scope) chain: from the explicit
// scope, or derived from the pinned versions of the consuming links. Multiple
// distinct chains without a selector fail RULE_CHAIN_AMBIGUOUS.
func resolveRuleChain(ctx context.Context, st RuleImpactStore, organizationID, topicKey string, scope *core.Scope, links []core.RuleLinkConsuming) ([]core.AccountingMemory, error) {
	if scope != nil {
		chain, err := st.FindChain(topicKey, *scope)
		if err != nil {
			return nil, err
		}
		return chain, nil
	}
	scopes := map[string]core.Scope{}
	for _, link := range links {
		if link.LinkedVersion == "" {
			continue
		}
		rule, ok := st.FindByID(link.LinkedVersion)
		if !ok || rule.Kind != core.KindRule || rule.Identity.TopicKey != topicKey {
			continue
		}
		if rule.Scope.OrganizationID != organizationID {
			continue // cross-tenant pin — never merged
		}
		scopes[rule.Scope.CompanyID+"|"+rule.Scope.RUC+"|"+rule.Scope.Period] = rule.Scope
	}
	if len(scopes) == 0 {
		return nil, auth.New(auth.CodeMemoryNotFound, "no rule chain resolvable for "+topicKey+" (pass an exact scope or link a pinned version)")
	}
	if len(scopes) > 1 {
		return nil, auth.New(auth.CodeInvalidTransition, "RULE_CHAIN_AMBIGUOUS: topic "+topicKey+" maps to multiple exact scopes; supply a scope selector")
	}
	for _, s := range scopes {
		chain, err := st.FindChain(topicKey, s)
		if err != nil {
			return nil, err
		}
		return chain, nil
	}
	return nil, nil
}

// RuleHistory returns the full rule chain of a (topicKey, exact Scope),
// ordered by revision ascending (design §6: history).
func RuleHistory(ctx context.Context, st RuleImpactStore, topicKey string, scope core.Scope) ([]core.AccountingMemory, error) {
	chain, err := st.FindChain(topicKey, scope)
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 {
		return nil, auth.New(auth.CodeMemoryNotFound, "no rule chain found for "+topicKey)
	}
	return chain, nil
}

// RuleShow returns the CURRENT rule revision (chain head) of a
// (topicKey, exact Scope) — the rule the reconstruction would resolve to.
func RuleShow(ctx context.Context, st RuleImpactStore, topicKey string, scope core.Scope) (core.AccountingMemory, error) {
	chain, err := RuleHistory(ctx, st, topicKey, scope)
	if err != nil {
		return core.AccountingMemory{}, err
	}
	return chain[len(chain)-1], nil
}

func revisionNum(chain []core.AccountingMemory, id string) int {
	for i, m := range chain {
		if m.Identity.ID == id {
			return i + 1
		}
	}
	return 0
}

func validityWindow(v *core.Validity) (string, string) {
	if v == nil {
		return "", ""
	}
	return v.EffectiveAt, v.ExpiresAt
}

func cloneValidity(v *core.Validity) *core.Validity {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
