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
#       _ ___      ____ _(_) |_      z e n - a w a i t
#      / _` \ \ /\ / / _` | | __|
#     | (_| |\ V  V / (_| | | |_
#      \__,_| \_/\_/ \__,_|_|\__|
#
# ------------------------------------------------------------------------------
#
#  NAME
#      await.py  --  park until a watched agent-manager session has an event
#
#  SYNOPSIS
#      zen-await <session>... [--timeout 4h] [--new] [--json]
#      zen-await --summary <session>... [--json]
#
#  OPTIONS
#      --timeout DUR  Give up after DUR (e.g. 90s, 30m, 4h, 1h30m). Default 4h.
#      --new          Ignore a final message already there when the wait starts
#                     (use right after `agent-manager send`).
#      --summary      Classify each session's last message now, without waiting.
#                     One line per session, in argument order.
#      --json         Print {session, task_id, state, summary} as JSON
#                     (JSON Lines with several sessions).
#
#  DESCRIPTION
#      Loops `agent-manager wait --timeout 5m` on every watched session and
#      returns on the first one that stops working. Its transcript is read from
#      ~/.claude/projects/<slug>/<agent_session_id>.jsonl and the last end_turn
#      text is classified. Meant for agents: run it from Bash with
#      run_in_background, so the wait costs no turns.
#      A live session that is not waiting and has no end_turn yet (a worker
#      still reading its spec) keeps being waited on; after 10m without a reply
#      it returns idle. A session that is archived, not running, dead or errored
#      is dead unless its last message carries a done marker; an unreadable or
#      empty sessions list never proves that (retried, then keeps waiting).
#      Session parsing lives in lib/sessions.py. A background run dies with
#      the shell that started it (e.g. on an agent-manager restart); re-run it.
#
#  EXIT CODES
#      0    done: `WORKER_DONE <id>` or `SPEC_READY <id>`
#      10   escalation: `failing · tried · cause · needs`
#      11   idle: stopped without a marker, or waiting on a prompt
#      20   dead or errored session
#      With --summary and several sessions: the most severe code,
#      20 > 11 > 10 > 0.
#      124  timeout
#      2    usage error (unknown session)
#
#  AUTHOR
#      vzsoares.
#
# ==============================================================================
"""Wait for an agent-manager session to finish, then classify its last message."""

from __future__ import annotations

import importlib.util
import json
import re
import subprocess
import sys
import time
from dataclasses import asdict, dataclass
from pathlib import Path
from types import ModuleType
from typing import Annotated

import typer

EXIT_DONE = 0
EXIT_USAGE = 2
EXIT_ESCALATION = 10
EXIT_IDLE = 11
EXIT_DEAD = 20
EXIT_TIMEOUT = 124

STATE_EXIT = {
    "done": EXIT_DONE,
    "escalation": EXIT_ESCALATION,
    "idle": EXIT_IDLE,
    "dead": EXIT_DEAD,
    "timeout": EXIT_TIMEOUT,
}

WAIT_SLICE = "5m"
STARTUP_GRACE = 600
REREAD_TRIES = 4
SUMMARY_MAX = 1500

MARKER_RE = re.compile(r"\b(WORKER_DONE|SPEC_READY)\s+#?(\d+)\b")
TASK_RE = re.compile(r"(?:\btask\s*|#)(\d+)\b", re.IGNORECASE)
ESCALATION_WORDS = ("failing", "tried", "cause", "needs")

SEVERITY = {"done": 0, "escalation": 1, "idle": 2, "dead": 3}


def load_sessions() -> ModuleType:
    path = Path(__file__).resolve().parent / "lib" / "sessions.py"
    spec = importlib.util.spec_from_file_location("zen_sessions", path)
    if spec is None or spec.loader is None:
        raise ImportError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["zen_sessions"] = module
    spec.loader.exec_module(module)
    return module


_sessions = load_sessions()
DEAD_STATUSES = _sessions.DEAD_STATUSES
list_sessions = _sessions.list_sessions
find_session = _sessions.find_session
is_live = _sessions.is_live


@dataclass
class Event:
    session: str
    task_id: str | None
    state: str
    summary: str

    @property
    def exit_code(self) -> int:
        return STATE_EXIT[self.state]


# --------------------------------------------------------------------------- #
# Classification (pure)                                                        #
# --------------------------------------------------------------------------- #


def is_escalation(text: str) -> bool:
    lower = text.lower()
    return all(re.search(rf"\b{w}\b", lower) for w in ESCALATION_WORDS)


def classify(text: str) -> tuple[str, str | None]:
    """Map a final message to (state, task_id)."""
    markers = MARKER_RE.findall(text)
    if markers:
        return "done", markers[-1][1]
    task = TASK_RE.search(text)
    task_id = task.group(1) if task else None
    if is_escalation(text):
        return "escalation", task_id
    return "idle", task_id


def summarize(text: str) -> str:
    text = text.strip()
    if len(text) <= SUMMARY_MAX:
        return text
    return "…" + text[-SUMMARY_MAX:]


def parse_duration(value: str) -> float:
    """Parse 90s / 30m / 4h / 1h30m (bare numbers are seconds)."""
    value = value.strip().lower()
    if re.fullmatch(r"\d+(\.\d+)?", value):
        return float(value)
    parts = re.findall(r"(\d+(?:\.\d+)?)([hms])", value)
    if not parts or "".join(n + u for n, u in parts) != value:
        raise typer.BadParameter(f"bad duration: {value!r}")
    scale = {"h": 3600, "m": 60, "s": 1}
    return sum(float(n) * scale[u] for n, u in parts)


# --------------------------------------------------------------------------- #
# Transcripts                                                                  #
# --------------------------------------------------------------------------- #


def project_slug(directory: str) -> str:
    return re.sub(r"[^A-Za-z0-9]", "-", directory)


def transcript_path(
    session: dict[str, object], root: Path | None = None
) -> Path | None:
    directory = session.get("directory")
    agent_id = session.get("agent_session_id")
    if not isinstance(directory, str) or not isinstance(agent_id, str):
        return None
    base = root if root is not None else Path.home() / ".claude" / "projects"
    return base / project_slug(directory) / f"{agent_id}.jsonl"


def _records(path: Path) -> list[dict[str, object]]:
    out: list[dict[str, object]] = []
    for line in path.read_text(errors="replace").splitlines():
        try:
            rec = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(rec, dict):
            out.append(rec)
    return out


def _message(rec: dict[str, object]) -> dict[str, object] | None:
    if rec.get("type") != "assistant" or rec.get("isSidechain"):
        return None
    msg = rec.get("message")
    return msg if isinstance(msg, dict) else None


def _text_of(msg: dict[str, object]) -> str:
    content = msg.get("content")
    if isinstance(content, str):
        return content
    if not isinstance(content, list):
        return ""
    return "\n".join(
        str(block.get("text", ""))
        for block in content
        if isinstance(block, dict) and block.get("type") == "text"
    )


def last_end_turn(path: Path) -> tuple[str, str]:
    """Return (uuid, text) of the last end_turn assistant message, or ("", "").

    One API message can span several jsonl records (one per content block), so
    text is gathered from every record sharing the end_turn message's id.
    """
    if not path.exists():
        return "", ""
    records = _records(path)
    for i in range(len(records) - 1, -1, -1):
        msg = _message(records[i])
        if msg is None or msg.get("stop_reason") != "end_turn":
            continue
        msg_id = msg.get("id")
        parts: list[str] = []
        for rec in records[: i + 1]:
            other = _message(rec)
            if other is not None and (
                other is msg or (msg_id and other.get("id") == msg_id)
            ):
                parts.append(_text_of(other))
        return str(records[i].get("uuid", i)), "\n".join(p for p in parts if p).strip()
    return "", ""


# --------------------------------------------------------------------------- #
# agent-manager                                                                #
# --------------------------------------------------------------------------- #


def event_for(session: dict[str, object], root: Path | None = None) -> Event:
    name = str(session.get("name") or session.get("id"))
    status = str(session.get("status", ""))
    path = transcript_path(session, root)
    _, text = last_end_turn(path) if path else ("", "")
    state, task_id = classify(text)
    if not is_live(session) and state != "done":
        state = "dead"
    elif status == "waiting":
        # A pending prompt outranks whatever the previous final message said.
        state, text = "idle", f"waiting for input\n{text}"
    return Event(session=name, task_id=task_id, state=state, summary=summarize(text))


def reread_session(session_id: str, gap: float) -> tuple[str, dict[str, object] | None]:
    """Look a session up again: ("found", s), ("missing", None) or ("unknown", None).

    Missing needs two consecutive readable, non-empty lists without it, `gap`
    apart. A failed, unreadable or empty read never proves anything: it resets
    that count and is retried, then the answer is unknown.
    """
    misses = 0
    for attempt in range(REREAD_TRIES):
        if attempt:
            time.sleep(gap)
        known = list_sessions()
        if not known:
            misses = 0
            continue
        found = find_session(session_id, known)
        if found is not None:
            return "found", found
        misses += 1
        if misses == 2:
            return "missing", None
    return "unknown", None


def session_alive(session_id: str, gap: float = 5.0) -> bool:
    """False only when the manager shows the session dead or gone."""
    state, found = reread_session(session_id, gap)
    if state == "found" and found is not None:
        return is_live(found)
    return state == "unknown"


def fmt_duration(seconds: float) -> str:
    return f"{seconds / 60:g}m" if seconds >= 60 else f"{seconds:g}s"


def _spawn_wait(session_id: str) -> subprocess.Popen[str]:
    return subprocess.Popen(
        ["agent-manager", "wait", session_id, "--timeout", WAIT_SLICE, "--json"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )


def await_sessions(
    sessions: list[dict[str, object]],
    timeout: float,
    new_only: bool = False,
    root: Path | None = None,
    poll: float = 1.0,
) -> Event:
    """Block until one watched session stops with a (new) event."""
    deadline = time.monotonic() + timeout
    seen: dict[str, str] = {}
    silent: dict[str, float] = {}
    if new_only:
        for s in sessions:
            path = transcript_path(s, root)
            seen[str(s["id"])] = last_end_turn(path)[0] if path else ""

    procs: dict[str, subprocess.Popen[str]] = {}
    try:
        while time.monotonic() < deadline:
            for s in sessions:
                sid = str(s["id"])
                if sid not in procs:
                    procs[sid] = _spawn_wait(sid)
            for s in sessions:
                sid = str(s["id"])
                proc = procs[sid]
                if proc.poll() is None:
                    continue
                out, err = proc.communicate()
                del procs[sid]
                if not out.strip().startswith("{"):
                    # Archived or deleted sessions get a refusal, not a JSON result;
                    # a manager hiccup also lands here, so confirm before calling it.
                    if session_alive(sid, poll * 5):
                        time.sleep(poll * 5)
                        continue
                    return Event(
                        session=str(s.get("name") or sid),
                        task_id=None,
                        state="dead",
                        summary=summarize(err),
                    )
                result = json.loads(out)
                if not isinstance(result, dict) or not result.get("reached"):
                    continue
                current = result.get("session")
                if not isinstance(current, dict):
                    # The startup snapshot cannot tell dead from idle.
                    state, found = reread_session(sid, poll * 5)
                    if state == "unknown":
                        continue
                    current = found if found is not None else {**s, "running": False}
                path = transcript_path(current, root)
                uuid = last_end_turn(path)[0] if path else ""
                stale = is_live(current) and current.get("status") != "waiting"
                if new_only and stale and uuid == seen.get(sid, ""):
                    time.sleep(poll * 5)
                    continue
                if stale and not uuid:
                    first_silent = silent.setdefault(sid, time.monotonic())
                    if time.monotonic() - first_silent < STARTUP_GRACE:
                        time.sleep(poll * 5)
                        continue
                    return Event(
                        session=str(current.get("name") or sid),
                        task_id=None,
                        state="idle",
                        summary=f"no reply yet after {fmt_duration(STARTUP_GRACE)}",
                    )
                return event_for(current, root)
            time.sleep(poll)
    finally:
        for proc in procs.values():
            proc.kill()
    names = ", ".join(str(s.get("name") or s.get("id")) for s in sessions)
    return Event(session=names, task_id=None, state="timeout", summary="")


# --------------------------------------------------------------------------- #
# CLI                                                                          #
# --------------------------------------------------------------------------- #


def emit(event: Event, as_json: bool) -> None:
    if as_json:
        print(json.dumps(asdict(event)))
        return
    last = event.summary.splitlines()[-1] if event.summary else ""
    task = f"#{event.task_id}" if event.task_id else "-"
    print(f"{event.session} · {event.state} · {task} · {last}".rstrip(" ·"))


app = typer.Typer(add_completion=False, help=__doc__)


@app.command()
def main(
    sessions: Annotated[
        list[str], typer.Argument(help="Session ids or names to watch.")
    ],
    timeout: str = typer.Option("4h", "--timeout", help="Give up after this long."),
    new: bool = typer.Option(
        False, "--new", help="Ignore a final message already there."
    ),
    summary: bool = typer.Option(False, "--summary", help="Classify now, no wait."),
    as_json: bool = typer.Option(False, "--json", help="JSON output."),
) -> None:
    """Wait for a session to stop; exit 0 done, 10 escalation, 11 idle, 20 dead, 124 timeout."""
    known = list_sessions()
    if known is None:
        print("zen-await: agent-manager sessions failed", file=sys.stderr)
        raise typer.Exit(EXIT_USAGE)
    watched: list[dict[str, object]] = []
    for key in sessions:
        found = find_session(key, known)
        if found is None:
            print(f"zen-await: unknown session {key!r}", file=sys.stderr)
            raise typer.Exit(EXIT_USAGE)
        watched.append(found)

    if summary:
        events = [event_for(s) for s in watched]
        for event in events:
            emit(event, as_json)
        worst = max(events, key=lambda e: SEVERITY[e.state])
        raise typer.Exit(worst.exit_code)
    event = await_sessions(watched, parse_duration(timeout), new_only=new)
    emit(event, as_json)
    raise typer.Exit(event.exit_code)


if __name__ == "__main__":
    app()
