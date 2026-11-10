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
import threading
import time
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


def test_await_refused_wait_is_dead(projects: Path, fake_am: Path) -> None:
    s = _session("/repo")
    (fake_am / "wait.json").write_text("")
    (fake_am / "sessions.json").write_text(json.dumps([{**s, "archived": True}]))
    ev = zen_await.await_sessions([s], timeout=5, root=projects, poll=0.01)
    assert (ev.state, ev.exit_code) == ("dead", 20)


def test_await_refusal_from_live_session_keeps_waiting(
    projects: Path, fake_am: Path
) -> None:
    s = _session("/repo", status="working")
    (fake_am / "wait.json").write_text("")
    (fake_am / "sessions.json").write_text(json.dumps([s]))
    ev = zen_await.await_sessions([s], timeout=0.3, root=projects, poll=0.01)
    assert ev.state == "timeout"


def _reached(fake_am: Path, session: dict[str, object] | None) -> None:
    body: dict[str, object] = {"reached": True}
    if session is not None:
        body["session"] = session
    (fake_am / "wait.json").write_text(json.dumps(body))


def test_fresh_worker_keeps_waiting_until_reply(
    projects: Path, fake_am: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = _session("/repo", status="idle")
    monkeypatch.setattr(zen_await, "STARTUP_GRACE", 30)
    _reached(fake_am, s)
    timer = threading.Timer(
        0.4, _write_transcript, (projects, s, [_assistant("ok\nWORKER_DONE 571")])
    )
    timer.start()
    started = time.monotonic()
    ev = zen_await.await_sessions([s], timeout=10, root=projects, poll=0.01)
    timer.join()
    assert (ev.state, ev.task_id, ev.exit_code) == ("done", "571", 0)
    assert time.monotonic() - started >= 0.4


def test_fresh_worker_without_reply_goes_idle_after_grace(
    projects: Path, fake_am: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = _session("/repo", status="idle")
    monkeypatch.setattr(zen_await, "STARTUP_GRACE", 0.3)
    _reached(fake_am, s)
    ev = zen_await.await_sessions([s], timeout=10, root=projects, poll=0.01)
    assert (ev.state, ev.exit_code) == ("idle", 11)
    assert ev.summary.startswith("no reply yet after")


def test_fresh_worker_waiting_is_idle_at_once(
    projects: Path, fake_am: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = _session("/repo", status="waiting")
    monkeypatch.setattr(zen_await, "STARTUP_GRACE", 30)
    _reached(fake_am, s)
    ev = zen_await.await_sessions([s], timeout=10, root=projects, poll=0.01)
    assert (ev.state, ev.exit_code) == ("idle", 11)
    assert ev.summary.startswith("waiting for input")


def test_fresh_worker_dead_returns_at_once(
    projects: Path, fake_am: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = _session("/repo", status="errored")
    monkeypatch.setattr(zen_await, "STARTUP_GRACE", 30)
    _reached(fake_am, s)
    ev = zen_await.await_sessions([s], timeout=10, root=projects, poll=0.01)
    assert ev.exit_code == 20


@pytest.mark.parametrize(
    "current",
    [
        {"status": "dead"},
        {"status": "errored"},
        {"running": False},
        {"archived": True},
        None,
    ],
    ids=["dead", "errored", "not-running", "archived", "missing-twice"],
)
def test_wait_without_session_rereads_dead(
    projects: Path, fake_am: Path, current: dict[str, object] | None
) -> None:
    s = _session("/repo", status="working")
    _write_transcript(projects, s, [_assistant("half way")])
    _reached(fake_am, None)
    other = _session("/other", sid="other1")
    listed = [other] if current is None else [{**s, **current}]
    (fake_am / "sessions.json").write_text(json.dumps(listed))
    ev = zen_await.await_sessions([s], timeout=5, root=projects, poll=0.01)
    assert (ev.state, ev.exit_code) == ("dead", 20)


@pytest.mark.parametrize("body", [None, "", "[]", "not json", '{"id": "x"}'])
def test_wait_without_session_unreadable_list_keeps_waiting(
    projects: Path, fake_am: Path, body: str | None
) -> None:
    s = _session("/repo", status="working")
    _write_transcript(projects, s, [_assistant("half way")])
    _reached(fake_am, None)
    if body is not None:
        (fake_am / "sessions.json").write_text(body)
    ev = zen_await.await_sessions([s], timeout=0.5, root=projects, poll=0.01)
    assert (ev.state, ev.exit_code) == ("timeout", 124)


def test_wait_without_session_retries_until_readable(
    projects: Path, fake_am: Path
) -> None:
    s = _session("/repo", status="working")
    _write_transcript(projects, s, [_assistant("half way")])
    _reached(fake_am, None)
    timer = threading.Timer(
        0.08,
        (fake_am / "sessions.json").write_text,
        (json.dumps([{**s, "status": "dead"}]),),
    )
    timer.start()
    ev = zen_await.await_sessions([s], timeout=5, root=projects, poll=0.01)
    timer.join()
    assert (ev.state, ev.exit_code) == ("dead", 20)


def test_wait_without_session_one_miss_is_not_dead(
    projects: Path, fake_am: Path
) -> None:
    s = _session("/repo", status="working")
    other = _session("/other", sid="other1")
    _write_transcript(projects, s, [_assistant("half way")])
    _reached(fake_am, None)
    (fake_am / "sessions.json").write_text(json.dumps([other]))
    timer = threading.Timer(
        0.02,
        (fake_am / "sessions.json").write_text,
        (json.dumps([other, {**s, "status": "idle"}]),),
    )
    timer.start()
    ev = zen_await.await_sessions([s], timeout=5, root=projects, poll=0.01)
    timer.join()
    assert (ev.state, ev.exit_code) == ("idle", 11)


def test_wait_without_session_dead_with_marker_is_done(
    projects: Path, fake_am: Path
) -> None:
    s = _session("/repo", status="working")
    _write_transcript(projects, s, [_assistant("WORKER_DONE 571")])
    _reached(fake_am, None)
    (fake_am / "sessions.json").write_text(json.dumps([{**s, "archived": True}]))
    ev = zen_await.await_sessions([s], timeout=5, root=projects, poll=0.01)
    assert (ev.state, ev.exit_code) == ("done", 0)


def test_wait_without_session_uses_fresh_status(projects: Path, fake_am: Path) -> None:
    s = _session("/repo", status="working")
    _write_transcript(projects, s, [_assistant("stopped, no marker")])
    _reached(fake_am, None)
    (fake_am / "sessions.json").write_text(json.dumps([{**s, "status": "idle"}]))
    ev = zen_await.await_sessions([s], timeout=5, root=projects, poll=0.01)
    assert (ev.state, ev.exit_code) == ("idle", 11)


@pytest.fixture
def summary_env(
    fake_am: Path, monkeypatch: pytest.MonkeyPatch, projects: Path
) -> list[dict[str, object]]:
    done = _session("/a", sid="aaa")
    dead = _session("/b", status="errored", sid="bbb")
    idle = _session("/c", sid="ccc")
    _write_transcript(projects, done, [_assistant("SPEC_READY 5")])
    _write_transcript(projects, dead, [_assistant("half way")])
    _write_transcript(projects, idle, [_assistant("stopped")])
    (fake_am / "sessions.json").write_text(json.dumps([done, dead, idle]))
    real = zen_await.event_for
    monkeypatch.setattr(zen_await, "event_for", lambda sess: real(sess, projects))
    return [done, dead, idle]


def test_cli_summary_and_unknown(summary_env: list[dict[str, object]]) -> None:
    runner = CliRunner()
    ok = runner.invoke(zen_await.app, ["--summary", "worker-aaa", "--json"])
    assert ok.exit_code == 0
    assert json.loads(ok.stdout) == {
        "session": "worker-aaa",
        "task_id": "5",
        "state": "done",
        "summary": "SPEC_READY 5",
    }

    missing = runner.invoke(zen_await.app, ["--summary", "ghost"])
    assert missing.exit_code == 2


def test_cli_summary_reports_every_session(
    summary_env: list[dict[str, object]],
) -> None:
    runner = CliRunner()
    out = runner.invoke(
        zen_await.app, ["--summary", "worker-ccc", "worker-aaa", "worker-bbb", "--json"]
    )
    states = [json.loads(line)["state"] for line in out.stdout.splitlines()]
    assert states == ["idle", "done", "dead"]
    assert out.exit_code == 20

    text = runner.invoke(zen_await.app, ["--summary", "worker-aaa", "worker-ccc"])
    assert [line.split(" · ")[0] for line in text.stdout.splitlines()] == [
        "worker-aaa",
        "worker-ccc",
    ]
    assert text.exit_code == 11


@pytest.mark.parametrize("body", ["not json", '{"id": "a"}'])
def test_cli_bad_sessions_json_is_a_manager_failure(fake_am: Path, body: str) -> None:
    (fake_am / "sessions.json").write_text(body)
    out = CliRunner().invoke(zen_await.app, ["--summary", "worker-aaa"])
    assert out.exit_code == 2
    assert "agent-manager sessions failed" in out.output
    assert "unknown session" not in out.output


def test_cli_summary_unknown_among_several(
    summary_env: list[dict[str, object]],
) -> None:
    out = CliRunner().invoke(zen_await.app, ["--summary", "worker-aaa", "ghost"])
    assert out.exit_code == 2
    assert out.stdout == ""


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
