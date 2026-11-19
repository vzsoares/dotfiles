---
name: browser
description: Drive headless Chromium with Playwright for JavaScript-heavy pages, scrolling, waiting on selectors and logged-in sites. Use when fetch returns an empty or skeleton page.
---

# browser

Chromium runs headless only (Playwright's headless shell); there is no display.
Save the rendered HTML to `out/`, then parse it with `extract`.

## Render a page

```python
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch()
    ctx = browser.new_context(user_agent="Mozilla/5.0 (compatible; pi-box/1.0)")
    page = ctx.new_page()
    page.goto(URL, wait_until="domcontentloaded", timeout=60000)
    page.wait_for_selector("main, article, #content", timeout=30000)
    page.wait_for_load_state("networkidle")
    for _ in range(10):                      # lazy-loaded lists
        page.mouse.wheel(0, 4000)
        page.wait_for_timeout(500)
    open("out/page.html", "w").write(page.content())
    browser.close()
```

Replace the selector with one that exists on the target. On a timeout, save
`page.content()` anyway and read it before retrying.

## Logged-in sites

The user exports a site's cookies to `../../in/cookies/<site>.txt` (Netscape
format). Convert once, then load as `storageState`:

```bash
pi-box-cookies2state ../../in/cookies/<site>.txt > out/state.json
```

```python
ctx = browser.new_context(storage_state="out/state.json")
```

Delete `out/state.json` before finishing: it holds session cookies.
Never look for a browser profile; none is mounted.

## Notes

- Wait `CRAWL_DELAY_MS` between page loads on one domain.
- Capture JSON endpoints a page calls with `page.on("response", ...)` and
  fetch those directly next time.
- The box has 2 GB of RAM: one context at a time, close the browser at the end.
