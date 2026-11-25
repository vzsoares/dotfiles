#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/role.py.

Run with:  uv run scripts/test_role.py
"""

from __future__ import annotations

import importlib.util
import json
import os
import re
import sys
from pathlib import Path

import pytest

_spec = importlib.util.spec_from_file_location(
    "role", Path(__file__).parent / "role.py"
)
assert _spec is not None and _spec.loader is not None
role = importlib.util.module_from_spec(_spec)
sys.modules["role"] = role
_spec.loader.exec_module(role)

TRACKED = Path(__file__).resolve().parents[1]
FIGMA = "figma@claude-plugins-official"

GLOBAL_CLAUDE = """# User

- zsh

# Rules

Run this:

```
# not a section
make
```

# Tools

Use uv.
"""

FAKE_MANAGER = """#!/usr/bin/env python3
import json, os, sys
from pathlib import Path

state = Path(os.environ["FAKE_STATE"])
args = sys.argv[1:]
with open(state / "log", "a") as log:
    log.write(" ".join(args) + "\\n")
profiles = json.loads((state / "profiles.json").read_text())
if args[:2] == ["env", "list"]:
    print(json.dumps([{"name": n, "keys": sorted(v)} for n, v in profiles.items()]))
elif args[:2] == ["env", "show"]:
    print(json.dumps({"name": args[2], "vars": profiles[args[2]]}))
elif args[:2] == ["env", "set"]:
    pairs = [a for a in args[3:] if a != "--json"]
    profiles.setdefault(args[2], {}).update(p.split("=", 1) for p in pairs)
    (state / "profiles.json").write_text(json.dumps(profiles))
else:
    sys.exit(2)
"""


# --------------------------------------------------------------------------- #
# Fixtures                                                                      #
# --------------------------------------------------------------------------- #


@pytest.fixture
def repo(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """A fake repo and HOME: two presets, a skill tree, no roles.json yet."""
    root = tmp_path / "repo"
    (root / "agent-manager" / "roles").mkdir(parents=True)
    (root / "agent-manager" / "config.json").write_text(
        json.dumps({"presets": [{"name": "chief"}, {"name": "worker-rules"}]})
    )
    home = tmp_path / "home"
    skills = home / ".claude" / "skills"
    for name in ("zen-flow", "wiki", "diagram"):
        (skills / name).mkdir(parents=True)
        (skills / name / "SKILL.md").write_text("x")
    (skills / "clawd-status" / "skills" / "clawd").mkdir(parents=True)
    (skills / "half-made").mkdir()
    bucket = skills / "synced" / "bucket-1"
    for name in ("zen-flow", "chrome-browser", "pdf"):
        (bucket / name).mkdir(parents=True)
    (bucket / "manifest.json").write_text("{}")
    (skills / "synced" / ".bucket-1").write_text("")
    (home / ".claude" / "CLAUDE.md").write_text(GLOBAL_CLAUDE)
    monkeypatch.setattr(role, "REPO", root)
    monkeypatch.setenv("HOME", str(home))
    return root


def write_roles(repo: Path, roles: object) -> None:
    (repo / "agent-manager" / "roles.json").write_text(json.dumps(roles))


@pytest.fixture
def manager(repo: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    state = tmp_path / "state"
    state.mkdir()
    (state / "profiles.json").write_text(
        json.dumps({"pi-box": {"PATH": "/pi"}, "role-gone": {"ZEN_ROLE": "gone"}})
    )
    (state / "log").write_text("")
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    fake = bin_dir / "agent-manager"
    fake.write_text(FAKE_MANAGER)
    fake.chmod(0o755)
    shim = repo / "agent-manager" / "shim"
    monkeypatch.setenv("FAKE_STATE", str(state))
    monkeypatch.setenv("PATH", f"{shim}:{bin_dir}:{shim}/:{os.environ['PATH']}")
    write_roles(
        repo, {"chief": {"saved": "chief"}, "worker": {"saved": "worker-rules"}}
    )
    return state


def calls(state: Path, verb: str) -> list[str]:
    return [
        line
        for line in (state / "log").read_text().splitlines()
        if line.startswith(f"env {verb}")
    ]


# --------------------------------------------------------------------------- #
# check                                                                         #
# --------------------------------------------------------------------------- #


REJECTED = [
    ({"saved": "chief", "colour": 1}, "r.colour: unknown key"),
    ({"model": "opus"}, "r.saved: required"),
    ({"saved": "chief", "model": 3}, "r.model:"),
    ({"saved": "chief", "effort": "huge"}, "r.effort:"),
    ({"saved": "chief", "autocompact": "300k"}, "r.autocompact:"),
    ({"saved": "chief", "autocompact": True}, "r.autocompact:"),
    ({"saved": "chief", "autocompact": 99_999}, "r.autocompact: must be 100000"),
    ({"saved": "chief", "settings": []}, "r.settings:"),
    ({"saved": "chief", "skills": ["zen-flow"]}, "r.skills:"),
    ({"saved": "chief", "skills": {"only": "zen-flow"}}, "r.skills.only:"),
    ({"saved": "chief", "skills": {"keep": []}}, "r.skills.keep: unknown key"),
    ({"saved": "chief", "tools": {"deny": [1]}}, "r.tools.deny:"),
    ({"saved": "chief", "tools": {"deny": ["A,B"]}}, "r.tools.deny:"),
    ({"saved": "nobody"}, "r.saved: no preset named 'nobody'"),
    ({"saved": "chief", "skills": {"only": ["ghost"]}}, "r.skills.only: 'ghost'"),
    (
        {"saved": "chief", "skills": {"only": ["anthropic-skills:wiki"]}},
        "r.skills.only: 'anthropic-skills:wiki'",
    ),
    ({"saved": "chief", "skills": {"only": ["half-made"]}}, "r.skills.only:"),
    ({"saved": "chief", "append": "missing.md"}, "r.append:"),
    ({"saved": "chief", "append": "../config.json"}, "r.append: must be a file under"),
    ({"saved": "chief", "isolate": "yes"}, "r.isolate: must be true or false"),
    (
        {"saved": "chief", "isolate": True, "instructions": {"keep": []}},
        "r.isolate: requires skills.only",
    ),
    (
        {"saved": "chief", "isolate": True, "skills": {"only": ["wiki"]}},
        "r.isolate: requires instructions",
    ),
    ({"saved": "chief", "instructions": {}}, "r.instructions.keep: required"),
    ({"saved": "chief", "instructions": {"drop": []}}, "r.instructions.drop:"),
    ({"saved": "chief", "instructions": ["Rules"]}, "r.instructions:"),
    (
        {
            "saved": "chief",
            "isolate": True,
            "skills": {"only": ["zen-flow", "anthropic-skills:zen-flow"]},
            "instructions": {"keep": []},
        },
        "r.skills.only: an isolated role cannot keep a skill and its",
    ),
    (
        {
            "saved": "chief",
            "isolate": True,
            "skills": {"only": ["wiki"]},
            "instructions": {"keep": ["Rules", "Gone"]},
        },
        "r.instructions.keep: 'Gone' is no section of",
    ),
    ("opus", "r: must be an object"),
]


@pytest.mark.parametrize(("raw", "message"), REJECTED)
def test_check_rejects(
    repo: Path, capsys: pytest.CaptureFixture[str], raw: object, message: str
) -> None:
    write_roles(repo, {"good": {"saved": "chief"}, "r": raw})
    assert role.main(["check"]) == 1
    assert f"zen-role: {message}" in capsys.readouterr().err


def test_check_accepts_every_field(
    repo: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    (repo / "agent-manager" / "roles" / "extra.md").write_text("more")
    write_roles(
        repo,
        {
            "r": {
                "saved": "chief",
                "model": "opus",
                "effort": "max",
                "autocompact": 100_000,
                "settings": {"outputStyle": "default"},
                "skills": {
                    "only": ["wiki", "anthropic-skills:pdf"],
                    "off": ["figma:x"],
                },
                "tools": {"deny": ["WebSearch"]},
                "append": "extra.md",
            }
        },
    )
    assert role.main(["check"]) == 0
    assert "ok: 1 roles" in capsys.readouterr().out


@pytest.mark.parametrize("text", ["{nope", "[]"])
def test_invalid_file(
    repo: Path, capsys: pytest.CaptureFixture[str], text: str
) -> None:
    (repo / "agent-manager" / "roles.json").write_text(text)
    assert role.main(["check"]) == 1
    assert "roles.json" in capsys.readouterr().err


def test_missing_file(repo: Path, capsys: pytest.CaptureFixture[str]) -> None:
    assert role.main(["list"]) == 1
    assert "cannot read" in capsys.readouterr().err


# --------------------------------------------------------------------------- #
# skills, merge, show                                                           #
# --------------------------------------------------------------------------- #


def test_disk_skills(repo: Path) -> None:
    assert role.disk_skills() == {
        "zen-flow",
        "wiki",
        "diagram",
        "anthropic-skills:zen-flow",
        "anthropic-skills:chrome-browser",
        "anthropic-skills:pdf",
    }


def test_skills_only_turns_the_rest_off(repo: Path) -> None:
    parsed = role.parse_role(
        "r",
        {
            "saved": "chief",
            "skills": {
                "only": ["zen-flow", "anthropic-skills:chrome-browser"],
                "off": ["figma:figma-use"],
            },
        },
    )
    assert role.skill_overrides(parsed, role.disk_skills()) == {
        "anthropic-skills:pdf": "off",
        "anthropic-skills:zen-flow": "off",
        "diagram": "off",
        "figma:figma-use": "off",
        "wiki": "off",
    }


def test_no_skills_key_turns_nothing_off(repo: Path) -> None:
    parsed = role.parse_role("r", {"saved": "chief"})
    assert role.skill_overrides(parsed, role.disk_skills()) == {}


def test_merge() -> None:
    base = {"a": 1, "o": {"x": 1, "y": 2}, "l": [1, 2], "s": [1]}
    top = {"a": 2, "o": {"y": 3, "z": 4}, "l": [2, 3], "s": "scalar", "n": 5}
    assert role.merge(base, top) == {
        "a": 2,
        "o": {"x": 1, "y": 3, "z": 4},
        "l": [1, 2, 3],
        "s": "scalar",
        "n": 5,
    }
    assert base["o"] == {"x": 1, "y": 2}


def test_isolated_merge_keeps_global_hooks_beside_the_role_hook() -> None:
    glob = {"hooks": {"PreToolUse": [{"matcher": "Edit", "hooks": [1]}]}}
    top = {"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [2]}]}}
    merged = role.merge(glob, top)
    assert [h["matcher"] for h in merged["hooks"]["PreToolUse"]] == ["Edit", "Bash"]


def test_window_overrides_each_pinned_model(repo: Path) -> None:
    settings = Path.home() / ".claude" / "settings.json"
    settings.write_text(
        json.dumps(
            {
                "autoCompactWindow": 800_000,
                "modelSettings": {
                    "fable": {"autoCompactWindow": 300_000},
                    "opus": {"effortLevel": "high"},
                },
            }
        )
    )
    parsed = role.parse_role("r", {"saved": "chief", "autocompact": 200_000})
    resolved = role.overlay(parsed, set(), role.window_models())
    assert resolved == {
        "autoCompactWindow": 200_000,
        "modelSettings": {"fable": {"autoCompactWindow": 200_000}},
    }


def test_show_json(repo: Path, capsys: pytest.CaptureFixture[str]) -> None:
    write_roles(
        repo,
        {"r": {"saved": "chief", "model": "opus", "skills": {"only": ["zen-flow"]}}},
    )
    assert role.main(["show", "r", "--json"]) == 0
    shown = json.loads(capsys.readouterr().out)
    assert shown["mode"] == "overlay"
    assert shown["profile"] == "role-r"
    assert shown["flags"][:2] == ["--model", "opus"]
    assert shown["settings"]["skillOverrides"]["wiki"] == "off"
    assert role.main(["show", "r"]) == 0
    assert "mode:     overlay" in capsys.readouterr().out
    assert role.main(["show", "nope"]) == 1
    assert "unknown role 'nope'" in capsys.readouterr().err


def test_list(repo: Path, capsys: pytest.CaptureFixture[str]) -> None:
    write_roles(repo, {"b": {"saved": "chief"}, "a": {"saved": "chief"}})
    assert role.main(["list"]) == 0
    assert capsys.readouterr().out.split() == ["b", "a"]


# --------------------------------------------------------------------------- #
# isolate                                                                       #
# --------------------------------------------------------------------------- #

ISOLATED = {
    "saved": "chief",
    "isolate": True,
    "skills": {"only": ["wiki", "anthropic-skills:pdf", "not-synced-yet"]},
    "instructions": {"keep": ["Tools", "User"]},
    "append": "extra.md",
}


def test_sections_skip_fenced_blocks() -> None:
    found = role.sections("intro\n" + GLOBAL_CLAUDE)
    assert [title for title, _ in found] == ["User", "Rules", "Tools"]
    assert "# not a section" in found[1][1]
    assert "".join(body for _, body in found) == GLOBAL_CLAUDE


def test_sections_skip_tilde_fences() -> None:
    found = role.sections("# A\n\n~~~\n# comment\n~~~\n\n# B\n")
    assert [title for title, _ in found] == ["A", "B"]


def test_unreadable_project_instructions_fail_cleanly(
    repo: Path, tmp_path: Path
) -> None:
    parsed = role.parse_role("r", {**ISOLATED, "append": None} | {"append": "x.md"})
    (repo / "agent-manager" / "roles" / "x.md").write_text("x")
    (tmp_path / "CLAUDE.md").write_bytes(b"caf\xe9")
    with pytest.raises(role.RoleError, match=r"CLAUDE\.md"):
        role.build_instructions(parsed, tmp_path)


def test_check_reports_each_isolate_problem(
    repo: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    write_roles(
        repo, {"r": {**ISOLATED, "instructions": {"keep": ["Gone"]}, "append": "no.md"}}
    )
    assert role.main(["check"]) == 1
    err = capsys.readouterr().err
    assert "r.append:" in err
    assert "r.instructions.keep: 'Gone'" in err
    assert "mcp.json" in err


def test_isolated_instructions(repo: Path, tmp_path: Path) -> None:
    (repo / "agent-manager" / "roles" / "extra.md").write_text("Role extra.\n")
    parsed = role.parse_role("r", ISOLATED)
    project = tmp_path / "project"
    project.mkdir()
    bare = "# User\n\n- zsh\n\n# Tools\n\nUse uv.\n\nRole extra.\n"
    assert role.build_instructions(parsed, project) == bare
    (project / "CLAUDE.md").write_text("# Project\n\nCodeword.\n\n")
    assert role.build_instructions(parsed, project) == (
        "# User\n\n- zsh\n\n# Tools\n\nUse uv.\n\n"
        f"# Project instructions ({project}/CLAUDE.md)\n\n# Project\n\nCodeword.\n\n"
        "Role extra.\n"
    )


def test_isolated_plugin_links_the_kept_skills(repo: Path, tmp_path: Path) -> None:
    parsed = role.parse_role("r", ISOLATED)
    target = tmp_path / "plugin"
    (target / "skills" / "stale").mkdir(parents=True)
    role.build_plugin(parsed, target)
    manifest = json.loads((target / ".claude-plugin" / "plugin.json").read_text())
    assert manifest["name"] == "role"
    links = {link.name: link.resolve() for link in (target / "skills").iterdir()}
    skills = (Path.home() / ".claude" / "skills").resolve()
    assert links == {
        "wiki": skills / "wiki",
        "pdf": skills / "synced" / "bucket-1" / "pdf",
    }


def test_isolated_flags_and_show(
    repo: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    (repo / "agent-manager" / "roles" / "extra.md").write_text("Role extra.\n")
    write_roles(repo, {"r": ISOLATED})
    assert role.role_flags(role.load_role("r"), [], "R") == [
        "--setting-sources",
        "local",
        "--plugin-dir",
        "R/plugin",
        "--mcp-config",
        str(repo / "agents" / "mcp.json"),
        "--append-system-prompt-file",
        "R/instructions.md",
        "--settings",
        "R/settings.json",
    ]
    assert role.main(["show", "r", "--json"]) == 0
    shown = json.loads(capsys.readouterr().out)
    assert shown["mode"] == "isolate"
    assert shown["sections"] == ["User", "Tools"]
    assert shown["skills"] == ["role:wiki", "role:pdf", "role:not-synced-yet"]
    assert role.main(["show", "r"]) == 0
    assert "sections: User; Tools" in capsys.readouterr().out


def test_tracked_file_isolates_no_role(tracked: None) -> None:
    assert [r.name for r in role.load_roles() if r.isolate or r.keep] == []


# --------------------------------------------------------------------------- #
# build                                                                         #
# --------------------------------------------------------------------------- #


def test_build_sets_profiles(
    repo: Path, manager: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    assert role.main(["build"]) == 0
    out = capsys.readouterr().out
    profiles = json.loads((manager / "profiles.json").read_text())
    shim = str(repo / "agent-manager" / "shim")
    for name in ("chief", "worker"):
        profile = profiles[f"role-{name}"]
        assert profile["ZEN_ROLE"] == name
        parts = profile["PATH"].split(":")
        assert parts[0] == shim
        assert shim not in parts[1:] and f"{shim}/" not in parts[1:]
        assert all(Path(part).is_absolute() for part in parts)
    assert profiles["pi-box"] == {"PATH": "/pi"}
    assert profiles["role-gone"] == {"ZEN_ROLE": "gone"}
    assert "orphan    role-gone" in out
    assert "pi-box" not in out
    assert len(calls(manager, "set")) == 2
    assert calls(manager, "rm") == []


def test_build_is_idempotent(
    repo: Path, manager: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    assert role.main(["build"]) == 0
    before = (manager / "profiles.json").read_text()
    capsys.readouterr()
    assert role.main(["build"]) == 0
    assert (manager / "profiles.json").read_text() == before
    assert len(calls(manager, "set")) == 2
    assert capsys.readouterr().out.count("unchanged") == 2


def test_build_keeps_other_keys_and_fixes_drift(repo: Path, manager: Path) -> None:
    (manager / "profiles.json").write_text(
        json.dumps({"role-chief": {"ZEN_ROLE": "chief", "PATH": "/old", "EXTRA": "1"}})
    )
    assert role.main(["build"]) == 0
    chief = json.loads((manager / "profiles.json").read_text())["role-chief"]
    assert chief["EXTRA"] == "1"
    assert chief["PATH"].startswith(str(repo / "agent-manager" / "shim") + ":")


def test_build_refuses_an_invalid_file(
    repo: Path, manager: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    write_roles(repo, {"r": {"saved": "chief", "autocompact": 1}})
    assert role.main(["build"]) == 1
    assert calls(manager, "set") == []
    assert "r.autocompact" in capsys.readouterr().err


def test_build_refuses_a_linked_worktree(
    repo: Path, manager: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    (repo / ".git").write_text("gitdir: /elsewhere\n")
    assert role.main(["build"]) == 1
    assert calls(manager, "set") == []
    assert "linked worktree" in capsys.readouterr().err


def test_build_without_agent_manager(
    repo: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    write_roles(repo, {"r": {"saved": "chief"}})
    monkeypatch.setenv("PATH", "/nonexistent")
    assert role.main(["build"]) == 1
    assert "agent-manager is not on PATH" in capsys.readouterr().err


# --------------------------------------------------------------------------- #
# The tracked files                                                             #
# --------------------------------------------------------------------------- #


@pytest.fixture
def tracked(repo: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """The tracked roles.json and presets, over a skill tree holding its skills."""
    monkeypatch.setattr(role, "REPO", TRACKED)
    skills = Path.home() / ".claude" / "skills"
    for name in ("pi", "second-brain", "wrapup", "bruno"):
        (skills / name).mkdir()
        (skills / name / "SKILL.md").write_text("x")


DAY_ONE = {
    "chief": (
        "chief",
        "fable",
        "high",
        300_000,
        None,
        [
            "zen-flow",
            "pi",
            "second-brain",
            "wiki",
            "wrapup",
            "anthropic-skills:chrome-browser",
        ],
    ),
    "orchestrator": ("orchestrator", "opus", "high", 300_000, None, ["zen-flow"]),
    "spec-writer": ("spec-writer", "opus", "high", None, "default", ["zen-flow"]),
    "worker": (
        "worker-rules",
        "opus",
        "medium",
        None,
        "default",
        ["zen-flow", "anthropic-skills:chrome-browser"],
    ),
    "designer": (
        "designer",
        "fable",
        "high",
        None,
        None,
        ["zen-flow", "diagram", "anthropic-skills:chrome-browser"],
    ),
    "explorer": ("explorer", "haiku", None, None, "default", ["zen-flow"]),
}
EDIT_TOOLS = ["Edit", "Write", "NotebookEdit"]


def test_tracked_file_is_the_day_one_table(tracked: None) -> None:
    assert role.check_errors() == []
    roles = {r.name: r for r in role.load_roles()}
    assert list(roles) == list(DAY_ONE)
    for name, (saved, model, effort, window, style, only) in DAY_ONE.items():
        r = roles[name]
        assert (r.saved, r.model, r.effort, r.autocompact) == (
            saved,
            model,
            effort,
            window,
        )
        assert r.settings.get("outputStyle") == style
        assert r.settings["enabledPlugins"] == {FIGMA: False}
        assert r.only == only
        assert (r.off, r.append) == ([], None)
        assert r.deny == (EDIT_TOOLS if name == "explorer" else [])


RTK_HOOK = {
    "matcher": "Bash",
    "hooks": [
        {
            "type": "command",
            "command": "command -v rtk >/dev/null && rtk hook claude || true",
        }
    ],
}


def test_only_the_worker_carries_the_rtk_hook(tracked: None) -> None:
    for r in role.load_roles():
        pre = r.settings.get("hooks", {}).get("PreToolUse", [])
        assert (RTK_HOOK in pre) == (r.name == "worker"), r.name


def test_tracked_worker_resolves(tracked: None) -> None:
    worker = role.load_role("worker")
    assert role.role_flags(worker, [], "R") == [
        "--model",
        "opus",
        "--effort",
        "medium",
        "--settings",
        "R/settings.json",
    ]
    off = role.skill_overrides(worker, role.disk_skills())
    assert set(off) == {
        "anthropic-skills:pdf",
        "anthropic-skills:zen-flow",
        "bruno",
        "diagram",
        "pi",
        "second-brain",
        "wiki",
        "wrapup",
    }


def _preset(name: str) -> str:
    presets = json.loads((TRACKED / "agent-manager" / "config.json").read_text())[
        "presets"
    ]
    return next(p["instructions"] for p in presets if p["name"] == name)


@pytest.mark.parametrize("preset", ["chief", "orchestrator"])
def test_spawn_lines_name_a_role_profile(tracked: None, preset: str) -> None:
    profiles = {f"role-{name}" for name in DAY_ONE}
    lines = [line for line in _preset(preset).splitlines() if "create_session" in line]
    assert lines
    for line in lines:
        named = set(re.findall(r"role-[a-z-]+", line))
        assert named, line
        assert named <= profiles, line
        assert "effort" not in line, line


def test_explorer_is_haiku_and_read_only(tracked: None) -> None:
    flags = role.role_flags(role.load_role("explorer"), [], "R")
    assert flags[:2] == ["--model", "haiku"]
    assert "--effort" not in flags
    assert flags[flags.index("--disallowedTools") + 1] == "Edit,Write,NotebookEdit"


def test_chief_sends_questions_to_the_explorer(tracked: None) -> None:
    step = next(
        line
        for line in _preset("chief").splitlines()
        if line.startswith("12.")
    )
    assert "role-explorer" in step and "role-worker" in step
    text = _preset("explorer")
    assert "send_session" in text and "archive_self" in text


def test_explore_override_runs_on_haiku_read_only() -> None:
    text = (TRACKED / ".claude" / "agents" / "Explore.md").read_text()
    head = text.split("---")[1]
    fields = dict(line.split(": ", 1) for line in head.strip().splitlines())
    assert fields["name"] == "Explore"
    assert fields["model"] == "haiku"
    assert fields["disallowedTools"] == "Edit, Write, NotebookEdit"
    assert "file:line" in fields["description"]


def test_chief_header_and_start_command(tracked: None) -> None:
    assert "effort max" not in _preset("chief").splitlines()[0]
    skill = (TRACKED / ".claude" / "skills" / "zen-flow" / "SKILL.md").read_text()
    start = re.search(r"agent-manager spawn \\\n  --name chief.*?```", skill, re.DOTALL)
    assert start is not None
    command = start.group(0)
    assert "--env role-chief" in command
    assert "--model" not in command and "--effort" not in command
    for name in DAY_ONE:
        assert f"`role-{name}`" in skill


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
