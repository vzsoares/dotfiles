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
#                [--context "<text>"] [--gate] [--title "<task title>"]
#                [--brief "<text>"] [--session <id>] [--json]
#      zen-inbox brief <task> [--title "<text>"] [--text "<text>"] [--session <id>]
#                [--json]
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
#                 --title/--brief upsert the session+task context; empty keeps it.
#      brief      set or replace that context alone (given flags only).
#      amend      change an open decision; asking session only unless --force.
#      withdraw   close an open decision unanswered; same ownership rule.
#      answer     record a pick or free text; delivery is held while the
#                 session still has an open decision.
#      resolve    close a decision settled in talk; nothing is sent for it.
#      retry      resend a session's failed delivery as one message.
#      watch      live gum view, one row per asking session. A session shows its
#                 task context, then its questions; Answer walks them (pick, free
#                 text, skip), then Send records them. Talk opens a tmux popup on
#                 `tmux -L agentmgr attach -t am_<session>`, then asks which
#                 decisions it settled.
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
import shutil
import sqlite3
import subprocess
import sys
import textwrap
import threading
import time
from dataclasses import asdict, dataclass, field
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Annotated, Protocol

import typer

SCHEMA_VERSION = 2
LETTERS = "abcd"
SEND_LIMIT = 8000
SEND_MARGIN = 200
POLL_SECONDS = 2.0
SESSIONS_SECONDS = 10.0
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

BRIEFS = """
CREATE TABLE decision_briefs (
    session_id TEXT NOT NULL,
    task TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    brief TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (session_id, task)
);
"""

MIGRATIONS = {1: SCHEMA, 2: BRIEFS}


class InboxError(Exception):
    def __init__(self, message: str, code: int) -> None:
        super().__init__(message)
        self.code = code


class Amended(InboxError):
    def __init__(self, decision_id: str) -> None:
        super().__init__(f"{decision_id} was amended", 1)


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
    rev: int = 0
    title: str = ""
    brief: str = ""

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
            version = conn.execute("PRAGMA user_version").fetchone()[0]
            if version < SCHEMA_VERSION:
                _migrate(conn, version)
            conn.execute("COMMIT")
    except (OSError, sqlite3.Error) as e:
        raise InboxError(f"db unavailable: {path}: {e}", 4) from e
    return conn


def _migrate(conn: sqlite3.Connection, version: int) -> None:
    for target in range(version + 1, SCHEMA_VERSION + 1):
        for statement in MIGRATIONS[target].split(";"):
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
    brief = conn.execute(
        "SELECT title, brief FROM decision_briefs WHERE session_id = ? AND task = ?",
        (row["session_id"], row["task"]),
    ).fetchone()
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
        rev=row["rev"],
        title=brief["title"] if brief else "",
        brief=brief["brief"] if brief else "",
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
    briefs = conn.execute(
        "SELECT session_id, task, title, brief FROM decision_briefs"
        " ORDER BY session_id, task"
    ).fetchall()
    return tuple((str(r["id"]), int(r["rev"])) for r in rows) + tuple(
        ("\x1f".join(str(v) for v in b), 0) for b in briefs
    )


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
    title: str = "",
    brief: str = "",
) -> Decision:
    check_task(task)
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
        if title or brief:
            _upsert_brief(conn, session_id, task, title or None, brief or None)
    return get(conn, decision_id)


def check_task(task: str) -> None:
    if not TASK_RE.match(task):
        raise InboxError(f"bad task {task!r}: letters, digits, - or _", 2)


def _upsert_brief(
    conn: sqlite3.Connection,
    session_id: str,
    task: str,
    title: str | None,
    text: str | None,
) -> None:
    """None keeps the stored value; a string replaces it."""
    conn.execute(
        "INSERT INTO decision_briefs (session_id, task, title, brief, updated_at)"
        " VALUES (?, ?, ?, ?, ?) ON CONFLICT (session_id, task) DO UPDATE SET"
        " title = CASE WHEN ? THEN excluded.title ELSE title END,"
        " brief = CASE WHEN ? THEN excluded.brief ELSE brief END,"
        " updated_at = excluded.updated_at",
        (
            session_id,
            task,
            title or "",
            text or "",
            now(),
            title is not None,
            text is not None,
        ),
    )


def set_brief(
    conn: sqlite3.Connection,
    session_id: str,
    task: str,
    title: str | None,
    text: str | None,
) -> dict[str, str]:
    check_task(task)
    if title is None and text is None:
        raise InboxError("give --title or --text", 2)
    with immediate(conn):
        _upsert_brief(conn, session_id, task, title, text)
        row = conn.execute(
            "SELECT * FROM decision_briefs WHERE session_id = ? AND task = ?",
            (session_id, task),
        ).fetchone()
    return dict(zip(row.keys(), map(str, row), strict=True))


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
    rev: int | None = None,
) -> Decision:
    stamp = now()
    with immediate(conn):
        d = _open_owned(conn, decision_id, caller or "", force)
        if rev is not None and d.rev != rev:
            raise Amended(decision_id)
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
    rev: int | None = None,
) -> Delivery | None:
    """rev, when given, refuses the answer (Amended) if the decision changed."""
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
        rev=rev,
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
# Layout                                                                       #
# --------------------------------------------------------------------------- #

MIN_WIDTH = 20
REC_MARK = "  ← rec"
MORE = "… (View full)"
CUT_ORDER = ("brief", "context", "asked-by", "help", "menu", "title", "options")
Item = tuple[str, str]


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


def wrap(text: str, width: int, first: str = "", rest: str | None = None) -> list[str]:
    """Word-wrap at width with a hanging indent; words longer than a line split."""
    width = max(width, MIN_WIDTH)
    hang = " " * len(first) if rest is None else rest
    out: list[str] = []
    for para in text.splitlines() or [""]:
        lead = hang if out else first
        out += textwrap.wrap(
            para,
            width,
            initial_indent=lead,
            subsequent_indent=hang,
            break_on_hyphens=False,
        ) or [lead.rstrip()]
    return out


def menu_rows(items: int, height: int, show_help: bool) -> int:
    """Rows gum choose (empty header) takes: list, help footer, paging dots."""
    return height + (3 if show_help else 1) + (1 if height < items else 0)


@dataclass
class Brief:
    task: str
    title: str = ""
    text: str = ""
    asked_by: str = ""

    def head(self) -> str:
        return f"#{self.task} {self.title}".rstrip()


@dataclass
class Layout:
    lines: list[str]
    menu_height: int
    show_help: bool = True
    cuts: list[str] = field(default_factory=list)


def asked_by(
    name: str, session: dict[str, object] | None, oldest: str, at: datetime
) -> str:
    meta = [
        str(v)
        for v in (
            (session or {}).get("group"),
            Path(str((session or {}).get("directory") or "")).name,
        )
        if v
    ]
    who = f"{name} ({', '.join(meta)})" if meta else name
    return f"asked by {who} · {session_status(session)} · {age(oldest, at)}"


def briefs_for(
    decisions: list[Decision],
    sessions: list[dict[str, object]] | None,
    at: datetime,
) -> list[Brief]:
    """One context block per task, in ID order."""
    out: dict[str, Brief] = {}
    for d in sorted(decisions, key=lambda d: id_key(d.id)):
        if d.task not in out:
            oldest = min(x.created_at for x in decisions if x.task == d.task)
            session = find_session(sessions, d.session_id)
            line = asked_by(d.session_name, session, oldest, at)
            out[d.task] = Brief(d.task, d.title, d.brief, line)
    return list(out.values())


def context_block(b: Brief, width: int) -> list[str]:
    text = wrap(b.text, width, "  ") if b.text.strip() else []
    return wrap(b.head(), width, rest="  ") + text + wrap(b.asked_by, width)


def question_lines(d: Decision, width: int) -> list[str]:
    tags = (" gate" if d.gate else "") + (" (amended)" if d.amended else "")
    return wrap(f"{d.id}{tags}  {d.question}", width, rest="  ")


def context_lines(d: Decision, width: int) -> list[str]:
    return wrap(d.context, width, "│ ") if d.context.strip() else []


def option_lines(d: Decision, width: int) -> list[str]:
    return [
        line
        for letter, text in d.options
        for line in wrap(
            text + (REC_MARK if letter == d.rec else ""), width, f"{letter}) "
        )
    ]


def decision_lines(d: Decision, width: int) -> list[str]:
    return question_lines(d, width) + context_lines(d, width) + option_lines(d, width)


def question_menu(d: Decision) -> list[Item]:
    picks = [(ltr + (REC_MARK if ltr == d.rec else ""), ltr) for ltr, _ in d.options]
    return [
        *picks,
        ("Free text", "free"),
        ("Skip", "skip"),
        ("Talk", "talk"),
        ("View full", "full"),
        ("Back", "back"),
    ]


def clip_lines(lines: list[str], rows: int, width: int) -> list[str]:
    if len(lines) <= rows:
        return lines
    if rows <= 0:
        return []
    room = max(width, MIN_WIDTH) - len(MORE) - 1
    return [*lines[: rows - 1], f"{lines[rows - 1][:room].rstrip()} {MORE}"]


def screen(d: Decision, brief: Brief, width: int, height: int) -> Layout:
    """Question screen: text above a short-item menu, fitted to width x height.

    Space runs short → cut in CUT_ORDER. Options are clipped only when the
    question and options alone overflow the pane, so the question stays on screen.
    """
    width = max(width, MIN_WIDTH)
    items = len(question_menu(d))
    head = wrap(brief.head(), width, rest="  ")
    text = wrap(brief.text, width, "  ") if brief.text.strip() else []
    asked = wrap(brief.asked_by, width) if brief.asked_by else []
    body = question_lines(d, width)
    ctx = context_lines(d, width)
    opts = option_lines(d, width)
    # (cuts, lines above the question, context clipped?, help shown, menu heights)
    stages: list[tuple[list[str], list[str], bool, bool, range]] = [
        ([], [*head, *text, *asked, ""], False, True, range(items, items - 1, -1)),
        (["brief"], [*head, *asked, ""], False, True, range(items, items - 1, -1)),
        (
            ["brief", "context"],
            [*head, *asked],
            True,
            True,
            range(items, items - 1, -1),
        ),
        (
            ["brief", "context", "asked-by"],
            head,
            True,
            True,
            range(items, items - 1, -1),
        ),
        (
            ["brief", "context", "asked-by", "help"],
            head,
            True,
            False,
            range(items, items - 1, -1),
        ),
        (
            ["brief", "context", "asked-by", "help", "menu"],
            head,
            True,
            False,
            range(items - 1, 0, -1),
        ),
        (list(CUT_ORDER[:-1]), [], True, False, range(items - 1, 0, -1)),
    ]
    for cuts, top, clip, show_help, heights in stages:
        for h in heights:
            room = (
                height
                - menu_rows(items, h, show_help)
                - len(top)
                - len(body)
                - len(opts)
            )
            if room < (min(len(ctx), 1) if clip else len(ctx)):
                continue
            shown = clip_lines(ctx, room, width) if clip else ctx
            return Layout([*top, *body, *shown, *opts], h, show_help, cuts)
    room = height - menu_rows(items, 1, False) - len(body)
    return Layout([*body, *clip_lines(opts, room, width)], 1, False, list(CUT_ORDER))


def fit(lines: list[str], compact: list[str], items: int, height: int) -> Layout:
    """lines if they fit above the menu, else compact, else compact with no help."""
    for text, show_help in ((lines, True), (compact, True), (compact, False)):
        h = min(items, max(height - len(text) - (3 if show_help else 1), 1))
        if len(text) + menu_rows(items, h, show_help) <= height:
            return Layout(text, h, show_help)
    return Layout(compact, 1, False)


def session_lines(
    decisions: list[Decision],
    sessions: list[dict[str, object]] | None,
    width: int,
    at: datetime,
    compact: bool = False,
) -> list[str]:
    lines: list[str] = []
    for b in briefs_for(decisions, sessions, at):
        lines += context_block(b, width)
    for d in sorted(decisions, key=lambda d: id_key(d.id)):
        if compact:
            tag = " gate" if d.gate else ""
            lines.append(_clip(f"{d.id}{tag}  {d.question}", max(width, MIN_WIDTH)))
        else:
            lines += ["", *decision_lines(d, width)]
    return lines


SESSION_MENU: list[Item] = [("Answer", "answer"), ("Talk", "talk"), ("Back", "back")]
REVIEW_MENU: list[Item] = [
    ("Send", "send"),
    ("Change one", "change"),
    ("Cancel", "cancel"),
]


@dataclass
class Staged:
    kind: str
    option: str
    text: str
    rev: int


def staged_text(d: Decision, s: Staged | None) -> str:
    if s is None:
        return "skipped"
    if s.kind == "pick":
        return f"{s.option}: {d.option_text(s.option)}"
    return f"free text: {s.text}"


def review_screen(
    entries: list[tuple[Decision, Staged | None]], width: int, height: int
) -> Layout:
    width = max(width, MIN_WIDTH)

    def lines(question_width: int | None) -> list[str]:
        out = ["Review: Send records these answers.", ""]
        for d, s in entries:
            q = (
                d.question
                if question_width is None
                else _clip(d.question, question_width)
            )
            head = f"{d.id}  {q}" if q else d.id
            out += wrap(f"{head} → {staged_text(d, s)}", width, rest="    ")
        return out

    return fit(lines(None), lines(width // 2), len(REVIEW_MENU), height)


# --------------------------------------------------------------------------- #
# List                                                                         #
# --------------------------------------------------------------------------- #


@dataclass
class Row:
    session_id: str
    name: str
    tasks: list[str]
    title: str
    count: int
    oldest: str
    gate: bool
    status: str


def list_rows(
    opened: list[Decision], sessions: list[dict[str, object]] | None
) -> list[Row]:
    """One row per asking session, oldest open decision first."""
    groups: dict[str, list[Decision]] = {}
    for d in sorted(opened, key=lambda d: (d.created_at, id_key(d.id))):
        groups.setdefault(d.session_id, []).append(d)
    rows = []
    for sid, ds in groups.items():
        tasks = list(
            dict.fromkeys(d.task for d in sorted(ds, key=lambda d: id_key(d.id)))
        )
        title = next((d.title for d in ds if d.title), "") if len(tasks) == 1 else ""
        status = session_status(find_session(sessions, sid))
        rows.append(
            Row(
                sid,
                ds[0].session_name,
                tasks,
                title,
                len(ds),
                ds[0].created_at,
                any(d.gate for d in ds),
                status,
            )
        )
    return rows


def row_label(row: Row, at: datetime | None = None) -> str:
    tasks = ", ".join(f"#{t}" for t in row.tasks)
    parts = [
        row.name,
        f"{tasks} {row.title}".rstrip(),
        f"{row.count} open",
        age(row.oldest, at),
        f"[{row.status}]",
    ]
    return " · ".join(parts + (["gate"] if row.gate else []))


def status_lines(conn: sqlite3.Connection) -> list[str]:
    lines: list[str] = []
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
    return lines


def render(
    conn: sqlite3.Connection,
    sessions: list[dict[str, object]] | None,
    width: int = 80,
    at: datetime | None = None,
) -> str:
    rows = list_rows(select(conn, "status = 'open'"), sessions)
    lines = [line for r in rows for line in wrap(row_label(r, at), width, rest="    ")]
    lines += [line for s in status_lines(conn) for line in wrap(s, width, rest="    ")]
    return "\n".join(lines) if lines else "inbox empty"


def list_items(
    conn: sqlite3.Connection,
    sessions: list[dict[str, object]] | None,
    width: int,
    at: datetime | None = None,
) -> list[Item]:
    # gum's cursor takes 2 columns; a longer label would be cut with "…".
    room = max(width, MIN_WIDTH) - 3
    items = [
        (_clip(row_label(r, at), room), f"s:{r.session_id}")
        for r in list_rows(select(conn, "status = 'open'"), sessions)
    ]
    items += [
        (
            _clip(f"retry {batch[0].session_name}: {len(batch)} answer(s)", room),
            f"r:{sid}",
        )
        for sid, batch in undelivered(conn).items()
    ]
    return items


def list_fingerprint(
    conn: sqlite3.Connection,
    sessions: list[dict[str, object]] | None,
    at: datetime,
) -> tuple[object, ...]:
    """The inbox, the status of every listed session, and the minute (for ages)."""
    sids = sorted({d.session_id for d in select(conn, "status = 'open'")})
    statuses = tuple((sid, session_status(find_session(sessions, sid))) for sid in sids)
    return fingerprint(conn), statuses, int(at.timestamp() // 60)


def bell_messages(opened: list[Decision], seen: set[str]) -> list[str]:
    """One message per session with decisions not seen before."""
    fresh: dict[str, list[Decision]] = {}
    for d in opened:
        if d.id not in seen:
            fresh.setdefault(d.session_id, []).append(d)
    return [f"{ds[0].session_name}: {len(ds)} new decision(s)" for ds in fresh.values()]


# --------------------------------------------------------------------------- #
# Talk                                                                         #
# --------------------------------------------------------------------------- #


def tmux_socket(tmux_env: str | None) -> str:
    return tmux_env.split(",")[0] if tmux_env else ""


def attach_command(session_id: str) -> str:
    """Sessions live on agent-manager's own server, whatever $TMUX names."""
    return f"tmux -L {AM_SOCKET} attach -t am_{session_id}"


def popup_argv(session_id: str, tmux_env: str | None) -> list[str] | None:
    socket = tmux_socket(tmux_env)
    if not socket:
        return None
    inner = f"env -u TMUX {attach_command(session_id)}"
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
    print(f"no tmux popup available; attach with: {attach_command(session_id)}")
    return False


# --------------------------------------------------------------------------- #
# Watch                                                                        #
# --------------------------------------------------------------------------- #


def gum(*args: str, stdin: str | None = None) -> tuple[int, str]:
    # stdout only: gum draws on stderr (README gotcha).
    result = subprocess.run(
        ["gum", *args], stdout=subprocess.PIPE, input=stdin, text=True, check=False
    )
    return result.returncode, result.stdout.strip()


def bell(message: str) -> None:
    try:
        subprocess.Popen(
            ["zen-bell", "window-attention", "-n", "zen-inbox", "-b", message],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except FileNotFoundError:
        pass


class UI(Protocol):
    def size(self) -> tuple[int, int]: ...

    def choose(
        self, lines: list[str], items: list[Item], height: int, show_help: bool
    ) -> str: ...

    def pick_many(self, header: str, items: list[Item]) -> list[str]: ...

    def write(self, header: str) -> str: ...

    def text(self, placeholder: str) -> str: ...

    def pager(self, text: str) -> None: ...

    def talk(self, session_id: str) -> bool: ...


DELIM = "\x1f"


def choose_args(items: list[Item], height: int, show_help: bool) -> list[str]:
    return [
        "choose",
        "--header",
        "",
        "--label-delimiter",
        DELIM,
        "--height",
        str(height),
        "--show-help" if show_help else "--no-show-help",
        *(f"{label.replace(DELIM, ' ')}{DELIM}{value}" for label, value in items),
    ]


def draw(lines: list[str]) -> None:
    print("\033[H\033[2J", end="", flush=True)
    if lines:
        print("\n".join(lines), flush=True)


class GumUI:
    def size(self) -> tuple[int, int]:
        cols, rows = shutil.get_terminal_size()
        return cols, rows

    def choose(
        self, lines: list[str], items: list[Item], height: int, show_help: bool
    ) -> str:
        draw(lines)
        code, out = gum(*choose_args(items, height, show_help))
        return out if code == 0 else ""

    def live_choose(
        self, lines: list[str], items: list[Item], height: int, poller: Poller
    ) -> str | None:
        """The list's menu; None when the poller killed it to redraw."""
        draw(lines)
        proc = subprocess.Popen(
            ["gum", *choose_args(items, height, True)],
            stdout=subprocess.PIPE,
            text=True,
        )
        poller.watch(proc)
        out, _ = proc.communicate()
        poller.watch(None)
        if proc.returncode != 0:
            return None if poller.changed.is_set() else ""
        return out.strip()

    def pick_many(self, header: str, items: list[Item]) -> list[str]:
        width = self.size()[0]
        code, out = gum(
            "choose",
            "--no-limit",
            "--header",
            _clip(header, width),
            "--label-delimiter",
            DELIM,
            *(f"{_clip(label, width - 4)}{DELIM}{value}" for label, value in items),
        )
        return out.splitlines() if code == 0 else []

    def write(self, header: str) -> str:
        width = self.size()[0]
        code, out = gum(
            "write",
            "--header",
            _clip(header, width),
            "--placeholder",
            "answer (empty cancels)",
        )
        return out if code == 0 else ""

    def text(self, placeholder: str) -> str:
        code, out = gum("input", "--placeholder", placeholder)
        return out if code == 0 else ""

    def pager(self, text: str) -> None:
        subprocess.run(["gum", "pager"], input=text, text=True, check=False)

    def talk(self, session_id: str) -> bool:
        return talk(session_id, os.environ.get("TMUX"))


def describe(
    result: Delivery | None, conn: sqlite3.Connection, session_id: str, name: str = ""
) -> str:
    if result is None:
        waiting = open_ids(conn, session_id)
        return f"held until {', '.join(waiting)}" if waiting else "nothing to send"
    if result.ok:
        return f"sent {', '.join(result.ids)} to {name or session_id}"
    return f"delivery failed: {result.error}; zen-inbox retry {session_id}"


def full_text(d: Decision, brief: Brief) -> str:
    lines = [brief.head(), *([brief.text] if brief.text else []), brief.asked_by, ""]
    lines += [f"{d.id}  {d.question}", *([d.context] if d.context else [])]
    lines += [f"{ltr}) {t}{REC_MARK if ltr == d.rec else ''}" for ltr, t in d.options]
    return "\n".join(lines)


def session_open(conn: sqlite3.Connection, session_id: str) -> list[Decision]:
    return select(conn, "session_id = ? AND status = 'open'", (session_id,))


def talk_resolve(
    conn: sqlite3.Connection, ui: UI, session_id: str
) -> tuple[list[str], str]:
    """Popup once, then resolve the decisions the user says it settled."""
    if not ui.talk(session_id):
        return [], f"talk unavailable; attach with: {attach_command(session_id)}"
    opened = session_open(conn, session_id)
    if not opened:
        return [], "nothing open"
    chosen = ui.pick_many(
        "Which did the talk settle? (space marks, none leaves all open)",
        [(f"{d.id}  {d.question}", d.id) for d in opened],
    )
    if not chosen:
        return [], "all left open"
    note = ui.text("note (optional)")
    resolved: list[str] = []
    notes: list[str] = []
    result: Delivery | None = None
    for decision_id in chosen:
        try:
            result = resolve(conn, decision_id, note) or result
            resolved.append(decision_id)
        except InboxError as e:
            notes.append(str(e))
    flash = [f"resolved {', '.join(resolved)}"] if resolved else []
    if result is not None:
        flash.append(describe(result, conn, session_id, opened[0].session_name))
    return resolved, "; ".join(flash + notes)


@dataclass
class Pass:
    session_id: str
    staged: dict[str, Staged] = field(default_factory=dict)
    visited: list[str] = field(default_factory=list)
    notes: list[str] = field(default_factory=list)
    recorded: list[str] = field(default_factory=list)

    def stopped(self, how: str) -> str:
        kept = f"{', '.join(self.recorded)} recorded earlier" if self.recorded else ""
        return "; ".join([f"{how}: {kept or 'nothing recorded'}", *self.notes])


def walk_one(
    conn: sqlite3.Connection,
    ui: UI,
    p: Pass,
    decision_id: str,
    sessions: list[dict[str, object]] | None,
) -> bool:
    """Stage (or skip) one question; False when the user backs out of the pass."""
    while True:
        d = get(conn, decision_id)
        if d.status != "open":
            p.notes.append(f"{d.id} already {d.status}")
            p.staged.pop(d.id, None)
            return True
        width, height = ui.size()
        [brief] = briefs_for([d], sessions, datetime.now(UTC))
        layout = screen(d, brief, width, height)
        choice = ui.choose(
            layout.lines, question_menu(d), layout.menu_height, layout.show_help
        )
        if choice in ("", "back"):
            return False
        if choice == "skip":
            p.staged.pop(d.id, None)
            return True
        if choice == "full":
            ui.pager(full_text(d, brief))
        elif choice == "talk":
            resolved, flash = talk_resolve(conn, ui, d.session_id)
            for decision_id_ in resolved:
                p.staged.pop(decision_id_, None)
            if flash:
                p.notes.append(flash)
            if d.id in resolved:
                return True
        elif choice == "free":
            text = ui.write(f"{d.id}  {d.question}")
            if text.strip():
                p.staged[d.id] = Staged("text", "", text, d.rev)
                return True
        elif d.option_text(choice):
            p.staged[d.id] = Staged("pick", choice, "", d.rev)
            return True


def commit(conn: sqlite3.Connection, p: Pass) -> tuple[list[str], Delivery | None]:
    """Record every staged answer; returns (amended ids, last delivery)."""
    amended: list[str] = []
    result: Delivery | None = None
    for decision_id in sorted(p.staged, key=id_key):
        s = p.staged.pop(decision_id)
        letter = s.option if s.kind == "pick" else None
        text = s.text if s.kind == "text" else None
        try:
            result = answer(conn, decision_id, letter, text, rev=s.rev) or result
            p.recorded.append(decision_id)
        except Amended:
            amended.append(decision_id)
        except InboxError:
            p.notes.append(f"{decision_id} already {get(conn, decision_id).status}")
    return amended, result


def answer_pass(
    conn: sqlite3.Connection,
    ui: UI,
    session_id: str,
    sessions: list[dict[str, object]] | None,
) -> str:
    """Walk, review, then Send; nothing is recorded before Send."""
    p = Pass(session_id)
    todo = open_ids(conn, session_id)
    result: Delivery | None = None
    while True:
        for decision_id in todo:
            if decision_id not in p.visited:
                p.visited.append(decision_id)
            if not walk_one(conn, ui, p, decision_id, sessions):
                return p.stopped("back")
        todo = [i for i in open_ids(conn, session_id) if i not in p.visited]
        if todo:
            continue
        entries = [
            (d, p.staged.get(d.id))
            for d in (get(conn, i) for i in p.visited)
            if d.status == "open"
        ]
        if not entries:
            return "; ".join(p.notes) or "nothing open"
        width, height = ui.size()
        layout = review_screen(entries, width, height)
        choice = ui.choose(
            layout.lines, REVIEW_MENU, layout.menu_height, layout.show_help
        )
        if choice == "send":
            amended, sent = commit(conn, p)
            result = sent or result
            if amended:
                p.notes.append(f"{', '.join(amended)} amended; shown again")
                todo = amended
                continue
            name = entries[0][0].session_name
            return "; ".join([describe(result, conn, session_id, name), *p.notes])
        if choice == "change":
            items = [(f"{d.id}  {d.question}", d.id) for d, _ in entries]
            pick = ui.choose(
                [],
                [(_clip(lbl, width - 3), v) for lbl, v in items] + [("Back", "")],
                min(len(items) + 1, max(height - 3, 1)),
                True,
            )
            todo = [pick] if pick else []
            continue
        return p.stopped("cancelled")


def session_flow(conn: sqlite3.Connection, ui: UI, session_id: str) -> str:
    sessions = am_sessions()
    opened = session_open(conn, session_id)
    if not opened:
        return "nothing open for that session"
    width, height = ui.size()
    at = datetime.now(UTC)
    layout = fit(
        session_lines(opened, sessions, width, at),
        session_lines(opened, sessions, width, at, compact=True),
        len(SESSION_MENU),
        height,
    )
    choice = ui.choose(layout.lines, SESSION_MENU, layout.menu_height, layout.show_help)
    if choice == "answer":
        return answer_pass(conn, ui, session_id, sessions)
    if choice == "talk":
        return talk_resolve(conn, ui, session_id)[1]
    return ""


class Poller(threading.Thread):
    """Kills the list's gum when its view would change; rings once per session."""

    def __init__(self, path: Path) -> None:
        super().__init__(daemon=True)
        self.path = path
        self.last: tuple[object, ...] = ()
        self.seen: set[str] | None = None
        self.sessions: list[dict[str, object]] | None = None
        self.sessions_at = 0.0
        self.changed = threading.Event()
        self.lock = threading.Lock()
        self.proc: subprocess.Popen[str] | None = None

    def watch(self, proc: subprocess.Popen[str] | None) -> None:
        with self.lock:
            self.proc = proc

    def sync(
        self, current: tuple[object, ...], sessions: list[dict[str, object]] | None
    ) -> None:
        self.last = current
        self.sessions = sessions
        self.sessions_at = time.monotonic()
        self.changed.clear()

    def ring(self, conn: sqlite3.Connection) -> None:
        opened = select(conn, "status = 'open'")
        if self.seen is not None:
            for message in bell_messages(opened, self.seen):
                bell(message)
        self.seen = (self.seen or set()) | {d.id for d in opened}

    def cycle(self, conn: sqlite3.Connection, at: datetime) -> bool:
        if time.monotonic() - self.sessions_at >= SESSIONS_SECONDS:
            self.sessions = am_sessions()
            self.sessions_at = time.monotonic()
        self.ring(conn)
        current = list_fingerprint(conn, self.sessions, at)
        if current == self.last:
            return False
        self.last = current
        return True

    def run(self) -> None:
        conn: sqlite3.Connection | None = None
        while True:
            time.sleep(POLL_SECONDS)
            try:
                conn = conn or connect(self.path)
                changed = self.cycle(conn, datetime.now(UTC))
            except (InboxError, sqlite3.Error):
                continue
            if not changed:
                continue
            self.changed.set()
            with self.lock:
                if self.proc is not None and self.proc.poll() is None:
                    self.proc.terminate()


def watch_loop(path: Path) -> None:
    conn = connect(path)
    poller = Poller(path)
    poller.ring(conn)
    poller.start()
    ui = GumUI()
    flash = ""
    while True:
        sessions = am_sessions()
        at = datetime.now(UTC)
        poller.sync(list_fingerprint(conn, sessions, at), sessions)
        width, height = ui.size()
        lines = [*wrap(f"» {flash}", width, rest="  "), ""] if flash else []
        lines += [
            line for s in status_lines(conn) for line in wrap(s, width, rest="    ")
        ]
        items = list_items(conn, sessions, width, at)
        if not items:
            draw([*lines, "inbox empty"])
            while not poller.changed.wait(0.5):
                pass
            continue
        menu = min(len(items), max(height - len(lines) - 4, 1))
        value = ui.live_choose(lines, items, menu, poller)
        if value is None:
            continue
        if not value:
            return
        flash = ""
        kind, _, session_id = value.partition(":")
        try:
            if kind == "r":
                flash = describe(retry(conn, session_id), conn, session_id)
            else:
                flash = session_flow(conn, ui, session_id)
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
    title: str = typer.Option("", "--title", help="Board task title."),
    brief: str = typer.Option("", "--brief", help="1-3 lines of task context."),
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
            conn,
            task,
            question,
            option or [],
            rec,
            context,
            gate,
            session_id,
            name,
            title,
            brief,
        )
    except InboxError as e:
        raise _fail(e) from e
    print(json.dumps(d.to_json()) if as_json else d.id)


@app.command("brief")
def cmd_brief(
    task: str = typer.Argument(..., help="Board task ID."),
    title: str | None = typer.Option(None, "--title", help="Board task title."),
    text: str | None = typer.Option(None, "--text", help="1-3 lines of context."),
    session: str = typer.Option("", "--session", help="Asking session id."),
    as_json: bool = typer.Option(False, "--json"),
) -> None:
    """Set or replace a task's title and brief for the asking session."""
    session_id = session or _caller()
    if not session_id:
        raise _fail(
            InboxError("no session: set AGENT_MANAGER_SESSION_ID or --session", 2)
        )
    try:
        row = set_brief(_open(), session_id, task, title, text)
    except InboxError as e:
        raise _fail(e) from e
    print(json.dumps(row) if as_json else f"brief {task} for {session_id}")


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
            sessions = am_sessions()
            width = shutil.get_terminal_size().columns
            at = datetime.now(UTC)
            print(render(conn, sessions, width, at))
            for row in list_rows(select(conn, "status = 'open'"), sessions):
                opened = session_open(conn, row.session_id)
                print("\n" + "\n".join(session_lines(opened, sessions, width, at)))
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
    print(f"#{d.task} {d.title}".rstrip())
    if d.brief:
        print(d.brief)
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
    """Live view, one row per asking session; walk, review, then Send."""
    try:
        watch_loop(db_path(Path.home()))
    except InboxError as e:
        raise _fail(e) from e
    except KeyboardInterrupt:
        print()


if __name__ == "__main__":
    app()
