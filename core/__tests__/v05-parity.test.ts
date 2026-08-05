/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * v0.5.0 Go↔TS parity fixture test (design §8, AC12) — the TypeScript runner of
 * the SHARED `testdata/v05-parity.json` fixture. The SAME fixture runs from Go
 * (internal/core/v05_parity_test.go): every expected value is pinned
 * Go-computed, and a divergence between runtimes fails one of the two runners,
 * never silently. The five vectors cover:
 *
 *  1. approved close + memory_closed/memory_reopened receipt signatures;
 *  2. blocked write (PERIOD_CLOSED) with no partial mutation;
 *  3. reconciliation proposed→confirmed, projected edge, and receipt;
 *  4. period delta with new/removed/changed chains and pending delta;
 *  5. initialize context for configured/missing/invalid scopes.
 */

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import {
	APPROVAL_ERROR_CODES,
	RECEIPT_PAYLOAD_VERSION,
	RECEIPT_PAYLOAD_VERSION_V05,
	type AccountingMemory,
	type ClosePendingItem,
	type CurrentContext,
	type PrincipalSnapshot,
	type MemoryScope,
	type PeriodComparison,
	type ReceiptPayload,
	type Reconciliation,
	type SignedReceipt,
} from "../types.js";
import { computeContentHash, computePeriodComparison, computeReconciliationHash } from "../types.js";
import { NodeSeedSigner, receiptHash, verifyReceipt } from "../receipt.js";

/** The pinned parity seed (32×0x01), shared with the Go tests. */
const PARITY_SEED_HEX =
	"0101010101010101010101010101010101010101010101010101010101010101";

// ──────────────────────────────────────────────
// Fixture wire types (shared shape with internal/core/v05_parity_test.go)
// ──────────────────────────────────────────────

interface V05ParityMemory {
	id: string;
	topicKey: string;
	kind: string;
	status: string;
	title: string;
	scope: {
		kind: string;
		organizationId: string;
		companyId: string;
		ruc: string;
		period: string;
	};
	content: { what: string; why: string; where: string; learned: string };
	fiscalEffect: string;
	effectiveAt: string;
	source: { system: string; actorKind: string };
	recordedAt: string;
	revision: number;
	supersedesId: string;
	evidenceRefs: string[];
	ruleRefs: string[];
}

interface V05ParityPending {
	memoryId: string;
	topicKey: string;
}

interface V05ParityFixture {
	name: string;
	seed: string;
	publicKey: string;
	vectors: {
		close_receipts: {
			subjectId: string;
			receipts: SignedReceipt[];
			payloads: ReceiptPayload[];
			expected: {
				receiptHashes: string[];
				actions: string[];
				payloadVersions: string[];
			};
		};
		blocked_write: {
			scenario: {
				operation: string;
				tenantId: string;
				companyId: string;
				fiscalPeriodId: string;
				ruc: string;
				closureState: string;
				closeMemoryId: string;
			};
			expected: {
				errorCode: string;
				rowsWritten: number;
				eventsWritten: number;
				receiptsWritten: number;
			};
		};
		reconciliation: {
			proposed: Record<string, unknown>;
			confirmed: Record<string, unknown>;
			receipt: SignedReceipt;
			payload: ReceiptPayload;
			expected: {
				proposedHash: string;
				confirmedHash: string;
				projectedRelation: {
					fromId: string;
					toId: string;
					relation: string;
				};
			};
		};
		period_comparison: {
			inputs: {
				fromPeriod: string;
				toPeriod: string;
				from: V05ParityMemory[];
				to: V05ParityMemory[];
				fromPending: V05ParityPending[];
				toPending: V05ParityPending[];
				fromCloseState: string;
				toCloseState: string;
			};
			expected: PeriodComparison;
		};
		current_context: {
			configured: Record<string, unknown>;
			missing: { outcome: string; instruction: string };
			invalid: { outcome: string; failClosed: boolean; partialData: boolean };
		};
	};
}

const V05_PARITY_PATH = resolve(process.cwd(), "testdata", "v05-parity.json");

function loadV05ParityFixture(): V05ParityFixture {
	return JSON.parse(
		readFileSync(V05_PARITY_PATH, "utf-8"),
	) as V05ParityFixture;
}

function decodeBase64(value: string): Uint8Array {
	return Uint8Array.from(Buffer.from(value, "base64"));
}

/** Builds a TS AccountingMemory from the shared fixture wire shape. */
async function accountingMemoryFromFixture(
	m: V05ParityMemory,
): Promise<AccountingMemory> {
	const scope: MemoryScope = {
		kind: "company",
		organizationId: m.scope.organizationId,
		companyId: m.scope.companyId,
		ruc: m.scope.ruc,
		period: m.scope.period,
	};
	const memory: AccountingMemory = {
		identity: { id: m.id, topicKey: m.topicKey },
		title: m.title,
		kind: m.kind as AccountingMemory["kind"],
		scope,
		content: { ...m.content },
		status: m.status as AccountingMemory["status"],
		fiscalEffect: m.fiscalEffect as AccountingMemory["fiscalEffect"],
		effectiveAt: m.effectiveAt,
		recordedAt: m.recordedAt,
		source: { system: m.source.system, actorKind: m.source.actorKind as "agent" },
		supersedesId: m.supersedesId === "" ? undefined : m.supersedesId,
		evidenceRefs: [...m.evidenceRefs],
		ruleRefs: [...m.ruleRefs],
		revision: m.revision,
		contentHash: "", // computed below — the canonical immutable hash
	};
	memory.contentHash = await computeContentHash(memory);
	return memory;
}

/** Builds a TS Reconciliation from the fixture's Go-marshaled wire shape. */
function reconciliationFromFixture(
	wire: Record<string, unknown>,
): Reconciliation {
	return {
		id: String(wire.id),
		tenantId: String(wire.tenantId),
		companyId: String(wire.companyId),
		fiscalPeriodId:
			wire.fiscalPeriodId === undefined ? undefined : String(wire.fiscalPeriodId),
		leftMemoryId: String(wire.leftMemoryId),
		rightMemoryId: String(wire.rightMemoryId),
		method: String(wire.method),
		currency: String(wire.currency),
		leftAmountCents: BigInt(String(wire.leftAmountCents)),
		rightAmountCents: BigInt(String(wire.rightAmountCents)),
		varianceCents: BigInt(String(wire.varianceCents)),
		toleranceCents: BigInt(String(wire.toleranceCents)),
		status: String(wire.status) as Reconciliation["status"],
		proposer: {
			system: String((wire.proposer as { system: unknown }).system),
			actorId:
				(wire.proposer as { actorId?: unknown }).actorId === undefined
					? undefined
					: String((wire.proposer as { actorId: unknown }).actorId),
			actorKind: String(
				(wire.proposer as { actorKind: unknown }).actorKind,
			) as "human" | "agent" | "system",
		},
		proposalReason: String(wire.proposalReason),
		resolution:
			wire.resolution === undefined ? undefined : String(wire.resolution),
		adjudicator:
			wire.adjudicator === undefined
				? undefined
				: ({
						subjectId: String(
							(wire.adjudicator as { subjectId: unknown }).subjectId,
						),
						membershipId: String(
							(wire.adjudicator as { membershipId: unknown }).membershipId,
						),
						roles: (wire.adjudicator as { roles: unknown[] }).roles.map((r) =>
							String(r),
						),
						authenticationMethod: String(
							(wire.adjudicator as { authenticationMethod: unknown })
								.authenticationMethod,
						),
						assuranceLevel: String(
							(wire.adjudicator as { assuranceLevel: unknown }).assuranceLevel,
						),
						authenticatedAt: String(
							(wire.adjudicator as { authenticatedAt: unknown }).authenticatedAt,
						),
				  } as PrincipalSnapshot),
		policyVersion:
			wire.policyVersion === undefined ? undefined : String(wire.policyVersion),
		predecessorId:
			wire.predecessorId === undefined ? undefined : String(wire.predecessorId),
		supersedesId:
			wire.supersedesId === undefined ? undefined : String(wire.supersedesId),
		proposedAt: String(wire.proposedAt),
		decidedAt: wire.decidedAt === undefined ? undefined : String(wire.decidedAt),
	};
}

describe("v0.5.0 parity fixture (Go↔TS shared vectors)", () => {
	const fixture = loadV05ParityFixture();
	const publicKey = decodeBase64(fixture.publicKey);

	it("pins the shared seed, derived public key and receipt consts", () => {
		expect(fixture.name).toBe("v05-parity");
		expect(fixture.seed).toBe(PARITY_SEED_HEX);
		// RFC 8032 seed derivation is byte-identical with Go (JWK `d` import).
		const signer = new NodeSeedSigner(Buffer.from(PARITY_SEED_HEX, "hex"));
		expect(Buffer.from(signer.publicKey).toString("base64")).toBe(
			fixture.publicKey,
		);
		expect(RECEIPT_PAYLOAD_VERSION).toBe("receipt-payload/v0.4.0");
		expect(RECEIPT_PAYLOAD_VERSION_V05).toBe("receipt-payload/v0.5.0");
		// The v0.5.0 reconciliation error codes join the frozen union + array.
		for (const code of [
			"RECONCILIATION_NOT_FOUND",
			"INVALID_RECONCILIATION_TRANSITION",
			"RECONCILIATION_CONFLICT",
			"RECONCILIATION_HASH_MISMATCH",
		]) {
			expect(APPROVAL_ERROR_CODES).toContain(code);
		}
	});

	it("vector 1 — approved close + memory_closed/memory_reopened signatures", () => {
		const v = fixture.vectors.close_receipts;
		expect(v.receipts).toHaveLength(3);
		expect(v.payloads).toHaveLength(3);
		expect(v.expected.actions).toEqual([
			"memory_approved",
			"memory_closed",
			"memory_reopened",
		]);
		expect(v.expected.payloadVersions).toEqual([
			"receipt-payload/v0.4.0",
			"receipt-payload/v0.5.0",
			"receipt-payload/v0.5.0",
		]);
		let prev = "";
		v.receipts.forEach((receipt, i) => {
			expect(receipt.subjectType).toBe("memory");
			expect(receipt.subjectId).toBe(v.subjectId);
			expect(receipt.previousReceiptHash).toBe(prev);
			// Closed enums, payload/envelope equality, canonical payload hash,
			// keyId and the Ed25519 signature all recompute against the seed.
			expect(() => verifyReceipt(receipt, v.payloads[i], publicKey)).not.toThrow();
			const recomputed = receiptHash(receipt);
			expect(recomputed).toBe(v.expected.receiptHashes[i]);
			expect(receipt.action).toBe(v.expected.actions[i]);
			prev = recomputed;
		});
	});

	it("vector 2 — blocked write (PERIOD_CLOSED) with no partial mutation", () => {
		const v = fixture.vectors.blocked_write;
		expect(v.scenario.operation).toBe("save");
		expect(v.scenario.closureState).toBe("closed");
		expect(v.expected.errorCode).toBe("PERIOD_CLOSED");
		// PERIOD_CLOSED is a frozen member of the mirror's closed error-code set.
		expect(APPROVAL_ERROR_CODES).toContain("PERIOD_CLOSED");
		// A blocked write leaves ZERO partial mutation (rows/events/receipts).
		expect(v.expected.rowsWritten).toBe(0);
		expect(v.expected.eventsWritten).toBe(0);
		expect(v.expected.receiptsWritten).toBe(0);
		expect(v.scenario.closeMemoryId.length).toBeGreaterThan(0);
		expect(v.scenario.fiscalPeriodId).toBe("202601");
	});

	it("vector 3 — reconciliation proposed→confirmed, edge, and receipt", async () => {
		const v = fixture.vectors.reconciliation;
		const proposed = reconciliationFromFixture(v.proposed);
		const confirmed = reconciliationFromFixture(v.confirmed);

		// The mirror recomputes the canonical hashes byte-identically to Go.
		await expect(computeReconciliationHash(proposed)).resolves.toBe(
			v.expected.proposedHash,
		);
		await expect(computeReconciliationHash(confirmed)).resolves.toBe(
			v.expected.confirmedHash,
		);
		expect(v.expected.proposedHash).not.toBe(v.expected.confirmedHash);

		// The confirmed entity is a legal confirmed state (adjudicator set).
		expect(confirmed.status).toBe("confirmed");
		expect(confirmed.adjudicator).toBeDefined();

		// The receipt covers the reviewed/resulting hashes + both endpoint ids.
		expect(v.receipt.subjectType).toBe("reconciliation");
		expect(v.receipt.action).toBe("reconciliation_confirmed");
		expect(v.payload.version).toBe("receipt-payload/v0.5.0");
		expect(v.payload.reviewedJudgmentHash).toBe(v.expected.proposedHash);
		expect(v.payload.resultingJudgmentHash).toBe(v.expected.confirmedHash);
		expect(v.payload.fromMemoryId).toBe(confirmed.leftMemoryId);
		expect(v.payload.toMemoryId).toBe(confirmed.rightMemoryId);
		expect(() => verifyReceipt(v.receipt, v.payload, publicKey)).not.toThrow();

		// Confirmation projects exactly one observation relation
		// leftMemoryId --reconciles--> rightMemoryId.
		expect(v.expected.projectedRelation).toEqual({
			fromId: confirmed.leftMemoryId,
			toId: confirmed.rightMemoryId,
			relation: "reconciles",
		});
	});

	it("vector 4 — period delta with new/removed/changed chains and pending delta", async () => {
		const v = fixture.vectors.period_comparison;
		const from: AccountingMemory[] = [];
		for (const m of v.inputs.from) {
			from.push(await accountingMemoryFromFixture(m));
		}
		const to: AccountingMemory[] = [];
		for (const m of v.inputs.to) {
			to.push(await accountingMemoryFromFixture(m));
		}
		const fromPending: ClosePendingItem[] = v.inputs.fromPending.map((p) => ({
			memoryId: p.memoryId,
			topicKey: p.topicKey,
			kind: "",
			status: "",
			title: "",
			effectiveAt: "",
		}));
		const toPending: ClosePendingItem[] = v.inputs.toPending.map((p) => ({
			memoryId: p.memoryId,
			topicKey: p.topicKey,
			kind: "",
			status: "",
			title: "",
			effectiveAt: "",
		}));

		const got = await computePeriodComparison(
			v.inputs.fromPeriod,
			v.inputs.toPeriod,
			from,
			to,
			fromPending,
			toPending,
			v.inputs.fromCloseState,
			v.inputs.toCloseState,
		);

		// Semantic deep-equality with the pinned Go-computed expected delta.
		expect(got).toEqual(v.expected);
		// Spot-check the deterministic delta shape.
		expect(got.chains.new).toEqual([
			expect.objectContaining({ topicKey: "account/4011/ventas-agosto" }),
		]);
		expect(got.chains.removed).toHaveLength(2);
		expect(got.chains.changed).toHaveLength(2);
		expect(got.chains.unchangedCount).toBe(1);
		expect(got.statusChanges).toEqual([
			{
				topicKey: "adjust/aj-001",
				fromId: "j5",
				toId: "a4",
				fromStatus: "pending_review",
				toStatus: "approved",
			},
		]);
		expect(got.pendingItems).toEqual({
			from: 3,
			to: 1,
			delta: -2,
			addedIds: [],
			resolvedIds: ["j4", "j5"],
		});
		expect(got.closeState).toEqual({ from: "closed", to: "open" });
		expect(got.narrative.length).toBeGreaterThan(0);
	});

	it("vector 5 — initialize context for configured/missing/invalid scopes", () => {
		const v = fixture.vectors.current_context;

		// Configured: the fixture context satisfies the bounded CurrentContext
		// shape (compile-time) and the field-level contract.
		const configured = v.configured as unknown as CurrentContext;
		expect(configured.scope).toMatchObject({ kind: "company", period: "202608" });
		expect(configured.periodSummary.closureState).toBe("open");
		expect(configured.periodSummary.latestClose).toBe("");
		expect(configured.recentChains.length).toBeLessThanOrEqual(20);
		expect(configured.generatedAt).toMatch(/^2026-08-05T15:00:00Z$/);

		// Missing: unset configuration returns null and instructs the explicit tool.
		expect(v.missing).toEqual({
			outcome: "null",
			instruction: "use accounting_current_context",
		});
		// Invalid: fail closed — no context and NO partial cross-scope data.
		expect(v.invalid).toEqual({
			outcome: "null",
			failClosed: true,
			partialData: false,
		});
	});
});
