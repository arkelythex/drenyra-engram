# Drenyra Engram — Roadmap

> **Last reviewed:** 2026-10-28. **Maturity: alpha.** This document looks forward:
> open outcomes, explicit deferrals, and the v1.0 readiness gates. Delivered
> history lives in [CHANGELOG.md](CHANGELOG.md); published release facts live
> there and in [RELEASING.md](RELEASING.md). Nothing here is a production-readiness
> claim.

## Release status

| Item | Status | Evidence |
| --- | --- | --- |
| Latest Go engine tag | `v0.9.0` (2026-10-27), schema v17 | `git tag -l`; [CHANGELOG `v0.9.0`](CHANGELOG.md#v090--2026-10-27-github-release) |
| Earlier Go engine tags | `v0.2.0`, `v0.2.1` | `git tag -l` |
| Milestones v0.3.0 – v0.8.0 | Delivered, **never tagged on their own**; they shipped inside `v0.9.0` | [CHANGELOG `v0.9.0`](CHANGELOG.md#v090--2026-10-27-github-release) |
| Fiscal runtime foundations (schema v18) | Implemented, **unreleased** | [CHANGELOG `[Unreleased]`](CHANGELOG.md#unreleased) |
| TypeScript reference package | `0.0.1-prealpha.x` track, independent of engine tags | [RELEASING.md](RELEASING.md#version-policy) |

> **Version nomenclature (frozen):** `v0.3.0` is the Accounting Memory Kernel.
> The original vision's "V0.2 Evidence and Judgment" is NOT closed by it —
> adjudicated contradictions, cryptographic receipts and offline verification
> are `v0.4.0`. The numbering is therefore: `v0.4.0` Evidence, Conflict and
> Judgment · `v0.5.0` Close Intelligence · `v0.6.0` Fiscal Policy Memory ·
> `v1.0.0` Institutional Accounting Brain.

## Program alignment

Drenyra Engram participates in the [Drenyra Dominion Program](https://github.com/arkelythex/drenyra-ai/tree/main/program/drenyra-dominion), the federated master that fixes vision, authority, contracts, dependencies, gates, and sequencing across the ecosystem. This roadmap is the repository's own implementation sequence; the program's vertical SDDs frame the ecosystem's coordinated release:

| Program SDD | Wave | Alignment with this roadmap |
| --- | --- | --- |
| [SDD-080 — Engram Institutional Memory](https://github.com/arkelythex/drenyra-ai/tree/main/program/drenyra-dominion/sdds/sdd-080-engram) | 2 — Fiscal intelligence | Institutional memory that informs but never authorizes — the delivered milestones below cover its slices (memory, receipts, offline verification, close intelligence, fiscal policy memory, evidence objects, review workspace) |
| [SDD-050 — Peruvian Monthly Close](https://github.com/arkelythex/drenyra-ai/tree/main/program/drenyra-dominion/sdds/sdd-050-monthly-close) | 3 — Flagship product | Consumes Engram's prior-decision context for the monthly close journey — memory informs, never authorizes, and is never evidence |

## Delivered so far

Each row links to the changelog entry that holds the facts. Detail was moved
out of this roadmap without loss; see
[CHANGELOG — Delivery references](CHANGELOG.md#delivery-references-and-readiness-evidence-moved-from-roadmapmd).

| Milestone | Outcome | Changelog |
| --- | --- | --- |
| Go engine (ADR-001), `v0.2.0` | Standalone Go engine: SQLite store, scope-first search, CLI, MCP and HTTP surfaces, local sync, chain history; first tagged release | [v0.2.0](CHANGELOG.md#v020--2026-08-03-github-release) |
| `v0.3.0` Accounting Memory Kernel | AccountingMemory v2 model, mandatory human approval gate, schema v2, 17 relations, accounting MCP tools | [v0.3.0](CHANGELOG.md#v030--accounting-memory-kernel-not-tagged-shipped-in-v090) |
| `v0.4.0` Evidence, Conflict and Judgment | Authenticated approval principal, adjudicable judgments, Ed25519 receipts, offline verification; 12 acceptance criteria closed | [v0.4.0](CHANGELOG.md#v040--evidence-conflict-and-judgment-not-tagged-shipped-in-v090) |
| `v0.5.0` Close Intelligence | Monthly close with an enforced period gate, reconciliations, period comparison, automatic agent context | [v0.5.0](CHANGELOG.md#v050--close-intelligence-not-tagged-shipped-in-v090) |
| `v0.6.0` Fiscal Policy Memory | Versioned rules with temporal vigencia, jurisdiction dimension, structured rule links, regulatory-change impact | [v0.9.0 entry](CHANGELOG.md#v090--2026-10-27-github-release) |
| `v0.7.0` Evidence Objects | Local-first content-addressed WORM object store, schema v8, `object_stored` receipts, object-level verification | [v0.9.0 entry](CHANGELOG.md#v090--2026-10-27-github-release) |
| `v0.8.0` Evidence Lifecycle | Retention policies, legal holds, approved purge pipeline, deterministic lifecycle export (schema v12) | [v0.9.0 entry](CHANGELOG.md#v090--2026-10-27-github-release) |
| `v0.9.0` Review Workspace | Engine-side review queue, review detail, authenticated reject/return, anti-rubber-stamp gates (schema v13); tagged 2026-10-27 | [v0.9.0](CHANGELOG.md#v090--2026-10-27-github-release) |
| Readiness evidence slices (2026-08) | Search baseline benchmark, comprobante adapter contract, reconstructible-close fixture, fuzz/restore/corruption drills, key-compromise response, reconstructibility surface | [v0.9.0 entry](CHANGELOG.md#v090--2026-10-27-github-release) |
| Identity, tenancy, and encryption slices | Stateless OIDC access tokens, identity→scope binding, `tenant list`/`consolidate`, opt-in per-tenant content encryption, `encrypt`, required `confidence` (schema v17) | [v0.9.0 entry](CHANGELOG.md#v090--2026-10-27-github-release) |
| Fiscal runtime foundations (schema v18) | Fiscal scope binding v1, immutable act-evidence linkage, `DRENYRA_FISCAL_RUNTIME_MODE`; **unreleased** | [Unreleased](CHANGELOG.md#unreleased) |

## Next outcomes

Open work, each tied to the gate it advances. None of it is implemented unless
the [changelog](CHANGELOG.md) says so.

### Release and rollout

- [ ] Tag a release that includes the fiscal runtime foundations (schema v18).
      Until then `v0.9.0` is the latest installable engine. The runtime mode
      defaults to `shadow`, which fails every company-scoped protected write
      closed; the rollout needs an explicit operator decision per deployment
      (see [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md#runtime-mode)).
- [ ] Materiality-aware period views (open since v0.4.0).

### Production identity (advances G-4)

First slice delivered (stateless OIDC, `v0.9.0`). Design:
[docs/architecture/oidc-access-token-identity.md](docs/architecture/oidc-access-token-identity.md).

- [ ] MFA/ACR assurance elevation (today: standard assurance only, `acr` and
      `amr` ignored).
- [ ] Token revocation beyond DB membership state.
- [ ] Proactive TTL-based JWKS refresh.
- [ ] Signed service assertions.
- [ ] User provisioning.
- [ ] HSM/KMS key management ([ADR-005](docs/decisions/ADR-005-signing-key-custody-hsm-kms.md)).
- [ ] Dual approval, production-backed.

### Evidence and operations (advances G-5, G-6, G-7)

- [ ] Production object-store operations: backup/restore drills beyond the
      repository-local ones, encryption-at-rest/TDE, and recovery objectives.
      The opt-in per-tenant encryption shipped so far covers observation
      content narrative only.
- [ ] HSM/KMS custody and a signing-key compromise-response drill (the
      playbook and boundary matrix exist; the drill is parent-owned).
- [ ] Customer-observed target for the reconstructibility North Star (the
      engineering baseline and instrumentation exist; G-10).

### Integrations (advances G-3)

- [ ] Production comprobante ingestion. Gate 0 decision G0-5: CDR/XML
      comprobante ingestion first, SIRE second, ERP deferred
      ([gate-0-decisions](docs/product/gate-0-decisions.md)). Only the adapter
      contract (`core.ParseComprobanteXML`, `core.ParseCDRXML`,
      `server.IngestComprobante`, CLI `object ingest`) exists. There are no
      SUNAT credentials, no retries, no outage or response-retention handling,
      and no source authority. Parsing is not integration.
- [ ] Any ERP integration (deferred).

### Search

- Decision recorded: FTS5/BM25 is **not adopted**; the baseline meets every
  target ([benchmark](docs/benchmark/search-baseline-v0.1.md)).
- [ ] Typo tolerance (0.58 in the baseline) would need a trigram tokenizer;
      tracked only if it becomes a product requirement.

### Original extraction plan

The first plan (TypeScript package extraction, phases 0–2) was superseded by
[ADR-001](docs/decisions/ADR-001-engine-strategy.md). What remains open from it:

- [ ] Contract freeze status is tracked per contract in
      [contracts/README.md](contracts/README.md); the original blanket `v0.1`
      freeze of `memory`, `scope`, `lifecycle`, `provenance` and its
      conformance suite are not tracked as one milestone any more.
- [ ] Drenyra and Drenyra Pi consume a released engine instead of internal
      implementations. The Drenyra adapter was live-tested against `v0.2.0`
      ([CHANGELOG](CHANGELOG.md#v020--2026-08-03-github-release)); adoption by
      other consumers is not verified from this repository.

### SDD-060 tenant surface

- [ ] Phase 5, LATAM namespacing: deferred by the SDD itself.

## v1.0 readiness gates

**No release is labelled v1.0 with an open gate.** The gate is a release rule,
not a target date. The authoritative table, with evidence and owners, is
[D-6 in docs/product/initial-market-and-v1-gate.md](docs/product/initial-market-and-v1-gate.md#d-6--v1-gate);
the statuses below mirror it and must be updated with it.

| Gate | Criterion | Status |
| --- | --- | --- |
| G-1 | One paying customer profile and one workflow validated | NOT MET |
| G-2 | One real or legally approved anonymized close is reproducible end-to-end | PARTIAL — fictional fixture delivered; a real or anonymized close is still required |
| G-3 | Ledger boundary and first ERP/SUNAT integration are explicit | PARTIAL — ledger boundary frozen; adapter contract defined; production integration not implemented |
| G-4 | Human identity, MFA/assurance, membership, SoD, dual approval production-backed | PARTIAL — first OIDC slice only |
| G-5 | Evidence objects have hash, retention, availability, legal-hold, export semantics | PARTIAL — cloud/remote storage, scheduler executor, OCR/content search deferred |
| G-6 | Cross-tenant, race, fuzz, restore, and corruption drills pass | PARTIAL |
| G-7 | Signing-key rotation/revocation and compromise response are tested | PARTIAL — HSM/KMS and the compromise-response drill remain open |
| G-8 | Threat model and privacy/data-provider policy are reviewed | NOT MET — external review pending |
| G-9 | Verification UI says cryptographic integrity is not accounting correctness | PARTIAL — CLI reports do; no product UI in this repository |
| G-10 | North Star metric has a baseline and a customer-observed target | NOT MET |
| G-11 | Product/license/non-goals documentation is internally consistent | PARTIAL |

## `v1.0.0` — Institutional Accounting Brain

Not started. Candidate outcomes, none implemented:

- [ ] Accounting firms: shared memory with controlled sync
- [ ] Authorized anonymized cross-company learning
- [ ] Full knowledge graph + pattern detection
- [ ] Specialized agents + complete audit

## Explicitly deferred or out of scope

Kept visible on purpose. None of these is implemented.

- **Remote and cloud evidence storage** — cloud/remote object storage for
  evidence objects.
- **Cloud offering** — deferred to `arkelythex/drenyra-cloud`.
- **Professional Web UI** — headless and independently deployable; the
  professional Web UI belongs to Drenyra and is not part of this repository.
- **Production readiness** — the project is alpha; no production-readiness,
  certification, or compliance claim is made.
- **SUNAT and ERP integration** — see Integrations above.
- **Scheduler executor for lifecycle expiry**, **OCR or content search over
  objects**, **SUNAT/ERP object ingestion**.
- **Authorization engine** — that is `arkelythex/drenyra-ai` gates plus human
  approval.
- **PostgreSQL in this repository** — local-first; PostgreSQL is the Drenyra
  ecosystem's authoritative store ([ADR-002](docs/decisions/ADR-002-storage-authority.md)).

## Open Gate 0 decisions

From [gate-0-decisions](docs/product/gate-0-decisions.md); no item here may be
represented as implemented.

- **G0-3:** retention periods per document class (needs legal/tax counsel
  sign-off). Jurisdiction scope is resolved: Perú only for v1.
- **G0-4:** MFA minimum for `closing`/`sunat_filing` with the chosen provider;
  membership-source mapping to the engine's tenant/company/role model. The
  identity provider is resolved: self-hosted Keycloak (managed IdP remains a
  per-deployment option).
- **G0-6:** named owner and review date per gate item G-1…G-11.
- **D-1/D-2/D-3/D-5:** product hypotheses stay PROVISIONAL until customer
  evidence lands.
