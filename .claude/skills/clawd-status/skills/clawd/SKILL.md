---
name: clawd
description: Manage the clawd-status status line sprite — install, remove, hide or show the character, preview frames, or edit the sprite art. Use when the user mentions clawd, the status line sprite, or the little pixel creature in their status bar.
---

# clawd-status

A pixel Clawd that lives in the status line. Three terminal rows of half-block
sprite on the left, session info on the right.

## Commands

The plugin ships a `clawd-status` executable on the Bash tool's PATH:

| Command | What it does |
| --- | --- |
| `clawd-status preview [state] [secs]` | Animate a state (`idle`, `work`, `wait`) in the terminal |
| `clawd-status frames [state]` | Print every frame of a cycle side by side |
| `clawd-status hide` / `show` / `toggle` | Hide or show the character |
| `clawd-status install` | Point `~/.claude/settings.json` at the clawd status line |
| `clawd-status uninstall` | Restore the previous status line |
| `clawd-status doctor` | Check the wiring, jq, and permissions |

## Hiding the character

With the character hidden the status line collapses from three rows to a
single row carrying every segment, and subagent rows fall back to Claude
Code's default rendering. The info is never lost — only the sprite goes away.

```
dotfiles git:master ✗ Opus 5 ⚡ high ctx:23% quota:41% idle
```

The setting lives in `~/.claude/clawd-status.json` (`{"hidden": true}`) and
takes effect on the next tick, no restart needed. `CLAWD_HIDDEN` in the
environment overrides the file in both directions, so a single session or a
screen recording can opt out with `CLAWD_HIDDEN=1` without touching config.

## How it moves

The status line command re-runs on session events (debounced at 300ms) and on
the `refreshInterval` timer. That gives roughly 3fps while Claude is generating
and 1fps when idle — the animation naturally speeds up when there's work.

State comes from hooks, not the status line payload, which carries no
"is Claude busy" field:

| Hook | State |
| --- | --- |
| `SessionStart`, `Stop` | `idle` — breathe and blink |
| `UserPromptSubmit`, `PostToolUse` | `working` — arms pump, legs stride |
| `Notification` | `waiting` — gold `!` blinks |

Hooks write `$XDG_RUNTIME_DIR/clawd-status/<session_id>.state`. A `working`
state older than 180s decays back to `idle`, so a missed `Stop` hook can't
leave clawd stuck.

## Editing the sprite

Frames live in `lib/sprite.sh` as pixel grids, one character per pixel:

```
C coral body   K eye   Y gold (alert)   R love   . transparent
```

Two pixel rows pack into one terminal row via the upper half block (`▀`), top
pixel as foreground and bottom as background. Every frame in a cycle **must**
be 6 rows of exactly `CLAWD_W` characters — `clawd-status frames` renders ragged rows
as misaligned columns, which is the fastest way to spot a typo.

Add a palette colour by adding a `CLAWD_PAL` entry; the renderer picks it up
with no other change.

## Gotchas

- Don't parse the jq output with tabs. Tab is IFS whitespace, so `read`
  collapses runs of it and empty fields shift every later field left. The
  scripts join with `` instead.
- `~/.claude/settings.json` is a symlink into the dotfiles repo. Write through
  it (`cat tmp > file`); `mv` replaces the symlink with a regular file.
- The status line only runs after the workspace trust dialog is accepted. If
  it's blank, `claude --debug` says so.
