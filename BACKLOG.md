# BACKLOG — Release tool rewrite (`scripts/release.py`)

Replace `scripts/release.sh` with a richer, resumable release orchestrator. Keep
everything the bash script does today, then add: hook gates, secret scanning,
changelog generation, package publish, and GitHub release — all **optional and
auto-skipped** when the repo doesn't publish, isn't on GitHub, or the tool isn't
enabled in the user's saved config.

## Goal

A single-file Python script (`uv run scripts/release.py`) that drives an
interactive, **idempotent, resumable** release. The script is a thin
**orchestrator**: it shells out to best-in-class external tools per phase. A
one-time `setup` command detects which tools are installed and saves the result
to a global config; at release time the script reads that config and **skips any
phase whose tool isn't enabled** — there is **no fallback tool chain**. It must
work for repos that are Python, Node, generic-tag-only, private (no publish),
and/or not on GitHub.

## Decisions (locked)

- **Language:** Python, single file, [PEP 723](https://peps.python.org/pep-0723/)
  inline deps, run with `uv run scripts/release.py`. No build step, edit-and-run
  like the current bash scripts.
- **Inline deps (kept minimal):** `tomlkit` (format-preserving `pyproject.toml`
  edits), `typer` (subcommands: `setup`, `release` + flags `--resume`,
  `--restart`, `--dry-run`, `--yes`). Display/TUI is **not** a Python dep — we
  shell out to `gum`.
- **Interaction:** shell out to `gum` (`choose`/`input`/`confirm`/`spin`),
  matching the look of the current scripts.
- **No fallbacks.** Each phase has exactly one primary external tool and a skip
  condition. If the tool isn't enabled in config (or exits non-zero at runtime),
  the phase is **skipped with a notice** — we never silently substitute a second
  tool. The only exception is the changelog's built-in `git log` rendering, which
  is the tool's *own* behavior, not a separate binary.
- **One-time setup, persisted config.** `release.py setup` detects installed
  tools once and writes a **global** config. Release runs read that config
  instead of re-probing every binary on every run. Re-run `setup` whenever the
  toolchain changes — that is the recovery path.

## Setup command & config (`zen-release`)

- **Command:** `uv run scripts/release.py setup`. Detects every external tool in
  the table below, verifies each actually runs (e.g. `gum --version`,
  `gitleaks version`), and writes the result.
- **Required vs optional:** each tool is **required** or **optional**. `setup`
  only succeeds when **all required tools** (`git`, `gum`) are present — a missing
  required tool aborts with a non-zero exit and **no config is written**. Optional
  tools may be missing: they're recorded `enabled: false` and their phase is
  skipped at release time.
- **Config path:** `~/.config/zen-release/config.json` (respects
  `$XDG_CONFIG_HOME`). Global / machine-wide, **not** per-repo. Unique name
  `zen-release` to avoid clashes.
- **Config contents:** schema version, timestamp, and a tool map
  `{ tool: { enabled: bool, path: str, version: str } }`. `enabled` is true only
  when the tool was found **and** its version probe succeeded.
- **Recovery (setup-time):** required gaps abort with install hints and save
  nothing. For each missing *optional* tool, print the exact install hint
  (`mise use`, `go install`, `uv tool install`); the tool is recorded
  `enabled: false` and the rest still save. Re-running `setup` re-probes and heals
  the config.
- **Recovery (release-time):** if no config exists, the release aborts and tells
  the user to run `setup` first. If a tool marked `enabled` in config is missing
  or fails at runtime, that phase is skipped with a warning that points back to
  `setup` — the release does **not** hard-fail.

## Per-repo config (`.git/zen-release.json`)

A second, repo-local tier. The global config says *what tools you have*; the
per-repo config says *what this repo wants done*.

- **Path:** `.git/zen-release.json` — **uncommitted** (lives in `.git/`, per-clone,
  sits next to `release-state.json`). Never dirties the working tree.
- **Created on first run:** the first release in a repo (with no per-repo config)
  runs an interactive setup — asks for the **source** and **target** branches
  (develop → production), then a **gum multi-select checklist** of the optional
  phases, pre-checked by detection. The answers are saved; later runs don't re-ask.
- **Contents:** schema, `branches: {source, target}` (empty `source` = release the
  current branch, no merge), and `phases: {name: bool}` for each optional phase
  (`hooks`, `secret_scan`, `changelog`, `publish`, `github_release`).
- **Effect:** an optional phase whose flag is `false` is skipped with a notice;
  core phases (merge, write_version, tag, push, rebase_source) always run, gated
  only by `no_merge`. Branch selection reads from this config — no per-run prompt.
- **Reconfigure:** `--reconfigure` re-runs the first-run picker to change branches
  or phase toggles. A configured branch that no longer exists aborts with a hint
  to `--reconfigure`.

## Tooling strategy (config-driven; enabled → use, else → skip)

| Phase                | Primary external tool                                                              | Skip when                                              |
| -------------------- | ---------------------------------------------------------------------------------- | ------------------------------------------------------ |
| Prompts/TUI          | `gum`                                                                               | never — hard require (abort if missing)                |
| Secret scan          | `gitleaks detect`                                                                   | not enabled in config, or `--no-scan` flag             |
| Hook gates           | `pre-commit` / `lefthook` / native `.git/hooks/*`                                   | no hooks configured in the repo                        |
| Version decision     | [`go-semantic-release`](https://github.com/go-semantic-release/semantic-release) (auto bump) | not enabled — manual bump menu is always offered |
| Changelog            | [`git-cliff`](https://github.com/orhun/git-cliff) (built-in `git log` rendering if no config file) | `--no-changelog` flag                  |
| Publish              | Node `npm publish` / Python `uv build && uv publish`                                | not publishable                                        |
| GitHub release       | `gh release create` with notes from the changelog                                   | not on GitHub / `gh` not authenticated                 |

Notes:
- **Publishing is by `project_type`:** Node → `npm publish`, Python →
  `uv build && uv publish`. Go modules publish by pushing a tag (already done in
  the tag/push phases), so there's no separate publish step for them.
- **go-semantic-release** is offered as an *auto* bump choice alongside the
  manual finalize/patch/minor/major options — only when enabled in config.
- A tool that isn't enabled never hard-fails the run: its phase is skipped with a
  one-line notice and a pointer to `setup`.

## Capability detection (split: global config vs. per-run)

- **Saved once by `setup` (global config):** the tool presence/version map for
  every external tool in the table (`git`, `gum`, `gitleaks`, `gh`, `git-cliff`,
  `go-semantic-release`, `pre-commit`, `lefthook`).
- **Detected fresh each run (per-repo, cheap, stored in state):**
  - `is_github` — `git remote get-url origin` host is `github.com` **and** `gh`
    is authenticated (`gh auth status`).
  - `publishable` — Node: `package.json` has `name` and **not** `"private": true`;
    Python: `pyproject.toml` has a `[build-system]`; else not publishable.
  - `project_type` — python / node / go / generic (tag-only).
  - `hook_manager` — `pre-commit` (`.pre-commit-config.yaml`) / `lefthook`
    (`lefthook.yml`) / `native` (executable `.git/hooks/pre-commit|pre-push`) / none.

## Run model & dependencies

- File: `scripts/release.py`, executable, with `uv` shebang + PEP 723 block.
- Invoke: `uv run scripts/release.py setup` (once) then
  `uv run scripts/release.py [--resume|--restart] [--dry-run] [--reconfigure]`.
- **Dev mode:** `uv run scripts/release.py --dev` — a lightweight release that
  bumps to a `-dev.N` version, tags, and pushes the **current** branch (no merge,
  changelog, publish, GitHub release, or gates). Parity with the old
  `release-dev.sh`. Exposed via the `run.sh` alias `release-dev` (so
  `run release-dev`).
- Add zsh alias (e.g. `release`) in `zsh/` after parity is verified.
- Preflight hard requires: `uv` (implicit), `git` repo, `gum`, and an existing
  `zen-release` config (else: "run setup first").

## State & resume design

- State file: `.git/release-state.json` (repo-local, never committed, outside the
  worktree so it can't dirty the tree).
- Contents: schema version, timestamp, chosen `version`, `source`/`target`
  branches, `no_merge`, per-repo capability map (the per-run set above), per-phase
  status (`pending|done|failed`), and any phase outputs (e.g. generated changelog
  section, computed tag).
- On startup, if a state file exists → `gum choose`: **Resume / Restart / Abort**
  (also `--resume`/`--restart` to skip the prompt).
- Each phase is wrapped: mark `started` → run → mark `done`; on exception, persist
  state, print the **exact manual command** to recover plus
  `uv run scripts/release.py --resume`, then exit non-zero.
- Delete the state file only on full success.

## Phases (the work)

> Each phase must be **idempotent** so `--resume` is safe after a mid-phase crash.

### P-setup — `setup` subcommand ✅
- [x] `release.py setup`: probe + version-check every external tool, print
      per-tool status with install hints for failures, write
      `~/.config/zen-release/config.json`. Required tools (`git`, `gum`) missing
      → abort, save nothing.
- **Done:** verified live + unit tests (`missing_required`, probe, config save).

### P0 — Scaffolding & preflight ✅
- [x] `release.py` with uv shebang, PEP 723 deps, `typer` CLI (`setup` + default
      `release`), `gum` wrappers (`choose/input/confirm/spin`, `--dry-run` echoes).
- [x] Hard-require checks (git repo, clean tree, `gum`, config exists); load the
      saved tool map. (Refactored into `preflight()`.)

### P1 — State machine & resume ✅
- [x] `.git/release-state.json` read/write, Resume/Restart/Abort, per-phase status.
- [x] Recovery message format (manual command + `--resume`).
- **Done:** `test_e2e_resume_after_crash` proves resume skips completed phases.

### P2 — Hook gates ✅
- [x] Detect `hook_manager`; run pre-commit then pre-push stages
      (`pre-commit` / `lefthook` / native hooks). Failure aborts before any branch
      mutation. (`--yes` was dropped from the design — no bypass exists.)

### P3 — Secret scan ✅
- [x] `gitleaks detect --redact` with `gum confirm` override on findings;
      `--no-scan` / gitleaks-disabled skips with a notice.

### P4 — Branch selection & version decision ✅
- [x] Branch selection now comes from the **per-repo config** (source→target, or
      empty source = release current), with existence validation
      (`apply_branches_from_repo`) — supersedes the verbatim prompt.
- [x] Version detection (`pyproject.toml` → `package.json` → latest `v*` tag →
      `0.0.0`) + semver `-dev.N` parsing.
- [x] Bump menu `finalize/patch/minor/major` + `auto` (only when
      semantic-release enabled).

### P5 — Merge ✅
- [x] Checkout target, pull, `merge --no-ff -m "Release version X"`; detects an
      in-progress merge before re-merging.

### P6 — Version file writes + commit ✅
- [x] `package.json` (JSON), `pyproject.toml` (**tomlkit**, formatting preserved),
      `config.py`/`settings.py` `VERSION: str = "…"`; `chore: bump version to X`
      commit, skipped if already committed. (`test_write_version_preserves_toml`.)

### P7 — Changelog ⚠️
- [x] Generate/prepend `CHANGELOG.md` via `git-cliff` when enabled, else built-in
      grouped `git log`. Persists the section in state for tag/GH bodies.
- [x] Offer to edit (`gum confirm` → `$EDITOR`).
- [ ] **TODO:** commit is always *separate*; the "amend into the bump commit,
      configurable" flag isn't implemented.

### P8 — Tag ✅
- [x] Annotated `vX.Y.Z` (changelog section as message; `Dev release` for `--dev`).
- [x] Idempotent: continue if tag already at HEAD; abort if it exists elsewhere.
      (`test_phase_tag_idempotent`, `test_phase_tag_conflict_aborts`.)

### P9 — Push ✅
- [x] Confirm + `git push origin <target> --follow-tags`, with manual-command
      recovery on failure.

### P10 — Publish (optional) ✅
- [x] Node `npm publish` / Python `uv build && uv publish`, selected by
      `project_type`. Not-publishable skips with a notice.
- [x] Idempotent: `already_published()` checks the registry (npm view / PyPI JSON)
      and skips if the version is already present.

### P11 — GitHub release (optional) ✅
- [x] `gh release create vX.Y.Z` with notes from the changelog (or
      `--notes-from-tag`); idempotent via `gh release view`; skips off-GitHub.

### P12 — Rebase source branch back + push ✅
- [x] When not `no_merge`: checkout source, `rebase <target>`, optional push, with
      recovery messaging.

### P13 — Finalize ⚠️
- [x] Summary panel (ran / skipped) + delete state file on success.
- [ ] **TODO:** panel doesn't yet list *published targets / release URL*.

### P14 — Quality, tests, migration, docs ⚠️
- [x] `ruff`, `ruff format`, `mypy` clean (no `as`/`Any`).
- [x] Test suite (59) incl. e2e: tag-only, Node, Python, no-merge, merge,
      not-GitHub, not-publishable, missing config, mid-phase crash → `--resume`,
      `--dev`, disabled phases, first-run config, publish-idempotency.
- [ ] **TODO:** live smoke test of an actual `npm`/`uv` publish and a real GitHub
      release (only mocked so far).
- [x] `release.sh` and `release-dev.sh` removed; `--dev` folded in
      (`run release-dev` alias + zsh `release` / `release-dev` aliases).
- [x] `CLAUDE.md` + `README.md` updated with a Release-tool section; zsh aliases
      added (`release`, `release-dev`).

## Idempotency cheat-sheet (for resume correctness)

- Merge: detect `MERGE_HEAD` / in-progress merge before re-merging.
- Version commit: skip if HEAD already contains the bump.
- Tag: skip if `vX.Y.Z` already points at HEAD.
- Push: re-push is a no-op; treat "up to date" as success.
- Publish: query registry for the version first.
- GH release: `gh release view` before create.

## Open questions / out of scope

- Monorepo / multiple packages per repo — out of scope for v1.
- Signing tags/artifacts (GPG, cosign) — out of scope for v1.
- Whether changelog commit should amend the version bump or be separate
  (default: separate; make it a flag).
