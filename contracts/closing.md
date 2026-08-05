# Contract — Monthly Close (cierre) & Period Gate (v0.5.0, frozen)

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
> no float is ever used for money; version/sequence numbers are JSON integers,
> never floats.
>
> Status: frozen for v0.5.0. Related: contracts/approval.md,
> contracts/receipts.md, contracts/period-comparison.md, contracts/lifecycle.md.

## Invariant

A monthly close is an immutable memory (`kind=summary`, `fiscalEffect=closing`,
topic `closing/CIERRE-YYYYMM`, exact company/period scope, `pending_review`) that
only the existing authenticated approval flow can move to `approved` — and
`closing` requires the `controller` role. **Approving a close BLOCKS all
period-scoped accounting mutations until an explicit controller reopen.**

## Close creation

`CreateClose(scope, input)`:

1. validates the exact company scope and valid `YYYYMM` period;
2. fixes the topic `closing/CIERRE-<period>` and rejects another current close
   for that scope (`PERIOD_ALREADY_CLOSED`);
3. derives the period summary and freezes pending items;
4. validates caller-supplied monetary totals — each `{code, currency,
   amountCents int64, sourceMemoryIds[]}` requires ≥1 same-scope source memory;
   the engine NEVER derives money from prose;
5. saves normally: `pending_review`, `effectiveAt` = last day of the month at
   23:59:59 UTC.

The `CloseSnapshot` (structured, canonical, immutable, participates in the
envelope hash) records: period, generatedAt, summaryHash (self-hash of the
canonical snapshot JSON), counts (total/byKind/byStatus — reflecting the period
state at creation, the close itself NOT counted), totals, pendingItems (the
close excluded), reconciliation counts, narrative memory IDs.

## Period gate

`period_closures(tenant_id, company_id, fiscal_period_id, close_memory_id,
status closed|reopened, close_approval_event_id, closed_at, reopened_at,
reopened_by_subject_id, reopen_reason)` — the authoritative projection.

`assertPeriodWritable` is a STORE INVARIANT checked INSIDE the write
transaction (no adapter bypass, no TOCTOU) before mutation in: save, status /
supersession transitions, evidence/rule links, judgment AND reconciliation
proposal/decision paths (when an endpoint observation is in the closed
period). It applies only to exact company scopes with a period. Reads, close
approval, receipt verification and reopen are exempt. Denied → `PERIOD_CLOSED`
carrying tenant/company/period + close memory ID, never private content.

## Reopen

`ReopenPeriod(scope, expectedCloseMemoryId, reason, requestId, principal)`:
an explicit authenticated CONTROLLER act (standard assurance) with BEGIN
IMMEDIATE, idempotency by (tenant, requestId), exact-scope and
expected-close guards. It only flips the projection to `reopened` and appends
an immutable `period_closure_events` row; it NEVER edits the approved close
memory. A later close is a new revision of the same close topic.

## Receipts

Close approval emits `memory_approved` then `memory_closed` atomically (same
transaction — projection or receipt failure rolls back the approval). Reopen
emits `memory_reopened` on the close-memory receipt chain. Receipt payload
version advances to v0.5 while verifiers keep accepting v0.4.

## Errors (frozen)

```text
PERIOD_CLOSED            (409)  PERIOD_ALREADY_CLOSED   (409)
INVALID_PERIOD           (400)
```

## Surfaces

- HTTP: `POST /accounting/closings` · `POST /accounting/periods/{period}/reopen`
  (authenticated, controller).
- MCP: `accounting_close_create` · `accounting_period_reopen` (fail-closed
  without a session binding).
- CLI: `close create|show|reopen` (reopen loads the 0600 session).

A close may reference IGV/PDT 621 obligations but does not calculate, file or
authorize them. This milestone adds provenance and controls, not tax
correctness.
