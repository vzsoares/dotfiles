# /// script
# requires-python = ">=3.11"
# dependencies = ["typer>=0.12"]
# ///
"""Writes v1.sql and v2.sql: dbs made by inbox.py ($INBOX_PY), as SQL dumps.

Run once, before inbox.py was deleted, from inbox/testdata/xver:
  INBOX_PY=<path to inbox.py> uv run -q --script make_fixtures.py
v1 is inbox.py's v1 SCHEMA with rows in every state; v2 is the same history
written through inbox.py's own functions (briefs included).
"""

import importlib.util
import os
import sqlite3
import sys
from pathlib import Path

spec = importlib.util.spec_from_file_location("zen_inbox", os.environ["INBOX_PY"])
assert spec is not None and spec.loader is not None
inbox = importlib.util.module_from_spec(spec)
sys.modules["zen_inbox"] = inbox
spec.loader.exec_module(inbox)

T0 = "2026-01-01T00:00:00+00:00"


def dump(conn: sqlite3.Connection, version: int, out: Path) -> None:
    lines = [line for line in conn.iterdump()]
    lines.append(f"PRAGMA user_version = {version};")
    out.write_text("\n".join(lines) + "\n")


def v1(out: Path) -> None:
    path = out.with_suffix(".db")
    path.unlink(missing_ok=True)
    raw = sqlite3.connect(path)
    for statement in inbox.SCHEMA.split(";"):
        if statement.strip():
            raw.execute(statement)
    rows = [
        ("7.1", "7", 1, 0, "s1", "spec-s1", "Old?", "", "b", "open", 0, "", "", "", "", "none", "", "", 0),
        ("7.2", "7", 2, 0, "s1", "spec-s1", "Free?", "why", "", "open", 1, "", "", "", "", "none", "", "", 1),
        ("7.g", "7", 1, 1, "s1", "spec-s1", "Gate?", "", "", "answered", 0, "pick", "a", "", "go", "sent", "42", "", 3),
        ("8.1", "8", 1, 0, "s2", "spec-s2", "Failed?", "", "", "answered", 0, "text", "", "later", "", "failed", "", "send refused", 2),
        ("8.2", "8", 2, 0, "s2", "spec-s2", "Gone?", "", "", "withdrawn", 0, "", "", "", "", "none", "", "", 1),
    ]
    for r in rows:
        raw.execute(
            "INSERT INTO decisions (id, task, seq, gate, session_id, session_name, question,"
            " context, rec, status, amended, answer_kind, answer_option, answer_text, note,"
            " delivery, message_id, delivery_error, rev, created_at, updated_at, answered_at)"
            " VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (*r, T0, T0, T0 if r[9] != "open" else ""),
        )
    raw.executemany(
        "INSERT INTO decision_options VALUES (?, ?, ?)",
        [("7.1", "a", "x"), ("7.1", "b", "y"), ("7.g", "a", "ship"), ("7.g", "b", "hold")],
    )
    raw.execute("PRAGMA user_version = 1")
    raw.commit()
    dump(raw, 1, out)
    raw.close()
    path.unlink()


def v2(out: Path) -> None:
    path = out.with_suffix(".db")
    path.unlink(missing_ok=True)
    os.environ["PATH"] = "/nonexistent"  # no agent-manager: sends fail, sessions None
    conn = inbox.connect(path)
    inbox.ask(conn, "7", "Which way?", ["left", "right"], "a", "ctx", False, "s1", "spec-s1",
              "Zen board", "What the board does.")
    inbox.ask(conn, "7", "Free?", [], "", "", False, "s1", "spec-s1")
    inbox.ask(conn, "7", "Gate?", ["yes", "no"], "", "", True, "s1", "spec-s1")
    inbox.amend(conn, "7.2", "s1", False, "Free now?", None, None, "more")
    inbox.answer(conn, "7.g", "a", None, note="go")
    inbox.ask(conn, "8", "Failed?", ["a", "b"], "", "", False, "s2", "spec-s2", "Other")
    inbox.answer(conn, "8.1", None, "later")
    inbox.ask(conn, "8", "Gone?", [], "", "", False, "s2", "spec-s2")
    inbox.withdraw(conn, "8.2", "s2", False)
    conn.execute("UPDATE decisions SET created_at = ?, updated_at = ?,"
                 " answered_at = CASE WHEN answered_at = '' THEN '' ELSE ? END", (T0, T0, T0))
    conn.execute("UPDATE decision_briefs SET updated_at = ?", (T0,))
    conn.execute("PRAGMA journal_mode = DELETE")
    dump(conn, 2, out)
    conn.close()
    path.unlink()


here = Path(__file__).parent
v1(here / "v1.sql")
v2(here / "v2.sql")
