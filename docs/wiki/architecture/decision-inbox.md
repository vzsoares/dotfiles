---
title: Decision Inbox — zen-inbox
category: architecture
updated: 2026-11-09
related: [overview, release-tooling]
---

# Decision Inbox — zen-inbox

`scripts/inbox.py` (`zen-inbox`) is a single-file `uv` + `typer` script that collects
agent decisions in one place, so they never interrupt chief's pane. Spec-writers and
the orchestrator run `zen-inbox ask` and end their turn; the user answers from a
watcher terminal; the answers go back to the asking session in one message.

## Flow

1. **ask** — `zen-inbox ask <task> -q ... -o ... --rec <letter> [--context ...]
   [--gate]`. The asker is `--session`, else `$AGENT_MANAGER_SESSION_ID` (set in every
   agent-manager pane); its name comes from `agent-manager sessions --json`.
2. **watch** — a terminal named `decisions` in group `chief` runs `zen-inbox watch`.
   New decisions ring `zen-bell window-attention`.
3. **answer** — pick an option, free text (`gum write`), or talk: `tmux display-popup`
   attached to the agent's own tmux session `am_<session-id>`. Detaching returns to
   the watcher, which asks `resolved?`; yes closes it as `talk` with an optional note.
4. **flush** — when the session's last open decision closes (answer, resolve or
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
there); `ZEN_FLOW_DB` overrides it for tests. Schema version in `PRAGMA user_version`.
Tables `decisions` and `decision_options` use generic names because the db is meant to
hold other zen-flow state later.

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
- **Watcher redraws.** A poll thread compares a fingerprint (`id, rev` of every open,
  held or undelivered row) every 2 s and terminates the list's `gum choose` only, never
  a decision's menu. `rev` is bumped on every write, so same-second changes still flip.
- **Popup needs a client.** `display-popup` targets the watcher's tmux server (first
  field of `$TMUX`) and fails with no attached client; the watcher then prints the
  `tmux … attach -t am_<id>` command instead.
- Pipe gum's stdout only (see `scripts/README.md`).

## Tests

`uv run scripts/test_inbox.py`: temp db through `ZEN_FLOW_DB`; `agent-manager` and
`tmux` are fake scripts on PATH that record their argv. Concurrency tests spawn real
processes against one db.
