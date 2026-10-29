# Releasing — Drenyra Engram

> **Last updated:** 2026-10-28.

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents; no float is ever used for money; version/sequence numbers are JSON integers, never floats.
> **Open-source product policy:** Drenyra Engram is released under the **Apache License 2.0**. Source, releases, and container images are public; contributions follow [CONTRIBUTING.md](CONTRIBUTING.md). Drenyra Engram is excluded from the Drenyra private-product policy (Drenyra repo).

## Version policy

- **The Go engine releases via tags + GitHub releases** (e.g. `v0.2.0`,
  static binaries for linux/darwin/windows x amd64/arm64). The Go module is
  go-gettable at the tag. This is the publish path for the engine; the npm
  policy below applies ONLY to the TypeScript reference package.
- **Where status lives.** Whether a version is released is decided by its git tag (`git tag -l`; today `v0.2.0`, `v0.2.1`, `v0.9.0`) and the GitHub release it produces. [CHANGELOG.md](CHANGELOG.md) records what each tag contains and keeps unreleased work under `## [Unreleased]`; [ROADMAP.md](ROADMAP.md) lists future outcomes and the v1.0 gates; [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md) is the support matrix (platforms, toolchains, schema, MCP, stability levels). A milestone heading in the changelog without a tag was never released on its own.
- Until the first contract is frozen, the TS reference uses **`0.0.1-prealpha.x`** (x increments per release).
- The first release that freezes a contract (`memory`, `scope`, `lifecycle`, `provenance` — per the ROADMAP's Phase 1) is **`0.1.0`**.
- After `0.1.0`, **Semantic Versioning** applies: MAJOR = breaking contract change, MINOR = backward-compatible addition, PATCH = backward-compatible fix. Contract changes are public surface changes.

## Release process (Go engine)

Releases are **automated** (`.github/workflows/release.yml`, goreleaser). On every `v*` tag push:

1. Merge the feature PRs to `main` (CI must be green: Go build/vet/test/race + TS typecheck/test/package). Every user-visible change already has a line under `## [Unreleased]` in `CHANGELOG.md`.
2. **Prepare the release in a PR before tagging:** in `CHANGELOG.md`, rename the populated `## [Unreleased]` content to `## [vX.Y.Z] — YYYY-MM-DD (GitHub release)`, add a fresh empty `## [Unreleased]`, and update the compare links at the bottom; update the "Release status" table in `ROADMAP.md`; update `docs/COMPATIBILITY.md` if the schema version, supported platforms, MCP tool count, or stability levels changed. Confirm the engine version string reported in MCP `serverInfo` (`engineVersion` in `internal/server/mcp.go`) and the `-X` ldflag in `.goreleaser.yml` agree with the tag.
3. `git tag -a v0.X.Y -m "<notes>" origin/main && git push origin v0.X.Y`.
4. The **Release** workflow builds cross-platform static binaries (`CGO_ENABLED=0`), attaches **SPDX SBOMs** per binary and a **checksums.txt** (SHA-256).
5. `gh release create` is handled by goreleaser (draft=false for tagged releases; `prerelease: auto`).
6. After the release exists, confirm `git tag -l` shows the tag and that `CHANGELOG.md`, `ROADMAP.md`, and this file agree on the latest version. Do not write a release date before the tag exists.

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
2. **Tests** — full test suite passes (`go vet ./...`, `go test ./... -count=1`, and `bun run test`; see [CONTRIBUTING.md](CONTRIBUTING.md)).
3. **Conformance vectors** — deterministic behavior (search ranking, lifecycle transitions, relation judgments, scope filtering) passes against canonical vectors from the exact release candidate, including Go↔TypeScript parity (`TestGoldenVectorsGo` and `core/__tests__/golden.test.ts` over `testdata/golden/`).
4. **Package build + pack verification** — build and verify the packed artifact contains exactly the intended files (`bun run verify:package`). This applies to the TypeScript reference package.
5. **Packed-install test** — install the packed tarball in a clean consumer and prove the library entry resolves outside the checkout (`node scripts/verify-packed-install.mjs`). This applies to the TypeScript reference package.
6. **Documentation examples** — build the binary and run `examples/run-all.sh` (see [CONTRIBUTING.md](CONTRIBUTING.md#documentation-checks)).

## Commit and release discipline

- **Conventional Commits** (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`), with scope when useful.
- **No AI attribution** in commit messages or release notes — no "Generated with" or "Co-Authored-By" AI markers.
- Contract changes are high-materiality: proportional risk review before publish.
- The non-authorization boundary is part of the contract surface and must survive every release unchanged in meaning.
