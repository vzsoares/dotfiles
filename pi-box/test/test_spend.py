#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["pytest>=8.0"]
# ///
"""Tests for pi-box/spend.py and pi-box/contract.py. No model, key or network.

Run with:  uv run pi-box/test/test_spend.py
"""

from __future__ import annotations

import importlib.util
import json
import sys
import time
from pathlib import Path
from types import ModuleType

import pytest

PKG = Path(__file__).resolve().parent.parent


def load(name: str) -> ModuleType:
    spec = importlib.util.spec_from_file_location(name, PKG / f"{name}.py")
    assert spec is not None and spec.loader is not None
    mod = importlib.util.module_from_spec(spec)
    sys.modules[name] = mod
    spec.loader.exec_module(mod)
    return mod


sp = load("spend")
ct = load("contract")

NOW = int(time.time() * 1000)
MONTH = sp.month_of(NOW)
LAST_MONTH_MS = NOW - 40 * 86_400_000


@pytest.fixture
def root(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    (tmp_path / "agent").mkdir()
    prices = tmp_path.parent / f"{tmp_path.name}-prices.json"
    monkeypatch.setattr(sp, "PRICES_FILE", prices)
    prices.write_text(
        json.dumps(
            {
                "providers": {
                    "venice": {
                        "models": [
                            {"id": "glm", "cost": {"input": 1.0, "output": 2.0}},
                        ]
                    }
                }
            }
        )
    )
    return tmp_path


def ledger(root: Path, *lines: object) -> None:
    with (root / "ledger.jsonl").open("a") as f:
        for line in lines:
            f.write((line if isinstance(line, str) else json.dumps(line)) + "\n")


def run_entries(
    run: str, cost: float | None, ts: int = NOW, reserve: float = 0.1
) -> list[dict[str, object]]:
    out: list[dict[str, object]] = [
        {"kind": "begin", "run": run, "ts": ts, "reserve": reserve}
    ]
    if cost is not None:
        out.append({"kind": "end", "run": run, "models": {"glm": cost}})
    return out


def message(
    ts: int, usage: object, model: str = "glm", role: str = "assistant"
) -> dict[str, object]:
    return {"type": "message", "message": {"role": role, "model": model,
                                           "timestamp": ts, "usage": usage}}  # fmt: skip


def session(
    root: Path, *records: object, name: str = "s1", task: str = "t", mode: str = "w"
) -> Path:
    d = root / "agent" / "sessions" / f"--{task}--"
    d.mkdir(parents=True, exist_ok=True)
    header = {"type": "session", "cwd": str(root / "work" / task)}
    path = d / f"{name}.jsonl"
    with path.open(mode) as f:
        if mode == "w":
            f.write(json.dumps(header) + "\n")
        for r in records:
            f.write(json.dumps(r) + "\n")
    return path


def start(root: Path, task: str = "t", reserve: float = 0.1) -> None:
    """Run 'r' begins now, with the session files as they stand."""
    sp.append(
        root,
        {"kind": "begin", "run": "r", "task": task, "ts": NOW, "reserve": reserve,
         "offsets": sp.offsets(root, task)},
    )  # fmt: skip


def spent(root: Path) -> float:
    return sp.month_spend(root, MONTH, sp.Limits(root))[0]


def test_begin_allows_under_cap_and_records(root: Path, capsys) -> None:
    ledger(root, *run_entries("a", 3.0))
    assert sp.begin(root, "t1", False) == 0
    run = capsys.readouterr().out.strip()
    entries, _ = sp.read_ledger(root)
    assert entries[-1]["run"] == run and entries[-1]["task"] == "t1"


def test_begin_refuses_when_reserve_would_cross_cap(root: Path, capsys) -> None:
    ledger(root, *run_entries("a", 9.95))
    assert sp.begin(root, "t1", False) == sp.EXIT_OVER_CAP
    assert "cap" in capsys.readouterr().err
    assert len(sp.read_ledger(root)[0]) == 2  # nothing appended


def test_begin_allows_exactly_to_the_cap(root: Path) -> None:
    ledger(root, *run_entries("a", 9.9))
    assert sp.begin(root, "t1", True) == 0


def test_warns_at_threshold_not_below(root: Path, capsys) -> None:
    ledger(root, *run_entries("a", 7.99))
    assert sp.begin(root, "t", True) == 0
    assert "warning" not in capsys.readouterr().err
    ledger(root, *run_entries("b", 0.01))
    assert sp.begin(root, "t", True) == 0
    assert "warning" in capsys.readouterr().err


def test_check_only_writes_nothing(root: Path) -> None:
    assert sp.begin(root, "t", True) == 0
    assert not (root / "ledger.jsonl").exists()


def test_unsettled_run_counts_at_its_reserve(root: Path) -> None:
    ledger(root, *run_entries("a", None, reserve=0.25))
    assert spent(root) == pytest.approx(0.25)


def test_many_unsettled_runs_still_hit_the_cap(root: Path, capsys) -> None:
    ledger(root, *[e for i in range(100) for e in run_entries(f"r{i}", None)])
    assert spent(root) == pytest.approx(10.0)
    assert sp.begin(root, "t", False) == sp.EXIT_OVER_CAP


def test_unreadable_ledger_lines_count_at_reserve(root: Path) -> None:
    ledger(root, "{not json", "[1]", *run_entries("a", 1.0))
    assert spent(root) == pytest.approx(1.2)


def test_other_months_do_not_count(root: Path) -> None:
    ledger(root, *run_entries("old", 50.0, ts=LAST_MONTH_MS))
    assert spent(root) == 0


def test_bad_config_falls_back_to_the_defaults(root: Path) -> None:
    (root / "config.env").write_text("PIBOX_CAP_USD=nan\nPIBOX_WARN_PCT=-5\n")
    lim = sp.Limits(root)
    assert (lim.cap, lim.warn_pct) == (sp.DEFAULT_CAP, sp.DEFAULT_WARN_PCT)
    (root / "config.env").write_text('PIBOX_CAP_USD="2.5"\nPIBOX_CALL_RESERVE_USD=1\n')
    lim = sp.Limits(root)
    assert (lim.cap, lim.reserve) == (2.5, 1.0)
    ledger(root, *run_entries("a", 1.6))
    assert sp.begin(root, "t", True) == sp.EXIT_OVER_CAP


def test_host_env_cannot_raise_the_cap(root: Path, monkeypatch) -> None:
    monkeypatch.setenv("PIBOX_CAP_USD", "1000")
    assert sp.Limits(root).cap == sp.DEFAULT_CAP


def test_end_sums_reported_and_computed_costs(root: Path) -> None:
    start(root)
    session(
        root,
        message(NOW + 1, {"input": 1_000_000, "output": 0, "cost": {"total": 0.5}}),
        message(NOW + 2, {"input": 0, "output": 1_000_000, "cost": {"total": 0}}),
        message(NOW + 3, {"input": 1, "output": 1}, role="user"),
    )
    assert sp.end(root, "r") == 0
    # max(0.5 reported, 1.0 computed) + max(0, 2.0 computed)
    assert spent(root) == pytest.approx(3.0)


def test_end_charges_the_reserve_for_unknown_cost(root: Path) -> None:
    start(root)
    err = message(NOW + 4, None)
    err["message"]["stopReason"] = "error"  # type: ignore[index]
    session(
        root,
        message(NOW + 1, None),
        message(NOW + 2, {"input": 5, "output": 5}, model="not-priced"),
        message(NOW + 3, {"input": 5, "output": 5, "cost": {"total": 0.01}}, "other"),
        err,
    )
    sp.end(root, "r")
    entries, _ = sp.read_ledger(root)
    assert entries[-1]["unknown_messages"] == 2
    assert spent(root) == pytest.approx(0.21)


def test_end_counts_only_what_was_appended_since_begin(root: Path) -> None:
    old = {"input": 1_000_000, "output": 0}
    path = session(root, message(NOW - 60_000, old), message(0, old))
    start(root)
    with path.open("a") as f:
        f.write(json.dumps(message(NOW + 5, old)) + "\n")
    sp.end(root, "r")
    assert spent(root) == pytest.approx(1.0)


def test_end_ignores_other_tasks_sessions(root: Path) -> None:
    start(root, "t")
    session(root, message(NOW + 1, {"input": 1_000_000, "output": 0}), task="other")
    sp.end(root, "r")
    assert spent(root) == 0


def test_end_charges_reserve_when_sessions_shrink_or_vanish(root: Path) -> None:
    path = session(root, message(NOW, {"input": 1, "output": 1}))
    start(root)
    path.write_text("")
    sp.end(root, "r")
    assert spent(root) == pytest.approx(0.1)
    path.unlink()
    sp.append(root, {"kind": "begin", "run": "r2", "task": "t", "ts": NOW,
                     "reserve": 0.1, "offsets": {"--t--/s1.jsonl": 5}})  # fmt: skip
    sp.end(root, "r2")
    assert spent(root) == pytest.approx(0.2)


def test_prices_do_not_come_from_the_box_mount(root: Path) -> None:
    (root / "agent" / "models.json").write_text(
        json.dumps({"providers": {"v": {"models": [
            {"id": "glm", "cost": {"input": 0, "output": 0}}]}}})
    )  # fmt: skip
    start(root)
    session(root, message(NOW + 1, {"input": 1_000_000, "output": 0}))
    sp.end(root, "r")
    assert spent(root) == pytest.approx(1.0)


def test_end_with_no_sessions_costs_nothing(root: Path) -> None:
    start(root)
    assert sp.end(root, "r") == 0
    assert spent(root) == 0


def test_end_unknown_run_fails(root: Path) -> None:
    assert sp.end(root, "ghost") == 1


def test_cost_lists_models_total_and_cap(root: Path, capsys) -> None:
    ledger(root, *run_entries("a", 1.5), *run_entries("old", 9, ts=LAST_MONTH_MS))
    assert sp.main(["spend.py", "cost", str(root)]) == 0
    out = capsys.readouterr().out
    assert "glm" in out and "$1.5000" in out and "$10.00" in out
    assert "$9.0000" not in out
    old = sp.month_of(LAST_MONTH_MS)
    sp.main(["spend.py", "cost", str(root), "--month", old])
    assert "$9.0000" in capsys.readouterr().out


# RESULT.md contract and the handoff line


def work(root: Path, task: str, result: str | None, out: bool = True) -> None:
    d = root / "work" / task
    (d / "out").mkdir(parents=True) if out else d.mkdir(parents=True)
    if result is not None:
        (d / "RESULT.md").write_text(result)


GOOD = "# t\n## Summary\nDone.\n## Sources\n- https://example.com\n## Left undone\nnothing\n"


def test_contract_accepts_a_complete_result(tmp_path: Path) -> None:
    work(tmp_path, "t1", GOOD)
    assert ct.check(tmp_path, "t1") == []


def test_contract_reports_each_gap(tmp_path: Path) -> None:
    work(tmp_path, "t1", "## Summary\n\n## sources\nx\n", out=False)
    assert ct.check(tmp_path, "t1") == [
        "out/ is missing",
        "RESULT.md '## Summary' is empty",
        "RESULT.md has no '## Left undone' section",
    ]


def test_contract_ignores_headings_in_code_and_other_sections(tmp_path: Path) -> None:
    body = "## Summary\nok\n## Sources\n```\n## Left undone\n```\n# Notes\nx\n"
    work(tmp_path, "t1", body)
    assert ct.check(tmp_path, "t1") == ["RESULT.md has no '## Left undone' section"]


def test_contract_missing_result(tmp_path: Path) -> None:
    work(tmp_path, "t1", None)
    assert ct.check(tmp_path, "t1") == ["RESULT.md is missing"]


def test_contract_rejects_a_path_like_task_id(tmp_path: Path) -> None:
    with pytest.raises(ValueError):
        ct.check(tmp_path, "../x")


def test_handoff_command_and_prompt(tmp_path: Path) -> None:
    import shlex

    line = ct.command(tmp_path, "898.e2e", "Fetch it.\nIt's $1 'x'.", ct.DEFAULT_MODEL)
    argv = shlex.split(line)
    assert argv[:2] == ["agent-manager", "spawn"]
    assert argv[argv.index("--env") + 1] == "pi-box"
    assert argv[argv.index("--group") + 1] == "pi-box"
    assert argv[argv.index("--directory") + 1] == str(tmp_path / "work" / "898.e2e")
    assert argv[argv.index("--model") + 1] == "venice/glm-5-3-flash-uncensored"
    prompt = argv[argv.index("--prompt") + 1]
    assert "It's $1 'x'." in prompt
    for section in ct.SECTIONS:
        assert f"## {section}" in prompt
    assert prompt.endswith("WORKER_DONE 898.e2e.")


def test_main_check_exit_codes(tmp_path: Path) -> None:
    work(tmp_path, "t1", GOOD)
    assert ct.main(["c", "check", "t1", "--root", str(tmp_path)]) == 0
    assert ct.main(["c", "check", "nope", "--root", str(tmp_path)]) == 1
    assert ct.main(["c", "check", "../x", "--root", str(tmp_path)]) == 2


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
