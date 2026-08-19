# Spec — sdd-060-confidence-required

> Phase: spec · Artifact: spec · Status: draft
> Inputs: proposal. Frozen bindings: fail-closed, additive-only migrations
> (never rewrite tables), no money fields, docs_as_code, strict TDD for apply.

## Functional requirements

### FR-CN-1 — confidence required in the core model

- `core.AccountingMemory.Confidence` becomes `float64` (non-pointer,
  `json:"confidence"` — ALWAYS serialized, no `omitempty`).
- `core.SaveInput.Confidence` becomes `float64` (non-pointer, always present).
- Range validation runs on EVERY write: `INVALID_CONFIDENCE` when
  `confidence < 0 || confidence > 1` (existing error code, no new codes).
- The clone path (`AccountingMemory.Clone`) copies the value.

### FR-CN-2 — schema guard (v16→v17, additive)

- SQLite cannot `ALTER COLUMN ... SET NOT NULL`; the store must NOT rewrite
  the observations table (frozen additive-migration rule).
- Instead, schema version advances 16→17 via an additive migration that:
  (a) creates a `BEFORE INSERT` trigger and a `BEFORE UPDATE` trigger
  (`observations_confidence_required_insert` / `_update`) that abort with
  `CONFIDENCE_REQUIRED` when `NEW.confidence IS NULL` (defense in depth for
  any writer that bypasses the core validation);
  (b) sets `schema_version = 17` last; any failure rolls the migration back to
  v16 (crash-convergent, same pattern as v15→v16).
- **Legacy rows are PRESERVED unchanged**: rows written before this change
  (when the column was nullable and never backfilled) keep their NULL
  confidence and read back as confidence 0. The migration does NOT fail on
  them, does NOT re-hash and does NOT backfill — failing closed on legacy NULLs
  would strand every pre-v17 store with at least one unscored memory, which
  violates the additive-migration rule. The required-confidence invariant is
  enforced FORWARD: new writes must carry confidence (core type + validation +
  triggers).

### FR-CN-3 — production writers set confidence

- `internal/server/mcp.go` (`accounting_save` path) supplies an explicit
  confidence for every save.
- `internal/server/close_service.go` supplies an explicit confidence for the
  monthly-close summary memory.
- A writer may not omit it (compile-time via the non-pointer field).

### FR-CN-4 — test fixtures sweep

- Every `SaveInput` fixture across the Go suite supplies a confidence.
- A single shared helper (in the store/server test-support surface) provides an
  incidental default (e.g. `0.8`) so tests whose subject is NOT confidence keep
  one-line fixtures.
- No test may exercise "confidence omitted" as a valid path (it no longer
  exists).

### FR-CN-5 — envelope/hash semantics

- Confidence already participates in the content and envelope hashes (frozen
  decision). Making it always-present changes the canonical bytes of NEW
  writes only.
- Legacy golden vectors that freeze pre-change envelopes remain untouched (no
  TS/golden edit; the migration does not re-hash).
- Docs state the new-write envelope change explicitly.

## Acceptance criteria

- AC-CN-1: `SaveInput` without a confidence value does not compile (field is
  non-pointer, required).
- AC-CN-2: a write with `confidence` out of [0,1] fails `INVALID_CONFIDENCE`
  (validated on every write, including MCP and close paths — covered by tests).
- AC-CN-3: a fresh store bootstraps to schema v17; the
  `observations_confidence_required` trigger exists; a direct SQL INSERT with
  `confidence = NULL` aborts with `CONFIDENCE_REQUIRED`.
- AC-CN-4: a v16 store containing legacy NULL-confidence rows upgrades
  cleanly to v17 — the rows are PRESERVED unchanged (no re-hash, no backfill)
  and read back as confidence 0; the triggers block NEW NULL-confidence writes
  (covered by a dedicated test).
- AC-CN-5: crash between trigger creation and version write converges to v16 on
  reopen and re-runs the migration (tested).
- AC-CN-6: full gates green (`go test ./... -count=1`, `go vet`, `gofmt`,
  typecheck, npm test, golden vectors); docs updated (README/DOCS/ROADMAP/
  CHANGELOG).

## Out of scope

Authority-boundary changes (`drenyra-ai` `MEMORY_SHAPED` already enforces the
stronger rule); confidence→draft wiring (consumer-side, deferred per
REQ-BOUND-001); `materiality` optionality; legacy data backfill; TS/golden
edits to frozen vectors.
