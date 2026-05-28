# zen-release — design (`scripts/release.py`)

A single-file `uv` script (PEP 723 deps) that drives an interactive, idempotent,
**resumable** release. It's a thin **orchestrator**: it shells out to external
tools per phase and **skips any phase whose tool isn't enabled** — there is **no
fallback tool chain**. Works for Python / Node / generic-tag-only repos, private
or public, on GitHub or not.

- Display/TUI: shell out to `gum` (`choose`/`input`/`confirm`/`spin`).
- Format-preserving `pyproject.toml` edits via `tomlkit`; CLI via `typer`.

## Two config tiers

**Global** — `~/.config/zen-release/config.json` (respects `$XDG_CONFIG_HOME`),
written by `zen-release setup`. *What tools you have.* Detects + version-checks
every external tool once and saves `{ tool: {enabled, path, version} }`. `git` +
`gum` are **required** (setup aborts without them, saving nothing); everything
else is optional and recorded `enabled: false` when absent, with an install hint.
Re-run `setup` to heal it.

**Per-repo** — `.git/zen-release.json`, **uncommitted** (sits next to the state
file). *What this repo wants done.* Created on first run: asks for source/target
branches, then a gum checklist of the optional phases (pre-checked by detection).
Empty source = release the current branch (no merge). `--reconfigure` re-runs the
picker; a configured branch that no longer exists aborts pointing at it.

## Tooling (enabled → use, else → skip)

| Phase            | Tool                                                          | Skip when                                   |
| ---------------- | ------------------------------------------------------------- | ------------------------------------------- |
| Prompts/TUI      | `gum`                                                         | never — hard require                        |
| Secret scan      | `gitleaks detect`                                             | not enabled, or `--no-scan`                 |
| Hook gates       | `pre-commit` / `lefthook` / native `.git/hooks/*`             | no hooks in the repo                        |
| Version decision | `semantic-release` (auto bump)                                | not enabled — manual menu always offered    |
| Changelog        | `git-cliff` (built-in grouped `git log` otherwise)            | `--no-changelog`                            |
| Publish          | Node `npm publish` / Python `uv build && uv publish`          | not publishable                             |
| GitHub release   | `gh release create` (notes from changelog)                    | not on GitHub / `gh` unauthenticated        |

- Publishing is by `project_type`. Go modules publish by pushing a tag (already
  done in tag/push), so there's no separate publish step.
- A tool that isn't enabled never hard-fails — its phase is skipped with a notice
  pointing back to `setup`.

## Capability detection

- **Global (setup):** tool presence/version map.
- **Per-run (cheap, stored in state):** `is_github` (origin is github.com **and**
  `gh` authed), `publishable` (Node `name` & not `private`; Python `[build-system]`),
  `project_type` (python/node/go/generic), `hook_manager`.

## Phase pipeline (each idempotent → `--resume` is safe)

```
hooks → secret_scan → branch+version → merge → write_version → changelog
→ tag → push → publish → github_release → rebase_source
```

- **Optional / repo-toggleable:** hooks, secret_scan, changelog, publish, github_release.
- **Core / always run:** merge, write_version, tag, push, rebase_source — gated by `no_merge`.
- **Dev mode** (`--dev`): bump to `-dev.N`, tag + push the current branch only.
  Stateless (no resume), since each step is idempotent.
- **Headless** (`--yes --bump <level>`): no gum prompts — confirms auto-accept,
  secret findings **abort**, no changelog editor. `--source`/`--target` supply
  branches when the repo has no saved config.

## State & resume

- `.git/release-state.json` (repo-local, never committed): version, branches,
  `no_merge`, per-run capability map, per-phase status (`pending|done|failed`),
  phase outputs (changelog section, published target, release URL).
- On startup with existing state → Resume / Restart / Abort (or `--resume` /
  `--restart`). Deleted on full success; on failure, persists + prints the manual
  recovery command plus `zen-release --resume`.
- Idempotency: detect `MERGE_HEAD` before re-merging; skip bump commit if HEAD
  already has it; skip tag if already at HEAD; re-push is a no-op; query the
  registry before publish; `gh release view` before create.

## Status

Feature-complete and tested (release + commit suites; ruff/format/mypy clean).
GitHub release validated live. Remaining:

- [ ] Live smoke test of a real `npm` / `uv` publish (only mocked so far) — pair
      with the next real package release.

## Out of scope (v1)

- Monorepo / multiple packages per repo.
- Signing tags/artifacts (GPG, cosign).
