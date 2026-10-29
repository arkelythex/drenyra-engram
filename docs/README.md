# Documentation

> Find the page you need by who you are, then by what you need from it. For
> what Drenyra Engram is, start at the [project README](../README.md).

Drenyra Engram is a local accounting-memory engine at **alpha** maturity.
Memory informs, evidence demonstrates, policy restricts, and authenticated
professionals authorize. The shipped runtime is the Go binary; the TypeScript
package is a reference mirror for contract parity
([details](COMPATIBILITY.md#typescript-reference-mirror)).

## By audience

| You are | Read in this order |
| --- | --- |
| **An evaluator** deciding whether this fits | [Product](PRODUCT.md) · [Getting started](GETTING-STARTED.md) · [Data, security, and privacy](DATA-SECURITY-PRIVACY.md) · [Compatibility](COMPATIBILITY.md) |
| **An integrator** wiring an agent or program | [Installation](INSTALLATION.md) · [Connecting agents and programs](CONSUMING.md) · [Examples](../examples/README.md) · [Technical reference](../DOCS.md) |
| **An accounting professional or reviewer** | [Approval contract](../contracts/approval.md) · [Verification contract](../contracts/verification.md) · [Trust model](architecture/trust-model.md) · [Reconstructible close demo](demo/reconstructible-close.md) |
| **An operator** running a local deployment | [Data, security, and privacy](DATA-SECURITY-PRIVACY.md) · [Troubleshooting](TROUBLESHOOTING.md) · [Key compromise response](security/key-compromise-response.md) · [Threat model](security/evidence-lifecycle-and-threat-model.md) |
| **A contributor** changing the engine | [Codebase guide](CODEBASE-GUIDE.md) · [Architecture](ARCHITECTURE.md) · [Contributing](../CONTRIBUTING.md) · [Contracts](../contracts/README.md) |

## By content type

### Tutorial — learn by doing

- [Getting started](GETTING-STARTED.md) — an isolated local workflow with synthetic data and cleanup.
- [Examples](../examples/README.md) — runnable CLI, verification, MCP, HTTP, and Docker scripts.

### How-to — solve a specific problem

- [Installation](INSTALLATION.md) — source, release-binary, and container paths.
- [Connecting agents and programs](CONSUMING.md) — MCP clients, HTTP, and CLI.
- [Troubleshooting](TROUBLESHOOTING.md) — messages mapped to recovery steps.
- [Key compromise response](security/key-compromise-response.md) — what to do if a signing key is exposed.
- [Releasing](../RELEASING.md) — release process for maintainers.

### Reference — look something up

- [Technical reference](../DOCS.md) — environment variables, CLI, MCP tools, HTTP routes, scope, verification.
- [Compatibility](COMPATIBILITY.md) — platforms, versions, schema, MCP protocol, stability levels.
- [Contracts](../contracts/README.md) — versioned, transport-independent contract set.
- [Changelog](../CHANGELOG.md) — published history.

### Explanation — understand why

- [Product](PRODUCT.md) — problem, audiences, boundaries, and hypotheses.
- [Architecture](ARCHITECTURE.md) — engine design, trust model, storage layout.
- [System boundaries](SYSTEM-BOUNDARIES.md) — the non-authorization boundary and layer model.
- [Codebase guide](CODEBASE-GUIDE.md) — where each invariant lives.
- [Data, security, and privacy](DATA-SECURITY-PRIVACY.md) — what is stored, protected, and not guaranteed.
- [Roadmap](../ROADMAP.md) — future outcomes and readiness gates.

## Design records

| Area | Documents |
| --- | --- |
| Decisions (ADRs) | [ADR-001 engine strategy](decisions/ADR-001-engine-strategy.md) · [ADR-002 storage authority](decisions/ADR-002-storage-authority.md) · [ADR-003 approval principal](decisions/ADR-003-approval-principal.md) · [ADR-003 multi-jurisdiction](decisions/ADR-003-multijurisdiction.md) · [ADR-004 license](decisions/ADR-004-open-source-license.md) · [ADR-005 signing-key custody](decisions/ADR-005-signing-key-custody-hsm-kms.md) |
| Fiscal continuity | [Requirements](../specs/fiscal-runtime-foundations/spec.md) · [ODD backlog](../odd/tasks/fiscal-runtime-foundations.md) · [Historical design](decisions/fiscal-runtime-foundations-design.md) · [Historical proposal](decisions/fiscal-runtime-foundations-proposal.md) · [Historical task record](decisions/fiscal-runtime-foundations-task-history.md) · [Verification history](decisions/fiscal-runtime-foundations-verification-history.md) — preserved records, not an alternative workflow or completion claim |
| Milestone architecture | [`architecture/`](architecture/): [trust model](architecture/trust-model.md) · [approval principal](architecture/approval-principal-step1.md) · [conflict judgment](architecture/conflict-judgment-step2.md) · [receipts](architecture/ed25519-receipts-step3.md) · [offline verification](architecture/offline-verification-step4.md) · [close intelligence](architecture/close-intelligence-v0.5.md) · [fiscal policy memory](architecture/fiscal-policy-memory-v0.6.md) · [evidence objects](architecture/evidence-object-v0.7.md) · [evidence lifecycle](architecture/evidence-lifecycle-v0.8.md) · [review workspace](architecture/review-workspace-v0.9.md) · [OIDC identity](architecture/oidc-access-token-identity.md) · [dependency direction](architecture/dependency-direction.md) · [ecosystem boundaries](architecture/ecosystem-boundaries.md) · [operability evidence](architecture/operability-evidence.md) |
| Security | [Evidence lifecycle and threat model](security/evidence-lifecycle-and-threat-model.md) · [Key compromise response](security/key-compromise-response.md) · [Security policy](../SECURITY.md) |
| Product and audit | [Gate 0 decisions](product/gate-0-decisions.md) · [Initial market and v1 gate](product/initial-market-and-v1-gate.md) · [Product and architecture audit](due-diligence/2026-08-product-architecture-audit.md) |
| Ecosystem | [Capabilities and conformance](ecosystem/capabilities-and-conformance.md) · [Dominion integration](ecosystem/dominion-integration.md) |
| Evidence | [Search baseline benchmark](benchmark/search-baseline-v0.1.md) · [Reconstructible close demo](demo/reconstructible-close.md) |

## Naming note

`ARCHITECTURE.md` describes the engine design. The boundary document that was
previously `architecture.md` is now [`SYSTEM-BOUNDARIES.md`](SYSTEM-BOUNDARIES.md),
so the two names no longer collide on case-insensitive file systems.
