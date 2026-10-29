# Feature: sdd-to-odd-migration

**Objective:** retire the SDD/OpenSpec workflow in Drenyra Engram and make ODD the only workflow, as Drenyra Shell already did.
**Original branch:** `chore/purge-sdd-full-odd` · **Follow-up branch:** `chore/project-excellence-foundations` · **Origin:** the ecosystem request to sync Drenyra AI, Drenyra Shell, and Drenyra Engram, and to remove legacy SDD in favour of full ODD.

## Specs

- **S1 — "eliminar sdd legacy full ODD de gentleman".** `openspec/` is deleted: 7 changes, all `status: archive`; 5 change templates; `config.yaml`.
- **S2 — No requirement is lost.** Each change's `spec.md` moves verbatim to `specs/<change>/spec.md`. Proposal, design, task, and report artifacts stay in git history.
- **S3 — No rule is lost.** The project conventions and verification order in `openspec/config.yaml` move to a new `AGENTS.md`.
- **S4 — No pending work is dropped silently.** Unchecked tasks in archived changes are checked against the code and recorded in L3.
- **S5 — No live reference to `openspec/` in this repository.** Links into Drenyra AI's program master (`drenyra-ai/openspec/programs/drenyra-dominion`) are out of scope: they change when Drenyra AI converts that program master.
- **S6 — Coexistence with in-flight work.** Retire the fiscal workflow on the current feature branch without waiting for every fiscal PR to merge. Preserve the unique fiscal requirements, design rationale, historical evidence, and unfinished Slice 7 work. This is a documentation migration, not a claim that the fiscal stack is merged or that its pending work is complete.
- **S7** (user, verbatim): "hey bro has limpiea y elimiina todo sobre SDD , ahora el flujo de vanguardia en gentle-shell 4.5.1 es con ODD y mas".
- **S8** (user selection, verbatim): "Retirar el workflow SDD/OpenSpec (recomendado)". Preserve historical requirement IDs, golden vectors, valid external URLs, contracts, and Git history.
- **S9** (user, verbatim): "perfect commit + push + limpieza de ramas y mas de forma autonoma".

## Tasks

- [x] **T1** — Move the 7 specs, delete `openspec/`. *(S1, S2)*
- [x] **T2** — `AGENTS.md` with the conventions and verification order; ODD in `CONTRIBUTING.md`. *(S3)*
- [x] **T3** — Fix the 3 local `openspec/` references; CHANGELOG entry. *(S5)*
- [x] **T4** — Verify unchecked tasks against the code. *(S4)*
- [x] **T5** — Migrate the fiscal requirements verbatim to `specs/`, historical records to `docs/decisions/`, and unfinished work to an ODD feature document. *(S2, S4, S6–S8)* — done; route: parent; commit: `34748450340af73b98ae5eb2365f2c174a8ca032`. All five payload hashes matched; five unfinished Slice 7 outcomes and final evidence gap retained.
- [x] **T6** — Repoint active comment references and workflow guidance without changing executable code or historical IDs. *(S3, S5, S7–S8)* — done; route: parent; commit: `34748450340af73b98ae5eb2365f2c174a8ca032`. Eleven changed source files have identical executable code; the sole protected historical citation is explicitly documented.
- [x] **T7** — Verify byte preservation, pending-work continuity, links, unchanged code/contracts/goldens/user edits, and the absence of an active OpenSpec tree. *(S2–S8)* — done; route: parent; commit: `34748450340af73b98ae5eb2365f2c174a8ca032`. Structural checks and focused public checks passed; no native verdict is implied.
- [x] **T8** — Publish the verified current branch with a normal push and upstream; retain the non-destructive cleanup boundary and existing PR heads. *(S8–S9)* — done; route: parent; publication tip: `5bde53fbfe84ab792a6f5b589c4b054ed157f8ac`; evidence commit: this publication record. Job 23 push/readback and corrected job 25 postconditions passed.

## Log

### L1 — 2026-10-27 — original request (verbatim)

> yes te autorizo todo avanza ya que mi oobjetivo es que Drenyra-ai Drenyra-shell + Drenyra-engram esten bien sincornizados actualizados bien configurados 10/10 y eliminar sdd legacy full ODD de gentleman

### L2 — 2026-10-27 — inventory

`openspec/` on `main` held 63 files: the 7 archived changes `audit-register-closure`, `scope-param-rollout`, `sdd-060-at-rest-encryption`, `sdd-060-confidence-required`, `sdd-060-legacy-reencrypt`, `sdd-060-tenant-cli`, `v1-readiness`; 5 `change-templates`; and `config.yaml`. No code, test, script, Makefile, or CI workflow reads `openspec/`. Three docs referenced paths inside it.

### L3 — 2026-10-27 — unchecked tasks in archived changes

- `sdd-060-confidence-required`: 16 unchecked. The work is implemented on `main`: `Confidence float64` is required (`internal/core/types.go`), `internal/store/migration_v17.go` and its test exist, and commit `e8d227d feat(confidence): required confidence field and schema v17` delivered it. The boxes were simply never ticked.
- `v1-readiness`: 5 unchecked, all SDD-orchestrator ceremony (`sdd-verify`, `sdd-archive`, review-workload decisions). Its archive report states that all 11 ACs are green and the chain is merged. Obsolete under ODD.

### L4 — 2026-10-27 — version incoherence noticed (out of scope here)

`CHANGELOG.md` describes v0.7–v0.9 milestones as unreleased, the latest published tag is `v0.2.1`, and `package.json` says `0.0.1-prealpha.1`. Drenyra Shell pins a snapshot of commit `6a371a9`. Reconciling this is a release decision for the maintainer, not part of this migration.

### L5 — 2026-10-27 — verification

- The diff contains only Markdown files and deletions under `openspec/` (no code, test, or config that runs).
- Local: `go vet` clean, `gofmt -l` empty, `go build` OK, `go test ./... -count=1` 10/10 packages ok, `bun run typecheck` OK.
- Local `bun run test`: 285 passed, 34 failed, all with `TypeError: Invalid JWK OKP key` (Ed25519 key import). The cause is the local runtime (Node 26.7.0, Bun 1.4.0), the Node 26 defect tracked as #45. CI pins Bun 1.3.11; CI is the verification of record for the TS suite.

### L6 — Fiscal follow-up request (verbatim)

> hey bro has limpiea y elimiina todo sobre SDD , ahora el flujo de vanguardia en gentle-shell 4.5.1 es con ODD y mas

### L7 — Scope decision (verbatim)

> Retirar el workflow SDD/OpenSpec (recomendado)

The selected scope retires workflow artifacts, not historical identifiers or business requirements. Fiscal slices 5/6 and the original PRs are not assumed merged. Migrate all five fiscal records without losing their payloads, keep unfinished work explicitly blocked on separate fiscal delivery, and preserve the existing native review authority. Do not change contracts, golden vectors, external program links, unrelated user tests, or the installed Gentle Shell configuration. No runtime behavior changes, so no meaningful RED test applies; use content/hash, link, and executable-source invariance checks instead. The work-unit boundary is `645721f`, which closes the prior non-destructive audit; prior published/tested tip `6e57f93` is not rewritten. T5 preservation checks passed: all five original payloads match their pre-move SHA-256 values, and the five unfinished Slice 7 outcomes plus the final evidence gap remain explicitly blocked in the ODD continuity document.

### L8 — Reference-check correction and source invariance

The first T6 structural check exited 1: the initial map omitted six source comment references. Five additional citations were repointed; the sixth is the historical header in the user-modified `internal/store/fiscal_binding_enforcement_test.go` and remains untouched. The passing check explicitly allowlists only that exact header and verifies the original binary user diff. It does not claim zero textual historical references. All eleven changed Go/TypeScript files retain byte-identical executable code, with only header/line comments changed. Active project guidance now points to `specs/`, `odd/tasks/`, and `docs/decisions/`; historical records are not workflow instructions.

### L9 — Follow-up verification

- Five original payload hashes matched, including the unchanged fiscal spec. All five unfinished Slice 7 outcomes and the final evidence gap remain recorded.
- The link check passed for 121 relative links/anchors across 12 changed Markdown documents. `gofmt` is clean for the 10 changed Go files. No CI, script, or package workflow depends on the retired tree.
- Job 21 exited 0: `go test ./internal/core -run '^TestGoldenVectorsGo$' -count=1` exited 0; `npm run typecheck` exited 0.
- Contracts, golden vectors, historical IDs/external URLs, and the two unrelated user-test diffs are unchanged. Only exact migration surfaces were staged.
- Full runtime suites were not rerun: executable code is byte-identical. No meaningful RED applies to this documentation/comment-only change. The previously reported Node 26 crypto issue is not claimed fixed.
- The human-owned RDD switch reads on. Native assessment uses the committed work-unit range to exclude unrelated unstaged user edits; no review verdict or delivery approval is asserted by these functional checks.
- Risk: none — source changes are comments only, with executable-byte invariance proved; no shared parser, option, guard, contract, concurrency, data, or environment behavior changes.

### L10 — Work-unit evidence and native assessment

Implementation and functional evidence are committed in `34748450340af73b98ae5eb2365f2c174a8ca032`, based on `645721ff7c551fe26844eeda707a65ec08925748`. Git rename detection reports 23 files and 179 authored diff lines (137 insertions, 42 deletions); the five record moves preserve their payloads.

Committed-only native assessment returned `nativeRisk: medium`, `reviewDue: false`, `reviewDueReason: under_budget`, `writerSelfVerification: true`, and `independentVerifier: false`. Native accounting reports 28 changed paths and 3,399 changed lines without Git's rename-aware presentation; this is a different metric, not hidden review workload. No review was started, no native verdict or authority was created/consumed, and the unrelated MCP lineage is still unresolved. At this checkpoint the implementation and closure record were local; no push, PR mutation, branch deletion, or history rewrite had occurred. Publication was authorized separately afterward, as recorded below.

### L11 — Publication authorization and first push

> perfect commit + push + limpieza de ramas y mas de forma autonoma

Job 23 exited 0: a normal push published the five existing commits through `5bde53fbfe84ab792a6f5b589c4b054ed157f8ac` to `origin/chore/project-excellence-foundations`, and exact remote-SHA/upstream readback passed. The existing PR #67 was not enlarged: its published head remains `6e57f93`; direct replacement would have produced 465 authored diff lines, versus 246 incremental lines for the separately published current branch. No duplicate implementation commit, force push, stash, reset, branch deletion, PR creation, merge, or protected-label mutation was performed.

The first post-push inspection (job 24) exited 1 while parsing colorized GitHub JSON, not during push or CI. Inherited `CLICOLOR_FORCE=1` explains the ANSI rendering; a local reproduction confirmed strict JSON parsing works after removing only color sequences. A corrected command-scoped, read-only inspection is used for the missing remote/CI checks, without replaying the push or modifying global configuration. The earlier non-destructive branch-cleanup decision remains in force.

### L12 — Publication postconditions

Job 24's local branch/backup, worktree-registration, and exact user-diff assertions passed before its JSON parser failed. Corrected read-only job 25 exited 0: PR #67 retains its original head, base, and open/draft state; all four audited remote references are retained; the new remote branch matches the published SHA. `gh run list` returned `[]` (exit 0): there are no CI runs for this branch push. CI/Go Engine run on `main` pushes or PRs, fuzzing is scheduled/manual, and release/image workflows are tag/manual-driven. No CI success is asserted for this publication.

The five previously local work units are published, and the current branch now tracks its matching origin branch. The two unrelated user tests remain unstaged with their original binary diff. All audited branches, worktrees, backups, and the unresolved native review are preserved. No runtime suite was rerun for publication-only metadata, and the prior native assessment of the published range returned medium with `reviewDue: false` and self-verification sufficient.
