---
title: Agent Tool Telemetry — zen-telemetry
category: architecture
updated: 2026-11-10
related: [decision-inbox, overview]
---

# Agent Tool Telemetry — zen-telemetry

`zen-telemetry` logs every tool call an agent-manager agent session makes into
`<vault>/config/zen-flow.db`, imports the past month once from transcripts, and gives
chief a weekly time-sink report. The goal is evidence for decisions like replacing one
tool with another (Notion MCP vs `zen-board`, gum vs Bubble Tea).

## Why a Go binary

The hook runs on every tool call. Measured on the Mac (40 runs, one row into a WAL db):
Go + modernc sqlite 5.9 ms median, python3 stdlib 23.8 ms, bash + jq + sqlite3 25.5 ms,
`uv run --script` 68 ms. The hook is also registered `async: true`, so the tool call
never waits on it; the cost is CPU, not latency. Most of the Go time is process start,
so it scales with machine load (it measured ~16 ms under a load average of 25-58).

Its own module at `scripts/telemetry/` (pure Go, no cgo, so it builds the same on macOS
and Manjaro). `./link-bin` runs `go build` into `scripts/telemetry/bin/` (gitignored).

## Data

| Table | Holds |
|---|---|
| `tool_calls` | One row per call, unique on `(session_id, tool_use_id)` |
| `tool_sessions` | name, group, role, model, repo per session (import and report fill it) |
| `telemetry_filed` | tasks chief filed per `(week, sink_key)` |
| `telemetry_weeks` | ISO weeks whose review is done |
| `telemetry_meta` | telemetry's own schema version |

The db is shared with zen-inbox, which owns `PRAGMA user_version` and creates its
tables with a plain `CREATE TABLE` while it is below its version. Telemetry never reads
or sets `user_version`, uses `CREATE TABLE IF NOT EXISTS`, and prefixes every table
`tool_` or `telemetry_`, so neither tool's migrations can collide.

Per call: session ids (Claude and agent-manager), subagent id/type, prompt id, host,
cwd, effort, permission mode, UTC completion time, `duration_ms`, tool, MCP server,
the main and all executables of a Bash command, a redacted input summary, ok, exit
code (`Exit code N`), redacted error (300 chars), interrupted, timed out
(`/timed out/i`), background, input and output size, source (`hook` or `import`).

- **Never stored:** `tool_response` (only its byte size), MCP input values (key names
  only, since they carry private page content), any secret: AWS keys, GitHub, Anthropic,
  OpenAI and Slack tokens, Bearer and Authorization headers, JWTs, URL credentials,
  `*TOKEN*=`/`*SECRET*=`/`*PASSWORD*=`/`*KEY*=` assignments, `--password x`, `-u user:pass`.
- **Exe parsing** skips env assignments, `cd`, and the wrappers sudo, env, timeout,
  nice, command, exec and time; it drops redirections and heredoc bodies. It does not
  unwrap `uv run` or `bun run`.

## Hook

`zen-telemetry hook`, on `PostToolUse` and `PostToolUseFailure` with matcher `""`,
async. Both events carry `duration_ms`, which excludes permission prompts, so no
`PreToolUse` hook is needed; cancelled calls are not logged. It exits 0 and prints
nothing in every case. It writes nothing without `AGENT_MANAGER_SESSION_ID`, without a
vault, or on a malformed payload. WAL with `busy_timeout` 1000 ms: under contention
the row is dropped, never retried. The hot path is one `INSERT`; the schema is only
created when it fails with "no such table".

## Import

`zen-telemetry import [--days 31] [--dry-run]` streams
`~/.claude/projects/*/*.jsonl` modified in the window and their
`<session>/subagents/*.jsonl` (agent type from the `.meta.json`). A session counts as an
agent session when its id is an `agent_session_id` in `agent-manager sessions --json`
or one of its first five user turns carries the agent-manager banner. Each `tool_use`
pairs with its `tool_result`; duration is the result timestamp minus the use timestamp
(approximate, includes permission waits). Only `assistant`/`user` entries count:
`api-request-blob` lines repeat tool blocks. Inserts are `ON CONFLICT DO NOTHING`, so a
rerun adds 0 rows and a hook row for the same call always wins (the hook upserts).

## Report

`zen-telemetry report [--days 7] [--slow 30s] [--json] [--due]`, window = the days
before the run, labelled with the run's ISO week. It prunes rows older than 365 days,
fills missing session metadata, then prints:

1. Calls over the slow threshold: count, total, top groups with the slowest example.
2. Time per tool (Bash per executable): calls, total, median, p90, fail %, retry %,
   timeouts, median and total output bytes.
3. Same-job comparisons from `scripts/telemetry/jobs.json` (tool names and globs only).
4. Background runs, listed apart since their runtime is not in the payload.
5. The top 6 time-sink candidates: a stable `sink-key` (`bash:zen-merge`, an MCP tool
   name), a suggested title, a `markdown` body with its job comparison, and the tasks
   filed for that key in earlier weeks.
6. Unmapped tools by total time, so the jobs map can grow.

- **Group key:** the tool name, or `bash:<exe>`. Exes listed under `subcommands` in
  jobs.json keep their first argument (`bash:agent-manager wait`).
- **Retry:** the previous call in the same session, agent and group failed less than
  5 min earlier.
- **Sinks** exclude the jobs.json `exclude` list (Agent/Task, whose inner calls are
  logged separately; AskUserQuestion/ExitPlanMode, which wait on a human) and
  background runs. Totals are summed call durations; parallel calls overlap.

`--due` exits 10 and prints nothing when the week is in `telemetry_weeks`.

## Chief's weekly step

At its first start each ISO week chief runs `zen-telemetry report --due --json`. On
exit 10 it skips. Otherwise it files the first 3 sinks with no open task yet as To Do
tasks under #588's project, records each with `zen-telemetry filed <week> <key> <task>`,
and runs `zen-telemetry mark-done <week>`, also when there are fewer than 3 sinks.

## Scale

One user, two machines, each with its own local db (the vault ignores it, so it does
not sync; `host` is stored to allow a merge later). About 1-5k calls a day, 150k rows a
month. The report loads one week (up to ~35k rows) into memory, using the `ts` and
`(session_id, ts)` indexes.
