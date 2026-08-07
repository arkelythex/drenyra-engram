# Contract: memory

> Version: 0.2 · Status: frozen-for-0.2 · Transport-agnostic.

Defines the **accounting memory model**: the unit of institutional, fiscal and
evidentiary memory for accounting agents.

## AccountingMemory

| Field          | Description                                                                 |
| -------------- | --------------------------------------------------------------------------- |
| `id`           | Canonical memory identifier (immutable)                                     |
| `topicKey`     | Stable accounting-fiscal key (the upsert target)                            |
| `title`        | Short, searchable title (verb + what)                                       |
| `kind`         | `fact` `evidence` `decision` `rule` `exception` `control` `obligation` `summary` |
| `scope`        | Company/RUC/period context (see [scope](scope.md))                          |
| `content`      | Structured: `What` / `Why` / `Where` / `Learned`                            |
| `status`       | `active` `pending_review` `approved` `rejected` `superseded` `voided` (see [lifecycle](lifecycle.md)) |
| `fiscalEffect` | `none` `journal_entry` `declaration` `closing` `adjustment` `reclassification` `approval` `sunat_filing` |
| `effectiveAt`  | When the event happened ACCOUNTING-wise (the period it belongs to)          |
| `recordedAt`   | When it entered the system — automatic, immutable                           |
| `observedAt`   | When it was detected (optional)                                             |
| `source`       | Structured provenance: `system` (required), `reference`, `actorId`, `actorKind` (`human`/`agent`/`system`), `model`, `session` |
| `validity`     | Effective/expiry window (vigencia)                                          |
| `evidenceRefs` | Evidence objects backing this memory (XML/PDF/CDR/extracto); grows via links |
| `ruleRefs`     | Policy/rule paths applied (e.g. `policy/igv/late-document-v3`); grows via links |
| `confidence`   | Optional 0..1 probability (never money)                                     |
| `materiality`  | Optional monetary threshold in **int64 cents** (never float)                |
| `contentHash`  | Canonical SHA-256 of the immutable content (see below)                      |
| `receiptId`    | Reference to the Ed25519 receipt issued by the Drenyra ecosystem (only a reference — this engine never signs) |
| `supersedesId` | Id of the memory this one replaces (set on the successor)                   |
| `revision`     | 1-based revision within the (topicKey, scope) chain                         |

### Memory kinds

| Kind          | Meaning                                                                 |
| ------------- | ----------------------------------------------------------------------- |
| `fact`        | Directly observed accounting fact (a comprobante was issued, a balance)  |
| `evidence`    | Document or result backing a fact (XML, PDF, CDR, bank statement, SUNAT response, RUC capture, SIRE file, contract, email) |
| `decision`    | Professional judgment by a person or agent (classified as expense, credit deferred) |
| `rule`        | Policy or disposition applied (SUNAT rule, internal policy, IFRS, materiality threshold) |
| `exception`   | Something that does not fit or needs intervention (XML vs PDF amounts differ, invoice missing from SIRE, balance mismatch) |
| `control`     | A validation executed and its result (duplicity PASS, closed period BLOCKED, tenant PASS, minimum evidence FAIL) |
| `obligation`  | A future event derived from what is known (file PDT 621, request substantiation, reconcile an account) |
| `summary`     | A closing or executed-work summary (monthly close, mission summary)      |

### Fiscal effects

`none` marks an informative memory (saved directly `active`). Every non-none
effect triggers the mandatory human-approval gate
([lifecycle.md](lifecycle.md)): `journal_entry` (asiento), `declaration`
(declaración), `closing` (cierre mensual), `adjustment` (ajuste),
`reclassification` (reclasificación), `approval` (aprobación),
`sunat_filing` (presentación SUNAT).

### Triple timestamps — the late-event semantics

The separation between `effectiveAt` and `recordedAt` is critical:

- the invoice belongs to **15 July**;
- it was received on **3 August**;
- the accountant recorded it on **4 August**;
- the July close had already been completed.

A plain search sees an invoice. Drenyra-Engram sees a **late event affecting a
previous closed period** — `effectiveAt` stays in July, `observedAt` in
August, and the period summary surfaces it as such.

## Rules

1. **Structured content.** Free-form blobs are discouraged;
   `What/Why/Where/Learned` is the canonical shape, extensible per kind.
2. **Stable topic keys.** Evolving knowledge (a decision revisited across
   sessions) upserts under one `topicKey`; the history remains via immutable
   revisions and relations.
3. **Immutable history.** A memory is never edited in place after write; a
   correction is a new revision linked via `supersedes`. Evidence and rule
   references grow ONLY through link records — the memory itself never mutates.
4. **Scope is mandatory.** A memory without company/RUC/period context is only
   allowed for truly institutional (cross-scope) knowledge and must be marked
   as such (see [scope](scope.md)).
5. **The gate is mandatory.** A memory with fiscal effect is `pending_review`
   until a HUMAN approves it; the gate fails closed with `GATE_REQUIRES_HUMAN`.
6. **Vigencia is honored at read time.** Expired memories surface as stale,
   never as current fact.

## Frozen semantics (v0.2)

1. **Immutable revision model.** `save()` on the same `topicKey` + exact scope
   creates a NEW immutable revision (`revision + 1`); the previous current
   revision is superseded (status + `supersedesId` + `supersedes` relation) in
   the same transaction. In-place edit is forbidden: content, scope,
   timestamps, source and `contentHash` of a stored revision never change after
   write. History is preserved and every revision is retrievable by `id`.
2. **Write idempotency.** Same `topicKey` + exact scope → a NEW immutable
   revision is always created; the outcome is `updated` whether the content is
   identical or evolved. A new memory is never silently overwritten. The
   outcome `conflict` is reserved for genuine optimistic-concurrency races in a
   future slice. An unexpected persistence error → outcome `unknown` — success
   is never fabricated.
3. **Canonical content hash.** `contentHash` is the SHA-256 (hex) of the
   immutable content: scope, kind, title, fiscal effect, effective date, the
   four content fields, and the source system + actor kind. Identity, status,
   `recordedAt` and `revision` do NOT participate — the hash identifies the
   content, not the envelope. Same input → same hash; any immutable-field
   change → a different hash.
4. **Two memory tiers.** Informative memory (fiscalEffect `none`) saves
   directly as `active`. Memory with accounting effect passes the mandatory
   gates: `pending_review` → human `approve` → `approved`.

## Relations (17)

The six legacy relations plus eleven accounting-evidence relations:

`related` `compatible` `scoped` `conflicts_with` `supersedes` `not_conflict`
`supports` `contradicts` `explains` `derived_from` `posted_as` `reconciles`
`reverses` `requires` `violates` `approved_by` `rejected_by`

These turn the memory into an evidence-backed accounting knowledge graph:
a comprobante XML `supports` its entry, `appears_in` SIRE, is `reconciled`
with the bank movement, and a rule is `violated` or `applied`.

## Storage

- Local store is the default; cloud is an explicit opt-in with clear sync
  semantics (additive, provenance-preserving, conflict-visible).
- Store layout is versioned (schema v8 since the v0.7.0 evidence-object slice);
  migrations are additive and reversible; corruption fails closed, silent repair
  is forbidden.

## Evidence objects (v0.7.0) — relation to this contract

`evidenceRefs` in this contract are LINK records (references) that grow only
via `evidence_linked`; the memory envelope itself never mutates. The v0.7.0
EvidenceObject slice is a SEPARATE local WORM object store: content-addressed
artifact bytes (object id = SHA-256 hex of the bytes), immutable schema-v8
`evidence_objects` metadata rows, `object_stored` receipts, scoped store/get,
and `verify object` rehash verification. A memory's `evidenceRefs` may point at
stored objects, and `verify memory` reports object availability for refs that
resolve to stored objects — but the memory model is unchanged: refs are
references, the object store holds bytes. Retention expiry, legal hold, export,
purge and cloud/remote object storage are explicitly DEFERRED — see
docs/architecture/evidence-object-v0.7.md and
docs/security/evidence-lifecycle-and-threat-model.md.

## Conformance

Vectors cover: canonical serialization, topic-key upsert behavior,
structured-content validation, immutable-history enforcement, the approval
gate, the canonical hash, stale-read marking, and the frozen-for-0.2
revision/idempotency semantics above.
