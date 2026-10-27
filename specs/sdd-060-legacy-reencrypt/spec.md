# Spec — sdd-060-legacy-reencrypt

> Phase: spec · Artifact: spec · Status: draft
> Inputs: proposal. Frozen bindings: fail-closed, scope_first, no money fields,
> strict TDD for apply, docs_as_code.

## Functional requirements

### FR-RE-1 — command shape

`drenyra-engram encrypt [--dry-run | --apply] [--db <path>]`

- `--dry-run` and `--apply` mutually exclusive (both → usage error 2).
- NEITHER set → default `--dry-run` (ZERO writes).
- Exit codes: 0 ok · 1 runtime/store error · 2 usage error.

### FR-RE-2 — re-encryption scope

- Target rows: company-scope observations (`scope_kind = 'company'`) whose
  `content_algo = ''` (legacy plaintext). Institutional and non-company scopes
  are NEVER touched (structural data stays plaintext — same boundary as new
  writes).
- Already-encrypted rows (`content_algo != ''`) are skipped (idempotent).
- Re-encryption uses the store's configured master key + the row's tenant
  (organization_id) derived key — identical envelope semantics to new writes
  (`aes-256-gcm-v1`, fresh nonce).

### FR-RE-3 — fail closed

- A store opened WITHOUT the master key cannot re-encrypt: the command fails
  with `ENCRYPTION_REQUIRED` (never a silent plaintext continuation).
- `--apply` runs in ONE transaction: any failure aborts with NO partial
  commit (never a half-re-encrypted store); the failing row is named in the
  error.
- Malformed key material fails at store open (`INVALID_ENCRYPTION_KEY`, same as
  the parent change).

### FR-RE-4 — integrity invariants

- The re-encrypted memory is byte-identical to the pre-re-encryption decrypted
  view: `content_hash`, `identity_hash` and `envelope_hash` are UNCHANGED by
  the operation (they are content hashes of the decrypted memory, not of the
  at-rest envelope).
- Receipts, relations, evidence/rule links and the transition log are untouched.
- `--dry-run` report: total legacy rows + per-tenant counts, no mutation
  (store snapshot identical).

### FR-RE-5 — constraints

- No money fields; no authorization semantics; no new error codes beyond the
  frozen set (ENCRYPTION_REQUIRED, INVALID_ENCRYPTION_KEY already exist).
- No TS/golden change (storage-layer concern, documented rationale).

### FR-RE-6 — immutability-trigger refinement (v15→v16, D-RE-7)

- The v15 `observations_immutable_content` trigger blocks ANY update of the
  immutable columns, which would reject the plaintext-redaction UPDATE
  (`what/why/where_text/learned = ''`) that `--apply` needs. Schema version
  advances 15→16 via an ADDITIVE migration that validates the v15 trigger
  exists (corruption signal if absent), drops it, and recreates it permitting
  EXACTLY ONE content transition: `content_algo` passes from `''` to non-empty
  with the four plaintext content columns emptied and every other immutable
  column byte-identical. Any other UPDATE touching the immutable columns
  aborts exactly as before.
- The migration commits only after the trigger is verified; failure rolls back
  to schema_version=15 (fail closed, no partial migration).

## Acceptance criteria

- AC-RE-1: `--dry-run` reports the correct legacy counts (per tenant) and
  performs ZERO writes (superseded/snapshot equality, ciphertext count
  unchanged).
- AC-RE-2: `--apply` re-encrypts every legacy company-scope row; raw SQL shows
  non-empty `content_cipher` + `content_algo = 'aes-256-gcm-v1'`; plaintext
  columns redacted.
- AC-RE-3: hashes unchanged — content/identity/envelope identical before/after
  `--apply` for every re-encrypted row.
- AC-RE-4: institutional rows and already-encrypted rows untouched.
- AC-RE-5: store without key → `ENCRYPTION_REQUIRED`; `--dry-run`+`--apply` →
  usage error 2.
- AC-RE-6: after re-encryption, a store WITHOUT the key fails reads
  (ENCRYPTION_REQUIRED) — the legacy plaintext gap is closed.
- AC-RE-7: full gates green (`go test ./... -count=1`, npm test, typecheck,
  vet, gofmt, golden); usage + README/DOCS/ROADMAP + CHANGELOG updated.

## Out of scope

Cloud/KMS/HSM; TS/golden; hash/receipt/relation changes; institutional-scope
re-encryption. The only schema-version advance allowed is the additive
15→16 immutability-trigger refinement of FR-RE-6 — no column/layout change,
no backfill, no rewrite.
