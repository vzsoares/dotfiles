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
#      zen-board next [--agent] [--json]
#      zen-board start <task> [--json]
#      zen-board done <task> [--note TEXT] [--json]
#      zen-board add <title> --project P [--inbox] [--human-only]
#                    [--human-approval] [--priority low|medium]
#                    [--related <task>]... [--json]
#      zen-board priority <task> low|medium|none [--json]
#      zen-board relate <task> <task>... [--json]
#      zen-board note <task> <text> [--json]
#      zen-board wontdo <task> --note TEXT [--json]
#      zen-board checklist <task> <item> [--check] [--json]
#      zen-board stale [--days N] [--json]
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
#      --note TEXT    done: append a dated note after the status change;
#                     wontdo: required, the reason.
#      --check        checklist: tick the open item starting with <item>
#                     instead of appending one.
#      --days N       stale: notes older than N days (default 14) count as stale.
#      --agent        next: To Do tasks only, skipping Human Only ones.
#      --json         Print the outcome as JSON.
#
#  DESCRIPTION
#      check   open tasks: In Progress, To Do, Blocked, Inbox; with Priority
#      next    one recommendation and why: In Progress (latest edit), then
#              To Do by Priority (High, Medium, Low, none), the vault's
#              next_order projects (unlisted tie after), nearest due, oldest;
#              never Blocked/Inbox. --json adds "rule": the key that put it
#              ahead of the runner-up (in_progress, priority, next_order, due,
#              oldest), or "only"
#      start   Status = In Progress
#      done    Status = Done and Conclusion Date = today in one PATCH
#      add     new task (To Do unless --inbox); prints its numeric ID
#      note    appends "**YYYY-MM-DD:** text" to the page body
#      priority  sets Low, Medium or none; refuses High, and refuses to change
#              a task that is already High (exit 2)
#      relate  links the tasks in Related Tasks, keeping the existing links
#      wontdo  Status = Won't Do and Conclusion Date = today, then the note
#      checklist  appends a to-do item; prints the task, its open items and
#              the oldest open item's age in days
#      stale   In Progress, To Do and Blocked tasks whose newest dated note or
#              checklist item (or creation, with none) is N or more days old
#
#      NOTION_TOKEN comes from the env, else from ~/.zshrc.local. Board and
#      project IDs and next_order come from the vault config/zen-flow.json,
#      never this repo.
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
from dataclasses import asdict, dataclass, field
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
WONT_DO = "🗑️ Won't Do"
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
    next_order: list[str] = field(default_factory=list)  # project names, any case

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
    order = [text(n) for n in arr(raw.get("next_order")) if text(n)]
    return Config(
        data_source=source, projects=projects, groups=groups, next_order=order
    )


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


def project_rank(project: str, order: list[str]) -> int:
    names = [n.lower() for n in order]
    return names.index(project.lower()) if project.lower() in names else len(names)


# Names each rank() key, in order, for `next --json`'s "rule".
RANK_RULES = ["in_progress", "priority", "next_order", "due", "oldest"]


def rank(task: Task, order: list[str]) -> tuple[int, int, int, str, float]:
    group = (
        STATUS_ORDER.index(task.status)
        if task.status in STATUS_ORDER
        else len(STATUS_ORDER)
    )
    if task.status == TODO:
        due = task.due or "9999-99-99"
        return (
            group,
            priority_rank(task.priority),
            project_rank(task.project, order),
            due,
            timestamp(task.created),
        )
    return group, 0, 0, "", -timestamp(task.last_edited)


def open_tasks(client: Notion, cfg: Config) -> list[Task]:
    tasks = [parse_task(p, cfg) for p in query(client, cfg, open_filter(STATUS_ORDER))]
    return sorted(tasks, key=lambda t: rank(t, cfg.next_order))


def deciding_rule(pick: Task, runner_up: Task | None, order: list[str]) -> str:
    if runner_up is None:
        return "only"
    if pick.status == IN_PROGRESS:
        return "in_progress"
    pairs = zip(rank(pick, order), rank(runner_up, order), RANK_RULES)
    return next((rule for a, b, rule in pairs if a != b), "oldest")


def recommend(
    tasks: list[Task], order: list[str], agent: bool = False
) -> tuple[Task | None, str, str]:
    """`tasks` must already be in rank order. Returns (pick, reason, rule)."""
    if agent:
        ready = [t for t in tasks if t.status == TODO and HUMAN_ONLY not in t.tags]
    else:
        ready = [t for t in tasks if t.status not in (BLOCKED, INBOX)]
    if not ready:
        return None, "nothing open", ""
    pick, runner_up = ready[0], (ready[1] if len(ready) > 1 else None)
    rule = deciding_rule(pick, runner_up, order)
    if pick.status == IN_PROGRESS:
        return pick, "already in progress: finish it before starting new work", rule
    if pick.due:
        reason = f"nearest due date ({pick.due})"
    else:
        reason = "oldest To Do; none has a due date"
    if rule == "next_order":
        reason = f"{pick.project} first at equal priority (next_order); {reason}"
    if pick.priority:
        reason = f"{pick.priority} priority; {reason}"
    return pick, reason, rule


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


def conclude(client: Notion, cfg: Config, page_id: str, status: str) -> Task:
    props: Json = {
        STATUS: {"select": {"name": status}},
        CONCLUSION: {"date": {"start": date.today().isoformat()}},
    }
    return set_status(client, cfg, page_id, props)


def children(client: Notion, page_id: str) -> list[Json]:
    blocks: list[Json] = []
    cursor = ""
    while True:
        path = f"/blocks/{page_id}/children?page_size=100"
        if cursor:
            path += f"&start_cursor={cursor}"
        res = client.request("GET", path)
        blocks.extend(obj(b) for b in arr(res.get("results")))
        cursor = text(res.get("next_cursor"))
        if res.get("has_more") is not True or not cursor:
            return blocks


def plain(block: Json) -> str:
    body = obj(block.get(text(block.get("type"))))
    return "".join(
        text(obj(t).get("plain_text")) or text(obj(obj(t).get("text")).get("content"))
        for t in arr(body.get("rich_text"))
    )


def day_of(iso: str) -> date | None:
    try:
        return date.fromisoformat(iso[:10])
    except ValueError:
        return None


@dataclass
class Checklist:
    id: int | None
    open: int
    oldest_days: int | None
    checked: str


def open_items(blocks: list[Json]) -> list[Json]:
    return [
        b
        for b in blocks
        if b.get("type") == "to_do" and obj(b.get("to_do")).get("checked") is not True
    ]


def checklist_item(
    client: Notion, cfg: Config, page_id: str, item: str, check: bool
) -> Checklist:
    checked = ""
    if check:
        match = next(
            (
                b
                for b in open_items(children(client, page_id))
                if plain(b).startswith(item)
            ),
            None,
        )
        if match is None:
            raise BoardError(EXIT_USAGE, f"no open item starting with {item!r}")
        checked = plain(match)
        client.request(
            "PATCH", f"/blocks/{text(match.get('id'))}", {"to_do": {"checked": True}}
        )
    else:
        rich_text = [{"type": "text", "text": {"content": item}}]
        block = {
            "object": "block",
            "type": "to_do",
            "to_do": {"rich_text": rich_text, "checked": False},
        }
        client.request("PATCH", f"/blocks/{page_id}/children", {"children": [block]})
    task = parse_task(client.request("GET", f"/pages/{page_id}"), cfg)
    items = open_items(children(client, page_id))
    days = [d for d in (day_of(text(b.get("created_time"))) for b in items) if d]
    oldest = (date.today() - min(days)).days if days else None
    return Checklist(task.id, len(items), oldest, checked)


DATED = re.compile(r"(\d{4}-\d{2}-\d{2}):")
STALE_STATUSES = [IN_PROGRESS, TODO, BLOCKED]


def newest_note(blocks: list[Json]) -> date | None:
    """Newest dated note, or checklist item added, so a live checklist isn't stale."""
    dates = [
        d
        for b in blocks
        if b.get("type") == "paragraph" and (m := DATED.match(plain(b)))
        for d in [day_of(m.group(1))]
        if d
    ] + [
        d
        for b in blocks
        if b.get("type") == "to_do"
        for d in [day_of(text(b.get("created_time")))]
        if d
    ]
    return max(dates) if dates else None


def stale_tasks(client: Notion, cfg: Config, days: int) -> list[tuple[Task, str, int]]:
    """(task, newest note date or "", age in days), oldest first."""
    status_filter: Json = {
        "or": [{"property": STATUS, "select": {"equals": s}} for s in STALE_STATUSES]
    }
    today = date.today()
    out: list[tuple[Task, str, int]] = []
    for task in (parse_task(p, cfg) for p in query(client, cfg, status_filter)):
        created = day_of(task.created) or today
        # A note can't predate the page, so a young page can't be stale.
        if (today - created).days < days:
            continue
        note = newest_note(children(client, task.page_id))
        age = (today - (note or created)).days
        if age >= days:
            out.append((task, note.isoformat() if note else "", age))
    return sorted(out, key=lambda t: -t[2])


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
def next_(
    agent: bool = typer.Option(
        False, "--agent", help="To Do only, skipping Human Only tasks."
    ),
    as_json: bool = JSON_OPT,
) -> None:
    """Recommend one task and say why."""
    try:
        client, cfg = connect()
        tasks = open_tasks(client, cfg)
    except BoardError as exc:
        raise fail(exc) from exc
    pick, reason, rule = recommend(tasks, cfg.next_order, agent)
    if as_json:
        task = pick.to_json() if pick else None
        emit({"task": task, "reason": reason, "rule": rule or None})
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
        updated = conclude(client, cfg, page_id, DONE)
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


@app.command()
def wontdo(
    task: str,
    note: str = typer.Option(..., "--note", help="Why it is dropped."),
    as_json: bool = JSON_OPT,
) -> None:
    """Set Status = Won't Do and Conclusion Date = today, then append --note."""
    if not note.strip():
        raise fail(BoardError(EXIT_USAGE, "--note must say why"))
    try:
        client, cfg = connect()
        page_id = resolve(client, cfg, task)
        updated = conclude(client, cfg, page_id, WONT_DO)
        append_note(client, page_id, note)
    except BoardError as exc:
        raise fail(exc) from exc
    emit(updated.to_json()) if as_json else print(updated.line())


@app.command()
def checklist(
    task: str,
    item: str,
    check: bool = typer.Option(False, "--check", help="Tick the open item."),
    as_json: bool = JSON_OPT,
) -> None:
    """Append a to-do item (or --check one); print open count and oldest age."""
    if not item.strip():
        raise fail(BoardError(EXIT_USAGE, "item is empty"))
    try:
        client, cfg = connect()
        page_id = resolve(client, cfg, task)
        result = checklist_item(client, cfg, page_id, item, check)
    except BoardError as exc:
        raise fail(exc) from exc
    if as_json:
        emit(asdict(result))
        return
    num = f"#{result.id}" if result.id is not None else "#?"
    oldest = f"{result.oldest_days}d" if result.oldest_days is not None else "-"
    print(f"{num}  open {result.open}  oldest {oldest}")


@app.command()
def stale(
    days: int = typer.Option(14, "--days", min=1, help="Age threshold in days."),
    as_json: bool = JSON_OPT,
) -> None:
    """List open tasks with no dated note or checklist item in N days."""
    try:
        client, cfg = connect()
        rows = stale_tasks(client, cfg, days)
    except BoardError as exc:
        raise fail(exc) from exc
    if as_json:
        emit(
            [
                {**t.to_json(), "last_note": note or None, "age_days": age}
                for t, note, age in rows
            ]
        )
        return
    for t, note, age in rows:
        print(f"{t.line()}  ({age}d, {'note ' + note if note else 'no note'})")
    if not rows:
        print("nothing stale")


if __name__ == "__main__":
    app()
