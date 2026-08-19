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
 * v0.4.0 Step 3 adds the Ed25519 receipt contract ("receipt"): the fixed seed
 * (32 bytes of 0x01) and its pinned canonical bytes/digests/signature - Go
 * signs and Node verifies, Node canonicalizes/signs and Go verifies (AC9/AC10).
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
	ReceiptPayload,
	SignedReceipt,
	VerifiedApprovalPrincipal,
} from "../types.js";
import {
	ApprovalError,
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
import {
	canonicalReceiptPayload,
	canonicalUnsignedEnvelope,
	completeReceiptBytes,
	receiptHash,
	receiptKeyId,
	receiptPayloadHash,
	signReceipt,
	verifyReceipt,
} from "../receipt.js";
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
import {
	reviewChecksRequired,
	sodViolation,
	validateReviewChecks,
} from "../../authz/approval-policy.js";
import {
	buildReconstructibilityCounts,
	classifyReconstructibility,
	isMaterialDecision,
} from "../reconstructibility.js";
import { foldTopicKey } from "../topic-fold.js";
import {
	LAYER_CHAIN_LINK,
	LAYER_ENVELOPE_INTEGRITY,
	LAYER_EVIDENCE_AVAILABILITY,
	LAYER_OBJECT_AVAILABILITY,
	LAYER_PAYLOAD_CANONICALIZATION,
	LAYER_PRINCIPAL_PROVENANCE,
	LAYER_RULE_AVAILABILITY,
	LAYER_RULE_VERSION_VIGENCIA,
	LAYER_SIGNATURE,
	LAYER_SIGNING_KEY_VALIDITY,
	LAYER_SUPERSESSION_CHAIN,
	LAYER_TENANT_COMPANY_SCOPE,
	type VerificationLayer,
	type VerificationOutcome,
	type VerificationReport,
	type VerificationStatus,
} from "../verify.js";

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

/**
 * The shared v0.4.0 Step 3 Ed25519 receipt vector (contract "receipt"): the
 * fixed seed (32 bytes of 0x01) and its derived RFC 8032 keypair, the full
 * memory_approved payload, the canonical payload bytes, the payload digest,
 * the unsigned envelope bytes Ed25519 signs, the raw signature (hex), the
 * complete receipt bytes, the chain digest and the signed envelope (signature
 * in padded base64 - the model form). All hex is lowercase. Both runtimes must
 * compute byte-identical values - the vector IS the AC9/AC10 cross-runtime
 * contract.
 */
interface GoldenReceipt {
	seed: string;
	publicKey: string;
	keyId: string;
	payload: ReceiptPayload;
	canonicalPayloadBytes: string;
	payloadHash: string;
	unsignedEnvelopeBytes: string;
	signature: string;
	completeReceiptBytes: string;
	receiptHash: string;
	signedReceipt: SignedReceipt;
}

interface GoldenCase {
	name: string;
	contract?:
		| "legacy-hash"
		| "approval-policy"
		| "approval-envelope"
		| "principal-snapshot"
		| "sod-policy"
		| "review-checks"
		| "judgment"
		| "receipt"
		| "reconstructibility"
		| "topic-fold";
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
	/** v0.4.0 Step 3 Ed25519 receipt vector (contract "receipt"). */
	receipt?: GoldenReceipt;
	/**
	 * v0.9.0 review-workspace clauses (design §5/§6): SODCases (contract
	 * "sod-policy") are the proposer/reviewer pairs the pure SoD clause
	 * evaluates; reviewChecksCases (contract "review-checks") are the
	 * materiality + declared-checks triples the review-checks clause evaluates.
	 */
	sodCases?: Array<{ proposer: string; reviewer: string; violation: boolean }>;
	reviewChecksCases?: Array<{
		materialityLevel: MaterialityLevel | null;
		evidenceInspected: boolean;
		ruleInspected: boolean;
		required: boolean;
		errorCode: string;
	}>;
	/** sdd-060-tenant-cli topic-fold vectors (contract "topic-fold"). */
	foldCases?: Array<{ input: string; expected: string }>;
	/**
	 * v1-readiness reconstructibility vectors (contract "reconstructibility"):
	 * the pure FZ-1 eligibility predicate, the FZ-2 classifier precedence and
	 * the frozen ratio/percentage — mirrored in core/reconstructibility.ts.
	 */
	reconstructibility?: {
		eligibilityCases?: Array<{
			name: string;
			memory: GoldenReconstructibilityMemory;
			scope: MemoryScope;
			isLatest: boolean;
			expected: boolean;
		}>;
		classifierCases?: Array<{
			name: string;
			memory: GoldenReconstructibilityMemory;
			layers: Record<string, string>;
			expectedReason: string;
			expectedReconstructible: boolean;
		}>;
		ratioCases?: Array<{
			name: string;
			numerator: number;
			denominator: number;
			expectedRatio: { numerator: number; denominator: number };
			expectedZeroDenominator: boolean;
			expectedPercentage: number | null;
		}>;
	};
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
/** Golden reconstructibility memory snapshot (mirrors goldenReconstructibilityMemory in Go). */
interface GoldenReconstructibilityMemory {
	id: string;
	topicKey: string;
	kind?: MemoryKind;
	status?: MemoryStatus;
	fiscalEffect?: FiscalEffect;
	materialityLevel?: MaterialityLevel | null;
	materiality?: number;
	revision?: number;
	scope?: MemoryScope;
}

/** Builds the vector memory; eligible cases omit scope and inherit the requested one. */
function goldenReconstructibilityMemory(
	g: GoldenReconstructibilityMemory,
	requestedScope: MemoryScope | undefined,
): AccountingMemory {
	const mem: AccountingMemory = {
		identity: { id: g.id, topicKey: g.topicKey },
		title: "vector",
		kind: (g.kind ?? "decision") as MemoryKind,
		scope: g.scope ?? {
			kind: "company",
			organizationId: "",
			companyId: "",
			ruc: "",
			period: "",
		},
		content: { what: "v", why: "v", where: "v", learned: "v" },
		status: (g.status ?? "approved") as MemoryStatus,
		fiscalEffect: (g.fiscalEffect ?? "journal_entry") as FiscalEffect,
		effectiveAt: "2026-01-01T00:00:00Z",
		recordedAt: "2026-01-01T00:00:00Z",
		contentHash: "",
		revision: g.revision ?? 1,
		source: { system: "golden", actorId: "golden", actorKind: "agent" },
	};
	if (g.materialityLevel !== undefined && g.materialityLevel !== null) {
		mem.materialityLevel = g.materialityLevel;
	}
	if (g.materiality !== undefined) {
		mem.materiality = BigInt(g.materiality);
	}
	if (g.scope === undefined && requestedScope) {
		// Eligible cases omit memory.scope and inherit the requested scope;
		// exclusion cases carry their own scope so scopeEquals fails.
		mem.scope = requestedScope;
	}
	return mem;
}

/** Builds a VerificationReport from the vector's layer map (name → status). */
function goldenReport(layers: Record<string, string>): VerificationReport {
	const names = [
		LAYER_PAYLOAD_CANONICALIZATION,
		LAYER_ENVELOPE_INTEGRITY,
		LAYER_SIGNATURE,
		LAYER_SIGNING_KEY_VALIDITY,
		LAYER_TENANT_COMPANY_SCOPE,
		LAYER_CHAIN_LINK,
		LAYER_PRINCIPAL_PROVENANCE,
		LAYER_SUPERSESSION_CHAIN,
		LAYER_EVIDENCE_AVAILABILITY,
		LAYER_OBJECT_AVAILABILITY,
		LAYER_RULE_AVAILABILITY,
		LAYER_RULE_VERSION_VIGENCIA,
	];
	const reportLayers: VerificationLayer[] = names.map((name) => ({
		name,
		status: (layers[name] ?? "passed") as VerificationStatus,
		detail: "",
	}));
	return {
		subjectType: "memory",
		subjectId: "vector",
		outcome: "passed" as VerificationOutcome,
		receipts: [],
		layers: reportLayers,
		accountingCorrectness: "Accounting correctness: NOT ASSERTED",
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
				case "receipt": {
					if (tc.receipt === undefined) {
						throw new Error(`${tc.name}: receipt vector requires receipt`);
					}
					const vec = tc.receipt;
					const seed = Buffer.from(vec.seed, "hex");
					const publicKey = Buffer.from(vec.publicKey, "hex");

					// AC10: Node canonicalizes the vector payload and the unsigned
					// envelope - byte-identical with Go's pinned bytes.
					expect(
						Buffer.from(canonicalReceiptPayload(vec.payload), "utf8").toString(
							"hex",
						),
					).toBe(vec.canonicalPayloadBytes);
					expect(receiptPayloadHash(vec.payload)).toBe(vec.payloadHash);
					expect(
						Buffer.from(
							canonicalUnsignedEnvelope(vec.signedReceipt),
							"utf8",
						).toString("hex"),
					).toBe(vec.unsignedEnvelopeBytes);

					// AC10: Node signs the SAME bytes with the seed via the RFC 8032
					// JWK path (the same path core/receipt.ts uses) - the deterministic
					// signature equals Go's pinned signature and the signed envelope
					// matches the vector's claim byte-for-byte.
					const { receipt: signed, publicKey: signedPublicKey } = signReceipt(
						vec.payload,
						seed,
					);
					expect(Buffer.from(signedPublicKey).toString("hex")).toBe(
						vec.publicKey,
					);
					expect(Buffer.from(signed.signature, "base64").toString("hex")).toBe(
						vec.signature,
					);
					expect(signed).toEqual(vec.signedReceipt);

					// Complete receipt bytes, chain digest and key id.
					expect(
						Buffer.from(
							completeReceiptBytes(vec.signedReceipt),
							"utf8",
						).toString("hex"),
					).toBe(vec.completeReceiptBytes);
					expect(receiptHash(vec.signedReceipt)).toBe(vec.receiptHash);
					expect(receiptKeyId(publicKey)).toBe(vec.keyId);

					// The vector IS the AC9/AC10 proof: Go's pinned signature claim
					// (reconstructed from the vector) verifies in Node.
					expect(() =>
						verifyReceipt(vec.signedReceipt, vec.payload, publicKey),
					).not.toThrow();
					break;
				}
				case "sod-policy": {
					if (!tc.sodCases) {
						throw new Error(`${tc.name}: sod-policy vector requires sodCases`);
					}
					for (const [i, c] of tc.sodCases.entries()) {
						const got = sodViolation(c.proposer, c.reviewer);
						expect(got, `${tc.name}: sodCases[${i}]`).toBe(c.violation);
					}
					break;
				}
				case "review-checks": {
					if (!tc.reviewChecksCases) {
						throw new Error(
							`${tc.name}: review-checks vector requires reviewChecksCases`,
						);
					}
					for (const [i, c] of tc.reviewChecksCases.entries()) {
						expect(
							reviewChecksRequired(c.materialityLevel ?? undefined),
							`${tc.name}: reviewChecksCases[${i}] required`,
						).toBe(c.required);
						let code: string | undefined;
						try {
							validateReviewChecks(c.materialityLevel ?? undefined, {
								evidenceInspected: c.evidenceInspected,
								ruleInspected: c.ruleInspected,
							});
						} catch (err) {
							code = err instanceof ApprovalError ? err.code : undefined;
						}
						if (c.errorCode === "") {
							expect(
								code,
								`${tc.name}: reviewChecksCases[${i}]`,
							).toBeUndefined();
						} else {
							expect(code, `${tc.name}: reviewChecksCases[${i}]`).toBe(
								c.errorCode,
							);
						}
					}
					break;
				}
				case "reconstructibility": {
					const data = tc.reconstructibility;
					if (!data) {
						throw new Error(
							`${tc.name}: reconstructibility vector requires data`,
						);
					}
					for (const c of data.eligibilityCases ?? []) {
						const mem = goldenReconstructibilityMemory(c.memory, c.scope);
						expect(
							isMaterialDecision(mem, c.scope, c.isLatest),
							`${tc.name}: eligibility ${c.name}`,
						).toBe(c.expected);
					}
					for (const c of data.classifierCases ?? []) {
						const mem = goldenReconstructibilityMemory(c.memory, undefined);
						const res = classifyReconstructibility(mem, goldenReport(c.layers));
						expect(
							res.reconstructible,
							`${tc.name}: classifier ${c.name} reconstructible`,
						).toBe(c.expectedReconstructible);
						expect(res.reason, `${tc.name}: classifier ${c.name} reason`).toBe(
							c.expectedReason,
						);
					}
					for (const c of data.ratioCases ?? []) {
						const counts = buildReconstructibilityCounts(
							c.denominator,
							c.numerator,
						);
						expect(counts.ratio, `${tc.name}: ratio ${c.name}`).toEqual(
							c.expectedRatio,
						);
						expect(
							counts.zeroDenominator,
							`${tc.name}: ratio ${c.name} zeroDenominator`,
						).toBe(c.expectedZeroDenominator);
						expect(
							counts.percentage,
							`${tc.name}: ratio ${c.name} percentage`,
						).toBe(c.expectedPercentage);
					}
    					break;
    				}
    				case "topic-fold": {
    					if (!tc.foldCases) {
    						throw new Error(
    							`${tc.name}: topic-fold vector requires foldCases`,
    						);
    					}
    					for (const [i, c] of tc.foldCases.entries()) {
    						expect(
    							foldTopicKey(c.input),
    							`${tc.name}: foldCases[${i}]`,
    						).toBe(c.expected);
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
