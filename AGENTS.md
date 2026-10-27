# Drenyra Engram — Agent Guide

This file is for AI agents and their humans working in this repository. It answers: *what are the non-negotiable rules, how is work delivered, and how is it verified?*

> [!IMPORTANT]
> **Drenyra Engram never authorizes operations.** No code path may treat an observation as approval. The approval gate is professional review of a memory, never authorization of a declaration.

## Non-Negotiable Rules

These rules were carried over verbatim from the former `openspec/config.yaml` conventions when the SDD/OpenSpec workflow was retired.

- **Commits:** conventional commits only (`feat:`, `fix:`, `docs:`, `test:`, `bench:`, `build:`, `refactor:`). Never add AI attribution (no `Co-Authored-By`).
- **Money:** monetary values are WHOLE int64 cents (Go) / BigInt cents (TS). Never floats. Sequence/revision counters are JSON integers, never floats.
- **Scope first:** scope (Company/RUC/period) is structural, not a post-filter. Every read or mutation takes the exact scope and FAILS CLOSED on mismatch. Cross-tenant invisibility is a tested invariant.
- **Golden parity:** Go↔TS byte-identical golden vectors live in `testdata/golden` and are enforced by `TestGoldenVectorsGo`; new domain logic must join the parity mechanism.
- **Non-authorization boundary:** Drenyra Engram NEVER authorizes operations. No code path may treat an observation as approval.
- **Contracts:** `contracts/` is a frozen public surface. Contract changes are high-materiality: bump the version, document the migration path, and get explicit approval.
- **Provenance:** every observation records who/what/when/why; forgeable provenance is a defect.
- **Secrets:** no credentials, tokens, or customer data in code, docs, tests, or seeds.
- **TS types:** no `any`; use precise types, `unknown`, or justified generics.
- **Docs as code:** update docs in the same PR as code; stale docs are a bug.
- **PR size:** prefer small verifiable changes; split changes over 400 lines into chained PRs.
- **The review gate rejects:** silent error handling, production `console.log`, missing scope checks, missing provenance, missing tests, and contract changes without docs.

## Delivery Workflow — ODD

Organic Driven Development is the only delivery workflow. It replaced SDD/OpenSpec (see [`odd/tasks/sdd-to-odd-migration.md`](odd/tasks/sdd-to-odd-migration.md)).

1. **Authorize.** A request to investigate, explain, review, or plan is read-only. Only explicit change intent authorizes writes.
2. **Explore** the existing code and the relevant [`specs/`](specs) requirement before proposing anything.
3. **Size the task.** Small work (understood, contained risk, resumable from the diff) runs inline with no durable artifacts. Large work creates `odd/tasks/<feature>.md` before the first source write.
4. **Track.** A feature document has a header, then `## Specs` with numbered `S#` quoting the request verbatim, then `## Tasks` (ID, linked `S#`, status, commit), then `## Log`, where `L1` is the original request verbatim.
5. **Implement task by task, strict TDD.** Write the failing test (RED), implement (GREEN), refactor. Check a task off only after its outcome and checks were observed.
6. **Close** with the verified outcome, every failed or skipped check, and the next step.

## Verification

Recommended order for a change touching Go and TS:

```sh
npm run typecheck
go vet ./...
gofmt -l .
go test ./...
npm test
```

Also available: `go test ./internal/core -run TestGoldenVectorsGo` (golden parity), `go test ./internal/search/bench -v` (search bench), and `npm run verify:package` (full TS verification).

## Where Things Live

| Path | Contents |
| --- | --- |
| `cmd/drenyra-engram` | CLI entrypoint (save, review, rule, object, verify, mcp, serve, …) |
| `internal/core` | Domain model and pure logic (no I/O) |
| `internal/auth`, `internal/authz` | Verified principal and sessions; versioned policies |
| `internal/sync` | Additive local-store reconciliation |
| `core/`, `store/`, `authz/`, `lifecycle/` | TypeScript reference and parity mirrors |
| `contracts/` | Frozen contract set — public surface |
| `testdata/golden` | Go↔TS golden vectors |
| `specs/` | Requirements records migrated from the retired SDD changes |
| `odd/tasks/` | ODD feature documents |
| `docs/` | Architecture, decisions, product, demo, benchmark, security |
