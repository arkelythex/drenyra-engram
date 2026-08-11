# Key Compromise Response — Drenyra Engram

> **Status:** operator playbook (v1 gate, G-7) · **Basis:** NIST SP 800-57
> key-compromise guidance and the FROZEN FZ-3 cutoff semantics of this change
> (`openspec/changes/v1-readiness/spec.md`).
> **Automated DRILL:** `TestCLICompromiseResponseDrill`
> (`cmd/drenyra-engram/compromise_drill_test.go`) exercises the full
> suspend → rotate → revoke → verify fail-closed journey through the real CLI.
> **Applies to:** `contracts/verification.md` (signing-key validity layer),
> `internal/core/verify.go` (`VerifySigningKeyValidity`), the one-way
> `signing_keys.revoked_at` update, and the keyring rotation surface
> (`drenyra-engram keys rotate`).
> **Related:** `docs/security/evidence-lifecycle-and-threat-model.md`,
> `contracts/verification.md`, ADR-003, `docs/architecture/ed25519-receipts-step3.md`.

This document is the deterministic operator response to a suspected signing-key
compromise. It is **not** a security audit, **not** an incident-report template,
and **not** a claim that cryptographic integrity proves accounting correctness.
The verification engine always ends every report with
`Accounting correctness: NOT ASSERTED`; this playbook inherits that non-claim.

## Purpose and non-claims

- **What this playbook is:** the exact eight-step sequence to execute when a
  private signing seed (or the machine holding it) may be compromised, aligned
  to NIST SP 800-57 key-management guidance, with the engine's actual commands
  and the exact `FZ-3` cutoff comparison it enforces.
- **What it is not:**
  - it does **not** authorize the engine to reopen writes, repair evidence, or
    self-heal — the engine **never reopens writes on its own**;
  - it does **not** make the engine a recovery authority — recovery of trust is
    the operator's decision, expressed through the authenticated one-way
    revocation + independent replacement keypair;
  - it does **not** assert that re-signed artifacts are accounting-true — re-signing
    only restores *signer provenance*, never correctness.

## Roles and prerequisites

| Role | Responsibility |
| --- | --- |
| **Operator** | Detects/suspects exposure, executes the eight steps, records the exact compromise time, runs the inventory. |
| **Signer environment owner** | Holds the keyring file (`0600`); confirms which environment(s) had seed access. |
| **Verification consumer** | Stops relying on the compromised key's outputs until the cutoff is authenticated; re-checks affected scope. |

Prerequisites:

- The keyring file path (`$DRENYRA_ENGRAM_SIGNING_KEY` or the platform config
  dir — `drenyra-engram keys show` reports the active key).
- The store path (`--db`, default `./engram.db` or `$DRENYRA_ENGRAM_DB`).
- A **clean** environment (never the suspected-compromised machine) for Step 4.

---

## The eight-step response sequence

### Step 1 — Treat the exposure as a FULL TRUST FAILURE

Any suspicion that the private seed leaked — disk copy, backup, CI cache, debugger,
screen share, exfiltration — is treated as a **full trust failure of that key**,
not as a partial or probabilistic event. NIST SP 800-57 treats a compromised
private key as invalidating the key's remaining lifetime: the key is dead from
the suspected moment forward; every signature it could have produced after that
moment is untrusted. Do not wait for proof of misuse; acting early means one
rotation, acting late means forged receipts.

### Step 2 — Stop signing and suspend verification with the compromised key

- **Stop signing is ENFORCED by the engine:** a revoked key (in the keyring or
  in the store — the store is authoritative) is never selected for a new
  signature. `internal/receipts/signer.go` fails closed:
  `"a revoked key is never selected for new signatures"`.
- **Suspend verification** has two parts:
  1. the **automatic** part: once `revoked_at` is authenticated, the FZ-3 cutoff
     makes the signing-key validity layer reject every at/after-cutoff signature
     (Step 6) — the verification surface itself enforces the suspension;
  2. the **operator** part: stop *relying on* pre-cutoff verification results
     for the affected scope until Step 7's inventory clears them. Verification
     is **read-only** and always reports; there is no "suspend" flag, and this
     playbook does not invent one.
- Freeze new writes that would depend on the compromised key's chain: route
  through the replacement key (Steps 4–5) before continuing.

### Step 3 — Preserve evidence and record the exact compromise time

- Record the **exact compromise time** — the best-known UTC instant (RFC3339) at
  which the seed may have left trusted custody — in the incident log, together
  with who reported it, how it was detected, and every environment that held the
  seed. This timestamp drives Step 7's inventory, even though the engine's
  authenticated cutoff is the rotation instant (Step 5).
- Preserve, byte-for-byte and untouched: the keyring file, the store database,
  its WAL/shm sidecars, and any evidence objects signed by the suspected key.
  The evidence trail is the audit record (`NFR-4`); never "clean up" or edit it
  during the response.
- Capture the current key state with `drenyra-engram keys show` (prints
  `keyId`, `publicKey` hex — never the seed — `createdAt`, `revokedAt`) as the
  pre-incident snapshot.

### Step 4 — Generate an INDEPENDENT replacement keypair in a clean environment

- The replacement key must be **independent**: a fresh Ed25519 keypair generated
  in a clean environment, never derived from, copied from, or related to the
  suspected seed. The engine's rotation path generates the new key from fresh
  OS entropy in the keyring file.
- If the current keyring itself may be compromised, initialize a brand-new
  keyring on the clean machine (`drenyra-engram keys init`) so no seed bytes
  from the suspected environment are carried forward.
- Verify the new key before activating it: `drenyra-engram keys show` must
  report the NEW `keyId` and a `revokedAt` of the OLD key.

### Step 5 — Publish an authenticated revocation

The only legal `signing_keys` mutation is the one-way, null-to-timestamp
`revoked_at` update (the `signing_keys_revoke_only` trigger aborts any other
shape with `IMMUTABLE_SIGNING_KEY`). Publish it through the engine:

```text
drenyra-engram keys rotate --db <path>
```

Rotation is a single DB transaction: it registers the new key, revokes the old
key, and durably activates the new key. `keys rotate` **does not backdate**: the
authenticated cutoff (`signing_keys.revoked_at`) is stamped at the rotation
moment. The **exact compromise time** (Step 3) stays in the operator's incident
log and drives the Step 7 inventory; verification enforces the engine's
authenticated `revoked_at` — the FZ-3 rule that missing or unauthenticated
cutoff information is NEVER accepted as a cutoff. Only the authenticated one-way
update is a cutoff.

### Step 6 — Fail closed for signatures at/after the cutoff

The signing-key validity layer (`core.VerifySigningKeyValidity`,
`internal/core/verify.go:328`) enforces the frozen FZ-3 comparison: when the
signing key is revoked, any signature with `issued_at >= revoked_at` is
REJECTED and only `issued_at < revoked_at` is accepted (all other layers
passing):

| Comparison | Verdict |
| --- | --- |
| `issued_at < revoked_at` | **pass** — pre-cutoff (retained under policy) |
| `issued_at == revoked_at` | **reject** — equality fails, deterministically |
| `issued_at > revoked_at` | **reject** — post-cutoff |
| `created_at > issued_at` | **reject** — key did not exist yet |
| `revoked_at` unparseable | **reject** — fail closed, **never a guess** |
| `revoked_at` empty | active-key contract (no cutoff has been published) |

Both timestamps MUST parse as RFC3339; a revoked key whose `revoked_at` cannot
be parsed is a fail-closed error, never a guess. The equal-timestamp case is
REJECTED by design: a signature issued exactly at the compromise instant cannot
be distinguished from one issued after it, so it fails closed.

**Retention policy for pre-compromise artifacts:** signatures issued strictly
before the authenticated cutoff (`issued_at < revoked_at`) remain VALID when
every other layer passes. This is the explicit policy that NIST SP 800-57
permits — pre-compromise artifacts may be retained under explicit policy — and
it is frozen in `FZ-3`. The engine never silently invalidates pre-cutoff
history; the operator re-verifies each pre-cutoff artifact during Step 7 and
decides, under policy, whether to keep it (retained) or replace it.

### Step 7 — Inventory and re-sign affected artifacts where policy permits

1. Enumerate every receipt whose `keyId` is the compromised key (query the
   store's `receipts` rows for that `key_id`).
2. Classify each by cutoff: run the read-only verifier —
   `drenyra-engram verify receipt <hash|id> --db <path>` (or
   `drenyra-engram verify memory <id> --db <path>`). A **failed** signing-key
   validity layer means the receipt is at/after the cutoff → compromised-origin,
   remove from trust. A **passed** layer means pre-cutoff → retained under the
   Step 6 policy.
3. **Re-sign where policy permits.** Re-signing is a NEW covered act by the NEW
   key — it never rewrites history (receipts have no-update/no-delete triggers)
   and never alters the payload: the canonical `payload_json` bytes are
   re-signed as-is, so monetary values stay whole int64 cents — no cent changes
   in any re-signed artifact. Policy decides which artifacts MAY be re-signed
   (for example, approval receipts backed by preserved evidence) and which MUST
   simply be re-created through the normal gated flow.
4. Record every disposition (retained / re-signed / re-created / rejected) in
   the incident log with the receipt hashes involved.

### Step 8 — Investigate adjacent systems as potentially compromised

The seed almost never travels alone. Treat as potentially compromised until
proven otherwise:

- every store/session principal authenticated by the same environment;
- the keyring's backup copies, deployment caches, CI caches, and log exports;
- evidence objects or exports signed by the compromised key;
- any remote system that received a public key or receipt from the suspected
  machine;
- the machine's local keyring store, shell history, and credential vaults.

Only after the adjacent-system investigation is closed should the incident move
from "response" to "post-incident review".

---

## Cutoff policy examples (FZ-3)

Given a receipt issued at `2026-08-05T13:00:00Z` and an authenticated
`revoked_at`:

| `revoked_at` | Comparison | Verification result |
| --- | --- | --- |
| `2026-08-05T14:00:00Z` | `issued_at < revoked_at` | passed (pre-cutoff, retained) |
| `2026-08-05T13:00:00Z` | `issued_at == revoked_at` | **failed** (at the cutoff) |
| `2026-08-05T12:00:00Z` | `issued_at > revoked_at` | **failed** (after the cutoff) |
| `not-a-timestamp` | unparseable | **failed** (fail closed, never a guess) |
| *(empty)* | no cutoff published | active-key contract |

## Command / evidence checklist

| Action | Command | Evidence it produces |
| --- | --- | --- |
| Pre-incident snapshot | `drenyra-engram keys show` | active `keyId`, hex `publicKey` (never the seed), `createdAt`, `revokedAt` |
| Rotate + revoke (one tx) | `drenyra-engram keys rotate --db <path>` | new active key; old `revoked_at` stamped in store + keyring |
| Post-rotation check | `drenyra-engram keys show` | new `keyId`, old key's `revokedAt` recorded |
| Classify an artifact | `drenyra-engram verify receipt <hash|id> --db <path>` | full report; signing-key validity passed=failed per FZ-3 |
| Classify a subject | `drenyra-engram verify memory <id> --db <path>` | full memory report; same cutoff layer |
| Incident log | operator-maintained | exact compromise time (RFC3339), reporter, detection method, dispositions |

Every command above is **read-only except `keys rotate`**, which performs the
one legal revocation write. Nothing here reopens writes, repairs data, or
authorizes recovery.

## Recovery and re-signing constraints

- **No backdating:** `keys rotate` stamps the rotation instant; the exact
  compromise time is evidence (Step 3), not an engine input. If the engine ever
  needs to enforce a backdated cutoff, that is a contract change under the
  frozen-contracts rule (version bump, migration path, explicit approval) — it
  is NOT something this playbook smuggles in.
- **No rewrite:** receipts are immutable (no-update/no-delete triggers); a
  "re-sign" is a new act with the new key and the SAME canonical payload bytes.
- **No self-recovery:** the engine never reopens writes on its own and **never
  authorizes recovery**; the operator re-enables normal operation by completing
  Steps 4–7 with the replacement key.
- **Money invariant:** monetary values are whole int64 cents (Go) / BigInt cents
  (TS); re-signing carries payload bytes unchanged, so no cent value moves, and
  no money arithmetic appears anywhere in the response path.
- **Retention is policy, not silence:** pre-cutoff artifacts are retained because
  the operator's policy says so and the frozen contract permits it — not because
  the engine quietly ignored the compromise.

---

## Gap analysis: implementation vs contract (FZ-3)

The comparison below proves `contracts/verification.md` (frozen) and the
implementation agree on FZ-3. Evidence is quoted symbol/contract text.

| Surface | Implementation (evidence) | Contract (evidence) | Verdict |
| --- | --- | --- | --- |
| Read-only verification surface | `SQLiteStore.SigningKeyForVerify` — "reads a signing-key row through the pool connection — the read-only verification surface" (`internal/store/store.go:6228`); delegates to `LookupSigningKey`; no transaction. | "Offline verification is READ-ONLY over the local SQLite store" (`contracts/verification.md`). | **Match** |
| Key row read | `SQLiteStore.LookupSigningKey` reads `algorithm, public_key, created_at, revoked_at`; `Found=false` on a missing row (`internal/store/store.go:5981`). | "key exists; algorithm Ed25519; base64 decodes to a raw public key; `ReceiptKeyID(raw) == keyId`" (verification.md, signing-key validity). | **Match** |
| One-way revoke-only trigger | `signing_keys_revoke_only` "allows EXACTLY ONE update shape: setting a previously-NULL `revoked_at` to a timestamp while every other column stays byte-equal"; re-revocation / un-revocation abort `IMMUTABLE_SIGNING_KEY` (`internal/store/store.go:2278`). `RevokePublicKey` is the only writer. | FZ-3 / FR-8: "Missing or unauthenticated cutoff information MUST NOT be accepted as a cutoff (only the authenticated one-way `signing_keys.revoked_at` update is a cutoff)." | **Match** |
| Pure cutoff comparison | `core.VerifySigningKeyValidity` (`internal/core/verify.go:328`): `created.After(issued)` → fail; unparseable `revoked_at`/`issued_at`/`created_at` → fail ("is not a valid RFC3339 timestamp"); `!issued.Before(revoked)` → fail ("at/after revocation"); empty `revoked_at` → active-key contract. | "`created_at <= issued_at`; `revoked_at` empty or `issued_at < revoked_at`. Issued before revocation passes; issued at/after revocation fails." (verification.md, signing-key validity). | **Match** |
| Revoked key never signs | `internal/receipts/signer.go`: signer fails closed when the keyring OR the store marks the key revoked — "a revoked key is never selected for new signatures". | FZ-3: "A revoked key MUST never sign new receipts." | **Match** |

**Conclusion: Implementation == Contract (FZ-3).** No hardening item was
required: the frozen semantics are already enforced by the existing seam. The
boundary tests in `internal/core/verify_test.go`
(`TestVerifySigningKeyValidityFZ3CutoffMatrix`) and
`internal/server/verify_service_test.go`
(`TestVerifyServiceSigningKeyCutoffMatrix`) pin the comparison as permanent
regressions (AC-7), and the contract guard
(`internal/server/key_compromise_playbook_test.go`) pins the quoted evidence
sentence verbatim against `contracts/verification.md` and `internal/core/verify.go`
(NFR-5). If a future change breaks this agreement, it must surface as a mismatch
through those tests — never as a silent semantic edit.
