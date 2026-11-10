#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "typer>=0.12",
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/board.py. The HTTP transport is a fake; nothing hits Notion.

Run with:  uv run scripts/test_board.py
"""

from __future__ import annotations

import importlib.util
import json
import sys
from dataclasses import dataclass, field
from datetime import date
from pathlib import Path

import pytest
from typer.testing import CliRunner

_spec = importlib.util.spec_from_file_location(
    "board", Path(__file__).parent / "board.py"
)
assert _spec is not None and _spec.loader is not None
board = importlib.util.module_from_spec(_spec)
sys.modules["board"] = board
_spec.loader.exec_module(board)

Json = dict[str, object]

# Built at runtime so no literal Notion-shaped ID sits in this public file.
PAGE = "ab" * 16
DASHED = "-".join((PAGE[:8], PAGE[8:12], PAGE[12:16], PAGE[16:20], PAGE[20:]))
ME = "11" * 16
WORK = "22" * 16
OTHER = "cd" * 16
TOKEN = "secret-token-value"
TODAY = date.today().isoformat()


# == Fixtures ================================================================ #


@dataclass
class Fake:
    """A scripted Notion: answers requests in order and records them."""

    responses: list[tuple[int, Json, dict[str, str]]] = field(default_factory=list)
    calls: list[tuple[str, str, Json]] = field(default_factory=list)
    auth: list[str] = field(default_factory=list)

    def push(self, body: Json, status: int = 200, **headers: str) -> None:
        self.responses.append((status, body, headers))

    def __call__(
        self, method: str, url: str, data: bytes | None, headers: dict[str, str]
    ) -> tuple[int, dict[str, str], bytes]:
        body = board.obj(json.loads(data)) if data else {}
        self.calls.append((method, url.removeprefix(board.API), body))
        self.auth.append(headers["Authorization"])
        status, payload, hdrs = self.responses.pop(0)
        return status, hdrs, json.dumps(payload).encode()


def write_vault(home: Path, groups: bool = True) -> None:
    vault = home / "vault"
    (vault / "config").mkdir(parents=True)
    (home / ".claude").mkdir()
    (home / ".claude" / "second-brain.json").write_text(
        json.dumps({"vault": str(vault)})
    )
    config: Json = {
        "board": {"tasks_data_source": "collection://ds-tasks"},
        "projects": [
            {"name": "Me", "page_id": ME},
            {"name": "Work", "page_id": WORK},
        ],
    }
    if groups:
        config["agent_groups"] = {"office": "Work"}
    (vault / "config" / "zen-flow.json").write_text(json.dumps(config))


@pytest.fixture
def fake(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Fake:
    write_vault(tmp_path)
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.setenv("NOTION_TOKEN", TOKEN)
    f = Fake()
    monkeypatch.setattr(board, "transport", f)
    monkeypatch.setattr(board, "sleep", lambda _s: None)
    return f


def page(
    num: int,
    status: str = "",
    *,
    page_id: str = PAGE,
    project: str = ME,
    due: str = "",
    edited: str = "2026-11-01T00:00:00.000Z",
    created: str = "2026-10-01T00:00:00.000Z",
    tags: tuple[str, ...] = (),
    priority: str = "",
    related: tuple[str, ...] = (),
) -> Json:
    return {
        "id": page_id,
        "url": f"https://app.notion.com/p/{page_id}",
        "last_edited_time": edited,
        "created_time": created,
        "properties": {
            board.TITLE: {"title": [{"plain_text": f"task {num}"}]},
            board.STATUS: {"select": {"name": status} if status else None},
            board.UNIQUE_ID: {"unique_id": {"number": num, "prefix": None}},
            board.PROJECT: {"relation": [{"id": project}]},
            board.TAGS: {"multi_select": [{"name": t} for t in tags]},
            board.DUE: {"date": {"start": due} if due else None},
            board.PRIORITY: {"select": {"name": priority} if priority else None},
            board.RELATED: {"relation": [{"id": r} for r in related]},
        },
    }


def results(*pages: Json, cursor: str = "") -> Json:
    return {"results": list(pages), "has_more": bool(cursor), "next_cursor": cursor}


def run(*args: str) -> tuple[int, str, str]:
    res = CliRunner().invoke(board.app, list(args))
    return res.exit_code, res.stdout, res.stderr


# == Task resolution ========================================================= #


@pytest.mark.parametrize("ref", ["570", "#570"])
def test_resolves_task_numbers_by_unique_id(fake: Fake, ref: str) -> None:
    fake.push(results(page(570)))
    client, cfg = board.connect()
    assert board.resolve(client, cfg, ref) == PAGE
    method, path, body = fake.calls[0]
    assert (method, path) == ("POST", "/data_sources/ds-tasks/query")
    assert body["filter"] == {"property": "ID", "unique_id": {"equals": 570}}


@pytest.mark.parametrize(
    "ref",
    [
        f"https://app.notion.com/p/{PAGE}?pvs=204",
        f"https://www.notion.so/Fix-deadbeef-cafe-{PAGE}",
        f"https://www.notion.so/ws/{WORK}?v={ME}&p={PAGE}",
        f"www.notion.so/Fix-{PAGE}",
        DASHED,
        PAGE,
        PAGE.upper(),
    ],
)
def test_resolves_urls_and_uuids_without_http(fake: Fake, ref: str) -> None:
    client, cfg = board.connect()
    assert board.resolve(client, cfg, ref) == PAGE
    assert fake.calls == []


def test_unknown_number_exits_2(fake: Fake) -> None:
    fake.push(results())
    code, _, err = run("start", "999")
    assert code == board.EXIT_USAGE
    assert "no task #999" in err


def test_garbage_ref_exits_2(fake: Fake) -> None:
    code, _, _ = run("start", "not-a-task")
    assert code == board.EXIT_USAGE


# == check =================================================================== #


def statuses_in(filter_: object) -> list[object]:
    return [
        board.obj(board.obj(c).get("select")).get("equals")
        for c in board.arr(board.obj(filter_).get("or"))
    ]


def test_check_filters_open_statuses_without_urgent(fake: Fake) -> None:
    fake.push(results())
    code, out, _ = run("check")
    assert (code, out.strip()) == (0, "nothing open")
    names = statuses_in(fake.calls[0][2]["filter"])
    assert {board.IN_PROGRESS, board.TODO, board.BLOCKED, None} <= set(names)
    assert len(fake.calls) == 1


def test_check_follows_pagination(fake: Fake) -> None:
    fake.push(results(page(1, board.TODO, due="2026-12-01"), cursor="c2"))
    fake.push(results(page(2, board.IN_PROGRESS, project=WORK)))
    code, out, _ = run("check")
    assert code == 0
    assert fake.calls[1][2]["start_cursor"] == "c2"
    assert "start_cursor" not in fake.calls[0][2]
    assert out.splitlines() == [
        "#2  In Progress  Work  task 2",
        f"#1  {board.TODO}  Me  task 1  (due 2026-12-01)",
    ]


def test_check_json_shape_and_unmapped_project(fake: Fake) -> None:
    fake.push(results(page(3, project="33" * 16, tags=("Human Only",))))
    code, out, _ = run("check", "--json")
    assert code == 0
    (task,) = json.loads(out)
    assert set(task) == {
        "id",
        "title",
        "status",
        "project",
        "tags",
        "priority",
        "related",
        "due",
        "last_edited",
        "url",
    }
    assert (task["status"], task["project"], task["tags"]) == (
        "Inbox",
        "?",
        ["Human Only"],
    )
    assert (task["priority"], task["related"]) == (None, [])


def test_check_shows_priority_and_related_ids(fake: Fake) -> None:
    fake.push(results(page(4, board.TODO, priority="High", related=(WORK,))))
    code, out, _ = run("check")
    assert (code, out.strip()) == (0, f"#4  {board.TODO}  Me  [High]  task 4")
    fake.push(results(page(4, board.TODO, priority="Low", related=(WORK, ME))))
    (task,) = json.loads(run("check", "--json")[1])
    assert (task["priority"], task["related"]) == ("Low", [WORK, ME])


# == next ==================================================================== #


def pick(fake: Fake, *pages: Json) -> tuple[int, str]:
    fake.push(results(*pages))
    code, out, _ = run("next", "--json")
    assert code == 0
    data = board.obj(json.loads(out))
    task = board.obj(data.get("task"))
    num = task.get("id")
    return (num if isinstance(num, int) else 0), board.text(data.get("reason"))


def test_next_in_progress_beats_todo_even_when_high(fake: Fake) -> None:
    todo = page(1, board.TODO, due="2026-11-11", priority="High")
    doing = page(2, board.IN_PROGRESS)
    assert pick(fake, todo, doing)[0] == 2


def test_next_todo_orders_by_priority_then_due_then_oldest(fake: Fake) -> None:
    none = page(1, board.TODO, due="2026-11-01")
    low = page(2, board.TODO, priority="Low", due="2026-11-01")
    med_late = page(3, board.TODO, priority="Medium", due="2026-12-01")
    med_soon = page(4, board.TODO, priority="Medium", due="2026-11-05")
    high = page(5, board.TODO, priority="High")
    assert pick(fake, none, low, med_late, med_soon)[0] == 4
    assert pick(fake, none, low, med_late)[0] == 3
    assert pick(fake, none, low)[0] == 2
    assert pick(fake, none, low, high) == (
        5,
        "High priority; oldest To Do; none has a due date",
    )
    old = page(6, board.TODO, priority="Low", created="2026-01-01T00:00:00.000Z")
    assert pick(fake, low, old)[0] == 2


def test_next_in_progress_prefers_latest_edit(fake: Fake) -> None:
    old = page(1, board.IN_PROGRESS, edited="2026-11-01T00:00:00.000Z")
    new = page(2, board.IN_PROGRESS, edited="2026-11-09T00:00:00.000Z")
    assert pick(fake, old, new)[0] == 2


def test_next_todo_nearest_due_then_undated_last(fake: Fake) -> None:
    undated = page(1, board.TODO, created="2026-01-01T00:00:00.000Z")
    later = page(2, board.TODO, due="2026-12-20")
    sooner = page(3, board.TODO, due="2026-11-20")
    assert pick(fake, undated, later, sooner) == (3, "nearest due date (2026-11-20)")
    newer = page(4, board.TODO, created="2026-06-01T00:00:00.000Z")
    assert pick(fake, newer, undated)[0] == 1


def test_next_never_picks_blocked_or_inbox(fake: Fake) -> None:
    blocked = page(1, board.BLOCKED, edited="2026-11-09T00:00:00.000Z")
    inbox = page(2)
    assert pick(fake, blocked, inbox) == (0, "nothing open")


def test_next_empty_board_prints_nothing_open(fake: Fake) -> None:
    fake.push(results())
    assert run("next") == (0, "nothing open\n", "")


# == Writes ================================================================== #


def note_text(body: Json) -> tuple[list[object], list[object]]:
    (block,) = board.arr(body["children"])
    paragraph = board.obj(board.obj(block).get("paragraph"))
    rich = board.arr(paragraph.get("rich_text"))
    return [board.obj(board.obj(r).get("text")).get("content") for r in rich], [
        board.obj(board.obj(r).get("annotations")).get("bold") for r in rich
    ]


def test_done_is_one_patch_then_note(fake: Fake) -> None:
    fake.push(results(page(570, board.IN_PROGRESS)))
    fake.push(page(570, board.DONE))
    fake.push({})
    code, out, _ = run("done", "#570", "--note", "v1.2.3-dev.0 · abc1234")
    assert code == 0
    assert out.startswith("#570  ✅ Done")
    patches = [c for c in fake.calls if c[0] == "PATCH" and c[1].startswith("/pages")]
    assert len(patches) == 1
    assert patches[0] == (
        "PATCH",
        f"/pages/{PAGE}",
        {
            "properties": {
                "Status": {"select": {"name": board.DONE}},
                "Conclusion Date": {"date": {"start": TODAY}},
            }
        },
    )
    method, path, body = fake.calls[2]
    assert (method, path) == ("PATCH", f"/blocks/{PAGE}/children")
    assert note_text(body) == (
        [f"{TODAY}:", " v1.2.3-dev.0 · abc1234"],
        [True, None],
    )


def test_done_without_note_skips_the_append(fake: Fake) -> None:
    fake.push(page(570, board.DONE))
    assert run("done", PAGE)[0] == 0
    assert [c[0] for c in fake.calls] == ["PATCH"]


def test_start_sets_in_progress(fake: Fake) -> None:
    fake.push(page(570, board.IN_PROGRESS))
    assert run("start", DASHED)[0] == 0
    assert fake.calls[0][2] == {
        "properties": {"Status": {"select": {"name": board.IN_PROGRESS}}}
    }


def test_note_is_one_bold_dated_paragraph(fake: Fake) -> None:
    fake.push({})
    code, _, _ = run("note", PAGE, "waiting on review")
    assert code == 0
    method, path, body = fake.calls[0]
    assert (method, path) == ("PATCH", f"/blocks/{PAGE}/children")
    assert note_text(body) == ([f"{TODAY}:", " waiting on review"], [True, None])


@pytest.mark.parametrize(
    ("project", "page_id"), [("me", ME), ("Work", WORK), ("OFFICE", WORK)]
)
def test_add_maps_project_and_prints_id(fake: Fake, project: str, page_id: str) -> None:
    fake.push(page(601, board.TODO))
    code, out, _ = run("add", "new thing", "--project", project)
    assert (code, out) == (0, "601\n")
    method, path, body = fake.calls[0]
    assert (method, path) == ("POST", "/pages")
    assert body["parent"] == {"type": "data_source_id", "data_source_id": "ds-tasks"}
    props = board.obj(body["properties"])
    assert props["☢️ Projeto"] == {"relation": [{"id": page_id}]}
    assert props["Status"] == {"select": {"name": board.TODO}}
    assert "Tags" not in props


def test_add_inbox_and_human_only(fake: Fake) -> None:
    fake.push(page(602))
    code, _, _ = run("add", "idea", "--project", "Me", "--inbox", "--human-only")
    assert code == 0
    props = board.obj(fake.calls[0][2]["properties"])
    assert "Status" not in props
    assert props["Tags"] == {"multi_select": [{"name": "Human Only"}]}


def test_add_priority_and_related(fake: Fake) -> None:
    fake.push(results(page(570, page_id=PAGE)))
    fake.push(results(page(571, page_id=WORK)))
    fake.push(page(602, board.TODO))
    code, _, _ = run(
        "add", "follow-up", "--project", "Me",
        "--priority", "Medium", "--related", "570", "--related", "#571",
    )  # fmt: skip
    assert code == 0
    props = board.obj(fake.calls[2][2]["properties"])
    assert props["Priority"] == {"select": {"name": "Medium"}}
    assert props["Related Tasks"] == {"relation": [{"id": PAGE}, {"id": WORK}]}


@pytest.mark.parametrize("value", ["high", "HIGH", "urgent"])
def test_add_refuses_high_and_junk_before_any_call(fake: Fake, value: str) -> None:
    code, _, err = run("add", "x", "--project", "Me", "--priority", value)
    assert code == board.EXIT_USAGE
    assert fake.calls == []
    if value != "urgent":
        assert "High is the user's; set it in Notion" in err


def test_add_unknown_project_lists_valid_names(fake: Fake) -> None:
    code, _, err = run("add", "x", "--project", "nope")
    assert code == board.EXIT_USAGE
    assert "valid: Me, office, Work" in err
    assert fake.calls == []


def test_add_without_agent_groups_takes_names_only(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    write_vault(tmp_path, groups=False)
    monkeypatch.setenv("HOME", str(tmp_path))
    cfg = board.load_config()
    assert cfg.project_id("work") == WORK
    with pytest.raises(board.BoardError):
        cfg.project_id("office")


# == priority and relate ===================================================== #


@pytest.mark.parametrize(
    ("value", "expected"),
    [("low", {"name": "Low"}), ("Medium", {"name": "Medium"}), ("none", None)],
)
def test_priority_sets_or_clears(fake: Fake, value: str, expected: Json | None) -> None:
    fake.push(page(5, board.TODO, priority="Low"))
    fake.push(page(5, board.TODO))
    code, _, _ = run("priority", PAGE, value)
    assert code == 0
    assert fake.calls[0][:2] == ("GET", f"/pages/{PAGE}")
    assert fake.calls[1] == (
        "PATCH",
        f"/pages/{PAGE}",
        {"properties": {"Priority": {"select": expected}}},
    )


def test_priority_refuses_high_without_calls(fake: Fake) -> None:
    code, _, err = run("priority", PAGE, "high")
    assert code == board.EXIT_USAGE
    assert "High is the user's; set it in Notion" in err
    assert fake.calls == []


@pytest.mark.parametrize("value", ["low", "none"])
def test_priority_will_not_touch_a_high_task(fake: Fake, value: str) -> None:
    fake.push(page(5, board.TODO, priority="High"))
    code, _, err = run("priority", PAGE, value)
    assert code == board.EXIT_USAGE
    assert "#5 is High" in err
    assert [c[0] for c in fake.calls] == ["GET"]


def test_relate_writes_the_union_and_keeps_old_links(fake: Fake) -> None:
    fake.push(results(page(570, page_id=WORK)))
    fake.push(page(5, board.TODO, related=(OTHER, WORK)))
    fake.push(page(5, board.TODO))
    code, _, _ = run("relate", PAGE, "570", OTHER)
    assert code == 0
    method, path, body = fake.calls[2]
    assert (method, path) == ("PATCH", f"/pages/{PAGE}")
    assert body == {
        "properties": {"Related Tasks": {"relation": [{"id": OTHER}, {"id": WORK}]}}
    }


def test_relate_skips_self_and_refuses_truncated_links(fake: Fake) -> None:
    fake.push(page(5, board.TODO))
    fake.push(page(5, board.TODO))
    assert run("relate", PAGE, PAGE)[0] == 0
    assert fake.calls[1][2] == {"properties": {"Related Tasks": {"relation": []}}}
    truncated = page(5, board.TODO)
    props = board.obj(truncated["properties"])
    props[board.RELATED] = {"relation": [], "has_more": True}
    truncated["properties"] = props
    fake.push(truncated)
    code, _, err = run("relate", PAGE, OTHER)
    assert code == board.EXIT_API
    assert "too many" in err
    assert len(fake.calls) == 3


# == Token and config ======================================================== #


def test_env_token_wins(fake: Fake, tmp_path: Path) -> None:
    (tmp_path / ".zshrc.local").write_text("export NOTION_TOKEN=from-file\n")
    fake.push(page(570, board.DONE))
    run("done", PAGE)
    assert fake.auth == [f"Bearer {TOKEN}"]


@pytest.mark.parametrize(
    "line",
    [
        "export NOTION_TOKEN=tok-123",
        "export NOTION_TOKEN='tok-123'",
        'export NOTION_TOKEN="tok-123"',
        "NOTION_TOKEN=tok-123",
        'export NOTION_TOKEN="tok-123"  # board integration',
    ],
)
def test_token_falls_back_to_zshrc_local(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, line: str
) -> None:
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.delenv("NOTION_TOKEN", raising=False)
    (tmp_path / ".zshrc.local").write_text(f"export OTHER=1\n{line}\n")
    assert board.load_token() == "tok-123"


def test_missing_token_exits_3_with_hint(
    fake: Fake, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv("NOTION_TOKEN")
    code, _, err = run("check")
    assert code == board.EXIT_CONFIG
    assert "~/.zshrc.local" in err


def test_missing_vault_config_exits_3(
    fake: Fake, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    (tmp_path / "vault" / "config" / "zen-flow.json").unlink()
    code, _, err = run("check")
    assert code == board.EXIT_CONFIG
    assert "zen-flow.json" in err


def test_api_error_exits_1_without_leaking_token(fake: Fake) -> None:
    fake.push({"message": "Could not find page"}, status=404)
    code, out, err = run("start", PAGE)
    assert code == board.EXIT_API
    assert "HTTP 404 Could not find page" in err
    assert TOKEN not in out + err


# == Rate limit ============================================================== #


def test_429_retries_then_succeeds(fake: Fake, monkeypatch: pytest.MonkeyPatch) -> None:
    waits: list[float] = []
    monkeypatch.setattr(board, "sleep", waits.append)
    fake.push({}, status=429, **{"retry-after": "2"})
    fake.push({}, status=429)
    fake.push(page(570, board.IN_PROGRESS))
    assert run("start", PAGE)[0] == 0
    assert waits == [2.0, 1.0]
    assert len(fake.calls) == 3


def test_three_429s_exit_1(fake: Fake) -> None:
    for _ in range(3):
        fake.push({}, status=429, **{"retry-after": "0"})
    code, _, err = run("start", PAGE)
    assert code == board.EXIT_API
    assert "HTTP 429" in err
    assert len(fake.calls) == 3


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
