---
name: zen-flow
description: Vinicius's personal work workflow — the Notion "Main Board" task board (Approva + personal projects), picking what to do next, starting and closing tasks, triage. Use when the user says "my board", "board", "tasks", "what should I do", "what's next", "check my board", "mark it done", "add a task", or wants to plan their day/week.
argument-hint: <check|next|start|done|add|triage> [task]
allowed-tools: Read Bash mcp__claude_ai_Notion__notion-search mcp__claude_ai_Notion__notion-fetch mcp__claude_ai_Notion__notion-query-data-sources mcp__claude_ai_Notion__notion-create-pages mcp__claude_ai_Notion__notion-update-page
---

# zen-flow — personal workflow

Grows over time: each section is one part of the workflow. Add a new `##` section
when a new habit or tool becomes part of the routine; keep sections short and factual.

Storage plan: when a workflow needs local state that Notion or the JSON config handle
poorly (history, multi-step state, joins, queries across runs), propose a SQLite db at
`<vault>/config/zen-flow.db` — the user wants this; suggest it once the need is real.

## Board (Notion)

Single source of truth for tasks. Notion MCP tools are deferred — load them with
`ToolSearch("select:mcp__claude_ai_Notion__notion-query-data-sources,mcp__claude_ai_Notion__notion-fetch,mcp__claude_ai_Notion__notion-update-page,mcp__claude_ai_Notion__notion-create-pages")`.

### IDs and projects — private, read from the vault

This skill lives in a public repo. Database IDs, data source URLs, and the project list
live in the private second-brain vault: read `config/zen-flow.json` under the `vault`
path in `~/.claude/second-brain.json` before any board call. Projects with
`"private": true` must never be named outside the vault.

Never write project names or Notion IDs into this file or any other public repo file.

### Schema

- `Write a task` (title), `Status` (select), `☢️ Projeto` (relation, limit 1),
  `Due Date`, `Conclusion Date`, `Tags` (`Has Data`), `Created time`, `Last Edited`
- `Do` / `Undo` are Notion buttons — not callable via API; replicate their effect with
  property updates (below). They're excluded from SQL.

### Status semantics

| Status | Meaning |
|---|---|
| *(empty)* | Inbox — untriaged idea. Leave alone unless triaging. |
| `🧱 To Do` | Committed, not started |
| `In Progress` | Being worked on now |
| `💥Urgent` | Drop-everything priority |
| `✅ Done` | Finished — always set `Conclusion Date` = today |
| `🗑️ Won't Do` | Dropped |

### Operations

- **check** — list open tasks (Urgent, In Progress, To Do, empty), grouped by project:
  ```sql
  SELECT "Write a task", Status, "☢️ Projeto", "date:Due Date:start" AS due, "Last Edited", url
  FROM "<board.tasks_data_source>"
  WHERE Status IN ('💥Urgent','🧱 To Do','In Progress') OR Status IS NULL
  ORDER BY "Last Edited" DESC
  ```
  Map project URLs to names via `projects[].page_id`. Present Urgent → In Progress → To Do → Inbox.
- **next** — recommend one task: Urgent first, then an existing In Progress (finish before
  starting), then To Do with nearest due date. Give one reason.
- **start** — set `Status` = `In Progress`.
- **done** — set `Status` = `✅ Done` and `date:Conclusion Date:start` = today (ISO date).
- **add** — create a page in the Tasks data source with title, `☢️ Projeto` (ask if
  ambiguous; default Approva when working in an Approva repo), `Status` = `🧱 To Do`
  unless the user says it's just an idea (leave empty).
- **triage** — walk Inbox items one at a time; for each propose To Do / Won't Do / leave.

### Write permissions

- Status changes and new tasks: do it, then report what changed.
- `🗑️ Won't Do`, deleting, or editing a task's title/body: ask first.

## Linking tasks to code

- Approva tasks usually map to a repo under `~/code/approva/` — check the second brain
  (`/second-brain`) index for which repo owns the feature before starting.
- When a task is finished and committed (`zen-commit`), run **done** on the board task.
