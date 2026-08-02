# Releasing — Drenyra Engram

> **Last updated:** 2026-08-01.

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents; no float is ever used for money; version/sequence numbers are JSON integers, never floats.

## Version policy

- Until the first contract is frozen, releases use **`0.0.1-prealpha.x`** (x increments per release).
- The first release that freezes a contract (`memory`, `scope`, `lifecycle`, `provenance` — per the ROADMAP's Phase 1) is **`0.1.0`**.
- After `0.1.0`, **Semantic Versioning** applies: MAJOR = breaking contract change, MINOR = backward-compatible addition, PATCH = backward-compatible fix. Contract changes are public surface changes.

## Release checklist

Every release must pass, in order:

1. **Typecheck** — the repo's configured typecheck is clean.
2. **Tests** — full test suite passes.
3. **Conformance vectors** — deterministic behavior (search ranking, lifecycle transitions, relation judgments, scope filtering) passes against canonical vectors from the exact release candidate.
4. **Package build + pack verification** — build and verify the packed artifact contains exactly the intended files.
5. **Packed-install test** — install the packed tarball in a clean consumer and run a smoke command (e.g. `drenyra-engram save` + `search` in an isolated scope) to prove the release works outside the checkout.

## Commit and release discipline

- **Conventional Commits** (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`), with scope when useful.
- **No AI attribution** in commit messages or release notes — no "Generated with" or "Co-Authored-By" AI markers.
- Contract changes are high-materiality: proportional risk review before publish.
- The non-authorization boundary is part of the contract surface and must survive every release unchanged in meaning.
