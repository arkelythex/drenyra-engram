# Review Workspace v0.9 — Engine-Side Vertical Slice (Design)

> **Status:** design (approved 2026-08-10 by owner per design brief §6)
> **Version:** v0.9 (next contract surface after evidence-lifecycle v0.8)
> **Companion:** design brief §6 (Review Workspace); contracts: lifecycle.md,
> receipts.md, approval.md, scope.md; parity: testdata/golden/*.json.

## 1. Scope

Engine-side slice of the Review Workspace. Drenyra Engram is headless and
independently deployable; the professional Web UI belongs to Drenyra and is
OUT OF SCOPE here (design brief §6.1). This slice delivers the queue, the
detail assembly, and the decision operations over the existing `pending_review`
state — all usable via MCP, HTTP, and CLI.

**Works without Phase 6 (Fiscal Policy Memory):** rule references and vigencia
are surfaced when present, never required. Acceptance criterion §6.5.1.

**Non-goals (this slice):** Web UI, bulk/batch approval, FTS search, Phase 6
rules content, velocity dashboard (only observable events are emitted).

## 2. Contract surface changes (explicit, high-materiality)

| Change | Detail |
|---|---|
| Lifecycle status +1 | `returned` (non-terminal): `pending_review → returned` (human decision); agent Save on a returned memory creates a NEW revision that re-enters `pending_review`. Terminal statuses unchanged (`rejected`, `superseded`, `voided` never reopen). |
| Receipt action +1 | `memory_returned` (signed, immutably recorded). |
| Receipt payload version | `memory_rejected` payload extended with `reason` + reviewed envelope hash H1 (mirrors approve signing H1+H2); new payload version. |
| Approval policy | SoD clause: approver ≠ proposer of the pending revision (fail-closed, `SOD_VIOLATION`). Review-checks clause: high-risk approvals (materialityLevel `material`/`critical`) require `reviewChecks.evidenceInspected` and `reviewChecks.ruleInspected` true (`REVIEW_CHECKS_REQUIRED`). |
| Scope contract | Unchanged semantics; queue is scope-first (exact scope, server-side). |

These extend frozen contracts additively (no existing behavior removed).
RELEASING.md: contract changes are high-materiality → proportional risk review
before publish.

## 3. Queue — list pending review

`ListReviewQueue(scope, opts)` → items with: `memoryId`, `kind`,
`fiscalEffect`, `materialityLevel`, `materialityCents`, `status`,
`envelopeHash` (current, the one the reviewer must sign against), `recordedBy`
(proposer), `recordedAt`, `evidenceRefCount`, `ruleRefCount`,
`openJudgmentCount` (proposed judgments touching this memory).

- Scope-first: exact scope (organizationId/companyId/ruc/period) enforced
  server-side on every list (design brief §6.5.2).
- Deterministic ordering: `materialityLevel` rank DESC → `recordedAt` ASC
  (oldest first within the same risk class) → `rowid` ASC.
- Pagination: `limit`/`offset` (bounded; default 50, max 200).
- Status filter: `pending_review` only (closed set), no status injection.
- No Phase 6 dependency; rule refs counted when present.

## 4. Review detail — assembly

`ReviewDetail(memoryId, scope)` composes, scope-guarded:

1. **Current pending revision** (latest chain head) with full envelope fields.
2. **Proposed revision vs prior**: structured content diff over the immutable
   chain (previous revision → head); identity/content fields only — status,
   timestamps, and recorded-by are provenance, not content.
3. **Evidence state**: evidence refs + object availability (WORM object
   present / absent / not-a-ref) via the existing object store — no Phase 6.
4. **Rule state**: rule refs + vigencia window when present (best effort;
   fiscal-policy-memory is Phase 6, not required).
5. **Open judgments**: proposed judgments with `fromId` or `toId` = this
   memory (new store query).
6. **Review metadata**: envelope hash to sign (H1), recordedBy, recordedAt,
   observedAt, fiscalEffect, materiality, prior approved revision (when the
   chain has one) for before/after context.
7. **Boundary notice**: "signature integrity is not accounting correctness"
   echoed in every detail payload (gate G-9 wording).

## 5. Decision operations

All three are authenticated (ApprovalPrincipal), idempotent by
(tenant, requestId), hash-guarded (fresh H1 recompute vs
`expectedEnvelopeHash`), atomic in one transaction, and emit an immutable
event + signed receipt.

| Op | Status transition | Reason | SoD | New bits |
|---|---|---|---|---|
| `approve` | `pending_review → approved` | existing (REASON_REQUIRED per risk policy) | approver ≠ proposer | `reviewChecks` required for material/critical; `memory_closed` extra receipt for closing memories (existing) |
| `reject` | `pending_review → rejected` (terminal) | reason REQUIRED when materiality ≥ material OR fiscalEffect ∈ {closing, declaration, sunat_filing}; optional otherwise | approver ≠ proposer | authenticated + idempotent + hash-guarded + reason in receipt (replaces legacy actor-only path) |
| `return` | `pending_review → returned` (non-terminal) | reason REQUIRED (this is a correction request; the reason tells the agent what to fix) | approver ≠ proposer | new status + `memory_returned` receipt; agent Save on returned memory → new revision → `pending_review` |

SoD check runs INSIDE the transaction: the pending revision's `recordedBy`
must differ from the authenticated principal's `subjectId` → else
`SOD_VIOLATION` (fail-closed, §6.5.5). Existing approval path must gain this
clause (verify at implementation whether proposer equality is already
rejected).

Envelope semantics: any change to the proposal between review and decision
changes H1 → `ENVELOPE_MISMATCH`, invalidating the review and forcing a fresh
decision (§6.4, §6.5.3). This already holds for approve; reject/return join
the same guarantee.

## 6. Anti-rubber-stamp controls (§6.4, engine side)

- No bulk approval surface exists and none is added (greenfield — nothing to
  disable).
- High-risk approval (material/critical) requires the two review checks above
  (`REVIEW_CHECKS_REQUIRED`).
- Reason policy per row above; empty reason for required classes →
  `REASON_REQUIRED`.
- SoD fail-closed as above.
- Envelope exactness as above; proposal change invalidates the review.
- **Velocity / unusual-pattern observable events (minimal):** per-principal
  rolling counters; thresholds (defaults, configurable): > 30 approvals per
  15-minute window, or ≥ 3 consecutive rejections/returns without an
  intervening approval or different item set. On threshold → an immutable
  `review_velocity_alert` event row (audit-visible, listed via the audit
  trail). NOT a receipt (monitoring signal, not an accounting act) and NOT a
  blocking control in this slice (dashboard/UX is Drenyra's).

## 7. Surfaces

| Surface | Additions |
|---|---|
| MCP | `accounting_review_queue`, `accounting_review_detail`, `accounting_review_reject`, `accounting_review_return` (fail-closed without authenticated session, like existing accounting_* decision tools) |
| HTTP | `GET /accounting/review/queue`, `GET /accounting/review/{memoryId}`, `POST /accounting/memories/{memoryId}/reject`, `POST /accounting/memories/{memoryId}/return` |
| CLI | `review queue [--limit] [--offset]`, `review detail <id>`, `review reject <id> --reason ...`, `review return <id> --reason ...` (authenticated session for reject/return) |

## 8. Parity and tests

- New policy/transition logic joins the Go↔TS golden mechanism
  (`testdata/golden/*.json`): SoD policy vector, review-checks policy vector,
  `memory_returned` receipt vector, reject-with-reason payload vector.
- TS mirrors updated: `lifecycle/transitions.ts` (+`returned`), receipt
  payload builder, `store/memory-store.ts` (queue/reject/return interfaces).
- Go suite: `go test ./...`; TS suite: `npm test`; typecheck `tsc --noEmit`.

## 9. Acceptance mapping (design brief §6.5)

| §6.5 criterion | Engine-side proof |
|---|---|
| pending items visible without Phase 6 | queue over existing status; rule refs best-effort |
| scope isolation on every list/mutation | exact scope enforced in queue/detail/decisions |
| stale/changed proposal cannot be approved | fresh H1 vs expectedEnvelopeHash → ENVELOPE_MISMATCH |
| high-risk approval requires reason + checks | REASON_REQUIRED + REVIEW_CHECKS_REQUIRED |
| SoD failures fail-closed | SOD_VIOLATION inside the transaction |
| approve/reject/return idempotent | (tenant, requestId) reservation for all three |
| every decision emits event + signed receipt | approval/rejection/return events + receipts |
| never claims approval = correctness | boundary notice in detail; verification wording unchanged |
| cannot execute journal entry / SUNAT filing | no such surface exists (non-authorization boundary intact) |
| accessibility/keyboard | Drenyra Web UI (out of scope) |

## 10. Out of scope (explicitly deferred)

Bulk/batch approval, dual approval (audit J), HSM/KMS, velocity blocking,
Review Workspace Web UI, Phase 6 rules content, FTS. Each is tracked in the
ROADMAP/audit register; none is claimed implemented.
