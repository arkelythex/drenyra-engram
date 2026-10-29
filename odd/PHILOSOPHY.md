# Drenyra Engram — Engineering Philosophy

This is the durable reference for how work gets planned, built, reviewed, and kept clean in this repository.
It does not replace requirements under `specs/` or decision records under `docs/decisions/` — it sets the standard those documents
and every code change are held to. Historical records are evidence, not an alternative workflow. Update this file when a convention genuinely changes; do not fork it per
feature.

## 1. Organic Driven Development (ODD)

ODD is the only development workflow here, not a ceremony reserved for big features.

- **Investigate before writing.** Read the relevant code and requirements proportionally to the change before
  proposing or editing anything. A one-line fix does not need an essay; a cross-cutting change does not skip
  the reading.
- **Track resumability, not step counts.** Small work is understood from a bounded read, has contained risk,
  and can be resumed from the request and diff alone. It stays inline without a new feature document.
  Work that fails that resume test gets `odd/tasks/<feature-name>.md` before the first source write:
  a short header, `## Specs` quoting the request, `## Tasks` with IDs/status/routes/commits, and `## Log` last.
  Keep the file, available memory mirror, and visible task list aligned after each transition.
- **Commit work units, not checkboxes.** A feature-branch commit holds a cohesive, reviewable outcome,
  including its tests and docs. Aim for about 400 authored changed lines without fragmenting an outcome
  or deleting useful content to reach a number. Conventional Commits only; no AI attribution in trailers.
- **Checkboxes are not approval.** A task is checked off only after its outcome and checks were actually
  observed. Failed, skipped, or pending checks are recorded honestly, not hidden.
- **Findings drive scope, not silent expansion.** Review or verification findings update the task list with a
  reason; they never justify quietly widening what a "cleanup" or "fix" touches without saying so.
- **Delegate by mechanism, not file count.** Use bounded exploration when the evidence budget is exceeded;
  independent writers only for sufficiently substantial, independent behavior units with disjoint edit
  surfaces; and an independent verifier for a named high-risk item. Small, understood work stays inline.
  Product and scope decisions remain with the person driving the session; an agent returns gaps, not consent.

## 2. Gentleman Programming principles

These are the architecture and craft defaults for this codebase, applied pragmatically — they describe the
shape code should tend toward, not a checklist to satisfy for its own sake.

- **Screaming architecture.** Open the top-level directory tree and the *domain* should be obvious before any
  framework or library name is. `internal/store`, `internal/authz`, `core/`, `authority/` should read as
  "this is an institutional accounting memory system with fiscal compliance," not "this is a Go project that
  happens to do accounting."
- **Hexagonal / ports-and-adapters boundaries.** Domain logic (fiscal rules, receipt hashing, review-checks
  decoding, lifecycle transitions) must not depend on transport (HTTP, CLI, MCP). Transport adapters translate
  into and out of the domain; they never carry business rules themselves. When the same decode/validation logic
  is duplicated across CLI, HTTP, and MCP entry points, that's a boundary leak to fix, not a pattern to repeat.
- **Container/presentational separation** applies wherever there's a UI or CLI-rendering layer: the piece that
  fetches/decides is not the piece that renders. The same split shows up server-side as "handler decides,
  formatter renders."
- **Atomic composition.** Small, single-purpose units (functions, types, components) compose upward. A unit
  that both validates and persists and formats a response is three responsibilities wearing one name — split
  it, don't add flags to it.
- **Tests as a design tool, not a coverage tax.** A test should fail for exactly one reason and read as a
  specification of behavior. Reflection-based invariant tests (like this repo's frozen field-shape tests) are
  a deliberate device to catch accidental schema drift — extend them explicitly when a type gains a field,
  never work around them.

## 3. General software-engineering best practices (the non-negotiables)

- **YAGNI over speculative generality.** No abstraction, interface, or config flag for a future need that
  hasn't arrived. Three similar lines beat a premature shared helper.
- **DRY is about knowledge, not text.** Two pieces of code that look similar but encode different business
  rules are not duplication — merging them creates a hidden coupling. Two pieces of code that encode the *same*
  rule in two places (e.g., the same fiscal decode logic in `internal/server` and `core`) are duplication and
  should collapse to one source of truth.
- **Fail closed on ambiguity in fiscal/security paths.** This repo already practices this (fail closed on
  fiscal-links read failure, actor-forgery/bypass-guard closures) — keep it as the default posture for any new
  write path, not just the ones that have already been through an incident.
- **No dead weight in version control.** Build output (`dist/`), compiled binaries, and language-specific
  linter caches are reproducible artifacts, not source — they do not belong in git. If a build target writes
  into a path, that path is either gitignored or it is a bug.
- **Delete code with evidence, not vibes.** "Looks unused" is a hypothesis, not a finding. Confirm zero
  importers/callers (grep, codegraph, or a passing test suite after removal) before deleting; when unsure,
  archive/flag it in the task document instead of guessing.
- **Small, reviewable diffs.** ~400 authored changed lines per work unit is a planning heuristic, not a hard
  cap — split for reviewability, never split just to hit a number, and never strip comments/blank lines to
  dodge the count.
- **Security and privilege boundaries reviewed like code.** Every write path that changes fiscal state,
  authorization, or actor identity gets the same scrutiny as the tests around it — this is exactly what the
  RDD (receipt-driven development) outside view complements; it follows the human-owned switch and the
  current native risk assessment, not a saved claim that it is enabled or a review per checkbox. Functional
  checks and any required independent verifier remain necessary. Never invent a native verdict or route
  around a review-due stop.

## 4. Current standards to hold this stack to

Grounded in what this repo already declares (not speculative version trivia):

- **Money is never a float.** `package.json`'s own description states the convention: monetary values are
  BigInt cents; sequence/revision counters are JSON integers, never floats. Any new numeric field touching
  money or a monotonic counter must follow this or be rejected in review.
- **Two distribution channels, two disciplines.** The npm package (`drenyra-engram`, TS, `package.json#exports`)
  and the Go CLI/server (`cmd/drenyra-engram`, `internal/`) are verified-or-being-verified as separate shipped
  surfaces (see `odd/tasks/project-excellence-foundations.md`, MAP-001). Until that's confirmed, treat apparent
  duplication between them as "needs verification," not "obviously redundant" — reimplementing the same domain
  natively per runtime is a legitimate reason for two implementations to exist side by side.
  Runtime targets declared: Node `>=22` for the TS package; Go module `go 1.26` for the CLI/server. Pin
  reasoning about language-version behavior to what the toolchain in this repo actually reports
  (`go version`, `tsc --version`), not to assumed defaults — the versions declared here move faster than any
  static write-up of "current" JS/Go idioms.
  Fuzz testing is a standing CI-owned discipline here (three frozen, time-boxed fuzz targets in `Makefile`
  `fuzz-ci`), used deliberately for parser/canonicalization surfaces (comprobante XML parsing, canonical
  receipt payloads, search tokenization) — extend that list when a new parsing/canonicalization boundary is
  added, don't invent a parallel fuzzing setup.
- **Supply-chain hygiene.** `renovate.json` keeps dependencies current automatically; do not hand-pin a
  dependency version to dodge a renovate PR without a documented reason in the commit.

## 5. How this document is used

Every ODD task document should be written as if it will be read by someone who has only this file and the code
— not this conversation. When a convention here turns out to be wrong or stale, fix this file in the same
work unit that proves it wrong; don't let it silently rot next to the code it's supposed to describe.
