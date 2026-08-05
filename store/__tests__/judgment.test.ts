/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Judgment critical-section mirror tests (v0.4.0 Step 2) — the exact TS mirror
 * of the SQLiteStore adjudication path (internal/store/judgment_test.go): the
 * atomic propose/confirm/reject/withdraw transitions with the frozen error
 * codes, the JUDGMENT_HASH_MISMATCH details carrier (ONLY the two hashes), the
 * idempotency replay/conflict contract, the observation relation projection on
 * confirm (none on reject), and the correction supersession routing
 * (judgmentSuccessorOf). Agents can never confirm/reject: the signatures
 * REQUIRE a VerifiedApprovalPrincipal (a branded factory product).
 */

import { describe, expect, it } from "vitest";

import type { MemoryScope, SaveMemoryInput } from "../../core/types.js";
import { ApprovalError } from "../../core/types.js";
import { createVerifiedApprovalPrincipal } from "../../auth/principal.js";
import { InMemoryMemoryStore } from "../memory-store.js";

const ORG = "org-001";
const COMPANY = "acme";
const PERIOD = "202401";

const scope = (): MemoryScope => ({
	kind: "company",
	organizationId: ORG,
	companyId: COMPANY,
	ruc: "20100039201",
	period: PERIOD,
});

const agentSource = {
	system: "vitest",
	actorId: "agent-1",
	actorKind: "agent" as const,
	session: "sess-1",
};

const otherAgentSource = {
	system: "vitest",
	actorId: "agent-2",
	actorKind: "agent" as const,
	session: "sess-2",
};

const humanSource = {
	system: "vitest",
	actorId: "maria.torres",
	actorKind: "human" as const,
};

function observation(topicKey: string): SaveMemoryInput {
	return {
		topicKey,
		title: "Observation",
		kind: "fact",
		scope: scope(),
		content: {
			what: "saldo de mayor",
			why: "fixture",
			where: "Peru",
			learned: "n/a",
		},
		fiscalEffect: "none",
		effectiveAt: "2024-01-15T00:00:00.000Z",
		source: agentSource,
	};
}

function principal(overrides: Record<string, unknown> = {}) {
	return createVerifiedApprovalPrincipal({
		subjectId: "maria.torres",
		tenantId: ORG,
		membershipId: "m-1",
		companyScopes: [COMPANY],
		roles: ["controller"],
		authenticationMethod: "session",
		assuranceLevel: "standard",
		authenticatedAt: "2024-02-01T10:00:00.000Z",
		...overrides,
	} as Parameters<typeof createVerifiedApprovalPrincipal>[0]);
}

async function saveObservations(store: InMemoryMemoryStore) {
	const a = await store.save(observation("judgment/from"));
	const b = await store.save(observation("judgment/to"));
	return [a.memory.identity.id, b.memory.identity.id] as const;
}

async function propose(
	store: InMemoryMemoryStore,
	fromId: string,
	toId: string,
	key: string,
	overrides: Record<string, unknown> = {},
) {
	return store.proposeJudgment(
		{
			fromId,
			toId,
			relation: "contradicts",
			reason: "diferencia de saldo entre mayor y SIRE",
			requestId: key,
			...(overrides as object),
		},
		agentSource,
	);
}

describe("judgment critical section (v0.4.0 Step 2)", () => {
	it("proposes a judgment over two observations with provenance preserved", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);

		const result = await propose(store, fromId, toId, "propose-01");

		expect(result.idempotentReplay).toBe(false);
		expect(result.judgmentId).toBe(result.judgment.id);
		const j = result.judgment;
		expect(j.status).toBe("proposed");
		expect(j.tenantId).toBe(ORG);
		expect(j.companyId).toBe(COMPANY);
		expect(j.fiscalPeriodId).toBe(PERIOD);
		expect(j.fromId).toBe(fromId);
		expect(j.toId).toBe(toId);
		expect(j.relation).toBe("contradicts");
		expect(j.proposer).toEqual(agentSource);
		expect(j.proposalReason).toBe("diferencia de saldo entre mayor y SIRE");
		expect(j.resolution).toBeUndefined();
		expect(j.decidedAt).toBeUndefined();

		// A proposal writes NO judgment event (frozen actions admit only
		// confirm|reject|withdraw|supersede).
		expect(store.judgmentEvents()).toHaveLength(0);
	});

	it("rejects a human proposer with PROPOSAL_UNAUTHORIZED", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);

		await expect(
			store.proposeJudgment(
				{
					fromId,
					toId,
					relation: "supports",
					reason: "r",
					requestId: "propose-human",
				},
				humanSource,
			),
		).rejects.toMatchObject({ code: "PROPOSAL_UNAUTHORIZED" });
	});

	it("rejects a non-proposable relation with RELATION_NOT_PROPOSABLE", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);

		await expect(
			store.proposeJudgment(
				{
					fromId,
					toId,
					relation: "conflicts_with",
					reason: "r",
					requestId: "propose-rel",
				},
				agentSource,
			),
		).rejects.toMatchObject({ code: "RELATION_NOT_PROPOSABLE" });
	});

	it("rejects a second open proposal for the tuple with JUDGMENT_CONFLICT", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);
		await propose(store, fromId, toId, "propose-first");

		await expect(
			propose(store, fromId, toId, "propose-second"),
		).rejects.toMatchObject({ code: "JUDGMENT_CONFLICT" });
	});

	it("confirms a proposed judgment against the reviewed hash, with event and relation projection", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);
		const proposed = await propose(store, fromId, toId, "propose-confirm");
		const { computeJudgmentHash } = await import("../../core/types.js");
		const hash = await computeJudgmentHash(proposed.judgment);

		const result = await store.confirmJudgment(
			{
				judgmentId: proposed.judgmentId,
				resolution: "El crédito se difiere; el mayor prevalece.",
				expectedJudgmentHash: hash,
				requestId: "confirm-01",
			},
			principal(),
		);

		expect(result.idempotentReplay).toBe(false);
		expect(result.judgmentEventId).not.toBe("");
		const j = result.judgment;
		expect(j.status).toBe("confirmed");
		expect(j.resolution).toBe("El crédito se difiere; el mayor prevalece.");
		expect(j.adjudicator?.subjectId).toBe("maria.torres");
		expect(j.policyVersion).toBe("judgment-policy/v0.4.0");
		expect(j.decidedAt).toBeDefined();

		// Exactly ONE immutable confirm event with the principal snapshot.
		const events = store.judgmentEvents();
		expect(events).toHaveLength(1);
		expect(events[0]!.action).toBe("confirm");
		expect(events[0]!.fromStatus).toBe("proposed");
		expect(events[0]!.toStatus).toBe("confirmed");
		expect(events[0]!.principalSnapshot?.subjectId).toBe("maria.torres");
		expect(events[0]!.policyVersion).toBe("judgment-policy/v0.4.0");

		// Confirmation inserts ONE compatibility observation relation projection
		// whose actor is the verified subject; the judgment remains authoritative.
		const projection = store.relations().find(
			(record) => record.fromId === fromId && record.toId === toId,
		);
		expect(projection).toBeDefined();
		expect(projection!.relation).toBe("contradicts");
		expect(projection!.actor).toBe("maria.torres");
	});

	it("fails with JUDGMENT_HASH_MISMATCH carrying ONLY the two hashes", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);
		const proposed = await propose(store, fromId, toId, "propose-stale");
		const { computeJudgmentHash } = await import("../../core/types.js");
		const currentHash = await computeJudgmentHash(proposed.judgment);
		const staleHash = "0".repeat(currentHash.length);

		const error = await store
			.confirmJudgment(
				{
					judgmentId: proposed.judgmentId,
					resolution: "x",
					expectedJudgmentHash: staleHash,
					requestId: "confirm-stale",
				},
				principal(),
			)
			.catch((e: unknown) => e);
		expect(error).toBeInstanceOf(ApprovalError);
		const typed = error as ApprovalError;
		expect(typed.code).toBe("JUDGMENT_HASH_MISMATCH");
		expect(typed.expectedJudgmentHash).toBe(staleHash);
		expect(typed.actualJudgmentHash).toBe(currentHash);
		// The "ONLY the two hashes" contract: no envelope pair is ever set.
		expect(typed.expectedEnvelopeHash).toBeUndefined();
		expect(typed.actualEnvelopeHash).toBeUndefined();
	});

	it("rejects a proposed judgment to terminal, storing the human reason, with NO relation projection", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);
		const proposed = await propose(store, fromId, toId, "propose-reject");
		const { computeJudgmentHash } = await import("../../core/types.js");
		const hash = await computeJudgmentHash(proposed.judgment);

		const result = await store.rejectJudgment(
			{
				judgmentId: proposed.judgmentId,
				reason: "El XML no corresponde al CDR.",
				expectedJudgmentHash: hash,
				requestId: "reject-01",
			},
			principal(),
		);

		expect(result.judgment.status).toBe("rejected");
		expect(result.judgment.resolution).toBe("El XML no corresponde al CDR.");
		expect(result.judgment.adjudicator?.subjectId).toBe("maria.torres");
		expect(store.judgmentEvents()).toHaveLength(1);
		expect(store.judgmentEvents()[0]!.action).toBe("reject");
		// Rejection writes NO observation relation projection.
		expect(
			store.relations().some((record) => record.fromId === fromId),
		).toBe(false);

		// Terminal: a later confirm is an invalid transition.
		await expect(
			store.confirmJudgment(
				{
					judgmentId: proposed.judgmentId,
					resolution: "x",
					expectedJudgmentHash: hash,
					requestId: "confirm-after-reject",
				},
				principal(),
			),
		).rejects.toMatchObject({ code: "INVALID_JUDGMENT_TRANSITION" });
	});

	it("withdraws only with the exact same proposer identity", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);
		const proposed = await propose(store, fromId, toId, "propose-withdraw");

		// A DIFFERENT agent identity is provenance discontinuity → unauthorized.
		await expect(
			store.withdrawJudgment(
				{ judgmentId: proposed.judgmentId, requestId: "withdraw-other" },
				otherAgentSource,
			),
		).rejects.toMatchObject({ code: "PROPOSAL_UNAUTHORIZED" });

		// The SAME proposer identity withdraws its own proposal (terminal).
		const result = await store.withdrawJudgment(
			{ judgmentId: proposed.judgmentId, requestId: "withdraw-same" },
			agentSource,
		);
		expect(result.judgment.status).toBe("withdrawn");
		expect(result.judgment.decidedAt).toBeDefined();
		expect(store.judgmentEvents()).toHaveLength(1);
		expect(store.judgmentEvents()[0]!.action).toBe("withdraw");
	});

	it("replays a completed idempotency reservation and conflicts on a changed command", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);
		const first = await propose(store, fromId, toId, "propose-replay");

		// Same request id + same command → replay of the ORIGINAL judgment.
		const replay = await propose(store, fromId, toId, "propose-replay");
		expect(replay.idempotentReplay).toBe(true);
		expect(replay.judgmentId).toBe(first.judgmentId);

		// Same request id + DIFFERENT command (relation changed) → conflict.
		await expect(
			store.proposeJudgment(
				{
					fromId,
					toId,
					relation: "supports",
					reason: "otra razon",
					requestId: "propose-replay",
				},
				agentSource,
			),
		).rejects.toMatchObject({ code: "IDEMPOTENCY_CONFLICT" });
	});

	it("supersedes a confirmed predecessor atomically with the confirming correction", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);
		const { computeJudgmentHash } = await import("../../core/types.js");

		// J1: proposed → confirmed.
		const j1 = await propose(store, fromId, toId, "propose-j1");
		const h1 = await computeJudgmentHash(j1.judgment);
		await store.confirmJudgment(
			{
				judgmentId: j1.judgmentId,
				resolution: "resolucion original",
				expectedJudgmentHash: h1,
				requestId: "confirm-j1",
			},
			principal(),
		);

		// J2 corrects J1 (same pair and relation, predecessorId=J1).
		const j2 = await store.proposeJudgment(
			{
				fromId,
				toId,
				relation: "contradicts",
				reason: "correccion: el importe correcto es 10,000",
				requestId: "propose-j2",
				predecessorId: j1.judgmentId,
			},
			agentSource,
		);
		const h2 = await computeJudgmentHash(j2.judgment);
		const confirmed = await store.confirmJudgment(
			{
				judgmentId: j2.judgmentId,
				resolution: "correccion confirmada",
				expectedJudgmentHash: h2,
				requestId: "confirm-j2",
			},
			principal(),
		);

		// J1 is now superseded and routes readers to J2.
		const predecessor = store
			.judgments()
			.find((judgment) => judgment.id === j1.judgmentId)!;
		expect(predecessor.status).toBe("superseded");
		expect(predecessor.supersedesId).toBe(j2.judgmentId);
		expect(store.judgmentSuccessorOf(j1.judgmentId)?.id).toBe(j2.judgmentId);
		expect(confirmed.judgment.status).toBe("confirmed");

		// Two decision events plus the supersede event of the predecessor.
		const actions = store.judgmentEvents().map((event) => event.action);
		expect(actions.filter((action) => action === "confirm")).toHaveLength(2);
		expect(actions).toContain("supersede");
	});

	it("supersedes a PROPOSED predecessor immediately when corrected by its own proposer", async () => {
		const store = new InMemoryMemoryStore();
		const [fromId, toId] = await saveObservations(store);

		const j1 = await propose(store, fromId, toId, "propose-p1");
		const j2 = await store.proposeJudgment(
			{
				fromId,
				toId,
				relation: "contradicts",
				reason: "correccion de la propuesta",
				requestId: "propose-p2",
				predecessorId: j1.judgmentId,
			},
			agentSource,
		);

		const predecessor = store
			.judgments()
			.find((judgment) => judgment.id === j1.judgmentId)!;
		expect(predecessor.status).toBe("superseded");
		expect(predecessor.supersedesId).toBe(j2.judgmentId);
		expect(store.judgmentSuccessorOf(j1.judgmentId)?.id).toBe(j2.judgmentId);

		// A DIFFERENT proposer may not correct an open proposal.
		await expect(
			store.proposeJudgment(
				{
					fromId,
					toId,
					relation: "contradicts",
					reason: "intento ajeno",
					requestId: "propose-p3",
					predecessorId: j2.judgmentId,
				},
				otherAgentSource,
			),
		).rejects.toMatchObject({ code: "PROPOSAL_UNAUTHORIZED" });
	});
});
