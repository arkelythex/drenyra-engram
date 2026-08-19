# Verify Report — sdd-060-confidence-required

> Phase: verify · Status: done · 2026-08-19

## Scope

Full-gate verification of the required-confidence change (FR-CN-1…5,
AC-CN-1…6) against a fresh working tree (`-count=1`).

## Evidence

| Gate | Command | Result |
| --- | --- | --- |
| Go build | `go build ./...` | PASS |
| Go unit suite | `go test ./...` (fresh) | PASS — all 10 packages ok (cmd 35.4s, core 4.7s, search 6.4s, server 150.1s, store 107.6s, sync 11.5s) |
| Confidence migration tests | `go test ./internal/store -run 'TestConfidence' -count=1` | PASS — clean v16→v17 upgrade, legacy NULL rows preserved, crash-convergence, trigger blocks NULL insert |
| Golden vectors | `go test ./internal/core -run TestGoldenVectorsGo -count=1` | PASS (confidence does not participate in content/identity/envelope hashes — frozen vectors untouched) |
| Go vet | `go vet ./...` | PASS |
| gofmt | `gofmt -l internal cmd` | clean |
| TS suite | `npm test` (vitest) | PASS — 26 files, 386 tests |
| Typecheck | `npm run typecheck` (tsc --noEmit) | PASS |

## AC mapping

| AC | Verdict | Evidence |
| --- | --- | --- |
| AC-CN-1 `SaveInput` without confidence does not compile | PASS | field is non-pointer `float64` — a missing field is a compile error by construction |
| AC-CN-2 out-of-range confidence fails on every write | PASS | `internal/core/types_test.go` (confidence 1.5 → INVALID_CONFIDENCE, was skipped pre-change) |
| AC-CN-3 fresh store bootstraps v17; triggers block NULL writes | PASS | `TestConfidenceMigrationCleanV16Upgrades`, `TestConfidenceTriggerBlocksNullInsert` |
| AC-CN-4 legacy NULL rows upgrade cleanly, preserved | PASS | `TestConfidenceMigrationPreservesLegacyNullRows` (row survives with NULL, read back as confidence 0; new NULL writes blocked) |
| AC-CN-5 crash between trigger and version write converges | PASS | `TestConfidenceMigrationCrashConvergesToV17` |
| AC-CN-6 full gates green + docs | PASS | table above; CHANGELOG/README/DOCS/ROADMAP updated |

## Reviewer-grep surface (FR-CN-5)

- No table rewrite: v17 only creates two triggers + bumps the version marker.
- No re-hash, no backfill: legacy rows byte-identical (NULLs read as confidence 0).
- No money fields introduced; confidence is a probability (documented "never money").
- No authorization semantics; no exported error-code additions (the trigger's
  CONFIDENCE_REQUIRED is a raw SQLite RAISE message surfaced by the store,
  not a new exported Go code).
- Production writers (MCP save, close service) always supply confidence.
- Authority boundary unchanged: `drenyra-ai` `MEMORY_SHAPED` still rejects the
  memory channel as evidence.

## Conclusion

PASS. All acceptance criteria verified green on a fresh run; the working tree is
deliverable (ordinary-policy commits per the change's delivery note).
