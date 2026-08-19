# Drenyra Engram — Technical Reference

> The full reference for the engine: environment variables, CLI, MCP tools,
> HTTP endpoints, scope semantics, contracts, and verification. Companion to
> [README.md](README.md) (overview) and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
> (design).

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

---

## Environment variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `DRENYRA_ENGRAM_DB` | `./engram.db` | SQLite database path |
| `DRENYRA_ENGRAM_OBJECTS` | `./objects` | Evidence-object WORM root (`objects/<ab>/<cd>/<sha256>`) |
| `DRENYRA_ENGRAM_SIGNING_KEY` | `${UserConfigDir}/drenyra-engram/signing-keys.json` | Ed25519 signing keyring path (user-only, 0600) |
| `DRENYRA_ENGRAM_SESSION` | — | Session store override (auth/session lookup) |
| `DRENYRA_ENGRAM_TOKEN` | — | HTTP bearer token; when set, every request must present `Authorization: Bearer <token>` |
| `DRENYRA_ENCRYPTION_MASTER_KEY` | — | 32-byte master key (hex or base64) enabling per-tenant at-rest content encryption (sdd-060): company-scope narrative encrypted with tenant-derived HKDF/AES-256-GCM keys; reads fail closed (`ENCRYPTION_REQUIRED`/`DECRYPTION_FAILED`) without/with a wrong key |
| `DRENYRA_DEFAULT_SCOPE` | — | Exact scope injected into MCP `initialize` metadata (`drenyra/currentContext`) — never inferred |
| `DRENYRA_ENV` | — | Environment name (provenance) |

> **Required field (sdd-060-confidence-required, v17):** every observation
> carries a `confidence` probability 0..1 (never money); `SaveInput` and the
> API/MCP surfaces supply it, range validation runs on every write, and the
> store aborts `CONFIDENCE_REQUIRED` on a NULL-confidence write. Legacy rows
> written before v17 are preserved unchanged (NULLs read back as confidence 0).

## CLI reference

```text
drenyra-engram <command> [flags]     JSON output · exit codes 0/1/2
```

| Command | Purpose |
| --- | --- |
| `save` | Upsert an observation (new immutable revision); fiscal effects → `pending_review` |
| `search` | Scope-first token-overlap search (`--any` for broader recall) |
| `context` | Current memory per chain for a scope |
| `get` / `get-by-topic` / `chain` | One memory / latest revision / full revision history |
| `compare` | Identity/scope/content deltas + relation verdict |
| `doctor` | Store health: schema guards, counts, object findings, purge lifecycle |
| `approve` / `reject` | Authenticated human gate (session token + exact envelope hash) |
| `review queue\|detail\|reject\|return` | Professional review workspace (v0.9) |
| `rule show\|history\|impact` | Fiscal policy memory surfaces (Phase 6) |
| `judge propose\|confirm\|reject\|withdraw\|show` | Adjudicable conflicts |
| `reconcile ...` | First-class reconciliations |
| `object store\|get\|ingest` | WORM evidence objects; `ingest` parses an invoice XML (adapter contract) |
| `verify memory\|judgment\|receipt\|object` | Offline verification (12 layers, NOT ASSERTED) |
| `close` / `period-summary` / `compare-periods` | Monthly close + explainable period summary |
| `keys init\|show\|rotate` | Ed25519 signing-key lifecycle |
| `auth login` | Session-based authentication (approval principal) |
| `sync --from <db> --to <db>` | Additive store reconciliation (divergence surfaced, never silently resolved) |
| `encrypt [--dry-run | --apply]` | Re-encrypt legacy plaintext rows under `DRENYRA_ENCRYPTION_MASTER_KEY` (one transaction; hashes untouched; dry-run default) |
    | `tenant list` | Operator enumeration — ids/counts only, never per-tenant content (sdd-060 Phase 1) |
| `tenant consolidate --ruc <RUC> [--period] [--dry-run | --apply]` | Topic-key drift within one RUC; `--apply` merges drifted chains into the canonical chain via audited supersede (sdd-060 Phase 3) |
| `mcp` / `serve` | MCP stdio server / HTTP REST + MCP |

## MCP tools

The catalog is 57 tools: 13 `engram_*` (general memory) + 44 `accounting_*`
(fiscal). **There is no authorize/approve/allow tool** — memory never
authorizes. Domain failures return in-band results (`isError: true`) with
stable error codes; shape errors are JSON-RPC `-32602`.

| Family | Tools |
| --- | --- |
| `engram_save/get/get_by_topic/chain/search/context/compare/doctor/review/promote/supersede/relations/transitions` | General memory |
| `accounting_record/get/search/timeline/approve` | Records + human approval |
| `accounting_close_create/period_reopen` | Monthly close |
| `accounting_judgment_propose\|confirm\|reject\|withdraw` | Conflicts |
| `accounting_reconciliation_propose\|confirm\|reject\|withdraw` | Reconciliations |
| `accounting_review_queue/detail/reject/return` | Review workspace |
| `accounting_rule_show/history/impact` | Fiscal policy memory |
| `accounting_object_store/get` | WORM evidence objects |
| `accounting_retention_policy_put/resolve/evaluate` · `accounting_hold_place/lift/holds_list` · `accounting_purge_*` · `accounting_lifecycle_export` | Evidence lifecycle |
| `accounting_period_summary/current_context/compare_periods/context` | Explainability |

Authenticated decision tools (`accounting_approve`, `accounting_judgment_confirm`,
`accounting_review_reject`, …) derive the principal **only** from the session —
tool arguments never carry identity — and fail closed with
`AUTHENTICATION_REQUIRED` on a session-less server.

## HTTP API

`drenyra-engram serve --addr 127.0.0.1:8787 [--token <secret>]` binds to
localhost by default; with a token every request must present
`Authorization: Bearer <token>`. Error envelope:
`{"error": {"code", "message"}}` (400/404/409/500).

| Area | Routes |
| --- | --- |
| Memory | `POST /v1/observations` · `GET /v1/observations/{id}` · `GET /v1/topic/{topicKey}` · `GET /v1/chain` · `GET /v1/search` · `GET /v1/context` · `POST /v1/compare` · `GET /v1/relations` · `GET /v1/transitions` · `GET /v1/doctor` |
| Approval | `POST /accounting/memories/{memoryId}/approve\|reject\|return` (authenticated) |
| Review | `GET /accounting/review/queue` · `GET /accounting/review/{memoryId}` |
| Judgments / Reconciliations | `POST /accounting/judgments(/{id}/confirm\|reject\|withdraw)` · `/accounting/reconciliations…` |
| Rules | `GET /accounting/rules/{topic}(/history)(/impact?revision=N)` |
| Close | `POST /accounting/closings` · `POST /accounting/periods/{period}/reopen` · `GET /accounting/periods/compare` |
| Objects | `POST /accounting/objects` · `GET /accounting/objects/{objectId}` |
| Lifecycle | `POST /accounting/retention-policies…` · `POST /accounting/objects/{objectId}/holds` · `POST /accounting/purge-requests/…` · `GET /accounting/lifecycle/export` |
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
