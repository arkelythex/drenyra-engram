# Fiscal Policy Memory v0.6 — Integration Design

**Decision:** fiscal rules remain `kind=rule` memories. The existing `(topicKey, exact Scope)` revision chain is the rule-version chain; v0.6 adds policy metadata, version-pinned rule links, temporal verification, and reverse impact reads. It does not introduce a second Rule aggregate.

This design implements ROADMAP Phase 6 (`ROADMAP.md:193-197`) additively in the Go engine and TypeScript mirror. Existing `ruleRefs`, envelope bytes, receipt formats, and frozen golden vectors remain unchanged for memories that do not use v0.6 fields.

## 1. Decisions and invariants

| Area | Decision |
| --- | --- |
| Rule aggregate | A rule remains an `AccountingMemory` with `KindRule` (`internal/core/types.go:105-107`). `Save` on the same `topicKey` and exact `Scope` creates its next immutable revision; `FindChain`/`API.Chain` remains the history source (`internal/server/api.go:108-125`). |
| Why not a Rule table | A separate entity would duplicate identity, scope, lifecycle, provenance, supersession, receipts, sync, and chain reads. Logical link validation provides the useful Rule FK behavior without a second aggregate. |
| Jurisdiction | Add `policyRule.jurisdiction` to the rule memory, never to `Scope`. `Scope` continues to identify tenant/company/period; policy geography must not fragment memory identity. |
| Vigencia | `Validity{EffectiveAt, ExpiresAt, Source}` remains the sole temporal window (`internal/core/types.go:286-306`). Windows are half-open: `[effectiveAt, expiresAt)`; an empty bound is unbounded. |
| Hash freeze | Legacy `ruleRefs` retain their exact `canonicalRefs` contribution (`internal/core/types.go:717-739`). `Validity` remains excluded for legacy memories. A self-describing policy-rule contribution is appended only when `policyRule` is present, following `CloseSnapshot`. |
| Version pointer | `rule_links.version` stores the immutable rule-memory ID for one chain revision, not the mutable latest revision and not a display label. The API calls it `version`; traces also expose its numeric `revision`. |
| Decision time | `rule_links.effective_at` snapshots the consuming memory's `EffectiveAt` and must equal it for links created with the memory. |
| Correctness boundary | Resolution proves provenance, chain membership, temporal applicability, and lifecycle consistency. It never asserts fiscal or accounting correctness. |

## 2. Domain contracts

### 2.1 `PolicyRule`

Add an optional field to Go `AccountingMemory` and `SaveInput`, mirrored in `core/types.ts`:

```json
{
  "policyRule": {
    "jurisdiction": "PE",
    "legislation": "NATIONAL-TAX",
    "authority": "National tax authority",
    "tags": ["indirect-tax", "late-document"]
  }
}
```

Contract:

- Allowed only when `kind=rule`; any other kind fails with `INVALID_POLICY_RULE`.
- `jurisdiction` is required, uppercase, and matches `^[A-Z][A-Z0-9-]{1,15}$`; examples are `PE`, `LATAM`, and `INTL`. The engine validates syntax, not geopolitical or legal truth.
- `legislation` and `authority` are required non-empty strings. The first identifies the regime/family; the second records its issuer or policy owner.
- `tags` are optional, trimmed, non-empty, deduplicated, and lexicographically sorted for canonicalization.
- A v0.6 rule created through rule-specific write surfaces requires `policyRule` and declared `Validity.Source`. Legacy generic saves remain compatible with `policyRule=nil`.

`policy_rule_json` stores compact canonical JSON in this exact property order: `jurisdiction`, `legislation`, `authority`, `tags`. The canonical hash contribution is:

```text
policyRule/v0.6\x00<canonical-policy-rule-json>\x00<validity.effectiveAt>\x00<validity.expiresAt>\x00<validity.source>
```

Append that contribution to both `ComputeContentHash` and `ComputeEnvelopeHash` only when `policyRule != nil`. Therefore:

- every pre-v0.6 memory and frozen vector hashes byte-identically;
- a v0.6 policy rule changing only its vigencia gets a distinct content/envelope hash and a new chain revision;
- legacy `Validity` hash exclusion remains frozen outside the opt-in extension.

### 2.2 Structured rule links

Keep `AccountingMemory.ruleRefs: string[]` unchanged. Add a write/read link shape, not a second hash field:

```json
{
  "ref": "policy/indirect-tax/late-document",
  "version": "<immutable rule memory id>",
  "effectiveAt": "2026-07-31T12:00:00Z"
}
```

`ref` is the rule chain's stable `topicKey`. `version` identifies exactly one `KindRule` row in that chain. `effectiveAt` is the consuming decision's accounting time.

For v0.6 writes, `SaveInput.ruleLinks[]` is optional and transport-only. The store derives/deduplicates bare `RuleRefs` from `ruleLinks[].ref` before hashing, inserts the memory and structured rows in the same `BEGIN IMMEDIATE` transaction, then emits existing receipts. Structured metadata does not contribute to the envelope.

Compatibility rules:

- Existing `ruleRefs` without link metadata remain valid legacy references. Their temporal layer is `skipped`, with an explicit `legacy unversioned rule ref` detail.
- A structured link requires non-empty `ref` and `version`, plus RFC3339 `effectiveAt`.
- The target must exist, be `KindRule`, have `Identity.TopicKey == ref`, and belong to a chain visible from the consuming memory's tenant boundary.
- `(memory_id, ref)` remains unique. Repeating the identical structured link is a no-op; a different version/date for the same pair fails `RULE_LINK_VERSION_CONFLICT`. Metadata is never updated in place.
- `AddRuleLinkVersion(memoryID, link, actor)` is the post-save API. It retains the closed-period gate and atomically inserts metadata plus refreshes the envelope only when the bare ref itself is new (`internal/store/store.go:4763-4830`).

## 3. Schema v7 and migration

Upgrade `schemaVersion` from 6 to 7 (`internal/store/store.go:101`) with one fail-closed transaction:

```sql
ALTER TABLE observations ADD COLUMN policy_rule_json TEXT NULL;
ALTER TABLE rule_links ADD COLUMN version TEXT NULL;
ALTER TABLE rule_links ADD COLUMN effective_at TEXT NULL;
CREATE INDEX idx_rule_links_ref
  ON rule_links(ref, version, effective_at, memory_id);
UPDATE schema_version SET version = 7;
```

No existing row is backfilled or re-hashed. `NULL` means legacy/unversioned. Validate that all three columns and the index are absent before mutation; any conflict rolls the whole v6→v7 step back and leaves `schema_version=6`, matching the v5→v6 fail-closed pattern (`internal/store/migration_v6_test.go:328-367`). Fresh schema DDL includes the same columns and index.

`policy_rule_json` is decoded with unknown-field rejection and re-canonicalized on read; malformed stored JSON is corruption, not silently ignored. `rule_links.version` deliberately has no SQL FK because pre-v7 refs are not rule IDs and imported chains may arrive in dependency order; service verification enforces the logical reference.

## 4. Resolution and verification

### 4.1 `ResolveRuleVersion`

Given a consuming memory and structured link:

1. Load the row identified by `version`; require `KindRule` and `topicKey == ref`.
2. Load its exact `(topicKey, Scope)` chain and prove membership with the stored numeric revision.
3. Require `link.effectiveAt == consumingMemory.EffectiveAt`.
4. Select chain revisions whose `Validity` contains that instant using `[start,end)`.
5. Fail on zero matches (`RULE_NOT_IN_FORCE`) or multiple matches (`RULE_VIGENCIA_OVERLAP`); never guess.
6. Require the sole resolved row ID to equal `link.version`, otherwise fail `RULE_VERSION_MISMATCH`.
7. Reconstruct lifecycle status at that instant from initial status plus ordered `transition_log`. A later supersession is historically valid; a rule already rejected, voided, or superseded at that instant fails `RULE_STATUS_INVALID`.

The pure result is `RuleVersionTrace`:

```json
{
  "ref": "policy/indirect-tax/late-document",
  "linkedVersion": "rule-v1-id",
  "resolvedVersion": "rule-v1-id",
  "revision": 1,
  "decisionEffectiveAt": "2026-07-31T12:00:00Z",
  "validity": {"effectiveAt":"2026-01-01T00:00:00Z","expiresAt":"2026-08-01T00:00:00Z","source":"declared"},
  "jurisdiction": "PE",
  "legislation": "NATIONAL-TAX",
  "statusAsOf": "active",
  "outcome": "passed",
  "detail": "linked rule revision was the sole revision in force"
}
```

### 4.2 Additive verification report

Append `LayerRuleVersionVigencia = "rule version/vigencia"` after existing `rule availability` (`internal/server/verify_service.go:98-110`). Run one pure layer per rule link and combine it with generic `AggregateLayers`; aggregation itself does not change.

Add optional `ruleVersions,omitempty: RuleVersionTrace[]` to `VerificationReport`. It is absent when no structured links exist, preserving existing parity JSON byte-for-byte. A legacy unversioned ref contributes a skipped layer and trace outcome; an invalid structured ref fails the report. The report still ends with `Accounting correctness: NOT ASSERTED`.

The Go `VerificationStore` gains read-only methods for structured links, exact rule chains, and transition history. Mirror constants, pure resolution inputs, layer behavior, traces, ordering, and fixtures in `core/verify.ts`.

## 5. Regulatory-change impact

Add pure service `RuleImpact(topicKey, optional exact Scope)`:

1. Resolve matching `KindRule` chain(s); tenant visibility is mandatory and cross-tenant results are forbidden.
2. Read `rule_links` by exact `ref=topicKey` using `idx_rule_links_ref`; include legacy `version IS NULL` rows as unresolved.
3. Join each link to its consuming memory and referenced rule revision.
4. Compare the consuming memory interval (`Validity` when present, otherwise point `EffectiveAt`) with the selected changed revision's vigencia window.
5. Return deterministic order by consuming `effectiveAt`, `memoryId`, `ref`.

`RuleImpactResult` contains rule chain identity, selected revision/window, and items with consuming memory ID/topic/kind/status, linked/resolved version, decision time, jurisdiction, `overlapsChangedWindow`, and resolution outcome. The caller may select a revision/version; absent selection means latest. This read never mutates status, links, receipts, or envelopes.

Topic keys may contain `/`; HTTP clients percent-encode `{topic}`. If a topic maps to multiple exact scopes and no selector is supplied, fail `RULE_CHAIN_AMBIGUOUS` rather than merge unrelated chains.

## 6. Surfaces and data flow

| Surface | Contract |
| --- | --- |
| CLI | `rule show <topic> [scope flags]`, `rule history <topic> [scope flags]`, `rule impact <topic> [--revision N] [scope flags]`; indent-2 JSON, read-only. Rule creation/evolution continues through normal `save kind=rule`. |
| MCP | `accounting_rule_show`, `accounting_rule_history`, `accounting_rule_impact`; structured topic/scope/revision inputs and the same service outputs. Existing memory-save tooling performs create/update. |
| HTTP | `GET /accounting/rules/{topic}`, `GET /accounting/rules/{topic}/history`, `GET /accounting/rules/{topic}/impact?revision=N` under existing read authentication and tenant boundaries. Existing memory POST/lifecycle routes remain the mutation surface. |
| Delete | No hard delete. Existing lifecycle voiding retires a rule; immutable history remains reconstructible. |

Write flow: transport validation → rule/link domain validation → canonical policy rule + bare refs → one store transaction saves/supersedes and inserts structured links → hashes/receipts commit → response returns memory and links.

Read flow: exact tenant/scope resolution → chain/link reads → pure temporal resolution or impact comparison → deterministic DTO → CLI/MCP/HTTP adapter.

Compliance gate: creation or revision of regulated fiscal-policy content must carry source provenance and pass the external `@drenyra/pi` compliance suite. Fixtures use synthetic content. Engine and docs state: “provenance recorded; tax correctness not asserted.”

## 7. File-level integration map

- `internal/core/types.go`, `core/types.ts`: `PolicyRule`, validation, clone, canonical contribution, memory/save fields.
- `internal/store/store.go`, new `internal/store/migration_v7_test.go`: schema/migration, canonical JSON persistence, atomic structured links, reverse query.
- `internal/core/verify.go`, `core/verify.ts`: trace types, layer constant, pure temporal/version checks.
- `internal/server/verify_service.go`: load chain/link/status inputs and append layer/traces.
- `internal/server/api.go`, `internal/server/http.go`, MCP server files, `cmd/drenyra-engram/main.go`: show/history/impact adapters.
- `store/memory-store.ts`: mirrored link model, conflict/idempotency behavior, in-memory impact reads.
- `contracts/memory.md`: rule chains, `PolicyRule`, validity hash opt-in, legacy exclusion, and structured-link limitations.
- `testdata/`: new v0.6 vectors only; existing adjustment and stale-envelope vectors remain byte-identical.

## 8. Acceptance and failure tests

End-to-end acceptance:

1. Save rule v1 under a stable topic, jurisdiction `PE`, with a declared vigencia window.
2. Atomically save a decision whose structured link pins v1 at the decision `EffectiveAt`.
3. Save rule v2 on the same exact chain with `LATAM` metadata and a later/non-overlapping window.
4. `rule history` returns v1 then v2; v1 is superseded but historically active at the decision instant.
5. `rule impact` for v2 returns the decision and classifies overlap with the changed window.
6. `verify memory` resolves the decision to v1, matches the pin, and emits a passing trace.
7. Save another revision changing only `Validity`; it becomes the next revision and changes v0.6 policy-rule hashes.
8. Compliance fixtures prove provenance and external-gate invocation without asserting fiscal correctness.

Also test: half-open boundary equality; open bounds; overlapping/no-match windows; wrong kind/topic/tenant/version; voided-as-of time; legacy skipped behavior; ambiguous scopes; deterministic impact order; malformed `policy_rule_json`; v6→v7 rollback; fresh v7; unchanged legacy hashes/goldens; Go↔TS canonical bytes and verification JSON.

Concurrency: two writers pinning different versions for the same `(memory_id, ref)` result in exactly one commit and one `RULE_LINK_VERSION_CONFLICT`; no partial memory/link/envelope state is visible. Save-with-links rollback tests inject failures after memory insert and after link insert.

## 9. Executable batches

1. **`feat(rule): add policy rule metadata and jurisdiction`** — domain types/validation, additive hash contribution, schema v7 policy column, Go↔TS canonical tests, migration tests, contract update.
2. **`feat(ref): pin rule versions in atomic links`** — structured DTO, v7 link columns/index, save transaction integration, conflict/idempotency/concurrency tests, TS store mirror.
3. **`feat(impact): reconstruct regulatory change impact`** — reverse query, pure impact service, CLI/MCP/HTTP show/history/impact, tenant/scope and temporal tests.
4. **`feat(verify): reconstruct historical rule versions`** — resolver, verification layer/traces, Go↔TS vectors, end-to-end acceptance and compliance-gate tests; finish with the docs/golden-freeze check in the same work unit.

Each batch includes focused tests and leaves the repository usable. If a batch approaches the review limit, split only at these behavior boundaries; never separate tests from implementation.

## 10. Rollout

Release reads first against migrated v7 stores; legacy links remain visible but explicitly unversioned. New rule-specific writes emit `policyRule` and pinned links. No automatic backfill is allowed because choosing a historical revision is a fiscal assertion. A future reviewed migration tool may add pins with explicit provenance.

Rollback before v0.6 writes may use the normal database backup. After v0.6 rows exist, binary downgrade is unsupported: retain the v7 database and roll forward, because older binaries cannot preserve policy metadata or structured links.
