/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Observation lifecycle (contracts/lifecycle.md) and vigencia enforcement.
 *
 * Legal state machine for this slice — ONLY adjacent forward transitions:
 *
 *   draft ──► reviewed ──► promoted ──► superseded
 *
 * Anything else throws `INVALID_TRANSITION`. Note this is deliberately STRICTER
 * than the 0.1-draft contract table (which also lists promote-from-draft):
 * unknown or non-adjacent transitions fail closed, and re-submission is always a
 * new revision, never an in-place edit.
 *
 * `supersede` requires a target observation id and records a `supersedes`
 * relation on the store (from the superseded observation to its replacement),
 * marking the superseded observation's status. It never auto-promotes the
 * replacement — promotion is explicit (contracts/lifecycle.md rule 1).
 */

import type { MemoryAuthorityStatus, MemoryObservation } from "../core/types.js";
import type { MemoryStore } from "../store/memory-store.js";

export const INVALID_TRANSITION_ERROR = "INVALID_TRANSITION";
export const INVALID_SUPERSEDE_ERROR = "INVALID_SUPERSEDE_TARGET";

const LEGAL_TRANSITIONS: ReadonlyMap<MemoryAuthorityStatus, MemoryAuthorityStatus> =
  new Map<MemoryAuthorityStatus, MemoryAuthorityStatus>([
    ["draft", "reviewed"],
    ["reviewed", "promoted"],
    ["promoted", "superseded"],
  ]);

/** True only for adjacent forward transitions in the legal chain. */
export function isLegalTransition(
  from: MemoryAuthorityStatus,
  to: MemoryAuthorityStatus,
): boolean {
  return LEGAL_TRANSITIONS.get(from) === to;
}

/** Guard: throws `INVALID_TRANSITION` for anything outside the legal chain. */
export function transitionAuthority(
  from: MemoryAuthorityStatus,
  to: MemoryAuthorityStatus,
): void {
  if (!isLegalTransition(from, to)) {
    throw new Error(
      `${INVALID_TRANSITION_ERROR}: ${from} → ${to} is not legal — the only legal chain is draft → reviewed → promoted → superseded`,
    );
  }
}

export interface TransitionMeta {
  actor: string;
  timestamp: string;
}

/**
 * Apply a legal status transition to a stored observation, recording an
 * audit-trail entry (provenance.md rule 3: every state traces to actor+time).
 */
export function applyTransition(
  store: MemoryStore,
  observationId: string,
  to: MemoryAuthorityStatus,
  meta: TransitionMeta,
): MemoryObservation {
  const observation = store.findById(observationId);
  if (observation === undefined) {
    throw new Error(`OBSERVATION_NOT_FOUND: ${observationId}`);
  }
  transitionAuthority(observation.authorityStatus, to);
  return store.applyStatusTransition(observationId, to, meta);
}

export interface SupersedeInput extends TransitionMeta {
  store: MemoryStore;
  /** The observation being superseded (must be promoted). */
  observationId: string;
  /** REQUIRED target: the replacing observation this one routes readers to. */
  targetId: string;
}

/**
 * Supersede a promoted observation: marks it `superseded` and records a
 * `supersedes` relation to the target. Never edits content in place.
 */
export function supersede(input: SupersedeInput): MemoryObservation {
  const { store, observationId, targetId, actor, timestamp } = input;
  if (observationId === targetId) {
    throw new Error(
      `${INVALID_SUPERSEDE_ERROR}: observation ${observationId} cannot supersede itself`,
    );
  }
  const target = store.findById(observationId);
  if (target === undefined) {
    throw new Error(`OBSERVATION_NOT_FOUND: ${observationId}`);
  }
  const replacement = store.findById(targetId);
  if (replacement === undefined) {
    throw new Error(`OBSERVATION_NOT_FOUND: ${targetId}`);
  }
  transitionAuthority(target.authorityStatus, "superseded");
  const updated = store.applyStatusTransition(observationId, "superseded", {
    actor,
    timestamp,
  });
  store.relate(observationId, targetId, "supersedes", { actor, timestamp });
  return updated;
}
