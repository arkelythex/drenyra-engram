# Archive Report — sdd-060-tenant-cli

> Phase: archive · Artifact: archive-report · Status: closed
> Inputs: proposal + spec + design + tasks + apply-progress + verify-report.

## Outcome

sdd-060-tenant-cli is COMPLETE and archived. SDD-060 Fases 1 y 3 (tenant CLI
surface) delivered: `tenant list` (operator enumeration, ids/counts only) and
`tenant consolidate` (topic-key drift detection with a golden-parity fold;
`--apply` merges drifted chains into the canonical chain via the existing
audited supersede path; dry-run default with zero writes; adversarial cross-RUC
isolation proven).

## Final state facts (supersede intermediate snapshots)

- All tasks.md checkboxes marked after the final gate (which ran FRESH
  `-count=1` AFTER the last code edit).
- Verify: PASS — AC-TEN-1…7 all green with mapped evidence (verify-report.md).
- Gates at the final boundary: `go test ./... -count=1` all ok · `npm test`
  386/386 · typecheck clean · vet clean · gofmt clean · `TestGoldenVectorsGo` ok.
- Delivery: three atomic conventional commits (feat tenant list → feat
  consolidate detection → feat consolidate apply + docs), review clone-off
  (user decision) — ordinary-policy commits.

## Handoff notes

- `FoldTopicKey` (internal/core + core/topic-fold.ts) is now a golden-parity
  contract (`topic-fold`); future normalization logic MUST extend it the same
  way.
- `DriftCandidates` semantics: empty period = whole tenant, groups per
  (folded key, period); merges always stay inside one exact period scope.
- `tenant list` is the operator surface pattern (ids/counts only) — any future
  operator command should follow the same content-free enumeration rule.
- Encryption slices (at-rest por tenant + sync) are the next change (Unit C).
