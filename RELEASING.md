# Releasing — Drenyra Engram

> **Last updated:** 2026-08-11.

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents; no float is ever used for money; version/sequence numbers are JSON integers, never floats.
> **Open-source product policy:** Drenyra Engram is released under the **Apache License 2.0**. Source, releases, and container images are public; contributions follow [CONTRIBUTING.md](CONTRIBUTING.md). Drenyra Engram is excluded from the Drenyra private-product policy (Drenyra repo).

## Version policy

- **The Go engine releases via tags + GitHub releases** (e.g. `v0.2.0`,
  static binaries for linux/darwin/windows x amd64/arm64). The Go module is
  go-gettable at the tag. This is the publish path for the engine; the npm
  policy below applies ONLY to the TypeScript reference package.
- Until the first contract is frozen, the TS reference uses **`0.0.1-prealpha.x`** (x increments per release).
- The first release that freezes a contract (`memory`, `scope`, `lifecycle`, `provenance` — per the ROADMAP's Phase 1) is **`0.1.0`**.
- After `0.1.0`, **Semantic Versioning** applies: MAJOR = breaking contract change, MINOR = backward-compatible addition, PATCH = backward-compatible fix. Contract changes are public surface changes.

## Release process (Go engine)

Releases are **automated** (`.github/workflows/release.yml`, goreleaser). On every `v*` tag push:

1. Merge the feature PRs to `main` (CI must be green: Go build/vet/test/race + TS typecheck/test/package).
2. `git tag -a v0.X.Y -m "<notes>" origin/main && git push origin v0.X.Y`.
3. The **Release** workflow builds cross-platform static binaries (`CGO_ENABLED=0`), attaches **SPDX SBOMs** per binary and a **checksums.txt** (SHA-256).
4. `gh release create` is handled by goreleaser (draft=false for tagged releases).
5. Update `ROADMAP.md` and `CHANGELOG.md` with the release entry.

### Audit-trail surface (supply-chain review)

Every release ships, for procurement/audit review:

- **Binaries** — linux/darwin/windows × amd64/arm64, static, `-trimpath`.
- **checksums.txt** — SHA-256 of every artifact; verify with `sha256sum -c`.
- **SBOMs** — `*.spdx.json` per binary (syft); the bill of materials answers "what is in this engine?" for compliance review.
- **SLSA provenance** — **enabled**: GitHub Attestations
  (`actions/attest` v4, Sigstore-signed) generates a provenance attestation
  that binds every artifact in `checksums.txt` (name + SHA-256) to the
  build. Verify any artifact with `gh attestation verify <artifact> -R
  arkelythex/drenyra-engram`. Attestations are available on the free plan
  for public repositories.

These artifacts are **public** GitHub release assets under the Apache-2.0 license — open source, freely downloadable.

## Release checklist

Every release must pass, in order:

1. **Typecheck** — the repo's configured typecheck is clean.
2. **Tests** — full test suite passes.
3. **Conformance vectors** — deterministic behavior (search ranking, lifecycle transitions, relation judgments, scope filtering) passes against canonical vectors from the exact release candidate.
4. **Package build + pack verification** — build and verify the packed artifact contains exactly the intended files.
5. **Packed-install test** — install the packed tarball in a clean consumer and run a smoke command (e.g. `drenyra-engram save` + `search` in an isolated scope) to prove the release works outside the checkout.

## Commit and release discipline

- **Conventional Commits** (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`), with scope when useful.
- **No AI attribution** in commit messages or release notes — no "Generated with" or "Co-Authored-By" AI markers.
- Contract changes are high-materiality: proportional risk review before publish.
- The non-authorization boundary is part of the contract surface and must survive every release unchanged in meaning.
