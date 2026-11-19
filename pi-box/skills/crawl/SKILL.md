---
name: crawl
description: Crawl many pages of one site into markdown files with pi-box-crawl (crawl4ai). Use for site sections, docs trees and link-following; use fetch for one page, browser for logins.
---

# crawl

```bash
pi-box-crawl https://example.com/docs/ --depth 2 --out out/crawl
```

Writes `out/crawl/NNNN.md` per page plus `out/crawl/index.jsonl`
(`url`, `status`, `file`, `error`). Stays on the start URL's domain, breadth
first, and prints one line per page.

## Defaults (env; the user edits `config.env`, a run may override)

| Env | Default | Effect |
|---|---|---|
| `CRAWL_DELAY_MS` | 1000 | pause between pages |
| `CRAWL_CONCURRENCY` | 2 | parallel fetches |
| `CRAWL_MAX_PAGES` | 500 | hard page cap |
| `CRAWL_RESPECT_ROBOTS` | 0 | `1` skips pages robots.txt disallows |
| `CRAWL_USER_AGENT` | generic bot | sent to the site |

Set per run: `CRAWL_MAX_PAGES=50 CRAWL_RESPECT_ROBOTS=1 pi-box-crawl ...`.
Start with `--depth 1` and a small `CRAWL_MAX_PAGES`, read `index.jsonl`, then
widen.

## Notes

- Rows with `error` set failed; retry those URLs with `fetch` or `browser`.
  On 429/503, lower `CRAWL_CONCURRENCY` and raise `CRAWL_DELAY_MS`.
- crawl4ai flags tiny pages (under about 50 visible characters) as blocked,
  and script-only shells show up empty: use `browser` for those.
- Run one crawl at a time (2 GB of RAM).
- For custom extraction, use crawl4ai's Python API directly
  (`AsyncWebCrawler`, `CrawlerRunConfig`) and keep to the same defaults.
- Hand the markdown to `extract`; list sources and the page count in `RESULT.md`.
