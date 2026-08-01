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
