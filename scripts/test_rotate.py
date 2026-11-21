#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/rotate.py; agent-manager, tmux, zen-inbox and gitleaks are fakes.

Run with:  uv run scripts/test_rotate.py
"""

from __future__ import annotations

import fcntl
import importlib.util
import json
import os
import shutil
import stat
import sys
from datetime import UTC, datetime
from pathlib import Path

import pytest

_spec = importlib.util.spec_from_file_location(
    "rotate", Path(__file__).parent / "rotate.py"
)
assert _spec is not None and _spec.loader is not None
rotate = importlib.util.module_from_spec(_spec)
sys.modules["rotate"] = rotate
_spec.loader.exec_module(rotate)

NOW = datetime(2026, 11, 21, 12, 0, 0, tzinfo=UTC).timestamp()
HOUR = 3600.0

# One script under four names; $FAKE holds its inputs and the recorded calls.
STUB = """#!/usr/bin/env python3
import json, os, sys
d = os.environ["FAKE"]
name, args = os.path.basename(sys.argv[0]), sys.argv[1:]
has = lambda f: os.path.exists(os.path.join(d, f))
read = lambda f: open(os.path.join(d, f)).read()
with open(os.path.join(d, "calls"), "a") as f:
    f.write(json.dumps([name, *args]) + "\\n")
if name == "agent-manager":
    if args[0] == "sessions":
        after = has("cleared") and has("sessions_after.json")
        print(read("sessions_after.json" if after else "sessions.json"))
    elif args[0] == "send":
        if has("refuse_from") and "--from" in args:
            sys.stderr.write("a sender name is for a caller with no session\\n")
            sys.exit(1)
        sys.exit(1 if has("send_fail") else 0)
elif name == "tmux":
    if "capture-pane" in args:
        if has("tmux_fail"):
            sys.exit(1)
        sys.stdout.write(read("screen"))
    elif "send-keys" in args:
        markers = [f for f in os.listdir(os.environ["ZEN_ROTATE_STATE"]) if f.startswith("clearing-")]
        open(os.path.join(d, "markers_at_clear"), "w").write(",".join(markers))
        open(os.path.join(d, "cleared"), "w").close()
elif name == "zen-inbox":
    if has("approvals"):
        sys.stdout.write(read("approvals"))
        sys.exit(0)
    sys.exit(int(read("inbox_exit")) if has("inbox_exit") else 1)
elif name == "gitleaks":
    sys.exit(3 if "PLANTED_SECRET" in open(args[1]).read() else 0)
"""

RULE = "─" * 40
EMPTY_SCREEN = f"some output\n\n{RULE}\n❯ \n{RULE}\n  status line\n"
TYPED_SCREEN = f"some output\n\n{RULE}\n❯ half a thought\n{RULE}\n  status line\n"
DIALOG_SCREEN = f"Do you want to proceed?\n❯ 1. Yes\n  2. No\n\n{RULE}\n"


class Env:
    def __init__(self, tmp: Path) -> None:
        self.tmp = tmp
        self.fake = tmp / "fake"
        self.home = tmp / "home"
        self.state = tmp / "state"
        self.config = tmp / "xdg" / "zen" / "config.json"
        for d in (self.fake, self.home, self.state, self.config.parent):
            d.mkdir(parents=True)
        self.screen(EMPTY_SCREEN)

    def roles(self, **enabled: bool) -> None:
        roles = {
            "chief": {"enabled": enabled.get("chief", False), "quiet_minutes": 10},
            "orchestrator": {
                "enabled": enabled.get("orchestrator", False),
                "quiet_minutes": 2,
            },
        }
        self.config.write_text(json.dumps({"rotate": {"roles": roles}}))

    def sessions(self, *sessions: dict[str, object], after: bool = False) -> None:
        name = "sessions_after.json" if after else "sessions.json"
        (self.fake / name).write_text(json.dumps(list(sessions)))

    def screen(self, text: str) -> None:
        (self.fake / "screen").write_text(text)

    def flag(self, name: str, body: str = "") -> None:
        (self.fake / name).write_text(body)

    def unflag(self, name: str) -> None:
        (self.fake / name).unlink(missing_ok=True)

    def calls(self, *names: str) -> list[list[str]]:
        path = self.fake / "calls"
        raw = path.read_text().splitlines() if path.exists() else []
        calls = [json.loads(c) for c in raw]
        return [c for c in calls if not names or c[0] in names]

    def sends(self) -> list[list[str]]:
        """Each send's argv, without `--from zen-rotate` and its plain retry."""
        sends = [c[1:] for c in self.calls("agent-manager") if c[1] == "send"]
        return [s[:-2] for s in sends if s[-2:] == ["--from", "zen-rotate"]]

    def forget(self) -> None:
        (self.fake / "calls").unlink(missing_ok=True)

    def transcript(self, session: dict[str, object]) -> Path:
        path = rotate._sessions.transcript_path(session)
        assert isinstance(path, Path)
        return path

    def write(
        self,
        session: dict[str, object],
        lines: list[str],
        age: float = 7 * HOUR,
        quiet: float = HOUR,
    ) -> Path:
        """A transcript started `age` ago and last written `quiet` ago."""
        path = self.transcript(session)
        path.parent.mkdir(parents=True, exist_ok=True)
        started = datetime.fromtimestamp(rotate.now() - age, UTC).isoformat()
        first = json.dumps({"type": "user", "timestamp": started, "message": {}})
        path.write_text("\n".join([first, *lines]) + "\n")
        mtime = rotate.now() - quiet
        os.utime(path, (mtime, mtime))
        return path

    def append(self, session: dict[str, object], *lines: str) -> None:
        path = self.transcript(session)
        with path.open("a") as f:
            f.write("\n".join(lines) + "\n")

    def cycle(self, key: str) -> dict[str, object]:
        return json.loads((self.state / "state.json").read_text())[key]

    def log(self) -> str:
        path = self.state / "rotate.log"
        return path.read_text() if path.exists() else ""


@pytest.fixture
def env(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Env:
    e = Env(tmp_path)
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    for name in ("agent-manager", "tmux", "zen-inbox", "gitleaks"):
        stub = bin_dir / name
        stub.write_text(STUB)
        stub.chmod(stub.stat().st_mode | stat.S_IEXEC)
    monkeypatch.setenv("PATH", f"{bin_dir}{os.pathsep}{os.environ['PATH']}")
    monkeypatch.setenv("FAKE", str(e.fake))
    monkeypatch.setenv("HOME", str(e.home))
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "xdg"))
    monkeypatch.setenv("ZEN_ROTATE_STATE", str(e.state))
    monkeypatch.setenv("ZEN_ROTATE_NOW", str(NOW))
    monkeypatch.setenv("ZEN_ROTATE_CLEAR_WAIT", "0.3")
    monkeypatch.setenv("ZEN_ROTATE_CLEAR_POLL", "0.05")
    e.roles(orchestrator=True)
    return e


def at(monkeypatch: pytest.MonkeyPatch, minutes: float) -> None:
    monkeypatch.setenv("ZEN_ROTATE_NOW", str(NOW + minutes * 60))


def orch(**extra: object) -> dict[str, object]:
    return {
        "id": "o1",
        "name": "orchestrator",
        "group": "chief",
        "tool": "claude",
        "directory": "/repo/dotfiles",
        "status": "finished",
        "running": True,
        "archived": False,
        "agent_session_id": "conv-1",
        **extra,
    }


def turn(text: str, uuid: str, stop: str = "end_turn") -> str:
    message = {
        "id": f"m-{uuid}",
        "stop_reason": stop,
        "content": [{"type": "text", "text": text}],
    }
    return json.dumps({"type": "assistant", "uuid": uuid, "message": message})


def tool_result(text: str) -> str:
    content = [{"type": "tool_result", "content": text}]
    return json.dumps({"type": "user", "message": {"content": content}})


def valid_note(body: str = "none") -> str:
    return "# Handoff\n\n" + "".join(
        f"## {title}\n{body}\n\n" for title, _ in rotate.SECTIONS
    )


def tick() -> int:
    return rotate.main(["tick"])


def request(env: Env, session: dict[str, object] | None = None) -> Path:
    """Tick a due, at-rest orchestrator into the requested phase; the note path."""
    session = session or orch()
    env.sessions(session)
    env.write(session, [turn("earlier work", "u0")])
    assert tick() == 0
    assert env.cycle("orchestrator")["phase"] == "requested"
    return Path(str(env.cycle("orchestrator")["note"]))


def ready(env: Env, note: Path, uuid: str = "u1") -> None:
    note.write_text(valid_note())
    env.append(orch(), turn(f"Note written.\n\n{rotate.READY}", uuid))


# --------------------------------------------------------------------------- #
# Due and at rest                                                               #
# --------------------------------------------------------------------------- #


def test_disabled_roles_run_no_command(env: Env) -> None:
    env.roles()
    env.sessions(orch())
    env.write(orch(), [])
    assert tick() == 0
    assert env.calls() == []


def test_tracked_config_ships_both_roles_off(
    env: Env, monkeypatch: pytest.MonkeyPatch
) -> None:
    env.config.unlink()
    config = rotate.load_config()
    assert config.hours == 6 and config.defer_minutes == 30
    assert {n: (r.enabled, r.quiet_minutes) for n, r in config.roles.items()} == {
        "chief": (False, 10),
        "orchestrator": (False, 2),
    }
    assert tick() == 0
    assert env.calls() == []


def test_not_due_sends_nothing(env: Env) -> None:
    env.sessions(orch())
    env.write(orch(), [], age=5 * HOUR)
    assert tick() == 0
    assert env.sends() == []
    assert env.calls("tmux", "zen-inbox") == []


def test_due_and_at_rest_requests_a_note(env: Env) -> None:
    note = request(env)
    assert note.parent == env.home / ".cache/zen/handoff/orchestrator"
    assert note.name == datetime.fromtimestamp(NOW).strftime("%Y%m%d-%H%M%S.md")
    assert stat.S_IMODE(note.parent.stat().st_mode) == 0o700
    (send,) = env.sends()
    assert send[:2] == ["send", "o1"] and len(send) == 3
    text = send[2]
    assert text.startswith("[zen-rotate] Restart request.")
    assert str(note) in text
    for title, _ in rotate.SECTIONS:
        assert f"`## {title}`" in text
    assert "TaskStop" in text and "CronDelete" in text
    assert rotate.READY in text and f"{rotate.DEFER} <reason>" in text
    assert "every live worker" in text and "zen-board check" not in text
    assert len(text.encode()) < 8000
    assert [
        "tmux",
        "-L",
        "agentmgr",
        "capture-pane",
        "-p",
        "-t",
        "am_o1",
    ] in env.calls()
    assert ["zen-inbox", "approval", "open"] in env.calls()


def test_send_inside_a_session_drops_the_sender_name(env: Env) -> None:
    env.flag("refuse_from")
    env.sessions(orch())
    env.write(orch(), [turn("earlier work", "u0")])
    tick()
    assert env.cycle("orchestrator")["phase"] == "requested"
    first, second = (c[1:] for c in env.calls("agent-manager") if c[1] == "send")
    assert first[-2:] == ["--from", "zen-rotate"] and second == first[:-2]


def test_chief_request_names_the_board_and_watchers(env: Env) -> None:
    text = rotate.request_text("chief", Path("/n.md"))
    assert "zen-board check" in text and "go #<id>" in text
    assert len(text.encode()) < 8000


@pytest.mark.parametrize("status", ["working", "waiting", "starting"])
def test_guard_status(env: Env, status: str) -> None:
    env.sessions(orch(status=status))
    env.write(orch(), [])
    tick()
    assert env.sends() == []
    assert env.cycle("orchestrator")["block"] == f"status is {status}"


def test_guard_dead_session_is_not_the_role(env: Env) -> None:
    env.sessions(orch(status="dead"))
    env.write(orch(), [])
    tick()
    assert env.sends() == []
    assert "no live session" in str(env.cycle("orchestrator")["block"])


def test_guard_session_outside_group_chief(env: Env) -> None:
    env.sessions(orch(group="me"))
    env.write(orch(), [])
    tick()
    assert env.sends() == []


def test_guard_two_live_sessions(env: Env) -> None:
    env.sessions(orch(), orch(id="o2"))
    env.write(orch(), [])
    tick()
    assert env.sends() == []
    assert "2 live sessions" in str(env.cycle("orchestrator")["block"])


def test_guard_quiet_time(env: Env) -> None:
    env.sessions(orch())
    env.write(orch(), [], quiet=60)
    tick()
    assert env.sends() == []
    assert "needs 2m quiet" in str(env.cycle("orchestrator")["block"])
    assert env.calls("tmux", "zen-inbox") == []


def test_guard_typed_text(env: Env) -> None:
    env.sessions(orch())
    env.write(orch(), [])
    env.screen(TYPED_SCREEN)
    tick()
    assert env.sends() == []
    assert env.cycle("orchestrator")["block"] == "text typed in the prompt"


def test_guard_open_dialog(env: Env) -> None:
    env.sessions(orch())
    env.write(orch(), [])
    env.screen(DIALOG_SCREEN)
    tick()
    assert env.sends() == []
    assert "dialog" in str(env.cycle("orchestrator")["block"])


def test_guard_unreadable_pane(env: Env) -> None:
    env.sessions(orch())
    env.write(orch(), [])
    env.flag("tmux_fail")
    tick()
    assert env.sends() == []
    assert env.cycle("orchestrator")["block"] == "pane unreadable"


def test_guard_approved_prod_action(env: Env) -> None:
    env.sessions(orch())
    env.write(orch(), [])
    env.flag("approvals", "932.5 approved push dotfiles master@0123456789ab\n")
    tick()
    assert env.sends() == []
    assert env.cycle("orchestrator")["block"] == "approved prod action open (932.5)"


def test_guard_inbox_failure_blocks(env: Env) -> None:
    env.sessions(orch())
    env.write(orch(), [])
    env.flag("inbox_exit", "4")
    tick()
    assert env.sends() == []
    assert "failed (exit 4)" in str(env.cycle("orchestrator")["block"])


def test_guard_backoff_and_paused_run_no_command(
    env: Env, monkeypatch: pytest.MonkeyPatch
) -> None:
    env.sessions(orch())
    env.write(orch(), [])
    state = {"orchestrator": {"backoff_until": NOW + 600}}
    (env.state / "state.json").write_text(json.dumps(state))
    tick()
    assert env.sends() == [] and env.calls("tmux", "zen-inbox") == []
    assert "backoff for 10m" in str(env.cycle("orchestrator")["block"])

    (env.state / "state.json").write_text(
        json.dumps({"orchestrator": {"paused": True}})
    )
    tick()
    assert env.sends() == [] and env.calls("tmux", "zen-inbox") == []
    assert "paused" in str(env.cycle("orchestrator")["block"])


@pytest.mark.parametrize(
    ("screen", "state"),
    [
        (EMPTY_SCREEN, "empty"),
        (TYPED_SCREEN, "text"),
        (DIALOG_SCREEN, "none"),
        (f"{RULE}\n❯ [Pasted text #1 +12 lines]\n{RULE}\n", "text"),
        (f"❯ old prompt in scrollback\nanswer\n{RULE}\n❯ \n{RULE}\n", "empty"),
        ("", "none"),
    ],
)
def test_prompt_state(screen: str, state: str) -> None:
    assert rotate.prompt_state(screen) == state


# --------------------------------------------------------------------------- #
# Ready, clear, resume                                                          #
# --------------------------------------------------------------------------- #


def test_ready_clears_and_resumes(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    note = request(env)
    ready(env, note)
    env.sessions(orch(agent_session_id="conv-2", status="idle"), after=True)
    env.forget()
    assert tick() == 0

    assert [c for c in env.calls("tmux") if "send-keys" in c] == [
        ["tmux", "-L", "agentmgr", "send-keys", "-t", "am_o1", "/clear", "Enter"]
    ]
    resume = env.sends()[-1]
    assert resume[:4] == ["send", "o1", "--saved", "orchestrator"]
    assert resume[4].startswith("[zen-rotate] You were restarted in place")
    assert str(note) in resume[4] and "First actions" in resume[4]
    assert len(env.sends()) == 1
    assert (env.fake / "markers_at_clear").read_text() == "clearing-o1"
    assert not list(env.state.glob("clearing-*"))
    assert stat.S_IMODE(note.stat().st_mode) == 0o600

    cycle = env.cycle("orchestrator")
    assert cycle["phase"] == "idle" and cycle["cancels"] == 0
    assert cycle["last_rotation"] == NOW and cycle["last_note"] == str(note)
    out = capsys.readouterr().out
    assert f"rotated orchestrator: 7h00m old, note {note}" in out


def test_ready_waits_while_the_turn_is_still_running(env: Env) -> None:
    request(env)
    env.sessions(orch(status="working"))
    env.forget()
    tick()
    assert env.calls("tmux") == [] and env.sends() == []
    assert env.cycle("orchestrator")["phase"] == "requested"


def test_ready_rechecks_rest_before_the_clear(env: Env) -> None:
    note = request(env)
    ready(env, note)
    env.screen(TYPED_SCREEN)
    env.forget()
    tick()
    assert not [c for c in env.calls("tmux") if "send-keys" in c]
    assert env.cycle("orchestrator")["block"] == "text typed in the prompt"
    assert env.cycle("orchestrator")["phase"] == "requested"


def test_ready_rereads_the_status_before_the_clear(
    env: Env, monkeypatch: pytest.MonkeyPatch
) -> None:
    note = request(env)
    ready(env, note)
    listings = iter([[orch()], [orch(status="working")]])
    monkeypatch.setattr(rotate, "need_sessions", lambda: next(listings))
    env.forget()
    tick()
    assert not [c for c in env.calls("tmux") if "send-keys" in c]
    assert env.cycle("orchestrator")["block"] == "status is working"
    assert env.cycle("orchestrator")["phase"] == "requested"


def test_failed_rerequest_send_is_not_a_miss(env: Env) -> None:
    request(env)
    env.append(orch(), turn("Done.", "u1"))
    env.flag("send_fail")
    tick()
    cycle = env.cycle("orchestrator")
    assert cycle["misses"] == 0 and cycle["phase"] == "requested"
    env.unflag("send_fail")
    env.forget()
    tick()
    (again,) = env.sends()
    assert "did not end with HANDOFF_READY" in again[2]
    assert env.cycle("orchestrator")["misses"] == 1


def test_ready_ignores_quiet_time(env: Env) -> None:
    note = request(env)
    ready(env, note)
    env.sessions(orch(agent_session_id="conv-2"), after=True)
    tick()
    assert env.cycle("orchestrator")["last_note"] == str(note)


def test_pending_background_blocks_the_clear(env: Env) -> None:
    note = request(env)
    env.append(orch(), tool_result("Command running in background with ID: bg7"))
    ready(env, note)
    env.forget()
    tick()
    assert not [c for c in env.calls("tmux") if "send-keys" in c]
    (again,) = env.sends()
    assert "background runs are still running: bg7" in again[2]
    assert "(request 2 of 3)" in again[2]


def test_stopped_background_run_does_not_block(env: Env) -> None:
    note = request(env)
    env.append(
        orch(),
        tool_result("Command running in background with ID: bg7"),
        tool_result('{"message":"Successfully stopped task: bg7"}'),
    )
    ready(env, note)
    env.sessions(orch(agent_session_id="conv-2"), after=True)
    tick()
    assert env.cycle("orchestrator")["last_note"] == str(note)


def test_defer_backs_off_without_a_cancel(
    env: Env, monkeypatch: pytest.MonkeyPatch
) -> None:
    request(env)
    env.append(
        orch(), turn(f"A merge is half done.\n{rotate.DEFER} merge of #931", "u1")
    )
    env.forget()
    tick()
    cycle = env.cycle("orchestrator")
    assert cycle["phase"] == "idle" and cycle["cancels"] == 0
    assert cycle["backoff_until"] == NOW + 30 * 60
    assert env.sends() == []
    at(monkeypatch, 29)
    tick()
    assert env.sends() == []
    at(monkeypatch, 31)
    env.write(orch(), [turn("x", "u0")], age=8 * HOUR)
    tick()
    assert len(env.sends()) == 1


def test_no_marker_asks_again_then_cancels(env: Env) -> None:
    note = request(env)
    env.append(orch(), turn("I wrote the note.", "u1"))
    env.forget()
    tick()
    (again,) = env.sends()
    assert "did not end with HANDOFF_READY" in again[2] and str(note) in again[2]
    assert env.cycle("orchestrator")["requests"] == 2

    env.forget()
    tick()
    assert env.sends() == []

    env.append(orch(), turn("Still no marker.", "u2"))
    tick()
    (cancel,) = env.sends()
    assert cancel[2].startswith("[zen-rotate] No restart this time (")
    assert "Re-arm any waiter or timer you stopped" in cancel[2]
    cycle = env.cycle("orchestrator")
    assert cycle["phase"] == "idle" and cycle["cancels"] == 1
    assert cycle["backoff_until"] == NOW + 30 * 60


def test_invalid_note_asks_again_naming_the_gap(env: Env) -> None:
    note = request(env)
    note.write_text("## State\nnone\n")
    env.append(orch(), turn(rotate.READY, "u1"))
    env.forget()
    tick()
    (again,) = env.sends()
    assert "the note is not valid" in again[2]
    assert "missing heading '## First actions'" in again[2]


def test_turn_after_ready_asks_for_an_updated_note(env: Env) -> None:
    note = request(env)
    ready(env, note)
    env.append(orch(), turn("Answered the worker.", "u2"))
    env.forget()
    tick()
    (again,) = env.sends()
    assert "a later turn ran after your HANDOFF_READY" in again[2]
    cycle = env.cycle("orchestrator")
    assert cycle["requests"] == 2 and cycle["misses"] == 0

    env.append(orch(), turn(rotate.READY, "u3"), turn("Another message.", "u4"))
    tick()
    assert env.cycle("orchestrator")["requests"] == 3
    env.append(orch(), turn(rotate.READY, "u5"), turn("And another.", "u6"))
    env.forget()
    tick()
    (cancel,) = env.sends()
    assert "3 requests without a clean handoff" in cancel[2]
    assert f"listed in {note}" in cancel[2]


def test_request_timeout_cancels(env: Env, monkeypatch: pytest.MonkeyPatch) -> None:
    request(env)
    env.sessions(orch(status="working"))
    at(monkeypatch, 19)
    env.forget()
    tick()
    assert env.sends() == []
    at(monkeypatch, 21)
    tick()
    (cancel,) = env.sends()
    assert "no turn end within 20 minutes" in cancel[2]
    cycle = env.cycle("orchestrator")
    assert cycle["cancels"] == 1
    assert cycle["last_cancel"] == (
        "2026-11-21T12:21:00+00:00 no turn end within 20 minutes of the request"
    )


def test_clear_not_confirmed_cancels_with_long_backoff(env: Env) -> None:
    note = request(env)
    ready(env, note)
    env.screen(EMPTY_SCREEN + "\n".join(f"line {n}" for n in range(30)) + "\n")
    env.forget()
    tick()
    (cancel,) = env.sends()
    assert "the /clear was not confirmed" in cancel[2]
    assert "--saved" not in cancel
    cycle = env.cycle("orchestrator")
    assert cycle["backoff_until"] == NOW + 60 * 60 and cycle["cancels"] == 1
    assert not list(env.state.glob("clearing-*"))
    log = env.log()
    assert "pane of o1 after an unconfirmed /clear" in log
    assert "line 29" in log and "line 15" in log and "line 14" not in log


def test_resume_send_failure_is_retried(env: Env) -> None:
    note = request(env)
    ready(env, note)
    env.sessions(orch(agent_session_id="conv-2"), after=True)
    env.flag("send_fail")
    tick()
    assert env.cycle("orchestrator")["phase"] == "cleared"
    env.unflag("send_fail")
    env.forget()
    tick()
    assert not [c for c in env.calls("tmux") if "send-keys" in c]
    assert env.sends()[-1][:4] == ["send", "o1", "--saved", "orchestrator"]
    assert env.cycle("orchestrator")["last_note"] == str(note)


def test_conversation_changed_cancels(env: Env) -> None:
    request(env)
    env.sessions(orch(agent_session_id="conv-9"))
    env.forget()
    tick()
    (cancel,) = env.sends()
    assert "the conversation changed" in cancel[2]


def test_session_gone_cancels_without_a_message(env: Env) -> None:
    request(env)
    env.sessions(orch(status="dead"))
    env.forget()
    tick()
    assert env.sends() == []
    assert env.cycle("orchestrator")["cancels"] == 1


def test_three_cancels_pause_until_resume(
    env: Env, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    for n in range(3):
        at(monkeypatch, n * 100)
        env.sessions(orch())
        env.write(orch(), [turn("work", "u0")])
        tick()
        assert env.cycle("orchestrator")["phase"] == "requested"
        env.sessions(orch(status="working"))
        at(monkeypatch, n * 100 + 21)
        tick()
        assert env.cycle("orchestrator")["cancels"] == n + 1
    assert env.cycle("orchestrator")["paused"] is True
    assert "paused orchestrator" in capsys.readouterr().out

    at(monkeypatch, 1000)
    env.sessions(orch())
    env.write(orch(), [turn("work", "u0")])
    env.forget()
    tick()
    assert env.calls() == [["agent-manager", "sessions", "--json"]]

    assert rotate.main(["resume", "orchestrator"]) == 0
    cycle = env.cycle("orchestrator")
    assert cycle["paused"] is False and cycle["cancels"] == 0
    tick()
    assert len(env.sends()) == 1


def test_rotation_resets_the_cancel_count(env: Env) -> None:
    (env.state / "state.json").write_text(json.dumps({"orchestrator": {"cancels": 2}}))
    note = request(env)
    ready(env, note)
    env.sessions(orch(agent_session_id="conv-2"), after=True)
    tick()
    assert env.cycle("orchestrator")["cancels"] == 0


# --------------------------------------------------------------------------- #
# Lock                                                                          #
# --------------------------------------------------------------------------- #


def test_second_tick_exits_on_the_lock(env: Env) -> None:
    env.sessions(orch())
    env.write(orch(), [])
    with (env.state / "lock").open("w") as held:
        fcntl.flock(held, fcntl.LOCK_EX)
        assert tick() == 0
    assert env.calls() == []


def test_lock_file_nobody_holds_is_taken(env: Env) -> None:
    env.sessions(orch())
    env.write(orch(), [])
    (env.state / "lock").write_text("999999999")
    assert tick() == 0
    assert len(env.sends()) == 1
    assert stat.S_IMODE(env.state.stat().st_mode) == 0o700


# --------------------------------------------------------------------------- #
# check-note                                                                    #
# --------------------------------------------------------------------------- #


def test_check_note_valid(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    note = env.tmp / "note.md"
    note.write_text(valid_note())
    note.chmod(0o644)
    assert rotate.main(["check-note", str(note)]) == 0
    assert capsys.readouterr().out == "ok\n"
    assert stat.S_IMODE(note.stat().st_mode) == 0o600
    gitleaks = env.calls("gitleaks")[0]
    assert gitleaks[1:3] == ["dir", str(note)]
    assert gitleaks[gitleaks.index("--config") + 1].endswith("git/gitleaks.toml")


def test_check_note_missing_file(env: Env) -> None:
    assert rotate.check_note(env.tmp / "nope.md") == [
        f"no note at {env.tmp / 'nope.md'}"
    ]


def test_check_note_missing_heading(env: Env) -> None:
    note = env.tmp / "note.md"
    note.write_text(valid_note().replace("## Owed\n", "## Owing\n"))
    assert rotate.check_note(note) == ["missing heading '## Owed'"]


def test_check_note_empty_section(env: Env) -> None:
    note = env.tmp / "note.md"
    note.write_text(valid_note().replace("## Files\nnone\n", "## Files\n\n"))
    assert rotate.check_note(note) == ["section '## Files' is empty (write none)"]


def test_check_note_heading_inside_a_fence_is_content(env: Env) -> None:
    note = env.tmp / "note.md"
    fenced = "## Files\n```\n## First actions\n```\n"
    note.write_text(valid_note().replace("## Files\nnone\n", fenced))
    assert rotate.check_note(note) == []


def test_check_note_oversize(env: Env) -> None:
    note = env.tmp / "note.md"
    note.write_text(valid_note("x" * 4000))
    (problem,) = rotate.check_note(note)
    assert "over the 30000 limit" in problem


def test_check_note_planted_secret(
    env: Env, capsys: pytest.CaptureFixture[str]
) -> None:
    note = env.tmp / "note.md"
    note.write_text(valid_note("token PLANTED_SECRET"))
    assert rotate.main(["check-note", str(note)]) == 1
    assert "gitleaks found a secret" in capsys.readouterr().out


def test_check_note_real_gitleaks_and_repo_config(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The real binary, which the pre-push hook needs on both machines anyway."""
    assert shutil.which("gitleaks"), "install gitleaks"
    monkeypatch.setenv("HOME", str(tmp_path))
    note = tmp_path / "note.md"
    note.write_text(valid_note())
    assert rotate.check_note(note) == []
    key = "ghp_" + "8fK2mQ9xLr4TzW1vB7nYc3JdH6sPa0EgUi5o"
    note.write_text(valid_note(f"the token is {key}"))
    assert rotate.check_note(note) == [
        "gitleaks found a secret; name it by where it lives instead"
    ]


def test_note_pruning_keeps_30(env: Env) -> None:
    directory = env.home / ".cache/zen/handoff/orchestrator"
    directory.mkdir(parents=True)
    for n in range(34):
        (directory / f"20261101-0000{n:02d}.md").write_text("old")
    note = request(env)
    ready(env, note)
    env.sessions(orch(agent_session_id="conv-2"), after=True)
    tick()
    kept = sorted(p.name for p in directory.glob("*.md"))
    assert len(kept) == 30 and kept[-1] == note.name
    assert kept[0] == "20261101-000005.md"


# --------------------------------------------------------------------------- #
# now, status                                                                   #
# --------------------------------------------------------------------------- #


def probe(**extra: object) -> dict[str, object]:
    return orch(id="p1", name="rotate-probe", group="me", **extra)


def test_now_role_skips_only_the_age_check(
    env: Env, capsys: pytest.CaptureFixture[str]
) -> None:
    env.roles()
    env.sessions(orch())
    env.write(orch(), [], age=60, quiet=60)
    assert rotate.main(["now", "orchestrator"]) == 1
    assert "needs 2m quiet" in capsys.readouterr().err
    assert env.sends() == []

    env.write(orch(), [], age=600, quiet=300)
    assert rotate.main(["now", "orchestrator"]) == 0
    assert len(env.sends()) == 1
    assert rotate.main(["now", "orchestrator"]) == 1
    assert "already in flight" in capsys.readouterr().err


def test_now_session_refuses_role_sessions(
    env: Env, capsys: pytest.CaptureFixture[str]
) -> None:
    chief = orch(id="c1", name="chief")
    env.sessions(chief, orch())
    for key in ("c1", "chief", "o1", "orchestrator"):
        assert rotate.main(["now", "--session", key]) == 1
        assert "is a role session" in capsys.readouterr().err
    assert env.sends() == []
    assert (
        not (env.state / "state.json").exists()
        or "session:c1" not in (env.state / "state.json").read_text()
    )


def test_now_session_runs_a_cycle_with_roles_off(env: Env) -> None:
    env.roles()
    env.sessions(probe())
    env.write(probe(), [turn("started sleep", "u0")], age=60, quiet=1)
    assert rotate.main(["now", "--session", "rotate-probe"]) == 0
    cycle = env.cycle("session:p1")
    note = Path(str(cycle["note"]))
    assert note.parent == env.home / ".cache/zen/handoff/session-p1"
    note.write_text(valid_note())
    env.append(probe(), turn(rotate.READY, "u1"))
    env.sessions(probe(agent_session_id="conv-2"), after=True)
    env.forget()
    assert tick() == 0
    resume = env.sends()[-1]
    assert resume[:2] == ["send", "p1"] and len(resume) == 3
    assert "session:p1" not in json.loads((env.state / "state.json").read_text())
    env.forget()
    tick()
    assert env.calls() == []


def test_now_session_saved_is_used_on_resume(env: Env) -> None:
    env.sessions(probe())
    env.write(probe(), [turn("x", "u0")])
    assert rotate.main(["now", "--session", "p1", "--saved", "worker-rules"]) == 0
    note = Path(str(env.cycle("session:p1")["note"]))
    note.write_text(valid_note())
    env.append(probe(), turn(rotate.READY, "u1"))
    env.sessions(probe(agent_session_id="conv-2"), after=True)
    tick()
    assert env.sends()[-1][:4] == ["send", "p1", "--saved", "worker-rules"]


def test_now_usage_errors(env: Env) -> None:
    for argv in (
        ["now"],
        ["now", "chief", "--session", "x"],
        ["now", "chief", "--saved", "s"],
    ):
        with pytest.raises(SystemExit) as e:
            rotate.main(argv)
        assert e.value.code == 2


def test_status_json(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    env.sessions(orch())
    env.write(orch(), [], age=5 * HOUR)
    state = {"orchestrator": {"last_rotation": NOW - HOUR, "last_note": "/n.md"}}
    (env.state / "state.json").write_text(json.dumps(state))
    assert rotate.main(["status", "--json"]) == 0
    rows = {r["role"]: r for r in json.loads(capsys.readouterr().out)}
    row = rows["orchestrator"]
    assert row["enabled"] is True and row["session"] == "o1"
    assert row["age_seconds"] == 5 * HOUR and row["due_in_seconds"] == HOUR
    assert row["phase"] == "idle" and row["last_note"] == "/n.md"
    assert row["last_cancel"] is None
    assert row["last_rotation"] == "2026-11-21T11:00:00+00:00"
    assert rows["chief"]["enabled"] is False and rows["chief"]["session"] is None

    assert rotate.main(["status"]) == 0
    out = capsys.readouterr().out
    assert "orchestrator · session o1 · age 5h00m · due in 1h00m · phase idle" in out
    assert "chief (off) · session -" in out


def test_tick_fails_in_one_line_when_sessions_fail(
    env: Env, capsys: pytest.CaptureFixture[str]
) -> None:
    assert tick() == 1
    assert capsys.readouterr().err == "zen-rotate: agent-manager sessions failed\n"


def test_bad_config_is_one_line(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    env.config.write_text('{"rotate": {"hours": "six"}}')
    assert tick() == 1
    assert "rotate.hours: expected a number" in capsys.readouterr().err


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
