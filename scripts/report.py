#!/usr/bin/env python3
#
# zen-report — chief's scheduled routines (work reports, weekly survey), run
# by a launchd agent (launchd/) so they survive chief and reboots.
#
#   zen-report tick                 deliver every due job (launchd runs this)
#   zen-report status [--json]      per job: next slot, last slot handled, result
#   zen-report run <job> [--force]  deliver one job now (refuses a handled slot
#                                   without --force)
#
# Schedule: zen.json `report.jobs` (id, at, weekday?, window_until, prompt),
# deep-merged with ~/.config/zen/config.json. A slot is due from its time until
# window_until (`next-week`: the following week's slot). Only the latest due
# daily job runs; one superseded by a later slot is recorded `stale`, one whose
# window closed undelivered `missed` (and announced once with a banner).
# Delivery is `zen-chief "[zen-report] <job> <slot>: <prompt>"`; a slot is
# `sent` only when that returns a message id, otherwise it is retried next tick.
#
# State and logs: ~/.cache/zen/report/. ZEN_REPORT_NOW (ISO local time) pins the
# clock, tests only.
# Exit: 0 ok (nothing due included), 1 a delivery failed or the lock timed out,
# 2 usage or config error.

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from datetime import datetime, time, timedelta
from pathlib import Path

HERE = Path(__file__).resolve().parent
WEEKDAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"]
LOG_CAP = 1_000_000
HANDLED = ("sent", "stale", "missed")
SLOT_FMT = "%Y-%m-%dT%H:%M"


class ConfigError(Exception):
    pass


@dataclass(frozen=True)
class Job:
    id: str
    at: time
    weekday: int | None
    until: time | None  # None = next-week
    prompt: str


def cache_dir() -> Path:
    return Path.home() / ".cache" / "zen" / "report"


def now() -> datetime:
    pinned = os.environ.get("ZEN_REPORT_NOW")
    if pinned:
        return datetime.fromisoformat(pinned).replace(tzinfo=None)
    return datetime.now().replace(microsecond=0)


def _merge(a: dict[str, object], b: dict[str, object]) -> dict[str, object]:
    out = dict(a)
    for k, v in b.items():
        cur = out.get(k)
        out[k] = _merge(cur, v) if isinstance(cur, dict) and isinstance(v, dict) else v
    return out


def _read_json(path: Path) -> dict[str, object]:
    if not path.is_file():
        return {}
    try:
        data = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        raise ConfigError(f"{path}: {e}") from None
    if not isinstance(data, dict):
        raise ConfigError(f"{path}: not a JSON object")
    return data


def _hhmm(value: object, what: str) -> time:
    try:
        if isinstance(value, str):
            t = time.fromisoformat(value)
            if t.tzinfo is None:
                return t
    except ValueError:
        pass
    raise ConfigError(f"{what}: expected HH:MM, got {value!r}")


def load_jobs() -> list[Job]:
    xdg = os.environ.get("XDG_CONFIG_HOME") or str(Path.home() / ".config")
    cfg = _merge(
        _read_json(HERE.parent / "zen.json"),
        _read_json(Path(xdg) / "zen" / "config.json"),
    )
    report = cfg.get("report")
    raw = report.get("jobs") if isinstance(report, dict) else None
    if not isinstance(raw, list) or not raw:
        raise ConfigError("zen.json: report.jobs is missing or empty")
    jobs: list[Job] = []
    for item in raw:
        if not isinstance(item, dict):
            raise ConfigError("report.jobs: each job must be an object")
        jid, prompt = item.get("id"), item.get("prompt")
        if (
            not isinstance(jid, str)
            or not jid
            or not isinstance(prompt, str)
            or not prompt
        ):
            raise ConfigError("report.jobs: each job needs a string id and prompt")
        wd = item.get("weekday")
        if wd is not None and wd not in WEEKDAYS:
            raise ConfigError(f"{jid}: weekday must be one of {', '.join(WEEKDAYS)}")
        until_raw = item.get("window_until")
        if until_raw == "next-week":
            until = None
        else:
            until = _hhmm(until_raw, f"{jid}.window_until")
        jobs.append(
            Job(
                jid,
                _hhmm(item.get("at"), f"{jid}.at"),
                WEEKDAYS.index(wd) if isinstance(wd, str) else None,
                until,
                prompt,
            )
        )
    return jobs


def latest_slot(job: Job, at: datetime) -> datetime:
    """The newest slot at or before `at`."""
    for back in range(8):
        day = (at - timedelta(days=back)).date()
        if job.weekday is not None and day.weekday() != job.weekday:
            continue
        slot = datetime.combine(day, job.at)
        if slot <= at:
            return slot
    raise ConfigError(f"{job.id}: no slot found")


def next_slot(job: Job, at: datetime) -> datetime:
    for ahead in range(8):
        day = (at + timedelta(days=ahead)).date()
        if job.weekday is not None and day.weekday() != job.weekday:
            continue
        slot = datetime.combine(day, job.at)
        if slot > at:
            return slot
    raise ConfigError(f"{job.id}: no slot found")


def window_end(job: Job, slot: datetime) -> datetime:
    if job.until is None:
        return slot + timedelta(days=7)
    end = datetime.combine(slot.date(), job.until)
    return end if end > slot else end + timedelta(days=1)


def iso(slot: datetime) -> str:
    return slot.strftime(SLOT_FMT)


def load_state(path: Path) -> dict[str, dict[str, object]]:
    try:
        data = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError):
        return {}
    if not isinstance(data, dict):
        return {}
    return {k: v for k, v in data.items() if isinstance(k, str) and isinstance(v, dict)}


def save_state(path: Path, state: dict[str, dict[str, object]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=".state-")
    with os.fdopen(fd, "w") as f:
        json.dump(state, f, indent=2)
        f.write("\n")
    os.replace(tmp, path)


def cap_file(path: Path) -> None:
    """Keep only the last half of a log over the cap, in place (launchd appends)."""
    try:
        if path.stat().st_size <= LOG_CAP:
            return
        with open(path, "r+b") as f:
            f.seek(-(LOG_CAP // 2), os.SEEK_END)
            tail = f.read()
            nl = tail.find(b"\n")
            tail = tail[nl + 1 :] if nl >= 0 else tail
            f.seek(0)
            f.write(tail)
            f.truncate()
    except OSError:
        pass


def log(line: str) -> None:
    path = cache_dir() / "report.log"
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "a") as f:
        f.write(f"{iso_sec(now())} {line}\n")
    cap_file(path)


def iso_sec(d: datetime) -> str:
    return d.strftime("%Y-%m-%dT%H:%M:%S")


def banner(job: str, slot: str) -> None:
    title, msg = "zen-report", f"{job} {slot} missed: window closed undelivered"
    if shutil.which("terminal-notifier"):
        cmd = ["terminal-notifier", "-title", title, "-message", msg]
    elif shutil.which("osascript"):
        script = f'display notification "{msg}" with title "{title}"'
        cmd = ["osascript", "-e", script]
    else:
        return
    try:
        subprocess.run(cmd, capture_output=True, check=False, timeout=30)
    except (OSError, subprocess.TimeoutExpired):
        pass


def deliver(job: Job, slot: datetime) -> tuple[str | None, str]:
    """(message id, failure reason)."""
    message = f"[zen-report] {job.id} {iso(slot)}: {job.prompt}"
    try:
        r = subprocess.run(
            ["zen-chief", message, "--json"],
            capture_output=True,
            text=True,
            check=False,
            timeout=120,
        )
    except FileNotFoundError:
        return None, "zen-chief not found"
    except subprocess.TimeoutExpired:
        return None, "zen-chief timed out"
    if r.returncode != 0:
        why = (r.stderr or r.stdout).strip().splitlines()
        return None, why[0] if why else f"zen-chief exited {r.returncode}"
    try:
        data = json.loads(r.stdout)
    except json.JSONDecodeError:
        data = None
    mid = data.get("id") if isinstance(data, dict) else None
    if mid is None or mid == "":
        return None, "zen-chief printed no message id"
    return str(mid), ""


def record(
    state: dict[str, dict[str, object]],
    job: str,
    slot: datetime,
    result: str,
    mid: str | None = None,
    reason: str = "",
) -> None:
    entry: dict[str, object] = {
        "slot": iso(slot),
        "result": result,
        "at": iso_sec(now()),
    }
    if mid:
        entry["id"] = mid
    if reason:
        entry["reason"] = reason
    state[job] = entry
    detail = f"sent {mid}" if result == "sent" else result
    log(f"{job} {iso(slot)} {detail}" + (f": {reason}" if reason else ""))


def attempt(state: dict[str, dict[str, object]], job: Job, slot: datetime) -> bool:
    mid, reason = deliver(job, slot)
    if mid is None:
        record(state, job.id, slot, "failed", reason=reason)
        return False
    record(state, job.id, slot, "sent", mid)
    return True


def tick(jobs: list[Job], state_path: Path) -> int:
    at = now()
    state = load_state(state_path)
    plan: list[tuple[Job, datetime, bool]] = []  # job, slot, window open
    for job in jobs:
        slot = latest_slot(job, at)
        prev = state.get(job.id, {})
        if prev.get("slot") == iso(slot) and prev.get("result") in HANDLED:
            continue
        plan.append((job, slot, at < window_end(job, slot)))
    newest_daily = max(
        (s for j, s, open_ in plan if open_ and j.weekday is None), default=None
    )
    latest_any = max(
        (latest_slot(j, at) for j in jobs if j.weekday is None), default=None
    )
    failed = False
    for job, slot, open_ in plan:
        prev = state.get(job.id, {})
        was_failed = prev.get("slot") == iso(slot) and prev.get("result") == "failed"
        if open_ and job.weekday is None and slot != newest_daily:
            record(state, job.id, slot, "stale", reason="superseded")
        elif open_:
            failed |= not attempt(state, job, slot)
        elif was_failed or (job.id in state and not (latest_any and latest_any > slot)):
            record(state, job.id, slot, "missed")
            banner(job.id, iso(slot))
        else:
            record(state, job.id, slot, "stale", reason="superseded")
        save_state(state_path, state)
    return 1 if failed else 0


def _describe(entry: dict[str, object]) -> str:
    result = str(entry.get("result", "?"))
    if result == "sent":
        return f"sent {entry.get('id', '?')}"
    reason = entry.get("reason")
    return f"{result}: {reason}" if result == "failed" and reason else result


def status(jobs: list[Job], state_path: Path, as_json: bool) -> int:
    at = now()
    state = load_state(state_path)
    rows = []
    for job in jobs:
        e = state.get(job.id, {})
        rows.append(
            {
                "job": job.id,
                "next": iso(next_slot(job, at)),
                "last_slot": e.get("slot"),
                "result": _describe(e) if e else None,
                "at": e.get("at"),
            }
        )
    if as_json:
        print(json.dumps(rows, indent=2))
        return 0
    for r in rows:
        print(
            f"{r['job']:<10} next {r['next']}  last {r['last_slot'] or '-'}  "
            f"{r['result'] or '-'}  {r['at'] or ''}".rstrip()
        )
    return 0


def run_one(jobs: list[Job], state_path: Path, job_id: str, force: bool) -> int:
    job = next((j for j in jobs if j.id == job_id), None)
    if job is None:
        print(f"zen-report: unknown job {job_id!r}", file=sys.stderr)
        return 2
    slot = latest_slot(job, now())
    state = load_state(state_path)
    prev = state.get(job.id, {})
    if not force and prev.get("slot") == iso(slot) and prev.get("result") in HANDLED:
        print(
            f"zen-report: {job.id} {iso(slot)} already {prev['result']} (--force)",
            file=sys.stderr,
        )
        return 1
    ok = attempt(state, job, slot)
    save_state(state_path, state)
    return 0 if ok else 1


def locked(argv: list[str]) -> int:
    """Re-run this command under the shared lock so overlapping runs never double-send."""
    cap_file(cache_dir() / "launchd.log")
    cmd = [
        str(HERE / "lock.sh"),
        "report",
        "--wait",
        "30",
        "--",
        sys.executable,
        str(Path(__file__).resolve()),
        *argv,
    ]
    r = subprocess.run(cmd, env={**os.environ, "ZEN_REPORT_LOCKED": "1"}, check=False)
    return 1 if r.returncode == 75 else r.returncode


def main(argv: list[str] | None = None) -> int:
    argv = sys.argv[1:] if argv is None else argv
    p = argparse.ArgumentParser(
        prog="zen-report", description="Scheduled chief routines."
    )
    sub = p.add_subparsers(dest="cmd", required=True)
    sub.add_parser("tick", help="deliver every due job")
    st = sub.add_parser("status", help="show every job")
    st.add_argument("--json", action="store_true")
    rn = sub.add_parser("run", help="deliver one job now")
    rn.add_argument("job")
    rn.add_argument("--force", action="store_true")
    args = p.parse_args(argv)
    try:
        jobs = load_jobs()
        state_path = cache_dir() / "state.json"
        if args.cmd == "status":
            return status(jobs, state_path, args.json)
        if not os.environ.get("ZEN_REPORT_LOCKED"):
            return locked(argv)
        if args.cmd == "run":
            return run_one(jobs, state_path, args.job, args.force)
        return tick(jobs, state_path)
    except (ConfigError, ValueError, TypeError) as e:
        print(f"zen-report: {e}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
