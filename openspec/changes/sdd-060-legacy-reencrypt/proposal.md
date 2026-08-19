# Proposal — sdd-060-legacy-reencrypt

> Phase: propose · Artifact: proposal · Status: draft
> Inputs: sdd-060-at-rest-encryption (delivered — opt-in at-rest encryption),
> user decision to continue.

## Problem

The at-rest encryption change (schema v15) encrypts NEW company-scope writes;
**legacy rows** written before encryption was enabled keep `content_algo = ''`
and remain PLAINTEXT forever (documented non-goal of that change). An operator
who enables `DRENYRA_ENCRYPTION_MASTER_KEY` on an existing store has no way to
close that gap: institutional memory written pre-encryption stays readable in
plaintext at rest.

## Proposed capability

`drenyra-engram encrypt [--dry-run | --apply] [--db <path>]` — re-encrypts the
legacy plaintext content of company-scope observations under the configured
master key (per-tenant derived keys, exactly like new writes):

- **`--dry-run` default (ZERO writes)**: reports how many rows would be
  re-encrypted (company-scope, `content_algo = ''`), grouped per tenant.
- **`--apply`**: re-encrypts each legacy row in ONE transaction (fail closed —
  any failure aborts with no partial commit); idempotent (already-encrypted
  rows skipped); the decrypted content is byte-identical so content/identity/
  envelope hashes, receipts, relations and the transition log are untouched.
- **Fail-closed**: requires `DRENYRA_ENCRYPTION_MASTER_KEY` (a store opened
  without it → `ENCRYPTION_REQUIRED`); institutional/non-company scopes are
  never touched (structural data stays plaintext).

## Non-goals

- No schema change (v15 stays).
- No hash/receipt/relation changes (the decrypted memory is byte-identical).
- No cloud/KMS/HSM (unchanged from prior changes).
- No TS/golden changes (storage-layer concern, same rationale as the parent
  change).

## Delivery

Store method + CLI + tests + docs. Estimated ≈ 200–280 lines (well under the
400-line rule — no chained PR needed). Review clone-off (user decision) —
ordinary-policy commits.

## Risks

- Batch failure: mitigated by single-transaction atomicity (no partial
  re-encryption) + per-row GCM construction (a corrupt legacy row fails the
  whole operation with a clear error — never a silently skipped row).
- Hash drift: impossible by construction (the envelope hashes are content
  hashes of the DECRYPTED memory, which is byte-identical); guarded by a test.
