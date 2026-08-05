# Contract — Offline Verification (v0.4.0 Step 4, frozen)

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
> no float is ever used for money; version/sequence numbers are JSON integers,
> never floats.
>
> Status: frozen for v0.4.0 Step 4. Related: contracts/receipts.md,
> contracts/approval.md, contracts/judgment.md, ADR-003.

## Invariant

Offline verification is READ-ONLY over the local SQLite store — no network,
remote key service, HTTP or MCP dependency. It answers, per layer, whether an
act's cryptographic integrity, key timing, provenance continuity, scope
consistency, chain continuity and referenced-state availability hold.

**It never answers whether the professional decision is correct.**

> **Accounting correctness: NOT ASSERTED**

Every verification report — all-pass AND failed — ends with exactly this
conclusion.

## Surface

```text
drenyra-engram verify memory <id> [--db <path>]
drenyra-engram verify judgment <id> [--db <path>]
drenyra-engram verify receipt <hash|id> [--db <path>]
```

- `verify receipt <hash>`: exactly 64 lowercase hex (portable identity).
- `verify receipt <id>`: decimal SQLite row id (local convenience).
- Exit `0` when the outcome is passed; `1` when any applicable layer fails (the
  report is still emitted, to stdout); `2` for usage errors, malformed/not-found
  targets, and store/query/decode errors. A layer failure is evidence, never a
  store error.

## Report

```ts
interface VerificationReport {
  subjectType: "memory" | "judgment";
  subjectId: string;
  outcome: "passed" | "failed";
  receipts: Array<{ receiptHash: string; action: ReceiptAction; layers: VerificationLayer[] }>;
  layers: VerificationLayer[];          // aggregated per layer, stable order
  accountingCorrectness: string;        // always "Accounting correctness: NOT ASSERTED" (last)
}
```

A top-level layer is failed if any per-receipt instance fails, skipped only
when inapplicable, otherwise passed. `outcome = passed` only when every
applicable layer passes. A dependency-blocked check is skipped with the failed
prerequisite named in its detail (that prerequisite still fails the report).

## Layers (stable order)

Receipt: `payload canonicalization` · `envelope integrity` · `signature` ·
`signing-key validity` · `tenant/company scope` · `chain link`.

Memory adds: `principal provenance` · `supersession chain` · `evidence
availability` · `rule availability`.

Judgment adds: `principal provenance` · `judgment hash` · `supersession chain`.

Semantics:

- **Payload canonicalization**: the stored `payload_json` must strict-decode to
  exactly one `ReceiptPayload` (no trailing/unknown data), byte-equal
  `CanonicalReceiptPayload`, and hash to the receipt's `payloadHash`.
- **Envelope integrity**: closed enums; every duplicated payload/envelope field
  equal; `ReceiptHash` recomputed equals the stored `receipt_hash`.
- **Signature**: padded-base64 Ed25519 over `CanonicalUnsignedEnvelope`.
- **Signing-key validity**: key exists; algorithm Ed25519; base64 decodes to a
  raw public key; `ReceiptKeyID(raw) == keyId`; `created_at <= issued_at`;
  `revoked_at` empty or `issued_at < revoked_at`. Issued before revocation
  passes; issued at/after revocation fails.
- **Tenant/company scope**: payload equals envelope and both equal the stored
  subject's scope (verified against the FK-backed memory/judgment, not
  self-consistency).
- **Chain link**: the first ordered subject receipt is genesis (empty
  previous); each later receipt references the immediately preceding computed
  hash; a standalone non-genesis predecessor resolves by hash with the same
  subject.
- **Principal provenance**: verified acts match the immutable event snapshots
  (approval_events / judgment_events); claimed acts compare `principalId` with
  the recorded actor — attribution continuity, never authorization.
- **Supersession chain**: walk `SuccessorOf` / `JudgmentSuccessorOf` to the
  current subject; fail on missing successor, cycle, cross-scope link, or
  status/relation disagreement.
- **Evidence availability**: every receipt `evidenceRef` has a current
  `evidence_links` row; the envelope recomputed from immutable refs + current
  links must equal the latest receipt's `resultingEnvelopeHash`. A link removed
  (even by direct SQL) is detected. Same for rules via `rule_links`.
- **Judgment hash**: `ComputeJudgmentHash` of the current row equals the latest
  decision receipt's `resultingJudgmentHash`; the reviewed hash matches the
  immutable event snapshot.

Only the chain head's resulting envelope hash is compared with current state —
comparing every historical result would falsely fail after legitimate later
links/transitions.

## Non-claims

Verification establishes canonical encoding, ledger/envelope integrity,
signer/key timing, provenance continuity, scope consistency, chain continuity
and referenced-state availability. It does not establish evidence truth,
correct rule interpretation, professional soundness, or accounting
correctness.

**Accounting correctness: NOT ASSERTED**
