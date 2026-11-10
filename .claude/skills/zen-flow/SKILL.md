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
| `Blocked` | **Blocked on someone else** (a colleague, a partner, an external approval). The task page says who and since when. Not the user's own pending steps — those are `🧱 To Do` + `Human Only`. |

### Tags (labels)

- `Human Only` — a step only the user can do (deploys, visual review, messages to
  colleagues, CMS edits, pushes of the public dotfiles). When a session leaves such steps
  behind, create one task per step with this tag so the board says what to do next.
- `Has Data` — the task page carries data/notes worth reading before starting.

### Operations

- **check** — list open tasks (Urgent, In Progress, To Do, empty), grouped by project:
  ```sql
  SELECT "Write a task", Status, "☢️ Projeto", "date:Due Date:start" AS due, "Last Edited", url
  FROM "<board.tasks_data_source>"
  WHERE Status IN ('💥Urgent','🧱 To Do','In Progress','Blocked') OR Status IS NULL
  ORDER BY "Last Edited" DESC
  ```
  Map project URLs to names via `projects[].page_id`. Present Urgent → In Progress → To Do →
  Blocked (with who) → Inbox.
- **next** — recommend one task: Urgent first, then an existing In Progress (finish before
  starting), then To Do with nearest due date. Give one reason. Blocked tasks are listed
  separately ("waiting on …") and never recommended; nudge if blocked for more than a week.
- **block** — set `Status` = `Blocked` and append "Aguardando <quem> desde <data>" to the
  page. When the user says the blocker cleared, move it back to `In Progress`.
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

- Work tasks map to a repo listed under `repos` in the vault `config/zen-flow.json`;
  check the second brain (`/second-brain`) index for which repo owns the feature
  before starting.
- When a task is finished and committed (`zen-commit`), run **done** on the board task.

## Agent roles

Four agent-manager presets (`agent-manager/config.json`). Presets hold instructions
only; model and effort are set per session (`create_session` `model`/`effort`, or
`agent-manager spawn --model --effort`).

| Role         | Preset         | Model · effort          | Session                                               |
| ------------ | -------------- | ----------------------- | ----------------------------------------------------- |
| chief        | `chief`        | Opus · max              | the one the user talks to; group `chief`              |
| spec-writer  | `spec-writer`  | Opus · high             | one per task, short-lived; project group; no worktree |
| orchestrator | `orchestrator` | Opus · high             | one, long-running; group `chief`; no worktree         |
| worker       | `worker-rules` | Opus or Sonnet · medium | one per task and repo; project group; worktree        |

- **chief** reads the board, gets the user's approval, spawns a spec-writer per task
  (or writes a trivial spec itself), hands `SPEC_READY` tasks to the orchestrator
  (task ID, page URL, repo, model tier) and sets them In Progress. It relays decision
  gates as numbered decisions and lists Human Only work. It never waits on sessions,
  merges or releases. After an agent-manager restart it revives the orchestrator.
- **spec-writer** writes the spec into the task page body: goal, done-criteria, file
  scope, required tests, expected load, settled decisions, out of scope, Human Only
  steps. Open
  decisions go to chief as numbered questions. Its only board write is that page body.
  Ends with `SPEC_READY <id>`.
- **orchestrator** spawns workers (`saved: worker-rules`; Opus for design-heavy or
  cross-cutting work, Sonnet for pattern or spec work; max 3; `list_reservations`
  before a second worker in one repo) and waits on them. A stuck Sonnet worker gets one
  relaunch on Opus on the same branch; anything else goes to chief as a decision gate
  (`failing · tried · cause · needs`).
- **worker** commits on its branch with `zen-commit --task <id>` (adds a `Task: #<id>`
  trailer), reports escalations to the orchestrator, and ends with `WORKER_DONE <id>`.
  Its rules list the banned ways to get green (skipped tests, widened timeouts, mocked
  code under test, catch-all fallbacks, RLS/auth bypass) and the "Lessons from
  ponytail" coding rules, adopted from ponytail.

The user never reviews diffs. A worker is done only on `WORKER_DONE <id>`; then the
orchestrator:

- checks green was reported, one `/code-review` round ran, and the branch diff has no
  banned way to get green; each `shortcut:` marker in the diff becomes a To Do task
- merges into `dev` in the repo's main checkout (`git merge --ff-only`; for a real
  merge, re-runs the repo's checks first), runs `zen-release --dev --yes --bump patch`,
  and marks the task Done (**done**) with the release tag in a note
- files open review findings as To Do tasks, and device, prod, data or
  messages-to-people steps as Human Only tasks
- archives the session, runs `git worktree remove <path>` and `git branch -d <branch>`
  (`-D` once `git merge-base --is-ancestor <branch> dev` confirms it), and sends chief
  one line

Repos without `dev` (dotfiles on master): the orchestrator merges locally and leaves
the push to the user. Prod merges, prod releases and deploys stay the user's.

Start chief from any shell; check `agent-manager sessions` first so only one runs:

```
agent-manager spawn \
  --name chief \
  --saved chief \
  --tool claude \
  --model opus \
  --effort max \
  --group chief \
  --prompt "Start: check the board."
```

## Commit → dev release routine (every repo)

Each batch of work on a dev branch ends the same way, in every repo touched
(`repos` in the vault config), not just the one the session started in:

1. `zen-commit --all -m "<type>(<scope>): …"` — one commit per logical change.
2. `zen-release --dev --yes --bump patch` — bumps the version to `-dev.N`, tags and
   pushes. Skipping this leaves the dev branch on the prod version number (happened on
   2026-10-14).
3. Board: **done** (or a status note) on the task, with the release tag in the note.

Prod releases and deploys are never run here — the user does them (`~/.claude/CLAUDE.md`).
