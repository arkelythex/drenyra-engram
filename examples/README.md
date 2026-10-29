# Examples

Small, runnable scripts for each Drenyra Engram adapter. Each one creates a
temporary workspace, uses only the synthetic fixture in this directory, and
removes everything on exit. None of them touch `./engram.db` or your signing
keyring.

| Example | Shows | Expected result |
| --- | --- | --- |
| [`cli/quickstart.sh`](cli/quickstart.sh) | Save, scoped search, context, doctor | One search hit in `202401`; `[]` in `202402` |
| [`verification/verify-memory.sh`](verification/verify-memory.sh) | Signed receipt and offline verification | `"outcome": "passed"` and `Accounting correctness: NOT ASSERTED` |
| [`mcp/quickstart.sh`](mcp/quickstart.sh) | A scripted JSON-RPC session over stdio | 58 tools listed; `accounting_approve` fails with `AUTHENTICATION_REQUIRED` |
| [`http/quickstart.sh`](http/quickstart.sh) | Bearer-token HTTP adapter with curl | `401` without a token; `201` on save; one search hit |
| [`docker/`](docker/README.md) | The repository container image | A health report in `shadow` mode |
| [`mcp/client-config.json`](mcp/client-config.json) | A starting MCP client configuration | Replace every `<placeholder>` before use |

## Run them

From the repository root, with Go 1.26 or later:

```bash
bin_dir="$(mktemp -d)"
go build -o "$bin_dir/drenyra-engram" ./cmd/drenyra-engram
DRENYRA_ENGRAM_BIN="$bin_dir/drenyra-engram" bash examples/run-all.sh
```

Without `DRENYRA_ENGRAM_BIN`, the scripts call `drenyra-engram` from your
`PATH`. The HTTP example needs `curl` and listens on `127.0.0.1:18787`; set
`PORT` to change it.

## Rules every example follows

- **Synthetic data only.** The fixture uses a documentation RUC and invented
  content. Never replace it with taxpayer, customer, or production data.
- **Exact scope on every read.** Company, RUC, and period are part of the
  query, not a ranking hint. The CLI fills `organizationId` with `cli`; HTTP
  and MCP readers must pass it explicitly.
- **`legacy_compat` stays local.** The scripts set
  `DRENYRA_FISCAL_RUNTIME_MODE=legacy_compat` because the fixture is a
  legacy-class memory. Do not copy that setting into a deployment.
- **Approval is not an agent capability.** The MCP example shows the stdio
  server refusing an approval because it has no authenticated session.

When an example fails, look up its error code in
[Troubleshooting](../docs/TROUBLESHOOTING.md).
