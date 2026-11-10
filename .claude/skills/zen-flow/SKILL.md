---
name: zen-flow
description: Vinicius's personal work workflow — the Notion "Main Board" task board (Approva + personal projects), picking what to do next, starting and closing tasks, triage. Use when the user says "my board", "board", "tasks", "what should I do", "what's next", "check my board", "mark it done", "add a task", or wants to plan their day/week.
argument-hint: <check|next|start|done|add|triage|block|id|reopen|note> [task]
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

`board.database_id` is the Main Board's linked view; API calls use the Tasks data
source (`board.tasks_data_source`) or the Tasks database (`board.tasks_database_id`).

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
  One task with a checklist is fine when the steps share a kind or a release (device
  checks, a prod rollout); append new items to an open checklist task instead of
  creating a new one.
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
- **done** — set `Status` = `✅ Done` and `date:Conclusion Date:start` = today (ISO date),
  then **note** with the release tag and commits.
- **add** — create a page in the Tasks data source with title, `☢️ Projeto` (ask if
  ambiguous; default Approva when working in an Approva repo), `Status` = `🧱 To Do`
  unless the user says it's just an idea (leave empty).
- **triage** — walk Inbox items one at a time; for each propose To Do / Won't Do / leave.
- **id** — a task's numeric `userDefined:ID`; create-pages doesn't return it and
  `zen-commit --task` needs it. Query with params `["%<page id: 32 hex, no dashes>%"]`:
  ```sql
  SELECT "userDefined:ID" FROM "<board.tasks_data_source>" WHERE url LIKE ?
  ```
- **reopen** — set `Status` = `In Progress` and `date:Conclusion Date:start` = null.
- **note** — append `**YYYY-MM-DD:** …` at the end of the page with `insert_content`;
  Done notes carry the release tag and commits.

### Write permissions

- Status changes, new tasks, reopen and note: do it, then report what changed. A dated
  note is not a body edit.
- `🗑️ Won't Do`, deleting, or editing a task's title/body: ask first.

## Linking tasks to code

- Work tasks map to a repo listed under `repos` in the vault `config/zen-flow.json`;
  check the second brain (`/second-brain`) index for which repo owns the feature
  before starting. Personal repos live under `repos.personal`; `agent_groups` maps an
  agent-manager group to its board project.
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
  (task ID, page URL, repo, model tier) and sets them In Progress. It does not relay
  decisions (see Decision inbox), keeps the `decisions` watcher running, records answers
  the user gives in chat with `zen-inbox answer`, and lists Human Only work. It never
  waits on sessions, merges or releases. After an agent-manager restart it revives the
  orchestrator and the watcher.
- **spec-writer** writes the spec into the task page body: goal, done-criteria, file
  scope, required tests, expected load, settled decisions, out of scope, Human Only
  steps. Open decisions go to the inbox, one `zen-inbox ask` each; the answers arrive
  together in one message. Its only board write is that
  page body. Ends with `SPEC_READY <id>`.
- **orchestrator** spawns workers (`saved: worker-rules`; Opus for design-heavy or
  cross-cutting work, Sonnet for pattern or spec work; max 3; `list_reservations`
  before a second worker in one repo) and waits on them. A stuck Sonnet worker gets one
  relaunch on Opus on the same branch; anything else becomes a gate in the inbox
  (`zen-inbox ask <task> --gate`, `failing · tried · cause · needs` in `--context`).
- **worker** commits on its branch with `zen-commit --task <id>` (adds a `Task: #<id>`
  trailer), reports escalations to the orchestrator, and ends with `WORKER_DONE <id>`.
  Its rules list the banned ways to get green (skipped tests, widened timeouts, mocked
  code under test, catch-all fallbacks, RLS/auth bypass) and the "Lessons from
  ponytail" coding rules, adopted from ponytail.

### Scripts

Repeated orchestration lives in `zen-*` scripts, so an agent only wakes when one exits
non-zero. Each takes `--json`; see `scripts/README.md`.

- `zen-await <session>...` (orchestrator, run_in_background): waits until a worker
  stops; 0 done marker, 10 escalation, 11 idle, 20 dead, 124 timeout.
- `zen-gate [--base dev]` (worker, before `WORKER_DONE`): banned ways to get green
  in the branch diff; exceptions in `.zen-gate-allow`.
- `zen-merge <session>` (orchestrator, on exit 0): gate, ff-merge, dev release,
  archive, worktree and branch cleanup; 2 gate, 3 not ff, 4 dirty, 5 release.
- `zen-sweep` (chief, at start and after a restart): read-only report of dirty
  trees, unpushed work, stashes, orphan branches and worktrees.
- `zen-inbox` (spec-writer, orchestrator, chief): the decision inbox below.

### Decision inbox

Agents never message chief with decisions. They run `zen-inbox ask <task> -q ... -o ...
--rec <letter>` (gates: `--gate`) and end the turn. A terminal named `decisions` in group
`chief` runs `zen-inbox watch`: open decisions appear live (zen-bell rings), and the
user answers by pick, free text, or talk (a tmux popup attached to `am_<session>`;
after detaching, `resolved?` closes it with an optional note and sends nothing).
Answers are held until the asking session has no open decision, then go out as one
`agent-manager send <session> ... --from zen-inbox` message. A failed delivery stays
listed until `zen-inbox retry <session>`.

- IDs `<task>.<n>` and gates `<task>.g`, `.g2`, never reused; `amend`/`withdraw` are
  the asker's (`--force` for chief).
- Storage: `<vault>/config/zen-flow.db` (SQLite, gitignored in the vault).
- Chief keeps the watcher up: `agent-manager terminal list --json` shows a running
  `decisions` terminal in `chief`, else `agent-manager terminal create --name decisions
  --group chief --nest=false --json` and `agent-manager terminal send <id> --command
  "zen-inbox watch"`.

The user never reviews diffs. A worker is done only on `WORKER_DONE <id>`; then the
orchestrator:

- checks green was reported and one `/code-review` round ran, then runs `zen-merge`:
  `zen-gate` on the branch diff, `git merge --ff-only` into `dev` in the repo's main
  checkout, `zen-release --dev --yes --bump patch`, then cleanup (below); each
  shortcut follow-up it prints becomes a To Do task
- marks the task Done (**done**) with the release tag in a note
- files open review findings as To Do tasks, and device, prod, data or
  messages-to-people steps as Human Only tasks
- leaves cleanup to `zen-merge`: archive the session, `git worktree remove <path>`,
  `git branch -d <branch>` (`-D` once `git merge-base --is-ancestor <branch> dev`
  confirms it); then sends chief one line

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
