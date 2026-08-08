// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the PURE deterministic evidence-lifecycle export
// model of the v0.8 evidence lifecycle (batch 4 —
// docs/architecture/evidence-lifecycle-v0.8.md §12; WU-3 — the core/store/API
// boundary only; the HTTP/CLI/MCP surfaces are deferred to the transport work):
//
//   - the export is a READ-ONLY, tenant/RUC-scoped audit bundle. An explicit
//     RUC-scoped request (EvidenceExportCriteria) selects the exact company
//     scope (tenant/company/RUC, optional YYYYMM period — an empty period
//     selects ALL periods of the RUC), and NO data ever crosses the
//     tenant/company/RUC/period boundary:
//     ValidateEvidenceExportScopeCoverage fails closed on ANY out-of-scope row;
//   - the bundle is DETERMINISTIC: canonical row ordering everywhere (sorted
//     arrays; receipts preserved in exact per-subject chain order — the store
//     emits them in (issuedAt, insertion) order, the same order the
//     verification engine walks), canonical JSON with FIXED property order and
//     NO HTML escaping (Go↔TS byte-identical);
//   - the manifest is SELF-HASHING with TWO structurally separate digests:
//     contentManifestHash is the CONTENT hash — the lowercase SHA-256 hex of the
//     canonical CONTENT of the complete bundle (every array/row in canonical
//     order, receipts included) — so identical canonical content yields the
//     identical content hash and any row-content change re-hashes, regardless of
//     counts; bundleHash is the ENVELOPE hash — the lowercase SHA-256 hex of the
//     canonical manifest-core bytes (version, scope, criteria, generatedAt,
//     counts, contentManifestHash). generatedAt participates ONLY in the envelope
//     hash (it is the derived "as of" timestamp of the data — never a wall
//     clock), never in the content hash: equivalent content can never differ on a
//     timestamp. exportId is the content-addressed identity
//     "evidence-export/v0.8.0:<contentManifestHash>". Identical data yields the
//     identical bundle and export identity, so replay/idempotency is structural
//     — and the store writes NOTHING and emits NO receipt (the export is a
//     read-only query, never a material export act; a receipt would add a
//     wall-clock issuedAt and a row write, contradicting the deterministic
//     read-only contract — see ExportEvidenceLifecycle);
//   - generatedAt is DETERMINISTIC: the maximum timestamp across every included
//     row (the "as of" point of the snapshot), derived from the data itself, so
//     two exports of identical data are byte-identical;
//   - purged objects export immutable metadata/hash/lifecycle/receipt evidence
//     ONLY — never object bytes; the object entry carries bytes: "purged" with
//     the purge_executed completion receipt hash. Non-purged objects are still
//     an audit manifest, never a byte export: bytes: "stored" means "expected
//     present at the content address" — the export never reads or verifies
//     bytes, it only records the lifecycle truth (a completed execution removed
//     them).
//
// This module is PURE: no I/O, no store, no clock. It owns the closed model,
// the criteria validator, the canonical ordering, the self-hash contract, the
// deterministic generatedAt derivation and the fail-closed scope-coverage
// validation. The read-only SQL gathering, the bytes-state derivation and the
// manifest construction live in internal/store (evidence_export_store.go).
package core

import (
	"fmt"
	"sort"
	"strings"
)

// EvidenceExportModelVersion is the frozen version of the deterministic export
// bundle (design §12): stamped on the manifest and used as the content-addressed
// exportId prefix.
const EvidenceExportModelVersion = "evidence-export/v0.8.0"

// ──────────────────────────────────────────────
// Criteria — the explicit RUC-scoped request
// ──────────────────────────────────────────────

// EvidenceExportCriteria is the explicit RUC-scoped export request. Scope must
// be an EXACT company scope (tenant/company/RUC); Period is optional — when
// empty the bundle spans ALL fiscal periods of the exact RUC, when present only
// that exact YYYYMM period. No other selection exists in this slice (the
// criteria envelope is the audit record of WHAT was requested; the manifest
// carries it verbatim).
type EvidenceExportCriteria struct {
	Scope Scope `json:"scope"`
}

// AssertValidEvidenceExportCriteria fails closed on a malformed export request:
// institutional scopes are rejected (the export is tenant/RUC-scoped — objects
// require an exact company scope in the v0.7/v0.8 slice), the tenant
// (organizationId) and companyId must be non-empty, the RUC exactly 11 digits,
// and the period (when present) a valid YYYYMM.
func AssertValidEvidenceExportCriteria(c EvidenceExportCriteria) error {
	if c.Scope.Kind != ScopeKindCompany {
		return &ExportScopeError{Code: "INVALID_EXPORT_SCOPE",
			Message: "evidence lifecycle export requires an exact company scope (tenant/company/RUC), got kind " + string(c.Scope.Kind)}
	}
	if strings.TrimSpace(c.Scope.OrganizationID) == "" {
		return &ExportScopeError{Code: "INVALID_EXPORT_SCOPE", Message: "organizationId (tenant) must be a non-empty string"}
	}
	if strings.TrimSpace(c.Scope.CompanyID) == "" {
		return &ExportScopeError{Code: "INVALID_EXPORT_SCOPE", Message: "companyId must be a non-empty string"}
	}
	if !IsValidRUC(c.Scope.RUC) {
		return &ExportScopeError{Code: "INVALID_EXPORT_SCOPE", Message: "expected exactly 11 RUC digits, got " + c.Scope.RUC}
	}
	if c.Scope.Period != "" && !IsValidPeriod(c.Scope.Period) {
		return &ExportScopeError{Code: "INVALID_EXPORT_SCOPE", Message: "expected YYYYMM (six digits, month 01-12), got " + c.Scope.Period}
	}
	return nil
}

// ExportScopeError is the typed export failure: a frozen code plus a human
// message. It covers malformed criteria (INVALID_EXPORT_SCOPE) and any
// fail-closed scope-coverage violation (EXPORT_SCOPE_VIOLATION — a row that
// would cross the tenant/company/RUC/period boundary).
type ExportScopeError struct {
	Code    string
	Message string
}

func (e *ExportScopeError) Error() string { return e.Code + ": " + e.Message }

// exportScopeViolation builds a typed EXPORT_SCOPE_VIOLATION error naming the
// row kind and identifier (cross-scope leakage is evidence of corruption — the
// export fails closed, never silently drops).
func exportScopeViolation(kind, id, detail string) error {
	return &ExportScopeError{Code: "EXPORT_SCOPE_VIOLATION",
		Message: kind + " " + id + " escapes the requested scope: " + detail}
}

// ──────────────────────────────────────────────
// Bundle model (design §12)
// ──────────────────────────────────────────────

// EvidenceExportCounts is the row-count summary of the bundle. Every count is a
// JSON integer (never a float). Counts participate in the canonical manifest
// core, so ANY row-count change re-hashes the manifest.
type EvidenceExportCounts struct {
	Objects           int `json:"objects"`
	LifecycleStates   int `json:"lifecycleStates"`
	RetentionPolicies int `json:"retentionPolicies"`
	Holds             int `json:"holds"`
	PurgeRequests     int `json:"purgeRequests"`
	PurgeApprovals    int `json:"purgeApprovals"`
	PurgeExecutions   int `json:"purgeExecutions"`
	LifecycleEvents   int `json:"lifecycleEvents"`
	Receipts          int `json:"receipts"`
	SigningKeys       int `json:"signingKeys"`
}

// EvidenceExportManifest is the canonical, sorted, self-hashing root of the
// bundle (design §12). Field ORDER is the canonical property order. Scope is
// the applied scope tuple (the criteria scope verbatim — tenant/company/RUC,
// with the exact period when the criteria carried one); Criteria is the verbatim
// request envelope (the audit record of WHAT was requested). GeneratedAt is the
// DETERMINISTIC "as of" timestamp: the maximum timestamp across every included
// row (empty for an empty bundle) — data-derived, never a wall clock, and it
// participates ONLY in the envelope hash, never in the content hash.
// ContentManifestHash is the CONTENT hash: the lowercase SHA-256 hex of the
// canonical content of the complete bundle (every array/row in canonical
// order, receipts included) — the hash an auditor compares to prove the exact
// rows; identical content yields the identical content hash even when the
// source order differs. BundleHash is the ENVELOPE hash: the lowercase SHA-256
// hex of the canonical manifest-core bytes (version, scope, criteria,
// generatedAt, counts, contentManifestHash). ExportID is the content-addressed
// identity "evidence-export/v0.8.0:<contentManifestHash>" — identical data
// yields the identical export id, so replay/idempotency is structural.
type EvidenceExportManifest struct {
	Version             string                 `json:"version"`
	ExportID            string                 `json:"exportId"`
	Scope               Scope                  `json:"scope"`
	Criteria            EvidenceExportCriteria `json:"criteria"`
	GeneratedAt         string                 `json:"generatedAt"`
	Counts              EvidenceExportCounts   `json:"counts"`
	ContentManifestHash string                 `json:"contentManifestHash"`
	BundleHash          string                 `json:"bundleHash"`
}

// EvidenceBytesState is the closed per-object byte-state token of the export.
// The export NEVER reads, verifies or carries object bytes — it only records
// the lifecycle truth derived from the immutable execution ledger.
type EvidenceBytesState string

const (
	// EvidenceBytesStored marks an object with NO committed purge execution and NO
	// purge intent: bytes are EXPECTED present at the content address (the export
	// never verifies them — this is an audit manifest, not a byte export).
	EvidenceBytesStored EvidenceBytesState = "stored"
	// EvidenceBytesPurged marks an object whose bytes were physically removed by
	// a committed execution: the bundle carries immutable
	// metadata/hash/lifecycle/receipt evidence ONLY.
	EvidenceBytesPurged EvidenceBytesState = "purged"
	// EvidenceBytesIntended marks an object whose receipt-covered purge intent
	// committed but whose completion never did (an interrupted/recovery-window
	// attempt): the bytes may or may not be present and are NEVER presented as
	// ordinary stored bytes — the honest state of a crash-intent (the object
	// exports its immutable history; the completion evidence is absent).
	EvidenceBytesIntended EvidenceBytesState = "intended"
)

// IsValidEvidenceBytesState reports whether s is a closed byte-state token.
func IsValidEvidenceBytesState(s EvidenceBytesState) bool {
	return s == EvidenceBytesStored || s == EvidenceBytesPurged || s == EvidenceBytesIntended
}

// EvidenceObjectExport is ONE evidence object of the bundle: the immutable
// metadata (never the bytes), the closed byte-state token and, for a purged
// object, the purge_executed completion receipt hash (the audit anchor the
// design §12 marks as `bytes: "purged"` — empty only when the deployment runs
// without a receipt signer). A stored object carries NO purge receipt hash; an
// intended object (a committed purge intent whose completion never did) also
// carries none — its immutable history and intent receipts stay in the
// bundle, but no completion is ever claimed.
type EvidenceObjectExport struct {
	Object EvidenceObject `json:"object"`
	// BytesState is the closed byte-state marker: "stored" means the bytes are
	// expected present (never verified by the export), "purged" means a
	// committed execution removed them, and "intended" means a receipt-covered
	// purge intent committed but the completion never did (the bytes are never
	// presented as ordinary stored bytes).
	BytesState EvidenceBytesState `json:"bytes"`
	// PurgeExecutedReceiptHash is the completion receipt hash of the object's
	// committed execution (evidence_purge_executions.completion_receipt_id);
	// empty for a stored object, for an intended object and for a purged object
	// of a no-signer store.
	PurgeExecutedReceiptHash string `json:"purgeExecutedReceiptHash,omitempty"`
}

// EvidenceExportReceipt is ONE receipt of the bundle's complete per-subject
// chains (design §12): the full signed envelope (signature as padded base64),
// the exact canonical payload bytes stored at emission and the stored
// receipt_hash digest — together fully offline-verifiable (payloadHash against
// the payload bytes, the Ed25519 signature over the canonical unsigned
// envelope, receipt_hash against the complete canonical bytes, and the chain
// links via previousReceiptHash). ChainOrdinal is the receipt's 0-based
// position in its subject's chain: the store propagates the (issuedAt,
// insertion) position of its emission query, and the canonical receipt sort
// uses it as the same-issued_at tie-break, so equal-timestamp chain receipts
// keep their emission order across canonicalization. The ordinal is part of
// the canonical content (never omitted) — the content hash commits to the
// chain order.
type EvidenceExportReceipt struct {
	ChainOrdinal int           `json:"chainOrdinal"`
	Receipt      SignedReceipt `json:"receipt"`
	PayloadJSON  string        `json:"payloadJson"`
	ReceiptHash  string        `json:"receiptHash"`
}

// EvidenceExportSigningKey is ONE public signing-key record referenced by the
// included receipts (PUBLIC keys only — never private material): the key id,
// the algorithm, the padded-base64 public key (the exact stored form the
// verification engine decodes to raw bytes) and the registration lifecycle. The
// bundle fails closed when an included receipt references a key with no
// exported row — an unverifiable chain is never silently exported.
type EvidenceExportSigningKey struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"publicKey"`
	CreatedAt string `json:"createdAt"`
	RevokedAt string `json:"revokedAt,omitempty"`
}

// EvidenceExportBundle is the full deterministic audit bundle (design §12):
// the self-hashing manifest plus every scoped row — object metadata (never
// bytes), the current lifecycle-state projection, the bound retention policies
// (policy resolution evidence), holds, purge requests/approvals/executions,
// lifecycle events and the complete per-subject receipt chains with their
// public keys. Array field order IS the canonical property order; array element
// order is the canonical ordering of CanonicalizeEvidenceExportBundle.
// Receipt rows sort by their per-subject chain ordinal first.
type EvidenceExportBundle struct {
	Manifest          EvidenceExportManifest     `json:"manifest"`
	Objects           []EvidenceObjectExport     `json:"objects"`
	LifecycleStates   []EvidenceRetentionState   `json:"lifecycleStates"`
	RetentionPolicies []RetentionPolicy          `json:"retentionPolicies"`
	Holds             []EvidenceHold             `json:"holds"`
	PurgeRequests     []EvidencePurgeRequest     `json:"purgeRequests"`
	PurgeApprovals    []EvidencePurgeApproval    `json:"purgeApprovals"`
	PurgeExecutions   []EvidencePurgeExecution   `json:"purgeExecutions"`
	LifecycleEvents   []EvidenceLifecycleEvent   `json:"lifecycleEvents"`
	Receipts          []EvidenceExportReceipt    `json:"receipts"`
	SigningKeys       []EvidenceExportSigningKey `json:"signingKeys"`
}

// ──────────────────────────────────────────────
// Canonical ordering (determinism contract)
// ──────────────────────────────────────────────

// validChainOrdinals reports whether ordinals are the complete per-subject
// chain sequence of one subject (every position 0..n-1 exactly once) — the
// exact shape the store propagates (its query's (issuedAt, emission)
// positions).
func validChainOrdinals(ordinals []int) bool {
	n := len(ordinals)
	seen := make([]bool, n)
	for _, o := range ordinals {
		if o < 0 || o >= n || seen[o] {
			return false
		}
		seen[o] = true
	}
	return true
}

// deriveReceiptChainOrdinals reconstructs the 0-based chain positions of ONE
// subject's receipts from their previousReceiptHash links when the links form
// exactly ONE linear chain covering every receipt (the genesis carries an
// empty or out-of-bundle previous hash; every step has exactly one successor
// and never revisits an already-placed receipt). It returns nil for any
// ambiguous shape — multiple roots, a fork (two receipts chaining on the
// same hash), a gap, a cycle or a duplicate hash — so the caller falls back
// to the deterministic ordering instead of guessing.
func deriveReceiptChainOrdinals(receipts []EvidenceExportReceipt) []int {
	n := len(receipts)
	if n == 0 {
		return nil
	}
	byHash := make(map[string]int, n)
	for i := range receipts {
		if _, dup := byHash[receipts[i].ReceiptHash]; dup {
			return nil // duplicate hash — ambiguous
		}
		byHash[receipts[i].ReceiptHash] = i
	}
	roots := make([]int, 0, 1)
	for i := range receipts {
		prev := receipts[i].Receipt.PreviousReceiptHash
		if prev == "" {
			roots = append(roots, i) // genesis
			continue
		}
		if _, ok := byHash[prev]; !ok {
			roots = append(roots, i) // predecessor resolved outside the bundle
		}
	}
	if len(roots) != 1 {
		return nil // no genesis, two genii or a cycle — ambiguous
	}
	ordinals := make([]int, n)
	for i := range ordinals {
		ordinals[i] = -1
	}
	ordinals[roots[0]] = 0
	current := receipts[roots[0]].ReceiptHash
	for pos := 1; pos < n; pos++ {
		next := -1
		for i := range receipts {
			if receipts[i].Receipt.PreviousReceiptHash != current {
				continue
			}
			if next != -1 {
				return nil // fork — two receipts chain on the same hash
			}
			next = i
		}
		if next == -1 {
			return nil // gap — the chain stops before covering the subject
		}
		if ordinals[next] != -1 {
			return nil // cycle — a receipt chains on an already-placed receipt
		}
		ordinals[next] = pos
		current = receipts[next].ReceiptHash
	}
	return ordinals
}

// establishCanonicalReceiptOrdinals fixes the chain ordinal of every receipt
// BEFORE the canonical sort (the ordinal is part of the canonical content —
// the content hash commits to the chain order). Per subject:
//
//  1. the propagated ordinals when they form the complete per-subject
//     sequence (the store's (issuedAt, emission) positions — the
//     authoritative chain order of every store bundle);
//  2. otherwise the ordinals reconstructed from the previousReceiptHash
//     links when they form one linear chain (a hand-built bundle that
//     preserves its chain links but carries no ordinals still canonicalizes
//     to chain order);
//  3. otherwise every ordinal of the subject stays zero and the
//     deterministic (subject, issuedAt, receiptHash) ordering decides — an
//     ambiguous input that no store bundle can produce (store chains are
//     linear and carry complete ordinals).
//
// The resolution is order-free (groups are processed independently; every
// write is index-based), so identical data canonicalizes identically
// regardless of input order, and a second pass sees valid ordinals and is
// idempotent.
func establishCanonicalReceiptOrdinals(receipts []EvidenceExportReceipt) {
	groups := make(map[string][]int, 8)
	for i := range receipts {
		subject := receipts[i].Receipt.SubjectID
		groups[subject] = append(groups[subject], i)
	}
	for _, idx := range groups {
		provided := make([]int, len(idx))
		for k, i := range idx {
			provided[k] = receipts[i].ChainOrdinal
		}
		if validChainOrdinals(provided) {
			continue // propagated — already the complete chain sequence
		}
		group := make([]EvidenceExportReceipt, len(idx))
		for k, i := range idx {
			group[k] = receipts[i]
		}
		derived := deriveReceiptChainOrdinals(group)
		for k, i := range idx {
			if derived != nil {
				receipts[i].ChainOrdinal = derived[k]
			} else {
				receipts[i].ChainOrdinal = 0
			}
		}
	}
}

// CanonicalizeEvidenceExportBundle normalizes every array of b to its canonical
// deterministic order and guarantees non-nil (empty, never null) arrays. Order
// keys are data-derived total orders, so a fixed dataset always canonicalizes
// to the identical byte sequence:
//
//   - objects, lifecycleStates: by object id (the content address);
//   - retentionPolicies: by (jurisdiction, legislation, category, version, id);
//   - holds: by (object id, placedAt, hold id);
//   - purgeRequests: by (object id, request id);
//   - purgeApprovals: by (request id, approvalOrder, approval id);
//   - purgeExecutions: by (request id, execution id);
//   - lifecycleEvents: by (object id, createdAt, event id);
//   - signingKeys: by key id;
//   - receipts: by (subject id, issuedAt, chain ordinal) — the store's
//     per-subject chain order, preserved verbatim. The chain ordinal is the
//     explicit stable tie-break that keeps equal issued_at (same-second)
//     chain receipts in their emission order — never receipt-hash order.
//     The store propagates the ordinal (its query's (issuedAt, emission)
//     position); a subject without usable ordinals is established from its
//     previousReceiptHash links when they form one linear chain (chain
//     reconstruction); the deterministic receipt-hash key remains a final
//     tie-break ONLY for ambiguous inputs a store bundle can never produce
//     (equal subject, issuedAt AND chain ordinal). The ordinal is part of
//     the canonical content, so the content hash commits to the chain order.

func CanonicalizeEvidenceExportBundle(b *EvidenceExportBundle) {
	sort.Slice(b.Objects, func(i, j int) bool { return b.Objects[i].Object.ObjectID < b.Objects[j].Object.ObjectID })
	sort.Slice(b.LifecycleStates, func(i, j int) bool { return b.LifecycleStates[i].ObjectID < b.LifecycleStates[j].ObjectID })
	sort.Slice(b.RetentionPolicies, func(i, j int) bool {
		a, c := b.RetentionPolicies[i], b.RetentionPolicies[j]
		if a.Jurisdiction != c.Jurisdiction {
			return a.Jurisdiction < c.Jurisdiction
		}
		if a.Legislation != c.Legislation {
			return a.Legislation < c.Legislation
		}
		if a.Category != c.Category {
			return a.Category < c.Category
		}
		if a.Version != c.Version {
			return a.Version < c.Version
		}
		return a.PolicyID < c.PolicyID
	})
	sort.Slice(b.Holds, func(i, j int) bool {
		a, c := b.Holds[i], b.Holds[j]
		if a.ObjectID != c.ObjectID {
			return a.ObjectID < c.ObjectID
		}
		if a.PlacedAt != c.PlacedAt {
			return a.PlacedAt < c.PlacedAt
		}
		return a.HoldID < c.HoldID
	})
	sort.Slice(b.PurgeRequests, func(i, j int) bool {
		a, c := b.PurgeRequests[i], b.PurgeRequests[j]
		if a.ObjectID != c.ObjectID {
			return a.ObjectID < c.ObjectID
		}
		return a.RequestID < c.RequestID
	})
	sort.Slice(b.PurgeApprovals, func(i, j int) bool {
		a, c := b.PurgeApprovals[i], b.PurgeApprovals[j]
		if a.RequestID != c.RequestID {
			return a.RequestID < c.RequestID
		}
		if a.ApprovalOrder != c.ApprovalOrder {
			return a.ApprovalOrder < c.ApprovalOrder
		}
		return a.ApprovalID < c.ApprovalID
	})
	sort.Slice(b.PurgeExecutions, func(i, j int) bool {
		a, c := b.PurgeExecutions[i], b.PurgeExecutions[j]
		if a.RequestID != c.RequestID {
			return a.RequestID < c.RequestID
		}
		return a.ExecutionID < c.ExecutionID
	})
	sort.Slice(b.LifecycleEvents, func(i, j int) bool {
		a, c := b.LifecycleEvents[i], b.LifecycleEvents[j]
		if a.ObjectID != c.ObjectID {
			return a.ObjectID < c.ObjectID
		}
		if a.CreatedAt != c.CreatedAt {
			return a.CreatedAt < c.CreatedAt
		}
		return a.EventID < c.EventID
	})
	sort.Slice(b.SigningKeys, func(i, j int) bool { return b.SigningKeys[i].KeyID < b.SigningKeys[j].KeyID })
	// Receipts: per-subject chain order (subject id, issuedAt, chain ordinal) —
	// the store's (issuedAt, emission) chain order preserved verbatim: the
	// chain ordinal (established below) is the same-second tie-break — NEVER
	// the receipt hash — so equal issued_at chain receipts keep their emission
	// order and the content hash commits to the chain order. The receipt-hash
	// key is a final tie-break ONLY for ambiguous inputs (equal subject,
	// issuedAt AND chain ordinal) that a store bundle can never produce (its
	// per-subject ordinals are the complete chain sequence).
	establishCanonicalReceiptOrdinals(b.Receipts)
	sort.Slice(b.Receipts, func(i, j int) bool {
		a, c := b.Receipts[i], b.Receipts[j]
		if a.Receipt.SubjectID != c.Receipt.SubjectID {
			return a.Receipt.SubjectID < c.Receipt.SubjectID
		}
		if a.Receipt.IssuedAt != c.Receipt.IssuedAt {
			return a.Receipt.IssuedAt < c.Receipt.IssuedAt
		}
		if a.ChainOrdinal != c.ChainOrdinal {
			return a.ChainOrdinal < c.ChainOrdinal
		}
		return a.ReceiptHash < c.ReceiptHash
	})
	b.Objects = emptyIfNil(b.Objects)
	b.LifecycleStates = emptyIfNil(b.LifecycleStates)
	b.RetentionPolicies = emptyIfNil(b.RetentionPolicies)
	b.Holds = emptyIfNil(b.Holds)
	b.PurgeRequests = emptyIfNil(b.PurgeRequests)
	b.PurgeApprovals = emptyIfNil(b.PurgeApprovals)
	b.PurgeExecutions = emptyIfNil(b.PurgeExecutions)
	b.LifecycleEvents = emptyIfNil(b.LifecycleEvents)
	b.Receipts = emptyIfNil(b.Receipts)
	b.SigningKeys = emptyIfNil(b.SigningKeys)
}

func emptyIfNil[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}

// ──────────────────────────────────────────────
// Deterministic generatedAt
// ──────────────────────────────────────────────

// EvidenceExportGeneratedAt returns the DETERMINISTIC "as of" timestamp of the
// bundle: the maximum (lexicographic — every stored timestamp is RFC3339 UTC,
// and RFC3339 sorts chronologically lexicographically) across every timestamp
// of every included row (objects storedAt, lifecycle-state updatedAt, policy
// createdAt, hold placedAt/liftedAt, request requestedAt/approvedAt, approval
// createdAt, execution intentAt/completedAt, event createdAt, receipt issuedAt,
// signing-key createdAt). Empty for an empty bundle. Derived from the data, so
// two exports of identical data produce the identical generatedAt — the export
// never consults a wall clock.
func EvidenceExportGeneratedAt(b EvidenceExportBundle) string {
	max := ""
	consider := func(ts string) {
		if ts > max {
			max = ts
		}
	}
	for _, o := range b.Objects {
		consider(o.Object.StoredAt)
	}
	for _, rs := range b.LifecycleStates {
		consider(rs.UpdatedAt)
	}
	for _, p := range b.RetentionPolicies {
		consider(p.CreatedAt)
	}
	for _, h := range b.Holds {
		consider(h.PlacedAt)
		consider(h.LiftedAt)
	}
	for _, r := range b.PurgeRequests {
		consider(r.RequestedAt)
		consider(r.ApprovedAt)
	}
	for _, a := range b.PurgeApprovals {
		consider(a.CreatedAt)
	}
	for _, e := range b.PurgeExecutions {
		consider(e.IntentAt)
		consider(e.CompletedAt)
	}
	for _, ev := range b.LifecycleEvents {
		consider(ev.CreatedAt)
	}
	for _, r := range b.Receipts {
		consider(r.Receipt.IssuedAt)
	}
	for _, k := range b.SigningKeys {
		consider(k.CreatedAt)
	}
	return max
}

// EvidenceExportCountsOf derives the row-count summary of a bundle (the counts
// the manifest hash commits to).
func EvidenceExportCountsOf(b EvidenceExportBundle) EvidenceExportCounts {
	return EvidenceExportCounts{
		Objects:           len(b.Objects),
		LifecycleStates:   len(b.LifecycleStates),
		RetentionPolicies: len(b.RetentionPolicies),
		Holds:             len(b.Holds),
		PurgeRequests:     len(b.PurgeRequests),
		PurgeApprovals:    len(b.PurgeApprovals),
		PurgeExecutions:   len(b.PurgeExecutions),
		LifecycleEvents:   len(b.LifecycleEvents),
		Receipts:          len(b.Receipts),
		SigningKeys:       len(b.SigningKeys),
	}
}

// ──────────────────────────────────────────────
// Self-hashing manifest (the byte contract)
// ──────────────────────────────────────────────

// canonicalExportManifestCore is the canonical JSON shape of the manifest
// ENVELOPE core — the bytes the envelope hash covers. The struct field order IS
// the property order (Go marshals in declaration order): version, scope,
// criteria, generatedAt, counts, contentManifestHash. exportId and bundleHash
// are DERIVED from these bytes and never participate (a hash cannot cover
// itself; the verifier recomputes the core from the manifest fields and
// compares). generatedAt is derived from the data (the maximum row timestamp)
// and participates ONLY here — never in the content hash, so equivalent content
// can never differ on a timestamp.
type canonicalExportManifestCore struct {
	Version             string                 `json:"version"`
	Scope               Scope                  `json:"scope"`
	Criteria            EvidenceExportCriteria `json:"criteria"`
	GeneratedAt         string                 `json:"generatedAt"`
	Counts              EvidenceExportCounts   `json:"counts"`
	ContentManifestHash string                 `json:"contentManifestHash"`
}

// canonicalExportManifest is the canonical JSON shape of the WIRE manifest: the
// core fields in the same fixed order PLUS the derived exportId (after version)
// and bundleHash (last).
type canonicalExportManifest struct {
	Version             string                 `json:"version"`
	ExportID            string                 `json:"exportId"`
	Scope               Scope                  `json:"scope"`
	Criteria            EvidenceExportCriteria `json:"criteria"`
	GeneratedAt         string                 `json:"generatedAt"`
	Counts              EvidenceExportCounts   `json:"counts"`
	ContentManifestHash string                 `json:"contentManifestHash"`
	BundleHash          string                 `json:"bundleHash"`
}

// canonicalExportContent is the canonical JSON shape of the bundle CONTENT — the
// bytes the content hash covers: every data array in canonical order (the SAME
// shapes CanonicalEvidenceExportBundleJSON emits), with NO manifest fields (the
// content hash cannot cover the derived manifest without circularity) and NO
// generatedAt (data-derived envelope field — equivalent content never differs on
// a timestamp).
type canonicalExportContent struct {
	Objects           []EvidenceObjectExport     `json:"objects"`
	LifecycleStates   []EvidenceRetentionState   `json:"lifecycleStates"`
	RetentionPolicies []RetentionPolicy          `json:"retentionPolicies"`
	Holds             []EvidenceHold             `json:"holds"`
	PurgeRequests     []EvidencePurgeRequest     `json:"purgeRequests"`
	PurgeApprovals    []EvidencePurgeApproval    `json:"purgeApprovals"`
	PurgeExecutions   []EvidencePurgeExecution   `json:"purgeExecutions"`
	LifecycleEvents   []EvidenceLifecycleEvent   `json:"lifecycleEvents"`
	Receipts          []EvidenceExportReceipt    `json:"receipts"`
	SigningKeys       []EvidenceExportSigningKey `json:"signingKeys"`
}

// ComputeEvidenceExportContentHash returns the CONTENT hash of a bundle: the
// lowercase SHA-256 hex of the canonical CONTENT bytes (every array/row in
// canonical order, receipts sorted by their per-subject chain order —
// (subject, issuedAt, chain ordinal), the store's (issuedAt, emission)
// sequence — compact UTF-8 JSON, NO HTML escaping). The hash commits to the
// EXACT rows of the bundle and to the chain order (the ordinal is part of
// the canonical content): identical canonical content -> identical hash;
// any row-content change -> a different hash EVEN when counts are
// unchanged; permuted source order -> the identical canonical bundle and hash
// (the input is canonicalized defensively on a COPY — the returned bundle stays
// untouched). generatedAt and the counts are envelope fields and never
// participate here.
func ComputeEvidenceExportContentHash(b EvidenceExportBundle) string {
	CanonicalizeEvidenceExportBundle(&b)
	return sha256HexBytes(canonicalJSONBytes(canonicalExportContent{
		Objects:           b.Objects,
		LifecycleStates:   b.LifecycleStates,
		RetentionPolicies: b.RetentionPolicies,
		Holds:             b.Holds,
		PurgeRequests:     b.PurgeRequests,
		PurgeApprovals:    b.PurgeApprovals,
		PurgeExecutions:   b.PurgeExecutions,
		LifecycleEvents:   b.LifecycleEvents,
		Receipts:          b.Receipts,
		SigningKeys:       b.SigningKeys,
	}))
}

// ComputeEvidenceExportBundleHash returns the ENVELOPE hash of a manifest: the
// lowercase SHA-256 hex of the canonical manifest-core bytes (version, scope,
// criteria, generatedAt, counts, contentManifestHash — fixed property order,
// compact UTF-8 JSON, NO HTML escaping). The envelope commits to the requested
// scope, the deterministic as-of timestamp and every row count AND to the exact
// canonical content via contentManifestHash — the content itself is hashed by
// ComputeEvidenceExportContentHash.
func ComputeEvidenceExportBundleHash(m EvidenceExportManifest) string {
	return sha256HexBytes(canonicalJSONBytes(canonicalExportManifestCore{
		Version:             m.Version,
		Scope:               m.Scope,
		Criteria:            m.Criteria,
		GeneratedAt:         m.GeneratedAt,
		Counts:              m.Counts,
		ContentManifestHash: m.ContentManifestHash,
	}))
}

// BuildEvidenceExportManifest constructs the self-hashing manifest root: the
// version, the content-addressed exportId ("evidence-export/v0.8.0:<contentHash>"),
// the applied scope, the verbatim criteria envelope, the deterministic
// generatedAt and the counts, with ContentManifestHash = contentHash (the
// canonical CONTENT digest computed by ComputeEvidenceExportContentHash) and
// BundleHash = ComputeEvidenceExportBundleHash (the ENVELOPE digest).
func BuildEvidenceExportManifest(scope Scope, criteria EvidenceExportCriteria, counts EvidenceExportCounts, generatedAt, contentHash string) EvidenceExportManifest {
	m := EvidenceExportManifest{
		Version:             EvidenceExportModelVersion,
		Scope:               scope,
		Criteria:            criteria,
		GeneratedAt:         generatedAt,
		Counts:              counts,
		ContentManifestHash: contentHash,
	}
	m.BundleHash = ComputeEvidenceExportBundleHash(m)
	m.ExportID = EvidenceExportModelVersion + ":" + m.ContentManifestHash
	return m
}

// CanonicalEvidenceExportManifestJSON returns the canonical compact UTF-8 JSON
// bytes of a manifest (fixed property order, JSON string escaping, NO HTML
// escaping).
func CanonicalEvidenceExportManifestJSON(m EvidenceExportManifest) []byte {
	return canonicalJSONBytes(canonicalExportManifest{
		Version:             m.Version,
		ExportID:            m.ExportID,
		Scope:               m.Scope,
		Criteria:            m.Criteria,
		GeneratedAt:         m.GeneratedAt,
		Counts:              m.Counts,
		ContentManifestHash: m.ContentManifestHash,
		BundleHash:          m.BundleHash,
	})
}

// CanonicalEvidenceExportBundleJSON returns the canonical compact UTF-8 JSON
// bytes of a full bundle (fixed property order, JSON string escaping, NO HTML
// escaping) — the deterministic wire form two exports of identical data must
// reproduce byte-for-byte. Callers MUST canonicalize (CanonicalizeEvidenceExportBundle)
// before marshaling.
func CanonicalEvidenceExportBundleJSON(b EvidenceExportBundle) []byte {
	return canonicalJSONBytes(b)
}

// ──────────────────────────────────────────────
// Fail-closed scope-coverage validation
// ──────────────────────────────────────────────

// ValidateEvidenceExportScopeCoverage fails closed on ANY row that would cross
// the tenant/company/RUC/period boundary of the manifest scope: every row must
// belong to the exact tenant + company + RUC, and a perioded criteria must match
// the exact period (an empty criteria period admits every period of the RUC —
// period is the LAST scope dimension, never a leak of a different period into a
// perioded export). Rows without their own scope columns are validated through
// their scope authority: events/retention states through their object, approvals
// and executions through their request, receipts through their subject AND their
// stored scope columns. Bound retention policies may be tenant-level (empty
// company/RUC/period) or exact-company; every non-empty policy scope field must
// match the criteria. A violation is corruption — the export fails closed, never
// silently drops the row.
func ValidateEvidenceExportScopeCoverage(b EvidenceExportBundle) error {
	scope := b.Manifest.Scope
	if scope.Kind != ScopeKindCompany || scope.OrganizationID == "" || scope.CompanyID == "" || !IsValidRUC(scope.RUC) {
		return exportScopeViolation("criteria", "scope", "the manifest scope is not an exact company scope (tenant/company/RUC)")
	}

	objectScopes := make(map[string]Scope, len(b.Objects))
	for _, o := range b.Objects {
		if !exportRowInScope(scope, o.Object.TenantID, o.Object.CompanyID, o.Object.RUC, o.Object.Period) {
			return exportScopeViolation("object", o.Object.ObjectID, exportScopeDetail(scope, o.Object.TenantID, o.Object.CompanyID, o.Object.RUC, o.Object.Period))
		}
		objectScopes[o.Object.ObjectID] = Scope{
			Kind:           ScopeKindCompany,
			OrganizationID: o.Object.TenantID,
			CompanyID:      o.Object.CompanyID,
			RUC:            o.Object.RUC,
			Period:         o.Object.Period,
		}
	}

	requestScopes := make(map[string]Scope, len(b.PurgeRequests))
	for _, r := range b.PurgeRequests {
		if !exportRowInScope(scope, r.TenantID, r.CompanyID, r.RUC, r.Period) {
			return exportScopeViolation("purge request", r.RequestID, exportScopeDetail(scope, r.TenantID, r.CompanyID, r.RUC, r.Period))
		}
		requestScopes[r.RequestID] = Scope{
			Kind:           ScopeKindCompany,
			OrganizationID: r.TenantID,
			CompanyID:      r.CompanyID,
			RUC:            r.RUC,
			Period:         r.Period,
		}
	}

	for _, h := range b.Holds {
		if !exportRowInScope(scope, h.TenantID, h.CompanyID, h.RUC, h.Period) {
			return exportScopeViolation("hold", h.HoldID, exportScopeDetail(scope, h.TenantID, h.CompanyID, h.RUC, h.Period))
		}
	}

	for _, p := range b.RetentionPolicies {
		if !exportPolicyInScope(scope, p) {
			return exportScopeViolation("retention policy", p.PolicyID, "tenant/company/RUC/period fields do not match the requested scope")
		}
	}

	for _, rs := range b.LifecycleStates {
		os, ok := objectScopes[rs.ObjectID]
		if !ok {
			return exportScopeViolation("lifecycle state", rs.ObjectID, "no object with this id is in the requested scope")
		}
		_ = os
	}

	for _, a := range b.PurgeApprovals {
		rs, ok := requestScopes[a.RequestID]
		if !ok {
			return exportScopeViolation("purge approval", a.ApprovalID, "no purge request with this id is in the requested scope")
		}
		if !exportRowInScope(scope, rs.OrganizationID, rs.CompanyID, rs.RUC, rs.Period) {
			return exportScopeViolation("purge approval", a.ApprovalID, "its purge request escapes the requested scope")
		}
	}

	for _, e := range b.PurgeExecutions {
		rs, ok := requestScopes[e.RequestID]
		if !ok {
			return exportScopeViolation("purge execution", e.ExecutionID, "no purge request with this id is in the requested scope")
		}
		if !exportRowInScope(scope, rs.OrganizationID, rs.CompanyID, rs.RUC, rs.Period) {
			return exportScopeViolation("purge execution", e.ExecutionID, "its purge request escapes the requested scope")
		}
	}

	for _, ev := range b.LifecycleEvents {
		os, ok := objectScopes[ev.ObjectID]
		if !ok {
			return exportScopeViolation("lifecycle event", ev.EventID, "no object with this id is in the requested scope")
		}
		if ev.RequestID != "" {
			if rs, ok := requestScopes[ev.RequestID]; !ok {
				return exportScopeViolation("lifecycle event", ev.EventID, "references a purge request outside the requested scope")
			} else if !exportRowInScope(scope, rs.OrganizationID, rs.CompanyID, rs.RUC, rs.Period) {
				return exportScopeViolation("lifecycle event", ev.EventID, "its purge request escapes the requested scope")
			}
		}
		_ = os
	}

	for _, r := range b.Receipts {
		// The receipt's RUC dimension is enforced through its subject: the receipt
		// must belong to a scoped evidence object (the JOIN in the store and this
		// subject check). The envelope carries no RUC field, so the stamped scope
		// columns cover tenant/company and the period dimension.
		if _, ok := objectScopes[r.Receipt.SubjectID]; !ok {
			return exportScopeViolation("receipt", r.ReceiptHash, "its subject (evidence object) is not in the requested scope")
		}
		if r.Receipt.TenantID != scope.OrganizationID || r.Receipt.CompanyID != scope.CompanyID {
			return exportScopeViolation("receipt", r.ReceiptHash, "stamped scope columns (tenant="+r.Receipt.TenantID+", company="+r.Receipt.CompanyID+") differ from the requested scope")
		}
		if scope.Period != "" && r.Receipt.FiscalPeriodID != "" && r.Receipt.FiscalPeriodID != scope.Period {
			return exportScopeViolation("receipt", r.ReceiptHash, "stamped fiscalPeriodId "+r.Receipt.FiscalPeriodID+" differs from the requested period "+scope.Period)
		}
	}

	return nil
}

// exportRowInScope is the exact scope predicate: tenant/company/RUC must equal
// the criteria, and a perioded criteria must match the row's period exactly (an
// empty criteria period admits every period of the RUC).
func exportRowInScope(scope Scope, tenantID, companyID, ruc, period string) bool {
	if tenantID != scope.OrganizationID || companyID != scope.CompanyID || ruc != scope.RUC {
		return false
	}
	return scope.Period == "" || period == scope.Period
}

// exportPolicyInScope admits tenant-level policies (empty company/RUC/period)
// and exact-company policies of the criteria: every NON-EMPTY policy scope field
// must match the corresponding criteria field (a tenant-level policy of the
// tenant is in scope; a company policy of a different company/RUC/period is not).
func exportPolicyInScope(scope Scope, p RetentionPolicy) bool {
	if p.TenantID != scope.OrganizationID {
		return false
	}
	if p.CompanyID != "" && p.CompanyID != scope.CompanyID {
		return false
	}
	if p.RUC != "" && p.RUC != scope.RUC {
		return false
	}
	if p.Period != "" && scope.Period != "" && p.Period != scope.Period {
		return false
	}
	return true
}

// exportScopeDetail renders the human-readable scope mismatch evidence of a row.
func exportScopeDetail(scope Scope, tenantID, companyID, ruc, period string) string {
	return "row scope (tenant=" + tenantID + ", company=" + companyID + ", ruc=" + ruc + ", period=" + period +
		") differs from the requested scope (tenant=" + scope.OrganizationID + ", company=" + scope.CompanyID +
		", ruc=" + scope.RUC + ", period=" + scope.Period + ")"
}

// ──────────────────────────────────────────────
// Bundle validation (fail closed)
// ──────────────────────────────────────────────

// AssertValidEvidenceExportBundle fails closed on a self-inconsistent bundle:
// the frozen version, a contentManifestHash that matches the canonical CONTENT
// digest (recomputed over a canonicalized copy — a non-canonical bundle fails
// closed), a bundleHash that matches the canonical ENVELOPE core, a
// content-addressed exportId (bound to contentManifestHash), counts that equal
// the array lengths, valid object metadata with a closed byte-state token (a
// stored or intended object carries NO purge receipt hash; a purged object
// carries the receipt hash when a signer exists), a criteria that validates, and
// full signing-key coverage (every receipt's key must have an exported
// public-key row — an unverifiable chain is never exported). Scope coverage is
// validated by ValidateEvidenceExportScopeCoverage (also run here, so a single
// call closes both).
func AssertValidEvidenceExportBundle(b EvidenceExportBundle) error {
	if b.Manifest.Version != EvidenceExportModelVersion {
		return fmt.Errorf("INVALID_EXPORT_BUNDLE: manifest version %q differs from the frozen %q", b.Manifest.Version, EvidenceExportModelVersion)
	}
	if err := AssertValidEvidenceExportCriteria(b.Manifest.Criteria); err != nil {
		return err
	}
	if err := ValidateEvidenceExportScopeCoverage(b); err != nil {
		return err
	}
	// The CONTENT hash binds the exact canonical rows: it is recomputed over a
	// canonicalized copy, so a bundle whose arrays are not in canonical order or
	// whose rows differ from the committed content digest fails closed.
	contentHash := ComputeEvidenceExportContentHash(b)
	if b.Manifest.ContentManifestHash != contentHash {
		return fmt.Errorf("INVALID_EXPORT_BUNDLE: manifest contentManifestHash %q does not match the canonical content digest %q", b.Manifest.ContentManifestHash, contentHash)
	}
	envelopeHash := ComputeEvidenceExportBundleHash(b.Manifest)
	if b.Manifest.BundleHash != envelopeHash {
		return fmt.Errorf("INVALID_EXPORT_BUNDLE: manifest bundleHash %q does not match the canonical envelope digest %q", b.Manifest.BundleHash, envelopeHash)
	}
	if b.Manifest.ExportID != EvidenceExportModelVersion+":"+b.Manifest.ContentManifestHash {
		return fmt.Errorf("INVALID_EXPORT_BUNDLE: exportId %q is not the content-addressed identity of contentManifestHash %q", b.Manifest.ExportID, b.Manifest.ContentManifestHash)
	}
	if counts := EvidenceExportCountsOf(b); counts != b.Manifest.Counts {
		return fmt.Errorf("INVALID_EXPORT_BUNDLE: manifest counts %+v differ from the bundle arrays %+v", b.Manifest.Counts, counts)
	}
	for _, o := range b.Objects {
		if err := AssertValidEvidenceObject(o.Object); err != nil {
			return fmt.Errorf("INVALID_EXPORT_BUNDLE: object %s: %w", o.Object.ObjectID, err)
		}
		if !IsValidEvidenceBytesState(o.BytesState) {
			return fmt.Errorf("INVALID_EXPORT_BUNDLE: object %s has an unknown byte-state token %q", o.Object.ObjectID, o.BytesState)
		}
		switch o.BytesState {
		case EvidenceBytesStored, EvidenceBytesIntended:
			if o.PurgeExecutedReceiptHash != "" {
				return fmt.Errorf("INVALID_EXPORT_BUNDLE: %s object %s must not carry a purgeExecutedReceiptHash (no completion exists)", o.BytesState, o.Object.ObjectID)
			}
		case EvidenceBytesPurged:
			if o.PurgeExecutedReceiptHash != "" && !hash64Pattern.MatchString(o.PurgeExecutedReceiptHash) {
				return fmt.Errorf("INVALID_EXPORT_BUNDLE: purged object %s carries a malformed purgeExecutedReceiptHash", o.Object.ObjectID)
			}
		}
	}
	// Signing-key completeness: every receipt's key must be exported (a chain the
	// recipient cannot verify is never silently shipped).
	referenced := make(map[string]struct{}, len(b.Receipts))
	for _, r := range b.Receipts {
		if r.Receipt.KeyID != "" {
			referenced[r.Receipt.KeyID] = struct{}{}
		}
	}
	exported := make(map[string]struct{}, len(b.SigningKeys))
	for _, k := range b.SigningKeys {
		exported[k.KeyID] = struct{}{}
	}
	for keyID := range referenced {
		if _, ok := exported[keyID]; !ok {
			return fmt.Errorf("INVALID_EXPORT_BUNDLE: receipt key %s has no exported public-key row (unverifiable chain)", keyID)
		}
	}
	return nil
}
