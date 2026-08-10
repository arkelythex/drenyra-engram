# Proposal — {change-name}

> Phase: propose · Artifact: proposal · Status: draft
> Strict TDD: not active in this phase (planning only).

## Problem statement

_What business/product problem are we solving? Who experiences it, in which situations, and why does it matter today? Reference the current gap in the codebase (README / docs / contracts / ROADMAP)._

## Proposed change

_What are we going to do, at product level? Scope boundary: what is IN this change and what is explicitly OUT (non-goals)._

## Business rules and implications

_List concrete business rules the change must honor. For Drenyra Engram, always check the standing invariants:_

- _Money is whole int64 cents / BigInt cents — never floats._
- _Scope (RUC/period) is structural and fails closed on mismatch._
- _Non-authorization boundary: the engine never authorizes operations._
- _Contracts are frozen surfaces — versioned, migration path, explicit approval._

## Impact

_What surfaces does this touch (internal/core, internal/store, internal/server, contracts/, TS mirrors, testdata/golden)? Cross-runtime parity consequences? Doc updates required (docs-as-code)?_

## Edge cases and open questions

_Edge cases the reviewer must think about; decisions still open. 3–5 product questions per round; record assumptions._

## Alternatives considered

_What else was considered and why rejected (with tradeoffs)._

## Acceptance outlook

_How will we know this worked? (Product-level signals; concrete criteria land in spec.md.)_
