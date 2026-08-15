# Drenyra Engram — Ecosystem Capabilities and Conformance (SDD-010)

> Status: maintained · Owner: `drenyra-engram` · Updated: 2026-08-15
> Purpose: the engram-side manifest for the Drenyra Dominion Program's SDD-010
> (Contracts & Release Train). This document is the source of truth the Dominion
> program-lock (`drenyra-ai/openspec/programs/drenyra-dominion/program-lock.json`)
> consumes for the `drenyra-engram` sibling row; refresh the lock's sibling facts
> from this document after every verified release.

## Repository identity

| Field | Value |
|---|---|
| Repository | `arkelythex/drenyra-engram` |
| Verified revision | `ccdce72` (main, pushed 2026-08-15) |
| Package version | `0.0.1-prealpha.1` (npm) / Go module `github.com/arkelythex/drenyra-engram` |
| License | Apache-2.0 (ADR-004) |
| Open component | yes — publicly visible, mirrors the proprietary core's contracts |

## Versioned contract surface

The public contract surface lives in `contracts/` (frozen subset marked
`frozen-for-0.1`; see `contracts/README.md`). Produced contracts consumed by the
Dominion program-lock:

| Contract | Version | Status | Governs |
|---|---|---|---|
| `memory` | 0.1-draft | Draft (subset frozen) | Observation model and storage |
| `scope` | 0.1-draft | Draft (subset frozen) | Company/RUC/period structural scoping |
| `lifecycle` | 0.1-draft | Draft (subset frozen) | Observation lifecycle + vigencia |
| `provenance` | 0.1-draft | Draft (subset frozen) | Audit metadata + non-authorization boundary |
| `receipts` | 0.1-draft | Draft | Ed25519 integrity receipts + offline verification |
| `verification` | 0.1-draft | Draft | Offline verification contract (PASS/FAIL/UNKNOWN/INCOMPLETE) |
| `closing` | 0.1-draft | Draft | Period closing semantics (SDD-050 context) |
| `reconciliation` | 0.1-draft | Draft | Additive local-store reconciliation |
| `judgment` / `approval` | 0.1-draft | Draft | Professional review / approval-policy review gates (never fiscal authorization) |
| `period-comparison` | 0.1-draft | Draft | Period-over-period comparison semantics |

Compatibility policy: versioned contracts with a declared compatibility policy
(`contracts/README.md` rule 1); contract changes are high-materiality and require
explicit approval per `openspec/config.yaml`.

## Capability matrix (implemented and tested)

| Capability | Evidence |
|---|---|
| Scope-first SQLite store (structural, fail-closed) | `internal/store` (schema v14), `contracts/scope.md`; cross-tenant matrix `TestCrossTenantMatrix*` (internal/server/cross_tenant_matrix_test.go) |
| Immutable observation history + supersession | lifecycle state machine, `internal/core`; golden parity |
| EvidenceObject WORM (v0.7) | schema v8 object bytes, `object_stored` receipts, scoped store/get, rehash verification |
| Evidence lifecycle (retention, holds, purge, export, v0.8) | versioned retention policies, legal holds, approved purge pipeline, deterministic lifecycle export |
| Ed25519 receipts + offline verification | kernel protocol + `verify object` (six receipt layers, WORM byte integrity) |
| Professional memory review (Review Workspace, v0.9) | scope-first review queue, review-detail assembly, reject/return/approve gates (never authorization) |
| OIDC access-token identity (issue #18, SDD-110) | stateless RS256 validation (exact iss/aud, JWKS cache), DB membership/scope cross-check, fail-closed config, standard assurance only |
| Reconstructibility + period closing | FZ-1/FZ-2 classifier, `FindPeriodClosure`, period summary |
| Operability (doctor, snapshot/restore, corruption drills) | `docs/architecture/operability-evidence.md` (qualitative objectives; RTO/RPO owner-owned) |
| CLI + HTTP API + MCP | `cmd/drenyra-engram`, `internal/server` (HTTP + MCP JSON-RPC) |

## Runtime compatibility

| Surface | Status |
|---|---|
| Go binary CLI | implemented (`drenyra-engram` — save/review/rule/object/verify/mcp/serve/…) |
| HTTP REST API | implemented (scope-first adapters; caller-asserted scope at unauthenticated surfaces; identity binding = block I / issue #18) |
| MCP (JSON-RPC) | implemented (`accounting_*` tool catalog) |
| TypeScript SDK reference | implemented (`core/`, `store/`, `search/`, `lifecycle/`, `index.ts`) — reference, not the destination (ADR-001/ADR-002) |
| SDK (as a consumable package) | not yet (follow-up) |

## Conformance evidence (2026-08-15, revision `e52ff3e`)

| Suite | Result |
|---|---|
| `go test ./...` | ok — 10/10 packages (incl. OIDC auth + server suites) |
| `npm test` | 385/385 (26 files) |
| `npm run typecheck` | clean |
| `go vet ./...` | clean |
| `gofmt -l .` | clean |
| `go test ./internal/core -run TestGoldenVectorsGo` | ok (Go↔TS parity) |
| Native bounded reviews | `review-bd7751cd5b4e8293` (one bounded correction) → approved; `review-983059cb39d4ef37` (audit chain delivery) → approved; `review-9908fc2e19c6fb99` (OIDC code) + `review-578d333380b33ad7` (OIDC docs) → approved |

## Release-train relationship

- Participant in the Drenyra Dominion Program release train (SDD-010); the
  program-lock's `drenyra-engram` row is refreshed from this document.
- Versioning: semantic versioning policy per `RELEASING.md`; monetary values are
  whole int64/BigInt cents, never floats (cross-repo fiscal convention).
- Non-authorization boundary: engram produces integrity receipts and professional
  review approvals; it never produces fiscal authorization (SDD-080 / R14; the
  drenyra-ai boundary rejects memory-shaped evidence channels).

## Maintenance rule

If a cited test or contract path is removed or fails, this manifest's claim for
that capability is stale and MUST be restored to conservative wording (same rule
as `docs/architecture/operability-evidence.md` § Evidence maintenance).
