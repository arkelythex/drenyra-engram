# Security Policy

## Scope

This repository covers the Drenyra Engram memory engine: storage, search, relations, lifecycle, provenance, sync, cloud, and the MCP/HTTP/CLI/TUI surfaces. It stores **institutional accounting knowledge** — treat confidentiality, integrity, and provenance as product safety requirements.

## Reporting a vulnerability

Use **GitHub Private Vulnerability Reporting**: open the **Security** tab of this repository → **Report a vulnerability**. Do not open a public issue for security defects.

When reporting, include:

- Affected version/commit and component (`store`, `search`, `sync`, `cloud`, `mcp`, `http`, …)
- A minimal, safe reproduction (no real company data, no RUCs, no credentials)
- Expected vs. actual behavior
- Impact assessment (data exposure? provenance forgery? cross-tenant leakage?)

## Out of scope

- Production credentials, tokens, or customer data — never attach these
- Vulnerabilities in Drenyra, Drenyra AI, or Drenyra Pi (report in their own repos)
- Brute-force or spam abuse of public endpoints

## Handling

Reports are acknowledged within 5 business days. A fix, workaround, or risk acceptance is communicated before public disclosure. Pre-alpha project: fixes land as patch releases on `main` with an advisory note in the release.

## Responsible use

This software is released under the Apache License 2.0 (see [LICENSE](LICENSE)). Please report vulnerabilities privately as described above; do not open public issues for security defects.
