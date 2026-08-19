# Archive Report — sdd-060-legacy-reencrypt

> Phase: archive · Status: done · 2026-08-19

## Executive summary

The `encrypt` operator command closes the legacy-plaintext gap: company-scope
observations written before at-rest encryption was enabled can now be
re-encrypted under `DRENYRA_ENCRYPTION_MASTER_KEY` (dry-run default, ZERO
writes; `--apply` in ONE transaction, fail-closed). Content/identity/envelope
hashes, receipts, relations and the transition log are untouched — the
decrypted memory is byte-identical. The change also advances schema 15→16 via
an additive immutability-trigger refinement (D-RE-7) that permits exactly the
plaintext→encrypted transition and nothing else.

## Delivered

- `drenyra-engram encrypt [--dry-run | --apply] [--db <path>]` CLI command
  (exit codes 0/1/2; `--dry-run --apply` → usage error 2).
- Store methods `LegacyEncryptionCounts` (read-only, key-safe) and
  `ReencryptLegacyContent` (single transaction, idempotent, failing row named).
- Pure `core.ReencryptReport` / `core.ReencryptTenantCount` shapes.
- Additive v15→v16 migration `migrateV15ToV16` (trigger-only, no layout change;
  corruption-signal validation; crash-convergent).
- Docs as code: README / DOCS / ROADMAP / CHANGELOG.

## Verification

All AC-RE-1…7 green on a fresh run: full Go suite, store/CLI re-encrypt tests,
golden vectors, TS suite (386), typecheck, vet, gofmt, cross-tenant adversarial
isolation. See verify-report.md.

## Final state

- Change archived; `next_recommended: none`; no blocked reasons.
- Working-tree changes (feat + docs) are the deliverable, to be committed under
  ordinary repository policy (conventional commits, no AI attribution).
- `sdd-060-legacy-reencrypt` completes the SDD-060 Fase 3 surface: consolidate +
  audit trail + at-rest encryption (incl. legacy re-encryption) are now fully
  delivered.
