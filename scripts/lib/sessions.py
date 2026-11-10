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


def parse_sessions(out: str) -> list[dict[str, object]]:
    try:
        data = json.loads(out or "[]")
    except json.JSONDecodeError:
        return []
    return [s for s in data if isinstance(s, dict)] if isinstance(data, list) else []


def list_sessions() -> list[dict[str, object]] | None:
    """Sessions from `sessions --json`; None when agent-manager is unavailable."""
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
    """Match by id, then by name; archived sessions keep names, so prefer live."""
    for s in sessions:
        if s.get("id") == key:
            return s
    named = [s for s in sessions if s.get("name") == key]
    return next((s for s in named if is_live(s)), None) or next(iter(named), None)
