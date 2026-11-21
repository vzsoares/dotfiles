---
title: Agent Roles — zen-role
category: architecture
updated: 2026-11-21
related: [overview, decision-inbox, agent-telemetry]
---

# Agent Roles — zen-role

Each agent-manager role (chief, orchestrator, spec-writer, worker, designer) gets its
own Claude Code config from one tracked file, `agent-manager/roles.json`. A preset in
`agent-manager/config.json` still holds the role's instructions; the role file holds
everything else. A session with no role (the user's own chats, anything outside
agent-manager) is unchanged.

## The three parts

| Part | Where | Does |
|---|---|---|
| Role table | `agent-manager/roles.json` | one entry per role; the only source of truth |
| Env profile | agent-manager, `role-<name>` | sets `ZEN_ROLE=<name>` and a `PATH` whose first entry is `agent-manager/shim` |
| Shim | `agent-manager/shim/claude` | with `ZEN_ROLE` set, adds the role's flags and settings, then runs the real CLI |

`zen-role build` writes the profiles (`agent-manager/link` runs it after the import).
It stores `PATH` as a literal, so re-run it when `PATH` changes. It refuses to run from a linked worktree, never removes a
profile and reports a `role-*` profile that has no entry in the file.

A spawn names the profile: `environment role-worker` on `create_session`,
`--env role-chief` on `agent-manager spawn`. A spawn that names none inherits its
group's profile, else its spawner's, which is why the presets name one on every
`create_session` line.

## Role fields

All optional except `saved`.

| Field | Meaning |
|---|---|
| `saved` | the preset name in `agent-manager/config.json` |
| `model`, `effort` | launch defaults; a spawn that passes its own keeps it |
| `autocompact` | compaction window in tokens, 100000 or more |
| `settings` | a Claude Code settings object laid over the global one (output style, plugins, permissions, any key) |
| `skills.only` | the user skills (`zen-flow`) and synced skills (`anthropic-skills:<name>`) the role keeps; every other one on disk goes off |
| `skills.off` | literal skill names to turn off, any namespace |
| `tools.deny` | built-in or MCP tool names to disallow |
| `append` | a file under `agent-manager/roles/` appended to the system prompt |

`skills.only` reads the skill dirs at launch: `~/.claude/skills/*/SKILL.md` and
`~/.claude/skills/synced/<bucket>/*/`. A newly synced skill is therefore off for a
role with `only` from the next launch. The `anthropic-skills:` twin of a kept user
skill goes off unless it is listed too. Bundled skills (`code-review`) and plugin
skills (`clawd-status:clawd`) are untouched; use `skills.off` for those.

`zen-role check` validates the file: unknown keys, types, the window minimum, `saved`
against the presets, `skills.only` names against the disk, the `append` file.
`zen-role show <name> [--json]` prints the flags and the settings overlay a role
resolves to on this machine.

## Overlay mode

The only mode so far. The global layer (`~/.claude/settings.json`, the user and
project `CLAUDE.md`, MCP servers, connectors) loads as always and the role's values
are laid over it.

What the shim does on a role launch, first start and `--resume` alike:

1. Finds the real `claude` on `PATH`, skipping itself.
2. Takes agent-manager's `--settings` (a file or inline JSON) off the argv and merges
   it with the role overlay: objects key by key, lists joined (so every hook entry of
   both survives), and agent-manager's value wins a scalar conflict. The CLI keeps
   only the last `--settings`, so exactly one is passed on.
3. Adds `--model` and `--effort` when the launch has none, `--autocompact`,
   `--disallowedTools` and `--append-system-prompt-file` when the role sets them.
4. Passes every other argument through unchanged.

The window is set three ways so the role wins over the global file: the
`--autocompact` flag, `autoCompactWindow` in the merged settings, and
`modelSettings.<model>.autoCompactWindow` for each model the global file pins.

Run files live in `~/.cache/zen/roles/run/<launcher settings file name>/`, rewritten
on each launch. A set is pruned on a later launch once its launcher file and its CLI process are both gone.

### What a role does not touch

Subcommands (`mcp`, `auth`, `config`, `plugin`/`plugins`, `update`/`upgrade`,
`doctor`, `agents`, `install`, `setup-token`, `logs`, `attach`, `auto-mode`,
`gateway`, `import`, `purge`, `respawn`, `rm`, `stop`/`kill`, `ultrareview`),
`-p`/`--print`, `--version` and `--help` run the
real CLI with the same argv, in bash, with no interpreter started. So headless calls
made inside a role session (`zen-commit`, the wrap-up hook, probes) keep the global
config, the `wrapup` skill included.

Overlay cannot drop instructions: the global `CLAUDE.md` still tells every role to use
`/wiki` and `/second-brain`, which a worker or spec-writer no longer lists.

### Errors

An unknown `ZEN_ROLE`, an unreadable or invalid `roles.json`, an invalid role entry, a
missing `append` file, an unreadable launcher settings file, or no real `claude` on
`PATH` each stop the launch with one `zen-role: …` line. There is no fallback to the
global config. A `skills.only` name that is not on disk does not stop a launch (a
skill may not have synced yet); `zen-role check` reports it.

## Reverting a cut

Each cut is one line in `roles.json`. Delete it and the next launch or revive of that
role runs without it; agent-manager needs no restart.

| To undo | Remove |
|---|---|
| the skill cut | the role's `skills` key (or add the skill to `only`) |
| the output style | `settings.outputStyle` |
| the plugin cut | the entry under `settings.enabledPlugins` |
| the compaction window | `autocompact` |
| the role altogether | spawn without `environment` / `--env`, or `unset ZEN_ROLE` in the profile |

## Tests

```
uv run scripts/test_role.py
bash agent-manager/test/shim.sh
```

`shim.sh` runs the shim against a fake `claude`, a copy of the repo layout and a temp
`HOME`. The settings overlay was also checked once against the real CLI: the
`system/init` event of a headless `--output-format stream-json --verbose` run with the
worker role's overlay lists the `default` style, no figma plugin, and of the user and
synced skills only `zen-flow` and `anthropic-skills:chrome-browser`. The compaction
window cannot be observed that way; it shows only when a session first compacts.
