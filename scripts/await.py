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
#      zen-await --summary <session> [--json]
#
#  OPTIONS
#      --timeout DUR  Give up after DUR (e.g. 90s, 30m, 4h, 1h30m). Default 4h.
#      --new          Ignore a final message already there when the wait starts
#                     (use right after `agent-manager send`).
#      --summary      Classify the session's last message now, without waiting.
#      --json         Print {session, task_id, state, summary} as JSON.
#
#  DESCRIPTION
#      Loops `agent-manager wait --timeout 5m` on every watched session and
#      returns on the first one that stops working. Its transcript is read from
#      ~/.claude/projects/<slug>/<agent_session_id>.jsonl and the last end_turn
#      text is classified. Meant for agents: run it from Bash with
#      run_in_background, so the wait costs no turns.
#
#  EXIT CODES
#      0    done: `WORKER_DONE <id>` or `SPEC_READY <id>`
#      10   escalation: `failing · tried · cause · needs`
#      11   idle: stopped without a marker, or waiting on a prompt
#      20   dead or errored session
#      124  timeout
#      2    usage error (unknown session)
#
#  AUTHOR
#      vzsoares.
#
# ==============================================================================
"""Wait for an agent-manager session to finish, then classify its last message."""

from __future__ import annotations

import json
import re
import subprocess
import sys
import time
from dataclasses import asdict, dataclass
from pathlib import Path
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

DEAD_STATUSES = ("dead", "errored")
WAIT_SLICE = "5m"
SUMMARY_MAX = 1500

MARKER_RE = re.compile(r"\b(WORKER_DONE|SPEC_READY)\s+#?(\d+)\b")
TASK_RE = re.compile(r"(?:\btask\s*|#)(\d+)\b", re.IGNORECASE)
ESCALATION_WORDS = ("failing", "tried", "cause", "needs")


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


def agent_manager(*args: str) -> tuple[int, str]:
    result = subprocess.run(
        ["agent-manager", *args], capture_output=True, text=True, check=False
    )
    return result.returncode, result.stdout


def list_sessions_from(out: str) -> list[dict[str, object]]:
    try:
        data = json.loads(out or "[]")
    except json.JSONDecodeError:
        return []
    return [s for s in data if isinstance(s, dict)] if isinstance(data, list) else []


def list_sessions() -> list[dict[str, object]]:
    code, out = agent_manager("sessions", "--json")
    return list_sessions_from(out) if code == 0 else []


def find_session(
    key: str, sessions: list[dict[str, object]]
) -> dict[str, object] | None:
    for s in sessions:
        if s.get("id") == key:
            return s
    named = [s for s in sessions if s.get("name") == key]
    # Archived sessions keep their names, so a reused name must pick the live one.
    return next((s for s in named if not s.get("archived")), None) or next(
        iter(named), None
    )


def event_for(session: dict[str, object], root: Path | None = None) -> Event:
    name = str(session.get("name") or session.get("id"))
    status = str(session.get("status", ""))
    path = transcript_path(session, root)
    _, text = last_end_turn(path) if path else ("", "")
    state, task_id = classify(text)
    if status in DEAD_STATUSES and state != "done":
        state = "dead"
    elif status == "waiting":
        # A pending prompt outranks whatever the previous final message said.
        state, text = "idle", f"waiting for input\n{text}"
    return Event(session=name, task_id=task_id, state=state, summary=summarize(text))


def session_alive(session_id: str) -> bool:
    """True if the manager still lists the session as live (or is unreachable)."""
    code, out = agent_manager("sessions", "--json")
    if code != 0:
        return True
    found = find_session(session_id, list_sessions_from(out))
    return (
        found is not None
        and not found.get("archived")
        and found.get("status") not in DEAD_STATUSES
    )


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
                    if session_alive(sid):
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
                fresh = result.get("session")
                current = fresh if isinstance(fresh, dict) else s
                path = transcript_path(current, root)
                uuid = last_end_turn(path)[0] if path else ""
                stale = current.get("status") not in (*DEAD_STATUSES, "waiting")
                if new_only and stale and uuid == seen.get(sid, ""):
                    time.sleep(poll * 5)
                    continue
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
    watched: list[dict[str, object]] = []
    for key in sessions:
        found = find_session(key, known)
        if found is None:
            print(f"zen-await: unknown session {key!r}", file=sys.stderr)
            raise typer.Exit(EXIT_USAGE)
        watched.append(found)

    if summary:
        event = event_for(watched[0])
    else:
        event = await_sessions(watched, parse_duration(timeout), new_only=new)
    emit(event, as_json)
    raise typer.Exit(event.exit_code)


if __name__ == "__main__":
    app()
