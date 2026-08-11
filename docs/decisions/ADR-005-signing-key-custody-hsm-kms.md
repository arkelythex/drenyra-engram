# ADR-005 — Signing-key custody: local file keyring for v1, cloud KMS as the production upgrade path

> Status: accepted · Applied: v1 gate G-7 (key-compromise response, audit O) · Updated: 2026-08-11

## Context

The engine signs every immutable act with an Ed25519 keypair whose private seed
lives in a user-only keyring file (`0600` file, `0700` directory), never in the
database. Threat T-9 (compromised signing key) rates this custody model
**Medium**: a host-level attacker who can read the keyring can sign claimed
acts. The compromise-response playbook
(`docs/security/key-compromise-response.md`) and the FZ-3 cutoff semantics
mitigate the *consequences* (one-way revocation, never signs again,
fail-closed verification), but they do not mitigate the *exposure*: the seed is
present, in plaintext, on the signing host.

This ADR records the v1 decision on where the private seed lives, and the
explicit conditions under which custody moves to a hardware security module
(HSM) or cloud key-management service (KMS). It is the documented answer to
audit block O's "HSM/KMS decision" and closes the T-9 residual named in
`docs/security/evidence-lifecycle-and-threat-model.md`.

## Decision

**For v1 (local-first, single-operator deployments), keep the private seed in
the user-owned file keyring.** The keyring's security properties are the
host's access control (`0600`/`0700`), and the engine's rotation/revocation
seam is the fail-closed response when that boundary is breached.

**Treat cloud KMS / HSM as the production upgrade path, selected only when the
deployment meets any of these conditions:**

1. **Multi-operator signing** — more than one operator or machine may sign in
   the same tenant scope, so the seed would have to be shared or replicated.
2. **Regulated/high-value fiscal signing** — a client or jurisdiction requires
   key custody with independent audit attestation (for example, a tax-filing
   regime that demands hardware-backed signatures or a documented key-escrow
   authority).
3. **Cloud-managed deployment** — the store runs on a cloud instance where the
   platform's KMS integration is the least-friction way to satisfy condition 2
   (AWS KMS / GCP Cloud KMS / Azure Key Vault, or a Sigstore/Rekor-style
   managed identity flow).
4. **Audited multi-tenant SaaS** — the operator sells Drenyra Engram as a
   hosted service and wants per-tenant key separation enforced by the platform.

### Why not HSM/KMS for v1

- **The threat model is local-first.** The v1 deployment is a single-operator
  engine on a host the operator controls. The T-9 boundary is "a host-level
  attacker who can read the keyring" — the same attacker, on the same host,
  also has the store and the operator's session. An HSM on the *same host*
  reduces seed exposure but does not change the effective trust boundary; the
  compromise response (rotate + revoke + fail-closed) does.
- **The contract is keyring-shaped.** The signing seam
  (`internal/receipts/signer.go`) reads a keyring file. Moving to a remote KMS
  changes the signing call from a local Ed25519 sign to a network round-trip
  per receipt: latency, availability, and offline-signing semantics all change.
  That is a contract-level change (frozen-contracts rule), not a config flag.
- **Local verification must keep working offline.** The engine's whole value
  proposition includes offline verification over a local store. If signing
  were KMS-bound, offline *signing* would break; a dual-path design (KMS for
  signing, local keys only for verification) doubles the key-management
  surface for zero v1 benefit.

### The v1 custody contract (unchanged, now explicit)

- The private seed lives only in the user-owned keyring file; it is NEVER
  stored in the database, in receipts, in logs, or in any export.
- `keys init` generates from OS entropy; `keys rotate` activates a fresh
  independent keypair and revokes the old one in one transaction.
- A revoked key NEVER signs new receipts (store/signing seam, fail closed).
- The keyring file is `0600` (dir `0700`); any other permission mode fails
  closed on open.
- The compromise-response playbook is the deterministic operator response;
  the DRILL test (`TestCLICompromiseResponseDrill`) exercises
  suspend → rotate → revoke → verify fail-closed as one CLI journey.

## Consequences

- **Accepted risk:** a host-level attacker who reads the keyring can sign
  claimed acts until revocation is published. This matches the v1 threat-model
  verdict (Medium) and is bounded by the one-way revocation + FZ-3 cutoff.
- **Upgrade path is a contract change.** Moving to KMS/HSM requires a new
  signing-key contract version (the seam must speak to a key service), a
  migration path for existing keyrings, and explicit approval — it cannot
  silently swap the custody model behind the existing contract.
- **No key escrow in v1.** The engine does not back up seeds. Losing the
  keyring means generating a new keypair and re-signing where policy permits
  (Step 7 of the playbook); pre-cutoff artifacts remain verifiable.
- **Cloud KMS decision factors are documented** (above), so the operator can
  select the upgrade without re-litigating the trade-off.

## Reference

- `docs/security/key-compromise-response.md` — the operator playbook this
  decision complements (G-7)
- `docs/security/evidence-lifecycle-and-threat-model.md` — T-9 and audit O
- `contracts/receipts.md` — frozen receipt contract (keyId, chain, signature)
- `contracts/verification.md` — FZ-3 signing-key validity (frozen)
- `internal/receipts/signer.go` — the signing seam (local keyring)
- `cmd/drenyra-engram/compromise_drill_test.go` — the G-7 DRILL journey
- NIST SP 800-57 — key-management guidance the playbook follows
