# Wiki Log

## [2026-05-25] bootstrap + ingest | Project overview & fresh-install guide
Bootstrapped the wiki (index.md, log.md). Ingested a project overview from README, link scripts, .zshrc, mise config, and submodules. Created a fresh Manjaro i3 install guide and folded in the user-provided Spotify/PipeWire audio fix as a dedicated, cross-linked page. Noted two source discrepancies: README lists `packer` but nvim uses lazy.nvim; clone path is load-bearing (`~/code/personal/dotfiles`).
Pages created: overview.md, guides/fresh-install.md, guides/audio-pipewire-spotify.md, index.md, log.md

## [2026-05-25] reorg | Nest install guides under manjaroi3/
Moved fresh-install.md and audio-pipewire-spotify.md from guides/ into manjaroi3/. overview.md kept at wiki root. Wiki-links unchanged (filename-based); index "Guides" section relabeled "Manjaro i3".
Pages moved: manjaroi3/fresh-install.md, manjaroi3/audio-pipewire-spotify.md

## [2026-05-25] ingest | Chrome portal file-picker HiDPI fix
Documented the oversized/"blank" Chrome file picker bug: the dialog is drawn by the dbus/systemd-activated xdg-desktop-portal-gtk, which doesn't inherit GDK_SCALE/GDK_DPI_SCALE from ~/.profile. Fix is ~/.config/environment.d/hidpi.conf. Recorded the debugging dead-ends (not DISPLAY, not Chrome flags, not GTK4 GL/NVIDIA — portal is GTK3) so they aren't repeated.
Pages created: manjaroi3/chrome-portal-file-picker-hidpi.md

## [2026-05-26] ingest | Dark mode for GTK/GNOME apps on i3
Saved the `gsettings set org.gnome.desktop.interface color-scheme 'prefer-dark'` command. On i3 there's no settings daemon, so GTK4/libadwaita apps default to light; this dconf key flips them dark. Noted the legacy `gtk-theme` key for GTK3 apps and that the portal exposes the setting to Flatpaks.
Pages created: manjaroi3/dark-mode-gtk-apps.md

## [2026-05-26] ingest | Color emoji fonts (Notion/browsers)
Documented enabling color emoji: install `noto-fonts-emoji` (extra repo, not yay) + a `75-noto-color-emoji.conf` aliasing Apple/Segoe/Twemoji names to Noto. Verified live on this machine (`fc-match emoji` was falling back to Noto Znamenny Musical Notation). Recorded that fontconfig 2.17 makes the old `70-no-bitmaps.conf` panic unnecessary — color emoji are flagged scalable and survive the reject; only swap to the except-emoji variant if `fc-match emoji` still fails after install.
Pages created: manjaroi3/color-emoji-fonts.md

## [2026-08-20] ingest | gitleaks bootstrap requirement + login-shell PATH invariant
Added `gitleaks` to the fresh-install package list (Arch `extra`, verified) with a "Secret scanning" section. `gitleaks` is a hard dependency for committing in **two** places: `zen-commit`'s `scan_secrets()` shells out to `gitleaks protect --staged`, and work repos run `gitleaks git --staged` from a husky `pre-commit` hook — note the differing subcommands. Recorded that `~/go/bin` (holding both `gitleaks` and `gum`) is added by `.zshrc`, an interactive-only file. Documented the login-shell invariant in overview.md: naming `[terminal.shell] program` makes Alacritty spawn a non-login shell, skipping `~/.zprofile`/`/etc/zprofile` and emptying PATH — rebooting does not help, since the cause is config, not state. Overview reframed for two machines (Manjaro i3 + macOS) after an OS-only key was put in a shared config.
Pages updated: manjaroi3/fresh-install.md, overview.md
Code: commit.py now checks `git commit`'s exit code (it printed "Committed" over failed commits, leaving work staged) and preflights `gitleaks` instead of dying on FileNotFoundError.

## [2026-11-09] ingest | Decision inbox (zen-inbox)
Added `scripts/inbox.py` (`zen-inbox`): agents record decisions in `<vault>/config/zen-flow.db` instead of messaging chief; a `decisions` watcher terminal in group `chief` answers them by pick, free text or talk (tmux popup on `am_<session>`). Answers are held per session and flushed as one `agent-manager send --from zen-inbox` message. Found that agent-manager refuses `--from` inside an agent pane, so the send falls back to the caller's identity there. Presets for chief, spec-writer and orchestrator now use the inbox.
Pages created: architecture/decision-inbox.md

## [2026-11-10] update | zen-inbox session view (#586, folds in #583 and #585)
`zen-inbox watch` now lists one row per asking session; a session screen shows each task's context (title, brief, asker's group, repo, status and age), then its questions. Answers are walked one by one, reviewed, then sent; nothing is recorded before Send, and amended or elsewhere-answered decisions are caught at Send through a `rev` check. Schema v2 adds `decision_briefs`, set by `ask --title/--brief` or `zen-inbox brief`. Found that gum choose cuts items at the pane width, so full text is printed above a short-item menu fitted by `screen()`. The talk popup now always attaches with `-L agentmgr`, and chief starts the watcher with `tmux -L agentmgr send-keys`, since agent-manager refuses `terminal send` to an un-nested terminal from inside a session.
Pages updated: architecture/decision-inbox.md

## [2026-11-10] ingest | Agent tool telemetry (#588)
Added `zen-telemetry`, a Go binary (modernc sqlite) in its own module at `scripts/telemetry/`, built by `./link-bin`. An async `PostToolUse`/`PostToolUseFailure` hook logs every tool call of an agent-manager session into `zen-flow.db` beside zen-inbox: `tool_*`/`telemetry_*` tables only, `user_version` untouched. A one-time `import` pairs `tool_use`/`tool_result` from transcripts (skipping `api-request-blob` lines, which repeat tool blocks), and `report` ranks time sinks and compares same-job tools from `jobs.json` for chief's weekly step. Found that Go hook latency is mostly process start, so it tracks machine load.
Pages created: architecture/agent-telemetry.md

## [2026-11-10] update | zen-inbox rewritten in Go (#587, folds in #582)
`zen-inbox` is now one Go binary in `inbox/` (cobra CLI, Bubble Tea panel, pure-Go `modernc.org/sqlite`), replacing `scripts/inbox.py`. CLI output matches inbox.py byte for byte, checked by golden files captured from the Python version before deletion; v1/v2 dbs written by Python open and answer unchanged. The panel keeps #586's flow and adds live session updates (amended, answered elsewhere, new asks). `link-bin` now links the `inbox/zen-inbox` launcher, which rebuilds into `~/.cache/zen/bin` when a source changed. #582: an agent withdrawing its own last open decision gets the held answers on stdout instead of a send to itself.
Pages updated: architecture/decision-inbox.md

## [2026-11-10] update | dev + prod branch model and prod approval (#612)
Two branch models: dev+prod (land and dev-release on `dev`) and single-branch (`main`/`master`, land locally, `push pending`). `zen-merge` detects the model: `PROTECTED` is now `prod` plus the zen-release target of a repo with `dev`, a repo with no `dev` and a checkout off `main`/`master` exits 6, and only base `dev` is released. `zen-release --dev` refuses `prod`/`main`/`master` and the saved target, and a first run pre-fills `dev → prod` when both exist. `zen-sweep` reports `branch-model` drift in personal repos. The prod rule changed from "never" to "only after the user approves that one action as a `zen-inbox ask`" in CLAUDE.md, the presets and the zen-flow skill; dotfiles stays single-branch on `master`.
Pages updated: architecture/release-tooling.md

## [2026-11-10] update | zen-inbox stuck-paste check, panel refresh (#610, folds in #670)
After a successful `agent-manager send`, zen-inbox captures the asker's pane and presses Enter once when the prompt holds only an unsent `[Pasted text #N]` placeholder (Claude sessions only, two matching captures, early stop when the session works). The delivery state never changes; the outcome line says `submitted a stuck paste` or `still stuck` with the attach command, and zen-bell rings once on the latter. Also: an answer recorded inside the asking session is handed back instead of sent (`Inbox.Self`). The decision-inbox page now covers the panel after #614 and #611: busy loader, bottom status line, `r` retry, glamour markdown on ANSI-16 with hard-wrap, and scrolling on every screen.
Pages updated: architecture/decision-inbox.md

## [2026-11-10] update | zen-release prod approval gate (#680)
A full zen-release now fails closed: headless, agent-session or non-TTY runs need `--approval <id>`, a structured `zen-inbox ask --prod` answered Approve, bound to repo, target branch and the full source sha, consumed once by `zen-inbox approval use` (exit 3 otherwise). zen-inbox schema v3 adds `prod_actions`; amend of such an ask is refused. `--dev` is unchanged.
Pages updated: architecture/release-tooling.md, architecture/decision-inbox.md

## [2026-11-10] update | zen-inbox: only the user answers a prod ask (#691)
The store refuses `answer`/`resolve` on a prod-action ask unless `Inbox.User`, an in-process bit only the panel sets after `internal/guard` passes (agent env vars and a `ps` walk for claude/codex ancestors, fail closed). A refused panel shows a notice and offers no answer; a passing one confirms Approve on a y/N screen with the full sha.
Pages updated: architecture/decision-inbox.md

## [2026-11-10] update | Prod push hook (#703)
Global `core.hooksPath` = `git/hooks`: pre-push refuses an agent push to prod/main/master unless `zen-inbox approval use --match --action push` consumes an approved ask for that repo, branch and sha; deletion and force are refused; a human push gets a default-No gum confirm; zen-release's push passes via `ZEN_PROD_RELEASE` + read-only `approval used`. Every other hook name dispatches to the repo's own `.git/hooks`. zen-sweep reports `hooks-path` repos that skip it.
Pages updated: architecture/release-tooling.md, architecture/decision-inbox.md

## [2026-11-11] update | zen-inbox notes (#707)
Schema v5 adds `notes`: chief's non-decision messages (`zen-inbox note`, chief only), listed in the decisions panel below the decision rows with mark read/unread (`m`, `M`) and one reply per note sent back through the answer send path, with retry. Read notes hide after 24 h and are pruned after 30 days; notes never ring. Chief's preset gained the note/neither rule: chat holds replies only, notes carry what the user asked for or something very important, routine status goes nowhere.
Pages updated: architecture/decision-inbox.md

## [2026-11-11] update | zen-inbox --prod run (#736)
Schema v6 adds `prod_actions.cmd`: `zen-inbox ask <task> --prod run --repo R --cmd "<exact command>"` files a user-only approval for a prod script or migration; `approval use|check --action run --repo R --cmd C` consumes it once, only on a byte-for-byte command match. The panel's y/N confirm prints the whole command. zen-flow Tags: copying/archiving data and API-writable CMS edits are Human Approval; drops/deletes of data or stacks and backoffice-UI CMS edits are Human Only.
Pages updated: architecture/decision-inbox.md
