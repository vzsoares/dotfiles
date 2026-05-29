#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "tomlkit>=0.13",
#     "typer>=0.12",
# ]
# ///
#
# ==============================================================================
#
#      ____  _____ _     _____    _    ____  _____
#     |  _ \| ____| |   | ____|  / \  / ___|| ____|
#     | |_) |  _| | |   |  _|   / _ \ \___ \|  _|        z e n - r e l e a s e
#     |  _ <| |___| |___| |___ / ___ \ ___) | |___
#     |_| \_\_____|_____|_____/_/   \_\____/|_____|
#
# ------------------------------------------------------------------------------
#
#  NAME
#      release.py  --  resumable, interactive release orchestrator
#
#  SYNOPSIS
#      zen-release setup                        # run once: detect tools
#      zen-release [options]                    # run a release
#      zen-release --dev                        # quick dev (-dev.N) release
#
#  OPTIONS
#      --resume        Continue a release interrupted mid-run.
#      --restart       Discard saved state and start over.
#      --dry-run       Echo mutating commands instead of running them.
#      --dev           Dev release: bump to -dev.N, tag + push current branch.
#      --no-scan       Skip the gitleaks secret scan.
#      --no-changelog  Skip changelog generation.
#      --amend-changelog  Fold the changelog into the version-bump commit.
#      --reconfigure   Re-run this repo's first-run config (branches/phases).
#      --yes / -y      Headless: no prompts, auto-confirm (needs --bump).
#      --bump VALUE    Headless version choice: patch|minor|major|finalize|auto.
#      --source/--target  Override the source/target branches for this run.
#
#  HEADLESS
#      zen-release --yes --bump patch          # full release, no prompts
#      zen-release --dev --yes --bump patch    # dev release, no prompts
#
#  DESCRIPTION
#      A thin orchestrator: it shells out to best-in-class external tools, one
#      per phase, and does NOT reimplement them.  `setup` probes the toolchain
#      once and writes a global config (~/.config/zen-release/config.json);
#      release runs read that config.  There is no fallback tool chain -- if a
#      tool is optional and absent, its phase is simply skipped with a notice.
#      Required tools (git, gum) must be present or `setup` refuses to save.
#
#      On its first run in a repo it asks (once) for the source/target branches
#      and which optional phases to enable, saving them to a per-repo config so
#      later runs don't re-ask. Change those answers later with --reconfigure.
#
#      The branch you're on is the source: running from a feature branch merges
#      it into the configured target. Only when you're already on the target is
#      the configured source used (empty = release the target with no merge).
#      --source/--target override the saved config for a single run, headless or
#      interactive.
#
#  PHASES
#      hooks -> secret_scan -> branch+version -> merge -> write_version
#      -> changelog -> tag -> push -> publish -> github_release -> rebase_source
#      Each phase is idempotent so --resume is safe after a mid-phase crash.
#      Optional phases (hooks, secret_scan, changelog, publish, github_release)
#      can be toggled per repo.
#
#  FILES
#      ~/.config/zen-release/config.json   detected toolchain (from `setup`)
#      .git/zen-release.json               per-repo branches + phase toggles
#      .git/release-state.json             per-run resume state (auto-removed)
#
#  REQUIRES
#      uv (implicit), git, gum.  Optional: gitleaks, gh, git-cliff,
#      semantic-release, pre-commit, lefthook.
#
#  AUTHOR
#      vzsoares.  See docs/wiki/architecture/release-tooling.md for the design.
#
# ==============================================================================
"""Resumable release orchestrator.

A thin orchestrator that shells out to external tools per phase. Tools are
detected once by `release.py setup` and saved to a global config; release runs
read that config and skip any phase whose tool isn't enabled. There is no
fallback tool chain — a missing/disabled tool means the phase is skipped with a
notice. See docs/wiki/architecture/release-tooling.md for the design.

# TODO: monorepo / multiple packages per repo
# TODO: signing tags/artifacts (GPG, cosign)
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import time
from collections.abc import Callable
from dataclasses import asdict, dataclass, field
from pathlib import Path

import typer

# --------------------------------------------------------------------------- #
# Tool registry — what `setup` probes and saves                               #
# --------------------------------------------------------------------------- #


@dataclass(frozen=True)
class ToolSpec:
    """An external tool we can orchestrate, with how to check and install it.

    `required` tools must be present for `setup` to succeed at all; optional
    tools that are missing just cause their phase to be skipped at release time.
    """

    name: str
    check: list[str]
    install_hint: str
    required: bool = False


TOOL_SPECS: tuple[ToolSpec, ...] = (
    # git — the substrate: every phase shells out to it (branch, merge, tag,
    # push, rebase). Nothing works without it, so it's required.
    ToolSpec(
        "git",
        ["git", "--version"],
        "https://git-scm.com/downloads",
        required=True,
    ),
    # gum — the entire interactive UI (prompts, menus, confirms, spinners). The
    # orchestrator is interactive by design, so this is required too.
    ToolSpec(
        "gum",
        ["gum", "--version"],
        "mise use -g gum   (or: go install github.com/charmbracelet/gum@latest)",
        required=True,
    ),
    # gitleaks — scans the repo for committed secrets (API keys, tokens) before
    # you tag and publish, so a leak never makes it into a release.
    ToolSpec(
        "gitleaks",
        ["gitleaks", "version"],
        "mise use -g gitleaks   (or: go install github.com/gitleaks/gitleaks/v8@latest)",
    ),
    # gh — GitHub's CLI; lets the run create a GitHub Release with notes drawn
    # from the changelog. Skipped for non-GitHub remotes.
    ToolSpec(
        "gh",
        ["gh", "--version"],
        "https://cli.github.com/   (then: gh auth login)",
    ),
    # git-cliff — builds a polished, grouped CHANGELOG from conventional commits.
    # When absent we fall back to git's own log rendering (built-in, not a tool).
    ToolSpec(
        "git-cliff",
        ["git-cliff", "--version"],
        "uv tool install git-cliff   (or: cargo install git-cliff)",
    ),
    # semantic-release — computes the next version automatically from conventional
    # commits, offered as the "auto" choice in the bump menu.
    ToolSpec(
        "semantic-release",
        ["semantic-release", "--version"],
        "go install github.com/go-semantic-release/semantic-release/v2@latest",
    ),
    # pre-commit — runs the repo's pre-commit/pre-push hook suite as a gate before
    # any branch mutation, so a release can't proceed on failing checks.
    ToolSpec(
        "pre-commit",
        ["pre-commit", "--version"],
        "uv tool install pre-commit",
    ),
    # lefthook — the same hook-gate role as pre-commit, for repos that use lefthook
    # to manage their git hooks instead.
    ToolSpec(
        "lefthook",
        ["lefthook", "version"],
        "mise use -g lefthook   (or: go install github.com/evilmartians/lefthook@latest)",
    ),
)

TOOL_BY_NAME: dict[str, ToolSpec] = {spec.name: spec for spec in TOOL_SPECS}

CONFIG_SCHEMA = 1
STATE_SCHEMA = 1
REPO_SCHEMA = 1

# Optional phases a repo can toggle on/off in its per-repo config. Core phases
# (merge, write_version, tag, push, rebase_source) always run, gated by no_merge.
OPTIONAL_PHASES: tuple[str, ...] = (
    "hooks",
    "secret_scan",
    "changelog",
    "publish",
    "github_release",
)


# --------------------------------------------------------------------------- #
# Global config (`~/.config/zen-release/config.json`)                         #
# --------------------------------------------------------------------------- #


@dataclass
class ToolStatus:
    enabled: bool
    path: str
    version: str


@dataclass
class Config:
    schema: int
    created_at: float
    tools: dict[str, ToolStatus]

    def enabled(self, tool: str) -> bool:
        status = self.tools.get(tool)
        return status is not None and status.enabled


def config_path() -> Path:
    base = os.environ.get("XDG_CONFIG_HOME") or str(Path.home() / ".config")
    return Path(base) / "zen-release" / "config.json"


def load_config() -> Config | None:
    path = config_path()
    if not path.exists():
        return None
    raw = json.loads(path.read_text())
    if not isinstance(raw, dict):
        return None
    tools_raw = raw.get("tools")
    tools: dict[str, ToolStatus] = {}
    if isinstance(tools_raw, dict):
        for name, status in tools_raw.items():
            if not isinstance(status, dict):
                continue
            tools[str(name)] = ToolStatus(
                enabled=bool(status.get("enabled", False)),
                path=str(status.get("path", "")),
                version=str(status.get("version", "")),
            )
    schema_raw = raw.get("schema", 0)
    created_raw = raw.get("created_at", 0.0)
    return Config(
        schema=int(schema_raw) if isinstance(schema_raw, (int, float)) else 0,
        created_at=float(created_raw) if isinstance(created_raw, (int, float)) else 0.0,
        tools=tools,
    )


def save_config(config: Config) -> None:
    path = config_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "schema": config.schema,
        "created_at": config.created_at,
        "tools": {name: asdict(status) for name, status in config.tools.items()},
    }
    path.write_text(json.dumps(payload, indent=2) + "\n")


def probe_tool(spec: ToolSpec) -> ToolStatus:
    """Detect a tool and verify it runs. Disabled if absent or the probe fails."""
    found = shutil.which(spec.name)
    if found is None:
        return ToolStatus(enabled=False, path="", version="")
    try:
        result = subprocess.run(
            spec.check,
            capture_output=True,
            text=True,
            timeout=10,
            check=False,
        )
    except (OSError, subprocess.SubprocessError):
        return ToolStatus(enabled=False, path=found, version="")
    if result.returncode != 0:
        return ToolStatus(enabled=False, path=found, version="")
    version = (result.stdout or result.stderr).strip().splitlines()
    return ToolStatus(enabled=True, path=found, version=version[0] if version else "")


def missing_required(tools: dict[str, ToolStatus]) -> list[ToolSpec]:
    """Required tool specs that aren't enabled in the given probe results."""
    return [
        spec
        for spec in TOOL_SPECS
        if spec.required and not (tools.get(spec.name) and tools[spec.name].enabled)
    ]


# --------------------------------------------------------------------------- #
# gum wrappers (TUI). --dry-run short-circuits mutating phases, not prompts.   #
# --------------------------------------------------------------------------- #

DRY_RUN = False
# Headless: set by --yes. No gum prompts — choices come from flags, confirms are
# auto-accepted. (Styling/spinners still print fine without a TTY.)
HEADLESS = False


def _no_prompt(what: str) -> None:
    """Abort: a prompt was reached in headless mode (a flag should cover it)."""
    fail(f"Headless run can't prompt for {what} — pass the matching flag.")
    raise typer.Exit(1)


def _gum(*args: str) -> str:
    # Capture stdout only (the chosen value); gum draws its interactive UI on
    # stderr and reads the tty, so stderr/stdin must stay attached to the
    # terminal or the prompt is invisible and appears to hang.
    result = subprocess.run(
        ["gum", *args], text=True, stdout=subprocess.PIPE, check=False
    )
    if result.returncode != 0:
        # gum returns non-zero on cancel (Esc/Ctrl-C); treat as an abort.
        raise typer.Abort()
    return result.stdout.strip()


def gum_style(text: str, *flags: str) -> None:
    subprocess.run(["gum", "style", *flags, text], check=False)


def info(text: str) -> None:
    gum_style(text, "--faint")


def good(text: str) -> None:
    gum_style(text, "--foreground", "82")


def warn(text: str) -> None:
    gum_style(text, "--foreground", "214")


def fail(text: str) -> None:
    gum_style(text, "--foreground", "196")


def banner(text: str, color: str = "212") -> None:
    gum_style(
        text,
        "--bold",
        "--border",
        "double",
        "--padding",
        "0 2",
        "--border-foreground",
        color,
    )


def gum_choose(header: str, options: list[str]) -> str:
    if HEADLESS:
        _no_prompt(header)
    return _gum("choose", "--header", header, *options)


def gum_choose_multi(header: str, options: list[str], selected: list[str]) -> list[str]:
    """Checklist multi-select; `selected` are pre-checked. Returns chosen items."""
    if HEADLESS:
        _no_prompt(header)
    args = ["choose", "--no-limit", "--header", header]
    if selected:
        args += ["--selected", ",".join(selected)]
    out = _gum(*args, *options)
    return [line for line in out.splitlines() if line.strip()]


def gum_input(header: str, placeholder: str = "", value: str = "") -> str:
    if HEADLESS:
        _no_prompt(header)
    args = ["input", "--header", header]
    if placeholder:
        args += ["--placeholder", placeholder]
    if value:
        args += ["--value", value]
    return _gum(*args)


def gum_confirm(prompt: str) -> bool:
    if HEADLESS:
        # --yes implies "yes" to every confirmation.
        info(f"[--yes] {prompt}")
        return True
    result = subprocess.run(["gum", "confirm", prompt], check=False)
    return result.returncode == 0


def run(cmd: list[str], *, title: str = "") -> subprocess.CompletedProcess[str]:
    """Run a command, optionally under `gum spin`. Honors --dry-run."""
    if DRY_RUN:
        info(f"[dry-run] {' '.join(cmd)}")
        return subprocess.CompletedProcess(cmd, 0, "", "")
    if title:
        spin = subprocess.run(
            ["gum", "spin", "--show-error", "--title", title, "--", *cmd], check=False
        )
        return subprocess.CompletedProcess(cmd, spin.returncode, "", "")
    return subprocess.run(cmd, text=True, capture_output=True, check=False)


def git(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["git", *args], text=True, capture_output=True, check=False)


# --------------------------------------------------------------------------- #
# Release state (`.git/release-state.json`)                                    #
# --------------------------------------------------------------------------- #


@dataclass
class State:
    schema: int = STATE_SCHEMA
    updated_at: float = 0.0
    version: str = ""
    source_branch: str = ""
    target_branch: str = ""
    no_merge: bool = True
    # Ordered merge chain, target last (e.g. ["feature/x", "develop", "master"]).
    # Each consecutive pair is one merge; the last lands on the target. Empty/one
    # element = no merge. source_branch/target_branch are kept as the immediate
    # source (chain[-2]) and the target (chain[-1]) for the tag/push/rebase phases.
    branches: list[str] = field(default_factory=list)
    dev: bool = False  # lightweight dev release (-dev.N tag on current branch)
    # per-run repo capabilities
    project_type: str = "generic"
    is_github: bool = False
    publishable: bool = False
    hook_manager: str = "none"
    # phase tracking
    phases: dict[str, str] = field(default_factory=dict)
    changelog: str = ""
    # outcomes (shown in the finalize summary)
    published: str = ""
    github_release_url: str = ""

    def status(self, phase: str) -> str:
        return self.phases.get(phase, "pending")


def state_path() -> Path:
    return git_dir() / "release-state.json"


def git_dir() -> Path:
    result = git("rev-parse", "--git-dir")
    return Path(result.stdout.strip())


def load_state() -> State | None:
    path = state_path()
    if not path.exists():
        return None
    raw = json.loads(path.read_text())
    if not isinstance(raw, dict):
        return None
    state = State()
    for key, value in raw.items():
        if hasattr(state, str(key)):
            setattr(state, str(key), value)
    if not isinstance(state.phases, dict):
        state.phases = {}
    return state


def save_state(state: State) -> None:
    state.updated_at = time.time()
    state_path().write_text(json.dumps(asdict(state), indent=2) + "\n")


def clear_state() -> None:
    state_path().unlink(missing_ok=True)


# --------------------------------------------------------------------------- #
# Per-repo config (`.git/zen-release.json`) — uncommitted, per-clone           #
# --------------------------------------------------------------------------- #


@dataclass
class RepoConfig:
    """What this repo wants done: branch defaults + which optional phases run."""

    schema: int
    source_branch: str  # empty = release current branch (no merge)
    target_branch: str
    phases: dict[str, bool]

    def enabled(self, phase: str) -> bool:
        return self.phases.get(phase, True)


def repo_config_path() -> Path:
    return git_dir() / "zen-release.json"


def load_repo_config() -> RepoConfig | None:
    path = repo_config_path()
    if not path.exists():
        return None
    raw = json.loads(path.read_text())
    if not isinstance(raw, dict):
        return None
    branches = raw.get("branches")
    source = str(branches.get("source", "")) if isinstance(branches, dict) else ""
    target = str(branches.get("target", "")) if isinstance(branches, dict) else ""
    phases_raw = raw.get("phases")
    phases: dict[str, bool] = {}
    if isinstance(phases_raw, dict):
        for name in OPTIONAL_PHASES:
            if name in phases_raw:
                phases[name] = bool(phases_raw[name])
    schema_raw = raw.get("schema", 0)
    return RepoConfig(
        schema=int(schema_raw) if isinstance(schema_raw, (int, float)) else 0,
        source_branch=source,
        target_branch=target,
        phases=phases,
    )


def save_repo_config(cfg: RepoConfig) -> None:
    payload = {
        "schema": cfg.schema,
        "branches": {"source": cfg.source_branch, "target": cfg.target_branch},
        "phases": {name: cfg.enabled(name) for name in OPTIONAL_PHASES},
    }
    repo_config_path().write_text(json.dumps(payload, indent=2) + "\n")


def detected_phase_defaults(state: State, config: Config) -> dict[str, bool]:
    """Sensible pre-checked defaults for the first-run phase picker."""
    return {
        "hooks": state.hook_manager != "none",
        "secret_scan": config.enabled("gitleaks"),
        "changelog": True,
        "publish": state.publishable,
        "github_release": state.is_github,
    }


def create_repo_config(state: State, config: Config) -> RepoConfig:
    """First-run interactive setup for this repo (branches + phase toggles)."""
    banner("First run — configure this repo")

    # Empty defaults — you type the branches. Empty source = no merge; the
    # target branch is always asked so it can be set even without a source.
    source = gum_input(
        "Source branch to merge FROM (empty = no merge)",
        placeholder="e.g. develop",
    )
    target = gum_input(
        "Target branch to release ON" + (" / merge INTO" if source else ""),
        placeholder="e.g. master",
    )

    defaults = detected_phase_defaults(state, config)
    chosen = gum_choose_multi(
        "Enable which phases for this repo? (space toggles, enter saves)",
        list(OPTIONAL_PHASES),
        [name for name in OPTIONAL_PHASES if defaults[name]],
    )
    phases = {name: name in chosen for name in OPTIONAL_PHASES}

    cfg = RepoConfig(
        schema=REPO_SCHEMA, source_branch=source, target_branch=target, phases=phases
    )
    save_repo_config(cfg)
    good(f"Saved repo config to {repo_config_path()}")
    return cfg


def headless_repo_config(
    state: State, config: Config, source: str, target: str
) -> RepoConfig:
    """Ephemeral repo config for a headless run with no saved one (not persisted).

    Branches come from --source/--target; an empty source means the branch you're
    on is used (the current branch is merged into the target, or released directly
    when you're already on it). Phases default to detection. Run interactively once
    to persist real choices.
    """
    defaults = detected_phase_defaults(state, config)
    info("No repo config — using detection defaults (run interactively once to save).")
    return RepoConfig(
        schema=REPO_SCHEMA,
        source_branch=source,
        target_branch=target,
        phases=dict(defaults),
    )


def apply_branches_from_repo(
    state: State,
    repo_cfg: RepoConfig,
    cli_source: str = "",
    cli_target: str = "",
) -> None:
    """Resolve the merge chain for this run (target last), then validate it.

    The chain is the branch you're on flowing up through the configured source into
    the target — `feature/xyz -> develop -> master`. Each leg is one merge; the
    last lands the release on the target. The shape adapts to where you stand:

      * on a feature branch + a source set  -> [current, source, target]
      * on a feature branch + no source     -> [current, target]
      * on the target + a source set        -> [source, target]
      * on the target + no source           -> [target]  (release in place, no merge)

    --source/--target override the saved config (the source still merges *after*
    the branch you're on). No branch switch happens unless a merge needs it, so a
    release-in-place never leaves the current branch.
    """
    branches = git("branch", "--format=%(refname:short)").stdout.split()
    current = git("branch", "--show-current").stdout.strip()

    source = cli_source or repo_cfg.source_branch
    target = cli_target or repo_cfg.target_branch or current

    chain: list[str] = []
    if current and current != target:
        chain.append(current)
    if source and source != target and source != current:
        chain.append(source)
    chain.append(target)

    missing = [b for b in chain if b not in branches]
    if missing:
        fail(
            f"Branch(es) don't exist: {', '.join(missing)}. Re-run with --reconfigure."
        )
        raise typer.Exit(1)

    state.branches = chain
    state.target_branch = chain[-1]
    state.source_branch = chain[-2] if len(chain) >= 2 else ""
    state.no_merge = len(chain) < 2

    if state.no_merge:
        info(f"Releasing {state.target_branch} (no merge)")
    else:
        info(" -> ".join(chain))


def merge_chain(state: State) -> list[str]:
    """The chain for this run, reconstructed from legacy state if `branches` is unset."""
    if state.branches:
        return state.branches
    if state.no_merge or not state.source_branch:
        return [state.target_branch]
    return [state.source_branch, state.target_branch]


# --------------------------------------------------------------------------- #
# Per-run repo capability detection                                            #
# --------------------------------------------------------------------------- #


def detect_project_type() -> str:
    if Path("go.mod").exists():
        return "go"
    if Path("pyproject.toml").exists():
        return "python"
    if Path("package.json").exists():
        return "node"
    return "generic"


def detect_publishable(project_type: str) -> bool:
    if project_type == "node" and Path("package.json").exists():
        raw = json.loads(Path("package.json").read_text())
        if isinstance(raw, dict):
            return bool(raw.get("name")) and raw.get("private") is not True
        return False
    if project_type == "python" and Path("pyproject.toml").exists():
        import tomlkit

        doc = tomlkit.parse(Path("pyproject.toml").read_text())
        return "build-system" in doc
    # Go modules "publish" by pushing a tag (done in the tag/push phases), so
    # there's no separate publish step here.
    return False


def detect_is_github(config: Config) -> bool:
    origin = git("remote", "get-url", "origin")
    if origin.returncode != 0 or "github.com" not in origin.stdout:
        return False
    if not config.enabled("gh"):
        return False
    auth = subprocess.run(["gh", "auth", "status"], capture_output=True, check=False)
    return auth.returncode == 0


def detect_hook_manager() -> str:
    if Path(".pre-commit-config.yaml").exists():
        return "pre-commit"
    if Path("lefthook.yml").exists() or Path("lefthook.yaml").exists():
        return "lefthook"
    hooks = git_dir() / "hooks"
    for name in ("pre-commit", "pre-push"):
        hook = hooks / name
        if hook.exists() and os.access(hook, os.X_OK):
            return "native"
    return "none"


# --------------------------------------------------------------------------- #
# Version helpers                                                              #
# --------------------------------------------------------------------------- #

SEMVER_RE = re.compile(r"^(\d+)\.(\d+)\.(\d+)(?:-dev\.(\d+))?$")


@dataclass
class Semver:
    major: int
    minor: int
    patch: int
    dev: int | None


def detect_version() -> str:
    if Path("pyproject.toml").exists():
        import tomlkit

        doc = tomlkit.parse(Path("pyproject.toml").read_text())
        project = doc.get("project")
        if isinstance(project, dict):
            ver = project.get("version")
            if isinstance(ver, str) and ver:
                return ver
    if Path("package.json").exists():
        raw = json.loads(Path("package.json").read_text())
        if isinstance(raw, dict):
            ver = raw.get("version")
            if isinstance(ver, str) and ver:
                return ver
    tags = git("tag", "--list", "v*", "--sort=-v:refname")
    if tags.returncode == 0 and tags.stdout.strip():
        first = tags.stdout.strip().splitlines()[0]
        return first.lstrip("v")
    return "0.0.0"


def parse_semver(version: str) -> Semver:
    match = SEMVER_RE.match(version)
    if match is None:
        fail(f"Could not parse version '{version}'. Expected X.Y.Z or X.Y.Z-dev.N.")
        raise typer.Exit(1)
    dev = match.group(4)
    return Semver(
        major=int(match.group(1)),
        minor=int(match.group(2)),
        patch=int(match.group(3)),
        dev=int(dev) if dev is not None else None,
    )


# --------------------------------------------------------------------------- #
# Phases                                                                       #
# --------------------------------------------------------------------------- #


def phase_hooks(state: State, config: Config) -> None:
    """P2 — run pre-commit & pre-push gates before any branch mutation."""
    manager = state.hook_manager
    if manager == "none":
        info("No hooks configured — skipping hook gates.")
        return

    def execute(label: str, cmd: list[str]) -> None:
        result = run(cmd)
        if result.returncode != 0:
            fail(f"{label} failed — aborting release.")
            if result.stdout:
                print(result.stdout)
            if result.stderr:
                print(result.stderr)
            raise typer.Exit(1)
        good(f"{label} passed")

    if manager == "pre-commit":
        if not config.enabled("pre-commit"):
            warn(
                "pre-commit hooks configured but tool not enabled (run setup) — skipping."
            )
            return
        execute("pre-commit (pre-commit stage)", ["pre-commit", "run", "--all-files"])
        execute(
            "pre-commit (pre-push stage)",
            ["pre-commit", "run", "--all-files", "--hook-stage", "pre-push"],
        )
    elif manager == "lefthook":
        if not config.enabled("lefthook"):
            warn("lefthook configured but tool not enabled (run setup) — skipping.")
            return
        execute("lefthook pre-commit", ["lefthook", "run", "pre-commit"])
        execute("lefthook pre-push", ["lefthook", "run", "pre-push"])
    elif manager == "native":
        hooks = git_dir() / "hooks"
        for name in ("pre-commit", "pre-push"):
            hook = hooks / name
            if hook.exists() and os.access(hook, os.X_OK):
                execute(f"native {name}", [str(hook)])


def phase_secret_scan(state: State, config: Config, no_scan: bool) -> None:
    """P3 — gitleaks secret scan."""
    if no_scan:
        info("Secret scan skipped (--no-scan).")
        return
    if not config.enabled("gitleaks"):
        info("gitleaks not enabled (run setup) — skipping secret scan.")
        return
    # Scan only commits not yet on remote (same logic as pre-push-gitleaks hook).
    remote_ref = git("rev-parse", f"origin/{state.target_branch}")
    if remote_ref.returncode == 0:
        log_opts = f"{remote_ref.stdout.strip()}..HEAD"
    else:
        log_opts = "HEAD"
    result = run(["gitleaks", "detect", f"--log-opts={log_opts}", "--redact", "--no-banner"])
    if result.returncode != 0:
        fail("gitleaks found potential secrets.")
        if result.stdout:
            print(result.stdout)
        if result.stderr:
            print(result.stderr)
        # Headless never auto-overrides a secret finding — it aborts.
        if HEADLESS:
            fail("Secrets detected — aborting (headless).")
            raise typer.Exit(1)
        if not gum_confirm("Secrets detected. Continue anyway?"):
            raise typer.Exit(1)
    else:
        good("Secret scan clean")


def _semantic_release_version() -> str:
    result = run(["semantic-release", "--dry", "--no-ci"])
    match = re.search(r"(\d+\.\d+\.\d+)", result.stdout)
    if match is None:
        fail("semantic-release did not produce a version — pick manually and re-run.")
        raise typer.Exit(1)
    return match.group(1)


def decide_version(state: State, config: Config, bump: str = "") -> None:
    """P4b — version detection + bump menu (parity + auto). `bump` skips the menu."""
    current = detect_version()
    sv = parse_semver(current)
    info(f"Current version: {current}")

    candidates = {
        "finalize": f"{sv.major}.{sv.minor}.{sv.patch}",
        "patch": f"{sv.major}.{sv.minor}.{sv.patch + 1}",
        "minor": f"{sv.major}.{sv.minor + 1}.0",
        "major": f"{sv.major + 1}.0.0",
    }

    if bump or HEADLESS:
        if not bump:
            _no_prompt("the version bump (use --bump)")
        if bump == "auto":
            state.version = _semantic_release_version()
        elif bump in candidates:
            state.version = candidates[bump]
        else:
            fail(f"--bump must be one of: {', '.join(candidates)}, auto")
            raise typer.Exit(1)
    else:
        options: list[str] = []
        if sv.dev is not None:
            options.append(f"finalize  ({candidates['finalize']})")
        options += [
            f"patch     ({candidates['patch']})",
            f"minor     ({candidates['minor']})",
            f"major     ({candidates['major']})",
        ]
        if config.enabled("semantic-release"):
            options.append("auto      (semantic-release)")
        choice = gum_choose("Version bump?", options).split()[0]
        state.version = (
            _semantic_release_version() if choice == "auto" else candidates[choice]
        )
    gum_style(f"Releasing v{state.version}", "--bold", "--foreground", "212")


def decide_version_dev(state: State, bump: str = "") -> None:
    """Dev bump menu (-dev.N, plus continue/custom). `bump` skips the menu."""
    current = detect_version()
    sv = parse_semver(current)
    info(f"Current version: {current}")

    candidates = {
        "patch": f"{sv.major}.{sv.minor}.{sv.patch + 1}-dev.1",
        "minor": f"{sv.major}.{sv.minor + 1}.0-dev.1",
        "major": f"{sv.major + 1}.0.0-dev.1",
    }
    if sv.dev is not None:
        candidates["continue"] = f"{sv.major}.{sv.minor}.{sv.patch}-dev.{sv.dev + 1}"

    if bump or HEADLESS:
        if not bump:
            _no_prompt("the version bump (use --bump)")
        if bump in candidates:
            state.version = candidates[bump]
        else:
            fail(f"--bump must be one of: {', '.join(candidates)}")
            raise typer.Exit(1)
    else:
        order = (["continue"] if "continue" in candidates else []) + [
            "patch",
            "minor",
            "major",
        ]
        options = [f"{k:<9} ({candidates[k]})" for k in order] + ["custom"]
        choice = gum_choose("Version bump?", options).split()[0]
        if choice == "custom":
            ver = gum_input("Custom version", placeholder="X.Y.Z-dev.N")
            parse_semver(ver)  # validates format or exits
            state.version = ver
        else:
            state.version = candidates[choice]
    gum_style(f"Releasing v{state.version}", "--bold", "--foreground", "212")


def phase_merge(state: State) -> None:
    """P5 — merge each leg of the chain up into the target.

    For `feature -> develop -> master` this merges feature into develop, then
    develop into master, ending checked out on the target. Already-merged legs are
    no-ops (git reports "Already up to date"), so a --resume re-runs safely.
    """
    chain = merge_chain(state)
    if len(chain) < 2:
        return
    if (git_dir() / "MERGE_HEAD").exists():
        info(
            "Merge already in progress — leaving it for you to resolve, then --resume."
        )
        return
    for i in range(1, len(chain)):
        frm, into = chain[i - 1], chain[i]
        is_last = i == len(chain) - 1
        run(["git", "checkout", into], title=f"Switching to {into}...")
        run(["git", "pull", "origin", into], title=f"Pulling {into}...")
        message = (
            f"Release version {state.version}"
            if is_last
            else f"Merge {frm} into {into} for release {state.version}"
        )
        merge = run(
            ["git", "merge", frm, "--no-ff", "-m", message],
            title=f"Merging {frm} into {into}...",
        )
        if merge.returncode != 0:
            fail(
                "Merge failed (conflicts?). Resolve, commit, then re-run with --resume."
            )
            raise typer.Exit(1)


def find_config_version_files() -> list[Path]:
    skip = {".git", "venv", ".venv", "node_modules", "target", "__pycache__"}
    found: list[Path] = []
    for name in ("config.py", "settings.py"):
        for path in Path(".").rglob(name):
            if any(part in skip or part.startswith(".") for part in path.parts[:-1]):
                continue
            if len(path.parts) > 4:
                continue
            found.append(path)
    return found


def phase_write_version(state: State) -> None:
    """P6 — write version into project files + commit."""
    version = state.version
    updated: list[str] = []

    if Path("package.json").exists():
        text = Path("package.json").read_text()
        new = re.sub(r'"version":\s*"[^"]*"', f'"version": "{version}"', text, count=1)
        if not DRY_RUN:
            Path("package.json").write_text(new)
        updated.append("package.json")

    if Path("pyproject.toml").exists():
        import tomlkit

        doc = tomlkit.parse(Path("pyproject.toml").read_text())
        project = doc.get("project")
        if isinstance(project, dict) and "version" in project:
            project["version"] = version
            if not DRY_RUN:
                Path("pyproject.toml").write_text(tomlkit.dumps(doc))
            updated.append("pyproject.toml")

    version_re = re.compile(r'^(\s*)VERSION: str = "[^"]*"', re.MULTILINE)
    for path in find_config_version_files():
        text = path.read_text()
        if version_re.search(text):
            new = version_re.sub(rf'\1VERSION: str = "{version}"', text)
            if not DRY_RUN:
                path.write_text(new)
            updated.append(str(path))

    if not updated:
        info("No version files found to update.")
        return

    info(f"Updated: {', '.join(updated)}")
    if DRY_RUN:
        return
    subject = git("log", "-1", "--pretty=%s").stdout.strip()
    if subject == f"chore: bump version to {version}":
        info("Version bump already committed — skipping.")
        return
    git("add", *updated)
    git("commit", "-m", f"chore: bump version to {version}")


def phase_changelog(
    state: State, config: Config, no_changelog: bool, amend: bool = False
) -> None:
    """P7 — generate/prepend a CHANGELOG.md section."""
    if no_changelog:
        info("Changelog skipped (--no-changelog).")
        return

    previous = git("describe", "--tags", "--abbrev=0").stdout.strip()
    section = ""
    if config.enabled("git-cliff"):
        result = run(
            [
                "git-cliff",
                "--unreleased",
                "--tag",
                f"v{state.version}",
                "--strip",
                "all",
            ]
        )
        section = result.stdout.strip()
    if not section:
        rng = f"{previous}..HEAD" if previous else "HEAD"
        log = git("log", rng, "--pretty=%s")
        lines = [line for line in log.stdout.splitlines() if line.strip()]
        section = render_changelog(state.version, lines)

    state.changelog = section
    if DRY_RUN:
        info("[dry-run] would write CHANGELOG.md section:")
        print(section)
        return

    changelog = Path("CHANGELOG.md")
    existing = changelog.read_text() if changelog.exists() else ""
    changelog.write_text(section + "\n\n" + existing if existing else section + "\n")

    # Never open an editor headless (it would block on a TTY).
    if not HEADLESS and gum_confirm("Edit CHANGELOG.md before committing?"):
        editor = os.environ.get("EDITOR", "nvim")
        subprocess.run([editor, str(changelog)], check=False)
        state.changelog = changelog.read_text().split("\n\n")[0]

    git("add", "CHANGELOG.md")
    bump_subject = f"chore: bump version to {state.version}"
    head_subject = git("log", "-1", "--pretty=%s").stdout.strip()
    if amend and head_subject == bump_subject:
        # Fold the changelog into the version-bump commit instead of a new one.
        git("commit", "--amend", "--no-edit")
        info("Amended changelog into the version-bump commit.")
    else:
        git("commit", "-m", f"docs: changelog for v{state.version}")


def render_changelog(version: str, subjects: list[str]) -> str:
    groups: dict[str, list[str]] = {}
    headings = {
        "feat": "Features",
        "fix": "Bug Fixes",
        "docs": "Documentation",
        "perf": "Performance",
        "refactor": "Refactor",
        "chore": "Chores",
    }
    for subject in subjects:
        match = re.match(r"^(\w+)(?:\([^)]*\))?!?:\s*(.*)$", subject)
        key = match.group(1) if match else "other"
        text = match.group(2) if match else subject
        groups.setdefault(headings.get(key, "Other"), []).append(text)
    out = [f"## v{version}", ""]
    for heading in [*headings.values(), "Other"]:
        if heading in groups:
            out.append(f"### {heading}")
            out += [f"- {item}" for item in groups[heading]]
            out.append("")
    return "\n".join(out).strip()


def phase_tag(state: State) -> None:
    """P8 — annotated tag, idempotent."""
    tag = f"v{state.version}"
    existing = git("tag", "--list", tag).stdout.strip()
    if existing:
        at = git("rev-list", "-n", "1", tag).stdout.strip()
        head = git("rev-parse", "HEAD").stdout.strip()
        if at == head:
            info(f"Tag {tag} already at HEAD — continuing.")
            return
        fail(f"Tag {tag} exists but not at HEAD. Resolve manually.")
        raise typer.Exit(1)
    default_message = f"Dev release {tag}" if state.dev else f"Release {tag}"
    message = state.changelog or default_message
    if DRY_RUN:
        info(f"[dry-run] git tag -a {tag}")
        return
    git("tag", "-a", tag, "-m", message)
    good(f"Tagged {tag} on {state.target_branch}")


def phase_push(state: State) -> None:
    """P9 — push target branch + tags."""
    target = state.target_branch
    if not gum_confirm(f"Push {target} and tags to origin?"):
        info("Skipped push. Don't forget to push later.")
        return
    result = run(
        ["git", "push", "origin", target, "--follow-tags"], title=f"Pushing {target}..."
    )
    if result.returncode != 0:
        fail("Push failed. Resolve the issue and push manually:")
        info(f"  git push origin {target} --follow-tags")
        raise typer.Exit(1)
    good(f"Pushed {target}")


def project_name() -> str:
    """The package name from package.json / pyproject.toml, or empty."""
    if Path("package.json").exists():
        raw = json.loads(Path("package.json").read_text())
        if isinstance(raw, dict) and isinstance(raw.get("name"), str):
            return raw["name"]
    if Path("pyproject.toml").exists():
        import tomlkit

        doc = tomlkit.parse(Path("pyproject.toml").read_text())
        project = doc.get("project")
        if isinstance(project, dict) and isinstance(project.get("name"), str):
            return str(project["name"])
    return ""


def already_published(project_type: str, version: str) -> bool:
    """Best-effort registry check so re-running publish is idempotent.

    Returns True only when the version is confirmed present on the registry; on
    any uncertainty (no name, tool/network error) returns False so we don't block
    a legitimate publish.
    """
    name = project_name()
    if not name:
        return False
    if project_type == "node":
        result = subprocess.run(
            ["npm", "view", f"{name}@{version}", "version"],
            capture_output=True,
            text=True,
            check=False,
        )
        return result.returncode == 0 and result.stdout.strip() != ""
    if project_type == "python":
        import urllib.error
        import urllib.request

        url = f"https://pypi.org/pypi/{name}/{version}/json"
        try:
            with urllib.request.urlopen(url, timeout=5) as resp:
                return resp.status == 200
        except urllib.error.HTTPError:
            return False
        except OSError:
            return False
    return False


def phase_publish(state: State) -> None:
    """P10 — optional package publish."""
    if not state.publishable:
        info("Not publishable — skipping publish.")
        return
    if already_published(state.project_type, state.version):
        info(f"v{state.version} already on the registry — skipping publish.")
        return
    name = project_name()
    if state.project_type == "node":
        if gum_confirm("npm publish?"):
            result = run(["npm", "publish"], title="npm publish...")
            if result.returncode == 0:
                state.published = f"npm: {name}@{state.version}"
    elif state.project_type == "python":
        if gum_confirm("uv build && uv publish?"):
            run(["uv", "build"], title="uv build...")
            result = run(["uv", "publish"], title="uv publish...")
            if result.returncode == 0:
                state.published = f"pypi: {name} {state.version}"


def phase_github_release(state: State) -> None:
    """P11 — optional GitHub release."""
    if not state.is_github:
        info("Not a GitHub repo (or gh not authenticated) — skipping GitHub release.")
        return
    tag = f"v{state.version}"
    existing = subprocess.run(
        ["gh", "release", "view", tag], capture_output=True, text=True, check=False
    )
    if existing.returncode == 0:
        info(f"GitHub release {tag} already exists — skipping.")
        url = existing.stdout.strip().splitlines()
        state.github_release_url = next(
            (line.split("\t", 1)[1] for line in url if line.startswith("url\t")), ""
        )
        return
    if not gum_confirm(f"Create GitHub release {tag}?"):
        return
    args = ["gh", "release", "create", tag, "--title", tag]
    if state.changelog:
        args += ["--notes", state.changelog]
    else:
        args += ["--notes-from-tag"]
    # No spinner here: `gh release create` prints the release URL to stdout and we
    # want to capture it for the summary.
    info(f"Creating GitHub release {tag}...")
    result = run(args)
    if result.returncode != 0:
        fail("GitHub release failed — create it manually if needed.")
        return
    state.github_release_url = result.stdout.strip()
    good(f"Created GitHub release {tag}")


def phase_rebase_source(state: State) -> None:
    """P12 — sync the source branch back onto the target after the release.

    Rebases the immediate source (the integration branch, e.g. `develop`) onto the
    released target and pushes it, so it ends aligned with `master`. Any feature
    branch you started the chain from is left as-is — rebase it yourself if needed.
    """
    if state.no_merge:
        return
    source = state.source_branch
    target = state.target_branch
    if not gum_confirm(f"Switch back to {source} and rebase with {target}?"):
        info(f"Stayed on {target}. Rebase manually when ready.")
        return
    run(["git", "checkout", source], title=f"Switching to {source}...")
    rebase = run(["git", "rebase", target], title=f"Rebasing {source} with {target}...")
    if rebase.returncode != 0:
        fail("Rebase failed (conflicts?). Resolve, then re-run with --resume.")
        raise typer.Exit(1)
    if gum_confirm(f"Push {source} to origin?"):
        result = run(["git", "push", "origin", source], title=f"Pushing {source}...")
        if result.returncode != 0:
            fail("Push failed. Push manually:")
            info(f"  git push origin {source}")
            raise typer.Exit(1)
        good(f"Pushed {source}")


# --------------------------------------------------------------------------- #
# Orchestration                                                                #
# --------------------------------------------------------------------------- #


def run_phase(name: str, state: State, fn: Callable[[], None]) -> None:
    """Wrap a phase: skip if done, run, persist, recover on failure."""
    if state.status(name) == "done":
        return
    state.phases[name] = "started"
    save_state(state)
    try:
        fn()
    except typer.Exit as exc:
        if exc.exit_code != 0:
            state.phases[name] = "failed"
            save_state(state)
            fail(f"Phase '{name}' failed. Fix the issue, then run:")
            info("  zen-release --resume")
        raise
    except Exception:
        state.phases[name] = "failed"
        save_state(state)
        fail(f"Phase '{name}' crashed. Fix the issue, then run:")
        info("  zen-release --resume")
        raise
    state.phases[name] = "done"
    save_state(state)


def run_optional(
    name: str, repo_cfg: RepoConfig, state: State, fn: Callable[[], None]
) -> None:
    """Run an optional phase only if the per-repo config enables it."""
    if not repo_cfg.enabled(name):
        info(f"{name} disabled for this repo — skipping.")
        return
    run_phase(name, state, fn)


# --------------------------------------------------------------------------- #
# CLI                                                                          #
# --------------------------------------------------------------------------- #

app = typer.Typer(add_completion=False, help=__doc__)


@app.command()
def setup() -> None:
    """Detect external tools once and save the zen-release config."""
    if shutil.which("gum") is None:
        print("Error: gum is required (it drives the UI). Install it first:")
        print(f"  {TOOL_BY_NAME['gum'].install_hint}")
        raise typer.Exit(1)

    banner("zen-release setup")
    tools: dict[str, ToolStatus] = {}
    missing_optional: list[ToolSpec] = []
    for spec in TOOL_SPECS:
        status = probe_tool(spec)
        tools[spec.name] = status
        tag = " (required)" if spec.required else ""
        if status.enabled:
            good(f"  ✓ {spec.name:<18} {status.version}")
        elif spec.required:
            fail(f"  ✗ {spec.name:<18} not found{tag}")
        else:
            warn(f"  ✗ {spec.name:<18} not found")
            missing_optional.append(spec)

    # Required tools must all be present — otherwise refuse to save a config.
    required_gaps = missing_required(tools)
    if required_gaps:
        fail("\nSetup failed — required tools are missing. No config was saved:")
        for spec in required_gaps:
            info(f"  {spec.name}: {spec.install_hint}")
        info("\nInstall them and re-run `setup`.")
        raise typer.Exit(1)

    config = Config(schema=CONFIG_SCHEMA, created_at=time.time(), tools=tools)
    save_config(config)
    good(f"\nSaved config to {config_path()}")

    if missing_optional:
        info("\nOptional tools missing (their phases will be skipped). To enable:")
        for spec in missing_optional:
            info(f"  {spec.name}: {spec.install_hint}")
        info("\nRe-run `setup` after installing to heal the config.")


def preflight(resume: bool, restart: bool, title: str) -> Config:
    """Hard-require checks (gum, git repo, config, clean tree) + banner. Stateless."""
    if shutil.which("gum") is None:
        print("Error: gum is required. Run: zen-release setup")
        raise typer.Exit(1)
    if git("rev-parse", "--git-dir").returncode != 0:
        fail("Not a git repository.")
        raise typer.Exit(1)

    config = load_config()
    if config is None:
        fail("No zen-release config found. Run `zen-release setup` first.")
        raise typer.Exit(1)

    if git("status", "--porcelain").stdout.strip() and not (resume or restart):
        fail("Working directory is not clean. Commit or stash changes first.")
        raise typer.Exit(1)

    banner(title)
    return config


def start_session(resume: bool, restart: bool, title: str) -> tuple[Config, State]:
    """Preflight + the full-release state machine (resume / restart / fresh)."""
    config = preflight(resume, restart, title)

    # State: resume / restart / fresh
    existing = load_state()
    if existing is not None and not restart:
        if not resume:
            if HEADLESS:
                fail("A previous release is in progress. Pass --resume or --restart.")
                raise typer.Exit(1)
            choice = gum_choose(
                "A previous release is in progress.", ["Resume", "Restart", "Abort"]
            )
            if choice == "Abort":
                raise typer.Abort()
            if choice == "Restart":
                existing = None
                clear_state()
        state = existing if existing is not None else State()
    else:
        if restart:
            clear_state()
        state = State()
    return config, state


def do_release(
    resume: bool,
    restart: bool,
    dry_run: bool,
    no_scan: bool,
    no_changelog: bool,
    reconfigure: bool = False,
    amend_changelog: bool = False,
    yes: bool = False,
    bump: str = "",
    source: str = "",
    target: str = "",
) -> None:
    """Run an interactive, resumable release."""
    global DRY_RUN, HEADLESS
    DRY_RUN = dry_run
    HEADLESS = yes

    config, state = start_session(resume, restart, "Release")

    # Per-run capability detection (fresh each run)
    state.project_type = detect_project_type()
    state.publishable = detect_publishable(state.project_type)
    state.is_github = detect_is_github(config)
    state.hook_manager = detect_hook_manager()

    # Per-repo config: created interactively on first run (or with --reconfigure).
    repo_cfg = None if reconfigure else load_repo_config()
    if repo_cfg is None:
        repo_cfg = (
            headless_repo_config(state, config, source, target)
            if HEADLESS
            else create_repo_config(state, config)
        )

    # Pre-branch phases (optional, gated by the repo config)
    run_optional("hooks", repo_cfg, state, lambda: phase_hooks(state, config))
    run_optional(
        "secret_scan",
        repo_cfg,
        state,
        lambda: phase_secret_scan(state, config, no_scan),
    )

    # Branch + version decision (only if not already chosen on a prior run)
    if not state.version:
        apply_branches_from_repo(state, repo_cfg, source, target)
        decide_version(state, config, bump)
        save_state(state)

    run_phase("merge", state, lambda: phase_merge(state))
    run_phase("write_version", state, lambda: phase_write_version(state))
    run_optional(
        "changelog",
        repo_cfg,
        state,
        lambda: phase_changelog(state, config, no_changelog, amend_changelog),
    )
    run_phase("tag", state, lambda: phase_tag(state))
    run_phase("push", state, lambda: phase_push(state))
    run_optional("publish", repo_cfg, state, lambda: phase_publish(state))
    run_optional("github_release", repo_cfg, state, lambda: phase_github_release(state))
    run_phase("rebase_source", state, lambda: phase_rebase_source(state))

    # Finalize
    summary = [f"Release v{state.version} complete"]
    if state.published:
        summary.append(f"published: {state.published}")
    if state.github_release_url:
        summary.append(f"release: {state.github_release_url}")
    skipped = [name for name, status in state.phases.items() if status != "done"]
    if skipped:
        summary.append(f"(skipped/incomplete: {', '.join(skipped)})")
    banner("\n".join(summary), color="82")
    if not DRY_RUN:
        clear_state()


def do_dev_release(dry_run: bool, yes: bool = False, bump: str = "") -> None:
    """Lightweight dev release: tag a -dev.N version on the current branch.

    No merge, changelog, publish, GitHub release, or hook/scan gates — just bump
    the version files, tag, and push the current branch (parity with the old
    release-dev.sh). **Stateless**: no resume/state file. Dev runs are short and
    every step is idempotent (version-commit skips if already made, tag skips if
    at HEAD, push is a no-op), so re-running from scratch is always safe — and we
    avoid colliding with a full release's resume state.
    """
    global DRY_RUN, HEADLESS
    DRY_RUN = dry_run
    HEADLESS = yes

    preflight(resume=False, restart=False, title="Dev Release")

    current = git("branch", "--show-current").stdout.strip()
    state = State(dev=True, no_merge=True, target_branch=current)
    info(f"Branch: {current}")

    decide_version_dev(state, bump)
    phase_write_version(state)
    phase_tag(state)
    phase_push(state)

    banner(f"Dev release v{state.version} complete", color="82")


@app.callback(invoke_without_command=True)
def main(
    ctx: typer.Context,
    resume: bool = typer.Option(False, "--resume", help="Resume a previous run."),
    restart: bool = typer.Option(
        False, "--restart", help="Discard prior state and restart."
    ),
    dry_run: bool = typer.Option(
        False, "--dry-run", help="Echo mutating commands only."
    ),
    no_scan: bool = typer.Option(False, "--no-scan", help="Skip the secret scan."),
    no_changelog: bool = typer.Option(
        False, "--no-changelog", help="Skip changelog generation."
    ),
    reconfigure: bool = typer.Option(
        False,
        "--reconfigure",
        help="Re-run this repo's first-run config (branches/phases).",
    ),
    dev: bool = typer.Option(
        False,
        "--dev",
        help="Dev release: tag a -dev.N version on the current branch (no merge).",
    ),
    amend_changelog: bool = typer.Option(
        False,
        "--amend-changelog",
        help="Fold the changelog into the version-bump commit instead of a new one.",
    ),
    yes: bool = typer.Option(
        False, "--yes", "-y", help="Headless: no prompts, auto-confirm (needs --bump)."
    ),
    bump: str = typer.Option(
        "",
        "--bump",
        help="Version bump for headless runs: patch|minor|major|finalize|auto.",
    ),
    source: str = typer.Option(
        "",
        "--source",
        help="Override the source branch for this run (empty = the branch you're on).",
    ),
    target: str = typer.Option(
        "",
        "--target",
        help="Override the target branch for this run (overrides saved config).",
    ),
) -> None:
    """Run a release by default; `setup` detects tools (run once)."""
    if ctx.invoked_subcommand is not None:
        return
    if dev:
        do_dev_release(dry_run, yes, bump)
        return
    do_release(
        resume,
        restart,
        dry_run,
        no_scan,
        no_changelog,
        reconfigure,
        amend_changelog,
        yes,
        bump,
        source,
        target,
    )


if __name__ == "__main__":
    app()
