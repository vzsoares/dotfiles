---
title: Decision Inbox — zen-inbox
category: architecture
updated: 2026-11-17
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
2. **watch** — a terminal named `cockpit` in group `chief` runs `zen-inbox watch`:
   one row per asking session (`name · #task title · n open · age · [status]`, `gate`
   when it has one). Each new decision (ask, gate, prod ask) or unread note posts one banner, never one
   per poll: title `zen-inbox · #<task> <task title>`, body the question's first line or the note title.
   macOS posts with `terminal-notifier` (Ping sound, click activates the terminal app
   from `$__CFBundleIdentifier`) and falls back to `osascript` when it exits non-zero,
   e.g. notifications are off for it (exit 3; `terminal-notifier -diagnose`); Linux
   goes through `zen-bell window-attention -n`.
3. **session screen** — per task a context block (`#task title`, the brief, `asked by
   name (group, repo) · status · age`; without a brief just `#task` and the asker),
   then the questions in ID order.
4. **answer** — Answer walks the open questions one screen each: `a`-`d` pick, `f`
   free text (a textarea, `ctrl+d` sends, `esc` keeps the text as a draft), `s` skip, `t` talk, `v` view full, `esc`
   the session's decision list; `?` toggles help and the footer always lists the screen's keys. Answers are
   staged with the decision's `rev` and kept as drafts (see Drafts under Panel); a
   review screen offers Send / Change one / Cancel. The open session updates live (2 s poll): an amended question shows
   its new text, a staged answer to it goes stale and is asked again before the
   review, one answered elsewhere shows `answered elsewhere: <answer>` and leaves the
   walk (the db records no answerer, so it names the answer), and new questions join
   its end. At Send the `rev` check stays the backstop: one closed elsewhere is
   reported and skipped, one amended is shown again. Talk opens `tmux display-popup`
   on the agent's session, then a multi-select resolves the decisions it settled (as
   `talk`, optional note); the rest stay open. A talk returns to where it started
   (the walk, the session menu, or Review all); the popup is checked before the agent is told.
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
6. **delivery check** — see Stuck paste below.

## Panel

- **Drafts (#711).** Every staged answer (pick, free text with its text, skip) is
  written to `decision_drafts` as it is staged, so esc, the list, a talk, a restart
  or a crash keep it. Writes run in a `tea.Cmd`, one batch at a time so they land in
  order; `m.drafts` mirrors the table, and a poll that started before a local change
  (`draftGen`) never overwrites it. Answer resumes at the first question with no
  draft, or the review when all have one. The store drops a draft when its decision
  closes (`closeOne`); an amend keeps it behind the decision's `rev`, and the panel
  shows it as `changed: review` and asks that question again before the review, so
  it is never sent unconfirmed (#838). A send clears the session's drafts. An `Approve` on a `--prod`
  ask is never written (store and panel both refuse), so after a restart it is asked
  again with its y/N confirm. Cancel and esc keep drafts. Esc on free text keeps
  non-blank typed text as that question's draft (blank stages nothing). `q`, esc on
  the list and `ctrl+c` quit only after the queued writes land; a second `ctrl+c`
  quits at once.
- **Decision list (#838).** Esc in the walk (and `l`) opens the session's decision
  list, `<name> · decisions · k/n drafted`: every open question with `○` none, `●`
  pick or free text, `–` skipped, and `· changed: review` on a draft amended since.
  Decisions a pass showed that closed since (withdrawn, answered elsewhere, talk) sit
  below, dimmed, `× id  question · state`; that memory is per cockpit run, not kept
  across a restart. `enter` opens a question on its draft (cursor on the pick, free
  text prefilled, `draft: …` under the question); esc there returns to the list with
  the cursor on it; esc on the list goes to the cockpit list (from the review's Change
  one, back to the review). Each session row shows `k/n drafted` after `n open`, and
  Review all shows each draft.
- **Moving in the walk.** `←` or `shift+tab` shows the previous question of the pass
  (from the review, the last one) with the cursor on its drafted option, and free
  text reopens with its text; `→` goes forward without changing anything (on the
  last question it opens the review once every question has a draft, else stays).
  After answering, the walk goes on at the first question with no draft. The walk
  footer drops `t talk · v full` (still menu items) when it would take more than two
  rows.
- **Busy loader.** A send, retry, resolve or talk runs in a `tea.Cmd`; while it runs
  the panel shows a spinner screen (`m.busy`, MiniDot spinner) instead of freezing,
  and polls keep running.
- **Cockpit layout (#785).** Every screen has a header row on top and its key footer
  pinned to the last rows. The header is `cockpit · » <busy text> ⠋` while busy,
  `cockpit · ✕ delivery failed · name · r retry` on a failed delivery, the chief
  message status, else counts: `cockpit · n open · k prod · ✉ u unread · h held`
  (`all clear` when none). The list shows labelled groups, most urgent first:
  `Failed delivery` (retry rows with the reason), `Threads` (one per task, see
  Threads below), `No task` (notes without a task), then `Held` (answers waiting for the session's
  open asks; not selectable). With nothing listed, `Nothing waiting for you` and
  `last item closed <age> ago` (latest decision close or note read) sit centred.
  The list footer is one row of keys for the row under the cursor (`enter open` /
  `a answer` on a session, `enter retry` on a failure, `enter open · m read|unread ·
  f reply` on a note) plus `r retry`, `c chief`, `M all read`, `? help`, `q quit`;
  when the row is too narrow it drops whole keys by rank (↑↓ first, the focused key
  last), never a cut word. Other screens wrap whole keys onto two rows. Keys are
  bold, labels plain. A success flash (`» sent …`) tops the list for a few seconds;
  polls clear it.
- **Code blocks (#864).** Fenced blocks render on a tinted background with no
  border characters beside the code (a drag-select copies only code; the context
  `│ ` gutter is dropped on those lines). Each has a `[n] copy` header, numbered in
  screen order on notes, the walk and View full. `y` then `1`-`9` copies that block's
  raw source by OSC 52 (plus `pbcopy` on a local Mac) and toasts `copied n`; another
  key cancels, an out-of-range number does nothing. No click-to-copy: the panel
  already captures the mouse, and a click target would not survive a drag.
- **Claude quota (#790).** Each poll reads `~/.cache/zen/claude-quota.json`, which
  the clawd-status statusline writes from Claude Code's `rate_limits`; the header
  shows it on the right: `5h 42% · resets 15:20  ·  week 63% · resets Fri` (local
  time), yellow from 80 %, red from 95 %, dimmed with `· <age> old` past 15 min,
  `quota n/a` when the file is missing or unreadable. A long status gets `5h 42%  ·
  week 63%`, and a narrower header drops the quota.
- **`c` message chief.** On the list, `c` opens a multi-line compose box sized to the
  pane (crush-style): `ctrl+d` sends (as in free text), `enter` adds a line, `esc`
  cancels and keeps the draft for the next `c`. Sending runs `zen-chief --json`
  (`scripts/chief.py`, the same script shells use), which finds the one live session
  named `chief` and queues the message with `agent-manager send --from zenha`. The
  status row then shows `✉ chief · message <id> queued`, polled with `zen-chief
  --status` until `delivered` (or `dropped`/`answered`), and clears 6 s later. A send
  error (no chief, several) stays in the box with the draft. `message-status` is
  scoped to the sender: if agent-manager cannot report a message sent with `--from
  zenha` (not verifiable from inside an agent session, which refuses `--from`), the
  third empty poll shows `sent` rather than `delivered`.
- **`r` retry.** On the list, `r` retries the first failed delivery; `enter` on a
  `retry <session>: n answer(s)` row retries that one. It runs `zen-inbox retry`'s
  path, including the delivery check.
- **Markdown (#611).** Question, context, option and brief text render through
  glamour (`internal/md`) with a cut-down style on ANSI-16 colour indexes, so the
  terminal's own palette applies; no margins, and under `NO_COLOR` (Ascii profile)
  no escape bytes at all. Lines are hard-wrapped (`ansi.Hardwrap`, escapes carried
  across breaks) so none exceeds the pane. Renders are cached per decision id, `rev`,
  field and width, and the cache is bounded.
- **Scrolling on every screen.** When a body outruns the pane it scrolls by half a
  page: `pgup`/`pgdn` everywhere, `ctrl+u`/`ctrl+b` and `ctrl+d`/`ctrl+f` except in
  the text inputs, `j`/`k` or the arrows one line on View full, and the mouse wheel
  (3 lines a notch). Elsewhere `j`/`k` move the menu cursor. The walk keeps its
  scroll position across View full.

## Prod-action asks (#680)

`zen-inbox ask <task> --prod <action> --repo R --branch B --sha S [--context …]
[--title … --brief …]` files an approval for one prod action. zen-inbox writes the
question `<action> <repo> <branch>@<sha12>?` and the options `Approve`/`Hold` with
rec `a`; `--prod` refuses `-q`, `-o`, `--rec` and `--gate`, needs all three
bindings, and `--sha` must be 40 lowercase hex (exit 2). The bound fields live in
`prod_actions`, so tools match on them, never on the question text. Amend is
refused (withdraw and re-ask), so the bindings cannot drift from the question.

`zen-inbox approval use <id> --action A --repo R --branch B --sha S [--by who]`
checks and consumes it in one `BEGIN IMMEDIATE` transaction; `approval check` does
the same matching without consuming. Valid means answered, kind `pick`, option text
exactly `Approve`, unused, and the same action, repo, branch and sha; talk-resolved
and free-text answers never count. Exit 0 valid or consumed, 1 rejected with a
one-line reason on stderr, 2 usage, 4 db. `--by` defaults to
`$AGENT_MANAGER_SESSION_ID`, else `user`. zen-release is the consumer; see
[[release-tooling]].

`approval use|check --match --action A --repo R --branch B --sha S` takes no id
(an id together with `--match`, or neither, is exit 2). In one `BEGIN IMMEDIATE`
it picks the oldest decision whose `prod_actions` row matches all four fields and
that is answered `pick` = `Approve` and unused; `use` consumes it, `check` never
does. None → exit 1, `no approved unused ask for <action> <repo> <branch>@<sha12>`.
Concurrent `use --match` calls on one approval: exactly one wins.

`approval pending --repo R --branch B` is read-only: one `<id> <approved sha>` line
per release ask answered `Approve`, used and not yet pushed, oldest first; exit 1
when none. `approval push <id> --repo R --branch B --sha S [--by who]` records S as
that release's one push (`pushed_sha`, `pushed_at`, `pushed_by`, default
`$AGENT_MANAGER_SESSION_ID` else `user`) in one `BEGIN IMMEDIATE`; not answered
Approve, not used, already pushed, or a repo/branch mismatch is exit 1. The pre-push
hook checks the pushed content against the approved sha before calling it.

`zen-inbox ask <task> --prod run --repo R --cmd "<exact command>"` (#736) files an
approval for a script or migration run against prod. It binds the repo (the main
checkout folder name) and the command string, no branch or sha; `--cmd` must be one
line of printable UTF-8 (no control, format or line-separator characters, no space but
ASCII space), as must `--repo`, so the panel shows exactly what runs. The question is `run in <repo>: <cmd>?` and the y/N confirm prints the
whole command, wrapped. `approval use|check <id> --action run --repo R --cmd C`
(or `--match`) consumes it only when the command matches byte for byte, once.
`--cmd` with any other action, or `--branch`/`--sha` with `run`, is exit 2.

Both serve the global `git/hooks/pre-push` gate (#703, #788): an agent, force or
delete push to `prod`/`main`/`master` runs `check --match` for every gated ref, then
`use --match` for each. zen-release's own push passes once on its used approval via
`approval pending` and `approval push`; no file in the repo grants anything. See
[[release-tooling]].

### Only the user answers one (#691)

- **CLI refusal.** The store refuses every close as `answered` (`answer` pick or
  text, `resolve`, `CloseRaw`) of a decision with a `prod_actions` row, inside
  `closeOne`'s `BEGIN IMMEDIATE`, unless `Inbox.User` is set: exit 1, `<id> is a
  prod-action ask; answer it in the decisions panel`, row left `open`. `withdraw`
  is unchanged (the asker's own, or `--force`). Chief can no longer record a chat
  "Approve" with `answer`.
- **The bit.** `Inbox.User` is an in-process field; only `tui.Watch` sets it, from
  `guard.Current()` at panel start. No flag, env var or file reaches it.
- **Guard (`internal/guard`).** Refuses, with the reason, when any of
  `AGENT_MANAGER_SESSION_ID`, `AGENT_MANAGER_AGENT_PID`, `CLAUDECODE` is non-empty,
  or when walking parents with `ps -o ppid=,comm= -p <pid>` finds a `claude` or
  `codex` comm. A `ps` error or a walk past 256
  levels refuses (fail closed). Gemini runs as `node`, so only the env check
  catches it.
- **Refused panel.** Everything else works; a prod ask shows `prod approvals only
  from the cockpit terminal (<reason>)`, its menu is Skip / Talk / View full /
  Back, `a`-`d` and `f` do nothing, and Talk's resolve list leaves it out.
- **Confirm.** In a passing panel, Approve (`a` or enter) opens a screen with
  action, repo, branch and the full 40-char sha, and `Approve? y/N`. Only `y`
  stages the pick for the normal review → Send; any other key stages nothing and
  returns to the question. Hold, free text and talk need no confirm.
- **Out of scope.** Deliberate bypasses: `tmux send-keys` into the cockpit pane,
  `tmux new-window 'zen-inbox watch'` (escapes the agent ancestry), raw `sqlite3`
  writes, editing the source the launcher rebuilds. The cockpit terminal must
  not inherit an agent env var from the agentmgr tmux server's environment, or the
  guard refuses there too.

## Notes (#707)

A second channel from chief to the user: a note is a short message that is not a
decision (a task finished, something very important). `zen-inbox note [<task>]
--title "<one line>" [--body <markdown> | --body -]` prints `n<id>`; the sender must
resolve to the session named `chief` (else exit 1: a guard against mistakes, not a
security boundary). Titles are one line of at most 120 characters, bodies at most
16 KiB. IDs are a global `AUTOINCREMENT`, never reused, separate from decision IDs.

- **Visible** means unread, read within 24 h, or with a reply in flight or failed;
  `zen-inbox notes [--all] [--json]` lists them newest first. Each insert prunes notes
  read over 30 days ago. `zen-inbox read <id>... | --all [--unread]` marks them.
- **Panel.** Notes with a task sit in that task's thread (Threads below); the rest
  under a `No task · n unread` header, one
  row per note (`● title · sender · #task · age`, bold unread, dim `○` read, `↩` once
  replied). Enter opens the note screen (header, title, glamour body cached per note
  id and width, then each reply in order as `↩ [n · ]age: text`), which scrolls like View full. `m` toggles read on the
  list row or the note, `M` marks every visible note read; opening never marks read.
  Notes bind none of `←`, `→`, `shift+tab`, `l`.
- **Fold (#801).** `z` on a `No task` note row collapses the group to one selectable header,
  `No task · n unread` (yellow) or `No task · all read` (dim); `enter` or `z` on it expands.
  New notes still raise the count and the banner. The state persists in
  `~/.cache/zen/inbox-panel.json` (`notes_folded`), not the db, so there is no migration.
- **Reply.** `f` opens the textarea; `ctrl+d` sends a reply, as many per note as you like, through the
  answer send path (`agent-manager send … --from zen-inbox`, the refusal fallback, the
  30 s timeout and the stuck-paste follow-up) as `[zen-inbox] Reply to your note n12
  (#707 <title>):` (`Reply 2 to …` from the second on) plus the text, cut at a UTF-8 boundary under the send limit. Schema v9 keeps every reply in `note_replies`; `notes.reply` and the delivery columns
  track the latest. A new reply waits while the latest is `sending` or `failed` (retry
  it first). The claim runs in one `BEGIN IMMEDIATE`, so two panels never overwrite
  each other's delivery state. The target is the one live session named like the sender (as `zen-chief`
  does), falling back to the stored id only while that session is alive; none or
  several live fail the reply. The send stores `message_id` first, then marks `sent`; a
  retry that finds a `message_id` asks `agent-manager message-status` and marks `sent`
  without resending unless the status is missing or `dropped`. Success marks the note read; a failure keeps the reply and its error, shows
  a `retry reply n12` row and the `✕ delivery failed` status, and `r` / `zen-inbox
  retry <session>` resend it (answers first; a `sending` claim whose `claimed_at` is older than 60 s counts).
- **Header and bell.** Unread notes count in the header (`✉ k unread`). Notes never
  ring zen-bell and refresh on the same 2 s poll.

## Threads (#861)

Everything about one task ID is one thread: its asking sessions, its visible notes
with their replies, and in the thread screen also its closed decisions and chats.

- **Key.** The task ID only; no board lookup, no merging of related tasks. A session
  asking for several tasks sits under its oldest open decision's task. The title is
  the task's newest `decision_briefs.title`.
- **List.** One `Threads · n` group. A thread with one row shows it flat; otherwise a
  header `▸ #task · PROD · n open · k unread · m items · age · title` (only the title
  is cut), bold with open asks or unread notes, red with a prod ask. Order: prod ask,
  open ask, unread notes, newest activity. Collapsed by default; `z`/`→`/`←` toggle,
  expand, collapse (`←` on a child collapses its parent). Expanded children are the
  usual `s:`/`n:` rows indented, notes without `#task`, so their keys are unchanged.
  `enter` on a header opens the thread, `a` answers its first session (prod first),
  `m` marks its notes read. Expanded tasks persist as `threads_open` in
  `~/.cache/zen/inbox-panel.json`; a poll keeps the cursor on its row, or on the
  thread a row folded into.
- **Thread screen.** `#task title` with `n open · k unread · m entries`, the brief
  (dropped first when rows run short), then the timeline oldest first (created time,
  then decisions, notes, chats, then ID): notes with replies, decisions with their state
  (`open`, `→ b: …`, `free text: …`, `held`, `talk`, dim `withdrawn`), chats as
  `✉ you → chief · age · status`. It opens at the first unread note or open ask, else
  at the end, and marks nothing read. `tab`/`shift+tab` move the `›` focus; `enter` opens
  the note (esc back to the thread) or walks the open ask (esc → decision list →
  thread); `f` replies to the focused note, else the newest; `m`, `a`, `c`, `T`, `g`/`G`.
  The panel reads `Thread(task)` only while a thread is open.
- **Re-file.** `zen-inbox refile <note> <task|->` (prints `n12 → #860`; `-` clears; a
  task that is not digits exits 2) or `T` on any note, a one-line input. Last write wins.
- **Chats.** `c` on a thread row, child or thread screen sends `[#task title] text`
  through `zen-chief`; every `c` send is stored in `chats` (task blank elsewhere) before
  the send, then gets the message id and `queued`, or `failed` with the error; status
  polls mirror `delivered`/`dropped`/`answered`/`sent` into the row.

## Phone access (#830)

The phone works the cockpit through a second panel, never the desk pane:

```
phone (Blink/Termius) → Tailscale → sshd (key-only) → zsh → zen-cockpit → zen-inbox watch
```

- **Quiet panel.** `zen-cockpit` (`scripts/cockpit.sh`) refuses to run without
  `SSH_CONNECTION` and keeps `TERM` as the client set it. With it set, `watch`
  posts no banner, rings no zen-bell and sends no ntfy push (`am.Remote`): those
  belong to the desk cockpit. Everything else is unchanged.
- **Prod asks.** The phone may approve them: the guard checks env vars and the
  process tree for an agent, and an SSH shell has neither, so the y/N confirm
  works. `guard` is untouched. The trust is the SSH key.
- **Layout.** Goldens at 44x24 (`phone-*-44x24`) cover the list, the walk, the
  review, the decision list and the prod confirm (full 40-char sha wrapped).
- **Alert.** With `ZEN_NTFY_TOPIC` set, the desk panel POSTs once per new decision
  or unread note, next to the banner, to `${ZEN_NTFY_URL:-https://ntfy.sh}/$topic`.
  The body is counts only: `#<task> · <n> open` (`· PROD` for a prod ask) or
  `✉ <n> unread`; never question, option, brief or note text. `ZEN_NTFY_CLICK`
  sets the optional `Click` header. 5 s timeout, runs in a `tea.Cmd`, a failure
  goes to stderr and is not retried.
- **Security posture.** Tailscale: private tailnet, optional ACL so only the phone
  reaches port 22. sshd: Remote Login for the user only, key-only. Key: Secure
  Enclave, so it cannot leave the phone. ntfy.sh: the topic name is the only
  secret and the body carries no content, so a leaked topic reveals only counts.
- **Limit.** The Mac must be awake; nothing here wakes it. Start the desk tmux
  server from a local terminal: one started over SSH leaks `SSH_CONNECTION` into
  every pane and silences the desk cockpit too.

Setup checklist (this Mac and the phone only):

1. Install Tailscale on both, one tailnet; optionally an ACL: phone → Mac:22.
2. Turn on Remote Login for the user only; in `/etc/ssh/sshd_config.d/` set
   `PasswordAuthentication no` and `KbdInteractiveAuthentication no` (sudo).
3. In Blink or Termius create a Secure Enclave key; add its public key to
   `~/.ssh/authorized_keys`.
4. Pick a long random topic; `export ZEN_NTFY_TOPIC=…` in `~/.zshrc.local`;
   subscribe in the ntfy app; restart the cockpit.
5. On cellular with Wi-Fi off: connect, run `zen-cockpit`, answer a test ask,
   approve a test prod ask.

## IDs

`<task>.<n>` with n = 1 + the highest n ever used for that task (withdrawn included);
gates `<task>.g`, `<task>.g2`, … in their own sequence. IDs never shift or get reused.

## Storage

`<vault>/config/zen-flow.db` (vault from `~/.claude/second-brain.json`, gitignored
there); `ZEN_FLOW_DB` overrides it for tests. Schema version in `PRAGMA user_version`,
now 10. Tables `decisions` and `decision_options` use generic names because the db is
meant to hold other zen-flow state later. v2 adds `decision_briefs (session_id, task,
title, brief, updated_at)`, keyed by session and task; `connect()` migrates inside its
`BEGIN IMMEDIATE` guard, so concurrent first opens are safe and a v1 reader keeps
working on a migrated db. v3 adds `prod_actions (decision_id, action, repo, branch,
sha, used_at, used_by)` and changes no existing table, so a v2 reader keeps working
on a v3 db. v4 adds `decision_drafts (decision_id, kind, option, text, rev,
updated_at)`, the panel's unsent answers, again without touching existing tables.
v5 adds `notes` (below), also leaving existing tables alone. v6 adds `prod_actions.cmd`
(default `''`), a run's exact command; a v5 reader still works on a v6 db (it names its
columns) and can never consume a run ask, since its checks need a branch and sha.
v7 adds `notes.claimed_at`: `replied_at` is the user's reply time, written once; a send
claim and every retry set `claimed_at`, which the 60 s staleness check reads. The
migration copies `replied_at` into `claimed_at` for rows with a reply.
v8 adds `prod_actions.pushed_sha`, `pushed_at` and `pushed_by` (default `''`), the one
push a used release approval let through; the migration stamps `pushed_at = used_at`,
`pushed_by = 'schema v8'` on every used row, so no approval used before it becomes
pushable. A v7 reader still works on a v8 db and never touches the new columns.
v9 adds `note_replies` (every reply to a note). v10 adds `chats (id, task, text,
sent_at, message_id, status, error)` with index `chats_task (task, id)`, the panel's
messages to chief; no existing table changes, so a v9 reader keeps working.

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

### Stuck paste (#610)

Claude Code collapses a long multi-line message into `[Pasted text #1 +12 lines]`,
and the Enter agent-manager types right after can be swallowed, leaving the answers
unsent while `send` reported ok. After a successful `send`, `am.Exec.Followup` checks
the asker's pane in the same command (the CLI blocks for it; the panel runs it inside
the send's `tea.Cmd`), outside the inbox mutex:

- Only for sessions whose `tool` is `claude` and that are not gone. It captures
  `tmux -L agentmgr capture-pane -p -t am_<sid>` every 1 s for up to 15 s and stops
  early when the session is `working` or gone, the prompt clears, or a capture fails.
- Stuck means the `❯` input line holds only placeholders (`StuckPaste`) and sits
  between two ─ rules; typed text, dialogs (`❯ 1. Yes`), an empty prompt or
  placeholders in scrollback never match.
- After two consecutive matching captures it presses `Enter` once. A fresh capture
  then says `submitted` (clear) or `still stuck` (zen-bell rings once; no second
  press).
- It never touches delivery state: rows stay `sent`. `Describe` adds the suffix
  `(submitted a stuck paste)` or `; the paste is still stuck, attach: tmux -L
  agentmgr attach -t am_<sid>`. A message agent-manager held because the session was
  busy is not covered.
- Timing is `am.Interval` / `am.Window`, so tests run in milliseconds against the
  fake tmux (scripted `capture-pane` screens, recorded `send-keys`).

## Gotchas

- **`--from` is refused inside an agent pane.** agent-manager identifies the caller
  from the process tree, not only env vars: `send --from` fails with "a sender name is
  for a caller with no session" when chief or an agent runs `answer`/`withdraw`. The
  send then retries once without `--from`, so the message shows the calling session as
  sender. A clean-env pane outside any session accepts `--from`; the unnested
  `cockpit` terminal is expected to as well.
- **Self-withdraw hands answers back (#582).** When an agent withdraws its own last
  open decision (`$AGENT_MANAGER_SESSION_ID` is the decision's session), the flush
  would run in that agent's process tree, where `--from` is refused and the fallback
  messages the agent itself. So `withdraw` prints the held-answers message on stdout,
  marks them `sent` with no message id and exits 0; a `--force` withdraw by another
  session still sends. This is one deliberate difference from `inbox.py`.
- **Answering inside the asking session sends nothing.** `Inbox.Self` is the CLI's
  `$AGENT_MANAGER_SESSION_ID`; any delivery to that session (answer, resolve, retry,
  withdraw) is handed back instead of sent, because agent-manager refuses a session
  messaging itself. The rows close as `sent` and `answer` prints `recorded <ids>;
  nothing sent to your own session`.
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
