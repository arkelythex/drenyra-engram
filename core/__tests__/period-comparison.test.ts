/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Period-over-period comparison mirror tests (v0.5.0, design §4) — the exact TS
 * mirror of the Go contract in `internal/core/period_comparison.go`: the
 * deterministic chain/status/pending/close deltas with stable ordering. There
 * are no monetary fields in the comparison model — no money value is asserted.
 */

import { describe, expect, it } from "vitest";

import type {
	AccountingMemory,
	ClosePendingItem,
	MemoryScope,
} from "../types.js";
import { computeContentHash, computePeriodComparison } from "../types.js";

/** Builds one current-revision memory with the given identity, kind, status and
 * content (the same zero-value optional fields the Go unit-test helper uses, so
 * both runtimes hash the same canonical bytes). */
async function comparisonMemory(
	id: string,
	topicKey: string,
	kind: string,
	status: string,
	title: string,
	what: string,
): Promise<AccountingMemory> {
	const scope: MemoryScope = {
		kind: "company",
		organizationId: "cmp_org",
		companyId: "cmp_01",
		ruc: "20601234567",
		period: "202607",
	};
	const memory: AccountingMemory = {
		identity: { id, topicKey },
		title,
		kind: kind as AccountingMemory["kind"],
		scope,
		content: { what, why: "fixture", where: "core", learned: "n/a" },
		status: status as AccountingMemory["status"],
		fiscalEffect: "none",
		effectiveAt: "",
		recordedAt: "",
		source: { system: "", actorKind: "system" },
		revision: 0,
		contentHash: "",
	};
	memory.contentHash = await computeContentHash(memory);
	return memory;
}

/** Clone of a memory with the scope period replaced (the pure comparison matches
 * chains by topic key across two scopes that differ only by period). */
function reperiod(memory: AccountingMemory, period: string): AccountingMemory {
	if (memory.scope.kind !== "company") return memory;
	return { ...memory, scope: { ...memory.scope, period } };
}

describe("period comparison mirror (v0.5.0)", () => {
	it("derives deterministic deltas (new/removed/changed/unchanged)", async () => {
		// July: three chains — account/4011 (content A), fact/igv-tasa (content
		// 18%), obligation/igv-621. August: account/4011 with CHANGED content,
		// fact/igv-tasa unchanged, and a NEW chain account/4011/ventas-agosto;
		// obligation/igv-621 is REMOVED. Expect: new=1, removed=1, changed=1,
		// unchanged=1, no status changes.
		const from = await Promise.all([
			comparisonMemory("j1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo"),
			comparisonMemory("j2", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%"),
			comparisonMemory("j3", "obligation/igv-621", "obligation", "active", "Obligacion PDT 621", "declarar IGV julio"),
		]);
		const to = await Promise.all([
			reperiod(await comparisonMemory("a1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo CORREGIDAS"), "202608"),
			reperiod(await comparisonMemory("a2", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%"), "202608"),
			reperiod(await comparisonMemory("a3", "account/4011/ventas-agosto", "fact", "active", "Ventas agosto", "ventas de agosto"), "202608"),
		]);

		const got = await computePeriodComparison("202607", "202608", from, to, [], [], "open", "open");

		expect(got.from).toBe("202607");
		expect(got.to).toBe("202608");
		expect(got.counts).toEqual({
			fromTotal: 3,
			toTotal: 3,
			delta: 0,
			byKindDelta: { fact: 1, obligation: -1 },
			byStatusDelta: { active: 0 },
		});
		expect(got.chains.new).toEqual([
			expect.objectContaining({ topicKey: "account/4011/ventas-agosto" }),
		]);
		expect(got.chains.removed).toEqual([
			expect.objectContaining({ topicKey: "obligation/igv-621" }),
		]);
		expect(got.chains.changed).toEqual([
			expect.objectContaining({ topicKey: "account/4011" }),
		]);
		expect(got.chains.unchangedCount).toBe(1);
		expect(got.statusChanges).toEqual([]);
		expect(got.closeState).toEqual({ from: "open", to: "open" });
	});

	it("reports a status-only change in BOTH changed and statusChanges", async () => {
		const from = await Promise.all([
			comparisonMemory("j1", "adjust/aj-001", "decision", "pending_review", "Ajuste AJ-001", "ajuste por comprobante tardio"),
		]);
		const to = await Promise.all([
			reperiod(await comparisonMemory("a1", "adjust/aj-001", "decision", "approved", "Ajuste AJ-001", "ajuste por comprobante tardio"), "202608"),
		]);

		const got = await computePeriodComparison("202607", "202608", from, to, [], [], "open", "open");

		expect(got.chains.changed).toEqual([
			{ topicKey: "adjust/aj-001", fromId: "j1", toId: "a1", kind: "decision", title: "Ajuste AJ-001" },
		]);
		expect(got.chains.unchangedCount).toBe(0);
		expect(got.statusChanges).toEqual([
			{
				topicKey: "adjust/aj-001",
				fromId: "j1",
				toId: "a1",
				fromStatus: "pending_review",
				toStatus: "approved",
			},
		]);
	});

	it("never trips on write-time metadata (recordedAt/revision)", async () => {
		const fromMem = await comparisonMemory("j1", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%");
		const toMem = reperiod(await comparisonMemory("a1", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%"), "202608");
		fromMem.recordedAt = "2026-07-01T00:00:00Z";
		toMem.recordedAt = "2026-08-01T00:00:00Z";
		fromMem.revision = 1;
		toMem.revision = 2;

		const got = await computePeriodComparison("202607", "202608", [fromMem], [toMem], [], [], "open", "open");
		expect(got.chains.changed).toEqual([]);
		expect(got.chains.unchangedCount).toBe(1);
	});

	it("treats evidence/rule refs as SETS (order-insensitive, growth-sensitive)", async () => {
		const base = await comparisonMemory("j1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo");
		const reordered = reperiod(await comparisonMemory("a1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo"), "202608");
		reordered.evidenceRefs = ["xml/ventas.xml", "cdr/ventas.cdr"];
		base.evidenceRefs = ["cdr/ventas.cdr", "xml/ventas.xml"];
		const got = await computePeriodComparison("202607", "202608", [base], [reordered], [], [], "open", "open");
		expect(got.chains.changed).toEqual([]);
		expect(got.chains.unchangedCount).toBe(1);

		const grown = reperiod(await comparisonMemory("a2", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo"), "202608");
		grown.evidenceRefs = ["xml/ventas.xml", "cdr/ventas.cdr", "extracto/ventas.pdf"];
		const base2 = await comparisonMemory("j2", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo");
		base2.evidenceRefs = ["xml/ventas.xml", "cdr/ventas.cdr"];
		const got2 = await computePeriodComparison("202607", "202608", [base2], [grown], [], [], "open", "open");
		expect(got2.chains.changed).toHaveLength(1);
	});

	it("derives the pending-item digest delta keyed by chain", async () => {
		const fromPending: ClosePendingItem[] = [
			{ memoryId: "mem-pend-a", topicKey: "adjust/aj-001", kind: "", status: "", title: "", effectiveAt: "" },
			{ memoryId: "mem-pend-b", topicKey: "obligation/igv-621", kind: "", status: "", title: "", effectiveAt: "" },
		];
		const toPending: ClosePendingItem[] = [
			{ memoryId: "mem-pend-b2", topicKey: "obligation/igv-621", kind: "", status: "", title: "", effectiveAt: "" },
			{ memoryId: "mem-pend-c", topicKey: "exception/banco-002", kind: "", status: "", title: "", effectiveAt: "" },
		];

		const got = await computePeriodComparison("202607", "202608", [], [], fromPending, toPending, "open", "open");

		expect(got.pendingItems).toEqual({
			from: 2,
			to: 2,
			delta: 0,
			addedIds: ["mem-pend-c"],
			resolvedIds: ["mem-pend-a"],
		});
	});

	it("carries the close state pair", async () => {
		const got = await computePeriodComparison("202607", "202608", [], [], [], [], "closed", "open");
		expect(got.closeState).toEqual({ from: "closed", to: "open" });
	});

	it("stable-sorts arrays by topic key then memory ID", async () => {
		const from = await Promise.all([
			comparisonMemory("jz", "topic/zeta", "fact", "active", "Z", "contenido z"),
			comparisonMemory("ja", "topic/alfa", "fact", "active", "A", "contenido a"),
		]);
		const to = await Promise.all([
			reperiod(await comparisonMemory("az", "topic/zeta", "fact", "active", "Z", "contenido z CAMBIADO"), "202608"),
			reperiod(await comparisonMemory("ab", "topic/bravo", "fact", "active", "B", "contenido b"), "202608"),
			reperiod(await comparisonMemory("aa", "topic/alfa", "fact", "active", "A", "contenido a"), "202608"),
		]);

		const got = await computePeriodComparison("202607", "202608", from, to, [], [], "open", "open");

		expect(got.chains.new.map((c) => c.topicKey)).toEqual(["topic/bravo"]);
		expect(got.chains.removed).toEqual([]);
		expect(got.chains.changed.map((c) => c.topicKey)).toEqual(["topic/zeta"]);
		expect(got.chains.unchangedCount).toBe(1);
	});

	it("is deterministic with a fixed narrative delta summary", async () => {
		const from = await Promise.all([
			comparisonMemory("j1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo"),
			comparisonMemory("j2", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%"),
			comparisonMemory("j3", "obligation/igv-621", "obligation", "active", "Obligacion PDT 621", "declarar IGV julio"),
			comparisonMemory("j4", "exception/banco-001", "exception", "active", "Diferencia banco", "extracto vs libro"),
			comparisonMemory("j5", "adjust/aj-001", "decision", "pending_review", "Ajuste AJ-001", "ajuste por comprobante tardio"),
		]);
		const to = await Promise.all([
			reperiod(await comparisonMemory("a1", "account/4011", "fact", "active", "Ventas julio", "ventas del periodo CORREGIDAS"), "202608"),
			reperiod(await comparisonMemory("a2", "fact/igv-tasa", "fact", "active", "Tasa IGV", "tasa vigente 18%"), "202608"),
			reperiod(await comparisonMemory("a3", "account/4011/ventas-agosto", "fact", "active", "Ventas agosto", "ventas de agosto"), "202608"),
			reperiod(await comparisonMemory("a4", "adjust/aj-001", "decision", "approved", "Ajuste AJ-001", "ajuste por comprobante tardio"), "202608"),
		]);
		const fromPending: ClosePendingItem[] = [
			{ memoryId: "j5", topicKey: "adjust/aj-001", kind: "", status: "", title: "", effectiveAt: "" },
			{ memoryId: "j3", topicKey: "obligation/igv-621", kind: "", status: "", title: "", effectiveAt: "" },
			{ memoryId: "j4", topicKey: "exception/banco-001", kind: "", status: "", title: "", effectiveAt: "" },
		];
		const toPending: ClosePendingItem[] = [
			{ memoryId: "j3", topicKey: "obligation/igv-621", kind: "", status: "", title: "", effectiveAt: "" },
		];

		const first = await computePeriodComparison("202607", "202608", from, to, fromPending, toPending, "closed", "open");
		const second = await computePeriodComparison("202607", "202608", from, to, fromPending, toPending, "closed", "open");

		expect(JSON.stringify(first)).toBe(JSON.stringify(second));
		expect(first.narrative.length).toBeGreaterThan(0);
		expect(first.narrative).toContain("Comparacion 202607 → 202608");
		expect(first.narrative).toContain("items pendientes: 3 → 1 (delta -2)");
	});

	it("handles empty periods", async () => {
		const got = await computePeriodComparison("202607", "202608", [], [], [], [], "open", "open");
		expect(got.counts).toEqual({
			fromTotal: 0,
			toTotal: 0,
			delta: 0,
			byKindDelta: {},
			byStatusDelta: {},
		});
		expect(got.chains).toEqual({ new: [], removed: [], changed: [], unchangedCount: 0 });
		expect(got.statusChanges).toEqual([]);
	});
});
