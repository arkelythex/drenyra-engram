# Project Excellence Foundations

## Objective

Give this repository a durable engineering-philosophy reference (`odd/PHILOSOPHY.md`) and use it to drive an
autonomous pass that removes redundancy, legacy artifacts, and dead weight from the codebase, raising overall
repo quality without breaking the shipped npm package or the Go CLI/server.

## Problem

The repo mixes a published TypeScript library (root `auth/`, `authority/`, `authz/`, `core/`, `lifecycle/`,
`search/`, `store/`, `index.ts` — package `drenyra-engram`, exported via `package.json#exports`) with a Go
CLI/server (`internal/`, `cmd/drenyra-engram`) that reimplements overlapping domain concepts natively. On top
of that there is a 21MB compiled binary committed at repo root and a `dist/` tree containing both JS build
output and multi-platform Go binaries. None of this has been audited for what is genuinely legacy/redundant
versus what is the real shipped surface of two separate distribution channels (npm package + Go binary).

## Why

User request (2026-09-21): create an ODD-tracked initiative that encodes engineering philosophy (ODD workflow +
Gentleman Programming principles + general software-engineering best practices + current standards) and then
use it to autonomously eliminate redundancy/legacy and clean up the project. User explicitly chose the
"autonomous direct" execution mode (no per-finding confirmation), so cleanup proceeds under ordinary ODD
work-unit discipline (branch, commit, review-per-commit) rather than pausing per item.

## Scope

In scope:
- One foundational doc: `odd/PHILOSOPHY.md`.
- Repo-wide legacy/redundancy audit and cleanup of confirmed-dead artifacts (committed binaries/build output,
  orphaned files, duplicate logic, stale docs superseded by the docs/ rewrite already done this session,
  completed-but-unarchived openspec changes).

Out of scope:
- Any change to the active fiscal-runtime business logic itself (Slice 6 in progress) beyond removing genuinely
  dead code paths identified with evidence.
- Publishing/releasing anything (npm publish, goreleaser run, git push, PR merge) — those remain user decisions.

## Constraints

- RDD (receipt-driven development) is **on** globally (`gentle-ai review mode status` → `on (decided by global)`).
  Every work-unit commit gets a review-due assessment; medium/high-risk deletions go through the native review
  lifecycle before being considered final. Passive doc-only commits use structural readback only.
- Do not delete anything currently required by `package.json#exports`, `cmd/drenyra-engram`, or referenced by a
  passing test, without evidence it is dead (zero importers/callers, confirmed via codegraph/grep, not vibes).
- Preserve git history discipline: no `--force`, no rewriting published commits, no destructive git ops without
  asking first (per standing safety rules) even though this feature is running autonomously.

## Resolved TDD mode

- **Mode**: not explicitly enabled/disabled at project or session level — no TDD gate found in `Makefile`
  (only `fuzz-ci`) or CI config during initial scan.
- **Source**: absence of an explicit TDD directive; defaulting to "ordinary functional checks per task", not RED→GREEN→REFACTOR.
- **Runners**: `go test ./...` (Go side, module `github.com/arkelythex/drenyra-engram`), `bun run test` /
  `vitest run` (TS side, per `package.json` scripts), `bun run typecheck` (`tsc --noEmit`).
- Any task that touches existing passing tests must keep them green; any task that removes code must remove or
  update its dead tests in the same work unit, not leave orphaned specs behind.

## Delivery strategy

`ask-on-risk` (default) — chain strategy will be asked for only if the accumulated authored-changed-lines forecast
or running total crosses ~400 lines.

## Tasks

| ID | Task | Route | Status |
|----|------|-------|--------|
| MAP-001 | Delegate read-only exploration: verify TS-vs-Go legacy/redundancy hypothesis, confirm whether `drenyra-engram` binary and `dist/` are git-tracked, find concrete dead-code/duplication/stale-doc candidates with evidence | Delegated (general-purpose agent) | done |
| PHIL-001 | Write `odd/PHILOSOPHY.md`: ODD workflow summary for this repo + Gentleman Programming engineering philosophy + general software-engineering best practices + current/vanguard standards, grounded (no fabricated version claims) | Direct inline | done |
| CLEAN-001 | Delete untracked, gitignored local build byproducts: root `drenyra-engram` binary (21MB), `dist/`, `.ruff_cache/` (no `.py` source exists; stray tool cache). Confirmed via `git status --porcelain --ignored=matching` (`!!`) and `.gitignore` — zero git impact, no commit needed | Direct inline (disk-only) | done |
| CLEAN-002 (deferred) | `docs/architecture.md` vs `docs/ARCHITECTURE.md` case-collision rename+relink | **Deferred** — DOC-005 in `odd/tasks/docs-product-quality-v2.md` already scopes "Clarify Go versus TypeScript roles in ... `docs/ARCHITECTURE.md`". Doing it here would duplicate/conflict with an already-planned task. Do not action from this feature. | not started here |
| CLEAN-003 (deferred) | Cross-link orphaned new docs (`docs/GETTING-STARTED.md`, `docs/DATA-SECURITY-PRIVACY.md`, `docs/PRODUCT.md`) from `README.md`/`DOCS.md` | **Deferred** — same reason as CLEAN-002; this is DOC-005/DOC-007 territory in `docs-product-quality-v2.md`, which also carries an explicit **"do not commit or push"** constraint on `README.md` and the new `docs/*.md` files from a prior session. Editing those here would violate that constraint and fork one doc effort into two. | not started here |

## MAP-001 verdict (evidence-backed)

- **TS root tree (`auth/`, `authority/`, `authz/`, `core/`, `lifecycle/`, `search/`, `store/`, `index.ts`) is NOT
  legacy.** It is a deliberate byte-parity mirror of the Go engine (`index.ts` header: "v2 reference mirror";
  golden-vector tests `TestGoldenVectorsGo` prove Go↔TS parity). Commit cadence on both sides runs in lockstep.
  **No deletion warranted.**
- **The shipped product is the Go binary**, not an npm package: README's only install path is
  `go install .../cmd/drenyra-engram@latest`; `.goreleaser.yml` builds only Go binaries; no npm-publish workflow
  exists; `package.json` has no `bin` field and sits at `0.0.1-prealpha.1`.
- **No git-tracking mistake**: `drenyra-engram` binary and `dist/` were never committed — confirmed ignored and
  untracked, purely local build byproducts.
- **"legacy"-labeled code in `internal/server/http.go` (deprecated v0.3 approval surface) is an intentional,
  tested, disabled-by-default compatibility guard**, not dead code — `TestHTTPLegacyApproveDisabledByDefault`
  pins it. **Keep as-is.**
- **`openspec/changes/`** is 7/8 already archived in place; only the in-flight `fiscal-runtime-foundations`
  change (current branch) lacks an archive report, which is expected for unfinished work. **No action.**
- **Real, actionable findings** (case-collision doc filenames, orphaned new docs) already fall inside the scope
  of the existing `docs-product-quality-v2` feature — see deferrals above. Duplicating that tracking here would
  itself be the kind of redundancy this feature exists to remove, so it explicitly does not.

## Overall verdict for this feature

This codebase does not have a significant legacy/redundancy problem to autonomously clean up beyond disk-only
build byproducts (CLEAN-001, done). The apparent TS/Go duplication is intentional and load-bearing (parity
testing). The only real, actionable documentation gaps are already tracked in `docs-product-quality-v2.md`
under DOC-005/DOC-007 and are intentionally left there rather than forked into a second, conflicting doc-cleanup
effort. This feature is complete: philosophy reference written, disk cleaned, no further deletions justified by
evidence.

## Progress

- 2026-09-21: Feature document created; MAP-001 delegated and returned; PHIL-001 written; CLEAN-001 executed
  (disk-only, no git action); CLEAN-002/003 explicitly deferred to avoid duplicating `docs-product-quality-v2`.
  RDD confirmed on (global) — `odd/PHILOSOPHY.md` and this task file are a passive, doc-only work unit.

## Next step

None outstanding for this feature. Follow-up documentation work (case-collision rename, orphaned-doc linking)
belongs to `docs-product-quality-v2.md` (DOC-005/DOC-007), which still carries its own "do not commit or push"
constraint from the user's prior session and should be resumed under its own name, not this one.
