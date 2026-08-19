# Apply Progress — sdd-060-confidence-required

> Phase: apply · Status: done · 2026-08-19

## Summary

Made `confidence` a REQUIRED 0..1 field on every observation (SDD-060
criterion 3): non-pointer in the core model, range-validated on every write,
schema v16→v17 with two confidence-required triggers, production writers
supply explicit values, and the full test fixture sweep updated.

## Slices

### Slice 1 — core model + validation (FR-CN-1, AC-CN-1/2) — done

- `internal/core/types.go`: `Confidence *float64` → `float64`
  (`json:"confidence"`, no `omitempty`) in `AccountingMemory` and `SaveInput`;
  validation drops the `!= nil` guard (`INVALID_CONFIDENCE` on every write);
  `Clone` copies the value.
- `internal/core/types_test.go`: confidence-out-of-range now fails on every
  write (previously skipped when nil); valid 0..1 passes.

### Slice 2 — schema v17 migration (FR-CN-2, AC-CN-3/4/5) — done

- `internal/store/migration_v17.go` `migrateV16ToV17`: creates two SQLite
  triggers (`observations_confidence_required_insert` BEFORE INSERT;
  `observations_confidence_required_update` BEFORE UPDATE OF confidence), each
  aborting `CONFIDENCE_REQUIRED` on a NULL-confidence write; sets
  schema_version 17 last; drop-then-create inside one transaction (idempotent,
  crash-convergent, same pattern as v15→v16).
- `internal/store/store.go`: `schemaVersion` 16→17; v16→v17 chain wiring.
- `internal/store/migration_v17_test.go`: clean v16 upgrade (trigger exists,
  store writable), legacy NULL rows PRESERVED (upgrade clean, no backfill,
  new NULL writes blocked by triggers), crash-convergence, trigger blocks
  direct NULL insert.

### Slice 3 — production writers + fixture sweep + docs (FR-CN-3/4, AC-CN-6) — done

- `internal/server/mcp.go` `accounting_save` → `Confidence: 0.5` (neutral
  default unless caller supplies one).
- `internal/server/close_service.go` close summary → `Confidence: 0.9`
  (automated computation with deterministic inputs).
- Fixture sweep: 86 `SaveInput` callers across 30 test files supplied
  `Confidence: 0.8` (mechanical insertion + gofmt + blank-line cleanup).
- `internal/core/golden_test.go`: `confidenceOrZero` converter (vectors
  predate the field; confidence does NOT participate in content/identity/
  envelope hashes, so frozen golden vectors are untouched).
- Docs as code: CHANGELOG (new entry), README (required-field note), DOCS
  (schema note), ROADMAP (Phase 6g entry).

## Deviations from planning (reconciled during apply)

- **Legacy NULL-confidence rows are PRESERVED, not fail-closed.** The original
  design failed the v17 migration on any legacy NULL-confidence row. Applying
  it exposed a design flaw: the `confidence` column has been nullable since
  the v1→v2 migration with no backfill, so EVERY pre-v17 store with at least
  one unscored memory would be stranded (violates the additive-migration rule
  "never rewrite, preserve rows"). The migration now preserves legacy NULLs
  (read back as confidence 0, no re-hash, no backfill) and enforces the
  invariant FORWARD via core type + validation + triggers. Spec/design/tasks
  updated to match.
- **Two triggers, not one.** SQLite has no combined `INSERT OR UPDATE`
  trigger; the guard is a BEFORE INSERT + a BEFORE UPDATE OF confidence pair.
- **`UPDATE OF confidence` scoping.** The UPDATE trigger fires only when an
  update touches the confidence column, so legitimate updates of other columns
  on legacy NULL-confidence rows still surface their own errors (e.g.
  IMMUTABLE_OBSERVATION) instead of being masked by CONFIDENCE_REQUIRED.

## Out-of-scope kept out

Authority-boundary changes (`drenyra-ai` `MEMORY_SHAPED` already enforces the
stronger rule); confidence→draft wiring (deferred per REQ-BOUND-001);
`materiality` optionality; legacy data backfill; TS/golden edits to frozen
vectors.
