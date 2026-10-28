<!--
Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents; no float is ever used for money; version/sequence numbers are JSON integers, never floats.
-->

## Scope

_What does this PR change, and why? One short paragraph. Link the issue/mission if any._

## Files touched

_List every file changed. For each: one line on what changed._

## Files NOT touched

_What did you deliberately NOT change, and why? This saves reviewers from hunting for expected-but-absent changes._

## Review path

_Where should review start, and in what order? What is the riskiest decision in this PR? What did you check before opening it? Note the review lens implied by the change (R0–R3)._

## Workload forecast

- Estimated changed lines: `N`
- Estimated review time: `N minutes`
- Risk tier: `R0 | R1 | R2 | R3`

> **Chained-PR note:** if this PR exceeds ~400 changed lines, it should be split into a **chained PR sequence** (stacked-to-main or feature-branch chain) so each review stays focused and each diff is reviewable. Ask before merging a single oversized PR.

## Tests

- [ ] `go vet ./...`, `gofmt -l internal/ cmd/` (prints nothing), and `go test ./... -count=1` pass
- [ ] Typecheck and tests pass if the TypeScript mirror changed (`bun run typecheck`, `bun run test`)
- [ ] Conformance vectors updated and passing in Go and TypeScript if behavior changed (`testdata/golden/`; search ranking / lifecycle / relations / scope filtering)
- [ ] Packed-install smoke test (if the package surface changed)

## Accounting safety

- [ ] Test data and examples are synthetic: no real taxpayer data, RUCs, credentials, or tokens
- [ ] No fail-closed behavior weakened (scope, runtime-mode gate, closed period, encryption, token validation)
- [ ] No path gives an agent or an unauthenticated caller approval authority
- [ ] Non-authorization boundary unaffected (if contracts/provenance changed)

## Contracts

- [ ] No change under `contracts/`, **or** the contract version, Go, TypeScript mirror, golden vectors, and docs all changed together (see CONTRIBUTING.md → Contract changes)

## Docs-as-code

- [ ] Docs updated in this same PR; relative links resolve and `git diff --check` is clean
- [ ] `CHANGELOG.md` updated under `## [Unreleased]` for user-visible changes
