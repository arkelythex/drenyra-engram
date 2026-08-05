/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * MaterialityLevel mirror tests (v0.4.0 Step 1): validation accepts known
 * levels and rejects unknown ones, cloning carries the declared level, and the
 * envelope hash stays byte-identical when the level is set (frozen decision —
 * the policy classifies by level; the hash bytes remain v0.3-identical).
 */

import { describe, expect, it } from "vitest";

import type {
	AccountingMemory,
	MaterialityLevel,
} from "../types.js";
import {
	assertValidMemory,
	cloneMemory,
	computeContentHash,
	computeEnvelopeHash,
	computeIdentityHash,
	MATERIALITY_LEVELS,
} from "../types.js";

function validMemory(): AccountingMemory {
	return {
		identity: { id: "mem-1", topicKey: "dk/acme/202601/fact-1" },
		title: "material adjustment",
		kind: "decision",
		scope: {
			kind: "company",
			organizationId: "tenant-1",
			companyId: "acme",
			ruc: "20100039201",
			period: "202601",
		},
		content: {
			what: "adjustment",
			why: "late document",
			where: "SUNAT",
			learned: "pending review",
		},
		status: "pending_review",
		fiscalEffect: "adjustment",
		effectiveAt: "2026-01-15T00:00:00Z",
		recordedAt: "2026-01-16T00:00:00Z",
		source: {
			system: "drenyra-core",
			actorId: "agent-1",
			actorKind: "agent",
		},
		contentHash: "content-hash",
		revision: 1,
	};
}

describe("MaterialityLevel mirror", () => {
	it("exposes the frozen const set", () => {
		expect(MATERIALITY_LEVELS).toEqual(["normal", "material", "critical"]);
	});

	it.each(["normal", "material", "critical"] as const)(
		"assertValidMemory accepts level %s",
		(level: MaterialityLevel) => {
			const memory = validMemory();
			memory.materialityLevel = level;
			expect(() => assertValidMemory(memory)).not.toThrow();
		},
	);

	it("treats NULL (unset) as valid and normal", () => {
		expect(() => assertValidMemory(validMemory())).not.toThrow();
	});

	it("assertValidMemory rejects an unknown level", () => {
		const memory = validMemory();
		memory.materialityLevel = "mega" as MaterialityLevel;
		expect(() => assertValidMemory(memory)).toThrow(/INVALID_MATERIALITY_LEVEL/);
	});

	it("cloneMemory carries the declared level defensively", () => {
		const memory = validMemory();
		memory.materialityLevel = "critical";
		const cloned = cloneMemory(memory);
		expect(cloned.materialityLevel).toBe("critical");
		memory.materialityLevel = "normal";
		expect(cloned.materialityLevel).toBe("critical");
	});

	it("envelope hash is byte-identical when materialityLevel is set", async () => {
		const base = validMemory();
		base.contentHash = await computeContentHash(base);
		base.identityHash = await computeIdentityHash(base);
		const baseHash = await computeEnvelopeHash(base);

		const raised = cloneMemory(base);
		raised.materialityLevel = "critical";
		const raisedHash = await computeEnvelopeHash(raised);

		expect(raisedHash).toBe(baseHash);
	});
});
