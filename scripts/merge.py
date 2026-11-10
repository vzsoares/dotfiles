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
#      _ __ ___   ___ _ __ __ _  ___      z e n - m e r g e
#     | '_ ` _ \ / _ \ '__/ _` |/ _ \
#     | | | | | |  __/ | | (_| |  __/
#     |_| |_| |_|\___|_|  \__, |\___|
#                         |___/
#
# ------------------------------------------------------------------------------
#
#  NAME
#      merge.py  --  land a finished worker branch and clean up after it
#
#  SYNOPSIS
#      zen-merge <session> [--base dev] [--phase N/M] [--task ID] [--dry-run]
#                          [--restart] [--json]
#
#  OPTIONS
#      --base BRANCH  Branch to land on. Default dev; a repo without `dev`
#                     lands on its main checkout's branch when that is main
#                     or master (no release, "push pending"), else exit 6.
#                     Only base dev is released.
#      --phase N/M    The merge is phase N of M of a phased task. A non-final
#                     phase appends a note and leaves the task open; only
#                     N == M (or no flag) marks it Done.
#      --task ID      Board task ID. Wins over the Task: trailer (stderr
#                     warning on a mismatch).
#      --dry-run      Run the checks, echo the mutating commands, save nothing.
#      --restart      Discard the saved state for this session and start over.
#      --json         Print the outcome as JSON.
#
#  PHASES (resumable; state in ~/.local/state/zen-merge/<session>.json)
#      resolve   worktree + branch from `agent-manager sessions --json`
#      clean     clean trees in the worker worktree and the main checkout
#      gate      zen-gate on the worker branch
#      merge     `git merge --ff-only <branch>` in the main checkout
#      release   `zen-release --dev --yes --bump patch`, capturing the tag
#      archive   `agent-manager archive <session>`
#      worktree  `git worktree remove <path>`
#      branch    `git branch -d`, or -D once merge-base --is-ancestor agrees
#      board     `zen-board done <task> --note "<tag> · <shas>"`, or `zen-board
#                note <task> "phase N/M · <tag> · <shas>"` for a non-final
#                phase; skipped without a task ID. The ID and shas come from
#                base..branch, else the base reflog's `merge <branch>:` entry
#                (a branch merged by hand), else the saved shas still on the
#                branch, else the branch tip.
#
#      Never merges into, or releases from, prod, or the zen-release target
#      branch of a repo with `dev`.
#
#  EXIT CODES
#      0 landed   2 gate hit   3 not fast-forward   4 dirty tree / wrong branch
#      5 release failed   6 refused (protected base, or no dev and the main
#      checkout off main/master)   7 board step failed
#      1 anything else (rerun resumes)
#
#  AUTHOR
#      vzsoares.
#
# ==============================================================================
"""Merge a worker session's branch, cut the dev release, then clean up."""

from __future__ import annotations

import importlib.util
import json
import os
import re
import shlex
import subprocess
import sys
from collections.abc import Callable
from dataclasses import asdict, dataclass, field
from pathlib import Path
from types import ModuleType

import typer

EXIT_ERROR = 1
EXIT_GATE = 2
EXIT_NOT_FF = 3
EXIT_DIRTY = 4
EXIT_RELEASE = 5
EXIT_REFUSED = 6
EXIT_BOARD = 7

PROTECTED = ("prod",)
SINGLE_BRANCH = ("main", "master")
PUSH_PENDING = "push pending"
BOARD = Path(__file__).resolve().parent / "board.py"

DRY_RUN = False


class Stop(Exception):
    def __init__(self, code: int, message: str) -> None:
        super().__init__(message)
        self.code = code


@dataclass
class State:
    session: str
    session_id: str = ""
    worktree: str = ""
    branch: str = ""
    main: str = ""
    base: str = ""
    release: bool = False
    task_id: str = ""
    tag: str = ""
    part: str = ""
    shas: list[str] = field(default_factory=list)
    board: str = ""
    phases: dict[str, str] = field(default_factory=dict)

    def done(self, phase: str) -> bool:
        return self.phases.get(phase) == "done"


# --------------------------------------------------------------------------- #
# Shell helpers                                                                #
# --------------------------------------------------------------------------- #


def git(cwd: str | Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True, text=True, check=False
    )


def mutate(cwd: str | Path, cmd: list[str]) -> subprocess.CompletedProcess[str]:
    """Run a mutating command, or echo it under --dry-run."""
    if DRY_RUN:
        print(f"[dry-run] (cd {cwd} && {shlex.join(cmd)})", file=sys.stderr)
        return subprocess.CompletedProcess(cmd, 0, "", "")
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, check=False)


def run_release(main: str) -> subprocess.CompletedProcess[str]:
    return mutate(main, ["zen-release", "--dev", "--yes", "--bump", "patch"])


def run_board(main: str, task_id: str, note: str) -> subprocess.CompletedProcess[str]:
    return mutate(main, [str(BOARD), "done", task_id, "--note", note])


def run_board_note(
    main: str, task_id: str, note: str
) -> subprocess.CompletedProcess[str]:
    return mutate(main, [str(BOARD), "note", task_id, note])


def load_gate() -> ModuleType:
    path = Path(__file__).resolve().parent / "gate.py"
    spec = importlib.util.spec_from_file_location("zen_gate", path)
    if spec is None or spec.loader is None:
        raise Stop(EXIT_ERROR, f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    # @dataclass resolves its module through sys.modules.
    sys.modules["zen_gate"] = module
    spec.loader.exec_module(module)
    return module


def load_sessions() -> ModuleType:
    path = Path(__file__).resolve().parent / "lib" / "sessions.py"
    spec = importlib.util.spec_from_file_location("zen_sessions", path)
    if spec is None or spec.loader is None:
        raise Stop(EXIT_ERROR, f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["zen_sessions"] = module
    spec.loader.exec_module(module)
    return module


_sessions = load_sessions()


def branch_exists(repo: str, name: str) -> bool:
    return (
        git(repo, "rev-parse", "--verify", "--quiet", f"refs/heads/{name}").returncode
        == 0
    )


def current_branch(repo: str) -> str:
    return git(repo, "branch", "--show-current").stdout.strip()


def main_checkout(worktree: str) -> str:
    out = git(worktree, "worktree", "list", "--porcelain").stdout
    for line in out.splitlines():
        if line.startswith("worktree "):
            return line[len("worktree ") :]
    raise Stop(EXIT_ERROR, f"{worktree} is not a git worktree")


def release_target(main: str) -> str:
    common = git(
        main, "rev-parse", "--path-format=absolute", "--git-common-dir"
    ).stdout.strip()
    path = Path(common) / "zen-release.json"
    if not common or not path.exists():
        return ""
    raw = json.loads(path.read_text())
    branches = raw.get("branches") if isinstance(raw, dict) else None
    target = branches.get("target", "") if isinstance(branches, dict) else ""
    return str(target)


def protected_branches(main: str) -> set[str]:
    """prod, plus the zen-release target in a dev+prod repo; a single-branch
    repo releases from the branch it lands on."""
    names = set(PROTECTED)
    target = release_target(main)
    if target and branch_exists(main, "dev"):
        names.add(target)
    return names


def worker_range(main: str, base: str, branch: str) -> str:
    """Revision range of the worker's commits, or "" when it cannot be found."""
    ahead = git(main, "rev-list", "--count", f"{base}..{branch}").stdout.strip()
    if ahead.isdigit() and int(ahead) > 0:
        return f"{base}..{branch}"
    if git(main, "merge-base", "--is-ancestor", branch, base).returncode != 0:
        return ""
    log = git(main, "log", "-g", "--format=%H %gs", f"refs/heads/{base}").stdout
    entries = log.splitlines()
    for i, entry in enumerate(entries):
        if entry.partition(" ")[2].startswith(f"merge {branch}:"):
            return (
                f"{entries[i + 1].split()[0]}..{branch}" if i + 1 < len(entries) else ""
            )
    return ""


def task_from_trailers(main: str, rng: str) -> str:
    if not rng:
        return ""
    out = git(main, "log", rng, "--format=%(trailers:key=Task,valueonly)").stdout
    for line in out.splitlines():
        if line.strip():
            return line.strip().lstrip("#")
    return ""


def branch_shas(main: str, rng: str, branch: str, saved: list[str]) -> list[str]:
    if rng:
        return git(main, "log", "--format=%h", rng).stdout.split()
    kept = [
        sha
        for sha in saved
        if git(main, "merge-base", "--is-ancestor", sha, branch).returncode == 0
    ]
    return kept or git(main, "log", "-1", "--format=%h", branch).stdout.split()


def parse_phase(raw: str) -> tuple[int, int]:
    match = re.fullmatch(r"(\d+)/(\d+)", raw)
    if match is None or not 1 <= int(match[1]) <= int(match[2]):
        raise Stop(EXIT_ERROR, f"bad --phase {raw!r}: want N/M with 1 <= N <= M")
    return int(match[1]), int(match[2])


# --------------------------------------------------------------------------- #
# State                                                                        #
# --------------------------------------------------------------------------- #


def state_dir() -> Path:
    base = os.environ.get("XDG_STATE_HOME") or str(Path.home() / ".local" / "state")
    return Path(base) / "zen-merge"


def state_path(session: str) -> Path:
    return state_dir() / f"{session.replace('/', '_')}.json"


def load_state(session: str) -> State | None:
    path = state_path(session)
    if not path.exists():
        return None
    raw = json.loads(path.read_text())
    if not isinstance(raw, dict):
        return None
    state = State(session=session)
    for key, value in raw.items():
        if hasattr(state, str(key)):
            setattr(state, str(key), value)
    return state


def save_state(state: State) -> None:
    if DRY_RUN:
        return
    path = state_path(state.session)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(asdict(state), indent=2) + "\n")


def clear_state(state: State) -> None:
    if not DRY_RUN:
        state_path(state.session).unlink(missing_ok=True)


# --------------------------------------------------------------------------- #
# Phases                                                                       #
# --------------------------------------------------------------------------- #


def phase_resolve(state: State, base: str) -> None:
    sessions = _sessions.list_sessions()
    if sessions is None:
        raise Stop(EXIT_ERROR, "agent-manager sessions failed")
    match = _sessions.find_session(state.session, sessions)
    if match is None:
        raise Stop(EXIT_ERROR, f"unknown session {state.session!r}")
    worktree, branch = match.get("directory"), match.get("branch")
    if not isinstance(worktree, str) or not isinstance(branch, str) or not branch:
        raise Stop(EXIT_ERROR, f"session {state.session!r} has no worktree branch")

    state.session_id = str(match.get("id"))
    state.worktree = worktree
    state.branch = branch
    state.main = main_checkout(worktree)
    if Path(state.main).resolve() == Path(worktree).resolve():
        raise Stop(
            EXIT_ERROR, f"{worktree} is the main checkout, not a worker worktree"
        )

    guarded = protected_branches(state.main)
    if base in guarded:
        raise Stop(EXIT_REFUSED, f"refusing to land on protected branch {base!r}")
    if branch_exists(state.main, base):
        state.base = base
    elif base == "dev":
        on = current_branch(state.main)
        if on not in SINGLE_BRANCH:
            raise Stop(
                EXIT_REFUSED,
                f"no dev branch and the main checkout is on {on!r},"
                " not main/master; see Branch model in the zen-flow skill",
            )
        state.base = on
    else:
        raise Stop(EXIT_ERROR, f"no branch {base!r} in {state.main}")
    state.release = state.base == "dev"

    if state.base in guarded:
        raise Stop(EXIT_REFUSED, f"refusing to land on protected branch {state.base!r}")
    if state.branch in guarded or state.branch == state.base:
        raise Stop(EXIT_REFUSED, f"refusing to merge branch {state.branch!r}")
    rng = worker_range(state.main, state.base, state.branch)
    state.task_id = task_from_trailers(state.main, rng) or state.task_id
    state.shas = branch_shas(state.main, rng, state.branch, state.shas)


def phase_clean(state: State) -> None:
    for label, path in (
        ("worker worktree", state.worktree),
        ("main checkout", state.main),
    ):
        if git(path, "status", "--porcelain").stdout.strip():
            raise Stop(EXIT_DIRTY, f"{label} {path} has uncommitted changes")
    on = current_branch(state.main)
    if on != state.base:
        raise Stop(EXIT_DIRTY, f"main checkout is on {on!r}, not {state.base!r}")


def phase_gate(state: State) -> None:
    gate = load_gate()
    try:
        report = gate.run_gate(Path(state.worktree), state.base)
    except gate.GateError as exc:
        raise Stop(EXIT_ERROR, f"zen-gate: {exc}") from exc
    for hit in report.followups:
        print(f"{hit} (follow-up)", file=sys.stderr)
    if not report.clean:
        for hit in report.hits:
            print(hit, file=sys.stderr)
        raise Stop(EXIT_GATE, f"zen-gate: {len(report.hits)} hit(s)")


def phase_merge(state: State) -> None:
    rng = worker_range(state.main, state.base, state.branch)
    state.task_id = state.task_id or task_from_trailers(state.main, rng)
    state.shas = branch_shas(state.main, rng, state.branch, state.shas)
    if (
        git(
            state.main, "merge-base", "--is-ancestor", state.branch, state.base
        ).returncode
        == 0
    ):
        return
    ff = git(state.main, "merge-base", "--is-ancestor", state.base, state.branch)
    if ff.returncode != 0:
        raise Stop(EXIT_NOT_FF, f"{state.branch} is not a fast-forward of {state.base}")
    result = mutate(state.main, ["git", "merge", "--ff-only", state.branch])
    if result.returncode != 0:
        raise Stop(EXIT_NOT_FF, result.stderr.strip() or "git merge --ff-only failed")


def phase_release(state: State) -> None:
    if not state.release:
        state.tag = PUSH_PENDING
        return
    if state.base in protected_branches(state.main):
        raise Stop(EXIT_REFUSED, f"refusing to release from {state.base!r}")
    result = run_release(state.main)
    if result.returncode != 0:
        tail = (result.stdout + result.stderr).strip().splitlines()[-5:]
        raise Stop(EXIT_RELEASE, "zen-release failed:\n" + "\n".join(tail))
    if DRY_RUN:
        state.tag = "(dry-run)"
        return
    tags = git(
        state.main, "tag", "--points-at", "HEAD", "--sort=-v:refname"
    ).stdout.split()
    state.tag = tags[0] if tags else "(untagged)"


def phase_archive(state: State) -> None:
    if DRY_RUN:
        print(
            f"[dry-run] agent-manager archive {state.session_id} --json",
            file=sys.stderr,
        )
        return
    code, _ = _sessions.agent_manager("archive", state.session_id, "--json")
    if code != 0:
        raise Stop(EXIT_ERROR, f"agent-manager archive {state.session_id} failed")


def phase_worktree(state: State) -> None:
    if not Path(state.worktree).exists():
        return
    result = mutate(state.main, ["git", "worktree", "remove", state.worktree])
    if result.returncode != 0:
        raise Stop(EXIT_ERROR, result.stderr.strip() or "git worktree remove failed")


def delete_branch(main: str, branch: str, base: str) -> None:
    if not branch_exists(main, branch):
        return
    if mutate(main, ["git", "branch", "-d", branch]).returncode == 0:
        return
    if git(main, "merge-base", "--is-ancestor", branch, base).returncode != 0:
        raise Stop(EXIT_ERROR, f"{branch} is not merged into {base}; left in place")
    result = mutate(main, ["git", "branch", "-D", branch])
    if result.returncode != 0:
        raise Stop(
            EXIT_ERROR, result.stderr.strip() or f"git branch -D {branch} failed"
        )


def phase_branch(state: State) -> None:
    delete_branch(state.main, state.branch, state.base)


def is_partial(state: State) -> bool:
    if not state.part:
        return False
    n, m = parse_phase(state.part)
    return n < m


def phase_board(state: State) -> None:
    if not state.task_id:
        state.board = "skipped"
        return
    parts = [state.tag, " ".join(state.shas)] if state.shas else [state.tag]
    if state.part:
        parts.insert(0, f"phase {state.part}")
    note = " · ".join(parts)
    verb = "note" if is_partial(state) else "done"
    run = run_board_note if verb == "note" else run_board
    result = run(state.main, state.task_id, note)
    if result.returncode != 0:
        tail = (result.stdout + result.stderr).strip().splitlines()[-5:]
        raise Stop(EXIT_BOARD, f"zen-board {verb} failed:\n" + "\n".join(tail))
    if DRY_RUN:
        state.board = "(dry-run)"
    else:
        state.board = "noted" if verb == "note" else "done"


def run_phase(state: State, name: str, fn: Callable[[], None]) -> None:
    if state.done(name):
        return
    state.phases[name] = "started"
    try:
        fn()
    except Stop:
        state.phases[name] = "failed"
        if name != "resolve":
            save_state(state)
        raise
    state.phases[name] = "done"
    save_state(state)


def land(
    session: str,
    base: str,
    restart: bool = False,
    phase: str = "",
    task: str = "",
) -> State:
    if phase:
        parse_phase(phase)
    state = None if restart else load_state(session)
    if state is None:
        state = State(session=session)
    if phase:
        state.part = phase
    if not state.done("merge"):
        # The branch may have moved since a failed run, so re-check it.
        state.phases.pop("clean", None)
        state.phases.pop("gate", None)
    run_phase(state, "resolve", lambda: phase_resolve(state, base))
    task = task.lstrip("#")
    if task and task != state.task_id:
        if state.task_id:
            print(
                f"zen-merge: --task {task} overrides Task: {state.task_id}",
                file=sys.stderr,
            )
        state.task_id = task
    run_phase(state, "clean", lambda: phase_clean(state))
    run_phase(state, "gate", lambda: phase_gate(state))
    run_phase(state, "merge", lambda: phase_merge(state))
    run_phase(state, "release", lambda: phase_release(state))
    run_phase(state, "archive", lambda: phase_archive(state))
    run_phase(state, "worktree", lambda: phase_worktree(state))
    run_phase(state, "branch", lambda: phase_branch(state))
    run_phase(state, "board", lambda: phase_board(state))
    clear_state(state)
    return state


def summary_line(state: State) -> str:
    task = f"#{state.task_id}" if state.task_id else "-"
    if DRY_RUN:
        return f"{task} · {state.tag} · dry-run, nothing changed"
    board = {
        "skipped": " · board skipped",
        "noted": f" · board noted (phase {state.part})",
    }.get(state.board, "")
    return f"{task} · {state.tag} · cleanup ok{board}"


# --------------------------------------------------------------------------- #
# CLI                                                                          #
# --------------------------------------------------------------------------- #

app = typer.Typer(add_completion=False, help=__doc__)


@app.command()
def main(
    session: str = typer.Argument(..., help="Worker session id or name."),
    base: str = typer.Option("dev", "--base", help="Branch to land on."),
    dry_run: bool = typer.Option(
        False, "--dry-run", help="Echo mutating commands only."
    ),
    restart: bool = typer.Option(False, "--restart", help="Discard saved state."),
    phase: str = typer.Option(
        "", "--phase", help="Phase N/M of a phased task; only N == M closes it."
    ),
    task: str = typer.Option(
        "", "--task", help="Board task ID; wins over the trailer."
    ),
    as_json: bool = typer.Option(False, "--json", help="JSON output."),
) -> None:
    """Land a worker branch; exit 0 landed, 2 gate, 3 not ff, 4 dirty, 5 release, 6 refused, 7 board."""
    global DRY_RUN
    DRY_RUN = dry_run
    try:
        state = land(session, base, restart, phase, task)
    except Stop as exc:
        if as_json:
            print(
                json.dumps(
                    {
                        "session": session,
                        "ok": False,
                        "exit": exc.code,
                        "error": str(exc),
                    }
                )
            )
        print(f"zen-merge: {exc}", file=sys.stderr)
        if exc.code == EXIT_ERROR and not dry_run:
            print("  fix it, then rerun zen-merge to resume", file=sys.stderr)
        if exc.code == EXIT_BOARD:
            print(
                "  rerun zen-merge to retry the board step, or mark it Done via MCP",
                file=sys.stderr,
            )
        raise typer.Exit(exc.code) from exc
    if as_json:
        print(
            json.dumps(
                {
                    "session": session,
                    "ok": True,
                    "task_id": state.task_id,
                    "tag": state.tag,
                    "base": state.base,
                    "branch": state.branch,
                    "cleanup": "dry-run" if dry_run else "ok",
                    "board": state.board,
                    "phase": state.part,
                }
            )
        )
    else:
        print(summary_line(state))


if __name__ == "__main__":
    app()
