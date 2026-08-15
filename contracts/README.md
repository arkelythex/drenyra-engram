# Drenyra Engram — Contracts

> **Status: draft (pre-alpha) with a frozen-for-0.1 subset.** These contracts define the memory engine's public surface. Engine/storage decisions are frozen by [ADR-001](../docs/decisions/ADR-001-engine-strategy.md) (engine strategy) and [ADR-002](../docs/decisions/ADR-002-storage-authority.md) (storage authority). The bullets marked **frozen-for-0.1** in each contract are normative; the rest stay draft until Phase 1 of the [ROADMAP](../ROADMAP.md) completes.

## Index

| Contract                    | Version | Status | Governs                          |
| --------------------------- | ------- | ------ | -------------------------------- |
| [approval](approval.md)     | 0.4.0   | Frozen (Step 1) | Authenticated professional approval: verified principal, atomic H1/H2 transition, versioned policy |
| [closing](closing.md)       | 0.5.0   | Frozen | Monthly close (cierre) and the enforceable period gate |
| [judgment](judgment.md)     | 0.4.0   | Frozen (Step 2) | Adjudicable conflicts: propose/confirm/reject/withdraw/supersede, principal-only confirmation |
| [lifecycle](lifecycle.md)   | 0.5     | Frozen-for-0.5 | Observation lifecycle machine + vigencia |
| [memory](memory.md)         | 0.2     | Frozen-for-0.2 | AccountingMemory observation model and storage |
| [period-comparison](period-comparison.md) | 0.5.0 | Frozen | Period-over-period comparison + session context (DRENYRA_DEFAULT_SCOPE) |
| [provenance](provenance.md) | 0.2     | Frozen-for-0.2 | Audit metadata + non-authorization boundary |
| [receipts](receipts.md)     | 0.4.0   | Frozen (Step 3) | Ed25519 action receipts + signing-key lifecycle |
| [reconciliation](reconciliation.md) | 0.5.0 | Frozen | First-class adjudicated reconciliations |
| [scope](scope.md)           | 0.2     | Frozen-for-0.2 | Company/RUC/period scoping, cross-tenant isolation |
| [verification](verification.md) | 0.4.0 | Frozen (Step 4) | Offline verification layers; "Accounting correctness: NOT ASSERTED" |

## Contract requirements

1. **Versioned.** Every contract declares `version` and a compatibility policy.
2. **Scope-first.** Company/RUC/period scoping is structural, not a post-filter.
3. **Provenanced.** Every observation carries who/what/when/why at creation.
4. **Non-authorizing.** No contract, API, or storage layer grants or implies authorization.

## Reference implementation

The first verifiable vertical (memory core + scope-first search, pre-alpha, zero
runtime dependencies) lives under the repository root:

| Concern             | Implementation                                      |
| ------------------- | --------------------------------------------------- |
| Core model          | [`core/types.ts`](../core/types.ts)                 |
| In-memory store     | [`store/memory-store.ts`](../store/memory-store.ts) |
| Scope-first search  | [`search/scope-first.ts`](../search/scope-first.ts) |
| Lifecycle           | [`lifecycle/transitions.ts`](../lifecycle/transitions.ts) |
| Non-authorization   | [`authority/boundary.ts`](../authority/boundary.ts) |
| Public API          | [`index.ts`](../index.ts)                           |

The mandatory cross-tenant isolation conformance test lives in
[`search/__tests__/scope-isolation.test.ts`](../search/__tests__/scope-isolation.test.ts)
and runs with `bun run test`.

## Frozen engine/storage decisions

[ADR-001](../docs/decisions/ADR-001-engine-strategy.md) — **accepted**: the
standalone Go daemon is the long-term engine, but v0.1 ships as a TypeScript
package. `contracts/` is the frozen surface; the TypeScript implementation is the
**reference, not the destination**.

[ADR-002](../docs/decisions/ADR-002-storage-authority.md) — **accepted**:
PostgreSQL is the transactional authority in the v0.1 integration path; the
in-memory store is a reference/development adapter and is **never canonical**;
local SQLite arrives only in v0.2+ with the standalone engine.
