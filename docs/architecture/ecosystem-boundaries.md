# Ecosystem Boundaries — Drenyra Engram (Institutional Accounting Memory)

> **Last updated:** 2026-08-01.

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents; no float is ever used for money; version/sequence numbers are JSON integers, never floats.

## Role in the ecosystem

Drenyra Engram is the **Institutional Accounting Memory**: scope-first memory for companies, fiscal periods, policies, and institutional knowledge. It is the direct accounting-domain counterpart of Engram.

Drenyra Engram is **independent**: it has no dependencies on Drenyra, Drenyra AI, or Drenyra Pi, and the other three may read from it. It preserves what the organization knows and can prove about its accounting — **remember is not authorize**.

## What Drenyra Engram is (in scope)

- **Observations** — structured memories with type, scope, topic key, and content.
- **Institutional knowledge** — policies, conventions, and precedents that outlive sessions.
- **Mission summaries and learnings** — persisted for cross-session recovery.
- **Relations** — `related`, `supersedes`, `conflicts_with`, and more between memories.
- **Vigencia** — effective/expiry semantics so stale knowledge is visible, not silently trusted.
- **Provenance** — who/what/when/why for every observation, auditable.
- **Scope-first search** — company/RUC/period filters are structural, not post-filters.
- **Local and cloud memory** — with clear, tombstone-aware sync semantics.
- **Surfaces** — MCP, HTTP, CLI, TUI over the same engine.

## Explicit non-goals

Drenyra Engram is **not**:

- An authorization engine. It has no authority model; no observation is ever approval, permission, or authorization.
- A receipt engine. Receipts, gates, and approvals belong to `drenyra-ai`.
- A product UI or Pi harness. Those belong to Drenyra and `drenyra-pi`.

## What Drenyra Engram must NOT contain long-term

- **Authorization, receipts, or gates** → `drenyra-ai`. Cross-consumer approvals route through Drenyra AI gates and human professionals, never through memory.
- **Product surfaces** (UI, tenants, documents, accounts, SUNAT flows) → Drenyra.
- **Pi harness logic** → `drenyra-pi`.
- **Cloud offering** → deferred to `arkelythex/drenyra-cloud`.

## Consumers and producers

| Direction | Party | Relation |
| --------- | ----- | -------- |
| Read by | Drenyra | institutional memory in fiscal workflows (never authorizes) |
| Read by | `drenyra-ai` | optional memory integration (never authorizes) |
| Read by | `drenyra-pi` | context threading, memory reads (never authorizes) |
| Produces for | all consumers | scoped, provenance-backed, vigencia-aware knowledge |

## Current state and maturity

- Pre-alpha: contracts drafted (memory, scope, lifecycle, provenance); memory core defined by the contracts; store, search, relations, and surfaces are planned in the ROADMAP.
- Independent deployability: local and cloud memory work with no ecosystem components present.

## Ownership and accountability

- Memory contracts, lifecycle, provenance, and sync semantics: this repo.
- Authorization, receipts, and gates: `drenyra-ai`. Product: Drenyra. Pi harness: `drenyra-pi`.
- A defect in memory behavior is filed here; a defect in how a consumer reads memory belongs to that consumer.

## Boundary enforcement

- The **non-authorization boundary** is the defining invariant: it is enforced in review, in contracts, and in the API — nothing in this repo grants or implies authorization.
- Direction violations are caught in review: a PR that imports Drenyra, Drenyra AI, or Drenyra Pi types into Engram is rejected.
