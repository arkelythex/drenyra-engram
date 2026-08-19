# Design — sdd-060-at-rest-encryption

> Phase: design · Artifact: design · Status: draft
> Inputs: frozen spec. Delivery slices PR A (encryption core + schema v15) →
> PR B (sync guard + CLI env + docs), stacked-to-main, ask-on-risk.

## Decisions

- `D-ENC-1` — **Storage-layer crypto in `internal/store/crypto.go`.** The AEAD
  envelope and HKDF derivation are storage implementation details: the memory
  MODEL and all frozen contracts are unchanged (encrypted rows decrypt to
  byte-identical AccountingMemory). Golden parity does NOT apply (no shared
  Go↔TS semantic change — documented in spec FR-ENC-6).
- `D-ENC-2` — **Per-tenant KEK via HKDF-SHA256.** `kek = HKDF-SHA256(master,
  salt=organization_id, info="drenyra-engram/at-rest/v1")`. One derivation per
  row operation (cheap); no KEK persistence; separable keys (right-to-delete).
  Salt = organization_id (the tenant); a company under the same tenant shares
  the tenant key (tenant-level separation, per SDD-060 §5).
- `D-ENC-3` — **AES-256-GCM with a fresh random nonce per write.** Nonce (12B)
  - ciphertext stored on the row; GCM authentication failure on a wrong key is
  the `DECRYPTION_FAILED` signal (fail closed, never partial).
- `D-ENC-4` — **Schema v15 additive migration.** `migration_v15.go`: three
  nullable/default columns on observations (`content_cipher BLOB`,
  `content_nonce BLOB`, `content_algo TEXT NOT NULL DEFAULT ''`); `schemaVersion`
  → 15; the existing migration ladder + schema guard discipline unchanged.
  Legacy rows (`content_algo=''`) are plaintext forever (no re-encryption in
  this change).
- `D-ENC-5` — **Store option via `OpenWithOptions`.** `type Options struct {
  EncryptionKey []byte }`; `Open(path, signers...)` delegates with nil (default
  OFF — existing callsites/tests/fixtures unchanged). The store keeps
  `encMaster []byte` (nil = disabled). Invalid key length (≠32) fails open
  closed with `INVALID_ENCRYPTION_KEY`.
- `D-ENC-6` — **Encrypt in Save, decrypt in scanMemory.** Save: when enabled,
  serialize the 4 content fields into the canonical content shape, encrypt into
  content_cipher/nonce/algo, write "" into the plaintext columns. Read
  (`scanMemory`): when content_algo ≠ '' → require encMaster (else
  `ENCRYPTION_REQUIRED`), derive the tenant key, decrypt (GCM fail →
  `DECRYPTION_FAILED`), populate content. Both paths preserve the byte-identical
  decrypted shape (FR-ENC-2).
- `D-ENC-7` — **Sync guard via optional interface assertion.** `sync.Sync`
  checks `interface{ EncryptionEnabled() bool }` on the source and sink: source
  enabled && sink not enabled → `SYNC_ENCRYPTION_MISMATCH` (fail closed, no
  copy). Both enabled → transparent (Source/Sink already carry decrypted
  memories; the target re-encrypts with ITS own KEKs on write). Backward
  compatible: stores without the method (non-SQLite sources) skip the guard.
- `D-ENC-8` — **CLI env wiring in `openStore`.** `DRENYRA_ENCRYPTION_MASTER_KEY`
  (hex or base64, decoded to 32 bytes; malformed → fail closed). One shared
  helper → every CLI command incl. `sync` inherits it. No new flag/command.
- `D-ENC-9` — **No cross-RUC weakening.** Encryption is per-observation; the
  scope-first isolation invariants are untouched (encryption never changes
  visibility). Cross-tenant matrix stays green.

## File placement

| File | Purpose |
| --- | --- |
| `internal/store/crypto.go` | `deriveTenantKey` + `encryptContent` + `decryptContent` (AES-256-GCM + HKDF) |
| `internal/store/store.go` | `Options`/`OpenWithOptions`/`encMaster`; Save encrypt path; `scanMemory` decrypt path; `EncryptionEnabled()`; `schemaVersion` 15 |
| `internal/store/migration_v15.go` | additive v15 migration (3 columns) |
| `internal/store/migration_v15_test.go` | v14→v15 additive upgrade test |
| `internal/store/crypto_test.go` | roundtrip, fail-closed (no/wrong key), per-tenant separation, legacy rows |
| `internal/sync/sync.go` | encryption-mismatch guard |
| `internal/sync/sync_test.go` | guard: encrypted→plaintext fails; encrypted→encrypted succeeds |
| `cmd/drenyra-engram/main.go` | `openStore` env wiring |
| `README.md`, `DOCS.md`, `ROADMAP.md` | docs_as_code |

## Behavior matrix

| Store (read) | Row | Result |
| --- | --- | --- |
| key set | encrypted | decrypted byte-identical (FR-ENC-2) |
| key set | legacy plaintext | plaintext as-is |
| no key | encrypted | `ENCRYPTION_REQUIRED` (fail closed) |
| wrong key | encrypted | `DECRYPTION_FAILED` (GCM) |
| key set, tenant A | tenant B row | B's KEK fails GCM (`DECRYPTION_FAILED`) — separation |

| Sync | Source | Sink | Result |
| --- | --- | --- | --- |
| both plaintext | off | off | unchanged (pre-change) |
| source encrypted | on | off | `SYNC_ENCRYPTION_MISMATCH` (no copy) |
| both encrypted | on | on | success; target rows encrypted |

## Verification gates per PR

- PR A: `go test ./internal/store` (incl. migration + crypto suites) + full
  `go test ./...`; cross-tenant matrix green (encryption never weakens
  isolation); schema guard green.
- PR B: `go test ./internal/sync ./cmd/drenyra-engram` + full gates; usage/docs
  updated.
- Full chain gate per boundary: `npm run typecheck` → `go vet ./...` →
  `gofmt -l .` → `go test ./... -count=1` → `npm test`.
