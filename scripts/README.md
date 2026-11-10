# scripts

Utility scripts for this dotfiles repo. The Python scripts are
[`uv`](https://docs.astral.sh/uv/) single-file scripts — they declare their own
deps in a `# /// script` header and run via the `#!/usr/bin/env -S uv run --script`
shebang, so no virtualenv setup is needed.

The `zen-*` commands are symlinked onto `PATH` by the root `./link-bin` script,
which owns the authoritative name → source table. `./link-bin --check` reports
drift (missing, dangling, or unmanaged links) without changing anything.

## Commands

| Script | Command | What it does |
|---|---|---|
| `commit.py` | `zen-commit` | Conventional-Commit helper: stage → secret-scan → AI message (claude haiku) → commit. |
| `release.py` | `zen-release` | Resumable release orchestrator: version bump, tag, changelog, push, GitHub release. `--dev` refuses prod/main/master and the saved target. |
| `await.py` | `zen-await` | Waits on agent-manager sessions and classifies the last message (done, escalation, idle, dead). |
| `gate.py` | `zen-gate` | Flags skipped tests, widened timeouts, auth bypasses and other fake greens in a branch diff. |
| `merge.py` | `zen-merge` | Lands a worker branch: gate, ff-merge, dev release, session + worktree cleanup. Resumable; `--dry-run` prints `dry-run, nothing changed` and JSON `cleanup: dry-run`. |
| `board.py` | `zen-board` | Everyday Notion board ops over REST: check, next, start, done, add, note, priority, relate. |
| `sweep.py` | `zen-sweep` | Read-only report of dirty trees, unpushed work, stashes, orphan branches/worktrees and `branch-model` drift. |
| `../inbox/` (Go) | `zen-inbox` | Decision inbox: agents `ask`, a Bubble Tea watcher answers by pick, free text or talk; batched delivery. See `inbox/README.md`. |
| `telemetry/` (Go) | `zen-telemetry` | Hook that logs every agent tool call to `zen-flow.db`; weekly time-sink report for chief. |
| `pgp.py` | `zen-pgp` | gum-driven `gpg` encrypt/decrypt helper (symmetric or to a recipient key). |
| `env-build.py` | `zen-env` | Resolves `.env.example` parameters from AWS SSM Parameter Store. |
| `hash.sh` | `zen-hash` | `sha256sum` helper: hashes files given as args, or gum-picks them. |
| `ytdl.py` | `zen-ytdl` | Interactive `yt-dlp` downloader with WhatsApp-safe re-encode. |
| `doc.sh` | `zen-doc` | Adds PDFs/docs to the second-brain vault as Markdown (markitdown/docling) and refreshes the qmd index. |
| `wrapup.sh` | `zen-wrapup` | SessionEnd hook: digests the transcript and runs the `wrapup` skill headless (memories, vault session log, consolidation). |
| `bell.sh` | `zen-bell` | Plays a feedback sound / desktop notification, unless the terminal is focused. |
| `config.sh` | `zen-config` | Reads personal settings from `zen.json` (+ untracked `~/.config/zen/config.json`). |
| `run.sh` | — | Fuzzy picker that lists & runs the scripts in this dir (and aliases). |
| `screenkey.sh` | — | Loops `screenkey` for on-screen keystroke display (demos/screencasts). |

### zen-commit
```
zen-commit                 # interactive: pick files, AI message, confirm
zen-commit --all --yes     # headless: stage everything, accept AI message
zen-commit --all -m "..."  # headless: verbatim message, skip AI
zen-commit --all --yes --task 42  # appends a "Task: #42" trailer
```
Stages files, scans the staged diff for secrets/sensitive files, generates a
Conventional Commit message, then lets you commit / edit / regenerate. Runs
headless (no `gum` prompts) when there's no TTY or `--yes`/`-m` is given. The
secret guardrail aborts on findings; `--force` overrides it.

Env files are flagged by name, with two exceptions: the documentation suffixes
(`.env.example`, `.sample`, `.template`) and **encrypted** ones (`.env.gpg`,
`.age`, `.asc`, `.enc`) — the latter committed on purpose beside a decrypt
script. The suffix is not enough on its own: the guardrail also reads the
staged bytes and still aborts if they aren't an encrypted blob, so renaming a
plaintext `.env` to `.env.gpg` does not get past it.

### zen-release
```
zen-release setup          # run once per repo: detect tools, configure branches/phases
zen-release                # full release
zen-release --dev          # quick dev (-dev.N) release on current branch; refused on prod/main/master/target
zen-release --resume        # continue a release interrupted mid-run
zen-release --yes --bump <patch|minor|major>   # headless
```
Resumable, interactive orchestrator — state is saved so an interrupted run can
`--resume`. `--dry-run` echoes mutating commands instead of running them.

A release flows up a **merge chain**, target last: the branch you're on → the
configured source → the target (e.g. `feature/x → develop → master`). On a
feature branch it merges up through the source; on the target with no source it
releases in place (no merge, no checkout). `--source`/`--target` override the
saved config for one run. See `docs/wiki/architecture/release-tooling.md` for the
full design, `BACKLOG.md` for future work. A first run pre-fills source `dev`,
target `prod` when both branches exist (also headless with no saved config).

### Agent orchestration (zen-await, zen-gate, zen-merge, zen-sweep)

Repeated orchestrator work as scripts, so an agent only wakes on a non-zero exit.
All take `--json`; the header of each script is the full reference.

```
zen-await <session>... [--timeout 4h] [--new]   # 0 done · 10 escalation · 11 idle · 20 dead · 124 timeout
zen-await --summary <session>...                # classify each last message now; worst code wins
zen-gate [--base dev]                           # 0 clean · 1 hits (file:line rule)
zen-merge <session> [--base dev] [--phase N/M] [--task ID] [--dry-run]
                                                # 0 landed · 2 gate · 3 not ff · 4 dirty · 5 release · 6 refused · 7 board
zen-sweep                                       # 0 clean · 1 findings; never changes anything
```

- **zen-await** loops `agent-manager wait` and reads the last `end_turn` text from
  `~/.claude/projects/<slug>/<agent_session_id>.jsonl`. `WORKER_DONE <id>` /
  `SPEC_READY <id>` = done; `failing · tried · cause · needs` = escalation. Agents run
  it from Bash with `run_in_background`; `--new` ignores a message already there.
  A live worker with no reply yet is waited on for up to 10m, then reported idle; an
  archived, stopped, dead or errored session is dead (20) unless it ended with a done
  marker. A session counts as gone only when a readable, non-empty list shows it dead
  or omits it twice 5s apart; an unreadable or empty list keeps it waiting. An
  unreadable list at start-up exits 2 ("agent-manager sessions failed").
  Re-run it after an agent-manager restart, which kills background runs.
- **zen-gate** scans only added lines (docs skipped). `shortcut:` markers print as
  follow-ups, not failures. `.zen-gate-allow` takes `<path glob> [rule]` per line.
- **zen-merge** keeps state in `~/.local/state/zen-merge/<session>.json`; rerun to
  resume. A repo without `dev` whose main checkout is on `main` or `master` lands
  there and reports "push pending"; any other checkout exits 6. Only base `dev` is
  released. It refuses (exit 6) prod, and the zen-release target branch when the repo has `dev`.
  Its last phase runs `zen-board done <task> --note "<tag> · <shas>"` from the
  branch's `Task:` trailer, or the base reflog's `merge <branch>:` range once merged
  by hand, or `--task` (skipped without an ID); exit 7 = that step failed.
  `--phase N/M` on a non-final phase runs `zen-board note` instead and leaves the
  task open (`board noted`).
- **zen-sweep** covers the vault `repos` (config/zen-flow.json) plus
  `~/code/personal/*`, and every worktree of each. A linked worktree with a live
  session is skipped and noted as `live worker: <path> (<branch>)`; the main checkout
  is always checked. Session parsing is shared in `scripts/lib/sessions.py`.
  Repos under `~/code/personal` also get `branch-model`: `dev` without `prod` (or
  the reverse), a `main`/`master` beside dev+prod, or a dev+prod `origin/HEAD`
  other than `dev`. Single-branch repos are valid.

### zen-inbox
```
zen-inbox ask <task> -q "<question>" -o a -o b --rec a   # prints 580.1 (--gate: 580.g)
          [--title "<task title>"] [--brief "<1-3 lines>"]  # session+task context
zen-inbox brief <task> [--title ...] [--text ...] [--session <id>]  # set/replace it
zen-inbox amend|withdraw <id> [--force]                 # asker only, --force for chief
zen-inbox list [--all] | show <id>                      # --json on both
zen-inbox answer <id> (<letter> | --text "...") [--note "..."]
zen-inbox resolve <id> [--note "..."]                   # settled in talk; nothing sent
zen-inbox retry <session>                               # resend a failed delivery
zen-inbox watch                                         # one row per session
```
SQLite at `<vault>/config/zen-flow.db` (`ZEN_FLOW_DB` overrides). Answers are held
until the asking session has no open decision, then sent as one
`agent-manager send … --from zen-inbox` message (without `--from` when run inside an
agent pane, where agent-manager refuses it). An agent that withdraws its own last
open decision gets the held answers printed on stdout instead of a send to itself.
Exit codes: 0 ok · 1 not found or wrong state · 2 usage · 3 recorded, delivery
failed · 4 vault, db or Go unavailable. `watch` is a Bubble Tea panel: one row per
asking session; opening one shows each task's title, brief and asker, then its
questions. Answer walks them (`a`-`d` pick, `f` free text, `s` skip, `t` talk, `v`
view full, `esc` back), a review screen sends them; nothing is recorded before Send.
The open session updates live: amended questions show their new text, ones answered
elsewhere leave the walk, new ones join it. Talk opens `tmux display-popup` on
`tmux -L agentmgr attach -t am_<session>`, then asks which decisions it settled.
It is a Go binary: `inbox/zen-inbox` rebuilds it into `~/.cache/zen/bin` when a
source changed (see `inbox/README.md`). See `docs/wiki/architecture/decision-inbox.md`.

### zen-board
```
zen-board check [--json]                          # open tasks with Priority, In Progress → Inbox
zen-board next [--json]                           # In Progress, then To Do by Priority, due, age
zen-board start <task>                            # In Progress
zen-board done <task> [--note TEXT]               # Done + Conclusion Date, one PATCH
zen-board add <title> --project P [--inbox] [--human-only]   # prints the new ID
          [--priority low|medium] [--related <task>]...
zen-board priority <task> low|medium|none         # refuses High and tasks already High (exit 2)
zen-board relate <task> <task>...                 # adds to Related Tasks, keeps old links
zen-board note <task> <text>                      # appends **YYYY-MM-DD:** text
```
`<task>` is `570`, `#570`, a page URL or a page UUID. `--json` lists `related` as
page UUIDs (no dashes) and `priority` as null when unset. Calls the Notion REST API
(`Notion-Version: 2025-09-03`) on the vault's `board.tasks_data_source`; every ID
comes from the vault `config/zen-flow.json`. `NOTION_TOKEN` is read from the env,
else from the `export NOTION_TOKEN=` line in `~/.zshrc.local`. Exit 0 ok · 1 API ·
2 usage or not found · 3 token/config missing. Block, reopen, triage and Won't Do
stay on the Notion MCP (zen-flow skill).
### zen-telemetry
```
zen-telemetry hook                              # PostToolUse/PostToolUseFailure hook; reads stdin, always exits 0
zen-telemetry import [--days 31] [--dry-run]    # one-time import from ~/.claude/projects transcripts
zen-telemetry report [--days 7] [--slow 30s] [--json] [--due]   # --due: exit 10 if this week is done
zen-telemetry filed <week> <sink-key> <task-id> # chief records a filed task
zen-telemetry mark-done <week>                  # chief closes the week
```
A Go module in `scripts/telemetry/` (modernc sqlite, no cgo), built by `./link-bin` to
`scripts/telemetry/bin/` (gitignored), because it runs on every tool call. It logs
only sessions with `AGENT_MANAGER_SESSION_ID` set, into the `tool_*`/`telemetry_*`
tables of `zen-flow.db` beside zen-inbox, and never touches `user_version`. Secrets are
redacted before storage; MCP inputs keep key names only; tool output keeps its size
only. `report` ranks time sinks (Agent/Task, AskUserQuestion, ExitPlanMode and
background runs excluded) and compares tools that do the same job per
`scripts/telemetry/jobs.json`. See `docs/wiki/architecture/agent-telemetry.md`.

### zen-pgp
```
zen-pgp                    # interactive menu
zen-pgp encrypt [FILE]     # symmetric (passphrase) or asymmetric (recipient)
zen-pgp decrypt [FILE]     # auto-detects symmetric vs. key-based
```

## Conventions

- **Header doc** — each Python script carries a man-page-style banner at the top
  (NAME / SYNOPSIS / OPTIONS / DESCRIPTION). Read it first; it's the source of truth
  for flags and behaviour. Keep it in sync when changing the interface.
- **uv single-file** — declare runtime deps in the `# /// script` block, not a
  `requirements.txt` or venv.
- **gum-driven UI** — interactive scripts shell out to [`gum`](https://github.com/charmbracelet/gum);
  they fall back to headless mode when there's no TTY.
- **Shell scripts** — `bash` with `set -e`.

## Gotchas

- **gum captures stderr → invisible hang.** `gum` draws its interactive widgets
  (choose/input/file/confirm) on **stderr** and reads keystrokes from the tty.
  When shelling out and capturing the selection, pipe **stdout only** (Python:
  `subprocess.run([...], stdout=subprocess.PIPE)`, never `capture_output=True`).
  Capturing stderr hides the UI and the program looks frozen while gum silently
  waits for input. Sneaky part: a blind tmux `send-keys` test still passes with
  stderr captured (default option + Enter selects fine), so confirm the widget
  is actually visible via `capture-pane`.

## Dependencies

- `uv` — runs the Python scripts
- `go` — builds `zen-telemetry` (`./link-bin` runs `go build`)
- `gum` — interactive UI (commit, release, pgp, run)
- `fzf` — fuzzy matching in `run.sh`
- `gpg` — `zen-pgp`
- `claude` — AI commit messages
- `gitleaks` — secret scan in `zen-release` (optional via `--no-scan`)
- `git-cliff` — changelog in `zen-release` (optional via `--no-changelog`)
- `gh` — GitHub release creation in `zen-release`

## Tests

```
uv run scripts/test_commit.py
uv run scripts/test_release.py
uv run scripts/test_await.py
uv run scripts/test_gate.py
uv run scripts/test_merge.py
uv run scripts/test_board.py
uv run scripts/test_sweep.py
(cd scripts/telemetry && gofmt -l . && go vet ./... && go test ./...)
uv run scripts/lib/test_sessions.py
```

`zen-inbox` is Go: `cd inbox && go test -race ./...`.

### zen-config
```
zen-config show                 # merged settings
zen-config get bell.enabled     # one key
zen-config edit                 # shared zen.json (both machines)
zen-config edit --local         # ~/.config/zen/config.json (this machine)
```
`zen.json` at the repo root is tracked and public, so it holds no secrets. The
machine file is deep-merged on top. Env vars (`ZEN_BELL_OFF=1 …`) win over both.
Scripts read keys at runtime; shell-side keys (`bell.threshold`, `bell.ignore`,
`git.fetchIntervalMinutes`) apply to new shells.
