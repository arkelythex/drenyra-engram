# Compatibility

> What Drenyra Engram runs on, which versions it needs, and how stable each
> surface is. Every value below is read from `go.mod`, `package.json`,
> `.goreleaser.yml`, the `Dockerfile`, or the Go source named beside it.

## At a glance

| Area | Value | Source |
| --- | --- | --- |
| Release status | **Alpha** — not production-ready | [README](../README.md#maturity) |
| Shipped runtime | Go binary `drenyra-engram` | `cmd/drenyra-engram/` |
| Go toolchain (build from source) | 1.26 | `go.mod` (`go 1.26`) |
| SQLite driver | `modernc.org/sqlite` v1.56.0, pure Go, no CGO | `go.mod` |
| Store schema version | 18 | `internal/store/store.go` (`schemaVersion`) |
| MCP protocol versions | `2025-06-18` (latest), `2025-03-26`, `2024-11-05` | `internal/server/mcp.go` |
| MCP tools | 58 (13 `engram_*` + 45 `accounting_*`) | `tools/list` at runtime |
| TypeScript package | `0.0.1-prealpha.1`, unpublished reference mirror | `package.json` |

## Platforms

Release builds come from [`.goreleaser.yml`](../.goreleaser.yml) with
`CGO_ENABLED=0` and `-trimpath`:

| OS | amd64 | arm64 |
| --- | --- | --- |
| Linux | build target | build target |
| macOS (darwin) | build target | build target |
| Windows | build target | build target |

- Archives are `tar.gz`, and `zip` on Windows. Each release attaches a SHA-256
  `checksums.txt` and an SPDX SBOM per binary.
- "Build target" means the release configuration produces the binary. The
  repository's CI workflows run on `ubuntu-latest` only, so Linux is the only
  platform exercised by automated tests.
- The `Dockerfile` builds on `golang:1.26-alpine` and runs on `alpine:3.20`.
  See [`examples/docker/`](../examples/docker/README.md).

## Go

- Building from source needs **Go 1.26**, the version in `go.mod`. With an older
  toolchain, `go install` fails or downloads a newer toolchain when
  `GOTOOLCHAIN=auto` allows it.
- Release binaries need no Go installation.

## SQLite and schema

- The store is a single SQLite file. The driver is pure Go
  (`modernc.org/sqlite`); no C compiler or system SQLite library is required.
- The current schema version is **18**. `drenyra-engram doctor` reports it as
  `"schemaVersion":18`.
- **Migrations are additive and forward-only.** Opening an older store migrates
  it in place, inside transactions; a failed migration rolls back and leaves
  the store untouched. There is no downgrade path: back up the database file
  and the objects directory before opening a store with a new release.
- **A newer store fails closed.** An older binary refuses a store written by a
  newer schema:

  ```text
  drenyra-engram: unsupported store layout: schema_version=99, supported=18 — fail closed; migrate additively, never rewrite
  ```

  Upgrade the binary. Never edit `schema_meta` to bypass the check. See
  [Troubleshooting](TROUBLESHOOTING.md#unsupported-store-layout).
- Runtime write behavior depends on `DRENYRA_FISCAL_RUNTIME_MODE`
  (`shadow` by default, `enforce`, `legacy_compat`, `read_only`). The modes are
  described in [Troubleshooting](TROUBLESHOOTING.md#runtime-mode) and
  [DOCS.md](../DOCS.md#environment-variables).

## MCP

- `drenyra-engram mcp` speaks newline-delimited JSON-RPC over **stdio**.
  `drenyra-engram serve` additionally exposes the same server at `POST /mcp`
  (JSON over HTTP, bound to `127.0.0.1` by default).
- During `initialize`, the server echoes the client's `protocolVersion` when it
  is one of `2025-06-18`, `2025-03-26`, or `2024-11-05`; otherwise it answers
  with `2025-06-18`.
- Any MCP client that can launch a stdio server can use it; the repository does
  not test against specific client products. A starting configuration is in
  [`examples/mcp/client-config.json`](../examples/mcp/client-config.json).
- Identity comes from an authenticated session, never from tool arguments. On
  stdio there is no session, so decision tools such as `accounting_approve` fail
  closed with `AUTHENTICATION_REQUIRED`.
- Every property advertised in a tool's `inputSchema` is accepted by its
  decoder under that name and type (`TestMCPInputSchemaMatchesDecoder`).

## TypeScript reference mirror

The TypeScript at the repository root (`core/`, `store/`, `search/`,
`lifecycle/`, `authority/`, `index.ts`) is a **reference mirror**, not a
runtime.

| Property | Value |
| --- | --- |
| Package | `drenyra-engram` `0.0.1-prealpha.1` |
| Published | No. The registry package is not part of the supported install path |
| Executable | None (`package.json` has no `bin`) |
| Engines | Node `>=22`; the CI scripts use Bun 1.3.11 |
| Purpose | Reference implementation of the contracts and Go↔TypeScript golden-vector parity (`testdata/golden/`) |

Use it to read the reference behavior or to run the parity suite
(`npm test` or `bun run test`, `npm run typecheck`). Do not deploy it. Where Go
and TypeScript disagree, the Go source and the [contracts](../contracts/README.md)
win.

## Stability levels

| Surface | Level | What it means |
| --- | --- | --- |
| [Contracts](../contracts/README.md) | Versioned; most are marked frozen | Each contract declares its version and status in the index. Changes are public-surface changes. |
| CLI, HTTP, and MCP surfaces | Alpha | Names and shapes can change before 1.0. Consult the [changelog](../CHANGELOG.md) before upgrading. |
| Store schema | Additive, forward-only | Newer schema fails closed in older binaries. |
| Receipts and verification | Contract-versioned | Verification reports integrity and provenance only; they end with "Accounting correctness: NOT ASSERTED". |
| TypeScript package | Pre-alpha, unpublished | May change without notice. |

The [version policy](../RELEASING.md#version-policy) states how versions are
assigned. Post-0.1 versions follow Semantic Versioning.

## Release status

- The project is **alpha**. The [README](../README.md#maturity) states what the
  repository does and does not demonstrate.
- Published history is in the [changelog](../CHANGELOG.md); planned work and
  readiness gates are in the [roadmap](../ROADMAP.md). This page does not
  duplicate them.
- Git tags in the repository today: `v0.2.0`, `v0.2.1`, `v0.9.0`. Treat
  [RELEASING.md](../RELEASING.md) and the GitHub releases page as the authority
  for what is published. Release builds stamp the engine version reported in
  MCP `serverInfo` from the tag (`.goreleaser.yml` ldflag on
  `internal/server.engineVersion`); a source build reports the default `0.9.0`.
- No compliance, certification, production-readiness, cloud, ERP, or SUNAT
  integration claim is made by this repository.
