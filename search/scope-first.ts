/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Scope-first search (contracts/scope.md rule 1: scope is a structural filter,
 * never a post-filter).
 *
 * Pipeline:
 *   1. STRUCTURAL SCOPE FILTER — exact scope match for company observations;
 *      institutional observations only when the query scope is institutional or
 *      the caller explicitly requests them via `includeInstitutional`. This runs
 *      BEFORE any scoring, so a company-A observation can never be scored for a
 *      company-B query.
 *   2. Chain dedup — only the latest revision per (topicKey, exact scope).
 *   3. Token-overlap ranking over title + content (`matchMode` "all" requires
 *      every query token, "any" requires at least one).
 *   4. Stale marking — an expired observation (expiresAt < now) is surfaced as
 *      `stale`, never presented as current fact (contracts/lifecycle.md).
 */

import {
  assertValidScope,
  cloneObservation,
  scopeEquals,
  scopeKey,
  type MemoryObservation,
  type MemoryScope,
} from "../core/types.js";
import type { MemoryStore } from "../store/memory-store.js";

export interface ScopeFirstSearchInput {
  query: string;
  scope: MemoryScope;
  matchMode?: "all" | "any";
  /**
   * Explicit institutional intent: with a company query scope, institutional
   * observations are returned ONLY when this flag is true (scope.md rule 3).
   */
  includeInstitutional?: boolean;
  limit?: number;
}

export interface ScopeFirstSearchResult {
  observation: MemoryObservation;
  /** Number of query tokens matched across title + content. */
  score: number;
  /** True when the observation is expired (expiresAt < now) at read time. */
  stale: boolean;
}

/** Search an in-memory store scope-first. Returns results ranked by overlap. */
export function scopeFirstSearch(
  store: MemoryStore,
  input: ScopeFirstSearchInput,
): ScopeFirstSearchResult[] {
  assertValidScope(input.scope);

  const matchMode = input.matchMode ?? "any";
  const limit = input.limit ?? 20;
  const tokens = uniqueTokens(tokenize(input.query));
  if (tokens.length === 0) return [];

  // STEP 1 — structural scope filter, before any scoring.
  const candidates = scopeCandidates(store, input);

  // STEP 2 — latest revision per (topicKey, scope) chain.
  const latest = latestPerChain(candidates);

  // STEP 3 — token-overlap ranking over title + content.
  const results: ScopeFirstSearchResult[] = [];
  for (const observation of latest) {
    const score = scoreObservation(observation, tokens);
    const matched = matchMode === "all" ? score === tokens.length : score > 0;
    if (!matched) continue;
    results.push({
      observation: cloneObservation(observation),
      score,
      stale: isStale(observation),
    });
  }

  // Rank: higher overlap first, newer provenance first, id last for stability.
  results.sort(
    (left, right) =>
      right.score - left.score ||
      timestampRank(right.observation) - timestampRank(left.observation) ||
      left.observation.identity.id.localeCompare(right.observation.identity.id),
  );

  return results.slice(0, limit);
}

function scopeCandidates(
  store: MemoryStore,
  input: ScopeFirstSearchInput,
): MemoryObservation[] {
  const includeInstitutional =
    input.includeInstitutional === true || input.scope.kind === "institutional";
  return store.list().filter((observation) => {
    if (observation.scope.kind === "institutional") return includeInstitutional;
    return scopeEquals(observation.scope, input.scope);
  });
}

function latestPerChain(observations: MemoryObservation[]): MemoryObservation[] {
  const latest = new Map<string, MemoryObservation>();
  for (const observation of observations) {
    const key = `${observation.identity.topicKey}\u0000${scopeKey(observation.scope)}`;
    const current = latest.get(key);
    if (current === undefined || observation.revision > current.revision) {
      latest.set(key, observation);
    }
  }
  return [...latest.values()];
}

const TOKEN_PATTERN = /[^a-z0-9áéíóúñ]+/iu;

function tokenize(text: string): string[] {
  return text
    .toLowerCase()
    .split(TOKEN_PATTERN)
    .map((term) => term.trim())
    .filter((term) => term.length > 0);
}

function uniqueTokens(tokens: string[]): string[] {
  return [...new Set(tokens)];
}

function scoreObservation(observation: MemoryObservation, tokens: string[]): number {
  const searchable = new Set(
    tokenize(
      [
        observation.title,
        observation.content.what,
        observation.content.why,
        observation.content.where,
        observation.content.learned,
      ].join(" "),
    ),
  );
  return tokens.reduce((score, token) => score + (searchable.has(token) ? 1 : 0), 0);
}

function isStale(observation: MemoryObservation): boolean {
  const expiresAt = observation.validity?.expiresAt;
  return expiresAt !== undefined && Date.parse(expiresAt) < Date.now();
}

function timestampRank(observation: MemoryObservation): number {
  const parsed = Date.parse(observation.provenance.timestamp);
  return Number.isNaN(parsed) ? 0 : parsed;
}
