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
#      zen-board add <title> --project P [--inbox] [--human-only |
#                    --human-approval] [--priority low|medium] [--related <task>]... [--json]
#      zen-board priority <task> low|medium|none [--json]
#      zen-board relate <task> <task>... [--json]
#      zen-board note <task> <text> [--json]
#
#  OPTIONS
#      <task>         570, #570, a Notion page URL, or a page UUID.
#      --project P    A vault projects[].name or agent_groups key (any case).
#      --inbox        add: leave Status empty instead of To Do.
#      --human-only   add: set the Human Only tag.
#      --human-approval  add: set the Human Approval tag; with --human-only
#                     both tags are set.
#      --priority P   add: set Priority (low or medium; High is the user's).
#      --related T    add: link T in Related Tasks (repeatable).
#      --note TEXT    done: append a dated note after the status change.
#      --json         Print the outcome as JSON.
#
#  DESCRIPTION
#      check   open tasks: In Progress, To Do, Blocked, Inbox; with Priority
#      next    one recommendation and why: In Progress (latest edit), then
#              To Do by Priority (High, Medium, Low, none), nearest due, oldest;
#              never Blocked/Inbox
#      start   Status = In Progress
#      done    Status = Done and Conclusion Date = today in one PATCH
#      add     new task (To Do unless --inbox); prints its numeric ID
#      note    appends "**YYYY-MM-DD:** text" to the page body
#      priority  sets Low, Medium or none; refuses High, and refuses to change
#              a task that is already High (exit 2)
#      relate  links the tasks in Related Tasks, keeping the existing links
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
"""Run the everyday zen-flow board ops (check, next, start, done, add, note, ...)."""

from __future__ import annotations

import json
import os
import re
import shlex
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
PRIORITY = "Priority"
RELATED = "Related Tasks"

IN_PROGRESS = "In Progress"
TODO = "🧱 To Do"
BLOCKED = "Blocked"
DONE = "✅ Done"
INBOX = ""
HUMAN_ONLY = "Human Only"
HUMAN_APPROVAL = "Human Approval"
HIGH = "High"
AGENT_PRIORITIES = {"low": "Low", "medium": "Medium"}
PRIORITY_ORDER = [HIGH, "Medium", "Low"]

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
                try:
                    words = shlex.split(m.group(1), comments=True)
                except ValueError:
                    words = []
                token = (words[0] if words else "") or token
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
    priority: str
    related: list[str]  # page IDs without dashes
    due: str
    last_edited: str
    created: str
    url: str
    page_id: str

    def line(self) -> str:
        num = f"#{self.id}" if self.id is not None else "#?"
        due = f"  (due {self.due})" if self.due else ""
        prio = f"  [{self.priority}]" if self.priority else ""
        return (
            f"{num}  {self.status or 'Inbox'}  {self.project}{prio}  {self.title}{due}"
        )

    def to_json(self) -> Json:
        out: Json = asdict(self)
        out["status"] = self.status or "Inbox"
        out["priority"] = self.priority or None
        del out["created"], out["page_id"]
        return out


def relation_ids(prop: object) -> list[str]:
    return [
        text(obj(r).get("id")).replace("-", "")
        for r in arr(obj(prop).get("relation"))
        if text(obj(r).get("id"))
    ]


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
        priority=text(obj(obj(props.get(PRIORITY)).get("select")).get("name")),
        related=relation_ids(props.get(RELATED)),
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


STATUS_ORDER = [IN_PROGRESS, TODO, BLOCKED, INBOX]


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


def priority_rank(priority: str) -> int:
    return (
        PRIORITY_ORDER.index(priority)
        if priority in PRIORITY_ORDER
        else len(PRIORITY_ORDER)
    )


def rank(task: Task) -> tuple[int, int, str, float]:
    group = (
        STATUS_ORDER.index(task.status)
        if task.status in STATUS_ORDER
        else len(STATUS_ORDER)
    )
    if task.status == TODO:
        due = task.due or "9999-99-99"
        return group, priority_rank(task.priority), due, timestamp(task.created)
    return group, 0, "", -timestamp(task.last_edited)


def open_tasks(client: Notion, cfg: Config) -> list[Task]:
    tasks = [parse_task(p, cfg) for p in query(client, cfg, open_filter(STATUS_ORDER))]
    return sorted(tasks, key=rank)


def recommend(tasks: list[Task]) -> tuple[Task | None, str]:
    """`tasks` must already be in rank order."""
    pick = next((t for t in tasks if t.status not in (BLOCKED, INBOX)), None)
    if pick is None:
        return None, "nothing open"
    if pick.status == IN_PROGRESS:
        return pick, "already in progress: finish it before starting new work"
    if pick.due:
        reason = f"nearest due date ({pick.due})"
    else:
        reason = "oldest To Do; none has a due date"
    return pick, f"{pick.priority} priority; {reason}" if pick.priority else reason


UUID = re.compile(r"[0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12}")


def trailing_uuid(value: str) -> str:
    for tail in (value[-36:], value[-32:]):
        if UUID.fullmatch(tail):
            return tail.replace("-", "")
    return ""


def page_id_from(ref: str) -> str:
    ref = ref.strip().lower()
    if "://" not in ref:
        if "/" not in ref:
            return ref.replace("-", "") if UUID.fullmatch(ref) else ""
        ref = "https://" + ref
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


def agent_priority(value: str) -> str:
    """Option name for `value`; "" clears. High is the user's to set."""
    key = value.strip().lower()
    if key == HIGH.lower():
        raise BoardError(EXIT_USAGE, "High is the user's; set it in Notion")
    if key == "none":
        return ""
    if key not in AGENT_PRIORITIES:
        raise BoardError(EXIT_USAGE, f"priority must be low, medium or none: {value!r}")
    return AGENT_PRIORITIES[key]


def relation(page_ids: list[str]) -> Json:
    return {"relation": [{"id": i} for i in page_ids]}


def set_priority(client: Notion, cfg: Config, page_id: str, value: str) -> Task:
    name = agent_priority(value)
    current = parse_task(client.request("GET", f"/pages/{page_id}"), cfg)
    if current.priority == HIGH:
        raise BoardError(
            EXIT_USAGE, f"#{current.id} is High: only the user changes it in Notion"
        )
    props: Json = {PRIORITY: {"select": {"name": name} if name else None}}
    return set_status(client, cfg, page_id, props)


def relate_tasks(client: Notion, cfg: Config, page_id: str, others: list[str]) -> Task:
    page = client.request("GET", f"/pages/{page_id}")
    links = obj(obj(page.get("properties")).get(RELATED))
    if links.get("has_more") is True:
        raise BoardError(EXIT_API, "too many Related Tasks to read; link in Notion")
    existing = parse_task(page, cfg).related
    union = [*existing, *(o for o in others if o != page_id)]
    props: Json = {RELATED: relation(list(dict.fromkeys(union)))}
    return set_status(client, cfg, page_id, props)


def add_task(
    client: Notion,
    cfg: Config,
    title: str,
    project: str,
    inbox: bool,
    human_only: bool,
    human_approval: bool = False,
    priority: str = "",
    related: list[str] | None = None,
) -> Task:
    name = agent_priority(priority) if priority else ""
    links = [resolve(client, cfg, ref) for ref in related or []]
    props: Json = {
        TITLE: {"title": [{"type": "text", "text": {"content": title}}]},
        PROJECT: {"relation": [{"id": cfg.project_id(project)}]},
    }
    if not inbox:
        props[STATUS] = {"select": {"name": TODO}}
    tags = [
        name
        for name, on in ((HUMAN_ONLY, human_only), (HUMAN_APPROVAL, human_approval))
        if on
    ]
    if tags:
        props[TAGS] = {"multi_select": [{"name": t} for t in tags]}
    if name:
        props[PRIORITY] = {"select": {"name": name}}
    if links:
        props[RELATED] = relation(list(dict.fromkeys(links)))
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
RELATED_OPT = typer.Option([], "--related", help="Task to link (repeatable).")
OTHERS_ARG = typer.Argument(..., help="Tasks to link.")


@app.command()
def check(as_json: bool = JSON_OPT) -> None:
    """List open tasks: In Progress, To Do, Blocked, Inbox."""
    try:
        tasks = open_tasks(*connect())
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
        tasks = open_tasks(*connect())
    except BoardError as exc:
        raise fail(exc) from exc
    pick, reason = recommend(tasks)
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
    human_approval: bool = typer.Option(
        False, "--human-approval", help="Tag Human Approval."
    ),
    priority: str = typer.Option("", "--priority", help="low or medium."),
    related: list[str] = RELATED_OPT,
    as_json: bool = JSON_OPT,
) -> None:
    """Create a task and print its numeric ID."""
    try:
        client, cfg = connect()
        created = add_task(
            client,
            cfg,
            title,
            project,
            inbox,
            human_only,
            human_approval,
            priority,
            related,
        )
    except BoardError as exc:
        raise fail(exc) from exc
    emit(created.to_json()) if as_json else print(created.id)


@app.command("priority")
def priority_(task: str, value: str, as_json: bool = JSON_OPT) -> None:
    """Set Priority to low, medium or none; High is the user's."""
    try:
        client, cfg = connect()
        updated = set_priority(client, cfg, resolve(client, cfg, task), value)
    except BoardError as exc:
        raise fail(exc) from exc
    emit(updated.to_json()) if as_json else print(updated.line())


@app.command()
def relate(
    task: str,
    others: list[str] = OTHERS_ARG,
    as_json: bool = JSON_OPT,
) -> None:
    """Link tasks in Related Tasks, keeping the existing links."""
    try:
        client, cfg = connect()
        page_id = resolve(client, cfg, task)
        linked = [resolve(client, cfg, o) for o in others]
        updated = relate_tasks(client, cfg, page_id, linked)
    except BoardError as exc:
        raise fail(exc) from exc
    emit(updated.to_json()) if as_json else print(updated.line())


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
