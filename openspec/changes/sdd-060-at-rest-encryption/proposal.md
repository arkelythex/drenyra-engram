# Proposal — sdd-060-at-rest-encryption

> Phase: propose · Artifact: proposal · Status: draft
> Inputs: SDD-060 §5 (security — cifrado at-rest por tenant con llaves
> separables; sync cifrado en tránsito), repo model (SQLite store schema v14,
> local db-to-db sync), user-approved scope (opt-in, fail-closed al activar).

## Problem

SDD-060 §5 requires per-tenant at-rest encryption with separable keys (to
support eventual right-to-delete / data portability) and encrypted sync. The
store today stores observation content in PLAINTEXT columns and sync copies
db-to-db in-process. Institutional accounting memory is more sensitive than
code snippets — at-rest protection with per-tenant key separation is the Phase
3 security slice.

## Proposed capability

1. **At-rest per-tenant encryption (OPT-IN, fail-closed when enabled).**
   - Master key from `DRENYRA_ENCRYPTION_MASTER_KEY` (32-byte hex/base64). When
     the store is opened WITH it, every company-scope observation's CONTENT
     fields (what/why/where/learned — the institutional narrative) are encrypted
     at write time with a **per-tenant derived key**:
     `KEK = HKDF-SHA256(master, salt=organization_id, info="drenyra-engram/at-rest/v1")`
     (separable keys: a tenant's key material is independent; deleting it makes
     that tenant's data unrecoverable — right-to-delete).
   - AEAD: AES-256-GCM (stdlib), random 12-byte nonce per write; ciphertext +
     nonce + algorithm marker stored on the observation row (schema v15,
     additive migration — never rewrite).
   - Topic keys, titles, scopes stay PLAINTEXT (structural, indexed, needed for
     scope-first search — never encrypted).
   - **Fail-closed:** a store WITHOUT the key reading an encrypted row fails
     with `ENCRYPTION_REQUIRED` (never redacted/partial content); a WRONG key
     fails with `DECRYPTION_FAILED` (GCM auth). Legacy plaintext rows stay
     readable (backward compatible — existing deployments/fixtures unaffected,
     default OFF).
2. **Sync encryption guard (cifrado en tránsito for local db-to-db).** Sync
   reads/writes via the store, which decrypts/encrypts transparently — so an
   encrypted source syncing to an encrypted target works. When the SOURCE is
   encryption-enabled but the TARGET is not, sync FAILS CLOSED with
   `SYNC_ENCRYPTION_MISMATCH` (never copies decrypted institutional memory into
   a plaintext store).

## Non-goals (this change)

- No re-encryption of legacy rows (documented: new writes encrypted; legacy
  rows readable; a re-encryption tool is a future change).
- No cloud sync (deferred per ROADMAP non-goals).
- No KMS/HSM integration (ADR-005 covers SIGNING keys; encryption master key is
  operator-held env config, documented).
- No MCP/HTTP surface changes.
- No change to any frozen contract (contracts/ untouched); the memory model is
  unchanged — encryption is a STORAGE-layer concern, so golden parity does not
  apply (documented decision; no shared Go↔TS semantic changes).

## Delivery

Schema v15 + crypto helpers + store integration + sync guard + CLI env wiring +
tests + docs. Estimated ≈ 400–500 lines → chained PRs (encryption core → sync
guard + docs). Review clone-off (user decision) — ordinary-policy commits.

## Risks

- Migration surface: additive v15 (new nullable columns) — existing DBs upgrade
  in place; guarded by the schema-version fail-closed check.
- Key handling: the master key lives in the process env; derived per-tenant KEKs
  never persist. Wrong-key reads fail closed (GCM), never partial.
- Sync transparency: Source/Sink already carry decrypted memories; the mismatch
  guard is the only new sync logic.
