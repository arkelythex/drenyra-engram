import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
	AUTHORITY_LEVEL,
	FISCAL_SCOPE_ERROR,
	FiscalScopeError,
	canonicalFiscalScopeBytes,
	decodeFiscalScopeV1,
	fiscalScopeHash,
	parseFiscalScopeV1JSON,
	reviewAcknowledgementState,
	type FiscalScopeBinding,
} from "../fiscal-scope.js";
import { RUC_CLASSIFICATION, classifyRuc } from "../ruc.js";

interface ScopeVector {
	binding: FiscalScopeBinding;
	canonicalHex: string;
	hash: string;
}
interface RucCase {
	input: string;
	classification: string;
}
interface RucVector {
	cases: RucCase[];
}
const golden = (name: string): unknown =>
	JSON.parse(readFileSync(resolve("testdata/golden", name), "utf8"));

describe("canonical fiscal RUC", () => {
	it("matches shared shape and checksum classifications", () => {
		const vector = golden("fiscal-ruc-v1.json") as RucVector;
		for (const c of vector.cases)
			expect(classifyRuc(c.input), c.input).toBe(c.classification);
		expect(classifyRuc("20100070970")).toBe(RUC_CLASSIFICATION.VALID);
	});
});

describe("fiscal scope v1", () => {
	it("matches shared canonical bytes and hash", () => {
		const vector = golden("fiscal-scope-v1.json") as ScopeVector;
		const binding = decodeFiscalScopeV1(vector.binding);
		expect(Buffer.from(canonicalFiscalScopeBytes(binding)).toString("hex")).toBe(
			vector.canonicalHex,
		);
		expect(fiscalScopeHash(binding)).toBe(vector.hash);
	});

	it("accepts well-formed Unicode and frames its UTF-8 byte length", () => {
		const { binding } = golden("fiscal-scope-v1.json") as ScopeVector;
		const unicodeBinding = decodeFiscalScopeV1({
			...binding,
			actor: "agente:🧾",
		});
		expect(
			new TextDecoder().decode(canonicalFiscalScopeBytes(unicodeBinding)),
		).toContain("actor=11:agente:🧾\0");
	});

	it("hash-binds every element independently", () => {
		const vector = golden("fiscal-scope-v1.json") as ScopeVector;
		const mutations: Partial<Record<keyof FiscalScopeBinding, string>> = {
			tenant: "tenant-other",
			organization: "org-002",
			company: "20600055519",
			fiscalPeriod: "202602",
			ledgerBook: "sales",
			operationType: "memory.supersede",
			sourceSnapshot: `a${vector.binding.sourceSnapshot.slice(1)}`,
			policyVersion: "fiscal-policy/v2",
			actor: "agent:other",
		};
		for (const [key, value] of Object.entries(mutations)) {
			const changed = { ...vector.binding, [key]: value } as FiscalScopeBinding;
			expect(fiscalScopeHash(changed), key).not.toBe(vector.hash);
		}
		expect(() =>
			fiscalScopeHash({ ...vector.binding, authorityLevel: AUTHORITY_LEVEL.ASK }),
		).toThrowError(FiscalScopeError);
	});

	it.each([
		[
			"duplicate",
			`{"version":"v1","tenant":"t","tenant":"x"}`,
			FISCAL_SCOPE_ERROR.INVALID,
		],
		["reordered", `{"tenant":"t","version":"v1"}`, FISCAL_SCOPE_ERROR.INVALID],
		["missing", `{"version":"v1"}`, FISCAL_SCOPE_ERROR.REQUIRED],
		[
			"unknown version",
			`{"version":"v2"}`,
			FISCAL_SCOPE_ERROR.UNSUPPORTED_VERSION,
		],
	])("rejects %s JSON", (_name, raw, code) => {
		expect(() => parseFiscalScopeV1JSON(raw)).toThrowError(
			expect.objectContaining({ code }),
		);
	});

	it("rejects malformed values and operation vocabulary", () => {
		const { binding } = golden("fiscal-scope-v1.json") as ScopeVector;
		for (const changed of [
			{ ...binding, fiscalPeriod: "202613" },
			{ ...binding, sourceSnapshot: "A".repeat(64) },
			{ ...binding, authorityLevel: AUTHORITY_LEVEL.EXECUTE },
			{ ...binding, actor: "bad\0actor" },
			{ ...binding, actor: "\ud800" },
		])
			expect(() => decodeFiscalScopeV1(changed)).toThrowError(FiscalScopeError);
	});

	it("keeps review acknowledgement omission distinct from false and true", () => {
		expect(reviewAcknowledgementState({ present: false, value: false })).toBe(
			"omitted",
		);
		expect(reviewAcknowledgementState({ present: true, value: false })).toBe(
			"false",
		);
		expect(reviewAcknowledgementState({ present: true, value: true })).toBe(
			"true",
		);
	});
});
