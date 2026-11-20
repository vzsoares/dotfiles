#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["pytest>=8.0"]
# ///
"""Tests for .claude/skills/pi/scripts/refresh.py. No network.

Run with:  uv run agents/test/test_pi_refresh.py
"""

from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path
from types import ModuleType

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / ".claude/skills/pi/scripts/refresh.py"


def load() -> ModuleType:
    spec = importlib.util.spec_from_file_location("refresh", SCRIPT)
    assert spec is not None and spec.loader is not None
    mod = importlib.util.module_from_spec(spec)
    sys.modules["refresh"] = mod
    spec.loader.exec_module(mod)
    return mod


rf = load()

INDEX_HTML = """
<a href="/latest">Home</a><a href="/latest/cli">CLI</a>
<a href="/latest/cli#invocation-and-output">x</a><a href="/latest/mcp">MCP</a>
<a href="/assets/docs/docs.css?v=1">css</a><a href="https://pi.dev/changelog">c</a>
"""

PAGE_HTML = """
<nav>Skip me</nav>
<main><article class="docs-article"><h1>MCP   Servers</h1>
<p>Pi connects over <code>stdio</code>.</p>
<ul><li>one</li><li>two</li></ul>
<pre><code>pi mcp add x -- cmd
  --flag</code></pre>
</article></main><footer>also skipped</footer>
"""

FEED = """<rss><channel><title>Pi Changelog</title>
<item>
  <title>Pi 1.6.0</title>
</item><item><title>Pi 1.5.2</title></item></channel></rss>"""


def test_page_slugs_dedupes_and_keeps_only_doc_pages() -> None:
    assert rf.page_slugs(INDEX_HTML) == ["", "cli", "mcp"]


def test_page_text_keeps_article_only_as_markdown() -> None:
    text = rf.page_text(PAGE_HTML)
    assert "Skip me" not in text and "also skipped" not in text
    assert text.startswith("# MCP Servers\n")
    assert "Pi connects over `stdio`." in text
    assert "- one\n- two" in text
    assert "```\npi mcp add x -- cmd\n  --flag\n```" in text


def test_latest_version_takes_the_first_item() -> None:
    assert rf.latest_version(FEED) == "1.6.0"
    cdata = "<item><title><![CDATA[Pi 1.7.0]]></title></item>"
    assert rf.latest_version(cdata) == "1.7.0"


def test_latest_version_rejects_a_feed_without_releases() -> None:
    with pytest.raises(ValueError):
        rf.latest_version("<rss><channel><title>Pi</title></channel></rss>")


def test_drift_reports_added_removed_changed() -> None:
    old = {"a": "1", "b": "2", "c": "3"}
    new = {"a": "1", "b": "9", "d": "4"}
    assert rf.drift(old, new) == {"added": ["d"], "removed": ["c"], "changed": ["b"]}
    assert rf.drift(new, new) == {"added": [], "removed": [], "changed": []}


def test_load_manifest_missing_and_present(tmp_path: Path) -> None:
    assert rf.load_manifest(tmp_path / "none.json") == {}
    p = tmp_path / "SOURCES.json"
    p.write_text(json.dumps({"version": "1.6.0", "pages": {"cli": {"sha256": "abc"}}}))
    assert rf.load_manifest(p) == {"cli": "abc"}
    p.write_text(json.dumps({"pages": ["cli"]}))
    with pytest.raises(TypeError):
        rf.load_manifest(p)
    p.write_text(json.dumps({"pages": {"cli": "abc"}}))
    with pytest.raises(TypeError):
        rf.load_manifest(p)


def test_main_exit_codes_and_write(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    pages = {rf.CHANGELOG: FEED, rf.INDEX: INDEX_HTML}
    for slug in ("cli", "mcp"):
        pages[f"{rf.INDEX}/{slug}"] = PAGE_HTML.replace("MCP", slug)
    monkeypatch.setattr(rf, "fetch", lambda url: pages[url])
    manifest = tmp_path / "SOURCES.json"
    args = ["--out", str(tmp_path / "out"), "--delay", "0", "--manifest", str(manifest)]

    assert rf.main([*args, "--write"]) == 3
    data = json.loads(manifest.read_text())
    assert data["version"] == "1.6.0" and sorted(data["pages"]) == [
        "cli",
        "index",
        "mcp",
    ]
    assert (tmp_path / "out" / "cli.md").read_text().startswith("# cli Servers")

    assert rf.main(args) == 0

    pages[f"{rf.INDEX}/mcp"] = "<article><h1>changed</h1></article>"
    assert rf.main(args) == 3

    def boom(url: str) -> str:
        raise OSError("offline")

    monkeypatch.setattr(rf, "fetch", boom)
    assert rf.main(args) == 1


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, "-q"]))
