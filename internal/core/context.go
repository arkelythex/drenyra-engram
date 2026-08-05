// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the automatic MCP session
// context model (v0.5.0 — docs/architecture/close-intelligence-v0.5.md §5).
//
// CurrentContext is the bounded session context an MCP server carries on
// initialize: the EXACT configured scope (never inferred from database
// recency), the compact explainable period summary with the period_closures
// projection state and the latest close memory id, the shared pending-item
// digest, the at most 20 most recent chains (latest revision per chain,
// effectiveAt desc) and the generation timestamp. The compact period summary
// is the wire subset of the server's full PeriodSummaryOutput (total, counts
// by kind/status, closureState, latestClose) the MCP session actually needs.
package core

// RecentChain is one recent-chain entry of a CurrentContext (design §5): the
// latest revision's topic key, memory id, kind, status, effectiveAt and title.
type RecentChain struct {
	TopicKey    string `json:"topicKey"`
	MemoryID    string `json:"memoryId"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	EffectiveAt string `json:"effectiveAt"`
	Title       string `json:"title"`
}

// CurrentContextPeriodSummary is the compact explainable period summary inside
// a CurrentContext (design §5): the period's memory counts by kind and status,
// the period_closures projection state (open|closed|reopened) and the latest
// close memory ID (empty when the period has no close). It is the wire subset
// of the server.PeriodSummaryOutput the session context carries; the full
// summary (pending approvals, narrative, …) stays on the dedicated summary
// surface.
type CurrentContextPeriodSummary struct {
	Total        int            `json:"total"`
	ByKind       map[string]int `json:"byKind"`
	ByStatus     map[string]int `json:"byStatus"`
	ClosureState string         `json:"closureState"`
	LatestClose  string         `json:"latestClose"`
}

// CurrentContext is the automatic MCP session context (design §5): the exact
// configured scope, the compact period summary with closure state, the shared
// pending-item digest (the same frozen list a close snapshot embeds), the at
// most 20 most recent chains and the generation timestamp.
type CurrentContext struct {
	Scope         Scope                       `json:"scope"`
	PeriodSummary CurrentContextPeriodSummary `json:"periodSummary"`
	PendingItems  []ClosePendingItem          `json:"pendingItems"`
	RecentChains  []RecentChain               `json:"recentChains"`
	GeneratedAt   string                      `json:"generatedAt"`
}
