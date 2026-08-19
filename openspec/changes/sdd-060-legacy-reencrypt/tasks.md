# Tasks — sdd-060-legacy-reencrypt

> Phase: tasks · Artifact: tasks · Status: draft
> Inputs: spec + design (required). Strict TDD — RED → GREEN per slice;
> `go test ./... -count=1` and `npm test` stay green at every boundary.
> Delivery: single change (≈200–280 lines, under the 400-line rule), review
> clone-off — ordinary-policy commits.

## Review Workload Forecast

| Field | Value |
| --- | --- |
| Estimated changed lines | ≈ 200–280 (store method + CLI + tests + docs) |
| 400-line budget risk | Low |
| Chained PRs recommended | No (single change) |
| Chain strategy | n/a |

---

## Slice 1 — store re-encryption + report (FR-RE-2/3/4, AC-RE-1/2/3/4/5/6)

- [x] 1.1 RED — `internal/core/reencrypt.go` pure `ReencryptReport` shape +
  `internal/store/crypto_test.go`: `TestReencryptDryRunZeroWrites` — seed legacy
  (plaintext) + encrypted rows across two tenants + one institutional row;
  `LegacyEncryptionCounts` reports per-tenant legacy counts, excludes encrypted
  - institutional; store snapshot identical (no ciphertext change). RED until
  the method exists. <!-- sdd-owner: implementation -->
- [x] 1.2 GREEN — `LegacyEncryptionCounts(ctx)` (read-only SELECT
  `scope_kind='company' AND content_algo=''` grouped by organization_id) +
  `ReencryptLegacyContent(ctx)` (fail-closed without key; ONE transaction;
  per-row tenant-key encryption + redaction; row-id named on failure; idempotent
  by construction). <!-- sdd-owner: implementation -->
- [x] 1.3 RED — `TestReencryptApplyHashesUnchanged`: capture content/identity/
  envelope hashes of every legacy row BEFORE `--apply`; after, hashes identical,
  ciphertext present + algo marker, plaintext columns redacted; institutional
  and already-encrypted rows untouched; second `--apply` is a no-op (idempotent).
  <!-- sdd-owner: implementation -->
- [x] 1.4 GREEN — complete the apply path; assertions green. <!-- sdd-owner: implementation -->
- [x] 1.5 RED — `TestReencryptFailClosed`: store WITHOUT key → `ReencryptLegacyContent`
  errors `ENCRYPTION_REQUIRED`; `LegacyEncryptionCounts` works read-only without
  a key (the report is safe). <!-- sdd-owner: implementation -->
- [x] 1.6 RED — `TestMigrationV15Additive` + `TestMigrationCrashReopenConvergesToV16`
  (D-RE-7): the v15 trigger exists before migration; after, the ONLY permitted
  content mutation is plaintext→encrypted; any other immutable UPDATE still
  aborts; a crash between trigger swap and version write converges on reopen.
  <!-- sdd-owner: implementation -->
- [x] 1.7 GREEN — `migrateV15ToV16` in `internal/store/migration_v16.go` + wiring in
  `store.go` (version 15 branch); schemaVersion 15→16; all migration tests
  re-pointed at 16. <!-- sdd-owner: implementation -->

**Gate slice 1:** `go test ./internal/store -count=1` then `go test ./... -count=1`.

---

## Slice 2 — CLI + docs (FR-RE-1/5, AC-RE-7)

- [x] 2.1 RED — `cmd/drenyra-engram/encrypt_test.go` `TestCLIEncrypt`: seeded
  legacy store + env key → default dry-run report JSON (per-tenant counts,
  total) exit 0 zero writes; `--apply` exit 0 + rows encrypted (verify via
  second context read); no-key → `ENCRYPTION_REQUIRED` exit 1; `--dry-run
  --apply` → usage error 2. RED until the command exists. <!-- sdd-owner: implementation -->
- [x] 2.2 GREEN — `cmd/drenyra-engram/encrypt.go` `cmdEncrypt` + dispatch case
  - usage line + `cliDispatchPaths` entry. <!-- sdd-owner: implementation -->
- [x] 2.3 TRIANGULATE — reviewer greps: no schema change, no new error code, no
  money fields, no authorization semantics; full gate
  (typecheck → vet → gofmt → `go test ./... -count=1` → npm test) green.
  <!-- sdd-owner: implementation -->
- [x] 2.4 GREEN — docs_as_code: usage text, README/DOCS/ROADMAP + CHANGELOG
  entry. <!-- sdd-owner: implementation -->

**Gate slice 2 (final):** config `verify_order` green fresh; `TestGoldenVectorsGo`
green.

---

## Cross-cutting checklist

- [x] Conventional commits (feat + docs); no AI attribution.
- [x] Strict TDD per slice: named failing tests land RED first.
- [x] Fail-closed discipline: no-key re-encryption impossible; no partial
  batch (single transaction).
- [x] Integrity: hashes/receipts/relations untouched (test-guarded).
- [x] No money fields, no `any`, no schema change, no authorization semantics.

## Definition of done

- [x] All tasks checked; every AC-RE-1…7 verified green by its mapped test.
- [x] Full gates per config `verify_order` fresh (`-count=1`) + golden green.
