# Feature: sdd-to-odd-migration

**Objective:** retire the SDD/OpenSpec workflow in Drenyra Engram and make ODD the only workflow, as Drenyra Shell already did.
**Branch:** `chore/purge-sdd-full-odd` · **Origin:** the ecosystem request to sync Drenyra AI, Drenyra Shell, and Drenyra Engram, and to remove legacy SDD in favour of full ODD.

## Specs

- **S1 — "eliminar sdd legacy full ODD de gentleman".** `openspec/` is deleted: 7 changes, all `status: archive`; 5 change templates; `config.yaml`.
- **S2 — No requirement is lost.** Each change's `spec.md` moves verbatim to `specs/<change>/spec.md`. Proposal, design, task, and report artifacts stay in git history.
- **S3 — No rule is lost.** The project conventions and verification order in `openspec/config.yaml` move to a new `AGENTS.md`.
- **S4 — No pending work is dropped silently.** Unchecked tasks in archived changes are checked against the code and recorded in L3.
- **S5 — No live reference to `openspec/` in this repository.** Links into Drenyra AI's program master (`drenyra-ai/openspec/programs/drenyra-dominion`) are out of scope: they change when Drenyra AI converts that program master.
- **S6 — Coexistence with in-flight work.** The open fiscal stack (PRs #35–#39) and the local `chore/project-excellence-foundations` branch add `openspec/changes/fiscal-runtime-foundations/`. This migration does not touch that path. When the stack lands, that change moves to `odd/tasks/` in a follow-up.

## Tasks

- [x] **T1** — Move the 7 specs, delete `openspec/`. *(S1, S2)*
- [x] **T2** — `AGENTS.md` with the conventions and verification order; ODD in `CONTRIBUTING.md`. *(S3)*
- [x] **T3** — Fix the 3 local `openspec/` references; CHANGELOG entry. *(S5)*
- [x] **T4** — Verify unchecked tasks against the code. *(S4)*
- [ ] **T5** — After the fiscal stack merges, move `openspec/changes/fiscal-runtime-foundations/` to `odd/tasks/` and its spec to `specs/`. *(S6)* — blocked on PRs #35–#39

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
