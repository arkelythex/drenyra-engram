# Apply Progress — sdd-060-legacy-reencrypt

> Phase: apply · Status: done · 2026-08-19

## Summary

Implemented the `encrypt` operator command for legacy plaintext → at-rest
encryption re-encryption, plus the additive v15→v16 immutability-trigger
refinement that makes the redaction UPDATE legal without weakening the
immutable-content guard.

## Slices

### Slice 1 — store re-encryption + report (FR-RE-2/3/4, AC-RE-1/2/3/4/5/6) — done

- `internal/core/reencrypt.go`: pure `ReencryptReport` + `ReencryptTenantCount`
  shapes (ids/counts only, never content; no money fields).
- `internal/store/crypto.go`:
  - `LegacyEncryptionCounts(ctx)` — read-only per-tenant SELECT of
    `scope_kind='company' AND content_algo=''`, safe without a key.
  - `ReencryptLegacyContent(ctx)` — fail-closed (`ENCRYPTION_REQUIRED` without
    master key); ONE transaction; per-row tenant-derived key encryption
    (aes-256-gcm-v1, fresh nonce); plaintext columns redacted; failing row id
    named; idempotent by construction (already-encrypted rows outside the
    SELECT); deterministic sorted per-tenant report.
- `internal/store/migration_v16.go` (D-RE-7): `migrateV15ToV16` — validates the
  v15 immutability trigger exists (corruption signal if missing), drops and
  recreates it permitting ONLY the legacy-plaintext → encrypted-at-rest
  transition, sets schema_version=16 last; any failure rolls back to 15.
- `internal/store/store.go`: `schemaVersion` 15 → 16; version-15 migration
  branch wired.
- Tests (RED→GREEN): `TestReencryptDryRunZeroWrites`, `TestReencryptApplyHashesUnchanged`,
  `TestReencryptFailClosed`, `TestMigrationV15Additive`, `TestMigrationCrashReopenConvergesToV16`,
  `TestDirectUpgradeMatrixV1ToV16` (re-pointed), per-version migration tests
  re-pointed at 16.

### Slice 2 — CLI + docs (FR-RE-1/5, AC-RE-7) — done

- `cmd/drenyra-engram/encrypt.go`: `cmdEncrypt` — `--dry-run` default (ZERO
  writes), `--apply` executes in one transaction, mutually exclusive (usage
  error 2), exit codes 0/1/2.
- `cmd/drenyra-engram/main.go`: dispatch `case "encrypt"` + usage line.
- `cmd/drenyra-engram/no_override_surface_test.go`: `cliDispatchPaths` entry.
- `cmd/drenyra-engram/encrypt_test.go`: `TestCLIEncrypt` — dry-run report +
  zero writes, apply + fail-closed key-less read, no-key apply failure,
  idempotent second apply, usage errors.
- Docs as code: README/DOCS/ROADMAP/CHANGELOG updated with the `encrypt`
  command, env var, and v16 trigger note.

## Cross-cutting checklist

- [x] Conventional commits (feat + docs); no AI attribution.
- [x] Strict TDD per slice: named failing tests landed RED first.
- [x] Fail-closed discipline: no-key re-encryption impossible; no partial batch
      (single transaction).
- [x] Integrity: hashes/receipts/relations untouched (test-guarded).
- [x] No money fields, no `any`, no authorization semantics, no error-code
      additions.

## Out-of-scope kept out

Institutional-scope re-encryption, cloud/KMS/HSM, TS/golden changes.

## Deviations from planning (reconciled)

- **D-RE-7 (new):** the v15 immutability trigger rejects ANY immutable-column
  UPDATE, which would block the redaction UPDATE `--apply` performs. Schema
  version advances 15→16 via an additive trigger-only migration (no column or
  layout change). Spec/design/tasks updated to reflect this before archive.
