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

## Layout

```
main.go             wires the CLI to the real agent-manager client and the panel
internal/inbox      store: schema v8, IDs, guarded transitions, flush, message,
                    prod-action approvals, chief's notes
internal/guard      refuses prod answers from inside an agent session (env, ps walk)
internal/am         agent-manager, tmux popup, stuck-paste follow-up, banners (terminal-notifier, osascript, zen-bell)
internal/view       text for list rows, context blocks and question screens
internal/md         glamour markdown for the panel: ANSI-16 style, hardwrap, cache
internal/cli        cobra commands, output and exit codes
internal/tui        the watch panel (list, session, walk, review, talk, notes)
internal/pyfmt      Python textwrap / repr / json.dumps behaviour, for parity
testdata/golden     inbox.py's output per command, which the binary must match
testdata/xver       v1 and v2 dbs written by inbox.py, and a v4 db written by the
                    v4 binary (v4.sql), as SQL dumps
testdata/fakes      fake agent-manager, tmux, zen-bell, terminal-notifier, osascript for tests
```

## Test

```
cd inbox
gofmt -l . && go vet ./... && go test -race ./...
```

`ZEN_FLOW_DB` points tests at a temp db and `ZEN_INBOX_NOW` pins the clock (tests
only). The golden files were captured once from `inbox.py` with
`testdata/golden/pyshim.py` before it was deleted; `withdraw` by the asking session
itself is a deliberate deviation (#582) and is not in them.
