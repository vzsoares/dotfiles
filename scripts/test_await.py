#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "typer>=0.12",
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/await.py.

Run with:  uv run scripts/test_await.py
"""

from __future__ import annotations

import importlib.util
import json
import os
import sys
from pathlib import Path

import pytest
import typer
from typer.testing import CliRunner

_spec = importlib.util.spec_from_file_location(
    "zen_await", Path(__file__).parent / "await.py"
)
assert _spec is not None and _spec.loader is not None
zen_await = importlib.util.module_from_spec(_spec)
sys.modules["zen_await"] = zen_await
_spec.loader.exec_module(zen_await)


# --------------------------------------------------------------------------- #
# Fixtures                                                                      #
# --------------------------------------------------------------------------- #


def _assistant(
    text: str, stop: str = "end_turn", msg_id: str = "m1", uuid: str = "u1"
) -> str:
    return json.dumps(
        {
            "type": "assistant",
            "uuid": uuid,
            "message": {
                "id": msg_id,
                "stop_reason": stop,
                "content": [{"type": "text", "text": text}],
            },
        }
    )


def _session(
    directory: str, status: str = "finished", sid: str = "abc123"
) -> dict[str, object]:
    return {
        "id": sid,
        "name": f"worker-{sid}",
        "directory": directory,
        "status": status,
        "agent_session_id": f"agent-{sid}",
    }


@pytest.fixture
def projects(tmp_path: Path) -> Path:
    root = tmp_path / "projects"
    root.mkdir()
    return root


def _write_transcript(root: Path, session: dict[str, object], lines: list[str]) -> Path:
    path: Path | None = zen_await.transcript_path(session, root)
    assert path is not None
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("\n".join(lines) + "\n")
    return path


@pytest.fixture
def fake_am(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """An `agent-manager` on PATH that prints $FAKE_AM/<subcommand>.json."""
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


# --------------------------------------------------------------------------- #
# classify                                                                      #
# --------------------------------------------------------------------------- #


@pytest.mark.parametrize(
    ("text", "state", "task"),
    [
        ("all green\n\nWORKER_DONE 568", "done", "568"),
        ("spec written. SPEC_READY #42", "done", "42"),
        (
            "failing: tests · tried: x · cause: y · needs: decision on #9",
            "escalation",
            "9",
        ),
        (
            "Failing — e2e\nTried — retry\nCause — flake\nNeeds — a human",
            "escalation",
            None,
        ),
        ("I made some progress on task 12", "idle", "12"),
        ("", "idle", None),
    ],
)
def test_classify(text: str, state: str, task: str | None) -> None:
    assert zen_await.classify(text) == (state, task)


def test_classify_last_marker_wins() -> None:
    assert zen_await.classify("SPEC_READY 1 then WORKER_DONE 2") == ("done", "2")


def test_escalation_needs_all_words() -> None:
    assert not zen_await.is_escalation("failing and tried, no cause")


@pytest.mark.parametrize(
    ("value", "seconds"),
    [("90s", 90), ("30m", 1800), ("4h", 14400), ("1h30m", 5400), ("15", 15)],
)
def test_parse_duration(value: str, seconds: float) -> None:
    assert zen_await.parse_duration(value) == seconds


@pytest.mark.parametrize("value", ["", "4x", "h", "1h junk"])
def test_parse_duration_rejects(value: str) -> None:
    with pytest.raises(typer.BadParameter):
        zen_await.parse_duration(value)


def test_summarize_keeps_tail() -> None:
    text = "x" * 3000 + "WORKER_DONE 1"
    out = zen_await.summarize(text)
    assert out.endswith("WORKER_DONE 1")
    assert len(out) <= zen_await.SUMMARY_MAX + 1


# --------------------------------------------------------------------------- #
# transcripts                                                                   #
# --------------------------------------------------------------------------- #


def test_project_slug() -> None:
    assert (
        zen_await.project_slug("/Users/me/code/dot-files_x.y")
        == "-Users-me-code-dot-files-x-y"
    )


def test_last_end_turn_skips_tool_use(projects: Path) -> None:
    s = _session("/repo")
    path = _write_transcript(
        projects,
        s,
        [
            _assistant("first", uuid="a", msg_id="m1"),
            _assistant("working", stop="tool_use", uuid="b", msg_id="m2"),
            "not json",
        ],
    )
    assert zen_await.last_end_turn(path) == ("a", "first")


def test_last_end_turn_joins_blocks_of_one_message(projects: Path) -> None:
    s = _session("/repo")
    path = _write_transcript(
        projects,
        s,
        [
            _assistant("part one", stop="", uuid="a", msg_id="m9"),
            _assistant("WORKER_DONE 7", uuid="b", msg_id="m9"),
        ],
    )
    assert zen_await.last_end_turn(path) == ("b", "part one\nWORKER_DONE 7")


def test_last_end_turn_ignores_sidechain(projects: Path) -> None:
    s = _session("/repo")
    side = json.loads(_assistant("subagent", uuid="z", msg_id="m2"))
    side["isSidechain"] = True
    path = _write_transcript(
        projects, s, [_assistant("main", uuid="a"), json.dumps(side)]
    )
    assert zen_await.last_end_turn(path)[1] == "main"


def test_last_end_turn_missing_file(tmp_path: Path) -> None:
    assert zen_await.last_end_turn(tmp_path / "nope.jsonl") == ("", "")


# --------------------------------------------------------------------------- #
# event_for                                                                     #
# --------------------------------------------------------------------------- #


def test_event_for_done(projects: Path) -> None:
    s = _session("/repo")
    _write_transcript(projects, s, [_assistant("ok\nWORKER_DONE 568")])
    ev = zen_await.event_for(s, projects)
    assert (ev.state, ev.task_id, ev.exit_code) == ("done", "568", 0)


def test_event_for_dead_without_marker(projects: Path) -> None:
    s = _session("/repo", status="errored")
    _write_transcript(projects, s, [_assistant("half way")])
    assert zen_await.event_for(s, projects).exit_code == 20


def test_event_for_waiting_is_idle(projects: Path) -> None:
    s = _session("/repo", status="waiting")
    _write_transcript(projects, s, [_assistant("WORKER_DONE 3")])
    ev = zen_await.event_for(s, projects)
    assert (ev.state, ev.exit_code) == ("idle", 11)
    assert ev.summary.startswith("waiting for input")


# --------------------------------------------------------------------------- #
# await_sessions (fake agent-manager)                                           #
# --------------------------------------------------------------------------- #


def test_await_returns_on_reached(projects: Path, fake_am: Path) -> None:
    s = _session("/repo")
    _write_transcript(projects, s, [_assistant("failing a tried b cause c needs d")])
    (fake_am / "wait.json").write_text(json.dumps({"reached": True, "session": s}))
    ev = zen_await.await_sessions([s], timeout=10, root=projects, poll=0.01)
    assert (ev.state, ev.exit_code) == ("escalation", 10)


def test_await_times_out(projects: Path, fake_am: Path) -> None:
    s = _session("/repo")
    (fake_am / "wait.json").write_text(
        json.dumps({"reached": False, "outcome": "timeout"})
    )
    ev = zen_await.await_sessions([s], timeout=0.3, root=projects, poll=0.01)
    assert (ev.state, ev.exit_code) == ("timeout", 124)


def test_await_new_ignores_stale_message(projects: Path, fake_am: Path) -> None:
    s = _session("/repo")
    _write_transcript(projects, s, [_assistant("WORKER_DONE 1")])
    (fake_am / "wait.json").write_text(json.dumps({"reached": True, "session": s}))
    ev = zen_await.await_sessions(
        [s], timeout=0.3, new_only=True, root=projects, poll=0.01
    )
    assert ev.state == "timeout"


def test_cli_summary_and_unknown(
    fake_am: Path, monkeypatch: pytest.MonkeyPatch, projects: Path
) -> None:
    s = _session("/repo")
    _write_transcript(projects, s, [_assistant("SPEC_READY 5")])
    (fake_am / "sessions.json").write_text(json.dumps([s]))
    real = zen_await.event_for
    monkeypatch.setattr(zen_await, "event_for", lambda sess: real(sess, projects))
    runner = CliRunner()

    ok = runner.invoke(zen_await.app, ["--summary", "worker-abc123", "--json"])
    assert ok.exit_code == 0
    assert json.loads(ok.stdout) == {
        "session": "worker-abc123",
        "task_id": "5",
        "state": "done",
        "summary": "SPEC_READY 5",
    }

    missing = runner.invoke(zen_await.app, ["--summary", "ghost"])
    assert missing.exit_code == 2


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
