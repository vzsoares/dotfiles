"""Exits 0 when the file at REV_OLD and REV_NEW differs only in the root
package's own version, the one thing zen-release's bump writes.

Usage: release-fits.py REV_OLD REV_NEW PATH

Each file is parsed, the root version(s) are masked in both sides, and the
rest must be identical. Any parse error, duplicate key, odd version value or
unknown file name exits 1.
"""

from __future__ import annotations

import json
import re
import subprocess
import sys
import tomllib
from pathlib import Path

MASK = "\0version"
VALUE = r"[0-9A-Za-z][0-9A-Za-z.+_-]*"
VALUE_RE = re.compile(rf"{VALUE}")

JsonObj = dict[str, object]


class Refuse(Exception):
    pass


def same(a: object, b: object) -> bool:
    """Equality that keeps True, 1 and 1.0 apart (Python's == does not)."""
    if type(a) is not type(b):
        return False
    if isinstance(a, dict) and isinstance(b, dict):
        return a.keys() == b.keys() and all(same(a[k], b[k]) for k in a)
    if isinstance(a, list) and isinstance(b, list):
        return len(a) == len(b) and all(map(same, a, b))
    return a == b


def show(rev: str, path: str) -> str:
    out = subprocess.run(
        ["git", "show", f"{rev}:{path}"], capture_output=True, check=True
    )
    return out.stdout.decode("utf-8")


def unique(pairs: list[tuple[str, object]]) -> JsonObj:
    obj = dict(pairs)
    if len(obj) != len(pairs):
        raise Refuse("duplicate key")
    return obj


def mask(table: object, *keys: str) -> None:
    """Replaces table[keys...]["version"] with MASK when it is a plain string."""
    for key in keys:
        if not isinstance(table, dict) or not isinstance(table.get(key), dict):
            return
        table = table[key]
    if not isinstance(table, dict) or "version" not in table:
        return
    value = table["version"]
    if not isinstance(value, str) or not VALUE_RE.fullmatch(value):
        raise Refuse(f"unexpected version value {value!r}")
    table["version"] = MASK


def masked_json(text: str, lock: bool) -> object:
    doc = json.loads(text, object_pairs_hook=unique)
    if not isinstance(doc, dict):
        raise Refuse("root is not an object")
    mask(doc)
    if lock:
        mask(doc, "packages", "")
    return doc


def masked_toml(text: str, tables: tuple[tuple[str, ...], ...]) -> object:
    doc = tomllib.loads(text)
    for keys in tables:
        mask(doc, *keys)
    return doc


def masked_text(text: str, line_re: re.Pattern[str]) -> list[str]:
    """Lines with the value of every version line cut out; others verbatim."""
    out: list[str] = []
    for line in text.split("\n"):
        m = line_re.match(line)
        out.append(m.group("pre") + MASK + m.group("post") if m else line)
    return out


PY_RE = re.compile(
    r'^(?P<pre>\s*(?:VERSION|VERSAO|__version__)(?:\s*:\s*\w+)?\s*=\s*)"'
    rf'{VALUE}"(?P<post>[ \t]*(?:#.*)?)$'
)
PUBSPEC_RE = re.compile(rf"^(?P<pre>version:)[ \t]*{VALUE}(?P<post>(?:[ \t]*#.*)?)$")


def parse(name: str, text: str) -> object:
    match name:
        case "package.json":
            return masked_json(text, lock=False)
        case "package-lock.json":
            return masked_json(text, lock=True)
        case "pyproject.toml":
            return masked_toml(text, (("project",), ("tool", "poetry")))
        case "Cargo.toml":
            return masked_toml(text, (("package",),))
        case "pubspec.yaml":
            return masked_text(text, PUBSPEC_RE)
        case "config.py" | "settings.py":
            return masked_text(text, PY_RE)
    raise Refuse(f"{name} is not a version file")


def main() -> int:
    if len(sys.argv) != 4:
        print("usage: release-fits.py REV_OLD REV_NEW PATH", file=sys.stderr)
        return 1
    old_rev, new_rev, path = sys.argv[1:]
    name = Path(path).name
    try:
        old = parse(name, show(old_rev, path))
        new = parse(name, show(new_rev, path))
    except (Refuse, ValueError, subprocess.CalledProcessError, UnicodeDecodeError) as e:
        print(f"{path}: {e}", file=sys.stderr)
        return 1
    if not same(old, new):
        print(f"{path}: changes more than the root version", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
