# Contract: scope

> Version: 0.2 · Status: frozen-for-0.2 · Transport-agnostic.

Defines **company/RUC/period scoping** — the structural tenant-isolation model of the memory engine.

## Scope

Every memory carries a fiscal scope:

| Field    | Description                                                |
| -------- | ---------------------------------------------------------- |
| `company`| Company or organization identifier                         |
| `ruc`    | Peruvian RUC (checksummed where applicable)                |
| `period` | Fiscal period(s) `YYYYMM` or range                          |
| `kind`   | `company` (scoped) · `institutional` (cross-company, explicit) |

## Rules

1. **Scope-first search.** Queries filter by company/RUC/period *before* ranking. Scope is never a post-filter.
2. **No cross-scope leakage.** A scoped memory is invisible to queries in another company unless the query explicitly declares cross-scope intent (and policy allows it).
3. **Institutional knowledge is explicit.** Cross-company knowledge must be declared `institutional`; undeclared cross-scope access is a defect.
4. **Scope is enforced in every surface.** MCP, HTTP, CLI, TUI, and search share the same scope filter — no surface bypasses it.
    5. **Scope is part of identity.** Two memorys differing only in scope are different memorys.

## Frozen semantics (v0.1)

> The bullets in this section are **frozen-for-0.2**: normative, enforced by the
> conformance suite, and carried unchanged into the standalone Go engine
> (ADR-001). The rules above stay `0.1-draft` until the Phase 1 freeze review.

1. **Scope-first ordering is frozen.** Queries filter by company/RUC/period
   BEFORE any ranking. Scope is never a post-filter: a company-A memory can
   never be scored for a company-B query.
2. **Institutional opt-in is frozen.** Institutional memorys are surfaced to
   company-scoped queries only when the caller explicitly opts in
   (`includeInstitutional`); an institutional-scoped query surfaces them by
   default.
3. **The cross-tenant negative property is a REQUIRED conformance test.**
   Company A's memory is never retrievable from a company B query — under any
   match mode, limit, or ordering. The reference test lives in
   `search/__tests__/scope-isolation.test.ts` and runs in CI.

## Conformance

Vectors cover: scope-first ranking order, cross-company invisibility, institutional declaration, surface parity, scope-in-identity, and the frozen-for-0.2 semantics above (including the REQUIRED cross-tenant negative test).
