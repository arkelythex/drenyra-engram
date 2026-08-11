# Fuzz seed corpora — internal/search

Seed policy (spec FR-2 / design D-10): corpus entries live at
`testdata/fuzz/FuzzSearchTokenize/` in the go fuzz corpus format (a
`go test fuzz v1` header line plus one `[]byte("…")` value line per entry,
exactly as the go tool reads and writes them). Every entry is a permanent
regression (AC-2): it replays green under `go test ./...` and its deletion
fails `TestFuzzSearchCorpusNeverDeleted`.

> **Layout note:** this README deliberately lives OUTSIDE the target
> directory — the go tool parses every file inside
> `testdata/fuzz/FuzzSearchTokenize/` as a corpus entry.

## FuzzSearchTokenize

Target: `FuzzSearchTokenize` (internal/search/search_fuzz_test.go) — drives
`tokenize` (internal/search/search.go). Input cap: 1 MiB (spec FR-1 v /
design D-10).

| Seed | Bug class / purpose |
| --- | --- |
| `seed_empty.bin` | Empty input (boundary) — yields zero tokens. |
| `seed_one_byte.bin` | Single byte `a` (boundary). |
| `seed_spanish_text.txt` | Production-shaped Spanish text (accents, `ñ`, punctuation, digits) — must tokenize deterministically, non-empty, separator-free. |
| `seed_emoji_and_punct.txt` | Emoji + mixed punctuation (`—`, `#`, `·`, `«»`, `<`, `&`, `%`) — emoji/symbols are separators, never tokens or panics. |
| `seed_nul_and_control.bin` | NUL and C0 control bytes — separators, deterministic, no panic. |
| `seed_invalid_utf8.bin` | Invalid UTF-8 sequences — decoded as replacement characters, treated as separators, never tokens or panics. |
| `seed_cap_max.bin` | **Documented boundary seed:** exactly 1 MiB (the cap) — processed fully; decoded size pinned by `TestFuzzCorpusSecuritySweep`. |
| `seed_cap_just_below.bin` | **Documented boundary seed:** exactly 1 MiB − 1 byte (just below the cap); decoded size pinned by `TestFuzzCorpusSecuritySweep`. |

Named tokenizer regressions live in `TestSearchTokenizeKnownBehavior`. No
crasher has been found in the bounded smoke runs.
