"""Shared pytest fixtures for scripts/test_*.py."""

from __future__ import annotations

import pytest


@pytest.fixture(autouse=True)
def _hermetic_git(
    tmp_path_factory: pytest.TempPathFactory, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Keep the user's global git config (and its hooks) out of temp repos.

    test_prepush.py builds its own global config and ignores this one.
    """
    root = tmp_path_factory.mktemp("hermetic-git")
    hooks = root / "hooks"
    hooks.mkdir()
    cfg = root / "gitconfig"
    cfg.write_text(f"[core]\n\thooksPath = {hooks}\n")
    monkeypatch.setenv("GIT_CONFIG_GLOBAL", str(cfg))
    monkeypatch.setenv("GIT_CONFIG_NOSYSTEM", "1")
