#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["pytest>=8.0"]
# ///
"""Tests for pi-box/eval/eval.py. No test reaches Venice or a real key.

Run with:  uv run pi-box/eval/test_eval.py
"""

from __future__ import annotations

import importlib.util
import io
import json
import os
import socket
import subprocess
import sys
import types
from collections.abc import Iterator, Mapping
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parent
_spec = importlib.util.spec_from_file_location("pibox_eval", HERE / "eval.py")
assert _spec is not None and _spec.loader is not None
ev = importlib.util.module_from_spec(_spec)
sys.modules["pibox_eval"] = ev
_spec.loader.exec_module(ev)

FAKE_KEY = "fake-key-for-tests-only"
MODELS = ev.load_models(HERE / "models.json")
TASKS = ev.load_tasks(HERE / "tasks.json")
CHEAP = ev.Model("m", price_in=1.0, price_out=1.0, max_tokens=1000, context_window=9000)


def assistant(
    tokens_in: int, tokens_out: int, text: str = "", cost: float = 0.0
) -> bytes:
    usage = {"input": tokens_in, "output": tokens_out, "cost": {"total": cost}}
    msg = {
        "role": "assistant",
        "content": [{"type": "text", "text": text}],
        "usage": usage,
    }
    return json.dumps({"type": "message_end", "message": msg}).encode() + b"\n"


# --- plan and dry run -------------------------------------------------------


def test_plan_covers_three_models_by_ten_tasks() -> None:
    assert [m.id for m in MODELS] == [
        "glm-5-3-flash-uncensored",
        "deepseek-v4-1-flash-uncensored",
        "gemma-4-uncensored",
    ]
    assert len(TASKS) == 10
    calls = ev.plan(MODELS, TASKS)
    assert len(calls) == 30
    assert {(c.model.id, c.task.id) for c in calls} == {
        (m.id, t.id) for m in MODELS for t in TASKS
    }
    assert [c.model.id for c in calls[:3]] == [m.id for m in MODELS]


def test_dry_run_prints_every_call_and_the_estimate() -> None:
    calls = ev.plan(MODELS, TASKS)
    buf = io.StringIO()
    total = ev.dry_run(calls, 3.0, ev.CALL_CAP_USD, buf)
    out = buf.getvalue()
    for c in calls:
        row = f"{c.n:>3}  {c.model.id:<32} {c.task.id:<15} {c.model.max_tokens:>7}"
        assert row in out
    assert total == pytest.approx(sum(c.estimate() for c in calls))
    assert f"estimate: ${total:.4f}" in out
    assert "worst case: $3.00 (30 of 30 calls fit the cap)" in out
    assert total < 1.0


def test_estimate_uses_published_prices() -> None:
    glm = MODELS[0]
    call = ev.Call(1, glm, TASKS[0])
    want = (TASKS[0].est_input_tokens * 0.165 + TASKS[0].est_output_tokens * 0.55) / 1e6
    assert call.estimate() == pytest.approx(want)


def test_validate_refuses_a_cap_over_three_dollars() -> None:
    assert ev.validate(MODELS, 3.0, ev.CALL_CAP_USD) == []
    assert ev.validate(MODELS, 3.01, ev.CALL_CAP_USD)
    assert ev.validate(MODELS, 0, ev.CALL_CAP_USD)
    assert ev.validate(MODELS, 3.0, 0.03)  # two DeepSeek messages don't fit


class _Recorder(Mapping[str, str]):
    """os.environ stand-in that records every lookup (get and `in` included)."""

    def __init__(self, base: dict[str, str]) -> None:
        self.base = base
        self.seen: list[str] = []

    def __getitem__(self, key: str) -> str:
        self.seen.append(key)
        return self.base[key]

    def __iter__(self) -> Iterator[str]:
        return iter(self.base)

    def __len__(self) -> int:
        return len(self.base)


def test_dry_run_touches_no_network_process_or_key(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    def refuse(*_a: object, **_k: object) -> None:
        raise AssertionError("dry run reached the network or a process")

    monkeypatch.setattr(socket.socket, "connect", refuse)
    monkeypatch.setattr(socket, "create_connection", refuse)
    monkeypatch.setattr(subprocess, "Popen", refuse)
    monkeypatch.setattr(subprocess, "run", refuse)
    env = _Recorder({"VENICE_API_KEY": FAKE_KEY})
    monkeypatch.setattr(ev, "os", types.SimpleNamespace(environ=env))
    assert ev.main([]) == 0
    assert ev.main(["--dry-run"]) == 0
    assert "VENICE_API_KEY" not in env.seen
    assert FAKE_KEY not in capsys.readouterr().out


def test_dry_run_cli_never_runs_the_wrapper(tmp_path: Path) -> None:
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    marker = tmp_path / "called"
    for name in ("pi-box", "docker", "pi", "curl"):
        tool = bin_dir / name
        tool.write_text(f"#!/bin/sh\necho {name} >> {marker}\n")
        tool.chmod(0o755)
    env = {
        "PATH": f"{bin_dir}:/usr/bin:/bin",
        "HOME": str(tmp_path / "home"),
        "VENICE_API_KEY": FAKE_KEY,
    }
    proc = subprocess.run(
        [sys.executable, str(HERE / "eval.py")],
        env=env,
        capture_output=True,
        text=True,
        check=True,
    )
    assert "DRY RUN" in proc.stdout
    assert FAKE_KEY not in proc.stdout + proc.stderr
    assert not marker.exists()
    assert not (tmp_path / "home").exists()


# --- cap arithmetic ---------------------------------------------------------


def test_budget_starts_a_call_only_when_its_cap_still_fits() -> None:
    b = ev.Budget(cap=3.0, call_cap=0.10)
    started = 0
    while b.can_start():
        b.spent += b.call_cap
        started += 1
    assert started == 30
    assert b.spent <= 3.0 + ev.EPS
    assert not ev.Budget(3.0, 0.10, spent=2.95).can_start()
    assert ev.Budget(3.0, 0.10, spent=2.90).can_start()


def test_guard_stops_while_two_messages_still_fit() -> None:
    reserve = CHEAP.message_reserve()  # $0.01
    g = ev.CallGuard(call_cap=0.05, reserve=reserve)
    for _ in range(3):
        g.add(0.01)
    assert not g.must_stop()
    g.add(0.0001)
    assert g.must_stop()


def test_consume_stops_before_the_call_can_cross_its_cap() -> None:
    reserve = CHEAP.message_reserve()
    guard = ev.CallGuard(call_cap=0.05, reserve=reserve)
    result = ev.CallResult(model="m", task="t")
    stops: list[str] = []
    lines = [assistant(5000, 500) for _ in range(20)]
    consumed: list[bytes] = []

    def feed() -> Iterator[bytes]:
        for line in lines:
            if stops:
                return
            consumed.append(line)
            yield line

    ev.consume(feed(), CHEAP, guard, result, stops.append)
    assert stops == ["call cap"]
    assert result.stopped == "call cap"
    # one message may still land after the kill; it stays under the cap
    assert result.cost + reserve <= 0.05 + ev.EPS
    assert len(consumed) < len(lines)


def test_message_cost_takes_the_larger_of_reported_and_computed() -> None:
    assert ev.message_cost({"input": 1000, "output": 0}, CHEAP) == pytest.approx(0.001)
    assert ev.message_cost(
        {"input": 1000, "output": 0, "cost": {"total": 0.5}}, CHEAP
    ) == pytest.approx(0.5)
    assert ev.message_cost(
        {"input": 0, "output": 0, "cacheRead": 2000}, CHEAP
    ) == pytest.approx(0.002)


def test_charge_counts_unseen_messages_after_kill_or_crash() -> None:
    r = ev.CallResult(model="m", task="t", cost=0.02, exit_code=0)
    assert ev.charge(r, CHEAP, 0.1) == pytest.approx(0.02)
    r.exit_code = 1
    assert ev.charge(r, CHEAP, 0.1) == pytest.approx(0.03)
    r.stopped = "timeout"
    assert ev.charge(r, CHEAP, 0.1) == pytest.approx(0.1)
    r.cost = 0.12  # messages already in the pipe when the kill landed
    assert ev.charge(r, CHEAP, 0.1) == pytest.approx(0.12)


FAKE_PIBOX = """#!{python}
import json, os, sys, time
with open(os.environ["FAKE_ARGV"], "a") as f:
    f.write(json.dumps(sys.argv[1:]) + "\\n")
with open(os.environ["FAKE_ARGV"] + ".browser", "a") as f:
    f.write(os.environ.get("PIBOX_BROWSER", "unset") + "\\n")
n = int(os.environ["FAKE_MESSAGES"])
for i in range(n):
    msg = {{"role": "assistant", "content": [{{"type": "text", "text": "x"}}],
           "usage": {{"input": int(os.environ["FAKE_INPUT"]), "output": 1000, "cost": {{"total": 0}}}}}}
    print(json.dumps({{"type": "tool_execution_end", "isError": False}}), flush=True)
    print(json.dumps({{"type": "message_end", "message": msg}}), flush=True)
    time.sleep(float(os.environ.get("FAKE_DELAY", "0")))
"""


def _live_env(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, messages: int, tokens_in: int
) -> Path:
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    pibox = bin_dir / "pi-box"
    pibox.write_text(FAKE_PIBOX.format(python=sys.executable))
    pibox.chmod(0o755)
    docker = bin_dir / "docker"
    docker.write_text(
        f'#!/bin/sh\necho "$@" >> {tmp_path / "docker.log"}\n'
        '[ "$1" = container ] && exit 1\nexit 0\n'
    )
    docker.chmod(0o755)
    monkeypatch.setenv("PATH", f"{bin_dir}:{os.environ['PATH']}")
    monkeypatch.setenv("HOME", str(tmp_path / "home"))
    monkeypatch.setenv("VENICE_API_KEY", FAKE_KEY)
    monkeypatch.setenv("FAKE_ARGV", str(tmp_path / "argv.log"))
    monkeypatch.setenv("FAKE_MESSAGES", str(messages))
    monkeypatch.setenv("FAKE_INPUT", str(tokens_in))
    monkeypatch.delenv("PIBOX_BROWSER", raising=False)
    monkeypatch.setattr(ev.Path, "home", lambda: tmp_path / "home")
    return pibox


def _results(tmp_path: Path) -> dict[str, object]:
    (path,) = (tmp_path / "home" / "pi-box" / "evals").glob("*/results.json")
    data: dict[str, object] = json.loads(path.read_text())
    return data


def test_live_stops_before_a_call_that_could_cross_the_cap(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # an oversized GLM message gets the call killed and charged $0.10;
    # a second call could pass $0.15
    pibox = _live_env(tmp_path, monkeypatch, messages=1, tokens_in=200_000)
    calls = ev.plan(MODELS, TASKS)
    assert ev.live(calls, 0.15, 0.10, str(pibox)) == 0
    argv = (tmp_path / "argv.log").read_text().splitlines()
    assert len(argv) == 1
    first = json.loads(argv[0])
    assert first[:3] == [
        "exec",
        "env",
        f"PI_CODING_AGENT_DIR={tmp_path}/home/pi-box/work/eval-898/agent",
    ]
    assert first[3:9] == [
        "pi",
        "--mode",
        "json",
        "--no-session",
        "--provider",
        "venice",
    ]
    assert FAKE_KEY not in "".join(argv)
    assert (tmp_path / "argv.log.browser").read_text().splitlines() == ["0"]
    data = _results(tmp_path)
    spent = data["spent_usd"]
    assert isinstance(spent, float) and spent <= 0.15
    stopped = data["stopped"]
    assert isinstance(stopped, str) and stopped.startswith("cap")
    agent = tmp_path / "home" / "pi-box" / "work" / "eval-898" / "agent"
    assert (agent / "models.json").read_text() == (HERE / "models.json").read_text()
    assert (agent / "skills" / "fetch" / "SKILL.md").is_file()


def test_live_kills_a_call_over_its_cap(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    pibox = _live_env(tmp_path, monkeypatch, messages=200, tokens_in=20_000)
    monkeypatch.setenv("FAKE_DELAY", "0.2")  # a real message takes seconds
    calls = ev.plan(MODELS, TASKS)[1:2]  # DeepSeek, the priciest
    assert ev.live(calls, 3.0, 0.10, str(pibox)) == 0
    docker_log = (tmp_path / "docker.log").read_text()
    assert "kill pi-box-eval-898" in docker_log
    assert "container inspect pi-box-eval-898" in docker_log
    data = _results(tmp_path)
    calls_out = data["calls"]
    assert isinstance(calls_out, list) and len(calls_out) == 1
    call = calls_out[0]
    assert isinstance(call, dict) and call["stopped"] == "call cap"
    assert call["cost"] + calls[0].model.message_reserve() <= 0.10 + ev.EPS
    assert data["spent_usd"] == pytest.approx(0.10)


def test_live_refuses_without_a_key(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("VENICE_API_KEY", raising=False)
    assert ev.live(ev.plan(MODELS, TASKS), 3.0, 0.10, "pi-box") == 2


# --- rubric -----------------------------------------------------------------


def _task(checks: list[dict[str, object]], tid: str = "t") -> object:
    return ev.Task(tid, "p", 1, 1, checks, {})


def test_every_task_check_type_is_known(tmp_path: Path) -> None:
    for task in TASKS:
        for check in task.checks:
            ev.run_check(check, tmp_path)  # raises on an unknown type


def test_checks(tmp_path: Path) -> None:
    out = tmp_path / "out"
    out.mkdir()
    (out / "a.txt").write_text("Albert Einstein\nJane Austen\n")
    (out / "n.txt").write_text("2\n")
    (out / "j.json").write_text('{"status": 200}')
    (out / "l.json").write_text("[1, 2]")
    run = ev.run_check
    assert run(
        {"type": "contains", "path": "out/a.txt", "all": ["Jane Austen"]}, tmp_path
    )
    assert not run(
        {"type": "contains", "path": "out/a.txt", "all": ["Nobody"]}, tmp_path
    )
    assert run({"type": "equals", "path": "out/n.txt", "value": "2"}, tmp_path)
    assert run({"type": "min_lines", "path": "out/a.txt", "lines": 2}, tmp_path)
    assert run(
        {"type": "count_matches_lines", "path": "out/n.txt", "lines_of": "out/a.txt"},
        tmp_path,
    )
    assert run(
        {"type": "json_field", "path": "out/j.json", "field": "status", "equals": 200},
        tmp_path,
    )
    assert run({"type": "json_length", "path": "out/l.json", "length": 2}, tmp_path)
    assert not run({"type": "exists", "path": "out/missing"}, tmp_path)


def test_scores(tmp_path: Path) -> None:
    (tmp_path / "f").write_text("ok")
    passing = _task([{"type": "equals", "path": "f", "value": "ok"}])
    failing = _task([{"type": "equals", "path": "f", "value": "no"}])

    def scored(
        task: object,
        tool_calls: int = 0,
        tool_errors: int = 0,
        final_text: str = "",
        stopped: str = "",
    ) -> int:
        r = ev.CallResult(
            model="m",
            task="t",
            tool_calls=tool_calls,
            tool_errors=tool_errors,
            final_text=final_text,
            stopped=stopped,
        )
        ev.score(r, task, tmp_path)
        return int(r.score)

    assert scored(passing, tool_calls=2, final_text="done WORKER_DONE t") == 3
    assert scored(passing, tool_calls=2, tool_errors=1, final_text="WORKER_DONE t") == 2
    assert scored(passing, tool_calls=2) == 2
    assert scored(passing, tool_calls=2, final_text="WORKER_DONE t, then more") == 2
    assert (
        scored(passing, tool_calls=2, final_text="WORKER_DONE t", stopped="call cap")
        == 1
    )
    assert scored(failing, tool_calls=1) == 1
    assert scored(failing, tool_calls=1, tool_errors=1) == 0
    assert scored(failing) == 0


def test_default_model_keeps_glm_within_two_points() -> None:
    glm, ds = "glm-5-3-flash-uncensored", "deepseek-v4-1-flash-uncensored"
    close = {glm: {"score": 26.0, "cost": 0.1}, ds: {"score": 28.0, "cost": 0.2}}
    assert ev.default_model(close, glm) == glm
    far = {glm: {"score": 20.0, "cost": 0.1}, ds: {"score": 28.0, "cost": 0.2}}
    assert ev.default_model(far, glm) == ds
    tie = {"a": {"score": 20.0, "cost": 0.2}, "b": {"score": 20.0, "cost": 0.1}}
    assert ev.default_model(tie, "x") == "b"


def test_complete_rounds_drops_a_half_run_task() -> None:
    rs = [
        ev.CallResult(model="a", task="t1"),
        ev.CallResult(model="b", task="t1"),
        ev.CallResult(model="a", task="t2"),
    ]
    assert [(r.model, r.task) for r in ev.complete_rounds(rs)] == [
        ("a", "t1"),
        ("b", "t1"),
    ]


def test_guard_widens_its_step_after_an_oversized_message() -> None:
    g = ev.CallGuard(call_cap=0.10, reserve=0.01)
    g.add(0.04)
    assert g.must_stop()


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
