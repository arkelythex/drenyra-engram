/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * In-memory memory store — immutable revision history, scope-partitioned.
 * Implements the storage surface of contracts/memory.md for the first vertical.
 *
 * Semantics:
 * - Upsert by (topicKey, exact scope): each save creates a NEW revision and
 *   NEVER edits a stored observation in place. History stays retrievable by id.
 * - A promoted observation is never rewritten; corrections are new revisions or
 *   explicit supersession via `supersede` (lifecycle/transitions.ts).
 * - `applyStatusTransition` is the single status-only mutation the lifecycle
 *   machine may perform; content/scope/provenance stay immutable. Legality of
 *   transitions is enforced by lifecycle/transitions.ts, not re-derived here.
 * - Write outcomes: `created` / `updated` on success; `unknown` is the
 *   documented fallback when an unexpected persistence error occurs — in that
 *   case the observation is NOT stored and callers must re-read state. Invalid
 *   input throws (deterministic caller errors fail fast, fail closed).
 * - `conflict` is reserved for a future optimistic-concurrency slice.
 */

import { randomUUID } from "node:crypto";

import {
  assertValidContent,
  assertValidProvenance,
  assertValidScope,
  assertValidValidity,
  cloneObservation,
  scopeEquals,
  scopeKey,
  type MemoryAuthorityStatus,
  type MemoryObservation,
  type MemoryRelation,
  type MemoryRelationRecord,
  type MemoryScope,
  type MemoryWriteResult,
  type SaveMemoryInput,
  type StatusTransitionRecord,
} from "../core/types.js";

/** Storage surface consumed by search and lifecycle modules. */
export interface MemoryStore {
  save(input: SaveMemoryInput): Promise<MemoryWriteResult>;
  findById(id: string): MemoryObservation | undefined;
  /** Latest revision of the (topicKey, exact scope) chain, if any. */
  findByTopicKey(topicKey: string, scope: MemoryScope): MemoryObservation | undefined;
  /** Every stored observation whose scope equals the query scope. */
  findByScope(scope: MemoryScope): MemoryObservation[];
  /** Every stored observation (full revision history), insertion order. */
  list(): MemoryObservation[];
  /** Every stored observation with the given authority status. */
  listByAuthority(status: MemoryAuthorityStatus): MemoryObservation[];
  relate(
    fromId: string,
    toId: string,
    relation: MemoryRelation,
    meta?: { actor?: string; timestamp?: string },
  ): void;
  /** Successor of a superseded observation (routes readers onward). */
  successorOf(observationId: string): MemoryObservation | undefined;
  /** Status-only lifecycle mutation; records an audit-trail entry. */
  applyStatusTransition(
    observationId: string,
    to: MemoryAuthorityStatus,
    meta: { actor: string; timestamp: string },
  ): MemoryObservation;
  relations(): MemoryRelationRecord[];
  transitionLog(): StatusTransitionRecord[];
}

/** In-memory implementation. Data is lost when the process exits. */
export class InMemoryMemoryStore implements MemoryStore {
  private readonly observations: MemoryObservation[] = [];
  private readonly chains = new Map<string, MemoryObservation[]>();
  private readonly byId = new Map<string, MemoryObservation>();
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
    assertValidProvenance(input.provenance);
    assertValidValidity(input.validity);

    const chain = this.chain(input.topicKey, input.scope);
    const latest = chain[chain.length - 1];
    const observation: MemoryObservation = {
      identity: { id: randomUUID(), topicKey: input.topicKey },
      title: input.title,
      type: input.type,
      scope: { ...input.scope },
      content: { ...input.content },
      authorityStatus: input.authorityStatus ?? "draft",
      ...(input.validity === undefined ? {} : { validity: { ...input.validity } }),
      provenance: { ...input.provenance },
      revision: latest === undefined ? 1 : latest.revision + 1,
    };

    try {
      chain.push(observation);
      this.observations.push(observation);
      this.byId.set(observation.identity.id, observation);
      return {
        observation: cloneObservation(observation),
        outcome: latest === undefined ? "created" : "updated",
      };
    } catch {
      // Documented fallback: an unexpected persistence failure yields
      // outcome "unknown" — the observation is NOT stored; the caller must
      // re-read state before acting on anything.
      return { observation: cloneObservation(observation), outcome: "unknown" };
    }
  }

  findById(id: string): MemoryObservation | undefined {
    const observation = this.byId.get(id);
    return observation === undefined ? undefined : cloneObservation(observation);
  }

  findByTopicKey(topicKey: string, scope: MemoryScope): MemoryObservation | undefined {
    const chain = this.chains.get(chainKey(topicKey, scope));
    const latest = chain === undefined ? undefined : chain[chain.length - 1];
    return latest === undefined ? undefined : cloneObservation(latest);
  }

  findByScope(scope: MemoryScope): MemoryObservation[] {
    return this.observations
      .filter((observation) => scopeEquals(observation.scope, scope))
      .map(cloneObservation);
  }

  list(): MemoryObservation[] {
    return this.observations.map(cloneObservation);
  }

  listByAuthority(status: MemoryAuthorityStatus): MemoryObservation[] {
    return this.observations
      .filter((observation) => observation.authorityStatus === status)
      .map(cloneObservation);
  }

  relate(
    fromId: string,
    toId: string,
    relation: MemoryRelation,
    meta?: { actor?: string; timestamp?: string },
  ): void {
    if (fromId === toId) {
      throw new Error("INVALID_RELATION: an observation cannot relate to itself");
    }
    if (!this.byId.has(fromId)) {
      throw new Error(`OBSERVATION_NOT_FOUND: ${fromId}`);
    }
    if (!this.byId.has(toId)) {
      throw new Error(`OBSERVATION_NOT_FOUND: ${toId}`);
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

  successorOf(observationId: string): MemoryObservation | undefined {
    const record = this.relationRecords.find(
      (candidate) =>
        candidate.fromId === observationId && candidate.relation === "supersedes",
    );
    if (record === undefined) return undefined;
    const successor = this.byId.get(record.toId);
    return successor === undefined ? undefined : cloneObservation(successor);
  }

  applyStatusTransition(
    observationId: string,
    to: MemoryAuthorityStatus,
    meta: { actor: string; timestamp: string },
  ): MemoryObservation {
    const observation = this.byId.get(observationId);
    if (observation === undefined) {
      throw new Error(`OBSERVATION_NOT_FOUND: ${observationId}`);
    }
    const from = observation.authorityStatus;
    // Status-only mutation: the single field the lifecycle machine may touch.
    // Content, scope, provenance and revision remain immutable after creation.
    observation.authorityStatus = to;
    this.statusTransitions.push({
      observationId,
      from,
      to,
      actor: meta.actor,
      timestamp: meta.timestamp,
    });
    return cloneObservation(observation);
  }

  relations(): MemoryRelationRecord[] {
    return this.relationRecords.map((record) => ({ ...record }));
  }

  transitionLog(): StatusTransitionRecord[] {
    return this.statusTransitions.map((entry) => ({ ...entry }));
  }

  private chain(topicKey: string, scope: MemoryScope): MemoryObservation[] {
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
