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

import { randomUUID, createHash } from "node:crypto";

import {
	ApprovalError,
	assertValidContent,
	assertValidMemory,
	assertValidScope,
	assertValidSource,
	assertValidValidity,
	cloneMemory,
	computeContentHash,
	computeEnvelopeHash,
	computeIdentityHash,
	computeJudgmentHash,
	isProposableRelation,
	scopeEquals,
	scopeKey,
	type AccountingJudgment,
	type AccountingMemory,
	type ApprovalAuthorizationDecision,
	type ApprovalEvent,
	type ApprovalResult,
	type ApproveMemoryCommand,
	type ConfirmJudgmentCommand,
	type ConfirmJudgmentResult,
	type JudgmentEvent,
	type MemoryRelation,
	type MemoryRelationRecord,
	type MemoryScope,
	type MemorySource,
	type MemoryStatus,
	type MemoryWriteResult,
	type ProposeJudgmentCommand,
	type ProposeJudgmentResult,
	type RejectJudgmentCommand,
	type RejectJudgmentResult,
	type SaveMemoryInput,
	type StatusTransitionRecord,
	type VerifiedApprovalPrincipal,
	type WithdrawJudgmentCommand,
	type WithdrawJudgmentResult,
} from "../core/types.js";
import {
	canProposeJudgment,
	canVoid,
	initialStatus,
} from "../lifecycle/transitions.js";
    import { principalSnapshot } from "../auth/principal.js";
    import { authorizeApproval } from "../authz/approval-policy.js";
    import {
    	authorizeJudgment,
    	type JudgmentAuthorizationDecision,
    } from "../authz/judgment-policy.js";

    /**
     * Pure authorization function passed to the atomic approval (mirror of the
     * authz.ApprovalAuthorizationPolicy interface). Defaults to the frozen
     * v0.4.0 policy.
     */
    export type ApprovalPolicyFn = (
    	principal: VerifiedApprovalPrincipal,
    	memory: AccountingMemory,
    ) => ApprovalAuthorizationDecision;

    /**
     * Pure judgment authorization function passed to the atomic decisions (mirror
     * of the authz.JudgmentAuthorizationPolicy interface). Defaults to the frozen
     * v0.4.0 policy.
     */
    export type JudgmentPolicyFn = (
    	principal: VerifiedApprovalPrincipal,
    	judgment: AccountingJudgment,
    ) => JudgmentAuthorizationDecision;

    /**
     * The judgment surface a store must expose (v0.4.0 Step 2). Every operation
     * is ONE atomic transition — propose/confirm/reject/withdraw — with no
     * read + mutate composition (mirror of the SQLiteStore BEGIN IMMEDIATE
     * contract). Agents can never confirm/reject: those signatures REQUIRE a
     * VerifiedApprovalPrincipal (a branded factory product).
     */
    export interface JudgmentStore {
    	/**
    	 * Atomically propose a judgment over two observations. The caller Source
    	 * is provenance-only (agent|system), never authority.
    	 */
    	proposeJudgment(
    		command: ProposeJudgmentCommand,
    		caller: MemorySource,
    	): Promise<ProposeJudgmentResult>;
    	/**
    	 * Atomically confirm a proposed judgment: idempotency reservation →
    	 * locked re-read → status gate → fresh hash vs expected → pure policy →
    	 * guarded transition + immutable event (+ correction supersession).
    	 */
    	confirmJudgment(
    		command: ConfirmJudgmentCommand,
    		principal: VerifiedApprovalPrincipal,
    		policy?: JudgmentPolicyFn,
    	): Promise<ConfirmJudgmentResult>;
    	/** Same atomic path as confirmation, storing the human reason as the resolution. */
    	rejectJudgment(
    		command: RejectJudgmentCommand,
    		principal: VerifiedApprovalPrincipal,
    		policy?: JudgmentPolicyFn,
    	): Promise<RejectJudgmentResult>;
    	/**
    	 * Atomically withdraw the caller's OWN proposed judgment (same exact
    	 * proposer identity; provenance continuity, never authority).
    	 */
    	withdrawJudgment(
    		command: WithdrawJudgmentCommand,
    		caller: MemorySource,
    	): Promise<WithdrawJudgmentResult>;
    	/** Every stored judgment (map insertion order). */
    	judgments(): AccountingJudgment[];
    	/** Immutable judgment transition events (confirm/reject/withdraw/supersede). */
    	judgmentEvents(): JudgmentEvent[];
    	/** Successor of a superseded judgment (routes readers onward). */
    	judgmentSuccessorOf(judgmentId: string): AccountingJudgment | undefined;
    }


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
	/**
	 * Atomic authenticated approval (v0.4.0 Step 1, ADR-003): the principal is
	 * ALWAYS a separate verified argument, never part of the command. Mirrors
	 * store.SQLiteStore.ApproveMemory: idempotency reservation → locked re-read
	 * → scope/status checks → fresh H1 vs expected → pure policy → guarded
	 * status flip + H2 → immutable approval event → completed reservation.
	 */
	approveMemory(
		command: ApproveMemoryCommand,
		principal: VerifiedApprovalPrincipal,
		policy?: ApprovalPolicyFn,
	): Promise<ApprovalResult>;
	relations(): MemoryRelationRecord[];
	transitionLog(): StatusTransitionRecord[];
	/** Immutable authenticated approval events (v0.4.0 Step 1). */
	approvalEvents(): ApprovalEvent[];
}

/** In-memory implementation. Data is lost when the process exits. */
export class InMemoryMemoryStore implements MemoryStore {
	private readonly memories: AccountingMemory[] = [];
	private readonly chains = new Map<string, AccountingMemory[]>();
	private readonly byId = new Map<string, AccountingMemory>();
	private readonly relationRecords: MemoryRelationRecord[] = [];
	private readonly statusTransitions: StatusTransitionRecord[] = [];
	private readonly approvalEventRecords: ApprovalEvent[] = [];
	private readonly idempotency = new Map<
		string,
		{
			commandHash: string;
			principalSubjectId: string;
			membershipId: string;
			result?: ApprovalResult;
			completedAt?: string;
		}
	>();
	// ── v0.4.0 Step 2 judgment state (mirror of the judgments / judgment_events /
	// judgment_idempotency_keys / judgment_relations tables). ──
	private readonly judgmentsById = new Map<string, AccountingJudgment>();
	private readonly judgmentEventRecords: JudgmentEvent[] = [];
	private readonly judgmentIdempotency = new Map<
		string,
		{
			commandHash: string;
			binding: string;
			judgmentId: string;
			result?: { judgmentId: string; judgmentEventId: string };
			completedAt?: string;
		}
	>();
	private readonly judgmentRelations: {
		fromJudgmentId: string;
		toJudgmentId: string;
		relation: "supersedes";
		actor?: string;
		timestamp: string;
	}[] = [];

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
				: {
						validity: {
							...input.validity,
							// The vigencia of a written v2 memory is declared (frozen
							// decision): migrated rows carry migrated_from_effective_at_v1.
							source: input.validity.source ?? "declared",
						},
					}),
			...(input.ruleRefs === undefined
				? {}
				: { ruleRefs: [...input.ruleRefs] }),
			...(input.confidence === undefined
				? {}
				: { confidence: input.confidence }),
			...(input.materiality === undefined
				? {}
				: { materiality: input.materiality }),
			...(input.materialityLevel === undefined
				? {}
				: { materialityLevel: input.materialityLevel }),
			...(input.receiptId === undefined ? {} : { receiptId: input.receiptId }),
			contentHash: "",
			revision: latest === undefined ? 1 : latest.revision + 1,
		};
		memory.contentHash = await computeContentHash(memory);
		memory.identityHash = await computeIdentityHash(memory);
		memory.envelopeHash = await computeEnvelopeHash(memory);
		assertValidMemory(memory);

		// Immutable history: supersede the previous current revision of the chain
		// ONLY when it is supersedeable (active | pending_review | approved — the
		// canVoid guard). Terminal heads (rejected, superseded, voided) NEVER
		// reopen; the new revision does NOT inherit the previous approval (a
		// fiscal effect lands it pending_review behind the human gate).
		if (latest !== undefined && canVoid(latest.status)) {
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

	approvalEvents(): ApprovalEvent[] {
		return this.approvalEventRecords.map((event) => ({ ...event }));
	}

	/**
	 * Atomic authenticated approval (mirror of SQLiteStore.ApproveMemory). The
	 * in-memory store is a semantic mirror, not a concurrency proof: the
	 * single-threaded critical section is the contiguous final block (mutate +
	 * record + complete reservation) after every await, so a throw before it
	 * leaves no partial state (rollback of the incomplete reservation).
	 */
	async approveMemory(
		command: ApproveMemoryCommand,
		principal: VerifiedApprovalPrincipal,
		policy: ApprovalPolicyFn = authorizeApproval,
	): Promise<ApprovalResult> {
		// Syntax guards (defense in depth — the transitions orchestrator validates
		// first). Mirrors the store's fail-closed guards.
		if (command.reason.trim().length === 0) {
			throw new ApprovalError(
				"REASON_REQUIRED",
				"a reason is required for approval",
			);
		}
		if (
			command.memoryId.trim().length === 0 ||
			command.expectedEnvelopeHash.trim().length === 0 ||
			command.requestId.trim().length === 0
		) {
			throw new ApprovalError(
				"MEMORY_NOT_FOUND",
				"approval command is incomplete (memoryId, expectedEnvelopeHash and requestId are required)",
			);
		}

		const now = new Date().toISOString();
		const commandHash = approveCommandHash(
			command.memoryId,
			command.expectedEnvelopeHash,
			command.reason,
		);
		// Idempotency scope: one reservation per (tenant, requestId).
		const key = `${principal.tenantId}\u0000${command.requestId}`;
		const existing = this.idempotency.get(key);
		if (existing !== undefined) {
			// The reservation exists: the command AND the principal binding must
			// match exactly, else the request id was reused for a different intent.
			if (
				existing.commandHash !== commandHash ||
				existing.principalSubjectId !== principal.subjectId ||
				existing.membershipId !== principal.membershipId
			) {
				throw new ApprovalError(
					"IDEMPOTENCY_CONFLICT",
					"request id already used with a different command or principal",
				);
			}
			if (existing.completedAt !== undefined && existing.result !== undefined) {
				// Completed replay: return the stored result marked as a replay.
				return { ...existing.result, idempotentReplay: true };
			}
			// Incomplete reservation (an interrupted attempt): reuse it — the
			// memory re-check below decides ALREADY_DECIDED when the memory was
			// decided by another request.
		} else {
			// Reserve: result/completion stay unset until the approval commits.
			this.idempotency.set(key, {
				commandHash,
				principalSubjectId: principal.subjectId,
				membershipId: principal.membershipId,
			});
		}

		try {
			// Locked re-read of the memory (the mirror has a single byId map; the
			// Go store re-reads through the locked connection).
			const memory = this.byId.get(command.memoryId);
			if (memory === undefined) {
				throw new ApprovalError(
					"MEMORY_NOT_FOUND",
					`memory not found: ${command.memoryId}`,
				);
			}

			// Scope checks (mirror of the store's locked transaction).
			if (memory.scope.kind !== "company") {
				throw new ApprovalError(
					"COMPANY_SCOPE_DENIED",
					"institutional memories cannot be approved by a company-scoped principal",
				);
			}
			if (principal.tenantId !== memory.scope.organizationId) {
				throw new ApprovalError(
					"TENANT_SCOPE_MISMATCH",
					"principal tenant does not match the memory tenant",
				);
			}
			if (!principal.companyScopes.includes(memory.scope.companyId)) {
				throw new ApprovalError(
					"COMPANY_SCOPE_DENIED",
					"company is outside the principal's scope",
				);
			}

			// Status gate: only pending_review can be approved.
			if (memory.status === "approved" || memory.status === "rejected") {
				throw new ApprovalError("ALREADY_DECIDED", "memory is already decided");
			}
			if (memory.status !== "pending_review") {
				throw new ApprovalError(
					"INVALID_TRANSITION",
					`approval is not legal from status "${memory.status}"`,
				);
			}

			// H1 recomputed FRESH from the current row — never from a stale cached
			// envelope. A mismatch carries ONLY the two hashes, never content.
			const h1 = await computeEnvelopeHash(memory);
			if (
				command.expectedEnvelopeHash.trim().toLowerCase() !== h1.toLowerCase()
			) {
				throw new ApprovalError(
					"ENVELOPE_MISMATCH",
					"memory envelope changed after review; expected hash does not match the current envelope",
					{
						expectedEnvelopeHash: command.expectedEnvelopeHash,
						actualEnvelopeHash: h1,
					},
				);
			}

			// Pure policy in the critical section (mirror of the in-transaction
			// policy run). Any denial fails the approval.
			const decision = policy(principal, memory);
			if (!decision.allowed) {
				throw new ApprovalError(
					decision.reasonCode as ApprovalError["code"],
					"authorization policy denied the approval",
				);
			}

			// H2 from the approved snapshot; status participates in the envelope
			// hash so it must differ from H1.
			const approved = cloneMemory(memory);
			approved.status = "approved";
			const h2 = await computeEnvelopeHash(approved);
			if (h2 === h1) {
				throw new ApprovalError(
					"INVALID_TRANSITION",
					"resulting envelope equals reviewed envelope — status change did not affect the hash",
				);
			}

			// ── Atomic final block: mutate + record + complete the reservation. ──
			// No awaits after this point: a throw below cannot happen, and a throw
			// above already rolled back the reservation in the catch handler.
			const event: ApprovalEvent = {
				id: randomUUID(),
				requestId: command.requestId,
				memoryId: memory.identity.id,
				tenantId: memory.scope.organizationId,
				companyId: memory.scope.companyId,
				...(memory.scope.period === undefined || memory.scope.period === ""
					? {}
					: { fiscalPeriodId: memory.scope.period }),
				action: "approved",
				fromStatus: "pending_review",
				toStatus: "approved",
				reviewedEnvelopeHash: h1,
				resultingEnvelopeHash: h2,
				reason: command.reason,
				principalSnapshot: principalSnapshot(principal),
				policyVersion: decision.policyVersion,
				authorizationReasonCode: "AUTHORIZED",
				createdAt: now,
			};
			memory.status = "approved";
			memory.envelopeHash = h2;
			this.approvalEventRecords.push(event);
			this.statusTransitions.push({
				memoryId: memory.identity.id,
				from: "pending_review",
				to: "approved",
				actor: principal.subjectId,
				actorKind: "human",
				timestamp: now,
			});
			const result: ApprovalResult = {
				memoryId: memory.identity.id,
				approvalEventId: event.id,
				previousStatus: "pending_review",
				currentStatus: "approved",
				reviewedEnvelopeHash: h1,
				resultingEnvelopeHash: h2,
				principalSubjectId: principal.subjectId,
				membershipId: principal.membershipId,
				policyVersion: decision.policyVersion,
				approvedAt: now,
				idempotentReplay: false,
			};
			this.idempotency.set(key, {
				commandHash,
				principalSubjectId: principal.subjectId,
				membershipId: principal.membershipId,
				result,
				completedAt: now,
			});
			return result;
		} catch (error) {
			// Rollback of the incomplete reservation (mirror of the transaction
			// rollback); a completed reservation is never removed.
			const current = this.idempotency.get(key);
			if (current !== undefined && current.completedAt === undefined) {
				this.idempotency.delete(key);
			}
			throw error;
		}
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

	// ──────────────────────────────────────────────
	// v0.4.0 Step 2 — judgment critical section
	// ──────────────────────────────────────────────

	/** Every stored judgment (map insertion order). */
	judgments(): AccountingJudgment[] {
		return [...this.judgmentsById.values()].map((judgment) => ({
			...judgment,
		}));
	}

	/** Immutable judgment transition events (confirm/reject/withdraw/supersede). */
	judgmentEvents(): JudgmentEvent[] {
		return this.judgmentEventRecords.map((event) => ({ ...event }));
	}

	/** Successor of a superseded judgment (routes readers onward). */
	judgmentSuccessorOf(judgmentId: string): AccountingJudgment | undefined {
		const record = this.judgmentRelations.find(
			(candidate) =>
				candidate.fromJudgmentId === judgmentId &&
				candidate.relation === "supersedes",
		);
		if (record === undefined) return undefined;
		const successor = this.judgmentsById.get(record.toJudgmentId);
		return successor === undefined ? undefined : { ...successor };
	}

	/**
	 * Atomically propose a judgment over two existing observations (mirror of
	 * SQLiteStore.ProposeJudgment). The caller Source is provenance-only
	 * (agent|system); tenant/company are derived from the observations, never
	 * from caller claims. A proposal writes NO judgment event; the reservation
	 * completes with the created judgment id so a same-request retry replays
	 * THAT exact row.
	 */
	async proposeJudgment(
		command: ProposeJudgmentCommand,
		caller: MemorySource,
	): Promise<ProposeJudgmentResult> {
		// Syntax guards (defense in depth — the transitions orchestrator validates
		// first). Mirrors the store's fail-closed guards.
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
		if (!canProposeJudgment(caller)) {
			throw new ApprovalError(
				"PROPOSAL_UNAUTHORIZED",
				"only agents and systems may propose judgments (provenance, never authority)",
			);
		}
		if (command.fromId === command.toId) {
			throw new ApprovalError(
				"MEMORY_NOT_FOUND",
				"a judgment requires two DISTINCT observations (fromId and toId must differ)",
			);
		}

		const from = this.byId.get(command.fromId);
		const to = this.byId.get(command.toId);
		if (from === undefined || to === undefined) {
			throw new ApprovalError(
				"MEMORY_NOT_FOUND",
				"a judgment requires two existing observations",
			);
		}
		if (from.scope.kind !== "company" || to.scope.kind !== "company") {
			throw new ApprovalError(
				"COMPANY_SCOPE_DENIED",
				"institutional observations have no company to adjudicate",
			);
		}
		if (from.scope.organizationId !== to.scope.organizationId) {
			throw new ApprovalError(
				"TENANT_SCOPE_MISMATCH",
				"judgment observations must belong to the same tenant",
			);
		}
		if (from.scope.companyId !== to.scope.companyId) {
			throw new ApprovalError(
				"COMPANY_SCOPE_DENIED",
				"judgment observations must belong to the same company",
			);
		}
		const tenantId = from.scope.organizationId;
		const companyId = from.scope.companyId;
		const fiscalPeriodId =
			from.scope.period !== undefined &&
			from.scope.period !== "" &&
			from.scope.period === to.scope.period
				? from.scope.period
				: undefined;

		const id = randomUUID();
		const now = new Date().toISOString();
		const commandHash = judgmentProposeCommandHash(command);
		const binding = judgmentProposerBinding(caller);
		const key = `${tenantId}\u0000${command.requestId}`;
		const existing = this.judgmentIdempotency.get(key);
		if (existing !== undefined) {
			if (existing.commandHash !== commandHash || existing.binding !== binding) {
				throw new ApprovalError(
					"IDEMPOTENCY_CONFLICT",
					"request id already used with a different proposal or proposer",
				);
			}
			if (existing.completedAt !== undefined) {
				// Replay: return the ORIGINAL proposal the reservation created.
				const original = this.judgmentsById.get(existing.judgmentId);
				if (original !== undefined) {
					return {
						judgmentId: original.id,
						judgment: { ...original },
						idempotentReplay: true,
					};
				}
				const open = this.openJudgmentForTuple(
					tenantId,
					companyId,
					command.fromId,
					command.toId,
					command.relation,
				);
				if (open !== undefined) {
					return {
						judgmentId: open.id,
						judgment: { ...open },
						idempotentReplay: true,
					};
				}
				throw new ApprovalError(
					"JUDGMENT_NOT_FOUND",
					"proposal reservation completed but no judgment row found for the tuple",
				);
			}
		} else {
			// Reserve: completion stays unset until the proposal commits.
			this.judgmentIdempotency.set(key, {
				commandHash,
				binding,
				judgmentId: id,
			});
		}

		try {
			// Predecessor (design §3.7): same pair and relation; a CONFIRMED
			// predecessor stays current until the correction confirms; a PROPOSED
			// predecessor may be superseded immediately by the SAME proposer
			// (which frees the open tuple for the correction — design §3.7).
			let supersededPredId = "";
			if (command.predecessorId !== undefined && command.predecessorId !== "") {
				const pred = this.judgmentsById.get(command.predecessorId);
				if (pred === undefined) {
					throw new ApprovalError(
						"JUDGMENT_NOT_FOUND",
						`predecessor judgment not found: ${command.predecessorId}`,
					);
				}
				if (
					pred.fromId !== command.fromId ||
					pred.toId !== command.toId ||
					pred.relation !== command.relation
				) {
					throw new ApprovalError(
						"JUDGMENT_CONFLICT",
						"a predecessor must concern the same pair and relation",
					);
				}
				switch (pred.status) {
					case "confirmed":
						// Deferred to confirm time (design §5 step 7).
						break;
					case "proposed":
						if (judgmentProposerBinding(pred.proposer) !== binding) {
							throw new ApprovalError(
								"PROPOSAL_UNAUTHORIZED",
								"a proposed judgment may only be corrected by its own proposer",
							);
						}
						supersededPredId = pred.id;
						break;
					default:
						throw new ApprovalError(
							"INVALID_JUDGMENT_TRANSITION",
							`a "${pred.status}" predecessor cannot be corrected`,
						);
				}
			}

			// Only one OPEN proposal per (tenant, company, from, to, relation) —
			// another request is JUDGMENT_CONFLICT, never silent dedup. The
			// predecessor superseded by THIS correction is excluded: after the
			// supersession the tuple is free for the new row (mirror of the
			// partial unique index rejecting the INSERT only when another OPEN
			// proposal survives the predecessor supersession).
			const open = this.openJudgmentForTuple(
				tenantId,
				companyId,
				command.fromId,
				command.toId,
				command.relation,
			);
			if (open !== undefined && open.id !== supersededPredId) {
				throw new ApprovalError(
					"JUDGMENT_CONFLICT",
					"an open proposal already exists for this observation pair and relation",
				);
			}

			const judgment: AccountingJudgment = {
				id,
				tenantId,
				companyId,
				...(fiscalPeriodId === undefined ? {} : { fiscalPeriodId }),
				fromId: command.fromId,
				toId: command.toId,
				relation: command.relation,
				status: "proposed",
				proposer: judgmentProvenance(caller),
				proposalReason: command.reason,
				...(command.predecessorId === undefined || command.predecessorId === ""
					? {}
					: { predecessorId: command.predecessorId }),
				proposedAt: now,
				updatedAt: now,
			};

			// ── Atomic final block: mutate + record + complete the reservation. ──
			// No awaits after this point: a throw below cannot happen, and a throw
			// above already rolled back the reservation in the catch handler.
			this.judgmentsById.set(judgment.id, judgment);
			if (supersededPredId !== "") {
				const pred = this.judgmentsById.get(supersededPredId)!;
				pred.status = "superseded";
				pred.supersedesId = judgment.id;
				pred.decidedAt = now;
				pred.updatedAt = now;
				this.judgmentRelations.push({
					fromJudgmentId: supersededPredId,
					toJudgmentId: judgment.id,
					relation: "supersedes",
					actor: binding,
					timestamp: now,
				});
			}
			this.judgmentIdempotency.set(key, {
				commandHash,
				binding,
				judgmentId: judgment.id,
				completedAt: now,
			});
			return {
				judgmentId: judgment.id,
				judgment: { ...judgment },
				idempotentReplay: false,
			};
		} catch (error) {
			// Rollback of the incomplete reservation; a completed reservation is
			// never removed.
			const current = this.judgmentIdempotency.get(key);
			if (current !== undefined && current.completedAt === undefined) {
				this.judgmentIdempotency.delete(key);
			}
			throw error;
		}
	}

	/**
	 * Atomically confirm a proposed judgment (mirror of SQLiteStore.ConfirmJudgment):
	 * idempotency reservation → locked re-read → status gate → FRESH hash vs
	 * expected → pure policy → guarded transition + immutable event (+ correction
	 * supersession of the confirmed predecessor + observation relation projection).
	 */
	async confirmJudgment(
		command: ConfirmJudgmentCommand,
		principal: VerifiedApprovalPrincipal,
		policy: JudgmentPolicyFn = authorizeJudgment,
	): Promise<ConfirmJudgmentResult> {
		if (command.resolution.trim().length === 0) {
			throw new ApprovalError(
				"RESOLUTION_REQUIRED",
				"confirmation requires a non-empty professional resolution",
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

		const now = new Date().toISOString();
		const commandHash = judgmentDecideCommandHash(
			command.judgmentId,
			command.expectedJudgmentHash,
			command.resolution,
		);
		const binding = principal.subjectId;
		const key = `${principal.tenantId}\u0000${command.requestId}`;
		const existing = this.judgmentIdempotency.get(key);
		if (existing !== undefined) {
			if (existing.commandHash !== commandHash || existing.binding !== binding) {
				throw new ApprovalError(
					"IDEMPOTENCY_CONFLICT",
					"request id already used with a different command or principal",
				);
			}
			if (existing.completedAt !== undefined && existing.result !== undefined) {
				const replay = this.judgmentsById.get(existing.result.judgmentId);
				if (replay === undefined) {
					throw new ApprovalError(
						"JUDGMENT_NOT_FOUND",
						"decision reservation completed but no judgment row found",
					);
				}
				return {
					judgmentId: replay.id,
					judgment: { ...replay },
					judgmentEventId: existing.result.judgmentEventId,
					idempotentReplay: true,
				};
			}
		} else {
			this.judgmentIdempotency.set(key, {
				commandHash,
				binding,
				judgmentId: command.judgmentId,
			});
		}

		try {
			const judgment = this.judgmentsById.get(command.judgmentId);
			if (judgment === undefined) {
				throw new ApprovalError(
					"JUDGMENT_NOT_FOUND",
					`judgment not found: ${command.judgmentId}`,
				);
			}

			// Status gate: only a proposed judgment may be decided.
			if (judgment.status !== "proposed") {
				throw new ApprovalError(
					"INVALID_JUDGMENT_TRANSITION",
					`confirm is not legal from status "${judgment.status}"`,
				);
			}

			// The reviewed hash is recomputed FRESH from the current row and compared
			// against what the adjudicator actually reviewed; a mismatch carries ONLY
			// the two hashes, never content.
			const actual = await computeJudgmentHash(judgment);
			if (
				command.expectedJudgmentHash.trim().toLowerCase() !==
				actual.toLowerCase()
			) {
				throw new ApprovalError(
					"JUDGMENT_HASH_MISMATCH",
					"judgment changed after review; expected hash does not match the current proposed state",
					{
						expectedJudgmentHash: command.expectedJudgmentHash,
						actualJudgmentHash: actual,
					},
				);
			}

			// Pure policy in the critical section; any denial fails the decision.
			const decision = policy(principal, judgment);
			if (!decision.allowed) {
				throw new ApprovalError(
					decision.reasonCode as ApprovalError["code"],
					"judgment authorization policy denied the confirm decision",
				);
			}

			const snapshot = principalSnapshot(principal);
			const confirmed: AccountingJudgment = {
				...judgment,
				status: "confirmed",
				resolution: command.resolution,
				adjudicator: snapshot,
				policyVersion: decision.policyVersion,
				decidedAt: now,
				updatedAt: now,
			};
			const confirmedHash = await computeJudgmentHash(confirmed);

			// Correction supersession is atomic with the confirmation: the
			// predecessor must be confirmed (or already superseded by THIS very
			// proposal at propose time).
			let supersededHash = "";
			if (judgment.predecessorId !== undefined && judgment.predecessorId !== "") {
				const pred = this.judgmentsById.get(judgment.predecessorId);
				if (pred === undefined) {
					throw new ApprovalError(
						"JUDGMENT_NOT_FOUND",
						`predecessor judgment not found: ${judgment.predecessorId}`,
					);
				}
				switch (pred.status) {
					case "confirmed":
						supersededHash = await computeJudgmentHash({
							...pred,
							status: "superseded",
							supersedesId: judgment.id,
							updatedAt: now,
						});
					break;
					case "superseded":
						if (pred.supersedesId !== judgment.id) {
							throw new ApprovalError(
								"INVALID_JUDGMENT_TRANSITION",
								"predecessor is already superseded by a different judgment",
							);
						}
						break;
					default:
						throw new ApprovalError(
							"INVALID_JUDGMENT_TRANSITION",
							`predecessor status "${pred.status}" cannot be superseded by a correction`,
						);
				}
			}

			// ── Atomic final block: mutate + record + complete the reservation. ──
			const eventId = randomUUID();
			const event: JudgmentEvent = {
				id: eventId,
				requestId: command.requestId,
				judgmentId: judgment.id,
				tenantId: judgment.tenantId,
				action: "confirm",
				fromStatus: "proposed",
				toStatus: "confirmed",
				judgmentHash: confirmedHash,
				principalSnapshot: snapshot,
				policyVersion: decision.policyVersion,
				reason: command.resolution,
				createdAt: now,
			};
			Object.assign(judgment, confirmed);
			this.judgmentEventRecords.push(event);
			// Compatibility observation relation projection (judgments remain
			// authoritative; the relation row is a read projection).
			this.relate(judgment.fromId, judgment.toId, judgment.relation, {
				actor: snapshot.subjectId,
				timestamp: now,
			});
			if (supersededHash !== "") {
				const pred = this.judgmentsById.get(judgment.predecessorId!)!;
				pred.status = "superseded";
				pred.supersedesId = judgment.id;
				pred.updatedAt = now;
				this.judgmentEventRecords.push({
					id: randomUUID(),
					requestId: command.requestId,
					judgmentId: pred.id,
					tenantId: pred.tenantId,
					action: "supersede",
					fromStatus: "confirmed",
					toStatus: "superseded",
					judgmentHash: supersededHash,
					reason: command.resolution,
					createdAt: now,
				});
				this.judgmentRelations.push({
					fromJudgmentId: pred.id,
					toJudgmentId: judgment.id,
					relation: "supersedes",
					actor: snapshot.subjectId,
					timestamp: now,
				});
			}
			const result: ConfirmJudgmentResult = {
				judgmentId: judgment.id,
				judgment: { ...judgment },
				judgmentEventId: eventId,
				idempotentReplay: false,
			};
			this.judgmentIdempotency.set(key, {
				commandHash,
				binding,
				judgmentId: judgment.id,
				result: { judgmentId: judgment.id, judgmentEventId: eventId },
				completedAt: now,
			});
			return result;
		} catch (error) {
			const current = this.judgmentIdempotency.get(key);
			if (current !== undefined && current.completedAt === undefined) {
				this.judgmentIdempotency.delete(key);
			}
			throw error;
		}
	}

	/**
	 * Atomically reject a proposed judgment (mirror of SQLiteStore.RejectJudgment):
	 * the same lock/hash/policy/idempotency path as confirmation, storing the
	 * HUMAN reason as the resolution and becoming terminal. Writes NO observation
	 * relation projection and performs no supersession.
	 */
	async rejectJudgment(
		command: RejectJudgmentCommand,
		principal: VerifiedApprovalPrincipal,
		policy: JudgmentPolicyFn = authorizeJudgment,
	): Promise<RejectJudgmentResult> {
		if (command.reason.trim().length === 0) {
			throw new ApprovalError(
				"RESOLUTION_REQUIRED",
				"rejection requires a non-empty human reason",
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

		const now = new Date().toISOString();
		const commandHash = judgmentDecideCommandHash(
			command.judgmentId,
			command.expectedJudgmentHash,
			command.reason,
		);
		const binding = principal.subjectId;
		const key = `${principal.tenantId}\u0000${command.requestId}`;
		const existing = this.judgmentIdempotency.get(key);
		if (existing !== undefined) {
			if (existing.commandHash !== commandHash || existing.binding !== binding) {
				throw new ApprovalError(
					"IDEMPOTENCY_CONFLICT",
					"request id already used with a different command or principal",
				);
			}
			if (existing.completedAt !== undefined && existing.result !== undefined) {
				const replay = this.judgmentsById.get(existing.result.judgmentId);
				if (replay === undefined) {
					throw new ApprovalError(
						"JUDGMENT_NOT_FOUND",
						"decision reservation completed but no judgment row found",
					);
				}
				return {
					judgmentId: replay.id,
					judgment: { ...replay },
					judgmentEventId: existing.result.judgmentEventId,
					idempotentReplay: true,
				};
			}
		} else {
			this.judgmentIdempotency.set(key, {
				commandHash,
				binding,
				judgmentId: command.judgmentId,
			});
		}

		try {
			const judgment = this.judgmentsById.get(command.judgmentId);
			if (judgment === undefined) {
				throw new ApprovalError(
					"JUDGMENT_NOT_FOUND",
					`judgment not found: ${command.judgmentId}`,
				);
			}
			if (judgment.status !== "proposed") {
				throw new ApprovalError(
					"INVALID_JUDGMENT_TRANSITION",
					`reject is not legal from status "${judgment.status}"`,
				);
			}
			const actual = await computeJudgmentHash(judgment);
			if (
				command.expectedJudgmentHash.trim().toLowerCase() !==
				actual.toLowerCase()
			) {
				throw new ApprovalError(
					"JUDGMENT_HASH_MISMATCH",
					"judgment changed after review; expected hash does not match the current proposed state",
					{
						expectedJudgmentHash: command.expectedJudgmentHash,
						actualJudgmentHash: actual,
					},
				);
			}
			const decision = policy(principal, judgment);
			if (!decision.allowed) {
				throw new ApprovalError(
					decision.reasonCode as ApprovalError["code"],
					"judgment authorization policy denied the reject decision",
				);
			}

			const snapshot = principalSnapshot(principal);
			const rejected: AccountingJudgment = {
				...judgment,
				status: "rejected",
				resolution: command.reason,
				adjudicator: snapshot,
				policyVersion: decision.policyVersion,
				decidedAt: now,
				updatedAt: now,
			};
			const rejectedHash = await computeJudgmentHash(rejected);

			// ── Atomic final block: mutate + record + complete the reservation. ──
			const eventId = randomUUID();
			const event: JudgmentEvent = {
				id: eventId,
				requestId: command.requestId,
				judgmentId: judgment.id,
				tenantId: judgment.tenantId,
				action: "reject",
				fromStatus: "proposed",
				toStatus: "rejected",
				judgmentHash: rejectedHash,
				principalSnapshot: snapshot,
				policyVersion: decision.policyVersion,
				reason: command.reason,
				createdAt: now,
			};
			Object.assign(judgment, rejected);
			this.judgmentEventRecords.push(event);
			const result: RejectJudgmentResult = {
				judgmentId: judgment.id,
				judgment: { ...judgment },
				judgmentEventId: eventId,
				idempotentReplay: false,
			};
			this.judgmentIdempotency.set(key, {
				commandHash,
				binding,
				judgmentId: judgment.id,
				result: { judgmentId: judgment.id, judgmentEventId: eventId },
				completedAt: now,
			});
			return result;
		} catch (error) {
			const current = this.judgmentIdempotency.get(key);
			if (current !== undefined && current.completedAt === undefined) {
				this.judgmentIdempotency.delete(key);
			}
			throw error;
		}
	}

	/**
	 * Atomically withdraw the caller's OWN proposed judgment (mirror of
	 * SQLiteStore.WithdrawJudgment). The SAME exact proposer identity
	 * (system+actorId+actorKind+session) is required — mismatch is
	 * PROPOSAL_UNAUTHORIZED (provenance continuity, never professional
	 * authorization). Idempotency is keyed by (judgment tenant, requestId); the
	 * withdrawal stamps decidedAt and writes a 'withdraw' event.
	 */
	async withdrawJudgment(
		command: WithdrawJudgmentCommand,
		caller: MemorySource,
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

		const judgment = this.judgmentsById.get(command.judgmentId);
		if (judgment === undefined) {
			throw new ApprovalError(
				"JUDGMENT_NOT_FOUND",
				`judgment not found: ${command.judgmentId}`,
			);
		}

		const now = new Date().toISOString();
		const commandHash = judgmentWithdrawCommandHash(command.judgmentId);
		const binding = judgmentProposerBinding(caller);
		const key = `${judgment.tenantId}\u0000${command.requestId}`;
		const existing = this.judgmentIdempotency.get(key);
		if (existing !== undefined) {
			if (existing.commandHash !== commandHash || existing.binding !== binding) {
				throw new ApprovalError(
					"IDEMPOTENCY_CONFLICT",
					"request id already used with a different command or proposer",
				);
			}
			if (existing.completedAt !== undefined && existing.result !== undefined) {
				const replay = this.judgmentsById.get(existing.result.judgmentId);
				if (replay === undefined) {
					throw new ApprovalError(
						"JUDGMENT_NOT_FOUND",
						"withdraw reservation completed but no judgment row found",
					);
				}
				return {
					judgmentId: replay.id,
					judgment: { ...replay },
					judgmentEventId: existing.result.judgmentEventId,
					idempotentReplay: true,
				};
			}
		} else {
			this.judgmentIdempotency.set(key, {
				commandHash,
				binding,
				judgmentId: command.judgmentId,
			});
		}

		try {
			// Only an open proposal may be withdrawn, and only by its OWN proposer.
			if (judgment.status !== "proposed") {
				throw new ApprovalError(
					"INVALID_JUDGMENT_TRANSITION",
					`withdrawal is not legal from status "${judgment.status}"`,
				);
			}
			if (judgmentProposerBinding(judgment.proposer) !== binding) {
				throw new ApprovalError(
					"PROPOSAL_UNAUTHORIZED",
					"a judgment may only be withdrawn by its own proposer",
				);
			}

			const withdrawn: AccountingJudgment = {
				...judgment,
				status: "withdrawn",
				decidedAt: now,
				updatedAt: now,
			};
			const withdrawnHash = await computeJudgmentHash(withdrawn);

			// ── Atomic final block: mutate + record + complete the reservation. ──
			const eventId = randomUUID();
			const event: JudgmentEvent = {
				id: eventId,
				requestId: command.requestId,
				judgmentId: judgment.id,
				tenantId: judgment.tenantId,
				action: "withdraw",
				fromStatus: "proposed",
				toStatus: "withdrawn",
				judgmentHash: withdrawnHash,
				createdAt: now,
			};
			Object.assign(judgment, withdrawn);
			this.judgmentEventRecords.push(event);
			const result: WithdrawJudgmentResult = {
				judgmentId: judgment.id,
				judgment: { ...judgment },
				judgmentEventId: eventId,
				idempotentReplay: false,
			};
			this.judgmentIdempotency.set(key, {
				commandHash,
				binding,
				judgmentId: judgment.id,
				result: { judgmentId: judgment.id, judgmentEventId: eventId },
				completedAt: now,
			});
			return result;
		} catch (error) {
			const current = this.judgmentIdempotency.get(key);
			if (current !== undefined && current.completedAt === undefined) {
				this.judgmentIdempotency.delete(key);
			}
			throw error;
		}
	}

	/** The one OPEN proposal for a (tenant, company, from, to, relation) tuple. */
	private openJudgmentForTuple(
		tenantId: string,
		companyId: string,
		fromId: string,
		toId: string,
		relation: MemoryRelation,
	): AccountingJudgment | undefined {
		for (const judgment of this.judgmentsById.values()) {
			if (
				judgment.tenantId === tenantId &&
				judgment.companyId === companyId &&
				judgment.fromId === fromId &&
				judgment.toId === toId &&
				judgment.relation === relation &&
				judgment.status === "proposed"
			) {
				return judgment;
			}
		}
		return undefined;
	}
}

function chainKey(topicKey: string, scope: MemoryScope): string {
	return `${topicKey}\u0000${scopeKey(scope)}`;
}

/**
 * Canonical idempotency command hash (mirror of approveCommandHash in
 * store.go): SHA-256 of `memoryId \x00 lowercase(expectedHash) \x00 reason`,
 * hex-encoded. Byte-identical to the Go implementation so replayed results are
 * comparable across runtimes.
 */
function approveCommandHash(
	memoryId: string,
	expectedEnvelopeHash: string,
	reason: string,
): string {
	const canonical = `${memoryId}\u0000${expectedEnvelopeHash.toLowerCase()}\u0000${reason}`;
	return createHash("sha256").update(canonical, "utf8").digest("hex");
}

/**
 * Provenance-only copy of a proposer Source (v0.4.0 Step 2): the judgment row
 * records exactly the claim the proposer presented (system, actorId, actorKind,
 * session) — it NEVER authorizes. Mirrors the store's proposer column write.
 */
function judgmentProvenance(source: MemorySource): MemorySource {
	return {
		system: source.system,
		...(source.actorId === undefined || source.actorId === ""
			? {}
			: { actorId: source.actorId }),
		actorKind: source.actorKind,
		...(source.model === undefined || source.model === ""
			? {}
			: { model: source.model }),
		...(source.reference === undefined || source.reference === ""
			? {}
			: { reference: source.reference }),
		...(source.session === undefined || source.session === ""
			? {}
			: { session: source.session }),
	};
}

/**
 * Canonical proposer identity binding (mirror of proposerBinding in store.go):
 * system NUL actorId NUL actorKind NUL session. Withdrawal and proposed-
 * predecessor correction require the EXACT same binding (provenance continuity).
 */
function judgmentProposerBinding(source: MemorySource): string {
	return [
		source.system,
		source.actorId ?? "",
		source.actorKind,
		source.session ?? "",
	].join("\u0000");
}

/**
 * Canonical idempotency command hash of a proposal (mirror of
 * proposeJudgmentCommandHash in store.go): SHA-256 hex of
 * fromId NUL toId NUL relation NUL reason NUL predecessorId. RequestId is the
 * KEY, not part of the payload.
 */
function judgmentProposeCommandHash(command: ProposeJudgmentCommand): string {
	const canonical = [
		command.fromId,
		command.toId,
		command.relation,
		command.reason,
		command.predecessorId ?? "",
	].join("\u0000");
	return createHash("sha256").update(canonical, "utf8").digest("hex");
}

/**
 * Canonical idempotency command hash of a confirm/reject decision (mirror of
 * decideJudgmentCommandHash in store.go): judgmentId NUL lowercase(expectedHash)
 * NUL resolution.
 */
function judgmentDecideCommandHash(
	judgmentId: string,
	expectedHash: string,
	resolution: string,
): string {
	const canonical = [
		judgmentId,
		expectedHash.toLowerCase(),
		resolution,
	].join("\u0000");
	return createHash("sha256").update(canonical, "utf8").digest("hex");
}

/**
 * Canonical idempotency command hash of a withdrawal (mirror of
 * withdrawJudgmentCommandHash in store.go): SHA-256 hex of the judgment id.
 */
function judgmentWithdrawCommandHash(judgmentId: string): string {
	return createHash("sha256").update(judgmentId, "utf8").digest("hex");
}
