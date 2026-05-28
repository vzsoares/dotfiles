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

### CLI tools

Install the personal commands onto PATH with `./link-bin` (symlinks into
`~/.local/bin`):

```sh
zen-commit            # interactive Conventional-Commit helper (stage → guardrail → AI msg)
zen-commit --all --yes        # headless: stage all, AI message, commit

zen-release setup     # once per machine: detect tools, save ~/.config/zen-release
zen-release            # full release (first run configures branches + phases per repo)
zen-release --dev      # quick dev release: bump -dev.N, tag + push current branch
zen-release --resume   # continue an interrupted release
zen-release --yes --bump patch   # headless: no prompts
```

`zen-release` is a resumable, interactive release orchestrator (single-file `uv`
script, shells out to `gum`); see `BACKLOG.md` for the design. Both tools run
headless (no prompts) with the flags above. Tests: `uv run scripts/test_release.py`
and `uv run scripts/test_commit.py`.

### Other

##### Pull git submodules

`git submodule update --init --recursive`
