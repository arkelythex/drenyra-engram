/**
 * Rule version resolution — TypeScript mirror tests (design §8 Go↔TS parity).
 * The SAME cases as internal/server/rules_verify_test.go
 * TestResolveRuleVersionFromChainPure must yield the identical outcomes.
 */
import { describe, expect, it } from "vitest";
import type { AccountingMemory } from "../types.js";
import {
	RULE_NOT_IN_FORCE,
	RULE_VERSION_MISMATCH,
	RULE_VIGENCIA_OVERLAP,
	RuleVersionError,
	resolveRuleVersionFromChain,
	statusAsOf,
} from "../rules.js";

function mkRule(
	id: string,
	topic: string,
	eff: string,
	exp: string,
): AccountingMemory {
	const base: AccountingMemory = {
		identity: { id, topicKey: topic },
		title: "rule",
		kind: "rule",
		scope: {
			kind: "company",
			organizationId: "cmp_org",
			companyId: "cmp_01",
			ruc: "20100039201",
			period: "202601",
		},
		content: { what: "r", why: "x", where: "f", learned: "x" },
		status: "active",
		fiscalEffect: "none",
		effectiveAt: eff,
		recordedAt: eff,
		contentHash: "",
		revision: 1,
		source: { system: "go-test", actorId: "agent", actorKind: "agent" },
	};
	if (exp !== "") {
		base.validity = { effectiveAt: eff, expiresAt: exp, source: "declared" };
	}
	return base;
}

const v1 = mkRule(
	"rule-v1",
	"policy/t",
	"2026-01-01T00:00:00Z",
	"2026-03-31T23:59:59Z",
);
const v2 = mkRule("rule-v2", "policy/t", "2026-04-01T00:00:00Z", "");

describe("resolveRuleVersionFromChain (Go↔TS parity)", () => {
	it("resolves to v1 when v1 is the sole revision in force", () => {
		const got = resolveRuleVersionFromChain(
			[v1, v2],
			"rule-v1",
			"2026-02-15T00:00:00Z",
		);
		expect(got.identity.id).toBe("rule-v1");
	});

	it("resolves to v2 (open-ended window)", () => {
		const got = resolveRuleVersionFromChain(
			[v1, v2],
			"rule-v2",
			"2026-06-01T00:00:00Z",
		);
		expect(got.identity.id).toBe("rule-v2");
	});

	it("fails RULE_NOT_IN_FORCE before any window", () => {
		expect(() =>
			resolveRuleVersionFromChain([v1, v2], "rule-v1", "2020-01-01T00:00:00Z"),
		).toThrowError(RULE_NOT_IN_FORCE);
	});

	it("adjacent windows: v2 in force at the gap → RULE_VERSION_MISMATCH for a v1 pin", () => {
		expect(() =>
			resolveRuleVersionFromChain([v1, v2], "rule-v1", "2026-04-15T00:00:00Z"),
		).toThrowError(RULE_VERSION_MISMATCH);
	});

	it("fails RULE_VIGENCIA_OVERLAP on overlapping windows", () => {
		const a = mkRule(
			"a",
			"policy/t",
			"2026-01-01T00:00:00Z",
			"2026-12-31T00:00:00Z",
		);
		const b = mkRule(
			"b",
			"policy/t",
			"2026-06-01T00:00:00Z",
			"2027-01-01T00:00:00Z",
		);
		expect(() =>
			resolveRuleVersionFromChain([a, b], "a", "2026-07-01T00:00:00Z"),
		).toThrowError(RULE_VIGENCIA_OVERLAP);
	});

	it("typed error carries the stable code", () => {
		try {
			resolveRuleVersionFromChain([v1, v2], "rule-v2", "2026-02-15T00:00:00Z");
			throw new Error("should have thrown");
		} catch (err) {
			expect(err).toBeInstanceOf(RuleVersionError);
			expect((err as RuleVersionError).code).toBe(RULE_VERSION_MISMATCH);
		}
	});
});

describe("statusAsOf (Go↔TS parity)", () => {
	it("returns the initial status with no transitions before the instant", () => {
		expect(
			statusAsOf(
				"active",
				[{ timestamp: "2026-03-01T00:00:00Z", to: "superseded" }],
				"2026-02-01T00:00:00Z",
			),
		).toBe("active");
	});

	it("applies the last transition at/before the instant", () => {
		expect(
			statusAsOf(
				"active",
				[{ timestamp: "2026-03-01T00:00:00Z", to: "superseded" }],
				"2026-04-01T00:00:00Z",
			),
		).toBe("superseded");
	});
});
