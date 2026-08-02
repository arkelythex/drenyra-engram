# Changelog

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents; no float is ever used for money; version/sequence numbers are JSON integers, never floats.

All notable changes to Drenyra Engram will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to the version policy in [RELEASING.md](RELEASING.md).

## 0.0.1-prealpha.1 — 2026-08-01

### Added

- Repository identity scaffolding: README, LICENSE, SECURITY, CONTRIBUTING, CODEOWNERS, architecture and roadmap docs.
- Draft contract index (`contracts/`): `memory`, `scope`, `lifecycle`, `provenance`.
- **Memory core** — the observation model defined by the contracts: structured observations with type, scope (company/RUC/period), topic keys, lifecycle states (draft → reviewed → promoted → superseded), vigencia, and provenance.
- **Non-authorization boundary** — documented and carried in the provenance contract: memory guides, it never authorizes.

### Notes

- Pre-alpha: contracts are not frozen; search, store, relations, and surfaces (MCP/CLI/HTTP/TUI) are planned in the ROADMAP.
- Version policy: `0.0.1-prealpha.x` until the first frozen contract, then `0.1.0`.
