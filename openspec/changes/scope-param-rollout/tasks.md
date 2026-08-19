# Tasks — scope-param-rollout

> Phase: tasks · Artifact: tasks · Status: draft
> Inputs: spec + design (required). Strict TDD: not active in this phase (planning); `phases.apply.strict_tdd: true` per openspec/config.yaml — apply must run RED → GREEN per slice. `go test ./...` and `npm test` MUST stay green at every slice boundary.
> Forwarded session settings: delivery_strategy=ask-on-risk, chain_strategy=stacked-to-main (owner-approved), artifact_store=openspec.
> Binding: FR-SPR-1…5, NFR-SPR-1…6, AC-SPR-1…9, IR-1…5, and FD-SPR-1…6 are frozen and MUST NOT deviate. Apply MUST NOT touch the store, any authorization policy, `DRENYRA_DEFAULT_SCOPE` construction, the canonical `API` method set, `requireToken`'s shared-token guard, or the cross-tenant exhaustiveness catalog's method set — any of these is an exception requiring re-approval (per spec OUT / design posture). No new error code, HTTP status mapping, route, MCP tool, CLI command, or parameter shape MAY be introduced (FD-SPR-5, NFR-SPR-6).

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ≈ 200–400 (additions + deletions; adapter binding helpers + focused binding tests + two doc files, no store/schema/policy work) |
| 400-line budget risk | Low–Medium |
| Chained PRs recommended | Yes (defensive; under-budget but slice-separated) |
| Suggested split | PR 1 (slice 1 HTTP) → PR 2 (slice 2 MCP+CLI) → PR 3 (slice 3 docs) |
| Delivery strategy | ask-on-risk |
| Chain strategy | stacked-to-main |

```text
Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: stacked-to-main
400-line budget risk: Low
```

Per-slice line estimates: slice 1 ≈ 80–160 (`internal/server/scope_binding.go` helper + per-handler application in `http.go` + `scope_binding_http_test.go` + identity-bound matrix rows); slice 2 ≈ 70–140 (`handleToolsCall` validation in `mcp.go` + CLI helper/command sites + `scope_binding_mcp_test.go` + `scope_binding_cli_test.go` + cross-surface consistency tests); slice 3 ≈ 40–80 (`contracts/scope.md` v1 section + `docs/ecosystem/capabilities-and-conformance.md` breaking-change note). The change stays at or under the 400-line rule in openspec/config.yaml (`pr_size`), but the three slices are independent, tests-first, and each independently revertible (D-SPR-6); delivery strategy is `ask-on-risk` and chain strategy `stacked-to-main` (already owner-approved). Each PR merges to main in order 1 → 2 → 3 and never leaves a block at an unsupported PASS. No production seam outside the adapter boundary exists; if any binding test exposes a real store/policy/schema defect, apply MUST stop that slice and report for re-approval — never weaken an assertion.

---

## PR 1 — HTTP identity→scope binding + identity-bound matrix (AC-SPR-1, AC-SPR-4, AC-SPR-6)

Objective: one shared exact-match binding helper in `internal/server`, explicit per-handler validation at every `httpQueryScope` call site behind the `authenticate` middleware (never inside `requireToken`), and identity-bound matrix rows that keep the exhaustiveness guard green. Files: `internal/server/scope_binding.go` (new), `internal/server/http.go`, `internal/server/scope_binding_http_test.go` (new), `internal/server/cross_tenant_matrix_test.go`. No route, API method, error code, or status mapping change (FD-SPR-5).

- [x] S1.1 RED — Write `internal/server/scope_binding_http_test.go` with `TestHTTPIdentityScopeBinding` (AC-SPR-1, AC-SPR-4, FR-SPR-1, D-SPR-5): a table-driven test over every generic read handler that derives scope via `httpQueryScope(r, false)` (`http.go:1805` — Save, Get, GetByTopic, Chain, Search, Context, Compare, Relations, Transitions, review queue/detail, rule show/history/impact, reconstructibility, close create, lifecycle export), reusing `newCrossTenantFixture`/`seedMemory`/`saveOne` (`cross_tenant_matrix_test.go:214`). Rows: principal-for-A + query-scope-B → HTTP 403 with `TENANT_SCOPE_MISMATCH`/`COMPANY_SCOPE_DENIED` and NO API/effect; principal-for-A + query-scope-A → result unchanged; shared-token-only → unchanged (`FD-SPR-3`); institutional (`httpQueryScope(r, true)`) → byte-identical with and without a principal (`FD-SPR-4`). RED until every generic-read handler has its four rows and no mismatch row proceeds past the binding. <!-- sdd-owner: implementation -->
- [x] S1.2 GREEN — Implement `internal/server/scope_binding.go` with `bindScopeToPrincipal(scope core.Scope, p auth.VerifiedApprovalPrincipal) error` (D-SPR-1, FD-SPR-1/FD-SPR-4/FD-SPR-5): return nil for non-company kinds; `auth.ErrTenantScopeMismatch` when `scope.OrganizationID != p.TenantID()`; `auth.ErrCompanyScopeDenied` when `scope.CompanyID` is outside `p.CompanyScopes()` (mirror `contains(p.CompanyScopes(), ...)` at `authz/approval_policy.go:61-67/:194`); add the `contains` helper if not already reachable. No fallback, no rewrite, institutional untouched. Apply the helper at each generic read handler in `http.go` after `httpQueryScope` succeeds and BEFORE the API call, only when a `VerifiedApprovalPrincipal` is present via `PrincipalFromContext` (`auth_context.go:31`); `requireToken` (`http.go:497`) stays the shared-token guard and resolves NO principal (`FD-SPR-3`, FR-SPR-1). Mismatch maps to HTTP 403 through the existing `authErrorHTTPStatus` envelope (`http.go:162-170`) — no new status mapping. GREEN only after every row in S1.1 passes. <!-- sdd-owner: implementation -->
- [x] S1.3 RED — Add identity-bound matrix rows to the cross-tenant catalog or a focused sibling matrix (AC-SPR-6, NFR-SPR-3, D-SPR-5): for HTTP, principal-A + scope-B → typed `SCOPE_DENIED` (`safeDenied`-style) and principal-A + scope-A → allowed, WITHOUT changing the method set produced by `crossTenantCatalog()` (`cross_tenant_matrix_test.go:299`) so `TestCrossTenantMatrixExhaustiveness` (`:353`) stays green. RED if the guard's catalog method-set assertion fails or a bound row is missing. <!-- sdd-owner: implementation -->
- [x] S1.4 TRIANGULATE — Assert non-weakering and boundary (NFR-SPR-1/NFR-SPR-2/NFR-SPR-3, AC-SPR-7): review the HTTP diff — no new error code, no new HTTP status mapping, no fallback-on-mismatch path, no principal resolution added to `requireToken`, institutional requests never special-cased, store assertions untouched. Any isolation or fail-closed gap keeps the criterion RED and is reported, never widened/deleted. <!-- sdd-owner: implementation -->

**Verification gate PR 1:** `go test ./internal/server` then `go test ./...` green (incl. `TestHTTPIdentityScopeBinding`, the unchanged cross-tenant matrix + `TestCrossTenantMatrixExhaustiveness`); `gofmt -l .` and `go vet ./...` clean; `git diff -- internal/server/http.go` limited to the per-handler binding calls; `npm test` green.

---

## PR 2 — MCP + CLI binding + cross-surface consistency (AC-SPR-2, AC-SPR-3, AC-SPR-6)

Objective: bound-principal validation in `handleToolsCall`, a CLI-local exact-match helper applied at session-present scope-flag command sites, and cross-surface denial tests proving the typed scope-denied is consistent across HTTP/MCP/CLI. Files: `internal/server/mcp.go`, `internal/server/scope_binding_mcp_test.go` (new), `cmd/drenyra-engram/scope_binding.go` (new), `cmd/drenyra-engram/main.go`, `cmd/drenyra-engram/scope_binding_cli_test.go` (new). No change to `DRENYRA_DEFAULT_SCOPE` construction semantics (`FD-SPR-3`, NFR-SPR-6).

- [x] S2.1 RED — Write `internal/server/scope_binding_mcp_test.go` with `TestMCPIdentityScopeBinding` (AC-SPR-2, FR-SPR-2, D-SPR-3/D-SPR-5): bound B-principal (supplied via the HTTP `/mcp` middleware identity context per `mcp.go:1450-1455`) + scope-A company-kind tool call → typed scope-denied tool error BEFORE dispatch and no API effect; bound B + scope-B → result unchanged; no bound principal → pre-change `DRENYRA_DEFAULT_SCOPE`/embedded behavior unchanged (`FD-SPR-3`). The existing construction fail-closed tests (`mcp_context_test.go:97` `TestMCPServerDefaultScopeFailClosed`) stay green. RED until each `handleToolsCall` identity-present row fails/matches as specified. <!-- sdd-owner: implementation -->
- [x] S2.2 GREEN — In `internal/server/mcp.go`, validate each company-kind tool call's effective scope through the shared `bindScopeToPrincipal` at `handleToolsCall` (`~1044`) when a bound principal is present, before dispatch (D-SPR-3, FD-SPR-6); mismatch returns the typed denial text via the existing MCP error envelope. `DRENYRA_DEFAULT_SCOPE` and the unauthenticated/embedded path stay untouched (`FD-SPR-3`). GREEN only after every S2.1 row passes. <!-- sdd-owner: implementation -->
- [x] S2.3 RED — Write `cmd/drenyra-engram/scope_binding_cli_test.go` with `TestCLIIdentityScopeBinding` (AC-SPR-3, FR-SPR-3, D-SPR-4/D-SPR-5): session-A principal (`loadSessionToken` + `resolver.Authenticate(AuthMethodSession)`, pattern at `main.go:517-532`) + `--company <B-RUC>` on a scope-flag read command → typed scope-denied, non-zero exit, NO store access; session-A + `--company <A-RUC>` → unchanged; session-less → unchanged (`FD-SPR-3`). RED until each session-present row fails/matches as specified. <!-- sdd-owner: implementation -->
- [x] S2.4 GREEN — Implement the CLI-local binding helper `cmd/drenyra-engram/scope_binding.go` (same exact-match semantics as the server helper; Go cannot import the server package, D-SPR-1) and apply it after `cliCompanyScope` (`main.go:2303`) at the session-present scope-flag command sites (`~300`, `~348`) when a session resolves to a `VerifiedApprovalPrincipal`; mismatch → typed denial text + non-zero exit before store access. Session-less operation unchanged (`FD-SPR-3`). GREEN only after every S2.3 row passes. <!-- sdd-owner: implementation -->
- [x] S2.5 TRIANGULATE — Cross-surface consistency and matrix (AC-SPR-6, NFR-SPR-3, AC-SPR-7): assert the typed scope-denied carries the identical frozen code semantics on HTTP/MCP/CLI for the principal-A + scope-B pair; add the MCP/CLI identity-bound rows to the matrix without altering `crossTenantCatalog()`'s method set so the exhaustiveness guard stays green; grep the MCP/CLI diff for no new error code, no fallback path, and no `DRENYRA_DEFAULT_SCOPE` construction change. <!-- sdd-owner: implementation -->

**Verification gate PR 2:** `go test ./internal/server ./cmd/drenyra-engram` then `go test ./...` green (incl. `TestMCPIdentityScopeBinding`, `TestCLIIdentityScopeBinding`, unchanged `TestCrossTenantMatrixExhaustiveness`, unchanged `mcp_context_test.go` fail-closed tests); `gofmt -l .` and `go vet ./...` clean; `npm test` green.

---

## PR 3 — v1 scope contract + capabilities manifest note (AC-SPR-5, AC-SPR-7, AC-SPR-8)

Objective: document the binding contract and the pre-v1 breaking change. Files: `contracts/scope.md`, `docs/ecosystem/capabilities-and-conformance.md`. Docs-only; no code, schema, policy, or `DRENYRA_DEFAULT_SCOPE` change (AC-SPR-8).

- [x] S3.1 GREEN — Add the v1 scope-contract section to `contracts/scope.md` (AC-SPR-5, FR-SPR-5, NFR-SPR-5, D-SPR-5): effective scope = explicit scope parameters validated against the authenticated principal's membership scope when identity is present (exact-match, `FD-SPR-1`); unauthenticated/reference surfaces stay caller-asserted and are reference mode only (`FD-SPR-3`); institutional surfaces orthogonal (`FD-SPR-4`); typed denial = `TENANT_SCOPE_MISMATCH`/`COMPANY_SCOPE_DENIED` (`FD-SPR-5`); multi-company principals as a future allowed-set extension (`FD-SPR-2`); store boundary — adapter enforcement does not protect direct Go callers (`NFR-SPR-5`). States the four frozen semantics verbatim-consistent and names the typed denial. <!-- sdd-owner: implementation -->
- [x] S3.2 GREEN — Add the breaking-change note to `docs/ecosystem/capabilities-and-conformance.md` (AC-SPR-5, FR-SPR-5): authenticated calls whose scope differs from the principal's membership now fail closed with the typed scope-denied, effective pre-v1 (0.x). No policy version, check-order, or contract-version bump (`NFR-SPR-6`). <!-- sdd-owner: implementation -->
- [x] S3.3 TRIANGULATE — Final slice-3 verification gate covering the contracts/manifest documentation review (AC-SPR-5, AC-SPR-7, AC-SPR-8): a reviewer grep over the whole change diff finds no new error code, no new HTTP status mapping, no fallback-on-mismatch path, and no schema/policy/store/`DRENYRA_DEFAULT_SCOPE` change; `git diff -- contracts/scope.md docs/ecosystem/capabilities-and-conformance.md` is limited to the v1 contract section and the manifest note. No doc claims a production isolation guarantee for reference-mode surfaces. <!-- sdd-owner: implementation -->

**Verification gate PR 3 (final whole-change gate):** config `verify_order` green (`npm run typecheck` → `go vet ./...` → `gofmt -l .` → `go test ./...` → `npm test`); `go test ./internal/core -run TestGoldenVectorsGo` green (no shared Go↔TS semantics change, NFR-SPR-4/AC-SPR-9); greps confirm no new error code, no new status mapping, no fallback path, and no schema/policy/store/`DRENYRA_DEFAULT_SCOPE` change across the diff (AC-SPR-7, AC-SPR-8).

---

## Verification gates per PR

- PR 1: `go test ./internal/server` then `go test ./...`; `http.go` diff limited to per-handler binding calls; exhaustiveness guard green.
- PR 2: `go test ./internal/server ./cmd/drenyra-engram` then `go test ./...`; exhaustiveness guard green; `DRENYRA_DEFAULT_SCOPE` construction untouched.
- PR 3: config `verify_order` + `TestGoldenVectorsGo`; docs diff limited to the v1 section + manifest note; reviewer greps for AC-SPR-7/AC-SPR-8.
- Full chain gate per PR boundary: `npm run typecheck` → `go vet ./...` → `gofmt -l .` → `go test ./...` → `npm test`.

## Cross-cutting checklist

- [x] Conventional commit per atomic milestone (`feat:` for HTTP/MCP/CLI binding, `docs:` for slice 3); no AI attribution. <!-- sdd-owner: implementation -->
- [x] Strict TDD per slice: the named failing binding tests land RED before their helper/application lands; green at each slice boundary with `go test ./...` and `npm test` (NFR-SPR-4). <!-- sdd-owner: implementation -->
- [x] Scope stays structural and fails closed on mismatch; cross-tenant invisibility extended at the adapter boundary, never weakened (IR-2, NFR-SPR-1). <!-- sdd-owner: implementation -->
- [x] Non-authorization boundary intact: the binding grants no authority; it only constrains already-authorized callers' scope (IR-3). <!-- sdd-owner: implementation -->
- [x] No shared Go↔TS semantic change; `TestGoldenVectorsGo` green (IR-4, NFR-SPR-4). <!-- sdd-owner: implementation -->
- [x] No `any` in TypeScript (IR-5); no schema/policy/store/`DRENYRA_DEFAULT_SCOPE` change and no new error code or status mapping (AC-SPR-7, AC-SPR-8, NFR-SPR-6). <!-- sdd-owner: implementation -->

## Definition of done

- [x] All tasks checked; every acceptance criterion AC-SPR-1…AC-SPR-9 in `spec.md` verified green by its mapped test or frozen contract per the traceability in `design.md` (D-SPR-5/D-SPR-6). <!-- sdd-owner: implementation -->
- [x] Full gates per config verify_order green at every PR boundary, plus `go test ./internal/core -run TestGoldenVectorsGo` (AC-SPR-9). <!-- sdd-owner: implementation -->
- [x] Cross-tenant matrix + `TestCrossTenantMatrixExhaustiveness` stay green with the identity-bound rows added and the catalog method set unchanged (NFR-SPR-3, AC-SPR-6). <!-- sdd-owner: implementation -->
- [x] Chain fully merged to main via stacked-to-main; delivery and chain strategy recorded before apply (Review Workload Guard decision). <!-- sdd-owner: parent -->

    Strategy recorded in the tasks header: `ask-on-risk` / `stacked-to-main` (owner-approved), chain PR 1 → PR 2 → PR 3.

## Parent lifecycle gates (post-apply, grouped)

- [x] Start or reuse bounded review for each stacked-to-main PR boundary after its normalization + candidate freeze (PR 1 → PR 2 → PR 3); one correction budget per candidate; no reviewer launched by apply. <!-- sdd-owner: parent -->
- [x] After the final PR merges, run the verify phase (`sdd-verify`) against AC-SPR-1…9 and this tasks list; remediate only through the bounded correction path. <!-- sdd-owner: parent -->
- [x] Archive the change only when verify reports all criteria green and the full chain is merged. <!-- sdd-owner: parent -->
