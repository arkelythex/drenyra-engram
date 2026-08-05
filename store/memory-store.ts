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
	scopeEquals,
	scopeKey,
	type AccountingMemory,
	type ApprovalAuthorizationDecision,
	type ApprovalEvent,
	type ApprovalResult,
	type ApproveMemoryCommand,
	type MemoryRelation,
	type MemoryRelationRecord,
	type MemoryScope,
	type MemoryStatus,
	type MemoryWriteResult,
	type SaveMemoryInput,
	type StatusTransitionRecord,
	type VerifiedApprovalPrincipal,
} from "../core/types.js";
import { canVoid, initialStatus } from "../lifecycle/transitions.js";
import { principalSnapshot } from "../auth/principal.js";
import { authorizeApproval } from "../authz/approval-policy.js";

/**
 * Pure authorization function passed to the atomic approval (mirror of the
 * authz.ApprovalAuthorizationPolicy interface). Defaults to the frozen
 * v0.4.0 policy.
 */
export type ApprovalPolicyFn = (
	principal: VerifiedApprovalPrincipal,
	memory: AccountingMemory,
) => ApprovalAuthorizationDecision;

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
			throw new ApprovalError("REASON_REQUIRED", "a reason is required for approval");
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
			if (command.expectedEnvelopeHash.trim().toLowerCase() !== h1.toLowerCase()) {
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
