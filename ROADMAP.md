# Drenyra Engram — Roadmap

> **Last updated:** 2026-08-08. Status: alpha (v0.7.0 local-first EvidenceObject slice delivered; v0.5.0 Close Intelligence released; first OIDC production identity slice implemented in the working tree, unreleased).
>
> **Version nomenclature (frozen):** `v0.3.0` is the Accounting Memory Kernel.
> The original vision's "V0.2 Evidence and Judgment" is NOT closed by it —
> adjudicated contradictions, cryptographic receipts and offline verification
> are Phase 4 (`v0.4.0`). The numbering is therefore: `v0.4.0` Evidence,
> Conflict and Judgment · `v0.5.0` Close Intelligence · `v0.6.0` Fiscal Policy
> Memory · `v1.0.0` Institutional Accounting Brain.

## Program alignment

Drenyra Engram participates in the [Drenyra Dominion Program](https://github.com/arkelythex/drenyra-ai/tree/main/openspec/programs/drenyra-dominion), the federated master that fixes vision, authority, contracts, dependencies, gates, and sequencing across the ecosystem. This roadmap is the repository's own implementation sequence; the program's vertical SDDs frame the ecosystem's coordinated release:

| Program SDD | Wave | Alignment with this roadmap |
| --- | --- | --- |
| [SDD-080 — Engram Institutional Memory](https://github.com/arkelythex/drenyra-ai/tree/main/openspec/programs/drenyra-dominion/sdds/sdd-080-engram) | 2 — Fiscal intelligence | Institutional memory that informs but never authorizes — the phases below deliver its slices (memory, receipts, offline verification, close intelligence, fiscal policy memory, evidence objects, review workspace) |
| [SDD-050 — Peruvian Monthly Close](https://github.com/arkelythex/drenyra-ai/tree/main/openspec/programs/drenyra-dominion/sdds/sdd-050-monthly-close) | 3 — Flagship product | Consumes Engram's prior-decision context for the monthly close journey — memory informs, never authorizes, and is never evidence |

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

## Phase 6 — Fiscal Policy Memory (v0.6.0) — DELIVERED (2026-08-10)

- [x] **Versioned rules with temporal vigencia** — rules remain `kind=rule`
memories; the (topicKey, exact Scope) revision chain IS the version
chain; `Validity{EffectiveAt, ExpiresAt, Source}` half-open windows;
superseded revisions stay visible and historically valid.
- [x] **Jurisdiction dimension (Perú → LATAM)** — `policyRule{jurisdiction,
legislation, authority, tags}` on rule memories (syntax-validated, never
geopolitical truth), self-describing hash contribution (legacy memories
hash byte-identically), schema v7 `policy_rule_json`.
- [x] **Structured rule links (Batch 2)** — `SaveInput.ruleLinks[]` pins the
EXACT rule revision at the decision instant (schema v14:
`rule_links.version` + `effective_at` + `idx_rule_links_ref`); atomic
save integration; `AddRuleLinkVersion` post-save; idempotency +
`RULE_LINK_VERSION_CONFLICT`; closed-period gate; TS mirror.
- [x] **Regulatory-change impact reconstruction (Batch 3)** — `RuleImpact`
reverse read (structured pins UNION legacy bare refs via json_each,
tenant-visible, company-scoped with a scope selector,
`RULE_CHAIN_AMBIGUOUS`); overlap classification against the selected
changed revision's window; CLI `rule show|history|impact`, MCP
`accounting_rule_show|history|impact`, HTTP
`GET /accounting/rules/{topic}(/history)(/impact?revision=N)`.
- [x] **Rule used in a historical decision, reconstructible (Batch 4)** —
pure `ResolveRuleVersionFromChain` (RULE_NOT_IN_FORCE /
RULE_VIGENCIA_OVERLAP / RULE_VERSION_MISMATCH / RULE_STATUS_INVALID),
`rule version/vigencia` verification layer + `ruleVersions` traces
(legacy refs = skipped, invalid pin fails the layer, NOT ASSERTED
preserved), Go↔TS parity (core/rules.ts mirror + mirrored tests).

Design: docs/architecture/fiscal-policy-memory-v0.6.md · contracts:
memory.md (structured links), verification.md (rule version/vigencia layer).
Tests: Go 9/9 packages, TS 360 tests, typecheck clean.

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
      `internal/server`, `cmd`) and TypeScript suite green (360 tests / 25 files
      incl. `core/evidence-object.ts` mirror + `store/memory-store.ts` v0.7
      mirror).

**v0.8.0 evidence lifecycle (DELIVERED after this list — schema v12):**
versioned retention policies (`PutRetentionPolicy`/`Resolve`/`Evaluate`),
object-level legal holds (place/lift/list), the approved purge pipeline
(request → approval → physical execution), and the deterministic lifecycle
export — docs/architecture/evidence-lifecycle-v0.8.md. As of v0.8 the
deferral list below is REDUCED to the items still genuinely open.

**Explicitly DEFERRED (not implemented):** cloud/remote object storage, the
scheduler executor for lifecycle expiry, OCR or content search over objects,
SUNAT/ERP object ingestion, and production object-store operations
(backup/restore drills, encryption-at-rest/TDE, recovery objectives).
Architecture: docs/architecture/evidence-object-v0.7.md;
threat model: docs/security/evidence-lifecycle-and-threat-model.md.

## Phase 6c — Review Workspace (v0.9.0) — DELIVERED (engine-side slice)

> The professional review layer over the existing `pending_review` state
> (design brief §6; docs/architecture/review-workspace-v0.9.md). Headless and
> independently deployable; the professional Web UI belongs to Drenyra and is
> NOT part of this repository. Works without Phase 6 (rule refs are
> best-effort). Schema v13.

- [x] **Queue** — `ListReviewQueue`: pending_review of an EXACT company scope,
deterministic ordering (materiality rank DESC → recordedAt ASC → rowid),
bounded pagination (default 50, max 200), ref counts + open-judgment
counts per item. Scope isolation enforced server-side.
- [x] **Review detail** — `ReviewDetail`: pending revision + structured
content diff vs chain predecessor + evidence WORM availability + rule
refs/vigencia best-effort + open proposed judgments touching the memory +
fresh envelope H1 + boundary notice ("signature integrity is not
accounting correctness").
- [x] **Decisions (authenticated, idempotent, hash-guarded)** — `approve`
extended (SoD + review checks for material/critical); `reject` NEW
authenticated path with reason policy + idempotency + H1 guard + reason in
receipt (payload v0.10.0); `return` NEW non-terminal status (`returned`,
pending_review → returned) with required reason + `memory_returned`
receipt; agent Save on a returned memory re-enters pending_review.
- [x] **Anti-rubber-stamp** — SoD fail-closed (SOD_VIOLATION);
REVIEW_CHECKS_REQUIRED for material/critical; proposal change invalidates
the review (ENVELOPE_MISMATCH); per-principal velocity alerts
(review_velocity_events, observable, non-blocking).
- [x] **Surfaces** — MCP `accounting_review_queue|detail|reject|return`;
HTTP `GET /accounting/review/queue`, `GET /accounting/review/{memoryId}`,
`POST /accounting/memories/{memoryId}/reject|return`;
CLI `review queue|detail|reject|return`. Contracts extended:
lifecycle.md (`returned`), receipts.md (`memory_returned` + reject
payload v0.10.0), approval.md (SoD + review-checks clauses). Go↔TS golden
vectors: sod-policy, review-checks, memory-returned-receipt-v10.
- [x] Tests: Go suite green (9/9 packages), TS suite green (352 tests),
typecheck clean.

## Phase 6d — Reconstructible-close demo fixture (DELIVERED 2026-08-10)

> The deterministic fictional drill proving the design brief §7.1 promise
> (balance → ledger ref → entry → evidence object → rule + vigencia →
> judgment → approval → offline verification) without the original agent.
> Test: `TestReconstructibleCloseFixture`; docs/demo/reconstructible-close.md.
> Advances gate G-2 to PARTIAL (real/anonymized close still required). The
> drill also fixed a verifier gap: `evidence_linked` provenance now resolves
> by (memory, timestamp, ref) — same-second XML+CDR pairs are unambiguous.

## Phase 6f — Comprobante ingestion adapter contract (DELIVERED 2026-08-10)

> The design brief §7.3 ADAPTER CONTRACT: `core.ParseComprobanteXML`
> (minimal UBL 2.1 invoice parsing — serie/numero, emitter RUC with the SUNAT
> mod-11 checksum, issue date, PayableAmount in whole cents; `<Invoice>` admits
> ONLY Catálogo 01 codes 01/03 — notas 07/08 are CreditNote/DebitNote roots),
> `core.ParseCDRXML` (response code),
> `server.IngestComprobante` (parse + WORM object store, content-addressed
> duplicate NO-OP), CLI `object ingest <file>`. The explicit
> NON-INTEGRATION boundary: NO SUNAT credentials, NO retries, NO outage/
> response-retention, NO source authority — production SUNAT/ERP ingestion
> remains a Gate 0 decision (G0-5: CDR/XML first) and is NOT implemented.
> Advances gate G-3 (adapter contract defined); the fixture demo docs
> updated. Tests: core parser (valid/failure matrix) + server end-to-end.

## Phase 6e — Search baseline benchmark (DELIVERED 2026-08-10)

> The deterministic search benchmark (design brief §8): a 25,500-memory
> synthetic corpus (RucA 25k + RucB 500), 203 labeled queries + 14
> cross-tenant probes with a COMPUTED ground truth, and the harness
> internal/search/bench. Results: Recall@10 0.931, MRR@10 0.931, warm p95
> 31ms, leakage exactly 0, deterministic ordering, adversarial-safe — the
> baseline MEETS every §8.3 target. **FTS5/BM25 NOT adopted** (§8.4: no
> quality gap to justify it; standard BM25 would not close the one weakness
> — typo tolerance 0.58 — which needs a trigram tokenizer and is tracked as
> a follow-up if it becomes a product requirement). Report:
> docs/benchmark/search-baseline-v0.1.md; audit Block AA → PARTIAL.

## Phase 6g — v1-readiness engineering evidence (DELIVERED 2026-08-11, SDD v1-readiness)

> The SDD change v1-readiness (openspec/changes/v1-readiness, archived) delivered the
> implementable v1.0-gate engineering evidence:
>
> - **G-6 (resilience drills):** Go fuzz targets (comprobante XML, receipt payload
>   canonicalization, search tokenizer) + 21 committed seeds + `make fuzz-ci` (3×30s
>   bounded CI); doctor SQLite health checks (quick/integrity drill-only/
>   foreign-key/cell-size); copy-only corruption drill (marked copy, detection-
>   required, STORE_WRITE_FROZEN retry-proof latch, byte-preserved evidence); restore
>   drill (VACUUM INTO + 4-check verify + 6-case negative matrix). The fuzz harnesses
>   found and fixed a real parser bug (silent trailing-garbage amount parsing).
> - **G-7 (key-compromise response):** docs/security/key-compromise-response.md
>   (NIST SP 800-57, 8 steps) + gap analysis proving implementation == contract +
>   the FZ-3 cutoff boundary matrix as permanent regression. HSM/KMS + compromise-
>   response drill remain open (parent-owned).
> - **G-10 (reconstructibility North Star):** the read-only reconstructibility
>   surface (CLI/MCP/HTTP) scoring material decisions reconstructible to evidence +
>   rule + approval, with frozen eligibility/classifier semantics + golden Go↔TS
>   vectors. Engineering baseline + instrumentation delivered; the customer-observed
>   target remains parent-owned.

## Phase 6h — Production identity — OIDC first slice (implemented, unreleased)

> First production identity slice: stateless OpenID Connect access-token
> validation on the HTTP surface. Implemented in the working tree (unreleased);
> remaining identity work is listed explicitly and is NOT implemented.
> Design: [docs/architecture/oidc-access-token-identity.md](docs/architecture/oidc-access-token-identity.md).

- [x] **Stateless RS256 access-token validation** — `alg` pinned to RS256 (no
      algorithm confusion), `kid` resolved from a cached JWKS (one unknown-kid
      refresh, bounded fetch), exact `iss` and `aud`, required `sub` plus
      tenant/company custom claims, `exp`/`nbf`/`iat` with bounded clock skew;
      raw tokens never persisted, logged or hashed.
- [x] **DB membership/scope cross-check** — verified `(sub, tenant, company)`
      must exist in `memberships` (`LookupMembershipByScope`); missing →
      `PRINCIPAL_INVALID`, inactive → `MEMBERSHIP_INACTIVE`. Claims alone never
      mint membership.
- [x] **Fail-closed configuration** — partial or invalid `DRENYRA_OIDC_*` set
      aborts `serve`; OIDC disabled by default.
- [x] **Standard assurance only** — no ACR/MFA elevation; `acr` and `amr`
      ignored; no ID tokens, browser/refresh flows, or user provisioning.
- [ ] **Remaining identity work (not implemented):** MFA/ACR assurance
      elevation; token revocation beyond DB membership; proactive TTL-based
      JWKS refresh; signed service assertions; user provisioning; HSM/KMS key
      management. G-4 (v1 gate) stays PARTIAL until these land.

    ## Phase 7 — Institutional Accounting Brain (v1.0.0)
    
    - [ ] Accounting firms: shared memory with controlled sync
    - [ ] Authorized anonymized cross-company learning
    - [ ] Full knowledge graph + pattern detection
    - [ ] Specialized agents + complete audit

    ## SDD-060 — tenant operator surface (DELIVERED, 2026-08-19)

    > SDD-060 (Drenyra Engram, DRAFT) Fases 1 y 3: the tenant-scoped operator CLI
    > surface. Phase 5 (LATAM namespacing) stays deferred per the SDD itself; the
    > encryption slices (at-rest por tenant, sync) land with the Unit C change.

    - [x] **`tenant list` (Fase 1)** — operator enumeration: organizations,
          companies, periods, counts — ids/counts only, never per-tenant content.
    - [x] **`tenant consolidate` (Fase 3)** — topic-key drift detection within one
          RUC (canonical fold with Go↔TS golden parity); `--apply` merges drifted
          chains into the canonical chain via the audited supersede path
          (memory_superseded receipts + transition log); dry-run default (ZERO
          writes); adversarial cross-RUC isolation tested.
    - [x] **Cifrado at-rest por tenant + sync encryption** — opt-in via
      `DRENYRA_ENCRYPTION_MASTER_KEY` (per-tenant HKDF-derived keys, AES-256-GCM,
      schema v15 additive, fail-closed reads) + sync encryption-mismatch guard
      (source encrypted → plaintext sink refused). Legacy rows readable; default OFF.

## Non-goals (for now)

- Authorization engine (that is `arkelythex/drenyra-ai` gates + human approval)
- Cloud offering (deferred to `arkelythex/drenyra-cloud`) — including
  cloud/remote **object** storage for evidence objects
- PostgreSQL in this repo (local-first; PostgreSQL is the Drenyra ecosystem's
  authoritative store, ADR-002)
