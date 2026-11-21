#!/usr/bin/env python3
#
# zen-rotate — restart a long-running agent session in place from a handoff
# note it writes itself. The agent-manager session id never changes: the
# conversation is cleared with a typed /clear and resumed from the note.
#
#   zen-rotate tick                 one step per enabled role and per cycle in
#                                   flight (zen-resume-watch runs it each minute)
#   zen-rotate status [--json]      per role: session, age, due in, phase,
#                                   block reason, last rotation, last cancel,
#                                   last note
#   zen-rotate now <role>           start a cycle now, skipping the age check
#   zen-rotate now --session <id> [--saved <preset>]
#                                   the same for any other session
#   zen-rotate check-note <path>    validate a handoff note, set it to 0600
#   zen-rotate resume <role>        lift a pause and any backoff
#
# Config: zen.json `rotate` (hours, defer_minutes, roles.<name>.enabled,
# roles.<name>.quiet_minutes), deep-merged with ~/.config/zen/config.json.
# A role is the one live session with that name in group `chief`; its saved
# prompt has the same name.
#
# Files: ~/.cache/zen/rotate/ (state.json, lock, rotate.log,
# clearing-<session id>), notes in ~/.cache/zen/handoff/<role>/.
# Env: ZEN_ROTATE_NOW pins the clock, ZEN_ROTATE_STATE moves the state dir,
# ZEN_ROTATE_CLEAR_WAIT / ZEN_ROTATE_CLEAR_POLL time the clear check (seconds).
#
# Exit: 0 ok, 1 refused, blocked or failed, 2 usage.

from __future__ import annotations

import argparse
import contextlib
import importlib.util
import json
import os
import re
import subprocess
import sys
import time
from collections.abc import Iterator
from dataclasses import asdict, dataclass, field, fields
from datetime import UTC, datetime
from pathlib import Path
from types import ModuleType

HERE = Path(__file__).resolve().parent
GROUP = "chief"
PROTECTED = ("chief", "orchestrator")
TMUX_SOCKET = "agentmgr"
REST_STATUSES = ("finished", "idle")
SENDER = "zen-rotate"

READY = "HANDOFF_READY"
DEFER = "HANDOFF_DEFER"

NOTE_MAX = 30_000
NOTE_ASK = 12_000
NOTES_KEPT = 30
MAX_REQUESTS = 3
MAX_MISSES = 2
REQUEST_TIMEOUT = 20 * 60
CLEAR_BACKOFF = 60 * 60
PAUSE_AFTER = 3
PANE_TAIL = 15

SECTIONS: tuple[tuple[str, str], ...] = (
    (
        "State",
        "every task in flight: id, title, stage, owner session id, repo and branch, "
        + "next step",
    ),
    (
        "Waiters and timers",
        "each background run you stopped, with the exact command to re-run it; "
        + "each timer, with its time and what it does",
    ),
    (
        "Open asks and approvals",
        "inbox ask ids still open, held answers, gates, approved prod actions "
        + "not yet run (id, action, repo, sha or command)",
    ),
    (
        "Standing rules",
        "what the user said this session that still applies, with its end date "
        + "when it has one (slots, model defaults, paused projects)",
    ),
    (
        "Mistakes not to repeat",
        "each correction you received this session and the right behaviour",
    ),
    (
        "Owed",
        "replies, notes and reports still owed, and questions asked of the user "
        + "in chat with what each answer triggers",
    ),
    ("Files", "state files you keep (queue file, state.md, prompt dirs)"),
    (
        "First actions",
        "a numbered list for the fresh conversation, starting with re-arming the "
        + "waiters and re-creating the timers",
    ),
)

ROLE_EXTRA = {
    "chief": (
        "As chief: do not copy the board (the fresh conversation re-reads it "
        "with `zen-board check`); list pending `go #<id>` entries and the "
        "state of the watchers (cockpit, resume-watch)."
    ),
    "orchestrator": (
        "As the orchestrator: list the queue in order with tiers, every live "
        "worker (session id, task, repo, worktree, phase, last event), and "
        "the pending merges and pending pushes per repo."
    ),
}


class RotateError(Exception):
    pass


def _load_sessions() -> ModuleType:
    path = HERE / "lib" / "sessions.py"
    spec = importlib.util.spec_from_file_location("zen_sessions", path)
    if spec is None or spec.loader is None:
        raise ImportError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules["zen_sessions"] = module
    spec.loader.exec_module(module)
    return module


_sessions = _load_sessions()

Session = dict[str, object]


# --------------------------------------------------------------------------- #
# Clock, paths, config                                                         #
# --------------------------------------------------------------------------- #


def parse_stamp(value: str) -> float | None:
    try:
        return float(value)
    except ValueError:
        pass
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError:
        return None
    return parsed.timestamp()


def now() -> float:
    pinned = os.environ.get("ZEN_ROTATE_NOW")
    if pinned:
        value = parse_stamp(pinned)
        if value is None:
            raise RotateError(f"ZEN_ROTATE_NOW: bad time {pinned!r}")
        return value
    return time.time()


def state_dir() -> Path:
    override = os.environ.get("ZEN_ROTATE_STATE")
    return Path(override) if override else Path.home() / ".cache" / "zen" / "rotate"


def handoff_dir(key: str) -> Path:
    return Path.home() / ".cache" / "zen" / "handoff" / key.replace(":", "-")


def marker_path(session_id: str) -> Path:
    return state_dir() / f"clearing-{session_id}"


@dataclass
class RoleConfig:
    enabled: bool
    quiet_minutes: float


@dataclass
class Config:
    hours: float
    defer_minutes: float
    roles: dict[str, RoleConfig]


def _read_json(path: Path) -> dict[str, object]:
    if not path.exists():
        return {}
    try:
        data = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        raise RotateError(f"{path}: {e}") from None
    if not isinstance(data, dict):
        raise RotateError(f"{path}: not a JSON object")
    return data


def _merge(base: dict[str, object], over: dict[str, object]) -> dict[str, object]:
    out = dict(base)
    for key, value in over.items():
        current = out.get(key)
        if isinstance(current, dict) and isinstance(value, dict):
            out[key] = _merge(current, value)
        else:
            out[key] = value
    return out


def _number(value: object, what: str, minimum: float) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise RotateError(f"{what}: expected a number, got {value!r}")
    if value < minimum:
        raise RotateError(f"{what}: must be at least {minimum:g}, got {value!r}")
    return float(value)


def load_config() -> Config:
    xdg = os.environ.get("XDG_CONFIG_HOME") or str(Path.home() / ".config")
    merged = _merge(
        _read_json(HERE.parent / "zen.json"),
        _read_json(Path(xdg) / "zen" / "config.json"),
    )
    raw = merged.get("rotate")
    if not isinstance(raw, dict):
        raise RotateError("zen.json: rotate is missing")
    hours = _number(raw.get("hours", 6), "rotate.hours", 0.01)
    defer = _number(raw.get("defer_minutes", 30), "rotate.defer_minutes", 0)
    raw_roles = raw.get("roles")
    if not isinstance(raw_roles, dict):
        raise RotateError("zen.json: rotate.roles is missing")
    roles: dict[str, RoleConfig] = {}
    for name, entry in raw_roles.items():
        if not isinstance(entry, dict):
            raise RotateError(f"rotate.roles.{name}: expected an object")
        enabled = entry.get("enabled", False)
        if not isinstance(enabled, bool):
            raise RotateError(f"rotate.roles.{name}.enabled: expected true or false")
        quiet = _number(
            entry.get("quiet_minutes", 0), f"rotate.roles.{name}.quiet_minutes", 0
        )
        roles[str(name)] = RoleConfig(enabled=enabled, quiet_minutes=quiet)
    return Config(hours=hours, defer_minutes=defer, roles=roles)


# --------------------------------------------------------------------------- #
# State and lock                                                               #
# --------------------------------------------------------------------------- #


@dataclass
class Cycle:
    phase: str = "idle"
    session: str = ""
    conversation: str = ""
    saved: str = ""
    note: str = ""
    requests: int = 0
    misses: int = 0
    requested_at: float = 0
    base_uuid: str = ""
    age: float = 0
    backoff_until: float = 0
    cancels: int = 0
    paused: bool = False
    block: str = ""
    last_rotation: float = 0
    last_note: str = ""
    last_cancel: str = ""

    def in_flight(self) -> bool:
        return self.phase != "idle"


@dataclass
class State:
    cycles: dict[str, Cycle] = field(default_factory=dict)

    def cycle(self, key: str) -> Cycle:
        return self.cycles.setdefault(key, Cycle())


def state_path() -> Path:
    return state_dir() / "state.json"


def load_state() -> State:
    raw = _read_json(state_path())
    known = {f.name for f in fields(Cycle)}
    state = State()
    for key, entry in raw.items():
        if isinstance(entry, dict):
            state.cycles[key] = Cycle(**{k: v for k, v in entry.items() if k in known})
    return state


def save_state(state: State) -> None:
    path = state_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(".tmp")
    tmp.write_text(
        json.dumps({k: asdict(c) for k, c in state.cycles.items()}, indent=1)
    )
    os.replace(tmp, path)


def _pid_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


@contextlib.contextmanager
def lock() -> Iterator[bool]:
    """Yield True when this process holds the lock; a dead owner is taken over."""
    path = state_dir() / "lock"
    path.parent.mkdir(parents=True, exist_ok=True)
    held = False
    for _ in range(2):
        try:
            fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        except FileExistsError:
            try:
                owner = int(path.read_text().strip() or "0")
            except (OSError, ValueError):
                owner = 0
            if owner and _pid_alive(owner):
                break
            path.unlink(missing_ok=True)
            continue
        with os.fdopen(fd, "w") as f:
            f.write(str(os.getpid()))
        held = True
        break
    try:
        yield held
    finally:
        if held:
            path.unlink(missing_ok=True)


def log(message: str) -> None:
    line = f"{datetime.fromtimestamp(now()).strftime('%H:%M')} {message}"
    print(line, flush=True)
    path = state_dir() / "rotate.log"
    path.parent.mkdir(parents=True, exist_ok=True)
    day = datetime.fromtimestamp(now()).strftime("%Y-%m-%d")
    with path.open("a") as f:
        f.write(f"{day} {line}\n")


# --------------------------------------------------------------------------- #
# External commands                                                            #
# --------------------------------------------------------------------------- #


def run(*argv: str) -> tuple[int, str]:
    """(exit code, stdout); 127 when the binary is missing."""
    try:
        r = subprocess.run(list(argv), capture_output=True, text=True, check=False)
    except FileNotFoundError:
        return 127, ""
    return r.returncode, r.stdout


def capture_pane(session_id: str) -> str | None:
    code, out = run(
        "tmux", "-L", TMUX_SOCKET, "capture-pane", "-p", "-t", f"am_{session_id}"
    )
    return out if code == 0 else None


def type_clear(session_id: str) -> bool:
    code, _ = run(
        "tmux",
        "-L",
        TMUX_SOCKET,
        "send-keys",
        "-t",
        f"am_{session_id}",
        "/clear",
        "Enter",
    )
    return code == 0


def send(session_id: str, message: str, saved: str = "") -> bool:
    """Queue a message; agent-manager types it once the session is at rest."""
    argv = ["agent-manager", "send", session_id]
    if saved:
        argv += ["--saved", saved]
    argv.append(message)
    try:
        r = subprocess.run(
            [*argv, "--from", SENDER], capture_output=True, text=True, check=False
        )
        # A caller that has a session is refused a sender name; a failed send
        # delivers nothing, so the plain retry cannot send twice.
        if r.returncode != 0:
            r = subprocess.run(argv, capture_output=True, text=True, check=False)
    except FileNotFoundError:
        return False
    return r.returncode == 0


def _is_rule(line: str) -> bool:
    line = line.strip()
    return bool(line) and not line.strip("─")


def prompt_state(screen: str) -> str:
    """ "empty", "text" or "none" for the input box: a ❯ line between two ─ rules.

    A dialog or any other screen has no such box and is "none".
    """
    lines = screen.replace(" ", " ").splitlines()
    for i in range(len(lines) - 1, -1, -1):
        if not lines[i].startswith("❯"):
            continue
        above, below = i - 1, i + 1
        while above >= 0 and not lines[above].strip():
            above -= 1
        while below < len(lines) and not lines[below].strip():
            below += 1
        if above < 0 or below >= len(lines):
            continue
        if _is_rule(lines[above]) and _is_rule(lines[below]):
            return "text" if lines[i][1:].strip() else "empty"
    return "none"


def open_approvals() -> str:
    """ "" when no approved prod action is open, else why the restart waits."""
    code, out = run("zen-inbox", "approval", "open")
    if code == 1:
        return ""
    if code == 0:
        ids = ", ".join(line.split()[0] for line in out.splitlines() if line.strip())
        return f"approved prod action open ({ids})"
    return f"zen-inbox approval open failed (exit {code})"


# --------------------------------------------------------------------------- #
# Transcripts                                                                  #
# --------------------------------------------------------------------------- #


def transcript(session: Session) -> Path | None:
    path = _sessions.transcript_path(session)
    return path if isinstance(path, Path) and path.exists() else None


def first_timestamp(path: Path) -> float | None:
    with path.open(errors="replace") as f:
        for line in f:
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                continue
            stamp = rec.get("timestamp") if isinstance(rec, dict) else None
            if isinstance(stamp, str):
                value = parse_stamp(stamp)
                if value is not None:
                    return value
    return None


def session_age(session: Session) -> float | None:
    path = transcript(session)
    started = first_timestamp(path) if path else None
    return None if started is None else max(0.0, now() - started)


def end_turns(path: Path) -> list[tuple[str, str]]:
    """(uuid, text) of every end_turn assistant message, oldest first."""
    recs = _sessions.records(path)
    texts: dict[str, list[str]] = {}
    out: list[tuple[str, str]] = []
    for i, rec in enumerate(recs):
        msg = _sessions.assistant_message(rec)
        if msg is None:
            continue
        msg_id = str(msg.get("id") or f"rec-{i}")
        texts.setdefault(msg_id, []).append(_sessions.text_of(msg))
        if msg.get("stop_reason") == "end_turn":
            text = "\n".join(p for p in texts[msg_id] if p).strip()
            out.append((str(rec.get("uuid", i)), text))
    return out


def marker_of(text: str) -> tuple[str, str]:
    """(READY | DEFER | "", defer reason) from a turn's last non-empty line."""
    lines = [line.strip().strip("`*") for line in text.splitlines() if line.strip()]
    if not lines:
        return "", ""
    last = lines[-1].strip()
    if last == READY:
        return READY, ""
    if last == DEFER or last.startswith(DEFER + " "):
        return DEFER, last[len(DEFER) :].strip() or "no reason given"
    return "", ""


def turns_since(path: Path, base_uuid: str) -> list[tuple[str, str]] | None:
    """End turns after base_uuid; None when base_uuid is no longer there."""
    turns = end_turns(path)
    if not base_uuid:
        return turns
    for i, (uuid, _) in enumerate(turns):
        if uuid == base_uuid:
            return turns[i + 1 :]
    return None


# --------------------------------------------------------------------------- #
# Handoff notes                                                                #
# --------------------------------------------------------------------------- #

HEADING_RE = re.compile(r"^(#{1,2})\s+(.*?)\s*$")


def note_sections(text: str) -> dict[str, str]:
    sections: dict[str, str] = {}
    current: str | None = None
    fenced = False
    for line in text.splitlines():
        if line.lstrip().startswith(("```", "~~~")):
            fenced = not fenced
        match = None if fenced else HEADING_RE.match(line)
        if match:
            current = match.group(2) if match.group(1) == "##" else None
            if current is not None:
                sections.setdefault(current, "")
            continue
        if current is not None:
            sections[current] += line + "\n"
    return sections


def gitleaks_problem(path: Path) -> str:
    config = HERE.parent / "git" / "gitleaks.toml"
    code, _ = run(
        "gitleaks",
        "dir",
        str(path),
        "--config",
        str(config),
        "--redact",
        "--no-banner",
        "--exit-code",
        "3",
    )
    if code == 0:
        return ""
    if code == 3:
        return "gitleaks found a secret; name it by where it lives instead"
    if code == 127:
        return "gitleaks not found (brew install gitleaks / sudo pacman -S gitleaks)"
    return f"gitleaks exited {code}"


def check_note(path: Path) -> list[str]:
    """Problems with a handoff note; empty when it is valid. Sets it to 0600."""
    if not path.is_file():
        return [f"no note at {path}"]
    os.chmod(path, 0o600)
    text = path.read_text(errors="replace")
    problems: list[str] = []
    if len(text) > NOTE_MAX:
        problems.append(f"{len(text)} characters, over the {NOTE_MAX} limit")
    sections = note_sections(text)
    for title, _ in SECTIONS:
        if title not in sections:
            problems.append(f"missing heading '## {title}'")
        elif not sections[title].strip():
            problems.append(f"section '## {title}' is empty (write none)")
    leak = gitleaks_problem(path)
    if leak:
        problems.append(leak)
    return problems


def new_note_path(key: str) -> Path:
    directory = handoff_dir(key)
    directory.mkdir(parents=True, exist_ok=True)
    os.chmod(directory, 0o700)
    stamp = datetime.fromtimestamp(now()).strftime("%Y%m%d-%H%M%S")
    return directory / f"{stamp}.md"


def prune_notes(key: str) -> None:
    notes = sorted(handoff_dir(key).glob("*.md"))
    for old in notes[:-NOTES_KEPT]:
        old.unlink(missing_ok=True)


# --------------------------------------------------------------------------- #
# Messages                                                                     #
# --------------------------------------------------------------------------- #


def request_text(key: str, note: Path) -> str:
    headings = "\n".join(f"`## {title}`: {holds}" for title, holds in SECTIONS)
    intro = (
        "[zen-rotate] Restart request. This conversation is about to be cleared "
        "to drop its context; a fresh one starts from a handoff note that you "
        "write now, on your own."
    )
    where = (
        f"Write the note to {note} (the directory exists). Keep it under "
        f"{NOTE_ASK} characters (hard limit {NOTE_MAX}). No secrets: name tokens, "
        "keys and passwords by where they live, never copy them. Use exactly "
        'these headings, in this order; "none" is a valid body, an empty section '
        "is not:"
    )
    before = (
        "Before you answer:\n"
        "1. Stop every background run you started (TaskStop) and delete every "
        "session timer (CronDelete, a self-paced wakeup). List each under "
        '"Waiters and timers".\n'
        "2. Do nothing else: no board writes, no messages, no new work."
    )
    defer = (
        "Defer instead when a background run that is not a waiter is still "
        "running, a multi-step action is half done (a merge, a release, an "
        "approved prod action), or the user is mid-conversation with you. Then "
        "change nothing (stop no run, delete no timer, write no note) and end "
        f"your turn with the single line {DEFER} <reason>"
    )
    parts = [intro, where, headings, ROLE_EXTRA.get(key, ""), before]
    parts += [f"End your turn with the single line {READY}", defer]
    return "\n\n".join(part for part in parts if part)


def rerequest_text(note: str, gap: str, number: int) -> str:
    return (
        f"[zen-rotate] Not restarted yet (request {number} of {MAX_REQUESTS}): "
        f"{gap}. Fix that, bring the note at {note} up to date, and end your "
        f"turn with the single line {READY} (or {DEFER} <reason>)."
    )


def resume_text(note: str) -> str:
    return (
        "[zen-rotate] You were restarted in place to drop context; your "
        f"session id is unchanged. Read the handoff note at {note} and do its "
        "First actions in order. Then handle any message that arrived in this "
        "conversation before this one."
    )


def cancel_text(reason: str, note: str) -> str:
    rearm = (
        f"Re-arm the waiters and timers listed in {note} and carry on."
        if note and Path(note).is_file()
        else "Re-arm any waiter or timer you stopped for it and carry on."
    )
    return f"[zen-rotate] No restart this time ({reason}). {rearm}"


def iso(epoch: float) -> str:
    return datetime.fromtimestamp(epoch, UTC).isoformat(timespec="seconds")


def fmt_span(seconds: float) -> str:
    minutes = int(seconds // 60)
    return f"{minutes // 60}h{minutes % 60:02d}m" if minutes >= 60 else f"{minutes}m"


# --------------------------------------------------------------------------- #
# The cycle                                                                    #
# --------------------------------------------------------------------------- #


def role_session(name: str, sessions: list[Session]) -> tuple[Session | None, str]:
    live = [
        s
        for s in sessions
        if s.get("name") == name and s.get("group") == GROUP and _sessions.is_live(s)
    ]
    if not live:
        return None, f"no live session named {name} in group {GROUP}"
    if len(live) > 1:
        return None, f"{len(live)} live sessions named {name} in group {GROUP}"
    return live[0], ""


def cycle_session(key: str, cycle: Cycle, sessions: list[Session]) -> Session | None:
    if key.startswith("session:"):
        found = _sessions.find_session(key.split(":", 1)[1], sessions)
    else:
        found, _ = role_session(key, sessions)
    if found is None or not _sessions.is_live(found):
        return None
    if cycle.session and found.get("id") != cycle.session:
        return None
    return found


def rest_block(session: Session, quiet_seconds: float) -> str:
    """Why the session is not at rest; "" when it is."""
    status = str(session.get("status", ""))
    if status not in REST_STATUSES:
        return f"status is {status or 'unknown'}"
    if session.get("tool", "claude") != "claude":
        return f"tool is {session.get('tool')}, not claude"
    if quiet_seconds > 0:
        path = transcript(session)
        if path is not None:
            idle = now() - path.stat().st_mtime
            if idle < quiet_seconds:
                return (
                    f"transcript written {fmt_span(max(idle, 0))} ago, "
                    f"needs {fmt_span(quiet_seconds)} quiet"
                )
    screen = capture_pane(str(session.get("id")))
    if screen is None:
        return "pane unreadable"
    prompt = prompt_state(screen)
    if prompt == "text":
        return "text typed in the prompt"
    if prompt == "none":
        return "a dialog is open or the prompt is not showing"
    return open_approvals()


def gate_block(cycle: Cycle) -> str:
    if cycle.paused:
        return "paused after 3 cancelled restarts (zen-rotate resume)"
    if cycle.backoff_until > now():
        return f"backoff for {fmt_span(cycle.backoff_until - now())} more"
    return ""


def start_cycle(
    key: str, cycle: Cycle, session: Session, saved: str, age: float
) -> bool:
    sid = str(session.get("id"))
    note = new_note_path(key)
    path = transcript(session)
    base = _sessions.last_end_turn(path)[0] if path else ""
    if not send(sid, request_text(key, note)):
        cycle.block = "agent-manager send failed"
        return False
    cycle.phase = "requested"
    cycle.session = sid
    cycle.conversation = str(session.get("agent_session_id") or "")
    cycle.saved = saved
    cycle.note = str(note)
    cycle.requests = 1
    cycle.misses = 0
    cycle.requested_at = now()
    cycle.base_uuid = base
    cycle.age = age
    cycle.block = ""
    log(f"requested a handoff note from {key} ({sid}): {note}")
    return True


def end_cycle(key: str, state: State) -> None:
    cycle = state.cycle(key)
    if key.startswith("session:"):
        del state.cycles[key]
        return
    cycle.phase = "idle"
    cycle.requests = cycle.misses = 0
    cycle.requested_at = 0
    cycle.base_uuid = cycle.conversation = cycle.note = ""


def cancel(
    key: str,
    state: State,
    config: Config,
    reason: str,
    session: Session | None,
    backoff: float | None = None,
) -> None:
    cycle = state.cycle(key)
    if session is not None:
        send(cycle.session, cancel_text(reason, cycle.note))
    cycle.cancels += 1
    cycle.backoff_until = now() + (
        config.defer_minutes * 60 if backoff is None else backoff
    )
    cycle.block = f"cancelled: {reason}"
    cycle.last_cancel = f"{iso(now())} {reason}"
    log(f"cancelled {key}: {reason}")
    if cycle.cancels >= PAUSE_AFTER and not key.startswith("session:"):
        cycle.paused = True
        log(f"paused {key}: {PAUSE_AFTER} cancelled restarts in a row")
    end_cycle(key, state)


def rerequest(
    key: str, state: State, config: Config, session: Session, gap: str, base: str
) -> None:
    cycle = state.cycle(key)
    if cycle.requests >= MAX_REQUESTS:
        cancel(
            key,
            state,
            config,
            f"{MAX_REQUESTS} requests without a clean handoff",
            session,
        )
        return
    text = rerequest_text(cycle.note, gap, cycle.requests + 1)
    if not send(cycle.session, text):
        cycle.block = "agent-manager send failed"
        return
    cycle.requests += 1
    cycle.requested_at = now()
    cycle.base_uuid = base
    cycle.block = f"asked again: {gap}"
    log(f"asked {key} again: {gap}")


def miss(
    key: str, state: State, config: Config, session: Session, gap: str, base: str
) -> None:
    cycle = state.cycle(key)
    cycle.misses += 1
    if cycle.misses >= MAX_MISSES:
        cancel(key, state, config, gap, session)
        return
    rerequest(key, state, config, session, gap, base)


def confirm_clear(session_id: str, old_conversation: str) -> bool:
    wait = float(os.environ.get("ZEN_ROTATE_CLEAR_WAIT", "20"))
    poll = float(os.environ.get("ZEN_ROTATE_CLEAR_POLL", "1"))
    deadline = time.monotonic() + wait
    while True:
        sessions = _sessions.list_sessions() or []
        found = next((s for s in sessions if s.get("id") == session_id), None)
        current = found.get("agent_session_id") if found else None
        if isinstance(current, str) and current and current != old_conversation:
            return True
        if time.monotonic() >= deadline:
            return False
        time.sleep(poll)


def clear_and_resume(key: str, state: State, config: Config, session: Session) -> None:
    cycle = state.cycle(key)
    sid = cycle.session
    marker = marker_path(sid)
    marker.parent.mkdir(parents=True, exist_ok=True)
    marker.write_text(cycle.conversation)
    try:
        cleared = type_clear(sid) and confirm_clear(sid, cycle.conversation)
    finally:
        marker.unlink(missing_ok=True)
    if not cleared:
        tail = "\n".join((capture_pane(sid) or "").splitlines()[-PANE_TAIL:])
        with (state_dir() / "rotate.log").open("a") as f:
            f.write(f"pane of {sid} after an unconfirmed /clear:\n{tail}\n")
        cancel(
            key, state, config, "the /clear was not confirmed", session, CLEAR_BACKOFF
        )
        return
    cycle.phase = "cleared"
    send_resume(key, state)


def send_resume(key: str, state: State) -> None:
    cycle = state.cycle(key)
    if not send(cycle.session, resume_text(cycle.note), cycle.saved):
        cycle.block = "cleared, but the resume message was not sent; retrying"
        log(f"{key} is cleared but the resume send failed; retrying next tick")
        return
    log(f"rotated {key}: {fmt_span(cycle.age)} old, note {cycle.note}")
    cycle.last_rotation = now()
    cycle.last_note = cycle.note
    cycle.cancels = 0
    cycle.backoff_until = 0
    cycle.block = cycle.last_cancel = ""
    prune_notes(key)
    end_cycle(key, state)


def advance(key: str, state: State, config: Config, sessions: list[Session]) -> None:
    """One step of a cycle in flight."""
    cycle = state.cycle(key)
    session = cycle_session(key, cycle, sessions)
    if cycle.phase == "cleared":
        if session is None:
            cancel(key, state, config, "the session is gone", None)
        else:
            send_resume(key, state)
        return
    if session is None:
        cancel(key, state, config, "the session is gone", None)
        return
    if str(session.get("agent_session_id") or "") != cycle.conversation:
        cancel(
            key, state, config, "the conversation changed before the restart", session
        )
        return
    timed_out = now() - cycle.requested_at > REQUEST_TIMEOUT
    path = transcript(session)
    turns = turns_since(path, cycle.base_uuid) if path else []
    if turns is None:
        cancel(
            key, state, config, "the conversation changed before the restart", session
        )
        return
    if str(session.get("status", "")) not in REST_STATUSES or not turns:
        if timed_out:
            cancel(
                key,
                state,
                config,
                "no turn end within 20 minutes of the request",
                session,
            )
        else:
            cycle.block = "waiting for the handoff turn to end"
        return
    last_uuid, last_text = turns[-1]
    marker, reason = marker_of(last_text)
    if marker == DEFER:
        cycle.backoff_until = now() + config.defer_minutes * 60
        cycle.block = f"deferred: {reason}"
        log(f"{key} deferred: {reason}")
        end_cycle(key, state)
        return
    if marker != READY:
        if any(marker_of(text)[0] == READY for _, text in turns[:-1]):
            gap = f"a later turn ran after your {READY}"
            rerequest(key, state, config, session, gap, last_uuid)
        else:
            gap = f"your turn did not end with {READY} or {DEFER} <reason>"
            miss(key, state, config, session, gap, last_uuid)
        return
    problems = check_note(Path(cycle.note))
    if problems:
        miss(
            key,
            state,
            config,
            session,
            "the note is not valid: " + "; ".join(problems),
            last_uuid,
        )
        return
    pending = _sessions.pending_background(_sessions.records(path)) if path else []
    if pending:
        gap = "background runs are still running: " + ", ".join(pending)
        miss(key, state, config, session, gap, last_uuid)
        return
    block = rest_block(session, 0)
    if block:
        if timed_out:
            cancel(key, state, config, f"not at rest for 20 minutes ({block})", session)
        else:
            cycle.block = block
        return
    clear_and_resume(key, state, config, session)


def consider(name: str, state: State, config: Config, sessions: list[Session]) -> None:
    """Start a cycle for an enabled role when it is due and at rest."""
    cycle = state.cycle(name)
    role = config.roles[name]
    cycle.block = gate_block(cycle)
    if cycle.block:
        return
    session, problem = role_session(name, sessions)
    if session is None:
        cycle.block = problem
        return
    age = session_age(session)
    if age is None or age < config.hours * 3600:
        return
    cycle.block = rest_block(session, role.quiet_minutes * 60)
    if not cycle.block:
        start_cycle(name, cycle, session, name, age)


# --------------------------------------------------------------------------- #
# Commands                                                                     #
# --------------------------------------------------------------------------- #


def need_sessions() -> list[Session]:
    sessions = _sessions.list_sessions()
    if sessions is None:
        raise RotateError("agent-manager sessions failed")
    return sessions


def cmd_tick() -> int:
    config = load_config()
    with lock() as held:
        if not held:
            return 0
        state = load_state()
        enabled = [name for name, role in config.roles.items() if role.enabled]
        flying = [key for key, cycle in state.cycles.items() if cycle.in_flight()]
        if not enabled and not flying:
            return 0
        sessions = need_sessions()
        try:
            for key in flying:
                advance(key, state, config, sessions)
            for name in enabled:
                if name not in flying:
                    consider(name, state, config, sessions)
        finally:
            save_state(state)
    return 0


def cmd_now(role: str | None, session_key: str | None, saved: str) -> int:
    config = load_config()
    with lock() as held:
        if not held:
            raise RotateError("another zen-rotate is running; try again in a moment")
        state = load_state()
        sessions = need_sessions()
        if role is not None:
            if role not in config.roles:
                raise RotateError(f"unknown role {role!r} (zen.json rotate.roles)")
            key, quiet = role, config.roles[role].quiet_minutes * 60
            session, problem = role_session(role, sessions)
            if session is None:
                raise RotateError(problem)
            saved = role
        else:
            session = _sessions.find_session(str(session_key), sessions)
            if session is None or not _sessions.is_live(session):
                raise RotateError(f"no live session {session_key!r}")
            if session.get("name") in (*PROTECTED, *config.roles):
                raise RotateError(
                    f"{session.get('name')} is a role session; use zen-rotate now <role>"
                )
            key, quiet = f"session:{session.get('id')}", 0.0
        cycle = state.cycle(key)
        try:
            if cycle.in_flight():
                raise RotateError(f"a restart of {key} is already in flight")
            block = gate_block(cycle) or rest_block(session, quiet)
            if block:
                raise RotateError(f"{key} is not at rest: {block}")
            if not start_cycle(key, cycle, session, saved, session_age(session) or 0):
                raise RotateError(f"could not message {key}: {cycle.block}")
        finally:
            if not cycle.in_flight() and key.startswith("session:"):
                state.cycles.pop(key, None)
            save_state(state)
    return 0


def status_rows(
    config: Config, state: State, sessions: list[Session]
) -> list[dict[str, object]]:
    rows: list[dict[str, object]] = []
    keys = list(config.roles) + [k for k in state.cycles if k not in config.roles]
    for key in keys:
        cycle = state.cycles.get(key, Cycle())
        role = config.roles.get(key)
        if key.startswith("session:"):
            session = _sessions.find_session(key.split(":", 1)[1], sessions)
        else:
            session, _ = role_session(key, sessions)
        age = session_age(session) if session else None
        due_in = None if age is None else max(0.0, config.hours * 3600 - age)
        rows.append(
            {
                "role": key,
                "enabled": bool(role and role.enabled),
                "session": session.get("id") if session else None,
                "age_seconds": None if age is None else round(age),
                "due_in_seconds": None if due_in is None else round(due_in),
                "phase": "paused" if cycle.paused else cycle.phase,
                "block": gate_block(cycle) or cycle.block,
                "cancels": cycle.cancels,
                "last_rotation": iso(cycle.last_rotation)
                if cycle.last_rotation
                else None,
                "last_cancel": cycle.last_cancel or None,
                "last_note": cycle.last_note or None,
            }
        )
    return rows


def cmd_status(as_json: bool) -> int:
    config = load_config()
    rows = status_rows(config, load_state(), need_sessions())
    if as_json:
        print(json.dumps(rows))
        return 0
    for row in rows:
        age, due = row["age_seconds"], row["due_in_seconds"]
        last = row["last_rotation"]
        parts = [
            str(row["role"]) + ("" if row["enabled"] else " (off)"),
            f"session {row['session'] or '-'}",
            f"age {fmt_span(age) if isinstance(age, int) else '-'}",
            f"due in {fmt_span(due) if isinstance(due, int) else '-'}",
            f"phase {row['phase']}",
            f"block {row['block'] or '-'}",
            f"last rotation {last or '-'}",
            f"last cancel {row['last_cancel'] or '-'}",
            f"last note {row['last_note'] or '-'}",
        ]
        print(" · ".join(parts))
    return 0


def cmd_check_note(path: str) -> int:
    problems = check_note(Path(path))
    for problem in problems:
        print(problem)
    if not problems:
        print("ok")
    return 1 if problems else 0


def cmd_resume(role: str) -> int:
    config = load_config()
    if role not in config.roles:
        raise RotateError(f"unknown role {role!r} (zen.json rotate.roles)")
    with lock() as held:
        if not held:
            raise RotateError("another zen-rotate is running; try again in a moment")
        state = load_state()
        cycle = state.cycle(role)
        cycle.paused = False
        cycle.cancels = 0
        cycle.backoff_until = 0
        cycle.block = cycle.last_cancel = ""
        save_state(state)
    print(f"{role}: pause and backoff lifted")
    return 0


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(
        prog="zen-rotate",
        description="Restart a long-running agent session in place from its own handoff note.",
    )
    sub = p.add_subparsers(dest="command", required=True)
    sub.add_parser("tick", help="one step per enabled role and cycle in flight")
    status = sub.add_parser("status", help="per role: session, age, phase, block")
    status.add_argument("--json", action="store_true", help="JSON output")
    now_p = sub.add_parser("now", help="start a cycle now, skipping the age check")
    now_p.add_argument("role", nargs="?", help="a role from zen.json rotate.roles")
    now_p.add_argument("--session", help="any other session id or name")
    now_p.add_argument(
        "--saved", default="", help="with --session: saved prompt to resume with"
    )
    check = sub.add_parser("check-note", help="validate a handoff note")
    check.add_argument("path")
    resume = sub.add_parser("resume", help="lift a role's pause and backoff")
    resume.add_argument("role")
    args = p.parse_args(argv)
    try:
        if args.command == "tick":
            return cmd_tick()
        if args.command == "status":
            return cmd_status(args.json)
        if args.command == "now":
            if (args.role is None) == (args.session is None):
                p.error("give a role, or --session ID")
            if args.saved and args.session is None:
                p.error("--saved needs --session")
            return cmd_now(args.role, args.session, args.saved)
        if args.command == "check-note":
            return cmd_check_note(args.path)
        return cmd_resume(args.role)
    except RotateError as e:
        print(f"zen-rotate: {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
