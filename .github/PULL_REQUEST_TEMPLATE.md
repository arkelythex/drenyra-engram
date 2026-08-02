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

- [ ] Typecheck passes
- [ ] Tests pass
- [ ] Conformance vectors updated and passing (search ranking / lifecycle / relations / scope filtering)
- [ ] Packed-install smoke test (if the package surface changed)

## Docs-as-code

- [ ] Docs updated in this same PR (markdownlint + link check)
- [ ] `CHANGELOG.md` updated
- [ ] Non-authorization boundary unaffected (if contracts/provenance changed)
