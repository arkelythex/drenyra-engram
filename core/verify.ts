/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * OFFLINE verification mirror (v0.4.0 Step 4) — the exact TypeScript counterpart
 * of `internal/core/verify.go`'s PURE logic (design §2-§3): the report contract,
 * the stable layer order, the deterministic layer functions, the aggregator, the
 * report builder and the mandatory conclusion. This module is PURE: no SQLite, no
 * CLI, no I/O orchestration (that lives in internal/server/verify_service.go).
 * The Go↔TS shared fixture (testdata/verify-parity.json) is the executable
 * cross-runtime example (design §7-§8).
 *
 * The engine establishes canonical encoding, ledger/envelope integrity,
 * signer/key timing, provenance continuity, scope consistency, chain continuity
 * and referenced-state availability. It does NOT establish evidence truth,
 * correct rule interpretation, professional soundness or accounting correctness.
 * Every report ends with the exact conclusion
 * `Accounting correctness: NOT ASSERTED`, regardless of the outcome.
 *
 * Layer detail strings are part of the Go↔TS fixture contract: they must be
 * byte-identical with `internal/core/verify.go` (names, statuses and details).
 * The signature layer therefore mirrors Go 1:1 (base64 + length + Ed25519 over
 * the canonical unsigned envelope) instead of wrapping `verifyReceipt`, whose
 * error codes and messages differ from the layer's detail strings.
 */
import {
	createPublicKey,
	verify as ed25519Verify,
	type KeyObject,
} from "node:crypto";

import {
	canonicalReceiptPayload,
	canonicalUnsignedEnvelope,
	receiptHash,
	receiptKeyId,
	receiptPayloadHash,
} from "./receipt.js";
import {
	RECEIPT_ACTIONS,
	RECEIPT_ALGORITHM,
	RECEIPT_SUBJECT_TYPES,
	type EvidenceObject,
	type ReceiptAction,
	type ReceiptPayload,
	type SignedReceipt,
} from "./types.js";

// ──────────────────────────────────────────────
// Report contract (Go↔TS mirror, design §2)
// ──────────────────────────────────────────────

/** Per-layer result of one check instance. Mirrors core.VerificationStatus. */
export const VERIFICATION_STATUSES = ["passed", "failed", "skipped"] as const;

export type VerificationStatus = (typeof VERIFICATION_STATUSES)[number];

/** Report-level conclusion: passed only when every applicable layer passes. */
export const VERIFICATION_OUTCOMES = ["passed", "failed"] as const;

export type VerificationOutcome = (typeof VERIFICATION_OUTCOMES)[number];

/**
 * The exact conclusion EVERY report ends with (design §1 decision 5): Finalize
 * always populates it LAST so the JSON closing brace follows it with no
 * non-JSON trailer (AC12). Verification never asserts accounting correctness.
 */
export const ACCOUNTING_CORRECTNESS_NOT_ASSERTED =
	"Accounting correctness: NOT ASSERTED";

/** One named check with its status and a deterministic detail string. */
export interface VerificationLayer {
	name: string;
	status: VerificationStatus;
	detail: string;
}

/** The per-receipt diagnostic block: stored hash, covered action and the six
 * receipt-layer instances. Mirrors core.ReceiptVerification. */
export interface ReceiptVerification {
	receiptHash: string;
	action: ReceiptAction;
	layers: VerificationLayer[];
}

/** The complete offline verification report. Mirrors core.VerificationReport. */
export interface VerificationReport {
	subjectType: string;
	subjectId: string;
	outcome: VerificationOutcome;
	receipts: ReceiptVerification[];
	layers: VerificationLayer[];
	accountingCorrectness: string;
}

/**
 * Stable layer names — EXACT strings shared with the Go mirror (design §2).
 * Receipt order: payload canonicalization, envelope integrity, signature,
 * signing-key validity, tenant/company scope, chain link. Memory then appends
 * principal provenance, supersession chain, evidence availability and rule
 * availability; judgment appends principal provenance, judgment hash and
 * supersession chain.
 */
export const LAYER_PAYLOAD_CANONICALIZATION = "payload canonicalization";
export const LAYER_ENVELOPE_INTEGRITY = "envelope integrity";
export const LAYER_SIGNATURE = "signature";
export const LAYER_SIGNING_KEY_VALIDITY = "signing-key validity";
export const LAYER_TENANT_COMPANY_SCOPE = "tenant/company scope";
export const LAYER_CHAIN_LINK = "chain link";
export const LAYER_PRINCIPAL_PROVENANCE = "principal provenance";
export const LAYER_SUPERSESSION_CHAIN = "supersession chain";
export const LAYER_EVIDENCE_AVAILABILITY = "evidence availability";
export const LAYER_OBJECT_AVAILABILITY = "object availability";
export const LAYER_RULE_AVAILABILITY = "rule availability";
export const LAYER_JUDGMENT_HASH = "judgment hash";

/** The six receipt-layer names in the stable order — the per-receipt order AND
 * the first six top-level aggregate layers. */
export function receiptLayerNames(): string[] {
	return [
		LAYER_PAYLOAD_CANONICALIZATION,
		LAYER_ENVELOPE_INTEGRITY,
		LAYER_SIGNATURE,
		LAYER_SIGNING_KEY_VALIDITY,
		LAYER_TENANT_COMPANY_SCOPE,
		LAYER_CHAIN_LINK,
	];
}

// ──────────────────────────────────────────────
// Pure verification inputs (store-independent values)
// ──────────────────────────────────────────────

/** The pure verification view of a registered public signing key. PublicKey is
 * padded base64 of the RAW public key. Mirrors core.SigningKey. */
export interface SigningKey {
	found: boolean;
	algorithm: string;
	publicKey: string;
	createdAt: string;
	revokedAt: string;
}

/** The stored fiscal scope of a subject — what the tenant/company scope layer
 * compares the envelope and payload against. Mirrors core.SubjectScope. */
export interface SubjectScope {
	tenantId: string;
	companyId: string;
	fiscalPeriodId: string;
}

/** The immutable event snapshot a covered-act receipt must match (design §3).
 * Verified acts (memory_approved, relation_confirmed, relation_rejected) fill
 * the complete snapshot; claimed acts fill action/timestamp/principalId only —
 * the layer compares the claimed principalId with the immutable act actor/source
 * only (attribution continuity, never authorization). Mirrors core.ActProvenance. */
export interface ActProvenance {
	action: string;
	timestamp: string;
	/** Attribution: the actor recorded by the immutable event. */
	principalId: string;
	/** Verified-act snapshot fields (empty for claimed acts). */
	membershipId: string;
	roles: string[];
	authenticationMethod: string;
	assuranceLevel: string;
	authenticatedAt: string;
	policy: string;
	reason: string;
	reviewedEnvelopeHash: string;
	resultingEnvelopeHash: string;
	/** The judgment hash the immutable decision event recorded. */
	recordedJudgmentHash: string;
}

/** One step of the supersession walk: the subject row, its successor routing
 * ("" for the terminal step) and the stored scope. Mirrors core.SupersessionLink. */
export interface SupersessionLink {
	subjectId: string;
	successorId: string;
	superseded: boolean;
	scope: SubjectScope;
}

// ──────────────────────────────────────────────
// Layer status helpers
// ──────────────────────────────────────────────

function layerPassed(name: string, detail: string): VerificationLayer {
	return { name, status: "passed", detail };
}

function layerFailed(name: string, detail: string): VerificationLayer {
	return { name, status: "failed", detail };
}

function layerSkipped(name: string, detail: string): VerificationLayer {
	return { name, status: "skipped", detail };
}

// ──────────────────────────────────────────────
// Payload canonicalization
// ──────────────────────────────────────────────

const RECEIPT_PAYLOAD_STRING_FIELDS = [
	"version",
	"subjectType",
	"subjectId",
	"action",
	"tenantId",
	"companyId",
	"fiscalPeriodId",
	"reviewedEnvelopeHash",
	"resultingEnvelopeHash",
	"reviewedJudgmentHash",
	"resultingJudgmentHash",
	"fromMemoryId",
	"fromEnvelopeHash",
	"toMemoryId",
	"toEnvelopeHash",
	"successorId",
	"evidenceRef",
	"reason",
	"principalId",
	"membershipId",
	"authenticationMethod",
	"assuranceLevel",
	"principalAuthenticatedAt",
	"policyVersion",
	"issuedAt",
] as const;

const RECEIPT_PAYLOAD_KEYS: ReadonlySet<string> = new Set([
	...RECEIPT_PAYLOAD_STRING_FIELDS,
	"principalRoles",
]);

/**
 * Strict-decodes exactly one ReceiptPayload from the stored canonical JSON:
 * unknown fields and trailing data are rejected. The canonical payload is the
 * authoritative signed input — a parse failure is corruption, never a successful
 * skip (design §1 decision 3). Mirrors core.DecodeStoredPayload.
 */
export function decodeStoredPayload(payloadJson: string): ReceiptPayload {
	let parsed: unknown;
	try {
		parsed = JSON.parse(payloadJson);
	} catch (cause) {
		throw new Error(
			`stored payload_json is not a valid canonical receipt payload: ${
				cause instanceof Error ? cause.message : String(cause)
			}`,
		);
	}
	if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
		throw new Error(
			"stored payload_json is not a valid canonical receipt payload: decoded value is not an object",
		);
	}
	const record = parsed as Record<string, unknown>;
	for (const key of Object.keys(record)) {
		if (!RECEIPT_PAYLOAD_KEYS.has(key)) {
			throw new Error(
				`stored payload_json is not a valid canonical receipt payload: unknown field ${JSON.stringify(key)}`,
			);
		}
	}
	for (const key of RECEIPT_PAYLOAD_STRING_FIELDS) {
		if (record[key] !== undefined && typeof record[key] !== "string") {
			throw new Error(
				`stored payload_json is not a valid canonical receipt payload: field ${JSON.stringify(key)} is not a string`,
			);
		}
	}
	if (
		record.principalRoles !== undefined &&
		!Array.isArray(record.principalRoles)
	) {
		throw new Error(
			'stored payload_json is not a valid canonical receipt payload: field "principalRoles" is not an array',
		);
	}
	return record as unknown as ReceiptPayload;
}

/**
 * Checks the stored payload bytes against the canonical re-marshal (byte
 * equality rejects alternate key order, whitespace, duplicate/unknown keys and
 * non-canonical escaping) and the envelope's payloadHash against the canonical
 * payload digest. Mirrors core.VerifyPayloadCanonicalization.
 */
export function verifyPayloadCanonicalization(
	payloadJson: string,
	payload: ReceiptPayload,
	receipt: SignedReceipt,
): VerificationLayer {
	if (payloadJson !== canonicalReceiptPayload(payload)) {
		return layerFailed(
			LAYER_PAYLOAD_CANONICALIZATION,
			"stored payload_json differs from the canonical payload bytes (alternate key order, whitespace, duplicate/unknown keys or non-canonical escaping)",
		);
	}
	const got = receiptPayloadHash(payload);
	if (receipt.payloadHash !== got) {
		return layerFailed(
			LAYER_PAYLOAD_CANONICALIZATION,
			`payloadHash ${receipt.payloadHash} does not match the canonical payload digest ${got}`,
		);
	}
	return layerPassed(
		LAYER_PAYLOAD_CANONICALIZATION,
		"payload is canonical and payloadHash matches the canonical digest",
	);
}

// ──────────────────────────────────────────────
// Envelope integrity
// ──────────────────────────────────────────────

/** Go-style %q for ASCII values: JSON quoting matches Go's %q escaping for
 * printable ASCII (both escape `"` and `\` the same way). */
function q(value: string): string {
	return JSON.stringify(value);
}

/**
 * Mirrors core.verifyPayloadEnvelopeEquality: returns the RECEIPT_INVALID error
 * text (code prefix included — Go's Error() is Code + ": " + Message) naming the
 * first duplicated payload/envelope field that drifts, or null when equal.
 */
function payloadEnvelopeEqualityError(
	receipt: SignedReceipt,
	payload: ReceiptPayload,
): string | null {
	const checks: Array<[string, string, string]> = [
		["subjectType", payload.subjectType, receipt.subjectType],
		["subjectId", payload.subjectId, receipt.subjectId],
		["action", payload.action, receipt.action],
		["tenantId", payload.tenantId, receipt.tenantId],
		["companyId", payload.companyId, receipt.companyId],
		["fiscalPeriodId", payload.fiscalPeriodId, receipt.fiscalPeriodId],
		["principalId", payload.principalId, receipt.principalId],
		["membershipId", payload.membershipId, receipt.membershipId],
		["policyVersion", payload.policyVersion, receipt.policyVersion],
		["issuedAt", payload.issuedAt, receipt.issuedAt],
	];
	for (const [field, got, want] of checks) {
		if (got !== want) {
			return `RECEIPT_INVALID: payload ${field} ${q(got)} differs from envelope ${q(want)}`;
		}
	}
	return null;
}

/**
 * Validates the closed enums, the duplicated payload/envelope fields and the
 * recomputed receipt digest against the stored receipt_hash. The digest
 * comparison is byte-exact: any envelope mutation changes ReceiptHash. Mirrors
 * core.VerifyEnvelopeIntegrity.
 */
export function verifyEnvelopeIntegrity(
	receipt: SignedReceipt,
	payload: ReceiptPayload,
	storedHash: string,
): VerificationLayer {
	if (!RECEIPT_SUBJECT_TYPES.includes(receipt.subjectType)) {
		return layerFailed(
			LAYER_ENVELOPE_INTEGRITY,
			`unknown subjectType ${q(receipt.subjectType)} — expected memory|judgment`,
		);
	}
	if (!RECEIPT_ACTIONS.includes(receipt.action)) {
		return layerFailed(
			LAYER_ENVELOPE_INTEGRITY,
			`unknown action ${q(receipt.action)} — the action set is closed`,
		);
	}
	if (receipt.algorithm !== RECEIPT_ALGORITHM) {
		return layerFailed(
			LAYER_ENVELOPE_INTEGRITY,
			`algorithm ${q(receipt.algorithm)} — expected ${q(RECEIPT_ALGORITHM)}`,
		);
	}
	const equalityError = payloadEnvelopeEqualityError(receipt, payload);
	if (equalityError !== null) {
		return layerFailed(LAYER_ENVELOPE_INTEGRITY, equalityError);
	}
	const computed = receiptHash(receipt);
	if (computed !== storedHash) {
		return layerFailed(
			LAYER_ENVELOPE_INTEGRITY,
			`recomputed receipt hash ${computed} differs from the stored receipt_hash ${storedHash}`,
		);
	}
	return layerPassed(
		LAYER_ENVELOPE_INTEGRITY,
		"envelope is integral and the stored receipt_hash matches the recomputed digest",
	);
}

// ──────────────────────────────────────────────
// Signature and signing-key validity
// ──────────────────────────────────────────────

/**
 * Strict standard-padded-base64 decode: Node's `Buffer.from(s, "base64")` is
 * lenient, so a re-encode round trip enforces the same strictness as Go's
 * `base64.StdEncoding.DecodeString` (bad alphabet chars and missing padding
 * fail). Returns null on invalid material.
 */
function strictBase64Decode(encoded: string): Buffer | null {
	try {
		const decoded = Buffer.from(encoded, "base64");
		if (decoded.toString("base64") !== encoded) {
			return null;
		}
		return decoded;
	} catch {
		return null;
	}
}

/** A public KeyObject for the raw 32-byte RFC 8032 public key (same JWK path as
 * core/receipt.ts). */
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

/**
 * Returns the raw Ed25519 public key bytes of a stored signing-key record, or
 * null when the key material is missing or invalid — the dependency gate of the
 * signature layer (missing/invalid material skips the signature check with the
 * failed signing-key prerequisite named). Mirrors core.DecodeSigningPublicKey.
 */
export function decodeSigningPublicKey(key: SigningKey): Uint8Array | null {
	if (!key.found || key.algorithm !== RECEIPT_ALGORITHM) {
		return null;
	}
	const pub = strictBase64Decode(key.publicKey);
	if (pub === null || pub.length !== 32) {
		return null;
	}
	return pub;
}

/**
 * Checks the padded-base64 Ed25519 signature over the canonical unsigned
 * envelope. rawKey is the DECODED public key; null skips the layer naming the
 * failed signing-key prerequisite (design §3: a dependency-blocked check may be
 * skipped, the prerequisite still fails the report). Mirrors
 * core.VerifySignature — detail strings are byte-identical.
 */
export function verifySignature(
	receipt: SignedReceipt,
	rawKey: Uint8Array | null,
): VerificationLayer {
	if (rawKey === null) {
		return layerSkipped(
			LAYER_SIGNATURE,
			"skipped: prerequisite 'signing-key validity' failed (key material missing or invalid)",
		);
	}
	const sig = strictBase64Decode(receipt.signature);
	if (sig === null) {
		return layerFailed(LAYER_SIGNATURE, "signature is not valid padded base64");
	}
	if (sig.length !== 64) {
		return layerFailed(
			LAYER_SIGNATURE,
			`signature length ${sig.length} — expected 64`,
		);
	}
	const valid = ed25519Verify(
		null,
		Buffer.from(canonicalUnsignedEnvelope(receipt), "utf8"),
		publicKeyObject(rawKey),
		sig,
	);
	if (!valid) {
		return layerFailed(
			LAYER_SIGNATURE,
			"Ed25519 signature verification failed over the canonical unsigned envelope",
		);
	}
	return layerPassed(
		LAYER_SIGNATURE,
		"Ed25519 signature verifies over the canonical unsigned envelope",
	);
}

/**
 * Strict RFC3339 parse mirroring Go's time.Parse(time.RFC3339, ...): the layout
 * requires the full `YYYY-MM-DDTHH:MM:SS` form plus `Z` or a numeric offset
 * (`±hh`, `±hhmm` or `±hh:mm` — Go's Z07:00 leniency), and rejects fractional
 * seconds and out-of-range dates. Returns null on failure.
 */
const RFC3339_PATTERN =
	/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}(:?\d{2})?)$/;

function parseRfc3339(value: string): Date | null {
	const match = RFC3339_PATTERN.exec(value);
	if (match === null) {
		return null;
	}
	let normalized = value;
	if (match[1] !== "Z" && !match[1].includes(":")) {
		normalized = `${value.slice(0, 19)}${match[1]}:00`;
	}
	const parsed = new Date(normalized);
	return Number.isNaN(parsed.getTime()) ? null : parsed;
}

/**
 * Checks the stored key row: the key exists, the algorithm is Ed25519, the
 * base64 decodes to a raw Ed25519 public key, ReceiptKeyID(rawKey) equals the
 * receipt keyId, created_at <= issued_at, and revoked_at is empty or
 * issued_at < revoked_at. Issued before revocation passes; issued at/after
 * revocation fails. Protocol timestamps must parse as RFC3339. Mirrors
 * core.VerifySigningKeyValidity — detail strings are byte-identical.
 */
export function verifySigningKeyValidity(
	key: SigningKey,
	receipt: SignedReceipt,
): VerificationLayer {
	if (!key.found) {
		return layerFailed(
			LAYER_SIGNING_KEY_VALIDITY,
			`signing key ${receipt.keyId} is not registered`,
		);
	}
	if (key.algorithm !== RECEIPT_ALGORITHM) {
		return layerFailed(
			LAYER_SIGNING_KEY_VALIDITY,
			`signing key ${receipt.keyId} algorithm ${q(key.algorithm)} — expected ${q(RECEIPT_ALGORITHM)}`,
		);
	}
	const pub = strictBase64Decode(key.publicKey);
	if (pub === null || pub.length !== 32) {
		return layerFailed(
			LAYER_SIGNING_KEY_VALIDITY,
			`signing key ${receipt.keyId} public key is not valid padded-base64 Ed25519 material`,
		);
	}
	const derived = receiptKeyId(pub);
	if (derived !== receipt.keyId) {
		return layerFailed(
			LAYER_SIGNING_KEY_VALIDITY,
			`keyId ${receipt.keyId} does not match the stored public key (${derived})`,
		);
	}
	const created = parseRfc3339(key.createdAt);
	if (created === null) {
		return layerFailed(
			LAYER_SIGNING_KEY_VALIDITY,
			`signing key ${receipt.keyId} created_at ${q(key.createdAt)} is not a valid RFC3339 timestamp`,
		);
	}
	const issued = parseRfc3339(receipt.issuedAt);
	if (issued === null) {
		return layerFailed(
			LAYER_SIGNING_KEY_VALIDITY,
			`receipt issued_at ${q(receipt.issuedAt)} is not a valid RFC3339 timestamp`,
		);
	}
	if (created.getTime() > issued.getTime()) {
		return layerFailed(
			LAYER_SIGNING_KEY_VALIDITY,
			`signing key ${receipt.keyId} was created at ${key.createdAt}, after the receipt issuance at ${receipt.issuedAt}`,
		);
	}
	if (key.revokedAt !== "") {
		const revoked = parseRfc3339(key.revokedAt);
		if (revoked === null) {
			return layerFailed(
				LAYER_SIGNING_KEY_VALIDITY,
				`signing key ${receipt.keyId} revoked_at ${q(key.revokedAt)} is not a valid RFC3339 timestamp`,
			);
		}
		if (!(issued.getTime() < revoked.getTime())) {
			return layerFailed(
				LAYER_SIGNING_KEY_VALIDITY,
				`signing key ${receipt.keyId} was revoked at ${key.revokedAt}; issuance at ${receipt.issuedAt} is at/after revocation`,
			);
		}
	}
	return layerPassed(
		LAYER_SIGNING_KEY_VALIDITY,
		"signing key is registered, matches the keyId and its validity window covers the issuance",
	);
}

// ──────────────────────────────────────────────
// Tenant/company scope
// ──────────────────────────────────────────────

/**
 * Requires the payload and the envelope to equal the stored subject's
 * tenant/company/fiscal period. Envelope==payload equality is enforced by the
 * envelope-integrity layer; this layer anchors BOTH to the stored subject, so it
 * is never mere self-consistency (design §3). Mirrors
 * core.VerifyTenantCompanyScope.
 */
export function verifyTenantCompanyScope(
	receipt: SignedReceipt,
	payload: ReceiptPayload,
	subject: SubjectScope,
): VerificationLayer {
	if (
		receipt.tenantId !== subject.tenantId ||
		payload.tenantId !== subject.tenantId
	) {
		return layerFailed(
			LAYER_TENANT_COMPANY_SCOPE,
			`tenantId ${q(receipt.tenantId)}/${q(payload.tenantId)} differs from the stored subject tenant ${q(subject.tenantId)}`,
		);
	}
	if (
		receipt.companyId !== subject.companyId ||
		payload.companyId !== subject.companyId
	) {
		return layerFailed(
			LAYER_TENANT_COMPANY_SCOPE,
			`companyId ${q(receipt.companyId)}/${q(payload.companyId)} differs from the stored subject company ${q(subject.companyId)}`,
		);
	}
	if (
		receipt.fiscalPeriodId !== subject.fiscalPeriodId ||
		payload.fiscalPeriodId !== subject.fiscalPeriodId
	) {
		return layerFailed(
			LAYER_TENANT_COMPANY_SCOPE,
			`fiscalPeriodId ${q(receipt.fiscalPeriodId)}/${q(payload.fiscalPeriodId)} differs from the stored subject fiscal period ${q(subject.fiscalPeriodId)}`,
		);
	}
	return layerPassed(
		LAYER_TENANT_COMPANY_SCOPE,
		"payload, envelope and the stored subject share the same tenant/company/fiscal period",
	);
}

// ──────────────────────────────────────────────
// Chain link
// ──────────────────────────────────────────────

/**
 * Checks the receipt's previousReceiptHash against the immediately preceding
 * COMPUTED receipt hash of the same subject chain (empty for genesis).
 * prevComputedHash is the recomputed digest of the previous receipt in the full
 * chain, or of the resolved standalone predecessor; predecessorResolved
 * distinguishes "genesis with an empty previous hash" from "a non-genesis
 * predecessor that could not be resolved". Mirrors core.VerifyChainLink.
 */
export function verifyChainLink(
	receipt: SignedReceipt,
	prevComputedHash: string,
	predecessorResolved: boolean,
): VerificationLayer {
	if (receipt.previousReceiptHash === "") {
		if (prevComputedHash === "") {
			return layerPassed(
				LAYER_CHAIN_LINK,
				"genesis receipt has an empty previousReceiptHash",
			);
		}
		return layerFailed(
			LAYER_CHAIN_LINK,
			"chain gap: previousReceiptHash is empty but the receipt is not first in the chain",
		);
	}
	if (!predecessorResolved) {
		return layerFailed(
			LAYER_CHAIN_LINK,
			`previousReceiptHash ${receipt.previousReceiptHash} does not resolve to a stored predecessor receipt`,
		);
	}
	if (prevComputedHash === "") {
		return layerFailed(
			LAYER_CHAIN_LINK,
			"genesis receipt must have an empty previousReceiptHash",
		);
	}
	if (receipt.previousReceiptHash !== prevComputedHash) {
		return layerFailed(
			LAYER_CHAIN_LINK,
			`previousReceiptHash ${receipt.previousReceiptHash} does not reference the immediately preceding receipt hash ${prevComputedHash}`,
		);
	}
	return layerPassed(
		LAYER_CHAIN_LINK,
		"chain link references the immediately preceding receipt hash",
	);
}

// ──────────────────────────────────────────────
// Principal provenance
// ──────────────────────────────────────────────

function provenanceFieldFail(
	field: string,
	got: string,
	want: string,
): VerificationLayer {
	return layerFailed(
		LAYER_PRINCIPAL_PROVENANCE,
		`principal provenance mismatch: ${field} ${q(got)} differs from the immutable event ${q(want)}`,
	);
}

/**
 * Matches a covered-act receipt against its immutable event snapshot. Verified
 * acts (memory_approved, relation_confirmed, relation_rejected) compare the
 * complete snapshot; claimed acts compare principalId with the immutable act
 * actor/source ONLY — attribution continuity, never authorization (design §3).
 * Mirrors core.VerifyPrincipalProvenance.
 */
export function verifyPrincipalProvenance(
	payload: ReceiptPayload,
	act: ActProvenance,
): VerificationLayer {
	switch (payload.action) {
		case "memory_approved": {
			if (act.action !== "approved") {
				return layerFailed(
					LAYER_PRINCIPAL_PROVENANCE,
					`immutable event action ${q(act.action)} differs from the claimed approval act`,
				);
			}
			const fields: Array<[string, string, string]> = [
				["principalId", payload.principalId, act.principalId],
				["membershipId", payload.membershipId, act.membershipId],
				[
					"authenticationMethod",
					payload.authenticationMethod,
					act.authenticationMethod,
				],
				["assuranceLevel", payload.assuranceLevel, act.assuranceLevel],
				[
					"principalAuthenticatedAt",
					payload.principalAuthenticatedAt,
					act.authenticatedAt,
				],
				["policyVersion", payload.policyVersion, act.policy],
				["reason", payload.reason, act.reason],
				[
					"reviewedEnvelopeHash",
					payload.reviewedEnvelopeHash,
					act.reviewedEnvelopeHash,
				],
				[
					"resultingEnvelopeHash",
					payload.resultingEnvelopeHash,
					act.resultingEnvelopeHash,
				],
				["issuedAt", payload.issuedAt, act.timestamp],
			];
			for (const [field, got, want] of fields) {
				if (got !== want) {
					return provenanceFieldFail(field, got, want);
				}
			}
			if (!equalStringSets(payload.principalRoles, act.roles)) {
				return layerFailed(
					LAYER_PRINCIPAL_PROVENANCE,
					"principal provenance mismatch: principalRoles differ from the immutable event roles",
				);
			}
			return layerPassed(
				LAYER_PRINCIPAL_PROVENANCE,
				"principal provenance matches the immutable approval event snapshot",
			);
		}
		case "relation_confirmed":
		case "relation_rejected": {
			const wantAction =
				payload.action === "relation_confirmed" ? "confirm" : "reject";
			if (act.action !== wantAction) {
				return layerFailed(
					LAYER_PRINCIPAL_PROVENANCE,
					`immutable event action ${q(act.action)} differs from the claimed decision act`,
				);
			}
			const fields: Array<[string, string, string]> = [
				["principalId", payload.principalId, act.principalId],
				["membershipId", payload.membershipId, act.membershipId],
				[
					"authenticationMethod",
					payload.authenticationMethod,
					act.authenticationMethod,
				],
				["assuranceLevel", payload.assuranceLevel, act.assuranceLevel],
				[
					"principalAuthenticatedAt",
					payload.principalAuthenticatedAt,
					act.authenticatedAt,
				],
				["policyVersion", payload.policyVersion, act.policy],
				["reason", payload.reason, act.reason],
				["issuedAt", payload.issuedAt, act.timestamp],
			];
			for (const [field, got, want] of fields) {
				if (got !== want) {
					return provenanceFieldFail(field, got, want);
				}
			}
			if (!equalStringSets(payload.principalRoles, act.roles)) {
				return layerFailed(
					LAYER_PRINCIPAL_PROVENANCE,
					"principal provenance mismatch: principalRoles differ from the immutable event roles",
				);
			}
			// The immutable event's recorded judgment hash must agree with the
			// committed result (the recompute-vs-current-row agreement lives in the
			// judgment-hash layer).
			if (payload.resultingJudgmentHash !== act.recordedJudgmentHash) {
				return provenanceFieldFail(
					"resultingJudgmentHash",
					payload.resultingJudgmentHash,
					act.recordedJudgmentHash,
				);
			}
			return layerPassed(
				LAYER_PRINCIPAL_PROVENANCE,
				"principal provenance matches the immutable decision event snapshot",
			);
		}
		default: {
			// Claimed acts: attribution continuity only.
			if (payload.principalId !== act.principalId) {
				return layerFailed(
					LAYER_PRINCIPAL_PROVENANCE,
					`attribution mismatch: claimed principal ${q(payload.principalId)} differs from the immutable act actor ${q(act.principalId)}`,
				);
			}
			return layerPassed(
				LAYER_PRINCIPAL_PROVENANCE,
				"attribution continuity: claimed principal matches the immutable act actor",
			);
		}
	}
}

// ──────────────────────────────────────────────
// Supersession chain
// ──────────────────────────────────────────────

/**
 * Validates the full supersession walk from the subject to the terminal current
 * row: fail on a missing referenced successor, a cycle, a cross-tenant/company
 * link, or a disagreement between status and relation (a superseded row without
 * a successor, or a successor link on a non-superseded row). The passed detail
 * reports the terminal current id. Mirrors core.VerifySupersessionChain.
 */
export function verifySupersessionChain(
	links: SupersessionLink[],
	chainScope: SubjectScope,
): VerificationLayer {
	if (links.length === 0) {
		return layerFailed(
			LAYER_SUPERSESSION_CHAIN,
			"no subject row to walk — supersession chain cannot be established",
		);
	}
	const seen = new Set<string>();
	for (let i = 0; i < links.length; i++) {
		const link = links[i];
		if (!scopeEquals(link.scope, chainScope)) {
			return layerFailed(
				LAYER_SUPERSESSION_CHAIN,
				`supersession link at ${link.subjectId} crosses scope (tenant/company/fiscal period)`,
			);
		}
		if (seen.has(link.subjectId)) {
			return layerFailed(
				LAYER_SUPERSESSION_CHAIN,
				`supersession cycle detected at ${link.subjectId}`,
			);
		}
		seen.add(link.subjectId);
		if (link.successorId !== "") {
			if (!link.superseded) {
				return layerFailed(
					LAYER_SUPERSESSION_CHAIN,
					`status of ${link.subjectId} disagrees with its successor relation to ${link.successorId} (row is not superseded)`,
				);
			}
			continue;
		}
		if (link.superseded) {
			return layerFailed(
				LAYER_SUPERSESSION_CHAIN,
				`status of ${link.subjectId} is superseded but no successor relation resolves (missing successor)`,
			);
		}
		if (i !== links.length - 1) {
			return layerFailed(
				LAYER_SUPERSESSION_CHAIN,
				"the terminal subject is not the last step of the walk",
			);
		}
	}
	const terminal = links[links.length - 1].subjectId;
	return layerPassed(
		LAYER_SUPERSESSION_CHAIN,
		`supersession chain is current at ${terminal}`,
	);
}

function scopeEquals(a: SubjectScope, b: SubjectScope): boolean {
	return (
		a.tenantId === b.tenantId &&
		a.companyId === b.companyId &&
		a.fiscalPeriodId === b.fiscalPeriodId
	);
}

// ──────────────────────────────────────────────
// Evidence / rule availability
// ──────────────────────────────────────────────

/**
 * Requires every non-empty receipt evidenceRef to have a current evidence_links
 * row and the current memory envelope (rebuilt from immutable refs plus current
 * links) to match the envelope the chain head committed. The row check plus the
 * committed-envelope comparison detect direct-SQL link removal (design §3, AC7).
 * Mirrors core.VerifyEvidenceAvailability.
 */
export function verifyEvidenceAvailability(
	declaredRefs: string[],
	currentLinks: string[],
	currentEnvelope: string,
	committedEnvelope: string,
): VerificationLayer {
	for (const ref of declaredRefs) {
		if (!containsString(currentLinks, ref)) {
			return layerFailed(
				LAYER_EVIDENCE_AVAILABILITY,
				`evidence ref ${ref} has no current evidence_links row (direct-SQL link removal or corruption)`,
			);
		}
	}
	if (currentEnvelope !== committedEnvelope) {
		return layerFailed(
			LAYER_EVIDENCE_AVAILABILITY,
			`current envelope ${currentEnvelope} differs from the committed head result ${committedEnvelope}`,
		);
	}
	return layerPassed(
		LAYER_EVIDENCE_AVAILABILITY,
		"every declared evidence ref is linked and the current envelope matches the committed head result",
	);
}

/**
 * Object-level availability layer (v0.7.0): classifies every declared evidence
 * ref as OBJECT-BACKED (resolves to a stored EvidenceObject row) or
 * LEGACY/UNRESOLVED (arbitrary external reference — the pre-v0.7 semantics,
 * fully backward compatible, reported never failed). resolved maps each ref
 * that resolves to a stored object to its metadata; the SERVICE resolves rows
 * and verifies their WORM bytes BEFORE calling this layer (a
 * resolved-but-corrupt object is a FAILED layer via verifyObjectBytesIntegrity,
 * never a passed one). Mirrors core.VerifyObjectAvailability.
 *
 * Outcomes: skipped when there are no declared refs or when NONE resolves to
 * an object (legacy refs named); passed when every object-backed ref is
 * present and byte-verified (legacy refs named as byte-unverified); failed
 * only via verifyObjectBytesIntegrity.
 */
export function verifyObjectAvailability(
	refs: string[],
	resolved: Record<string, EvidenceObject>,
): VerificationLayer {
	const canonical = canonicalRefs(refs);
	if (canonical.length === 0) {
		return layerSkipped(
			LAYER_OBJECT_AVAILABILITY,
			"no declared evidence refs — object availability not applicable",
		);
	}
	const legacy = canonical.filter((ref) => !(ref in resolved));
	if (legacy.length === canonical.length) {
		return layerSkipped(
			LAYER_OBJECT_AVAILABILITY,
			`no declared evidence ref resolves to a stored evidence object — legacy/unresolved refs stay backward compatible and byte-unverified: ${legacy.join(", ")}`,
		);
	}
	let detail = `${canonical.length - legacy.length} object-backed evidence refs resolve to stored objects with verified bytes`;
	if (legacy.length > 0) {
		detail += `; legacy/unresolved refs left byte-unverified: ${legacy.join(", ")}`;
	}
	return layerPassed(LAYER_OBJECT_AVAILABILITY, detail);
}

/**
 * WORM byte-integrity layer: passed when the stored bytes of every
 * object-backed ref re-hash to their content addresses, failed when err
 * carries a corruption code (OBJECT_BYTES_MISSING | OBJECT_HASH_MISMATCH —
 * fail closed, no silent repair). err === null passes. The error text names the
 * failing object. Mirrors core.VerifyObjectBytesIntegrity.
 */
export function verifyObjectBytesIntegrity(
	err: string | null,
): VerificationLayer {
	if (err === null) {
		return layerPassed(
			LAYER_OBJECT_AVAILABILITY,
			"object WORM bytes re-hash to their stored content addresses",
		);
	}
	return layerFailed(
		LAYER_OBJECT_AVAILABILITY,
		`object-backed evidence ref fails WORM byte integrity: ${err}`,
	);
}

/** Sorted, deduplicated, non-empty ref set — the canonical order the pure
 * layers classify. Mirrors core.canonicalRefsList. */
function canonicalRefs(refs: string[]): string[] {
	return [...new Set(refs.filter((ref) => ref !== ""))].sort();
}

/**
 * Requires every dynamically declared rule ref (the merged refs beyond the
 * immutable stored set) to be backed by a current rule_links row and the current
 * envelope to match the committed head result. Since v5 has no rule_linked
 * action, removal is detected by the committed-envelope mismatch, not by a new
 * receipt action (design §3). Mirrors core.VerifyRuleAvailability.
 */
export function verifyRuleAvailability(
	storedRefs: string[],
	currentLinks: string[],
	mergedRefs: string[],
	currentEnvelope: string,
	committedEnvelope: string,
): VerificationLayer {
	for (const ref of mergedRefs) {
		if (
			!containsString(storedRefs, ref) &&
			!containsString(currentLinks, ref)
		) {
			return layerFailed(
				LAYER_RULE_AVAILABILITY,
				`rule ref ${ref} lacks a rule_links row (direct-SQL removal or corruption)`,
			);
		}
	}
	if (currentEnvelope !== committedEnvelope) {
		return layerFailed(
			LAYER_RULE_AVAILABILITY,
			`current envelope ${currentEnvelope} differs from the committed head result ${committedEnvelope}`,
		);
	}
	return layerPassed(
		LAYER_RULE_AVAILABILITY,
		"rule refs are row-backed and the current envelope matches the committed head result",
	);
}

// ──────────────────────────────────────────────
// Judgment hash
// ──────────────────────────────────────────────

/**
 * Compares the recomputed hash of the CURRENT judgment row with the latest
 * decision receipt's resultingJudgmentHash (committed state == current state)
 * and requires that committed result to agree with the hash the immutable
 * decision event recorded. A judgment with no decision receipt is a
 * target-not-verifiable failure, never a successful skip (design §3). Mirrors
 * core.VerifyJudgmentHash.
 */
export function verifyJudgmentHash(
	currentHash: string,
	resultingJudgmentHash: string,
	recordedEventHash: string,
): VerificationLayer {
	if (currentHash !== resultingJudgmentHash) {
		return layerFailed(
			LAYER_JUDGMENT_HASH,
			`recomputed current judgment hash ${currentHash} differs from the committed resultingJudgmentHash ${resultingJudgmentHash}`,
		);
	}
	if (resultingJudgmentHash !== recordedEventHash) {
		return layerFailed(
			LAYER_JUDGMENT_HASH,
			`committed resultingJudgmentHash ${resultingJudgmentHash} differs from the immutable decision event hash ${recordedEventHash}`,
		);
	}
	return layerPassed(
		LAYER_JUDGMENT_HASH,
		"judgment hash matches the current row and the immutable decision event",
	);
}

// ──────────────────────────────────────────────
// Aggregation, builder and finalization
// ──────────────────────────────────────────────

/**
 * Merges the per-receipt instances of one layer into the top-level result:
 * failed if any instance fails, skipped only when ALL instances are
 * inapplicable, otherwise passed (design §2). A single instance (the object
 * layers and standalone reports) aggregates to itself so its detail is
 * preserved. The first failed instance's detail is kept — deterministic because
 * receipts are ordered. Mirrors core.AggregateLayers.
 */
export function aggregateLayers(
	name: string,
	layers: VerificationLayer[],
): VerificationLayer {
	if (layers.length === 1) {
		return { ...layers[0], name };
	}
	if (layers.length === 0) {
		return { name, status: "skipped", detail: "inapplicable" };
	}
	let allSkipped = true;
	for (const layer of layers) {
		if (layer.status === "failed") {
			return { name, status: "failed", detail: layer.detail };
		}
		if (layer.status !== "skipped") {
			allSkipped = false;
		}
	}
	if (allSkipped) {
		return { ...layers[0], name, status: "skipped" };
	}
	return {
		name,
		status: "passed",
		detail: `all ${layers.length} receipts passed`,
	};
}

/**
 * Builds an empty verification report for a subject. Outcome is provisional
 * until finalize recomputes it from the layers. Mirrors core.NewReport.
 */
export function newReport(
	subjectType: string,
	subjectId: string,
): VerificationReport {
	return {
		subjectType,
		subjectId,
		outcome: "passed",
		receipts: [],
		layers: [],
		accountingCorrectness: "",
	};
}

/**
 * Closes a report: it derives the outcome (passed only when every applicable
 * top-level layer passes) and forces the accounting-correctness conclusion LAST,
 * so the JSON closing brace always follows it (AC12). Mutates the report in
 * place, mirroring core.Finalize.
 */
export function finalize(report: VerificationReport): void {
	let outcome: VerificationOutcome = "passed";
	for (const layer of report.layers) {
		if (layer.status === "failed") {
			outcome = "failed";
			break;
		}
	}
	report.outcome = outcome;
	report.accountingCorrectness = ACCOUNTING_CORRECTNESS_NOT_ASSERTED;
}

// ──────────────────────────────────────────────
// Small set helpers
// ──────────────────────────────────────────────

function containsString(list: string[], value: string): boolean {
	return list.includes(value);
}

/**
 * Canonical roles: sorted, deduplicated, empty strings dropped — the roles
 * contract of the protocol (same canonicalization as core/receipt.ts).
 */
function canonicalRoles(roles: string[]): string[] {
	return [...new Set(roles.filter((role) => role !== ""))].sort();
}

/** Compares two string lists as canonical sets (sorted, deduplicated, empty
 * strings dropped) — mirrors core.equalStringSets. */
function equalStringSets(a: string[], b: string[]): boolean {
	const sa = canonicalRoles(a);
	const sb = canonicalRoles(b);
	if (sa.length !== sb.length) {
		return false;
	}
	for (let i = 0; i < sa.length; i++) {
		if (sa[i] !== sb[i]) {
			return false;
		}
	}
	return true;
}
