# Tasks — v1-readiness

> Phase: tasks · Artifact: tasks · Status: draft
> Inputs: spec + design (required). Strict TDD: not active in this phase (planning); `phases.apply.strict_tdd: true` per openspec/config.yaml — apply must run RED → GREEN per slice.
> Forwarded session settings: delivery_strategy=ask-on-risk (default), chain_strategy=pending, pr_boundary=not set.
> FZ-1…FZ-4 and D-1…D-10 are binding; apply MUST NOT deviate.

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ≈ 4,400–5,600 (additions + deletions, incl. fuzz seed corpus and golden vectors) |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR 1 → PR 2 → PR 3 → PR 4 → PR 5 |
| Delivery strategy | ask-on-risk |
| Chain strategy | pending |

```text
Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: pending
400-line budget risk: High
```

Per-work-unit line estimates: W1 fuzz ≈ 800–1,000 (harnesses ~380, seed corpus ~400, Makefile/CI ~55, crasher regression ~80); W2 doctor/drills ≈ 1,100–1,400; W3 G-7 ≈ 550–750 (playbook ~260, boundary/doc tests ~420); W4 G-10 ≈ 1,900–2,400 (pure core + TS + golden ~1,050, store + server ~510, adapters ~550). Total ≈ 4,400–5,600 → exceeds the 400-line rule in openspec/config.yaml (`pr_size`), so the change MUST be split into chained PRs.

**Suggested chain (each PR ≤ ~1,600 lines, autonomous, independently verifiable):**

- PR 1 — W4 metric core: pure FZ-1/FZ-2/ratio + TS mirror + golden vectors + store head query + server aggregator (no adapters yet).
- PR 2 — W4 adapters: CLI `reconstructibility`, MCP `accounting_reconstructibility`, HTTP GET route.
- PR 3 — W2 doctor checks + copy-only corruption/restore drills.
- PR 4 — W1 fuzz targets, seed corpora, root Makefile `fuzz-ci`, CI wiring.
- PR 5 — W3 G-7 playbook, gap analysis, cutoff boundary tests.

Chain strategy is `pending`: the orchestrator must ask the user for `stacked-to-main` vs `feature-branch-chain` before apply. Do NOT start apply until the guard decision is made.

---

## Work units

### W1 — G-6 fuzz targets and bounded CI (AC-1, AC-2; FR-1, FR-2, FR-3; NFR-2 seeds)

Objective: three frozen same-package fuzz targets with committed seed corpora, 1 MiB input cap, and a bounded 30s-per-target `make fuzz-ci` gate. No parser invariant is weakened. Files: `internal/core/comprobante_fuzz_test.go`, `internal/core/receipt_fuzz_test.go`, `internal/search/search_fuzz_test.go`, `internal/core/testdata/fuzz/FuzzParseComprobanteXML/`, `internal/core/testdata/fuzz/FuzzCanonicalReceiptPayload/`, `internal/search/testdata/fuzz/FuzzSearchTokenize/`, root `Makefile` (new), CI workflow file (`.github/workflows/*`).

- [x] 1.1 RED — Write `internal/core/comprobante_fuzz_test.go` (`package core`): `FuzzParseComprobanteXML` drives `ParseComprobanteXML` + `ParseCDRXML` (`internal/core/comprobante.go`); asserts no panic, determinism (call twice, byte-compare result/error classification), no-invalid-success (nil error ⇒ parsed `Comprobante` fields satisfy the frozen schema contract), and returns immediately above 1 MiB with the cap documented in the target doc comment. <!-- sdd-owner: implementation -->
- [x] 1.2 RED — Write `internal/core/receipt_fuzz_test.go` (`package core`): `FuzzCanonicalReceiptPayload` drives `CanonicalReceiptPayload`/`CompleteReceiptBytes`/`ReceiptHash` (`internal/core/receipt.go`) across frozen payload versions `receipt-payload/v0.4.0`…`v0.10.0`; asserts no panic, deterministic bytes, round-trip stability (payload re-parses and re-canonicalizes byte-identically), no-invalid-success, and the 1 MiB cap documented. <!-- sdd-owner: implementation -->
- [x] 1.3 RED — Write `internal/search/search_fuzz_test.go` (`package search`): `FuzzSearchTokenize` drives `tokenize` (`internal/search/search.go`); asserts no panic, deterministic tokens, non-empty token lists, no separator characters in emitted tokens, and the 1 MiB cap documented. <!-- sdd-owner: implementation -->
- [x] 1.4 GREEN — Commit the seed corpus per target under `internal/core/testdata/fuzz/FuzzParseComprobanteXML/`, `internal/core/testdata/fuzz/FuzzCanonicalReceiptPayload/`, `internal/search/testdata/fuzz/FuzzSearchTokenize/`: valid production-shaped fictional samples (no real RUC/customer data), boundary samples (empty, 1 byte, max size, just-below-cap), malformed samples (truncated XML, wrong encoding, nested/unbounded depth probes, invalid UTF-8, canonical-shape violations), and every previously-failing input; add a short README/index per target naming the bug class of each committed crasher. Corpus entries must remain green under `go test ./...`. <!-- sdd-owner: implementation -->
- [x] 1.5 TRIANGULATE — Add a named unit regression test proving a known crasher input fails the target invariant before its fix and stays green after; assert the corpus entry is never deleted while the bug class exists (see FR-2). <!-- sdd-owner: implementation -->
- [x] 1.6 REFACTOR — Add root `Makefile` (new) with `fuzz-ci` running exactly three explicit foreground invocations (`go test ./internal/core -run '^$' -fuzz='^FuzzParseComprobanteXML$' -fuzztime=30s`, the same for `FuzzCanonicalReceiptPayload`, and `go test ./internal/search -run '^$' -fuzz='^FuzzSearchTokenize$' -fuzztime=30s`), each propagating non-zero exit; no unbounded fuzz command exists anywhere in CI. Wire the existing CI workflow to invoke `make fuzz-ci` without changing target budgets. Add a Makefile-contract test (or CI-script inspection) pinning exactly three `-fuzztime=30s` invocations. <!-- sdd-owner: implementation -->
- [x] 1.7 REFACTOR — Security sweep over seeds/fixtures: scan corpus, READMEs, and drill fixtures for credentials, tokens, and real customer/RUC data; assert oversized committed files are only the documented boundary seed (NFR-2). <!-- sdd-owner: implementation -->

**Verification gate W1:** `go test ./...` green (corpus files run as unit tests); `make fuzz-ci` completes 3×30s and exits non-zero on any failure; `gofmt -l .` clean; `go vet ./...` clean.

---

### W2 — G-6 doctor health checks and copy-only drills (AC-3, AC-4, AC-5; FR-4, FR-5, FR-6, FZ-4; NFR-1, NFR-2, NFR-4)

Objective: extend `DoctorReport` with the four SQLite check fields (routine = `quick_check` + `foreign_key_check`, full `integrity_check` only on the explicit drill path and always paired with `foreign_key_check`); deliver copy-only corruption drill with write-freeze latch and `VACUUM INTO` restore drill with ordered verify-after-restore. Live DB never corrupted, repaired, or auto-reopened. Files: `internal/store/store.go`, `internal/store/drill.go` (new), `internal/store/doctor.go` (new or extracted), store tests (`internal/store/*_test.go`), `cmd/drenyra-engram/main.go`, `cmd/drenyra-engram/main_test.go`.

- [x] 2.1 RED — Extend `internal/store/store.go` `DoctorReport` (line ~6610) with `quickCheck {status,detail}`, `integrityCheck {status:"not_run"|"ok"|"failed",detail}`, `foreignKeyCheck {status,violationCount,detail}`, `cellSizeCheck {effective:"on"|"off",detail}`; add `DoctorOptions{Mode: Routine|Full}`. Write failing store tests asserting: routine `Doctor()` runs `PRAGMA quick_check` then `PRAGMA foreign_key_check`, reports `integrityCheck:not_run`, and never emits an `integrity_check` statement (query-hook instrumentation). <!-- sdd-owner: implementation -->
- [x] 2.2 GREEN — Implement the routine path: `Doctor()` executes quick_check → foreign_key_check; enable `PRAGMA cell_size_check=ON` at connection initialization where compatible with the existing connection lifecycle and always report the effective value (documented when unsupported). Keep all checks read-only; no mutation of the database. <!-- sdd-owner: implementation -->
- [x] 2.3 RED — Write `internal/store/drill.go` tests + failing implementation for `CreateDrillSnapshot(input)`: distinct canonical source/copy paths, refuse overwrite, `VACUUM INTO ?` with safely bound output path, close/sync output, SHA-256 hash, atomic sidecar manifest (format version, canonical source/copy paths, source snapshot hash, creation timestamp, expected scope). Assert the source DB is untouched and the snapshot is a consistent standalone DB. <!-- sdd-owner: implementation -->
- [x] 2.4 RED — Implement + test the drill-marker contract: `openStore` rejects a database carrying the adjacent `<copy>.drenyra-drill.json` marker with typed `DRILL_COPY_ONLY`; full doctor (`DoctorOptions{Mode:Full}` via `DoctorCopy`) requires the marked copy, read-only `mode=ro`/query-only open, runs `integrity_check` then paired `foreign_key_check` (test asserts the pairing), and never opens the manifest's source path; CLI `doctor --drill-copy <copy.db> --snapshot-manifest <manifest.json>` is mutually exclusive with `--db` (rejected with `INVALID_DRILL_PATH`/`DRILL_COPY_REQUIRED` usage). Extend `main_test.go` doctor block for the four new JSON fields and the mode contract. <!-- sdd-owner: implementation -->
- [x] 2.5 RED — Implement + test `RunCorruptionDrill` (AC-4): copies the snapshot once more to a dedicated evidence path (fsync), deterministically flips bytes in a selected non-header database page, hashes the corrupted bytes, opens ONLY that marked evidence copy through the drill-only store, runs full doctor, and requires detection; on no detection return typed `CORRUPTION_NOT_DETECTED`. Test asserts: detection reported; the next write returns typed `STORE_WRITE_FROZEN` before transaction start; retry cannot clear the latch; the corrupted copy's SHA-256 is identical before/after checking and refused writes (byte preservation); no repair SQL executes (no in-place repair); the live/source DB bytes and logical state are unchanged; no unfreeze/repair method exists. <!-- sdd-owner: implementation -->
- [x] 2.6 RED — Implement + test the restore drill (AC-5, D-7): copy the immutable snapshot bytes to `<requested>.candidate` (fsync file and dir), open read-only, verify in this fixed order — `integrity_check` → `foreign_key_check` → exact expected scope-row conformance → expected backup identity (manifest SHA-256 + schema/identity metadata); only after all four pass, atomically rename to the separate requested output and emit a verified manifest. Negative matrix, each returning typed `RESTORE_VERIFICATION_FAILED` / `BACKUP_IDENTITY_MISMATCH` and never publishing the output: corrupted candidate bytes, FK violation, missing/wrong exact-scope rows (no foreign-row enumeration in the error), wrong manifest hash/identity, same source/output path, pre-existing output, interrupted candidate; source snapshot untouched and rejected candidate quarantined in every negative. <!-- sdd-owner: implementation -->
- [x] 2.7 TRIANGULATE — Scope-isolation conformance test for drills: a readable snapshot containing only another company/period fails the scope-conformance check and is rejected; assert cross-tenant invisibility for both metric and drill surfaces (IR-2). <!-- sdd-owner: implementation -->

**Verification gate W2:** `go test ./internal/store ./cmd/drenyra-engram` green (AC-3/4/5 tests pass); `go test ./...` green; routine doctor never runs `integrity_check`; `gofmt -l .` and `go vet ./...` clean.

---

### W3 — G-7 cutoff evidence and key-compromise playbook (AC-6, AC-7; FR-7, FR-8, FZ-3; NFR-5)

Objective: prove the EXISTING `VerifySigningKeyValidity` seam matches `contracts/verification.md` on FZ-3 with boundary tests (before/at/after cutoff, `created_at > issued_at`, fail-closed unparseable timestamps, empty `revoked_at`), prove revoked keys refuse to sign, and deliver `docs/security/key-compromise-response.md` with the eight NIST-aligned steps and the implementation-vs-contract gap analysis. No semantic production change unless the gap tests demonstrate a real mismatch (contract guard). Files: `internal/core/verify_test.go`, `internal/server/verify_service_test.go`, signing/store seam test (`internal/store` or existing signing test), `docs/security/key-compromise-response.md` (new), doc readback test.

- [x] 3.1 RED — Extend `internal/core/verify_test.go` with a table-driven boundary matrix over the existing pure `core.VerifySigningKeyValidity` (`internal/core/verify.go:328`), RFC3339 timestamps, one case per FZ-3 row: `issuedAt < revokedAt` (one nanosecond before cutoff) → valid when all other layers pass; `issuedAt == revokedAt` → rejected (equality fails); `issuedAt > revokedAt` → rejected; `createdAt > issuedAt` → rejected; revoked key with unparseable `revoked_at` → fail closed (never a guess); revoked-but-never-cutoff (empty `revoked_at`) → current active-key contract. No production edit is permitted merely to make a new test pass (contract guard; if a test fails, stop and surface the mismatch before any semantic change). <!-- sdd-owner: implementation -->
- [x] 3.2 RED — Extend `internal/server/verify_service_test.go` with a fully signed receipt chain repeating before/equal/after the cutoff through the verification service path; assert only the signing-key-validity layer changes while report construction stays read-only/no-transaction. Add the store/signing seam test proving a revoked key refuses to sign new receipts. <!-- sdd-owner: implementation -->
- [x] 3.3 GREEN — Write `docs/security/key-compromise-response.md` (new): purpose and non-claims; roles/prerequisites; the exact eight-step NIST-aligned sequence (treat exposure as full trust failure → stop signing and suspend verification with the compromised key → preserve evidence and record the exact compromise time → generate an independent replacement keypair in a clean environment → publish an authenticated revocation → fail closed for signatures at/after the cutoff → inventory and re-sign affected artifacts where policy permits → investigate adjacent systems); cutoff policy with before/equal/after examples citing NIST SP 800-57 pre-compromise retention; command/evidence checklist; recovery and re-signing constraints; and the gap-analysis table comparing `SQLiteStore.SigningKeyForVerify`/`LookupSigningKey`, the one-way revoke-only trigger/write path, `core.VerifySigningKeyValidity`, and `contracts/verification.md`, concluding implementation==FZ-3 with quoted symbol/contract evidence (or naming the hardening item implemented in this change). No `contracts/` file changes. <!-- sdd-owner: implementation -->
- [x] 3.4 TRIANGULATE — Add a structural doc readback test asserting: all eight ordered steps present, exact compromise-time recording, NIST reference, pre-cutoff retention policy, at/after fail-closed policy, non-authorization/recovery boundaries, and a completed gap-analysis conclusion with quoted evidence. Add the contract-guard comparison pinning implementation to `contracts/verification.md` (AC-6/NFR-5). <!-- sdd-owner: implementation -->

**Verification gate W3:** `go test ./internal/core ./internal/server` green (AC-7 matrix + AC-6 readback); doc readback green; `gofmt -l .` and `go vet ./...` clean; `git diff -- contracts/` empty.

---

### W4 — G-10 reconstructibility metric, all adapters, golden parity (AC-8, AC-9, AC-10; FR-9, FR-10, FZ-1, FZ-2; NFR-1, NFR-3, IR-1, IR-4, IR-5)

Objective: deterministic read-only reconstructibility baseline for one exact company scope + period via pure FZ-1 eligibility, FZ-2 first-failure classifier with the closed six-reason vocabulary, integer-only ratio/percentage, and the frozen result shape — exposed through CLI (`reconstructibility <ruc> --period`), MCP (`accounting_reconstructibility`), and HTTP (GET `/accounting/reconstructibility`), all delegating to one `internal/server` service. New shared pure logic joins the Go↔TS golden mechanism. Files: `internal/core/reconstructibility.go` (+test), `core/reconstructibility.ts` (+test, exports), `testdata/golden/reconstructibility-*.json`, `internal/core/golden_test.go`, TS golden dispatcher, `internal/store/reconstructibility_store.go` (+test), `internal/server/reconstructibility_service.go` (+test), `internal/server/api.go`, `cmd/drenyra-engram/main.go` (+`main_test.go`), `internal/server/mcp.go` (+`mcp_reconstructibility_test.go`), `internal/server/http.go` + `internal/server/reconstructibility_http.go` (+HTTP tests).

- [x] 4.1 RED — Write `internal/core/reconstructibility.go` failing tests + implementation of pure `IsMaterialDecision(memory, requestedScope, isLatest)` (FZ-1: latest revision only; exact company scope with matching CompanyID/RUC and byte-equal Period; `StatusApproved`; `FiscalEffect` in exactly {JournalEntry, Adjustment, Reclassification, Declaration, Closing, SunatFiling}; `MaterialityLevel` in {Material, Critical} — nil/normal excluded; numeric `Materiality` cents MUST NOT participate). Table tests vary one axis at a time: latest/non-latest; every `MemoryStatus`; all fiscal effects incl. `none`/`approval`; materiality nil/normal/material/critical; numeric `Materiality` values proving irrelevance; company vs institutional scope; organization/company/RUC/period mismatches. <!-- sdd-owner: implementation -->
- [x] 4.2 RED — Write failing tests + implementation of pure `ClassifyReconstructibility(memory, verificationReport)` applying FZ-2 in frozen first-failure order with exactly one closed reason per decision: approval (`not_approved`) → full signed receipt chain + approval provenance (`receipt_failed`) → evidence availability (`missing_evidence`) → object availability (`evidence_missing_object`) → rule availability (`rule_unresolved`) → rule-version/vigencia (`rule_version_failed`). Table tests make each category the first failure and combine later-failure coexistence to prove precedence; include a direct non-approved classifier input (reachable in the unit surface though excluded from the production denominator); assert exactly one category; the reason vocabulary is a closed enum (no arbitrary strings compile in). <!-- sdd-owner: implementation -->
- [x] 4.3 RED — Write failing tests + implementation of the integer ratio/percentage helper: `percentage = (numerator*100)/denominator` in integer math with overflow guard (counts originate from bounded slice length, guard anyway), `null` when denominator == 0; pin truncation (e.g. 2/3 → 66); zero-denominator unit proof for `{denominator:0,numerator:0,ratio:{0,0},percentage:null,zeroDenominator:true}` with all six reason lists empty (`[]`, never `null`). <!-- sdd-owner: implementation -->
- [x] 4.4 GREEN — Mirror FZ-1/FZ-2/ratio in `core/reconstructibility.ts` (new) with precise types, `bigint`/integer-safe count handling, closed reason union, and NO `any` (IR-5); export from the package index; add `core/__tests__/reconstructibility.test.ts` with the same matrix (including 2/3 → 66 and zero-denominator bytes). <!-- sdd-owner: implementation -->
- [x] 4.5 GREEN — Add golden vectors under `testdata/golden/reconstructibility-*.json` covering every eligibility axis, the six-reason precedence combinations, the direct `not_approved` case, and ratio/zero-denominator bytes; extend `internal/core/golden_test.go` so `TestGoldenVectorsGo` runs them; update the TS golden dispatcher so both runtimes compare byte-identical canonical JSON (AC-9, IR-4). <!-- sdd-owner: implementation -->
- [x] 4.6 RED — Write failing tests + implementation of `internal/store/reconstructibility_store.go` `LatestMaterialDecisionHeads(ctx, scope)`: a single SQL read scoped to the exact six-column scope tuple + byte-equal period, selecting only the max revision per `(topic_key, exact scope)` and applying FZ-1 status/fiscal-effect/materiality predicates in SQL; never scans/returns another scope (structural, not post-filter). Tests: two organizations + two companies sharing topic keys + adjacent periods → A/P returns only A/P latest heads; superseded/rejected/draft heads and older approved revisions excluded; valid empty scope returns zero; malformed/partial scope fails closed. <!-- sdd-owner: implementation -->
- [x] 4.7 RED — Write failing tests + implementation of `internal/server/reconstructibility_service.go`: `Reconstructibility(ctx, scope)` depends only on `MaterialDecisionReader` (`LatestMaterialDecisionHeads`) and `MemoryVerifier` (`VerifyMemory` via a small server adapter over `server.VerifyMemory(ctx, store, id)`); defensively re-runs pure `IsMaterialDecision` per row and rejects out-of-scope rows with typed `RECONSTRUCTIBILITY_SCOPE_MISMATCH`; `ErrNoReceipts` maps to `receipt_failed`; persistence/decode/corruption errors abort the whole report with `RECONSTRUCTIBILITY_UNAVAILABLE` (never mislabeled as a business reason); assembles the frozen `ReconstructibilityResult` (field order: scope, period, denominator, numerator, ratio, percentage, reasons, zeroDenominator) with concrete reason struct in frozen vocabulary order and decision IDs sorted bytewise. Integration tests: real verification reports per layer failure; byte-identical double-run determinism; read-only proof (pre/post store-state hash + row/envelope counts unchanged); no-transaction discipline on the read path. <!-- sdd-owner: implementation -->
- [x] 4.8 RED — Add CLI `reconstructibility <ruc> --period <YYYYMM> [--company-id <id>] [--organization <id>] [--db <path>] [--objects <dir>]` in `cmd/drenyra-engram/main.go` following the existing `doctor` command pattern: JSON to stdout, exit 0 even when denominator is zero, exit 2 with stable code on stderr for invalid/ambiguous scope/period or unavailable/corrupt read; `companyId := ruc` derivation only when `--company-id` omitted. Extend `main_test.go`: golden JSON parity, exit codes, zero-denominator exit 0, missing `--period` exit 2. <!-- sdd-owner: implementation -->
- [x] 4.9 RED — Add MCP tool `accounting_reconstructibility` in `internal/server/mcp.go` following the `engram_doctor` read-only pattern (catalog wording: “read-only observation; does not authorize or approve”): one required strict-decoded `scope` object (`kind=company`, non-empty `companyId`, valid `ruc`, valid non-empty `period`); unknown arguments fail closed; domain error text begins with the stable code; JSON-RPC transport stays successful per existing MCP domain-failure convention. Add `mcp_reconstructibility_test.go`: strict decode failures, unknown args, valid call golden JSON. <!-- sdd-owner: implementation -->
- [x] 4.10 RED — Add HTTP `GET /accounting/reconstructibility?organizationId=<id>&companyId=<id>&ruc=<11-digits>&period=<YYYYMM>` in `internal/server/http.go` route table + `internal/server/reconstructibility_http.go`, mounted under the existing shared-token read guard; all four exact-scope fields required (no generic `companyId := ruc` fallback on HTTP); `INVALID_RECONSTRUCTIBILITY_SCOPE`/`INVALID_PERIOD` → 400; `RECONSTRUCTIBILITY_SCOPE_MISMATCH`/`RECONSTRUCTIBILITY_UNAVAILABLE` → 500; register before generic routes. HTTP tests: missing fields, cross-tenant probe isolation, golden body bytes, guard behavior unchanged. <!-- sdd-owner: implementation -->
- [x] 4.11 TRIANGULATE — Adapter parity test: CLI, MCP, and HTTP golden-response tests compare the same seeded result bytes; reviewer-checkable assertions that every new surface is read-only/observational and accepts no principal/action/approval/recovery field (AC-10, IR-3). <!-- sdd-owner: implementation -->

**Verification gate W4:** `go test ./...` green; `go test ./internal/core -run TestGoldenVectorsGo` green; `npm test` green; `npm run typecheck` clean (no `any`); `gofmt -l .` and `go vet ./...` clean.

---

## Verification gates per work unit

- W1: `go test ./...` (corpus green) + `make fuzz-ci` (3×30s, non-zero on failure) + `gofmt -l .` / `go vet ./...` clean.
- W2: `go test ./internal/store ./cmd/drenyra-engram` then `go test ./...`; AC-3/4/5 tests green; routine doctor never runs `integrity_check`.
- W3: `go test ./internal/core ./internal/server`; AC-7 matrix + AC-6 doc readback green; `git diff -- contracts/` empty.
- W4: `go test ./...` + `go test ./internal/core -run TestGoldenVectorsGo` + `npm test` + `npm run typecheck` (no `any`).
- Full chain gate per PR boundary: `npm run typecheck` → `go vet ./...` → `gofmt -l .` → `go test ./...` → `npm test` (config verify_order), then bounded `make fuzz-ci` once W1 lands.

## Cross-cutting checklist

- [x] Conventional commit per atomic milestone (`feat:`, `test:`, `docs:`, `build:`, `bench:`); no AI attribution. <!-- sdd-owner: implementation -->
- [x] Money stays whole int64 cents / BigInt cents; no float path anywhere (percentage is integer division; money never appears in drill/metric arithmetic — IR-1). <!-- sdd-owner: implementation -->
- [x] Scope stays structural and fails closed on mismatch; cross-tenant invisibility tested for metric and drills (IR-2). <!-- sdd-owner: implementation -->
- [x] Non-authorization boundary intact: no new surface approves, posts, files, reopens writes, or authorizes recovery (IR-3); revoked keys never sign. <!-- sdd-owner: implementation -->
- [x] Docs-as-code: `docs/security/key-compromise-response.md` and any behavior docs land in the same PR as their code; stale docs are a defect. <!-- sdd-owner: implementation -->
- [x] No `any` in TypeScript (IR-5); seeds/fixtures contain no credentials, tokens, or customer data (NFR-2). <!-- sdd-owner: implementation -->
- [x] Schema version stays 14; opening an existing v14 fixture requires no migration (migration-proof assertion in the apply slice). <!-- sdd-owner: implementation -->

## Definition of done

- [x] All tasks checked; every acceptance criterion AC-1…AC-11 in `spec.md` verified green by its mapped test: AC-1/2 (W1), AC-3/4/5 (W2), AC-6/7 (W3), AC-8/9/10 (W4), AC-11 (full gates). <!-- sdd-owner: implementation -->
- [x] Full gates per config verify_order: `npm run typecheck` → `go vet ./...` → `gofmt -l .` → `go test ./...` → `npm test`; plus `go test ./internal/core -run TestGoldenVectorsGo` and bounded `make fuzz-ci` green. <!-- sdd-owner: implementation -->
- [ ] Review Workload Guard decision recorded (delivery_strategy, chain_strategy) before apply; change delivered as chained PRs per the forecast split. <!-- sdd-owner: parent -->
- [ ] Bounded post-apply review of the chained diff (native review as applicable per repository policy), then `sdd-verify` against the spec, then `sdd-archive`. <!-- sdd-owner: parent -->

## Parent lifecycle gates (post-apply, grouped)

- [ ] Start or reuse bounded review for each PR boundary after its normalization + candidate freeze; one correction budget per candidate; no reviewer launched by apply. <!-- sdd-owner: parent -->
- [ ] After the final PR merges, run the verify phase (`sdd-verify`) against AC-1…AC-11 and this tasks list; remediate only through the bounded correction path. <!-- sdd-owner: parent -->
- [ ] Archive the change only when verify reports all criteria green and the chain is fully merged. <!-- sdd-owner: parent -->
