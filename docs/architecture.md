# Drenyra Engram — Architecture

> **Last updated:** 2026-08-10. This is the canonical boundary document the
> contracts reference; the design overview lives in
> [docs/ARCHITECTURE.md](ARCHITECTURE.md).

## Position in the ecosystem

Drenyra Engram is the **institutional accounting memory** layer of the Drenyra
ecosystem. It is **independent**: it has no dependencies on Drenyra, Drenyra
AI, or Drenyra Pi, and it never authorizes operations in any of them. The
deployment-selected ledger (ERP / Drenyra) remains the source of truth — this
repository records and explains memory, it is not a ledger.

## Non-authorization boundary

This is the defining invariant of the engine:

```text
Memoria orienta.      Memory guides.
Política restringe.   Policy restricts.
Evidencia demuestra.  Evidence demonstrates.
Receipt certifica.    Receipt certifies.
Profesional autoriza. A professional authorizes.
```

- The engine stores and retrieves institutional accounting knowledge. It does
  **not** authorize business operations.
- The engine DOES implement authenticated professional review (ApprovalPrincipal,
  ADR-003), immutable act receipts (Ed25519), judgments, reconciliations, and
  offline integrity verification. Those certify **Engram's own state
  transitions** — never accounting correctness, never permission to execute an
  external action.
- There is no `authorize`/`approve`/`allow`/`execute` tool; there is no
  journal-entry posting and no SUNAT filing surface. Every verification report
  ends with exactly **"Accounting correctness: NOT ASSERTED"**.
- Cross-consumer business authorization routes through the owning application
  policy and human professionals.

## Core model

```text
AccountingMemory (v2)
├── identity: id, topicKey (stable), scope (org/company/RUC/period)
├── kind: fact · evidence · decision · rule · exception · control · obligation · summary
├── content: What / Why / Where / Learned
├── fiscalEffect: none · journal_entry · declaration · closing · adjustment · reclassification · approval · sunat_filing
├── status: active · pending_review · approved · rejected · returned · voided · superseded
├── timestamps: effectiveAt (accounting) · recordedAt (system) · observedAt (detected)
├── materiality: threshold (whole cents) + level (normal/material/critical)
├── vigencia: Validity{effectiveAt, expiresAt, source} — half-open windows
├── hashes: contentHash · identityHash · envelopeHash (status participates)
└── provenance: actor, timestamp, source, session

Relations (17): supports · contradicts · explains · reconciles · reverses ·
approved_by · supersedes · related · compatible · scoped · conflicts_with ·
not_conflict · and the judgment/evidence vocabulary
```

Lifecycle: `save(none) → active` · `save(fiscal) → pending_review` (human
gate) → `approved | rejected | returned`; `active|approved → voided|superseded`
(terminal); `returned` is non-terminal (a Save re-enters `pending_review`).

## Layer model

```text
mcp / http / cli                      surfaces (adapters — never invent authority)
        │
review / rules / judgments / search  domain services
        │
core                                  pure domain model + verification layers
        │
store                                 SQLite (immutable history, single-writer)
        │
objects/                              WORM evidence bytes (content-addressed)
```

- **Scope is structural.** `organization/company/RUC/period` lives in the
  schema and in every query — a company-A query can never see company-B
  memory (a tested invariant, not a filter).
- **Surfaces are adapters.** MCP, HTTP, and CLI exercise the same domain
  services; the principal is derived only from the session (ADR-003).
- **History is immutable.** A correction is a new revision linked via
  `supersedes`; evidence and rule links grow only through link records.
- **Deterministic behavior is tested.** Lifecycle transitions, relation
  judgments, search ranking, approval policy, and receipts ship with
  conformance vectors and Go↔TS golden parity.

## Sync

Local stores sync additively: full revision history, relations, and the
lifecycle audit trail cross with original ids/provenance. Divergence is
**surfaced, never silently resolved** — divergent chain heads are preserved in
both stores and linked with a `conflicts_with` relation. Cloud sync is
deferred (ROADMAP non-goals).

## Repository scope

This repo is the memory engine and contains professional approval, judgment,
receipt, verification, review-workspace, fiscal-policy, and evidence-lifecycle
primitives for its own immutable state transitions. It does **not** authorize
external business operations, provide the product UI (that is
`arkelythex/drenyra-app-web`), or provide the Pi harness (that is `arkelythex/drenyra-pi`).
The owning application remains responsible for business authorization and
external side effects.
