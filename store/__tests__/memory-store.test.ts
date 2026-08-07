/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * InMemoryMemoryStore behavior (contracts/memory.md v2):
 * - Upsert by topicKey + exact scope: first save is revision 1 (`created`);
 *   a second save of the same chain is revision 2 (`updated`) with history
 *   preserved AND the previous current revision superseded.
 * - Scope is part of identity: the same topicKey under a different scope is a
 *   different chain (a fresh revision 1, never a revision of the other tenant).
 * - Reads: findById, findByTopicKey (latest), findByScope (exact), list, and
 *   listByStatus. A stored memory is never edited in place.
 * - The approval gate: fiscalEffect != none → pending_review.
 */

import { describe, expect, it } from "vitest";

import type { MemoryScope, SaveMemoryInput } from "../../core/types.js";
import { InMemoryMemoryStore } from "../memory-store.js";

const ORG = "org-001";
const RUC_A = "20100039201";
const RUC_B = "20600995804";
const PERIOD = "202401";

const scope = (ruc: string = RUC_A): MemoryScope => ({
	kind: "company",
	organizationId: ORG,
	companyId: "acme",
	ruc,
	period: PERIOD,
});

const testAgentSource = {
	system: "vitest",
	actorId: "test-agent",
	actorKind: "agent" as const,
};

function input(topicKey: string, what: string): SaveMemoryInput {
	return {
		topicKey,
		title: "IGV base rate",
		kind: "rule",
		scope: scope(),
		content: {
			what,
			why: "standard rate for goods",
			where: "Peru",
			learned: "applies to all invoices",
		},
		fiscalEffect: "none",
		effectiveAt: "2024-01-01T00:00:00.000Z",
		source: testAgentSource,
	};
}

describe("InMemoryMemoryStore", () => {
	it("creates revision 1 with outcome created on first save", async () => {
		const store = new InMemoryMemoryStore();
		const result = await store.save(input("tax.igv.rate", "first version"));

		expect(result.outcome).toBe("created");
		expect(result.memory.revision).toBe(1);
		expect(result.memory.status).toBe("active");
		expect(result.memory.identity.topicKey).toBe("tax.igv.rate");
		expect(result.memory.contentHash.length).toBe(64);
	});

	it("creates revision 2 on a second save of the same chain, superseding the first", async () => {
		const store = new InMemoryMemoryStore();
		const first = await store.save(input("tax.igv.rate", "first version"));
		const second = await store.save(input("tax.igv.rate", "second version"));

		expect(second.outcome).toBe("updated");
		expect(second.memory.revision).toBe(2);
		expect(second.memory.identity.id).not.toBe(first.memory.identity.id);

		// History preserved: both revisions remain retrievable by id; the first is
		// superseded and routes readers to the second.
		const storedFirst = store.findById(first.memory.identity.id);
		expect(storedFirst?.revision).toBe(1);
		expect(storedFirst?.content.what).toBe("first version");
		expect(storedFirst?.status).toBe("superseded");
		expect(storedFirst?.supersedesId).toBe(second.memory.identity.id);
		expect(store.findById(second.memory.identity.id)?.revision).toBe(2);
		expect(store.successorOf(first.memory.identity.id)?.identity.id).toBe(
			second.memory.identity.id,
		);
	});

	it("returns undefined for unknown ids", async () => {
		const store = new InMemoryMemoryStore();
		expect(store.findById("does-not-exist")).toBeUndefined();
	});

	it("findByTopicKey returns the latest revision for the exact scope", async () => {
		const store = new InMemoryMemoryStore();
		await store.save(input("tax.igv.rate", "first version"));
		await store.save(input("tax.igv.rate", "second version"));

		const latest = store.findByTopicKey("tax.igv.rate", scope());
		expect(latest?.revision).toBe(2);
		expect(latest?.content.what).toBe("second version");
	});

	it("treats scope as part of identity — same topicKey under another scope is a new chain", async () => {
		const store = new InMemoryMemoryStore();
		const a = await store.save({
			...input("tax.igv.rate", "first version"),
			scope: scope(RUC_A),
		});
		const b = await store.save({
			...input("tax.igv.rate", "first version"),
			scope: scope(RUC_B),
		});

		expect(a.outcome).toBe("created");
		expect(b.outcome).toBe("created");
		expect(b.memory.revision).toBe(1);
		expect(store.findById(b.memory.identity.id)?.scope).toEqual(scope(RUC_B));
	});

	it("findByScope filters by exact scope", async () => {
		const store = new InMemoryMemoryStore();
		const a = await store.save({
			...input("topic.a", "version a"),
			scope: scope(RUC_A),
		});
		await store.save({ ...input("topic.b", "version b"), scope: scope(RUC_B) });

		const inA = store.findByScope(scope(RUC_A));
		expect(inA).toHaveLength(1);
		expect(inA[0].identity.id).toBe(a.memory.identity.id);
	});

	it("list returns every stored revision", async () => {
		const store = new InMemoryMemoryStore();
		await store.save(input("tax.igv.rate", "first version"));
		await store.save(input("tax.igv.rate", "second version"));

		expect(store.list()).toHaveLength(2);
	});

	it("listByStatus filters by status", async () => {
		const store = new InMemoryMemoryStore();
		const first = await store.save(input("tax.igv.rate", "first version"));
		await store.save(input("tax.igv.rate", "second version"));

		// The second save superseded the first.
		expect(store.listByStatus("superseded")).toHaveLength(1);
		expect(store.listByStatus("active")).toHaveLength(1);

		store.applyStatusTransition(first.memory.identity.id, "voided", {
			actor: "reviewer",
			actorKind: "human",
			timestamp: "2026-01-15T12:00:00.000Z",
		});

		expect(store.listByStatus("voided")).toHaveLength(1);
	});

	it("never edits a stored memory in place — a new save creates a new revision", async () => {
		const store = new InMemoryMemoryStore();
		const first = await store.save(input("tax.igv.rate", "first version"));

		const second = await store.save(input("tax.igv.rate", "second version"));
		expect(second.memory.revision).toBe(2);

		const storedFirst = store.findById(first.memory.identity.id);
		expect(storedFirst?.status).toBe("superseded");
		expect(storedFirst?.content.what).toBe("first version");
	});

	it("applies the approval gate — fiscalEffect != none lands pending_review", async () => {
	const store = new InMemoryMemoryStore();
	const gated = await store.save({
	...input("adjust/aj-001", "ajuste de periodo"),
	fiscalEffect: "adjustment",
	effectiveAt: "2024-01-31T00:00:00.000Z",
	});

	expect(gated.memory.status).toBe("pending_review");
	expect(store.listByStatus("pending_review")).toHaveLength(1);
	});

	it("object store: same-scope duplicate is a NO-OP, cross-scope identical bytes conflict without leaking scope values (v0.7.x scope-aware duplicates)", () => {
	const store = new InMemoryMemoryStore();
	const bytes = new TextEncoder().encode("identical artifact bytes");
	const inA = {
	bytes,
	contentType: "application/xml",
	scope: scope(RUC_A),
	source: testAgentSource,
	};

	const first = store.storeObject(inA);
	expect(first.created).toBe(true);

	// Same exact scope: content-addressed duplicate NO-OP.
	const dup = store.storeObject(inA);
	expect(dup.created).toBe(false);
	expect(dup.object.objectId).toBe(first.object.objectId);

	// Different RUC: typed non-enumerating conflict.
	const inB = { ...inA, scope: scope(RUC_B) };
	expect(() => store.storeObject(inB)).toThrow(/OBJECT_SCOPE_CONFLICT/);
	try {
	store.storeObject(inB);
	throw new Error("must not reach here");
	} catch (err) {
	const msg = (err as Error).message;
	for (const leak of [ORG, RUC_A, RUC_B, PERIOD, "acme"]) {
	expect(msg).not.toContain(leak);
	}
	}

	// Different period: conflict too.
	const inC = { ...inA, scope: scope(RUC_A) as MemoryScope };
	inC.scope = { kind: "company", organizationId: ORG, companyId: "acme", ruc: RUC_A, period: "202402" };
	expect(() => store.storeObject(inC)).toThrow(/OBJECT_SCOPE_CONFLICT/);
	});
    });
