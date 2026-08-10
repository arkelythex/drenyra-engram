# Drenyra Engram — Architecture

> How the engine is designed: position, trust model, the fiscal memory
> pipeline, the non-authorization boundary, and the storage layout. Contract-
> level detail lives in [contracts/](../contracts/README.md); the ADR trail
> lives in [docs/decisions/](../decisions/).

## Position in the ecosystem

Drenyra Engram is the **institutional accounting memory** layer of the Drenyra
ecosystem. It is standalone and headless: a single Go binary that any MCP
agent can talk to. It has **no dependencies** on Drenyra, Drenyra AI, or
Drenyra Pi, and it **never authorizes operations** in any of them.

```text
                    ┌─────────────────────────────┐
   agents (MCP) ───►│  Drenyra Engram (Go binary) │
   professionals ──►│  scope-first SQLite memory  │
                    │  Ed25519 receipts + verify  │
                    │  WORM evidence objects      │
                    └──────────────┬──────────────┘
                                   │ records & explains
                                   ▼
                    deployment-selected ledger (ERP / Drenyra)
                    — the source of truth, never this repository
```

## Trust model

Three parties, three distinct roles — never conflated:

| Party | Role | Bound |
|---|---|---|
| **Agent** | Observes and **proposes** | Can create `pending_review` memory, propose judgments, link evidence — can never approve, confirm, or impersonate a human |
| **Professional** | **Validates** | Authenticated principal (ADR-003); approves exactly the envelope they reviewed; SoD forbids approving their own proposal |
| **Engine** | **Records and explains** | Immutable revisions, signed receipts, offline verification — asserts provenance, never accounting correctness |

## The fiscal memory pipeline

```text
observation ──► pending_review (human gate) ──► approved
      │                 │                            │
      │            reject / return                 institutional
      │            (with reason)                    memory
      ▼                                            │
   evidence objects (WORM) ◄── evidence links       │
   rules with vigencia ◄── rule links (pinned)      ▼
                                             reconstruction:
                                        balance → evidence → rule →
                                        approval → offline verify
```

Every stage is auditable: saves, approvals, rejections, returns, judgments,
reconciliations, closes, object stores, retention binds, purges — each emits
an immutable event and an Ed25519-signed receipt.

## The non-authorization boundary

**Drenyra Engram does not authorize operations.** The approval gate is the
professional review of a *memory*, never authorization of a declaration,
payment, or filing. There is no `authorize`/`approve`/`allow`/`execute` tool,
no journal-entry posting, no SUNAT filing surface. Every verification report
ends with exactly **"Accounting correctness: NOT ASSERTED"**.

## Storage layout

```text
SQLite (single file)
├── observations      immutable memory revisions (scope-indexed)
├── evidence_links / rule_links      post-write links (structured pins)
├── approval_events / judgment_events / memory_decision_events   immutable acts
├── receipts          Ed25519-signed, per-subject hash chain
├── evidence_objects  content-addressed object metadata (WORM)
├── retention_policies / evidence_holds / purge_*    evidence lifecycle
└── review_velocity_events   anti-rubber-stamp signals

objects/              WORM bytes: objects/<ab>/<cd>/<sha256>
signing-keys.json     user-only Ed25519 keyring (0600)
```

The single-writer discipline (`MaxOpenConns(1)`) makes every
read-modify-write sequence atomic on one SQLite connection; approvals and
decisions run inside `BEGIN IMMEDIATE` transactions.

## Surfaces

One engine, three adapters: **CLI** (scripts, humans), **MCP** (agents, 57
tools), **HTTP** (programs). The MCP catalog has no authorization tools; the
HTTP approval routes are authenticated; the CLI `approve`/`review reject`/
`review return` require a session. Scope-first reads never require a
principal — reads never authorize.

## Key architectural decisions (ADR trail)

- **ADR-001** — standalone Go engine (TS reference retired by parity)
- **ADR-002** — local-first SQLite; PostgreSQL is the ecosystem's authoritative store
- **ADR-003** — authenticated ApprovalPrincipal (identity never caller-declared)
- **ADR-004** — Apache-2.0 open-source licensing
- plus the per-milestone architecture docs under [docs/architecture/](../architecture/)
