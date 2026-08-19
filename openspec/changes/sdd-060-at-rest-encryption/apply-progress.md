# Apply Progress — sdd-060-at-rest-encryption

> Phase: apply · Artifact: apply-progress · Status: done
> Inputs: spec + design + tasks. Strict TDD per slice; ALL gates green at the
> final boundary (`go test ./... -count=1` 10/10, `npm test` 386/386, typecheck,
> vet, gofmt, `TestGoldenVectorsGo`).

## Slice A — encryption core + schema v15 (FR-ENC-1/2/3, AC-ENC-1/2/3/4/5) — DONE

- `internal/store/crypto.go` (new): `deriveTenantKey` (HKDF-SHA256,
  `crypto/hkdf` stdlib), `encryptContent`/`decryptContent` (AES-256-GCM, random
  12B nonce, algo `aes-256-gcm-v1`), `contentEnvelope` (canonical
  {what,why,where,learned}), `errEncryptionRequired`, shared
  `encryptContentForWrite` seam, `EncryptionEnabled()`.
- `internal/store/migration_v15.go` (new): additive v14→v15 (3 nullable columns
  on observations; corruption-signal checks; single transaction; schema_meta →
  15). `schemaVersion` → 15.
- `internal/store/store.go`: `Options{EncryptionKey}` + `OpenWithOptions` +
  `OpenWithObjectsAndOptions` (objectsRoot + key — the CLI seam); `encMaster`
  on the store; Save encrypt path (company scope, per-tenant key, plaintext
  columns redacted); `scanMemory(rs, encMaster)` decrypt path (algo ≠ '' →
  require key → `ENCRYPTION_REQUIRED` / GCM fail → `DECRYPTION_FAILED`); legacy
  rows pass through; `refreshEnvelopeCache` method-ized (needs the key);
  ImportObservation re-encrypts on the target (synced content never lands
  plaintext).
- `internal/store/crypto_test.go` (new): roundtrip byte-identity + raw-column
  proof; fail-closed (no key → ENCRYPTION_REQUIRED; wrong key →
  DECRYPTION_FAILED); per-tenant separation (B's key fails GCM on A's row);
  legacy rows readable in both modes; v15 additive migration test.
- Migration test expectations updated 14 → 15 (bootstrap/direct-upgrade suites).

## Slice B — sync guard + CLI env + docs (FR-ENC-4/5, AC-ENC-6/7) — DONE

- `internal/sync/sync.go`: optional-interface guard — source `EncryptionEnabled()`
  && sink not → `SYNC_ENCRYPTION_MISMATCH` before ANY copy (fail closed).
  Both enabled → transparent (source decrypts, target re-encrypts via
  ImportObservation).
- `internal/sync/sync_test.go`: mismatch (nothing copied) + both-enabled
  (target rows encrypted — proven by reopen-without-key ENCRYPTION_REQUIRED).
- `cmd/drenyra-engram/main.go`: `openStoreWithRoot` reads
  `DRENYRA_ENCRYPTION_MASTER_KEY` (hex/base64 32B, malformed →
  INVALID_ENCRYPTION_KEY) and opens via `OpenWithObjectsAndOptions` (objectsRoot
  preserved — an early draft dropped it and broke `--objects` parity; caught by
  the suite).
- `cmd/drenyra-engram/tenant_test.go`: `TestCLIEncryptionEnvWiring` — save with
  key encrypts; context without key → ENCRYPTION_REQUIRED; malformed key fails.
- Docs: README/DOCS/ROADMAP encryption sections.

## Deviation notes

- The objectsRoot regression (OpenWithOptions dropping the WORM root) was caught
  and fixed within the slice (OpenWithObjectsAndOptions).
- Golden parity deliberately NOT applied: encryption is a storage-layer concern
  (spec FR-ENC-6); no shared Go↔TS semantic change.
