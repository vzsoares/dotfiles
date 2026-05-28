#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "tomlkit>=0.13",
#     "typer>=0.12",
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/release.py.

Run with:  uv run scripts/test_release.py
(pytest is invoked on this file at the bottom via __main__).
"""

from __future__ import annotations

import importlib.util
import subprocess
import sys
from pathlib import Path

import pytest
import typer

# Load release.py by path so tests work regardless of cwd / sys.path.
_spec = importlib.util.spec_from_file_location(
    "release", Path(__file__).parent / "release.py"
)
assert _spec is not None and _spec.loader is not None
release = importlib.util.module_from_spec(_spec)
sys.modules["release"] = release  # dataclass introspection looks the module up here
_spec.loader.exec_module(release)


# --------------------------------------------------------------------------- #
# Fixtures                                                                      #
# --------------------------------------------------------------------------- #


def _git(*args: str, cwd: Path | None = None) -> str:
    out = subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True, text=True, check=True
    )
    return out.stdout


@pytest.fixture
def git_repo(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """A fresh git repo (in a subdir) as cwd, with an initial commit.

    The repo lives in `tmp_path/repo` so `tmp_path/config` (the XDG config home)
    is a sibling — writing config never dirties the repo's working tree.
    """
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
def xdg_home(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "config"))
    return tmp_path / "config"


@pytest.fixture
def offline_gum(monkeypatch: pytest.MonkeyPatch):  # noqa: ANN201
    """Silence gum styling and replace `run` with a plain (no gum-spin) runner."""
    monkeypatch.setattr(release, "gum_style", lambda *a, **k: None)

    def plain_run(
        cmd: list[str], *, title: str = ""
    ) -> subprocess.CompletedProcess[str]:
        if release.DRY_RUN:
            return subprocess.CompletedProcess(cmd, 0, "", "")
        return subprocess.run(cmd, text=True, capture_output=True, check=False)

    monkeypatch.setattr(release, "run", plain_run)


def write_config(*, extra_enabled: tuple[str, ...] = ()) -> None:
    """Save a config with git+gum (and any extras) enabled, rest absent."""
    names = ("git", "gum", *extra_enabled)
    tools = {
        n: release.ToolStatus(enabled=True, path=f"/bin/{n}", version="v1")
        for n in names
    }
    release.save_config(
        release.Config(schema=release.CONFIG_SCHEMA, created_at=0.0, tools=tools)
    )


def write_repo_config(
    *, source: str = "", target: str = "", phases: dict[str, bool] | None = None
) -> None:
    """Save a per-repo config so do_release skips the interactive first-run setup."""
    enabled = dict.fromkeys(release.OPTIONAL_PHASES, True)
    if phases:
        enabled.update(phases)
    release.save_repo_config(
        release.RepoConfig(
            schema=release.REPO_SCHEMA,
            source_branch=source,
            target_branch=target,
            phases=enabled,
        )
    )


# --------------------------------------------------------------------------- #
# Config                                                                        #
# --------------------------------------------------------------------------- #


def test_config_path_respects_xdg(xdg_home: Path) -> None:
    assert release.config_path() == xdg_home / "zen-release" / "config.json"


def test_load_config_missing_returns_none(xdg_home: Path) -> None:
    assert release.load_config() is None


def test_config_roundtrip(xdg_home: Path) -> None:
    cfg = release.Config(
        schema=release.CONFIG_SCHEMA,
        created_at=123.0,
        tools={
            "gum": release.ToolStatus(enabled=True, path="/bin/gum", version="v1"),
            "git-cliff": release.ToolStatus(enabled=False, path="", version=""),
        },
    )
    release.save_config(cfg)
    loaded = release.load_config()
    assert loaded is not None
    assert loaded.schema == release.CONFIG_SCHEMA
    assert loaded.enabled("gum") is True
    assert loaded.enabled("git-cliff") is False
    assert loaded.enabled("nonexistent") is False
    assert loaded.tools["gum"].path == "/bin/gum"


def test_load_config_tolerates_garbage(xdg_home: Path) -> None:
    path = release.config_path()
    path.parent.mkdir(parents=True)
    path.write_text('{"schema": "bad", "tools": "nope"}')
    loaded = release.load_config()
    assert loaded is not None
    assert loaded.tools == {}


# --------------------------------------------------------------------------- #
# Tool probing                                                                  #
# --------------------------------------------------------------------------- #


def test_probe_tool_missing() -> None:
    spec = release.ToolSpec(
        "definitely-not-a-real-tool-xyz", ["definitely-not-a-real-tool-xyz"], ""
    )
    status = release.probe_tool(spec)
    assert status.enabled is False
    assert status.path == ""


def test_probe_tool_present() -> None:
    # git is guaranteed present in this environment.
    spec = release.ToolSpec("git", ["git", "--version"], "")
    status = release.probe_tool(spec)
    assert status.enabled is True
    assert status.path != ""
    assert "git" in status.version.lower()


def _enabled(name: str):  # noqa: ANN202 — release.ToolStatus, dynamically imported
    return release.ToolStatus(enabled=True, path=f"/bin/{name}", version="v1")


def _disabled():  # noqa: ANN202 — release.ToolStatus, dynamically imported
    return release.ToolStatus(enabled=False, path="", version="")


def test_missing_required_all_present() -> None:
    tools = {spec.name: _enabled(spec.name) for spec in release.TOOL_SPECS}
    assert release.missing_required(tools) == []


def test_missing_required_flags_required_only() -> None:
    # All required tools missing, all optional present.
    tools = {
        spec.name: (_disabled() if spec.required else _enabled(spec.name))
        for spec in release.TOOL_SPECS
    }
    gaps = {spec.name for spec in release.missing_required(tools)}
    assert gaps == {spec.name for spec in release.TOOL_SPECS if spec.required}
    assert "git" in gaps and "gum" in gaps


def test_missing_required_ignores_optional() -> None:
    # Required present, every optional missing → no required gaps.
    tools = {
        spec.name: (_enabled(spec.name) if spec.required else _disabled())
        for spec in release.TOOL_SPECS
    }
    assert release.missing_required(tools) == []


# --------------------------------------------------------------------------- #
# Semver                                                                        #
# --------------------------------------------------------------------------- #


def test_parse_semver_plain() -> None:
    sv = release.parse_semver("1.2.3")
    assert (sv.major, sv.minor, sv.patch, sv.dev) == (1, 2, 3, None)


def test_parse_semver_dev() -> None:
    sv = release.parse_semver("1.2.3-dev.5")
    assert (sv.major, sv.minor, sv.patch, sv.dev) == (1, 2, 3, 5)


@pytest.mark.parametrize("bad", ["1.2", "v1.2.3", "1.2.3-rc.1", "abc"])
def test_parse_semver_invalid(bad: str) -> None:
    with pytest.raises(typer.Exit):
        release.parse_semver(bad)


# --------------------------------------------------------------------------- #
# Version detection (in real temp repos)                                        #
# --------------------------------------------------------------------------- #


def test_detect_version_default(git_repo: Path) -> None:
    assert release.detect_version() == "0.0.0"


def test_detect_version_from_tag(git_repo: Path) -> None:
    subprocess.run(["git", "tag", "-a", "v2.4.6", "-m", "x"], check=True)
    assert release.detect_version() == "2.4.6"


def test_detect_version_pyproject_wins(git_repo: Path) -> None:
    (git_repo / "pyproject.toml").write_text(
        '[project]\nname = "x"\nversion = "9.9.9"\n'
    )
    subprocess.run(["git", "tag", "-a", "v2.4.6", "-m", "x"], check=True)
    assert release.detect_version() == "9.9.9"


def test_detect_version_package_json(git_repo: Path) -> None:
    (git_repo / "package.json").write_text('{"name": "x", "version": "3.1.4"}\n')
    assert release.detect_version() == "3.1.4"


# --------------------------------------------------------------------------- #
# Capability detection                                                          #
# --------------------------------------------------------------------------- #


def test_detect_project_type(git_repo: Path) -> None:
    assert release.detect_project_type() == "generic"
    (git_repo / "package.json").write_text('{"name": "x"}')
    assert release.detect_project_type() == "node"
    (git_repo / "pyproject.toml").write_text('[project]\nname="x"\n')
    assert release.detect_project_type() == "python"
    (git_repo / "go.mod").write_text("module x\n")
    assert release.detect_project_type() == "go"


def test_detect_publishable_node(git_repo: Path) -> None:
    (git_repo / "package.json").write_text('{"name": "x", "version": "1.0.0"}')
    assert release.detect_publishable("node") is True
    (git_repo / "package.json").write_text('{"name": "x", "private": true}')
    assert release.detect_publishable("node") is False
    (git_repo / "package.json").write_text('{"version": "1.0.0"}')
    assert release.detect_publishable("node") is False


def test_detect_publishable_python(git_repo: Path) -> None:
    (git_repo / "pyproject.toml").write_text('[project]\nname="x"\n')
    assert release.detect_publishable("python") is False
    (git_repo / "pyproject.toml").write_text(
        '[build-system]\nrequires=["hatchling"]\n[project]\nname="x"\n'
    )
    assert release.detect_publishable("python") is True


# --------------------------------------------------------------------------- #
# Publish idempotency (registry check)                                          #
# --------------------------------------------------------------------------- #


def test_project_name(git_repo: Path) -> None:
    assert release.project_name() == ""
    (git_repo / "package.json").write_text('{"name": "pkg"}')
    assert release.project_name() == "pkg"


def test_already_published_no_name_is_false(git_repo: Path) -> None:
    assert release.already_published("node", "1.0.0") is False


def test_already_published_node_true(
    git_repo: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    (git_repo / "package.json").write_text('{"name": "pkg", "version": "1.0.0"}')
    monkeypatch.setattr(
        release.subprocess,
        "run",
        lambda *a, **k: subprocess.CompletedProcess(a, 0, "1.0.0\n", ""),
    )
    assert release.already_published("node", "1.0.0") is True


def test_already_published_node_false(
    git_repo: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    (git_repo / "package.json").write_text('{"name": "pkg", "version": "1.0.0"}')
    monkeypatch.setattr(
        release.subprocess,
        "run",
        lambda *a, **k: subprocess.CompletedProcess(a, 1, "", "npm ERR! 404"),
    )
    assert release.already_published("node", "1.0.0") is False


def test_already_published_python(
    git_repo: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    import urllib.error
    from email.message import Message

    (git_repo / "pyproject.toml").write_text('[project]\nname = "pkg"\n')

    class _Resp:
        status = 200

        def __enter__(self) -> _Resp:
            return self

        def __exit__(self, *_a: object) -> None:
            return None

    monkeypatch.setattr("urllib.request.urlopen", lambda url, timeout=5: _Resp())
    assert release.already_published("python", "1.0.0") is True

    def _raise(url: str, timeout: int = 5) -> object:
        raise urllib.error.HTTPError(url, 404, "Not Found", Message(), None)

    monkeypatch.setattr("urllib.request.urlopen", _raise)
    assert release.already_published("python", "1.0.0") is False


def test_detect_hook_manager(git_repo: Path) -> None:
    assert release.detect_hook_manager() == "none"
    (git_repo / ".pre-commit-config.yaml").write_text("repos: []\n")
    assert release.detect_hook_manager() == "pre-commit"


def test_detect_hook_manager_lefthook(git_repo: Path) -> None:
    (git_repo / "lefthook.yml").write_text("pre-commit:\n")
    assert release.detect_hook_manager() == "lefthook"


def test_find_config_version_files(git_repo: Path) -> None:
    (git_repo / "app").mkdir()
    target = git_repo / "app" / "config.py"
    target.write_text('VERSION: str = "1.0.0"\n')
    (git_repo / "node_modules").mkdir()
    (git_repo / "node_modules" / "settings.py").write_text('VERSION: str = "0.0.0"\n')
    found = release.find_config_version_files()
    names = {str(p) for p in found}
    assert any("config.py" in n for n in names)
    assert not any("node_modules" in n for n in names)


# --------------------------------------------------------------------------- #
# Version writing                                                               #
# --------------------------------------------------------------------------- #


def test_write_version_preserves_toml(git_repo: Path) -> None:
    (git_repo / "pyproject.toml").write_text(
        '# keep me\n[project]\nname = "x"\nversion = "1.0.0"  # inline\n'
    )
    state = release.State(version="1.2.0", no_merge=True, target_branch="main")
    release.phase_write_version(state)
    text = (git_repo / "pyproject.toml").read_text()
    assert 'version = "1.2.0"' in text
    assert "# keep me" in text
    assert "# inline" in text


def test_write_version_skips_duplicate_commit(git_repo: Path) -> None:
    (git_repo / "package.json").write_text('{"name": "x", "version": "1.0.0"}\n')
    state = release.State(version="1.1.0", no_merge=True, target_branch="main")
    release.phase_write_version(state)
    first = subprocess.run(
        ["git", "rev-parse", "HEAD"], capture_output=True, text=True, check=True
    ).stdout
    # Re-running with the bump already committed must not create a new commit.
    release.phase_write_version(state)
    second = subprocess.run(
        ["git", "rev-parse", "HEAD"], capture_output=True, text=True, check=True
    ).stdout
    assert first == second


# --------------------------------------------------------------------------- #
# Changelog rendering                                                           #
# --------------------------------------------------------------------------- #


def test_render_changelog_groups() -> None:
    out = release.render_changelog(
        "1.0.0",
        [
            "feat: add thing",
            "fix(core): crash",
            "feat!: breaking",
            "random commit",
        ],
    )
    assert "## v1.0.0" in out
    assert "### Features" in out
    assert "- add thing" in out
    assert "### Bug Fixes" in out
    assert "- crash" in out
    assert "### Other" in out
    assert "- random commit" in out


# --------------------------------------------------------------------------- #
# State machine                                                                 #
# --------------------------------------------------------------------------- #


def test_state_roundtrip(git_repo: Path) -> None:
    state = release.State(version="1.2.3", target_branch="prod", no_merge=False)
    state.phases["merge"] = "done"
    release.save_state(state)
    loaded = release.load_state()
    assert loaded is not None
    assert loaded.version == "1.2.3"
    assert loaded.status("merge") == "done"
    assert loaded.status("push") == "pending"


def test_clear_state(git_repo: Path) -> None:
    release.save_state(release.State(version="1.0.0"))
    assert release.state_path().exists()
    release.clear_state()
    assert not release.state_path().exists()
    # Idempotent.
    release.clear_state()


def test_run_phase_skips_done(git_repo: Path, offline_gum: None) -> None:
    state = release.State()
    calls: list[str] = []
    release.run_phase("a", state, lambda: calls.append("a"))
    assert state.status("a") == "done"
    release.run_phase("a", state, lambda: calls.append("a"))  # already done -> skip
    assert calls == ["a"]


def test_run_phase_marks_failed_and_persists(git_repo: Path, offline_gum: None) -> None:
    state = release.State()

    def boom() -> None:
        raise typer.Exit(1)

    with pytest.raises(typer.Exit):
        release.run_phase("b", state, boom)
    assert state.status("b") == "failed"
    reloaded = release.load_state()
    assert reloaded is not None
    assert reloaded.status("b") == "failed"


# --------------------------------------------------------------------------- #
# Per-repo config (.git/zen-release.json)                                       #
# --------------------------------------------------------------------------- #


def test_repo_config_missing_returns_none(git_repo: Path) -> None:
    assert release.load_repo_config() is None


def test_repo_config_roundtrip(git_repo: Path) -> None:
    write_repo_config(source="dev", target="prod", phases={"publish": False})
    cfg = release.load_repo_config()
    assert cfg is not None
    assert cfg.source_branch == "dev"
    assert cfg.target_branch == "prod"
    assert cfg.enabled("publish") is False
    assert cfg.enabled("changelog") is True
    # Lives in .git/, uncommitted.
    assert (git_repo / ".git" / "zen-release.json").exists()


def test_repo_config_enabled_defaults_true_for_unknown(git_repo: Path) -> None:
    cfg = release.RepoConfig(schema=1, source_branch="", target_branch="", phases={})
    assert cfg.enabled("anything") is True


def test_apply_branches_no_merge(git_repo: Path, offline_gum: None) -> None:
    cfg = release.RepoConfig(schema=1, source_branch="", target_branch="", phases={})
    state = release.State()
    release.apply_branches_from_repo(state, cfg)
    assert state.no_merge is True
    assert state.target_branch == "main"  # current branch
    assert state.source_branch == ""


def test_apply_branches_merge(git_repo: Path, offline_gum: None) -> None:
    _git("branch", "prod")
    _git("checkout", "-q", "-b", "dev")
    cfg = release.RepoConfig(
        schema=1, source_branch="dev", target_branch="prod", phases={}
    )
    state = release.State()
    release.apply_branches_from_repo(state, cfg)
    assert state.no_merge is False
    assert state.source_branch == "dev"
    assert state.target_branch == "prod"


def test_apply_branches_missing_branch_aborts(
    git_repo: Path, offline_gum: None
) -> None:
    cfg = release.RepoConfig(
        schema=1, source_branch="ghost", target_branch="main", phases={}
    )
    with pytest.raises(typer.Exit):
        release.apply_branches_from_repo(release.State(), cfg)


def test_e2e_disabled_phases_are_skipped(
    git_repo: Path, xdg_home: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A repo with changelog/publish/github_release off: tag still cut, no CHANGELOG."""
    write_config()
    write_repo_config(
        source="",
        phases={"changelog": False, "publish": False, "github_release": False},
    )
    monkeypatch.setattr(release, "gum_choose", lambda header, options: options[0])
    monkeypatch.setattr(release, "gum_confirm", lambda prompt: False)

    release.do_release(
        resume=False, restart=False, dry_run=False, no_scan=False, no_changelog=False
    )

    assert "v0.0.1" in _git("tag").split()
    assert not (git_repo / "CHANGELOG.md").exists()  # changelog phase was skipped


def test_e2e_first_run_creates_repo_config(
    git_repo: Path, xdg_home: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    """No repo config yet → interactive first-run picker writes one, then releases."""
    write_config()  # global config only; no repo config
    monkeypatch.setattr(release, "gum_input", lambda *a, **k: "")  # release current
    monkeypatch.setattr(release, "gum_choose", lambda header, options: options[0])
    # First call (phase multi-select) returns none-checked; rest decline.
    monkeypatch.setattr(release, "gum_choose_multi", lambda h, opts, sel: [])
    monkeypatch.setattr(release, "gum_confirm", lambda prompt: False)

    release.do_release(
        resume=False, restart=False, dry_run=False, no_scan=False, no_changelog=False
    )

    cfg = release.load_repo_config()
    assert cfg is not None  # the first run persisted a repo config
    assert cfg.source_branch == ""
    # All optional phases were left unchecked in the picker.
    assert cfg.enabled("changelog") is False
    assert "v0.0.1" in _git("tag").split()
    assert not (git_repo / "CHANGELOG.md").exists()


# --------------------------------------------------------------------------- #
# Dev release (--dev)                                                           #
# --------------------------------------------------------------------------- #


def test_decide_version_dev_continue(
    git_repo: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    (git_repo / "package.json").write_text('{"name": "x", "version": "1.2.3-dev.2"}\n')
    monkeypatch.setattr(release, "gum_choose", lambda h, opts: opts[0])  # "continue"
    state = release.State()
    release.decide_version_dev(state)
    assert state.version == "1.2.3-dev.3"


def test_decide_version_dev_patch_from_release(
    git_repo: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    (git_repo / "package.json").write_text('{"name": "x", "version": "1.2.3"}\n')
    monkeypatch.setattr(release, "gum_choose", lambda h, opts: opts[0])  # patch
    state = release.State()
    release.decide_version_dev(state)
    assert state.version == "1.2.4-dev.1"


def test_decide_version_dev_custom(
    git_repo: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(release, "gum_choose", lambda h, opts: "custom")
    monkeypatch.setattr(release, "gum_input", lambda *a, **k: "2.0.0-dev.1")
    state = release.State()
    release.decide_version_dev(state)
    assert state.version == "2.0.0-dev.1"


def test_phase_tag_dev_message(git_repo: Path, offline_gum: None) -> None:
    state = release.State(version="1.0.0-dev.1", target_branch="main", dev=True)
    release.phase_tag(state)
    notes = _git("for-each-ref", "refs/tags/v1.0.0-dev.1", "--format=%(contents)")
    assert "Dev release" in notes


def test_e2e_dev_release(
    git_repo: Path, xdg_home: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    """--dev: bump to -dev.N, tag current branch; no merge/changelog/repo-config."""
    write_config()  # NOTE: no repo config written — dev mode must not need one
    monkeypatch.setattr(release, "gum_choose", lambda h, opts: opts[0])  # patch -dev.1
    monkeypatch.setattr(release, "gum_confirm", lambda prompt: False)  # skip push

    release.do_dev_release(dry_run=False)

    assert "v0.0.1-dev.1" in _git("tag").split()
    assert not (git_repo / "CHANGELOG.md").exists()
    assert release.load_repo_config() is None  # dev mode never creates a repo config
    # Dev mode is stateless — it must not write a release-state file at all.
    assert not (git_repo / ".git" / "release-state.json").exists()


def test_dev_release_is_stateless(
    git_repo: Path, xdg_home: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A full release's leftover state must not be touched by a dev run."""
    write_config()
    # Simulate an interrupted full release leaving state behind.
    leftover = release.State(version="9.9.9")
    leftover.phases["merge"] = "failed"
    release.save_state(leftover)

    monkeypatch.setattr(release, "gum_choose", lambda h, opts: opts[0])
    monkeypatch.setattr(release, "gum_confirm", lambda prompt: False)
    release.do_dev_release(dry_run=False)

    # The dev run tagged its own version and left the full-release state intact.
    assert "v0.0.1-dev.1" in _git("tag").split()
    survived = release.load_state()
    assert survived is not None
    assert survived.version == "9.9.9"
    assert survived.status("merge") == "failed"


# --------------------------------------------------------------------------- #
# Interactive gum I/O guard                                                     #
# --------------------------------------------------------------------------- #


def test_interactive_gum_keeps_terminal_io(monkeypatch: pytest.MonkeyPatch) -> None:
    """Regression guard: `gum input`/`choose` draw their UI on stderr and read
    the tty. If we capture stderr/stdin the prompt is invisible and looks hung,
    so interactive gum must capture stdout ONLY. (See the open-oembed-widgets
    hang.)"""
    calls: list[tuple[list[str], dict[str, object]]] = []

    def recorder(cmd: list[str], **kwargs: object) -> subprocess.CompletedProcess[str]:
        calls.append((cmd, kwargs))
        return subprocess.CompletedProcess(cmd, 0, "value", "")

    monkeypatch.setattr(release.subprocess, "run", recorder)

    release.gum_input("pick a branch")
    release.gum_choose("pick a bump", ["patch", "minor"])

    assert calls, "gum was never invoked"
    for cmd, kwargs in calls:
        assert cmd[0] == "gum"
        # Capturing both pipes (capture_output=True) is exactly the bug.
        assert kwargs.get("capture_output") in (None, False), (
            f"{cmd}: must not capture both"
        )
        assert kwargs.get("stderr") is None, (
            f"{cmd}: stderr must reach the terminal (UI)"
        )
        assert kwargs.get("stdin") is None, (
            f"{cmd}: stdin must stay attached to the tty"
        )
        # ...but stdout must be captured so we can read the chosen value.
        assert kwargs.get("stdout") is subprocess.PIPE, (
            f"{cmd}: stdout must be captured"
        )


# --------------------------------------------------------------------------- #
# Phase behavior (tag idempotency, conflict)                                    #
# --------------------------------------------------------------------------- #


def test_phase_tag_idempotent(git_repo: Path, offline_gum: None) -> None:
    state = release.State(version="1.0.0", target_branch="main")
    release.phase_tag(state)
    release.phase_tag(state)  # already at HEAD -> continue, no new tag, no error
    tags = _git("tag").split()
    assert tags.count("v1.0.0") == 1


def test_phase_tag_conflict_aborts(git_repo: Path, offline_gum: None) -> None:
    _git("tag", "-a", "v1.0.0", "-m", "x")
    (git_repo / "x.txt").write_text("x\n")
    _git("add", ".")
    _git("commit", "-q", "-m", "more")  # HEAD moved past the tag
    state = release.State(version="1.0.0", target_branch="main")
    with pytest.raises(typer.Exit):
        release.phase_tag(state)


# --------------------------------------------------------------------------- #
# End-to-end release flow (gum prompts scripted)                                #
# --------------------------------------------------------------------------- #


def test_e2e_release_current_branch(
    git_repo: Path, xdg_home: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    """No-merge release of the current branch: bump -> changelog -> tag."""
    write_config()  # git+gum only; gitleaks/git-cliff/gh all disabled
    write_repo_config(source="")  # release current branch, all phases on
    monkeypatch.setattr(
        release, "gum_choose", lambda header, options: options[0]
    )  # patch bump
    monkeypatch.setattr(
        release, "gum_confirm", lambda prompt: False
    )  # decline push/edit/rebase

    release.do_release(
        resume=False, restart=False, dry_run=False, no_scan=False, no_changelog=False
    )

    assert "v0.0.1" in _git("tag").split()
    assert (git_repo / "CHANGELOG.md").exists()
    # State file is removed on success.
    assert not (git_repo / ".git" / "release-state.json").exists()
    # Tag points at HEAD of the current branch.
    assert (
        _git("rev-list", "-n", "1", "v0.0.1").strip()
        == _git("rev-parse", "HEAD").strip()
    )


def test_e2e_release_with_merge(
    git_repo: Path, xdg_home: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Merge dev -> prod, then tag the merge commit on prod."""
    write_config()
    _git("branch", "prod")
    _git("checkout", "-q", "-b", "dev")
    (git_repo / "feature.txt").write_text("feature\n")
    _git("add", ".")
    _git("commit", "-q", "-m", "feat: add feature")

    write_repo_config(source="dev", target="prod")
    monkeypatch.setattr(
        release, "gum_choose", lambda header, options: options[0]
    )  # patch
    monkeypatch.setattr(release, "gum_confirm", lambda prompt: False)

    release.do_release(
        resume=False, restart=False, dry_run=False, no_scan=False, no_changelog=False
    )

    assert _git("branch", "--show-current").strip() == "prod"
    assert "v0.0.1" in _git("tag").split()
    log = _git("log", "--oneline")
    assert "Release version 0.0.1" in log  # the --no-ff merge commit
    assert "feat: add feature" in log


def test_e2e_dry_run_mutates_nothing(
    git_repo: Path, xdg_home: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    write_config()
    write_repo_config(source="")
    before = _git("rev-parse", "HEAD").strip()
    monkeypatch.setattr(release, "gum_choose", lambda header, options: options[0])
    monkeypatch.setattr(release, "gum_confirm", lambda prompt: False)

    release.do_release(
        resume=False, restart=False, dry_run=True, no_scan=False, no_changelog=False
    )

    assert _git("tag").strip() == ""  # no tag created
    assert _git("rev-parse", "HEAD").strip() == before  # no commit
    assert not (git_repo / "CHANGELOG.md").exists()


def test_e2e_missing_config_aborts(
    git_repo: Path, xdg_home: Path, offline_gum: None
) -> None:
    # No write_config() — load_config returns None.
    with pytest.raises(typer.Exit):
        release.do_release(
            resume=False,
            restart=False,
            dry_run=False,
            no_scan=False,
            no_changelog=False,
        )


def test_e2e_resume_after_crash(
    git_repo: Path, xdg_home: Path, offline_gum: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Crash during tag, then --resume completes without redoing prior phases."""
    write_config()
    write_repo_config(source="")
    monkeypatch.setattr(release, "gum_choose", lambda header, options: options[0])
    monkeypatch.setattr(release, "gum_confirm", lambda prompt: False)

    # First run: blow up inside the tag phase.
    real_tag = release.phase_tag

    def exploding_tag(state: object) -> None:
        raise RuntimeError("boom")

    monkeypatch.setattr(release, "phase_tag", exploding_tag)
    with pytest.raises(RuntimeError):
        release.do_release(
            resume=False,
            restart=False,
            dry_run=False,
            no_scan=False,
            no_changelog=False,
        )

    # State persisted: changelog done, tag failed, nothing past it ran.
    mid = release.load_state()
    assert mid is not None
    assert mid.status("changelog") == "done"
    assert mid.status("tag") == "failed"
    assert mid.status("push") == "pending"
    assert mid.version == "0.0.1"
    changelog_commit = _git("rev-parse", "HEAD").strip()

    # Second run: real tag phase, resume. Must not re-run the changelog commit.
    monkeypatch.setattr(release, "phase_tag", real_tag)
    release.do_release(
        resume=True, restart=False, dry_run=False, no_scan=False, no_changelog=False
    )

    assert "v0.0.1" in _git("tag").split()
    # HEAD is unchanged — changelog wasn't committed a second time.
    assert _git("rev-parse", "HEAD").strip() == changelog_commit
    assert not (git_repo / ".git" / "release-state.json").exists()


if __name__ == "__main__":
    import sys

    sys.exit(pytest.main([__file__, "-v"]))
