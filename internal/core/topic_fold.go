// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the canonical topic-key fold
// (sdd-060-tenant-cli, FR-TEN-3): a PURE normalization that maps a raw topic
// key to its folded form for drift comparison, plus the pure drift-group
// shapes. It joins the Go↔TS golden parity mechanism (config golden_parity).
// No monetary field exists anywhere in this file.
package core

import "strings"

// FoldTopicKey normalizes a raw topic key for drift comparison:
//
//  1. Unicode lower-case (strings.ToLower);
//  2. every rune that is not a letter or digit becomes a SINGLE SPACE
//     separator (punctuation, slashes, hyphens, dots, marks — all fold to the
//     same separator, preserving word boundaries);
//  3. whitespace runs (real or punctuation-derived) are collapsed to one space
//     and the result is trimmed.
//
// Examples: "rule/IGV credit" and "rule/igv-credit" both fold to
// "rule igv credit" — formatting drift collides, while word boundaries
// survive. Accent folding is EXPLICITLY out of scope (the fold is conservative
// so genuinely distinct keys never collide). The result is deterministic and
// pure. Two chains drift when their folded keys are equal but their raw keys
// differ under the same exact scope tuple.
func FoldTopicKey(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	pendingSpace := false
	for _, r := range strings.ToLower(raw) {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			pendingSpace = true
		case isFoldRune(r):
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			pendingSpace = false
			b.WriteRune(r)
		default:
			// punctuation, slashes, hyphens, dots, marks — a separator space.
			pendingSpace = true
		}
	}
	return b.String()
}

// isFoldRune reports whether the rune survives the fold: letters and digits
// (Unicode-aware; anything else — punctuation, symbols, marks — is dropped).
func isFoldRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		(r >= 0x00C0 && r <= 0x024F) || // Latin-1 supplement + extended (incl. accented letters, kept as-is)
		(r >= 0x0370 && r <= 0x03FF) // Greek
}

// DriftedChain is one non-canonical chain in a drift group.
type DriftedChain struct {
	TopicKey  string `json:"topicKey"`
	ChainSize int    `json:"chainSize"`
}

// DriftGroup is one collision group: chains whose folded topic keys are equal
// but raw keys differ under one tenant tuple and one fiscal period (Period is
// the exact period of the group; "" never occurs — empty period scans the whole
// tenant and groups per period). Canonical is the deterministic candidate (most
// observations, ties broken lexicographically).
type DriftGroup struct {
	Period             string         `json:"period"`
	Canonical          string         `json:"canonical"`
	CanonicalChainSize int            `json:"canonicalChainSize"`
	Drifted            []DriftedChain `json:"drifted"`
}

// ConsolidateReport is the `tenant consolidate` document (dry-run and apply).
type ConsolidateReport struct {
	RUC                string       `json:"ruc"`
	Period             string       `json:"period"`
	DryRun             bool         `json:"dryRun"`
	DriftGroups        []DriftGroup `json:"driftGroups"`
	TotalDriftGroups   int          `json:"totalDriftGroups"`
	TotalDriftedChains int          `json:"totalDriftedChains"`
}
