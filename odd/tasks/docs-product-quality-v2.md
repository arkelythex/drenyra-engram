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
- Do not commit or push.
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

- [ ] **DOC-003: Document data, security, and privacy boundaries**
  - Add `docs/DATA-SECURITY-PRIVACY.md` covering stored data, metadata, encryption, scopes, receipts, evidence objects, retention, purge, sync, backup responsibilities, telemetry posture, and safe demo data.
  - Link existing threat-model and key-compromise procedures instead of duplicating them.
  - Acceptance: operators can identify what is protected, what remains visible, what they must operate, and which guarantees are not provided.
  - Checks: statements match current source and security docs; local links resolve; `git diff --check` passes.

- [ ] **DOC-004: Add runnable examples and troubleshooting**
  - Add a narrow `examples/` catalog for supported CLI, HTTP, MCP, verification, and Docker paths.
  - Add `docs/TROUBLESHOOTING.md` for database locks, schema migration, encryption keys, OIDC, scope mismatch, MCP startup, and package/runtime issues.
  - Acceptance: examples avoid real taxpayer data, use explicit placeholders, state expected results, and map failures to recovery steps.
  - Checks: structured fixtures parse; shell snippets pass syntax checks where practical; links resolve; `git diff --check` passes.

- [ ] **DOC-005: Build the documentation information architecture**
  - Add `docs/README.md` organized by audience and content type.
  - Add `docs/COMPATIBILITY.md` for operating systems, architectures, Go, Node/Bun, SQLite/schema, MCP clients, API/contract stability, and release status.
  - Clarify Go versus TypeScript roles in `DOCS.md`, `docs/ARCHITECTURE.md`, `docs/CODEBASE-GUIDE.md`, `docs/CONSUMING.md`, and `contracts/README.md`.
  - Acceptance: readers can choose the correct runtime and documentation path without reconstructing repository history.
  - Checks: catalog and version claims match package metadata and source; local links resolve; `git diff --check` passes.

- [ ] **DOC-006: Reconcile roadmap, changelog, and contributor guidance**
  - Refocus `ROADMAP.md` on future outcomes and explicit readiness gates.
  - Preserve published facts in `CHANGELOG.md` while making unreleased versus released status unambiguous.
  - Improve `CONTRIBUTING.md` with a first-contribution path, contract-change flow, golden-vector expectations, and documentation checks.
  - Update `RELEASING.md` only where the new compatibility and documentation structure requires it.
  - Acceptance: roadmap, changelog, contribution, and release documents have distinct jobs and no known status contradictions.
  - Checks: release/version statements agree across files; links resolve; `git diff --check` passes.

- [ ] **DOC-007: Run corpus-wide quality verification**
  - Audit changed Markdown against the repository evidence, cognitive-document principles, and the current writing guidelines.
  - Verify relative links, code fences, fixtures, capability counts, schema versions, and command names.
  - Correct only findings inside this authorized documentation scope.
  - Acceptance: no known broken local links, contradictory core claims, invalid fixtures, or formatting errors remain in changed documentation.
  - Checks: focused documentation validators pass; `git diff --check` passes; required spot checks are recorded.

## Progress

- Scope selected by the user: complete documentation transformation.
- Read-only repository audit completed.
- Gentleman Engram v2 README structure reviewed as an external quality reference.
- Existing unrelated test changes identified and reserved from modification.
- DOC-001 completed: `README.md` now acts as an accounting-first landing page with an explicit trust boundary, alpha maturity, a verified local start path, audience routes, and concise navigation.
- DOC-001 resolved the MCP contradiction against source: 58 registered tools, including `accounting_approve`; approval requires session-derived professional identity and stdio fails closed.
- DOC-002 completed: `docs/GETTING-STARTED.md` provides an isolated synthetic-data tutorial, and `docs/PRODUCT.md` separates delivered boundaries from market, buyer, pricing, integration, and identity hypotheses.

## Verification evidence

- DOC-001 writer: `git diff --check -- README.md` passed.
- DOC-001 writer: `go test ./cmd/drenyra-engram ./internal/server` passed.
- DOC-001 writer: all relative links in `README.md` resolved with a read-only Python link check.
- DOC-001 parent spot check: `git diff --check -- README.md` passed; unrelated store-test changes remained untouched.
- DOC-002 writer: `git diff --check -- docs/GETTING-STARTED.md docs/PRODUCT.md` passed.
- DOC-002 writer: `go test ./cmd/drenyra-engram` and `go run ./cmd/drenyra-engram help` passed.
- DOC-002 writer: 14 relative links resolved and tutorial commands, flags, environment variables, and fixture fields mapped to current Go source.
- DOC-002 parent spot check: `git diff --check -- docs/GETTING-STARTED.md docs/PRODUCT.md` passed; both documents were read back in full.
- DOC-002 limitation: the live tutorial remains pending DOC-007 execution because the bounded writer did not create runtime files outside its allowed edit surfaces.

## Next step

Implement DOC-003 through one bounded writer, verify it, update this document and its Engram mirror, then continue to DOC-004.
