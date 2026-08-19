# Tasks — sdd-060-at-rest-encryption

> Phase: tasks · Artifact: tasks · Status: draft
> Inputs: spec + design (required). Strict TDD per `phases.apply.strict_tdd: true` —
> RED → GREEN per slice; `go test ./... -count=1` and `npm test` stay green at
> every slice boundary.
> Delivery: `ask-on-risk` / `stacked-to-main` (repo default), review clone-off —
> ordinary-policy commits.

## Review Workload Forecast

| Field | Value |
| --- | --- |
| Estimated changed lines | ≈ 400–500 (crypto + migration + store integration + sync guard + tests + docs) |
| 400-line budget risk | Medium — chained PRs (A encryption core → B sync guard + docs) |
| Chained PRs recommended | Yes |
| Chain strategy | stacked-to-main |

---

## PR A — encryption core + schema v15 (FR-ENC-1/2/3, AC-ENC-1/2/3/4/5)

- [x] A.1 RED — `internal/store/crypto_test.go`: `TestEncryptionRoundtrip` —
  `OpenWithOptions` with a key; Save → read returns byte-identical content;
  raw SQL shows content_cipher non-empty + plaintext columns "". RED until the
  option/migration exist. <!-- sdd-owner: implementation -->
- [x] A.2 GREEN — `internal/store/crypto.go` (`deriveTenantKey` HKDF-SHA256,
  `encryptContent`/`decryptContent` AES-256-GCM, nonce 12B, algo
  `aes-256-gcm-v1`) + `Options`/`OpenWithOptions`/`encMaster`/`schemaVersion` 15
  - `migration_v15.go` (3 additive columns). <!-- sdd-owner: implementation -->
- [x] A.3 RED — `TestEncryptionFailClosed`: store WITHOUT key reading an
  encrypted row → `ENCRYPTION_REQUIRED`; WRONG key → `DECRYPTION_FAILED`;
  never partial content. <!-- sdd-owner: implementation -->
- [x] A.4 GREEN — Save encrypt path (content → ciphertext, plaintext columns
  "") + `scanMemory` decrypt path (algo ≠ '' → require key → derive → decrypt;
  GCM fail → `DECRYPTION_FAILED`). Legacy rows (`algo=''`) plaintext in both
  modes. <!-- sdd-owner: implementation -->
- [x] A.5 RED — `TestEncryptionTenantSeparation`: tenant A's ciphertext fails
  GCM under tenant B's derived key; plus `TestMigrationV15Additive` (v14 DB
  upgrades in place, schema guard green) and cross-tenant matrix still green.
  <!-- sdd-owner: implementation -->
- [x] A.6 TRIANGULATE — greps: no frozen contract change, no money fields, no
  authorization semantics, encryption never changes scope-first visibility;
  full gate (typecheck → vet → gofmt → `go test ./... -count=1` → npm test)
  green. <!-- sdd-owner: implementation -->

**Gate PR A:** `go test ./internal/store -count=1` (crypto + migration + full)
then `go test ./... -count=1`; cross-tenant matrix + schema guard green.

---

## PR B — sync guard + CLI env + docs (FR-ENC-4/5, AC-ENC-6/7)

- [x] B.1 RED — `internal/sync/sync_test.go`: source encrypted + sink plaintext
  → `SYNC_ENCRYPTION_MISMATCH` + NO rows copied; both encrypted → success and
  target rows encrypted. RED until the guard exists. <!-- sdd-owner: implementation -->
- [x] B.2 GREEN — `sync.Sync` optional-interface guard (D-ENC-7): source
  `EncryptionEnabled()` && sink not → fail closed; both enabled → transparent.
  <!-- sdd-owner: implementation -->
- [x] B.3 RED — `cmd/drenyra-engram` test: `DRENYRA_ENCRYPTION_MASTER_KEY` env
  wired through `openStore` (save with env → row encrypted; read without env →
  `ENCRYPTION_REQUIRED`; malformed key → store open fails). <!-- sdd-owner: implementation -->
- [x] B.4 GREEN — `openStore` env wiring (hex/base64 32B decode, fail closed on
  malformed); `sync` inherits it. <!-- sdd-owner: implementation -->
- [x] B.5 TRIANGULATE + docs — reviewer greps (no contract/surface change, no
  new error code beyond the frozen set, sync diff limited to the guard);
  docs_as_code: README/DOCS/ROADMAP encryption section + usage note; final
  gate fresh. <!-- sdd-owner: implementation -->

**Gate PR B (final):** `go test ./internal/sync ./cmd/drenyra-engram -count=1`
then full `go test ./... -count=1`; `npm test`; typecheck; vet; gofmt;
`TestGoldenVectorsGo`.

---

## Cross-cutting checklist

- [x] Conventional commit per atomic milestone; no AI attribution.
- [x] Strict TDD per slice: named failing tests land RED before their
  implementation.
- [x] Fail-closed discipline: no key / wrong key / sync mismatch NEVER leak
  plaintext; partial decrypt is impossible (GCM).
- [x] Scope-first isolation untouched: cross-tenant matrix green; encryption
  never changes visibility.
- [x] No money fields, no `any`, no contract change, no authorization semantics.
- [x] Legacy compatibility: default OFF; plaintext rows readable in both modes.

## Definition of done

- [x] All tasks checked; every AC-ENC-1…7 verified green by its mapped test.
- [x] Full gates per config `verify_order` fresh (`-count=1`) at every PR
  boundary, plus `TestGoldenVectorsGo`.
- [x] Chain merged to main via stacked-to-main; delivery recorded before apply.
