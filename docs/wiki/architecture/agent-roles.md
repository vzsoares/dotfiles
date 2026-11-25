---
title: Agent Roles — zen-role
category: architecture
updated: 2026-11-24
related: [overview, decision-inbox, agent-telemetry]
---

# Agent Roles — zen-role

Each agent-manager role (chief, orchestrator, spec-writer, worker, designer, explorer) gets its
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
| `isolate` | `true` turns on isolate mode; needs `skills.only` and `instructions` |
| `instructions.keep` | the top-level section titles of the global `CLAUDE.md` an isolated role keeps |

`skills.only` reads the skill dirs at launch: `~/.claude/skills/*/SKILL.md` and
`~/.claude/skills/synced/<bucket>/*/`. A newly synced skill is therefore off for a
role with `only` from the next launch. The `anthropic-skills:` twin of a kept user
skill goes off unless it is listed too. Bundled skills (`code-review`) and plugin
skills (`clawd-status:clawd`) are untouched; use `skills.off` for those.

`zen-role check` validates the file: unknown keys, types, the window minimum, `saved`
against the presets, `skills.only` names against the disk, the `append` file, and for
an isolated role its `instructions.keep` titles against the global `CLAUDE.md`.
`zen-role show <name> [--json]` prints the mode, the flags and the settings overlay a
role resolves to on this machine, plus the kept sections and skill names of an
isolated role.

## Overlay mode

The default, and what all six roles use. The global layer (`~/.claude/settings.json`, the user and
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

## Isolate mode

`"isolate": true` on a role. The session loads only what the role names instead of
laying it over everything. No role has it on; it exists so a role can drop global
instructions, which overlay cannot.

On top of the overlay steps the shim passes `--setting-sources local` and builds three
things at launch, from the live files, into the run set. Nothing generated is tracked,
so there is no copy to keep in step.

| Built | From |
|---|---|
| `settings.json` | the live `~/.claude/settings.json` (every hook, the permission rules, the status line), then the role overlay, then agent-manager's file |
| `instructions.md` | the `instructions.keep` sections of the global `CLAUDE.md` in file order, then the working directory's root `CLAUDE.md` when there is one, then the role's `append` file; passed as `--append-system-prompt-file` |
| `plugin/` | a plugin named `role` whose `skills/` links to each `skills.only` skill; passed as `--plugin-dir` |

A kept skill is therefore listed as `role:<name>` (`role:zen-flow`,
`role:chrome-browser`), not under its usual name. A preset that says "use the zen-flow
skill" still finds it. A role cannot keep both a skill and its `anthropic-skills:`
twin, since both would be `role:<name>`.

`--setting-sources local` also drops the user-scope MCP servers, so the shim passes
`--mcp-config <repo>/agents/mcp.json` beside agent-manager's own. Checked against the
real CLI (2.1.330): the session lists `agent-manager`, the `agents/mcp.json` servers
and the account connectors (Notion included). There is no `--strict-mcp-config`.

### What an isolated role does not load

Confirmed with headless runs of a throwaway isolated role, one of them inside this
repo:

- the user `CLAUDE.md`, beyond the kept sections
- the user and synced skill dirs, beyond `skills.only`
- the project layer: `.claude/settings.json`, `.claude/skills/`, and nested
  `CLAUDE.md` files such as `.claude/CLAUDE.md` (only the root one is appended)
- plugins that load from the user skills dir (`clawd-status`)
- MCP servers added by hand with `claude mcp add` and absent from `agents/mcp.json`

Limits to know before turning it on for a role:

- Only `CLAUDE.md` in the launch directory is appended, as plain text: a launch from
  a subdirectory gets none, and `@path` imports in it are not expanded.
- The global settings ride in the `--settings` file, which the CLI ranks above
  `.claude/settings.local.json`, so a local scalar that overrides the global one in
  overlay mode loses to it here.
- A fence left open in the global `CLAUDE.md` hides the titles after it.

Still loaded: auto-memory (the project's memory dir), `.claude/settings.local.json`,
bundled skills, and whatever `enabledPlugins` holds after the merge.

A kept section that no longer exists in the global `CLAUDE.md`, or an unreadable
global `settings.json`, stops the launch with one line: a renamed section must not
silently drop its rules.

## Explorer role and the Explore shadow

Read-only exploration runs on Haiku (task #1009). Two pieces, neither moves a role that
writes, reviews or decides:

- **`explorer` role** (`role-explorer`, preset `explorer`): `model: haiku`, no effort,
  `tools.deny` of `Edit`, `Write`, `NotebookEdit`, spec-writer's overlay, skills
  `zen-flow` only. Chief's step 12 spawns it for question-only investigation, research
  and support digging; digging that may change code or data stays `role-worker`. Bash
  stays (search needs it); the preset forbids writes through it.
- **`Explore` subagent**: `.claude/agents/Explore.md` (linked to `~/.claude/agents/`)
  is a user-level agent named `Explore`, which replaces the built-in. It sets
  `model: haiku` and `disallowedTools: Edit, Write, NotebookEdit`. The built-in runs on
  the parent's model (Opus on a subscription).
- `CLAUDE_CODE_SUBAGENT_MODEL` and `_FORCE` stay unset: they would also move
  `/code-review` and every `general-purpose` subagent. `Plan`, `fork` and the designer
  critics keep the parent's model.

Compare tokens before and after with `zen-telemetry report --by-model`
([[agent-telemetry]]).

| To undo | Do |
|---|---|
| Explore on Haiku | delete `.claude/agents/Explore.md` |
| explorer role | chief's step 12 back to `role-worker`; remove the `explorer` entry from `roles.json` and the preset from `config.json`. `zen-role build` reports the stale `role-explorer` profile and never removes it: delete it in agent-manager |
| both | `git revert` the commit, then rerun `agent-manager/link` |

A wrong or redone explorer answer gets a dated note on its task; two in a week means roll
back.

## Reverting a cut

Each cut is one line in `roles.json`. Delete it and the next launch or revive of that
role runs without it; agent-manager needs no restart.

| To undo | Remove |
|---|---|
| the skill cut | the role's `skills` key (or add the skill to `only`) |
| the output style | `settings.outputStyle` |
| the plugin cut | the entry under `settings.enabledPlugins` |
| the compaction window | `autocompact` |
| isolate mode | `isolate` (the role falls back to overlay; `instructions` is then unused) |
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
Isolate mode was checked the same way with a throwaway role.
