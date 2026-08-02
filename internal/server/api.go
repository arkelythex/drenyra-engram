// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the shared domain-service
// layer for the engine surfaces; observation content is structured text
// (What/Why/Where/Learned) with no monetary fields, so no money value is
// computed here.
//
// Shared API — the single semantic surface exercised by every transport.
// docs/architecture.md: "Surfaces are adapters. MCP, HTTP, TUI, and CLI exercise
// the same domain services." All transports (CLI, HTTP, MCP) call this layer so
// compare verdicts, lifecycle transitions, and scope semantics stay identical
// everywhere — the CLI never re-derives them.
//
// Non-authorization boundary (contracts/provenance.md): this layer deliberately
// has NO authorize/approve/allow operation. Memory guides; it never authorizes.
// Consumers route approvals through drenyra-ai gates and human professionals.
//
// Error model: the engine's stable error codes are returned as error strings
// (INVALID_SCOPE, OBSERVATION_NOT_FOUND, INVALID_TRANSITION, IMMUTABLE_*).
// Transport adapters classify them with IsNotFound/IsInvalid/IsConflict so
// HTTP status codes and MCP tool errors map the same way on every surface.

package server

import (
	"errors"
	"strings"
	"time"

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

// Save upserts an observation under its (topicKey, exact scope) chain.
func (a *API) Save(input core.SaveInput) (core.WriteResult, error) {
	return a.Store.Save(input)
}

// ──────────────────────────────────────────────
// Reads
// ──────────────────────────────────────────────

// Get returns the observation with the given id; OBSERVATION_NOT_FOUND when
// absent (callers classify with IsNotFound).
func (a *API) Get(id string) (core.Observation, error) {
	observation, ok := a.Store.FindByID(id)
	if !ok {
		return core.Observation{}, errors.New("OBSERVATION_NOT_FOUND: " + id)
	}
	return observation, nil
}

// GetByTopic returns the latest revision of the (topicKey, exact scope) chain.
func (a *API) GetByTopic(topicKey string, scope core.Scope) (core.Observation, error) {
	observation, ok := a.Store.FindByTopicKey(topicKey, scope)
	if !ok {
		return core.Observation{}, errors.New("OBSERVATION_NOT_FOUND: topicKey " + topicKey)
	}
	return observation, nil
}

// Chain returns the FULL revision history of a (topicKey, exact scope) chain,
// ordered by revision ascending (every revision, not just the current one —
// the counterpart of GetByTopic).
func (a *API) Chain(topicKey string, scope core.Scope) ([]core.Observation, error) {
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
		chain = []core.Observation{}
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
func (a *API) Context(scope core.Scope) ([]core.Observation, error) {
	observations, err := a.Store.FindByScope(scope)
	if err != nil {
		return nil, err
	}
	current := search.LatestPerChain(observations)
	if current == nil {
		current = []core.Observation{}
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
	IDA             string               `json:"idA"`
	IDB             string               `json:"idB"`
	IdentityMatch   bool                 `json:"identityMatch"`
	ScopeMatch      string               `json:"scopeMatch"`
	StatusA         core.AuthorityStatus `json:"statusA"`
	StatusB         core.AuthorityStatus `json:"statusB"`
	ContentDeltas   ContentDeltas        `json:"contentDeltas"`
	RelationVerdict string               `json:"relationVerdict"`
}

// ContentDeltas flags which of the four structured fields differ between the
// two observations (contracts/memory.md rule 1: What/Why/Where/Learned).
type ContentDeltas struct {
	What    bool `json:"what"`
	Why     bool `json:"why"`
	Where   bool `json:"where"`
	Learned bool `json:"learned"`
}

// Compare reports how two stored observations relate.
func (a *API) Compare(idA, idB string) (CompareOutput, error) {
	aObs, err := a.Get(idA)
	if err != nil {
		return CompareOutput{}, err
	}
	bObs, err := a.Get(idB)
	if err != nil {
		return CompareOutput{}, err
	}

	identityMatch := aObs.Identity.ID == bObs.Identity.ID || aObs.Identity.TopicKey == bObs.Identity.TopicKey

	return CompareOutput{
		IDA:             aObs.Identity.ID,
		IDB:             bObs.Identity.ID,
		IdentityMatch:   identityMatch,
		ScopeMatch:      compareScopeMatch(aObs.Scope, bObs.Scope),
		StatusA:         aObs.AuthorityStatus,
		StatusB:         bObs.AuthorityStatus,
		ContentDeltas:   compareContentDeltas(aObs.Content, bObs.Content),
		RelationVerdict: compareRelationVerdict(a, aObs, bObs, identityMatch),
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

// compareRelationVerdict decides how the two observations relate:
//   - "supersedes" — the relations table records A→B as `supersedes` AND A (the
//     superseded source) is stored as superseded — a completed supersede pair.
//     The successor B is typically draft/promoted, never superseded itself.
//   - "related" — the observations share a topicKey;
//   - "not_conflict" — otherwise.
//
// The supersedes check runs first so a completed supersede pair wins over the
// weaker shared-topicKey signal.
func compareRelationVerdict(a *API, aObs, bObs core.Observation, identityMatch bool) string {
	if rel, ok := a.Store.RelationBetween(aObs.Identity.ID, bObs.Identity.ID); ok && rel == string(core.RelationSupersedes) && aObs.AuthorityStatus == core.StatusSuperseded {
		return "supersedes"
	}
	if identityMatch {
		return "related"
	}
	return "not_conflict"
}

// ──────────────────────────────────────────────
// Lifecycle — review / promote / supersede
// ──────────────────────────────────────────────

// TransitionOutput is the JSON shape of review and promote.
type TransitionOutput struct {
	ID       string               `json:"id"`
	From     core.AuthorityStatus `json:"from"`
	To       core.AuthorityStatus `json:"to"`
	Revision int                  `json:"revision"`
}

// Review moves an observation draft → reviewed (adjacent-forward only; illegal
// moves fail closed with INVALID_TRANSITION, leaving the observation unchanged).
func (a *API) Review(id, actor string) (TransitionOutput, error) {
	return a.transition(id, core.StatusReviewed, actor)
}

// Promote moves an observation reviewed → promoted.
func (a *API) Promote(id, actor string) (TransitionOutput, error) {
	return a.transition(id, core.StatusPromoted, actor)
}

func (a *API) transition(id string, to core.AuthorityStatus, actor string) (TransitionOutput, error) {
	if actor == "" {
		actor = a.DefaultActor
	}
	before, err := a.Get(id)
	if err != nil {
		return TransitionOutput{}, err
	}
	updated, err := core.ApplyTransition(a.Store, id, to, core.TransitionMeta{
		Actor:     actor,
		Timestamp: nowISO(),
	})
	if err != nil {
		return TransitionOutput{}, err
	}
	return TransitionOutput{
		ID:       updated.Identity.ID,
		From:     before.AuthorityStatus,
		To:       updated.AuthorityStatus,
		Revision: updated.Revision,
	}, nil
}

// SupersedeOutput is the JSON shape of the supersede operation.
type SupersedeOutput struct {
	ID       string               `json:"id"`
	From     core.AuthorityStatus `json:"from"`
	To       core.AuthorityStatus `json:"to"`
	TargetID string               `json:"targetId"`
}

// Supersede marks a promoted observation superseded and records a `supersedes`
// relation to the REQUIRED target (never auto-promotes the replacement —
// contracts/lifecycle.md rule 1).
func (a *API) Supersede(id, targetID, actor string) (SupersedeOutput, error) {
	if actor == "" {
		actor = a.DefaultActor
	}
	before, err := a.Get(id)
	if err != nil {
		return SupersedeOutput{}, err
	}
	updated, err := core.Supersede(core.SupersedeInput{
		Store:         a.Store,
		ObservationID: id,
		TargetID:      targetID,
		Actor:         actor,
		Timestamp:     nowISO(),
	})
	if err != nil {
		return SupersedeOutput{}, err
	}
	return SupersedeOutput{
		ID:       updated.Identity.ID,
		From:     before.AuthorityStatus,
		To:       updated.AuthorityStatus,
		TargetID: targetID,
	}, nil
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
	return err != nil && strings.Contains(err.Error(), "OBSERVATION_NOT_FOUND")
}

// IsInvalid reports whether err is a caller/validation failure (400 on HTTP):
// malformed scope, RUC, period, content, provenance, topic key or supersede
// target.
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
