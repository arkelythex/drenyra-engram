/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Ed25519 action receipt protocol mirror (v0.4.0 Step 3) — the exact TypeScript
 * counterpart of `internal/core/receipt.go`: canonicalization (byte-identical
 * compact UTF-8 JSON, fixed property order, JSON string escaping, NO HTML
 * escaping), lowercase SHA-256 hex digests, key-id derivation, RFC 8032 signing
 * (node:crypto) and the minimal fail-closed verification. Accounting
 * correctness: NOT ASSERTED.
 *
 * KEY DERIVATION NOTE (cross-runtime parity): Node's
 * `generateKeyPairSync("ed25519", { seed })` is NOT RFC 8032 compliant — it
 * derives a different public key than Go's `ed25519.NewKeyFromSeed` for the
 * same seed. This module therefore imports the seed as a JWK `d` value, which
 * makes OpenSSL re-derive the RFC 8032 public key — byte-identical with Go.
 */
import {
	createHash,
	createPrivateKey,
	createPublicKey,
	sign as ed25519Sign,
	verify as ed25519Verify,
	type KeyObject,
} from "node:crypto";

import {
	RECEIPT_ACTIONS,
	RECEIPT_ALGORITHM,
	RECEIPT_SUBJECT_TYPES,
	type ReceiptPayload,
	type SignedReceipt,
} from "./types.js";

// ──────────────────────────────────────────────
// Typed verification errors (receipt protocol)
// ──────────────────────────────────────────────

/** Frozen receipt error codes (v0.4.0 Step 3). Mirrors core's receipt codes. */
export const RECEIPT_ERROR_CODES = [
	"RECEIPT_INVALID",
	"RECEIPT_PAYLOAD_HASH_MISMATCH",
	"RECEIPT_KEY_MISMATCH",
	"RECEIPT_SIGNATURE_INVALID",
] as const;

export type ReceiptErrorCode = (typeof RECEIPT_ERROR_CODES)[number];

/**
 * Typed receipt verification error: a frozen code plus a human message.
 * Mirrors core.ReceiptError. VerifyReceipt never claims accounting
 * correctness.
 */
export class ReceiptError extends Error {
	readonly code: ReceiptErrorCode;

	constructor(code: ReceiptErrorCode, message: string) {
		super(message);
		this.name = "ReceiptError";
		this.code = code;
	}
}

// ──────────────────────────────────────────────
// Canonicalization (the byte contract)
// ──────────────────────────────────────────────

/**
 * Canonical roles: sorted, deduplicated, empty strings dropped — the canonical
 * order the payload covers (Go and TS bytes match; the contract must not depend
 * on the caller's ordering). Mirrors core.canonicalRoles.
 */
function canonicalRoles(roles: string[]): string[] {
	return [...new Set(roles.filter((role) => role !== ""))].sort();
}

/**
 * Canonical compact JSON of a receipt payload: fixed property order (exactly
 * the ReceiptPayload field order), JSON string escaping, NO HTML escaping
 * (JSON.stringify never escapes `<`, `>`, `&` — matching Go's
 * SetEscapeHTML(false)), every key present (inapplicable fields stay ""), roles
 * canonicalized. Maps, nulls and optional properties are forbidden. The UTF-8
 * encoding of the returned string equals core.CanonicalReceiptPayload's bytes.
 */
export function canonicalReceiptPayload(payload: ReceiptPayload): string {
	return JSON.stringify({
		version: payload.version,
		subjectType: payload.subjectType,
		subjectId: payload.subjectId,
		action: payload.action,
		tenantId: payload.tenantId,
		companyId: payload.companyId,
		fiscalPeriodId: payload.fiscalPeriodId,
		reviewedEnvelopeHash: payload.reviewedEnvelopeHash,
		resultingEnvelopeHash: payload.resultingEnvelopeHash,
		reviewedJudgmentHash: payload.reviewedJudgmentHash,
		resultingJudgmentHash: payload.resultingJudgmentHash,
		fromMemoryId: payload.fromMemoryId,
		fromEnvelopeHash: payload.fromEnvelopeHash,
		toMemoryId: payload.toMemoryId,
		toEnvelopeHash: payload.toEnvelopeHash,
		successorId: payload.successorId,
		evidenceRef: payload.evidenceRef,
		reason: payload.reason,
		principalId: payload.principalId,
		membershipId: payload.membershipId,
		principalRoles: canonicalRoles(payload.principalRoles),
		authenticationMethod: payload.authenticationMethod,
		assuranceLevel: payload.assuranceLevel,
		principalAuthenticatedAt: payload.principalAuthenticatedAt,
		policyVersion: payload.policyVersion,
		issuedAt: payload.issuedAt,
	});
}

/** The unsigned envelope object in the design's exact canonical order. */
function unsignedEnvelopeObject(
	receipt: SignedReceipt,
): Record<string, string> {
	return {
		subjectType: receipt.subjectType,
		subjectId: receipt.subjectId,
		action: receipt.action,
		tenantId: receipt.tenantId,
		companyId: receipt.companyId,
		fiscalPeriodId: receipt.fiscalPeriodId,
		payloadHash: receipt.payloadHash,
		previousReceiptHash: receipt.previousReceiptHash,
		principalId: receipt.principalId,
		membershipId: receipt.membershipId,
		policyVersion: receipt.policyVersion,
		algorithm: receipt.algorithm,
		keyId: receipt.keyId,
		issuedAt: receipt.issuedAt,
	};
}

/**
 * Canonical compact JSON of the UNSIGNED envelope — the bytes Ed25519 signs
 * (signature NOT included), transitively signing the payload. The UTF-8
 * encoding of the returned string equals core.CanonicalUnsignedEnvelope's
 * bytes.
 */
export function canonicalUnsignedEnvelope(receipt: SignedReceipt): string {
	return JSON.stringify(unsignedEnvelopeObject(receipt));
}

/**
 * Canonical compact JSON of the COMPLETE signed receipt: identical to the
 * unsigned envelope with signature (padded base64) between keyId and issuedAt.
 * receiptHash digests its UTF-8 bytes; the next receipt for the same subject
 * chains on that digest via previousReceiptHash.
 */
export function completeReceiptBytes(receipt: SignedReceipt): string {
	return JSON.stringify({
		subjectType: receipt.subjectType,
		subjectId: receipt.subjectId,
		action: receipt.action,
		tenantId: receipt.tenantId,
		companyId: receipt.companyId,
		fiscalPeriodId: receipt.fiscalPeriodId,
		payloadHash: receipt.payloadHash,
		previousReceiptHash: receipt.previousReceiptHash,
		principalId: receipt.principalId,
		membershipId: receipt.membershipId,
		policyVersion: receipt.policyVersion,
		algorithm: receipt.algorithm,
		keyId: receipt.keyId,
		signature: receipt.signature,
		issuedAt: receipt.issuedAt,
	});
}

// ──────────────────────────────────────────────
// Digests and key ids
// ──────────────────────────────────────────────

function sha256Hex(data: Buffer | string): string {
	return createHash("sha256").update(data).digest("hex");
}

/** Lowercase SHA-256 hex of the canonical payload bytes. */
export function receiptPayloadHash(payload: ReceiptPayload): string {
	return sha256Hex(canonicalReceiptPayload(payload));
}

/**
 * Lowercase SHA-256 hex of the complete canonical signed receipt bytes.
 * previousReceiptHash of the NEXT receipt for the same subject chains on this
 * digest (genesis is empty).
 */
export function receiptHash(receipt: SignedReceipt): string {
	return sha256Hex(completeReceiptBytes(receipt));
}

/**
 * Canonical key id: "ed25519:" plus the full SHA-256 hexadecimal digest of the
 * RAW public key (never truncated). Mirrors core.ReceiptKeyID.
 */
export function receiptKeyId(publicKey: Uint8Array): string {
	return "ed25519:" + sha256Hex(Buffer.from(publicKey));
}

// ──────────────────────────────────────────────
// Node crypto adapters (RFC 8032)
// ──────────────────────────────────────────────

/**
 * Builds an RFC 8032 private KeyObject from a 32-byte seed. Uses JWK import
 * (`d` = the seed) because OpenSSL then derives the RFC 8032 public key —
 * byte-identical with Go's `ed25519.NewKeyFromSeed`. (Node's
 * `generateKeyPairSync("ed25519", { seed })` is NOT RFC 8032 compliant and
 * must not be used for parity.)
 */
function privateKeyFromSeed(seed: Uint8Array): KeyObject {
	const seedBytes = Buffer.from(seed);
	if (seedBytes.length !== 32) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`ed25519 seed must be exactly 32 bytes, got ${seedBytes.length}`,
		);
	}
	return createPrivateKey({
		key: {
			kty: "OKP",
			crv: "Ed25519",
			d: seedBytes.toString("base64url"),
			x: seedBytes.toString("base64url"),
		},
		format: "jwk",
	});
}

/** The raw RFC 8032 public key (32 bytes) of an imported private key. */
function rawPublicKey(privateKey: KeyObject): Uint8Array {
	const jwk = privateKey.export({ format: "jwk" });
	if (typeof jwk.x !== "string") {
		throw new Error("receipt: private key export produced no public key");
	}
	return Buffer.from(jwk.x, "base64url");
}

/** A public KeyObject for the raw 32-byte RFC 8032 public key. */
function publicKeyObject(publicKey: Uint8Array): KeyObject {
	return createPublicKey({
		key: {
			kty: "OKP",
			crv: "Ed25519",
			x: Buffer.from(publicKey).toString("base64url"),
		},
		format: "jwk",
	});
}

// ──────────────────────────────────────────────
// Signer surface (the store receives one; Go: internal/receipts.Signer)
// ──────────────────────────────────────────────

/**
 * ReceiptSigner is the store-facing signing surface of the v0.4.0 Step 3
 * protocol (the Go counterpart is internal/receipts.Signer). The in-memory
 * store receives one at construction; the default Node construction loads
 * or creates the keyring. Signing with a revoked key fails closed; the
 * caller passes the subject's latest receipt hash to chain (genesis = "").
 */
export interface ReceiptSigner {
	/** Canonical key id: "ed25519:" + SHA-256 hex of the raw public key. */
	readonly keyId: string;
	/** The raw RFC 8032 public key (32 bytes) — what keyId derives from. */
	readonly publicKey: Uint8Array;
	/** True once this key may no longer sign (revocation is one-way). */
	readonly revoked: boolean;
	/**
	 * Signs a covered-act payload with this key. previousReceiptHash chains
	 * the new receipt on the subject's latest one (genesis = ""). Throws
	 * ReceiptError when the key is revoked.
	 */
	sign(
		payload: ReceiptPayload,
		previousReceiptHash?: string,
	): SignedReceiptResult;
}

/** Options for NodeSeedSigner. */
export interface NodeSeedSignerOptions {
	/** Mark the key revoked: sign() then fails closed. */
	revoked?: boolean;
}

/**
 * The default seed-based signer: derives the RFC 8032 keypair from a
 * 32-byte seed (JWK `d` import — byte-identical with Go's
 * ed25519.NewKeyFromSeed) and signs via node:crypto. No file keyring lives
 * in the in-memory mirror — callers and tests supply the seed; the keyring
 * file belongs to the Go CLI (internal/receipts).
 */
export class NodeSeedSigner implements ReceiptSigner {
	readonly keyId: string;
	readonly publicKey: Uint8Array;
	readonly revoked: boolean;
	private readonly seed: Uint8Array;

	constructor(seed: Uint8Array, options: NodeSeedSignerOptions = {}) {
		if (seed.length !== 32) {
			throw new ReceiptError(
				"RECEIPT_INVALID",
				`ed25519 seed must be exactly 32 bytes, got ${seed.length}`,
			);
		}
		this.seed = Uint8Array.from(seed);
		this.publicKey = rawPublicKey(privateKeyFromSeed(this.seed));
		this.keyId = receiptKeyId(this.publicKey);
		this.revoked = options.revoked ?? false;
	}

	/** Signs via signReceipt; a revoked key fails closed. */
	sign(payload: ReceiptPayload, previousReceiptHash = ""): SignedReceiptResult {
		if (this.revoked) {
			throw new ReceiptError(
				"RECEIPT_INVALID",
				`key ${this.keyId} is revoked — a revoked key is never selected for new signatures`,
			);
		}
		return signReceipt(payload, this.seed, previousReceiptHash);
	}
}

/** Result of signReceipt: the signed envelope plus the raw public key. */
export interface SignedReceiptResult {
	receipt: SignedReceipt;
	/** Raw RFC 8032 public key (32 bytes) — what keyId derives from. */
	publicKey: Uint8Array;
}

/**
 * Signs a receipt payload with the given 32-byte seed: derives the RFC 8032
 * keypair, computes the canonical payload hash and key id, builds the envelope
 * from the payload's own scope/principal/policy/timestamp/subject/action
 * fields (the design invariant verifyReceipt enforces) and signs the canonical
 * unsigned envelope. previousReceiptHash defaults to genesis ("").
 * Deterministic Ed25519: the same seed + payload produce byte-identical
 * signatures across runtimes.
 */
export function signReceipt(
	payload: ReceiptPayload,
	seed: Uint8Array,
	previousReceiptHash = "",
): SignedReceiptResult {
	const privateKey = privateKeyFromSeed(seed);
	const publicKey = rawPublicKey(privateKey);
	const receipt: SignedReceipt = {
		subjectType: payload.subjectType,
		subjectId: payload.subjectId,
		action: payload.action,
		tenantId: payload.tenantId,
		companyId: payload.companyId,
		fiscalPeriodId: payload.fiscalPeriodId,
		payloadHash: receiptPayloadHash(payload),
		previousReceiptHash,
		principalId: payload.principalId,
		membershipId: payload.membershipId,
		policyVersion: payload.policyVersion,
		algorithm: RECEIPT_ALGORITHM,
		keyId: receiptKeyId(publicKey),
		signature: "",
		issuedAt: payload.issuedAt,
	};
	const signature = ed25519Sign(
		null,
		Buffer.from(canonicalUnsignedEnvelope(receipt), "utf8"),
		privateKey,
	);
	receipt.signature = signature.toString("base64");
	return { receipt, publicKey };
}

/**
 * Minimal fail-closed verification of a receipt against its payload and the
 * signer's raw public key. Checks, in order: closed enums (subjectType,
 * action, algorithm); payload/envelope equality; payloadHash vs the canonical
 * payload digest; keyId vs the supplied public key; the Ed25519 signature over
 * the reconstructed unsigned envelope. Throws ReceiptError with a frozen code.
 * It proves INTEGRITY and SIGNER POSSESSION only. Chain traversal, key
 * lookup/revocation, principal provenance, evidence/rule availability and the
 * verification CLI remain Step 4. Accounting correctness: NOT ASSERTED.
 */
export function verifyReceipt(
	receipt: SignedReceipt,
	payload: ReceiptPayload,
	publicKey: Uint8Array,
): void {
	if (!RECEIPT_SUBJECT_TYPES.includes(receipt.subjectType)) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`unknown subjectType "${receipt.subjectType}" — expected memory|judgment`,
		);
	}
	if (!RECEIPT_ACTIONS.includes(receipt.action)) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`unknown action "${receipt.action}" — the action set is closed`,
		);
	}
	if (receipt.algorithm !== RECEIPT_ALGORITHM) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`algorithm "${receipt.algorithm}" — expected "${RECEIPT_ALGORITHM}"`,
		);
	}
	assertPayloadEnvelopeEquality(receipt, payload);
	const computedHash = receiptPayloadHash(payload);
	if (receipt.payloadHash !== computedHash) {
		throw new ReceiptError(
			"RECEIPT_PAYLOAD_HASH_MISMATCH",
			`payloadHash "${receipt.payloadHash}" does not match the canonical payload digest "${computedHash}"`,
		);
	}
	const derivedKeyId = receiptKeyId(publicKey);
	if (receipt.keyId !== derivedKeyId) {
		throw new ReceiptError(
			"RECEIPT_KEY_MISMATCH",
			`keyId "${receipt.keyId}" does not match the supplied public key ("${derivedKeyId}")`,
		);
	}
	const signature = decodeStrictBase64(receipt.signature);
	if (signature.length !== 64) {
		throw new ReceiptError(
			"RECEIPT_SIGNATURE_INVALID",
			`signature length ${signature.length} — expected 64`,
		);
	}
	const valid = ed25519Verify(
		null,
		Buffer.from(canonicalUnsignedEnvelope(receipt), "utf8"),
		publicKeyObject(publicKey),
		signature,
	);
	if (!valid) {
		throw new ReceiptError(
			"RECEIPT_SIGNATURE_INVALID",
			"Ed25519 signature verification failed over the unsigned envelope",
		);
	}
}

/**
 * Strict standard-padded-base64 decode: Node's `Buffer.from(s, "base64")` is
 * lenient, so a re-encode round trip enforces the same strictness as Go's
 * `base64.StdEncoding.DecodeString` (bad alphabet chars and missing padding
 * fail).
 */
function decodeStrictBase64(encoded: string): Buffer {
	try {
		const decoded = Buffer.from(encoded, "base64");
		if (decoded.toString("base64") !== encoded) {
			throw new Error("non-canonical base64");
		}
		return decoded;
	} catch {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			"signature is not valid padded base64",
		);
	}
}

/** Payload scope, subject, action, principal, policy and timestamp equal the
 * envelope (design invariant). */
function assertPayloadEnvelopeEquality(
	receipt: SignedReceipt,
	payload: ReceiptPayload,
): void {
	if (payload.subjectType !== receipt.subjectType) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`payload subjectType "${payload.subjectType}" differs from envelope "${receipt.subjectType}"`,
		);
	}
	if (payload.subjectId !== receipt.subjectId) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`payload subjectId "${payload.subjectId}" differs from envelope "${receipt.subjectId}"`,
		);
	}
	if (payload.action !== receipt.action) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`payload action "${payload.action}" differs from envelope "${receipt.action}"`,
		);
	}
	if (payload.tenantId !== receipt.tenantId) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`payload tenantId "${payload.tenantId}" differs from envelope "${receipt.tenantId}"`,
		);
	}
	if (payload.companyId !== receipt.companyId) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`payload companyId "${payload.companyId}" differs from envelope "${receipt.companyId}"`,
		);
	}
	if (payload.fiscalPeriodId !== receipt.fiscalPeriodId) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`payload fiscalPeriodId "${payload.fiscalPeriodId}" differs from envelope "${receipt.fiscalPeriodId}"`,
		);
	}
	if (payload.principalId !== receipt.principalId) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`payload principalId "${payload.principalId}" differs from envelope "${receipt.principalId}"`,
		);
	}
	if (payload.membershipId !== receipt.membershipId) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`payload membershipId "${payload.membershipId}" differs from envelope "${receipt.membershipId}"`,
		);
	}
	if (payload.policyVersion !== receipt.policyVersion) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`payload policyVersion "${payload.policyVersion}" differs from envelope "${receipt.policyVersion}"`,
		);
	}
	if (payload.issuedAt !== receipt.issuedAt) {
		throw new ReceiptError(
			"RECEIPT_INVALID",
			`payload issuedAt "${payload.issuedAt}" differs from envelope "${receipt.issuedAt}"`,
		);
	}
}
