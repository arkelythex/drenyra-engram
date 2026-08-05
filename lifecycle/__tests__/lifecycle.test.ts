/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Lifecycle v2 (contracts/lifecycle.md):
 * - The human approval gate: a memory with fiscalEffect != none lands
 *   `pending_review`; only a HUMAN actor can approve/reject it
 *   (GATE_REQUIRES_HUMAN otherwise). Informative memory (fiscalEffect none)
 *   saves directly `active`.
 * - approve/reject/void/supersede legality fail closed (INVALID_TRANSITION).
 * - Vigencia: an expired memory (expiresAt < now) surfaces as stale in search
 *   results, never as current fact.
 */

import { describe, expect, it } from "vitest";

import type { MemoryContent, MemoryScope } from "../../core/types.js";
import { InMemoryMemoryStore } from "../../store/memory-store.js";
import { scopeFirstSearch } from "../../search/scope-first.js";
import {
	applyGateTransition,
	approve,
	assertHumanApproval,
	canApprove,
	canVoid,
	initialStatus,
	isGated,
	reject,
	voidMemory,
} from "../transitions.js";

const SCOPE: MemoryScope = {
	kind: "company",
	organizationId: "org-001",
	companyId: "acme",
	ruc: "20100039201",
	period: "202401",
};
const T = "2026-01-15T12:00:00.000Z";

const CONTENT: MemoryContent = {
	what: "IGV base rate is 18 percent",
	why: "standard rate for goods",
	where: "Peru",
	learned: "applies to all invoices",
};

const agentMeta = {
	actor: "test-agent",
	actorKind: "agent" as const,
	timestamp: T,
};
const humanMeta = {
	actor: "maria.torres",
	actorKind: "human" as const,
	timestamp: T,
};

const informative = {
	topicKey: "tax.igv.rate",
	title: "IGV base rate",
	kind: "rule" as const,
	scope: SCOPE,
	content: CONTENT,
	fiscalEffect: "none" as const,
	effectiveAt: "2024-01-01T00:00:00.000Z",
	source: {
		system: "vitest",
		actorId: "test-agent",
		actorKind: "agent" as const,
	},
};

const gated = {
	...informative,
	topicKey: "adjust/aj-001",
	title: "Ajuste AJ-001",
	kind: "decision" as const,
	fiscalEffect: "adjustment" as const,
	effectiveAt: "2024-01-31T00:00:00.000Z",
};

describe("the approval gate", () => {
	it("initialStatus: none → active; any fiscal effect → pending_review", () => {
		expect(initialStatus("none")).toBe("active");
		expect(initialStatus("adjustment")).toBe("pending_review");
		expect(initialStatus("journal_entry")).toBe("pending_review");
		expect(initialStatus("sunat_filing")).toBe("pending_review");
		expect(isGated("none")).toBe(false);
		expect(isGated("closing")).toBe(true);
	});

	it("a memory with fiscal effect is saved pending_review", async () => {
		const store = new InMemoryMemoryStore();
		const saved = await store.save(gated);
		expect(saved.memory.status).toBe("pending_review");
	});

	it("only pending_review memories can be approved", () => {
		expect(canApprove("pending_review")).toBe(true);
		expect(canApprove("active")).toBe(false);
		expect(canApprove("approved")).toBe(false);
		expect(canApprove("superseded")).toBe(false);
	});

	it("a machine cannot approve — GATE_REQUIRES_HUMAN", () => {
		const memory = {
			...gated,
			identity: { id: "m1", topicKey: gated.topicKey },
			status: "pending_review" as const,
		};
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
		expect(() => approve(memory as any, agentMeta)).toThrow(
			/GATE_REQUIRES_HUMAN/,
		);
		expect(() => assertHumanApproval("agent")).toThrow(/GATE_REQUIRES_HUMAN/);
		expect(() => assertHumanApproval("human")).not.toThrow();
	});

	it("a human approves a pending_review memory through the store", async () => {
		const store = new InMemoryMemoryStore();
		const saved = await store.save(gated);
		const approved = applyGateTransition(
			store,
			saved.memory.identity.id,
			"approved",
			humanMeta,
		);
		expect(approved.status).toBe("approved");
		const log = store.transitionLog();
		expect(log).toHaveLength(1);
		expect(log[0].memoryId).toBe(saved.memory.identity.id);
		expect(log[0].from).toBe("pending_review");
		expect(log[0].to).toBe("approved");
		expect(log[0].actorKind).toBe("human");
	});

	it("approving an active (informative) memory fails closed with INVALID_TRANSITION", async () => {
		const store = new InMemoryMemoryStore();
		const saved = await store.save(informative);
		expect(saved.memory.status).toBe("active");
		expect(() =>
			applyGateTransition(
				store,
				saved.memory.identity.id,
				"approved",
				humanMeta,
			),
		).toThrow(/INVALID_TRANSITION/);
	});

	it("a machine cannot reject — GATE_REQUIRES_HUMAN; a human can", async () => {
		const store = new InMemoryMemoryStore();
		const saved = await store.save(gated);
		const memory = { ...saved.memory };
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
		expect(() => reject(memory as any, agentMeta)).toThrow(
			/GATE_REQUIRES_HUMAN/,
		);
		const rejected = applyGateTransition(
			store,
			saved.memory.identity.id,
			"rejected",
			humanMeta,
		);
		expect(rejected.status).toBe("rejected");
		// Terminal: a rejected memory cannot be approved later.
		expect(() =>
			applyGateTransition(
				store,
				saved.memory.identity.id,
				"approved",
				humanMeta,
			),
		).toThrow(/INVALID_TRANSITION/);
	});

	it("void admits human or system, never an agent", async () => {
		expect(canVoid("active")).toBe(true);
		expect(canVoid("approved")).toBe(true);
		expect(canVoid("superseded")).toBe(false);
		expect(canVoid("voided")).toBe(false);
		const memory = {
			...gated,
			identity: { id: "m1", topicKey: gated.topicKey },
			status: "active" as const,
		};
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
		expect(() => voidMemory(memory as any, agentMeta)).toThrow(
			/GATE_AGENT_CANNOT_VOID/,
		);
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
		expect(() => voidMemory(memory as any, humanMeta)).not.toThrow();
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
		expect(() =>
			voidMemory(memory as any, {
				actor: "system",
				actorKind: "system" as const,
				timestamp: T,
			}),
		).not.toThrow();
	});
});

describe("supersession (immutable history)", () => {
	it("a second save of the same chain supersedes the first atomically", async () => {
		const store = new InMemoryMemoryStore();
		const first = await store.save(informative);
		const second = await store.save({
			...informative,
			content: { ...CONTENT, what: "v2" },
		});

		const storedFirst = store.findById(first.memory.identity.id);
		expect(storedFirst?.status).toBe("superseded");
		expect(storedFirst?.supersedesId).toBe(second.memory.identity.id);
		expect(store.successorOf(first.memory.identity.id)?.identity.id).toBe(
			second.memory.identity.id,
		);
		expect(store.listByStatus("active")).toHaveLength(1);
		expect(store.listByStatus("superseded")).toHaveLength(1);
	});
});

describe("vigencia (stale marking)", () => {
	it("an expired memory surfaces as stale in search, never as current fact", async () => {
		const store = new InMemoryMemoryStore();
		await store.save({
			...informative,
			topicKey: "tax.igv.expired",
			validity: {
				effectiveAt: "2020-01-01T00:00:00.000Z",
				expiresAt: "2020-06-01T00:00:00.000Z",
			},
		});

		const results = scopeFirstSearch(store, {
			query: "IGV base rate",
			scope: SCOPE,
		});
		expect(results).toHaveLength(1);
		expect(results[0].stale).toBe(true);
	});
});
