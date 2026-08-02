# Dependency Direction — Drenyra Engram (Institutional Accounting Memory)

> **Last updated:** 2026-08-01.

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents; no float is ever used for money; version/sequence numbers are JSON integers, never floats.

## Ecosystem dependency graph

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

Arrows point toward the dependency. Drenyra Engram sits at the top of the graph: the other three read from it, and **nothing flows into it as a dependency**.

## Direction rules applied to Drenyra Engram

### Drenyra Engram MAY depend on

**Nothing ecosystem-internal.** Drenyra Engram is independent by design. It has no dependencies on Drenyra, Drenyra AI, or Drenyra Pi.

### Drenyra Engram must NEVER be depended on

Drenyra Engram is *meant* to be read by the others — that is its role. The rule that protects it is the reverse: the other three never modify it, and none of their types or contracts ever leak into it.

### Who may never depend on Drenyra Engram in a way that defines it

- Drenyra, `drenyra-ai`, and `drenyra-pi` read memory through Engram's surfaces (contracts, MCP, HTTP, CLI).
- They never import Engram internals, and they never treat a memory observation as authorization.

## Rules in practice

1. Engram's contracts are the public surface: `memory`, `scope`, `lifecycle`, `provenance` — versioned and transport-agnostic.
2. Consumers read through the same domain services; there are no consumer-specific back doors.
3. Approvals never flow through Engram: consumers route them through `drenyra-ai` gates and human professionals.
4. Engram may be deployed independently (local and/or cloud) with no ecosystem components present.

## Why this matters

Memory must stay neutral to be trusted. If Engram depended on the ecosystem or carried authority, its knowledge would be advocacy, not evidence.
