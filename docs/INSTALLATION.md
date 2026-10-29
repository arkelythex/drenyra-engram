# Installing Drenyra Engram

A single static Go binary — no Node.js, no Python, no Docker required for the
core engine. SQLite is pure Go (`modernc.org/sqlite`), so there are no CGO
dependencies.

See [COMPATIBILITY.md](COMPATIBILITY.md) for supported platforms and versions.

## Prerequisites

- **Go 1.26+** to build from source (Go 1.26 is the current release).
- Any **MCP-capable agent** to consume the engine (Claude, pi, OpenCode,
  Gemini CLI, Codex, …) — see [CONSUMING.md](CONSUMING.md).

## Install from source

```bash
go install github.com/arkelythex/drenyra-engram/cmd/drenyra-engram@latest
```

The binary lands in `$(go env GOPATH)/bin`; add it to your `PATH` if needed.

## Release binaries

GitHub releases ship static binaries for `linux`/`darwin`/`windows` ×
`amd64`/`arm64`, each with an SPDX SBOM and a SHA-256 `checksums.txt`:

Each release publishes `drenyra-engram_<version>_<os>_<arch>.tar.gz`
(`.zip` on Windows) for `linux`, `darwin`, and `windows` on `amd64` and
`arm64`, a `checksums.txt`, and an SPDX SBOM per archive.

```bash
# Example: linux/amd64, release v0.9.0
version=0.9.0
base=https://github.com/arkelythex/drenyra-engram/releases/download/v$version
curl -fsSLO "$base/drenyra-engram_${version}_linux_amd64.tar.gz"
curl -fsSLO "$base/checksums.txt"
sha256sum --ignore-missing -c checksums.txt
tar -xzf "drenyra-engram_${version}_linux_amd64.tar.gz" drenyra-engram
./drenyra-engram doctor
```

A tagged release contains only what was merged before its tag; check the
[changelog](../CHANGELOG.md) `[Unreleased]` section for features that need a
source build.

## Verify the install

```bash
drenyra-engram doctor            # store health + schema version
drenyra-engram save --help       # surface help
```

## First run

```bash
drenyra-engram mcp               # MCP stdio server (agents) — DB ./engram.db
drenyra-engram serve             # HTTP REST /v1 + MCP /mcp on 127.0.0.1:8787
```

## Configuration

Everything is optional and environment-driven — see
[DOCS.md#environment-variables](../DOCS.md#environment-variables):

```bash
export DRENYRA_ENGRAM_DB="$HOME/.drenyra/engram.db"     # database path
export DRENYRA_ENGRAM_OBJECTS="$HOME/.drenyra/objects"  # evidence-object WORM root
export DRENYRA_ENGRAM_TOKEN="<secret>"                   # HTTP bearer token
export DRENYRA_DEFAULT_SCOPE='{"kind":"company","organizationId":"...","companyId":"...","ruc":"20100039201","period":"202601"}'
```

## Docker

A Dockerfile is provided for containerized deployments (the engine is
local-first; bind the data dir as a volume):

```bash
docker build -t drenyra-engram .
docker run -v "$PWD/data:/data" -e DRENYRA_ENGRAM_DB=/data/engram.db \
  drenyra-engram serve
```

> **Note:** the HTTP server binds to `127.0.0.1` by default and there is no
> network MCP endpoint — MCP is stdio only. For containerized agents, mount
> the binary and share a volume (see [CONSUMING.md](CONSUMING.md)).

## Upgrading

- The engine is **local-first**: back up the SQLite file and the objects root
  before upgrading.
- Schema migrations are fail-closed and transactional (v1 → v18 today); a
  migration conflict rolls back and leaves the store untouched.
- **Roll forward only**: after v0.6+ rows exist, older binaries cannot
  preserve policy metadata / structured links — retain the current database.
