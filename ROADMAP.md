# Drenyra Engram — Roadmap

> **Last updated:** 2026-08-07. Status: alpha (v0.7.0 local-first EvidenceObject slice delivered; v0.5.0 Close Intelligence released).
>
> **Version nomenclature (frozen):** `v0.3.0` is the Accounting Memory Kernel.
> The original vision's "V0.2 Evidence and Judgment" is NOT closed by it —
> adjudicated contradictions, cryptographic receipts and offline verification
> are Phase 4 (`v0.4.0`). The numbering is therefore: `v0.4.0` Evidence,
> Conflict and Judgment · `v0.5.0` Close Intelligence · `v0.6.0` Fiscal Policy
> Memory · `v1.0.0` Institutional Accounting Brain.

## Phase 0 — Identity (current)

- [x] Repository created with identity scaffolding (README, LICENSE, SECURITY, CONTRIBUTING, CODEOWNERS)
- [x] Contract index drafted (`contracts/`)
- [ ] Contract review and freeze: memory, scope, lifecycle, provenance
- [ ] Public roadmap and architecture published

## Phase 1 — Contracts (v0.1)

- [ ] Freeze `memory` v0.1 (observation model, topic keys, structured content)
- [ ] Freeze `scope` v0.1 (company/RUC/period scoping, scope-first search)
- [ ] Freeze `lifecycle` v0.1 (draft → reviewed → promoted → superseded, vigencia)
- [ ] Freeze `provenance` v0.1 (who/what/when/why, auditability, non-authorization boundary)
- [ ] Contract conformance test suite

## Phase 2 — Vertical slices from Drenyra

Extracted via vertical PRs and versioned releases, **not** a bulk move:

- [ ] Slice 1: mission summary + accounting memory search
- [ ] Slice 2: observation store with scope-first indexing
- [ ] Slice 3: relations + lifecycle + vigencia
- [ ] Slice 4: MCP server + CLI
- [ ] Drenyra and Drenyra Pi consume the first released version instead of internal implementations

## Phase 2 — Standalone Go engine (ADR-001) — COMPLETE (v0.2.0 released 2026-08-03)

- [x] **v0.2 foundation:** Go module (`github.com/arkelythex/drenyra-engram`), core types + validators (RUC 11 digits, period YYYYMM), lifecycle machine, SQLite store (modernc.org/sqlite, pure Go — immutable history + schema guards), scope-first search with the MANDATORY cross-tenant isolation property, and the CLI (`save | search | context | doctor`). 40 Go tests green; non-authorization boundary enforced (no authorize/approve/allow commands).
- [x] CLI polish: `compare`, `review`, `promote`, `supersede` commands (45 Go tests green; compare verdict matrix incl. supersedes-with-source-check)
- [x] MCP + HTTP surfaces (shared domain services `internal/server/api.go`; `mcp` stdio + POST /mcp; `serve` REST /v1 + token; 87 Go tests green)
- [x] Local/cloud sync — additive, provenance-preserving, conflict-visible
  reconciliation (`sync --from <src> --to <dst>`; full revision history +
  relations + audit trail; lifecycle via transition replay; divergent chains
  surfaced with `conflicts_with` relations, never silently resolved; cloud
  deferred per non-goals) — 104 Go tests green
- [x] Chain history surface (`GET /v1/chain` + `engram_chain` tool) — full
  revision history per (topicKey, scope); powers the Drenyra fiscal-memory
  adapter. 106 Go tests green; `go test -race ./...` clean.
- [x] **Released as v0.2.0** (GitHub release, static binaries for
  linux/darwin/windows x amd64/arm64, Go module go-gettable). First
  consumer: Drenyra adapter (observability read + mission write + fiscal
  memory) live-tested 12/12 against the released binary.

## Phase 3 — Accounting Memory Kernel (v0.3.0) — COMPLETE

- [x] AccountingMemory v2 model: 8 kinds, 6 statuses, 8 fiscal effects, triple
      timestamps, structured source, canonical content hash, materiality.
- [x] Mandatory human approval gate (`pending_review` → human `approve`).
- [x] Store schema v2 with additive v1→v2 migration, evidence/rule links.
- [x] 17 relations (accounting-evidence vocabulary).
- [x] Search filters (kind/status/fiscal effect) over scope-first isolation.
- [x] API: Approve/Reject/Void/Supersede/Judge/LinkEvidence/PeriodSummary.
- [x] 10 MCP `accounting_*` tools; CLI approve/reject/void/link-evidence/
      period-summary/timeline; HTTP approve/reject/void endpoints.
- [x] Kill demo: explainable period summary of account 4011 (280 Go tests).

## Phase 4 — Evidence, Conflict and Judgment (v0.4.0) — MILESTONE FROZEN

> Four vertical capabilities, in this order:
> `1. ApprovalPrincipal` → `2. adjudicable conflicts` → `3. Ed25519 receipts`
> → `4. offline verification`.

- [x] **1. ApprovalPrincipal autenticado** (ADR-003, v0.4.0 Step 1 — DONE): the
      API never accepts authorization via `{ "actorKind": "human" }` — the
      service derives the principal from the authenticated session (subjectId,
      tenantId, membershipId, companyScopes, roles, authenticationMethod,
      assuranceLevel, authenticatedAt). Command shape:
      `approveMemory({ memoryId, reason, expectedEnvelopeHash, requestId },
      authenticatedPrincipal)` — the principal is a SEPARATE verified argument,
      never part of the payload; `expectedEnvelopeHash` protects against
      approving a version different from the one the human reviewed; `requestId`
      gives idempotency by (tenant, requestId).
      Invariants: principal authenticated · tenant matches · membership
      active · company in scope · role authorized for the fiscalEffect ·
      status == pending_review · envelope current == envelope reviewed ·
      reason required. Atomic `BEGIN IMMEDIATE` transition, immutable
      `approval_events` (both reviewed/resulting envelope hashes), versioned
      policy `approval-policy/v0.4.0`, frozen error codes, surfaces:
      HTTP `/accounting/memories/{id}/approve` · MCP `accounting_approve`
      (fail-closed without session) · CLI `auth login` + authenticated
      `approve`. Implemented in commits 9d4211c..65af686.
- [x] **2. Conflictos adjudicables** (`AccountingJudgment` — DONE): an agent may
      PROPOSE a contradiction (`supports`/`contradicts`/`explains`/
      `reconciles`/`reverses`/`supersedes`) but never confirm it.
      `status: proposed → confirmed|rejected|superseded|withdrawn`; a
      confirmed judgment is immutable — a correction is a NEW judgment that
      supersedes it. First-class entity (schema v4 `judgments` + immutable
      events + idempotency), atomic adjudication (`BEGIN IMMEDIATE`, hash
      guard `expectedJudgmentHash`, `judgment-policy/v0.4.0` senior_accountant+
      standard assurance), legacy `API.Judge` caller-authority path closed,
      surfaces: HTTP `/accounting/judgments*` · MCP
      `accounting_judgment_*` (confirm/reject fail-closed without session) ·
      CLI `judge propose|confirm|reject|withdraw|show`. Contracts:
      contracts/judgment.md. Implemented in commits 0333a29..b49436f.
- [x] **3. Receipts Ed25519** (`SignedReceipt` — DONE): every immutable act
      emits a canonical, offline-verifiable receipt — `memory_recorded`,
      `memory_approved`, `memory_rejected`, `memory_voided`,
      `relation_confirmed`, `relation_rejected`, `evidence_linked`,
      `memory_superseded`. Envelope: `{ subjectType, subjectId, action,
      tenantId, companyId, fiscalPeriodId, payloadHash, previousReceiptHash,
      principalId, membershipId, policyVersion, algorithm (Ed25519), keyId,
      signature, issuedAt }`. An approval signs the reviewed envelopeHash (H1)
      + resultingEnvelopeHash (H2) + principal identity + action + reason +
      policyVersion + timestamp. Atomic emission inside each act's transaction
      (schema v5 `receipts` immutable + `signing_keys` public-only); private
      keyring in user-only file (0600), key lifecycle (`keys init|show|rotate`),
      per-subject `previousReceiptHash` chain; Go signs / TS verifies and TS
      canonicalizes / Go verifies (golden vector). Receipt integrity never
      implies accounting correctness. Contracts: contracts/receipts.md.
      Implemented in commits 5e07d90..1eb9055.
- [x] **4. Verificación offline** (`verify memory|judgment|receipt` — DONE):
      read-only over the local store, answering per layer — Payload
      canonicalization, Envelope integrity, Signature, Signing-key validity,
      Principal provenance, Tenant/company scope, Supersession chain, Evidence
      availability, Rule availability — and ending with **"Accounting
      correctness: NOT ASSERTED"** (a valid signature proves nobody altered the
      act, never that the professional decision is correct). Pure layer logic
      in internal/core with an exact TS mirror + shared parity fixture; CLI
      `verify memory|judgment|receipt` (exit 0/1/2); AC7 (removed evidence
      detected via envelope recompute) and AC12 (the NOT ASSERTED conclusion on
      every report) proven. Contracts: contracts/verification.md. Implemented in
      commits c1ddfee..36b39f1.

    > **Milestone v0.4.0 COMPLETE** — all four verticals (ApprovalPrincipal
    > autenticado, Conflictos adjudicables, Receipts Ed25519, Verificación
    > offline) and all 12 acceptance criteria closed.
    > See contracts/approval.md, contracts/judgment.md, contracts/receipts.md,
    > contracts/verification.md.
- [x] **Acceptance criteria:** all 12 v0.4 criteria are closed; see the four contracts and the milestone implementation commits above.
      1 agent proposes a contradiction but cannot confirm it · 2 an
      authenticated human without company access cannot approve · 3 an
      authorized accountant approves exactly the envelope reviewed · 4 the
      memory changes between review and approval → optimistic conflict · 5 the
      approval emits an offline-verifiable Ed25519 receipt · 6 a modified
      signature fails · 7 a removed evidence is detected by verification · 8 a
      confirmed judgment can be superseded, never edited · 9 Go signs and TS
      verifies · 10 TS canonicalizes and Go verifies · 11 an approved
      superseded memory produces a new pending_review revision · 12 the
      verifier never presents cryptographic integrity as accounting
      correctness.
- [x] **Implementation sequence:** ApprovalPrincipal, authorization, optimistic concurrency, judgment lifecycle, Ed25519 receipts, key lifecycle, offline verification, Go↔TS vectors, CLI surfaces, and contract documentation are complete for v0.4.
      feat(authz) authorize by tenant/company/role/fiscalEffect ·
      feat(concurrency) approve against expected envelope hash · feat(judgment)
      proposed/confirmed/rejected lifecycle · feat(receipts) canonical signed
      action envelope · feat(keys) signing-key lifecycle + key IDs ·
      feat(verify) memory/judgment/receipt offline · test(protocol) Go↔TS
      signature golden vectors · feat(cli) judgment + verification commands ·
      docs freeze v0.4 contracts and threat model.
- [x] Evidence object store references (XML/PDF/CDR beyond refs) — **local-first
      slice delivered as v0.7.0** (see Phase 6b): content-addressed WORM object
      bytes, schema-v8 metadata, `object_stored` receipts, scoped store/get,
      object-level rehash availability verification, CLI/HTTP/MCP surfaces.
      Retention/legal-hold/export/purge/cloud remain DEFERRED.
- [ ] Materiality-aware period views

## Phase 5 — Close Intelligence (v0.5.0) — COMPLETE

- [x] **Monthly close memory (cierre) with pending items**: `CreateClose` builds
      a `closing/CIERRE-YYYYMM` summary memory (kind=summary, fiscalEffect=closing,
      pending_review, month-end effectiveAt, frozen CloseSnapshot with counts /
      signed-cent totals / pending-item digest / summaryHash). Controller
      approval projects `period_closures` and **blocks all period-scoped
      mutations** (`assertPeriodWritable` → PERIOD_CLOSED, inside the write
      transaction) until an explicit controller `ReopenPeriod` (immutable
      events + `memory_reopened` receipt). Receipts `memory_closed` /
      `memory_reopened`. Contracts: contracts/closing.md.
- [x] **Reconciliations as first-class** (`reconciles` relation): adjudicated
      `Reconciliation` entity mirroring the judgment pattern — propose/confirm/
      reject/withdraw/supersede, `ComputeReconciliationHash` guard,
      `reconciliation-policy/v0.5.0` (controller), confirmed projects the
      `reconciles` observation edge, receipts `reconciliation_confirmed` /
      `reconciliation_rejected`, cross-period entities keep fiscalPeriodId NULL.
      Contracts: contracts/reconciliation.md.
- [x] **Period-over-period comparison**: `ComparePeriods` (pure read) with
      chain deltas (new/removed/changed by topic key, period-stripped content
      comparison), status changes, pending-item delta by chain topic, close
      state. Contracts: contracts/period-comparison.md.
- [x] **Automatic agent context on session start**: `DRENYRA_DEFAULT_SCOPE`
      (exact scope, never inferred) → `initialize._meta["drenyra/currentContext"]`
      + `accounting_current_context` tool; PeriodSummary extended with
      latestClose/closureState/pendingItems; stale MCP lifecycle text fixed.

    > Implemented in commits 3eab1e1..2fe6a7e.

## Phase 6 — Fiscal Policy Memory (v0.6.0)

- [ ] Versioned rules with temporal vigencia
- [ ] Jurisdiction dimension (Perú → LATAM)
- [ ] Regulatory-change impact reconstruction
- [ ] Rule used in a historical decision, reconstructible

## Phase 6b — Evidence Objects (v0.7.0) — DELIVERED (local-first slice)

> The EvidenceObject local-first slice (schema v8 on top of the v0.6 rule
> foundation in schema v7). Deferred production stages are listed explicitly
> below — they are NOT implemented.

- [x] **Local content-addressed WORM object bytes**: layout
      `objects/<sha[0:2]>/<sha[2:4]>/<sha256>`; the object id IS the SHA-256 hex
      of the bytes (`ComputeObjectID`); identical bytes → duplicate store is a
      NO-OP (`created=false`, no receipt); no overwrite/delete API; path escape
      fails closed as corruption; no silent repair
      — [internal/store/object_store.go](internal/store/object_store.go).
- [x] **Schema v8 immutable object metadata**: `evidence_objects` table with
      no-update/no-delete triggers and scope index; one-transaction v7→v8
      migration (receipts table copied+swapped to extend the subject CHECK to
      `evidence_object`, the action CHECK to `object_stored`, and add the typed
      FK) — [internal/store/store.go](internal/store/store.go).
- [x] **`object_stored` receipt**: emitted atomically inside the store
      transaction for genuinely new objects; subjectType `evidence_object`
      — [contracts/receipts.md](contracts/receipts.md).
- [x] **Scoped store/get**: exact tenant/company/RUC/period scope (institutional
      objects rejected); reads are scope-first — exact scope must match
      — [contracts/scope.md](contracts/scope.md).
- [x] **Object-level rehash availability verification**: `verify object` runs the
      six receipt layers + principal provenance + WORM byte integrity (stored
      bytes re-hash to the content address); `verify memory` reports object
      availability for refs that resolve to stored objects (legacy refs stay
      backward compatible, reported never failed)
      — [contracts/verification.md](contracts/verification.md).
- [x] **Surfaces**: CLI `object store|get` + `verify object`; HTTP
      `POST /accounting/objects`, `GET /accounting/objects/{objectId}`; MCP
      `accounting_object_store`, `accounting_object_get`.
- [x] **Go/TS coverage**: Go suite green (`internal/core`, `internal/store`,
      `internal/server`, `cmd`) and TypeScript suite green (277 tests / 20 files
      incl. `core/evidence-object.ts` mirror + `store/memory-store.ts` v0.7
      mirror).

**Explicitly DEFERRED (not implemented):** retention expiry (no retention
clock), legal hold, export, purge/deletion, cloud/remote object storage, OCR
or content search over objects, SUNAT/ERP object ingestion, and production
object-store operations (backup/restore drills, encryption-at-rest/TDE,
recovery objectives). Architecture: docs/architecture/evidence-object-v0.7.md;
threat model: docs/security/evidence-lifecycle-and-threat-model.md.

## Phase 7 — Institutional Accounting Brain (v1.0.0)

- [ ] Accounting firms: shared memory with controlled sync
- [ ] Authorized anonymized cross-company learning
- [ ] Full knowledge graph + pattern detection
- [ ] Specialized agents + complete audit

## Non-goals (for now)

- Authorization engine (that is `arkelythex/drenyra-ai` gates + human approval)
- Cloud offering (deferred to `arkelythex/drenyra-cloud`) — including
  cloud/remote **object** storage for evidence objects
- PostgreSQL in this repo (local-first; PostgreSQL is the Drenyra ecosystem's
  authoritative store, ADR-002)
