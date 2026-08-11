# Fuzz seed corpora — internal/core

Seed policy (spec FR-2 / design D-10): corpus entries live at
`testdata/fuzz/<FuzzTargetName>/` in the go fuzz corpus format (a
`go test fuzz v1` header line plus one `[]byte("…")` value line per entry,
exactly as the go tool reads and writes them). Every entry is a permanent
regression (AC-2): it replays green under `go test ./...` and its deletion
fails `TestFuzzCoreCorpusManifestNeverDeleted`.

> **Layout note:** this README deliberately lives OUTSIDE the target
> directories — the go tool parses every file inside
> `testdata/fuzz/<FuzzTargetName>/` as a corpus entry.

## FuzzParseComprobanteXML

Target: `FuzzParseComprobanteXML` (internal/core/comprobante_fuzz_test.go) —
drives `ParseComprobanteXML` + `ParseCDRXML` (internal/core/comprobante.go).
Input cap: 1 MiB (spec FR-1 v / design D-10).

| Seed | Bug class / purpose |
| --- | --- |
| `seed_valid_invoice.xml` | Valid production-shaped UBL 2.1 invoice (fictional RUC 20100070970) — must parse with internally consistent metadata. |
| `seed_empty.bin` | Empty input (boundary). |
| `seed_one_byte.bin` | Single byte `<` (boundary). |
| `seed_truncated_invoice.xml` | Truncated valid invoice — must fail closed with typed `INVALID_COMPROBANTE_XML`. |
| `seed_wrong_encoding.bin` | Declared ISO-8859-1 with a raw latin-1 byte — charset mismatch must be a typed error, never a panic. |
| `seed_deep_nesting.xml` | 10,000-deep nested elements — unbounded-depth probe; must stay bounded and typed. |
| `seed_amount_trailing_garbage.xml` | **Previously-failing input:** `PayableAmount` `1284abc` was silently accepted as 128400 cents via `fmt.Sscanf` trailing-data leniency; fixed with `strconv.ParseInt` + strict ISO 4217 suffix guard (regression: `TestParseComprobanteXMLTrailingGarbageAmountRejected`). |

## FuzzCanonicalReceiptPayload

Target: `FuzzCanonicalReceiptPayload` (internal/core/receipt_fuzz_test.go) —
drives `CanonicalReceiptPayload`, `CompleteReceiptBytes`, `ReceiptHash`
(internal/core/receipt.go) across the frozen payload versions
(`receipt-payload/v0.4.0` … `v0.10.0`). Input cap: 1 MiB.

| Seed | Bug class / purpose |
| --- | --- |
| `seed_valid_payload_v04.json` | Valid v0.4.0 `memory_approved` payload with unsorted + duplicated roles (canonicalization must sort/dedupe) and JSON-escaping-sensitive characters. |
| `seed_valid_payload_v09.json` | Valid v0.9.0 `purge_intent` payload with lifecycle hashes + execution-attempt id — exercises the version-conditional canonical extension. |
| `seed_empty.bin` | Empty input (boundary). |
| `seed_one_byte.bin` | Single byte `{` (boundary). |
| `seed_partial_json.bin` | Canonical-shape violation: a partial payload document must canonicalize deterministically and round-trip. |
| `seed_not_json.bin` | Non-JSON input — deterministic rejection, never a panic. |

The round-trip invariant (`canonical bytes → re-parse → re-canonicalize →
identical`) is the no-invalid-success contract (spec FR-1 iii/iv) enforced by
`checkReceiptInvariants`. No crasher has been found in the bounded smoke runs.
No real RUC or customer data is committed.
