# scripts

Utility scripts for this dotfiles repo. The Python scripts are
[`uv`](https://docs.astral.sh/uv/) single-file scripts — they declare their own
deps in a `# /// script` header and run via the `#!/usr/bin/env -S uv run --script`
shebang, so no virtualenv setup is needed.

`zen-commit` and `zen-release` are symlinked onto `PATH` as global commands by
the root `./link-bin` script.

## Commands

| Script | Command | What it does |
|---|---|---|
| `commit.py` | `zen-commit` | Conventional-Commit helper: stage → secret-scan → AI message (claude haiku) → commit. |
| `release.py` | `zen-release` | Resumable release orchestrator: version bump, tag, changelog, push, GitHub release. |
| `pgp.py` | `zen-pgp` | gum-driven `gpg` encrypt/decrypt helper (symmetric or to a recipient key). |
| `run.sh` | — | Fuzzy picker that lists & runs the scripts in this dir (and aliases). |
| `screenkey.sh` | — | Loops `screenkey` for on-screen keystroke display (demos/screencasts). |

### zen-commit
```
zen-commit                 # interactive: pick files, AI message, confirm
zen-commit --all --yes     # headless: stage everything, accept AI message
zen-commit --all -m "..."  # headless: verbatim message, skip AI
```
Stages files, scans the staged diff for secrets/sensitive files, generates a
Conventional Commit message, then lets you commit / edit / regenerate. Runs
headless (no `gum` prompts) when there's no TTY or `--yes`/`-m` is given. The
secret guardrail aborts on findings.

### zen-release
```
zen-release setup          # run once per repo: detect tools, configure branches/phases
zen-release                # full release
zen-release --dev          # quick dev (-dev.N) release on current branch
zen-release --resume        # continue a release interrupted mid-run
zen-release --yes --bump <patch|minor|major>   # headless
```
Resumable, interactive orchestrator — state is saved so an interrupted run can
`--resume`. `--dry-run` echoes mutating commands instead of running them.

A release flows up a **merge chain**, target last: the branch you're on → the
configured source → the target (e.g. `feature/x → develop → master`). On a
feature branch it merges up through the source; on the target with no source it
releases in place (no merge, no checkout). `--source`/`--target` override the
saved config for one run. See `docs/wiki/architecture/release-tooling.md` for the
full design, `BACKLOG.md` for future work.

### zen-pgp
```
zen-pgp                    # interactive menu
zen-pgp encrypt [FILE]     # symmetric (passphrase) or asymmetric (recipient)
zen-pgp decrypt [FILE]     # auto-detects symmetric vs. key-based
```

## Conventions

- **Header doc** — each Python script carries a man-page-style banner at the top
  (NAME / SYNOPSIS / OPTIONS / DESCRIPTION). Read it first; it's the source of truth
  for flags and behaviour. Keep it in sync when changing the interface.
- **uv single-file** — declare runtime deps in the `# /// script` block, not a
  `requirements.txt` or venv.
- **gum-driven UI** — interactive scripts shell out to [`gum`](https://github.com/charmbracelet/gum);
  they fall back to headless mode when there's no TTY.
- **Shell scripts** — `bash` with `set -e`.

## Gotchas

- **gum captures stderr → invisible hang.** `gum` draws its interactive widgets
  (choose/input/file/confirm) on **stderr** and reads keystrokes from the tty.
  When shelling out and capturing the selection, pipe **stdout only** (Python:
  `subprocess.run([...], stdout=subprocess.PIPE)`, never `capture_output=True`).
  Capturing stderr hides the UI and the program looks frozen while gum silently
  waits for input. Sneaky part: a blind tmux `send-keys` test still passes with
  stderr captured (default option + Enter selects fine), so confirm the widget
  is actually visible via `capture-pane`.

## Dependencies

- `uv` — runs the Python scripts
- `gum` — interactive UI (commit, release, pgp, run)
- `fzf` — fuzzy matching in `run.sh`
- `gpg` — `zen-pgp`
- `claude` — AI commit messages
- `gitleaks` — secret scan in `zen-release` (optional via `--no-scan`)
- `git-cliff` — changelog in `zen-release` (optional via `--no-changelog`)
- `gh` — GitHub release creation in `zen-release`

## Tests

```
uv run scripts/test_commit.py
uv run scripts/test_release.py
```
