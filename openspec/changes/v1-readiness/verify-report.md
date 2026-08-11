# Verify Report — v1-readiness

> Phase: verify · Artifact: verify-report · Status: **FAIL (CRITICAL completeness gaps — archive blocked)**
> Inputs: spec + tasks + apply-progress (OpenSpec store). Strict TDD active (`openspec/config.yaml` `phases.apply.strict_tdd: true`, `phases.verify.strict_tdd: true`).
> Store: OpenSpec file-based (openspec/changes/v1-readiness/verify-report.md). No engram writes.
> Verified against: commit `acba08b` (HEAD, clean tree) — full PR-1..PR-5 chain merged (d02ee75, db93011, caf5da4, c8eaf6a, acba08b).

## Structured status / actionContext

- Native dispatcher quirk (previously recorded in apply-progress): engine does not index `spec.md`; `blockedReasons` empty. All artifacts present and readable in `openspec/changes/v1-readiness/`. Not treated as a real blocker (consistent with apply-phase handling).
- `actionContext`: repo-local; implementation ownership fully inside the repo. No edit-root concern for this verify pass.
- Review workload guard: chain strategy `auto-chain` recorded in apply-progress (PR-4/PR-5 sections); delivered as 5 chained PRs exactly per the forecast split.

## Gates (config.yaml verify_order + mandated extras) — ALL GREEN

| # | Command | Result |
|---|---------|--------|
| 1 | `npm run typecheck` | ✅ exit 0 (tsc --noEmit clean) |
| 2 | `go vet ./...` | ✅ exit 0, no findings |
| 3 | `gofmt -l .` | ✅ no output |
| 4 | `go test ./...` | ✅ 10 packages ok (core 2.9s, server 20.9s, store, search, cmd, sync, auth, authz, receipts, search/bench) |
| 5 | `npm test` | ✅ 385 tests / 26 files passed |
| 6 | `go test ./internal/core -run TestGoldenVectorsGo` | ✅ PASS |
| 7 | `go test ./internal/search/bench -v` | ✅ PASS (recall/mrr metrics + adversarial no-crash) |
| 8 | `make fuzz-ci` (3 × 30s) | ✅ exit 0 — FuzzParseComprobanteXML, FuzzCanonicalReceiptPayload, FuzzSearchTokenize each ran full 30s, no crashers |

## Acceptance criteria — per-AC verdict

| AC | Verdict | Evidence (test names + results) |
|----|---------|---------------------------------|
| AC-1 | ✅ PASS | Three fuzz targets exist in the frozen packages: `FuzzParseComprobanteXML` (`internal/core/comprobante_fuzz_test.go`, drives `ParseComprobanteXML`+`ParseCDRXML`), `FuzzCanonicalReceiptPayload` (`internal/core/receipt_fuzz_test.go`, drives `CanonicalReceiptPayload`/`CompleteReceiptBytes`/`ReceiptHash` across `receipt-payload/v0.4.0…v0.10.0`), `FuzzSearchTokenize` (`internal/search/search_fuzz_test.go`). Each enforces no-panic, determinism, round-trip (receipt), no-invalid-success (typed `INVALID_*`, `serie-numero==documentId`, RUC shape+checksum, non-empty tokens, no separators), and documents/enforces the 1 MiB cap (`fuzzMaxInputBytes` / `searchFuzzMaxInputBytes`). Committed corpora (7+6+8 = 21 entries) replay GREEN under `go test` (seed mode: `FuzzParseComprobanteXML/seed_amount_trailing_garbage.xml` … all PASS) and inside `go test ./...`. |
| AC-2 | ✅ PASS | `make fuzz-ci` (root Makefile) runs exactly the three `-fuzztime=30s` invocations — verified by reading the Makefile, `make fuzz-ci` executed full 3×30s with exit 0, and pinned by `TestMakefileFuzzCIContract`. `.github/workflows/fuzz.yml` is weekly + manual dispatch only (never on push/PR, never unbounded). Crasher policy: the real PR-4 finding `seed_amount_trailing_garbage.xml` is committed, pinned by `TestParseComprobanteXMLTrailingGarbageAmountRejected` (7 cases) + positive control `TestParseComprobanteXMLCurrencySuffixAccepted`; deletion is forbidden by `TestFuzzCoreCorpusManifestNeverDeleted` / `TestFuzzSearchCorpusNeverDeleted`; security sweep `TestFuzzCorpusSecuritySweep` green (no credentials/tokens/customer data, boundary-size pins). |
| AC-3 | ✅ PASS | `store.DoctorReport` extended with `quickCheck`, `integrityCheck`, `foreignKeyCheck`, `cellSizeCheck` (`internal/store/doctor.go`). Routine = `quick_check` → `foreign_key_check`, `integrityCheck` `not_run`, order pinned by query-trace (`TestDoctorRoutineRunsQuickCheckThenForeignKeyCheck`); behavioral proof that routine never runs `integrity_check` even on a damaged store (`TestDoctorRoutineNeverRunsIntegrityCheckOnCorruptStore`); full path only via marked drill copy, always paired with FK (`TestDoctorFullRequiresMarkedDrillCopy`; CLI `TestCLIDoctorDrillCopyModeContract` asserts full report with `integrityCheck ok` + FK on a marked copy); `cell_size_check` effective value reported (`on`). CLI doctor JSON contract extended for the four fields (`cmd/drenyra-engram/main_test.go`). |
| AC-4 | ❌ **FAIL** | **No corruption-drill test exists anywhere in the repo.** Task 2.5 is marked `[x]` but no test does: disposable copy → deliberate damage → check-surface detection → write refused with typed error → corrupted bytes preserved → no in-place repair → live DB untouched. `corruptSigningKeysRootTypeByte` (doctor_test.go) exists but is only used to prove routine doctor skips `integrity_check` on a LIVE store, never on a drill copy. The `STORE_WRITE_FROZEN` latch (store.go:513/5944/6757) is implemented but **zero tests exercise it** (no test ever sets the latch and asserts a refused write). The `RunCorruptionDrill` driver described in `drill.go`'s package header does not exist in the codebase. |
| AC-5 | ❌ **FAIL** | **No restore-drill implementation or test exists.** `CreateDrillSnapshot` (VACUUM INTO + sidecar manifest) and `OpenDrillCopy` exist, but there is no restore pipeline: no candidate copy, no ordered verify-after-restore (integrity → foreign_key_check → scope conformance → backup identity), no atomic publish, no typed rejection, no negative matrix (corrupted candidate / wrong identity / missing scope rows / same source+output / pre-existing output / interrupted candidate). No scope-conformance or backup-identity verification functions exist. Task 2.6 marked `[x]` — claim unsupported. Task 2.7 (drill scope-isolation conformance) also absent. |
| AC-6 | ✅ PASS | `docs/security/key-compromise-response.md` exists: purpose/non-claims, roles, exact eight NIST-aligned steps, FZ-3 cutoff policy with before/equal/after examples + pre-compromise retention (NIST SP 800-57), command/evidence checklist, gap-analysis table concluding Implementation == Contract (FZ-3) with quoted evidence. Pinned by `TestKeyCompromisePlaybookStructuralReadback` and `TestKeyCompromisePlaybookContractGuard` (three-way whitespace-normalized guard: docs == `contracts/verification.md` == `internal/core/verify.go`). Both green. |
| AC-7 | ✅ PASS | Pure seam: `TestVerifySigningKeyValidityFZ3CutoffMatrix` — 7/7 rows green (pre-cutoff 1ns valid / equal-timestamp rejected / post-cutoff rejected / `created_at > issued_at` rejected / unparseable `revoked_at` fail-closed / unparseable `issued_at` fail-closed / empty `revoked_at` keeps active-key contract). Existing `TestVerifySigningKeyValidity` (12 cases) green. Service path: `TestVerifyServiceSigningKeyCutoffMatrix` — 4/4 green (only signing-key-validity layer changes, chain read-only, real-store revoked-key signing refusal). |
| AC-8 | ✅ PASS | `TestIsMaterialDecisionEligibilityMatrix` (every status / fiscal effect incl. `none`+`approval` / every materiality incl. nil / numeric-cents irrelevance), `TestClassifyReconstructibilityFirstFailurePrecedence` (six reason categories + direct `not_approved` classifier case + closed-vocabulary rejection), ratio/percentage integer math incl. 2/3→66, null-on-zero (`TestReconstructibilityRatio*`); store: `TestLatestMaterialDecisionHeadsExactScopeIsolation`, `LatestRevisionOnly`, `ValidEmptyScopeReturnsZero`, `FailsClosedOnPartialScope` (5 subtests); server: `TestReconstructibilityDeterminism` (byte-identical double run), `TestReconstructibilityRealStoreIntegrationReadOnly` (store-file-hash unchanged before/after), `TestReconstructibilityZeroDenominatorFrozenShape` (`denominator 0`, `numerator 0`, `ratio {0,0}`, `percentage null`, `zeroDenominator true`), cross-tenant probes on HTTP (`TestHTTPReconstructibilityScopeIsolation`) and MCP (`TestMCPReconstructibilityScopeIsolation`). All green. |
| AC-9 | ✅ PASS | `testdata/golden/reconstructibility-eligibility.json` + `reconstructibility-classifier.json` present; `TestGoldenVectorsGo` green (incl. `reconstructibility` contract case); TS dispatcher case `"reconstructibility"` in `core/__tests__/golden.test.ts`; TS reconstructibility + golden suites green (51 tests). `core/verify.ts` gained the one mirrored constant `LAYER_RULE_VERSION_VIGENCIA` (FZ-2 layer name) — consistent with the parity mechanism. |
| AC-10 | ✅ PASS | No `any` in changed TS (typecheck green; grep hits are doc-comment text only). No float path: ratio/percentage are integer division; no money in drill/metric arithmetic (IR-1). No new authorization surface: CLI `reconstructibility` (JSON report, exit 0/2), MCP `accounting_reconstructibility` (catalog wording test asserts "read-only observation… never authorizes/approves/posts/files/reopens"), HTTP `GET /accounting/reconstructibility` (token-guarded, read-only); drill surfaces read-only (`OpenDrillCopy` mode=ro+query_only, snapshot via mode=ro VACUUM INTO, monotonic write-freeze latch, no unfreeze). `TestCLIHasNoAuthorizationCommands` green. |
| AC-11 | ✅ PASS | All eight gates green (table above). |

## Task checkbox verification (tasks.md)

Implementation work units W1–W4 (1.1–1.7, 2.1–2.7, 3.1–3.4, 4.1–4.11) are all `[x]`. **However**, rows marked `sdd-owner: implementation` remain unchecked in `tasks.md` (lines 117–128) and — critically — three `[x]` rows (2.5, 2.6, 2.7) have **no corresponding implementation or tests**:

Unchecked `- [ ]` lines in tasks.md (exact):

```
- [ ] Conventional commit per atomic milestone (`feat:`, `test:`, `docs:`, `build:`, `bench:`); no AI attribution. <!-- sdd-owner: implementation -->
- [ ] Money stays whole int64 cents / BigInt cents; no float path anywhere (percentage is integer division; money never appears in drill/metric arithmetic — IR-1). <!-- sdd-owner: implementation -->
- [ ] Scope stays structural and fails closed on mismatch; cross-tenant invisibility tested for metric and drills (IR-2). <!-- sdd-owner: implementation -->
- [ ] Non-authorization boundary intact: no new surface approves, posts, files, reopens writes, or authorizes recovery (IR-3); revoked keys never sign. <!-- sdd-owner: implementation -->
- [ ] Docs-as-code: `docs/security/key-compromise-response.md` and any behavior docs land in the same PR as their code; stale docs are a defect. <!-- sdd-owner: implementation -->
- [ ] No `any` in TypeScript (IR-5); seeds/fixtures contain no credentials, tokens, or customer data (NFR-2). <!-- sdd-owner: implementation -->
- [ ] Schema version stays 14; opening an existing v14 fixture requires no migration (migration-proof assertion in the apply slice). <!-- sdd-owner: implementation -->
- [ ] All tasks checked; every acceptance criterion AC-1…AC-11 in `spec.md` verified green by its mapped test: AC-1/2 (W1), AC-3/4/5 (W2), AC-6/7 (W3), AC-8/9/10 (W4), AC-11 (full gates). <!-- sdd-owner: implementation -->
- [ ] Full gates per config verify_order: `npm run typecheck` → `go vet ./...` → `gofmt -l .` → `go test ./...` → `npm test`; plus `go test ./internal/core -run TestGoldenVectorsGo` and bounded `make fuzz-ci` green. <!-- sdd-owner: implementation -->
```

The unchecked cross-cutting/DoD rows are checklist invariants whose substance was largely verified in this pass (money int64, no `any`, schema 14, non-authz boundary, docs-as-code, gates) — but they remain unchecked markers and the DoD row "every acceptance criterion verified green by its mapped test" is factually **false** for AC-4/AC-5 (their mapped tests do not exist). Parent-owned lifecycle rows (129–130, 134–136) are outside this phase's scope.

## Strict TDD compliance

| Check | Result | Details |
|-------|--------|---------|
| TDD Evidence reported | ✅ | TDD Cycle Evidence tables present for PR-2, PR-4, PR-5 slices |
| TDD evidence for ALL slices | ⚠️ | PR-1 (W4 core) and PR-3 (W2 drills) were "reconciled" without TDD Cycle Evidence tables — and PR-3's reconciliation is exactly where the missing AC-4/AC-5 tests should have been caught |
| RED confirmed (test files exist) | ❌ | Tasks 2.5/2.6/2.7 claim RED tests; no corruption-drill or restore-drill test files exist |
| GREEN confirmed (tests pass) | ✅ | Every existing changed-file test passes when run |
| Triangulation | ✅ | W1: 7-case + 6-case + 8-case corpora + named regressions; W3: 7-row + 4-row matrices; W4: full eligibility/classifier matrices; W2: routine/full doctor matrix |
| Safety net | ✅ | New files are genuinely new; existing-suite baselines referenced |
| Assertion quality | ✅ | No tautologies, no ghost loops (all loops iterate non-empty literal arrays), no type-only-alone, no smoke-only (CLI smoke asserts exit codes + JSON), no impl-detail assertions. `.toBe(true)` instances assert predicate results paired with `.toBe(false)` negatives |

## Review workload / PR boundary findings

- Chained PRs recommended: Yes → delivered as 5 PRs matching the forecast split exactly (PR-1 W4 core, PR-2 W4 adapters, PR-3 W2 drills, PR-4 W1 fuzz, PR-5 W3 playbook). No `size:exception` needed; chain strategy recorded (`auto-chain`).
- No scope creep across slices: per-slice file inventories confirm W1/W2/W3/W4 boundaries held (only PR-4's `parsePayableAmount` fail-closed hardening and PR-2's one-token TS union fix crossed into production files — both documented, in-scope corrections).
- ⚠️ **W2 slice delivered less than tasks 2.5/2.6/2.7 promise**: the PR-3 boundary is the one slice whose claimed deliverables (corruption drill test, restore drill, scope-isolation drill test) are absent.

## Non-goals held

- No product UI ✅ (no frontend changes; TS changes are `core/` library + tests only).
- No SUNAT/ERP integration ✅ (`comprobante.go` pre-existed; PR-4 only hardened `parsePayableAmount` fail-closed — no new integration surface).
- No identity-provider/MFA/membership implementation ✅.
- No external/third-party security review ✅.
- Non-authorization boundary ✅ (spot-checked: metric CLI/MCP/HTTP and drill surfaces are read-only observations; `OpenDrillCopy` is mode=ro+query_only; no unfreeze/repair/authorize path).
- `contracts/` untouched ✅ — `git diff d02ee75~1..acba08b -- contracts/` is EMPTY (0 lines). Frozen surface intact (NFR-5).
- No breaking contract change ✅. Schema version stays 14 ✅ (`const schemaVersion = 14`).

## Discrepancies vs apply-progress claims

1. **CRITICAL — AC-4/AC-5 claims false.** apply-progress (PR-3 section) claims "go test ./internal/store ./cmd/drenyra-engram green (AC-3/4/5 tests pass)" and "test asserts: detection reported; the next write returns typed STORE_WRITE_FROZEN… byte preservation… no repair…" — no such tests exist. `drill.go`'s package header documents the corruption drill and restore drill as implemented, but the code does not contain `RunCorruptionDrill`, a restore pipeline, scope-conformance, or backup-identity verification. Doc-vs-code mismatch.
2. **CRITICAL — tasks 2.5, 2.6, 2.7 marked `[x]` without deliverables.** No test file exercises the write-freeze latch (`STORE_WRITE_FROZEN`), byte preservation, no-repair, live-DB-untouched, restore verification order, or restore negative matrix.
3. **WARNING — write-freeze latch zero coverage.** store.go implements the monotonic latch and every write entry point checks it, but nothing in the suite ever triggers it.
4. **WARNING — PR-1/PR-3 slices lack TDD Cycle Evidence tables** (reconciled, not evidenced).
5. **WARNING — `strconvAtoi` (drill.go:345) uses `fmt.Sscanf("%d")`** — the same silent-prefix-parse pattern PR-4 removed from `comprobante.go`; `"14abc"` would silently parse as 14. Low reachability (schema_meta is engine-written) but violates the "never a silent guess" fail-closed principle in corruption-detection code. Recommend `strconv.Atoi`.
6. **Minor — package-count drift** in apply-progress prose ("11 packages" PR-4 vs "10 packages" PR-5); actual `go test ./...` reports 10 packages. Cosmetic.

## Exact blockers (archive gate)

1. **AC-4 not delivered**: no corruption-drill test (copy → corrupt → detect → `STORE_WRITE_FROZEN` refusal → byte-preserved evidence → no repair → live DB untouched); `RunCorruptionDrill` driver absent.
2. **AC-5 not delivered**: no restore-drill implementation or test (ordered verify-after-restore integrity → FK → scope conformance → backup identity; typed rejection matrix; output never released on any failure).
3. **Task 2.7 absent**: drill scope-isolation conformance test.
4. **Unchecked implementation-owned task markers** remain in tasks.md (lines 117–128; exact lines above) — DoD "all ACs verified green by mapped test" is false for AC-4/AC-5.

## Recommended remediation (orchestrator-owned, NOT executed here)

1. Re-open W2 scope: implement `RunCorruptionDrill` (evidence-copy + deterministic page damage) and the restore pipeline (`RunRestoreDrill` with the four ordered checks + atomic publish) in `internal/store/drill.go`, or explicitly descope AC-4/AC-5 with a spec amendment (they are binding FZ-4/FR-5/FR-6 requirements — descoping requires a proposal + spec change).
2. Write the RED tests first (strict TDD): corruption drill full path incl. write-freeze latch, byte preservation, no-repair, live-DB-untouched; restore success + negative matrix; drill scope-isolation.
3. Fix `strconvAtoi` to `strconv.Atoi` (fail-closed).
4. Reconcile the unchecked cross-cutting/DoD markers in tasks.md with evidence, or keep them unchecked until AC-4/AC-5 land.
5. Re-run the full gate set + focused store/cmd suites; then archive.

## Verification commands actually run

```
npm run typecheck                                  → exit 0
go vet ./...                                       → exit 0
gofmt -l .                                         → no output
go test ./...                                      → 10 packages ok
npm test                                           → 385 tests / 26 files ok
go test ./internal/core -run TestGoldenVectorsGo   → PASS
go test ./internal/search/bench -v                 → PASS
make fuzz-ci                                       → exit 0 (3×30s, no crashers)
go test ./internal/store -run 'TestDoctor|TestLatestMaterialDecisionHeads' -v → PASS
go test ./internal/server -run 'TestReconstructibility|TestVerifyServiceSigningKeyCutoffMatrix|TestKeyCompromisePlaybook' -v → PASS
go test ./internal/core -run 'TestVerifySigningKeyValidityFZ3CutoffMatrix|TestIsMaterialDecision|TestClassifyReconstructibility' -v → PASS
go test ./cmd/drenyra-engram -run 'TestCLIReconstructibility|TestCLIDoctorDrillCopyModeContract' -v → PASS
npm test -- --run core/__tests__/reconstructibility.test.ts core/__tests__/golden.test.ts → 51 tests ok
git diff d02ee75~1..acba08b -- contracts/         → empty (0 lines)
```
