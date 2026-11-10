"""Shared agent-manager session parsing for zen-await and zen-sweep.

Plain stdlib, no CLI. Scripts load it by path with importlib.
"""

from __future__ import annotations

import json
import subprocess

DEAD_STATUSES = ("dead", "errored")


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
