/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Receipt protocol mirror tests (v0.4.0 Step 3) — the exact TS counterpart of
 * `internal/core/receipt_test.go`: the closed model, the canonical byte
 * contract (deterministic, fixed-order, no HTML escaping, roles canonicalized),
 * the digest/key-id derivation, RFC 8032 signing and the fail-closed
 * verification.
 *
 * CROSS-RUNTIME PARITY (AC9/AC10): the fixed seed (32×0x01) and the pinned hex
 * constants below are SHARED with the Go test. Both runtimes derive the same
 * RFC 8032 keypair and deterministic signature from the seed — Go signs and TS
 * asserts the SAME public key, payload hash, signature, envelope bytes and
 * receipt hash (and vice versa). Node's `generateKeyPairSync("ed25519",
 * { seed })` is NOT RFC 8032 compliant, so the mirror imports the seed via JWK
 * `d` (see core/receipt.ts) — that path is byte-identical with Go. The full
 * golden vector file lands in batch 3; these constants must agree NOW.
 */

import { describe, expect, it } from "vitest";

import {
	canonicalReceiptPayload,
	canonicalUnsignedEnvelope,
	completeReceiptBytes,
	receiptHash,
	receiptKeyId,
	receiptPayloadHash,
	ReceiptError,
	signReceipt,
	verifyReceipt,
} from "../receipt.js";
import {
	RECEIPT_ACTIONS,
	RECEIPT_ALGORITHM,
	RECEIPT_SUBJECT_TYPES,
	type ReceiptAction,
	type ReceiptPayload,
	type SignedReceipt,
} from "../types.js";

/** Fixed parity seed (AC9/AC10): 32 bytes of 0x01 (documented in the Go test). */
const PARITY_SEED_HEX =
	"0101010101010101010101010101010101010101010101010101010101010101";

/** Pinned constants — the SAME values internal/core/receipt_test.go asserts. */
const PARITY_PUBLIC_KEY_HEX =
	"8a88e3dd7409f195fd52db2d3cba5d72ca6709bf1d94121bf3748801b40f6f5c";
const PARITY_KEY_ID =
	"ed25519:34750f98bd59fcfc946da45aaabe933be154a4b5094e1c4abf42866505f3c97e";
const PARITY_PAYLOAD_HASH =
	"145911e45560f4cda235ca4641b881fa10ec00c52413f0abcfdd7a06c4d41fc6";
const PARITY_SIGNATURE_HEX =
	"662c77c6de213aa692160ecb08afaabd4e4c105a43ce80df30084b635e86b5e3c0066a4e4943c53efecf6c8dd4d1a0b20b31f08507d73c9e8f6979f49181120d";
const PARITY_RECEIPT_HASH =
	"9a26e2c5a570619b12bbcb9499e89c190e811c998297ea14034b0ba5c3e1826b";
/** Hex of the canonical unsigned-envelope JSON bytes and of the complete
 * signed receipt bytes (signature between keyId and issuedAt). */
const PARITY_UNSIGNED_ENVELOPE_HEX =
	"7b227375626a65637454797065223a226d656d6f7279222c227375626a6563744964223a226d656d6f72792d31222c22616374696f6e223a226d656d6f72795f617070726f766564222c2274656e616e744964223a2274656e616e742d31222c22636f6d70616e794964223a2261636d65222c2266697363616c506572696f644964223a22323032363031222c227061796c6f616448617368223a2231343539313165343535363066346364613233356361343634316238383166613130656330306335323431336630616263666464376130366334643431666336222c2270726576696f75735265636569707448617368223a22222c227072696e636970616c4964223a227375626a6563742d31222c226d656d626572736869704964223a226d656d626572736869702d31222c22706f6c69637956657273696f6e223a22617070726f76616c2d706f6c6963792f76302e342e30222c22616c676f726974686d223a2245643235353139222c226b65794964223a22656432353531393a33343735306639386264353966636663393436646134356161616265393333626531353461346235303934653163346162663432383636353035663363393765222c226973737565644174223a22323032362d30382d30355431333a30303a30305a227d";
const PARITY_COMPLETE_BYTES_HEX =
	"7b227375626a65637454797065223a226d656d6f7279222c227375626a6563744964223a226d656d6f72792d31222c22616374696f6e223a226d656d6f72795f617070726f766564222c2274656e616e744964223a2274656e616e742d31222c22636f6d70616e794964223a2261636d65222c2266697363616c506572696f644964223a22323032363031222c227061796c6f616448617368223a2231343539313165343535363066346364613233356361343634316238383166613130656330306335323431336630616263666464376130366334643431666336222c2270726576696f75735265636569707448617368223a22222c227072696e636970616c4964223a227375626a6563742d31222c226d656d626572736869704964223a226d656d626572736869702d31222c22706f6c69637956657273696f6e223a22617070726f76616c2d706f6c6963792f76302e342e30222c22616c676f726974686d223a2245643235353139222c226b65794964223a22656432353531393a33343735306639386264353966636663393436646134356161616265393333626531353461346235303934653163346162663432383636353035663363393765222c227369676e6174757265223a225a697833787434684f7161534667374c434b2b717655354d454670447a6f44664d41684c5931364774655041426d704f53555046507637506249335530614379437a487768516658504a365061586e306b59455344513d3d222c226973737565644174223a22323032362d30382d30355431333a30303a30305a227d";

/**
 * The canonical fixture of the parity constants: a memory_approved payload
 * exercising the complete principal snapshot, empty inapplicable fields, a `&`
 * (no HTML escaping) and a `"` (JSON escaping) in the reason, and UNSORTED +
 * DUPLICATED roles (canonicalized by the payload hash contract). The identical
 * fixture lives in the Go test (baseReceiptPayload).
 */
function baseReceiptPayload(
	overrides?: Partial<ReceiptPayload>,
): ReceiptPayload {
	return {
		version: "receipt-payload/v0.4.0",
		subjectType: "memory",
		subjectId: "memory-1",
		action: "memory_approved",
		tenantId: "tenant-1",
		companyId: "acme",
		fiscalPeriodId: "202601",
		reviewedEnvelopeHash: "h1-reviewed-envelope",
		resultingEnvelopeHash: "h2-resulting-envelope",
		reviewedJudgmentHash: "",
		resultingJudgmentHash: "",
		fromMemoryId: "",
		fromEnvelopeHash: "",
		toMemoryId: "",
		toEnvelopeHash: "",
		successorId: "",
		evidenceRef: "",
		reason: 'approved & verified "controller" review',
		principalId: "subject-1",
		membershipId: "membership-1",
		principalRoles: ["controller", "accountant", "controller"],
		authenticationMethod: "session",
		assuranceLevel: "standard",
		principalAuthenticatedAt: "2026-08-05T13:00:00Z",
		policyVersion: "approval-policy/v0.4.0",
		issuedAt: "2026-08-05T13:00:00Z",
		...overrides,
	};
}

function paritySeed(): Uint8Array {
	return Buffer.from(PARITY_SEED_HEX, "hex");
}

/** Signs the parity fixture with the fixed seed (genesis chain). */
function signParity(): {
	receipt: SignedReceipt;
	publicKey: Uint8Array;
	signatureHex: string;
} {
	const { receipt, publicKey } = signReceipt(baseReceiptPayload(), paritySeed());
	return {
		receipt,
		publicKey,
		signatureHex: Buffer.from(receipt.signature, "base64").toString("hex"),
	};
}

/** Returns the frozen ReceiptError code thrown by fn, or undefined. */
function codeOf(fn: () => void): string | undefined {
	try {
		fn();
		return undefined;
	} catch (error) {
		return error instanceof ReceiptError ? error.code : undefined;
	}
}

const EIGHT_ACTIONS: readonly ReceiptAction[] = [
	"memory_recorded",
	"memory_approved",
	"memory_rejected",
	"memory_voided",
	"relation_confirmed",
	"relation_rejected",
	"evidence_linked",
	"memory_superseded",
];

describe("receipt protocol mirror (v0.4.0 Step 3)", () => {
	it("exposes the closed subject and action sets", () => {
		expect(RECEIPT_SUBJECT_TYPES).toEqual(["memory", "judgment"]);
		expect(RECEIPT_ACTIONS).toEqual([...EIGHT_ACTIONS]);
		expect(RECEIPT_ACTIONS.length).toBe(8);
		expect(RECEIPT_ALGORITHM).toBe("Ed25519");
	});

	it("pins the canonical payload hash shared with Go", () => {
		expect(receiptPayloadHash(baseReceiptPayload())).toBe(PARITY_PAYLOAD_HASH);
	});

	it("is deterministic for the same input", () => {
		const a = receiptPayloadHash(baseReceiptPayload());
		const b = receiptPayloadHash(baseReceiptPayload());
		expect(a).toBe(b);
	});

	it("keeps & raw, escapes quotes, and canonicalizes roles", () => {
		const canonical = canonicalReceiptPayload(baseReceiptPayload());
		expect(canonical).toContain(
			'"reason":"approved & verified \\"controller\\" review"',
		);
		expect(canonical).toContain(
			'"principalRoles":["accountant","controller"]',
		);
	});

	it("keeps every key present when empty and never marshals null roles", () => {
		const canonical = canonicalReceiptPayload(baseReceiptPayload());
		for (const key of [
			'"version":"receipt-payload/v0.4.0"',
			'"reviewedJudgmentHash":""',
			'"resultingJudgmentHash":""',
			'"fromMemoryId":""',
			'"fromEnvelopeHash":""',
			'"toMemoryId":""',
			'"toEnvelopeHash":""',
			'"successorId":""',
			'"evidenceRef":""',
		]) {
			expect(canonical).toContain(key);
		}
		const emptyRoles = canonicalReceiptPayload(
			baseReceiptPayload({ principalRoles: ["", "", ""] }),
		);
		expect(emptyRoles).toContain('"principalRoles":[]');
	});

	it("pins the unsigned envelope and complete receipt bytes shared with Go", () => {
		const { receipt } = signParity();
		expect(Buffer.from(canonicalUnsignedEnvelope(receipt), "utf8").toString("hex")).toBe(
			PARITY_UNSIGNED_ENVELOPE_HEX,
		);
		expect(canonicalUnsignedEnvelope(receipt)).not.toContain("signature");
		expect(Buffer.from(completeReceiptBytes(receipt), "utf8").toString("hex")).toBe(
			PARITY_COMPLETE_BYTES_HEX,
		);
		// signature sits between keyId and issuedAt in the complete bytes
		const complete = completeReceiptBytes(receipt);
		expect(complete.indexOf('"keyId"')).toBeLessThan(complete.indexOf('"signature"'));
		expect(complete.indexOf('"signature"')).toBeLessThan(complete.indexOf('"issuedAt"'));
	});

	it("pins the receipt hash and the key id derivation shared with Go", () => {
		const { receipt, publicKey } = signParity();
		expect(receiptHash(receipt)).toBe(PARITY_RECEIPT_HASH);
		expect(receiptKeyId(publicKey)).toBe(PARITY_KEY_ID);
		expect(receiptKeyId(publicKey)).toMatch(/^ed25519:[0-9a-f]{64}$/);
	});

	it("AC10: signs the fixed seed payload and pins the signature Go produced", () => {
		const { publicKey, signatureHex } = signParity();
		expect(Buffer.from(publicKey).toString("hex")).toBe(PARITY_PUBLIC_KEY_HEX);
		expect(signatureHex).toBe(PARITY_SIGNATURE_HEX);
	});

	it("sign then verify round trip", () => {
		const { receipt, publicKey } = signParity();
		expect(() => verifyReceipt(receipt, baseReceiptPayload(), publicKey)).not.toThrow();
	});

	it("rejects a modified payload with RECEIPT_PAYLOAD_HASH_MISMATCH", () => {
		const { receipt, publicKey } = signParity();
		const tampered = baseReceiptPayload({ reason: "a different reason" });
		expect(codeOf(() => verifyReceipt(receipt, tampered, publicKey))).toBe(
			"RECEIPT_PAYLOAD_HASH_MISMATCH",
		);
	});

	it("rejects a modified signature with RECEIPT_SIGNATURE_INVALID", () => {
		const { receipt, publicKey } = signParity();
		const mutated: SignedReceipt = {
			...receipt,
			signature: flipFirstByte(receipt.signature),
		};
		expect(codeOf(() => verifyReceipt(mutated, baseReceiptPayload(), publicKey))).toBe(
			"RECEIPT_SIGNATURE_INVALID",
		);
	});

	it("rejects modified envelope fields", () => {
		const { receipt, publicKey } = signParity();
		const payload = baseReceiptPayload();

		// issuedAt differs from the payload → equality check fails closed.
		expect(
			codeOf(() =>
				verifyReceipt({ ...receipt, issuedAt: "2026-08-05T14:00:00Z" }, payload, publicKey),
			),
		).toBe("RECEIPT_INVALID");

		// previousReceiptHash is signed → signature fails.
		expect(
			codeOf(() =>
				verifyReceipt({ ...receipt, previousReceiptHash: "deadbeef" }, payload, publicKey),
			),
		).toBe("RECEIPT_SIGNATURE_INVALID");

		// the payloadHash field itself → digest check fails.
		expect(
			codeOf(() =>
				verifyReceipt({ ...receipt, payloadHash: "deadbeef" }, payload, publicKey),
			),
		).toBe("RECEIPT_PAYLOAD_HASH_MISMATCH");
	});

	it("rejects a wrong public key with RECEIPT_KEY_MISMATCH", () => {
		const { receipt } = signParity();
		const wrongSeed = Buffer.alloc(32, 0x02);
		const { publicKey: wrongPublicKey } = signReceipt(
			baseReceiptPayload(),
			wrongSeed,
		);
		expect(codeOf(() => verifyReceipt(receipt, baseReceiptPayload(), wrongPublicKey))).toBe(
			"RECEIPT_KEY_MISMATCH",
		);
	});

	it("fails closed on unknown enums with RECEIPT_INVALID", () => {
		const { receipt, publicKey } = signParity();
		const payload = baseReceiptPayload();

		expect(
			codeOf(() =>
				verifyReceipt(
					{ ...receipt, action: "memory_deleted" as ReceiptAction },
					payload,
					publicKey,
				),
			),
		).toBe("RECEIPT_INVALID");
		expect(
			codeOf(() =>
				verifyReceipt(
					{ ...receipt, subjectType: "envelope" as ReceiptPayload["subjectType"] },
					payload,
					publicKey,
				),
			),
		).toBe("RECEIPT_INVALID");
		expect(
			codeOf(() =>
				verifyReceipt({ ...receipt, algorithm: "RSA" }, payload, publicKey),
			),
		).toBe("RECEIPT_INVALID");
	});

	it("rejects malformed signatures", () => {
		const { receipt, publicKey } = signParity();
		const payload = baseReceiptPayload();

		expect(
			codeOf(() =>
				verifyReceipt({ ...receipt, signature: "!!!not-base64!!!" }, payload, publicKey),
			),
		).toBe("RECEIPT_INVALID");
		expect(
			codeOf(() =>
				verifyReceipt(
					{ ...receipt, signature: Buffer.from("too short").toString("base64") },
					payload,
					publicKey,
				),
			),
		).toBe("RECEIPT_SIGNATURE_INVALID");
	});

	it("rejects a seed of the wrong length", () => {
		expect(() => signReceipt(baseReceiptPayload(), Buffer.alloc(31))).toThrow(
			ReceiptError,
		);
	});
});

/** Decodes a padded-base64 signature, flips one byte and re-encodes. */
function flipFirstByte(signature: string): string {
	const raw = Buffer.from(signature, "base64");
	raw[0] ^= 0x01;
	return raw.toString("base64");
}
