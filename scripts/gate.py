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
#      __ _  __ _| |_ ___       z e n - g a t e
#     / _` |/ _` | __/ _ \
#    | (_| | (_| | ||  __/
#     \__, |\__,_|\__\___|
#     |___/
#
# ------------------------------------------------------------------------------
#
#  NAME
#      gate.py  --  flag banned ways to get a branch green
#
#  SYNOPSIS
#      zen-gate [--base dev] [--json]
#
#  DESCRIPTION
#      Scans only the lines `git diff <base>...HEAD` adds (docs excluded) for:
#        skip-focus     .only( .skip xit( @pytest.mark.skip t.Skip( #[ignore]
#        test-deleted   a deleted test file
#        tests-removed  more test cases removed than added
#        timeout-bump   testTimeout, jest.setTimeout, --timeout
#        auth-bypass    service_role, bypassRLS, row_security = off, and
#                       systemCtx/systemDb/admin clients outside fixtures
#        empty-catch    catch {} or `except Exception: pass`
#        mock-changed   vi.mock/jest.mock of a non-test file this diff changes
#      `shortcut:` markers are listed as follow-ups, not failures.
#
#      `.zen-gate-allow` at the repo root lists accepted exceptions, one per
#      line: `<path glob> [rule]` (no rule = every rule). `#` starts a comment.
#
#  OUTPUT
#      One `file:line rule` per hit.
#
#  EXIT CODES
#      0  clean      1  hits      2  git error
#
#  AUTHOR
#      vzsoares.
#
# ==============================================================================
"""Scan a branch diff for skipped tests, bypasses and other fake greens."""

from __future__ import annotations

import fnmatch
import json
import posixpath
import re
import subprocess
import sys
from dataclasses import asdict, dataclass, field
from pathlib import Path

import typer

ALLOW_FILE = ".zen-gate-allow"
DOC_SUFFIXES = (".md", ".mdx", ".txt", ".rst", ".typ")

TEST_PATH_RE = re.compile(
    r"(^|/)(tests?|__tests__|spec)/"
    r"|(^|/)test_[^/]+\.py$"
    r"|_test\.(py|go)$"
    r"|\.(test|spec)\.[cm]?[jt]sx?$"
)
FIXTURE_PATH_RE = re.compile(
    r"fixture|(^|/)seeds?[/.]|conftest\.py$|__mocks__/", re.IGNORECASE
)
TEST_CASE_RE = re.compile(
    r"^\s*(?:it|test)(?:\.each\b[^(]*)?\s*\("
    r"|^\s*(?:async\s+)?def\s+test_"
    r"|^\s*func\s+Test[A-Z_]"
    r"|^\s*#\[(?:tokio::)?test\]"
)

LINE_RULES: tuple[tuple[str, re.Pattern[str]], ...] = (
    (
        "skip-focus",
        re.compile(
            r"\.only\s*\(|\b(?:it|test|describe|context|suite)\.skip\b|\.skip\s*\("
            r"|\bx(?:it|describe|test)\s*\("
            r"|@pytest\.mark\.skip|\bt\.Skip(?:Now|f)?\(|#\[ignore\]"
        ),
    ),
    ("timeout-bump", re.compile(r"testTimeout|jest\.setTimeout|--timeout\b")),
    (
        "auth-bypass",
        re.compile(r"service_role|bypass_?rls|row_security\s*=\s*off", re.IGNORECASE),
    ),
    (
        "empty-catch",
        re.compile(
            r"\bcatch\s*(?:\([^)]*\))?\s*\{\s*\}"
            r"|^\s*except(?:\s+(?:Base)?Exception\b[^:]*)?\s*:\s*pass\b"
        ),
    ),
)
ADMIN_CLIENT_RE = re.compile(r"\b(?:systemCtx|systemDb|\w*[aA]dmin(?:Client|Db)\w*)\b")
OPEN_CATCH_RE = re.compile(r"\bcatch\s*(?:\([^)]*\))?\s*\{\s*$")
OPEN_EXCEPT_RE = re.compile(r"^\s*except(?:\s+(?:Base)?Exception\b[^:]*)?\s*:\s*$")
MOCK_RE = re.compile(r"\b(?:vi|jest)\.mock\(\s*['\"]([^'\"]+)['\"]")
SHORTCUT_RE = re.compile(r"\bshortcut:", re.IGNORECASE)
HUNK_RE = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")
CODE_SUFFIX_RE = re.compile(r"\.(?:[cm]?[jt]sx?|py|go|rs|vue|svelte)$")


@dataclass
class Hit:
    file: str
    line: int
    rule: str
    text: str = ""

    def __str__(self) -> str:
        return f"{self.file}:{self.line} {self.rule}"


@dataclass
class FileDiff:
    path: str
    status: str = "M"
    added: list[tuple[int, str]] = field(default_factory=list)
    removed: list[str] = field(default_factory=list)


@dataclass
class Report:
    hits: list[Hit] = field(default_factory=list)
    followups: list[Hit] = field(default_factory=list)

    @property
    def clean(self) -> bool:
        return not self.hits


# --------------------------------------------------------------------------- #
# Diff parsing (pure)                                                          #
# --------------------------------------------------------------------------- #


def parse_diff(text: str) -> list[FileDiff]:
    """Parse `git diff --unified=0` output into per-file added/removed lines."""
    files: list[FileDiff] = []
    cur: FileDiff | None = None
    new_line = 0
    in_hunk = False
    for raw in text.splitlines():
        if raw.startswith("diff --git "):
            cur = FileDiff(path=raw.split(" b/", 1)[-1])
            files.append(cur)
            in_hunk = False
            continue
        if cur is None:
            continue
        if m := HUNK_RE.match(raw):
            new_line = int(m.group(3))
            in_hunk = True
        elif not in_hunk:
            if raw.startswith("deleted file mode"):
                cur.status = "D"
            elif raw.startswith("new file mode"):
                cur.status = "A"
            elif raw.startswith("+++ b/"):
                cur.path = raw[6:]
        elif raw.startswith("+"):
            cur.added.append((new_line, raw[1:]))
            new_line += 1
        elif raw.startswith("-"):
            cur.removed.append(raw[1:])
    return files


def is_test_path(path: str) -> bool:
    return bool(TEST_PATH_RE.search(path))


def is_doc(path: str) -> bool:
    return path.lower().endswith(DOC_SUFFIXES)


def _module_key(path: str) -> str:
    return CODE_SUFFIX_RE.sub("", path).removesuffix("/index")


def mock_target(spec: str, from_file: str) -> str | None:
    """Repo-relative module key for a mock specifier; None for packages."""
    if spec.startswith("."):
        joined = posixpath.normpath(posixpath.join(posixpath.dirname(from_file), spec))
        return _module_key(joined)
    for alias in ("@/", "~/", "#/", "src/"):
        if spec.startswith(alias):
            return _module_key(spec[len(alias) :])
    return None


def _mock_hits(fd: FileDiff, changed: dict[str, str]) -> list[Hit]:
    hits: list[Hit] = []
    for lineno, text in fd.added:
        for spec in MOCK_RE.findall(text):
            key = mock_target(spec, fd.path)
            if key is None:
                continue
            for path_key, path in changed.items():
                if path_key == key or path_key.endswith("/" + key):
                    hits.append(
                        Hit(fd.path, lineno, "mock-changed", f"{spec} -> {path}")
                    )
                    break
    return hits


def _multiline_catch(fd: FileDiff) -> list[Hit]:
    hits: list[Hit] = []
    for (ln, text), (next_ln, next_text) in zip(fd.added, fd.added[1:]):
        if next_ln != ln + 1:
            continue
        closes = next_text.strip()
        empty_js = OPEN_CATCH_RE.search(text) and closes.startswith("}")
        empty_py = OPEN_EXCEPT_RE.match(text) and closes == "pass"
        if empty_js or empty_py:
            hits.append(Hit(fd.path, ln, "empty-catch", text.strip()))
    return hits


def scan(files: list[FileDiff]) -> Report:
    report = Report()
    changed = {
        _module_key(fd.path): fd.path
        for fd in files
        if fd.status != "D" and not is_test_path(fd.path)
    }
    cases_added = cases_removed = 0
    removed_by_file: list[tuple[str, int]] = []

    for fd in files:
        if is_doc(fd.path):
            continue
        if fd.status == "D":
            if is_test_path(fd.path):
                report.hits.append(Hit(fd.path, 0, "test-deleted"))
            continue
        for lineno, text in fd.added:
            for rule, pattern in LINE_RULES:
                if pattern.search(text):
                    report.hits.append(Hit(fd.path, lineno, rule, text.strip()))
            if ADMIN_CLIENT_RE.search(text) and not FIXTURE_PATH_RE.search(fd.path):
                report.hits.append(Hit(fd.path, lineno, "auth-bypass", text.strip()))
            if SHORTCUT_RE.search(text):
                report.followups.append(Hit(fd.path, lineno, "shortcut", text.strip()))
        report.hits.extend(_multiline_catch(fd))
        if is_test_path(fd.path):
            report.hits.extend(_mock_hits(fd, changed))
            added = sum(1 for _, t in fd.added if TEST_CASE_RE.search(t))
            removed = sum(1 for t in fd.removed if TEST_CASE_RE.search(t))
            cases_added += added
            cases_removed += removed
            if removed > added:
                removed_by_file.append((fd.path, removed - added))

    for fd in files:
        if fd.status == "D" and is_test_path(fd.path) and not is_doc(fd.path):
            cases_removed += sum(1 for t in fd.removed if TEST_CASE_RE.search(t))
    if cases_removed > cases_added:
        detail = f"-{cases_removed}/+{cases_added}"
        for path, _ in removed_by_file or [("(diff)", 0)]:
            report.hits.append(Hit(path, 0, "tests-removed", detail))
    report.hits.sort(key=lambda h: (h.file, h.line, h.rule))
    return report


# --------------------------------------------------------------------------- #
# Allow list                                                                   #
# --------------------------------------------------------------------------- #


def parse_allow(text: str) -> list[tuple[str, str]]:
    rules: list[tuple[str, str]] = []
    for line in text.splitlines():
        line = line.split("#", 1)[0].strip()
        if not line:
            continue
        parts = line.split()
        rules.append((parts[0], parts[1] if len(parts) > 1 else ""))
    return rules


def allowed(hit: Hit, allow: list[tuple[str, str]]) -> bool:
    return any(
        fnmatch.fnmatch(hit.file, glob) and rule in ("", hit.rule)
        for glob, rule in allow
    )


def apply_allow(report: Report, allow: list[tuple[str, str]]) -> Report:
    return Report(
        hits=[h for h in report.hits if not allowed(h, allow)],
        followups=[f for f in report.followups if not allowed(f, allow)],
    )


# --------------------------------------------------------------------------- #
# git                                                                          #
# --------------------------------------------------------------------------- #


class GateError(Exception):
    pass


def branch_diff(repo: Path, base: str) -> str:
    result = subprocess.run(
        ["git", "diff", "--unified=0", "--no-color", "--no-ext-diff", f"{base}...HEAD"],
        cwd=repo,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise GateError(result.stderr.strip() or f"git diff {base}...HEAD failed")
    return result.stdout


def run_gate(repo: Path, base: str) -> Report:
    report = scan(parse_diff(branch_diff(repo, base)))
    allow_path = repo / ALLOW_FILE
    allow = parse_allow(allow_path.read_text()) if allow_path.exists() else []
    return apply_allow(report, allow)


# --------------------------------------------------------------------------- #
# CLI                                                                          #
# --------------------------------------------------------------------------- #

app = typer.Typer(add_completion=False, help=__doc__)


@app.command()
def main(
    base: str = typer.Option("dev", "--base", help="Branch the diff is taken against."),
    as_json: bool = typer.Option(False, "--json", help="JSON output."),
) -> None:
    """Flag fake-green patterns in `git diff <base>...HEAD`; exit 1 on hits."""
    try:
        report = run_gate(Path.cwd(), base)
    except GateError as exc:
        print(f"zen-gate: {exc}", file=sys.stderr)
        raise typer.Exit(2) from exc
    if as_json:
        print(
            json.dumps(
                {
                    "clean": report.clean,
                    "hits": [asdict(h) for h in report.hits],
                    "followups": [asdict(h) for h in report.followups],
                }
            )
        )
    else:
        for hit in report.hits:
            print(hit)
        for f in report.followups:
            print(f"{f} (follow-up)")
    raise typer.Exit(0 if report.clean else 1)


if __name__ == "__main__":
    app()
