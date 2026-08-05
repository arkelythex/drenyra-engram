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
	type ConfirmJudgmentCommand,
	type ConfirmJudgmentResult,
	type FiscalEffect,
	type JudgmentStatus,
	type MemorySource,
	type MemoryStatus,
	type ProposeJudgmentCommand,
	type ProposeJudgmentResult,
	type RejectJudgmentCommand,
	type RejectJudgmentResult,
	type VerifiedApprovalPrincipal,
	type WithdrawJudgmentCommand,
	type WithdrawJudgmentResult,
	isProposableRelation,
} from "../core/types.js";
import type { ApprovalPolicyFn, JudgmentPolicyFn, JudgmentStore, MemoryStore } from "../store/memory-store.js";

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

    // ──────────────────────────────────────────────
    // Judgment lifecycle (v0.4.0 Step 2 — adjudicable conflicts)
    // ──────────────────────────────────────────────

    /**
     * Only an agent|system Source may propose (provenance-only — the proposal
     * Source records the claim, it never authorizes). Humans propose nothing:
     * their authority arrives as a verified principal at confirm/reject time.
     * Mirrors core.CanPropose.
     */
    export function canProposeJudgment(source: MemorySource): boolean {
    	return source.actorKind === "agent" || source.actorKind === "system";
    }

    /** Only a proposed judgment can be confirmed. Mirrors core.CanConfirm. */
    export function canConfirmJudgment(status: JudgmentStatus): boolean {
    	return status === "proposed";
    }

    /**
     * Only a proposed judgment can be rejected. Named canRejectJudgment because
     * the v2 memory lifecycle already owns canReject(MemoryStatus). Mirrors
     * core.CanRejectJudgment.
     */
    export function canRejectJudgment(status: JudgmentStatus): boolean {
    	return status === "proposed";
    }

    /** Only a proposed judgment can be withdrawn by its proposer. Mirrors core.CanWithdraw. */
    export function canWithdrawJudgment(status: JudgmentStatus): boolean {
    	return status === "proposed";
    }

    /**
     * Only a confirmed judgment can be superseded (a correction supersedes its
     * predecessor while atomically confirming). Mirrors core.CanSupersedeConfirmed.
     */
    export function canSupersedeConfirmed(status: JudgmentStatus): boolean {
    	return status === "confirmed";
    }

    /**
     * Adjacency table of the judgment machine, independent of any stored
     * judgment: proposed → confirmed|rejected|withdrawn|superseded; confirmed →
     * superseded ONLY; terminal states never re-open. Mirrors
     * core.IsLegalJudgmentTransition.
     */
    export function isLegalJudgmentTransition(
    	from: JudgmentStatus,
    	to: JudgmentStatus,
    ): boolean {
    	switch (from) {
    		case "proposed":
    			return (
    				to === "confirmed" ||
    				to === "rejected" ||
    				to === "withdrawn" ||
    				to === "superseded"
    			);
    		case "confirmed":
    			return to === "superseded";
    	}
    	return false;
    }

    /**
     * Propose a judgment (v0.4.0 Step 2) — mirror of server.ProposeJudgment.
     * Validates command syntax and proposer provenance (agent|system ONLY) then
     * delegates the WHOLE state change to the store's atomic proposal; there is
     * no read + mutate composition. The caller Source is provenance only and is
     * a SEPARATE argument — it never travels inside the command.
     */
    export async function proposeJudgment(
    	command: ProposeJudgmentCommand,
    	caller: MemorySource,
    	store: JudgmentStore,
    ): Promise<ProposeJudgmentResult> {
    	if (
    		command.fromId.trim().length === 0 ||
    		command.toId.trim().length === 0 ||
    		command.reason.trim().length === 0 ||
    		command.requestId.trim().length === 0
    	) {
    		throw new ApprovalError(
    			"MEMORY_NOT_FOUND",
    			"proposal command is incomplete (fromId, toId, reason and requestId are required)",
    		);
    	}
    	if (!isProposableRelation(command.relation)) {
    		throw new ApprovalError(
    			"RELATION_NOT_PROPOSABLE",
    			"relation is not proposable (supports|contradicts|explains|reconciles|reverses|supersedes only)",
    		);
    	}
    	if (command.fromId === command.toId) {
    		throw new ApprovalError(
    			"MEMORY_NOT_FOUND",
    			"a judgment requires two DISTINCT observations (fromId and toId must differ)",
    		);
    	}
    	if (!canProposeJudgment(caller)) {
    		throw new ApprovalError(
    			"PROPOSAL_UNAUTHORIZED",
    			"only agents and systems may propose judgments (provenance, never authority)",
    		);
    	}
    	return store.proposeJudgment(command, caller);
    }

    /**
     * Confirm a proposed judgment (v0.4.0 Step 2) — mirror of
     * server.ConfirmJudgment. The principal is ALWAYS a separate verified
     * argument; agent-confirm is IMPOSSIBLE by construction because the command
     * has no actor-kind and the principal is a branded factory product. The
     * store owns the whole authenticated act (hash comparison, policy, guarded
     * transition, event, correction supersession).
     */
    export async function confirmJudgment(
    	command: ConfirmJudgmentCommand,
    	principal: VerifiedApprovalPrincipal,
    	store: JudgmentStore,
    	policy?: JudgmentPolicyFn,
    ): Promise<ConfirmJudgmentResult> {
    	if (principal.subjectId.trim() === "") {
    		throw new ApprovalError(
    			"PRINCIPAL_INVALID",
    			"no verified approval principal present",
    		);
    	}
    	if (
    		command.judgmentId.trim().length === 0 ||
    		command.expectedJudgmentHash.trim().length === 0 ||
    		command.requestId.trim().length === 0
    	) {
    		throw new ApprovalError(
    			"JUDGMENT_NOT_FOUND",
    			"confirm command is incomplete (judgmentId, expectedJudgmentHash and requestId are required)",
    		);
    	}
    	if (command.resolution.trim().length === 0) {
    		throw new ApprovalError(
    			"RESOLUTION_REQUIRED",
    			"a non-empty professional resolution is required for confirmation",
    		);
    	}
    	return store.confirmJudgment(command, principal, policy);
    }

    /**
     * Reject a proposed judgment (v0.4.0 Step 2) — mirror of server.RejectJudgment.
     * The human reason is stored as the resolution; the proposal reason is never
     * silently promoted into a professional resolution.
     */
    export async function rejectJudgment(
    	command: RejectJudgmentCommand,
    	principal: VerifiedApprovalPrincipal,
    	store: JudgmentStore,
    	policy?: JudgmentPolicyFn,
    ): Promise<RejectJudgmentResult> {
    	if (principal.subjectId.trim() === "") {
    		throw new ApprovalError(
    			"PRINCIPAL_INVALID",
    			"no verified approval principal present",
    		);
    	}
    	if (
    		command.judgmentId.trim().length === 0 ||
    		command.expectedJudgmentHash.trim().length === 0 ||
    		command.requestId.trim().length === 0
    	) {
    		throw new ApprovalError(
    			"JUDGMENT_NOT_FOUND",
    			"reject command is incomplete (judgmentId, expectedJudgmentHash and requestId are required)",
    		);
    	}
    	if (command.reason.trim().length === 0) {
    		throw new ApprovalError(
    			"RESOLUTION_REQUIRED",
    			"a non-empty human reason is required for rejection",
    		);
    	}
    	return store.rejectJudgment(command, principal, policy);
    }

    /**
     * Withdraw the caller's OWN proposed judgment (v0.4.0 Step 2) — mirror of
     * server.WithdrawJudgment. The SAME exact proposer identity is required
     * (provenance continuity — never professional authorization); the store
     * enforces it inside the atomic withdrawal.
     */
    export async function withdrawJudgment(
    	command: WithdrawJudgmentCommand,
    	caller: MemorySource,
    	store: JudgmentStore,
    ): Promise<WithdrawJudgmentResult> {
    	if (command.judgmentId.trim().length === 0 || command.requestId.trim().length === 0) {
    		throw new ApprovalError(
    			"JUDGMENT_NOT_FOUND",
    			"withdraw command is incomplete (judgmentId and requestId are required)",
    		);
    	}
    	if (!canProposeJudgment(caller)) {
    		throw new ApprovalError(
    			"PROPOSAL_UNAUTHORIZED",
    			"only the proposing agent/system may withdraw",
    		);
    	}
    	return store.withdrawJudgment(command, caller);
    }
