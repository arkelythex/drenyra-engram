// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module defines the EvidenceObject model
// (v0.7.0 local-first slice — docs/architecture/evidence-object-v0.7.md).
//
// An EvidenceObject is ONE immutable artifact (XML/PDF/CDR/extracto bytes) with
// its metadata, stored WRITE-ONCE-READ-MANY (WORM) under a content-addressed
// layout. The object's IDENTITY is deterministic: the lowercase SHA-256 hex of
// the object bytes (ComputeObjectID). Identical bytes → the same object id →
// a duplicate store is a NO-OP (created=false), never a second record.
//
// The engine treats object bytes as DATA, never instructions: no content is
// ever executed, parsed as a rule, or interpreted by the engine — the object
// layer only hashes, stores, reads and re-hashes. Object storage never
// authorizes anything (non-authorization boundary): storing an object is a
// provenance-recorded act, not an approval.
//
// This module is PURE: no I/O, no store, no keyring. It owns the closed model,
// the validators (fail closed on malformed metadata), the canonical byte
// contract (fixed property order, compact UTF-8 JSON, NO HTML escaping —
// byte-identical with the TypeScript mirror in core/evidence-object.ts) and
// the deterministic object id. WORM storage, the closed-period gate, receipt
// emission and scope-first reads live in internal/store (object_store.go).
//
// DELIBERATELY OUT OF SCOPE for this slice (documented deferrals, never
// implemented here): legal hold, retention expiry, export, purge/deletion,
// cloud/remote storage, OIDC/SUNAT/ERP integration. Object bytes are stored
// locally, have no overwrite/delete API and no retention clock.
package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// EvidenceObject is one stored immutable artifact and its metadata. ObjectID is
// the deterministic content address (lowercase SHA-256 hex of the bytes);
// SHA256 carries the same digest explicitly (an audit never has to re-derive
// it from the id). Size is the byte length — a JSON integer, never a float.
// Scope is the EXACT tenant/company/RUC/period tuple of the write; reads are
// scope-first (a caller whose scope differs never sees the object). RelPath is
// the content-addressed relative path under the objects root.
type EvidenceObject struct {
	ObjectID    string `json:"objectId"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType,omitempty"`
	// Scope — exact company scope of the write (flat, mirroring the DB row).
	TenantID  string `json:"tenantId"`
	CompanyID string `json:"companyId"`
	RUC       string `json:"ruc"`
	Period    string `json:"period,omitempty"`
	// Source — the provenance of the capture (who recorded the artifact, from
	// which system, with which external reference). The captured actor is the
	// claimed principal of the object_stored receipt.
	SourceSystem    string    `json:"sourceSystem"`
	SourceReference string    `json:"sourceReference,omitempty"`
	SourceActorID   string    `json:"sourceActorId,omitempty"`
	SourceActorKind ActorKind `json:"sourceActorKind"`
	// StoredBy is the actor that performed the storage act (equals the source
	// actor on every surface of this slice; kept explicit so the immutable
	// evidence_objects row is the provenance anchor for object_stored).
	StoredBy string `json:"storedBy"`
	// StoredAt is the automatic, immutable capture timestamp (RFC3339 UTC).
	StoredAt string `json:"storedAt"`
	// RelPath is the content-addressed relative path under the objects root
	// ("objects/<sha256[0:2]>/<sha256[2:4]>/<sha256>").
	RelPath string `json:"relPath"`
}

// ObjectStoreInput is the input for storing ONE object. Bytes are the artifact
// bytes (the identity input); Scope and Source must be complete (the store
// fails closed on malformed scope/source). ContentType is an optional MIME
// hint stored verbatim.
type ObjectStoreInput struct {
	Bytes       []byte `json:"bytes"`
	ContentType string `json:"contentType,omitempty"`
	Scope       Scope  `json:"scope"`
	Source      Source `json:"source"`
}

// ObjectStoreResult is the outcome of a store attempt: the stored object plus
// whether this call CREATED it. created=false means the exact same bytes were
// already stored (content-addressed duplicate NO-OP — no new row, no receipt).
type ObjectStoreResult struct {
	Object  EvidenceObject `json:"object"`
	Created bool           `json:"created"`
}

// objectRelPathDepth is the two-level content-addressed sharding depth: the
// layout is objects/<ab>/<cd>/<sha256> (256² buckets per level, deterministic,
// independent of any caller-controlled input).
const objectRelPathDepth = 2

// objectIDPattern freezes the object id syntax: exactly 64 lowercase hex
// digits (the SHA-256 digest shape).
var objectIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ErrObjectBytesPurgedExpected is the documented EXPECTED-ABSENCE sentinel of the
// object verifier/doctor surfaces: an object whose WORM bytes are missing is NOT
// corruption when a receipt-covered purge authorization (a valid
// evidence_purge_executions intent row bound to the object identity, or a
// completed execution) explains the absence. The store returns an error wrapping
// this sentinel (errors.Is works through %w); core.VerifyObjectBytesIntegrity
// maps it to a PASSED layer with the purged-specific message, and the doctor
// surface skips it as documented expected absence instead of failing closed.
// Missing bytes WITHOUT such an authorized intent remain the typed
// OBJECT_BYTES_MISSING corruption incident.
var ErrObjectBytesPurgedExpected = errors.New("OBJECT_PURGED_EXPECTED_ABSENCE: a receipt-covered purge authorization explains the missing bytes (expected absence, not corruption)")

// ComputeObjectID returns the deterministic object identity: the lowercase
// SHA-256 hex digest of the object bytes. This is BOTH the id and the content
// address — two byte sets that differ in any bit get different ids; identical
// bytes always collide on purpose.
func ComputeObjectID(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}

// ObjectRelPath derives the deterministic content-addressed relative layout of
// an object: objects/<sha[0:2]>/<sha[2:4]>/<sha>. Callers MUST pass the digest
// of the bytes being written — the layout is derived from the identity, never
// from caller-supplied names (a path traversal cannot be expressed).
func ObjectRelPath(objectID string) string {
	if len(objectID) < objectRelPathDepth*2 {
		return "objects/" + objectID
	}
	return "objects/" + objectID[0:2] + "/" + objectID[2:4] + "/" + objectID
}

// AssertValidEvidenceObject fails closed on malformed stored metadata. The
// identity is structural: sha256 must EQUAL objectId (a mismatch means the row
// was corrupted), both must be 64 lowercase hex digits, size must be >= 0 and
// the scope tuple must be valid (company scope, RUC 11 digits, period YYYYMM
// when present). Object bytes themselves are never validated — any bytes are
// storable; they are data, never instructions.
func AssertValidEvidenceObject(o EvidenceObject) error {
	if !objectIDPattern.MatchString(o.ObjectID) {
		return fmt.Errorf("INVALID_OBJECT_ID: expected 64 lowercase hex digits, got %q", o.ObjectID)
	}
	if o.SHA256 != o.ObjectID {
		return fmt.Errorf("INVALID_OBJECT_SHA256: sha256 %q differs from the content-addressed objectId %q", o.SHA256, o.ObjectID)
	}
	if o.Size < 0 {
		return fmt.Errorf("INVALID_OBJECT_SIZE: size must be >= 0 bytes, got %d", o.Size)
	}
	if err := AssertValidObjectScope(Scope{
		Kind:           ScopeKindCompany,
		OrganizationID: o.TenantID,
		CompanyID:      o.CompanyID,
		RUC:            o.RUC,
		Period:         o.Period,
	}); err != nil {
		return err
	}
	if err := AssertValidSource(Source{
		System:    o.SourceSystem,
		Reference: o.SourceReference,
		ActorID:   o.SourceActorID,
		ActorKind: o.SourceActorKind,
	}); err != nil {
		return err
	}
	if strings.TrimSpace(o.StoredBy) == "" {
		return fmt.Errorf("INVALID_OBJECT_STORED_BY: storedBy must be a non-empty string")
	}
	if o.StoredAt != "" {
		if _, ok := ParseDateTime(o.StoredAt); !ok {
			return fmt.Errorf("INVALID_OBJECT_STORED_AT: storedAt must be a parseable date string, got %q", o.StoredAt)
		}
	}
	if o.RelPath != ObjectRelPath(o.ObjectID) {
		return fmt.Errorf("INVALID_OBJECT_REL_PATH: relPath %q does not match the content-addressed layout %q", o.RelPath, ObjectRelPath(o.ObjectID))
	}
	return nil
}

// AssertValidObjectScope fails closed on the object scope. Objects are tenant
// artifacts in this slice: an INSTITUTIONAL scope is rejected (an object must
// belong to exactly one tenant/company/RUC/period tuple — institutional
// objects are a documented future decision, never a silent fallback). Period
// is optional and validated as YYYYMM when present.
func AssertValidObjectScope(s Scope) error {
	if s.Kind != ScopeKindCompany {
		return fmt.Errorf("INVALID_OBJECT_SCOPE: evidence objects require an exact company scope (institutional objects are out of scope for v0.7), got kind %q", s.Kind)
	}
	return AssertValidScope(s)
}

// canonicalEvidenceObject is the canonical JSON shape of an EvidenceObject:
// the struct field order IS the property order (Go marshals in declaration
// order) and property names use the wire names (json tags), so the canonical
// bytes are the same as the wire representation of the metadata.
type canonicalEvidenceObject EvidenceObject

// CanonicalEvidenceObjectJSON returns the canonical compact UTF-8 JSON bytes of
// an EvidenceObject: FIXED property order (exactly the struct order above),
// JSON string escaping, NO HTML escaping (matching the receipt canonicalizers —
// Go escapes <,>,& by default, disabling it keeps Go and TypeScript bytes
// identical). These are the bytes the Go↔TS parity fixture pins and the bytes
// an audit can re-canonicalize from any transport. Marshaling cannot fail
// (fixed value shapes) — a failure is an internal invariant violation and
// fails closed via panic.
func CanonicalEvidenceObjectJSON(o EvidenceObject) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(canonicalEvidenceObject(o)); err != nil {
		panic("evidence object: canonical marshal failed: " + err.Error())
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}
