---
title: Fresh Manjaro i3 Install
category: concept
updated: 2026-05-25
related: [overview, audio-pipewire-spotify]
---

# Fresh Manjaro i3 Install

Step-by-step bring-up of this dotfiles environment on a clean **Manjaro Linux + i3** machine. Order matters: oh-my-zsh must exist before `./link`, and the repo must live at the expected path before zsh will load.

## 0. Clone to the right place

`.zshrc` hardcodes `~/code/personal/dotfiles` for `ZSH_CUSTOM` and the `scripts/` PATH entry. Clone there exactly:

```bash
mkdir -p ~/code/personal
git clone <repo-url> ~/code/personal/dotfiles
cd ~/code/personal/dotfiles
git submodule update --init --recursive   # zsh plugins live as submodules
```

## 1. Base packages (pacman)

```bash
sudo pacman -S --needed \
  neovim ripgrep fzf gpg \
  alacritty i3-wm i3status \
  playerctl ttf-hack-nerd \
  pavucontrol pipewire pipewire-pulse pipewire-alsa wireplumber libpulse
```

- `neovim` — editor (config uses **lazy.nvim**, which self-bootstraps; no packer needed despite the README)
- `ripgrep`, `fzf` — used by nvim + tmux-sessionizer
- `playerctl` — media keys under i3
- `ttf-hack-nerd` — Hack Nerd Font (icons in nvim/tmux/prompt)
- `pavucontrol` + pipewire stack — audio (see [[audio-pipewire-spotify]])

## 2. oh-my-zsh (required before `./link`)

```bash
sh -c "$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)"
```

`./link` refuses to symlink `.zshrc` unless `~/.oh-my-zsh` exists. The custom `zenha` theme and plugins are sourced from the repo via `ZSH_CUSTOM`, so no extra theme install is needed.

## 3. Version managers & tools

```bash
# mise — manages go/bun/deno/node/python/rust/usage/watchexec per mise/config.toml
curl https://mise.run | sh

# uv — Python package manager (README requirement; used for vectorcode)
curl -LsSf https://astral.sh/uv/install.sh | sh
```

## 4. Run the link scripts

From the repo root:

```bash
./link            # .zshrc, .gitconfig, biome.json → ~
./link-claude     # .claude/{CLAUDE.md,settings.json,skills} → ~/.claude/
( cd alacritty && ./link )
( cd i3 && ./link )
( cd nvim && ./link )
( cd tmux && ./link )
( cd mise && ./link )
```

Each script removes any existing target then creates an absolute symlink back into the repo. See [[overview]] for the full link map.

## 5. First launches

```bash
exec zsh          # reload shell; oh-my-zsh + zenha theme + plugins load
mise install      # install the tool versions from mise/config.toml
nvim              # lazy.nvim bootstraps and installs plugins on first run
```

## 6. Audio

i3 ships no audio applet. After installing the pipewire stack above, enable the user services and pick an output with `pavucontrol`. If Spotify shows **"can't play this right now"**, follow [[audio-pipewire-spotify]] — the usual cause on a fresh install is a missing WirePlumber session manager / sink.

## Not handled by link scripts

- `discord/settings.json` — present in the repo but not symlinked by any script; copy manually if wanted.

## See Also

- [[overview]] — what each directory is and how linking works
- [[audio-pipewire-spotify]] — the PipeWire/Spotify fix referenced in step 6
