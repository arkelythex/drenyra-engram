/**
 * G-10 reconstructibility — TypeScript mirror of internal/core/
 * reconstructibility.go (design D-2, spec FZ-1/FZ-2/FR-9). The SAME pure logic
 * must stay behavior-identical across runtimes: the FZ-1 material-decision
 * eligibility predicate, the FZ-2 first-failure classifier with the closed
 * six-reason vocabulary, the integer-only ratio/percentage (never floating
 * point; money never appears here) and the pure aggregation of the frozen
 * metric.
 *
 * PURITY BOUNDARY: every function is pure — no store access, no I/O, no clocks.
 * The server aggregator composes these functions with the narrow store reads.
 * No `any` (IR-5): precise types only.
 */
import type { AccountingMemory, MemoryScope } from "./types.js";
import { scopeEquals } from "./types.js";
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
	LAYER_TENANT_COMPANY_SCOPE,
	type VerificationReport,
} from "./verify.js";

// ──────────────────────────────────────────────
// FZ-2 closed reason vocabulary
// ──────────────────────────────────────────────

/**
 * The complete, closed reason vocabulary in the frozen FZ-2 order — exactly six
 * members. A decision is NEVER classified with more than one reason, and no
 * other reason string may be emitted (FZ-2).
 */
export const RECONSTRUCTIBILITY_REASONS = [
	"not_approved",
	"receipt_failed",
	"missing_evidence",
	"evidence_missing_object",
	"rule_unresolved",
	"rule_version_failed",
] as const;

export type ReconstructibilityReason = (typeof RECONSTRUCTIBILITY_REASONS)[number];

/** Type guard over the closed vocabulary; arbitrary strings never classify. */
export function isValidReconstructibilityReason(reason: string): reason is ReconstructibilityReason {
	return (RECONSTRUCTIBILITY_REASONS as readonly string[]).includes(reason);
}

// ──────────────────────────────────────────────
// FZ-1 material-decision eligibility (denominator)
// ──────────────────────────────────────────────

/**
 * The frozen FZ-1 eligibility predicate: latest chain revision (isLatest),
 * exact company scope + byte-equal period, StatusApproved, one of the six
 * frozen fiscal effects and a declared material/critical level (nil — which
 * means normal — is NOT eligible). The numeric materiality cents threshold never
 * participates (frozen decision). Pure.
 */
export function isMaterialDecision(
	memory: AccountingMemory,
	requestedScope: MemoryScope,
	isLatest: boolean,
): boolean {
	if (!isLatest) {
		return false;
	}
	if (!scopeEquals(memory.scope, requestedScope)) {
		return false;
	}
	if (memory.status !== "approved") {
		return false;
	}
	switch (memory.fiscalEffect) {
		case "journal_entry":
		case "adjustment":
		case "reclassification":
		case "declaration":
		case "closing":
		case "sunat_filing":
			break;
		default:
			return false;
	}
	if (memory.materialityLevel === undefined) {
		return false;
	}
	return memory.materialityLevel === "material" || memory.materialityLevel === "critical";
}

// ──────────────────────────────────────────────
// FZ-2 reconstructibility classifier
// ──────────────────────────────────────────────

/** The six receipt layer names plus principal provenance (FZ-2 step d). */
const RECEIPT_LAYER_NAMES: readonly string[] = [
	LAYER_PAYLOAD_CANONICALIZATION,
	LAYER_ENVELOPE_INTEGRITY,
	LAYER_SIGNATURE,
	LAYER_SIGNING_KEY_VALIDITY,
	LAYER_TENANT_COMPANY_SCOPE,
	LAYER_CHAIN_LINK,
];

/** receiptChainFailed: any failed receipt layer OR failed approval provenance. */
function receiptChainFailed(report: VerificationReport): boolean {
	for (const l of report.layers) {
		if (RECEIPT_LAYER_NAMES.includes(l.name) || l.name === LAYER_PRINCIPAL_PROVENANCE) {
			if (l.status === "failed") {
				return true;
			}
		}
	}
	return false;
}

/** reportLayerFailed: the named top-level layer is present AND failed (skipped
 * or absent is not a failure). */
function reportLayerFailed(report: VerificationReport, name: string): boolean {
	for (const l of report.layers) {
		if (l.name === name) {
			return l.status === "failed";
		}
	}
	return false;
}

/**
 * The classification of one decision: reconstructible (numerator member) or
 * NOT reconstructible with exactly ONE closed reason. Mirrors Go's
 * ClassifyReconstructibility (reason, ok) tuple.
 */
export interface ReconstructibilityClassification {
	reconstructible: boolean;
	reason: ReconstructibilityReason | "";
}

/**
 * FZ-2 first-failure precedence: approval → receipt → evidence → object →
 * rule → rule-version. A decision is never classified with more than one reason.
 * Pure.
 */
export function classifyReconstructibility(
	memory: AccountingMemory,
	report: VerificationReport,
): ReconstructibilityClassification {
	if (memory.status !== "approved") {
		return { reconstructible: false, reason: "not_approved" };
	}
	if (receiptChainFailed(report)) {
		return { reconstructible: false, reason: "receipt_failed" };
	}
	if (reportLayerFailed(report, LAYER_EVIDENCE_AVAILABILITY)) {
		return { reconstructible: false, reason: "missing_evidence" };
	}
	if (reportLayerFailed(report, LAYER_OBJECT_AVAILABILITY)) {
		return { reconstructible: false, reason: "evidence_missing_object" };
	}
	if (reportLayerFailed(report, LAYER_RULE_AVAILABILITY)) {
		return { reconstructible: false, reason: "rule_unresolved" };
	}
	if (reportLayerFailed(report, LAYER_RULE_VERSION_VIGENCIA)) {
		return { reconstructible: false, reason: "rule_version_failed" };
	}
	return { reconstructible: true, reason: "" };
}

// ──────────────────────────────────────────────
// Integer ratio and percentage (FR-9 iii) — never floating point
// ──────────────────────────────────────────────

/** The frozen ratio shape {numerator, denominator}. */
export interface ReconstructibilityRatio {
	numerator: number;
	denominator: number;
}

/**
 * Integer division (numerator*100)/denominator — never floating point — with an
 * explicit multiplication-overflow guard. Null when the denominator is zero;
 * also null (fail closed, never a wrapped value) when the true percentage is
 * not representable as an integer-safe number (unreachable for bounded slice
 * counts, but the computation must never silently wrap).
 */
export function reconstructibilityPercentage(numerator: number, denominator: number): number | null {
	if (denominator <= 0) {
		return null;
	}
	if (numerator <= 0) {
		return 0;
	}
	// Decompose (numerator*100)/denominator = q*100 + (r*100)/denominator so the
	// multiplication never overflows while the true percentage stays integer-safe.
	const q = Math.floor(numerator / denominator);
	const r = numerator % denominator;
	if (q > Math.floor(Number.MAX_SAFE_INTEGER / 100)) {
		return null;
	}
	return q * 100 + Math.floor((r * 100) / denominator);
}

/** The frozen count shape of the metric (FR-9 iii/iv). */
export interface ReconstructibilityCounts {
	denominator: number;
	numerator: number;
	ratio: ReconstructibilityRatio;
	percentage: number | null;
	zeroDenominator: boolean;
}

/**
 * The frozen zero-denominator representation: {0,0}, percentage null,
 * zeroDenominator true — NEVER a misleading 0% or 100% (FR-9 iv).
 */
export function buildReconstructibilityCounts(denominator: number, numerator: number): ReconstructibilityCounts {
	if (denominator <= 0) {
		return {
			denominator: 0,
			numerator: 0,
			ratio: { numerator: 0, denominator: 0 },
			percentage: null,
			zeroDenominator: true,
		};
	}
	return {
		denominator,
		numerator,
		ratio: { numerator, denominator },
		percentage: reconstructibilityPercentage(numerator, denominator),
		zeroDenominator: false,
	};
}

// ──────────────────────────────────────────────
// Pure aggregation
// ──────────────────────────────────────────────

/**
 * The pure outcome of aggregating the metric over chain heads: the denominator
 * (FZ-1 eligible heads), the numerator (reconstructible heads) and the
 * per-reason decision-ID lists of the non-reconstructible members.
 */
export interface ReconstructibilityAggregate {
	denominator: number;
	numerator: number;
	reasons: Partial<Record<ReconstructibilityReason, string[]>>;
}

/**
 * Applies FZ-1 to every chain head, classifies each eligible head with FZ-2 and
 * counts the frozen metric. Deterministic (FR-9 vi): heads are processed in
 * bytewise decision-ID order and every reason list is sorted bytewise, so the
 * aggregate is independent of reader ordering. An eligible head MISSING from
 * reports fails closed as receipt_failed — the pure counterpart of the service
 * layer's ErrNoReceipts mapping (design D-3). Pure.
 */
export function aggregateReconstructibility(
	heads: AccountingMemory[],
	reports: Record<string, VerificationReport>,
	scope: MemoryScope,
): ReconstructibilityAggregate {
	const ordered = [...heads].sort((a, b) => (a.identity.id < b.identity.id ? -1 : a.identity.id > b.identity.id ? 1 : 0));
	const agg: ReconstructibilityAggregate = { denominator: 0, numerator: 0, reasons: {} };
	for (const head of ordered) {
		if (!isMaterialDecision(head, scope, true)) {
			continue;
		}
		agg.denominator++;
		const report = reports[head.identity.id];
		if (report === undefined) {
			(agg.reasons.receipt_failed ??= []).push(head.identity.id);
			continue;
		}
		const { reconstructible, reason } = classifyReconstructibility(head, report);
		if (reconstructible) {
			agg.numerator++;
			continue;
		}
		// A non-reconstructible decision always carries exactly one closed reason;
		// the narrow guard keeps the index type-safe (no `any`, IR-5).
		if (reason !== "") {
			(agg.reasons[reason] ??= []).push(head.identity.id);
		}
	}
	for (const reason of Object.keys(agg.reasons) as ReconstructibilityReason[]) {
		agg.reasons[reason]!.sort();
	}
	return agg;
}
