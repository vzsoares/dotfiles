#!/usr/bin/env python3
#
# zen-chief — message the live chief session, whatever its id is now.
#
#   zen-chief "<message>"     queue it with `agent-manager send --from zenha`
#                             and print the message id
#   zen-chief --status <id>   print the message's status (queued, held,
#                             delivered, dropped, answered)
#   --json                    print {"id": ..., "session": ...} / {"status": ...}
#
# chief is the one live, unarchived session named "chief"; none or several is
# an error. agent-manager queues the message until chief is at rest. The panel
# (`zen-inbox watch`, key `m`) runs this same script.
#
# Exit: 0 ok, 1 no chief / several / agent-manager refused, 2 usage.

from __future__ import annotations

import argparse
import importlib.util
import json
import subprocess
import sys
from pathlib import Path

SENDER = "zenha"
SENDER_REFUSED = "a sender name is for a caller with no session"
NAME = "chief"


def _load_sessions():
    path = Path(__file__).resolve().parent / "lib" / "sessions.py"
    spec = importlib.util.spec_from_file_location("zen_sessions", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules["zen_sessions"] = module
    spec.loader.exec_module(module)
    return module


_sessions = _load_sessions()


class ChiefError(Exception):
    pass


def resolve(sessions: list[dict[str, object]] | None) -> str:
    """The id of the one live chief session."""
    if sessions is None:
        raise ChiefError("could not list sessions (agent-manager failed)")
    live = [s for s in sessions if s.get("name") == NAME and _sessions.is_live(s)]
    if not live:
        raise ChiefError(f"no live session named {NAME!r}")
    if len(live) > 1:
        ids = ", ".join(str(s.get("id")) for s in live)
        raise ChiefError(f"{len(live)} live sessions named {NAME!r}: {ids}")
    return str(live[0]["id"])


def _run(*args: str) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(
            ["agent-manager", *args], capture_output=True, text=True, check=False
        )
    except FileNotFoundError:
        raise ChiefError("agent-manager not found") from None


def _error_text(r: subprocess.CompletedProcess[str]) -> str:
    return (r.stderr or r.stdout).strip() or f"agent-manager exited {r.returncode}"


def send(message: str) -> tuple[str, str]:
    """Queue message for chief; (message id, session id)."""
    session = resolve(_sessions.list_sessions())
    argv = ["send", session, message, "--json"]
    r = _run(*argv, "--from", SENDER)
    if r.returncode != 0 and SENDER_REFUSED in r.stderr:
        r = _run(*argv)
    if r.returncode != 0:
        raise ChiefError(_error_text(r))
    try:
        data = json.loads(r.stdout or "{}")
    except json.JSONDecodeError:
        data = {}
    if isinstance(data, dict):
        if data.get("error") or data.get("refused"):
            raise ChiefError(str(data.get("error") or data.get("refused")))
        mid = data.get("message_id", data.get("id"))
        if mid is not None:
            return str(mid), session
    raise ChiefError("agent-manager send printed no message id")


def status(message_id: str) -> str:
    r = _run("message-status", message_id, "--json")
    if r.returncode != 0:
        raise ChiefError(_error_text(r))
    try:
        data = json.loads(r.stdout)
    except json.JSONDecodeError:
        raise ChiefError("message-status printed no JSON") from None
    value = data.get("status") if isinstance(data, dict) else None
    if not isinstance(value, str) or not value:
        raise ChiefError("message-status printed no status")
    return value


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(prog="zen-chief", description="Message the live chief.")
    p.add_argument("message", nargs="?", help="text to send")
    p.add_argument("--status", metavar="ID", help="status of a message sent earlier")
    p.add_argument("--json", action="store_true", help="JSON output")
    args = p.parse_args(argv)
    if (args.message is None) == (args.status is None):
        p.error("give a message, or --status ID")
    try:
        if args.status is not None:
            value = status(args.status)
            print(json.dumps({"status": value}) if args.json else value)
        else:
            if not args.message.strip():
                p.error("message is empty")
            mid, session = send(args.message)
            print(json.dumps({"id": mid, "session": session}) if args.json else mid)
    except ChiefError as e:
        print(f"zen-chief: {e}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
