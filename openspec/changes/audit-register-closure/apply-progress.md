# Apply Progress — audit-register-closure

> Phase: apply · Artifact: apply-progress · Status: **PR J (AC-J-1…AC-J-5) COMPLETE — first chained slice; full gates green**
> Inputs: spec + design + tasks (read from `openspec/changes/audit-register-closure/`). Strict TDD active (`openspec/config.yaml` `phases.apply.strict_tdd: true`); test commands `go test ./...`, `npm test`, `go vet ./...`, `gofmt -l .`.
> Change delivered as CHAINED PRs stacked-to-main per the Review Workload Forecast (J → L → Q → G → Z → evidence-pass). THIS SLICE = PR J only (AC-J-1…AC-J-5); PRs L/Q/G/Z/evidence-pass are LATER slices and were NOT implemented.

## Structured status consumed

Produced via the native dispatcher (`gentle-ai sdd-status audit-register-closure --cwd . --json --instructions`):

- `changeName: audit-register-closure`, `artifactStore: openspec`, planning `repo-local`.
- `actionContext: {mode: repo-local, workspaceRoot: <repo>, allowedEditRoots: [<repo>]}` — edit scope safe.
- **Native quirk (flagged, same as v1-readiness):** the engine reports `nextRecommended: spec` / blocked with `blockedReasons: []` because it does NOT index `spec.md` (`artifactPaths.specs` is empty; the file exists and is readable). Per the parent's explicit PR-J apply directive and the empty `blockedReasons`, apply proceeded; the discrepancy is recorded here, not treated as a real blocker.
- Native Runtime Attempt Authority: `gentle-ai sdd-attempt acquire --cwd . --change audit-register-closure --request-id apply-prj-001 --work-unit "PR J slice (AC-J-1..AC-J-5)" --evidence-goal "AC-J-1..AC-J-5 green via consolidated policy matrix, structural/behavioral override-negative tests, provenance section; full gates green" --max-attempts 4 --max-changed-lines 1500` → `state: proceed` (token retained for settle).
- Review Workload Guard decision: chain approved by the orchestrator (`stacked-to-main`, owner-approved per tasks.md); PR boundary = J exactly (J.1…J.9 only); zero L/Q/G/Z/evidence-pass code touched.
- Task ownership: J.1…J.9 all carry valid terminal `<!-- sdd-owner: implementation -->` markers; parent-owned rows untouched byte-for-byte.

## TDD Cycle Evidence (PR J)

Every J task is a CONFORMANCE test pinning EXISTING frozen behavior (design: "fail-before-fix" contract guard). Tests were written FIRST (the RED artifact); they passed IMMEDIATELY against the existing implementation — that IS the contract-guard proof (implementation == frozen contract). NO production Go/TS, schema DDL, API signature, adapter decoder, policy semantics, idempotency, or migration file was touched. Any RED here would have been a REAL defect → slice stop + re-approval; none occurred.

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| J.1 RED / J.2 GREEN | `internal/authz/consolidated_policy_matrix_test.go` (`TestConsolidatedVersionedPolicyMatrix`) | Unit (pure policies) | ✅ four policy suites unchanged + green (NFR-J.2 version freeze) | ✅ written first (RED artifact; contract-guard: no production change permitted) | ✅ 40 subtests green immediately (0.004s) | ✅ 4 exact version constants; 24 role×action×effect rows (allow/role/scope/assurance/materiality/deny-list/dual/SoD); 10 named check-order probes copying `TestLifecycleCheckOrderIsFrozen` (evidence_lifecycle_policy_test.go:497); version anchors cited (approval:113, judgment:54, reconciliation:68, lifecycle:161) | ✅ gofmt/vet clean |
| J.3 RED | `internal/server/no_override_surface_test.go` (`TestNoOverrideFieldOnRequestSurfaces`, `TestCanonicalRequestTypesMatchAPISurface`) | Unit (reflection + AST) | ✅ server suite green | ✅ written first | ✅ green immediately — no override spelling exists on any canonical request type | ✅ 25-type registry (14 API + 11 HTTP/MCP adapter-only); recursive walk (visited set, structs/ptrs/slices/arrays/map elems, Go name + JSON tag normalized); AST guard requires api.go `*core.XCommand`/`*core.XInput` params registered; adapter-only classification honest (discovered∩adapterOnly=∅); deliberate-absence comments cited (evidence_lifecycle_policy.go:38, purge_store.go:32/856/1010/1237, store.go:383/390, auth/errors.go:104, purge_execution_store.go:573) | ✅ gofmt/vet clean |
| J.4 RED | `cmd/drenyra-engram/no_override_surface_test.go` (`TestCLINoOverrideFlagOnCommands`, `TestCLICommandCatalogGuard`) | Unit (in-process dispatch, real FlagSets) | ✅ cmd suite green | ✅ written first | ✅ green immediately — 60 command paths × 4 spellings all exit 2 with "flag provided but not defined" | ✅ every leaf + group subcommand dispatches its real FlagSet; stderr verbatim assertion; catalog guard parses `run` + 13 group switches (main.go/rule.go) via go/parser → registry == dispatch catalog | ✅ gofmt/vet clean |
| J.5 RED | `internal/server/override_negative_test.go` (`TestOverrideInputDeniedFailClosed`) | Integration (real strict decoders) | ✅ server suite green | ✅ written first | ✅ green immediately — every forbidden spelling rejected by real decoders | ✅ 9 MCP mutation tools × 5 spellings → JSON-RPC -32602, zero doctor-digest change; HTTP approve with privileged records-compliance-officer session (NFR-J.1) × 5 spellings → 400 INVALID, zero digest change | ✅ gofmt/vet clean |
| J.6 RED | `cmd/drenyra-engram/no_override_surface_test.go` (`TestCLIOverrideInputDeniedFailClosed`) | Integration (subprocess binary + temp DB) | ✅ cmd suite green | ✅ written first | ✅ green immediately | ✅ 7 representative privileged commands (approve, review reject, purge approve, close reopen, judge confirm, reconcile confirm, hold place) × 4 spellings → exit 2 + unknown-flag + DB **byte-identical** before/after | ✅ gofmt/vet clean |
| J.7 RED | `internal/store/authorization_no_bypass_test.go` (`TestDeniedOperationNotBypassableViaAdapterOrStore`) | Integration (real store) | ✅ store suite green (SoD anchors stay green: purge_sod_test.go:74, review_store_test.go:555, review_test.go:229, purge_http_test.go:391) | ✅ written first | ✅ green immediately (0.46s) | ✅ 5 rows × 3 boundaries (pure policy / store method the adapters call / store transactional depth): deny-listed admin (NFR-J.1), requester==approver, same-principal second approval, controller-default-approver role denial, review self-decision; same frozen code at every layer + doctor-digest + operation-specific zero-state-change assertions | ✅ gofmt/vet clean |
| J.8 GREEN | `contracts/provenance.md` | Doc (frozen contract) | ✅ `git diff -- contracts/provenance.md` = +27 lines ONLY the new section | ✅ doc written after tests landed (per tasks.md ordering) | ✅ frozen section in place | ✅ `## Frozen override-absence decision` inserted immediately after `## Non-authorization boundary`, before migration semantics; states no override/break-glass/force/bypass/privileged-emergency path, negative-conformance semantics (FD-1), privileged/urgent callers bound to the same versioned policy/scope/assurance/role/SoD rules, professional memory approval outside business/payment authorization | ✅ no contract version / schema version change |
| J.9 TRIANGULATE | review of all J artifacts | Audit | ✅ full gates green | N/A (review step) | ✅ PASS | ✅ every new artifact is observation/denial-only: matrix rows assert pure policy decisions; structural guards reflect/parse and reject; behavioral tests assert typed denials with zero state change; no new surface approves/posts/files/reopens writes; decision record asserts the non-authorization boundary and claims no business/payment authorization capability (AC-J-5, FR-J.5, NFR-XC-4) | ✅ |

## Test summary (PR J)

- **Tests written**: 5 new test files, 6 new top-level test functions + ~250 subtests/rows:
  - `TestConsolidatedVersionedPolicyMatrix` (40 subtests: 4 version constants + 24 matrix rows + 10 check-order probes + 2 SoD allow/denial rows),
  - `TestNoOverrideFieldOnRequestSurfaces` + `TestCanonicalRequestTypesMatchAPISurface` (server structural),
  - `TestCLINoOverrideFlagOnCommands` (240 subtests: 60 paths × 4 spellings) + `TestCLICommandCatalogGuard` + `TestCLIOverrideInputDeniedFailClosed` (28 subtests),
  - `TestOverrideInputDeniedFailClosed` (45 MCP + 5 HTTP subtests + 2 digest-equality assertions),
  - `TestDeniedOperationNotBypassableViaAdapterOrStore` (5 rows × 3 boundaries).
- **Tests passing**: all focused + full `go test ./...` green (10 packages).
- **Layers**: Unit (pure policies, reflection, AST), Integration (real strict decoders, real store, real CLI binary subprocess).
- **Approval tests**: none needed — zero production files touched (tests + docs only).
- **Pure functions created**: none (semantics frozen; this slice proves, it does not implement).

## Files changed (PR J)

| File | Change |
| --- | --- |
| `internal/authz/consolidated_policy_matrix_test.go` | NEW — four-policy consolidated matrix (FR-J.1 / AC-J-1): exact versions, 24 role×action×effect rows, dual-approval gate, SoD denials, approval SODViolation clause, 10-probe frozen check-order subtest |
| `internal/server/no_override_surface_test.go` | NEW — structural override-absence (FR-J.2 / AC-J-2): 25-type reflection registry + recursive field walk + api.go AST drift guard; cites all deliberate-absence comments |
| `cmd/drenyra-engram/no_override_surface_test.go` | NEW — CLI structural sweep (60 dispatch paths × 4 spellings via real FlagSets, stderr verbatim) + dispatch-catalog guard + behavioral `TestCLIOverrideInputDeniedFailClosed` (AC-J-3, 7 privileged commands, DB byte-identity) |
| `internal/server/override_negative_test.go` | NEW — behavioral override-absence (FR-J.3 / AC-J-3): 9 MCP mutation tools + privileged HTTP approve through real strict decoders; -32602 / 400 INVALID; doctor-digest state equality |
| `internal/store/authorization_no_bypass_test.go` | NEW — policy/service/store three-boundary denial parity (FR-J.3 / AC-J-3): 5 rows incl. deny-listed admin + same-principal SoD; same frozen code at every layer, zero state change |
| `contracts/provenance.md` | `## Frozen override-absence decision` section added (FR-J.4 / AC-J-4, FD-1) — +27 lines only |
| `openspec/changes/audit-register-closure/tasks.md` | J.1…J.9 checked `[x]` (implementation-owned rows; L/Q/G/Z + parent-owned rows untouched byte-for-byte) |

## Gate results (PR J)

| Gate | Result |
| --- | --- |
| Focused: `go test ./internal/authz ./internal/server ./internal/store ./cmd/drenyra-engram` | ✅ ok (authz 0.014s, server 22.2s, store 43.2s, cmd 11.4s) |
| `go test ./...` | ✅ all 10 packages ok |
| `go vet ./...` | ✅ clean (exit 0, no findings) |
| `gofmt -l .` | ✅ clean (no output) |
| `npm test` | ✅ 385 tests / 26 files passed (unchanged — zero TS bytes touched) |
| `git diff -- contracts/provenance.md` | ✅ limited to the frozen override-absence section (+27 lines) |

## Deviations / reconciliations (PR J)

1. **RED semantics for a frozen seam (by design, not a deviation):** every J task pins EXISTING frozen behavior as a permanent regression; per the contract guard, no production edit is permitted merely to make a new test pass. All six test functions were written first and passed immediately against the existing implementation — that IS the contract-guard proof (implementation == frozen contract). No real defect was exposed, so no exception/re-approval was needed.
2. **"SoD denial each" scope (task J.1 text vs frozen policies):** the judgment and reconciliation policies define NO SoD clause in their frozen `Authorize` (their check order is tenant → company → membership → role → assurance). Rather than invent policy semantics, the matrix pins SoD denials where the frozen policies define them: the approval policy's `SODViolation` clause (v0.9.0 review workspace, real public function) and the evidence-lifecycle policy's requester≠approver + distinct-principal second-approval denials. The consolidated test doc comment states this explicitly; store-side SoD for the other flows is proven by the J.7 no-bypass rows (FR-J.3). This is documented, not hidden.
3. **CLI structural check (task J.4 "construct each current command's real flag.FlagSet, VisitAll"):** the FlagSets are constructed inline inside each `cmd*` function (no shared builder exists), so a test cannot extract and VisitAll them without running the command. The faithful equivalent is implemented: dispatch the REAL command with the forbidden flag as its ONLY argument — the command constructs its real FlagSet, `fs.Parse` rejects the spelling with `flag provided but not defined: -<flag>` and returns 2 BEFORE any store/file side effect (verified verbatim on stderr). If a command ever defined an override flag, Parse would succeed and the command would proceed — the assertion fails. The catalog guard (go/parser over `run` + 13 group switches) prevents new commands/subcommands from bypassing inspection. Behaviorally equivalent to VisitAll; stronger than source-string scanning (D-4).
4. **`TestLifecyclePolicyCheckOrderIsFrozen` naming (task text):** the existing test is `TestLifecycleCheckOrderIsFrozen` (evidence_lifecycle_policy_test.go:497). The check-order subtest copies its probes faithfully and completes the ten frozen positions (2-company-scope and 3-membership-active reuse the same assertions as the existing `TestLifecycleCompanyOutOfScopeDenied` / `TestLifecycleInactiveMembershipDenied`).
5. **Server/service boundary in J.7:** for the purge/review/approval flows the server adapter layer performs NO authorization of its own (it validates syntax and delegates); the store method IS the adapter entry point. The three boundaries are therefore: pure policy → store method the adapters call (service boundary) → same method's transactional depth asserted via doctor-digest + operation-specific zero-state-change (store boundary). The file doc comment states this mapping explicitly.
6. **No real defect found anywhere:** no surface silently accepts an override spelling, no CLI flag is accepted, no store bypass exists, no boundary diverges in reason code. Slice stopped for re-approval is NOT needed.

## Workload / PR boundary

- ≈ 1,950 changed lines (5 new Go test files ≈ 1,920 incl. comments + +27 doc lines + 9 tasks.md checkbox edits). Above the 400-line budget by design — the whole change is delivered as chained PRs (Review Workload Forecast, owner-approved stacked-to-main); PR J is the first slice only.
- The PR J boundary holds: the ONLY changed/added files are the five J test files, `contracts/provenance.md` (frozen section), and the two openspec artifacts (tasks.md checkboxes + this apply-progress). Zero production Go/TS, zero store/schema bytes, zero other docs touched (audit register J row is NOT flipped — that is the evidence-pass slice, NFR-XC-5).

## Remaining tasks (unchecked — future slices + parent lifecycle)

Implementation-owned rows still pending in `tasks.md` (later chained PRs — NOT this slice):

- [ ] L.1 … L.7 (PR L — idempotency replay matrix and lost-response proof)
- [ ] Q.1 … Q.5 (PR Q — generated cross-tenant matrix)
- [ ] G.1 … G.7 (PR G — direct upgrades and migration provenance)
- [ ] Z.1 … Z.5 (PR Z — operability evidence and reconciliation)
- [ ] E.1 … E.3 (PR evidence-pass — audit register closure)
- [ ] Cross-cutting checklist rows + Definition-of-done rows (checked at chain close)

Parent-owned rows (deferred lifecycle actions, preserved byte-for-byte in tasks.md):

- [ ] Start or reuse bounded review for each stacked-to-main PR boundary after its normalization + candidate freeze (PR J → L → Q → G → Z → evidence-pass); one correction budget per candidate; no reviewer launched by apply.
- [ ] After the final evidence-pass PR merges, run the verify phase (`sdd-verify`) against AC-L/J/Q/Z/G/XC criteria; remediate only through the bounded correction path.
- [ ] Archive the change only when verify reports all criteria green and the full chain is merged.

## Cross-cutting checklist status (PR J)

- Conventional commits per atomic milestone, no AI attribution — VCS owned by the orchestrator; NOT committed here (slice instruction).
- Money stays whole int64 cents / BigInt cents — no money value crosses any new test (reconciliation fixture amounts are int64 cents in the matrix, matching the frozen fixture; no float anywhere) (IR-1).
- Scope structural + fails closed — untouched (matrix and denial tests assert scope denials; no scope logic changed) (IR-2).
- Non-authorization boundary — every new artifact is observational or denial-only; the matrix asserts pure policy decisions; the behavioral tests assert typed denials with zero state change; the provenance decision asserts the boundary; no new surface approves, posts, files, or reopens writes (IR-3, NFR-XC-4).
- No `any` in TS — untouched (typecheck-equivalent via `npm test` green; zero TS bytes).
- Golden parity — no shared Go↔TS semantic change; `internal/core` untouched (IR-4).
- No new production schema, override path, public API operation, or numerical recovery promise (NFR-XC-6, FD-1) — reviewer grep over changed files confirms.
- Fixtures/docs contain no credentials, tokens, or customer data (NFR-XC-7).

## Next phase

- Parent: bounded post-apply review of the PR J candidate (normalization + freeze), then chain continues to PR L. Verify and archive run only after the full chain merges.

## PR L — Idempotency replay matrix and lost-response proof (APPENDED by orchestrator)

**status:** DELIVERED (all L tasks checked, gates green)

**Defects exposed by the matrix and fixed (NFR-L.1 production-exception path):**

1. `purge_store.go` — ApprovePurge/RejectPurge/CancelPurge/WithdrawPurge replay paths
   returned the stored outcome with `IdempotentReplay=false` (the stored JSON carried
   the original-attempt flag). Fixed: set `replayed.IdempotentReplay = true` after
   unmarshal, matching the ExecutePurge rule (`purge_execution_store.go:536`).
2. `hold_store.go` + `retention_policy_store.go` — the interrupted-reservation scan
   read `hold_id`/`result_json`/`retention_policy_id` into plain `string`, so a
   reserved-but-never-completed row (NULL columns) surfaced a raw scan error instead
   of the typed IDEMPOTENCY_CONFLICT. Fixed: `sql.NullString` + `.Valid` guard,
   mirroring the purge precedent (`purge_store.go:2060`).

**Tests added:**

- `internal/server/idempotency_replay_matrix_test.go` — TestIdempotencyReplayMatrix
  (consolidated operation × surface catalog: approve, judgment propose/confirm/withdraw,
  reconciliation propose/confirm/withdraw, review reject, hold place/lift, reopen,
  retention put, purge request/approve/reject/cancel/withdraw/execute × HTTP/MCP/CLI/sync)
  - TestIdempotencyReplayMatrixProofLinksResolve (external-proof link guard).
- MCP replay cases added to mcp_purge/mcp_judgment/mcp_reconciliation tests.
- CLI replay cases added to judge/reconcile/review/purge command tests.
- `internal/store/idempotency_interrupted_reservation_test.go` — lost-response
  recovery for approve/judgment/reconciliation/review/hold + reopen atomic
  rollback/replay tests.

**Gates:** go test ./... green (10 packages), go vet clean, gofmt clean,
npm test 385/385 green.

**Orchestrator note:** the apply subagent for PR L timed out twice mid-implementation;
the orchestrator completed the helper functions, fixed the `body` field typed-nil bug
in the matrix harness (replayInvocation.body map[string]any -> any), resolved the
proof-link paths, and applied the two production defect fixes above.

## PR Q — Generated cross-tenant matrix + SECURITY FIX (APPENDED by orchestrator)

**status:** DELIVERED (matrix green for 43/46 operations; 3 link methods remain
RED diagnostics requiring a separately approved correction — the design's
predicted risk).

**CRITICAL DEFECT exposed and FIXED (owner-approved, 2026-08-11):**
The exhaustive matrix proved the audit block's "PASS for structural scope
claims" was WRONG. `GET /v1/observations/{id}`, `GET /v1/relations`,
`GET /v1/transitions`, `POST /v1/compare`, and the lifecycle mutations
`reject`/`void`/`supersede` were CROSS-TENANT LEAKS: any caller (with the
shared token or none) could read or MUTATE another tenant's memories without
scope. Probes confirmed: tenant-A memory readable with no scope (200), and
rejectable cross-tenant (status flipped to rejected).

Fix (contracts/scope.md rule 4, approved as "scope in the HTTP adapter"):

- handleGet: requires ?ruc=&period=&companyId= and verifies the memory's exact
  scope (scope mismatch = MEMORY_NOT_FOUND, no existence disclosure).
- handleRelations / handleTransitions: require caller scope, filter via new
  store.RelationsForScope / TransitionLogForScope (SQL JOIN on observations).
- writeGateTransition (reject/void) + handleSupersede + handleCompare: require
  caller scope and verify target memory(ies) belong to it.
- Scope mismatch is indistinguishable from not-found (no oracle).
- Production change: internal/server/http.go, internal/server/api.go (2 scoped
  passthroughs), internal/store/store.go (2 scoped methods + Store interface).

**REMAINING DEFECT (documented, NOT fixed — separate SDD required):**
LinkEvidence/LinkRules/LinkRuleVersion are exposed via MCP
(accounting_link_evidence) and CLI (link-evidence) WITHOUT caller scope — the
matrix keeps them RED (diagnostic) and they block AC-Q-1..Q-5 until a
separately approved correction scopes the MCP/CLI link surfaces.

**Tests:** internal/server/cross_tenant_matrix_test.go — TestCrossTenantMatrix
(43/46 green, 3 link RED), TestCrossTenantMutationNoSideEffect (43/46),
TestCrossTenantMatrixExhaustiveness (green, excludes *ForScope views).
Existing HTTP tests updated with scope query params (http_test.go).

**Gates:** go test ./internal/server (only the 6 expected link diagnostics
fail), go test ./... green elsewhere, go vet clean, gofmt clean.

## PR G — Interruption recovery + migration provenance (APPENDED by apply continuation)

**status:** PARTIAL DELIVERED — G.4 + G.5 + G.6 complete and green; G.1/G.2/G.3
(direct-upgrade matrix v1…v13) and G.7 (TRIANGULATE of the full matrix) remain
unchecked (later slice, outside this run's 400-line budget).

**Files changed (this slice):**

- `internal/store/migration_direct_upgrade_test.go` (NEW, 288 lines) —
  `TestMigrationInterruptedRollsBackToPriorVersion` + `TestMigrationCrashReopenConvergesToV14`
  (AC-G-2 / FR-G.2). The design's planned file; the v1…v13 matrix rows land here by G.1–G.3.
- `contracts/provenance.md` (+23/−1) — stale `(schema_version = 2)` literal made
  version-neutral + new `### Migration provenance for schema v14 (frozen)` subsection
  (AC-G-3 / AC-G-4 / FR-G.3 / FR-G.4 / FD-2).

**Strict-TDD cycle (G slice):**

- RED: both tests written FIRST. First run RED — but the failure was a test-harness
  bug (the shared assertion queried `COALESCE(version,'')` on the rolled-back v13
  state where the column correctly does not exist), NOT a migration defect. Harness
  fixed to be version-aware; both tests then GREEN immediately against the existing
  implementation — the contract-guard proof that the one-transaction-per-step model
  already rolls back mid-chain failures and re-converges on reopen.
- TRIANGULATE: assertions verify structural invariants + byte preservation + unique
  row counts, never version numbers alone (NFR-G.1); no production migration code,
  schema DDL, or one-transaction-per-step rule touched (NFR-G.2).

**Injection seam — documented deviation from design D-5 (NFR-G.2):**

- D-5 proposed a pre-existing wrong `idx_rule_links_ref` so migrateV13ToV14 would run
  its ALTERs then fail at index creation. The REAL migration fail-closes on ANY
  pre-existing index at step (a) BEFORE executing ALTERs (migration_v14.go) — that
  injection can never execute partial DDL and only re-proves the already-tested
  fail-closed abort (`TestV13StoreMigrationFailsClosedOnPreExistingIndex`).
- Strongest achievable proof used instead: a test-only `BEFORE UPDATE OF value ON
  schema_meta` trigger raises ABORT when the migration's LAST statement (schema_version
  = 14 advance) runs — at that point both ALTERs and the CREATE INDEX already executed
  inside the step's single transaction, so the failed advance must roll the WHOLE step
  back. Test asserts: schema_version still 13, version/effective_at columns ABSENT,
  idx_rule_links_ref ABSENT, no `receipts_v*` staging tables, seeded observation +
  legacy rule-link byte-intact and exactly one copy. Reopen test drops the trigger,
  confirms coherent v13, then normal `Open` re-runs to v14 with the exact frozen index
  definition (PRAGMA index_info = ref/version/effective_at/memory_id) and one copy of
  each seeded row.
- Proof claim (bounded, NFR-G.2): SQLite transaction rollback of a mid-chain failure
  at the schema_version advance + deterministic re-run convergence under the
  one-transaction-per-step model. NOT an OS-kill emulation; no production hook/ledger.

**Gates (this slice):**

| Gate | Result |
| --- | --- |
| Focused: `go test ./internal/store -run 'TestMigrationInterruptedRollsBackToPriorVersion|TestMigrationCrashReopenConvergesToV14'` | ok |
| `go test ./internal/store` | ok (35s) |
| `go test ./...` | ok except the 6 PRE-EXISTING Q link-method diagnostics in internal/server (LinkEvidence/LinkRules/LinkRuleVersion × TestCrossTenantMatrix + TestCrossTenantMutationNoSideEffect — documented, require separate approval; present before this slice) |
| `go vet ./...` | clean |
| `gofmt -l .` | clean |
| `go test ./internal/core -run TestGoldenVectorsGo` | ok |
| `npm test` | 385/385 passed |

**Workload / PR boundary:** this slice = G.4, G.5, G.6 only. Remaining implementation
rows in tasks.md (unchanged, unchecked): G.1, G.2, G.3 (direct-upgrade matrix v1…v13),
G.7 (matrix TRIANGULATE), Z.1…Z.5, E.1…E.3, cross-cutting checklist, DoD rows.
Parent-owned rows untouched byte-for-byte.

## PR G — Direct-upgrade matrix v1…v13 (G.1/G.2/G.3) + TRIANGULATE (G.7) (APPENDED by apply continuation)

**status:** DELIVERED — G.1, G.2, G.3, G.7 complete and green. PR G now fully
implemented (G.4/G.5/G.6 landed in the prior slice); the audit G row flip
remains the evidence-pass slice (NFR-XC-5).

**Files changed (this slice):**

- `internal/store/migration_direct_upgrade_test.go` (288 → 894 lines) —
  `TestDirectUpgradeMatrixV1ToV14` (AC-G-1 / FR-G.1): 13 named rows v1…v13,
  each built at the genuine frozen source layout, seeded, opened exactly once
  through normal `Open`, and asserted for schema 14, exact bytes, per-table row
  counts, the v14 invariant manifest, and fail-closed guards.
- `openspec/changes/audit-register-closure/tasks.md` — G.1/G.2/G.3/G.7 checked.

**Strict-TDD cycle (G matrix slice):**

| Phase | Evidence |
|---|---|
| RED | Matrix + all seed/assert helpers written FIRST (contract-guard conformance pattern: no production change permitted). |
| RED observed | v5/v6 subtests failed — **harness bug, NOT a migration defect**: the snapshot captured receipt bytes under hardcoded `receipt-hash-v7` while the v5/v6 seed writes `receipt-hash-v5`. Fixed to be version-aware. |
| GREEN | All 13 rows pass immediately against the frozen implementation — the contract-guard proof that the migration chain already converges every legacy version to v14 with exact preservation. No production Go/TS, DDL, or migration file touched. |
| TRIANGULATE (G.7) | (a) Builders derive from frozen migration-era schemas/tests: `v1SchemaDDL`, `openV2Schema`…`openV13Schema`, and the REAL production `migrateV5ToV6`/`migrateV11ToV12` (v6/v12 have no dedicated builder) — zero cloned DDL. (b) Failure-injection does not alter the one-transaction-per-step model: this slice adds NO injection; the G.4/G.5 trigger seam lives in the test DB only and its bounded claim (rollback at the schema_version advance + re-run convergence) is documented in the file header. (c) Assertions verify structural invariants + data preservation, never version numbers alone: baseline identity bytes, receipt action/payload bytes (the receipts table is REBUILT at v5→v6, v7→v8 and v12→v13 — bytes prove every rebuild), `policy_rule_json` bytes (v7+), `rule_links.ref` bytes (v13+), per-table row counts for EVERY table the source version owns (zero loss AND zero duplication), the full v14 table/index/trigger manifest, `rule_links.version`/`effective_at`, `idx_rule_links_ref`, zero `receipts_v*` staging tables, and representative immutable/no-delete aborts. |

**Matrix content (G.1/G.2/G.3):**

- Fixture per version; builder asserts `readSchemaVersion == source version`
  before seeding; raw handle closed before normal `Open`.
- Seeds at the FIRST version each feature exists, carried forward by the
  threshold model (v13 row carries every representative): baseline observation
  (v1 exact 21-col shape; v2 `saveV2Row`; v3+ `saveV3Row`; v7+ 41-col insert
  writing `policy_rule_json` INLINE because the v7 immutability trigger guards
  that column), approval/membership (v3, frozen companies→memberships→
  approval_events FK chain), judgment (v4, `proposedJudgment` +
  `insertJudgment`), receipt/key (v5 `insertV5ReceiptRowRaw`, v7+
  `insertV7ReceiptRowRaw`), materiality/close + reconciliation (v6, one closed
  period + one proposed reconciliation + event), structured policy data (v7),
  evidence object (v8), retention policy (v9), hold (v10), purge lifecycle
  (v11, request + immutable lifecycle event), purge execution (v12, intent
  row), review decision + legacy unversioned rule link (v13).
- Assertions: schema_version == 14; baseline bytes; receipt bytes; policy JSON;
  rule-link ref; per-table counts (`tablesAtVersion` covers every table the
  source owns); v14 invariant manifest (all per-version helper lists +
  structured-link columns + `idx_rule_links_ref` + no `receipts_v*`); fail-closed
  guards (IMMUTABLE_OBSERVATION update/delete, IMMUTABLE_APPROVAL_EVENT v3+,
  IMMUTABLE_JUDGMENT v4+, IMMUTABLE_LIFECYCLE_EVENT v11+).

**Gates (this slice):**

| Gate | Result |
| --- | --- |
| Focused: `go test ./internal/store -run TestDirectUpgradeMatrixV1ToV14` | ok (13/13 rows) |
| `go test ./internal/store` | ok (35s; per-step migration tests + G.4/G.5 stay green) |
| `go test ./...` | ok except the 6 PRE-EXISTING Q link-method diagnostics in internal/server (LinkEvidence/LinkRules/LinkRuleVersion × TestCrossTenantMatrix + TestCrossTenantMutationNoSideEffect — unchanged from before this slice, require separate approval) |
| `go vet ./...` | clean |
| `gofmt -l .` | clean |
| `go test ./internal/core -run TestGoldenVectorsGo` | ok |
| `npm test` | 385/385 passed |
| `npm run typecheck` | clean |

**Workload / PR boundary — LINE-BUDGET OVERRUN (disclosed):**

- This slice adds **≈ 606 authored lines to `migration_direct_upgrade_test.go`**
  (matrix section 302→894) plus 5 tasks.md checkbox edits — ABOVE the parent's
  400-line cap for this continuation slice.
- Why the full matrix cannot fit 400 lines: the code size is driven by the seed
  functions (every feature branch is needed — a v13 row carries all
  representatives) and the assertions, NOT by the row count; trimming rows
  saves ≈ 13 lines but breaks AC-G-1's "all v1…v13 rows exist" contract, and
  trimming assertions breaks G.3 steps 6–7. The design's own per-slice estimate
  for the matrix portion was **≈ 500–650 lines** (PR G total 700–950 incl. the
  288-line interruption/provenance slice already delivered). 507 of the 606
  lines are code; 60 are comments.
- Options for the parent: (1) accept the disclosed overrun — matrix complete,
  PR G evidence fully landed, chain proceeds to PR Z; or (2) direct a
  re-budgeted split — a partial matrix leaves G.1/AC-G-1 red and re-runs all
  seeding work later. No production file, schema DDL, or migration code is
  touched either way.

**Remaining implementation rows in tasks.md (unchanged, unchecked):** Z.1…Z.5,
E.1…E.3, cross-cutting checklist rows, DoD rows. Parent-owned rows untouched
byte-for-byte.

## PR Z — Operability evidence and reconciliation (Z.1…Z.5) (APPENDED by apply continuation)

**status:** DELIVERED — Z.1…Z.5 complete and green. The audit register Z row
flip to bounded PASS remains the evidence-pass slice (E.1, NFR-XC-5); this
slice only reconciles the stale drill-deferral claims in the two named
architecture/product docs and adds the evidence document + structural readback.

**Files changed (this slice):**

- `internal/store/operability_documentation_test.go` (NEW, 258 lines) —
  `TestOperabilityDocumentationReadback` (AC-Z-1..AC-Z-4, FR-Z.1..FR-Z.3, FD-3):
  structural readback over the four Z-slice documents (operability-evidence.md,
  evidence-object-v0.7.md, initial-market-and-v1-gate.md, audit register Z row
  read-only).
- `docs/architecture/operability-evidence.md` (NEW, 61 lines) — the five
  required sections (Purpose and evidence boundary; Delivered capabilities and
  executable evidence; Qualitative recovery objectives; Operational boundaries
  and non-claims; Evidence maintenance), every capability paired with an
  executable citation, qualitative UNKNOWN RTO/RPO statement (AC-Z-1/AC-Z-2).
- `docs/architecture/evidence-object-v0.7.md` (+9/−2) — the stale
  "Production object-store operations" deferred bullet split: repository-local
  snapshot/restore + marked-copy corruption drills DELIVERED with drill.go/
  drill_test.go citations; cloud/remote, encryption-at-rest/TDE, deployment-owned
  numerical recovery objectives, external production drills explicitly unproven
  (AC-Z-3, NFR-Z.1).
- `docs/product/initial-market-and-v1-gate.md` (2 rows, +4/−2) — G-5 removes
  only "production backup/restore drills" from the DEFERRED list and cites
  operability-evidence.md (retains cloud/remote storage, scheduler executor,
  OCR/content search); G-6 records restore/corruption drills as
  repository-delivered with drill_test.go citations and retains a bounded
  PARTIAL (race/fuzz/cross-tenant closure not all closed) (AC-Z-3).
- `openspec/changes/audit-register-closure/tasks.md` — Z.1…Z.5 checked.

**Strict-TDD cycle (Z slice):**

| Phase | Evidence |
|---|---|
| RED | `TestOperabilityDocumentationReadback` written FIRST (structural readback contract). First run RED: `read docs/architecture/operability-evidence.md: no such file` (missing doc) + stale phrases present in evidence-object-v0.7.md:58-59 and G-5/G-6. |
| GREEN | Created operability-evidence.md; split the evidence-object deferred bullet; reconciled G-5/G-6. Readback test green (7/7 subtests, 0.02s). Contract-guard conformance: no production Go/TS touched. |
| TRIANGULATE (Z.5) | (a) Recovery-objective wording is qualitative: doc states snapshot/restore/verify/corruption-detection are proven mechanisms and that numerical RTO/RPO targets are deployment/business-owned and UNKNOWN until an owner records them; (b) reviewer grep over the three changed docs confirms ZERO numeric recovery targets (RTO/RPO/SLA/SLO+value, durations, percentiles); (c) PASS scope bounded to repository-verifiable capabilities: boundaries section lists no deployment backup schedule, retained recovery points, external drill completion, guaranteed recovery time/data loss, encryption-at-rest/TDE, cloud storage, or production readiness claim; the audit register Z row was NOT flipped (that is E.1). |
| REFACTOR | gofmt -l clean; go vet clean. |

**Readback test details (structural, observation-only per NFR-XC-4):**

- Required headings + qualitative statement (contains UNKNOWN/RTO/RPO/deployment/business-owned).
- Executable citations resolve (EC-3): every cited `internal/...`/`cmd/...` Go
  file exists and every cited `TestXxx` token is declared in a cited `_test.go`
  (go/parser over FuncDecls): doctor (doctor_test.go:67/:122/:171,
  object_hardening_test.go:242/:300/:319/:634, api_test.go:363/:425,
  main_test.go:261), snapshot/restore/corruption (drill.go::CreateDrillSnapshot
  :134; drill_test.go:162/:256/:287/:322/:353/:404/:487/:678/:762), tenant
  export (evidence_export_test.go:129/:362/:507, purge_http_test.go:637).
- Stale drill-deferral phrases absent from the named blocks (FR-Z.3);
  split statements present with citations (NFR-Z.1).
- Numeric recovery-target patterns rejected (FD-3). Deliberate scope decision:
  single-letter unit regexes (`\b\d+(\.\d+)?\s*(ms|s|h|m|d|w)\b`) are NOT used
  because they false-positive on legitimate technical text already present
  (e.g. "SHA-256 hex" in evidence-object-v0.7.md); the contract rejects
  recovery-target-shaped patterns only (acronym+value, explicit duration words,
  percentages). Audit register Z row is read-only here (no numeric target; the
  bounded PASS flip is E.1).

**Gates (this slice):**

| Gate | Result |
| --- | --- |
| Focused: `go test ./internal/store -run TestOperabilityDocumentationReadback` | ok (7/7 subtests) |
| `go test ./internal/store` | ok (36.7s) |
| `go test ./...` | ok except the 6 PRE-EXISTING Q link-method diagnostics in internal/server (LinkEvidence/LinkRules/LinkRuleVersion × TestCrossTenantMatrix + TestCrossTenantMutationNoSideEffect — unchanged from before this slice, require separate approval) |
| `go vet ./...` | clean |
| `gofmt -l .` | clean |
| `go test ./internal/core -run TestGoldenVectorsGo` | ok |
| `npm test` | 385/385 passed |
| `npm run typecheck` | clean |

**Workload / PR boundary:** this slice = Z.1…Z.5 only: 258-line readback test +
61-line evidence doc + 2 small reconciliation edits (+13/−6 across the two docs)

- 5 tasks.md checkbox edits. No production Go/TS, schema, API, or contract
version touched; no audit register edit (bounded PASS flip is the evidence-pass
slice E.1). Remaining implementation rows in tasks.md (unchanged, unchecked):
E.1…E.3, cross-cutting checklist rows, DoD rows. Parent-owned rows untouched
byte-for-byte.

**Evidence SHA-256 (slice):** computed over the four Z-slice artifacts
(operability_documentation_test.go, operability-evidence.md,
evidence-object-v0.7.md, initial-market-and-v1-gate.md) — see the returned
Result Contract envelope for the exact digest and changed-line count.

## PR evidence-pass — Audit register closure and whole-change gates (E.1…E.3) (APPENDED by apply continuation)

**status:** DELIVERED — E.1 (five audit rows G/J/L/Q/Z flipped to bounded PASS with
per-claim citations), E.2 (whole-change gates green in config verify_order), E.3
(register-only deltas confirmed) complete. Q's focused cross-tenant matrix is now
FULLY green (the three link-method rows LinkEvidence/LinkRules/LinkRuleVersion were
resolved by the parent-approved scope-first propagation to MCP/CLI surfaces — see
the orchestrator context; `go test ./internal/server -run TestCrossTenantMatrix` ok,
full `go test ./...` ok). This is the final implementation slice of the chain.

**Structured status consumed:** parent-injected context states the native attempt
token `sha256:7b5e85d0d540114bd288731c1c90d44a525408c34a1da68e286a349377fcfd53`
(request `pi-sdd080-apply-20260302-006`, work unit `audit-register-closure-evidence-pass`)
was already acquired by the orchestrator; per delegation, apply did NOT acquire or
settle. Native dispatcher status: artifactStore openspec, actionContext repo-local
edit roots safe (same known `nextRecommended: spec` quirk as prior slices — the
engine does not index spec.md; empty blockedReasons; not treated as a blocker).

**Strict-TDD cycle (evidence-pass slice — proportionate, checks before prose):**

| Phase | Evidence |
|---|---|
| RED/check-first | Baseline gates run BEFORE any prose edit: `npm run typecheck`, `go vet ./...`, `gofmt -l .`, `go test ./...` (10/10 ok), `npm test` (385/385), `TestGoldenVectorsGo` ok. Reviewer greps before edits: no float money (the two `float64` hits in `internal/store/store.go:2854/:3005` are pre-existing reconstructibility confidence scores — git-blamed to 2026-08-04, NOT money, NOT this change); zero TS files changed (no `any`); no DDL (`CREATE/ALTER/DROP` absent from diff); override spellings in changed production files are denial/negative comments only; no numeric recovery target in changed docs (readback patterns + manual grep; hits are FD-3/SHA-256 references). |
| GREEN (E.1) | Five audit rows updated to bounded PASS with unique executable citations (EC-1/EC-3): G cites `TestDirectUpgradeMatrixV1ToV14` + interruption/reopen tests + provenance §; J cites `TestConsolidatedVersionedPolicyMatrix` + no-override structural/behavioral tests + override-absence §, with explicit non-identity/non-authorization boundary (block I retained); L cites `TestIdempotencyReplayMatrix` + MCP/CLI replay + interrupted-reservation file + sync tests; Q cites `TestCrossTenantMatrix*` + scope-first enforcement files + leakage probes; Z is `PASS for repository-demonstrated operability / RISK for deployment objectives` citing `operability-evidence.md` + doctor/drill/export evidence, RTO/RPO explicitly UNKNOWN (FD-3). |
| GREEN (E.2) | Full verify_order re-run AFTER the edit: typecheck OK, vet OK, gofmt OK, `go test ./...` 10/10 ok (store re-ran 78.3s — readback test re-validated the register), `npm test` 385/385, golden vectors ok. `TestOperabilityDocumentationReadback` 7/7 subtests pass against the edited register (Z row numeric-free). |
| TRIANGULATE (E.3) | `git diff docs/due-diligence/2026-08-product-architecture-audit.md` = exactly 10 changed lines (5 removed + 5 added), ALL five G/J/L/Q/Z rows; zero adjacent-row deltas (block I identity readiness, production-readiness caveats, block N, dated reconciliation paragraphs byte-identical). |

**Files changed (this slice — evidence-pass artifacts only):**

| File | Change |
| --- | --- |
| `docs/due-diligence/2026-08-product-architecture-audit.md` | 5 rows (G 111–122, J 159–175, L 186–194, Q 277–296, Z 420–433) flipped to bounded PASS with per-claim citations (AC-L-8, AC-J-5, AC-Q-5, AC-G-5, AC-Z-4, EC-1/EC-3); Z carries the deployment-objective caveat (FD-3) |
| `openspec/changes/audit-register-closure/tasks.md` | E.1/E.2/E.3 checked `[x]`; cross-cutting rows Strict-TDD, money, scope, non-authorization, golden parity, no-`any`/secrets, no-new-production-surface checked `[x]`; DoD rows 145/146/147 checked `[x]`; commit row 134 and all parent rows left unchecked (see below) |
| `openspec/changes/audit-register-closure/apply-progress.md` | This appended section (merge, no overwrite) |

**Gate results (final):**

| Gate | Result |
| --- | --- |
| `npm run typecheck` | clean |
| `go vet ./...` | clean |
| `gofmt -l .` | clean |
| `go test ./...` | 10/10 packages ok (cmd 19.8s, server 75.1s, store 78.3s incl. Q + G matrices) |
| `npm test` | 385/385 (26 files) |
| `go test ./internal/core -run TestGoldenVectorsGo` | ok |
| `go test ./internal/store -run TestOperabilityDocumentationReadback` | ok (7/7 subtests, incl. audit Z row numeric-free) |
| Reviewer greps (E.2) | float money: none introduced; TS `any`: zero TS changed + typecheck; DDL/schema: none in diff; override: denial-only mentions; numeric recovery targets: none |

**Deviations / reconciliations:**

1. **Audit diff scope (E.3) is exactly the five rows.** The dated `Reconciliation (2026-08-07)` paragraph and block N's deferral list still mention "production object-store operations (backup/restore drills …)" — that is historical dated block-N wording, NOT the Z block (420–433), and E.3 pins the diff to the five rows. The Z-slice reconciliation of stale drill deferrals was completed in the named Z targets (evidence-object-v0.7.md, v1-gate G-5/G-6) in the prior slice. Left byte-for-byte per E.3.
2. **Task 134 (conventional commit) left unchecked.** This delegation explicitly forbids committing (parent: "Do NOT commit, push, or open a PR"). VCS/commit milestone is orchestrator-owned (same handling as PR J's cross-cutting note). Reported as a deferred parent action, not evidenced here.
3. **DoD row 145 ("All tasks checked") checked with a documented boundary:** parent-owned rows (148, 152–154) remain unchecked by the ownership contract, and task 134 is VCS-deferred; the check means all implementation-owned tasks are complete and every AC's mapped proof is green (formal AC validation remains the verify phase's parent-owned job). Parent rows preserved byte-for-byte.
4. **No new/adjusted test evidence was strictly needed for this slice** (parent: "add/adjust only if strictly needed"): `TestOperabilityDocumentationReadback` already machine-guards the Z row and numeric-freeness; the G/J/L/Q citation contract is enforced by the register edit itself and E.3's diff check. The verify phase re-validates ACs per the traceability table.
5. **Q completeness note:** all 46 canonical API operations now carry cross-tenant rows (43 green previously + 3 link rows green after the MCP/CLI scope-first propagation). No diagnostic rows remain. This is the evidence basis for the Q PASS row; the scope-first production changes were owner-approved per the prior Q-slice record and are not part of this slice's diff.

**Workload / PR boundary — within budget:** authored lines this slice ≈ 98 (5 register rows + 13 tasks.md checkbox flips + ~80 apply-progress lines). Well under the parent's 400-line cap. Zero production Go/TS, zero schema/DDL, zero contract-version changes in this slice; the audit register diff is the only doc artifact changed.

**Evidence SHA-256 (slice):** computed over the three evidence-pass artifacts
(docs/due-diligence/2026-08-product-architecture-audit.md,
openspec/changes/audit-register-closure/tasks.md,
openspec/changes/audit-register-closure/apply-progress.md) — exact digest in the
returned Result Contract envelope.

**Remaining tasks (unchecked — parent lifecycle + VCS):**

- [ ] 134 Conventional commit per atomic milestone (orchestrator-owned VCS; apply forbidden to commit)
- [ ] 148 Chain fully merged to main via stacked-to-main (parent)
- [ ] 152–154 Bounded review per PR boundary, verify phase, archive (parent)

Implementation-owned task set is COMPLETE. Parent-owned rows untouched byte-for-byte.

## Delivery — review, merge, push to main (APPENDED by orchestrator, post-apply)

**status:** DELIVERED — the parent-owned lifecycle gates are complete.

**Review (native bounded, one candidate per RDD workspace freeze):**

| Lineage | Candidate | Result |
|---|---|---|
| `review-bd7751cd5b4e8293` | Frozen Q/G/Z/evidence-pass workspace candidate (19 files, 3420 lines, high → 4R) | 1 CRITICAL `R1-caller-controlled-scope` → ONE bounded correction (audit Q row bounded PASS wording + caller-asserted scope documented in httpQueryScope + matrix HTTP invocators note + relations seed + 400/404/409 denial tightening + dead-branch removal) → approved, pre-commit gate ALLOW, commit `2f2f251` |
| `review-114d210a8af75ab0` | Merged candidate after origin advanced (committed range 53e95d9..HEAD, 20 files) | WARNING/SUGGESTION only → approved; delivery range exceeded genesis (ROADMAP) → re-reviewed on the full delivery range |
| `review-983059cb39d4ef37` | Full delivery range f997abc..HEAD (21 files, 3492 lines, high → 4R) | WARNING/SUGGESTION only → approved, pre-push gate ALLOW |

**Delivery:** chain merged to main as `d7f89d2` (parents `2f2f251` + `f997abc`), pushed to origin/main (fast-forward `f997abc..d7f89d2`, 2026-08-15). Origin had advanced mid-review with `f997abc` (#25 README-align); the user chose merge + re-review (non-destructive) over force-push/RDD-disable.

**Remaining:** verify phase (parent-owned, task 153) → archive (task 154).
