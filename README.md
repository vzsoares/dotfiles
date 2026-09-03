# dotfiles

- minimalistic
- [mocha theme](https://github.com/catppuccin/catppuccin)

### showcase:

![root](./static/base.png)
![root](./static/nvim.png)
![root](./static/nvimtree.png)

- https://github.com/webpro/awesome-dotfiles
- https://github.com/ThePrimeagen/.dotfiles
- https://github.com/olimorris/dotfiles/blob/main/.config/nvim/lua/plugins/coding.lua

### Software

- nvim aur:neovim (uses lazy.nvim, self-bootstraps)
    - ripgrep
    - fzf
- gpg
- nerdfonts (Hack Nerd Font) aur:ttf-hack-nerd
- ohmyzsh
- alacritty
- mise (version manager)
- uv (python pkg manager)
- i3 (linux only)
    - playerctl
- AeroSpace (macos only)
- vpn client (differs per OS)
    - linux: eovp
    - macos: tunnelblick

See `docs/wiki/manjaroi3/fresh-install.md` for full setup.

### Structure

```
alacritty/    # terminal emulator config
aerospace/    # AeroSpace window manager config (macOS counterpart to i3/)
i3/           # i3 window manager config (linux)
nvim/         # neovim config (lua)
tmux/         # tmux config
typst/        # long-form writing (books, TCC) — template + nvim setup
zsh/          # zsh aliases, plugins, themes
.claude/      # Claude Code settings & skills
scripts/      # utility scripts (release, commit, run, ...)
```

### Other

##### Pull git submodules

`git submodule update --init --recursive`
