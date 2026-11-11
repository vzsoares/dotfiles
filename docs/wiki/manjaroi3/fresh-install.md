---
title: Fresh Manjaro i3 Install
category: concept
updated: 2026-08-20
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
  neovim ripgrep fzf gpg gitleaks \
  alacritty i3-wm i3status \
  playerctl ttf-hack-nerd \
  pavucontrol pipewire pipewire-pulse pipewire-alsa wireplumber libpulse
```

- `neovim` — editor (config uses **lazy.nvim**, which self-bootstraps; no packer needed despite the README)
- `ripgrep`, `fzf` — used by nvim + tmux-sessionizer
- `gitleaks` — secret scanner. **Required on PATH**, not optional: work repos
  run it from a husky `pre-commit` hook, and without it every commit in those
  repos is rejected (see [Secret scanning](#secret-scanning-gitleaks) below)
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
./link-claude     # .claude/{CLAUDE.md,settings.json,skills,agents} → ~/.claude/
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

## Secret scanning (gitleaks)

`gitleaks` is a **hard dependency for committing and pushing**, required in three
independent places:

1. **`zen-commit`'s guardrail** — `scan_secrets()` shells out to
   `gitleaks protect --staged` (`scripts/commit.py`). Filename/`.npmrc` checks are
   pure Python, but the content scan is the real binary.
2. **Repo `pre-commit` hooks** — several work repos have a husky hook running
   `gitleaks git --staged --redact --verbose`, which aborts the commit if the
   binary is missing.

3. **The global pre-push hook** — `git/hooks/pre-push` sources `git/secret-scan.sh`,
   which runs `gitleaks git --log-opts <range>` over only the pushed commits with the
   tuned global config `git/gitleaks.toml` (gitleaks' defaults plus basic-auth URI,
   password/secret assignment and high-entropy rules, modelled on GitGuardian). A
   repo `.gitleaks.toml` with only `[[allowlists]]` is layered on top; one with its
   own `[extend]` is used as-is. A missing or failing gitleaks refuses the push.

Note the first two use **different subcommands** (`protect` vs `git --staged`); both
exist in current gitleaks, but a version that drops `protect` would break
`zen-commit` while leaving the hook working.

Verify it resolves:

```bash
command -v gitleaks   # empty means you cannot commit at all
```

Install: `sudo pacman -S gitleaks` (Arch `extra`), `brew install gitleaks`
(macOS), or `go install github.com/zricethezav/gitleaks/v8@latest` on either.

> **PATH gotcha.** A `go install`-ed gitleaks lands in `~/go/bin` — and so does
> `gum`, which `zen-commit` also needs. That directory is added by `.zshrc`, an
> *interactive* shell file, so anything under a non-interactive shell won't see
> it. `zen-commit` preflights both and exits with an install hint; a bare hook
> just fails with `gitleaks not found on PATH`.

### ggshield (GitGuardian), second layer on push

On remotes under `github.com/vzsoares/`, the pre-push hook also runs
`ggshield secret scan pre-push`, which uploads the pushed diffs to GitGuardian.
When ggshield is missing, unauthenticated, offline or slower than 15 s, the hook
prints `pre-push: ggshield skipped: <cause>` and goes on with gitleaks alone. It
writes `.cache_ggshield` in the repo; the hook adds it to `.git/info/exclude`.

Same on both machines (needs `uv`):

```bash
uv tool install ggshield
command -v ggshield      # ~/.local/bin/ggshield
ggshield auth login      # browser flow; token lands in ggshield's config
ggshield api-status      # expect: status: healthy
```

Fallback without the browser flow: create a personal access token in the
GitGuardian dashboard and add `export GITGUARDIAN_API_KEY=…` to
`~/.zshrc.local`, then check it in a new shell with
`echo "${#GITGUARDIAN_API_KEY} chars"`.

A finding refuses the push. A secret that could be real is rotated and rewritten
out of the unpushed commits, never ignored. A clear false alarm gets its
`Fingerprint:` in `.gitleaksignore` (or `ggshield secret ignore --last-found`
into `.gitguardian.yaml`) under a `# reason` line; the fingerprint holds the
commit sha, so rebasing that commit means redoing it. `--no-verify` is the only
bypass.

## Not handled by link scripts

- `discord/settings.json` — present in the repo but not symlinked by any script; copy manually if wanted.

## See Also

- [[overview]] — what each directory is and how linking works
- [[audio-pipewire-spotify]] — the PipeWire/Spotify fix referenced in step 6
