// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module defines the observation core
// model, whose content is structured text (What/Why/Where/Learned) — there are
// no monetary fields in the memory model and no money value is computed here.
//
// Core memory model — the observation unit of institutional accounting memory.
// Implements contracts/memory.md, contracts/scope.md, contracts/lifecycle.md and
// contracts/provenance.md (frozen-for-0.1 semantics, carried unchanged into the
// standalone Go engine per ADR-001). It mirrors core/types.ts semantically.
//
// Contract-to-code mapping (same as the TypeScript reference):
//   - AuthorityStatus is the contract's lifecycle state
//     (draft → reviewed → promoted → superseded).
//   - Validity is the contract's vigencia (effective/expiry window).
//   - Identity.TopicKey is the contract's topic_key (the upsert target).
//   - Scope equality is exact: two observations differing only in scope are
//     different observations (scope.md rule 5), and period participates in that
//     equality. Single YYYYMM periods only in this slice; ranges are future.

package core

import (
	"fmt"
	"strings"
	"time"
)

// ──────────────────────────────────────────────
// Scope — structural tenant isolation
// ──────────────────────────────────────────────

// ScopeKind discriminates a company-scoped scope from an institutional one.
type ScopeKind string

const (
	// ScopeKindCompany scopes an observation to an organization/company/RUC/period;
	// invisible to queries from any other company (structural isolation, not a
	// post-filter).
	ScopeKindCompany ScopeKind = "company"
	// ScopeKindInstitutional declares explicitly cross-company knowledge; it is
	// only surfaced when the query scope is institutional or the caller
	// explicitly asks for it (scope.md rule 3).
	ScopeKindInstitutional ScopeKind = "institutional"
)

// Scope is the fiscal scope of an observation (contracts/scope.md).
//
// For kind=company: Period is the single fiscal period `YYYYMM` when present
// (empty when absent); ranges are future. For kind=institutional all the
// company fields are empty.
type Scope struct {
	Kind           ScopeKind `json:"kind"`
	OrganizationID string    `json:"organizationId,omitempty"`
	CompanyID      string    `json:"companyId,omitempty"`
	// RUC is the Peruvian RUC — exactly 11 digits in this slice (checksum:
	// future).
	RUC    string `json:"ruc,omitempty"`
	Period string `json:"period,omitempty"`
}

// ──────────────────────────────────────────────
// Identity
// ──────────────────────────────────────────────

// Identity is the canonical identity: a stable observation id plus the upsert
// topic key.
type Identity struct {
	ID       string `json:"id"`
	TopicKey string `json:"topicKey"`
}

// ──────────────────────────────────────────────
// Content / provenance / validity
// ──────────────────────────────────────────────

// Content is the structured content — the canonical What/Why/Where/Learned
// shape (contracts/memory.md rule 1).
type Content struct {
	What    string `json:"what"`
	Why     string `json:"why"`
	Where   string `json:"where"`
	Learned string `json:"learned"`
}

// Provenance is audit metadata captured at creation; not editable afterward
// (contracts/provenance.md).
type Provenance struct {
	Actor     string `json:"actor"`
	Timestamp string `json:"timestamp"` // UTC ISO-8601 creation time
	Source    string `json:"source"`
	Session   string `json:"session,omitempty"`
}

// Validity is the vigencia window: effective/expiry. Expired observations
// surface as stale at read time, never as current fact.
type Validity struct {
	EffectiveAt string `json:"effectiveAt,omitempty"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
}

// ──────────────────────────────────────────────
// Lifecycle / relations / writes
// ──────────────────────────────────────────────

// AuthorityStatus is the observation lifecycle state
// (contracts/lifecycle.md). Unknown states fail closed at read time: a state
// the engine does not recognize is treated as not-promoted.
type AuthorityStatus string

const (
	StatusDraft      AuthorityStatus = "draft"
	StatusReviewed   AuthorityStatus = "reviewed"
	StatusPromoted   AuthorityStatus = "promoted"
	StatusSuperseded AuthorityStatus = "superseded"
)

// Relation is the relation vocabulary between observations
// (contracts/memory.md, lifecycle.md).
type Relation string

const (
	RelationRelated       Relation = "related"
	RelationCompatible    Relation = "compatible"
	RelationScoped        Relation = "scoped"
	RelationConflictsWith Relation = "conflicts_with"
	RelationSupersedes    Relation = "supersedes"
	RelationNotConflict   Relation = "not_conflict"
)

// WriteOutcome is the save (upsert) outcome. Conflict and Unknown are the
// documented fallback outcomes (contracts/memory.md frozen semantics).
type WriteOutcome string

const (
	WriteCreated  WriteOutcome = "created"
	WriteUpdated  WriteOutcome = "updated"
	WriteConflict WriteOutcome = "conflict" // reserved for a future OCC slice
	WriteUnknown  WriteOutcome = "unknown"
)

// ──────────────────────────────────────────────
// Observation / results
// ──────────────────────────────────────────────

// Observation is a stored observation. Content/scope/provenance are immutable
// once created; AuthorityStatus is the single field the lifecycle machine may
// transition.
type Observation struct {
	Identity        Identity        `json:"identity"`
	Title           string          `json:"title"`
	Type            string          `json:"type"`
	Scope           Scope           `json:"scope"`
	Content         Content         `json:"content"`
	AuthorityStatus AuthorityStatus `json:"authorityStatus"`
	Validity        *Validity       `json:"validity,omitempty"`
	Provenance      Provenance      `json:"provenance"`
	// Revision is the 1-based revision within the (topicKey, scope) chain; a
	// JSON integer, never a float.
	Revision int `json:"revision"`
}

// WriteResult is the result of a save (upsert) operation.
type WriteResult struct {
	Observation Observation  `json:"observation"`
	Outcome     WriteOutcome `json:"outcome"`
}

// SaveInput is the input for saving/upserting under a topic key + exact scope.
type SaveInput struct {
	TopicKey        string          `json:"topicKey"`
	Title           string          `json:"title"`
	Type            string          `json:"type"`
	Scope           Scope           `json:"scope"`
	Content         Content         `json:"content"`
	AuthorityStatus AuthorityStatus `json:"authorityStatus,omitempty"`
	Validity        *Validity       `json:"validity,omitempty"`
	Provenance      Provenance      `json:"provenance"`
}

// RelationMeta carries the optional actor/timestamp of a relation record.
type RelationMeta struct {
	Actor     string
	Timestamp string
}

// RelationRecord is a recorded relation between two observations.
type RelationRecord struct {
	FromID    string `json:"fromId"`
	ToID      string `json:"toId"`
	Relation  Relation `json:"relation"`
	Actor     string  `json:"actor,omitempty"`
	Timestamp string  `json:"timestamp,omitempty"`
}

// StatusTransitionRecord is one entry of the lifecycle audit trail
// (contracts/provenance.md rule 3: every state traces to actor+time).
type StatusTransitionRecord struct {
	ObservationID string          `json:"observationId"`
	From          AuthorityStatus `json:"from"`
	To            AuthorityStatus `json:"to"`
	Actor         string          `json:"actor"`
	Timestamp     string          `json:"timestamp"`
}

// ──────────────────────────────────────────────
// Scope helpers
// ──────────────────────────────────────────────

// ScopeEquals is exact scope equality. Period participates: "" === "" but a
// perioded scope never equals an unperioded one (scope is part of identity).
func ScopeEquals(a, b Scope) bool {
	if a.Kind == ScopeKindInstitutional && b.Kind == ScopeKindInstitutional {
		return true
	}
	if a.Kind != ScopeKindCompany || b.Kind != ScopeKindCompany {
		return false
	}
	return a.OrganizationID == b.OrganizationID &&
		a.CompanyID == b.CompanyID &&
		a.RUC == b.RUC &&
		a.Period == b.Period
}

// ScopeKey is the canonical serialization of a scope, used to key upsert
// chains (mirrors core/types.ts scopeKey).
func ScopeKey(s Scope) string {
	if s.Kind == ScopeKindInstitutional {
		return "institutional"
	}
	return "company\x00" + s.OrganizationID + "\x00" + s.CompanyID + "\x00" + s.RUC + "\x00" + s.Period
}

// CloneObservation is a defensive copy — stored observations are never handed
// out by reference.
func CloneObservation(o Observation) Observation {
	cloned := o
	if o.Validity != nil {
		v := *o.Validity
		cloned.Validity = &v
	}
	return cloned
}

// ──────────────────────────────────────────────
// Validation (scope.md: RUC 11 digits, period YYYYMM)
// ──────────────────────────────────────────────

const rucDigits = 11

// IsValidRUC reports whether ruc is exactly 11 digits (checksum validation:
// future slice).
func IsValidRUC(ruc string) bool {
	if len(ruc) != rucDigits {
		return false
	}
	for i := 0; i < len(ruc); i++ {
		if ruc[i] < '0' || ruc[i] > '9' {
			return false
		}
	}
	return true
}

// IsValidPeriod reports whether period is `YYYYMM` (six digits, month 01-12)
// in this slice.
func IsValidPeriod(period string) bool {
	if len(period) != 6 {
		return false
	}
	for i := 0; i < len(period); i++ {
		if period[i] < '0' || period[i] > '9' {
			return false
		}
	}
	month := (period[4]-'0')*10 + (period[5] - '0')
	return month >= 1 && month <= 12
}

// AssertValidScope fails closed on malformed company scopes (throws in TS;
// returns an error here). An institutional scope is always valid.
func AssertValidScope(s Scope) error {
	if s.Kind == ScopeKindInstitutional {
		return nil
	}
	if s.Kind != ScopeKindCompany {
		return fmt.Errorf("INVALID_SCOPE: unknown scope kind %q", s.Kind)
	}
	if s.OrganizationID == "" {
		return fmt.Errorf("INVALID_SCOPE: organizationId must be a non-empty string")
	}
	if s.CompanyID == "" {
		return fmt.Errorf("INVALID_SCOPE: companyId must be a non-empty string")
	}
	if !IsValidRUC(s.RUC) {
		return fmt.Errorf("INVALID_RUC: expected exactly 11 digits, got %q", s.RUC)
	}
	if s.Period != "" && !IsValidPeriod(s.Period) {
		return fmt.Errorf("INVALID_PERIOD: expected YYYYMM (six digits, month 01-12), got %q", s.Period)
	}
	return nil
}

// AssertValidContent validates the structured content — all four fields must
// be non-empty strings.
func AssertValidContent(c Content) error {
	for field, value := range map[string]string{
		"what": c.What, "why": c.Why, "where": c.Where, "learned": c.Learned,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("INVALID_CONTENT: field %q must be a non-empty string", field)
		}
	}
	return nil
}

// AssertValidProvenance validates that provenance is captured at creation and
// traceable: actor and source non-empty, timestamp parseable.
func AssertValidProvenance(p Provenance) error {
	if p.Actor == "" {
		return fmt.Errorf("INVALID_PROVENANCE: actor must be a non-empty string")
	}
	if _, ok := ParseDateTime(p.Timestamp); !ok {
		return fmt.Errorf("INVALID_PROVENANCE: timestamp must be a parseable date string")
	}
	if p.Source == "" {
		return fmt.Errorf("INVALID_PROVENANCE: source must be a non-empty string")
	}
	return nil
}

// AssertValidValidity fails closed on malformed vigencia windows at write time;
// a nil window is valid.
func AssertValidValidity(v *Validity) error {
	if v == nil {
		return nil
	}
	if v.EffectiveAt != "" {
		if _, ok := ParseDateTime(v.EffectiveAt); !ok {
			return fmt.Errorf("INVALID_VALIDITY: effectiveAt must be a parseable date string")
		}
	}
	if v.ExpiresAt != "" {
		if _, ok := ParseDateTime(v.ExpiresAt); !ok {
			return fmt.Errorf("INVALID_VALIDITY: expiresAt must be a parseable date string")
		}
	}
	return nil
}

// timeLayouts are the accepted timestamp shapes. The reference (TS Date.parse)
// accepts ISO-8601; RFC3339 (with optional fractional seconds) covers the
// canonical form, with two lenient fallbacks.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// ParseDateTime parses a date string the way the reference Date.parse does; ok
// is false for unparseable input (fail closed, never fabricate a time).
func ParseDateTime(s string) (time.Time, bool) {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
