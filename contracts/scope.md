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

    ## v1 scope contract — identity→scope binding (scope-param-rollout, effective pre-v1 0.x)

    > Version: 0.3 · Status: frozen-for-v1 · Scope-param-rollout slice 3 (AC-SPR-5).

    When identity is present, the **effective scope is the explicit scope parameters
    validated against the authenticated principal's membership scope** — exact-match
    semantics, no fallback, no rewrite (FD-SPR-1). The binding is enforced at the
    adapter boundary (HTTP, MCP, CLI) only; it grants no authority, it constrains
    already-authenticated callers' scope.

    1. **Exact-match binding (FD-SPR-1).** A verified approval principal's effective
       company scope MUST equal its membership scope: `organizationId ==
       principal.tenantId` and `companyId ∈ principal.companyScopes`. On mismatch the
       call fails closed BEFORE the operation with the typed denial
       `TENANT_SCOPE_MISMATCH` / `COMPANY_SCOPE_DENIED` (FD-SPR-5) — the scope is
       never silently rewritten to the principal's scope.
    2. **Reference mode is caller-asserted (FD-SPR-3).** Unauthenticated surfaces
       (shared-token / session-less reference calls) keep the pre-existing
       caller-asserted scope derivation: the caller declares the scope and the
       store's exact-scope assertions are the isolation boundary. Reference mode is
       never a substitute for identity.
    3. **Institutional is orthogonal (FD-SPR-4).** The binding applies only to
       company-kind effective scopes; institutional requests pass through unchanged
       (byte-identical with and without a principal).
    4. **Multi-company principals (FD-SPR-2).** A principal whose membership carries
       a set of companies is bound against the full allowed set; expanding the set
       (e.g. multi-company scopes beyond the current exact tuple) is a future
       extension, not a contract change.
    5. **Store boundary (NFR-SPR-5).** The adapter binding is defense-in-depth at
       the boundary. Direct Go callers of the canonical API are protected by the
       store-level exact-scope assertions, which remain the authoritative isolation
       mechanism and are unchanged by this contract.

    Period is not an identity dimension: a principal's membership carries the
    company, not the fiscal period. The binding therefore compares
    organization/company only; the period stays part of the exact effective scope
    as before.
