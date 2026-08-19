# Spec — sdd-060-at-rest-encryption

> Phase: spec · Artifact: spec · Status: draft
> Inputs: proposal. Frozen bindings: scope_first, fail-closed,
> non_authorization_boundary, docs_as_code, strict TDD for apply, schema v14
> additive-migration discipline.

## Functional requirements

### FR-ENC-1 — at-rest encryption (opt-in, per tenant, separable keys)

- `store.OpenWithOptions(path, Options{EncryptionKey []byte})` — a store opened
  with a 32-byte master key is encryption-ENABLED; `Open` (no key) is
  encryption-DISABLED (default; existing deployments/fixtures unchanged).
- Per-tenant derived key: `KEK(tenant) = HKDF-SHA256(master, salt=organization_id,
  info="drenyra-engram/at-rest/v1")` — 32 bytes. Keys are separable: each
  tenant's key material is independent of every other tenant's (beyond the
  master); discarding a tenant's salt/derived key makes that tenant's encrypted
  content unrecoverable (right-to-delete posture).
- AEAD: AES-256-GCM (stdlib); one fresh random 12-byte nonce per write.
- Encrypted fields: the observation CONTENT narrative — `what`, `why`,
  `where_text`, `learned`. Plaintext columns hold "" for encrypted rows.
- NOT encrypted (structural, always plaintext): topic_key, title, kind, scope
  tuple (scope-first search and indexing require them), timestamps, status,
  fiscal_effect, hashes, receipts.
- Envelope stored on the observation row (schema v15, additive): `content_cipher`
  BLOB, `content_nonce` BLOB, `content_algo` TEXT (`aes-256-gcm-v1`).
- Legacy plaintext rows (content_algo = '') stay readable in both modes.

### FR-ENC-2 — fail-closed reads

- An encryption-enabled store reads both encrypted and legacy rows.
- A store WITHOUT the key reading an encrypted row fails with
  `ENCRYPTION_REQUIRED` — never redacted, never partial, never plaintext leak.
- A store WITH the wrong key fails with `DECRYPTION_FAILED` (GCM authentication
  failure) — never a partial decrypt.
- Encryption never changes any success-path contract of the memory model: a
  decrypted row is byte-identical to what would have been stored plaintext.

### FR-ENC-3 — schema v15 (additive migration)

- `ALTER TABLE observations ADD COLUMN content_cipher BLOB`, `content_nonce
  BLOB`, `content_algo TEXT NOT NULL DEFAULT ''` — additive, nullable, no data
  rewrite. `schemaVersion` → 15. Migration guard: v14 DBs upgrade in place; a
  store at any unsupported version fails closed (existing discipline).

### FR-ENC-4 — sync encryption guard

- `sync.Sync(from, to, opts)` fails closed with `SYNC_ENCRYPTION_MISMATCH` when
  the source store is encryption-enabled but the target is NOT (institutional
  memory must never land in a plaintext store). Both enabled → transparent
  (source decrypts on read, target encrypts on write — the existing
  Source/Sink interfaces already carry decrypted memories).
- Source enabled + target enabled with DIFFERENT master keys → each store
  re-encrypts with its own per-tenant KEKs; no cross-key requirement (each
  store owns its key material; GCM per-row nonces keep the envelopes valid).

### FR-ENC-5 — CLI wiring

- `openStore` (shared CLI helper) reads `DRENYRA_ENCRYPTION_MASTER_KEY`
  (hex or base64, 32 bytes) and passes it to `OpenWithOptions`. Invalid key
  material → store open fails closed (exit 1, clear message).
- `drenyra-engram sync` inherits the env wiring + the mismatch guard.
- No new command, no new flag, no contract surface change.

### FR-ENC-6 — constraints

- No money fields (repo convention); monetary values remain int64 cents
  plaintext (closing totals etc. are scope data, not content narrative).
- No authorization semantics: encryption never authorizes (boundary intact).
- No change to any frozen contract (contracts/ untouched); the memory model is
  unchanged — encryption is a STORAGE-layer concern (golden parity not
  applicable — documented decision, no shared Go↔TS semantic changes).
- No `any` in TS (no TS change at all expected).

## Acceptance criteria

- AC-ENC-1: encryption roundtrip — save with key → read returns byte-identical
  content; the observations table holds ciphertext (content_cipher non-empty,
  plaintext columns "").
- AC-ENC-2: fail-closed without key — store without key reading an encrypted
  row errors `ENCRYPTION_REQUIRED`; with a wrong key errors
  `DECRYPTION_FAILED`; NEVER partial/redacted content.
- AC-ENC-3: per-tenant separation — tenant A's ciphertext does NOT decrypt
  under tenant B's derived key (GCM auth fails); cross-tenant isolation
  invariants still green.
- AC-ENC-4: legacy compatibility — plaintext rows readable in both modes;
  default OFF changes nothing (full existing suite green).
- AC-ENC-5: schema v15 — v14 DB upgrades additively; schema guard green.
- AC-ENC-6: sync guard — source encrypted + target plaintext →
  `SYNC_ENCRYPTION_MISMATCH`; both encrypted → sync succeeds and target rows are
  encrypted.
- AC-ENC-7: full gates — `go test ./... -count=1`, `npm test`, typecheck, vet,
  gofmt, `TestGoldenVectorsGo` green; usage/docs updated (docs_as_code).

## Out of scope

Legacy-row re-encryption tool; KMS/HSM master-key custody; cloud sync; contract
changes; MCP/HTTP surface changes; TS changes.
