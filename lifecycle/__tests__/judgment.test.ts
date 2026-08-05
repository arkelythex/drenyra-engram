/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Judgment lifecycle mirror tests (v0.4.0 Step 2) — the exact TS mirror of the
 * pure judgment machine in internal/core/judgment.go and the orchestration in
 * internal/server/judgment_service.go: the proposable-source predicate, the
 * status predicates (proposed-only decisions, confirmed-only supersession), the
 * adjacency table, and the syntax/provenance guards of the orchestration
 * functions that delegate the WHOLE state change to the store. Agent-confirm is
 * IMPOSSIBLE by construction: confirm takes a VerifiedApprovalPrincipal (a
 * branded factory product), and the agent-shaped path fails with
 * AUTHENTICATION_REQUIRED / PRINCIPAL_INVALID at the guard — provenance is
 * never authority.
 */

import { describe, expect, it } from "vitest";

import type {
	RejectJudgmentCommand,
	WithdrawJudgmentCommand,
} from "../../core/types.js";
import {
	ApprovalError,
	type MemorySource,
	type ProposeJudgmentCommand,
} from "../../core/types.js";
import { createVerifiedApprovalPrincipal } from "../../auth/principal.js";
import { InMemoryMemoryStore } from "../../store/memory-store.js";
import {
	canConfirmJudgment,
	canProposeJudgment,
	canRejectJudgment,
	canSupersedeConfirmed,
	canWithdrawJudgment,
	confirmJudgment,
	isLegalJudgmentTransition,
	proposeJudgment,
	rejectJudgment,
	withdrawJudgment,
} from "../transitions.js";

const ORG = "org-001";
const COMPANY = "acme";

const agentSource: MemorySource = {
	system: "vitest",
	actorId: "agent-1",
	actorKind: "agent",
	session: "sess-1",
};

const humanSource: MemorySource = {
	system: "vitest",
	actorId: "maria.torres",
	actorKind: "human",
};

function principal() {
	return createVerifiedApprovalPrincipal({
		subjectId: "maria.torres",
		tenantId: ORG,
		membershipId: "m-1",
		companyScopes: [COMPANY],
		roles: ["controller"],
		authenticationMethod: "session",
		assuranceLevel: "standard",
		authenticatedAt: "2024-02-01T10:00:00.000Z",
	});
}

function proposeCommand(
	overrides: Partial<ProposeJudgmentCommand> = {},
): ProposeJudgmentCommand {
	return {
		fromId: "obs-a",
		toId: "obs-b",
		relation: "contradicts",
		reason: "diferencia de saldo",
		requestId: "req-1",
		...overrides,
	};
}

describe("judgment lifecycle predicates (v0.4.0 Step 2)", () => {
	it("allows only agent|system sources to propose", () => {
		expect(canProposeJudgment({ system: "sire", actorKind: "agent" })).toBe(true);
		expect(canProposeJudgment({ system: "sire", actorKind: "system" })).toBe(true);
		expect(canProposeJudgment(humanSource)).toBe(false);
	});

	it("allows confirm/reject/withdraw only from proposed", () => {
		for (const status of ["proposed", "confirmed", "rejected", "withdrawn", "superseded"] as const) {
			expect(canConfirmJudgment(status)).toBe(status === "proposed");
			expect(canRejectJudgment(status)).toBe(status === "proposed");
			expect(canWithdrawJudgment(status)).toBe(status === "proposed");
		}
	});

	it("allows supersession only from confirmed", () => {
		expect(canSupersedeConfirmed("confirmed")).toBe(true);
		for (const status of ["proposed", "rejected", "withdrawn", "superseded"] as const) {
			expect(canSupersedeConfirmed(status)).toBe(false);
		}
	});

	it("freezes the adjacency table", () => {
		// proposed → confirmed|rejected|withdrawn|superseded
		expect(isLegalJudgmentTransition("proposed", "confirmed")).toBe(true);
		expect(isLegalJudgmentTransition("proposed", "rejected")).toBe(true);
		expect(isLegalJudgmentTransition("proposed", "withdrawn")).toBe(true);
		expect(isLegalJudgmentTransition("proposed", "superseded")).toBe(true);
		// confirmed → superseded ONLY
		expect(isLegalJudgmentTransition("confirmed", "superseded")).toBe(true);
		expect(isLegalJudgmentTransition("confirmed", "rejected")).toBe(false);
		expect(isLegalJudgmentTransition("confirmed", "proposed")).toBe(false);
		// terminal states never re-open
		for (const from of ["rejected", "withdrawn", "superseded"] as const) {
			for (const to of ["proposed", "confirmed", "rejected", "withdrawn", "superseded"] as const) {
				expect(isLegalJudgmentTransition(from, to)).toBe(false);
			}
		}
	});
});

describe("judgment orchestration guards (v0.4.0 Step 2)", () => {
	it("rejects a human proposer with PROPOSAL_UNAUTHORIZED before touching the store", async () => {
		const store = new InMemoryMemoryStore();
		await expect(
			proposeJudgment(proposeCommand(), humanSource, store),
		).rejects.toMatchObject({ code: "PROPOSAL_UNAUTHORIZED" });
	});

	it("rejects an incomplete proposal as MEMORY_NOT_FOUND", async () => {
		const store = new InMemoryMemoryStore();
		await expect(
			proposeJudgment(
				proposeCommand({ requestId: "  " }),
				agentSource,
				store,
			),
		).rejects.toMatchObject({ code: "MEMORY_NOT_FOUND" });
	});

	it("rejects a non-proposable relation as RELATION_NOT_PROPOSABLE", async () => {
		const store = new InMemoryMemoryStore();
		await expect(
			proposeJudgment(
				proposeCommand({ relation: "conflicts_with" }),
				agentSource,
				store,
			),
		).rejects.toMatchObject({ code: "RELATION_NOT_PROPOSABLE" });
	});

	it("delegates a valid proposal to the store's atomic transition", async () => {
		const store = new InMemoryMemoryStore();
		// The store requires two EXISTING observations: save them first.
		const input = {
			topicKey: "judgment/orch/from",
			title: "From",
			kind: "fact" as const,
			scope: {
				kind: "company" as const,
				organizationId: ORG,
				companyId: COMPANY,
				ruc: "20100039201",
				period: "202401",
			},
			content: { what: "a", why: "b", where: "c", learned: "d" },
			fiscalEffect: "none" as const,
			effectiveAt: "2024-01-15T00:00:00.000Z",
			source: agentSource,
		};
		const from = await store.save(input);
		const to = await store.save({ ...input, topicKey: "judgment/orch/to" });
		const result = await proposeJudgment(
			proposeCommand({
				fromId: from.memory.identity.id,
				toId: to.memory.identity.id,
			}),
			agentSource,
			store,
		);
		expect(result.judgment.status).toBe("proposed");
		expect(result.judgment.proposer.actorId).toBe("agent-1");
	});

	it("rejects a zero principal as PRINCIPAL_INVALID", async () => {
		const store = new InMemoryMemoryStore();
		const zero = { subjectId: "" } as Parameters<typeof confirmJudgment>[1];
		await expect(
			confirmJudgment(
				{
					judgmentId: "j-1",
					resolution: "x",
					expectedJudgmentHash: "abc",
					requestId: "req-1",
				},
				zero,
				store,
			),
		).rejects.toMatchObject({ code: "PRINCIPAL_INVALID" });
	});

	it("rejects an empty resolution as RESOLUTION_REQUIRED (never promoting the proposal reason)", async () => {
		const store = new InMemoryMemoryStore();
		await expect(
			confirmJudgment(
				{
					judgmentId: "j-1",
					resolution: "   ",
					expectedJudgmentHash: "abc",
					requestId: "req-1",
				},
				principal(),
				store,
			),
		).rejects.toMatchObject({ code: "RESOLUTION_REQUIRED" });
	});

	it("rejects an incomplete confirm command as JUDGMENT_NOT_FOUND", async () => {
		const store = new InMemoryMemoryStore();
		await expect(
			confirmJudgment(
				{
					judgmentId: "",
					resolution: "x",
					expectedJudgmentHash: "abc",
					requestId: "req-1",
				},
				principal(),
				store,
			),
		).rejects.toMatchObject({ code: "JUDGMENT_NOT_FOUND" });
	});

	it("rejects an empty reject reason as RESOLUTION_REQUIRED", async () => {
		const store = new InMemoryMemoryStore();
		await expect(
			rejectJudgment(
				{
					judgmentId: "j-1",
					reason: "",
					expectedJudgmentHash: "abc",
					requestId: "req-1",
				} as RejectJudgmentCommand,
				principal(),
				store,
			),
		).rejects.toMatchObject({ code: "RESOLUTION_REQUIRED" });
	});

	it("rejects a human withdrawal with PROPOSAL_UNAUTHORIZED", async () => {
		const store = new InMemoryMemoryStore();
		await expect(
			withdrawJudgment(
				{ judgmentId: "j-1", requestId: "req-1" } as WithdrawJudgmentCommand,
				humanSource,
				store,
			),
		).rejects.toMatchObject({ code: "PROPOSAL_UNAUTHORIZED" });
	});

	it("delegates a valid decision to the store's atomic transition", async () => {
		const store = new InMemoryMemoryStore();
		const input = {
			topicKey: "judgment/orch2/from",
			title: "From",
			kind: "fact" as const,
			scope: {
				kind: "company" as const,
				organizationId: ORG,
				companyId: COMPANY,
				ruc: "20100039201",
				period: "202401",
			},
			content: { what: "a", why: "b", where: "c", learned: "d" },
			fiscalEffect: "none" as const,
			effectiveAt: "2024-01-15T00:00:00.000Z",
			source: agentSource,
		};
		const from = await store.save(input);
		const to = await store.save({ ...input, topicKey: "judgment/orch2/to" });
		const proposed = await store.proposeJudgment(
			{
				fromId: from.memory.identity.id,
				toId: to.memory.identity.id,
				relation: "contradicts",
				reason: "r",
				requestId: "req-orch2",
			},
			agentSource,
		);
		const { computeJudgmentHash } = await import("../../core/types.js");
		const hash = await computeJudgmentHash(proposed.judgment);

		const confirmed = await confirmJudgment(
			{
				judgmentId: proposed.judgmentId,
				resolution: "resolucion profesional",
				expectedJudgmentHash: hash,
				requestId: "req-confirm-orch2",
			},
			principal(),
			store,
		);
		expect(confirmed.judgment.status).toBe("confirmed");
		expect(confirmed.judgment.adjudicator?.subjectId).toBe("maria.torres");
		expect(confirmed.judgment.policyVersion).toBe("judgment-policy/v0.4.0");
		// The typed error is an ApprovalError (frozen-code contract).
		expect(ApprovalError).toBeDefined();
	});
});
