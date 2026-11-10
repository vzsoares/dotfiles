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
    """Stands in for agent-manager and zen-release."""

    sessions: list[dict[str, object]] = field(default_factory=list)
    calls: list[tuple[str, ...]] = field(default_factory=list)
    release_ok: bool = True
    releases: int = 0

    def agent_manager(self, *args: str) -> tuple[int, str]:
        self.calls.append(args)
        if args[0] == "sessions":
            return 0, json.dumps(self.sessions)
        if args[0] == "archive":
            return 0, "{}"
        return 1, ""

    def release(self, main: str) -> subprocess.CompletedProcess[str]:
        if merge.DRY_RUN:
            return subprocess.CompletedProcess([], 0, "", "")
        if not self.release_ok:
            return subprocess.CompletedProcess([], 1, "", "boom")
        self.releases += 1
        _git("tag", f"v0.0.{self.releases}-dev.0", cwd=Path(main))
        return subprocess.CompletedProcess([], 0, "", "")


@dataclass
class Setup:
    main: Path
    worktree: Path
    fake: Fake


def _make(tmp_path: Path, monkeypatch: pytest.MonkeyPatch, base: str = "dev") -> Setup:
    main = tmp_path / "repo"
    main.mkdir()
    _git("init", "-q", "-b", base, cwd=main)
    _git("config", "user.email", "t@t.dev", cwd=main)
    _git("config", "user.name", "tester", cwd=main)
    _commit(main, "a.txt", "a\n", "init")
    wt = tmp_path / "wt"
    _git("worktree", "add", "-q", "-b", "am/task", str(wt), cwd=main)
    _commit(wt, "b.txt", "b\n", "feat: b\n\nTask: #568")

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
    monkeypatch.setattr(merge, "agent_manager", fake.agent_manager)
    monkeypatch.setattr(merge, "run_release", fake.release)
    monkeypatch.setattr(merge, "DRY_RUN", False)
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))
    return Setup(main=main, worktree=wt, fake=fake)


@pytest.fixture
def setup(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Setup:
    return _make(tmp_path, monkeypatch)


def _code() -> int:
    with pytest.raises(merge.Stop) as exc:
        merge.land("task", "dev")
    code: int = exc.value.code
    return code


# --------------------------------------------------------------------------- #
# Happy paths                                                                   #
# --------------------------------------------------------------------------- #


def test_lands_releases_and_cleans_up(setup: Setup) -> None:
    state = merge.land("task", "dev")
    assert merge.summary_line(state) == "#568 · v0.0.1-dev.0 · cleanup ok"
    assert (setup.main / "b.txt").exists()
    assert not setup.worktree.exists()
    assert "am/task" not in _git("branch", "--list", cwd=setup.main)
    assert ("archive", "s1", "--json") in setup.fake.calls
    assert not merge.state_path("task").exists()


def test_no_dev_branch_skips_release(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = _make(tmp_path, monkeypatch, base="master")
    state = merge.land("task", "dev")
    assert (state.base, state.tag, state.release) == ("master", "push pending", False)
    assert s.fake.releases == 0
    assert (s.main / "b.txt").exists()


def test_dry_run_changes_nothing(setup: Setup, monkeypatch: pytest.MonkeyPatch) -> None:
    head = _git("rev-parse", "HEAD", cwd=setup.main)
    monkeypatch.setattr(merge, "DRY_RUN", True)
    state = merge.land("task", "dev")
    assert state.tag == "(dry-run)"
    assert _git("rev-parse", "HEAD", cwd=setup.main) == head
    assert setup.worktree.exists()
    assert not any(c[0] == "archive" for c in setup.fake.calls)
    assert not merge.state_path("task").exists()


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


def test_refuses_prod_and_main(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    _make(tmp_path, monkeypatch, base="main")
    with pytest.raises(merge.Stop) as exc:
        merge.land("task", "main")
    assert exc.value.code == merge.EXIT_REFUSED
    with pytest.raises(merge.Stop) as exc:
        merge.land("task", "dev")
    assert exc.value.code == merge.EXIT_REFUSED


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


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
