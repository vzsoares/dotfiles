#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/chief.py, with agent-manager stubbed on PATH.

Run with:  uv run scripts/test_chief.py
"""

from __future__ import annotations

import importlib.util
import json
import stat
import sys
from pathlib import Path

import pytest

_spec = importlib.util.spec_from_file_location(
    "chief", Path(__file__).parent / "chief.py"
)
assert _spec is not None and _spec.loader is not None
chief = importlib.util.module_from_spec(_spec)
sys.modules["chief"] = chief
_spec.loader.exec_module(chief)

STUB = """#!/usr/bin/env python3
import json, os, sys
d = os.environ["FAKE_AM"]
args = sys.argv[1:]
with open(os.path.join(d, "calls"), "a") as f:
    f.write(json.dumps(args) + "\\n")
if args[0] == "sessions":
    print(open(os.path.join(d, "sessions.json")).read())
elif args[0] == "send":
    if os.path.exists(os.path.join(d, "refuse_from")) and "--from" in args:
        sys.stderr.write("a sender name is for a caller with no session\\n")
        sys.exit(1)
    print('{"message_id": 77}')
elif args[0] == "message-status":
    print('{"status": "delivered"}')
"""


def session(id: str, name: str = "chief", **kw: object) -> dict[str, object]:
    return {"id": id, "name": name, "status": "idle", "running": True, **kw}


@pytest.fixture
def am(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    stub = bin_dir / "agent-manager"
    stub.write_text(STUB)
    stub.chmod(stub.stat().st_mode | stat.S_IEXEC)
    monkeypatch.setenv("PATH", f"{bin_dir}:{__import__('os').environ['PATH']}")
    monkeypatch.setenv("FAKE_AM", str(tmp_path))

    def set_sessions(*sessions: dict[str, object]) -> Path:
        (tmp_path / "sessions.json").write_text(json.dumps(list(sessions)))
        return tmp_path

    def calls() -> list[list[str]]:
        raw = (tmp_path / "calls").read_text().splitlines()
        return [json.loads(c) for c in raw]

    set_sessions.calls = calls  # type: ignore[attr-defined]
    set_sessions.dir = tmp_path  # type: ignore[attr-defined]
    return set_sessions


def test_resolve_one_live_chief() -> None:
    assert chief.resolve([session("a"), session("b", "other")]) == "a"


def test_resolve_ignores_archived_dead_and_stopped_chiefs() -> None:
    sessions = [
        session("old", archived=True),
        session("dead", status="dead"),
        session("off", running=False),
        session("live"),
    ]
    assert chief.resolve(sessions) == "live"


def test_resolve_zero_chiefs() -> None:
    with pytest.raises(chief.ChiefError, match="no live session named 'chief'"):
        chief.resolve([session("a", "other"), session("b", archived=True)])


def test_resolve_two_chiefs_lists_both() -> None:
    with pytest.raises(chief.ChiefError, match="2 live sessions.*a, b"):
        chief.resolve([session("a"), session("b")])


def test_resolve_unreadable_sessions() -> None:
    with pytest.raises(chief.ChiefError, match="could not list"):
        chief.resolve(None)


def test_send_targets_the_chief_and_returns_the_message_id(am, capsys) -> None:
    am(session("c1"), session("x", "other"))
    assert chief.main(["hello\nworld"]) == 0
    assert capsys.readouterr().out == "77\n"
    send = [c for c in am.calls() if c[0] == "send"]
    assert send == [["send", "c1", "hello\nworld", "--json", "--from", "zenha"]]


def test_message_starting_with_a_dash(am, capsys) -> None:
    am(session("c1"))
    assert chief.main(["--", "-x flag-like"]) == 0
    send = [c for c in am.calls() if c[0] == "send"]
    assert send[0][2] == "-x flag-like"


def test_send_drops_from_when_agent_manager_refuses_it(am, capsys) -> None:
    am(session("c1"))
    (am.dir / "refuse_from").write_text("1")
    assert chief.main(["hi"]) == 0
    sends = [c for c in am.calls() if c[0] == "send"]
    assert [("--from" in c) for c in sends] == [True, False]


def test_send_without_chief_exits_1_and_sends_nothing(am, capsys) -> None:
    am(session("x", "other"))
    assert chief.main(["hi"]) == 1
    assert "no live session" in capsys.readouterr().err
    assert not [c for c in am.calls() if c[0] == "send"]


def test_status(am, capsys) -> None:
    am()
    assert chief.main(["--status", "77"]) == 0
    assert capsys.readouterr().out == "delivered\n"


def test_usage_errors() -> None:
    for argv in ([], ["a", "--status", "1"], ["  "]):
        with pytest.raises(SystemExit) as e:
            chief.main(argv)
        assert e.value.code == 2


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q", *sys.argv[1:]]))
