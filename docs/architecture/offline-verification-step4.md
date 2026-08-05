# v0.4.0 Step 4 — Offline Verification Integration Design

Status: implementation design for ROADMAP Phase 4 Step 4 (`ROADMAP.md:113-118`).
Scope: Go engine, CLI, and TypeScript pure-logic mirror. No HTTP or MCP verification surface.

## 1. Decisions

1. Verification is read-only over local SQLite. “Offline” means no network, remote key service, HTTP, or MCP dependency.
2. Pure checks/report construction live in `internal/core/verify.go`; I/O orchestration lives in `internal/server/verify_service.go`. TypeScript mirrors only pure logic/report shape in `core/verify.ts`.
3. Stored `payload_json` is authoritative signed input: strict-decode, re-canonicalize, and byte-compare before semantic checks. Receipt columns independently reconstruct `core.SignedReceipt`.
4. Memory/judgment verification covers every receipt for that subject ordered by `issued_at, rowid`. Receipt verification covers one selected receipt and its predecessor link.
5. Every report contains the exact conclusion `Accounting correctness: NOT ASSERTED`, regardless of result.

## 2. Report contract

Add in Go and mirror exactly in TypeScript:

```go
type VerificationStatus string // passed | failed | skipped
type VerificationOutcome string // passed | failed

type VerificationLayer struct {
    Name string `json:"name"`
    Status VerificationStatus `json:"status"`
    Detail string `json:"detail"`
}
type ReceiptVerification struct {
    ReceiptHash string `json:"receiptHash"`
    Action ReceiptAction `json:"action"`
    Layers []VerificationLayer `json:"layers"`
}
type VerificationReport struct {
    SubjectType string `json:"subjectType"`
    SubjectID string `json:"subjectId"`
    Outcome VerificationOutcome `json:"outcome"`
    Receipts []ReceiptVerification `json:"receipts"`
    Layers []VerificationLayer `json:"layers"`
    AccountingCorrectness string `json:"accountingCorrectness"`
}
```

`AccountingCorrectness` is always populated last with `Accounting correctness: NOT ASSERTED`. JSON’s closing brace follows it; no non-JSON trailer is emitted.

A top-level layer aggregates its per-receipt instances: failed if any fails, skipped only when inapplicable, otherwise passed. A dependency-blocked check may be skipped with the failed prerequisite named in `detail`; that prerequisite still makes the report fail. `outcome=passed` only when every applicable layer passes.

Stable layer order is part of the Go↔TS fixture:

- Receipt: `payload canonicalization`, `envelope integrity`, `signature`, `signing-key validity`, `tenant/company scope`, `chain link`.
- Memory: all receipt layers, then `principal provenance`, `supersession chain`, `evidence availability`, `rule availability`.
- Judgment: all receipt layers, then `principal provenance`, `judgment hash`, `supersession chain`.

The report preserves per-receipt diagnostics and one aggregate top-level result per layer.

## 3. Pure layer semantics

`internal/core/verify.go` exposes deterministic layer functions plus builders/aggregators. Reuse canonicalizers and cryptographic primitives from `internal/core/receipt.go:339`, but do not classify layers by parsing error text.

- **Payload canonicalization**: strict-decode exactly one `ReceiptPayload`; reject unknown/trailing data; compare stored bytes with `CanonicalReceiptPayload`; require `ReceiptPayloadHash(payload) == receipt.PayloadHash`. Byte equality rejects alternate key order, whitespace, duplicate/unknown keys, and non-canonical escaping.
- **Envelope integrity**: validate closed enums; compare every duplicated payload/envelope field; recompute `ReceiptHash(receipt)` and compare with stored `receipt_hash`.
- **Signature**: verify padded-base64 Ed25519 over `CanonicalUnsignedEnvelope`. Missing/invalid key material skips this layer with a failed signing-key prerequisite.
- **Signing-key validity**: key exists; algorithm is Ed25519; base64 decodes to a raw Ed25519 public key; `ReceiptKeyID(rawKey) == receipt.KeyID`; `created_at <= issued_at`; and `revoked_at` is empty or `issued_at < revoked_at`. Issued before revocation passes; issued at/after revocation fails. Protocol timestamps must parse.
- **Tenant/company scope**: payload equals envelope and both equal the stored subject’s tenant/company/fiscal period. Standalone receipt verification loads its FK-backed memory/judgment, so this is not mere self-consistency.
- **Chain link**: the first ordered subject receipt has empty previous hash; each later receipt references the immediately preceding computed hash; every stored hash equals the computed hash. A standalone non-genesis predecessor must resolve by hash and have the same subject type/id.
- **Principal provenance**: verified acts (`memory_approved`, `relation_confirmed`, `relation_rejected`) match immutable `approval_events`/`judgment_events`: principal, membership, roles, authentication, assurance, authenticated-at, policy, reason, hashes, action, and timestamp. Claimed acts compare `principalId` with the immutable act actor/source only; that proves attribution continuity, not authorization.
- **Supersession chain**: walk `SuccessorOf` or `JudgmentSuccessorOf` until current. Fail on missing referenced successor, cycle, cross-tenant/company link, or disagreement between status and relation. Report terminal current ID.
- **Evidence availability**: collect every non-empty receipt `evidenceRef` and require a current `evidence_links` row. Rebuild current memory from immutable refs plus current links, recompute `ComputeEnvelopeHash`, and compare with the latest receipt payload’s `resultingEnvelopeHash`. This detects direct-SQL link removal.
- **Rule availability**: rebuild immutable rule refs plus current `rule_links`, require declared dynamic refs to have rows, and compare current envelope hash with the latest receipt’s `resultingEnvelopeHash`. Since v5 has no `rule_linked` action, removal is detected by the committed-envelope mismatch, not a new receipt action.
- **Judgment hash**: recompute `ComputeJudgmentHash` (`internal/core/judgment.go:456`) for the current row and compare with the latest decision receipt’s `resultingJudgmentHash`; require its reviewed hash to match the immutable event snapshot. No decision receipt is a target-not-verifiable error, not a successful skip.

Only the chain head’s resulting envelope hash is compared with current memory. Comparing every historical result to current state would falsely fail after legitimate later links/transitions.

## 4. Store read surface

Extend `internal/store/store.go` without migration:

```go
ReceiptsForSubject(ctx context.Context, subjectType core.SubjectType, subjectID string) ([]core.SignedReceipt, error)
ReceiptByHash(ctx context.Context, receiptHash string) (core.SignedReceipt, error)
ReceiptByID(ctx context.Context, id int64) (core.SignedReceipt, error)
ReceiptPayloadByHash(ctx context.Context, receiptHash string) (payloadJSON string, storedHash string, rowID int64, err error)
```

The subject query orders `issued_at ASC, rowid ASC`. Scans convert raw signature BLOBs to padded base64. Typed/sentinel not-found errors distinguish bad targets from corruption.

The payload accessor is necessary because `SignedReceipt` intentionally excludes persistence metadata and canonical `payload_json`. An unexported `storedReceiptRow` query may avoid N+1 reads while preserving the narrow public contracts.

Reuse `LookupSigningKey` (`internal/store/store.go:4051`). No verifier equivalent of `LatestReceiptChainHead` (`:4073`) is needed: ordered receipts verify full chains and `ReceiptByHash` resolves standalone predecessors. Keep the head method emission-only.

Add read-only helpers returning immutable snapshots rather than exposing SQL:

```go
ReceiptActProvenance(ctx, subjectType, subjectID, action, issuedAt) (core.ActProvenance, bool, error)
EvidenceLinkRefs(ctx, memoryID string) ([]string, error)
RuleLinkRefs(ctx, memoryID string) ([]string, error)
```

Resolve provenance from `approval_events`, `judgment_events`, `transition_log`, `evidence_links`, or recorded observation source. Reject zero/multiple matches; action/subject/timestamp correlate records because receipts have no event-id column.

## 5. Orchestration service

Create `internal/server/verify_service.go` with a small read interface:

```go
func VerifyMemory(ctx context.Context, st VerificationStore, memoryID string) (core.VerificationReport, error)
func VerifyJudgment(ctx context.Context, st VerificationStore, judgmentID string) (core.VerificationReport, error)
func VerifyReceipt(ctx context.Context, st VerificationStore, target core.ReceiptTarget) (core.VerificationReport, error)
```

Flow: load subject/receipts; strict-decode stored payloads; resolve/decode keys; run pure receipt layers; aggregate chain/scope; load provenance/current links; run object layers; build stable report and force the conclusion constant.

Memory accepts its `subject_type=memory` chain from `memory_recorded` genesis through latest act. Judgment accepts its own `relation_confirmed`/`relation_rejected` chain. Receipt selects exact 64-hex hash or decimal SQLite ID; ID is local convenience, hash is portable identity.

## 6. CLI

Add `verify` to `cmd/drenyra-engram/main.go:45-86`:

```text
drenyra-engram verify memory <id> [--db <path>]
drenyra-engram verify judgment <id> [--db <path>]
drenyra-engram verify receipt <hash|id> [--db <path>]
```

Follow `cmdDoctor` (`main.go:263`) for parsing/store lifecycle and `emit` (`:1414`) for indent-2 JSON. Emit reports for both verification pass and failure. Exit 0 only when passed, 1 when any applicable layer fails, and 2 for usage, malformed/not-found target, database/query/decode errors, or inability to complete. A layer failure is evidence, not a store error.

No HTTP route or MCP tool is added.

## 7. TypeScript mirror

Add/export `core/verify.ts` and test it in `core/__tests__/verify.test.ts`. Mirror pure input types, constants, functions, aggregation order, key-time semantics, and report builder. Reuse `core/receipt.ts` and `computeEnvelopeHash` from `core/types.ts`.

Do not mirror SQLite or CLI orchestration. Skip an in-memory example: it duplicates tests without proving integration; the shared fixture is the executable example.

## 8. Tests and acceptance

- Go core: canonical/non-canonical payload, altered signature, key mismatch, malformed key/time, before-revocation pass, at/after-revocation fail, aggregation, mandatory conclusion.
- Store: ordered reads, hash/ID lookup, payload retrieval, predecessor resolution, provenance ambiguity failure.
- Service: valid receipt/memory/judgment; broken predecessor/genesis/cycle; cross-scope chain; supersession walk; judgment mismatch.
- **AC7**: create a valid evidence-linked receipt, bypass protections with direct SQL to remove its link row (or fixture a committed receipt without its link), then require evidence availability failure and current-envelope/head-result hash mismatch.
- **AC12**: serialized all-pass and failed-signature reports both end semantically with exactly `Accounting correctness: NOT ASSERTED`.
- Cross-runtime: one shared JSON fixture contains receipt, payload JSON, key/times, subject scope, links, and expected ordered results. Go and TS must match names, statuses, details, outcome, and conclusion.
- CLI: three forms, hash/numeric ID selectors, indent-2 JSON, and exits 0/1/2.

## 9. Delivery batches

**Batch 1 — engine and mirror**

1. `feat(verify): offline verification per layer` — Go checks, reads, service, tests.
2. `test(protocol): add Go TypeScript verification parity` — TS mirror and shared fixture.

**Batch 2 — surface and freeze**

1. `feat(cli): add offline verify commands` — routing, JSON, exit tests.
2. `docs(architecture): freeze offline verification step 4` — contracts, threat model, ROADMAP after tests pass.

## 10. Explicit non-claims

Verification establishes canonical encoding, ledger/envelope integrity, signer/key timing, provenance continuity, scope consistency, chain continuity, and referenced-state availability. It does not establish evidence truth, correct rule interpretation, professional soundness, or accounting correctness.

**Accounting correctness: NOT ASSERTED**
