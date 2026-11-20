#!/usr/bin/env python3
"""Re-scrape docs.pi.dev/latest and diff it against references/SOURCES.json.

  refresh.py [--out DIR] [--delay SECS] [--write]

Saves each page's article text to DIR (default: a new temp dir), prints the
pi version (newest changelog entry) and which pages were added, removed or
changed since SOURCES.json. --write records the new hashes, version and date.
Exit 0: no drift; 3: drift found; 1: fetch or parse error. Stdlib only.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
import tempfile
import time
import urllib.request
from datetime import date
from html.parser import HTMLParser
from pathlib import Path

BASE = "https://docs.pi.dev"
INDEX = f"{BASE}/latest"
CHANGELOG = "https://pi.dev/changelog.xml"
UA = "dotfiles-pi-skill-refresh (+https://github.com/vzsoares/dotfiles)"
SKILL_DIR = Path(__file__).resolve().parent.parent
MANIFEST = SKILL_DIR / "references" / "SOURCES.json"

BLOCK = {"p", "div", "section", "ul", "ol", "table", "tr", "blockquote", "br"}
HEADINGS = {"h1": "#", "h2": "##", "h3": "###", "h4": "####"}


def fetch(url: str) -> str:
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    with urllib.request.urlopen(req, timeout=30) as resp:
        body: bytes = resp.read()
    return body.decode("utf-8", errors="replace")


def page_slugs(index_html: str) -> list[str]:
    """Page slugs linked from the index, '' for the index itself."""
    slugs = {""}
    for m in re.finditer(r'href="/latest/([a-z0-9-]+)(?:#[^"]*)?"', index_html):
        slugs.add(m.group(1))
    return sorted(slugs)


class _Article(HTMLParser):
    """Collects the text of <article> as light markdown."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.depth = 0
        self.pre = 0
        self.out: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        if tag == "article":
            self.depth += 1
        if not self.depth:
            return
        if tag in HEADINGS:
            self.out.append(f"\n\n{HEADINGS[tag]} ")
        elif tag == "pre":
            self.pre += 1
            self.out.append("\n```\n")
        elif tag == "li":
            self.out.append("\n- ")
        elif tag in BLOCK:
            self.out.append("\n")
        elif tag == "code" and not self.pre:
            self.out.append("`")

    def handle_endtag(self, tag: str) -> None:
        if not self.depth:
            return
        if tag == "article":
            self.depth -= 1
        elif tag == "pre":
            self.pre = max(0, self.pre - 1)
            self.out.append("\n```\n")
        elif tag == "code" and not self.pre:
            self.out.append("`")
        elif tag in HEADINGS or tag in BLOCK:
            self.out.append("\n")

    def handle_data(self, data: str) -> None:
        if not self.depth:
            return
        self.out.append(data if self.pre else re.sub(r"\s+", " ", data))


def page_text(html: str) -> str:
    parser = _Article()
    parser.feed(html)
    text = "".join(parser.out)
    text = re.sub(r"[ \t]+\n", "\n", text)
    return re.sub(r"\n{3,}", "\n\n", text).strip() + "\n"


def latest_version(changelog_xml: str) -> str:
    m = re.search(r"<item>\s*<title>\s*Pi\s+([0-9][^<\s]*)\s*</title>", changelog_xml)
    if not m:
        raise ValueError("no 'Pi <version>' item in the changelog feed")
    return m.group(1)


def digest(text: str) -> str:
    return hashlib.sha256(text.encode()).hexdigest()


def drift(old: dict[str, str], new: dict[str, str]) -> dict[str, list[str]]:
    return {
        "added": sorted(set(new) - set(old)),
        "removed": sorted(set(old) - set(new)),
        "changed": sorted(k for k in set(old) & set(new) if old[k] != new[k]),
    }


def load_manifest(path: Path) -> dict[str, str]:
    if not path.exists():
        return {}
    data = json.loads(path.read_text())
    pages = data.get("pages", {})
    if not isinstance(pages, dict):
        raise TypeError(f"{path}: 'pages' is not an object")
    out: dict[str, str] = {}
    for slug, entry in pages.items():
        if not isinstance(entry, dict) or not isinstance(entry.get("sha256"), str):
            raise TypeError(f"{path}: page {slug!r} has no sha256")
        out[str(slug)] = entry["sha256"]
    return out


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--out", type=Path, help="where to save page text")
    ap.add_argument("--delay", type=float, default=1.0, help="seconds between fetches")
    ap.add_argument("--write", action="store_true", help="update SOURCES.json")
    ap.add_argument("--manifest", type=Path, default=MANIFEST)
    args = ap.parse_args(argv)

    out: Path = args.out or Path(tempfile.mkdtemp(prefix="pi-docs-"))
    out.mkdir(parents=True, exist_ok=True)
    try:
        version = latest_version(fetch(CHANGELOG))
        slugs = page_slugs(fetch(INDEX))
        pages: dict[str, str] = {}
        for i, slug in enumerate(slugs):
            if i:
                time.sleep(args.delay)
            text = page_text(fetch(f"{INDEX}/{slug}" if slug else INDEX))
            name = slug or "index"
            (out / f"{name}.md").write_text(text)
            pages[name] = digest(text)
        old = load_manifest(args.manifest)
    except (OSError, ValueError, TypeError) as e:
        print(f"refresh: {e}", file=sys.stderr)
        return 1

    d = drift(old, pages)
    print(f"pi {version}: {len(pages)} pages saved to {out}")
    for kind, names in d.items():
        for name in names:
            print(f"{kind:8} {INDEX if name == 'index' else f'{INDEX}/{name}'}")
    if args.write:
        args.manifest.write_text(
            json.dumps(
                {
                    "source": INDEX,
                    "version": version,
                    "fetched": date.today().isoformat(),
                    "pages": {k: {"sha256": v} for k, v in pages.items()},
                },
                indent=2,
                sort_keys=True,
            )
            + "\n"
        )
        print(f"wrote {args.manifest}")
    return 3 if any(d.values()) else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
