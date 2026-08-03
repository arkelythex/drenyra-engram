# ADR-003 — Multi-jurisdiction institutional knowledge (Peru -> LATAM)

> Status: **proposed** (Phase 3 of the engine ROADMAP; not yet implemented)
> Date: 2026-08-03

## Context

Drenyra Engram's scope model is **Peru-shaped**: `Scope{RUC (11 digits), period YYYYMM}`. The RUC format, validation, and the scope grammar are baked into `internal/core` (`IsValidRUC`, `IsValidPeriod`, `ScopeKey`), the contracts (`scope.md`), and the CLI/HTTP/MCP surfaces (`companyId = ruc` derivation).

Phase 3 of the ROADMAP targets **multi-jurisdiction institutional knowledge (Peru -> LATAM)**. Drenyra already ships `country-packs/` (peru, chile, colombia) with jurisdiction-specific fiscal rules; the memory engine must be able to scope observations for non-Peruvian jurisdictions (Chile RUT, Colombia NIT, Mexico RFC, Argentina CUIT, ...) without breaking the frozen scope-first isolation property.

## The constraint that matters

**Scope is identity.** The contracts freeze: "scope equality is exact; a perioded scope never matches an unperioded one" and "queries filter scope before ranking". Any multi-jurisdiction change must keep that invariant: an observation scoped to a Chilean RUT must be structurally invisible to a Peruvian RUC query and vice versa — under any query, any match mode.

The second frozen constraint: **the non-authorization boundary** — jurisdiction adds no authorization semantics; memory still never authorizes.

## Decision (options)

### Option A — Jurisdiction-tagged scope (recommended)

Add a `jurisdiction` field to the scope model (default `"pe"`), and generalize the company identifier from `ruc` to a jurisdiction-specific tax-id:

```text
Scope { kind: company, jurisdiction: "pe"|"cl"|"co"|..., organizationId, companyId, taxId, period }
```

- `ScopeKey` becomes `jurisdiction + companyId + taxId + period` — cross-jurisdiction isolation is structural, same as today.
- Validators become jurisdiction-aware: `ValidateTaxId("pe", id)` -> 11-digit RUC; `("cl", id)` -> RUT with check digit; unknown jurisdiction fails closed.
- The wire format keeps `ruc` as an ALIAS for `taxId` when `jurisdiction = "pe"` (backward compatible: existing observations and clients unchanged).
- Country-packs (Drenyra) own the jurisdiction tax-id rules; the engine asks a jurisdiction provider (or embeds a minimal validator table) rather than hardcoding.

**Pros**: one coherent scope model; isolation preserved by construction; backward compatible via the `ruc` alias; matches how country-packs already split per-jurisdiction rules.
**Cons**: touches the frozen contracts (`scope.md` must be amended with a migration note); the CLI/HTTP `--company <ruc>` derivation needs a jurisdiction flag or inference; `ScopeKey` changes mean existing stored scopes need a migration decision (additive: keep old key form for `pe`, new form for others).

### Option B — Separate namespaces (parallel models)

Keep the Peruvian model untouched; add a parallel `jurisdiction` field at the observation level (not in the scope), and a separate validation/search path per jurisdiction.

**Pros**: zero risk to the frozen Peruvian model.
**Cons**: two scope models = two isolation rules = drift risk; the exact-scope invariant becomes ambiguous across models; search/CLI/MCP surfaces double. Violates the "one engine" direction.

### Option C — Defer, keep Peru-only

Ship Phase 3 without jurisdiction changes; add multi-jurisdiction only when a non-Peruvian consumer exists.

**Pros**: zero cost now; no contract amendments.
**Cons**: the ROADMAP Phase 3 item stays open; the engine's scope model hardens further around Peru, making the later change more invasive (the frozen contracts are the durable surface).

## Recommendation

**Option A, staged**: (1) add the `jurisdiction` field (default `"pe"`, `ruc` alias preserved) as an additive contract amendment with a migration note; (2) generalize validation through a jurisdiction provider that falls back to a minimal embedded table (pe/cl/co) until country-packs define the rest; (3) keep the scope-first isolation and non-authorization invariants untouched and re-verify with conformance vectors per jurisdiction.

**v1.0 criterion**: two consumers running on the released contracts — Drenyra (first) + one more (drenyra-pi reading context, or a non-Peruvian adapter) — gates the v1.0 candidate regardless of jurisdiction.

## Consequences

- Contracts: `scope.md` gains a jurisdiction field + migration note; the frozen isolation ordering stays.
- `internal/core`: `Scope`/`ScopeKey`/validators become jurisdiction-aware (additive, fail-closed on unknown jurisdiction).
- CLI/HTTP/MCP: scope inputs accept an optional jurisdiction; `--company` stays RUC for `pe`.
- Country-packs: own the per-jurisdiction tax-id rules; the engine consumes them via a provider interface (no hardcoded non-Peruvian rules in the engine beyond a minimal fallback).
- Tests: conformance vectors per jurisdiction (isolation, validation, keying); the existing Peruvian vectors stay green unchanged.

## Open questions

1. Should `jurisdiction` be a 2-letter ISO code (`pe/cl/co/mx/ar`) or an enum with explicit labels?
2. Is the `ruc` alias on the wire worth the compat cost, or should the CLI migrate to `--tax-id --jurisdiction` with a deprecation window?
3. Who owns the jurisdiction provider — a new `internal/jurisdiction` package or a thin adapter to Drenyra country-packs?
