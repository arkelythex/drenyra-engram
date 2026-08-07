# Drenyra Engram — Architecture

> **Last updated:** 2026-08-07.

## Position in the ecosystem

```text
                    ┌───────────────────┐
                    │ Drenyra-Engram    │
                    │ Accounting Memory │
                    └─────────▲─────────┘
                              │
                ┌─────────────┴─────────────┐
                │                           │
       ┌────────┴────────┐        ┌─────────┴─────────┐
       │ Drenyra-AI      │        │ Drenyra-Pi       │
       │ Agent Ecosystem │◄───────│ Pi-native Harness│
       └────────▲────────┘        └───────────────────┘
                │
       ┌────────┴────────┐
       │ Drenyra         │
       │ Command Center  │
       └─────────────────┘
```

Drenyra Engram is **independent**: it has no dependencies on the other three, and the other three may read from it.

## Non-authorization boundary

This is the defining invariant of the engine:

```text
Memoria orienta.      Memory guides.
Política restringe.   Policy restricts.
Evidencia demuestra.  Evidence demonstrates.
Receipt certifica.    Receipt certifies.
Profesional autoriza. A professional authorizes.
```

- The engine stores and retrieves knowledge. It does not authorize business operations.
- The engine does implement authenticated professional review, immutable act receipts, and offline integrity verification; those certify Engram's own state transitions, not accounting correctness or permission to execute an external action.
- No observation is ever treated as approval or authorization by a consumer. Cross-consumer business authorization must route through the owning application policy and human professionals.

## Core model

```text
Observation
├── id, title, type, scope (company/RUC/period)
├── topic_key      stable key for evolving knowledge
├── content        structured: What / Why / Where / Learned
├── lifecycle      draft → reviewed → promoted → superseded
├── vigencia       effective / expiry windows
└── provenance     actor, timestamp, source, session

Relations:  related · compatible · scoped · conflicts_with · supersedes · not_conflict
```

## Layer model

```text
mcp / http / tui / cli / clients      surfaces (adapters)
        │
search/relations/lifecycle            domain services
        │
core                                  memory model
        │
store                                 persistence (local + cloud)
```

- **Scope is structural.** `company/RUC/period` lives in the observation schema and in search indexes; every query filters scope before ranking.
- **Surfaces are adapters.** MCP, HTTP, TUI, and CLI exercise the same domain services.
- **Provenance is written at creation** and cannot be silently rewritten; a correction is a new observation linked via relations.
- **Deterministic behavior is tested.** Search ranking, lifecycle transitions, and relation judgments ship with conformance vectors.

## Sync

Local and cloud stores sync with explicit semantics: tombstone-aware, provenance-preserving, and conflict-visible (conflicts are surfaced for human/relation review — never silently resolved).

## Repository scope

This repo is the memory engine and contains professional approval, judgment, receipt, and verification primitives for its own immutable state transitions. It does **not** authorize external business operations, provide the product UI (that is `arkelythex/Drenyra`), or provide the Pi harness (that is `arkelythex/drenyra-pi`). The owning application remains responsible for business authorization and external side effects.
