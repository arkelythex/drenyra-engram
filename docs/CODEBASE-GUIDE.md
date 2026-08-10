# Drenyra Engram — Codebase Guide

> How the repository is organized and how to think about it. A mental model
> for contributors: where each invariant lives, how a feature flows through
> the layers, and where to look first for any given concern.

## Repository layout

```text
cmd/drenyra-engram/   CLI entrypoint (save, review, rule, object, verify, mcp, serve, …)
internal/core/        Domain model + PURE logic: memories, lifecycle, receipts,
                      verification layers, rule resolution, comprobante parsing
internal/store/       SQLite store (pure Go, immutable history, schema v14,
                      migrations, atomic transactions)
internal/search/      Scope-first token-overlap search + the deterministic
                      benchmark harness (internal/search/bench/)
internal/server/      Shared domain services + MCP (JSON-RPC) + HTTP REST adapters,
                      auth resolver, approval/judgment/review/rule/ingest services
internal/auth/        Verified principal, sessions, error codes
internal/authz/       Versioned policies: approval, judgment, evidence lifecycle
internal/sync/        Additive local-store reconciliation
contracts/            The frozen contract set (memory, scope, lifecycle, provenance,
                      receipts, verification, approval, judgment, closing, …)
core/  store/  authz/  lifecycle/   TypeScript reference + parity mirrors
testdata/golden/      Go↔TS golden vectors (byte-identical across runtimes)
docs/                 Architecture, decisions, product, demo, benchmark, security
```

## Mental model

**Think of the engine as three concentric layers:**

1. **The domain (internal/core)** — pure, deterministic, no I/O. The
   lifecycle machine, the envelope hashes, the receipt payloads, the
   verification layers, the rule-version resolver. If it can be computed
   without touching the disk, it lives here — and it is mirrored in
   TypeScript (`core/`, `lifecycle/`) with golden vectors proving parity.

2. **The store (internal/store)** — the only place that touches SQLite. Every
   write is a single-writer transaction; every mutation is immutable
   (revisions, not edits); every act emits a receipt in the same transaction.
   Schema migrations are fail-closed and versioned (v14 today).

3. **The adapters (internal/server + cmd)** — thin, stateless surfaces over
   the domain and store: MCP tools, HTTP routes, CLI commands. **They never
   invent authority** — the principal is derived only from the session
   (ADR-003), and the tool catalog has no authorize/allow operations.

**The invariant that ties them together:** *memory guides, policy restricts,
evidence demonstrates, receipt certifies, a professional authorizes.* If you
are adding a surface, ask: can it approve, post, or file anything? If yes, it
violates the non-authorization boundary.

## Where each concern lives

| Concern | Start here |
|---|---|
| Memory model, kinds, fiscal effects, hashes | `internal/core/types.go` |
| Lifecycle / human gate / `returned` | `internal/core/lifecycle.go` |
| Approval principal + policy | `internal/auth/` + `internal/authz/approval_policy.go` |
| Receipts (Ed25519) + payload versions | `internal/core/receipt.go` |
| Offline verification (12 layers) | `internal/core/verify.go` + `internal/server/verify_service.go` |
| Review workspace (queue/detail/decisions) | `internal/core/review.go` + `internal/store/review_store.go` |
| Fiscal policy memory (rules, impact) | `internal/core/rules.go` + `internal/server/rules_service.go` |
| Evidence objects + lifecycle | `internal/store/object_store.go` + `internal/store/purge_store.go` |
| Comprobante ingestion adapter | `internal/core/comprobante.go` + `internal/server/ingest_service.go` |
| Scope-first search + benchmark | `internal/search/` + `internal/search/bench/` |
| MCP tools / HTTP routes / CLI | `internal/server/mcp.go` / `http.go` / `cmd/drenyra-engram/` |
| Contracts (frozen surface) | `contracts/` |
| Go↔TS parity | `testdata/golden/` + `core/__tests__/` |

## Testing and quality gates

```bash
go test ./...            # full Go suite (10 packages)
go test ./internal/search/bench -v   # search benchmark (Recall/MRR/leakage)
npm test                 # TypeScript suite (360 tests)
npm run typecheck        # tsc --noEmit
go vet ./...             # vet
gofmt -l .               # formatting
go test ./internal/core -run TestGoldenVectorsGo   # Go↔TS golden parity
```

Conventions:

- **Money is whole int64 cents** — never floats; the @drenyra/pi guard enforces it.
- **Scope-first** — a new read or mutation must take the exact scope and fail
  closed on mismatch; cross-tenant invisibility is a tested invariant.
- **Conventional commits** — `feat:`, `fix:`, `docs:`, `test:`, `bench:`,
  `build:`, `refactor:` — atomic per milestone.
- **No AI attribution** in commits.
- Contract changes are high-materiality: versioned policy + proportional risk
  review before publish.

## Contributing

See [CONTRIBUTING.md](../CONTRIBUTING.md) and [SECURITY.md](../SECURITY.md).
New domain logic must join the Go↔TS parity mechanism; new fiscal behavior
must keep the non-authorization boundary intact.
