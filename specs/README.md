# specs/

Requirement records for Drenyra Engram.

Each directory holds the `spec.md` of one change delivered under the retired SDD/OpenSpec workflow. The files moved here verbatim from `openspec/changes/<change>/spec.md`. Under OpenSpec this repository never kept a separate canonical spec layer, so these files are the requirements of record.

| Spec | Delivered |
| --- | --- |
| [`audit-register-closure`](audit-register-closure/spec.md) | Audit register closure |
| [`scope-param-rollout`](scope-param-rollout/spec.md) | Scope parameter rollout |
| [`sdd-060-at-rest-encryption`](sdd-060-at-rest-encryption/spec.md) | SDD-060 at-rest encryption |
| [`sdd-060-confidence-required`](sdd-060-confidence-required/spec.md) | SDD-060 required confidence (schema v17, `e8d227d`) |
| [`sdd-060-legacy-reencrypt`](sdd-060-legacy-reencrypt/spec.md) | SDD-060 legacy re-encryption |
| [`sdd-060-tenant-cli`](sdd-060-tenant-cli/spec.md) | SDD-060 tenant CLI |
| [`v1-readiness`](v1-readiness/spec.md) | v1 readiness engineering evidence |

The proposal, design, task, apply-progress, verify-report, and archive-report artifacts of these changes are not kept in the tree. They remain in git history before the migration commit.

Inside these files, mentions of `openspec/changes/...` paths and of the `openspec/config.yaml` verify order are historical provenance, kept verbatim. The verify order and conventions now live in [`AGENTS.md`](../AGENTS.md).

New requirements are added here as a change's feature document (`odd/tasks/`) is delivered.
