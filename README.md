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

### Requirements

- nvim aur:neovim
    - packer
    - fzf
    - ripgrep
- gpg
- nerdfonts (Hack Nerd Font) aur:ttf-hack-nerd
- ohmyzsh
- alacritty
- uv (python pkg manager)
    - vectorcode

- i3
    - playerctl

### Release tool

`scripts/release.py` — a resumable, interactive release orchestrator (single-file
`uv` script, shells out to `gum`). See `BACKLOG.md` for the design.

```sh
release setup        # once per machine: detect tools, save ~/.config/zen-release
release               # full release (first run configures branches + phases per repo)
release --dev         # quick dev release: bump -dev.N, tag + push current branch
release --resume      # continue an interrupted release
```

`run release-dev` works too. Tests: `uv run scripts/test_release.py`.

### Other

##### Pull git submodules

`git submodule update --init --recursive`
