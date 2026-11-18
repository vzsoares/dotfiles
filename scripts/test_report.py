#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/report.py, with zen-chief and the banner stubbed on PATH.

Run with:  uv run scripts/test_report.py
"""

from __future__ import annotations

import json
import os
import shutil
import stat
import subprocess
import sys
from pathlib import Path

import pytest

SCRIPTS = Path(__file__).resolve().parent
REPORT = SCRIPTS / "report.py"
LINK = SCRIPTS.parent / "launchd" / "link"

CHIEF_STUB = """#!/usr/bin/env python3
import json, os, sys, time
d = os.environ["FAKE_BIN"]
time.sleep(float(os.environ.get("CHIEF_SLEEP", "0")))
with open(os.path.join(d, "calls"), "a") as f:
    f.write(json.dumps(sys.argv[1:]) + "\\n")
if os.path.exists(os.path.join(d, "chief_down")):
    print("no live session named 'chief'", file=sys.stderr)
    sys.exit(1)
n = sum(1 for _ in open(os.path.join(d, "calls")))
print(json.dumps({"id": f"m{n}", "session": "s1"}))
"""

BANNER_STUB = """#!/bin/sh
echo "$@" >> "$FAKE_BIN/banners"
"""

# 2026-11-18 is a Wednesday.
WED = "2026-11-18"


@pytest.fixture
def env(tmp_path: Path) -> dict[str, str]:
    fake = tmp_path / "fake-bin"
    fake.mkdir()
    for name, body in (("zen-chief", CHIEF_STUB), ("terminal-notifier", BANNER_STUB)):
        path = fake / name
        path.write_text(body)
        path.chmod(path.stat().st_mode | stat.S_IEXEC)
    home = tmp_path / "home"
    home.mkdir()
    return {
        "PATH": f"{fake}:/usr/bin:/bin",
        "HOME": str(home),
        "XDG_CONFIG_HOME": str(home / ".config"),
        "FAKE_BIN": str(fake),
        "ZEN_LOCK_POLL": "0.2",
    }


def zr(env: dict[str, str], now: str, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(REPORT), *args],
        env={**env, "ZEN_REPORT_NOW": now},
        capture_output=True,
        text=True,
        check=False,
    )


def calls(env: dict[str, str]) -> list[str]:
    path = Path(env["FAKE_BIN"]) / "calls"
    if not path.exists():
        return []
    return [json.loads(line)[0] for line in path.read_text().splitlines()]


def sent(env: dict[str, str], job: str) -> list[str]:
    return [c for c in calls(env) if c.startswith(f"[zen-report] {job} ")]


def state(env: dict[str, str]) -> dict[str, dict[str, str]]:
    return json.loads((Path(env["HOME"]) / ".cache/zen/report/state.json").read_text())


def seed(env: dict[str, str], data: dict[str, dict[str, str]]) -> None:
    path = Path(env["HOME"]) / ".cache/zen/report/state.json"
    path.parent.mkdir(parents=True)
    path.write_text(json.dumps(data))


def log_text(env: dict[str, str]) -> str:
    return (Path(env["HOME"]) / ".cache/zen/report/report.log").read_text()


def test_on_time_sends_once(env: dict[str, str]) -> None:
    assert zr(env, f"{WED}T06:00:30", "tick").returncode == 0
    msgs = sent(env, "report-06")
    assert len(msgs) == 1
    assert msgs[0].startswith(
        f"[zen-report] report-06 {WED}T06:00: Write the 06h report"
    )
    assert state(env)["report-06"]["result"] == "sent"
    assert f"{WED}T06:00 sent m" in log_text(env)
    assert zr(env, f"{WED}T06:15:00", "tick").returncode == 0
    assert len(sent(env, "report-06")) == 1


def test_wake_catch_up_sends_latest_only(env: dict[str, str]) -> None:
    seed(
        env,
        {
            "report-18": {"slot": "2026-11-17T18:00", "result": "sent", "id": "m0"},
            "survey": {"slot": "2026-11-16T09:07", "result": "sent", "id": "m0"},
        },
    )
    assert zr(env, f"{WED}T14:10:00", "tick").returncode == 0
    assert len(sent(env, "report-12")) == 1
    assert sent(env, "report-06") == []
    assert sent(env, "report-18") == []
    s = state(env)
    assert s["report-06"]["result"] == "stale"
    assert s["report-12"]["result"] == "sent"
    assert s["report-18"]["id"] == "m0"
    assert not (Path(env["FAKE_BIN"]) / "banners").exists()


def test_survey_window(env: dict[str, str]) -> None:
    seed(env, {"survey": {"slot": "2026-11-09T09:07", "result": "sent", "id": "m0"}})
    assert zr(env, f"{WED}T10:00:00", "tick").returncode == 0
    assert len(sent(env, "survey")) == 1
    zr(env, "2026-11-19T10:00:00", "tick")
    assert len(sent(env, "survey")) == 1
    zr(env, "2026-11-23T09:10:00", "tick")
    assert len(sent(env, "survey")) == 2


def test_board_sweep_is_weekly_on_monday(env: dict[str, str]) -> None:
    seed(
        env,
        {"board-sweep": {"slot": "2026-11-16T09:15", "result": "sent", "id": "m0"}},
    )
    rows = {
        row["job"]: row
        for row in json.loads(zr(env, f"{WED}T10:00:00", "status", "--json").stdout)
    }
    assert rows["board-sweep"]["next"] == "2026-11-23T09:15"
    zr(env, f"{WED}T10:00:00", "tick")
    assert sent(env, "board-sweep") == []
    zr(env, "2026-11-23T09:10:00", "tick")
    assert sent(env, "board-sweep") == []
    zr(env, "2026-11-23T09:20:00", "tick")
    zr(env, "2026-11-24T09:20:00", "tick")
    msgs = sent(env, "board-sweep")
    assert len(msgs) == 1
    assert msgs[0].startswith("[zen-report] board-sweep 2026-11-23T09:15: ")
    assert "zen-board stale --days 14 --json" in msgs[0]


def test_no_live_chief_retries_then_missed(env: dict[str, str]) -> None:
    down = Path(env["FAKE_BIN"]) / "chief_down"
    down.write_text("")
    r = zr(env, f"{WED}T06:00:30", "tick")
    assert r.returncode == 1
    assert state(env)["report-06"]["result"] == "failed"
    assert "report-06" in log_text(env) and "failed" in log_text(env)
    zr(env, f"{WED}T06:15:00", "tick")
    assert len(sent(env, "report-06")) == 2

    down.unlink()
    assert zr(env, f"{WED}T06:30:00", "tick").returncode == 0
    assert state(env)["report-06"]["result"] == "sent"


def test_window_closes_undelivered_is_missed_once(env: dict[str, str]) -> None:
    down = Path(env["FAKE_BIN"]) / "chief_down"
    down.write_text("")
    zr(env, f"{WED}T11:45:00", "tick")
    assert state(env)["report-06"]["result"] == "failed"
    zr(env, f"{WED}T12:00:00", "tick")
    assert state(env)["report-06"]["result"] == "missed"
    banners = (Path(env["FAKE_BIN"]) / "banners").read_text().splitlines()
    assert len(banners) == 1 and "report-06" in banners[0]
    zr(env, f"{WED}T12:15:00", "tick")
    assert len((Path(env["FAKE_BIN"]) / "banners").read_text().splitlines()) == 1


def test_concurrent_ticks_send_once(env: dict[str, str]) -> None:
    slow = {**env, "CHIEF_SLEEP": "1", "ZEN_REPORT_NOW": f"{WED}T06:00:30"}
    procs = [
        subprocess.Popen(
            [sys.executable, str(REPORT), "tick"],
            env=slow,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        for _ in range(2)
    ]
    assert [p.wait(timeout=60) for p in procs] == [0, 0]
    assert len(sent(env, "report-06")) == 1


def test_corrupt_state_sends_current_window_only(env: dict[str, str]) -> None:
    path = Path(env["HOME"]) / ".cache/zen/report/state.json"
    path.parent.mkdir(parents=True)
    path.write_text("{not json")
    assert zr(env, f"{WED}T14:10:00", "tick").returncode == 0
    assert len(sent(env, "report-12")) == 1
    assert sent(env, "report-06") == []
    assert sent(env, "report-18") == []
    assert not (Path(env["FAKE_BIN"]) / "banners").exists()
    assert state(env)["report-12"]["result"] == "sent"


def test_run_refuses_handled_slot_without_force(env: dict[str, str]) -> None:
    now = f"{WED}T12:30:00"
    assert zr(env, now, "run", "report-12").returncode == 0
    assert zr(env, now, "run", "report-12").returncode == 1
    assert zr(env, now, "run", "report-12", "--force").returncode == 0
    assert len(sent(env, "report-12")) == 2
    assert zr(env, now, "run", "nope").returncode == 2


def test_status_json(env: dict[str, str]) -> None:
    zr(env, f"{WED}T06:00:30", "tick")
    r = zr(env, f"{WED}T06:05:00", "status", "--json")
    rows = {row["job"]: row for row in json.loads(r.stdout)}
    assert rows["report-06"]["result"].startswith("sent m")
    assert rows["report-06"]["next"] == "2026-11-19T06:00"
    assert rows["survey"]["next"] == "2026-11-23T09:07"


def test_bad_config_exits_2(env: dict[str, str]) -> None:
    cfg = Path(env["XDG_CONFIG_HOME"]) / "zen" / "config.json"
    cfg.parent.mkdir(parents=True)
    cfg.write_text(
        json.dumps({"report": {"jobs": [{"id": "x", "at": "25:99", "prompt": "p"}]}})
    )
    assert zr(env, f"{WED}T06:00:00", "tick").returncode == 2


def test_log_capped(env: dict[str, str]) -> None:
    log = Path(env["HOME"]) / ".cache/zen/report/launchd.log"
    log.parent.mkdir(parents=True)
    log.write_text("x" * 99 + "\n" * 1 + ("y" * 99 + "\n") * 12_000)
    zr(env, f"{WED}T06:00:30", "tick")
    assert 400_000 < log.stat().st_size <= 600_000
    assert log.read_text().startswith("y")


def test_plist_renders_clean(tmp_path: Path) -> None:
    out = tmp_path / "out"
    r = subprocess.run(
        ["bash", str(LINK), "--render-only", str(out)],
        env={**os.environ, "HOME": str(tmp_path / "h")},
        capture_output=True,
        text=True,
        check=False,
    )
    assert r.returncode == 0, r.stderr
    plist = out / "local.zen.report.plist"
    text = plist.read_text()
    assert "~" not in text and "$HOME" not in text and "@HOME@" not in text
    assert f"{tmp_path}/h/.local/bin/zen-report" in text
    if sys.platform == "darwin" and shutil.which("plutil"):
        lint = subprocess.run(
            ["plutil", "-lint", str(plist)], capture_output=True, check=False
        )
        assert lint.returncode == 0


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q", *sys.argv[1:]]))
