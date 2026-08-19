# Proposal — scope-param-rollout

> Phase: propose · Artifact: proposal · Status: draft
> Strict TDD: not active in this phase (planning only); strict TDD applies during apply and verify.

## Problem statement

Block Q (audit-register-closure) delivered a scope-parameterized canonical API and the exhaustive cross-tenant matrix, but the v1 adapter surfaces still derive scope from **caller-asserted inputs** that are never bound to the authenticated principal's membership:

- **HTTP:** `httpQueryScope` builds the effective scope entirely from query parameters (`?ruc=&period=&companyId=&organizationId=`). `requireToken` / `authenticate` prove *who* the caller is (session or OIDC bearer), but no generic read handler cross-checks the query scope against the resolved principal's membership scope. A valid token for company A can read company B's memories by passing B's RUC in the query string.
- **MCP:** the server binds one operator-configured `DRENYRA_DEFAULT_SCOPE` at construction; there is no per-request principal→scope binding.
- **CLI:** `cliCompanyScope` derives the scope from `--company/--period` flags (caller-asserted); a session principal's membership is not used to constrain them.

Per AC-Q-5 (audit-register-closure verify-report): *"adapter scope is caller-asserted; identity-to-scope binding is a production identity prerequisite (block I / issue #18)."* Issue #18 delivered the identity layer — `internal/auth/resolver.go` resolves a verified principal whose OIDC claims (`tenantId`/`companyId`) are cross-checked against **active DB membership** — but that membership scope is **not yet enforced at the adapter boundary**. Authentication today proves identity, not *which scope the identity may access*.

This matters because tenant isolation at the v1 boundary currently rests on the caller being honest about the RUC in the query string. That is acceptable for an unauthenticated reference surface, but it is not a production isolation guarantee once a token or session is in play.

## Proposed change

Enforce **identity→scope binding** at every adapter surface, pre-v1, as a documented breaking change.

### 1. Define the v1 scope contract

- Every surface's **effective scope** = the explicit scope parameters (query/body/env/flag) **intersected with** the authenticated principal's allowed membership scope.
- For principals whose membership carries one company (`VerifiedApprovalPrincipal.AllowedCompanyIDs`, resolved from the DB membership tuple `(subject, tenant, company)`), the surface scope MUST match that company scope (tenant/company/RUC/period) exactly; mismatch **fails closed** (`SCOPE_DENIED`, never a fallback to the caller's value).
- Unauthenticated surfaces stay explicitly caller-asserted and documented as such (reference mode only).

### 2. HTTP adapter

- `requireToken` / `authenticate` already resolve a `VerifiedApprovalPrincipal`. Add a scope-binding step: the `httpQueryScope`-derived scope must be validated against the principal's membership scope before the handler proceeds; a mismatch returns `SCOPE_DENIED`.
- Institutional surfaces (`kind=institutional`) remain explicit opt-in and are not granted by identity binding.
- Keep the existing route contract (all generic reads already carry scope query params) — the change is enforcement, not a new parameter shape.

### 3. MCP adapter

- When an identity context is present, validate the effective scope against the principal's membership scope; fail closed on mismatch.
- Keep `DRENYRA_DEFAULT_SCOPE` as the operator-level default for the unauthenticated/embedded case, and document that identity-present calls must match the principal's scope.

### 4. CLI

- When a session principal is present, `cliCompanyScope` must validate the derived scope against the principal's membership scope; mismatch fails closed.
- Session-less CLI operation stays caller-asserted (reference mode).

### 5. Tests

- Extend the cross-tenant matrix (or add a focused identity-bound matrix) with: principal-for-A + query-scope-B → `SCOPE_DENIED` on HTTP, MCP, and CLI; principal-for-A + query-scope-A → allowed; institutional surfaces unaffected.
- Keep `TestCrossTenantMatrixExhaustiveness` green (no API surface removal; adapter-internal binding).
- Add auth-scope mismatch tests at the service/adapter boundary (HTTP middleware, MCP tool wrapper, CLI command).

## Alternatives considered

- **Token-only binding (claims as scope):** rely on the OIDC claims being correct and ignore query params. Rejected: the claims are validated against DB membership, but the caller could still pass a different RUC; the query surface must be constrained, not ignored.
- **Scope in the token only, no query params:** remove scope query params entirely and derive scope from the principal. Rejected: breaks the documented reference-mode surface and the unauthenticated case; the breaking change would be larger than needed pre-v1.
- **Keep caller-asserted scope (status quo):** rejected — it is the exact gap AC-Q-5 flags, and it leaves authenticated tenant isolation to caller honesty.

## Risks

- **Membership model ambiguity:** a principal may hold multiple membership rows across companies. Mitigation: the verified principal currently carries one company scope; the proposal freezes exact-match semantics on that scope and documents multi-company principals as a future extension (explicit allowed-set).
- **Breaking-change blast radius:** existing HTTP/MCP/CLI callers that pass a scope differing from their principal will break. Mitigation: strict enforcement lands pre-v1 (0.x) per the documented follow-up; the reference/unauthenticated surface keeps caller-asserted behavior.
- **Institutional surface interplay:** identity binding must not accidentally grant or revoke institutional opt-in. Mitigation: institutional handling stays explicit and orthogonal.
- **Test surface growth:** identity-bound matrices add cases to an already large suite. Mitigation: reuse the existing cross-tenant fixture helpers and keep the binding tests at the adapter boundary.
- **False assurance:** enforcement at the adapter does not protect the store against direct Go callers. Mitigation: keep `RelationsForScope`/store-level scope assertions (already hardened) and state the boundary precisely.

## Rollback

- Adapter-level enforcement is a behavior change in HTTP/MCP/CLI adapters only; reverting the binding restores caller-asserted behavior without schema, contract, or data changes.
- The v1 scope-contract documentation can be reverted independently.
- Store-level scope assertions (`RelationsForScope` to_id hardening) stay regardless — they are defense-in-depth, not part of this rollout.
- No schema change and no authz-policy change is planned.

## Delivery shape and review workload forecast

Bounded adapter + test change; no store or schema work.

- **Changed-line forecast:** likely **under 400 lines** (HTTP/MCP/CLI binding helpers + focused identity-bound matrix cases).
- **Recommended slices:**
  1. HTTP identity→scope binding + matrix extension;
  2. MCP + CLI binding + cross-surface consistency tests;
  3. v1 scope-contract documentation + breaking-change note in the capabilities manifest.
- Each slice carries its own focused tests and stays reviewable; the cross-tenant exhaustiveness guard remains green throughout.

## Acceptance outlook

This proposal succeeds when: a valid principal for company A with a query/flag scope for company B **fails closed** with `SCOPE_DENIED` on every adapter surface; the matching A-scope call succeeds unchanged; the unauthenticated reference surface is unchanged and documented; the v1 scope contract is written; and the existing cross-tenant matrix + exhaustiveness guard stay green.
