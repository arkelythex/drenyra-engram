import { createHash } from "node:crypto";
import { isValidFiscalRuc } from "./ruc.js";
import type { ReviewAcknowledgement } from "./types.js";

export const FISCAL_SCOPE_VERSION = { V1: "v1" } as const;
export type FiscalScopeVersion =
	(typeof FISCAL_SCOPE_VERSION)[keyof typeof FISCAL_SCOPE_VERSION];
export const AUTHORITY_LEVEL = {
	ASK: "ASK",
	ANALYZE: "ANALYZE",
	PREPARE: "PREPARE",
	EXECUTE: "EXECUTE",
} as const;
export type AuthorityLevel =
	(typeof AUTHORITY_LEVEL)[keyof typeof AUTHORITY_LEVEL];
export const FISCAL_SCOPE_ERROR = {
	REQUIRED: "SCOPE_BINDING_REQUIRED",
	INVALID: "SCOPE_BINDING_INVALID",
	MISMATCH: "SCOPE_MISMATCH",
	UNSUPPORTED_VERSION: "UNSUPPORTED_SCOPE_VERSION",
} as const;
export type FiscalScopeErrorCode =
	(typeof FISCAL_SCOPE_ERROR)[keyof typeof FISCAL_SCOPE_ERROR];

export class FiscalScopeError extends Error {
	constructor(
		readonly code: FiscalScopeErrorCode,
		message: string,
	) {
		super(`${code}: ${message}`);
	}
}

export interface FiscalScopeBinding {
	version: FiscalScopeVersion;
	tenant: string;
	organization: string;
	company: string;
	fiscalPeriod: string;
	ledgerBook: string;
	operationType: string;
	sourceSnapshot: string;
	policyVersion: string;
	actor: string;
	authorityLevel: AuthorityLevel;
}

const KEYS = [
	"version",
	"tenant",
	"organization",
	"company",
	"fiscalPeriod",
	"ledgerBook",
	"operationType",
	"sourceSnapshot",
	"policyVersion",
	"actor",
	"authorityLevel",
] as const;
const OPERATIONS: Readonly<Record<string, AuthorityLevel>> = {
	"memory.save": AUTHORITY_LEVEL.PREPARE,
	"memory.supersede": AUTHORITY_LEVEL.PREPARE,
	"evidence.store": AUTHORITY_LEVEL.PREPARE,
	"evidence.link": AUTHORITY_LEVEL.PREPARE,
	"rule.link": AUTHORITY_LEVEL.PREPARE,
	"close.create": AUTHORITY_LEVEL.PREPARE,
	"context.read": AUTHORITY_LEVEL.ASK,
	"review.queue": AUTHORITY_LEVEL.ASK,
	"review.detail": AUTHORITY_LEVEL.ASK,
	"timeline.read": AUTHORITY_LEVEL.ASK,
	"evidence.get": AUTHORITY_LEVEL.ASK,
	"reconstruct.read": AUTHORITY_LEVEL.ANALYZE,
	"verify.read": AUTHORITY_LEVEL.ANALYZE,
	"memory.approve": AUTHORITY_LEVEL.EXECUTE,
	"close.approve": AUTHORITY_LEVEL.EXECUTE,
	"period.reopen": AUTHORITY_LEVEL.EXECUTE,
	"period.write-check": AUTHORITY_LEVEL.EXECUTE,
};

export function decodeFiscalScopeV1(input: unknown): FiscalScopeBinding {
	if (typeof input !== "object" || input === null || Array.isArray(input))
		fail(FISCAL_SCOPE_ERROR.INVALID, "binding must be one object");
	const record = input as Record<string, unknown>;
	if (!("version" in record))
		fail(FISCAL_SCOPE_ERROR.REQUIRED, "binding version is required");
	if (record.version !== FISCAL_SCOPE_VERSION.V1)
		fail(
			FISCAL_SCOPE_ERROR.UNSUPPORTED_VERSION,
			"unsupported fiscal scope version",
		);
	const keys = Object.keys(record);
	if (
		keys.length !== KEYS.length ||
		keys.some((key, index) => key !== KEYS[index])
	)
		fail(
			FISCAL_SCOPE_ERROR.INVALID,
			"keys must be unique and in canonical order",
		);
	for (const key of KEYS)
		if (typeof record[key] !== "string")
			fail(FISCAL_SCOPE_ERROR.INVALID, `${key} must be a string`);
	// SAFETY: exact keys, string values, and version were checked above; closed vocabularies are checked below.
	const binding = record as unknown as FiscalScopeBinding;
	for (const key of KEYS.slice(1)) {
		const value = binding[key];
		if (
			!value.trim() ||
			value.includes("\0") ||
			!isValidUnicodeScalarString(value)
		)
			fail(FISCAL_SCOPE_ERROR.INVALID, `${key} is invalid`);
	}
	if (!isValidFiscalRuc(binding.company))
		fail(FISCAL_SCOPE_ERROR.INVALID, "company is not a valid RUC");
	if (!/^\d{4}(0[1-9]|1[0-2])$/.test(binding.fiscalPeriod))
		fail(FISCAL_SCOPE_ERROR.INVALID, "fiscalPeriod must be YYYYMM");
	if (!/^[0-9a-f]{64}$/.test(binding.sourceSnapshot))
		fail(FISCAL_SCOPE_ERROR.INVALID, "sourceSnapshot must be lowercase sha256");
	if (OPERATIONS[binding.operationType] !== binding.authorityLevel)
		fail(
			FISCAL_SCOPE_ERROR.INVALID,
			"operationType and authorityLevel do not match the v1 vocabulary",
		);
	return { ...binding };
}

export function parseFiscalScopeV1JSON(text: string): FiscalScopeBinding {
	let cursor = skip(text, 0);
	if (text[cursor++] !== "{")
		fail(FISCAL_SCOPE_ERROR.INVALID, "binding must be one JSON object");
	const values: Record<string, string> = {};
	for (const [index, expected] of KEYS.entries()) {
		cursor = skip(text, cursor);
		if (text[cursor] === "}")
			fail(FISCAL_SCOPE_ERROR.REQUIRED, "complete v1 binding is required");
		const key = readString(text, cursor);
		cursor = skip(text, key.next);
		if (key.value !== expected)
			fail(
				FISCAL_SCOPE_ERROR.INVALID,
				"keys must be unique and in canonical order",
			);
		if (text[cursor++] !== ":")
			fail(FISCAL_SCOPE_ERROR.INVALID, "invalid binding JSON");
		const value = readString(text, skip(text, cursor));
		cursor = skip(text, value.next);
		values[key.value] = value.value;
		if (index === 0 && value.value !== FISCAL_SCOPE_VERSION.V1)
			fail(
				FISCAL_SCOPE_ERROR.UNSUPPORTED_VERSION,
				"unsupported fiscal scope version",
			);
		const last = index === KEYS.length - 1;
		if (text[cursor++] !== (last ? "}" : ","))
			fail(
				last ? FISCAL_SCOPE_ERROR.INVALID : FISCAL_SCOPE_ERROR.REQUIRED,
				"complete canonical binding is required",
			);
	}
	if (skip(text, cursor) !== text.length)
		fail(FISCAL_SCOPE_ERROR.INVALID, "trailing JSON value");
	return decodeFiscalScopeV1(values);
}

export function canonicalFiscalScopeBytes(
	binding: FiscalScopeBinding,
): Uint8Array {
	const checked = decodeFiscalScopeV1(binding);
	let canonical = "drenyra:fiscal-scope:v1\0";
	for (const key of KEYS.slice(1)) {
		const value = checked[key];
		canonical += `${key}=${new TextEncoder().encode(value).length}:${value}\0`;
	}
	return new TextEncoder().encode(canonical);
}

export function fiscalScopeHash(binding: FiscalScopeBinding): string {
	return createHash("sha256")
		.update(canonicalFiscalScopeBytes(binding))
		.digest("hex");
}

export const REVIEW_ACKNOWLEDGEMENT_STATE = {
	OMITTED: "omitted",
	FALSE: "false",
	TRUE: "true",
} as const;
export type ReviewAcknowledgementState =
	(typeof REVIEW_ACKNOWLEDGEMENT_STATE)[keyof typeof REVIEW_ACKNOWLEDGEMENT_STATE];
export function reviewAcknowledgementState(
	value: ReviewAcknowledgement,
): ReviewAcknowledgementState {
	return value.present
		? value.value
			? REVIEW_ACKNOWLEDGEMENT_STATE.TRUE
			: REVIEW_ACKNOWLEDGEMENT_STATE.FALSE
		: REVIEW_ACKNOWLEDGEMENT_STATE.OMITTED;
}

function readString(
	text: string,
	start: number,
): { value: string; next: number } {
	if (text[start] !== '"')
		fail(FISCAL_SCOPE_ERROR.INVALID, "binding keys and values must be strings");
	for (let cursor = start + 1; cursor < text.length; cursor++) {
		if (text[cursor] === "\\") {
			cursor++;
			continue;
		}
		if (text[cursor] === '"') {
			try {
				return {
					value: JSON.parse(text.slice(start, cursor + 1)) as string,
					next: cursor + 1,
				};
			} catch {
				fail(FISCAL_SCOPE_ERROR.INVALID, "invalid JSON string");
			}
		}
	}
	return fail(FISCAL_SCOPE_ERROR.INVALID, "unterminated JSON string");
}
function skip(text: string, start: number): number {
	while (/\s/.test(text[start] ?? "")) start++;
	return start;
}
function isValidUnicodeScalarString(value: string): boolean {
	try {
		encodeURIComponent(value);
		return true;
	} catch {
		return false;
	}
}
function fail(code: FiscalScopeErrorCode, message: string): never {
	throw new FiscalScopeError(code, message);
}
