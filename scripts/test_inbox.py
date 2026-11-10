#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "typer>=0.12",
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/inbox.py.

Run with:  uv run scripts/test_inbox.py
"""

from __future__ import annotations

import importlib.util
import json
import os
import sqlite3
import subprocess
import sys
from pathlib import Path

import pytest
from typer.testing import CliRunner

SCRIPT = Path(__file__).parent / "inbox.py"
_spec = importlib.util.spec_from_file_location("zen_inbox", SCRIPT)
assert _spec is not None and _spec.loader is not None
inbox = importlib.util.module_from_spec(_spec)
sys.modules["zen_inbox"] = inbox
_spec.loader.exec_module(inbox)

runner = CliRunner()

FAKE_AM = r"""#!/usr/bin/env bash
case "$1" in
  sessions) cat "$FAKE_AM/sessions.json" ;;
  send)
    f=$(mktemp "$FAKE_AM/sends/send.XXXXXX")
    printf '%s\0' "$@" > "$f"
    if [ -f "$FAKE_AM/refuse_from" ] && [[ " $* " == *" --from "* ]]; then
      rm "$f"
      echo "agent-manager: a sender name is for a caller with no session on this machine" >&2
      exit 1
    fi
    if [ -f "$FAKE_AM/send_rc" ]; then
      echo "send refused" >&2
      exit "$(cat "$FAKE_AM/send_rc")"
    fi
    echo '{"message_id": 42}'
    ;;
esac
"""

FAKE_TMUX = r"""#!/usr/bin/env bash
printf '%s\0' "$@" > "$FAKE_AM/tmux.argv"
"""


# --------------------------------------------------------------------------- #
# Fixtures                                                                      #
# --------------------------------------------------------------------------- #


def _session(sid: str, status: str = "idle", running: bool = True) -> dict[str, object]:
    return {
        "id": sid,
        "name": f"spec-{sid}",
        "status": status,
        "running": running,
        "archived": False,
    }


@pytest.fixture
def fake(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """Fake agent-manager and tmux on PATH, temp db, no caller session."""
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    data = tmp_path / "am"
    (data / "sends").mkdir(parents=True)
    (data / "sessions.json").write_text(json.dumps([_session("s1"), _session("s2")]))
    for name, body in (("agent-manager", FAKE_AM), ("tmux", FAKE_TMUX)):
        script = bin_dir / name
        script.write_text(body)
        script.chmod(0o755)
    monkeypatch.setenv("PATH", f"{bin_dir}{os.pathsep}{os.environ['PATH']}")
    monkeypatch.setenv("FAKE_AM", str(data))
    monkeypatch.setenv("ZEN_FLOW_DB", str(tmp_path / "zen-flow.db"))
    monkeypatch.delenv("AGENT_MANAGER_SESSION_ID", raising=False)
    monkeypatch.delenv("TMUX", raising=False)
    return data


@pytest.fixture
def conn(fake: Path) -> sqlite3.Connection:
    connection: sqlite3.Connection = inbox.connect(Path(os.environ["ZEN_FLOW_DB"]))
    return connection


def sends(fake: Path) -> list[list[str]]:
    files = sorted((fake / "sends").iterdir(), key=lambda p: p.stat().st_mtime_ns)
    return [f.read_bytes().decode().split("\0")[:-1] for f in files]


def ask(
    conn: sqlite3.Connection,
    task: str = "7",
    session: str = "s1",
    options: list[str] | None = None,
    gate: bool = False,
    question: str = "Which way?",
) -> str:
    opts = ["left", "right"] if options is None else options
    d = inbox.ask(conn, task, question, opts, "", "", gate, session, f"spec-{session}")
    return str(d.id)


def cli(*args: str) -> tuple[int, str]:
    result = runner.invoke(inbox.app, list(args))
    return result.exit_code, result.output


# --------------------------------------------------------------------------- #
# IDs and schema                                                                #
# --------------------------------------------------------------------------- #


def test_ids_sequential_per_task_and_never_reused(conn: sqlite3.Connection) -> None:
    assert [ask(conn), ask(conn)] == ["7.1", "7.2"]
    assert ask(conn, task="8") == "8.1"
    inbox.withdraw(conn, "7.2", "s1", False)
    assert ask(conn) == "7.3"
    assert [ask(conn, gate=True), ask(conn, gate=True), ask(conn, gate=True)] == [
        "7.g",
        "7.g2",
        "7.g3",
    ]
    assert ask(conn) == "7.4"


def test_schema_init_and_reopen(fake: Path) -> None:
    path = Path(os.environ["ZEN_FLOW_DB"])
    first = inbox.connect(path)
    assert first.execute("PRAGMA user_version").fetchone()[0] == inbox.SCHEMA_VERSION
    assert first.execute("PRAGMA journal_mode").fetchone()[0] == "wal"
    ask(first)
    first.close()
    again = inbox.connect(path)
    assert again.execute("PRAGMA user_version").fetchone()[0] == inbox.SCHEMA_VERSION
    assert [d.id for d in inbox.select(again)] == ["7.1"]


def test_unavailable_vault_exits_4(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv("ZEN_FLOW_DB", raising=False)
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.setattr(inbox.Path, "home", lambda: tmp_path)
    code, _ = cli("list")
    assert code == 4


def test_concurrent_asks_get_distinct_consecutive_ids(fake: Path) -> None:
    env = {**os.environ, "AGENT_MANAGER_SESSION_ID": "s1"}
    procs = [
        subprocess.Popen(
            [sys.executable, str(SCRIPT), "ask", "9", "-q", f"q{i}"],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        for i in range(6)
    ]
    results = [p.communicate() for p in procs]
    assert [p.returncode for p in procs] == [0] * 6, results
    ids = sorted(out.strip() for out, _ in results)
    assert ids == [f"9.{n}" for n in range(1, 7)]
    assert not any("locked" in err for _, err in results)


# --------------------------------------------------------------------------- #
# Validation and ownership                                                      #
# --------------------------------------------------------------------------- #


@pytest.mark.parametrize("count", [1, 5])
def test_option_count_rejected(fake: Path, count: int) -> None:
    opts = [arg for i in range(count) for arg in ("-o", f"opt{i}")]
    code, _ = cli("ask", "7", "-q", "Q?", *opts, "--session", "s1")
    assert code == 2


def test_unknown_rec_rejected(fake: Path) -> None:
    code, _ = cli("ask", "7", "-q", "Q?", "-o", "a1", "-o", "b1", "--rec", "c")
    assert code == 2
    code, _ = cli(
        "ask", "7", "-q", "Q?", "-o", "a1", "-o", "b1", "--rec", "c", "--session", "s1"
    )
    assert code == 2


def test_no_session_exits_2(fake: Path) -> None:
    code, _ = cli("ask", "7", "-q", "Q?")
    assert code == 2


def test_session_option_and_env(fake: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("AGENT_MANAGER_SESSION_ID", "s2")
    code, out = cli("ask", "7", "-q", "Q?", "--json")
    assert code == 0
    assert json.loads(out)["session_id"] == "s2"
    assert json.loads(out)["session_name"] == "spec-s2"
    code, out = cli("ask", "7", "-q", "Q?", "--session", "s1", "--json")
    assert json.loads(out)["session_id"] == "s1"


def test_amend_owner_only_and_force(
    fake: Path, conn: sqlite3.Connection, monkeypatch: pytest.MonkeyPatch
) -> None:
    ask(conn)
    monkeypatch.setenv("AGENT_MANAGER_SESSION_ID", "s2")
    assert cli("amend", "7.1", "-q", "New?")[0] == 1
    assert cli("amend", "7.1", "-q", "New?", "--force")[0] == 0
    monkeypatch.setenv("AGENT_MANAGER_SESSION_ID", "s1")
    code, _ = cli("amend", "7.1", "-o", "x", "-o", "y", "-o", "z", "--rec", "c")
    assert code == 0
    d = inbox.get(conn, "7.1")
    assert (d.id, d.question, d.rec, d.amended) == ("7.1", "New?", "c", True)
    assert [t for _, t in d.options] == ["x", "y", "z"]


def test_withdraw_owner_only_and_force(
    fake: Path, conn: sqlite3.Connection, monkeypatch: pytest.MonkeyPatch
) -> None:
    ask(conn)
    ask(conn)
    monkeypatch.setenv("AGENT_MANAGER_SESSION_ID", "s2")
    assert cli("withdraw", "7.1")[0] == 1
    assert cli("withdraw", "7.1", "--force")[0] == 0
    monkeypatch.setenv("AGENT_MANAGER_SESSION_ID", "s1")
    assert cli("withdraw", "7.2")[0] == 0
    assert {d.status for d in inbox.select(conn)} == {"withdrawn"}


def test_amend_withdraw_refused_when_closed(
    fake: Path, conn: sqlite3.Connection, monkeypatch: pytest.MonkeyPatch
) -> None:
    ask(conn)
    ask(conn)
    inbox.answer(conn, "7.1", "a", None)
    inbox.withdraw(conn, "7.2", "s1", False)
    monkeypatch.setenv("AGENT_MANAGER_SESSION_ID", "s1")
    for target in ("7.1", "7.2"):
        assert cli("amend", target, "-q", "x?", "--force")[0] == 1
        assert cli("withdraw", target, "--force")[0] == 1
    assert cli("show", "7.99")[0] == 1


# --------------------------------------------------------------------------- #
# Batching                                                                      #
# --------------------------------------------------------------------------- #


def test_first_answer_held_second_sends_one_message(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn, question="First?")
    ask(conn, question="Second?")
    assert inbox.answer(conn, "7.2", None, "do it") is None
    assert inbox.get(conn, "7.2").delivery == "held"
    assert sends(fake) == []
    result = inbox.answer(conn, "7.1", "b", None)
    assert result is not None and result.ok
    assert len(sends(fake)) == 1
    msg = sends(fake)[0][2]
    assert msg.index("7.1 First?") < msg.index("7.2 Second?")
    assert {d.delivery for d in inbox.select(conn)} == {"sent"}
    assert {d.message_id for d in inbox.select(conn)} == {"42"}


@pytest.mark.parametrize("closer", ["resolve", "withdraw"])
def test_resolve_or_withdraw_of_last_open_flushes(
    fake: Path, conn: sqlite3.Connection, closer: str
) -> None:
    ask(conn, question="Answered?")
    ask(conn, question="Closer?")
    inbox.answer(conn, "7.1", "a", None)
    if closer == "resolve":
        result = inbox.resolve(conn, "7.2", "talked it over")
    else:
        result = inbox.withdraw(conn, "7.2", "s1", False)
    assert result is not None and result.ids == ["7.1"]
    [argv] = sends(fake)
    assert "Answered?" in argv[2]
    assert "Closer?" not in argv[2]
    assert inbox.get(conn, "7.2").delivery == "none"


def test_other_sessions_open_decisions_do_not_hold(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn, session="s1")
    ask(conn, session="s2")
    result = inbox.answer(conn, "7.1", "a", None)
    assert result is not None and result.ok
    assert sends(fake)[0][1] == "s1"


def test_resolve_alone_sends_nothing(fake: Path, conn: sqlite3.Connection) -> None:
    ask(conn)
    assert inbox.resolve(conn, "7.1") is None
    assert sends(fake) == []
    assert inbox.get(conn, "7.1").answer_kind == "talk"


# --------------------------------------------------------------------------- #
# Message                                                                       #
# --------------------------------------------------------------------------- #


def test_message_lines_and_argv(fake: Path, conn: sqlite3.Connection) -> None:
    ask(conn, question="Pick?")
    ask(conn, question="Free?", options=[])
    inbox.answer(conn, "7.1", "b", None, note="ship it")
    inbox.answer(conn, "7.2", None, "use the env var")
    [argv] = sends(fake)
    assert argv[:2] == ["send", "s1"]
    assert argv[3:5] == ["--from", "zen-inbox"]
    assert argv[2] == (
        "[zen-inbox] Answers to your decisions:\n"
        "7.1 Pick? → b: right\n"
        "  Note: ship it\n"
        "7.2 Free? → free text: use the env var"
    )


def test_oversize_batch_trimmed_under_limit(conn: sqlite3.Connection) -> None:
    for _ in range(4):
        ask(conn, question="Q" * 3000)
    for n in range(1, 5):
        inbox._close(conn, f"7.{n}", "answered", "pick", option="a", delivery="held")
    msg = inbox.build_message(inbox.select(conn))
    assert len(msg.encode()) < inbox.SEND_LIMIT
    assert all(f"7.{n}" in msg and "→ a: left" in msg for n in range(1, 5))


def test_huge_free_text_hard_truncated(conn: sqlite3.Connection) -> None:
    ask(conn, options=[])
    inbox._close(conn, "7.1", "answered", "text", text="é" * 9000, delivery="held")
    msg = inbox.build_message(inbox.select(conn))
    assert len(msg.encode()) < inbox.SEND_LIMIT
    assert msg.endswith("zen-inbox show <id>")


def test_message_ids_in_numeric_order(conn: sqlite3.Connection) -> None:
    for _ in range(10):
        ask(conn)
    for n in (10, 2):
        inbox._close(conn, f"7.{n}", "answered", "pick", option="a", delivery="held")
    msg = inbox.build_message(inbox.select(conn, "delivery = 'held'"))
    assert msg.index("7.2 ") < msg.index("7.10 ")


# --------------------------------------------------------------------------- #
# Delivery failure                                                              #
# --------------------------------------------------------------------------- #


def test_failed_send_exit_3_then_retry(fake: Path, conn: sqlite3.Connection) -> None:
    ask(conn)
    ask(conn)
    assert cli("answer", "7.1", "a")[0] == 0
    (fake / "send_rc").write_text("1")
    code, _ = cli("answer", "7.2", "--text", "free")
    assert code == 3
    rows = inbox.select(conn)
    assert {d.delivery for d in rows} == {"failed"}
    assert all("send refused" in d.delivery_error for d in rows)
    assert "retry s1" in inbox.render(conn, None)
    (fake / "send_rc").unlink()
    assert cli("retry", "s1")[0] == 0
    assert len(sends(fake)) == 2
    assert sends(fake)[-1][2] == sends(fake)[-2][2]
    assert {d.delivery for d in inbox.select(conn)} == {"sent"}
    assert cli("retry", "s1")[0] == 1


def test_gone_session_fails_without_sending(
    fake: Path, conn: sqlite3.Connection
) -> None:
    (fake / "sessions.json").write_text(json.dumps([_session("s1", "dead", False)]))
    ask(conn)
    result = inbox.answer(conn, "7.1", "a", None)
    assert result is not None and not result.ok and "gone" in result.error
    assert sends(fake) == []


def test_sender_refused_falls_back_without_from(
    fake: Path, conn: sqlite3.Connection
) -> None:
    (fake / "refuse_from").write_text("")
    ask(conn)
    result = inbox.answer(conn, "7.1", "a", None)
    assert result is not None and result.ok
    [argv] = sends(fake)
    assert "--from" not in argv


def test_second_answer_refused(fake: Path, conn: sqlite3.Connection) -> None:
    ask(conn)
    assert cli("answer", "7.1", "a")[0] == 0
    assert cli("answer", "7.1", "b")[0] == 1
    assert cli("resolve", "7.1")[0] == 1
    assert cli("answer", "7.1")[0] == 2


def test_concurrent_answers_one_wins_flush_once(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn)
    procs = [
        subprocess.Popen(
            [sys.executable, str(SCRIPT), "answer", "7.1", letter],
            env=dict(os.environ),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        for letter in ("a", "b")
    ]
    codes = sorted(p.wait() for p in procs)
    assert codes == [0, 1]
    assert len(sends(fake)) == 1


def test_concurrent_answers_on_last_two_flush_once(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn)
    ask(conn)
    procs = [
        subprocess.Popen(
            [sys.executable, str(SCRIPT), "answer", did, "a"],
            env=dict(os.environ),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        for did in ("7.1", "7.2")
    ]
    assert [p.wait() for p in procs] == [0, 0]
    [argv] = sends(fake)
    assert "7.1 " in argv[2] and "7.2 " in argv[2]


# --------------------------------------------------------------------------- #
# Talk                                                                          #
# --------------------------------------------------------------------------- #


def test_popup_argv_targets_session_on_own_socket() -> None:
    argv = inbox.popup_argv("ab12", "/tmp/tmux-501/agentmgr,123,4")
    assert argv is not None
    assert argv[:3] == ["tmux", "-S", "/tmp/tmux-501/agentmgr"]
    assert argv[3:9] == ["display-popup", "-E", "-w", "90%", "-h", "90%"]
    assert argv[9] == "env -u TMUX tmux -S /tmp/tmux-501/agentmgr attach -t am_ab12"


def test_talk_runs_popup(fake: Path) -> None:
    assert inbox.talk("ab12", "/tmp/sock,1,2")
    argv = (fake / "tmux.argv").read_bytes().decode().split("\0")[:-1]
    assert argv[:3] == ["-S", "/tmp/sock", "display-popup"]
    assert argv[-1].endswith("attach -t am_ab12")


def test_talk_without_tmux_prints_attach(
    fake: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    assert not inbox.talk("ab12", None)
    assert "tmux -L agentmgr attach -t am_ab12" in capsys.readouterr().out
    assert not (fake / "tmux.argv").exists()


# --------------------------------------------------------------------------- #
# Watch change detection                                                        #
# --------------------------------------------------------------------------- #


def test_fingerprint_flips_on_every_change(fake: Path) -> None:
    path = Path(os.environ["ZEN_FLOW_DB"])
    watcher = inbox.connect(path)
    writer = inbox.connect(path)
    last = inbox.fingerprint(watcher)

    def flipped() -> bool:
        nonlocal last
        current = inbox.fingerprint(watcher)
        changed = bool(current != last)
        last = current
        return changed

    assert not flipped()
    ask(writer)
    assert flipped()
    ask(writer)
    ask(writer)
    ask(writer)
    assert flipped()
    inbox.amend(writer, "7.1", "s1", False, "Changed?", None, None, None)
    assert flipped()
    inbox.withdraw(writer, "7.2", "s1", False)
    assert flipped()
    inbox.answer(writer, "7.1", "a", None)
    assert flipped()
    inbox.resolve(writer, "7.3")
    assert flipped()
    assert not flipped()


def test_render_shows_held_and_gone(fake: Path, conn: sqlite3.Connection) -> None:
    ask(conn)
    ask(conn)
    inbox.answer(conn, "7.1", "a", None)
    view = inbox.render(conn, [_session("s1", "dead", False)])
    assert "spec-s1 [gone]" in view
    assert "7.1 held until 7.2" in view


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
