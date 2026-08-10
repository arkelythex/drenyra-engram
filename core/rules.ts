/**
 * Rule version resolution — Phase 6 v0.6.0, design §4.
 *
 * TypeScript mirror of internal/core/rules.go (the PURE resolution logic that
 * must stay byte-identical across runtimes): the vigencia selection and the
 * pinned-version check. The Go↔TS parity intent is the design §8 "Go↔TS
 * canonical bytes" — the mirrored unit test in core/__tests__/rules.test.ts
 * exercises the SAME cases as the Go test.
 */
import type { AccountingMemory, MemoryStatus } from "./types.js";

/** Stable outcome codes — identical strings to core/rules.go. */
export const RULE_VERSION_RESOLVED = "passed";
export const RULE_VERSION_LEGACY_SKIPPED = "skipped";
export const RULE_NOT_IN_FORCE = "RULE_NOT_IN_FORCE";
export const RULE_VIGENCIA_OVERLAP = "RULE_VIGENCIA_OVERLAP";
export const RULE_VERSION_MISMATCH = "RULE_VERSION_MISMATCH";
export const RULE_STATUS_INVALID = "RULE_STATUS_INVALID";
export const RULE_VERSION_TARGET_INVALID = "RULE_VERSION_TARGET_INVALID";

/** RuleVersionError mirrors core.RuleVersionError: code + message. */
export class RuleVersionError extends Error {
	readonly code: string;
	constructor(code: string, message: string) {
		super(`${code}: ${message}`);
		this.code = code;
	}
}

function ruleInForceAt(
	chain: AccountingMemory[],
	instant: string,
): AccountingMemory[] {
	const inForce: AccountingMemory[] = [];
	for (const m of chain) {
		const v = m.validity;
		if (v === undefined || (v.effectiveAt ?? "") === "") {
			// No declared window: in force at/after the rule's own effectiveAt.
			if (m.effectiveAt !== "" && instant >= m.effectiveAt) {
				inForce.push(m);
			}
			continue;
		}
		const vStart = v.effectiveAt ?? "";
		const vEnd = v.expiresAt ?? "";
		if (instant < vStart) {
			continue;
		}
		if (vEnd !== "" && instant >= vEnd) {
			continue;
		}
		inForce.push(m);
	}
	return inForce;
}

/**
 * Pure version resolution (design §4.1 steps 2-6): select the revision(s) in
 * force at the decision instant, fail on zero (RULE_NOT_IN_FORCE) or multiple
 * (RULE_VIGENCIA_OVERLAP), require the sole match to equal the pin
 * (RULE_VERSION_MISMATCH). Mirrors core.ResolveRuleVersionFromChain.
 */
export function resolveRuleVersionFromChain(
	chain: AccountingMemory[],
	pinnedID: string,
	decisionTime: string,
): AccountingMemory {
	if (pinnedID === "" || decisionTime === "") {
		throw new RuleVersionError(
			RULE_VERSION_TARGET_INVALID,
			"pinned version and decision time are required",
		);
	}
	const inForce = ruleInForceAt(chain, decisionTime);
	if (inForce.length === 0) {
		throw new RuleVersionError(
			RULE_NOT_IN_FORCE,
			`no rule revision in force at ${decisionTime}`,
		);
	}
	if (inForce.length > 1) {
		throw new RuleVersionError(
			RULE_VIGENCIA_OVERLAP,
			`multiple rule revisions in force at ${decisionTime} — overlapping vigencia`,
		);
	}
	if (inForce[0].identity.id !== pinnedID) {
		throw new RuleVersionError(
			RULE_VERSION_MISMATCH,
			`the revision in force at ${decisionTime} is not the pinned version`,
		);
	}
	return inForce[0];
}

/**
 * StatusAsOf reconstructs a subject's lifecycle status AT an instant from its
 * ordered transitions (design §4.1 step 7). Mirrors core.StatusAsOf.
 */
export function statusAsOf(
	initial: MemoryStatus,
	transitions: Array<{ timestamp: string; to: MemoryStatus }>,
	instant: string,
): MemoryStatus {
	let status = initial;
	for (const t of transitions) {
		if (t.timestamp !== "" && t.timestamp <= instant) {
			status = t.to;
		}
	}
	return status;
}
