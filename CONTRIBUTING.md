# Contributing to Drenyra Engram

**Status: alpha.** The Go engine is the supported runtime; the TypeScript package is a reference mirror kept in golden-vector parity. The maintainer (Arkelythex) drives the roadmap, and contracts in [`contracts/`](contracts/README.md) are consumed by Drenyra, Drenyra AI, and Drenyra Pi. Open an issue before starting anything larger than a typo or a documentation fix, so scope and contract impact are agreed first.

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md). Report security issues privately — see [SECURITY.md](SECURITY.md), not a public issue.

## Where to start

| You want to | Read |
| --- | --- |
| Understand the product and its boundaries | [README.md](README.md) · [docs/PRODUCT.md](docs/PRODUCT.md) |
| Find any document | [docs/README.md](docs/README.md) |
| Orient yourself in the code | [docs/CODEBASE-GUIDE.md](docs/CODEBASE-GUIDE.md) · [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) |
| Run it locally on synthetic data | [docs/GETTING-STARTED.md](docs/GETTING-STARTED.md) · [examples/README.md](examples/README.md) |
| Diagnose an error message | [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) |
| Check supported platforms and stability | [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md) |
| See what is planned or released | [ROADMAP.md](ROADMAP.md) · [CHANGELOG.md](CHANGELOG.md) · [RELEASING.md](RELEASING.md) |

## Accounting-safety rules

These are product safety requirements, not style preferences. A change that weakens one is rejected.

- **Memory never authorizes.** No code path may treat an observation as approval. Agents never get approval authority: approval requires a verified, session-derived professional identity, and surfaces without one fail closed.
- **Fail closed stays closed.** Do not loosen scope checks, runtime-mode gates (`DRENYRA_FISCAL_RUNTIME_MODE`), the closed-period gate, encryption-key checks, or token validation to make a test or demo pass. If a guard is wrong, change it explicitly and say so in the PR.
- **Scope is structural.** Company/RUC/period scope is part of the memory model and search — never a post-filter. Cross-tenant invisibility is a tested invariant.
- **Provenance is mandatory.** Every observation records who/what/when/why; forgeable provenance is a defect.
- **Integrity is not correctness.** Signatures and verification prove an act was not altered. Never word output, docs, or UI so that they imply the accounting decision is correct; verification reports end with `Accounting correctness: NOT ASSERTED`.
- **Synthetic data only.** Use fictional companies, invented identifiers, and the fixtures in [`examples/fixtures/`](examples/fixtures/). Never put real taxpayer data, real RUCs, credentials, tokens, or customer records in code, docs, tests, seeds, issues, or PRs. (A syntactically valid RUC checksum is not a reason to use a real one.)
- **Money is whole cents.** `int64` cents in Go, `BigInt` cents in TypeScript; never floats. Sequence and revision counters are JSON integers.
- **TypeScript: no `any`.** Use precise types, `unknown`, or justified generics.

## Your first contribution

### 1. Prerequisites

| Tool | Needed for | Version |
| --- | --- | --- |
| Go | The engine, CLI, MCP and HTTP servers, and all Go tests | 1.26 or later (`go.mod`: `go 1.26`) |
| Node.js | The TypeScript reference mirror and its tests only | 22 or later (`package.json` `engines`) |
| Bun | Running the mirror's scripts (`bun run …`); CI uses Bun 1.3.11 | Only for the TypeScript mirror |

If you only touch Go or documentation, you do not need Node or Bun.

### 2. Build and run the checks

```bash
git clone https://github.com/arkelythex/drenyra-engram.git
cd drenyra-engram

go build ./...
go vet ./...
gofmt -l internal/ cmd/      # must print nothing
go test ./... -count=1
```

`go test ./...` includes slow packages (`internal/store` is the longest). For a quick loop, run the package you changed, for example `go test ./internal/core -count=1`.

If you touch the TypeScript mirror or anything under `testdata/golden/`:

```bash
bun install --frozen-lockfile
bun run typecheck       # tsc --noEmit
bun run test            # vitest run
```

CI runs the same commands: `.github/workflows/go.yml` (format, build + vet, tests, race detector) and `.github/workflows/ci.yml` (typecheck, tests, package verification), plus a bounded fuzz gate (`make fuzz-ci`, `.github/workflows/fuzz.yml`). Before a release-affecting change to the TypeScript package, also run `bun run verify:package`.

> The race detector run is not part of the quick loop: `internal/store` takes several minutes under `-race`. Let CI run it, or run it for the package you changed.

### 3. Branch, commit, open a PR

1. Follow ODD as described in [AGENTS.md](AGENTS.md): substantial work gets a feature document under `odd/tasks/` before the first source write, and behavior changes are test-first.
2. Create a dedicated branch (or isolated worktree for medium or large changes) named with the change type, for example `fix/…`, `feat/…`, `docs/…`, `chore/…`. Keep `main` clean.
3. Prefer small, verifiable, reversible changes. Split changes over about 400 lines into chained PRs.
4. Use **Conventional Commits** (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`), with a scope when useful, for example `fix(store): …`. Never add AI attribution — no "Generated with" or `Co-Authored-By` AI markers.
5. Update docs in the same PR as the code (docs-as-code). Stale docs are a bug. Add a line under `## [Unreleased]` in [CHANGELOG.md](CHANGELOG.md) for any user-visible change.
6. Add tests for changed behavior — scope-first search, lifecycle transitions, sync semantics, relation judgments. Write the failing test first when the behavior is testable.
7. Fill in the [pull request template](.github/PULL_REQUEST_TEMPLATE.md).

The review gate rejects: silent error handling, production `console.log`, missing scope checks, missing provenance, missing tests, and contract changes without docs.

Issues use the templates in [`.github/ISSUE_TEMPLATE/`](.github/ISSUE_TEMPLATE/). For fiscal behavior, always include scope and period (synthetic values only).

## Contract changes

Everything in [`contracts/`](contracts/README.md) is a public contract. The contracts are transport-independent: they describe behavior, not an HTTP route, an MCP tool, or a CLI flag. A contract change is high-materiality and must move together:

1. **Contract text** — bump the affected contract's version explicitly in its header and in the index table in `contracts/README.md`, and document the migration path.
2. **Go** — the implementation under `internal/` (and its tests).
3. **TypeScript mirror** — the matching code under `core/`, `store/`, `authz/`, `lifecycle/` (and its tests under `__tests__/`).
4. **Golden vectors** — update or add the shared vector under `testdata/golden/` so both languages assert the same bytes and outcomes.
5. **Docs** — `CHANGELOG.md` `[Unreleased]` entry, plus any guide that quotes the behavior (error codes, schema versions, tool counts).
6. **Approval** — get explicit maintainer approval before merging; consumers (Drenyra, Drenyra AI, Drenyra Pi) depend on these contracts.

Do not change a frozen contract's meaning in place. Add a new version or a new contract and say how existing data migrates.

### Golden vectors

`testdata/golden/*.json` are shared Go↔TypeScript vectors. Each file declares a `contract` discriminator (for example `approval-policy`, `receipt`, `topic-fold`, `fiscal-scope-v1`); Go dispatches on it in `TestGoldenVectorsGo`, and the TypeScript side reads the same files in `core/__tests__/golden.test.ts` (plus focused tests such as `core/__tests__/fiscal-scope.test.ts`). The same file must pass in both languages.

There is no generator or `-update` flag in this repository: vectors are reviewed JSON, edited on purpose. To change one, edit the file, then verify both sides:

```bash
go test ./internal/core -run TestGoldenVectorsGo -count=1
bun run test core/__tests__/golden.test.ts
```

Never "fix" a failing vector by regenerating expected values from one implementation; decide which side is wrong and fix that side. A new `contract` value needs a `case` in `TestGoldenVectorsGo`, otherwise it fails with `unknown golden contract`.

On Node.js 26 the vectors `memory-rejected-reason-payload-v10`, `memory-returned-receipt-v10`, and `receipt-ed25519-v1` fail in `golden.test.ts` with an Ed25519 JWK import error (observed on Node v26.7.0, 2026-10-28; the `package` CI job pins Node 22). Run them on Node 22 before concluding a change broke them.

## Documentation checks

Documentation changes are verified like code.

**Examples run.** Build the binary and run the whole example set; it uses a temporary workspace and a synthetic fixture and cleans up after itself:

```bash
bin_dir="$(mktemp -d)"
go build -o "$bin_dir/drenyra-engram" ./cmd/drenyra-engram
DRENYRA_ENGRAM_BIN="$bin_dir/drenyra-engram" bash examples/run-all.sh
```

The Docker example is excluded from `run-all.sh`; see [`examples/docker/README.md`](examples/docker/README.md).

**Links resolve.** There is no link-check script in the repository. This is a minimal relative-link check over the files you changed (replace the file list):

```bash
python3 -I - CHANGELOG.md ROADMAP.md <<'PY'
import pathlib, re, sys
for name in sys.argv[1:]:
    base = pathlib.Path(name).parent
    text = pathlib.Path(name).read_text(encoding="utf-8")
    for target in re.findall(r"\]\(([^)#\s]+)", text):
        if "://" in target or target.startswith("mailto:"):
            continue
        if not (base / target).exists():
            print(f"{name}: missing {target}")
PY
```

**Whitespace.** `git diff --check` prints nothing.

**Claims match source.** Command names, flags, environment variables, error messages, tool counts, and schema versions come from the code, not from older docs. When docs and source disagree, source wins; fix the doc.

## Getting help

Open an issue with a clear description, using synthetic data only. For security issues, use Private Vulnerability Reporting — see [SECURITY.md](SECURITY.md).
