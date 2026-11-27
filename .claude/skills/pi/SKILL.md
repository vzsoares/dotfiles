---
name: pi
description: How the pi coding agent (docs.pi.dev) works and how this repo's pi-box wraps it. Use before running, configuring or reasoning about pi — pi CLI flags, print/json/rpc modes, PI_CODING_AGENT_DIR, the agent dir, settings.json, models.json, auth.json, pi mcp (native MCP, exposure modes, codemode, tool_search), pi install / packages, pi skills, prompts, extensions, sessions (resume, fork, tree, compaction), project trust — and for pi-box (sealed Docker box, spend cap, contract.py, RESULT.md). pi behaves unlike Claude Code, Codex and agy; do not assume their behaviour.
---

# pi

pi is a terminal coding agent by Earendil (npm `@earendil-works/pi-coding-agent`).
This skill covers **pi 1.6.0** (docs scraped 2026-11-20; see
`references/SOURCES.json`). The pi-box image pins an older pi (see
`pi-box/README.md`), so check `pi --version` before trusting a new flag.

Read the core below first; open a `references/` file only for the topic at hand.

| Topic | File |
|---|---|
| Install, CLI flags, slash commands, keys, TUI | `references/cli.md` |
| Agent dir, settings.json, env vars, themes | `references/config.md` |
| Models, providers, models.json, auth | `references/models.md` |
| MCP servers, exposure, OAuth | `references/mcp.md` |
| Skills, prompt templates, packages | `references/skills-prompts-packages.md` |
| Extensions API, codemode | `references/extensions.md` |
| Sessions, tree, compaction, session file format | `references/sessions.md` |
| Print/JSON/RPC modes, SDK | `references/automation.md` |
| Security, project trust, containers, agent loop | `references/security.md` |

## Hard rules in this repo

- **Never start a paid pi run on your own.** Any pi call that reaches a paid
  model (host pi or pi-box, interactive or `-p`) needs the user's approval
  first, filed as a cockpit ask: `zen-inbox ask <task> --prod run --repo
  dotfiles --cmd "<exact command>"`. Reading docs, `pi --help`, `pi
  --version`, `pi mcp list` and `pi list` are free.
- Never read, print or echo provider keys (`VENICE_API_KEY`, `auth.json`,
  `pi auth print-api-key`).
- pi config lives in this repo; edit it here, never in `~/.pi` directly
  (see "Config in this repo").

## How pi differs from Claude Code, Codex and agy

| Area | pi | Claude Code | Codex / agy |
|---|---|---|---|
| Config home | `~/.pi/agent` (`PI_CODING_AGENT_DIR`) + project `.pi/` | `~/.claude` + `.claude/` | `~/.codex` (`CODEX_HOME`) / `~/.gemini/config` |
| Instructions | `AGENTS.md` **or** `CLAUDE.md`, walked up from cwd + agent dir; `AGENTS.override.md` wins in its dir | `CLAUDE.md` | `AGENTS.md` |
| System prompt | `SYSTEM.md` replaces, `APPEND_SYSTEM.md` appends (agent dir or `.pi/`) | output styles, `--append-system-prompt` | profile / config |
| Approvals | **None per tool call.** Tools run with your OS permissions; only project *trust* gates loading `.pi/` resources | permission prompts, modes | approval + sandbox modes |
| Default tools | `read bash edit write` (+ `grep find ls` opt-in) | many built-ins | shell + patch |
| MCP | **Native.** `pi mcp add/list/remove`; tools named `mcp__<server>__<tool>`; default exposure `codemode` (not declared to the model, reached via a JS `codemode` tool) | native, tools declared directly | native (`codex mcp add`, `agy mcp add`) |
| Hooks | TypeScript **extensions** in-process (events, tools, commands, UI) | shell hooks in settings.json | their own config; not pi's |
| Skills | `SKILL.md` dirs in agent dir, `.pi/skills`, `~/.agents/skills`, `.agents/skills`; invoked as `/skill:name` | `~/.claude/skills`, `/name` | `~/.codex/skills` / `~/.gemini/config/skills` |
| Plugins | **pi packages** from npm/git: `pi install npm:…` | plugins / marketplaces | — |
| Sessions | JSONL **tree** in `~/.pi/agent/sessions/<cwd>`; `/tree`, `/fork`, `/clone` branch in place | linear transcripts, `--resume` | linear, `resume` |
| Headless | `pi -p "…"`, `--mode json` (JSONL events), `--mode rpc` (stdin commands), TS SDK | `claude -p`, stream-json | `codex exec` |
| Models | any provider; custom endpoints in `models.json`; `--model provider/id:thinking` | Anthropic only | vendor models |

Source: https://docs.pi.dev/latest/configuration, https://docs.pi.dev/latest/cli,
https://docs.pi.dev/latest/mcp, https://docs.pi.dev/latest/security

## Install and update

- Installer: `curl -fsSL https://pi.dev/install.sh | sh` (pins deps), or
  `npm install -g --ignore-scripts @earendil-works/pi-coding-agent` (Node ≥ 22.19).
- `pi update` updates pi itself; `--extensions` the packages, `--models` the
  model catalogs, `--all` everything. A pi installed by another manager
  needs `PI_SELF_UPDATE_COMMAND` or the `selfUpdateCommand` setting.
- `pi --version`, `pi --help` (help includes flags added by loaded extensions).

Source: https://docs.pi.dev/latest/quickstart, https://docs.pi.dev/latest/cli

## Modes and the flags agents need

```
pi                                   # interactive TUI in cwd
pi -p "task"                         # one shot, final text to stdout
git diff | pi -p "review"            # stdin is prepended to the prompt
pi --mode json "task" > events.jsonl # one shot, JSONL agent events
pi --mode rpc                        # JSONL commands on stdin until shutdown
```

- `-p` exits non-zero on error/abort; `--mode json` does **not** — read the
  events, and treat `agent_settled` (not `agent_end`) as done.
- Redirected stdin/stdout without `--mode json|rpc` falls back to print mode.
  `--mode text` does **not** imply one-shot; use `-p`.
- `@path` adds a file/image to the first prompt; `--` ends option parsing.
- Model: `--model <pattern>` (exact, fuzzy, `provider/id`, `:high` suffix),
  `--provider`, `--thinking off|minimal|low|medium|high|xhigh|max`,
  `--models a,b` (cycling scope), `--list-models [q]`.
- Tools: `--tools read,grep` replaces the set; `--tools +codemode,-write`
  edits the default set (no mixing the two forms); `--exclude-tools 'mcp__*'`,
  `--no-tools`, `--no-builtin-tools`, `--no-mcp`.
- Resources: `-e <ext>`, `--skill <path>`, `--prompt-template <path>`, and
  `--no-extensions` / `--no-skills` / `--no-prompt-templates` /
  `--no-context-files`.
- Prompt: `--system-prompt <text|file>`, `--append-system-prompt` (repeatable).
- Trust: `-a/--approve` trusts project files for this run, `-na` ignores them.
- `--offline` (= `PI_OFFLINE=1`) stops catalog refreshes and other network
  chatter.

Inside the TUI: `Enter` steers mid-run, `Alt+Enter` queues a follow-up,
`Escape` aborts, `!cmd` runs a shell command into context (`!!` keeps it
out), `/model`, `/thinking`, `/settings`, `/reload` after editing config.

Source: https://docs.pi.dev/latest/cli, https://docs.pi.dev/latest/usage

## Agent dir, project dir and env vars

`<agent-dir>` = `PI_CODING_AGENT_DIR`, default `~/.pi/agent`:

| File | Holds |
|---|---|
| `settings.json` | defaults (`defaultProvider`, `defaultModel`, `defaultTools`, `defaultProjectTrust`), packages, resource paths |
| `models.json` | custom providers/endpoints, model overrides, costs |
| `auth.json` | saved API keys and OAuth tokens (`/login`, `pi auth login`) — secret |
| `mcp.json` | user MCP servers (`mcpServers`) |
| `keybindings.json` | key overrides |
| `AGENTS.md` / `CLAUDE.md` | global instructions |
| `SYSTEM.md`, `APPEND_SYSTEM.md` | replace / extend the system prompt |
| `extensions/ skills/ prompts/ themes/` | user resources |
| `sessions/` | session JSONL files, grouped by cwd |

Project `.pi/` mirrors it (`settings.json`, `mcp.json`, `SYSTEM.md`,
`extensions/`, …) and loads only once the project is trusted; context files
(`AGENTS.md`, `CLAUDE.md`) load **without** trust. Precedence: CLI flag >
project > user. `sessionDir` is read before trust.

Env: `PI_CODING_AGENT_DIR`, `PI_CODING_AGENT_SESSION_DIR`, `PI_OFFLINE`,
`PI_SKIP_VERSION_CHECK`, `PI_TELEMETRY`, `PI_PACKAGE_DIR`. pi's `bash` tool
exports `PI_SESSION_ID`, `PI_SESSION_FILE`, `PI_PROVIDER`, `PI_MODEL`,
`PI_REASONING_LEVEL` to every command — read those to learn which model runs.

Source: https://docs.pi.dev/latest/configuration,
https://docs.pi.dev/latest/environment-variables, https://docs.pi.dev/latest/settings

## Models and auth

- `/login` or `pi auth login <provider>` (`--api-key`, `--no-browser`) stores
  credentials in `auth.json`; env vars and `models.json` `apiKey` also work.
- `models.json` `apiKey` accepts a literal, `$ENV_VAR`, or `!command` whose
  stdout is the key.
- `pi auth check --provider <p>` prints `ready` / `not_ready` / `invalid`
  without spending tokens.

Source: https://docs.pi.dev/latest/providers, https://docs.pi.dev/latest/models-configuration

## MCP (native)

```
pi mcp add <name> [--env K=V] [--cwd D] -- <cmd> [args]   # stdio
pi mcp add <name> --url <u> [--bearer-token-env-var NAME] # HTTP
pi mcp add -l …        # project .pi/mcp.json instead of ~/.pi/agent/mcp.json
pi mcp list [--json]   # connects; exit 1 if a server fails
pi mcp remove|login|logout <name>
```

Exposure per server (`exposure`) or per tool (`toolExposure`):
`codemode` (default — callable only from `codemode` JS scripts),
`deferred` (loaded via `tool_search`), `direct` (declared like a built-in),
`hidden`. pi turns on `codemode`/`tool_search` itself when a server needs
them. Large text results (>20 KB) are middle-truncated with the full text in
a temp file. `/mcp` manages servers interactively.

Source: https://docs.pi.dev/latest/mcp

## Skills, prompts, extensions, packages

- **Skills:** a dir with `SKILL.md` (`name`, `description` required;
  `disable-model-invocation: true` hides it from auto-selection). Only the
  description is in context; the body loads on demand. Run with
  `/skill:<name> args`. Name collisions keep the first found.
- **Prompt templates:** `prompts/*.md` become `/name` commands.
- **Extensions:** TypeScript modules run in the pi process; they register
  tools, commands, shortcuts, providers and event handlers (pi's answer to
  hooks). They have full process permissions.
- **Packages:** bundles of the above from npm or git:
  `pi install npm:@scope/pkg` (`-l` for project), `pi list`, `pi remove`,
  `pi config` to toggle resources.

Source: https://docs.pi.dev/latest/skills, https://docs.pi.dev/latest/extensions,
https://docs.pi.dev/latest/packages

## Sessions

- Saved automatically as a JSONL **tree**; the active branch is the context.
- `pi -c` continues the latest session for cwd, `pi -r` picks one,
  `--session <path|id>`, `--session-id <id>` (open or create with that id),
  `--fork <path|id>`, `--no-session` (in memory), `-n <name>`.
- In the TUI: `/new`, `/resume`, `/name`, `/session`, `/tree` (jump to an
  earlier entry = new branch), `/fork`, `/clone`, `/export`, `/share`
  (uploads — never on private work).
- Auto-compaction fires when context > window − `reserveTokens` (default
  16384); `/compact [focus]` by hand. Old entries stay in the file.

Source: https://docs.pi.dev/latest/sessions, https://docs.pi.dev/latest/compaction

## Project trust and safety

pi asks before loading a folder's `.pi/` settings, extensions, skills,
prompts and project MCP servers (`defaultProjectTrust`: `ask` by default,
or `always` / `never`; agent-dir settings only — pi-box sets `always`).
Trust is not a sandbox: once running, tools have the full permissions of the
pi process and pi does not confirm each call. Run unattended or untrusted
work in a container — this repo's pi-box.
Headless runs (`-p`, json, rpc) cannot answer the trust prompt, so they skip
project `.pi/` resources unless `-a`, a saved `/trust` or the setting allows.

Source: https://docs.pi.dev/latest/security, https://docs.pi.dev/latest/containerization

## Config in this repo

- `agents/settings.json`, `agents/models.json`, `agents/mcp.json` →
  linked into `~/.pi/agent` by `agents/link pi` (with `.claude/CLAUDE.md` as
  `AGENTS.md`). `agents/mcp.json` is the one MCP list for every agent.
- pi runs in `tuiMode: "regular"` (host and pi-box) so tmux copy-mode handles
  scrolling; `/settings` or removing the key switches back to fullscreen.
- Shared skills (this one, `wiki`, `second-brain`) are linked into
  `~/.agents/skills`, `~/.codex/skills` and `~/.gemini/config/skills` by
  `agents/link`; Claude Code gets them through `./link-claude`.

## pi-box (how this repo runs pi)

Full detail: `pi-box/README.md`. Repo paths, `zen-inbox` and `agent-manager`
below are host-only; inside the box this section is background. The short
version:

- **Sealed box.** `pi-box` (the `pi-box/wrapper`, on PATH via `./link-bin`)
  runs pi in Docker with only `~/pi-box/work/<task-id>` (rw), `~/pi-box/in`
  (ro) and `~/pi-box/agent` (mounted as the box's `~/.pi/agent`). No host
  repos, secrets or services; private network ranges are rejected; env is an
  allowlist. The wrapper exits 2 outside `~/pi-box/work/<task-id>/` and 3 when
  that task's container already runs.
- **Browser.** The one way out: the `browser` MCP server (`mcp.json`, reset by
  the wrapper on every start) drives the user's logged-in Chrome through the
  paired agent-browser-bridge extension. Only the run that got host port 9242
  has the tools; `PIBOX_BROWSER=0` skips it.
- **Agent dir.** `pi-box/link` copies `pi-box/agent/*` (settings, models, mcp,
  AGENTS.md) into `~/pi-box/agent` and refreshes the box skills (including a
  copy of this one). Copies, not links: the repo is not mounted in the box.
- **Models.** `pi-box/agent/models.json` holds Venice only: default
  `glm-5-3-flash-uncensored`, fallback `deepseek-v4-1-flash-uncensored`.
- **Spend cap.** Every start goes through `pi-box/spend.py`: $10/month cap
  (`PIBOX_CAP_USD`), warn at 80 %, a per-call reserve; exit 4 when over.
  Ledger `~/pi-box/ledger.jsonl`; `pi-box cost` prints the month.
- **Handoff.** `python3 pi-box/contract.py command <task> goal.md` prints the
  `agent-manager spawn --tool pi --env pi-box …` line; that line goes in a
  `zen-inbox ask <task> --prod run` for the user to approve. The session
  writes `out/` and `RESULT.md` (`## Summary`, `## Sources`,
  `## Left undone`, all non-empty) and ends with `WORKER_DONE <task>`;
  `python3 pi-box/contract.py check <task>` verifies it.
- Free checks: `PIBOX_DRY_RUN=1 pi-box`, `pi-box doctor`, `pi-box cost`.

## Refresh this skill when pi changes

```
python3 .claude/skills/pi/scripts/refresh.py
```

It fetches every `docs.pi.dev/latest` page (1 s apart), saves their text to a
fresh temp dir (or `--out`), prints the pi version from the changelog feed, and lists pages
added, removed or changed since `references/SOURCES.json` (exit 3 on drift,
0 when current). Re-read the changed pages, update the matching
`references/` file and this core (paraphrase, keep the `Source:` lines),
then record the new state with `--write`.

Limit: it only finds pages linked from the docs index (the site has no
sitemap or `llms.txt`), so a page reachable only from another page is missed.
