# Sessions, branching and compaction

Source: pi 1.6.0 docs, scraped 2026-11-20. Re-check with scripts/refresh.py.

## Resume and open sessions

Sessions persist automatically unless `--no-session` (in-memory, cannot be
resumed).

| Command | Effect | Working dir used |
|---------|--------|------------------|
| `pi -c` / `--continue` | most recent session for cwd | current dir |
| `pi -r` / `--resume`, `/resume` | picker (Tab includes other dirs) | session's own cwd |
| `--session <path\|id>` | open by file or full/partial ID | session's own cwd (offers fork if ID only exists for another dir) |
| `--session-id <id>` | open-or-create that ID for cwd | current dir |
| `--fork <path\|id>` | copy into a new session | current dir |
| `/new` | fresh session | - |
| `/import` | open a session file | session's own cwd |

- `--fork` can't combine with `--session`, `--continue`, `--resume`,
  `--no-session`. `--session-id` can't combine with `--session`,
  `--continue`, `--resume`, but pairs with `--fork` to choose the new ID.
- A session's cwd is fixed at creation; tools, `@path`, project config and
  context files resolve from it. If that dir vanished, interactive mode asks
  Continue (here, not remembered) / Fork / Cancel; other modes exit with an
  error.
- `/name` or `-n/--name` sets a display name; `/session` shows file path, ID,
  message count, tokens and cost. In the `/resume` picker, Ctrl+D deletes
  (uses `trash` CLI when present).

Difference from Claude Code: `-c`/`-r` exist with similar meaning, but
`--resume` jumps to the session's original cwd rather than the current one.

Source: https://docs.pi.dev/latest/sessions, https://docs.pi.dev/latest/cli, https://docs.pi.dev/latest/session-format

## Branching

Sessions are a tree in one file; leaving a branch never deletes it.

- `/tree`: move within the same file. Picking a user message puts its text
  back in the editor to edit and resubmit (new branch); picking another
  entry continues after it with an empty editor. pi offers to summarize the
  branch you leave (a `branch_summary` entry on the new branch).
- `/fork`: new session file starting from an earlier user message.
- `/clone`: new session file copying the whole active branch.
- Only the active branch (leaf back to root) is sent to the model.

Source: https://docs.pi.dev/latest/sessions, https://docs.pi.dev/latest/compaction

## Storage location

Default: `~/.pi/agent/sessions/--<path>--/<timestamp>_<session-id>.jsonl`
where `<path>` is the cwd with the leading separator dropped and `/`, `\`,
`:` replaced by `-`. Session ID is a UUID unless set via `--session-id`/SDK.

Override precedence: `--session-dir` > `PI_CODING_AGENT_SESSION_DIR` >
`sessionDir` setting. Gotcha: a project `sessionDir` is read before project
trust is resolved.

Delete sessions by removing their `.jsonl` files.

Export/share: `/export` (HTML or JSONL), `/share` (Radius artifact if Radius
auth is configured, else private GitHub gist; force with
`sessionShareDestination: "gist" | "radius"`). `/bug [description]` builds a
private report. Review before sharing: transcripts can hold file contents and
secrets.

Source: https://docs.pi.dev/latest/sessions, https://docs.pi.dev/latest/session-format

## Compaction: when it fires

Auto-compaction triggers when
`contextTokens > contextWindow - reserveTokens` (default reserve 16384).

- Checked before each new user prompt, between turns after tool results are
  appended (in `prepareNextTurn`), and as overflow recovery: a provider
  context-overflow error or early `stopReason: "length"` gets one
  compact-and-retry.
- Manual: `/compact [instructions]` (instructions focus the summary). Works
  even when auto is disabled.
- The footer shows context usage. If summarization fails (provider down),
  fix the provider and rerun `/compact`.

Settings (`~/.pi/agent/settings.json` or `.pi/settings.json`):

```
"compaction": {
  "enabled": true,
  "reserveTokens": 16384,
  "keepRecentTokens": 20000,
  "modelOverrides": { "provider/model-id": { "reserveTokens": 400000 } }
}
```

`modelOverrides` keys are exact case-sensitive `provider/modelId`; each value
falls back independently to the ordinary setting then the default. `enabled`
is global only. Settings files merge recursively, so a user-level model
override beats a project-wide plain value.

Note: the threshold is an absolute token reserve, tunable per model, and
compaction only appends; original entries stay in the file.

Source: https://docs.pi.dev/latest/compaction, https://docs.pi.dev/latest/sessions

## Compaction: what it does

1. Walk back from the end until `keepRecentTokens` (default 20k) is
   accumulated; that sets the cut point.
2. Summarize everything from the previous kept boundary up to the cut
   (previous summary passed in for iterative updates).
3. Append a `compaction` entry with `summary` and `firstKeptEntryId`.
4. Next request = system prompt + summary + messages from
   `firstKeptEntryId` onward.

- Cuts land on user, assistant, bashExecution or custom messages, never on
  a tool result (stays with its call). If one user turn exceeds the budget
  the cut splits it and two summaries (history + turn prefix) are merged.
- Summary sections: Goal, Constraints & Preferences, Progress (Done / In
  Progress / Blocked), Key Decisions, Next Steps, Critical Context, plus
  `<read-files>` / `<modified-files>` lists tracked cumulatively.
- Input is serialized as `[User]:`, `[Assistant]:`,
  `[Assistant tool calls]:`, `[Tool result]:`; thinking omitted; tool
  results truncated to 2000 chars.
- Branch summaries use the same format minus Critical Context.
- Extension hooks: `session_before_compact` (cancel or supply summary;
  `reason` is `manual`, `threshold` or `overflow`), `session_compact_failed`,
  `session_before_tree`.

Source: https://docs.pi.dev/latest/compaction

## Session file format (for parsing)

JSONL, one object per line, each with `type`. Current version 3 (v1/v2
auto-migrate on load). Line 1 is the header (not in the tree):

```
{"type":"session","version":3,"id":"<uuid>","timestamp":"<iso>","cwd":"/path","parentSession":"/orig.jsonl"}
```

`parentSession` appears only for `/fork`, `/clone` results. Every other entry
has `type`, `id` (usually 8 hex chars), `parentId` (null for a root),
`timestamp` (ISO 8601).

| `type` | Key fields | In model context? |
|--------|-----------|-------------------|
| `message` | `message` (an AgentMessage) | yes |
| `model_change` | `provider`, `modelId` | no (sets model) |
| `thinking_level_change` | `thinkingLevel` | no |
| `usage` | `kind`, `provider`, `model`, `usage` | no (counts in totals) |
| `compaction` | `summary`, `firstKeptEntryId`, `tokensBefore`, `systemMessage?`, `details?` | yes, replaces older |
| `context_edit` | `targetId`, `replacement` (`null` = omit target) | edits target |
| `branch_summary` | `summary`, `fromId` | yes |
| `custom` | `customType`, `data` | no (extension state) |
| `custom_message` | `customType`, `content`, `display` | yes |
| `label` | `targetId`, `label` | no |
| `session_info` | `name` | no |

Rebuilding context: walk `parentId` from the leaf (usually the last entry)
to the root. If a `compaction` is on the path, use the latest: its summary,
then non-system entries from `firstKeptEntryId` up to it, then everything
after. Apply the latest `context_edit` per target. A retain-none compaction
sets `firstKeptEntryId` to its own id.

Source: https://docs.pi.dev/latest/session-format

## Message shapes inside `message` entries

`message.timestamp` is Unix ms (entry `timestamp` is ISO). Roles:

- `system`: `content`, `sections` (named prompt parts; `null` removes),
  `toolsAdded`, `toolsRemoved`. The first request stores the full prompt and
  tools; later ones are patches. Replay in order for the current prompt.
- `user`: `content` string or `[text|image]` blocks.
- `assistant`: `content` of `{type:"text",text}`,
  `{type:"thinking",thinking}`, `{type:"toolCall",id,name,arguments}`;
  plus `provider`, `model`, `usage`, `stopReason`
  (`stop|length|toolUse|error|aborted|deferred`; `pending` never persisted),
  optional `errorMessage`, `thinkingLevel`.
- `toolResult`: `toolCallId`, `toolName`, `content`, `isError`, optional
  `details`, `usage`, `nestedCalls`.
- `bashExecution` (user `!` commands, not a tool result): `command`,
  `output`, `exitCode`, `cancelled`, `truncated`; sent to the model as user
  text unless `excludeFromContext`.
- `custom`, `branchSummary`, `compactionSummary`: synthesized from entries.

`usage`: `input`, `output`, `cacheRead`, `cacheWrite`, `totalTokens`,
`cost.{input,output,cacheRead,cacheWrite,total}`; `reasoning` is already
inside `output` (don't add). Tolerate unknown roles and unknown `usage.kind`.

Source: https://docs.pi.dev/latest/message-types, https://docs.pi.dev/latest/session-format
