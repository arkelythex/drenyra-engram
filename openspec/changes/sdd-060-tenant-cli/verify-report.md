# Verify Report — sdd-060-tenant-cli

> Phase: verify · Artifact: verify-report · Status: PASS
> Inputs: spec + tasks + apply-progress. Final whole-change gate run FRESH
> (`-count=1`) after the last edit.

## Result: PASS — all acceptance criteria verified green

| Criterion | Evidence |
| --- | --- |
| AC-TEN-1 tenant list enumeration | `TestTenantList` + `TestTenantListEmpty` (store) + `TestCLITenantList`/`TestCLITenantListEmpty` (CLI) — deterministic ids/counts from identities + observations, never content |
| AC-TEN-2 fold purity + golden parity | `TestFoldTopicKey*` + `TestGoldenVectorsGo` (contract `topic-fold`) + vitest golden 386/386 — Go↔TS byte-identical vectors |
| AC-TEN-3 dry-run zero writes | `TestCLIConsolidateDryRun` — drift group + deterministic canonical + superseded-count snapshot equality before/after |
| AC-TEN-4 apply merge + audit | `TestCLIConsolidateApply` — drifted head superseded; `memory_superseded` receipt present; merge ledger correct |
| AC-TEN-5 adversarial isolation | `TestCLIConsolidateIsolation` — tenant-B chains untouched by an A-only apply (no B supersede, no B receipt) |
| AC-TEN-6 usage errors | `TestCLIConsolidateMutuallyExclusive` + `TestCLITenantUsageErrors` — `--dry-run`+`--apply` and invalid RUC → exit 2 |
| AC-TEN-7 gates + docs | Full gate fresh green: `go test ./... -count=1` all ok, `npm test` 386/386, typecheck, `go vet ./...`, `gofmt -l .`, `TestGoldenVectorsGo`; usage text + README + DOCS + ROADMAP updated |

## Constraints honored

- No schema change (schema v14 untouched — pure reads + existing supersede).
- No money fields; no new error codes beyond the supersede path.
- Non-authorization boundary: consolidate authorizes nothing; merge reuses the
  audited supersede path only.
- Scope-first fail-closed: `DriftCandidates` pins the tenant tuple; empty period
  = whole tenant with per-period groups; merges stay inside one exact period
  scope.
- No `any` in TS; golden parity joined for `FoldTopicKey`.

## Gate results (final, fresh)

- `go test ./... -count=1` — all packages ok.
- `npm test` — 386/386 (26 files) · `npm run typecheck` — clean.
- `go vet ./...` — clean · `gofmt -l .` — clean.
- `go test ./internal/core -run TestGoldenVectorsGo` — ok.

## Notes

- pi-lens runner diagnostics during apply were stale (replayed the RED-state
  snapshot: reported `TenantList undefined` while the method existed and the
  fresh `-count=1` suite passed) — a runner-cache defect, not a code defect.
- Delivery: three atomic conventional commits (tenant list → consolidate
  detection → consolidate apply + docs); review clone-off, ordinary-policy
  commits per user decision.
