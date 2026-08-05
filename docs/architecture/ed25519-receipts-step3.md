# Ed25519 Action Receipts — v0.4.0 Step 3 Integration Design

## Decisions

- Each covered act emits an immutable receipt in its transaction; signing failure rolls back the act.
- Receipts remain separate from the frozen memory envelope. `AccountingMemory.ReceiptID` is never populated after write.
- SQLite stores public keys only; private signing material stays in a user-owned config file.
- Receipt validity proves integrity and signer possession, never accounting correctness.

## Core model

Add `internal/core/receipt.go` and mirror it in `core/types.ts`.

`SignedReceipt` contains the frozen envelope fields: `subjectType`, `subjectId`, `action`, `tenantId`, `companyId`, `fiscalPeriodId`, `payloadHash`, `previousReceiptHash`, `principalId`, `membershipId`, `policyVersion`, `algorithm`, `keyId`, `signature`, and `issuedAt`.

The closed action set is `memory_recorded`, `memory_approved`, `memory_rejected`, `memory_voided`, `relation_confirmed`, `relation_rejected`, `evidence_linked`, and `memory_superseded`. Subject type is `memory` or `judgment`.

Hashes use lowercase SHA-256 hexadecimal. `algorithm` is exactly `Ed25519`; `signature` is padded base64 in the model and raw bytes in SQLite. `previousReceiptHash` is the digest of the prior complete canonical signed receipt for the same subject; genesis is empty.

Private files use mode `0600` and directories use `0700`.

### Canonical payload and signing

`ReceiptPayload` has every key present in this order: `version`, `subjectType`, `subjectId`, `action`, `tenantId`, `companyId`, `fiscalPeriodId`, `reviewedEnvelopeHash`, `resultingEnvelopeHash`, `reviewedJudgmentHash`, `resultingJudgmentHash`, `fromMemoryId`, `fromEnvelopeHash`, `toMemoryId`, `toEnvelopeHash`, `successorId`, `evidenceRef`, `reason`, `principalId`, `membershipId`, `principalRoles`, `authenticationMethod`, `assuranceLevel`, `principalAuthenticatedAt`, `policyVersion`, `issuedAt`.

`version` is `receipt-payload/v0.4.0`. Roles are sorted and deduplicated. Inapplicable fields are empty. Payload scope, principal, policy, timestamp, subject, and action equal the envelope. `payloadHash` is SHA-256 of canonical UTF-8 payload bytes.

Act coverage:

- `memory_recorded`: new memory and its resulting envelope hash.
- `memory_approved`: reviewed H1, resulting H2, reason, and complete verified principal snapshot.
- `memory_rejected` and `memory_voided`: envelope hashes before and after transition.
- `relation_confirmed` and `relation_rejected`: judgment subject, proposed/resulting judgment hashes, both observation IDs and current envelope hashes, resolution, and verified principal snapshot.
- `evidence_linked`: memory subject, pre-link/post-link envelope hashes, and exact evidence reference.
- `memory_superseded`: superseded memory subject, pre/post envelope hashes, and successor ID.

Rejected judgments retain frozen `ComputeJudgmentHash` behavior. Judgment predecessor supersession is covered inside `relation_confirmed`; it does not create another action.

Canonical unsigned-envelope order is `subjectType`, `subjectId`, `action`, `tenantId`, `companyId`, `fiscalPeriodId`, `payloadHash`, `previousReceiptHash`, `principalId`, `membershipId`, `policyVersion`, `algorithm`, `keyId`, `issuedAt`. Ed25519 signs those bytes, transitively signing the payload.

Complete-receipt order is identical with `signature` between `keyId` and `issuedAt`.

Canonicalization uses compact UTF-8 JSON, fixed property order, no omitted keys, JSON string escaping, and no HTML escaping, following `canonicalJSON` in `internal/core/judgment.go`. TS constructs properties in the same order and matches Go escaping. Protocol objects contain strings plus the roles array; maps, nulls, and optional properties are forbidden.

## Signing-key lifecycle

Add `internal/receipts` for key-file I/O and signer orchestration; keep canonicalization and `VerifyReceipt` pure in `internal/core`.

The default keyring is `${UserConfigDir}/drenyra-engram/signing-keys.json`, overridden by `DRENYRA_ENGRAM_SIGNING_KEY`. It contains the active key ID and private Ed25519 seeds in padded base64. Copy the session-file controls: user-only permissions, same-directory exclusive temporary creation, fsync, atomic rename, and fail-closed permission checks. Concurrent first use must converge on one active key or fail.

`keyId` is `ed25519:` plus the full SHA-256 hexadecimal digest of the raw public key. Do not truncate it. SQLite stores only raw public keys.

Generation occurs on first covered mutation or `drenyra-engram keys init`. `keys show` prints active key ID, public key, and lifecycle timestamps but never a seed. `keys rotate` durably creates and activates a new key, registers it, then revokes the old public key in one database transaction. Rotation is explicit, not scheduled.

Revocation blocks new signatures but does not invalidate receipts issued before revocation. Step 4 evaluates issuance against key creation/revocation. Old private seeds may remain protected for recovery but are never selected after rotation. An existing key ID with different public bytes is corruption and fails closed.

## SQLite schema v5

Implement one additive v4-to-v5 migration in `internal/store/store.go`, following the existing fail-closed version chain. Never backfill historical receipts because retrospective signing would falsely claim contemporaneous issuance.

`signing_keys` has `key_id` primary key, constrained `algorithm`, raw `public_key`, `created_at`, and nullable `revoked_at`. Public key and creation are immutable; deletion is forbidden; revocation is a one-way null-to-timestamp update.

`receipts` has an internal ID; signed subject/action/scope/payload-hash/chain/principal/policy/algorithm/key/signature/time columns; canonical `payload_json`; derived unique `receipt_hash`; nullable `memory_id` and `judgment_id` foreign keys. A CHECK requires exactly one typed FK and equality with signed `subject_id`. Add subject/time and key/time indexes.

No-update and no-delete triggers make receipt rows immutable. Enforce unique `(subject_type, subject_id, action, payload_hash)` and a partial singleton index per subject/action except `evidence_linked`. Existing evidence-link uniqueness plus payload uniqueness handles retries; approval/judgment replay paths return before signing.

Before insertion, select the latest receipt for the same subject and copy its derived hash into `previousReceiptHash`; no prior row means empty. The covered mutation has already acquired SQLite's writer lock, preventing committed chain forks.

## Atomic emission points

A store helper receives the active transaction/connection, payload, and captured timestamp. It reads the chain head, canonicalizes, signs, registers the public key, and inserts the receipt. It never starts or commits a transaction.

- `Save` (`store.go` L1081): after observation insertion, emit `memory_recorded` with `recordedAt`. If auto-supersession changed the prior observation, first emit `memory_superseded` for that prior subject with the same timestamp.
- `ApproveMemory` (L1721): after event and transition insertion, before idempotency completion, emit `memory_approved` with H1, H2, reason, verified snapshot, returned approval policy, and transaction `now`.
- `ApplyStatusTransition` (L3175, reached from `api.go` L287/L298): compute envelope hashes around reject/void, write the transition, then emit the corresponding receipt with one timestamp.
- `adjudicateJudgment` (L2430): after decision event and projections, emit `relation_confirmed` or `relation_rejected` with proposed/resulting judgment hashes, both locked observation hashes, resolution, verified snapshot, judgment policy, and transaction timestamp.
- `addLink` (L3385): after a genuinely new evidence row, recompute the envelope with merged links and emit `evidence_linked`. A duplicate remains a no-op. Rule links are not covered.
- `SupersedeExplicit` (L3094): compute envelope hashes around transition and emit `memory_superseded` with successor and transition timestamp.

Any other path that changes an observation to superseded calls the same helper. Imported/sync records do not mint local receipts for remote historical acts; later receipt import must preserve original bytes.

Scope comes from the stored subject. Institutional subjects use empty company and period. Non-policy acts use `kernel/v0.4.0`, avoiding an ambiguous empty policy. Claimed acts use source/transition/link actor as principal ID with empty membership, roles, and authentication fields. Approval and relation decisions use only `VerifiedApprovalPrincipal` snapshots.

Attach signer configuration when `SQLiteStore` opens, below HTTP/MCP/CLI adapters, so all surfaces emit identically. Tests inject a deterministic signer. The TS in-memory store receives a `ReceiptSigner`; default Node construction loads or creates the keyring.

## Minimal verification in Step 3

`core.VerifyReceipt(receipt, payload, publicKey)` checks closed enums and formats; payload/envelope equality; canonical payload hash; key ID derived from the supplied public key; and Ed25519 signature over the reconstructed unsigned envelope.

A modified payload, signed envelope field, or signature fails. Chain traversal, key lookup/revocation, principal provenance, evidence/rule availability, and the verification CLI remain Step 4. Neither this API nor its errors claim accounting correctness; documentation states **Accounting correctness: NOT ASSERTED**.

## TypeScript mirror

Add receipt interfaces to `core/types.ts`, canonical and Node crypto adapters to `core/receipt.ts`, and immutable receipt/public-key collections plus matching emission points to `store/memory-store.ts`. No package dependency is needed.

Add `testdata/golden/receipt-ed25519-v1.json` containing a fixed standard seed/public key, payload, canonical payload bytes, payload hash, unsigned envelope bytes, signature, complete receipt, receipt hash, and contract discriminator. Existing vectors remain byte-for-byte unchanged. Go signs and Node verifies it; Node canonicalizes/signs and Go verifies it.

## Tests and acceptance

- **AC5:** approval persists one resolvable receipt and verifies offline.
- **AC6:** mutation of payload, any signed envelope field, or signature fails.
- **AC9:** a Go-generated signature verifies in Node.
- **AC10:** Node-canonicalized and signed content verifies in Go.
- Cover all eight actions and exact payload fields; assert shared transaction timestamps.
- Inject signer/insert failures and assert neither act nor receipt commits.
- Test receipt immutability and signing-key update restrictions.
- Test idempotent approval/judgment retries and duplicate evidence links emit no duplicate.
- Test subject-chain integrity and changed-link detection.
- Rotate keys: new acts use the new key, old receipts still verify, and the revoked key cannot sign new acts.
- Test fresh v5, additive v4 migration, unknown-version failure, and no historical backfill.

## Executable batches

- **Protocol and keys:** `feat(receipts): add canonical signed action envelope`; `feat(keys): add signing-key lifecycle and key IDs`. Add models, canonicalization, minimal verification, keyring, schema, and migration tests.
- **Atomic integration:** `feat(store): emit action receipts atomically`. Wire all eight acts in Go and TS with rollback, idempotency, chain, and rotation tests.
- **Parity and docs:** `test(protocol): add Go-TS Ed25519 golden vectors`; `docs(architecture): freeze v0.4 receipt boundaries`. Reconcile `ecosystem-boundaries.md` and `trust-model.md`: Engram owns receipts for its own immutable acts; cross-product authorization gates remain outside, and receipt integrity never implies accounting correctness.
