# Archive Report — v1-readiness

> Phase: archive · Artifact: archive-report · Status: **ARCHIVED (PASS)**
> Inputs: proposal + spec + design + tasks + apply-progress + verify-report (OpenSpec file store).
> Store: OpenSpec file-based (`openspec/changes/v1-readiness/`). No engram writes (config.yaml `artifact_store: openspec`).
> Archived against: HEAD `1025c4d` — the chain PR-1..PR-5 + remediation is merged and pushed; `sdd-verify` re-run is PASS (archive-ready, all 11 ACs green).
> Forwarded final-state facts (orchestrator, outrank stale snapshots): full chain delivered; gates Go 10/10 packages, TS 385/385, typecheck, vet, gofmt, golden Go↔TS, search bench, `make fuzz-ci` 3×30s (no crashers); `contracts/` untouched; non-goals held.

## Archive status

**PASS** — archive preconditions all satisfied:

- Verification report present and clearly passing (re-run at `486ad08`/`1025c4d`, supersedes the 2026-08-10 FAIL report); no unresolved `FAIL`, `BLOCKED`, `CRITICAL`, or verification blockers; the previous report's 5 CRITICAL/WARNING findings are all resolved with mapped green tests.
- All required artifacts present and readable (proposal, spec, design, tasks, apply-progress, verify-report).
- Implementation tasks complete: 38/38 `- [x]` rows with `sdd-owner: implementation`; zero unchecked implementation markers.
- File-backed mode: this repository's OpenSpec convention uses a flat `spec.md` as the spec artifact (`openspec/change-templates/state.yaml` links `spec: openspec/changes/{change-name}/spec.md`); there is no `openspec/specs/{domain}/` canonical layer, no `specs/` subdirectory, and no `sync-report.md` exists for any prior change. No sync was required or performed; the parent prompt scoped this archive to state-marking (archive-report + state marker) per the repo convention.
- No destructive merge performed; no REMOVED/MODIFIED canonical merge; no explicit destructive-merge approval needed.

## Artifacts read

| Artifact | Path | Status |
|---|---|---|
| Proposal | `openspec/changes/v1-readiness/proposal.md` | read — G-6/G-7/G-10 evidence gaps, 3 additive workstreams, non-goals |
| Spec | `openspec/changes/v1-readiness/spec.md` | read — FZ-1…FZ-4 frozen, FR-1…FR-10, NFR-1…5, IR-1…5, AC-1…AC-11 |
| Design | `openspec/changes/v1-readiness/design.md` | read — D-1…D-10, WU-1…WU-6, file change plan |
| Tasks | `openspec/changes/v1-readiness/tasks.md` | read — 38/38 implementation rows checked; 5 parent-owned lifecycle rows unchecked (non-blocking, listed below) |
| Apply progress | `openspec/changes/v1-readiness/apply-progress.md` | read — PR-1..PR-5 + remediation slice evidence |
| Verify report | `openspec/changes/v1-readiness/verify-report.md` | read — PASS, all 11 ACs green |
| Config | `openspec/config.yaml` | read — conventions, verify_order, strict_tdd |
| State template | `openspec/change-templates/state.yaml` | read — archive convention / state machine |
| Sync report | (absent) | noted — repo has no canonical `specs/` layer; no sync-report convention in use |

## What shipped (per slice)

| Slice | Commit | Delivered |
|---|---|---|
| PR-1 (W4 metric core) | `d02ee75` | Pure FZ-1 eligibility + FZ-2 classifier + integer ratio (`internal/core/reconstructibility.go`), TS mirror (`core/reconstructibility.ts`), golden vectors (`testdata/golden/reconstructibility-*.json`), store latest-material-head read, server aggregator (`API.Reconstructibility`). PR-1 defect (TS golden dispatcher union) fixed with one token in PR-2's slice. |
| PR-2 (W4 adapters) | `db93011` | CLI `reconstructibility <ruc> --period` (exit 0 zero-denominator / exit 2 usage), MCP `accounting_reconstructibility` (strict scope, fail-closed), HTTP `GET /accounting/reconstructibility` (exact four-field scope, token guard), adapter parity tests. |
| PR-3 (W2 doctor + drills) | `caf5da4` | `DoctorReport` extended (`quickCheck`, `integrityCheck`, `foreignKeyCheck`, `cellSizeCheck`), routine = `quick_check` + `foreign_key_check`, full integrity only on the explicit drill path, copy-only corruption/restore drill surface, drill-marker contract, CLI `doctor --drill-copy` mode. |
| PR-4 (W1 fuzz + CI) | `c8eaf6a` | Three fuzz targets (`FuzzParseComprobanteXML`, `FuzzCanonicalReceiptPayload`, `FuzzSearchTokenize`), 21 committed seed-corpus entries, root `Makefile` `fuzz-ci` (3×30s bounded), `.github/workflows/fuzz.yml` (dispatch + weekly, never on push/PR). Real defect found+fixed: `parsePayableAmount` trailing-garbage silent parse → strict `strconv.ParseInt` + ISO 4217 guard (fail-closed strengthening). |
| PR-5 (W3 G-7) | `acba08b` | `docs/security/key-compromise-response.md` (8 NIST-aligned steps), gap analysis concluding Implementation == Contract (FZ-3), 7-row pure FZ-3 cutoff matrix + 4-case service matrix as permanent regressions, playbook readback + three-way contract guard. |
| Remediation (PR-3-R) | `486ad08` | Closed every verify blocker: `RunCorruptionDrill` (mark-enforced, deterministic page damage, detection, `STORE_WRITE_FROZEN` latch, byte preservation, no repair), `RunRestoreDrill` (4 ordered verify-after-restore checks → atomic publish + `.drenyra-verified.json`), scope-isolation conformance, strict `strconv.Atoi` schema-version parse. |
| Verify re-run | `1025c4d` | `sdd-verify` PASS — archive gate ready. |

## v1-gate impact

| Gate | Status | Delivered evidence |
|---|---|---|
| **G-6** (fuzz / corruption / restore drills) | **PARTIAL → stronger** | Fuzz targets + seed corpus + bounded CI (AC-1/AC-2), doctor SQLite health checks (AC-3), copy-only corruption drill with write-freeze latch (AC-4), WAL-safe restore drill with verify-after-restore (AC-5). Drills exist; the gate also mentions cross-tenant + race, which pre-existed → effectively stronger than before. |
| **G-7** (compromise-response playbook + cutoff) | **PARTIAL** | NIST-aligned playbook + gap analysis (AC-6), FZ-3 cutoff boundary tests — before/equal/after, `created_at > issued_at`, fail-closed timestamps, revoked-key signing refusal (AC-7). Playbook written + tested; HSM/KMS integration and a compromise-response drill remain open per the audit. |
| **G-10** (reconstructibility North Star) | **PARTIAL** | Engineering baseline delivered: read-only reconstructibility surface (CLI/MCP/HTTP), FZ-1/FZ-2 frozen definitions, integer-only ratio/percentage, golden Go↔TS parity, determinism + read-only proof (AC-8/AC-9/AC-10). Customer-observed target still pending. |
| G-1/G-2/G-4/G-8 | open | External evidence — parent-owned. |

## Final gate results (config.yaml verify_order + mandated extras — all green)

| # | Gate | Result |
|---|---|---|
| 1 | `npm run typecheck` | ✅ exit 0 |
| 2 | `go vet ./...` | ✅ exit 0 |
| 3 | `gofmt -l .` | ✅ no output |
| 4 | `go test ./...` | ✅ 10 packages ok |
| 5 | `npm test` | ✅ 385 tests / 26 files |
| 6 | `go test ./internal/core -run TestGoldenVectorsGo` | ✅ PASS |
| 7 | `go test ./internal/search/bench -v` | ✅ PASS |
| 8 | `make fuzz-ci` (3 × 30s) | ✅ exit 0, no crashers, no new corpus entries |

Acceptance criteria: **AC-1…AC-11 all PASS** (per verify-report re-run).

## Task completion

- Implementation rows: **38/38 checked** (`sdd-owner: implementation`); zero unchecked implementation markers.
- Cross-cutting checklist (6 rows) and DoD implementation rows (2 rows) checked with evidence after the remediation slice.
- Stale-checkbox reconciliation (recorded): tasks 2.5/2.6/2.7 were falsely `[x]` before the remediation slice (verify FAIL finding); they are now HONESTLY checked because the deliverables exist (`RunCorruptionDrill`, `RunRestoreDrill`, scope-conformance/backup-identity verification) with green mapped tests. PR-1/PR-3 checkbox reconciliation recorded in apply-progress. No further mechanical repair performed during archive; none was needed.
- Unchecked `- [ ]` lines remaining in tasks.md (exact, all `sdd-owner: parent` — deferred lifecycle actions, NOT implementation work; non-blocking, explicitly reconciled by verify-report):

```text
- [ ] Review Workload Guard decision recorded (delivery_strategy, chain_strategy) before apply; change delivered as chained PRs per the forecast split. <!-- sdd-owner: parent -->
- [ ] Bounded post-apply review of the chained diff (native review as applicable per repository policy), then `sdd-verify` against the spec, then `sdd-archive`. <!-- sdd-owner: parent -->
- [ ] Start or reuse bounded review for each PR boundary after its normalization + candidate freeze; one correction budget per candidate; no reviewer launched by apply. <!-- sdd-owner: parent -->
- [ ] After the final PR merges, run the verify phase (`sdd-verify`) against AC-1…AC-11 and this tasks list; remediate only through the bounded correction path. <!-- sdd-owner: parent -->
- [ ] Archive the change only when verify reports all criteria green and the chain is fully merged. <!-- sdd-owner: parent -->
```

Line 136's substance ("run the verify phase against AC-1…AC-11") is satisfied by the re-run PASS; these rows remain unchecked as the parent's lifecycle convention.

## Archive convention (repo) and state marker

- `openspec/change-templates/state.yaml` defines the state machine: `draft | proposed | in-progress | verify | archive`, with per-phase `pending | done` statuses for proposal → spec → design → tasks → apply → verify → archive.
- **No `state.yaml` existed inside `openspec/changes/v1-readiness/`** — per the parent's directive ("update the change's state accordingly if a state.yaml exists, else note the convention") and the archive task list, a state marker was created following the template: `openspec/changes/v1-readiness/state.yaml` with `status: archive`, all phases `done`, `archive: done`, `next_recommended: none`, `blocked_reasons: []`.
- **Folder move intentionally NOT performed.** The parent prompt scoped this archive to "archive-report + any state marker" and "Do NOT touch code; update only the openspec artifacts". The repo convention archives by state marking (the change folder stays under `openspec/changes/v1-readiness/` with `status: archive`), rather than moving to `openspec/changes/archive/`. No `openspec/changes/archive/` directory exists in this repo. The audit trail is fully preserved in place.

## Domains synced

**None.** This repo has no `openspec/specs/{domain}/spec.md` canonical layer; the spec artifact is the flat `spec.md` per `change-templates/state.yaml`. No sync-report.md exists; no archive-time sync fallback was required or performed (nothing to sync, and the parent did not request one). ADDED/MODIFIED/REMOVED requirement-name reporting therefore does not apply to this archive.

## Active same-domain change warnings

**None.** `v1-readiness` is the only change under `openspec/changes/`. No other active change touches the same domain.

## Structured status / actionContext findings

- Native dispatcher quirk (recorded in every prior phase, not a blocker): the engine reports `applyState: blocked`, `nextRecommended: spec`, `dependencies.specs: blocked` with empty `blockedReasons` because it does not index the flat `spec.md` (`artifactPaths.specs` empty). OpenSpec file store is authoritative here; all artifacts present and readable.
- `actionContext`: repo-local; edit scope confined to `openspec/changes/v1-readiness/` for this archive (archive-report + state marker only). No code touched.

## Destructive merge approvals / blockers

**None.** No canonical merge was performed; no REMOVED/MODIFIED requirement blocks; no destructive change to any spec. `contracts/` untouched (`git diff d02ee75~1..HEAD -- contracts/` = 0 lines, verified by the verify phase).

## Remaining open items (parent-owned)

1. **G-10** — customer-observed North Star target (engineering baseline + instrumentation delivered; customer target pending).
2. **G-7** — HSM/KMS integration + compromise-response drill (playbook + cutoff tests delivered; per the audit these remain open).
3. **G-1 / G-2 / G-4 / G-8** — external evidence (all parent-owned).

## Archived path

- Change folder: `openspec/changes/v1-readiness/` (kept in place; state-based archival per repo convention and parent scope).
- State marker: `openspec/changes/v1-readiness/state.yaml` (`status: archive`).
- This archive report: `openspec/changes/v1-readiness/archive-report.md`.
- Memory observation IDs: N/A (OpenSpec store; no engram writes).

## Next steps

- Update `ROADMAP.md` with the delivered v1-gate evidence: G-6 (fuzz + corruption/restore drills), G-7 (playbook + cutoff tests, HSM/KMS still open), G-10 (engineering baseline; customer-observed target still open), and record the final gate results.
- Parent lifecycle: optionally mark the 5 deferred `sdd-owner: parent` rows complete (verify PASS + this archive satisfy their substance) or leave them as historical lifecycle records.
- If a physical archive folder is ever desired, move `openspec/changes/v1-readiness/` → `openspec/changes/archive/YYYY-MM-DD-v1-readiness/` (audit-trail-safe) — not performed per parent scope.

## Risks

- **No physical archive move** (deviation from the generic sdd-archive template, per explicit parent scope and repo state-based convention): the change folder remains under `openspec/changes/`. If a later consumer expects `openspec/changes/archive/`, the state marker documents the archived state; a safe move can still be performed later.
- **No canonical sync layer**: future OpenSpec tooling expecting `openspec/specs/{domain}/spec.md` will not find one; this is a repo-convention gap, not a v1-readiness defect.
- **G-6/G-7/G-10 remain PARTIAL at the gate level** despite full AC delivery — archive records engineering delivery, not gate closure; remaining items are parent-owned.
- **Fuzz exec counts vary across runs** (71k/743k/302k vs 213k/695k/305k) — non-deterministic by design; the gate contract (3×30s, exit 0, no crashers) held in every run.
- **Cosmetic subtest-count prose drift** (14 vs 10) noted in verify-report — no substance impact.
