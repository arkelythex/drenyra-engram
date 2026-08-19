# Proposal — sdd-060-tenant-cli

> Phase: propose · Artifact: proposal · Status: draft
> Inputs: SDD-060 (Drenyra Engram — DRAFT), repo model (AccountingMemory v2, scope-first store, supersede + receipts), config conventions.

## Problem

SDD-060 defines the tenant-scoped CLI surface: Phase 1 `drenyra-engram tenant list`
(equivalence to `engram projects list`, but scoped by RUC/organization) and
Phase 3 `drenyra-engram tenant consolidate` ("fix de drift de nombres de
cuenta/proveedor dentro de un mismo RUC") with audit trail. Neither exists in
the repository today: the CLI has no `tenant` command, and there is no drift
detection or canonicalization surface.

The SDD's MemoryEntry model (with account/provider names) does not exist in this
repository — the actual model is AccountingMemory v2 with scope-first topic-key
chains. The honest mapping of "account/provider name drift within a RUC" to this
model is **topic-key drift**: chains whose topic keys differ only by case,
whitespace, or punctuation represent the same institutional memory under
multiple keys. `tenant consolidate` detects and (with `--apply`) merges them.

## Proposed capability

1. **`tenant list`** — operator/administrative read (same surface class as
   `doctor`): JSON enumeration of tenants present in the store — organizationId,
   companies (companyId, RUC, identity-known name, memory count), periods, total
   counts. No per-tenant CONTENT is exposed (ids/counts only), and the command is
   a local-store operator tool, not a data-read surface.
2. **`tenant consolidate --ruc <RUC> [--period <YYYYMM>] [--dry-run | --apply]`** —
   within ONE exact tenant scope:
   - canonical **folding** of topic keys (unicode lower-case, whitespace
     collapse, punctuation strip) — a PURE function in `internal/core` with
     Go↔TS golden parity (config `golden_parity`);
   - **drift detection** (default `--dry-run`): groups of chains whose folded
     topic keys collide but raw keys differ, with the deterministic canonical
     candidate per group — ZERO mutation;
   - **`--apply`**: merges each drifted chain into the canonical chain via the
     existing supersede mechanism (`API.Supersede`, `memory_superseded` receipt,
     `transition_log` entry) — one atomic supersede act per drifted chain, each
     with recorded reason; failures reported per-merge with non-zero exit.
   - **audit trail** = the existing receipt + transition-log standard
     (SDD-060 AC-4: same standard as `DRENYRA_AUDIT_LOG`).

## Non-goals (this change)

- No schema change (schema v14 untouched — pure read queries + existing supersede).
- No new store mutation beyond the existing supersede path.
- No MCP/HTTP surface for tenant commands (CLI-only, per SDD-060 Phase 1/3).
- No accent-folding (documented fold limitation — avoids new x/text dependency).
- No auto-merge without explicit `--apply`; dry-run is the default.
- No authorization semantics: consolidate never authorizes anything
  (non-authorization boundary).

## Cross-tenant isolation

Both commands fail closed on scope. `consolidate --ruc A` only ever reads and
merges chains whose scope is RUC A; an adversarial test proves RUC-B chains are
untouched and invisible (SDD-060 AC-5 pattern).

## Delivery

Estimated ~350–450 changed lines (Go + TS + tests + docs). Per config `pr_size`
(split over 400 lines into chained PRs): planned slices PR A (tenant list),
PR B (consolidate detection + dry-run), PR C (consolidate apply + docs).
Delivery strategy `ask-on-risk`, chain strategy `stacked-to-main` (repo default).
Review mode clone-off (user decision) — ordinary-policy commits.

## Risks

- Supersede-based merge semantics: merging a whole chain means superseding each
  drifted chain's CURRENT head into the canonical head; readers follow the
  successor. History is never rewritten (immutability preserved). Medium risk,
  mitigated by per-merge atomicity + dry-run default.
- Fold false-positives (two genuinely distinct topic keys colliding after
  folding): mitigated by the fold being conservative (no accent folding, no
  synonym folding) and dry-run-first review.
