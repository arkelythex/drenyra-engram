# Design — v1-readiness

> Phase: design · Artifact: design · Status: draft
> Inputs: proposal + spec (required). Strict TDD: not active in this phase.
> Store: OpenSpec file-based. FZ-1 through FZ-4 are binding and are not reopened here.

## Context

This design closes the v1 engineering-evidence gaps without creating authority or changing accounting state. It adds: (G-10) one exact-company, exact-period reconstructibility read that reuses the existing offline verifier and rule-version traces; (G-6) bounded fuzzing plus copy-only SQLite health/corruption/restore drills; and (G-7) permanent boundary proofs and an operator playbook for the already-frozen signing-key cutoff. Scope remains structural, metric output is deterministic integer-only JSON, drills never corrupt or repair a live database, and new shared pure logic joins the existing Go↔TypeScript golden mechanism. Inspection confirms `internal/core/verify.go::VerifySigningKeyValidity` and `contracts/verification.md` already implement FZ-3 (`created_at <= issued_at`, pre-revocation pass, equality/after reject, malformed timestamps fail closed); delivery therefore adds proofs and documentation, not new cutoff semantics.

## Decisions

- `D-1` — **Expose G-10 through all three existing adapters, CLI first.** The exact surfaces are:
  - CLI: `drenyra-engram reconstructibility <ruc> --period <YYYYMM> [--company-id <id>] [--organization <id>] [--db <path>] [--objects <dir>]`. `--period` is mandatory; `companyId` defaults to the established CLI derivation (`companyId := ruc`) when omitted. JSON goes to stdout. Exit `0` means the report was built, including a zero denominator; exit `2` means invalid scope/period or an unavailable/corrupt read. The metric has no “failed metric” exit `1`: non-reconstructible decisions are report data, not command failure.
  - MCP tool: `accounting_reconstructibility`, with one required strict-decoded `scope` object. The scope must be `kind=company` with non-empty `companyId`, valid `ruc`, and valid non-empty `period`; unknown arguments fail closed.
  - HTTP: `GET /accounting/reconstructibility?organizationId=<id>&companyId=<id>&ruc=<11-digits>&period=<YYYYMM>`, mounted under the existing shared-token read guard. This route requires all four exact-scope fields rather than applying the generic HTTP `companyId := ruc` fallback; this prevents an apparently precise baseline from querying an inferred company identity.
  All adapters delegate to one `internal/server` service and serialize the same result type. No adapter accepts a principal, action, approval, or recovery field.

- `D-2` — **Separate eligibility, classification, and aggregation.** `internal/core/reconstructibility.go` owns pure `IsMaterialDecision(memory, requestedScope, isLatest)` and `ClassifyReconstructibility(memory, verificationReport)` functions plus integer ratio computation. The classifier is callable independently of denominator selection, so `not_approved` remains a real, unit-testable closed-vocabulary outcome rather than dead code hidden by a prefiltered query. The classifier applies FZ-2 in exactly this first-failure order: approval → full receipt chain plus approval provenance (`receipt_failed`) → evidence availability (`missing_evidence`) → object availability (`evidence_missing_object`) → rule availability (`rule_unresolved`) → rule-version/vigencia (`rule_version_failed`). No report-wide `Outcome` shortcut may reorder these checks.

- `D-3` — **Use a two-method aggregation seam and reuse verification rather than duplicating it.** The server aggregator depends on:

  ```go
  type MaterialDecisionReader interface {
      LatestMaterialDecisionHeads(ctx context.Context, scope core.Scope) ([]core.AccountingMemory, error)
  }
  type MemoryVerifier interface {
      VerifyMemory(ctx context.Context, memoryID string) (core.VerificationReport, error)
  }
  ```

  `SQLiteStore.LatestMaterialDecisionHeads` performs a single SQL-scoped read over the exact six-column scope tuple and period, selecting only the maximum revision per `(topic_key, exact scope)` and applying the FZ-1 status/fiscal-effect/materiality predicates in SQL. The service still re-runs pure `IsMaterialDecision` on every returned row as a defensive invariant and rejects an out-of-scope row with `RECONSTRUCTIBILITY_SCOPE_MISMATCH`. The production `MemoryVerifier` is a small server adapter around existing `server.VerifyMemory(ctx, store, id)`; its internal reads remain the existing `VerificationStore` methods (`ReceiptsForSubject`, receipt payload/hash reads, `ReceiptActProvenance`, evidence/rule link reads, `ObjectAvailability`, `SigningKeyForVerify`, rule-chain/version/transition reads, and successor reads). Thus the aggregator itself sees only two methods while verification keeps its established narrow read contract and no transaction. `ErrNoReceipts` becomes `receipt_failed`; persistence/decode/corruption errors abort the whole report as unavailable rather than being mislabeled as a business reason.

- `D-4` — **Freeze deterministic metric bytes.** `internal/server.ReconstructibilityResult` has this declaration/JSON order: `scope`, `period`, `denominator`, `numerator`, `ratio`, `percentage`, `reasons`, `zeroDenominator`. `ratio` is `{numerator, denominator}`. `percentage` is `*int` (`null` for zero denominator), computed as integer `(numerator*100)/denominator`; the implementation guards multiplication overflow even though counts originate from bounded slice length. `reasons` is a concrete struct, not a map, with fields in frozen vocabulary order: `notApproved`, `receiptFailed`, `missingEvidence`, `evidenceMissingObject`, `ruleUnresolved`, `ruleVersionFailed`, each containing decision IDs sorted bytewise ascending. The wire keys remain the spec’s snake-case category strings through explicit JSON tags. Empty lists serialize as `[]`, never `null`. A zero denominator emits exactly `{denominator:0,numerator:0,ratio:{numerator:0,denominator:0},percentage:null,...,zeroDenominator:true}`.

- `D-5` — **Extend doctor with explicit modes and structured results.** `DoctorReport` gains:
  - `quickCheck: {status:"ok"|"failed", detail:string}` — always populated on routine doctor;
  - `integrityCheck: {status:"not_run"|"ok"|"failed", detail:string}` — `not_run` on routine doctor, never omitted;
  - `foreignKeyCheck: {status:"ok"|"failed", violationCount:int, detail:string}` — run on routine and full paths;
  - `cellSizeCheck: {effective:"on"|"off", detail:string}` — reports the effective `PRAGMA cell_size_check` value after attempting `ON` on connection initialization.
  `Doctor(ctx, DoctorOptions{Mode: Routine})` executes `quick_check` then `foreign_key_check`. `DoctorCopy(ctx, DrillCopyInput)` executes `integrity_check` then `foreign_key_check` on a drill copy only. Full integrity is not reachable through the routine API.

- `D-6` — **Make full doctor copy-only by construction.** Routine CLI remains `doctor --db <live>`. Full diagnostics use the mutually exclusive command `doctor --drill-copy <copy.db> --snapshot-manifest <manifest.json>`; `--db` is rejected in this mode. The manifest records a format version, canonical source path, canonical copy path, source snapshot SHA-256, creation timestamp, and expected scope. The full path requires: an existing manifest, canonical copy path equality, source/copy path inequality, a `drillCopy:true` marker, and read-only SQLite open (`mode=ro`, query-only). A database carrying the adjacent `<copy.db>.drenyra-drill.json` marker is rejected by normal `openStore` with `DRILL_COPY_ONLY`, so the drill artifact cannot be accidentally used as a live writable store. Full checks never accept or open the manifest’s source path.

- `D-7` — **Use `VACUUM INTO` for WAL-safe snapshots and restore drills.** This repository uses `modernc.org/sqlite` and has no existing backup wrapper; `VACUUM INTO` is available through the current SQL connection, creates a transactionally consistent standalone database without a blind WAL file copy, and avoids introducing driver-specific backup handles. `CreateDrillSnapshot` validates distinct canonical paths, refuses overwrite, runs `VACUUM INTO ?` with safely bound/escaped output, closes/syncs the output, hashes it, and writes the manifest atomically beside it. The restore drill copies the immutable snapshot bytes to `<requested>.candidate`, fsyncs file and directory, then opens the candidate read-only and verifies in this fixed order: `integrity_check`; `foreign_key_check`; exact expected scope row conformance; expected snapshot identity (manifest SHA-256 and required schema/identity metadata). Only after all four pass is the candidate atomically renamed to the separate requested output and a verified manifest emitted. Any failure returns `RESTORE_VERIFICATION_FAILED`, leaves the source snapshot untouched, leaves the rejected candidate quarantined, and does not publish the output.

- `D-8` — **Corrupt only a marked copy and preserve its bytes.** `RunCorruptionDrill` accepts only a `DrillCopy` produced by `CreateDrillSnapshot`, copies it once more to a dedicated evidence path, fsyncs it, deterministically flips bytes in a selected non-header database page, and hashes the corrupted bytes. It opens that evidence copy through the drill-only store, runs full doctor, and requires detection. Detection sets a process-local, monotonic write-freeze latch on that drill store; every write entry point checks the common latch before beginning a transaction and returns typed `STORE_WRITE_FROZEN`. Retry cannot clear it. Closing/reopening through normal store APIs is blocked by the drill marker. Doctor and attempted writes run read-only/no-transaction and must not change the corrupted file; a before/after SHA-256 proves byte preservation. There is no repair function and no unfreeze method. Only D-7 can produce a separately verified usable output.

- `D-9` — **Deliver G-7 as evidence around the existing seam.** `docs/security/key-compromise-response.md` contains: purpose/non-claims; roles and prerequisites; the exact eight-step NIST-aligned response; cutoff policy with before/equal/after examples; command/evidence checklist; recovery and re-signing constraints; and a “Gap analysis” table comparing `SQLiteStore.SigningKeyForVerify`/`LookupSigningKey`, the one-way revoke trigger/write path, `core.VerifySigningKeyValidity`, and `contracts/verification.md`. The gap conclusion must quote symbol/contract evidence and say implementation matches FZ-3 unless tests demonstrate otherwise. Boundary tests live in `internal/core/verify_test.go` for the pure seam and `internal/server/verify_service_test.go` for a full receipt chain. Revoked-key signing refusal stays at the signing/store seam test. No contract edit is planned.

- `D-10` — **Place fuzz harnesses beside the code under test.** Use same-package Go tests so unexported helpers and validity predicates are testable:
  - `internal/core/comprobante_fuzz_test.go` (`package core`) defines `FuzzParseComprobanteXML` and drives both `ParseComprobanteXML` and `ParseCDRXML`;
  - `internal/core/receipt_fuzz_test.go` (`package core`) defines `FuzzCanonicalReceiptPayload` over payload versions `receipt-payload/v0.4.0` through `v0.10.0`, including `CompleteReceiptBytes` and `ReceiptHash`;
  - `internal/search/search_fuzz_test.go` (`package search`) defines `FuzzSearchTokenize` and directly drives `tokenize`.
  Each target returns immediately above 1 MiB and documents the cap. Corpora live at Go’s package-relative paths: `internal/core/testdata/fuzz/FuzzParseComprobanteXML/`, `internal/core/testdata/fuzz/FuzzCanonicalReceiptPayload/`, and `internal/search/testdata/fuzz/FuzzSearchTokenize/`. A new root `Makefile` (none currently exists) provides `fuzz-ci` with three explicit foreground `go test ... -run '^$' -fuzz='^Target$' -fuzztime=30s` commands. Minimized crashers are committed under the matching target directory with a short adjacent README/index entry naming the bug class; they are never deleted while the class exists, and opaque corpus names are supplemented by a named unit regression when needed.

## Architecture

### Layers touched

| Layer | Change |
| --- | --- |
| `internal/core` (pure domain) | Add pure FZ-1 eligibility, FZ-2 ordered classifier/reason enum, integer ratio helper, and two core fuzz harnesses. Add only G-7 boundary tests; do not alter `VerifySigningKeyValidity` unless the gap test proves a mismatch. |
| `internal/search` | Add same-package tokenizer fuzz target and corpus; production tokenizer semantics remain unchanged unless fuzzing exposes a bug. |
| `internal/store` (SQLite, single-writer, immutable) | Add exact-scope latest-material-head read; doctor check types/options; `cell_size_check` setup/reporting; copy/manifest, corruption latch, `VACUUM INTO`, restore verification, and typed drill errors. No accounting-table or receipt mutation. |
| `internal/server` + `cmd` (adapters, thin) | Add canonical reconstructibility aggregator/result, API method, CLI command, MCP tool, HTTP GET route, strict scope decoding, and adapter tests. Add verification-service cutoff integration tests. Extend doctor CLI with copy-only full mode. |
| TS mirrors (`core/`) | Mirror eligibility, classifier, reason vocabulary, and integer ratio logic with precise types and `bigint`/integer-safe count handling; no `any`. Operational drills are Go-only. |
| `contracts/` (frozen surface) | No change. `contracts/verification.md` is cited by the playbook; FZ-3 is already implemented. Any discovered mismatch that truly requires a contract change blocks this change and requires a separate approved versioned amendment. |
| `testdata/golden` | Add vectors for every eligibility axis, six-reason precedence combinations, direct `not_approved`, and ratio/zero-denominator bytes; run in Go and TS. |
| `docs/security` | Add the key-compromise playbook and implementation/contract gap analysis. |
| Build/CI | Add root `Makefile` with bounded `fuzz-ci`; wire the existing CI workflow to invoke it without changing target budgets. |

### Data model / invariants

No persistent accounting data model changes are required.

```go
type ReconstructibilityResult struct {
    Scope           core.Scope                `json:"scope"`
    Period          string                    `json:"period"`
    Denominator     int                       `json:"denominator"`
    Numerator       int                       `json:"numerator"`
    Ratio           ReconstructibilityRatio   `json:"ratio"`
    Percentage      *int                      `json:"percentage"`
    Reasons         ReconstructibilityReasons `json:"reasons"`
    ZeroDenominator bool                      `json:"zeroDenominator"`
}
```

The denominator is only FZ-1: latest chain revision, exact company scope and byte-equal period, `approved`, one of the six frozen fiscal effects, and material/critical declared level. Numeric `Materiality` never participates. The numerator and one reason per failed member are only FZ-2. Reasons are closed enum values and concrete output fields, preventing arbitrary strings. Candidate and reason IDs are sorted before assembly. JSON uses structs, not maps, to freeze field order.

Doctor additions are observational structs only; `not_run` is explicit for routine `integrityCheck`. Drill manifests are sidecar operational evidence, not SQLite schema. The write-freeze latch applies only to the marked drill-store handle and has no authority to freeze or reopen a production database.

### Flow

#### Reconstructibility

1. Adapter validates a complete exact company scope and non-empty valid period. Institutional, wildcard, partial, or ambiguous scopes return `INVALID_RECONSTRUCTIBILITY_SCOPE`.
2. `server.Reconstructibility` calls `LatestMaterialDecisionHeads` with the exact scope. SQL includes every scope column and period and never scans/returns another scope.
3. The service defensively verifies returned scope equality and pure FZ-1 eligibility.
4. For each candidate ordered by decision ID, the injected existing verifier builds the complete memory report without a transaction.
5. The pure classifier applies FZ-2 first-failure precedence. Passed decisions increment numerator; failed decisions append the ID to exactly one fixed reason list.
6. Lists are sorted, ratio/percentage are computed in integer math, and the concrete result is serialized identically by CLI/MCP/HTTP.
7. No state transition, receipt, event, evidence access mutation, or transaction occurs.

#### Routine and corruption doctor

1. Routine doctor opens the requested database normally, enables/reads `cell_size_check`, runs `quick_check`, then `foreign_key_check`, and reports `integrityCheck:not_run`.
2. A corruption drill first creates a consistent `VACUUM INTO` snapshot and manifest at a path distinct from the source, then creates the dedicated evidence copy.
3. The deterministic page flip touches only the evidence copy. Full doctor opens only that marked copy read-only, runs `integrity_check` then `foreign_key_check`, detects corruption, and latches writes closed.
4. A write attempt returns `STORE_WRITE_FROZEN` before transaction start. Hash-before/hash-after proves doctor and the write refusal preserved corrupted bytes. The live DB hash/state remains unchanged.

#### Restore

1. Create or select the immutable `VACUUM INTO` snapshot and its expected identity manifest.
2. Copy snapshot bytes to a distinct `.candidate`; never overwrite source or requested final output.
3. Read-only verify candidate in fixed order: integrity → foreign keys → exact expected scope → backup identity.
4. On any failure, return typed rejection and quarantine candidate. On success only, atomically publish the separate output and verified manifest.

## API / interface changes

| Surface | Success | Validation/error contract |
| --- | --- | --- |
| CLI `reconstructibility` | Canonical JSON result; exit 0 even when denominator is zero | Usage/invalid scope/period and unavailable/corrupt reads exit 2 with stable code on stderr. |
| MCP `accounting_reconstructibility` | Text content containing canonical JSON | Strict argument decode. Domain error text begins with the stable code; JSON-RPC transport remains successful as in existing MCP domain-failure convention. |
| HTTP `GET /accounting/reconstructibility` | 200 + canonical JSON | `INVALID_RECONSTRUCTIBILITY_SCOPE` / `INVALID_PERIOD` → 400; `RECONSTRUCTIBILITY_SCOPE_MISMATCH` and `RECONSTRUCTIBILITY_UNAVAILABLE` → 500. Bearer/shared-token behavior is unchanged. |
| CLI `doctor --db` | Extended routine `DoctorReport` | Never runs `integrity_check`. Existing success/failure exit convention remains. |
| CLI `doctor --drill-copy ... --snapshot-manifest ...` | Extended full `DoctorReport` for marked copy | `INVALID_DRILL_PATH`/`DRILL_COPY_REQUIRED` → usage/typed failure; cannot be combined with `--db`; source is never opened. |
| Internal restore/corruption drill service | Typed report/manifest evidence | `STORE_WRITE_FROZEN`, `CORRUPTION_NOT_DETECTED`, `RESTORE_VERIFICATION_FAILED`, `BACKUP_IDENTITY_MISMATCH`; no repair/unfreeze operation exists. |

`API.Reconstructibility(ctx, scope)` is the canonical server method. The route/tool/CLI do not call store methods directly. HTTP registers the new GET before generic routes and uses a dedicated exact-scope parser requiring period and company identity. MCP catalog wording explicitly says “read-only observation; does not authorize or approve.”

## Security and audit considerations

- **No new authority:** metric and health results are observations. No principal is accepted, no receipt is minted, and no lifecycle or accounting operation is authorized. HTTP/MCP retain existing access guards but those guards do not turn the result into an authorization decision.
- **Structural isolation:** the metric query includes organization, company ID, RUC, kind, and period in SQL. Returned rows are checked again against the requested scope. A valid other-tenant query returns its own zero/result only; leakage is never post-filtered.
- **Closed failure vocabulary:** non-reconstructibility emits one of six FZ-2 reasons only. Infrastructure failures stop the report rather than being disguised as a reason.
- **Drill-only corruption surface:** the corruption function is not exposed over HTTP or MCP. Its CLI/test path accepts only marked copies and rejects live paths, symlink/canonical-path aliasing, existing outputs, and source/copy equality. Normal store open rejects drill-marked databases. Test fixtures and manifests contain no customer data or secrets.
- **Evidence preservation:** snapshots, corrupted copies, rejected restore candidates, hashes, and manifests are fsynced and never modified by checks. No in-place repair exists.
- **DoS control:** routine doctor excludes full integrity; fuzz input is capped at 1 MiB and CI is 30 seconds per target. Metric SQL is exact-scope and latest-head constrained.
- **Key compromise:** FZ-3 is unchanged. The playbook records authenticated compromise time, preserves evidence, distinguishes pre-cutoff retention from at/after rejection, and forbids a revoked key from signing. Unparseable timestamps fail closed.
- **Non-claims:** reconstructibility and successful drills do not assert accounting correctness, production backup fitness, or external security validation.

## Migration and compatibility

- **SQLite migration: none.** All additions are reads, connection pragmas, in-memory drill latches, filesystem manifests, and separate snapshot/output files. No table, column, trigger, schema version, receipt payload, or persisted lifecycle change is planned.
- **Frozen contract migration: none.** `contracts/verification.md` remains untouched. The reconstructibility result is additive through existing adapter families and is not inserted into a frozen receipt or accounting contract.
- **Golden data:** add new vector files/contracts and the matching TS dispatcher/types in one change. This is test-contract expansion, not persisted-store migration.
- **Rollout order:** land pure metric logic and vectors; store query and service; adapters; doctor checks; snapshot/corruption/restore drills; fuzz/CI; G-7 playbook and evidence links. Routine doctor remains backward compatible with additive JSON fields. Rollback removes additive surfaces; drill files are external evidence and accounting rows need no rollback.
- **Flag condition:** if implementation discovers that a durable production write freeze is required, that is outside this copy-only drill design and would require a new schema/authority proposal. It must not be smuggled into this change.

## File change plan

- `internal/core/reconstructibility.go`, `internal/core/reconstructibility_test.go` — pure FZ-1/FZ-2/ratio.
- `core/reconstructibility.ts`, `core/__tests__/reconstructibility.test.ts`, exports — TS mirror, no `any`.
- `testdata/golden/reconstructibility-*.json`, `internal/core/golden_test.go`, matching TS golden dispatcher — parity vectors.
- `internal/store/reconstructibility_store.go`, tests — exact-scope latest-head SQL.
- `internal/server/reconstructibility_service.go`, tests; `api.go` — canonical aggregation/result.
- `cmd/drenyra-engram/main.go`, `main_test.go` — CLI command/help/parity/error exits.
- `internal/server/mcp.go`, `mcp_reconstructibility_test.go` — tool catalog/call/strict scope.
- `internal/server/http.go`, `reconstructibility_http.go`, HTTP tests — GET route and stable codes.
- `internal/store/doctor.go` or a focused extraction from `store.go`, plus tests — check modes/report fields/pragma.
- `internal/store/drill.go`, tests — manifests, `VACUUM INTO`, corruption evidence, freeze latch, restore verification.
- `internal/core/comprobante_fuzz_test.go`, `internal/core/receipt_fuzz_test.go`, `internal/search/search_fuzz_test.go` and package-relative corpus directories.
- Root `Makefile` and existing CI workflow — `fuzz-ci` only, 30 seconds each.
- `internal/core/verify_test.go`, `internal/server/verify_service_test.go`, existing signing/store test — G-7 boundaries.
- `docs/security/key-compromise-response.md` — playbook and gap analysis.

## Testing strategy

Testing is organized as work units, but every acceptance criterion has explicit proof.

### WU-1 — Pure metric contract and parity

- **AC-8 / FZ-1:** table tests vary one axis at a time: latest/non-latest; every `MemoryStatus`; all fiscal effects including `none` and `approval`; materiality nil/normal/material/critical; numeric `Materiality` values proving irrelevance; company/institutional scope; organization/company/RUC/period mismatches. Only the frozen conjunction is eligible.
- **AC-8 / FZ-2:** table tests make each category the first failure and construct combinations where later failures coexist, proving precedence is approval → receipt → evidence → object → rule → rule-version. Include direct non-approved classifier input even though the production denominator excludes it. Assert exactly one category and reject/compile out arbitrary reason strings.
- **AC-8 zero case:** golden/unit proof for `{0,0}`, `percentage:null`, `zeroDenominator:true`, all six empty arrays; non-zero cases pin integer truncation (for example 2/3 → 66), no float.
- **AC-9/AC-10:** the same vectors run under `TestGoldenVectorsGo` and TS tests and compare canonical JSON bytes; TypeScript typecheck proves no `any` and tests use integer/`bigint`-safe inputs.

### WU-2 — Metric store, aggregation, and adapters

- **AC-8 scope isolation:** seed two organizations, two companies sharing topic keys, and adjacent periods. SQL query for A/period P returns only A/P latest heads; B rows are never loaded. A valid empty scope returns zero; malformed/partial scope fails closed.
- **AC-8 latest determinism:** seed superseded/rejected/draft heads and older approved revisions; prove only the latest revision can enter and output IDs are bytewise sorted independent of insertion order.
- **AC-8 classifier integration:** use real verification reports for each layer failure; `ErrNoReceipts` maps to `receipt_failed`, while injected persistence errors abort with `RECONSTRUCTIBILITY_UNAVAILABLE`.
- **AC-8 read-only:** hash all SQLite database bytes/state plus relevant row counts/envelopes before and after two runs; results are byte-identical and state hash/counts are unchanged. Use a quiescent test DB so byte hash is meaningful.
- **AC-10 adapters:** CLI, MCP, and HTTP golden-response tests compare the same seeded result bytes. Missing period/company identity, institutional scope, unknown MCP fields, defensive out-of-scope reader result, and HTTP cross-tenant probes all fail/return zero as designed. Catalog/route review proves read-only verbs and no authority fields.

### WU-3 — Doctor and resilience drills

- **AC-3:** instrument/query-hook tests assert routine order `quick_check` then `foreign_key_check`, explicit `integrityCheck:not_run`, and no `integrity_check` statement. Full-copy tests assert `integrity_check` then paired `foreign_key_check`. Connection tests pin reported `cellSizeCheck` on/off behavior for supported and unsupported pragma outcomes.
- **AC-4:** create a WAL-active source, snapshot via `VACUUM INTO`, corrupt only the marked evidence copy, detect it, assert the next write returns `STORE_WRITE_FROZEN` before transaction start, retry and reopen attempts remain refused, before/after corrupted-copy SHA-256 is identical across checking/refused writes, no repair SQL executes, and the source DB bytes/logical state are unchanged. Negative path proves `CORRUPTION_NOT_DETECTED` if corruption is ineffective.
- **AC-5:** successful restore proves all four checks execute in order and only then the final output appears/opens. Separate negatives cover corrupt candidate, FK violation, missing/wrong exact-scope rows, wrong manifest hash/identity, same source/output path, pre-existing output, and interrupted candidate. Every negative returns typed rejection, leaves snapshot untouched, and never publishes the final output.
- **Scope isolation:** scope-conformance verification rejects a readable snapshot containing only another company/period and does not enumerate foreign rows in the error.

### WU-4 — Fuzz harnesses and bounded CI

- **AC-1:** each of the exactly three targets runs its committed corpus under `go test ./...`. For each parser, call twice and compare typed result/error classification and output bytes; validate successful comprobante/CDR schema fields; validate canonical receipt strict reparse/recanonicalization and complete-bytes/hash stability across v0.4.0–v0.10.0; validate tokens are deterministic, non-empty, and separator-free. Inputs over 1 MiB return before parsing.
- **AC-2:** package corpora include valid production-shaped fictional data, empty/one-byte/just-below-cap/at-cap seeds, malformed/truncated/wrong-encoding/depth/invalid-UTF-8/canonical-shape seeds, and minimized prior crashers. A harness regression fixture demonstrates that a known bad corpus input fails before its fix and remains committed afterward. A Makefile contract test or CI script inspection pins exactly three `-fuzztime=30s` invocations and non-zero propagation; no unbounded CI command exists.
- **Security:** corpus scanning rejects credentials, tokens, real RUC/customer fixtures, and oversized committed files except the documented boundary seed.

### WU-5 — G-7 cutoff evidence and playbook

- **AC-6:** documentation test/readback asserts all eight ordered response steps, exact compromise-time recording, NIST reference, pre-cutoff retention policy, at/after fail-closed policy, non-authorization/recovery boundaries, and a completed symbol-by-symbol gap-analysis conclusion.
- **AC-7 pure seam:** table tests directly exercise existing `VerifySigningKeyValidity`: issued one nanosecond before cutoff passes; equal rejects; after rejects; `created_at > issued_at` rejects; malformed created/issued/revoked timestamps reject; empty `revoked_at` follows the current active-key contract.
- **AC-7 service seam:** a fully signed memory receipt chain repeats before/equal/after and confirms only the signing-key-validity layer changes while report construction remains read-only. A store/signing test proves revoked keys refuse new receipts.
- **Contract guard:** a test/document comparison pins implementation to the existing `contracts/verification.md`; no semantic production edit is accepted merely to make a newly written test pass.

### WU-6 — Full gates and review checks

- **AC-11:** run in configured order: `npm run typecheck`, `go vet ./...`, `gofmt -l .`, `go test ./...`, `npm test`, plus `go test ./internal/core -run TestGoldenVectorsGo` and bounded `make fuzz-ci`.
- **AC-10 reviewer proof:** inspect every new command/tool/route for read-only call flow, absence of principal/approval/post/file/recovery authority, integer-only percentage, precise TS types, and copy-only corruption path.
- **Migration proof:** assert schema version remains 14 and opening an existing v14 fixture requires no migration.

### Acceptance-criterion traceability

| Criterion | Primary proof |
| --- | --- |
| AC-1 | WU-4 exactly-three target/corpus/invariant tests |
| AC-2 | WU-4 Makefile/CI bound and committed-crasher regression |
| AC-3 | WU-3 routine/full query-order and report tests |
| AC-4 | WU-3 copy corruption/freeze/hash/live-untouched test |
| AC-5 | WU-3 ordered verify-after-restore positive and negative matrix |
| AC-6 | WU-5 playbook structural readback + gap evidence |
| AC-7 | WU-5 pure, service, and signing refusal boundary tests |
| AC-8 | WU-1 pure matrix + WU-2 deterministic scoped integration/read-only hash |
| AC-9 | WU-1 Go↔TS golden vectors |
| AC-10 | WU-1 type/integer checks + WU-2 adapter contract + WU-6 review |
| AC-11 | WU-6 full gates |
