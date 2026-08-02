# Drenyra Engram — Roadmap

> **Last updated:** 2026-08-01. Status: pre-alpha.

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

## Phase 2 — Standalone Go engine (ADR-001) — IN PROGRESS

- [x] **v0.2 foundation:** Go module (`github.com/arkelythex/drenyra-engram`), core types + validators (RUC 11 digits, period YYYYMM), lifecycle machine, SQLite store (modernc.org/sqlite, pure Go — immutable history + schema guards), scope-first search with the MANDATORY cross-tenant isolation property, and the CLI (`save | search | context | doctor`). 40 Go tests green; non-authorization boundary enforced (no authorize/approve/allow commands).
- [ ] CLI polish: `compare`, `review`, `promote`, `supersede` commands
- [ ] MCP + HTTP surfaces
- [ ] Local/cloud sync

## Phase 3 — Ecosystem maturity (alpha → beta)

- [ ] Local/cloud sync with clear semantics
- [ ] HTTP API + TUI
- [ ] Multi-jurisdiction institutional knowledge (Perú → LATAM)
- [ ] v1.0 candidate when two consumers run on the released contracts

## Non-goals (for now)

- Authorization engine (that is `arkelythex/drenyra-ai` gates + human approval)
- Cloud offering (deferred to `arkelythex/drenyra-cloud`)
