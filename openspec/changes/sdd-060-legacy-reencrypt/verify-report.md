# Verify Report — sdd-060-legacy-reencrypt

> Phase: verify · Status: done · 2026-08-19

## Scope

Full-gate verification of the `encrypt` legacy re-encryption change
(FR-RE-1…6, AC-RE-1…7) against a fresh working tree (`-count=1`).

## Evidence

| Gate | Command | Result |
| --- | --- | --- |
| Go build | `go build ./...` | PASS |
| Go unit suite | `go test ./...` (fresh) | PASS — all packages ok (cmd 18.3s, core 3.2s, search 3.6s, server 119.6s, store 75.4s, sync 4.0s) |
| Store re-encrypt tests | `go test ./internal/store -run 'Reencrypt | MigrationV15 | MigrationCrash | DirectUpgrade' -count=1` | PASS |
| CLI encrypt tests | `go test ./cmd/drenyra-engram -run TestCLIEncrypt -count=1` | PASS |
| Golden vectors | `go test ./internal/core -run TestGoldenVectorsGo -count=1` | PASS |
| TS suite | `npm test` (vitest) | PASS — 26 files, 386 tests |
| Typecheck | `npm run typecheck` (tsc --noEmit) | PASS |
| Go vet | `go vet ./...` | PASS |
| gofmt | `gofmt -l cmd internal` | clean |
| Cross-tenant adversarial | `bun test search/__tests__/scope-isolation.test.ts` | PASS — 3/3 (RUC A vs RUC B, identical content/topicKey) |

## AC mapping

| AC | Verdict | Evidence |
| --- | --- | --- |
| AC-RE-1 dry-run per-tenant counts + ZERO writes | PASS | `TestReencryptDryRunZeroWrites`, `TestCLIEncrypt/default dry-run` (key-less read still succeeds after dry-run) |
| AC-RE-2 apply re-encrypts every legacy company row; ciphertext + algo marker; plaintext redacted | PASS | `TestReencryptApplyHashesUnchanged`, `TestCLIEncrypt/apply` (ENCRYPTION_REQUIRED on key-less read after apply) |
| AC-RE-3 hashes unchanged | PASS | `TestReencryptApplyHashesUnchanged` (content/identity/envelope hashes captured before, asserted equal after) |
| AC-RE-4 institutional + already-encrypted untouched | PASS | `TestReencryptDryRunZeroWrites` (institutional excluded from counts), SELECT scoped to `scope_kind='company' AND content_algo=''` |
| AC-RE-5 no-key → ENCRYPTION_REQUIRED; `--dry-run --apply` → usage 2 | PASS | `TestReencryptFailClosed`, `TestCLIEncrypt/no-key apply` + `usage errors` |
| AC-RE-6 legacy plaintext gap closed | PASS | `TestCLIEncrypt/apply` (key-less context read fails ENCRYPTION_REQUIRED after apply) |
| AC-RE-7 full gates green + docs | PASS | table above; README/DOCS/ROADMAP/CHANGELOG updated |

## Reviewer-grep surface (FR-RE-5)

- No new error codes: only frozen `ENCRYPTION_REQUIRED` / `INVALID_ENCRYPTION_KEY` referenced.
- No money fields in any touched file.
- No authorization semantics added.
- Schema advance is the single additive trigger-only v15→v16 migration (D-RE-7), no column/layout/backfill change.

## Conclusion

PASS. All acceptance criteria verified green on a fresh run; the working tree is
deliverable as-is (ordinary-policy commits, per the change's delivery note).
