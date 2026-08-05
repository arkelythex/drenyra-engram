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
	"fmt"
	"time"
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

// ──────────────────────────────────────────────
// CreateClose command (design §2.1)
// ──────────────────────────────────────────────

// CreateCloseInput is the canonical CreateClose application-service input
// (docs/architecture/close-intelligence-v0.5.md §2.1): the YYYYMM period (the
// exact company scope is a separate argument and MUST carry the same period),
// the caller-supplied monetary totals (each requires code, currency and at
// least one same-scope source memory — the engine never derives money from
// prose), an optional close rationale and the provenance Source of the
// creation (agent|system — creation is a normal save; the APPROVAL is the
// authenticated controller act and never happens here).
type CreateCloseInput struct {
	// Period is the YYYYMM fiscal period the close covers; it must equal the
	// scope's period (they are one tuple).
	Period string
	// Totals are the explicit monetary totals frozen into the CloseSnapshot
	// (signed int64 cents; never float).
	Totals []CloseTotal
	// Reason is an optional close rationale recorded in the Why content.
	Reason string
	// Source is the provenance of the creation claim (agent|system; a human
	// source is legal provenance too — it never authorizes).
	Source Source
}

// MonthEndUTC returns the LAST DAY of the YYYYMM period at 23:59:59 UTC — the
// canonical effectiveAt of a monthly close (design §2.1: "effectiveAt at month
// end UTC"). A malformed period fails with INVALID_PERIOD.
func MonthEndUTC(period string) (string, error) {
	if !IsValidPeriod(period) {
		return "", fmt.Errorf("INVALID_PERIOD: expected YYYYMM (six digits, month 01-12), got %q", period)
	}
	t, err := time.Parse("200601", period)
	if err != nil {
		return "", fmt.Errorf("INVALID_PERIOD: %v", err)
	}
	// The last day of the month is the day BEFORE the first day of the next
	// month; the close is stamped at the final second of the month in UTC.
	lastDay := t.AddDate(0, 1, 0).AddDate(0, 0, -1)
	end := time.Date(lastDay.Year(), lastDay.Month(), lastDay.Day(), 23, 59, 59, 0, time.UTC)
	return end.Format(time.RFC3339), nil
}

// ──────────────────────────────────────────────
// ReopenPeriod command and result (design §2.3)
// ──────────────────────────────────────────────

// ReopenPeriodCommand is the explicit controller reopen command: the exact
// company scope, the close memory the caller EXPECTS to be current (a stale
// reference fails the guard), the human reason and the idempotency request id
// scoped to (tenant, requestId). It deliberately carries NO principal fields
// (ADR-003 pattern — authority arrives as the verified principal argument).
type ReopenPeriodCommand struct {
	// Scope is the exact company scope (with period) being reopened.
	Scope Scope
	// ExpectedCloseMemoryID must equal the current period_closures
	// close_memory_id; a mismatch is INVALID_TRANSITION (the caller reviewed a
	// stale close).
	ExpectedCloseMemoryID string
	// Reason is the human-readable reopen justification (REQUIRED).
	Reason string
	// RequestID is the idempotency key scoped to (tenant, requestId); a replay
	// with the same id and intent returns the stored result.
	RequestID string
}

// ReopenPeriodResult is the outcome of an atomic reopen. Status is always
// "reopened" for a fresh reopen; a replay returns the reconstructed result
// with IdempotentReplay=true.
type ReopenPeriodResult struct {
	TenantID           string `json:"tenantId"`
	CompanyID          string `json:"companyId"`
	FiscalPeriodID     string `json:"fiscalPeriodId"`
	CloseMemoryID      string `json:"closeMemoryId"`
	EventID            string `json:"eventId"`
	Status             string `json:"status"`
	ReopenedAt         string `json:"reopenedAt"`
	PrincipalSubjectID string `json:"principalSubjectId"`
	PolicyVersion      string `json:"policyVersion"`
	IdempotentReplay   bool   `json:"idempotentReplay"`
}

// ClosureState names the three closure states of the period_closures
// projection (design §2.2): "open" (no closure row), "closed" (an approved
// close gates the period) and "reopened" (an explicit controller reopen
// admitted corrections until the next close approval re-closes).
type ClosureState string

const (
	ClosureStateOpen     ClosureState = "open"
	ClosureStateClosed   ClosureState = "closed"
	ClosureStateReopened ClosureState = "reopened"
)
