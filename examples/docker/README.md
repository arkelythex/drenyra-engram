# Docker example

The repository [`Dockerfile`](../../Dockerfile) builds a static binary on
`golang:1.26-alpine` and runs it on `alpine:3.20`. The image starts the HTTP
adapter on `0.0.0.0:8733` inside the container and stores the database at
`/data/engram.db` on a volume.

```bash
docker build -t drenyra-engram:local .

docker run --rm \
  -p 127.0.0.1:8733:8733 \
  -v drenyra-engram-data:/data \
  -e DRENYRA_ENGRAM_TOKEN=example-token-not-a-secret \
  drenyra-engram:local
```

Publish the port on `127.0.0.1` only, and always set a token: inside the
container the adapter listens on every interface.

```bash
curl -s -H 'Authorization: Bearer example-token-not-a-secret' \
  http://127.0.0.1:8733/v1/doctor
```

Expected: a JSON health report with `"schemaVersion"` and
`"fiscalRuntimeMode": "shadow"`.

## Runtime mode

The image does not set `DRENYRA_FISCAL_RUNTIME_MODE`, so it starts in the
fail-closed `shadow` mode: reads work, and company-scoped protected writes
return `FISCAL_WRITE_GATE_CLOSED`. Choose a mode deliberately with
`-e DRENYRA_FISCAL_RUNTIME_MODE=enforce` (strict v1 fiscal binding) rather than
reaching for `legacy_compat`. See [Troubleshooting](../../docs/TROUBLESHOOTING.md#fiscal_write_gate_closed-on-every-write).

## What the image does not do

- It holds no signing key. Mount a keyring and set `DRENYRA_ENGRAM_SIGNING_KEY`
  if receipts are required, and protect that mount like a secret.
- It does not back up `/data`. The volume holds the database and the evidence
  objects root; back both up together, as described in
  [Data, security, and privacy](../../docs/DATA-SECURITY-PRIVACY.md#sync-and-recovery).
- It is a local evaluation image, not a hardened production deployment.
