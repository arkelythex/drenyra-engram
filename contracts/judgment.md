# Contract — Adjudicable Conflicts / AccountingJudgment (v0.4.0 Step 2, frozen)

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
> no float is ever used for money; version/sequence numbers are JSON integers,
> never floats.
>
> Status: frozen for v0.4.0 Step 2. Related: ADR-003, contracts/approval.md,
> contracts/lifecycle.md, contracts/provenance.md.

## Invariant

An agent or system may PROPOSE a contradiction but never confirm it. Only a
session-derived `VerifiedApprovalPrincipal` (Step 1 authentication) may
confirm or reject. A confirmed judgment is immutable; a correction is a NEW
judgment that supersedes it.

## Entity

```ts
interface AccountingJudgment {
  id: string;
  tenantId: string;
  companyId: string;
  fiscalPeriodId?: string;      // set only when both observations share a period
  fromId: string;               // first observation
  toId: string;                 // second observation (distinct from fromId)
  relation:
    | "supports" | "contradicts" | "explains"
    | "reconciles" | "reverses" | "supersedes";
  status: "proposed" | "confirmed" | "rejected" | "withdrawn" | "superseded";
  proposer: Source;             // agent|system — provenance only, never authority
  proposalReason: string;
  resolution: string;           // empty until confirmed/rejected
  adjudicator?: PrincipalSnapshot; // set on confirmed/rejected (canonical roles)
  policyVersion: string;        // "judgment-policy/v0.4.0" on decided rows
  predecessorId?: string;       // correction target declared by the successor
  supersedesId?: string;        // successor routing stored on the superseded row
  proposedAt: string;           // RFC3339
  updatedAt: string;
  decidedAt?: string;           // set on every non-proposed row
}
```

## Lifecycle

```text
proposed ──confirm(principal)──► confirmed ──superseded (atomic with correction confirm)
        ──reject(principal)───► rejected        (terminal)
        ──withdraw(proposer)─► withdrawn        (terminal)
        ──supersede(proposer)─► superseded      (terminal; same-proposer immediate correction)
```

- Agents/systems propose and withdraw (provenance continuity — the same
  proposer identity, enforced by the store).
- Only a `VerifiedApprovalPrincipal` confirms/rejects (policy
  `judgment-policy/v0.4.0`: senior_accountant minimum via ladder dominance,
  assurance ≥ standard, exact tenant, company in scope).
- A confirmed judgment is immutable: no update (except the atomic
  confirmed→superseded routing) and no delete (SQLite triggers).
- A correction is a new proposed judgment with `predecessorId`; confirming it
  atomically supersedes the confirmed predecessor and routes readers to the
  successor.

## Commands

```ts
interface ProposeJudgmentCommand { fromId: string; toId: string; relation: Relation; reason: string; requestId: string; predecessorId?: string; }
interface ConfirmJudgmentCommand  { judgmentId: string; resolution: string; expectedJudgmentHash: string; requestId: string; }
interface RejectJudgmentCommand   { judgmentId: string; reason: string; expectedJudgmentHash: string; requestId: string; }
interface WithdrawJudgmentCommand { judgmentId: string; requestId: string; }
```

No command carries subject, membership, role, actor-kind or assurance fields.

## Hash guard

`ComputeJudgmentHash` (canonical JSON + SHA-256) — the proposed hash covers
id, scope, pair, relation, status=proposed, canonical proposer, proposal
reason, predecessor id, proposed timestamp; the confirmed hash additionally
covers resolution, canonical adjudicator snapshot, policy version, status and
decided timestamp. Confirmation/rejection compare a freshly recomputed hash
with `expectedJudgmentHash`; mismatch → `JUDGMENT_HASH_MISMATCH` carrying only
the two hashes, never content.

## Semantics

- One open proposal per (tenant, company, from, to, relation) — the partial
  unique index enforces it; a second open proposal → `JUDGMENT_CONFLICT`.
- An identical confirmed judgment → `JUDGMENT_CONFLICT` (unless this proposal
  names it as `predecessorId`). Different relations for the same pair are
  permitted — apparent disagreement is what adjudication preserves.
- Confirmation projects the proposed relation into the observation `relations`
  table as a compatibility read (actor = verified subject); the judgment row
  remains authoritative — relation presence alone never proves confirmation.
- Confirming/rejecting a judgment NEVER changes the status of the related
  observations (approval is a separate act — contracts/approval.md).
- `conflicts_with` remains a legacy sync/discovery marker: it can motivate a
  proposal but is neither accepted as a proposal relation nor removed.

## Error codes (frozen, transport-independent)

New in Step 2 (reusing the Step 1 codes where applicable):

```text
JUDGMENT_NOT_FOUND        (404)
RELATION_NOT_PROPOSABLE   (400)
RESOLUTION_REQUIRED       (400)
PROPOSAL_UNAUTHORIZED     (403)
INVALID_JUDGMENT_TRANSITION (409)
JUDGMENT_CONFLICT         (409)
JUDGMENT_HASH_MISMATCH    (409; only expected/actual hashes in the details)
```

Reused: `AUTHENTICATION_REQUIRED`, `PRINCIPAL_INVALID`, `MEMBERSHIP_INACTIVE`,
`TENANT_SCOPE_MISMATCH`, `COMPANY_SCOPE_DENIED`, `ROLE_NOT_AUTHORIZED`,
`ASSURANCE_TOO_LOW`, `MEMORY_NOT_FOUND`, `IDEMPOTENCY_CONFLICT`.

## Surfaces

- HTTP: `POST /accounting/judgments` (propose, provenance `source` in body) ·
  `POST /accounting/judgments/{id}/confirm` (authenticated) · `.../reject`
  (authenticated) · `.../withdraw` (provenance). All strict
  (`DisallowUnknownFields`); `Idempotency-Key` header required on mutations.
- MCP: `accounting_judgment_propose` / `_withdraw` (agent|system source,
  snake_case args) · `_confirm` / `_reject` fail closed
  `AUTHENTICATION_REQUIRED` without a session binding. The legacy
  `accounting_judge` tool is REMOVED.
- CLI: `judge propose|confirm|reject|withdraw|show`; confirm/reject load the
  0600 auth session and reject any `--actor`/`--subject`/`--role` flag.
- The v0.3 `API.Judge(conflictID, resolution, actor)` path is deprecated and
  fail-closed (`AUTHENTICATION_REQUIRED`, writes nothing); removed in v0.5.0.
