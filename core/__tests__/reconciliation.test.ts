/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Reconciliation model mirror tests (v0.5.0, design §3) — the exact TS mirror of
 * the Go contract in `internal/core/reconciliation.go`: the closed status set,
 * the lifecycle transition matrix, the engine-derived variance, the canonical
 * reconciliation hash (byte-identical with Go, pinned to the shared vectors) and
 * the status-shaped hash coverage.
 */

import { describe, expect, it } from "vitest";

import type { PrincipalSnapshot, Reconciliation } from "../types.js";
import {
	RECONCILIATION_STATUSES,
	canConfirmReconciliation,
	canRejectReconciliation,
	canSupersedeReconciliation,
	canWithdrawReconciliation,
	computeReconciliationHash,
	isLegalReconciliationTransition,
	isValidReconciliationStatus,
} from "../types.js";

/**
 * The canonical proposed reconciliation of the hash vectors: the same fixture in
 * Go (internal/core/reconciliation_test.go baseReconciliation) must produce the
 * SAME pinned hex below.
 */
function baseReconciliation(
	overrides?: Partial<Reconciliation>,
): Reconciliation {
	return {
		id: "reconciliation-1",
		tenantId: "tenant-1",
		companyId: "acme",
		fiscalPeriodId: "202601",
		leftMemoryId: "obs-1",
		rightMemoryId: "obs-2",
		method: "trial-balance",
		currency: "PEN",
		leftAmountCents: 150000n,
		rightAmountCents: 149500n,
		varianceCents: 500n,
		toleranceCents: 1000n,
		status: "proposed",
		proposer: { system: "sire", actorId: "agent-7", actorKind: "agent" },
		proposalReason: "bank statement matches ledger within tolerance",
		proposedAt: "2026-08-05T12:00:00Z",
		...overrides,
	};
}

/** The canonical adjudicator snapshot of the confirmed vector (Go parity). */
function snapshot(): PrincipalSnapshot {
	return {
		subjectId: "subject-1",
		membershipId: "membership-1",
		roles: ["controller"],
		authenticationMethod: "session",
		assuranceLevel: "standard",
		authenticatedAt: "2026-08-05T13:00:00Z",
	};
}

function confirmedReconciliation(): Reconciliation {
	return baseReconciliation({
		status: "confirmed",
		resolution: "ledger matches bank statement; variance within tolerance",
		adjudicator: snapshot(),
		policyVersion: "reconciliation-policy/v0.5.0",
		decidedAt: "2026-08-05T13:00:00Z",
	});
}

const ALL_STATUSES = RECONCILIATION_STATUSES;

describe("reconciliation model mirror (v0.5.0)", () => {
	it("exposes exactly the five closed statuses and rejects unknown ones", () => {
		expect(ALL_STATUSES).toEqual([
			"proposed",
			"confirmed",
			"rejected",
			"withdrawn",
			"superseded",
		]);
		for (const status of ALL_STATUSES) {
			expect(isValidReconciliationStatus(status)).toBe(true);
		}
		expect(isValidReconciliationStatus("draft")).toBe(false);
		expect(isValidReconciliationStatus("")).toBe(false);
	});

	it("freezes the lifecycle transition matrix", () => {
		const legal: Record<Reconciliation["status"], Reconciliation["status"][]> =
			{
				proposed: ["confirmed", "rejected", "withdrawn", "superseded"],
				confirmed: ["superseded"],
				rejected: [],
				withdrawn: [],
				superseded: [],
			};
		for (const from of ALL_STATUSES) {
			for (const to of ALL_STATUSES) {
				expect(isLegalReconciliationTransition(from, to)).toBe(
					legal[from].includes(to),
				);
			}
		}
	});

	it("freezes the lifecycle predicates (proposed only / confirmed only)", () => {
		expect(canConfirmReconciliation("proposed")).toBe(true);
		expect(canRejectReconciliation("proposed")).toBe(true);
		expect(canWithdrawReconciliation("proposed")).toBe(true);
		expect(canSupersedeReconciliation("confirmed")).toBe(true);
		for (const status of [
			"confirmed",
			"rejected",
			"withdrawn",
			"superseded",
		] as const) {
			expect(canConfirmReconciliation(status)).toBe(false);
			expect(canRejectReconciliation(status)).toBe(false);
			expect(canWithdrawReconciliation(status)).toBe(false);
		}
		for (const status of [
			"proposed",
			"rejected",
			"withdrawn",
			"superseded",
		] as const) {
			expect(canSupersedeReconciliation(status)).toBe(false);
		}
	});

	it("keeps the variance engine-derived (left − right)", () => {
		const r = baseReconciliation();
		expect(r.varianceCents).toBe(r.leftAmountCents - r.rightAmountCents);
		expect(r.varianceCents).toBe(500n);
	});

	it("pins the canonical byte vectors shared with Go", async () => {
		// These hex strings are the SAME constants the Go test
		// (TestComputeReconciliationHash + the v05-parity fixture) asserts —
		// cross-runtime byte parity.
		await expect(computeReconciliationHash(baseReconciliation())).resolves.toBe(
			"3f57f9ac0457ad9d391cdd188fecc581bd77805c196041908459a3e850307000",
		);
		await expect(
			computeReconciliationHash(confirmedReconciliation()),
		).resolves.toBe(
			"0c163b541c86acfea21bd50ceb19212ae7e60a11518add4ebcd1868e47d4b0ed",
		);
	});

	it("is deterministic for the same input", async () => {
		const a = await computeReconciliationHash(baseReconciliation());
		const b = await computeReconciliationHash(baseReconciliation());
		expect(a).toBe(b);
	});

	it("changes the hash when the status moves proposed → confirmed", async () => {
		await expect(
			computeReconciliationHash(confirmedReconciliation()),
		).resolves.not.toBe(await computeReconciliationHash(baseReconciliation()));
	});

	it("covers the adjudication fields in the confirmed hash", async () => {
		const a = confirmedReconciliation();
		const b = confirmedReconciliation();
		b.resolution = "different professional resolution";
		await expect(computeReconciliationHash(a)).resolves.not.toBe(
			await computeReconciliationHash(b),
		);
		const c = confirmedReconciliation();
		c.decidedAt = "2026-08-05T15:00:00Z";
		await expect(computeReconciliationHash(a)).resolves.not.toBe(
			await computeReconciliationHash(c),
		);
	});

	it("covers the pair/amounts/method/currency/tolerance in the proposed hash", async () => {
		const mutations: Array<[string, Partial<Reconciliation>]> = [
			["amount", { leftAmountCents: 150001n }],
			["currency", { currency: "USD" }],
			["method", { method: "bank-statement" }],
			["tolerance", { toleranceCents: 0n }],
			["rightMemory", { rightMemoryId: "obs-9" }],
		];
		const base = await computeReconciliationHash(baseReconciliation());
		for (const [name, change] of mutations) {
			await expect(
				computeReconciliationHash(baseReconciliation(change)),
			).resolves.not.toBe(base);
			expect(name).toBeTruthy();
		}
	});

	it("never lets routing fields participate (supersedesId alone)", async () => {
		const routed = confirmedReconciliation();
		routed.supersedesId = "reconciliation-9";
		await expect(computeReconciliationHash(routed)).resolves.toBe(
			await computeReconciliationHash(confirmedReconciliation()),
		);
	});

	it("covers the engine-derived variance", async () => {
		const noVariance = baseReconciliation({ varianceCents: 0n });
		await expect(computeReconciliationHash(noVariance)).resolves.not.toBe(
			await computeReconciliationHash(baseReconciliation()),
		);
	});

	it("carries the proposed amounts as BigInt cents (never float)", () => {
		const r = baseReconciliation();
		expect(typeof r.leftAmountCents).toBe("bigint");
		expect(typeof r.rightAmountCents).toBe("bigint");
		expect(typeof r.toleranceCents).toBe("bigint");
	});
});
