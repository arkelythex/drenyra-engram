# specs/

Requirement records for Drenyra Engram.

Each directory holds a change's `spec.md`, the requirements of record. Imported specifications retain their original bytes and identifiers; historical workflow terminology inside them is provenance, not an active workflow. ODD feature documents track delivery separately.

| Spec | Subject |
| --- | --- |
| [`audit-register-closure`](audit-register-closure/spec.md) | Audit register closure |
| [`fiscal-runtime-foundations`](fiscal-runtime-foundations/spec.md) | Fiscal requirements; [ODD continuity and unfinished work](../odd/tasks/fiscal-runtime-foundations.md), not a claim of completed delivery |
| [`scope-param-rollout`](scope-param-rollout/spec.md) | Scope parameter rollout |
| [`sdd-060-at-rest-encryption`](sdd-060-at-rest-encryption/spec.md) | SDD-060 at-rest encryption |
| [`sdd-060-confidence-required`](sdd-060-confidence-required/spec.md) | SDD-060 required confidence (schema v17, `e8d227d`) |
| [`sdd-060-legacy-reencrypt`](sdd-060-legacy-reencrypt/spec.md) | SDD-060 legacy re-encryption |
| [`sdd-060-tenant-cli`](sdd-060-tenant-cli/spec.md) | SDD-060 tenant CLI |
| [`v1-readiness`](v1-readiness/spec.md) | v1 readiness engineering evidence |

The seven previously archived changes keep their proposal, design, task, apply-progress, verify-report, and archive-report artifacts in Git history. The in-flight fiscal change instead preserves its unique [design](../docs/decisions/fiscal-runtime-foundations-design.md), [proposal](../docs/decisions/fiscal-runtime-foundations-proposal.md), [task history](../docs/decisions/fiscal-runtime-foundations-task-history.md), and [verification history](../docs/decisions/fiscal-runtime-foundations-verification-history.md) as explicitly historical records. Its unfinished work is retained in ODD, not silently dropped.

Inside these files, mentions of `openspec/changes/...` paths and of the `openspec/config.yaml` verify order are historical provenance, kept verbatim. The verify order and conventions now live in [`AGENTS.md`](../AGENTS.md).

New requirements are added here as a change's feature document (`odd/tasks/`) is delivered.
