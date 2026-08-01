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

## Storage

- Local store is the default; cloud is an explicit opt-in with clear sync semantics.
- Store layout is versioned; migrations are additive and reversible.
- Corruption is detected (checksums) and reported; silent repair is forbidden — a corrupt store fails closed.

## Conformance

Vectors cover: canonical observation serialization, topic-key upsert behavior, structured-content validation, immutable-history enforcement, and stale-read marking.
