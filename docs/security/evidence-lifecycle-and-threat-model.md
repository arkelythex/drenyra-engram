# Evidence Lifecycle and Threat Model

> **Status:** reconciled against the delivered **v0.7.0 local-first EvidenceObject
> slice** (IMPLEMENTED) · **Date:** 2026-08-07
> **Basis:** repository evidence at audit time, per
> [docs/due-diligence/2026-08-product-architecture-audit.md](../due-diligence/2026-08-product-architecture-audit.md)
> (audit blocks N — Evidence, Y — Threat model, X — Privacy, Z — Operability).

This document closes two audit gaps in written form: the **evidence object and
retention model** (audit decision #3, block N) and the **written threat model**
(block Y). The local-first stages of the §2–§3 contract are now **implemented**
(v0.7.0: content-addressed WORM object bytes, schema-v8 immutable metadata,
`object_stored` receipts, scoped store/get, object-level rehash availability
verification, and CLI/HTTP/MCP surfaces with Go + TS coverage). The
production-facing stages — retention expiry, legal hold, export, purge,
cloud/remote storage — are **explicitly DEFERRED** and remain proposed contract.
This document is deliberately explicit about which stages are delivered and
which are deferred.

## Reading guide

| Label | Meaning |
|---|---|
| **IMPLEMENTED** | Behavior exists in this repository with a test or frozen contract (audit evidence rules: PASS). |
| **PROPOSED** | Contract defined here for the DEFERRED EvidenceObject stages (retention, legal hold, export, purge, cloud); **not implemented**; nothing in this document implies they exist. |
| **NOT IMPLEMENTED** | Explicitly absent; the audit classifies the area RISK/UNKNOWN until closure evidence lands. |
| **NON-CLAIM** | A capability this document explicitly does not assert (see §6). |

## 1. EvidenceObject vs evidence links (the core distinction)

The core separation: **link integrity is not object integrity.** Since v0.7.0
the engine stores BOTH — references to evidence (links, in memory envelopes)
and, when captured, the artifact bytes themselves in the local WORM object
store. The two stay distinct: a link is a record **about** an artifact; an
EvidenceObject IS the artifact's bytes + metadata.

| Concept | What it is | Status | Repository anchor |
|---|---|---|---|
| **Evidence link (reference)** | A memory's reference to an external artifact (XML, PDF, CDR, extracto). Two mechanisms: (1) immutable `evidenceRefs` inside the frozen memory envelope; (2) post-write `EvidenceLink` rows in the dedicated `evidence_links` table. | **IMPLEMENTED** | `AccountingMemory.EvidenceRefs` and `EvidenceLink` in [internal/core/types.go](../../internal/core/types.go); `evidence_links(memory_id, ref, actor, timestamp)`, PK `(memory_id, ref)` in [internal/store/store.go](../../internal/store/store.go); semantics in [contracts/memory.md](../../contracts/memory.md) (rules 3, field `evidenceRefs`) |
| **EvidenceObject** | The artifact itself: its immutable bytes and metadata (XML/PDF/CDR/extracto), with storage, hash, retention, legal-hold, export, and availability semantics. | **IMPLEMENTED (local-first slice, v0.7.0):** content-addressed WORM object bytes, schema-v8 immutable metadata, `object_stored` receipt, scoped store/get, rehash availability verification, CLI/HTTP/MCP surfaces. **DEFERRED:** retention expiry, legal hold, export, purge, cloud/remote storage | Model [internal/core/evidence_object.go](../../internal/core/evidence_object.go); WORM store [internal/store/object_store.go](../../internal/store/object_store.go); schema v8 `evidence_objects` [internal/store/store.go](../../internal/store/store.go); TS mirror [core/evidence-object.ts](../../core/evidence-object.ts); surfaces [docs/architecture/evidence-object-v0.7.md](../architecture/evidence-object-v0.7.md) |

**Why the distinction matters:**

- Link integrity is **not** object integrity. Link-level verification proves a
  link row exists in current state and the envelope hash reflects it — it never
  proves the referenced object exists, matches its bytes, or is retained. The
  v0.7.0 object layer closes part of that gap: refs that RESOLVE to a stored
  object are re-hashed against their content address; refs with no stored
  object stay backward compatible and are reported, never failed.
- The `evidence` memory kind documents that an artifact backs a fact; it is a
  record **about** an object, not the object.

## 2. Immutable object lifecycle

The lifecycle below is the EvidenceObject contract. The stages through
**verify availability** are implemented in the local-first v0.7.0 slice; the
stages from **legal hold** onward are explicitly deferred and remain proposed
contract. Each stage names its implemented anchor where one exists.

```text
capture → canonical hash → store (WORM) → link → verify availability   [v0.7.0 DELIVERED]
   → legal hold (optional) → retention expiry → export → purge (approved)  [DEFERRED]
```

| Stage | Semantics | Implemented anchor today |
|---|---|---|
| Capture | The artifact bytes plus provenance (source system, reference, actor) enter the engine. Object bytes never enter the memory envelope. | **IMPLEMENTED (v0.7.0)**: `ObjectStoreInput{Bytes, ContentType, Scope, Source}` with fail-closed scope/source validation — [internal/core/evidence_object.go](../../internal/core/evidence_object.go); `kind=evidence` memories and `evidenceRefs` continue to document artifacts — [contracts/memory.md](../../contracts/memory.md) |
| Canonical hash | SHA-256 over canonical bytes, computed at write and re-verified at read; hash is part of the object's identity. | **IMPLEMENTED (v0.7.0)**: the object id IS the lowercase SHA-256 hex of the bytes (`ComputeObjectID`); re-hashed on every read/availability check (`VerifyObjectBytes`) — [internal/core/evidence_object.go](../../internal/core/evidence_object.go), [internal/store/object_store.go](../../internal/store/object_store.go) |
| Store (WORM) | Write-once, read-many object store; no in-place update, no silent repair; corruption fails closed. | **IMPLEMENTED (v0.7.0)**: content-addressed layout `objects/<sha[0:2]>/<sha[2:4]>/<sha256>`; no overwrite/delete API; path traversal cannot be expressed and escaping the objects root fails closed as corruption; duplicate bytes are a NO-OP (`created=false`, no receipt); schema-v8 `evidence_objects` rows are immutable (no-update/no-delete triggers) — [internal/store/object_store.go](../../internal/store/object_store.go), [internal/store/store.go](../../internal/store/store.go) migration v7→v8 |
| Link | A memory references the object via `EvidenceLink`; linking is post-write, insert-only, period-gated, receipt-covered. | **IMPLEMENTED**: `addLink` with `INSERT OR IGNORE`, one captured timestamp, `PERIOD_CLOSED` gate on closed periods, `evidence_linked` receipt emitted atomically — [internal/store/store.go](../../internal/store/store.go), [contracts/receipts.md](../../contracts/receipts.md). The v0.7.0 object slice itself does not auto-link objects to memories; object↔memory wiring stays link-level and is unchanged |
| Verify availability | Read-time proof that every referenced link resolves to a current row and the envelope recomputes to the latest receipt hash. | **IMPLEMENTED at both levels**: link level (AC7, "evidence availability") and object level (v0.7.0) — `verify memory` gains an object-availability layer (resolved refs must re-hash to their content address; legacy/unresolved refs are reported, never failed) and `verify object` re-hashes the stored WORM bytes ("WORM byte integrity") — [contracts/verification.md](../../contracts/verification.md), [internal/server/verify_service.go](../../internal/server/verify_service.go) |
| Legal hold | A hold record overrides retention expiry for the held scope; hold blocks purge only — never export, verification, or audit reads. | **NOT IMPLEMENTED** (proposed) |
| Retention expiry | Per-jurisdiction retention decision triggers review; expiry never silently deletes — it surfaces for a documented decision. | **NOT IMPLEMENTED** (no retention clock exists; memory `validity` windows are vigencia semantics, not retention) — [contracts/memory.md](../../contracts/memory.md) |
| Export | Tenant-scoped export of objects + links + receipts + verification bundle, so a firm can move or answer audits. | **NOT IMPLEMENTED** (no export surface; `sync` is additive local-to-local reconciliation, not a tenant export — audit block Z: "tenant export and corruption drills") |
| Purge | Deletion only as an approved, receipt-covered, hold-checked act; the link record's history (and its absence) stays auditable. | **NOT IMPLEMENTED** (proposed). Note: hard-delete/legal-retention policy is an open question in the audit — block H: "explicit hard-delete/legal-retention decision" |

## 3. Semantics: storage, hash, retention, legal hold, export, availability

| Semantics | Status | Definition |
|---|---|---|
| **Storage** | **IMPLEMENTED (objects, local-first v0.7.0):** separate WORM object layer — content-addressed object bytes under the local objects root + immutable schema-v8 `evidence_objects` metadata rows; **IMPLEMENTED (memory):** local SQLite (pure Go), additive reversible migrations, fail-closed corruption, immutable history; **DEFERRED (cloud):** cloud/remote object storage and PostgreSQL-in-this-repo remain ROADMAP non-goals (ADR-002 describes the ecosystem authority, not this repository) | [internal/store/object_store.go](../../internal/store/object_store.go), [internal/store/store.go](../../internal/store/store.go), [contracts/provenance.md](../../contracts/provenance.md), [ROADMAP.md](../../ROADMAP.md), [docs/decisions/ADR-002-storage-authority.md](../decisions/ADR-002-storage-authority.md) |
| **Hash** | **IMPLEMENTED:** object identity IS the SHA-256 content address, re-hashed on every read/availability check (v0.7.0); canonical SHA-256 hex for content; envelope hash covers status + evidence/rule refs; receipt hashes form per-subject chains; Go↔TS canonicalization is byte-identical (golden vectors) | [contracts/memory.md](../../contracts/memory.md), [contracts/receipts.md](../../contracts/receipts.md), [contracts/verification.md](../../contracts/verification.md) |
| **Retention** | **NOT IMPLEMENTED — DEFERRED.** This document asserts **no statutory retention period**; that is a per-jurisdiction legal decision per deployment (the v0.6 jurisdiction dimension is in progress — [docs/architecture/fiscal-policy-memory-v0.6.md](../architecture/fiscal-policy-memory-v0.6.md)). The proposed model: retention policy per scope/jurisdiction, expiry surfaces for a documented decision, never silent deletion. Objects have NO retention clock in the delivered slice | — |
| **Legal hold** | **NOT IMPLEMENTED — DEFERRED.** Proposed: hold is a first-class record (scope, reason, owner, timestamps, receipts), overrides expiry, blocks purge only, never blocks export/verification | — |
| **Export** | **NOT IMPLEMENTED — DEFERRED.** Proposed: tenant-scoped export = full revision chains + relations + links + receipts + offline verification bundle, deterministic, receipt-covered. No object-export surface exists in the delivered slice | audit block Z |
| **Availability** | **IMPLEMENTED at both levels (v0.7.0):** link level — per-receipt `evidenceRef` must resolve to a current `evidence_links` row, envelope recompute equals the latest receipt's `resultingEnvelopeHash`, removed links fail verification, `doctor` reports link counts; object level — `verify object` re-hashes stored WORM bytes to the content address (fails closed, no silent repair), `ObjectAvailability` resolves a set of refs re-hashing each, and `verify memory` reports object availability for refs that resolve to stored objects. Offline verification is read-only over the local store | [contracts/verification.md](../../contracts/verification.md), [internal/store/store.go](../../internal/store/store.go) (`Doctor`), [internal/store/object_store.go](../../internal/store/object_store.go) |

## 4. Threat model

### 4.1 Assets

| Asset | Where it lives | Integrity/availability control today |
|---|---|---|
| Memory records (observations) | Local SQLite store | Immutable history, content/envelope hashes, no-update triggers, supersession-as-correction — [contracts/memory.md](../../contracts/memory.md), [contracts/lifecycle.md](../../contracts/lifecycle.md) |
| Evidence links (`evidence_links`) | Local SQLite store | Insert-only, envelope-recompute verification detects removal — [contracts/verification.md](../../contracts/verification.md) |
| Receipts + public keys | Local SQLite store (`receipts`, `signing_keys`) | Immutable rows (no-update/no-delete triggers), per-subject hash chain, revocation one-way — [contracts/receipts.md](../../contracts/receipts.md) |
| Private signing key | User-only file (`${UserConfigDir}/drenyra-engram/signing-keys.json`, dir 0700, file 0600) | Never in the database; explicit rotation — [contracts/receipts.md](../../contracts/receipts.md) |
| Sessions / credentials | `sessions` table | Only SHA-256 token hashes stored; never tokens, never content — [contracts/approval.md](../../contracts/approval.md) |
| Evidence objects | Content-addressed WORM object bytes under the local objects root (`objects/<ab>/<cd>/<sha256>`) + immutable `evidence_objects` rows (schema v8) | Content address = SHA-256 of the bytes (re-hashed on read, fails closed); no update/delete API; scope-first reads; `object_stored` receipt per new object — [internal/store/object_store.go](../../internal/store/object_store.go), [docs/architecture/evidence-object-v0.7.md](../architecture/evidence-object-v0.7.md) |
| Source code + releases | Private repo; private releases (GHCR authenticated pulls) | Proprietary license, private distribution — [README.md](../../README.md) |

### 4.2 Trust boundaries (frozen)

1. **Scope is structural**: company/RUC/period filters precede ranking; cross-tenant leakage is a defect with a required negative test — [contracts/scope.md](../../contracts/scope.md).
2. **Memory never authorizes**: no authorize/approve/allow tool; approval is professional review of a memory — [docs/architecture.md](../architecture.md), [contracts/provenance.md](../../contracts/provenance.md).
3. **Agents record, humans decide**: agents propose memories, links, judgments, reconciliations; only an authenticated principal confirms/approves; voiding admits human/system, never agents — [contracts/lifecycle.md](../../contracts/lifecycle.md), [contracts/judgment.md](../../contracts/judgment.md).
4. **Local-first host boundary**: the SQLite file, keyring, and binary sit on the operator's host; host security is the effective boundary until production hardening (see §5, audit block Z).

### 4.3 Threat table

Columns: actor → objective / representative attack → controls that exist in this repository (evidence) → residual risk → required closure (audit block).

| # | Actor / scenario | Attack objective | Implemented controls (evidence) | Residual risk | Required closure |
|---|---|---|---|---|---|
| T-1 | **Malicious employee** (firm staff without approval rights) | Read other clients' memory; forge approvals; silently alter history | Structural scope isolation + required negative test ([contracts/scope.md](../../contracts/scope.md)); human-only approval gate with authenticated principal and scope checks (`TENANT_SCOPE_MISMATCH`, `COMPANY_SCOPE_DENIED`) ([contracts/approval.md](../../contracts/approval.md)); immutable history, no in-place edit ([contracts/memory.md](../../contracts/memory.md)); envelope hash + receipt chain make tampering detectable offline ([contracts/verification.md](../../contracts/verification.md)) | **High:** an employee with direct file access to the local SQLite DB can read everything on that host (no encryption at rest) and could tamper with files; detection is fail-closed but not forensic | Host/OS access control, encryption at rest, backup/restore + corruption drills, tenant-export drill (audit Z); SoD/dual-approval review (audit J) |
| T-2 | **Malicious accountant** (legitimate scoped role) | Approve beyond authority; approve a stale version; approve then deny later | Role matrix per fiscal effect with materiality raising the ladder; `expectedEnvelopeHash` guard rejects approving a version different from the one reviewed; idempotency by (tenant, requestId); immutable approval events with principal snapshot ([contracts/approval.md](../../contracts/approval.md)); `closing` requires `controller` ([contracts/closing.md](../../contracts/closing.md)); approved-then-superseded memories return to `pending_review` (AC11) | **Medium:** in-role abuse is bounded but possible; dual approval and override audit are not implemented (audit J RISK) | Versioned policy matrix, SoD/dual-approval rules, override audit tests (audit J); rubber-stamping controls in UX (audit AG) |
| T-3 | **Malicious tenant** (cross-tenant access) | Retrieve another tenant/company's memory via search/context/API | Scope is part of identity; exact-scope semantics; CLI/HTTP derive `companyId = ruc` while MCP accepts full scope (fail-closed direction); cross-tenant invisibility is a REQUIRED conformance test ([contracts/scope.md](../../contracts/scope.md), [README.md](../../README.md) "Scope across surfaces"); tenant/company checks inside the approval transaction | **Low–Medium** for memory reads; the full cross-tenant matrix for every operation is not exhaustively generated (audit Q RISK) | Generated cross-tenant matrix for every operation; nonexistence-safe errors (audit Q) |
| T-4 | **Compromised agent** (an AI agent's session is taken over or misdirected) | Write misleading memories/links; propose false contradictions; attempt approvals | Agents cannot approve/confirm/void: approval requires a separate authenticated principal; MCP approval/confirm tools fail closed without a session ([contracts/approval.md](../../contracts/approval.md), [contracts/judgment.md](../../contracts/judgment.md)); judgment/reconciliation confirmation is principal-only ([contracts/judgment.md](../../contracts/judgment.md), [contracts/reconciliation.md](../../contracts/reconciliation.md)); every act is provenance-tagged with actor kind; receipts for claimed acts record the claimed actor ([contracts/provenance.md](../../contracts/provenance.md), [contracts/receipts.md](../../contracts/receipts.md)) | **Medium:** a compromised agent can pollute memory with false records; human gate limits fiscal-effect damage but noise/poisoning requires review discipline | Capability matrix, agent authorization limits, session revocation, anomaly detection (audit R); insufficient-evidence behavior (audit S) |
| T-5 | **Prompt-injected document** (an artifact's content steers an agent into malicious actions) | Exfiltrate data via agent tool calls; approve or propose harmful acts | The engine has no content-execution surface: documents are data, never instructions to the engine; agents only propose, humans decide (trust boundary 3); LLM output is explicitly not evidence until supported by structured source evidence and human review (audit "safe to state"); fiscal-effect memories always pass the human gate ([contracts/lifecycle.md](../../contracts/lifecycle.md)) | **High:** no untrusted-document ingestion policy, no prompt-injection test suite, no redaction/data-classification pipeline (audit R, X RISK). Injection targets the agent harness (outside this repo) but this repo's data is the payload it manipulates | Untrusted-document policy, prompt-injection tests, data classification + provider routing, redaction (audit R, X) |
| T-6 | **Compromised database** (local SQLite file altered or replaced) | Alter history; forge links; destroy records | Corruption fails closed (checksums, schema guards; silent repair forbidden); additive migrations; no-update/no-delete triggers; offline verification detects altered envelopes and removed evidence (AC7) ([contracts/provenance.md](../../contracts/provenance.md), [contracts/verification.md](../../contracts/verification.md)); `doctor` health snapshot ([internal/store/store.go](../../internal/store/store.go)) | **High:** detection after the fact, not prevention; attacker with file-level access can rewrite the store and keyring together; no backup/restore drill demonstrated (audit Z) | Restore/corruption drills, off-host backups, recovery objectives, TDE decision (audit Z, O); production DB authority decision |
| T-7 | **Compromised object storage** (EvidenceObject layer) | Swap an artifact for a different one; delete artifacts to break reconstruction | **IMPLEMENTED (local-first slice, v0.7.0):** content-addressed WORM store — the id IS the SHA-256 of the bytes, so any swap is detected by the read-time re-hash (fails closed, no silent repair); no overwrite/delete API; path escape fails closed as corruption; `object_stored` receipts per new object; scope-first reads; `verify memory`/`verify object` report byte integrity ([internal/store/object_store.go](../../internal/store/object_store.go), [contracts/verification.md](../../contracts/verification.md)) | **Medium–High:** a host-level attacker with write access to the objects root can still replace or delete bytes; detection is fail-closed but not forensic; no retention clock, legal hold, export surface, backup/restore drill or cloud redundancy (DEFERRED) | Host/OS access control, encryption at rest, backup/restore + corruption drills, retention/legal-hold/export surfaces (audit Z, N); object store compromise playbook (audit Y) |
| T-8 | **Compromised identity provider / session store** | Impersonate a principal and approve; replay approvals | Principal is derived from an authenticated session, never caller-declared (ADR-003); sessions store only SHA-256 hashes; `local_dev` seeding is isolated behind `DRENYRA_ENV=local_dev`; envelope-hash guard and required reason limit blind approvals; approval events are immutable with the full principal snapshot ([contracts/approval.md](../../contracts/approval.md), [docs/decisions/ADR-003-approval-principal.md](../decisions/ADR-003-approval-principal.md)) | **Medium:** session compromise = in-scope approval capability for that principal; **OIDC/MFA are NOT implemented** (`oidc` recognized, not resolvable — NON-CLAIM); no HSM/KMS for keys | Production identity provider + MFA/assurance + membership source (audit I); key management decision (audit O); session revocation |
| T-9 | **Compromised signing key** | Mint valid-looking receipts for forged acts | Private key never stored in the DB; user-only keyring file (0600/0700); explicit one-transaction rotation; one-way revocation (revoked keys never sign; pre-revocation receipts remain valid); historical acts never backfilled (no retrospective signing) ([contracts/receipts.md](../../contracts/receipts.md)) | **Medium:** a host-level attacker who reads the keyring can sign claimed acts; no HSM/KMS; compromise-response playbook not written or tested (audit O RISK) | Key rotation/revocation + compromise-response drill (gate G-7, audit O); HSM/KMS decision |
| T-10 | **Supply chain** | Ship a tampered binary or dependency that leaks or forges data | Private, proprietary repository and private releases (GHCR authenticated pulls) ([README.md](../../README.md)); vulnerability reporting process ([SECURITY.md](../../SECURITY.md)) | **High:** no signed releases, no SBOM, no dependency audit demonstrated (audit AJ FAIL/RISK); Go module is go-gettable | Signed releases + SBOM, dependency/CI hardening, contribution review (audit AJ); reconciliation with the proprietary policy |

## 5. Residual-risk summary (what a v1 gate must close)

1. Local-first storage makes **host security** the effective trust boundary (T-1, T-6): encryption at rest, backups, restore and corruption drills, and a production DB authority decision are prerequisites for gate G-6.
2. **Evidence objects are delivered local-first, production stages deferred** (T-7, gate G-5): the v0.7.0 slice delivers WORM storage, hash identity, and availability verification, so "evidence-backed" now means link-backed AND byte-verified locally; **retention expiry, legal hold, export, purge, cloud/remote storage and production object-store operations (backup/restore drills, TDE) remain DEFERRED** — "evidence retention" claims stay bytes-backed, not policy-backed, until those land.
3. **Identity is not production-ready** (T-8, gate G-4): no OIDC/MFA; `service_assertion` bearer + `local_dev` are the current bounds.
4. **Prompt injection and data classification** are unaddressed (T-5, audit R/X): the engine's structure reduces blast radius (agents cannot approve; documents never execute) but does not eliminate it.
5. **Key compromise response** is untested (T-9, gate G-7).
6. **Supply-chain assurance** is absent (T-10, audit AJ).

## 6. Non-claims

This document does **not** claim, and the repository does **not** implement:

- Any **integration** (SUNAT, ERP, SIRE ingestion, bank feeds) — audit block W is UNKNOWN; ROADMAP has no implemented integration.
- **OIDC** or any production identity provider, MFA, or signed service assertions — `oidc` is recognized but not resolvable ([contracts/approval.md](../../contracts/approval.md)).
- **Cloud infrastructure** or a managed offering — ROADMAP non-goal; `sync` is local, additive, conflict-visible only.
- **Compliance** of any kind (certifications, statutory retention, tax correctness) — receipts and verification never assert accounting correctness ([contracts/verification.md](../../contracts/verification.md)); legal/tax validation is out of scope for repository evidence (audit).
- **Production evidence-object operations** — the v0.7.0 slice stores local WORM bytes with hash identity and availability verification, but there is **no retention clock, legal hold, export, purge/deletion, cloud/remote object storage, OCR or content search over objects, or SUNAT/ERP object ingestion**; those remain DEFERRED (§2–§3).
- An **externally reviewed** threat model — this is the first written model (audit block Y closure requires review).

## Next step

Review this model with product and security. On sign-off, (1) treat the
local-first v0.7.0 slice as the delivered base for gate G-5 and plan the
retention/legal-hold/export/purge stages as the next EvidenceObject work,
(2) run the threat table against each v0.6/v1.0 slice as a design check, and
(3) schedule the audit-Y closure review (external or independent) before v1.0.
