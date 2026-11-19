---
name: fetch
description: Fetch one URL and get clean text. Use for a single page, an API response or a file download; use crawl for many pages and browser when the page needs JavaScript.
---

# fetch

Defaults come from env: `CRAWL_USER_AGENT`, `CRAWL_DELAY_MS` (default 1000),
`HTTP_PROXY`/`HTTPS_PROXY` (curl and httpx read them on their own).

## One page to text

```bash
UA="${CRAWL_USER_AGENT:-Mozilla/5.0 (compatible; pi-box/1.0)}"
curl -fsSL --retry 3 --retry-delay 2 --retry-all-errors -A "$UA" \
  "$URL" -o out/page.html
python - out/page.html <<'PY' > out/page.txt
import sys, trafilatura
html = open(sys.argv[1], encoding="utf-8", errors="replace").read()
print(trafilatura.extract(html, include_links=True, include_tables=True) or "")
PY
```

Empty output means the page is mostly script-rendered: use the `browser` skill.

## httpx, with backoff on 429/503

```python
import httpx, time

def get(url, tries=5):
    for n in range(tries):
        r = httpx.get(url, follow_redirects=True, timeout=30,
                      headers={"User-Agent": "Mozilla/5.0 (compatible; pi-box/1.0)"})
        if r.status_code not in (429, 503):
            return r
        time.sleep(float(r.headers.get("retry-after", 2 ** n)))
    r.raise_for_status()
```

Wait `CRAWL_DELAY_MS` between requests to the same domain.

## Rules

- Logged-in pages: `browser` skill (cookies from `../../in/cookies/<site>.txt`).
- `curl -b ../../in/cookies/<site>.txt` takes the Netscape file directly.
- Never read a browser profile or use `--cookies-from-browser`.
