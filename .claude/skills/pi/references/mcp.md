# MCP in pi

Source: pi 1.6.0 docs, scraped 2026-11-20. Re-check with scripts/refresh.py.

MCP support is built in (a default-on extension named `builtin:mcp`), not a
third-party add-on. Transports: stdio and streamable HTTP. Legacy SSE is
rejected outright.

## Adding servers from the shell

```
pi mcp add filesystem -- npx -y @modelcontextprotocol/server-filesystem .
pi mcp add docs --url https://example.com/mcp --bearer-token-env-var DOCS_TOKEN
pi mcp add -l tools --env API_KEY='${TOOLS_KEY}' -- uvx tools-mcp
pi mcp list
```

- `pi mcp add` writes the user-level file by default; `--local` / `-l`
  targets the project file instead.
- Other shell subcommands: `pi mcp remove`, `pi mcp list`, `pi mcp login`,
  `pi mcp logout`. They need no running session and never load extensions,
  so they only see file-configured servers.
- `pi mcp list` actually connects to every enabled server, prints state,
  tools and errors, and exits 1 if any entry is invalid or any enabled
  server failed to connect. Good as a CI/health check.
- `--oauth-client-registration <auto|dcr|cimd>` sets the OAuth registration
  mode at add time.

Source: https://docs.pi.dev/latest/mcp

## Config files and precedence

| Scope   | Path                     | Notes                                   |
|---------|--------------------------|-----------------------------------------|
| User    | `~/.pi/agent/mcp.json`   | Put personal / credentialed servers here |
| Project | `.pi/mcp.json`           | Read only after project trust is granted |

- Top-level key is `mcpServers`, same shape as Claude Desktop / Claude Code /
  Cursor, so those entries can be pasted in as-is.
- Same-name project entry replaces the user entry wholesale, UNLESS the
  project entry has none of `command`, `url`, `type`: then it only overrides
  `enabled`, `exposure`, `toolExposure` and inherits env/headers/auth. Use
  `{ "enabled": false }` to switch a user server off in one project.
- Invalid entries are reported and skipped; other servers still connect.
- Inside a session, edits made outside need `/reload`.

Source: https://docs.pi.dev/latest/mcp

## Entry fields

- stdio: `command` (a single executable, NOT a shell string), `args`, `env`,
  `cwd` (relative resolves against the session dir; leading `~/` expands in
  `command`, args and `cwd`).
- HTTP: `url`, `headers`, `oauth`.
- `type` optional; if set must be `stdio`, `http` or `streamable-http`.
  `sse` is an error; try the server's `/mcp` endpoint instead of `/sse`.
- Shared: `timeout` (seconds, default 60, reset by progress notifications),
  `enabled: false`, `exposure`, `toolExposure`, `description` (one sentence;
  feeds the system prompt listing, tool search ranking and
  `describeNamespace()`; falls back to first line of server instructions).
- `env`/`headers` values expand `${VAR}`. A value beginning with `!` runs a
  command, and the command must be the entire value:
  `"Authorization": "!echo Bearer $(gh auth token)"`.
- Server names: letters, digits, `_`, `-` only. Names differing only by `-`
  vs `_` are treated as the same server (second is rejected).

Migrating: VS Code uses top-level `servers` and `${input:...}` (swap for
`${NAME}`); Codex `[mcp_servers.<name>]` TOML must be converted to JSON;
OpenCode `local`/`remote` types map to stdio/url entries
(`environment` -> `env`, `{env:NAME}` -> `${NAME}`).

Source: https://docs.pi.dev/latest/mcp

## Tool naming and exposure modes

Every server tool is registered as `mcp__<server>__<tool>` (same scheme as
Claude Code). Non `[A-Za-z0-9_]` chars become `_`; collisions get a hash
suffix.

Key difference from Claude Code / Codex: by default MCP tools are NOT
declared to the model as normal tools. `exposure` per server:

| Mode | Model sees | Use for |
|------|-----------|---------|
| `codemode` (default; alias `codemode-deferred`) | Nothing declared; reachable only from `codemode` scripts via `searchTools()`, `describeTool()`, `ALL_TOOLS` | general servers, scripted combining/filtering |
| `deferred` | Declared only after `tool_search` loads a match | big servers you want called directly after discovery |
| `direct` | Declared like a built-in tool (and callable from codemode) | small, hot tool sets |
| `hidden` | Registered but unreachable | blocking tools |

- `toolExposure` overrides per tool. Keys are exact tool names or `*` globs;
  exact beats pattern, first matching pattern wins. Example:

```
"exposure": "deferred",
"toolExposure": { "search_code": "direct", "get_*": "codemode", "delete_*": "hidden" }
```

- A connecting `codemode` server auto-activates the `codemode` tool; a
  `deferred` server activates `tool_search`. Pi warns once if neither is
  active while non-direct tools exist.
- `codemode`/`deferred` servers are listed in an `mcp_servers` section of
  the system prompt. When that section changes Pi appends it to the
  conversation rather than rewriting tool declarations (keeps prompt cache).
- `describeNamespace()` / `searchTools({namespace})` accept
  `mcp__dev-radius`, `mcp__dev_radius`, `dev-radius` or `dev_radius`.
- Settings: `"defaultTools": ["+codemode"]` keeps codemode on with no MCP;
  `"autoEnableCodemode": false` (beside `mcpServers`) stops auto-activation.
- Codemode calls survive `/tree`, resume and fork; tools loaded by
  `tool_search` are recorded in the transcript and stay declared on that
  branch.
- `pi mcp list` and the `/mcp` Tools view show the effective exposure.

Source: https://docs.pi.dev/latest/mcp

## CLI filters

- `--tools` leaves MCP tools alone unless an entry starts with `mcp__`, e.g.
  `pi --tools read,codemode,'mcp__radius__*'`.
- `--exclude-tools` takes the same patterns.
- `--no-mcp` turns built-in MCP off for one run (no servers, no `/mcp`).
- Permanent off: disable `mcp` under Built-in in `pi config`, or
  `"extensions": ["-builtin:mcp"]` in settings.

Source: https://docs.pi.dev/latest/mcp

## Runtime behaviour and results

- All enabled servers connect in the background at session start. The first
  prompt waits up to 10 s only for servers that have `direct` tools. Codemode
  scripts wait for the servers they name; `searchTools()`/`ALL_TOOLS`,
  `tool_search` and resource tools wait for all.
- HTTP network errors and 408/429/5xx are retried twice on connect; a dropped
  connection reconnects on next call. Tool calls themselves are not retried.
- `tools/list_changed` adds new tools; withdrawn ones become unreachable.
- Stopping a stdio server: close stdin, SIGTERM, then SIGKILL to the process
  group (cleans up `npx`/`uvx` wrappers).
- Text results above 20 KB are middle-truncated with a
  `…N chars truncated…` marker; the full text goes to a temp file named in
  the result. Codemode scripts get the full `CallToolResult`
  (`content`, `structuredContent`, `isError`).
- Server instructions are not inlined into tool descriptions; read them with
  `describeNamespace("mcp__<server>")`.
- Logs: `~/.pi/agent/mcp.log` (`<time> [<server>] <level> <logger>: <msg>`),
  rotated to `mcp.log.1` past 5 MB.

Source: https://docs.pi.dev/latest/mcp

## Interactive `/mcp`

`/mcp` lists servers (problem ones first) with state, tool count, exposure
and source; select one to inspect tools, reconnect, sign in/out, change
exposure or enable/disable. Changes are written back into whichever file
defines the server. In a trusted project, "Enable/Disable in this project"
writes a project override. Non-TUI forms: `/mcp login <server>`,
`/mcp logout <server>`, `/mcp reconnect <server>`.

Source: https://docs.pi.dev/latest/mcp

## OAuth

- HTTP servers without an `Authorization` header can use OAuth; no
  credentials needed in `mcp.json`. Sign in via `/mcp`, `/mcp login <name>`
  or `pi mcp login <name>`. Over SSH, paste the redirected URL back in.
- Tokens stored in `~/.pi/agent/mcp-auth.json`, auto-refreshed. Credentials
  are keyed by server name + URL.
- `oauth` keys: `clientId`, `clientSecret`, `callbackPort`
  (`http://127.0.0.1:<port>/callback`), `callbackUrl` (loopback HTTP only),
  `scope`, `clientName` (default registers as `pi`; some servers only allow
  known client names), `clientRegistration` (`auto` | `dcr` | `cimd`),
  `authServerMetadataUrl` (override broken discovery; HTTPS unless loopback).
- Use `dcr` when the auth page says "invalid client". To re-register under a
  new `clientName`, sign out first.

Source: https://docs.pi.dev/latest/mcp

## Resources

When a server offers resources pi adds `list_mcp_resources`,
`list_mcp_resource_templates`, `read_mcp_resource` (same names as Codex /
OpenCode). Their exposure is the widest among resource-bearing servers.
Non-text, non-image binaries are written to temp files. MCP Apps resources
(`ui://`, `text/html;profile=mcp-app`) are skipped. List/read retry once on
transient HTTP errors.

Source: https://docs.pi.dev/latest/mcp

## Permissions, extensions, SDK

- There is no built-in per-call approval prompt. MCP calls go through the
  normal tool pipeline, so extension `tool_call`/`tool_result` handlers
  (permission gates) apply. Codemode-originated calls carry
  `parentToolCallId`.
- `pi.getAllTools()` exposes server hints `readOnlyHint`,
  `destructiveHint`, `idempotentHint`, `openWorldHint` for gating logic.
- Extensions can add session-scoped servers with
  `pi.registerMcpServer(name, config)`; a same-name file entry wins.
- An extension registering `/mcp` (e.g. `pi-mcp-adapter`) replaces built-in
  MCP entirely: `mcp.json` is then ignored in sessions. Same for extensions
  registering `codemode` or `tool_search`.
- SDK sessions load no built-in extensions; add the MCP, codemode and
  tool-search extensions to the resource loader yourself.

Source: https://docs.pi.dev/latest/mcp
