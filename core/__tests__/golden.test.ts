/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * SHARED golden vectors — the same testdata/golden/*.json files run from Go
 * (internal/core/golden_test.go) and from here. Go and TypeScript must agree
 * on the canonical hashes (content/identity/envelope), the approval gate and
 * the initial status. The expected hashes are FIXED values: a divergence
 * between runtimes fails one of the two runners, never silently.
 *
 * v0.4.0: each vector carries an optional `contract` discriminator. Legacy
 * vectors (no field, or "legacy-hash") keep the historical hash/gate
 * assertions; the new contracts run the PURE approval policy
 * ("approval-policy"), the reviewed/resulting envelope pair ("approval-envelope",
 * plus the post-review stale-envelope proof via linkedAfterReviewRefs), and the
 * canonical role order of a principal snapshot ("principal-snapshot").
 */

import { describe, expect, it } from "vitest";
import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";

import type {
	AccountingMemory,
	AccountingRole,
	AssuranceLevel,
	AuthenticationMethod,
	MaterialityLevel,
	MemoryStatus,
	VerifiedApprovalPrincipal,
} from "../types.js";
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
import {
	createVerifiedApprovalPrincipal,
	principalSnapshot,
} from "../../auth/principal.js";
import { authorizeApproval } from "../../authz/approval-policy.js";

interface GoldenPrincipal {
	subjectId: string;
	tenantId: string;
	membershipId: string;
	companyScopes: string[];
	roles: AccountingRole[];
	authenticationMethod: AuthenticationMethod;
	assuranceLevel: AssuranceLevel;
	authenticatedAt: string;
}

interface GoldenMemory {
	id: string;
	topicKey: string;
	title: string;
	kind: MemoryKind;
	scope: MemoryScope;
	content: MemoryContent;
	source: MemorySource;
	fiscalEffect: FiscalEffect;
	status: MemoryStatus;
	effectiveAt: string;
	recordedAt: string;
	evidenceRefs?: string[];
	ruleRefs?: string[];
	materialityLevel?: MaterialityLevel;
}

interface GoldenCase {
	name: string;
	contract?: "legacy-hash" | "approval-policy" | "approval-envelope" | "principal-snapshot";
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
	principal?: GoldenPrincipal;
	memory?: GoldenMemory;
	/** Evidence refs added AFTER the review — the stale-envelope proof. */
	linkedAfterReviewRefs?: string[];
	expected: {
		contentHash: string;
		identityHash: string;
		envelopeHash: string;
		initialStatus: string;
		canApproveAgent: boolean;
		canApproveHuman: boolean;
		allowed?: boolean;
		reasonCode?: string;
		policyVersion?: string;
		reviewedEnvelopeHash?: string;
		resultingEnvelopeHash?: string;
		actualEnvelopeHash?: string;
		canonicalRoles?: AccountingRole[];
	};
}

const goldenDir = resolve(process.cwd(), "testdata", "golden");

function loadCases(): GoldenCase[] {
	return readdirSync(goldenDir)
		.filter((file) => file.endsWith(".json"))
		.sort()
		.map((file) => JSON.parse(readFileSync(join(goldenDir, file), "utf-8")));
}

/** Canonical memory of a v0.4.0 vector — the same shape both runtimes consume. */
function buildGoldenMemory(mem: GoldenMemory): AccountingMemory {
	return {
		identity: { id: mem.id, topicKey: mem.topicKey },
		title: mem.title,
		kind: mem.kind,
		scope: mem.scope,
		content: mem.content,
		status: mem.status,
		fiscalEffect: mem.fiscalEffect,
		effectiveAt: mem.effectiveAt,
		recordedAt: mem.recordedAt,
		source: mem.source,
		...(mem.evidenceRefs === undefined
			? {}
			: { evidenceRefs: [...mem.evidenceRefs] }),
		...(mem.ruleRefs === undefined ? {} : { ruleRefs: [...mem.ruleRefs] }),
		...(mem.materialityLevel === undefined
			? {}
			: { materialityLevel: mem.materialityLevel }),
		contentHash: "",
		revision: 1,
	};
}

/** The vector's FIXED principal data — never derived from the memory. */
function principalFromVector(p: GoldenPrincipal): VerifiedApprovalPrincipal {
	return createVerifiedApprovalPrincipal({
		subjectId: p.subjectId,
		tenantId: p.tenantId,
		membershipId: p.membershipId,
		companyScopes: p.companyScopes,
		roles: p.roles,
		authenticationMethod: p.authenticationMethod,
		assuranceLevel: p.assuranceLevel,
		authenticatedAt: p.authenticatedAt,
	});
}

describe("shared golden vectors (Go ↔ TS parity)", () => {
	const cases = loadCases();
	expect(cases.length).toBeGreaterThan(0);

	for (const tc of cases) {
		it(tc.name, async () => {
			const contract = tc.contract ?? "legacy-hash";
			switch (contract) {
				case "legacy-hash": {
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
					break;
				}
				case "approval-policy": {
					if (tc.principal === undefined || tc.memory === undefined) {
						throw new Error(`${tc.name}: approval-policy vector requires principal and memory`);
					}
					const decision = authorizeApproval(
						principalFromVector(tc.principal),
						buildGoldenMemory(tc.memory),
					);
					expect(decision.allowed).toBe(tc.expected.allowed);
					expect(decision.reasonCode).toBe(tc.expected.reasonCode);
					expect(decision.policyVersion).toBe(tc.expected.policyVersion);
					break;
				}
				case "approval-envelope": {
					if (tc.memory === undefined) {
						throw new Error(`${tc.name}: approval-envelope vector requires memory`);
					}
					const memory = buildGoldenMemory(tc.memory);
					memory.contentHash = await computeContentHash(memory);
					const reviewed = await computeEnvelopeHash(memory);
					const resulting = await computeEnvelopeHash({
						...memory,
						status: "approved",
					});
					expect(reviewed).toBe(tc.expected.reviewedEnvelopeHash);
					expect(resulting).toBe(tc.expected.resultingEnvelopeHash);
					// Status participates in the envelope: H1 and H2 must differ.
					expect(reviewed).not.toBe(resulting);
					if (
						tc.linkedAfterReviewRefs !== undefined &&
						tc.linkedAfterReviewRefs.length > 0
					) {
						const actual = await computeEnvelopeHash({
							...memory,
							evidenceRefs: [
								...(memory.evidenceRefs ?? []),
								...tc.linkedAfterReviewRefs,
							],
						});
						expect(actual).toBe(tc.expected.actualEnvelopeHash);
						// Stale-review proof: a post-review link changes the envelope.
						expect(actual).not.toBe(reviewed);
					}
					break;
				}
				case "principal-snapshot": {
					if (tc.principal === undefined) {
						throw new Error(`${tc.name}: principal-snapshot vector requires principal`);
					}
					expect(
						principalSnapshot(principalFromVector(tc.principal)).roles,
					).toEqual(tc.expected.canonicalRoles);
					break;
				}
				default: {
					throw new Error(`${tc.name}: unknown golden contract "${contract}"`);
				}
			}
		});
	}
});
