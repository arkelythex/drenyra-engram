// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test runs the SHARED golden vectors
// (testdata/golden/*.json) against the Go implementation. The SAME files run
// from TypeScript (core/__tests__/golden.test.ts), so Go and TS must agree on
// the canonical hashes, the approval gate and the initial status. The expected
// hashes are FIXED values — a divergence between runtimes fails one of the two
// runners, never silently.
//
// v0.4.0: each vector carries an optional `contract` discriminator. Legacy
// vectors (no field, or "legacy-hash") keep the historical hash/gate
// assertions; the new contracts run the PURE approval policy
// ("approval-policy"), the reviewed/resulting envelope pair ("approval-envelope",
// plus the post-review stale-envelope proof via linkedAfterReviewRefs), and the
// canonical role order of a principal snapshot ("principal-snapshot").
package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// goldenCase is the shared vector shape (testdata/golden/*.json).
type goldenCase struct {
	Name        string         `json:"name"`
	Contract    string         `json:"contract"`
	Description string         `json:"description"`
	Input       goldenInput    `json:"input"`
	Expected    goldenExpected `json:"expected"`
	// Principal and Memory carry the v0.4.0 approval vectors (contracts
	// "approval-policy", "approval-envelope" and "principal-snapshot").
	Principal *goldenPrincipal `json:"principal,omitempty"`
	Memory    *goldenMemory    `json:"memory,omitempty"`
	// LinkedAfterReviewRefs are evidence refs added AFTER the review that
	// produced reviewedEnvelopeHash — a stale-envelope proof: the actual
	// envelope must differ from the hash the reviewer saw.
	LinkedAfterReviewRefs []string `json:"linkedAfterReviewRefs,omitempty"`

	// v0.4.0 judgment vectors (contract "judgment"). Resolution/DecidedAt are
	// the CONFIRMATION facts the harness uses to derive the confirmed state
	// (status=confirmed + resolution + canonical adjudicator snapshot of the
	// vector's principal + frozen policy version + decidedAt); SupersededAt is
	// the moment the correction confirms and routes the predecessor.
	Judgment     *goldenJudgment `json:"judgment,omitempty"`
	Predecessor  *goldenJudgment `json:"predecessor,omitempty"`
	Resolution   string          `json:"resolution,omitempty"`
	DecidedAt    string          `json:"decidedAt,omitempty"`
	SupersededAt string          `json:"supersededAt,omitempty"`
}

type goldenInput struct {
	ID           string            `json:"id"`
	TopicKey     string            `json:"topicKey"`
	Title        string            `json:"title"`
	Kind         core.MemoryKind   `json:"kind"`
	Scope        core.Scope        `json:"scope"`
	Content      core.Content      `json:"content"`
	FiscalEffect core.FiscalEffect `json:"fiscalEffect"`
	EffectiveAt  string            `json:"effectiveAt"`
	RecordedAt   string            `json:"recordedAt"`
	ObservedAt   string            `json:"observedAt"`
	Source       core.Source       `json:"source"`
	EvidenceRefs []string          `json:"evidenceRefs"`
	RuleRefs     []string          `json:"ruleRefs"`
	Confidence   *float64          `json:"confidence"`
	Materiality  *int64            `json:"materiality"`
	ReceiptID    string            `json:"receiptId"`
	SupersedesID string            `json:"supersedesId"`
	Status       core.MemoryStatus `json:"status"`
}

// goldenPrincipal is the FIXED pre-verified principal data of a v0.4.0 vector.
// It is never derived from the memory: the harness mints it through the real
// factory path (auth.Resolver.Authenticate) with a minimal fake session store,
// preserving the no-public-arbitrary-input-constructor invariant (ADR-003).
type goldenPrincipal struct {
	SubjectID            string                    `json:"subjectId"`
	TenantID             string                    `json:"tenantId"`
	MembershipID         string                    `json:"membershipId"`
	CompanyScopes        []string                  `json:"companyScopes"`
	Roles                []auth.AccountingRole     `json:"roles"`
	AuthenticationMethod auth.AuthenticationMethod `json:"authenticationMethod"`
	AssuranceLevel       auth.AssuranceLevel       `json:"assuranceLevel"`
	AuthenticatedAt      string                    `json:"authenticatedAt"`
}

// goldenMemory is the canonical memory shape of a v0.4.0 vector. Timestamps are
// fixed RFC3339; content is exactly {what,why,where,learned}; source is
// {system, actorId, actorKind}. The materialityLevel is the DECLARED level
// (normal|material|critical; NULL = normal) and does NOT participate in the
// envelope hash (frozen decision).
type goldenMemory struct {
	ID               string                 `json:"id"`
	TopicKey         string                 `json:"topicKey"`
	Title            string                 `json:"title"`
	Kind             core.MemoryKind        `json:"kind"`
	Scope            core.Scope             `json:"scope"`
	Content          core.Content           `json:"content"`
	Source           core.Source            `json:"source"`
	FiscalEffect     core.FiscalEffect      `json:"fiscalEffect"`
	Status           core.MemoryStatus      `json:"status"`
	EffectiveAt      string                 `json:"effectiveAt"`
	RecordedAt       string                 `json:"recordedAt"`
	EvidenceRefs     []string               `json:"evidenceRefs"`
	RuleRefs         []string               `json:"ruleRefs"`
	MaterialityLevel *core.MaterialityLevel `json:"materialityLevel"`
}

// memory builds the canonical AccountingMemory of the vector: it fills the
// content hash (the canonicalized identical shape both runtimes consume) and
// derives the initial status when the vector leaves it empty.
func (g *goldenMemory) memory() core.AccountingMemory {
	m := core.AccountingMemory{
		Identity:         core.Identity{ID: g.ID, TopicKey: g.TopicKey},
		Title:            g.Title,
		Kind:             g.Kind,
		Scope:            g.Scope,
		Content:          g.Content,
		Source:           g.Source,
		FiscalEffect:     g.FiscalEffect,
		EffectiveAt:      g.EffectiveAt,
		RecordedAt:       g.RecordedAt,
		EvidenceRefs:     append([]string(nil), g.EvidenceRefs...),
		RuleRefs:         append([]string(nil), g.RuleRefs...),
		MaterialityLevel: g.MaterialityLevel,
		Status:           g.Status,
	}
	if m.Status == "" {
		m.Status = core.InitialStatus(m.FiscalEffect)
	}
	m.ContentHash = core.ComputeContentHash(m)
	return m
}

type goldenExpected struct {
	ContentHash     string `json:"contentHash"`
	IdentityHash    string `json:"identityHash"`
	EnvelopeHash    string `json:"envelopeHash"`
	InitialStatus   string `json:"initialStatus"`
	CanApproveAgent bool   `json:"canApproveAgent"`
	CanApproveHuman bool   `json:"canApproveHuman"`

	// v0.4.0 contracts.
	Allowed               *bool    `json:"allowed"`
	ReasonCode            string   `json:"reasonCode"`
	PolicyVersion         string   `json:"policyVersion"`
	ReviewedEnvelopeHash  string   `json:"reviewedEnvelopeHash"`
	ResultingEnvelopeHash string   `json:"resultingEnvelopeHash"`
	ActualEnvelopeHash    string   `json:"actualEnvelopeHash"`
	CanonicalRoles        []string `json:"canonicalRoles"`

	// v0.4.0 judgment contract (contract "judgment").
	ProposedJudgmentHash              string `json:"proposedJudgmentHash,omitempty"`
	ConfirmedJudgmentHash             string `json:"confirmedJudgmentHash,omitempty"`
	SupersededJudgmentHash            string `json:"supersededJudgmentHash,omitempty"`
	CanPropose                        *bool  `json:"canPropose,omitempty"`
	CanConfirm                        *bool  `json:"canConfirm,omitempty"`
	CanReject                         *bool  `json:"canReject,omitempty"`
	CanWithdraw                       *bool  `json:"canWithdraw,omitempty"`
	CanSupersedeConfirmed             *bool  `json:"canSupersedeConfirmed,omitempty"`
	PredecessorCanSupersedeConfirmed  *bool  `json:"predecessorCanSupersedeConfirmed,omitempty"`
	PredecessorTerminalAfterSupersede *bool  `json:"predecessorTerminalAfterSupersede,omitempty"`
	AgentConfirmErrorCode             string `json:"agentConfirmErrorCode,omitempty"`
	Immutable                         bool   `json:"immutable,omitempty"`
}

// TestGoldenVectorsGo runs every shared vector against the Go implementation,
// dispatching by the `contract` discriminator: legacy vectors keep the
// historical hash/gate assertions; the v0.4.0 contracts run the pure approval
// policy, the reviewed/resulting envelope pair and the canonical principal role
// order. The same vector files run from TypeScript and must agree exactly.
func TestGoldenVectorsGo(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "testdata", "golden", "*.json"))
	if err != nil {
		t.Fatalf("glob golden: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no golden vectors found")
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var tc goldenCase
		if err := json.Unmarshal(raw, &tc); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		t.Run(tc.Name, func(t *testing.T) {
			switch tc.Contract {
			case "", "legacy-hash":
				runLegacyGolden(t, tc)
			case "approval-policy":
				runApprovalPolicyGolden(t, tc)
			case "approval-envelope":
				runApprovalEnvelopeGolden(t, tc)
			case "principal-snapshot":
				runPrincipalSnapshotGolden(t, tc)
			case "judgment":
				runJudgmentGolden(t, tc)
			default:
				t.Fatalf("%s: unknown golden contract %q", tc.Name, tc.Contract)
			}
		})
	}
}

// runLegacyGolden keeps the historical assertions for vectors without a
// contract field (or with "legacy-hash"): canonical hashes, initial status and
// the v0.3 approval gate.
func runLegacyGolden(t *testing.T, tc goldenCase) {
	t.Helper()
	memory := core.AccountingMemory{
		Identity:     core.Identity{ID: tc.Input.ID, TopicKey: tc.Input.TopicKey},
		Title:        tc.Input.Title,
		Kind:         tc.Input.Kind,
		Scope:        tc.Input.Scope,
		Content:      tc.Input.Content,
		FiscalEffect: tc.Input.FiscalEffect,
		EffectiveAt:  tc.Input.EffectiveAt,
		RecordedAt:   tc.Input.RecordedAt,
		ObservedAt:   tc.Input.ObservedAt,
		Source:       tc.Input.Source,
		EvidenceRefs: tc.Input.EvidenceRefs,
		RuleRefs:     tc.Input.RuleRefs,
		Confidence:   tc.Input.Confidence,
		Materiality:  tc.Input.Materiality,
		ReceiptID:    tc.Input.ReceiptID,
		SupersedesID: tc.Input.SupersedesID,
		Status:       core.MemoryStatus(tc.Input.Status),
	}
	if memory.Status == "" {
		memory.Status = core.InitialStatus(memory.FiscalEffect)
	}

	contentHash := core.ComputeContentHash(memory)
	identityHash := core.ComputeIdentityHash(memory)
	memory.ContentHash = contentHash
	envelopeHash := core.ComputeEnvelopeHash(memory)

	// initial status from the gate
	if got := string(core.InitialStatus(memory.FiscalEffect)); got != tc.Expected.InitialStatus {
		t.Errorf("%s: initialStatus = %s, want %s", tc.Name, got, tc.Expected.InitialStatus)
	}

	// approval gate: agents never approve; humans approve pending_review
	agentApproves := core.Approve(&memory, core.Source{System: "golden", ActorID: "a", ActorKind: core.ActorKindAgent}) == nil
	if agentApproves != tc.Expected.CanApproveAgent {
		t.Errorf("%s: canApproveAgent = %v, want %v", tc.Name, agentApproves, tc.Expected.CanApproveAgent)
	}
	humanApproves := core.Approve(&memory, core.Source{System: "golden", ActorID: "h", ActorKind: core.ActorKindHuman}) == nil
	if humanApproves != tc.Expected.CanApproveHuman {
		t.Errorf("%s: canApproveHuman = %v, want %v", tc.Name, humanApproves, tc.Expected.CanApproveHuman)
	}

	// fixed hashes: same value in Go and TS (the shared contract)
	if tc.Expected.ContentHash != "" && contentHash != tc.Expected.ContentHash {
		t.Errorf("%s: contentHash = %s, want %s", tc.Name, contentHash, tc.Expected.ContentHash)
	}
	if tc.Expected.IdentityHash != "" && identityHash != tc.Expected.IdentityHash {
		t.Errorf("%s: identityHash = %s, want %s", tc.Name, identityHash, tc.Expected.IdentityHash)
	}
	if tc.Expected.EnvelopeHash != "" && envelopeHash != tc.Expected.EnvelopeHash {
		t.Errorf("%s: envelopeHash = %s, want %s", tc.Name, envelopeHash, tc.Expected.EnvelopeHash)
	}

	// print the computed hashes so the golden files can be pinned
	t.Logf("HASHES %s content=%s identity=%s envelope=%s", tc.Name, contentHash, identityHash, envelopeHash)
}

// goldenSessionStore is a minimal fake SessionStore that returns the vector's
// fixed principal fields. It exists ONLY to route the vector data through the
// real factory path (auth.Resolver.Authenticate) — the same path production
// middleware and the authz unit tests use. No public principal constructor is
// added (ADR-003 invariant).
type goldenSessionStore struct {
	principal goldenPrincipal
}

func (g *goldenSessionStore) LookupByTokenHash(context.Context, string) (auth.StoredSession, error) {
	return auth.StoredSession{
		ID:                   "golden-session",
		MembershipID:         g.principal.MembershipID,
		AuthenticationMethod: g.principal.AuthenticationMethod,
		AssuranceLevel:       g.principal.AssuranceLevel,
		AuthenticatedAt:      g.principal.AuthenticatedAt,
		ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}, nil
}

func (g *goldenSessionStore) LoadMembership(context.Context, string) (auth.MembershipRecord, error) {
	// Step 1 derives a single company scope from the active membership; the
	// vectors therefore carry exactly one companyScopes entry.
	company := ""
	if len(g.principal.CompanyScopes) > 0 {
		company = g.principal.CompanyScopes[0]
	}
	return auth.MembershipRecord{
		ID:            g.principal.MembershipID,
		SubjectID:     g.principal.SubjectID,
		TenantID:      g.principal.TenantID,
		CompanyID:     company,
		Status:        "active",
		Roles:         g.principal.Roles,
		CompanyActive: true,
	}, nil
}

// mintGoldenPrincipal derives the vector's principal through the resolver — the
// ONLY factory path (ADR-003).
func mintGoldenPrincipal(p goldenPrincipal) (auth.VerifiedApprovalPrincipal, error) {
	resolver := &auth.Resolver{
		Sessions: &goldenSessionStore{principal: p},
		Mode:     auth.RuntimeProduction,
	}
	return resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: "golden-fixture-token",
	})
}

// runApprovalPolicyGolden runs the PURE v0.4.0 policy (authz.Authorize) on the
// vector's fixed principal and memory and pins allowed/reasonCode/policyVersion
// — the same vector file must yield the identical triple from TypeScript.
func runApprovalPolicyGolden(t *testing.T, tc goldenCase) {
	t.Helper()
	if tc.Principal == nil || tc.Memory == nil {
		t.Fatalf("%s: approval-policy vector requires principal and memory", tc.Name)
	}
	principal, err := mintGoldenPrincipal(*tc.Principal)
	if err != nil {
		t.Fatalf("%s: mint golden principal: %v", tc.Name, err)
	}
	decision := authz.NewApprovalPolicy().Authorize(principal, tc.Memory.memory())
	if tc.Expected.Allowed != nil && decision.Allowed != *tc.Expected.Allowed {
		t.Errorf("%s: allowed = %v, want %v", tc.Name, decision.Allowed, *tc.Expected.Allowed)
	}
	if tc.Expected.ReasonCode != "" && decision.ReasonCode != tc.Expected.ReasonCode {
		t.Errorf("%s: reasonCode = %q, want %q", tc.Name, decision.ReasonCode, tc.Expected.ReasonCode)
	}
	if tc.Expected.PolicyVersion != "" && decision.PolicyVersion != tc.Expected.PolicyVersion {
		t.Errorf("%s: policyVersion = %q, want %q", tc.Name, decision.PolicyVersion, tc.Expected.PolicyVersion)
	}
	t.Logf("DECISION %s allowed=%v reasonCode=%s policyVersion=%s", tc.Name, decision.Allowed, decision.ReasonCode, decision.PolicyVersion)
}

// runApprovalEnvelopeGolden computes H1 from the input memory (status
// pending_review) and H2 from the same memory with status approved; the hashes
// must be byte-identical in Go and TS and must differ (status participates in
// the envelope). When the vector carries linkedAfterReviewRefs, the harness
// additionally computes the ACTUAL envelope after the post-review link and
// proves it differs from the stale reviewed hash (a reviewer presenting the old
// H1 would fail ENVELOPE_MISMATCH).
func runApprovalEnvelopeGolden(t *testing.T, tc goldenCase) {
	t.Helper()
	if tc.Memory == nil {
		t.Fatalf("%s: approval-envelope vector requires memory", tc.Name)
	}
	m := tc.Memory.memory()
	reviewed := core.ComputeEnvelopeHash(m)
	if tc.Expected.ReviewedEnvelopeHash != "" && reviewed != tc.Expected.ReviewedEnvelopeHash {
		t.Errorf("%s: reviewedEnvelopeHash = %s, want %s", tc.Name, reviewed, tc.Expected.ReviewedEnvelopeHash)
	}

	approved := m
	approved.Status = core.StatusApproved
	resulting := core.ComputeEnvelopeHash(approved)
	if tc.Expected.ResultingEnvelopeHash != "" && resulting != tc.Expected.ResultingEnvelopeHash {
		t.Errorf("%s: resultingEnvelopeHash = %s, want %s", tc.Name, resulting, tc.Expected.ResultingEnvelopeHash)
	}
	if reviewed == resulting {
		t.Errorf("%s: reviewed and resulting envelope hashes must differ (status participates)", tc.Name)
	}

	actual := ""
	if len(tc.LinkedAfterReviewRefs) > 0 {
		linked := m
		linked.EvidenceRefs = append(append([]string(nil), m.EvidenceRefs...), tc.LinkedAfterReviewRefs...)
		actual = core.ComputeEnvelopeHash(linked)
		if tc.Expected.ActualEnvelopeHash != "" && actual != tc.Expected.ActualEnvelopeHash {
			t.Errorf("%s: actualEnvelopeHash = %s, want %s", tc.Name, actual, tc.Expected.ActualEnvelopeHash)
		}
		if actual == reviewed {
			t.Errorf("%s: a post-review evidence link must change the envelope (stale review proof)", tc.Name)
		}
	}
	t.Logf("ENVELOPE %s reviewed=%s resulting=%s actual=%s", tc.Name, reviewed, resulting, actual)
}

// runPrincipalSnapshotGolden pins the canonical (sorted, deduplicated) role
// order of the vector's principal — Go and TS must produce the same bytes.
func runPrincipalSnapshotGolden(t *testing.T, tc goldenCase) {
	t.Helper()
	if tc.Principal == nil {
		t.Fatalf("%s: principal-snapshot vector requires principal", tc.Name)
	}
	principal, err := mintGoldenPrincipal(*tc.Principal)
	if err != nil {
		t.Fatalf("%s: mint golden principal: %v", tc.Name, err)
	}
	got := principal.PrincipalSnapshot().Roles
	want := tc.Expected.CanonicalRoles
	if len(got) != len(want) {
		t.Errorf("%s: canonicalRoles = %v, want %v", tc.Name, got, want)
		return
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Errorf("%s: canonicalRoles = %v, want %v", tc.Name, got, want)
			break
		}
	}
	t.Logf("SNAPSHOT %s canonicalRoles=%v", tc.Name, got)
}

// ──────────────────────────────────────────────
// v0.4.0 Step 2 — judgment contract ("judgment")
// ──────────────────────────────────────────────

// goldenJudgment is the canonical judgment shape of a v0.4.0 vector: the record
// fields BOTH runtimes consume. Status tells which state's lifecycle predicates
// the vector asserts; the confirmed state is NEVER read from the JSON — the
// harness DERIVES it (status=confirmed + the vector's resolution + the canonical
// snapshot of the vector's authorizing principal + the frozen policy version +
// the vector's decidedAt), because the adjudicator snapshot must come from the
// verified-principal factory path (ADR-003), never from caller-declared JSON.
// The confirmed hash therefore pins the same bytes in Go and TS by construction.
type goldenJudgment struct {
	ID             string              `json:"id"`
	TenantID       string              `json:"tenantId"`
	CompanyID      string              `json:"companyId"`
	FiscalPeriodID string              `json:"fiscalPeriodId,omitempty"`
	FromID         string              `json:"fromId"`
	ToID           string              `json:"toId"`
	Relation       core.Relation       `json:"relation"`
	Status         core.JudgmentStatus `json:"status"`
	Proposer       core.Source         `json:"proposer"`
	ProposalReason string              `json:"proposalReason"`
	PredecessorID  string              `json:"predecessorId,omitempty"`
	ProposedAt     string              `json:"proposedAt"`
	UpdatedAt      string              `json:"updatedAt"`
}

// judgment builds the raw AccountingJudgment of the vector (the JSON state).
func (g *goldenJudgment) judgment() core.AccountingJudgment {
	return core.AccountingJudgment{
		ID:             g.ID,
		TenantID:       g.TenantID,
		CompanyID:      g.CompanyID,
		FiscalPeriodID: g.FiscalPeriodID,
		FromID:         g.FromID,
		ToID:           g.ToID,
		Relation:       g.Relation,
		Status:         g.Status,
		Proposer:       g.Proposer,
		ProposalReason: g.ProposalReason,
		PredecessorID:  g.PredecessorID,
		ProposedAt:     g.ProposedAt,
		UpdatedAt:      g.UpdatedAt,
	}
}

// reviewedState is the REVIEWED shape the proposed hash covers: base fields with
// status forced to proposed and the decided fields cleared — the same shape a
// confirm/reject compares ExpectedJudgmentHash against before deciding (design
// §2). Routing (supersedesId) and updatedAt never participate either.
func (g *goldenJudgment) reviewedState() core.AccountingJudgment {
	j := g.judgment()
	j.Status = core.JudgmentProposed
	j.Resolution = ""
	j.DecidedAt = ""
	return j
}

// confirmedState derives the CONFIRMED state of the vector: status=confirmed,
// the vector's resolution, the canonical adjudicator snapshot of the authorizing
// principal, the frozen judgment policy version and the vector's decidedAt
// (design §4/§6). updatedAt is set to decidedAt exactly as the pure transition
// does; it never participates in the hash.
func (g *goldenJudgment) confirmedState(snapshot *auth.PrincipalSnapshot, resolution, decidedAt string) core.AccountingJudgment {
	j := g.judgment()
	j.Status = core.JudgmentConfirmed
	j.Resolution = resolution
	j.Adjudicator = snapshot
	j.PolicyVersion = authz.JudgmentPolicyVersion
	j.DecidedAt = decidedAt
	j.UpdatedAt = decidedAt
	return j
}

// assertBool asserts an optional expected boolean when the vector pins one.
func assertBool(t *testing.T, name, field string, got bool, want *bool) {
	t.Helper()
	if want != nil && got != *want {
		t.Errorf("%s: %s = %v, want %v", name, field, got, *want)
	}
}

// runJudgmentGolden runs the PURE v0.4.0 judgment contract on a vector: the
// canonical reviewed/proposed hash, the DERIVED confirmed hash, the lifecycle
// predicates against the vector's recorded state, the judgment policy decision
// for the vector's principal (if present) and — for the agent vector — the
// AUTHENTICATION_REQUIRED proof that an agent Source carries no adjudication
// authority. The SAME vector file must yield the identical hashes, predicates
// and policy outcomes from TypeScript (core/__tests__/golden.test.ts).
func runJudgmentGolden(t *testing.T, tc goldenCase) {
	t.Helper()
	if tc.Judgment == nil {
		t.Fatalf("%s: judgment vector requires judgment", tc.Name)
	}
	raw := tc.Judgment.judgment()

	// Canonical reviewed/proposed hash (shared with every sibling vector that
	// describes the same proposed judgment).
	proposedHash := core.ComputeJudgmentHash(tc.Judgment.reviewedState())
	if tc.Expected.ProposedJudgmentHash != "" && proposedHash != tc.Expected.ProposedJudgmentHash {
		t.Errorf("%s: proposedJudgmentHash = %s, want %s", tc.Name, proposedHash, tc.Expected.ProposedJudgmentHash)
	}

	// Lifecycle predicates against the vector's recorded state. canPropose takes
	// the proposer SOURCE (agents/systems propose; provenance never authorizes).
	assertBool(t, tc.Name, "canPropose", core.CanPropose(raw.Proposer), tc.Expected.CanPropose)
	assertBool(t, tc.Name, "canConfirm", core.CanConfirm(raw.Status), tc.Expected.CanConfirm)
	assertBool(t, tc.Name, "canReject", core.CanRejectJudgment(raw.Status), tc.Expected.CanReject)
	assertBool(t, tc.Name, "canWithdraw", core.CanWithdraw(raw.Status), tc.Expected.CanWithdraw)
	assertBool(t, tc.Name, "canSupersedeConfirmed", core.CanSupersedeConfirmed(raw.Status), tc.Expected.CanSupersedeConfirmed)

	confirmedHash := ""
	if tc.Principal != nil {
		principal, err := mintGoldenPrincipal(*tc.Principal)
		if err != nil {
			t.Fatalf("%s: mint golden principal: %v", tc.Name, err)
		}

		// Derive the confirmed state when the vector supplies the confirmation
		// facts; the adjudicator snapshot is the canonical view of the SAME
		// principal that authorized the decision. For a correction vector the
		// expected confirmedJudgmentHash belongs to the PREDECESSOR (the confirmed
		// record being corrected) and is asserted in the predecessor block below.
		if tc.Resolution != "" && tc.DecidedAt != "" && tc.Predecessor == nil {
			snapshot := principal.PrincipalSnapshot()
			confirmed := tc.Judgment.confirmedState(&snapshot, tc.Resolution, tc.DecidedAt)
			confirmedHash = core.ComputeJudgmentHash(confirmed)
			if tc.Expected.ConfirmedJudgmentHash != "" && confirmedHash != tc.Expected.ConfirmedJudgmentHash {
				t.Errorf("%s: confirmedJudgmentHash = %s, want %s", tc.Name, confirmedHash, tc.Expected.ConfirmedJudgmentHash)
			}
		}

		// Frozen judgment policy on the vector's recorded judgment.
		decision := authz.NewJudgmentPolicy().Authorize(principal, raw)
		if tc.Expected.Allowed != nil && decision.Allowed != *tc.Expected.Allowed {
			t.Errorf("%s: allowed = %v, want %v", tc.Name, decision.Allowed, *tc.Expected.Allowed)
		}
		if tc.Expected.ReasonCode != "" && decision.ReasonCode != tc.Expected.ReasonCode {
			t.Errorf("%s: reasonCode = %q, want %q", tc.Name, decision.ReasonCode, tc.Expected.ReasonCode)
		}
		if tc.Expected.PolicyVersion != "" && decision.PolicyVersion != tc.Expected.PolicyVersion {
			t.Errorf("%s: policyVersion = %q, want %q", tc.Name, decision.PolicyVersion, tc.Expected.PolicyVersion)
		}
	}

	// Agent-confirm proof: an agent-shaped confirm (no verified principal) fails
	// closed with AUTHENTICATION_REQUIRED before any mutation, and the vector can
	// never even carry a principal — the policy decision is impossible by
	// construction.
	if tc.Expected.AgentConfirmErrorCode != "" {
		if tc.Resolution == "" {
			t.Fatalf("%s: agentConfirmErrorCode requires a resolution", tc.Name)
		}
		if tc.Principal != nil {
			t.Errorf("%s: an agent proposer must not carry a principal (impossible by construction)", tc.Name)
		}
		attempt := raw
		err := core.ConfirmJudgment(&attempt, tc.Resolution, nil, authz.JudgmentPolicyVersion, tc.DecidedAt)
		if got := auth.Code(err); got != tc.Expected.AgentConfirmErrorCode {
			t.Errorf("%s: agentConfirmErrorCode = %q, want %q", tc.Name, got, tc.Expected.AgentConfirmErrorCode)
		}
		if attempt.Status != raw.Status {
			t.Errorf("%s: failed agent confirm mutated status to %q", tc.Name, attempt.Status)
		}
	}

	// Predecessor supersession (correction vector): the confirmed predecessor can
	// be superseded ONLY by the confirming correction, and once superseded it is
	// terminal. The superseded hash is the reviewed shape with status=superseded
	// (decided fields never participate; routing/updatedAt neither).
	supersededHash := ""
	if tc.Predecessor != nil {
		if tc.Principal == nil || tc.Resolution == "" || tc.DecidedAt == "" || tc.SupersededAt == "" {
			t.Fatalf("%s: predecessor supersession requires principal, resolution, decidedAt and supersededAt", tc.Name)
		}
		principal, err := mintGoldenPrincipal(*tc.Principal)
		if err != nil {
			t.Fatalf("%s: mint golden principal: %v", tc.Name, err)
		}
		snapshot := principal.PrincipalSnapshot()
		predConfirmed := tc.Predecessor.confirmedState(&snapshot, tc.Resolution, tc.DecidedAt)
		predConfirmedHash := core.ComputeJudgmentHash(predConfirmed)
		if tc.Expected.ConfirmedJudgmentHash != "" && predConfirmedHash != tc.Expected.ConfirmedJudgmentHash {
			t.Errorf("%s: confirmedJudgmentHash = %s, want %s", tc.Name, predConfirmedHash, tc.Expected.ConfirmedJudgmentHash)
		}
		// The PREDECESSOR's confirmed hash is the one the vector pins as
		// expected.confirmedJudgmentHash (the confirmed record being corrected).
		confirmedHash = predConfirmedHash
		assertBool(t, tc.Name, "predecessorCanSupersedeConfirmed", core.CanSupersedeConfirmed(predConfirmed.Status), tc.Expected.PredecessorCanSupersedeConfirmed)
		// A confirmed record is terminal except for the supersede route.
		if core.CanConfirm(predConfirmed.Status) || core.CanRejectJudgment(predConfirmed.Status) || core.CanWithdraw(predConfirmed.Status) {
			t.Errorf("%s: confirmed predecessor must be terminal except for supersession", tc.Name)
		}

		predSuperseded := predConfirmed
		if err := core.SupersedeJudgment(&predSuperseded, tc.Judgment.ID, tc.SupersededAt); err != nil {
			t.Fatalf("%s: supersede predecessor: %v", tc.Name, err)
		}
		supersededHash = core.ComputeJudgmentHash(predSuperseded)
		if tc.Expected.SupersededJudgmentHash != "" && supersededHash != tc.Expected.SupersededJudgmentHash {
			t.Errorf("%s: supersededJudgmentHash = %s, want %s", tc.Name, supersededHash, tc.Expected.SupersededJudgmentHash)
		}
		supersededOK := predSuperseded.Status == core.JudgmentSuperseded &&
			!core.CanConfirm(predSuperseded.Status) &&
			!core.CanRejectJudgment(predSuperseded.Status) &&
			!core.CanWithdraw(predSuperseded.Status)
		assertBool(t, tc.Name, "predecessorTerminalAfterSupersede", supersededOK, tc.Expected.PredecessorTerminalAfterSupersede)
	}

	// Immutability proof: the confirmed hash is STABLE (recomputing from the same
	// fields yields the same hash) and editing ANY adjudication field (resolution,
	// decidedAt) yields a DIFFERENT hash — the record the hash protects cannot
	// change silently.
	if tc.Expected.Immutable {
		if tc.Principal == nil || tc.Resolution == "" || tc.DecidedAt == "" {
			t.Fatalf("%s: immutable proof requires principal, resolution and decidedAt", tc.Name)
		}
		principal, err := mintGoldenPrincipal(*tc.Principal)
		if err != nil {
			t.Fatalf("%s: mint golden principal: %v", tc.Name, err)
		}
		snapshot := principal.PrincipalSnapshot()
		confirmed := tc.Judgment.confirmedState(&snapshot, tc.Resolution, tc.DecidedAt)
		a := core.ComputeJudgmentHash(confirmed)
		b := core.ComputeJudgmentHash(confirmed)
		if a != b {
			t.Errorf("%s: recomputing the confirmed hash must be deterministic", tc.Name)
		}
		if tc.Expected.ConfirmedJudgmentHash != "" && a != tc.Expected.ConfirmedJudgmentHash {
			t.Errorf("%s: confirmedJudgmentHash = %s, want %s", tc.Name, a, tc.Expected.ConfirmedJudgmentHash)
		}
		mutatedResolution := confirmed
		mutatedResolution.Resolution = "a different professional resolution"
		if core.ComputeJudgmentHash(mutatedResolution) == a {
			t.Errorf("%s: editing resolution must change the confirmed hash", tc.Name)
		}
		mutatedDecidedAt := confirmed
		mutatedDecidedAt.DecidedAt = "2026-08-05T15:00:00Z"
		if core.ComputeJudgmentHash(mutatedDecidedAt) == a {
			t.Errorf("%s: editing decidedAt must change the confirmed hash", tc.Name)
		}
	}

	t.Logf("JUDGMENT %s proposed=%s confirmed=%s superseded=%s", tc.Name, proposedHash, confirmedHash, supersededHash)
}
