/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Judgment model mirror tests (v0.4.0 Step 2) — the exact TS mirror of the Go
 * contract in `internal/core/judgment.go`: proposable relations, the canonical
 * judgment hash (byte-identical with Go, pinned to the shared vectors) and the
 * status-shaped hash coverage.
 */

import { describe, expect, it } from "vitest";

import type {
	AccountingJudgment,
	MemoryRelation,
	PrincipalSnapshot,
} from "../types.js";
import {
	computeJudgmentHash,
	isProposableRelation,
	proposableRelations,
} from "../types.js";

/**
 * The canonical proposed judgment of the hash vectors: the same fixture in Go
 * (internal/core/judgment_test.go baseJudgment) must produce the SAME pinned
 * hex below.
 */
function baseJudgment(
	overrides?: Partial<AccountingJudgment>,
): AccountingJudgment {
	return {
		id: "judgment-1",
		tenantId: "tenant-1",
		companyId: "acme",
		fiscalPeriodId: "202601",
		fromId: "obs-1",
		toId: "obs-2",
		relation: "contradicts",
		status: "proposed",
		proposer: { system: "sire", actorId: "agent-7", actorKind: "agent" },
		proposalReason: "igv rate mismatch",
		proposedAt: "2026-08-05T12:00:00Z",
		updatedAt: "2026-08-05T12:00:00Z",
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

function confirmedJudgment(): AccountingJudgment {
	return baseJudgment({
		status: "confirmed",
		resolution: "igv rate confirmed at 18 percent",
		adjudicator: snapshot(),
		policyVersion: "judgment-policy/v0.4.0",
		decidedAt: "2026-08-05T13:00:00Z",
		updatedAt: "2026-08-05T13:00:00Z",
	});
}

const SIX_PROPOSABLE: readonly MemoryRelation[] = [
	"supports",
	"contradicts",
	"explains",
	"reconciles",
	"reverses",
	"supersedes",
];

describe("judgment model mirror (v0.4.0 Step 2)", () => {
	it("exposes exactly the six proposable relations in fixed order", () => {
		expect(proposableRelations()).toEqual([...SIX_PROPOSABLE]);
		for (const relation of SIX_PROPOSABLE) {
			expect(isProposableRelation(relation)).toBe(true);
		}
		// conflicts_with is a legacy sync/discovery marker — never proposable.
		expect(isProposableRelation("conflicts_with")).toBe(false);
		expect(isProposableRelation("related")).toBe(false);
		expect(isProposableRelation("derived_from")).toBe(false);
		expect(isProposableRelation("not_conflict")).toBe(false);
		expect(isProposableRelation("approved_by")).toBe(false);
	});

	it("pins the canonical byte vectors shared with Go", async () => {
		// These hex strings are the SAME constants the Go test
		// (TestComputeJudgmentHash) asserts — cross-runtime byte parity.
		await expect(computeJudgmentHash(baseJudgment())).resolves.toBe(
			"62cf0ff6d9d5531d771008ce0cb6dbe6d474adbda11d51e8bdff7a0d93409ad8",
		);
		await expect(computeJudgmentHash(confirmedJudgment())).resolves.toBe(
			"9a43192f84d067fd3dafde45a1e64f80f3ae5e24a9cb274ccf44bd335891aaad",
		);
	});

	it("is deterministic for the same input", async () => {
		const a = await computeJudgmentHash(baseJudgment());
		const b = await computeJudgmentHash(baseJudgment());
		expect(a).toBe(b);
	});

	it("changes the hash when the status moves proposed → confirmed", async () => {
		const proposed = await computeJudgmentHash(baseJudgment());
		const confirmed = await computeJudgmentHash(confirmedJudgment());
		expect(proposed).not.toBe(confirmed);
	});

	it("covers resolution in the confirmed hash", async () => {
		const a = confirmedJudgment();
		const b = confirmedJudgment();
		b.resolution = "different professional resolution";
		await expect(computeJudgmentHash(a)).resolves.not.toBe(
			await computeJudgmentHash(b),
		);
	});

	it("covers decidedAt in the confirmed hash", async () => {
		const a = confirmedJudgment();
		const b = confirmedJudgment();
		b.decidedAt = "2026-08-05T15:00:00Z";
		await expect(computeJudgmentHash(a)).resolves.not.toBe(
			await computeJudgmentHash(b),
		);
	});

	it("covers proposalReason in the proposed hash", async () => {
		const a = baseJudgment();
		const b = baseJudgment();
		b.proposalReason = "a different mismatch";
		await expect(computeJudgmentHash(a)).resolves.not.toBe(
			await computeJudgmentHash(b),
		);
	});

	it("ignores resolution, updatedAt and supersedesId in the reviewed shape", async () => {
		const a = baseJudgment();
		const b = baseJudgment({
			resolution: "must not participate in the reviewed hash",
			updatedAt: "2026-08-05T23:59:59Z",
			supersedesId: "judgment-9",
		});
		await expect(computeJudgmentHash(a)).resolves.toBe(
			await computeJudgmentHash(b),
		);
	});

    	it("omits empty optional proposer fields from the canonical JSON", async () => {
    		const a = baseJudgment({
    			proposer: { system: "sire", actorKind: "agent" },
    		});
    		// Explicitly-empty optional fields are canonically equivalent to omitted
    		// ones (Go parity: internal/core/judgment_test.go).
    		const b = baseJudgment({
    			proposer: {
    				system: "sire",
    				actorId: "",
    				actorKind: "agent",
    				model: "",
    				reference: "",
    				session: "",
    			},
    		});
    		await expect(computeJudgmentHash(a)).resolves.toBe(
    			await computeJudgmentHash(b),
    		);
    	});
});
