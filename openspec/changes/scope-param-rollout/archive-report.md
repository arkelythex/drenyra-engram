# Archive Report — scope-param-rollout

> Phase: archive · Artifact: archive-report · Status: closed
> Inputs: proposal + spec + design + tasks + apply-progress + verify-report.

## Outcome

scope-param-rollout is COMPLETE and archived. Identity→scope binding is enforced
at all three adapter surfaces (HTTP, MCP, CLI) with exact-match semantics and
frozen typed denial codes; unauthenticated/reference surfaces are unchanged;
`requireToken`, the store, the authorization policies, `DRENYRA_DEFAULT_SCOPE`
construction, and the cross-tenant catalog method set were never touched.

## Final state facts (supersede intermediate snapshots)

- All tasks.md checkboxes marked; apply-progress and verify-report written after
  the final whole-change gate (which ran AFTER the last code edit).
- Verify: PASS — AC-SPR-1…9 all green with mapped evidence (verify-report.md).
- Gates at the final boundary: `npm run typecheck` clean · `go vet ./...` clean ·
  `gofmt -l .` clean · `go test ./...` all packages ok · `npm test` 385/385 ·
  `TestGoldenVectorsGo` ok.
- Delivery: three atomic conventional commits (feat HTTP binding → feat MCP+CLI
  binding → docs), review mode clone-off (user decision) — ordinary repository
  policy per the tasks' recorded `ask-on-risk` / `stacked-to-main` decision.

## Handoff notes

- `contracts/scope.md` now carries the v1 scope contract (identity→scope
  binding) — subsequent adapter surfaces MUST honor it.
- The `/mcp` identity plumbing (`HandleMessageContext` + `authenticate` in the
  route chain) is the seam future authenticated MCP tools build on.
- The two apply deviations (centralized `httpQueryScope` binding; identity
  plumbing built at `/mcp`) are recorded in apply-progress.md for reviewers.
