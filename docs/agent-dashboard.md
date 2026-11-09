# Agent dashboard on tmux

**Status:** adopted [agent-manager](https://github.com/wainmann/agent-manager)
instead of building this (see [Decision](#decision)). Config lives in
`agent-manager/`. The design below is kept as the fallback if it falls short.

Run several coding agents in parallel, each in its own git worktree, and keep
one tmux window as a live dashboard where every agent is visible _and_
answerable in place. Same goal as Orca, built from what's already here: tmux
(`C-Space` prefix), nvim, Claude Code hooks, `zen-commit`.

## Model

- **One tmux session per repo.** `tmux-sessionizer` (`C-Space f`) already
  does this.
- **Window 0 `agents` is the dashboard.** Every agent pane lives there in a
  `select-layout tiled` grid. These are the real sessions, not previews.
- **One window per task** for the worktree's nvim and shell. The agent pane
  is not here; it stays in the grid.
- **Each task gets its own worktree** on branch `wt/<name>`.

```
 dotfiles │ 0:agents  1:main  2:auth-fix  3:promo-tests  4:docs
┌─ ● auth-fix ───────────────┬─ ? promo-tests ──────────────┐
│ Editing src/auth/session.ts│ Allow edit promo.test.ts?    │
│ ▌                          │ ❯ 1. Yes  2. No              │
├─ ✓ docs ───────────────────┼─ ● nvim-lsp ─────────────────┤
│ Done. 3 files changed.     │ Running bun test…            │
└────────────────────────────┴──────────────────────────────┘
```

## Interface

| Key / command                  | Does                                                        |
| ------------------------------ | ----------------------------------------------------------- |
| `zen-wt new <name> "<prompt>"` | worktree + branch, task window, agent pane in the grid      |
| `C-Space a`                    | gum popup (name, prompt) running `zen-wt new`               |
| `M-arrows`                     | move between agent panes; type or answer `1`/`2` in place   |
| `C-Space z`                    | zoom one agent full screen and back to the grid             |
| `C-Space g`                    | toggle between an agent pane and its task window            |
| `C-Space d`                    | diff popup: diffview of `main...wt/<name>`                  |
| `C-Space m`                    | finish: `zen-commit`, merge or PR, remove worktree + window |

From the diff view, a nvim keymap sends `file:line: comment` to the task's
agent pane with `tmux send-keys`, a lightweight version of Orca's inline diff
comments.

## State without polling

Claude Code hooks set a window/pane option, and tmux formats render it:

| Hook               | State | Extra                                           |
| ------------------ | ----- | ----------------------------------------------- |
| `UserPromptSubmit` | `●`   |                                                 |
| `Notification`     | `?`   | highlight the pane border, desktop notification |
| `Stop`             | `✓`   |                                                 |

`pane-border-status top` with `pane-border-format` shows the state and task
name over each grid pane. Notifications use `osascript` on macOS and
`notify-send` on Linux, so both machines work.

## Limits

- The grid is readable up to ~6 agents. Past that, a list view (one row per
  agent, its last output line, preview, Enter to jump) scales better. It
  can reuse the same hooks.
- Only Claude Code gets status. Other CLIs need their own hooks or
  screen-scraping. This is the part Orca and agent-manager put real work
  into.
- Things tmux can't do: an embedded browser with click-to-select-element,
  image or PDF previews (Alacritty has no image protocol), a mobile app that
  can reply, simulators.

## Prior art

Look at these before building. herdr and agent-manager already cover most of
this design.

| Project                                                    | What                                                                                                                                                                                                                                                                                        | Overlap                                                                                            |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| [Orca](https://www.onorca.dev/)                            | Desktop "agent IDE" (macOS/Windows/Linux, said to be MIT). One worktree, terminal and browser per task, coordinator/worker agents, diff review with inline comments to the agent, embedded Chromium with Design Mode, mobile companion, SSH/remote                                          | Its GUI features are what tmux can't match                                                         |
| [agent-manager](https://github.com/wainmann/agent-manager) | Go TUI over tmux (Apache-2.0). Each agent in a persistent tmux session, one list with live status for ~13 CLIs, `space` sends a prompt without attaching, `ctrl+r` full-file diff where line comments go back as one review prompt, forks, agents can spawn and message each other over MCP | Closest to the list-view option. The diff → agent comment loop already exists                      |
| [herdr](https://github.com/herdrdev/herdr)                 | Rust terminal multiplexer built for agents (Apache-2.0). Background server survives detach, restores layout and resumes sessions, each pane marked working/blocked/idle, agents drive it through a CLI and socket API, multi-machine over SSH, plugins                                      | Replaces tmux rather than extending it. Same status-per-pane idea, and it handles other agents too |

## Decision

Adopt agent-manager. It covers what I want:

| Want            | agent-manager                                                                                                                     |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Workspaces      | Groups: a nested tree with default path, worktree toggle and base ref. Worktrees at `<repo>-worktrees/<name>`, branch `am/<name>` |
| Agent           | A session: one CLI in its own tmux session (`-L agentmgr`), live status                                                           |
| Sub-agent       | `create_session` over its MCP server; the child is a normal row                                                                   |
| Master agent    | `list/read/send/wait_for_session` + a shared `task` list. Saved prompt `lead` sets it up                                          |
| Config as files | Not native (`state.db`, SQLite). `agent-manager/link` imports/exports JSON instead                                                |

Master agent limits: it can't answer another agent's permission prompts, and
it can't commit, push or merge for one. `send_session` delivers only when the
target is idle.

Config sync: `./link` imports `config.json` (settings + saved prompts) and the
vault's `config/agent-manager-groups.json` (groups hold private repo paths).
`./link export` writes changes made in the app back to both.
