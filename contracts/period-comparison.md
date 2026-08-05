# Contract — Period-over-Period Comparison & Session Context (v0.5.0, frozen)

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
> no float is ever used for money; version/sequence numbers are JSON integers,
> never floats.
>
> Status: frozen for v0.5.0. Related: contracts/closing.md,
> contracts/reconciliation.md, contracts/lifecycle.md.

## Period comparison (pure read — no schema)

`ComparePeriods(fromScope, toScope)` compares two exact company scopes of the
same tenant/company/RUC with distinct valid `YYYYMM` periods. It is a pure
read: no mutations, no receipts.

```ts
interface PeriodComparison {
  from: string; to: string;                       // YYYYMM
  counts: { fromTotal, toTotal, delta, byKindDelta, byStatusDelta };
  chains: { new: ChainRef[]; removed: ChainRef[]; changed: ChainRef[]; unchangedCount };
  statusChanges: { topicKey, fromId, toId, fromStatus, toStatus }[];
  pendingItems: { from, to, delta, addedIds[], resolvedIds[] };
  closeState: { from, to };                        // open | closed | reopened
  narrative: string;
}
```

Semantics:

- Chains match by **topic key** (exact scope stripped).
- `changed` = canonical content (with the scope **period stripped** from the
  comparison hash), status, link sets, or supersedesId differ; status changes
  are ALSO reported separately in `statusChanges`.
- Pending-item delta is keyed by **chain topic** (a carried-over obligation is
  the same chain with a new revision id — neither added nor resolved).
- Close state per period comes from `period_closures`.
- Arrays are stable-sorted by topic key then memory ID; output is
  deterministic.

## Session context

`CurrentContext` is the agent-facing startup snapshot of an exact scope:

```ts
interface CurrentContext {
  scope: MemoryScope;            // exact company scope, never inferred
  periodSummary: { total, byKind, byStatus, closureState, latestClose };
  pendingItems: PendingItem[];
  recentChains: { topicKey, memoryId, kind, status, effectiveAt, title }[];  // ≤ 20
  generatedAt: string;
}
```

- Configured via `DRENYRA_DEFAULT_SCOPE` (JSON-encoded exact company scope) at
  MCP server construction. Absent → the context is `null` and the tool
  `accounting_current_context` refreshes on demand. Present but invalid or
  inaccessible → the server fails closed at construction (never partial
  cross-scope data). Scope is NEVER inferred from database recency.
- `initialize.result._meta["drenyra/currentContext"]` carries the snapshot;
  `accounting_current_context { scope }` refreshes it.

## Errors (frozen)

```text
INVALID_PERIOD            (400)   COMPANY_SCOPE_DENIED  (403)
```

## Surfaces

- HTTP: `GET /accounting/periods/compare?ruc=&from=&to=`.
- MCP: `accounting_compare_periods` · `accounting_current_context`.
- CLI: `compare-periods <ruc> --from YYYYMM --to YYYYMM`.
