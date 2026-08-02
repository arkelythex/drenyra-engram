// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module implements scope-first search
// over observation text; there are no monetary fields in the model, so no
// money value is scored or computed here.
//
// Scope-first search (contracts/scope.md rule 1: scope is a structural filter,
// never a post-filter). Mirrors search/scope-first.ts semantically.
//
// Pipeline:
//  1. STRUCTURAL SCOPE FILTER — exact scope match for company observations;
//     institutional observations only when the query scope is institutional or
//     the caller explicitly requests them via IncludeInstitutional. This runs
//     BEFORE any scoring, so a company-A observation can never be scored for a
//     company-B query.
//  2. Chain dedup — only the latest revision per (topicKey, exact scope).
//  3. Token-overlap ranking over title + content (MatchAll requires every query
//     token, MatchAny requires at least one).
//  4. Stale marking — an expired observation (expiresAt < now) is surfaced as
//     Stale, never presented as current fact (contracts/lifecycle.md).

package search

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// MatchMode selects the token-overlap rule.
type MatchMode string

const (
	// MatchAll requires every query token to be present in title + content.
	MatchAll MatchMode = "all"
	// MatchAny requires at least one query token.
	MatchAny MatchMode = "any"
)

// ObservationSource is the narrow read surface search needs; the SQLite store
// satisfies it structurally (consumer-side interface).
type ObservationSource interface {
	List() ([]core.Observation, error)
}

// Input is a scope-first search request.
type Input struct {
	Query string
	Scope core.Scope
	// MatchMode defaults to MatchAny when empty (reference default).
	MatchMode MatchMode
	// IncludeInstitutional is the explicit institutional opt-in for company
	// query scopes (scope.md rule 3).
	IncludeInstitutional bool
	// Limit caps the returned results (0 → 20).
	Limit int
}

// Result is one ranked search hit.
type Result struct {
	Observation core.Observation `json:"observation"`
	// Score is the number of query tokens matched across title + content.
	Score int `json:"score"`
	// Stale is true when the observation is expired (expiresAt < now) at read
	// time — visible, never current fact.
	Stale bool `json:"stale"`
}

// tokenPattern matches the token separators of the reference
// (/[^a-z0-9áéíóúñ]+/iu). Go's regexp is UTF-8 aware; case folding happens
// via strings.ToLower before splitting.
var tokenPattern = regexp.MustCompile(`[^a-z0-9áéíóúñ]+`)

// ScopeFirst searches src scope-first and returns results ranked by token
// overlap. The structural scope filter runs before any ranking, so a
// company-A observation can never be scored for a company-B query — under any
// match mode, limit, or ordering.
func ScopeFirst(src ObservationSource, input Input) ([]Result, error) {
	if err := core.AssertValidScope(input.Scope); err != nil {
		return nil, err
	}

	matchMode := input.MatchMode
	if matchMode == "" {
		matchMode = MatchAny
	}
	if matchMode != MatchAll && matchMode != MatchAny {
		return nil, fmt.Errorf("INVALID_MATCH_MODE: %q — supported modes are %q and %q", matchMode, MatchAll, MatchAny)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}

	tokens := uniqueTokens(tokenize(input.Query))
	if len(tokens) == 0 {
		return nil, nil
	}

	all, err := src.List()
	if err != nil {
		return nil, err
	}

	// STEP 1 — structural scope filter, before any scoring.
	candidates := scopeCandidates(all, input)

	// STEP 2 — latest revision per (topicKey, scope) chain.
	latest := LatestPerChain(candidates)

	// STEP 3 — token-overlap ranking over title + content.
	now := time.Now()
	results := make([]Result, 0, len(latest))
	for _, observation := range latest {
		score := scoreObservation(observation, tokens)
		matched := (matchMode == MatchAll && score == len(tokens)) || (matchMode == MatchAny && score > 0)
		if !matched {
			continue
		}
		results = append(results, Result{
			Observation: core.CloneObservation(observation),
			Score:       score,
			Stale:       isStale(observation, now),
		})
	}

	// Rank: higher overlap first, newer provenance first, id last for stability.
	sort.SliceStable(results, func(i, j int) bool {
		left, right := results[i], results[j]
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		leftRank, rightRank := timestampRank(left.Observation), timestampRank(right.Observation)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		return left.Observation.Identity.ID < right.Observation.Identity.ID
	})

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func scopeCandidates(observations []core.Observation, input Input) []core.Observation {
	includeInstitutional := input.IncludeInstitutional || input.Scope.Kind == core.ScopeKindInstitutional
	candidates := make([]core.Observation, 0, len(observations))
	for _, observation := range observations {
		if observation.Scope.Kind == core.ScopeKindInstitutional {
			if includeInstitutional {
				candidates = append(candidates, observation)
			}
			continue
		}
		if core.ScopeEquals(observation.Scope, input.Scope) {
			candidates = append(candidates, observation)
		}
	}
	return candidates
}

// LatestPerChain keeps only the latest revision per (topicKey, exact scope)
// chain. It is exported because the CLI `context` command surfaces the current
// context the same way.
func LatestPerChain(observations []core.Observation) []core.Observation {
	latest := make(map[string]core.Observation)
	for _, observation := range observations {
		key := chainKey(observation)
		if current, ok := latest[key]; !ok || observation.Revision > current.Revision {
			latest[key] = observation
		}
	}
	out := make([]core.Observation, 0, len(latest))
	for _, observation := range latest {
		out = append(out, observation)
	}
	return out
}

func chainKey(observation core.Observation) string {
	return observation.Identity.TopicKey + "\x00" + core.ScopeKey(observation.Scope)
}

func tokenize(text string) []string {
	parts := tokenPattern.Split(strings.ToLower(text), -1)
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			tokens = append(tokens, part)
		}
	}
	return tokens
}

func uniqueTokens(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	unique := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		unique = append(unique, token)
	}
	return unique
}

func scoreObservation(observation core.Observation, tokens []string) int {
	searchable := make(map[string]struct{})
	for _, token := range tokenize(strings.Join([]string{
		observation.Title,
		observation.Content.What,
		observation.Content.Why,
		observation.Content.Where,
		observation.Content.Learned,
	}, " ")) {
		searchable[token] = struct{}{}
	}
	score := 0
	for _, token := range tokens {
		if _, ok := searchable[token]; ok {
			score++
		}
	}
	return score
}

func isStale(observation core.Observation, now time.Time) bool {
	if observation.Validity == nil || observation.Validity.ExpiresAt == "" {
		return false
	}
	expiresAt, ok := core.ParseDateTime(observation.Validity.ExpiresAt)
	return ok && expiresAt.Before(now)
}

func timestampRank(observation core.Observation) int64 {
	parsed, ok := core.ParseDateTime(observation.Provenance.Timestamp)
	if !ok {
		return 0
	}
	return parsed.UnixNano()
}
