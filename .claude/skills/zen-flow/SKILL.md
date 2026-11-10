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
  `Due Date`, `Conclusion Date`, `Tags` (`Has Data`), `Priority` (select),
  `Related Tasks` (two-way self-relation: link one side, both show it),
  `Created time`, `Last Edited`
- `Do` / `Undo` are Notion buttons — not callable via API; replicate their effect with
  property updates (below). They're excluded from SQL.

### Status semantics

| Status | Meaning |
|---|---|
| *(empty)* | Inbox — untriaged idea. Leave alone unless triaging. |
| `🧱 To Do` | Committed, not started |
| `In Progress` | Being worked on now |
| `✅ Done` | Finished — always set `Conclusion Date` = today |
| `🗑️ Won't Do` | Dropped |
| `Blocked` | **Blocked on someone else** (a colleague, a partner, an external approval). The task page says who and since when. Not the user's own pending steps — those are `🧱 To Do` + `Human Only`. |

### Priority

`High`, `Medium`, `Low`, or empty. Agents set `Low` or `Medium` freely; only the user
sets `High` (it replaces the old Urgent status). Never set `High`, and never change or
clear a task that is already `High`; `zen-board priority` refuses both (exit 2).

### Related Tasks

Follow-ups (review findings, Human Only steps, shortcuts) link to the task they came
from, and a folded task links to the one it folded into. A dated note still says why.
The relation is two-way, so one write links both pages.

### Tags (labels)

- `Human Only` — a step that needs the user's own eyes, hands, accounts or secrets:
  visual and device checks, steps on another machine, messages or calls to people,
  creating or typing secrets and API keys, logins to outside dashboards and apps,
  purchases, irreversible destruction (DB drops, deleting prod stacks). When a session
  leaves such steps behind, create one task per step with this tag so the board says
  what to do next. One task with a checklist is fine when the steps share a kind or a
  release (device checks, a prod rollout); append new items to an open checklist task
  instead of creating a new one.
- `Human Approval` — a step an agent can run from this machine with credentials it
  already has, once the user approves: pushes, remote branch deletes, edits to
  `.claude/settings.json` or permissions, tool installs, `gh` repo settings, prod
  releases, deploys and migrations, archiving data, scripts run against prod. It goes
  to the inbox as an approval ask (see Agent roles), not as a Done/Later request.
  If the user answers "I'll do it myself", re-tag the task `Human Only` and keep the
  ask's commands in it.
- `Has Data` — the task page carries data/notes worth reading before starting.

### Operations

**check, next, start, done, add and note run `zen-board` first** (Bash; see
`scripts/README.md`): `zen-board check`, `next`, `start <id>`,
`done <id> --note "…"`, `add "<title>" --project <name or group> [--inbox]
[--human-only] [--human-approval] [--priority low|medium] [--related <id>]...`
(prints the new ID),
`priority <id> low|medium|none`, `relate <id> <id>...` (adds to the existing links,
never drops one), `note <id> "…"`. It reads the IDs from the vault
config and `NOTION_TOKEN` from the env or `~/.zshrc.local`. When it exits non-zero
(1 API, 2 not found, 3 token/config missing), fall back to the MCP steps below.
Triage, block, reopen, title/body edits and Won't Do stay on MCP.

- **check** — list open tasks (In Progress, To Do, Blocked, empty) with Priority, grouped by project:
  ```sql
  SELECT "Write a task", Status, Priority, "☢️ Projeto", "date:Due Date:start" AS due, "Last Edited", url
  FROM "<board.tasks_data_source>"
  WHERE Status IN ('🧱 To Do','In Progress','Blocked') OR Status IS NULL
  ORDER BY "Last Edited" DESC
  ```
  Map project URLs to names via `projects[].page_id`. Present In Progress → To Do (High first) →
  Blocked (with who) → Inbox.
- **next** — recommend one task: an existing In Progress first (finish before starting),
  then To Do by Priority (High, Medium, Low, none), then nearest due date, then oldest.
  Give one reason. Blocked tasks are listed
  separately ("waiting on …") and never recommended; nudge if blocked for more than a week.
- **block** — set `Status` = `Blocked` and append "Aguardando <quem> desde <data>" to the
  page. When the user says the blocker cleared, move it back to `In Progress`.
- **start** — set `Status` = `In Progress`.
- **done** — set `Status` = `✅ Done` and `date:Conclusion Date:start` = today (ISO date),
  then **note** with the release tag and commits.
- **add** — create a page in the Tasks data source with title, `☢️ Projeto` (ask if
  ambiguous; default Approva when working in an Approva repo), `Status` = `🧱 To Do`
  unless the user says it's just an idea (leave empty). A follow-up gets `--related
  <origin id>`; `Priority` only `low` or `medium`.
- **relate** — `zen-board relate <id> <id>...` links tasks in Related Tasks. On MCP,
  read the existing relation first and write the union.
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
  note is not a body edit. Applies to chief and the orchestrator, who change status
  and append dated notes without asking. Workers don't write to the board.
- `🗑️ Won't Do`, deleting, or editing a task's title/body: ask first. Exception: a
  spec-writer writes its spec into the body of the task it was spawned for (its only
  board write).
- Prod actions: a push, tag, release or deploy that targets `prod`, `main` or
  `master` is filed as `zen-inbox ask <task> --prod <push|tag|release|deploy>
  --repo <repo> --branch <branch> --sha <40-char sha>`, approved once per action in the
  panel, in every repo (Approva included). `zen-release --approval <id>` consumes
  it; file the ask before any such `git push`. Agents never answer a `--prod` ask. Workers
  never run one; chief or the orchestrator may after my approval. Never ff-merge a
  worker branch into `prod`.

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
| --- | --- | --- | --- |
| chief        | `chief`        | Opus · max              | the one the user talks to; group `chief`              |
| spec-writer  | `spec-writer`  | Opus · high             | one per task, short-lived; project group; no worktree |
| orchestrator | `orchestrator` | Opus · high             | one, long-running; group `chief`; no worktree         |
| worker       | `worker-rules` | Opus or Sonnet · medium | one per task and repo; project group; worktree        |

- **chief** reads the board, gets the user's approval, spawns a spec-writer per task
  (or writes a trivial spec itself), hands `SPEC_READY` tasks to the orchestrator
  (task ID, page URL, repo, model tier) and sets them In Progress. It does not relay
  decisions (see Decision inbox), keeps the `decisions` watcher running, records answers
  the user gives in chat with `zen-inbox answer` (never a `--prod` ask: the user
  approves those in the panel), runs each Human Approval step the user approves (or hands
  it to the orchestrator; if auto mode blocks it, it asks for `go #<id>`), re-tags a step
  `Human Only` when the user answers "I'll do it myself", and lists Human Only work. On the user's `go #<id>` in
  its chat it spawns that queue entry's worker (`worker-rules`, tier and repo from the
  entry) and sends the orchestrator the session id; if auto mode blocks it too, it says
  so in one line. It never waits on sessions, merges or cuts dev releases; a prod
  action it runs only after an approved ask (Write permissions). After an agent-manager restart it revives the
  orchestrator and the watcher. At its first start each ISO week it runs the telemetry
  review (see Telemetry).
- **spec-writer** writes the spec into the task page body: goal, done-criteria, file
  scope, required tests, expected load, settled decisions, out of scope, Human Only
  steps. Open decisions go to the inbox, one `zen-inbox ask` each; the answers arrive
  together in one message. Its only board write is that
  page body. Ends with `SPEC_READY <id>`.
- **orchestrator** spawns workers (`saved: worker-rules`; Opus for design-heavy or
  cross-cutting work, Sonnet for pattern or spec work; max 3 core workers, or 2 while the
  5-minute load average (middle number of `uptime`) is over 150, checked before each core
  claim and never by stopping running workers; plus up to 2 dotfiles workers (light: unit
  tests, no browsers or dev servers); the core-heavy lock `~/.cache/zen/core-heavy.lock`
  covers browser runs, `just check`, tsc/tsgo, `just test` and builds; `list_reservations`
  before a second worker in one repo) and waits on them. When auto mode blocks a spawn it
  files a gate (`zen-inbox ask <task> --gate`) telling the user to type `go #<id>` in
  chief's chat, then adopts the worker chief starts (zen-await, merge as usual). A stuck Sonnet worker gets one
  relaunch on Opus on the same branch; anything else becomes a gate in the inbox
  (`zen-inbox ask <task> --gate`, `failing · tried · cause · needs` in `--context`).
- **worker** commits on its branch with `zen-commit --task <id>` (adds a `Task: #<id>`
  trailer), never pushes or releases (a prod push it needs goes in its final
  message), reports escalations to the orchestrator, and ends with `WORKER_DONE <id>`.
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
- `zen-merge <session>` (orchestrator, on exit 0): gate, ff-merge, dev release
  (base `dev` only; a single-branch repo lands locally as `push pending`),
  archive, worktree and branch cleanup, then marks the task Done via `zen-board`
  with the tag and shas in a note (`--phase N/M` on a non-final phase only appends
  the note and leaves the task open; `--task <id>` overrides the recovered ID);
  2 gate, 3 not ff, 4 dirty, 5 release, 6 refused (see Branch model), 7 board.
- `zen-board` (everyone): the routine board ops above, over REST.
- `zen-sweep` (chief, at start and after a restart): read-only report of dirty
  trees, unpushed work, stashes, orphan branches and worktrees, and `branch-model`
  drift in personal repos.
- `zen-lock <name> [--wait SECS] -- <cmd>` (worker): runs a core-heavy command under
  `~/.cache/zen/<name>.lock`; releases on exit/INT/TERM, takes over a lock whose owner
  pid is gone, exits 75 on timeout. `status <name>`, `break <name>`.
- `zen-inbox` (spec-writer, orchestrator, chief): the decision inbox below.
- `zen-telemetry` (hook on every agent tool call; chief weekly): see Telemetry.

### Telemetry

A `PostToolUse`/`PostToolUseFailure` hook (`zen-telemetry hook`, async) logs every tool
call of an agent-manager session into `zen-flow.db` (`tool_*` tables; secrets redacted,
MCP inputs as key names only). Chief, at its first start each ISO week, runs
`zen-telemetry report --due --json`: exit 10 means done, skip. Otherwise it files the
first 3 `sinks` with no open task yet as To Do tasks under #588's project (title = the
sink's `title`, body = its `markdown`), records each with `zen-telemetry filed <week>
<sink key> <task id>`, then runs `zen-telemetry mark-done <week>`, also when there
are fewer than 3 sinks or no data. The same-job map is `scripts/telemetry/jobs.json`.

### Decision inbox

Agents never message chief with decisions. They run `zen-inbox ask <task> -q ... -o ...
--rec <letter>` (gates: `--gate`), passing `--title "<task title>" --brief "<1-3 lines>"`
on the first ask, and end the turn; `zen-inbox brief` sets that context later. A
terminal named `decisions` in group `chief` runs `zen-inbox watch`: one row per asking
session (zen-bell rings once per session per poll). Opening one shows the task context, then
its questions; the user walks them (pick, free text, skip) and sends them from a
review screen, or talks (a tmux popup on `tmux -L agentmgr attach -t am_<session>`,
then a pick of which decisions it settled; those close and send nothing).
Answers are held until the asking session has no open decision, then go out as one
`agent-manager send <session> ... --from zen-inbox` message. A failed delivery stays
listed until `zen-inbox retry <session>`.

- IDs `<task>.<n>` and gates `<task>.g`, `.g2`, never reused; `amend`/`withdraw` are
  the asker's (`--force` for chief).
- Storage: `<vault>/config/zen-flow.db` (SQLite, gitignored in the vault).
- Chief keeps the watcher up: `agent-manager terminal list --json` shows a running
  `decisions` terminal in `chief`, else `agent-manager terminal create --name decisions
  --group chief --nest=false --json` and `tmux -L agentmgr send-keys -t am_<terminal id>
  'zen-inbox watch' Enter` (agent-manager refuses `terminal send` to an un-nested
  terminal from inside a session).

The user never reviews diffs. A worker is done only on `WORKER_DONE <id>`; then the
orchestrator:

- checks green was reported and one `/code-review` round ran, then runs `zen-merge`:
  `zen-gate` on the branch diff, `git merge --ff-only` into the base (`dev`, or the
  single branch) in the repo's main checkout, `zen-release --dev --yes --bump patch`
  on `dev` only, then cleanup (below); each
  shortcut follow-up it prints becomes a To Do task linked with `--related <origin id>`
- leaves **done** to `zen-merge`'s board phase (tag and shas in the note); on
  exit 7 it reruns `zen-merge` or marks it Done via MCP, and on `board skipped`
  (no `Task:` trailer) it marks it Done itself
- files open review findings as To Do tasks, and classifies each leftover step: Human
  Only (device, secrets, messages to people, outside logins, purchases, irreversible
  destruction) becomes a Human Only task; Human Approval (an agent can run it from this
  machine once approved: pushes, settings edits, installs, deploys, prod scripts)
  becomes a Human Approval task plus an ask stating the exact commands:
  `zen-inbox ask <task> -q "<what the agent will run>. Approve?" -o "Approve: an agent
  runs it" -o "I'll do it myself" -o "Later" --rec a` (`--prod …` for prod actions,
  which only the user answers)
- leaves cleanup to `zen-merge`: archive the session, `git worktree remove <path>`,
  `git branch -d <branch>` (`-D` once `git merge-base --is-ancestor <branch> dev`
  confirms it); then sends chief one line

Single-branch repos (`main` or `master` only, such as dotfiles): `zen-merge` lands
locally and reports `push pending`; the orchestrator files the push as a prod-action
ask (Write permissions) and pushes only after "Approve". Prod releases and deploys of
dev+prod repos go through the same ask.

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

Single-branch repos skip step 2: `zen-release --dev` refuses `prod`, `main`,
`master` and the saved release target. Prod releases and deploys are not part of
this routine; each one needs an approved prod-action ask (`~/.claude/CLAUDE.md`).

## Branch model

Two models; the tooling detects which one a repo uses from its local branches.

- **dev + prod**: agents land and dev-release on `dev`; `prod` is reached only by a
  prod release (`zen-release`, `.git/zen-release.json` branches `dev → prod`).
  `zen-merge` picks this model when the repo has a `dev` branch.
- **single-branch** (`main` or `master` only): agents land on that branch locally,
  never cut a dev release, and the push waits for an approved ask. `zen-merge` picks
  this model when there is no `dev` and the main checkout is on `main` or `master`.

Anything else (no `dev`, checkout on another branch) is refused with exit 6;
`zen-merge` never creates a `dev`. Landing on `prod` is always refused, and so is
the saved release target in a dev+prod repo. `zen-release` pre-fills `dev → prod`
on a first run when both exist.
`zen-sweep` reports `branch-model` drift in personal repos only: `dev` without
`prod` (or the reverse), a `main`/`master` left beside dev+prod, or a dev+prod repo
whose `origin/HEAD` is not `dev`.

The per-repo model list lives in the vault (`repos.branch_models` in
`config/zen-flow.json`). Public ones: dotfiles is single `master` (raw URLs and
clones depend on it); the vault is single `main` (never a worker target, never
released).

Moving a repo to dev+prod, in its main checkout with a clean tree (local steps):

1. Keep the released history as `prod`: `git branch -m main prod` (or `master`).
   Never cut `prod` from `dev`.
2. Create `dev` if missing: `git branch dev prod`, then `git switch dev`.
3. Delete a leftover `main`/`master` already merged into `prod`: `git branch -d main`.
4. Write `.git/zen-release.json` with `"branches": {"source": "dev", "target":
   "prod"}` (or run `zen-release --reconfigure` and accept the defaults).

Remote steps are prod actions or Human Approval: pushing `dev` and `prod` goes through
an approved ask; the GitHub default branch (`dev`) and branch protection on `prod` are
`gh` repo settings, so Human Approval. After the default branch changes, `git remote set-head origin -a`
refreshes the local `origin/HEAD` that `zen-sweep` checks.
