#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "typer>=0.12",
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/merge.py.

Run with:  uv run scripts/test_merge.py
"""

from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path

import pytest
from typer.testing import CliRunner

_spec = importlib.util.spec_from_file_location(
    "merge", Path(__file__).parent / "merge.py"
)
assert _spec is not None and _spec.loader is not None
merge = importlib.util.module_from_spec(_spec)
sys.modules["merge"] = merge
_spec.loader.exec_module(merge)
_real_board_note = merge.run_board_note


# --------------------------------------------------------------------------- #
# Fixtures                                                                      #
# --------------------------------------------------------------------------- #


def _git(*args: str, cwd: Path) -> str:
    return subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True, text=True, check=True
    ).stdout


def _commit(repo: Path, name: str, content: str, msg: str = "change") -> None:
    (repo / name).write_text(content)
    _git("add", "-A", cwd=repo)
    _git("commit", "-q", "-m", msg, cwd=repo)


@dataclass
class Fake:
    """Stands in for agent-manager, zen-release and zen-board."""

    sessions: list[dict[str, object]] = field(default_factory=list)
    calls: list[tuple[str, ...]] = field(default_factory=list)
    release_ok: bool = True
    releases: int = 0
    board_ok: bool = True
    # (task id, note, branch still present) per zen-board call
    boards: list[tuple[str, str, bool]] = field(default_factory=list)
    # (task id, note) per zen-board note call
    notes: list[tuple[str, str]] = field(default_factory=list)
    kill_ok: bool = True

    def agent_manager(self, *args: str) -> tuple[int, str]:
        self.calls.append(args)
        if args[0] == "sessions":
            return 0, json.dumps(self.sessions)
        if args[0] == "archive":
            return 0, "{}"
        if args[0] == "kill":
            return (0, "{}") if self.kill_ok else (1, "")
        return 1, ""

    def release(self, main: str) -> subprocess.CompletedProcess[str]:
        if merge.DRY_RUN:
            return subprocess.CompletedProcess([], 0, "", "")
        if not self.release_ok:
            return subprocess.CompletedProcess([], 1, "", "boom")
        self.releases += 1
        _git("tag", f"v0.0.{self.releases}-dev.0", cwd=Path(main))
        return subprocess.CompletedProcess([], 0, "", "")

    def board(
        self, main: str, task_id: str, note: str
    ) -> subprocess.CompletedProcess[str]:
        self.boards.append((task_id, note, merge.branch_exists(main, "am/task")))
        if not self.board_ok:
            return subprocess.CompletedProcess([], 1, "", "zen-board: HTTP 502")
        return subprocess.CompletedProcess([], 0, "", "")

    def note(
        self, main: str, task_id: str, note: str
    ) -> subprocess.CompletedProcess[str]:
        self.notes.append((task_id, note))
        if not self.board_ok:
            return subprocess.CompletedProcess([], 1, "", "zen-board: HTTP 502")
        return subprocess.CompletedProcess([], 0, "", "")


@dataclass
class Setup:
    main: Path
    worktree: Path
    fake: Fake


def _make(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    base: str = "dev",
    trailer: str = "\n\nTask: #568",
) -> Setup:
    main = tmp_path / "repo"
    main.mkdir()
    _git("init", "-q", "-b", base, cwd=main)
    _git("config", "user.email", "t@t.dev", cwd=main)
    _git("config", "user.name", "tester", cwd=main)
    _commit(main, "a.txt", "a\n", "init")
    wt = tmp_path / "wt"
    _git("worktree", "add", "-q", "-b", "am/task", str(wt), cwd=main)
    _commit(wt, "b.txt", "b\n", f"feat: b{trailer}")

    fake = Fake(
        sessions=[
            {
                "id": "s1",
                "name": "task",
                "directory": str(wt),
                "branch": "am/task",
                "status": "finished",
            }
        ]
    )
    monkeypatch.setattr(merge._sessions, "agent_manager", fake.agent_manager)
    monkeypatch.setattr(merge, "run_release", fake.release)
    monkeypatch.setattr(merge, "run_board", fake.board)
    monkeypatch.setattr(merge, "run_board_note", fake.note)
    monkeypatch.setattr(merge, "DRY_RUN", False)
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))
    monkeypatch.setenv("AGENT_MANAGER_SESSION_ID", "orch")
    return Setup(main=main, worktree=wt, fake=fake)


@pytest.fixture
def setup(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Setup:
    return _make(tmp_path, monkeypatch)


def _sha(repo: Path, ref: str) -> str:
    return _git("log", "-1", "--format=%h", ref, cwd=repo).strip()


def _code() -> int:
    with pytest.raises(merge.Stop) as exc:
        merge.land("task", "dev")
    code: int = exc.value.code
    return code


# --------------------------------------------------------------------------- #
# Happy paths                                                                   #
# --------------------------------------------------------------------------- #


def test_lands_releases_and_cleans_up(setup: Setup) -> None:
    sha = _sha(setup.worktree, "HEAD")
    state = merge.land("task", "dev")
    assert setup.fake.boards == [("568", f"v0.0.1-dev.0 · {sha}", False)]
    assert state.board == "done"
    assert merge.summary_line(state) == "#568 · v0.0.1-dev.0 · cleanup ok"
    assert (setup.main / "b.txt").exists()
    assert not setup.worktree.exists()
    assert "am/task" not in _git("branch", "--list", cwd=setup.main)
    assert ("archive", "s1", "--json") in setup.fake.calls
    assert not merge.state_path("task").exists()


def test_dry_run_changes_nothing(setup: Setup, monkeypatch: pytest.MonkeyPatch) -> None:
    head = _git("rev-parse", "HEAD", cwd=setup.main)
    monkeypatch.setattr(merge, "DRY_RUN", True)
    state = merge.land("task", "dev")
    assert (state.tag, state.board) == ("(dry-run)", "(dry-run)")
    assert _git("rev-parse", "HEAD", cwd=setup.main) == head
    assert setup.worktree.exists()
    assert not any(c[0] in ("archive", "kill") for c in setup.fake.calls)
    assert not merge.state_path("task").exists()


def test_dry_run_summary_and_json(setup: Setup) -> None:
    res = CliRunner().invoke(merge.app, ["task", "--dry-run"])
    assert res.exit_code == 0
    assert "dry-run, nothing changed" in res.stdout
    assert "cleanup ok" not in res.stdout
    res = CliRunner().invoke(merge.app, ["task", "--dry-run", "--json"])
    assert json.loads(res.stdout)["cleanup"] == "dry-run"


def test_kills_the_worker_after_archiving_it(setup: Setup) -> None:
    state = merge.land("task", "dev")
    kinds = [c[0] for c in setup.fake.calls]
    assert kinds.index("archive") < kinds.index("kill")
    assert ("kill", "s1", "--json") in setup.fake.calls
    assert [c for c in setup.fake.calls if c[0] == "kill"] == [("kill", "s1", "--json")]
    assert state.stopped == "killed"


@pytest.mark.parametrize(
    "dead", [{"status": "dead"}, {"status": "errored"}, {"running": False}]
)
def test_skips_kill_when_already_dead(setup: Setup, dead: dict[str, object]) -> None:
    setup.fake.sessions[0].update(dead)
    state = merge.land("task", "dev")
    assert not any(c[0] == "kill" for c in setup.fake.calls)
    assert state.stopped == "already dead"


def test_failing_kill_only_warns(setup: Setup) -> None:
    setup.fake.kill_ok = False
    res = CliRunner().invoke(merge.app, ["task", "--json"])
    assert res.exit_code == 0
    assert json.loads(res.stdout)["stopped"] == "failed"
    assert "kill s1 failed" in res.stderr
    assert not setup.worktree.exists()
    assert not merge.state_path("task").exists()


def test_never_kills_the_calling_session(
    setup: Setup, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("AGENT_MANAGER_SESSION_ID", "s1")
    state = merge.land("task", "dev")
    assert not any(c[0] == "kill" for c in setup.fake.calls)
    assert state.stopped == "self"
    assert not setup.worktree.exists()


def test_dry_run_prints_the_kill(setup: Setup) -> None:
    res = CliRunner().invoke(merge.app, ["task", "--dry-run", "--json"])
    assert res.exit_code == 0
    assert "[dry-run] agent-manager kill s1 --json" in res.stderr
    assert json.loads(res.stdout)["stopped"] == "(dry-run)"
    assert not any(c[0] == "kill" for c in setup.fake.calls)


# --------------------------------------------------------------------------- #
# Stops                                                                         #
# --------------------------------------------------------------------------- #


def test_gate_hit(setup: Setup) -> None:
    _commit(setup.worktree, "c.test.ts", "it.only('x', () => {})\n")
    assert _code() == merge.EXIT_GATE
    assert "b.txt" not in _git("ls-files", cwd=setup.main)


def test_not_fast_forward(setup: Setup) -> None:
    _commit(setup.main, "z.txt", "z\n")
    assert _code() == merge.EXIT_NOT_FF


@pytest.mark.parametrize("where", ["worktree", "main"])
def test_dirty_tree(setup: Setup, where: str) -> None:
    target = setup.worktree if where == "worktree" else setup.main
    (target / "a.txt").write_text("dirty\n")
    assert _code() == merge.EXIT_DIRTY


def test_main_checkout_on_other_branch(setup: Setup) -> None:
    _git("checkout", "-q", "-b", "other", cwd=setup.main)
    assert _code() == merge.EXIT_DIRTY


def test_release_failure_then_resume(setup: Setup) -> None:
    setup.fake.release_ok = False
    assert _code() == merge.EXIT_RELEASE
    saved = merge.load_state("task")
    assert saved is not None
    assert saved.phases["merge"] == "done" and saved.phases["release"] == "failed"

    setup.fake.release_ok = True
    state = merge.land("task", "dev")
    assert state.tag == "v0.0.1-dev.0"
    assert not setup.worktree.exists()


def test_resume_regates_a_moved_branch(setup: Setup) -> None:
    _commit(setup.main, "z.txt", "z\n")
    assert _code() == merge.EXIT_NOT_FF
    _git("rebase", "-q", "dev", cwd=setup.worktree)
    _commit(setup.worktree, "c.test.ts", "it.only('x', () => {})\n")
    assert _code() == merge.EXIT_GATE


def test_resume_after_manual_merge(setup: Setup) -> None:
    _commit(setup.main, "z.txt", "z\n")
    assert _code() == merge.EXIT_NOT_FF
    _git("merge", "-q", "--no-edit", "am/task", cwd=setup.main)
    state = merge.land("task", "dev")
    assert state.tag == "v0.0.1-dev.0"
    assert not setup.worktree.exists()


def test_name_prefers_live_session(setup: Setup) -> None:
    live = setup.fake.sessions[0]
    stale = {**live, "id": "old", "directory": "/gone", "archived": True}
    setup.fake.sessions = [stale, live]
    state = merge.land("task", "dev")
    assert state.session_id == "s1"


@pytest.mark.parametrize("single", ["master", "main"])
def test_single_branch_lands_without_release(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, single: str
) -> None:
    s = _make(tmp_path, monkeypatch, base=single)
    state = merge.land("task", "dev")
    assert (state.base, state.tag, state.release) == (single, "push pending", False)
    assert s.fake.releases == 0
    assert s.fake.boards[0][1].startswith("push pending · ")
    assert (s.main / "b.txt").exists()


@pytest.mark.parametrize("on", ["prod", "feature"])
def test_no_dev_and_other_checkout_refused(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, on: str
) -> None:
    s = _make(tmp_path, monkeypatch, base=on)
    head = _git("rev-parse", "HEAD", cwd=s.main)
    with pytest.raises(merge.Stop) as exc:
        merge.land("task", "dev")
    assert exc.value.code == merge.EXIT_REFUSED
    assert repr(on) in str(exc.value) and "Branch model" in str(exc.value)
    assert _git("rev-parse", "HEAD", cwd=s.main) == head
    assert s.worktree.exists()
    assert s.fake.releases == 0


def test_base_prod_refused(setup: Setup) -> None:
    _git("branch", "prod", cwd=setup.main)
    with pytest.raises(merge.Stop) as exc:
        merge.land("task", "prod")
    assert exc.value.code == merge.EXIT_REFUSED
    assert "b.txt" not in _git("ls-tree", "--name-only", "prod", cwd=setup.main)


def test_explicit_non_dev_base_never_releases(setup: Setup) -> None:
    _git("branch", "staging", cwd=setup.main)
    _git("checkout", "-q", "staging", cwd=setup.main)
    state = merge.land("task", "staging")
    assert (state.tag, setup.fake.releases) == ("push pending", 0)


def test_single_branch_lands_on_saved_target(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = _make(tmp_path, monkeypatch, base="master")
    common = _git("rev-parse", "--git-common-dir", cwd=s.main).strip()
    (s.main / common / "zen-release.json").write_text(
        json.dumps({"branches": {"source": "", "target": "master"}})
    )
    state = merge.land("task", "dev")
    assert (state.base, state.tag) == ("master", "push pending")


def test_refuses_release_target(setup: Setup) -> None:
    common = _git("rev-parse", "--git-common-dir", cwd=setup.main).strip()
    (setup.main / common / "zen-release.json").write_text(
        json.dumps({"branches": {"source": "", "target": "dev"}})
    )
    assert _code() == merge.EXIT_REFUSED


def test_unknown_session(setup: Setup) -> None:
    setup.fake.sessions = []
    assert _code() == merge.EXIT_ERROR
    assert not merge.state_path("task").exists()


# --------------------------------------------------------------------------- #
# Board                                                                         #
# --------------------------------------------------------------------------- #


def test_board_skipped_without_task_trailer(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = _make(tmp_path, monkeypatch, trailer="")
    state = merge.land("task", "dev")
    assert (state.task_id, state.board) == ("", "skipped")
    assert s.fake.boards == []
    assert merge.summary_line(state).endswith("cleanup ok · board skipped")


def test_board_failure_exits_7_then_resumes_only_board(setup: Setup) -> None:
    setup.fake.board_ok = False
    assert _code() == merge.EXIT_BOARD
    saved = merge.load_state("task")
    assert saved is not None
    assert saved.phases["branch"] == "done" and saved.phases["board"] == "failed"
    calls = len(setup.fake.calls)

    setup.fake.board_ok = True
    state = merge.land("task", "dev")
    assert state.board == "done"
    assert setup.fake.releases == 1
    assert len(setup.fake.calls) == calls
    assert [b[1] for b in setup.fake.boards] == [setup.fake.boards[0][1]] * 2
    assert not merge.state_path("task").exists()


def test_board_note_keeps_shas_after_manual_merge(setup: Setup) -> None:
    sha = _sha(setup.worktree, "HEAD")
    _commit(setup.main, "z.txt", "z\n")
    assert _code() == merge.EXIT_NOT_FF
    _git("merge", "-q", "--no-edit", "am/task", cwd=setup.main)
    merge.land("task", "dev")
    assert setup.fake.boards[-1][1] == f"v0.0.1-dev.0 · {sha}"


def test_run_board_dry_run_echoes(
    setup: Setup, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.undo()
    monkeypatch.setattr(merge, "DRY_RUN", True)
    assert merge.run_board(str(setup.main), "568", "tag · abc").returncode == 0
    err = capsys.readouterr().err
    assert "board.py done 568 --note 'tag · abc'" in err


def _stop_not_ff(s: Setup) -> None:
    _commit(s.main, "z.txt", "z\n")
    assert _code() == merge.EXIT_NOT_FF


def test_restart_after_manual_merge_recovers_task(setup: Setup) -> None:
    sha = _sha(setup.worktree, "HEAD")
    _stop_not_ff(setup)
    _git("merge", "-q", "--no-edit", "am/task", cwd=setup.main)
    state = merge.land("task", "dev", restart=True)
    assert state.task_id == "568"
    assert setup.fake.boards == [("568", f"v0.0.1-dev.0 · {sha}", False)]


def test_manual_merge_without_prior_run(setup: Setup) -> None:
    sha = _sha(setup.worktree, "HEAD")
    _git("merge", "-q", "--ff-only", "am/task", cwd=setup.main)
    state = merge.land("task", "dev")
    assert (state.task_id, state.board, state.shas) == ("568", "done", [sha])


def test_rebase_then_manual_merge_lists_new_shas(setup: Setup) -> None:
    old = _sha(setup.worktree, "HEAD")
    _stop_not_ff(setup)
    _git("rebase", "-q", "dev", cwd=setup.worktree)
    new = _sha(setup.worktree, "HEAD")
    assert new != old
    _git("merge", "-q", "--ff-only", "am/task", cwd=setup.main)
    merge.land("task", "dev")
    note = setup.fake.boards[-1][1]
    assert new in note and old not in note


def _expire_reflog(repo: Path) -> None:
    _git("reflog", "expire", "--expire=now", "--all", cwd=repo)


def test_no_reflog_keeps_saved_shas_on_branch(setup: Setup) -> None:
    sha = _sha(setup.worktree, "HEAD")
    (setup.main / "a.txt").write_text("dirty\n")
    assert _code() == merge.EXIT_DIRTY
    _git("checkout", "-q", "a.txt", cwd=setup.main)
    _git("reset", "-q", "--hard", "am/task", cwd=setup.main)
    _expire_reflog(setup.main)
    state = merge.land("task", "dev")
    assert state.shas == [sha]


def test_no_reflog_after_rebase_falls_back_to_tip(setup: Setup) -> None:
    _stop_not_ff(setup)
    _git("rebase", "-q", "dev", cwd=setup.worktree)
    tip = _sha(setup.worktree, "HEAD")
    _git("reset", "-q", "--hard", "am/task", cwd=setup.main)
    _expire_reflog(setup.main)
    state = merge.land("task", "dev")
    assert state.shas == [tip]


def test_phase_1_of_2_notes_and_leaves_task_open(setup: Setup) -> None:
    sha = _sha(setup.worktree, "HEAD")
    res = CliRunner().invoke(merge.app, ["task", "--phase", "1/2", "--json"])
    assert res.exit_code == 0
    out = json.loads(res.stdout)
    assert (out["phase"], out["board"]) == ("1/2", "noted")
    assert setup.fake.boards == []
    assert setup.fake.notes == [("568", f"phase 1/2 · v0.0.1-dev.0 · {sha}")]


def test_phase_summary_ends_with_board_noted(setup: Setup) -> None:
    state = merge.land("task", "dev", phase="1/2")
    assert state.board == "noted"
    assert merge.summary_line(state).endswith(
        "#568 · v0.0.1-dev.0 · cleanup ok · board noted (phase 1/2)"
    )


def test_final_phase_closes_task_with_prefixed_note(setup: Setup) -> None:
    state = merge.land("task", "dev", phase="2/2")
    assert state.board == "done"
    assert setup.fake.notes == []
    assert setup.fake.boards[0][1].startswith("phase 2/2 · v0.0.1-dev.0 · ")


def test_phase_flag_on_rerun_overrides_saved_state(setup: Setup) -> None:
    setup.fake.board_ok = False
    assert _code() == merge.EXIT_BOARD
    setup.fake.board_ok = True
    state = merge.land("task", "dev", phase="1/3")
    assert state.board == "noted"
    assert setup.fake.boards[0][0] == "568"


def test_noted_board_failure_exits_7(setup: Setup) -> None:
    setup.fake.board_ok = False
    res = CliRunner().invoke(merge.app, ["task", "--phase", "1/2"])
    assert res.exit_code == merge.EXIT_BOARD


@pytest.mark.parametrize("value", ["3/2", "0/2", "x", "1/", "1/0", "-1/2"])
def test_invalid_phase_exits_1(setup: Setup, value: str) -> None:
    res = CliRunner().invoke(merge.app, ["task", "--phase", value])
    assert res.exit_code == merge.EXIT_ERROR
    assert setup.worktree.exists() and setup.fake.calls == []


def test_task_flag_wins_over_trailer(
    setup: Setup, capsys: pytest.CaptureFixture[str]
) -> None:
    state = merge.land("task", "dev", task="#900")
    assert (state.task_id, setup.fake.boards[0][0]) == ("900", "900")
    assert "--task 900 overrides Task: 568" in capsys.readouterr().err


def test_task_flag_without_trailer_runs_board(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = _make(tmp_path, monkeypatch, trailer="")
    state = merge.land("task", "dev", task="700")
    assert (state.task_id, state.board) == ("700", "done")
    assert s.fake.boards[0][0] == "700"


def test_dry_run_phase_echoes_board_note(
    setup: Setup, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setattr(merge, "run_board_note", _real_board_note)
    monkeypatch.setattr(merge, "DRY_RUN", True)
    state = merge.land("task", "dev", phase="1/2")
    assert state.board == "(dry-run)"
    assert "board.py note 568 'phase 1/2 · (dry-run) · " in capsys.readouterr().err


# --------------------------------------------------------------------------- #
# Branch cleanup                                                                #
# --------------------------------------------------------------------------- #


def test_delete_branch_force_only_when_merged(setup: Setup) -> None:
    _git("merge", "-q", "--ff-only", "am/task", cwd=setup.main)
    _git("worktree", "remove", str(setup.worktree), cwd=setup.main)
    _git("checkout", "-q", "-b", "elsewhere", "HEAD~1", cwd=setup.main)
    merge.delete_branch(str(setup.main), "am/task", "dev")
    assert "am/task" not in _git("branch", "--list", cwd=setup.main)

    _git("branch", "unmerged", cwd=setup.main)
    _commit(setup.main, "u.txt", "u\n")
    _git("checkout", "-q", "dev", cwd=setup.main)
    _git("branch", "-f", "unmerged", "elsewhere", cwd=setup.main)
    with pytest.raises(merge.Stop):
        merge.delete_branch(str(setup.main), "unmerged", "dev")


def test_cli_json(setup: Setup) -> None:
    res = CliRunner().invoke(merge.app, ["task", "--json"])
    assert res.exit_code == 0
    out = json.loads(res.stdout)
    assert (out["task_id"], out["tag"], out["cleanup"]) == ("568", "v0.0.1-dev.0", "ok")
    assert out["board"] == "done"


def test_no_dev_explicit_base_skips_release(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = _make(tmp_path, monkeypatch, base="master")
    state = merge.land("task", "master")
    assert (state.base, state.tag, state.release) == ("master", "push pending", False)
    assert s.fake.releases == 0
    assert (s.main / "b.txt").exists()


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
