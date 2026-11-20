#!/usr/bin/env python3
"""pi-box monthly spend ledger and cap. Stdlib only; never calls a model.

  spend.py begin ROOT TASK [--check-only]   reserve a call; exit 4 over the cap
  spend.py end ROOT RUN                     settle a run from the pi sessions
  spend.py cost ROOT [--month YYYY-MM]      spend per model, from the ledger

The cap is a gate at start, not a live limit; the Venice prepaid credit is the
hard stop. The ledger is ROOT/ledger.jsonl, one JSON object per line. A run is a `begin`
line (carrying its reserve) and, when it finishes, an `end` line with the real
cost. A run with no `end` (killed wrapper) and any unreadable line count at
the reserve, so the cap holds when a cost is unknown.
"""

from __future__ import annotations

import fcntl
import json
import sys
import time
import uuid
from pathlib import Path

DEFAULT_CAP = 10.0
DEFAULT_WARN_PCT = 80.0
DEFAULT_RESERVE = 0.10
EXIT_OVER_CAP = 4


def num(value: object) -> float | None:
    if isinstance(value, bool) or not isinstance(value, int | float):
        return None
    return float(value)


def config_values(root: Path) -> dict[str, str]:
    """Last KEY=value per key in ROOT/config.env, quotes stripped."""
    values: dict[str, str] = {}
    try:
        text = (root / "config.env").read_text()
    except OSError:
        return values
    for line in text.splitlines():
        key, sep, val = line.strip().partition("=")
        if sep and key.isidentifier():
            values[key] = val.strip().strip("\"'")
    return values


def setting(values: dict[str, str], key: str, default: float) -> float:
    """A finite, non-negative number from config; anything else is the default."""
    try:
        parsed = float(values[key])
    except (KeyError, ValueError):
        return default
    return parsed if 0 <= parsed < float("inf") else default


class Limits:
    def __init__(self, root: Path) -> None:
        values = config_values(root)
        self.cap = setting(values, "PIBOX_CAP_USD", DEFAULT_CAP)
        self.warn_pct = setting(values, "PIBOX_WARN_PCT", DEFAULT_WARN_PCT)
        self.reserve = setting(values, "PIBOX_CALL_RESERVE_USD", DEFAULT_RESERVE)


def month_of(ms: float) -> str:
    return time.strftime("%Y-%m", time.localtime(ms / 1000))


def read_ledger(root: Path) -> tuple[list[dict[str, object]], int]:
    """Parsed entries and the count of unreadable lines."""
    entries: list[dict[str, object]] = []
    bad = 0
    try:
        text = (root / "ledger.jsonl").read_text()
    except OSError:
        return entries, 0
    for line in text.splitlines():
        if not line.strip():
            continue
        try:
            entry = json.loads(line)
        except ValueError:
            bad += 1
            continue
        if isinstance(entry, dict):
            entries.append(entry)
        else:
            bad += 1
    return entries, bad


def month_runs(
    entries: list[dict[str, object]], month: str, reserve: float
) -> dict[str, dict[str, float]]:
    """Run id -> {model: cost} for runs begun in `month`; open runs at reserve."""
    begun: dict[str, float] = {}
    settled: dict[str, dict[str, float]] = {}
    for e in entries:
        run = e.get("run")
        if not isinstance(run, str):
            continue
        if e.get("kind") == "begin":
            r = num(e.get("reserve"))
            begun[run] = r if r is not None and r >= 0 else reserve
        elif e.get("kind") == "end":
            models = e.get("models")
            per_model: dict[str, float] = {}
            if isinstance(models, dict):
                for name, cost in models.items():
                    c = num(cost)
                    per_model[str(name)] = c if c is not None and c >= 0 else reserve
            settled[run] = per_model
    starts = {
        str(e["run"]): num(e.get("ts"))
        for e in entries
        if e.get("kind") == "begin" and isinstance(e.get("run"), str)
    }
    out: dict[str, dict[str, float]] = {}
    for run, held in begun.items():
        ts = starts.get(run)
        if ts is None or month_of(ts) != month:
            continue
        out[run] = settled.get(run, {"(unsettled)": held})
    return out


def month_spend(
    root: Path, month: str, limits: Limits
) -> tuple[float, dict[str, float]]:
    entries, bad = read_ledger(root)
    by_model: dict[str, float] = {}
    for per_model in month_runs(entries, month, limits.reserve).values():
        for name, cost in per_model.items():
            by_model[name] = by_model.get(name, 0.0) + cost
    if bad:
        by_model["(unreadable ledger lines)"] = bad * limits.reserve
    return sum(by_model.values()), by_model


def append(root: Path, entry: dict[str, object]) -> None:
    with (root / "ledger.jsonl").open("a") as f:
        f.write(json.dumps(entry, separators=(",", ":")) + "\n")


def begin(root: Path, task: str, check_only: bool) -> int:
    limits = Limits(root)
    month = month_of(time.time() * 1000)
    root.mkdir(parents=True, exist_ok=True)
    with (root / ".ledger.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        spent, _ = month_spend(root, month, limits)
        if spent + limits.reserve > limits.cap:
            print(
                f"pi-box: spend cap reached: ${spent:.4f} this month + "
                f"${limits.reserve:.2f} reserve is over ${limits.cap:.2f}",
                file=sys.stderr,
            )
            return EXIT_OVER_CAP
        if spent >= limits.cap * limits.warn_pct / 100:
            print(
                f"pi-box: warning: ${spent:.4f} of ${limits.cap:.2f} spent this month",
                file=sys.stderr,
            )
        if check_only:
            return 0
        run = uuid.uuid4().hex[:12]
        append(
            root,
            {
                "kind": "begin",
                "run": run,
                "task": task,
                "ts": int(time.time() * 1000),
                "reserve": limits.reserve,
                "offsets": offsets(root, task),
            },
        )
    print(run)
    return 0


PRICES_FILE = Path(__file__).resolve().parent / "agent" / "models.json"


def model_prices() -> dict[str, tuple[float, float]]:
    """Model id -> (input, output) USD per 1M tokens.

    Read from the repo copy: the box can rewrite ~/pi-box/agent/models.json.
    """
    prices: dict[str, tuple[float, float]] = {}
    try:
        data = json.loads(PRICES_FILE.read_text())
    except (OSError, ValueError):
        return prices
    providers = data.get("providers") if isinstance(data, dict) else None
    if not isinstance(providers, dict):
        return prices
    for provider in providers.values():
        models = provider.get("models") if isinstance(provider, dict) else None
        for m in models if isinstance(models, list) else []:
            cost = m.get("cost") if isinstance(m, dict) else None
            if isinstance(cost, dict) and isinstance(m.get("id"), str):
                cin, cout = num(cost.get("input")), num(cost.get("output"))
                if cin is not None and cout is not None:
                    prices[str(m["id"])] = (cin, cout)
    return prices


def message_cost(
    message: dict[str, object], prices: dict[str, tuple[float, float]]
) -> float | None:
    """USD for one assistant message, or None when it cannot be known."""
    usage = message.get("usage")
    if not isinstance(usage, dict):
        return None
    reported: float | None = None
    cost = usage.get("cost")
    if isinstance(cost, dict):
        reported = num(cost.get("total"))
    tokens_in = sum(
        num(usage.get(k)) or 0.0 for k in ("input", "cacheRead", "cacheWrite")
    )
    tokens_out = num(usage.get("output"))
    price = prices.get(str(message.get("model")))
    computed = None
    if price is not None and tokens_out is not None:
        computed = (tokens_in * price[0] + tokens_out * price[1]) / 1_000_000
    known = [c for c in (reported, computed) if c is not None]
    return max(known) if known else None


def task_sessions(root: Path, task: str) -> dict[str, Path]:
    """Relative path -> session file whose recorded cwd is this task's folder."""
    base = root / "agent" / "sessions"
    work = root / "work" / task
    found: dict[str, Path] = {}
    for path in sorted(base.rglob("*.jsonl")) if base.is_dir() else []:
        try:
            with path.open() as f:
                header = json.loads(f.readline())
        except (OSError, ValueError):
            continue
        cwd = header.get("cwd") if isinstance(header, dict) else None
        if isinstance(cwd, str) and Path(cwd).is_relative_to(work):
            found[str(path.relative_to(base))] = path
    return found


def offsets(root: Path, task: str) -> dict[str, int]:
    out: dict[str, int] = {}
    for rel, path in task_sessions(root, task).items():
        try:
            out[rel] = path.stat().st_size
        except OSError:
            continue
    return out


def session_messages(
    root: Path, task: str, before: dict[str, int]
) -> tuple[list[dict[str, object]], bool]:
    """Assistant messages appended since `before`; True if a file shrank or vanished."""
    found = task_sessions(root, task)
    tampered = any(rel not in found for rel in before)
    messages: list[dict[str, object]] = []
    for rel, path in found.items():
        start = before.get(rel, 0)
        try:
            with path.open("rb") as f:
                f.seek(0, 2)
                if f.tell() < start:
                    tampered = True
                    continue
                f.seek(start)
                lines = f.read().decode(errors="replace").splitlines()
        except OSError:
            tampered = True
            continue
        for line in lines:
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            msg = rec.get("message") if isinstance(rec, dict) else None
            if isinstance(msg, dict) and msg.get("role") == "assistant":
                messages.append(msg)
    return messages, tampered


def end(root: Path, run: str) -> int:
    limits = Limits(root)
    entries, _ = read_ledger(root)
    begun = next(
        (e for e in entries if e.get("kind") == "begin" and e.get("run") == run), None
    )
    if begun is None:
        print(f"pi-box: no ledger entry for run {run}", file=sys.stderr)
        return 1
    task = str(begun.get("task", ""))
    before = begun.get("offsets")
    messages, tampered = session_messages(
        root,
        task,
        {k: v for k, v in before.items() if isinstance(v, int)}
        if isinstance(before, dict)
        else {},
    )
    prices = model_prices()
    models: dict[str, float] = {}
    unknown = 0
    for msg in messages:
        cost = message_cost(msg, prices)
        if cost is None and msg.get("stopReason") in ("error", "aborted"):
            cost = 0.0
        if cost is None:
            cost = limits.reserve
            unknown += 1
        name = str(msg.get("model") or "(unknown model)")
        models[name] = models.get(name, 0.0) + cost
    if tampered:
        models["(sessions truncated)"] = limits.reserve
    append(
        root,
        {
            "kind": "end",
            "run": run,
            "ts": int(time.time() * 1000),
            "models": models,
            "unknown_messages": unknown,
        },
    )
    spent, _ = month_spend(root, month_of(num(begun.get("ts")) or 0), limits)
    print(
        f"pi-box: run ${sum(models.values()):.4f}; "
        f"month ${spent:.4f} of ${limits.cap:.2f}",
        file=sys.stderr,
    )
    return 0


def cost(root: Path, month: str) -> int:
    limits = Limits(root)
    spent, by_model = month_spend(root, month, limits)
    print(f"pi-box spend {month}")
    for name in sorted(by_model):
        print(f"  {name:<40} ${by_model[name]:.4f}")
    print(f"  {'total':<40} ${spent:.4f}")
    print(
        f"  {'cap':<40} ${limits.cap:.2f} (remaining ${max(limits.cap - spent, 0):.4f})"
    )
    return 0


def main(argv: list[str]) -> int:
    if len(argv) >= 4 and argv[1] == "begin":
        return begin(Path(argv[2]), argv[3], "--check-only" in argv[4:])
    if len(argv) == 4 and argv[1] == "end":
        return end(Path(argv[2]), argv[3])
    if len(argv) >= 3 and argv[1] == "cost":
        rest = argv[3:]
        month = month_of(time.time() * 1000)
        if rest[:1] == ["--month"]:
            month = rest[1] if len(rest) == 2 else month
            rest = rest[2:] if len(rest) == 2 else rest[1:]
        if not rest:
            return cost(Path(argv[2]), month)
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
