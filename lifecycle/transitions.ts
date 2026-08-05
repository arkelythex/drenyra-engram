/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Memory lifecycle v2 (contracts/lifecycle.md) and vigencia enforcement.
 *
 * Legal v2 state machine (approval-gated):
 *
 *   save (fiscalEffect == none)  → active             (informative, current)
 *   save (fiscalEffect != none)  → pending_review     (GATE: human approval)
 *   pending_review ──approve(human)──► approved
 *   pending_review ──reject(human)───► rejected       (terminal)
 *   active|pending_review|approved ──void(human|system)──► voided (terminal)
 *   active|pending_review|approved ──supersede──► superseded   (terminal)
 *
 * GATE semantics: a memory with fiscal effect can only reach `approved` through
 * a HUMAN actor (source.actorKind == human). Agents and systems record; they
 * never approve. `rejected`, `superseded` and `voided` are terminal.
 *
 * "La IA asiste; el sistema valida; el profesional revisa; la evidencia
 * permanece." Memory informs decisions — it never authorizes business actions.
 */

import {
	ApprovalError,
	type AccountingMemory,
	type ActorKind,
	type ApprovalResult,
	type ApproveMemoryCommand,
	type FiscalEffect,
	type MemoryStatus,
	type VerifiedApprovalPrincipal,
} from "../core/types.js";
import type { ApprovalPolicyFn, MemoryStore } from "../store/memory-store.js";

export const INVALID_TRANSITION_ERROR = "INVALID_TRANSITION";
export const GATE_REQUIRES_HUMAN_ERROR = "GATE_REQUIRES_HUMAN";

/** Save-time status derived from the fiscal effect (the approval gate). */
export function initialStatus(effect: FiscalEffect): MemoryStatus {
	return effect === "none" ? "active" : "pending_review";
}

/** True when the fiscal effect triggers the human-approval gate. */
export function isGated(effect: FiscalEffect): boolean {
	return effect !== "none";
}

/** Only a pending_review memory can be approved. */
export function canApprove(status: MemoryStatus): boolean {
	return status === "pending_review";
}

/** Only a pending_review memory can be rejected. */
export function canReject(status: MemoryStatus): boolean {
	return status === "pending_review";
}

/** active | pending_review | approved can be voided. */
export function canVoid(status: MemoryStatus): boolean {
	return (
		status === "active" || status === "pending_review" || status === "approved"
	);
}

/** Fail-closed: approval requires a human actor. */
export function assertHumanApproval(actorKind: ActorKind): void {
	if (actorKind !== "human") {
		throw new Error(
			`${GATE_REQUIRES_HUMAN_ERROR}: approve/reject requires a human actor (source.actorKind == human)`,
		);
	}
}

export interface TransitionMeta {
	actor: string;
	actorKind: ActorKind;
	timestamp: string;
}

/**
 * Approve a pending_review memory. REQUIRES a human actor; fails closed with
 * GATE_REQUIRES_HUMAN otherwise.
 */
export function approve(memory: AccountingMemory, meta: TransitionMeta): void {
	if (!canApprove(memory.status)) {
		throw new Error(
			`${INVALID_TRANSITION_ERROR}: ${memory.identity.id} → approved is not legal from status "${memory.status}"`,
		);
	}
	assertHumanApproval(meta.actorKind);
}

/**
 * Reject a pending_review memory (terminal). REQUIRES a human actor.
 */
export function reject(memory: AccountingMemory, meta: TransitionMeta): void {
	if (!canReject(memory.status)) {
		throw new Error(
			`${INVALID_TRANSITION_ERROR}: ${memory.identity.id} → rejected is not legal from status "${memory.status}"`,
		);
	}
	assertHumanApproval(meta.actorKind);
}

/**
 * Void an active | pending_review | approved memory (terminal, no successor).
 * Admits human or system actors (systemic correction), NEVER an agent.
 */
export function voidMemory(
	memory: AccountingMemory,
	meta: TransitionMeta,
): void {
	if (!canVoid(memory.status)) {
		throw new Error(
			`${INVALID_TRANSITION_ERROR}: ${memory.identity.id} → voided is not legal from status "${memory.status}"`,
		);
	}
	if (meta.actorKind === "agent") {
		throw new Error(
			"GATE_AGENT_CANNOT_VOID: voiding requires a human or system actor, never an agent",
		);
	}
}

/**
 * Mark a previously current memory superseded, routing readers to successorId.
 * Only active | pending_review | approved memories can be superseded. Returns
 * the superseded state (immutable mirror of core.SupersedePrev mutation).
 */
export function supersedePrev(
	memory: AccountingMemory,
	successorId: string,
): AccountingMemory {
	if (!canVoid(memory.status)) {
		throw new Error(
			`${INVALID_TRANSITION_ERROR}: ${memory.identity.id} → superseded is not legal from status "${memory.status}"`,
		);
	}
	return { ...memory, status: "superseded", supersedesId: successorId };
}

/**
 * Authenticated atomic approval (v0.4.0 Step 1, ADR-003) — mirror of
 * server.ApproveMemory. The principal is ALWAYS a separate verified argument
 * (auth/principal.ts factory), never part of the command: the transport
 * payload can never declare authority. This function validates command
 * syntax and delegates the whole state change to the store's atomic
 * approval; authorization (scope, role, assurance, materiality) belongs to
 * the pure policy, not here.
 */
export async function approveMemory(
command: ApproveMemoryCommand,
principal: VerifiedApprovalPrincipal,
store: MemoryStore,
policy?: ApprovalPolicyFn,
): Promise<ApprovalResult> {
// A zero principal cannot approve anything (mirror of the service's fail-closed
// guard before the policy could misreport it). The factory always mints a
// fully-validated principal, so an empty subject is the only observable zero
// value in the mirror.
if (principal.subjectId.trim() === "") {
throw new ApprovalError(
"PRINCIPAL_INVALID",
"no verified approval principal present",
);
}
// Command syntax — the frozen transport mapping treats a malformed
// command as a not-found/identity failure, never an authorization
// decision.
if (command.memoryId.trim() === "") {
throw new ApprovalError("MEMORY_NOT_FOUND", "memoryId is required");
}
if (command.expectedEnvelopeHash.trim() === "") {
throw new ApprovalError(
"MEMORY_NOT_FOUND",
"expectedEnvelopeHash is required",
);
}
if (command.requestId.trim() === "") {
throw new ApprovalError(
"MEMORY_NOT_FOUND",
"requestId (idempotency key) is required",
);
}
if (command.reason.trim() === "") {
throw new ApprovalError(
"REASON_REQUIRED",
"a reason is required for approval",
);
}
return store.approveMemory(command, principal, policy);
}
    
/**
 * Apply a gated status transition (approve/reject/void) to a stored memory,
 * recording an audit-trail entry. The store applies the transition; legality
 * and the human gate are checked here (mirror of core.Approve/Reject/Void).
 */
export function applyGateTransition(
	store: MemoryStore,
	memoryId: string,
	to: MemoryStatus,
	meta: TransitionMeta,
): AccountingMemory {
	const memory = store.findById(memoryId);
	if (memory === undefined) {
		throw new Error(`MEMORY_NOT_FOUND: ${memoryId}`);
	}
	if (to === "approved") approve(memory, meta);
	else if (to === "rejected") reject(memory, meta);
	else if (to === "voided") voidMemory(memory, meta);
	return store.applyStatusTransition(memoryId, to, meta);
}
