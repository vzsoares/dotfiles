#!/usr/bin/env python3
"""zen-flow handoff to a pi-box session and the RESULT.md contract. No model call.

  contract.py command TASK GOAL_FILE [--model PROVIDER/MODEL] [--root DIR]
      the exact `agent-manager spawn` line to file in a `--prod run` ask
  contract.py check TASK [--root DIR]
      verify work/TASK/RESULT.md and out/; exit 1 listing each problem

RESULT.md has one `## ` section per name in SECTIONS, each non-empty. Pi cannot
know its own cost: the orchestrator adds `pi-box cost` to its note on the task.
"""

from __future__ import annotations

import re
import shlex
import sys
from pathlib import Path

SECTIONS = ("Summary", "Sources", "Left undone")
DEFAULT_MODEL = "venice/glm-5-3-flash-uncensored"
TASK_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]*")


def task_dir(root: Path, task: str) -> Path:
    if not TASK_ID.fullmatch(task):
        raise ValueError(f"bad task id {task!r}")
    return root / "work" / task


def prompt(task: str, goal: str) -> str:
    sections = ", ".join(f"## {s}" for s in SECTIONS)
    return (
        f"Task {task}.\n\n{goal.strip()}\n\n"
        "Rules: work only in your current directory. Put every deliverable in "
        "out/. Write RESULT.md here with a non-empty section for each of: "
        f"{sections}. In Sources list every URL or file you used; in Left "
        "undone write 'nothing' if so. Your final message ends with the line "
        f"WORKER_DONE {task}."
    )


def command(root: Path, task: str, goal: str, model: str) -> str:
    return shlex.join(
        [
            "agent-manager", "spawn",
            "--tool", "pi",
            "--env", "pi-box",
            "--group", "pi-box",
            "--directory", str(task_dir(root, task)),
            "--model", model,
            "--prompt", prompt(task, goal),
        ]
    )  # fmt: skip


def check(root: Path, task: str) -> list[str]:
    base = task_dir(root, task)
    problems: list[str] = []
    if not (base / "out").is_dir():
        problems.append("out/ is missing")
    try:
        text = (base / "RESULT.md").read_text()
    except OSError:
        return [*problems, "RESULT.md is missing"]
    bodies: dict[str, str] = {}
    name = ""
    fenced = False
    for line in text.splitlines():
        if line.lstrip().startswith(("```", "~~~")):
            fenced = not fenced
        heading = None if fenced else re.fullmatch(r"(#{1,6})\s+(.+?)\s*", line)
        if heading:
            name = heading.group(2).lower() if heading.group(1) == "##" else ""
            if name:
                bodies.setdefault(name, "")
        elif name:
            bodies[name] += line.strip()
    for section in SECTIONS:
        if section.lower() not in bodies:
            problems.append(f"RESULT.md has no '## {section}' section")
        elif not bodies[section.lower()]:
            problems.append(f"RESULT.md '## {section}' is empty")
    return problems


def parse(argv: list[str]) -> tuple[list[str], dict[str, str]]:
    pos: list[str] = []
    opts: dict[str, str] = {}
    it = iter(argv)
    for arg in it:
        if arg in ("--model", "--root"):
            opts[arg[2:]] = next(it, "")
        else:
            pos.append(arg)
    return pos, opts


def main(argv: list[str]) -> int:
    pos, opts = parse(argv[1:])
    root = Path(opts.get("root") or Path.home() / "pi-box")
    try:
        if pos[:1] == ["command"] and len(pos) == 3:
            goal = Path(pos[2]).read_text()
            print(command(root, pos[1], goal, opts.get("model") or DEFAULT_MODEL))
            return 0
        if pos[:1] == ["check"] and len(pos) == 2:
            problems = check(root, pos[1])
            for p in problems:
                print(f"contract: {p}", file=sys.stderr)
            return 1 if problems else 0
    except (OSError, ValueError) as e:
        print(f"contract: {e}", file=sys.stderr)
        return 2
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
