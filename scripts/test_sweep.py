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
    assert sweep.sweep_repo(remote_repo, live_dirs=set()).findings == []


def test_dirty_unpushed_and_stash(remote_repo: Path) -> None:
    (remote_repo / "a.txt").write_text("b\n")
    _git("stash", "push", "-q", "-m", "sweep-test", cwd=remote_repo)
    (remote_repo / "n.txt").write_text("n\n")
    _git("add", ".", cwd=remote_repo)
    _git("commit", "-q", "-m", "local", cwd=remote_repo)
    (remote_repo / "a.txt").write_text("c\n")
    kinds = _kinds(sweep.sweep_repo(remote_repo, live_dirs=set()).findings)
    assert kinds == [
        ("dirty", f"{remote_repo} (1 changed)"),
        ("stash", "1 entry"),
        ("unpushed", "dev [ahead 1]"),
    ]


def test_local_hooks_path_reported(remote_repo: Path, tmp_path: Path) -> None:
    other = _init(tmp_path / "plain")
    assert sweep.sweep_repo(other, live_dirs=set()).findings == []
    _git("config", "core.hooksPath", "custom-hooks", cwd=remote_repo)
    assert _kinds(sweep.sweep_repo(remote_repo, live_dirs=set()).findings) == [
        ("hooks-path", "custom-hooks (skips the global prod-push hook)")
    ]


def test_no_upstream_and_merged(remote_repo: Path) -> None:
    _git("branch", "feature", cwd=remote_repo)
    kinds = _kinds(sweep.sweep_repo(remote_repo, live_dirs=set()).findings)
    assert kinds == [("merged", "feature (into dev)"), ("no-upstream", "feature")]


def test_no_remote_skips_upstream_check(tmp_path: Path) -> None:
    repo = _init(tmp_path / "local", branch="master")
    _git("branch", "topic", cwd=repo)
    _git("checkout", "-q", "topic", cwd=repo)
    (repo / "t.txt").write_text("t\n")
    _git("add", ".", cwd=repo)
    _git("commit", "-q", "-m", "t", cwd=repo)
    assert sweep.sweep_repo(repo, live_dirs=set()).findings == []


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
    findings = sweep.sweep_repo(repo, live_dirs={str(live.resolve())}).findings
    assert _kinds(findings) == [
        ("orphan-branch", "am/orphan"),
        ("orphan-worktree", str(dead)),
    ]

    skipped = sweep.sweep_repo(repo, live_dirs=None).findings
    assert _kinds(skipped) == [("orphan-branch", "am/orphan")]


@pytest.fixture
def live_worker(remote_repo: Path, tmp_path: Path) -> tuple[Path, Path]:
    """remote_repo plus a dirty, upstream-less, already-merged am/* worktree."""
    wt = tmp_path / "wt-live"
    _git("worktree", "add", "-q", "-b", "am/live", str(wt), cwd=remote_repo)
    (wt / "a.txt").write_text("dirty\n")
    return remote_repo, wt


def test_live_worker_worktree_is_skipped(live_worker: tuple[Path, Path]) -> None:
    repo, wt = live_worker
    result = sweep.sweep_repo(repo, live_dirs={str(wt.resolve())})
    assert result.findings == []
    assert result.notes == [f"live worker: {wt} (am/live)"]


def test_live_worker_checked_when_agent_manager_unavailable(
    live_worker: tuple[Path, Path],
) -> None:
    repo, wt = live_worker
    result = sweep.sweep_repo(repo, live_dirs=None)
    assert result.notes == []
    assert _kinds(result.findings) == [
        ("dirty", f"{wt} (1 changed)"),
        ("merged", "am/live (into dev)"),
        ("no-upstream", "am/live"),
    ]


def test_dead_worktree_still_orphan(live_worker: tuple[Path, Path]) -> None:
    repo, wt = live_worker
    result = sweep.sweep_repo(repo, live_dirs=set())
    assert ("orphan-worktree", str(wt)) in _kinds(result.findings)
    assert result.notes == []


def test_live_session_in_main_checkout_does_not_hide_dirty(
    remote_repo: Path,
) -> None:
    (remote_repo / "a.txt").write_text("dirty\n")
    result = sweep.sweep_repo(remote_repo, live_dirs={str(remote_repo.resolve())})
    assert _kinds(result.findings) == [("dirty", f"{remote_repo} (1 changed)")]
    assert result.notes == []


def test_run_sweep_live_workers_only_is_clean(live_worker: tuple[Path, Path]) -> None:
    repo, wt = live_worker
    result = sweep.run_sweep([repo], {str(wt.resolve())})
    assert result.findings == []
    assert result.notes == [f"live worker: {wt} (am/live)"]


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


def _models(repo: Path) -> list[str]:
    findings = sweep.sweep_repo(repo, live_dirs=set(), personal=True).findings
    return sorted(f.detail for f in findings if f.kind == "branch-model")


def test_branch_model_dev_or_prod_alone(tmp_path: Path) -> None:
    assert _models(_init(tmp_path / "d", branch="dev")) == ["dev without prod"]
    assert _models(_init(tmp_path / "p", branch="prod")) == ["prod without dev"]


@pytest.mark.parametrize("old", ["main", "master"])
def test_branch_model_leftover_single_branch(tmp_path: Path, old: str) -> None:
    repo = _init(tmp_path / "r", branch="dev")
    _git("branch", "prod", cwd=repo)
    assert _models(repo) == []
    _git("branch", old, cwd=repo)
    assert _models(repo) == [f"leftover {old} beside dev+prod"]


def test_branch_model_origin_head(remote_repo: Path) -> None:
    _git("branch", "prod", cwd=remote_repo)
    assert _models(remote_repo) == []
    _git("remote", "set-head", "origin", "--delete", cwd=remote_repo)
    assert _models(remote_repo)[0].startswith("origin/HEAD is unset, not origin/dev")
    _git("push", "-q", "--no-verify", "origin", "prod", cwd=remote_repo)
    _git("remote", "set-head", "origin", "prod", cwd=remote_repo)
    assert _models(remote_repo)[0].startswith("origin/HEAD is origin/prod")


@pytest.mark.parametrize("single", ["main", "master"])
def test_branch_model_single_branch_is_valid(tmp_path: Path, single: str) -> None:
    repo = _init(tmp_path / "r", branch=single)
    assert _models(repo) == []


def test_branch_model_dev_prod_without_remote(tmp_path: Path) -> None:
    repo = _init(tmp_path / "r", branch="dev")
    _git("branch", "prod", cwd=repo)
    assert _models(repo) == []


def test_branch_model_only_for_personal_repos(tmp_path: Path) -> None:
    work = _init(tmp_path / "work" / "api", branch="dev")
    mine = _init(tmp_path / "personal" / "core", branch="dev")
    result = sweep.run_sweep([work, mine], set(), tmp_path / "personal")
    assert [(f.repo, f.kind) for f in result.findings] == [(str(mine), "branch-model")]


def test_run_sweep_notes_missing_agent_manager(tmp_path: Path) -> None:
    repo = _init(tmp_path / "r")
    result = sweep.run_sweep([repo], None)
    assert result.findings == []
    assert result.notes


def _hooks(repo: Path) -> tuple[list[str], list[str]]:
    result = sweep.sweep_repo(repo, None)
    kinds = [f.kind for f in result.findings if f.kind == "hooks-path"]
    return kinds, [n for n in result.notes if "husky" in n]


def test_hooks_no_override_is_silent(tmp_path: Path) -> None:
    assert _hooks(_init(tmp_path / "r")) == ([], [])


@pytest.mark.parametrize(
    "value", [".husky", ".husky/_", ".husky/", "/work/repo/.husky/_"]
)
def test_hooks_husky_value_is_info(tmp_path: Path, value: str) -> None:
    repo = _init(tmp_path / "r")
    _git("config", "core.hooksPath", value, cwd=repo)
    kinds, notes = _hooks(repo)
    assert kinds == []
    assert notes[0].endswith("husky hooks: prod gate via zen-release only")


def test_hooks_husky_dir_with_other_value_is_warning(tmp_path: Path) -> None:
    repo = _init(tmp_path / "r")
    (repo / ".husky").mkdir()
    _git("config", "core.hooksPath", "/other/hooks", cwd=repo)
    assert _hooks(repo) == (["hooks-path"], [])


def test_hooks_other_override_is_warning(tmp_path: Path) -> None:
    repo = _init(tmp_path / "r")
    _git("config", "core.hooksPath", "custom-hooks", cwd=repo)
    assert _hooks(repo) == (["hooks-path"], [])


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
