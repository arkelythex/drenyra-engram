# Design — scope-param-rollout

> Phase: design · Artifact: design · Status: draft
> Inputs: frozen spec + proposal + exploration inventory. Strict TDD applies during apply and verify.
> Store: OpenSpec file-based (openspec/changes/scope-param-rollout/design.md).
> Binding constraints: FR-SPR-1…5, NFR-SPR-1…6, AC-SPR-1…9, IR-1…5, and FD-SPR-1…6 are not reopened here.

## Context and design posture

This change enforces **identity→scope binding** at the three adapter surfaces only. It introduces no new API operation, route, MCP tool, CLI command, parameter shape, schema, or error code; it does not touch the store, the authorization policies, or `DRENYRA_DEFAULT_SCOPE` construction. The canonical API and the store-level scope assertions stay exactly as hardened — this is adapter-internal enforcement, defense-in-depth at the boundary.

The central rule for apply is fail-before-fix and no invented seams:

- **No principal resolution inside `requireToken`.** `requireToken` (`http.go:497`) is the shared-token guard and does not resolve a principal. The binding applies only where a `VerifiedApprovalPrincipal` is already present in the request context (resolved by `authenticate` at `http.go:449` or the HTTP `/mcp` middleware). Shared-token-only calls pass through unchanged (FD-SPR-3).
- **Exact-match, no fallback.** On mismatch the request fails closed with the frozen typed codes; the scope is never silently rewritten to the principal's scope (FD-SPR-1).
- **Institutional orthogonal.** The binding applies only to company-kind effective scopes; institutional requests are byte-identical before/after (FD-SPR-4).

## Decisions

- `D-SPR-1` — **One shared binding helper, exact-match semantics.** A single helper in `internal/server` (owned by the HTTP/MCP adapters) and a CLI-local twin in `cmd/drenyra-engram` (Go cannot import another package's helpers; the server and CLI are separate binaries):

  ```go
  // bindScopeToPrincipal returns the frozen typed scope-denied error when the
  // effective scope does not exactly match the principal's membership scope.
  // Mirrors authz/approval_policy.go:61-67 (contains(p.CompanyScopes(), ...)).
  // No fallback, no rewrite, institutional untouched.
  func bindScopeToPrincipal(scope core.Scope, p auth.VerifiedApprovalPrincipal) error {
      if scope.Kind != core.ScopeKindCompany {
          return nil // FD-SPR-4: institutional orthogonal
      }
      if scope.OrganizationID != p.TenantID() {
          return auth.ErrTenantScopeMismatch // TENANT_SCOPE_MISMATCH
      }
      if !contains(p.CompanyScopes(), scope.CompanyID) {
          return auth.ErrCompanyScopeDenied // COMPANY_SCOPE_DENIED
      }
      return nil
  }
  ```

  The comparison mirrors the frozen policy semantics exactly: `organizationId == principal.TenantID()` and `companyId ∈ principal.CompanyScopes()`. The effective scope remains an exact period-pinned company scope; period is not an identity dimension (a principal's membership carries the company, not the period).

- `D-SPR-2` — **HTTP seam: a binding wrapper at route registration, not inside `requireToken`.** Add a small middleware `bindScope(next http.HandlerFunc) http.HandlerFunc` that: (1) reads the principal via `PrincipalFromContext` (`auth_context.go:31`); (2) if no principal is present, calls `next` unchanged (FD-SPR-3); (3) if a principal is present, derives the effective scope via the handler's existing `httpQueryScope` path is NOT re-derived — the wrapper instead calls `next` with the principal, and each handler validates its already-derived scope through `bindScopeToPrincipal` before the API call (FD-SPR-6: enforcement before the API call, never re-deriving scope). Simpler alternative: apply the binding as a named helper called at the top of each generic read handler after `httpQueryScope` succeeds and before the API call. **Decision:** use the explicit helper call at each `httpQueryScope` call site (there are ~10 generic read handlers) rather than a wrapping middleware, because the scope is derived per-handler (query vs body differences across handlers) and a wrapper cannot see it. This keeps every seam explicit and greppable for review.
- `D-SPR-3` — **MCP seam: binding inside `handleToolsCall`.** At `mcp.go:1044`, when a bound principal is present in the MCP server's identity context (supplied by the HTTP `/mcp` middleware per `mcp.go:1450-1455`), validate each company-kind tool call's effective scope through `bindScopeToPrincipal` before dispatch; mismatch returns the typed denial text. `DRENYRA_DEFAULT_SCOPE` and the unauthenticated/embedded path are untouched (FD-SPR-3).
- `D-SPR-4` — **CLI seam: validation after `cliCompanyScope` on session-present commands.** In `cmd/drenyra-engram/main.go`, the commands at `~300` and `~348` derive scope via `cliCompanyScope`. When a session credential is present and resolves to a `VerifiedApprovalPrincipal` (existing `loadSessionToken` + `resolver.Authenticate(AuthMethodSession)` pattern at `~517-532`), validate the derived scope through the CLI-local binding helper before store access; mismatch → typed denial text + non-zero exit. Session-less operation unchanged (FD-SPR-3).
- `D-SPR-5` — **Test placement: per-surface table-driven tests + identity-bound matrix rows.** New test files: `internal/server/scope_binding_http_test.go`, `internal/server/scope_binding_mcp_test.go`, `cmd/drenyra-engram/scope_binding_cli_test.go`. Reuse the existing cross-tenant fixture helpers (`newCrossTenantFixture`, `seedMemory`, `saveOne`). Add identity-bound rows to `crossTenantCatalog()` (or a sibling focused matrix) for the principal-A + scope-B → typed denial and principal-A + scope-A → allowed pairs WITHOUT changing the catalog's method set, so `TestCrossTenantMatrixExhaustiveness` stays green (NFR-SPR-3).
- `D-SPR-6` — **Delivery slices.** Slice 1: HTTP binding helper + per-handler application + identity-bound matrix rows + HTTP binding tests. Slice 2: MCP + CLI binding + cross-surface consistency tests. Slice 3: `contracts/scope.md` v1 scope-contract section + `docs/ecosystem/capabilities-and-conformance.md` breaking-change note. Each slice is tests-first and independently revertible.

## HTTP — identity→scope binding on generic reads

### File placement

- `internal/server/scope_binding.go` — `bindScopeToPrincipal` + the `contains` helper (or reuse the existing pattern).
- `internal/server/http.go` — call `bindScopeToPrincipal(scope, principal)` after each `httpQueryScope` success and before the API call in the generic read handlers: Save, Get, GetByTopic, Chain, Search, Context, Compare, Relations, Transitions, review queue/detail, rule show/history/impact, reconstructibility, close create, lifecycle export (FR-SPR-1).

### Behavior

| Caller | Scope | Before | After |
|---|---|---|---|
| Principal A (session/OIDC) | exact A | proceeds | **proceeds unchanged** |
| Principal A | exact B | proceeds (leak) | **typed 403 denial**, no API call |
| Shared token only | any | proceeds | **proceeds unchanged** (FD-SPR-3) |
| Institutional | any | proceeds | **proceeds byte-identical** (FD-SPR-4) |

### Test table (AC-SPR-1, AC-SPR-6)

- `TestHTTPIdentityScopeBinding` — table-driven over the generic read handlers: principal-A + scope-B → 403 with `TENANT_SCOPE_MISMATCH`/`COMPANY_SCOPE_DENIED` and no API effect; principal-A + scope-A → unchanged result; shared-token-only → unchanged; institutional → byte-identical with/without principal.

## MCP — identity→scope binding

### File placement

- `internal/server/scope_binding.go` — shared helper (same package as the MCP server).
- `internal/server/mcp.go` — `handleToolsCall` (`~1044`): validate company-kind tool scope through the helper when a bound principal is present.

### Behavior

| Caller | Tool scope | Before | After |
|---|---|---|---|
| Bound principal B | B | proceeds | **proceeds unchanged** |
| Bound principal B | A | proceeds (leak) | **typed denial tool error**, no dispatch |
| No bound principal | any | proceeds | **proceeds unchanged** (FD-SPR-3) |

### Test table (AC-SPR-2)

- `TestMCPIdentityScopeBinding` — bound B-principal + scope-A tool → typed denial; bound B + scope-B → unchanged; unauthenticated/embedded → `DRENYRA_DEFAULT_SCOPE` behavior unchanged (existing `mcp_context_test.go:94` construction fail-closed tests stay green).

## CLI — identity→scope binding

### File placement

- `cmd/drenyra-engram/scope_binding.go` — CLI-local binding helper (mirrors the server helper; same exact-match semantics).
- `cmd/drenyra-engram/main.go` — validate after `cliCompanyScope` at the session-present command sites (`~300`, `~348`).

### Behavior

| Caller | --company | Before | After |
|---|---|---|---|
| Session A | A | proceeds | **proceeds unchanged** |
| Session A | B | proceeds (leak) | **typed denial, non-zero exit**, no store access |
| Session-less | any | proceeds | **proceeds unchanged** (FD-SPR-3) |

### Test table (AC-SPR-3)

- `TestCLIIdentityScopeBinding` — session-A + `--company B` → typed denial + non-zero exit + no store access; session-A + `--company A` → unchanged; session-less → unchanged.

## Contracts and documentation

- `contracts/scope.md` — new v1 scope-contract section stating: effective scope = explicit scope parameters validated against the authenticated principal's membership scope when identity is present (exact-match, FD-SPR-1); unauthenticated/reference surfaces stay caller-asserted and are reference mode only (FD-SPR-3); institutional orthogonal (FD-SPR-4); typed denial = `TENANT_SCOPE_MISMATCH`/`COMPANY_SCOPE_DENIED` (FD-SPR-5); multi-company principals as a future allowed-set extension (FD-SPR-2); the store boundary: adapter enforcement does not protect direct Go callers (NFR-SPR-5).
- `docs/ecosystem/capabilities-and-conformance.md` — breaking-change note: authenticated calls whose scope differs from the principal's membership now fail closed with the typed scope-denied, effective pre-v1 (0.x).

## Verification gates (per slice)

- `go test ./...` green (incl. the new binding tests and the unchanged cross-tenant matrix + exhaustiveness guard).
- `npm test` (385/385) and `npm run typecheck` green — no TS change expected.
- `go vet ./...` and `gofmt -l .` clean.
- `go test ./internal/core -run TestGoldenVectorsGo` green (no shared Go↔TS semantics change).
- Reviewer greps per slice: no new error code, no new HTTP status mapping, no fallback-on-mismatch path, no schema/policy/store/`DRENYRA_DEFAULT_SCOPE` change (AC-SPR-7, AC-SPR-8).

## Delivery slices

1. **HTTP binding + identity-bound matrix** — `scope_binding.go`, per-handler application in `http.go`, `scope_binding_http_test.go`, matrix rows; exhaustiveness guard green.
2. **MCP + CLI binding + cross-surface tests** — `handleToolsCall` validation, CLI helper + command sites, `scope_binding_mcp_test.go`, `scope_binding_cli_test.go`, cross-surface consistency tests.
3. **Contracts + manifest note** — `contracts/scope.md` v1 section, `docs/ecosystem/capabilities-and-conformance.md` breaking-change note.

Each slice lands tests-first and keeps the full suites green at its boundary; rollback is adapter-only with no schema, contract, or data change.
