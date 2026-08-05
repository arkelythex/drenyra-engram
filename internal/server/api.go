// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the shared domain-service
// layer for the engine surfaces. The memory model carries no monetary fields
// except the optional Materiality threshold (int64 cents); Confidence is a
// probability (0..1), never money. No money value is computed here.
//
// Shared API — the single semantic surface exercised by every transport.
// docs/architecture.md: "Surfaces are adapters. MCP, HTTP, TUI, and CLI exercise
// the same domain services." All transports (CLI, HTTP, MCP) call this layer so
// approval gates, lifecycle transitions, and scope semantics stay identical
// everywhere — the CLI never re-derives them.
//
// v2 approval gate (contracts/lifecycle.md): a memory with fiscalEffect != none
// is saved as pending_review and can only reach `approved` through a HUMAN
// actor (source.actorKind == human). Agents and systems record; they never
// approve. This layer implements the mandatory human gate for approve/reject;
// it still has NO authorize operation beyond the professional review of a
// memory — memory guides, it never authorizes business actions.
//
// Error model: stable error codes are returned as error strings
// (INVALID_SCOPE, MEMORY_NOT_FOUND, INVALID_TRANSITION, GATE_REQUIRES_HUMAN).
// Transport adapters classify them with IsNotFound/IsInvalid/IsConflict/
// IsGateError so HTTP status codes and MCP tool errors map the same way.
package server

import (
	"errors"
	"strings"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/search"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// API is the shared domain-service surface. It wraps a store.Store and composes
// the store, search, and lifecycle machines into the operations the transports
// expose. It is safe for concurrent readers; the store serializes writers
// (single-connection SQLite, the daemon pattern of ADR-001).
type API struct {
	Store store.Store
	// DefaultActor is recorded in the audit trail when a caller does not name
	// an actor explicitly (e.g. an HTTP request without an actor field).
	DefaultActor string
}

// New returns an API over st with the given default actor.
func New(st store.Store, defaultActor string) *API {
	if defaultActor == "" {
		defaultActor = "engine"
	}
	return &API{Store: st, DefaultActor: defaultActor}
}

// ──────────────────────────────────────────────
// Writes
// ──────────────────────────────────────────────

// Save validates and upserts a memory under its (topicKey, exact scope) chain,
// applying the v2 approval gate:
//   - recordedAt is the engine clock (automatic, immutable).
//   - effectiveAt defaults to recordedAt when absent (callers that need a
//     different accounting date pass it explicitly — critical for late events
//     affecting a previous closed period).
//   - status derives from fiscalEffect: none → active (informative); any
//     non-none effect → pending_review (mandatory human approval gate).
//   - contentHash is computed from the immutable content.
//
// A prior current revision of the same (topicKey, scope) is superseded with a
// `supersedes` relation to the new revision (immutable history — nothing is
// ever edited in place).
func (a *API) Save(input core.SaveInput) (core.WriteResult, error) {
	// The store owns derivation (recordedAt, status gate, contentHash, revision,
	// supersession) and full v2 validation (AssertValidMemory covers scope,
	// content, source, kind, status, fiscal effect, timestamps, confidence and
	// materiality). This layer adds the explicit supersedes relation so the
	// immutable-history chain is visible in the relation graph.
	// The store records the supersedes relation atomically when it supersedes a
	// previous revision of the chain (immutable history is visible in the
	// relation graph).
	return a.Store.Save(input)
}

// ──────────────────────────────────────────────
// Reads
// ──────────────────────────────────────────────

// Get returns the memory with the given id; MEMORY_NOT_FOUND when absent
// (callers classify with IsNotFound).
func (a *API) Get(id string) (core.AccountingMemory, error) {
	memory, ok := a.Store.FindByID(id)
	if !ok {
		return core.AccountingMemory{}, errors.New("MEMORY_NOT_FOUND: " + id)
	}
	return memory, nil
}

// GetByTopic returns the latest revision of the (topicKey, exact scope) chain.
func (a *API) GetByTopic(topicKey string, scope core.Scope) (core.AccountingMemory, error) {
	memory, ok := a.Store.FindByTopicKey(topicKey, scope)
	if !ok {
		return core.AccountingMemory{}, errors.New("MEMORY_NOT_FOUND: topicKey " + topicKey)
	}
	return memory, nil
}

// Chain returns the FULL revision history of a (topicKey, exact scope) chain,
// ordered by revision ascending (every revision, not just the current one —
// the counterpart of GetByTopic).
func (a *API) Chain(topicKey string, scope core.Scope) ([]core.AccountingMemory, error) {
	if err := core.AssertValidScope(scope); err != nil {
		return nil, err
	}
	if strings.TrimSpace(topicKey) == "" {
		return nil, errors.New("INVALID_TOPIC_KEY: topicKey must be a non-empty string")
	}
	chain, err := a.Store.FindChain(topicKey, scope)
	if err != nil {
		return nil, err
	}
	if chain == nil {
		chain = []core.AccountingMemory{}
	}
	return chain, nil
}

// Search runs the scope-first search (scope is a structural filter, never a
// post-filter — contracts/scope.md rule 1).
func (a *API) Search(input search.Input) ([]search.Result, error) {
	results, err := search.ScopeFirst(a.Store, input)
	if err != nil {
		return nil, err
	}
	if results == nil {
		return []search.Result{}, nil
	}
	return results, nil
}

// Context returns the CURRENT memory for a scope: the latest revision per
// (topicKey, exact scope) chain, never the full revision history.
func (a *API) Context(scope core.Scope) ([]core.AccountingMemory, error) {
	memories, err := a.Store.FindByScope(scope)
	if err != nil {
		return nil, err
	}
	current := search.LatestPerChain(memories)
	if current == nil {
		current = []core.AccountingMemory{}
	}
	return current, nil
}

// Relations returns every recorded relation, insertion order.
func (a *API) Relations() ([]core.RelationRecord, error) {
	return a.Store.Relations()
}

// Transitions returns the full lifecycle audit trail, insertion order.
func (a *API) Transitions() ([]core.StatusTransitionRecord, error) {
	return a.Store.TransitionLog()
}

// Doctor returns the store health snapshot (schema guards, counts).
func (a *API) Doctor() (store.DoctorReport, error) {
	return a.Store.Doctor()
}

// ──────────────────────────────────────────────
// compare — identity / scope / content deltas + relation verdict
// ──────────────────────────────────────────────

// CompareOutput is the JSON shape of the compare operation (shared by every
// transport so verdicts are byte-identical).
type CompareOutput struct {
	IDA             string            `json:"idA"`
	IDB             string            `json:"idB"`
	IdentityMatch   bool              `json:"identityMatch"`
	ScopeMatch      string            `json:"scopeMatch"`
	KindA           core.MemoryKind   `json:"kindA"`
	KindB           core.MemoryKind   `json:"kindB"`
	StatusA         core.MemoryStatus `json:"statusA"`
	StatusB         core.MemoryStatus `json:"statusB"`
	ContentDeltas   ContentDeltas     `json:"contentDeltas"`
	RelationVerdict string            `json:"relationVerdict"`
}

// ContentDeltas flags which of the four structured fields differ between the
// two memories (contracts/memory.md rule 1: What/Why/Where/Learned).
type ContentDeltas struct {
	What    bool `json:"what"`
	Why     bool `json:"why"`
	Where   bool `json:"where"`
	Learned bool `json:"learned"`
}

// Compare reports how two stored memories relate.
func (a *API) Compare(idA, idB string) (CompareOutput, error) {
	aMem, err := a.Get(idA)
	if err != nil {
		return CompareOutput{}, err
	}
	bMem, err := a.Get(idB)
	if err != nil {
		return CompareOutput{}, err
	}

	identityMatch := aMem.Identity.ID == bMem.Identity.ID || aMem.Identity.TopicKey == bMem.Identity.TopicKey

	return CompareOutput{
		IDA:             aMem.Identity.ID,
		IDB:             bMem.Identity.ID,
		IdentityMatch:   identityMatch,
		ScopeMatch:      compareScopeMatch(aMem.Scope, bMem.Scope),
		KindA:           aMem.Kind,
		KindB:           bMem.Kind,
		StatusA:         aMem.Status,
		StatusB:         bMem.Status,
		ContentDeltas:   compareContentDeltas(aMem.Content, bMem.Content),
		RelationVerdict: compareRelationVerdict(a, aMem, bMem, identityMatch),
	}, nil
}

// compareScopeMatch reports how two scopes relate: "exact" (equal scope per
// core.ScopeEquals — period participates in equality), "partial" (same
// company/RUC with a different organization or period) or "none" otherwise.
func compareScopeMatch(a, b core.Scope) string {
	if core.ScopeEquals(a, b) {
		return "exact"
	}
	if a.Kind == core.ScopeKindCompany && b.Kind == core.ScopeKindCompany &&
		a.CompanyID == b.CompanyID && a.RUC == b.RUC &&
		(a.OrganizationID != b.OrganizationID || a.Period != b.Period) {
		return "partial"
	}
	return "none"
}

func compareContentDeltas(a, b core.Content) ContentDeltas {
	return ContentDeltas{
		What:    a.What != b.What,
		Why:     a.Why != b.Why,
		Where:   a.Where != b.Where,
		Learned: a.Learned != b.Learned,
	}
}

// compareRelationVerdict decides how the two memories relate:
//   - "supersedes" — the relations table records A→B as `supersedes` AND A (the
//     superseded source) is stored as superseded — a completed supersede pair.
//   - "related" — the memories share a topicKey;
//   - "not_conflict" — otherwise.
func compareRelationVerdict(a *API, aMem, bMem core.AccountingMemory, identityMatch bool) string {
	if rel, ok := a.Store.RelationBetween(aMem.Identity.ID, bMem.Identity.ID); ok && rel == string(core.RelationSupersedes) && aMem.Status == core.StatusSuperseded {
		return "supersedes"
	}
	if identityMatch {
		return "related"
	}
	return "not_conflict"
}

// ──────────────────────────────────────────────
// Lifecycle — v2 human-gated approval machine
// ──────────────────────────────────────────────

// TransitionOutput is the JSON shape of an approval lifecycle operation.
type TransitionOutput struct {
	ID       string            `json:"id"`
	From     core.MemoryStatus `json:"from"`
	To       core.MemoryStatus `json:"to"`
	Revision int               `json:"revision"`
}

// Approve approves a pending_review memory. REQUIRES a human actor
// (source.actorKind == human); anything else fails with GATE_REQUIRES_HUMAN.
// The approval is recorded in the audit trail with actor + actorKind — approval
// provenance, never an artificial relation.
func (a *API) Approve(memoryID string, actor core.Source) (core.AccountingMemory, error) {
	return a.gatedTransition(memoryID, actor, func(m *core.AccountingMemory, src core.Source) error {
		return core.Approve(m, src)
	})
}

// Reject rejects a pending_review memory (terminal). REQUIRES a human actor.
func (a *API) Reject(memoryID string, actor core.Source) (core.AccountingMemory, error) {
	return a.gatedTransition(memoryID, actor, func(m *core.AccountingMemory, src core.Source) error {
		return core.Reject(m, src)
	})
}

// Void voids an active | pending_review | approved memory (terminal, no
// successor). Admits human or system actors (systemic correction), NEVER an
// agent. The reason for the void is documented by creating a memory (the
// caller records the correction context); the void itself is a status-only
// transition.
func (a *API) Void(memoryID string, actor core.Source) (core.AccountingMemory, error) {
	return a.gatedTransition(memoryID, actor, func(m *core.AccountingMemory, src core.Source) error {
		return core.Void(m, src)
	})
}

// gatedTransition applies a lifecycle mutation (approve/reject/void) and
// persists the resulting status transition with the actor's audit metadata.
func (a *API) gatedTransition(memoryID string, actor core.Source, mutate func(*core.AccountingMemory, core.Source) error) (core.AccountingMemory, error) {
	if actor.ActorID == "" {
		actor.ActorID = a.DefaultActor
	}
	memory, ok := a.Store.FindByID(memoryID)
	if !ok {
		return core.AccountingMemory{}, errors.New("MEMORY_NOT_FOUND: " + memoryID)
	}
	before := memory.Status
	if err := mutate(&memory, actor); err != nil {
		return core.AccountingMemory{}, err
	}
	updated, err := a.Store.ApplyStatusTransition(memoryID, memory.Status, core.TransitionMeta{
		Actor:     actor.ActorID,
		ActorKind: actor.ActorKind,
		Timestamp: nowISO(),
	})
	if err != nil {
		return core.AccountingMemory{}, err
	}
	_ = before // the audit trail records from/to; kept for clarity
	return updated, nil
}

// SupersedeOutput is the JSON shape of the explicit supersede operation.
type SupersedeOutput struct {
	ID       string            `json:"id"`
	From     core.MemoryStatus `json:"from"`
	To       core.MemoryStatus `json:"to"`
	TargetID string            `json:"targetId"`
}

// Supersede explicitly marks a memory superseded and records a `supersedes`
// relation to the REQUIRED successor (never auto-promotes the replacement).
// Immutable history: the superseded memory stays visible and routes readers to
// the successor. (Save also supersedes automatically when a chain evolves.)
func (a *API) Supersede(memoryID, successorID string, actor core.Source) (SupersedeOutput, error) {
	if actor.ActorID == "" {
		actor.ActorID = a.DefaultActor
	}
	before, err := a.Get(memoryID)
	if err != nil {
		return SupersedeOutput{}, err
	}
	if _, err := a.Get(successorID); err != nil {
		return SupersedeOutput{}, err
	}
	if memoryID == successorID {
		return SupersedeOutput{}, errors.New("INVALID_SUPERSEDE_TARGET: a memory cannot supersede itself")
	}
	// Legality via the lifecycle machine, then one atomic low-level mutation.
	superseded := core.CloneMemory(before)
	if err := core.SupersedePrev(&superseded, successorID); err != nil {
		return SupersedeOutput{}, err
	}
	updated, err := a.Store.SupersedeExplicit(memoryID, successorID, core.TransitionMeta{
		Actor:     actor.ActorID,
		ActorKind: actor.ActorKind,
		Timestamp: nowISO(),
	})
	if err != nil {
		return SupersedeOutput{}, err
	}
	return SupersedeOutput{
		ID:       updated.Identity.ID,
		From:     before.Status,
		To:       updated.Status,
		TargetID: successorID,
	}, nil
}

// ──────────────────────────────────────────────
// Evidence / rule links (immutable memory, growing links)
// ──────────────────────────────────────────────

// LinkEvidence attaches evidence references (XML/PDF/CDR/extracto) to a memory
// AFTER write. The memory itself is never mutated (immutability); links live in
// the dedicated evidence_links table. Returns the full deduplicated list
// (write-time refs + links).
func (a *API) LinkEvidence(memoryID string, refs []string, actor string) ([]string, error) {
	memory, ok := a.Store.FindByID(memoryID)
	if !ok {
		return nil, errors.New("MEMORY_NOT_FOUND: " + memoryID)
	}
	if actor == "" {
		actor = a.DefaultActor
	}
	seen := make(map[string]struct{})
	for _, ref := range memory.EvidenceRefs {
		seen[ref] = struct{}{}
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			continue
		}
		if err := a.Store.AddEvidenceLink(memoryID, ref, actor); err != nil {
			return nil, err
		}
		seen[ref] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for ref := range seen {
		out = append(out, ref)
	}
	return out, nil
}

// LinkRules attaches rule/policy references (e.g. "policy/igv/late-document-v3")
// to a memory AFTER write. Same immutability contract as LinkEvidence.
func (a *API) LinkRules(memoryID string, refs []string, actor string) ([]string, error) {
	memory, ok := a.Store.FindByID(memoryID)
	if !ok {
		return nil, errors.New("MEMORY_NOT_FOUND: " + memoryID)
	}
	if actor == "" {
		actor = a.DefaultActor
	}
	seen := make(map[string]struct{})
	for _, ref := range memory.RuleRefs {
		seen[ref] = struct{}{}
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			continue
		}
		if err := a.Store.AddRuleLink(memoryID, ref, actor); err != nil {
			return nil, err
		}
		seen[ref] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for ref := range seen {
		out = append(out, ref)
	}
	return out, nil
}

// ──────────────────────────────────────────────
// Judge — DEPRECATED legacy caller-declared adjudication (design §4)
// ──────────────────────────────────────────────

// Judge is the DEPRECATED v0.3 caller-declared adjudication path (design §4).
// Since v0.4.0 Step 2 it is FAIL-CLOSED: it returns AUTHENTICATION_REQUIRED
// and writes NOTHING — no decision memory, no explains relation. The legacy
// caller-declared actor (machine OR human-without-a-principal) can never
// supply the verified principal that adjudication now requires; confirm and
// reject happen only through the authenticated judgment services (POST
// /accounting/judgments/{id}/confirm|reject). Kept compiled for v0.3
// consumers; removed in v0.5.0.
func (a *API) Judge(conflictID string, resolution string, actor core.Source) (core.AccountingMemory, error) {
	return core.AccountingMemory{}, auth.New(auth.CodeAuthenticationRequired,
		"legacy Judge is deprecated and fail-closed: authenticated adjudication happens through the judgment services (design §4)")
}

// ──────────────────────────────────────────────
// Period summary — the killer demo (explainable institutional memory)
// ──────────────────────────────────────────────

// PeriodSummaryOutput is the aggregated, explainable view of a fiscal period.
// v0.5.0 close foundation (design §2.2): LatestClose is the current close memory
// (nil when the period has none), ClosureState is the period_closures projection
// state (open|closed|reopened) and PendingItems is the shared pending-item digest
// (pending_review memories + active/pending/approved obligations and exceptions,
// deduped, sorted by kind/effectiveAt/memoryId). The pre-v0.5 fields stay
// unchanged for compatibility.
type PeriodSummaryOutput struct {
	Scope             core.Scope                `json:"scope"`
	Total             int                       `json:"total"`
	ByKind            map[core.MemoryKind]int   `json:"byKind"`
	ByStatus          map[core.MemoryStatus]int `json:"byStatus"`
	PendingApprovals  []core.AccountingMemory   `json:"pendingApprovals"`
	ActiveObligations []core.AccountingMemory   `json:"activeObligations"`
	ActiveExceptions  []core.AccountingMemory   `json:"activeExceptions"`
	Narrative         []NarrativeItem           `json:"narrative"`
	NarrativeText     string                    `json:"narrativeText"`
	// LatestClose is the latest revision of the period's close chain
	// (closing/CIERRE-<period>), nil when the period has no close memory.
	LatestClose *core.AccountingMemory `json:"latestClose,omitempty"`
	// ClosureState is the authoritative period_closures projection state:
	// "open" (never closed), "closed" or "reopened" (design §2.2).
	ClosureState string `json:"closureState"`
	// PendingItems is the shared pending-item digest (design §2.2) — the same
	// frozen list CreateClose embeds in the CloseSnapshot.
	PendingItems []core.ClosePendingItem `json:"pendingItems"`
}

// NarrativeItem is one line of the explainable period narrative (the killer
// demo: "why did account 4011 end with this balance").
type NarrativeItem struct {
	MemoryID    string          `json:"memoryId"`
	EffectiveAt string          `json:"effectiveAt"`
	Kind        core.MemoryKind `json:"kind"`
	Title       string          `json:"title"`
	What        string          `json:"what"`
}

// PeriodSummary aggregates the memories of an exact scope (company + period) and
// composes an explainable narrative ordered by accounting-effective date.
//   - ByKind / ByStatus: counts over ALL memories of the scope.
//   - PendingApprovals: memories awaiting the human approval gate.
//   - ActiveObligations / ActiveExceptions: current (non-terminal) obligations
//     and exceptions.
//   - Narrative: fact/decision/exception memories in current states, ordered by
//     EffectiveAt ASC — the institutional story of the period.
func (a *API) PeriodSummary(scope core.Scope) (PeriodSummaryOutput, error) {
	if err := core.AssertValidScope(scope); err != nil {
		return PeriodSummaryOutput{}, err
	}
	memories, err := a.Store.FindByScope(scope)
	if err != nil {
		return PeriodSummaryOutput{}, err
	}
	current := search.LatestPerChain(memories)

	out := PeriodSummaryOutput{
		Scope:             scope,
		Total:             len(current),
		ByKind:            make(map[core.MemoryKind]int),
		ByStatus:          make(map[core.MemoryStatus]int),
		PendingApprovals:  []core.AccountingMemory{},
		ActiveObligations: []core.AccountingMemory{},
		ActiveExceptions:  []core.AccountingMemory{},
		Narrative:         []NarrativeItem{},
		// v0.5.0 close foundation (design §2.2): the projection state defaults to
		// open and is replaced by the stored row when the period was closed.
		ClosureState: string(core.ClosureStateOpen),
		PendingItems: []core.ClosePendingItem{},
	}
	for _, memory := range current {
		out.ByKind[memory.Kind]++
		out.ByStatus[memory.Status]++
		switch memory.Status {
		case core.StatusPendingReview:
			out.PendingApprovals = append(out.PendingApprovals, memory)
		}
		if memory.Status == core.StatusActive || memory.Status == core.StatusPendingReview || memory.Status == core.StatusApproved {
			switch memory.Kind {
			case core.KindObligation:
				out.ActiveObligations = append(out.ActiveObligations, memory)
			case core.KindException:
				out.ActiveExceptions = append(out.ActiveExceptions, memory)
			}
		}
		if isNarrativeKind(memory) {
			out.Narrative = append(out.Narrative, NarrativeItem{
				MemoryID:    memory.Identity.ID,
				EffectiveAt: memory.EffectiveAt,
				Kind:        memory.Kind,
				Title:       memory.Title,
				What:        memory.Content.What,
			})
		}
	}

	sortNarrativeByEffectiveAt(out.Narrative)

	// v0.5.0 close foundation (design §2.2): the closure projection state, the
	// latest close revision of the period's close chain and the shared
	// pending-item digest. The projection is the authoritative closure source;
	// the close memory (with its frozen snapshot) is exposed for review.
	if closure, ok := a.Store.FindPeriodClosure(scope); ok {
		out.ClosureState = closure.Status
	}
	for _, memory := range current {
		if core.IsCloseMemory(memory) {
			m := memory
			out.LatestClose = &m
			break
		}
	}
	out.PendingItems = deriveClosePendingItems(out, scope.Period)

	period := scope.Period
	if period == "" {
		period = "scope"
	}
	var sb strings.Builder
	sb.WriteString("Periodo ")
	sb.WriteString(period)
	sb.WriteString(" — ")
	sb.WriteString(itoa(len(out.Narrative)))
	sb.WriteString(" memorias relevantes:\n")
	for _, item := range out.Narrative {
		sb.WriteString("- ")
		sb.WriteString(item.Title)
		sb.WriteString(": ")
		sb.WriteString(item.What)
		sb.WriteString(" (efectivo ")
		sb.WriteString(item.EffectiveAt)
		sb.WriteString(")\n")
	}
	out.NarrativeText = sb.String()
	return out, nil
}

// FindPeriodClosure returns the period_closures projection record of the exact
// company scope, when one exists (v0.5.0 close foundation — the authoritative
// closure state consumed by PeriodSummary and CreateClose).
func (a *API) FindPeriodClosure(scope core.Scope) (store.PeriodClosureRecord, bool) {
	return a.Store.FindPeriodClosure(scope)
}

// isNarrativeKind reports whether a memory participates in the period
// narrative: fact / decision / exception in a current (non-terminal) state.
// Terminal states (superseded, voided, rejected) never narrate current facts.
func isNarrativeKind(memory core.AccountingMemory) bool {
	if memory.Status == core.StatusSuperseded || memory.Status == core.StatusVoided || memory.Status == core.StatusRejected {
		return false
	}
	switch memory.Kind {
	case core.KindFact, core.KindDecision, core.KindException:
		return true
	}
	return false
}

// sortNarrativeByEffectiveAt orders narrative items by effective date ascending
// (stable — ties keep insertion order).
func sortNarrativeByEffectiveAt(items []NarrativeItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0; j-- {
			if lessEffective(items[j], items[j-1]) {
				items[j], items[j-1] = items[j-1], items[j]
			} else {
				break
			}
		}
	}
}

func lessEffective(a, b NarrativeItem) bool {
	ta, oka := core.ParseDateTime(a.EffectiveAt)
	tb, okb := core.ParseDateTime(b.EffectiveAt)
	if !oka {
		return false
	}
	if !okb {
		return true
	}
	return ta.Before(tb)
}

// itoa is a small allocation-free int formatter for the narrative (avoids
// importing strconv for a single use).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	negative := n < 0
	if negative {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// nowISO is the API's event timestamp: current UTC time in RFC3339, which the
// core timestamp grammar accepts (contracts/provenance.md rule 3: every state
// traces to actor+time).
func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ──────────────────────────────────────────────
// Error classification (shared by every transport)
// ──────────────────────────────────────────────

// IsNotFound reports whether err is a not-found failure (404 on HTTP).
func IsNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "MEMORY_NOT_FOUND")
}

// IsInvalid reports whether err is a caller/validation failure (400 on HTTP):
// malformed scope, RUC, period, content, source, topic key or supersede target.
func IsInvalid(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "INVALID_") && !strings.Contains(message, "INVALID_TRANSITION")
}

// IsConflict reports whether err is a state conflict (409 on HTTP): an illegal
// lifecycle transition or an immutability guard violation. The caller must
// re-read state before acting (fail closed).
func IsConflict(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "INVALID_TRANSITION") || strings.Contains(message, "IMMUTABLE_")
}

// IsGateError reports whether err is the mandatory human-approval gate
// (403 on HTTP): approve/reject requires a human actor.
func IsGateError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "GATE_REQUIRES_HUMAN")
}
