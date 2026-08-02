# ADR-001 — Engine strategy: TS reference package now, standalone Go daemon later

> Status: **accepted**

## Context

Drenyra Engram is the institutional accounting memory counterpart of Engram:
persistent observations, mission summaries, learned policies, professional
judgments, relations, vigencia, and provenance — searched scope-first
(company/RUC/period) over MCP, HTTP, CLI, and TUI, with local and cloud storage.

Building the full surface inside a TypeScript package today would entangle the
engine with one runtime and one transport, and would make the destination
architecture — a standalone engine serving many client languages — harder to
reach. The contracts must therefore be the durable surface, not the TS code.

## Decision

Long term, the engine is a **standalone Go daemon/binary** — the "Engram"
pattern:

- **Local storage:** SQLite; PostgreSQL/cloud optional.
- **Surfaces:** CLI + MCP + HTTP + TUI.
- **Clients:** TypeScript, Go, and Python libraries speaking the engine's API.

**v0.1 ships as a TypeScript package** with three parts:

1. `contracts/` — the transport- and implementation-agnostic contract set
   (memory, scope, lifecycle, provenance).
2. An in-memory reference implementation of those contracts.
3. A PostgreSQL adapter (the transactional authority — see ADR-002).

The contracts stay transport-agnostic precisely so the Go migration is possible
without renegotiating semantics.

## Consequences

- `contracts/` is the **frozen surface**: public behavior is defined there, not in
  the TypeScript implementation.
- The TypeScript implementation is the **reference, not the destination**; it can
  be rewritten or retired once the Go daemon reaches contract parity.
- The semantics marked **frozen-for-0.1** in each contract carry over unchanged to
  the Go engine.
- No consumer-facing decision is made against TypeScript internals; transports,
  clients, and the daemon boundary are designed against `contracts/`.
