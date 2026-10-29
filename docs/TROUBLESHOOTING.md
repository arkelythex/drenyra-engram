# Troubleshooting

Find the message you see, then apply the fix. Every message below was
reproduced against the current CLI and is quoted exactly. Drenyra Engram fails
closed: when something is ambiguous it refuses, so most problems are a missing
or mismatched setting rather than a data problem.

CLI exit codes: `0` success, `1` a runtime or domain failure, `2` invalid
arguments.

## Quick index

| You see | Section |
| --- | --- |
| `FISCAL_WRITE_GATE_CLOSED` | [Writes rejected by the runtime mode](#fiscal_write_gate_closed-on-every-write) |
| `SCOPE_BINDING_REQUIRED` | [Legacy memory under `enforce`](#scope_binding_required-under-enforce) |
| `invalid DRENYRA_FISCAL_RUNTIME_MODE` | [Unknown runtime mode](#invalid-runtime-mode) |
| Search returns `[]` | [Nothing found in a scope you expected](#search-returns-an-empty-list) |
| `INVALID_SCOPE: organizationId must be a non-empty string` | [Incomplete scope over HTTP, MCP, or the default scope](#invalid_scope-organizationid-must-be-a-non-empty-string) |
| `invalid RUC` / `invalid period` | [Malformed scope values](#invalid-ruc-or-period) |
| `ENCRYPTION_REQUIRED` / `DECRYPTION_FAILED` / `INVALID_ENCRYPTION_KEY` | [Encryption key problems](#encryption-key-problems) |
| `AUTHENTICATION_REQUIRED` | [Approval or review without a session](#authentication_required) |
| `UNAUTHORIZED: missing or invalid bearer token` | [HTTP token](#http-401-unauthorized) |
| `invalid oidc configuration` | [OIDC startup](#oidc-startup-failures) |
| `unsupported store layout` | [Database schema](#unsupported-store-layout) |
| `database is locked` / slow writes | [Concurrent access](#database-locked-or-busy) |
| `invalid params: json: unknown field` | [MCP argument names](#mcp-invalid-params-json-unknown-field) |

## Runtime mode

`DRENYRA_FISCAL_RUNTIME_MODE` decides which company-scoped protected writes
(save, approve, supersede, link evidence, store object) the store accepts. It
is resolved once when the store opens.

| Mode | Accepts | Use it for |
| --- | --- | --- |
| unset → `shadow` | No protected writes; reads work | The fail-closed default |
| `enforce` | Writes carrying a strict v1 fiscal scope binding | Deployments |
| `legacy_compat` | Legacy, unbound writes | Local examples and migration windows only |
| `read_only` | No protected writes | Freezing a store |

### `FISCAL_WRITE_GATE_CLOSED` on every write

```text
drenyra-engram: FISCAL_WRITE_GATE_CLOSED: fiscal protected writes are disabled by runtime mode
```

The mode is `shadow` (unset) or `read_only`. When it is unset, the CLI also
prints a startup warning naming the mode. Set the mode deliberately:

```bash
export DRENYRA_FISCAL_RUNTIME_MODE=enforce
```

`drenyra-engram doctor` reports the active mode as `fiscalRuntimeMode`.

### `SCOPE_BINDING_REQUIRED` under `enforce`

```text
drenyra-engram: SCOPE_BINDING_REQUIRED: legacy protected mutation requires an explicit compatibility mode
```

`enforce` accepts only writes that carry a strict v1 fiscal scope binding
(`fiscalScope`, see [`contracts/fiscal-scope-v1.md`](../contracts/fiscal-scope-v1.md)).
Add the binding to the write. Use `legacy_compat` only for a bounded, local
migration of legacy data, and switch back afterwards.

### Invalid runtime mode

```text
drenyra-engram: invalid DRENYRA_FISCAL_RUNTIME_MODE "strict"
```

The value must be exactly one of `shadow`, `enforce`, `legacy_compat`, or
`read_only`. Every command refuses to open the store until it is.

## Scope

### Search returns an empty list

Scope is a filter, not a ranking hint: a memory is visible only under its exact
scope tuple. Check, in order:

1. **Period.** A query with `--period 202402` never sees a `202401` memory, and
   a query without `--period` only sees period-less memories.
2. **Organization.** The CLI fills `organizationId` with `cli`. A memory saved
   through MCP or HTTP with another organization is invisible to CLI reads.
3. **Company ID.** The CLI and HTTP derive `companyId` from the RUC. A memory
   saved through MCP with a custom `companyId` is invisible to derived-scope
   reads. Keep `companyId` equal to the RUC, or read through the surface you
   wrote with.
4. **Match mode.** CLI search requires every token by default; add `--any` for
   broader recall.

### `INVALID_SCOPE: organizationId must be a non-empty string`

HTTP and MCP reads take the full scope, and do not fill in `organizationId`:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:8787/v1/search?q=policy&organizationId=cli&ruc=20100039201&period=202401"
```

The same message prefixed with `DRENYRA_DEFAULT_SCOPE:` means the default scope
given to `mcp` or `serve` is incomplete. It must be an exact JSON scope:

```json
{"kind":"company","organizationId":"<organization-id>","companyId":"<company-id>","ruc":"<11-digit-ruc>","period":"<YYYYMM>"}
```

### Invalid RUC or period

```text
drenyra-engram: invalid RUC "123": expected exactly 11 digits
drenyra-engram: invalid period "202413": expected YYYYMM with month 01-12
```

Both exit with code `2`. Pass an 11-digit RUC and a `YYYYMM` period.

## Encryption key problems

Content encryption is off unless `DRENYRA_ENCRYPTION_MASTER_KEY` is set. Once a
store holds encrypted content, every process that reads it needs the same key.

| Message | Cause | Fix |
| --- | --- | --- |
| `INVALID_ENCRYPTION_KEY: DRENYRA_ENCRYPTION_MASTER_KEY must be hex or base64 of exactly 32 bytes` | The key does not decode to 32 bytes | Generate one with `openssl rand -hex 32` and store it in your secret manager |
| `ENCRYPTION_REQUIRED: observation content is encrypted at rest and no DRENYRA_ENCRYPTION_MASTER_KEY is configured` | The store is encrypted; this process has no key | Provide the key to this process |
| `DECRYPTION_FAILED: observation content could not be authenticated with the configured master key` | A different key from the one that wrote the data | Restore the original key; the engine never returns partial plaintext |

A lost master key cannot be recovered by the engine. See
[Data, security, and privacy](DATA-SECURITY-PRIVACY.md#encryption-and-visible-metadata).

## Identity and access

### `AUTHENTICATION_REQUIRED`

Approvals, review decisions, judgments, reconciliations, holds, and purges
derive the principal from an authenticated session, never from arguments.

- **CLI:** run `drenyra-engram auth login --token <token> --db <path>` first.
  For a local development identity, see `auth seed-local-dev`, which only runs
  when `DRENYRA_ENV=local_dev`.
- **MCP over stdio:** this is expected. The stdio server has no session
  binding, so decision tools always fail closed. Agents cannot approve.
- **HTTP:** authenticate the request with a session or a configured OIDC
  access token.

### HTTP 401 `UNAUTHORIZED`

```json
{"error":{"code":"UNAUTHORIZED","message":"missing or invalid bearer token"}}
```

`serve` was started with `--token` or `DRENYRA_ENGRAM_TOKEN`. Send
`Authorization: Bearer <token>` on every request.

### OIDC startup failures

```text
drenyra-engram: serve: invalid oidc configuration: oidc audience is required
```

Setting any `DRENYRA_OIDC_*` variable enables OIDC, which then requires both
`DRENYRA_OIDC_ISSUER` and `DRENYRA_OIDC_AUDIENCE`; `serve` refuses to start on a
partial configuration rather than failing later at request time. The issuer and
JWKS endpoint must use HTTPS. Read the
[OIDC architecture](architecture/oidc-access-token-identity.md) for the full
variable set. After an identity-provider key rotation or compromise, restart
the server: the JWKS cache has no TTL.

## Storage

### Unsupported store layout

```text
drenyra-engram: unsupported store layout: schema_version=99, supported=18 — fail closed; migrate additively, never rewrite
```

The database was written by a newer engine than the binary you are running.
Upgrade the binary; never edit `schema_meta` or downgrade a store. Older stores
migrate forward additively when opened. Take a backup before the first open
with a new release.

### Database locked or busy

Each connection waits up to 5 seconds (`busy_timeout = 5000`) for a write lock.
SQLite allows one writer at a time, so:

- Run one long-lived process (`serve` or `mcp`) per database, and route other
  clients through it.
- Do not place the database on a network filesystem.
- Never copy a live database with `cp`: the WAL sidecar files must stay
  consistent with it. Stop the process first or use SQLite's online backup.
  See [operability evidence](architecture/operability-evidence.md).

## MCP

### The server prints nothing

`drenyra-engram mcp` speaks newline-delimited JSON-RPC on stdin and stdout and
waits for an `initialize` request. Diagnostics go to stderr. Replay
[`examples/mcp/session.jsonl`](../examples/mcp/session.jsonl) to confirm the
binary works before you debug the client configuration.

### MCP `invalid params: json: unknown field`

Tool arguments are decoded strictly: an unknown field, including any attempt to
pass identity, is a JSON-RPC `-32602` error rather than being ignored.

Use the exact property names that `tools/list` publishes in each tool's
`inputSchema`; a test pins every advertised name and type to the decoder.
MCP decision and review tools use snake_case, for example
`accounting_approve(memory_id, expected_envelope_hash, reason, request_id)`,
as specified in the [approval contract](../contracts/approval.md). If an older
server rejects names copied from its own `tools/list`, upgrade it: earlier
builds advertised camelCase names for `accounting_approve`,
`accounting_review_detail`, `accounting_review_reject`, and
`accounting_review_return` that their decoders did not accept.

## Runtime and package

### Go version

The module declares Go 1.26. With an older toolchain, `go install` either
fails or downloads a newer toolchain when `GOTOOLCHAIN=auto` allows it.

### The npm package

`package.json` describes a TypeScript reference mirror used for Go↔TypeScript
golden-vector parity. It is not published and is not a runtime for the engine;
install the Go binary instead. See [Compatibility](COMPATIBILITY.md).

## Still stuck

Run `drenyra-engram doctor` and keep its JSON output. Open an issue with the
command, the exact message, the doctor report, and your OS. Remove every
database path, token, key, and real taxpayer value first. Report security
problems privately as described in [SECURITY.md](../SECURITY.md).
