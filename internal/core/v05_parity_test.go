// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test freezes the v0.5.0 Go↔TS parity
// fixture (testdata/v05-parity.json) against the Go implementations
// (docs/architecture/close-intelligence-v0.5.md §8 — five shared vectors):
//
//  1. approved close + memory_closed/memory_reopened receipt signatures;
//  2. blocked write (PERIOD_CLOSED) with no partial mutation;
//  3. reconciliation proposed→confirmed, projected edge, and receipt;
//  4. period delta with new/removed/changed chains and pending delta;
//  5. initialize context for configured/missing/invalid scopes.
//
// The SAME fixture runs from TypeScript (core/__tests__/v05-parity.test.ts):
// every value here is pinned Go-computed, and a divergence between runtimes
// fails one of the two runners, never silently (design §8, AC12).
package core_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ──────────────────────────────────────────────
// Fixture wire types (shared shape with core/__tests__/v05-parity.test.ts)
// ──────────────────────────────────────────────

type v05parityScope struct {
	Kind           string `json:"kind"`
	OrganizationID string `json:"organizationId"`
	CompanyID      string `json:"companyId"`
	RUC            string `json:"ruc"`
	Period         string `json:"period"`
}

type v05parityContent struct {
	What    string `json:"what"`
	Why     string `json:"why"`
	Where   string `json:"where"`
	Learned string `json:"learned"`
}

type v05paritySource struct {
	System    string `json:"system"`
	ActorKind string `json:"actorKind"`
}

type v05parityMemory struct {
	ID           string           `json:"id"`
	TopicKey     string           `json:"topicKey"`
	Kind         string           `json:"kind"`
	Status       string           `json:"status"`
	Title        string           `json:"title"`
	Scope        v05parityScope   `json:"scope"`
	Content      v05parityContent `json:"content"`
	FiscalEffect string           `json:"fiscalEffect"`
	EffectiveAt  string           `json:"effectiveAt"`
	Source       v05paritySource  `json:"source"`
	RecordedAt   string           `json:"recordedAt"`
	Revision     int              `json:"revision"`
	SupersedesID string           `json:"supersedesId"`
	EvidenceRefs []string         `json:"evidenceRefs"`
	RuleRefs     []string         `json:"ruleRefs"`
}

func (m v05parityMemory) accountingMemory() core.AccountingMemory {
	mem := core.AccountingMemory{
		Identity:     core.Identity{ID: m.ID, TopicKey: m.TopicKey},
		Title:        m.Title,
		Kind:         core.MemoryKind(m.Kind),
		Scope:        core.Scope{Kind: core.ScopeKindCompany, OrganizationID: m.Scope.OrganizationID, CompanyID: m.Scope.CompanyID, RUC: m.Scope.RUC, Period: m.Scope.Period},
		Content:      core.Content{What: m.Content.What, Why: m.Content.Why, Where: m.Content.Where, Learned: m.Content.Learned},
		Status:       core.MemoryStatus(m.Status),
		FiscalEffect: core.FiscalEffect(m.FiscalEffect),
		EffectiveAt:  m.EffectiveAt,
		RecordedAt:   m.RecordedAt,
		Source:       core.Source{System: m.Source.System, ActorKind: core.ActorKind(m.Source.ActorKind)},
		SupersedesID: m.SupersedesID,
		EvidenceRefs: append([]string(nil), m.EvidenceRefs...),
		RuleRefs:     append([]string(nil), m.RuleRefs...),
		Revision:     m.Revision,
	}
	mem.ContentHash = core.ComputeContentHash(mem)
	return mem
}

type v05parityPending struct {
	MemoryID string `json:"memoryId"`
	TopicKey string `json:"topicKey"`
}

type v05parityComparisonInputs struct {
	FromPeriod     string             `json:"fromPeriod"`
	ToPeriod       string             `json:"toPeriod"`
	From           []v05parityMemory  `json:"from"`
	To             []v05parityMemory  `json:"to"`
	FromPending    []v05parityPending `json:"fromPending"`
	ToPending      []v05parityPending `json:"toPending"`
	FromCloseState string             `json:"fromCloseState"`
	ToCloseState   string             `json:"toCloseState"`
}

type v05parityBlocked struct {
	Scenario struct {
		Operation     string `json:"operation"`
		TenantID      string `json:"tenantId"`
		CompanyID     string `json:"companyId"`
		FiscalPeriod  string `json:"fiscalPeriodId"`
		RUC           string `json:"ruc"`
		ClosureState  string `json:"closureState"`
		CloseMemoryID string `json:"closeMemoryId"`
	} `json:"scenario"`
	Expected struct {
		ErrorCode       string `json:"errorCode"`
		RowsWritten     int    `json:"rowsWritten"`
		EventsWritten   int    `json:"eventsWritten"`
		ReceiptsWritten int    `json:"receiptsWritten"`
	} `json:"expected"`
}

type v05parityContext struct {
	Configured core.CurrentContext `json:"configured"`
	Missing    struct {
		Outcome     string `json:"outcome"`
		Instruction string `json:"instruction"`
	} `json:"missing"`
	Invalid struct {
		Outcome     string `json:"outcome"`
		FailClosed  bool   `json:"failClosed"`
		PartialData bool   `json:"partialData"`
	} `json:"invalid"`
}

type v05parityFixture struct {
	Name      string `json:"name"`
	Seed      string `json:"seed"`
	PublicKey string `json:"publicKey"`
	Vectors   struct {
		CloseReceipts struct {
			SubjectID string                `json:"subjectId"`
			Receipts  []core.SignedReceipt  `json:"receipts"`
			Payloads  []core.ReceiptPayload `json:"payloads"`
			Expected  struct {
				ReceiptHashes   []string `json:"receiptHashes"`
				Actions         []string `json:"actions"`
				PayloadVersions []string `json:"payloadVersions"`
			} `json:"expected"`
		} `json:"close_receipts"`
		BlockedWrite v05parityBlocked `json:"blocked_write"`
		Reconciliation struct {
			Proposed  core.Reconciliation  `json:"proposed"`
			Confirmed core.Reconciliation  `json:"confirmed"`
			Receipt   core.SignedReceipt   `json:"receipt"`
			Payload   core.ReceiptPayload  `json:"payload"`
			Expected  struct {
				ProposedHash      string `json:"proposedHash"`
				ConfirmedHash     string `json:"confirmedHash"`
				ProjectedRelation struct {
					FromID   string `json:"fromId"`
					ToID     string `json:"toId"`
					Relation string `json:"relation"`
				} `json:"projectedRelation"`
			} `json:"expected"`
		} `json:"reconciliation"`
		PeriodComparison struct {
			Inputs   v05parityComparisonInputs `json:"inputs"`
			Expected core.PeriodComparison     `json:"expected"`
		} `json:"period_comparison"`
		CurrentContext v05parityContext `json:"current_context"`
	} `json:"vectors"`
}

func loadV05ParityFixture(t *testing.T) v05parityFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "v05-parity.json"))
	if err != nil {
		t.Fatalf("read parity fixture: %v", err)
	}
	var f v05parityFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode parity fixture: %v", err)
	}
	if f.Seed != paritySeedHex {
		t.Fatalf("fixture seed = %q, want the fixed parity seed", f.Seed)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(f.PublicKey)
	if err != nil {
		t.Fatalf("fixture publicKey is not padded base64: %v", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		t.Fatalf("fixture publicKey length %d — expected %d", len(pubBytes), ed25519.PublicKeySize)
	}
	// The pinned seed must derive the SAME public key (RFC 8032 parity).
	seed, err := hexDecode(paritySeedHex)
	if err != nil {
		t.Fatalf("decode parity seed: %v", err)
	}
	if derived := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey); !reflect.DeepEqual(derived, ed25519.PublicKey(pubBytes)) {
		t.Fatalf("fixture publicKey does not derive from the pinned seed")
	}
	return f
}

// ──────────────────────────────────────────────
// Vector 1 — approved close + memory_closed/memory_reopened signatures
// ──────────────────────────────────────────────

// TestV05ParityVector1CloseReceipts verifies the three chained close receipts
// against the pinned seed: closed enums, canonical payload hash, key id and
// Ed25519 signature all recompute to the fixture's expected values; the chain
// links (approved → closed → reopened) hold.
func TestV05ParityVector1CloseReceipts(t *testing.T) {
	f := loadV05ParityFixture(t)
	v := f.Vectors.CloseReceipts
	if len(v.Receipts) != 3 || len(v.Payloads) != 3 {
		t.Fatalf("fixture must carry a 3-receipt close chain, got %d receipts / %d payloads", len(v.Receipts), len(v.Payloads))
	}
	pub := ed25519.PublicKey(mustDecodeB64(t, f.PublicKey))
	wantActions := []string{string(core.ReceiptActionMemoryApproved), string(core.ReceiptActionMemoryClosed), string(core.ReceiptActionMemoryReopened)}
	if !reflect.DeepEqual(v.Expected.Actions, wantActions) {
		t.Fatalf("expected actions = %v, want %v", v.Expected.Actions, wantActions)
	}
	wantVersions := []string{core.ReceiptPayloadVersion, core.ReceiptPayloadVersionV05, core.ReceiptPayloadVersionV05}
	if !reflect.DeepEqual(v.Expected.PayloadVersions, wantVersions) {
		t.Fatalf("expected payload versions = %v, want %v", v.Expected.PayloadVersions, wantVersions)
	}
	prev := ""
	for i, r := range v.Receipts {
		if r.SubjectType != core.SubjectTypeMemory || r.SubjectID != v.SubjectID {
			t.Fatalf("receipt %d subject = %s %s, want memory %s", i, r.SubjectType, r.SubjectID, v.SubjectID)
		}
		if r.PreviousReceiptHash != prev {
			t.Fatalf("receipt %d previousReceiptHash = %q, want %q (chain)", i, r.PreviousReceiptHash, prev)
		}
		if err := core.VerifyReceipt(r, v.Payloads[i], pub); err != nil {
			t.Fatalf("receipt %d (%s) fails verification: %v", i, r.Action, err)
		}
		recomputed := core.ReceiptHash(r)
		if recomputed != v.Expected.ReceiptHashes[i] {
			t.Fatalf("receipt %d hash = %q, want fixture %q", i, recomputed, v.Expected.ReceiptHashes[i])
		}
		if r.Action != core.ReceiptAction(v.Expected.Actions[i]) {
			t.Fatalf("receipt %d action = %q, want %q", i, r.Action, v.Expected.Actions[i])
		}
		prev = recomputed
	}
}

// ──────────────────────────────────────────────
// Vector 2 — blocked write (PERIOD_CLOSED), no partial mutation
// ──────────────────────────────────────────────

// TestV05ParityVector2BlockedWrite freezes the deterministic gate contract: the
// fixture describes a closed exact company period with an attempted save; the
// EXPECTED outcome is the frozen PERIOD_CLOSED code and zero written rows,
// events and receipts. The full gate behavior (no partial mutation on every
// period-scoped write path) is exercised at the store boundary by
// internal/store/period_closure_test.go; this vector pins the protocol-level
// contract both runtimes share.
func TestV05ParityVector2BlockedWrite(t *testing.T) {
	f := loadV05ParityFixture(t)
	v := f.Vectors.BlockedWrite
	if v.Scenario.Operation != "save" || v.Scenario.ClosureState != "closed" {
		t.Fatalf("scenario must describe a save into a closed period, got %+v", v.Scenario)
	}
	if v.Expected.ErrorCode != string(auth.CodePeriodClosed) {
		t.Fatalf("expected errorCode = %q, want the frozen %q", v.Expected.ErrorCode, auth.CodePeriodClosed)
	}
	if v.Expected.RowsWritten != 0 || v.Expected.EventsWritten != 0 || v.Expected.ReceiptsWritten != 0 {
		t.Fatalf("a blocked write must leave zero partial mutation, got %+v", v.Expected)
	}
	// The gate code is frozen: the same tuple carries the scope + close memory id
	// (never private content) — the fixture names the identical tuple.
	if v.Scenario.CloseMemoryID == "" || v.Scenario.FiscalPeriod == "" {
		t.Fatalf("fixture must carry the scope tuple + close memory id, got %+v", v.Scenario)
	}
}

// ──────────────────────────────────────────────
// Vector 3 — reconciliation proposed→confirmed, edge, receipt
// ──────────────────────────────────────────────

func TestV05ParityVector3Reconciliation(t *testing.T) {
	f := loadV05ParityFixture(t)
	v := f.Vectors.Reconciliation
	pub := ed25519.PublicKey(mustDecodeB64(t, f.PublicKey))

	// The engine recomputes the canonical hashes byte-identically to the fixture.
	if got := core.ComputeReconciliationHash(v.Proposed); got != v.Expected.ProposedHash {
		t.Fatalf("proposed hash = %q, want fixture %q", got, v.Expected.ProposedHash)
	}
	if got := core.ComputeReconciliationHash(v.Confirmed); got != v.Expected.ConfirmedHash {
		t.Fatalf("confirmed hash = %q, want fixture %q", got, v.Expected.ConfirmedHash)
	}
	if v.Expected.ProposedHash == v.Expected.ConfirmedHash {
		t.Fatal("proposed and confirmed hashes must differ")
	}

	// The confirmed entity is a legal confirmed state (adjudicator snapshot set).
	if v.Confirmed.Status != core.ReconciliationConfirmed || v.Confirmed.Adjudicator == nil {
		t.Fatalf("fixture confirmed state mismatch: %+v", v.Confirmed)
	}

	// The receipt covers the reviewed/resulting hashes + both endpoint ids and is
	// verifiable against the pinned seed.
	if v.Receipt.Action != core.ReceiptActionReconciliationConfirmed || v.Receipt.SubjectType != core.SubjectTypeReconciliation {
		t.Fatalf("receipt subject/action = %s %s, want reconciliation reconciliation_confirmed", v.Receipt.SubjectType, v.Receipt.Action)
	}
	if v.Payload.ReviewedJudgmentHash != v.Expected.ProposedHash || v.Payload.ResultingJudgmentHash != v.Expected.ConfirmedHash {
		t.Fatalf("receipt payload must cover the reviewed/resulting reconciliation hashes")
	}
	if v.Payload.Version != core.ReceiptPayloadVersionV05 {
		t.Fatalf("payload version = %q, want %q", v.Payload.Version, core.ReceiptPayloadVersionV05)
	}
	if err := core.VerifyReceipt(v.Receipt, v.Payload, pub); err != nil {
		t.Fatalf("reconciliation receipt fails verification: %v", err)
	}

	// Confirmation projects exactly one observation relation
	// leftMemoryId --reconciles--> rightMemoryId.
	edge := v.Expected.ProjectedRelation
	if edge.Relation != "reconciles" || edge.FromID != v.Confirmed.LeftMemoryID || edge.ToID != v.Confirmed.RightMemoryID {
		t.Fatalf("projected relation = %+v, want leftMemoryId --reconciles--> rightMemoryId", edge)
	}
}

// ──────────────────────────────────────────────
// Vector 4 — period delta (new/removed/changed chains, pending delta)
// ──────────────────────────────────────────────

func TestV05ParityVector4PeriodComparison(t *testing.T) {
	f := loadV05ParityFixture(t)
	v := f.Vectors.PeriodComparison
	inputs := v.Inputs

	fromMems := make([]core.AccountingMemory, 0, len(inputs.From))
	for _, m := range inputs.From {
		fromMems = append(fromMems, m.accountingMemory())
	}
	toMems := make([]core.AccountingMemory, 0, len(inputs.To))
	for _, m := range inputs.To {
		toMems = append(toMems, m.accountingMemory())
	}
	fromPending := make([]core.ClosePendingItem, 0, len(inputs.FromPending))
	for _, p := range inputs.FromPending {
		fromPending = append(fromPending, core.ClosePendingItem{MemoryID: p.MemoryID, TopicKey: p.TopicKey})
	}
	toPending := make([]core.ClosePendingItem, 0, len(inputs.ToPending))
	for _, p := range inputs.ToPending {
		toPending = append(toPending, core.ClosePendingItem{MemoryID: p.MemoryID, TopicKey: p.TopicKey})
	}

	got := core.ComputePeriodComparison(inputs.FromPeriod, inputs.ToPeriod, fromMems, toMems, fromPending, toPending, inputs.FromCloseState, inputs.ToCloseState)

	// Byte-identical JSON (Go marshals maps with sorted keys deterministically).
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal computed comparison: %v", err)
	}
	wantJSON, err := json.Marshal(v.Expected)
	if err != nil {
		t.Fatalf("marshal fixture expected: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("computed comparison differs from the fixture:\n got  %s\nwant %s", gotJSON, wantJSON)
	}

	// The delta shape is the deterministic contract: new/removed/changed chains,
	// unchanged count, status changes, pending delta and the narrative.
	if len(got.Chains.New) != 1 || got.Chains.New[0].TopicKey != "account/4011/ventas-agosto" {
		t.Fatalf("new chains = %+v, want exactly account/4011/ventas-agosto", got.Chains.New)
	}
	if len(got.Chains.Removed) != 2 {
		t.Fatalf("removed chains = %+v, want obligation/igv-621 and exception/banco-001", got.Chains.Removed)
	}
	if len(got.Chains.Changed) != 2 || got.Chains.UnchangedCount != 1 {
		t.Fatalf("changed = %+v unchanged = %d, want account/4011 + adjust/aj-001 changed, 1 unchanged", got.Chains.Changed, got.Chains.UnchangedCount)
	}
	if len(got.StatusChanges) != 1 || got.StatusChanges[0].FromStatus != "pending_review" || got.StatusChanges[0].ToStatus != "approved" {
		t.Fatalf("statusChanges = %+v, want exactly adjust/aj-001 pending_review → approved", got.StatusChanges)
	}
	if got.PendingItems.From != 3 || got.PendingItems.To != 1 || got.PendingItems.Delta != -2 {
		t.Fatalf("pendingItems = %+v, want from 3, to 1, delta -2", got.PendingItems)
	}
	if len(got.PendingItems.AddedIDs) != 0 || !reflect.DeepEqual(got.PendingItems.ResolvedIDs, []string{"j4", "j5"}) {
		t.Fatalf("pending added/resolved = %v/%v, want [] / [j4 j5]", got.PendingItems.AddedIDs, got.PendingItems.ResolvedIDs)
	}
	if got.CloseState.From != "closed" || got.CloseState.To != "open" {
		t.Fatalf("closeState = %+v, want closed/open", got.CloseState)
	}
}

// ──────────────────────────────────────────────
// Vector 5 — initialize context (configured / missing / invalid)
// ──────────────────────────────────────────────

func TestV05ParityVector5CurrentContext(t *testing.T) {
	f := loadV05ParityFixture(t)
	v := f.Vectors.CurrentContext

	// Configured: the fixture's CurrentContext must decode into the Go model and
	// satisfy the bounded shape (exact scope, compact summary, ≤20 recent chains).
	var decoded core.CurrentContext
	raw, err := json.Marshal(v.Configured)
	if err != nil {
		t.Fatalf("marshal configured context: %v", err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("configured context must decode into core.CurrentContext: %v", err)
	}
	if !reflect.DeepEqual(decoded, v.Configured) {
		t.Fatalf("decoded context differs:\n got %+v\nwant %+v", decoded, v.Configured)
	}
	if decoded.Scope.Kind != core.ScopeKindCompany || decoded.Scope.Period != "202608" {
		t.Fatalf("configured scope = %+v, want the exact configured company scope with period 202608", decoded.Scope)
	}
	if len(decoded.RecentChains) > 20 {
		t.Fatalf("recentChains = %d, want at most 20", len(decoded.RecentChains))
	}
	if decoded.PeriodSummary.ClosureState != "open" || decoded.PeriodSummary.LatestClose != "" {
		t.Fatalf("periodSummary = %+v, want open with no latest close", decoded.PeriodSummary)
	}

	// Missing: unset configuration returns null and instructs the explicit tool.
	if v.Missing.Outcome != "null" || v.Missing.Instruction != "use accounting_current_context" {
		t.Fatalf("missing-scope semantics = %+v, want null + accounting_current_context instruction", v.Missing)
	}
	// Invalid: fail closed — no context and NO partial cross-scope data.
	if v.Invalid.Outcome != "null" || !v.Invalid.FailClosed || v.Invalid.PartialData {
		t.Fatalf("invalid-scope semantics = %+v, want fail-closed null with no partial data", v.Invalid)
	}
}

// ──────────────────────────────────────────────
// Small helpers
// ──────────────────────────────────────────────

func mustDecodeB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	return b
}

func hexDecode(s string) ([]byte, error) {
	return hex.DecodeString(s)
}
