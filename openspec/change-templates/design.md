# Design — {change-name}

> Phase: design · Artifact: design · Status: draft
> Inputs: proposal (required). Strict TDD: not active in this phase.

## Context

_One paragraph: what the design must achieve, key constraints from the proposal/spec (scope-first, non-authorization boundary, parity, determinism)._

## Decisions

- `D-1` — _Decision with rationale and tradeoffs._
- `D-2` — _…_

## Architecture

### Layers touched

| Layer | Change |
| --- | --- |
| `internal/core` (pure domain) | _…_ |
| `internal/store` (SQLite, single-writer, immutable) | _…_ |
| `internal/server` + `cmd` (adapters, thin) | _…_ |
| TS mirrors (`core/`, `store/`, `lifecycle/`) | _…_ |
| `contracts/` (frozen surface) | _…_ (version bump + migration path if touched) |
| `testdata/golden` | _…_ |

### Data model / invariants

_New or changed types, envelope hashes, receipt payloads, schema implications (migrations fail-closed, versioned). Money types are whole int64/BigInt cents._

### Flow

_Describe the call flow or state transitions (e.g., lifecycle transitions) with exact failure semantics: fail closed on scope mismatch, no silent error handling._

## API / interface changes

_New or changed MCP tools / HTTP routes / CLI commands / TS exports. Adapters never invent authority — the principal derives only from the session._

## Security and audit considerations

_Provenance completeness (who/what/when/why), receipt coverage, non-authorization boundary check for each new surface._

## Migration and compatibility

_Schema migrations, contract version bumps, golden vector regeneration, rollout order._

## Testing strategy

_Unit, parity, golden, scope-isolation, integration — mapped to the spec's acceptance criteria. No new domain logic without Go↔TS parity._
