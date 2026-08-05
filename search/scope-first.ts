/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Scope-first search (contracts/scope.md rule 1: scope is a structural filter,
 * never a post-filter). v2: kind/status/fiscal-effect filters are also
 * structural (they run inside the scope-filtered set, before ranking).
 *
 * Pipeline:
 *   1. STRUCTURAL SCOPE FILTER — exact scope match for company memories;
 *      institutional memories only when the query scope is institutional or
 *      the caller explicitly requests them via `includeInstitutional`.
 *   1b. v2 FILTERS — kind / status / fiscal effect (structural).
 *   2. Chain dedup — only the latest revision per (topicKey, exact scope).
 *   3. Token-overlap ranking over title + content (`matchMode` "all" requires
 *      every query token, "any" requires at least one).
 *   4. Stale marking — an expired memory (expiresAt < now) is surfaced as
 *      `stale`, never presented as current fact (contracts/lifecycle.md).
 */

import {
	assertValidScope,
	cloneMemory,
	scopeEquals,
	scopeKey,
	type AccountingMemory,
	type FiscalEffect,
	type MemoryKind,
	type MemoryScope,
	type MemoryStatus,
} from "../core/types.js";
import type { MemoryStore } from "../store/memory-store.js";

export interface ScopeFirstSearchInput {
	query: string;
	scope: MemoryScope;
	matchMode?: "all" | "any";
	/**
	 * Explicit institutional intent: with a company query scope, institutional
	 * memories are returned ONLY when this flag is true (scope.md rule 3).
	 */
	includeInstitutional?: boolean;
	/** Kind filter (OR). Empty = no kind filter. */
	kinds?: MemoryKind[];
	/** Status filter (OR). Empty = no status filter. */
	status?: MemoryStatus[];
	/** Exact fiscal-effect filter. Undefined = no effect filter. */
	fiscalEffect?: FiscalEffect;
	limit?: number;
}

export interface ScopeFirstSearchResult {
	memory: AccountingMemory;
	/** Number of query tokens matched across title + content. */
	score: number;
	/** True when the memory is expired (expiresAt < now) at read time. */
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
	const scoped = scopeCandidates(store, input);

	// STEP 1b — v2 structural filters.
	const candidates = v2Filters(scoped, input);

	// STEP 2 — latest revision per (topicKey, scope) chain.
	const latest = latestPerChain(candidates);

	// STEP 3 — token-overlap ranking over title + content.
	const results: ScopeFirstSearchResult[] = [];
	for (const memory of latest) {
		const score = scoreMemory(memory, tokens);
		const matched = matchMode === "all" ? score === tokens.length : score > 0;
		if (!matched) continue;
		results.push({
			memory: cloneMemory(memory),
			score,
			stale: isStale(memory),
		});
	}

	// Rank: higher overlap first, newer record time first, id last for stability.
	results.sort(
		(left, right) =>
			right.score - left.score ||
			timestampRank(right.memory) - timestampRank(left.memory) ||
			left.memory.identity.id.localeCompare(right.memory.identity.id),
	);

	return results.slice(0, limit);
}

function v2Filters(
	memories: AccountingMemory[],
	input: ScopeFirstSearchInput,
): AccountingMemory[] {
	const kinds = input.kinds ?? [];
	const statuses = input.status ?? [];
	if (
		kinds.length === 0 &&
		statuses.length === 0 &&
		input.fiscalEffect === undefined
	) {
		return memories;
	}
	return memories.filter((memory) => {
		if (kinds.length > 0 && !kinds.includes(memory.kind)) return false;
		if (statuses.length > 0 && !statuses.includes(memory.status)) return false;
		if (
			input.fiscalEffect !== undefined &&
			memory.fiscalEffect !== input.fiscalEffect
		) {
			return false;
		}
		return true;
	});
}

function scopeCandidates(
	store: MemoryStore,
	input: ScopeFirstSearchInput,
): AccountingMemory[] {
	const includeInstitutional =
		input.includeInstitutional === true || input.scope.kind === "institutional";
	return store.list().filter((memory) => {
		if (memory.scope.kind === "institutional") return includeInstitutional;
		return scopeEquals(memory.scope, input.scope);
	});
}

function latestPerChain(memories: AccountingMemory[]): AccountingMemory[] {
	const latest = new Map<string, AccountingMemory>();
	for (const memory of memories) {
		const key = `${memory.identity.topicKey}\u0000${scopeKey(memory.scope)}`;
		const current = latest.get(key);
		if (current === undefined || memory.revision > current.revision) {
			latest.set(key, memory);
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

function scoreMemory(memory: AccountingMemory, tokens: string[]): number {
	const searchable = new Set(
		tokenize(
			[
				memory.title,
				memory.content.what,
				memory.content.why,
				memory.content.where,
				memory.content.learned,
			].join(" "),
		),
	);
	return tokens.reduce(
		(score, token) => score + (searchable.has(token) ? 1 : 0),
		0,
	);
}

function isStale(memory: AccountingMemory): boolean {
	const expiresAt = memory.validity?.expiresAt;
	return expiresAt !== undefined && Date.parse(expiresAt) < Date.now();
}

function timestampRank(memory: AccountingMemory): number {
	const parsed = Date.parse(memory.recordedAt);
	return Number.isNaN(parsed) ? 0 : parsed;
}
