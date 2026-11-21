#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
#
# ==============================================================================
#
#               _
#     _ __ ___ | | ___       z e n - r o l e
#    | '__/ _ \| |/ _ \
#    | | | (_) | |  __/
#    |_|  \___/|_|\___|
#
# ------------------------------------------------------------------------------
#
#  NAME
#      role.py  --  per-role Claude Code config for agent-manager sessions
#
#  SYNOPSIS
#      zen-role list
#      zen-role show <name> [--json]
#      zen-role check
#      zen-role build
#
#  DESCRIPTION
#      agent-manager/roles.json declares each agent role: model, effort,
#      compaction window, a settings overlay, skills, denied tools and an
#      appended instructions file. `"isolate": true` loads only what the role
#      names instead of laying it over the global config.
#
#        list   the role names
#        show   what a role resolves to: flags and the settings overlay
#        check  validate roles.json against the presets, the skill dirs and
#               agent-manager/roles/
#        build  create or update one agent-manager env profile `role-<name>`
#               per role (ZEN_ROLE and a PATH led by agent-manager/shim)
#
#      `launch <real claude> <argv...>` is what agent-manager/shim/claude runs
#      for a session with ZEN_ROLE set: it merges the launcher's --settings
#      with the role overlay into ~/.cache/zen/roles/run/ and execs the CLI.
#
#  EXIT CODES
#      0  ok      1  invalid role config, or agent-manager failed
#
#  AUTHOR
#      vzsoares.
#
# ==============================================================================
"""Resolve agent roles from agent-manager/roles.json and launch Claude with them."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]

Obj = dict[str, object]

KEYS = {
    "saved",
    "model",
    "effort",
    "autocompact",
    "settings",
    "skills",
    "tools",
    "append",
    "isolate",
    "instructions",
}
EFFORTS = ("low", "medium", "high", "xhigh", "max")
MIN_WINDOW = 100_000
SYNCED_PREFIX = "anthropic-skills:"
PROFILE_PREFIX = "role-"
PLUGIN = "role"


class RoleError(Exception):
    """A role config problem; the message names the role and the key."""


@dataclass
class Role:
    name: str
    saved: str
    model: str | None = None
    effort: str | None = None
    autocompact: int | None = None
    settings: Obj = field(default_factory=dict)
    only: list[str] | None = None
    off: list[str] = field(default_factory=list)
    deny: list[str] = field(default_factory=list)
    append: str | None = None
    isolate: bool = False
    keep: list[str] = field(default_factory=list)


# --------------------------------------------------------------------------- #
# Paths                                                                         #
# --------------------------------------------------------------------------- #


def roles_file() -> Path:
    return REPO / "agent-manager" / "roles.json"


def presets_file() -> Path:
    return REPO / "agent-manager" / "config.json"


def append_dir() -> Path:
    return REPO / "agent-manager" / "roles"


def mcp_file() -> Path:
    return REPO / "agents" / "mcp.json"


def shim_dir() -> Path:
    return REPO / "agent-manager" / "shim"


def skills_root() -> Path:
    return Path.home() / ".claude" / "skills"


def global_settings() -> Path:
    return Path.home() / ".claude" / "settings.json"


def global_claude() -> Path:
    return Path.home() / ".claude" / "CLAUDE.md"


def run_root() -> Path:
    return Path.home() / ".cache" / "zen" / "roles" / "run"


# --------------------------------------------------------------------------- #
# Parsing                                                                       #
# --------------------------------------------------------------------------- #


def _names(where: str, value: object) -> list[str]:
    if not isinstance(value, list):
        raise RoleError(f"{where}: must be a list of names")
    names: list[str] = []
    for item in value:
        if not isinstance(item, str) or not item:
            raise RoleError(f"{where}: must be a list of names")
        names.append(item)
    return names


def _group(where: str, value: object, allowed: tuple[str, ...]) -> Obj:
    if not isinstance(value, dict):
        raise RoleError(f"{where}: must be an object")
    for key in value:
        if key not in allowed:
            raise RoleError(f"{where}.{key}: unknown key")
    return value


def _text(where: str, value: object) -> str:
    if not isinstance(value, str) or not value:
        raise RoleError(f"{where}: must be a non-empty string")
    return value


def parse_role(name: str, raw: object) -> Role:
    if not isinstance(raw, dict):
        raise RoleError(f"{name}: must be an object")
    for key in raw:
        if key not in KEYS:
            raise RoleError(f"{name}.{key}: unknown key")
    if "saved" not in raw:
        raise RoleError(f"{name}.saved: required")
    role = Role(name=name, saved=_text(f"{name}.saved", raw["saved"]))
    if "model" in raw:
        role.model = _text(f"{name}.model", raw["model"])
    if "effort" in raw:
        role.effort = _text(f"{name}.effort", raw["effort"])
        if role.effort not in EFFORTS:
            raise RoleError(f"{name}.effort: must be one of {', '.join(EFFORTS)}")
    if "autocompact" in raw:
        window = raw["autocompact"]
        if isinstance(window, bool) or not isinstance(window, int):
            raise RoleError(f"{name}.autocompact: must be a number of tokens")
        if window < MIN_WINDOW:
            raise RoleError(f"{name}.autocompact: must be {MIN_WINDOW} or more")
        role.autocompact = window
    if "settings" in raw:
        settings = raw["settings"]
        if not isinstance(settings, dict):
            raise RoleError(f"{name}.settings: must be an object")
        role.settings = settings
    if "skills" in raw:
        skills = _group(f"{name}.skills", raw["skills"], ("only", "off"))
        if "only" in skills:
            role.only = _names(f"{name}.skills.only", skills["only"])
        if "off" in skills:
            role.off = _names(f"{name}.skills.off", skills["off"])
    if "tools" in raw:
        tools = _group(f"{name}.tools", raw["tools"], ("deny",))
        if "deny" in tools:
            role.deny = _names(f"{name}.tools.deny", tools["deny"])
            if any("," in tool for tool in role.deny):
                raise RoleError(f"{name}.tools.deny: a tool name holds a comma")
    if "append" in raw:
        role.append = _text(f"{name}.append", raw["append"])
        base = append_dir().resolve()
        if not (base / role.append).resolve().is_relative_to(base):
            raise RoleError(f"{name}.append: must be a file under agent-manager/roles/")
    if "instructions" in raw:
        group = _group(f"{name}.instructions", raw["instructions"], ("keep",))
        if "keep" not in group:
            raise RoleError(f"{name}.instructions.keep: required")
        role.keep = _names(f"{name}.instructions.keep", group["keep"])
    if "isolate" in raw:
        if not isinstance(raw["isolate"], bool):
            raise RoleError(f"{name}.isolate: must be true or false")
        role.isolate = raw["isolate"]
    if role.isolate:
        if role.only is None:
            raise RoleError(f"{name}.isolate: requires skills.only")
        if "instructions" not in raw:
            raise RoleError(f"{name}.isolate: requires instructions")
        bare = [skill.removeprefix(SYNCED_PREFIX) for skill in role.only]
        if len(set(bare)) != len(bare):
            raise RoleError(
                f"{name}.skills.only: an isolated role cannot keep a skill "
                "and its anthropic-skills twin"
            )
    return role


def load_raw() -> Obj:
    path = roles_file()
    try:
        data = json.loads(path.read_text())
    except OSError as exc:
        raise RoleError(f"cannot read {path}: {exc.strerror}") from exc
    except ValueError as exc:
        raise RoleError(f"{path} is not valid JSON: {exc}") from exc
    if not isinstance(data, dict):
        raise RoleError(f"{path}: must be an object of roles")
    return data


def load_role(name: str) -> Role:
    raw = load_raw()
    if name not in raw:
        raise RoleError(f"unknown role '{name}' (roles: {', '.join(raw)})")
    return parse_role(name, raw[name])


def load_roles() -> list[Role]:
    return [parse_role(name, raw) for name, raw in load_raw().items()]


# --------------------------------------------------------------------------- #
# Resolution                                                                    #
# --------------------------------------------------------------------------- #


def skill_sources() -> dict[str, Path]:
    """Skill dirs on disk: user skills by name, synced ones as
    `anthropic-skills:<name>`.

    A dir without SKILL.md (a plugin such as clawd-status) is not a skill.
    """
    root = skills_root()
    found: dict[str, Path] = {}
    if not root.is_dir():
        return found
    for entry in root.iterdir():
        if entry.name != "synced" and (entry / "SKILL.md").is_file():
            found[entry.name] = entry
    synced = root / "synced"
    if synced.is_dir():
        for bucket in synced.iterdir():
            if bucket.name.startswith(".") or not bucket.is_dir():
                continue
            for entry in bucket.iterdir():
                if entry.is_dir() and not entry.name.startswith("."):
                    found[SYNCED_PREFIX + entry.name] = entry
    return found


def disk_skills() -> set[str]:
    return set(skill_sources())


def skill_overrides(role: Role, disk: set[str]) -> dict[str, str]:
    off = set(role.off)
    if role.only is not None:
        off |= disk - set(role.only)
    return dict.fromkeys(sorted(off), "off")


def merge(base: Obj, top: Obj) -> Obj:
    """Deep merge: objects key by key, lists joined, `top` wins a scalar."""
    out = dict(base)
    for key, value in top.items():
        current = out.get(key)
        if isinstance(current, dict) and isinstance(value, dict):
            out[key] = merge(current, value)
        elif isinstance(current, list) and isinstance(value, list):
            out[key] = current + [item for item in value if item not in current]
        else:
            out[key] = value
    return out


def window_models() -> list[str]:
    """Models whose window the global settings pin; a role's window overrides each."""
    try:
        data = json.loads(global_settings().read_text())
    except (OSError, ValueError):
        return []
    models = data.get("modelSettings") if isinstance(data, dict) else None
    if not isinstance(models, dict):
        return []
    return [
        model
        for model, value in models.items()
        if isinstance(value, dict) and "autoCompactWindow" in value
    ]


def overlay(role: Role, disk: set[str], models: list[str]) -> Obj:
    generated: Obj = {}
    overrides = skill_overrides(role, disk)
    if overrides:
        generated["skillOverrides"] = overrides
    if role.autocompact is not None:
        generated["autoCompactWindow"] = role.autocompact
        if models:
            generated["modelSettings"] = {
                model: {"autoCompactWindow": role.autocompact} for model in models
            }
    return merge(role.settings, generated)


def append_file(role: Role) -> Path | None:
    if role.append is None:
        return None
    path = append_dir() / role.append
    if not path.is_file():
        raise RoleError(f"{role.name}.append: {path} is not a file")
    return path


def sections(text: str) -> list[tuple[str, str]]:
    """Top-level `# ` sections as (title, text). A `# ` line inside a fenced
    block is a shell comment, not a title."""
    found: list[tuple[str, str]] = []
    fenced = False
    for line in text.splitlines(keepends=True):
        if line.lstrip().startswith(("```", "~~~")):
            fenced = not fenced
        elif not fenced and line.startswith("# "):
            found.append((line[2:].strip(), ""))
        if found:
            title, body = found[-1]
            found[-1] = (title, body + line)
    return found


def read_text(path: Path) -> str:
    try:
        return path.read_text()
    except OSError as exc:
        raise RoleError(f"cannot read {path}: {exc.strerror}") from exc
    except UnicodeDecodeError as exc:
        raise RoleError(f"cannot read {path}: {exc}") from exc


def kept_sections(role: Role) -> list[tuple[str, str]]:
    """The sections of the global CLAUDE.md an isolated role keeps, in file order."""
    path = global_claude()
    have = sections(read_text(path))
    titles = [title for title, _ in have]
    for title in role.keep:
        if title not in titles:
            raise RoleError(
                f"{role.name}.instructions.keep: '{title}' is no section of {path}"
            )
    return [(title, body) for title, body in have if title in role.keep]


def build_instructions(role: Role, cwd: Path) -> str:
    """Kept global sections, then the working directory's CLAUDE.md, then `append`."""
    parts = [body.rstrip() + "\n" for _, body in kept_sections(role)]
    project = cwd / "CLAUDE.md"
    if project.is_file():
        text = read_text(project).rstrip()
        parts.append(f"# Project instructions ({project})\n\n{text}\n")
    appended = append_file(role)
    if appended is not None:
        parts.append(read_text(appended).rstrip() + "\n")
    return "\n".join(parts)


def build_plugin(role: Role, target: Path) -> None:
    """A plugin dir holding links to the kept skills, listed as `role:<skill>`."""
    shutil.rmtree(target, ignore_errors=True)
    (target / ".claude-plugin").mkdir(parents=True)
    (target / "skills").mkdir()
    manifest = {
        "name": PLUGIN,
        "version": "0.0.0",
        "description": f"skills of the agent role {role.name}",
    }
    (target / ".claude-plugin" / "plugin.json").write_text(json.dumps(manifest) + "\n")
    sources = skill_sources()
    for skill in role.only or []:
        if skill in sources:
            link = target / "skills" / skill.removeprefix(SYNCED_PREFIX)
            link.symlink_to(sources[skill], target_is_directory=True)


def isolated_base() -> Obj:
    """The live global settings: an isolated role loads no settings layer, so
    its one --settings file carries them."""
    path = global_settings()
    try:
        data = json.loads(path.read_text())
    except OSError as exc:
        raise RoleError(f"cannot read {path}: {exc.strerror}") from exc
    except ValueError as exc:
        raise RoleError(f"{path} is not valid JSON: {exc}") from exc
    if not isinstance(data, dict):
        raise RoleError(f"{path}: must be a JSON object")
    return data


def has_flag(args: list[str], flag: str) -> bool:
    return any(arg == flag or arg.startswith(flag + "=") for arg in args)


def role_flags(role: Role, args: list[str], run: str) -> list[str]:
    """Flags laid in front of the launch argv. --settings stays last: it ends
    the variadic --disallowedTools before the prompt argument."""
    flags: list[str] = []
    if role.model and not has_flag(args, "--model"):
        flags += ["--model", role.model]
    if role.effort and not has_flag(args, "--effort"):
        flags += ["--effort", role.effort]
    if role.autocompact is not None and not has_flag(args, "--autocompact"):
        flags += ["--autocompact", str(role.autocompact)]
    if role.deny:
        flags += ["--disallowedTools", ",".join(role.deny)]
    if role.isolate:
        append_file(role)
        flags += ["--setting-sources", "local", "--plugin-dir", f"{run}/plugin"]
        # Without the user source the CLI drops the user-scope MCP servers.
        flags += ["--mcp-config", str(mcp_file())]
        flags += ["--append-system-prompt-file", f"{run}/instructions.md"]
    else:
        appended = append_file(role)
        if appended is not None:
            flags += ["--append-system-prompt-file", str(appended)]
    return [*flags, "--settings", f"{run}/settings.json"]


# --------------------------------------------------------------------------- #
# Launch                                                                        #
# --------------------------------------------------------------------------- #


def split_settings(args: list[str]) -> tuple[list[str], list[str]]:
    rest: list[str] = []
    found: list[str] = []
    i = 0
    while i < len(args):
        arg = args[i]
        if arg == "--settings" and i + 1 < len(args):
            found.append(args[i + 1])
            i += 2
            continue
        if arg.startswith("--settings="):
            found.append(arg.split("=", 1)[1])
        else:
            rest.append(arg)
        i += 1
    return rest, found


def read_launcher(value: str) -> tuple[Obj, Path | None]:
    """A --settings value is a file path or an inline JSON object."""
    inline = value.lstrip().startswith("{")
    try:
        data = json.loads(value if inline else Path(value).read_text())
    except OSError as exc:
        raise RoleError(f"launcher --settings {value}: {exc.strerror}") from exc
    except ValueError as exc:
        raise RoleError(f"launcher --settings is not valid JSON: {exc}") from exc
    if not isinstance(data, dict):
        raise RoleError("launcher --settings must be a JSON object")
    return data, None if inline else Path(value).resolve()


def write_atomic(path: Path, text: str) -> None:
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.")
    with os.fdopen(fd, "w") as handle:
        handle.write(text)
    os.replace(tmp, path)


def _alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def _read(path: Path) -> str | None:
    try:
        return path.read_text().strip()
    except OSError:
        return None


def prune(keep: Path) -> None:
    """Drop run sets whose launcher file and CLI process are both gone."""
    for entry in run_root().iterdir():
        if entry == keep or not entry.is_dir():
            continue
        pid = _read(entry / "pid") or entry.name.removeprefix("pid-")
        if pid.isdigit() and _alive(int(pid)):
            continue
        launcher = _read(entry / "launcher")
        named_by_pid = entry.name.startswith("pid-")
        if named_by_pid or (launcher is not None and not Path(launcher).exists()):
            shutil.rmtree(entry, ignore_errors=True)


def host_env(env: dict[str, str]) -> dict[str, str]:
    """`env` without what `uv run` adds for this script: its venv's bin dir on
    PATH and VIRTUAL_ENV, which would otherwise reach the CLI and the profiles."""
    out = dict(env)
    if sys.prefix == sys.base_prefix or out.get("VIRTUAL_ENV") != sys.prefix:
        return out
    del out["VIRTUAL_ENV"]
    own = str(Path(sys.prefix) / "bin")
    out["PATH"] = ":".join(
        part for part in out.get("PATH", "").split(":") if part.rstrip("/") != own
    )
    return out


def launch_argv(name: str, real: str, args: list[str]) -> list[str]:
    role = load_role(name)
    rest, found = split_settings(args)
    launcher: Obj = {}
    source: Path | None = None
    for value in found:
        data, path = read_launcher(value)
        launcher = merge(launcher, data)
        source = path or source
    layered = overlay(role, disk_skills(), window_models())
    instructions = ""
    if role.isolate:
        if not mcp_file().is_file():
            raise RoleError(f"{role.name}.isolate: {mcp_file()} is not a file")
        layered = merge(isolated_base(), layered)
        instructions = build_instructions(role, Path.cwd())
    merged = merge(layered, launcher)

    run = run_root() / (source.name if source else f"pid-{os.getpid()}")
    run.mkdir(parents=True, exist_ok=True)
    if source:
        write_atomic(run / "launcher", f"{source}\n")
    # exec keeps the pid, so it names the CLI process that reads this set.
    write_atomic(run / "pid", f"{os.getpid()}\n")
    flags = role_flags(role, rest, str(run))
    write_atomic(run / "settings.json", json.dumps(merged, indent=2) + "\n")
    if role.isolate:
        write_atomic(run / "instructions.md", instructions)
        build_plugin(role, run / "plugin")
    prune(run)
    return [real, *flags, *rest]


# --------------------------------------------------------------------------- #
# Commands                                                                      #
# --------------------------------------------------------------------------- #


def preset_names() -> set[str]:
    path = presets_file()
    try:
        data = json.loads(path.read_text())
    except (OSError, ValueError) as exc:
        raise RoleError(f"cannot read presets from {path}: {exc}") from exc
    presets = data.get("presets") if isinstance(data, dict) else None
    if not isinstance(presets, list):
        raise RoleError(f"{path}: no presets list")
    return {
        preset["name"]
        for preset in presets
        if isinstance(preset, dict) and isinstance(preset.get("name"), str)
    }


def check_errors() -> list[str]:
    errors: list[str] = []
    presets = preset_names()
    disk = disk_skills()
    for name, raw in load_raw().items():
        try:
            role = parse_role(name, raw)
        except RoleError as exc:
            errors.append(str(exc))
            continue
        if role.saved not in presets:
            errors.append(f"{name}.saved: no preset named '{role.saved}'")
        for skill in role.only or []:
            if skill not in disk:
                errors.append(f"{name}.skills.only: '{skill}' is not on disk")
        try:
            append_file(role)
        except RoleError as exc:
            errors.append(str(exc))
        if not role.isolate:
            continue
        try:
            kept_sections(role)
        except RoleError as exc:
            errors.append(str(exc))
        if not mcp_file().is_file():
            errors.append(f"{name}.isolate: {mcp_file()} is not a file")
    return errors


def cmd_check() -> int:
    errors = check_errors()
    for error in errors:
        print(f"zen-role: {error}", file=sys.stderr)
    if errors:
        return 1
    print(f"ok: {len(load_raw())} roles")
    return 0


def cmd_list() -> int:
    for name in load_raw():
        print(name)
    return 0


def cmd_show(name: str, as_json: bool) -> int:
    role = load_role(name)
    mode = "isolate" if role.isolate else "overlay"
    profile = PROFILE_PREFIX + role.name
    flags = role_flags(role, [], "<run>")
    settings = overlay(role, disk_skills(), window_models())
    kept = [title for title, _ in kept_sections(role)] if role.isolate else []
    skills = [f"{PLUGIN}:{s.removeprefix(SYNCED_PREFIX)}" for s in role.only or []]
    if as_json:
        resolved: Obj = {
            "name": role.name,
            "saved": role.saved,
            "mode": mode,
            "profile": profile,
            "flags": flags,
            "settings": settings,
        }
        if role.isolate:
            resolved["sections"] = kept
            resolved["skills"] = skills
        print(json.dumps(resolved, indent=2))
        return 0
    print(f"role:     {role.name} (saved {role.saved})")
    print(f"mode:     {mode}")
    print(f"profile:  {profile}")
    print(f"flags:    {' '.join(flags)}")
    if role.isolate:
        print(f"sections: {'; '.join(kept)}")
        print(f"skills:   {' '.join(skills)}")
        print("settings (over the global settings.json):")
    else:
        print("settings:")
    print(json.dumps(settings, indent=2))
    return 0


def _manager(*args: str) -> str:
    proc = subprocess.run(
        ["agent-manager", *args], capture_output=True, text=True, check=False
    )
    if proc.returncode != 0:
        detail = proc.stderr.strip() or f"exit {proc.returncode}"
        raise RoleError(f"agent-manager {' '.join(args[:3])}: {detail}")
    return proc.stdout


def _manager_json(*args: str) -> object:
    try:
        return json.loads(_manager(*args))
    except ValueError as exc:
        raise RoleError(f"agent-manager {' '.join(args[:3])}: not JSON: {exc}") from exc


def profile_path(shim: str, path: str) -> str:
    """The shim dir once, in front; relative entries mean nothing in a stored profile."""
    rest = [
        part
        for part in path.split(":")
        if part.startswith("/") and part.rstrip("/") != shim
    ]
    return ":".join([shim, *rest])


def cmd_build() -> int:
    roles = load_roles()
    if (REPO / ".git").is_file():
        raise RoleError(
            f"build runs from the main checkout; {REPO} is a linked worktree "
            "and its shim path would not outlive it"
        )
    if shutil.which("agent-manager") is None:
        raise RoleError("agent-manager is not on PATH")
    shim = str(shim_dir())
    path = profile_path(shim, host_env(dict(os.environ)).get("PATH", ""))
    listed = _manager_json("env", "list", "--json")
    if not isinstance(listed, list):
        raise RoleError("agent-manager env list --json: not a list of profiles")
    existing = {
        entry["name"]
        for entry in listed
        if isinstance(entry, dict) and isinstance(entry.get("name"), str)
    }
    for role in roles:
        profile = PROFILE_PREFIX + role.name
        wanted = {"ZEN_ROLE": role.name, "PATH": path}
        if profile in existing:
            shown = _manager_json("env", "show", profile, "--json")
            current = shown.get("vars") if isinstance(shown, dict) else None
            if isinstance(current, dict) and all(
                current.get(key) == value for key, value in wanted.items()
            ):
                print(f"unchanged {profile}")
                continue
        _manager("env", "set", profile, *(f"{k}={v}" for k, v in wanted.items()))
        print(f"set       {profile}")
    names = {PROFILE_PREFIX + role.name for role in roles}
    for profile in sorted(existing - names):
        if profile.startswith(PROFILE_PREFIX):
            print(f"orphan    {profile}: no entry in roles.json, left as is")
    return 0


def main(argv: list[str]) -> int:
    try:
        if argv[:1] == ["launch"]:
            if len(argv) < 2:
                raise RoleError("launch needs the real claude path")
            name = os.environ.get("ZEN_ROLE", "")
            command = launch_argv(name, argv[1], argv[2:])
            os.execve(command[0], command, host_env(dict(os.environ)))
        parser = argparse.ArgumentParser(
            prog="zen-role", description="Per-role Claude Code config."
        )
        sub = parser.add_subparsers(dest="command", required=True)
        sub.add_parser("list", help="print the role names")
        show = sub.add_parser("show", help="print what a role resolves to")
        show.add_argument("name")
        show.add_argument("--json", action="store_true")
        sub.add_parser("check", help="validate agent-manager/roles.json")
        sub.add_parser("build", help="set the role-<name> env profiles")
        opts = parser.parse_args(argv)
        if opts.command == "list":
            return cmd_list()
        if opts.command == "show":
            return cmd_show(opts.name, opts.json)
        if opts.command == "check":
            return cmd_check()
        return cmd_build()
    except RoleError as exc:
        print(f"zen-role: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
