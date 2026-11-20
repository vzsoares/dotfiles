# Extensions and codemode

Source: pi 1.6.0 docs, scraped 2026-11-20. Re-check with scripts/refresh.py.

## What an extension is (vs Claude Code / Codex)

- A pi extension is a **TypeScript module running in-process** with pi's full OS
  permissions. One mechanism covers what Claude Code splits into hooks,
  slash commands, custom tools, status line and MCP config.
- Claude Code hooks are shell commands wired in `settings.json` that get JSON on
  stdin and answer via exit code / stdout. pi has **no shell-hook config**: you
  subscribe with `pi.on("tool_call", ...)` and return `{ block: true, reason }`.
  Codex likewise has no equivalent event API; its approvals are policy flags.
- Tools are registered in code (`pi.registerTool`), not via MCP only. Slash
  commands are code (`pi.registerCommand`), not markdown files (pi's markdown
  equivalents are prompt templates and skills, invoked as `/name` and
  `/skill:name`).
- Loaded through jiti: `.ts` works without a build step. Trust matters: a
  project's `.pi/extensions` only loads once the project is trusted.

Source: https://docs.pi.dev/latest/extensions

## Where extensions live and load from

| Location | Scope |
|---|---|
| `~/.pi/agent/extensions/*.ts` / `*.js` | global |
| `~/.pi/agent/extensions/<dir>/index.ts` | global, multi-file |
| `~/.pi/agent/extensions/<dir>/package.json` with `"pi": {"extensions": [...]}` | global, package with deps |
| `.pi/extensions/...` (same three layouts) | project, only when trusted |

- Discovery is one directory level deep; larger packages list entry points in
  the `pi.extensions` field of `package.json`.
- Extra sources in `settings.json`: `"packages": ["npm:@scope/pkg@1.0.0",
  "git:github.com/user/repo@v1"]` and `"extensions": ["/abs/file.ts", "/abs/dir"]`.
  Built-ins are referenced as `builtin:<name>` (e.g. `builtin:mcp`); disable one
  with `-builtin:<name>` in `extensions`.
- CLI: `pi -e ./ext.ts` (`--extension`) for a one-off test; `pi -ne`
  (`--no-extensions`) disables discovered, configured and built-in extensions but
  still loads explicit `-e` paths. Auto-discovered ones hot-reload with `/reload`.
- npm deps: put a `package.json` next to the extension and `npm install`. For
  packages shared via `pi install`, runtime deps go in `dependencies`; do not list
  host-provided packages.
- Host-provided imports (pi maps these to its own copies):
  `@earendil-works/pi-coding-agent` (types, helpers), `@earendil-works/pi-agent-core`,
  `@earendil-works/pi-ai` (`StringEnum`, model types), `@earendil-works/pi-tui`,
  `typebox`. Legacy `@mariozechner/*` and `@sinclair/typebox` aliases still map.
- Use `CONFIG_DIR_NAME` instead of hardcoding `.pi` (forks can rebrand it).

Source: https://docs.pi.dev/latest/extensions

## Minimal shape

```ts
import { type ExtensionAPI, isToolCallEventType } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
export default function (pi: ExtensionAPI) {
  pi.on("tool_call", async (event, ctx) => {
    if (isToolCallEventType("bash", event) && event.input.command.includes("rm -rf"))
      return { block: true, reason: "blocked" };
  });
  pi.registerCommand("hello", { description: "Say hi", handler: async (args, ctx) => ctx.ui.notify(`hi ${args}`, "info") });
}
```

- The default export is a factory; it may be `async` (pi awaits it before
  `session_start`, useful for discovering models then calling `registerProvider`).
- Do not start processes, sockets, watchers or timers in the factory (some
  invocations never start a session). Start them in `session_start` and clean up in
  an idempotent `session_shutdown` handler.

Source: https://docs.pi.dev/latest/extensions

## Event catalogue

Handlers run in extension load order, then registration order. Return semantics:
notify (ignored), in-place mutation, chained transform, gate (block/cancel/handle).
Gates should fail closed.

| Event | When | Can return / do |
|---|---|---|
| `project_trust` | before trusting a project's `.pi` / `.agents/skills` (global + `-e` exts only) | `{ trusted: "yes"\|"no"\|"undecided", remember? }` |
| `session_start` | startup/reload/new/resume/fork (`event.reason`) | notify; rebuild state here |
| `resources_discover` | after `session_start` | `{ skillPaths, promptPaths, themePaths }` |
| `session_before_switch` / `session_before_fork` | `/new`, `/resume` / `/fork`, `/clone` | `{ cancel: true }` |
| `session_before_compact` | manual/threshold/overflow compaction | `{ cancel }` or `{ compaction: { summary, firstKeptEntryId, tokensBefore } }` |
| `session_compact` / `session_compact_failed` | after compaction | notify |
| `session_before_tree` / `session_tree` | `/tree` navigation | cancel or custom summary |
| `session_info_changed` | session renamed | notify |
| `session_shutdown` | quit/reload/new/resume/fork | cleanup |
| `input` | user text, after extension-command check, before skill/template expansion | `{ action: "continue"\|"transform"\|"handled", text? }` |
| `before_agent_start` | before a run | `{ message?, systemPrompt? }`; mutate `event.systemPromptOptions` |
| `agent_start` / `agent_end` | per low-level run (not final!) | notify |
| `agent_before_settle` | last actionable point | `{ entries, continue: true }` |
| `agent_settled` | truly idle (use for "done" notifications) | notify |
| `turn_start` / `turn_end` | per model response + its tools | `turn_end` can add entries / `continue` |
| `context` | before each LLM call, messages without system | `{ messages }` |
| `context_with_system` | after `context`, full transcript incl. system | `{ messages }` (keep system msg at index 0) |
| `before_provider_headers` | headers built | mutate `event.headers` (`null` deletes) |
| `before_provider_request` | payload built | return replacement payload |
| `after_provider_response` / `provider_stream_event` | HTTP status/headers; raw stream | notify |
| `message_start` / `message_update` / `message_end` | every message; streaming deltas | `message_end` may return `{ message }` |
| `tool_execution_start` / `_update` / `_end` | tool lifecycle; `_end` fires for every finalized call | notify |
| `tool_call` | before execute | mutate `event.input`; `{ block, reason, terminate? }` |
| `tool_result` | after execute | partial patch `{ content, details, isError, ... }` (chained) |
| `user_bash` | user `!` / `!!` | `{ operations }` or `{ result }` |
| `model_select` / `thinking_level_select` | model or level change | notify |
| `ui_prompt_start` / `ui_prompt_end` | dialog open/close | notify |
| `cache_warming_decision` | idle prompt-cache refresh | `{ action: "warm"\|"stop" }` (last wins) |
| `mcp_servers_change` | MCP servers registered/removed late | notify |

- Claude Code mapping: `PreToolUse` ~ `tool_call`; `PostToolUse` ~ `tool_result`;
  `UserPromptSubmit` ~ `input` / `before_agent_start`; `Stop` ~ `agent_settled`
  (or `agent_before_settle` to force one more turn); `SessionStart` /
  `SessionEnd` ~ `session_start` / `session_shutdown`; `PreCompact` ~
  `session_before_compact`.
- Unknown tool or invalid args skip `tool_call`; a blocked call skips `tool_result`.
  Both still emit `tool_execution_end`. No re-validation after you mutate input.
- Unconditional `continue: true` can loop forever; guard it.
- Errors: a throwing handler is logged and pi continues, except a throwing
  `tool_call` blocks the tool and a throwing `user_bash` blocks the command.

Source: https://docs.pi.dev/latest/extensions

## ExtensionAPI (`pi.*`) catalogue

| Method | Purpose |
|---|---|
| `on(event, handler)` | subscribe; returns an unsubscribe fn |
| `registerTool(def)` | LLM-callable tool; works after startup too |
| `registerCommand(name, { description, handler, getArgumentCompletions? })` | `/name`; duplicates become `/name:1`, `/name:2` |
| `registerShortcut(key, { handler })` / `registerFlag(name, {type, default})` + `getFlag` | keybinding / CLI flag |
| `sendMessage({customType, content, display, details}, {deliverAs, triggerTurn})` | custom message into LLM context; `deliverAs`: `steer`/`followUp`/`nextTurn` |
| `sendUserMessage(content, {deliverAs, expandPromptTemplates})` | as if typed; must set `deliverAs` while streaming |
| `appendEntry(customType, data)` | persisted, NOT sent to the LLM |
| `setSessionName` / `getSessionName` / `setLabel(entryId, label)` | session metadata, `/tree` bookmarks |
| `getCommands()` | extension, prompt and skill commands with `sourceInfo` |
| `exec(cmd, args, {signal, timeout, cwd, maxBuffer})` | run without a shell; `{stdout, stderr, code, killed, truncated}` |
| `getActiveTools` / `getAllTools` / `setActiveTools(names)` | tool loadout |
| `setModel(model)` / `getThinkingLevel` / `setThinkingLevel` | per-session, not defaults |
| `events.on/emit` | inter-extension bus |
| `registerProvider` / `unregisterProvider` | model providers (proxy, OAuth, `refreshModels`) |
| `registerVirtualModel` / `unregisterVirtualModel` | router model picking a physical model per request |
| `registerMcpServer(name, cfg)` / `unregisterMcpServer` / `getMcpServers` | session-only MCP servers (not persisted) |
| `registerMessageRenderer` / `registerEntryRenderer` / `registerToolRenderer` / `registerMarkdownTransformer` | TUI rendering |
| `getSettings()` | merged effective settings copy |

Source: https://docs.pi.dev/latest/extensions

## Context objects

- `ExtensionContext` (all handlers): `ctx.ui`, `ctx.mode` (`"tui"|"rpc"|"json"|"print"`),
  `ctx.hasUI` (true in TUI and RPC), `ctx.cwd`, `ctx.isProjectTrusted()`,
  `ctx.sessionManager` (read-only: `getEntries`, `getBranch`, `getSessionFile`,
  `getLeafId`, `buildContextEntries`...), `ctx.model`, `ctx.modelRegistry`
  (`streamSimple`, `complete`, `classify`, `generateImages`), `ctx.thinkingLevel`,
  `ctx.scopedModels`, `ctx.signal` (pass to `fetch`), `ctx.isIdle()`, `ctx.abort()`,
  `ctx.hasPendingMessages()`, `ctx.shutdown()`, `ctx.getContextUsage()`,
  `ctx.compact()`, `ctx.getSystemPrompt()`.
- `ExtensionCommandContext` (commands only, would deadlock in handlers):
  `waitForIdle()`, `newSession({setup, withSession})`, `fork(entryId, {position})`,
  `navigateTree`, `switchSession(path)`, `reload()`, `getSystemPromptOptions()`.
  After a session replacement only the `ctx` passed to `withSession` is valid; old
  `pi`/`ctx` objects throw. Treat `await ctx.reload(); return;` as terminal.
- Tool `execute()` gets `ExtensionToolContext`: adds `ctx.tools` and
  `ctx.executeTool(name, args)` (never rejects; failures come back `isError`).
- A tool cannot reload; set a flag and send `/cmd` from `agent_settled` with
  `sendUserMessage(..., { expandPromptTemplates: true })`.

Source: https://docs.pi.dev/latest/extensions

## Custom tools

- Definition: `name`, `label`, `description`, `parameters` (TypeBox / JSON Schema),
  `execute(toolCallId, params, signal, onUpdate, ctx)` returning
  `{ content: [{type:"text", text}], details }`. `details` is required (may be
  `undefined`) on every result and partial update.
- Optional: `promptSnippet` (one line in `<tools>`), `promptGuidelines` (bullets in
  `<rules>`; name the tool explicitly, never "this tool"), `prepareArguments`
  (migrate old arg shapes), `outputSchema` + `structuredContent`,
  `executionMode: "sequential"`, `defaultActive: false`, `exposure`, `namespace`,
  `annotations` (MCP-style `readOnlyHint`, `destructiveHint`...), `reachesTools`,
  `prepareLoadout`, `renderCall`/`renderResult`/`renderShell`, `constrainedSampling`.
- Errors: throw, or return `isError: true`. `terminate: true` skips the follow-up
  LLM call when every result in the batch terminates.
- Validation coerces loose args (numeric strings, `"true"`, single value to array);
  `Type.Refine`, codecs and custom `Format.Set` formats are not applied.
- Overriding built-ins (`read`, `bash`, `powershell`, `edit`, `write`, `grep`,
  `find`, `ls`): register the same name; rendering is inherited per slot but prompt
  snippets/guidelines are not. `--no-builtin-tools` starts with only extension tools.
- Remote execution: `createReadTool(cwd, { operations })` and `*Operations`
  interfaces; `createBashTool(cwd, { spawnHook })` rewrites command/cwd/env. Bash
  exposes `PI_SESSION_ID`, `PI_SESSION_FILE`, `PI_PROVIDER`, `PI_MODEL`,
  `PI_REASONING_LEVEL` unless `exposeSessionEnvironment: false`.
- File-mutating tools must wrap the read-modify-write in
  `withFileMutationQueue(absPath, fn)` because tool calls run in parallel.
- Truncate output (built-in limit 50KB / 2000 lines): `truncateHead`,
  `truncateTail`, `DEFAULT_MAX_BYTES`, `DEFAULT_MAX_LINES`; tell the model where
  the full output is.
- State: store it in result `details` and rebuild from `ctx.sessionManager.getBranch()`
  on `session_start` and `session_tree`, so branching stays correct.
- Exposure levels: `direct` (default), `model-only`, `codemode`, `deferred`
  (found by built-in `tool_search`), `hidden` (the only way to withdraw a tool).

Source: https://docs.pi.dev/latest/extensions

## UI hooks (`ctx.ui`)

- Dialogs: `select`, `confirm`, `input`, `editor` (accept `{ timeout }` or
  `{ signal }`); `notify(msg, "info"|"warning"|"error")`.
- Chrome: `setStatus(key, text)`, `setWidget(key, lines|factory, {placement})`,
  `setFooter`, `setHeader`, `setTitle`, `setWorkingMessage`, `setWorkingIndicator`,
  `setWorkingVisible`, `setHiddenThinkingLabel`, `setEditorText`, `getEditorText`,
  `pasteToEditor`, `addAutocompleteProvider`, `setEditorComponent` (extend
  `CustomEditor`), `setToolsExpanded`, `setTheme`, `onTerminalInput`.
- `ctx.ui.custom(factory, { overlay?, overlayOptions? })` swaps in a full TUI
  component until `done(value)`.
- Mode behaviour: print (`-p`) and JSON mode have `hasUI=false` (UI is a no-op);
  RPC forwards dialogs/notify/status/widget/title over JSON (see automation.md);
  guard `custom()` and component factories with `ctx.mode === "tui"`.

Source: https://docs.pi.dev/latest/extensions, https://docs.pi.dev/latest/rpc-extension-ui

## MCP from extensions

- pi has built-in MCP support (`builtin:mcp`) reading `mcp.json`; extensions can add
  servers per session with `pi.registerMcpServer(name, cfg)` using the same shape
  (`command`/`args`/`env`/`cwd` or `url`/`headers`/`oauth`, plus `exposure`,
  `toolExposure`, `timeout`). An `mcp.json` entry with the same name wins.
- MCP tool names look like `mcp__<server>__<tool>`; default MCP exposure is
  `codemode`, i.e. reached through scripts, not declared directly.

Source: https://docs.pi.dev/latest/extensions, https://docs.pi.dev/latest/codemode

## Codemode in brief

- `codemode` is a built-in, **inactive by default** tool: the model writes raw
  JavaScript (no fences, no JSON) that runs as an async function body in a QuickJS
  sandbox and calls other tools; only the script's output reaches the model.
  Enable with `"defaultTools": ["+codemode"]` in settings or `pi --tools +codemode`.
  No direct Claude Code / Codex equivalent.
- Sandbox: no Node APIs, filesystem, network or timers; 256 MB memory; at most two
  workers per process (`PI_CODEMODE_MAX_WORKERS`); scripts cannot nest codemode.
- Optional first line: `// @options: {"max_output_tokens": 2000, "timeout_ms": 60000}`
  (output default 10000 tokens; no default timeout).
- Globals: `tools.<name>(args)` (non-identifier chars become `_`), `text()`,
  `image()`, `console.log`, `return`, `exit()`, `store(key, v)` / `load(key)`
  (small JSON persisted per branch on success), `ALL_TOOLS`, `searchTools(q)`,
  `describeTool(name)`, `describeNamespace(name)`, `models`.
- Call results: tools with an output schema return structured values (`bash` gives
  `{ output, truncated, full_output_path?, exit_code, wall_time_seconds }` with up
  to 1 MiB of output); MCP tools return `CallToolResult`; `read` returns text or an
  image block; others return text. Failures reject; use `Promise.allSettled`.
- `models.classify()` and `models.generateImages()` run non-chat models with the
  session credentials (max four concurrent per script); check `stopReason`.
- Settings: `codemode.mode` `on` (tools stay declared) or `only` (tools hidden and
  listed in codemode); `codemode.inlineBudget` (default ~3000 tokens of listed
  declarations). Calls are real; nothing is rolled back if the script fails.

Source: https://docs.pi.dev/latest/codemode, https://docs.pi.dev/latest/extensions
