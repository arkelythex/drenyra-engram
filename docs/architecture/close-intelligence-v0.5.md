# Close Intelligence v0.5.0 — Integration Design

**Status:** integration design, implementation-ready (user-confirmed decision 2026-08-05: approved closes BLOCK period mutations; reopen is an explicit authenticated controller act — "Block with explicit reopen" chosen over informational-only and caller-exception-flag).  
**Scope:** Go engine and TypeScript protocol mirror  
**Milestone:** ROADMAP Phase 5 (`ROADMAP.md:163-166`)

## 1. Decisions

1. A monthly close is an immutable memory: `kind=summary`, `fiscalEffect=closing`, topic `closing/CIERRE-YYYYMM`, exact company/period scope, and `pending_review`. The existing authenticated approval flow is the only path to approval; closing already requires `controller` (`internal/authz/approval_policy.go:111-112`).
2. Approving a close blocks later period-scoped accounting mutations. Reopening is an explicit authenticated controller act. “Closed” is an enforceable store invariant, not a label agents may ignore.
3. Reconciliations are first-class entities, not a new memory kind. They reuse the judgment architecture—agent/system proposal, authenticated human confirmation, immutable events, hash guard, and superseding correction—with reconciliation-specific fields and policy.
4. Period comparison is a pure scope-first read model; it adds no schema.
5. MCP startup context is opt-in through a server-configured exact scope and returned in `initialize.result._meta`; tenant/company are never inferred.
6. SQLite v6 is additive. Existing observations, judgments, relations, and receipts remain valid; receipt enums grow without rewriting prior receipts.

## 2. Monthly close

### 2.1 Command and memory

Add `CreateClose(scope, input)` as the canonical application service. HTTP/MCP/CLI must not construct arbitrary closing memories through generic save. It:

1. validates exact company scope and `YYYYMM` period;
2. fixes topic `closing/CIERRE-<period>` and rejects another current close for that scope;
3. calls existing `PeriodSummary(scope)` (`internal/server/api.go:495`);
4. derives and freezes pending items;
5. validates caller-supplied monetary totals and their source memory IDs;
6. saves through normal immutable `Store.Save`.

The memory has `kind=summary`, `fiscalEffect=closing`, `status=pending_review`, and `effectiveAt` at month end UTC. Its existing What/Why/Where/Learned content is:

- **What:** period, counts, totals, and pending-item digest.
- **Why:** close rationale and policy basis.
- **Where:** company/RUC, period, source systems, and source memory IDs.
- **Learned:** unresolved risks, reconciliation coverage, and follow-up.

Add optional structured `content.closeSnapshot` in Go and TS:

```text
CloseSnapshot {
  period, generatedAt, summaryHash,
  counts { total, byKind, byStatus },
  totals[] { code, currency, amountCents, sourceMemoryIds[] },
  pendingItems[] { memoryId, topicKey, kind, status, title, effectiveAt },
  reconciliation { proposed, confirmed, rejected },
  narrativeMemoryIds[]
}
```

`amountCents` is signed `int64` and a TS bigint transport string; never float. Totals are explicit inputs because current memories expose no general machine-readable amount. Each total requires code, currency, and at least one same-scope source memory; the engine never derives money from prose.

`summaryHash` hashes canonical snapshot JSON. New `observations.close_snapshot_json` stores identical canonical bytes, participates in content/envelope hashes, and is immutable with the memory.

### 2.2 Pending items

At creation, derive from latest revision per chain:

- every `pending_review` memory;
- active/pending/approved `obligation` memories;
- active/pending/approved `exception` memories.

Deduplicate by memory ID, exclude the close being created, sort by `kind,effectiveAt,memoryId`, and embed the frozen list. Pending items do not prevent close approval: they disclose close state. Later resolution requires reopening or a new-period memory that references the old item without mutating it.

Extend `PeriodSummaryOutput` with `LatestClose`, `ClosureState`, and `PendingItems`; retain current fields for compatibility.

### 2.3 Closure projection and write gate

Add this authoritative, query-efficient projection:

```sql
period_closures(
  tenant_id, company_id, fiscal_period_id,
  close_memory_id UNIQUE REFERENCES observations(id),
  status CHECK(status IN ('closed','reopened')),
  close_approval_event_id REFERENCES approval_events(id),
  closed_at, reopened_at, reopened_by_subject_id, reopen_reason,
  PRIMARY KEY(tenant_id, company_id, fiscal_period_id)
)
```

When `ApproveMemory` approves a valid close, its existing `BEGIN IMMEDIATE` transaction (`internal/store/store.go:2043`) inserts/updates this row to `closed` before receipts. Projection or receipt failure rolls back approval. Querying approved closing memories remains a doctor/rebuild consistency check, not the hot-path gate.

The gate is a store invariant. Add `assertPeriodWritable(ctx,q,scope,operation)` after obtaining the write transaction and before mutation in:

- `Store.Save` (`internal/store/store.go:1352`);
- status/supersession transitions and evidence/rule links;
- judgment/reconciliation proposal and decision paths when either endpoint is in the closed period.

It applies only to exact company scopes with a period. Reads, close approval, receipt verification, and `ReopenPeriod` are exempt. Return typed `PERIOD_CLOSED` with tenant/company/period and close memory ID, never private content. Checking inside the transaction closes adapter bypass and TOCTOU races.

`ReopenPeriod(scope, expectedCloseMemoryId, reason, requestId, principal)` uses `BEGIN IMMEDIATE`, idempotency, exact-scope and expected-close guards, and controller/standard-assurance policy. It only changes the projection and appends immutable `period_closure_events`; it never edits the approved memory. A later close is a new revision of the same close topic.

### 2.4 Alternatives

- **Informational-only:** simplest, but “closed period BLOCKED” remains unimplemented and callers must voluntarily comply.
- **Block with caller exception flag:** convenient, but leaks authority into payloads and gives agents a bypass.
- **Block with explicit reopen — recommended:** strongest audit semantics and no hidden bypass; corrections cost one controller step and event/receipt.

### 2.5 Receipts

Extend the closed action sets in `internal/core/receipt.go` and `core/types.ts:1223` with `memory_closed` and `memory_reopened`.

Close approval emits `memory_approved` then `memory_closed`, atomically. Reopen emits `memory_reopened` on the close-memory receipt chain. Close covers H1/H2, scope, snapshot hash, approval principal, policy, and reason; reopen covers close/event IDs, principal, reason, and timestamp. Payload version advances to v0.5 while verifiers continue accepting v0.4.

## 3. First-class reconciliation

### 3.1 Entity choice

A reconciliation is an adjudicated relationship between observations. A memory kind would duplicate relationship state in prose and still need a confirmed `reconciles` edge. The `AccountingJudgment` pattern already proves the authority boundary and transition machinery (`internal/store/store.go:910+`). A dedicated entity adds domain amounts, variance, and method without making generic judgments a catch-all.

### 3.2 Model and lifecycle

```text
Reconciliation {
  id, tenantId, companyId, fiscalPeriodId,
  leftMemoryId, rightMemoryId,
  method, currency, leftAmountCents, rightAmountCents, varianceCents,
  toleranceCents, status, proposalReason, resolution,
  proposer, adjudicator?, policyVersion?,
  predecessorId?, supersedesId?, proposedAt, decidedAt?
}
status: proposed -> confirmed | rejected | withdrawn | superseded
```

Endpoints must exist, differ, and share tenant/company. They either share a non-empty period or explicitly declare cross-period reconciliation; cross-period entities keep `fiscalPeriodId=NULL`, matching judgment convention. `varianceCents = leftAmountCents - rightAmountCents` is engine-derived.

Confirmation requires resolution, `expectedReconciliationHash`, request ID, standard assurance, and `controller`. Agent/system may propose or withdraw its own proposal but never confirm/reject.

Schema v6 adds `reconciliations`, immutable `reconciliation_events`, `reconciliation_idempotency_keys`, and `reconciliation_relations` for entity supersession. Confirmation atomically projects one observation relation `leftMemoryId --reconciles--> rightMemoryId`; rejected/withdrawn proposals project none. Correction creates a new entity and supersedes the old; a confirmed historical edge is not deleted.

Extend receipt subject types with `reconciliation`, and actions with `reconciliation_confirmed` and `reconciliation_rejected`. Receipts cover reviewed/resulting reconciliation hashes, both observation IDs/envelope hashes, principal snapshot, resolution, and `reconciliation-policy/v0.5.0`.

## 4. Period-over-period comparison

Add `ComparePeriods(fromScope,toScope)`. Scopes must be exact company scopes for the same tenant, company, and RUC, with distinct valid periods. Reuse `FindByScope` and latest-per-chain selection; no schema.

```text
PeriodComparison {
  from, to,
  counts { fromTotal, toTotal, delta, byKindDelta, byStatusDelta },
  chains { new[], removed[], changed[], unchangedCount },
  statusChanges[] { topicKey, fromId, toId, fromStatus, toStatus },
  pendingItems { from, to, delta, addedIds[], resolvedIds[] },
  closeState { from, to }, narrative
}
```

Match chains by topic key after exact scope is stripped. `changed` means canonical content/envelope-relevant fields differ. Stable-sort arrays by topic key then memory ID. This is not existing memory-ID `Compare`.

Surfaces: `GET /accounting/periods/compare?ruc=&from=&to=`, CLI `compare-periods <ruc> --from YYYYMM --to YYYYMM`, and MCP `accounting_compare_periods` with two exact scopes.

## 5. Automatic MCP session context

```text
CurrentContext {
  scope,
  periodSummary { total, byKind, byStatus, closureState, latestClose },
  pendingItems[],
  recentChains[] { topicKey, memoryId, kind, status, effectiveAt, title },
  generatedAt
}
```

Configure one exact default scope through `DRENYRA_DEFAULT_SCOPE` JSON. On `initialize`, validate it and load summary plus at most 20 recent chains. Return it as `initialize.result._meta["drenyra/currentContext"]`. If unset, return `null` and instruct use of `accounting_current_context`; if invalid/inaccessible, fail closed with no partial cross-scope data. Never infer scope from database recency.

`notifications/initialized` remains notification-only; no unsolicited push in v0.5. Add MCP `accounting_current_context` for refresh. Replace stale lifecycle text in `internal/server/mcp.go:276` with current memory status/close semantics.

## 6. Surfaces and contracts

- **HTTP:** `POST /accounting/closings`; existing `POST /accounting/memories/{id}/approve`; `POST /accounting/periods/{period}/reopen`; reconciliation propose/confirm/reject/withdraw/show mirroring judgments; period-summary, comparison, and current-context GET routes.
- **MCP:** `accounting_close_create`, existing `accounting_approve`, `accounting_period_reopen`, `accounting_reconciliation_*`, `accounting_compare_periods`, `accounting_current_context`.
- **CLI:** `close create|show|reopen`, existing authenticated `approve`, `reconcile propose|confirm|reject|withdraw|show`, `compare-periods`, extended `period-summary`.
- **Contracts:** add `contracts/closing.md`, `contracts/reconciliation.md`, `contracts/period-comparison.md`; update stale `contracts/lifecycle.md` header and describe period closure without importing the external captura→…→auditoria guard lifecycle.

Adapters call shared application/store services; none reimplement authorization, closure checks, hash guards, or receipts.

## 7. SQLite v6 and compatibility

Migration v5→v6 is one additive transaction: add `observations.close_snapshot_json`; create closure/event and reconciliation/event/idempotency/relation tables, indexes, and immutability triggers; set schema 6 last. Any DDL conflict or unknown version fails closed. Existing rows use NULL snapshot. Existing receipts stay byte-valid; verification dispatches by payload version and rejects unknown subjects/actions.

## 8. TypeScript mirror and golden vectors

Mirror `CloseSnapshot`, closure state/result/errors, `Reconciliation`, command/hash canonicalization, `PeriodComparison`, `CurrentContext`, receipt subjects/actions, and v0.5 payload fields in `core/types.ts` and verification modules.

Shared Go↔TS vectors cover:

1. approved close and `memory_closed`/`memory_reopened` signatures;
2. blocked write (`PERIOD_CLOSED`) with no partial mutation;
3. reconciliation proposed→confirmed, projected edge, and receipt;
4. period delta with new/removed/changed chains and pending delta;
5. initialize context for configured, missing, and invalid scopes.

Update tests asserting exactly eight actions; old receipt fixtures remain unchanged and valid.

## 9. Acceptance and compliance

End-to-end acceptance:

1. create July close from `PeriodSummary`; verify frozen pending items/totals;
2. observe `pending_review`; accountant approval denied, controller approval succeeds against reviewed envelope;
3. verify closure/receipts; ordinary July save fails `PERIOD_CLOSED` without rows/events/receipts;
4. reopen with controller, record/confirm reconciliation, re-close; verify `reconciles` projection/receipt;
5. compare July/August; verify deterministic chain/status/pending deltas;
6. initialize MCP with August default scope; receive bounded context.

Also test signer-failure rollback, idempotent replay, concurrent close/reopen/write races, cross-tenant/company denial, malformed periods, duplicate close approval, cross-period reconciliation, TS parity, and migration retry.

A close may reference IGV/PDT 621 obligations but does not calculate, file, or authorize them. Any later derivation of IGV, SUNAT, UBL, PDT, or filing values requires external `@drenyra/pi` compliance tests. This milestone adds provenance and controls, not tax correctness.

## 10. Executable batches

1. **Close foundation/gate:** v6 closure schema, close snapshot, create/reopen services, approval projection, store gate, receipts, surfaces, parity/concurrency tests. Commit: `feat(close): add monthly close memory and period gate`.
2. **First-class reconciliation:** entity/events/idempotency, policy/hash, relation projection, receipts, surfaces, mirror/tests. Commit: `feat(reconcile): add adjudicated first-class reconciliations`.
3. **Read intelligence:** comparison, extended summary, MCP context/startup `_meta`, HTTP/CLI/MCP tests. Commit: `feat(compare): add period comparison and session close context`.
4. **Protocol freeze:** contracts, lifecycle/MCP drift fixes, migration/golden matrices, ROADMAP acceptance. Commit: `docs(v0.5): freeze close intelligence contracts`.
