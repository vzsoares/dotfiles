---
title: Release Tooling — zen-release
category: architecture
updated: 2026-05-28
related: [overview]
---

# Release Tooling — zen-release

`scripts/release.py` (`zen-release`) is a single-file `uv` script (PEP 723 deps)
that drives an interactive, idempotent, **resumable** release. It's a thin
**orchestrator**: it shells out to external tools per phase and **skips any phase
whose tool isn't enabled** — there is **no fallback tool chain**. Works for
Python / Node / generic-tag-only repos, private or public, on GitHub or not.

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

### Branch resolution — the merge chain

A release flows up a **chain** of branches, target last: the branch you're on →
the configured source → the target. Each consecutive pair is one merge; the last
leg lands the release on the target. The chain is built per-run from where you
stand, so you're never forced onto another branch unless a merge needs it:

| You're on…                  | Source configured? | Chain                          |
| --------------------------- | ------------------ | ------------------------------ |
| a feature branch            | yes (`develop`)    | `feature → develop → master`   |
| a feature branch            | no                 | `feature → master`             |
| the target (`master`)       | yes (`develop`)    | `develop → master`             |
| the target (`master`)       | no                 | `master` — release in place    |

- **Release in place** (single-element chain) does **no merge and no checkout** —
  the tag/bump/push happen on the branch you're on.
- After the release lands on the target, the chain is **synced back down**
  (`rebase_source`): each lower branch is rebased onto the one above and pushed,
  so `develop` and the feature branch end aligned with the released `master`.
- `--source` / `--target` override the saved config for a single run (the branch
  you're on still leads the chain); `--target` defaults to the current branch.
- Every branch in the chain is validated up front; a missing one aborts pointing
  at it. `--reconfigure` re-runs the picker.

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
  `merge` walks every leg of the chain; `rebase_source` syncs them back down.
- **Dev mode** (`--dev`): bump to `-dev.N`, tag + push the current branch only.
  Stateless (no resume), since each step is idempotent.
- **Headless** (`--yes --bump <level>`): no gum prompts — confirms auto-accept,
  secret findings **abort**, no changelog editor. `--source`/`--target` override
  the saved branch config (else the chain is built from where you stand).

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

## See Also

- [[overview]] — the dotfiles environment this tooling lives in
- `scripts/README.md` — usage and command reference
- `BACKLOG.md` (repo root) — remaining future work
