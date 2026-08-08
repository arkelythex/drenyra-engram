// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the PURE retention-policy model of the v0.8
// evidence lifecycle (batch 2 — docs/architecture/evidence-lifecycle-v0.8.md
// §3.1/§6):
//
//   - a RetentionPolicy is deployment-policy evidence, NEVER a statutory
//     assertion: every row REQUIRES jurisdiction/legislation/authority/source
//     (absence is POLICY_EVIDENCE_REQUIRED at the store), and this repository
//     asserts NO statutory retention period for any jurisdiction (§1
//     governance decision);
//   - policies are immutable and versioned: one row per version, supersession
//     via supersedesPolicyId, never an in-place edit; the exact
//     tenant/company/RUC/period scope tuple is part of every row (exact scope
//     columns, repository convention) and of the resolution key;
//   - resolution is exact: (exact scope, jurisdiction, legislation, category)
//     against the HIGHEST version of an ENABLED policy; zero matches or any
//     ambiguity fail closed (UNKNOWN_RETENTION_STATE /
//     RETENTION_POLICY_AMBIGUOUS) — the engine never guesses a retention
//     outcome;
//   - eligibility is a separate pure dimension (§6): `eligible` (resolved and
//     the object's period reached the policy's min_period) · `not_due`
//     (resolved, period not reached) · `unknown` (unresolvable). Only
//     `eligible` may ever permit a request; the engine NEVER auto-deletes and
//     NEVER claims a statutory duration — minPeriod is the deployment-declared
//     YYYYMM retention floor, compared as a zero-padded 6-digit period string
//     (lexicographic order IS chronological order for YYYYMM).
//
// This module is PURE: no I/O, no store, no clock. It owns the closed model,
// the fail-closed validators and the canonical byte contract (fixed property
// order, compact UTF-8 JSON, NO HTML escaping — byte-identical with the
// TypeScript mirror in core/retention-policy.ts). Persistence, the
// authenticated put gate, tenant-scoped idempotency and scope-first reads live
// in internal/store (retention_policy_store.go).
package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// RetentionPolicyVersionPrefix is the version-prefix of the v0.8 retention
// policy model (stamped on store results). The design freezes the policy
// version evidence-lifecycle-policy/v0.8.0 for AUTHORIZATION; the retention
// model itself is versioned per row via the Version field.
const RetentionPolicyModelVersion = "retention-policy/v0.8.0"

// RetentionEligibility is the pure eligibility dimension (design §6): the
// resolution outcome of an exact active policy against an object's period.
// `eligible` is the ONLY state that may permit a purge request; `unknown` and
// `not_due` block, and the engine never auto-deletes.
type RetentionEligibility string

const (
	// RetentionEligibilityEligible means an exact active policy resolved AND
	// the object's period reached the policy's min_period floor.
	RetentionEligibilityEligible RetentionEligibility = "eligible"
	// RetentionEligibilityNotDue means an exact active policy resolved but the
	// object's period has NOT reached the policy's min_period floor.
	RetentionEligibilityNotDue RetentionEligibility = "not_due"
	// RetentionEligibilityUnknown means no exact active policy resolved (or the
	// resolution was ambiguous) — fail closed, never guessed.
	RetentionEligibilityUnknown RetentionEligibility = "unknown"
)

// IsValidRetentionEligibility reports whether e is a known eligibility value.
func IsValidRetentionEligibility(e RetentionEligibility) bool {
	switch e {
	case RetentionEligibilityEligible, RetentionEligibilityNotDue, RetentionEligibilityUnknown:
		return true
	}
	return false
}

// Frozen closed enums of the v0.8 retention policy (§3.1): dual-approval roles
// and blocking hold kinds are validated against these at insert and persisted
// as canonical sorted JSON (fixed property order for hashing).
const (
	// RetentionDualApprovalRoleController is a dual-approval second approver.
	RetentionDualApprovalRoleController = "controller"
	// RetentionDualApprovalRoleTaxResponsible is a dual-approval second approver.
	RetentionDualApprovalRoleTaxResponsible = "tax_responsible"

	// RetentionHoldKindLegal is a legal hold (blocks purge by default).
	RetentionHoldKindLegal = "legal"
	// RetentionHoldKindAudit is an audit hold (blocks purge by default).
	RetentionHoldKindAudit = "audit"
	// RetentionHoldKindDispute is a dispute hold (blocks purge by default).
	RetentionHoldKindDispute = "dispute"
	// RetentionHoldKindFiscalization is a fiscalization hold (blocks purge by
	// default).
	RetentionHoldKindFiscalization = "fiscalization"
	// RetentionHoldKindOther is a non-blocking-by-default hold kind.
	RetentionHoldKindOther = "other"
)

// DefaultDualApproverRoles is the frozen default of
// retention_policies.dual_approver_roles (§3.1).
var DefaultDualApproverRoles = []string{
	RetentionDualApprovalRoleController,
	RetentionDualApprovalRoleTaxResponsible,
}

// DefaultBlockingHoldKinds is the frozen default of
// retention_policies.blocking_hold_kinds (§3.1/§7).
var DefaultBlockingHoldKinds = []string{
	RetentionHoldKindLegal,
	RetentionHoldKindAudit,
	RetentionHoldKindDispute,
	RetentionHoldKindFiscalization,
}

// IsValidRetentionDualApprovalRole reports whether r is a closed dual-approval
// role token.
func IsValidRetentionDualApprovalRole(r string) bool {
	return r == RetentionDualApprovalRoleController || r == RetentionDualApprovalRoleTaxResponsible
}

// IsValidRetentionHoldKind reports whether k is a closed hold-kind token.
func IsValidRetentionHoldKind(k string) bool {
	switch k {
	case RetentionHoldKindLegal, RetentionHoldKindAudit, RetentionHoldKindDispute,
		RetentionHoldKindFiscalization, RetentionHoldKindOther:
		return true
	}
	return false
}

// RetentionPolicy is ONE immutable, versioned retention-policy row (design
// §3.1). The scope tuple is flattened (tenantId/companyId/ruc/period) exactly
// like EvidenceObject so the wire shape mirrors the DB row and the TypeScript
// mirror byte-for-byte; companyId/ruc/period are empty for a tenant-level
// policy. MinPeriod is the deployment-declared YYYYMM retention floor — the
// engine makes NO statutory duration claim about it. Version is a JSON
// integer (never a float) that counts the supersession chain
// (jurisdiction/legislation/category) revisions; SupersedesPolicyID links the
// chain, never an in-place edit.
type RetentionPolicy struct {
	PolicyID  string `json:"policyId"`
	TenantID  string `json:"tenantId"`
	CompanyID string `json:"companyId,omitempty"`
	RUC       string `json:"ruc,omitempty"`
	Period    string `json:"period,omitempty"`
	// ── policy evidence (required — POLICY_EVIDENCE_REQUIRED when absent) ──
	Jurisdiction string `json:"jurisdiction"`
	Legislation  string `json:"legislation"`
	Authority    string `json:"authority"`
	Source       string `json:"source"`
	Category     string `json:"category"`
	MinPeriod    string `json:"minPeriod"`
	// Version is the supersession-chain counter (1-based); SupersedesPolicyID
	// is the previous version's row id (empty for version 1).
	Version            int64  `json:"version"`
	SupersedesPolicyID string `json:"supersedesPolicyId,omitempty"`
	// DualApprovalRequired is the deployment configuration for the category
	// (§8.1): when true, a second approval by a dual-approval role is required.
	DualApprovalRequired bool `json:"dualApprovalRequired"`
	// DualApproverRoles is canonical SORTED JSON, subset of the closed enum
	// {controller, tax_responsible} (default when empty).
	DualApproverRoles []string `json:"dualApproverRoles"`
	// BlockingHoldKinds is canonical SORTED JSON, subset of the closed enum
	// {legal, audit, dispute, fiscalization, other} (default when empty).
	BlockingHoldKinds []string `json:"blockingHoldKinds"`
	// Enabled gates resolution: only an enabled policy can resolve (§6).
	Enabled bool `json:"enabled"`
	// CreatedAt is the automatic immutable insertion timestamp (RFC3339 UTC).
	CreatedAt string `json:"createdAt"`
	// CreatedBy is the authenticated principal that put the policy.
	CreatedBy string `json:"createdBy"`
}

// PutRetentionPolicyCommand is the store command for ONE policy put (batch 2):
// an authenticated principal writes a NEW immutable policy version. Scope is
// the exact tenant/company/RUC/period tuple (tenant-level policies use empty
// company/RUC/period); the resolution evidence (jurisdiction, legislation,
// authority, source) is REQUIRED; ExpectedVersion is the version of the
// CURRENT highest policy on the (scope, jurisdiction, legislation, category)
// chain that the caller reviewed — 0 asserts "no current policy exists" and
// yields version 1; any drift fails with LIFECYCLE_VERSION_MISMATCH.
// RequestID is the tenant-scoped idempotency key.
type PutRetentionPolicyCommand struct {
	Scope           Scope  `json:"scope"`
	Jurisdiction    string `json:"jurisdiction"`
	Legislation     string `json:"legislation"`
	Authority       string `json:"authority"`
	Source          string `json:"source"`
	Category        string `json:"category"`
	MinPeriod       string `json:"minPeriod"`
	ExpectedVersion int64  `json:"expectedVersion"`
	// DualApprovalRequired mirrors the design §8.1 category configuration.
	DualApprovalRequired bool `json:"dualApprovalRequired"`
	// DualApproverRoles defaults to ["controller","tax_responsible"] when empty.
	DualApproverRoles []string `json:"dualApproverRoles,omitempty"`
	// BlockingHoldKinds defaults to the four frozen blocking kinds when empty.
	BlockingHoldKinds []string `json:"blockingHoldKinds,omitempty"`
	Enabled           bool     `json:"enabled"`
	RequestID         string   `json:"requestId"`
}

// PutRetentionPolicyResult is the outcome of a policy put. A successful put
// ALWAYS created the new immutable row (Created=true); IdempotentReplay=true
// means the (tenant, requestId) was already completed with the SAME command
// and principal, so the stored outcome is returned with NO new row.
type PutRetentionPolicyResult struct {
	Policy           RetentionPolicy `json:"policy"`
	Created          bool            `json:"created"`
	IdempotentReplay bool            `json:"idempotentReplay"`
}

// EvaluatePurgeEligibilityInput is the read-only evaluation input: the exact
// scope tuple, the resolution evidence and the object's fiscal period
// (YYYYMM). The engine makes NO statutory duration claim — it compares the
// object's period against the deployment-declared min_period floor only.
type EvaluatePurgeEligibilityInput struct {
	Scope        Scope  `json:"scope"`
	Jurisdiction string `json:"jurisdiction"`
	Legislation  string `json:"legislation"`
	Category     string `json:"category"`
	// ObjectPeriod is the object's fiscal period (YYYYMM), zero-padded.
	ObjectPeriod string `json:"objectPeriod"`
}

// RetentionEligibilityResult is the evaluation outcome: the eligibility
// dimension plus the exact policy evidence that resolved (never empty for a
// non-unknown result). Fail-closed: no exact active policy → the store returns
// UNKNOWN_RETENTION_STATE; this result never fabricates a policy.
type RetentionEligibilityResult struct {
	Eligibility   RetentionEligibility `json:"eligibility"`
	PolicyID      string               `json:"policyId,omitempty"`
	PolicyVersion int64                `json:"policyVersion,omitempty"`
	MinPeriod     string               `json:"minPeriod,omitempty"`
	Jurisdiction  string               `json:"jurisdiction,omitempty"`
	Legislation   string               `json:"legislation,omitempty"`
	Category      string               `json:"category,omitempty"`
	ModelVersion  string               `json:"modelVersion"`
}

// RetentionPolicyResolution is the outcome of a scope-first resolve read: the
// exact active policy plus the matched chain version. ok=false means no exact
// active policy (or an ambiguity — the store fails closed with
// RETENTION_POLICY_AMBIGUOUS, never guessing).
type RetentionPolicyResolution struct {
	Policy  RetentionPolicy `json:"policy"`
	Matched bool            `json:"matched"`
}

// jurisdictionPattern freezes the jurisdiction syntax (§3.1): uppercase, one
// letter then 1–15 letters/digits/hyphens.
var jurisdictionPattern = regexp.MustCompile(`^[A-Z][A-Z0-9-]{1,15}$`)

// JurisdictionOK reports whether the jurisdiction token matches the frozen
// syntax (uppercase ^[A-Z][A-Z0-9-]{1,15}$ — syntax only, never geopolitical
// truth).
func JurisdictionOK(jurisdiction string) bool {
	return jurisdictionPattern.MatchString(jurisdiction)
}

// AssertValidRetentionScope fails closed on the policy scope: institutional
// (cross-company) scopes are rejected (NOT_PURGEABLE at the store), the tenant
// (organizationId) is always required, and the company tuple is either fully
// absent (tenant-level policy — companyId/ruc/period all empty) or a full
// exact company scope (company-level policy). Exact-scope resolution therefore
// matches the exact tuple that was written, never a partial one.
func AssertValidRetentionScope(s Scope) error {
	if s.Kind != ScopeKindCompany {
		return fmt.Errorf("INVALID_RETENTION_SCOPE: retention policies require an exact company scope (institutional is not purgeable and not policy-scoped), got kind %q", s.Kind)
	}
	if s.OrganizationID == "" {
		return fmt.Errorf("INVALID_RETENTION_SCOPE: tenant (organizationId) must be a non-empty string")
	}
	if s.CompanyID == "" && s.RUC == "" && s.Period == "" {
		return nil // tenant-level policy
	}
	return AssertValidScope(s) // full exact company tuple
}

// AssertValidRetentionPolicy fails closed on a malformed policy: the exact
// scope must be a company scope, the resolution evidence
// (jurisdiction/legislation/authority/source) is REQUIRED (POLICY_EVIDENCE_REQUIRED),
// jurisdiction matches the frozen syntax, minPeriod is YYYYMM, version >= 1 and
// the closed-enum arrays are subsets of the frozen sets (canonical, deduped).
// It normalizes DualApproverRoles/BlockingHoldKinds to their canonical sorted
// form (defaults when empty).
func AssertValidRetentionPolicy(p *RetentionPolicy) error {
	if p == nil {
		return fmt.Errorf("INVALID_RETENTION_POLICY: nil policy")
	}
	if err := AssertValidRetentionScope(Scope{
		Kind:           ScopeKindCompany,
		OrganizationID: p.TenantID,
		CompanyID:      p.CompanyID,
		RUC:            p.RUC,
		Period:         p.Period,
	}); err != nil {
		return err
	}
	if !jurisdictionPattern.MatchString(p.Jurisdiction) {
		return fmt.Errorf("INVALID_RETENTION_JURISDICTION: jurisdiction must match ^[A-Z][A-Z0-9-]{1,15}$, got %q", p.Jurisdiction)
	}
	if strings.TrimSpace(p.Legislation) == "" {
		return fmt.Errorf("POLICY_EVIDENCE_REQUIRED: legislation must be a non-empty regime/family identifier")
	}
	if strings.TrimSpace(p.Authority) == "" {
		return fmt.Errorf("POLICY_EVIDENCE_REQUIRED: authority (policy owner/issuer) must be non-empty")
	}
	if strings.TrimSpace(p.Source) == "" {
		return fmt.Errorf("POLICY_EVIDENCE_REQUIRED: source (who decided, when, on what basis) must be non-empty")
	}
	if strings.TrimSpace(p.Category) == "" {
		return fmt.Errorf("INVALID_RETENTION_CATEGORY: category must be non-empty")
	}
	if !IsValidPeriod(p.MinPeriod) {
		return fmt.Errorf("INVALID_RETENTION_MIN_PERIOD: minPeriod must be YYYYMM with month 01-12, got %q", p.MinPeriod)
	}
	if p.Version < 1 {
		return fmt.Errorf("INVALID_RETENTION_VERSION: version must be >= 1 (JSON integer, never a float), got %d", p.Version)
	}
	if p.PolicyID != "" && !policyIDPattern(p.PolicyID) {
		return fmt.Errorf("INVALID_RETENTION_POLICY_ID: policyId must be a UUID, got %q", p.PolicyID)
	}
	var err error
	p.DualApproverRoles, err = canonicalRoleList(p.DualApproverRoles, DefaultDualApproverRoles, IsValidRetentionDualApprovalRole, "dualApproverRoles")
	if err != nil {
		return err
	}
	p.BlockingHoldKinds, err = canonicalRoleList(p.BlockingHoldKinds, DefaultBlockingHoldKinds, IsValidRetentionHoldKind, "blockingHoldKinds")
	if err != nil {
		return err
	}
	return nil
}

// policyIDPattern accepts the UUID shape minted by the store (newUUID).
func policyIDPattern(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, r := range id {
		if r == '-' && (i == 8 || i == 13 || i == 18 || i == 23) {
			continue
		}
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

// canonicalRoleList normalizes a closed-enum list to canonical sorted, deduped
// form, applying the default when empty and failing closed on any token outside
// the closed set (design §3.1: validated to the closed enum at insert).
func canonicalRoleList(tokens, defaults []string, valid func(string) bool, field string) ([]string, error) {
	if len(tokens) == 0 {
		out := append([]string(nil), defaults...)
		sort.Strings(out)
		return out, nil
	}
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !valid(t) {
			return nil, fmt.Errorf("INVALID_RETENTION_%s: %q is not in the frozen closed set", strings.ToUpper(field), t)
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// EvaluateRetentionEligibility is the PURE eligibility evaluation (design §6,
// batch 2): given the resolved exact active policy and the object's fiscal
// period, report eligible when the object's period REACHED the policy's
// min_period floor, not_due otherwise. YYYYMM periods are zero-padded, so
// lexicographic string comparison IS chronological order. Any invalid input
// (empty/malformed minPeriod or objectPeriod) fails closed to `unknown` — the
// engine never guesses a retention outcome. This function makes NO statutory
// duration claim: minPeriod is deployment-declared evidence, not a legal
// assertion.
func EvaluateRetentionEligibility(p RetentionPolicy, objectPeriod string) RetentionEligibility {
	if !IsValidPeriod(p.MinPeriod) || !IsValidPeriod(objectPeriod) {
		return RetentionEligibilityUnknown
	}
	if objectPeriod >= p.MinPeriod {
		return RetentionEligibilityEligible
	}
	return RetentionEligibilityNotDue
}

// ResolveRetentionPolicy is the PURE exact resolution (design §6): the exact
// scope tuple + (jurisdiction, legislation, category) match against the
// HIGHEST version of an ENABLED policy. Zero matches → ok=false; two or more
// enabled candidates sharing the same highest version → ok=false (ambiguity —
// the caller fails closed with RETENTION_POLICY_AMBIGUOUS; the schema UNIQUE
// makes it a corruption backstop, never a silent pick). A disabled policy, a
// version drift or an unsourced row never resolves.
func ResolveRetentionPolicy(policies []RetentionPolicy, scope Scope, jurisdiction, legislation, category string) (RetentionPolicy, bool) {
	var best RetentionPolicy
	bestVersion := int64(0)
	matches := 0
	for _, p := range policies {
		if !p.Enabled {
			continue
		}
		if p.TenantID != scope.OrganizationID || p.CompanyID != scope.CompanyID || p.RUC != scope.RUC || p.Period != scope.Period {
			continue
		}
		if p.Jurisdiction != jurisdiction || p.Legislation != legislation || p.Category != category {
			continue
		}
		if p.Version < 1 {
			continue // unsourced/unversioned row — never resolves
		}
		if p.Version > bestVersion {
			best = p
			bestVersion = p.Version
			matches = 1
		} else if p.Version == bestVersion {
			matches++ // ambiguity — fail closed at the store layer
		}
	}
	if matches != 1 {
		return RetentionPolicy{}, false
	}
	return best, true
}

// canonicalRetentionPolicy is the canonical JSON shape of a RetentionPolicy:
// the struct field order IS the property order and property names use the
// wire names, so the canonical bytes are the wire representation.
type canonicalRetentionPolicy RetentionPolicy

// CanonicalRetentionPolicyJSON returns the canonical compact UTF-8 JSON bytes
// of a RetentionPolicy: FIXED property order (exactly the struct order
// above), JSON string escaping, NO HTML escaping (matching the receipt
// canonicalizers — Go escapes <,>,& by default, disabling it keeps Go and
// TypeScript bytes identical). These are the bytes the Go↔TS parity fixture
// pins and the bytes an audit can re-canonicalize from any transport.
// Marshaling cannot fail for validated policies (fixed value shapes); a
// failure is an internal invariant violation and fails closed via panic.
func CanonicalRetentionPolicyJSON(p RetentionPolicy) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(canonicalRetentionPolicy(p)); err != nil {
		panic(fmt.Sprintf("canonicalize retention policy: %v", err))
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}
