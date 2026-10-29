# Apply Progress — Fiscal Runtime Foundations

## Cumulative status

- **Change:** `fiscal-runtime-foundations`
- **Artifact store:** OpenSpec
- **Delivery strategy:** chained delivery, `stacked-to-main`
- **Implemented boundary:** Delivery Slices 1–2 only; Slice 3 was not started.
- **Slice 1 state:** complete and verified; all five implementation-owned Slice 1 rows remain visibly checked in `tasks.md`.
- **Slice 2 state:** complete and focused-verification green; all five implementation-owned Slice 2 rows are visibly checked in `tasks.md`.
- **Commit evidence:** uncommitted worktree candidate; no commit was created because apply was not authorized to commit.
- **Rollback:** remove the additive fiscal contracts, pure Go/TypeScript validators, v18 migration/persistence/runtime-mode code, focused tests, and fiscal vectors; retain legacy records unchanged and do not touch receipt implementation/goldens.

## Recovery summary

The maintainer-authorized retry retained valid work from the interrupted candidate. On entry, the reported duplicate `IsValidRUC` compiler blocker was already absent in the live worktree: `internal/core/types.go` contained one legacy `IsValidRUC` declaration, while the additive canonical checksum validator was named `IsValidFiscalRUC`. Focused compilation confirmed the duplicate-declaration failure was resolved.

The remaining RED/GREEN work closed an encoding gap: Go `encoding/json` had accepted malformed UTF-8 by replacement, and TypeScript had accepted unpaired UTF-16 surrogates that cannot represent valid UTF-8 scalar values. Both runtimes now reject those values while accepting and byte-framing well-formed multibyte values.

## Completed implementation tasks and persisted checkboxes

- `[x]` Slice 1 RED — retained prior table-driven parity coverage and added observed failing malformed-encoding controls plus well-formed multibyte triangulation.
- `[x]` Slice 1 GREEN — retained canonical RUC/scope implementations and added strict Go raw UTF-8 and TypeScript Unicode-scalar validation.
- `[x]` Slice 1 TRIANGULATE — focused Go core/goldens, TypeScript fiscal/goldens, and typecheck passed.
- `[x]` Slice 1 REFACTOR — replaced the positional Go binding composite with named fields; focused tests remained green.
- `[x]` Slice 1 evidence/rollback — commands, candidate revision, changed-path boundary, receipt denylist, and rollback are recorded here.
- `[x]` Slice 2 RED — retained the partial attempt's temporary-directory migration/store safety net.
- `[x]` Slice 2 GREEN — retained the additive v18 persistence, inventory/classification, runtime-mode, and downgrade implementation after focused compilation passed.
- `[x]` Slice 2 TRIANGULATE — focused migration/store, store/core packages, vet, formatting, and unchanged core golden evidence passed.
- `[x]` Slice 2 REFACTOR — migration and mode policy remain isolated from command/public adapter wiring; diagnostics remain opaque.
- `[x]` Slice 2 evidence/rollback — fresh candidate revision, changed-path/line boundary, receipt denylist, and rollback are recorded below.

## Files changed in the cumulative Slice 1 candidate

- `contracts/approval.md`
- `contracts/fiscal-scope-v1.md`
- `contracts/scope.md`
- `core/__tests__/fiscal-scope.test.ts`
- `core/__tests__/golden.test.ts`
- `core/fiscal-scope.ts`
- `core/index.ts`
- `core/ruc.ts`
- `core/types.ts`
- `internal/core/approval.go`
- `internal/core/comprobante.go`
- `internal/core/comprobante_fuzz_test.go`
- `internal/core/fiscal_scope.go`
- `internal/core/fiscal_scope_test.go`
- `internal/core/golden_test.go`
- `internal/core/ruc.go`
- `testdata/golden/fiscal-ruc-v1.json`
- `testdata/golden/fiscal-scope-v1.json`

## TDD Cycle Evidence

| Task | Test file | Layer | Safety Net / RED | GREEN | TRIANGULATE | REFACTOR |
| --- | --- | --- | --- | --- | --- | --- |
| Slice 1 RED | `internal/core/fiscal_scope_test.go`, `core/__tests__/fiscal-scope.test.ts` | Pure unit/parity | Prior candidate tests retained. New invalid UTF-8/unpaired-surrogate tests failed as expected: Go returned nil; Vitest reported 1 failed/8 passed. | N/A | Added valid `agente:🧾` byte-length controls. | N/A |
| Slice 1 GREEN | `internal/core/fiscal_scope_test.go`, `core/__tests__/fiscal-scope.test.ts` | Pure unit/parity | RED above | Go rejects invalid raw UTF-8; TypeScript rejects invalid scalar strings. Focused tests passed. | Valid multibyte data remains accepted and frames `actor=11`. | Named Go struct fields remove positional decoding fragility. |
| Slice 1 TRIANGULATE | focused Go/TypeScript suites | Package/parity | Initial focused Go compilation passed after the prior duplicate fix. | Focused suites green. | Shared RUC/scope vectors and existing golden harness green. | Typecheck green after replacing unsupported `String.isWellFormed` with a target-compatible scalar check. |
| Slice 1 REFACTOR | same focused suites | Pure unit/parity | Existing behavior protected by retained tests. | Green after refactor. | Both malformed and valid multibyte paths covered. | `gofmt` applied; focused Go and TS tests rerun. |
| Slice 1 evidence | `tasks.md`, this artifact | Structural | N/A | N/A | Changed-path and receipt denylist checks passed. | No source-normalizing changes after final evidence. |

### Test summary

- **New recovery controls:** 4 scenarios (Go invalid UTF-8 + valid multibyte framing; TypeScript unpaired surrogate + valid multibyte framing).
- **Focused Go core:** `go test ./internal/core -count=1` — PASS.
- **Go fiscal/golden vectors:** `go test ./internal/core -run 'TestFiscalRUCGolden|TestFiscalScopeV1Golden|TestFiscalScopeV1EveryElementIsBound|TestGoldenVectorsGo' -count=1` — PASS.
- **TypeScript fiscal/golden:** 2 files, 41 tests passed.
- **Typecheck:** PASS.
- **Approval tests:** none; this slice adds pure values and does not wire approval transactions.

## Commands and results

1. `go test ./internal/core -run 'RUC|Fiscal|Golden'` — PASS on recovery entry; compiler blocker absent.
2. `go test ./internal/core` — PASS safety net before recovery edits.
3. `npm test -- --run core/__tests__/fiscal-scope.test.ts core/__tests__/golden.test.ts` — 40 retained tests PASS before recovery edits.
4. `npm run typecheck` — PASS before recovery edits.
5. `go test ./internal/core -run TestDecodeFiscalScopeV1JSONRejectsInvalidUTF8 -count=1` — RED, expected failure (`error = <nil>`).
6. `npm test -- --run core/__tests__/fiscal-scope.test.ts` — RED, expected 1 failed / 8 passed for unpaired surrogate acceptance.
7. Focused GREEN and triangulation reruns — PASS (Go malformed/valid UTF-8 controls; TypeScript 10 fiscal tests).
8. `go test ./internal/core -count=1` — final PASS.
9. `go test ./internal/core -run 'TestFiscalRUCGolden|TestFiscalScopeV1Golden|TestFiscalScopeV1EveryElementIsBound|TestGoldenVectorsGo' -count=1` — final PASS.
10. `npm test -- --run core/__tests__/fiscal-scope.test.ts core/__tests__/golden.test.ts` — final PASS, 41/41.
11. `npm run typecheck` — final PASS.
12. Slice-boundary script — PASS; all implementation paths are inside Delivery Slice 1.
13. Receipt denylist script — PASS; no changes to `internal/core/receipt.go`, `core/receipt.ts`, `contracts/receipts.md`, receipt tests, or receipt goldens.

## Fresh candidate evidence for parent settlement

- **Failed evidence being remediated:** `sha256:3755f6bde8d6e0e257a7bc4a34fd7ed26c301319037ffe99b51098f02759c3bc`
- **Fresh implementation candidate revision:** `sha256:ccd758270846ecfa57c44bcd8d2d307182d0ef8315a927a424552f65cbb09c38`
- **Revision method:** SHA-256 over sorted implementation paths, each path's mode, and each file's SHA-256; OpenSpec planning/progress artifacts excluded.
- **Diagnosis:** duplicate declaration is absent; remaining malformed-encoding acceptance was corrected in both runtimes.
- **Harness disposition:** reused.
- **Cleanup evidence:** focused tests exited; no temporary repository artifacts created; `gofmt` applied only to touched Go files.
- **Process evidence:** final Go core, Go golden, TypeScript fiscal/golden, typecheck, slice-boundary, and receipt-denylist commands all passed.

## Delivery Slice 2 retry evidence

The retry retained the existing partial v18 candidate and first proved that it compiled. No production repair was required after inspection: the migration, persistence, inventory/classification, immutable triggers, runtime modes, and downgrade guard were already present and the focused tests passed. The retry completed Slice 2 by running the required focused/package/vet/golden evidence, auditing the changed-path and receipt boundaries, and persisting the five Slice 2 checkbox updates.

### Slice 2 files changed

- `internal/auth/errors.go`
- `internal/store/store.go`
- `internal/store/fiscal_scope_store.go`
- `internal/store/migration_v18_test.go`
- `internal/store/crypto_test.go`
- `internal/store/migration_direct_upgrade_test.go`
- `internal/store/migration_v10_test.go`
- `internal/store/migration_v11_test.go`
- `internal/store/migration_v12_test.go`
- `internal/store/migration_v14_test.go`
- `internal/store/migration_v17_test.go`
- `internal/store/migration_v3_test.go`
- `internal/store/migration_v4_test.go`
- `internal/store/migration_v5_test.go`
- `internal/store/migration_v6_test.go`
- `internal/store/migration_v7_v8_test.go`
- `internal/store/migration_v9_test.go`
- `internal/store/review_store_test.go`

### Slice 2 TDD Cycle Evidence

| Task | Test file | Layer | RED | GREEN | TRIANGULATE | REFACTOR |
| --- | --- | --- | --- | --- | --- | --- |
| Slice 2 RED | `internal/store/migration_v18_test.go` | Migration/store | Retained the partial attempt's temporary-directory tests for v17→v18, fresh v18, rollback, inventory/classification, immutability, modes, and downgrade refusal. | N/A | Focused rerun passed all three named tests. | N/A |
| Slice 2 GREEN | `internal/store/fiscal_scope_store.go`, `internal/store/store.go`, `internal/auth/errors.go` | Persistence/policy | Existing RED safety net retained. | Partial candidate compiled and focused tests passed without further production edits. | Store plus core package suites passed. | Migration/mode policy remains isolated from public adapter wiring. |
| Slice 2 TRIANGULATE | focused migration/store, package suites, vet, core golden | Package/invariant | N/A | All commands passed. | Legacy RUC/envelope bytes remained unchanged in migration test; receipt denylist was empty. | `gofmt -l` returned no files. |
| Slice 2 evidence | `tasks.md`, this artifact | Structural | N/A | Five Slice 2 rows checked. | Changed-path boundary contains only migration/store/auth-error fixtures and code. | Rollback remains additive and preserves legacy data. |

### Slice 2 commands and results

1. `go test ./internal/store -run 'TestMigrationV18|TestFiscal' -count=1` — PASS; existing candidate compiled.
2. `go test ./internal/store ./internal/core -count=1` — PASS.
3. `gofmt -l internal/store/fiscal_scope_store.go internal/store/migration_v18_test.go internal/store/store.go internal/auth/errors.go` — PASS; no output.
4. `go vet ./...` — PASS; no output.
5. `go test ./internal/core -run TestGoldenVectorsGo -count=1` — PASS.
6. `go test ./internal/store -run 'TestMigrationV18IsAdditiveAndFailsClosed|TestFiscalPersistenceInventoryAndImmutability|TestFiscalRuntimeModesAndDowngradeRefusal' -count=1 -v` — PASS; all three named tests passed.
7. Receipt denylist audit — PASS; no changes to `internal/core/receipt.go`, `core/receipt.ts`, `contracts/receipts.md`, receipt canonicalization tests, or receipt goldens.

### Slice 2 candidate and workload boundary

- **Failed evidence being remediated:** `sha256:461ef614d0e149ebc722f0b2172b583c71ddc8e05c182b5d67fdccc90d14914e`
- **Fresh implementation candidate revision:** `sha256:ac9f38761506da67012b440f0bc54a9fb7ba5143c9b718fd91c80e0927ec1aa9`
- **Revision method:** SHA-256 over sorted implementation paths, each path's mode, and each file's SHA-256; OpenSpec planning/progress artifacts excluded.
- **Slice 2 changed-path count:** 18 paths.
- **Slice 2 changed-line count:** 430 (400 additions, 30 deletions), including migration fixture updates.
- **PR boundary:** chained `stacked-to-main`, Delivery Slice 2 only. No Slice 3 or public adapter work was started.
- **Harness disposition:** reused.
- **Runtime harness:** N/A — Slice 2 is intentionally limited to migration/store internals and exposes no public command, service, or adapter path; focused `t.TempDir()` store tests are the applicable executable evidence.
- **Cleanup/process evidence:** all focused commands exited; no temporary repository artifacts were created; tests use `t.TempDir()`.
- **Rollback:** remove/disable additive v18 schema and runtime-mode code before v1 writes; existing legacy rows remain unchanged. After v1 data exists, only a v18-aware `read_only`/dual-read rollback is safe.

## Deviations from design

None. The cumulative candidate remains additive, performs no SUNAT lookup, treats scope metadata as non-authorizing, wires no public adapter, and changes no receipt implementation or golden.

## Structured status consumed

- `schemaName`: `gentle-ai.sdd-status`
- `changeName`: `fiscal-runtime-foundations`
- `artifactStore`: `openspec` (authoritative)
- `applyState`: `ready`
- `dependencies.apply`: `ready`
- `nextRecommended`: `apply`
- `blockedReasons`: none
- `actionContext.mode`: `repo-local`
- `actionContext.workspaceRoot`: `/home/dreamcoder08/Documents/PROYECTOS/drenyra-engram`
- `actionContext.allowedEditRoots`: `/home/dreamcoder08/Documents/PROYECTOS/drenyra-engram`
- `actionContext.warnings`: none
- Workload gate was resolved by the parent as chained `stacked-to-main`; this retry completed only Delivery Slice 2, and the cumulative candidate contains Slices 1–2.

## Remaining implementation tasks (exact unchecked rows)

- [ ] RED: add store-level tests for complete-binding enforcement, operation/classification mismatch, trusted-axis mismatch, legacy-to-v1 refusal, invalid input before reservation/object access, atomic evidence/rule batches, H1/H2 linkage, sequence gaps, tampering, idempotent replay, and `PERIOD_CLOSED`. <!-- sdd-owner: implementation -->
- [ ] GREEN: implement immutable act-evidence canonicalization, v1-only envelope contribution, exact binding reload/hash checks, store-authoritative operation map, bound save/supersede/evidence/object/rule/close commands, atomic batch links, and close write guards. <!-- sdd-owner: implementation -->
- [ ] TRIANGULATE: run focused store/core tests, direct bypass and cross-tenant/RUC/period/source-snapshot controls, `go test ./...`, and unchanged receipt goldens; verify zero partial rows/objects/links/receipts/audit/idempotency state on rejected commands. <!-- sdd-owner: implementation -->
- [ ] REFACTOR: consolidate transaction guard ordering, preserve legacy receipt bytes, and make immutable-link/audit references explicit without introducing receipt payload changes. <!-- sdd-owner: implementation -->
- [ ] Record the slice boundary and rollback as disabling v1 protected write commands while retaining additive immutable evidence and legacy reads. <!-- sdd-owner: implementation -->
- [ ] RED: add service/read/verification tests for pre-auth RUC validation, exact scope predicates, cross-tenant/organization/RUC/period/snapshot non-disclosure, legacy/unbound versus v1 versus unverifiable reports, audit-anchor resolution, reload mismatch, offline operation, and the exact `Accounting correctness: NOT ASSERTED` conclusion. <!-- sdd-owner: implementation -->
- [ ] GREEN: implement service defense-in-depth validation, trusted-context comparisons, bound read/reconstruction/export methods, verification classification, audit linkage resolution, and the TypeScript semantic store/verification repetition. <!-- sdd-owner: implementation -->
- [ ] TRIANGULATE: run focused Go server/store tests, TypeScript tests/typecheck, `go test ./...`, `npm test`, and `npm run typecheck`; confirm no network/write behavior in offline verification and unchanged receipt goldens. <!-- sdd-owner: implementation -->
- [ ] REFACTOR: align error mapping and parity fixtures, isolate read-only verification from authorization, and remove any inferred/default scope path. <!-- sdd-owner: implementation -->
- [ ] Record evidence and rollback as disabling bound service/read paths while preserving legacy labelled reads and stored immutable evidence. <!-- sdd-owner: implementation -->
- [ ] RED: add table-driven tests for omitted/false/true acknowledgement states, material/critical policy, stale H1, wrong actor/binding, inactive membership, role/assurance, self-approval, idempotency conflict/replay, concurrency, signing failure, close approval, and post-close denial with zero partial state. <!-- sdd-owner: implementation -->
- [ ] GREEN: carry tri-state `ReviewChecks` through the authenticated approval transaction, hash v1 idempotency intent, enforce policy/SoD and exact envelope freshness, compute/store H2 and immutable act evidence, and retain existing approval/close receipt payloads unchanged. <!-- sdd-owner: implementation -->
- [ ] TRIANGULATE: run focused approval/close tests, store bypass tests, unchanged receipt golden tests, `go test ./...`, and audit/verification assertions for trusted actor, RUC, period, reason, receipt continuity, and `Accounting correctness: NOT ASSERTED`. <!-- sdd-owner: implementation -->
- [ ] REFACTOR: separate principal-derived authority from binding metadata, centralize acknowledgement mapping, and make transaction ordering visibly fail closed before reservation and mutation. <!-- sdd-owner: implementation -->
- [ ] Record rollback as closing the new professional approval capability (never defaulting checks to true or bypassing policy) while retaining historical receipts and immutable evidence. <!-- sdd-owner: implementation -->
- [ ] RED: add boundary-matrix tests proving invalid RUC/binding rejection before DB/token/store work, strict duplicate/unknown/trailing JSON rejection, omitted versus false flags/fields, redaction, HTTP authority-field rejection, MCP `AUTHENTICATION_REQUIRED` before decode/store, and `DRENYRA_ENV=local_dev` isolation. <!-- sdd-owner: implementation -->
- [ ] GREEN: wire `--fiscal-scope <path>`, presence-aware CLI acknowledgement flags, strict HTTP DTO/pre-auth validation, MCP binding input/fail-closed approval, canonical RUC scope parsing, and fictional local seed validation through the shared services. <!-- sdd-owner: implementation -->
- [ ] TRIANGULATE: run focused CLI/HTTP/MCP tests, `go test ./...`, `go vet ./...`, `gofmt -l .`, `npm test`, and typecheck where TypeScript adapters are touched; verify no token/credential leakage, foreign disclosure, or partial mutation. <!-- sdd-owner: implementation -->
- [ ] REFACTOR: keep transport decoding strict and thin, preserve machine-readable stable codes, document local-only limitations, and confirm changed paths stay within the adapter/seed boundary. <!-- sdd-owner: implementation -->
- [ ] Record rollback as disabling v1 public adapter exposure and failing closed for material approvals, while leaving authenticated policy and immutable store evidence intact. <!-- sdd-owner: implementation -->
- [ ] RED: add rollout/evidence checks for inventory, contract/vector freeze, shadow gate, enforce gate, legacy compatibility selection, downgrade refusal, receipt changed-path denylist, complete verification evidence, and consumer handoff prerequisites. <!-- sdd-owner: implementation -->
- [ ] GREEN: document versioned scope/approval contracts, legacy/v1 classifications, fictional local-dev operation, rollback/read-only modes, non-authorization boundary, required commands, and the exact onboarding handoff without claiming onboarding implementation. <!-- sdd-owner: implementation -->
- [ ] TRIANGULATE: run the complete verification order `npm run typecheck`, `go vet ./...`, `gofmt -l .`, `go test ./...`, `npm test`; run compliance gates where applicable (`bun run compliance:sire-gate`, `bun run compliance:sire-repro`), inspect changed paths, and confirm all receipt goldens remain byte-identical. <!-- sdd-owner: implementation -->
- [ ] REFACTOR: remove stale workaround language, ensure docs match implemented version/error semantics, preserve explicit `Accounting correctness: NOT ASSERTED`, and produce a final trace from each specification requirement to evidence. <!-- sdd-owner: implementation -->
- [ ] Record final work-unit commit/rollback boundaries and mark the dependent onboarding consumer blocked pending a passing verify report; do not modify its implementation in this change. <!-- sdd-owner: implementation -->

## Deferred parent lifecycle actions (preserved, exact unchecked rows)

- [ ] Select the chain strategy or explicitly approve a documented size exception before launching apply; retain the ask-on-risk decision in the delivery record. <!-- sdd-owner: parent -->
- [ ] Start or reuse one bounded review per approved delivery slice after implementation and before delivery gating. <!-- sdd-owner: parent -->
- [ ] Confirm all seven slices have native attempt acquisition, focused evidence, clean changed-path boundaries, and no unresolved receipt hard stop before verification/archive. <!-- sdd-owner: parent -->
