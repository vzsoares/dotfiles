---
title: Agent Rotation — zen-rotate
category: architecture
updated: 2026-11-21
related: [agent-roles, decision-inbox, overview]
---

# Agent Rotation — zen-rotate

Chief and the orchestrator run for days, and re-reading their own context was half
of all cache-read tokens (#935). `zen-rotate` (`scripts/rotate.py`) drops that
context every 6 hours without losing state: the session writes its own handoff note,
the conversation is cleared in place, and the fresh one starts from the note plus
the role preset. The note is written by the session's own model, never by a
`claude -p` summary (#925: own handoff scored 4.23 and 4.33 against 3.52 and 3.31).

It ships with both roles off (`zen.json` `rotate.roles.*.enabled: false`).

## In place, not a new session

The restart is a typed `/clear`. The agent-manager row, session id, pane, process,
env profile and role flags all stay, so everything addressed by session id keeps
working: messages agent-manager holds, workers that send to the orchestrator,
zen-inbox asks and the waiting queue. agent-manager's `SessionStart` hook (matcher
`startup|resume|clear`) records the new conversation as `agent_session_id`, which
is how the clear is confirmed.

## The cycle

`zen-resume-watch` runs `zen-rotate tick` once a minute. Per enabled role (the one
live session with that name in group `chief`):

1. **Due.** Age is now minus the first timestamp in the current transcript; due at
   `rotate.hours`. A `/clear` starts a new transcript, so the age resets itself.
2. **At rest**, all of: status `finished` or `idle`; the transcript last written
   `quiet_minutes` ago (chief 10, orchestrator 2); the pane's input box (the `❯`
   line between two `─` rules) empty and showing; `zen-inbox approval open` exits
   1; no backoff, not paused.
3. **Request.** `agent-manager send` with the note path, the required headings and
   the rules: stop every background run (TaskStop), delete every timer, list them,
   do nothing else, end with `HANDOFF_READY` or `HANDOFF_DEFER <reason>`.
4. **Ready**, on a later tick: at rest again, the last turn ends with
   `HANDOFF_READY`, the note passes `check-note`, and `pending_background()` of
   the transcript is empty. The at-rest checks run again, without the quiet time.
5. **Clear.** Write `~/.cache/zen/rotate/clearing-<session id>`, type `/clear` with
   `tmux -L agentmgr send-keys`, poll `agent-manager sessions --json` up to 20 s
   for a new `agent_session_id`, remove the marker.
6. **Resume.** `agent-manager send <id> --saved <role> "<resume>"`: read the note,
   do its First actions, then handle any message that arrived first. A failed
   resume send is retried every tick.

Not-ready paths:

| What happened | Result |
|---|---|
| `HANDOFF_DEFER <reason>` | backoff `rotate.defer_minutes` (30); not a cancel |
| no marker, an invalid note, or a background run still pending | one more request naming the gap; a second miss cancels |
| a turn ran after `HANDOFF_READY` | "update your note" request; at most 3 requests per cycle |
| no turn end within 20 minutes of the request | cancel |
| the clear is not confirmed in 20 s | cancel, the pane's last 15 lines go to `rotate.log`, backoff 60 minutes |
| the session died, or its conversation changed by other means | cancel |

A cancel sends `[zen-rotate] No restart this time (<reason>). Re-arm the waiters and
timers listed in <note> and carry on.` and backs off `defer_minutes`. Three cancels
in a row pause the role until `zen-rotate resume <role>`. Nothing forces a restart
at any age.

## The handoff note

`~/.cache/zen/handoff/<role>/<YYYYmmdd-HHMMSS>.md`, directory 0700, file set to 0600
by `check-note`; never in this repo (public) or the vault. The newest 30 per role
are kept. At most 30,000 characters (the request asks for under 12,000).

`zen-rotate check-note <path>` fails on a missing `## ` heading, an empty section
("none" is a valid body), an oversize note, or a finding from `gitleaks dir` with
`git/gitleaks.toml`. Headings inside a code fence do not count. The sections:
State, Waiters and timers, Open asks and approvals, Standing rules, Mistakes not to
repeat, Owed, Files, First actions. Chief re-reads the board instead of copying it;
the orchestrator lists its queue, live workers and pending merges and pushes.

## Commands

```
zen-rotate tick                         # one step per enabled role and cycle in flight
zen-rotate status [--json]              # session, age, due in, phase, block, last rotation/cancel/note
zen-rotate now <role>                   # start a cycle now; skips only the age check
zen-rotate now --session <id> [--saved <preset>]   # any other session; refuses a role session
zen-rotate check-note <path>
zen-rotate resume <role>                # lift a pause and any backoff
```

A cycle started with `now` is carried on by later ticks, whatever `enabled` says.
With both roles off and no cycle in flight, `tick` runs no external command.
`tick` and `now` take `~/.cache/zen/rotate/lock` (a dead owner's pid is taken
over); a second `tick` exits 0 at once.

Sends go out as `--from zen-rotate`, and again without the name when that fails:
agent-manager refuses a sender name from a caller that has a session.

## What blocks and for how long

`zen-inbox approval open [--json]` is read-only: one line per prod ask answered
`Approve` in the last 12 hours that is unused, or a used release not yet pushed;
exit 1 when none. An approved prod action therefore blocks a restart for 12 hours
after its answer; past that it only has to be in the note, so an abandoned approval
cannot block restarts for good.

`zen-wrapup hook` exits without a run when the `SessionEnd` reason is `clear` and
`clearing-$AGENT_MANAGER_SESSION_ID` exists, so a rotation adds no headless wrap-up.

## Accepted races

One user, one machine, two rotating sessions, about 8 restarts a day.

- A message delivered between the last at-rest check and the typed `/clear`
  (milliseconds): the clear is verified, a miss cancels.
- A message delivered in the 1 to 3 s between the clear and the resume lands in a
  conversation with no preset; the resume message tells the session to handle it
  next.
- The user typing in chief's pane: the empty-prompt check and the 10-minute quiet
  time.

## Live probe (2026-11-21)

Run once on a throwaway Haiku session `rotate-probe` (Claude Code 2.1.330, macOS), with
both roles off; the probe was killed and deleted afterwards.

| Check | Result |
|---|---|
| note valid, background run stopped before the clear | yes: `check-note` ok; the session stopped its `sleep 900` with TaskStop and listed it |
| `agent_session_id` changed | yes, about 2 s after the typed `/clear` |
| `claude` pid and model unchanged | yes (same pid, `claude-haiku-5-5` before and after) |
| resume message is the first user message of the new transcript | yes, after the `/clear` command record itself |
| a message sent with `agent-manager send` right after the clear | answered in the new conversation, after the resume |
| a background `sleep 60` left alive across a hand-typed `/clear` | it survives the clear, and its `<task-notification>` starts a turn in the new conversation, which has no idea what it is. This is why waiters are stopped before the clear. |

Also seen: a session spawned in an untrusted directory sits on the folder-trust
dialog with status `waiting`, and both guards refuse it (`status is waiting`; the
pane has no input box).

## Turning it on

Not part of the merge: `./link-bin` and `./agent-manager/link` in the main checkout,
restart the `resume-watch` terminal, then set `rotate.roles.orchestrator.enabled`
to `true` in `~/.config/zen/config.json`; chief after one clean orchestrator
restart. `zen-rotate now orchestrator` forces the first one while watching its pane.

## Tests

```
uv run scripts/test_rotate.py       # fake agent-manager, tmux, zen-inbox, gitleaks; pinned clock
bash scripts/test_resume_watch.sh   # tick once per pass, none during a limit wait
bash scripts/test_wrapup.sh         # the hook skips on clear plus marker
cd inbox && go test -race ./...     # approval open
```
