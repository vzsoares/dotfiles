#!/usr/bin/env bash
# Offline toolkit check, run inside the box by `pi-box doctor`.
# Every check uses local fixtures; nothing touches the network.

FIX=/opt/pi-box/doctor/fixtures
TMP="$(mktemp -d)"
FAILS=0
SERVER=""
trap '[ -n "$SERVER" ] && kill "$SERVER" 2>/dev/null; rm -rf "$TMP"' EXIT

check() { # name, command...
  local name="$1"
  shift
  if "$@" >"$TMP/out" 2>&1; then
    echo "ok   $name"
  else
    echo "FAIL $name"
    sed 's/^/       /' "$TMP/out" | head -15
    FAILS=$((FAILS + 1))
  fi
}
contains() { # text, command...; the command's stdout must contain text
  local want="$1"
  shift
  "$@" 2>&1 | grep -q -- "$want"
}

for t in pi bun uv python curl wget jq htmlq rg fd yt-dlp ffmpeg ffprobe \
  sqlite3 pandoc git; do
  check "tool: $t" command -v "$t"
done

check "curl reads a file:// fixture" \
  contains "Doctor fixture" curl -fsS "file://$FIX/page.html"
check "htmlq selects text" \
  contains "Doctor fixture" htmlq --text '#title' <"$FIX/page.html"
check "htmlq selects an attribute" \
  contains "/next.html" htmlq --attribute href 'article a' <"$FIX/page.html"
check "jq filters json" \
  contains "2" jq '.a + 1' <<<'{"a":1}'
check "fd finds the fixture" contains "page.html" fd page "$FIX"
check "rg searches the fixture" contains "quick brown fox" rg fox "$FIX"
check "sqlite3 runs a query" \
  contains "42" sqlite3 :memory: 'select 6*7'
check "pandoc converts html to markdown" \
  contains "Doctor fixture" pandoc -f html -t markdown "$FIX/page.html"
check "yt-dlp runs" yt-dlp --version
check "ffmpeg makes and probes a tone" bash -c "
  ffmpeg -v error -f lavfi -i sine=d=1 '$TMP/t.wav' &&
  ffprobe -v error -show_entries format=duration -of csv=p=0 '$TMP/t.wav' |
    grep -q '^1'"
check "trafilatura extracts the article" python - "$FIX/page.html" <<'PY'
import sys, trafilatura
text = trafilatura.extract(open(sys.argv[1]).read())
assert text and "quick brown fox" in text and "menu" not in text, text
PY
check "readability-lxml extracts the article" python - "$FIX/page.html" <<'PY'
import sys
from readability import Document
assert "quick brown fox" in Document(open(sys.argv[1]).read()).summary()
PY
check "httpx and pandas import" python -c "import httpx, pandas"
check "chromium renders a js page" python - "$FIX/js.html" <<'PY'
import sys
from playwright.sync_api import sync_playwright
with sync_playwright() as p:
    b = p.chromium.launch()
    page = b.new_page()
    page.goto("file://" + sys.argv[1])
    page.wait_for_function("document.getElementById('out').textContent !== 'loading'")
    assert page.inner_text("#out") == "rendered-by-js"
    b.close()
PY
check "crawl4ai crawls a local page" python - "$FIX/page.html" <<'PY'
import asyncio, sys
from crawl4ai import AsyncWebCrawler

async def main():
    async with AsyncWebCrawler() as c:
        r = await c.arun("file://" + sys.argv[1])
        assert r.success and "quick brown fox" in r.markdown, r.error_message

asyncio.run(main())
PY
PORT=$((20000 + RANDOM % 20000))
python -m http.server "$PORT" --bind 127.0.0.1 --directory "$FIX/site" \
  >/dev/null 2>&1 &
SERVER=$!
sleep 1
SITE="http://127.0.0.1:$PORT"
lines() { wc -l <"$1" | tr -d ' '; }
crawl_depth() {
  CRAWL_DELAY_MS=50 pi-box-crawl "$SITE/" --depth 1 --out "$TMP/d1" &&
    [ "$(lines "$TMP/d1/index.jsonl")" = 3 ] && ! rg -q Gamma "$TMP/d1"
}
crawl_cap() {
  CRAWL_DELAY_MS=50 CRAWL_MAX_PAGES=2 pi-box-crawl "$SITE/" --depth 2 \
    --out "$TMP/cap" && [ "$(lines "$TMP/cap/index.jsonl")" = 2 ]
}
crawl_robots() {
  CRAWL_DELAY_MS=50 CRAWL_RESPECT_ROBOTS=1 pi-box-crawl "$SITE/" --depth 1 \
    --out "$TMP/rob"
  rg -q Alpha "$TMP/rob" && ! rg -q Beta "$TMP/rob"
}
check "pi-box-crawl follows links to the depth limit" crawl_depth
check "pi-box-crawl honors CRAWL_MAX_PAGES" crawl_cap
check "pi-box-crawl respects robots when asked" crawl_robots
check "cookies.txt converts to storageState" bash -c "
  pi-box-cookies2state '$FIX/cookies.txt' |
    jq -e '(.cookies | length == 2) and .cookies[0].secure and .cookies[1].httpOnly and .cookies[1].expires == -1'"
check "no git credentials" bash -c '! git config --get-regexp "^credential" | grep -q .'

echo
if [ "$FAILS" = 0 ]; then
  echo "doctor: all checks passed"
else
  echo "doctor: $FAILS failed"
  exit 1
fi
