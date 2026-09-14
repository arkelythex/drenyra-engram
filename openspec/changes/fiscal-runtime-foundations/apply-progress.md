# Apply Progress — Fiscal Runtime Foundations

## Cumulative status

- **Change:** `fiscal-runtime-foundations`
- **Artifact store:** OpenSpec
- **Delivery strategy:** chained delivery, `stacked-to-main`
- **Implemented boundary:** Delivery Slices 1–3.
- **Slice 1 state:** complete and verified; all five implementation-owned Slice 1 rows remain visibly checked in `tasks.md`. Committed: `feat/fiscal-runtime-foundations-slice-1` (PR #35 → `main`, draft).
- **Slice 2 state:** complete and focused-verification green; all five implementation-owned Slice 2 rows are visibly checked in `tasks.md`. Committed: `feat/fiscal-runtime-foundations-slice-2` (PR #36 → slice-1, draft).
- **Slice 3 state:** complete and full-suite green (see "Delivery Slice 3" section below); all five implementation-owned Slice 3 rows are visibly checked in `tasks.md`. Committed on `feat/fiscal-runtime-foundations-slice-3` (branched from slice-2; not yet pushed/PR'd as of this entry).
- **Commit evidence:** Slices 1–2 committed and pushed as open draft PRs (#35, #36); Slice 3 committed locally on its own stacked branch.
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

## Delivery Slice 3 — Envelope linkage and store enforcement (complete)

Implemented the immutable act-evidence and envelope-linkage mechanism (design.md "Immutable act evidence and envelope linkage") and wired it into every first-slice protected write path: `Save`, `SupersedeExplicit`, the new atomic-batch `AddEvidenceLinksBound`/`AddRuleLinksBound`, and the new `StoreObjectWithFiscalIntent`. Each persists one immutable `fiscal_binding_links` row atomically with its act; a legacy (nil-intent) caller against an already v1-bound subject fails closed (`SCOPE_BINDING_REQUIRED`); a structurally valid but axis-mismatched binding fails closed (`SCOPE_MISMATCH`) without disclosing the foreign value; `PERIOD_CLOSED` is checked before any fiscal append; and a fully-duplicate batch replay mints no phantom act.

### Bug found and fixed during this slice

`addLinksBound`'s fiscal-link append was unconditional on `intent != nil`, ignoring whether the batch actually inserted any new ref. Replaying an identical `AddEvidenceLinksBound`/`AddRuleLinksBound` call (all refs already persisted, `INSERT OR IGNORE` silently no-ops) still minted a new `fiscal_binding_links` row with an incremented sequence and a changed envelope hash — a phantom act for a command that persisted nothing new, violating the "one immutable link per successful persisted act" invariant and the tasks.md "idempotent replay" acceptance scenario. Fixed by gating the fiscal-link append on `anyInserted` (mirrors the existing `evidence_links` receipt-emission guard). Proven by `TestAddEvidenceLinksBoundIdempotentReplayMintsNoPhantomAct`, which also triangulates that a batch containing at least one genuinely new ref still mints exactly one further act.

### Open design question for a human reviewer (not blocking, does not weaken any guarantee)

`fiscal_binding_links` carries `UNIQUE(subject_type, subject_id, binding_hash, act_evidence_hash)` (Slice 2 schema, design.md: "uniqueness for sequence and idempotent act identity"). Because `act_evidence_hash` covers only `(bindingHash, reviewedEnvelopeHash, reviewChecksState)` — never ref content, a timestamp, or the resulting hash — two **genuinely distinct** acts (e.g. two separate evidence-link batches on the same subject) sharing an identical binding and tri-state (`omitted,omitted`, the case for every non-approval act in this slice) collide on that constraint. This slice's tests avoid the collision by using a different `actor` for a second act on the same subject (a realistic case — a different session/agent — and it changes the binding hash), but did not resolve whether the schema intends true acts to collapse in that edge case or whether `appendFiscalBindingLinkTx` should catch the constraint violation and treat it as an idempotent replay. Flagging for Slice 4/5 design confirmation rather than guessing; does not affect any Slice 3 acceptance scenario actually exercised.

### Slice 3 files changed

- `internal/core/lifecycle.go` (+5: `TransitionMeta.FiscalIntent`)
- `internal/core/types.go` (+23: `AccountingMemory.FiscalLinks`, `SaveInput.FiscalIntent`, `ComputeEnvelopeHash` fiscal contribution)
- `internal/core/fiscal_act_evidence.go` (new, 83 lines: `ComputeActEvidenceHash`, `FiscalWriteIntent`, `FiscalBindingLinkContribution`, envelope-contribution framing)
- `internal/core/fiscal_act_evidence_test.go` (new, 120 lines)
- `internal/store/store.go` (+234/-1: bound `Save`/`SupersedeExplicit`, new `AddEvidenceLinksBound`/`AddRuleLinksBound`/`addLinksBound`, `FiscalLinks` population in `withLinks`/`readMemoryWithLinks`/`refreshEnvelopeCache`)
- `internal/store/fiscal_scope_store.go` (+147: `verifyFiscalIntentAxes`, `requireFiscalIntentForBoundSubject`, `storeFiscalScopeBindingTx`, `appendFiscalBindingLinkTx`, `fiscalBindingLinkContributionsTx`/`...BestEffort`)
- `internal/store/object_store.go` (+36: `StoreObjectWithFiscalIntent`)
- `internal/store/fiscal_binding_enforcement_test.go` (new, 458 lines: 12 tests)
- `cmd/drenyra-engram/main_test.go`, `cmd/drenyra-engram/purge_test.go`, `internal/server/api_test.go` (schema-version literal fix, see below — test-only, zero production change)

### Pre-existing regression found and fixed (not this slice's production code — a Slice 2 test-coverage gap)

Slice 2's own TRIANGULATE only ran `go test ./internal/store ./internal/core`, not `go test ./...`. This slice's TRIANGULATE step (which explicitly requires `go test ./...`) surfaced 4 failures in `cmd/drenyra-engram` and `internal/server` from hardcoded `SchemaVersion != 17` doctor-report assertions that never got updated when Slice 2 bumped the schema to v18. Fixed by updating the 4 literals to `18` (and correcting one stale `"want 14"` message that never matched its own `!= 17` check, predating this change). Test-only; zero production behavior changed; already-open PR #36 (Slice 2) was left untouched — this fix ships as part of Slice 3 since that is where the full-suite requirement first applies.

### Slice 3 TDD Cycle Evidence

| Task | Test file | Layer | Safety Net / RED | GREEN | TRIANGULATE | REFACTOR |
| --- | --- | --- | --- | --- | --- | --- |
| Slice 3 RED | `internal/core/fiscal_act_evidence_test.go`, `internal/store/fiscal_binding_enforcement_test.go` | Pure unit + store/SQLite integration | `go build ./...` and `go test ./internal/store ./internal/core` green before any Slice 3 edit. | N/A | 12 store-level scenarios: complete-binding persistence, operation mismatch, axis mismatch (no disclosure), direct bypass (nil intent), direct bypass (mismatched intent), positive supersede append (sequence 2), atomic batch (all-or-nothing), idempotent replay (no phantom act), bypass on evidence links, object-store fiscal linkage, `PERIOD_CLOSED` ordering. | N/A |
| Slice 3 GREEN | `internal/core/fiscal_act_evidence.go`, `internal/store/store.go`, `internal/store/fiscal_scope_store.go`, `internal/store/object_store.go` | Store/core | RED above | All 12 new tests pass; `go build ./...` clean. | Full `go test ./internal/store ./internal/core` green (87–95s). | N/A |
| Slice 3 TRIANGULATE | full repo | Package/invariant | N/A | `go test ./...` initially FAILED (4 pre-existing schema-version literal failures, see above). | After the 4-literal fix, `go test ./...` green across all 9 packages; `go vet ./...` and `gofmt -l .` clean; receipt/golden diff empty. | N/A |
| Slice 3 REFACTOR | `internal/store/store.go` | Store | Idempotent-replay bug found by triangulating the new test (see "Bug found" above). | Fixed by gating the fiscal-link append on `anyInserted`; re-ran the full Slice 3 suite green. | Added `TestAddEvidenceLinksBoundIdempotentReplayMintsNoPhantomAct` with a second triangulation case (distinct actor still mints a new act). | Transaction guard ordering (fiscal axis/bypass checks before `assertPeriodWritable` and any exec) is consistent across `Save`/`SupersedeExplicit`/`addLinksBound`/`storeObject`. |
| Slice 3 evidence | `tasks.md`, this artifact | Structural | N/A | N/A | Changed-path boundary audited (see files list); receipt denylist empty. | No source-normalizing changes after final evidence. |

### Slice 3 commands and results

1. `go build ./...` — PASS (before and after every edit).
2. `go test ./internal/store -run '<12 new test names>' -v` — 11/12 PASS, 1 FAIL (`TestAddEvidenceLinksBoundIdempotentReplayMintsNoPhantomAct`, revealing the real `anyInserted` bug — a UNIQUE-constraint collision in its own triangulation case, resolved by using a distinct actor per the design question above).
3. Same command after the fix — 12/12 PASS.
4. `go test ./internal/store ./internal/core` — PASS (95s / 2.7s).
5. `go vet ./...` — PASS, no output.
6. `gofmt -l .` — PASS, no output.
7. `git diff --stat -- internal/core/receipt.go core/receipt.ts contracts/receipts.md testdata/golden/` — empty (receipt/golden denylist respected).
8. `go test ./...` (first run) — FAIL: `cmd/drenyra-engram` (2 tests), `internal/server` (2 tests), all hardcoded `SchemaVersion 17` doctor-report assertions.
9. Fixed the 4 literals (17→18, plus one stale message).
10. `go test ./...` (second run) — PASS across all 9 packages (`cmd/drenyra-engram`, `internal/auth`, `internal/authz`, `internal/core`, `internal/receipts`, `internal/search`, `internal/search/bench`, `internal/server`, `internal/store`, `internal/sync`).

### Slice 3 candidate and workload boundary

- **Slice 3 changed-path count:** 10 paths (3 new, 7 modified) inside the declared boundary, plus 3 test-only files outside it for the schema-version regression fix (documented above).
- **PR boundary:** chained `stacked-to-main`, branched from `feat/fiscal-runtime-foundations-slice-2` (PR #36). Not yet pushed/PR'd as of this entry — parent orchestrator handles push/PR after reviewing this result.
- **Runtime harness:** N/A — Slice 3 exposes no public command/service/adapter path; focused SQLite store tests (`newTestStore(t)`, real transactions) are the applicable executable evidence.
- **Rollback:** disable the new bound entry points (`Save`'s/`SupersedeExplicit`'s fiscal-intent branches, `AddEvidenceLinksBound`, `AddRuleLinksBound`, `StoreObjectWithFiscalIntent`) — legacy callers (`AddEvidenceLink`, `AddRuleLink`, `StoreObject`) are untouched and remain the only public entry points until Slice 6 wires adapters. Additive `fiscal_binding_links`/`fiscal_scope_bindings` rows created during any rollback window remain (append-only, never deleted).

## Deviations from design

None beyond the open design question above (flagged for reviewer confirmation, not a deviation). The cumulative candidate remains additive, performs no SUNAT lookup, treats scope metadata as non-authorizing, wires no public adapter, and changes no receipt implementation or golden.

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

- [x] Select the chain strategy or explicitly approve a documented size exception before launching apply; retain the ask-on-risk decision in the delivery record. <!-- sdd-owner: parent --> — resolved 2026-09-14: `auto-chain` delivery, `stacked-to-main` chain strategy (recorded in `tasks.md`'s Review Workload Forecast table).
- [ ] Start or reuse one bounded review per approved delivery slice after implementation and before delivery gating. <!-- sdd-owner: parent -->
- [ ] Confirm all seven slices have native attempt acquisition, focused evidence, clean changed-path boundaries, and no unresolved receipt hard stop before verification/archive. <!-- sdd-owner: parent -->
