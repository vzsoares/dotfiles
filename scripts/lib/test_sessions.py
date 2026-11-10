#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/lib/sessions.py.

Run with:  uv run scripts/lib/test_sessions.py
"""

from __future__ import annotations

import importlib.util
import os
import sys
from pathlib import Path

import pytest

_spec = importlib.util.spec_from_file_location(
    "zen_sessions", Path(__file__).parent / "sessions.py"
)
assert _spec is not None and _spec.loader is not None
sessions = importlib.util.module_from_spec(_spec)
sys.modules["zen_sessions"] = sessions
_spec.loader.exec_module(sessions)


def _s(sid: str, name: str = "w", **extra: object) -> dict[str, object]:
    return {"id": sid, "name": name, "status": "idle", "running": True, **extra}


@pytest.fixture
def fake_am(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    data = tmp_path / "am"
    data.mkdir()
    script = bin_dir / "agent-manager"
    script.write_text('#!/usr/bin/env bash\ncat "$FAKE_AM/$1.json"\n')
    script.chmod(0o755)
    monkeypatch.setenv("PATH", f"{bin_dir}{os.pathsep}{os.environ['PATH']}")
    monkeypatch.setenv("FAKE_AM", str(data))
    return data


def test_list_sessions_parses_list_and_drops_junk(fake_am: Path) -> None:
    (fake_am / "sessions.json").write_text('[{"id": "a"}, 3, "x", null, {"id": "b"}]')
    assert sessions.list_sessions() == [{"id": "a"}, {"id": "b"}]


@pytest.mark.parametrize("body", ["not json", '{"id": "a"}', ""])
def test_list_sessions_tolerates_bad_output(fake_am: Path, body: str) -> None:
    (fake_am / "sessions.json").write_text(body)
    assert sessions.list_sessions() == []


def test_list_sessions_none_when_manager_fails(fake_am: Path) -> None:
    assert sessions.list_sessions() is None


def test_list_sessions_none_when_manager_missing(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("PATH", str(tmp_path))
    assert sessions.list_sessions() is None


def test_find_session_id_beats_name() -> None:
    by_name = _s("x1", name="target")
    by_id = _s("target", name="other")
    assert sessions.find_session("target", [by_name, by_id]) is by_id


def test_find_session_live_name_beats_archived() -> None:
    old = _s("old", archived=True)
    live = _s("new")
    assert sessions.find_session("w", [old, live]) is live
    assert sessions.find_session("w", [live, old]) is live


def test_find_session_falls_back_to_archived() -> None:
    old = _s("old", archived=True)
    assert sessions.find_session("w", [old]) is old
    assert sessions.find_session("ghost", [old]) is None


@pytest.mark.parametrize(
    ("session", "live"),
    [
        (_s("a", archived=True), False),
        (_s("a", running=False), False),
        (_s("a", status="dead"), False),
        (_s("a", status="errored"), False),
        (_s("a", status="working"), True),
        (_s("a", status="idle"), True),
        ({"id": "a", "status": "idle"}, True),
    ],
)
def test_is_live(session: dict[str, object], live: bool) -> None:
    assert sessions.is_live(session) is live


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
