# Spec — sdd-060-tenant-cli

> Phase: spec · Artifact: spec · Status: draft
> Inputs: proposal. Frozen bindings: scope_first, golden_parity,
> non_authorization_boundary, pr_size, docs_as_code, strict TDD for apply.

## Functional requirements

### FR-TEN-1 — tenant list (Phase 1)

`drenyra-engram tenant list [--db <path>]` prints a JSON document to stdout:

```json
{
  "tenants": [
    {
      "organizationId": "org-1",
      "companies": [
        {"companyId": "co_a", "ruc": "20100039201", "name": "", "memoryCount": 3}
      ],
      "periods": ["202401", "202402"],
      "memoryCount": 3
    }
  ],
  "totalTenants": 1
}
```

- Source: identities (memberships/companies tables: tenant_id, company_id, RUC,
  name) UNION observations (organization_id, company_id, ruc, period, counts).
- Read-only; no per-tenant content (what/why/learned, topic keys) is emitted.
- Exit 0 on success; exit 2 on usage error; exit 1 on store failure.
- Scope-neutral operator surface (same class as `doctor`), documented as such.

### FR-TEN-2 — tenant consolidate command shape (Phase 3)

`drenyra-engram tenant consolidate --ruc <11 digits> [--period <YYYYMM>]
[--dry-run | --apply] [--db <path>]`

- `--ruc` REQUIRED and validated (exactly 11 digits); `--period` optional.
- `--dry-run` and `--apply` are mutually exclusive — both set is a usage error
  (exit 2). NEITHER set defaults to `--dry-run` (ZERO mutation by default).
- Scope derivation: `cliCompanyScope` semantics (organizationId = CLI org,
  companyId = RUC) — same exact-scope tuple the CLI uses everywhere.

### FR-TEN-3 — drift definition

A **canonical fold** `FoldTopicKey(raw) string`:

1. Unicode lower-case (`strings.ToLower`).
2. Punctuation removed (rune class filter — letters, digits, spaces kept).
3. Whitespace runs collapsed to a single space, trimmed.

Accent folding is EXPLICITLY out of scope (documented limitation). Two chains
`drift` when `FoldTopicKey(topicKeyA) == FoldTopicKey(topicKeyB)` AND
`topicKeyA != topicKeyB` under the SAME exact scope tuple.

`FoldTopicKey` is PURE, lives in `internal/core`, has a TypeScript twin in
`core/`, and joins the Go↔TS golden parity mechanism (config `golden_parity`).

### FR-TEN-4 — drift detection (dry-run)

`tenant consolidate` (dry-run default) prints:

```json
{
  "ruc": "20100039201",
  "period": "",
  "dryRun": true,
  "driftGroups": [
    {
      "canonical": "rule/igv-credit",
      "canonicalChainSize": 4,
      "drifted": [
        {"topicKey": "rule/IGV credit", "chainSize": 2}
      ]
    }
  ],
  "totalDriftGroups": 1,
  "totalDriftedChains": 1
}
```

- ZERO mutation: no store writes at all in dry-run mode.
- Canonical candidate = the raw key with the most observations in the group;
  ties broken lexicographically (deterministic).
- Only chains with ≥1 observation under the exact scope are considered.
- Empty store / no drift → `driftGroups: []`, exit 0.

### FR-TEN-5 — merge (--apply)

For each drift group, for each drifted chain:

1. Resolve the drifted chain's CURRENT head (latest revision, active status)
   under the exact scope; resolve the canonical chain's current head.
2. `API.Supersede(driftedHead.ID, canonicalHead.ID, source)` — the EXISTING
   supersede path (atomic per act, `memory_superseded` receipt, `transition_log`
   entry, idempotency by the API's own rules).
3. Reason recorded: `tenant consolidate: canonical topic key <canonical>`.

Per-merge outcome printed (from → to, id, receipt presence, error if any).
If ANY merge fails, the command exits non-zero with the failures listed;
successful merges are NOT rolled back (each act is independently atomic —
documented semantics).

### FR-TEN-6 — audit trail

Every `--apply` merge emits the standard audit artifacts of the supersede path:
`memory_superseded` Ed25519 receipt + `transition_log` row (from/to status,
actor, timestamp). The command prints the receipt digest per merge. No new audit
mechanism (SDD-060 AC-4: same standard as `DRENYRA_AUDIT_LOG`).

### FR-TEN-7 — tenant isolation

- The drift query is scoped by the exact `(organization_id, company_id, ruc
  [, period])` tuple; a chain outside the tuple is never returned.
- The merge pre-check asserts both heads' scope equals the exact tuple; any
  mismatch fails that merge closed (no cross-RUC supersede).
- Adversarial test: consolidate `--ruc A` on a store with RUC-A and RUC-B chains
  — RUC-B chains are never listed, never merged, receipts/transition_log show no
  RUC-B writes (SDD-060 AC-5 pattern).

### FR-TEN-8 — constraints

- No schema change (schema v14 untouched; pure read queries + existing supersede).
- No money fields anywhere (IR-1; the repo convention).
- No new error codes beyond what the supersede path already defines.
- Non-authorization boundary intact: consolidate authorizes nothing.

## Acceptance criteria

- AC-TEN-1: `tenant list` returns correct tenant/company/count enumeration on a
  seeded multi-tenant store (identities + observations).
- AC-TEN-2: `FoldTopicKey` is pure, deterministic, and golden-parity-covered
  (Go↔TS byte-identical vectors).
- AC-TEN-3: dry-run consolidate detects drift groups with the deterministic
  canonical candidate and performs ZERO store writes (store snapshot identical).
- AC-TEN-4: `--apply` merges drifted chains via supersede; each merge emits a
  receipt + transition_log row; readers of a drifted key now resolve to the
  canonical chain head.
- AC-TEN-5: adversarial isolation — `consolidate --ruc A` never touches RUC-B
  (no read leak, no write, no receipt).
- AC-TEN-6: `--dry-run` + `--apply` together is a usage error (exit 2); invalid
  RUC is a usage error.
- AC-TEN-7: full gates green — `go test ./...`, `npm test`, typecheck, vet,
  gofmt, `TestGoldenVectorsGo`; usage text + README + DOCS updated
  (docs_as_code).

## Out of scope

MCP/HTTP tenant surfaces; accent/synonym folding; auto-merge without `--apply`;
cross-RUC consolidate; schema or contract changes; encryption (Unit C).
