# Installing Drenyra Engram

A single static Go binary — no Node.js, no Python, no Docker required for the
core engine. SQLite is pure Go (`modernc.org/sqlite`), so there are no CGO
dependencies.

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

```bash
# Example (linux/amd64) — adapt the version
curl -fsSL -o drenyra-engram \
  https://github.com/arkelythex/drenyra-engram/releases/download/v0.7.0/drenyra-engram_linux_amd64
chmod +x drenyra-engram
./drenyra-engram doctor
```

Verify integrity:

```bash
sha256sum -c checksums.txt
```

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
[DOCS.md#environment-variables](DOCS.md#environment-variables):

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
- Schema migrations are fail-closed and transactional (v1 → v14 today); a
  migration conflict rolls back and leaves the store untouched.
- **Roll forward only**: after v0.6+ rows exist, older binaries cannot
  preserve policy metadata / structured links — retain the current database.
