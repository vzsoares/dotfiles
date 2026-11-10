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
#      _                         _
#     | |__   ___   __ _ _ __ __| |     z e n - b o a r d
#     | '_ \ / _ \ / _` | '__/ _` |
#     | |_) | (_) | (_| | | | (_| |
#     |_.__/ \___/ \__,_|_|  \__,_|
#
# ==============================================================================
#
#  NAME
#      board.py  --  the everyday zen-flow board ops over the Notion REST API
#
#  SYNOPSIS
#      zen-board check [--json]
#      zen-board next [--json]
#      zen-board start <task> [--json]
#      zen-board done <task> [--note TEXT] [--json]
#      zen-board add <title> --project P [--inbox] [--human-only] [--json]
#      zen-board note <task> <text> [--json]
#
#  OPTIONS
#      <task>         570, #570, a Notion page URL, or a page UUID.
#      --project P    A vault projects[].name or agent_groups key (any case).
#      --inbox        add: leave Status empty instead of To Do.
#      --human-only   add: set the Human Only tag.
#      --note TEXT    done: append a dated note after the status change.
#      --json         Print the outcome as JSON.
#
#  DESCRIPTION
#      check   open tasks: Urgent, In Progress, To Do, Blocked, Inbox
#      next    one recommendation and why: Urgent, then In Progress (latest
#              edit), then To Do (nearest due, then oldest); never Blocked/Inbox
#      start   Status = In Progress
#      done    Status = Done and Conclusion Date = today in one PATCH
#      add     new task (To Do unless --inbox); prints its numeric ID
#      note    appends "**YYYY-MM-DD:** text" to the page body
#
#      NOTION_TOKEN comes from the env, else from ~/.zshrc.local. Board and
#      project IDs come from the vault config/zen-flow.json, never this repo.
#
#  EXIT CODES
#      0 ok   1 API or network error   2 bad usage, task or project not found
#      3 token or vault config missing
#
#  AUTHOR
#      vzsoares.
#
# ==============================================================================
"""Run the everyday zen-flow board ops (check, next, start, done, add, note)."""

from __future__ import annotations

import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from collections.abc import Callable
from dataclasses import asdict, dataclass
from datetime import date, datetime
from pathlib import Path
from urllib.parse import parse_qs, urlparse

import typer

EXIT_API = 1
EXIT_USAGE = 2
EXIT_CONFIG = 3

API = "https://api.notion.com/v1"
NOTION_VERSION = "2025-09-03"
RETRIES = 2

TITLE = "Write a task"
STATUS = "Status"
UNIQUE_ID = "ID"
PROJECT = "☢️ Projeto"
TAGS = "Tags"
DUE = "Due Date"
CONCLUSION = "Conclusion Date"

IN_PROGRESS = "In Progress"
TODO = "🧱 To Do"
BLOCKED = "Blocked"
DONE = "✅ Done"
INBOX = ""
HUMAN_ONLY = "Human Only"

Json = dict[str, object]
Transport = Callable[
    [str, str, bytes | None, dict[str, str]], tuple[int, dict[str, str], bytes]
]

sleep: Callable[[float], None] = time.sleep


class BoardError(Exception):
    def __init__(self, code: int, message: str) -> None:
        super().__init__(message)
        self.code = code


def obj(value: object) -> Json:
    return {str(k): v for k, v in value.items()} if isinstance(value, dict) else {}


def arr(value: object) -> list[object]:
    return list(value) if isinstance(value, list) else []


def text(value: object) -> str:
    return value if isinstance(value, str) else ""


# =========================================================================== #
# Token and vault config                                                       #
# =========================================================================== #


def load_token() -> str:
    token = os.environ.get("NOTION_TOKEN", "").strip()
    if token:
        return token
    rc = Path.home() / ".zshrc.local"
    if rc.exists():
        for line in rc.read_text().splitlines():
            m = re.match(r"\s*(?:export\s+)?NOTION_TOKEN=(.*)$", line)
            if m:
                value = m.group(1).strip()
                if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
                    value = value[1:-1]
                token = value or token
    if token:
        return token
    raise BoardError(
        EXIT_CONFIG,
        "NOTION_TOKEN is not set: export it, or add "
        "`export NOTION_TOKEN=...` to ~/.zshrc.local",
    )


def read_json(path: Path) -> Json:
    try:
        return obj(json.loads(path.read_text()))
    except (OSError, ValueError) as exc:
        raise BoardError(EXIT_CONFIG, f"cannot read {path}: {exc}") from exc


@dataclass
class Config:
    data_source: str
    projects: dict[str, str]  # name -> page id without dashes
    groups: dict[str, str]  # agent group key -> project name

    def project_name(self, page_id: str) -> str:
        bare = page_id.replace("-", "")
        return next((n for n, p in self.projects.items() if p == bare), "?")

    def project_id(self, name: str) -> str:
        wanted = name.lower()
        groups = {k.lower(): v.lower() for k, v in self.groups.items()}
        for key in (wanted, groups.get(wanted, "")):
            for project, page_id in self.projects.items():
                if project.lower() == key:
                    return page_id
        valid = ", ".join(sorted({*self.projects, *self.groups}, key=str.lower))
        raise BoardError(EXIT_USAGE, f"unknown project {name!r}; valid: {valid}")


def load_config() -> Config:
    brain = Path.home() / ".claude" / "second-brain.json"
    vault = text(read_json(brain).get("vault"))
    if not vault:
        raise BoardError(EXIT_CONFIG, f"no vault path in {brain}")
    path = Path(vault).expanduser() / "config" / "zen-flow.json"
    raw = read_json(path)
    source = text(obj(raw.get("board")).get("tasks_data_source"))
    source = source.removeprefix("collection://")
    if not source:
        raise BoardError(EXIT_CONFIG, f"no board.tasks_data_source in {path}")
    projects = {
        text(p.get("name")): text(p.get("page_id")).replace("-", "")
        for p in map(obj, arr(raw.get("projects")))
        if text(p.get("name")) and text(p.get("page_id"))
    }
    groups = {k: text(v) for k, v in obj(raw.get("agent_groups")).items() if text(v)}
    return Config(data_source=source, projects=projects, groups=groups)


# =========================================================================== #
# HTTP                                                                         #
# =========================================================================== #


def urllib_transport(
    method: str, url: str, data: bytes | None, headers: dict[str, str]
) -> tuple[int, dict[str, str], bytes]:
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            hdrs = {k.lower(): v for k, v in resp.headers.items()}
            body: bytes = resp.read()
            return resp.status, hdrs, body
    except urllib.error.HTTPError as exc:
        return exc.code, {k.lower(): v for k, v in exc.headers.items()}, exc.read()


transport: Transport = urllib_transport


def retry_after(headers: dict[str, str]) -> float:
    try:
        return max(0.0, float(headers.get("retry-after", "1")))
    except ValueError:
        return 1.0


class Notion:
    def __init__(self, token: str, send: Transport) -> None:
        self.token = token
        self.send = send

    def request(self, method: str, path: str, body: Json | None = None) -> Json:
        data = json.dumps(body).encode() if body is not None else None
        headers = {
            "Authorization": f"Bearer {self.token}",
            "Notion-Version": NOTION_VERSION,
            "Content-Type": "application/json",
        }
        attempt = 0
        while True:
            try:
                status, hdrs, raw = self.send(method, API + path, data, headers)
            except OSError as exc:
                raise BoardError(EXIT_API, f"{method} {path}: {exc}") from exc
            if status == 429 and attempt < RETRIES:
                attempt += 1
                sleep(retry_after(hdrs))
                continue
            try:
                payload = obj(json.loads(raw or b"{}"))
            except ValueError:
                payload = {}
            if status >= 400:
                detail = text(payload.get("message"))
                raise BoardError(
                    EXIT_API, f"{method} {path}: HTTP {status} {detail}".rstrip()
                )
            return payload


# =========================================================================== #
# Tasks                                                                        #
# =========================================================================== #


@dataclass
class Task:
    id: int | None
    title: str
    status: str
    project: str
    tags: list[str]
    due: str
    last_edited: str
    created: str
    url: str
    page_id: str

    def line(self) -> str:
        num = f"#{self.id}" if self.id is not None else "#?"
        due = f"  (due {self.due})" if self.due else ""
        return f"{num}  {self.status or 'Inbox'}  {self.project}  {self.title}{due}"

    def to_json(self) -> Json:
        out: Json = asdict(self)
        out["status"] = self.status or "Inbox"
        del out["created"], out["page_id"]
        return out


def parse_task(page: Json, cfg: Config) -> Task:
    props = obj(page.get("properties"))
    title = "".join(
        text(obj(t).get("plain_text")) for t in arr(obj(props.get(TITLE)).get("title"))
    )
    number = obj(obj(props.get(UNIQUE_ID)).get("unique_id")).get("number")
    relation = arr(obj(props.get(PROJECT)).get("relation"))
    project = cfg.project_name(text(obj(relation[0]).get("id"))) if relation else "-"
    return Task(
        id=number if isinstance(number, int) else None,
        title=title,
        status=text(obj(obj(props.get(STATUS)).get("select")).get("name")),
        project=project,
        tags=[
            text(obj(t).get("name"))
            for t in arr(obj(props.get(TAGS)).get("multi_select"))
        ],
        due=text(obj(obj(props.get(DUE)).get("date")).get("start"))[:10],
        last_edited=text(page.get("last_edited_time")),
        created=text(page.get("created_time")),
        url=text(page.get("url")),
        page_id=text(page.get("id")),
    )


def query(client: Notion, cfg: Config, filter_: Json) -> list[Json]:
    pages: list[Json] = []
    cursor = ""
    while True:
        body: Json = {"filter": filter_, "page_size": 100}
        if cursor:
            body["start_cursor"] = cursor
        res = client.request("POST", f"/data_sources/{cfg.data_source}/query", body)
        pages.extend(obj(p) for p in arr(res.get("results")))
        cursor = text(res.get("next_cursor"))
        if res.get("has_more") is not True or not cursor:
            return pages


def urgent_option(client: Notion, cfg: Config) -> str:
    """The Urgent status option, or "" when the schema has none (filtering on it 400s)."""
    schema = client.request("GET", f"/data_sources/{cfg.data_source}")
    status = obj(obj(schema.get("properties")).get(STATUS))
    names = [
        text(obj(o).get("name")) for o in arr(obj(status.get("select")).get("options"))
    ]
    return next((n for n in names if "urgent" in n.lower()), "")


def status_order(urgent: str) -> list[str]:
    return [*([urgent] if urgent else []), IN_PROGRESS, TODO, BLOCKED, INBOX]


def open_filter(order: list[str]) -> Json:
    clauses: list[object] = [
        {"property": STATUS, "select": {"equals": s}} for s in order if s
    ]
    clauses.append({"property": STATUS, "select": {"is_empty": True}})
    return {"or": clauses}


def timestamp(iso: str) -> float:
    try:
        return datetime.fromisoformat(iso).timestamp()
    except ValueError:
        return 0.0


def rank(task: Task, order: list[str]) -> tuple[int, str, float]:
    group = order.index(task.status) if task.status in order else len(order)
    if task.status == TODO:
        return group, task.due or "9999-99-99", timestamp(task.created)
    return group, "", -timestamp(task.last_edited)


def open_tasks(client: Notion, cfg: Config) -> tuple[list[Task], str]:
    urgent = urgent_option(client, cfg)
    order = status_order(urgent)
    tasks = [parse_task(p, cfg) for p in query(client, cfg, open_filter(order))]
    return sorted(tasks, key=lambda t: rank(t, order)), urgent


def recommend(tasks: list[Task], urgent: str) -> tuple[Task | None, str]:
    """`tasks` must already be in rank order."""
    pick = next((t for t in tasks if t.status not in (BLOCKED, INBOX)), None)
    if pick is None:
        return None, "nothing open"
    if urgent and pick.status == urgent:
        return pick, "urgent"
    if pick.status == IN_PROGRESS:
        return pick, "already in progress: finish it before starting new work"
    if pick.due:
        return pick, f"nearest due date ({pick.due})"
    return pick, "oldest To Do; none has a due date"


UUID = re.compile(r"[0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12}")


def trailing_uuid(value: str) -> str:
    for tail in (value[-36:], value[-32:]):
        if UUID.fullmatch(tail):
            return tail.replace("-", "")
    return ""


def page_id_from(ref: str) -> str:
    ref = ref.strip().lower()
    if "://" not in ref:
        return ref.replace("-", "") if UUID.fullmatch(ref) else ""
    url = urlparse(ref)
    # A database view URL names the open page in ?p=, not the path.
    for candidate in [*parse_qs(url.query).get("p", []), url.path.rstrip("/")]:
        page_id = trailing_uuid(candidate)
        if page_id:
            return page_id
    return ""


def resolve(client: Notion, cfg: Config, ref: str) -> str:
    m = re.fullmatch(r"#?(\d+)", ref.strip())
    if m:
        number = int(m.group(1))
        pages = query(
            client, cfg, {"property": UNIQUE_ID, "unique_id": {"equals": number}}
        )
        if not pages:
            raise BoardError(EXIT_USAGE, f"no task #{number} on the board")
        return text(pages[0].get("id")).replace("-", "")
    page_id = page_id_from(ref)
    if not page_id:
        raise BoardError(
            EXIT_USAGE, f"{ref!r} is not a task number, page URL or page UUID"
        )
    return page_id


def set_status(client: Notion, cfg: Config, page_id: str, props: Json) -> Task:
    page = client.request("PATCH", f"/pages/{page_id}", {"properties": props})
    return parse_task(page, cfg)


def append_note(client: Notion, page_id: str, note: str) -> None:
    rich_text = [
        {
            "type": "text",
            "text": {"content": f"{date.today().isoformat()}:"},
            "annotations": {"bold": True},
        },
        {"type": "text", "text": {"content": f" {note}"}},
    ]
    block = {
        "object": "block",
        "type": "paragraph",
        "paragraph": {"rich_text": rich_text},
    }
    client.request("PATCH", f"/blocks/{page_id}/children", {"children": [block]})


def add_task(
    client: Notion,
    cfg: Config,
    title: str,
    project: str,
    inbox: bool,
    human_only: bool,
) -> Task:
    props: Json = {
        TITLE: {"title": [{"type": "text", "text": {"content": title}}]},
        PROJECT: {"relation": [{"id": cfg.project_id(project)}]},
    }
    if not inbox:
        props[STATUS] = {"select": {"name": TODO}}
    if human_only:
        props[TAGS] = {"multi_select": [{"name": HUMAN_ONLY}]}
    body: Json = {
        "parent": {"type": "data_source_id", "data_source_id": cfg.data_source},
        "properties": props,
    }
    task = parse_task(client.request("POST", "/pages", body), cfg)
    if task.id is None:
        raise BoardError(EXIT_API, f"created {task.url} but the response has no ID")
    return task


# =========================================================================== #
# CLI                                                                          #
# =========================================================================== #

app = typer.Typer(add_completion=False, help=__doc__, no_args_is_help=True)


def connect() -> tuple[Notion, Config]:
    cfg = load_config()
    return Notion(load_token(), transport), cfg


def fail(exc: BoardError) -> typer.Exit:
    print(f"zen-board: {exc}", file=sys.stderr)
    return typer.Exit(exc.code)


def emit(data: object) -> None:
    print(json.dumps(data, ensure_ascii=False))


JSON_OPT = typer.Option(False, "--json", help="JSON output.")


@app.command()
def check(as_json: bool = JSON_OPT) -> None:
    """List open tasks: Urgent, In Progress, To Do, Blocked, Inbox."""
    try:
        tasks, _ = open_tasks(*connect())
    except BoardError as exc:
        raise fail(exc) from exc
    if as_json:
        emit([t.to_json() for t in tasks])
        return
    for task in tasks:
        print(task.line())
    if not tasks:
        print("nothing open")


@app.command("next")
def next_(as_json: bool = JSON_OPT) -> None:
    """Recommend one task and say why."""
    try:
        tasks, urgent = open_tasks(*connect())
    except BoardError as exc:
        raise fail(exc) from exc
    pick, reason = recommend(tasks, urgent)
    if as_json:
        emit({"task": pick.to_json() if pick else None, "reason": reason})
    elif pick is None:
        print(reason)
    else:
        print(pick.line())
        print(f"  why: {reason}")


@app.command()
def start(task: str, as_json: bool = JSON_OPT) -> None:
    """Set Status = In Progress."""
    try:
        client, cfg = connect()
        page_id = resolve(client, cfg, task)
        updated = set_status(
            client, cfg, page_id, {STATUS: {"select": {"name": IN_PROGRESS}}}
        )
    except BoardError as exc:
        raise fail(exc) from exc
    emit(updated.to_json()) if as_json else print(updated.line())


@app.command()
def done(
    task: str,
    note: str = typer.Option("", "--note", help="Append a dated note."),
    as_json: bool = JSON_OPT,
) -> None:
    """Set Status = Done and Conclusion Date = today, then append --note."""
    try:
        client, cfg = connect()
        page_id = resolve(client, cfg, task)
        updated = set_status(
            client,
            cfg,
            page_id,
            {
                STATUS: {"select": {"name": DONE}},
                CONCLUSION: {"date": {"start": date.today().isoformat()}},
            },
        )
        if note:
            append_note(client, page_id, note)
    except BoardError as exc:
        raise fail(exc) from exc
    emit(updated.to_json()) if as_json else print(updated.line())


@app.command()
def add(
    title: str,
    project: str = typer.Option(..., "--project", help="Project name or group."),
    inbox: bool = typer.Option(False, "--inbox", help="Leave Status empty."),
    human_only: bool = typer.Option(False, "--human-only", help="Tag Human Only."),
    as_json: bool = JSON_OPT,
) -> None:
    """Create a task and print its numeric ID."""
    try:
        client, cfg = connect()
        created = add_task(client, cfg, title, project, inbox, human_only)
    except BoardError as exc:
        raise fail(exc) from exc
    emit(created.to_json()) if as_json else print(created.id)


@app.command("note")
def note_(task: str, body: str, as_json: bool = JSON_OPT) -> None:
    """Append "**YYYY-MM-DD:** text" to the task page."""
    try:
        client, cfg = connect()
        page_id = resolve(client, cfg, task)
        append_note(client, page_id, body)
    except BoardError as exc:
        raise fail(exc) from exc
    if as_json:
        emit({"page_id": page_id, "note": body})
    else:
        print(f"noted {task}")


if __name__ == "__main__":
    app()
