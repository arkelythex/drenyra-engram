# Archive Report — sdd-060-confidence-required

> Phase: archive · Status: done · 2026-08-19

## Executive summary

SDD-060 criterion 3 ("every MemoryEntry carries confidence") is now enforced
executably: `confidence` is a REQUIRED non-pointer `float64` in the core model
(range-validated on every write), schema advances v16→v17 additively with two
`observations_confidence_required_*` triggers, both production writers supply
explicit values, and the full test fixture sweep (86 callers, 30 files) was
updated. Legacy rows are preserved unchanged — the migration neither re-hashes
nor backfills, and legacy NULLs read back as confidence 0.

## Delivered

- `core.AccountingMemory.Confidence` / `core.SaveInput.Confidence`: `float64`
  non-pointer, always serialized, `INVALID_CONFIDENCE` validated on every write.
- `internal/store/migration_v17.go`: `migrateV16ToV17` — additive v17 with
  `observations_confidence_required_insert` (BEFORE INSERT) and
  `observations_confidence_required_update` (BEFORE UPDATE OF confidence)
  triggers; drop-then-create idempotency; crash-convergent version bump.
- Production writers: MCP `accounting_save` (0.5 neutral), close summary (0.9).
- Fixture sweep: 86 `SaveInput` callers supply `Confidence: 0.8`; golden
  vectors untouched (confidence does not participate in the hashes).
- Docs as code: CHANGELOG, README, DOCS, ROADMAP (Phase 6g).

## Design correction (documented in apply-progress)

The planned fail-closed-on-legacy-NULL behavior was rejected during apply: the
`confidence` column has been nullable since v1→v2 with no backfill, so failing
the migration on legacy NULLs would strand every pre-v17 store that ever saved
an unscored memory — violating the additive-migration rule. Legacy NULLs are
preserved (read as confidence 0); the invariant is enforced forward.

## Verification

All AC-CN-1…6 green on a fresh run: full Go suite (10 packages), confidence
migration tests, golden vectors, vet, gofmt, TS suite (386), typecheck. See
verify-report.md.

## Final state

- Change archived; `next_recommended: none`; no blocked reasons.
- Working-tree changes (feat + docs) are the deliverable, to be committed under
  ordinary repository policy (conventional commits, no AI attribution).
- `sdd-060-confidence-required` closes SDD-060 DoD criterion 3. The remaining
  SDD-060 surface (Fase 2 MCP integration) is deliberately deferred per
  REQ-BOUND-001 in drenyra-pi; the authority half of criterion 3 is already
  enforced more strictly in drenyra-ai (`MEMORY_SHAPED`).
