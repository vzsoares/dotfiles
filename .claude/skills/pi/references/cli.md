# pi CLI, TUI and terminal

Source: pi 1.6.0 docs, scraped 2026-11-20. Re-check with scripts/refresh.py.

## Install, start, uninstall

- Installers (pin all deps, self-update via `pi update`): `curl -fsSL https://pi.dev/install.sh | sh`
  (Windows: `powershell -c "irm https://pi.dev/install.ps1 | iex"`).
- npm (Node >= 22.19, transitive deps not pinned): `npm install -g --ignore-scripts @earendil-works/pi-coding-agent`.
  Lifecycle scripts are not needed.
- Nix: `nix profile add github:earendil-works/pi/stable` (older Nix: `nix profile install`); update with
  `nix profile upgrade pi` -- `pi update` cannot self-update a Nix install.
- Verify with `pi --version`. Start with `pi` inside the target folder: cwd drives file discovery, context
  files, project config and session grouping.
- First run: `/login` (subscription OAuth or API key), then `/model`.
- pi does NOT ask permission before each tool call (no approval prompts like Claude Code / Codex). Use git,
  backups, or a container/sandbox for untrusted or unattended work.
- Uninstall (npm uninstall / re-run installer / `nix profile remove pi`) leaves `~/.pi/agent/` (config,
  credentials, sessions, packages) in place.
- Customization ladder, least to most powerful: `AGENTS.md` -> prompt template -> skill -> extension ->
  custom TUI component -> custom provider -> pi package.

Source: https://docs.pi.dev/latest/quickstart

## Interactive use

- `Enter` sends; `Shift+Enter` (or `Ctrl+J`) newline; `Ctrl+G` opens external editor.
- `@` fuzzy-picks a file into the prompt; `Tab` completes paths; images can be pasted/dragged.
- While pi works: `Enter` = steering message (delivered after current response + its tool calls);
  `Alt+Enter` = follow-up (delivered after the whole task); `Alt+Up` pulls queued messages back;
  `Escape` aborts (queued messages return to the editor).
- `!cmd` runs a shell command and adds output to the conversation; `!!cmd` runs it without sending output to
  the model.
- `Ctrl+O` toggle tool output, `Ctrl+T` toggle thinking blocks, `Ctrl+X` / `/copy` copy last reply.
- `/export` -> HTML or JSONL. `/share` uploads and returns a link (Radius org if logged in to Radius,
  otherwise a private GitHub gist via `gh`; force with `sessionShareDestination`). Sessions may contain
  secrets -- review first.
- Sessions auto-save; `pi --continue` resumes the latest one for the cwd.
- `/debug` writes rendered lines + session messages to `pi-debug.log` in the agent dir (may contain secrets).
- Startup may ask whether to trust the folder before loading project `.pi/` resources.

Source: https://docs.pi.dev/latest/usage

## Command synopsis and run modes

```
pi [options] [--] [@files...] [messages...]
pi install|remove|uninstall <source> [-l]
pi update [target] [--extensions|--models|--all|--force]
pi list | pi config [-l]
pi auth login|logout <provider> | pi auth check|print-api-key|print-bearer-token
pi mcp add|remove|list|login|logout
```

| Option | Effect |
|---|---|
| `-p`, `--print` | Run prompts, print final assistant text to stdout, exit |
| `--mode text` | Text output; still opens TUI if stdin/stdout are a TTY (use `-p` for one-shot) |
| `--mode json` | Run prompts, emit JSONL events on stdout, exit |
| `--mode rpc` | JSONL commands on stdin, responses/events on stdout until shutdown; rejects `@file` |
| `--export <in> [out]` | Session file -> HTML, then exit |

- If stdin or stdout is redirected and no json/rpc mode is given, pi falls back to print mode.
- Piped stdin is prepended to the first prompt (`git diff | pi -p "Review this"`).
- `@path` resolves from cwd; `--` ends option parsing so a prompt can start with `-`.
- json/rpc reserve stdout for protocol records. There is also an SDK for embedding.

Source: https://docs.pi.dev/latest/cli

## Model, session and tool flags

Models:
- `--model <pattern>`: exact ID or fuzzy ID/name; accepts `provider/id` and `:<thinking>` suffix
  (`pi --model sonnet:high`). Multiple provider match -> prefers one with credentials, else asks for
  `--provider`.
- `--provider <name>` (needs `--model`); `--api-key <key>` (non-persistent; needs `--model`/`--models`).
- `--thinking off|minimal|low|medium|high|xhigh|max` (overrides suffix, clamped to model).
- `--models <patterns>` comma list (globs ok) scoping startup + `Ctrl+P` cycling.
- `--list-models [search]` lists and exits.

Sessions:
- `-c/--continue` latest for cwd; `-r/--resume` picker (Tab = all dirs); `--session <path|id>` (partial
  ID ok; other-dir match offers a fork); `--session-id <id>` open-or-create in cwd; `--fork <path|id>`;
  `--no-session` (in-memory); `-n/--name`; `--session-dir <dir>` (beats `PI_CODING_AGENT_SESSION_DIR` and
  `sessionDir`).
- `--fork` excludes `--session/--continue/--resume/--no-session`. `--session-id` excludes
  `--session/--continue/--resume` but may pair with `--fork` to name the fork.

Tools (defaults: `read`, `bash`, `edit`, `write`; other built-ins: `grep`, `find`, `ls`, `powershell`):
- `-t/--tools a,b` plain names = full allowlist (`*` patterns ok). Only `+x`/`-x` entries = edit the default
  set (exact names only, no mixing with plain names): `pi --tools +codemode,-write`.
- `-xt/--exclude-tools` disables names/patterns last, MCP included. `-nbt/--no-builtin-tools`;
  `-nt/--no-tools` disables everything.
- MCP tools survive `--tools` unless some entry starts with `mcp__` (`'mcp__radius__*'`). Remove them with
  `--exclude-tools 'mcp__*'` or `--no-mcp`.
- Off-by-default built-in-extension tools: `codemode` (QuickJS sandbox; scripts call `tools.<name>()`,
  can run calls in parallel, use `models.classify()` / `models.generateImages()`) and `tool_search`
  (declares deferred tools). Enable via `"defaultTools": ["+codemode"]` or `--tools +codemode`.

Source: https://docs.pi.dev/latest/cli

## Resource, prompt and process flags

- `-e/--extension <path|builtin:name>` (repeatable); `-ne/--no-extensions` (also kills built-ins; explicit
  `-e` still loads: `pi -ne -e builtin:mcp`); `--no-mcp`.
- `--skill <path>` / `-ns`; `--prompt-template <path>` / `-np`; `--theme <path>` / `--no-themes`;
  `--use-theme <name|light/dark>`. Explicit paths load even with the `--no-*` switch.
- `-nc/--no-context-files` disables `AGENTS.md` and `CLAUDE.md` discovery.
- `--system-prompt <text|file>` replaces; `--append-system-prompt <text|file>` appends (repeatable).
- `--tui-mode fullscreen|regular`, `--verbose` (overrides `quietStartup`).
- `-a/--approve` trusts project-local config for this process; `-na/--no-approve` ignores it.
- `--offline` (= `PI_OFFLINE=1`) disables network chatter incl. catalog refresh.
- Extensions can add long flags (shown in `pi --help`); unknown short flags are rejected.

Source: https://docs.pi.dev/latest/cli

## Package, auth and MCP subcommands

- Packages: `pi install npm:@scope/pkg`, `pi list`, `pi remove <src>`, `pi config` (toggle package
  resources and built-ins). `-l/--local` writes project settings instead of global.
- `pi update` = pi itself; `--extensions` all packages; `<source>` one; `--models` refresh catalogs;
  `--all` both. Wrapped installs: `PI_SELF_UPDATE_COMMAND` (space-split, placeholders `{packageName}`,
  `{version}`, `{installSpec}`) or the `selfUpdateCommand` argv setting.
- `pi auth login <provider> [--api-key] [--no-browser]` (interactive TTY only); `pi auth logout <provider>`
  (exit 0 even if nothing stored).
- `pi auth check --provider X [--json] [--credentials] [--no-refresh]` -> `ready`/`not_ready`/`invalid`,
  exit 0/1/2. `print-api-key`, `print-bearer-token [--min-expiry 30m]` print secrets to stdout.
- MCP is built in (unlike Codex/Claude Code config files, it lives in pi's own `mcp.json`):
  - `pi mcp add <name> [--env K=V] [--cwd dir] -- <cmd> [args]` (stdio)
  - `pi mcp add <name> --url <url> [--header K=V] [--bearer-token-env-var NAME] [--oauth-*]` (HTTP)
  - `pi mcp remove|list [--json]|login|logout <name>`; `--exposure <mode>`, `--description` on add.
  - Writes `~/.pi/agent/mcp.json`, or `.pi/mcp.json` with `-l`. Project file read only when trusted.
    `add` does not connect -- run `pi mcp list` (exit 1 if any enabled server fails).
- `pi auth login radius` also registers the Radius MCP server.

Source: https://docs.pi.dev/latest/cli

## Slash commands (built-in)

| Area | Commands |
|---|---|
| Models | `/model [prov/id]`, `/thinking [lvl]`, `/scoped-models`, `/login [prov]`, `/logout`, `/llama`, `/settings` |
| Sessions | `/new`, `/resume`, `/name [n]`, `/session`, `/tree`, `/fork`, `/clone`, `/compact [instr]`, `/import <jsonl>` |
| Output | `/copy`, `/export [path]`, `/share`, `/bug [desc]` |
| Runtime | `/trust`, `/mcp`, `/reload`, `/hotkeys`, `/changelog`, `/quit`, `/debug` |

- Prompt templates appear as `/<template-name>`; skills as `/skill:<name>` (if `enableSkillCommands`);
  extensions register their own. `/reload` picks up keybindings, extensions, skills, templates, themes,
  context files.

Source: https://docs.pi.dev/latest/slash-commands

## Keybindings

File: `<agent-dir>/keybindings.json` (default `~/.pi/agent/keybindings.json`), action id -> key or list;
`[]` disables. `/reload` applies. Syntax `ctrl+shift+x`, modifiers `ctrl shift alt super`.

```
{ "$schema": "https://pi.dev/schemas/keybindings.schema.json",
  "app.session.new": "ctrl+shift+n" }
```

| Action | Default |
|---|---|
| `app.interrupt` | `escape` |
| `app.clear` / `app.exit` | `ctrl+c` (twice exits) / `ctrl+d` on empty editor |
| `app.editor.external` | `ctrl+g` (`externalEditor`, `$VISUAL`, `$EDITOR`) |
| `app.model.select` / `cycleForward` | `ctrl+l` / `ctrl+p` |
| `app.thinking.cycle` / `toggle` | `shift+tab` / `ctrl+t` |
| `app.tools.expand` | `ctrl+o` |
| `app.message.followUp` | `alt+enter` (`ctrl+q` on Windows/WSL) |
| `app.message.dequeue` | `alt+up` (`alt+q` on Windows/WSL) |
| `app.clipboard.pasteImage` | `ctrl+v` (`alt+v` on Windows/WSL) |
| `tui.input.newLine` | `shift+enter`, `ctrl+j` |
| `tui.altScreen.search` | `ctrl+shift+f` (fullscreen transcript search) |
| `app.suspend` | `ctrl+z` (none on native Windows) |

Session actions (`app.session.new/tree/fork/resume`) are unbound by default. Double `Escape` on an empty
editor opens `/tree` (`doubleEscapeAction`).

Source: https://docs.pi.dev/latest/keybindings

## Shell used by tools

- Built-in `bash` tool and `!`/`!!` run a fresh non-interactive `bash -c` per command: no aliases, no
  interactive rc files. Unix lookup: `/bin/bash`, `bash` on PATH, then `sh`.
- `shellPath` picks the executable; `shellCommandPrefix` is prepended (newline-joined) to every command,
  so keep it fast and non-interactive. Aliases: `"shellCommandPrefix": "shopt -s expand_aliases\nsource ~/.bash_aliases"`.
  Do not source a zsh rc into bash.
- Extensions replacing `bash` ignore these settings. `shopt` missing = fell back to `sh`.

Source: https://docs.pi.dev/latest/shell-aliases

## Terminals, tmux, Windows, Termux

- tmux >= 3.5: `set -g extended-keys on` + `set -g extended-keys-format csi-u`; 3.2-3.4: only
  `extended-keys on`. Restart the tmux server. tmux 3.6 may print `c0c/0c0c...` junk (upgrade).
- Ghostty: drop any old Claude Code `keybind = shift+enter=text:\n` (sends raw LF, indistinguishable from
  `Ctrl+J`). Alacritty/WezTerm on macOS: map Option+Enter to `\u001b[13;3u` for follow-ups.
- iTerm2 slow fullscreen scroll: Advanced > "Trackpad scrolls fast?" = No.
- Capability overrides (setting wins over env): `PI_HYPERLINKS` / `terminal.hyperlinks`,
  `PI_IMAGE_PROTOCOL` / `terminal.images`, `PI_TRUE_COLOR` / `terminal.trueColor`.
- pi emits OSC 7501 program status (`working`, `blocked`, `done`, `error`, `idle`) after the terminal
  confirms support; tmux/screen do not forward it. Force with `PI_PROGRAM_STATUS=1|0`.
- IME placement issues (WSL, IntelliJ): `PI_HARDWARE_CURSOR=1` or `showHardwareCursor`.
- Windows: bash resolution `shellPath` -> Git Bash under Program Files -> `bash.exe` on PATH. Optional
  `powershell` tool (native Windows only): `"defaultTools": ["-bash", "+powershell"]`; `!` stays bash.
  Prefer Windows Terminal; old conhost crashes on fullscreen<->regular (use `--tui-mode regular`).
- Termux: install via npm after `pkg install nodejs git`; `termux-setup-storage` for shared storage;
  `pkg install termux-api` for text clipboard (no image paste). Put Android hints in `~/.pi/agent/AGENTS.md`.
- TUI package `@earendil-works/pi-tui` is for extension authors (`ctx.ui.select/confirm/input/custom`);
  `PI_TUI_WRITE_LOG` captures raw ANSI for render debugging.

Source: https://docs.pi.dev/latest/terminal-setup, https://docs.pi.dev/latest/tmux, https://docs.pi.dev/latest/windows, https://docs.pi.dev/latest/termux, https://docs.pi.dev/latest/tui
