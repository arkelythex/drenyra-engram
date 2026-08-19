# Design — sdd-060-tenant-cli

> Phase: design · Artifact: design · Status: draft
> Inputs: frozen spec. Delivery slices PR A (tenant list) → PR B (detection +
> dry-run) → PR C (apply + docs), stacked-to-main, ask-on-risk.

## Decisions

- `D-TEN-1` — **`FoldTopicKey` lives in `internal/core` with a TS twin + golden
  parity.** It is pure domain logic (config `golden_parity`: "new domain logic
  MUST join the Go<->TS golden parity mechanism"). Placement:
  `internal/core/topic_fold.go` + `core/topic-fold.ts` + golden vectors in
  `testdata/golden/` wired into `TestGoldenVectorsGo` and the TS golden test.
  Fold scope: `strings.ToLower` → rune filter (letters/digits/space) →
  whitespace collapse + trim. Accents NOT folded (no x/text dependency;
  documented in the contract of the function).
- `D-TEN-2` — **Read queries live in `internal/store`, executed by the CLI
  directly** (the CLI already uses `search.ScopeFirst(st, ...)` and `st.FindByScope`
  patterns). Two new read-only methods on `SQLiteStore`:
  - `TenantList(ctx context.Context) ([]core.TenantSummary, error)` — SQL over
    `memberships`/`companies` (identity side) + `observations` (data side:
    `SELECT organization_id, company_id, ruc, period, COUNT(*) ... GROUP BY`).
    Deterministic ordering (organizationId, companyId).
  - `DriftCandidates(ctx context.Context, scope core.Scope) ([]core.DriftGroup, error)`
    — `SELECT DISTINCT topic_key, COUNT(*) FROM observations WHERE
    <exact scope tuple>` then group in Go by `core.FoldTopicKey`. Exact-scope
    WHERE (fail closed; the store's own scope assertion pattern).
  The `core.TenantSummary` / `core.DriftGroup` shapes are PURE types in
  `internal/core` (no store imports).
- `D-TEN-3` — **Merge reuses `API.Supersede`** (the CLI's existing
  `server.New(st, "cli")` + `api.Supersede(id, target, cliSource(actor))` path,
  cmdSupersede pattern). No new mutation primitive; the supersede path already
  emits the receipt + transition_log (audit standard). Per-merge:
  - drifted head = latest revision with status in {active, pending_review}
    under the exact scope (query via existing chain/head accessors);
  - pre-check: `head.Scope` equals the exact tuple (fail closed per merge);
  - `api.Supersede(driftedHead.ID, canonicalHead.ID, source)` with reason
    `tenant consolidate: canonical topic key <canonical>`.
- `D-TEN-4` — **CLI: one `cmdTenant` in `cmd/drenyra-engram/tenant.go` +
  `case "tenant"` in the dispatch switch + usage lines.** Subcommands `list` /
  `consolidate` via `args[0]`. Flag set: `--db`, `--ruc`, `--period`,
  `--dry-run`, `--apply` (mutually exclusive — usage error 2 when both).
- `D-TEN-5` — **Dry-run is the default and performs ZERO writes** (spec
  FR-TEN-4). The drift report is computed entirely from the read query.
- `D-TEN-6` — **Tenant list is an operator surface** (same class as `doctor`):
  it enumerates ids/counts, never per-tenant content; documented in usage text
  and DOCS. No session required (local-store operator tool).
- `D-TEN-7` — **No schema change.** Both commands read existing tables and write
  only through the existing supersede path. Schema v14 untouched.

## File placement

| File | Purpose |
| --- | --- |
| `internal/core/topic_fold.go` | `FoldTopicKey` (pure) + `TenantSummary`/`DriftGroup` types |
| `core/topic-fold.ts` | TS twin (golden parity) |
| `internal/core/topic_fold_test.go` | fold unit tests |
| `core/__tests__/topic-fold.test.ts` | TS twin tests |
| `testdata/golden/` | Go↔TS golden vectors for the fold |
| `internal/store/tenant.go` | `TenantList` + `DriftCandidates` (read-only SQL) |
| `internal/store/tenant_test.go` | store read tests + isolation adversarial |
| `cmd/drenyra-engram/tenant.go` | `cmdTenant` (list / consolidate) |
| `cmd/drenyra-engram/tenant_test.go` | CLI tests (dry-run zero-write, apply merge+receipt, isolation) |
| `cmd/drenyra-engram/main.go` | dispatch `case "tenant"` + usage lines |
| `README.md`, `DOCS.md`, `ROADMAP.md` | docs_as_code |

## Behavior matrix — consolidate

| Caller | Mode | RUC | Before | After |
| --- | --- | --- | --- | --- |
| operator | default (dry-run) | A | n/a | report only, zero writes |
| operator | `--apply` | A | drifted chains under A | merged into canonical A chain; receipts + transition rows |
| operator | `--apply` | A (store has B too) | B chains | **B untouched** (isolation, AC-TEN-5) |
| operator | `--dry-run --apply` | A | n/a | usage error exit 2 (FR-TEN-2) |
| operator | invalid RUC | x | n/a | usage error exit 2 |

## Verification gates per PR

- PR A: `go test ./internal/store ./cmd/drenyra-engram` + full `go test ./...`;
  tenant list JSON shape + counts.
- PR B: fold tests + golden parity (`TestGoldenVectorsGo` + TS golden) +
  dry-run zero-write proof (store snapshot identical before/after).
- PR C: apply merge + receipt/transition assertions + adversarial isolation +
  usage/docs updates.
- Full chain gate per boundary: `npm run typecheck` → `go vet ./...` →
  `gofmt -l .` → `go test ./...` → `npm test`.
