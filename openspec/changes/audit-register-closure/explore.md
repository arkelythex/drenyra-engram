# Exploration — SDD change `audit-register-closure`

## Scope

Convert 5 audit-register blocks from UNKNOWN/RISK to PASS with CODE-VERIFIABLE
evidence, per `docs/due-diligence/2026-08-product-architecture-audit.md`:
**L. Idempotency (186–194), J. Authorization (159–175), Q. Multi-tenant
security (277–296), Z. Operability (420–433), G. Migrations (111–122).**

Repo: Go (SQLite) kernel + Go↔TS parity mirror. Strict TDD: `go test ./...`,
`npm test`.

This is an inventory/mapping document. It does NOT design solutions or write code.

---

## Common reading notes (evidence rules)

- **PASS** requires implemented behavior + a relevant test/frozen contract.
- The engine is authenticated-approval-based; no float money; cross-tenant
  isolation is a REQUIRED structural property (`contracts/scope.md`).
- Repo artifact store is `openspec`; exploration is also mirrored to engram.

---

## L. Idempotency (186–194) — replay across HTTP/MCP/CLI/sync + lost-response

### 1. Evidence that ALREADY EXISTS

**Store-level idempotency reservation pattern** (every approved mutation):

- `internal/store/reconciliation.go` — `proposeReconciliationCommandHash`,
  `decideReconciliationCommandHash`, `withdrawReconciliationCommandHash`;
  `(tenant, requestId)` reservation; completed reservation replays original
  result; incomplete reservation re-derives (lines 173–311, 504–592, 967–986).
- `internal/store/purge_store.go` — `RequestPurge`/`ApprovePurge` idempotency
  on `evidence_purge_idempotency_keys` (`completePurgeIdempotencyOn` at :890).
- `internal/store/hold_store.go`, `internal/store/retention_policy_store.go`,
  `internal/store/review_store.go`, and `ApproveMemory` in `store.go`.
- `internal/server/approval_service.go:47` — requestId idempotency-key required.
- Command structs carry `RequestID` / `RequestIDKey` idempotency fields
  (`internal/core/purge_lifecycle.go:338–368`, `internal/core/closing.go:221`).
- `core/types.ts` TS mirror: `idempotentReplay`, `IDEMPOTENCY_CONFLICT`
  (`core/types.ts:279,353,376–377,491,524`).

**Store-level replay/conflict tests (Go):**

- `internal/store/approval_test.go:505 TestApproveMemoryIdempotentReplay`,
  `:544 TestApproveMemoryIdempotencyConflict`.
- `internal/store/judgment_test.go:677 TestProposeJudgmentIdempotentReplayAndConflict`,
  `:719 TestConfirmJudgmentIdempotentReplayAndConflict`,
  `:763 TestWithdrawJudgmentIdempotentReplay`.
- `internal/store/judgment_immutability_test.go:278
  TestJudgmentConfirmIdempotencyDifferentPrincipalConflict`,
  `:312 TestProposeReplayReturnsOriginalProposal`.
- `internal/store/reconciliation_test.go:685 TestReconciliationConfirmReplayAndConflict`,
  `:736 TestProposeReconciliationConflict`,
  `:763 TestProposeReconciliationReplayReturnsOriginalProposal`.
- `internal/store/review_store_test.go:589 TestRejectMemoryIdempotentReplay`.
- `internal/store/hold_test.go:172 TestPlaceHoldIdempotency` (replay :189).
- `internal/store/retention_policy_test.go:178 TestPutRetentionPolicyIdempotency`.
- `internal/store/reopen_test.go:237 TestReopenPeriodIdempotency`.
- `internal/store/receipt_emission_test.go:429
  TestApproveMemoryIdempotentRetryEmitsNoDuplicateReceipt`.

**Lost-response / interrupted-reservation coverage (partially):**

- `internal/store/purge_store_test.go:58
  TestRequestPurgeInterruptedReservationIsConflictNotScanError`,
  `:121 TestRejectPurgeInterruptedReservationReplayIsConflictNotScanError`
  (reserved-but-never-completed key → typed IDEMPOTENCY_CONFLICT, not a scan
  error; pipeline untouched).
- `internal/store/purge_execution_test.go:559 TestExecutePurgeInterruptedIntentAndRetry`,
  `:713 TestExecutePurgeCrashAfterUnlinkSameIDConverges`,
  `:802 TestExecutePurgeCrashAfterUnlinkFreshIDConvergesNoDuplicate`.

**HTTP surface replay tests:**

- `internal/server/http_review_test.go:268 TestHTTPReviewRejectIdempotentReplay`
  (only HTTP-level replay test asserting stored-event return).
- `internal/server/purge_http_test.go` — `TestHTTPPurgeRequestHappyPath`
  (replay block :279–297), `TestHTTPPurgeExecuteHappyPath` (replay :533–547),
  `TestHTTPPurgeRequestRequiresIdempotencyKey` (:590).
- `internal/server/http_approval_test.go:216 TestHTTPApprovalRequiresIdempotencyKey`.

**TS parity replay tests:**

- `store/__tests__/approval.test.ts:132` (replays same result, no second event),
  `:153` (different payload → IDEMPOTENCY_CONFLICT).
- `store/__tests__/judgment.test.ts:324` (replay + conflict).
- `store/__tests__/receipt-emission.test.ts:305` (idempotent retry → no dup receipt).

**Sync idempotency:**

- `internal/sync/sync_test.go:106 TestSyncIdempotent` (second run imports 0,
  skips 1, no conflicts).
- `cmd/drenyra-engram/main_test.go:859 TestCLISyncRoundTrip` (second sync is an
  idempotent no-op).

### 2. What is MISSING to convert to PASS

1. **No consolidated request-replay matrix across HTTP/MCP/CLI/sync.** Coverage
   is scattered per-operation per-surface. There is **no single table-driven
   test** enumerating (operation × surface) asserting replay returns the stored
   outcome with `idempotentReplay=true`.
2. **MCP-level replay tests are absent.** MCP tests only assert "fresh must NOT
   be a replay" (`mcp_purge_test.go:46`, `mcp_judgment_test.go:93`,
   `mcp_reconciliation_test.go:74`). No MCP test actually replays a request id
   and asserts the stored outcome.
3. **CLI-level replay tests are absent.** CLI tests likewise only assert fresh
   non-replay (`main_test.go:1137`, `judge_test.go:99`,
   `reconcile_test.go:109`, `review_test.go:160`, `purge_test.go:217`). No CLI
   test submits the same `--request-id` twice.
4. **Lost-response coverage is limited to purge.** The interrupted-reservation
   replay (reserved row with NULL entity/result) is only explicitly tested for
   purge. Approval/judgment/reconciliation/review/hold/reopen interrupted
   reservations are NOT directly tested for that scenario (the code path exists
   in `reconciliation.go` but has no dedicated lost-response test).

### 3. Gap classification (L)

- **Pure test addition:** a surface×operation replay matrix (Go table-driven,
  mirrors existing HTTP/store/TS tests); MCP replay tests; CLI replay tests.
- **Pure test addition:** lost-response (interrupted reservation) replay tests
  for each idempotent mutation (approval, judgment, reconciliation, review,
  hold, reopen) — against existing reservation code, no production change
  expected.
- **No production code change anticipated** for L (idempotency is implemented).

---

## J. Authorization (159–175) — SoD/dual-approval, override audit, segregation

### 1. Evidence that ALREADY EXISTS

**Versioned policy matrix (all frozen, constant-exact tested):**

- `internal/authz/approval_policy.go` — `PolicyVersion = "approval-policy/v0.4.0"`
  (:30); frozen matrix, role ladder, materiality raises ladder, assurance,
  tenant/company scope checks; `SODViolation` (:207), `ReviewChecksRequired`
  (:221), `ValidateReviewChecks` (:235). Test
  `internal/authz/approval_policy_test.go:113 TestPolicyVersionConstantIsExact`.
- `internal/authz/judgment_policy.go` — `JudgmentPolicyVersion =
  "judgment-policy/v0.4.0"` (:27). Test
  `judgment_policy_test.go:54 TestJudgmentPolicyVersionConstantIsExact`.
- `internal/authz/reconciliation_policy.go` — `ReconciliationPolicyVersion =
  "reconciliation-policy/v0.5.0"` (:31). Test
  `reconciliation_policy_test.go:68 TestReconciliationPolicyVersionConstantIsExact`.
- `internal/authz/evidence_lifecycle_policy.go` — dual-approval + SoD rules
  (see below). Test `evidence_lifecycle_policy_test.go:161
  TestLifecyclePolicyVersionConstantIsExact`.

**Dual-approval rules (implemented + tested):**

- `internal/authz/evidence_lifecycle_policy.go` — `LifecycleActionSecondApprove`
  (:66); the 10-step check order includes `DualApprovalRequired` config gate
  (:190) and distinct-principal rule (:194); role matrix `lifecycleRoleAllowed`
  (second approver = controller|tax_responsible only, :214); deny-list
  `roleDenied` (operational_accountant/admin, :242); `samePrincipal` (:259).
- Tests: `evidence_lifecycle_policy_test.go:275 TestApproveRequesterCannotBeApprover`,
  `:283 TestApproveDistinctRequesterIsAllowed`,
  `:292 TestSecondApproveRoleMatrix`, `:323
  TestSecondApproveRequiresDualCategoryConfiguration`, `:335
  TestSecondApproveRequiresDistinctPrincipal`, `:502` frozen check-order.
- Store-side SoD enforcement against STORED requester/first approver:
  `internal/store/purge_sod_test.go:74 TestPurgeApproveStoreSideSeparationOfDuties`
  (requester-can't-approve-own; same principal can't supply second approval →
  `APPROVER_IS_REQUESTER` / `SAME_PRINCIPAL_SECOND_APPROVAL`).
- HTTP dual second approval: `internal/server/purge_http_test.go:391
  TestHTTPPurgeDualSecondApproval`.

**Segregation (SoD) tests across surfaces:**

- `internal/store/review_store_test.go:555 TestRejectMemorySODViolation`
  (self-reject; fail-closed, zero decision events).
- `cmd/drenyra-engram/review_test.go:229 TestCLIReviewRejectSODViolation`.
- Approval SoD: `internal/authz/approval_policy_test.go` role/ladder matrix.

**"No override exists" — documented but NOT tested:**

- Code explicitly states **no override field/mechanism exists**:
  `internal/authz/evidence_lifecycle_policy.go:38`,
  `internal/store/purge_store.go:32,856,1010,1237`,
  `internal/auth/errors.go:104`, `internal/store/store.go:383,390`,
  `internal/store/purge_execution_store.go:573`.

### 2. What is MISSING to convert to PASS

1. **Override audit is absent.** There is no override implementation (by
   design) and, critically, **no test proving no override/break-glass path
   exists** across any approval surface. The audit's required closure is
   "override audit and segregation tests" — the negative assertion
   ("commands/requests carry no override field; no code path bypasses the
   policy") is not tested, and no decision record formalizes override-absence.
2. **No consolidated versioned-policy matrix test** that asserts every role ×
   action × effect across ALL four policies in one table (each policy is tested
   separately; a single cross-cutting matrix would be the strong "versioned
   policy matrix" artifact the audit asks for).
3. Segregation (SoD) is well covered; the missing piece is the override-negative
   test and the formal override-absence decision.

### 3. Gap classification (J)

- **Pure test addition:** negative override test(s) asserting no override
  field/flag on every request/command struct and no policy bypass path
  (compile-time struct-shape assertions + a behavioral test attempting
  break-glass inputs → frozen deny code).
- **Pure test addition:** a consolidated versioned-policy matrix test across
  approval/judgment/reconciliation/lifecycle policies.
- **Doc-only closure:** a decision/ADR recording "no override exists; override
  audit = negative conformance" (mirrors existing code comments). No production
  code change anticipated (override is deliberately absent).

---

## Q. Multi-tenant security (277–296) — cross-tenant matrix + nonexistence-safe errors

### 1. Evidence that ALREADY EXISTS (scattered cross-tenant tests)

- `internal/search/search_test.go:77 TestCompanyAObservationNeverVisibleFromCompanyB`
  (THE mandatory structural isolation conformance test).
- `internal/server/api_test.go:125 TestSearchScopeIsolation`;
  `internal/server/http_test.go:131 TestHTTPSearchScopeIsolation`.
- `internal/server/http_approval_test.go:283 TestHTTPApprovalCrossTenantDenied`.
- `internal/server/http_reconciliation_test.go:424
  TestHTTPReconciliationConfirmCrossTenantDenied`.
- `internal/server/period_service_test.go:214` ("cross tenant" case,
  `CodeCompanyScopeDenied`).
- `internal/server/object_http_test.go:92 TestHTTPObjectGetScopeFirstInvisibility`;
  `internal/server/mcp_object_test.go:91 TestMCPObjectGetScopeFirst`
  (→ `OBJECT_NOT_FOUND`).
- `internal/server/mcp_retention_policy_test.go:284
  TestMCPRetentionPolicyResolveCrossTenantInvisible` (→ ZERO policy);
  `internal/server/mcp_reconstructibility_test.go:121
  TestMCPReconstructibilityScopeIsolation`;
  `internal/server/reconstructibility_http_test.go:253
  TestHTTPReconstructibilityScopeIsolation`.
- `cmd/drenyra-engram/review_test.go:42 TestCLIReviewQueueScopeFirst`;
  `internal/store/review_store_test.go:201 TestReviewQueueScopeIsolation`.
- Policy-level: `internal/authz/approval_policy_test.go:200
  TestCrossTenantPrincipalDenied`; `judgment_policy_test.go:95
  TestCrossTenantJudgmentDenied`; `reconciliation_policy_test.go:109
  TestCrossTenantReconciliationDenied`; `evidence_lifecycle_policy_test.go:446
  TestLifecycleCrossTenantDenied`.
- Store-level: `internal/store/reconciliation_test.go:291
  TestProposeReconciliationCrossTenantDenied`; `internal/store/object_store_test.go:176
  TestGetObjectScopeFirst`; `internal/store/reconstructibility_store_test.go:96
  TestLatestMaterialDecisionHeadsExactScopeIsolation`;
  `internal/store/reopen_test.go` (cross-tenant principal).
- **Nonexistence-safe / non-enumerating errors:**
  `internal/store/object_hardening_test.go:342
  TestStoreObjectCrossScopeConflictNonEnumerating` (OBJECT_SCOPE_CONFLICT, and
  asserts NO scope value is leaked); object read → `OBJECT_NOT_FOUND`;
  retention resolve → ZERO policy; reconstructibility → zero denominator;
  chain read → empty.
- **Search leakage benchmark:** `internal/search/bench/bench_test.go:107
  TestSearchBenchmarkMetrics` / `:309 TestSearchBenchmarkProductionScale` use
  `CrossTenantProbes` (`queries.go:160`) asserting leakage EXACTLY 0.

### 2. What is MISSING to convert to PASS

1. **No "generated cross-tenant matrix for every operation."** Cross-tenant
   coverage is scattered per operation and NOT exhaustive: many API operations
   (e.g. Save, GetByTopic, Context, Relations, Transitions, RuleShow/RuleHistory/
   RuleImpact, Judge, LinkEvidence/LinkRules, PeriodSummary, Compare,
   Supersede, Void, ExportEvidenceLifecycle) have no explicit cross-tenant
   isolation test. There is no generator that enumerates every operation and
   runs the isolation assertion.
2. **Nonexistence-safe error audit is not exhaustive.** Non-enumerating /
   invisible-elsewhere behavior is proven for search/object/retention/
   reconstructibility/chain, but not for every operation's read path.
3. The required closure explicitly names a **generated** matrix; the current
   tests are hand-written and partial.

### 3. Gap classification (Q)

- **Pure test addition (primary):** a generated cross-tenant isolation matrix
  test — a Go helper that enumerates the API operation surface
  (`internal/server/api.go` methods: Save, Get, GetByTopic, Chain, Search,
  Context, Relations, Transitions, StoreObject, GetObject,
  PutRetentionPolicy/Resolve/Evaluate, Place/LiftHold, purge pipeline,
  ExportEvidenceLifecycle, ReviewQueue, Reconstructibility, Rule*, Judge,
  PeriodSummary, LinkEvidence/Rule*, Compare/Supersede/Void) and asserts
  cross-tenant invisibility + nonexistence-safe outcomes for each.
- **Small code addition (optional, only if needed to enumerate uniformly):** a
  shared test-scope/operation enumerator helper (test-only, no production code).
- **Pure test addition:** extend nonexistence-safe assertions to every read
  operation.
- No production behavior change anticipated (isolation is structurally
  implemented); the gap is proof-of-matrix.

---

## Z. Operability (420–433) — doctor, backup/restore, recovery objectives, corruption drills

### 1. Evidence that ALREADY EXISTS

**Doctor coverage:**

- `internal/store/doctor.go` — `Doctor` routine path runs `quick_check` then
  `foreign_key_check`, reports `integrityCheck: not_run`; full path runs
  `integrity_check` + FK check on a marked drill copy only; `cell_size_check`
  effective value reported; fail-closed on missing tables/triggers.
- `internal/store/store.go:6657 type DoctorReport` (schema version, all table
  counts incl. purge lifecycle, object findings, purge findings, health checks).
- Tests: `internal/store/doctor_test.go:67
  TestDoctorRoutineRunsQuickCheckThenForeignKeyCheck`, `:122
  TestDoctorRoutineNeverRunsIntegrityCheckOnCorruptStore`, `:171
  TestDoctorFullRequiresMarkedDrillCopy`;
  `internal/store/object_hardening_test.go:242 TestDoctorReportsOrphanAndTempFilesWithoutRepair`,
  `:300 TestDoctorMissingBytesFailsClosed`, `:319 TestDoctorInvalidRowPathFailsClosed`,
  `:634 TestDoctorReportsLifecycleTableCounts`, `:683/:739
  TestDoctorReportsIntentExecution…`.
- Server: `internal/server/api_test.go:363 TestDoctorFailsClosedOnMissingTable`,
  `:425 TestDoctorReportsPurgeLifecycleThroughAPI`.
- CLI: `cmd/drenyra-engram/main.go:314 cmdDoctor` (`--db`, `--drill-copy`,
  `--snapshot-manifest`); `main_test.go:261 TestCLIDoctorDrillCopyModeContract`.

**Backup/restore (implemented + tested):**

- `internal/store/drill.go` — `CreateDrillSnapshot` (VACUUM INTO consistent
  snapshot, :134), `OpenDrillCopy` (:240), sidecar `DrillManifest`, restore
  verify order integrity → FK → scope conformance → backup identity; sentinel
  errors `ErrStoreWriteFrozen`, `ErrDrillCopyOnly`, `ErrRestoreVerificationFailed`,
  `ErrBackupIdentityMismatch`.
- Tests: `internal/store/drill_test.go:404 TestRunRestoreDrillSuccess`, `:487
  TestRunRestoreDrillNegativeMatrix`, `:678 TestRunRestoreDrillScopeIsolation`,
  `:762 TestSchemaVersionParseFailsClosed`.

**Corruption drills (implemented + tested):**

- `internal/store/drill_test.go:162 TestRunCorruptionDrillFullPath`, `:256
  TestCorruptionDrillRequiresMarkedCopy`, `:287
  TestCorruptionDrillEvidencePathContract`, `:322
  TestCorruptionDrillEvidenceCannotOpenAsLiveStore`, `:353
  TestCorruptionDrillNotDetectedFailsClosed`.
- CLI drill usage/contract: `cmd/drenyra-engram/main_test.go:261`.
- Key-compromise drill (related O, not Z, but precedent): `compromise_drill_test.go:66`.

**Tenant export (delivered):**

- `internal/store/evidence_export_store.go` + `evidence_export_test.go`
  (`TestExportEvidenceLifecycleDeterministicBundle` :129, `:362
  TestExportEvidenceLifecycleTenantIsolation`, `:507
  TestExportCrashIntentNotStoredAndRecoveryConverges`);
  `internal/server/purge_http_test.go:637 TestHTTPLifecycleExportScopeFirst`.

### 2. What is MISSING to convert to PASS

1. **Recovery objectives (RTO/RPO) are entirely absent.** No documented
   recovery-time objective / recovery-point objective anywhere.
   `docs/architecture/evidence-object-v0.7.md:59` explicitly states "backup/
   restore drills, encryption-at-rest/TDE, **recovery objectives** are not
   demonstrated." This is the one hard gap.
2. **Backup/restore + corruption drill delivery is undocumented in the audit
   register.** The drills ARE implemented and tested (`drill.go`, `drill_test.go`,
   `TestRunRestoreDrill*`, `TestRunCorruptionDrill*`) but the audit register and
   `docs/product/initial-market-and-v1-gate.md` (G-5/G-6) still list
   backup/restore + corruption drills as DEFERRED/not demonstrated — a stale
   documentation state.
3. Doctor coverage and tenant export are present; a consolidated operability
   evidence matrix (doctor + backup/restore + corruption + export + recovery
   objectives) does not exist as a single artifact.

### 3. Gap classification (Z)

- **Doc-only closure:** document recovery objectives (RTO/RPO) — no production
  code, add to the operability/evidence-lifecycle doc or a new ADR.
- **Doc-only closure:** reconcile the audit register / G-5 / G-6 to record that
  backup/restore + corruption drills ARE delivered and tested (cite
  `drill.go`/`drill_test.go`), removing the stale "DEFERRED/not demonstrated"
  claim. (Pure documentation of existing evidence.)
- **Optional pure test addition (small):** a consolidated operability matrix
  test asserting the full doctor + restore + corruption + export surface in one
  table (evidence synthesis); not required if docs suffice for PASS.
- No production code change anticipated (features exist).

---

## G. Migrations (111–122) — direct-upgrade matrix, journal/recovery, provenance

### 1. Evidence that ALREADY EXISTS

**Versioned additive migration chain (implemented, `schemaVersion = 14`):**

- `internal/store/store.go:185 const schemaVersion = 14`.
- Migration functions (each ONE fail-closed transaction, `schema_version`
  flipped last): `migrateV1ToV2` (store.go:930), `migrateV2ToV3` (:1142),
  `migrateV3ToV4` (:1233), `migrateV4ToV5` (:1309), `migrateV5ToV6` (:1406),
  `migrateV6ToV7` (:1529), `migrateV7ToV8` (:1589),
  `migrateV8ToV9` (`retention_policy_store.go:208`),
  `migrateV9ToV10` (`hold_store.go:222`),
  `migrateV10ToV11` (`purge_store.go:311`),
  `migrateV11ToV12` (`purge_execution_store.go:242`),
  `migrateV12ToV13` (`review_store.go:211`),
  `migrateV13ToV14` (`migration_v14.go:43`).
- `Open`/`OpenWithObjects` runs the full chain (store.go:562–570, chain :900+);
  `applySchema` (:900); unknown version fails closed.
- WAL journal mode set at open (`PRAGMA journal_mode = WAL`).

**Migration tests (per-step additive + rollback + fresh bootstrap):**

- `store_test.go:722 TestV1ToV2MigrationIsAdditive`.
- `migration_v3_test.go:106 TestFreshStoreBootstrapsToSchemaV5`, `:151
  TestV2StoreMigratesToV5AdditivelyPreservingRows`, `:211
  TestV2ToV3MigrationRollsBackLeavingSchemaV2`.
- `migration_v4_test.go:133 TestFreshStoreBootstrapsV4JudgmentPersistence`,
  `:188 TestV3StoreMigratesToV5AdditivelyPreservingRows`, `:251
  TestV3ToV4MigrationRollsBackLeavingSchemaV3`.
- `migration_v5_test.go:126 TestV4StoreMigratesToV5AdditivelyPreservingRows`,
  `:185 TestV4ToV5MigrationRollsBackLeavingSchemaV4`, `:231
  TestUnknownSchemaVersionFailsClosed`.
- `migration_v6_test.go:167 TestV5StoreMigratesToV6AdditivelyPreservingRows`,
  `:331 TestV5ToV6MigrationRollsBackLeavingSchemaV5`.
- `migration_v7_v8_test.go:177 TestV7StoreMigratesToV8AdditivelyPreservingRows`.
- `migration_v9_test.go:233 TestV8StoreMigratesToV9AdditivelyPreservingRows`.
- `migration_v10_test.go:145 TestV9StoreMigratesToV10AdditivelyPreservingRows`.
- `migration_v11_test.go:399 TestV10StoreMigratesToV11AdditivelyPreservingRows`.
- `migration_v12_test.go:269 TestV11StoreMigratesToV12AdditivelyPreservingRows`.
- `migration_v14_test.go:151 TestV13StoreMigratesToV14AdditivelyPreservingRows`,
  `:196/:227 TestV13StoreMigrationFailsClosedOnPreExisting{Column,Index}`.

**Migration policy (frozen):**

- `contracts/provenance.md:55–75` — "Frozen semantics (v0.2) — migration
  policy": additive/reversible, corruption fails closed, lossless v1→v2.

### 2. What is MISSING to convert to PASS

1. **No consolidated "direct-upgrade matrix."** Tests are per-step (vN→vN+1)
   and a few multi-step; there is no single table-driven test proving every
   legacy version (v1…v13) opens directly to v14 in one `Open`, asserting the
   final schema version, additive preservation, and rollback for each — the
   audit's "direct-upgrade matrix."
2. **No migration journal / recovery proof.** Migrations rely on SQLite
   single-transaction atomicity + WAL; there is no migration journal/ledger and
   no explicit test demonstrating mid-migration crash → clean recovery/re-run.
   The purge/export crash-recovery tests (`purge_execution_test.go:559`,
   `evidence_export_test.go:507`) cover runtime recovery, not migration
   recovery.
3. **No migration provenance record.** There is no record of which migrations
   ran, when, or the provenance of the migration chain (beyond the current
   `schema_version` value and code comments). The audit's "migration
   provenance" is not materialized.

### 3. Gap classification (G)

- **Pure test addition:** a generated direct-upgrade matrix test — for each
  legacy version (v1…v13) build a genuine store at that version, open it with
  `Open`, assert schema_version=14, additive row preservation, and fail-closed
  rollback on an injected mid-migration failure.
- **Pure test addition (or doc-only):** a migration crash/recovery test proving
  a failed migration rolls back and the store can be re-opened and re-migrated
  (the code path exists — each migration is one transaction; only the proof is
  missing). Could also be closed by a doc asserting the single-transaction
  model is the recovery guarantee.
- **Small code addition + test (provenance) OR doc-only:** materialize a
  migration provenance/history record (which migrations applied, when) — this
  likely requires a small production addition (a `schema_meta` migration-history
  row/table) plus tests; alternatively a doc-only decision defining provenance
  semantics if a ledger is deemed out of scope.

---

## Cross-cutting synthesis (for the proposal/spec phase)

| Block | Primary existing evidence | Hardest gap | Gap type |
|---|---|---|---|
| L | Store/HTTP/TS replay + sync idempotency | MCP & CLI replay tests; lost-response for non-purge ops; consolidated matrix | Pure test |
| J | 4 versioned policies + SoD/dual-approval tests | Override-negative test + override-absence decision; consolidated policy matrix | Pure test + doc |
| Q | Scattered cross-tenant + leakage benchmark | **Generated cross-tenant matrix for EVERY operation** + exhaustive nonexistence-safe errors | Pure test (+ test-only helper) |
| Z | Doctor, restore/corruption drills, tenant export all implemented+tested | **Recovery objectives (RTO/RPO)**; stale "DEFERRED" claims in docs | Doc-only (+ optional matrix test) |
| G | Full v1→v14 chain + per-step tests + frozen policy | **Direct-upgrade matrix**; migration journal/recovery proof; migration provenance | Pure test (+ small code for provenance) |

**Suggested sequencing for the proposal:** L, Q and the G direct-upgrade matrix
are predominantly pure test additions (largest TDD surface, lowest risk). Z is
almost entirely doc-only closure of already-delivered evidence. J requires a
decision (override-absence) plus negative tests. G's migration-provenance piece
is the only probable production-code addition.

## Open questions for the proposal round

1. J: Is "override audit" satisfied by a negative conformance test + an
   override-absence decision, or does the owner expect an override feature?
   (The code deliberately has NO override; recommend negative-only closure.)
2. G: Should migration provenance be materialized (small production addition)
   or closed as a documented decision that the single-transaction chain + schema
   version IS the provenance record?
3. Z: Are RTO/RPO numbers a business decision (owner-provided) or should the
   doc only assert the delivered drill/doctor capabilities without targets?
4. Q: Is a "generated matrix" acceptable as a Go test-only helper enumerating
   the API surface, or must it be a checked-in artifact (e.g. a JSON matrix
   document)?

## skill_resolution

- `paths-injected` (no project skills were injected; standard executor contract).
