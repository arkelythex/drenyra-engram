/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Atomic receipt emission mirror tests (v0.4.0 Step 3) — the exact TS counterpart
 * of `internal/store/receipt_emission_test.go`: every covered mutation of the
 * in-memory store mints EXACTLY ONE immutable Ed25519 receipt inside its
 * critical section (memory_recorded, memory_approved, memory_rejected,
 * memory_voided, relation_confirmed, relation_rejected, evidence_linked,
 * memory_superseded); NO signer → no receipts; idempotent retries and duplicate
 * links emit nothing; subject chains link on the previous receipt's hash; the
 * deterministic parity seed (32×0x01) makes every asserted receipt verify with
 * the SAME public key Go derives.
 */

import { describe, expect, it } from "vitest";

import {
	NodeSeedSigner,
	receiptHash,
	receiptPayloadHash,
	verifyReceipt,
} from "../../core/receipt.js";
import {
	RECEIPT_PAYLOAD_VERSION,
	computeEnvelopeHash,
	computeJudgmentHash,
	type MemoryScope,
	type ReceiptPayload,
	type SaveMemoryInput,
	type SignedReceipt,
} from "../../core/types.js";
import { createVerifiedApprovalPrincipal } from "../../auth/principal.js";
import { InMemoryMemoryStore } from "../memory-store.js";

/** The deterministic parity seed (32×0x01), shared with the Go tests. */
const PARITY_SEED = Buffer.from(
	"0101010101010101010101010101010101010101010101010101010101010101",
	"hex",
);

const ORG = "org-001";
const COMPANY = "acme";
const RUC = "20100039201";
const PERIOD = "202401";

const scope = (): MemoryScope => ({
	kind: "company",
	organizationId: ORG,
	companyId: COMPANY,
	ruc: RUC,
	period: PERIOD,
});

const agentSource = {
	system: "vitest",
	actorId: "agent-1",
	actorKind: "agent" as const,
};

/** Non-gated observation: fiscalEffect none → active (recorded directly). */
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
		source: agentSource,
	};
}

/** Gated observation: fiscalEffect adjustment → pending_review (approvable). */
function gatedInput(topicKey: string, what: string): SaveMemoryInput {
	return {
		...input(topicKey, what),
		fiscalEffect: "adjustment",
		effectiveAt: "2024-01-15T00:00:00.000Z",
	};
}

/** The verified controller principal of the Go tests (subject-1/membership-1). */
function principal() {
	return createVerifiedApprovalPrincipal({
		subjectId: "subject-1",
		tenantId: ORG,
		membershipId: "membership-1",
		companyScopes: [COMPANY],
		roles: ["controller"],
		authenticationMethod: "session",
		assuranceLevel: "standard",
		authenticatedAt: "2026-08-05T12:00:00Z",
	} as Parameters<typeof createVerifiedApprovalPrincipal>[0]);
}

/**
 * Builds the exact payload the store must have emitted for a receipt (the
 * mirror of Go's payload(t) read-back): every key present, inapplicable fields
 * empty. verifyReceipt then proves payload/envelope equality, the canonical
 * payload hash and the Ed25519 signature with the parity public key.
 */
function payload(
	overrides: Partial<ReceiptPayload> &
		Pick<ReceiptPayload, "subjectType" | "subjectId" | "action">,
): ReceiptPayload {
	return {
		version: RECEIPT_PAYLOAD_VERSION,
		subjectType: overrides.subjectType,
		subjectId: overrides.subjectId,
		action: overrides.action,
		tenantId: overrides.tenantId ?? "",
		companyId: overrides.companyId ?? "",
		fiscalPeriodId: overrides.fiscalPeriodId ?? "",
		reviewedEnvelopeHash: overrides.reviewedEnvelopeHash ?? "",
		resultingEnvelopeHash: overrides.resultingEnvelopeHash ?? "",
		reviewedJudgmentHash: overrides.reviewedJudgmentHash ?? "",
		resultingJudgmentHash: overrides.resultingJudgmentHash ?? "",
		fromMemoryId: overrides.fromMemoryId ?? "",
		fromEnvelopeHash: overrides.fromEnvelopeHash ?? "",
		toMemoryId: overrides.toMemoryId ?? "",
		toEnvelopeHash: overrides.toEnvelopeHash ?? "",
		successorId: overrides.successorId ?? "",
		evidenceRef: overrides.evidenceRef ?? "",
		reason: overrides.reason ?? "",
		principalId: overrides.principalId ?? "",
		membershipId: overrides.membershipId ?? "",
		principalRoles: overrides.principalRoles ?? [],
		authenticationMethod: overrides.authenticationMethod ?? "",
		assuranceLevel: overrides.assuranceLevel ?? "",
		principalAuthenticatedAt: overrides.principalAuthenticatedAt ?? "",
		policyVersion: overrides.policyVersion ?? "",
		issuedAt: overrides.issuedAt ?? "",
	};
}

/** Asserts the receipt verifies offline with the parity public key and that its
 * payload hash equals the canonical payload digest (Go's verifyStored). */
function expectVerified(
	receipt: SignedReceipt,
	expectedPayload: ReceiptPayload,
	signer: NodeSeedSigner,
): void {
	expect(receipt.payloadHash).toBe(receiptPayloadHash(expectedPayload));
	expect(() => verifyReceipt(receipt, expectedPayload, signer.publicKey)).not.toThrow();
	expect(receipt.keyId).toBe(signer.keyId);
	expect(receipt.algorithm).toBe("Ed25519");
}

describe("atomic receipt emission (v0.4.0 Step 3)", () => {
	it("emits NO receipts without a signer", async () => {
		const store = new InMemoryMemoryStore();
		await store.save(input("receipt.nil-signer", "no signer attached"));
		expect(store.receipts()).toHaveLength(0);
	});

	it("save emits exactly one memory_recorded with the exact payload fields", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const saved = await store.save(input("receipt.recorded", "first version"));
		const id = saved.memory.identity.id;

		const receipts = store.receipts();
		expect(receipts).toHaveLength(1);
		const r = receipts[0]!;
		expect(r.subjectType).toBe("memory");
		expect(r.subjectId).toBe(id);
		expect(r.action).toBe("memory_recorded");
		expect(r.previousReceiptHash).toBe(""); // genesis
		expect(r.policyVersion).toBe("kernel/v0.4.0");
		expect(r.issuedAt).toBe(saved.memory.recordedAt); // shared timestamp

		expectVerified(
			r,
			payload({
				subjectType: "memory",
				subjectId: id,
				action: "memory_recorded",
				tenantId: ORG,
				companyId: COMPANY,
				fiscalPeriodId: PERIOD,
				resultingEnvelopeHash: saved.memory.envelopeHash!,
				principalId: agentSource.actorId,
				policyVersion: "kernel/v0.4.0",
				issuedAt: saved.memory.recordedAt,
			}),
			signer,
		);
	});

	it("auto-supersession emits memory_superseded for the PRIOR subject, then memory_recorded", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const first = await store.save(input("receipt.chain", "v1"));
		const second = await store.save(input("receipt.chain", "v2"));
		const firstID = first.memory.identity.id;
		const secondID = second.memory.identity.id;

		const receipts = store.receipts();
		expect(receipts).toHaveLength(3);

		const rec1 = receipts[0]!;
		expect(rec1.action).toBe("memory_recorded");
		expect(rec1.subjectId).toBe(firstID);
		expect(rec1.previousReceiptHash).toBe("");

		const sup = receipts[1]!;
		expect(sup.action).toBe("memory_superseded");
		expect(sup.subjectId).toBe(firstID);
		// Chain: the supersession receipt chains on the FIRST recorded receipt.
		expect(sup.previousReceiptHash).toBe(receiptHash(rec1));
		const expectedFrom = await computeEnvelopeHash(first.memory);
		const expectedTo = await computeEnvelopeHash({
			...first.memory,
			status: "superseded",
			supersedesId: secondID,
		});
		expectVerified(
			sup,
			payload({
				subjectType: "memory",
				subjectId: firstID,
				action: "memory_superseded",
				tenantId: ORG,
				companyId: COMPANY,
				fiscalPeriodId: PERIOD,
				fromEnvelopeHash: expectedFrom,
				toEnvelopeHash: expectedTo,
				successorId: secondID,
				principalId: agentSource.actorId,
				policyVersion: "kernel/v0.4.0",
				issuedAt: second.memory.recordedAt, // the capturing save's timestamp
			}),
			signer,
		);

		const rec2 = receipts[2]!;
		expect(rec2.action).toBe("memory_recorded");
		expect(rec2.subjectId).toBe(secondID);
		expect(rec2.previousReceiptHash).toBe(""); // new subject chain
		expect(rec2.issuedAt).toBe(second.memory.recordedAt);
	});

	it("approveMemory emits memory_approved with H1/H2, the reason and the verified snapshot", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const saved = await store.save(gatedInput("receipt.approve", "needs review"));
		const id = saved.memory.identity.id;
		const expected = saved.memory.envelopeHash!;

		const result = await store.approveMemory(
			{
				memoryId: id,
				expectedEnvelopeHash: expected,
				reason: "approved by fixture reviewer",
				requestId: "req-approve-receipt",
			},
			principal(),
		);

		const receipts = store.receipts();
		expect(receipts).toHaveLength(2);
		const r = receipts.find((candidate) => candidate.action === "memory_approved")!;
		expect(r.subjectType).toBe("memory");
		expect(r.subjectId).toBe(id);
		expect(r.issuedAt).toBe(result.approvedAt); // the decision's captured now

		expectVerified(
			r,
			payload({
				subjectType: "memory",
				subjectId: id,
				action: "memory_approved",
				tenantId: ORG,
				companyId: COMPANY,
				fiscalPeriodId: PERIOD,
				reviewedEnvelopeHash: expected,
				resultingEnvelopeHash: result.resultingEnvelopeHash,
				reason: "approved by fixture reviewer",
				principalId: "subject-1",
				membershipId: "membership-1",
				principalRoles: ["controller"],
				authenticationMethod: "session",
				assuranceLevel: "standard",
				principalAuthenticatedAt: "2026-08-05T12:00:00Z",
				policyVersion: "approval-policy/v0.4.0",
				issuedAt: result.approvedAt,
			}),
			signer,
		);
	});

	it("an idempotent approval retry emits NO duplicate receipt", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const saved = await store.save(gatedInput("receipt.approve-idem", "idempotent approval"));
		const id = saved.memory.identity.id;
		const expected = saved.memory.envelopeHash!;
		const command = {
			memoryId: id,
			expectedEnvelopeHash: expected,
			reason: "replay approval",
			requestId: "req-approve-idem",
		};

		await store.approveMemory(command, principal());
		const replay = await store.approveMemory(command, principal());
		expect(replay.idempotentReplay).toBe(true);

		// recorded + approved — the retry must NOT mint a duplicate.
		expect(store.receipts()).toHaveLength(2);
		expect(
			store.receipts().filter((r) => r.action === "memory_approved"),
		).toHaveLength(1);
	});

	it("applyStatusTransition to rejected emits memory_rejected with pre/post envelopes", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const saved = await store.save(gatedInput("receipt.reject", "to reject"));
		const id = saved.memory.identity.id;
		const meta = { actor: "reviewer-9", actorKind: "human" as const, timestamp: "2026-08-05T14:00:00Z" };

		store.applyStatusTransition(id, "rejected", meta);

		const receipts = store.receipts();
		expect(receipts).toHaveLength(2);
		const r = receipts.find((candidate) => candidate.action === "memory_rejected")!;
		const expectedFrom = await computeEnvelopeHash(saved.memory);
		const expectedTo = await computeEnvelopeHash({ ...saved.memory, status: "rejected" });
		expectVerified(
			r,
			payload({
				subjectType: "memory",
				subjectId: id,
				action: "memory_rejected",
				tenantId: ORG,
				companyId: COMPANY,
				fiscalPeriodId: PERIOD,
				reviewedEnvelopeHash: expectedFrom,
				resultingEnvelopeHash: expectedTo,
				principalId: meta.actor,
				policyVersion: "kernel/v0.4.0",
				issuedAt: meta.timestamp,
			}),
			signer,
		);
		// The envelope cache stays fresh after the transition (status hashes in).
		expect(store.findById(id)?.envelopeHash).toBe(expectedTo);
	});

	it("applyStatusTransition to voided emits memory_voided with pre/post envelopes", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const saved = await store.save(input("receipt.void", "to void"));
		const id = saved.memory.identity.id;
		const meta = { actor: "auditor-4", actorKind: "human" as const, timestamp: "2026-08-05T14:30:00Z" };

		store.applyStatusTransition(id, "voided", meta);

		const receipts = store.receipts();
		expect(receipts).toHaveLength(2);
		const r = receipts.find((candidate) => candidate.action === "memory_voided")!;
		const expectedFrom = await computeEnvelopeHash(saved.memory);
		const expectedTo = await computeEnvelopeHash({ ...saved.memory, status: "voided" });
		expectVerified(
			r,
			payload({
				subjectType: "memory",
				subjectId: id,
				action: "memory_voided",
				tenantId: ORG,
				companyId: COMPANY,
				fiscalPeriodId: PERIOD,
				reviewedEnvelopeHash: expectedFrom,
				resultingEnvelopeHash: expectedTo,
				principalId: meta.actor,
				policyVersion: "kernel/v0.4.0",
				issuedAt: meta.timestamp,
			}),
			signer,
		);
	});

	it("confirmJudgment emits relation_confirmed with both hashes, envelopes and the snapshot", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const fromId = (await store.save(input("receipt.from", "from observation"))).memory.identity.id;
		const toId = (await store.save(input("receipt.to", "to observation"))).memory.identity.id;
		const proposed = await store.proposeJudgment(
			{
				fromId,
				toId,
				relation: "supports",
				reason: "proposal",
				requestId: "req-propose-confirm",
			},
			agentSource,
		);
		const proposedHash = await computeJudgmentHash(proposed.judgment);
		const confirmed = await store.confirmJudgment(
			{
				judgmentId: proposed.judgmentId,
				resolution: "resolution text",
				expectedJudgmentHash: proposedHash,
				requestId: "req-confirm-receipt",
			},
			principal(),
		);

		const receipts = store.receipts();
		expect(receipts).toHaveLength(3); // recorded from, recorded to, confirmed
		const r = receipts.find((candidate) => candidate.action === "relation_confirmed")!;
		expect(r.subjectType).toBe("judgment");
		expect(r.subjectId).toBe(proposed.judgmentId);
		expect(r.issuedAt).toBe(confirmed.judgment.decidedAt);

		const fromMem = store.findById(fromId)!;
		const toMem = store.findById(toId)!;
		expectVerified(
			r,
			payload({
				subjectType: "judgment",
				subjectId: proposed.judgmentId,
				action: "relation_confirmed",
				tenantId: ORG,
				companyId: COMPANY,
				fiscalPeriodId: PERIOD,
				reviewedJudgmentHash: proposedHash,
				resultingJudgmentHash: await computeJudgmentHash(confirmed.judgment),
				fromMemoryId: fromId,
				fromEnvelopeHash: await computeEnvelopeHash(fromMem),
				toMemoryId: toId,
				toEnvelopeHash: await computeEnvelopeHash(toMem),
				reason: "resolution text",
				principalId: "subject-1",
				membershipId: "membership-1",
				principalRoles: ["controller"],
				authenticationMethod: "session",
				assuranceLevel: "standard",
				principalAuthenticatedAt: "2026-08-05T12:00:00Z",
				policyVersion: "judgment-policy/v0.4.0",
				issuedAt: confirmed.judgment.decidedAt!,
			}),
			signer,
		);
	});

	it("rejectJudgment emits relation_rejected with the human reason", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const fromId = (await store.save(input("receipt.from", "from observation"))).memory.identity.id;
		const toId = (await store.save(input("receipt.to", "to observation"))).memory.identity.id;
		const proposed = await store.proposeJudgment(
			{
				fromId,
				toId,
				relation: "supports",
				reason: "proposal",
				requestId: "req-propose-reject",
			},
			agentSource,
		);
		const proposedHash = await computeJudgmentHash(proposed.judgment);
		const rejected = await store.rejectJudgment(
			{
				judgmentId: proposed.judgmentId,
				reason: "not supported by evidence",
				expectedJudgmentHash: proposedHash,
				requestId: "req-reject-receipt",
			},
			principal(),
		);

		const receipts = store.receipts();
		expect(receipts).toHaveLength(3);
		const r = receipts.find((candidate) => candidate.action === "relation_rejected")!;
		expect(r.subjectType).toBe("judgment");
		expect(r.subjectId).toBe(proposed.judgmentId);

		const fromMem = store.findById(fromId)!;
		const toMem = store.findById(toId)!;
		expectVerified(
			r,
			payload({
				subjectType: "judgment",
				subjectId: proposed.judgmentId,
				action: "relation_rejected",
				tenantId: ORG,
				companyId: COMPANY,
				fiscalPeriodId: PERIOD,
				reviewedJudgmentHash: proposedHash,
				resultingJudgmentHash: await computeJudgmentHash(rejected.judgment),
				fromMemoryId: fromId,
				fromEnvelopeHash: await computeEnvelopeHash(fromMem),
				toMemoryId: toId,
				toEnvelopeHash: await computeEnvelopeHash(toMem),
				reason: "not supported by evidence",
				principalId: "subject-1",
				membershipId: "membership-1",
				principalRoles: ["controller"],
				authenticationMethod: "session",
				assuranceLevel: "standard",
				principalAuthenticatedAt: "2026-08-05T12:00:00Z",
				policyVersion: "judgment-policy/v0.4.0",
				issuedAt: rejected.judgment.decidedAt!,
			}),
			signer,
		);
	});

	it("a judgment correction covers the predecessor supersession inside relation_confirmed", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const fromId = (await store.save(input("receipt.from", "from observation"))).memory.identity.id;
		const toId = (await store.save(input("receipt.to", "to observation"))).memory.identity.id;

		const first = await store.proposeJudgment(
			{
				fromId,
				toId,
				relation: "supports",
				reason: "original",
				requestId: "req-corr-first",
			},
			agentSource,
		);
		await store.confirmJudgment(
			{
				judgmentId: first.judgmentId,
				resolution: "first resolution",
				expectedJudgmentHash: await computeJudgmentHash(first.judgment),
				requestId: "req-corr-confirm-1",
			},
			principal(),
		);

		const second = await store.proposeJudgment(
			{
				fromId,
				toId,
				relation: "supports",
				reason: "correction",
				requestId: "req-corr-second",
				predecessorId: first.judgmentId,
			},
			agentSource,
		);
		await store.confirmJudgment(
			{
				judgmentId: second.judgmentId,
				resolution: "corrected resolution",
				expectedJudgmentHash: await computeJudgmentHash(second.judgment),
				requestId: "req-corr-confirm-2",
			},
			principal(),
		);

		// 2 recorded + 2 relation_confirmed — the predecessor supersession is
		// covered inside the correction's receipt, never a separate action.
		const receipts = store.receipts();
		expect(receipts).toHaveLength(4);
		for (const r of receipts) {
			if (r.action === "memory_recorded") {
				expect(r.subjectType).toBe("memory");
			} else if (r.action === "relation_confirmed") {
				expect(r.subjectType).toBe("judgment");
			} else {
				throw new Error(
					`unexpected action ${r.action} in a correction flow — the predecessor supersession must not mint a separate receipt`,
				);
			}
		}
		expect(
			receipts.filter((r) => r.action === "relation_confirmed"),
		).toHaveLength(2);
	});

	it("a genuinely new evidence link emits evidence_linked chained on the recorded receipt", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const saved = await store.save(input("receipt.link", "linked memory"));
		const id = saved.memory.identity.id;
		const ref = "evidence://factura-2024-0001.pdf";

		store.addEvidenceLink(id, ref, "cli");

		const receipts = store.receipts();
		expect(receipts).toHaveLength(2);
		const r = receipts.find((candidate) => candidate.action === "evidence_linked")!;
		const recorded = receipts.find((candidate) => candidate.action === "memory_recorded")!;
		expect(recorded.subjectId).toBe(id);
		expect(r.previousReceiptHash).toBe(receiptHash(recorded));

		const expectedFrom = await computeEnvelopeHash(saved.memory);
		const expectedTo = await computeEnvelopeHash(store.findById(id)!);
		expectVerified(
			r,
			payload({
				subjectType: "memory",
				subjectId: id,
				action: "evidence_linked",
				tenantId: ORG,
				companyId: COMPANY,
				fiscalPeriodId: PERIOD,
				fromEnvelopeHash: expectedFrom,
				toEnvelopeHash: expectedTo,
				evidenceRef: ref,
				principalId: "cli",
				policyVersion: "kernel/v0.4.0",
				issuedAt: r.issuedAt,
			}),
			signer,
		);
	});

	it("a duplicate evidence link is a no-op: no mutation, no receipt", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const saved = await store.save(input("receipt.link", "linked memory"));
		const id = saved.memory.identity.id;
		const ref = "evidence://factura-2024-0001.pdf";

		store.addEvidenceLink(id, ref, "cli");
		const before = store.findById(id)?.envelopeHash;
		store.addEvidenceLink(id, ref, "cli"); // duplicate

		expect(store.receipts()).toHaveLength(2); // still recorded + linked
		expect(store.findById(id)?.envelopeHash).toBe(before); // no state change
	});

	it("rule links emit NO receipt (the closed action set has no rule action)", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const saved = await store.save(input("receipt.rule-link", "rule-linked memory"));

		store.addRuleLink(saved.memory.identity.id, "rule://tax-2024-008", "cli");

		expect(store.receipts()).toHaveLength(1); // recorded only
	});

	it("receipts() returns immutable copies", async () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		const saved = await store.save(input("receipt.immutable", "immutable copies"));
		const id = saved.memory.identity.id;
    
		// Mutating a returned array or receipt must never reach the store.
		const leaked = store.receipts();
		leaked.push({ ...leaked[0]! });
		leaked[0]!.subjectId = "tampered";
    
		expect(store.receipts()).toHaveLength(1);
		expect(store.receipts()[0]!.subjectId).toBe(id);
	});
});
