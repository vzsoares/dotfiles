# clawd-status

A little pixel Clawd that lives in the Claude Code status line. It breathes
when idle, pumps its arms and strides when Claude is working, and waves a gold
`!` when it needs you. Subagents get their own one-row mini clawd.

```
 ▀▀▀▀▀▀▀    dotfiles git:master ✗
▀▀▀▀▀▀▀▀▀   Opus 5 high ctx:23%
 ▀ ▀ ▀ ▀    quota:41% idle
```

## Install

The plugin loads itself. `~/.claude/skills/` is a symlink to `.claude/skills/`
in this repo, and any folder there with a `.claude-plugin/plugin.json` is
picked up as `clawd-status@skills-dir` on the next session — no marketplace, no
install step. That covers the hooks and the subagent rows.

The main status line is the one piece a plugin can't ship (plugin
`settings.json` only supports `agent` and `subagentStatusLine`), so it's wired
into `.claude/settings.json` directly:

```json
"statusLine": {
  "type": "command",
  "command": "$HOME/.claude/skills/clawd-status/scripts/statusline.sh",
  "refreshInterval": 1
}
```

`clawd-status install` writes that for you and `clawd-status uninstall` puts
the old one back (stashed in
`~/.claude/clawd-status-previous-statusline.json`).

## Hiding the character

`clawd-status hide` (or `show` / `toggle`) drops the sprite and collapses the
status line to a single row with every segment on it — the info stays, only
the character goes:

```
dotfiles git:master ✗ Opus 5 ⚡ high ctx:23% quota:41% idle
```

Subagent rows fall back to Claude Code's default rendering. The flag lives in
`~/.claude/clawd-status.json` and applies on the next tick with no restart.
`CLAWD_HIDDEN=1` (or `=0`) in the environment overrides the file for a single
session.

## Layout

```
.claude-plugin/plugin.json   manifest
settings.json                subagentStatusLine (plugin-scoped)
hooks/hooks.json             session state tracking
lib/sprite.sh                frames + half-block renderer
lib/state.sh                 shared runtime state
scripts/statusline.sh        main status line
scripts/subagent.sh          subagent rows
scripts/state-hook.sh        hook handler
bin/clawd-status             preview / frames / hide / install / doctor
skills/clawd/SKILL.md        how to drive and edit it
```

## Cost

The status line runs locally and burns no tokens. One bash process plus one
`jq` per tick, ~30ms, with git output cached for 3s. The `PostToolUse` hook
forks nothing — no jq, no sed — so it adds a bare bash startup per tool call.
