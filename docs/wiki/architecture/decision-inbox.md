---
title: Decision Inbox — zen-inbox
category: architecture
updated: 2026-11-10
related: [overview, release-tooling]
---

# Decision Inbox — zen-inbox

`zen-inbox` is a Go binary (`inbox/`, one binary for the CLI and a Bubble Tea panel)
that collects agent decisions in one place, so they never interrupt chief's pane. It
replaced `scripts/inbox.py` with the same commands, flags, output, exit codes and db.
Spec-writers and the orchestrator run `zen-inbox ask` and end their turn; the user
answers from a watcher terminal; the answers go back to the asking session in one
message.

## Flow

1. **ask** — `zen-inbox ask <task> -q ... -o ... --rec <letter> [--context ...]
   [--gate] [--title ... --brief ...]`. The asker is `--session`, else
   `$AGENT_MANAGER_SESSION_ID` (set in every agent-manager pane); its name comes from
   `agent-manager sessions --json`. `--title`/`--brief` (or `zen-inbox brief`) store
   the task context for that session.
2. **watch** — a terminal named `decisions` in group `chief` runs `zen-inbox watch`:
   one row per asking session (`name · #task title · n open · age · [status]`, `gate`
   when it has one). New decisions ring `zen-bell window-attention` once per session
   per poll.
3. **session screen** — per task a context block (`#task title`, the brief, `asked by
   name (group, repo) · status · age`; without a brief just `#task` and the asker),
   then the questions in ID order.
4. **answer** — Answer walks the open questions one screen each: `a`-`d` pick, `f`
   free text (a textarea, `ctrl+d` sends), `s` skip, `t` talk, `v` view full, `esc`
   back; `?` toggles help and the footer always lists the screen's keys. Answers are
   staged in memory with the decision's `rev`; a review screen offers Send / Change
   one / Cancel. The open session updates live (2 s poll): an amended question shows
   its new text, a staged answer to it goes stale and is asked again before the
   review, one answered elsewhere shows `answered elsewhere: <answer>` and leaves the
   walk, and new questions join its end. (The spec said `answered by <who>`; the db records no
   answerer and schema changes were out of scope, so it names the answer instead.) At Send the `rev` check stays the backstop:
   one closed elsewhere is reported and skipped, one amended is shown again. Talk opens `tmux display-popup` on
   the agent's session, then a multi-select resolves the decisions it settled (as
   `talk`, optional note); the rest stay open.
5. **flush** — when the session's last open decision closes (answer, resolve or
   withdraw), its held answers are sent as one message:

   ```
   [zen-inbox] Answers to your decisions:
   580.1 <question> → b: <option text>
     Note: <note>
   580.2 <question> → free text: <text>
   ```

   Talk-resolved and withdrawn decisions are left out. Questions are clipped, then
   dropped, when the message nears agent-manager's 8000-byte send limit.

## IDs

`<task>.<n>` with n = 1 + the highest n ever used for that task (withdrawn included);
gates `<task>.g`, `<task>.g2`, … in their own sequence. IDs never shift or get reused.

## Storage

`<vault>/config/zen-flow.db` (vault from `~/.claude/second-brain.json`, gitignored
there); `ZEN_FLOW_DB` overrides it for tests. Schema version in `PRAGMA user_version`,
now 2. Tables `decisions` and `decision_options` use generic names because the db is
meant to hold other zen-flow state later. v2 adds `decision_briefs (session_id, task,
title, brief, updated_at)`, keyed by session and task; `connect()` migrates inside its
`BEGIN IMMEDIATE` guard, so concurrent first opens are safe and a v1 reader keeps
working on a migrated db.

Concurrency: WAL, `busy_timeout` 5000, and every ID allocation and open → closed
transition in one `BEGIN IMMEDIATE` transaction, so two watchers can never both answer
one decision. The flush claims the held rows (`delivery = sending`) in a transaction
before sending, so concurrent closers send the batch exactly once.

`delivery`: `none` → `held` → `sending` → `sent` | `failed`. A failure (session dead,
archived or missing, non-zero send, refusal) keeps the answers with the error, exits 3,
and stays listed in the watcher until `zen-inbox retry <session>`.
`retry` also picks up a `sending` claim older than 60 s (a send that died; sends
time out at 30 s) and `held` answers whose session has nothing left open (closed but
never flushed).

## Gotchas

- **`--from` is refused inside an agent pane.** agent-manager identifies the caller
  from the process tree, not only env vars: `send --from` fails with "a sender name is
  for a caller with no session" when chief or an agent runs `answer`/`withdraw`. The
  send then retries once without `--from`, so the message shows the calling session as
  sender. A clean-env pane outside any session accepts `--from`; the unnested
  `decisions` terminal is expected to as well.
- **Self-withdraw hands answers back (#582).** When an agent withdraws its own last
  open decision (`$AGENT_MANAGER_SESSION_ID` is the decision's session), the flush
  would run in that agent's process tree, where `--from` is refused and the fallback
  messages the agent itself. So `withdraw` prints the held-answers message on stdout,
  marks them `sent` with no message id and exits 0; a `--force` withdraw by another
  session still sends. This is the one deliberate difference from `inbox.py`.
- **Watcher refresh.** The panel reads the db every 2 s and `agent-manager sessions`
  every 10 s, all inside `tea.Cmd`s, so `Update` never blocks; Bubble Tea redraws
  only what changed. Sends, popups and bells run in commands too.
- **Popup socket.** `display-popup` goes to the watcher's own tmux server (first field
  of `$TMUX`), but the inner attach always uses `env -u TMUX tmux -L agentmgr attach -t
  am_<id>`: agent sessions live on agent-manager's server, so a watcher run in the
  user's own tmux still opens the right one. With no client the popup fails and the
  watcher prints that attach command instead.
- **Screen fit.** The question, context and full option text sit above a menu of
  short items (`a`, `b  ← rec`, `Free text`, …) and a key footer. `fitWalk()` cuts in
  order: brief text, per-decision context (`… (View full)`), the asked-by line, the
  `?` help, menu height (paged), the title line. When the question and options alone
  overflow, the whole body scrolls (`pgup`/`pgdn`) instead: option text is never
  clipped. Text is wrapped by a port of Python's `textwrap`, so `list` matches
  `inbox.py` byte for byte; widths count code points, so wide (CJK, emoji) text can
  overrun a pane.
- **Build.** `inbox/zen-inbox` (linked by `link-bin`) rebuilds into `~/.cache/zen/bin`
  when a source is newer than the binary; Go from PATH or mise's shims, else exit 4.
  `modernc.org/sqlite` is pure Go, so `CGO_ENABLED=0` builds on both machines.
- **Watcher start from chief.** agent-manager refuses `terminal send` to an un-nested
  terminal from inside a session, so chief starts it with `tmux -L agentmgr send-keys
  -t am_<terminal id> 'zen-inbox watch' Enter`.
## Tests

`cd inbox && go test -race ./...`: temp db through `ZEN_FLOW_DB`, clock pinned with
`ZEN_INBOX_NOW`; `agent-manager`, `tmux` and `zen-bell` are fake scripts on PATH that
record their argv. Golden files hold `inbox.py`'s stdout, stderr, exit code and send
argv for ~150 commands, captured before it was deleted; v1 and v2 dbs it wrote are
committed as SQL dumps. Concurrency tests spawn real processes against one db. The
panel is driven through `Update` with key and window-size messages at 92x29, 60x20
and 40x15; the launcher tests rebuild a copy of the module.
