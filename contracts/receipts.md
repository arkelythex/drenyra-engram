# Contract — Ed25519 Action Receipts (v0.4.0 Step 3, frozen)

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
> no float is ever used for money; version/sequence numbers are JSON integers,
> never floats.
>
> Status: frozen for v0.4.0 Step 3. Related: ADR-003, contracts/approval.md,
> contracts/judgment.md, contracts/provenance.md.
>
> **Receipt integrity proves that an act happened and nobody altered it — it
> NEVER proves the professional decision is correct. Accounting correctness is
> NOT ASSERTED.**

## Invariant

Every immutable act emits an Ed25519 receipt atomically inside the act's own
transaction: the act and its receipt commit together or not at all. A signing
failure rolls back the act. Receipts are separate from the frozen memory
envelope — `AccountingMemory.ReceiptID` is never populated after write.

## Acts (closed set)

```text
memory_recorded      memory_approved     memory_rejected     memory_voided
relation_confirmed   relation_rejected   evidence_linked     memory_superseded
```

`subjectType` is `memory` or `judgment`. Unknown actions/subject types fail
closed.

## Envelope

```ts
interface SignedReceipt {
  subjectType: "memory" | "judgment";
  subjectId: string;
  action: ReceiptAction;            // the 8 acts above
  tenantId: string;
  companyId: string;                // "" for institutional subjects
  fiscalPeriodId: string;           // "" when absent
  payloadHash: string;              // SHA-256 hex of the canonical payload
  previousReceiptHash: string;      // ReceiptHash of the prior receipt of the SAME subject; "" for genesis
  principalId: string;              // verified snapshot OR the recorded claim (actorId)
  membershipId: string;             // verified snapshot OR ""
  policyVersion: string;            // decision policy, or "kernel/v0.4.0" for non-policy acts
  algorithm: "Ed25519";
  keyId: string;                    // "ed25519:" + SHA-256 hex of the raw public key
  signature: string;                // padded base64 in the model; raw bytes in SQLite
  issuedAt: string;                 // RFC3339 UTC — the act's captured transaction timestamp
}
```

## Canonicalization and signing

- `ReceiptPayload` has EVERY key present, in this exact order: `version`
  ("receipt-payload/v0.4.0"), subjectType, subjectId, action, tenantId,
  companyId, fiscalPeriodId, reviewedEnvelopeHash, resultingEnvelopeHash,
  reviewedJudgmentHash, resultingJudgmentHash, fromMemoryId, fromEnvelopeHash,
  toMemoryId, toEnvelopeHash, successorId, evidenceRef, reason, principalId,
  membershipId, principalRoles (sorted + deduplicated), authenticationMethod,
  assuranceLevel, principalAuthenticatedAt, policyVersion, issuedAt.
  Inapplicable fields are empty. No omitted keys, no nulls, no maps.
- `payloadHash` = SHA-256 of the canonical UTF-8 payload bytes (compact JSON,
  fixed property order, JSON string escaping, NO HTML escaping — byte-identical
  in Go and TypeScript).
- The unsigned envelope is signed in this order: subjectType, subjectId,
  action, tenantId, companyId, fiscalPeriodId, payloadHash,
  previousReceiptHash, principalId, membershipId, policyVersion, algorithm,
  keyId, issuedAt. `signature = Ed25519.Sign(privateKey, unsignedEnvelopeBytes)`.
- `ReceiptHash` = SHA-256 of the complete receipt bytes (same order with
  `signature` between keyId and issuedAt). The chain: each receipt's
  `previousReceiptHash` = `ReceiptHash` of the prior receipt of the same
  subject; genesis is empty.

## Act payloads

- `memory_recorded`: new memory + its resulting envelope hash.
- `memory_approved`: reviewedEnvelopeHash (H1) + resultingEnvelopeHash (H2) +
  reason + complete verified principal snapshot + approval policy version.
- `memory_rejected` / `memory_voided`: envelope hashes before and after the
  transition (claimed actor as principalId).
- `relation_confirmed` / `relation_rejected`: judgment subject, proposed +
  resulting judgment hashes, both observation IDs and their current envelope
  hashes, resolution, verified principal snapshot, judgment policy version.
- `evidence_linked`: memory subject, pre-link + post-link envelope hashes, the
  exact evidence reference. Rule links are NOT covered; duplicate links emit
  nothing.
- `memory_superseded`: superseded memory subject, pre/post envelope hashes,
  successor ID.

Non-policy acts use `policyVersion = "kernel/v0.4.0"`. Claimed acts (recorded,
rejected, voided, superseded, linked) use the recorded Source/transition/link
actor as principalId with empty membership/roles/authentication. Verified acts
(approved, relation_*) use the full principal snapshot.

## Key lifecycle

- The private signing key lives in a user-only file
  (`${UserConfigDir}/drenyra-engram/signing-keys.json`, dir 0700, file 0600,
  override `DRENYRA_ENGRAM_SIGNING_KEY`) — never in the database.
- SQLite stores public keys only (`signing_keys`: key_id PK, algorithm,
  public_key, created_at, revoked_at). Public key + creation are immutable;
  deletion is forbidden; revocation is a one-way null→timestamp update.
- `keyId = "ed25519:" + SHA-256 hex of the raw public key` (full digest, never
  truncated).
- Rotation is explicit (`keys rotate`): creates + activates a new key, then
  revokes the old public key in ONE database transaction. A revoked key never
  signs new acts; receipts issued before revocation remain valid.
- Historical acts are NEVER backfilled: retrospective signing would falsely
  claim contemporaneous issuance.

## Verification (Step 3 minimal)

`VerifyReceipt(receipt, payload, publicKey)` checks: closed enums and formats;
payload/envelope field equality; payloadHash matches the canonical payload;
keyId derives from the supplied public key; the Ed25519 signature verifies over
the reconstructed unsigned envelope. Chain traversal, key revocation state,
principal provenance and the verification CLI are Step 4. The verifier never
claims accounting correctness.

## Error codes

```text
RECEIPT_INVALID              RECEIPT_PAYLOAD_HASH_MISMATCH
RECEIPT_SIGNATURE_INVALID    RECEIPT_KEY_MISMATCH
```

## Surfaces

Receipts are emitted at store level (inside each act's transaction), so HTTP,
MCP and CLI all produce receipts identically. Imported/sync records never mint
local receipts for remote historical acts. `drenyra-engram keys init|show|rotate`
manage the signing key; `keys show` prints keyId + public key + lifecycle, never
the seed.
