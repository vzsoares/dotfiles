# Dotfiles

Personal dotfiles for vzsoares. Manjaro Linux (i3).

## Structure

```
alacritty/    # terminal emulator config
cava/         # console audio visualizer (Rose Piné, tuned for a narrow pane)
i3/           # i3 window manager config
flameshot/    # screenshot tool (Catppuccin Mocha); i3 binds Print / mod+Shift+Print
mermaid/      # zen-diagram: themed mermaid renderer (bun); screenshots and
              #   custom HTML in diagram nodes — see its README.
              #   Driven by the `diagram` skill in .claude/skills/
nvim/         # neovim config (lua)
tmux/         # tmux config
zsh/          # zsh aliases, plugins, themes
.claude/      # Claude Code settings & skills
              #   skills/clawd-status/ — status line sprite plugin (auto-loads
              #   as clawd-status@skills-dir; see its README)
scripts/      # utility scripts (release.py, commit.py, run.sh, ...)
```

Config dirs (alacritty, cava, flameshot, i3, nvim, tmux) each have their own
`link` script.

## Linking

Each config directory that needs symlinking has its own `link` script.
Root-level link scripts:

- `./link` — symlinks `.zshrc`, `.gitconfig`, `biome.json` to `~`
- `./link-claude` — symlinks `.claude/` contents to `~/.claude/`: `CLAUDE.md`,
  `settings.json`, `skills/`, `statusline-command.sh`
- `./link-bin` — symlinks the `zen-*` commands into `~/.local/bin`. The `LINKS`
  table at the top of that script is the authoritative list — read it there
  rather than duplicating it. `./link-bin --check` reports drift (missing,
  dangling, or unmanaged links) and exits non-zero.
  (`zen-diagram` needs `bun install` in `mermaid/` first)

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
