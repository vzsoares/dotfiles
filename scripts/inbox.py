#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "typer>=0.12",
# ]
# ///
#
# ==============================================================================
#
#      _       _                  z e n - i n b o x
#     (_)_ __ | |__   _____  __
#     | | '_ \| '_ \ / _ \ \/ /
#     | | | | | |_) | (_) >  <
#     |_|_| |_|_.__/ \___/_/\_\
#
# ------------------------------------------------------------------------------
#
#  NAME
#      inbox.py  --  decision inbox: agents ask, the user answers from a watcher
#
#  SYNOPSIS
#      zen-inbox ask <task> -q "<question>" [-o "<option>"]... [--rec <letter>]
#                [--context "<text>"] [--gate] [--session <id>] [--json]
#      zen-inbox amend <id> [-q ...] [-o ...]... [--rec ...] [--context ...] [--force]
#      zen-inbox withdraw <id> [--force]
#      zen-inbox list [--all] [--json]
#      zen-inbox show <id> [--json]
#      zen-inbox answer <id> (<letter> | --text "<text>") [--note "<text>"] [--json]
#      zen-inbox resolve <id> [--note "<text>"]
#      zen-inbox retry <session>
#      zen-inbox watch
#
#  DESCRIPTION
#      ask        record a decision, print its ID. Asker: --session, else
#                 $AGENT_MANAGER_SESSION_ID. Options are lettered a-d in order;
#                 0 (free text only) or 2-4. IDs are <task>.<n>, gates <task>.g,
#                 <task>.g2, ...; numbers are never reused, withdrawn included.
#      amend      change an open decision; asking session only unless --force.
#      withdraw   close an open decision unanswered; same ownership rule.
#      answer     record a pick or free text; delivery is held while the
#                 session still has an open decision.
#      resolve    close a decision settled in talk; nothing is sent for it.
#      retry      resend a session's failed delivery as one message.
#      watch      live gum view of open decisions: pick, free text or talk
#                 (tmux popup attached to am_<session>).
#
#      When a session's last open decision closes, its held answers go out as
#      one `agent-manager send <session> "<msg>" --from zen-inbox` message.
#
#  STORAGE
#      <vault>/config/zen-flow.db (vault from ~/.claude/second-brain.json);
#      $ZEN_FLOW_DB overrides the path.
#
#  EXIT CODES
#      0  ok          1  not found or wrong state     2  usage
#      3  answer recorded, delivery failed            4  vault or db unavailable
#
#  AUTHOR
#      vzsoares.
#
# ==============================================================================
"""Decision inbox: agents ask, the user answers from a live watcher."""

from __future__ import annotations

import json
import os
import re
import shlex
import sqlite3
import subprocess
import sys
import threading
import time
from dataclasses import asdict, dataclass, field
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Annotated

import typer

SCHEMA_VERSION = 1
LETTERS = "abcd"
SEND_LIMIT = 8000
SEND_MARGIN = 200
POLL_SECONDS = 2.0
SEND_TIMEOUT = 30
# A 'sending' row older than this is a crashed send, not one in flight.
STALE_SECONDS = 2 * SEND_TIMEOUT
AM_SOCKET = "agentmgr"

SCHEMA = """
CREATE TABLE decisions (
    id TEXT PRIMARY KEY,
    task TEXT NOT NULL,
    seq INTEGER NOT NULL,
    gate INTEGER NOT NULL DEFAULT 0,
    session_id TEXT NOT NULL,
    session_name TEXT NOT NULL,
    question TEXT NOT NULL,
    context TEXT NOT NULL DEFAULT '',
    rec TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    amended INTEGER NOT NULL DEFAULT 0,
    answer_kind TEXT NOT NULL DEFAULT '',
    answer_option TEXT NOT NULL DEFAULT '',
    answer_text TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    answered_at TEXT NOT NULL DEFAULT '',
    delivery TEXT NOT NULL DEFAULT 'none',
    message_id TEXT NOT NULL DEFAULT '',
    delivery_error TEXT NOT NULL DEFAULT '',
    rev INTEGER NOT NULL DEFAULT 0,
    UNIQUE (task, gate, seq)
);
CREATE TABLE decision_options (
    decision_id TEXT NOT NULL REFERENCES decisions(id),
    letter TEXT NOT NULL,
    text TEXT NOT NULL,
    PRIMARY KEY (decision_id, letter)
);
CREATE INDEX decisions_session ON decisions (session_id, status);
"""


class InboxError(Exception):
    def __init__(self, message: str, code: int) -> None:
        super().__init__(message)
        self.code = code


@dataclass
class Decision:
    id: str
    task: str
    seq: int
    gate: bool
    session_id: str
    session_name: str
    question: str
    context: str
    rec: str
    status: str
    amended: bool
    answer_kind: str
    answer_option: str
    answer_text: str
    note: str
    created_at: str
    updated_at: str
    answered_at: str
    delivery: str
    message_id: str
    delivery_error: str
    options: list[tuple[str, str]] = field(default_factory=list)

    def option_text(self, letter: str) -> str:
        return next((t for ltr, t in self.options if ltr == letter), "")

    def to_json(self) -> dict[str, object]:
        data = asdict(self)
        data["options"] = [{"letter": ltr, "text": t} for ltr, t in self.options]
        return data


@dataclass
class Delivery:
    session_id: str
    ids: list[str]
    ok: bool
    message_id: str = ""
    error: str = ""


# --------------------------------------------------------------------------- #
# Storage                                                                      #
# --------------------------------------------------------------------------- #


def now() -> str:
    return datetime.now(UTC).isoformat(timespec="seconds")


def db_path(home: Path) -> Path:
    override = os.environ.get("ZEN_FLOW_DB")
    if override:
        return Path(override).expanduser()
    brain = home / ".claude" / "second-brain.json"
    try:
        vault = json.loads(brain.read_text()).get("vault")
    except (OSError, json.JSONDecodeError) as e:
        raise InboxError(f"vault config unreadable: {brain}: {e}", 4) from e
    if not isinstance(vault, str):
        raise InboxError(f"no vault in {brain}", 4)
    config = Path(vault).expanduser() / "config"
    if not config.is_dir():
        raise InboxError(f"vault config dir missing: {config}", 4)
    return config / "zen-flow.db"


def connect(path: Path) -> sqlite3.Connection:
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        conn = sqlite3.connect(path, timeout=5, isolation_level=None)
        conn.row_factory = sqlite3.Row
        conn.execute("PRAGMA busy_timeout = 5000")
        conn.execute("PRAGMA journal_mode = WAL")
        if conn.execute("PRAGMA user_version").fetchone()[0] < SCHEMA_VERSION:
            conn.execute("BEGIN IMMEDIATE")
            if conn.execute("PRAGMA user_version").fetchone()[0] < SCHEMA_VERSION:
                _init_schema(conn)
            conn.execute("COMMIT")
    except (OSError, sqlite3.Error) as e:
        raise InboxError(f"db unavailable: {path}: {e}", 4) from e
    return conn


def _init_schema(conn: sqlite3.Connection) -> None:
    for statement in SCHEMA.split(";"):
        if statement.strip():
            conn.execute(statement)
    conn.execute(f"PRAGMA user_version = {SCHEMA_VERSION}")


class immediate:
    """BEGIN IMMEDIATE ... COMMIT, rolled back on any exception."""

    def __init__(self, conn: sqlite3.Connection) -> None:
        self.conn = conn

    def __enter__(self) -> sqlite3.Connection:
        self.conn.execute("BEGIN IMMEDIATE")
        return self.conn

    def __exit__(self, exc_type: object, exc: object, tb: object) -> None:
        self.conn.execute("ROLLBACK" if exc_type else "COMMIT")


def _decision(conn: sqlite3.Connection, row: sqlite3.Row) -> Decision:
    options = [
        (str(r["letter"]), str(r["text"]))
        for r in conn.execute(
            "SELECT letter, text FROM decision_options"
            " WHERE decision_id = ? ORDER BY letter",
            (row["id"],),
        )
    ]
    return Decision(
        id=row["id"],
        task=row["task"],
        seq=row["seq"],
        gate=bool(row["gate"]),
        session_id=row["session_id"],
        session_name=row["session_name"],
        question=row["question"],
        context=row["context"],
        rec=row["rec"],
        status=row["status"],
        amended=bool(row["amended"]),
        answer_kind=row["answer_kind"],
        answer_option=row["answer_option"],
        answer_text=row["answer_text"],
        note=row["note"],
        created_at=row["created_at"],
        updated_at=row["updated_at"],
        answered_at=row["answered_at"],
        delivery=row["delivery"],
        message_id=row["message_id"],
        delivery_error=row["delivery_error"],
        options=options,
    )


def id_key(decision_id: str) -> tuple[tuple[int, int | str], int, int]:
    task, _, suffix = decision_id.rpartition(".")
    task_key: tuple[int, int | str] = (0, int(task)) if task.isdigit() else (1, task)
    if suffix.startswith("g"):
        return task_key, 1, int(suffix[1:] or 1)
    return task_key, 0, int(suffix) if suffix.isdigit() else 0


def select(
    conn: sqlite3.Connection, where: str = "1", params: tuple[object, ...] = ()
) -> list[Decision]:
    rows = conn.execute(f"SELECT * FROM decisions WHERE {where}", params).fetchall()
    return sorted((_decision(conn, r) for r in rows), key=lambda d: id_key(d.id))


def get(conn: sqlite3.Connection, decision_id: str) -> Decision:
    found = select(conn, "id = ?", (decision_id,))
    if not found:
        raise InboxError(f"no decision {decision_id}", 1)
    return found[0]


def open_ids(conn: sqlite3.Connection, session_id: str) -> list[str]:
    return [
        d.id for d in select(conn, "session_id = ? AND status = 'open'", (session_id,))
    ]


def fingerprint(conn: sqlite3.Connection) -> tuple[tuple[str, int], ...]:
    """Changes whenever the watcher's view of the inbox would change."""
    rows = conn.execute(
        "SELECT id, rev FROM decisions WHERE status = 'open'"
        " OR delivery IN ('held', 'sending', 'failed') ORDER BY id"
    ).fetchall()
    return tuple((str(r["id"]), int(r["rev"])) for r in rows)


# --------------------------------------------------------------------------- #
# agent-manager                                                                #
# --------------------------------------------------------------------------- #


def am_sessions() -> list[dict[str, object]] | None:
    try:
        result = subprocess.run(
            ["agent-manager", "sessions", "--json"],
            capture_output=True,
            text=True,
            check=False,
            timeout=SEND_TIMEOUT,
        )
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return None
    if result.returncode != 0:
        return None
    try:
        data = json.loads(result.stdout or "[]")
    except json.JSONDecodeError:
        return None
    if not isinstance(data, list):
        return None
    return [s for s in data if isinstance(s, dict)]


def find_session(
    sessions: list[dict[str, object]] | None, session_id: str
) -> dict[str, object] | None:
    return next((s for s in sessions or [] if s.get("id") == session_id), None)


def session_status(session: dict[str, object] | None) -> str:
    if (
        session is None
        or not session.get("running")
        or session.get("archived")
        or session.get("status") in ("dead", "errored")
    ):
        return "gone"
    return str(session.get("status") or "unknown")


SENDER_REFUSED = "a sender name is for a caller with no session"


def send(session_id: str, message: str) -> tuple[bool, str, str]:
    """(ok, message_id, error) for one agent-manager send."""
    sessions = am_sessions()
    if (
        sessions is not None
        and session_status(find_session(sessions, session_id)) == "gone"
    ):
        return False, "", f"session {session_id} is gone (dead, archived or missing)"
    argv = ["agent-manager", "send", session_id, message, "--from", "zen-inbox"]

    def run(cmd: list[str]) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [*cmd, "--json"],
            capture_output=True,
            text=True,
            check=False,
            timeout=SEND_TIMEOUT,
        )

    try:
        result = run(argv)
        # Inside an agent pane --from is refused; the caller session sends instead.
        if result.returncode != 0 and SENDER_REFUSED in result.stderr:
            result = run(argv[:4])
    except FileNotFoundError:
        return False, "", "agent-manager not found"
    except subprocess.TimeoutExpired:
        return False, "", f"agent-manager send timed out after {SEND_TIMEOUT}s"
    if result.returncode != 0:
        error = (result.stderr or result.stdout).strip()
        return False, "", error or f"agent-manager send exited {result.returncode}"
    try:
        data = json.loads(result.stdout or "{}")
    except json.JSONDecodeError:
        data = {}
    if isinstance(data, dict):
        if data.get("refused") or data.get("error"):
            return False, "", str(data.get("error") or data.get("refused"))
        mid = data.get("message_id", data.get("id", ""))
        return True, str(mid), ""
    return True, "", ""


# --------------------------------------------------------------------------- #
# Message                                                                      #
# --------------------------------------------------------------------------- #

HEADER = "[zen-inbox] Answers to your decisions:"


def _clip(text: str, width: int) -> str:
    return text if len(text) <= width else text[: max(width - 1, 0)] + "…"


def answer_line(d: Decision, width: int | None = None) -> str:
    if d.answer_kind == "pick":
        answer = f"{d.answer_option}: {d.option_text(d.answer_option)}"
    else:
        answer = f"free text: {d.answer_text}"
    question = d.question if width is None else _clip(d.question, width)
    head = f"{d.id} {question}" if question else d.id
    line = f"{head} → {answer}"
    return f"{line}\n  Note: {d.note}" if d.note else line


def build_message(answers: list[Decision], limit: int = SEND_LIMIT) -> str:
    budget = limit - SEND_MARGIN
    ordered = sorted(answers, key=lambda d: id_key(d.id))
    for width in (None, 200, 120, 80, 40, 20, 0):
        msg = "\n".join([HEADER, *(answer_line(d, width) for d in ordered)])
        if len(msg.encode()) <= budget:
            return msg
    tail = "\n… truncated; see zen-inbox show <id>"
    cut = msg.encode()[: budget - len(tail.encode())].decode(errors="ignore")
    return cut + tail


# --------------------------------------------------------------------------- #
# Operations                                                                   #
# --------------------------------------------------------------------------- #

TASK_RE = re.compile(r"^[A-Za-z0-9_-]+$")


def check_options(options: list[str], rec: str) -> None:
    if len(options) == 1 or len(options) > len(LETTERS):
        raise InboxError("give 0 options (free text) or 2 to 4", 2)
    if any(not o.strip() for o in options):
        raise InboxError("options must not be empty", 2)
    if rec and rec not in LETTERS[: len(options)]:
        raise InboxError(f"--rec {rec!r} is not one of the options", 2)


def ask(
    conn: sqlite3.Connection,
    task: str,
    question: str,
    options: list[str],
    rec: str,
    context: str,
    gate: bool,
    session_id: str,
    session_name: str,
) -> Decision:
    if not TASK_RE.match(task):
        raise InboxError(f"bad task {task!r}: letters, digits, - or _", 2)
    if not question.strip():
        raise InboxError("a question is required", 2)
    check_options(options, rec)
    stamp = now()
    with immediate(conn):
        seq = (
            1
            + conn.execute(
                "SELECT COALESCE(MAX(seq), 0) FROM decisions WHERE task = ? AND gate = ?",
                (task, int(gate)),
            ).fetchone()[0]
        )
        if gate:
            decision_id = f"{task}.g" if seq == 1 else f"{task}.g{seq}"
        else:
            decision_id = f"{task}.{seq}"
        conn.execute(
            "INSERT INTO decisions (id, task, seq, gate, session_id, session_name,"
            " question, context, rec, created_at, updated_at)"
            " VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                decision_id,
                task,
                seq,
                int(gate),
                session_id,
                session_name,
                question,
                context,
                rec,
                stamp,
                stamp,
            ),
        )
        conn.executemany(
            "INSERT INTO decision_options (decision_id, letter, text) VALUES (?, ?, ?)",
            [(decision_id, LETTERS[i], o) for i, o in enumerate(options)],
        )
    return get(conn, decision_id)


def _open_owned(
    conn: sqlite3.Connection, decision_id: str, caller: str, force: bool
) -> Decision:
    d = get(conn, decision_id)
    if d.status != "open":
        raise InboxError(f"{decision_id} is {d.status}", 1)
    if not force and caller != d.session_id:
        raise InboxError(
            f"{decision_id} belongs to session {d.session_id}; --force overrides", 1
        )
    return d


def amend(
    conn: sqlite3.Connection,
    decision_id: str,
    caller: str,
    force: bool,
    question: str | None,
    options: list[str] | None,
    rec: str | None,
    context: str | None,
) -> Decision:
    if question is None and options is None and rec is None and context is None:
        raise InboxError("nothing to amend", 2)
    if question is not None and not question.strip():
        raise InboxError("a question is required", 2)
    with immediate(conn):
        d = _open_owned(conn, decision_id, caller, force)
        texts = options if options is not None else [t for _, t in d.options]
        new_rec = rec if rec is not None else d.rec
        if options is not None and rec is None and new_rec not in LETTERS[: len(texts)]:
            new_rec = ""
        check_options(texts, new_rec)
        conn.execute(
            "UPDATE decisions SET question = ?, context = ?, rec = ?, amended = 1,"
            " updated_at = ?, rev = rev + 1 WHERE id = ?",
            (
                d.question if question is None else question,
                d.context if context is None else context,
                new_rec,
                now(),
                decision_id,
            ),
        )
        if options is not None:
            conn.execute(
                "DELETE FROM decision_options WHERE decision_id = ?", (decision_id,)
            )
            conn.executemany(
                "INSERT INTO decision_options (decision_id, letter, text)"
                " VALUES (?, ?, ?)",
                [(decision_id, LETTERS[i], o) for i, o in enumerate(texts)],
            )
    return get(conn, decision_id)


def _close(
    conn: sqlite3.Connection,
    decision_id: str,
    status: str,
    kind: str = "",
    option: str = "",
    text: str = "",
    note: str = "",
    delivery: str = "none",
    caller: str | None = None,
    force: bool = True,
) -> Decision:
    stamp = now()
    with immediate(conn):
        d = _open_owned(conn, decision_id, caller or "", force)
        if kind == "pick" and not d.option_text(option):
            raise InboxError(f"{decision_id} has no option {option!r}", 2)
        conn.execute(
            "UPDATE decisions SET status = ?, answer_kind = ?, answer_option = ?,"
            " answer_text = ?, note = ?, delivery = ?, answered_at = ?,"
            " updated_at = ?, rev = rev + 1 WHERE id = ? AND status = 'open'",
            (status, kind, option, text, note, delivery, stamp, stamp, decision_id),
        )
    return d


def withdraw(
    conn: sqlite3.Connection, decision_id: str, caller: str, force: bool
) -> Delivery | None:
    d = _close(conn, decision_id, "withdrawn", caller=caller, force=force)
    return flush(conn, d.session_id)


def answer(
    conn: sqlite3.Connection,
    decision_id: str,
    letter: str | None,
    text: str | None,
    note: str = "",
) -> Delivery | None:
    if (letter is None) == (text is None):
        raise InboxError("give an option letter or --text, not both", 2)
    if text is not None and not text.strip():
        raise InboxError("--text must not be empty", 2)
    d = _close(
        conn,
        decision_id,
        "answered",
        "pick" if letter is not None else "text",
        option=letter or "",
        text=text or "",
        note=note,
        delivery="held",
    )
    return flush(conn, d.session_id)


def resolve(
    conn: sqlite3.Connection, decision_id: str, note: str = ""
) -> Delivery | None:
    d = _close(conn, decision_id, "answered", "talk", note=note)
    return flush(conn, d.session_id)


def _deliver(conn: sqlite3.Connection, session_id: str, ids: list[str]) -> Delivery:
    batch = [get(conn, i) for i in ids]
    ok, message_id, error = send(session_id, build_message(batch))
    with immediate(conn):
        conn.executemany(
            "UPDATE decisions SET delivery = ?, message_id = ?, delivery_error = ?,"
            " updated_at = ?, rev = rev + 1 WHERE id = ?",
            [("sent" if ok else "failed", message_id, error, now(), i) for i in ids],
        )
    return Delivery(session_id, ids, ok, message_id, error)


def _claim(conn: sqlite3.Connection, ids: list[str]) -> None:
    stamp = now()
    conn.executemany(
        "UPDATE decisions SET delivery = 'sending', updated_at = ?, rev = rev + 1"
        " WHERE id = ?",
        [(stamp, i) for i in ids],
    )


def flush(conn: sqlite3.Connection, session_id: str) -> Delivery | None:
    """Send a session's held answers once none of its decisions are open."""
    with immediate(conn):
        if open_ids(conn, session_id):
            return None
        ids = [
            d.id
            for d in select(conn, "session_id = ? AND delivery = 'held'", (session_id,))
        ]
        if not ids:
            return None
        _claim(conn, ids)
    return _deliver(conn, session_id, ids)


# Failed, crashed mid-send, or held with nothing left open (closed but never flushed).
UNDELIVERED = (
    "(delivery = 'failed' OR (delivery = 'sending' AND updated_at < ?)"
    " OR (delivery = 'held' AND NOT EXISTS (SELECT 1 FROM decisions o"
    " WHERE o.session_id = decisions.session_id AND o.status = 'open')))"
)


def _stale_cutoff() -> str:
    then = datetime.now(UTC) - timedelta(seconds=STALE_SECONDS)
    return then.isoformat(timespec="seconds")


def undelivered(conn: sqlite3.Connection) -> dict[str, list[Decision]]:
    out: dict[str, list[Decision]] = {}
    for d in select(conn, UNDELIVERED, (_stale_cutoff(),)):
        out.setdefault(d.session_id, []).append(d)
    return out


def retry(conn: sqlite3.Connection, session: str) -> Delivery:
    with immediate(conn):
        rows = select(
            conn,
            f"(session_id = ? OR session_name = ?) AND {UNDELIVERED}",
            (session, session, _stale_cutoff()),
        )
        if not rows:
            raise InboxError(f"no failed delivery for session {session}", 1)
        session_id = rows[0].session_id
        ids = [d.id for d in rows if d.session_id == session_id]
        _claim(conn, ids)
    return _deliver(conn, session_id, ids)


# --------------------------------------------------------------------------- #
# Rendering                                                                    #
# --------------------------------------------------------------------------- #


def age(stamp: str, at: datetime | None = None) -> str:
    try:
        then = datetime.fromisoformat(stamp)
    except ValueError:
        return "?"
    secs = int(((at or datetime.now(UTC)) - then).total_seconds())
    for unit, size in (("d", 86400), ("h", 3600), ("m", 60)):
        if secs >= size:
            return f"{secs // size}{unit}"
    return f"{max(secs, 0)}s"


def render(conn: sqlite3.Connection, sessions: list[dict[str, object]] | None) -> str:
    lines: list[str] = []
    opened = select(conn, "status = 'open'")
    tasks: dict[str, list[Decision]] = {}
    for d in sorted(opened, key=lambda d: d.created_at):
        tasks.setdefault(d.task, []).append(d)
    for task, decisions in tasks.items():
        lines.append(f"── task {task}")
        for d in decisions:
            status = session_status(find_session(sessions, d.session_id))
            mark = " (amended)" if d.amended else ""
            lines.append(
                f"  {d.id}{mark}  {d.session_name} [{status}]  {age(d.created_at)}"
            )
            lines.append(f"    {d.question}")
            if d.context:
                lines += [f"    │ {c}" for c in d.context.splitlines()]
            for letter, text in d.options:
                rec = "  ← recommended" if letter == d.rec else ""
                lines.append(f"    {letter}) {text}{rec}")
    held: dict[str, list[str]] = {}
    names: dict[str, str] = {}
    for d in select(conn, "delivery = 'held'"):
        held.setdefault(d.session_id, []).append(d.id)
        names[d.session_id] = d.session_name
    for sid, ids in held.items():
        waiting = open_ids(conn, sid)
        if waiting:
            lines.append(
                f"held · {names[sid]}: {', '.join(ids)} held until {', '.join(waiting)}"
            )
    for sid, batch in undelivered(conn).items():
        lines.append(
            f"failed · {batch[0].session_name}: {', '.join(d.id for d in batch)}"
            f" ({batch[0].delivery_error or batch[0].delivery}); zen-inbox retry {sid}"
        )
    return "\n".join(lines) if lines else "inbox empty"


# --------------------------------------------------------------------------- #
# Talk                                                                         #
# --------------------------------------------------------------------------- #


def tmux_socket(tmux_env: str | None) -> str:
    return tmux_env.split(",")[0] if tmux_env else ""


def attach_command(session_id: str, tmux_env: str | None) -> str:
    socket = tmux_socket(tmux_env)
    where = f"-S {shlex.quote(socket)}" if socket else f"-L {AM_SOCKET}"
    return f"tmux {where} attach -t am_{session_id}"


def popup_argv(session_id: str, tmux_env: str | None) -> list[str] | None:
    socket = tmux_socket(tmux_env)
    if not socket:
        return None
    inner = f"env -u TMUX {attach_command(session_id, tmux_env)}"
    return [
        "tmux",
        "-S",
        socket,
        "display-popup",
        "-E",
        "-w",
        "90%",
        "-h",
        "90%",
        inner,
    ]


def talk(session_id: str, tmux_env: str | None) -> bool:
    """Open the agent in a popup; False (and print how to attach) if impossible."""
    argv = popup_argv(session_id, tmux_env)
    if argv is not None:
        try:
            if subprocess.run(argv, check=False).returncode == 0:
                return True
        except FileNotFoundError:
            pass
    print(
        f"no tmux popup available; attach with: {attach_command(session_id, tmux_env)}"
    )
    return False


# --------------------------------------------------------------------------- #
# Watch                                                                        #
# --------------------------------------------------------------------------- #


def gum(*args: str) -> tuple[int, str]:
    # stdout only: gum draws on stderr (README gotcha).
    result = subprocess.run(
        ["gum", *args], stdout=subprocess.PIPE, text=True, check=False
    )
    return result.returncode, result.stdout.strip()


def bell(d: Decision) -> None:
    try:
        subprocess.Popen(
            [
                "zen-bell",
                "window-attention",
                "-n",
                "zen-inbox",
                "-b",
                f"{d.id} {d.question}",
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except FileNotFoundError:
        pass


class Poller(threading.Thread):
    """Kills the list's gum when the inbox changes so the loop redraws it."""

    def __init__(self, path: Path) -> None:
        super().__init__(daemon=True)
        self.path = path
        self.last: tuple[tuple[str, int], ...] = ()
        self.changed = threading.Event()
        self.lock = threading.Lock()
        self.proc: subprocess.Popen[str] | None = None

    def watch(self, proc: subprocess.Popen[str] | None) -> None:
        with self.lock:
            self.proc = proc

    def run(self) -> None:
        conn: sqlite3.Connection | None = None
        while True:
            time.sleep(POLL_SECONDS)
            try:
                conn = conn or connect(self.path)
                current = fingerprint(conn)
            except (InboxError, sqlite3.Error):
                continue
            if current == self.last:
                continue
            self.last = current
            self.changed.set()
            with self.lock:
                if self.proc is not None and self.proc.poll() is None:
                    self.proc.terminate()


def describe(result: Delivery | None, conn: sqlite3.Connection, session_id: str) -> str:
    if result is None:
        waiting = open_ids(conn, session_id)
        return f"held until {', '.join(waiting)}" if waiting else "nothing to send"
    if result.ok:
        return f"sent {', '.join(result.ids)} to {session_id}"
    return f"delivery failed: {result.error}; zen-inbox retry {session_id}"


def decision_menu(conn: sqlite3.Connection, d: Decision) -> str:
    labels = {
        f"{letter}) {text}{' (rec)' if letter == d.rec else ''}": letter
        for letter, text in d.options
    }
    header = d.question + (f"\n{d.context}" if d.context else "")
    code, choice = gum(
        "choose",
        "--header",
        f"{d.id}  {header}",
        *labels,
        "Free text",
        "Talk",
        "Back",
    )
    if code != 0 or choice in ("", "Back"):
        return ""
    if choice in labels:
        return describe(answer(conn, d.id, labels[choice], None), conn, d.session_id)
    if choice == "Free text":
        code, text = gum(
            "write",
            "--header",
            f"{d.id}  {d.question}",
            "--placeholder",
            "answer (empty cancels)",
        )
        if code != 0 or not text:
            return "cancelled"
        return describe(answer(conn, d.id, None, text), conn, d.session_id)
    if not talk(d.session_id, os.environ.get("TMUX")):
        return f"talk unavailable; attach with: {attach_command(d.session_id, os.environ.get('TMUX'))}"
    if gum("confirm", f"{d.id} resolved?")[0] != 0:
        return f"{d.id} left open"
    note = gum("input", "--placeholder", "note (optional)")[1]
    return describe(resolve(conn, d.id, note), conn, d.session_id)


def watch_loop(path: Path) -> None:
    conn = connect(path)
    poller = Poller(path)
    poller.start()
    seen: set[str] | None = None
    flash = ""
    while True:
        poller.last = fingerprint(conn)
        poller.changed.clear()
        opened = select(conn, "status = 'open'")
        if seen is not None:
            for d in opened:
                if d.id not in seen:
                    bell(d)
        seen = {d.id for d in opened} | (seen or set())
        print("\033[H\033[2J", end="")
        if flash:
            print(f"» {flash}\n")
        print(render(conn, am_sessions()) + "\n")
        items = [
            f"{d.id}  {d.session_name}  {_clip(d.question, 70)}"
            for d in sorted(opened, key=lambda d: d.created_at)
        ]
        items += [
            f"retry {sid}  {batch[0].session_name}: {len(batch)} answer(s)"
            for sid, batch in undelivered(conn).items()
        ]
        if not items:
            while not poller.changed.wait(0.5):
                pass
            continue
        proc = subprocess.Popen(
            ["gum", "choose", "--header", "decision (esc quits)", *items],
            stdout=subprocess.PIPE,
            text=True,
        )
        poller.watch(proc)
        out, _ = proc.communicate()
        poller.watch(None)
        if proc.returncode != 0:
            if poller.changed.is_set():
                continue
            return
        token = out.split()
        flash = ""
        try:
            if token and token[0] == "retry":
                flash = describe(retry(conn, token[1]), conn, token[1])
            elif token:
                flash = decision_menu(conn, get(conn, token[0]))
        except InboxError as e:
            flash = str(e)


# --------------------------------------------------------------------------- #
# CLI                                                                          #
# --------------------------------------------------------------------------- #

app = typer.Typer(add_completion=False, help=__doc__, no_args_is_help=True)


def _open() -> sqlite3.Connection:
    return connect(db_path(Path.home()))


def _caller() -> str:
    return os.environ.get("AGENT_MANAGER_SESSION_ID", "")


def _fail(e: InboxError) -> typer.Exit:
    print(f"zen-inbox: {e}", file=sys.stderr)
    return typer.Exit(e.code)


def _report(result: Delivery | None, conn: sqlite3.Connection, session_id: str) -> int:
    message = describe(result, conn, session_id)
    print(message, file=sys.stderr if result and not result.ok else sys.stdout)
    return 3 if result is not None and not result.ok else 0


@app.command("ask")
def cmd_ask(
    task: str = typer.Argument(..., help="Board task ID."),
    question: str = typer.Option(..., "-q", "--question"),
    option: Annotated[
        list[str] | None, typer.Option("-o", "--option", help="Up to 4, in order.")
    ] = None,
    rec: str = typer.Option("", "--rec", help="Recommended option letter."),
    context: str = typer.Option("", "--context"),
    gate: bool = typer.Option(False, "--gate", help="Gate ID <task>.g."),
    session: str = typer.Option("", "--session", help="Asking session id."),
    as_json: bool = typer.Option(False, "--json"),
) -> None:
    """Record a decision; print its ID."""
    session_id = session or _caller()
    if not session_id:
        raise _fail(
            InboxError("no session: set AGENT_MANAGER_SESSION_ID or --session", 2)
        )
    try:
        conn = _open()
        found = find_session(am_sessions(), session_id)
        name = str(found.get("name") or session_id) if found else session_id
        d = ask(
            conn, task, question, option or [], rec, context, gate, session_id, name
        )
    except InboxError as e:
        raise _fail(e) from e
    print(json.dumps(d.to_json()) if as_json else d.id)


@app.command("amend")
def cmd_amend(
    decision_id: str = typer.Argument(..., metavar="ID"),
    question: str | None = typer.Option(None, "-q", "--question"),
    option: Annotated[list[str] | None, typer.Option("-o", "--option")] = None,
    rec: str | None = typer.Option(None, "--rec"),
    context: str | None = typer.Option(None, "--context"),
    force: bool = typer.Option(False, "--force", help="Amend another session's."),
) -> None:
    """Change an open decision, keeping its ID."""
    try:
        d = amend(
            _open(),
            decision_id,
            _caller(),
            force,
            question,
            option or None,
            rec,
            context,
        )
    except InboxError as e:
        raise _fail(e) from e
    print(f"amended {d.id}")


@app.command("withdraw")
def cmd_withdraw(
    decision_id: str = typer.Argument(..., metavar="ID"),
    force: bool = typer.Option(False, "--force", help="Withdraw another session's."),
) -> None:
    """Close an open decision unanswered."""
    try:
        conn = _open()
        result = withdraw(conn, decision_id, _caller(), force)
        session_id = get(conn, decision_id).session_id
    except InboxError as e:
        raise _fail(e) from e
    print(f"withdrew {decision_id}")
    raise typer.Exit(_report(result, conn, session_id) if result else 0)


@app.command("list")
def cmd_list(
    show_all: bool = typer.Option(False, "--all", help="Every decision ever asked."),
    as_json: bool = typer.Option(False, "--json"),
) -> None:
    """Open decisions, held answers and failed deliveries."""
    try:
        conn = _open()
        where = (
            "1"
            if show_all
            else ("status = 'open' OR delivery IN ('held', 'sending', 'failed')")
        )
        decisions = select(conn, where)
        if as_json:
            print(json.dumps([d.to_json() for d in decisions]))
        elif show_all:
            for d in decisions:
                state = f"{d.status}/{d.delivery}" if d.delivery != "none" else d.status
                print(f"{d.id}\t{state}\t{d.session_name}\t{d.question}")
        else:
            print(render(conn, am_sessions()))
    except InboxError as e:
        raise _fail(e) from e


@app.command("show")
def cmd_show(
    decision_id: str = typer.Argument(..., metavar="ID"),
    as_json: bool = typer.Option(False, "--json"),
) -> None:
    """One decision in full."""
    try:
        d = get(_open(), decision_id)
    except InboxError as e:
        raise _fail(e) from e
    if as_json:
        print(json.dumps(d.to_json()))
        return
    print(f"{d.id}  [{d.status}]  {d.session_name} ({d.session_id})  {d.created_at}")
    print(d.question)
    if d.context:
        print(d.context)
    for letter, text in d.options:
        print(f"  {letter}) {text}{'  ← recommended' if letter == d.rec else ''}")
    if d.status == "answered":
        print(
            answer_line(d) if d.answer_kind != "talk" else f"resolved in talk {d.note}"
        )
        print(f"delivery: {d.delivery} {d.message_id} {d.delivery_error}".rstrip())


@app.command("answer")
def cmd_answer(
    decision_id: str = typer.Argument(..., metavar="ID"),
    letter: str | None = typer.Argument(None, help="Option letter."),
    text: str | None = typer.Option(None, "--text", help="Free-text answer."),
    note: str = typer.Option("", "--note"),
    as_json: bool = typer.Option(False, "--json"),
) -> None:
    """Record an answer; it is sent once the session has no open decision."""
    try:
        conn = _open()
        result = answer(conn, decision_id, letter, text, note)
        d = get(conn, decision_id)
    except InboxError as e:
        raise _fail(e) from e
    if as_json:
        print(json.dumps(d.to_json()))
        raise typer.Exit(3 if result is not None and not result.ok else 0)
    raise typer.Exit(_report(result, conn, d.session_id))


@app.command("resolve")
def cmd_resolve(
    decision_id: str = typer.Argument(..., metavar="ID"),
    note: str = typer.Option("", "--note"),
) -> None:
    """Close a decision settled in talk; nothing is sent for it."""
    try:
        conn = _open()
        result = resolve(conn, decision_id, note)
        session_id = get(conn, decision_id).session_id
    except InboxError as e:
        raise _fail(e) from e
    print(f"resolved {decision_id}")
    raise typer.Exit(_report(result, conn, session_id) if result else 0)


@app.command("retry")
def cmd_retry(session: str = typer.Argument(..., help="Session id or name.")) -> None:
    """Resend a session's failed delivery as one message."""
    try:
        conn = _open()
        result = retry(conn, session)
    except InboxError as e:
        raise _fail(e) from e
    raise typer.Exit(_report(result, conn, result.session_id))


@app.command("watch")
def cmd_watch() -> None:
    """Live view of open decisions; answer by pick, free text or talk."""
    try:
        watch_loop(db_path(Path.home()))
    except InboxError as e:
        raise _fail(e) from e
    except KeyboardInterrupt:
        print()


if __name__ == "__main__":
    app()
