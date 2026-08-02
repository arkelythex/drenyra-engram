# Changelog

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents; no float is ever used for money; version/sequence numbers are JSON integers, never floats.

All notable changes to Drenyra Engram will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to the version policy in [RELEASING.md](RELEASING.md).

## 0.0.1-prealpha.4 — 2026-08-02

### Added — Full chain revision history (GET /v1/chain, engram_chain)

- **`GET /v1/chain?topicKey=<key>&ruc=&period=&organizationId=`** — the full
  revision history of a (topicKey, exact scope) chain, ordered by revision
  ascending (the counterpart of context/get_by_topic which return only the
  latest). Store `FindChain` (additive to the Store interface), server API
  `Chain`, and the 13th MCP tool `engram_chain`.
- Needed by the Drenyra fiscal-memory adapter (`findById`/`findRevisions`
  map to this surface) — the first consumer that requires every revision of
  a topic key, not just the current one.
- 4 new Go tests (API history + structural isolation, HTTP route + fail
  closed, MCP tool) — total 104 Go tests green; `go test -race ./...`
  clean on all 6 packages; build + vet clean.

## 0.0.1-prealpha.3 — 2026-08-02

### Added — Local/cloud sync (ADR-001 v0.2, Phase 2 complete)

- **`drenyra-engram sync --from <src-db> --to <dst-db>`** — additive,
  provenance-preserving, conflict-visible reconciliation
  (docs/architecture.md sync semantics). Imports the source's FULL revision
  history, relations and lifecycle audit trail into the sink with original
  ids, revisions and provenance (store `ImportObservation`/`TransitionExists`
  — verbatim, validated, idempotent; never a re-save that would fabricate
  ids). Lifecycle propagates via TRANSITION REPLAY through the lifecycle
  machine (the imported audit record is the row — nothing is duplicated).
- **Conflict-visible, never silently resolved**: divergent (topicKey, scope)
  chain heads are preserved in BOTH stores and linked with a `conflicts_with`
  relation plus a report entry; an id existing with different immutable bytes
  is skipped (IMPORT_CONFLICT surfaced); an illegal transition replay is
  reported. Re-running the same pair is a no-op.
- Cloud is out of scope (ROADMAP non-goals — deferred to drenyra-cloud); this
  closes Phase 2 of the standalone Go engine.
- Corrected during review (fresh-eyes verification found two real bugs,
  fixed with regression tests): (1) a first full sync into an empty sink
  imported observations at their final status, making earlier audit records
  look "backward" — phantom transition conflicts on every run and an empty
  sink audit trail; sync now imports records verbatim and converges status
  FORWARD-ONLY (log-less, because the imported record IS the audit row), so
  the trail is complete with no duplication. (2) a LAGGING sink (source
  advanced the chain after a prior sync) was misreported as a divergent
  conflict with a permanent conflicts_with relation; divergence is now only
  detected when the sink's head is NOT an ancestor in the source's chain.
- 13 new Go tests (sync engine 12, CLI smoke 1) — total 100 Go tests green;
  build + vet + gofmt clean; live smoke: A→B round trip, idempotent re-run,
  full-lifecycle one-shot sync (0 phantom conflicts), fast-forward (0 false
  conflicts), divergent-chain conflict surfaced with conflicts_with relation.

## 0.0.1-prealpha.2 — 2026-08-02

### Added — MCP + HTTP surfaces (ADR-001 v0.2, Phase 2)

- **Shared domain services** (`internal/server/api.go`): one semantic surface
  for every transport — save/get/search/context/compare/doctor/review/promote/
  supersede/relations/transitions with a single error classification
  (not-found/invalid/conflict). The CLI now delegates compare/review/promote/
  supersede to it, so verdicts and lifecycle semantics are byte-identical on
  every surface (no re-derived logic, no drift risk).
- **MCP server** (`drenyra-engram mcp`): Model Context Protocol over stdio
  (JSON-RPC 2.0, newline-delimited) plus POST /mcp on the HTTP port. 12
  `engram_*` tools (save/get/get_by_topic/search/context/compare/doctor/review/
  promote/supersede/relations/transitions) with JSON-schema input validation
  (-32602 on shape errors); domain failures return in-band tool results with
  isError=true and the engine's stable error codes. initialize handshake,
  notifications, ping, tools/list supported.
- **HTTP API** (`drenyra-engram serve`): REST /v1/* surface bound to
  127.0.0.1 by default (fail closed), optional bearer token
  (DRENYRA_ENGRAM_TOKEN / --token) required on every request when set, and
  status mapping 400/404/409/500 with a machine-readable error envelope.
- **Non-authorization boundary kept on every surface**: no authorize/approve/
  allow tool, endpoint, or command — enforced by tests (catalog reflection,
  route 404s, CLI help).
- 42 new Go tests (server API 14, HTTP 10, MCP 15, CLI smoke 3) — total 87 Go tests green;
  build + vet clean; live smoke: CLI save → HTTP doctor/context/search → MCP
  over HTTP → MCP stdio round trip.

## 0.0.1-prealpha.1 — 2026-08-01

### Added — CLI complete (ADR-001 v0.2)

- `compare` (identity/scope/content deltas + relation verdict: supersedes /
  related / not_conflict — supersedes checks the SOURCE status, the successor
  stays draft/promoted), `review` (draft→reviewed), `promote` (reviewed→promoted),
  `supersede` (promoted→superseded with required target + relation). 45 Go tests
  green; non-authorization boundary kept (authorize/approve/allow rejected).


### Added — v0.2 standalone Go engine foundation (ADR-001)

- Go module + core types/validators, lifecycle machine, SQLite store (pure Go,
  immutable history, schema guards), scope-first search with cross-tenant
  isolation, and the `drenyra-engram` CLI (save/search/context/doctor). 40 Go
  tests green; the non-authorization boundary is enforced (no authorize commands).


### Added

- Repository identity scaffolding: README, LICENSE, SECURITY, CONTRIBUTING, CODEOWNERS, architecture and roadmap docs.
- Draft contract index (`contracts/`): `memory`, `scope`, `lifecycle`, `provenance`.
- **Memory core** — the observation model defined by the contracts: structured observations with type, scope (company/RUC/period), topic keys, lifecycle states (draft → reviewed → promoted → superseded), vigencia, and provenance.
    - **Non-authorization boundary** — documented and carried in the provenance contract: memory guides, it never authorizes.
    - **PR 1 — Functional vertical:** in-memory store with immutable revisions, scope-first search with the REQUIRED cross-tenant isolation property (company A memory never retrievable from company B, even with identical text/topic key), lifecycle transitions, and the compile-time `NonAuthorizing` guard (31 tests).
    - **Strategy freeze + packaging:**
      - ADR-001 (engine strategy: standalone Go engine long-term; v0.1 TS contracts + in-memory reference + PostgreSQL adapter; contracts stay neutral) and ADR-002 (storage authority: PostgreSQL transactional authority; in-memory = reference only).
      - Frozen-for-0.1 semantics in `contracts/`: immutable revision model, write idempotency (updated/conflict/unknown outcomes), scope-filter-before-ranking, UNKNOWN fail-closed, migration policy.
      - Build to `dist/` (tsc, NodeNext, declarations), `engines >= 22`, complete `files` manifest, subpath `exports`, `verify:package` + `verify-packed-install` + prepack/prepublishOnly gates + CI package job.

    ### Notes

    - Pre-alpha: contracts beyond the frozen-for-0.1 semantics are not frozen; relations, sync, and surfaces (MCP/CLI/HTTP/TUI) are planned in the ROADMAP.
    - Version policy: `0.0.1-prealpha.x` until the first frozen contract, then `0.1.0`.
