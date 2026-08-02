# Contract: memory

> Version: 0.1-draft · Status: draft · Transport-agnostic.

Defines the **observation model**: the unit of institutional accounting memory.

## Observation

| Field        | Description                                                          |
| ------------ | -------------------------------------------------------------------- |
| `id`         | Canonical observation identifier                                     |
| `title`      | Short, searchable title (verb + what)                                |
| `type`       | `bugfix` · `decision` · `architecture` · `discovery` · `pattern` · `config` · `preference` · `judgment` · `policy` |
| `scope`      | Company/RUC/period context (see [scope](scope.md))                   |
| `topic_key`  | Stable key for evolving knowledge (upsert target)                    |
| `content`    | Structured: `What` / `Why` / `Where` / `Learned`                     |
| `lifecycle`  | `draft → reviewed → promoted → superseded` (see [lifecycle](lifecycle.md)) |
| `vigencia`   | Effective/expiry window                                              |
| `provenance` | Actor, timestamp, source, session (see [provenance](provenance.md))  |
| `version`    | Observation schema version                                           |

## Rules

1. **Structured content.** Free-form blobs are discouraged; `What/Why/Where/Learned` is the canonical shape, extensible per type.
2. **Stable topic keys.** Evolving knowledge (e.g. a decision revisited across sessions) upserts under one `topic_key`; the history remains via relations.
3. **Immutable history.** An observation is never edited in place after promotion; a correction is a new observation linked via relations (`supersedes`, `related`).
4. **Scope is mandatory.** An observation without company/RUC/period context is only allowed for truly institutional (cross-scope) knowledge and must be marked as such.
    5. **Vigencia is honored at read time.** Expired observations surface as stale, never as current fact.

## Frozen semantics (v0.1)

> The bullets in this section are **frozen-for-0.1**: normative, enforced by the
> conformance suite, and carried unchanged into the standalone Go engine
> (ADR-001). The rules above stay `0.1-draft` until the Phase 1 freeze review.

1. **Immutable revision model.** `save()` on the same `topicKey` + exact scope
   creates a NEW immutable revision (`revision + 1`). In-place edit is forbidden:
   content, scope, and provenance of a stored revision never change after write.
   History is preserved and every revision is retrievable by `id`.
2. **Write idempotency.** Same `topicKey` + exact scope → a NEW immutable
   revision is always created; the outcome is `updated` whether the content
   is identical or evolved (topic evolution is normal — a topic key is a
   stable handle for evolving knowledge, never a unique-content constraint).
   A new observation is never silently overwritten — history is preserved
   and the latest revision is the current one. The outcome `conflict` is
   reserved for genuine optimistic-concurrency races (two writes on the
   same revision base) in a future slice — it is never produced by plain
   sequential saves. An unexpected persistence error → outcome `unknown` —
   success is never fabricated; callers must re-read state before acting on
   anything.

## Storage

- Local store is the default; cloud is an explicit opt-in with clear sync semantics.
- Store layout is versioned; migrations are additive and reversible.
- Corruption is detected (checksums) and reported; silent repair is forbidden — a corrupt store fails closed.

## Conformance

Vectors cover: canonical observation serialization, topic-key upsert behavior, structured-content validation, immutable-history enforcement, stale-read marking, and the frozen-for-0.1 revision/idempotency semantics above.
