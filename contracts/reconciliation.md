# Contract — First-class Reconciliation (v0.5.0, frozen)

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
> no float is ever used for money; version/sequence numbers are JSON integers,
> never floats.
>
> Status: frozen for v0.5.0. Related: contracts/judgment.md (the adjudication
> pattern this mirrors), contracts/approval.md, contracts/receipts.md,
> contracts/closing.md.

## Invariant

A reconciliation is a first-class ADJUDICATED entity between two observations.
An agent or system may propose; only a session-derived
`VerifiedApprovalPrincipal` with the `controller` role confirms/rejects. A
confirmed reconciliation is immutable; a correction is a NEW entity that
supersedes it. The `reconciles` observation relation is a projection of a
confirmed reconciliation — the entity is authoritative.

## Entity

```ts
interface Reconciliation {
  id: string;
  tenantId: string;
  companyId: string;
  fiscalPeriodId?: string;      // shared period, or NULL for explicit cross-period
  leftMemoryId: string;
  rightMemoryId: string;
  method: string;               // e.g. "bank_statement", "subledger"
  currency: string;             // ISO 4217
  leftAmountCents: bigint;      // signed int64 cents; never float
  rightAmountCents: bigint;
  varianceCents: bigint;        // engine-derived: left - right
  toleranceCents: bigint;
  status: "proposed" | "confirmed" | "rejected" | "withdrawn" | "superseded";
  proposalReason: string;
  resolution: string;           // empty until confirmed/rejected
  proposer: Source;             // agent|system — provenance only
  adjudicator?: PrincipalSnapshot;
  policyVersion: string;        // "reconciliation-policy/v0.5.0" on decided rows
  predecessorId?: string;
  supersedesId?: string;
  proposedAt: string;
  decidedAt?: string;
}
```

Endpoints must exist, differ, and share tenant/company. They either share a
non-empty period (fiscalPeriodId set) or explicitly declare cross-period
reconciliation (NULL). `varianceCents` is engine-derived, never caller-input.

## Lifecycle

```text
proposed ──confirm(controller)──► confirmed ──superseded (atomic with correction confirm)
        ──reject(controller)───► rejected        (terminal)
        ──withdraw(proposer)──► withdrawn        (terminal)
```

Confirmation requires resolution, `expectedReconciliationHash` (fresh
`ComputeReconciliationHash` vs the proposed state), request ID, standard
assurance and `controller` (`reconciliation-policy/v0.5.0`, check order
tenant → company → membership → role → assurance). Confirming atomically
projects one observation relation `leftMemoryId --reconciles--> rightMemoryId`;
rejected/withdrawn proposals project none. A confirmed historical edge is never
deleted. The period write gate applies when either endpoint is in a closed
period.

## Commands

```ts
interface ProposeReconciliationCommand {
  leftMemoryId: string; rightMemoryId: string;
  method: string; currency: string;
  leftAmountCents: bigint; rightAmountCents: bigint; toleranceCents: bigint;
  reason: string; requestId: string; predecessorId?: string;
}
interface ConfirmReconciliationCommand {
  reconciliationId: string; resolution: string;
  expectedReconciliationHash: string; requestId: string;
}
interface RejectReconciliationCommand {
  reconciliationId: string; reason: string;
  expectedReconciliationHash: string; requestId: string;
}
interface WithdrawReconciliationCommand { reconciliationId: string; requestId: string; }
```

No command carries subject, membership, role, actor-kind or assurance fields.

## Errors (frozen)

```text
RECONCILIATION_NOT_FOUND            (404)
INVALID_RECONCILIATION_TRANSITION   (409)
RECONCILIATION_CONFLICT             (409)
RECONCILIATION_HASH_MISMATCH        (409; only the two hashes in the details)
```

Reused: `AUTHENTICATION_REQUIRED`, `PRINCIPAL_INVALID`, `MEMBERSHIP_INACTIVE`,
`TENANT_SCOPE_MISMATCH`, `COMPANY_SCOPE_DENIED`, `ROLE_NOT_AUTHORIZED`,
`ASSURANCE_TOO_LOW`, `MEMORY_NOT_FOUND`, `IDEMPOTENCY_CONFLICT`,
`PERIOD_CLOSED`.

## Receipts

Confirm/reject emit `reconciliation_confirmed` / `reconciliation_rejected`
(subject type `reconciliation`) covering reviewed/resulting reconciliation
hashes, both observation IDs and envelope hashes, principal snapshot,
resolution and `reconciliation-policy/v0.5.0`.

## Surfaces

- HTTP: `POST /accounting/reconciliations` + `/confirm` + `/reject` +
  `/withdraw` (strict bodies, Idempotency-Key; confirm/reject authenticated).
- MCP: `accounting_reconciliation_propose|confirm|reject|withdraw` (snake_case
  wire; amounts as integer cents; confirm/reject fail closed without a session).
- CLI: `reconcile propose|confirm|reject|withdraw|show`.
