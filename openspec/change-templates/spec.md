# Spec — {change-name}

> Phase: spec · Artifact: spec · Status: draft
> Inputs: proposal (required). Strict TDD: not active in this phase.

## Scope

_One-paragraph summary of what this change does, within the proposal's scope. No scope creep beyond the accepted proposal._

## Requirements

### Functional requirements

- `FR-1` — _User story: as a … I want … so that …_
- `FR-2` — _…_

### Non-functional requirements

- `NFR-1` — _performance, security, auditability, offline verification, determinism, cross-runtime parity…_

### Invariant requirements (Drenyra Engram standing rules)

- `IR-1` — Money remains whole int64 cents / BigInt cents; no float path is introduced.
- `IR-2` — Scope stays structural and fails closed on mismatch.
- `IR-3` — Non-authorization boundary holds: no new surface approves, posts, or files.
- `IR-4` — Go↔TS golden parity is extended for new domain logic (testdata/golden).
- `IR-5` — No `any` in TypeScript; precise types or `unknown`.

## Acceptance criteria

- `AC-1` — _Concrete, testable outcome for FR-1 (what test or command proves it)._
- `AC-2` — _…_
- `AC-N` — `go test ./...` and `npm test` remain green; `npm run typecheck` clean.

## Out of scope

_Explicitly listed non-goals from the proposal, restated so later phases cannot drift._

## Test plan

_Which test layers cover which criteria: Go unit (internal/core, internal/store), TS parity (core/__tests__), golden vectors, scope-isolation conformance, integration (internal/server)._
