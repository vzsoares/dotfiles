---
title: Release Tooling — zen-release
category: architecture
updated: 2026-11-10
related: [overview, decision-inbox]
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
branches (pre-filled `dev`/`prod` when both exist, else empty; a headless run with
no saved config uses the same defaults unless `--source`/`--target` is given), then
a gum checklist of the optional phases (pre-checked by detection).

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
  Stateless (no resume), since each step is idempotent. Refused (exit 1, no tag,
  no push) on `prod`, `main`, `master` and the saved target branch.
- **Headless** (`--yes --bump <level>`): no gum prompts — confirms auto-accept,
  secret findings **abort**, no changelog editor. `--source`/`--target` override
  the saved branch config (else the chain is built from where you stand).

## Branch models

Repos follow one of two models (zen-flow skill, Branch model section):

- **dev + prod**: workers land on `dev` and `zen-merge` cuts a `--dev` release
  there; `prod` moves only through a full release (`dev → prod`).
- **single-branch** (`main` or `master` only): `zen-merge` lands locally with no
  release and reports `push pending`.

Every push, tag, release or deploy on `prod`/`main`/`master` needs the user's
approval for that one action, filed as a `zen-inbox ask`. `zen-sweep` reports
`branch-model` drift in personal repos.

## Prod approval gate (#680)

A full release (no `--dev`) always tags and pushes its target, so zen-release fails
closed on it. Right after branch resolution, before hooks or any mutating phase:

- **When it is needed.** With `--yes`, with `AGENT_MANAGER_SESSION_ID` set, or with a
  non-TTY stdin, a release without `--approval <id>` exits **3** and prints the ask
  to file: `zen-inbox ask <task> --prod release --repo R --branch B --sha S`. Only a
  TTY run in your own terminal may skip it, through a default-No `gum confirm`
  showing repo, branch and sha12.
- **Binding.** repo = basename of the main checkout (parent of `git rev-parse
  --git-common-dir`, so a worktree reports its repo); branch = the resolved target;
  sha = the full tip of the chain's first branch (`dev` for `dev → prod`, the target
  itself in place). zen-release runs `zen-inbox approval use <id> --action release
  ...`, which checks and consumes in one `BEGIN IMMEDIATE` transaction. Any non-zero
  exit, a missing binary included, becomes exit 3 with zen-inbox's reason (`not
  answered`, `answered Hold`, `already used by … at …`, `sha mismatch: approved a,
  releasing b`, …). A mismatch leaves the approval unused.
- **Resume.** The consumed id is saved as `approval_id` in
  `.git/release-state.json`; `--resume` needs nothing new. A state without it
  follows the rules above again; `--restart` clears it and needs a fresh approval.
- **Dry run** never consumes and never refuses: it runs `approval check` when given
  an id, else prints that a real run needs one.
- **Exit codes.** 3 = approval missing, rejected or used; every other failure stays
  1. `--dev` is unchanged and needs no approval, so `zen-merge` is unaffected.
- **Not covered.** `git push` of single-branch repos (`zen-merge`'s `push pending`),
  deploys, and an agent that edits `zen-flow.db` or the state file by hand.

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
