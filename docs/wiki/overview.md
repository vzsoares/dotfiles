---
title: Overview
category: architecture
updated: 2026-05-25
related: [fresh-install, audio-pipewire-spotify]
---

# Overview

Personal dotfiles for **vzsoares** (zenha), targeting **Manjaro Linux** with the **i3** window manager. Minimalistic setup themed with [Catppuccin Mocha](https://github.com/catppuccin/catppuccin). The repo is a collection of per-tool config directories, each linked into place from the repo via small `link` scripts.

## Repository layout

| Path | Purpose |
|------|---------|
| `alacritty/` | Terminal emulator config (`.alacritty.toml`) — own `link` script |
| `i3/` | i3 WM config + i3status — own `link` script |
| `nvim/` | Neovim config (Lua, **lazy.nvim**) — own `link` script |
| `tmux/` | tmux config, `tmux-sessionizer`, status helper scripts — own `link` script |
| `mise/` | [mise](https://mise.jdx.dev/) tool-version + task config — own `link` script |
| `zsh/` | aliases, custom functions, themes (`zenha`), plugins (git submodules) |
| `.claude/` | Claude Code settings, `CLAUDE.md`, and skills — linked via `link-claude` |
| `scripts/` | Utility scripts (`commit.sh`, `release.sh`, `release-dev.sh`, `run.sh`); added to `PATH` |
| `discord/` | Discord `settings.json` (not linked by any script) |
| `static/`, `pics/` | Screenshots / wallpaper assets |

Root-level dotfiles linked to `~`: `.zshrc`, `.gitconfig`, `biome.json`.

## Linking model

Each tool is symlinked into place (always **absolute paths**) by a `link` script. Symlinks point back at the repo, so editing a config in the repo immediately affects the live environment.

- `./link` — `.zshrc`, `.gitconfig`, `biome.json` → `~` (requires oh-my-zsh already installed)
- `./link-claude` — `.claude/{CLAUDE.md,settings.json,skills}` → `~/.claude/`
- `alacritty/link` — `.alacritty.toml` → `~`
- `i3/link` — `config` → `~/.i3/config`, `.i3status.conf` → `~`
- `nvim/link` — whole dir → `~/.config/nvim`
- `tmux/link` — whole dir → `~/.config/tmux`
- `mise/link` — whole dir → `~/.config/mise`

> **Clone location matters.** `.zshrc` hardcodes `ZSH_CUSTOM=$HOME/code/personal/dotfiles/zsh` and adds `$HOME/code/personal/dotfiles/scripts` to `PATH`. The repo must live at `~/code/personal/dotfiles` for zsh to load correctly.

## Key components

- **Shell**: zsh + oh-my-zsh, custom theme `zenha` (`zsh/themes/`). Plugins: `git`, `zsh-autosuggestions`, `zsh-syntax-highlighting`, `zsh-vi-mode` — the latter three are **git submodules** under `zsh/plugins/`.
- **Editor**: Neovim with a Lua config bootstrapped by **lazy.nvim** (`nvim/lua/config/layz.lua`, `nvim/lazy-lock.json`). The README's mention of `packer` is stale — lazy.nvim self-bootstraps on first launch.
- **Tool versions**: managed by **mise** (`mise/config.toml`): go, bun, deno, node, python, rust, usage, watchexec. `.zshrc` also wires up nvm, bun, deno, pyenv, sdkman, and Go paths.
- **Terminal multiplexer**: tmux with `tmux-sessionizer` and CPU/memory status scripts.
- **Fonts**: Hack Nerd Font (`ttf-hack-nerd`).

## Conventions

- Shell scripts use `bash` with `set -e`.
- Symlinks always use absolute paths.
- oh-my-zsh is required (theme: `zenha`).
- Git: no fast-forward merges, pull with rebase, auto setup remote.
- Editor: neovim.

## See Also

- [[fresh-install]] — step-by-step bring-up of this setup on a new Manjaro i3 machine
- [[audio-pipewire-spotify]] — PipeWire/WirePlumber audio backend fix (Spotify "can't play this right now")
