# zen-inbox

Decision inbox: agents `ask`, the user answers from `zen-inbox watch` (a Bubble
Tea panel), and each session gets its answers back as one `agent-manager send`.
Usage, exit codes and flow: `scripts/README.md` and
`docs/wiki/architecture/decision-inbox.md`.

## Build

`./link-bin` links `~/.local/bin/zen-inbox` to the launcher `inbox/zen-inbox`. On
each run it rebuilds the binary into `${XDG_CACHE_HOME:-~/.cache}/zen/bin/zen-inbox`
(`CGO_ENABLED=0 go build`) when any `.go`, `go.mod` or `go.sum` here is newer than
it, then execs it; a `git pull` takes effect on the next run. Go comes from PATH,
else mise's shims; without either it exits 4. Builds go to a temp file renamed
into place, so concurrent first runs are safe. Nothing built lands in the repo.

The SQLite driver is `modernc.org/sqlite` (pure Go), so the same build works on
macOS arm64 and Manjaro amd64.

## Writing for the cockpit

Note bodies, briefs, and an ask's question, body, context and options render as
markdown in a ~92x29 pane (titles and list rows stay one plain line):

- Open with a **bold lead line**: the outcome or the call, in one sentence.
- Then short bullets, one fact each; no walls of text.
- Commands, paths, IDs and flags in `code spans`.
- Fenced blocks for anything to copy, every line 70 chars or less.
- Headings (`##`) only in long notes; tables for comparisons.
- An ask: a one-sentence question, then short options that each carry their
  consequence (`-o "Keep glamour: no new code"`). Commands, lists and anything to
  copy go in `--body` (markdown, `--body -` reads stdin, 16 KiB at most), shown
  under the question; `amend --body` replaces it and `--body ""` clears it.
- A step the user runs by hand (a Human Only step) is `ask <task> --manual -q
  "<the step>"` with its commands in `--body`: the options are fixed (Done, Later),
  Done opens a confirm that shows the step again and takes a typed `y`, and the
  asker gets `a: Done (confirmed)`. `zen-inbox answer` refuses Done on one.
- A `--body` with a command fence (untagged, or sh, bash, zsh, shell, console)
  must say who runs it, or `ask` exits 2 with `--body has a code block: add
  --manual (the user runs it) or --agent-runs (you run it after the answer); tag
  an example's fence with its language`. A fence tagged sql, json, ts, diff and
  the like is an example and passes. `--prod` asks are exempt.
  `--agent-runs` is for the asker: the panel shows "The agent runs this after you
  answer. Don't run it yourself." If the user copies a block anyway (`y`, a number)
  the panel asks "Did you run it? type y"; on `y` the answer reads `... (user ran
  it; skip your run)` and `show <id> --json` has a non-empty `confirmed_at` with
  `"kind": "agent-runs"`, so the asker skips its run.
- Every ask that runs a command wears a filled tag at the top of its view and on
  its list row: `YOU RUN THIS` (black on yellow, a `--manual` ask), `AGENT RUNS
  THIS` (black on cyan, an `--agent-runs` ask) and `PROD` (white on red). ANSI
  colours 3, 6 and 1, so they follow the terminal theme; the text stays black or
  white on the fill, which reads on light and dark themes.
- `y` then a block's number copies its source exactly; a `--prod run` ask shows
  its `--cmd` as such a block. A code line wider than the pane wraps at the pane
  edge on screen only.

## Threads

The cockpit groups everything with one task ID into a collapsible `#task` thread:
its asking sessions, visible notes (with replies), and in the thread screen
(`o` on its row; `enter` folds it) the full timeline, closed decisions and your `c` chats to chief included.
Notes without a task sit in `No task`; `zen-inbox refile <note> <task|->` or `T` on
a note moves one in or out. `c` on a thread binds the message as `[#task title] …`
and stores it in the `chats` table (schema v10). Every 15 s a pass sends what waits
for a session that was down (see Waiting) and tracks the status of the undelivered chats.
The top Inbox row is always shown (`Inbox · all clear` at zero); `enter` lists the unread notes and open decisions flat; finishing
one opens the next. Expanded threads (`threads_open`) whose task has nothing left are dropped from the state file, except from an empty snapshot.

## Waiting

A message for a session that is down is kept and sent when the session is back:

- **Down** means it can come back: no live session named `chief` for a `c` chat or
  a note reply; for answers, an asker still listed, not archived and dead. An
  archived or missing asker, several live namesakes, a refusal or a timeout stay a
  failed delivery with a manual `r`.
- Answers go to the asking session while it is live, else to the one live session
  with its name.
- `R` (panel, on a failed or waiting answer row, or in a Delivery entry) and
  `retry SESSION --to-chief` / `delivery resend ID --to-chief` send to the one live
  `chief` instead, headed as meant for the original recipient, whether it is gone,
  down or refusing. They refuse, rows untouched, unless chief is exactly one.
- The pass (`inbox.Pass`: the panel every 15 s and at start, or `zen-inbox retry
  --waiting`) lists sessions once, claims each item in its own `BEGIN IMMEDIATE`
  and sends at most one message per target: answers, then replies oldest first,
  then the oldest chat. With nothing waiting it runs no command.
- `chats.claimed_at` (schema v13) is the chat claim; a `sending` chat older than
  60 s is taken over. A chat written while others wait or are being sent queues behind them.
- Retry and pacing (schema v14, `send_log` / `send_retry`): a send agent-manager
  refuses as transient (`am.Transient`: the per-minute limit, a timeout, the
  manager not answering) stays `waiting` with `retrying (attempt n, next HH:MM:SS):
  <error>` and is retried by the pass after 15 s, 30 s, then every 60 s, for 30
  min, then shows as failed with its `r` key. A session that is gone fails at
  once. Sends to one session are held to 4 a minute (agent-manager refuses a
  sixth); a chat or answer past that waits for the window. `ZEN_INBOX_PACE=off`
  skips pacing (the golden run's frozen clock).
- Answers submitted together in the panel are recorded (`Record`) then flushed
  once per session (`FlushSession`, across tasks): one message, or the fewest
  that fit `SendLimit` unclipped (`packAnswers`). A refused group and the groups
  after it stay `waiting` and the pass sends them again together.
- A chat or reply delivered 10 minutes or more after it was written says so:
  `(written 3h ago, queued while chief was down) …` when chief being down kept
  the chat waiting, `(written 3h ago) …` when it only queued behind another chat
  (a reply: `…: (written 3h ago)`).
- The list shows a `Waiting` group and the header `◷ <name> is down · n queued`;
  after 15 minutes the header turns red and one banner is posted per outage.

## Delivery

agent-manager's `dropped` is not proof of loss: a dropped message is often in the
recipient's transcript. Schema v15 adds `delivery_log`, one row per problem,
`UNIQUE (source, source_id)` so every write is an idempotent upsert.

- **Scan** (`zen-inbox delivery scan`; the panel every 60 s, `zen-resume-watch` every
  5 min) reads agent-manager's `state.db` read-only (`mode=ro`, `query_only`,
  `ZEN_AM_STATE_DB` overrides the path) before its ~12 h of `session_inbox` rows age
  out. A missing file, table or column records nothing and reports
  `unavailable: <why>`.
- **Kinds**: `false_drop` (dropped, body in a user turn of the recipient's Claude
  transcript: logged closed, no body), `lost` (dropped and not there; or undelivered to
  an archived or deleted session), `held` (undelivered 5+ min: a dialog, not running;
  closes as `delivered_late`), `unverified` (a pi/codex recipient or no transcript),
  `refused` (zen-inbox's own transient refusals per retry chain, closed `retried` by
  the next success unless it gave up; failed chats and answers; agents' failed `send_session` calls
  from `tool_calls`, closed when the same sender sends within 10 min).
- **Transcripts** (`ZEN_CLAUDE_PROJECTS`, else `~/.claude/projects`): the files of the
  current, retired and released agent session ids, plus any jsonl in those dirs or
  the cwd's dir written after `sent_at` (a rotation clears in place). The first 200
  runes of the body, whitespace folded, must sit in a user turn with
  `[agent-manager] Message from`, stamped no earlier than a minute before `sent_at`;
  tool results never count.
- `zen-inbox delivery list [--all]`, `check <message id>` (any sender: `delivered`,
  `false drop`, `lost`, `held: <why>`, `unverified: <why>`, `unknown`, exit 1),
  `dismiss <entry>`, `resend <entry>` (recipient if live, else its one live
  namesake, through pacing; prefixed `(resent from the cockpit: <sender>'s message
  <id> of HH:MM)`; exit 3 when down); all take `--json`.
- Closed entries are pruned 30 days after `closed_at`. Bodies live only in the db.

## Layout

```
main.go             wires the CLI to the real agent-manager client and the panel
internal/inbox      store: schema v15, IDs, guarded transitions, flush, message,
                    prod-action approvals, chief's notes, threads, chats, the
                    waiting pass, the delivery log
internal/guard      refuses prod answers and a manual Done from inside an agent session (env, ps walk)
internal/am         agent-manager, tmux popup, stuck-paste follow-up, banners (terminal-notifier, osascript, zen-bell),
                    read-only state.db reader and transcript match (amtest builds their fixtures)
internal/view       text for list rows, context blocks and question screens
internal/md         glamour markdown for the panel: ANSI-16 style, boxed code, hardwrap, cache
internal/cli        cobra commands, output and exit codes
internal/tui        the watch panel (list, threads, session, review all, walk, review, talk, notes)
internal/pyfmt      Python textwrap / repr / json.dumps behaviour, for parity
testdata/golden     inbox.py's output per command, which the binary must match
testdata/panel      panel screens at fixed sizes (`go test ./internal/tui -update-panel`)
testdata/xver       v1 and v2 dbs written by inbox.py, and v4, v9, v12 and v14 dbs
                    written by those binaries (v4.sql ... v14.sql), as SQL dumps
testdata/fakes      fake agent-manager, tmux, zen-bell, terminal-notifier, osascript for tests
```

## Test

```
cd inbox
gofmt -l . && go vet ./... && go test -race ./...
```

`ZEN_NTFY_TOPIC` (with optional `ZEN_NTFY_URL`, default `https://ntfy.sh`, and
`ZEN_NTFY_CLICK`) makes the desk panel POST a counts-only alert per new decision or note;
nothing is sent over SSH (see the wiki's "Phone access").

`ZEN_FLOW_DB` points tests at a temp db and `ZEN_INBOX_NOW` pins the clock (tests
only). The golden files were captured once from `inbox.py` with
`testdata/golden/pyshim.py` before it was deleted; `withdraw` by the asking session
itself is a deliberate deviation (#582) and is not in them. Steps 147 on (waiting
answers, `retry --waiting`) were captured from the Go binary.
