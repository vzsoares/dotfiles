# pi configuration, settings and environment

Source: pi 1.6.0 docs, scraped 2026-11-20. Re-check with scripts/refresh.py.

## Layout: agent dir vs project `.pi/`

- User-level config lives in the agent dir, default `~/.pi/agent` (NOT `~/.claude` or `~/.codex`).
  Relocate with `PI_CODING_AGENT_DIR` (or SDK `agentDir`).
- Project config lives in `<cwd>/.pi/` and only loads after the project is trusted. Exception:
  `sessionDir` is read before trust so sessions can be located.
- Change via `/settings`, by editing files, or by asking pi; run `/reload` after manual edits.

| Agent dir (`~/.pi/agent/`) | Project (`.pi/`) | Purpose |
|---|---|---|
| `settings.json` | `settings.json` | Settings, resource paths, package list |
| `keybindings.json` | -- | Key remaps |
| `mcp.json` | `mcp.json` | MCP servers (project one needs trust) |
| `models.json` | -- | Custom endpoints/models/overrides |
| `auth.json` | -- | Stored API keys + OAuth tokens (keep private) |
| `AGENTS.md` etc. | (context files, see below) | Global instructions |
| `SYSTEM.md` | `SYSTEM.md` | Replace default system prompt |
| `APPEND_SYSTEM.md` | `APPEND_SYSTEM.md` | Append to system prompt |
| `extensions/`, `skills/`, `prompts/`, `themes/` | same names | Discovered resources |

- `SYSTEM.md` / `APPEND_SYSTEM.md`: a trusted project file wins over the agent-dir file of the same name;
  they are not concatenated.
- Skills dir is `~/.pi/agent/skills/` / `.pi/skills/` (not `~/.claude/skills`). Prompt templates in
  `prompts/` become slash commands.

Source: https://docs.pi.dev/latest/configuration

## Context files (AGENTS.md / CLAUDE.md)

- Accepted names: `AGENTS.override.md`, `AGENTS.md`, `AGENTS.MD`, `CLAUDE.md`, `CLAUDE.MD`. pi reads both
  the Codex-style `AGENTS.md` and Claude-style `CLAUDE.md`.
- Loaded from the agent dir, the cwd, and every parent dir; each applies to its dir and below.
- `AGENTS.override.md` replaces `AGENTS.md`/`CLAUDE.md` only within its own directory; it does not hide
  files from other dirs or the agent dir.
- Context-file discovery does not require project trust (unlike `.pi/`). Disable with `-nc`.

Source: https://docs.pi.dev/latest/configuration

## Settings precedence and merge rules

- Project `.pi/settings.json` overrides `~/.pi/agent/settings.json`; resource lists are unioned.
- Some keys are agent-dir only: `defaultProjectTrust`, `httpProxy`, `selfUpdateCommand`, `cacheWarming`.
- Editor validation: `"$schema": "https://pi.dev/schemas/settings.schema.json"`.
- CLI flags override settings for one run (e.g. `--tools`, `--session-dir`, `--tui-mode`).
- Resource paths: user settings resolve relative to the agent dir, project settings relative to `.pi/`;
  absolute and `~` allowed. Arrays support `!glob` exclusion, `+path` exact include, `-path` exact exclude.

Source: https://docs.pi.dev/latest/settings

## Model, interaction and tool settings

| Key | Default | Notes |
|---|---|---|
| `defaultProvider`, `defaultModel` | auto | Startup model |
| `defaultThinkingLevel` | `"medium"` | `off minimal low medium high xhigh max` |
| `modelThinkingLevels` | -- | keyed by exact `provider/modelId` |
| `thinkingBudgets` | built-in | tokens for minimal..high |
| `enabledModels` | all | patterns like `--models`, for cycling |
| `cacheWarming` | `"idle"` | `off`/`streaming`/`idle` (keeps cache warm up to 30 min) |
| `steeringMode`, `followUpMode` | `"one-at-a-time"` | or `"all"` |
| `externalEditor` | `$VISUAL`/`$EDITOR` | |
| `doubleEscapeAction` | `"tree"` | `fork`, `none` |
| `defaultProjectTrust` | `"ask"` | `always`, `never`; agent dir only |
| `defaultTools` | read,bash,edit,write | plain list replaces; `+x`/`-x` edits |
| `codemode.mode` | `"on"` | `"only"` hides other tools behind codemode |
| `codemode.inlineBudget` | `3000` | token budget for tool declarations |

- `defaultTools` layering: a project list of only `+`/`-` entries edits the user's selection; any plain
  name replaces it. `[]` disables built-ins only (extension/SDK tools stay). `/reload` adds newly listed
  tools but does not disable removed ones.
- Example: `"defaultTools": ["-bash", "+powershell", "+grep"]`.

Source: https://docs.pi.dev/latest/settings

## Sessions, compaction, display

| Key | Default | Notes |
|---|---|---|
| `sessionDir` | agent session dir | relative -> cwd; env and `--session-dir` override |
| `sessionShareDestination` | `"auto"` | `gist`, `radius` |
| `compaction.enabled` | `true` | |
| `compaction.reserveTokens` | `16384` | free tokens that trigger auto-compaction |
| `compaction.keepRecentTokens` | `20000` | kept verbatim |
| `compaction.modelOverrides` | -- | per `provider/modelId` |
| `branchSummary.reserveTokens` / `skipPrompt` | `16384` / `false` | |
| `theme` | `"system"` | `"light/dark"` pair form allowed |
| `quietStartup` | `false` | `true` or `"header"` |
| `tuiMode` | `"fullscreen"` | `regular` uses native scrollback |
| `fullscreenCopyOnSelect` | `true` | |
| `showHardwareCursor` | `false` | IME help |
| `terminal.showImages` / `imageWidthCells` | `true` / `60` | |
| `terminal.hyperlinks` / `images` / `trueColor` | `"auto"` | override detection |
| `images.autoResize` | `true` | <= 2000x2000 before sending |
| `images.blockImages` | `false` | never send images |
| `markdown.mermaid` | `"streaming"` | `off`, `final` |

Source: https://docs.pi.dev/latest/settings

## Network, shell, resources, telemetry

| Key | Default | Notes |
|---|---|---|
| `transport` | `"auto"` | `sse`, `websocket`, `websocket-cached` |
| `httpProxy` | -- | sets HTTP(S)_PROXY for pi clients; agent dir only |
| `httpIdleTimeoutMs` | `300000` | `0`/`"disabled"` off |
| `retry.enabled` / `maxRetries` | `true` / `3` | agent-level retries |
| `retry.baseDelayMs` / `maxAgentDelayMs` | `2000` / `60000` | |
| `retry.provider.maxRetries` | `0` | keep 0 so pi handles quota errors |
| `shellPath` | platform | `~` allowed |
| `shellCommandPrefix` | -- | prepended to every bash command |
| `npmCommand` | `["npm"]` | argv for package installs |
| `selfUpdateCommand` | -- | argv; env `PI_SELF_UPDATE_COMMAND` wins |
| `packages`, `extensions`, `skills`, `prompts`, `themes` | `[]` | resource lists |
| `enableSkillCommands` | `true` | `/skill:name` |
| `enableInstallTelemetry` | `true` | anonymous install pings |
| `enableAnalytics` | `false` | |
| `warnings.anthropicExtraUsage` | `true` | warns on paid extra usage |

- Built-in extensions: `builtin:mcp`, `builtin:llama.cpp`, `builtin:codemode`, `builtin:tool-search`.
  Loaded by default; disable with `"extensions": ["-builtin:mcp"]` (project setting can override user).

Source: https://docs.pi.dev/latest/settings

## Environment variables

Markers pi sets for children: `AI_AGENT=pi`, `PI_CODING_AGENT=true` (CLI/RPC only, not SDK).

Injected into the model's `bash`/`powershell` tool calls (not into user `!` commands), resolved per command:
`PI_SESSION_ID`, `PI_SESSION_FILE` (unset when ephemeral), `PI_PROVIDER`, `PI_MODEL`,
`PI_REASONING_LEVEL`. To learn the running model, read these rather than guessing:

```
printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
```

Read by pi:

| Var | Purpose |
|---|---|
| `PI_CODING_AGENT_DIR` | agent dir (default `~/.pi/agent`) |
| `PI_CODING_AGENT_SESSION_DIR` | session storage (below `--session-dir`) |
| `PI_PACKAGE_DIR` | package dir (Nix/Guix) |
| `PI_OFFLINE` | no automatic network activity |
| `PI_SKIP_VERSION_CHECK` | skip pi.dev version request |
| `PI_SELF_UPDATE_COMMAND` | custom `pi update` command |
| `PI_TELEMETRY` | `1/true/yes` or `0/false/no` |
| `PI_CODEMODE_MAX_WORKERS` | default `2`, 256 MB each |
| `PI_CACHE_RETENTION` | `long` for extended prompt caching |
| `PI_SHARE_VIEWER_URL`, `PI_RADIUS_GATEWAY` | share / bug upload endpoints |
| `PI_CLEAR_ON_SHRINK`, `PI_HARDWARE_CURSOR` | display tweaks |
| `PI_CLIPBOARD_OSC52` | also copy via OSC 52 (remote tmux, IDEs) |
| `PI_HYPERLINKS`, `PI_IMAGE_PROTOCOL`, `PI_TRUE_COLOR`, `PI_PROGRAM_STATUS` | capability overrides |
| `PI_TUI_ESC_TIMEOUT` | ms to wait after ESC (100 over SSH, 10 locally) |
| `VISUAL`, `EDITOR`, `HTTP_PROXY`, `HTTPS_PROXY` | standard |

Provider keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, ...) are listed in models.md.

Source: https://docs.pi.dev/latest/environment-variables

## Themes

- Built-ins: `system` (default; derives colors from the terminal's reported palette and re-queries on
  light/dark switch), `dark`, `light`. `"theme": "light/dark"` follows terminal appearance.
- Detection fallback order when no background is reported: terminal light/dark notification, `COLORFGBG`,
  then dark. `--use-theme <name>` for one run.
- Custom theme: `<agent-dir>/themes/<name>.json`, file name == `name`; only that path hot-reloads, other
  sources need `/reload`. Project themes in `.pi/themes/` need trust. Names must be unique, no `/`, not
  `system`.
- Top-level keys: `$schema`, `name` (req), `appearance`, `vars`, `colors` (req), `export`. Strict: extra
  keys are rejected; put custom colors in `vars`.
- Color forms: hex, `oklch(...)`, `okhsl(...)`, 0-255 index, var reference, `""` (terminal default).
- Optional fallbacks: `scrollbarTrack`->`muted`, `scrollbarThumb`->`text`, `searchMatchBg`->`selectedBg`,
  `searchMatchText`->`text`, `thinkingMax`->`thinkingXhigh`.

Source: https://docs.pi.dev/latest/themes
