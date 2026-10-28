# Drenyra Engram — Technical Reference

> The full reference for the engine: environment variables, CLI, MCP tools,
> HTTP endpoints, scope semantics, contracts, and verification. Companion to
> [README.md](README.md) (overview) and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
> (design).
>
> **Runtime:** the Go binary `drenyra-engram` is the shipped engine and the
> authority for everything below. The TypeScript package is a reference mirror
> for contract parity, not a runtime ([compatibility](docs/COMPATIBILITY.md#typescript-reference-mirror)).
>
> **Related:** [documentation index](docs/README.md) · [examples](examples/README.md) ·
> [troubleshooting](docs/TROUBLESHOOTING.md) · [compatibility](docs/COMPATIBILITY.md)

## Quick navigation

- [Environment variables](#environment-variables)
- [CLI reference](#cli-reference)
- [MCP tools](#mcp-tools)
- [HTTP API](#http-api)
- [Scope semantics](#scope-semantics)
- [The fiscal model](#the-fiscal-model)
- [Verification](#verification)
- [Contracts](#contracts)
- [Data and storage](#data-and-storage)
- [FAQ](#faq)
- [Known issues](#known-issues)

---

## Environment variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `DRENYRA_ENGRAM_DB` | `./engram.db` | SQLite database path |
| `DRENYRA_ENGRAM_OBJECTS` | `<dir-of-db>/objects` | Evidence-object WORM root (`objects/<ab>/<cd>/<sha256>`); the `--objects` flag overrides it |
| `DRENYRA_ENGRAM_SIGNING_KEY` | `${UserConfigDir}/drenyra-engram/signing-keys.json` | Ed25519 signing keyring path (user-only, 0600) |
| `DRENYRA_ENGRAM_SESSION` | `${UserConfigDir}/drenyra-engram/session.json` | CLI session file written by `auth login` (0600) |
| `DRENYRA_ENGRAM_TOKEN` | — | HTTP bearer token; when set, every request must present `Authorization: Bearer <token>` |
| `DRENYRA_ENCRYPTION_MASTER_KEY` | — | 32-byte master key (hex or base64) enabling per-tenant at-rest content encryption (sdd-060): company-scope narrative encrypted with tenant-derived HKDF/AES-256-GCM keys; reads fail closed (`ENCRYPTION_REQUIRED`/`DECRYPTION_FAILED`) without/with a wrong key |
| `DRENYRA_FISCAL_RUNTIME_MODE` | `shadow` | Write gate for company-scoped protected writes: `shadow` (default; protected writes fail closed with `FISCAL_WRITE_GATE_CLOSED`), `enforce` (only writes with a strict v1 fiscal scope binding), `legacy_compat` (legacy unbound writes; local and migration use only), `read_only` (no protected writes). Any other value is refused at startup. `doctor` reports it as `fiscalRuntimeMode`. See [troubleshooting](docs/TROUBLESHOOTING.md#runtime-mode) |
| `DRENYRA_OIDC_ISSUER` | — | Exact `iss` every OIDC access token must carry; must be an `https` URL. Required together with the audience |
| `DRENYRA_OIDC_AUDIENCE` | — | Exact `aud` (resource-server identifier). Required together with the issuer |
| `DRENYRA_OIDC_JWKS_URL` | `<issuer>/.well-known/jwks.json` | JWKS location; must be an `https` URL |
| `DRENYRA_OIDC_CLOCK_SKEW` | `30s` | Go duration tolerance for `exp`/`nbf`/`iat`; values above `5m` fail startup |
| `DRENYRA_OIDC_CLAIM_TENANT` | `tenant_id` | Claim name carrying the tenant id |
| `DRENYRA_OIDC_CLAIM_COMPANY` | `company_id` | Claim name carrying the company id |
| `DRENYRA_DEFAULT_SCOPE` | — | Exact scope injected into MCP `initialize` metadata (`drenyra/currentContext`) — never inferred |
| `DRENYRA_ENV` | — | Environment name; `local_dev` is required by `auth seed-local-dev` and rejected otherwise |

OIDC applies to `serve` only. Setting any `DRENYRA_OIDC_*` variable enables it,
and then the whole set must be valid: a partial or invalid set (for example an
issuer without an audience) fails startup. Tokens are verified statelessly
(RS256 only) and the tenant and company claims are cross-checked against the
active database membership; OIDC never creates sessions. See
[troubleshooting](docs/TROUBLESHOOTING.md#oidc-startup-failures) and
[the OIDC design note](docs/architecture/oidc-access-token-identity.md).

> **Required field (sdd-060-confidence-required, v17):** every observation
> carries a `confidence` probability 0..1 (never money); `SaveInput` and the
> API/MCP surfaces supply it, range validation runs on every write, and the
> store aborts `CONFIDENCE_REQUIRED` on a NULL-confidence write. Legacy rows
> written before v17 are preserved unchanged (NULLs read back as confidence 0).

## CLI reference

```text
drenyra-engram <command> [flags]     JSON output · exit codes 0 ok / 1 runtime error / 2 usage error
```

`drenyra-engram help` is the authoritative command list. The CLI fills
`organizationId` with `cli` and derives `companyId` from `--ruc` or
`--company`.

| Command | Purpose |
| --- | --- |
| `save` / `record` | Upsert an observation (new immutable revision); fiscal effects → `pending_review` |
| `search` | Scope-first token-overlap search (`--any` for broader recall) |
| `context` | Current memory per chain for a scope |
| `timeline` | Revision history of one topic key in a scope |
| `compare` | Identity/scope/content deltas + relation verdict |
| `doctor` | Store health: schema guards, counts, object findings, purge lifecycle, `fiscalRuntimeMode` |
| `approve` | Authenticated human gate (session token + exact envelope hash) |
| `reject` / `void` / `supersede` | End a review / annul without successor / route readers to `--target` |
| `link-evidence` | Link evidence references to a memory |
| `review queue\|detail\|reject\|return` | Professional review workspace (v0.9) |
| `rule show\|history\|impact` | Fiscal policy memory surfaces (Phase 6) |
| `judge propose\|confirm\|reject\|withdraw\|show` | Adjudicable conflicts |
| `reconcile propose\|confirm\|reject\|withdraw\|show` | First-class reconciliations |
| `object store\|get\|ingest` | WORM evidence objects; `ingest` parses an invoice XML (adapter contract) |
| `retention-policy put\|resolve\|evaluate` · `hold place\|lift\|list` · `purge request\|approve\|reject\|cancel\|withdraw\|execute` · `export lifecycle` | Evidence lifecycle |
| `verify memory\|judgment\|receipt\|object` | Offline verification (12 layers, NOT ASSERTED) |
| `close create\|show\|reopen` · `period-summary` · `compare-periods` | Monthly close + explainable period summary |
| `reconstructibility` | Read-only reconstructibility metric for a period |
| `keys init\|show\|rotate` | Ed25519 signing-key lifecycle |
| `auth login` · `auth seed-local-dev` | Session-based authentication (approval principal); `seed-local-dev` needs `DRENYRA_ENV=local_dev` |
| `sync --from <db> --to <db>` | Additive store reconciliation (divergence surfaced, never silently resolved) |
| `encrypt [--dry-run \| --apply]` | Re-encrypt legacy plaintext rows under `DRENYRA_ENCRYPTION_MASTER_KEY` (one transaction; hashes untouched; dry-run default) |
| `tenant list` | Operator enumeration — ids/counts only, never per-tenant content (sdd-060 Phase 1) |
| `tenant consolidate --ruc <RUC> [--period] [--dry-run \| --apply]` | Topic-key drift within one RUC; `--apply` merges drifted chains into the canonical chain via audited supersede (sdd-060 Phase 3) |
| `mcp` / `serve` | MCP stdio server / HTTP REST + MCP |

There are no `get`, `get-by-topic`, or `chain` CLI commands. Those reads exist on
the HTTP (`/v1/observations/{id}`, `/v1/topic/{topicKey}`, `/v1/chain`) and MCP
(`engram_get`, `engram_get_by_topic`, `engram_chain`) surfaces; on the CLI use
`search`, `context`, and `timeline`.

## MCP tools

The catalog is **58 tools**: 13 `engram_*` (general memory) + 45 `accounting_*`
(fiscal). It is the exact `tools/list` response of `drenyra-engram mcp`; run the
[MCP example](examples/mcp/quickstart.sh) to see it. Domain failures return
in-band results (`isError: true`) with stable error codes; shape errors are
JSON-RPC `-32602`.

**Memory never authorizes.** There is no authorize, allow, or execute tool.
`accounting_approve` exists, and it is the professional review of a memory — not
authorization of a declaration, payment, or filing. It derives the principal
**only** from an authenticated session (tool arguments never carry identity) and
fails closed with `AUTHENTICATION_REQUIRED` on stdio, where there is no session.
The same holds for the other decision tools (`accounting_judgment_confirm`,
`accounting_review_reject`, and so on).

| Family | Tools |
| --- | --- |
| General memory (13) | `engram_save` · `engram_get` · `engram_get_by_topic` · `engram_chain` · `engram_search` · `engram_context` · `engram_compare` · `engram_doctor` · `engram_reject` · `engram_void` · `engram_supersede` · `engram_relations` · `engram_transitions` |
| Records and human approval | `accounting_record` · `accounting_get` · `accounting_search` · `accounting_timeline` · `accounting_approve` |
| Review workspace | `accounting_review_queue` · `accounting_review_detail` · `accounting_review_reject` · `accounting_review_return` |
| Fiscal policy memory | `accounting_rule_show` · `accounting_rule_history` · `accounting_rule_impact` · `accounting_reconstructibility` |
| Monthly close | `accounting_close_create` · `accounting_period_reopen` |
| Conflicts (judgments) | `accounting_judgment_propose` · `accounting_judgment_confirm` · `accounting_judgment_reject` · `accounting_judgment_withdraw` |
| Reconciliations | `accounting_reconciliation_propose` · `accounting_reconciliation_confirm` · `accounting_reconciliation_reject` · `accounting_reconciliation_withdraw` |
| Evidence objects | `accounting_link_evidence` · `accounting_object_store` · `accounting_object_get` |
| Evidence lifecycle | `accounting_retention_policy_put` · `accounting_retention_policy_resolve` · `accounting_retention_policy_evaluate` · `accounting_hold_place` · `accounting_hold_lift` · `accounting_holds_list` · `accounting_purge_request` · `accounting_purge_approve` · `accounting_purge_reject` · `accounting_purge_cancel` · `accounting_purge_withdraw` · `accounting_purge_finalize` · `accounting_lifecycle_export` |
| Explainability | `accounting_period_summary` · `accounting_current_context` · `accounting_compare_periods` · `accounting_compare` · `accounting_context` |
| Diagnostics | `accounting_doctor` |

Protocol versions the server negotiates: see
[compatibility](docs/COMPATIBILITY.md#mcp). Client configuration:
[`examples/mcp/client-config.json`](examples/mcp/client-config.json).

## HTTP API

`drenyra-engram serve --addr 127.0.0.1:8787 [--token <secret>]` binds to
localhost by default; with a token every request must present
`Authorization: Bearer <token>`. Error envelope:
`{"error": {"code", "message"}}` (400/404/409/500).

**HTTP reads require an explicit `organizationId`** (plus `ruc` and, usually,
`period`) as query parameters; there is no default. Only the CLI fills
`organizationId` with `cli`. A request without it fails with
`INVALID_SCOPE: organizationId must be a non-empty string`. Runnable requests:
[`examples/http/quickstart.sh`](examples/http/quickstart.sh).

| Area | Routes |
| --- | --- |
| Memory | `POST /v1/observations` · `GET /v1/observations/{id}` · `GET /v1/topic/{topicKey}` · `GET /v1/chain` · `GET /v1/search` · `GET /v1/context` · `POST /v1/compare` · `POST /v1/observations/{id}/reject\|void\|supersede` · `GET /v1/relations` · `GET /v1/transitions` · `GET /v1/doctor` |
| Approval | `POST /accounting/memories/{memoryId}/approve\|reject\|return` (authenticated) |
| Review | `GET /accounting/review/queue` · `GET /accounting/review/{memoryId}` |
| Judgments / Reconciliations | `POST /accounting/judgments(/{id}/confirm\|reject\|withdraw)` · `/accounting/reconciliations…` |
| Rules | `GET /accounting/rules/{topic}(/history)(/impact?revision=N)` |
| Reconstructibility | `GET /accounting/reconstructibility` |
| Close | `POST /accounting/closings` · `POST /accounting/periods/{period}/reopen` · `GET /accounting/periods/compare` |
| Objects | `POST /accounting/objects` · `GET /accounting/objects/{objectId}` |
| Lifecycle | `POST /accounting/retention-policies…` · `POST /accounting/objects/{objectId}/holds` · `POST /accounting/holds/{holdId}/lift` · `POST /accounting/objects/{objectId}/purge` · `POST /accounting/purge-requests/{requestId}/approve\|reject\|cancel\|withdraw\|execute` · `GET /accounting/lifecycle/export` |
| MCP | `POST /mcp` (streamable HTTP JSON) |

## Scope semantics

Scope is the engine's spine: `organizationId` (the firm) + `companyId`/`ruc`
(the client) + `period` (fiscal period). **Scope is a structural filter, never
a post-filter** — a company-A query can never see company-B memory, and this
is a tested invariant (`TestReviewQueueScopeIsolation`, cross-tenant probes in
the search benchmark with exactly 0 leakage).

- The **CLI and HTTP** derive `companyId = ruc`; reads only see observations
  saved with that derived scope.
- **MCP** accepts the full scope tuple; saving with a custom `companyId`
  creates memory that CLI/HTTP derived-scope queries will not surface
  (exact-scope semantics, fail-closed direction).
- Save company memory through the surface you intend to query it from, or keep
  `companyId = ruc`.

## The fiscal model

- **Eight kinds** — fact, evidence, decision, rule, exception, control,
  obligation, summary.
- **Eight fiscal effects** — none, journal_entry, declaration, closing,
  adjustment, reclassification, approval, sunat_filing. A non-none effect puts
  the memory behind the **human approval gate** (`pending_review`).
- **Triple timestamps** — `effectiveAt` (accounting-wise), `recordedAt`
  (system entry), `observedAt` (detected) — a late event affecting a previous
  closed period is visible as such.
- **Materiality** — declared threshold in whole int64 cents + a level
  (`normal`/`material`/`critical`) that raises the approval ladder and demands
  review checks (anti-rubber-stamp).
- **Vigencia** — `Validity{effectiveAt, expiresAt, source}` half-open windows;
  expired memories surface as stale, never as current fact.
- **Lifecycle** — `pending_review → approved | rejected | returned`; terminal
  `rejected`/`voided`/`superseded` never reopen; `returned` is non-terminal
  (an agent Save re-enters `pending_review`).

## Verification

`verify memory|judgment|receipt|object` is **read-only** over the local store,
answering per layer: payload canonicalization, envelope integrity, signature,
signing-key validity, tenant/company scope, chain link, principal provenance,
supersession chain, evidence availability, object availability, rule
availability, and (Phase 6) rule version/vigencia. Every report ends with
**"Accounting correctness: NOT ASSERTED"** — a valid signature proves nobody
altered the act, never that the professional decision is correct.

## Contracts

The frozen contract set lives in [contracts/](contracts/README.md): memory,
scope, lifecycle, provenance, receipts (Ed25519), verification, approval
(principal + policy), judgment, reconciliation, closing, period comparison.
They are the durable surface — the transport- and implementation-agnostic
contracts, mirrored Go↔TS with golden vectors.

## Data and storage

- **SQLite** (pure Go, `modernc.org/sqlite`) — immutable history, schema
  guards, WAL-safe single-writer discipline.
- **Evidence objects** — content-addressed WORM bytes under
  `objects/<ab>/<cd>/<sha256>`; the object id IS the SHA-256 of the bytes;
  retention policies, legal holds, approved purge and lifecycle export are
  policy-backed (v0.8).
- **Receipts** — Ed25519-signed immutable acts in a per-subject hash chain.
- **Sync** — additive reconciliation between local stores; divergence is
  surfaced with `conflicts_with` relations, never silently resolved.

## FAQ

**Is Drenyra Engram a ledger?** No — memory guides, the deployment-selected
ledger is the source of truth.

**Can agents approve?** No — the gate is human-only, authenticated,
hash-guarded, and SoD-separated.

**Does verification assert correctness?** Never — reports end with
"Accounting correctness: NOT ASSERTED".

**Is there a SUNAT integration?** The ingestion adapter contract is defined;
no production integration exists.

## Known issues

- None open in this reference. Messages and recovery steps live in [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md).
