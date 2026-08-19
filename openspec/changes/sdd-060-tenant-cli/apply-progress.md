# Apply Progress — sdd-060-tenant-cli

> Phase: apply · Artifact: apply-progress · Status: done
> Inputs: spec + design + tasks. Strict TDD per slice; all gates green at the
> final boundary (`go test ./... -count=1`, `npm test` 386/386, typecheck, vet,
> gofmt, `TestGoldenVectorsGo`).

## Slice A — tenant list (FR-TEN-1, AC-TEN-1) — DONE

- `internal/core/tenant.go` (new): pure `TenantSummary`/`TenantCompany`/
  `TenantListResult` shapes (ids/counts only, never content).
- `internal/store/tenant.go` (new): `TenantList(ctx)` — identities UNION
  observations, deterministic ordering (organizationId → companyId → period),
  no schema change (D-TEN-7).
- `cmd/drenyra-engram/tenant.go` (new): `cmdTenant` + `list` subcommand;
  `case "tenant"` in the dispatch switch; usage lines (main + subcommand).
- `internal/store/tenant_test.go` + `cmd/drenyra-engram/tenant_test.go`:
  `TestTenantList`, `TestTenantListEmpty`, `TestCLITenantList`,
  `TestCLITenantListEmpty`, `TestCLITenantUsageErrors`; `cliDispatchPaths`
  catalog guard extended (`tenant list`, `tenant consolidate`).

## Slice B — fold + drift detection + dry-run (FR-TEN-2/3/4, AC-TEN-2/3) — DONE

- `internal/core/topic_fold.go` (new): `FoldTopicKey` (PURE; lower-case →
  punctuation-to-separator → whitespace collapse; accents NOT folded,
  documented) + `DriftedChain`/`DriftGroup`/`ConsolidateReport` shapes.
- `core/topic-fold.ts` (new): TS twin; `testdata/golden/topic-fold.json`
  vectors + Go runner (`runTopicFoldGolden`) + TS golden dispatch — Go↔TS
  byte-identical (golden parity joined, `TestGoldenVectorsGo` green).
- `internal/store/tenant.go`: `DriftCandidates(ctx, scope)` — tenant-scoped
  (organization/company/RUC pinned; empty period = whole tenant, groups per
  (folded key, period)); deterministic canonical (most observations, tie →
  lexicographic).
- `cmd/drenyra-engram/tenant.go`: `consolidate` subcommand with
  `--ruc/--period/--dry-run/--apply`; dry-run default (ZERO writes — proven by
  superseded-count snapshot equality); `--dry-run`+`--apply` → usage error 2.
- Tests: `TestFoldTopicKey*`, `TestCLIConsolidateDryRun`,
  `TestCLIConsolidateMutuallyExclusive`.

## Slice C — apply merge + audit + docs (FR-TEN-5/6/7, AC-TEN-4/5/7) — DONE

- Apply path: per-group exact (tenant, period) scope; `chainHead` resolves the
  latest active/pending_review head (fail closed per merge); merge via the
  existing `API.Supersede` (memory_superseded receipt + transition_log —
  audit standard, FR-TEN-6); per-merge ledger output; non-zero exit + failure
  list on any failure (no rollback of succeeded merges — documented).
- Tests: `TestCLIConsolidateApply` (drifted head superseded + receipt present),
  `TestCLIConsolidateIsolation` (tenant-B chains untouched by an A-only apply:
  no B supersede, no B receipt).
- Docs (docs_as_code): usage text (main + verify fix), README CLI list, DOCS.md
  CLI table, ROADMAP.md SDD-060 delivered section.

## Deviation notes

- The drift scope semantics: `--period` empty = WHOLE tenant, with groups per
  (folded key, period) so merges stay inside one exact period scope — faithful
  to FR-TEN-2's "empty = whole tenant", and the exact-scope supersede invariant
  is preserved (FR-TEN-7).
- `tenant list` is deliberately an OPERATOR surface (same class as `doctor`):
  ids/counts only, no per-tenant content, no session required (D-TEN-6).
