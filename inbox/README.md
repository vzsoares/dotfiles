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

Note bodies, briefs, and an ask's question, context and options render as
markdown in a ~92x29 pane (titles and list rows stay one plain line):

- Open with a **bold lead line**: the outcome or the call, in one sentence.
- Then short bullets, one fact each; no walls of text.
- Commands, paths, IDs and flags in `code spans`.
- Fenced blocks for anything to copy, every line 70 chars or less.
- Headings (`##`) only in long notes; tables for comparisons.
- An ask: a one-sentence question, then short options that each carry their
  consequence (`-o "Keep glamour: no new code"`).

## Threads

The cockpit groups everything with one task ID into a collapsible `#task` thread:
its asking sessions, visible notes (with replies), and in the thread screen
(`o` on its row; `enter` folds it) the full timeline, closed decisions and your `c` chats to chief included.
Notes without a task sit in `No task`; `zen-inbox refile <note> <task|->` or `T` on
a note moves one in or out. `c` on a thread binds the message as `[#task title] …`
and stores it in the `chats` table (schema v10). Every poll sends a chat
that never got a message id and tracks the status of all undelivered ones, in order.
The top Inbox row (`enter`) lists the unread notes and open decisions flat; finishing
one opens the next. Expanded threads (`threads_open`) whose task has nothing left are dropped from the state file.

## Layout

```
main.go             wires the CLI to the real agent-manager client and the panel
internal/inbox      store: schema v10, IDs, guarded transitions, flush, message,
                    prod-action approvals, chief's notes, threads, chats
internal/guard      refuses prod answers from inside an agent session (env, ps walk)
internal/am         agent-manager, tmux popup, stuck-paste follow-up, banners (terminal-notifier, osascript, zen-bell)
internal/view       text for list rows, context blocks and question screens
internal/md         glamour markdown for the panel: ANSI-16 style, boxed code, hardwrap, cache
internal/cli        cobra commands, output and exit codes
internal/tui        the watch panel (list, threads, session, review all, walk, review, talk, notes)
internal/pyfmt      Python textwrap / repr / json.dumps behaviour, for parity
testdata/golden     inbox.py's output per command, which the binary must match
testdata/xver       v1 and v2 dbs written by inbox.py, and v4 and v9 dbs written by
                    those binaries (v4.sql, v9.sql), as SQL dumps
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
itself is a deliberate deviation (#582) and is not in them.
