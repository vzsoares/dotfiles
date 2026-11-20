#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# ///
"""pi-box model eval: every task in tasks.json on every model in models.json.

Dry run (default) prints the plan and a cost estimate; it reads only the
files next to this script, never the network or the env.
--live runs each call through `pi-box exec` with a hard USD cap.

    uv run pi-box/eval/eval.py            # dry run
    uv run pi-box/eval/eval.py --live     # paid: needs an approved ask
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import threading
import time
from collections.abc import Callable, Iterable
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import IO

HERE = Path(__file__).resolve().parent
BOX_DIR = HERE.parent
MAX_CAP_USD = 3.0
CALL_CAP_USD = 0.10
CALL_TIMEOUT_S = 600
PROVIDER = "venice"
THINKING = "low"
WORK_ID = "eval-898"
EPS = 1e-9


@dataclass(frozen=True)
class Model:
    id: str
    price_in: float  # USD per 1M input tokens
    price_out: float
    max_tokens: int
    context_window: int

    def cost(self, tokens_in: int, tokens_out: int) -> float:
        return (tokens_in * self.price_in + tokens_out * self.price_out) / 1e6

    def message_reserve(self) -> float:
        """Worst case for one more assistant message: a full context in, maxTokens out."""
        return self.cost(self.context_window, self.max_tokens)


@dataclass(frozen=True)
class Task:
    id: str
    prompt: str
    est_input_tokens: int
    est_output_tokens: int
    checks: list[dict[str, object]]
    files: dict[str, str]


@dataclass(frozen=True)
class Call:
    n: int
    model: Model
    task: Task

    def estimate(self) -> float:
        return self.model.cost(self.task.est_input_tokens, self.task.est_output_tokens)


@dataclass
class Budget:
    """Total spend never passes cap: a call starts only when its own cap still fits."""

    cap: float
    call_cap: float
    spent: float = 0.0

    def can_start(self) -> bool:
        return self.spent + self.call_cap <= self.cap + EPS


@dataclass
class CallGuard:
    """Stops a call while two more messages still fit: one may already be in
    flight when the kill lands, so observed spend plus it stays under the cap."""

    call_cap: float
    reserve: float
    spent: float = 0.0
    largest: float = 0.0

    def add(self, cost: float) -> None:
        self.spent += cost
        self.largest = max(self.largest, cost)

    def must_stop(self) -> bool:
        # pi does not enforce contextWindow per request, so a seen message may beat reserve
        step = max(self.reserve, self.largest)
        return self.spent + 2 * step > self.call_cap + EPS


@dataclass
class CallResult:
    model: str
    task: str
    cost: float = 0.0
    messages: int = 0
    tool_calls: int = 0
    tool_errors: int = 0
    final_text: str = ""
    stopped: str = ""
    exit_code: int | None = None
    seconds: float = 0.0
    checks_passed: int = 0
    checks_total: int = 0
    failed_checks: list[str] = field(default_factory=list)
    score: int = 0


def load_models(path: Path) -> list[Model]:
    data = json.loads(path.read_text())
    out = []
    for m in data["providers"][PROVIDER]["models"]:
        out.append(
            Model(
                id=m["id"],
                price_in=float(m["cost"]["input"]),
                price_out=float(m["cost"]["output"]),
                max_tokens=int(m["maxTokens"]),
                context_window=int(m["contextWindow"]),
            )
        )
    return out


def load_tasks(path: Path) -> list[Task]:
    data = json.loads(path.read_text())
    return [
        Task(
            id=t["id"],
            prompt=t["prompt"],
            est_input_tokens=int(t["est_input_tokens"]),
            est_output_tokens=int(t["est_output_tokens"]),
            checks=list(t["checks"]),
            files=dict(t.get("files", {})),
        )
        for t in data["tasks"]
    ]


def plan(models: list[Model], tasks: list[Task]) -> list[Call]:
    """Task-major order, so a cap stop leaves every model with the same tasks done."""
    calls: list[Call] = []
    for task in tasks:
        for model in models:
            calls.append(Call(len(calls) + 1, model, task))
    return calls


def validate(models: list[Model], cap: float, call_cap: float) -> list[str]:
    errors = []
    if not 0 < cap <= MAX_CAP_USD:
        errors.append(f"cap must be in (0, {MAX_CAP_USD}], got {cap}")
    if call_cap > cap:
        errors.append(f"call cap {call_cap} exceeds total cap {cap}")
    for m in models:
        if 2 * m.message_reserve() >= call_cap:
            errors.append(
                f"{m.id}: two messages can cost ${2 * m.message_reserve():.4f}, "
                f"not under the ${call_cap} call cap"
            )
    return errors


def full_prompt(task: Task) -> str:
    return (
        f"{task.prompt}\n\nWork only inside the current folder. "
        f"End your final message with WORKER_DONE {task.id}"
    )


def pi_command(pibox: str, agent_dir: Path, call: Call) -> list[str]:
    return [
        pibox,
        "exec",
        "env",
        f"PI_CODING_AGENT_DIR={agent_dir}",
        "pi",
        "--mode",
        "json",
        "--no-session",
        "--provider",
        PROVIDER,
        "--model",
        call.model.id,
        "--thinking",
        THINKING,
        full_prompt(call.task),
    ]


def dry_run(
    calls: list[Call], cap: float, call_cap: float, out: IO[str] = sys.stdout
) -> float:
    w = out.write
    w(f"DRY RUN: no network, no key. {len(calls)} calls, provider {PROVIDER}, ")
    w(f"thinking {THINKING}\n")
    w(f"cap ${cap:.2f} total, ${call_cap:.2f} per call\n\n")
    w(f"{'#':>3}  {'model':<32} {'task':<15} {'max_tok':>7} {'est_usd':>8}\n")
    total = 0.0
    budget = Budget(cap, call_cap)
    fit = 0
    for c in calls:
        est = c.estimate()
        total += est
        if budget.can_start():
            budget.spent += call_cap
            fit += 1
        w(
            f"{c.n:>3}  {c.model.id:<32} {c.task.id:<15} "
            f"{c.model.max_tokens:>7} {est:>8.4f}\n"
        )
    w("\nper model:\n")
    for m in dict.fromkeys(c.model for c in calls):
        sub = sum(c.estimate() for c in calls if c.model == m)
        w(
            f"  {m.id:<32} ${sub:.4f}  (in ${m.price_in}/M, out ${m.price_out}/M, "
            f"msg worst ${m.message_reserve():.4f})\n"
        )
    w(f"\nestimate: ${total:.4f}\n")
    w(f"worst case: ${fit * call_cap:.2f} ({fit} of {len(calls)} calls fit the cap)\n")
    if calls:
        w("\nfirst call would run:\n  ")
        w(" ".join(pi_command("pi-box", Path("<work>/agent"), calls[0])[:-1]))
        w(" '<prompt>'\n")
    return total


def message_cost(usage: dict[str, object], model: Model) -> float:
    """The larger of pi's reported cost and our own from the token counts."""

    def num(key: str) -> int:
        v = usage.get(key, 0)
        return v if isinstance(v, int) else 0

    ours = model.cost(
        num("input") + num("cacheRead") + num("cacheWrite"), num("output")
    )
    reported = 0.0
    cost = usage.get("cost")
    if isinstance(cost, dict):
        total = cost.get("total", 0)
        if isinstance(total, int | float):
            reported = float(total)
    return max(ours, reported)


def text_of(message: dict[str, object]) -> str:
    content = message.get("content")
    if not isinstance(content, list):
        return ""
    parts = []
    for block in content:
        if isinstance(block, dict) and block.get("type") == "text":
            text = block.get("text")
            if isinstance(text, str):
                parts.append(text)
    return "".join(parts)


def consume(
    lines: Iterable[bytes],
    model: Model,
    guard: CallGuard,
    result: CallResult,
    stop: Callable[[str], None],
) -> None:
    """Reads pi's JSONL stream, tallying cost; calls stop() once over the call cap."""
    for raw in lines:
        try:
            event = json.loads(raw.rstrip(b"\r\n"))
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        kind = event.get("type")
        if kind == "tool_execution_end":
            result.tool_calls += 1
            if event.get("isError"):
                result.tool_errors += 1
        elif kind == "message_end":
            message = event.get("message")
            if not isinstance(message, dict) or message.get("role") != "assistant":
                continue
            usage = message.get("usage")
            cost = message_cost(usage, model) if isinstance(usage, dict) else 0.0
            guard.add(cost)
            result.cost = guard.spent
            result.messages += 1
            result.final_text = text_of(message) or result.final_text
            if guard.must_stop() and not result.stopped:
                result.stopped = "call cap"
                stop("call cap")


def read_text(path: Path) -> str | None:
    try:
        return path.read_text().strip()
    except (OSError, UnicodeDecodeError):
        return None


def run_check(check: dict[str, object], root: Path) -> bool:
    path = root / str(check["path"])
    kind = check["type"]
    if kind == "exists":
        return path.is_file() and path.stat().st_size > 0
    text = read_text(path)
    if text is None:
        return False
    if kind == "contains":
        needles = check["all"]
        return isinstance(needles, list) and all(str(n) in text for n in needles)
    if kind == "equals":
        return text == str(check["value"]).strip()
    if kind == "min_lines":
        want = check["lines"]
        return isinstance(want, int) and len(text.splitlines()) >= want
    if kind == "count_matches_lines":
        other = read_text(root / str(check["lines_of"]))
        lines = [ln for ln in (other or "").splitlines() if ln.strip()]
        return text.isdigit() and int(text) == len(lines) > 0
    try:
        data = json.loads(text)
    except ValueError:
        return False
    if kind == "json_field":
        return (
            isinstance(data, dict) and data.get(str(check["field"])) == check["equals"]
        )
    if kind == "json_length":
        return isinstance(data, list) and len(data) == check["length"]
    raise ValueError(f"unknown check type {kind!r}")


def score(result: CallResult, task: Task, root: Path) -> None:
    """RUBRIC.md: 3 clean pass, 2 pass with tool errors or no marker, 1 tried, 0 nothing."""
    result.checks_total = len(task.checks)
    for i, check in enumerate(task.checks):
        if run_check(check, root):
            result.checks_passed += 1
        else:
            result.failed_checks.append(f"{i}:{check['type']}:{check['path']}")
    passed = result.checks_passed == result.checks_total and not result.stopped
    marker = result.final_text.rstrip().endswith(f"WORKER_DONE {task.id}")
    if passed and marker and result.tool_errors == 0:
        result.score = 3
    elif passed:
        result.score = 2
    elif result.tool_calls > result.tool_errors:
        result.score = 1
    else:
        result.score = 0


def charge(result: CallResult, model: Model, call_cap: float) -> float:
    """What a finished call counts against the total: an unseen message is
    possible after a kill or a crash, so those are charged their bound."""
    if result.stopped:
        return max(call_cap, result.cost)
    if result.exit_code != 0:
        return max(result.cost, min(call_cap, result.cost + model.message_reserve()))
    return result.cost


def docker_kill(name: str) -> None:
    subprocess.run(["docker", "kill", name], capture_output=True, check=False)


def wait_gone(name: str, timeout: float = 30.0) -> bool:
    """--rm removal is async once the client is killed; the next run reuses the name."""
    deadline = time.monotonic() + timeout
    while True:
        probe = subprocess.run(
            ["docker", "container", "inspect", name], capture_output=True, check=False
        )
        if probe.returncode != 0:
            return True
        if time.monotonic() >= deadline:
            return False
        time.sleep(0.5)


def run_call(
    cmd: list[str],
    cwd: Path,
    model: Model,
    call_cap: float,
    log: Path,
    container: str,
    timeout: float = CALL_TIMEOUT_S,
) -> CallResult:
    result = CallResult(model=model.id, task=cwd.name)
    guard = CallGuard(call_cap, model.message_reserve())
    started = time.monotonic()
    with log.open("wb") as tee, (log.with_suffix(".stderr")).open("wb") as err:
        proc = subprocess.Popen(
            cmd, cwd=cwd, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=err
        )
        stdout = proc.stdout
        if stdout is None:
            raise RuntimeError("no stdout pipe")
        stopped = threading.Event()

        def stop(reason: str) -> None:
            if stopped.is_set():
                return
            stopped.set()
            result.stopped = result.stopped or reason
            docker_kill(container)
            proc.kill()

        timer = threading.Timer(timeout, stop, args=("timeout",))
        timer.start()

        def lines() -> Iterable[bytes]:
            for line in stdout:
                tee.write(line)
                yield line

        try:
            consume(lines(), model, guard, result, stop)
        finally:
            timer.cancel()
            result.exit_code = proc.wait()
    if stopped.is_set() and not wait_gone(container):
        raise RuntimeError(f"{container} survived docker kill; stop it by hand")
    result.seconds = round(time.monotonic() - started, 1)
    return result


def prepare_agent_dir(agent: Path) -> None:
    """The eval's own pi agent dir: its models.json, the box AGENTS.md and skills."""
    agent.mkdir(parents=True, exist_ok=True)
    shutil.copy(HERE / "models.json", agent / "models.json")
    shutil.copy(BOX_DIR / "agent" / "AGENTS.md", agent / "AGENTS.md")
    (agent / "settings.json").write_text('{"defaultProjectTrust": "always"}\n')
    skills = agent / "skills"
    for skill in sorted((BOX_DIR / "skills").iterdir()):
        dest = skills / skill.name
        dest.mkdir(parents=True, exist_ok=True)
        shutil.copy(skill / "SKILL.md", dest / "SKILL.md")


def default_model(totals: dict[str, dict[str, float]], incumbent: str) -> str:
    """Keeps the incumbent within 2 points of the best score; else best score, then cheapest."""
    if not totals:
        return incumbent
    best = max(totals, key=lambda m: (totals[m]["score"], -totals[m]["cost"]))
    if incumbent in totals and totals[incumbent]["score"] >= totals[best]["score"] - 2:
        return incumbent
    return best


def summarize(results: list[CallResult]) -> dict[str, dict[str, float]]:
    totals: dict[str, dict[str, float]] = {}
    for r in results:
        t = totals.setdefault(
            r.model, {"score": 0, "passed": 0, "calls": 0, "cost": 0.0, "tool_calls": 0}
        )
        t["score"] += r.score
        t["passed"] += r.score >= 2
        t["calls"] += 1
        t["cost"] += r.cost
        t["tool_calls"] += r.tool_calls
    return totals


def complete_rounds(results: list[CallResult]) -> list[CallResult]:
    """Only tasks every model ran, so a cap stop mid-round cannot tilt the pick."""
    models = {r.model for r in results}
    ran: dict[str, set[str]] = {}
    for r in results:
        ran.setdefault(r.task, set()).add(r.model)
    return [r for r in results if ran[r.task] == models]


def write_report(
    out_dir: Path, results: list[CallResult], spent: float, cap: float, stopped: str
) -> None:
    totals = summarize(results)
    incumbent = "glm-5-3-flash-uncensored"
    pick = default_model(summarize(complete_rounds(results)), incumbent)
    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "results.json").write_text(
        json.dumps(
            {
                "spent_usd": round(spent, 6),
                "cap_usd": cap,
                "stopped": stopped,
                "default": pick,
                "totals": totals,
                "calls": [asdict(r) for r in results],
            },
            indent=2,
        )
        + "\n"
    )
    rows = [
        "| model | score | passed | calls | cost USD | tool calls |",
        "|---|---|---|---|---|---|",
    ]
    for m, t in totals.items():
        rows.append(
            f"| {m} | {int(t['score'])}/{int(t['calls']) * 3} | {int(t['passed'])} | "
            f"{int(t['calls'])} | {t['cost']:.4f} | {int(t['tool_calls'])} |"
        )
    detail = [
        "| model | task | score | checks | tool errs | cost | stopped |",
        "|---|---|---|---|---|---|---|",
    ]
    for r in results:
        detail.append(
            f"| {r.model} | {r.task} | {r.score} | {r.checks_passed}/{r.checks_total} | "
            f"{r.tool_errors} | {r.cost:.4f} | {r.stopped} |"
        )
    (out_dir / "results.md").write_text(
        f"# pi-box eval\n\nSpent ${spent:.4f} of ${cap:.2f}."
        f"{' Stopped: ' + stopped + '.' if stopped else ''}\n"
        f"Default by the rubric: `{pick}`.\n\n"
        + "\n".join(rows)
        + "\n\n"
        + "\n".join(detail)
        + "\n"
    )


def live(calls: list[Call], cap: float, call_cap: float, pibox: str) -> int:
    if "VENICE_API_KEY" not in os.environ:
        print("eval: VENICE_API_KEY is not set in this shell", file=sys.stderr)
        return 2
    # the wrapper mounts the physical path, so PI_CODING_AGENT_DIR must match it
    root = (Path.home() / "pi-box").resolve()
    stamp = time.strftime("%Y%m%d-%H%M%S")
    work = root / "work" / WORK_ID
    run_dir = work / "runs" / stamp
    out_dir = root / "evals" / stamp
    agent = work / "agent"
    prepare_agent_dir(agent)
    budget = Budget(cap, call_cap)
    results: list[CallResult] = []
    stopped = ""
    for call in calls:
        if not budget.can_start():
            stopped = f"cap: ${budget.spent:.4f} spent, next call could pass ${cap:.2f}"
            print(f"eval: stop before call {call.n}, {stopped}", file=sys.stderr)
            break
        cwd = run_dir / call.model.id / call.task.id
        (cwd / "out").mkdir(parents=True)
        for rel, content in call.task.files.items():
            (cwd / rel).parent.mkdir(parents=True, exist_ok=True)
            (cwd / rel).write_text(content)
        print(
            f"eval: call {call.n}/{len(calls)} {call.model.id} {call.task.id}",
            file=sys.stderr,
        )
        try:
            result = run_call(
                pi_command(pibox, agent, call),
                cwd,
                call.model,
                call_cap,
                cwd / "events.jsonl",
                f"pi-box-{WORK_ID}",
            )
        except RuntimeError as e:
            stopped = str(e)
            print(f"eval: abort at call {call.n}, {stopped}", file=sys.stderr)
            write_report(out_dir, results, budget.spent + call_cap, cap, stopped)
            return 1
        budget.spent += charge(result, call.model, call_cap)
        score(result, call.task, cwd)
        results.append(result)
        write_report(out_dir, results, budget.spent, cap, stopped)
        print(
            f"eval:   score {result.score}, ${result.cost:.4f}, total ${budget.spent:.4f}",
            file=sys.stderr,
        )
    write_report(out_dir, results, budget.spent, cap, stopped)
    print(f"eval: results in {out_dir}")
    return 0


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="pi-box model eval (dry run by default)")
    ap.add_argument("--live", action="store_true", help="call Venice (paid)")
    ap.add_argument("--dry-run", action="store_true", help="the default")
    ap.add_argument("--cap", type=float, default=MAX_CAP_USD)
    ap.add_argument("--call-cap", type=float, default=CALL_CAP_USD)
    ap.add_argument("--pi-box", default="pi-box", help="wrapper to run")
    args = ap.parse_args(argv)
    if args.live and args.dry_run:
        ap.error("--live and --dry-run are exclusive")
    models = load_models(HERE / "models.json")
    tasks = load_tasks(HERE / "tasks.json")
    errors = validate(models, args.cap, args.call_cap)
    if errors:
        for e in errors:
            print(f"eval: {e}", file=sys.stderr)
        return 2
    calls = plan(models, tasks)
    if not args.live:
        dry_run(calls, args.cap, args.call_cap)
        return 0
    return live(calls, args.cap, args.call_cap, args.pi_box)


if __name__ == "__main__":
    sys.exit(main())
