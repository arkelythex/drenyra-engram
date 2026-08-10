// Rule impact reconstruction — Phase 6 v0.6.0, design §5.
//
// RuleImpact answers "which decisions did this rule revision affect, and do
// their accounting-time intervals overlap the changed revision's vigencia
// window?" — the regulatory-change impact read. PURE and READ-ONLY: it never
// mutates status, links, receipts, or envelopes; it never asserts fiscal or
// accounting correctness (design §1 correctness boundary).
package core

import "strings"

// RuleImpactItem is one consuming memory linked to the rule chain, with the
// overlap classification against the SELECTED changed revision's window.
type RuleImpactItem struct {
	// MemoryID is the consuming memory (the decision that referenced the rule).
	MemoryID string `json:"memoryId"`
	// TopicKey is the consuming memory's identity topic.
	TopicKey string `json:"topicKey"`
	// Kind is the consuming memory kind.
	Kind MemoryKind `json:"kind"`
	// Status is the consuming memory's CURRENT lifecycle status.
	Status MemoryStatus `json:"status"`
	// DecisionTime is the consuming memory's accounting time (EffectiveAt).
	DecisionTime string `json:"decisionTime"`
	// LinkedVersion is the pinned rule revision (empty for legacy links).
	LinkedVersion string `json:"linkedVersion,omitempty"`
	// ResolvedVersion is the rule revision actually matched at read time
	// (equals LinkedVersion when the pin resolves).
	ResolvedVersion string `json:"resolvedVersion,omitempty"`
	// Jurisdiction is the consuming memory's own policy jurisdiction when it is
	// itself a rule, else empty (the referenced rule's jurisdiction rides the
	// chain identity, not the consuming item).
	Jurisdiction string `json:"jurisdiction,omitempty"`
	// OverlapsChangedWindow reports whether the consuming interval intersects
	// the selected changed revision's vigencia window [effectiveAt, expiresAt).
	OverlapsChangedWindow bool `json:"overlapsChangedWindow"`
	// Outcome classifies the resolution: "resolved", "legacy unversioned ref",
	// or a typed failure code (the item still appears, never silently skipped).
	Outcome string `json:"outcome"`
	// Detail is the human-readable resolution note.
	Detail string `json:"detail,omitempty"`
}

// RuleImpactResult is the deterministic impact of one rule chain revision.
type RuleImpactResult struct {
	// Ref is the rule chain topic key.
	Ref string `json:"ref"`
	// SelectedRevision is the rule revision the impact was computed against
	// (the caller's --revision, else the chain head).
	SelectedRevision string `json:"selectedRevision"`
	// Revision is the numeric chain revision of the selected revision.
	Revision int `json:"revision"`
	// Jurisdiction is the selected revision's policy jurisdiction.
	Jurisdiction string `json:"jurisdiction,omitempty"`
	// Validity is the selected revision's vigencia window.
	Validity *Validity `json:"validity,omitempty"`
	// Items are the consuming memories, ordered by DecisionTime, MemoryID, Ref.
	Items []RuleImpactItem `json:"items"`
	// UnresolvedLegacy counts links without version metadata (legacy refs).
	UnresolvedLegacy int `json:"unresolvedLegacy"`
}

// RuleImpactOutcomes — the closed outcome vocabulary of an impact item.
const (
	RuleImpactResolved       = "resolved"
	RuleImpactLegacyUnpinned = "legacy unversioned ref"
	RuleImpactFailed         = "failed"
)

// intervalsOverlap reports whether the consuming interval [dStart, dEnd) (a
// POINT when dEnd is empty — the decision instant) intersects the changed
// window [rStart, rEnd) — half-open, an empty bound is unbounded.
//
// Bounds are RFC3339 strings; lexical comparison is valid for this canonical
// shape (UTC, fixed format). A malformed/empty consuming start fails closed as
// no-overlap — never a guess.
func intervalsOverlap(dStart, dEnd, rStart, rEnd string) bool {
	if dStart == "" {
		return false
	}
	if dEnd == "" {
		// Point at the decision instant: member of [rStart, rEnd) when in
		// force (rStart <= t < rEnd) — a decision exactly at the window start
		// IS in force; an empty bound is unbounded.
		if rStart != "" && dStart < rStart {
			return false
		}
		if rEnd != "" && dStart >= rEnd {
			return false
		}
		return true
	}
	// Window [dStart, dEnd): overlaps [rStart, rEnd) iff dEnd > rStart &&
	// dStart < rEnd (half-open at both ends).
	if rStart != "" && dEnd <= rStart {
		return false // consuming window ends before/at the window start
	}
	if rEnd != "" && dStart >= rEnd {
		return false // consuming window starts at/after the window end
	}
	return true
}

// ClassifyRuleImpactItem is the PURE classification of one consuming memory
// against one changed rule revision's window. The consuming interval is passed
// EXPLICITLY (dStart, dEnd — the memory's own Validity window, or a point when
// dStart == dEnd == DecisionTime); the service resolves it from the memory
// before calling. Deterministic, no store access, Go↔TS mirrored.
func ClassifyRuleImpactItem(item RuleImpactItem, dStart, dEnd, changedStart, changedEnd string) RuleImpactItem {
	item.OverlapsChangedWindow = intervalsOverlap(dStart, dEnd, changedStart, changedEnd)
	return item
}

// RuleLinkConsuming is ONE reverse-read row (design §5): a stored rule link
// joined to its consuming observation. Legacy rows carry empty LinkedVersion.
type RuleLinkConsuming struct {
	MemoryID               string       `json:"memoryId"`
	Ref                    string       `json:"ref"`
	LinkedVersion          string       `json:"linkedVersion,omitempty"`
	LinkEffectiveAt        string       `json:"linkEffectiveAt,omitempty"`
	ConsumingTopicKey      string       `json:"consumingTopicKey"`
	ConsumingKind          MemoryKind   `json:"consumingKind"`
	ConsumingStatus        MemoryStatus `json:"consumingStatus"`
	DecisionTime           string       `json:"decisionTime"`
	ConsumingValidityStart string       `json:"consumingValidityStart,omitempty"`
	ConsumingValidityEnd   string       `json:"consumingValidityEnd,omitempty"`
}

// Interval returns the consuming memory's accounting interval: its own
// Validity window when declared, else a POINT at DecisionTime.
func (c RuleLinkConsuming) Interval() (string, string) {
	if c.ConsumingValidityStart != "" {
		return c.ConsumingValidityStart, c.ConsumingValidityEnd
	}
	return c.DecisionTime, c.DecisionTime
}

// NormalizeRuleImpactItem canonicalizes an impact item's outcome string: an
// empty outcome becomes "resolved"; a legacy pin becomes the legacy outcome.
func NormalizeRuleImpactItem(item RuleImpactItem) RuleImpactItem {
	if strings.TrimSpace(item.Outcome) == "" {
		item.Outcome = RuleImpactResolved
	}
	if item.LinkedVersion == "" && item.Outcome == RuleImpactResolved {
		item.Outcome = RuleImpactLegacyUnpinned
	}
	return item
}

// ──────────────────────────────────────────────
// Rule version resolution — design §4 (Batch 4)
// ──────────────────────────────────────────────

// RuleVersionTrace is the pure outcome of resolving ONE structured rule link
// against the rule chain (design §4.1). The verification report carries one per
// structured link; legacy unversioned refs contribute a skipped trace.
type RuleVersionTrace struct {
	Ref                 string    `json:"ref"`
	LinkedVersion       string    `json:"linkedVersion"`
	ResolvedVersion     string    `json:"resolvedVersion,omitempty"`
	Revision            int       `json:"revision,omitempty"`
	DecisionEffectiveAt string    `json:"decisionEffectiveAt"`
	Validity            *Validity `json:"validity,omitempty"`
	Jurisdiction        string    `json:"jurisdiction,omitempty"`
	StatusAsOf          string    `json:"statusAsOf,omitempty"`
	Outcome             string    `json:"outcome"`
	Detail              string    `json:"detail,omitempty"`
}

// RuleVersionOutcomes — the closed outcome vocabulary of a rule-version trace.
const (
	RuleVersionResolved      = "passed"
	RuleVersionLegacySkipped = "skipped"
	RuleVersionNotInForce    = "RULE_NOT_IN_FORCE"
	RuleVersionOverlap       = "RULE_VIGENCIA_OVERLAP"
	RuleVersionMismatch      = "RULE_VERSION_MISMATCH"
	RuleVersionStatusInvalid = "RULE_STATUS_INVALID"
	RuleVersionTargetInvalid = "RULE_VERSION_TARGET_INVALID"
)

// ruleInForceAt selects the chain revisions whose vigencia window contains the
// decision instant, PURE (no store access): half-open [effectiveAt, expiresAt),
// an empty bound is unbounded, and a revision WITHOUT Validity is in force only
// at/after its own EffectiveAt (a point window).
func ruleInForceAt(chain []AccountingMemory, instant string) []AccountingMemory {
	var inForce []AccountingMemory
	for _, m := range chain {
		v := m.Validity
		if v == nil || v.EffectiveAt == "" {
			// No declared window: in force at/after the rule's own effectiveAt.
			if m.EffectiveAt != "" && instant >= m.EffectiveAt {
				inForce = append(inForce, m)
			}
			continue
		}
		if instant < v.EffectiveAt {
			continue
		}
		if v.ExpiresAt != "" && instant >= v.ExpiresAt {
			continue
		}
		inForce = append(inForce, m)
	}
	return inForce
}

// ResolveRuleVersionFromChain is the PURE version resolution (design §4.1 steps
// 2-6): given the FULL chain, the pinned version id and the decision instant,
// select the revision(s) in force, fail on zero (RULE_NOT_IN_FORCE) or multiple
// (RULE_VIGENCIA_OVERLAP), and require the sole match to equal the pin
// (RULE_VERSION_MISMATCH). The caller supplies the chain (exact scope, tenant-
// visible) and the transitions for status-as-of reconstruction.
func ResolveRuleVersionFromChain(chain []AccountingMemory, pinnedID, decisionTime string) (AccountingMemory, error) {
	if strings.TrimSpace(pinnedID) == "" || strings.TrimSpace(decisionTime) == "" {
		return AccountingMemory{}, NewRuleVersionError(RuleVersionTargetInvalid, "pinned version and decision time are required")
	}
	inForce := ruleInForceAt(chain, decisionTime)
	if len(inForce) == 0 {
		return AccountingMemory{}, NewRuleVersionError(RuleVersionNotInForce, "no rule revision in force at "+decisionTime)
	}
	if len(inForce) > 1 {
		return AccountingMemory{}, NewRuleVersionError(RuleVersionOverlap, "multiple rule revisions in force at "+decisionTime+" — overlapping vigencia")
	}
	if inForce[0].Identity.ID != pinnedID {
		return AccountingMemory{}, NewRuleVersionError(RuleVersionMismatch, "the revision in force at "+decisionTime+" is not the pinned version")
	}
	return inForce[0], nil
}

// RuleVersionError is the typed failure of rule-version resolution: the stable
// outcome code is the error's message prefix (transport-independent, mirrors
// the design's fail-closed codes).
type RuleVersionError struct {
	Code    string
	Message string
}

func (e *RuleVersionError) Error() string {
	return e.Code + ": " + e.Message
}

// NewRuleVersionError builds a typed rule-version failure.
func NewRuleVersionError(code, message string) error {
	return &RuleVersionError{Code: code, Message: message}
}

// RuleVersionCode extracts the stable code from a rule-version error ("" when
// the error is not a rule-version failure).
func RuleVersionCode(err error) string {
	if rv, ok := err.(*RuleVersionError); ok {
		return rv.Code
	}
	return ""
}

// StatusAsOf reconstructs a subject's lifecycle status AT an instant from its
// ordered transitions: walk every transition with timestamp <= instant and take
// the last one's To; none → the initial status. PURE (design §4.1 step 7: a
// rule already rejected/voided/superseded at the decision instant is invalid).
func StatusAsOf(initial MemoryStatus, transitions []StatusTransitionRecord, instant string) MemoryStatus {
	status := initial
	for _, t := range transitions {
		if t.Timestamp != "" && t.Timestamp <= instant {
			status = t.To
		}
	}
	return status
}
