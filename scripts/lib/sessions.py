"""Shared agent-manager session and transcript parsing for the zen-* scripts.

Plain stdlib, no CLI. Scripts load it by path with importlib.
"""

from __future__ import annotations

import glob
import json
import os
import re
import subprocess
from pathlib import Path

# "errored" is not here: a usage limit leaves it set on a running session
# until its next turn, and a send still lands.
DEAD_STATUSES = ("dead",)


def agent_manager(*args: str) -> tuple[int, str]:
    """Run agent-manager; (127, "") when the binary is not installed."""
    try:
        result = subprocess.run(
            ["agent-manager", *args], capture_output=True, text=True, check=False
        )
    except FileNotFoundError:
        return 127, ""
    return result.returncode, result.stdout


def parse_sessions(out: str) -> list[dict[str, object]] | None:
    """Session dicts from JSON output; None when it is not a JSON list."""
    try:
        data = json.loads(out or "[]")
    except json.JSONDecodeError:
        return None
    return [s for s in data if isinstance(s, dict)] if isinstance(data, list) else None


def list_sessions() -> list[dict[str, object]] | None:
    """Sessions from `sessions --json`; None when agent-manager fails or lies."""
    code, out = agent_manager("sessions", "--json")
    return parse_sessions(out) if code == 0 else None


def is_live(session: dict[str, object]) -> bool:
    return (
        not session.get("archived")
        and session.get("running") is not False
        and session.get("status") not in DEAD_STATUSES
    )


def find_session(
    key: str, sessions: list[dict[str, object]]
) -> dict[str, object] | None:
    """Match by id, then by name: live, then unarchived, then any (list order)."""
    for s in sessions:
        if s.get("id") == key:
            return s
    named = [s for s in sessions if s.get("name") == key]
    tiers = (is_live, lambda s: not s.get("archived"), lambda s: True)
    for tier in tiers:
        match = next((s for s in named if tier(s)), None)
        if match is not None:
            return match
    return None


BG_START_RE = re.compile(r"^Command running in background with ID: ([\w-]+)")
TASK_ID_RE = re.compile(r"<task-id>([\w-]+)</task-id>")
# TaskStop's tool_result; a stopped run gets no <task-notification>.
BG_STOP_RE = re.compile(r'^\{"message":"Successfully stopped task: ([\w-]+)')


def _queued_texts(rec: dict[str, object]) -> list[str]:
    """Prompt queued while the agent was busy (a mid-turn <task-notification>)."""
    attachment = rec.get("attachment")
    if rec.get("type") != "attachment" or rec.get("isSidechain"):
        return []
    if not isinstance(attachment, dict) or attachment.get("type") != "queued_command":
        return []
    prompt = attachment.get("prompt")
    return [prompt] if isinstance(prompt, str) else []


def _user_texts(rec: dict[str, object]) -> tuple[list[str], list[str]]:
    """(tool_result texts, plain texts) of a main-chain user record."""
    msg = rec.get("message")
    if rec.get("type") != "user" or rec.get("isSidechain") or not isinstance(msg, dict):
        return [], []
    content = msg.get("content")
    if isinstance(content, str):
        return [], [content]
    if not isinstance(content, list):
        return [], []
    results: list[str] = []
    texts: list[str] = []
    for block in content:
        if not isinstance(block, dict):
            continue
        if block.get("type") == "text":
            texts.append(str(block.get("text", "")))
        elif block.get("type") == "tool_result":
            inner = block.get("content")
            if isinstance(inner, str):
                results.append(inner)
            elif isinstance(inner, list):
                results.extend(
                    str(b.get("text", ""))
                    for b in inner
                    if isinstance(b, dict) and b.get("type") == "text"
                )
    return results, texts


def pending_background(records: list[dict[str, object]]) -> list[str]:
    """IDs of Bash background runs not yet notified or stopped."""
    pending: dict[str, None] = {}
    for rec in records:
        results, texts = _user_texts(rec)
        for result in results:
            match = BG_START_RE.match(result)
            if match:
                pending[match.group(1)] = None
            stopped = BG_STOP_RE.match(result)
            if stopped:
                pending.pop(stopped.group(1), None)
        for text in texts + _queued_texts(rec):
            if "<task-notification>" in text:
                for task_id in TASK_ID_RE.findall(text):
                    pending.pop(task_id, None)
    return list(pending)


def project_slug(directory: str) -> str:
    return re.sub(r"[^A-Za-z0-9]", "-", directory)


def pi_slug(directory: str) -> str:
    """pi's session dir name for a cwd (session-manager getDefaultSessionDirPath)."""
    return "--" + re.sub(r"[/\\:]", "-", re.sub(r"^[/\\]", "", directory)) + "--"


def is_pi(session: dict[str, object]) -> bool:
    return session.get("tool") == "pi"


def transcript_path(
    session: dict[str, object], root: Path | None = None
) -> Path | None:
    directory = session.get("directory")
    agent_id = session.get("agent_session_id")
    if not isinstance(directory, str) or not isinstance(agent_id, str):
        return None
    if is_pi(session):
        # Boxed pi (pi-box env profile) first, then host pi. The box names the
        # dir after the physical cwd (pwd -P).
        bases = (
            [root]
            if root is not None
            else [
                Path.home() / "pi-box" / "agent" / "sessions",
                Path.home() / ".pi" / "agent" / "sessions",
            ]
        )
        pattern = f"*_{glob.escape(agent_id)}.jsonl"
        for base in bases:
            for d in dict.fromkeys([directory, os.path.realpath(directory)]):
                found = sorted((base / pi_slug(d)).glob(pattern))
                if found:
                    return found[-1]
        return None
    base = root if root is not None else Path.home() / ".claude" / "projects"
    return base / project_slug(directory) / f"{agent_id}.jsonl"


def records(path: Path) -> list[dict[str, object]]:
    out: list[dict[str, object]] = []
    for line in path.read_text(errors="replace").splitlines():
        try:
            rec = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(rec, dict):
            out.append(rec)
    return out


def assistant_message(rec: dict[str, object]) -> dict[str, object] | None:
    if rec.get("type") != "assistant" or rec.get("isSidechain"):
        return None
    msg = rec.get("message")
    return msg if isinstance(msg, dict) else None


def text_of(msg: dict[str, object]) -> str:
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
    recs = records(path)
    for i in range(len(recs) - 1, -1, -1):
        msg = assistant_message(recs[i])
        if msg is None or msg.get("stop_reason") != "end_turn":
            continue
        msg_id = msg.get("id")
        parts: list[str] = []
        for rec in recs[: i + 1]:
            other = assistant_message(rec)
            if other is not None and (
                other is msg or (msg_id and other.get("id") == msg_id)
            ):
                parts.append(text_of(other))
        return str(recs[i].get("uuid", i)), "\n".join(p for p in parts if p).strip()
    return "", ""
