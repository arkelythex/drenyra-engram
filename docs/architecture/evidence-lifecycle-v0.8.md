# Evidence Lifecycle v0.8 — Policy-Backed Retention and Purge (Delivered)

> **Status:** DELIVERED (batches 1–4, schema v12 — the full local-first v0.8
> slice below is implemented and tested; the Go store is primary, with
> TypeScript mirrors for the receipt payload vocabulary, the object-level hold
> model and the lifecycle policy — see the four delivery-status notes).
> Remaining scope is documented per section: the §13.1 named lifecycle
> verification layers, the deployment-configured scheduler executor surface
> (§11), and **bundle import/restore — the lifecycle export is a read-only
> query and importing a bundle into another store is explicitly OUT OF SCOPE**
> (§12).
> **Date:** 2026-08-07 · **Basis:** delivered v0.7.0 EvidenceObject slice
> ([evidence-object-v0.7.md](evidence-object-v0.7.md)), the frozen contracts
> ([contracts/lifecycle.md](../../contracts/lifecycle.md),
> [contracts/approval.md](../../contracts/approval.md),
> [contracts/receipts.md](../../contracts/receipts.md),
> [contracts/verification.md](../../contracts/verification.md),
> [contracts/scope.md](../../contracts/scope.md),
> [contracts/closing.md](../../contracts/closing.md)), the delivered stages of the
> [threat model](../security/evidence-lifecycle-and-threat-model.md) (§2–§3, §6),
> ADR-003, and the **approved governance decision** (§1, normative).
>
> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
> no float is ever used for money; version/sequence numbers are JSON integers,
> never floats.

This design turns the **deferred** EvidenceObject production stages — retention
eligibility, legal/audit holds, approved purge, and deterministic export — into
one generic **policy-backed lifecycle**. The invariant that makes it auditable:
**physical bytes are removed ONLY by a human-approved, policy-eligible,
blocker-checked execution act; everything else — metadata, hash, links, events,
receipts, eligibility, approvals — remains immutable.** Nothing in v0.8
auto-deletes, auto-approves, or claims statutory authority.

## Quick path (what a reviewer reads first)

1. Read the **governance decision** (§1) — it is normative and was explicitly
   approved; every design choice below traces to it.
2. Read the **state machine** (§2) and **physical purge semantics** (§11) — the
   two behaviors that differ most from the delivered v0.7 slice.
3. Read the **authz/SoD matrix** (§8) — the deny-list and dual-approval rules are
   the highest-risk surface; the pure policy shipped with batch 1 and the
   storage batches 2–4 follow below.
4. Verify with the **checklist** (§18) that no section claims cloud/OIDC/SUNAT/
   ERP/OCR or legal compliance.

## Delivery status (batch 1 of the v0.8 plan — DELIVERED)

This narrow batch implemented ONLY the pure policy model and authorization
roles. Everything that touched bytes or storage remained deferred at the
time — delivered by batches 2–4 below.

- **DELIVERED (this batch):**
  - The four lifecycle role tokens `records_compliance_officer`,
    `tenant_records_owner`, `tax_responsible`, `operational_accountant` in the
    Go and TypeScript domains (`internal/auth/types.go`, `core/types.ts`); the
    new roles sit outside the accounting ladder at rank 0, exactly like tax
    roles (§8.1).
  - The pure versioned policy `evidence-lifecycle-policy/v0.8.0`
    (`internal/authz/evidence_lifecycle_policy.go`,
    `authz/evidence-lifecycle-policy.ts`) enforcing the frozen §8.2 check
    order: tenant → company scope → membership active → actor-kind deny
    (agent/system) → role deny-list (`operational_accountant`, any role token
    containing `admin`) → role allow → assurance ≥ standard → requester ≠
    approver (SoD) → dual-approval config (category) → second approver
    distinct principal. Denial precedes allow; the four new frozen reason
    codes `ROLE_DENIED`, `APPROVER_IS_REQUESTER`, `DUAL_APPROVAL_REQUIRED`,
    `SAME_PRINCIPAL_SECOND_APPROVAL` were added to the shared code vocabulary
    (`internal/auth/errors.go`, `core/types.ts`).
  - Table-driven Go tests (`internal/authz/evidence_lifecycle_policy_test.go`)
    and TS mirror tests (`authz/__tests__/evidence-lifecycle-policy.test.ts`)
    covering the role matrices, deny-list precedence, SoD, dual approval,
    cross-tenant/company, and principal authentication failures (inactive
    membership, low assurance).
- **DEFERRED (at the time of this batch):** schema v8→v9 and all tables
  (§3–§4), store transitions and blockers (§2, §9–§11), retention policy and
  holds storage (§6–§7), receipts (§5), HTTP/MCP/CLI surfaces (§9, §14),
  deterministic export (§12), and the verification/doctor layers (§13).
  Those sections were the design target and were delivered by batches 2–4
  (below).

## Delivery status (batch 2 of the v0.8 plan — retention policy storage + policy put/resolve/evaluate)

This narrow batch delivered the retention-policy storage layer and its narrow
policy surfaces (§3.1/§4/§6/§9): NO holds, NO purge, NO export, NO deletion,
NO scheduling — the purge-transition acts stayed frozen until the later
holds/request/approval/execution batches (below) delivered them.

- **DELIVERED (this batch):**
  - schema v8→v9, one fail-closed transaction (§4): the immutable
`retention_policies` table (exact scope columns + scope index +
no-update/no-delete triggers), the tenant-scoped
`retention_policy_idempotency_keys` ledger, and the receipts action CHECK
extended by the seven v0.8 evidence-lifecycle acts (layout verbatim;
emission lands with object binding — batch 4);
  - `PutRetentionPolicy` / `ResolveRetentionPolicy` /
`EvaluatePurgeEligibility` (store + API + HTTP + CLI + MCP): the
authenticated administration put (deny-list first, then
records_compliance_officer | tenant_records_owner, assurance ≥ standard,
tenant match), (tenant, requestId) idempotency, the expected-version
supersession guard, the scope-first exact resolution read and the
fail-closed eligibility dimension (§6/§9);
  - surfaces: `POST /accounting/retention-policies` (authenticated) plus
`GET …/resolve` and `POST …/evaluate` (HTTP); the
`accounting_retention_policy_put|resolve|evaluate` MCP tools (the put is
the authenticated mutation and fails closed with AUTHENTICATION_REQUIRED
on the session-less stdio server — tool arguments never supply identity);
and the `retention-policy put|resolve|evaluate` CLI commands (the put
derives the principal from the stored CLI session; reads are exact
scope-first). A policy put emits NO receipt (a policy put is not an
object-chain act; the retention_bound receipt lands with object binding).
- **DEFERRED (at the time of this batch):** holds storage and transitions
  (§7), purge request/approval/execution (§2, §9–§11, §13), deterministic
  export (§12) and the verification/doctor layers (§13). Those sections
  were the design target and were delivered by batches 3–4 (below).

## Delivery status (batch 3 of the v0.8 plan — holds storage + surfaces)

This batch delivered the first-class object-level hold records (§3.2/§7,
schema v10):

- **DELIVERED (this batch):**
  - schema v9→v10, one fail-closed transaction: the immutable `evidence_holds`
    table (object-level only — scope-level holds were cut from the delivered
    slice, closed kind enum, one-way
    `lifted_at/lifted_by/lift_reason` NULL→value closure, exact scope tuple), the
    tenant-scoped `evidence_hold_idempotency_keys` ledger, and the receipts
    action CHECK extended by `hold_placed`/`hold_lifted`;
  - `PlaceHold` / `LiftHold` / `ListHolds` (store + API + HTTP + CLI + MCP):
    the authenticated preservation mutations (principal pre-verified, deny-list
    first, `place_hold`/`lift_hold` actions of the extended evidence-lifecycle
    policy) that DELIBERATELY bypass the closed-period gate (holds only
    preserve evidence), `(tenant, requestId)` idempotency, `hold_placed` /
    `hold_lifted` events + receipts emitted atomically on the `evidence_object`
    chain;
  - surfaces: `POST /accounting/objects/{objectId}/holds`,
    `POST /accounting/holds/{holdId}/lift`, `GET /accounting/objects/{objectId}/holds`
    (HTTP); the `accounting_hold_place|lift|holds_list` MCP tools (mutations
    fail closed with AUTHENTICATION_REQUIRED on the session-less stdio server);
    the `hold place|lift|list` CLI commands (principal from the stored CLI
    session).
  - Receipt payload version advances to `receipt-payload/v0.8.0` for the hold
    acts (additive, shape unchanged — the envelope-hash fields are reused).
- **DEFERRED (at the time of this batch):** purge request/approval/execution
  (§2, §9–§11), deterministic export (§12), verification/doctor layers (§13)
  — delivered by batch 4 (below).

## Delivery status (batch 4 of the v0.8 plan — purge pipeline + physical execution + export + verification/doctor + surfaces)

This batch completes the v0.8 slice: the full purge machine, the physical
purge protocol, the deterministic export bundle, the documented-purge
verification/doctor behavior and the HTTP/CLI/MCP surfaces (schemas v11 and v12).

- **DELIVERED (this batch):**
  - schema v10→v11 (one fail-closed transaction): the immutable
    `evidence_purge_requests` aggregate (ONE open pipeline per object —
    `UNIQUE(object_id)`, guarded status machine, immutable evidence columns with
    the documented retraction-cycle exception), the immutable
    `evidence_purge_approvals` decision ledger (decision token, 1-based
    `approval_order` 1|2, reviewed H1 / resulting H2 hashes, full principal
    snapshot, frozen policy version), the immutable `evidence_lifecycle_events`
    log, the guarded `evidence_retention_state` projection and the tenant-scoped
    `evidence_purge_idempotency_keys` ledger; the receipts singleton index is
    rebuilt excluding the seven purge acts (purge acts legitimately grow per
    object — the exact-duplicate backstop stays the table UNIQUE constraint);
  - schema v11→v12 (one fail-closed transaction): the immutable
    `evidence_purge_executions` attempt ledger (`intent → completed |
    interrupted`, exact rel_path/size, pre-removal hash, bound
    `intent_reviewed_hash`) and the receipts table copied+swapped to extend the
    action CHECK with `purge_intent` (the singleton index excludes it — one
    intent receipt per attempt);
  - the full purge machine (`RequestPurge` / `ApprovePurge` (order 1 + dual
    second approval are the SAME operation — the store derives the order from
    the decision ledger) / `RejectPurge` / `CancelPurge` / `WithdrawPurge` /
    `ExecutePurge`): full blocker set re-checked BEFORE authz on every gate
    (closed-period gate, exact retention resolution + eligibility, active
    blocking hold scan including post-approval holds, lifecycle snapshot/version
    drift — approval can never override a blocker), requester ≠ approver and
    distinct-principal SoD enforced store-side against the STORED requester/first
    approver, `(tenant, requestId)` idempotency, receipts carrying the canonical
    reviewed/resulting lifecycle snapshot hashes H1/H2 in the additive
    `receipt-payload/v0.9.0` payload (with `executionAttemptId` on `purge_intent`
    — the per-attempt discriminator);
  - physical execution (§11): the two-phase, receipt-covered protocol with the
    pre-removal hash check (a mismatch ABORTS, nothing is unlinked), the bound
    intent snapshot and crash convergence (see §11); retry safe by `executionId`;
  - the READ-ONLY deterministic export (`ExportEvidenceLifecycle`, §12):
    tenant/RUC-scoped audit bundle, self-hashing manifest with the separate
    CONTENT (`contentManifestHash`) and ENVELOPE (`bundleHash`) digests,
    per-subject receipt chain ordinals, content-addressed `exportId`,
    deterministic data-derived `generatedAt`, NO receipt emitted, fail-closed
    scope coverage;
  - verification/doctor (§13): `verify object` distinguishes the documented
    purge absence; `doctor` reports lifecycle table counts, `intent`/`interrupted`
    executions (`PURGE_EXECUTION_INTERRUPTED`) and the documented
    `documented_purge`/`documented_intent` object findings; the pure OFFLINE
    export verifier (`VerifyEvidenceExportBundle`) re-validates the bundle
    (structural validity, byte-state consistency, six receipt layers) with no
    store, keyring, network or clock;
  - surfaces: HTTP `POST /accounting/objects/{objectId}/purge`,
    `POST /accounting/purge-requests/{requestId}/approve|reject|cancel|withdraw|execute`,
    `GET /accounting/lifecycle/export`; MCP
    `accounting_purge_request|approve|reject|cancel|withdraw|finalize` and
    `accounting_lifecycle_export` (mutations fail closed with
    AUTHENTICATION_REQUIRED on the session-less stdio server); CLI
    `purge request|approve|reject|cancel|withdraw|execute` and
    `export lifecycle` (identity never from flags — always the stored CLI
    session).
- **DEFERRED (remaining, documented):** the §13.1 named lifecycle verification
  layers (retention-policy / lifecycle-chain / purge-approval-chain /
  hold-consistency), the deployment-configured scheduler executor surface (§11
  — the guarded store operation is executor-agnostic and the scheduler is never
  an approver), and **bundle import/restore** (the export is a read-only query;
  no import/restore surface exists — §12).

## 1. Governance decision (approved — normative)

The following decision is the approved source of truth for v0.8. Design choices
in this document are subordinate to it; where a design choice is open, the
safest reading of this decision wins and is marked as a decision.

- **PURGE requires policy eligibility PLUS human authorization.** No path
  exists to remove bytes without both.
- **Default approver** is `records_compliance_officer` **or**
  `tenant_records_owner` — never an agent, never the system alone, never an
  `operational_accountant`, never a generic admin.
- **Accountant/controller may request** a purge; request is itself a
  professional act (see §8).
- **For configured fiscal/material categories, dual approval is required**:
  default approver PLUS `controller` or `tax_responsible` — two distinct human
  principals, independent acts.
- **Human approval never overrides blockers.** There is no override parameter;
  a blocker (unknown retention state, active hold, closed period, version
  drift) fails the transaction even for an otherwise authorized approver.
- **`UNKNOWN_RETENTION_STATE` blocks** request and execution — the engine
  never guesses a retention outcome.
- **Active legal/audit/dispute/fiscalization holds block** request and
  execution.
- **A hold appearing AFTER approval blocks execution** — approval is a
  necessary, never a sufficient, condition for execution.
- **Physical bytes are removed only at execution**, while metadata, hash,
  links, events, receipts, eligibility and approvals remain immutable and
  auditable.
- **Statutory retention periods: NONE are asserted.** The source check could
  not independently establish the supplied SUNAT duration claim, so this
  document records **no statutory period for any jurisdiction**. Deployments
  MUST supply policy/jurisdiction evidence (a `retention_policies` row with
  `jurisdiction`, `legislation`, `authority` and `source`); the absence of
  resolvable evidence is `UNKNOWN_RETENTION_STATE` and blocks.

| Area | Decision |
| --- | --- |
| Aggregate | The EvidenceObject stays the single aggregate: immutable `evidence_objects` row + WORM bytes ([v0.7.0](evidence-object-v0.7.md)). v0.8 adds lifecycle records AROUND it; it never updates the object row. |
| State machine | `stored → purge_requested → purge_approved → purged`, with terminal `purge_rejected` and reversible `cancel`/`withdraw`; holds are orthogonal, never a machine state (§2). |
| Retention | Policy-backed via a dedicated immutable `retention_policies` table; resolution requires an exact `(jurisdiction, legislation, category)` match, else `UNKNOWN_RETENTION_STATE` (§6). |
| Holds | First-class records (legal/audit/dispute/fiscalization block by default); a one-way `placed → lifted` record, never deleted (§7). |
| Authorization | New versioned policy `evidence-lifecycle-policy/v0.8.0`; deny-list evaluated before any allow; dual approval by category (§8). |
| Bytes | Two-phase execution protocol (intent + completion) with hash-before-unlink; no cross-layer atomicity is claimed (§11). |
| Export | Deterministic tenant-scoped bundle including lifecycle records; purged objects export metadata + purge receipts, never bytes (§12). |
| Verification | Additive layers: retention policy · lifecycle chain · purge approval chain · hold consistency; WORM layer distinguishes documented purge from corruption (§13). |
| Schema | v8→v12, one fail-closed transaction per step (v9/v10/v11/v12), no backfill (§4 + batch notes). |

## 2. Lifecycle state machine

The per-object machine covers the purge pipeline only. Retention eligibility is
a separate **dimension** (§6), and holds are separate **records** (§7) that
gate transitions — neither adds machine states.

```text
stored ──request_purge(eligible, no hold, period open, human)──► purge_requested
purge_requested ──approve(human, SoD, dual where configured)──► purge_approved
purge_requested ──reject(human)──► purge_rejected          (terminal)
purge_requested ──cancel(requester)──► stored               (retraction)
purge_approved  ──withdraw(default approver)──► stored      (approval retracted)
purge_approved  ──execute(blockers re-checked, human or scheduler)──► purged (terminal)
```

| State | Meaning |
| --- | --- |
| `stored` | Bytes present; no outstanding purge request. Objects without lifecycle records are reported as `unmanaged`, never as `stored` (§14). |
| `purge_requested` | Policy-eligible request recorded with full provenance; awaiting human authorization. |
| `purge_approved` | Human authorization recorded; execution pending. Approval alone NEVER removes bytes. |
| `purge_rejected` | Terminal — the request is closed; a new request is a fresh act. History stays visible. |
| `purged` | Terminal — bytes physically removed at execution; metadata/hash/links/events/receipts/eligibility/approvals remain immutable and auditable. |

Transitions:

| Transition | Allowed from | Guards (all checked inside one transaction, in order) |
| --- | --- | --- |
| `request_purge` | `stored` | scope exactness · closed-period gate · retention state resolvable AND `eligible` (`UNKNOWN_RETENTION_STATE`/`RETENTION_NOT_DUE` block) · no active blocking hold · authenticated human requester (`accountant`/`senior_accountant`/`controller`) · expected lifecycle hash matches · idempotency |
| `approve` | `purge_requested` | same blocker set as request (never overridden by approval) · human principal · deny-list · default-approver role · assurance ≥ `standard` · requester ≠ approver · dual-approval config for the category · expected reviewed hash matches |
| `reject` | `purge_requested` | human principal with approval authority; reason required |
| `cancel` | `purge_requested` | the original requester (idempotent retraction) |
| `withdraw` | `purge_approved` | a default approver or dual second approver, reason required |
| `execute` | `purge_approved` | **full blocker re-check** (retention state, holds including any placed after approval, closed period, expected approval hash) · prior human approval present and not withdrawn · executor = human default approver / dual second approver, OR a deployment-configured scheduler invoking the same guarded store operation (never an approver; §11) |

**Blockers are re-evaluated at execution time, not trusted from request time.**
A hold that appears after approval, a policy change that makes the retention
state unknown, or a version drift all block execution with the same frozen
reason codes as request time.

## 3. Data model

All new tables follow the repository conventions: additive DDL, immutable
history, no-update/no-delete triggers on immutable rows, one-way null→timestamp
updates where a record is allowed to close, canonical JSON, and exact scope
columns. The `evidence_objects` row itself is **never** modified by v0.8 —
not even at purge.

### 3.1 `retention_policies` (immutable, versioned)

One row per policy version; supersession via `supersedes_policy_id`, never an
in-place edit. Mirrors the `PolicyRule` evidence shape from v0.6
([fiscal-policy-memory-v0.6.md](fiscal-policy-memory-v0.6.md)): **a policy row
without `jurisdiction`, `legislation`, `authority` and `source` is rejected at
insert** (`POLICY_EVIDENCE_REQUIRED`).

```sql
CREATE TABLE retention_policies (
  id TEXT PRIMARY KEY,
  jurisdiction TEXT NOT NULL,             -- uppercase ^[A-Z][A-Z0-9-]{1,15}$ (syntax only)
  legislation TEXT NOT NULL,              -- regime/family identifier
  authority TEXT NOT NULL,                -- policy owner / issuer
  source TEXT NOT NULL,                   -- evidence: who decided, when, on what basis
  category TEXT NOT NULL,                 -- e.g. 'invoice','cdr','extracto','fiscal','material'
  min_period TEXT NOT NULL CHECK(min_period GLOB '[0-9][0-9][0-9][0-9][0-9][0-9]'),
  version INTEGER NOT NULL,
  supersedes_policy_id TEXT,              -- version chain
  dual_approval_required INTEGER NOT NULL CHECK(dual_approval_required IN (0,1)),
  dual_approver_roles TEXT NOT NULL,      -- canonical JSON array, default '["controller","tax_responsible"]'
  blocking_hold_kinds TEXT NOT NULL,      -- canonical JSON array, default '["legal","audit","dispute","fiscalization"]'
  enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
  created_at TEXT NOT NULL,
  created_by TEXT NOT NULL,
  UNIQUE(jurisdiction, legislation, category, version)
);
```

No-update/no-delete triggers (frozen pattern). `dual_approver_roles` and
`blocking_hold_kinds` are validated to the closed enum at insert; they are
canonical JSON with fixed property order for hashing.

### 3.2 `evidence_holds` (first-class hold records)

Object-level only — `object_id` is a NOT NULL FK (the delivered slice cut
scope-level holds). One-way closure exactly like
`signing_keys.revoked_at` ([contracts/receipts.md](../../contracts/receipts.md)):
`lifted_at`/`lifted_by`/`lift_reason` are NULL→timestamp/subject/reason updates
only, performed by the guarded store API; every other column is immutable.

```sql
CREATE TABLE evidence_holds (
  id TEXT PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES evidence_objects(id),
  tenant_id TEXT NOT NULL, company_id TEXT NOT NULL, ruc TEXT NOT NULL, period TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL CHECK(kind IN ('legal','audit','dispute','fiscalization','other')),
  reason TEXT NOT NULL,
  owner_subject_id TEXT NOT NULL,
  placed_at TEXT NOT NULL, placed_by TEXT NOT NULL,
  lifted_at TEXT, lifted_by TEXT, lift_reason TEXT
);
```

### 3.3 `evidence_purge_requests` (the request aggregate)

The object + requested purge intent, with a guarded status flip through the
store API only (projection pattern, like `period_closures` in
[contracts/closing.md](../../contracts/closing.md)). Immutable columns never
change; only `status` advances through recorded transitions.

```sql
CREATE TABLE evidence_purge_requests (
  id TEXT PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES evidence_objects(id),
  tenant_id TEXT NOT NULL, company_id TEXT NOT NULL, ruc TEXT NOT NULL, period TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL,
  policy_id TEXT NOT NULL REFERENCES retention_policies(id),
  retention_state_snapshot TEXT NOT NULL CHECK(retention_state_snapshot IN ('eligible','not_due','unknown')),
  reviewed_lifecycle_hash TEXT NOT NULL,   -- H_request: what the requester asserted
  status TEXT NOT NULL CHECK(status IN ('requested','approved','rejected','withdrawn','cancelled','executed')),
  requested_at TEXT NOT NULL, requested_by TEXT NOT NULL,
  approved_at TEXT, execution_id TEXT,
  UNIQUE(object_id)                          -- one open purge pipeline per object
);
```

### 3.4 `evidence_purge_approvals` (immutable)

Mirrors `approval_events` from [contracts/approval.md](../../contracts/approval.md):
full principal snapshot, both envelope hashes, policy version, reason.

```sql
CREATE TABLE evidence_purge_approvals (
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL REFERENCES evidence_purge_requests(id),
  approval_order INTEGER NOT NULL CHECK(approval_order IN (1,2)),
  decision TEXT NOT NULL CHECK(decision IN ('approved','rejected','withdrawn')),
  reviewed_hash TEXT NOT NULL,             -- H1: the lifecycle snapshot the human examined
  resulting_hash TEXT NOT NULL,            -- H2: after the transition (always != H1)
  principal_snapshot_json TEXT NOT NULL,   -- subjectId, membershipId, roles (canonical sorted), authMethod, assurance, authenticatedAt
  reason TEXT NOT NULL,
  policy_version TEXT NOT NULL,            -- 'evidence-lifecycle-policy/v0.8.0'
  created_at TEXT NOT NULL
);
```

### 3.5 `evidence_lifecycle_events` (immutable event log)

Every transition, hold placement/lift, and retention binding appends one row
atomically with its receipt. This is the queryable source of truth; projections
are derived.

```sql
CREATE TABLE evidence_lifecycle_events (
  id TEXT PRIMARY KEY,
  object_id TEXT NOT NULL REFERENCES evidence_objects(id),
  request_id TEXT,
  action TEXT NOT NULL CHECK(action IN
    ('retention_bound','purge_requested','purge_approved','purge_rejected',
     'purge_cancelled','purge_withdrawn','purge_intent','purge_executed',
     'hold_placed','hold_lifted')),
  from_state TEXT NOT NULL, to_state TEXT NOT NULL,
  reviewed_hash TEXT NOT NULL, resulting_hash TEXT NOT NULL,
  principal_snapshot_json TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  policy_version TEXT NOT NULL,
  created_at TEXT NOT NULL
);
```

### 3.6 `evidence_retention_state` (guarded projection, one row per object)

```sql
CREATE TABLE evidence_retention_state (
  object_id TEXT PRIMARY KEY REFERENCES evidence_objects(id),
  lifecycle_state TEXT NOT NULL CHECK(lifecycle_state IN
    ('stored','purge_requested','purge_approved','purge_rejected','purged')),
  retention_state TEXT NOT NULL CHECK(retention_state IN ('eligible','not_due','unknown','unmanaged')),
  policy_id TEXT REFERENCES retention_policies(id),
  category TEXT NOT NULL DEFAULT '',
  has_active_blocking_hold INTEGER NOT NULL DEFAULT 0 CHECK(has_active_blocking_hold IN (0,1)),
  current_hash TEXT NOT NULL,              -- SHA-256 of the canonical lifecycle snapshot (§3.8)
  updated_at TEXT NOT NULL
);
```

`unmanaged` marks legacy v8 objects with no lifecycle row (see §14); the doctor
and verification REPORT it, never fail it.

### 3.7 `evidence_purge_executions` (execution protocol records)

```sql
CREATE TABLE evidence_purge_executions (
  execution_id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL REFERENCES evidence_purge_requests(id),
  object_id TEXT NOT NULL REFERENCES evidence_objects(id),
  rel_path TEXT NOT NULL,
  size INTEGER NOT NULL CHECK(size >= 0),
  pre_removal_hash TEXT NOT NULL,          -- re-hash of the bytes immediately before unlink
  state TEXT NOT NULL CHECK(state IN ('intent','completed','interrupted')),
  intent_at TEXT NOT NULL, intent_by TEXT,
  completed_at TEXT, completed_by TEXT,
  completion_receipt_id TEXT
);
```

`interrupted` is terminal for that attempt; a retry creates a NEW execution row
under the same request (idempotent by `execution_id`). The verification layer
and doctor both surface `intent`/`interrupted` rows as findings (§13).

### 3.8 Canonical lifecycle snapshot hash

`current_hash` (and the reviewed/resulting hashes H1/H2) covers, in fixed
canonical JSON order: object id, exact scope tuple, lifecycle state, retention
state, policy id + version, category, active blocking holds (ids + kinds +
placed_at, sorted), and the request/approval ids when present. The contribution
uses the frozen canonicalization rules of the engine (compact UTF-8 JSON, fixed
property order, no HTML escaping, byte-identical Go↔TS). This hash is what an
approver examines and what execution re-checks — no drift can pass silently.

## 4. Schema v8→v9 migration

One fail-closed `BEGIN IMMEDIATE` transaction, exactly mirroring the v7→v8
pattern ([internal/store/store.go](../../internal/store/store.go),
`migrateV7ToV8`):

1. Validate that NONE of the new tables exists (a pre-existing `retention_policies`
   or sibling is a corruption signal; abort).
2. Create the six new tables + `evidence_retention_state` with their triggers
   and scope indexes (DDL above).
3. Rebuild `receipts` under a staging name `receipts_v9`: v8 layout verbatim
   (every CHECK, FK, exactly-one-typed-FK constraint, unique
   `(subject_type, subject_id, action, payload_hash)`) with ONLY the action
   CHECK extended by the seven new v0.8 acts
   (`retention_bound`, `purge_requested`, `purge_approved`, `purge_rejected`,
   `purge_cancelled`, `purge_withdrawn`, `purge_executed`) — the subject CHECK
   already includes `evidence_object`; `hold_placed`/`hold_lifted` are emitted
   as lifecycle events AND as receipt acts on the same subject chain
   (§5). Copy all rows, swap the table into place.
4. `UPDATE schema_meta SET value = '9'` ONLY after every step above succeeded;
   any failure rolls the whole migration back and leaves `schema_version=8`.

No existing row is backfilled or re-hashed. Fresh schema DDL includes the same
tables/triggers. `retention_policies` deliberately has no SQL FK from
`evidence_retention_state` (migration-order resilience); service verification
enforces the logical reference, matching the `rule_links.version` precedent.

## 5. Event, projection and receipt strategy

- **Events are the source of truth.** Every transition appends an immutable
  `evidence_lifecycle_events` row and updates the guarded projection in the
  SAME transaction; projection is derived, queryable state, never a separate
  authority.
- **Receipts certify atomically.** Each event emits an Ed25519 receipt inside
  the same `BEGIN IMMEDIATE` transaction (the frozen invariant of
  [contracts/receipts.md](../../contracts/receipts.md)): act and receipt commit
  together or not at all. `subjectType` stays `evidence_object`;
  `subjectId` is the object id; `previousReceiptHash` chains on the object's
  existing chain (`object_stored` → lifecycle acts).
- **Receipt payload versions advance additively** (mirroring the v0.5
  precedent in [contracts/closing.md](../../contracts/closing.md)): the hold
  acts stamp `receipt-payload/v0.8.0` (shape unchanged — the envelope-hash
  fields are reused) and the purge acts stamp `receipt-payload/v0.9.0`;
  verifiers keep accepting v0.4+ payloads. v0.9.0 acts carry the lifecycle
  snapshot hashes (reviewed + resulting, H1/H2), plus reason and the complete
  principal snapshot for verified acts.
- **Hold and retention acts are receipts too**: `hold_placed`/`hold_lifted`
  and `retention_bound` close the chain so an audit can prove every gate input
  (policy binding, hold placement/lift) was receipt-covered at the time.
- A signing failure rolls back the transition; a receipt never exists for a
  transition that did not commit.
- Blocked or denied attempts (blockers, authz, concurrency) return frozen error
  codes and are NOT recorded as events — consistent with the existing
  transition-error behavior; the codes are stable for audit correlation.

## 6. Retention policy and eligibility

- Retention is **deployment policy, not statutory assertion**. `retention_policies`
  rows require jurisdiction/legislation/authority/source evidence at insert
  (§3.1). This repository asserts NO statutory period (governance decision §1).
- **Resolution** (`ResolveRetentionPolicy(object)`): exact
  `(jurisdiction, legislation, category)` match against the highest
  `version` of an enabled policy whose `min_period` is reached; jurisdiction
  derives from the object's scope/tenant configuration, category from the
  object's content type or declared category. Zero matches, ambiguous matches,
  disabled policy, or an unversioned/unsourced row → `UNKNOWN_RETENTION_STATE`.
  Multiple enabled candidates for the same tuple fail closed
  (`RETENTION_POLICY_AMBIGUOUS`) — never guess.
- **Eligibility dimension**: `eligible` (resolved, period reached) ·
  `not_due` (resolved, period not reached) · `unknown` (unresolvable) ·
  `unmanaged` (legacy object, no lifecycle row). Only `eligible` permits a
  request; `unknown` and `unmanaged` block; `not_due` blocks with
  `RETENTION_NOT_DUE`.
- The `retention_bound` event + receipt records the resolution snapshot
  (policy id, version, eligibility, resolution time) so a later policy change
  is auditable against what was bound at request time.

## 7. Holds

- A hold is a first-class, object-level record (`evidence_holds`, with a
  required `object_id` FK — the delivered slice has no scope-level holds),
  placed by an authenticated human principal with a reason and
  owner; placement emits `hold_placed` event + receipt.
- **Blocking kinds default to `legal`, `audit`, `dispute`, `fiscalization`**
  (frozen default in policy `blocking_hold_kinds`); `other` holds do not block
  purge by default — a deployment may extend the blocking set per policy.
- An active blocking hold (placed and not lifted) blocks `request_purge` and
  `execute` with `HOLD_ACTIVE`. Lifting is a one-way closure (`hold_lifted`
  event + receipt) by an authenticated principal with a reason; lifted holds
  remain visible forever.
- **A hold placed after approval blocks execution**: `execute` re-scans holds
  inside the transaction; `HOLD_ACTIVE` fails execution even though approval
  exists. The approval itself stays valid and auditable; withdrawing it is the
  documented cleanup (§2).

## 8. Authorization and SoD policy

Versioned pure policy `evidence-lifecycle-policy/v0.8.0` in the same module
family as `approval-policy/v0.4.0`
([authz/approval-policy.ts](../../authz/approval-policy.ts),
`internal/authz/approval_policy.go`) — deterministic, no DB/clock/identity
access, principal passed as a pre-verified argument (ADR-003: the transport
payload can never declare roles).

### 8.1 Roles

| Role | Allowed to do |
| --- | --- |
| `accountant` / `senior_accountant` / `controller` | Request purge (accounting ladder) |
| `records_compliance_officer` | Default approver / reject / withdraw; second approver never |
| `tenant_records_owner` | Default approver / reject / withdraw; second approver never |
| `tax_responsible` | Second approver ONLY for dual-approval categories |
| `controller` | Second approver for dual-approval categories (ladder position never implies tax roles, and vice versa) |
| `operational_accountant`, any role token containing `admin`, agents, systems | **NEVER** request, approve, reject, withdraw or execute — deny-list wins over every other check |

New roles extend the `AccountingRole` union (Go + TS mirrors); the accounting
ladder rank of the new roles is 0 (explicit-match only), exactly like tax
roles.

> **DELIVERED (batch 1):** role tokens and the pure policy below ship in
> `internal/authz/evidence_lifecycle_policy.go` +
> `authz/evidence-lifecycle-policy.ts` with the table-driven Go + TS tests
> (see the delivery-status note above). The blocker codes at the bottom of
> §8.3 belong to the store layer, delivered with the purge pipeline
> (batches 3–4).

### 8.2 Check order (frozen; first reason code wins)

```text
tenant → company scope → membership active → actor-kind deny (agent/system)
→ role deny-list (operational_accountant, *admin) → role allow
(default approver) → assurance ≥ standard → requester ≠ approver (SoD)
→ dual-approval config (category) → second approver role + distinct principal
```

**Blocker checks (§2 guard list) run BEFORE authorization** — a blocked request
never reaches the approval policy, so an authorized human cannot "approve
through" a blocker. Any attempt to express an override is rejected with
`BLOCKER_PRECEDES_APPROVAL`; there is no override field, flag or role.

### 8.3 Reason codes (frozen)

```text
AUTHORIZED                    TENANT_SCOPE_MISMATCH      COMPANY_SCOPE_DENIED
MEMBERSHIP_INACTIVE           ROLE_NOT_AUTHORIZED        ROLE_DENIED
ASSURANCE_TOO_LOW             APPROVER_IS_REQUESTER      DUAL_APPROVAL_REQUIRED
SAME_PRINCIPAL_SECOND_APPROVAL BLOCKER_PRECEDES_APPROVAL UNKNOWN_RETENTION_STATE
RETENTION_POLICY_AMBIGUOUS    RETENTION_NOT_DUE          HOLD_ACTIVE
PERIOD_CLOSED                 NOT_PURGEABLE              INVALID_TRANSITION
LIFECYCLE_VERSION_MISMATCH    ALREADY_DECIDED            IDEMPOTENCY_CONFLICT
REASON_REQUIRED               OBJECT_NOT_FOUND           POLICY_EVIDENCE_REQUIRED
```

Errors carry only identifiers and reason data — never object bytes or private
content (mirrors the `ENVELOPE_MISMATCH` rule).

> **Batch note:** the pure policy returns the first four new codes
> (`ROLE_DENIED`, `APPROVER_IS_REQUESTER`, `DUAL_APPROVAL_REQUIRED`,
> `SAME_PRINCIPAL_SECOND_APPROVAL`) plus the shared scope/membership/role/
> assurance codes; the blocker/state codes below
> (`BLOCKER_PRECEDES_APPROVAL` … `POLICY_EVIDENCE_REQUIRED`) were frozen for
> the store batches and shipped with the purge pipeline (batches 3–4).

## 9. Expected-version, idempotency and transaction ordering

Follows the frozen approval pattern ([contracts/approval.md](../../contracts/approval.md)):

- **Request**: `requestPurge({ objectId, expectedLifecycleHash, reason, requestId }, principal)`.
  The approver reviews H1 = the canonical lifecycle snapshot hash (§3.8);
  the approval event records `reviewedHash` (H1) and `resultingHash` (H2, after
  the transition). Any drift between what was reviewed and current state →
  `LIFECYCLE_VERSION_MISMATCH` carrying ONLY the two hashes.
- **Approve**: `approvePurge({ requestId, expectedLifecycleHash, reason, requestIdKey }, principal)` —
  same H1/H2 discipline; the second approval additionally carries the first
  approval's `resultingHash` as its reviewed context.
- **Execute**: `executePurge({ requestId, expectedApprovalHash, reason, executionId }, principalOrScheduler)` —
  `expectedApprovalHash` is the committed approval chain hash; execution
  re-verifies it before touching bytes.
- **Idempotency**: every command reserves `(tenant, requestId)` in
  `idempotency_keys` inside the transaction. Same requestId + same payload →
  replay (`idempotentReplay: true`, no new event/receipt); same requestId +
  different payload/principal → `IDEMPOTENCY_CONFLICT`.
- **Ordering** (every transition, one `BEGIN IMMEDIATE`):
  `idempotency → locked re-read → scope exactness → closed-period gate →
  retention resolution → hold scan → authz → expected-version compare →
  guarded state flip + event + projection + receipt(s) → completed reservation →
  COMMIT`. Concurrency: exactly one transition; losers → `ALREADY_DECIDED`.

## 10. Scope and closed-period rule

- **Scope-first, always.** Every lifecycle record carries the exact
  tenant/company/RUC/period tuple; reads filter scope before anything else
  ([contracts/scope.md](../../contracts/scope.md)). Institutional
  (cross-company) objects are **not purgeable** — `NOT_PURGEABLE`.
- **Closed-period gate**: purge is a period-scoped mutation of evidence
  availability, so the existing `assertPeriodWritable` invariant
  ([contracts/closing.md](../../contracts/closing.md)) applies: a
  `request_purge` or `execute` for an object whose period is closed is denied
  with `PERIOD_CLOSED` (carrying tenant/company/period + close memory id, never
  private content). The documented path for retention-driven purge of closed
  periods is the existing controller `ReopenPeriod` flow — a human, audited
  decision, never a silent bypass.
- **Decision**: the closed-period gate applies to purge because purge reduces
  evidence availability for exactly the records audit cares about most. The
  reopen cost for bulk old-evidence purge is accepted; a deployment may batch
  reopens with reasons, and every reopen is already receipt-covered
  (`memory_reopened`).

## 11. Physical purge semantics

Bytes live on the WORM filesystem; the SQLite transaction cannot include the
filesystem atomically. The design therefore uses a **two-phase, receipt-covered
execution protocol** that bounds the window and makes every state auditable:

1. **Intent** (transaction 1): re-run ALL blocker checks (§2 `execute` guards:
   retention state, active holds including post-approval holds, closed period,
   `expectedApprovalHash`), then write the `purge_intent` event + `purged`
   projection? **No** — the projection only flips in completion. Intent writes:
   `evidence_purge_executions` row `state='intent'` + `purge_intent` event +
   receipt, COMMIT. At this point NOTHING has been deleted and the object is
   still verifiable as `stored`.
2. **Byte removal** (outside SQL): read the exact content-addressed path,
   re-hash the bytes, and require `pre_removal_hash == objectId`; a mismatch
   ABORTS — the engine never deletes bytes that do not match the recorded hash.
   Then unlink the file (only the exact `rel_path`, no wildcards).
3. **Completion** (transaction 2): write `purge_executed` event + receipt and
   flip the projection to `purged` (terminal), mark the execution row
   `completed`, set `evidence_purge_requests.status='executed'`; COMMIT.

Failure semantics: if step 2 fails (or the process dies between 1 and 3), the
execution row remains `intent` and the projection stays `stored` — the doctor
and verification report `PURGE_EXECUTION_INTERRUPTED` as a finding, and a retry
runs a fresh execution row under the same request (idempotent by
`execution_id`). The engine NEVER claims `purged` unless the completion
receipt committed. The `intent→completed` window is the only moment where
bytes can be absent without the terminal state; it is explicitly surfaced, not
hidden.

**Executor**: human (default approver or dual second approver) by default; a
deployment-configured scheduler may invoke the same guarded store operation for
already-approved requests. The scheduler is never an approver — it cannot
create, approve or unblock anything; every execution is receipt-covered with a
principal snapshot of the executor.

## 12. Deterministic export bundle

`export lifecycle <scope>` produces a tenant-scoped, deterministic bundle
(delivered, batch 4 — this closes the deferred "export" stage of the threat
model; the delivered v0.7 slice had no export):

- `manifest.json` — canonical, sorted, self-hashing root: bundle version
  `evidence-export/v0.8.0`, scope tuple, generatedAt, counts, and the bundle
  hash (SHA-256 over the canonical manifest bytes).
- Object metadata rows (immutable `evidence_objects`), lifecycle events,
  holds, purge requests/approvals/executions, retention policy rows, and the
  full per-subject receipt chains (`evidence_object` + referenced memories).
- No embedded verification report: the bundle is re-validated offline by the
  separate pure verifier `VerifyEvidenceExportBundle` (batch 4, §13), never
  by a report inside the bundle; the §13.1 named layers are remaining scope.
- Deterministic ordering everywhere: subjectType → subjectId → event sequence;
  canonical JSON, fixed property order (Go↔TS byte-identical).
- **Purged objects export metadata + lifecycle + purge receipts ONLY** — never
  bytes. The manifest marks them `bytes: "purged"` with the `purge_executed`
  receipt hash. Export never resurrects removed bytes; the export itself emits
  NO receipt, and the bundle carries the per-subject receipt chains and is
  hash-pinned so a recipient can verify it offline.
- **Import/restore is OUT OF SCOPE**: `ExportEvidenceLifecycle` is a read-only
  query — no surface imports a bundle into another store.

## 13. Verification and doctor behavior

> **Delivered surface (batch 4):** the §13.2 documented-purge distinction in
> `verify object`, the §13.3 doctor counts/findings, and the pure offline
> export verifier. The four named §13.1 layers below are **remaining scope** —
> the documented design target, not implemented.

### 13.1 New verification layers (appended after the v0.7 object layers, stable order)

| Layer | Semantics |
| --- | --- |
| `retention policy` | The bound policy row exists, enabled, with jurisdiction/legislation/authority/source; the bound version matches the resolved chain; eligibility at bind time consistent with the stored snapshot. `unknown`/`unmanaged` → REPORTED (skipped with detail), never failed — legacy compatibility. |
| `lifecycle chain` | `evidence_lifecycle_events` ordered and contiguous per object; projection (`evidence_retention_state`) equals the last event's resulting state; every event's resulting_hash recomputes canonically; receipts exist for every event (atomicity proof). |
| `purge approval chain` | Approvals match their events and receipts (principal snapshots, hashes); requester ≠ approver; dual-approval configuration satisfied for the category; denied roles (`operational_accountant`, `*admin`, agent, system) absent from every decision. |
| `hold consistency` | Every blocking hold recorded in `evidence_holds` is reflected in `has_active_blocking_hold` at all relevant instants; holds placed after approval are detected and must have blocked execution. |

### 13.2 Modified WORM byte-integrity layer

`verify object` must now distinguish three outcomes for the stored bytes:

1. Bytes present and re-hash to the content address → **passed** (unchanged).
2. Bytes absent AND the executions ledger carries a receipt-covered purge
   authorization for the object (a completed execution whose `purge_executed`
   receipt committed, OR a live `intent` row) → the byte-integrity layer
   **passes with detail `documented purge (receipt-covered absence)`** —
   expected, NOT corruption.
3. Bytes absent WITHOUT such an authorization (including an
   `interrupted`-only history with no live intent) → **failed** —
   corruption/unexpected loss, fails closed, no silent repair (unchanged
   rule). A live `intent` without completion is the crash-recovery window:
   it is an authorized absence (reported as `documented_intent` by doctor,
   never failed), because the intent transaction committed receipt-covered.

### 13.3 Doctor

`doctor` adds: counts for all lifecycle tables; findings for `intent`/
`interrupted` execution rows (`PURGE_EXECUTION_INTERRUPTED`); orphan byte files
and retained files of purged objects (REPORTED findings, never deleted, never
repaired — doctor stays read-only evidence, per the v0.7.x hardening pattern).

## 14. Compatibility

- **Stores**: a v8 store migrates additively through v9→v12 (§4 + the batch
  notes) and remains fully readable; the binary fails closed on any other
  version. Rollback before v0.8 writes uses the normal backup; after v0.8
  rows exist, downgrade is unsupported (older binaries cannot preserve
  lifecycle records) — same policy as v0.6.
- **Legacy objects**: v8 objects without lifecycle rows are `unmanaged` —
  REPORTED at verification and by doctor, never failed, never silently
  converted. `unmanaged` is not purgeable.
- **Receipts**: verifiers accept v0.4–v0.7 payloads unchanged plus the
  additive v0.8.0 (hold acts) and v0.9.0 (purge acts) payloads; each
  `receipts` rebuild (v9/v10/v12) is byte-identical in layout for existing
  rows (staging copy+swap). No historical receipt is re-signed or backfilled.
- **Parity**: new fields on verification/doctor/export outputs are additive and
  `omitempty`; existing parity goldens remain byte-for-byte identical. The
  TypeScript mirror covers the contract-shared lifecycle surface: the
  extended receipt action vocabulary and the v0.8.0/v0.9.0 payload versions
  (`core/types.ts`, `core/receipt.ts`), the pure object-level hold model with
  byte-identical canonical hold JSON (`core/evidence-hold.ts`), and the
  lifecycle policy mirror (`authz/evidence-lifecycle-policy.ts`). The purge
  pipeline, the deterministic export bundle and the canonical lifecycle
  snapshot hash are Go-only; there is no TS store mirror for them.
- **Non-authorization boundary unchanged**: storing, binding, holding,
  approving and purging certify Engram's own state transitions — none of them
  authorizes an external business action.

## 15. Tests

Acceptance (behavior-level, following the repo's portable adversarial style):

1. Request → approve (single) → execute removes bytes, projection `purged`,
   metadata intact, `purge_executed` receipt on the object chain.
2. Dual approval: configured fiscal/material category requires second approver;
   `controller` and `tax_responsible` both satisfy; a second approval by the
   SAME principal fails `SAME_PRINCIPAL_SECOND_APPROVAL`.
3. Deny-list: `operational_accountant`, any `*admin` role, agent and system
   actor kinds fail `ROLE_DENIED`/`ROLE_NOT_AUTHORIZED` at every gate.
4. Blocker precedence: `UNKNOWN_RETENTION_STATE`, `HOLD_ACTIVE`, `PERIOD_CLOSED`
   and `LIFECYCLE_VERSION_MISMATCH` each fail request AND execution even with an
   otherwise fully authorized human; no override field exists.
5. Post-approval hold: approve, then place a legal hold → execute fails
   `HOLD_ACTIVE`; lift → execute succeeds.
6. Hash-before-unlink: corrupt the byte file before execute → abort, no
   deletion, `pre_removal_hash` mismatch recorded.
7. Crash protocol: kill between intent and completion → `intent` row +
   `PURGE_EXECUTION_INTERRUPTED` finding; retry with the same request completes
   exactly once (idempotent by `execution_id`).
8. Export determinism: two exports of the same scope are byte-identical;
   purged objects export metadata + receipts only.
9. Verification after purge: `verify object` passes with the documented-purge
   skip; missing bytes without a purge receipt fail.
10. Migration: v8→v9 rollback on mid-failure; fresh v9 DDL; v8 parity goldens
    unchanged; legacy objects `unmanaged` and reported, not failed; Go↔TS
    canonical lifecycle hash vectors byte-identical.
11. Idempotency/concurrency: replay returns `idempotentReplay: true` with no new
    event; two concurrent approvals produce exactly one transition and one
    `ALREADY_DECIDED`.

## 16. Non-goals and non-claims

This document does **not** claim, and v0.8 does **not** implement:

- **Any statutory retention period** for any jurisdiction, including the
  asserted SUNAT duration (unverifiable by the source check — §1); deployments
  MUST provide policy/jurisdiction evidence, and absence blocks.
- **Cloud/remote object storage**, OIDC/MFA production identity, SUNAT/ERP/SIRE
  integration, or OCR/content search over objects — all remain ROADMAP
  non-goals exactly as in v0.7.
- **Automatic purge or automatic execution without human approval** — no
  retention clock silently deletes; expiry/eligibility always surfaces for a
  documented human decision.
- **Deletion of audit state** — metadata, hash, links, events, receipts,
  eligibility and approvals are immutable; purge removes bytes only.
- **Legal compliance assertions** — receipts and verification certify Engram's
  own state transitions, never accounting, fiscal or legal correctness
  (repeated verbatim in every report: "Accounting correctness: NOT ASSERTED").
- **Bypass paths** — no override flag, no admin escalation, no "system
  maintenance" route around blockers.

## 17. Rollout

Release reads first against migrated v12 stores; legacy objects stay
`unmanaged` and visible. New writes opt in per object via `retention_bound`
(policy evidence required — the binding receipt is emitted with the first
purge request, batch 4). No automatic backfill: binding a retention state to
legacy objects is a documented policy decision, not an engine guess. The
implementation followed the file-level integration map pattern of
[fiscal-policy-memory-v0.6.md](fiscal-policy-memory-v0.6.md) (§7): pure policy
module + store transitions + verification layers + surfaces + TS mirror, each
batch closing with the docs/golden-freeze check. Gate: v1 criterion G-5
(delivered: hash + availability; this design closes retention/legal-hold/
export/purge).

## 18. Review checklist

- [ ] Every design choice traces to a governance-decision bullet (§1) or is
      explicitly marked as a decision.
- [ ] No section claims cloud, OIDC, SUNAT/ERP/OCR, statutory periods, or
      legal compliance — the non-goals list (§16) is complete.
- [ ] `UNKNOWN_RETENTION_STATE`, holds, closed period and version drift block
      BOTH request and execution; approval cannot override any blocker.
- [ ] Default approver is exactly `records_compliance_officer` /
      `tenant_records_owner`; the deny-list (`operational_accountant`, `*admin`,
      agent, system) precedes every allow.
- [ ] Dual approval is enforced for configured categories with
      `controller`/`tax_responsible`, distinct principals.
- [ ] Bytes are removed only at execution, only after hash verification, and
      only when a `purge_executed` receipt commits; interrupted executions are
      surfaced, not hidden.
- [ ] Metadata/hash/links/events/receipts/eligibility/approvals survive purge.
- [ ] The state machine, tables, migration, reason codes, and layers match the
      frozen conventions of the cited contracts.

## Next step

The v0.8 slice is delivered (batches 1–4, schema v12). Remaining scope,
documented per section, is: the §13.1 named lifecycle verification layers,
the deployment-configured scheduler executor surface (§11), and bundle
import/restore (§12 — the export is a read-only query). Re-run the threat
table (T-7) against the purge protocol before any production claim.
