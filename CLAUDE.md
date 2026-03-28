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
scripts/      # Utility scripts (release, etc.)
```

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
