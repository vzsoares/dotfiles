#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "typer>=0.12",
#     "pytest>=8.0",
# ]
# ///
"""Tests for scripts/gate.py.

Run with:  uv run scripts/test_gate.py
"""

from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
from pathlib import Path

import pytest
from typer.testing import CliRunner

_spec = importlib.util.spec_from_file_location(
    "gate", Path(__file__).parent / "gate.py"
)
assert _spec is not None and _spec.loader is not None
gate = importlib.util.module_from_spec(_spec)
sys.modules["gate"] = gate
_spec.loader.exec_module(gate)


# --------------------------------------------------------------------------- #
# Fixtures                                                                      #
# --------------------------------------------------------------------------- #


def _git(*args: str, cwd: Path) -> str:
    return subprocess.run(
        ["git", *args], cwd=cwd, capture_output=True, text=True, check=True
    ).stdout


@pytest.fixture
def repo(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """A repo on `feature`, branched from `dev`, with src + tests committed."""
    r = tmp_path / "repo"
    r.mkdir()
    monkeypatch.chdir(r)
    _git("init", "-q", "-b", "dev", cwd=r)
    _git("config", "user.email", "t@t.dev", cwd=r)
    _git("config", "user.name", "tester", cwd=r)
    (r / "src").mkdir()
    (r / "src" / "auth.ts").write_text("export const a = 1\n")
    (r / "src" / "auth.test.ts").write_text("it('a', () => {})\nit('b', () => {})\n")
    _git("add", ".", cwd=r)
    _git("commit", "-q", "-m", "init", cwd=r)
    _git("checkout", "-q", "-b", "feature", cwd=r)
    return r


def _commit(repo: Path, files: dict[str, str | None]) -> None:
    for name, content in files.items():
        path = repo / name
        if content is None:
            path.unlink()
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
    _git("add", "-A", cwd=repo)
    _git("commit", "-q", "-m", "change", cwd=repo)


def _rules(text: str, path: str = "src/x.ts") -> list[str]:
    diff = f"diff --git a/{path} b/{path}\n--- a/{path}\n+++ b/{path}\n@@ -0,0 +1,{len(text.splitlines())} @@\n"
    diff += "".join(f"+{line}\n" for line in text.splitlines())
    return [h.rule for h in gate.scan(gate.parse_diff(diff)).hits]


# --------------------------------------------------------------------------- #
# Line rules                                                                    #
# --------------------------------------------------------------------------- #


@pytest.mark.parametrize(
    "line",
    [
        "it.only('x', () => {})",
        "describe.skip('x', () => {})",
        "test.skip.each([1])('x', () => {})",
        "xit('x', () => {})",
        "@pytest.mark.skip(reason='flaky')",
        "@pytest.mark.skipif(True, reason='x')",
        '    t.Skip("later")',
        "#[ignore]",
        "pytest.skip('x')",
    ],
)
def test_skip_focus(line: str) -> None:
    assert _rules(line) == ["skip-focus"]


@pytest.mark.parametrize(
    "line",
    ["  testTimeout: 60000,", "jest.setTimeout(30000)", "pytest --timeout 600"],
)
def test_timeout_bump(line: str) -> None:
    assert _rules(line) == ["timeout-bump"]


@pytest.mark.parametrize(
    "line",
    [
        "const key = process.env.SUPABASE_SERVICE_ROLE_KEY // service_role",
        "await prisma.$executeRaw`SET bypassRLS`",
        "SET row_security = off;",
        "const db = systemDb()",
        "const c = createAdminClient(url)",
        "return systemCtx",
    ],
)
def test_auth_bypass(line: str) -> None:
    assert _rules(line) == ["auth-bypass"]


def test_admin_client_allowed_in_fixtures() -> None:
    assert _rules("const db = createAdminClient()", "tests/fixtures/db.ts") == []
    assert _rules("db = systemDb()", "tests/conftest.py") == []


@pytest.mark.parametrize(
    "text",
    [
        "try { x() } catch {}",
        "try { x() } catch (e) { }",
        "except Exception: pass",
        "except: pass",
        "try {\n} catch (err) {\n}",
        "try:\n    x()\nexcept Exception:\n    pass",
    ],
)
def test_empty_catch(text: str) -> None:
    assert _rules(text) == ["empty-catch"]


@pytest.mark.parametrize(
    "line",
    [
        "catch (e) { log(e) }",
        "except ValueError: pass",
        "args.skip = True",
        "only(x)",
        "const timeout = 5",
        "adminPanel()",
    ],
)
def test_clean_lines(line: str) -> None:
    assert _rules(line) == []


def test_docs_are_ignored() -> None:
    assert _rules("run `zen-await --timeout 4h`", "README.md") == []


def test_shortcut_is_followup_not_hit() -> None:
    diff = "diff --git a/a.py b/a.py\n+++ b/a.py\n@@ -1,0 +5 @@\n+x = 1  # shortcut: hardcoded\n"
    report = gate.scan(gate.parse_diff(diff))
    assert report.clean
    assert [str(f) for f in report.followups] == ["a.py:5 shortcut"]


# --------------------------------------------------------------------------- #
# Diff parsing                                                                  #
# --------------------------------------------------------------------------- #


def test_parse_diff_line_numbers_and_sql_comments() -> None:
    diff = (
        "diff --git a/q.sql b/q.sql\n--- a/q.sql\n+++ b/q.sql\n"
        "@@ -3 +3,2 @@\n--- old comment\n+-- new\n+SELECT 1;\n"
        "@@ -10,0 +11 @@\n+SELECT 2;\n"
    )
    [fd] = gate.parse_diff(diff)
    assert fd.path == "q.sql"
    assert fd.added == [(3, "-- new"), (4, "SELECT 1;"), (11, "SELECT 2;")]
    assert fd.removed == ["-- old comment"]


@pytest.mark.parametrize(
    ("path", "is_test"),
    [
        ("src/a.test.ts", True),
        ("src/a.spec.tsx", True),
        ("tests/x.py", True),
        ("scripts/test_gate.py", True),
        ("pkg/a_test.go", True),
        ("src/__tests__/a.js", True),
        ("src/testing.ts", False),
        ("src/latest.py", False),
    ],
)
def test_is_test_path(path: str, is_test: bool) -> None:
    assert gate.is_test_path(path) is is_test


@pytest.mark.parametrize(
    ("spec", "src", "key"),
    [
        ("./auth", "src/auth.test.ts", "src/auth"),
        ("../lib/db.ts", "src/x/a.test.ts", "src/lib/db"),
        ("@/lib/db", "src/a.test.ts", "lib/db"),
        ("axios", "src/a.test.ts", None),
    ],
)
def test_mock_target(spec: str, src: str, key: str | None) -> None:
    assert gate.mock_target(spec, src) == key


# --------------------------------------------------------------------------- #
# Allow list                                                                    #
# --------------------------------------------------------------------------- #


def test_allow_rules() -> None:
    allow = gate.parse_allow(
        "# comment\nscripts/gate.py\nscripts/await.py timeout-bump  # flag\n"
    )
    assert allow == [("scripts/gate.py", ""), ("scripts/await.py", "timeout-bump")]
    assert gate.allowed(gate.Hit("scripts/gate.py", 1, "skip-focus"), allow)
    assert gate.allowed(gate.Hit("scripts/await.py", 1, "timeout-bump"), allow)
    assert not gate.allowed(gate.Hit("scripts/await.py", 1, "skip-focus"), allow)
    report = gate.Report(followups=[gate.Hit("scripts/gate.py", 3, "shortcut")])
    assert gate.apply_allow(report, allow).followups == []


# --------------------------------------------------------------------------- #
# End to end on a temp repo                                                     #
# --------------------------------------------------------------------------- #


def test_clean_branch(repo: Path) -> None:
    _commit(repo, {"src/auth.ts": "export const a = 2\n"})
    assert gate.run_gate(repo, "dev").clean


def test_only_added_lines_count(repo: Path) -> None:
    _git("checkout", "-q", "dev", cwd=repo)
    _commit(repo, {"src/old.ts": "it.only('x')\n"})
    _git("checkout", "-q", "feature", cwd=repo)
    _git("merge", "-q", "dev", cwd=repo)
    _commit(repo, {"src/auth.ts": "export const a = 3\n"})
    assert gate.run_gate(repo, "dev").clean


def test_deleted_test_file(repo: Path) -> None:
    _commit(repo, {"src/auth.test.ts": None})
    rules = {h.rule for h in gate.run_gate(repo, "dev").hits}
    assert rules == {"test-deleted", "tests-removed"}


def test_fewer_test_cases(repo: Path) -> None:
    _commit(repo, {"src/auth.test.ts": "it('a', () => {})\n"})
    hits = gate.run_gate(repo, "dev").hits
    assert [(h.file, h.rule, h.text) for h in hits] == [
        ("src/auth.test.ts", "tests-removed", "-1/+0")
    ]


def test_moved_tests_are_fine(repo: Path) -> None:
    _commit(
        repo,
        {
            "src/auth.test.ts": "it('a', () => {})\n",
            "src/auth2.test.ts": "it('b', () => {})\n",
        },
    )
    assert gate.run_gate(repo, "dev").clean


def test_mock_of_changed_file(repo: Path) -> None:
    _commit(
        repo,
        {
            "src/auth.ts": "export const a = 9\n",
            "src/auth.test.ts": "vi.mock('./auth')\nit('a', () => {})\nit('b', () => {})\n",
        },
    )
    hits = gate.run_gate(repo, "dev").hits
    assert [(h.file, h.line, h.rule) for h in hits] == [
        ("src/auth.test.ts", 1, "mock-changed")
    ]


def test_mock_of_unchanged_file_is_fine(repo: Path) -> None:
    _commit(
        repo,
        {
            "src/auth.test.ts": "vi.mock('./auth')\nit('a', () => {})\nit('b', () => {})\n"
        },
    )
    assert gate.run_gate(repo, "dev").clean


def test_allow_file_applies(repo: Path) -> None:
    _commit(
        repo,
        {
            "src/auth.ts": "jest.setTimeout(1)\n",
            ".zen-gate-allow": "src/*.ts timeout-bump\n",
        },
    )
    assert gate.run_gate(repo, "dev").clean


def test_cli_exit_codes(repo: Path) -> None:
    runner = CliRunner()
    _commit(repo, {"src/auth.ts": "try { a() } catch {}\n// shortcut: later\n"})
    res = runner.invoke(gate.app, ["--json"])
    assert res.exit_code == 1
    out = json.loads(res.stdout)
    assert [h["rule"] for h in out["hits"]] == ["empty-catch"]
    assert [f["line"] for f in out["followups"]] == [2]

    text = runner.invoke(gate.app, [])
    assert text.stdout.splitlines() == [
        "src/auth.ts:1 empty-catch",
        "src/auth.ts:2 shortcut (follow-up)",
    ]

    assert runner.invoke(gate.app, ["--base", "nope"]).exit_code == 2


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
