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
