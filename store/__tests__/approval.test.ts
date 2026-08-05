/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Authenticated atomic approval — v0.4.0 Step 1 (ADR-003). The principal is a
 * separate verified argument (auth/principal.ts factory), NEVER part of the
 * command; the transport payload can never declare authority. These tests cover
 * the mirror of SQLiteStore.ApproveMemory: idempotency (replay vs conflict),
 * fresh H1 vs expected (ENVELOPE_MISMATCH), the frozen scope/status gates, the
 * pure policy (role/assurance/materiality), H1 != H2, and the immutable
 * approval event keeping the principal snapshot used for the decision.
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
	actorId: "test-agent",
	actorKind: "agent" as const,
};

function input(overrides: Partial<SaveMemoryInput> = {}): SaveMemoryInput {
	return {
		topicKey: "tax/igv/adjustment-001",
		title: "IGV adjustment",
		kind: "exception",
		scope: scope(),
		content: {
			what: "XML and invoice differ",
			why: "vendor corrected the base",
			where: "Peru",
			learned: "document the correction",
		},
		fiscalEffect: "adjustment",
		effectiveAt: "2024-01-15T00:00:00.000Z",
		source: agentSource,
		...overrides,
	};
}

function principal(overrides: Record<string, unknown> = {}) {
	return createVerifiedApprovalPrincipal({
		subjectId: "user-1",
		tenantId: ORG,
		membershipId: "m-1",
		companyScopes: [COMPANY],
		roles: ["controller"],
		authenticationMethod: "session",
		assuranceLevel: "strong",
		authenticatedAt: "2024-02-01T10:00:00.000Z",
		...overrides,
	} as Parameters<typeof createVerifiedApprovalPrincipal>[0]);
}

async function savedMemoryId(
	store: InMemoryMemoryStore,
	overrides: Partial<SaveMemoryInput> = {},
) {
	const result = await store.save(input(overrides));
	return result.memory.identity.id;
}

describe("authenticated approval (v0.4.0 Step 1)", () => {
	it("approves a pending_review memory against the exact reviewed envelope", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store);
		const before = store.findById(id)!;
		expect(before.status).toBe("pending_review");

		const result = await store.approveMemory(
			{
				memoryId: id,
				expectedEnvelopeHash: before.envelopeHash!,
				reason: "Ajuste revisado contra XML, CDR y mayor contable.",
				requestId: "approval-01J",
			},
			principal(),
		);

		expect(result.idempotentReplay).toBe(false);
		expect(result.previousStatus).toBe("pending_review");
		expect(result.currentStatus).toBe("approved");
		expect(result.reviewedEnvelopeHash).toBe(before.envelopeHash!);
		expect(result.resultingEnvelopeHash).not.toBe(result.reviewedEnvelopeHash);
		expect(result.principalSubjectId).toBe("user-1");
		expect(result.membershipId).toBe("m-1");
		expect(result.policyVersion).toBe("approval-policy/v0.4.0");

		const after = store.findById(id)!;
		expect(after.status).toBe("approved");
		expect(after.envelopeHash).toBe(result.resultingEnvelopeHash);

		// Immutable event keeps the exact principal snapshot used for the decision.
		const events = store.approvalEvents();
		expect(events).toHaveLength(1);
		const event = events[0]!;
		expect(event.action).toBe("approved");
		expect(event.fromStatus).toBe("pending_review");
		expect(event.toStatus).toBe("approved");
		expect(event.reviewedEnvelopeHash).toBe(before.envelopeHash!);
		expect(event.resultingEnvelopeHash).toBe(result.resultingEnvelopeHash);
		expect(event.policyVersion).toBe("approval-policy/v0.4.0");
		expect(event.authorizationReasonCode).toBe("AUTHORIZED");
		expect(event.fiscalPeriodId).toBe(PERIOD);
		expect(event.principalSnapshot).toEqual({
			subjectId: "user-1",
			membershipId: "m-1",
			roles: ["controller"],
			authenticationMethod: "session",
			assuranceLevel: "strong",
			authenticatedAt: "2024-02-01T10:00:00.000Z",
		});
	});

	it("replays the SAME result for the same requestId + command without a second event", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store);
		const envelope = store.findById(id)!.envelopeHash!;
		const command = {
			memoryId: id,
			expectedEnvelopeHash: envelope,
			reason: "Revisado contra CDR.",
			requestId: "approval-replay",
		};

		const first = await store.approveMemory(command, principal());
		const replay = await store.approveMemory(command, principal());

		expect(replay.idempotentReplay).toBe(true);
		expect(replay.approvalEventId).toBe(first.approvalEventId);
		expect(replay.reviewedEnvelopeHash).toBe(first.reviewedEnvelopeHash);
		expect(replay.resultingEnvelopeHash).toBe(first.resultingEnvelopeHash);
		expect(store.approvalEvents()).toHaveLength(1);
	});

	it("rejects the same requestId with a different payload as IDEMPOTENCY_CONFLICT", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store);
		const envelope = store.findById(id)!.envelopeHash!;

		await store.approveMemory(
			{
				memoryId: id,
				expectedEnvelopeHash: envelope,
				reason: "Primera razón.",
				requestId: "approval-conflict",
			},
			principal(),
		);

		await expect(
			store.approveMemory(
				{
					memoryId: id,
					expectedEnvelopeHash: envelope,
					reason: "OTRA razón distinta.",
					requestId: "approval-conflict",
				},
				principal(),
			),
		).rejects.toMatchObject({ code: "IDEMPOTENCY_CONFLICT" });
	});

	it("fails with ENVELOPE_MISMATCH carrying only the two hashes when the memory changed after review", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store);
		const envelope = store.findById(id)!.envelopeHash!;

		const promise = store.approveMemory(
			{
				memoryId: id,
				expectedEnvelopeHash: "H1-modified",
				reason: "Revisé otra versión.",
				requestId: "approval-stale",
			},
			principal(),
		);

		await expect(promise).rejects.toMatchObject({
			code: "ENVELOPE_MISMATCH",
			expectedEnvelopeHash: "H1-modified",
			actualEnvelopeHash: envelope,
		});
		expect(store.findById(id)!.status).toBe("pending_review");
		expect(store.approvalEvents()).toHaveLength(0);
	});

	it("returns ALREADY_DECIDED for a second approval with a different requestId", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store);
		const envelope = store.findById(id)!.envelopeHash!;

		await store.approveMemory(
			{
				memoryId: id,
				expectedEnvelopeHash: envelope,
				reason: "Primera aprobación.",
				requestId: "approval-a",
			},
			principal(),
		);

		await expect(
			store.approveMemory(
				{
					memoryId: id,
					expectedEnvelopeHash: envelope,
					reason: "Segunda aprobación.",
					requestId: "approval-b",
				},
				principal(),
			),
		).rejects.toMatchObject({ code: "ALREADY_DECIDED" });
		expect(store.approvalEvents()).toHaveLength(1);
	});

	it("fails closed with INVALID_TRANSITION when the memory is not pending_review", async () => {
		const store = new InMemoryMemoryStore();
		// fiscalEffect none → active, never behind the gate.
		const id = await savedMemoryId(store, { fiscalEffect: "none" as const });
		const envelope = store.findById(id)!.envelopeHash!;

		await expect(
			store.approveMemory(
				{
					memoryId: id,
					expectedEnvelopeHash: envelope,
					reason: "No debería poder aprobar esto.",
					requestId: "approval-active",
				},
				principal(),
			),
		).rejects.toMatchObject({ code: "INVALID_TRANSITION" });
	});

	it("denies a principal from another tenant with TENANT_SCOPE_MISMATCH", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store);
		const envelope = store.findById(id)!.envelopeHash!;

		await expect(
			store.approveMemory(
				{
					memoryId: id,
					expectedEnvelopeHash: envelope,
					reason: "Tenant equivocado.",
					requestId: "approval-tenant",
				},
				principal({ tenantId: "org-OTHER" }),
			),
		).rejects.toMatchObject({ code: "TENANT_SCOPE_MISMATCH" });
	});

	it("denies a principal without the company in scope with COMPANY_SCOPE_DENIED", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store);
		const envelope = store.findById(id)!.envelopeHash!;

		await expect(
			store.approveMemory(
				{
					memoryId: id,
					expectedEnvelopeHash: envelope,
					reason: "Empresa fuera de scope.",
					requestId: "approval-company",
				},
				principal({ companyScopes: ["other-company"] }),
			),
		).rejects.toMatchObject({ code: "COMPANY_SCOPE_DENIED" });
	});

	it("denies an insufficient role with ROLE_NOT_AUTHORIZED", async () => {
		const store = new InMemoryMemoryStore();
		// closing requires controller; an accountant cannot approve it.
		const id = await savedMemoryId(store, { fiscalEffect: "closing" as const });
		const envelope = store.findById(id)!.envelopeHash!;

		await expect(
			store.approveMemory(
				{
					memoryId: id,
					expectedEnvelopeHash: envelope,
					reason: "Cierre contable.",
					requestId: "approval-role",
				},
				principal({ roles: ["accountant"] }),
			),
		).rejects.toMatchObject({ code: "ROLE_NOT_AUTHORIZED" });
	});

	it("raises the required role for a material adjustment (MATERIALITY_LIMIT_EXCEEDED)", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store, {
			materialityLevel: "material" as const,
		});
		const envelope = store.findById(id)!.envelopeHash!;

		// accountant is the base role for adjustment, but material raises to
		// senior_accountant.
		await expect(
			store.approveMemory(
				{
					memoryId: id,
					expectedEnvelopeHash: envelope,
					reason: "Ajuste material.",
					requestId: "approval-materiality",
				},
				principal({ roles: ["accountant"] }),
			),
		).rejects.toMatchObject({ code: "MATERIALITY_LIMIT_EXCEEDED" });

		// A senior accountant clears the raised bar.
		const ok = await store.approveMemory(
			{
				memoryId: id,
				expectedEnvelopeHash: envelope,
				reason: "Ajuste material revisado por senior.",
				requestId: "approval-materiality-ok",
			},
			principal({ roles: ["senior_accountant"] }),
		);
		expect(ok.currentStatus).toBe("approved");
	});

	it("requires a non-whitespace reason with REASON_REQUIRED", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store);
		const envelope = store.findById(id)!.envelopeHash!;

		await expect(
			store.approveMemory(
				{
					memoryId: id,
					expectedEnvelopeHash: envelope,
					reason: "   ",
					requestId: "approval-reason",
				},
				principal(),
			),
		).rejects.toMatchObject({ code: "REASON_REQUIRED" });
	});

	it("fails with MEMORY_NOT_FOUND for an unknown memory", async () => {
		const store = new InMemoryMemoryStore();

		await expect(
			store.approveMemory(
				{
					memoryId: "mem-missing",
					expectedEnvelopeHash: "deadbeef",
					reason: "No existe.",
					requestId: "approval-missing",
				},
				principal(),
			),
		).rejects.toMatchObject({ code: "MEMORY_NOT_FOUND" });
	});

	it("keeps the historical event snapshot even when roles change later", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store);
		const envelope = store.findById(id)!.envelopeHash!;

		await store.approveMemory(
			{
				memoryId: id,
				expectedEnvelopeHash: envelope,
				reason: "Aprobado como accountant.",
				requestId: "approval-audit",
			},
			principal({ roles: ["accountant", "controller"] }),
		);

		const event = store.approvalEvents()[0]!;
		// Canonical set: sorted and deduplicated, independent of future roles.
		expect(event.principalSnapshot.roles).toEqual(["accountant", "controller"]);
		// The event never carries session ids or token material.
		expect(event.principalSnapshot).not.toHaveProperty("sessionId");
		expect(JSON.stringify(event)).not.toContain("secret");
	});

	it("is an ApprovalError with a frozen code (transport-independent)", async () => {
		const store = new InMemoryMemoryStore();
		const id = await savedMemoryId(store);
		const envelope = store.findById(id)!.envelopeHash!;

		try {
			await store.approveMemory(
				{
					memoryId: id,
					expectedEnvelopeHash: "wrong",
					reason: "Con hash incorrecto.",
					requestId: "approval-error",
				},
				principal(),
			);
			expect.unreachable("should have thrown");
		} catch (error) {
			expect(error).toBeInstanceOf(ApprovalError);
			expect((error as ApprovalError).code).toBe("ENVELOPE_MISMATCH");
			expect((error as ApprovalError).actualEnvelopeHash).toBe(envelope);
		}
	});
});
