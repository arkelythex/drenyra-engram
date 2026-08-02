# ADR-002 — Storage authority: PostgreSQL is the transactional authority in v0.1

> Status: **accepted**

## Context

The memory store must never lose or silently mutate an observation: revision
history and provenance are audit-grade. An in-memory store is fast but ephemeral
and process-local, so it cannot be canonical for institutional memory.

## Decision

- **PostgreSQL is the transactional authority** in the v0.1 integration path.
  Writes are committed against it and reads are served from it; the PostgreSQL
  adapter is where durability and the fail-closed corruption behavior live.
- The **in-memory store is a reference/development adapter** — it exists to
  exercise the contracts and the conformance suite, and it is **never canonical**.
- The standalone engine (ADR-001) adds **local SQLite only in v0.2+**; SQLite is
  not introduced in v0.1 so there is a single transactional authority to harden.

## Consequences

- Contract conformance runs against the in-memory adapter; integration
  conformance runs against PostgreSQL.
- A consumer must never treat the in-memory store as durable authority.
- The migration policy (provenance.md frozen semantics) is enforced by the
  PostgreSQL adapter from the first integration slice.
- SQLite in v0.2+ inherits the same frozen semantics and fail-closed corruption
  behavior, so the authority change is an adapter swap, not a semantic change.
