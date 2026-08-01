# Contract: scope

> Version: 0.1-draft · Status: draft · Transport-agnostic.

Defines **company/RUC/period scoping** — the structural tenant-isolation model of the memory engine.

## Scope

Every observation carries a fiscal scope:

| Field    | Description                                                |
| -------- | ---------------------------------------------------------- |
| `company`| Company or organization identifier                         |
| `ruc`    | Peruvian RUC (checksummed where applicable)                |
| `period` | Fiscal period(s) `YYYYMM` or range                          |
| `kind`   | `company` (scoped) · `institutional` (cross-company, explicit) |

## Rules

1. **Scope-first search.** Queries filter by company/RUC/period *before* ranking. Scope is never a post-filter.
2. **No cross-scope leakage.** A scoped observation is invisible to queries in another company unless the query explicitly declares cross-scope intent (and policy allows it).
3. **Institutional knowledge is explicit.** Cross-company knowledge must be declared `institutional`; undeclared cross-scope access is a defect.
4. **Scope is enforced in every surface.** MCP, HTTP, CLI, TUI, and search share the same scope filter — no surface bypasses it.
5. **Scope is part of identity.** Two observations differing only in scope are different observations.

## Conformance

Vectors cover: scope-first ranking order, cross-company invisibility, institutional declaration, surface parity, and scope-in-identity.
