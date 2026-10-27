# Spec — scope-param-rollout

> Phase: spec · Artifact: spec · Status: draft
> Inputs: proposal (required, owner-approved). Strict TDD: not active in this phase; strict TDD applies during apply and verify.
> Store: OpenSpec file-based (openspec/changes/scope-param-rollout/spec.md).

## Scope

Enforce **identity→scope binding** at every adapter surface (HTTP, MCP, CLI), pre-v1, as a documented breaking change: when an authenticated principal is present, the surface's effective scope MUST be validated against the principal's membership scope and MUST fail closed with the typed scope-denied outcome on mismatch — never a fallback to the caller's value. Authentication today proves *who* the caller is (`internal/auth/resolver.go` resolves a `VerifiedApprovalPrincipal` whose OIDC claims `tenantId`/`companyId` are cross-checked against active DB membership), but the v1 adapter surfaces still derive scope from caller-asserted inputs (`httpQueryScope`, `DRENYRA_DEFAULT_SCOPE`, `cliCompanyScope`) that are never bound to that principal. This change closes AC-Q-5's flagged gap ("identity-to-scope binding is a production identity prerequisite") at the adapter boundary only.

**IN** — identity→scope binding at the three adapter surfaces: HTTP generic reads (`internal/server/http.go`, `httpQueryScope` + principal in request context), MCP identity-present tool calls (`internal/server/mcp.go`, `handleToolsCall`), and CLI session-present scope-flag commands (`cmd/drenyra-engram/main.go`, `cliCompanyScope`); fail-closed typed `SCOPE_DENIED` on mismatch; unauthenticated/reference surfaces stay caller-asserted and are documented as reference mode; institutional surfaces (`kind=institutional`) stay explicit opt-in and orthogonal; the v1 scope contract (`contracts/scope.md`) and the capabilities manifest breaking-change note (`docs/ecosystem/capabilities-and-conformance.md`).

**OUT** — no schema change; no authorization-policy change (the frozen approval/judgment/reconciliation/evidence policies, versions, check order, and denial codes are untouched); no token revocation, MFA, or membership provisioning (OIDC follow-ups); no store-level change (`RelationsForScope` and store-level scope assertions stay exactly as hardened — defense-in-depth, not part of this rollout); no new API surface, route, tool, command, parameter shape, or error code; no change to `DRENYRA_DEFAULT_SCOPE` construction semantics.

## Requirements

### Frozen decisions (binding — design and apply MUST NOT deviate)

These freeze the proposal's semantics. Any later change requires a new proposal + spec amendment.

- `FD-SPR-1` — **Exact-match scope semantics on the principal's single company scope.** For a principal whose membership carries one company (`VerifiedApprovalPrincipal`, resolved from the DB membership tuple `(subject, tenant, company)`), the effective scope MUST exactly match that membership scope on the identity dimensions the principal carries: the scope's `organizationId` MUST equal `principal.TenantID()` and the scope's `companyId` MUST be inside `principal.CompanyScopes()` — the same comparison the approval policy already freezes at `authz/approval_policy.go:61-67`. The effective scope MUST remain an exact period-pinned company scope. There is NO intersection fallback: on mismatch the request fails closed with the typed scope-denied and is NEVER silently rewritten to the principal's scope.
- `FD-SPR-2` — **Multi-company principals = future allowed-set extension.** The verified principal currently carries exactly one company id (`[]string{membership.CompanyID}`). This change freezes exact-match semantics on that single scope and documents multi-company memberships as a future extension (an explicit allowed-set check of the same shape the frozen policy's `contains(p.CompanyScopes(), ...)` already anticipates). No multi-company membership provisioning or binding MAY be introduced by this change.
- `FD-SPR-3` — **Unauthenticated/reference surfaces keep caller-asserted behavior.** Surfaces or calls without an identity context (no session, no OIDC bearer, no bound principal) keep the existing caller-asserted scope derivation, unchanged, and MUST be documented as reference mode only — never claimed as a production isolation guarantee.
- `FD-SPR-4` — **Institutional opt-in untouched.** Identity binding applies ONLY to company-kind effective scopes. Institutional surfaces (`kind=institutional`) remain explicit opt-in exactly as today; the binding neither grants nor revokes institutional access and MUST NOT reject, rewrite, or special-case institutional requests.
- `FD-SPR-5` — **Typed denial reuses the frozen scope-denied codes.** The binding's typed `SCOPE_DENIED` (the proposal's contract-level name) MUST be expressed with the error codes already frozen in the repo — `auth.CodeTenantScopeMismatch` (`TENANT_SCOPE_MISMATCH`) when the scope's `organizationId` differs from the principal's tenant, `auth.CodeCompanyScopeDenied` (`COMPANY_SCOPE_DENIED`) when the scope's `companyId` is outside the principal's company scopes — mapped to HTTP 403 by the existing error envelopes (`authErrorHTTPStatus`, `internal/server/http.go:162-170`), and to typed error text on MCP/CLI per their existing envelopes. No new error code and no new HTTP status mapping MAY be introduced.
- `FD-SPR-6` — **Enforcement lands at the adapter seam, before the API call.** The binding validates the already-derived effective scope against the principal present in the request context and runs BEFORE any API/service/store call. It never re-derives scope, never resolves a principal where none exists, and never weakens store-level scope assertions.

### Functional requirements

- `FR-SPR-1` — **HTTP identity→scope binding on every generic read.** As an authenticated HTTP caller I want my query-derived scope bound to my verified membership so a valid credential for company A can never read company B's memories by passing B's RUC. Every HTTP handler that derives its effective scope via `httpQueryScope(r, false)` (`internal/server/http.go:1805` — Save, Get, GetByTopic, Chain, Search, Context, Compare, Relations, audit-trail/transitions, review queue/detail, rule show/history/impact, reconstructibility, close create, lifecycle export, and any other generic read, all registered behind the token guard in `Handler()` at `http.go:309-360`) MUST, when a `VerifiedApprovalPrincipal` is present in the request context (resolved by the `authenticate` middleware at `http.go:449` into the context via the existing `PrincipalFromContext`/`RequirePrincipal` seam in `internal/server/auth_context.go`), validate the derived scope against that principal per `FD-SPR-1`/`FD-SPR-5` BEFORE the handler proceeds. Mismatch MUST fail closed with the typed scope-denied (403 on HTTP). When no principal is present (shared-token-only call), the request proceeds under the documented caller-asserted reference behavior (`FD-SPR-3`) — unchanged.

  #### Scenario: authenticated A-principal with B query scope is denied

  - GIVEN a valid session/OIDC principal for company A (membership tuple resolves tenant-A/company-A)
  - WHEN an HTTP generic read is invoked with that credential and a query scope for company B (`?ruc=<B>&period=…`)
  - THEN the handler fails closed with the typed scope-denied (`TENANT_SCOPE_MISMATCH` or `COMPANY_SCOPE_DENIED`, HTTP 403) and no API call executes

  #### Scenario: authenticated A-principal with A query scope succeeds unchanged

  - GIVEN the same principal for company A
  - WHEN the same read is invoked with an exact company-A scope (`?ruc=<A>&period=…`)
  - THEN the request proceeds exactly as before the change, with identical results

  #### Scenario: shared-token-only call stays caller-asserted

  - GIVEN a request carrying only the shared bearer token (no session/OIDC credential, no principal in context)
  - WHEN a generic read is invoked with any valid exact scope
  - THEN the request behaves exactly as before (reference mode, `FD-SPR-3`), with no binding and no new denial

- `FR-SPR-2` — **MCP identity→scope binding.** As an MCP consumer I want identity-present tool calls bound to my membership so a bound principal cannot read outside its company scope. In `handleToolsCall` (`internal/server/mcp.go:1044`), when an identity context is present (a bound `VerifiedApprovalPrincipal` supplied to the MCP server by the HTTP `/mcp` middleware, per the existing design note at `mcp.go:1450-1455`), every tool call whose effective scope is company-kind MUST validate that scope against the principal per `FD-SPR-1`/`FD-SPR-5` before dispatch; mismatch MUST fail closed with the typed scope-denied. `DRENYRA_DEFAULT_SCOPE` remains the operator-configured default for the unauthenticated/embedded case exactly as today (`NewMCPServerWithDefaultScope`, `mcp.go:132`, fails closed at construction); identity-present calls MUST match the principal's scope and MUST NOT be constrained or overridden by `DRENYRA_DEFAULT_SCOPE`.

  #### Scenario: bound B-principal calls a scope-A tool

  - GIVEN an MCP server with a bound principal for company B and a configured default scope (any)
  - WHEN the principal invokes a company-scoped tool whose effective scope is company A
  - THEN the tool fails closed with the typed scope-denied, before any API call

  #### Scenario: bound B-principal calls a scope-B tool

  - GIVEN the same bound principal for company B
  - WHEN the principal invokes a company-scoped tool whose effective scope is company B
  - THEN the tool executes exactly as before the change

  #### Scenario: unauthenticated embedded MCP unchanged

  - GIVEN an MCP server with no bound principal (stdio/embedded case)
  - WHEN a tool call carries a caller-asserted scope
  - THEN the call behaves exactly as before (`DRENYRA_DEFAULT_SCOPE`/reference behavior, `FD-SPR-3`), with no binding and no new denial

- `FR-SPR-3` — **CLI identity→scope binding.** As a CLI operator I want my session principal to constrain flag-derived scopes so a session for company A cannot read company B via `--company`. In `cmd/drenyra-engram/main.go`, when a session credential is present and resolves to a `VerifiedApprovalPrincipal` via the existing `loadSessionToken` + `resolver.Authenticate(AuthMethodSession)` pattern (`main.go:517-532`), every command that derives its scope through `cliCompanyScope` (`main.go:2303` — the scope-flag read commands) MUST validate that scope against the principal per `FD-SPR-1`/`FD-SPR-5` before proceeding to store access; mismatch MUST fail closed with the typed scope-denied and a non-zero exit. Session-less CLI operation stays caller-asserted (reference mode, `FD-SPR-3`), unchanged.

  #### Scenario: session A-principal with --company B

  - GIVEN a CLI session whose principal's membership is company A
  - WHEN a scope-flag command runs with `--company <B-RUC>`
  - THEN the command fails closed with the typed scope-denied and a non-zero exit, before any store access

  #### Scenario: session A-principal with --company A

  - GIVEN the same session for company A
  - WHEN the command runs with `--company <A-RUC>`
  - THEN the command executes exactly as before the change

  #### Scenario: session-less CLI unchanged

  - GIVEN no CLI session credential
  - WHEN a scope-flag command runs with any valid exact scope
  - THEN the command behaves exactly as before (reference mode, `FD-SPR-3`), with no binding and no new denial

- `FR-SPR-4` — **Institutional surfaces orthogonal.** As a maintainer I want `kind=institutional` handling untouched so identity binding neither grants nor revokes institutional opt-in. Institutional requests on every surface MUST behave exactly as before this change: the existing explicit opt-in path (`httpQueryScope(r, true)` / `kind=institutional` query, MCP/CLI equivalents) is unchanged, the binding never applies to institutional effective scopes (`FD-SPR-4`), and no test or code MAY assert that identity binding alters institutional outcomes.

  #### Scenario: institutional request identical before/after

  - GIVEN an institutional-scoped request on any surface
  - WHEN it is invoked with and without a principal present
  - THEN the outcome is byte-identical to the pre-change behavior in both cases, and no binding denial is produced

- `FR-SPR-5` — **v1 scope contract and manifest note.** As an integrator I want the binding contract written down so callers know what breaks pre-v1. `contracts/scope.md` MUST document, in a new v1 scope-contract section: effective scope = explicit scope parameters validated against the authenticated principal's membership scope when identity is present (exact-match, `FD-SPR-1`); unauthenticated/reference surfaces stay caller-asserted and are reference mode only (`FD-SPR-3`); institutional surfaces are orthogonal (`FD-SPR-4`); the typed `SCOPE_DENIED` contract (`FD-SPR-5`); and multi-company principals as a future allowed-set extension (`FD-SPR-2`). `docs/ecosystem/capabilities-and-conformance.md` MUST carry a breaking-change note stating that authenticated calls whose scope differs from the principal's membership now fail closed with the typed scope-denied.

  #### Scenario: contract states the breaking behavior

  - GIVEN the v1 scope-contract section and the manifest note
  - WHEN a reviewer reads them
  - THEN they state the four frozen semantics above verbatim-consistent, name the typed denial, and mark the behavior change as breaking pre-v1

### Non-functional / boundary rules

- `NFR-SPR-1` — **Fail-closed default.** The binding MUST fail closed on any mismatch or ambiguity; there is no scope fallback, no partial cross-scope data, and no request MAY proceed with an unvalidated company scope while a principal is present.
- `NFR-SPR-2` — **Error contract consistency.** The typed `SCOPE_DENIED` MUST be the existing frozen scope-denied codes (`FD-SPR-5`), consistent with the frozen scope-denied outcomes in `contracts/approval.md` (check order tenant → company) and `contracts/period-comparison.md`. The denial reveals only that the caller's scope is outside its membership — never any foreign state, existence, or tenant/company/RUC/period identifier of another company.
- `NFR-SPR-3` — **Matrix and exhaustiveness guard stay green.** `TestCrossTenantMatrixExhaustiveness` and the existing cross-tenant matrix (`internal/server/cross_tenant_matrix_test.go`) MUST stay green: the binding is adapter-internal, no canonical `API` operation is added, removed, or reclassified, and no safe-outcome assertion is weakened.
- `NFR-SPR-4` — **Strict TDD and green suites at every slice boundary.** Apply MUST work tests-first per delivery slice (`NFR-XC-1` pattern): each slice's binding tests are written and failing before its implementation lands. `go test ./...`, `npm test`, `npm run typecheck`, `go vet ./...`, and `gofmt -l .` MUST stay green at every slice boundary; `TestGoldenVectorsGo` stays green (no shared Go↔TS semantics change is expected).
- `NFR-SPR-5` — **Defense-in-depth boundary stated.** Adapter enforcement does not protect the store against direct Go callers; the store-level scope assertions (`RelationsForScope` to_id hardening and peers) stay exactly as hardened and are NOT modified by this change. The v1 scope-contract section MUST state this boundary precisely.
- `NFR-SPR-6` — **No authz-policy change.** The binding reuses the frozen policy comparison semantics and MUST NOT alter any authorization policy, policy version constant, check order, role/action matrix, or denial code; no `DRENYRA_DEFAULT_SCOPE` construction semantics change.

### Invariant requirements (Drenyra Engram standing rules)

- `IR-1` — Money remains whole `int64` cents / `BigInt` cents; no float path is introduced (nothing in this change touches money).
- `IR-2` — Scope stays structural and fails closed on mismatch; cross-tenant invisibility is a tested invariant (this change extends, never weakens, that rule at the adapter boundary).
- `IR-3` — Non-authorization boundary holds: the binding grants no authority; it only constrains already-authorized callers' scope.
- `IR-4` — Go↔TS golden parity is preserved for all shared semantics (`TestGoldenVectorsGo` green).
- `IR-5` — No `any` in TypeScript; precise types or `unknown` only.

## Acceptance criteria

- `AC-SPR-1` — HTTP binding tests exist at the adapter boundary (`internal/server`): for every generic read handler that derives scope via `httpQueryScope`, a principal-for-A + query-scope-B call fails with the typed scope-denied (403, `TENANT_SCOPE_MISMATCH`/`COMPANY_SCOPE_DENIED`), and a principal-for-A + query-scope-A call succeeds unchanged (`FR-SPR-1`).
- `AC-SPR-2` — MCP binding tests exist (`internal/server`): an identity-present call with a mismatched scope fails with the typed scope-denied, the matching-scope call succeeds unchanged, and the unauthenticated/embedded (`DRENYRA_DEFAULT_SCOPE`) behavior is unchanged (`FR-SPR-2`).
- `AC-SPR-3` — CLI binding tests exist (`cmd/drenyra-engram`): a session-present scope-flag command with a mismatched `--company`/`--period` fails with the typed scope-denied and non-zero exit, the matching-scope command succeeds unchanged, and the session-less command is unchanged (`FR-SPR-3`).
- `AC-SPR-4` — Institutional surface tests prove byte-identical pre/post behavior with and without a principal (`FR-SPR-4`, `FD-SPR-4`).
- `AC-SPR-5` — `contracts/scope.md` carries the v1 scope-contract section stating the four frozen semantics, the typed denial, the multi-company future extension, and the store-boundary statement; `docs/ecosystem/capabilities-and-conformance.md` carries the breaking-change note (`FR-SPR-5`, `NFR-SPR-5`).
- `AC-SPR-6` — The identity-bound matrix cases (extending `crossTenantCatalog()` or a focused sibling matrix) cover principal-A + scope-B → typed `SCOPE_DENIED` and principal-A + scope-A → allowed on HTTP, MCP, and CLI; institutional rows unaffected; `TestCrossTenantMatrixExhaustiveness` stays green (`NFR-SPR-3`).
- `AC-SPR-7` — The typed denial uses only the frozen codes and HTTP mappings; a reviewer grep over the diff finds no new error code, no new status mapping, and no fallback path on mismatch (`FD-SPR-5`, `NFR-SPR-2`).
- `AC-SPR-8` — No schema, policy, store, or `DRENYRA_DEFAULT_SCOPE` construction change exists in the diff (reviewer check per `OUT` scope and `NFR-SPR-6`).
- `AC-SPR-9` — `go test ./...`, `npm test`, `npm run typecheck`, `go vet ./...`, `gofmt -l .`, and `TestGoldenVectorsGo` are green at every slice boundary and at completion (`NFR-SPR-4`).

## Out of scope

- No schema change, migration, or contract version bump for scope storage.
- No authorization-policy change: no policy version bump, no check-order change, no new role/action, no override/break-glass path.
- No token revocation, session revocation, MFA, or membership provisioning (OIDC follow-ups per the proposal's issue-#18 lineage).
- No store-level change: `RelationsForScope`/store scope assertions and all cross-tenant safe outcomes stay exactly as hardened.
- No new API operation, route, MCP tool, CLI command, or parameter shape; the change is enforcement, not a new surface.
- No change to `DRENYRA_DEFAULT_SCOPE` construction, `initialize` context, or period-comparison contract.
- No multi-company membership support (`FD-SPR-2`).
- No weakening of any frozen contract, denial code, or isolation behavior to satisfy a test.

## Test plan

Obsessive by design — every acceptance criterion has at least one automated proof, and the failure path (identity A + scope B) is the first-class citizen:

- **HTTP (AC-SPR-1, AC-SPR-4):** per-handler binding tests at the adapter boundary over every `httpQueryScope`-deriving generic read (shared table-driven helper in `internal/server`, reusing the existing cross-tenant fixture helpers): principal-A + scope-B → typed 403 scope-denied before the API call; principal-A + scope-A → unchanged; shared-token-only → unchanged; institutional → byte-identical with/without principal.
- **MCP (AC-SPR-2):** `handleToolsCall`-level tests with a bound principal: mismatched company scope → typed scope-denied tool error text; matched scope → unchanged result; no bound principal → pre-change behavior, including `DRENYRA_DEFAULT_SCOPE` construction fail-closed tests staying green (`mcp_context_test.go:94`).
- **CLI (AC-SPR-3):** command-level tests in `cmd/drenyra-engram`: session-present scope-flag command with mismatched RUC → typed scope-denied, non-zero exit, no store access; matched → unchanged; session-less → unchanged.
- **Matrix (AC-SPR-6):** identity-bound matrix rows added to or alongside the cross-tenant catalog (principal-A + scope-B → `safeDenied`-style typed denial; principal-A + scope-A → allowed) for HTTP, MCP, and CLI; `TestCrossTenantMatrixExhaustiveness` remains green — no catalog method set change.
- **Contract (AC-SPR-5):** documentation review — v1 scope-contract section and manifest note state the frozen semantics; reviewer greps for new error codes, fallback-on-mismatch paths, schema/policy/store changes across the diff (AC-SPR-7, AC-SPR-8).
- **Cross-cutting (AC-SPR-9):** strict-TDD slice sequencing; `go test ./...` + `npm test` + `npm run typecheck` + `go vet ./...` + `gofmt -l .` green per `openspec/config.yaml` verify order at every slice boundary.

## Risks and delivery shape

- **Membership model ambiguity (WARNING):** a principal may hold multiple membership rows across companies. Mitigated by `FD-SPR-1`/`FD-SPR-2`: exact-match semantics on the single carried company scope, multi-company documented as a future allowed-set extension; no binding change is possible until the principal model carries the set.
- **Breaking-change blast radius (WARNING):** authenticated HTTP/MCP/CLI callers whose scope differs from their principal's membership will start failing with the typed scope-denied. Mitigated: enforcement lands pre-v1 (0.x) per the documented follow-up, is limited to identity-present calls, and the reference/unauthenticated surface keeps caller-asserted behavior (`FD-SPR-3`); the breaking-change note lands in the capabilities manifest (slice 3).
- **Principal-resolution seam ambiguity (WARNING):** `requireToken` (`http.go:497`) is the shared-token guard and does NOT resolve a principal; only `authenticate` (`http.go:449`) and the HTTP `/mcp` middleware supply a `VerifiedApprovalPrincipal`. Design/apply MUST NOT implement principal resolution inside `requireToken`; the binding applies only when a principal is already present in the context. Spec binds this truth in `FR-SPR-1`/`FR-SPR-2`; the identity-bound matrix proves the shared-token-only path is unchanged.
- **Institutional surface interplay (SUGGESTION):** identity binding must not accidentally grant or revoke institutional opt-in. Mitigated by `FD-SPR-4` + `AC-SPR-4`: binding applies only to company-kind scopes; institutional tests prove byte-identical behavior.
- **Test surface growth (SUGGESTION):** identity-bound matrix rows add to an already large suite. Mitigated: reuse the existing cross-tenant fixture helpers and keep binding tests at the adapter boundary (per-surface helper, one table per surface).
- **False assurance (SUGGESTION):** adapter enforcement does not protect the store against direct Go callers. Mitigated by `NFR-SPR-5`: store-level assertions stay, and the v1 scope contract states the boundary precisely.

**Delivery shape:** bounded adapter + test + documentation change; no store or schema work. Changed-line forecast: likely **under 400 lines**. Recommended slices:

1. **HTTP identity→scope binding + matrix extension** (binding helper + per-handler application in `internal/server`, identity-bound matrix rows, exhaustiveness guard stays green);
2. **MCP + CLI binding + cross-surface consistency tests** (bound-principal validation in `handleToolsCall`, session-present validation at `cliCompanyScope` call sites, cross-surface denial tests);
3. **v1 scope contract + capabilities manifest note** (`contracts/scope.md`, `docs/ecosystem/capabilities-and-conformance.md`).

Each slice carries its own focused tests-first work and stays reviewable; the cross-tenant exhaustiveness guard remains green throughout. Rollback is adapter-only: reverting the binding restores caller-asserted behavior with no schema, contract, or data change.
