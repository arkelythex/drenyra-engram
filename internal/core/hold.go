// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the PURE object-level legal-hold model of the
// v0.8 evidence lifecycle (batch 3 — docs/architecture/evidence-lifecycle-v0.8.md
// §3.2/§7):
//
//   - an EvidenceHold is a FIRST-CLASS hold record attached to EXACTLY ONE
//     EvidenceObject (this batch is OBJECT-LEVEL ONLY — scope-level holds are a
//     documented deferral, never a silent fallback: the schema enforces
//     object_id NOT NULL and this validator rejects any row without an object);
//   - hold kinds are the CLOSED retention-policy set {legal, audit, dispute,
//     fiscalization, other} (retention_policy.go §3.1 — one closed set, shared
//     with blocking_hold_kinds, never duplicated);
//   - a hold is a ONE-WAY record exactly like signing_keys.revoked_at: the
//     placed columns are immutable and the lift fields (lifted_at/lifted_by/
//     lift_reason) are NULL → value updates ONLY, performed by the guarded store
//     API; a lifted hold remains visible forever and never reopens;
//   - HasActiveBlockingHold is the PURE active-blocking-hold helper: a hold
//     blocks purge only while ACTIVE (not lifted) AND its kind is in the
//     deployment's blocking set (the frozen default is
//     {legal, audit, dispute, fiscalization}; 'other' never blocks by default —
//     §3.1/§7). An EMPTY blocking set blocks NOTHING (fail closed: a caller
//     that does not know its blocking policy cannot claim a block);
//   - the lift fields are CONSISTENT: all three are set together on lift and are
//     all empty while placed (a partially lifted row is invalid metadata).
//
// This module is PURE: no I/O, no store, no clock. It owns the closed model, the
// fail-closed validators and the canonical byte contract (fixed property order,
// compact UTF-8 JSON, NO HTML escaping — byte-identical with the TypeScript
// mirror in core/evidence-hold.ts). Persistence, the authenticated place/lift
// gate (the extended evidence-lifecycle policy with the place_hold/lift_hold
// actions), (tenant, requestId) idempotency, the receipt emission on the
// evidence_object chain and the scope-first blocking query live in
// internal/store (hold_store.go).
package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/auth"
)

// HoldKind is the closed hold-kind set (§3.2). The TOKENS are the frozen
// retention-policy hold-kind tokens (retention_policy.go §3.1) — one closed set
// across the lifecycle, never duplicated.
type HoldKind string

const (
	// HoldKindLegal is a legal hold (blocks purge by default).
	HoldKindLegal HoldKind = "legal"
	// HoldKindAudit is an audit hold (blocks purge by default).
	HoldKindAudit HoldKind = "audit"
	// HoldKindDispute is a dispute hold (blocks purge by default).
	HoldKindDispute HoldKind = "dispute"
	// HoldKindFiscalization is a fiscalization hold (blocks purge by default).
	HoldKindFiscalization HoldKind = "fiscalization"
	// HoldKindOther is a non-blocking-by-default hold kind.
	HoldKindOther HoldKind = "other"
)

// IsValidHoldKind reports whether k is a closed hold-kind token. The closed set
// is the retention-policy hold-kind set (IsValidRetentionHoldKind) — one shared
// enum, never a second copy.
func IsValidHoldKind(k HoldKind) bool {
	return IsValidRetentionHoldKind(string(k))
}

// EvidenceHold is ONE immutable object-level hold record (design §3.2). The
// scope tuple is flattened (tenantId/companyId/ruc/period) exactly like
// EvidenceObject so the wire shape mirrors the DB row and the TypeScript mirror
// byte-for-byte. HoldID is the store-minted UUID; ObjectID is the
// content-addressed evidence_objects id (64 lowercase hex) the hold protects.
// Kind is the closed hold-kind token; Reason and OwnerSubjectID are the
// placement evidence (who is responsible for the hold and why). PlacedAt/By are
// the automatic capture provenance; the lift fields are the ONE-WAY closure
// (all three empty while placed, all three set together on lift).
type EvidenceHold struct {
	HoldID    string `json:"holdId"`
	ObjectID  string `json:"objectId"`
	TenantID  string `json:"tenantId"`
	CompanyID string `json:"companyId"`
	RUC       string `json:"ruc"`
	Period    string `json:"period,omitempty"`
	// Kind is the closed hold-kind token (§3.2).
	Kind HoldKind `json:"kind"`
	// Reason is the placement justification (REQUIRED — REASON_REQUIRED at the
	// store when absent).
	Reason string `json:"reason"`
	// OwnerSubjectID is the subject responsible for the hold (business owner —
	// caller-supplied, never authorization).
	OwnerSubjectID string `json:"ownerSubjectId"`
	// PlacedAt/PlacedBy are the automatic, immutable capture provenance (the
	// acting principal's subject id).
	PlacedAt string `json:"placedAt"`
	PlacedBy string `json:"placedBy"`
	// LiftedAt/LiftedBy/LiftReason are the one-way closure fields: all empty
	// while placed, all set together on lift, never cleared or rewritten.
	LiftedAt   string `json:"liftedAt,omitempty"`
	LiftedBy   string `json:"liftedBy,omitempty"`
	LiftReason string `json:"liftReason,omitempty"`
}

// PlaceHoldCommand is the store command for ONE hold placement (batch 3): an
// authenticated principal places an object-level hold on an existing
// EvidenceObject. ObjectID is the content-addressed object id; Kind is the
// closed hold-kind token; Reason and OwnerSubjectID are the placement evidence;
// RequestID is the (tenant, requestId) idempotency key. The acting principal is
// a separate pre-verified argument (ADR-003 — the command can never declare
// identity).
type PlaceHoldCommand struct {
	ObjectID       string   `json:"objectId"`
	Kind           HoldKind `json:"kind"`
	Reason         string   `json:"reason"`
	OwnerSubjectID string   `json:"ownerSubjectId"`
	RequestID      string   `json:"requestId"`
}

// LiftHoldCommand is the store command for ONE hold lift (batch 3): an
// authenticated principal closes a placed hold one-way. HoldID is the
// store-minted hold id; Reason is the REQUIRED lift justification; RequestID is
// the (tenant, requestId) idempotency key. The acting principal is a separate
// pre-verified argument.
type LiftHoldCommand struct {
	HoldID    string `json:"holdId"`
	Reason    string `json:"reason"`
	RequestID string `json:"requestId"`
}

// PlaceHoldResult is the outcome of a hold placement. A successful placement
// ALWAYS created the immutable row (Created=true); IdempotentReplay=true means
// the (tenant, requestId) was already completed with the SAME command and
// principal, so the stored outcome is returned with NO new row and NO new
// receipt.
type PlaceHoldResult struct {
	Hold             EvidenceHold `json:"hold"`
	Created          bool         `json:"created"`
	IdempotentReplay bool         `json:"idempotentReplay"`
}

// LiftHoldResult is the outcome of a hold lift. A successful lift closed the
// placed row one-way (Lifted=true); IdempotentReplay=true means the
// (tenant, requestId) was already completed with the SAME command and principal,
// so the stored (already lifted) outcome is returned with NO new row and NO new
// receipt.
type LiftHoldResult struct {
	Hold             EvidenceHold `json:"hold"`
	Lifted           bool         `json:"lifted"`
	IdempotentReplay bool         `json:"idempotentReplay"`
}

// AssertValidPlaceHoldCommand fails closed on malformed placement input: the
// object id must be the 64-lowercase-hex content address, the kind must be a
// closed hold-kind token, reason and owner are REQUIRED non-empty strings and
// the (tenant, requestId) idempotency key is REQUIRED.
func AssertValidPlaceHoldCommand(cmd PlaceHoldCommand) error {
	if !objectIDPattern.MatchString(cmd.ObjectID) {
		return fmt.Errorf("INVALID_HOLD_OBJECT_ID: objectId must be 64 lowercase hex digits (the content-addressed object id), got %q", cmd.ObjectID)
	}
	if !IsValidHoldKind(cmd.Kind) {
		return fmt.Errorf("INVALID_HOLD_KIND: kind must be one of legal|audit|dispute|fiscalization|other, got %q", cmd.Kind)
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return fmt.Errorf("REASON_REQUIRED: a hold placement requires a non-empty reason")
	}
	if strings.TrimSpace(cmd.OwnerSubjectID) == "" {
		return fmt.Errorf("INVALID_HOLD_OWNER: ownerSubjectId must be a non-empty string (the subject responsible for the hold)")
	}
	if strings.TrimSpace(cmd.RequestID) == "" {
		return auth.New(auth.CodeIdempotencyConflict, "requestId (tenant-scoped idempotency key) is required")
	}
	return nil
}

// AssertValidLiftHoldCommand fails closed on malformed lift input: the hold id
// must be non-empty, the lift reason is REQUIRED and the (tenant, requestId)
// idempotency key is REQUIRED.
func AssertValidLiftHoldCommand(cmd LiftHoldCommand) error {
	if strings.TrimSpace(cmd.HoldID) == "" {
		return fmt.Errorf("INVALID_HOLD_ID: holdId must be a non-empty string")
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return fmt.Errorf("REASON_REQUIRED: a hold lift requires a non-empty reason")
	}
	if strings.TrimSpace(cmd.RequestID) == "" {
		return auth.New(auth.CodeIdempotencyConflict, "requestId (tenant-scoped idempotency key) is required")
	}
	return nil
}

// AssertValidEvidenceHold fails closed on malformed stored hold metadata: the
// hold id must be a UUID, the object id the 64-lowercase-hex content address,
// the scope an EXACT company scope (objects are tenant artifacts — institutional
// scopes are rejected exactly like AssertValidObjectScope), the kind a closed
// hold-kind token, reason/owner/placedBy non-empty, placedAt parseable when
// present, and the lift fields CONSISTENT (all three empty or all three set —
// a partially lifted row is invalid metadata).
func AssertValidEvidenceHold(h EvidenceHold) error {
	if h.HoldID != "" && !policyIDPattern(h.HoldID) {
		return fmt.Errorf("INVALID_HOLD_ID: holdId must be a UUID, got %q", h.HoldID)
	}
	if !objectIDPattern.MatchString(h.ObjectID) {
		return fmt.Errorf("INVALID_HOLD_OBJECT_ID: objectId must be 64 lowercase hex digits, got %q", h.ObjectID)
	}
	if err := AssertValidObjectScope(Scope{
		Kind:           ScopeKindCompany,
		OrganizationID: h.TenantID,
		CompanyID:      h.CompanyID,
		RUC:            h.RUC,
		Period:         h.Period,
	}); err != nil {
		return err
	}
	if !IsValidHoldKind(h.Kind) {
		return fmt.Errorf("INVALID_HOLD_KIND: kind must be one of legal|audit|dispute|fiscalization|other, got %q", h.Kind)
	}
	if strings.TrimSpace(h.Reason) == "" {
		return fmt.Errorf("REASON_REQUIRED: a hold record requires a non-empty reason")
	}
	if strings.TrimSpace(h.OwnerSubjectID) == "" {
		return fmt.Errorf("INVALID_HOLD_OWNER: ownerSubjectId must be a non-empty string")
	}
	if strings.TrimSpace(h.PlacedBy) == "" {
		return fmt.Errorf("INVALID_HOLD_PLACED_BY: placedBy must be a non-empty string")
	}
	if h.PlacedAt != "" {
		if _, ok := ParseDateTime(h.PlacedAt); !ok {
			return fmt.Errorf("INVALID_HOLD_PLACED_AT: placedAt must be a parseable date string, got %q", h.PlacedAt)
		}
	}
	// The lift fields are ONE-WAY and CONSISTENT: all three empty while placed,
	// all three set together on lift (a partially lifted row is invalid).
	hasLiftedAt := h.LiftedAt != ""
	hasLiftedBy := h.LiftedBy != ""
	hasLiftReason := h.LiftReason != ""
	if hasLiftedAt != hasLiftedBy || hasLiftedAt != hasLiftReason {
		return fmt.Errorf("INVALID_HOLD_LIFT: liftedAt/liftedBy/liftReason must be all empty (placed) or all set (lifted)")
	}
	if hasLiftedAt {
		if _, ok := ParseDateTime(h.LiftedAt); !ok {
			return fmt.Errorf("INVALID_HOLD_LIFTED_AT: liftedAt must be a parseable date string, got %q", h.LiftedAt)
		}
		if strings.TrimSpace(h.LiftReason) == "" {
			return fmt.Errorf("REASON_REQUIRED: a lifted hold requires a non-empty lift reason")
		}
	}
	return nil
}

// HasActiveBlockingHold is the PURE active-blocking-hold helper (design §7): a
// hold BLOCKS purge while it is ACTIVE (not lifted) AND its kind is in the
// deployment's blocking set. An EMPTY blocking set blocks NOTHING (fail closed —
// a caller that does not know its blocking policy cannot claim a block); the
// frozen policy default blocking set is {legal, audit, dispute, fiscalization}
// (retention_policy.go DefaultBlockingHoldKinds — §3.1/§7), so an 'other' hold
// never blocks unless a deployment explicitly added 'other' to
// blocking_hold_kinds. A lifted hold never blocks.
func HasActiveBlockingHold(h EvidenceHold, blockingKinds []string) bool {
	if h.LiftedAt != "" {
		return false
	}
	for _, k := range blockingKinds {
		if k == string(h.Kind) {
			return true
		}
	}
	return false
}

// canonicalEvidenceHold is the canonical JSON shape of an EvidenceHold: the
// struct field order IS the property order (Go marshals in declaration order)
// and property names use the wire names (json tags), so the canonical bytes are
// the same as the wire representation of the metadata.
type canonicalEvidenceHold EvidenceHold

// CanonicalEvidenceHoldJSON returns the canonical compact UTF-8 JSON bytes of an
// EvidenceHold: FIXED property order (exactly the struct order above), JSON
// string escaping, NO HTML escaping (matching the receipt canonicalizers — Go
// escapes <,>,& by default, disabling it keeps Go and TypeScript bytes
// identical). These are the bytes the Go↔TS parity fixture pins and the bytes an
// audit can re-canonicalize from any transport. Marshaling cannot fail (fixed
// value shapes) — a failure is an internal invariant violation and fails closed
// via panic.
func CanonicalEvidenceHoldJSON(h EvidenceHold) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(canonicalEvidenceHold(h)); err != nil {
		panic("evidence hold: canonical marshal failed: " + err.Error())
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}
