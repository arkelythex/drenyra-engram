# Data, security, and privacy boundaries

Drenyra Engram is a local accounting-memory engine. This guide tells an operator
what the repository stores, what its controls cover, and what remains an
operator or deployment responsibility. It is not a privacy policy, compliance
statement, security audit, or legal advice.

Read this page with the [evidence lifecycle and threat model](security/evidence-lifecycle-and-threat-model.md) and, for a suspected signing-key exposure, follow the [key-compromise response](security/key-compromise-response.md). Those documents own the detailed threat analysis and incident sequence.

## At a glance

- The local store is a SQLite database plus a separate local WORM evidence-object root. The host, filesystem permissions, backups, and key custody are part of the security boundary.
- Content encryption is **off by default**. When configured, it encrypts only the four company-scope memory narrative fields; it is not whole-database, filesystem, backup, or evidence-object encryption.
- Exact company scope is a structural filter. It is an important control, not a replacement for host access control, identity configuration, or operator review.
- Receipts and offline verification prove specified integrity, scope, chain, and provenance properties of Engram acts. They do not prove evidence truth, accounting correctness, legal compliance, or business authorization.

## Operating-boundary matrix

| Area | Implemented repository behavior | Operator responsibility | Unsupported, deferred, or not claimed | Evidence |
| --- | --- | --- | --- | --- |
| Local persistence | SQLite persists memory revisions, scope and provenance, lifecycle and relation history, evidence/rule links, approval and other audit records, receipt/public-key data, session and membership records, and evidence-object lifecycle metadata. | Protect the database file and its host; choose where it lives, who can read it, and how long its backups persist. | This is not a complete data-minimization inventory or a managed service. | [memory](../contracts/memory.md), [approval](../contracts/approval.md), [lifecycle](architecture/evidence-lifecycle-v0.8.md) |
| Evidence objects | Artifact bytes live under the local content-addressed WORM objects root; immutable object metadata is stored in SQLite. | Protect and back up the object root with the database; verify recovery and access controls. | No cloud/remote object store, OCR, or object-content search is provided. | [object architecture](architecture/evidence-object-v0.7.md) |
| Content encryption | Opt-in AES-256-GCM encryption with a per-tenant key derived from an operator-supplied master key protects newly written company-scope memory narrative content. Reads fail closed without the correct key. | Secure, recover, and restrict the master key; identify and re-encrypt legacy plaintext rows where appropriate. | Encryption is off by default and does not claim full-database, TDE, backup, filesystem, or evidence-object-byte encryption. | [crypto source](../internal/store/crypto.go), [CLI source](../cmd/drenyra-engram/main.go) |
| Tenant isolation | Scope-first, exact company/RUC/period filtering and identity binding protect supported scoped reads and mutations; authenticated paths additionally check membership scope. | Configure memberships and adapters correctly; test your own deployment's tenant boundaries and restrict direct file access. | Structural scope does not prevent a host-level reader of local files from reading what the host can access. | [scope](../contracts/scope.md), [threat model](security/evidence-lifecycle-and-threat-model.md) |
| Receipts and verification | Ed25519 receipts are immutable, chained per subject, and can be verified offline with public keys and stored state. | Keep signing keys separate from the database; run verification and follow the incident procedure after suspected key compromise. | A receipt does not establish evidence truth, professional judgment, accounting correctness, or authorization of an external business action. | [receipts](../contracts/receipts.md), [verification](../contracts/verification.md) |
| Retention, holds, purge, export | Object-level policy-backed retention, holds, approved purge, and a deterministic tenant/RUC-scoped lifecycle export exist. | Supply supported policy evidence and make the human retention, hold, approval, export handling, and backup decisions. | No statutory retention duration, automatic deletion, bundle import/restore, or general erasure facility is claimed. | [lifecycle](architecture/evidence-lifecycle-v0.8.md) |
| Sync | Local database-to-database sync is additive, idempotent, provenance-preserving, and conflict-visible for observations, relations, and transitions. | Choose trusted endpoints/filesystems and validate each side's encryption configuration and conflict report. | It is not cloud sync, a backup, an object-byte transfer, receipt/key replication, or lifecycle-bundle import. | [sync source](../internal/sync/sync.go) |
| Recovery | WAL-safe snapshot, restore verification, corruption-drill, and doctor mechanics are repository-tested. | Establish backup frequency, retention, off-host storage, restore ownership, and recovery objectives; drill the full database-plus-object-root recovery path. | No RTO, RPO, production backup schedule, external drill, or production readiness is demonstrated. | [operability evidence](architecture/operability-evidence.md) |
| Authentication | Session-based approval identity and an optional HTTP-only stateless OIDC access-token path derive a principal before protected decisions. | Configure the identity provider, memberships, issuer/audience, JWKS reachability, and process restarts for suspected issuer-key compromise. | No browser OIDC flow, ID-token use, MFA/ACR elevation, user provisioning, or token revocation beyond the membership check is provided. | [OIDC architecture](architecture/oidc-access-token-identity.md) |
| Logging and telemetry | The repository documents local `doctor` and verification outputs; this guide makes no telemetry or analytics collection claim. | Define host, reverse-proxy, identity-provider, and process-log collection, access, redaction, and retention rules before operating with sensitive data. | This is not a no-logging guarantee and no logging redaction, telemetry retention, or third-party data-handling claim is made. | [operability evidence](architecture/operability-evidence.md), [verification](../contracts/verification.md) |

## What persists locally

### SQLite store

The SQLite store persists the memory engine's durable state, not only a search
index. In particular, it holds memory records and immutable revisions; exact
scope, identity, content hashes, and provenance; relations and lifecycle
transitions; evidence and rule links; authenticated-decision snapshots;
receipts and public signing keys; session-token hashes and memberships; and
retention, hold, purge, and export-related metadata. The storage and lifecycle
designs identify the immutable tables and records in more detail:

- [memory and provenance model](../contracts/memory.md)
- [receipt and signing-key contract](../contracts/receipts.md)
- [approval data boundary](../contracts/approval.md)
- [evidence lifecycle data model](architecture/evidence-lifecycle-v0.8.md)

The store intentionally preserves history through immutable records and
successor/lifecycle relationships. Do not treat a SQLite file, its backups, or
exported manifests as free of personal, company, tax, provenance, or audit
information merely because some memory content is encrypted.

### Evidence-object store

An EvidenceObject is the artifact bytes plus immutable metadata. The bytes are
stored separately from SQLite in a local, content-addressed WORM layout beneath
the configured objects root. The metadata row persists the object ID and
SHA-256, size, optional content type, exact tenant/company/RUC/period scope,
source system/reference/actor, storing actor and time, and the relative object
path. A duplicate byte sequence is a no-op; a newly stored object receives an
`object_stored` receipt. See the [EvidenceObject architecture](architecture/evidence-object-v0.7.md) and [threat model](security/evidence-lifecycle-and-threat-model.md).

The object ID is the SHA-256 of the stored bytes. Object reads and verification
rehash those bytes and fail closed on a mismatch or unexplained absence. This
is an integrity and availability check; it is not content classification,
redaction, malware scanning, or a statement that the artifact is true.

## Encryption and visible metadata

Set `DRENYRA_ENCRYPTION_MASTER_KEY` to a hex- or base64-encoded 32-byte master
key to enable encryption for CLI-opened stores. The key is operator-held; the
repository does not supply KMS/HSM custody for this master key. The store
uses a tenant-derived AES-256-GCM key for newly written **company-scope** memory
content. Missing or wrong keys fail reads closed rather than returning partial
content. Encryption is disabled when the variable is absent.

When encryption is enabled, the following distinction is critical:

| Data | At-rest behavior | Operator implication |
| --- | --- | --- |
| `content.what`, `content.why`, `content.where`, and `content.learned` of a newly written company-scope memory | Encrypted together in the content envelope. | Do not expose the master key; loss of the correct key prevents reads. |
| Legacy company-scope rows | May remain plaintext until the operator uses the supported `encrypt` re-encryption path. | Inventory first; `encrypt` defaults to a dry run and `--apply` is a deliberate write. |
| Institutional-scope memory content | Not covered by this company-scope encryption feature. | Treat it as plaintext local data unless another deployment control protects it. |
| Memory identity, title, scope tuple, status, timestamps, provenance, content/envelope hashes, links, relations, audit records, receipt payloads, and principal snapshots | Remain outside the encrypted content envelope. | Treat these fields as sensitive metadata and protect the database and backups accordingly. |
| Evidence-object bytes and their metadata | Not covered by this content-encryption feature; object bytes are local WORM files and metadata stays in SQLite. | Apply host/filesystem controls and backup protections to the objects root. |

The implementation details and legacy conversion behavior are in
[`internal/store/crypto.go`](../internal/store/crypto.go), the CLI key boundary
is in [`cmd/drenyra-engram/main.go`](../cmd/drenyra-engram/main.go), and the
current delivered encryption record is in the [changelog](../CHANGELOG.md).
Encryption does not grant authority or change the scope rules.

## Scope and tenant isolation

A company scope carries organization, company, RUC, and optional period. Search
filters scope before ranking, and scope is part of memory identity. Evidence
objects require an exact company scope and are not institutional objects in the
implemented object store. On authenticated paths, the requested effective
organization/company scope must match the resolved membership scope; a mismatch
fails closed.

These controls prevent supported query and mutation paths from treating another
company's record as an in-scope result. They do not protect against an operator
or attacker with direct access to the SQLite database, objects root, backups,
or a compromised host. Use the [scope contract](../contracts/scope.md) for the
precise semantics and the [threat model](security/evidence-lifecycle-and-threat-model.md) for the residual-risk boundary.

## Receipts, keys, and incident response

For covered acts, the store atomically records an Ed25519 receipt with the act.
Receipts chain within a subject and include scope, payload hash, signer key ID,
and applicable provenance or verified-principal data. SQLite holds public keys;
the private signing seed belongs in a user-only keyring file, not the database.

Use `verify` to test receipt canonicalization, envelope integrity, signatures,
key timing, scope, chain continuity, and the applicable provenance and evidence
layers. Verification is local and read-only. It **does not** prove that an
artifact is truthful, a policy was interpreted correctly, a professional
judgment was sound, an accounting result is correct, or a business act was
authorized. See the [verification contract](../contracts/verification.md) and
[receipt contract](../contracts/receipts.md).

If a private signing key or its environment may be exposed, do not improvise a
recovery workflow. Preserve the relevant evidence and follow the existing
[key-compromise response](security/key-compromise-response.md), including its
rotation, revocation, cutoff, and re-verification steps.

## Retention, holds, purge, deletion, and export

The delivered lifecycle applies to evidence objects. A deployment provides
versioned policy evidence; the engine resolves eligibility exactly and fails
closed when it cannot resolve it. No statutory duration is supplied by the
repository. Legal, audit, dispute, and fiscalization holds block purge by
default; holds are object-level, and hold history remains visible after a lift.

Purge is neither automatic nor an ordinary delete. An eligible object must pass
the active-hold, closed-period, scope, lifecycle-hash, authorization, and
separation-of-duties checks; approval alone does not remove bytes. Physical byte
removal is a guarded two-phase execution. Object metadata, hashes, links,
events, receipts, eligibility, and approval records remain immutable after a
completed purge. A crash-window absence is surfaced as an auditable finding,
not silently repaired.

The lifecycle export is read-only and tenant/RUC scoped. It exports object
metadata, lifecycle records, holds, retention policies, and receipt chains; it
does **not** export object bytes, and a purged object's bytes cannot be
resurrected by the export. Bundle import/restore is out of scope. See the
[evidence lifecycle guide](architecture/evidence-lifecycle-v0.8.md) for the
state machine and the [threat model](security/evidence-lifecycle-and-threat-model.md) for the associated non-claims.

Accordingly, do not represent purge as comprehensive deletion, a privacy-rights
fulfillment mechanism, deletion from backups, deletion from external systems,
or deletion of immutable operational history. Establish those processes outside
this repository where they are required.

## Sync and recovery

`sync --from <db> --to <db>` reconciles observations, relations, and transition
history between local stores. It is additive and idempotent: it does not delete,
overwrite immutable conflicts, or silently resolve divergent chains. It does
not transfer evidence-object bytes, receipts, signing keys, hold/purge
lifecycle records, or an evidence lifecycle export bundle. It is therefore not
a substitute for evidence transfer or backup.

When the source store is content-encryption enabled, sync refuses an unencrypted
target. With encryption enabled on both stores, the source decrypts for the
store interface and the target re-encrypts its imported company-scope content
with its own derived key. This guard does not make local database-to-database
sync a cloud transport or a general transport-security claim.

Repository-tested recovery mechanics create WAL-safe database snapshots and
verify restore candidates before they are published; corruption drills operate
only on marked copies. They demonstrate mechanics, not an operated backup
program. Operators must own and test all of the following together:

1. A consistent SQLite database backup strategy, including any required WAL/SHM
   handling for the selected method.
2. A corresponding backup and restoration procedure for the separate objects
   root.
3. Secure recovery of the content-encryption master key and signing-key material
   under a documented access-control process.
4. Retention, off-host storage, restore authorization, recovery-point, and
   recovery-time decisions, plus periodic restore and verification drills.

See [operability evidence](architecture/operability-evidence.md). The
[key-compromise response](security/key-compromise-response.md) also names the
database, WAL/SHM sidecars, objects, and key material that must be preserved
when investigating an exposure.

## Authentication and OIDC limits

Protected decisions derive a principal from authentication and stored membership
information; caller-supplied role or identity fields do not create authority.
Session storage keeps token hashes rather than raw tokens. The optional OIDC
path is an HTTP resource-server path for stateless RS256 access tokens with
exact issuer/audience validation and a database membership/scope cross-check.
Raw OIDC tokens are not persisted, logged, or hashed into a session row by this
path.

This is deliberately bounded. OIDC is disabled by default and does not provide
browser authorization-code/PKCE flows, ID-token use, refresh tokens, user
provisioning, MFA/ACR elevation, or token revocation beyond the membership
check. The JWKS cache has no TTL; after an issuer signing-key compromise, the
documented operator action is to restart the serving process or force a JWKS
refresh. Read the [OIDC architecture](architecture/oidc-access-token-identity.md)
before enabling it.

## Logs, telemetry, and safe data

The engine has local diagnostic surfaces such as `doctor` and offline
verification. The current source does not identify an application telemetry or
analytics subsystem, and repository documentation does not define a log
redaction or telemetry-retention policy. That absence is not a promise that a
deployment, runtime, reverse proxy, identity provider, shell history, or host
will not record sensitive values. Keep sensitive arguments, database paths,
object paths, exports, keys, and identity-provider data out of logs unless a
separate, reviewed operational policy permits them.

Use fictional, synthetic, or legally approved anonymized data for demos, tests,
fixtures, bug reports, and security reproductions. Never put production
credentials, tokens, customer data, real taxpayer data, or raw production
evidence objects in repository artifacts. The [getting-started guide](GETTING-STARTED.md),
[demo fixture](demo/reconstructible-close.md), and [security reporting policy](../SECURITY.md)
show the same boundary.

## Explicit non-claims

This repository does not claim GDPR, SOC 2, ISO, regulatory, tax, residency,
privacy, or certification compliance. This document is not a privacy policy and
does not provide legal advice. It also does not claim production readiness, a managed
cloud service, whole-disk or whole-database encryption, remote evidence storage,
complete telemetry controls, comprehensive erasure, or guaranteed recovery
objectives.

Before using Engram with sensitive data, have the accountable organization
establish its own legal, privacy, security, identity, logging, retention,
backup, and incident-response policies. This guide describes repository behavior
and its boundaries; it does not replace those decisions.
