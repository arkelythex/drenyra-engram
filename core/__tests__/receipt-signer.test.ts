/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * ReceiptSigner mirror tests (v0.4.0 Step 3 keys commit) — the TS counterpart
 * of `internal/receipts/signer_test.go`: the store-facing signing surface. The
 * default NodeSeedSigner derives an RFC 8032 keypair from a 32-byte seed (JWK
 * `d` import, byte-identical with Go), produces a SignedReceipt that
 * verifyReceipt accepts, chains on the previous receipt hash, and fails closed
 * when the key is revoked. The in-memory store receives the signer at
 * construction (the Go CLI owns the 0600 keyring file — the mirror holds no
 * file keyring).
 */

import { describe, expect, it } from "vitest";

import {
	NodeSeedSigner,
	receiptKeyId,
	verifyReceipt,
	ReceiptError,
	type ReceiptSigner,
} from "../receipt.js";
import type { ReceiptPayload } from "../types.js";
import { InMemoryMemoryStore } from "../../store/memory-store.js";

/** The deterministic parity seed (32×0x01), shared with the Go tests. */
const PARITY_SEED = Buffer.from(
	"0101010101010101010101010101010101010101010101010101010101010101",
	"hex",
);

/** A minimal legal memory_recorded payload (kernel policy, empty inapplicable
 * fields). */
function signedPayload(): ReceiptPayload {
	return {
		version: "receipt-payload/v0.4.0",
		subjectType: "memory",
		subjectId: "memory-1",
		action: "memory_recorded",
		tenantId: "tenant-1",
		companyId: "acme",
		fiscalPeriodId: "202601",
		reviewedEnvelopeHash: "",
		resultingEnvelopeHash: "h2-resulting-envelope",
		reviewedJudgmentHash: "",
		resultingJudgmentHash: "",
		fromMemoryId: "",
		fromEnvelopeHash: "",
		toMemoryId: "",
		toEnvelopeHash: "",
		successorId: "",
		evidenceRef: "",
		reason: "",
		principalId: "cli",
		membershipId: "membership-1",
		principalRoles: [],
		authenticationMethod: "",
		assuranceLevel: "",
		principalAuthenticatedAt: "",
		policyVersion: "kernel/v0.4.0",
		issuedAt: "2026-08-05T13:00:00Z",
	};
}

describe("NodeSeedSigner (v0.4.0 Step 3 keys commit)", () => {
	it("produces a SignedReceipt that verifyReceipt accepts", () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const payload = signedPayload();

		const { receipt, publicKey } = signer.sign(payload);

		expect(signer.keyId).toBe(receiptKeyId(signer.publicKey));
		expect(receipt.keyId).toBe(signer.keyId);
		expect(receipt.previousReceiptHash).toBe("");
		expect(receipt.issuedAt).toBe(payload.issuedAt);
		expect(() => verifyReceipt(receipt, payload, publicKey)).not.toThrow();
		expect(publicKey).toEqual(signer.publicKey);
	});

	it("chains the receipt on the previous receipt hash", () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const { receipt } = signer.sign(signedPayload(), "prior-receipt-hash");
		expect(receipt.previousReceiptHash).toBe("prior-receipt-hash");
	});

	it("rejects a seed of the wrong length", () => {
		expect(() => new NodeSeedSigner(Buffer.alloc(31))).toThrow(ReceiptError);
	});

	it("fails closed when the key is revoked", () => {
		const signer = new NodeSeedSigner(PARITY_SEED, { revoked: true });
		expect(() => signer.sign(signedPayload())).toThrow(/revoked/);
		// Revocation is one-way: a revoked signer never produces a receipt.
		expect(signer.revoked).toBe(true);
	});

	it("receives a caller-provided signer at store construction", () => {
		const signer = new NodeSeedSigner(PARITY_SEED);
		const store = new InMemoryMemoryStore(signer);
		expect(store.receiptSigner).toBe(signer);

		const withoutSigner = new InMemoryMemoryStore();
		expect(withoutSigner.receiptSigner).toBeUndefined();
	});

	it("implements the ReceiptSigner contract shape", () => {
		const signer: ReceiptSigner = new NodeSeedSigner(PARITY_SEED);
		expect(typeof signer.sign).toBe("function");
		expect(signer.publicKey).toHaveLength(32);
		expect(signer.keyId.startsWith("ed25519:")).toBe(true);
	});
});
