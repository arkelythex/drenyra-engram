# Proposal — v1-readiness

> Phase: propose · Artifact: proposal · Status: draft
> Strict TDD: not active in this phase (planning only).

## Problem statement

Drenyra Engram cannot present its existing trust model as v1-ready while three engineering evidence gaps remain in the frozen v1 gate:

- **G-6 is PARTIAL:** cross-tenant and race coverage exist, but fuzz, restore, and corruption behavior are not demonstrated.
- **G-7 is PARTIAL:** signing-key rotation and revocation exist, but operators have no compromise-response playbook, and cutoff-time verification behavior still needs a gap analysis.
- **G-10 is NOT MET:** the candidate North Star metric—material decisions reconstructible to evidence + rule + approval—has no read-only measurement surface or baseline.

These gaps matter to accounting firms, operators, reviewers, and maintainers because the product promise is durable reconstruction under exact company scope. Without repeatable failure drills, a complete key-compromise response, and measurable reconstructibility, that promise depends on undocumented assumptions rather than inspectable evidence.

## Proposed change

Deliver three additive workstreams within the repository's existing boundaries.

### 1. G-6 resilience drills

- Add Go fuzz targets for the highest-risk parsers and decoders:
  - `core/comprobante.go` XML parsing;
  - receipt payload canonicalization;
  - search tokenization.
- Seed each target with valid, boundary, malformed, and previously failing inputs. Commit minimized crashers under `testdata/fuzz/` as permanent regressions.
- Enforce invariants appropriate to each target, including no panic, deterministic output, round-trip stability where defined, and no invalid successful result.
- Add bounded CI fuzz execution (`-fuzztime=30s`) rather than unbounded fuzzing.
- Add a read-only SQLite health-check surface that supports routine `quick_check`, full `integrity_check`, and paired `foreign_key_check` behavior.
- Add a corruption drill that damages a database copy, detects the failure, freezes writes, and preserves the corrupted copy as evidence rather than attempting in-place repair.
- Add a restore drill using `VACUUM INTO`, SQLite backup API, or an equivalent WAL-safe snapshot mechanism, then verify the restored database before use.
- Enable or document `cell_size_check=ON` where compatible with the existing connection lifecycle for earlier corruption detection.

### 2. G-7 key-compromise response

- Add `docs/security/key-compromise-response.md` with an operator sequence aligned to NIST SP 800-57:
  1. treat private-key exposure as a full trust failure;
  2. stop signing and suspend verification with the compromised key;
  3. preserve evidence and record the exact compromise time;
  4. generate an independent replacement keypair in a clean environment;
  5. publish an authenticated revocation;
  6. fail closed for signatures issued at or after the cutoff;
  7. inventory and re-sign affected artifacts where policy permits;
  8. investigate adjacent systems as potentially compromised.
- Gap-analyze `SigningKeyForVerify` and current `revoked_at` semantics.
- Harden verification, if required, so a known compromise cutoff rejects new signatures and signatures issued at or after that timestamp, with focused tests for before, at, and after the cutoff.

### 3. G-10 reconstructibility baseline

- Add a read-only `reconstructibility` surface for one exact company scope and period.
- Define a deterministic denominator of **material decisions** from existing domain metadata; the specification must freeze this eligibility rule before implementation.
- Count a decision as reconstructible only when existing verification and rule-version tracing can establish all three required links: evidence, applicable rule, and approval.
- Return aggregate counts, percentage, and bounded reason categories for non-reconstructible decisions so the baseline is explainable rather than log-only.
- Add baseline instrumentation that can be run repeatedly without mutating accounting state.

This work supplies the repository evidence for G-6 and G-7 and the engineering baseline required by G-10. It does **not** claim to close G-10's separate customer-observed target requirement.

### Non-goals

- No product UI.
- No production SUNAT or ERP integration.
- No identity-provider, MFA, or membership implementation.
- No external or third-party security review.
- No authorization, filing, payment, declaration, or execution capability.
- No claim that cryptographic integrity proves accounting correctness.
- No destructive corruption of a live database and no blind copy of a live WAL database.
- No pricing metric or customer-observed North Star target.
- No breaking contract change. Any contract change later found necessary requires a version bump, migration path, and explicit approval under `openspec/config.yaml`.

## Business rules and implications

- **Scope first:** reconstructibility and database operations take an exact company/RUC and period where applicable. Scope mismatch or ambiguity fails closed; cross-tenant aggregation is forbidden.
- **Money remains integer-only:** monetary values stay whole `int64` cents in Go and `BigInt` cents in TypeScript. The metric uses integer counts and an explicitly defined ratio representation; it must not introduce floating-point money.
- **Non-authorization boundary:** health, verification, and reconstructibility results are observations. They never authorize an accounting operation or assert accounting correctness.
- **Fail closed on corruption:** a failed SQLite check freezes writes, preserves byte-for-byte evidence, and requires restoration from a verified backup. The engine must not silently repair or continue.
- **Fail closed on known compromise:** once a compromise cutoff is authenticated, the affected key cannot validate signatures issued at or after that cutoff. Exact timestamp boundary behavior must be deterministic.
- **Existing historical semantics remain explicit:** signatures demonstrably issued before the cutoff are handled only according to the frozen receipt/revocation contract; this proposal does not silently redefine them.
- **Reconstructibility is evidence-based:** a decision contributes to the numerator only when evidence, rule version, and approval are all traceable through existing machinery. Missing, inaccessible, invalid, or scope-mismatched links do not count.
- **Read-only metric:** computing a baseline must not mutate decisions, approvals, evidence, receipts, or lifecycle state.
- **Golden Go↔TS parity:** any new shared domain result or canonical representation must join the byte-identical golden-vector mechanism. Go-only operational drill controls need not be mirrored unless they expose a shared contract.
- **Frozen contracts:** additions must remain backward compatible. Versioning and approval are mandatory if specification work discovers a public contract change.
- **Safe test data:** fuzz seeds, corruption fixtures, and drill artifacts contain no credentials or customer data.

## Impact

| Area | Expected impact |
|---|---|
| `core/comprobante.go`, receipt canonicalization, search tokenizer | Focused Go fuzz targets, seed corpora, invariants, and regression crashers. |
| `internal/store` | Read-only SQLite health checks, fail-closed write-freeze behavior, WAL-safe backup/restore drill support, and tests against copies. |
| Signing and verification paths | Gap analysis and potentially additive compromise-cutoff enforcement around `SigningKeyForVerify` / `revoked_at`. |
| Existing verification and rule-version tracing | Reused to compute reconstructibility without creating a second truth model. |
| CLI / HTTP / MCP adapters | An additive read-only reconstructibility and/or database-health exposure where the later spec selects an existing adapter pattern. No UI. |
| `contracts/` and TypeScript mirrors | No breaking change expected. Any new shared result shape requires approved additive contract treatment and Go↔TS parity. |
| `testdata/golden`, `testdata/fuzz` | New parity vectors where shared semantics change; committed fuzz seeds and minimized crashers. |
| `.github` or existing CI configuration | Bounded fuzz jobs or steps with a 30-second target budget, subject to the repository's current CI layout. |
| `docs/security/key-compromise-response.md` and v1 gate evidence | New operational playbook and evidence links for G-6/G-7/G-10 status updates. |

The largest implementation implication is that these workstreams cross core, store, security, adapters, tests, and docs. They should be delivered as reviewable slices while preserving one coherent v1-readiness outcome.

## Edge cases and open questions

- Empty periods must return a defined zero-denominator result, not a misleading 0% or 100%; the spec must freeze the representation.
- Partially reconstructible decisions—such as valid approval and rule trace but unavailable evidence—remain outside the numerator and report a reason category.
- Superseded, rejected, draft, reopened, or cross-period records need an explicit material-decision eligibility rule to prevent unstable baselines.
- SQLite checks may be slow or interrupted. Routine checks should use `quick_check`; full `integrity_check` is an explicit drill/diagnostic path. Both pair with `foreign_key_check`.
- Restore succeeds only after integrity, foreign-key, scope, and expected backup-identity checks pass. A readable database alone is insufficient.
- A compromise timestamp equal to a signature issuance timestamp is rejected. Missing or unauthenticated cutoff information must not be guessed.
- Fuzzing may discover expensive but non-crashing inputs; targets need bounded input sizes and deterministic invariants without weakening parser behavior.

## Proposal question round

The delegated brief is sufficient to draft the proposal, so the following assumptions need user/product review during specification rather than blocking this artifact:

1. **Materiality:** Which existing domain states or fiscal effects define a material decision, and are superseded/rejected records excluded from the denominator?
2. **Empty baseline:** What exact result should represent a scope/period with zero material decisions?
3. **Exposure boundary:** Must reconstructibility be available through CLI, HTTP, and MCP in the first slice, or is one canonical service plus CLI sufficient initially?
4. **Historical compromise policy:** Are pre-cutoff signatures retained as valid when all other verification layers pass, or should a declared full-trust failure invalidate the key's entire history?
5. **Operational ownership:** Who is allowed to freeze/unfreeze writes after a corruption signal, given that the engine reports state but does not become an authorization system?

Current proposal assumptions are: eligibility will reuse existing materiality metadata; zero denominator will be explicit and percentage-free; the service/result model is canonical with adapters added only where already conventional; at/after cutoff is always rejected; and recovery requires an explicit operator procedure outside the engine's authorization boundary.

## Alternatives considered

### No fuzzing; rely on example tests

Rejected. Example tests cover known cases but do not explore malformed parser/decoder inputs. Bounded CI fuzzing plus committed crashers adds repeatable evidence at controlled cost.

### Run fuzzing only manually

Rejected as the sole strategy. Manual campaigns remain useful, but a short CI budget prevents regressions in known high-risk targets. The tradeoff is additional CI time and possible platform-sensitive findings, mitigated by deterministic targets and committed seeds.

### Write only the compromise playbook

Rejected. Documentation without checking cutoff semantics can prescribe behavior the engine does not enforce. Playbook and implementation must agree; hardening occurs only where the gap analysis demonstrates a mismatch.

### Treat any revocation as invalidating all historical signatures

Not selected by default. It is simpler and more conservative, but may erase valid pre-compromise history. The proposal follows the supplied cutoff rule—reject at/after compromise—and leaves the stricter full-history policy as an explicit product/security decision.

### Emit reconstructibility only to logs

Rejected. Logs are hard to scope, compare, explain, and consume safely. A deterministic read-only surface provides an inspectable baseline and bounded failure reasons without making logs a public metric contract.

### Build a second reconstruction model

Rejected. Reusing existing verification and rule-version tracing avoids divergent truth definitions and reduces the chance that the metric reports success when offline verification would fail.

## Risks

- **Materiality ambiguity:** a poorly chosen denominator could inflate the North Star or make baselines incomparable. Mitigation: freeze the eligibility rule and fixtures in the spec before implementation.
- **Security semantic change:** cutoff enforcement may invalidate receipts previously accepted by current code. Mitigation: gap-analyze first, test the exact timestamp boundary, document compatibility, and require contract approval if public semantics change.
- **Operational denial of service:** automatic write freeze or full integrity scans could be triggered incorrectly or run on hot paths. Mitigation: keep full scans explicit, make routine checks bounded, preserve diagnostic evidence, and specify controlled recovery.
- **False confidence from drills:** passing synthetic drills does not prove production backup operations. Mitigation: label evidence accurately and do not claim external operational validation.
- **Cross-runtime drift:** a shared reconstructibility result could diverge between Go and TypeScript. Mitigation: use golden parity for shared domain semantics.
- **Review size:** three cross-cutting workstreams may exceed the preferred review budget. Mitigation: plan independent, dependency-aware delivery slices without weakening the common proposal.

## Rollback

- All new surfaces are additive and read-only; they can be removed or disabled before release without rewriting accounting records.
- No drill operates destructively on a live database. Corruption tests use disposable copies, and restore tests create separately verified outputs.
- Any schema addition must be backward compatible and have an explicit rollback/migration plan in design; this proposal prefers no schema migration.
- CI fuzz steps can be reverted independently if they prove unstable while preserving seeds and regression tests.
- Compromise-cutoff enforcement must not be silently disabled after release. If compatibility evidence requires rollback, release a documented versioned policy correction and preserve the security event trail.

## Acceptance outlook

This proposal is successful when later specifications and implementation can demonstrate:

- the three high-risk parser/decoder areas have deterministic fuzz targets, diverse committed seeds, bounded CI execution, and a crasher-to-regression policy;
- a copied database fails closed under deliberate corruption, writes remain frozen, evidence is preserved, and no in-place repair is attempted;
- a WAL-safe backup can be restored and passes integrity, foreign-key, scope, and expected-identity checks before use;
- operators have a complete key-compromise playbook, and verification tests prove deterministic behavior before, at, and after an authenticated cutoff;
- an exact-scope, exact-period reconstructibility query reports denominator, numerator, explainable gaps, and a repeatable baseline without mutation;
- shared semantics remain Go↔TS golden-compatible, money remains integer cents, and no surface crosses the non-authorization boundary;
- v1 gate evidence can mark G-6 and G-7 complete if all specified proofs pass, while G-10 records an engineering baseline and remains open until a customer-observed target exists.
