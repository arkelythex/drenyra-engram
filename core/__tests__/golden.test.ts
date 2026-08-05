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
	AccountingJudgment,
	AccountingMemory,
	AccountingRole,
	AssuranceLevel,
	AuthenticationMethod,
	JudgmentStatus,
	MaterialityLevel,
	MemoryStatus,
	PrincipalSnapshot,
	VerifiedApprovalPrincipal,
} from "../types.js";
import {
	computeContentHash,
	computeEnvelopeHash,
	computeIdentityHash,
	computeJudgmentHash,
	type MemoryScope,
	type MemorySource,
	type MemoryContent,
	type MemoryKind,
	type FiscalEffect,
	type MemoryRelation,
} from "../types.js";
import { approve, initialStatus } from "../../lifecycle/transitions.js";
import {
	createVerifiedApprovalPrincipal,
	principalSnapshot,
} from "../../auth/principal.js";
import { authorizeApproval } from "../../authz/approval-policy.js";
import {
	authorizeJudgment,
	JUDGMENT_POLICY_VERSION,
} from "../../authz/judgment-policy.js";

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

/**
 * Canonical judgment record of a v0.4.0 vector (contract "judgment") - the same
 * shape both runtimes consume. Status tells which state's lifecycle predicates
 * the vector asserts; the confirmed state is always DERIVED by the harness
 * (status=confirmed + the vector's resolution + the canonical snapshot of the
 * vector's authorizing principal + the frozen policy version + the vector's
 * decidedAt), never read from the JSON (ADR-003: only the factory path mints a
 * principal).
 */
interface GoldenJudgment {
	id: string;
	tenantId: string;
	companyId: string;
	fiscalPeriodId?: string;
	fromId: string;
	toId: string;
	relation: MemoryRelation;
	status: JudgmentStatus;
	proposer: MemorySource;
	proposalReason: string;
	predecessorId?: string;
	proposedAt: string;
	updatedAt: string;
}

interface GoldenCase {
	name: string;
	contract?:
		| "legacy-hash"
		| "approval-policy"
		| "approval-envelope"
		| "principal-snapshot"
		| "judgment";
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
	/** v0.4.0 judgment contract: the record plus the confirmation facts. */
	judgment?: GoldenJudgment;
	/** The confirmed judgment being corrected (correction vector only). */
	predecessor?: GoldenJudgment;
	resolution?: string;
	decidedAt?: string;
	supersededAt?: string;
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
		proposedJudgmentHash?: string;
		confirmedJudgmentHash?: string;
		supersededJudgmentHash?: string;
		canPropose?: boolean;
		canConfirm?: boolean;
		canReject?: boolean;
		canWithdraw?: boolean;
		canSupersedeConfirmed?: boolean;
		predecessorCanSupersedeConfirmed?: boolean;
		predecessorTerminalAfterSupersede?: boolean;
		agentConfirmErrorCode?: string;
		immutable?: boolean;
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

/**
 * Test-only mirror of core.CanPropose: only agent|system Sources may propose
 * (provenance never authorizes). The production TS mirror of the pure judgment
 * transitions lands with the lifecycle work; these helpers keep the SHARED
 * vector assertions byte-identical with Go without duplicating production logic
 * outside the harness.
 */
function canPropose(source: MemorySource): boolean {
	return source.actorKind === "agent" || source.actorKind === "system";
}

/** Mirror of core.CanConfirm: proposed only. */
function canConfirm(status: JudgmentStatus): boolean {
	return status === "proposed";
}

/** Mirror of core.CanRejectJudgment: proposed only. */
function canReject(status: JudgmentStatus): boolean {
	return status === "proposed";
}

/** Mirror of core.CanWithdraw: proposed only. */
function canWithdraw(status: JudgmentStatus): boolean {
	return status === "proposed";
}

/** Mirror of core.CanSupersedeConfirmed: confirmed only. */
function canSupersedeConfirmed(status: JudgmentStatus): boolean {
	return status === "confirmed";
}

/**
 * Test-only mirror of the Go pure confirm guard (core.ConfirmJudgment): the
 * first failing frozen code wins - INVALID_JUDGMENT_TRANSITION (status), then
 * RESOLUTION_REQUIRED (blank resolution), then AUTHENTICATION_REQUIRED (no
 * verified principal). An agent Source can never be a VerifiedApprovalPrincipal
 * (ADR-003 factory path), so the agent-shaped confirm fails with
 * AUTHENTICATION_REQUIRED - provenance is never authority.
 */
function confirmGuardErrorCode(
	status: JudgmentStatus,
	resolution: string,
	hasVerifiedPrincipal: boolean,
): string {
	if (status !== "proposed") {
		return "INVALID_JUDGMENT_TRANSITION";
	}
	if (resolution.trim().length === 0) {
		return "RESOLUTION_REQUIRED";
	}
	if (!hasVerifiedPrincipal) {
		return "AUTHENTICATION_REQUIRED";
	}
	return "";
}

/** The vector's judgment record as a core.AccountingJudgment. */
function buildJudgment(j: GoldenJudgment): AccountingJudgment {
	return {
		id: j.id,
		tenantId: j.tenantId,
		companyId: j.companyId,
		...(j.fiscalPeriodId === undefined
			? {}
			: { fiscalPeriodId: j.fiscalPeriodId }),
		fromId: j.fromId,
		toId: j.toId,
		relation: j.relation,
		status: j.status,
		proposer: { ...j.proposer },
		proposalReason: j.proposalReason,
		...(j.predecessorId === undefined
			? {}
			: { predecessorId: j.predecessorId }),
		proposedAt: j.proposedAt,
		updatedAt: j.updatedAt,
	};
}

/**
 * The DERIVED confirmed state of a vector: status=confirmed, the vector's
 * resolution, the canonical adjudicator snapshot of the authorizing principal,
 * the frozen judgment policy version and the vector's decidedAt. Mirrors
 * goldenJudgment.confirmedState (Go); updatedAt is set to decidedAt exactly as
 * the pure transition does and never participates in the hash.
 */
function confirmedJudgmentState(
	j: AccountingJudgment,
	snapshot: PrincipalSnapshot,
	resolution: string,
	decidedAt: string,
): AccountingJudgment {
	return {
		...j,
		status: "confirmed",
		resolution,
		adjudicator: snapshot,
		policyVersion: JUDGMENT_POLICY_VERSION,
		decidedAt,
		updatedAt: decidedAt,
	};
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
							approve(memory, {
								actor: "a",
								actorKind: "agent",
								timestamp: "t",
							});
							return true;
						} catch {
							return false;
						}
					})();
					const humanApproves = (() => {
						try {
							approve(memory, {
								actor: "h",
								actorKind: "human",
								timestamp: "t",
							});
							return true;
						} catch {
							return false;
						}
					})();

					expect(initialStatus(input.fiscalEffect)).toBe(
						tc.expected.initialStatus,
					);
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
						throw new Error(
							`${tc.name}: approval-policy vector requires principal and memory`,
						);
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
						throw new Error(
							`${tc.name}: approval-envelope vector requires memory`,
						);
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
						throw new Error(
							`${tc.name}: principal-snapshot vector requires principal`,
						);
					}
					expect(
						principalSnapshot(principalFromVector(tc.principal)).roles,
					).toEqual(tc.expected.canonicalRoles);
					break;
				}
				case "judgment": {
					if (tc.judgment === undefined) {
						throw new Error(`${tc.name}: judgment vector requires judgment`);
					}
					const raw = buildJudgment(tc.judgment);

					// Canonical reviewed/proposed hash - the same bytes the sibling
					// vectors sharing this proposed judgment pin.
					const proposedHash = await computeJudgmentHash({
						...raw,
						status: "proposed",
						resolution: undefined,
						decidedAt: undefined,
					});
					if (tc.expected.proposedJudgmentHash !== undefined) {
						expect(proposedHash).toBe(tc.expected.proposedJudgmentHash);
					}

					// Lifecycle predicates against the vector's recorded state.
					if (tc.expected.canPropose !== undefined) {
						expect(canPropose(raw.proposer)).toBe(tc.expected.canPropose);
					}
					if (tc.expected.canConfirm !== undefined) {
						expect(canConfirm(raw.status)).toBe(tc.expected.canConfirm);
					}
					if (tc.expected.canReject !== undefined) {
						expect(canReject(raw.status)).toBe(tc.expected.canReject);
					}
					if (tc.expected.canWithdraw !== undefined) {
						expect(canWithdraw(raw.status)).toBe(tc.expected.canWithdraw);
					}
					if (tc.expected.canSupersedeConfirmed !== undefined) {
						expect(canSupersedeConfirmed(raw.status)).toBe(
							tc.expected.canSupersedeConfirmed,
						);
					}

					// Agent-confirm proof: an agent-shaped confirm (no verified
					// principal) fails closed with AUTHENTICATION_REQUIRED, and the
					// vector can never even carry a principal - the policy decision
					// for the agent is impossible by construction.
					if (tc.expected.agentConfirmErrorCode !== undefined) {
						if (tc.resolution === undefined) {
							throw new Error(
								`${tc.name}: agentConfirmErrorCode requires a resolution`,
							);
						}
						expect(tc.principal).toBeUndefined();
						expect(
							confirmGuardErrorCode(raw.status, tc.resolution, false),
						).toBe(tc.expected.agentConfirmErrorCode);
					}

					// Confirmed state + policy (only with an authorizing principal).
					if (tc.principal !== undefined) {
						const principal = principalFromVector(tc.principal);
						if (
							tc.resolution !== undefined &&
							tc.decidedAt !== undefined &&
							tc.predecessor === undefined
						) {
							const confirmed = confirmedJudgmentState(
								raw,
								principalSnapshot(principal),
								tc.resolution,
								tc.decidedAt,
							);
							if (tc.expected.confirmedJudgmentHash !== undefined) {
								expect(await computeJudgmentHash(confirmed)).toBe(
									tc.expected.confirmedJudgmentHash,
								);
							}
						}
						const decision = authorizeJudgment(principal, raw);
						if (tc.expected.allowed !== undefined) {
							expect(decision.allowed).toBe(tc.expected.allowed);
						}
						if (tc.expected.reasonCode !== undefined) {
							expect(decision.reasonCode).toBe(tc.expected.reasonCode);
						}
						if (tc.expected.policyVersion !== undefined) {
							expect(decision.policyVersion).toBe(tc.expected.policyVersion);
						}
					}

					// Predecessor supersession (correction vector): the confirmed
					// predecessor can be superseded ONLY by the confirming correction,
					// and once superseded it is terminal. The superseded hash is the
					// reviewed shape with status=superseded (decided fields never
					// participate; routing/updatedAt neither).
					if (tc.predecessor !== undefined) {
						if (
							tc.principal === undefined ||
							tc.resolution === undefined ||
							tc.decidedAt === undefined ||
							tc.supersededAt === undefined
						) {
							throw new Error(
								`${tc.name}: predecessor supersession requires principal, resolution, decidedAt and supersededAt`,
							);
						}
						const principal = principalFromVector(tc.principal);
						const predConfirmed = confirmedJudgmentState(
							buildJudgment(tc.predecessor),
							principalSnapshot(principal),
							tc.resolution,
							tc.decidedAt,
						);
						if (tc.expected.confirmedJudgmentHash !== undefined) {
							expect(await computeJudgmentHash(predConfirmed)).toBe(
								tc.expected.confirmedJudgmentHash,
							);
						}
						if (tc.expected.predecessorCanSupersedeConfirmed !== undefined) {
							expect(canSupersedeConfirmed(predConfirmed.status)).toBe(
								tc.expected.predecessorCanSupersedeConfirmed,
							);
						}
						// A confirmed record is terminal except for the supersede route.
						expect(canConfirm(predConfirmed.status)).toBe(false);
						expect(canReject(predConfirmed.status)).toBe(false);
						expect(canWithdraw(predConfirmed.status)).toBe(false);
						// Superseding routes readers to the correction (supersedesId)
						// and makes the predecessor terminal.
						const predSuperseded: AccountingJudgment = {
							...predConfirmed,
							status: "superseded",
							supersedesId: tc.judgment.id,
							updatedAt: tc.supersededAt,
						};
						if (tc.expected.supersededJudgmentHash !== undefined) {
							expect(await computeJudgmentHash(predSuperseded)).toBe(
								tc.expected.supersededJudgmentHash,
							);
						}
						if (tc.expected.predecessorTerminalAfterSupersede !== undefined) {
							expect(predSuperseded.status).toBe("superseded");
							expect(canConfirm(predSuperseded.status)).toBe(false);
							expect(canReject(predSuperseded.status)).toBe(false);
							expect(canWithdraw(predSuperseded.status)).toBe(false);
						}
					}

					// Immutability proof: the confirmed hash is STABLE (recomputing
					// from the same fields yields the same hash) and editing ANY
					// adjudication field (resolution, decidedAt) yields a DIFFERENT
					// hash - the record the hash protects cannot change silently.
					if (tc.expected.immutable === true) {
						if (
							tc.principal === undefined ||
							tc.resolution === undefined ||
							tc.decidedAt === undefined
						) {
							throw new Error(
								`${tc.name}: immutable proof requires principal, resolution and decidedAt`,
							);
						}
						const principal = principalFromVector(tc.principal);
						const confirmed = confirmedJudgmentState(
							raw,
							principalSnapshot(principal),
							tc.resolution,
							tc.decidedAt,
						);
						const a = await computeJudgmentHash(confirmed);
						const b = await computeJudgmentHash(confirmed);
						expect(a).toBe(b);
						if (tc.expected.confirmedJudgmentHash !== undefined) {
							expect(a).toBe(tc.expected.confirmedJudgmentHash);
						}
						const changedResolution = await computeJudgmentHash({
							...confirmed,
							resolution: "a different professional resolution",
						});
						expect(changedResolution).not.toBe(a);
						const changedDecidedAt = await computeJudgmentHash({
							...confirmed,
							decidedAt: "2026-08-05T15:00:00Z",
						});
						expect(changedDecidedAt).not.toBe(a);
					}
					break;
				}
				default: {
					throw new Error(`${tc.name}: unknown golden contract "${contract}"`);
				}
			}
		});
	}
});
