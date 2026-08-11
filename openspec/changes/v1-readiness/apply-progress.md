# Apply Progress — v1-readiness

> Phase: apply · Artifact: apply-progress · Status: in progress (PR-4 of 5)
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

Implementation-owned rows still pending in `tasks.md` (PR-5 only):

- [ ] 3.1 … 3.4 (W3 G-7 playbook + boundary tests) — PR-5

W1 (1.1–1.7) is complete in this PR-4 slice; W2 (2.1–2.7) landed in PR-3 and its checkboxes were reconciled here against commit caf5da4 evidence (see PR-4 reconciliation 2).

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

---

## PR-4 — W1 fuzz targets, seed corpora, bounded CI (THIS SLICE)

PR-4 (fourth chained slice) delivered W1 (G-6 fuzz, AC-1/AC-2, FR-1/FR-2/FR-3, NFR-2) per design D-10 and tasks 1.1–1.7. All three fuzz targets, their committed seed corpora, the root `Makefile` `fuzz-ci` gate, and the bounded CI workflow landed. The slice ALSO found and fixed one real invalid-success defect in the comprobante parser (see reconciliation 1) — the fuzz harnesses paid for themselves on the first run.

### Structured status consumed

Parent launch for the PR-4 slice (chained-PR apply, W1 scope only). OpenSpec artifact store (`openspec/config.yaml`); `actionContext: repo-local` with full-repo edit roots — safe. The native dispatcher quirk recorded in PR-2 is unchanged (engine does not index `spec.md`); all required artifacts present and readable. Review Workload Guard decision: chain approved by the orchestrator (`auto-chain` mode), PR boundary = W1 exactly; no W2/W3/W4 code touched.

### TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 1.1 harness | `internal/core/comprobante_fuzz_test.go` | Unit (same-package fuzz) | ✅ core suite green (baseline captured) | ✅ harness written first (no production change) | ✅ green on existing parser | ✅ 7 committed seeds + 9 f.Add seeds | ✅ gofmt/vet clean |
| 1.2 harness | `internal/core/receipt_fuzz_test.go` | Unit (same-package fuzz) | ✅ (same run) | ✅ harness written first | ✅ green | ✅ six frozen versions v0.4.0…v0.10.0 rotated per input | ✅ gofmt/vet clean |
| 1.3 harness | `internal/search/search_fuzz_test.go` | Unit (same-package fuzz) | ✅ search suite green | ✅ harness written first | ✅ green | ✅ 8 committed seeds + 6 f.Add seeds | ✅ gofmt/vet clean |
| 1.4 corpus | `internal/core/testdata/fuzz/*`, `internal/search/testdata/fuzz/*` | Corpus (replays via `go test`) | N/A (new files) | N/A (seeds, not behavior) | ✅ `go test ./internal/core/ ./internal/search/` green incl. seed replay | ✅ 21 entries across 3 targets | ✅ corpus format verified empirically (see reconciliation 3) |
| 1.5 regression | `internal/core/fuzz_regression_test.go` + `TestFuzzCoreCorpusManifestNeverDeleted` / `TestFuzzSearchCorpusNeverDeleted` | Unit | ✅ (same run) | ✅ `TestParseComprobanteXMLTrailingGarbageAmountRejected` RED on the unfixed parser (probe proof + written before the fix) | ✅ GREEN after `strconv.ParseInt` + ISO 4217 guard fix | ✅ 7 malformed-amount cases + positive control (`1284.30 PEN` still parses) | ✅ named edge regressions (wrong encoding, truncated, deep nesting, CDR, v0.9 conditional, tokenizer edges) |
| 1.6 Makefile/CI | `internal/core/fuzz_ci_contract_test.go` + root `Makefile` + `.github/workflows/fuzz.yml` | Build contract | ✅ (same run) | ✅ contract test written first (asserts exact 3×30s shape) | ✅ Makefile + workflow satisfy it | ✅ exactly-three pins + no-unbounded + workflow invocation | ✅ Makefile comment avoids literal budget token (kept the count strict) |
| 1.7 sweep | `internal/core/fuzz_corpus_test.go` (`TestFuzzCorpusSecuritySweep`) | Unit (filesystem) | ✅ (same run) | ✅ sweep written before corpus hygiene fixes | ✅ green | ✅ decoded-content scan + exact 1 MiB / 1 MiB−1 pins + README scan | ✅ gofmt clean |

### Test summary (this slice)

- **Tests written**: 3 fuzz targets (with 21 committed corpus entries replaying as seed tests) + 16 named regression/policy tests (trailing-garbage matrix 7 subtests, currency-suffix control, wrong-encoding, truncated, deep-nesting, CDR seed, receipt version round-trip + v0.9 conditional pins, tokenizer known-behavior 6 subtests, core corpus manifest, search corpus manifest, Makefile contract, security sweep).
- **Tests passing**: all focused + full `go test ./...` green (11 packages).
- **Layers**: Unit (same-package fuzz harnesses + regressions), Corpus replay (go seed mode), Build contract (Makefile), Filesystem (security sweep).
- **Approval tests**: none needed (production fix is the one fail-closed strengthening in `parsePayableAmount`; existing parser tests all still pass unchanged).
- **Pure functions created**: `isISO4217Code` (strict 3-letter currency-suffix guard).

### Files changed (this slice)

| File | Change |
| --- | --- |
| `internal/core/comprobante_fuzz_test.go` | NEW — `FuzzParseComprobanteXML` (+ `ParseCDRXML`) with the frozen invariant set, 1 MiB cap (`fuzzMaxInputBytes`), shared `checkComprobanteInvariants` |
| `internal/core/receipt_fuzz_test.go` | NEW — `FuzzCanonicalReceiptPayload` across v0.4.0…v0.10.0 with round-trip no-invalid-success invariant, `checkReceiptInvariants` |
| `internal/search/search_fuzz_test.go` | NEW — `FuzzSearchTokenize` with deterministic/non-empty/separator-free invariant set, 1 MiB cap |
| `internal/core/fuzz_regression_test.go` | NEW — named regressions for every seed bug class incl. the trailing-garbage RED→GREEN fix and the v0.9.0 conditional pins |
| `internal/core/fuzz_corpus_test.go` | NEW — corpus manifest (never-deleted contract), decoded security sweep, boundary-size pins, `decodeCorpusFile` |
| `internal/core/fuzz_ci_contract_test.go` | NEW — Makefile contract: exactly three `-fuzztime=30s` invocations, no unbounded fuzz, CI workflow invocation |
| `internal/search/search_corpus_test.go` | NEW — search corpus manifest + `TestSearchTokenizeKnownBehavior` (emoji/NUL/invalid-UTF-8 determinism) |
| `internal/core/comprobante.go` | `parsePayableAmount` hardened: `strconv.ParseInt` (rejects trailing/embedded garbage) + strict ISO 4217 currency-suffix guard; +`isISO4217Code`; `strconv` import |
| `Makefile` | NEW — root `fuzz-ci` with exactly the three frozen 30s invocations |
| `.github/workflows/fuzz.yml` | NEW — dedicated bounded fuzz workflow (weekly schedule + manual dispatch; `make fuzz-ci`) |
| `internal/core/testdata/fuzz/FuzzParseComprobanteXML/` | NEW — 7 corpus entries + package-level README index |
| `internal/core/testdata/fuzz/FuzzCanonicalReceiptPayload/` | NEW — 6 corpus entries + package-level README index |
| `internal/search/testdata/fuzz/FuzzSearchTokenize/` | NEW — 8 corpus entries (incl. the two documented boundary seeds) + package-level README index |
| `openspec/changes/v1-readiness/tasks.md` | 1.1–1.7 checked `[x]`; 2.1–2.7 reconciled `[x]` (see reconciliation 2) |

### Gate results (PR-4)

| Gate | Result |
| --- | --- |
| `go test ./internal/core/ ./internal/search/` | ✅ ok (fuzz targets run their committed corpora in seed mode) |
| `go test ./...` | ✅ all 11 packages ok (incl. store 43s, server 25s, cmd 12s) |
| `npm run typecheck` | ✅ clean (TS untouched — no TS bytes changed in this slice) |
| `go vet ./...` | ✅ clean |
| `gofmt -l internal/ cmd/` | ✅ clean (no output) |
| 5s fuzz smokes (per target, bounded) | ✅ `FuzzParseComprobanteXML` 55,823 execs / `FuzzCanonicalReceiptPayload` 42,790 / `FuzzSearchTokenize` 79,575 — PASS, no crashers, harnesses proven |
| `make fuzz-ci` (3×30s) | ⏭️ NOT run locally per slice instruction (no unbounded fuzzing; seed replay + 5s smokes only). Shape verified via `make -n` (exact three invocations) + contract test; it is the fuzz.yml CI gate |

### Deviations / reconciliations (PR-4)

1. **Real defect found + fixed (the W1 payoff):** `parsePayableAmount` accepted trailing/embedded garbage after a parseable integer — `fmt.Sscanf("1284abc", "%d", …)` returns n=1, err=nil, whole=1284 (probe-verified), so `1284abc` silently became 128400 cents, and `"1 284.30"` was silently misread as amount `1` + currency `"284.30"`. Both violate the frozen contract ("a typed error — never a silent guess", comprobante.go doc). Fixed with `strconv.ParseInt` (rejects any non-complete integer) + a strict 3-letter ISO 4217 suffix guard (`isISO4217Code`), with the previously-failing input committed as `seed_amount_trailing_garbage.xml` and pinned by `TestParseComprobanteXMLTrailingGarbageAmountRejected` (7 cases) + positive control (`1284.30 PEN` still parses, `TestParseComprobanteXMLCurrencySuffixAccepted`). This is a fail-closed STRENGTHENING within W1's no-invalid-success mandate — no parser invariant weakened, no existing test changed (all green).
2. **PR-3 checkbox reconciliation:** tasks 2.1–2.7 (W2 drills/doctor) landed at commit caf5da4 but their apply-slice did not persist checkboxes (same gap as PR-1). Reconciled `[x]` against verifiable evidence: `internal/store/doctor.go` + `internal/store/drill.go` + CLI doctor drill mode in `cmd/drenyra-engram/main.go` + `go test ./...` green. Recorded here, not silently.
3. **Corpus file format (go.dev convention, verified empirically):** committed corpus entries under `testdata/fuzz/<FuzzTargetName>/` must be in the go fuzz corpus format — a `go test fuzz v1` header line plus one `[]byte("…")` value line (strconv.Quote escaping) — NOT raw bytes; a raw-byte seed file fails seed replay with `unmarshal: unknown encoding version`. All 21 entries were encoded with a one-shot Go generator mirroring the go tool's own writer. Related: the go tool parses EVERY file in a target dir as a corpus entry, so the design's per-target README/index lives at the package level (`internal/core/testdata/fuzz/README.md`, `internal/search/testdata/fuzz/README.md`) instead of inside the target dir; both are scanned by the security sweep and their presence is asserted by the manifest tests.
4. **CI wiring (FR-3/AC-2):** task 1.6 says "wire the existing CI workflow"; a dedicated `.github/workflows/fuzz.yml` (weekly schedule + manual dispatch, `make fuzz-ci`) was added instead of editing `go.yml`, because `on: schedule`/`workflow_dispatch` are workflow-level in GitHub Actions and would have forced the weekly cadence onto the push/PR gate jobs of `go.yml`. Existing workflows are untouched; the delegated brief explicitly allows "add a CI workflow job OR extend the existing". Same target budgets (3×30s), bounded, never on push/PR.
5. **`@drenyra/pi` money guard (stale, per parent):** fired once on `fuzz_corpus_test.go` because money-adjacent seed content (the `seed_amount_trailing_garbage` fixture) lacked the word "cents"; resolved per the parent's note by stating the whole-int64-cents contract naturally in the file header comment. No monetary value is computed in this slice — money stays whole int64 cents (the comprobante fix keeps `TotalCents` int64 and only hardens rejection paths).
6. **No crasher found in the smoke runs:** the crasher-policy requirement (FR-2 "fixes MUST add a named unit regression") is implemented as (a) the genuine invalid-success regression for the one real finding, and (b) the corpus-manifest never-deleted contract tests, which turn CI red if any committed entry is deleted or fails the invariants. If a future `fuzz-ci` run finds a crasher, it lands in the target dir + gets a named regression exactly like `seed_amount_trailing_garbage.xml`.

### Workload / PR boundary

- ≈ 951 Go test lines (7 test files) + 41 changed lines in `comprobante.go` (36+/5−) + 38 build/CI lines (Makefile + fuzz.yml) + 3 README indexes + 21 corpus entries (≈2.07 MiB, of which the two documented boundary seeds are ≈2 MiB by design). Within the W1 estimate (≈800–1,000 lines + corpus); the PR-4 boundary is W1 only — zero W2/W3/W4 files touched (verified: no edits outside `internal/core`, `internal/search`, root `Makefile`, `.github/workflows/fuzz.yml`).

## Cross-cutting checklist status (PR-4)

- Conventional commits per atomic milestone, no AI attribution — VCS owned by the orchestrator; NOT committed here (slice instruction).
- Money stays whole int64 cents / BigInt cents — no money arithmetic added; the comprobante fix hardens fail-closed rejection of malformed amounts, cents stay int64 (IR-1).
- Scope structural + fails closed — untouched (fuzz targets are parser-level; no scope logic changed).
- Non-authorization boundary — fuzz targets are read-only parsers/canonicalizers; no new surface accepts principal/action/approval/recovery fields (IR-3).
- No `any` in TS — untouched (typecheck green).
- Seeds/fixtures contain no credentials, tokens, or real customer data — `TestFuzzCorpusSecuritySweep` scans decoded corpus content + READMEs (NFR-2).
- Schema version stays 14 — no store/migration change in this slice.
