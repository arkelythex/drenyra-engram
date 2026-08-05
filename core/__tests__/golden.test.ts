/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * SHARED golden vectors — the same testdata/golden/*.json files run from Go
 * (internal/core/golden_test.go) and from here. Go and TypeScript must agree
 * on the canonical hashes (content/identity/envelope), the approval gate and
 * the initial status. The expected hashes are FIXED values: a divergence
 * between runtimes fails one of the two runners, never silently.
 */

import { describe, expect, it } from "vitest";
import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";

import type { AccountingMemory, MemoryStatus } from "../types.js";
import {
	computeContentHash,
	computeEnvelopeHash,
	computeIdentityHash,
	type MemoryScope,
	type MemorySource,
	type MemoryContent,
	type MemoryKind,
	type FiscalEffect,
} from "../types.js";
import { approve, initialStatus } from "../../lifecycle/transitions.js";

interface GoldenCase {
	name: string;
	description?: string;
	input: {
		id: string;
		topicKey: string;
		title: string;
		kind: MemoryKind;
		scope: MemoryScope;
		content: MemoryContent;
		fiscalEffect: FiscalEffect;
		effectiveAt: string;
		recordedAt: string;
		observedAt?: string;
		source: MemorySource;
		evidenceRefs?: string[];
		ruleRefs?: string[];
		confidence?: number;
		materiality?: number;
		receiptId?: string;
		supersedesId?: string;
		status?: MemoryStatus;
	};
	expected: {
		contentHash: string;
		identityHash: string;
		envelopeHash: string;
		initialStatus: string;
		canApproveAgent: boolean;
		canApproveHuman: boolean;
	};
}

const goldenDir = resolve(process.cwd(), "testdata", "golden");

function loadCases(): GoldenCase[] {
	return readdirSync(goldenDir)
		.filter((file) => file.endsWith(".json"))
		.sort()
		.map((file) => JSON.parse(readFileSync(join(goldenDir, file), "utf-8")));
}

describe("shared golden vectors (Go ↔ TS parity)", () => {
	const cases = loadCases();
	expect(cases.length).toBeGreaterThan(0);

	for (const tc of cases) {
		it(tc.name, async () => {
			const input = tc.input;
			const memory: AccountingMemory = {
				identity: { id: input.id, topicKey: input.topicKey },
				title: input.title,
				kind: input.kind,
				scope: input.scope,
				content: input.content,
				status: (input.status ??
					initialStatus(input.fiscalEffect)) as MemoryStatus,
				fiscalEffect: input.fiscalEffect,
				effectiveAt: input.effectiveAt,
				recordedAt: input.recordedAt,
				...(input.observedAt === undefined
					? {}
					: { observedAt: input.observedAt }),
				source: input.source,
				...(input.evidenceRefs === undefined
					? {}
					: { evidenceRefs: [...input.evidenceRefs] }),
				...(input.ruleRefs === undefined
					? {}
					: { ruleRefs: [...input.ruleRefs] }),
				...(input.confidence === undefined
					? {}
					: { confidence: input.confidence }),
				...(input.materiality === undefined
					? {}
					: { materiality: BigInt(input.materiality) }),
				...(input.receiptId === undefined
					? {}
					: { receiptId: input.receiptId }),
				...(input.supersedesId === undefined
					? {}
					: { supersedesId: input.supersedesId }),
				contentHash: "",
				revision: 1,
			};

			const contentHash = await computeContentHash(memory);
			const identityHash = await computeIdentityHash(memory);
			memory.contentHash = contentHash;
			const envelopeHash = await computeEnvelopeHash(memory);

			// The approval gate: agents never approve; humans approve pending_review.
			const agentApproves = (() => {
				try {
					approve(memory, { actor: "a", actorKind: "agent", timestamp: "t" });
					return true;
				} catch {
					return false;
				}
			})();
			const humanApproves = (() => {
				try {
					approve(memory, { actor: "h", actorKind: "human", timestamp: "t" });
					return true;
				} catch {
					return false;
				}
			})();

			expect(initialStatus(input.fiscalEffect)).toBe(tc.expected.initialStatus);
			expect(agentApproves).toBe(tc.expected.canApproveAgent);
			expect(humanApproves).toBe(tc.expected.canApproveHuman);

			// FIXED hashes — identical values in Go and TS (the shared contract).
			expect(contentHash).toBe(tc.expected.contentHash);
			expect(identityHash).toBe(tc.expected.identityHash);
			expect(envelopeHash).toBe(tc.expected.envelopeHash);
		});
	}
});
