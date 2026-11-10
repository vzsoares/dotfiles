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
from collections.abc import Callable
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


def test_in_flight_sending_not_retryable_until_stale(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn)
    conn.execute(
        "UPDATE decisions SET status = 'answered', answer_kind = 'pick',"
        " answer_option = 'a', delivery = 'sending', updated_at = ?",
        (inbox.now(),),
    )
    assert inbox.undelivered(conn) == {}
    assert cli("retry", "s1")[0] == 1
    conn.execute("UPDATE decisions SET updated_at = '2000-01-01T00:00:00+00:00'")
    assert cli("retry", "s1")[0] == 0
    assert len(sends(fake)) == 1


def test_held_with_nothing_open_is_retryable(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn)
    inbox._close(conn, "7.1", "answered", "pick", option="a", delivery="held")
    assert "retry s1" in inbox.render(conn, None)
    assert cli("retry", "s1")[0] == 0
    assert {d.delivery for d in inbox.select(conn)} == {"sent"}


def test_pick_checked_against_current_options(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn, options=["x", "y", "z"])
    inbox.amend(conn, "7.1", "s1", False, None, ["x", "y"], None, None)
    assert cli("answer", "7.1", "c")[0] == 2
    assert inbox.get(conn, "7.1").status == "open"


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
    first, second = sends(fake)
    assert first[3:5] == ["--from", "zen-inbox"]
    assert "--from" not in second
    assert second[2] == first[2]
    assert inbox.get(conn, "7.1").delivery == "sent"


def test_other_send_failure_does_not_drop_from(
    fake: Path, conn: sqlite3.Connection
) -> None:
    (fake / "send_rc").write_text("1")
    ask(conn)
    result = inbox.answer(conn, "7.1", "a", None)
    assert result is not None and not result.ok
    [argv] = sends(fake)
    assert "--from" in argv
    assert inbox.get(conn, "7.1").delivery == "failed"


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


@pytest.mark.parametrize(
    "tmux_env", ["/tmp/tmux-501/agentmgr,123,4", "/tmp/tmux-501/default,9,0"]
)
def test_popup_argv_attaches_on_manager_socket(tmux_env: str) -> None:
    argv = inbox.popup_argv("ab12", tmux_env)
    assert argv is not None
    assert argv[:3] == ["tmux", "-S", tmux_env.split(",")[0]]
    assert argv[3:9] == ["display-popup", "-E", "-w", "90%", "-h", "90%"]
    assert argv[9] == "env -u TMUX tmux -L agentmgr attach -t am_ab12"


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
    assert view.splitlines()[0].startswith("spec-s1 · #7 · 1 open · ")
    assert "[gone]" in view
    assert "7.1 held until 7.2" in view


# --------------------------------------------------------------------------- #
# Session view                                                                  #
# --------------------------------------------------------------------------- #

WORDS = "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod"


def words(n: int) -> str:
    """Synthetic prose of about n characters."""
    out = (WORDS + " ") * (n // len(WORDS) + 1)
    return out[:n].rstrip()


def stamp(conn: sqlite3.Connection, decision_id: str, created: str) -> None:
    conn.execute(
        "UPDATE decisions SET created_at = ? WHERE id = ?", (created, decision_id)
    )


def test_rows_one_per_session_oldest_first(conn: sqlite3.Connection) -> None:
    ask(conn, task="570", session="s2")
    ask(conn, task="573", session="s1")
    ask(conn, task="574", session="s1", gate=True)
    ask(conn, task="570", session="s2")
    stamp(conn, "570.1", "2026-01-01T00:00:02+00:00")
    stamp(conn, "570.2", "2026-01-01T00:00:03+00:00")
    stamp(conn, "573.1", "2026-01-01T00:00:01+00:00")
    stamp(conn, "574.g", "2026-01-01T00:00:04+00:00")
    rows = inbox.list_rows(inbox.select(conn, "status = 'open'"), None)
    assert [(r.session_id, r.tasks, r.count, r.gate) for r in rows] == [
        ("s1", ["573", "574"], 2, True),
        ("s2", ["570"], 2, False),
    ]
    label = inbox.row_label(rows[0])
    assert label.startswith("spec-s1 · #573, #574 · 2 open · ")
    assert label.endswith("[gone] · gate")


def test_context_block_before_questions(conn: sqlite3.Connection) -> None:
    inbox.ask(
        conn,
        "570",
        "Which way?",
        ["l", "r"],
        "a",
        "",
        False,
        "s1",
        "spec-s1",
        "Zen board",
        "What the board does.\nWhat these settle.",
    )
    sessions = [{**_session("s1"), "group": "me", "directory": "/x/dotfiles"}]
    at = inbox.datetime.now(inbox.UTC)
    lines = inbox.session_lines(inbox.session_open(conn, "s1"), sessions, 80, at)
    assert lines[:3] == [
        "#570 Zen board",
        "  What the board does.",
        "  What these settle.",
    ]
    assert lines[3].startswith("asked by spec-s1 (me, dotfiles) · idle · ")
    assert lines.index("570.1  Which way?") > 3
    compact = inbox.session_lines(
        inbox.session_open(conn, "s1"), sessions, 80, at, True
    )
    assert compact[:4] == lines[:4] and compact[4] == "570.1  Which way?"
    assert "a) l  ← rec" in lines


def test_context_block_falls_back_without_brief(conn: sqlite3.Connection) -> None:
    ask(conn, task="570")
    lines = inbox.session_lines(
        inbox.session_open(conn, "s1"),
        [_session("s1")],
        80,
        inbox.datetime.now(inbox.UTC),
    )
    assert lines[0] == "#570"
    assert lines[1].startswith("asked by spec-s1 · idle · ")


@pytest.mark.parametrize("width", [40, 80, 200])
def test_wrap_never_exceeds_width_or_splits_words(width: int) -> None:
    text = words(700)
    lines = inbox.wrap(text, width, "a) ")
    assert all(len(line) <= width for line in lines)
    assert " ".join(line.strip() for line in lines).replace("a) ", "", 1) == text


def test_brief_flags_upsert_and_keep(fake: Path, conn: sqlite3.Connection) -> None:
    base = ["ask", "7", "-q", "Q?", "--session", "s1"]
    assert cli(*base, "--title", "Old", "--brief", "First brief")[0] == 0
    assert cli(*base, "--title", "New")[0] == 0
    d = inbox.get(conn, "7.2")
    assert (d.title, d.brief) == ("New", "First brief")
    assert cli(*base, "--title", "", "--brief", "")[0] == 0
    assert (inbox.get(conn, "7.3").title, inbox.get(conn, "7.3").brief) == (
        "New",
        "First brief",
    )


def test_brief_command_replaces_and_targets_session(
    fake: Path, conn: sqlite3.Connection, monkeypatch: pytest.MonkeyPatch
) -> None:
    ask(conn, session="s1")
    ask(conn, session="s2")
    monkeypatch.setenv("AGENT_MANAGER_SESSION_ID", "s1")
    assert cli("brief", "7", "--title", "T1", "--text", "B1")[0] == 0
    assert cli("brief", "7", "--text", "B2")[0] == 0
    code, out = cli("brief", "7", "--session", "s2", "--title", "Other", "--json")
    assert code == 0 and json.loads(out)["session_id"] == "s2"
    assert cli("brief", "7")[0] == 2
    code, out = cli("list", "--json")
    by_id = {d["id"]: d for d in json.loads(out)}
    assert (by_id["7.1"]["title"], by_id["7.1"]["brief"]) == ("T1", "B2")
    assert (by_id["7.2"]["title"], by_id["7.2"]["brief"]) == ("Other", "")
    code, out = cli("show", "7.1", "--json")
    assert json.loads(out)["brief"] == "B2"
    code, out = cli("show", "7.1")
    assert out.index("#7 T1") < out.index("B2") < out.index("Which way?")


def _v1_db(path: Path) -> None:
    raw = sqlite3.connect(path)
    for statement in inbox.SCHEMA.split(";"):
        if statement.strip():
            raw.execute(statement)
    raw.execute("PRAGMA user_version = 1")
    raw.execute(
        "INSERT INTO decisions (id, task, seq, session_id, session_name, question,"
        " created_at, updated_at) VALUES ('7.1', '7', 1, 's1', 'spec-s1', 'Old?',"
        " '2026-01-01T00:00:00+00:00', '2026-01-01T00:00:00+00:00')"
    )
    raw.execute(
        "INSERT INTO decision_options VALUES ('7.1', 'a', 'x'), ('7.1', 'b', 'y')"
    )
    raw.commit()
    raw.close()


def test_v1_db_migrates_to_v2_once(fake: Path) -> None:
    path = Path(os.environ["ZEN_FLOW_DB"])
    _v1_db(path)
    first = inbox.connect(path)
    assert first.execute("PRAGMA user_version").fetchone()[0] == 2
    [d] = inbox.select(first)
    assert (d.id, d.question, d.options, d.title) == (
        "7.1",
        "Old?",
        [("a", "x"), ("b", "y")],
        "",
    )
    schema = first.execute("SELECT sql FROM sqlite_master ORDER BY name").fetchall()
    first.close()
    again = inbox.connect(path)
    assert (
        again.execute("SELECT sql FROM sqlite_master ORDER BY name").fetchall()
        == schema
    )
    assert [x.id for x in inbox.select(again)] == ["7.1"]


def test_concurrent_first_opens_of_v1_db(fake: Path) -> None:
    _v1_db(Path(os.environ["ZEN_FLOW_DB"]))
    procs = [
        subprocess.Popen(
            [sys.executable, str(SCRIPT), "list", "--json"],
            env=dict(os.environ),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        for _ in range(6)
    ]
    results = [p.communicate() for p in procs]
    assert [p.returncode for p in procs] == [0] * 6, results
    assert all(json.loads(out)[0]["id"] == "7.1" for out, _ in results)


# --------------------------------------------------------------------------- #
# Answer flow                                                                   #
# --------------------------------------------------------------------------- #

Step = str | Callable[[list[str], list[tuple[str, str]]], str]


class ScriptedUI:
    """Plays a script of menu choices; a callable step can act mid-pass."""

    def __init__(
        self,
        steps: list[Step],
        writes: list[str] | None = None,
        picks: list[list[str]] | None = None,
        size: tuple[int, int] = (92, 29),
    ) -> None:
        self.steps = steps
        self.writes = writes or []
        self.picks = picks or []
        self.dims = size
        self.screens: list[list[str]] = []
        self.talked: list[str] = []
        self.paged: list[str] = []

    def size(self) -> tuple[int, int]:
        return self.dims

    def choose(
        self,
        lines: list[str],
        items: list[tuple[str, str]],
        height: int,
        show_help: bool,
    ) -> str:
        assert self.steps, f"unexpected menu after {self.screens[-1:]}"
        self.screens.append(lines)
        step = self.steps.pop(0)
        return step if isinstance(step, str) else step(lines, items)

    def pick_many(self, header: str, items: list[tuple[str, str]]) -> list[str]:
        return self.picks.pop(0)

    def write(self, header: str) -> str:
        return self.writes.pop(0)

    def text(self, placeholder: str) -> str:
        return "talked"

    def pager(self, text: str) -> None:
        self.paged.append(text)

    def talk(self, session_id: str) -> bool:
        self.talked.append(session_id)
        return True


def statuses(conn: sqlite3.Connection) -> dict[str, str]:
    return {d.id: d.status for d in inbox.select(conn)}


def run_pass(conn: sqlite3.Connection, ui: ScriptedUI) -> str:
    flash = str(inbox.answer_pass(conn, ui, "s1", [_session("s1")]))
    assert not ui.steps, "script not used up"
    return flash


def test_pass_pick_free_skip_then_send(fake: Path, conn: sqlite3.Connection) -> None:
    for _ in range(3):
        ask(conn)

    def review(lines: list[str], items: list[tuple[str, str]]) -> str:
        assert set(statuses(conn).values()) == {"open"}
        assert [v for _, v in items] == ["send", "change", "cancel"]
        assert any(line.startswith("7.3 ") and "skipped" in line for line in lines)
        return "send"

    ui = ScriptedUI(["b", "free", "skip", review], writes=["use the env var"])
    assert run_pass(conn, ui) == "held until 7.3"
    assert statuses(conn) == {"7.1": "answered", "7.2": "answered", "7.3": "open"}
    assert sends(fake) == []
    assert run_pass(conn, ScriptedUI(["a", "send"])).startswith(
        "sent 7.1, 7.2, 7.3 to spec-s1"
    )
    [argv] = sends(fake)
    assert "7.1 Which way? → b: right" in argv[2]
    assert "7.2 Which way? → free text: use the env var" in argv[2]
    assert "7.3 Which way? → a: left" in argv[2]


def test_question_menu_has_short_items_and_no_accept_all(
    conn: sqlite3.Connection,
) -> None:
    ask(conn)
    labels = [label for label, _ in inbox.question_menu(inbox.get(conn, "7.1"))]
    assert labels == ["a", "b", "Free text", "Skip", "Talk", "View full", "Back"]


def test_empty_free_text_returns_to_question(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn)
    ui = ScriptedUI(["free", "full", "a", "send"], writes=[""])
    assert run_pass(conn, ui).startswith("sent 7.1")
    assert ui.screens[0] == ui.screens[1]
    assert ui.paged and "a) left" in ui.paged[0]


@pytest.mark.parametrize("script", [["a", "skip", "cancel"], ["a", "back"], ["a", ""]])
def test_cancel_and_back_record_nothing(
    fake: Path, conn: sqlite3.Connection, script: list[Step]
) -> None:
    ask(conn)
    ask(conn)
    before = conn.execute("SELECT * FROM decisions ORDER BY id").fetchall()
    assert "nothing recorded" in run_pass(conn, ScriptedUI(script))
    assert conn.execute("SELECT * FROM decisions ORDER BY id").fetchall() == before
    assert sends(fake) == []


def test_change_one_rewalks_only_that_question(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn, question="First?")
    ask(conn, question="Second?")
    ui = ScriptedUI(["a", "a", "change", "7.2", "b", "send"])
    run_pass(conn, ui)
    rewalked = ui.screens[4]
    assert "7.2  Second?" in rewalked and "7.1  First?" not in rewalked
    assert [(d.id, d.answer_option) for d in inbox.select(conn)] == [
        ("7.1", "a"),
        ("7.2", "b"),
    ]
    assert len(sends(fake)) == 1


def test_answered_elsewhere_mid_pass_is_skipped(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn)
    ask(conn)
    other = inbox.connect(Path(os.environ["ZEN_FLOW_DB"]))

    def chief_answers(lines: list[str], items: list[tuple[str, str]]) -> str:
        inbox.answer(other, "7.2", "b", None)
        return "send"

    flash = run_pass(conn, ScriptedUI(["a", "a", chief_answers]))
    assert "7.2 already answered" in flash
    assert inbox.get(conn, "7.2").answer_option == "b"
    [argv] = sends(fake)
    assert "7.2 Which way? → b: right" in argv[2]


def test_amended_mid_pass_is_shown_again(fake: Path, conn: sqlite3.Connection) -> None:
    ask(conn)
    other = inbox.connect(Path(os.environ["ZEN_FLOW_DB"]))

    def asker_amends(lines: list[str], items: list[tuple[str, str]]) -> str:
        inbox.amend(
            other, "7.1", "s1", False, "Which road?", ["up", "down"], None, None
        )
        return "send"

    ui = ScriptedUI(["a", asker_amends, "b", "send"])
    flash = run_pass(conn, ui)
    assert "7.1 (amended)  Which road?" in ui.screens[2]
    assert "b) down" in ui.screens[2]
    assert flash.startswith("sent 7.1")
    assert inbox.get(conn, "7.1").answer_option == "b"
    [argv] = sends(fake)
    assert "7.1 Which road? → b: down" in argv[2]


def test_new_ask_mid_pass_walked_before_review(
    fake: Path, conn: sqlite3.Connection
) -> None:
    ask(conn)

    def asker_asks(lines: list[str], items: list[tuple[str, str]]) -> str:
        ask(inbox.connect(Path(os.environ["ZEN_FLOW_DB"])), question="Late?")
        return "a"

    ui = ScriptedUI([asker_asks, "b", "send"])
    run_pass(conn, ui)
    assert "7.2  Late?" in ui.screens[1]
    assert statuses(conn) == {"7.1": "answered", "7.2": "answered"}
    assert len(sends(fake)) == 1


def test_talk_resolves_only_chosen(fake: Path, conn: sqlite3.Connection) -> None:
    for _ in range(3):
        ask(conn)
    ui = ScriptedUI([], picks=[["7.1", "7.3"]])
    resolved, flash = inbox.talk_resolve(conn, ui, "s1")
    assert resolved == ["7.1", "7.3"] and ui.talked == ["s1"]
    assert statuses(conn) == {"7.1": "answered", "7.2": "open", "7.3": "answered"}
    assert {inbox.get(conn, i).answer_kind for i in ("7.1", "7.3")} == {"talk"}
    assert sends(fake) == []
    _, flash = inbox.talk_resolve(conn, ScriptedUI([], picks=[[]]), "s1")
    assert flash == "all left open" and statuses(conn)["7.2"] == "open"


def test_session_screen_talk_then_resolve(fake: Path, conn: sqlite3.Connection) -> None:
    ask(conn)
    ask(conn)
    ui = ScriptedUI(["talk"], picks=[["7.2"]])
    assert inbox.session_flow(conn, ui, "s1", inbox.am_sessions()) == "resolved 7.2"
    assert statuses(conn) == {"7.1": "open", "7.2": "answered"}


# --------------------------------------------------------------------------- #
# Layout                                                                        #
# --------------------------------------------------------------------------- #


def long_fixture(conn: sqlite3.Connection) -> None:
    long_option = words(300)
    inbox.ask(
        conn,
        "570",
        "Where do briefs live?",
        [long_option, "keep it in memory"],
        "b",
        words(400),
        False,
        "s1",
        "spec-s1",
        "Zen board",
        "\n".join([words(70), words(70), words(70)]),
    )


@pytest.mark.parametrize("width,height", [(92, 29), (60, 20), (40, 15)])
def test_screen_fits_and_keeps_question_and_options(
    conn: sqlite3.Connection, width: int, height: int
) -> None:
    long_fixture(conn)
    d = inbox.get(conn, "570.1")
    [brief] = inbox.briefs_for([d], [_session("s1")], inbox.datetime.now(inbox.UTC))
    layout = inbox.screen(d, brief, width, height)
    items = inbox.question_menu(d)
    assert all(len(line) <= width for line in layout.lines)
    assert (
        len(layout.lines)
        + inbox.menu_rows(len(items), layout.menu_height, layout.show_help)
        <= height
    )
    text = " ".join(line.strip() for line in layout.lines)
    assert "Where do briefs live?" in text
    for letter, option in inbox.get(conn, "570.1").options:
        assert f"{letter}) {option}" in text
    assert layout.cuts == list(inbox.CUT_ORDER[: len(layout.cuts)])
    assert all(len(label) < width - 2 for label, _ in items)


def test_screen_cuts_in_order_as_space_shrinks(conn: sqlite3.Connection) -> None:
    long_fixture(conn)
    d = inbox.get(conn, "570.1")
    [brief] = inbox.briefs_for([d], [_session("s1")], inbox.datetime.now(inbox.UTC))
    seen = [inbox.screen(d, brief, 92, h).cuts for h in range(60, 10, -1)]
    assert seen[0] == []
    assert [len(c) for c in seen] == sorted(len(c) for c in seen)
    assert {"brief", "context", "asked-by"} <= set(seen[-1])
    clipped = inbox.screen(d, brief, 92, 22)
    assert "context" in clipped.cuts
    assert any(line.endswith(inbox.MORE) for line in clipped.lines)


def test_overflowing_options_keep_the_question(conn: sqlite3.Connection) -> None:
    inbox.ask(
        conn,
        "7",
        "Which way?",
        [words(150), words(150), words(150)],
        "",
        "",
        False,
        "s1",
        "spec-s1",
    )
    d = inbox.get(conn, "7.1")
    [brief] = inbox.briefs_for([d], None, inbox.datetime.now(inbox.UTC))
    layout = inbox.screen(d, brief, 40, 15)
    assert layout.cuts == list(inbox.CUT_ORDER)
    assert layout.lines[0] == "7.1  Which way?"
    assert layout.lines[-1].endswith(inbox.MORE)
    assert len(layout.lines) + inbox.menu_rows(8, layout.menu_height, False) <= 15


def test_fit_pages_menu_before_dropping_lines() -> None:
    lines = [f"line {i}" for i in range(5)]
    layout = inbox.fit(lines, ["compact"], 6, 11)
    assert layout.lines == lines and layout.show_help
    assert 1 <= layout.menu_height < 6
    assert len(lines) + inbox.menu_rows(6, layout.menu_height, True) <= 11


def test_review_screen_fits(conn: sqlite3.Connection) -> None:
    long_fixture(conn)
    d = inbox.get(conn, "570.1")
    staged = inbox.Staged("pick", "a", "", d.rev)
    for width, height in [(92, 29), (60, 20), (40, 15)]:
        layout = inbox.review_screen([(d, staged)], width, height)
        assert all(len(line) <= width for line in layout.lines)
        assert (
            len(layout.lines) + inbox.menu_rows(3, layout.menu_height, layout.show_help)
            <= height
        )


# --------------------------------------------------------------------------- #
# Refresh and bell                                                              #
# --------------------------------------------------------------------------- #


def test_list_fingerprint_tracks_status_and_minute(conn: sqlite3.Connection) -> None:
    ask(conn)
    at = inbox.datetime(2026, 1, 1, 0, 0, 10, tzinfo=inbox.UTC)
    base = inbox.list_fingerprint(conn, [_session("s1")], at)
    assert inbox.list_fingerprint(conn, [_session("s1")], at) == base
    assert inbox.list_fingerprint(conn, [_session("s1", "busy")], at) != base
    later = at + inbox.timedelta(seconds=60)
    assert inbox.list_fingerprint(conn, [_session("s1")], later) != base


def test_brief_change_flips_fingerprint(conn: sqlite3.Connection) -> None:
    ask(conn)
    before = inbox.fingerprint(conn)
    inbox.set_brief(conn, "s1", "7", "Title", None)
    assert inbox.fingerprint(conn) != before


def test_one_bell_per_session_per_cycle(
    fake: Path, conn: sqlite3.Connection, monkeypatch: pytest.MonkeyPatch
) -> None:
    rung: list[str] = []
    monkeypatch.setattr(inbox, "bell", rung.append)
    poller = inbox.Poller(Path(os.environ["ZEN_FLOW_DB"]))
    ask(conn)
    poller.ring(conn)
    assert rung == []
    for _ in range(3):
        ask(conn, session="s1")
    ask(conn, session="s2")
    poller.ring(conn)
    assert sorted(rung) == ["spec-s1: 3 new decision(s)", "spec-s2: 1 new decision(s)"]
    poller.ring(conn)
    assert len(rung) == 2


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
