# Verify Report — scope-param-rollout

> Phase: verify · Artifact: verify-report · Status: PASS
> Inputs: spec + tasks + apply-progress. Full gate per config `verify_order` run at the whole-change boundary (after slice 3).

## Result: PASS — all acceptance criteria verified green

| Criterion | Evidence |
| --- | --- |
| AC-SPR-1 HTTP binding on generic reads | `TestHTTPIdentityScopeBinding` — every `httpQueryScope(r,false)` handler × 4 rows (denial / match / shared-token / institutional) |
| AC-SPR-2 MCP binding | `TestMCPIdentityScopeBinding` (unit) + `TestMCPHTTPIdentityScopeBinding` (end-to-end `/mcp`) |
| AC-SPR-3 CLI binding | `TestCLIIdentityScopeBinding` (search + context: denial / match / session-less) |
| AC-SPR-4 no API/effect on denial | Denial rows assert typed 403/tool-error BEFORE dispatch; no mutation side-effects observed |
| AC-SPR-5 contracts + manifest | `contracts/scope.md` v1 section + `docs/ecosystem/capabilities-and-conformance.md` pre-v1 breaking-change note |
| AC-SPR-6 cross-surface consistency + matrix | `TestScopeDeniedCrossSurfaceConsistency` (same frozen code on HTTP + MCP); `TestCrossTenantMatrix` + `TestCrossTenantMatrixExhaustiveness` green with catalog method set unchanged |
| AC-SPR-7 no new error code / status / fallback | Reviewer greps over the diff: 0 new `http.Status` mappings, 0 new error constants, no fallback-on-mismatch path |
| AC-SPR-8 no schema/policy/store/`DRENYRA_DEFAULT_SCOPE` change | Diff limited to adapter files + docs + tests; `DRENYRA_DEFAULT_SCOPE` construction untouched |
| AC-SPR-9 no shared Go↔TS semantic change | `go test ./internal/core -run TestGoldenVectorsGo` green; `npm test` 385/385 |

## Frozen bindings honored (FD-SPR-1…6, NFR-SPR-1…6, IR-1…5)

- FD-SPR-1 exact-match, no fallback, no rewrite — asserted by tests and grep.
- FD-SPR-2 multi-company set semantics — helper uses `contains` over `CompanyScopes()`; documented as future extension in contract.
- FD-SPR-3 shared-token/reference unchanged — shared-token-only rows green on all three surfaces; `requireToken` untouched.
- FD-SPR-4 institutional orthogonal — institutional rows byte-identical with/without principal.
- FD-SPR-5 typed denial codes — `TENANT_SCOPE_MISMATCH` / `COMPANY_SCOPE_DENIED` frozen; no new codes.
- FD-SPR-6 never resolves a principal where none exists — `PrincipalFromContext` gate; CLI treats unresolved session as no principal.
- NFR-SPR-1 fail-closed on mismatch — all denial rows fail before any operation.
- NFR-SPR-3 exhaustiveness guard — catalog method set unchanged, guard green.
- NFR-SPR-4/5/6 — golden parity green; store boundary documented; no version bump.
- IR-1 no money fields in new code; IR-3 binding grants no authority; IR-4/5 no TS change, no `any`.

## Gate results

- `npm run typecheck` — clean.
- `go vet ./...` — clean.
- `gofmt -l .` — clean.
- `go test ./...` — all packages ok (cmd, auth, authz, core, receipts, search, server, store, sync).
- `npm test` — 26 files, 385 tests passed.
- `go test ./internal/core -run TestGoldenVectorsGo` — ok.

## Notes

- Delivery recorded in tasks header: `ask-on-risk` / `stacked-to-main` (owner-approved). Review mode was clone-off for this session (user decision) — delivery under ordinary repository policy, three atomic conventional commits per slice.
- Two apply deviations documented in apply-progress.md (centralized `httpQueryScope` binding; identity plumbing built at `/mcp`). Neither touches a frozen binding.
