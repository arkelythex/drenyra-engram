# Drenyra Engram — Contracts

> **Status: draft (pre-alpha).** These contracts define the memory engine's public surface. Nothing is frozen until Phase 1 of the [ROADMAP](../ROADMAP.md) completes.

## Index

| Contract                    | Version | Status | Governs                          |
| --------------------------- | ------- | ------ | -------------------------------- |
| [memory](memory.md)         | 0.1-draft | Draft | Observation model and storage    |
| [scope](scope.md)           | 0.1-draft | Draft | Company/RUC/period scoping       |
| [lifecycle](lifecycle.md)   | 0.1-draft | Draft | Observation lifecycle + vigencia |
| [provenance](provenance.md) | 0.1-draft | Draft | Audit metadata + non-authorization boundary |

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
