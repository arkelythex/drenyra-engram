# Verify Report — sdd-060-at-rest-encryption

> Phase: verify · Artifact: verify-report · Status: PASS
> Inputs: spec + tasks + apply-progress. Final whole-change gate run FRESH
> (`-count=1`) after the last edit.

## Result: PASS — all acceptance criteria verified green

| Criterion | Evidence |
| --- | --- |
| AC-ENC-1 encryption roundtrip | `TestEncryptionRoundtrip` — byte-identical read; raw SQL shows ciphertext + redacted plaintext columns + algo marker |
| AC-ENC-2 fail-closed reads | `TestEncryptionFailClosed` — no key → `ENCRYPTION_REQUIRED`; wrong key → `DECRYPTION_FAILED`; never partial |
| AC-ENC-3 per-tenant separation | `TestEncryptionTenantSeparation` — tenant B's derived key fails GCM on tenant A's row |
| AC-ENC-4 legacy compatibility | `TestEncryptionLegacyRows` — plaintext rows readable in both modes; default OFF (full suite unchanged semantics) |
| AC-ENC-5 schema v15 | `TestMigrationV15Additive` + the full migration suite (bootstrap + direct-upgrade matrix) updated and green |
| AC-ENC-6 sync guard | `TestSyncEncryptionMismatch` (nothing copied) + `TestSyncEncryptionBothEnabled` (target re-encrypted — reopen-without-key proof) |
| AC-ENC-7 gates + docs | `go test ./... -count=1` 10/10 · `npm test` 386/386 · typecheck · vet · gofmt · `TestGoldenVectorsGo`; README/DOCS/ROADMAP updated |

## Constraints honored

- No frozen contract change (contracts/ untouched); memory model unchanged.
- No money fields; encryption never authorizes; no new CLI surface (env-only).
- Scope-first isolation untouched: cross-tenant matrix green (encryption never
  changes visibility).
- Golden parity deliberately not applied (storage-layer concern, spec FR-ENC-6).
- Additive migration discipline: v14→v15 nullable columns, corruption-signal
  checks, single transaction, schema guard green.

## Gate results (final, fresh)

- `go test ./... -count=1` — 10/10 packages ok.
- `npm test` — 386/386 · `npm run typecheck` — clean.
- `go vet ./...` — clean · `gofmt -l .` — clean.
- `go test ./internal/core -run TestGoldenVectorsGo -count=1` — ok.

## Notes

- One regression was caught and fixed within the slice: an early draft of the
  CLI open dropped the WORM objectsRoot (OpenWithOptions) — `--objects` parity
  broke; fixed via `OpenWithObjectsAndOptions` and re-verified.
- pi-lens runner diagnostics were stale throughout (replayed the RED-state
  snapshot with old line numbers — refuted by the compiler: the full fresh
  suite passes). Documented here and in the tenant-cli verify-report.
