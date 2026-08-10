# Apply Progress — v1-readiness

> Phase: apply · Artifact: apply-progress · Status: in progress (PR-2 of 5)
> Inputs: spec + design + tasks (read from `openspec/changes/v1-readiness/`). Strict TDD active (`openspec/config.yaml` `phases.apply.strict_tdd: true`).
> Change delivered as CHAINED PRs per the Review Workload Forecast (PR 1 → PR 2 → PR 3 → PR 4 → PR 5).

## Structured status consumed

Produced via the native dispatcher (`gentle-ai sdd-status v1-readiness --cwd . --json --instructions`):

- `changeName: v1-readiness`, `artifactStore: openspec`, planning `repo-local`.
- `actionContext: {mode: repo-local, workspaceRoot: <repo>, allowedEditRoots: [<repo>]}` — edit scope safe.
- **Native quirk (flagged):** the engine reports `applyState: blocked`, `nextRecommended: spec`, `dependencies.specs: blocked` with `blockedReasons: []` — because the engine does NOT index `spec.md` (file exists; `artifactPaths.specs` is empty). The engine's own apply instructions still say "Implement only unchecked tasks"; all required artifacts (proposal/spec/design/tasks) are present and readable. Per the parent's explicit apply directive for PR-2 (and empty `blockedReasons`), apply proceeded; the discrepancy is recorded here rather than treated as a real blocker.
- Task progress at entry: 43 implementation tasks, 0 checked (PR-1 did NOT persist checkboxes).

## PR-1 state (reconciled, not re-implemented)

PR-1 (commit d02ee75) delivered the W4 metric core; the parent's gatekeeper approved it. No `apply-progress.md` existed for PR-1 (ENOENT on `openspec/changes/v1-readiness/apply-progress.md`; Engram HTTP server not responding — `mem_search` for `sdd/v1-readiness/apply-progress` failed with connection refused, so no engram copy exists either). PR-1's tasks checkboxes were left `- [ ]`; this slice reconciled 4.1–4.7 against verifiable evidence:

| Task | Evidence |
| --- | --- |
| 4.1/4.2/4.3 pure core (FZ-1/FZ-2/ratio) | `internal/core/reconstructibility.go` + `reconstructibility_test.go`; `go test ./internal/core` green |
| 4.4 TS mirror | `core/reconstructibility.ts` (exported from `core/index.ts`) + `core/__tests__/reconstructibility.test.ts`; `npm test` green (385 tests) |
| 4.5 golden vectors + dispatchers | `testdata/golden/reconstructibility-eligibility.json`, `reconstructibility-classifier.json`; `go test ./internal/core -run TestGoldenVectorsGo` green; TS dispatcher case present in `core/__tests__/golden.test.ts` |
| 4.6 store read | `internal/store/reconstructibility_store.go` + test; `go test ./internal/store` green |
| 4.7 server service | `internal/server/reconstructibility_service.go` + test; `go test ./internal/server` green |

**PR-1 defect found + fixed (one token, documented):** `npm run typecheck` was RED after PR-1 — `core/__tests__/golden.test.ts:963` added the `case "reconstructibility"` dispatch but never extended the `GoldenCase.contract` union, so `tsc --noEmit` failed TS2678. Fixed by adding `| "reconstructibility"` to the union (the only TS change in this slice). `npm run typecheck` now green.

## PR-2 — W4 adapters (THIS SLICE)

Strict TDD: RED tests written first (referencing not-yet-existing production code), then GREEN implementation, then TRIANGULATE, then full gates.

### TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| API method | `internal/server/reconstructibility_http_test.go` | Integration | ✅ server 19.15s ok | ✅ Written (compile RED) | ✅ Passed | ✅ eligibility-through-adapter + zero-denominator + determinism | ✅ gofmt/vet clean |
| HTTP route | `internal/server/reconstructibility_http_test.go` | Integration | ✅ (same run) | ✅ Written (404 RED) | ✅ Passed | ✅ 7 missing-field cases + A/B isolation + token guard | ✅ gofmt/vet clean |
| MCP tool | `internal/server/mcp_reconstructibility_test.go` | Integration | ✅ (same run) | ✅ Written (-32601 RED) | ✅ Passed | ✅ unknown args + strict scope + A/B isolation + catalog wording | ✅ gofmt/vet clean |
| MCP tool count | `internal/server/mcp_test.go` | Unit | ✅ (same run) | ✅ 57→58 fails | ✅ Passed | ➖ Single (closed count) | ✅ |
| CLI command | `cmd/drenyra-engram/reconstructibility_test.go` | Integration (real binary) | ✅ cmd 8.62s ok | ✅ Written (exit-2 RED) | ✅ Passed | ✅ zero-denominator + 5 usage errors + --company-id/--organization overrides + usage text | ✅ gofmt/vet clean |
| Adapter parity | `internal/server/reconstructibility_http_test.go` | Integration | ✅ (same run) | ✅ Written | ✅ Passed | ✅ MCP bytes == HTTP body−newline (one shared store) | ✅ |
| TS gate fix (PR-1 defect) | `core/__tests__/golden.test.ts` | Unit (typecheck) | ❌ pre-existing RED | N/A (CI RED) | ✅ typecheck green | ➖ Single (union member) | ✅ |

### Test summary (this slice)

- **Tests written**: 14 new top-level tests (API 3, HTTP 4, MCP 4, CLI 5, parity 1 — some count overlap) plus table subtests (7 missing-field, 5 scope-decode, 5 CLI usage-error cases).
- **Tests passing**: all focused + full suites green.
- **Layers**: Integration (real store + real binary + httptest), Unit (tool count, catalog wording).
- **Approval tests**: none needed (no refactor of existing behavior).
- **Pure functions created**: none (adapters only — the pure core is PR-1's, untouched).

### Files changed (this slice)

| File | Change |
| --- | --- |
| `internal/server/api.go` | `API.Reconstructibility(ctx, scope)` — the canonical method all adapters delegate to (design D-1/D-3); asserts the store implements the `VerificationStore` seam, fails closed `RECONSTRUCTIBILITY_UNAVAILABLE` otherwise |
| `internal/server/reconstructibility_http.go` | NEW — `handleReconstructibility` (dedicated exact-scope parser, ALL FOUR query fields required, no `companyId := ruc` fallback), `validateReconstructibilityAdapterScope`, `reconstructibilityCode`, `writeReconstructibilityError` (INVALID_*→ 400, RECONSTRUCTIBILITY_* → 500) |
| `internal/server/http.go` | Route `GET /accounting/reconstructibility` under `requireToken`, registered with the accounting read group (before the generic `/v1/*` routes) |
| `internal/server/mcp.go` | Catalog entry `accounting_reconstructibility` (read-only wording), dispatch case (strict args decode + strict scope decode + adapter scope validation), `decodeStrictScope` helper |
| `internal/server/mcp_test.go` | Tool count 57 → 58 (13 engram_*+ 45 accounting_*) |
| `internal/server/reconstructibility_http_test.go` | NEW — shared seeding fixture (`saveMaterialDecision`/`seedReconstructibilityDecision`), API + HTTP + parity tests |
| `internal/server/mcp_reconstructibility_test.go` | NEW — MCP tool dispatch, fail-closed, isolation, catalog wording |
| `cmd/drenyra-engram/main.go` | Dispatch `case "reconstructibility"`, top-level usage line + `--company-id`/`--organization` flag docs, `--objects` flag doc mention |
| `cmd/drenyra-engram/reconstructibility.go` | NEW — `cmdReconstructibility` (JSON to stdout; exit 0 on zero denominator; exit 2 with stable code on stderr for invalid scope/period or unavailable/corrupt read; `companyId := ruc` unless `--company-id`; `--organization` override) |
| `cmd/drenyra-engram/reconstructibility_test.go` | NEW — CLI smoke, zero-denominator exit 0, usage-error exit 2 matrix, explicit-company-id triangulation, usage text |
| `core/__tests__/golden.test.ts` | ONE token: `GoldenCase.contract` union + `"reconstructibility"` (completes PR-1's dispatcher; the only TS change) |

### Tests added

- `internal/server/reconstructibility_http_test.go`: `TestAPIReconstructibilityFrozenResult`, `TestAPIReconstructibilityFZ1EligibilityThroughAdapter`, `TestAPIReconstructibilityZeroDenominator`, `TestHTTPReconstructibilityReturnsFrozenResult`, `TestHTTPReconstructibilityScopeIsolation`, `TestHTTPReconstructibilityMissingFieldsFailClosed` (7 subtests), `TestHTTPReconstructibilityTokenGuard`, `TestReconstructibilityAdapterParityMCPHTTP`.
- `internal/server/mcp_reconstructibility_test.go`: `TestMCPReconstructibilityValidCall`, `TestMCPReconstructibilityUnknownArgumentsFailClosed`, `TestMCPReconstructibilityScopeObjectFailsClosed` (5 subtests), `TestMCPReconstructibilityScopeIsolation`, `TestMCPReconstructibilityCatalogReadOnlyWording`.
- `cmd/drenyra-engram/reconstructibility_test.go`: `TestCLIReconstructibilitySmoke`, `TestCLIReconstructibilityZeroDenominatorExitZero`, `TestCLIReconstructibilityUsageErrorsExitTwo` (5 subtests), `TestCLIReconstructibilityExplicitCompanyID`, `TestCLIReconstructibilityUsageText`.

### Gate results (PR-2)

| Gate | Result |
| --- | --- |
| `npm run typecheck` | ✅ clean (after the one-token PR-1 union fix) |
| `go vet ./...` | ✅ clean |
| `gofmt -l` (touched files) | ✅ clean (no output) |
| `go test ./internal/server/ ./cmd/drenyra-engram/` | ✅ ok (server 21.2s, cmd 9.2s) |
| `go test ./...` | ✅ all packages ok |
| `go test ./internal/core -run TestGoldenVectorsGo` | ✅ ok |
| `npm test` | ✅ 385 tests / 26 files passed (unchanged count — TS behavior untouched) |

### Deviations / reconciliations (PR-2)

1. **HTTP + MCP scope shape (parent summary vs binding design):** the parent brief's URL example (`?ruc=&organizationId=&period=`) omits `companyId`; the binding design D-1 / task 4.10 REQUIRES all four exact-scope fields on HTTP (no `companyId := ruc` fallback) and the MCP scope carries the full exact scope. The binding artifacts won (the parent's brief itself names them authoritative); the A/B isolation requirement in the brief also only makes sense with distinct company identities. The HTTP route and MCP tool therefore require `organizationId`, `companyId`, `ruc`, `period`.
2. **CLI signature:** the parent brief shows `reconstructibility <ruc> --period <YYYYMM> [--db <path>]`; design D-1 / task 4.8 additionally define optional `--company-id`, `--organization`, `--objects`. Implemented the full D-1 surface (the brief's form is the happy path; defaults preserve `companyId := ruc` under the fixed CLI organization).
3. **CLI output formatting:** the CLI emits pretty-printed JSON (`emit` — the repo's CLI convention); MCP/HTTP emit compact JSON. Parity is asserted on the canonical JSON DOCUMENT (`json.Marshal` equality of the typed result; MCP↔HTTP byte-identical on one shared store). Whitespace is not part of the frozen bytes contract.
4. **Store-open on a missing DB path creates an empty store** (normal SQLite/CLI behavior) — a "missing database" is therefore a valid zero-denominator result, not an error. The unavailable-read test uses a corrupt (non-SQLite) file instead → exit 2 `RECONSTRUCTIBILITY_UNAVAILABLE`.
5. **`writeReconstructibilityError`** follows the `closeCode`/`objectCode` precedent so the stable G-10 codes stay on the wire instead of collapsing to the generic `INVALID`/`INTERNAL`.

### Workload / PR boundary

- PR-2 changed ~1,150 lines incl. tests (≈1,060 Go + 1 TS token). Well under the 400-line guidance per slice is exceeded only because the estimate counts test scaffolding; the slice itself is the planned PR-2 boundary (W4 adapters only). No W1/W2/W3 code touched.
- Core/service/store semantics untouched (verified: no edits to `internal/core/reconstructibility.go`, `internal/store/reconstructibility_store.go`, `internal/server/reconstructibility_service.go`).

## Remaining tasks (unchecked — parent lifecycle + future slices)

Implementation-owned rows still pending in `tasks.md` (PR-3/4/5):

- [ ] 1.1 … 1.7 (W1 fuzz targets, seed corpora, Makefile `fuzz-ci`, CI wiring) — PR-4
- [ ] 2.1 … 2.7 (W2 doctor checks + copy-only corruption/restore drills) — PR-3
- [ ] 3.1 … 3.4 (W3 G-7 playbook + boundary tests) — PR-5

Parent-owned rows (deferred lifecycle actions, preserved byte-for-byte):

- [ ] Review Workload Guard decision recorded; change delivered as chained PRs per the forecast split.
- [ ] Bounded post-apply review of the chained diff (native review per repository policy), then `sdd-verify`, then `sdd-archive`.
- [ ] Start or reuse bounded review for each PR boundary after its normalization + candidate freeze.
- [ ] After the final PR merges, run `sdd-verify` against AC-1…AC-11; remediate only through the bounded correction path.
- [ ] Archive only when verify reports all criteria green and the chain is fully merged.

## Cross-cutting checklist status (this slice)

- Conventional commits per atomic milestone, no AI attribution — VCS owned by the orchestrator; NOT committed here (slice instruction).
- Money stays whole int64 cents / BigInt cents — no money value crosses any adapter; percentage is integer math (PR-1 core, untouched).
- Scope structural + fails closed — HTTP/MCP/CLI exact-scope parsers, isolation tested (company A vs B on HTTP and MCP; custom company on CLI).
- Non-authorization boundary — every new surface is read-only; no principal/action/approval/recovery field accepted; MCP catalog wording test asserts the negation.
- No `any` in TS — untouched (typecheck green).
- Schema version stays 14 — no store/migration change in this slice.
