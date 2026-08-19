# Tasks — sdd-060-confidence-required

> Phase: tasks · Artifact: tasks · Status: draft
> Inputs: spec + design (required). Strict TDD — RED → GREEN per slice;
> `go test ./... -count=1` and `npm test` stay green at every boundary.
> Delivery: single change (≈250–350 lines, under the 400-line rule).

## Review Workload Forecast

| Field | Value |
| --- | --- |
| Estimated changed lines | ≈ 250–350 (core type + migration + writers + fixture sweep + docs) |
| 400-line budget risk | Low–medium (fixture sweep is mostly one-liners) |
| Chained PRs recommended | No (single change) |
| Chain strategy | n/a |

---

## Slice 1 — core model + validation (FR-CN-1, AC-CN-1/2)

- [ ] 1.1 RED — `internal/core/types_test.go`: `TestConfidenceRequiredOnEveryWrite`
      — a save whose confidence is 1.5 fails `INVALID_CONFIDENCE` (previously
      this path was skipped when nil); a valid 0..1 confidence passes. RED until
      the type change lands. <!-- sdd-owner: implementation -->
- [ ] 1.2 GREEN — `internal/core/types.go`: `Confidence *float64` → `float64`
      (non-pointer, `json:"confidence"`, no `omitempty`) in `AccountingMemory`
      and `SaveInput`; validation drops the `!= nil` guard; `Clone` copies the
      value. <!-- sdd-owner: implementation -->

**Gate slice 1:** `go test ./internal/core -count=1`.

---

## Slice 2 — schema v17 migration (FR-CN-2, AC-CN-3/4/5)

- [ ] 2.1 RED — `internal/store/migration_v17_test.go`:
      `TestConfidenceMigrationCleanV16Upgrades` (seed v16 store with no
      NULL-confidence rows; reopen → v17, rows preserved, trigger exists);
      `TestConfidenceMigrationFailsClosedOnLegacyNull` (seed one
      NULL-confidence row; reopen fails naming the offending scope);
      `TestConfidenceMigrationCrashConvergesToV17`; `TestConfidenceTriggerBlocksNullInsert`
      (direct SQL `confidence = NULL` → `CONFIDENCE_REQUIRED`). RED until the
      migration exists. <!-- sdd-owner: implementation -->
- [ ] 2.2 GREEN — `internal/store/migration_v17.go` `migrateV16ToV17` (validate
      no legacy NULL rows, fail closed naming scope; create trigger
      `observations_confidence_required` BEFORE INSERT OR UPDATE WHEN
      NEW.confidence IS NULL → `RAISE(ABORT, 'CONFIDENCE_REQUIRED')`; set
      schema_version 17 last; any failure rolls back to v16) + wiring in
      `store.go` (schemaVersion 16→17, v16 branch). <!-- sdd-owner: implementation -->
- [ ] 2.3 GREEN — re-point existing migration/version tests at 17 (the
      mechanical `want 16` → `want 17` sweep, matching the prior v15→v16 sweep).
      <!-- sdd-owner: implementation -->

**Gate slice 2:** `go test ./internal/store ./internal/core -count=1`.

---

## Slice 3 — production writers + fixture sweep (FR-CN-3/4, AC-CN-6)

- [ ] 3.1 GREEN — `internal/server/mcp.go` `accounting_save` and
      `internal/server/close_service.go` supply explicit confidence (e.g. 0.8)
      on every save. <!-- sdd-owner: implementation -->
- [ ] 3.2 GREEN — add shared test helper (constant `testConfidence = 0.8` in
      the store/server test-support surface); sweep every `SaveInput` fixture
      (~30 files, ~86 callers) to supply confidence. <!-- sdd-owner: implementation -->
- [ ] 3.3 TRIANGULATE — reviewer greps: no table rewrite, no re-hash, no money
      fields, no authorization semantics, no new exported error code; full
      gates (build → vet → gofmt → `go test ./... -count=1` → npm test →
      typecheck → golden) green. <!-- sdd-owner: implementation -->
- [ ] 3.4 GREEN — docs_as_code: README/DOCS/ROADMAP/CHANGELOG note the required
      confidence field + new-write envelope change + schema v17 trigger.
      <!-- sdd-owner: implementation -->

**Gate slice 3 (final):** config `verify_order` green fresh; `TestGoldenVectorsGo`
green.

---

## Cross-cutting checklist

- [ ] Conventional commits (feat + docs); no AI attribution.
- [ ] Strict TDD per slice: named failing tests land RED first.
- [ ] Fail-closed discipline: legacy NULL-confidence rows refuse migration; no
      silent backfill or re-hash.
- [ ] Additive-only: no observations-table rewrite; only the new trigger +
      version bump.
- [ ] No money fields, no `any`, no authorization semantics, no exported
      error-code additions.

## Definition of done

- [ ] All tasks checked; every AC-CN-1…6 verified green by its mapped test.
- [ ] Full gates per config `verify_order` fresh (`-count=1`) + golden green.
