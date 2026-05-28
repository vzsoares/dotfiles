# Dotfiles

Personal dotfiles for vzsoares. Manjaro Linux (i3).

## Structure

```
alacritty/    # Terminal emulator config (has own link script)
i3/           # i3 window manager config (has own link script)
nvim/         # Neovim config with Lua (has own link script)
tmux/         # tmux config (has own link script)
zsh/          # Zsh aliases, plugins, themes, custom scripts
.claude/      # Claude Code settings, MCP servers, and skills
scripts/      # Utility scripts (release.py, run.sh, commit.sh, etc.)
```

## Release tool (`scripts/release.py`)

`release.py` is a single-file `uv` script (PEP 723 deps; shells out to `gum`) — a
resumable, interactive release orchestrator. See `BACKLOG.md` for the full design.

- **Setup once per machine:** `release setup` detects external tools and saves a
  global config to `~/.config/zen-release/config.json`. `git` + `gum` are required;
  everything else (gitleaks, gh, git-cliff, semantic-release, pre-commit, lefthook)
  is optional — a missing optional tool just skips its phase (no fallback chain).
- **Per-repo config:** the first release in a repo asks (once) for source/target
  branches and which optional phases to run, saved to `.git/zen-release.json`
  (uncommitted). Change it later with `release --reconfigure`.
- **Run a release:** `release` (full) or `release --dev` (lightweight: bump to
  `-dev.N`, tag + push the current branch; stateless). Resumable full releases use
  `release --resume` / `--restart`.
- **Aliases:** `release`, `release-dev` (zsh), and `run release-dev` via `run.sh`.
- **Tests:** `uv run scripts/test_release.py`.

## Linking

Each config directory that needs symlinking has its own `link` script.
Root-level link scripts:

- `./link` — symlinks `.zshrc`, `.gitconfig`, `biome.json` to `~`
- `./link-claude` — symlinks `.claude/settings.json` and `.claude/skills/` to `~/.claude/`

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
