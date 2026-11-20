# Automation: print, JSON, RPC and SDK

Source: pi 1.6.0 docs, scraped 2026-11-20. Re-check with scripts/refresh.py.

## Pick a mode

| Mode | Invocation | Output | Lifetime |
|---|---|---|---|
| Interactive | `pi` | TUI | until user exits |
| Print | `pi -p "prompt"` / `pi --print` | final assistant text on stdout | one shot |
| JSON | `pi --mode json "prompt"` | JSONL event stream on stdout | one shot |
| RPC | `pi --mode rpc` | JSONL commands in, responses + events out | long-lived |
| SDK | `import { createAgentSession }` | in-process TypeScript | host-controlled |

- If stdin or stdout is not a TTY and no mode is chosen, pi **falls into print mode
  automatically**, so `git diff | pi "review this"` works. `--mode text` does not
  force one-shot; use `--print` for that.
- Ordinary CLI flags (`--provider`, `--model`, `--tools`, `-e`, `--no-session`,
  `--session-dir`, `--name`/`-n`, `--session`, `-c`/`--continue`) apply in every mode.
- Compared to Claude Code: `claude -p` maps to `pi -p`;
  `claude -p --output-format stream-json` maps to `pi --mode json`. Claude Code has
  no bidirectional stdin protocol like pi RPC (its Agent SDK is the closest).
  Codex's `codex exec --json` is the nearest match for JSON mode.
- Rebranded forks change name and config dir via `"piConfig": {"name", "configDir"}`
  in `package.json`.

Source: https://docs.pi.dev/latest/cli-integration

## Print mode

- Runs the supplied prompts, writes only the final assistant text, exits. No
  intermediate events are visible.
- Errors go to stderr. Exit status is nonzero when the final response stopped with
  `error` or `aborted`, and also on `length` (partial text still printed, warning on
  stderr). Useful for `$(...)` substitution and pipelines.
- Extensions still run but `ctx.hasUI` is false and `ctx.shutdown()` is a no-op.

Source: https://docs.pi.dev/latest/cli-integration

## JSON mode: invocation and framing

- `pi --mode json "Review this repository" > events.jsonl`. All prompts are given
  at startup; the process streams events and exits. It accepts no later input.
- It is an **event stream**, not a single JSON result and not a structured-output
  constraint on the model.
- A failed or aborted assistant response does **not** make the exit status nonzero;
  inspect events. Nonzero only if the invocation itself throws.
- Strict JSONL: split on LF only (strip a trailing CR). Do not use Node `readline`,
  which also splits on U+2028/U+2029 inside strings. Read stdout continuously or pi
  stalls. stderr carries diagnostics.
- First record is the session header (JSON mode only):
  `{"type":"session","version":3,"id":"uuid","timestamp":"...","cwd":"/path"}`.
- One-liner: `pi --mode json "List files" 2>/dev/null | jq -c 'select(.type == "message_end")'`.

Source: https://docs.pi.dev/latest/json, https://docs.pi.dev/latest/cli-integration

## JSON event catalogue (shared by JSON and RPC)

| Event | Key fields | Meaning |
|---|---|---|
| `agent_start` | - | low-level run started |
| `agent_end` | `messages`, `willRetry` | run ended; retries/queued work may follow |
| `agent_settled` | `aborted` | **the real "done"**: no more automatic work |
| `turn_start` / `turn_end` | `message`, `toolResults` | one assistant response plus its tools |
| `message_start` / `message_end` | `message` | `message_end` is authoritative |
| `message_update` | `usage`, `assistantMessageEvent` | delta-only streaming |
| `tool_execution_start` | `toolCallId`, `toolName`, `args` | |
| `tool_execution_update` | `+ partialResult` | |
| `tool_execution_end` | `result`, `isError`, `durationMs` | |
| `queue_update` | `steering`, `followUp` | full current queues |
| `entry_appended` | `entry` | extension `appendEntry` |
| `session_info_changed` / `thinking_level_changed` | `name` / `level` | |
| `compaction_start` / `compaction_end` | `reason`, `result`, `aborted`, `willRetry`, `errorMessage` | reason: manual/threshold/overflow |
| `auto_retry_start` / `auto_retry_end` | `attempt`, `maxAttempts`, `delayMs`, `success`, `finalError` | transient provider errors |
| `summarization_retry_*` | `source`, `reason` | compaction/branch-summary retry |
| `bash_execution_update` (RPC) | `id`, `delta` | streamed RPC `bash` output |
| `extension_error` (RPC) | `extensionPath`, `event`, `error` | handler threw |

`assistantMessageEvent.type` values: `text_start`/`text_delta`/`text_end`,
`thinking_start`/`thinking_delta`/`thinking_end`, `toolcall_start` (`id`,
`toolName`)/`toolcall_delta`/`toolcall_end` (`toolCall`), all keyed by
`contentIndex`. Buffer deltas for live display, replace with `*_end` content, then
replace the whole message with `message_end.message`. TypeScript type:
`JsonAgentSessionEvent` from `@earendil-works/pi-coding-agent`.

Source: https://docs.pi.dev/latest/json

## RPC protocol

- Start: `pi --mode rpc --no-session`. `@file` prompt args are rejected; send
  prompts via the `prompt` command.
- Records: stdin commands; stdout `{"type":"response","command",...,"success","data"|"error"}`,
  session events (no id), and extension UI records (both directions).
- Correlate by optional string `id`, echoed in the response; responses may be out
  of order. Malformed JSON returns `{"type":"response","command":"parse","success":false}`.
- `prompt` success means accepted/queued/handled, **not finished**. Check
  `data.disposition`: `started` (wait for `agent_settled`), `queued`, or `handled`
  (no run started; do not wait). Subscribe before prompting.
- While streaming, a `prompt` must carry `"streamingBehavior": "steer"|"followUp"`
  or it errors. Extension `/commands` execute immediately even mid-stream.
- Shutdown: close stdin (orderly, waits for pending work); SIGTERM/SIGHUP also
  orderly; a second signal exits at once.
- TypeScript: prefer `RpcClient` (typed methods, `promptAndWait()`); types in
  `rpc-types.ts`.

```json
{"id":"req-1","type":"prompt","message":"Review this repository"}
{"id":"req-1","type":"response","command":"prompt","success":true,"data":{"disposition":"started"}}
```

Source: https://docs.pi.dev/latest/rpc

## RPC command catalogue

| Command | Params | Response `data` |
|---|---|---|
| `prompt` | `message`, `images?`, `streamingBehavior?` | `{disposition}` |
| `steer` | `message`, `images?` | `{disposition: queued\|handled}` |
| `follow_up` | `message`, `images?` | `{disposition}` |
| `abort` | - | waits until idle |
| `clear_queue` | - | `{steering[], followUp[]}` |
| `new_session` | `parentSession?` | `{cancelled}` |
| `get_state` | - | model, thinkingLevel, isStreaming, isCompacting, steering/followUp mode, sessionFile, sessionId, sessionName, autoCompactionEnabled, messageCount, pendingMessageCount |
| `get_messages` | - | `{messages}` |
| `set_model` | `provider`, `modelId` | Model object |
| `cycle_model` | - | `{model, thinkingLevel, isScoped}` or null |
| `get_available_models` | - | `{models}` |
| `set_thinking_level` | `level` (off/minimal/low/medium/high/xhigh/max) | - |
| `cycle_thinking_level` / `get_available_thinking_levels` | - | `{level}` / `{levels}` |
| `set_steering_mode` / `set_follow_up_mode` | `mode`: `all`\|`one-at-a-time` | - |
| `compact` | `customInstructions?` | summary, firstKeptEntryId, tokensBefore, estimatedTokensAfter, usage |
| `set_auto_compaction` / `set_auto_retry` | `enabled` | - |
| `abort_retry` | - | - |
| `bash` | `command`, `excludeFromContext?` | `{output, exitCode, cancelled, truncated, fullOutputPath?}` |
| `abort_bash` | - | - |
| `get_session_stats` | - | message/tool counts, `tokens`, `cost`, `contextUsage` |
| `export_html` | `outputPath?` | `{path}` |
| `switch_session` | `sessionPath` | `{cancelled}` |
| `fork` | `entryId` | `{text, cancelled}` |
| `clone` | - | `{cancelled}` |
| `get_fork_messages` | - | `{messages: [{entryId, text}]}` |
| `get_entries` | `since?` (entry id cursor) | `{entries, leafId}` |
| `get_tree` | - | `{tree, leafId}` |
| `get_last_assistant_text` | - | `{text}` (null if none) |
| `set_session_name` | `name` | - |
| `get_commands` | - | `{commands: [{name, description, source, sourceInfo}]}` |

- RPC `bash` output reaches the model only on the **next prompt**, as a user
  message, unless `excludeFromContext` is true.
- Esc-like behaviour: `clear_queue` then `abort`, and restore the returned text.
- Built-in TUI commands (`/settings`, `/model`...) are not available via `prompt`.

Source: https://docs.pi.dev/latest/rpc-commands

## RPC extension UI subprotocol

- pi emits `{"type":"extension_ui_request","id","method",...}` on stdout.
- Dialogs (`select` with `options`, `confirm` with `message`, `input` with
  `placeholder`, `editor` with `prefill`) block until the client answers on stdin:
  `{"type":"extension_ui_response","id":"<same>","value":"..."}`, or
  `"confirmed": true|false`, or `"cancelled": true`. A `timeout` field means pi
  auto-resolves; the client need not track it.
- Fire-and-forget (no reply): `notify` (`message`, `notifyType`), `setStatus`
  (`statusKey`, `statusText`), `setWidget` (`widgetKey`, `widgetLines`,
  `widgetPlacement`), `setTitle`, `set_editor_text`.
- Degraded in RPC: `custom()` returns `undefined`; footer/header/editor
  component/working-indicator/autocomplete setters are no-ops; themes unavailable.
  `ctx.mode` is `"rpc"` and `ctx.hasUI` is true.
- A headless JSON or print run has **no** way to answer dialogs (`hasUI` false), so
  a gate extension that prompts must default safely there.

Source: https://docs.pi.dev/latest/rpc-extension-ui

## SDK

- Package `@earendil-works/pi-coding-agent`, for Node.js or Bun in-process use.

```ts
import { createAgentSession, SessionManager } from "@earendil-works/pi-coding-agent";
const { session } = await createAgentSession({ sessionManager: SessionManager.inMemory() });
const off = session.subscribe((e) => {
  if (e.type === "message_update" && e.assistantMessageEvent.type === "text_delta")
    process.stdout.write(e.assistantMessageEvent.delta);
});
try { await session.prompt("Explain this repo"); } finally { off(); session.dispose(); }
```

- `session.prompt()` resolves when the run (including retries) finishes; while
  streaming you must pick `session.steer()` or `session.followUp()`. Also
  `abort()`, `waitForIdle()`, `getLastAssistantText()`, `messages`, `model`,
  `thinkingLevel`, `systemPrompt`, `getActiveToolNames()`.
- Sessions persist by default; use `SessionManager.inMemory()` to avoid files.
  Restore history via the manager, not by assigning `agent.state.messages`.
- `createAgentSession` options: `cwd`, `modelRuntime`, `model`, `thinkingLevel`,
  `scopedModels`, `settingsManager`, `sessionManager`, `resourceLoader`
  (`DefaultResourceLoader` or custom), `tools`, `noTools`, `excludeTools`,
  `customTools`.
- `AgentSessionRuntime` adds `newSession()`, `switchSession()`, `fork()`,
  `importFromJsonl()`; re-subscribe after each replacement.
- Unlike the CLI, SDK sessions do **not** load codemode, tool_search or MCP: add
  `createCodemodeExtension()`, `createToolSearchExtension()`, `createMcpExtension()`
  to the `extensionFactories` of `DefaultResourceLoader`, enable via
  `defaultTools`, and call `session.bindExtensions()` so MCP connects.
- For a subprocess boundary or another language, use RPC instead.

Source: https://docs.pi.dev/latest/sdk
