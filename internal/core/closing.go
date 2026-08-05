// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the monthly-close snapshot
// model (v0.5.0 — docs/architecture/close-intelligence-v0.5.md §2.1).
//
// A monthly close is an immutable memory (kind=summary, fiscalEffect=closing,
// topic closing/CIERRE-<YYYYMM>, exact company/period scope). The CloseSnapshot
// is its OPTIONAL structured payload: period, generation timestamp, the summary
// hash over the canonical snapshot bytes, counts, explicit monetary totals
// (signed int64 cents with source memory IDs — the engine never derives money
// from prose), the frozen pending-item digest, reconciliation counts, and the
// narrative memory IDs.
//
// observations.close_snapshot_json (schema v6) stores the canonical snapshot
// JSON bytes verbatim; the snapshot participates in the content and envelope
// hashes (a memory WITH a snapshot hashes differently from the same memory
// without one; a memory without a snapshot contributes the empty string, so
// every pre-v6 envelope stays byte-identical — frozen v0.3 hash contract).
package core

import (
	"bytes"
	"encoding/json"
)

// CloseSnapshot is the frozen structured payload of a monthly close memory.
// amountCents is a SIGNED int64 (cents, never float); totals are explicit
// inputs because current memories expose no general machine-readable amount.
// Every JSON property is present in this exact order — the canonical bytes are
// the authoritative snapshot, mirrored byte-identically by the TypeScript
// mirror and stored verbatim in observations.close_snapshot_json.
type CloseSnapshot struct {
	// Period is the fiscal period YYYYMM the close covers (the memory scope's
	// period).
	Period string `json:"period"`
	// GeneratedAt is when the snapshot was generated (UTC ISO-8601).
	GeneratedAt string `json:"generatedAt"`
	// SummaryHash is the SHA-256 hex digest of the canonical snapshot JSON —
	// the self-hash that makes the frozen snapshot independently verifiable.
	SummaryHash string `json:"summaryHash"`
	// Counts aggregates the period's memory counts at close time.
	Counts CloseCounts `json:"counts"`
	// Totals are the explicit monetary totals (code/currency/signed cents) and
	// the same-scope source memory IDs backing each total.
	Totals []CloseTotal `json:"totals"`
	// PendingItems is the frozen digest of pending items at close time (the
	// close discloses close state; pending items do not block close approval).
	PendingItems []ClosePendingItem `json:"pendingItems"`
	// Reconciliation counts the period's reconciliation proposals/decisions.
	Reconciliation CloseReconciliation `json:"reconciliation"`
	// NarrativeMemoryIDs lists the memory IDs that make up the period's
	// institutional narrative (fact/decision/exception story).
	NarrativeMemoryIDs []string `json:"narrativeMemoryIds"`
}

// CloseCounts is the period memory-count digest of a CloseSnapshot.
type CloseCounts struct {
	// Total is the total number of memories in the period.
	Total int `json:"total"`
	// ByKind counts memories per memory kind.
	ByKind map[string]int `json:"byKind"`
	// ByStatus counts memories per memory status.
	ByStatus map[string]int `json:"byStatus"`
}

// CloseTotal is one explicit monetary total of a CloseSnapshot. AmountCents is
// SIGNED int64 cents (negative totals are legal — e.g. credit-side amounts);
// it is never a float.
type CloseTotal struct {
	// Code identifies the total (e.g. "igv", "ventas", "compras").
	Code string `json:"code"`
	// Currency is the ISO 4217 currency code (e.g. "PEN", "USD").
	Currency string `json:"currency"`
	// AmountCents is the signed total in cents.
	AmountCents int64 `json:"amountCents"`
	// SourceMemoryIDs are the same-scope source memory IDs backing the total.
	SourceMemoryIDs []string `json:"sourceMemoryIds"`
}

// ClosePendingItem is one frozen pending item of a CloseSnapshot.
type ClosePendingItem struct {
	MemoryID    string `json:"memoryId"`
	TopicKey    string `json:"topicKey"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Title       string `json:"title"`
	EffectiveAt string `json:"effectiveAt"`
}

// CloseReconciliation counts the period's reconciliation lifecycle states.
type CloseReconciliation struct {
	Proposed  int `json:"proposed"`
	Confirmed int `json:"confirmed"`
	Rejected  int `json:"rejected"`
}

// CloseTopicPrefix is the fixed topic-key prefix of a monthly close memory:
// closing/CIERRE-<YYYYMM> (design §2.1 — the topic is canonical, never
// caller-chosen).
const CloseTopicPrefix = "closing/CIERRE-"

// IsCloseMemory reports whether m is a VALID monthly close memory: kind=summary,
// fiscalEffect=closing, exact company scope with a period, and the canonical
// close topic key for that period. Only such a memory projects a period closure
// when approved (approval-time guard; creation-time rejection of another
// current close lives in the CreateClose service, next batch).
func IsCloseMemory(m AccountingMemory) bool {
	if m.Kind != KindSummary || m.FiscalEffect != FiscalEffectClosing {
		return false
	}
	if m.Scope.Kind != ScopeKindCompany || m.Scope.Period == "" {
		return false
	}
	return m.Identity.TopicKey == CloseTopicPrefix+m.Scope.Period
}

// CanonicalCloseSnapshotJSON returns the canonical compact UTF-8 JSON bytes of a
// CloseSnapshot: the struct field order above, JSON string escaping, NO HTML
// escaping (matching the receipt canonicalizers — Go escapes <,>,& by default,
// disabling it keeps Go and TypeScript bytes identical). These are the exact
// bytes persisted in observations.close_snapshot_json (schema v6), the bytes
// SummaryHash digests, and the bytes that participate in the content/envelope
// hashes. Marshaling cannot fail (fixed value shapes) — a failure is an
// internal invariant violation and fails closed via panic.
func CanonicalCloseSnapshotJSON(s *CloseSnapshot) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		panic("closing: canonical snapshot marshal failed: " + err.Error())
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// CloseSnapshotSummaryHash is the lowercase SHA-256 hex of the canonical
// snapshot JSON — the self-hash a CloseSnapshot carries in its SummaryHash
// field.
func CloseSnapshotSummaryHash(s *CloseSnapshot) string {
	// sha256HexBytes is the receipt module's canonical lowercase SHA-256 hex
	// helper (same package) — one digest implementation for the protocol.
	return sha256HexBytes(CanonicalCloseSnapshotJSON(s))
}

// closeSnapshotCanonicalContribution is the canonical hash contribution of an
// optional CloseSnapshot: the canonical JSON bytes when present, the empty
// string when absent. The empty contribution keeps every memory WITHOUT a
// snapshot byte-identical to its pre-v6 hash (frozen v0.3 hash contract: a NEW
// optional field on NEW memories never silently re-hashes existing rows).
func closeSnapshotCanonicalContribution(s *CloseSnapshot) string {
	if s == nil {
		return ""
	}
	// The element is self-describing ("close_snapshot\x00<json>") so a snapshot
	// can never collide with a prior field's bytes.
	return "close_snapshot\x00" + string(CanonicalCloseSnapshotJSON(s))
}
