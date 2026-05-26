# BACKLOG — Release tool rewrite (`scripts/release.py`)

Replace `scripts/release.sh` with a richer, resumable release orchestrator. Keep
everything the bash script does today, then add: hook gates, secret scanning,
changelog generation, package publish, and GitHub release — all **optional and
auto-skipped** when the repo doesn't publish, isn't on GitHub, or lacks a tool.

## Goal

A single-file Python script (`uv run scripts/release.py`) that drives an
interactive, **idempotent, resumable** release. The script is a thin
**orchestrator**: it shells out to best-in-class external tools per phase and
falls back gracefully when a tool is missing. It must work for repos that are
Python, Node, generic-tag-only, private (no publish), and/or not on GitHub.

## Decisions (locked)

- **Language:** Python, single file, [PEP 723](https://peps.python.org/pep-0723/)
  inline deps, run with `uv run scripts/release.py`. No build step, edit-and-run
  like the current bash scripts.
- **Inline deps (kept minimal):** `tomlkit` (format-preserving `pyproject.toml`
  edits), `typer` (flags: `--resume`, `--restart`, `--dry-run`, `--yes`).
  Display/TUI is **not** a Python dep — we shell out to `gum`.
- **Interaction:** shell out to `gum` (`choose`/`input`/`confirm`/`spin`),
  matching the look of the current scripts.
- **External tools are fair game** — orchestrate, don't reimplement. Each phase
  has: a primary external tool, a fallback, and a skip condition.

## Tooling strategy (detect → use → fall back → skip)

| Phase            | Primary external tool                                                              | Fallback                                  | Skip when                          |
| ---------------- | ---------------------------------------------------------------------------------- | ----------------------------------------- | ---------------------------------- |
| Prompts/TUI      | `gum`                                                                               | — (hard require)                          | never (abort if missing)           |
| Secret scan      | `gitleaks detect`                                                                   | warn-only                                 | `--no-scan` flag                   |
| Hook gates       | `pre-commit` / `lefthook` / native `.git/hooks/*`                                   | run native hook scripts directly          | no hooks configured                |
| Version decision | [`go-semantic-release`](https://github.com/go-semantic-release/semantic-release) (auto, from conventional commits) | interactive `gum choose` bump | always offer manual                |
| Changelog        | [`git-cliff`] → [`hashicorp/go-changelog`](https://github.com/hashicorp/go-changelog) → semantic-release | grouped `git log` since last tag | `--no-changelog`        |
| Publish + GH release | [`goreleaser`](https://goreleaser.com/getting-started/intro/) (`.goreleaser.yaml` present) | `gh release create` + `npm`/`uv publish` | not publishable / not on GitHub |

Notes:
- **goreleaser** can subsume changelog + GitHub release + publish in one
  `goreleaser release` call — prefer it when `.goreleaser.yaml` exists (mainly Go
  repos). Otherwise compose the discrete tools below.
- **go-semantic-release** is offered as an *auto* bump choice alongside the
  existing manual finalize/patch/minor/major options — only when the binary is
  present.
- Missing tools never hard-fail (except `gum`, `git`, `uv`): the phase prints how
  to install (`mise use`, `go install`, `uv tool install`, `pipx`) and is skipped
  or downgraded to fallback.

## Capability detection (run once at startup, store in state)

- `is_github` — `git remote get-url origin` host is `github.com` **and** `gh` is
  authenticated (`gh auth status`).
- `publishable` — Node: `package.json` has `name` and **not** `"private": true`;
  Python: `pyproject.toml` has a `[build-system]`; else not publishable.
- `project_type` — python / node / go / generic (tag-only).
- Tool presence map for every external tool in the table above.
- `hook_manager` — `pre-commit` (`.pre-commit-config.yaml`) / `lefthook`
  (`lefthook.yml`) / `native` (executable `.git/hooks/pre-commit|pre-push`) / none.

## Run model & dependencies

- File: `scripts/release.py`, executable, with `uv` shebang + PEP 723 block.
- Invoke: `uv run scripts/release.py [--resume|--restart] [--dry-run] [--yes]`.
- Add zsh alias (e.g. `release`) in `zsh/` after parity is verified.
- Preflight hard requires: `uv` (implicit), `git` repo, `gum`. Everything else is
  detected.

## State & resume design

- State file: `.git/release-state.json` (repo-local, never committed, outside the
  worktree so it can't dirty the tree). Mirror the existing `tmp/NN-state`
  convention but scoped to `.git/`.
- Contents: schema version, timestamp, chosen `version`, `source`/`target`
  branches, `no_merge`, capability map, per-phase status
  (`pending|done|failed`), and any phase outputs (e.g. generated changelog
  section, computed tag).
- On startup, if a state file exists → `gum choose`: **Resume / Restart / Abort**
  (also `--resume`/`--restart` to skip the prompt).
- Each phase is wrapped: mark `started` → run → mark `done`; on exception, persist
  state, print the **exact manual command** to recover plus
  `uv run scripts/release.py --resume`, then exit non-zero.
- Delete the state file only on full success.

## Phases (the work)

> Each phase must be **idempotent** so `--resume` is safe after a mid-phase crash.

### P0 — Scaffolding & preflight
- [ ] `release.py` with uv shebang, PEP 723 deps, `typer` CLI, `gum` wrappers
      (`choose/input/confirm/spin`, `--dry-run` echoes instead of running).
- [ ] Hard-require checks (git repo, clean tree, `gum`); capability detection.
- **Done when:** running with a missing tool prints an install hint and continues;
      dirty tree aborts with the current message.

### P1 — State machine & resume
- [ ] Phase registry, `.git/release-state.json` read/write, Resume/Restart/Abort.
- [ ] Recovery message format (manual command + `--resume`).
- **Done when:** killing the script mid-run and re-running resumes at the failed
      phase without redoing completed ones.

### P2 — Hook gates (run pre-commit & pre-push *before* releasing)
- [ ] Detect `hook_manager`; run the **pre-commit** stage then the **pre-push**
      stage explicitly:
      `pre-commit run --all-files` / `lefthook run pre-commit` & `pre-push` /
      execute native `.git/hooks/pre-commit` & `pre-push` directly.
- [ ] Failure aborts before any branch mutation; `--yes` does **not** bypass.
- **Done when:** a failing hook stops the release with clear output; no hooks → skip.

### P3 — Secret scan
- [ ] `gitleaks detect --redact` over the repo (or staged/range). On findings show
      report and abort; allow explicit `gum confirm` override.
- **Done when:** a planted secret blocks the release; clean repo passes; `--no-scan`
      or missing `gitleaks` downgrades to a warning.

### P4 — Branch selection & version decision (parity + auto)
- [ ] Port branch selection verbatim (source→target merge, or release current with
      `no_merge`), incl. existence validation.
- [ ] Port version detection (`pyproject.toml` → `package.json` → latest `v*` tag →
      `0.0.0`) and semver `-dev.N` parsing.
- [ ] Bump menu: keep `finalize/patch/minor/major`; **add an `auto` option** that
      runs `go-semantic-release` to compute the next version when present.
- **Done when:** behavior matches `release.sh` for all current cases; `auto` only
      appears when the tool exists.

### P5 — Merge
- [ ] Port: checkout target, pull, `merge --no-ff -m "Release version X"` (only when
      `no_merge` is false).
- **Done when:** resume after a merge conflict is safe (detect in-progress merge).

### P6 — Version file writes + commit
- [ ] `package.json` via JSON edit; `pyproject.toml` via **tomlkit** (preserve
      formatting); `config.py`/`settings.py` `VERSION: str = "…"` via regex (port).
- [ ] Stage changed files, `chore: bump version to X` commit.
- **Done when:** identical files to the bash version, but `pyproject.toml`
      formatting/comments are preserved.

### P7 — Changelog
- [ ] Generate/prepend a `CHANGELOG.md` section for the new version:
      `git-cliff` → `go-changelog` → semantic-release → fallback grouped `git log`
      since previous tag (Conventional-Commit headings).
- [ ] Offer to edit (`gum` / `$EDITOR`); commit (amend into the bump commit or
      separate, configurable). Persist the section text in state for the tag/GH
      release bodies.
- **Done when:** `--no-changelog` skips cleanly; no tool present still yields a
      reasonable changelog from git log.

### P8 — Tag
- [ ] Annotated `vX.Y.Z` tag using the changelog section as the message.
- [ ] Idempotent: if the tag already exists at HEAD, continue; if it exists
      elsewhere, abort with guidance.
- **Done when:** resume never errors on an already-created tag.

### P9 — Push
- [ ] Port confirm + `git push origin <target> --follow-tags`, with the existing
      "push failed → here's the manual command" recovery.
- **Done when:** matches current behavior; idempotent on re-push.

### P10 — Publish (optional)
- [ ] If `publishable` and confirmed: `goreleaser release` (when configured) OR
      Node `npm publish` / Python `uv build && uv publish` (or twine).
- [ ] Idempotent: check the registry for the version before publishing; skip if
      already present.
- **Done when:** private/no-build repos skip silently with a summary note.

### P11 — GitHub release (optional)
- [ ] If `is_github` and confirmed: `gh release create vX.Y.Z` with notes from the
      P7 changelog section (or `--notes-from-tag`); attach goreleaser artifacts when
      applicable.
- [ ] Idempotent: skip/update if the release already exists.
- **Done when:** non-GitHub remotes or missing/unauthenticated `gh` skip silently.

### P12 — Rebase source branch back + push
- [ ] Port: when not `no_merge`, offer checkout source, `rebase <target>`, then
      optional push, with the existing recovery messaging.
- **Done when:** matches current behavior; resume-safe across rebase conflicts.

### P13 — Finalize
- [ ] Summary panel (what ran / skipped / published / release URL), delete state
      file on success.

### P14 — Quality, tests, migration, docs
- [ ] `ruff`, `ruff format`, `mypy` clean (per global quality rules; no `as`/`Any`).
- [ ] Smoke test against a throwaway temp git repo covering: tag-only, Node,
      Python, no-merge, not-GitHub, not-publishable, and a forced mid-phase
      crash → `--resume`.
- [ ] Keep `release.sh` until parity is verified, then remove (or thin to a shim
      calling the Python tool). Decide whether to fold `release-dev.sh` in as a
      `--dev` mode.
- [ ] Update `CLAUDE.md` (scripts section) and `README.md`; add the zsh alias.

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
