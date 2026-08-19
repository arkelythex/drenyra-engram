# Apply Progress — scope-param-rollout

> Phase: apply · Artifact: apply-progress · Status: done
> Inputs: spec + design + tasks (frozen bindings FR-SPR-1…5, NFR-SPR-1…6, AC-SPR-1…9, IR-1…5, FD-SPR-1…6).

## Slice 1 — HTTP identity→scope binding (AC-SPR-1, AC-SPR-4, AC-SPR-6) — DONE

- `internal/server/scope_binding.go` (new): `bindScopeToPrincipal` (exact-match, frozen typed codes) + `contains` + `boundScope(ctx, scope)` context helper. The design's `bindScope(w, r, scope)` middleware-style method was implemented then REMOVED as dead code: the binding centralizes inside `httpQueryScope` (single choke point covering every generic read handler, institutional early-return preserved — FD-SPR-4), which the design's simpler alternative explicitly allows and the test suite proves equivalent-or-stronger.
- `internal/server/http.go`: `httpQueryScope` binds the derived effective scope to `PrincipalFromContext` when present (FD-SPR-6: never resolves a principal where none exists; `requireToken` untouched — FD-SPR-3).
- `internal/server/scope_binding_http_test.go` (new): `TestHTTPIdentityScopeBinding` — table-driven over EVERY `httpQueryScope(r, false)` handler (Save, Get, GetByTopic, Chain, Search, Context, Compare, Relations, Transitions, review queue/detail, rule show/history/impact, reconstructibility, close create, lifecycle export, holds/retention/objects) with four row types each: principal-A + scope-B → 403 typed `TENANT_SCOPE_MISMATCH`/`COMPANY_SCOPE_DENIED` + no API effect; principal-A + scope-A → unchanged; shared-token-only → unchanged (FD-SPR-3); institutional → byte-identical with/without principal (FD-SPR-4).
- `TestCrossTenantMatrixExhaustiveness` stays green: `crossTenantCatalog()` method set untouched (NFR-SPR-3).

Verification gate PR 1: `go test ./internal/server` + `go test ./...` green; `gofmt -l .` + `go vet ./...` clean.

## Slice 2 — MCP + CLI binding + cross-surface consistency (AC-SPR-2, AC-SPR-3, AC-SPR-6) — DONE

- Identity plumbing (the design's `mcp.go:1450-1455` premise did not exist — built it): `HandleMessageContext(ctx, raw)` (new principal-aware entry; `HandleMessage` delegates with `context.Background()` — stdio remains unbound reference mode, FD-SPR-3); ctx threaded through `dispatch`/`handleToolsCall`/`handleNotification`.
- `/mcp` HTTP route: mounted `h.requireToken(h.authenticate(h.handleMCP))` — `requireToken` untouched (MUST-NOT), `authenticate` enriches the request context with a verified principal when a session bearer is present (never blocks). `handleMCP` passes `r.Context()` to `HandleMessageContext`. With a shared token configured, reference mode is unchanged; without one, session callers bind.
- Binding: `decodeScope(ctx, raw)` now enforces `boundScope` after parse (single enforcement point for all 22 accounting_* string-scope tool sites) + explicit `boundScope` checks at the five core.Scope-object tool sites (`engram_save`, `engram_get_by_topic`, `engram_chain`, `engram_search`, `engram_context`). Typed denial returns through the existing MCP tool-error envelope (`errTextContent`). `DRENYRA_DEFAULT_SCOPE` construction untouched.
- `internal/server/scope_binding_mcp_test.go` (new): `TestMCPIdentityScopeBinding` (bound B + scope A → typed denial before dispatch; bound B + scope B → unchanged; unbound → unchanged; object-scope and decodeScope paths covered) + `TestMCPHTTPIdentityScopeBinding` (end-to-end: session bearer over `/mcp` → denial; shared-token-only → unchanged) + `TestScopeDeniedCrossSurfaceConsistency` (same frozen code TENANT_SCOPE_MISMATCH on HTTP 403 envelope and MCP tool text for the same bound pair — AC-SPR-6).
- CLI: `cmd/drenyra-engram/scope_binding.go` (new) — CLI-local twin `bindScopeToPrincipal` + `contains` + `cliBindScope(scope, dbPath)` (loads session file; no session → unbound reference mode, FD-SPR-3; invalid session → no principal, FD-SPR-6; mismatch → typed denial + exit 1 before store data access). Applied in `cmdSearch` (`main.go ~300`) and `cmdContext` (`~348`) right after `cliCompanyScope`.
- `cmd/drenyra-engram/scope_binding_cli_test.go` (new): `TestCLIIdentityScopeBinding` — session A + `--company B` → `COMPANY_SCOPE_DENIED` + non-zero exit + no search data access; session A + `--company A` → unchanged; session-less → unchanged.

Verification gate PR 2: `go test ./internal/server ./cmd/drenyra-engram` + `go test ./...` green (incl. unchanged `TestCrossTenantMatrixExhaustiveness` and `TestMCPServerDefaultScopeFailClosed`); `npm test` 385/385; `gofmt`/`vet` clean.

## Slice 3 — v1 scope contract + capabilities manifest note (AC-SPR-5, AC-SPR-7, AC-SPR-8) — DONE

- `contracts/scope.md`: new "v1 scope contract — identity→scope binding" section (effective scope = explicit params validated against authenticated principal's membership, exact-match FD-SPR-1; reference mode caller-asserted FD-SPR-3; institutional orthogonal FD-SPR-4; typed denial FD-SPR-5; multi-company future FD-SPR-2; store boundary NFR-SPR-5; period not an identity dimension).
- `docs/ecosystem/capabilities-and-conformance.md`: "Pre-v1 breaking change (scope-param-rollout, 0.x)" note — authenticated out-of-membership calls fail closed with the typed denial, effective pre-v1; no policy/check-order/contract-version bump (NFR-SPR-6).

Verification gate PR 3 (whole change): config `verify_order` green (`npm run typecheck` → `go vet ./...` → `gofmt -l .` → `go test ./...` → `npm test`); `TestGoldenVectorsGo` green; reviewer greps: no new error code, no new HTTP status mapping, no fallback-on-mismatch path, no schema/policy/store/`DRENYRA_DEFAULT_SCOPE` change.

## Deviation notes

- D-SPR-2 chose per-handler binding; implementation centralizes in `httpQueryScope`. Equivalent-or-stronger (covers all call sites by construction), test-proven across every generic read handler, and the dead `bindScope` method was removed rather than shipped. No frozen binding deviated.
- The design referenced pre-existing identity plumbing at `mcp.go:1450-1455`; none existed — the `/mcp` identity context was built (slice 2) with `requireToken`'s shared-token guard untouched (FD-SPR-3 MUST-NOT honored).
