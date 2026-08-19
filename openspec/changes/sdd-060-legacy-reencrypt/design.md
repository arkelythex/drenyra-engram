# Design — sdd-060-legacy-reencrypt

> Phase: design · Artifact: design · Status: draft
> Inputs: frozen spec. Single slice (≈200–280 lines, under the 400-line rule —
> no chained PR needed). Delivery: one change, one commit pair (feat + docs).

## Decisions

- `D-RE-1` — **Store method `ReencryptLegacyContent(ctx) (ReencryptReport,
  error)` in `internal/store`.** The operation lives beside the encryption
  primitives (crypto.go / store.go) and reuses `encryptContent` + the store's
  configured `encMaster`. Fail-closed: `errEncryptionRequired` when the store
  was opened without the key (FR-RE-3).
- `D-RE-2` — **One transaction for `--apply`.** `BEGIN IMMEDIATE`; SELECT the
  target rows (`scope_kind='company' AND content_algo=''`), encrypt each with
  its tenant's derived key, UPDATE the three columns + redact the plaintext
  columns; COMMIT only after every row. Any failure → ROLLBACK (no partial
  re-encryption); the failing row id is named. Idempotent: rows with
  `content_algo != ''` are outside the SELECT (skipped by construction).
- `D-RE-3` — **Hashes untouched by construction + test-guarded.** The
  content/identity/envelope hashes are computed over the DECRYPTED memory;
  re-encryption changes only the at-rest envelope. The test captures the hashes
  before `--apply` and asserts equality after (AC-RE-3).
- `D-RE-4` — **CLI: `encrypt` top-level command** (`cmd/drenyra-engram/
  encrypt.go` + dispatch case + usage + `cliDispatchPaths` guard entry).
  `--dry-run` default, `--apply` to execute; store opened via the standard
  `openStore` (env-wired master key). Dry-run reuses the same SELECT (no
  writes) and prints per-tenant counts + total.
- `D-RE-5` — **ReencryptReport shape in `internal/core`** (pure):
  `{dryRun bool, totalLegacy int, perTenant []{organizationId, count}}`.
- `D-RE-6` — **No schema/TS/golden change** (storage-layer concern; rationale
  from the parent change).
- `D-RE-7` — **Additive immutability-trigger refinement (v15→v16).** The v15
  `observations_immutable_content` trigger would reject the redaction UPDATE
  (`what/why/where_text/learned = ''`) that re-encryption performs. Instead of
  weakening immutability globally, schema version advances 15→16 via
  `migrateV15ToV16` (internal/store/migration_v16.go): validate the v15 trigger
  exists (missing = corruption signal, abort), drop it, recreate it permitting
  ONLY the exact legacy-plaintext → encrypted-at-rest transition (content_algo
  '' → non-empty, four plaintext columns emptied, every other immutable column
  byte-identical), and set schema_version=16 last — any failure rolls back to
  15. No column/layout change, no backfill.

## File placement

| File | Purpose |
| --- | --- |
| `internal/core/reencrypt.go` | pure `ReencryptReport` shape |
| `internal/store/crypto.go` | `ReencryptLegacyContent` + `LegacyEncryptionCounts` (dry-run read) |
| `internal/store/migration_v16.go` | additive v15→v16 immutability-trigger refinement (D-RE-7) |
| `cmd/drenyra-engram/encrypt.go` | `cmdEncrypt` (dry-run/apply) |
| `cmd/drenyra-engram/main.go` | dispatch `case "encrypt"` + usage line |
| `internal/store/crypto_test.go` | store-level re-encryption tests (dry-run zero-write, apply + hashes, institutional untouched, fail-closed) |
| `cmd/drenyra-engram/encrypt_test.go` | CLI tests (dry-run report, apply, no-key fail, usage errors) |
| `cmd/drenyra-engram/no_override_surface_test.go` | `cliDispatchPaths` entry |
| `README.md`, `DOCS.md`, `ROADMAP.md`, `CHANGELOG.md` | docs_as_code |

## Behavior matrix

| Store | Command | Result |
| --- | --- | --- |
| key set, legacy rows | `encrypt` (default dry-run) | per-tenant report, ZERO writes |
| key set, legacy rows | `encrypt --apply` | all legacy company rows encrypted; hashes unchanged; idempotent on rerun |
| no key | `encrypt` | `ENCRYPTION_REQUIRED` (fail closed) |
| key set | `encrypt --dry-run --apply` | usage error 2 |
| key set | `encrypt` on all-encrypted store | `totalLegacy: 0`, exit 0 (idempotent) |

## Verification gates

- `go test ./internal/store ./cmd/drenyra-engram -count=1` then `go test ./...
  -count=1`; `npm test`; `npm run typecheck`; `go vet ./...`; `gofmt -l .`;
  `TestGoldenVectorsGo`.
- Reviewer greps: no schema change, no new error code, no money fields, no
  authorization semantics, diff limited to the re-encryption surface.
