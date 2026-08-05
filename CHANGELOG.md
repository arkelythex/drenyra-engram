# Changelog

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents; no float is ever used for money; version/sequence numbers are JSON integers, never floats.

All notable changes to Drenyra Engram will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to the version policy in [RELEASING.md](RELEASING.md).

## v0.4.0 — Evidence, Conflict and Judgment (unreleased)

### Step 1 — ApprovalPrincipal autenticado (DONE)

Approval no longer trusts the text `"human"`: it derives an authenticated,
pre-verified principal from the session and authorizes with a versioned,
reproducible policy against the exact envelope version that was reviewed.

- **Principal is never caller-declared**: `approveMemory({ memoryId, reason,
  expectedEnvelopeHash, requestId }, authenticatedPrincipal)` — the principal
  is a separate verified argument built only inside `internal/auth` via
  `Resolver.Authenticate`; no public arbitrary-input constructor. HTTP body,
  MCP arguments and CLI flags can never supply `actorKind`/`subjectId`/`roles`
  (strict surfaces reject them with `DisallowUnknownFields`).
- **Two envelope hashes**: status participates in the envelope hash, so
  approving changes it. The immutable `approval_events` row records
  `reviewedEnvelopeHash` (H1, what the human examined) AND
  `resultingEnvelopeHash` (H2, the approved state). A memory that changed
  after review (evidence/rule linked, status moved) fails with
  `ENVELOPE_MISMATCH` carrying only the two hashes.
- **Atomic transition**: one SQLite `BEGIN IMMEDIATE` transaction (write
  intent before race-sensitive reads) — idempotency resolution → locked
  re-read → scope/status checks → fresh H1 recompute → pure policy → guarded
  status flip + H2 → immutable event → completed reservation. Concurrent
  approvals produce exactly ONE transition; retries replay the committed
  result (`idempotentReplay: true`).
- **Versioned policy `approval-policy/v0.4.0`** (`internal/authz`, pure): base
  role matrix by fiscal effect, declared `materialityLevel` raises the
  accounting ladder, minimum assurance `standard` (`strong` for
  `sunat_filing`); frozen reason codes and check order.
- **Identity storage (schema v3, additive migration)**: `companies`,
  `memberships`, `membership_roles`, `sessions` (token hashes only),
  `approval_events` (immutable triggers), `idempotency_keys`;
  `observations.materiality_level`.
- **Surfaces**: HTTP `POST /accounting/memories/{id}/approve` (legacy route
  compiled but disabled by default, removed in v0.5.0) · MCP
  `accounting_approve(memory_id, expected_envelope_hash, reason, request_id)`
  fails closed `AUTHENTICATION_REQUIRED` without a session binding · CLI
  `auth login` + `auth seed-local-dev` (DRENYRA_ENV=local_dev only) +
  authenticated `approve` (caller-supplied `--actor` removed).
- **Parity**: shared golden protocol extended with a `contract`
  discriminator; six new vectors (authorized/unauthorized closing,
  cross-tenant, stale-envelope, approved-result-envelope,
  canonical-principal-role-order) pass identically in Go and TS.

### Frozen decisions

- The **principal never travels in the payload** (ADR-003 corrected command
  shape): `approveMemory` takes the principal as a separate verified
  argument. External JSON declaring `{ actorKind, subjectId, roles }` is
  rejected, not ignored.
- **Service assertions are opaque stored bearer credentials** (token hash in
  `sessions`, `authentication_method='service_assertion'`); no self-declared
  JWT claims until a signed-assertion trust contract exists. OIDC recognized
  but not resolvable in Step 1.
- **Materiality is a declared level** (`materialityLevel`: normal | material |
  critical, set by the writing agent); the policy never reinterprets the
  `Materiality *int64` threshold field.
- **Legacy HTTP approval route**: compiled for one release, disabled by
  default, removed in v0.5.0.

### Step 2 — Conflictos adjudicables / AccountingJudgment (DONE)

An agent may PROPOSE a contradiction but never confirm it. Only an
authenticated principal adjudicates; a confirmed judgment is immutable and a
correction is a NEW judgment that supersedes it.

- **First-class entity**: `AccountingJudgment` (schema v4 `judgments` +
  immutable `judgment_events` + `judgment_idempotency_keys` +
  `judgment_relations`; additive v3→v4 migration, fail-closed).
  `proposed → confirmed|rejected|withdrawn|superseded`;
  `confirmed → superseded` only. Confirmed rows are immutable (SQLite
  triggers) — the only legal update is the atomic confirmed→superseded
  routing of a correction.
- **Authority never caller-declared**: agents/systems propose and withdraw
  (provenance only — the same proposer identity is enforced); only a
  `VerifiedApprovalPrincipal` confirms/rejects under the frozen
  `judgment-policy/v0.4.0` (senior_accountant minimum via ladder, assurance ≥
  standard, exact tenant, company in scope).
- **Atomic adjudication**: one `BEGIN IMMEDIATE` transaction — idempotency →
  locked re-read → status → fresh `ComputeJudgmentHash` vs
  `expectedJudgmentHash` (mismatch carries only the two hashes) → pure policy
  → guarded status flip + immutable event + relation projection → completed
  reservation. Two concurrent confirms produce exactly one transition.
- **Corrections**: a new proposed judgment with `predecessorId`; confirming it
  atomically supersedes the confirmed predecessor and routes readers
  (`judgment_relations` + `JudgmentSuccessorOf`).
- **Confirmation does not approve**: the related observations keep their
  status — adjudication and approval are separate acts (contracts/approval.md).
- **Legacy gap closed**: `accounting_judge` (caller-supplied `actorId`) is
  REMOVED from MCP; `API.Judge` fails closed `AUTHENTICATION_REQUIRED` and no
  longer writes (removed in v0.5.0).
- **Surfaces**: HTTP `POST /accounting/judgments` + `/confirm` + `/reject` +
  `/withdraw` (strict bodies, Idempotency-Key) · MCP
  `accounting_judgment_propose/confirm/reject/withdraw` (confirm/reject fail
  closed without a session) · CLI `judge propose|confirm|reject|withdraw|show`
  (confirm/reject bound to the 0600 auth session).
- **Parity**: golden protocol `contract: "judgment"` — five vectors
  (proposed-confirmed, agent-cannot-confirm, cross-tenant-adjudicator,
  superseded-judgment-corrects-confirmed, immutable-confirmed) pass
  byte-identically in Go and TS.

### Step 3 — Receipts Ed25519 (DONE)

Every immutable act emits a canonical, offline-verifiable Ed25519 receipt,
atomically inside the act's transaction.

- **Canonical signed envelope** (`SignedReceipt`): subjectType, subjectId,
  action, tenantId, companyId, fiscalPeriodId, payloadHash,
  previousReceiptHash, principalId, membershipId, policyVersion, algorithm,
  keyId, signature, issuedAt. Byte-identical canonicalization in Go and
  TypeScript (compact JSON, fixed property order, no HTML escaping); an
  approval signs reviewedEnvelopeHash (H1) + resultingEnvelopeHash (H2) +
  principal identity + action + reason + policyVersion + timestamp.
- **8 acts covered**: memory_recorded, memory_approved, memory_rejected,
  memory_voided, relation_confirmed, relation_rejected, evidence_linked,
  memory_superseded — each emitted inside its own transaction (signing failure
  rolls back the act), with the transaction's captured timestamp.
- **Per-subject chain**: `previousReceiptHash` = ReceiptHash of the prior
  receipt of the same subject; genesis empty. The writer lock prevents chain
  forks.
- **Key lifecycle** (schema v5): private seed in a user-only file (0600,
  `keys init|show|rotate`); SQLite stores public keys only (`signing_keys`,
  revocation one-way); `keyId = ed25519:<sha256(pubkey)>`; rotation is
  explicit; revoked keys never sign; historical acts are never backfilled.
- **Parity**: golden vector `receipt-ed25519-v1.json` proves Go signs / TS
  verifies AND TS canonicalizes/signs / Go verifies (RFC 8032 deterministic
  Ed25519; Node key derivation via JWK import, byte-identical to Go).
- **Boundary**: receipts prove an act happened unaltered — they NEVER imply
  accounting correctness. Cross-product authorization gates remain in
  `drenyra-ai`; Engram's act receipts are its own immutable record
  (ecosystem-boundaries.md and trust-model.md reconciled).

## v0.3.0 — Accounting Memory Kernel (released)

> **Nomenclature note:** the original vision's "V0.2 Evidence and Judgment" is
> NOT closed by this release — adjudicated contradictions, cryptographic
> receipts and offline verification are `v0.4.0` (Evidence, Conflict and
> Judgment). The numbering continues: `v0.5.0` Close Intelligence, `v0.6.0`
> Fiscal Policy Memory, `v1.0.0` Institutional Accounting Brain.

### Frozen decisions (v0.3.0 review round 2)

- **Evidence/rule refs are SETS**: `canonicalRefs` (sort + unique) runs before
  the envelope hash in Go and TS — `[XML, CDR]` and `[CDR, XML]` now produce
  the SAME envelope hash. Two runtimes loading the same links in different SQL
  orders can no longer diverge. When ordinal or role matters, an explicit
  EvidenceLink entity is the vehicle, not a bare ref list. Golden vectors
  `evidence-order-1/2` were re-pinned to the same envelope.
- **Vigencia provenance is recorded**: `Validity.Source` distinguishes
  `"declared"` (written explicitly by a v2 caller) from
  `"migrated_from_effective_at_v1"` (inferred during the v1→v2 migration — the
  v1 `effective_at` doubled as the vigencia start). An audit never mistakes an
  inferred vigencia for originally confirmed data.
- **Fix (carried from bed2ecd)**: the v1→v2 migration backfill now writes
  `identity_hash` and `envelope_hash` (it calculated them but the SQL did not
  persist them — the import fallback masked it).

### Post-release fixes (review follow-ups)

- **P0.1 — persist `Validity.EffectiveAt`**: the vigencia start of a rule
  (published 1 Aug, effective 1 Jul, known 4 Aug) is now persisted in
  `validity_effective_at`, migrated from v1 stores and scanned back; the Go
  store no longer loses it (mirror TS already preserved it).
- **P0.2 — separate content/identity/envelope hashes**: `identityHash` (domain
  identity: scope + topicKey + effectiveAt + source reference) and
  `envelopeHash` (everything signable: identity + content + fiscal effect +
  source + evidence/rule refs + timestamps + supersession) join the canonical
  `contentHash`. `ImportObservation` decides on the envelope: matching envelope
  → exact duplicate (no-op); same identity + different envelope → immutable
  conflict; same content + different scope → independent memories.
- **P0.3 — terminal-state supersession policy**: the TS mirror no longer
  supersedes terminal heads (rejected/superseded/voided never reopen) and a
  new revision never inherits the previous approval (a fiscal effect lands it
  `pending_review` behind the human gate) — matching the Go store.
- **Shared golden vectors**: `testdata/golden/*.json` run from BOTH Go and
  TypeScript with FIXED expected hashes (content/identity/envelope), the
  approval gate and the initial status. Go creates → TS verifies the same
  hash; divergence fails one runner, never silently. 10 vectors: unicode,
  timezone offsets, materiality 0, evidence order, cross-tenant, late
  documents, system/agent/human actors, gated vs informative memories.
- **Known harness defect (P0.4, external)**: the gentle-ai review capture
  requires a provider-owned finding-id format inaccessible from this
  environment, and the client guard blocks repository commits without an
  exception for review-mode off. Commits for this release were executed by
  the maintainer from a terminal. Not fixable from the repository.

### Added

- **AccountingMemory v2 model** (`internal/core`): eight accounting kinds
  (`fact`, `evidence`, `decision`, `rule`, `exception`, `control`,
  `obligation`, `summary`), six statuses (`active`, `pending_review`,
  `approved`, `rejected`, `superseded`, `voided`), eight fiscal effects
  (`none`, `journal_entry`, `declaration`, `closing`, `adjustment`,
  `reclassification`, `approval`, `sunat_filing`), structured `Source`
  (`system`, `reference`, `actorId`, `actorKind`, `model`, `session`), triple
  timestamps (`effectiveAt`/`recordedAt`/`observedAt`), canonical
  `contentHash` (SHA-256 of the immutable content), `confidence`,
  `materiality` (int64 cents), `evidenceRefs`/`ruleRefs` and `receiptId`.
- **Human approval gate**: a memory with fiscal effect lands `pending_review`
  and only a human actor (`actorKind == human`) can `approve`/`reject` it;
  machine approval fails closed with `GATE_REQUIRES_HUMAN`. Voiding admits
  human/system, never agents. Supersession is explicit and atomic (status +
  `supersedesId` + `supersedes` relation in one transaction).
- **17 relations** (6 legacy + `supports`, `contradicts`, `explains`,
  `derived_from`, `posted_as`, `reconciles`, `reverses`, `requires`,
  `violates`, `approved_by`, `rejected_by`).
- **Store schema v2** with additive v1→v2 migration (legacy `type`→`kind`,
  `authority_status`→`status`, provenance→`source_json`; original columns
  preserved), `evidence_links`/`rule_links` tables, `receipt_id` column and
  sync import surfaces (`ImportObservation`, `ImportTransition`,
  `ApplyImportedStatus`, `SupersedeExplicit`).
- **Search filters**: kind/status/fiscal-effect filters over the mandatory
  scope-first isolation.
- **API v2**: `Approve`, `Reject`, `Void`, `Supersede`, `Judge` (documented
  professional adjudication with `explains` relation), `LinkEvidence`,
  `LinkRules`, `PeriodSummary` (the explainable period narrative — the killer
  demo: why did account 4011 end with this balance).
- **MCP**: 10 new `accounting_*` tools (`record`, `get`, `search`, `timeline`,
  `compare`, `judge`, `link_evidence`, `period_summary`, `context`, `doctor`)
  alongside the adapted `engram_*` surface (approve/reject/void replacing
  review/promote) — 24 tools total.
- **CLI**: `approve`, `reject`, `void`, `link-evidence`, `period-summary`,
  `timeline` commands; `save`/`supersede` adapted to the v2 model.
- **HTTP**: `/v1/observations/{id}/approve|reject|void` replacing
  review/promote; supersede with structured source.
- **Contracts**: `memory.md`, `lifecycle.md`, `provenance.md`, `scope.md`
  frozen-for-0.2 (kinds, gate, triple timestamps, source, hash, relations).
- **TypeScript reference mirror updated to v2**: `core/types.ts`
  (AccountingMemory, kinds, statuses, fiscal effects, source, canonical hash
  via WebCrypto), `lifecycle/transitions.ts` (approval-gated machine),
  `store/memory-store.ts` (v2 save with supersession + gate), `search/
  scope-first.ts` (kind/status/effect filters), `index.ts` and `lifecycle/
  index.ts` re-exported; reference tests rewritten to the v2 semantics.
- 280 Go tests green (was 106); `go test -race ./...` clean; 29 TS reference
  tests green (typecheck clean).

### Fixed

- **SQLite connection deadlock**: `queryMemories` resolved evidence/rule links
  while the scan `Rows` was still open, deadlocking under `MaxOpenConns(1)`;
  links are now resolved after the rows close.
- **modernc.org/sqlite tx quirks**: `defer tx.Rollback()` over an already
  committed transaction and indexed reads sharing a write transaction could
  hang the next connection use; transactions now use a conditional rollback
  and chain reads run before the write transaction.

### Removed

- The v1 lifecycle surface (`review`/`promote`, `draft`/`reviewed`/`promoted`
  statuses) is replaced by the v2 approval-gated machine. Legacy JSON reads
  and the migration preserve historical data.

## v0.2.0 — 2026-08-03 (GitHub release)

### Released — Go engine Phase 2 complete (ADR-001)

- **v0.2.0 tag + GitHub release** with static binaries (linux/darwin/windows x
  amd64/arm64, CGO=0, pure Go) and release notes. The Go module is
  go-gettable at `github.com/arkelythex/drenyra-engram@v0.2.0`.
- 106 Go tests green; `go test -race ./...` clean; non-authorization boundary
  intact; Drenyra adapter live-tested 12/12 against the released binary.
- Note: the `0.0.1-prealpha.x` npm policy applies to the TypeScript REFERENCE
  package (unchanged since prealpha.1, destined to be retired per ADR-001).
  The Go engine ships via the GitHub release, not npm.

## 0.0.1-prealpha.4 — 2026-08-02

### Added — Full chain revision history (GET /v1/chain, engram_chain)

- **`GET /v1/chain?topicKey=<key>&ruc=&period=&organizationId=`** — the full
  revision history of a (topicKey, exact scope) chain, ordered by revision
  ascending (the counterpart of context/get_by_topic which return only the
  latest). Store `FindChain` (additive to the Store interface), server API
  `Chain`, and the 13th MCP tool `engram_chain`.
- Needed by the Drenyra fiscal-memory adapter (`findById`/`findRevisions`
  map to this surface) — the first consumer that requires every revision of
  a topic key, not just the current one.
- 4 new Go tests (API history + structural isolation, HTTP route + fail
  closed, MCP tool) — total 104 Go tests green; `go test -race ./...`
  clean on all 6 packages; build + vet clean.

## 0.0.1-prealpha.3 — 2026-08-02

### Added — Local/cloud sync (ADR-001 v0.2, Phase 2 complete)

- **`drenyra-engram sync --from <src-db> --to <dst-db>`** — additive,
  provenance-preserving, conflict-visible reconciliation
  (docs/architecture.md sync semantics). Imports the source's FULL revision
  history, relations and lifecycle audit trail into the sink with original
  ids, revisions and provenance (store `ImportObservation`/`TransitionExists`
  — verbatim, validated, idempotent; never a re-save that would fabricate
  ids). Lifecycle propagates via TRANSITION REPLAY through the lifecycle
  machine (the imported audit record is the row — nothing is duplicated).
- **Conflict-visible, never silently resolved**: divergent (topicKey, scope)
  chain heads are preserved in BOTH stores and linked with a `conflicts_with`
  relation plus a report entry; an id existing with different immutable bytes
  is skipped (IMPORT_CONFLICT surfaced); an illegal transition replay is
  reported. Re-running the same pair is a no-op.
- Cloud is out of scope (ROADMAP non-goals — deferred to drenyra-cloud); this
  closes Phase 2 of the standalone Go engine.
- Corrected during review (fresh-eyes verification found two real bugs,
  fixed with regression tests): (1) a first full sync into an empty sink
  imported observations at their final status, making earlier audit records
  look "backward" — phantom transition conflicts on every run and an empty
  sink audit trail; sync now imports records verbatim and converges status
  FORWARD-ONLY (log-less, because the imported record IS the audit row), so
  the trail is complete with no duplication. (2) a LAGGING sink (source
  advanced the chain after a prior sync) was misreported as a divergent
  conflict with a permanent conflicts_with relation; divergence is now only
  detected when the sink's head is NOT an ancestor in the source's chain.
- 13 new Go tests (sync engine 12, CLI smoke 1) — total 100 Go tests green;
  build + vet + gofmt clean; live smoke: A→B round trip, idempotent re-run,
  full-lifecycle one-shot sync (0 phantom conflicts), fast-forward (0 false
  conflicts), divergent-chain conflict surfaced with conflicts_with relation.

## 0.0.1-prealpha.2 — 2026-08-02

### Added — MCP + HTTP surfaces (ADR-001 v0.2, Phase 2)

- **Shared domain services** (`internal/server/api.go`): one semantic surface
  for every transport — save/get/search/context/compare/doctor/review/promote/
  supersede/relations/transitions with a single error classification
  (not-found/invalid/conflict). The CLI now delegates compare/review/promote/
  supersede to it, so verdicts and lifecycle semantics are byte-identical on
  every surface (no re-derived logic, no drift risk).
- **MCP server** (`drenyra-engram mcp`): Model Context Protocol over stdio
  (JSON-RPC 2.0, newline-delimited) plus POST /mcp on the HTTP port. 12
  `engram_*` tools (save/get/get_by_topic/search/context/compare/doctor/review/
  promote/supersede/relations/transitions) with JSON-schema input validation
  (-32602 on shape errors); domain failures return in-band tool results with
  isError=true and the engine's stable error codes. initialize handshake,
  notifications, ping, tools/list supported.
- **HTTP API** (`drenyra-engram serve`): REST /v1/* surface bound to
  127.0.0.1 by default (fail closed), optional bearer token
  (DRENYRA_ENGRAM_TOKEN / --token) required on every request when set, and
  status mapping 400/404/409/500 with a machine-readable error envelope.
- **Non-authorization boundary kept on every surface**: no authorize/approve/
  allow tool, endpoint, or command — enforced by tests (catalog reflection,
  route 404s, CLI help).
- 42 new Go tests (server API 14, HTTP 10, MCP 15, CLI smoke 3) — total 87 Go tests green;
  build + vet clean; live smoke: CLI save → HTTP doctor/context/search → MCP
  over HTTP → MCP stdio round trip.

## 0.0.1-prealpha.1 — 2026-08-01

### Added — CLI complete (ADR-001 v0.2)

- `compare` (identity/scope/content deltas + relation verdict: supersedes /
  related / not_conflict — supersedes checks the SOURCE status, the successor
  stays draft/promoted), `review` (draft→reviewed), `promote` (reviewed→promoted),
  `supersede` (promoted→superseded with required target + relation). 45 Go tests
  green; non-authorization boundary kept (authorize/approve/allow rejected).

### Added — v0.2 standalone Go engine foundation (ADR-001)

- Go module + core types/validators, lifecycle machine, SQLite store (pure Go,
  immutable history, schema guards), scope-first search with cross-tenant
  isolation, and the `drenyra-engram` CLI (save/search/context/doctor). 40 Go
  tests green; the non-authorization boundary is enforced (no authorize commands).

### Added

- Repository identity scaffolding: README, LICENSE, SECURITY, CONTRIBUTING, CODEOWNERS, architecture and roadmap docs.
- Draft contract index (`contracts/`): `memory`, `scope`, `lifecycle`, `provenance`.
- **Memory core** — the observation model defined by the contracts: structured observations with type, scope (company/RUC/period), topic keys, lifecycle states (draft → reviewed → promoted → superseded), vigencia, and provenance.
  - **Non-authorization boundary** — documented and carried in the provenance contract: memory guides, it never authorizes.
  - **PR 1 — Functional vertical:** in-memory store with immutable revisions, scope-first search with the REQUIRED cross-tenant isolation property (company A memory never retrievable from company B, even with identical text/topic key), lifecycle transitions, and the compile-time `NonAuthorizing` guard (31 tests).
  - **Strategy freeze + packaging:**
    - ADR-001 (engine strategy: standalone Go engine long-term; v0.1 TS contracts + in-memory reference + PostgreSQL adapter; contracts stay neutral) and ADR-002 (storage authority: PostgreSQL transactional authority; in-memory = reference only).
    - Frozen-for-0.1 semantics in `contracts/`: immutable revision model, write idempotency (updated/conflict/unknown outcomes), scope-filter-before-ranking, UNKNOWN fail-closed, migration policy.
    - Build to `dist/` (tsc, NodeNext, declarations), `engines >= 22`, complete `files` manifest, subpath `exports`, `verify:package` + `verify-packed-install` + prepack/prepublishOnly gates + CI package job.

  ### Notes

  - Pre-alpha: contracts beyond the frozen-for-0.1 semantics are not frozen; relations, sync, and surfaces (MCP/CLI/HTTP/TUI) are planned in the ROADMAP.
  - Version policy: `0.0.1-prealpha.x` until the first frozen contract, then `0.1.0`.
