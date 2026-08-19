# Proposal — sdd-060-confidence-required

> Phase: propose · Artifact: proposal · Status: draft
> Inputs: SDD-060 (Draft, §4 data model + §6 DoD criterion 3), user decision
> (2026-08-19) to implement the criterion as a full change.

## Problem

SDD-060 DoD criterion 3 states: **"Toda `MemoryEntry` tiene `confidence` y
ninguna entrada con `confidence: baja` puede alimentar un candidate draft sin
que la persona marque explícitamente que la sugerencia viene de memoria."**

The authority-side half of that criterion is already enforced more strictly in
`drenyra-ai` (the memory/engram channel is rejected as evidence —
`EvidenceErrorCode.MEMORY_SHAPED`). But the data-model half is **not** met in
this repository: `core.AccountingMemory.Confidence` is `*float64` (optional,
`omitempty`), the schema column is `confidence REAL` (nullable), validation
(`INVALID_CONFIDENCE`) only runs when the pointer is non-nil, and the two
production writers (`internal/server/mcp.go`, `internal/server/close_service.go`)
plus the test suite persist observations without a confidence value. A memory
without confidence cannot be told apart from a memory the author deliberately
scored — the SDD's "every entry carries confidence" invariant has no executable
teeth.

## Proposed capability

Make confidence a **required, always-present field** end to end:

- **Core type**: `Confidence float64` (non-pointer, `json:"confidence"` —
  always serialized) in `core.AccountingMemory` and `core.SaveInput`; range
  validation (`0 <= c <= 1`) runs on EVERY write, not only when non-nil.
- **Schema**: `observations.confidence REAL` becomes `NOT NULL DEFAULT 0`
  via the additive v16→v17 migration (validate no legacy NULL rows exist and
  fail closed otherwise; then rebuild the column constraint — no backfill of
  meaning, no rewrite of other columns).
- **Callers**: the two production writers set an explicit confidence; every
  test fixture that builds `SaveInput` supplies one (default helper where the
  value is incidental to the test).
- **Envelope/hash**: confidence already participates in the content/envelope
  hash (frozen decision); making it always-present changes the canonical bytes
  of NEW writes only — golden vectors that freeze legacy envelopes stay
  untouched, and the migration does not re-hash existing rows.

## Non-goals

- No change to the authority boundary (already enforced via `MEMORY_SHAPED` in
  `drenyra-ai`; `review/` and `gates/` there remain Engram-free).
- No confidence→candidate-draft wiring in this repository (that surface is a
  `drenyra-pi`/`drenyra-ai` consumer concern, deferred per REQ-BOUND-001).
- No change to `materiality` (stays optional, distinct concern).
- No TS/golden change to legacy vectors (frozen bytes unchanged).

## Delivery

Core type + validation + schema migration v17 + production callers + test
fixture sweep + docs. Estimated ≈ 250–350 lines of Go changes across ~35 files
(most are one-line fixture additions); near the 400-line budget — single
change, no chained PR expected.

## Risks

- **Test sweep size**: 86 `SaveInput` callers across ~30 test files. Mitigated
  by a single shared fixture helper for incidental values; verified by full
  suite (`go test ./... -count=1`).
- **Migration fail-closed**: a store with legacy NULL-confidence rows must not
  silently upgrade; the v17 migration aborts naming the offending scope.
- **Envelope bytes change for new writes**: intended and documented (the field
  is now always serialized); legacy golden vectors remain frozen.
