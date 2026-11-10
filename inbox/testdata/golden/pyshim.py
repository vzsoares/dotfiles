# /// script
# requires-python = ">=3.11"
# dependencies = ["typer>=0.12"]
# ///
"""Runs inbox.py ($INBOX_PY) with its clock pinned to $ZEN_INBOX_NOW.

Used once to capture the golden outputs, before inbox.py was deleted:
  INBOX_PY=<path to inbox.py> go test -run TestGolden -golden-python \
    "uv run -q --script testdata/golden/pyshim.py"
"""

import importlib.util
import os
import sys
from datetime import datetime

spec = importlib.util.spec_from_file_location("zen_inbox", os.environ["INBOX_PY"])
assert spec is not None and spec.loader is not None
inbox = importlib.util.module_from_spec(spec)
sys.modules["zen_inbox"] = inbox
spec.loader.exec_module(inbox)
FIXED = datetime.fromisoformat(os.environ["ZEN_INBOX_NOW"])


class Frozen(datetime):
    @classmethod
    def now(cls, tz=None):  # type: ignore[override]
        return FIXED.astimezone(tz) if tz else FIXED.replace(tzinfo=None)


inbox.datetime = Frozen
inbox.app(prog_name="zen-inbox")
