# Tasks — sdd-060-tenant-cli

> Phase: tasks · Artifact: tasks · Status: draft
> Inputs: spec + design (required). Strict TDD per `phases.apply.strict_tdd: true` —
> apply runs RED → GREEN per slice; `go test ./...` and `npm test` stay green at
> every slice boundary.
> Delivery: `ask-on-risk` / `stacked-to-main` (repo default), review clone-off —
> ordinary-policy commits.

## Review Workload Forecast

| Field | Value |
| --- | --- |
| Estimated changed lines | ≈ 350–450 (fold + store reads + CLI + tests + docs) |
| 400-line budget risk | Medium — chained PRs planned (A → B → C) |
| Chained PRs recommended | Yes |
| Suggested split | PR A (tenant list) → PR B (fold + detection + dry-run) → PR C (apply + docs) |
| Chain strategy | stacked-to-main |

---

## PR A — tenant list (FR-TEN-1, AC-TEN-1)

- [x] A.1 RED — `internal/store/tenant_test.go` `TestTenantList`: seed identities
  (two tenants via `SeedIdentity`) + observations (memories under both scopes);
  assert the JSON-ready `TenantList` summary: organizationIds, companies
  (companyId/RUC/count), periods, totals, deterministic ordering. RED until the
  method exists or counts mismatch. <!-- sdd-owner: implementation -->
- [x] A.2 GREEN — `internal/core` `TenantSummary` types + `internal/store/tenant.go`
  `TenantList(ctx)`: SQL over `memberships`/`companies` UNION `observations`
  grouped by (organization_id, company_id), plus per-tenant period + count
  aggregation; deterministic ordering. No schema change. <!-- sdd-owner: implementation -->
- [x] A.3 RED — `cmd/drenyra-engram/tenant_test.go` `TestCLITenantList`: seeded
  store → `tenant list` JSON shape (tenants[], totalTenants), exit 0; empty
  store → `tenants: []` exit 0; usage error exit 2 on bad args. RED until the
  command exists. <!-- sdd-owner: implementation -->
- [x] A.4 GREEN — `cmd/drenyra-engram/tenant.go` `cmdTenant` with `list`
  subcommand + `case "tenant"` in the dispatch switch + usage line.
  Read-only; never emits per-tenant content (ids/counts only). <!-- sdd-owner: implementation -->

**Gate PR A:** `go test ./internal/store ./cmd/drenyra-engram` then `go test ./...`;
`gofmt -l .` / `go vet ./...` clean.

---

## PR B — fold + drift detection + dry-run (FR-TEN-2/3/4, AC-TEN-2/3)

- [x] B.1 RED — `internal/core/topic_fold_test.go` `TestFoldTopicKey`: case
  folding, punctuation stripping, whitespace collapse/trim, digits kept, unicode
  lower-case, empty input; plus golden-vector wiring (`TestGoldenVectorsGo`)
  against `testdata/golden/` vectors and the TS twin
  `core/__tests__/topic-fold.test.ts` (byte-identical outputs). RED until the
  function exists. <!-- sdd-owner: implementation -->
- [x] B.2 GREEN — `internal/core/topic_fold.go` `FoldTopicKey` (pure, per
  D-TEN-1) + `core/topic-fold.ts` twin + golden vectors registered in the
  Go↔TS parity mechanism. <!-- sdd-owner: implementation -->
- [x] B.3 RED — `internal/store/tenant_test.go` `TestDriftCandidates`: seeded
  chains under scope A with drifted topic keys (`rule/IGV credit` vs
  `rule/igv-credit`) → one drift group with the deterministic canonical (most
  observations, tie → lexicographic); distinct folded keys → no group; chains
  under scope B NEVER appear for an A query. RED until the method exists.
  <!-- sdd-owner: implementation -->
- [x] B.4 GREEN — `internal/store/tenant.go` `DriftCandidates(ctx, scope)`:
  exact-scope `SELECT DISTINCT topic_key, COUNT(*) ... GROUP BY topic_key`,
  grouped in Go by `core.FoldTopicKey` (D-TEN-2). <!-- sdd-owner: implementation -->
- [x] B.5 RED — `cmd/drenyra-engram/tenant_test.go` `TestCLIConsolidateDryRun`:
  seeded drift under A → default (dry-run) reports the drift group + canonical +
  zero writes (store snapshot via `doctor` digest identical before/after); RUC-B
  chains absent from the report; `--dry-run --apply` → usage error exit 2;
  invalid RUC → usage error exit 2. <!-- sdd-owner: implementation -->
- [x] B.6 GREEN — `consolidate` subcommand with `--ruc/--period/--dry-run/--apply`
  flags; default dry-run report shape (FR-TEN-4). <!-- sdd-owner: implementation -->

**Gate PR B:** full `go test ./...` + `npm test` + `TestGoldenVectorsGo`; typecheck
clean.

---

## PR C — apply merge + audit + docs (FR-TEN-5/6/7, AC-TEN-4/5/7)

- [x] C.1 RED — `cmd/drenyra-engram/tenant_test.go` `TestCLIConsolidateApply`:
  seeded drift under A → `--apply` merges drifted chains into the canonical
  (drifted chain head superseded; readers of the drifted key resolve to the
  canonical head); per-merge output + exit 0; `memory_superseded` receipts
  present and `transition_log` rows written. RED until the merge path exists.
  <!-- sdd-owner: implementation -->
- [x] C.2 GREEN — apply path in `cmdTenant`: per-merge head resolution (latest
  revision, active/pending_review, exact-scope pre-check — fail closed),
  `api.Supersede(driftedHead.ID, canonicalHead.ID, source)` with reason
  `tenant consolidate: canonical topic key <canonical>`; per-merge outcome
  printed; non-zero exit + failure list when any merge fails (no rollback of
  succeeded merges — documented). <!-- sdd-owner: implementation -->
- [x] C.3 RED — `TestCLIConsolidateIsolation` (AC-TEN-5): store with A + B
  drifted chains → `--apply --ruc A`; assert B chains untouched (no B receipt,
  no B transition, B reads unchanged). <!-- sdd-owner: implementation -->
- [x] C.4 TRIANGULATE — reviewer greps: no schema change, no money fields, no
  new error codes beyond the supersede path, non-authorization boundary intact;
  `git diff -- README.md DOCS.md ROADMAP.md` limited to the tenant-command docs;
  full gate (typecheck → vet → gofmt → go test → npm test) green.
  <!-- sdd-owner: implementation -->
- [x] C.5 GREEN — docs_as_code: usage text, README CLI list, DOCS.md tenant
  section, ROADMAP.md SDD-060 status note. <!-- sdd-owner: implementation -->

**Gate PR C (final):** config `verify_order` green; `TestGoldenVectorsGo` green;
adversarial isolation green; usage/docs updated.

---

## Cross-cutting checklist

- [x] Conventional commit per atomic milestone (feat: tenant list / feat:
  consolidate detection / feat: consolidate apply + docs); no AI attribution.
- [x] Strict TDD per slice: named failing tests land RED before their
  implementation; green at each boundary.
- [x] Scope stays structural and fails closed on mismatch; cross-tenant
  invisibility tested (AC-TEN-5).
- [x] Non-authorization boundary intact: consolidate authorizes nothing.
- [x] Golden parity joined: `FoldTopicKey` Go↔TS vectors byte-identical.
- [x] No money fields, no `any` in TS, no schema change.

## Definition of done

- [x] All tasks checked; every AC-TEN-1…7 verified green by its mapped test.
- [x] Full gates per config `verify_order` green at every PR boundary, plus
  `TestGoldenVectorsGo`.
- [x] Chain merged to main via stacked-to-main; delivery recorded before apply.
