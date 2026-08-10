# ADR-004 — Open-source licensing under Apache License 2.0

> Status: **accepted**
> Date: 2026-08-10

## Context

Drenyra Engram was created as a private, proprietary product: the repository
was private, the LICENSE declared proprietary and confidential rights, the
README documented "distribution is contractual, never public", and releases
and container images stayed private (GHCR authenticated pulls).

The repository was made public on 2026-08-09, creating a documented
contradiction: a public repository whose README, SECURITY, RELEASING,
workflows, and threat model still claimed private/contractual distribution.
An interim source-available posture (public code, proprietary license,
contractual distribution) was frozen, then explicitly superseded by an
open-source decision: the owner confirmed the repository will be released
under the **Apache License 2.0**.

The due-diligence audit (docs/due-diligence/2026-08-product-architecture-audit.md,
block AJ) had already classified the license/product-policy posture as
FAIL/RISK and required reconciliation as v1.0-gate evidence.

## Decision

1. Drenyra Engram is released under the **Apache License 2.0** (LICENSE now
   contains the canonical Apache-2.0 text).
2. The README license badge and license section, SECURITY.md responsible-use
   section, RELEASING.md product policy and artifact statements, release
   workflow comments, goreleaser comments, and the threat-model asset/supply
   chain rows are aligned to the open-source posture.
3. `package.json` declares `"license": "Apache-2.0"`.
4. Drenyra Engram is excluded from the Drenyra private-product policy; the
   physical edit to the Drenyra repo's `docs/products/private-product-policy.md`
   is tracked there (out of scope for this repository).
5. A full-history secret scan (gitleaks, 115 commits) was run before
   distribution: all 12 findings are `generic-api-key` false positives on test
   fixtures (request IDs, topic keys, documented fixture token hash, Go↔TS
   Ed25519 parity vectors); no real secrets were found.

## Consequences

- **Moat shift:** code no longer differentiates; differentiation must come
  from workflow, integrations, professional UX (Review Workspace), and
  operational/brand assets.
- **Supply-chain exposure rises:** public source + go-gettable module make
  signed releases and SBOMs more important (audit block AJ remains PARTIAL
  until signed releases, SBOM, dependency audit, and contribution review land).
- **Legal:** Apache-2.0 permits commercial use and includes an express patent
  license; legal review before commercial deployment remains appropriate.
- **Irreversible in effect:** once published, the license change cannot be
  revoked for already-distributed copies; it supersedes the interim
  source-available posture (2026-08-09).
- **SLSA attestations** (`actions/attest-build-provenance`) are unblocked on
  the free plan for public repositories; enabling them is pending a
  release-pipeline review.

## References

- [LICENSE](../../LICENSE)
- [README.md](../../README.md) (IMPORTANT block, License section)
- [RELEASING.md](../../RELEASING.md)
- [SECURITY.md](../../SECURITY.md)
- docs/security/evidence-lifecycle-and-threat-model.md
- docs/due-diligence/2026-08-product-architecture-audit.md (block AJ)
- docs/product/initial-market-and-v1-gate.md (gate G-11)
