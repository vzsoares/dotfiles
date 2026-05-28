# Dotfiles

Personal dotfiles for vzsoares. Manjaro Linux (i3).

## Structure

```
alacritty/    # terminal emulator config
i3/           # i3 window manager config
nvim/         # neovim config (lua)
tmux/         # tmux config
zsh/          # zsh aliases, plugins, themes
.claude/      # Claude Code settings & skills
scripts/      # utility scripts (release.py, commit.py, run.sh, ...)
```

Config dirs (alacritty, i3, nvim, tmux) each have their own `link` script.

## CLI tools (`scripts/`)

Global commands via `./link-bin` (symlinked into `~/.local/bin`):

- **`zen-commit`** (`commit.py`) — Conventional-Commit helper: stage → secret
  guardrail → AI message → commit. Headless with `--all --yes`.
- **`zen-release`** (`release.py`) — resumable release orchestrator; `BACKLOG.md`
  has the design. `zen-release setup` once, then `zen-release` / `--dev`.
- **`zen-pgp`** (`pgp.py`) — gum-driven PGP encrypt/decrypt over `gpg`
  (symmetric passphrase or recipient key). `zen-pgp encrypt|decrypt [FILE]`.

Tests: `uv run scripts/test_release.py`, `uv run scripts/test_commit.py`.

## Linking

Each config directory that needs symlinking has its own `link` script.
Root-level link scripts:

- `./link` — symlinks `.zshrc`, `.gitconfig`, `biome.json` to `~`
- `./link-claude` — symlinks `.claude/settings.json` and `.claude/skills/` to `~/.claude/`
- `./link-bin` — symlinks `zen-release` / `zen-commit` / `zen-pgp` into `~/.local/bin`

Run from the repo root.

## Conventions

- Shell scripts use `bash` with `set -e`
- Symlinks always use absolute paths
- Zsh config: `.zshrc` at root sources files from `zsh/` (aliases, custom, plugins, themes)
- Oh-my-zsh is required for zsh setup (theme: zenha)
- Git: no fast-forward merges, pull with rebase, auto setup remote
- Editor: neovim

# Second Brain

This project is connected to the personal second brain at `/home/zizmackrok/code/personal/valt`.
Use the `/second-brain` skill to file project knowledge, query cross-project context, or ingest sources.
When completing significant features or making architectural decisions, suggest filing them in the second brain if they have cross-project value.
