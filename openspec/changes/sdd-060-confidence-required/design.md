# Design — sdd-060-confidence-required

> Phase: design · Artifact: design · Status: draft
> Inputs: frozen spec. Single change (≈250–350 lines across ~35 files; most
> edits are one-line fixture additions — under the 400-line rule, no chained PR
> expected).

## Decisions

- `D-CN-1` — **`Confidence float64` non-pointer in the core model.**
  `core.AccountingMemory.Confidence` and `core.SaveInput.Confidence` become
  `float64` with `json:"confidence"` (no `omitempty`). This is the
  compile-time enforcement of FR-CN-1: a writer literally cannot omit the
  field. The clone path copies the value; validation in `Validate()` runs on
  every write (drop the `!= nil` guard, keep the `INVALID_CONFIDENCE` range
  check).
- `D-CN-2` — **Additive v16→v17 migration, trigger-guarded (no table
  rewrite, legacy rows preserved).** SQLite cannot `ALTER COLUMN ... SET
  NOT NULL`, and the store's frozen rule forbids rewriting the observations
  table. So v17: `migrateV16ToV17(db)` in `internal/store/migration_v17.go` —
  (a) creates TWO triggers (SQLite has no combined `INSERT OR UPDATE`
  trigger): `observations_confidence_required_insert` (BEFORE INSERT) and
  `observations_confidence_required_update` (BEFORE UPDATE), each aborting
  with `CONFIDENCE_REQUIRED` when `NEW.confidence IS NULL` — drop-then-create
  inside the transaction for idempotency (same pattern as v15→v16);
  (b) `setSchemaVersionTx(ctx, tx, 17)` last; any failure rolls back to v16.
  Crash between trigger creation and version write converges on reopen.
  **Legacy NULL-confidence rows are preserved**: the migration does not scan
  for them and does not fail on them — they read back as confidence 0. Failing
  closed on legacy NULLs was considered and rejected because the column has
  been nullable since v1→v2 with no backfill, so every pre-v17 store with at
  least one unscored memory would be stranded (violates additive migration).
  The invariant is enforced forward: core type + validation + triggers.
- `D-CN-3` — **`CONFIDENCE_REQUIRED` is a NEW error code** (contradicts the
  "no new error codes" binding? NO — the trigger error string is a raw SQLite
  `RAISE(ABORT, ...)` message, not a Go error code; the Go layer surfaces it
  as `persistence error: <trigger message>`. No new exported Go error code is
  added; `INVALID_CONFIDENCE` (validation) remains the only confidence code.)
- `D-CN-4` — **One shared test helper.** Add `confidenceForTest() float64` (or
  a constant `testConfidence = 0.8`) to the store/server test-support surface;
  fixtures that don't test confidence use it. Keeps the sweep mechanical.
- `D-CN-5` — **Envelope bytes change for NEW writes only.** Confidence is
  always serialized now, so new envelopes differ from pre-change ones. Legacy
  golden vectors are frozen and untouched; docs state the new-write change.
  No re-hash of existing rows (the migration validates, never rewrites).

## File placement

| File | Purpose |
| --- | --- |
| `internal/core/types.go` | `Confidence float64` (memory + save input), validation without nil guard, clone copy |
| `internal/core/types_test.go` | validation always-on tests (out-of-range fails even when previously skipped) |
| `internal/store/migration_v17.go` | `migrateV16ToV17` (validate no legacy NULL rows; create trigger; version 17) |
| `internal/store/store.go` | `schemaVersion` 16→17; v16→v17 chain wiring |
| `internal/store/migration_v17_test.go` | migration tests (legacy NULL row aborts naming scope; clean v16 upgrades; crash-convergence; trigger blocks NULL insert) |
| `internal/server/mcp.go` | `accounting_save` supplies explicit confidence |
| `internal/server/close_service.go` | close summary supplies explicit confidence |
| test fixtures (~30 files) | one-line confidence additions via helper |
| `README.md`, `DOCS.md`, `ROADMAP.md`, `CHANGELOG.md` | docs_as_code |

## Behavior matrix

| Store state | Action | Result |
| --- | --- | --- |
| fresh (no db) | open | bootstraps to v17, trigger present |
| v16, no NULL-confidence rows | open | upgrades to v17 additively, rows preserved, no re-hash |
| v16, legacy NULL-confidence rows | open | upgrades cleanly to v17; legacy rows preserved, read back as confidence 0 (no re-hash, no backfill) |
| v17 | direct SQL INSERT with confidence NULL | trigger aborts `CONFIDENCE_REQUIRED` |
| any | save with confidence 1.5 | `INVALID_CONFIDENCE` (validation, every write) |

## Verification gates

- `go test ./internal/core ./internal/store ./internal/server -count=1` then
  `go test ./... -count=1`; `npm test`; `npm run typecheck`; `go vet ./...`;
  `gofmt -l cmd internal`; `TestGoldenVectorsGo`.
- Reviewer greps: no table rewrite, no re-hash, no money fields, no
  authorization semantics, no exported error-code additions; production
  writers always set confidence.
