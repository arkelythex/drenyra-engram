/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * In-memory memory store (v2) — immutable revision history, scope-partitioned.
 * Implements the storage surface of contracts/memory.md for the reference mirror.
 *
 * Semantics (mirror of internal/store/store.go):
 * - Upsert by (topicKey, exact scope): each save creates a NEW revision and
 *   supersedes the previous current revision (status `superseded` +
 *   `supersedesId` + `supersedes` relation) — NEVER edits in place.
 * - `save` derives `recordedAt`, `effectiveAt` (default = record time),
 *   `status` from the fiscal effect (the approval gate) and the canonical
 *   `contentHash`. fiscalEffect != none → `pending_review`.
 * - `applyStatusTransition` is the single status-only mutation the lifecycle
 *   machine may perform; legality and the human gate live in
 *   lifecycle/transitions.ts, not re-derived here.
 * - Write outcomes: `created` / `updated` on success; `unknown` is the
 *   documented fallback on an unexpected persistence error — the memory is NOT
 *   stored and callers must re-read state. Invalid input throws (fail closed).
 * - `conflict` is reserved for a future optimistic-concurrency slice.
 */

import { randomUUID } from "node:crypto";

import {
	assertValidContent,
	assertValidMemory,
	assertValidScope,
	assertValidSource,
	assertValidValidity,
	cloneMemory,
	computeContentHash,
	scopeEquals,
	scopeKey,
	type AccountingMemory,
	type MemoryRelation,
	type MemoryRelationRecord,
	type MemoryScope,
	type MemoryStatus,
	type MemoryWriteResult,
	type SaveMemoryInput,
	type StatusTransitionRecord,
} from "../core/types.js";
import { initialStatus } from "../lifecycle/transitions.js";

/** Storage surface consumed by search and lifecycle modules. */
export interface MemoryStore {
	save(input: SaveMemoryInput): Promise<MemoryWriteResult>;
	findById(id: string): AccountingMemory | undefined;
	/** Latest revision of the (topicKey, exact scope) chain, if any. */
	findByTopicKey(
		topicKey: string,
		scope: MemoryScope,
	): AccountingMemory | undefined;
	/** Every stored memory whose scope equals the query scope. */
	findByScope(scope: MemoryScope): AccountingMemory[];
	/** Every stored memory (full revision history), insertion order. */
	list(): AccountingMemory[];
	/** Every stored memory with the given status. */
	listByStatus(status: MemoryStatus): AccountingMemory[];
	relate(
		fromId: string,
		toId: string,
		relation: MemoryRelation,
		meta?: { actor?: string; timestamp?: string },
	): void;
	/** Successor of a superseded memory (routes readers onward). */
	successorOf(memoryId: string): AccountingMemory | undefined;
	/** Status-only lifecycle mutation; records an audit-trail entry (v2). */
	applyStatusTransition(
		memoryId: string,
		to: MemoryStatus,
		meta: { actor: string; actorKind: string; timestamp: string },
	): AccountingMemory;
	relations(): MemoryRelationRecord[];
	transitionLog(): StatusTransitionRecord[];
}

/** In-memory implementation. Data is lost when the process exits. */
export class InMemoryMemoryStore implements MemoryStore {
	private readonly memories: AccountingMemory[] = [];
	private readonly chains = new Map<string, AccountingMemory[]>();
	private readonly byId = new Map<string, AccountingMemory>();
	private readonly relationRecords: MemoryRelationRecord[] = [];
	private readonly statusTransitions: StatusTransitionRecord[] = [];

	async save(input: SaveMemoryInput): Promise<MemoryWriteResult> {
		if (input.topicKey.trim().length === 0) {
			throw new Error("INVALID_TOPIC_KEY: topicKey must be a non-empty string");
		}
		if (input.title.trim().length === 0) {
			throw new Error("INVALID_TITLE: title must be a non-empty string");
		}
		assertValidScope(input.scope);
		assertValidContent(input.content);
		assertValidSource(input.source);
		assertValidValidity(input.validity);

		const chain = this.chain(input.topicKey, input.scope);
		const latest = chain[chain.length - 1];
		const recordedAt = new Date().toISOString();
		const effectiveAt = input.effectiveAt ?? recordedAt;

		const memory: AccountingMemory = {
			identity: { id: randomUUID(), topicKey: input.topicKey },
			title: input.title,
			kind: input.kind,
			scope: { ...input.scope },
			content: { ...input.content },
			// The approval gate: fiscalEffect != none → pending_review.
			status: initialStatus(input.fiscalEffect),
			fiscalEffect: input.fiscalEffect,
			effectiveAt,
			recordedAt,
			...(input.observedAt === undefined
				? {}
				: { observedAt: input.observedAt }),
			source: { ...input.source },
			...(input.validity === undefined
				? {}
				: { validity: { ...input.validity } }),
			...(input.ruleRefs === undefined
				? {}
				: { ruleRefs: [...input.ruleRefs] }),
			...(input.confidence === undefined
				? {}
				: { confidence: input.confidence }),
			...(input.materiality === undefined
				? {}
				: { materiality: input.materiality }),
			...(input.receiptId === undefined ? {} : { receiptId: input.receiptId }),
			contentHash: "",
			revision: latest === undefined ? 1 : latest.revision + 1,
		};
		memory.contentHash = await computeContentHash(memory);
		assertValidMemory(memory);

		// Immutable history: supersede the previous current revision of the chain.
		if (latest !== undefined) {
			latest.status = "superseded";
			latest.supersedesId = memory.identity.id;
			this.relationRecords.push({
				fromId: latest.identity.id,
				toId: memory.identity.id,
				relation: "supersedes",
				...(input.source.actorId === undefined
					? {}
					: { actor: input.source.actorId }),
				...(recordedAt === undefined ? {} : { timestamp: recordedAt }),
			});
		}

		try {
			chain.push(memory);
			this.memories.push(memory);
			this.byId.set(memory.identity.id, memory);
			return {
				memory: cloneMemory(memory),
				outcome: latest === undefined ? "created" : "updated",
			};
		} catch {
			// Documented fallback: an unexpected persistence failure yields
			// outcome "unknown" — the memory is NOT stored; the caller must
			// re-read state before acting on anything.
			return { memory: cloneMemory(memory), outcome: "unknown" };
		}
	}

	findById(id: string): AccountingMemory | undefined {
		const memory = this.byId.get(id);
		return memory === undefined ? undefined : cloneMemory(memory);
	}

	findByTopicKey(
		topicKey: string,
		scope: MemoryScope,
	): AccountingMemory | undefined {
		const chain = this.chains.get(chainKey(topicKey, scope));
		const latest = chain === undefined ? undefined : chain[chain.length - 1];
		return latest === undefined ? undefined : cloneMemory(latest);
	}

	findByScope(scope: MemoryScope): AccountingMemory[] {
		return this.memories
			.filter((memory) => scopeEquals(memory.scope, scope))
			.map(cloneMemory);
	}

	list(): AccountingMemory[] {
		return this.memories.map(cloneMemory);
	}

	listByStatus(status: MemoryStatus): AccountingMemory[] {
		return this.memories
			.filter((memory) => memory.status === status)
			.map(cloneMemory);
	}

	relate(
		fromId: string,
		toId: string,
		relation: MemoryRelation,
		meta?: { actor?: string; timestamp?: string },
	): void {
		if (fromId === toId) {
			throw new Error("INVALID_RELATION: a memory cannot relate to itself");
		}
		if (!this.byId.has(fromId)) {
			throw new Error(`MEMORY_NOT_FOUND: ${fromId}`);
		}
		if (!this.byId.has(toId)) {
			throw new Error(`MEMORY_NOT_FOUND: ${toId}`);
		}
		const alreadyRecorded = this.relationRecords.some(
			(record) =>
				record.fromId === fromId &&
				record.toId === toId &&
				record.relation === relation,
		);
		if (alreadyRecorded) return;
		this.relationRecords.push({
			fromId,
			toId,
			relation,
			...(meta?.actor === undefined ? {} : { actor: meta.actor }),
			...(meta?.timestamp === undefined ? {} : { timestamp: meta.timestamp }),
		});
	}

	successorOf(memoryId: string): AccountingMemory | undefined {
		const record = this.relationRecords.find(
			(candidate) =>
				candidate.fromId === memoryId && candidate.relation === "supersedes",
		);
		if (record === undefined) return undefined;
		const successor = this.byId.get(record.toId);
		return successor === undefined ? undefined : cloneMemory(successor);
	}

	applyStatusTransition(
		memoryId: string,
		to: MemoryStatus,
		meta: { actor: string; actorKind: string; timestamp: string },
	): AccountingMemory {
		const memory = this.byId.get(memoryId);
		if (memory === undefined) {
			throw new Error(`MEMORY_NOT_FOUND: ${memoryId}`);
		}
		const from = memory.status;
		// Status-only mutation: the single field the lifecycle machine may touch.
		memory.status = to;
		this.statusTransitions.push({
			memoryId,
			from,
			to,
			actor: meta.actor,
			actorKind: meta.actorKind as AccountingMemory["source"]["actorKind"],
			timestamp: meta.timestamp,
		});
		return cloneMemory(memory);
	}

	relations(): MemoryRelationRecord[] {
		return this.relationRecords.map((record) => ({ ...record }));
	}

	transitionLog(): StatusTransitionRecord[] {
		return this.statusTransitions.map((entry) => ({ ...entry }));
	}

	private chain(topicKey: string, scope: MemoryScope): AccountingMemory[] {
		const key = chainKey(topicKey, scope);
		let chain = this.chains.get(key);
		if (chain === undefined) {
			chain = [];
			this.chains.set(key, chain);
		}
		return chain;
	}
}

function chainKey(topicKey: string, scope: MemoryScope): string {
	return `${topicKey}\u0000${scopeKey(scope)}`;
}
