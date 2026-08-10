# Tasks — {change-name}

> Phase: tasks · Artifact: tasks · Status: draft
> Inputs: spec + design (required). Strict TDD: not active in this phase (planning).
> Forwarded session settings: delivery_strategy, chain_strategy, pr_boundary (if set).

## Work units

### WU-1 — {short name}

- [ ] 1.1 _Concrete step (test-first where the step has behavior): write failing test → implement → green._
- [ ] 1.2 _…_

### WU-2 — {short name}

- [ ] 2.1 _…_

## Verification gates per work unit

- `go test ./...` (or scoped `go test ./<pkg>` for the slice)
- `npm test` / `npm run typecheck` when TS mirrors are touched
- `go test ./internal/core -run TestGoldenVectorsGo` when golden vectors change

## Cross-cutting checklist

- [ ] Conventional commit per atomic milestone; no AI attribution.
- [ ] Money uses whole int64/BigInt cents; no float path.
- [ ] Scope checks fail closed; cross-tenant invisibility tested.
- [ ] Non-authorization boundary intact (new surface cannot approve/post/file).
- [ ] Docs updated in the same PR (docs-as-code).
- [ ] `go vet ./...` and `gofmt -l .` clean.

## Review Workload Forecast

- Estimated changed lines: _N_
- 400-line budget risk: _Low / Medium / High_
- Chained PRs recommended: _Yes / No_
- Decision needed before apply: _None / describe_

## Definition of done

- [ ] All tasks checked; all acceptance criteria in spec verified green.
- [ ] Full gates: `go test ./...` + `npm test` + `npm run typecheck`.
