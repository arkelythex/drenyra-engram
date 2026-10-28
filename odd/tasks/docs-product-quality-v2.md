# Drenyra Engram documentation quality v2

## Objective

Make Drenyra Engram's public documentation as usable, coherent, and trustworthy as Gentleman Engram's v2 documentation while giving Drenyra its own accounting-first product identity.

## Problem

The repository contains deep technical documentation, but readers face a long mixed-purpose README, scattered onboarding examples, incomplete data-handling guidance, and contradictory capability counts and maturity claims. The documentation must explain a fiscal institutional-memory product without overstating accounting correctness, authority, compliance, or production readiness.

## Why

Accounting software earns trust through precise boundaries, reproducible workflows, explicit evidence, and honest maturity claims. Documentation is part of that trust surface for operators, integrators, contributors, auditors, and AI agents.

## Scope

- Restructure the public README around product identity, boundaries, audiences, and a short verified path.
- Add task-oriented onboarding, product, data/security/privacy, troubleshooting, compatibility, and documentation-index pages.
- Add versioned, executable examples for CLI, HTTP, MCP, verification, and Docker where current surfaces support them.
- Clarify the supported Go runtime, TypeScript reference package, contracts, and stability boundaries.
- Separate forward roadmap, published history, and readiness evidence without rewriting historical release facts.
- Improve contributor onboarding and documentation navigation.
- Reconcile capability counts, schema versions, deferred features, and authorization language against source.

## Constraints

- Technical artifacts are written in English.
- Preserve the accounting boundary: memory informs, evidence demonstrates, policy restricts, and authenticated professionals authorize.
- Do not claim accounting correctness, regulatory compliance, certifications, production readiness, cloud support, or integrations without repository evidence.
- Source code and registered surfaces are authoritative when documentation conflicts.
- Keep Gentleman Engram as a quality reference, not a branding or prose template.
- Preserve unrelated uncommitted changes in `internal/store/doctor_test.go` and `internal/store/fiscal_binding_enforcement_test.go`.
- Do not push. (Commit authorized on 2026-10-28, L4.)
- TDD is not applicable to documentation-only changes. Verification must still check examples, links, formatting, and factual catalogs.
- The approximately 400 changed-line task size is advisory; do not sacrifice coherent reader journeys to meet it.

## Tasks

- [x] **DOC-001: Establish the product landing page**
  - Rewrite `README.md` as a concise landing page with an accounting-first value proposition, explicit non-goals, audience routes, verified install path, first outcome, architecture summary, maturity status, and documentation links.
  - Remove stale or contradictory capability statements.
  - Acceptance: a new reader can explain what Drenyra Engram is, what it is not, who authorizes, and where to start within two minutes.
  - Checks: factual claims match source; links resolve; `git diff --check` passes.

- [x] **DOC-002: Create the onboarding and product guides**
  - Add `docs/GETTING-STARTED.md` with a reproducible local workflow and cleanup.
  - Add `docs/PRODUCT.md` covering problem, audiences, workflow, product boundaries, maturity, hypotheses, and relationship to the Drenyra ecosystem.
  - Acceptance: an evaluator can reach a verified first outcome and distinguish delivered behavior from product hypotheses.
  - Checks: commands map to implemented CLI surfaces; local links resolve; `git diff --check` passes.

- [x] **DOC-003: Document data, security, and privacy boundaries**
  - Add `docs/DATA-SECURITY-PRIVACY.md` covering stored data, metadata, encryption, scopes, receipts, evidence objects, retention, purge, sync, backup responsibilities, telemetry posture, and safe demo data.
  - Link existing threat-model and key-compromise procedures instead of duplicating them.
  - Acceptance: operators can identify what is protected, what remains visible, what they must operate, and which guarantees are not provided.
  - Checks: statements match current source and security docs; local links resolve; `git diff --check` passes.

- [x] **DOC-004: Add runnable examples and troubleshooting**
  - Add a narrow `examples/` catalog for supported CLI, HTTP, MCP, verification, and Docker paths.
  - Add `docs/TROUBLESHOOTING.md` for database locks, schema migration, encryption keys, OIDC, scope mismatch, MCP startup, and package/runtime issues.
  - Acceptance: examples avoid real taxpayer data, use explicit placeholders, state expected results, and map failures to recovery steps.
  - Checks: structured fixtures parse; shell snippets pass syntax checks where practical; links resolve; `git diff --check` passes.

- [x] **DOC-005: Build the documentation information architecture**
  - Add `docs/README.md` organized by audience and content type.
  - Add `docs/COMPATIBILITY.md` for operating systems, architectures, Go, Node/Bun, SQLite/schema, MCP clients, API/contract stability, and release status.
  - Clarify Go versus TypeScript roles in `DOCS.md`, `docs/ARCHITECTURE.md`, `docs/CODEBASE-GUIDE.md`, `docs/CONSUMING.md`, and `contracts/README.md`.
  - Acceptance: readers can choose the correct runtime and documentation path without reconstructing repository history.
  - Checks: catalog and version claims match package metadata and source; local links resolve; `git diff --check` passes.

- [x] **DOC-006: Reconcile roadmap, changelog, and contributor guidance**
  - Refocus `ROADMAP.md` on future outcomes and explicit readiness gates.
  - Preserve published facts in `CHANGELOG.md` while making unreleased versus released status unambiguous.
  - Improve `CONTRIBUTING.md` with a first-contribution path, contract-change flow, golden-vector expectations, and documentation checks.
  - Update `RELEASING.md` only where the new compatibility and documentation structure requires it.
  - Acceptance: roadmap, changelog, contribution, and release documents have distinct jobs and no known status contradictions.
  - Checks: release/version statements agree across files; links resolve; `git diff --check` passes.

- [x] **DOC-008: Give the landing page a visual identity**
  - Add the existing `assets/branding/` hero and diagrams, status badges, a centered hero block, and audience navigation to `README.md`, using Gentleman Engram's README as the quality reference without copying its branding.
  - Acceptance: the README is scannable in under a minute, renders correctly on GitHub in light and dark themes, and every image has descriptive alt text.
  - Checks: image paths resolve; badges make no unverified claims; `git diff --check` passes.

- [x] **DOC-009: Theme-aware visuals from the Dreamcoder themes**
  - Derive Dreamcoder Dark variants of every branding SVG from the light sources by token-to-token mapping (`scripts/brand-dark-variants.mjs`, with `--check`), render PNGs, and serve each README visual through `<picture>` with `prefers-color-scheme`.
  - Acceptance: no invented colours; dark assets have no glow; README matches the reader's GitHub theme.
  - Checks: `--check` RED before generation and GREEN after; every srcset path exists; rendered PNGs inspected.

- [x] **DOC-007: Run corpus-wide quality verification**
  - Audit changed Markdown against the repository evidence, cognitive-document principles, and the current writing guidelines.
  - Verify relative links, code fences, fixtures, capability counts, schema versions, and command names.
  - Correct only findings inside this authorized documentation scope.
  - Acceptance: no known broken local links, contradictory core claims, invalid fixtures, or formatting errors remain in changed documentation.
  - Checks: focused documentation validators pass; `git diff --check` passes; required spot checks are recorded.

- [x] **DOC-010: Restack onto main**
  - Rebase the fiscal-runtime-foundations stack (slices 1-6) and this branch onto `origin/main` with `--update-refs`, resolve documentation conflicts without losing main's changes, and keep everything local.
  - Checks: ahead 20 / behind 0; build and vet pass; no conflict markers; backups under `refs/backup/20261028/`.

- [x] **DOC-011: Make the Go suite pass under the shadow default**
  - Select `legacy_compat` in the `internal/sync` and `internal/search` test store helpers, following the existing server/CLI pattern.
  - Checks: RED on clean HEAD (13 sync + search failures with FISCAL_WRITE_GATE_CLOSED); GREEN after; full `go test ./...`.

- [x] **DOC-012: Align MCP inputSchema with the argument decoders**
  - Advertise the snake_case names from `contracts/approval.md` for `accounting_approve`, `accounting_review_detail`, `accounting_review_reject`, `accounting_review_return`; add `TestMCPInputSchemaMatchesDecoder` covering every property of every tool (strict and lenient decoders).
  - Checks: RED with 10 mismatches; GREEN after; docs updated from known issue to fixed.

## Progress

- Scope selected by the user: complete documentation transformation.
- Read-only repository audit completed.
- Gentleman Engram v2 README structure reviewed as an external quality reference.
- Existing unrelated test changes identified and reserved from modification.
- DOC-001 completed: `README.md` now acts as an accounting-first landing page with an explicit trust boundary, alpha maturity, a verified local start path, audience routes, and concise navigation.
- DOC-001 resolved the MCP contradiction against source: 58 registered tools, including `accounting_approve`; approval requires session-derived professional identity and stdio fails closed.
- DOC-002 completed: `docs/GETTING-STARTED.md` provides an isolated synthetic-data tutorial, and `docs/PRODUCT.md` separates delivered boundaries from market, buyer, pricing, integration, and identity hypotheses.
- DOC-004 completed: `examples/` (shared synthetic fixture, CLI, verification, MCP, HTTP, Docker, client config, `run-all.sh`) and `docs/TROUBLESHOOTING.md` with every quoted message reproduced against the current binary.
- DOC-004 finding (source defect, out of documentation scope): `accounting_approve`, `accounting_review_reject`, and `accounting_review_return` advertise camelCase `inputSchema` properties but the strict decoder accepts only snake_case; schema-conforming MCP clients get `-32602`. Documented as a known issue; code fix needs a user decision.
- DOC-004 finding: `DOCS.md` MCP section claims 57 tools and no approve tool, and lists nonexistent `engram_review`/`engram_promote`; runtime `tools/list` reports 58 (13 + 45). Assigned to DOC-005.
- DOC-005 completed (worker): `docs/README.md` index, `docs/COMPATIBILITY.md`, DOCS.md MCP/env/CLI/HTTP reconciled to runtime (58 tools, no phantom tools), case collision resolved by renaming `docs/architecture.md` to `docs/SYSTEM-BOUNDARIES.md` (three Go comment-only references updated), Go-vs-TypeScript role stated once per doc, pre-existing broken links fixed.
- DOC-006 completed (worker): Keep a Changelog structure with `[Unreleased]` and tag-verified headings, ROADMAP refocused on future outcomes and gates (30 KB to 12.7 KB, deferrals kept), CONTRIBUTING first-contribution/contract/golden-vector/doc-check flow, RELEASING and PR template aligned.
- DOC-008 completed: README rebuilt as a visual landing page (hero, static verified badges, navigation, the three branding diagrams, 60-second quickstart through `examples/run-all.sh`, For agents section). Fixed the hero SVG overlapping tagline (fixed-x texts replaced by one flowing text with tspans) and re-rasterized the PNG with rsvg-convert.
- Finding (needs user decision): tag `v0.9.0` lives on main and is not an ancestor of this branch; CHANGELOG/ROADMAP/CONTRIBUTING will conflict with main. The imported v0.9.0 notes describe `engineVersion` as a stamped var, while this branch still has `const engineVersion = "0.3.0"` (`internal/server/mcp.go:51`).
- Finding (follow-up): `docs/INSTALLATION.md` release-binary snippet names `v0.7.0` and a raw asset, but no such tag exists and goreleaser publishes archives.
- Finding (pre-existing, out of scope): three TypeScript golden vectors fail on Node 26 with "Invalid JWK OKP key"; CI pins Node 22 and was not run here.
- DOC-009 completed: four `*-dark.svg`/`*-dark.png` derived from Dreamcoder Dark tokens (`dreamcoder-dots/DreamcoderThemes/dreamcoder/tokens.json`), shadows forced black, README `<picture>` sources, BRAND.md workflow.
- DOC-007 completed: corpus verified; fixed the HTTP example cleanup race that left a throwaway signing keyring after `rm -rf`.
- Finding (pre-existing, out of scope, needs user decision): `go test ./internal/sync` fails on clean HEAD because tests do not set `DRENYRA_FISCAL_RUNTIME_MODE` and the default `shadow` closes writes; passes with `legacy_compat`. CI does not set it either, so `go test ./...` is red on this branch.

## Commits and delivery

Branch `chore/project-excellence-foundations`, restacked on `origin/main` (bbfad20). The fiscal-runtime-foundations commits below it keep their own chained PRs (#35-#39, slice 6 has no PR yet); this feature is the slice on top and ships as its own PR.

| Task | Commit |
| --- | --- |
| DOC-011 sync/search test stores | a0e48e1 |
| DOC-012 MCP inputSchema fix (high-risk item 3, isolated) | f0c3e28 |
| DOC-005 rename to SYSTEM-BOUNDARIES.md | d611654 |
| DOC-009 Dreamcoder Dark variants, hero fix | 805f34b |
| DOC-004 examples and troubleshooting | 84ceda1 |
| DOC-002, DOC-003 guides | c16a186 |
| DOC-005 reference, docs index, compatibility | 7875792 |
| DOC-006 changelog, roadmap, contributing, releasing | 5a43d4f |
| DOC-001, DOC-008 README | d23fd62 |
| Feature document | this commit |

## Verification evidence

- DOC-010/011/012 parent (after fixes): `go build ./...`, `go vet ./...`, `gofmt -l` clean; full `go test ./...` exit 0; `examples/run-all.sh` exit 0; brand `--check` ok; `git diff --check` ok; INSTALLATION release snippet executed against the real v0.9.0 assets (download, checksum, extract, doctor) exit 0; corpus 80 Markdown files, 0 link problems.

- DOC-005 worker: link+anchor check over 11 files 0 problems; `go build ./...` and `go vet` on the three comment-only packages passed; runtime `tools/list` matches DOCS.md exactly (58).
- DOC-006 worker: links resolve; `go vet ./...`, `gofmt -l`, `bun run typecheck` clean; `TestGoldenVectorsGo` passes; statuses agree with `git tag -l` (v0.2.0, v0.2.1, v0.9.0).
- DOC-007 parent live run: GETTING-STARTED bash blocks extracted verbatim and executed (exit 0, title found, `legacy_compat`); README quickstart build + `examples/run-all.sh` exit 0; `go build ./...`, `go vet` on changed packages, `git diff --check` clean; `go test ./internal/server ./cmd/drenyra-engram` ok; `internal/sync` fails identically on clean HEAD (pre-existing).
- DOC-007 parent: HTTP example run 5 times in an isolated TMPDIR after the cleanup fix: 0 leftovers (before the fix, one run left `signing-keys.json`).
- DOC-009 parent: `node scripts/brand-dark-variants.mjs --check` exit 1 before generation, exit 0 after; all eight README image paths exist; light and dark PNGs inspected visually.
- DOC-007 parent: corpus link+anchor check over 70 Markdown files: 0 problems; 0 unbalanced fences; fixed the stale "no approve tool" claim in the threat model.
- DOC-004 parent: `bash examples/run-all.sh` exit 0 with a freshly built binary; observed `created`, `[]` for the other period, verify `passed` + `NOT ASSERTED`, 58 tools, `AUTHENTICATION_REQUIRED`, HTTP 401/201/200.
- DOC-004 parent: fixtures and JSONL parse; `bash -n` passes for every script; TROUBLESHOOTING messages and exit codes reproduced (shadow, read_only, enforce-legacy, bad mode, bad/wrong/missing key, bad RUC/period, partial OIDC, bad default scope, no CLI session, schema 99 vs 18).
- DOC-004 not executed: Docker example (no Docker daemon on this host); content derived from `Dockerfile` only.
- DOC-003 parent check (2026-10-28): all relative links in `docs/DATA-SECURITY-PRIVACY.md` resolve.

- DOC-001 writer: `git diff --check -- README.md` passed.
- DOC-001 writer: `go test ./cmd/drenyra-engram ./internal/server` passed.
- DOC-001 writer: all relative links in `README.md` resolved with a read-only Python link check.
- DOC-001 parent spot check: `git diff --check -- README.md` passed; unrelated store-test changes remained untouched.
- DOC-002 writer: `git diff --check -- docs/GETTING-STARTED.md docs/PRODUCT.md` passed.
- DOC-002 writer: `go test ./cmd/drenyra-engram` and `go run ./cmd/drenyra-engram help` passed.
- DOC-002 writer: 14 relative links resolved and tutorial commands, flags, environment variables, and fixture fields mapped to current Go source.
- DOC-002 parent spot check: `git diff --check -- docs/GETTING-STARTED.md docs/PRODUCT.md` passed; both documents were read back in full.
- DOC-002 limitation: the live tutorial remains pending DOC-007 execution because the bounded writer did not create runtime files outside its allowed edit surfaces.

- DOC-003 completed: `docs/DATA-SECURITY-PRIVACY.md` covers persistence, encryption scope, tenant isolation, receipts, lifecycle, sync, recovery, OIDC, telemetry posture, and explicit non-claims.

## Native review (RDD)

- Whole range `a0e48e1..7525fa1`: `lens_context_budget_exceeded` (3633 lines); no authority created.
- High-risk unit `f0c3e28` alone (base `a0e48e1`, worktree `../drenyra-engram-review-mcp`, branch `review/mcp-schema-fix`): lineage `review-a1c054860cb98751`, tier medium, 137 lines, lenses risk + reliability. Collect blocked: "no model is configured for review-risk; assign it a model in the agent model routing config". Lineage stays open at `collect`; resume with bound STATUS after assigning reviewer models.

## Next step

All tasks complete and committed. Remaining user actions: assign models to the review-risk/review-reliability agents and resume lineage review-a1c054860cb98751; push the restacked branches (force-with-lease for slices 1-6) and open the PR for this slice.

## Log

- L2 (2026-10-28, user, verbatim): "me encata la calidad del readme del repo de gentleman y toda su documentacion , https://github.com/Gentleman-Programming/engram , asi debe ser para este repo , todo bien 10/10 en la vanguardia asi como el lo es para la ingeneria de softwart agentica ,yo debo serlo para la contabilidad agentica"
- Decision (2026-10-28): resume DOC-004 to DOC-007 and add DOC-008 for README visual identity; the do-not-commit constraint still holds.
- L3 (2026-10-28, user, verbatim): "para los visuales , diagramas , svgs , inspirate de themes dreamcoder light y dreamcoder dark , ❯ pwd /home/dreamcoder08/Documents/PROYECTOS/dreamcoder-dots"
- Decision (2026-10-28): added DOC-009; dark assets derive from the light sources by Dreamcoder token mapping instead of hand-edited copies.
- L4 (2026-10-28, user, verbatim): "yes te autorizo todo y avanza de forma autonoma"
- Decision (2026-10-28): authorized items were the rebase onto main, the MCP schema fix, the sync-test fix, and commits; push and pull requests stay with the user. The whole fiscal stack was restacked locally with --update-refs because the branch sits on top of it.
