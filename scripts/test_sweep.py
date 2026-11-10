#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "typer>=0.12",
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/sweep.py.

Run with:  uv run scripts/test_sweep.py
"""

from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
from pathlib import Path
from typing import Protocol

import pytest

_spec = importlib.util.spec_from_file_location(
    "sweep", Path(__file__).parent / "sweep.py"
)
assert _spec is not None and _spec.loader is not None
sweep = importlib.util.module_from_spec(_spec)
sys.modules["sweep"] = sweep
_spec.loader.exec_module(sweep)


def _git(*args: str, cwd: Path) -> str:
    return subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True, text=True, check=True
    ).stdout


def _init(path: Path, branch: str = "dev") -> Path:
    path.mkdir(parents=True)
    _git("init", "-q", "-b", branch, cwd=path)
    _git("config", "user.email", "t@t.dev", cwd=path)
    _git("config", "user.name", "tester", cwd=path)
    (path / "a.txt").write_text("a\n")
    _git("add", ".", cwd=path)
    _git("commit", "-q", "-m", "init", cwd=path)
    return path


class _Finding(Protocol):
    kind: str
    detail: str


def _kinds(findings: list[_Finding]) -> list[tuple[str, str]]:
    return sorted((f.kind, f.detail) for f in findings)


@pytest.fixture
def remote_repo(tmp_path: Path) -> Path:
    """A clone of a bare remote, dev tracking origin/dev, nothing loose."""
    seed = _init(tmp_path / "seed")
    bare = tmp_path / "remote.git"
    _git("clone", "-q", "--bare", str(seed), str(bare), cwd=tmp_path)
    clone = tmp_path / "clone"
    _git("clone", "-q", str(bare), str(clone), cwd=tmp_path)
    _git("config", "user.email", "t@t.dev", cwd=clone)
    _git("config", "user.name", "tester", cwd=clone)
    return clone


def test_clean_repo(remote_repo: Path) -> None:
    assert sweep.sweep_repo(remote_repo, live_dirs=set()) == []


def test_dirty_unpushed_and_stash(remote_repo: Path) -> None:
    (remote_repo / "a.txt").write_text("b\n")
    _git("stash", "push", "-q", "-m", "sweep-test", cwd=remote_repo)
    (remote_repo / "n.txt").write_text("n\n")
    _git("add", ".", cwd=remote_repo)
    _git("commit", "-q", "-m", "local", cwd=remote_repo)
    (remote_repo / "a.txt").write_text("c\n")
    kinds = _kinds(sweep.sweep_repo(remote_repo, live_dirs=set()))
    assert kinds == [
        ("dirty", f"{remote_repo} (1 changed)"),
        ("stash", "1 entry"),
        ("unpushed", "dev [ahead 1]"),
    ]


def test_no_upstream_and_merged(remote_repo: Path) -> None:
    _git("branch", "feature", cwd=remote_repo)
    kinds = _kinds(sweep.sweep_repo(remote_repo, live_dirs=set()))
    assert kinds == [("merged", "feature (into dev)"), ("no-upstream", "feature")]


def test_no_remote_skips_upstream_check(tmp_path: Path) -> None:
    repo = _init(tmp_path / "local", branch="master")
    _git("branch", "topic", cwd=repo)
    _git("checkout", "-q", "topic", cwd=repo)
    (repo / "t.txt").write_text("t\n")
    _git("add", ".", cwd=repo)
    _git("commit", "-q", "-m", "t", cwd=repo)
    assert sweep.sweep_repo(repo, live_dirs=set()) == []


def test_worktrees_and_am_branches(tmp_path: Path) -> None:
    repo = _init(tmp_path / "repo", branch="master")
    live = tmp_path / "wt-live"
    dead = tmp_path / "wt-dead"
    for wt, br in ((live, "am/live"), (dead, "am/dead")):
        _git("worktree", "add", "-q", "-b", br, str(wt), cwd=repo)
        (wt / f"{br[3:]}.txt").write_text("x\n")
        _git("add", ".", cwd=wt)
        _git("commit", "-q", "-m", br, cwd=wt)
    _git("branch", "am/orphan", "am/dead", cwd=repo)

    trees = sweep.worktrees(repo)
    assert (trees[0].branch, trees[0].main) == ("master", True)
    assert sorted(w.branch for w in trees[1:]) == ["am/dead", "am/live"]
    findings = sweep.sweep_repo(repo, live_dirs={str(live.resolve())})
    assert _kinds(findings) == [
        ("orphan-branch", "am/orphan"),
        ("orphan-worktree", str(dead)),
    ]

    skipped = sweep.sweep_repo(repo, live_dirs=None)
    assert _kinds(skipped) == [("orphan-branch", "am/orphan")]


def test_collect_repos_dedupes_and_walks(tmp_path: Path) -> None:
    root = tmp_path / "work"
    a = _init(root / "back" / "api")
    b = _init(root / "front")
    personal = tmp_path / "personal"
    c = _init(personal / "dots", branch="master")
    _git("worktree", "add", "-q", "-b", "am/x", str(personal / "dots-wt"), cwd=c)
    (personal / "notes").mkdir()
    config = {
        "repos": {
            "root": str(root),
            "release_routine": [str(a), str(tmp_path / "gone")],
        }
    }
    assert sweep.collect_repos(config, personal) == [a, b, c]


def test_vault_config(tmp_path: Path) -> None:
    vault = tmp_path / "vault"
    (vault / "config").mkdir(parents=True)
    (vault / "config" / "zen-flow.json").write_text(
        json.dumps({"repos": {"root": "~/w"}})
    )
    (tmp_path / ".claude").mkdir()
    (tmp_path / ".claude" / "second-brain.json").write_text(
        json.dumps({"vault": str(vault)})
    )
    assert sweep.vault_config(tmp_path) == {"repos": {"root": "~/w"}}
    assert sweep.vault_config(tmp_path / "nobody") == {}


def test_run_sweep_notes_missing_agent_manager(tmp_path: Path) -> None:
    repo = _init(tmp_path / "r")
    result = sweep.run_sweep([repo], None)
    assert result.findings == []
    assert result.notes


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
