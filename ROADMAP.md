# Drenyra Engram — Roadmap

> **Last updated:** 2026-08-05. Status: alpha (v0.3.0 Accounting Memory Kernel released).
>
> **Version nomenclature (frozen):** `v0.3.0` is the Accounting Memory Kernel.
> The original vision's "V0.2 Evidence and Judgment" is NOT closed by it —
> adjudicated contradictions, cryptographic receipts and offline verification
> are Phase 4 (`v0.4.0`). The numbering is therefore: `v0.4.0` Evidence,
> Conflict and Judgment · `v0.5.0` Close Intelligence · `v0.6.0` Fiscal Policy
> Memory · `v1.0.0` Institutional Accounting Brain.

## Phase 0 — Identity (current)

- [x] Repository created with identity scaffolding (README, LICENSE, SECURITY, CONTRIBUTING, CODEOWNERS)
- [x] Contract index drafted (`contracts/`)
- [ ] Contract review and freeze: memory, scope, lifecycle, provenance
- [ ] Public roadmap and architecture published

## Phase 1 — Contracts (v0.1)

- [ ] Freeze `memory` v0.1 (observation model, topic keys, structured content)
- [ ] Freeze `scope` v0.1 (company/RUC/period scoping, scope-first search)
- [ ] Freeze `lifecycle` v0.1 (draft → reviewed → promoted → superseded, vigencia)
- [ ] Freeze `provenance` v0.1 (who/what/when/why, auditability, non-authorization boundary)
- [ ] Contract conformance test suite

## Phase 2 — Vertical slices from Drenyra

Extracted via vertical PRs and versioned releases, **not** a bulk move:

- [ ] Slice 1: mission summary + accounting memory search
- [ ] Slice 2: observation store with scope-first indexing
- [ ] Slice 3: relations + lifecycle + vigencia
- [ ] Slice 4: MCP server + CLI
- [ ] Drenyra and Drenyra Pi consume the first released version instead of internal implementations

## Phase 2 — Standalone Go engine (ADR-001) — COMPLETE (v0.2.0 released 2026-08-03)

- [x] **v0.2 foundation:** Go module (`github.com/arkelythex/drenyra-engram`), core types + validators (RUC 11 digits, period YYYYMM), lifecycle machine, SQLite store (modernc.org/sqlite, pure Go — immutable history + schema guards), scope-first search with the MANDATORY cross-tenant isolation property, and the CLI (`save | search | context | doctor`). 40 Go tests green; non-authorization boundary enforced (no authorize/approve/allow commands).
- [x] CLI polish: `compare`, `review`, `promote`, `supersede` commands (45 Go tests green; compare verdict matrix incl. supersedes-with-source-check)
- [x] MCP + HTTP surfaces (shared domain services `internal/server/api.go`; `mcp` stdio + POST /mcp; `serve` REST /v1 + token; 87 Go tests green)
- [x] Local/cloud sync — additive, provenance-preserving, conflict-visible
  reconciliation (`sync --from <src> --to <dst>`; full revision history +
  relations + audit trail; lifecycle via transition replay; divergent chains
  surfaced with `conflicts_with` relations, never silently resolved; cloud
  deferred per non-goals) — 104 Go tests green
- [x] Chain history surface (`GET /v1/chain` + `engram_chain` tool) — full
  revision history per (topicKey, scope); powers the Drenyra fiscal-memory
  adapter. 106 Go tests green; `go test -race ./...` clean.
- [x] **Released as v0.2.0** (GitHub release, static binaries for
  linux/darwin/windows x amd64/arm64, Go module go-gettable). First
  consumer: Drenyra adapter (observability read + mission write + fiscal
  memory) live-tested 12/12 against the released binary.

## Phase 3 — Accounting Memory Kernel (v0.3.0) — COMPLETE

- [x] AccountingMemory v2 model: 8 kinds, 6 statuses, 8 fiscal effects, triple
      timestamps, structured source, canonical content hash, materiality.
- [x] Mandatory human approval gate (`pending_review` → human `approve`).
- [x] Store schema v2 with additive v1→v2 migration, evidence/rule links.
- [x] 17 relations (accounting-evidence vocabulary).
- [x] Search filters (kind/status/fiscal effect) over scope-first isolation.
- [x] API: Approve/Reject/Void/Supersede/Judge/LinkEvidence/PeriodSummary.
- [x] 10 MCP `accounting_*` tools; CLI approve/reject/void/link-evidence/
      period-summary/timeline; HTTP approve/reject/void endpoints.
- [x] Kill demo: explainable period summary of account 4011 (280 Go tests).

## Phase 4 — Evidence, Conflict and Judgment (v0.4.0)

- [ ] **Adjudicated contradictions**: proposed/confirmed/rejected relation
      lifecycle (an agent may PROPOSE `XML contradicts PDF`; it cannot make it
      institutional truth)
- [ ] **Receipts Ed25519**: sign the full envelope (identity + content + fiscal
      effect + source + evidence/rule refs + supersession + actor + action +
      policy version); a signature proves integrity and provenance, never
      accounting correctness
- [ ] **Offline verification**: `drenyra-engram verify memory|receipt|period`
      answering per layer (payload integrity, receipt signature, evidence
      refs, approval principal, policy version, supersession chain;
      "Accounting correctness: NOT ASSERTED")
- [ ] **Authenticated approval principal** (authz risk): derive approval from
      an authenticated principal (subjectId, tenantId, company membership,
      roles, auth method) — never from caller-declared `actorKind`
- [ ] Evidence object store references (XML/PDF/CDR beyond refs)
- [ ] Materiality-aware period views

## Phase 5 — Close Intelligence (v0.5.0)

- [ ] Monthly close memory (cierre) with pending items
- [ ] Reconciliations as first-class memories (reconciles relation)
- [ ] Period-over-period comparison
- [ ] Automatic agent context on session start

## Phase 6 — Fiscal Policy Memory (v0.6.0)

- [ ] Versioned rules with temporal vigencia
- [ ] Jurisdiction dimension (Perú → LATAM)
- [ ] Regulatory-change impact reconstruction
- [ ] Rule used in a historical decision, reconstructible

## Phase 7 — Institutional Accounting Brain (v1.0.0)

- [ ] Accounting firms: shared memory with controlled sync
- [ ] Authorized anonymized cross-company learning
- [ ] Full knowledge graph + pattern detection
- [ ] Specialized agents + complete audit

## Non-goals (for now)

- Authorization engine (that is `arkelythex/drenyra-ai` gates + human approval)
- Cloud offering (deferred to `arkelythex/drenyra-cloud`)
- PostgreSQL in this repo (local-first; PostgreSQL is the Drenyra ecosystem's
  authoritative store, ADR-002)
