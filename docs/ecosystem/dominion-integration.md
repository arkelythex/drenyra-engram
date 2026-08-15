# Drenyra Engram — Dominion Program Integration Cuts (SDD-050/060/100/110)

> Status: maintained · Owner: `drenyra-engram` · Updated: 2026-08-15
> Purpose: records the engram-side of the five Dominion SDD integration cuts.
> Engram participates in these SDDs as a service provider / evidence holder; the
> authority for governed fiscal operations stays in `drenyra-ai`. Engram certifies
> what it stored and who reviewed a memory; it never authorizes journal entries,
> payments, declarations, fiscal closes, or SUNAT actions (SDD-080 constitutional
> rule).

## SDD-050 — Peruvian Monthly Close (engram provides context, never evidence)

Engram supplies prior-decision context and stored artifacts to the monthly close
journey; it is never the authoritative evidence of the close. The engram-side
surface:

- `contracts/closing.md` — period closing semantics (exact company scope with a
  YYYYMM period; `internal/server/close_service.go`).
- `internal/server/close_fixture_test.go` + `close_service_test.go` — the
  reconstructible monthly-close demo fixture (F001-947/948 XML+CDR EvidenceObjects
  linked to compras/ajuste memories) and the close service tests.
- `FindPeriodClosure` / `PeriodSummary` (internal/server/api.go) — the
  period-closure projection and the aggregated, explainable period view that the
  close journey can consume as context.
- Non-authorization boundary: the close decision itself belongs to `drenyra-ai`
  and the enabled professional (SDD-050); engram memories inform but never prove.

## SDD-060 — Multi-Operator (isolation + scope evidence; RBAC/ABAC authority in drenyra-ai)

The engram-side deliverable is exhaustive isolation evidence; authorization
authority (RBAC/ABAC) stays in `drenyra-ai`.

- `internal/server/cross_tenant_matrix_test.go` — `TestCrossTenantMatrix` /
  `TestCrossTenantMutationNoSideEffect` / `TestCrossTenantMatrixExhaustiveness`:
  every canonical API operation proven nonexistence-safe from tenant B with zero
  foreign-identifier leakage and zero side effect on denied mutations.
- Isolation anchors: `internal/search/search_test.go:77`,
  `internal/store/object_hardening_test.go:342`, `internal/server/mcp_object_test.go:91`,
  `internal/server/mcp_retention_policy_test.go:284`; search leakage probes at
  `internal/search/bench` (EXACTLY 0).
- Scope-first enforcement at HTTP/MCP/CLI/store boundaries (caller-asserted exact
  scope at unauthenticated adapter surfaces; store-level filtering is structural).
  Identity-to-scope binding is a production identity prerequisite (block I /
  issue #18).

## SDD-100 — Command Center (Review Workspace API for the Drenyra Web UI)

Engram exposes the engine-side Review Workspace surface; the Web UI belongs to
Drenyra (`drenyra-app-web`). The engram API a Web UI consumes:

- `ReviewQueue` — scope-first pending-review queue (deterministic ordering,
  bounded pagination).
- `ReviewDetail` — review-detail assembly (structured diff, evidence WORM
  availability, rule refs/vigencia, open judgments, envelope H1).
- `Approve` / `Reject` / `ReturnMemory` / `RejectMemory` — professional review
  actions on memories (approval of a memory is professional review, never fiscal
  authorization).
- `LinkEvidence` / `LinkRules` / `LinkRuleVersion` — evidence/rule attachment
  after write (immutable memory, growing links; exact-scope guarded).
- `Judge` / `Reconcile` — adjudication + reconciliation surfaces.
- HTTP: `/v1/observations/{id}/review-detail`, `/v1/review/queue`, approve/reject/
  return/reject-memory routes; MCP `accounting_review_*` tools.

The Web UI consumes these WITHOUT rebuilding authority: engram approval receipts
are integrity/professional-review receipts, not governed-operation receipts
(SDD-080 terminology table).

## SDD-110 — Production Readiness (OIDC, KMS/HSM, storage, recovery, observability)

Engram-side status at 2026-08-15 (repository-verifiable evidence; production
readiness as a whole is deployment-owned):

| Item | Status | Evidence / follow-up |
|---|---|---|
| OIDC access-token identity | implemented on `feat/oidc-access-token-identity` (RS256 validation, DB membership resolution, fail-closed config, rejection tests) — issue #18 approved | branch + `internal/auth` + `http_oidc*_test.go`; merge is a follow-up slice |
| Signing-key custody (HSM/KMS) | decision frozen | `docs/decisions/ADR-005-signing-key-custody-hsm-kms.md`; compromise-response DRILL (G-7) + playbook in `docs/security` |
| Remote/cloud object storage | not implemented (deferred) | `docs/architecture/operability-evidence.md` § Operational boundaries — explicitly unproven |
| Encryption-at-rest / TDE | not implemented (deferred) | same source — explicitly unproven |
| Recovery (snapshot/restore, corruption drills) | implemented + tested (repository-local) | `internal/store/drill.go`, `drill_test.go`, `doctor.go`; qualitative objectives, no invented RTO/RPO (FD-3) |
| Observability (doctor, health checks) | implemented | `internal/store/doctor.go` + `doctor_test.go` (routine/full modes, quick_check + foreign_key_check) |
| Deployment-owned objectives (RTO/RPO/SLA) | UNKNOWN until an accountable owner records them | `docs/architecture/operability-evidence.md` |

## How to keep this document truthful

Every claim pairs with an executable test or frozen contract path. If a cited
path is removed or its test fails, the corresponding claim is stale and MUST be
restored to conservative wording. This document adds no capability and never
promotes lifecycle state (status-and-evidence rules R3/R4).
