# Contributing to Drenyra Engram

**Status: pre-alpha.** Drenyra Engram is extracted from `arkelythex/drenyra-command-center` (`packages/memory`, `packages/agent-memory`) through vertical slices. The maintainer (Arkelythex) drives the extraction; external contributions are welcome only after the contracts in `contracts/` stabilize.

## Ground rules

- **Memory never authorizes.** No code path may treat an observation as approval. That boundary is a product safety requirement.
- **Scope is structural.** Company/RUC/period scope is part of the memory model and search — never an afterthought post-filter.
- **Provenance is mandatory.** Every observation records who/what/when/why; forgeable provenance is a defect.
- **No secrets.** No credentials, tokens, or customer data in code, docs, tests, or seeds.
- **No `any`.** Use precise types, `unknown`, or justified generics.

## Workflow

1. Create a dedicated branch (or isolated worktree for medium/large changes).
2. Keep `main` clean.
3. Prefer small, verifiable, reversible changes. Split changes over 400 lines into chained PRs.
4. Update docs in the same PR as code (docs-as-code). Stale docs are a bug.
5. Add tests for changed behavior — scope-first search, lifecycle transitions, sync semantics, relation judgments.
6. Conventional commits only (no AI attribution).
7. The review gate rejects: silent error handling, production `console.log`, missing scope checks, missing provenance, missing tests, contract changes without docs.

## Contract changes

Any change to `contracts/` is a **public contract change**:

- Bump the affected contract version explicitly.
- Document the migration path.
- Get explicit approval — contracts are consumed by Drenyra, Drenyra AI, and Drenyra Pi.

## Getting help

Open an issue with a clear description. For security issues, use Private Vulnerability Reporting — see [SECURITY.md](SECURITY.md).
