#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "typer>=0.12",
# ]
# ///
#
# ==============================================================================
#
#      _____      _____  ___ _ __       z e n - s w e e p
#     / __\ \ /\ / / _ \/ _ \ '_ \
#     \__ \\ V  V /  __/  __/ |_) |
#     |___/ \_/\_/ \___|\___| .__/
#                           |_|
#
# ------------------------------------------------------------------------------
#
#  NAME
#      sweep.py  --  read-only report of loose ends across every repo
#
#  SYNOPSIS
#      zen-sweep [--json]
#
#  DESCRIPTION
#      Repos: the vault config `repos` (config/zen-flow.json: `release_routine`
#      plus git repos up to two levels under `root`) and ~/code/personal/*.
#      For each repo and each `git worktree list --porcelain` entry it reports:
#        dirty           uncommitted changes
#        unpushed        commits ahead of upstream
#        no-upstream     a local branch with no upstream
#        stash           stash entries
#        orphan-branch   am/* branch with no worktree
#        orphan-worktree linked worktree with no live agent-manager session
#        merged          branch already merged into its base (dev, else main/master)
#      It never changes anything.
#
#  EXIT CODES
#      0  clean      1  findings
#
#  AUTHOR
#      vzsoares.
#
# ==============================================================================
"""Report dirty trees, unpushed work, stashes and stale worktrees. Read-only."""

from __future__ import annotations

import json
import subprocess
import sys
from dataclasses import asdict, dataclass, field
from pathlib import Path

import typer

LONG_LIVED = ("dev", "main", "master", "prod")


@dataclass
class Finding:
    repo: str
    kind: str
    detail: str

    def __str__(self) -> str:
        return f"{self.repo}: {self.kind} {self.detail}"


@dataclass
class Worktree:
    path: str
    branch: str = ""
    main: bool = False


@dataclass
class Sweep:
    findings: list[Finding] = field(default_factory=list)
    notes: list[str] = field(default_factory=list)


# --------------------------------------------------------------------------- #
# Repo discovery                                                               #
# --------------------------------------------------------------------------- #


def git(cwd: str | Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True, text=True, check=False
    )


def is_repo(path: Path) -> bool:
    return (path / ".git").exists()


def vault_config(home: Path) -> dict[str, object]:
    brain = home / ".claude" / "second-brain.json"
    if not brain.exists():
        return {}
    vault = json.loads(brain.read_text()).get("vault")
    if not isinstance(vault, str):
        return {}
    path = Path(vault).expanduser() / "config" / "zen-flow.json"
    if not path.exists():
        return {}
    data = json.loads(path.read_text())
    return data if isinstance(data, dict) else {}


def _under(root: Path, depth: int) -> list[Path]:
    if not root.is_dir():
        return []
    found: list[Path] = []
    for child in sorted(root.iterdir()):
        if not child.is_dir() or child.name.startswith("."):
            continue
        if is_repo(child):
            found.append(child)
        elif depth > 1:
            found.extend(_under(child, depth - 1))
    return found


def collect_repos(config: dict[str, object], personal: Path) -> list[Path]:
    candidates: list[Path] = []
    repos = config.get("repos")
    if isinstance(repos, dict):
        routine = repos.get("release_routine")
        if isinstance(routine, list):
            candidates += [Path(str(p)).expanduser() for p in routine]
        root = repos.get("root")
        if isinstance(root, str):
            candidates += _under(Path(root).expanduser(), depth=2)
    candidates += _under(personal, depth=1)

    seen: set[Path] = set()
    out: list[Path] = []
    for path in candidates:
        if not is_repo(path):
            continue
        common = git(path, "rev-parse", "--path-format=absolute", "--git-common-dir").stdout.strip()
        key = Path(common).resolve() if common else path.resolve()
        if key in seen:
            continue
        seen.add(key)
        out.append(path)
    return out


# --------------------------------------------------------------------------- #
# Checks                                                                       #
# --------------------------------------------------------------------------- #


def worktrees(repo: Path) -> list[Worktree]:
    out: list[Worktree] = []
    for line in git(repo, "worktree", "list", "--porcelain").stdout.splitlines():
        if line.startswith("worktree "):
            out.append(Worktree(path=line[len("worktree ") :], main=not out))
        elif line.startswith("branch ") and out:
            out[-1].branch = line[len("branch ") :].removeprefix("refs/heads/")
    return out


def base_branch(repo: Path) -> str:
    for name in ("dev", "main", "master"):
        if git(repo, "rev-parse", "--verify", "--quiet", f"refs/heads/{name}").returncode == 0:
            return name
    return ""


def sweep_repo(repo: Path, live_dirs: set[str] | None) -> list[Finding]:
    """Findings for one repo. live_dirs=None skips the session check."""
    name = str(repo)
    found: list[Finding] = []
    trees = worktrees(repo)
    has_remote = bool(git(repo, "remote").stdout.strip())

    for wt in trees:
        if not Path(wt.path).exists():
            found.append(Finding(name, "orphan-worktree", f"{wt.path} (missing on disk)"))
            continue
        status = git(wt.path, "status", "--porcelain").stdout.splitlines()
        if status:
            found.append(Finding(name, "dirty", f"{wt.path} ({len(status)} changed)"))
        if not wt.main and live_dirs is not None:
            if str(Path(wt.path).resolve()) not in live_dirs:
                found.append(Finding(name, "orphan-worktree", wt.path))

    refs = git(
        repo,
        "for-each-ref",
        "refs/heads",
        "--format=%(refname:short)\t%(upstream:short)\t%(upstream:track)",
    ).stdout.splitlines()
    checked_out = {wt.branch for wt in trees if wt.branch}
    for row in refs:
        branch, upstream, track = (row.split("\t") + ["", ""])[:3]
        if has_remote and not upstream:
            found.append(Finding(name, "no-upstream", branch))
        elif "ahead" in track:
            found.append(Finding(name, "unpushed", f"{branch} {track}"))
        if branch.startswith("am/") and branch not in checked_out:
            found.append(Finding(name, "orphan-branch", branch))

    base = base_branch(repo)
    if base:
        merged = git(repo, "branch", "--merged", base, "--format=%(refname:short)").stdout.split()
        for branch in merged:
            if branch != base and branch not in LONG_LIVED:
                found.append(Finding(name, "merged", f"{branch} (into {base})"))

    stashes = git(repo, "stash", "list").stdout.splitlines()
    if stashes:
        found.append(Finding(name, "stash", f"{len(stashes)} entr{'y' if len(stashes) == 1 else 'ies'}"))
    return found


def live_session_dirs() -> set[str] | None:
    try:
        result = subprocess.run(
            ["agent-manager", "sessions", "--json"], capture_output=True, text=True, check=False
        )
    except FileNotFoundError:
        return None
    if result.returncode != 0:
        return None
    data = json.loads(result.stdout or "[]")
    if not isinstance(data, list):
        return None
    return {
        str(Path(s["directory"]).resolve())
        for s in data
        if isinstance(s, dict)
        and isinstance(s.get("directory"), str)
        and s.get("running")
        and not s.get("archived")
        and s.get("status") not in ("dead", "errored")
    }


def run_sweep(repos: list[Path], live_dirs: set[str] | None) -> Sweep:
    result = Sweep()
    if live_dirs is None:
        result.notes.append("agent-manager unavailable: orphan-worktree check skipped")
    for repo in repos:
        result.findings += sweep_repo(repo, live_dirs)
    return result


# --------------------------------------------------------------------------- #
# CLI                                                                          #
# --------------------------------------------------------------------------- #

app = typer.Typer(add_completion=False, help=__doc__)


@app.command()
def main(as_json: bool = typer.Option(False, "--json", help="JSON output.")) -> None:
    """Report loose ends across every repo; exit 1 on findings. Never changes anything."""
    home = Path.home()
    repos = collect_repos(vault_config(home), home / "code" / "personal")
    result = run_sweep(repos, live_session_dirs())
    if as_json:
        print(
            json.dumps(
                {
                    "clean": not result.findings,
                    "repos": [str(r) for r in repos],
                    "findings": [asdict(f) for f in result.findings],
                    "notes": result.notes,
                }
            )
        )
    else:
        for note in result.notes:
            print(f"note: {note}", file=sys.stderr)
        for finding in result.findings:
            print(finding)
    raise typer.Exit(1 if result.findings else 0)


if __name__ == "__main__":
    app()
