// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.7.0 EvidenceObject
// pure model (internal/core/evidence_object.go): the deterministic content
// address (ComputeObjectID), the deterministic content-addressed relative layout
// (ObjectRelPath), the fail-closed validator matrix and the canonical metadata
// bytes pinned byte-identically with the TypeScript mirror
// (core/__tests__/evidence-object.test.ts — Go↔TS parity).
//
// Object bytes are DATA, never instructions: these tests never parse or execute
// artifact content, and no object operation authorizes anything.
package core_test

import (
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// sampleEvidenceObject returns a deterministic EvidenceObject exercising every
// field. The object id is the SHA-256 hex of the sample artifact bytes
// ("test" — a well-known digest, shared with the TypeScript mirror).
func sampleEvidenceObject() core.EvidenceObject {
	objectID := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	return core.EvidenceObject{
		ObjectID:        objectID,
		SHA256:          objectID,
		Size:            4,
		ContentType:     "application/xml",
		TenantID:        "org-001",
		CompanyID:       "acme",
		RUC:             "20100039201",
		Period:          "202401",
		SourceSystem:    "go-test",
		SourceReference: "F001-1",
		SourceActorID:   "test-agent",
		SourceActorKind: core.ActorKindAgent,
		StoredBy:        "test-agent",
		StoredAt:        "2026-01-15T12:00:00.000Z",
		RelPath:         core.ObjectRelPath(objectID),
	}
}

// pinnedCanonicalEvidenceObjectJSON is the FROZEN canonical bytes of
// sampleEvidenceObject — the exact same literal is pinned in
// core/__tests__/evidence-object.test.ts (Go↔TS canonical bytes must match
// byte-identically: fixed property order, compact UTF-8, no HTML escaping).
const pinnedCanonicalEvidenceObjectJSON = `{"objectId":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","size":4,"contentType":"application/xml","tenantId":"org-001","companyId":"acme","ruc":"20100039201","period":"202401","sourceSystem":"go-test","sourceReference":"F001-1","sourceActorId":"test-agent","sourceActorKind":"agent","storedBy":"test-agent","storedAt":"2026-01-15T12:00:00.000Z","relPath":"objects/9f/86/9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"}`

// TestComputeObjectIDDeterministic verifies the identity contract: the id is the
// lowercase SHA-256 hex of the bytes, deterministic across calls, and any bit
// difference yields a different id while identical bytes collide on purpose.
func TestComputeObjectIDDeterministic(t *testing.T) {
	if got := core.ComputeObjectID([]byte("test")); got != "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" {
		t.Fatalf("ComputeObjectID(test) = %s, want the pinned SHA-256 digest", got)
	}
	if a, b := core.ComputeObjectID([]byte("test")), core.ComputeObjectID([]byte("test")); a != b {
		t.Fatalf("identical bytes must collide: %s != %s", a, b)
	}
	if a, b := core.ComputeObjectID([]byte("test")), core.ComputeObjectID([]byte("Test")); a == b {
		t.Fatalf("bytes differing in one bit must NOT collide, both = %s", a)
	}
	for _, c := range core.ComputeObjectID([]byte("test")) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			t.Fatalf("object id must be lowercase hex, got %q", c)
		}
	}
}

// TestObjectRelPathContentAddressed verifies the deterministic two-level
// sharded layout: objects/<sha[0:2]>/<sha[2:4]>/<sha> — derived from the
// identity, never from caller-controlled names.
func TestObjectRelPathContentAddressed(t *testing.T) {
	id := core.ComputeObjectID([]byte("test"))
	if got, want := core.ObjectRelPath(id), "objects/9f/86/"+id; got != want {
		t.Fatalf("ObjectRelPath = %q, want %q", got, want)
	}
	if got := core.ObjectRelPath("ab"); got != "objects/ab" {
		t.Fatalf("short id must stay flat, got %q", got)
	}
}

// TestAssertValidEvidenceObjectMatrix is the fail-closed validation matrix:
// every malformed field is rejected with its typed code; the sample object is
// valid.
func TestAssertValidEvidenceObjectMatrix(t *testing.T) {
	sample := sampleEvidenceObject()
	if err := core.AssertValidEvidenceObject(sample); err != nil {
		t.Fatalf("sample object must be valid: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(o *core.EvidenceObject)
		want   string
	}{
		{"bad object id", func(o *core.EvidenceObject) { o.ObjectID = "not-hex" }, "INVALID_OBJECT_ID"},
		{"sha256 differs", func(o *core.EvidenceObject) { o.SHA256 = strings.Repeat("0", 64) }, "INVALID_OBJECT_SHA256"},
		{"negative size", func(o *core.EvidenceObject) { o.Size = -1 }, "INVALID_OBJECT_SIZE"},
		{"invalid ruc", func(o *core.EvidenceObject) { o.RUC = "123" }, "INVALID_RUC"},
		{"bad period", func(o *core.EvidenceObject) { o.Period = "2024" }, "INVALID_PERIOD"},
		{"missing storedBy", func(o *core.EvidenceObject) { o.StoredBy = "  " }, "INVALID_OBJECT_STORED_BY"},
		{"bad storedAt", func(o *core.EvidenceObject) { o.StoredAt = "not-a-date" }, "INVALID_OBJECT_STORED_AT"},
		{"relPath mismatch", func(o *core.EvidenceObject) { o.RelPath = "objects/evil" }, "INVALID_OBJECT_REL_PATH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := sample
			tc.mutate(&o)
			err := core.AssertValidEvidenceObject(o)
			if err == nil {
				t.Fatalf("want %s, got no error", tc.want)
			}
			if !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("error = %q, want prefix %q", err.Error(), tc.want)
			}
		})
	}
}

// TestAssertValidObjectScopeRejectsInstitutional pins the scope discipline:
// objects are tenant artifacts — an institutional scope is a documented deferral,
// never a silent fallback.
func TestAssertValidObjectScopeRejectsInstitutional(t *testing.T) {
	institutional := core.Scope{Kind: core.ScopeKindInstitutional, OrganizationID: "org-001"}
	if err := core.AssertValidObjectScope(institutional); err == nil {
		t.Fatal("institutional object scope must be rejected (documented deferral, never a silent fallback)")
	}
	company := core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: "org-001",
		CompanyID:      "acme",
		RUC:            "20100039201",
		Period:         "202401",
	}
	if err := core.AssertValidObjectScope(company); err != nil {
		t.Fatalf("exact company scope must be valid: %v", err)
	}
}

// TestCanonicalEvidenceObjectJSONPinned pins the FROZEN canonical bytes — the
// Go↔TS parity contract: fixed property order (the struct order), compact UTF-8
// JSON, NO HTML escaping. The same literal is asserted in the TypeScript mirror.
func TestCanonicalEvidenceObjectJSONPinned(t *testing.T) {
	got := string(core.CanonicalEvidenceObjectJSON(sampleEvidenceObject()))
	if got != pinnedCanonicalEvidenceObjectJSON {
		t.Fatalf("canonical bytes diverge from the pinned Go↔TS fixture:\n got  %s\n want %s", got, pinnedCanonicalEvidenceObjectJSON)
	}
	// No HTML escaping: a content type with <>& must serialize verbatim.
	o := sampleEvidenceObject()
	o.ContentType = "application/xml; q=<&>"
	if got := string(core.CanonicalEvidenceObjectJSON(o)); !strings.Contains(got, `"contentType":"application/xml; q=<&>"`) {
		t.Fatalf("canonical JSON must NOT HTML-escape <,>,& (parity with TS), got %s", got)
	}
}
