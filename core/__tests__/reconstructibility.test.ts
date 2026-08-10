/**
 * G-10 reconstructibility — TypeScript mirror tests (design D-2, spec FZ-1/
 * FZ-2/FR-9). The SAME cases as internal/core/reconstructibility_test.go must
 * yield the identical outcomes: the FZ-1 material-decision eligibility
 * predicate, the FZ-2 first-failure classifier with the closed six-reason
 * vocabulary, the integer-only ratio/percentage (never floating point) and the
 * pure aggregation of the frozen metric. No `any` (IR-5); counts are plain
 * integers from bounded slice lengths; money never appears here (IR-1).
 */
import { describe, expect, it } from "vitest";
import type { AccountingMemory, MemoryScope } from "../types.js";
import {
	RECONSTRUCTIBILITY_REASONS,
	aggregateReconstructibility,
	buildReconstructibilityCounts,
	classifyReconstructibility,
	isMaterialDecision,
	isValidReconstructibilityReason,
	reconstructibilityPercentage,
	type ReconstructibilityReason,
} from "../reconstructibility.js";
import type { VerificationReport } from "../verify.js";
import {
	LAYER_EVIDENCE_AVAILABILITY,
	LAYER_OBJECT_AVAILABILITY,
	LAYER_PRINCIPAL_PROVENANCE,
	LAYER_RULE_AVAILABILITY,
	LAYER_RULE_VERSION_VIGENCIA,
	LAYER_SIGNATURE,
} from "../verify.js";

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

const SCOPE: MemoryScope = {
	kind: "company",
	organizationId: "org-001",
	companyId: "acme",
	ruc: "20100039201",
	period: "202401",
};

function companyScope(
	overrides: Partial<{
		organizationId: string;
		companyId: string;
		ruc: string;
		period?: string;
	}> = {},
): MemoryScope {
	return { ...SCOPE, ...overrides };
}

function eligibleMemory(id = "mem-1", topicKey = "decision/expense/001"): AccountingMemory {
	return {
		identity: { id, topicKey },
		title: "expense classification",
		kind: "decision",
		scope: { ...SCOPE },
		content: { what: "w", why: "y", where: "f", learned: "x" },
		status: "approved",
		fiscalEffect: "journal_entry",
		effectiveAt: "2026-01-15T12:00:00.000Z",
		recordedAt: "2026-01-15T12:00:00.000Z",
		source: { system: "go-test", actorKind: "agent" },
		materialityLevel: "material",
		contentHash: "",
		revision: 1,
	};
}

function layer(name: string, status: "passed" | "failed" | "skipped"): {
	name: string;
	status: "passed" | "failed" | "skipped";
	detail: string;
} {
	return { name, status, detail: "test" };
}

/** A fully-passing report: the six receipt layers plus the object layers. */
function passingReport(): VerificationReport {
	const receiptLayers = [
		"payload canonicalization",
		"envelope integrity",
		"signature",
		"signing-key validity",
		"tenant/company scope",
		"chain link",
	];
	const layers = [
		...receiptLayers.map((name) => layer(name, "passed")),
		layer(LAYER_PRINCIPAL_PROVENANCE, "passed"),
		layer("supersession chain", "passed"),
		layer(LAYER_EVIDENCE_AVAILABILITY, "passed"),
		layer(LAYER_OBJECT_AVAILABILITY, "passed"),
		layer(LAYER_RULE_AVAILABILITY, "passed"),
		layer(LAYER_RULE_VERSION_VIGENCIA, "passed"),
	];
	return {
		subjectType: "memory",
		subjectId: "mem-1",
		outcome: "passed",
		receipts: [],
		layers,
		accountingCorrectness: "Accounting correctness: NOT ASSERTED",
	};
}

function reportWith(layers: { name: string; status: "passed" | "failed" | "skipped"; detail: string }[]): VerificationReport {
	const r = passingReport();
	r.layers = layers;
	return r;
}

// ──────────────────────────────────────────────
// FZ-1 eligibility matrix
// ──────────────────────────────────────────────

describe("isMaterialDecision (FZ-1 eligibility matrix)", () => {
	it("accepts the fully eligible decision", () => {
		expect(isMaterialDecision(eligibleMemory(), SCOPE, true)).toBe(true);
	});

	it("rejects non-latest revisions", () => {
		expect(isMaterialDecision(eligibleMemory(), SCOPE, false)).toBe(false);
	});

	it("rejects every non-approved status", () => {
		for (const status of ["active", "pending_review", "rejected", "returned", "superseded", "voided"] as const) {
			const m = eligibleMemory();
			m.status = status;
			expect(isMaterialDecision(m, SCOPE, true)).toBe(false);
		}
	});

	it("accepts exactly the six frozen fiscal effects", () => {
		for (const effect of [
			"journal_entry",
			"adjustment",
			"reclassification",
			"declaration",
			"closing",
			"sunat_filing",
		] as const) {
			const m = eligibleMemory();
			m.fiscalEffect = effect;
			expect(isMaterialDecision(m, SCOPE, true)).toBe(true);
		}
	});

	it("rejects none and approval fiscal effects", () => {
		for (const effect of ["none", "approval"] as const) {
			const m = eligibleMemory();
			m.fiscalEffect = effect;
			expect(isMaterialDecision(m, SCOPE, true)).toBe(false);
		}
	});

	it("accepts material and critical levels and rejects normal/nil", () => {
		for (const level of ["material", "critical"] as const) {
			const m = eligibleMemory();
			m.materialityLevel = level;
			expect(isMaterialDecision(m, SCOPE, true)).toBe(true);
		}
		for (const level of ["normal", undefined] as const) {
			const m = eligibleMemory();
			m.materialityLevel = level;
			expect(isMaterialDecision(m, SCOPE, true)).toBe(false);
		}
	});

	it("never lets the numeric materiality threshold participate", () => {
		// Huge threshold + normal level → excluded; nil threshold + material → eligible.
		const normal = eligibleMemory();
		normal.materialityLevel = "normal";
		normal.materiality = BigInt("9999999999");
		expect(isMaterialDecision(normal, SCOPE, true)).toBe(false);

		const material = eligibleMemory();
		material.materiality = undefined;
		expect(isMaterialDecision(material, SCOPE, true)).toBe(true);
	});

	it("rejects every exact-scope mismatch and institutional scopes", () => {
		const cases: [string, (m: AccountingMemory) => void][] = [
			["organization mismatch", (m) => (m.scope = companyScope({ organizationId: "org-999" }))],
			["company mismatch", (m) => (m.scope = companyScope({ companyId: "other-co" }))],
			["ruc mismatch", (m) => (m.scope = companyScope({ ruc: "20600995804" }))],
			["period mismatch", (m) => (m.scope = companyScope({ period: "202402" }))],
			[
				"institutional memory",
				(m) => (m.scope = { kind: "institutional" }),
			],
		];
		for (const [name, mutate] of cases) {
			const m = eligibleMemory();
			mutate(m);
			expect(isMaterialDecision(m, SCOPE, true), name).toBe(false);
		}
		const inst = { kind: "institutional" } as MemoryScope;
		expect(isMaterialDecision(eligibleMemory(), inst, true)).toBe(false);
	});

	it("pins that kind is NOT an eligibility axis (frozen)", () => {
		const m = eligibleMemory();
		m.kind = "fact";
		expect(isMaterialDecision(m, SCOPE, true)).toBe(true);
	});
});

// ──────────────────────────────────────────────
// FZ-2 classifier precedence
// ──────────────────────────────────────────────

describe("classifyReconstructibility (FZ-2 first-failure precedence)", () => {
	it("classifies a fully-passing decision as reconstructible", () => {
		const { reconstructible, reason } = classifyReconstructibility(eligibleMemory(), passingReport());
		expect(reconstructible).toBe(true);
		expect(reason).toBe("");
	});

	it("maps every one of the six categories as the first failure", () => {
		const cases: { name: string; mutate?: (m: AccountingMemory) => void; failedLayer?: string; want: ReconstructibilityReason }[] = [
			{ name: "not_approved", mutate: (m) => (m.status = "rejected"), want: "not_approved" },
			{ name: "receipt_failed (signature)", failedLayer: LAYER_SIGNATURE, want: "receipt_failed" },
			{ name: "receipt_failed (provenance)", failedLayer: LAYER_PRINCIPAL_PROVENANCE, want: "receipt_failed" },
			{ name: "missing_evidence", failedLayer: LAYER_EVIDENCE_AVAILABILITY, want: "missing_evidence" },
			{ name: "evidence_missing_object", failedLayer: LAYER_OBJECT_AVAILABILITY, want: "evidence_missing_object" },
			{ name: "rule_unresolved", failedLayer: LAYER_RULE_AVAILABILITY, want: "rule_unresolved" },
			{ name: "rule_version_failed", failedLayer: LAYER_RULE_VERSION_VIGENCIA, want: "rule_version_failed" },
		];
		for (const c of cases) {
			const m = eligibleMemory();
			c.mutate?.(m);
			const report = c.failedLayer ? reportWith([layer(c.failedLayer, "failed")]) : passingReport();
			const { reconstructible, reason } = classifyReconstructibility(m, report);
			expect(reconstructible, c.name).toBe(false);
			expect(reason, c.name).toBe(c.want);
		}
	});

	it("applies the frozen precedence over later failures", () => {
		const receiptWins = classifyReconstructibility(
			eligibleMemory(),
			reportWith([
				layer(LAYER_SIGNATURE, "failed"),
				layer(LAYER_EVIDENCE_AVAILABILITY, "failed"),
				layer(LAYER_RULE_AVAILABILITY, "failed"),
			]),
		);
		expect(receiptWins.reason).toBe("receipt_failed");

		const evidenceWins = classifyReconstructibility(
			eligibleMemory(),
			reportWith([
				layer(LAYER_EVIDENCE_AVAILABILITY, "failed"),
				layer(LAYER_OBJECT_AVAILABILITY, "failed"),
				layer(LAYER_RULE_VERSION_VIGENCIA, "failed"),
			]),
		);
		expect(evidenceWins.reason).toBe("missing_evidence");

		const objectWins = classifyReconstructibility(
			eligibleMemory(),
			reportWith([layer(LAYER_OBJECT_AVAILABILITY, "failed"), layer(LAYER_RULE_AVAILABILITY, "failed")]),
		);
		expect(objectWins.reason).toBe("evidence_missing_object");

		const ruleWins = classifyReconstructibility(
			eligibleMemory(),
			reportWith([layer(LAYER_RULE_AVAILABILITY, "failed"), layer(LAYER_RULE_VERSION_VIGENCIA, "failed")]),
		);
		expect(ruleWins.reason).toBe("rule_unresolved");

		// not_approved is first even when receipts fail.
		const notApproved = eligibleMemory();
		notApproved.status = "pending_review";
		const na = classifyReconstructibility(notApproved, reportWith([layer(LAYER_SIGNATURE, "failed")]));
		expect(na.reason).toBe("not_approved");
	});

	it("treats skipped and absent layers as non-failures", () => {
		const skippedObject = classifyReconstructibility(
			eligibleMemory(),
			reportWith([layer(LAYER_OBJECT_AVAILABILITY, "skipped")]),
		);
		expect(skippedObject.reconstructible).toBe(true);

		// No rule-version layer at all (memory without structured rule links).
		const withoutRuleVersion = passingReport();
		withoutRuleVersion.layers = withoutRuleVersion.layers.filter((l) => l.name !== LAYER_RULE_VERSION_VIGENCIA);
		expect(classifyReconstructibility(eligibleMemory(), withoutRuleVersion).reconstructible).toBe(true);
	});
});

// ──────────────────────────────────────────────
// Closed vocabulary
// ──────────────────────────────────────────────

describe("reconstructibility reason vocabulary (closed)", () => {
	it("has exactly the six frozen reasons in order", () => {
		expect(RECONSTRUCTIBILITY_REASONS).toEqual([
			"not_approved",
			"receipt_failed",
			"missing_evidence",
			"evidence_missing_object",
			"rule_unresolved",
			"rule_version_failed",
		]);
		expect(RECONSTRUCTIBILITY_REASONS).toHaveLength(6);
	});

	it("rejects arbitrary strings", () => {
		for (const bogus of ["", "made_up", "RECEIPT_FAILED", "not_approved "]) {
			expect(isValidReconstructibilityReason(bogus)).toBe(false);
		}
		for (const reason of RECONSTRUCTIBILITY_REASONS) {
			expect(isValidReconstructibilityReason(reason)).toBe(true);
		}
	});
});

// ──────────────────────────────────────────────
// Integer ratio/percentage — never floating point
// ──────────────────────────────────────────────

describe("reconstructibilityPercentage (integer math)", () => {
	it("truncates, never rounds", () => {
		expect(reconstructibilityPercentage(2, 3)).toBe(66);
		expect(reconstructibilityPercentage(1, 3)).toBe(33);
		expect(reconstructibilityPercentage(1, 2)).toBe(50);
		expect(reconstructibilityPercentage(5, 5)).toBe(100);
		expect(reconstructibilityPercentage(7, 2)).toBe(350);
		expect(reconstructibilityPercentage(0, 5)).toBe(0);
	});

	it("is null for a zero denominator — never 0% or 100%", () => {
		expect(reconstructibilityPercentage(0, 0)).toBeNull();
		expect(reconstructibilityPercentage(3, 0)).toBeNull();
	});

	it("fails closed on negative denominators", () => {
		expect(reconstructibilityPercentage(3, -1)).toBeNull();
	});

	it("guards multiplication overflow", () => {
		expect(reconstructibilityPercentage(Number.MAX_SAFE_INTEGER, 1)).toBeNull();
		// The largest representable percentage computes exactly.
		const maxSafe = Math.floor(Number.MAX_SAFE_INTEGER / 100);
		expect(reconstructibilityPercentage(maxSafe * 2, 2)).toBe(maxSafe * 100);
	});
});

// ──────────────────────────────────────────────
// Frozen count shape (zero denominator)
// ──────────────────────────────────────────────

describe("buildReconstructibilityCounts (frozen shape)", () => {
	it("emits the frozen zero-denominator representation", () => {
		expect(buildReconstructibilityCounts(0, 0)).toEqual({
			denominator: 0,
			numerator: 0,
			ratio: { numerator: 0, denominator: 0 },
			percentage: null,
			zeroDenominator: true,
		});
	});

	it("emits the non-zero shape with integer truncation", () => {
		expect(buildReconstructibilityCounts(3, 2)).toEqual({
			denominator: 3,
			numerator: 2,
			ratio: { numerator: 2, denominator: 3 },
			percentage: 66,
			zeroDenominator: false,
		});
	});
});

// ──────────────────────────────────────────────
// Pure aggregation
// ──────────────────────────────────────────────

describe("aggregateReconstructibility (pure, deterministic)", () => {
	it("counts only FZ-1 eligible heads and groups reasons", () => {
		const reconstructible = eligibleMemory("mem-a", "decision/a");
		const missingEvidence = eligibleMemory("mem-b", "decision/b");
		const noReceipts = eligibleMemory("mem-c", "decision/c");
		const notEligibleStatus = eligibleMemory("mem-d", "decision/d");
		notEligibleStatus.status = "rejected";
		const notEligibleEffect = eligibleMemory("mem-e", "decision/e");
		notEligibleEffect.fiscalEffect = "none";
		const outOfScope = eligibleMemory("mem-f", "decision/f");
		outOfScope.scope = companyScope({ companyId: "other-co" });

		// Deliberately reversed insertion order.
		const heads = [outOfScope, notEligibleEffect, noReceipts, notEligibleStatus, missingEvidence, reconstructible];
		const reports: Record<string, VerificationReport> = {
			"mem-a": passingReport(),
			"mem-b": reportWith([layer(LAYER_EVIDENCE_AVAILABILITY, "failed")]),
			// mem-c has NO report entry → fails closed as receipt_failed.
		};

		const got = aggregateReconstructibility(heads, reports, SCOPE);
		expect(got.denominator).toBe(3);
		expect(got.numerator).toBe(1);
		expect(got.reasons.missing_evidence).toEqual(["mem-b"]);
		expect(got.reasons.receipt_failed).toEqual(["mem-c"]);
		expect(got.reasons.not_approved).toBeUndefined();
		for (const id of ["mem-d", "mem-e", "mem-f"]) {
			for (const ids of Object.values(got.reasons)) {
				expect(ids).not.toContain(id);
			}
		}
	});

	it("sorts reason IDs bytewise independent of insertion order", () => {
		const makeHead = (id: string, topicKey: string): AccountingMemory => {
			const m = eligibleMemory(id, topicKey);
			return m;
		};
		const heads = [makeHead("mem-z", "decision/z"), makeHead("mem-10", "decision/10"), makeHead("mem-a", "decision/a")];
		const failed = reportWith([layer(LAYER_RULE_AVAILABILITY, "failed")]);
		const reports: Record<string, VerificationReport> = {
			"mem-z": failed,
			"mem-10": failed,
			"mem-a": failed,
		};
		const got = aggregateReconstructibility(heads, reports, SCOPE);
		expect(got.reasons.rule_unresolved).toEqual(["mem-10", "mem-a", "mem-z"]);
		expect(got.denominator).toBe(3);
		expect(got.numerator).toBe(0);
	});
});
