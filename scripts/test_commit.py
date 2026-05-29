#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "typer>=0.12",
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/commit.py.

Run with:  uv run scripts/test_commit.py
"""

from __future__ import annotations

import importlib.util
import subprocess
import sys
from pathlib import Path

import pytest
import typer

_spec = importlib.util.spec_from_file_location(
    "commit", Path(__file__).parent / "commit.py"
)
assert _spec is not None and _spec.loader is not None
commit = importlib.util.module_from_spec(_spec)
sys.modules["commit"] = commit
_spec.loader.exec_module(commit)


# --------------------------------------------------------------------------- #
# Fixtures                                                                      #
# --------------------------------------------------------------------------- #


def _git(*args: str, cwd: Path | None = None) -> str:
    return subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True, text=True, check=True
    ).stdout


@pytest.fixture
def git_repo(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    repo = tmp_path / "repo"
    repo.mkdir()
    monkeypatch.chdir(repo)
    _git("init", "-q", "-b", "main", cwd=repo)
    _git("config", "user.email", "t@t.dev", cwd=repo)
    _git("config", "user.name", "tester", cwd=repo)
    (repo / "README.md").write_text("hi\n")
    _git("add", ".", cwd=repo)
    _git("commit", "-q", "-m", "init", cwd=repo)
    return repo


@pytest.fixture
def quiet_gum(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(commit, "gum_style", lambda *a, **k: None)


# --------------------------------------------------------------------------- #
# clean_message                                                                 #
# --------------------------------------------------------------------------- #


def test_clean_message_plain() -> None:
    assert commit.clean_message("feat: add thing") == "feat: add thing"


def test_clean_message_trims_blank_lines() -> None:
    assert commit.clean_message("\n\nfix: bug\n\n") == "fix: bug"


def test_clean_message_extracts_last_fence() -> None:
    raw = "thinking...\n```\nfeat: real message\n```\n"
    assert commit.clean_message(raw) == "feat: real message"


def test_clean_message_strips_fake_tags() -> None:
    raw = "<thinking>\nfeat: x\n</thinking>"
    assert commit.clean_message(raw) == "feat: x"


def test_clean_message_keeps_body() -> None:
    raw = "feat: x\n\nlonger body line"
    assert commit.clean_message(raw) == "feat: x\n\nlonger body line"


# --------------------------------------------------------------------------- #
# looks_like_error                                                              #
# --------------------------------------------------------------------------- #


@pytest.mark.parametrize(
    "msg",
    ["┌ box drawing", "Please run /login", "Not logged in"],
)
def test_looks_like_error_true(msg: str) -> None:
    assert commit.looks_like_error(msg) is True


def test_looks_like_error_false() -> None:
    assert commit.looks_like_error("feat: a normal commit message") is False


# --------------------------------------------------------------------------- #
# Guardrail: filenames                                                          #
# --------------------------------------------------------------------------- #


def test_scan_filenames_flags_sensitive() -> None:
    files = [
        "secrets/server.pem",
        "deploy/id_rsa",
        ".env",
        "config/.env.production",
        "credentials.json",
        "gcp/service-account-prod.json",
    ]
    out = "\n".join(commit.scan_filenames(files))
    assert "server.pem" in out
    assert "id_rsa" in out
    assert ".env" in out
    assert ".env.production" in out
    assert "credentials.json" in out
    assert "service-account-prod.json" in out


def test_scan_filenames_allows_safe() -> None:
    files = [
        "deploy/id_rsa.pub",
        ".env.example",
        "config/.env.sample",
        "src/main.py",
        "cert.pub",
    ]
    assert commit.scan_filenames(files) == []


# --------------------------------------------------------------------------- #
# is_noisy_file                                                                 #
# --------------------------------------------------------------------------- #


@pytest.mark.parametrize(
    "name,noisy",
    [
        ("package-lock.json", True),
        ("frontend/pnpm-lock.yaml", True),
        ("uv.lock", True),
        ("dist/app.min.js", True),
        ("src/main.py", False),
        ("README.md", False),
    ],
)
def test_is_noisy_file(name: str, noisy: bool) -> None:
    assert commit.is_noisy_file(name) is noisy


# --------------------------------------------------------------------------- #
# scan_secrets in a real repo                                                   #
# --------------------------------------------------------------------------- #


def test_scan_secrets_staged_env(git_repo: Path) -> None:
    (git_repo / ".env").write_text("SECRET=1\n")
    _git("add", ".env")
    assert any(".env" in v for v in commit.scan_secrets())


def test_scan_secrets_content(
    git_repo: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    (git_repo / "app.py").write_text("KEY = 'AKIAIOSFODNN7EXAMPLE'\n")
    _git("add", "app.py")
    _real_run = subprocess.run

    def _mock_run(cmd, **kwargs):
        if isinstance(cmd, list) and cmd[:1] == ["gitleaks"]:
            return subprocess.CompletedProcess(cmd, 1, "Finding: aws-access-key-id\n", "")
        return _real_run(cmd, **kwargs)

    monkeypatch.setattr(commit.subprocess, "run", _mock_run)
    assert any("gitleaks" in v for v in commit.scan_secrets())


def test_scan_secrets_honors_gitleaks_allow(
    git_repo: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # gitleaks returning 0 means no findings (it handles gitleaks:allow natively).
    (git_repo / "app.py").write_text(
        "KEY = 'AKIAIOSFODNN7EXAMPLE'  # gitleaks:allow\n"
    )
    _git("add", "app.py")
    _real_run = subprocess.run

    def _mock_run(cmd, **kwargs):
        if isinstance(cmd, list) and cmd[:1] == ["gitleaks"]:
            return subprocess.CompletedProcess(cmd, 0, "", "")
        return _real_run(cmd, **kwargs)

    monkeypatch.setattr(commit.subprocess, "run", _mock_run)
    assert commit.scan_secrets() == []


# --------------------------------------------------------------------------- #
# Headless do_commit                                                            #
# --------------------------------------------------------------------------- #


def test_headless_commit_with_message(git_repo: Path, quiet_gum: None) -> None:
    (git_repo / "f.txt").write_text("x\n")
    commit.do_commit(stage_all=True, message="feat: add f", yes=True, force=False)
    assert _git("log", "-1", "--pretty=%s").strip() == "feat: add f"


def test_headless_generates_message(
    git_repo: Path, quiet_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    (git_repo / "f.txt").write_text("x\n")
    monkeypatch.setattr(commit, "run_claude", lambda diff, prompt: "feat: generated\n")
    commit.do_commit(stage_all=True, message="", yes=True, force=False)
    assert _git("log", "-1", "--pretty=%s").strip() == "feat: generated"


def test_headless_nothing_staged_aborts(git_repo: Path, quiet_gum: None) -> None:
    with pytest.raises(typer.Exit):
        commit.do_commit(stage_all=False, message="x", yes=True, force=False)


def test_headless_guardrail_aborts(git_repo: Path, quiet_gum: None) -> None:
    (git_repo / ".env").write_text("SECRET=1\n")
    with pytest.raises(typer.Exit):
        commit.do_commit(stage_all=True, message="chore: env", yes=True, force=False)
    # Nothing committed beyond the initial commit.
    assert _git("rev-list", "--count", "HEAD").strip() == "1"


def test_headless_guardrail_force_overrides(git_repo: Path, quiet_gum: None) -> None:
    (git_repo / ".env").write_text("SECRET=1\n")
    commit.do_commit(stage_all=True, message="chore: env", yes=True, force=True)
    assert _git("log", "-1", "--pretty=%s").strip() == "chore: env"


def test_headless_clean_run_count(git_repo: Path, quiet_gum: None) -> None:
    (git_repo / "f.txt").write_text("hello\n")
    commit.do_commit(stage_all=True, message="feat: f", yes=True, force=False)
    assert _git("rev-list", "--count", "HEAD").strip() == "2"


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-v"]))
