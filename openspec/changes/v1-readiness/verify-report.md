# Verify Report — v1-readiness (RE-RUN after remediation slice)

> Phase: verify · Artifact: verify-report · Status: **PASS (with caveats — archive-ready)**
> Inputs: spec + tasks + apply-progress (OpenSpec store). Strict TDD active (`openspec/config.yaml` `phases.apply.strict_tdd: true`, `phases.verify.strict_tdd: true`).
> Store: OpenSpec file-based (openspec/changes/v1-readiness/verify-report.md). No engram writes.
> Verified against: commit `486ad08` (HEAD, clean tree before this report write) — the remediation slice (PR-3-R) closing the previous verify FAIL. This report SUPERSEDES the 2026-08-10 FAIL report (verified against `acba08b`), which blocked archive on AC-4/AC-5 not being delivered.

## Structured status / actionContext

- `changeName: v1-readiness`, `artifactStore: openspec`, all artifacts present and readable in `openspec/changes/v1-readiness/` (proposal, spec, design, tasks, apply-progress, verify-report).
- Native dispatcher quirk (previously recorded): the engine does not index `spec.md`; `blockedReasons` empty. Not treated as a real blocker (consistent with prior phases; OpenSpec file store is authoritative here).
- `actionContext`: repo-local; implementation ownership fully inside the repo (HEAD `486ad08`, working tree clean at verification start). No edit-root concern for this verify pass.
- Review workload guard: chain delivered as 5 chained PRs per the forecast split, plus the verify-mandated remediation slice PR-3-R (W2 boundary re-opened exactly per the previous report's remediation recommendation #1). No new delivery decision needed; no `size:exception`.

## Gates (config.yaml verify_order + mandated extras) — ALL GREEN

| # | Command | Result |
|---|---------|--------|
| 1 | `npm run typecheck` | ✅ exit 0 (tsc --noEmit clean) |
| 2 | `go vet ./...` | ✅ exit 0, no findings |
| 3 | `gofmt -l .` | ✅ no output |
| 4 | `go test ./...` | ✅ 10 packages ok (core, server, store, search, search/bench, cmd, sync, auth, authz, receipts) |
| 5 | `npm test` | ✅ 385 tests / 26 files passed |
| 6 | `go test ./internal/core -run TestGoldenVectorsGo` | ✅ PASS |
| 7 | `go test ./internal/search/bench -v` | ✅ PASS (recall/mrr metrics + adversarial no-crash) |
| 8 | `make fuzz-ci` (3 × 30s) | ✅ exit 0 — FuzzParseComprobanteXML 71,157 execs / FuzzCanonicalReceiptPayload 743,737 / FuzzSearchTokenize 302,414, each ran the full 30s, no crashers, no new corpus entries persisted |

## Acceptance criteria — per-AC verdict (all eleven, re-verified against HEAD 486ad08)

| AC | Verdict | Evidence (test names + results, re-run this pass) |
|----|---------|---------------------------------------------------|
| AC-1 | ✅ PASS | Three fuzz targets in the frozen packages: `FuzzParseComprobanteXML` (`internal/core/comprobante_fuzz_test.go`), `FuzzCanonicalReceiptPayload` (`internal/core/receipt_fuzz_test.go`), `FuzzSearchTokenize` (`internal/search/search_fuzz_test.go`). Committed corpora 7 + 6 + 8 = 21 entries replay green inside `go test ./...` and live fuzzing (see gate 8). 1 MiB cap enforced + documented (`fuzzMaxInputBytes = 1 << 20`, `searchFuzzMaxInputBytes`; oversized inputs ignored before parsing). |
| AC-2 | ✅ PASS | `make fuzz-ci` (root Makefile) runs exactly the three frozen `-fuzztime=30s` invocations — executed full 3×30s exit 0, pinned by `TestMakefileFuzzCIContract` (PASS). `.github/workflows/fuzz.yml` is `workflow_dispatch` + weekly cron only (never on push/PR, never unbounded). Crasher policy: `seed_amount_trailing_garbage.xml` committed + pinned by `TestParseComprobanteXMLTrailingGarbageAmountRejected` (PASS) with positive control `TestParseComprobanteXMLCurrencySuffixAccepted` (PASS); deletion forbidden by `TestFuzzCoreCorpusManifestNeverDeleted` (PASS) and `TestFuzzSearchCorpusNeverDeleted` (PASS); security sweep `TestFuzzCorpusSecuritySweep` (PASS). |
| AC-3 | ✅ PASS | `store.DoctorReport` four fields + mode contract: `TestDoctorRoutineRunsQuickCheckThenForeignKeyCheck` (PASS, order trace-pinned), `TestDoctorRoutineNeverRunsIntegrityCheckOnCorruptStore` (PASS — routine never runs `integrity_check` even on a damaged store), `TestDoctorFullRequiresMarkedDrillCopy` (PASS), CLI `TestCLIDoctorDrillCopyModeContract` (PASS — full report with `integrityCheck` + FK on a marked copy). |
| AC-4 | ✅ PASS | Corruption drill delivered + tested (5 tests / 6 subtests, all PASS this pass): `TestRunCorruptionDrillFullPath` — marked-copy journey: deterministic damage → full doctor `integrityCheck failed` with trace `integrity_check,foreign_key_check` (detection-required + pairing); corrupted hash ≠ snapshot hash (damage real); `Save` ×2 returns typed `ErrStoreWriteFrozen` (retry-proof) + `BeginReceiptTx` frozen; evidence bytes byte-identical to `CorruptedSHA256` after doctor + refused writes (byte preservation, no in-place repair); live DB file hash unchanged + reopens + 3 scope rows (live untouched). `TestCorruptionDrillRequiresMarkedCopy` — live DB refused with `ErrDrillCopyRequired`, no evidence artifact created. `TestCorruptionDrillEvidencePathContract` — pre-existing/self evidence path refused `ErrInvalidDrillPath`. `TestCorruptionDrillEvidenceCannotOpenAsLiveStore` — marked evidence copy refused by normal `Open` with `ErrDrillCopyOnly` (both marker directions). `TestCorruptionDrillNotDetectedFailsClosed` — healthy copy AND structurally-invisible damage both → `ErrCorruptionNotDetected`, bytes preserved (fail closed, no false detection claim). Latch: `writeFrozen` set only on full-doctor integrity failure (store.go:6757), checked in `beginWriteTx` (store.go:5943), the single choke point of all 6 write entry points; no unfreeze/repair path exists (grep: only `Store(true)`, no `Store(false)`). |
| AC-5 | ✅ PASS | Restore drill delivered + tested (success + 6 negatives + scope isolation, all PASS this pass): `TestRunRestoreDrillSuccess` — all four ordered checks ok, output byte-identical to snapshot SHA-256, `.drenyra-verified.json` emitted, restored DB usable (3 scope rows), source snapshot untouched. `TestRunRestoreDrillNegativeMatrix` (6/6 PASS) — corrupted candidate → `RESTORE_VERIFICATION_FAILED` (proves check 1 ran before check 4); wrong backup identity (invisible header-byte damage) → `BACKUP_IDENTITY_MISMATCH` (proves checks 1–3 passed and identity ran last); tampered manifest identity → `BACKUP_IDENTITY_MISMATCH`; same source/output → refused before any file op; pre-existing output → refused, bytes unchanged; interrupted candidate → quarantined byte-for-byte, never clobbered. Every negative: typed rejection, output never published, candidate quarantined, snapshot untouched. `TestRunRestoreDrillScopeIsolation` (2/2 PASS) — wrong-scope snapshot rejected, error names only the expected scope, never foreign rows (IR-2); positive control restores under its own scope. |
| AC-6 | ✅ PASS | `docs/security/key-compromise-response.md` exists with the eight NIST-aligned steps + gap analysis; `TestKeyCompromisePlaybookStructuralReadback` (PASS) and `TestKeyCompromisePlaybookContractGuard` (PASS, three-way docs == contract == verify.go). |
| AC-7 | ✅ PASS | `TestVerifySigningKeyValidityFZ3CutoffMatrix` (PASS, 7/7 FZ-3 rows incl. equal-timestamp rejection, pre-cutoff acceptance, `created_at > issued_at`, unparseable timestamps fail-closed, empty `revoked_at` contract), `TestVerifySigningKeyValidity` (PASS, 12 cases), service path `TestVerifyServiceSigningKeyCutoffMatrix` (PASS, 4/4 — only the signing-key-validity layer changes, chain read-only, revoked key refuses to sign). |
| AC-8 | ✅ PASS | `TestIsMaterialDecisionEligibilityMatrix` (PASS), `TestClassifyReconstructibilityFirstFailurePrecedence` (PASS, incl. direct `not_approved` classifier case), ratio/percentage integer math (PASS), store `TestLatestMaterialDecisionHeadsExactScopeIsolation` / `LatestRevisionOnly` / `ValidEmptyScopeReturnsZero` / `FailsClosedOnPartialScope` (all PASS), server `TestReconstructibilityDeterminism` (PASS), `TestReconstructibilityZeroDenominatorFrozenShape` (PASS — `denominator 0`, `ratio {0,0}`, `percentage null`, `zeroDenominator true`), `TestReconstructibilityRealStoreIntegrationReadOnly` (PASS — store hash unchanged), cross-tenant HTTP/MCP isolation (green at prior pass, unchanged in this slice). |
| AC-9 | ✅ PASS | `testdata/golden/reconstructibility-eligibility.json` + `reconstructibility-classifier.json`; `TestGoldenVectorsGo` PASS this pass (incl. reconstructibility contract case); TS dispatcher case `reconstructibility` in `core/__tests__/golden.test.ts`; focused TS suites `reconstructibility.test.ts` + `golden.test.ts` = 51/51 PASS this pass. |
| AC-10 | ✅ PASS | No `any` in changed TS (typecheck clean; grep hits are doc-comment prose only, e.g. "No `any` (IR-5)"). No float path (integer division; no money in drill/metric arithmetic). No new authorization surface — remediation slice touched ONLY `internal/store/` (doctor.go extraction, drill.go additions) plus tests: zero new CLI/MCP/HTTP adapters; drills are read-only observations + a write-REFUSAL (freeze); no unfreeze/repair/authorize path. |
| AC-11 | ✅ PASS | All eight gates green (table above), re-run this pass at HEAD 486ad08. |

## Task checkbox verification (tasks.md)

- **Implementation rows: 38/38 checked `[x]` with `sdd-owner: implementation`; 0 unchecked implementation rows.** Verified by marker scan: `checked implementation = 38`, `unchecked implementation = 0`.
- Rows 2.5/2.6/2.7 are now HONESTLY `[x]`: the deliverables exist (`RunCorruptionDrill`, `RunRestoreDrill`, scope-conformance/backup-identity verification) and their mapped tests are green (AC-4/AC-5 evidence above). The previous FAIL's "falsely checked" finding is resolved with substance, not cosmetic uncheck/recheck.
- Cross-cutting checklist (6 rows) and DoD implementation rows (2 rows) are `[x]` — substance verified this pass (money int64, scope fail-closed, non-authz boundary, docs-as-code, no `any`, schema 14, full gates).
- Unchecked `- [ ]` lines remaining (exact, all `sdd-owner: parent` — deferred lifecycle actions, NOT implementation work):

```
- [ ] Review Workload Guard decision recorded (delivery_strategy, chain_strategy) before apply; change delivered as chained PRs per the forecast split. <!-- sdd-owner: parent -->
- [ ] Bounded post-apply review of the chained diff (native review as applicable per repository policy), then `sdd-verify` against the spec, then `sdd-archive`. <!-- sdd-owner: parent -->
- [ ] Start or reuse bounded review for each PR boundary after its normalization + candidate freeze; one correction budget per candidate; no reviewer launched by apply. <!-- sdd-owner: parent -->
- [ ] After the final PR merges, run the verify phase (`sdd-verify`) against AC-1…AC-11 and this tasks list; remediate only through the bounded correction path. <!-- sdd-owner: parent -->
- [ ] Archive the change only when verify reports all criteria green and the chain is fully merged. <!-- sdd-owner: parent -->
```

These are the parent's lifecycle gates (review + verify + archive). Line 135's substance ("run the verify phase against AC-1…AC-11") is satisfied by this re-run; line 136 gates archive on this PASS — both remain unchecked until archive, per convention. They do not block this verify PASS.

## Strict TDD compliance

| Check | Result | Details |
|-------|--------|---------|
| TDD Evidence reported | ✅ | TDD Cycle Evidence table present for the remediation slice (apply-progress §REMEDIATION SLICE) + the earlier PR-2/PR-4/PR-5 tables + retroactive PR-1 row |
| RED confirmed (tests exist) | ✅ | `internal/store/drill_test.go` exists (792 lines) referencing `RunCorruptionDrill`, `detectDrillCorruption`, `RunRestoreDrill`, `RestoreChecks`, `ScopeConformanceCheckResult`, `BackupIdentityCheckResult` etc. — all resolved; compile-RED narrative recorded (10 undefined symbols) |
| GREEN confirmed (tests pass) | ✅ | All 9 drill/schema test functions + 10 subtests pass on fresh `-count=1` run (3.5s); all previously-existing suites green (10 packages) |
| Triangulation | ✅ | Corruption: full journey + mark enforcement + path contract + marker-both-directions + 2 no-detection negatives; restore: success + 6 negatives + scope isolation with positive control; order proven behaviorally by distinct sentinels; strconv: 8-case matrix (1 valid + 7 garbage) |
| Safety net | ✅ | New file genuinely new; store suite baseline green (cached baseline + full re-run) |
| Assertion quality | ✅ | No tautologies, no ghost loops (every loop iterates a literal non-empty case array), no type-only-alone assertions, no smoke-only tests, no implementation-detail CSS/call-count coupling, zero mocks (real store + real VACUUM INTO + real corrupted bytes). All assertions verify hashes, typed errors (`errors.Is`), row counts, statuses, and manifest fields |

## Test layer distribution (remediation slice)

| Layer | Tests | Files |
|-------|-------|-------|
| Integration (real store + real SQLite + real corrupted/restored bytes) | 8 top-level / 10 subtests | `internal/store/drill_test.go` |
| Unit (pure parse) | 1 (8 cases) | `internal/store/drill_test.go` (`TestSchemaVersionParseFailsClosed`) |

## Review workload / PR boundary findings

- Chained PRs recommended: Yes → delivered as 5 chained PRs exactly per the forecast split (PR-1 W4 core, PR-2 W4 adapters, PR-3 W2 drills, PR-4 W1 fuzz, PR-5 W3 playbook) + the post-verify remediation slice PR-3-R (W2 boundary re-opened per verify remediation recommendation #1; ≈ 470 production + 880 test lines, store-only).
- No scope creep: PR-3-R touched only `internal/store/doctor.go` (scan extraction), `internal/store/drill.go` (new drivers), `internal/store/drill_test.go` (new), plus the SDD artifacts. Zero TS, zero fuzz corpus, zero playbook bytes — W1/W3/W4 boundaries untouched.

## Non-goals / frozen contracts

- `contracts/` untouched: `git diff d02ee75~1..HEAD -- contracts/` = **0 lines** (empty). Frozen surface intact (NFR-5).
- No product UI ✅; no SUNAT/ERP integration ✅; no identity-provider/MFA/membership ✅; no external security review ✅.
- Non-authorization boundary ✅ (drills read-only + write-refusal only; no unfreeze/repair/authorize path; no new adapter kind).
- No live-DB corruption, no blind copy of a live WAL DB ✅ (VACUUM INTO on mode=ro; marked-copy enforcement tested both directions).
- Schema version stays 14 ✅ (snapshot manifests record schema 14; no migration change).
- No breaking contract change ✅.

## Discrepancies (none blocking)

1. **Cosmetic — subtest count prose**: commit message + apply-progress claim "14 subtests"; actual = 10 (2 + 6 + 2). All tests exist and pass; the count in prose is off by 4. No substance impact.
2. **Cosmetic — fuzz exec counts** differ from the apply-progress figures (213k/695k/305k vs 71k/743k/302k this run) — fuzz execution counts are non-deterministic across runs; the gate contract (3 × 30s, exit 0, no crashers) held both times.
3. The previous report's 5 CRITICAL/WARNING findings (AC-4 absent, AC-5 absent, latch zero coverage, strconvAtoi silent-parse, unchecked markers) are all **resolved** — each has a mapped green test at HEAD 486ad08 (see AC-4/AC-5 rows and the strconv row below).

## Exact blockers (archive gate)

**None.** All prior blockers are closed: AC-4 delivered + tested, AC-5 delivered + tested, task 2.7 scope-isolation delivered + tested, latch coverage exercised, `strconvAtoi` strict (`TestSchemaVersionParseFailsClosed` PASS — `"14abc"`/`" 14"`/`"14 "`/`"0xE"`/`"v14"`/`""`/`"14.5"` all error, `"14"`→14), and all implementation task markers are now honestly checked with evidence. Verify phase: **PASS** — archive gate is ready for the parent.

## Verification commands actually run (this pass)

```
npm run typecheck                                  → exit 0
go vet ./...                                       → exit 0
gofmt -l .                                         → no output
go test ./...                                      → 10 packages ok
npm test                                           → 385 tests / 26 files ok
go test ./internal/core -run TestGoldenVectorsGo -count=1   → PASS
go test ./internal/search/bench -v -count=1        → PASS
make fuzz-ci                                       → exit 0 (3×30s, no crashers)
go test ./internal/store -count=1 -run 'TestRunCorruptionDrill|TestCorruptionDrill|TestRunRestoreDrill|TestSchemaVersionParseFailsClosed' -v
                                                   → 9/9 PASS, 10/10 subtests PASS (3.5s)
go test ./internal/store -count=1 -run 'TestDoctorRoutineRunsQuickCheckThenForeignKeyCheck|TestDoctorFullRequiresMarkedDrillCopy|TestDoctorRoutineNeverRunsIntegrityCheckOnCorruptStore' -v → PASS
go test ./internal/core -count=1 -run 'TestVerifySigningKeyValidityFZ3CutoffMatrix|TestVerifySigningKeyValidity$' -v → PASS
go test ./internal/server -count=1 -run 'TestVerifyServiceSigningKeyCutoffMatrix' -v → PASS
go test ./internal/server -count=1 -run 'TestKeyCompromisePlaybook' -v → PASS (2/2)
go test ./internal/core -count=1 -run 'TestIsMaterialDecisionEligibilityMatrix|TestClassifyReconstructibilityFirstFailurePrecedence' -v → PASS
go test ./internal/server -count=1 -run 'TestReconstructibilityDeterminism|TestReconstructibilityZeroDenominatorFrozenShape|TestReconstructibilityRealStoreIntegrationReadOnly' -v → PASS
go test ./internal/store -count=1 -run 'TestLatestMaterialDecisionHeads' -v → PASS (4/4)
go test ./internal/core -count=1 -run 'TestParseComprobanteXMLTrailingGarbageAmountRejected|TestParseComprobanteXMLCurrencySuffixAccepted|TestFuzzCoreCorpusManifestNeverDeleted|TestFuzzCorpusSecuritySweep' -v → PASS
go test ./internal/search -count=1 -run 'TestFuzzSearchCorpusNeverDeleted' -v → PASS
go test ./internal/core -count=1 -run 'TestMakefileFuzzCIContract' -v → PASS
go test ./cmd/drenyra-engram -count=1 -run 'TestCLIDoctorDrillCopyModeContract' -v → PASS
npm test -- --run core/__tests__/reconstructibility.test.ts core/__tests__/golden.test.ts → 51 tests ok
git diff d02ee75~1..HEAD -- contracts/            → empty (0 lines)
```
